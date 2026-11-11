import { screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import {
  configResponse,
  completeness,
  envelope,
  notFound,
  renderExplorer,
  requestURL,
} from "@/test/corePageFixtures";

const hash = `0x${"11".repeat(32)}`;
const overviewBlock = `0x${"22".repeat(32)}`;
const replacementBlock = `0x${"33".repeat(32)}`;
const address = `0x${"44".repeat(20)}`;
afterEach(() => vi.unstubAllGlobals());

it.each([
  { items: [] },
  {
    items: [
      {
        block_number: "12",
        block_hash: replacementBlock,
        transaction_hash: hash,
        transaction_index: "0",
        log_index: "0",
        from: address,
        to: address,
        amount: "123456789",
        timestamp: "2026-11-11T00:00:00Z",
      },
    ],
  },
])(
  "rejects another inclusion even when the native transfer page is empty: %j",
  async ({ items }) => {
    await i18n.changeLanguage("en");
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = requestURL(input);
        if (url.pathname === "/api/v1/config") return configResponse();
        if (url.pathname === `/api/v1/transactions/${hash}`)
          return envelope({
            hash,
            block_hash: overviewBlock,
            block_number: "12",
            transaction_index: 0,
            from: address,
            to: address,
            nonce: "1",
            value: "0",
            gas: "21000",
            type: "2",
            input: "0x",
            status: "success",
            canonical: true,
            finality: "safe",
            completeness: completeness(),
          });
        if (url.pathname.endsWith("/native-transfers"))
          return Response.json({
            applicable: true,
            data: items,
            meta: {
              chain_id: "1",
              request_id: "native-test",
              next_cursor: "another-page",
              browsing: {
                state: "complete",
                from_block: "12",
                to_block: "12",
                revision: "2",
                snapshot: { chain_id: "1", block_number: "12", block_hash: replacementBlock },
              },
            },
          });
        return notFound();
      }),
    );
    renderExplorer(`/tx/${hash}?tab=native-transfers`);
    expect(await screen.findByText(i18n.t("state.transactionIdentityChanged"))).toBeVisible();
    expect(screen.queryByText("123,456,789")).toBeNull();
    expect(screen.queryByText(i18n.t("nativeTransfer.empty"))).toBeNull();
    expect(screen.queryByRole("button", { name: /next page/i })).toBeNull();
  },
);
