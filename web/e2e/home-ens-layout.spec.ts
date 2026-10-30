import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { configureAppearance } from "./appearance";

const sender = `0x${"1".repeat(40)}`;
const recipient = `0x${"2".repeat(40)}`;
const unnamed = `0x${"3".repeat(40)}`;
const longName = "a-very-long-primary-name-for-home-layout.test";
for (const language of ["en", "zh"] as const) {
  for (const width of [390, 1440]) {
    test(`home ENS participants align in ${language} at ${width}px`, async ({ page }, testInfo) => {
      await configureAppearance(page, { width, language, theme: "light" });
      await page.route("**/api/v1/config", async (route) => {
        const response = await route.fetch();
        const body = await response.json();
        body.data.features.ens = true;
        await route.fulfill({ response, json: body });
      });
      await page.route("**/api/v1/home?*", async (route) => {
        const response = await route.fetch();
        const body = await response.json();
        const sample = body.data.transactions[0];
        body.data.transactions = [
          [sender, unnamed],
          [unnamed, sender],
          [sender, recipient],
          [unnamed, unnamed],
        ].map(([from, to], index) => ({
          ...sample,
          from,
          to,
          hash: `0x${String(index + 1).repeat(64)}`,
        }));
        await route.fulfill({ response, json: body });
      });
      await page.route("**/api/v1/address-names?*", async (route) => {
        const addresses = new URL(route.request().url()).searchParams.get("addresses")!.split(",");
        await route.fulfill({
          json: {
            data: {
              publication: "1",
              snapshot: "home-names",
              items: addresses.map((address) => ({
                address,
                state: address === unnamed ? "not_found" : "resolved",
                ...(address === unnamed
                  ? {}
                  : {
                      primary_name: {
                        name: address === recipient ? longName : "alice.test",
                        publication: "1",
                        observed_block_number: "100",
                        observed_block_hash: `0x${"1".repeat(64)}`,
                        valid_until: new Date(Date.now() + 60_000).toISOString(),
                      },
                    }),
              })),
            },
            meta: { request_id: "home-names", chain_id: "1" },
          },
        });
      });
      await page.goto("/");
      const rows = page.locator(".transaction-row");
      await expect(rows).toHaveCount(4);
      await expect(rows.getByRole("link", { name: "alice.test", exact: true })).toHaveCount(3);
      await expect(rows.getByRole("link", { name: longName, exact: true })).toHaveAttribute(
        "href",
        `/address/${recipient}`,
      );
      for (const theme of ["light", "dark"]) {
        await page.evaluate((theme) => localStorage.setItem("etherview.theme", theme), theme);
        await page.reload();
        await expect(rows.getByRole("link", { name: "alice.test", exact: true })).toHaveCount(3);
        const geometry = await rows.evaluateAll((elements) =>
          elements.map((row) => {
            const participants = [...row.querySelectorAll(".transaction-participant")];
            const bounds = row.getBoundingClientRect();
            return {
              alignment: Math.abs(
                participants[0]!.getBoundingClientRect().top -
                  participants[1]!.getBoundingClientRect().top,
              ),
              bottomSpace:
                bounds.bottom -
                Math.max(...participants.map((part) => part.getBoundingClientRect().bottom)),
              overflow: row.scrollWidth - row.clientWidth,
            };
          }),
        );
        for (const row of geometry) {
          expect(row.alignment).toBeLessThan(1);
          expect(row.bottomSpace).toBeGreaterThanOrEqual(10);
          expect(row.overflow).toBeLessThanOrEqual(1);
        }
        expect(await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBe(
          0,
        );
        await expect(
          rows.first().getByRole("link", { name: "alice.test", exact: true }),
        ).toHaveAttribute("href", `/address/${sender}`);
        await expect(rows.locator(`a[href="/address/${sender}"]`)).toHaveText([
          "alice.test",
          "alice.test",
          "alice.test",
        ]);
        await expect(rows.locator(`a[href="/address/${sender}"] code`)).toHaveCount(0);
        await expect(rows.locator(`a[href="/address/${unnamed}"] code`)).toHaveCount(4);
        const typography = await rows.first().evaluate((row) => {
          const name = getComputedStyle(row.querySelector(".address-name-link")!);
          const address = getComputedStyle(row.querySelector(".address-identity-value")!);
          return [name, address].map((style) => [
            style.color,
            style.fontFamily,
            style.fontSize,
            style.fontWeight,
            style.lineHeight,
          ]);
        });
        expect(typography[0]).toEqual(typography[1]);
        expect((await new AxeBuilder({ page }).analyze()).violations).toEqual([]);
        await page.screenshot({
          path: testInfo.outputPath(`home-ens-${theme}.png`),
          fullPage: true,
        });
      }
      await rows.first().getByRole("link", { name: "alice.test", exact: true }).click();
      await expect(page).toHaveURL(new RegExp(`/address/${sender}$`));
    });
  }
}
