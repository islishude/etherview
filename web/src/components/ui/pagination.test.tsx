import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { Pagination } from "./pagination";
import "@/i18n";

it("hides empty pagination and permits a later empty page to restart", async () => {
  const reset = vi.fn();
  const props = {
    itemCount: 0,
    hasPrevious: false,
    hasNext: false,
    onReset: reset,
    "aria-label": "Rows",
  };
  const view = render(
    <Pagination {...props}>
      <button>Previous page</button>
      <button>Next page</button>
    </Pagination>,
  );
  expect(screen.queryByRole("navigation")).not.toBeInTheDocument();
  expect(screen.queryByRole("button")).not.toBeInTheDocument();
  view.rerender(
    <Pagination {...props} hasPrevious>
      <button>Previous page</button>
      <button>Next page</button>
    </Pagination>,
  );
  expect(screen.queryByRole("navigation")).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Next page" })).not.toBeInTheDocument();
  await userEvent
    .setup()
    .click(screen.getByRole("button", { name: "Restart from the first page" }));
  expect(reset).toHaveBeenCalledOnce();
  view.rerender(
    <Pagination {...props} itemCount={1} hasNext busy>
      <button>Previous page</button>
      <button>Next page</button>
    </Pagination>,
  );
  expect(screen.getByRole("navigation", { name: "Rows" })).toBeVisible();
});

it("hides the entire navigation for a single page", () => {
  render(
    <Pagination itemCount={1} hasPrevious={false} hasNext={false} onReset={() => {}}>
      <span>Page 1</span>
    </Pagination>,
  );
  expect(screen.queryByRole("navigation")).not.toBeInTheDocument();
  expect(screen.queryByText("Page 1")).not.toBeInTheDocument();
});
