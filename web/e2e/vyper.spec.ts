import { expect, test } from "@playwright/test";

test("Vyper submission preserves its target, sources and optimization mode", async ({ page }) => {
  const address = `0x${"12".repeat(20)}`;
  const id = "123e4567-e89b-42d3-a456-426614174000";
  const meta = { request_id: "vyper-browser", chain_id: "1" };
  await page.route("**/api/v1/config", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.data.features.verification = true;
    await route.fulfill({ json: body });
  });
  await page.route("**/api/v1/verifier/compilers?**", async (route) => {
    const language = new URL(route.request().url()).searchParams.get("language");
    await route.fulfill({ json: { data: { language, versions: language === "vyper" ? ["0.4.3"] : ["0.8.30"] }, meta } });
  });
  const job = {
    id, kind: "address", status: "succeeded", created_at: "2026-09-08T00:00:00Z", updated_at: "2026-09-08T00:00:01Z",
    outcome: {
      kind: "verification_success", language: "vyper", compiler_version: "0.4.3", file_name: "A.vy", contract_name: "A",
      sources: {}, settings: {}, compilation_artifacts: {}, creation_code_artifacts: {}, runtime_code_artifacts: {}, libraries: {}, is_blueprint: false,
      runtime_match: { match_type: "partial", transformations: [], values: {} },
    },
  };
  await page.route(`**/api/v1/contracts/${address}/verification`, async (route) => {
    expect(route.request().postDataJSON()).toMatchObject({
      language: "vyper", compiler_version: "0.4.3", input_kind: "multipart", target_file: "A.vy", optimization_mode: "codesize",
    });
    expect(route.request().postDataJSON().sources["A.vy"]).toContain("def value()");
    expect(route.request().postDataJSON()).not.toHaveProperty("optimization_runs");
    await route.fulfill({ status: 202, json: { data: job, meta } });
  });
  await page.route(`**/api/v1/verifier/jobs/${id}`, (route) => route.fulfill({ json: { data: job, meta } }));
  await page.goto("/verify");
  await page.getByRole("combobox", { name: "Language", exact: true }).selectOption("vyper");
  await expect(page.getByRole("combobox", { name: "Compiler version", exact: true })).toHaveValue("0.4.3");
  await page.getByRole("combobox", { name: "Input format", exact: true }).selectOption("multipart");
  await page.getByLabel("Address", { exact: true }).fill(address);
  await page.getByLabel(/^API key/).fill("browser-only-test-key");
  await page.getByRole("combobox", { name: "Optimization mode", exact: true }).selectOption("codesize");
  await page.getByRole("button", { name: "Submit verification", exact: true }).click();
  await expect(page.getByText("partial", { exact: true }).first()).toBeVisible();
});
