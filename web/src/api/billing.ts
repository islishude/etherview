import * as sdk from "./heyapi/sdk.gen";
import type * as schemas from "./heyapi/types.gen";
import { apiClient, requireEnvelope } from "./client";

export type BillingPayment = schemas.BillingPayment;
export type BillingPaymentState = schemas.BillingPaymentState;
export type BillingSummary = schemas.BillingSummary;
export type BillingAccount = schemas.BillingAccount;
export type BillingConfig = schemas.BillingConfig;
export type BillingTopupIntent = schemas.BillingTopupIntent;
export type BillingTopupReceipt = schemas.BillingTopupReceipt;
export type BillingTransferMethod = schemas.BillingAssetTransferMethod;

type AdminPaymentQuery = NonNullable<schemas.ListAdminBillingPaymentsData["query"]>;

export type AdminBillingFilters = Pick<
  AdminPaymentQuery,
  "asset" | "from_time" | "network" | "operation" | "state" | "to_time"
>;

export async function listCurrentUserBillingPayments(limit: number, cursor?: string) {
  return requireEnvelope(
    await sdk.listCurrentUserBillingPayments({ client: apiClient, query: { cursor, limit } }),
  );
}

export async function getBillingConfig(): Promise<BillingConfig> {
  return requireEnvelope(await sdk.getBillingConfig({ client: apiClient })).data;
}

export async function getCurrentBillingAccount(): Promise<BillingAccount> {
  return requireEnvelope(await sdk.getCurrentBillingAccount({ client: apiClient })).data;
}

export async function createBillingTopupIntent(
  amountAtomic: string,
  csrfToken: string,
): Promise<BillingTopupIntent> {
  return requireEnvelope(
    await sdk.createBillingTopupIntent({
      client: apiClient,
      body: { amount_atomic: amountAtomic },
      headers: { "X-CSRF-Token": csrfToken },
    }),
  ).data;
}

export async function getBillingTopupIntent(id: string): Promise<BillingTopupIntent> {
  return requireEnvelope(await sdk.getBillingTopupIntent({ client: apiClient, path: { id } })).data;
}

export async function listCurrentBillingTopupIntents(limit = 10, cursor?: string) {
  return requireEnvelope(
    await sdk.listCurrentBillingTopupIntents({ client: apiClient, query: { cursor, limit } }),
  );
}

export async function listCurrentUserBillingUsage(limit = 25) {
  return requireEnvelope(
    await sdk.listCurrentUserBillingUsage({ client: apiClient, query: { limit } }),
  );
}

export async function listAdminBillingPayments(
  limit: number,
  cursor?: string,
  filters: AdminBillingFilters = {},
) {
  return requireEnvelope(
    await sdk.listAdminBillingPayments({
      client: apiClient,
      query: {
        asset: filters.asset,
        cursor,
        from_time: filters.from_time,
        limit,
        network: filters.network,
        operation: filters.operation,
        state: filters.state,
        to_time: filters.to_time,
      },
    }),
  );
}

export async function getAdminBillingSummary(
  filters: AdminBillingFilters = {},
): Promise<BillingSummary> {
  return requireEnvelope(
    await sdk.getAdminBillingSummary({
      client: apiClient,
      query: {
        asset: filters.asset,
        from_time: filters.from_time,
        network: filters.network,
        operation: filters.operation,
        state: filters.state,
        to_time: filters.to_time,
      },
    }),
  ).data;
}
