import { expect, test, type Locator } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

const address = "0x1111111111111111111111111111111111111111";
const unverifiedAddress = "0x1212121212121212121212121212121212121212";
const delegatedAddress = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266";
const clearedDelegationAddress = "0x7777777777777777777777777777777777777777";
const delegatedDelegate = "0x5FbDB2315678afecb367f032d93F642f64180aa3";
const transparentImplementation = "0x3000000000000000000000000000000000000030";
const uupsProxyAddress = "0x3000000000000000000000000000000000000003";
const uupsImplementation = "0x4000000000000000000000000000000000000004";
const beaconProxyAddress = "0x5000000000000000000000000000000000000005";
const beaconImplementation = "0x6000000000000000000000000000000000000006";
const cloneAddress = "0x7000000000000000000000000000000000000007";
const cloneImplementation = "0x8000000000000000000000000000000000000008";
const cwiaAddress = "0xa00000000000000000000000000000000000000a";
const cwiaReadOnlyAddress = "0xc00000000000000000000000000000000000000c";
const cwiaUnverifiedAddress = "0xe00000000000000000000000000000000000000e";
const cwiaImplementation = "0xb00000000000000000000000000000000000000b";
const cwiaCodeHashImplementation = "0xf00000000000000000000000000000000000000f";
const proxyAdminAddress = "0x9000000000000000000000000000000000000009";
const upgradeableBeacon = "0x2000000000000000000000000000000000000020";
const oldImplementation = "0x4000000000000000000000000000000000000040";
const diamondAddress = "0xd000000000000000000000000000000000000000";
const diamondWriteFacet = "0xd100000000000000000000000000000000000001";
const diamondLoupeFacet = "0xd200000000000000000000000000000000000002";
const diamondInitAddress = "0xd300000000000000000000000000000000000003";
const codeHash = "0x1111111111111111111111111111111111111111111111111111111111111111";
const secondBlockHash = "0x2222222222222222222222222222222222222222222222222222222222222222";
const decodedTransactionHash = `0x${"a".repeat(64)}`;
const compoundTransactionHash = `0x${"ab".repeat(32)}`;
const failedTransactionHash = `0x${"d".repeat(64)}`;
const pendingTransactionHash = `0x${"c".repeat(64)}`;
const predecessorTransactionHash = `0x${"9".repeat(64)}`;
const replacementTransactionHash = `0x${"8".repeat(64)}`;
const delegationTransactionHash = `0x${"e".repeat(64)}`;
const clearingTransactionHash = `0x${"f".repeat(64)}`;
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
const userAPIKeyToken = `evk_abcdefghij_${"a".repeat(43)}`;
const rotatedUserAPIKeyToken = `evk_bcdefghij2_${"b".repeat(43)}`;

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

  await page.goto("/contracts");
  await expect(page.getByRole("heading", { name: /404 ·/ })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "主导航" }).getByRole("link", { name: "合约" })).toHaveCount(0);
});

test("verification compiler versions preserve semantic order", async ({ page }) => {
  const versions = [
    "0.8.3+commit.8d00100c",
    "0.8.20+commit.a1b79de6",
    "0.8.30+commit.73712a01",
  ];
  await page.route("**/api/v1/config", async (route) => {
    const response = await route.fetch();
    const payload = await response.json() as {
      data: { features: { verification: boolean } };
    };
    payload.data.features.verification = true;
    await route.fulfill({ response, json: payload });
  });
  await page.route("**/api/v1/verifier/compilers?language=solidity", async (route) => {
    await fulfillAPIEnvelope(route, { language: "solidity", versions });
  });

  const response = await page.goto("/verify");
  expect(response?.status()).toBe(200);
  const compilerVersion = page.getByLabel("Compiler version");
  await expect(compilerVersion).toHaveValue(versions[0]);
  expect(await compilerVersion.locator("option").allTextContents()).toEqual(versions);
  await assertA11yAndNoOverflow(page, "semantic compiler version order");
});

test("ENS primary names stay snapshot-stable, disclose addresses, and normalize search", async ({ page }) => {
  const snapshots: string[] = [];
  await page.route("**/api/v1/config", async (route) => {
    const response = await route.fetch();
    const payload = await response.json() as { data: { features: Record<string, boolean> } };
    payload.data.features.ens = true;
    await route.fulfill({ response, json: payload });
  });
  await page.route("**/api/v1/address-names**", async (route) => {
    const url = new URL(route.request().url());
    const snapshot = url.searchParams.get("snapshot");
    snapshots.push(snapshot ?? "<initial>");
    const addresses = (url.searchParams.get("addresses") ?? "").split(",").filter(Boolean);
    await fulfillAPIEnvelope(route, {
      snapshot: snapshot ?? "ens-browser-snapshot",
      items: addresses.map((value) => value.toLowerCase() === address.toLowerCase()
        ? {
            address: value,
            state: "resolved",
            primary_name: {
              name: "alice-with-an-intentionally-long-name.custom",
              source: "custom_ens",
            },
          }
        : { address: value, state: "not_found" }),
    });
  });
  await page.route("**/api/v1/search**", async (route) => {
    await fulfillAPIEnvelope(route, []);
  });

  await page.goto(`/address/${address}`);
  const primary = page.getByText("alice-with-an-intentionally-long-name.custom", { exact: true }).first();
  await expect(primary).toBeVisible();
  await expect(primary).toHaveAttribute("title", "alice-with-an-intentionally-long-name.custom");
  await expect(page.getByText("Custom ENS", { exact: true }).first()).toBeVisible();
  await expect(page.getByTitle(address).first()).toBeVisible();

  expect(snapshots).toContain("<initial>");

  await page.goto("/transactions");

  const search = page.getByRole("searchbox", { name: "Search" });
  await search.fill("RaFFY🚴‍♂️.eTh");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await expect(page).toHaveURL(/q=raffy/);
  await expect(page.getByText("raffy🚴‍♂.eth", { exact: true })).toBeVisible();

  await page.setViewportSize({ width: 390, height: 844 });
  await assertA11yAndNoOverflow(page, "ENS primary-name search at 390px");
});

test("transaction calldata separates decoded evidence from the read-only raw value", async ({ page }) => {
  const traceRequests: string[] = [];
  const internalTransactionRequests: string[] = [];
  page.on("request", (request) => {
    const pathname = new URL(request.url()).pathname;
    if (pathname === `/api/v1/transactions/${decodedTransactionHash}/trace`) {
      traceRequests.push(pathname);
    }
    if (pathname === `/api/v1/transactions/${decodedTransactionHash}/internal-transactions`) {
      internalTransactionRequests.push(pathname);
    }
  });
  await page.goto(`/tx/${decodedTransactionHash}`);
  await expect(page.getByRole("heading", { name: "Internal Transactions" })).toHaveCount(0);
  expect(internalTransactionRequests).toEqual([]);

  const internalTab = page.getByRole("tab", { name: "Internal Transactions" });
  const tokenTab = page.getByRole("tab", { name: "Token transfers" });
  await expect(internalTab).toBeVisible();
  expect(await internalTab.evaluate((element) => element.nextElementSibling?.textContent))
    .toBe(await tokenTab.textContent());
  await activateInView(internalTab);
  await expect(page).toHaveURL(new RegExp(`\\?tab=internal-transactions$`));
  const internalTransactions = page.getByRole("tabpanel", { name: "Internal Transactions" });
  await expect(internalTransactions.getByText("CALL", { exact: true })).toBeVisible();
  await expect(internalTransactions.getByText("1.25", { exact: true })).toBeVisible();
  expect(internalTransactionRequests).toHaveLength(1);
  expect(traceRequests).toEqual([]);

  await activateInView(tokenTab);
  await expect(page.getByText("1.2345", { exact: true })).toBeVisible();
  await activateInView(page.getByRole("tab", { name: "Overview" }));
  await activateInView(page.getByText("More details", { exact: true }));

  const decoded = page.getByRole("region", { name: "Decoded calldata · value()" });
  const raw = page.getByRole("region", { name: "Raw calldata" });
  await expect(decoded.getByText("value()", { exact: true })).toHaveCount(1);
  await expect(decoded.getByText("No parameters", { exact: true })).toBeVisible();
  const evidence = decoded.getByLabel("ABI evidence");
  await expect(evidence.getByText("Transaction-time execution", { exact: true })).toBeVisible();
  await expect(evidence.getByText("Direct code", { exact: true })).toBeVisible();
  await expect(evidence.getByText("ABI source · proxy_implementation", { exact: true })).toBeVisible();
  await expect(evidence.getByRole("link", { name: transparentImplementation })).toBeVisible();

  const rawValue = raw.getByRole("textbox", { name: "Raw calldata (Hex)" });
  await expect(rawValue).toHaveAttribute("readonly", "");
  await expect(rawValue).toHaveAttribute("wrap", "soft");
  await expect(rawValue).toHaveValue("0x3fa4f245");
  expect(await rawValue.evaluate((element) => getComputedStyle(element).whiteSpace)).toBe("pre-wrap");
  await expect(raw.getByRole("button", { name: "View as UTF-8" })).toBeVisible();
  await expect(raw.getByRole("button", { name: "Copy" })).toBeVisible();

  await activateInView(page.getByRole("button", { name: "切换到中文" }));
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole("region", { name: "已解码 calldata · value()" })).toBeVisible();
  const rawChinese = page.getByRole("region", { name: "原始 calldata" });
  await expect(rawChinese.getByRole("textbox", { name: "原始 calldata（十六进制）" })).toHaveValue("0x3fa4f245");
  await expect(rawChinese.getByRole("button", { name: "按 UTF-8 查看" })).toBeVisible();
  await assertAccessibleRoute(page, `/tx/${decodedTransactionHash}`);
});

