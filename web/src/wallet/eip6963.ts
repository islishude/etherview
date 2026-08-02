export interface EIP1193RequestArguments {
  method: string;
  params?: readonly unknown[] | Record<string, unknown>;
}

export type EIP1193Event = "accountsChanged" | "chainChanged" | "disconnect";

export interface EIP1193Provider {
  request<T = unknown>(args: EIP1193RequestArguments): Promise<T>;
  on(event: EIP1193Event, listener: (value: unknown) => void): void;
  removeListener(event: EIP1193Event, listener: (value: unknown) => void): void;
}

export interface EIP6963ProviderInfo {
  uuid: string;
  name: string;
  icon: string;
  rdns: string;
}

export interface EIP6963ProviderDetail {
  info: EIP6963ProviderInfo;
  provider: EIP1193Provider;
}

export const EIP6963_ANNOUNCE_EVENT = "eip6963:announceProvider";
export const EIP6963_REQUEST_EVENT = "eip6963:requestProvider";

export const MAX_CONTRACT_CALLDATA_BYTES = 128 * 1024;
export const MAX_CONTRACT_RESULT_BYTES = 1024 * 1024;
export const MAX_REVERT_DATA_BYTES = 128 * 1024;
export const MAX_SIGN_MESSAGE_BYTES = 4096;

const UUID_V4_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u;
const HEX_DATA_PATTERN = /^0x(?:[0-9a-f]{2})*$/iu;
const HEX_QUANTITY_PATTERN = /^0x(?:0|[1-9a-f][0-9a-f]*)$/iu;
const TRANSACTION_HASH_PATTERN = /^0x[0-9a-f]{64}$/iu;
const WALLET_SIGNATURE_PATTERN = /^0x[0-9a-f]{130}$/iu;
const MAX_UINT256 = (1n << 256n) - 1n;
const MAX_ADD_CHAIN_URLS = 5;
const MAX_ADD_CHAIN_URL_LENGTH = 2048;

export interface AddEthereumChainParameter {
  chainId: `0x${string}`;
  chainName: string;
  nativeCurrency: {
    name: string;
    symbol: string;
    decimals: number;
  };
  rpcUrls: string[];
  blockExplorerUrls?: string[];
  iconUrls?: string[];
}

export function buildAddEthereumChainParameter(value: unknown): AddEthereumChainParameter {
  if (typeof value !== "object" || value === null) {
    throw new WalletBoundaryError("CHAIN_UNAVAILABLE");
  }
  const candidate = value as Record<string, unknown>;
  const chainID = normalizeChainID(candidate.chain_id);
  const chainName = candidate.chain_name;
  const nativeCurrency = candidate.native_currency;
  if (
    chainID === undefined ||
    chainID === "0" ||
    typeof chainName !== "string" ||
    chainName.length < 1 ||
    chainName.length > 128 ||
    typeof nativeCurrency !== "object" ||
    nativeCurrency === null
  ) {
    throw new WalletBoundaryError("CHAIN_UNAVAILABLE");
  }
  const native = nativeCurrency as Record<string, unknown>;
  if (
    typeof native.name !== "string" ||
    native.name.length < 1 ||
    native.name.length > 64 ||
    typeof native.symbol !== "string" ||
    !/^[\x21-\x7e]{2,6}$/u.test(native.symbol) ||
    typeof native.decimals !== "number" ||
    !Number.isInteger(native.decimals) ||
    native.decimals < 0 ||
    native.decimals > 255
  ) {
    throw new WalletBoundaryError("CHAIN_UNAVAILABLE");
  }
  const rpcUrls = parseAddChainURLs(candidate.rpc_urls, true);
  const blockExplorerUrls = parseAddChainURLs(candidate.block_explorer_urls, false);
  const iconUrls = parseAddChainURLs(candidate.icon_urls, false);
  return {
    chainId: `0x${BigInt(chainID).toString(16)}`,
    chainName,
    nativeCurrency: {
      name: native.name,
      symbol: native.symbol,
      decimals: native.decimals,
    },
    rpcUrls,
    ...(blockExplorerUrls ? { blockExplorerUrls } : {}),
    ...(iconUrls ? { iconUrls } : {}),
  };
}

