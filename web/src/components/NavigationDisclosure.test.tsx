import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { NavigationDisclosure } from "./NavigationDisclosure";

function setup() {
  render(
    <>
      <NavigationDisclosure label="More">
        <a href="#pending">Pending</a>
        <a href="#status">Status</a>
      </NavigationDisclosure>
      <button>Outside</button>
    </>,
  );
  return userEvent.setup();
}

describe("navigation disclosure", () => {
  it("opens with the keyboard, allows Tab navigation and restores focus on Escape", async () => {
    const user = setup();
    const trigger = screen.getByRole("button", { name: "More" });
    expect(screen.queryByRole("link", { name: "Pending" })).toBeNull();
    trigger.focus();
    await user.keyboard("{Enter}{Tab}");
    expect(screen.getByRole("link", { name: "Pending" })).toHaveFocus();
    expect(trigger).toHaveAttribute("aria-expanded", "true");
    await user.keyboard("{Escape}");
    expect(trigger).toHaveFocus();
    expect(trigger).toHaveAttribute("aria-expanded", "false");
  });

  it("closes on outside pointer, focus departure and navigation", async () => {
    const user = setup();
    const trigger = screen.getByRole("button", { name: "More" });
    await user.click(trigger);
    fireEvent.pointerDown(document.body);
    expect(trigger).toHaveAttribute("aria-expanded", "false");
    await user.click(trigger);
    await user.tab();
    await user.tab();
    await user.tab();
    expect(screen.getByRole("button", { name: "Outside" })).toHaveFocus();
    expect(trigger).toHaveAttribute("aria-expanded", "false");
    await user.click(trigger);
    await user.click(screen.getByRole("link", { name: "Status" }));
    expect(trigger).toHaveAttribute("aria-expanded", "false");
    expect(trigger).toHaveFocus();
  });
});