test("transaction calldata renders a localized responsive recursive struct tree", async ({ page }) => {
  const requestedPaths: string[] = [];
  page.on("request", (request) => requestedPaths.push(new URL(request.url()).pathname));

  await page.goto(`/tx/${compoundTransactionHash}`);
  await activateInView(page.getByText("More details", { exact: true }));
  const decoded = page.getByRole("region", {
    name: "Decoded calldata · configure((address,uint256),uint8[2][])",
  });
  await expect(decoded.getByText("Config", { exact: true })).toBeVisible();
  await expect(decoded.getByText("config.owner", { exact: true })).toBeVisible();
  await expect(decoded.getByText("config.amount", { exact: true })).toBeVisible();
  await expect(decoded.getByText("2 items", { exact: true })).not.toHaveCount(0);
  await expect(decoded.getByText("#0", { exact: true })).not.toHaveCount(0);
  await expect(decoded.getByText("42", { exact: true })).toBeVisible();
  await expect(decoded.getByText('[["1","2"],["3","4"]]', { exact: true })).toHaveCount(0);

  const array = decoded.locator("details.calldata-array.calldata-depth-0").first();
  const summary = array.locator(":scope > summary");
  await expect(array).toHaveAttribute("open", "");
  await summary.focus();
  await page.keyboard.press("Enter");
  await expect(array).not.toHaveAttribute("open", "");
  await expect(summary).toBeFocused();
  await page.keyboard.press("Space");
  await expect(array).toHaveAttribute("open", "");

  await activateInView(page.getByRole("button", { name: "切换到中文" }));
  await page.setViewportSize({ width: 390, height: 844 });
  const decodedChinese = page.getByRole("region", {
    name: "已解码 calldata · configure((address,uint256),uint8[2][])",
  });
  await expect(decodedChinese.getByText("2 项", { exact: true })).not.toHaveCount(0);
  await expect(page.getByRole("textbox", { name: "原始 calldata（十六进制）" }))
    .toHaveValue(/^0xe967f546/u);
  await assertA11yAndNoOverflow(page, "recursive calldata tree in Chinese at 390px");

  expect(requestedPaths).not.toContain(`/api/v1/contracts/${address}/verification`);
  expect(requestedPaths).not.toContain(`/api/v1/contracts/${address}/proxy`);
  expect(requestedPaths).not.toContain(`/api/v1/addresses/${address}/delegation`);
});

test("failed transaction renders decoded custom error leaves in Name Type Data columns", async ({ page }) => {
  await page.goto(`/tx/${failedTransactionHash}`);

  const table = page.getByRole("table", { name: "Failure arguments" });
  await expect(table.getByRole("columnheader")).toHaveText(["Name", "Type", "Data"]);
  for (const name of ["sender", "amount", "pair[0]", "pair[1]", "values[1]", "items[0][2]"]) {
    await expect(table.getByText(name, { exact: true })).toBeVisible();
  }
  await expect(table.getByText("pair", { exact: true })).toHaveCount(0);
  await expect(table.getByText("items[0]", { exact: true })).toHaveCount(0);
  await expect(page.getByText(
    "TransferRejected(address,uint256,(address,uint256),uint256[],uint8[3][])",
    { exact: true },
  )).toBeVisible();

  await activateInView(page.getByRole("button", { name: "切换到中文" }));
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole("table", { name: "失败参数" }).getByRole("columnheader"))
    .toHaveText(["名称", "类型", "数据"]);
  await assertA11yAndNoOverflow(page, "decoded transaction failure in Chinese at 390px");
});

test("Solidity builtin failure renders concise error text without an ABI table", async ({ page }) => {
  await page.route(`**/api/v1/transactions/${failedTransactionHash}/failure`, async (route) => {
    await fulfillAPIEnvelope(route, {
      chain_id: "1", block_number: "1", block_hash: codeHash,
      transaction_hash: failedTransactionHash, transaction_index: "0", state: "complete",
      error: "execution reverted", revert_data: `0x4e487b71${"0".repeat(62)}12`,
      decoding: {
        status: "decoded", error_name: "Panic", signature: "Panic(uint256)",
        reason: "division or modulo by zero",
        arguments: [{ name: "code", type: "uint256", value: "18", components: [] }],
        candidates: [], abi_source: { kind: "builtin" },
      },
    });
  });

  await page.goto(`/tx/${failedTransactionHash}`);
  await expect(page.getByText("division or modulo by zero", { exact: true })).toBeVisible();
  await expect(page.getByText("Panic(uint256)", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("table", { name: "Failure arguments" })).toHaveCount(0);
  await expect(page.getByText("code", { exact: true })).toHaveCount(0);
  await expect(page.getByText("18", { exact: true })).toHaveCount(0);
  await expect(page.getByText("Revert data", { exact: true })).toHaveCount(0);
});

test("mempool details poll from pending through replacement to inclusion with accessible status icons", async ({
  page,
}) => {
  test.setTimeout(30_000);
  await page.setViewportSize({ width: 320, height: 844 });
  let phase: "pending" | "replaced" | "included" = "pending";
  let detailRequests = 0;
  const derivedRequests: string[] = [];
  page.on("request", (request) => {
    const pathname = new URL(request.url()).pathname;
    if (pathname.startsWith(`/api/v1/transactions/${pendingTransactionHash}/`)) {
      derivedRequests.push(pathname);
    }
  });
  await page.route("**/api/v1/transactions/**", async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    if (pathname === `/api/v1/transactions/${pendingTransactionHash}/token-transfers`) {
      await fulfillAPIEnvelope(route, {
        state: "complete",
        chain_id: "1",
        block_number: "2",
        block_hash: codeHash,
        transaction_hash: pendingTransactionHash,
        canonical: true,
        finality: "safe",
        items: [],
      });
      return;
    }
    if (pathname !== `/api/v1/transactions/${pendingTransactionHash}`) {
      await route.fallback();
      return;
    }
    detailRequests += 1;
    const transaction = {
      hash: pendingTransactionHash,
      from: address,
      nonce: "9007199254740993",
      value: "900719925474099312345",
      gas: "100000",
      max_fee_per_gas: "30000000000",
      max_priority_fee_per_gas: "1000000000",
      type: "2",
      input: "0x6000",
      replaces_hash: predecessorTransactionHash,
      first_seen_at: "2026-08-13T00:00:00Z",
      last_seen_at: "2026-08-13T00:00:01Z",
      expires_at: "2099-01-01T00:00:00Z",
      endpoint: "pending-primary",
    };
    if (phase === "pending") {
      await fulfillAPIEnvelope(route, { kind: "pending", transaction });
      return;
    }
    if (phase === "replaced") {
      await fulfillAPIEnvelope(route, {
        kind: "replaced",
        transaction,
        replacement_hash: replacementTransactionHash,
        replaced_at: "2026-08-13T00:00:02Z",
      });
      return;
    }
    await fulfillAPIEnvelope(route, {
      kind: "included",
      transaction: {
        hash: pendingTransactionHash,
        block_hash: codeHash,
        block_number: "2",
        transaction_index: 0,
        block_timestamp: "2026-08-13T00:00:03Z",
        from: address,
        nonce: "9007199254740993",
        value: "900719925474099312345",
        gas: "100000",
        gas_used: "53000",
        type: "2",
        input: "0x6000",
        status: "success",
        canonical: true,
        finality: "safe",
        contract_address: delegatedAddress,
        completeness: { core: "complete", trace: "complete", metadata: "complete", state: "complete" },
      },
    });
  });

  await page.goto("/pending");
  const pendingLink = page.getByRole("link", { name: "0xcccccc…cccccc" });
  const pendingRow = page.getByRole("row").filter({ has: pendingLink });
  const listStatus = pendingRow.locator('[data-status="pending"]');
  await expect(listStatus).toBeVisible();
  await expect(listStatus.locator("svg.lucide-clock-3")).toHaveAttribute("aria-hidden", "true");
  await pendingLink.click();

  await expect(page.getByRole("heading", { name: "Waiting for confirmation" })).toBeVisible();
  const detailPendingStatus = page.locator('[data-status="pending"]').first();
  await expect(detailPendingStatus.locator("svg.lucide-clock-3")).toBeVisible();
  const pendingColor = await detailPendingStatus.evaluate((element) => getComputedStyle(element).color);
  await expect(page.getByRole("link", { name: predecessorTransactionHash })).toBeVisible();
  await expect(page.getByRole("textbox", { name: "Raw calldata (Hex)" })).toHaveValue("0x6000");
  await expect(page.getByText("Contract creation", { exact: true })).toBeVisible();
  expect(derivedRequests).toEqual([]);
  await assertA11yAndNoOverflow(page, "pending transaction detail in English light mode at 320px");

  phase = "replaced";
  await expect(page.getByRole("heading", { name: "Transaction replaced" })).toBeVisible({ timeout: 3_500 });
  const replacedStatus = page.locator('[data-status="replaced"]').first();
  await expect(replacedStatus.locator("svg.lucide-arrow-right-left")).toBeVisible();
  const replacedColor = await replacedStatus.evaluate((element) => getComputedStyle(element).color);
  expect(replacedColor).not.toBe(pendingColor);
  await expect(page.getByRole("link", { name: replacementTransactionHash })).toBeVisible();
  expect(derivedRequests).toEqual([]);

  await activateInView(page.getByRole("button", { name: "Switch color theme" }));
  await activateInView(page.getByRole("button", { name: "切换到中文" }));
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect(page.getByRole("heading", { name: "交易已被替换" })).toBeVisible();
  await assertA11yAndNoOverflow(page, "replaced transaction detail in Chinese dark mode at 320px");

  phase = "included";
  const successStatus = page.locator('[data-status="success"]').first();
  await expect(successStatus).toBeVisible({ timeout: 3_500 });
  await expect(successStatus.locator("svg.lucide-circle-check")).toHaveAttribute("aria-hidden", "true");
  await expect(page.getByRole("tablist", { name: "交易详情分区" })).toBeVisible();
  await expect.poll(() => derivedRequests).toContain(
    `/api/v1/transactions/${pendingTransactionHash}/token-transfers`,
  );
  expect(detailRequests).toBeGreaterThanOrEqual(3);
  await assertA11yAndNoOverflow(page, "included transaction detail in Chinese dark mode at 320px");
});

