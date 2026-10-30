import {
  useCallback,
  useId,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type ReactNode,
} from "react";
import { RotateCw } from "lucide-react";
import { UIIcon } from "@/components/UIIcon";
import { BrowsingRefreshControlContext } from "@/components/BrowsingRefreshControl";
import "./browsingRefresh.css";
import "@/i18n/resources/refresh";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { activeQueries, snapshotInvalid } from "./browsingState";
import { RefreshContext } from "./browsingHooks";
import { refreshBrowsingPage } from "./refreshBrowsingPage";

export function BrowsingRefreshProvider({
  children,
  location,
}: {
  children: ReactNode;
  location: string;
}) {
  const client = useQueryClient();
  const hintID = useId();
  const { t } = useTranslation();
  const pagers = useRef(new Map<string, () => void>());
  const route = useRef(location);
  route.current = location;
  const running = useRef(false);
  const [refreshing, setRefreshing] = useState(false);
  const [hintDismissed, setHintDismissed] = useState(false);
  const subscribe = useCallback(
    (notify: () => void) => client.getQueryCache().subscribe(notify),
    [client],
  );
  useSyncExternalStore(subscribe, () =>
    activeQueries(client)
      .map((query) => `${query.queryHash}:${snapshotInvalid(query)}:${query.state.fetchStatus}`)
      .join("|"),
  );
  const queries = activeQueries(client);
  const stale = queries.some((query) => snapshotInvalid(query));
  const busy = refreshing || queries.some((query) => query.state.fetchStatus === "fetching");
  const context = useMemo(
    () => ({
      register(identity: string, reset: () => void) {
        pagers.current.set(identity, reset);
        return () => {
          if (pagers.current.get(identity) === reset) pagers.current.delete(identity);
        };
      },
    }),
    [],
  );
  const refresh = async () => {
    if (running.current) return;
    running.current = true;
    setRefreshing(true);
    const currentRoute = route.current;
    try {
      await refreshBrowsingPage(client, pagers.current, () => currentRoute === route.current);
    } finally {
      running.current = false;
      setRefreshing(false);
    }
  };
  return (
    <RefreshContext.Provider value={context}>
      <BrowsingRefreshControlContext.Provider
        value={
          queries.length > 0 ? (
            <div
              className={`browsing-refresh${stale ? " is-stale" : ""}`}
              data-hint-dismissed={hintDismissed || undefined}
              onMouseEnter={() => setHintDismissed(false)}
              onFocus={() => setHintDismissed(false)}
              onKeyDown={(event) => {
                if (event.key === "Escape") setHintDismissed(true);
              }}
            >
              {stale && (
                <span className="browsing-refresh-status" role="status">
                  <span aria-hidden="true" className="browsing-refresh-dot" />
                  {t("refresh.changed")}
                </span>
              )}
              <button
                className="browsing-refresh-button"
                type="button"
                aria-label={t(busy ? "refresh.loading" : "refresh.action")}
                aria-describedby={hintID}
                aria-busy={busy}
                disabled={busy}
                onClick={() => void refresh()}
              >
                <UIIcon icon={RotateCw} />
              </button>
              <span className="browsing-refresh-hint" id={hintID} role="tooltip">
                {t(stale ? "refresh.stale" : "refresh.action")}
              </span>
            </div>
          ) : null
        }
      >
        {children}
      </BrowsingRefreshControlContext.Provider>
    </RefreshContext.Provider>
  );
}
