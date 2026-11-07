import { useState } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { AlertDialog } from "./alert-dialog";

it("focuses cancellation, traps Tab, ignores backdrop clicks and restores focus on Escape", async () => {
  const confirm = vi.fn();
  function Example() {
    const [open, setOpen] = useState(false);
    return (
      <>
        <button onClick={() => setOpen(true)}>Open</button>
        {open && (
          <AlertDialog
            title="Delete key?"
            description="This cannot be undone."
            cancelLabel="Cancel"
            confirmLabel="Delete"
            onClose={() => setOpen(false)}
            onConfirm={confirm}
          />
        )}
      </>
    );
  }
  render(<Example />);
  const user = userEvent.setup();
  const trigger = screen.getByRole("button", { name: "Open" });
  await user.click(trigger);
  const dialog = screen.getByRole("alertdialog", { name: "Delete key?" });
  expect(dialog).toHaveAccessibleDescription("This cannot be undone.");
  expect(screen.getByRole("button", { name: "Cancel" })).toHaveFocus();
  await user.tab({ shift: true });
  expect(screen.getByRole("button", { name: "Delete" })).toHaveFocus();
  await user.tab();
  expect(screen.getByRole("button", { name: "Cancel" })).toHaveFocus();
  fireEvent.click(dialog);
  expect(dialog).toBeVisible();
  await user.keyboard("{Escape}");
  expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  expect(trigger).toHaveFocus();
  expect(confirm).not.toHaveBeenCalled();
  await user.click(trigger);
  await user.click(screen.getByRole("button", { name: "Cancel" }));
  expect(confirm).not.toHaveBeenCalled();
  await user.click(trigger);
  await user.click(screen.getByRole("button", { name: "Delete" }));
  expect(confirm).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
});
