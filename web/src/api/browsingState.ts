import type { Query, QueryClient } from "@tanstack/react-query";
import { ApiError } from "./client";

export function isBrowsingQuery(query: Query): boolean {
  return query.meta?.refreshPolicy !== "live" && Boolean(query.meta?.chainEvents);
}

const invalidations = new WeakMap<Query, number>();

export function snapshotInvalid(query: Query) {
  return (
    query.state.dataUpdateCount <= (invalidations.get(query) ?? -1) ||
    (query.state.error instanceof ApiError && query.state.error.code === "invalid_cursor")
  );
}

const withdrawals = new WeakMap<Query, number>();

export function markBrowsingQuery(query: Query, withdraw = false) {
  void query.cancel({ revert: true, silent: true });
  invalidations.set(query, query.state.dataUpdateCount);
  if (withdraw) withdrawals.set(query, query.state.dataUpdateCount);
  query.setState({ isInvalidated: true });
}

export function activeQueries(client: QueryClient) {
  return client
    .getQueryCache()
    .getAll()
    .filter((query) => query.isActive() && isBrowsingQuery(query));
}

export function queryWithdrawn(query: Query) {
  return query.state.dataUpdateCount <= (withdrawals.get(query) ?? -1);
}
