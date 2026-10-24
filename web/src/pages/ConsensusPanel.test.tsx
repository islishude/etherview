import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type * as schemas from "@/api/heyapi/types.gen";
import i18n from "@/i18n";
import { ConsensusPanel } from "./ConsensusPanels";

const hash = `0x${"11".repeat(32)}`;
const root = `0x${"22".repeat(32)}`;
const summary: schemas.ConsensusSummary = {
  block_hash: hash,
  state: "complete",
  slot: "9007199254740993",
  epoch: "281474976710656",
  proposer_index: "9007199254740994",
  fork: "fulu",
  root,
  parent_root: root,
  state_root: root,
  body_root: root,
  graffiti: `0x68656c6c6f${"00".repeat(27)}`,
  operations: { attestations: "9007199254740995", deposits: "0" },
};

beforeEach(async () => {
  await i18n.changeLanguage("en");
});
afterEach(() => vi.unstubAllGlobals());

function renderPanel(data: schemas.ConsensusSummary, status = 200) {
  const fetchMock = vi.fn(async (request: RequestInfo | URL) => {
    expect(new URL(String(request), "http://etherview.test").pathname).toBe(
      `/api/v1/blocks/${hash}/consensus`,
    );
    return Response.json(
      status === 200
        ? { data, meta: { chain_id: "1", request_id: "consensus-test" } }
        : { error: { code: "unavailable", message: "Unavailable" } },
      { status },
    );
  });
  vi.stubGlobal("fetch", fetchMock);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <ConsensusPanel hash={hash} />
    </QueryClientProvider>,
  );
  return fetchMock;
}

it.each(["en", "zh"])(
  "renders a compact %s summary with exact, expandable details",
  async (language) => {
    await i18n.changeLanguage(language);
    renderPanel(summary);
    const panel = screen.getByRole("tabpanel");
    await within(panel).findByText("9,007,199,254,740,993");
    expect(within(panel).getByText("9,007,199,254,740,994")).toBeVisible();
    expect(within(panel).getByText("281,474,976,710,656")).toBeVisible();
    expect(within(panel).getByText("fulu")).toBeVisible();
    expect(within(panel).queryByRole("heading")).not.toBeInTheDocument();
    expect(within(panel).queryByRole("status")).not.toBeInTheDocument();
    expect(panel).not.toHaveTextContent(/Available|可用/);
    const disclosure = screen.getByText(language === "en" ? "Technical details" : "技术详情");
    expect(disclosure.closest("details")).not.toHaveAttribute("open");
    for (const value of screen.getAllByText(root)) expect(value).not.toBeVisible();
    await userEvent.setup().click(disclosure);
    for (const value of screen.getAllByText(root)) expect(value).toBeVisible();
    expect(screen.getByText(summary.graffiti!)).toBeVisible();
    expect(screen.getByText("hello")).toBeVisible();
    expect(screen.getByText("9,007,199,254,740,995")).toBeVisible();
    expect(screen.getByText("0")).toBeVisible();
  },
);

it.each([
  ["pending", "Awaiting collection"],
  ["disabled", "Not enabled"],
  ["unavailable", "Unavailable from the source"],
  ["failed", "Collection failed"],
  ["not_applicable", "Not applicable"],
] as const)("explains %s without empty metric rows", async (state, message) => {
  renderPanel({ block_hash: hash, state });
  expect(await screen.findByText(message)).toBeVisible();
  expect(screen.queryByRole("definition")).not.toBeInTheDocument();
  expect(screen.queryByText("Technical details")).not.toBeInTheDocument();
});

it("keeps the slot conflict and source failure explanation visible", async () => {
  renderPanel({
    ...summary,
    state: "unavailable",
    code: "beacon_not_found",
    slot_conflict: true,
  });
  expect(await screen.findByText("Historical data is unavailable at this node.")).toBeVisible();
  expect(screen.getByText(/Consensus and execution slots differ/)).toBeVisible();
  expect(screen.getByText("9,007,199,254,740,993")).toBeVisible();
});

it("preserves zero and distinguishes missing values", async () => {
  renderPanel({ block_hash: hash, state: "complete", slot: "0", proposer_index: "0" });
  await screen.findAllByText("0");
  expect(screen.getAllByRole("definition").map((node) => node.textContent)).toEqual([
    "0",
    "—",
    "0",
    "—",
  ]);
});

it("shows a request failure without an empty detail card", async () => {
  renderPanel(summary, 503);
  expect(await screen.findByText("Explorer API is not available yet")).toBeVisible();
  expect(screen.queryByRole("definition")).not.toBeInTheDocument();
});
