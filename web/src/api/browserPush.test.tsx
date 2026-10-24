import pushWorkerURL from "@/workers/push-sw.ts?worker&url";
import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useAuth } from "@/auth/AuthProvider";
import { getAuthSession, type AuthSession } from "./auth";
import { listPushSubscriptions } from "./watchlist";
import { BrowserPushLifetime, endpointHash } from "./browserPush";

vi.mock("@/auth/AuthProvider", () => ({ useAuth: vi.fn() }));
vi.mock("./auth", () => ({ getAuthSession: vi.fn() }));
vi.mock("./watchlist", () => ({ listPushSubscriptions: vi.fn() }));

const endpoint = "https://push.example/shared";
const oldSession = {
  authenticated: true,
  csrf_token: "old-session",
  expires_at: "2030-01-01T00:00:01Z",
  user: { id: "alice" },
} as AuthSession;
const newSession = {
  ...oldSession,
  csrf_token: "new-session",
  expires_at: "2030-01-02T00:00:00Z",
};

function tabSession(session: AuthSession) {
  vi.mocked(useAuth).mockReturnValue({ session } as ReturnType<typeof useAuth>);
}

describe("browser push session cleanup", () => {
  const unsubscribe = vi.fn().mockResolvedValue(true);
  const close = vi.fn();
  let locked: boolean;
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2030-01-01T00:00:00Z"));
    locked = false;
    tabSession(oldSession);
    vi.mocked(getAuthSession).mockImplementation(async () => {
      expect(locked).toBe(true);
      return newSession;
    });
    vi.stubGlobal("navigator", {
      locks: {
        request: vi.fn(async (_name: string, operation: () => Promise<void>) => {
          locked = true;
          try {
            await operation();
          } finally {
            locked = false;
          }
        }),
      },
      serviceWorker: {
        getRegistration: vi.fn().mockResolvedValue({
          active: { scriptURL: new URL(pushWorkerURL, location.href).href },
          pushManager: { getSubscription: vi.fn().mockResolvedValue({ endpoint, unsubscribe }) },
          getNotifications: vi.fn().mockResolvedValue([{ close }]),
        }),
      },
    });
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.resetAllMocks();
    unsubscribe.mockResolvedValue(true);
  });

  async function newerBinding() {
    const hash = await endpointHash(endpoint);
    vi.mocked(listPushSubscriptions).mockImplementation(async () => {
      expect(locked).toBe(true);
      return {
        enabled: true,
        public_key: "public",
        items: [
          { id: "new", endpoint_hash: hash, language: "en", expires_at: newSession.expires_at! },
        ],
      };
    });
  }

  it("keeps a newer tab's binding when the old tab's session expires", async () => {
    await newerBinding();
    render(<BrowserPushLifetime />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    // Flush the digest used to compare the current endpoint inside the lock.
    await act(async () => {
      await vi.dynamicImportSettled();
      await vi.waitUntil(() => !locked);
    });
    expect(unsubscribe).not.toHaveBeenCalled();
    expect(close).not.toHaveBeenCalled();
    expect(listPushSubscriptions).toHaveBeenCalledOnce();
  });

  it("keeps a newer binding when an old tab clears its account state", async () => {
    await newerBinding();
    const view = render(<BrowserPushLifetime />);
    tabSession({ authenticated: false });
    await act(async () => view.rerender(<BrowserPushLifetime />));
    await act(async () => {
      await vi.dynamicImportSettled();
      await vi.waitUntil(() => !locked);
    });
    expect(unsubscribe).not.toHaveBeenCalled();
    expect(close).not.toHaveBeenCalled();
  });

  it("still cleans up after the current session expires", async () => {
    vi.mocked(getAuthSession).mockResolvedValue({ authenticated: false });
    render(<BrowserPushLifetime />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(999);
    });
    expect(unsubscribe).not.toHaveBeenCalled();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
      await vi.dynamicImportSettled();
    });
    expect(unsubscribe).toHaveBeenCalledOnce();
    expect(close).toHaveBeenCalledOnce();
  });

  it("removes the old binding on account change if the new session has not enabled it", async () => {
    vi.mocked(listPushSubscriptions).mockResolvedValue({
      enabled: true,
      public_key: "public",
      items: [],
    });
    const view = render(<BrowserPushLifetime />);
    const otherAccount = { ...newSession, user: { ...oldSession.user!, id: "bob" } };
    vi.mocked(getAuthSession).mockResolvedValue(otherAccount);
    tabSession(otherAccount);
    await act(async () => view.rerender(<BrowserPushLifetime />));
    await act(async () => {
      await vi.dynamicImportSettled();
      await vi.waitUntil(() => !locked);
    });
    expect(unsubscribe).toHaveBeenCalledOnce();
    expect(close).toHaveBeenCalledOnce();
  });

  it("does not apply an old expiry result to a newly restored session", async () => {
    await newerBinding();
    const view = render(<BrowserPushLifetime />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    await act(async () => {
      await vi.dynamicImportSettled();
      await vi.waitUntil(() => !locked);
    });
    tabSession(newSession);
    await act(async () => view.rerender(<BrowserPushLifetime />));
    await act(async () => {
      await vi.dynamicImportSettled();
      await vi.waitUntil(() => !locked);
    });
    expect(unsubscribe).not.toHaveBeenCalled();
    expect(close).not.toHaveBeenCalled();
  });

  it("does not cancel an unknown binding when the ownership check fails", async () => {
    vi.mocked(listPushSubscriptions).mockRejectedValue(new Error("offline"));
    render(<BrowserPushLifetime />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    await act(async () => {
      await vi.dynamicImportSettled();
      await vi.waitUntil(() => !locked);
    });
    expect(unsubscribe).not.toHaveBeenCalled();
    expect(close).not.toHaveBeenCalled();
  });
});
