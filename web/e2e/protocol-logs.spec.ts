import { expect, test } from "@playwright/test";

const system = "0xfffffffffffffffffffffffffffffffffffffffe";
const hash = `0x${"a".repeat(64)}`;
const amount = "115792089237316195423570985008687907853269984665640564039457584007913129639935";

test("protocol logs show bilingual system identity and retain raw data on mobile", async ({
  page,
}) => {
  await page.route(`**/api/v1/transactions/${hash}/logs*`, async (route) => {
    const response = await route.fetch();
    const envelope = await response.json();
    envelope.data.items = [
      {
        address: system,
        log_index: "0",
        topics: [
          "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
          `0x${"00".repeat(12)}${"11".repeat(20)}`,
          `0x${"00".repeat(12)}${"22".repeat(20)}`,
        ],
        data: `0x${"ff".repeat(32)}`,
        decoding: {
          status: "decoded",
          protocol: "eip7708",
          event_name: "Transfer",
          signature: "Transfer(address,address,uint256)",
          arguments: [
            {
              name: "from",
              type: "address",
              indexed: true,
              hashed: false,
              value: `0x${"11".repeat(20)}`,
            },
            {
              name: "to",
              type: "address",
              indexed: true,
              hashed: false,
              value: `0x${"22".repeat(20)}`,
            },
            { name: "value", type: "uint256", indexed: false, hashed: false, value: amount },
          ],
          candidates: [],
          attribution: { mode: "protocol", trace_path: [] },
        },
      },
    ];
    await route.fulfill({ response, json: envelope });
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`/tx/${hash}?tab=logs`);
  const card = page.locator(".transaction-log");
  await expect(card.getByText("ETH transfer · EIP-7708", { exact: true }).first()).toBeVisible();
  const marker = card.getByRole("button", { name: "System Address", exact: true });
  await expect(marker).toBeVisible();
  await expect(page.getByRole("tooltip")).toHaveCount(0);
  await marker.click();
  await expect(page.getByRole("tooltip")).toHaveText("System Address");
  await expect(page).toHaveURL(new RegExp(`/tx/${hash}`));
  await page.keyboard.press("Escape");
  await expect(page.getByRole("tooltip")).toHaveCount(0);
  await expect(card.getByText(amount, { exact: true })).toBeVisible();
  await card.getByText("More details", { exact: true }).click();
  await expect(card.getByRole("heading", { name: "Protocol source" })).toBeVisible();
  await expect(card.getByRole("heading", { name: "ABI provenance" })).toHaveCount(0);
  await expect(card.locator(".transaction-log-data code")).toHaveText(`0x${"ff".repeat(32)}`);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    ),
  ).toBeLessThanOrEqual(1);
  await page.getByRole("button", { name: "切换到中文" }).click();
  await card.getByRole("button", { name: "系统地址", exact: true }).focus();
  await expect(page.getByRole("tooltip")).toHaveText("系统地址");
  await expect(card.getByRole("heading", { name: "协议来源" })).toBeVisible();
  await card.getByRole("link").first().click();
  await expect(page).toHaveURL(new RegExp(`/address/${system}`, "i"));
  await expect(
    page.locator(".address-header-actions").getByRole("button", { name: "系统地址" }),
  ).toBeVisible();
});
