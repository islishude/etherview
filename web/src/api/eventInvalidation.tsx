import { useEffect } from "react";
import { useQueryClient, type QueryKey } from "@tanstack/react-query";

import { sameOriginAPIPath } from "./client";

const chainQueryRoots = new Set([
  "home",
  "status",
  "blocks",
  "transactions",
  "pending-transactions",
  "block",
  "transaction",
  "address",
  "tokens",
  "token",
  "nft",
  "search",
  "block-stats",
  "aggregate-stats",
  "chart-overview",
  "chart-metric",
  "verified-contract-artifact",
  "contract-proxy",
]);

export function shouldInvalidateFromChainEvent(
  eventType: string,
  queryKey: QueryKey,
): boolean {
  const root = queryKey[0];
  if (typeof root !== "string") return false;
  if (eventType === "status") return root === "home" || root === "status";
  return (eventType === "head" || eventType === "reorg") && chainQueryRoots.has(root);
}

export function ChainEventInvalidation() {
  const queryClient = useQueryClient();

  useEffect(() => {
    if (typeof EventSource === "undefined") return;
    const source = new EventSource(sameOriginAPIPath("/events"));
    const pending = new Set<string>();
    let scheduled = false;
    const flush = () => {
      scheduled = false;
      const eventTypes = [...pending];
      pending.clear();
      void queryClient.invalidateQueries({
        predicate: (query) => eventTypes.some((eventType) =>
          shouldInvalidateFromChainEvent(eventType, query.queryKey)),
      });
    };
    const invalidate = (event: Event) => {
      pending.add(event.type);
      if (scheduled) return;
      scheduled = true;
      queueMicrotask(flush);
    };
    for (const eventType of ["head", "reorg", "status"]) {
      source.addEventListener(eventType, invalidate);
    }
    return () => {
      for (const eventType of ["head", "reorg", "status"]) {
        source.removeEventListener(eventType, invalidate);
      }
      source.close();
    };
  }, [queryClient]);

  return null;
}
