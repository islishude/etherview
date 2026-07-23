import { expect, test, type Locator } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

const address = "0x1111111111111111111111111111111111111111";
const codeHash = "0x1111111111111111111111111111111111111111111111111111111111111111";
const readAPIKey = "ev_e2e_read";
const verificationJobID = "123e4567-e89b-42d3-a456-426614174000";
const transactionCursor = "transactions/snapshot?generation=7 + page=2&exact=true/#";
const walletAccount = "0x2222222222222222222222222222222222222222";
const walletTransactionHash = `0x${"d".repeat(64)}`;
const longWalletName = "W".repeat(128);

type WalletMode = "normal" | "reject-connect" | "invalid-call" | "delayed-write";

interface WalletRequest {
  method: string;
  params?: unknown;
}

interface WalletControl {
  emit(event: string, value: unknown): void;
  requests: WalletRequest[];
  resolveWrite(value: string): void;
  setMode(mode: WalletMode): void;
}

type WalletWindow = Window & { __etherviewE2EWallet: WalletControl };

test("embedded SPA deep links, language, theme, and keyboard entry remain functional", async ({ page }) => {
  const response = await page.goto("/blocks/1");
  expect(response?.status()).toBe(200);
  await expect(page.getByRole("heading", { name: "Block", exact: true })).toBeVisible();
  await expect(page.getByText("Finalized", { exact: true })).toBeVisible();

  await activateInView(page.getByRole("button", { name: "Switch color theme" }));
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");

  await activateInView(page.getByRole("button", { name: "切换到中文" }));
  await expect(page.locator("html")).toHaveAttribute("lang", "zh-CN");
  await expect(page.getByRole("navigation", { name: "主导航" }).getByText("区块", { exact: true })).toBeVisible();

  await page.reload();
  await page.keyboard.press("Tab");
  const skipLink = page.getByRole("link", { name: "跳到主要内容" });
  await expect(skipLink).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.locator("#main-content")).toBeFocused();
});

test("core explorer keeps canonical cursor pages and retained orphan context explicit", async ({
  page,
}) => {
  const transactionCursors: string[] = [];
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (url.pathname === "/api/v1/transactions" && url.searchParams.has("cursor")) {
      transactionCursors.push(url.searchParams.get("cursor") ?? "");
    }
  });

  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Coverage and finality context" })).toBeVisible();
  await expect(page.getByText("0 – 2", { exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "#2" })).toHaveAttribute(
    "href",
    "/blocks/0x2222222222222222222222222222222222222222222222222222222222222222",
  );

  await page.goto("/blocks");
  await expect(page.getByRole("note")).toContainText("This list contains canonical blocks only");
  await expect(page.getByRole("link", { name: "2" })).toBeVisible();
  await activateInView(page.getByRole("button", { name: "Next page" }));
  await expect(page.getByRole("link", { name: "1" })).toBeVisible();
  await expect(page.getByText("Page 2", { exact: true })).toBeVisible();

  await page.goto("/transactions");
  await expect(page.getByText("900,719,925,474,099,312,345", { exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: /0xaaaaaa…aaaaaa/ })).toBeVisible();
  await activateInView(page.getByRole("button", { name: "Next page" }));
  const secondPageTransaction = page.getByRole("link", { name: /0xbbbbbb…bbbbbb/ });
  await expect(secondPageTransaction).toBeVisible();
  await expect(page.getByText("Page 2", { exact: true })).toBeVisible();
  expect(transactionCursors).toContain(transactionCursor);
  await activateInView(secondPageTransaction);
  await expect(page.getByRole("heading", { name: "Transaction summary" })).toBeVisible();
  await expect(page.getByText("Finalized", { exact: true })).toBeVisible();
  await activateInView(page.getByRole("link", { name: address, exact: true }).first());
  await expect(page.getByRole("heading", { name: "Address summary" })).toBeVisible();
  await expect(page.getByText("900,719,925,474,099,312,345", { exact: true })).toBeVisible();
  await expect(page.getByText(/unavailable state is never displayed as zero/)).toBeVisible();

  const search = page.getByRole("searchbox", { name: "Search" });
  await search.fill("activity");
  await search.press("Enter");
  await expect(page.getByRole("link", { name: /Canonical transaction/ })).toBeVisible();
  await activateInView(page.getByRole("button", { name: "Next page" }));
  const orphan = page.getByRole("link", { name: /Retained orphan block #1/ });
  await expect(orphan).toBeVisible();
  await expect(orphan.getByText("Orphan", { exact: true })).toBeVisible();
  await activateInView(orphan);
  await expect(page.getByRole("heading", { name: "Retained orphan block" })).toBeVisible();
  await expect(page.getByText("Orphan", { exact: true })).toBeVisible();

  await activateInView(page.getByRole("button", { name: "切换到中文" }));
  await expect(page.getByRole("heading", { name: "已保留孤块" })).toBeVisible();
  await expect(page.getByText("孤链", { exact: true })).toBeVisible();
});

