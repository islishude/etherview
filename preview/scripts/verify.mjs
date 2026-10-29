import { pathToFileURL } from "node:url";

import { runCompose } from "../../.github/scripts/compose.mjs";

export function verifyPreview(compose = runCompose) {
  // Credential stays in memory and stdin, never command arguments, env, or files.
  const admin = (args) =>
    JSON.parse(
      compose([
        "-f",
        "compose.yaml",
        "exec",
        "-T",
        "api",
        "/etherview",
        "admin",
        "api-key",
        ...args,
        "--config=/etc/etherview/config.yaml",
      ]),
    );
  const key = admin([
    "create",
    "--name",
    "preview-genesis-verification",
    "--scope",
    "contract:verify",
    "--scope",
    "api:read",
  ]);
  try {
    compose(["-f", "compose.yaml", "run", "--rm", "--no-deps", "-T", "genesis-verify"], {
      input: key.token,
      stdio: ["pipe", "inherit", "inherit"],
      timeout: 30 * 60 * 1000,
    });
  } finally {
    admin(["revoke", key.prefix]);
  }
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    verifyPreview();
  } catch (error) {
    console.error(`preview-verify: ${error.message}`);
    process.exitCode = 1;
  }
}
