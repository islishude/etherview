import { execFileSync, spawnSync } from "node:child_process";

const forge = process.env.FORGE || "forge";
const expected = process.env.FOUNDRY_VERSION;
try {
  const version = execFileSync(forge, ["--version"], { encoding: "utf8" });
  if (!expected || version.split(/\r?\n/)[0] !== `forge Version: ${expected}`)
    throw new Error(`Forge ${expected} is required; found ${version.split(/\r?\n/)[0]}`);
  const files = execFileSync("git", ["ls-files", "-z", "--", "*.sol"], {
    encoding: "utf8",
  })
    .split("\0")
    .filter(Boolean);
  // An empty path list would make Forge discover its own default source tree.
  if (files.length) {
    const result = spawnSync(forge, ["fmt", ...process.argv.slice(2), ...files], {
      stdio: "inherit",
    });
    if (result.error) throw result.error;
    process.exitCode = result.status ?? 1;
  }
} catch (error) {
  console.error(
    `solidity-format: ${error.message}; install Foundry ${expected} and ensure FORGE is executable`,
  );
  process.exitCode = 1;
}
