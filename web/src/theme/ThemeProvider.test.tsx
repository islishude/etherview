import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { resolveInitialTheme, ThemeProvider, useTheme } from "./ThemeProvider";

describe("ThemeProvider", () => {
  it("restores and toggles a persisted dark theme", async () => {
    window.localStorage.setItem("etherview.theme", "dark");
    render(
      <ThemeProvider>
        <ThemeProbe />
      </ThemeProvider>,
    );

    expect(screen.getByText("dark")).toBeVisible();
    await waitFor(() => expect(document.documentElement).toHaveAttribute("data-theme", "dark"));

    await userEvent.setup().click(screen.getByRole("button", { name: "toggle" }));
    expect(screen.getByText("light")).toBeVisible();
    expect(document.documentElement).toHaveAttribute("data-theme", "light");
    expect(window.localStorage.getItem("etherview.theme")).toBe("light");
  });

  it("uses the system preference only when no stored choice exists", () => {
    vi.spyOn(window, "matchMedia").mockReturnValue({
      matches: true,
      media: "(prefers-color-scheme: dark)",
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    });

    expect(resolveInitialTheme()).toBe("dark");
    window.localStorage.setItem("etherview.theme", "light");
    expect(resolveInitialTheme()).toBe("light");
  });
});

function ThemeProbe() {
  const { theme, toggleTheme } = useTheme();
  return (
    <button type="button" aria-label="toggle" onClick={toggleTheme}>
      {theme}
    </button>
  );
}
