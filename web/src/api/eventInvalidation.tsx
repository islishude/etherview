import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { sameOriginAPIPath } from "./client";
import { observeEvent, type ChainEventType, type ChainQueryMeta } from "./chainEvents";

const eventTypes: readonly ChainEventType[] = ["head", "reorg", "status"];

export function shouldInvalidateFromChainEvent(
  eventType: ChainEventType,
  meta?: ChainQueryMeta,
): boolean {
  return meta?.chainEvents?.includes(eventType) ?? false;
}

export function ChainEventInvalidation() {
  const queryClient = useQueryClient();
  useEffect(() => {
    if (typeof EventSource === "undefined") return;
    let source: EventSource | undefined;
    let reconnect: ReturnType<typeof setTimeout> | undefined;
    let backoff = 1_000;
    let disposed = false;
    let scheduled = false;
    let refreshing = Promise.resolve();
    const pending = new Set<ChainEventType>();
    const flush = () => {
      scheduled = false;
      if (disposed) return;
      const types = [...pending];
      pending.clear();
      refreshing = refreshing.then(async () => {
        if (disposed) return;
        const matches = (meta?: ChainQueryMeta) =>
          types.some((type) => shouldInvalidateFromChainEvent(type, meta));
        await queryClient.cancelQueries({ predicate: (query) => matches(query.meta) });
        if (disposed) return;
        const reset = (meta?: ChainQueryMeta) =>
          types.includes("reorg") && meta?.clearOnReorg === true;
        void Promise.all([
          queryClient.resetQueries({
            predicate: (query) => matches(query.meta) && reset(query.meta),
          }),
          queryClient.invalidateQueries({
            predicate: (query) => matches(query.meta) && !reset(query.meta),
          }),
        ]);
      });
    };
    const schedule = (types: readonly ChainEventType[]) => {
      for (const type of types) pending.add(type);
      if (scheduled || disposed) return;
      scheduled = true;
      queueMicrotask(flush);
    };
    const connect = () => {
      if (disposed) return;
      source = new EventSource(sameOriginAPIPath("/events"));
      const current = source;
      for (const type of eventTypes) {
        current.addEventListener(type, (event) => {
          if (disposed || current !== source) return;
          observeEvent(queryClient, (event as MessageEvent).lastEventId ?? "");
          backoff = 1_000;
          schedule([type]);
        });
      }
      current.onopen = () => {
        if (!disposed && current === source) schedule(eventTypes);
      };
      current.onerror = () => {
        if (disposed || current !== source || current.readyState !== EventSource.CLOSED) return;
        current.close();
        source = undefined;
        schedule(eventTypes);
        reconnect = setTimeout(connect, backoff);
        backoff = Math.min(backoff * 2, 30_000);
      };
    };
    connect();
    return () => {
      disposed = true;
      pending.clear();
      if (reconnect !== undefined) clearTimeout(reconnect);
      source?.close();
    };
  }, [queryClient]);
  return null;
}
