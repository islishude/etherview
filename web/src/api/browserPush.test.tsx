import { act, render } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { BrowserPushLifetime } from "./browserPush";

const session = vi.hoisted(() => ({
  authenticated: true,
  user: { id: "alice" },
  expires_at: "2099-01-01T00:00:05Z",
}));
vi.mock("@/auth/AuthProvider", () => ({ useAuth: () => ({ session }) }));
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  session.user.id = "alice";
  session.expires_at = "2099-01-01T00:00:05Z";
});
function mockBrowser() {
  const unsubscribe = vi.fn().mockResolvedValue(true);
  const close = vi.fn();
  vi.stubGlobal("navigator", {
    serviceWorker: {
      getRegistration: vi.fn().mockResolvedValue({
        active: { scriptURL: "https://explorer.example/push-sw.js" },
        pushManager: { getSubscription: vi.fn().mockResolvedValue({ unsubscribe }) },
        getNotifications: vi.fn().mockResolvedValue([{ close }]),
      }),
    },
  });
  return { unsubscribe, close };
}
it("unsubscribes and closes displayed alerts when the session expires", async () => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2099-01-01T00:00:00Z"));
  const { unsubscribe, close } = mockBrowser();
  render(<BrowserPushLifetime />);
  await act(() => vi.advanceTimersByTimeAsync(4999));
  expect(unsubscribe).not.toHaveBeenCalled();
  await act(() => vi.advanceTimersByTimeAsync(1));
  expect(unsubscribe).toHaveBeenCalledOnce();
  expect(close).toHaveBeenCalledOnce();
});
it("clears the old browser binding when switching accounts", async () => {
  const { unsubscribe, close } = mockBrowser();
  const view = render(<BrowserPushLifetime />);
  session.user.id = "bob";
  await act(async () => {
    view.rerender(<BrowserPushLifetime />);
  });
  expect(unsubscribe).toHaveBeenCalledOnce();
  expect(close).toHaveBeenCalledOnce();
});