function parseAddChainURLs(value: unknown, required: true): string[];
function parseAddChainURLs(value: unknown, required: false): string[] | undefined;
function parseAddChainURLs(value: unknown, required: boolean): string[] | undefined {
  if (value === undefined && !required) return undefined;
  if (!Array.isArray(value) || value.length < 1 || value.length > MAX_ADD_CHAIN_URLS) {
    throw new WalletBoundaryError("CHAIN_UNAVAILABLE");
  }
  const urls: string[] = [];
  for (const item of value) {
    if (typeof item !== "string" || item.length < 1 || item.length > MAX_ADD_CHAIN_URL_LENGTH) {
      throw new WalletBoundaryError("CHAIN_UNAVAILABLE");
    }
    try {
      const parsed = new URL(item);
      const localPreviewRPC =
        required && parsed.protocol === "http:" && parsed.hostname === "localhost";
      if (
        (parsed.protocol !== "https:" && !localPreviewRPC) ||
        !parsed.hostname ||
        parsed.username ||
        parsed.password ||
        parsed.search ||
        parsed.hash
      ) {
        throw new WalletBoundaryError("CHAIN_UNAVAILABLE");
      }
    } catch (cause) {
      if (cause instanceof WalletBoundaryError) throw cause;
      throw new WalletBoundaryError("CHAIN_UNAVAILABLE");
    }
    urls.push(item);
  }
  return urls;
}

export function snapshotProviderDetail(value: unknown): EIP6963ProviderDetail | undefined {
  try {
    if (
      typeof value !== "object" ||
      value === null ||
      !("info" in value) ||
      !("provider" in value)
    ) {
      return undefined;
    }

    const info = value.info;
    const provider = value.provider;
    if (
      typeof info !== "object" ||
      info === null ||
      !("uuid" in info) ||
      !("name" in info) ||
      !("icon" in info) ||
      !("rdns" in info) ||
      typeof provider !== "object" ||
      provider === null ||
      !("request" in provider) ||
      !("on" in provider) ||
      !("removeListener" in provider)
    ) {
      return undefined;
    }

    const uuid = info.uuid;
    const name = info.name;
    const icon = info.icon;
    const rdns = info.rdns;
    const request = provider.request;
    const on = provider.on;
    const removeListener = provider.removeListener;
    if (
      typeof uuid !== "string" ||
      typeof name !== "string" ||
      typeof icon !== "string" ||
      typeof rdns !== "string" ||
      !isProviderUUID(uuid) ||
      !isProviderName(name) ||
      !isProviderIcon(icon) ||
      !isProviderRDNS(rdns) ||
      typeof request !== "function" ||
      typeof on !== "function" ||
      typeof removeListener !== "function"
    ) {
      return undefined;
    }

    const snapshotInfo = Object.freeze({
      uuid: uuid.toLowerCase(),
      name,
      icon,
      rdns,
    });
    const snapshotProvider: EIP1193Provider = Object.freeze({
      async request<T = unknown>(arguments_: EIP1193RequestArguments): Promise<T> {
        return await request.call(provider, arguments_) as T;
      },
      on(event: EIP1193Event, listener: (value: unknown) => void) {
        on.call(provider, event, listener);
      },
      removeListener(event: EIP1193Event, listener: (value: unknown) => void) {
        removeListener.call(provider, event, listener);
      },
    });
    return Object.freeze({
      info: snapshotInfo,
      provider: snapshotProvider,
    });
  } catch {
    return undefined;
  }
}

export function isProviderDetail(value: unknown): value is EIP6963ProviderDetail {
  return snapshotProviderDetail(value) !== undefined;
}

export function normalizeChainID(chainID: unknown): string | undefined {
  if (typeof chainID === "number") {
    if (!Number.isSafeInteger(chainID) || chainID < 0) return undefined;
    return BigInt(chainID).toString(10);
  }
  if (typeof chainID === "bigint") {
    return chainID < 0n || chainID > MAX_UINT256 ? undefined : chainID.toString(10);
  }
  if (
    typeof chainID !== "string" ||
    chainID.length > 78 ||
    !/^(?:0x[0-9a-f]+|[0-9]+)$/iu.test(chainID)
  ) {
    return undefined;
  }

  try {
    const normalized = BigInt(chainID);
    return normalized <= MAX_UINT256 ? normalized.toString(10) : undefined;
  } catch {
    return undefined;
  }
}

