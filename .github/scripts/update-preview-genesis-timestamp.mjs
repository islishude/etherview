import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { pathToFileURL } from "node:url";

const UINT64_MAX = 0xffffffffffffffffn;
const timestampFieldPattern = /("timestamp"\s*:\s*)"(0x[0-9a-fA-F]+)"/g;

function normalizeUnixSeconds(value) {
  let seconds;
  if (typeof value === "bigint") {
    seconds = value;
  } else if (typeof value === "number" && Number.isSafeInteger(value)) {
    seconds = BigInt(value);
  } else {
    throw new Error("timestamp must be a non-negative integer Unix timestamp");
  }
  if (seconds < 0n || seconds > UINT64_MAX) {
    throw new Error("timestamp must fit in an unsigned 64-bit quantity");
  }
  return seconds;
}

function readGenesis(sourcePath) {
  const source = fs.readFileSync(sourcePath, "utf8");
  let document;
  try {
    document = JSON.parse(source);
  } catch (error) {
    throw new Error(`Genesis JSON is invalid: ${error.message}`);
  }
  if (document === null || typeof document !== "object" || Array.isArray(document)) {
    throw new Error("Genesis JSON must contain an object");
  }
  if (typeof document.timestamp !== "string") {
    throw new Error("Genesis JSON must contain a string timestamp");
  }
  const matches = [...source.matchAll(timestampFieldPattern)];
  if (matches.length !== 1 || matches[0][2] !== document.timestamp) {
    throw new Error("Genesis JSON must contain exactly one unescaped timestamp field");
  }
  try {
    normalizeUnixSeconds(BigInt(document.timestamp));
  } catch (error) {
    throw new Error(`Genesis timestamp is invalid: ${error.message}`);
  }
  return { source, match: matches[0] };
}

export function writeGenesisWithTimestamp(
  sourcePath,
  destinationPath,
  unixSeconds = Math.floor(Date.now() / 1000),
) {
  const templatePath = path.resolve(sourcePath);
  const targetPath = path.resolve(destinationPath);
  if (templatePath === targetPath) {
    throw new Error("Genesis template and runtime output must be different files");
  }
  const { source, match } = readGenesis(templatePath);
  const next = normalizeUnixSeconds(unixSeconds);
  const nextText = `0x${next.toString(16)}`;
  const replacement = `${match[1]}"${nextText}"`;
  const updated =
    source.slice(0, match.index) + replacement + source.slice(match.index + match[0].length);

  let previous;
  try {
    previous = fs.readFileSync(targetPath, "utf8");
  } catch (error) {
    if (error.code !== "ENOENT") {
      throw error;
    }
  }
  const changed = previous !== updated;
  if (changed) {
    fs.mkdirSync(path.dirname(targetPath), { recursive: true });
    fs.writeFileSync(targetPath, updated, "utf8");
  }
  return { changed, timestamp: nextText };
}

function main() {
  const [, , sourcePath, destinationPath] = process.argv;
  if (!sourcePath || !destinationPath) {
    throw new Error(
      "usage: update-preview-genesis-timestamp.mjs <template-genesis-file> <runtime-genesis-file>",
    );
  }
  const result = writeGenesisWithTimestamp(sourcePath, destinationPath);
  console.log(
    `preview-genesis: ${result.changed ? "wrote" : "reused"} ${path.resolve(destinationPath)} (${result.timestamp})`,
  );
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  try {
    main();
  } catch (error) {
    console.error(`preview-genesis: ${error.message}`);
    process.exitCode = 1;
  }
}
