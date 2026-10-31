import type { BillingConfig } from "@/api/billing";

const MAX_ATOMIC = (1n << 256n) - 1n;

export function amountInputLength(decimals: number): number {
  return decimals === 0 ? 78 : Math.max(79, decimals + 2);
}

// Never pass token quantities through Number or a rounding decimal parser.
export function parseTokenAmount(value: string, decimals: number): string | undefined {
  if (!Number.isInteger(decimals) || decimals < 0 || decimals > 255) return;
  if (value.length > amountInputLength(decimals) || !/^\d+(?:\.\d+)?$/u.test(value)) return;
  const [whole, fraction = ""] = value.split(".");
  if (fraction.length > decimals) return;
  const atomic = BigInt(whole! + fraction.padEnd(decimals, "0"));
  if (atomic <= 0n || atomic > MAX_ATOMIC) return;
  return atomic.toString();
}

export function formatTokenAmount(atomic: string, decimals: number): string {
  const padded = atomic.padStart(decimals + 1, "0");
  if (decimals === 0) return padded;
  const fraction = padded.slice(-decimals).replace(/0+$/u, "");
  return padded.slice(0, -decimals) + (fraction ? `.${fraction}` : "");
}

export function topupAmount(value: string, config: BillingConfig): string | undefined {
  if (config.asset_decimals === undefined) return;
  const atomic = parseTokenAmount(value, config.asset_decimals);
  if (!atomic) return;
  if (
    config.minimum_topup_amount_atomic &&
    BigInt(atomic) < BigInt(config.minimum_topup_amount_atomic)
  )
    return;
  if (
    config.maximum_topup_amount_atomic &&
    BigInt(atomic) > BigInt(config.maximum_topup_amount_atomic)
  )
    return;
  return atomic;
}

export function displayBillingAmount(atomic: string, config?: BillingConfig): string {
  return config?.asset_decimals === undefined
    ? `${atomic} atomic`
    : formatTokenAmount(atomic, config.asset_decimals);
}

export function matchingAssetConfig(
  record: { network: string; asset: string },
  config?: BillingConfig,
): BillingConfig | undefined {
  return record.network === config?.network &&
    record.asset.toLowerCase() === config.asset?.toLowerCase()
    ? config
    : undefined;
}
