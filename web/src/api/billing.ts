import type { components, operations } from "./schema.gen";
import { apiClient, requireEnvelope } from "./client";

export type BillingPayment = components["schemas"]["BillingPayment"];
export type BillingPaymentState =
  components["schemas"]["BillingPaymentState"];
export type BillingSummary = components["schemas"]["BillingSummary"];
export type BillingAccount = components["schemas"]["BillingAccount"];
export type BillingConfig = components["schemas"]["BillingConfig"];
export type BillingTopupIntent = components["schemas"]["BillingTopupIntent"];
export type BillingTopupReceipt = components["schemas"]["BillingTopupReceipt"];
export type BillingTransferMethod =
  components["schemas"]["BillingAssetTransferMethod"];

type AdminPaymentQuery =
  NonNullable<
    operations["listAdminBillingPayments"]["parameters"]["query"]
  >;

export type AdminBillingFilters = Pick<
  AdminPaymentQuery,
  "asset" | "from_time" | "network" | "operation" | "state" | "to_time"
>;

export async function listCurrentUserBillingPayments(
  limit: number,
  cursor?: string,
) {
  return requireEnvelope(
    await apiClient.GET("/billing/payments", {
      params: { query: { cursor, limit } },
    }),
  );
}

export async function getBillingConfig(): Promise<BillingConfig> {
  return requireEnvelope(await apiClient.GET("/billing/config")).data;
}

export async function getCurrentBillingAccount(): Promise<BillingAccount> {
  return requireEnvelope(await apiClient.GET("/billing/account")).data;
}

export async function createBillingTopupIntent(
  amountAtomic: string,
  csrfToken: string,
): Promise<BillingTopupIntent> {
  return requireEnvelope(
    await apiClient.POST("/billing/topup-intents", {
      body: { amount_atomic: amountAtomic },
      params: { header: { "X-CSRF-Token": csrfToken } },
    }),
  ).data;
}

export async function getBillingTopupIntent(id: string): Promise<BillingTopupIntent> {
  return requireEnvelope(
    await apiClient.GET("/billing/topup-intents/{id}", {
      params: { path: { id } },
    }),
  ).data;
}

export async function listCurrentBillingTopupIntents(limit = 10, cursor?: string) {
  return requireEnvelope(
    await apiClient.GET("/billing/topup-intents", {
      params: { query: { cursor, limit } },
    }),
  );
}

export async function listCurrentUserBillingUsage(limit = 25) {
  return requireEnvelope(
    await apiClient.GET("/billing/usage", { params: { query: { limit } } }),
  );
}

export async function listAdminBillingPayments(
  limit: number,
  cursor?: string,
  filters: AdminBillingFilters = {},
) {
  return requireEnvelope(
    await apiClient.GET("/admin/billing/payments", {
      params: {
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
      },
    }),
  );
}

export async function getAdminBillingSummary(
  filters: AdminBillingFilters = {},
): Promise<BillingSummary> {
  return requireEnvelope(
    await apiClient.GET("/admin/billing/summary", {
      params: {
        query: {
          asset: filters.asset,
          from_time: filters.from_time,
          network: filters.network,
          operation: filters.operation,
          state: filters.state,
          to_time: filters.to_time,
        },
      },
    }),
  ).data;
}
