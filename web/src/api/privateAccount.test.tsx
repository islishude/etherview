import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { PrivateAccountCleanup, useWatches } from "./privateAccount";
import { listWatches } from "./watchlist";

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
vi.mock("./watchlist", () => ({ listWatches: vi.fn() }));
function PrivateView() {
  const result = useWatches();
  return (
    <>
      <PrivateAccountCleanup />
      <span>{result.data?.[0]?.label ?? "empty"}</span>
    </>
  );
}
describe("private account lifecycle", () => {
  it("cancels and removes previous account requests and rejects late results", async () => {
    fixture.user = "alice";
    fixture.authenticated = true;
    let oldSignal: AbortSignal | undefined;
    let resolveOld: ((value: Awaited<ReturnType<typeof listWatches>>) => void) | undefined;
    vi.mocked(listWatches).mockImplementationOnce((signal) => {
      oldSignal = signal;
      return new Promise((resolve) => {
        resolveOld = resolve;
      });
    });
    vi.mocked(listWatches).mockResolvedValue([]);
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
      resolveOld?.([{ label: "Alice private label" } as never]);
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
