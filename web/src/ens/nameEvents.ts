import type { QueryClient } from "@tanstack/react-query";

// The shell's single EventSource owns transport. Consumers only subscribe to
// its coalesced publication/reconnect notifications.
export type NameChange = "refresh" | "invalidate";
const listeners = new WeakMap<QueryClient, Set<(change: NameChange) => void>>();
export function subscribeNames(client: QueryClient, listener: (change: NameChange) => void) {
  const set = listeners.get(client) ?? new Set<(change: NameChange) => void>();
  listeners.set(client, set);
  set.add(listener);
  return () => {
    set.delete(listener);
  };
}
export function notifyNames(client: QueryClient, change: NameChange = "invalidate") {
  for (const listener of listeners.get(client) ?? []) listener(change);
}

const refreshers = new WeakMap<QueryClient, Set<() => Promise<void>>>();
export function registerNamesRefresh(client: QueryClient, refresh: () => Promise<void>) {
  const set = refreshers.get(client) ?? new Set<() => Promise<void>>();
  refreshers.set(client, set);
  set.add(refresh);
  return () => {
    set.delete(refresh);
  };
}
export async function refreshVisibleNames(client: QueryClient) {
  await Promise.all([...(refreshers.get(client) ?? [])].map((refresh) => refresh()));
}
