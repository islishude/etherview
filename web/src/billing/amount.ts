import type { BillingConfig, BillingPaymentOption } from "@/api/billing";

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

export function topupAmount(value: string, config: BillingPaymentOption): string | undefined {
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
  if (creditAmount(atomic, config) === undefined) return;
  return atomic;
}

export function creditAmount(
  atomic: string,
  option: Pick<BillingPaymentOption, "credit_rate_numerator" | "credit_rate_denominator">,
): string | undefined {
  const numerator = BigInt(option.credit_rate_numerator),
    denominator = BigInt(option.credit_rate_denominator);
  if (numerator <= 0n || denominator <= 0n) return;
  const product = BigInt(atomic) * numerator;
  if (product % denominator !== 0n) return;
  const credit = product / denominator;
  return credit > 0n && credit <= MAX_ATOMIC ? credit.toString() : undefined;
}
export function displayBillingAmount(
  atomic: string,
  config?: { asset_decimals?: number; credit_decimals?: number },
): string {
  const decimals = config?.asset_decimals ?? config?.credit_decimals;
  return decimals === undefined ? `${atomic} atomic` : formatTokenAmount(atomic, decimals);
}
export function matchingAssetConfig(
  record: { network: string; asset: string; payment_option?: BillingPaymentOption },
  _config?: BillingConfig,
): BillingPaymentOption | undefined {
  // Historical token precision comes only from the immutable intent snapshot.
  return record.payment_option;
}

export function tokenCreditRate(option: BillingPaymentOption): string {
  const numerator = BigInt(option.credit_rate_numerator) * 10n ** BigInt(option.asset_decimals);
  const denominator = BigInt(option.credit_rate_denominator) * 1000000n;
  let a = numerator,
    b = denominator;
  while (b !== 0n) [a, b] = [b, a % b];
  return denominator / a === 1n
    ? (numerator / a).toString()
    : `${numerator / a} / ${denominator / a}`;
}
