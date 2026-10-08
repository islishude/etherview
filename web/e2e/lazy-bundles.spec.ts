import { readdirSync, statSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { expect, test } from "@playwright/test";

test("production JavaScript chunks stay below the default Vite warning limit", () => {
  const assets = new URL("../dist/assets/", import.meta.url);
  const chunks = readdirSync(assets).filter((name) => name.endsWith(".js"));
  expect(chunks.length).toBeGreaterThan(0);
  for (const chunk of chunks) {
    expect(statSync(fileURLToPath(new URL(chunk, assets))).size, chunk).toBeLessThanOrEqual(
      500 * 1024,
    );
  }
});

test("verified source and chart bundles load only when their views are opened", async ({
  page,
}) => {
  const scripts: string[] = [];
  const errors: string[] = [];
  page.on("request", (request) => {
    if (request.resourceType() === "script") scripts.push(new URL(request.url()).pathname);
  });
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto("/address/0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266?tab=delegation#history");
  await expect(page.getByRole("heading", { name: "Delegation history" })).toBeVisible();
  expect(
    scripts.some((path) =>
      /ContractArtifactPanel-|codemirror-vendor|echarts-vendor|zrender-vendor/.test(path),
    ),
  ).toBe(false);

  const tabs = page.getByRole("tablist", { name: "Delegated account sections" });
  await tabs.getByRole("tab", { name: "Code", exact: true }).click();
  await expect(page.locator(".cm-editor")).toBeVisible();
  expect(scripts.some((path) => path.includes("ContractArtifactPanel-"))).toBe(true);
  expect(scripts.some((path) => path.includes("codemirror-vendor"))).toBe(true);
  expect(scripts.some((path) => /echarts-vendor|zrender-vendor/.test(path))).toBe(false);

  await page.goto("/charts/execution-fees?range=7d&interval=day");
  await expect(page.locator("canvas").first()).toBeVisible();
  expect(scripts.some((path) => path.includes("echarts-vendor"))).toBe(true);
  expect(scripts.some((path) => path.includes("zrender-vendor"))).toBe(true);
  expect(errors).toEqual([]);
});
