import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { CopyButton } from "./CopyButton";

afterEach(async () => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  await i18n.changeLanguage("en");
});

it.each(["en", "zh"])(
  "copies the exact value by keyboard with %s accessible feedback",
  async (language) => {
    await i18n.changeLanguage(language);
    const user = userEvent.setup();
    const value = "0xffffFFFfFFffffffffffffffFfFFFfffFFFfFFfE";
    render(<CopyButton value={value} />);
    const button = screen.getByRole("button", { name: language === "en" ? "Copy" : "复制" });
    const before = button.querySelector("svg")!;
    expect(before).toHaveClass("lucide-copy");
    expect(before).toHaveAttribute("aria-hidden", "true");
    await user.tab();
    expect(button).toHaveFocus();
    await user.keyboard("{Enter}");
    expect(await navigator.clipboard.readText()).toBe(value);
    const copied = language === "en" ? "Copied" : "已复制";
    expect(button).toHaveAccessibleName(copied);
    expect(button).toHaveAttribute("title", copied);
    const after = button.querySelector("svg")!;
    expect(after).toHaveClass("lucide-check");
    expect(after.getAttribute("width")).toBe(before.getAttribute("width"));
    expect(after.getAttribute("height")).toBe(before.getAttribute("height"));
  },
);

it("resets success after the latest copy, and never shows success on rejection", async () => {
  userEvent.setup();
  const write = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue();
  vi.useFakeTimers();
  render(<CopyButton value="exact value" />);
  const button = screen.getByRole("button", { name: "Copy" });
  await act(async () => {
    fireEvent.click(button);
  });
  expect(button).toHaveAccessibleName("Copied");
  await act(async () => {
    vi.advanceTimersByTime(800);
  });
  await act(async () => {
    fireEvent.click(button);
  });
  await act(async () => {
    vi.advanceTimersByTime(800);
  });
  expect(button).toHaveAccessibleName("Copied");
  await act(async () => {
    vi.advanceTimersByTime(400);
  });
  expect(button).toHaveAccessibleName("Copy");
  write.mockRejectedValueOnce(new Error("clipboard unavailable"));
  await act(async () => {
    fireEvent.click(button);
  });
  expect(button).toHaveAccessibleName("Copy");
  expect(button.querySelector("svg")).toHaveClass("lucide-copy");
});
