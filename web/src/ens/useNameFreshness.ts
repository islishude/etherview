import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { subscribeNames } from "./nameEvents";
import { freshNameDetail, nameDeadlines } from "./nameDetails";
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
        .flatMap((item) => nameDeadlines(item.name_details))
        .filter((expiry) => expiry > now),
    );
    if (!Number.isFinite(expiry)) return;
    const timer = setTimeout(
      () => setNow(Date.now()),
      Math.min(Math.max(1, expiry - Date.now()), 2_147_483_647),
    );
    return () => clearTimeout(timer);
  }, [items, now]);
  return {
    items: items?.map((item) =>
      item.kind === "name" && item.name_details
        ? { ...item, name_details: freshNameDetail(item.name_details, now, invalid) }
        : item,
    ),
    withdrawn: invalid,
  };
}
