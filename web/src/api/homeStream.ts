import { useQuery } from "@tanstack/react-query";

import { apiClient, requireEnvelope } from "./client";
import type {
  BlockSummary,
  ChainStatus,
  HomeSnapshotResponse,
  TransactionSummary,
} from "./types";

const MAX_HOME_SNAPSHOT_BYTES = 2 * 1024 * 1024;
const quantityPattern = /^(0|[1-9][0-9]*)$/;
const hashPattern = /^0x[0-9a-fA-F]{64}$/;
const addressPattern = /^0x[0-9a-fA-F]{40}$/;
const inputPattern = /^0x[0-9a-fA-F]*$/;
const dateTimePattern =
  /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/;
const stageStates = new Set(["complete", "pending", "unavailable", "failed"]);
const finalityStates = new Set(["pending", "latest", "safe", "finalized", "orphan"]);
const transactionStates = new Set(["pending", "success", "failed", "unknown"]);

export interface HomeStreamSnapshot {
  status: ChainStatus & {
    coverage_start?: string;
    coverage_end?: string;
  };
  blocks: BlockSummary[];
  transactions: TransactionSummary[];
}

export interface HomeStreamState {
  data?: HomeStreamSnapshot;
  error?: Error;
  isPending: boolean;
}

export function useHomeSnapshot(): HomeStreamState {
  const query = useQuery({
    queryKey: ["home"],
    queryFn: async () => {
      const envelope = requireEnvelope(await apiClient.GET("/home"));
      const response = parseHomeSnapshot(JSON.stringify(envelope));
      return {
        status: {
          ...response.data.status,
          coverage_start: response.meta.coverage_start,
          coverage_end: response.meta.coverage_end,
        },
        blocks: response.data.blocks,
        transactions: response.data.transactions,
      };
    },
    retry: false,
    staleTime: Number.POSITIVE_INFINITY,
  });
  return {
    data: query.data,
    error: query.error ?? undefined,
    isPending: query.isPending,
  };
}

export function parseHomeSnapshot(raw: string): HomeSnapshotResponse {
  if (typeof raw !== "string" ||
      new TextEncoder().encode(raw).byteLength > MAX_HOME_SNAPSHOT_BYTES) {
    throw new Error("Home snapshot exceeds its size limit");
  }
  const parsed: unknown = JSON.parse(raw);
  const response = objectWithKeys(parsed, ["data", "meta"], ["data", "meta"]);
  const data = objectWithKeys(
    response.data,
    ["status", "blocks", "transactions"],
    ["status", "blocks", "transactions"],
  );
  validateStatus(data.status);
  validateArray(data.blocks, 6, validateBlock, "blocks");
  validateArray(data.transactions, 6, validateTransaction, "transactions");
  validateMeta(response.meta);
  const typed = parsed as HomeSnapshotResponse;
  if (typed.data.status.chain_id !== typed.meta.chain_id) {
    throw new Error("Home snapshot chain identity differs");
  }
  return typed;
}

function validateStatus(value: unknown): void {
  const record = objectWithKeys(
    value,
    [
      "chain_id", "core_ready", "latest_block", "indexed_block",
      "highest_covered_block", "backfill_complete", "safe_block",
      "finalized_block", "lag", "completeness",
    ],
    [
      "chain_id", "core_ready", "latest_block", "indexed_block",
      "backfill_complete", "lag", "completeness",
    ],
  );
  quantity(record.chain_id, "status.chain_id");
  boolean(record.core_ready, "status.core_ready");
  quantity(record.latest_block, "status.latest_block");
  quantity(record.indexed_block, "status.indexed_block");
  optional(record, "highest_covered_block", quantity);
  boolean(record.backfill_complete, "status.backfill_complete");
  optional(record, "safe_block", quantity);
  optional(record, "finalized_block", quantity);
  quantity(record.lag, "status.lag");
  validateCompleteness(record.completeness);
}

function validateBlock(value: unknown): void {
  const record = objectWithKeys(
    value,
    [
      "hash", "number", "parent_hash", "timestamp", "miner",
      "transaction_count", "gas_used", "gas_limit", "base_fee_per_gas",
      "withdrawals", "canonical", "finality", "completeness",
    ],
    [
      "hash", "number", "parent_hash", "timestamp", "transaction_count",
      "canonical", "finality", "completeness",
    ],
  );
  hash(record.hash, "block.hash");
  quantity(record.number, "block.number");
  hash(record.parent_hash, "block.parent_hash");
  timestamp(record.timestamp, "block.timestamp");
  optional(record, "miner", address);
  nonNegativeInteger(record.transaction_count, "block.transaction_count");
  optional(record, "gas_used", quantity);
  optional(record, "gas_limit", quantity);
  optional(record, "base_fee_per_gas", quantity);
  optional(record, "withdrawals", validateWithdrawals);
  boolean(record.canonical, "block.canonical");
  enumeration(record.finality, finalityStates, "block.finality");
  validateCompleteness(record.completeness);
}

