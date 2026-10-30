import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { notifyNames } from "@/ens/nameEvents";
import { sameOriginAPIPath } from "./client";
import { observeEvent, type ChainEventType, type ChainQueryMeta } from "./chainEvents";
import { isBrowsingQuery, markBrowsingQuery } from "./browsingState";

const eventTypes: readonly ChainEventType[] = ["head", "reorg", "status", "names"];

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
    let scheduled: ReturnType<typeof setTimeout> | undefined;
    let opened = false;
    const pending = new Set<ChainEventType>();
    const pendingPublications = new Set<string>();
    let withdrawNames = false;
    const flush = () => {
      scheduled = undefined;
      if (disposed) return;
      const types = [...pending];
      if (types.includes("names") || types.includes("reorg"))
        notifyNames(
          queryClient,
          withdrawNames || types.includes("reorg") ? "invalidate" : "refresh",
        );
      const namesInvalidated = withdrawNames;
      withdrawNames = false;
      pending.clear();
      const publications = [...pendingPublications];
      pendingPublications.clear();
      for (const query of queryClient.getQueryCache().getAll()) {
        if (!isBrowsingQuery(query)) continue;
        const bounds = browsingBounds(query.state.data);
        const affected =
          (types.includes("reorg") && query.meta?.chainEvents?.includes("reorg")) ||
          (types.includes("names") && query.meta?.chainEvents?.includes("names")) ||
          (query.meta?.chainEvents?.includes("status") &&
            publications.some(
              (number) => !bounds || (BigInt(number) >= bounds.from && BigInt(number) <= bounds.to),
            ));
        if (!affected) continue;
        // Cancellation fences late results; invalidation only marks the snapshot.
        // Do not reset the query: empty active queries can start loading again.
        markBrowsingQuery(
          query,
          (types.includes("reorg") && query.meta?.chainEvents?.includes("reorg")) ||
            (types.includes("names") && namesInvalidated),
        );
      }
      void queryClient.invalidateQueries({
        predicate: (query) =>
          query.meta?.refreshPolicy === "live" &&
          types.some((type) => shouldInvalidateFromChainEvent(type, query.meta)),
      });
    };
    const schedule = (types: readonly ChainEventType[]) => {
      for (const type of types) pending.add(type);
      if (types.includes("reorg") && scheduled !== undefined) {
        clearTimeout(scheduled);
        scheduled = undefined;
      }
      if (scheduled !== undefined || disposed) return;
      // EventSource replay delivers separate tasks; a microtask cannot batch it.
      scheduled = setTimeout(flush, types.includes("reorg") ? 0 : 250);
    };
    const connect = () => {
      if (disposed) return;
      source = new EventSource(sameOriginAPIPath("/events"));
      const current = source;
      for (const type of eventTypes) {
        current.addEventListener(type, (event) => {
          if (disposed || current !== source) return;
          observeEvent(queryClient, (event as MessageEvent).lastEventId ?? "");
          if (type === "names") {
            try {
              const payload: unknown = JSON.parse((event as MessageEvent).data);
              // Recognized publications preserve still-valid observations.
              // Unknown or malformed events withdraw them without rereading.
              if (
                typeof payload !== "object" ||
                payload === null ||
                ("invalidated" in payload && payload.invalidated !== false) ||
                !(
                  ("publication" in payload && decimalBlock(payload.publication)) ||
                  ("directory" in payload && decimalBlock(payload.directory)) ||
                  ("history" in payload &&
                    payload.history === true &&
                    "invalidated" in payload &&
                    payload.invalidated === false)
                )
              )
                withdrawNames = true;
            } catch {
              withdrawNames = true;
            }
          }
          if (type === "status") {
            try {
              const payload: unknown = JSON.parse((event as MessageEvent).data);
              if (
                typeof payload === "object" &&
                payload !== null &&
                "reason" in payload &&
                payload.reason === "browsing_publication" &&
                "block_number" in payload &&
                decimalBlock(payload.block_number)
              )
                pendingPublications.add(payload.block_number);
            } catch {
              // Other status payloads refresh only allowlisted live queries.
            }
          }
          backoff = 1_000;
          schedule([type]);
        });
      }
      current.onopen = () => {
        if (!disposed && current === source) {
          if (opened) {
            withdrawNames = true;
            schedule(eventTypes);
          } else schedule(["head", "status"]);
          opened = true;
        }
      };
      current.onerror = () => {
        if (disposed || current !== source || current.readyState !== EventSource.CLOSED) return;
        current.close();
        source = undefined;
        if (opened) {
          withdrawNames = true;
          schedule(eventTypes);
        }
        reconnect = setTimeout(connect, backoff);
        backoff = Math.min(backoff * 2, 30_000);
      };
    };
    connect();
    return () => {
      disposed = true;
      pending.clear();
      if (scheduled !== undefined) clearTimeout(scheduled);
      if (reconnect !== undefined) clearTimeout(reconnect);
      source?.close();
    };
  }, [queryClient]);
  return null;
}

function decimalBlock(value: unknown): value is string {
  return typeof value === "string" && /^(0|[1-9][0-9]{0,77})$/.test(value);
}

function browsingBounds(data: unknown): { from: bigint; to: bigint } | undefined {
  if (typeof data !== "object" || data === null || !("meta" in data)) return;
  const meta = data.meta;
  if (typeof meta !== "object" || meta === null || !("browsing" in meta)) return;
  const browsing = meta.browsing;
  if (
    typeof browsing !== "object" ||
    browsing === null ||
    !("from_block" in browsing) ||
    !("to_block" in browsing) ||
    !decimalBlock(browsing.from_block) ||
    !decimalBlock(browsing.to_block)
  )
    return;
  return { from: BigInt(browsing.from_block), to: BigInt(browsing.to_block) };
}
