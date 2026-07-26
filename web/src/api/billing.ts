import type { components, operations } from "./schema.gen";
import { apiClient, requireEnvelope } from "./client";

export type BillingPayment = components["schemas"]["BillingPayment"];
export type BillingPaymentState =
  components["schemas"]["BillingPaymentState"];
export type BillingSummary = components["schemas"]["BillingSummary"];

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
