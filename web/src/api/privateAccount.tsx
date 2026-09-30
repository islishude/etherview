import { useCallback, useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useAuth } from "@/auth/AuthProvider";
import { listNotifications, listWatches } from "./watchlist";

export function usePrivateIdentity() {
  const auth = useAuth();
  return auth.session.authenticated && auth.session.user
    ? `${auth.session.user.id}:${auth.session.expires_at ?? ""}`
    : "";
}
export function PrivateAccountCleanup() {
  const identity = usePrivateIdentity();
  const client = useQueryClient();
  useEffect(
    () => () => {
      if (!identity) return;
      const filters = { queryKey: ["private-account", identity] };
      void client.cancelQueries(filters);
      client.removeQueries(filters);
    },
    [client, identity],
  );
  return null;
}
export function usePrivateLifetime() {
  const controller = useRef(new AbortController());
  useEffect(() => {
    if (controller.current.signal.aborted) controller.current = new AbortController();
    const current = controller.current;
    return () => current.abort();
  }, []);
  return useCallback(() => controller.current.signal, []);
}
export function useWatches() {
  const identity = usePrivateIdentity();
  return useQuery({
    queryKey: ["private-account", identity, "watches"],
    queryFn: ({ signal }) => listWatches(signal),
    enabled: Boolean(identity),
    retry: false,
  });
}
export function useNotifications(cursor = "", unreadOnly = false) {
  const identity = usePrivateIdentity();
  const [visible, setVisible] = useState(() => document.visibilityState !== "hidden");
  useEffect(() => {
    const listener = () => setVisible(document.visibilityState !== "hidden");
    document.addEventListener("visibilitychange", listener);
    return () => document.removeEventListener("visibilitychange", listener);
  }, []);
  return useQuery({
    queryKey: ["private-account", identity, "notifications", cursor, unreadOnly],
    queryFn: ({ signal }) => listNotifications(cursor, unreadOnly, signal),
    enabled: Boolean(identity) && visible,
    refetchInterval: visible && identity ? 15000 : false,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: true,
    retry: false,
  });
}
