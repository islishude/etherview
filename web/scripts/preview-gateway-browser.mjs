// Real Preview browser acceptance, invoked while the test-owned Compose stack is running.
import assert from "node:assert/strict";
import { createHash, X509Certificate } from "node:crypto";
import { readFileSync } from "node:fs";
import { chromium } from "@playwright/test";
import { privateKeyToAccount } from "viem/accounts";

const [binding, certificate, artifacts] = process.argv.slice(2);
assert(binding && certificate && artifacts);
const spki = new X509Certificate(readFileSync(certificate)).publicKey.export({
  type: "spki",
  format: "der",
});
const fingerprint = createHash("sha256").update(spki).digest("base64");
const browser = await chromium.launch({
  channel: process.env.PLAYWRIGHT_USE_BUNDLED === "1" ? undefined : "chrome",
  args: [
    `--host-resolver-rules=MAP *.etherview.localhost ${binding}, EXCLUDE localhost`,
    // Trust only this test's certificate key, not arbitrary HTTPS errors.
    `--ignore-certificate-errors-spki-list=${fingerprint}`,
    "--no-proxy-server",
  ],
});
try {
  const context = await browser.newContext();
  await context.tracing.start({ screenshots: true, snapshots: true });
  const page = await context.newPage();
  const response = await page.goto("https://app.etherview.localhost");
  assert.equal(response.status(), 200);
  assert.match(response.headers()["content-security-policy"], /connect-src 'self'/);
  await page.locator("main").waitFor();
  // The real app keeps the ordinary five-per-second anonymous quota. Let its
  // initial query burst drain before this independent browser API probe.
  await page.waitForTimeout(4000);
  assert.equal(await page.evaluate(() => isSecureContext), true);
  const config = await page.evaluate(async () => (await fetch("/api/v1/config")).json());
  assert(config.data, JSON.stringify(config));
  assert.deepEqual(config.data.wallet_add_chain.rpc_urls, ["https://geth.etherview.localhost"]);
  assert.deepEqual(config.data.wallet_add_chain.block_explorer_urls, [
    "https://app.etherview.localhost",
  ]);
  // Public Preview development account; never use this key outside disposable tests.
  const account = privateKeyToAccount(
    "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80",
  );
  const challenge = await page.evaluate(async (address) => {
    const result = await fetch("/api/v1/auth/challenge", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ address }),
    });
    if (result.status !== 201) throw new Error(`challenge: ${result.status}`);
    return (await result.json()).data;
  }, account.address);
  assert(challenge.message.startsWith("https://app.etherview.localhost wants you to sign in"));
  assert(challenge.message.includes("URI: https://app.etherview.localhost"));
  const signature = await account.signMessage({ message: challenge.message });
  const session = await page.evaluate(
    async (input) => {
      const result = await fetch("/api/v1/auth/verify", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      });
      if (result.status !== 201) throw new Error(`verify: ${result.status}`);
      return (await result.json()).data;
    },
    { challenge_id: challenge.challenge_id, signature },
  );
  const cookie = (await context.cookies()).find((item) => item.name === "etherview_session");
  assert(cookie?.secure && cookie.httpOnly);
  assert.equal(cookie.domain, "app.etherview.localhost");
  assert.equal(cookie.sameSite, "Lax");
  assert.equal(cookie.path, "/api/v1");
  assert.equal(
    await page.evaluate(
      async () => (await fetch("/api/v1/auth/logout", { method: "POST" })).status,
    ),
    403,
  );
  assert.equal(
    await page.evaluate(
      async (token) =>
        (await fetch("/api/v1/auth/logout", { method: "POST", headers: { "X-CSRF-Token": token } }))
          .status,
      session.csrf_token,
    ),
    204,
  );
  await page.screenshot({ path: `${artifacts}/gateway-browser.png`, fullPage: true });
  // Test development pages deliberately have their own CSP; the app CSP stays same-origin.
  for (const origin of ["http://localhost:5173", "http://127.0.0.1:5173"]) {
    await context.grantPermissions(["local-network-access"], { origin });
    const dev = await context.newPage();
    await dev.route(`${origin}/`, (route) =>
      route.fulfill({
        contentType: "text/html",
        body: "<!doctype html><title>Node CORS acceptance</title>",
      }),
    );
    await dev.goto(`${origin}/`);
    const result = await dev.evaluate(async () => {
      const rpc = await fetch("https://geth.etherview.localhost", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "eth_chainId", params: [] }),
      });
      const beacon = await fetch("https://beacon.etherview.localhost/eth/v1/node/version");
      return { chain: (await rpc.json()).result, beacon: beacon.status };
    });
    assert.deepEqual(result, { chain: "0xbeaf", beacon: 200 });
    await dev.evaluate(async () => {
      await Promise.all([
        new Promise((resolve, reject) => {
          const ws = new WebSocket("wss://geth.etherview.localhost");
          const observed = [];
          const timer = setTimeout(() => {
            ws.close();
            reject(
              new Error(`WSS timeout state=${ws.readyState} events=${JSON.stringify(observed)}`),
            );
          }, 30_000);
          ws.onopen = () =>
            ws.send(
              JSON.stringify({
                jsonrpc: "2.0",
                id: 1,
                method: "eth_subscribe",
                params: ["newHeads"],
              }),
            );
          ws.onmessage = (event) => {
            observed.push(event.data);
            const message = JSON.parse(event.data);
            if (message.error) {
              clearTimeout(timer);
              ws.close();
              reject(new Error(`WSS RPC: ${JSON.stringify(message.error)}`));
              return;
            }
            if (message.method === "eth_subscription") {
              clearTimeout(timer);
              ws.close();
              resolve();
            }
          };
          ws.onclose = (event) => {
            clearTimeout(timer);
            reject(new Error(`WSS closed: ${event.code} ${event.reason}`));
          };
          ws.onerror = () => {
            clearTimeout(timer);
            reject(new Error("WSS failed"));
          };
        }),
        new Promise((resolve, reject) => {
          const source = new EventSource(
            "https://beacon.etherview.localhost/eth/v1/events?topics=head",
          );
          const timer = setTimeout(() => {
            source.close();
            reject(new Error("SSE timeout"));
          }, 30_000);
          source.addEventListener("head", () => {
            clearTimeout(timer);
            source.close();
            resolve();
          });
          source.onerror = () => {
            clearTimeout(timer);
            source.close();
            reject(new Error("SSE failed"));
          };
        }),
      ]);
    });
    await dev.close();
  }
  await context.tracing.stop({ path: `${artifacts}/gateway-browser.zip` });
  console.log(
    "Preview Caddy browser: TLS, app/CSP, SIWE/CSRF/cookie, wallet URLs, node CORS, real WSS/SSE passed",
  );
} finally {
  await browser.close();
}
