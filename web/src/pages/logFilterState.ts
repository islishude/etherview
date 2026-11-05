import type { AbiParameter } from "viem";
import type { AddressLogEvent, EventIndexedField } from "@/api/heyapi/types.gen";
import type { AddressLogFilter } from "@/api/addressLogs";
import { createAbiInputTree, parseVerifiedABI, type AbiInputNode } from "@/contracts/abi";
import { encodeIndexedTopics } from "@/contracts/eventTopics";
export const topicKeys = ["topic0", "topic1", "topic2", "topic3"] as const;
export interface LogCondition {
  id: number;
  position: number;
  format: "raw" | "hash";
  hash: string;
  node?: AbiInputNode;
}
export interface LogDraft {
  mode: "name" | "topic";
  eventID: string;
  signature: string;
  event?: AddressLogEvent;
  conditions: LogCondition[];
}
export function logDraft(search: AddressLogFilter & { log_mode?: "name" | "topic" }): LogDraft {
  return {
    mode: search.log_mode ?? "name",
    eventID: search.event_id ?? "",
    signature: search.event_signature ?? "",
    conditions: topicKeys.flatMap((key, position) =>
      search[key] === undefined
        ? []
        : [{ id: position, position, format: "hash" as const, hash: search[key] }],
    ),
  };
}
export function logFilterKey(search: AddressLogFilter & { log_mode?: string }) {
  return JSON.stringify([
    search.log_mode ?? "name",
    search.event_id ?? "",
    search.event_signature ?? "",
    ...topicKeys.map((key) => search[key] ?? ""),
    search.from_block ?? "",
    search.to_block ?? "",
  ]);
}
export function eventParameter(field: EventIndexedField): AbiParameter {
  // Reuse the detached bounded ABI parser; metadata is untrusted API input.
  const abi = parseVerifiedABI([
    {
      type: "event",
      name: "Filter",
      anonymous: true,
      inputs: [
        {
          name: field.name,
          type: field.type,
          ...(field.components ? { components: field.components } : {}),
          indexed: true,
        },
      ],
    },
  ]);
  const event = abi[0];
  if (!event || event.type !== "event") throw new Error("invalid event field");
  return event.inputs[0]!;
}
export function editableEventParameter(field?: EventIndexedField): AbiParameter | undefined {
  try {
    return field ? eventParameter(field) : undefined;
  } catch {
    return undefined;
  }
}
export function newLogCondition(
  id: number,
  position: number,
  field?: EventIndexedField,
): LogCondition {
  let node: AbiInputNode | undefined;
  try {
    const parameter = editableEventParameter(field);
    if (parameter) node = createAbiInputTree([parameter])[0];
  } catch {
    // Types outside the recursive editor's limits still support exact topics.
  }
  return { id, position, hash: "", format: node ? "raw" : "hash", node };
}
export function appliedLogFilter(draft: LogDraft): AddressLogFilter {
  const filter: AddressLogFilter =
    draft.mode === "name"
      ? draft.eventID
        ? { event_id: draft.eventID }
        : { event_signature: draft.signature || undefined }
      : {};
  const parameters: AbiParameter[] = [];
  const nodes: AbiInputNode[] = [];
  const rawPositions: number[] = [];
  const seen = new Set<number>();
  for (const condition of draft.conditions) {
    if (!topicKeys[condition.position] || seen.has(condition.position))
      throw new Error("invalid topic position");
    seen.add(condition.position);
    if (condition.format === "hash") {
      const value = condition.hash.trim();
      if (!value && draft.mode === "topic") continue;
      if (!/^0x[0-9a-fA-F]{64}$/u.test(value)) throw new Error("invalid topic");
      filter[topicKeys[condition.position]!] = value.toLowerCase();
    } else {
      const field = draft.event?.indexed_fields.find(
        (item) => item.topic_index === condition.position,
      );
      if (!field || !condition.node) throw new Error("missing event definition");
      parameters.push(eventParameter(field));
      nodes.push(condition.node);
      rawPositions.push(condition.position);
    }
  }
  const encoded = encodeIndexedTopics(parameters, nodes);
  encoded.forEach((topic, i) => {
    filter[topicKeys[rawPositions[i]!]!] = topic;
  });
  return filter;
}
