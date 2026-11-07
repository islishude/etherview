import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { AdminSurface, useAdminIdentity } from "./AdminSurface";
import { Sheet } from "./ui/sheet";
import { ApiError } from "@/api/client";
import i18n from "@/i18n";

const fixture = vi.hoisted(() => ({
  id: "alice",
  role: "admin",
  authenticated: true,
  loading: false,
  error: undefined as string | undefined,
  refresh: vi.fn(),
}));
vi.mock("@/auth/AuthProvider", () => ({
  useAuth: () => ({
    enabled: true,
    loading: fixture.loading,
    error: fixture.error,
    refresh: fixture.refresh,
    session: {
      authenticated: fixture.authenticated,
      expires_at: "2099-01-01",
      user: { id: fixture.id, role: fixture.role, status: "active" },
    },
  }),
}));
vi.mock("@/api/publicConfig", () => ({
  usePublicConfig: () => ({ data: { features: { api_billing: true } } }),
}));
vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
}));

beforeEach(async () => {
  fixture.id = "alice";
  fixture.role = "admin";
  fixture.authenticated = true;
  fixture.loading = false;
  fixture.error = undefined;
  fixture.refresh.mockReset();
  await i18n.changeLanguage("en");
});

it.each(["logout", "role loss", "identity switch"])(
  "clears drawers and in-flight private data on %s",
  async (change) => {
    let oldSignal: AbortSignal | undefined;
    let resolveOld: ((value: string) => void) | undefined;
    function Content() {
      const identity = useAdminIdentity();
      const result = useQuery({
        queryKey: ["private-account", identity, "admin", "test"],
        queryFn: ({ signal }) => {
          if (fixture.id !== "alice") return Promise.resolve("new session");
          oldSignal = signal;
          return new Promise<string>((resolve) => {
            resolveOld = resolve;
          });
        },
      });
      return (
        <Sheet closeLabel="Close" title={`Settings ${fixture.id}`} onClose={() => {}}>
          {result.data}
        </Sheet>
      );
    }
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const view = () => (
      <QueryClientProvider client={client}>
        <AdminSurface>
          <Content />
        </AdminSurface>
      </QueryClientProvider>
    );
    const rendered = render(view());
    await screen.findByRole("dialog", { name: "Settings alice" });
    await waitFor(() => expect(oldSignal).toBeDefined());
    if (change === "logout") fixture.authenticated = false;
    else if (change === "role loss") fixture.role = "user";
    else fixture.id = "bob";
    rendered.rerender(view());
    await waitFor(() => expect(oldSignal?.aborted).toBe(true));
    expect(screen.queryByRole("dialog", { name: "Settings alice" })).toBeNull();
    await act(async () => {
      resolveOld?.("old private response");
    });
    expect(screen.queryByText("old private response")).toBeNull();
    expect(
      client.getQueriesData({ queryKey: ["private-account", "alice:2099-01-01", "admin"] }),
    ).toEqual([]);
    rendered.unmount();
    client.clear();
  },
);

it("rechecks server-rejected administration and never mounts content while checking", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function Content() {
    useQuery({
      queryKey: ["private-account", "alice:2099-01-01", "admin", "test"],
      queryFn: () =>
        Promise.reject(
          new ApiError(403, {
            error: { code: "admin_required", message: "Admin required", request_id: "test" },
          }),
        ),
    });
    return <span>Private content</span>;
  }
  const view = () => (
    <QueryClientProvider client={client}>
      <AdminSurface>
        <Content />
      </AdminSurface>
    </QueryClientProvider>
  );
  const rendered = render(view());
  await waitFor(() => expect(fixture.refresh).toHaveBeenCalled());
  fixture.loading = true;
  rendered.rerender(view());
  expect(screen.queryByText("Private content")).toBeNull();
  rendered.unmount();
  client.clear();
});
