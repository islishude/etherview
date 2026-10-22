import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { asSummary, init } from "license-checker-rseidelsohn";

const allowed = "0BSD;Apache-2.0;BSD-2-Clause;BSD-3-Clause;ISC;MIT;MPL-2.0;Unlicense";

export async function checkLicenses(root, workspace = "web") {
  // npm resolves the workspace's production closure, including hoisted peers.
  // The checker cannot resolve that closure when started inside a workspace.
  const tree = JSON.parse(
    execFileSync(
      "npm",
      ["ls", "--workspace", workspace, "--omit=dev", "--all", "--json", "--long"],
      {
        cwd: root,
        encoding: "utf8",
        stdio: ["ignore", "pipe", "pipe"],
        timeout: 30_000,
        maxBuffer: 16 << 20,
      },
    ),
  );
  const { name } = JSON.parse(readFileSync(join(root, workspace, "package.json")));
  const installed = tree.dependencies?.[name];
  assert.ok(installed, `${workspace} workspace is missing from the installed dependency tree`);
  const packages = new Set();
  function visit(node) {
    for (const [name, dependency] of Object.entries(node.dependencies ?? {})) {
      // npm includes empty entries for uninstalled optional dependencies.
      if (!dependency.version) continue;
      packages.add(`${dependency.name ?? name}@${dependency.version}`);
      visit(dependency);
    }
  }
  visit(installed);
  assert.ok(packages.size > 0, `${workspace} production dependency tree is empty`);
  const licenses = await new Promise((resolve, reject) => {
    init(
      { start: root, includePackages: [...packages].join(";"), onlyAllow: allowed },
      (error, result) => (error ? reject(error) : resolve(result)),
    );
  });
  for (const name of packages) {
    assert.ok(licenses[name]?.licenses, `license scan omitted ${name}`);
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
