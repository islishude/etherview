import { createHash } from "node:crypto";
import { readFileSync, statSync } from "node:fs";

const [path] = process.argv.slice(2);
if (!path?.startsWith("/var/lib/etherview/compilers/cache/solidity-sha256-")) {
  throw new Error("invalid compiler cache inspection path");
}
const stat = statSync(path, { bigint: true });
process.stdout.write(
  `ETHERVIEW_CACHE_INSPECTION_V1=${JSON.stringify({
    path,
    sha256: createHash("sha256").update(readFileSync(path)).digest("hex"),
    size: stat.size.toString(),
    inode: stat.ino.toString(),
    mode: Number(stat.mode & 0o777n).toString(8).padStart(3, "0"),
    modified_ns: stat.mtimeNs.toString(),
    changed_ns: stat.ctimeNs.toString(),
  })}\n`,
);