test("EIP-7702 transaction keeps authorization outcomes lazy and uses transaction-time delegate code", async ({ page }) => {
  const requestedURLs: URL[] = [];
  page.on("request", (request) => requestedURLs.push(new URL(request.url())));

  await page.goto(`/tx/${delegationTransactionHash}`);
  const transactionTabs = page.getByRole("tablist", { name: "Transaction detail sections" });
  await expect(page.getByText("Contract interaction", { exact: true })).toBeVisible();
  await expect(transactionTabs.getByRole("tab", { name: "Authorizations" })).toBeVisible();
  await expect(transactionTabs.getByRole("tab", { name: "Access list" })).toBeVisible();
  await expect(transactionTabs.getByRole("tab", { name: "Blob" })).toHaveCount(0);
  expect(requestedURLs.map((url) => url.pathname)).not.toContain(
    `/api/v1/transactions/${delegationTransactionHash}/authorizations`,
  );

  await activateInView(page.getByText("More details", { exact: true }));
  const typeLabel = page.getByText("Type", { exact: true });
  const typeRow = typeLabel.locator("..");
  await expect(typeRow.getByText("EIP-7702", { exact: true })).toBeVisible();
  const decoded = page.getByRole("region", { name: "Decoded calldata · setValue(uint256)" });
  await expect(decoded.getByText("EIP-7702 delegate code", { exact: true })).toBeVisible();
  await expect(decoded.getByRole("link", { name: delegatedDelegate })).toHaveCount(2);
  await expect(decoded.getByText("42", { exact: true })).toBeVisible();
  expect(requestedURLs.map((url) => url.pathname)).not.toContain(
    `/api/v1/addresses/${delegatedAddress}/delegation`,
  );
  expect(requestedURLs.map((url) => url.pathname)).not.toContain(
    `/api/v1/contracts/${delegatedAddress}/verification`,
  );

  await activateInView(transactionTabs.getByRole("tab", { name: "Authorizations" }));
  await expect(page).toHaveURL(new RegExp(`\\?tab=authorizations$`));
  const applied = page.locator("article.transaction-log").filter({ hasText: "Authorization #0" });
  await expect(applied.getByText("applied", { exact: true })).toBeVisible();
  await expect(applied.getByText(delegatedAddress, { exact: true })).toBeVisible();
  await expect(applied.getByText(delegatedDelegate, { exact: true })).toBeVisible();
  await activateInView(applied.getByText("Raw authorization signature", { exact: true }));
  await expect(applied.getByText("yParity", { exact: true })).toBeVisible();
  await expect(applied.getByText(codeHash, { exact: true })).toBeVisible();

  const authorizationPagination = page.getByRole("navigation", { name: "Authorizations" });
  await expect(authorizationPagination.getByRole("button", { name: "Previous page" })).toBeDisabled();
  await activateInView(authorizationPagination.getByRole("button", { name: "Next page" }));
  const skipped = page.locator("article.transaction-log").filter({ hasText: "Authorization #1" });
  await expect(skipped.getByText("skipped", { exact: true })).toBeVisible();
  await expect(skipped.getByText("valid", { exact: true })).toBeVisible();
  await expect(skipped.getByText("nonce_mismatch", { exact: true })).toBeVisible();
  expect(requestedURLs.some((url) =>
    url.pathname === `/api/v1/transactions/${delegationTransactionHash}/authorizations`
    && url.searchParams.get("cursor") === "authorization-next")).toBe(true);

  await page.goto(`/tx/${delegationTransactionHash}?tab=authorizations`);
  const deepLinkedTab = page.getByRole("tablist", { name: "Transaction detail sections" })
    .getByRole("tab", { name: "Authorizations" });
  await expect(deepLinkedTab).toHaveAttribute("aria-selected", "true");
  await expect(page.locator("article.transaction-log").filter({ hasText: "Authorization #0" })).toBeVisible();

  await page.setViewportSize({ width: 390, height: 844 });
  await activateInView(page.getByRole("button", { name: "切换到中文" }));
  await expect(page.getByRole("tablist", { name: "交易详情分区" }).getByRole("tab", { name: "授权" })).toBeVisible();
  await assertA11yAndNoOverflow(page, "EIP-7702 authorizations in Chinese narrow mode");
});

