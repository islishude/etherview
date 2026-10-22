import { createHash } from "node:crypto";
import { readFileSync, realpathSync, statSync } from "node:fs";
import { extname, isAbsolute, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";
import type { Plugin } from "vite";

const repositoryRoot = fileURLToPath(new URL("../../", import.meta.url));
const defaultIcon = resolve(repositoryRoot, "web/src/assets/etherview-mark.svg");
const virtualID = "virtual:etherview-branding";
const resolvedID = `\0${virtualID}`;
const fields = new Set(["name", "title", "description", "icon", "favicon", "logo"]);

function fail(message: string): never {
  throw new Error(`web-branding: ${message}`);
}

function readBounded(file: string, limit: number, label: string): Buffer {
  try {
    const info = statSync(file);
    if (!info.isFile() || info.size === 0 || info.size > limit) {
      fail(`${label} must be a nonempty regular file of at most ${limit} bytes`);
    }
    return readFileSync(file);
  } catch (error) {
    if (error instanceof Error && error.message.startsWith("web-branding:")) throw error;
    return fail(`cannot read ${label}`);
  }
}

function imageAsset(file: string, label: string) {
  const extension = extname(file).toLowerCase();
  if (extension !== ".svg" && extension !== ".png") fail(`${label} must be SVG or PNG`);
  const bytes = readBounded(file, 1024 * 1024, label);
  const valid =
    extension === ".png"
      ? bytes.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]))
      : /^(?:\uFEFF)?\s*(?:<\?xml[^>]*\?>\s*)?(?:<!--[\s\S]*?-->\s*)*<svg(?:\s|\/?>)/u.test(
          bytes.toString("utf8"),
        );
  if (!valid) fail(`${label} contents do not match its image format`);
  const hash = createHash("sha256").update(bytes).digest("hex").slice(0, 8);
  return {
    file,
    bytes,
    fileName: `assets/brand-${hash}${extension}`,
    url: `/assets/brand-${hash}${extension}`,
    type: extension === ".svg" ? "image/svg+xml" : "image/png",
  };
}

export function loadBranding(directory = process.env.ETHERVIEW_WEB_BRANDING_DIR) {
  const selected = directory || "web/branding/default";
  let root: string;
  try {
    root = realpathSync(resolve(repositoryRoot, selected));
  } catch {
    return fail("branding directory does not exist");
  }
  const configPath = resolve(root, "brand.json");
  const contents = readBounded(configPath, 16 * 1024, "brand.json");
  let config: Record<string, unknown>;
  try {
    config = JSON.parse(contents.toString("utf8"));
  } catch {
    return fail("brand.json must contain valid JSON");
  }
  if (!config || typeof config !== "object" || Array.isArray(config)) {
    fail("brand.json must contain an object");
  }
  for (const [key, value] of Object.entries(config)) {
    if (!fields.has(key)) fail(`unknown field ${key}`);
    if (typeof value !== "string" || !value.trim() || /[\u0000-\u001f\u007f]/u.test(value)) {
      fail(`${key} must be a nonempty string without control characters`);
    }
  }
  const values = config as Partial<
    Record<"name" | "title" | "description" | "icon" | "favicon" | "logo", string>
  >;
  const name = values.name ?? "Etherview";
  const title = values.title ?? name;
  const description = values.description ?? `${name} execution-layer block explorer`;
  for (const [key, value, limit] of [
    ["name", name, 80],
    ["title", title, 160],
    ["description", description, 512],
  ] as const) {
    if (Array.from(value).length > limit) fail(`${key} exceeds ${limit} characters`);
  }
  function imagePath(value: string | undefined, label: string): string {
    if (value === undefined) return defaultIcon;
    if (isAbsolute(value) || value.includes("\\") || /^[a-z][a-z0-9+.-]*:/iu.test(value)) {
      return fail(`${label} must be a relative file inside the branding directory`);
    }
    let file: string;
    try {
      file = realpathSync(resolve(root, value));
    } catch {
      return fail(`cannot read ${label}`);
    }
    const local = relative(root, file);
    if (local === ".." || local.startsWith(`..${sep}`) || isAbsolute(local)) {
      fail(`${label} must stay inside the branding directory`);
    }
    return file;
  }
  // Validate every supplied image, including a shared icon overridden by both consumers.
  const images = new Map<string, ReturnType<typeof imageAsset>>();
  for (const key of ["icon", "favicon", "logo"] as const) {
    if (values[key] !== undefined) images.set(key, imageAsset(imagePath(values[key], key), key));
  }
  const fallback = () => images.get("icon") ?? imageAsset(defaultIcon, "default icon");
  const favicon = images.get("favicon") ?? fallback();
  const logo = images.get("logo") ?? fallback();
  return {
    configPath,
    assets: [...new Map([favicon, logo].map((asset) => [asset.url, asset])).values()],
    brand: {
      name,
      title,
      description,
      favicon: favicon.url,
      faviconType: favicon.type,
      logo: logo.url,
    },
  };
}

export function brandingPlugin(directory?: string): Plugin {
  const { brand, assets, configPath } = loadBranding(directory);
  const htmlValues: Record<string, string> = {
    TITLE: brand.title,
    DESCRIPTION: brand.description,
  };
  return {
    name: "etherview-branding",
    resolveId(id) {
      if (id === virtualID) return resolvedID;
    },
    load(id) {
      if (id === resolvedID) return `export const brand = ${JSON.stringify(brand)};`;
    },
    buildStart() {
      this.addWatchFile(configPath);
      for (const asset of assets) this.addWatchFile(asset.file);
    },
    transformIndexHtml: {
      order: "post",
      handler(html) {
        const transformed = html.replace(/__BRAND_(TITLE|DESCRIPTION)__/gu, (_, key: string) =>
          htmlValues[key]!.replace(
            /[&<>"']/gu,
            (character) =>
              ({
                "&": "&amp;",
                "<": "&lt;",
                ">": "&gt;",
                '"': "&quot;",
                "'": "&#39;",
              })[character]!,
          ),
        );
        return {
          html: transformed,
          tags: [
            {
              tag: "link",
              attrs: { rel: "icon", type: brand.faviconType, href: brand.favicon },
              injectTo: "head",
            },
            ...(brand.logo === brand.favicon
              ? []
              : [
                  {
                    tag: "link",
                    attrs: { rel: "preload", as: "image", href: brand.logo },
                    injectTo: "head" as const,
                  },
                ]),
          ],
        };
      },
    },
    generateBundle() {
      for (const asset of assets) {
        this.emitFile({ type: "asset", fileName: asset.fileName, source: asset.bytes });
      }
    },
    configureServer(server) {
      server.middlewares.use((request, response, next) => {
        const asset = assets.find((candidate) => candidate.url === request.url?.split("?")[0]);
        if (!asset) return next();
        if (request.method !== "GET" && request.method !== "HEAD") {
          response.statusCode = 405;
          response.setHeader("Allow", "GET, HEAD");
          response.end();
          return;
        }
        response.setHeader("Content-Type", asset.type);
        response.setHeader("Cache-Control", "no-store");
        response.end(request.method === "HEAD" ? undefined : asset.bytes);
      });
    },
  };
}
