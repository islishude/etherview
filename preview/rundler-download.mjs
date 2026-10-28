import { execFileSync } from "node:child_process";
import crypto from "node:crypto";
// Fetch only reviewed release archives; a cached archive is checked on every build.
import fs from "node:fs";
import path from "node:path";
const release = JSON.parse(fs.readFileSync(new URL("rundler-release.json", import.meta.url)));
const arch = process.argv[2];
const item = release.linux[arch];
if (!item) throw new Error("Unsupported Rundler Linux architecture");
const directory = path.resolve(".local/rundler", arch);
fs.mkdirSync(directory, { recursive: true });
const archive = path.join(directory, "release.tar.gz");
if (!fs.existsSync(archive)) {
  const url = `https://github.com/${release.repository}/releases/download/v${release.version}/rundler-v${release.version}-${item.target}.tar.gz`;
  const temporary = archive + ".tmp";
  try {
    execFileSync(
      "curl",
      [
        "--fail",
        "--location",
        "--connect-timeout",
        "15",
        "--max-time",
        "180",
        "--retry",
        "2",
        "--output",
        temporary,
        url,
      ],
      { stdio: "inherit" },
    );
    if (
      crypto.createHash("sha256").update(fs.readFileSync(temporary)).digest("hex") !== item.sha256
    )
      throw new Error("Rundler release checksum mismatch");
    fs.renameSync(temporary, archive);
  } finally {
    fs.rmSync(temporary, { force: true });
  }
}
if (crypto.createHash("sha256").update(fs.readFileSync(archive)).digest("hex") !== item.sha256)
  throw new Error("Rundler cached release checksum mismatch");
execFileSync("tar", ["-xzf", archive, "-C", directory, "rundler"]);
fs.chmodSync(path.join(directory, "rundler"), 0o755);
