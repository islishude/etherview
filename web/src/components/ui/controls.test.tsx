import { createRef, useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { Button } from "./button";
import { Input } from "./input";
import { Label } from "./label";
import { Checkbox, Radio } from "./checkbox";
import { NativeSelect } from "./native-select";
import { Tabs, TabsList, TabsTrigger, NavigationTab, NavigationTabs } from "./tabs";

it("preserves native submission, labels, refs and successful form controls", async () => {
  const user = userEvent.setup();
  const submit = vi.fn((event: React.SubmitEvent<HTMLFormElement>) => event.preventDefault());
  const ref = createRef<HTMLInputElement>();
  render(
    <form aria-label="Settings" onSubmit={submit}>
      <Label htmlFor="name">Name</Label>
      <Input ref={ref} id="name" name="name" required />
      <Label>
        <Checkbox name="notify" defaultChecked />
        Notify
      </Label>
      <Label>
        <Radio name="scope" value="all" defaultChecked />
        All
      </Label>
      <Label>
        <NativeSelect name="chain" defaultValue="1">
          <option value="1">Ethereum</option>
        </NativeSelect>
        Chain
      </Label>
      <Button type="button">Preview</Button>
      <Button type="submit">Save</Button>
    </form>,
  );
  expect(ref.current).toBe(screen.getByLabelText("Name"));
  await user.click(screen.getByText("Name"));
  expect(ref.current).toHaveFocus();
  await user.click(screen.getByRole("button", { name: "Save" }));
  expect(submit).not.toHaveBeenCalled();
  await user.type(ref.current!, "Alice");
  await user.click(screen.getByRole("button", { name: "Preview" }));
  expect(submit).not.toHaveBeenCalled();
  await user.click(screen.getByRole("button", { name: "Save" }));
  expect(submit).toHaveBeenCalledOnce();
  const data = new FormData(screen.getByRole("form") as HTMLFormElement);
  expect(Object.fromEntries(data)).toEqual({
    name: "Alice",
    notify: "on",
    scope: "all",
    chain: "1",
  });
});

it("keeps multiple selects native without adding a decorative dropdown arrow", async () => {
  render(
    <NativeSelect aria-label="Networks" multiple size={3} defaultValue={["1"]}>
      <option value="1">Ethereum</option>
      <option value="2">Preview</option>
    </NativeSelect>,
  );
  const select = screen.getByRole("listbox", { name: "Networks" });
  await userEvent.setup().selectOptions(select, ["1", "2"]);
  expect(select).toHaveValue(["1", "2"]);
  expect(select.parentElement?.querySelector("svg")).toBeNull();
});

it("composes real links without nesting controls or overriding navigation", () => {
  render(
    <>
      <Button asChild variant="outline">
        <a href="/account">Account</a>
      </Button>
      <NavigationTabs aria-label="Address">
        <NavigationTab>
          <a href="?tab=assets" aria-current="page">
            Assets
          </a>
        </NavigationTab>
      </NavigationTabs>
    </>,
  );
  expect(screen.getByRole("link", { name: "Account" })).toHaveAttribute("href", "/account");
  expect(screen.queryByRole("button")).toBeNull();
  expect(screen.getByRole("link", { name: "Assets" })).toHaveAttribute("aria-current", "page");
});

it("moves tab focus and controlled selection while skipping disabled entries", async () => {
  function Example() {
    const [value, setValue] = useState("one");
    return (
      <Tabs value={value} onValueChange={setValue}>
        <TabsList aria-label="Sections">
          <TabsTrigger value="one" id="one-tab" aria-controls="one-panel">
            One
          </TabsTrigger>
          <TabsTrigger value="two" disabled>
            Two
          </TabsTrigger>
          <TabsTrigger value="three" id="three-tab" aria-controls="three-panel">
            Three
          </TabsTrigger>
        </TabsList>
        <section id={`${value}-panel`} aria-labelledby={`${value}-tab`} role="tabpanel">
          {value}
        </section>
      </Tabs>
    );
  }
  const user = userEvent.setup();
  render(<Example />);
  await user.tab();
  expect(screen.getByRole("tab", { name: "One" })).toHaveFocus();
  await user.keyboard("{ArrowRight}");
  expect(screen.getByRole("tab", { name: "Three" })).toHaveFocus();
  expect(screen.getByRole("tabpanel")).toHaveTextContent("three");
  await user.keyboard("{Home}");
  expect(screen.getByRole("tab", { name: "One" })).toHaveAttribute("aria-selected", "true");
});
