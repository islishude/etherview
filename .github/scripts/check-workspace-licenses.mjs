import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { existsSync, readFileSync, realpathSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { asSummary, init } from "license-checker-rseidelsohn";

const allowed = "0BSD;Apache-2.0;BSD-2-Clause;BSD-3-Clause;ISC;MIT;MPL-2.0;Unlicense";

export async function checkLicenses(root, workspace = "web") {
  // Resolve from each owning package's real location, including pnpm symlinks.
  // Validate required dependencies independently: the license scanner may omit
  // missing entries. Optional dependencies and absent peers need not be installed.
  const packages = new Map();
  const visited = new Set();
  function visit(directory, includeSelf = true) {
    directory = realpathSync(directory);
    if (visited.has(directory)) return;
    visited.add(directory);
    const manifest = JSON.parse(readFileSync(join(directory, "package.json")));
    if (includeSelf) packages.set(directory, `${manifest.name}@${manifest.version}`);
    const loader = createRequire(join(directory, "package.json"));
    const required = manifest.dependencies ?? {};
    const optional = manifest.optionalDependencies ?? {};
    const peers = manifest.peerDependencies ?? {};
    for (const name of Object.keys({ ...peers, ...required, ...optional })) {
      const path = loader.resolve
        .paths(name)
        ?.map((base) => join(base, name))
        .find((base) => existsSync(join(base, "package.json")));
      if (!path) {
        assert.ok(
          !(name in required) || name in optional,
          `missing required dependency ${name} in ${manifest.name}`,
        );
        continue;
      }
      visit(path);
    }
  }
  visit(join(root, workspace), false);
  assert.ok(packages.size > 0, `${workspace} production dependency tree is empty`);
  const licenses = {};
  for (const [directory, name] of packages) {
    // Scan each real package directly: a root scan can miss nested optional
    // dependencies in pnpm's virtual store. Keep license detection and policy
    // enforcement in the pinned checker, including license-file inference.
    const result = await new Promise((resolve, reject) => {
      init(
        {
          start: directory,
          direct: 0,
          includePackages: name,
          onlyAllow: allowed,
          ...(existsSync(join(root, "licenses/npm-clarifications.json"))
            ? { clarificationsFile: join(root, "licenses/npm-clarifications.json") }
            : {}),
        },
        (error, result) => (error ? reject(error) : resolve(result)),
      );
    });
    assert.ok(result[name]?.licenses, `license scan omitted ${name}`);
    Object.assign(licenses, result);
  }
  return licenses;
}

if (import.meta.main) {
  const workspace = process.argv[2];
  assert.ok(workspace, "usage: check-workspace-licenses.mjs <workspace>");
  const licenses = await checkLicenses(
    fileURLToPath(new URL("../../", import.meta.url)),
    workspace,
  );
  console.log(`${workspace}-license-check: ${Object.keys(licenses).length} production packages`);
  console.log(asSummary(licenses));
}
