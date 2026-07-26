import { afterEach, describe, expect, it, vi } from "vitest";

import {
  getAdminBillingSummary,
  listAdminBillingPayments,
  listCurrentUserBillingPayments,
} from "./billing";

const paymentCursor = "billing/snapshot + page=2?exact=true/#";
const amount = "340282366920938463463374607431768211455";
const paymentCount = "900719925474099312345";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("generated billing API adapter", () => {
  it("preserves the personal opaque cursor and never sends payment or CSRF headers", async () => {
    const storageWrite = vi.spyOn(window.localStorage, "setItem");
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      envelope([payment()], { next_cursor: paymentCursor }),
    );
    vi.stubGlobal("fetch", fetcher);

    const response = await listCurrentUserBillingPayments(10, paymentCursor);

    expect(response.data[0]?.amount_atomic).toBe(amount);
    expect(response.meta.next_cursor).toBe(paymentCursor);
    const [url, request] = fetcher.mock.calls[0]!;
    expect(url).toBe(
      "/api/v1/billing/payments?cursor=billing%2Fsnapshot%20%2B%20page%3D2%3Fexact%3Dtrue%2F%23&limit=10",
    );
    expect(request).toMatchObject({
      method: "GET",
      credentials: "same-origin",
      cache: "no-store",
    });
    const headers = new Headers(request?.headers);
    expect(headers.has("PAYMENT-SIGNATURE")).toBe(false);
    expect(headers.has("X-CSRF-Token")).toBe(false);
    expect(storageWrite).not.toHaveBeenCalled();
  });

  it("forwards strict admin filters and the opaque cursor through generated query parameters", async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      envelope([payment()], { next_cursor: paymentCursor }),
    );
    vi.stubGlobal("fetch", fetcher);

    await listAdminBillingPayments(25, paymentCursor, {
      asset: "0x3333333333333333333333333333333333333333",
      from_time: "2026-07-25T00:00:00.000Z",
      network: "eip155:84532",
      operation: "getBlock",
      state: "settling",
      to_time: "2026-07-26T00:00:00.000Z",
    });

    const [input, request] = fetcher.mock.calls[0]!;
    const url = new URL(String(input), "http://localhost");
    expect(url.pathname).toBe("/api/v1/admin/billing/payments");
    expect(Object.fromEntries(url.searchParams)).toEqual({
      asset: "0x3333333333333333333333333333333333333333",
      cursor: paymentCursor,
      from_time: "2026-07-25T00:00:00.000Z",
      limit: "25",
      network: "eip155:84532",
      operation: "getBlock",
      state: "settling",
      to_time: "2026-07-26T00:00:00.000Z",
    });
    expect(new Headers(request?.headers).has("PAYMENT-SIGNATURE")).toBe(false);
  });

  it("keeps summary quantities as exact strings without Number coercion", async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      envelope({
        amount_atomic: amount,
        from_time: "2026-07-25T00:00:00Z",
        payment_count: paymentCount,
        rows: [
          {
            amount_atomic: amount,
            asset: "0x3333333333333333333333333333333333333333",
            network: "eip155:84532",
            operation: "getBlock",
            payment_count: paymentCount,
            state: "settled",
          },
        ],
        to_time: "2026-07-26T00:00:00Z",
      }),
    );
    vi.stubGlobal("fetch", fetcher);

    const response = await getAdminBillingSummary();

    expect(response.amount_atomic).toBe(amount);
    expect(response.payment_count).toBe(paymentCount);
    expect(response.rows[0]?.amount_atomic).toBe(amount);
    expect(response.rows[0]?.payment_count).toBe(paymentCount);
  });
});

function envelope(data: unknown, meta: Record<string, unknown> = {}) {
  return Response.json({
    data,
    meta: {
      request_id: "billing-api-test",
      chain_id: "1",
      ...meta,
    },
  });
}

function payment() {
  return {
    id: "018f3b52-0b3d-7bf1-b65f-6f214827cb66",
    operation: "getBlock",
    state: "settled",
    network: "eip155:84532",
    asset: "0x3333333333333333333333333333333333333333",
    amount_atomic: amount,
    recipient: "0x4444444444444444444444444444444444444444",
    payer: "0x5555555555555555555555555555555555555555",
    created_at: "2026-07-25T23:58:00Z",
    updated_at: "2026-07-26T00:00:00Z",
    settled_at: "2026-07-26T00:00:00Z",
  };
}
