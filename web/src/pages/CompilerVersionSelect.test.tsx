import { useState } from "react";
import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import "@/i18n/resources/sourcify";
import { CompilerVersionSelect } from "./CompilerVersionSelect";

const versions = ["0.8.30+commit.73712a01", "0.8.10+commit.ABC", "0.8.9+commit.def"];
function setup(disabled = false) {
  const onChange = vi.fn();
  const onSubmit = vi.fn((event) => event.preventDefault());
  function Form() {
    const [value, setValue] = useState(versions[0]!);
    return (
      <form onSubmit={onSubmit}>
        <label htmlFor="compiler">Compiler version</label>
        <CompilerVersionSelect
          id="compiler"
          versions={versions}
          value={value}
          onChange={(version) => {
            onChange(version);
            setValue(version);
          }}
          disabled={disabled}
        />
        <button type="submit">Submit</button>
      </form>
    );
  }
  render(<Form />);
  return { user: userEvent.setup(), input: screen.getByRole("combobox"), onChange, onSubmit };
}

beforeEach(async () => {
  await i18n.changeLanguage("en");
});

describe("compiler version search", () => {
  it("starts a fresh search when typing or pasting after selection or Escape", async () => {
    const { user, input } = setup();
    await user.click(input);
    await user.keyboard("{ArrowDown}{Enter}");
    expect(input).toHaveValue(versions[1]);
    await user.keyboard("0.8.9");
    expect(input).toHaveValue("0.8.9");
    expect(screen.getAllByRole("option")).toHaveLength(1);
    await user.keyboard("{Escape}");
    expect(input).toHaveValue(versions[1]);
    await user.paste("0.8.30");
    expect(input).toHaveValue("0.8.30");
    expect(screen.getAllByRole("option")).toHaveLength(1);
  });

  it("filters full version text without changing the selection and clears the filter", async () => {
    const { user, input, onChange } = setup();
    await user.click(input);
    expect(
      within(screen.getByRole("listbox"))
        .getAllByRole("option")
        .map((item) => item.textContent),
    ).toEqual(versions);
    await user.type(input, "  COMMIT.abc  ");
    expect(screen.getAllByRole("option")).toHaveLength(1);
    expect(screen.getByRole("option")).toHaveTextContent(versions[1]!);
    expect(onChange).not.toHaveBeenCalled();
    await user.clear(input);
    expect(screen.getAllByRole("option")).toHaveLength(3);
    await user.click(screen.getByRole("option", { name: versions[2] }));
    expect(input).toHaveValue(versions[2]);
    expect(onChange).toHaveBeenCalledWith(versions[2]);
    expect(input).toHaveFocus();
    expect(input).toHaveAttribute("aria-expanded", "false");
  });

  it("supports arrows, Enter, Escape and Tab without submitting from the search", async () => {
    const { user, input, onSubmit, onChange } = setup();
    await user.click(input);
    await user.keyboard("{ArrowDown}{ArrowDown}{ArrowUp}");
    expect(document.getElementById(input.getAttribute("aria-activedescendant")!)).toHaveTextContent(
      versions[1]!,
    );
    await user.keyboard("{Enter}");
    expect(input).toHaveValue(versions[1]);
    expect(onSubmit).not.toHaveBeenCalled();
    await user.keyboard("{ArrowDown}");
    await user.type(input, "nonexistent");
    expect(screen.getByRole("status")).toHaveTextContent("No matching versions");
    expect(input).not.toHaveAttribute("aria-activedescendant");
    await user.keyboard("{Enter}{Escape}");
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(input).toHaveValue(versions[1]);
    expect(input).toHaveFocus();
    await user.click(input);
    expect(screen.getAllByRole("option")).toHaveLength(3);
    await user.tab();
    expect(screen.getByRole("button", { name: "Submit" })).toHaveFocus();
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("closes on outside focus and ignores composing Enter", async () => {
    const { user, input, onChange } = setup();
    await user.click(input);
    fireEvent.keyDown(input, { key: "Enter", isComposing: true });
    expect(onChange).not.toHaveBeenCalled();
    await user.type(input, "0.8.9");
    await user.click(screen.getByRole("button"));
    expect(input).toHaveValue(versions[0]);
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  });

  it("keeps unavailable catalogs disabled", async () => {
    const { user, input } = setup(true);
    await user.click(input);
    expect(input).toBeDisabled();
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  });

  it("localizes search and no-results text", async () => {
    await i18n.changeLanguage("zh");
    const { user, input } = setup();
    await user.click(input);
    expect(input).toHaveAttribute("placeholder", "搜索版本…");
    await user.type(input, "missing");
    expect(screen.getByRole("status")).toHaveTextContent("没有匹配的版本");
  });
});
