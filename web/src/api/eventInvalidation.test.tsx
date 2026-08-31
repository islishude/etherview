import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  ChainEventInvalidation,
  shouldInvalidateFromChainEvent,
} from "./eventInvalidation";

class FakeEventSource {
  static instances: FakeEventSource[] = [];

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

  emit(type: "head" | "reorg" | "status") {
    for (const listener of this.listeners.get(type) ?? []) listener(new Event(type));
  }
}

describe("durable chain event invalidation", () => {
  beforeEach(() => {
    FakeEventSource.instances = [];
    vi.stubGlobal("EventSource", FakeEventSource as unknown as typeof EventSource);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
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

  it("invalidates chain queries without waking unrelated account work", () => {
    expect(shouldInvalidateFromChainEvent("status", ["status"])).toBe(true);
    expect(shouldInvalidateFromChainEvent("status", ["transactions"])).toBe(false);
    expect(shouldInvalidateFromChainEvent("head", ["transactions", 25])).toBe(true);
    expect(shouldInvalidateFromChainEvent("reorg", ["contract-proxy", "0x01"])).toBe(true);
    expect(shouldInvalidateFromChainEvent("head", ["verification-job", "job"])).toBe(false);
    expect(shouldInvalidateFromChainEvent("head", ["current-user-api-keys"])).toBe(false);
  });
});
