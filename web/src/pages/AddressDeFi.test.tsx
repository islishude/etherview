import { screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, afterEach, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import {
  completeness,
  configResponse,
  envelope,
  notFound,
  renderExplorer,
  requestURL,
} from "@/test/corePageFixtures";
import type { DeFiPositionsPage } from "@/api/heyapi/types.gen";
const owner = `0x${"11".repeat(20)}`;
const token = `0x${"22".repeat(20)}`;
const hash = `0x${"ab".repeat(32)}`;
let data: DeFiPositionsPage;
let requests: URL[];
let failure: boolean;
beforeEach(async () => {
  await i18n.changeLanguage("en");
  requests = [];
  failure = false;
  data = {
    deployments: [
      {
        protocol: "aave-v3",
        deployment: "aave",
        state: "complete",
        block_number: "99",
        block_hash: hash,
      },
    ],
    positions: [
      {
        id: "loan",
        protocol: "aave-v3",
        deployment: "aave",
        owner,
        kind: "borrow",
        contract: token,
        assets: [],
        debts: [{ kind: "erc20", address: token, amount: "9007199254740993123", decimals: 18 }],
        claimable: [],
        unsettled_fees: [],
        state: "available",
        limitations: [],
        block_number: "99",
        block_hash: hash,
      },
    ],
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      requests.push(url);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/addresses/${owner}`)
        return envelope({
          address: owner,
          type: "eoa",
          balance: "0",
          nonce: "0",
          at_block: hash,
          completeness: completeness(),
        });
      if (url.pathname.endsWith("/defi-positions")) {
        if (failure)
          return Response.json(
            { error: { code: "query_failed", message: "Unavailable", request_id: "test" } },
            { status: 503 },
          );
        return envelope(data);
      }
      if (url.pathname.endsWith("/erc20-balances")) return envelope([]);
      return notFound();
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());
it("deep-links direct positions without fetching token or NFT balances", async () => {
  renderExplorer(`/address/${owner}?tab=assets&asset=defi`);
  expect(await screen.findByText("9.007199254740993123")).toBeVisible();
  const region = screen.getByRole("region", { name: "DeFi positions" });
  expect(within(region).getByRole("heading", { name: "Debt" })).toBeVisible();
  expect(
    within(region).queryByRole("heading", { name: "Underlying assets" }),
  ).not.toBeInTheDocument();
  expect(requests.some((url) => /\/(nfts|erc20-balances)$/.test(url.pathname))).toBe(false);
  expect(screen.getByRole("link", { name: "DeFi" })).toHaveAttribute("aria-current", "page");
});
it.each(["scanning", "partial", "unavailable", "not_configured"] as const)(
  "never claims no assets for %s coverage",
  async (state) => {
    data.positions = [];
    data.deployments[0]!.state = state;
    renderExplorer(`/address/${owner}?tab=assets&asset=defi`);
    await screen.findByRole("heading", { name: "Aave V3" });
    expect(
      screen.queryByText("No positions in the selected completed snapshots."),
    ).not.toBeInTheDocument();
  },
);
it("queries only after opening DeFi and filters through the generated client", async () => {
  const user = userEvent.setup();
  renderExplorer(`/address/${owner}?tab=assets`);
  await screen.findByRole("link", { name: "DeFi" });
  expect(requests.some((url) => url.pathname.endsWith("/defi-positions"))).toBe(false);
  await user.click(screen.getByRole("link", { name: "DeFi" }));
  await screen.findByRole("heading", { name: "Aave V3" });
  await user.selectOptions(screen.getByLabelText("Protocol"), "aave-v3");
  await waitFor(() =>
    expect(requests.some((url) => url.searchParams.get("protocol") === "aave-v3")).toBe(true),
  );
});
it("shows a typed query error without stale balances", async () => {
  failure = true;
  renderExplorer(`/address/${owner}?tab=assets&asset=defi`);
  await screen.findByText("Explorer API is not available yet");
  expect(screen.queryByText("9.007199254740993123")).not.toBeInTheDocument();
});
it("localizes the new tab and quantity groups", async () => {
  await i18n.changeLanguage("zh");
  renderExplorer(`/address/${owner}?tab=assets&asset=defi`);
  expect(await screen.findByRole("heading", { name: "债务" })).toBeVisible();
  expect(screen.getByLabelText("协议")).toBeVisible();
});