function validateTransaction(value: unknown): void {
  const record = objectWithKeys(
    value,
    [
      "hash", "block_timestamp", "confirmations", "block_hash", "block_number",
      "transaction_index", "from", "to", "contract_address", "nonce", "value",
      "gas", "gas_used", "base_fee_per_gas", "blob_base_fee_per_gas",
      "effective_gas_price", "tx_fee_wei", "gas_price",
      "max_fee_per_gas", "max_priority_fee_per_gas", "max_fee_per_blob_gas",
      "access_list", "blob_versioned_hashes", "burned_wei", "type", "input",
      "status", "canonical", "finality", "completeness",
    ],
    ["hash", "from", "nonce", "value", "gas", "input", "canonical", "finality", "completeness"],
  );
  hash(record.hash, "transaction.hash");
  optional(record, "block_timestamp", timestamp);
  optional(record, "confirmations", quantity);
  optional(record, "block_hash", hash);
  optional(record, "block_number", quantity);
  optional(record, "transaction_index", nonNegativeInteger);
  address(record.from, "transaction.from");
  if (record.to !== undefined && record.to !== null) {
    address(record.to, "transaction.to");
  }
  optional(record, "contract_address", address);
  quantity(record.nonce, "transaction.nonce");
  quantity(record.value, "transaction.value");
  quantity(record.gas, "transaction.gas");
  for (const key of [
    "gas_used", "base_fee_per_gas", "blob_base_fee_per_gas",
    "effective_gas_price", "tx_fee_wei", "gas_price",
    "max_fee_per_gas", "max_priority_fee_per_gas", "max_fee_per_blob_gas",
    "burned_wei",
  ] as const) {
    optional(record, key, quantity);
  }
  optional(record, "access_list", validateAccessList);
  optional(record, "blob_versioned_hashes", validateHashes);
  optional(record, "type", stringValue);
  stringPattern(record.input, inputPattern, "transaction.input");
  if (record.status !== undefined) {
    enumeration(record.status, transactionStates, "transaction.status");
  }
  boolean(record.canonical, "transaction.canonical");
  enumeration(record.finality, finalityStates, "transaction.finality");
  validateCompleteness(record.completeness);
}

function validateWithdrawals(value: unknown, label: string): void {
  validateList(value, (item) => {
    const record = objectWithKeys(
      item,
      ["index", "validator_index", "address", "amount"],
      ["index", "validator_index", "address", "amount"],
    );
    quantity(record.index, `${label}.index`);
    quantity(record.validator_index, `${label}.validator_index`);
    address(record.address, `${label}.address`);
    quantity(record.amount, `${label}.amount`);
  }, label);
}

function validateAccessList(value: unknown, label: string): void {
  validateList(value, (item) => {
    const record = objectWithKeys(
      item,
      ["address", "storage_keys"],
      ["address", "storage_keys"],
    );
    address(record.address, `${label}.address`);
    validateHashes(record.storage_keys, `${label}.storage_keys`);
  }, label);
}

function validateHashes(value: unknown, label: string): void {
  validateList(value, (item) => hash(item, label), label);
}

function validateCompleteness(value: unknown): void {
  const record = objectWithKeys(
    value,
    ["core", "trace", "metadata", "state"],
    ["core", "trace", "metadata", "state"],
  );
  for (const key of ["core", "trace", "metadata", "state"] as const) {
    enumeration(record[key], stageStates, `completeness.${key}`);
  }
}

function validateMeta(value: unknown): void {
  const record = objectWithKeys(
    value,
    ["request_id", "chain_id", "coverage_start", "coverage_end"],
    ["request_id", "chain_id", "coverage_start", "coverage_end"],
  );
  stringValue(record.request_id, "meta.request_id");
  quantity(record.chain_id, "meta.chain_id");
  quantity(record.coverage_start, "meta.coverage_start");
  quantity(record.coverage_end, "meta.coverage_end");
}

function validateArray(
  value: unknown,
  maximum: number,
  validate: (item: unknown) => void,
  label: string,
): void {
  if (!Array.isArray(value) || value.length > maximum) {
    throw new Error(`Invalid ${label}`);
  }
  value.forEach(validate);
}

function validateList(
  value: unknown,
  validate: (item: unknown) => void,
  label: string,
): void {
  if (!Array.isArray(value)) {
    throw new Error(`Invalid ${label}`);
  }
  value.forEach(validate);
}

function objectWithKeys(
  value: unknown,
  allowed: readonly string[],
  required: readonly string[],
): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error("Expected object");
  }
  const record = value as Record<string, unknown>;
  const keys = Object.keys(record);
  if (keys.some((key) => !allowed.includes(key)) ||
      required.some((key) => !Object.hasOwn(record, key))) {
    throw new Error("Object shape is invalid");
  }
  return record;
}

function optional(
  record: Record<string, unknown>,
  key: string,
  validate: (value: unknown, label: string) => void,
): void {
  if (record[key] !== undefined) {
    validate(record[key], key);
  }
}

function quantity(value: unknown, label: string): void {
  stringPattern(value, quantityPattern, label);
}

function hash(value: unknown, label: string): void {
  stringPattern(value, hashPattern, label);
}

function address(value: unknown, label: string): void {
  stringPattern(value, addressPattern, label);
}

function timestamp(value: unknown, label: string): void {
  stringValue(value, label);
  if (!dateTimePattern.test(value as string) || Number.isNaN(Date.parse(value as string))) {
    throw new Error(`Invalid ${label}`);
  }
}

function stringValue(value: unknown, label: string): void {
  if (typeof value !== "string") {
    throw new Error(`Invalid ${label}`);
  }
}

function stringPattern(value: unknown, pattern: RegExp, label: string): void {
  if (typeof value !== "string" || !pattern.test(value)) {
    throw new Error(`Invalid ${label}`);
  }
}

function boolean(value: unknown, label: string): void {
  if (typeof value !== "boolean") {
    throw new Error(`Invalid ${label}`);
  }
}

function nonNegativeInteger(value: unknown, label: string): void {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0) {
    throw new Error(`Invalid ${label}`);
  }
}

function enumeration(value: unknown, allowed: Set<string>, label: string): void {
  if (typeof value !== "string" || !allowed.has(value)) {
    throw new Error(`Invalid ${label}`);
  }
}
