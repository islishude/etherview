import { createContext, useContext } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { PrivateAccountCleanup } from "@/api/privateAccount";
import { WalletProvider } from "@/wallet/WalletProvider";
import i18n from "@/i18n";
import { AddressExportPage } from "./AddressActionPages";
import { AddressWatchButton } from "@/components/AddressAccountActions";

const auth = vi.hoisted(() => ({
  enabled: true,
  loading: false,
  pending: false,
  session: { authenticated: false } as Record<string, unknown>,
  refresh: vi.fn(),
  login: vi.fn(),
  error: undefined,
}));
vi.mock("@/auth/AuthProvider", async (original) => ({
  ...(await original<object>()),
  useAuth: () => {
    useContext(AuthRevisionContext);
    return auth;
  },
}));
const AuthRevisionContext = createContext(0);
const address = `0x${"1".repeat(40)}`;
const envelope = (data: unknown) =>
  Response.json({ data, meta: { chain_id: "1", request_id: "actions" } });
const watch = {
  id: "watch-1",
  address,
  label: "",
  kinds: ["transaction"],
  direction: "both",
  enabled: true,
  start_number: "10",
  start_hash: `0x${"a".repeat(64)}`,
};
function session(id = "user-1") {
  return {
    authenticated: true,
    user: { id },
    csrf_token: "c".repeat(43),
    expires_at: "2099-01-01T00:00:00Z",
  };
}
function setup(
  kind: "watch" | "export",
  respond: (path: string, init?: RequestInit) => Promise<Response> = async () => envelope([]),
) {
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/api/v1/config")
      return envelope({ chain_id: "1", features: { user_auth: true } });
    return respond(path, init);
  });
  vi.stubGlobal("fetch", fetcher);
  const Page = kind === "watch" ? AddressWatchButton : AddressExportPage;
  // A parent revision renders the same route instance with the new auth context.
  let revision = 0;
  const route = createRootRoute({ component: () => <Page address={address} /> });
  const router = createRouter({
    routeTree: route,
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const tree = () => (
    <QueryClientProvider client={client}>
      <AuthRevisionContext.Provider value={revision}>
        <WalletProvider>
          <PrivateAccountCleanup />
          <RouterProvider router={router} context={{ revision }} />
        </WalletProvider>
      </AuthRevisionContext.Provider>
    </QueryClientProvider>
  );
  const view = render(tree());
  return {
    ...view,
    client,
    fetcher,
    refresh: async () => {
      revision++;
      view.rerender(tree());
      await act(async () => {
        await router.invalidate();
      });
    },
  };
}
beforeEach(async () => {
  await i18n.changeLanguage("en");
  auth.enabled = true;
  auth.loading = false;
  auth.pending = false;
  auth.error = undefined;
  Object.defineProperty(HTMLDialogElement.prototype, "showModal", {
    configurable: true,
    value: function (this: HTMLDialogElement) {
      this.setAttribute("open", "");
    },
  });
  Object.defineProperty(HTMLDialogElement.prototype, "close", {
    configurable: true,
    value: function (this: HTMLDialogElement) {
      this.removeAttribute("open");
    },
  });
  auth.session = session();
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
  vi.stubGlobal(
    "URL",
    Object.assign(URL, { createObjectURL: vi.fn(() => "blob:test"), revokeObjectURL: vi.fn() }),
  );
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

it("requires retry after a failed watch read and explicitly confirms add and remove", async () => {
  let fail = true;
  let watching = false;
  const mutations: string[] = [];
  setup("watch", async (_path, init) => {
    if (init?.method === "GET")
      return fail
        ? Response.json(
            { error: { code: "activity_unavailable", message: "Unavailable", request_id: "test" } },
            { status: 503 },
          )
        : envelope(watching ? [watch] : []);
    mutations.push(init!.method!);
    if (init?.method === "DELETE") {
      watching = false;
      return new Response(null, { status: 204 });
    }
    expect(JSON.parse(String(init?.body))).toEqual({
      address,
      label: "",
      kinds: ["transaction", "erc20", "erc721", "erc1155"],
      direction: "both",
      enabled: true,
    });
    watching = true;
    return envelope(watch);
  });
  await screen.findByRole("alert");
  expect(screen.getByRole("button", { name: "Watch status unavailable" })).toHaveAttribute(
    "aria-disabled",
    "true",
  );
  fail = false;
  await userEvent.click(screen.getByRole("button", { name: "Retry" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Watch address" })).toBeEnabled());
  expect(mutations).toEqual([]);
  await userEvent.click(screen.getByRole("button", { name: "Watch address" }));
  const remove = await screen.findByRole("button", { name: "Remove watch" });
  expect(remove).toHaveAttribute("aria-pressed", "true");
  expect(remove.querySelector(".lucide-bell-ring")).not.toBeNull();
  expect(await screen.findByRole("status")).toHaveTextContent("Address watched.");
  expect(document.querySelector(".address-watch-feedback")).toBeNull();
  await userEvent.click(remove);
  const add = await screen.findByRole("button", { name: "Watch address" });
  expect(add).toHaveAttribute("aria-pressed", "false");
  expect(add.querySelector(".lucide-bell")).not.toBeNull();
  expect(await screen.findByRole("status")).toHaveTextContent("Address removed from watchlist.");
  expect(document.querySelector(".address-watch-feedback")).toBeNull();
  expect(mutations).toEqual(["POST", "DELETE"]);
});

it("blocks a sixth watch while allowing removal at the limit", async () => {
  let watches = Array.from({ length: 5 }, (_, i) => ({
    ...watch,
    id: `watch-${i}`,
    address: `0x${String(i + 2).repeat(40)}`,
  }));
  const view = setup("watch", async () => envelope(watches));
  await screen.findByText("You can watch at most 5 addresses.");
  expect(screen.getByRole("button", { name: "Watch address" })).toBeDisabled();
  watches = [watch, ...watches.slice(1)];
  await act(async () => {
    await view.client.invalidateQueries();
  });
  expect(await screen.findByRole("button", { name: "Remove watch" })).toBeEnabled();
});

it("cancels a pending watch on account change and never displays the old result", async () => {
  let signal: AbortSignal | null | undefined;
  let finish!: () => void;
  const view = setup("watch", async (_path, init) => {
    if (init?.method === "GET") return envelope([]);
    signal = init?.signal;
    await new Promise<void>((resolve) => {
      finish = resolve;
    });
    return envelope(watch);
  });
  await waitFor(() => expect(screen.getByRole("button", { name: "Watch address" })).toBeEnabled());
  await userEvent.click(screen.getByRole("button", { name: "Watch address" }));
  await waitFor(() => expect(signal).toBeDefined());
  auth.session = session("user-2");
  await view.refresh();
  await waitFor(() => expect(signal!.aborted).toBe(true));
  await act(async () => {
    finish();
  });
  expect(screen.queryByText("Address added to your watchlist.")).toBeNull();
  await waitFor(() => expect(screen.getByRole("button", { name: "Watch address" })).toBeEnabled());
});

it("preserves export choices on failure, downloads explicitly, and clears on logout", async () => {
  const requests: Record<string, unknown>[] = [];
  const view = setup("export", async (_path, init) => {
    requests.push(JSON.parse(String(init?.body)));
    expect(new Headers(init?.headers).get("X-CSRF-Token")).toBe("c".repeat(43));
    return requests.length === 1
      ? Response.json(
          { error: { code: "export_limit", message: "Limit", request_id: "test" } },
          { status: 400 },
        )
      : new Response("amount\r\n123\r\n", { headers: { "Content-Type": "text/csv" } });
  });
  await waitFor(() => expect(screen.getByRole("button", { name: "Download CSV" })).toBeEnabled());
  expect(requests).toEqual([]);
  const from = screen.getByLabelText("From (UTC, inclusive)");
  const to = screen.getByLabelText("To (UTC, exclusive)");
  const delta =
    Date.parse(`${(to as HTMLInputElement).value}:00Z`) -
    Date.parse(`${(from as HTMLInputElement).value}:00Z`);
  expect(delta).toBe(7 * 86400000);
  expect(screen.getByLabelText("Activity types")).toHaveValue("all");
  expect(screen.getByLabelText("Direction")).toHaveValue("both");
  fireEvent.change(from, { target: { value: "2026-10-01T00:00" } });
  fireEvent.change(to, { target: { value: "2026-10-02T00:00" } });
  await userEvent.selectOptions(screen.getByLabelText("Activity types"), "nft");
  await userEvent.selectOptions(screen.getByLabelText("Direction"), "out");
  await userEvent.click(screen.getByRole("button", { name: "Download CSV" }));
  await screen.findByText("The export exceeds its limits. Select a smaller date range.");
  expect(from).toHaveValue("2026-10-01T00:00");
  await userEvent.click(screen.getByRole("button", { name: "Download CSV" }));
  await screen.findByText("CSV download started.");
  expect(requests).toEqual(
    Array(2).fill({
      address,
      kind: "nft",
      direction: "out",
      from: "2026-10-01T00:00:00Z",
      to: "2026-10-02T00:00:00Z",
    }),
  );
  auth.session = { authenticated: false };
  await view.refresh();
  await screen.findByText("Sign in to export address activity.");
  expect(screen.queryByText("CSV download started.")).toBeNull();
  expect(screen.queryByRole("button", { name: "Download CSV" })).toBeNull();
});

it.each(["export"] as const)(
  "shows disabled authentication on the %s page without private requests",
  async (kind) => {
    auth.enabled = false;
    auth.session = { authenticated: false };
    const view = setup(kind);
    await screen.findByText("Sign-in is unavailable on this deployment.");
    expect(view.fetcher.mock.calls.every(([path]) => String(path) === "/api/v1/config")).toBe(true);
    expect(screen.queryByRole("button", { name: "Sign in" })).toBeNull();
  },
);

it("closes the login dialog after authentication without automatically watching", async () => {
  auth.session = { authenticated: false };
  const mutations: string[] = [];
  const view = setup("watch", async (_path, init) => {
    if (init?.method === "GET") return envelope([]);
    mutations.push(init!.method!);
    return envelope(watch);
  });
  const trigger = await screen.findByRole("button", { name: "Watch address" });
  await userEvent.click(trigger);
  expect(await screen.findByRole("dialog")).toBeVisible();
  expect(screen.getByRole("button", { name: "Close" })).toBeVisible();
  expect(
    screen.getByRole("button", { name: "Sign in to manage this address watch." }),
  ).toBeVisible();
  expect(mutations).toEqual([]);
  auth.session = session();
  await view.refresh();
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(mutations).toEqual([]);
  await waitFor(() => expect(screen.getByRole("button", { name: "Watch address" })).toHaveFocus());
  await userEvent.click(screen.getByRole("button", { name: "Watch address" }));
  expect(mutations).toEqual(["POST"]);
});

it.each([false, true])(
  "shows loading until completion and blocks double clicks (watching=%s)",
  async (initiallyWatching) => {
    let resolveSave!: () => void;
    let resolveRefresh!: () => void;
    let reads = 0;
    let writes = 0;
    let watching = initiallyWatching;
    setup("watch", async (_path, init) => {
      if (init?.method === "GET") {
        if (++reads > 1)
          await new Promise<void>((resolve) => {
            resolveRefresh = resolve;
          });
        return envelope(watching ? [watch] : []);
      }
      writes++;
      await new Promise<void>((resolve) => {
        resolveSave = resolve;
      });
      watching = !initiallyWatching;
      return watching ? envelope(watch) : new Response(null, { status: 204 });
    });
    const trigger = await screen.findByRole("button", {
      name: initiallyWatching ? "Remove watch" : "Watch address",
    });
    await userEvent.dblClick(trigger);
    expect(writes).toBe(1);
    expect(trigger).toBeDisabled();
    expect(trigger).toHaveAttribute("aria-busy", "true");
    expect(trigger.querySelector(".lucide-loader-circle.query-loading-spinner")).not.toBeNull();
    await act(async () => {
      resolveSave();
    });
    await waitFor(() => expect(resolveRefresh).toBeDefined());
    expect(trigger.querySelector(".lucide-loader-circle")).not.toBeNull();
    expect(trigger.querySelector(".lucide-bell-ring")).toBeNull();
    expect(trigger).toBeDisabled();
    await act(async () => {
      resolveRefresh();
    });
    await waitFor(() =>
      expect(trigger).toHaveAccessibleName(initiallyWatching ? "Watch address" : "Remove watch"),
    );
    expect(trigger).toBeEnabled();
    expect(trigger).toHaveAttribute("aria-busy", "false");
    expect(trigger.querySelector(".lucide-loader-circle")).toBeNull();
    expect(
      trigger.querySelector(initiallyWatching ? ".lucide-bell" : ".lucide-bell-ring"),
    ).not.toBeNull();
    expect(document.querySelector(".address-watch-feedback")).toBeNull();
  },
);

it("downloads all activity types by default using the all filename", async () => {
  let body: Record<string, unknown> | undefined;
  setup("export", async (_path, init) => {
    body = JSON.parse(String(init?.body));
    return new Response("kind\r\ntransaction\r\nerc20\r\nerc721\r\nerc1155\r\n", {
      headers: { "Content-Type": "text/csv" },
    });
  });
  await userEvent.click(await screen.findByRole("button", { name: "Download CSV" }));
  await screen.findByText("CSV download started.");
  expect(body?.kind).toBe("all");
  const anchor = vi
    .mocked(HTMLAnchorElement.prototype.click)
    .mock.instances.at(-1) as HTMLAnchorElement;
  expect(anchor.download).toMatch(/-all\.csv$/);
});

it("hides the watch button when authentication is disabled", async () => {
  auth.enabled = false;
  auth.session = { authenticated: false };
  const view = setup("watch");
  await act(async () => {});
  expect(screen.queryByRole("button", { name: "Watch address" })).toBeNull();
  expect(view.fetcher.mock.calls.every(([path]) => String(path) === "/api/v1/config")).toBe(true);
});

it("preserves the confirmed icon on failure and allows an explicit retry", async () => {
  let writes = 0;
  let watching = false;
  setup("watch", async (_path, init) => {
    if (init?.method === "GET") return envelope(watching ? [watch] : []);
    if (++writes === 1)
      return Response.json(
        { error: { code: "activity_unavailable", message: "Unavailable", request_id: "test" } },
        { status: 503 },
      );
    watching = true;
    return envelope(watch);
  });
  const trigger = await screen.findByRole("button", { name: "Watch address" });
  await userEvent.click(trigger);
  await screen.findByRole("alert");
  expect(trigger).toHaveAttribute("aria-pressed", "false");
  expect(trigger.querySelector(".lucide-bell-ring")).toBeNull();
  expect(screen.queryByText("Address added to your watchlist.")).toBeNull();
  await userEvent.click(trigger);
  await screen.findByRole("button", { name: "Remove watch" });
  expect(document.querySelector(".address-watch-feedback")).toBeNull();
  expect(trigger).toHaveAttribute("aria-pressed", "true");
  expect(writes).toBe(2);
});
