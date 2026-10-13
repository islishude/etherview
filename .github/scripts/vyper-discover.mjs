import { execFileSync } from "node:child_process";
import { appendFileSync } from "node:fs";
import { join } from "node:path";
import { validVersion } from "../../compiler/vyper/update.mjs";
import { ROOT } from "../../compiler/vyper/wasm/tooling.mjs";

export function publishDiscovery({ versions, requested, base, output, summary }) {
  if (!Array.isArray(versions) || !versions.every(validVersion))
    throw new Error("Invalid discovered compiler versions");
  if (requested) {
    if (!validVersion(requested) || !versions.includes(requested))
      throw new Error("Not a new stable PyPI compiler version");
    versions = [requested];
  }
  if (!/^[a-f0-9]{40}$/.test(base)) throw new Error("Invalid discovery base commit");
  appendFileSync(output, `versions=${JSON.stringify(versions)}\nbase=${base}\n`);
  appendFileSync(summary, `New stable versions: ${versions.join(", ") || "none"}.\n`);
}

if (import.meta.main) {
  const versions = JSON.parse(
    execFileSync(process.execPath, [join(ROOT, "compiler/vyper/update.mjs"), "--discover"], {
      cwd: ROOT,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "inherit"],
      timeout: 600000,
    }),
  );
  publishDiscovery({
    versions,
    requested: process.env.REQUESTED_VERSION,
    base: execFileSync("git", ["rev-parse", "HEAD"], { cwd: ROOT, encoding: "utf8" }).trim(),
    output: process.env.GITHUB_OUTPUT,
    summary: process.env.GITHUB_STEP_SUMMARY,
  });
}
