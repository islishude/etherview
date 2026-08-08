import { expect, test, type Locator } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

const address = "0x1111111111111111111111111111111111111111";
const transparentImplementation = "0x3000000000000000000000000000000000000030";
const uupsProxyAddress = "0x3000000000000000000000000000000000000003";
const uupsImplementation = "0x4000000000000000000000000000000000000004";
const beaconProxyAddress = "0x5000000000000000000000000000000000000005";
const beaconImplementation = "0x6000000000000000000000000000000000000006";
const cloneAddress = "0x7000000000000000000000000000000000000007";
const cloneImplementation = "0x8000000000000000000000000000000000000008";
const proxyAdminAddress = "0x9000000000000000000000000000000000000009";
const upgradeableBeacon = "0x2000000000000000000000000000000000000020";
const oldImplementation = "0x4000000000000000000000000000000000000040";
const codeHash = "0x1111111111111111111111111111111111111111111111111111111111111111";
const readAPIKey = "ev_e2e_read";
const verificationJobID = "123e4567-e89b-42d3-a456-426614174000";
const transactionCursor = "transactions/snapshot?generation=7 + page=2&exact=true/#";
const walletAccount = "0x2222222222222222222222222222222222222222";
const walletTransactionHash = `0x${"d".repeat(64)}`;
const longWalletName = "W".repeat(128);
const authChallengeID = "00000000-0000-7000-8000-000000000043";
const authCurrentUserID = "00000000-0000-7000-8000-000000000041";
const authTargetUserID = "00000000-0000-7000-8000-000000000042";
const authCSRFToken = "c".repeat(43);
const authSignature = `0x${"a".repeat(130)}`;
const authOrigin = "http://127.0.0.1:4173";
const authSIWEExpiresAt = "2099-01-01T00:05:00.000Z";
const authSIWEMessage =
  "http://127.0.0.1:4173 wants you to sign in with your Ethereum account:\n" +
  `${walletAccount}\n\n\n` +
  `URI: ${authOrigin}\n` +
  "Version: 1\n" +
  "Chain ID: 1\n" +
  "Nonce: abcdefghijklmnopqrstuvwx\n" +
  "Issued At: 2026-01-01T00:00:00.000Z\n" +
  `Expiration Time: ${authSIWEExpiresAt}\n` +
  `Request ID: ${authChallengeID}`;
const authUserCursor = "users/snapshot + page=2";
const billingPersonalCursor =
  "personal/ledger + page=2?exact=true/#";
const billingAdminCursor = "admin/ledger + page=2?exact=true/#";
const billingPaymentID = "00000000-0000-7000-8000-000000000066";
const billingHiddenUserID = "00000000-0000-7000-8000-000000000067";
const billingAdminUserID = "00000000-0000-7000-8000-000000000068";
const billingAsset = "0x3333333333333333333333333333333333333333";
const billingRecipient = "0x4444444444444444444444444444444444444444";
const billingPayer = "0x5555555555555555555555555555555555555555";
const billingAPIKeyPrefix = "ev_browser-prefix";
const billingAmount = "340282366920938463463374607431768211455";
const billingCount = "900719925474099312345";

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

interface AuthWalletControl {
  emit(event: string, value: unknown): void;
  requests: WalletRequest[];
}

type AuthWalletWindow = Window & {
  __etherviewE2EAuthWallet: AuthWalletControl;
};

interface BrowserAuthRequest {
  body: string | null;
  headers: Record<string, string>;
  method: string;
  pathname: string;
  search: string;
}

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

