import { spawnSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";

const root = path.resolve(import.meta.dirname, "../..");

// COMPOSE is a trusted operator command, just as in Make. Pass our arguments
// separately so paths, spaces and shell metacharacters remain literal arguments.
export function runCompose(
  args,
  options = {},
  command = process.env.COMPOSE || ".github/scripts/compose.sh",
) {
  const executable = fs.existsSync(path.resolve(root, command));
  const result = spawnSync(
    executable ? path.resolve(root, command) : "/bin/sh",
    executable ? args : ["-c", `exec ${command} "$@"`, "preview-compose", ...args],
    { cwd: root, encoding: "utf8", stdio: "pipe", ...options },
  );
  if (result.error || result.status !== 0)
    throw new Error(
      `Compose failed (${result.status}): ${result.error?.message ?? "see diagnostics"}`,
    );
  return result.stdout;
}

export function composeEndpoint(service, port) {
  const output = runCompose(["-f", "compose.yaml", "port", service, String(port)]);
  return `http://${output.trim().replace(/^0\.0\.0\.0:/, "127.0.0.1:")}`;
}
