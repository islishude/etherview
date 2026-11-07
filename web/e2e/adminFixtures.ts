import type { Page } from "@playwright/test";
export async function installAdminSession(
  page: Page,
  role: "admin" | "user" = "admin",
  features: Record<string, boolean> = {},
) {
  await page.route("**/api/v1/config", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.data.features.user_auth = true;
    body.data.features.api_billing = true;
    Object.assign(body.data.features, features);
    await route.fulfill({ response, json: body });
  });
  await page.route("**/api/v1/admin/overview", (route) =>
    route.fulfill({
      json: { data: { user_count: "2" }, meta: { chain_id: "1", request_id: "admin-overview" } },
    }),
  );
  await page.route("**/api/v1/admin/billing/summary**", (route) =>
    route.fulfill({
      json: {
        data: {
          payment_count: "0",
          amount_atomic: "0",
          from_time: "2026-01-01T00:00:00Z",
          to_time: "2026-01-02T00:00:00Z",
          rows: [],
        },
        meta: { chain_id: "1", request_id: "admin-summary" },
      },
    }),
  );
  await page.route("**/api/v1/auth/session", (route) =>
    route.fulfill({
      json: {
        data: {
          authenticated: true,
          csrf_token: "c".repeat(43),
          expires_at: "2099-01-01T00:00:00Z",
          user: {
            id: "00000000-0000-4000-8000-000000000001",
            chain_id: "1",
            address: "0x1111111111111111111111111111111111111111",
            role,
            status: "active",
            created_at: "2026-01-01T00:00:00Z",
            updated_at: "2026-01-01T00:00:00Z",
          },
        },
        meta: { chain_id: "1", request_id: "admin-session" },
      },
    }),
  );
}
