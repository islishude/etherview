import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { formatInteger, shorten } from "@/components/format";
import {
  completeness,
  configResponse,
  envelope,
  notFound,
  renderExplorer,
  requestURL,
} from "@/test/corePageFixtures";

const owner = `0x${"11".repeat(20)}`;
const otherOwner = `0x${"22".repeat(20)}`;
const token = `0x${"33".repeat(20)}`;
const tokenID = (2n ** 256n - 1n).toString();
const requests: URL[] = [];
const holding = {
  chain_id: "1",
  owner,
  token_address: token,
  balance: "1234500",
  decimals: 4,
  name: "Example Token",
  symbol: "EXT",
  confidence: "rpc_exact",
};
let tokenData: unknown[];
let tokenMeta: Record<string, unknown>;
let tokenError: string | undefined;

beforeEach(async () => {
  await i18n.changeLanguage("en");
  requests.length = 0;
  tokenData = [holding];
  tokenMeta = { coverage_end: "999", next_cursor: "token:next" };
  tokenError = undefined;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      requests.push(url);
      if (url.pathname === "/api/v1/config") return configResponse();
      if ([owner, otherOwner].some((address) => url.pathname === `/api/v1/addresses/${address}`)) {
        return envelope({
          address: url.pathname.split("/").at(-1),
          type: "eoa",
          balance: "0",
          nonce: "0",
          at_block: `0x${"aa".repeat(32)}`,
          completeness: completeness(),
        });
      }
      if (url.pathname.endsWith("/erc20-balances")) {
        if (tokenError)
          return Response.json(
            {
              error: {
                code: tokenError,
                message: "unavailable",
                details: { state: "unavailable" },
                request_id: "assets",
              },
            },
            { status: 503 },
          );
        return envelope(
          url.searchParams.has("cursor") ? [{ ...holding, name: "Second token" }] : tokenData,
          url.searchParams.has("cursor") ? { coverage_end: "999" } : tokenMeta,
        );
      }
      if (url.pathname.endsWith("/nfts")) {
        return envelope(
          [
            {
              ...holding,
              token_id: url.searchParams.has("cursor") ? "2" : tokenID,
              balance: tokenID,
            },
          ],
          {
            coverage_end: "999",
            next_cursor: url.searchParams.has("cursor") ? undefined : "nft:next",
          },
        );
      }
      return notFound();
    }),
  );
});

afterEach(() => vi.unstubAllGlobals());

function assetLinks() {
  return within(screen.getByRole("navigation", { name: "Asset types" }));
}

it.each(["", "&asset=invalid"])("defaults to ERC-20 only for %s", async (suffix) => {
  renderExplorer(`/address/${owner}?tab=assets${suffix}`);
  expect(await screen.findByText("Example Token")).toBeVisible();
  expect(screen.getByText("123.45")).toBeVisible();
  expect(assetLinks().getByRole("link", { name: "Token" })).toHaveAttribute("aria-current", "page");
  expect(requests.filter((url) => url.pathname.endsWith("/nfts"))).toHaveLength(0);
  expect(screen.queryByRole("region", { name: "Canonical NFT balances" })).not.toBeInTheDocument();
});

it("deep-links NFT assets without requesting tokens or metadata and preserves full identifiers", async () => {
  renderExplorer(`/address/${owner}?tab=assets&asset=nfts`);
  const link = await screen.findByTitle(tokenID);
  expect(link).toHaveTextContent(`#${shorten(tokenID)}`);
  expect(link).toHaveAttribute("href", `/nft/${token}/${tokenID}`);
  expect(screen.getByText(`Quantity: ${formatInteger(tokenID, "en")}`)).toBeVisible();
  expect(requests.some((url) => /erc20-balances|metadata|media/.test(url.pathname))).toBe(false);
});

it("keeps independent cursor positions through asset navigation and browser history", async () => {
  const user = userEvent.setup();
  const { router } = renderExplorer(`/address/${owner}?tab=assets`);
  await screen.findByText("Example Token");
  await user.click(screen.getByRole("button", { name: "Next page" }));
  await screen.findByText("Second token");
  await user.click(assetLinks().getByRole("link", { name: "NFT" }));
  await screen.findByTitle(tokenID);
  await user.click(screen.getByRole("button", { name: "Next page" }));
  await screen.findByRole("link", { name: "#2" });
  await act(async () => router.history.back());
  expect(await screen.findByText("Second token")).toBeVisible();
  await act(async () => router.history.forward());
  expect(await screen.findByRole("link", { name: "#2" })).toBeVisible();
  expect(requests.some((url) => url.searchParams.get("cursor") === "token:next")).toBe(true);
  expect(requests.some((url) => url.searchParams.get("cursor") === "nft:next")).toBe(true);
});

