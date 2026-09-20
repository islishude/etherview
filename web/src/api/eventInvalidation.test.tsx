import {
  chainQueryMeta,
  statusQueryMeta,
  userOperationQueryMeta,
  eventWatermark,
} from "./chainEvents";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ChainEventInvalidation, shouldInvalidateFromChainEvent } from "./eventInvalidation";

class FakeEventSource {
  static instances: FakeEventSource[] = [];

  static CLOSED = 2;
  readyState = 0;
  onerror?: () => void;
  onopen?: () => void;
  closed = false;
  private readonly listeners = new Map<string, Set<EventListener>>();

  constructor(readonly url: string | URL) {
    FakeEventSource.instances.push(this);
  }

  addEventListener(type: string, listener: EventListenerOrEventListenerObject | null) {
    if (typeof listener !== "function") return;
    const listeners = this.listeners.get(type) ?? new Set<EventListener>();
    listeners.add(listener);
    this.listeners.set(type, listeners);
  }

  removeEventListener(type: string, listener: EventListenerOrEventListenerObject | null) {
    if (typeof listener === "function") this.listeners.get(type)?.delete(listener);
  }

  close() {
    this.closed = true;
  }

  emit(type: "head" | "reorg" | "status", lastEventId = "1") {
    for (const listener of this.listeners.get(type) ?? [])
      listener(new MessageEvent(type, { lastEventId }));
  }
}

describe("durable chain event invalidation", () => {
  beforeEach(() => {
    FakeEventSource.instances = [];
    vi.stubGlobal("EventSource", FakeEventSource as unknown as typeof EventSource);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it("uses one same-origin event source and coalesces a runtime-event burst", async () => {
    const queryClient = new QueryClient();
    const invalidate = vi.spyOn(queryClient, "invalidateQueries").mockResolvedValue();
    const view = render(
      <QueryClientProvider client={queryClient}>
        <ChainEventInvalidation />
      </QueryClientProvider>,
    );
    expect(FakeEventSource.instances).toHaveLength(1);
    const source = FakeEventSource.instances[0]!;
    expect(String(source.url)).toBe("/api/v1/events");

    await act(async () => {
      source.emit("status");
      source.emit("head");
      await Promise.resolve();
    });
    expect(invalidate).toHaveBeenCalledTimes(1);
    view.unmount();
    expect(source.closed).toBe(true);
  });

  it("reconnects closed streams with bounded backoff and cleans up timers", async () => {
    vi.useFakeTimers();
    const client = new QueryClient();
    const view = render(
      <QueryClientProvider client={client}>
        <ChainEventInvalidation />
      </QueryClientProvider>,
    );
    const first = FakeEventSource.instances[0]!;
    await act(async () => {
      first.onerror?.();
      await vi.advanceTimersByTimeAsync(1000);
    });
    expect(FakeEventSource.instances).toHaveLength(1);
    await act(async () => {
      first.readyState = 2;
      first.onerror?.();
      await vi.advanceTimersByTimeAsync(999);
    });
    expect(first.closed).toBe(true);
    expect(FakeEventSource.instances).toHaveLength(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });
    const second = FakeEventSource.instances[1]!;
    await act(async () => {
      second.readyState = 2;
      second.onerror?.();
      await vi.advanceTimersByTimeAsync(1999);
    });
    expect(FakeEventSource.instances).toHaveLength(2);
    view.unmount();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(FakeEventSource.instances).toHaveLength(2);
  });

  it("tracks lossless event versions per client and withdraws orphan user operation data", async () => {
    const client = new QueryClient();
    const other = new QueryClient();
    client.setQueryDefaults(["user-operation"], { meta: userOperationQueryMeta });
    client.setQueryData(["user-operation", "hash"], { canonical: true });
    const view = render(
      <QueryClientProvider client={client}>
        <ChainEventInvalidation />
      </QueryClientProvider>,
    );
    await act(async () => {
      FakeEventSource.instances[0]!.emit("reorg", "9007199254740993");
    });
    expect(eventWatermark(client)).toBe("9007199254740993");
    expect(eventWatermark(other)).toBe("0");
    expect(client.getQueryData(["user-operation", "hash"])).toBeUndefined();
    expect(shouldInvalidateFromChainEvent("head", userOperationQueryMeta)).toBe(true);
    view.unmount();
  });

  it("invalidates chain queries without waking unrelated account work", () => {
    expect(shouldInvalidateFromChainEvent("status", statusQueryMeta)).toBe(true);
    expect(shouldInvalidateFromChainEvent("status", chainQueryMeta)).toBe(false);
    expect(shouldInvalidateFromChainEvent("head", chainQueryMeta)).toBe(true);
    expect(shouldInvalidateFromChainEvent("reorg", chainQueryMeta)).toBe(true);
    expect(shouldInvalidateFromChainEvent("head", undefined)).toBe(false);
    expect(shouldInvalidateFromChainEvent("head", undefined)).toBe(false);
  });
});
