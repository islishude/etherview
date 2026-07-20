import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import axe from "axe-core";
import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import i18n from "./i18n";
import { makeRouter } from "./router";
import { ThemeProvider } from "./theme/ThemeProvider";
import { WalletProvider } from "./wallet/WalletProvider";

describe("explorer accessibility baseline", () => {
  beforeEach(async () => {
    document.title = "Etherview";
    await i18n.changeLanguage("en");
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        Response.json(
          { error: { code: "NOT_READY", message: "API not ready", request_id: "a11y-test" } },
          { status: 503 },
        ),
      ),
    );
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("has no automated WCAG 2.1 A/AA semantic violations in the primary shell", async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } },
    });
    const router = makeRouter(createMemoryHistory({ initialEntries: ["/blocks"] }));

    render(
      <QueryClientProvider client={queryClient}>
        <ThemeProvider>
          <WalletProvider>
            <RouterProvider router={router} />
          </WalletProvider>
        </ThemeProvider>
      </QueryClientProvider>,
    );

    expect(await screen.findByRole("heading", { name: "Blocks", level: 1 })).toBeVisible();
    expect(screen.getByRole("link", { name: "Skip to content" })).toHaveAttribute(
      "href",
      "#main-content",
    );
    expect(screen.getByRole("main")).toHaveAttribute("tabindex", "-1");

    const scan = await axe.run(document, {
      runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"] },
      // jsdom has no layout or canvas implementation. The Playwright scan
      // exercises color contrast in a real renderer; this unit gate covers the
      // semantic rules that are deterministic in the frontend test runtime.
      rules: { "color-contrast": { enabled: false } },
    });
    expect(scan.violations, JSON.stringify(scan.violations, null, 2)).toEqual([]);
  });
});