test("EIP-7702 clearing keeps raw calldata and never falls back to stale delegate code", async ({ page }) => {
  const requestedPaths: string[] = [];
  page.on("request", (request) => requestedPaths.push(new URL(request.url()).pathname));

  await page.goto(`/tx/${clearingTransactionHash}`);
  expect(requestedPaths).not.toContain(`/api/v1/transactions/${clearingTransactionHash}/authorizations`);
  await expect(page.getByText("EOA transaction", { exact: true })).toBeVisible();
  await expect(page.getByText("Contract interaction", { exact: true })).toHaveCount(0);
  await activateInView(page.getByText("More details", { exact: true }));
  await expect(page.getByText(/No executable code at transaction execution time/u)).toBeVisible();
  await expect(page.getByRole("textbox", { name: "Raw calldata (Hex)" })).toHaveValue("0x55241077");
  await expect(page.getByText("EIP-7702 delegate code", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("link", { name: delegatedDelegate })).toHaveCount(0);

  await activateInView(page.getByRole("tab", { name: "Authorizations" }));
  const clearing = page.locator("article.transaction-log").filter({ hasText: "Authorization #0" });
  await expect(clearing.getByText("applied", { exact: true })).toBeVisible();
  await expect(clearing.getByText("0x0000000000000000000000000000000000000000", { exact: true })).toBeVisible();
  expect(requestedPaths).toContain(`/api/v1/transactions/${clearingTransactionHash}/authorizations`);
  expect(requestedPaths).not.toContain(`/api/v1/addresses/${delegatedAddress}/delegation`);
  expect(requestedPaths).not.toContain(`/api/v1/contracts/${delegatedDelegate}/verification`);

  await page.setViewportSize({ width: 390, height: 844 });
  await activateInView(page.getByRole("button", { name: "切换到中文" }));
  await expect(page.getByText("授权 #0", { exact: true })).toBeVisible();
  await assertA11yAndNoOverflow(page, "EIP-7702 clearing in Chinese narrow mode");
});

test("trace and log disclosures retain raw data and exact execution provenance", async ({ page }) => {
  await page.goto(`/tx/${decodedTransactionHash}?tab=trace`);
  await expect(page.getByText("retrieve()", { exact: true })).toBeVisible();
  await expect(page.getByText("Succeeded", { exact: true })).toBeVisible();
  await expect(page.getByText("setOwner(address)", { exact: true })).toBeVisible();
  await expect(page.getByText("execution reverted", { exact: true })).toBeVisible();

  const rootFrame = page.locator(".transaction-trace-frame").first();
  const rootDisclosure = rootFrame.locator("summary");
  await rootDisclosure.focus();
  await expect(rootDisclosure).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(rootFrame.getByText("Declared empty return", { exact: true })).toHaveCount(0);
  await expect(rootFrame.getByText("Decoded", { exact: true })).toBeVisible();
  await expect(rootFrame.getByText("42", { exact: true })).toBeVisible();
  await expect(rootFrame.getByText("0x2e64cec1", { exact: true })).toBeVisible();

  const revertedFrame = page.locator(".transaction-trace-frame").nth(1);
  await activateInView(revertedFrame.locator("summary"));
  await expect(revertedFrame.getByText("Not applicable", { exact: true })).toBeVisible();
  await expect(revertedFrame.getByText("Panic(uint256)", { exact: true })).toBeVisible();
  await expect(revertedFrame.getByText("17", { exact: true })).toBeVisible();

  await activateInView(page.getByRole("tab", { name: "Logs" }));
  await expect(page.getByText("ValueChanged(uint256)", { exact: true })).toBeVisible();
  const log = page.locator(".transaction-log").first();
  const moreDetails = log.getByText("More details", { exact: true });
  await expect(log.locator(".transaction-log-details")).not.toHaveAttribute("open", "");
  await activateInView(moreDetails);
  await expect(log.getByRole("heading", { name: "ABI provenance", exact: true })).toBeVisible();
  await expect(log.getByText("Exact address", { exact: true })).toBeVisible();
  await expect(log.getByText("Actual execution code", { exact: true })).toBeVisible();
  const executionProvenance = log.locator(".transaction-log-provenance-card").filter({ hasText: "Actual execution code" });
  await expect(executionProvenance.getByRole("link", { name: uupsImplementation })).toBeVisible();
  await expect(log.getByText("Exact Trace frame", { exact: true })).toBeVisible();
  await expect(executionProvenance.getByText(/^\[\d+(, \d+)*\]$/)).toBeVisible();
  await expect(log.getByRole("link", { name: uupsProxyAddress }).first()).toBeVisible();
  await expect(log.getByText("Raw topics and data", { exact: true })).toHaveCount(0);
  await expect(log.getByRole("heading", { name: "Topics", exact: true })).toBeVisible();
  await expect(log.locator(".transaction-log-data code")).toBeVisible();
  const topicZeroBox = await log.locator(".transaction-topic").first().locator(".copyable-field code").boundingBox();
  const dataBox = await log.locator(".transaction-log-data code").boundingBox();
  expect(topicZeroBox).not.toBeNull();
  expect(dataBox).not.toBeNull();
  expect(Math.abs((topicZeroBox?.x ?? 0) - (dataBox?.x ?? 0))).toBeLessThanOrEqual(1);

  await activateInView(page.getByRole("button", { name: "切换到中文" }));
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(log.getByRole("heading", { name: "ABI 来源", exact: true, level: 3 })).toBeVisible();
  await expect(log.getByText("实际执行代码", { exact: true })).toBeVisible();
  await expect(log.getByText("精确 Trace 调用帧", { exact: true })).toBeVisible();
  await expect(log.getByText("原始 topics 与 data", { exact: true })).toHaveCount(0);
  await expect(log.getByRole("heading", { name: "主题", exact: true })).toBeVisible();
  const narrowTopicZeroBox = await log.locator(".transaction-topic").first().locator(".copyable-field code").boundingBox();
  const narrowDataBox = await log.locator(".transaction-log-data code").boundingBox();
  expect(narrowTopicZeroBox).not.toBeNull();
  expect(narrowDataBox).not.toBeNull();
  expect(Math.abs((narrowTopicZeroBox?.x ?? 0) - (narrowDataBox?.x ?? 0))).toBeLessThanOrEqual(1);
  await assertAccessibleRoute(page, `/tx/${decodedTransactionHash}?tab=trace`);
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
  const addressWithdrawalRequests: string[] = [];
  const calldataRequests: string[] = [];
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (url.pathname === "/api/v1/transactions" && url.searchParams.has("cursor")) {
      transactionCursors.push(url.searchParams.get("cursor") ?? "");
    }
    if (url.pathname === `/api/v1/addresses/${address}/withdrawals`) {
      addressWithdrawalRequests.push(url.pathname);
    }
    if (url.pathname.endsWith("/calldata")) calldataRequests.push(url.pathname);
  });

  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Coverage and finality context" })).toBeVisible();
  await expect(page.getByText("0 – 2", { exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "#2" })).toHaveAttribute(
    "href",
    "/blocks/0x2222222222222222222222222222222222222222222222222222222222222222",
  );
  await expect(page.getByRole("columnheader", { name: "Method" })).toHaveCount(0);

  await page.goto("/blocks");
  await expect(page.getByRole("note")).toContainText("This list contains canonical blocks only");
  await expect(page.getByRole("link", { name: "2" })).toBeVisible();
  await activateInView(page.getByRole("button", { name: "Next page" }));
  await expect(page.getByRole("link", { name: "1" })).toBeVisible();
  await expect(page.getByText("Page 2", { exact: true })).toBeVisible();

  await page.goto("/blocks/2?tab=withdrawals");
  await expect(page.getByRole("tab", { name: "Withdrawals" })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByText("3.2 Ether", { exact: true })).toBeVisible();

  await page.goto("/transactions");
  await expect(page.getByText("900.719925474099312345", { exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: /0xaaaaaa…aaaaaa/ })).toBeVisible();
  await expect(page.getByRole("columnheader", { name: "Method" })).toBeVisible();
  const method = page.getByLabel("valueWithAnIntentionallyLongMethodName(uint256,address)");
  await expect(method).toHaveText("valueWithAnIntentionallyLongMethodName");
  await method.hover();
  await expect(method).toHaveAttribute(
    "title",
    "valueWithAnIntentionallyLongMethodName(uint256,address)",
  );
  await expect(method).toHaveCSS("text-overflow", "ellipsis");
  await page.setViewportSize({ width: 390, height: 844 });
  const methodListOverflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(methodListOverflow).toBeLessThanOrEqual(1);
  await activateInView(page.getByRole("button", { name: "切换到中文" }));
  await expect(page.getByRole("columnheader", { name: "方法" })).toBeVisible();
  await activateInView(page.getByRole("button", { name: "Switch to English" }));
  expect(addressWithdrawalRequests).toEqual([]);
  await activateInView(page.getByRole("button", { name: "Next page" }));
  const secondPageTransaction = page.getByRole("link", { name: /0xbbbbbb…bbbbbb/ });
  await expect(secondPageTransaction).toBeVisible();
  await expect(page.getByText("Contract Creation", { exact: true })).toBeVisible();
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
  await expect(transactionSummary.getByText("Gas Limit & Usage by Txn")).toBeVisible();
  await expect(transactionSummary.getByText("567,028 | 430,551 (75.93%)")).toBeVisible();
  await expect(transactionSummary).toContainText("Base: 0.112489733 Gwei");
  await expect(transactionSummary).toContainText("Max: 0.151663696 Gwei");
  await expect(transactionSummary).toContainText("Max Priority: 0.02831988 Gwei");
  await expect(transactionSummary).toContainText("Blob Base Fee: 0.001 Gwei");
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
  await activateInView(page.getByRole("tab", { name: "Blob" }));
  const blobPanel = page.getByRole("tabpanel");
  await expect(blobPanel).toContainText("Blob Base Fee: 0.001 Gwei");
  await expect(blobPanel).toContainText("Max: 1 Gwei");
  await activateInView(page.getByRole("tab", { name: "Overview" }));
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
    `/address/${address}#code`,
  );
  await expect(page.getByRole("link", { name: /0xaaaaaa…aaaaaa/ })).toBeVisible();
  const addressTransactionsTable = page.getByRole("table", { name: "Transactions" });
  expect(await addressTransactionsTable.getByRole("columnheader").allTextContents()).toEqual([
    "Hash", "Method", "Block", "Timestamp", "Status", "From", "Direction", "To", "Value (ETH)", "Finality",
  ]);
  await expect(page.getByRole("columnheader", { name: "Method" })).toBeVisible();
  await expect(page.getByRole("columnheader", { name: "Direction" })).toBeVisible();
  const addressMethod = page.getByLabel(
    "valueWithAnIntentionallyLongMethodName(uint256,address)",
  );
  await expect(addressMethod).toHaveText("valueWithAnIntentionallyLongMethodName");
  await addressMethod.hover();
  await expect(addressMethod).toHaveAttribute(
    "title",
    "valueWithAnIntentionallyLongMethodName(uint256,address)",
  );
  await expect(addressMethod).toHaveCSS("text-overflow", "ellipsis");
  const addressStatusGroups = page.locator(
    '.table-scroll[aria-label="Transactions"] .transaction-status-group',
  );
  await expect(addressStatusGroups).toHaveCount(2);
  await expect(addressStatusGroups.nth(0).locator('[data-status="success"]')).toBeVisible();
  await expect(addressStatusGroups.nth(1).locator('[data-status="failed"]')).toBeVisible();
  await expect(addressStatusGroups.nth(0).locator(".finality-badge")).toHaveCount(0);
  await expect(addressStatusGroups.nth(1).locator(".finality-badge")).toHaveCount(0);
  const addressFinalityCells = addressTransactionsTable.locator("tbody tr td:last-child");
  await expect(addressFinalityCells).toHaveCount(2);
  await expect(addressFinalityCells.nth(0).locator(".finality-badge")).toBeVisible();
  await expect(addressFinalityCells.nth(1).locator(".finality-badge")).toBeVisible();
  const addressTransactionsScroller = page.locator(
    '.table-scroll[aria-label="Transactions"]',
  );
  await addressTransactionsScroller.focus();
  await expect(addressTransactionsScroller).toBeFocused();
  const addressMethodOverflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(addressMethodOverflow).toBeLessThanOrEqual(1);
  await activateInView(page.getByRole("button", { name: "切换到中文" }));
  await expect(page.getByRole("columnheader", { name: "方法" })).toBeVisible();
  await expect(page.getByRole("columnheader", { name: "方向" })).toBeVisible();
  await expect(page.getByRole("columnheader", { name: "最终性" })).toBeVisible();
  await activateInView(page.getByRole("button", { name: "Switch to English" }));
  expect(calldataRequests).toEqual([]);
  await page.goto(`/address/${address}#read-contract`);
  await expect(page.getByRole("heading", { name: "Contract", level: 1 })).toBeVisible();
  await expect(page.getByRole("link", { name: "Contract", exact: true })).toHaveClass(
    /\bactive\b/,
  );
  await activateInView(page.getByRole("link", { name: "Internal Transactions" }));
  await expect(page).toHaveURL(new RegExp(`/address/${address}\\?tab=internal-transactions$`));
  await expect(page.getByText("SELF", { exact: true })).toBeVisible();
  await expect(page.getByRole("columnheader", { name: "Method" })).toHaveCount(0);
  await expect(page.getByRole("columnheader", { name: "Finality" })).toHaveCount(0);
  expect(addressWithdrawalRequests).toEqual([]);
  const contractTab = page.getByRole("link", { name: "Contract", exact: true });
  await expect(contractTab).not.toHaveClass(/\bactive\b/);
  const [contractStyle, ordinaryTabStyle] = await Promise.all([
    contractTab.evaluate((element) => {
      const style = getComputedStyle(element);
      return {
        marginInlineStart: style.marginInlineStart,
        color: style.color,
        backgroundColor: style.backgroundColor,
        border: style.border,
      };
    }),
    page.getByRole("link", { name: "ERC-20 Transfers" }).evaluate((element) => {
      const style = getComputedStyle(element);
      return {
        marginInlineStart: style.marginInlineStart,
        color: style.color,
        backgroundColor: style.backgroundColor,
        border: style.border,
      };
    }),
  ]);
  expect(contractStyle).toEqual(ordinaryTabStyle);
  await page.goto(`/address/${address}?tab=withdrawals`);
  await expect(page.getByRole("link", { name: "Withdrawals" })).toHaveAttribute("aria-current", "page");
  const withdrawalRows = page.getByRole("table", { name: "Withdrawals" }).getByRole("row");
  await expect(withdrawalRows).toHaveCount(3);
  await expect(withdrawalRows.nth(1)).toContainText("10");
  await expect(withdrawalRows.nth(2)).toContainText("2");
  await expect(withdrawalRows.nth(1).getByText("3.2 Ether", { exact: true })).toBeVisible();
  await expect(withdrawalRows.nth(2).getByText("0.000000001 Ether", { exact: true })).toBeVisible();
  await expect(withdrawalRows.nth(1).getByRole("link", { name: "2" })).toHaveAttribute("href", `/blocks/${secondBlockHash}`);
  expect(addressWithdrawalRequests).toHaveLength(1);
  await activateInView(page.getByRole("button", { name: "切换到中文" }));
  await expect(page.getByRole("link", { name: "提款" })).toHaveAttribute("aria-current", "page");
  await expect(page.getByRole("columnheader", { name: "数量（Ether）" })).toBeVisible();
  await activateInView(page.getByRole("button", { name: "Switch to English" }));
  await activateInView(page.getByRole("link", { name: "ERC-20 Transfers" }));
  await expect(page.locator("tbody").getByText("ERC-20", { exact: true })).toBeVisible();
  await expect(page.locator("tbody").getByText("1.2345", { exact: true })).toBeVisible();
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

