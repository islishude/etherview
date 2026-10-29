import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { notifyNames } from "@/ens/nameEvents";
import { sameOriginAPIPath } from "./client";
import { observeEvent, type ChainEventType, type ChainQueryMeta } from "./chainEvents";
import { resetBrowsingPagination } from "./pagination";

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
    let refreshing = Promise.resolve();
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
      withdrawNames = false;
      pending.clear();
      const publications = [...pendingPublications];
      pendingPublications.clear();
      refreshing = refreshing.then(async () => {
        if (disposed) return;
        const matches = (meta?: ChainQueryMeta) =>
          types.some((type) => shouldInvalidateFromChainEvent(type, meta));
        await queryClient.cancelQueries({ predicate: (query) => matches(query.meta) });
        if (disposed) return;
        const publicationAffects = (data: unknown) => {
          const bounds = browsingBounds(data);
          return publications.some((number) =>
            bounds ? BigInt(number) >= bounds.from && BigInt(number) <= bounds.to : true,
          );
        };
        const affectedPage = queryClient
          .getQueryCache()
          .getAll()
          .some(
            (query) =>
              query.getObserversCount() > 0 &&
              query.meta?.clearOnStatus === true &&
              browsingBounds(query.state.data) !== undefined &&
              publicationAffects(query.state.data),
          );
        if (affectedPage || types.includes("reorg")) {
          resetBrowsingPagination(queryClient);
        }
        const reset = (meta: ChainQueryMeta | undefined, data: unknown) =>
          (types.includes("reorg") && meta?.clearOnReorg === true) ||
          (meta?.clearOnStatus === true && publicationAffects(data));
        void Promise.all([
          queryClient.resetQueries({
            predicate: (query) => matches(query.meta) && reset(query.meta, query.state.data),
          }),
          queryClient.invalidateQueries({
            predicate: (query) => matches(query.meta) && !reset(query.meta, query.state.data),
          }),
        ]);
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
              // Only recognized publication notifications are safe to
              // refresh silently. Unknown or malformed events fail closed.
              if (
                typeof payload !== "object" ||
                payload === null ||
                ("invalidated" in payload && payload.invalidated !== false) ||
                !(
                  ("publication" in payload && decimalBlock(payload.publication)) ||
                  ("directory" in payload && decimalBlock(payload.directory))
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
              // Other status payloads still invalidate queries normally.
            }
          }
          backoff = 1_000;
          schedule([type]);
        });
      }
      current.onopen = () => {
        if (!disposed && current === source) {
          withdrawNames = true;
          schedule(eventTypes);
        }
      };
      current.onerror = () => {
        if (disposed || current !== source || current.readyState !== EventSource.CLOSED) return;
        current.close();
        source = undefined;
        withdrawNames = true;
        schedule(eventTypes);
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
