import fs from "node:fs";
import https from "node:https";
import { pathToFileURL } from "node:url";

import { mnemonicToAccount } from "viem/accounts";

import { runCompose } from "../../.github/scripts/compose.mjs";

export const adminAddress = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266";
const origin = "https://app.etherview.localhost";
// Public Preview fixture, never an operator-supplied production wallet.
const account = mnemonicToAccount("test test test test test test test test test test test junk");

export async function initializeAdmin(request, compose = runCompose) {
  const { data: challenge } = await request("/auth/challenge", { address: adminAddress });
  if (
    !challenge.message.startsWith(
      `${origin} wants you to sign in with your Ethereum account:\n${adminAddress}\n`,
    ) ||
    !challenge.message.includes(`\nURI: ${origin}\n`) ||
    !challenge.message.includes("\nChain ID: 48815\n")
  )
    throw new Error("Unexpected Preview login challenge");
  const { data: session, cookie } = await request("/auth/verify", {
    challenge_id: challenge.challenge_id,
    signature: await account.signMessage({ message: challenge.message }),
  });
  try {
    if (!session.authenticated || session.user?.address !== adminAddress || !cookie)
      throw new Error("Unexpected Preview login session");
    const result = JSON.parse(
      compose([
        "-f",
        "compose.yaml",
        "exec",
        "-T",
        "api",
        "/etherview",
        "admin",
        "user",
        "set-role",
        "--config=/etc/etherview/config.yaml",
        "--address",
        adminAddress,
        "--role",
        "admin",
      ]),
    );
    if (result.user?.role !== "admin" || result.user?.address !== adminAddress)
      throw new Error("Preview administrator promotion failed");
  } finally {
    // Revoke only this helper's session; existing browser logins remain valid.
    if (cookie && session.csrf_token)
      await request("/auth/logout", undefined, {
        Cookie: cookie,
        "X-CSRF-Token": session.csrf_token,
      });
  }
}

export function previewRequest(compose = runCompose) {
  const binding = compose(["-f", "compose.yaml", "port", "preview-gateway", "443"]).trim();
  if (!/^127\.0\.0\.1:\d+$/.test(binding)) throw new Error("Expected loopback Preview gateway");
  const port = Number(binding.split(":")[1]);
  const ca = fs.readFileSync(new URL("../../.local/preview-tls/rootCA.pem", import.meta.url));
  return (route, body, headers = {}) =>
    new Promise((resolve, reject) => {
      const req = https.request(
        {
          hostname: "127.0.0.1",
          port,
          servername: "app.etherview.localhost",
          ca,
          path: `/api/v1${route}`,
          method: "POST",
          signal: AbortSignal.timeout(15000),
          headers: {
            Host: "app.etherview.localhost",
            Origin: origin,
            "Content-Type": "application/json",
            ...headers,
          },
        },
        (res) => {
          let text = "";
          res.setEncoding("utf8");
          res.on("error", reject);
          res.on("data", (chunk) => {
            text += chunk;
            if (text.length > 65536) res.destroy(new Error("Preview response too large"));
          });
          res.on("end", () => {
            if (res.statusCode < 200 || res.statusCode >= 300) {
              reject(new Error(`Preview ${route}: HTTP ${res.statusCode}`));
              return;
            }
            try {
              const value = text ? JSON.parse(text) : {};
              value.cookie = res.headers["set-cookie"]
                ?.find((c) => c.startsWith("etherview_session="))
                ?.split(";")[0];
              resolve(value);
            } catch {
              reject(new Error("Invalid Preview response"));
            }
          });
        },
      );
      req.on("error", reject);
      req.end(body === undefined ? undefined : JSON.stringify(body));
    });
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    await initializeAdmin(previewRequest());
    console.log(`preview-admin: ${adminAddress} is admin`);
  } catch (error) {
    console.error(`preview-admin: ${error.message}`);
    process.exitCode = 1;
  }
}
