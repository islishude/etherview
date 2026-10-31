import { expect, test } from "@playwright/test";

import { configureAppearance } from "./appearance";

const address = "0x3000000000000000000000000000000000000030";
const longName = "lookupAccountConfigurationAndAssociatedPermissionDetails";
const abi = ["view", "pure", "nonpayable", "payable"].map((stateMutability, index) => ({
  type: "function",
  name: longName,
  stateMutability,
  inputs: [{ name: "value", type: ["address", "uint256", "bytes32", "string"][index] }],
  outputs: [],
}));

for (const language of ["en", "zh"] as const) {
  for (const theme of ["light", "dark"] as const) {
    test(`contract function rows stay readable in ${language} ${theme}`, async ({
      page,
    }, testInfo) => {
      await configureAppearance(page, { width: 1440, language, theme });
      await page.route(`**/api/v1/contracts/${address}/verification`, async (route) => {
        const response = await route.fetch();
        const body = await response.json();
        body.data.abi = abi;
        await route.fulfill({ response, json: body });
      });
      for (const mode of ["read", "write"] as const) {
        await page.goto(`/address/${address}#${mode}-contract`);
        const explorer = page.locator(".abi-function-explorer");
        const cards = explorer.locator(".abi-function-card");
        await expect(cards).toHaveCount(2);
        await expect(explorer.getByRole("heading")).toHaveCount(0);
        await expect(explorer.locator(".function-index")).toHaveCount(0);
        await expect(cards.first()).toHaveAttribute("open", "");
        const summary = cards.first().locator("summary");
        await summary.focus();
        await page.keyboard.press("Enter");
        await expect(cards.first()).not.toHaveAttribute("open", "");
        await summary.click();
        await expect(cards.first()).toHaveAttribute("open", "");

        for (const width of [1440, 390]) {
          await page.setViewportSize({ width, height: 1000 });
          const rows = await cards.locator("summary").evaluateAll((elements) =>
            elements.map((element) => {
              const code = element.querySelector("code")!;
              const type = element.querySelector(".function-mutability")!;
              const arrow = element.querySelector(".native-disclosure-icon")!;
              const bounds = element.getBoundingClientRect();
              const signatureBounds = code.getBoundingClientRect();
              const typeBounds = type.getBoundingClientRect();
              const arrowBounds = arrow.getBoundingClientRect();
              const range = document.createRange();
              range.selectNodeContents(code);
              return {
                ordered:
                  signatureBounds.right <= typeBounds.left && typeBounds.right <= arrowBounds.left,
                contained:
                  [...range.getClientRects()].every(
                    (rect) => rect.left >= bounds.left && rect.right <= typeBounds.left,
                  ) && arrowBounds.right <= bounds.right,
                signature: code.textContent,
                type: type.textContent,
                border: getComputedStyle(type).borderTopWidth,
              };
            }),
          );
          for (const row of rows) {
            expect(row.ordered).toBe(true);
            expect(row.contained).toBe(true);
            expect(row.signature).toContain(longName);
            expect(row.border).toBe("0px");
          }
          expect(new Set(rows.map((row) => row.signature)).size).toBe(2);
          expect(rows.map((row) => row.type).sort()).toEqual(
            mode === "read" ? ["pure", "view"] : ["nonpayable", "payable"],
          );
          expect(
            await page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
          ).toBeLessThanOrEqual(1);
          await page.screenshot({
            path: testInfo.outputPath(`${mode}-${width}.png`),
            fullPage: true,
          });
        }
      }
    });
  }
}
