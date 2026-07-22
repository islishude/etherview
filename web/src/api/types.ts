import type { components } from "./schema.gen";

export type DecimalString = components["schemas"]["Quantity"];
export type HexString = `0x${string}`;
export type Address = components["schemas"]["Address"];
export type ApiMeta = components["schemas"]["Meta"];

export type CompletenessState = components["schemas"]["StageState"];
export type Finality = components["schemas"]["Finality"];
export type Completeness = components["schemas"]["Completeness"];

export interface ApiEnvelope<T, Meta = ApiMeta> {
  data: T;
  meta: Meta;
}

export type ApiErrorPayload = components["schemas"]["ErrorResponse"];

export interface CursorPage<T> {
  items: T[];
  meta: ApiMeta;
  next_cursor?: string;
}

export type ChainStatus = components["schemas"]["Status"];
export type PublicConfig = components["schemas"]["PublicConfig"];
export type BlockSummary = components["schemas"]["Block"];
export type TransactionSummary = components["schemas"]["Transaction"];
export type PendingTransaction = components["schemas"]["PendingTransaction"];
export type PendingMeta = components["schemas"]["PendingMeta"];
export interface PendingSnapshot {
  items: PendingTransaction[];
  meta: PendingMeta;
}
export type AddressSummary = components["schemas"]["AddressSummary"];
export type SearchResult = components["schemas"]["SearchResult"];
export type TokenContract = components["schemas"]["TokenContract"];
export type TokenEvent = components["schemas"]["TokenEvent"];
export type NFTOwnership = components["schemas"]["NFTOwnership"];
export type NFTBalance = components["schemas"]["NFTBalance"];
export type TraceFrame = components["schemas"]["TraceFrame"];
export type TransactionTrace = components["schemas"]["TransactionTrace"];
export type BlockStat = components["schemas"]["BlockStat"];
export type VerificationSubmission = components["schemas"]["VerificationSubmission"];
export type VerificationJob = components["schemas"]["VerificationJob"];
export type VerifiedContract = components["schemas"]["VerifiedContract"];
