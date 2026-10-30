import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import {
  completeness,
  configResponse,
  envelope,
  notFound,
  renderExplorer,
  requestURL,
} from "@/test/corePageFixtures";

const blockHash = `0x${"11".repeat(32)}`;
const replacementHash = `0x${"22".repeat(32)}`;
const transactionHash = `0x${"aa".repeat(32)}`;
const address = `0x${"44".repeat(20)}`;
const transaction = {
  hash: transactionHash,
  block_hash: blockHash,
  block_number: "12",
  transaction_index: 0,
  from: address,
  to: address,
  nonce: "1",
  value: "0",
  gas: "21000",
  input: "0x12345678",
  status: "failed",
  canonical: true,
  finality: "safe",
  completeness: completeness(),
};

beforeEach(async () => {
  await i18n.changeLanguage("en");
});
afterEach(() => {
  vi.unstubAllGlobals();
});

it("keeps simultaneous calldata and failure mismatches closed without automatic requests", async () => {
  const reads = { calldata: 0, failure: 0 };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const path = requestURL(input).pathname;
      if (path === "/api/v1/config") return configResponse();
      if (path === `/api/v1/transactions/${transactionHash}`) return envelope(transaction);
      if (path === `/api/v1/transactions/${transactionHash}/calldata`) {
        reads.calldata++;
        return envelope({
          chain_id: "1",
          block_number: "12",
          block_hash: replacementHash,
          transaction_hash: transactionHash,
          transaction_index: "0",
          state: "complete",
          input: transaction.input,
          execution: {
            context_address: address,
            address,
            code_hash: blockHash,
            resolution: "direct",
          },
          decoding: { status: "unknown", inputs: [], candidates: [] },
        });
      }
      if (path === `/api/v1/transactions/${transactionHash}/failure`) {
        reads.failure++;
        return envelope({
          chain_id: "1",
          block_number: "12",
          block_hash: replacementHash,
          transaction_hash: transactionHash,
          transaction_index: "0",
          state: "complete",
          error: "execution reverted",
          decoding: { status: "unknown", arguments: [], candidates: [] },
        });
      }
      return notFound();
    }),
  );
  renderExplorer(`/tx/${transactionHash}`);
  await waitFor(() => expect(reads).toEqual({ calldata: 1, failure: 1 }));
  expect(await screen.findByText("Transaction action unavailable", { exact: true })).toBeVisible();
  await userEvent.setup().click(screen.getByText("More details"));
  expect(screen.getByRole("textbox", { name: "Raw calldata (Hex)" })).toHaveValue(
    transaction.input,
  );
  expect(reads).toEqual({ calldata: 1, failure: 1 });
});

it.each(["/blocks/12", "/block/12"])(
  "pins %s transaction requests to the resolved block across a reorg",
  async (path) => {
    const requests: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = requestURL(input).pathname;
        requests.push(path);
        if (path === "/api/v1/config") return configResponse();
        if (path === "/api/v1/blocks/12") return blockResponse();
        if (path === `/api/v1/blocks/${blockHash}/transactions`) return envelope([transaction]);
        if (path === "/api/v1/blocks/12/transactions") {
          return envelope([{ ...transaction, hash: replacementHash, block_hash: replacementHash }]);
        }
        return notFound();
      }),
    );
    renderExplorer(`${path}?tab=transactions`);
    await screen.findByRole("tab", { name: "Transactions" });
    await waitFor(() => expect(requests).toContain(`/api/v1/blocks/${blockHash}/transactions`));
    expect(requests).not.toContain("/api/v1/blocks/12/transactions");
  },
);

it.each([
  ["invalid", 400, "invalid_block_id", "The explorer identifier is invalid"],
  ["999999", 404, "not_found", "Indexed entity not found"],
] as const)(
  "preserves API errors through the block alias for %s",
  async (id, status, code, message) => {
    const requests: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const path = requestURL(input).pathname;
        requests.push(path);
        if (path === "/api/v1/config") return configResponse();
        if (path === `/api/v1/blocks/${id}`) {
          return Response.json(
            { error: { code, message: "Invalid or missing block", request_id: "alias-test" } },
            { status },
          );
        }
        return notFound();
      }),
    );
    renderExplorer(`/block/${id}`);
    expect(await screen.findByText(message, { exact: true })).toBeVisible();
    expect(requests).toContain(`/api/v1/blocks/${id}`);
    expect(requests.some((path) => path.endsWith("/transactions"))).toBe(false);
  },
);

it("restarts invalid block cursors with a fresh first page even while its cache is fresh", async () => {
  let firstPages = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/blocks/${blockHash}`) return blockResponse();
      if (url.pathname === `/api/v1/blocks/${blockHash}/transactions`) {
        if (url.searchParams.has("cursor"))
          return Response.json(
            {
              error: {
                code: "invalid_cursor",
                message: "stale snapshot",
                request_id: "cursor-test",
              },
            },
            { status: 400 },
          );
        firstPages++;
        return envelope([transaction], {
          next_cursor: firstPages === 1 ? "old-snapshot" : undefined,
        });
      }
      return notFound();
    }),
  );
  renderExplorer(`/blocks/${blockHash}?tab=transactions`);
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Next page" }));
  await user.click(await screen.findByRole("button", { name: "Restart from the first page" }));
  await waitFor(() => expect(firstPages).toBe(2));
  expect(screen.getByRole("button", { name: "Next page" })).toBeDisabled();
});

function blockResponse() {
  return envelope({
    hash: blockHash,
    number: "12",
    parent_hash: replacementHash,
    timestamp: "2026-01-01T00:00:00Z",
    miner: address,
    transaction_count: 1,
    gas_used: "21000",
    gas_limit: "30000000",
    canonical: true,
    finality: "safe",
    completeness: completeness(),
  });
}
