import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

const address = `0x${"11".repeat(20)}`;
const hash = `0x${"aa".repeat(32)}`;
const topic = `0x${"bb".repeat(32)}`;
test("contract event logs filter names and positioned topics in both languages on mobile", async ({
  page,
}) => {
  const requests: URL[] = [];
  const meta = { request_id: "address-events", chain_id: "1" };
  await page.route(new RegExp(`/api/v1/addresses/${address}(?:[/?]|$)`), async (route) => {
    const url = new URL(route.request().url());
    requests.push(url);
    let data: unknown;
    if (url.pathname.endsWith("/log-events"))
      data = [{ name: "Changed", signature: "Changed(uint256)" }];
    else if (url.pathname.endsWith("/logs"))
      data = [
        {
          block_number: "5",
          block_hash: hash,
          transaction_hash: hash,
          transaction_index: "0",
          block_timestamp: "2026-01-01T00:00:00Z",
          log: {
            address,
            log_index: "0",
            topics: [topic],
            data: "0x",
            decoding: {
              status: "unknown",
              arguments: [],
              candidates: [],
              attribution: { mode: "address_fallback", trace_path: [] },
            },
          },
        },
      ];
    else if (url.pathname.endsWith(address))
      data = {
        address,
        type: "contract",
        code_hash: hash,
        balance: "0",
        nonce: "0",
        at_block: hash,
        completeness: {
          core: "complete",
          trace: "unavailable",
          metadata: "pending",
          state: "complete",
          user_operations: "unavailable",
        },
      };
    else {
      await route.continue();
      return;
    }
    await route.fulfill({ json: { data, meta } });
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`/address/${address}?tab=logs`);
  await expect(page.getByText("Block 5", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: /Event signature:/ }).click();
  await page.getByRole("combobox", { name: "Search event names" }).fill("Ch");
  await page.getByRole("option", { name: /Changed\(uint256\)/ }).click();
  await page.getByLabel("From block", { exact: true }).fill("1");
  await page.getByLabel("To block", { exact: true }).fill("5");
  await page.getByRole("button", { name: "Query" }).click();
  await expect(page).toHaveURL(/event_signature=Changed/);
  await page.getByRole("button", { name: "Topic", exact: true }).click();
  await page.getByRole("combobox", { name: "Topic position", exact: true }).selectOption("1");
  await page.getByLabel("Topic hash", { exact: true }).fill(topic);
  await page.getByRole("button", { name: "Query" }).click();
  await expect
    .poll(() =>
      requests
        .filter((url) => url.pathname.endsWith("/logs"))
        .at(-1)
        ?.searchParams.get("topic_index"),
    )
    .toBe("1");
  expect(requests.at(-1)?.searchParams.has("event_signature")).toBe(false);
  await page.reload();
  await expect(page.getByLabel("Topic hash", { exact: true })).toHaveValue(topic);
  await expect(page.getByLabel("From block", { exact: true })).toHaveValue("1");
  await expect(page.getByLabel("To block", { exact: true })).toHaveValue("5");
  await page.getByRole("button", { name: "切换到中文" }).click();
  await expect(page.getByRole("heading", { name: "事件日志", exact: true })).toBeVisible();
  await expect(page.getByRole("combobox", { name: "Topic 位置", exact: true })).toHaveValue("1");
  const a11y = await new AxeBuilder({ page })
    .include(".address-logs")
    .withTags(["wcag2a", "wcag2aa", "wcag21aa"])
    .analyze();
  expect(a11y.violations).toEqual([]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    ),
  ).toBeLessThanOrEqual(1);
});
