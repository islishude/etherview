import pushWorkerURL from "@/workers/push-sw.ts?worker&url";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { BrowserPushPanel } from "./BrowserPushPanel";
import {
  listPushSubscriptions,
  savePushSubscription,
  deletePushSubscription,
} from "@/api/watchlist";
import { endpointHash } from "@/api/browserPush";

vi.mock("@/auth/AuthProvider", () => ({
  useAuth: () => ({
    session: {
      authenticated: true,
      user: { id: "alice" },
      csrf_token: "csrf",
      expires_at: "2099-01-01",
    },
  }),
}));
vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key, i18n: { language: "en" } }),
}));
vi.mock("@/api/auth", () => ({
  getAuthSession: vi.fn().mockResolvedValue({ authenticated: true, csrf_token: "csrf" }),
}));
vi.mock("@/api/watchlist", () => ({
  listPushSubscriptions: vi.fn(),
  savePushSubscription: vi.fn(),
  deletePushSubscription: vi.fn(),
}));
afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});
function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <BrowserPushPanel />
    </QueryClientProvider>,
  );
}
describe("browser notification permission", () => {
  it("does not cancel another tab's subscription when rebinding fails", async () => {
    const unsubscribe = vi.fn();
    const registration = {
      pushManager: {
        getSubscription: vi.fn().mockResolvedValue({
          endpoint: "https://push.example/shared",
          toJSON: () => ({ keys: { p256dh: "key", auth: "auth" } }),
          unsubscribe,
        }),
      },
    };
    vi.stubGlobal("isSecureContext", true);
    vi.stubGlobal("Notification", {
      permission: "granted",
      requestPermission: vi.fn().mockResolvedValue("granted"),
    });
    vi.stubGlobal("PushManager", class {});
    vi.stubGlobal("navigator", {
      serviceWorker: {
        getRegistration: vi.fn().mockResolvedValue(undefined),
        register: vi.fn().mockResolvedValue(registration),
        ready: Promise.resolve(registration),
      },
    });
    vi.mocked(listPushSubscriptions).mockResolvedValue({
      enabled: true,
      public_key: "AQ",
      items: [],
    });
    vi.mocked(savePushSubscription).mockRejectedValue(new Error("session changed in another tab"));
    mount();
    await waitFor(() => expect(screen.getByRole("button")).not.toBeDisabled());
    expect(screen.getByText("browserPush.allowed")).toBeVisible();
    expect(screen.getByRole("button", { name: "browserPush.enable" })).toBeVisible();
    fireEvent.click(screen.getByRole("button"));
    await screen.findByRole("alert");
    expect(Notification.requestPermission).not.toHaveBeenCalled();
    expect(unsubscribe).not.toHaveBeenCalled();
  });
  it("refreshes permission changes and browser settings changes without prompting", async () => {
    const permissionStatus = new EventTarget();
    const permissionsQuery = vi.fn().mockResolvedValue(permissionStatus);
    const notification = { permission: "denied", requestPermission: vi.fn() };
    vi.stubGlobal("isSecureContext", true);
    vi.stubGlobal("Notification", notification);
    vi.stubGlobal("PushManager", class {});
    vi.stubGlobal("navigator", {
      permissions: { query: permissionsQuery },
      serviceWorker: { getRegistration: vi.fn().mockResolvedValue(undefined) },
    });
    vi.mocked(listPushSubscriptions).mockResolvedValue({
      enabled: true,
      public_key: "AQ",
      items: [],
    });
    const { unmount } = mount();
    await screen.findByText("browserPush.denied");
    expect(screen.getByRole("button")).toBeDisabled();
    await waitFor(() => expect(permissionsQuery).toHaveBeenCalledWith({ name: "notifications" }));
    notification.permission = "granted";
    act(() => permissionStatus.dispatchEvent(new Event("change")));
    await screen.findByText("browserPush.allowed");
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "browserPush.enable" })).toBeEnabled(),
    );
    notification.permission = "denied";
    fireEvent.focus(window);
    await screen.findByText("browserPush.denied");
    expect(screen.getByRole("button")).toBeDisabled();
    expect(notification.requestPermission).not.toHaveBeenCalled();
    unmount();
  });
  it("reuses a subscription enabled by another tab after an older query", async () => {
    const endpoint = "https://push.example/browser";
    const unsubscribe = vi.fn();
    vi.stubGlobal("isSecureContext", true);
    vi.stubGlobal("Notification", { permission: "granted", requestPermission: vi.fn() });
    vi.stubGlobal("PushManager", class {});
    vi.stubGlobal("navigator", {
      serviceWorker: {
        getRegistration: vi.fn().mockResolvedValue({
          pushManager: { getSubscription: vi.fn().mockResolvedValue({ endpoint, unsubscribe }) },
        }),
      },
    });
    const enabled = { enabled: true, public_key: "", items: [] };
    vi.mocked(listPushSubscriptions)
      .mockResolvedValueOnce(enabled)
      .mockResolvedValue({
        ...enabled,
        items: [
          {
            id: "one",
            endpoint_hash: await endpointHash(endpoint),
            language: "en",
            expires_at: "2099-01-01",
          },
        ],
      });
    mount();
    await screen.findByText("browserPush.enabled");
    expect(screen.getByRole("button", { name: "browserPush.disable" })).toBeVisible();
    expect(screen.queryByRole("button", { name: "browserPush.enable" })).not.toBeInTheDocument();
    expect(unsubscribe).not.toHaveBeenCalled();
    expect(savePushSubscription).not.toHaveBeenCalled();
  });
  it("enables explicitly and disables the subscription and displayed notifications", async () => {
    const endpoint = "https://push.example/browser";
    const hash = await endpointHash(endpoint);
    const item = { id: "one", endpoint_hash: hash, language: "en", expires_at: "2099-01-01" };
    let subscribed = false;
    let saved = false;
    const close = vi.fn();
    const subscription = {
      endpoint,
      toJSON: () => ({ keys: { p256dh: "browser-key", auth: "browser-auth" } }),
      unsubscribe: vi.fn().mockImplementation(async () => {
        subscribed = false;
        return true;
      }),
    };
    const registration = {
      active: { scriptURL: new URL(pushWorkerURL, location.href).href },
      getNotifications: vi.fn().mockResolvedValue([{ close }]),
      pushManager: {
        getSubscription: vi.fn().mockImplementation(async () => (subscribed ? subscription : null)),
        subscribe: vi.fn().mockImplementation(async () => {
          subscribed = true;
          return subscription;
        }),
      },
    };
    const register = vi.fn().mockResolvedValue(registration);
    const requestPermission = vi.fn().mockResolvedValue("granted");
    vi.stubGlobal("isSecureContext", true);
    vi.stubGlobal("Notification", { permission: "default", requestPermission });
    vi.stubGlobal("PushManager", class {});
    vi.stubGlobal("navigator", {
      serviceWorker: {
        getRegistration: vi.fn().mockResolvedValue(registration),
        register,
        ready: Promise.resolve(registration),
      },
    });
    vi.mocked(listPushSubscriptions).mockImplementation(async () => ({
      enabled: true,
      public_key: "AQ",
      items: saved ? [item] : [],
    }));
    vi.mocked(savePushSubscription).mockImplementation(async () => {
      saved = true;
      return item;
    });
    vi.mocked(deletePushSubscription).mockImplementation(async () => {
      saved = false;
    });
    mount();
    await waitFor(() => expect(screen.getByRole("button")).not.toBeDisabled());
    expect(requestPermission).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button"));
    await screen.findByText("browserPush.enabled");
    expect(register).toHaveBeenCalledWith(pushWorkerURL, {
      type: "module",
      scope: "/",
      updateViaCache: "none",
    });
    expect(savePushSubscription).toHaveBeenCalledWith(
      { endpoint, p256dh: "browser-key", auth: "browser-auth", language: "en" },
      "csrf",
      expect.any(AbortSignal),
    );
    await waitFor(() => expect(screen.getByRole("button")).not.toBeDisabled());
    fireEvent.click(screen.getByRole("button"));
    await waitFor(() => expect(close).toHaveBeenCalledOnce());
    expect(deletePushSubscription).toHaveBeenCalledWith("one", "csrf", expect.any(AbortSignal));
    expect(subscription.unsubscribe).toHaveBeenCalledOnce();
  });
  it("does not request permission on render and shows unsupported state", async () => {
    vi.stubGlobal("isSecureContext", false);
    vi.mocked(listPushSubscriptions).mockResolvedValue({
      enabled: true,
      public_key: "",
      items: [],
    });
    mount();
    expect(screen.getByText("browserPush.unsupported")).toBeVisible();
    expect(screen.getByRole("button")).toBeDisabled();
  });
  it("asks only on a click and never registers after denial", async () => {
    const requestPermission = vi.fn().mockResolvedValue("denied");
    vi.stubGlobal("isSecureContext", true);
    vi.stubGlobal("Notification", { permission: "default", requestPermission });
    vi.stubGlobal("PushManager", class {});
    vi.stubGlobal("navigator", {
      serviceWorker: { getRegistration: vi.fn().mockResolvedValue(undefined) },
    });
    vi.mocked(listPushSubscriptions).mockResolvedValue({
      enabled: true,
      public_key: "",
      items: [],
    });
    mount();
    await waitFor(() => expect(screen.getByRole("button")).not.toBeDisabled());
    expect(requestPermission).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button"));
    await screen.findByText("browserPush.denied");
    expect(requestPermission).toHaveBeenCalledTimes(1);
    expect(savePushSubscription).not.toHaveBeenCalled();
  });
});