it("resets cursors for another address and drops asset selection outside Assets", async () => {
  const user = userEvent.setup();
  const { router } = renderExplorer(`/address/${owner}?tab=assets`);
  await screen.findByText("Example Token");
  await user.click(screen.getByRole("button", { name: "Next page" }));
  await screen.findByText("Second token");
  await act(async () =>
    router.navigate({
      to: "/address/$address",
      params: { address: otherOwner },
      search: { tab: "assets", asset: "tokens" },
    }),
  );
  expect(await screen.findByText("Example Token")).toBeVisible();
  const reads = requests.filter(
    (url) => url.pathname === `/api/v1/addresses/${otherOwner}/erc20-balances`,
  );
  expect(reads).toHaveLength(1);
  expect(reads[0]?.searchParams.has("cursor")).toBe(false);
  await user.click(
    within(screen.getByRole("navigation", { name: "Address activity sections" })).getByRole(
      "link",
      {
        name: "Transactions",
      },
    ),
  );
  await waitFor(() => expect(router.state.location.search).not.toHaveProperty("asset"));
});

it("shows raw units for unknown decimals and preserves more than eighteen decimal places", async () => {
  tokenData = [
    { ...holding, name: undefined, symbol: undefined, decimals: undefined, balance: tokenID },
    {
      ...holding,
      token_address: otherOwner,
      name: undefined,
      decimals: 24,
      balance: "1000000000000000000000001",
    },
  ];
  renderExplorer(`/address/${owner}?tab=assets`);
  expect(await screen.findByText("Raw units")).toBeVisible();
  expect(screen.getByText(formatInteger(tokenID, "en"))).toHaveAttribute(
    "title",
    formatInteger(tokenID, "en"),
  );
  expect(screen.getByRole("link", { name: shorten(token) })).toHaveAttribute(
    "href",
    `/token/${token}`,
  );
  expect(screen.getByText("1.000000000000000000000001")).toBeVisible();
});

it.each(["stage_unavailable", "upstream_failure"])(
  "does not report %s as empty holdings",
  async (code) => {
    tokenError = code;
    renderExplorer(`/address/${owner}?tab=assets`);
    const region = await screen.findByRole("region", { name: "ERC-20 holdings" });
    await waitFor(() =>
      expect(within(region).queryByText("Loading indexed data…")).not.toBeInTheDocument(),
    );
    expect(within(region).getByRole("status")).toBeVisible();
    expect(within(region).queryByRole("list")).not.toBeInTheDocument();
    expect(region.querySelector(".empty-result")).toBeNull();
    expect(region.querySelector(".address-asset-snapshot")).toBeNull();
  },
);

it.each(["complete", "partial"])(
  "preserves %s browsing semantics for empty and partial data",
  async (state) => {
    tokenData = [];
    tokenMeta = { browsing: { state }, coverage_end: "999" };
    renderExplorer(`/address/${owner}?tab=assets`);
    const region = await screen.findByRole("region", { name: "ERC-20 holdings" });
    await waitFor(() => expect(region.querySelector(".empty-result")).not.toBeNull());
    expect(region.querySelector(".empty-result")).toHaveTextContent(
      i18n.t(state === "complete" ? "state.noERC20Balances" : "state.browsingPending"),
    );
    expect(within(region).queryByRole("button", { name: "Next page" })).not.toBeInTheDocument();
  },
);

it("retains next-page navigation when a published page contains no holdings", async () => {
  tokenData = [];
  tokenMeta = { next_cursor: "token:next", browsing: { state: "partial" } };
  renderExplorer(`/address/${owner}?tab=assets`);
  const next = await screen.findByRole("button", { name: "Next page" });
  expect(next).toBeEnabled();
  await userEvent.setup().click(next);
  expect(await screen.findByText("Second token")).toBeVisible();
});
