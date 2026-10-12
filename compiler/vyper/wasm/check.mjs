// Authenticated offline candidate comparison; fresh-process acceptance remains separate.
import { execFileSync } from "node:child_process";
import { join } from "node:path";
import { writeFileSync } from "node:fs";
import { ROOT, HERE, versions } from "./tooling.mjs";
import { bundle } from "./packages.mjs";
execFileSync(process.execPath, [join(HERE, "prepare.mjs")], { cwd: ROOT, stdio: "inherit" });
const reports = [];
for (const item of versions()) {
  const destination = join(ROOT, ".local/vyper-wasm/packages", item.version);
  await bundle(item, destination);
  const output = execFileSync(
    process.execPath,
    [
      "--permission",
      "--allow-fs-read=" + ROOT,
      "--no-addons",
      "--max-old-space-size=128",
      "--wasm-max-mem-pages=6144",
      join(HERE, "matrix.mjs"),
      join(ROOT, ".local/vyper-wasm/shared"),
      destination,
      join(ROOT, "internal/verify/testdata/compiler/vyper/versions", item.version),
      item.version,
    ],
    { cwd: ROOT, timeout: 120000, maxBuffer: 1 << 20 },
  );
  const report = JSON.parse(output);
  reports.push(report);
  console.log(item.version, report.cases, "cases passed");
}
writeFileSync(
  join(ROOT, ".local/vyper-wasm/matrix-results.json"),
  JSON.stringify(reports, null, 2) + "\n",
);
