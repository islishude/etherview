import { expect, test } from "@playwright/test";

const operationHash = `0x${"12".repeat(32)}`;

test("native EventSource recovers from an expired cursor without duplicate instances", async ({
  page,
  context,
}) => {
  await page.addInitScript(() => {
    const Native = window.EventSource;
    const state = { created: 0, active: 0, maximum: 0, opened: 0 };
    Object.assign(window, { eventSourceProbe: state });
    window.EventSource = class extends Native {
      private counted = true;
      constructor(url: string | URL, options?: EventSourceInit) {
        super(url, options);
        state.created++;
        state.active++;
        state.maximum = Math.max(state.maximum, state.active);
        this.addEventListener("open", () => {
          state.opened++;
        });
        this.addEventListener("error", () => {
          if (this.readyState === Native.CLOSED) this.releaseCount();
        });
      }
      private releaseCount() {
        if (this.counted) state.active--;
        this.counted = false;
      }
      override close() {
        this.releaseCount();
        super.close();
      }
    };
  });
  const cursors: (string | undefined)[] = [];
  let blockReads = 0;
  page.on("request", (request) => {
    if (new URL(request.url()).pathname === "/api/v1/blocks") blockReads++;
  });
  await context.addCookies([
    { name: "etherview_e2e_home", value: `recovery-${Date.now()}`, url: "http://127.0.0.1:4173" },
  ]);
  page.on("request", (request) => {
    if (new URL(request.url()).pathname === "/api/v1/events") {
      const index = cursors.length;
      cursors.push(undefined);
      void request.allHeaders().then((headers) => {
        cursors[index] = headers["last-event-id"];
      });
    }
  });
  await page.goto("/blocks");
  await expect
    .poll(() => page.evaluate(() => Reflect.get(window, "eventSourceProbe").opened))
    .toBe(1);
  await page.evaluate(() => fetch("/__e2e/home/head", { method: "POST" }));
  await expect.poll(() => blockReads).toBeGreaterThan(1);
  await page.evaluate(() => fetch("/__e2e/events/expire", { method: "POST" }));
  await expect.poll(() => cursors.length, { timeout: 10000 }).toBe(3);
  await expect.poll(() => cursors).toEqual([undefined, "2", undefined]);
  await expect
    .poll(() => page.evaluate(() => Reflect.get(window, "eventSourceProbe").opened))
    .toBe(2);
  await expect.poll(() => blockReads).toBeGreaterThan(1);
  const probe = await page.evaluate(() => Reflect.get(window, "eventSourceProbe"));
  expect(probe.created).toBe(2);
  expect(probe.maximum).toBe(1);
  expect(probe.active).toBe(1);
});

test("UserOperation pages refresh on heads and withdraw orphaned detail after a reorg", async ({
  context,
  page,
}) => {
  await context.addCookies([
    { name: "etherview_e2e_home", value: `userop-${Date.now()}`, url: "http://127.0.0.1:4173" },
  ]);
  let listReads = 0;
  let detailReads = 0;
  let orphaned = false;
  await page.route(`**/api/v1/user-operations/${operationHash}`, async (route) => {
    detailReads++;
    if (orphaned) {
      await route.fulfill({
        status: 404,
        contentType: "application/json",
        body: JSON.stringify({
          error: {
            code: "not_found",
            message: "UserOperation not found",
            request_id: "reorg-test",
          },
        }),
      });
    } else {
      await route.continue();
    }
  });
  page.on("request", (request) => {
    if (new URL(request.url()).pathname === "/api/v1/user-operations") listReads++;
  });
  await page.goto("/user-operations");
  await expect(page.getByRole("table").getByRole("link", { name: /0x121212/u })).toBeVisible();
  const beforeHead = listReads;
  await page.evaluate(() => fetch("/__e2e/home/head", { method: "POST" }));
  await expect.poll(() => listReads).toBeGreaterThan(beforeHead);
  await page
    .getByRole("table")
    .getByRole("link", { name: /0x121212/u })
    .click();
  await expect(page.getByText("paymaster rejected", { exact: true }).first()).toBeVisible();
  const beforeDetailHead = detailReads;
  await page.evaluate(() => fetch("/__e2e/home/head", { method: "POST" }));
  await expect.poll(() => detailReads).toBeGreaterThan(beforeDetailHead);
  orphaned = true;
  await page.evaluate(() => fetch("/__e2e/home/reorg", { method: "POST" }));
  await expect(page.getByText("paymaster rejected", { exact: true })).toHaveCount(0);
  await expect(page.getByText("0xdeadbeef", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("status")).toContainText("Indexed entity not found");
});
