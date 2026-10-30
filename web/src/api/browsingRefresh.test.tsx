import { BrowsingRefreshControl } from "@/components/BrowsingRefreshControl";
import { PageHeading } from "@/components/PageHeading";
import { markBrowsingQuery } from "./browsingState";
import { useQueryWithdrawn } from "./browsingHooks";
import { QueryClient, QueryClientProvider, useQuery, onlineManager } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { useState } from "react";
import "@/i18n";
import { BrowsingRefreshProvider } from "./browsingRefresh";
import { useCursorHistory } from "./pagination";
import { ChainEventInvalidation } from "./eventInvalidation";
import { chainQueryMeta, statusQueryMeta } from "./chainEvents";

class Events {
  static latest: Events;
  static CLOSED = 2;
  readyState = 1;
  onopen?: () => void;
  onerror?: () => void;
  listeners = new Map<string, EventListener>();
  constructor() {
    Events.latest = this;
  }
  addEventListener(type: string, listener: EventListener) {
    this.listeners.set(type, listener);
  }
  close() {}
  emit(type: string, data = "{}") {
    this.listeners.get(type)?.(new MessageEvent(type, { data, lastEventId: "2" }));
  }
}
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  onlineManager.setOnline(true);
});

it("only refetches allowlisted live queries on events, initial open and reconnect", async () => {
  vi.stubGlobal("EventSource", Events);
  const reads = { manual: 0, live: 0 };
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, refetchOnReconnect: false } },
  });
  function Resources() {
    useQuery({ queryKey: ["manual"], meta: statusQueryMeta, queryFn: async () => ++reads.manual });
    useQuery({
      queryKey: ["live"],
      meta: { ...statusQueryMeta, refreshPolicy: "live" },
      queryFn: async () => ++reads.live,
    });
    return null;
  }
  render(
    <QueryClientProvider client={client}>
      <ChainEventInvalidation />
      <Resources />
    </QueryClientProvider>,
  );
  await waitFor(() => expect(reads).toEqual({ manual: 1, live: 1 }));
  vi.useFakeTimers();
  await act(async () => {
    Events.latest.readyState = Events.CLOSED;
    Events.latest.onerror?.();
    await vi.advanceTimersByTimeAsync(1000);
  });
  expect(client.getQueryState(["manual"])?.isInvalidated).toBe(false);
  expect(reads.manual).toBe(1);
  await act(async () => {
    Events.latest.onopen?.();
    await vi.advanceTimersByTimeAsync(250);
  });
  expect(client.getQueryState(["manual"])?.isInvalidated).toBe(false);
  for (const type of ["head", "status", "names"]) {
    await act(async () => {
      Events.latest.emit(type);
      await vi.advanceTimersByTimeAsync(250);
    });
  }
  expect(reads.manual).toBe(1);
  expect(reads.live).toBeGreaterThan(1);
  await act(async () => {
    Events.latest.onopen?.();
    await vi.advanceTimersByTimeAsync(250);
  });
  expect(reads.manual).toBe(1);
  expect(client.getQueryState(["manual"])?.isInvalidated).toBe(true);
  await act(async () => {
    onlineManager.setOnline(false);
    onlineManager.setOnline(true);
    await vi.advanceTimersByTimeAsync(20_000);
  });
  expect(reads.manual).toBe(1);
});

