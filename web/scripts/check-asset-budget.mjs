import { readFileSync } from "node:fs";
import { gzipSync } from "node:zlib";
import { dirname, join, relative, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { assetManifestName, assetManifestSchema } from "./prepare-assets.mjs";

const expectedSchema = "etherview-web-asset-budget-v1";

function inside(root, candidate) {
  const child = relative(root, candidate);
  return child !== ".." && !child.startsWith(`..${sep}`) && !child.startsWith(sep);
}

function boundedPositiveInteger(value, name) {
  if (!Number.isSafeInteger(value) || value < 1) {
    throw new Error(`${name} must be a positive safe integer`);
  }
  return value;
}

export function measureInitialAssets(distributionPath, budgetPath) {
  const distribution = resolve(distributionPath);
  const budgetFile = resolve(budgetPath);
  const budget = JSON.parse(readFileSync(budgetFile, "utf8"));
  if (budget.schema !== expectedSchema) {
    throw new Error("unsupported Web asset budget schema");
  }
  const maximumRaw = boundedPositiveInteger(
    budget.maximumInitialRawBytes,
    "maximumInitialRawBytes",
  );
  const maximumGzip = boundedPositiveInteger(
    budget.maximumInitialGzipBytes,
    "maximumInitialGzipBytes",
  );
  if (
    !Array.isArray(budget.forbiddenInitialPatterns) ||
    budget.forbiddenInitialPatterns.some(
      (value) => typeof value !== "string" || value.length === 0 || value.length > 128,
    )
  ) {
    throw new Error("forbiddenInitialPatterns must contain bounded strings");
  }

  const indexPath = join(distribution, "index.html");
  const index = readFileSync(indexPath);
  const manifest = JSON.parse(readFileSync(join(distribution, assetManifestName), "utf8"));
  if (manifest.schema !== assetManifestSchema || typeof manifest.assets !== "object") {
    throw new Error("invalid Web asset manifest");
  }
  const references = new Set(["index.html"]);
  for (const match of index.toString("utf8").matchAll(/(?:src|href)="([^"]+)"/gu)) {
    const reference = match[1];
    if (!reference.startsWith("/assets/")) continue;
    if (reference.includes("?") || reference.includes("#") || reference.includes("\\")) {
      throw new Error(`unsafe initial asset reference ${reference}`);
    }
    references.add(reference.slice(1));
  }

  let rawBytes = 0;
  let gzipBytes = 0;
  const files = [...references].sort();
  for (const reference of files) {
    const target = resolve(distribution, reference);
    if (!inside(distribution, target)) {
      throw new Error(`initial asset escapes distribution: ${reference}`);
    }
    for (const pattern of budget.forbiddenInitialPatterns) {
      if (reference.includes(pattern)) {
        throw new Error(`forbidden initial asset ${reference}`);
      }
    }
    const contents = readFileSync(target);
    rawBytes += contents.length;
    if (reference === "index.html") {
      gzipBytes += gzipSync(contents, { level: 9 }).length;
      continue;
    }
    const metadata = manifest.assets[reference];
    if (
      metadata?.identity?.path !== reference ||
      metadata.identity.bytes !== contents.length ||
      typeof metadata.gzip?.path !== "string" ||
      typeof metadata.br?.path !== "string"
    ) {
      throw new Error(`initial asset lacks precompressed metadata: ${reference}`);
    }
    const gzipPath = resolve(distribution, metadata.gzip.path);
    const brotliPath = resolve(distribution, metadata.br.path);
    if (!inside(distribution, gzipPath) || !inside(distribution, brotliPath)) {
      throw new Error(`compressed initial asset escapes distribution: ${reference}`);
    }
    if (
      readFileSync(gzipPath).length !== metadata.gzip.bytes ||
      readFileSync(brotliPath).length !== metadata.br.bytes
    ) {
      throw new Error(`compressed initial asset metadata differs: ${reference}`);
    }
    gzipBytes += metadata.gzip.bytes;
  }
  if (rawBytes > maximumRaw) {
    throw new Error(`initial raw assets ${rawBytes} exceed ${maximumRaw}`);
  }
  if (gzipBytes > maximumGzip) {
    throw new Error(`initial gzip assets ${gzipBytes} exceed ${maximumGzip}`);
  }
  return { files, rawBytes, gzipBytes };
}

function main() {
  const scriptDirectory = dirname(fileURLToPath(import.meta.url));
  const webRoot = resolve(scriptDirectory, "..");
  const result = measureInitialAssets(
    process.argv[2] ?? join(webRoot, "dist"),
    process.argv[3] ?? join(webRoot, "asset-budget.json"),
  );
  process.stdout.write(
    `web-asset-budget: files=${result.files.length} raw=${result.rawBytes} gzip=${result.gzipBytes}\n`,
  );
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main();
}