test("delegated-account panels keep shared layout and accessibility on narrow Preview pages", async ({ page }) => {
  const pageErrors: string[] = [];
  const consoleErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(message.text());
  });

  await page.goto(`/address/${delegatedAddress}?tab=delegation#code`);
  await expect(page.getByRole("heading", { name: "EIP-7702 delegation binding" })).toBeVisible();
  const tabs = page.getByRole("tablist", { name: "Delegated account sections" });
  await expect(tabs.getByRole("tab", { name: "Read contract" })).toBeVisible();
  await expect(tabs.getByRole("tab", { name: "Write contract" })).toBeVisible();
  await expect(tabs.getByRole("tab", { name: "Delegation history" })).toBeVisible();

  const bindingPanel = page.locator("section[aria-labelledby='delegation-binding-title']");
  await expect(bindingPanel).toHaveClass(/detail-card/);
  const layout = await bindingPanel.evaluate((element) => {
    const detailGrid = element.querySelector(".detail-grid");
    const detailItem = element.querySelector(".detail-item");
    const panelStyle = getComputedStyle(element);
    return {
      padding: panelStyle.padding,
      gridDisplay: detailGrid ? getComputedStyle(detailGrid).display : "",
      itemDisplay: detailItem ? getComputedStyle(detailItem).display : "",
    };
  });
  expect(layout.padding).not.toBe("0px");
  expect(layout.gridDisplay).toBe("grid");
  expect(layout.itemDisplay).toBe("grid");

  await tabs.getByRole("tab", { name: "Write contract" }).click();
  await expect(page.getByText("disperseToken(address,(address,uint256)[])", { exact: true })).toBeVisible();
  await tabs.getByRole("tab", { name: "Delegation history" }).click();
  await expect(page.getByRole("heading", { name: "Delegation history" })).toBeVisible();
  await expect(page.getByText("Re-delegated", { exact: true })).toBeVisible();
  const historyNavigation = page.getByRole("navigation", { name: "Delegation history" });
  await expect(historyNavigation.getByRole("button", { name: "Previous page" })).toBeDisabled();
  await historyNavigation.getByRole("button", { name: "Next page" }).click();
  await expect(page.getByText("Delegated", { exact: true })).toBeVisible();

  await page.setViewportSize({ width: 390, height: 844 });
  await assertA11yAndNoOverflow(page, "delegated account in English narrow mode");
  await page.getByRole("button", { name: "切换到中文" }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", "zh-CN");
  await assertA11yAndNoOverflow(page, "delegated account in Chinese narrow mode");
  expect(pageErrors).toEqual([]);
  expect(consoleErrors).toEqual([]);
});

test("cleared delegated accounts open canonical history without loading current binding", async ({ page }) => {
  const requestedPaths: string[] = [];
  page.on("request", (request) => requestedPaths.push(new URL(request.url()).pathname));

  await page.goto(`/address/${clearedDelegationAddress}`);
  const addressTabs = page.getByRole("navigation", { name: "Address activity sections" });
  const delegationEntry = addressTabs.getByRole("link", { name: "Delegation" });
  await expect(delegationEntry).toHaveAttribute(
    "href",
    `/address/${clearedDelegationAddress}?tab=delegation#history`,
  );
  expect(requestedPaths).not.toContain(`/api/v1/addresses/${clearedDelegationAddress}/delegations`);

  await delegationEntry.click();
  await expect(page.getByRole("heading", { name: "Delegation history" })).toBeVisible();
  await expect(page.getByText("Cleared", { exact: true })).toBeVisible();
  expect(requestedPaths).toContain(`/api/v1/addresses/${clearedDelegationAddress}/delegations`);
  expect(requestedPaths).not.toContain(`/api/v1/addresses/${clearedDelegationAddress}/delegation`);
  expect(requestedPaths).not.toContain(`/api/v1/contracts/${delegatedDelegate}/verification`);

  const delegatedTabs = page.getByRole("tablist", { name: "Delegated account sections" });
  await delegatedTabs.getByRole("tab", { name: "Status" }).click();
  await expect(page).toHaveURL(new RegExp(`/address/${clearedDelegationAddress}\\?tab=delegation#code$`));
  await expect(page.getByRole("heading", { name: "Delegation status" })).toBeVisible();
  await expect(page.getByText("Not delegated", { exact: true })).toBeVisible();
  await expect(page.getByText(/currently has no active EIP-7702 delegation/)).toBeVisible();
  expect(requestedPaths).toContain(`/api/v1/addresses/${clearedDelegationAddress}/delegation`);
  expect(requestedPaths).not.toContain(`/api/v1/contracts/${delegatedDelegate}/verification`);
  await expect(page.getByRole("heading", { name: "Verified artifact" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "View delegation history" })).toHaveCount(0);
  await page.setViewportSize({ width: 390, height: 844 });
  await assertA11yAndNoOverflow(page, "cleared delegated account status in narrow mode");
  await page.getByRole("button", { name: "切换到中文" }).click();
  const localizedTabs = page.getByRole("tablist", { name: "委托账户分区" });
  await expect(localizedTabs.getByRole("tab", { name: "状态" })).toBeVisible();
  await assertA11yAndNoOverflow(page, "cleared delegated account status in Chinese narrow mode");
});

