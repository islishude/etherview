import { expect, test } from "@playwright/test";

test("block height alias supports direct navigation, refresh, tabs and back navigation", async ({
  page,
}) => {
  await page.goto("/blocks");
  await expect(page.getByRole("heading", { name: "Blocks", exact: true })).toBeVisible();

  const response = await page.goto("/block/1?tab=transactions#main-content");
  expect(response?.status()).toBe(200);
  await expect(page).toHaveURL("/blocks/1?tab=transactions#main-content");
  await expect(page.getByRole("heading", { name: "Block", exact: true })).toBeVisible();
  await expect(page.getByRole("tab", { name: "Transactions", exact: true })).toHaveAttribute(
    "aria-selected",
    "true",
  );

  await page.reload();
  await expect(page).toHaveURL("/blocks/1?tab=transactions#main-content");
  await expect(page.getByRole("tab", { name: "Transactions", exact: true })).toHaveAttribute(
    "aria-selected",
    "true",
  );

  await page.getByRole("tab", { name: "Overview", exact: true }).click();
  await expect(page).toHaveURL("/blocks/1?tab=overview");
  await expect(page.getByText("Finalized", { exact: true })).toBeVisible();

  await page.goBack();
  await expect(page).toHaveURL("/blocks/1?tab=transactions#main-content");
  await page.goBack();
  await expect(page).toHaveURL("/blocks");
});
