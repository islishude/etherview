import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { AddressNamesProvider, usePrimaryName } from "./AddressNamesProvider";
import { notifyNames } from "./nameEvents";
vi.mock("@/api/publicConfig", () => ({
  usePublicConfig: () => ({ data: { features: { ens: true } } }),
}));
afterEach(() => vi.unstubAllGlobals());
const address = (n: number) => `0x${n.toString(16).padStart(40, "0")}`;
function Names({ count = 1 }: { count?: number }) {
  return (
    <>
      {Array.from({ length: count }, (_, i) => (
        <Name key={i} value={address(i + 1)} />
      ))}
    </>
  );
}
function Name({ value }: { value: string }) {
  const name = usePrimaryName(value);
  return <span>{name?.name ?? value}</span>;
}
function setup(count = 1) {
  const client = new QueryClient();
  const view = render(
    <QueryClientProvider client={client}>
      <AddressNamesProvider>
        <Names count={count} />
      </AddressNamesProvider>
    </QueryClientProvider>,
  );
  return { ...view, client };
}
function response(url: URL, name: string, version: string, expiry = Date.now() + 60000) {
  return new Response(
    JSON.stringify({
      data: {
        snapshot: `snapshot-${version}`,
        publication: version,
        items: (url.searchParams.get("addresses") ?? "").split(",").map((address) => ({
          address,
          state: "resolved",
          primary_name: {
            name,
            publication: version,
            valid_until: new Date(expiry).toISOString(),
          },
        })),
      },
      meta: { request_id: "test", chain_id: "1" },
    }),
    { headers: { "Content-Type": "application/json" } },
  );
}
it("commits all visible chunks atomically and replaces the entire publication on names events", async () => {
  let version = "1";
  let finish: (() => void) | undefined;
  const calls: Array<URL> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), "http://localhost");
      calls.push(url);
      if (calls.length === 2)
        await new Promise<void>((resolve) => {
          finish = resolve;
        });
      return response(url, `name-${version}.test`, version);
    }),
  );
  const { client } = setup(101);
  await waitFor(() => expect(finish).toBeTypeOf("function"));
  expect(screen.queryByText("name-1.test")).toBeNull();
  await act(async () => finish!());
  expect(await screen.findAllByText("name-1.test")).toHaveLength(101);
  expect(calls[0]!.searchParams.get("snapshot")).toBeNull();
  expect(calls[1]!.searchParams.get("snapshot")).toBe("snapshot-1");
  version = "2";
  await act(async () => notifyNames(client));
  expect(screen.queryByText("name-1.test")).toBeNull();
  expect(await screen.findAllByText("name-2.test")).toHaveLength(101);
  expect(calls[2]!.searchParams.get("snapshot")).toBeNull();
  expect(calls[3]!.searchParams.get("snapshot")).toBe("snapshot-2");
});
it("rejects a late old response even when its transport ignores cancellation", async () => {
  let finish: (() => void) | undefined;
  let calls = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), "http://localhost");
      const n = ++calls;
      if (n === 1)
        await new Promise<void>((resolve) => {
          finish = resolve;
        });
      return response(url, n === 1 ? "old.test" : "new.test", String(n));
    }),
  );
  const { client } = setup();
  await waitFor(() => expect(finish).toBeTypeOf("function"));
  await act(async () => notifyNames(client));
  expect(await screen.findByText("new.test")).toBeVisible();
  await act(async () => finish!());
  expect(screen.queryByText("old.test")).toBeNull();
  expect(screen.getByText("new.test")).toBeVisible();
});
it("hides expired labels without another request or live stream", async () => {
  const fetcher = vi.fn(async (input: RequestInfo | URL) =>
    response(new URL(String(input), "http://localhost"), "brief.test", "1", Date.now() + 150),
  );
  vi.stubGlobal("fetch", fetcher);
  setup();
  expect(await screen.findByText("brief.test")).toBeVisible();
  await waitFor(() => expect(screen.queryByText("brief.test")).toBeNull());
  expect(screen.getByText(address(1))).toBeVisible();
  expect(fetcher).toHaveBeenCalledTimes(1);
});
it("keeps missing and failed publication entries as exact addresses", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            data: {
              snapshot: "empty",
              publication: "0",
              items: [{ address: address(1), state: "unavailable", code: "not_indexed" }],
            },
            meta: { request_id: "test", chain_id: "1" },
          }),
          { headers: { "Content-Type": "application/json" } },
        ),
    ),
  );
  setup();
  expect(await screen.findByText(address(1))).toBeVisible();
});