export function normalizeProviderChainID(chainID: unknown): string | undefined {
  if (
    typeof chainID !== "string" ||
    chainID.length > 66 ||
    !HEX_QUANTITY_PATTERN.test(chainID)
  ) {
    return undefined;
  }
  return normalizeChainID(chainID);
}

export function chainsMatch(actual: unknown, expected: unknown): boolean {
  const actualID = normalizeChainID(actual);
  const expectedID = normalizeChainID(expected);
  return actualID !== undefined && expectedID !== undefined && actualID === expectedID;
}

export type WalletBoundaryErrorCode =
  | "NOT_CONNECTED"
  | "CHAIN_UNAVAILABLE"
  | "CHAIN_MISMATCH"
  | "ACCOUNT_CHANGED"
  | "SESSION_CHANGED"
  | "TRANSACTION_OUTCOME_UNKNOWN"
  | "PROVIDER_DISCONNECTED"
  | "INVALID_PROVIDER_RESPONSE"
  | "INVALID_REQUEST"
  | "USER_REJECTED"
  | "REQUEST_FAILED";

const WALLET_ERROR_MESSAGES: Record<WalletBoundaryErrorCode, string> = {
  NOT_CONNECTED: "A connected wallet account is required",
  CHAIN_UNAVAILABLE: "Explorer chain configuration is unavailable",
  CHAIN_MISMATCH: "Injected wallet is connected to another chain",
  ACCOUNT_CHANGED: "The active wallet account changed",
  SESSION_CHANGED: "The active wallet session changed",
  TRANSACTION_OUTCOME_UNKNOWN: "The transaction outcome is unknown",
  PROVIDER_DISCONNECTED: "The injected wallet disconnected",
  INVALID_PROVIDER_RESPONSE: "The injected wallet returned an invalid response",
  INVALID_REQUEST: "The contract request is invalid",
  USER_REJECTED: "The wallet request was rejected",
  REQUEST_FAILED: "The wallet request failed",
};

export class WalletBoundaryError extends Error {
  readonly code: WalletBoundaryErrorCode;
  readonly revertData?: `0x${string}`;

  constructor(code: WalletBoundaryErrorCode, revertData?: `0x${string}`) {
    super(WALLET_ERROR_MESSAGES[code]);
    this.name = "WalletBoundaryError";
    this.code = code;
    this.revertData = revertData;
  }
}

export function toWalletBoundaryError(cause: unknown): WalletBoundaryError {
  if (cause instanceof WalletBoundaryError) return cause;

  const providerCode = providerErrorCode(cause);
  const revertData = providerRevertData(cause);
  switch (providerCode) {
    case 4001:
      return new WalletBoundaryError("USER_REJECTED", revertData);
    case 4100:
      return new WalletBoundaryError("NOT_CONNECTED", revertData);
    case 4900:
      return new WalletBoundaryError("PROVIDER_DISCONNECTED", revertData);
    case 4901:
      return new WalletBoundaryError("CHAIN_MISMATCH", revertData);
    default:
      return new WalletBoundaryError("REQUEST_FAILED", revertData);
  }
}

export function walletErrorTranslationKey(
  code: WalletBoundaryErrorCode,
): `wallet.errors.${string}` {
  switch (code) {
    case "NOT_CONNECTED":
      return "wallet.errors.notConnected";
    case "CHAIN_UNAVAILABLE":
      return "wallet.errors.chainUnavailable";
    case "CHAIN_MISMATCH":
      return "wallet.errors.chainMismatch";
    case "ACCOUNT_CHANGED":
      return "wallet.errors.accountChanged";
    case "SESSION_CHANGED":
      return "wallet.errors.sessionChanged";
    case "TRANSACTION_OUTCOME_UNKNOWN":
      return "wallet.errors.transactionOutcomeUnknown";
    case "PROVIDER_DISCONNECTED":
      return "wallet.errors.disconnected";
    case "INVALID_PROVIDER_RESPONSE":
      return "wallet.errors.invalidResponse";
    case "INVALID_REQUEST":
      return "wallet.errors.invalidRequest";
    case "USER_REJECTED":
      return "wallet.errors.userRejected";
    case "REQUEST_FAILED":
      return "wallet.errors.requestFailed";
  }
}

