import { expect, test } from "@playwright/test";

import erc1820RegistryABI from "../src/contracts/testdata/erc1820-registry-abi.json" with { type: "json" };
import historicalABIs from "../src/contracts/testdata/historical-solidity-abi.json" with { type: "json" };

const address = "0x3000000000000000000000000000000000000030";

for (const fixture of [
  {
    name: "ERC1820Registry",
    abi: erc1820RegistryABI,
    read: "getManager(address)",
    write: "setManager(address,address)",
  },
  {
    name: "Solidity 0.4.15",
    abi: [
      ...historicalABIs.find((item) => item.version === "0.4.15")!.abi,
      { name: "legacyWrite", inputs: [] },
    ],
    read: "read()",
    write: "legacyWrite()",
  },
]) {
  test(`embedded forms accept ${fixture.name} ABI`, async ({ page }) => {
    await page.route(`**/api/v1/contracts/${address}/verification`, async (route) => {
      const response = await route.fetch();
      const body = await response.json();
      body.data.abi = fixture.abi;
      await route.fulfill({ response, json: body });
    });
    await page.goto(`/address/${address}#read-contract`);
    await expect(
      page.locator(".abi-function-card").getByText(fixture.read, { exact: true }),
    ).toBeVisible();
    await expect(page.getByText("The published ABI is malformed", { exact: false })).toHaveCount(0);
    await page.getByRole("tab", { name: "Write contract", exact: true }).click();
    await expect(
      page.locator(".abi-function-card").getByText(fixture.write, { exact: true }),
    ).toBeVisible();
    await expect(
      page.locator(".abi-function-card").getByText(fixture.read, { exact: true }),
    ).toHaveCount(0);
  });
}
