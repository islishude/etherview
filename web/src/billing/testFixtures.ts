import type { BillingPaymentConfig, BillingPaymentOption, BillingTopupIntent } from "@/api/billing";

export function testBillingConfig(value: Partial<BillingPaymentConfig> = {}): BillingPaymentConfig {
  const option: BillingPaymentOption = {
    id: value.id ?? "default",
    name: value.name ?? "Local USD",
    network: value.network ?? "eip155:1",
    asset: value.asset ?? "0x1111111111111111111111111111111111111111",
    recipient: value.recipient ?? "0x2222222222222222222222222222222222222222",
    asset_decimals: value.asset_decimals ?? 6,
    asset_eip712_name: value.asset_eip712_name ?? "Local USD",
    asset_eip712_version: value.asset_eip712_version ?? "1",
    asset_transfer_methods: value.asset_transfer_methods ?? ["eip3009", "permit2"],
    minimum_topup_amount_atomic: value.minimum_topup_amount_atomic ?? "1",
    maximum_topup_amount_atomic:
      value.maximum_topup_amount_atomic ?? ((1n << 256n) - 1n).toString(),
    credit_rate_numerator: value.credit_rate_numerator ?? "1",
    credit_rate_denominator: value.credit_rate_denominator ?? "1",
  };
  return {
    api_billing_enabled: true,
    x402_topups_enabled: true,
    x402_version: 2,
    scheme: "exact",
    operations: [],
    ...value,
    ...option,
    credit_decimals: 6,
    payment_chains: value.payment_chains ?? [
      { name: "Test Network", network: option.network, tokens: [option] },
    ],
  };
}
export function testTopupIntent(
  value: Omit<BillingTopupIntent, "payment_option" | "credit_amount_atomic"> &
    Partial<Pick<BillingTopupIntent, "payment_option" | "credit_amount_atomic">>,
  config?: Partial<BillingPaymentConfig>,
): BillingTopupIntent {
  return {
    ...value,
    payment_option:
      value.payment_option ??
      testBillingConfig({
        ...config,
        network: value.network,
        asset: value.asset,
        recipient: value.recipient,
      }).payment_chains[0]!.tokens[0]!,
    credit_amount_atomic: value.credit_amount_atomic ?? value.amount_atomic,
  };
}
