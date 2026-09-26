import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { PrivateAccountCleanup, useNotifications } from "./privateAccount";
import { listNotifications } from "./watchlist";

const fixture = vi.hoisted(() => ({ user: "alice", authenticated: true }));
vi.mock("@/auth/AuthProvider", () => ({
  useAuth: () => ({
    session: {
      authenticated: fixture.authenticated,
      expires_at: "2099-01-01",
      user: { id: fixture.user },
    },
  }),
}));
vi.mock("./watchlist", () => ({ listNotifications: vi.fn(), listWatches: vi.fn() }));
function PrivateView() {
  const result = useNotifications();
  return (
    <>
      <PrivateAccountCleanup />
      <span>{result.data?.items[0]?.label ?? "empty"}</span>
    </>
  );
}
describe("private account lifecycle", () => {
  it("cancels and removes previous account requests and rejects late results", async () => {
    fixture.user = "alice";
    fixture.authenticated = true;
    let oldSignal: AbortSignal | undefined;
    let resolveOld: ((value: Awaited<ReturnType<typeof listNotifications>>) => void) | undefined;
    vi.mocked(listNotifications).mockImplementationOnce((_cursor, _unread, signal) => {
      oldSignal = signal;
      return new Promise((resolve) => {
        resolveOld = resolve;
      });
    });
    vi.mocked(listNotifications).mockResolvedValue({
      items: [],
      next_cursor: "",
      watermark: "0",
      unread_count: "0",
    });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const view = () => (
      <QueryClientProvider client={client}>
        <PrivateView />
      </QueryClientProvider>
    );
    const rendered = render(view());
    await waitFor(() => expect(oldSignal).toBeDefined());
    fixture.user = "bob";
    rendered.rerender(view());
    await waitFor(() => expect(oldSignal?.aborted).toBe(true));
    await act(async () => {
      resolveOld?.({
        items: [{ label: "Alice private label" } as never],
        next_cursor: "",
        watermark: "1",
        unread_count: "1",
      });
    });
    expect(screen.queryByText("Alice private label")).toBeNull();
    expect(client.getQueriesData({ queryKey: ["private-account", "alice:2099-01-01"] })).toEqual(
      [],
    );
    fixture.authenticated = false;
    rendered.rerender(view());
    await waitFor(() =>
      expect(client.getQueriesData({ queryKey: ["private-account", "bob:2099-01-01"] })).toEqual(
        [],
      ),
    );
    rendered.unmount();
    client.clear();
  });
});