test("home uses one atomic snapshot stream without REST polling", async ({
  context,
  page,
}) => {
  const session = `home-${Date.now()}-${Math.random()}`;
  await context.addCookies([{
    name: "etherview_e2e_home",
    value: session,
    url: "http://127.0.0.1:4173",
  }]);
  const dynamicRequests: string[] = [];
  const streamRequests: string[] = [];
  page.on("request", (request) => {
    const pathname = new URL(request.url()).pathname;
    if (["/api/v1/status", "/api/v1/blocks", "/api/v1/transactions"].includes(pathname)) {
      dynamicRequests.push(pathname);
    }
    if (pathname === "/api/v1/home/stream") {
      streamRequests.push(pathname);
    }
  });

  await page.goto("/");
  await expect(page.getByRole("link", { name: "#2" })).toBeVisible();
  await expect(page.getByText("0 – 2", { exact: true })).toBeVisible();
  expect(streamRequests).toHaveLength(1);
  expect(dynamicRequests).toEqual([]);

  const advanced = await page.evaluate(async () => {
    const response = await fetch("/__e2e/home/head", { method: "POST" });
    return response.ok;
  });
  expect(advanced).toBe(true);
  await expect(page.getByRole("link", { name: "#3" })).toBeVisible();
  await expect(page.getByText("0 – 3", { exact: true })).toBeVisible();
  await expect(
    page.locator(".metric-card").filter({ hasText: "Network head" }),
  ).toContainText("3");
  await page.waitForTimeout(2_200);
  expect(dynamicRequests).toEqual([]);
  expect(streamRequests).toHaveLength(1);

  await activateInView(page.getByRole("button", { name: "Switch color theme" }));
  await activateInView(page.getByRole("button", { name: "切换到中文" }));
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect(page.locator("html")).toHaveAttribute("lang", "zh-CN");
  const scan = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
    .analyze();
  expect(scan.violations).toEqual([]);
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(1);
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
  await expect(page.getByText("900.719925474099312345", { exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: /0xaaaaaa…aaaaaa/ })).toBeVisible();
  await activateInView(page.getByRole("button", { name: "Next page" }));
  const secondPageTransaction = page.getByRole("link", { name: /0xbbbbbb…bbbbbb/ });
  await expect(secondPageTransaction).toBeVisible();
  await expect(page.getByText("Page 2", { exact: true })).toBeVisible();
  expect(transactionCursors).toContain(transactionCursor);
  await activateInView(secondPageTransaction);
  const transactionSummary = page.getByRole("heading", { name: "Transaction summary" })
    .locator("..");
  await expect(transactionSummary).toBeVisible();
  await expect(page.locator(".finality-badge.finalized")).toHaveText("Finalized");
  const recipientRow = transactionSummary.locator(".transaction-detail-row").filter({
    has: page.getByText("To", { exact: true }),
  });
  await expect(recipientRow.getByRole("link", { name: address })).toBeVisible();
  await expect(recipientRow.getByText("Contract creation")).toBeVisible();
  await activateInView(transactionSummary.getByText("More details"));
  const copyButtons = transactionSummary.getByRole("button", { name: "Copy" });
  await expect(copyButtons).toHaveCount(4);
  for (const button of await copyButtons.all()) {
    await expect(button).toBeVisible();
  }
  await expect(page.getByRole("heading", { name: "Data completeness" })).toHaveCount(0);
  await page.setViewportSize({ width: 390, height: 844 });
  const transactionOverflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(transactionOverflow).toBeLessThanOrEqual(1);
  await activateInView(page.getByRole("link", { name: address, exact: true }).first());
  await expect(page.getByRole("heading", { name: "Contract", level: 1 })).toBeVisible();
  const addressSummary = page.getByRole("heading", { name: "Address summary" }).locator("..");
  await expect(addressSummary).toBeVisible();
  await expect(addressSummary.getByText("900.719925474099312345 ETH", { exact: true })).toBeVisible();
  await expect(addressSummary.getByText("Type", { exact: true })).toHaveCount(0);
  await expect(addressSummary.getByText(address, { exact: true })).toHaveCount(0);
  await expect(addressSummary.getByRole("link", { name: walletAccount })).toBeVisible();
  await activateInView(page.getByRole("button", { name: "Show QR code" }));
  const qrDialog = page.getByRole("dialog", { name: "Address QR code" });
  await expect(qrDialog).toBeFocused();
  await expect(qrDialog.getByText(`ethereum:${address}@1`, { exact: true })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(qrDialog).toHaveCount(0);
  await expect(page.getByText(/unavailable state is never displayed as zero/)).toBeVisible();
  await expect(page.getByText("Data completeness", { exact: true })).toHaveCount(0);
  await expect(page.getByText("Code hash", { exact: true })).toHaveCount(0);
  await expect(page.getByText("State block hash", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Contract", exact: true })).toHaveAttribute(
    "href",
    `/contract/${address}`,
  );
  await expect(page.getByRole("link", { name: /0xaaaaaa…aaaaaa/ })).toBeVisible();
  await activateInView(page.getByRole("link", { name: "Internal Transactions" }));
  await expect(page.getByText("SELF", { exact: true })).toBeVisible();
  await activateInView(page.getByRole("link", { name: "ERC-20 Transfers" }));
  await expect(page.locator("tbody").getByText("ERC-20", { exact: true })).toBeVisible();
  await activateInView(page.getByRole("link", { name: "NFT Transfers" }));
  await expect(page.locator("tbody").getByText("ERC-1155", { exact: true })).toBeVisible();
  await page.goBack();
  await expect(page.getByRole("link", { name: "ERC-20 Transfers" })).toHaveAttribute(
    "aria-current",
    "page",
  );
  const addressOverflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(addressOverflow).toBeLessThanOrEqual(1);

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

  await page.goto(`/address/${address}?tab=assets`);
  await expect(page.getByRole("heading", { name: "ERC-20 holdings", level: 2 })).toBeVisible();
  await expect(page.getByText("123.45 EXT", { exact: true })).toBeVisible();
  const nftBalances = page.getByRole("region", { name: "Canonical NFT balances" });
  await expect(nftBalances.getByRole("heading", { name: "Canonical NFT balances", level: 2 })).toBeVisible();
  await expect(nftBalances.getByText("Exact RPC observation", { exact: true })).toBeVisible();

  await page.goto(`/address/${walletAccount}`);
  await expect(page.getByRole("heading", { name: "Address", level: 1 })).toBeVisible();
  await expect(page.getByText("Externally owned account", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Contract", exact: true })).toHaveCount(0);

  await page.goto("/verify");
  await expect(page.getByRole("heading", { name: "Public verification is unavailable" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Open a durable verification job" })).toBeVisible();
  await page.getByLabel("Job ID", { exact: true }).fill(verificationJobID);
  await page.getByLabel("Job read API key", { exact: true }).fill(readAPIKey);
  await activateInView(page.getByRole("button", { name: "Load job", exact: true }));
  await expect(page.getByText("succeeded", { exact: true })).toBeVisible();
  await expect(page.getByText("verification_success", { exact: true })).toBeVisible();

  await page.goto(`/contract/${address}`);
  await expect(page.getByRole("heading", { name: "Verified artifact" })).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "TransparentUpgradeableProxy", level: 2 }),
  ).toBeVisible();
  await expect(page.getByRole("heading", { name: "Contract source code", level: 3 })).toBeVisible();
  const sourceEditor = page.getByRole("textbox", {
    name: "Read-only source editor for TransparentUpgradeableProxy.sol",
  });
  await expect(sourceEditor).toHaveAttribute("contenteditable", "false");
  await expect(sourceEditor).toContainText("contract TransparentUpgradeableProxy");
  const keywordColor = await sourceEditor.locator(".tok-keyword").first().evaluate((keyword) =>
    getComputedStyle(keyword).color
  );
  const editorColor = await sourceEditor.evaluate((editor) => getComputedStyle(editor).color);
  expect(keywordColor).not.toBe(editorColor);
  const sourceLineTop = await sourceEditor.locator(".cm-line").first().evaluate((line) =>
    line.getBoundingClientRect().top
  );
  const firstLineNumberTop = await page
    .locator(".source-editor .cm-lineNumbers .cm-gutterElement")
    .filter({ visible: true })
    .first()
    .evaluate((lineNumber) => lineNumber.getBoundingClientRect().top);
  expect(Math.abs(sourceLineTop - firstLineNumberTop)).toBeLessThan(1);
  await page.getByRole("button", { name: "lib/ProxyBase.sol" }).click();
  const libraryEditor = page.getByRole("textbox", {
    name: "Read-only source editor for lib/ProxyBase.sol",
  });
  await expect(libraryEditor).toHaveAttribute("contenteditable", "false");
  await expect(libraryEditor).toContainText("abstract contract ProxyBase");
  await expect(page.getByText("Not explicitly set (compiler default)").first()).toBeVisible();
  await page.getByText("Complete compiler settings", { exact: true }).click();
  await expect(page.getByText(/"optimizer"/u)).toBeVisible();
  await expect(page.getByText(/functions ·/u)).toBeVisible();
  await expect(page.getByRole("tab", { name: "Read contract" })).toBeVisible();
  await expect(page.getByLabel(/API key/iu)).toHaveCount(0);
  await expect(page.getByLabel(/calldata/iu)).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Load verification" })).toHaveCount(0);

  await page.goto("/charts");
  await expect(page.getByRole("heading", { name: "Overview stats", level: 2 })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Activity", level: 2 })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Fees & burn", level: 2 })).toBeVisible();
  await expect(page.getByText("900.719925474099312345 ETH", { exact: true }).first()).toBeVisible();

  await page.goto("/charts/execution-fees?range=7d&interval=day");
  await expect(page.getByRole("button", { name: "7D" })).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByRole("button", { name: "Download CSV" })).toBeVisible();
  await expect(page.getByRole("columnheader", { name: "Exact API value" })).toBeVisible();
  await expect(page.getByText("900719925474099312345", { exact: true })).toBeVisible();

  await page.goto("/pending");
  await expect(page.getByRole("heading", { name: "Immutable node snapshot", level: 2 })).toBeVisible();
  await expect(page.getByText("9,007,199,254,740,993", { exact: true })).toBeVisible();

  await page.goto("/status");
  await expect(
    page.getByRole("heading", { name: "Data capabilities and current completeness", level: 2 }),
  ).toBeVisible();
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
    `/address/${walletAccount}`,
    "/contracts",
    `/contract/${address}`,
    "/verify",
    "/charts",
    "/charts/execution-fees?range=7d&interval=day",
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

test("verified OpenZeppelin proxy pages use anonymous generated forms and exact bound targets", async ({
  page,
}) => {
  test.setTimeout(120_000);
  const contractRequests: Array<{
    headers: Record<string, string>;
    method: string;
    pathname: string;
  }> = [];
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (!url.pathname.startsWith("/api/v1/contracts/")) return;
    contractRequests.push({
      headers: request.headers(),
      method: request.method(),
      pathname: url.pathname,
    });
  });

  await page.goto(`/contract/${address}`);
  await expect(
    page.getByRole("heading", { name: "TransparentUpgradeableProxy", level: 2 }),
  ).toBeVisible();
  await expect(page.getByText("Transparent proxy", { exact: true })).toBeVisible();
  const transparentTabs = page.getByRole("tablist", {
    name: "Contract interaction sections",
  });
  const codeTab = transparentTabs.getByRole("tab", { name: "Code" });
  await codeTab.focus();
  await codeTab.press("ArrowRight");
  const readContractTab = transparentTabs.getByRole("tab", { name: "Read contract" });
  await expect(readContractTab).toBeFocused();
  await expect(readContractTab).toHaveAttribute("aria-selected", "true");
  const directRead = page.locator(".abi-function-card").filter({ hasText: "proxyValue()" });
  await expect(directRead.getByText(address, { exact: true })).toBeVisible();

  await activateInView(transparentTabs.getByRole("tab", { name: "Write contract" }));
  await expect(page.getByText("setProxyValue(uint256)", { exact: true })).toBeVisible();
  await expect(page.getByLabel("newValue")).toBeVisible();
  await expect(page.getByLabel(/calldata/iu)).toHaveCount(0);

  await activateInView(transparentTabs.getByRole("tab", {
    name: "Read implementation (as proxy)",
  }));
  const transparentRead = page.locator(".abi-function-card").filter({ hasText: "value()" });
  await expect(transparentRead.getByText(address, { exact: true })).toBeVisible();
  await expect(page.getByText(transparentImplementation, { exact: true }).first()).toBeVisible();

  await activateInView(transparentTabs.getByRole("tab", { name: "Proxy management" }));
  const proxyAdminUpgrade = page.locator(".abi-function-card").filter({
    hasText: "upgradeAndCall(address,address,bytes)",
  });
  await expect(proxyAdminUpgrade.getByText(proxyAdminAddress, { exact: true })).toBeVisible();
  await expect(proxyAdminUpgrade).toContainText("management target linked to 1 proxies");
  await expect(proxyAdminUpgrade.getByText(/High-risk upgrade operation/)).toBeVisible();

  await activateInView(transparentTabs.getByRole("tab", { name: "Upgrade history" }));
  await expect(page.getByRole("heading", { name: "Canonical implementation upgrades" })).toBeVisible();
  await expect(page.getByText(oldImplementation, { exact: true })).toBeVisible();
  await expect(page.getByText(transparentImplementation, { exact: true }).last()).toBeVisible();

  await activateInView(transparentTabs.getByRole("tab", { name: "Initialization history" }));
  await expect(page.getByText("Initialized version 2", { exact: true })).toBeVisible();

  await page.goto(`/contract/${uupsProxyAddress}`);
  await expect(page.getByRole("heading", { name: "ERC1967Proxy", level: 2 })).toBeVisible();
  const uupsTabs = page.getByRole("tablist", { name: "Contract interaction sections" });
  await expect(uupsTabs.getByRole("tab", { name: "Proxy management" })).toHaveCount(0);
  await activateInView(uupsTabs.getByRole("tab", {
    name: "Read implementation (as proxy)",
  }));
  const uupsValue = page.locator(".abi-function-card").filter({ hasText: "value()" }).first();
  await expect(uupsValue.getByText(uupsProxyAddress, { exact: true })).toBeVisible();
  const proxiable = page.locator(".abi-function-card").filter({ hasText: "proxiableUUID()" });
  await activateInView(proxiable.locator("summary"));
  await expect(proxiable.getByText(uupsImplementation, { exact: true })).toBeVisible();
  await expect(proxiable.getByText(/called directly on the implementation/)).toBeVisible();
  await activateInView(uupsTabs.getByRole("tab", {
    name: "Write implementation (as proxy)",
  }));
  const uupsUpgrade = page.locator(".abi-function-card").filter({
    hasText: "upgradeToAndCall(address,bytes)",
  });
  await activateInView(uupsUpgrade.locator("summary"));
  await expect(uupsUpgrade.getByText(uupsProxyAddress, { exact: true })).toBeVisible();

  await page.goto(`/contract/${beaconProxyAddress}`);
  await expect(page.getByRole("heading", { name: "BeaconProxy", level: 2 })).toBeVisible();
  const beaconTabs = page.getByRole("tablist", { name: "Contract interaction sections" });
  await activateInView(beaconTabs.getByRole("tab", { name: "Proxy management" }));
  const beaconUpgrade = page.locator(".abi-function-card").filter({ hasText: "upgradeTo(address)" });
  await expect(beaconUpgrade.getByText(upgradeableBeacon, { exact: true })).toBeVisible();
  await expect(beaconUpgrade).toContainText("affects 2 linked proxies");
  await activateInView(beaconTabs.getByRole("tab", { name: "Upgrade history" }));
  await expect(page.getByText("Beacon implementation changed", { exact: true })).toBeVisible();
  await expect(page.getByText(beaconImplementation, { exact: true }).last()).toBeVisible();

  await page.goto(`/contract/${cloneAddress}`);
  await expect(page.getByRole("heading", { name: "MinimalClone", level: 2 })).toBeVisible();
  await expect(page.getByText(/This EIP-1167 Clone is immutable/)).toBeVisible();
  const cloneTabs = page.getByRole("tablist", { name: "Contract interaction sections" });
  await expect(cloneTabs.getByRole("tab", { name: "Upgrade history" })).toHaveCount(0);
  await activateInView(cloneTabs.getByRole("tab", {
    name: "Read implementation (as proxy)",
  }));
  const cloneRead = page.locator(".abi-function-card").filter({ hasText: "value()" });
  await expect(cloneRead.getByText(cloneAddress, { exact: true })).toBeVisible();
  await expect(page.getByText(cloneImplementation, { exact: true }).first()).toBeVisible();
  await expect.poll(() => contractRequests.some(({ pathname }) =>
    pathname === `/api/v1/contracts/${cloneAddress}/proxy/upgrades`)).toBe(false);

  await page.setViewportSize({ width: 390, height: 844 });
  await activateInView(page.getByRole("button", { name: "Switch color theme" }));
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await assertA11yAndNoOverflow(page, "clone contract page in English dark mode");
  await activateInView(page.getByRole("button", { name: "切换到中文" }));
  await expect(page.locator("html")).toHaveAttribute("lang", "zh-CN");
  await expect(page.getByRole("tab", { name: "读取实现（经代理）" })).toBeVisible();
  await expect(page.getByText(/该 EIP-1167 Clone 不可升级/)).toBeVisible();
  await assertA11yAndNoOverflow(page, "clone contract page in Chinese dark mode");

  expect(contractRequests.length).toBeGreaterThan(12);
  for (const request of contractRequests) {
    expect(request.method).toBe("GET");
    expect(request.headers["x-api-key"]).toBeUndefined();
    expect(request.headers["payment-signature"]).toBeUndefined();
    expect(request.headers["x-csrf-token"]).toBeUndefined();
  }
  expect(contractRequests.some(({ pathname }) =>
    pathname === `/api/v1/contracts/${proxyAdminAddress}/verification`)).toBe(true);
  expect(contractRequests.some(({ pathname }) =>
    pathname === `/api/v1/contracts/${upgradeableBeacon}/verification`)).toBe(true);
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

test("embedded SIWE account, billing, and administrator flows retain the wallet and generated-API boundaries", async ({
  page,
}) => {
  test.setTimeout(120_000);
  const authRequests: BrowserAuthRequest[] = [];
  let authenticated = false;
  let currentDisplayName: string | null = "Browser Admin";
  let targetRole: "user" | "admin" = "user";
  let targetStatus: "active" | "disabled" = "active";

  const currentUser = () => ({
    id: authCurrentUserID,
    chain_id: "1",
    address: walletAccount,
    role: "admin",
    status: "active",
    display_name: currentDisplayName,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    last_login_at: "2026-01-01T00:00:00Z",
  });
  const targetUser = () => ({
    id: authTargetUserID,
    chain_id: "1",
    address,
    role: targetRole,
    status: targetStatus,
    display_name: null,
    created_at: "2026-01-02T00:00:00Z",
    updated_at: "2026-01-02T00:00:00Z",
    last_login_at: "2026-01-02T00:00:00Z",
  });
  const authSession = () => ({
    authenticated: true,
    csrf_token: authCSRFToken,
    expires_at: "2099-01-08T00:00:00Z",
    user: currentUser(),
  });
  const envelope = (data: unknown, meta: Record<string, unknown> = {}) => ({
    data,
    meta: {
      request_id: "embedded-auth-e2e",
      chain_id: "1",
      ...meta,
    },
  });
  const record = (request: import("@playwright/test").Request) => {
    const url = new URL(request.url());
    authRequests.push({
      body: request.postData(),
      headers: request.headers(),
      method: request.method(),
      pathname: url.pathname,
      search: url.search,
    });
  };

  await page.route("**/api/v1/config", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      json: envelope({
        chain_id: "1",
        chain_name: "Ethereum",
        native_symbol: "ETH",
        native_name: "Ether",
        native_decimals: 18,
        features: {
          trace: true,
          mempool: true,
          historical_state: true,
          verification: false,
          nft_metadata: true,
          pricing: false,
          sourcify: false,
          user_auth: true,
          x402_billing: true,
        },
      }),
    });
  });
  await page.route("**/api/v1/auth/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    record(request);
    switch (`${request.method()} ${url.pathname}`) {
      case "GET /api/v1/auth/session":
        await route.fulfill({
          contentType: "application/json",
          json: envelope(
            authenticated ? authSession() : { authenticated: false },
          ),
        });
        return;
      case "POST /api/v1/auth/challenge":
        await route.fulfill({
          contentType: "application/json",
          json: envelope({
            challenge_id: authChallengeID,
            message: authSIWEMessage,
            expires_at: authSIWEExpiresAt,
          }),
          status: 201,
        });
        return;
      case "POST /api/v1/auth/verify":
        authenticated = true;
        await route.fulfill({
          contentType: "application/json",
          json: envelope(authSession()),
          status: 201,
        });
        return;
      case "POST /api/v1/auth/logout":
        authenticated = false;
        await route.fulfill({ body: "", status: 204 });
        return;
      default:
        await route.fulfill({ status: 404 });
    }
  });
  await page.route("**/api/v1/users/me", async (route) => {
    const request = route.request();
    record(request);
    const body = request.postDataJSON() as { display_name: string | null };
    currentDisplayName = body.display_name;
    await route.fulfill({
      contentType: "application/json",
      json: envelope(currentUser()),
    });
  });
  await page.route("**/api/v1/admin/users**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    record(request);
    if (
      request.method() === "GET" &&
      url.pathname === "/api/v1/admin/users"
    ) {
      const cursor = url.searchParams.get("cursor");
      await route.fulfill({
        contentType: "application/json",
        json: envelope(
          cursor === authUserCursor ? [currentUser()] : [targetUser()],
          cursor === authUserCursor
            ? {}
            : { next_cursor: authUserCursor },
        ),
      });
      return;
    }
    if (
      request.method() === "PATCH" &&
      url.pathname === `/api/v1/admin/users/${authTargetUserID}`
    ) {
      const body = request.postDataJSON() as {
        role?: "user" | "admin";
        status?: "active" | "disabled";
      };
      targetRole = body.role ?? targetRole;
      targetStatus = body.status ?? targetStatus;
      await route.fulfill({
        contentType: "application/json",
        json: envelope(targetUser()),
      });
      return;
    }
    if (
      request.method() === "POST" &&
      url.pathname ===
        `/api/v1/admin/users/${authTargetUserID}/sessions/revoke`
    ) {
      await route.fulfill({
        contentType: "application/json",
        json: envelope({ revoked_sessions: "3" }),
      });
      return;
    }
    await route.fulfill({ status: 404 });
  });
  const billingPayment = (
    overrides: Record<string, unknown> = {},
  ) => ({
    id: billingPaymentID,
    operation: "getBlock",
    state: "settled",
    network: "eip155:84532",
    asset: billingAsset,
    amount_atomic: billingAmount,
    recipient: billingRecipient,
    payer: billingPayer,
    transaction_hash: `0x${"6".repeat(64)}`,
    created_at: "2026-07-25T23:58:00Z",
    updated_at: "2026-07-26T00:00:00Z",
    settled_at: "2026-07-26T00:00:00Z",
    ...overrides,
  });
  await page.route("**/api/v1/billing/payments**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    record(request);
    const cursor = url.searchParams.get("cursor");
    await route.fulfill({
      contentType: "application/json",
      json: envelope(
        [
          billingPayment({
            api_key_prefix: billingAPIKeyPrefix,
            user_id: billingHiddenUserID,
          }),
        ],
        cursor === billingPersonalCursor
          ? {}
          : { next_cursor: billingPersonalCursor },
      ),
    });
  });
  await page.route("**/api/v1/admin/billing/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    record(request);
    if (
      request.method() === "GET" &&
      url.pathname === "/api/v1/admin/billing/summary"
    ) {
      await route.fulfill({
        contentType: "application/json",
        json: envelope({
          amount_atomic: billingAmount,
          from_time: "2026-07-25T00:00:00Z",
          payment_count: billingCount,
          rows: [
            {
              amount_atomic: billingAmount,
              asset: billingAsset,
              network: "eip155:84532",
              operation: "getBlock",
              payment_count: billingCount,
              state: "settled",
            },
          ],
          to_time: "2026-07-26T00:00:00Z",
        }),
      });
      return;
    }
    if (
      request.method() === "GET" &&
      url.pathname === "/api/v1/admin/billing/payments"
    ) {
      const cursor = url.searchParams.get("cursor");
      await route.fulfill({
        contentType: "application/json",
        json: envelope(
          [
            billingPayment({
              api_key_prefix: billingAPIKeyPrefix,
              failure_code: "settlement_unknown",
              state: "settling",
              user_id: billingAdminUserID,
            }),
          ],
          cursor === billingAdminCursor
            ? {}
            : { next_cursor: billingAdminCursor },
        ),
      });
      return;
    }
    await route.fulfill({ status: 404 });
  });
  await page.addInitScript(
    ({ account, signature }) => {
      const requests: WalletRequest[] = [];
      const listeners = new Map<string, Set<(value: unknown) => void>>();
      const provider = {
        async request({ method, params }: WalletRequest) {
          requests.push({ method, params });
          if (method === "eth_requestAccounts") return [account];
          if (method === "eth_accounts") return [account];
          if (method === "eth_chainId") return "0x1";
          if (method === "personal_sign") return signature;
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
          uuid: "00000000-0000-4000-8000-000000000065",
          name: "SIWE E2E Wallet",
          icon: "data:image/png;base64,",
          rdns: "org.etherview.siwe-e2e",
        }),
        provider,
      });
      window.addEventListener("eip6963:requestProvider", () => {
        window.dispatchEvent(
          new CustomEvent("eip6963:announceProvider", { detail }),
        );
      });
      (window as AuthWalletWindow).__etherviewE2EAuthWallet = {
        requests,
        emit(event, value) {
          for (const listener of listeners.get(event) ?? []) listener(value);
        },
      };
    },
    { account: walletAccount, signature: authSignature },
  );

  const response = await page.goto("/account");
  expect(response?.status()).toBe(200);
  await expect(
    page.getByRole("heading", { name: "Wallet connection" }),
  ).toBeVisible();
  await expect(
    page.getByText("Wallet disconnected", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("Not logged in", { exact: true }).last(),
  ).toBeVisible();

  await activateInView(page.locator(".auth-action-panel button"));
  await expect(
    page.locator(".identity-card").getByText("User authenticated", {
      exact: true,
    }),
  ).toBeVisible();
  const walletRequests = await page.evaluate(
    () =>
      (window as AuthWalletWindow).__etherviewE2EAuthWallet.requests,
  );
  expect(walletRequests.map(({ method }) => method)).toEqual([
    "eth_requestAccounts",
    "eth_chainId",
    "eth_chainId",
    "eth_accounts",
    "personal_sign",
    "eth_chainId",
    "eth_accounts",
  ]);
  expect(
    walletRequests.filter(({ method }) => method === "personal_sign"),
  ).toEqual([
    {
      method: "personal_sign",
      params: [
        `0x${Buffer.from(authSIWEMessage, "utf8").toString("hex")}`,
        walletAccount,
      ],
    },
  ]);

  const challengeRequest = authRequests.find(
    ({ pathname }) => pathname === "/api/v1/auth/challenge",
  );
  const verifyRequest = authRequests.find(
    ({ pathname }) => pathname === "/api/v1/auth/verify",
  );
  expect(challengeRequest?.body).toBe(
    JSON.stringify({ address: walletAccount }),
  );
  expect(challengeRequest?.headers.origin).toBe(
    "http://127.0.0.1:4173",
  );
  expect(verifyRequest?.body).toBe(
    JSON.stringify({
      challenge_id: authChallengeID,
      signature: authSignature,
    }),
  );
  expect(verifyRequest?.headers.origin).toBe("http://127.0.0.1:4173");
  expect(
    await page.evaluate(
      (token) =>
        [...Array(localStorage.length).keys()].every(
          (index) => localStorage.getItem(localStorage.key(index) ?? "") !== token,
        ),
      authCSRFToken,
    ),
  ).toBe(true);
  await expect(page.locator("body")).not.toContainText(authCSRFToken);

  expect(
    authRequests.filter(
      ({ method, pathname }) =>
        method === "POST" && pathname === "/api/v1/auth/logout",
    ),
  ).toHaveLength(0);
  await page.reload();
  await expect(
    page.locator(".identity-card").getByText("User authenticated", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    page.getByText("Wallet disconnected", { exact: true }),
  ).toBeVisible();
  expect(
    authRequests.filter(
      ({ method, pathname }) =>
        method === "POST" && pathname === "/api/v1/auth/logout",
    ),
  ).toHaveLength(0);

  await activateInView(page.locator(".wallet-summary"));
  await activateInView(
    page.getByRole("button", { name: /SIWE E2E Wallet/ }),
  );
  await expect(
    page.getByText("Wallet connected", { exact: true }),
  ).toBeVisible();
  expect(
    authRequests.filter(
      ({ method, pathname }) =>
        method === "POST" && pathname === "/api/v1/auth/logout",
    ),
  ).toHaveLength(0);

  const personalHistory = page.locator(".billing-history-section");
  await expect(
    personalHistory.getByRole("heading", { name: "Payment history" }),
  ).toBeVisible();
  await expect(
    personalHistory.getByText(billingAmount, { exact: true }),
  ).toBeVisible();
  await expect(personalHistory).not.toContainText(billingHiddenUserID);
  await expect(personalHistory).not.toContainText(billingAPIKeyPrefix);
  await expect(
    personalHistory.getByRole("columnheader", { name: "User ID" }),
  ).toHaveCount(0);
  await activateInView(
    personalHistory.getByRole("button", { name: "Next page" }),
  );
  await expect(
    personalHistory.getByText("Page 2", { exact: true }),
  ).toBeVisible();
  const personalCursorRequest = authRequests.find(
    ({ pathname, search }) =>
      pathname === "/api/v1/billing/payments" &&
      new URLSearchParams(search).get("cursor") === billingPersonalCursor,
  );
  expect(personalCursorRequest).toBeDefined();
  expect(
    new URLSearchParams(personalCursorRequest?.search).has("address"),
  ).toBe(false);
  expect(
    new URLSearchParams(personalCursorRequest?.search).has("user_id"),
  ).toBe(false);

  await page.locator(".profile-form input").fill("  Updated Browser Admin  ");
  await activateInView(
    page.getByRole("button", { name: "Save profile" }),
  );
  await expect(page.getByRole("status")).toContainText("Profile saved.");
  const profileRequest = authRequests.find(
    ({ pathname }) => pathname === "/api/v1/users/me",
  );
  expect(profileRequest?.body).toBe(
    JSON.stringify({ display_name: "Updated Browser Admin" }),
  );
  expect(profileRequest?.headers["x-csrf-token"]).toBe(authCSRFToken);
  expect(profileRequest?.headers.origin).toBe("http://127.0.0.1:4173");

  await activateInView(
    page
      .getByRole("navigation", { name: "Primary navigation" })
      .getByRole("link", { name: "User admin" }),
  );
  await expect(
    page.getByRole("heading", { name: "User administration" }),
  ).toBeVisible();
  await expect(page.getByText(address, { exact: true })).toBeVisible();
  await page
    .getByRole("combobox", { name: `Role for ${address}` })
    .selectOption("admin");
  await page
    .getByRole("combobox", { name: `Status for ${address}` })
    .selectOption("disabled");
  await activateInView(page.getByRole("button", { name: "Save user" }));
  await expect(page.getByRole("status")).toContainText(
    "Updated 0x111111…111111.",
  );
  await activateInView(
    page.getByRole("button", { name: "Revoke sessions" }),
  );
  await expect(page.getByRole("status")).toContainText(
    "Revoked 3 session(s) for 0x111111…111111.",
  );
  const adminPatchRequest = authRequests.find(
    ({ method, pathname }) =>
      method === "PATCH" &&
      pathname === `/api/v1/admin/users/${authTargetUserID}`,
  );
  const adminRevokeRequest = authRequests.find(
    ({ method, pathname }) =>
      method === "POST" &&
      pathname ===
        `/api/v1/admin/users/${authTargetUserID}/sessions/revoke`,
  );
  expect(adminPatchRequest?.headers["x-csrf-token"]).toBe(authCSRFToken);
  expect(adminRevokeRequest?.headers["x-csrf-token"]).toBe(authCSRFToken);

  await activateInView(page.getByRole("button", { name: "Next page" }));
  await expect(
    page
      .getByRole("navigation", {
        name: "Administrative user pages",
      })
      .getByText("Page 2", { exact: true }),
  ).toBeVisible();
  const cursorRequest = authRequests.find(({ pathname, search }) => {
    if (pathname !== "/api/v1/admin/users") return false;
    return new URLSearchParams(search).get("cursor") === authUserCursor;
  });
  expect(cursorRequest).toBeDefined();

  await activateInView(
    page
      .getByRole("navigation", { name: "Primary navigation" })
      .getByRole("link", { name: "Billing admin" }),
  );
  await expect(
    page.getByRole("heading", { name: "Billing administration" }),
  ).toBeVisible();
  await expect(
    page.getByText("Settlement unknown", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText(billingAdminUserID, { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText(billingAPIKeyPrefix, { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText(billingAmount, { exact: true }).first(),
  ).toBeVisible();
  await expect(
    page.getByText(billingCount, { exact: true }).first(),
  ).toBeVisible();

  await page
    .getByRole("combobox", { name: "State" })
    .selectOption("settling");
  await page
    .getByRole("combobox", { name: "Operation" })
    .selectOption("getBlock");
  await page
    .getByRole("textbox", { name: "Network" })
    .fill("eip155:84532");
  await page
    .getByRole("textbox", { name: "Asset" })
    .fill(billingAsset);
  await activateInView(
    page.getByRole("button", { name: "Apply filters" }),
  );
  await expect
    .poll(() =>
      authRequests.some(({ pathname, search }) => {
        if (pathname !== "/api/v1/admin/billing/payments") return false;
        const query = new URLSearchParams(search);
        return (
          query.get("state") === "settling" &&
          query.get("operation") === "getBlock" &&
          query.get("network") === "eip155:84532" &&
          query.get("asset") === billingAsset
        );
      }),
    )
    .toBe(true);

  await activateInView(page.getByRole("button", { name: "Next page" }));
  await expect(
    page
      .getByRole("navigation", {
        name: "Administrative payment ledger pages",
      })
      .getByText("Page 2", { exact: true }),
  ).toBeVisible();
  const billingCursorRequest = authRequests.find(
    ({ pathname, search }) =>
      pathname === "/api/v1/admin/billing/payments" &&
      new URLSearchParams(search).get("cursor") === billingAdminCursor,
  );
  expect(billingCursorRequest).toBeDefined();

  const billingRequests = authRequests.filter(({ pathname }) =>
    pathname.includes("/billing/"),
  );
  expect(billingRequests.length).toBeGreaterThanOrEqual(6);
  for (const request of billingRequests) {
    expect(request.method).toBe("GET");
    expect(request.body).toBeNull();
    expect(request.headers["payment-signature"]).toBeUndefined();
    expect(request.headers["x-csrf-token"]).toBeUndefined();
  }
  expect(
    await page.evaluate(
      (token) =>
        [...Array(localStorage.length).keys()].every(
          (index) => localStorage.getItem(localStorage.key(index) ?? "") !== token,
        ),
      authCSRFToken,
    ),
  ).toBe(true);
  await expect(page.locator("body")).not.toContainText(authCSRFToken);

  await activateInView(page.getByRole("button", { name: "切换到中文" }));
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(
    page.getByRole("heading", { name: "计费管理", level: 1 }),
  ).toBeVisible();
  const adminScan = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
    .analyze();
  expect(
    adminScan.violations,
    JSON.stringify(adminScan.violations, null, 2),
  ).toEqual([]);
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(1);

  await page.evaluate(() => {
    (window as AuthWalletWindow).__etherviewE2EAuthWallet.emit(
      "accountsChanged",
      ["0x4444444444444444444444444444444444444444"],
    );
  });
  await expect(
    page.getByRole("heading", { name: "需要已认证的用户会话。" }),
  ).toBeVisible();
  await expect
    .poll(
      () =>
        authRequests.filter(
          ({ method, pathname }) =>
            method === "POST" && pathname === "/api/v1/auth/logout",
        ).length,
    )
    .toBe(1);
  const logoutRequest = authRequests.find(
    ({ method, pathname }) =>
      method === "POST" && pathname === "/api/v1/auth/logout",
  );
  expect(logoutRequest?.headers["x-csrf-token"]).toBe(authCSRFToken);
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
            return mode === "invalid-call"
              ? { result: "0xfeed" }
              : `0x${"0".repeat(63)}2`;
          }
          if (method === "eth_sendTransaction") {
            if (mode === "delayed-write") {
              return await new Promise<string>((resolve) => {
                pendingWriteResolver = resolve;
              });
            }
            return transactionHash;
          }
          if (method === "wallet_addEthereumChain") return null;
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
  await activateInView(page.getByRole("button", { name: "Open contract" }));
  await expect(page.getByRole("heading", { name: "Contract", level: 1 })).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "TransparentUpgradeableProxy", level: 2 }),
  ).toBeVisible();
  await expect(page.getByRole("tab", { name: "Proxy management" })).toBeVisible();
  await expect(
    page.getByRole("link", { name: "Etherview home" })
      .getByText("Ethereum", { exact: true }),
  ).toBeVisible();

  recordWalletBoundary = true;
  await expect(
    page.locator("footer").getByRole("button", { name: "Add Ethereum network" }),
  ).toHaveCount(0);
  await activateInView(page.locator(".wallet-summary"));
  const walletPopover = page.locator(".wallet-popover");
  const addNetworkButton = walletPopover.getByRole("button", {
    name: "Add Ethereum network",
  });
  await expect(addNetworkButton).toBeVisible();
  await activateInView(addNetworkButton);
  await expect.poll(async () => page.evaluate(
    () => (window as WalletWindow).__etherviewE2EWallet.requests,
  )).toContainEqual(expect.objectContaining({ method: "wallet_addEthereumChain" }));
  const addNetworkRequests = await page.evaluate(
    () => (window as WalletWindow).__etherviewE2EWallet.requests,
  );
  expect(addNetworkRequests).toEqual([{
    method: "wallet_addEthereumChain",
    params: [{
      chainId: "0x1",
      chainName: "Ethereum",
      nativeCurrency: { name: "Ether", symbol: "ETH", decimals: 18 },
      rpcUrls: ["http://localhost:8545"],
    }],
  }]);
  await expect(walletPopover.locator(".wallet-option")).toContainText(longWalletName);
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

  await activateInView(page.getByRole("tab", { name: "Read contract" }));
  await expect(page.getByRole("button", { name: "Read contract" })).toBeEnabled();
  await activateInView(page.getByRole("button", { name: "Read contract" }));
  await expect(page.locator(".abi-output").getByText("2", { exact: true })).toBeVisible();
  await activateInView(page.getByRole("tab", { name: "Write contract" }));
  await page.getByLabel("newValue").fill("15");
  await activateInView(page.getByRole("button", { name: "Send transaction" }));
  await expect(page.getByText(walletTransactionHash, { exact: true })).toBeVisible();
  expect(backendRequests).toEqual([]);

  const requests = await page.evaluate(
    () => (window as WalletWindow).__etherviewE2EWallet.requests,
  );
  expect(requests.find(({ method }) => method === "eth_call")).toEqual({
    method: "eth_call",
    params: [
      expect.objectContaining({
        chainId: "0x1",
        from: walletAccount,
        to: address,
      }),
      "latest",
    ],
  });
  expect(requests.find(({ method }) => method === "eth_sendTransaction")).toEqual({
    method: "eth_sendTransaction",
    params: [expect.objectContaining({
      chainId: "0x1",
      from: walletAccount,
      to: address,
    })],
  });
  const generatedCalls = requests.filter(({ method }) =>
    method === "eth_call" || method === "eth_sendTransaction");
  for (const request of generatedCalls) {
    const transaction = request.params?.[0] as Record<string, unknown>;
    expect(transaction.data).toMatch(/^0x[0-9a-f]+$/u);
    expect(transaction.data).not.toBe("0x1234");
    expect(transaction.value).toBeUndefined();
  }
  expect(
    requests.every(({ method }) =>
      [
        "eth_accounts",
        "eth_call",
        "eth_chainId",
        "eth_requestAccounts",
        "eth_sendTransaction",
        "wallet_addEthereumChain",
      ].includes(method),
    ),
  ).toBe(true);
  await expect(page.getByLabel(/private key/i)).toHaveCount(0);

  await page.evaluate(() => {
    (window as WalletWindow).__etherviewE2EWallet.setMode("delayed-write");
  });
  await activateInView(page.getByRole("button", { name: "Send transaction" }));
  await expect(page.getByRole("button", { name: "Waiting for wallet…" })).toBeDisabled();
  await page.evaluate(
    ({ account }) => {
      const wallet = (window as WalletWindow).__etherviewE2EWallet;
      wallet.emit("accountsChanged", ["0x4444444444444444444444444444444444444444"]);
      wallet.emit("accountsChanged", [account]);
    },
    { account: walletAccount },
  );
  await expect(page.getByRole("button", { name: "Waiting for wallet…" })).toBeDisabled();
  await page.evaluate(
    ({ transactionHash }) => {
      (window as WalletWindow).__etherviewE2EWallet.resolveWrite(transactionHash);
    },
    { transactionHash: walletTransactionHash },
  );
  await expect(
    page.locator(".abi-function-card").getByRole("alert").filter({
      hasText:
        "The wallet changed while the transaction was pending. Its outcome is unknown; check your wallet before retrying.",
    }),
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
  await expect(
    page.getByRole("alert").filter({ hasText: "注入式钱包已断开连接。" }),
  ).toBeVisible();
  await expect(page.locator(".wallet-summary")).toBeFocused();
  await expect(page.getByText(/secret-wallet-message/)).toHaveCount(0);
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
  await expect(
    page.getByRole("alert").filter({ hasText: "钱包请求已被拒绝。" }),
  ).toBeVisible();
  await expect(page.getByText(/secret-wallet-message/)).toHaveCount(0);

  await page.evaluate(() => {
    (window as WalletWindow).__etherviewE2EWallet.setMode("normal");
  });
  await activateInView(page.locator(".wallet-option"));
  await page.evaluate(() => {
    (window as WalletWindow).__etherviewE2EWallet.setMode("invalid-call");
  });
  await activateInView(page.getByRole("tab", { name: "读取合约" }));
  await activateInView(page.getByRole("button", { name: "读取合约" }));
  await expect(
    page.getByRole("alert").filter({ hasText: "注入式钱包返回了无效响应。" }),
  ).toBeVisible();
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

  await expect(page.locator(".wallet-summary")).toHaveAttribute(
    "aria-label",
    /Connected E2E Wallet wallet/u,
  );
  await activateInView(page.getByRole("tab", { name: "Read contract" }));
  await expect(page.getByRole("button", { name: "Read contract" })).toBeDisabled();
  await activateInView(page.getByRole("tab", { name: "Write contract" }));
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
  await expect(page.locator(".wallet-popover")).toContainText("链 2");
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

async function assertA11yAndNoOverflow(
  page: import("@playwright/test").Page,
  context: string,
) {
  const scan = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
    .analyze();
  expect(scan.violations, `${context}\n${JSON.stringify(scan.violations, null, 2)}`).toEqual([]);
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(overflow, context).toBeLessThanOrEqual(1);
}
