import type { Page } from "@playwright/test";

export const verificationCSRF = "c".repeat(43);
export async function installVerificationSession(page: Page, verification = true) {
  await page.route("**/api/v1/config", async (route) => {
    const response = await route.fetch();
    const payload = await response.json();
    Object.assign(payload.data.features, { user_auth: true, verification, sourcify: verification });
    await route.fulfill({ response, json: payload });
  });
  await page.route("**/api/v1/auth/session", (route) =>
    route.fulfill({
      json: {
        data: {
          authenticated: true,
          csrf_token: verificationCSRF,
          expires_at: "2099-01-01T00:00:00Z",
          user: {
            id: "018f3b52-0b3d-7bf1-b65f-6f214827cb41",
            chain_id: "1",
            address: "0x1111111111111111111111111111111111111111",
            role: "user",
            status: "active",
            created_at: "2026-01-01T00:00:00Z",
            updated_at: "2026-01-01T00:00:00Z",
          },
        },
        meta: { chain_id: "1", request_id: "verification-session" },
      },
    }),
  );
}
