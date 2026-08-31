import {
  mkdirSync,
  readFileSync,
  readdirSync,
  statSync,
  writeFileSync,
} from "node:fs";
import { createHash } from "node:crypto";
import { dirname, join, relative, resolve, sep } from "node:path";
import {
  brotliCompressSync,
  constants as zlibConstants,
  gzipSync,
} from "node:zlib";
import { fileURLToPath, pathToFileURL } from "node:url";

export const assetManifestName = "asset-manifest.json";
export const assetManifestSchema = "etherview-web-assets-v1";

const hashedAssetPattern = /^assets\/[^/]+-[A-Za-z0-9_-]{8}\.[A-Za-z0-9]+$/u;
const compressibleExtensions = new Set([".css", ".html", ".js", ".json", ".svg", ".txt", ".wasm"]);

function filesUnder(root, directory = root) {
  const result = [];
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const target = join(directory, entry.name);
    if (entry.isDirectory()) result.push(...filesUnder(root, target));
    else if (entry.isFile()) result.push(relative(root, target).split(sep).join("/"));
  }
  return result;
}

function digest(contents) {
  return createHash("sha256").update(contents).digest("hex");
}

function representation(path, contents) {
  return { path, bytes: contents.length, sha256: digest(contents) };
}

function extension(name) {
  const index = name.lastIndexOf(".");
  return index < 0 ? "" : name.slice(index).toLowerCase();
}

export function prepareDistribution(distributionPath) {
  const distribution = resolve(distributionPath);
  if (!statSync(distribution).isDirectory()) {
    throw new Error("Web distribution is not a directory");
  }
  const assets = {};
  for (const name of filesUnder(distribution).sort()) {
    if (name === assetManifestName || name.endsWith(".gz") || name.endsWith(".br")) continue;
    const target = resolve(distribution, name);
    const contents = readFileSync(target);
    const metadata = { identity: representation(name, contents) };
    if (hashedAssetPattern.test(name) && compressibleExtensions.has(extension(name))) {
      const gzip = gzipSync(contents, { level: 9 });
      const brotli = brotliCompressSync(contents, {
        params: { [zlibConstants.BROTLI_PARAM_QUALITY]: 11 },
      });
      if (gzip.length < contents.length) {
        const gzipName = `${name}.gz`;
        mkdirSync(dirname(resolve(distribution, gzipName)), { recursive: true });
        writeFileSync(resolve(distribution, gzipName), gzip);
        metadata.gzip = representation(gzipName, gzip);
      }
      if (brotli.length < contents.length) {
        const brotliName = `${name}.br`;
        mkdirSync(dirname(resolve(distribution, brotliName)), { recursive: true });
        writeFileSync(resolve(distribution, brotliName), brotli);
        metadata.br = representation(brotliName, brotli);
      }
    }
    assets[name] = metadata;
  }
  const manifest = { schema: assetManifestSchema, assets };
  writeFileSync(
    join(distribution, assetManifestName),
    `${JSON.stringify(manifest, null, 2)}\n`,
  );
  return manifest;
}

function main() {
  const scriptDirectory = dirname(fileURLToPath(import.meta.url));
  const webRoot = resolve(scriptDirectory, "..");
  const manifest = prepareDistribution(process.argv[2] ?? join(webRoot, "dist"));
  const compressed = Object.values(manifest.assets).filter((asset) => asset.br || asset.gzip).length;
  process.stdout.write(
    `web-assets: files=${Object.keys(manifest.assets).length} compressed=${compressed}\n`,
  );
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main();
}
