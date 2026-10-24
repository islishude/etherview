import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { parseEnv } from "node:util";

const prefix = "ETHERVIEW_PUSH_";

// Preserve operator settings and comments verbatim. Never rotate an existing
// VAPID identity: browsers bind their subscriptions to its public key.
export function prepareEnvironment(file) {
  let source = "";
  try {
    if (!fs.lstatSync(file).isFile()) throw new Error("Preview env must be a regular file");
    source = fs.readFileSync(file, "utf8");
  } catch (error) {
    if (error.code !== "ENOENT") throw error;
  }
  const values = parseEnv(source);
  const additions = {};
  const add = (key, value) => {
    if (!values[prefix + key]) additions[prefix + key] = value;
  };
  const privateKey = values[prefix + "PRIVATE_KEY"];
  const publicKey = values[prefix + "PUBLIC_KEY"];
  const privateFile = values[prefix + "PRIVATE_KEY_FILE"];
  if (privateFile) {
    if (!publicKey || privateKey)
      throw new Error("File-based push keys require PUBLIC_KEY and no inline PRIVATE_KEY");
  } else {
    if (publicKey && !privateKey)
      throw new Error(
        "Existing push public key needs its matching private key in the Preview env file",
      );
    const key = crypto.createECDH("prime256v1");
    if (privateKey) {
      try {
        const bytes = Buffer.from(privateKey, "base64url");
        if (bytes.length !== 32 || bytes.toString("base64url") !== privateKey) throw new Error();
        key.setPrivateKey(bytes);
      } catch {
        throw new Error("Invalid Preview push private key");
      }
    } else {
      key.generateKeys();
      // OpenSSL omits leading zero bytes; VAPID consumers require a 32-byte scalar.
      const scalar = key.getPrivateKey();
      const bytes = Buffer.alloc(32);
      scalar.copy(bytes, bytes.length - scalar.length);
      add("PRIVATE_KEY", bytes.toString("base64url"));
    }
    const derived = key.getPublicKey().toString("base64url");
    if (publicKey && publicKey !== derived) throw new Error("Preview push key pair does not match");
    add("PUBLIC_KEY", derived);
  }
  add("ENABLED", "true");
  add("CONTACT", "https://github.com/islishude/etherview/issues");
  add("UNSAFE_ALLOW_PRIVATE_NETWORKS", "false");
  const lines = Object.entries(additions)
    .map(([key, value]) => `${key}=${value}\n`)
    .join("");
  const flags =
    fs.constants.O_WRONLY | fs.constants.O_APPEND | fs.constants.O_CREAT | fs.constants.O_NOFOLLOW;
  fs.mkdirSync(path.dirname(file), { recursive: true, mode: 0o700 });
  const descriptor = fs.openSync(file, flags, 0o600);
  try {
    fs.fchmodSync(descriptor, 0o600);
    if (lines)
      fs.writeFileSync(descriptor, `${source && !source.endsWith("\n") ? "\n" : ""}${lines}`);
  } finally {
    fs.closeSync(descriptor);
  }
}

if (import.meta.main) {
  try {
    prepareEnvironment(
      path.resolve(
        process.argv[2] ?? path.join(import.meta.dirname, "../../.local/preview-push.env"),
      ),
    );
    console.log(
      "preview-env: environment ready (0600); existing settings and VAPID identity preserved",
    );
  } catch (error) {
    console.error(`preview-env: ${error.message}`);
    process.exitCode = 1;
  }
}
