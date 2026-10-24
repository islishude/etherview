import type * as schemas from "./heyapi/types.gen";

export type DecimalString = schemas.Quantity;
export type HexString = `0x${string}`;
export type Address = schemas.Address;
export type ApiMeta = schemas.Meta;

export type CompletenessState = schemas.StageState;
export type Finality = schemas.Finality;
export type Completeness = schemas.Completeness;

export interface ApiEnvelope<T, Meta = ApiMeta> {
  data: T;
  meta: Meta;
}

export type ApiErrorPayload = schemas.ErrorResponse;

export interface CursorPage<T> {
  items: T[];
  meta: ApiMeta;
  next_cursor?: string;
}

export type ChainStatus = schemas.Status;
export type PublicConfig = schemas.PublicConfig;
export type BlockSummary = schemas.Block;
export type BlockWithdrawal = schemas.BlockWithdrawal;
export type AddressWithdrawal = schemas.AddressWithdrawal;
export type TransactionSummary = schemas.Transaction;
export type TransactionDetail = schemas.TransactionDetail;
export type UserOperationSummary = schemas.UserOperationSummary;
export type UserOperationDetail = schemas.UserOperationDetail;
export type UserOperationProtocolEvent = schemas.UserOperationProtocolEvent;
export type TransactionCalldata = schemas.TransactionCalldata;
export type TransactionFailure = schemas.TransactionFailure;
export type HomeSnapshotResponse = schemas.HomeSnapshotResponse;
export type PendingTransaction = schemas.PendingTransaction;
export type PendingMeta = schemas.PendingMeta;
export interface PendingSnapshot {
  items: PendingTransaction[];
  meta: PendingMeta;
}
export type AddressSummary = schemas.AddressSummary;
export type AddressInternalTransaction = schemas.AddressInternalTransaction;
export type AddressTokenTransfer = schemas.AddressTokenTransfer;
export type GenesisAccount = schemas.GenesisAccount;
export type SearchResult = schemas.SearchResult;
export type TokenContract = schemas.TokenContract;
export type TokenEvent = schemas.TokenEvent;
export type TokenHolder = schemas.TokenHolder;
export type TokenHolderMeta = schemas.TokenHolderMeta;
export interface TokenHolderPage {
  items: TokenHolder[];
  meta: TokenHolderMeta;
  next_cursor?: string;
}
export type NFTOwnership = schemas.NftOwnership;
export type NFTMetadata = schemas.NftMetadata;
export type NFTBalance = schemas.NftBalance;
export type ERC20Balance = schemas.Erc20Balance;
export type TraceFrame = schemas.TraceFrame;
export type TransactionTrace = schemas.TransactionTrace;
export type TransactionInternalTransaction = schemas.TransactionInternalTransaction;
export type TransactionInternalTransactions = schemas.TransactionInternalTransactions;
export type TransactionTokenTransfers = schemas.TransactionTokenTransfers;
export type TransactionLogs = schemas.TransactionLogs;
export type TransactionLog = schemas.TransactionLog;
export type TransactionStateChanges = schemas.TransactionStateChanges;
export type TransactionStateChange = schemas.TransactionStateChange;
export type BlockStat = schemas.BlockStat;
export type AggregateStats = schemas.AggregateStats;
export type ChartMetric = schemas.ChartMetric;
export type ChartInterval = schemas.ChartInterval;
export type ChartPoint = schemas.ChartPoint;
export type ChartPreview = schemas.ChartPreview;
export type ChartOverview = schemas.ChartOverview;
export type ChartMetricSeries = schemas.ChartMetricSeries;
export type VerificationSubmission = schemas.AddressVerificationSubmission;
export type VerificationJob = schemas.VerificationJob;
export type VerificationOutcome = schemas.VerificationOutcome;
export type VerificationSuccess = schemas.VerificationSuccess;
export type VerificationMatchDetails = schemas.VerificationMatchDetails;
export type CompilerCatalog = schemas.CompilerCatalog;
export type VerifiedContract = schemas.VerifiedContract;
