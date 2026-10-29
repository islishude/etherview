import { createRequire } from "node:module";

const require = createRequire(import.meta.url);

// Preserve the pinned ENS source-unit names (and therefore compiler metadata)
// while resolving its v4/v5 imports through our installed package names.
export function resolveENSImport(source) {
  const installed = source.startsWith("@openzeppelin/contracts/")
    ? source.replace("@openzeppelin/contracts/", "@openzeppelin/contracts-v4/")
    : source.replace("@openzeppelin/contracts-v5/", "@openzeppelin/contracts/");
  return require.resolve(installed);
}