test("capability pages survive the embedded binary boundary in both accessible themes and languages", async ({
  page,
}) => {
  test.setTimeout(120_000);
  const externalRequests: string[] = [];
  page.on("request", (request) => {
    if (new URL(request.url()).origin !== "http://127.0.0.1:4173") {
      externalRequests.push(request.url());
    }
  });

  await page.goto("/tokens");
  const tokenLink = page.getByRole("link", { name: "Example Collectible", exact: true });
  await expect(tokenLink).toBeVisible();
  await activateInView(tokenLink);
  await expect(page.getByRole("heading", { name: "Example Collectible", level: 1 })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Token events", level: 2 })).toBeVisible();
  await activateInView(page.getByRole("link", { name: "1", exact: true }));
  await expect(page.getByRole("heading", { name: "NFT instance", exact: true, level: 1 })).toBeVisible();
  await expect(page.getByRole("heading", { name: "NFT ownership", level: 2 })).toBeVisible();

  await page.goto(`/address/${address}`);
  await expect(page.getByRole("heading", { name: "Canonical NFT balances", level: 2 })).toBeVisible();
  await expect(page.getByText("Exact RPC observation", { exact: true })).toBeVisible();

  await page.goto("/verify");
  await expect(page.getByRole("heading", { name: "Public verification is unavailable" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Open a durable verification job" })).toBeVisible();
  await page.getByLabel("Job ID", { exact: true }).fill(verificationJobID);
  await page.getByLabel("Job read API key", { exact: true }).fill(readAPIKey);
  await activateInView(page.getByRole("button", { name: "Load job", exact: true }));
  await expect(page.getByText("succeeded", { exact: true })).toBeVisible();
  await expect(page.getByText("Yes", { exact: true })).toBeVisible();

  await page.goto(`/contract/${address}?code_hash=${codeHash}`);
  await expect(page.getByText(/published-artifact reads remain available/)).toBeVisible();
  await page.getByLabel("API key", { exact: true }).fill(readAPIKey);
  await activateInView(page.getByRole("button", { name: "Load verification", exact: true }));
  await expect(page.getByText("ExampleCollectible", { exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "ABI", exact: true })).toBeVisible();

  await page.goto("/charts");
  await expect(page.getByRole("heading", { name: "Range summary", level: 2 })).toBeVisible();
  await expect(page.getByText("900719925474099312345", { exact: true })).toBeVisible();
  await expect(page.getByRole("columnheader", { name: "Parent interval (seconds)" })).toBeVisible();

  await page.goto("/pending");
  await expect(page.getByRole("heading", { name: "Immutable node snapshot", level: 2 })).toBeVisible();
  await expect(page.getByText("9,007,199,254,740,993", { exact: true })).toBeVisible();

  await page.goto("/status");
  await expect(page.getByRole("heading", { name: "Indexed data completeness", level: 2 })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Configured optional features", level: 2 })).toBeVisible();
  const verificationFeature = page
    .getByRole("listitem")
    .filter({ hasText: "New public verification submissions" });
  await expect(verificationFeature).toContainText("Disabled");

  const capabilityRoutes = [
    "/tokens",
    `/token/${address}`,
    `/nft/${address}/1`,
    `/address/${address}`,
    "/contracts",
    `/contract/${address}?code_hash=${codeHash}`,
    "/verify",
    "/charts",
    "/pending",
    "/status",
  ];
  for (const route of capabilityRoutes) {
    await assertAccessibleRoute(page, route);
  }

  await activateInView(page.getByRole("button", { name: "Switch color theme" }));
  await activateInView(page.getByRole("button", { name: "切换到中文" }));
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect(page.locator("html")).toHaveAttribute("lang", "zh-CN");
  for (const route of capabilityRoutes) {
    await assertAccessibleRoute(page, route);
  }
  expect(externalRequests).toEqual([]);
});

test("embedded server isolates SPA fallback and serves only hashed immutable assets", async ({
  request,
}) => {
  const document = await request.get("/blocks/1", { headers: { Accept: "text/html" } });
  expect(document.status()).toBe(200);
  expect(document.headers()["cache-control"]).toBe("no-store");
  expect(document.headers()["x-content-type-options"]).toBe("nosniff");
  expect(document.headers()["cross-origin-resource-policy"]).toBe("same-origin");
  expect(document.headers()["referrer-policy"]).toBe("no-referrer");

  const policy = document.headers()["content-security-policy"] ?? "";
  expect(policy).toContain("default-src 'none'");
  expect(policy).toContain("script-src 'self'");
  expect(policy).toContain("style-src 'self'");
  expect(policy).toContain("connect-src 'self'");
  expect(policy).toContain("object-src 'none'");
  expect(policy).toContain("frame-ancestors 'none'");
  expect(policy).not.toContain("'unsafe-inline'");
  expect(policy).not.toContain("'unsafe-eval'");

  const html = await document.text();
  const entrypoints = [...html.matchAll(/(?:src|href)="(\/assets\/[^"]+)"/g)].map(
    ([, target]) => target,
  );
  expect(entrypoints.length).toBeGreaterThan(0);
  expect(entrypoints.every((target) => /-[A-Za-z0-9_-]{8}\.[A-Za-z0-9]+$/.test(target))).toBe(
    true,
  );

  const asset = await request.get(entrypoints[0]);
  expect(asset.status()).toBe(200);
  expect(asset.headers()["cache-control"]).toBe("public, max-age=31536000, immutable");
  expect(asset.headers()["etag"]).toMatch(/^"[a-f0-9]{64}"$/);
  expect(asset.headers()["x-content-type-options"]).toBe("nosniff");

  const notModified = await request.get(entrypoints[0], {
    headers: { "If-None-Match": asset.headers()["etag"] },
  });
  expect(notModified.status()).toBe(304);
  expect(notModified.headers()["cache-control"]).toBe(
    "public, max-age=31536000, immutable",
  );
  expect(notModified.headers()["content-security-policy"]).toBe(policy);
  expect(notModified.headers()["x-content-type-options"]).toBe("nosniff");

  const missingAPI = await request.get("/api/v1/not-a-route", {
    headers: { Accept: "text/html" },
  });
  expect(missingAPI.status()).toBe(404);
  expect(await missingAPI.text()).not.toContain('<div id="root"></div>');

  for (const missingAsset of ["/robots.txt", "/assets/missing.js", "/module.wasm"]) {
    const response = await request.get(missingAsset, { headers: { Accept: "text/html" } });
    expect(response.status()).toBe(404);
    expect(response.headers()["cache-control"]).toBe("no-store");
    expect(await response.text()).not.toContain('<div id="root"></div>');
  }

  const refusedHTML = await request.get("/blocks/1", {
    headers: { Accept: "text/html;q=0, */*;q=1" },
  });
  expect(refusedHTML.status()).toBe(404);
  expect(refusedHTML.headers()["cache-control"]).toBe("no-store");
  expect(await refusedHTML.text()).not.toContain('<div id="root"></div>');

  const headDeepLink = await request.head("/blocks/not-an-asset", {
    headers: { Accept: "text/html" },
  });
  expect(headDeepLink.status()).toBe(404);

  const postDeepLink = await request.post("/blocks/1", {
    headers: { Accept: "text/html" },
  });
  expect(postDeepLink.status()).toBe(405);
});

test("primary shell meets the WCAG 2.1 AA automated baseline on a narrow viewport", async ({
  page,
}) => {
  test.setTimeout(60_000);
  await page.setViewportSize({ width: 375, height: 812 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  const externalRequests: string[] = [];
  page.on("request", (request) => {
    if (new URL(request.url()).origin !== "http://127.0.0.1:4173") {
      externalRequests.push(request.url());
    }
  });

  await page.goto("/blocks");
  await expect(page.getByRole("heading", { name: "Blocks", exact: true })).toBeVisible();
  await expect(page.getByRole("table")).toBeVisible();
  await expect(page.getByText("Loading indexed data…", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Switch color theme" })).toBeVisible();
  await expect(page.getByRole("button", { name: "切换到中文" })).toBeVisible();

  const lightScan = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
    .analyze();
  expect(lightScan.violations, JSON.stringify(lightScan.violations, null, 2)).toEqual([]);

  await activateInView(page.getByRole("button", { name: "Switch color theme" }));
  await activateInView(page.getByRole("button", { name: "切换到中文" }));
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect(page.locator("html")).toHaveAttribute("lang", "zh-CN");

  const reducedMotion = await page.evaluate(() => {
    const probe = document.createElement("span");
    probe.className = "pulse-dot";
    document.body.append(probe);
    const style = getComputedStyle(probe);
    const rawDuration = style.animationDuration;
    const durationMilliseconds = rawDuration.endsWith("ms")
      ? Number.parseFloat(rawDuration)
      : Number.parseFloat(rawDuration) * 1_000;
    const result = {
      durationMilliseconds,
      iterationCount: style.animationIterationCount,
    };
    probe.remove();
    return result;
  });
  expect(reducedMotion.durationMilliseconds).toBeLessThanOrEqual(0.01);
  expect(reducedMotion.iterationCount).toBe("1");

  const darkChineseScan = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
    .analyze();
  expect(
    darkChineseScan.violations,
    JSON.stringify(darkChineseScan.violations, null, 2),
  ).toEqual([]);

  await expect(page.getByRole("heading", { name: "区块", exact: true, level: 1 })).toBeVisible();
  await expect(page.getByRole("table")).toBeVisible();
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(1);
  expect(externalRequests).toEqual([]);
});

test("EIP-6963 contract reads and writes stay inside the selected wallet boundary", async ({
  page,
}) => {
  test.setTimeout(120_000);
  const backendRequests: string[] = [];
  let recordWalletBoundary = false;
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (recordWalletBoundary && url.pathname.startsWith("/api/")) {
      backendRequests.push(`${request.method()} ${url.pathname}${url.search}`);
    }
  });

  await page.addInitScript(
    ({ account, name, transactionHash }) => {
      const requests: WalletRequest[] = [];
      const listeners = new Map<string, Set<(value: unknown) => void>>();
      let mode: WalletMode = "normal";
      let pendingWriteResolver: ((value: string) => void) | undefined;
      const provider = {
        async request({ method, params }: WalletRequest) {
          requests.push({ method, params });
          if (method === "eth_requestAccounts") {
            if (mode === "reject-connect") {
              throw {
                code: 4001,
                message: "secret-wallet-message https://wallet.invalid/?token=private",
              };
            }
            return [account];
          }
          if (method === "eth_accounts") return [account];
          if (method === "eth_chainId") return "0x1";
          if (method === "eth_call") {
            return mode === "invalid-call" ? { result: "0xfeed" } : "0xfeed";
          }
          if (method === "eth_sendTransaction") {
            if (mode === "delayed-write") {
              return await new Promise<string>((resolve) => {
                pendingWriteResolver = resolve;
              });
            }
            return transactionHash;
          }
          throw new Error(`unexpected wallet method: ${method}`);
        },
        on(event: string, listener: (value: unknown) => void) {
          const current = listeners.get(event) ?? new Set();
          current.add(listener);
          listeners.set(event, current);
        },
        removeListener(event: string, listener: (value: unknown) => void) {
          listeners.get(event)?.delete(listener);
        },
      };
      const detail = Object.freeze({
        info: Object.freeze({
          uuid: "00000000-0000-4000-8000-000000000001",
          name,
          icon: "data:image/png;base64,",
          rdns: "org.etherview.walletwithanintentionallylongbutvalidlabel",
        }),
        provider,
      });
      window.addEventListener("eip6963:requestProvider", () => {
        window.dispatchEvent(new CustomEvent("eip6963:announceProvider", { detail }));
      });
      (window as WalletWindow).__etherviewE2EWallet = {
        requests,
        resolveWrite(value) {
          const resolve = pendingWriteResolver;
          pendingWriteResolver = undefined;
          resolve?.(value);
        },
        setMode(nextMode) {
          mode = nextMode;
        },
        emit(event, value) {
          for (const listener of listeners.get(event) ?? []) listener(value);
        },
      };
    },
    {
      account: walletAccount,
      name: longWalletName,
      transactionHash: walletTransactionHash,
    },
  );

  await page.goto("/contracts");
  await page.getByLabel("Address", { exact: true }).fill(address);
  await expect(page.getByLabel("Code hash (optional)")).toHaveValue("");
  await activateInView(page.getByRole("button", { name: "Open contract" }));
  await expect(page.getByRole("heading", { name: "Contract", level: 1 })).toBeVisible();
  await expect(page.getByText("Ethereum", { exact: true })).toBeVisible();

  recordWalletBoundary = true;
  await activateInView(page.locator(".wallet-summary"));
  await expect(page.locator(".wallet-option")).toContainText(longWalletName);
  const providerMenuScan = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
    .analyze();
  expect(
    providerMenuScan.violations,
    JSON.stringify(providerMenuScan.violations, null, 2),
  ).toEqual([]);
  await activateInView(page.locator(".wallet-option"));
  await expect(page.locator(".wallet-summary")).toBeFocused();

  await activateInView(page.getByRole("button", { name: "Disconnect" }));
  await expect(page.locator(".wallet-summary")).toBeFocused();
  await activateInView(page.locator(".wallet-option"));
  await expect(page.locator(".wallet-summary")).toBeFocused();

  const calldata = page.getByLabel("Calldata");
  await expect(page.getByRole("button", { name: "Read contract" })).toBeEnabled();
  await calldata.fill("0x1234");
  await page.getByLabel("Value in wei (optional)").fill("15");
  await activateInView(page.getByRole("button", { name: "Read contract" }));
  await expect(page.getByText("0xfeed", { exact: true })).toBeVisible();
  await activateInView(page.getByRole("button", { name: "Send transaction" }));
  await expect(page.getByText(walletTransactionHash, { exact: true })).toBeVisible();
  expect(backendRequests).toEqual([]);

  const requests = await page.evaluate(
    () => (window as WalletWindow).__etherviewE2EWallet.requests,
  );
  expect(requests.find(({ method }) => method === "eth_call")).toEqual({
    method: "eth_call",
    params: [
      {
        chainId: "0x1",
        data: "0x1234",
        from: walletAccount,
        to: address,
        value: "0xf",
      },
      "latest",
    ],
  });
  expect(requests.find(({ method }) => method === "eth_sendTransaction")).toEqual({
    method: "eth_sendTransaction",
    params: [
      {
        chainId: "0x1",
        data: "0x1234",
        from: walletAccount,
        to: address,
        value: "0xf",
      },
    ],
  });
  expect(
    requests.every(({ method }) =>
      [
        "eth_accounts",
        "eth_call",
        "eth_chainId",
        "eth_requestAccounts",
        "eth_sendTransaction",
      ].includes(method),
    ),
  ).toBe(true);
  await expect(page.getByLabel(/private key/i)).toHaveCount(0);

  await page.evaluate(() => {
    (window as WalletWindow).__etherviewE2EWallet.setMode("delayed-write");
  });
  await activateInView(page.getByRole("button", { name: "Send transaction" }));
  await expect(page.getByText("Confirm or reject the transaction in your wallet.")).toBeVisible();
  await page.evaluate(
    ({ account }) => {
      const wallet = (window as WalletWindow).__etherviewE2EWallet;
      wallet.emit("accountsChanged", ["0x4444444444444444444444444444444444444444"]);
      wallet.emit("accountsChanged", [account]);
    },
    { account: walletAccount },
  );
  await expect(page.getByRole("button", { name: "Read contract" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Send transaction" })).toBeDisabled();
  await page.evaluate(
    ({ transactionHash }) => {
      (window as WalletWindow).__etherviewE2EWallet.resolveWrite(transactionHash);
    },
    { transactionHash: walletTransactionHash },
  );
  await expect(
    page.getByText(
      "The wallet changed while the transaction was pending. Its outcome is unknown; check your wallet before retrying.",
    ),
  ).toBeVisible();
  await expect(page.getByText(walletTransactionHash, { exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Send transaction" })).toBeEnabled();
  await page.evaluate(() => {
    (window as WalletWindow).__etherviewE2EWallet.setMode("normal");
  });

  await activateInView(page.locator(".wallet-summary"));
  await activateInView(page.getByRole("button", { name: "Switch color theme" }));
  await activateInView(page.getByRole("button", { name: "切换到中文" }));
  await page.setViewportSize({ width: 320, height: 720 });
  await activateInView(page.locator(".wallet-summary"));
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect(page.locator("html")).toHaveAttribute("lang", "zh-CN");
  const connectedNarrowScan = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
    .analyze();
  expect(
    connectedNarrowScan.violations,
    JSON.stringify(connectedNarrowScan.violations, null, 2),
  ).toEqual([]);
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(1);

  const disconnectButton = page.getByRole("button", { name: "断开连接" });
  await disconnectButton.focus();
  await expect(disconnectButton).toBeFocused();
  await page.evaluate(() => {
    (window as WalletWindow).__etherviewE2EWallet.emit("disconnect", {
      code: 4900,
      message: "secret-wallet-message https://wallet.invalid/?token=private",
    });
  });
  await expect(page.getByRole("alert")).toContainText("注入式钱包已断开连接。");
  await expect(page.locator(".wallet-summary")).toBeFocused();
  await expect(page.getByText(/secret-wallet-message/)).toHaveCount(0);
  await expect(page.getByRole("button", { name: "读取合约" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "发送交易" })).toBeDisabled();
  await page.locator(".wallet-summary").press("Enter");

  await page.evaluate(() => {
    (window as WalletWindow).__etherviewE2EWallet.setMode("reject-connect");
  });
  await activateInView(page.locator(".wallet-summary"));
  await expect(page.locator(".wallet-option")).toContainText(longWalletName);
  const providerListOverflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(providerListOverflow).toBeLessThanOrEqual(1);
  const disconnectedMenuScan = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
    .analyze();
  expect(
    disconnectedMenuScan.violations,
    JSON.stringify(disconnectedMenuScan.violations, null, 2),
  ).toEqual([]);
  await activateInView(page.locator(".wallet-option"));
  await expect(page.getByRole("alert")).toContainText("钱包请求已被拒绝。");
  await expect(page.getByText(/secret-wallet-message/)).toHaveCount(0);

  await page.evaluate(() => {
    (window as WalletWindow).__etherviewE2EWallet.setMode("normal");
  });
  await activateInView(page.locator(".wallet-option"));
  await page.evaluate(() => {
    (window as WalletWindow).__etherviewE2EWallet.setMode("invalid-call");
  });
  await activateInView(page.getByRole("button", { name: "读取合约" }));
  await expect(page.getByRole("alert")).toContainText("注入式钱包返回了无效响应。");
});

test("EIP-6963 wallet discovery keeps reads and writes disabled on chain mismatch", async ({
  page,
}) => {
  await page.addInitScript(() => {
    const requests: WalletRequest[] = [];
    const provider = {
      async request({ method, params }: WalletRequest) {
        requests.push({ method, params });
        if (method === "eth_requestAccounts") return ["0x2222222222222222222222222222222222222222"];
        if (method === "eth_accounts") return ["0x2222222222222222222222222222222222222222"];
        if (method === "eth_chainId") return "0x2";
        throw new Error(`unexpected wallet method: ${method}`);
      },
      on() {},
      removeListener() {},
    };
    const detail = {
      info: {
        uuid: "00000000-0000-4000-8000-000000000001",
        name: "E2E Wallet",
        icon: "data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg'/>",
        rdns: "org.etherview.e2e",
      },
      provider,
    };
    window.addEventListener("eip6963:requestProvider", () => {
      window.dispatchEvent(new CustomEvent("eip6963:announceProvider", { detail }));
    });
    (window as WalletWindow).__etherviewE2EWallet = {
      requests,
      setMode() {},
      emit() {},
    };
  });

  await page.goto(`/contract/${address}`);
  await activateInView(page.getByText("Connect wallet", { exact: true }).first());
  await activateInView(page.getByRole("button", { name: /E2E Wallet/ }));

  await expect(page.getByRole("status").filter({ hasText: "Switch the wallet to chain 1 (currently 2)." })).toBeVisible();
  await expect(page.getByText("Wallet connected", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Read contract" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Send transaction" })).toBeDisabled();
  const requests = await page.evaluate(
    () => (window as WalletWindow).__etherviewE2EWallet.requests,
  );
  expect(requests.map(({ method }) => method)).toEqual([
    "eth_requestAccounts",
    "eth_chainId",
  ]);

  await activateInView(page.locator(".wallet-summary"));
  await activateInView(page.getByRole("button", { name: "Switch color theme" }));
  await activateInView(page.getByRole("button", { name: "切换到中文" }));
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(
    page.getByRole("status").filter({ hasText: "请将钱包切换到链 1（当前为 2）。" }),
  ).toBeVisible();
  const mismatchScan = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
    .analyze();
  expect(mismatchScan.violations, JSON.stringify(mismatchScan.violations, null, 2)).toEqual([]);
});

async function activateInView(locator: Locator) {
  await locator.evaluate((element) => {
    element.scrollIntoView({ behavior: "instant", block: "center" });
    (element as HTMLElement).focus({ preventScroll: true });
  });
  await expect(locator).toBeFocused();
  await locator.press("Enter");
}

async function assertAccessibleRoute(page: import("@playwright/test").Page, route: string) {
  const response = await page.goto(route);
  expect(response?.status(), route).toBe(200);
  await expect(page.locator("main h1"), route).toBeVisible();
  await expect(page.locator(".query-notice .pulse-dot"), route).toHaveCount(0);
  await expect(page.locator(".chart-loading"), route).toHaveCount(0);

  const scan = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
    .analyze();
  expect(scan.violations, `${route}\n${JSON.stringify(scan.violations, null, 2)}`).toEqual([]);

  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(overflow, route).toBeLessThanOrEqual(1);
}
