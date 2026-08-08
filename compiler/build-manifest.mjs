import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  lstatSync,
  readFileSync,
  readdirSync,
  realpathSync,
  writeFileSync,
} from "node:fs";
import { join, relative, resolve, sep } from "node:path";

const [nodePathArgument, runtimeRootArgument, outputPathArgument] =
  process.argv.slice(2);
if (!nodePathArgument || !runtimeRootArgument || !outputPathArgument) {
  throw new Error("usage: build-manifest NODE RUNTIME_ROOT OUTPUT");
}

const nodePath = realpathSync(resolve(nodePathArgument));
const runtimeRoot = realpathSync(resolve(runtimeRootArgument));
const outputPath = resolve(outputPathArgument);

function sha256(path) {
  return createHash("sha256").update(readFileSync(path)).digest("hex");
}

function collect(root, current = root) {
  const result = [];
  for (const name of readdirSync(current).sort()) {
    const path = join(current, name);
    if (resolve(path) === outputPath) {
      continue;
    }
    const info = lstatSync(path);
    if (info.isSymbolicLink()) {
      throw new Error(`runtime manifest refuses symbolic link ${path}`);
    }
    if (info.isDirectory()) {
      result.push(...collect(root, path));
      continue;
    }
    if (!info.isFile()) {
      throw new Error(`runtime manifest refuses non-file ${path}`);
    }
    const relativePath = relative(root, path).split(sep).join("/");
    result.push({ path, logical_path: `runtime/${relativePath}`, sha256: sha256(path) });
  }
  return result;
}

const nodeVersion = execFileSync(nodePath, ["--version"], {
  encoding: "utf8",
  env: {},
}).trim();

const manifest = {
  schema: "etherview-solcjs-runtime-v1",
  node_version: nodeVersion,
  wrapper_package: "solc@0.8.36",
  files: [
    { path: nodePath, logical_path: "node", sha256: sha256(nodePath) },
    ...collect(runtimeRoot).sort((left, right) =>
      left.logical_path < right.logical_path
        ? -1
        : left.logical_path > right.logical_path
          ? 1
          : 0,
    ),
  ],
};

writeFileSync(outputPath, `${JSON.stringify(manifest)}\n`, {
  encoding: "utf8",
  mode: 0o444,
});
