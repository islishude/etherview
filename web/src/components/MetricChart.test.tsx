import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ThemeProvider, useTheme } from "@/theme/ThemeProvider";
import { MetricChart } from "./MetricChart";

const { setOption } = vi.hoisted(() => ({ setOption: vi.fn() }));
vi.mock("echarts/core", () => ({
  use: vi.fn(),
  init: vi.fn(() => ({ setOption, resize: vi.fn(), dispose: vi.fn() })),
}));

const points = [
  {
    bucket_start: "2026-01-01T00:00:00Z",
    bucket_end: "2026-01-02T00:00:00Z",
    from_block: "1",
    to_block: "42",
    partial: false,
    value: "42",
  },
];
let stylesheet: HTMLStyleElement;

afterEach(() => {
  stylesheet?.remove();
  setOption.mockClear();
});

describe("chart theme palette", () => {
  it.each([
    { initialTheme: "light", automatic: false },
    { initialTheme: "dark", automatic: false },
    { initialTheme: "light", automatic: true },
    { initialTheme: "dark", automatic: true },
  ] as const)(
    "uses the applied $initialTheme palette on mount and after switching both ways (automatic=$automatic)",
    async ({ initialTheme, automatic }) => {
      // Real CSS selection exposes effect ordering without mocking computed styles.
      stylesheet = document.createElement("style");
      stylesheet.textContent = `
        :root { --brand: #245bcc; --text-soft: #596779; }
        :root[data-theme="dark"] { --brand: #8bb5ff; --text-soft: #a5afbf; }
      `;
      document.head.append(stylesheet);
      const events = new EventTarget();
      let systemDark = initialTheme === "dark";
      if (automatic) {
        vi.spyOn(window, "matchMedia").mockImplementation((query) => ({
          matches: query === "(prefers-color-scheme: dark)" && systemDark,
          media: query,
          onchange: null,
          addEventListener: events.addEventListener.bind(events),
          removeEventListener: events.removeEventListener.bind(events),
          dispatchEvent: events.dispatchEvent.bind(events),
          addListener: vi.fn(),
          removeListener: vi.fn(),
        }));
      } else {
        window.localStorage.setItem("etherview.theme", initialTheme);
      }
      document.documentElement.dataset.theme = initialTheme === "dark" ? "light" : "dark";
      render(
        <ThemeProvider>
          <ThemeToggle />
          <MetricChart data={points} label="Transactions" locale="en" resetKey={0} />
        </ThemeProvider>,
      );
      const user = userEvent.setup();
      const opposite = initialTheme === "dark" ? "light" : "dark";
      for (const [index, theme] of [initialTheme, opposite, initialTheme].entries()) {
        if (index > 0) {
          if (automatic) {
            act(() => {
              systemDark = theme === "dark";
              events.dispatchEvent(new Event("change"));
            });
          } else {
            await user.click(screen.getByRole("button", { name: "Toggle theme" }));
          }
        }
        expect(setOption).toHaveBeenLastCalledWith(
          expect.objectContaining({
            color: [theme === "dark" ? "#8bb5ff" : "#245bcc"],
            textStyle: expect.objectContaining({
              color: theme === "dark" ? "#a5afbf" : "#596779",
            }),
          }),
        );
      }
    },
  );
});

function ThemeToggle() {
  const { toggleTheme } = useTheme();
  return <button onClick={toggleTheme}>Toggle theme</button>;
}
