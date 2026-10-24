import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { BlockRequests, ExecutionRequest } from "@/api/heyapi/types.gen";
import i18n from "@/i18n";
import { RequestsPanel } from "./RequestsPanel";

vi.mock("@/ens/AddressIdentity", () => ({
  AddressIdentity: ({ address }: { address: string }) => (
    <a href={`/address/${address}`}>{address}</a>
  ),
}));
const hex = (bytes: number) => `0x${"12".repeat(bytes)}`;
const hash = hex(32);
const items: ExecutionRequest[] = [
  {
    request_type: "0x00",
    sequence_index: "0",
    pubkey: hex(48),
    withdrawal_credentials: hex(32),
    amount: "18446744073709551615",
    signature: hex(96),
    index: "9007199254740993",
  },
  {
    request_type: "0x01",
    sequence_index: "0",
    source_address: hex(20),
    validator_pubkey: hex(48),
    amount: "0",
  },
  {
    request_type: "0x02",
    sequence_index: "0",
    source_address: hex(20),
    source_pubkey: hex(48),
    target_pubkey: hex(48),
  },
  {
    request_type: "0x03",
    sequence_index: "0",
    pubkey: hex(48),
    withdrawal_credentials: hex(32),
    amount: "32000000000",
    signature: hex(96),
  },
  { request_type: "0x04", sequence_index: "0", source_address: hex(20), pubkey: hex(48) },
];
const data: BlockRequests = {
  block_hash: hash,
  state: "complete",
  total: "5",
  counts: { "0x00": "1", "0x01": "1", "0x02": "1", "0x03": "1", "0x04": "1" },
  items,
  requests_hash: hash,
  beacon_root: hash,
};
beforeEach(async () => {
  await i18n.changeLanguage("en");
});
afterEach(() => vi.unstubAllGlobals());
function mount(value: BlockRequests, next?: string) {
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), "http://etherview.test");
    expect(url.pathname).toBe(`/api/v1/blocks/${hash}/requests`);
    const type = url.searchParams.get("request_type");
    return Response.json({
      data: {
        ...value,
        items: type ? value.items.filter((item) => item.request_type === type) : value.items,
      },
      meta: { chain_id: "1", request_id: "requests", next_cursor: next },
    });
  });
  vi.stubGlobal("fetch", fetchMock);
  render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <RequestsPanel hash={hash} />
    </QueryClientProvider>,
  );
  return fetchMock;
}
it.each(["en", "zh"])(
  "renders all five %s types with exact fields and disclosure",
  async (language) => {
    await i18n.changeLanguage(language);
    mount(data);
    const summaries = await screen.findAllByText(/#0/);
    expect(summaries).toHaveLength(5);
    for (const summary of summaries) await userEvent.click(summary);
    expect(screen.getByText("18,446,744,073,709,551,615 Gwei")).toBeVisible();
    expect(screen.getByText("9,007,199,254,740,993")).toBeVisible();
    for (const signature of screen.getAllByText(hex(96))) expect(signature).toBeVisible();
    expect(
      screen.getByText(language === "en" ? /Full exit request/ : /完全退出请求/),
    ).toBeVisible();
    expect(screen.getAllByRole("link")).toHaveLength(3);
    expect(
      screen.getAllByRole("button", { name: language === "en" ? "Copy" : "复制" }).length,
    ).toBeGreaterThan(5);
  },
);
it("binds filters and pagination to exact block, and resets the cursor on filter changes", async () => {
  const fetchMock = mount(data, "page-two");
  await screen.findByText(/Total requests/);
  await userEvent.click(screen.getByRole("button", { name: /Next/i }));
  await vi.waitFor(() =>
    expect(String(fetchMock.mock.calls.at(-1)?.[0])).toContain("cursor=page-two"),
  );
  await userEvent.selectOptions(screen.getByRole("combobox"), "0x04");
  await vi.waitFor(() => {
    const url = String(fetchMock.mock.calls.at(-1)?.[0]);
    expect(url).toContain("request_type=0x04");
    expect(url).not.toContain("cursor=");
  });
  expect(await screen.findAllByText(/#0/)).toHaveLength(1);
});
it.each(["disabled", "pending", "not_applicable", "unavailable", "failed"] as const)(
  "does not display an empty result for %s",
  async (state) => {
    mount({ block_hash: hash, state, items: [] });
    await screen.findByRole("status");
    expect(screen.queryByText(/No requests/)).not.toBeInTheDocument();
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
  },
);
it("only verified complete empty sets display the empty state", async () => {
  mount({ ...data, items: [], total: "0" });
  expect(await screen.findByText("No requests match this selection.")).toBeVisible();
  expect(within(screen.getByRole("tabpanel")).queryByRole("status")).not.toBeInTheDocument();
});
