import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { resolveInitialTheme, ThemeProvider, useTheme } from "./ThemeProvider";

afterEach(() => vi.unstubAllGlobals());

describe("ThemeProvider", () => {
  it("keeps manual choices until following the current system theme again, including after remount", async () => {
    const system = mockSystemTheme(false);
    const user = userEvent.setup();
    const mount = () =>
      render(
        <ThemeProvider>
          <ThemeProbe />
        </ThemeProvider>,
      );
    let view = mount();
    await user.click(screen.getByRole("button", { name: "toggle" }));
    expect(window.localStorage.getItem("etherview.theme")).toBe("dark");
    view.unmount();
    view = mount();
    for (const dark of [true, false]) {
      act(() => system.change(dark));
      expect(document.documentElement).toHaveAttribute("data-theme", "dark");
    }
    await user.click(screen.getByRole("button", { name: "system" }));
    expect(document.documentElement).toHaveAttribute("data-theme", "light");
    expect(window.localStorage.getItem("etherview.theme")).toBeNull();
    view.unmount();
    mount();
    act(() => system.change(true));
    expect(document.documentElement).toHaveAttribute("data-theme", "dark");
    expect(screen.getByRole("button", { name: "system" })).toHaveAttribute("aria-pressed", "true");
  });

  it("follows system changes when storage reads and writes are unavailable", async () => {
    const system = mockSystemTheme(false);
    for (const method of ["getItem", "setItem", "removeItem"] as const) {
      vi.spyOn(window.localStorage, method).mockImplementation(() => {
        throw new Error("Storage unavailable");
      });
    }
    render(
      <ThemeProvider>
        <ThemeProbe />
      </ThemeProvider>,
    );
    act(() => system.change(true));
    expect(document.documentElement).toHaveAttribute("data-theme", "dark");
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "toggle" }));
    expect(document.documentElement).toHaveAttribute("data-theme", "light");
    await user.click(screen.getByRole("button", { name: "system" }));
    expect(document.documentElement).toHaveAttribute("data-theme", "dark");
  });

  it("ignores invalid stored preferences and defaults to light without matchMedia", () => {
    window.localStorage.setItem("etherview.theme", "invalid");
    vi.stubGlobal("matchMedia", undefined);
    render(
      <ThemeProvider>
        <ThemeProbe />
      </ThemeProvider>,
    );
    expect(document.documentElement).toHaveAttribute("data-theme", "light");
    expect(window.localStorage.getItem("etherview.theme")).toBeNull();
  });

  it.each([false, true])(
    "follows system changes without persisting an automatic choice (dark=%s)",
    (dark) => {
      const system = mockSystemTheme(dark);
      const view = render(
        <ThemeProvider>
          <ThemeProbe />
        </ThemeProvider>,
      );
      expect(document.documentElement).toHaveAttribute("data-theme", dark ? "dark" : "light");
      for (const next of [!dark, dark]) {
        act(() => system.change(next));
        expect(document.documentElement).toHaveAttribute("data-theme", next ? "dark" : "light");
        expect(screen.getByRole("button", { name: "toggle" })).toHaveTextContent(
          next ? "dark" : "light",
        );
        expect(window.localStorage.getItem("etherview.theme")).toBeNull();
      }
      view.unmount();
      expect(system.media.removeEventListener).toHaveBeenCalledWith("change", expect.any(Function));
    },
  );

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

function mockSystemTheme(dark: boolean) {
  const events = new EventTarget();
  const media = {
    matches: dark,
    media: "(prefers-color-scheme: dark)",
    onchange: null,
    addEventListener: vi.fn(events.addEventListener.bind(events)),
    removeEventListener: vi.fn(events.removeEventListener.bind(events)),
    addListener: vi.fn(),
    removeListener: vi.fn(),
    dispatchEvent: events.dispatchEvent.bind(events),
  } satisfies MediaQueryList;
  vi.spyOn(window, "matchMedia").mockReturnValue(media);
  return {
    media,
    change(matches: boolean) {
      media.matches = matches;
      media.dispatchEvent(new Event("change"));
    },
  };
}

function ThemeProbe() {
  const { theme, toggleTheme, followsSystem, followSystem } = useTheme();
  return (
    <>
      <button type="button" aria-label="toggle" onClick={toggleTheme}>
        {theme}
      </button>
      <button type="button" aria-label="system" aria-pressed={followsSystem} onClick={followSystem}>
        System
      </button>
    </>
  );
}
