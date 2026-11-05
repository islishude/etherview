import { configureAppearance, collectBrowserErrors } from "./appearance";
import fixture from "../src/contracts/fixtures/indexed-events.json" with { type: "json" };
import { parseVerifiedABI } from "../src/contracts/abi";
import { toEventSignature } from "viem";
import { keccak256, stringToHex } from "viem";
import { expect, test, type Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

const address = `0x${"11".repeat(20)}`;
const hash = `0x${"aa".repeat(32)}`;
const topic = `0x${"bb".repeat(32)}`;
test("contract event logs filter names and positioned topics in both languages on mobile", async ({
  page,
}) => {
  const requests: URL[] = [];
  await mockAddress(page, requests);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`/address/${address}?tab=logs`);
  await expect(page.getByText("Block 5", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: /Event signature:/ }).click();
  await page.getByRole("combobox", { name: "Search event names" }).fill("Ch");
  await page.getByRole("option", { name: /Changed\(uint256\)/ }).click();
  await page.getByLabel("From block", { exact: true }).fill("1");
  await page.getByLabel("To block", { exact: true }).fill("5");
  await page.getByRole("button", { name: "Query" }).click();
  await expect(page).toHaveURL(/event_id=/);
  await page.getByRole("button", { name: "Topic", exact: true }).click();
  await page.getByRole("button", { name: "Add condition" }).click();
  await page.getByRole("combobox", { name: "Topic position", exact: true }).selectOption("1");
  await expect(page.getByRole("combobox", { name: "Topic position", exact: true })).toBeFocused();
  await page.getByLabel("Topic hash", { exact: true }).fill(topic);
  await page.getByRole("button", { name: "Query" }).click();
  await expect
    .poll(() =>
      requests
        .filter((url) => url.pathname.endsWith("/logs"))
        .at(-1)
        ?.searchParams.get("topic1"),
    )
    .toBe(topic);
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

function eventFixture(name: string, signature: string) {
  return {
    name,
    signature,
    event_id: keccak256(stringToHex(signature)),
    anonymous: false,
    indexed_fields: [],
  };
}

async function mockAddress(page: Page, requests: URL[]) {
  const meta = { request_id: "address-events", chain_id: "1" };
  await page.route(new RegExp(`/api/v1/addresses/${address}(?:[/?]|$)`), async (route) => {
    const url = new URL(route.request().url());
    requests.push(url);
    let data: unknown;
    if (url.pathname.endsWith("/log-events"))
      data = [
        eventFixture("Changed", "Changed(uint256)"),
        ...parseVerifiedABI(fixture.abi)
          .filter((event) => event.type === "event")
          .map((event) => {
            let position = event.anonymous ? 0 : 1;
            return {
              ...eventFixture(event.name, toEventSignature(event)),
              anonymous: event.anonymous ?? false,
              indexed_fields: event.inputs.flatMap((field, parameter_index) =>
                field.indexed
                  ? [{ ...field, name: field.name ?? "", parameter_index, topic_index: position++ }]
                  : [],
              ),
            };
          }),
      ];
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
}

for (const language of ["en", "zh"] as const)
  for (const theme of ["light", "dark"] as const) {
    test(`typed indexed tuple filters ${language} ${theme}`, async ({ page }, testInfo) => {
      await configureAppearance(page, { width: 390, language, theme });
      const errors = collectBrowserErrors(page);
      const requests: URL[] = [];
      await mockAddress(page, requests);
      await page.goto(`/address/${address}?tab=logs`);
      await page
        .getByRole("button", { name: language === "en" ? /Event signature:/ : /事件签名/ })
        .click();
      await page.getByRole("option", { name: /Tuple\(/ }).click();
      await page
        .getByRole("button", {
          name: language === "en" ? "Add condition" : "添加条件",
          exact: true,
        })
        .click();
      await page.getByRole("textbox", { name: /label.*string/ }).fill("abc");
      await page.getByRole("textbox", { name: /amount.*int16/ }).fill("-123");
      await page.getByRole("textbox", { name: /payload.*bytes/ }).fill("0xf00d");
      const add = page.getByRole("button", {
        name: language === "en" ? "Add array item" : "添加数组项",
      });
      await add.click();
      await page
        .getByRole("textbox", { name: /values.*uint16/ })
        .first()
        .fill("1");
      await add.click();
      await page
        .getByRole("textbox", { name: /values.*uint16/ })
        .nth(1)
        .fill("65535");
      await page
        .getByRole("button", { name: language === "en" ? "Query" : "查询", exact: true })
        .click();
      await expect
        .poll(() =>
          requests
            .filter((url) => url.pathname.endsWith("/logs"))
            .at(-1)
            ?.searchParams.get("topic1"),
        )
        .toBe(fixture.cases[3]!.topics[1]);
      await expect(page.getByRole("textbox", { name: /label.*string/ })).toHaveValue("abc");
      expect(
        (
          await new AxeBuilder({ page })
            .include(".address-logs")
            .withTags(["wcag2a", "wcag2aa", "wcag21aa"])
            .analyze()
        ).violations,
      ).toEqual([]);
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
        ),
      ).toBeLessThanOrEqual(1);
      await page.screenshot({
        path: testInfo.outputPath(`indexed-${language}-${theme}-mobile.png`),
        fullPage: true,
      });
      await page.setViewportSize({ width: 1440, height: 1000 });
      await page.screenshot({
        path: testInfo.outputPath(`indexed-${language}-${theme}-desktop.png`),
        fullPage: true,
      });
      expect(errors).toEqual([]);
    });
  }
