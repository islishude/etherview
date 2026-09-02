import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  parseHomeSnapshot,
  useHomeSnapshot,
} from "./homeStream";

function Probe() {
  const stream = useHomeSnapshot();
  if (stream.data) {
    return (
      <output>
        {stream.data.status.latest_block}:{stream.data.blocks.map((block) => block.number).join(",")}
      </output>
    );
  }
  if (stream.error) return <output>error</output>;
  return <output>pending</output>;
}

describe("home snapshot query", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("fetches one same-origin atomic snapshot and replaces it after invalidation", async () => {
    const fetcher = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(Response.json(snapshot("10", ["10", "9"])))
      .mockResolvedValueOnce(Response.json(snapshot("11", ["11"])));
    vi.stubGlobal("fetch", fetcher);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={queryClient}>
        <Probe />
      </QueryClientProvider>,
    );
    expect(screen.getByText("pending")).toBeVisible();
    expect(await screen.findByText("10:10,9")).toBeVisible();
    expect(String(fetcher.mock.calls[0]?.[0])).toBe("/api/v1/home");

    await act(async () => {
      await queryClient.invalidateQueries({ queryKey: ["home"] });
    });
    expect(await screen.findByText("11:11")).toBeVisible();
    expect(screen.queryByText(/10,9/)).not.toBeInTheDocument();
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it("shows an error when the snapshot is unavailable", async () => {
    vi.stubGlobal("fetch", vi.fn<typeof fetch>().mockResolvedValue(
      Response.json({ error: { code: "NOT_READY", message: "not ready" } }, { status: 503 }),
    ));
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={queryClient}>
        <Probe />
      </QueryClientProvider>,
    );
    expect(await screen.findByText("error")).toBeVisible();
  });

  it("rejects oversized, unknown, partial, and overlong payloads", () => {
    const valid = snapshot("10", ["10"]);
    expect(parseHomeSnapshot(JSON.stringify(valid)).data.blocks).toHaveLength(1);
    expect(() => parseHomeSnapshot(JSON.stringify({ ...valid, unknown: true }))).toThrow();
    expect(() => parseHomeSnapshot(JSON.stringify({
      ...valid,
      data: { ...valid.data, blocks: Array.from({ length: 7 }, () => valid.data.blocks[0]) },
    }))).toThrow();
    expect(() => parseHomeSnapshot(JSON.stringify({
      ...valid,
      data: { ...valid.data, transactions: [{ hash: `0x${"01".repeat(32)}` }] },
    }))).toThrow();
    expect(() => parseHomeSnapshot(" ".repeat(2 * 1024 * 1024 + 1))).toThrow();
  });

  it("accepts protocol block and typed-transaction fields", () => {
    const valid = snapshot("10", ["10"]);
    const completeness = valid.data.status.completeness;
    const parsed = parseHomeSnapshot(JSON.stringify({
      ...valid,
      data: {
        ...valid.data,
        blocks: [{
          ...valid.data.blocks[0],
          withdrawals: [{
            index: "1",
            validator_index: "2",
            address: `0x${"11".repeat(20)}`,
            amount: "3",
          }],
        }],
        transactions: [{
          hash: `0x${"22".repeat(32)}`,
          from: `0x${"33".repeat(20)}`,
          to: `0x${"44".repeat(20)}`,
          nonce: "1",
          value: "0",
          gas: "21000",
          base_fee_per_gas: "5",
          blob_base_fee_per_gas: "6",
          max_fee_per_blob_gas: "7",
          access_list: [{
            address: `0x${"55".repeat(20)}`,
            storage_keys: [`0x${"66".repeat(32)}`],
          }],
          blob_versioned_hashes: [`0x${"77".repeat(32)}`],
          input: "0x",
          canonical: true,
          finality: "safe",
          completeness,
        }],
      },
    }));

    expect(parsed.data.blocks[0]?.withdrawals).toHaveLength(1);
    expect(parsed.data.transactions[0]?.access_list).toHaveLength(1);
    expect(parsed.data.transactions[0]?.base_fee_per_gas).toBe("5");
    expect(parsed.data.transactions[0]?.blob_base_fee_per_gas).toBe("6");
  });

  it("rejects malformed protocol fields", () => {
    const valid = snapshot("10", ["10"]);
    expect(() => parseHomeSnapshot(JSON.stringify({
      ...valid,
      data: {
        ...valid.data,
        blocks: [{
          ...valid.data.blocks[0],
          withdrawals: [{
            index: "1",
            validator_index: "2",
            address: `0x${"11".repeat(20)}`,
            amount: "3",
            unknown: true,
          }],
        }],
      },
    }))).toThrow();
  });
});

function snapshot(latest: string, blockNumbers: string[]) {
  const completeness = {
    core: "complete",
    trace: "unavailable",
    metadata: "pending",
    state: "complete",
    user_operations: "unavailable",
  };
  return {
    data: {
      status: {
        chain_id: "1",
        core_ready: true,
        latest_block: latest,
        indexed_block: latest,
        backfill_complete: true,
        lag: "0",
        completeness,
      },
      blocks: blockNumbers.map((number) => ({
        hash: `0x${number.padStart(64, "0")}`,
        number,
        parent_hash: `0x${"00".repeat(32)}`,
        timestamp: "2026-01-01T00:00:00Z",
        transaction_count: 0,
        canonical: true,
        finality: "latest",
        completeness,
      })),
      transactions: [],
    },
    meta: {
      request_id: "home-test",
      chain_id: "1",
      coverage_start: "0",
      coverage_end: latest,
    },
  };
}
