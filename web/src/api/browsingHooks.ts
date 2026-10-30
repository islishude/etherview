import { createContext, useCallback, useContext, useEffect, useSyncExternalStore } from "react";
import { QueryClientContext, useQueryClient, type QueryKey } from "@tanstack/react-query";
import { activeQueries, snapshotInvalid, queryWithdrawn } from "./browsingState";

export function useQueryWithdrawn(queryKey: QueryKey, exact = true): boolean {
  const client = useQueryClient();
  const subscribe = useCallback(
    (notify: () => void) => client.getQueryCache().subscribe(notify),
    [client],
  );
  return useSyncExternalStore(subscribe, () =>
    client.getQueryCache().findAll({ queryKey, exact }).some(queryWithdrawn),
  );
}

export function useBrowsingInvalidated(): boolean {
  const client = useContext(QueryClientContext);
  const subscribe = useCallback(
    (notify: () => void) => client?.getQueryCache().subscribe(notify) ?? (() => {}),
    [client],
  );
  return useSyncExternalStore(subscribe, () =>
    client ? activeQueries(client).some((query) => snapshotInvalid(query)) : false,
  );
}

export const RefreshContext = createContext<
  | {
      register: (identity: string, reset: () => void) => () => void;
    }
  | undefined
>(undefined);

export function useRefreshPager(identity: string, reset: () => void): boolean {
  const context = useContext(RefreshContext);
  const client = useContext(QueryClientContext);
  useEffect(() => context?.register(identity, reset), [context, identity, reset]);
  const subscribe = useCallback(
    (notify: () => void) => client?.getQueryCache().subscribe(notify) ?? (() => {}),
    [client],
  );
  return useSyncExternalStore(subscribe, () =>
    client
      ? activeQueries(client).some(
          (query) => query.meta?.pagination === identity && snapshotInvalid(query),
        )
      : false,
  );
}
