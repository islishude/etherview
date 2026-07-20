export interface EIP1193RequestArguments {
  method: string;
  params?: readonly unknown[] | Record<string, unknown>;
}

export interface EIP1193Provider {
  request<T = unknown>(args: EIP1193RequestArguments): Promise<T>;
  on?(event: "accountsChanged" | "chainChanged", listener: (value: unknown) => void): void;
  removeListener?(
    event: "accountsChanged" | "chainChanged",
    listener: (value: unknown) => void,
  ): void;
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

export function isProviderDetail(value: unknown): value is EIP6963ProviderDetail {
  if (typeof value !== "object" || value === null || !("info" in value) || !("provider" in value)) {
    return false;
  }

  const { info, provider } = value;
  return (
    typeof info === "object" &&
    info !== null &&
    "uuid" in info &&
    typeof info.uuid === "string" &&
    info.uuid.length > 0 &&
    info.uuid.length <= 128 &&
    "name" in info &&
    typeof info.name === "string" &&
    info.name.length > 0 &&
    info.name.length <= 128 &&
    "icon" in info &&
    typeof info.icon === "string" &&
    "rdns" in info &&
    typeof info.rdns === "string" &&
    typeof provider === "object" &&
    provider !== null &&
    "request" in provider &&
    typeof provider.request === "function"
  );
}

export function normalizeChainID(chainID: unknown): string | undefined {
  if (typeof chainID !== "string" && typeof chainID !== "number" && typeof chainID !== "bigint") {
    return undefined;
  }

  try {
    const normalized = BigInt(chainID);
    if (normalized < 0n) return undefined;
    return normalized.toString(10);
  } catch {
    return undefined;
  }
}

export function chainsMatch(actual: unknown, expected: unknown): boolean {
  const actualID = normalizeChainID(actual);
  const expectedID = normalizeChainID(expected);
  return actualID !== undefined && expectedID !== undefined && actualID === expectedID;
}

export class WalletBoundaryError extends Error {
  readonly code:
    | "NOT_CONNECTED"
    | "CHAIN_UNAVAILABLE"
    | "CHAIN_MISMATCH"
    | "INVALID_PROVIDER_RESPONSE";

  constructor(code: WalletBoundaryError["code"], message: string) {
    super(message);
    this.name = "WalletBoundaryError";
    this.code = code;
  }
}

export function assertWalletChain(actual: unknown, expected: unknown): void {
  if (normalizeChainID(expected) === undefined) {
    throw new WalletBoundaryError("CHAIN_UNAVAILABLE", "Explorer chain configuration is unavailable");
  }
  if (!chainsMatch(actual, expected)) {
    throw new WalletBoundaryError("CHAIN_MISMATCH", "Injected wallet is connected to another chain");
  }
}
