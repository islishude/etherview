import { expect, test } from "@playwright/test";

import { configureAppearance } from "./appearance";

const address = "0x1111111111111111111111111111111111111111";
const otherAddress = "0x2222222222222222222222222222222222222222";
const tokenID = ((1n << 256n) - 1n).toString();
const tokenLabel = "#11579208…639935";

for (const locale of ["en", "zh"] as const) {
  test(`address NFT transfers keep long values and adjacent columns readable in ${locale}`, async ({
    page,
  }, testInfo) => {
    await configureAppearance(page, { width: 1440, language: locale, theme: "light" });
    await page.route(`**/api/v1/addresses/${address}/nft-transfers?*`, async (route) => {
      const response = await route.fetch();
      const envelope = await response.json();
      const transfer = envelope.data[0];
      envelope.data = ["erc721", "erc1155"].map((standard, index) => ({
        ...transfer,
        standard,
        log_index: String(index),
        token_address: otherAddress,
        from: address,
        to: otherAddress,
        token_id: tokenID,
        amount: standard === "erc721" ? undefined : "12345",
      }));
      await route.fulfill({ response, json: envelope });
    });
    await page.goto(`/address/${address}?tab=nft-transfers`);
    const table = page.locator(".address-activity-table");
    await expect(table.locator("tbody tr")).toHaveCount(2);
    const tokenLinks = table.getByRole("link", { name: tokenLabel, exact: true });
    await expect(tokenLinks).toHaveCount(2);
    for (const link of await tokenLinks.all()) {
      await expect(link).toHaveAttribute("title", tokenID);
      await expect(link).toHaveAttribute("href", `/nft/${otherAddress}/${tokenID}`);
    }

    for (const width of [390, 768, 1440]) {
      await page.setViewportSize({ width, height: 1000 });
      // Check rendered text geometry, not CSS declarations: short identities and
      // timestamps must stay on one line alongside compact uint256 token IDs.
      const brokenText = await table.locator("tbody td").evaluateAll((cells) =>
        cells.flatMap((cell) => {
          const bounds = cell.getBoundingClientRect();
          const walker = document.createTreeWalker(cell, NodeFilter.SHOW_TEXT);
          const failures: string[] = [];
          while (walker.nextNode()) {
            const node = walker.currentNode;
            if (!node.textContent?.trim()) continue;
            const range = document.createRange();
            range.selectNodeContents(node);
            const rects = [...range.getClientRects()];
            if (
              new Set(rects.map((rect) => Math.round(rect.top))).size > 1 ||
              rects.some((rect) => rect.left < bounds.left - 1 || rect.right > bounds.right + 1)
            ) {
              failures.push(node.textContent);
            }
          }
          return failures;
        }),
      );
      expect(brokenText).toEqual([]);
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
      ).toBeLessThanOrEqual(1);
      const scroller = page.locator(".address-activity .table-scroll");
      await scroller.scrollIntoViewIfNeeded();
      expect(await scroller.evaluate((element) => element.scrollWidth > element.clientWidth)).toBe(
        width < 1440,
      );
      await scroller.evaluate((element) => {
        element.scrollLeft = element.scrollWidth;
      });
      await expect(tokenLinks.first()).toBeInViewport();
      await page.screenshot({
        path: testInfo.outputPath(`nft-transfers-${width}.png`),
        fullPage: true,
      });
    }
  });
}
