import { expect, test, type Locator } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

const address = "0x1111111111111111111111111111111111111111";
const transactionCursor = "transactions/snapshot?generation=7 + page=2&exact=true/#";

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

test("EIP-6963 wallet discovery keeps reads and writes disabled on chain mismatch", async ({ page }) => {
  await page.addInitScript(() => {
    const provider = {
      async request({ method }: { method: string }) {
        if (method === "eth_requestAccounts") return ["0x2222222222222222222222222222222222222222"];
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
  });

  await page.goto(`/contract/${address}`);
  await activateInView(page.getByText("Connect wallet", { exact: true }).first());
  await activateInView(page.getByRole("button", { name: /E2E Wallet/ }));

  await expect(page.getByRole("status").filter({ hasText: "Switch the wallet to chain 1 (currently 2)." })).toBeVisible();
  await expect(page.getByRole("button", { name: "Read contract" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Send transaction" })).toBeDisabled();
});

async function activateInView(locator: Locator) {
  await locator.evaluate((element) => {
    element.scrollIntoView({ behavior: "instant", block: "center" });
    (element as HTMLElement).focus({ preventScroll: true });
  });
  await expect(locator).toBeFocused();
  await locator.press("Enter");
}
