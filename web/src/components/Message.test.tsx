import { StrictMode, type ReactElement } from "react";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { Message } from "./Message";

beforeEach(async () => {
  await import("./MessageView");
  vi.useFakeTimers();
});
async function renderMessage(element: ReactElement) {
  let view!: ReturnType<typeof render>;
  await act(async () => {
    view = render(element);
  });
  return view;
}
afterEach(() => vi.useRealTimers());

it("stacks distinct messages, deduplicates observers and removes timers/hosts on teardown", async () => {
  const view = await renderMessage(
    <StrictMode>
      <Message type="success">Saved</Message>
      <Message type="error">Failed</Message>
      <Message type="error">Failed</Message>
    </StrictMode>,
  );
  expect(screen.getAllByRole("alert")).toHaveLength(1);
  expect(screen.getByRole("status")).toHaveTextContent("Saved");
  expect(document.querySelectorAll(".message-stack")).toHaveLength(1);
  expect(document.querySelectorAll(".message")).toHaveLength(2);
  act(() => vi.advanceTimersByTime(2999));
  expect(screen.getByRole("alert")).toBeInTheDocument();
  act(() => vi.advanceTimersByTime(1));
  expect(document.querySelector(".message-stack")).toBeNull();
  view.unmount();
  expect(vi.getTimerCount()).toBe(0);
  await renderMessage(<Message type="error">Failed</Message>);
  expect(screen.getByRole("alert")).toBeInTheDocument();
});

it("pauses on hover and focus, closes without submitting and returns keyboard focus", async () => {
  const submit = vi.fn();
  await renderMessage(
    <form onSubmit={submit}>
      <button type="button">Trigger</button>
      <Message type="success">Saved</Message>
    </form>,
  );
  const trigger = screen.getByRole("button", { name: "Trigger" });
  trigger.focus();
  expect(trigger).toHaveFocus();
  const item = document.querySelector(".message")!;
  fireEvent.mouseEnter(item);
  act(() => vi.advanceTimersByTime(5000));
  expect(screen.getByRole("status")).toBeInTheDocument();
  fireEvent.mouseLeave(item);
  const close = screen.getByRole("button", { name: "Close" });
  act(() => close.focus());
  act(() => vi.advanceTimersByTime(5000));
  expect(close).toHaveFocus();
  fireEvent.click(close);
  expect(trigger).toHaveFocus();
  expect(submit).not.toHaveBeenCalled();
  expect(document.querySelector(".message-stack")).toBeNull();
});

it("keeps modal feedback in the dialog's accessible top layer and clears it with its owner", async () => {
  const view = await renderMessage(
    <dialog open>
      <Message type="error">Login failed</Message>
    </dialog>,
  );
  expect(screen.getByRole("dialog")).toContainElement(screen.getByRole("alert"));
  view.unmount();
  expect(document.querySelector(".message-stack")).toBeNull();
  expect(vi.getTimerCount()).toBe(0);
});

it("cleans up a paused shell message when its modal host closes", async () => {
  const dialog = document.createElement("dialog");
  dialog.open = true;
  document.body.appendChild(dialog);
  const view = await renderMessage(<Message type="error">Login failed</Message>);
  expect(dialog).toContainElement(screen.getByRole("alert"));
  fireEvent.mouseEnter(dialog.querySelector(".message")!);
  await act(async () => {
    dialog.remove();
  });
  expect(vi.getTimerCount()).toBe(0);
  view.unmount();
  await renderMessage(<Message type="error">Login failed</Message>);
  expect(screen.getByRole("alert")).toBeInTheDocument();
});

it("replaces feedback on a new outcome and translates the close control", async () => {
  await i18n.changeLanguage("zh");
  const view = await renderMessage(<Message type="error">失败</Message>);
  expect(screen.getByRole("button", { name: "关闭" })).toBeInTheDocument();
  act(() => vi.advanceTimersByTime(2000));
  view.rerender(<Message type="success">成功</Message>);
  act(() => vi.advanceTimersByTime(2000));
  expect(screen.queryByRole("alert")).toBeNull();
  expect(screen.getByRole("status")).toHaveTextContent("成功");
  act(() => vi.advanceTimersByTime(1000));
  expect(screen.queryByRole("status")).toBeNull();
  await i18n.changeLanguage("en");
});
