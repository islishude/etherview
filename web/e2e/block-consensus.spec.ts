import { expect, test, type Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

const blockHash = `0x${"1".repeat(64)}`;
const root = `0x${"ab".repeat(32)}`;
const observation = {
  block_hash: blockHash,
  state: "complete",
  slot: "9007199254740993",
  epoch: "281474976710656",
  proposer_index: "9007199254740994",
  fork: "fulu",
  root,
  parent_root: root,
  state_root: root,
  body_root: root,
  graffiti: `0x68656c6c6f${"00".repeat(27)}`,
  operations: { attestations: "9007199254740995", deposits: "0" },
};

async function configureConsensus(page: Page, enabled: boolean) {
  await page.route("**/api/v1/config", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.data.features.consensus = enabled;
    body.data.features.blobs = false;
    await route.fulfill({ response, json: body });
  });
}

test("disabled consensus hides the tab and its deep link falls back without a consensus request", async ({
  page,
}) => {
  await configureConsensus(page, false);
  const requests: string[] = [];
  page.on("request", (request) => {
    if (new URL(request.url()).pathname.endsWith("/consensus")) requests.push(request.url());
  });
  for (const url of ["/blocks/1", "/blocks/1?tab=consensus"]) {
    await page.goto(url);
    await expect(page.getByRole("tab", { name: "Overview", exact: true })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    await expect(page.getByRole("tab", { name: "Consensus", exact: true })).toHaveCount(0);
    await expect(page.locator(".block-consensus-panel")).toHaveCount(0);
    await expect(page.getByRole("tab", { name: "Transactions", exact: true })).toBeVisible();
  }
  expect(requests).toEqual([]);
});

test("enabled consensus retains the tab and explains missing, unavailable and disabled observations", async ({
  page,
}) => {
  await configureConsensus(page, true);
  let state = "pending";
  await page.route("**/api/v1/blocks/*/consensus", (route) => {
    expect(new URL(route.request().url()).pathname).toBe(`/api/v1/blocks/${blockHash}/consensus`);
    return route.fulfill({
      json: {
        data: {
          block_hash: blockHash,
          state,
          ...(state === "unavailable" ? { code: "beacon_not_found" } : {}),
        },
        meta: { chain_id: "1", request_id: "consensus-state" },
      },
    });
  });
  for (const [nextState, text] of [
    ["pending", "Awaiting collection"],
    ["unavailable", "Unavailable from the source"],
    ["failed", "Collection failed"],
    ["disabled", "Not enabled"],
  ]) {
    state = nextState!;
    await page.goto("/blocks/1?tab=consensus");
    await expect(page.getByRole("tab", { name: "Consensus", exact: true })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    const panel = page.getByRole("tabpanel");
    await expect(panel.getByRole("status")).toContainText(text!);
    await expect(panel.locator("dl")).toHaveCount(0);
    if (state === "unavailable") {
      await expect(panel).toContainText("Historical data is unavailable at this node.");
    }
  }
});

for (const width of [390, 768, 1440]) {
  for (const language of ["en", "zh"] as const) {
    for (const theme of ["light", "dark"] as const) {
      test(`block consensus ${width}px ${language} ${theme}`, async ({ page }, testInfo) => {
        await page.setViewportSize({ width, height: 1000 });
        await page.addInitScript(
          ({ language, theme }) => {
            localStorage.setItem("etherview.language", language);
            localStorage.setItem("etherview.theme", theme);
          },
          { language, theme },
        );
        await configureConsensus(page, true);
        let reads = 0;
        await page.route("**/api/v1/blocks/*/consensus", (route) => {
          reads++;
          expect(new URL(route.request().url()).pathname).toBe(
            `/api/v1/blocks/${blockHash}/consensus`,
          );
          return route.fulfill({
            json: { data: observation, meta: { chain_id: "1", request_id: "consensus-layout" } },
          });
        });
        const errors: string[] = [];
        page.on("pageerror", (error) => errors.push(error.message));
        page.on("console", (message) => {
          if (/content security policy|violates.*directive/i.test(message.text()))
            errors.push(message.text());
        });
        await page.goto("/blocks/1");
        const tab = page.getByRole("tab", {
          name: language === "en" ? "Consensus" : "共识层",
          exact: true,
        });
        await expect(tab).toBeVisible();
        expect(reads).toBe(0);
        await tab.click();
        const panel = page.getByRole("tabpanel");
        await expect(panel.getByText("9,007,199,254,740,994", { exact: true })).toBeVisible();
        await expect(panel.getByRole("heading")).toHaveCount(0);
        await expect(panel.getByRole("status")).toHaveCount(0);
        await expect(panel.locator(".block-consensus-summary dt")).toHaveCount(4);
        await expect(panel).not.toContainText(language === "en" ? "Available" : "可用");
        const details = panel.locator("details");
        await expect(details).not.toHaveAttribute("open");
        const disclosure = details.locator("summary");
        for (const expanded of [false, true]) {
          if (expanded) {
            await disclosure.focus();
            await page.keyboard.press("Enter");
            await expect(details).toHaveAttribute("open", "");
            await expect(panel.getByText("hello", { exact: true })).toBeVisible();
            await expect(panel.getByText(root, { exact: true })).toHaveCount(4);
            await expect(panel.getByText("9,007,199,254,740,995", { exact: true })).toBeVisible();
          }
          await expect(page.locator("body")).toHaveJSProperty("scrollWidth", width);
          const scan = await new AxeBuilder({ page })
            .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
            .analyze();
          expect(scan.violations).toEqual([]);
          const screenshot = testInfo.outputPath(
            `consensus-${expanded ? "expanded" : "summary"}.png`,
          );
          await page.screenshot({ path: screenshot, fullPage: true });
          await testInfo.attach(expanded ? "technical details" : "summary", {
            path: screenshot,
            contentType: "image/png",
          });
        }
        expect(errors).toEqual([]);
      });
    }
  }
}