it("retains the visible batch during publication refresh and atomically replaces it", async () => {
  let pending: (() => void) | undefined;
  let calls = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const n = ++calls;
      if (n === 4)
        await new Promise<void>((resolve) => {
          pending = resolve;
        });
      return response(
        new URL(String(input), "http://localhost"),
        n <= 2 ? "old.test" : "new.test",
        n <= 2 ? "1" : "2",
      );
    }),
  );
  const { client } = setup(101);
  expect(await screen.findAllByText("old.test")).toHaveLength(101);
  act(() => notifyNames(client, "refresh"));
  await waitFor(() => expect(pending).toBeTypeOf("function"));
  expect(screen.getAllByText("old.test")).toHaveLength(101);
  expect(screen.queryByText("new.test")).toBeNull();
  await act(async () => pending!());
  expect(await screen.findAllByText("new.test")).toHaveLength(101);
  expect(screen.queryByText("old.test")).toBeNull();
});

it("withdraws immediately on real invalidation and ignores the in-flight refresh", async () => {
  let pending: (() => void) | undefined;
  let calls = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const n = ++calls;
      if (n === 2)
        await new Promise<void>((resolve) => {
          pending = resolve;
        });
      if (n === 3) throw new Error("unavailable after invalidation");
      return response(new URL(String(input), "http://localhost"), "old.test", String(n));
    }),
  );
  const { client } = setup();
  await screen.findByText("old.test");
  act(() => notifyNames(client, "refresh"));
  await waitFor(() => expect(pending).toBeTypeOf("function"));
  act(() => notifyNames(client, "invalidate"));
  expect(screen.queryByText("old.test")).toBeNull();
  await act(async () => pending!());
  await waitFor(() => expect(calls).toBe(3));
  expect(screen.queryByText("old.test")).toBeNull();
});

it("a failed background refresh does not withdraw or extend unexpired names", async () => {
  let calls = 0;
  const expiry = Date.now() + 500;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      if (++calls > 1) throw new Error("network unavailable");
      return response(new URL(String(input), "http://localhost"), "brief.test", "1", expiry);
    }),
  );
  const { client } = setup();
  await screen.findByText("brief.test");
  act(() => notifyNames(client, "refresh"));
  await waitFor(() => expect(calls).toBe(2));
  expect(screen.getByText("brief.test")).toBeVisible();
  await waitFor(() => expect(screen.queryByText("brief.test")).toBeNull());
  expect(calls).toBe(2);
});

it("withdraws an invalid snapshot during refresh before retrying the current publication", async () => {
  let retry: (() => void) | undefined;
  let calls = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const n = ++calls;
      if (n === 2)
        return Response.json(
          { error: { code: "invalid_cursor", message: "Expired snapshot", request_id: "test" } },
          { status: 400 },
        );
      if (n === 3)
        await new Promise<void>((resolve) => {
          retry = resolve;
        });
      return response(
        new URL(String(input), "http://localhost"),
        n === 1 ? "old.test" : "current.test",
        String(n),
      );
    }),
  );
  const { client } = setup();
  await screen.findByText("old.test");
  act(() => notifyNames(client, "refresh"));
  await waitFor(() => expect(retry).toBeTypeOf("function"));
  expect(screen.queryByText("old.test")).toBeNull();
  await act(async () => retry!());
  expect(await screen.findByText("current.test")).toBeVisible();
});
