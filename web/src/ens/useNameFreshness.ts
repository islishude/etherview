import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { subscribeNames } from "./nameEvents";
import type { SearchResult } from "@/api/heyapi/types.gen";

export function useNameFreshness(items: SearchResult[] | undefined) {
  const client = useQueryClient();
  const [invalid, setInvalid] = useState(false);
  const [now, setNow] = useState(Date.now);
  useEffect(() => subscribeNames(client, () => setInvalid(true)), [client]);
  useEffect(() => {
    setInvalid(false);
    setNow(Date.now());
  }, [items]);
  useEffect(() => {
    const expiry = Math.min(
      ...(items ?? [])
        .filter((item) => item.name_source)
        .map((item) => Date.parse(item.name_valid_until ?? ""))
        .filter((expiry) => Number.isFinite(expiry) && expiry > now),
    );
    if (!Number.isFinite(expiry) || expiry <= now) return;
    const timer = setTimeout(
      () => setNow(Date.now()),
      Math.min(expiry - Date.now(), 2_147_483_647),
    );
    return () => clearTimeout(timer);
  }, [items, now]);
  const current = (item: SearchResult) =>
    !item.name_source || (!invalid && Date.parse(item.name_valid_until ?? "") > now);
  return {
    items: items?.filter(current),
    withdrawn: items?.some((item) => !current(item)) ?? false,
  };
}
