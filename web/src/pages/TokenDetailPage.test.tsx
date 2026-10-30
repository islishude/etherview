import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { formatInteger, shorten } from "@/components/format";
import {
  configResponse,
  envelope,
  notFound,
  renderExplorer,
  requestURL,
} from "@/test/corePageFixtures";

const address = `0x${"12".repeat(20)}`;
const peer = `0x${"34".repeat(20)}`;
const hash = `0x${"56".repeat(32)}`;
const snapshotHash = `0x${"78".repeat(32)}`;
let token: Record<string, unknown>;
let holderMeta: Record<string, unknown>;
let holderError: string | undefined;
const requests: URL[] = [];

beforeEach(async () => {
  await i18n.changeLanguage("en");
  requests.length = 0;
  holderError = undefined;
  token = {
    chain_id: "1",
    address,
    code_hash: hash,
    standard: "erc20",
    confidence: "verified",
    name: "Example Token",
    symbol: "EXT",
    decimals: 6,
    total_supply: "12000000",
    metadata_state: "complete",
    observed_block_number: "40",
    observed_block_hash: hash,
  };
  holderMeta = {
    holder_count: "2",
    total_supply: "15000000",
    snapshot_block_number: "42",
    snapshot_block_hash: snapshotHash,
    next_cursor: "holders:next",
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      requests.push(url);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === "/api/v1/tokens") return envelope([token]);
      if (url.pathname === `/api/v1/tokens/${address}`) return envelope(token);
      if (url.pathname.endsWith("/holders")) {
        if (holderError)
          return Response.json(
            {
              error: {
                code: holderError,
                request_id: "token-test",
                message: "unavailable",
                details: { state: "unavailable" },
              },
            },
            { status: 503 },
          );
        return envelope(
          holderMeta.holder_count === "0"
            ? []
            : [
                {
                  chain_id: "1",
                  token_address: address,
                  holder_address: peer,
                  balance: url.searchParams.has("cursor") ? "5000000" : "10000000",
                  confidence: "rpc_exact",
                  observed_block_number: "42",
                  observed_block_hash: snapshotHash,
                },
              ],
          {
            ...holderMeta,
            next_cursor: url.searchParams.has("cursor") ? undefined : holderMeta.next_cursor,
          },
        );
      }
      if (url.pathname.endsWith("/transfers"))
        return envelope(
          [
            {
              chain_id: "1",
              block_number: "42",
              block_hash: snapshotHash,
              log_index: "0",
              sub_index: "0",
              transaction_hash: hash,
              token_address: address,
              standard: "erc20",
              kind: "transfer",
              from: address,
              to: peer,
              amount: url.searchParams.has("cursor") ? "2000000" : "1000000",
              decimals: 6,
              confidence: "verified",
            },
          ],
          { next_cursor: url.searchParams.has("cursor") ? undefined : "transfers:next" },
        );
      return notFound();
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

const tabs = () => within(screen.getByRole("navigation", { name: "Token sections" }));
const summary = () => within(screen.getByRole("region", { name: "Token summary" }));
const resourceRequests = (kind: string) =>
  requests.filter((url) => url.pathname.endsWith(`/${kind}`));
const field = (label: string) => summary().getByText(label, { exact: true }).nextElementSibling!;

it("links the token address and shares the authoritative summary across tabs and browser history", async () => {
  const { router } = renderExplorer("/tokens");
  const addressLink = await screen.findByRole("link", { name: shorten(address) });
  expect(addressLink).toHaveAttribute("title", address);
  await userEvent.click(addressLink);
  await screen.findByRole("heading", { name: "Example Token" });
  await waitFor(() => expect(field("Holder count")).toHaveTextContent("2"));
  expect(field("Total supply")).toHaveTextContent("15Reconciled at block 42");
  expect(summary().getByRole("link", { name: "42" })).toHaveAttribute(
    "href",
    `/blocks/${snapshotHash}`,
  );
  expect(tabs().getByRole("link", { name: "Transfers" })).toHaveAttribute("aria-current", "page");
  expect(screen.queryByRole("region", { name: "Token holders" })).not.toBeInTheDocument();
  await userEvent.click(tabs().getByRole("link", { name: "Token holders" }));
  expect(await screen.findByRole("region", { name: "Token holders" })).toBeVisible();
  expect(resourceRequests("holders")).toHaveLength(1);
  await userEvent.click(tabs().getByRole("link", { name: "Details" }));
  expect(await screen.findByRole("heading", { name: "Token metadata" })).toBeVisible();
  expect(screen.getByText("Snapshot block hash").nextElementSibling).toHaveTextContent(
    snapshotHash,
  );
  act(() => router.history.back());
  expect(await screen.findByRole("region", { name: "Token holders" })).toBeVisible();
  act(() => router.history.forward());
  expect(await screen.findByRole("heading", { name: "Token metadata" })).toBeVisible();
});

it("deep-links details without loading transfers and preserves independent cursors until manual refresh", async () => {
  renderExplorer(`/token/${address}?tab=details`);
  await screen.findByRole("heading", { name: "Token metadata" });
  expect(resourceRequests("transfers")).toHaveLength(0);
  await userEvent.click(tabs().getByRole("link", { name: "Token holders" }));
  const holderRegion = await screen.findByRole("region", { name: "Token holders" });
  await userEvent.click(within(holderRegion).getByRole("button", { name: /next/i }));
  await waitFor(() =>
    expect(resourceRequests("holders").at(-1)?.searchParams.get("cursor")).toBe("holders:next"),
  );
  await userEvent.click(tabs().getByRole("link", { name: "Transfers" }));
  const transferRegion = await screen.findByRole("region", { name: "Token events" });
  await userEvent.click(within(transferRegion).getByRole("button", { name: /next/i }));
  await waitFor(() =>
    expect(resourceRequests("transfers").at(-1)?.searchParams.get("cursor")).toBe("transfers:next"),
  );
  await userEvent.click(tabs().getByRole("link", { name: "Token holders" }));
  expect(
    within(await screen.findByRole("region", { name: "Token holders" })).getByText("5", {
      exact: true,
    }),
  ).toBeVisible();
  await userEvent.click(await screen.findByRole("button", { name: "Refresh" }));
  await waitFor(() =>
    expect(resourceRequests("holders").at(-1)?.searchParams.has("cursor")).toBe(false),
  );
  expect(resourceRequests("transfers")).toHaveLength(2);
  await userEvent.click(tabs().getByRole("link", { name: "Transfers" }));
  expect(
    within(await screen.findByRole("region", { name: "Token events" })).getByRole("button", {
      name: /previous/i,
    }),
  ).toBeEnabled();
});

it("does not fabricate holder totals on failure and recovers invalid cursors", async () => {
  holderError = "token_holders_unavailable";
  renderExplorer(`/token/${address}`);
  await waitFor(() => expect(field("Holder count")).toHaveTextContent("—"));
  expect(field("Total supply")).toHaveTextContent("12Observed at block 40");
  await userEvent.click(tabs().getByRole("link", { name: "Token holders" }));
  expect(screen.queryByRole("table")).not.toBeInTheDocument();
  holderError = undefined;
  await userEvent.click(await screen.findByRole("button", { name: "Refresh" }));
  const region = await screen.findByRole("region", { name: "Token holders" });
  await within(region).findByRole("table");
  const next = within(region).getByRole("button", { name: /next/i });
  await waitFor(() => expect(next).toBeEnabled());
  holderError = "invalid_cursor";
  await userEvent.click(next);
  const restart = await screen.findByRole("button", { name: /restart/i });
  expect(field("Holder count")).toHaveTextContent("—");
  holderError = undefined;
  await userEvent.click(restart);
  await waitFor(() => expect(field("Holder count")).toHaveTextContent("2"));
});

it.each(["erc721", "erc1155", "unknown"])(
  "hides holder UI and falls back from its deep link for %s",
  async (standard) => {
    token.standard = standard;
    token.decimals = undefined;
    renderExplorer(`/token/${address}?tab=holders`);
    await screen.findByRole("heading", { name: "Token events" });
    expect(tabs().queryByRole("link", { name: "Token holders" })).not.toBeInTheDocument();
    expect(summary().queryByText("Holder count")).not.toBeInTheDocument();
    expect(resourceRequests("holders")).toHaveLength(0);
    expect(field("Total supply")).toHaveTextContent("(raw units)");
  },
);

it.each(["0", (2n ** 256n - 1n).toString()])(
  "preserves exact supply %s and defaults invalid tabs to transfers",
  async (supply) => {
    token.decimals = undefined;
    holderMeta.total_supply = supply;
    const count = supply === "0" ? "0" : "2";
    holderMeta.holder_count = count;
    renderExplorer(`/token/${address}?tab=invalid`);
    await waitFor(() => expect(field("Holder count")).toHaveTextContent(count));
    expect(field("Total supply")).toHaveTextContent(formatInteger(supply, "en"));
    expect(field("Total supply")).toHaveTextContent("(raw units)");
    expect(tabs().getByRole("link", { name: "Transfers" })).toHaveAttribute("aria-current", "page");
    if (supply === "0") {
      await userEvent.click(tabs().getByRole("link", { name: "Token holders" }));
      expect(
        await screen.findByText("The reconciled token supply has no holders at this snapshot."),
      ).toBeVisible();
    }
  },
);