it("refreshes the current overview and visible pager while preserving hidden cursors and form input", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const reads: string[] = [];
  function Page() {
    const [tab, setTab] = useState("a");
    const a = useCursorHistory("a");
    const b = useCursorHistory("b");
    const query = (id: string, pager: typeof a) => ({
      queryKey: [id, pager.request],
      meta: { ...chainQueryMeta, pagination: id },
      enabled: tab === id,
      queryFn: async () => {
        reads.push(`${id}:${pager.request.cursor ?? "first"}`);
        return "rows";
      },
    });
    useQuery(query("a", a));
    useQuery(query("b", b));
    useQuery({
      queryKey: ["overview"],
      meta: chainQueryMeta,
      queryFn: async () => {
        reads.push("overview");
        return "overview";
      },
    });
    return (
      <>
        <input aria-label="draft" />
        <button onClick={() => setTab(tab === "a" ? "b" : "a")}>Switch</button>
        <button onClick={() => (tab === "a" ? a : b).next("second")}>Next</button>
        <output>{`${tab}:${a.page}:${b.page}`}</output>
      </>
    );
  }
  render(
    <QueryClientProvider client={client}>
      <BrowsingRefreshProvider location="address">
        <PageHeading title="Example" actions={<BrowsingRefreshControl />} />
        <Page />
      </BrowsingRefreshProvider>
    </QueryClientProvider>,
  );
  await waitFor(() => expect(screen.getByRole("button", { name: "Refresh" })).toBeEnabled());
  fireEvent.change(screen.getByLabelText("draft"), { target: { value: "preserved" } });
  fireEvent.click(screen.getByText("Next"));
  await waitFor(() => expect(reads).toContain("a:second"));
  fireEvent.click(screen.getByText("Switch"));
  await waitFor(() => expect(reads).toContain("b:first"));
  fireEvent.click(screen.getByText("Next"));
  await waitFor(() => expect(reads).toContain("b:second"));
  await waitFor(() => expect(screen.getByRole("button", { name: "Refresh" })).toBeEnabled());
  reads.length = 0;
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Refresh" })).toBeEnabled());
  expect(reads.sort()).toEqual(["b:first", "overview"]);
  expect(screen.getByText("b:2:1")).toBeVisible();
  expect(screen.getByLabelText("draft")).toHaveValue("preserved");
});

it("fences late cancelled responses and keeps withdrawal until a successful explicit read", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(["name"], "initial");
  let finish: ((value: string) => void) | undefined;
  let reads = 0;
  function Page() {
    const query = useQuery({
      queryKey: ["name"],
      meta: chainQueryMeta,
      staleTime: Infinity,
      queryFn: async () => {
        reads++;
        if (reads === 1)
          return new Promise<string>((resolve) => {
            finish = resolve;
          });
        return "fresh";
      },
    });
    const withdrawn = useQueryWithdrawn(["name"]);
    return <output>{withdrawn ? "withdrawn" : query.data}</output>;
  }
  render(
    <QueryClientProvider client={client}>
      <BrowsingRefreshProvider location="name">
        <PageHeading title="Example" actions={<BrowsingRefreshControl />} />
        <Page />
      </BrowsingRefreshProvider>
    </QueryClientProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await waitFor(() => expect(finish).toBeTypeOf("function"));
  act(() => markBrowsingQuery(client.getQueryCache().find({ queryKey: ["name"] })!, true));
  await act(async () => finish!("obsolete"));
  expect(screen.getByText("withdrawn")).toBeVisible();
  expect(client.getQueryData(["name"])).toBe("initial");
  await waitFor(() => expect(screen.getByRole("button", { name: "Refresh" })).toBeEnabled());
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  expect(await screen.findByText("fresh")).toBeVisible();
  expect(screen.queryByText(/snapshot may be out of date/)).toBeNull();
});

it("keeps invalid snapshots after a failed refresh and permits a deliberate retry", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(["failure"], "old snapshot");
  let reads = 0;
  function Page() {
    const query = useQuery({
      queryKey: ["failure"],
      meta: chainQueryMeta,
      staleTime: Infinity,
      queryFn: async () => {
        if (++reads === 1) throw new Error("unavailable");
        return "new snapshot";
      },
    });
    return <output>{query.error ? "manual read failed" : query.data}</output>;
  }
  render(
    <QueryClientProvider client={client}>
      <BrowsingRefreshProvider location="failure">
        <PageHeading title="Example" actions={<BrowsingRefreshControl />} />
        <Page />
      </BrowsingRefreshProvider>
    </QueryClientProvider>,
  );
  act(() => markBrowsingQuery(client.getQueryCache().find({ queryKey: ["failure"] })!));
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  expect(await screen.findByText("manual read failed")).toBeVisible();
  expect(screen.getByText("Data changed")).toBeInTheDocument();
  expect(reads).toBe(1);
  await waitFor(() => expect(screen.getByRole("button", { name: "Refresh" })).toBeEnabled());
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  expect(await screen.findByText("new snapshot")).toBeVisible();
  expect(screen.queryByText(/snapshot may be out of date/)).toBeNull();
  expect(reads).toBe(2);
});
