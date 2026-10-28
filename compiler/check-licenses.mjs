// Read-only verification of checked-in notices against the installed compiler tree.
import assert from "node:assert/strict";
import { existsSync, readdirSync, readFileSync, realpathSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

export function compilerNotices(root = dirname(fileURLToPath(import.meta.url))) {
  const visited = new Set();
  const notices = new Map();
  function visit(directory, includeSelf = true) {
    directory = realpathSync(directory);
    if (visited.has(directory)) return;
    visited.add(directory);
    const manifest = JSON.parse(readFileSync(join(directory, "package.json")));
    if (includeSelf) {
      function scan(path) {
        for (const entry of readdirSync(path, { withFileTypes: true })) {
          if (entry.name === "node_modules") continue;
          const source = join(path, entry.name);
          if (entry.isDirectory()) scan(source);
          else if (entry.isFile() && /^(license|copying)(\.(txt|md))?$/i.test(entry.name)) {
            const name = `${manifest.name.replaceAll("/", "+")}@${manifest.version}/${relative(directory, source)}`;
            notices.set(name, source);
          }
        }
      }
      scan(directory);
    }
    const loader = createRequire(join(directory, "package.json"));
    const required = {
      ...manifest.dependencies,
      ...(!includeSelf ? { esbuild: manifest.devDependencies.esbuild } : {}),
    };
    const optional = manifest.optionalDependencies ?? {};
    for (const name of Object.keys({ ...manifest.peerDependencies, ...required, ...optional })) {
      const path = loader.resolve
        .paths(name)
        ?.map((base) => join(base, name))
        .find((base) => existsSync(join(base, "package.json")));
      assert.ok(
        path || !(name in required) || name in optional,
        `missing required dependency ${name}`,
      );
      if (path) visit(path);
    }
  }
  visit(root, false);
  assert.ok(notices.size > 0, "compiler notice inventory is empty");
  return notices;
}

export function checkLicenses(root, nodeLicense) {
  for (const [name, source] of compilerNotices()) {
    assert.ok(
      readFileSync(join(root, "dependencies", name)).equals(readFileSync(source)),
      `checked-in compiler notice differs: ${name}`,
    );
  }
  for (const [name, source] of [
    ["solc-LICENSE", import.meta.resolve("solc/LICENSE")],
    ["esbuild-LICENSE.md", import.meta.resolve("esbuild/LICENSE.md")],
  ]) {
    assert.ok(readFileSync(join(root, name)).equals(readFileSync(new URL(source))), name);
  }
  if (nodeLicense) {
    assert.ok(
      readFileSync(join(root, "node-LICENSE.txt")).equals(readFileSync(nodeLicense)),
      "checked-in Node notice differs from builder",
    );
  }
}

if (import.meta.main) {
  const root = resolve(
    process.argv[2] ?? fileURLToPath(new URL("../licenses/solcjs-runtime", import.meta.url)),
  );
  checkLicenses(root, process.argv[3]);
  console.log("checked-in compiler notices: PASS");
}