export function assertWalletChain(actual: unknown, expected: unknown): string {
  if (normalizeChainID(expected) === undefined) {
    throw new WalletBoundaryError("CHAIN_UNAVAILABLE");
  }
  const actualID = normalizeProviderChainID(actual);
  if (actualID === undefined) {
    throw new WalletBoundaryError("INVALID_PROVIDER_RESPONSE");
  }
  if (!chainsMatch(actualID, expected)) {
    throw new WalletBoundaryError("CHAIN_MISMATCH");
  }
  return actualID;
}

export function isContractCalldata(value: unknown): value is `0x${string}` {
  return (
    typeof value === "string" &&
    value.length <= MAX_CONTRACT_CALLDATA_BYTES * 2 + 2 &&
    HEX_DATA_PATTERN.test(value) &&
    (value.length - 2) / 2 <= MAX_CONTRACT_CALLDATA_BYTES
  );
}

export function isContractResult(value: unknown): value is `0x${string}` {
  return (
    typeof value === "string" &&
    value.length <= MAX_CONTRACT_RESULT_BYTES * 2 + 2 &&
    HEX_DATA_PATTERN.test(value) &&
    (value.length - 2) / 2 <= MAX_CONTRACT_RESULT_BYTES
  );
}

export function isTransactionHash(value: unknown): value is `0x${string}` {
  return (
    typeof value === "string" &&
    value.length === 66 &&
    TRANSACTION_HASH_PATTERN.test(value)
  );
}

export function isWalletSignature(value: unknown): value is `0x${string}` {
  return (
    typeof value === "string" &&
    value.length === 132 &&
    WALLET_SIGNATURE_PATTERN.test(value)
  );
}

export function isUint256Quantity(value: unknown): value is `0x${string}` {
  if (
    typeof value !== "string" ||
    value.length > 66 ||
    !HEX_QUANTITY_PATTERN.test(value)
  ) {
    return false;
  }
  try {
    return BigInt(value) <= MAX_UINT256;
  } catch {
    return false;
  }
}

function isProviderUUID(value: string): boolean {
  return value.length === 36 && UUID_V4_PATTERN.test(value.toLowerCase());
}

function isProviderName(value: string): boolean {
  return (
    value.length > 0 &&
    value.length <= 128 &&
    value === value.trim() &&
    !/[\u0000-\u001f\u007f]/u.test(value)
  );
}

function isProviderIcon(value: string): boolean {
  const normalized = value.toLowerCase();
  return (
    value.length > 0 &&
    value.length <= 32 * 1024 &&
    /^data:image\/[^,;]+(?:;[^,]*)?,/u.test(normalized)
  );
}

function isProviderRDNS(value: string): boolean {
  if (value.length === 0 || value.length > 253) return false;
  const labels = value.toLowerCase().split(".");
  return (
    labels.length >= 2 &&
    labels.every(
      (label) =>
        label.length > 0 &&
        label.length <= 63 &&
        /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/u.test(label),
    )
  );
}

function providerErrorCode(cause: unknown): number | undefined {
  try {
    if (
      typeof cause === "object" &&
      cause !== null &&
      "code" in cause &&
      typeof cause.code === "number" &&
      Number.isInteger(cause.code)
    ) {
      return cause.code;
    }
  } catch {
    return undefined;
  }
  return undefined;
}

function providerRevertData(cause: unknown): `0x${string}` | undefined {
  const visited = new Set<object>();
  const inspect = (value: unknown, depth: number): `0x${string}` | undefined => {
    if (depth > 4) return undefined;
    if (
      typeof value === "string" &&
      value.length <= MAX_REVERT_DATA_BYTES * 2 + 2 &&
      HEX_DATA_PATTERN.test(value)
    ) {
      return value as `0x${string}`;
    }
    if (typeof value !== "object" || value === null || visited.has(value)) {
      return undefined;
    }
    visited.add(value);
    try {
      const record = value as Record<string, unknown>;
      for (const key of ["data", "error", "cause"] as const) {
        const found = inspect(record[key], depth + 1);
        if (found !== undefined) return found;
      }
    } catch {
      return undefined;
    }
    return undefined;
  };
  return inspect(cause, 0);
}
