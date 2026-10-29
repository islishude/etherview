import type { QueryClient } from "@tanstack/react-query";

// The shell's single EventSource owns transport. Consumers only subscribe to
// its coalesced publication/reconnect notifications.
const listeners = new WeakMap<QueryClient, Set<() => void>>();
export function subscribeNames(client: QueryClient, listener: () => void) {
  const set = listeners.get(client) ?? new Set<() => void>();
  listeners.set(client, set);
  set.add(listener);
  return () => {
    set.delete(listener);
  };
}
export function notifyNames(client: QueryClient) {
  for (const listener of listeners.get(client) ?? []) listener();
}
