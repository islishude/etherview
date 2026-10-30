import type { QueryClient } from "@tanstack/react-query";

export type ChainEventType = "head" | "reorg" | "status" | "names";
export interface ChainQueryMeta extends Record<string, unknown> {
  refreshPolicy?: "live" | "manual";
  pagination?: string;
  chainEvents?: readonly ChainEventType[];
}

declare module "@tanstack/react-query" {
  interface Register {
    queryMeta: ChainQueryMeta;
  }
}

export const chainQueryMeta: ChainQueryMeta = { chainEvents: ["head", "reorg"] };
export const statusQueryMeta: ChainQueryMeta = { chainEvents: ["head", "reorg", "status"] };
export const userOperationQueryMeta: ChainQueryMeta = {
  ...statusQueryMeta,
};
const observedEvents = new WeakMap<QueryClient, string>();

export function validEventID(value: string): boolean {
  return /^(0|[1-9][0-9]{0,18})$/.test(value) && BigInt(value) <= 9223372036854775807n;
}

export function eventWatermark(client: QueryClient): string {
  return observedEvents.get(client) ?? "0";
}

export function observeEvent(client: QueryClient, value: string): void {
  if (validEventID(value) && BigInt(value) > BigInt(eventWatermark(client))) {
    observedEvents.set(client, value);
  }
}