test("capability pages survive the embedded binary boundary in both accessible themes and languages", async ({
  page,
}) => {
  test.setTimeout(120_000);
  const nftImageURL = "https://media.example.invalid/nft.png?token=fixture";
  await page.context().route("https://media.example.invalid/**", async (route) => {
    await route.fulfill({ status: 200, contentType: "text/plain", body: "external fixture" });
  });
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
  await expect(page.getByRole("heading", { name: "Example Collectible #1", exact: true, level: 1 })).toBeVisible();
  await expect(page.getByRole("heading", { name: "NFT instance", exact: true, level: 2 })).toBeVisible();
  await expect(page.getByRole("heading", { name: "NFT ownership", level: 2 })).toBeVisible();
  const metadataRegion = page.getByRole("region", { name: "NFT metadata" });
  await expect(metadataRegion.getByText("Plain fixture metadata; no image is embedded.")).toBeVisible();
  await expect(metadataRegion.locator(".nft-trait dd")).toContainText("9007199254740993");
  await expect(metadataRegion.locator("img")).toHaveCount(0);
  const reviewImage = metadataRegion.getByRole("button", {
    name: `Review unverified external image target ${nftImageURL}`,
  });
  await activateInView(reviewImage);
  const warningDialog = page.getByRole("dialog", { name: "Open an unverified external link?" });
  await expect(warningDialog).toBeVisible();
  await expect(warningDialog.getByRole("alert")).toContainText("connects your browser directly to a third party");
  expect(externalRequests).toEqual([]);
  await assertA11yAndNoOverflow(page, "NFT external-link warning in English");
  const externalLink = warningDialog.getByRole("link", { name: "Open in new tab" });
  await expect(externalLink).toHaveAttribute("href", nftImageURL);
  await expect(externalLink).toHaveAttribute("target", "_blank");
  await expect(externalLink).toHaveAttribute("rel", "external noopener noreferrer");
  await expect(externalLink).toHaveAttribute("referrerpolicy", "no-referrer");
  const popupPromise = page.waitForEvent("popup");
  await externalLink.click();
  const popup = await popupPromise;
  await popup.waitForLoadState("domcontentloaded");
  expect(popup.url()).toBe(nftImageURL);
  await popup.close();
  externalRequests.length = 0;

  await activateInView(page.getByRole("button", { name: "切换到中文" }));
  const chineseMetadataRegion = page.getByRole("region", { name: "NFT 元数据" });
  await activateInView(chineseMetadataRegion.getByRole("button", {
    name: `检查未经验证的外部图片目标 ${nftImageURL}`,
  }));
  const chineseWarning = page.getByRole("dialog", { name: "打开未经验证的外部链接？" });
  await expect(chineseWarning.getByRole("alert")).toContainText("由浏览器直接连接第三方");
  expect(externalRequests).toEqual([]);
  await activateInView(chineseWarning.getByRole("button", { name: "取消" }));
  await activateInView(page.getByRole("button", { name: "Switch to English" }));

  await page.goto(`/nft/${address}/2`);
  const staleMetadata = page.getByRole("region", { name: "NFT metadata" });
  await expect(staleMetadata.getByText("A newer metadata refresh is not available yet")).toBeVisible();
  await expect(staleMetadata.getByText(/refresh at block 3 is Pending/)).toContainText("block 2");
  await expect(staleMetadata.getByText("Prior canonical metadata remains visible.")).toBeVisible();
  await expect(staleMetadata.locator("img")).toHaveCount(0);
  expect(externalRequests).toEqual([]);
  await assertA11yAndNoOverflow(page, "stale NFT metadata in English");
  await activateInView(page.getByRole("button", { name: "切换到中文" }));
  const staleChineseMetadata = page.getByRole("region", { name: "NFT 元数据" });
  await expect(staleChineseMetadata.getByText("较新的元数据刷新尚不可用")).toBeVisible();
  await expect(staleChineseMetadata.getByText(/区块 3 的刷新状态为处理中/)).toContainText("区块 2");
  await assertA11yAndNoOverflow(page, "stale NFT metadata in Chinese");
  await activateInView(page.getByRole("button", { name: "Switch to English" }));

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

  await page.goto(`/address/${unverifiedAddress}#code`);
  const verificationEntry = page.getByRole("link", { name: "Submit a verification request" });
  await expect(page.getByText(/This contract has not been verified yet/)).toBeVisible();
  await expect(verificationEntry).toHaveAttribute(
    "href",
    `/verify?address=${unverifiedAddress}`,
  );
  await page.setViewportSize({ width: 390, height: 844 });
  await assertA11yAndNoOverflow(page, "unverified contract verification entry in English narrow mode");
  await page.getByRole("button", { name: "切换到中文" }).click();
  const localizedVerificationEntry = page.getByRole("link", { name: "提交合约验证请求" });
  await expect(localizedVerificationEntry).toHaveAttribute(
    "href",
    `/verify?address=${unverifiedAddress}`,
  );
  await assertA11yAndNoOverflow(page, "unverified contract verification entry in Chinese narrow mode");
  await page.getByRole("button", { name: "Switch to English" }).click();
  await page.setViewportSize({ width: 1280, height: 720 });
  await activateInView(verificationEntry);
  await expect(page).toHaveURL(new RegExp(`/verify\\?address=${unverifiedAddress}$`));
  await expect(page.getByRole("heading", { name: "Public verification is unavailable" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Open a durable verification job" })).toBeVisible();
  await page.getByLabel("Job ID", { exact: true }).fill(verificationJobID);
  await page.getByLabel("Job read API key", { exact: true }).fill(readAPIKey);
  await activateInView(page.getByRole("button", { name: "Load job", exact: true }));
  await expect(page.getByText("succeeded", { exact: true })).toBeVisible();
  await expect(page.getByText("verification_success", { exact: true })).toBeVisible();

  const sourcePageErrors: string[] = [];
  const sourceConsoleErrors: string[] = [];
  const onSourcePageError = (error: Error) => sourcePageErrors.push(error.message);
  const onSourceConsole = (message: import("@playwright/test").ConsoleMessage) => {
    if (message.type() === "error") sourceConsoleErrors.push(message.text());
  };
  page.on("pageerror", onSourcePageError);
  page.on("console", onSourceConsole);
  await page.goto(`/address/${address}#code`);
  await expect(page.getByRole("heading", { name: "Verified artifact" })).toBeVisible();
	await expect(page.getByText("Factory-derived", { exact: true })).toBeVisible();
	await expect(page.getByRole("status")).toContainText("Auto-verified from verified factory:");
	await expect(page.getByRole("heading", { name: "Created contracts" })).toBeVisible();
	await expect(page.getByRole("link", { name: uupsImplementation })).toBeVisible();
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
  await page.getByRole("treeitem", { name: "ProxyBase.sol", exact: true }).click();
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

  await activateInView(page.getByRole("button", { name: "Switch color theme" }));
  await activateInView(page.getByRole("button", { name: "切换到中文" }));
  await expect(page.locator("html")).toHaveAttribute("lang", "zh-CN");
  await expect(page.locator(".source-editor")).toHaveCount(1);
  await activateInView(page.getByRole("button", { name: "切换颜色主题" }));
  await activateInView(page.getByRole("button", { name: "Switch to English" }));
  expect(sourcePageErrors).toEqual([]);
  expect(sourceConsoleErrors).toEqual([]);
  page.off("pageerror", onSourcePageError);
  page.off("console", onSourceConsole);

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
    `/address/${address}#code`,
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

  await page.goto(`/address/${address}#code`);
  await expect(
    page.getByRole("heading", { name: "TransparentUpgradeableProxy", level: 2 }),
  ).toBeVisible();
  await page.getByRole("heading", { name: "Proxy identity" }).click();
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
  await expect(directRead.getByText(address, { exact: true })).toHaveCount(0);

  await activateInView(transparentTabs.getByRole("tab", { name: "Write contract" }));
  await expect(page.getByText("setProxyValue(uint256)", { exact: true })).toBeVisible();
  await expect(page.getByLabel("newValue")).toBeVisible();
  await expect(page.getByRole("button", { name: "Copy calldata" })).toBeVisible();

  await activateInView(transparentTabs.getByRole("tab", {
    name: "Read implementation (as proxy)",
  }));
  const transparentRead = page.locator(".abi-function-card").filter({ hasText: "value()" });
  await expect(transparentRead.getByText(address, { exact: true })).toHaveCount(0);
  await expect(transparentRead.getByText(transparentImplementation, { exact: true })).toHaveCount(0);

  await activateInView(transparentTabs.getByRole("tab", { name: "Proxy management" }));
  const proxyAdminUpgrade = page.locator(".abi-function-card").filter({
    hasText: "upgradeAndCall(address,address,bytes)",
  });
  await expect(proxyAdminUpgrade.getByText(proxyAdminAddress, { exact: true })).toHaveCount(0);
  await expect(proxyAdminUpgrade.getByText(/High-risk upgrade operation/)).toBeVisible();

  await activateInView(transparentTabs.getByRole("tab", { name: "Upgrade history" }));
  await expect(page.getByRole("heading", { name: "Canonical implementation upgrades" })).toBeVisible();
  await expect(page.getByText(oldImplementation, { exact: true })).toBeVisible();
  await expect(page.getByText(transparentImplementation, { exact: true }).last()).toBeVisible();

  await activateInView(transparentTabs.getByRole("tab", { name: "Initialization history" }));
  await expect(page.getByText("Initialized version 2", { exact: true })).toBeVisible();

  await page.goto(`/address/${uupsProxyAddress}#code`);
  await expect(page.getByRole("heading", { name: "ERC1967Proxy", level: 2 })).toBeVisible();
  const uupsTabs = page.getByRole("tablist", { name: "Contract interaction sections" });
  await expect(uupsTabs.getByRole("tab", { name: "Proxy management" })).toHaveCount(0);
  await activateInView(uupsTabs.getByRole("tab", {
    name: "Read implementation (as proxy)",
  }));
  const uupsValue = page.locator(".abi-function-card").filter({ hasText: "value()" }).first();
  await expect(uupsValue.getByText(uupsProxyAddress, { exact: true })).toHaveCount(0);
  const proxiable = page.locator(".abi-function-card").filter({ hasText: "proxiableUUID()" });
  await activateInView(proxiable.locator("summary"));
  await expect(proxiable.getByText(uupsImplementation, { exact: true })).toHaveCount(0);
  await expect(proxiable.getByText(/called directly on the implementation/)).toBeVisible();
  await activateInView(uupsTabs.getByRole("tab", {
    name: "Write implementation (as proxy)",
  }));
  const uupsUpgrade = page.locator(".abi-function-card").filter({
    hasText: "upgradeToAndCall(address,bytes)",
  });
  await activateInView(uupsUpgrade.locator("summary"));
  await expect(uupsUpgrade.getByText(uupsProxyAddress, { exact: true })).toHaveCount(0);

  await page.goto(`/address/${beaconProxyAddress}#code`);
  await expect(page.getByRole("heading", { name: "BeaconProxy", level: 2 })).toBeVisible();
  const beaconTabs = page.getByRole("tablist", { name: "Contract interaction sections" });
  await activateInView(beaconTabs.getByRole("tab", { name: "Proxy management" }));
  const beaconUpgrade = page.locator(".abi-function-card").filter({ hasText: "upgradeTo(address)" });
  await expect(beaconUpgrade.getByText(upgradeableBeacon, { exact: true })).toHaveCount(0);
  await activateInView(beaconTabs.getByRole("tab", { name: "Upgrade history" }));
  await expect(page.getByText("Beacon implementation changed", { exact: true })).toBeVisible();
  await expect(page.getByText(beaconImplementation, { exact: true }).last()).toBeVisible();

  await page.goto(`/address/${cloneAddress}#code`);
  await expect(page.getByRole("heading", { name: "MinimalClone", level: 2 })).toBeVisible();
  await page.getByRole("heading", { name: "Proxy identity" }).click();
  await expect(page.getByText(/This EIP-1167 Clone is immutable/)).toBeVisible();
  const cloneTabs = page.getByRole("tablist", { name: "Contract interaction sections" });
  await expect(cloneTabs.getByRole("tab", { name: "Upgrade history" })).toHaveCount(0);
  await activateInView(cloneTabs.getByRole("tab", {
    name: "Read implementation (as proxy)",
  }));
  const cloneRead = page.locator(".abi-function-card").filter({ hasText: "value()" });
  await expect(cloneRead.getByText(cloneAddress, { exact: true })).toHaveCount(0);
  await expect(cloneRead.getByText(cloneImplementation, { exact: true })).toHaveCount(0);
  await expect.poll(() => contractRequests.some(({ pathname }) =>
    pathname === `/api/v1/contracts/${cloneAddress}/proxy/upgrades`)).toBe(false);
  const cloneCodeTab = cloneTabs.getByRole("tab", { name: "Code" });
  await cloneCodeTab.click();
  await expect(cloneCodeTab).toHaveAttribute("aria-selected", "true");
  await page.getByRole("heading", { name: "Proxy identity" }).click();

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

test("Solady legacy CWIA displays verified packed arguments and gates writes on its schema", async ({
	page,
}) => {
	test.setTimeout(120_000);
	let transientProxyReads = 0;
	let allowReadOnlyClassification = false;
	await page.route(new RegExp(
		`/api/v1/contracts/${cwiaReadOnlyAddress}/proxy$`,
		"iu",
	), async (route) => {
		if (!allowReadOnlyClassification) {
			await fulfillAPIEnvelope(route, {
				address: cwiaReadOnlyAddress,
				status: "unavailable",
				snapshot: {
					chain_id: "1",
					block_number: "43",
					block_hash: codeHash,
				},
				evidence: [],
			});
			return;
		}
		await route.continue();
	});
	await page.route(new RegExp(
		`/api/v1/contracts/${cwiaUnverifiedAddress}/proxy$`,
		"iu",
	), async (route) => {
		transientProxyReads += 1;
		if (transientProxyReads === 2 || transientProxyReads === 3) {
			await fulfillAPIEnvelope(route, {
				address: cwiaUnverifiedAddress,
				status: "unavailable",
				snapshot: {
					chain_id: "1",
					block_number: "43",
					block_hash: codeHash,
				},
				evidence: [],
			});
			return;
		}
		await route.continue();
	});
	await page.addInitScript(() => {
		const requests: WalletRequest[] = [];
		const listeners = new Map<string, Set<(value: unknown) => void>>();
		const account = "0x2222222222222222222222222222222222222222";
		const provider = {
			async request({ method, params }: WalletRequest) {
				requests.push({ method, params });
				if (method === "eth_requestAccounts" || method === "eth_accounts") return [account];
				if (method === "eth_chainId") return "0x1";
				if (method === "eth_call") return `0x${"0".repeat(63)}2`;
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
		const detail = {
			info: {
				uuid: "00000000-0000-4000-8000-000000000069",
				name: "CWIA E2E Wallet",
				icon: "data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg'/>",
				rdns: "org.etherview.cwia-e2e",
			},
			provider,
		};
		window.addEventListener("eip6963:requestProvider", () => {
			window.dispatchEvent(new CustomEvent("eip6963:announceProvider", { detail }));
		});
		(window as WalletWindow).__etherviewE2EWallet = {
			requests,
			resolveWrite() {},
			setMode() {},
			emit(event, value) {
				for (const listener of listeners.get(event) ?? []) listener(value);
			},
		};
	});
	const requestedPaths: string[] = [];
	page.on("request", (request) => {
		const url = new URL(request.url());
		if (url.pathname.startsWith("/api/v1/contracts/")) requestedPaths.push(url.pathname);
	});

	await page.goto(`/address/${cwiaAddress}#code`);
	await expect(page.getByRole("heading", { name: "Proxy identity", level: 2 })).toBeVisible();
	await expect(page.getByRole("heading", { name: "Verified artifact" })).toHaveCount(0);
	await expect(page.getByRole("link", { name: "Submit a verification request" })).toHaveCount(0);
	await page.getByRole("heading", { name: "Proxy identity" }).click();
	await expect(page.getByText("Solady legacy CWIA bytecode", { exact: true })).toBeVisible();
	await expect(page.getByText("Verified Solidity AST and decoded", { exact: true })).toBeVisible();
	await expect(page.getByText("Exact implementation address", { exact: true })).toBeVisible();
	const argumentsRegion = page.getByRole("region", { name: "Decoded immutable arguments" });
	const argumentsTable = argumentsRegion.getByRole("table", { name: "Decoded immutable arguments" });
	for (const heading of ["Name", "Type", "Offset", "Data"]) {
		await expect(argumentsTable.getByRole("columnheader", { name: heading })).toBeVisible();
	}
	await expect(argumentsRegion.getByText("owner", { exact: true })).toBeVisible();
	await expect(argumentsRegion.getByText("address", { exact: true })).toBeVisible();
	await expect(argumentsRegion.getByText("0", { exact: true })).toBeVisible();
	await expect(argumentsRegion.getByText("0x2222222222222222222222222222222222222222", { exact: true })).toBeVisible();
	await expect(argumentsRegion.getByText("number", { exact: true })).toBeVisible();
	await expect(argumentsRegion.getByText("uint256", { exact: true })).toBeVisible();
	await expect(argumentsRegion.getByText("20", { exact: true })).toBeVisible();
	await expect(argumentsRegion.getByText("42", { exact: true })).toBeVisible();
	await expect(argumentsRegion.getByText("data_length", { exact: true })).toBeVisible();
	await expect(argumentsRegion.getByText("data", { exact: true })).toBeVisible();
	await expect(argumentsRegion.getByRole("button", { name: "Copy" })).toHaveCount(4);
	await expect(argumentsRegion.getByText("0x68656c6c6f2c776f726c64", { exact: true })).toBeVisible();
	const writableTabs = page.getByRole("tablist", { name: "Contract interaction sections" });
	await expect(writableTabs.getByRole("tab", { name: "Upgrade history" })).toHaveCount(0);
	await activateInView(writableTabs.getByRole("tab", { name: "Write implementation (as proxy)" }));
	await expect(page.getByText("setValue(uint256)", { exact: true })).toBeVisible();

	await page.goto(`/address/${cwiaReadOnlyAddress}#code`);
	await expect(page.getByRole("tab", { name: "Code" })).toBeVisible();
	await expect(page.getByRole("heading", { name: "Verified artifact" })).toHaveCount(0);
	await expect(page.getByRole("link", { name: "Submit a verification request" })).toHaveCount(0);
	allowReadOnlyClassification = true;
	await expect(page.getByRole("heading", { name: "Proxy identity" })).toBeVisible();
	await page.getByRole("heading", { name: "Proxy identity" }).click();
	await expect(page.getByText("Verified Solidity AST unavailable", { exact: true })).toBeVisible();
	await expect(page.getByText(/writes are disabled.*no current compiler-derived CWIA AST analysis/u)).toBeVisible();
	const readOnlyTabs = page.getByRole("tablist", { name: "Contract interaction sections" });
	await activateInView(readOnlyTabs.getByRole("tab", { name: "Read implementation (as proxy)" }));
	await expect(page.getByText("value()", { exact: true })).toBeVisible();
	await activateInView(readOnlyTabs.getByRole("tab", { name: "Write implementation (as proxy)" }));
	await expect(page.getByText("This ABI has no callable state-changing functions for this target.", { exact: true })).toBeVisible();
	await expect.poll(() => requestedPaths.some((pathname) => pathname.endsWith("/proxy/upgrades"))).toBe(false);

	await page.goto(`/address/${cwiaUnverifiedAddress}#code`);
	await expect(page.getByRole("heading", { name: "Verified artifact" })).toHaveCount(0);
	await expect(page.getByRole("link", { name: "Submit a verification request" })).toHaveCount(0);
	await page.getByRole("heading", { name: "Proxy identity" }).click();
	await expect(page.getByText("Verified Solidity AST and decoded", { exact: true })).toBeVisible();
	await expect(page.getByText("Matching implementation code hash", { exact: true })).toBeVisible();
	await expect(page.getByText("Verified by code hash", { exact: true })).toBeVisible();
	await expect(page.getByText(/writes are disabled.*proxy binding is not verified/u)).toBeVisible();
	await expect(page.getByText("Verified Solidity AST unavailable", { exact: true })).toHaveCount(0);
	const unverifiedTabs = page.getByRole("tablist", { name: "Contract interaction sections" });
	await expect(unverifiedTabs.getByRole("tab", { name: "Read contract" })).toHaveCount(0);
	await activateInView(unverifiedTabs.getByRole("tab", { name: "Read implementation (as proxy)" }));
	await expect(page.getByText("value()", { exact: true })).toBeVisible();
	await activateInView(page.getByText("Connect wallet", { exact: true }).first());
	await activateInView(page.getByRole("button", { name: /CWIA E2E Wallet/u }));
	const readValue = page.getByRole("button", { name: "Read contract" });
	await activateInView(readValue);
	await expect(page.getByRole("alert")).toContainText(
		"The latest proxy stage is temporarily unavailable",
	);
	await expect(unverifiedTabs.getByRole("tab", { name: "Read implementation (as proxy)" })).toBeVisible();
	await expect(unverifiedTabs.getByRole("tab", { name: "Write implementation (as proxy)" })).toBeVisible();
	await expect(page).toHaveURL(new RegExp(`${cwiaUnverifiedAddress}#read-implementation$`, "u"));
	expect(transientProxyReads).toBe(2);
	let walletRequests = await page.evaluate(
		() => (window as WalletWindow).__etherviewE2EWallet.requests,
	);
	expect(walletRequests.some(({ method }) => method === "eth_call")).toBe(false);

	await activateInView(readValue);
	await expect(page.getByRole("alert")).toContainText(
		"The latest proxy stage is temporarily unavailable",
	);
	expect(transientProxyReads).toBe(3);
	await activateInView(readValue);
	await expect(page.locator(".abi-output").getByText("2", { exact: true })).toBeVisible();
	expect(transientProxyReads).toBe(4);
	walletRequests = await page.evaluate(
		() => (window as WalletWindow).__etherviewE2EWallet.requests,
	);
	expect(walletRequests.filter(({ method }) => method === "eth_call")).toHaveLength(1);
	await activateInView(unverifiedTabs.getByRole("tab", { name: "Write implementation (as proxy)" }));
	await expect(page.getByText("This ABI has no callable state-changing functions for this target.", { exact: true })).toBeVisible();

	await page.goto(`/address/${cwiaCodeHashImplementation}#code`);
	await expect(page.getByRole("heading", { name: "MyAccount", level: 2 })).toBeVisible();
	await expect(page.getByText("Source verified by code hash")).toHaveCount(2);
	await expect(page.getByText("Source code verified", { exact: true })).toHaveCount(0);
	await expect(page.getByRole("status")).toContainText(
		"Source verified by identical runtime code hash:",
	);
	await expect(page.getByRole("link", { name: cwiaImplementation })).toBeVisible();
	await expect(page.getByRole("link", { name: "Submit a verification request" })).toHaveCount(0);
	await activateInView(page.getByRole("button", { name: "切换到中文" }));
	await expect(page.getByText("源码已通过代码哈希验证")).toHaveCount(2);
	await expect(page.getByRole("status")).toContainText("源码已通过相同运行时代码哈希验证：");
	await activateInView(page.getByRole("button", { name: "Switch to English" }));

	await page.goto(`/address/${cwiaAddress}#code`);
	await page.setViewportSize({ width: 390, height: 844 });
	await page.getByRole("heading", { name: "Proxy identity" }).click();
	const argumentsScroll = page.locator(".cwia-arguments-scroll");
	await expect(argumentsScroll).toBeVisible();
	await expect.poll(() => argumentsScroll.evaluate((element) =>
		element.scrollWidth > element.clientWidth)).toBe(true);
	await argumentsScroll.focus();
	await expect(argumentsScroll).toBeFocused();
	await activateInView(page.getByRole("button", { name: "Switch color theme" }));
	await activateInView(page.getByRole("button", { name: "切换到中文" }));
	const localizedArguments = page.getByRole("region", { name: "已解码 immutable 参数" });
	for (const heading of ["名称", "类型", "偏移", "数据"]) {
		await expect(localizedArguments.getByRole("columnheader", { name: heading })).toBeVisible();
	}
	await assertA11yAndNoOverflow(page, "decoded CWIA table in Chinese dark narrow mode");
});

test("ERC-2535 pages preserve selector-scoped facets and ordered DiamondCut history", async ({
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

  await page.goto(`/address/${diamondAddress}#code`);
  await expect(page.getByRole("heading", { name: "DiamondRouter", level: 2 })).toBeVisible();
  await page.getByRole("heading", { name: "Proxy identity" }).click();
  await expect(page.getByText("ERC-2535 Diamond", { exact: true })).toBeVisible();
  await expect(page.getByText("Full cross-check", { exact: true })).toBeVisible();
  await expect(page.getByText("Not detected", { exact: true })).toBeVisible();
  await expect(page.locator(".proxy-facts")).toContainText(diamondWriteFacet);
  await expect(page.locator(".proxy-facts")).toContainText(diamondLoupeFacet);

  const tabs = page.getByRole("tablist", { name: "Contract interaction sections" });
  await expect(tabs.getByRole("tab", { name: "Read implementation (as proxy)" })).toHaveCount(0);
  await expect(tabs.getByRole("tab", { name: "Upgrade history" })).toHaveCount(0);
  await activateInView(tabs.getByRole("tab", { name: "Diamond facets" }));
  await expect(page.getByText(/Every call is sent to the Diamond/)).toBeVisible();
  const writeFacetCard = page.locator(".diamond-facet-card").filter({ hasText: diamondWriteFacet });
  await activateInView(writeFacetCard.locator("summary"));
  await expect(writeFacetCard.getByText("setValue(uint256)", { exact: true })).toBeVisible();
  await expect(writeFacetCard.getByText("value()", { exact: true })).toHaveCount(0);

  await activateInView(tabs.getByRole("tab", { name: "DiamondCut history" }));
  await expect(page.getByText("Add selectors", { exact: true }).first()).toBeVisible();
  await expect(page.getByText(diamondInitAddress, { exact: true })).toBeVisible();
  await expect(page.getByText("0x55241077", { exact: true })).toBeVisible();
  await expect.poll(() => contractRequests.some(({ pathname }) =>
    pathname === `/api/v1/contracts/${diamondAddress}/proxy/diamond-cuts`)).toBe(true);

  await page.setViewportSize({ width: 390, height: 844 });
  await activateInView(page.getByRole("button", { name: "Switch color theme" }));
  await assertA11yAndNoOverflow(page, "Diamond contract page in English dark mode");
  await activateInView(page.getByRole("button", { name: "切换到中文" }));
  await expect(page.locator("html")).toHaveAttribute("lang", "zh-CN");
  await expect(page.getByRole("tab", { name: "DiamondCut 历史" })).toBeVisible();
  await expect(page.getByText("添加 Selector", { exact: true }).first()).toBeVisible();
  await assertA11yAndNoOverflow(page, "Diamond contract page in Chinese dark mode");

  for (const request of contractRequests) {
    expect(request.method).toBe("GET");
    expect(request.headers["x-api-key"]).toBeUndefined();
    expect(request.headers["payment-signature"]).toBeUndefined();
    expect(request.headers["x-csrf-token"]).toBeUndefined();
  }
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
  expect(policy).not.toContain("http:");
  expect(policy).not.toContain("https:");
  const nonceMatch = policy.match(/'nonce-([A-Za-z0-9_-]{43})'/u);
  expect(nonceMatch).not.toBeNull();
  const shellNonce = nonceMatch?.[1] ?? "";
  const basePolicy = policy.replace(/ 'nonce-[^']+'/u, "");
  expect(document.headers()["etag"]).toBeUndefined();

  const html = await document.text();
  const metaNonce = html.match(/<meta name="etherview-csp-nonce" content="([A-Za-z0-9_-]{43})">/u)?.[1];
  expect(metaNonce).toBe(shellNonce);
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
  expect(asset.headers()["content-security-policy"]).toBe(basePolicy);
  expect(asset.headers()["x-content-type-options"]).toBe("nosniff");

  const notModified = await request.get(entrypoints[0], {
    headers: { "If-None-Match": asset.headers()["etag"] },
  });
  expect(notModified.status()).toBe(304);
  expect(notModified.headers()["cache-control"]).toBe(
    "public, max-age=31536000, immutable",
  );
  expect(notModified.headers()["content-security-policy"]).toBe(basePolicy);
  expect(notModified.headers()["x-content-type-options"]).toBe("nosniff");

  const missingAPI = await request.get("/api/v1/not-a-route", {
    headers: { Accept: "text/html" },
  });
  expect(missingAPI.status()).toBe(404);
  expect(missingAPI.headers()["content-security-policy"]).toBe(basePolicy);
  expect(await missingAPI.text()).not.toContain('<div id="root"></div>');

  for (const missingAsset of ["/robots.txt", "/assets/missing.js", "/module.wasm"]) {
    const response = await request.get(missingAsset, { headers: { Accept: "text/html" } });
    expect(response.status()).toBe(404);
    expect(response.headers()["cache-control"]).toBe("no-store");
    expect(response.headers()["content-security-policy"]).toBe(basePolicy);
    expect(await response.text()).not.toContain('<div id="root"></div>');
  }

  const refusedHTML = await request.get("/blocks/1", {
    headers: { Accept: "text/html;q=0, */*;q=1" },
  });
  expect(refusedHTML.status()).toBe(404);
  expect(refusedHTML.headers()["cache-control"]).toBe("no-store");
  expect(refusedHTML.headers()["content-security-policy"]).toBe(basePolicy);
  expect(await refusedHTML.text()).not.toContain('<div id="root"></div>');

  const headDeepLink = await request.head("/blocks/not-an-asset", {
    headers: { Accept: "text/html" },
  });
  expect(headDeepLink.status()).toBe(404);
  expect(headDeepLink.headers()["content-security-policy"]).toBe(basePolicy);

  const postDeepLink = await request.post("/blocks/1", {
    headers: { Accept: "text/html" },
  });
  expect(postDeepLink.status()).toBe(405);
  expect(postDeepLink.headers()["content-security-policy"]).toBe(basePolicy);
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
  let personalKey = {
    prefix: "abcdefghij",
    name: "Browser indexer",
    scopes: ["api:read"] as Array<"api:read" | "contract:verify">,
    status: "active" as "active" | "revoked",
  };

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
          user_api_keys: true,
          api_billing: true,
          x402_topups: false,
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
  await page.route("**/api/v1/users/me/api-keys**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    record(request);
    const key = () => ({
      ...personalKey,
      rate_per_second: 20,
      burst: 40,
      created_at: "2026-08-10T00:00:00Z",
      ...(personalKey.status === "revoked"
        ? { revoked_at: "2026-08-10T01:00:00Z" }
        : {}),
    });
    if (request.method() === "GET" && url.pathname === "/api/v1/users/me/api-keys") {
      await route.fulfill({
        contentType: "application/json",
        json: envelope({
          items: [key()],
          policy: {
            rate_per_second: 20,
            burst: 40,
            maximum_active: 5,
            active_count: personalKey.status === "active" ? 1 : 0,
            allowed_scopes: ["api:read", "contract:verify"],
          },
        }),
      });
      return;
    }
    if (request.method() === "POST" && url.pathname === "/api/v1/users/me/api-keys") {
      const body = request.postDataJSON() as {
        name: string;
        scopes: Array<"api:read" | "contract:verify">;
      };
      personalKey = {
        prefix: "abcdefghij",
        name: body.name,
        scopes: body.scopes,
        status: "active",
      };
      await route.fulfill({
        contentType: "application/json",
        json: envelope({ token: userAPIKeyToken, key: key() }),
        status: 201,
      });
      return;
    }
    if (request.method() === "POST" && url.pathname.endsWith("/rotate")) {
      personalKey = { ...personalKey, prefix: "bcdefghij2" };
      await route.fulfill({
        contentType: "application/json",
        json: envelope({ token: rotatedUserAPIKeyToken, key: key() }),
        status: 201,
      });
      return;
    }
    if (request.method() === "DELETE") {
      personalKey = { ...personalKey, status: "revoked" };
      await route.fulfill({ body: "", status: 204 });
      return;
    }
    await route.fulfill({ status: 404 });
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

  page.on("dialog", async (dialog) => dialog.accept());
  const accountSections = page.getByRole("navigation", { name: "Account sections" });
  await activateInView(
    accountSections.getByRole("link", { name: "API Keys", exact: true }),
  );
  await expect(page.getByRole("heading", { name: "API Keys" })).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await assertA11yAndNoOverflow(page, "account API keys in English narrow mode");
  await activateInView(page.getByRole("button", { name: "切换到中文" }));
  await expect(page.getByText(/最小权限凭据/)).toBeVisible();
  await assertA11yAndNoOverflow(page, "account API keys in Chinese narrow mode");
  await activateInView(page.getByRole("button", { name: "Switch to English" }));
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.getByRole("textbox", { name: "Key name" }).fill("Production verifier");
  await page.getByRole("checkbox", { name: /Contract verification/ }).check();
  await activateInView(page.getByRole("button", { name: "Create key" }));
  await expect(page.getByRole("dialog", { name: "Save your API key now" })).toContainText(
    userAPIKeyToken,
  );
  expect(page.url()).not.toContain(userAPIKeyToken);
  expect(
    await page.evaluate(
      (token) => Object.values(localStorage).every((value) => value !== token) &&
        Object.values(sessionStorage).every((value) => value !== token),
      userAPIKeyToken,
    ),
  ).toBe(true);
  await activateInView(page.getByRole("button", { name: "I saved the token" }));
  await activateInView(page.getByRole("button", { name: "Rotate" }));
  await expect(page.getByRole("dialog", { name: "Save your API key now" })).toContainText(
    rotatedUserAPIKeyToken,
  );
  await activateInView(page.getByRole("button", { name: "I saved the token" }));
  await activateInView(page.getByRole("button", { name: "Revoke" }));
  await expect(
    page.locator(".api-key-card .user-state").getByText("Revoked", { exact: true }),
  ).toBeVisible();
  const personalKeyWrites = authRequests.filter(
    ({ method, pathname }) =>
      pathname.startsWith("/api/v1/users/me/api-keys") && method !== "GET",
  );
  expect(personalKeyWrites).toHaveLength(3);
  for (const request of personalKeyWrites) {
    expect(request.headers["x-csrf-token"]).toBe(authCSRFToken);
    expect(request.headers.origin).toBe("http://127.0.0.1:4173");
  }

  await activateInView(
    accountSections.getByRole("link", { name: "Billing", exact: true }),
  );

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

  await activateInView(
    accountSections.getByRole("link", { name: "Overview", exact: true }),
  );
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

  await page.goto(`/address/${address}#code`);
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
  const transactionLink = page.getByRole("link", { name: walletTransactionHash });
  await expect(transactionLink).toBeVisible();
  await expect(transactionLink).toHaveAttribute(
    "href",
    `/tx/${walletTransactionHash}?tab=overview`,
  );
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

  await page.goto(`/address/${address}#code`);
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

async function fulfillAPIEnvelope(
  route: import("@playwright/test").Route,
  data: unknown,
) {
  await route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({
      data,
      meta: { request_id: "mempool-browser-e2e", chain_id: "1" },
    }),
  });
}
