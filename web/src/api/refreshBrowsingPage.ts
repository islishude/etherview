import { flushSync } from "react-dom";
import type { QueryClient } from "@tanstack/react-query";
import { refreshVisibleNames } from "@/ens/nameEvents";
import { isBrowsingQuery } from "./browsingState";

export async function refreshBrowsingPage(
  client: QueryClient,
  pagers: Map<string, () => void>,
  isCurrent: () => boolean,
) {
  if (!isCurrent()) return;
  const current = client
    .getQueryCache()
    .getAll()
    .filter((query) => query.isActive() && isBrowsingQuery(query));
  const selected = new Set(current.map((query) => query.queryHash));
  await client.cancelQueries({ predicate: (query) => selected.has(query.queryHash) });
  if (!isCurrent()) return;
  const reset = new Set(
    current.flatMap((query) => {
      const identity = query.meta?.pagination;
      return identity && pagers.has(identity) ? [identity] : [];
    }),
  );
  // Commit new pagination keys first: never fetch old cursors during a restart.
  flushSync(() => {
    for (const identity of reset) pagers.get(identity)?.();
  });
  await Promise.all([
    client.refetchQueries({
      predicate: (query) =>
        selected.has(query.queryHash) &&
        query.isActive() &&
        !reset.has(query.meta?.pagination ?? ""),
    }),
    refreshVisibleNames(client),
  ]);
}
