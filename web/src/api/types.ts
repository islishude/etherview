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
export type BlockWithdrawal = components["schemas"]["BlockWithdrawal"];
export type TransactionSummary = components["schemas"]["Transaction"];
export type TransactionDetail = components["schemas"]["TransactionDetail"];
export type TransactionCalldata = components["schemas"]["TransactionCalldata"];
export type HomeSnapshotResponse = components["schemas"]["HomeSnapshotResponse"];
export type PendingTransaction = components["schemas"]["PendingTransaction"];
export type PendingMeta = components["schemas"]["PendingMeta"];
export interface PendingSnapshot {
  items: PendingTransaction[];
  meta: PendingMeta;
}
export type AddressSummary = components["schemas"]["AddressSummary"];
export type AddressInternalTransaction =
  components["schemas"]["AddressInternalTransaction"];
export type AddressTokenTransfer = components["schemas"]["AddressTokenTransfer"];
export type GenesisAccount = components["schemas"]["GenesisAccount"];
export type SearchResult = components["schemas"]["SearchResult"];
export type TokenContract = components["schemas"]["TokenContract"];
export type TokenEvent = components["schemas"]["TokenEvent"];
export type NFTOwnership = components["schemas"]["NFTOwnership"];
export type NFTBalance = components["schemas"]["NFTBalance"];
export type ERC20Balance = components["schemas"]["ERC20Balance"];
export type TraceFrame = components["schemas"]["TraceFrame"];
export type TransactionTrace = components["schemas"]["TransactionTrace"];
export type TransactionInternalTransaction =
  components["schemas"]["TransactionInternalTransaction"];
export type TransactionInternalTransactions =
  components["schemas"]["TransactionInternalTransactions"];
export type TransactionTokenTransfers = components["schemas"]["TransactionTokenTransfers"];
export type TransactionLogs = components["schemas"]["TransactionLogs"];
export type TransactionLog = components["schemas"]["TransactionLog"];
export type TransactionStateChanges = components["schemas"]["TransactionStateChanges"];
export type TransactionStateChange = components["schemas"]["TransactionStateChange"];
export type BlockStat = components["schemas"]["BlockStat"];
export type AggregateStats = components["schemas"]["AggregateStats"];
export type ChartMetric = components["schemas"]["ChartMetric"];
export type ChartInterval = components["schemas"]["ChartInterval"];
export type ChartPoint = components["schemas"]["ChartPoint"];
export type ChartPreview = components["schemas"]["ChartPreview"];
export type ChartOverview = components["schemas"]["ChartOverview"];
export type ChartMetricSeries = components["schemas"]["ChartMetricSeries"];
export type VerificationSubmission = components["schemas"]["AddressVerificationSubmission"];
export type VerificationJob = components["schemas"]["VerificationJob"];
export type VerificationOutcome = components["schemas"]["VerificationOutcome"];
export type VerificationSuccess = components["schemas"]["VerificationSuccess"];
export type VerificationMatchDetails = components["schemas"]["VerificationMatchDetails"];
export type CompilerCatalog = components["schemas"]["CompilerCatalog"];
export type VerifiedContract = components["schemas"]["VerifiedContract"];
