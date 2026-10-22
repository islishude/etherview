import { expect, test } from "@playwright/test";

for (const language of ["en", "zh"] as const) {
  test(`home indexing summary preserves states and exact heights in ${language}`, async ({
    page,
  }) => {
    await page.addInitScript((language) => {
      localStorage.setItem("etherview.language", language);
    }, language);
    const height = "9007199254740993123456789";
    const formattedHeight = "9,007,199,254,740,993,123,456,789";
    let mode: "ready" | "lagging" | "not-ready" | "zero" | "missing" = "ready";
    await page.route("**/api/v1/home?*", async (route) => {
      if (mode === "missing") {
        await route.fulfill({
          status: 503,
          json: { error: { code: "NOT_READY", message: "not ready", request_id: "home-summary" } },
        });
        return;
      }
      const response = await route.fetch();
      const body = await response.json();
      body.data.status.core_ready = mode !== "not-ready";
      body.data.status.lag = mode === "lagging" ? "1" : "0";
      body.data.status.indexed_block = mode === "zero" ? "0" : height;
      body.data.status.latest_block = mode === "zero" ? "0" : height;
      await route.fulfill({ response, json: body });
    });

    const summary = page.locator(".home-indexing-summary");
    const values = summary.getByRole("definition");
    for (const state of ["ready", "lagging", "not-ready", "zero", "missing"] as const) {
      mode = state;
      await page.goto("/");
      const expectedHeight = state === "missing" ? "—" : state === "zero" ? "0" : formattedHeight;
      const expectedState =
        state === "missing"
          ? "—"
          : state === "ready" || state === "zero"
            ? language === "en"
              ? "Caught up"
              : "已追平"
            : language === "en"
              ? "Syncing"
              : "同步中";
      await expect(values).toHaveText([expectedHeight, expectedHeight, expectedState]);
      await expect(summary.getByRole("term")).toHaveText(
        language === "en"
          ? ["Indexed block", "Network head", "Indexing state"]
          : ["已索引区块", "网络最新区块", "索引状态"],
      );
    }

    mode = "ready";
    await page.goto("/");
    await expect(values.first()).toHaveText(formattedHeight);
    for (const theme of ["light", "dark"]) {
      await page.evaluate((theme) => {
        localStorage.setItem("etherview.theme", theme);
      }, theme);
      await page.reload();
      await expect(values.first()).toHaveText(formattedHeight);
      await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
      for (const width of [390, 768, 1440]) {
        await page.setViewportSize({ width, height: 1000 });
        const layout = await summary.evaluate((element) => {
          const bounds = element.getBoundingClientRect();
          const activity = document.querySelector(".activity-grid")!.getBoundingClientRect();
          const style = getComputedStyle(element);
          return {
            afterActivity: bounds.top >= activity.bottom,
            overflow: document.documentElement.scrollWidth - innerWidth,
            fontSize: parseFloat(style.fontSize),
            background: style.backgroundColor,
            shadow: style.boxShadow,
            items: Array.from(element.children, (item) => {
              const term = item.querySelector("dt")!;
              const value = item.querySelector("dd")!;
              return {
                withinSummary: item.getBoundingClientRect().right <= bounds.right + 1,
                valueOverflow: value.scrollWidth - value.clientWidth,
                sameLine:
                  Math.abs(term.getBoundingClientRect().top - value.getBoundingClientRect().top) <
                  1,
                sameColor: getComputedStyle(term).color === getComputedStyle(value).color,
              };
            }),
          };
        });
        expect(layout.afterActivity).toBe(true);
        expect(layout.overflow).toBeLessThanOrEqual(1);
        expect(layout.fontSize).toBeGreaterThanOrEqual(12);
        expect(layout.fontSize).toBeLessThanOrEqual(13);
        expect(layout.background).toBe("rgba(0, 0, 0, 0)");
        expect(layout.shadow).toBe("none");
        for (const item of layout.items) {
          expect(item.withinSummary).toBe(true);
          expect(item.valueOverflow).toBeLessThanOrEqual(1);
          expect(item.sameLine).toBe(true);
          expect(item.sameColor).toBe(true);
        }
      }
    }
  });
}
