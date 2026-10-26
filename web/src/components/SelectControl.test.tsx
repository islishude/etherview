import { createRef } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it } from "vitest";
import { SelectControl } from "./SelectControl";

it("retains native labels, refs, selection, disabled state and form values", async () => {
  const ref = createRef<HTMLSelectElement>();
  const { rerender } = render(
    <form aria-label="Settings">
      <label htmlFor="network">Network</label>
      <SelectControl id="network" name="network" ref={ref} defaultValue="1">
        <option value="1">Ethereum</option>
        <option value="2">Preview</option>
      </SelectControl>
    </form>,
  );
  const select = screen.getByRole("combobox", { name: "Network" });
  expect(ref.current).toBe(select);
  await userEvent.setup().selectOptions(select, "2");
  expect(new FormData(screen.getByRole("form") as HTMLFormElement).get("network")).toBe("2");
  expect(select.parentElement?.querySelector("svg")).toHaveAttribute("aria-hidden", "true");
  rerender(
    <SelectControl aria-label="Network" disabled>
      <option>Ethereum</option>
    </SelectControl>,
  );
  expect(screen.getByRole("combobox", { name: "Network" })).toBeDisabled();
});
