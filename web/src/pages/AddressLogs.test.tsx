import type { AddressLogEvent } from "@/api/heyapi/types.gen";
import solidityFixture from "../contracts/fixtures/indexed-events.json";
import { parseVerifiedABI } from "@/contracts/abi";
import { toEventSignature } from "viem";
import { keccak256, stringToHex } from "viem";
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
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

const address = `0x${"11".repeat(20)}`;
const hash = `0x${"aa".repeat(32)}`;
const topic = `0x${"bb".repeat(32)}`;
let requests: URL[] = [];
let accountType = "contract";
let directoryPages = false;
let directoryFailure = false;
let logCoverage: Record<string, string> | undefined;
let delayedDirectory: ((response: Response) => void) | undefined;
const log = {
  block_number: "5",
  block_hash: hash,
  transaction_hash: hash,
  transaction_index: "0",
  block_timestamp: "2026-01-01T00:00:00Z",
  log: {
    address,
    log_index: "0",
    topics: [topic],
    data: "0x",
    decoding: {
      status: "unknown",
      arguments: [],
      candidates: [],
      attribution: { mode: "address_fallback", trace_path: [] },
    },
  },
};
let typedDirectory = false;
let extraDefinition: AddressLogEvent | undefined;
beforeEach(async () => {
  typedDirectory = false;
  extraDefinition = undefined;
  await i18n.changeLanguage("en");
  requests = [];
  accountType = "contract";
  directoryPages = false;
  directoryFailure = false;
  logCoverage = undefined;
  delayedDirectory = undefined;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = requestURL(input);
      requests.push(url);
      if (url.pathname === "/api/v1/config") return configResponse();
      if (url.pathname === `/api/v1/addresses/${address}`)
        return envelope({
          address,
          type: accountType,
          code_hash: hash,
          balance: "0",
          nonce: "0",
          at_block: hash,
          completeness: completeness(),
        });
      if (url.pathname.endsWith("/log-events")) {
        if (extraDefinition) return envelope([extraDefinition]);
        if (typedDirectory) {
          const definitions = parseVerifiedABI(solidityFixture.abi)
            .filter((event) => event.type === "event")
            .map((event) => {
              let position = event.anonymous ? 0 : 1;
              return {
                ...eventFixture(event.name, toEventSignature(event)),
                anonymous: event.anonymous ?? false,
                indexed_fields: event.inputs.flatMap((field, parameter_index) =>
                  field.indexed
                    ? [
                        {
                          ...field,
                          name: field.name ?? "",
                          parameter_index,
                          topic_index: position++,
                        },
                      ]
                    : [],
                ),
              };
            });
          return envelope(
            url.searchParams.has("event_id")
              ? definitions.filter((item) => item.event_id === url.searchParams.get("event_id"))
              : definitions,
          );
        }

        if (directoryFailure)
          return new Response(
            JSON.stringify({ error: { code: "query_failed", message: "Query failed" } }),
            { status: 500, headers: { "content-type": "application/json" } },
          );
        if (url.searchParams.get("name") === "slow")
          return new Promise<Response>((resolve) => {
            delayedDirectory = resolve;
          });
        if (url.searchParams.get("name") === "fast")
          return envelope([eventFixture("Fast", "Fast(uint256)")]);
        if (directoryPages)
          return url.searchParams.has("cursor")
            ? envelope([eventFixture("Hidden", "Hidden(bytes)")])
            : envelope([eventFixture("Transfer", "Transfer(bytes32)")], {
                next_cursor: "names-next",
              });
        return envelope([
          eventFixture("Transfer", "Transfer(address,uint256)"),
          eventFixture("Transfer", "Transfer(bytes32)"),
        ]);
      }
      if (url.pathname.endsWith("/logs")) {
        if (logCoverage) return envelope([], { browsing: logCoverage });
        return envelope(
          [{ ...log, block_number: url.searchParams.has("cursor") ? "4" : "5" }],
          url.searchParams.has("cursor") ? {} : { next_cursor: "next" },
        );
      }
      if (url.pathname.endsWith("/transactions")) return envelope([]);
      return notFound();
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

it("filters historical signatures and resets pagination, then switches exclusively to positioned topics", async () => {
  const user = userEvent.setup();
  const { router } = renderExplorer(`/address/${address}?tab=logs`);
  await screen.findByText("Block 5");
  await user.click(screen.getByRole("button", { name: "Next page" }));
  await screen.findByText("Block 4");
  await user.click(screen.getByRole("button", { name: /Event signature:/ }));
  await user.click(await screen.findByRole("option", { name: /Transfer\(bytes32\)/ }));
  await user.click(screen.getByRole("button", { name: "Query" }));
  await waitFor(() =>
    expect(router.state.location.search.event_id).toBe(
      eventFixture("Transfer", "Transfer(bytes32)").event_id,
    ),
  );
  await screen.findByText("Block 5");
  expect(
    requests
      .filter((url) => url.pathname.endsWith("/logs"))
      .at(-1)
      ?.searchParams.has("cursor"),
  ).toBe(false);
  await user.click(screen.getByRole("button", { name: "Topic" }));
  await user.click(screen.getByRole("button", { name: "Add condition" }));
  await user.selectOptions(await screen.findByLabelText("Topic position"), "3");
  expect(screen.getByLabelText("Topic position")).toHaveFocus();
  await user.type(screen.getByLabelText("Topic hash"), topic);
  await user.click(screen.getByRole("button", { name: "Query" }));
  await waitFor(() => expect(router.state.location.search.topic3).toBe(topic));
  expect(router.state.location.search.event_signature).toBeUndefined();
  const request = requests.filter((url) => url.pathname.endsWith("/logs")).at(-1)!;
  expect(request.searchParams.get("topic3")).toBe(topic);
  expect(request.searchParams.has("event_signature")).toBe(false);
});

it("restores topic deep links and rejects invalid input without a request", async () => {
  const user = userEvent.setup();
  renderExplorer(`/address/${address}?tab=logs&log_mode=topic&topic2=${topic}`);
  expect(await screen.findByLabelText("Topic position")).toHaveValue("2");
  expect(screen.getByLabelText("Topic hash")).toHaveValue(topic);
  await screen.findByText("Block 5");
  const count = requests.filter((url) => url.pathname.endsWith("/logs")).length;
  await user.clear(screen.getByLabelText("Topic hash"));
  await user.type(screen.getByLabelText("Topic hash"), "0x12");
  await user.click(screen.getByRole("button", { name: "Query" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("64 hexadecimal");
  expect(requests.filter((url) => url.pathname.endsWith("/logs"))).toHaveLength(count);
  await act(async () => {
    await i18n.changeLanguage("zh");
  });
  expect(screen.getByLabelText("Topic 位置")).toHaveValue("2");
});

it.each(["topic2", "event_id"])(
  "preserves an invalid numeric %s URL filter without broadening the query",
  async (key) => {
    renderExplorer(`/address/${address}?tab=logs&${key}=1`);
    await screen.findByText("Block 5");
    const logs = requests.filter((url) => url.pathname.endsWith("/logs"));
    expect(logs.length).toBeGreaterThan(0);
    expect(logs.every((url) => url.searchParams.get(key) === "1")).toBe(true);
  },
);

it("searches the directory by name prefix and hides the tab for EOAs", async () => {
  accountType = "eoa";
  renderExplorer(`/address/${address}?tab=logs`);
  await waitFor(() =>
    expect(requests.some((url) => url.pathname.endsWith("/transactions"))).toBe(true),
  );
  expect(screen.queryByRole("link", { name: "Event Logs" })).not.toBeInTheDocument();
  expect(requests.some((url) => /\/(logs|log-events)$/.test(url.pathname))).toBe(false);
});

it("keeps range and mode edits as drafts until query, and searches inside the dropdown", async () => {
  const user = userEvent.setup();
  const { router } = renderExplorer(`/address/${address}?tab=logs&from_block=1&to_block=5`);
  await screen.findByText("Block 5");
  const count = requests.filter((u) => u.pathname.endsWith("/logs")).length;
  expect(screen.queryByRole("combobox", { name: "Search event names" })).not.toBeInTheDocument();
  await user.clear(screen.getByLabelText("From block"));
  await user.type(screen.getByLabelText("From block"), "2");
  await user.click(screen.getByRole("button", { name: /Event signature:/ }));
  const input = await screen.findByRole("combobox", { name: "Search event names" });
  await user.type(input, "Trans");
  await waitFor(() =>
    expect(
      requests.some(
        (u) =>
          u.pathname.endsWith("/log-events") &&
          u.searchParams.get("name") === "Trans" &&
          u.searchParams.get("from_block") === "2" &&
          u.searchParams.get("to_block") === "5",
      ),
    ).toBe(true),
  );
  await user.keyboard("{ArrowDown}{Enter}");
  expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  expect(requests.filter((u) => u.pathname.endsWith("/logs"))).toHaveLength(count);
  await user.click(screen.getByRole("button", { name: "Topic" }));
  expect(screen.getByLabelText("From block")).toHaveValue("2");
  expect(router.state.location.search.from_block).toBe("1");
  await user.click(screen.getByRole("button", { name: "Query" }));
  await waitFor(() => expect(router.state.location.search.from_block).toBe("2"));
  expect(router.state.location.search.to_block).toBe("5");
  expect(router.state.location.search.event_signature).toBeUndefined();
});

it("rejects invalid ranges without fetching and exposes compact log details on demand", async () => {
  const user = userEvent.setup();
  renderExplorer(`/address/${address}?tab=logs`);
  await screen.findByText("Block 5");
  const toggle = screen.getByRole("button", { expanded: false, name: /Log/ });
  expect(screen.queryByText("More details")).not.toBeInTheDocument();
  await user.click(toggle);
  expect(screen.getByText("More details")).toBeVisible();
  const count = requests.filter((u) => u.pathname.endsWith("/logs")).length;
  await user.type(screen.getByLabelText("From block"), "9007199254740993");
  await user.type(screen.getByLabelText("To block"), "9007199254740992");
  await user.click(screen.getByRole("button", { name: "Query" }));
  expect(screen.getByRole("alert")).toHaveTextContent("start must not exceed");
  expect(requests.filter((u) => u.pathname.endsWith("/logs"))).toHaveLength(count);
  await user.click(screen.getByRole("button", { name: "Reset" }));
  expect(screen.getByLabelText("From block")).toHaveValue("");
});

it("loads more names inside the dropdown and keeps keyboard focus on Escape", async () => {
  directoryPages = true;
  const user = userEvent.setup();
  renderExplorer(`/address/${address}?tab=logs`);
  await screen.findByText("Block 5");
  const trigger = screen.getByRole("button", { name: /Event signature:/ });
  await user.click(trigger);
  await user.click(await screen.findByRole("button", { name: "Load more events" }));
  expect(await screen.findByRole("option", { name: /Hidden\(bytes\)/ })).toBeVisible();
  expect(screen.getByRole("option", { name: /Transfer\(bytes32\)/ })).toBeVisible();
  await user.keyboard("{Escape}");
  expect(trigger).toHaveFocus();
  expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
});
it("ignores responses from an older dropdown search", async () => {
  const user = userEvent.setup();
  renderExplorer(`/address/${address}?tab=logs`);
  await screen.findByText("Block 5");
  await user.click(screen.getByRole("button", { name: /Event signature:/ }));
  const input = await screen.findByRole("combobox", { name: "Search event names" });
  await user.type(input, "slow");
  await waitFor(() => expect(delayedDirectory).toBeDefined());
  await user.clear(input);
  await user.type(input, "fast");
  expect(await screen.findByRole("option", { name: /Fast\(uint256\)/ })).toBeVisible();
  await act(async () => {
    delayedDirectory?.(envelope([eventFixture("Slow", "Slow(uint256)")]));
  });
  expect(screen.queryByRole("option", { name: /Slow\(uint256\)/ })).not.toBeInTheDocument();
  expect(screen.getByRole("option", { name: /Fast\(uint256\)/ })).toBeVisible();
});

it("restores unquoted uint256 block bounds without Number rounding", async () => {
  renderExplorer(
    `/address/${address}?tab=logs&from_block=9007199254740993&to_block=9007199254740995`,
  );
  expect(await screen.findByLabelText("From block")).toHaveValue("9007199254740993");
  expect(screen.getByLabelText("To block")).toHaveValue("9007199254740995");
  await waitFor(() =>
    expect(
      requests
        .filter((u) => u.pathname.endsWith("/logs"))
        .at(-1)
        ?.searchParams.get("from_block"),
    ).toBe("9007199254740993"),
  );
});

it("keeps oversized topic deep links invalid instead of querying a truncated hash", async () => {
  const user = userEvent.setup();
  renderExplorer(`/address/${address}?tab=logs&log_mode=topic&topic0=${topic}00`);
  const input = await screen.findByLabelText("Topic hash");
  expect((input as HTMLInputElement).value.length).toBeGreaterThan(66);
  await screen.findByText("Block 5");
  const sent = requests.filter((u) => u.pathname.endsWith("/logs"));
  expect(sent.at(-1)?.searchParams.get("topic0")).not.toBe(topic);
  await user.click(screen.getByRole("button", { name: "Query" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("64 hexadecimal");
  expect(requests.filter((u) => u.pathname.endsWith("/logs"))).toHaveLength(sent.length);
});

it("waits for IME composition before searching and keeps a selected signature absent from candidates", async () => {
  const user = userEvent.setup();
  renderExplorer(`/address/${address}?tab=logs&event_signature=Hidden(bytes)`);
  await screen.findByText("Block 5");
  await user.click(screen.getByRole("button", { name: "Event name" }));
  await user.click(screen.getByRole("button", { name: /Event signature: Hidden/ }));
  const input = await screen.findByRole("combobox", { name: "Search event names" });
  fireEvent.compositionStart(input);
  fireEvent.change(input, { target: { value: "事件" } });
  fireEvent.keyDown(input, { key: "Enter", isComposing: true });
  fireEvent.keyDown(input, { key: "Escape", isComposing: true });
  await act(() => new Promise((resolve) => setTimeout(resolve, 300)));
  expect(screen.getByRole("listbox")).toBeVisible();
  expect(requests.some((u) => u.searchParams.get("name") === "事件")).toBe(false);
  fireEvent.compositionEnd(input);
  await waitFor(() =>
    expect(requests.some((u) => u.searchParams.get("name") === "事件")).toBe(true),
  );
  await user.keyboard("{Escape}");
  expect(screen.getByRole("button", { name: /Event signature: Hidden\(bytes\)/ })).toHaveFocus();
});

it("retries directory failures inside the popup without changing applied logs", async () => {
  directoryFailure = true;
  const user = userEvent.setup();
  renderExplorer(`/address/${address}?tab=logs`);
  await screen.findByText("Block 5");
  const count = requests.filter((u) => u.pathname.endsWith("/logs")).length;
  await user.click(screen.getByRole("button", { name: /Event signature:/ }));
  const retry = await screen.findByRole("button", { name: "Retry" });
  directoryFailure = false;
  await user.click(retry);
  expect(await screen.findByRole("option", { name: /Transfer\(bytes32\)/ })).toBeVisible();
  expect(requests.filter((u) => u.pathname.endsWith("/logs"))).toHaveLength(count);
  await user.keyboard("{Escape}");
  expect(screen.getByRole("button", { name: /Event signature:/ })).toHaveFocus();
  expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
});

it("distinguishes future ranges and partial coverage from a complete empty result", async () => {
  logCoverage = { state: "pending", from_block: "10", to_block: "5" };
  const user = userEvent.setup();
  renderExplorer(`/address/${address}?tab=logs&from_block=10`);
  expect(await screen.findByText(/range is beyond the current indexed snapshot/i)).toBeVisible();
  expect(screen.queryByText(/Results through block/)).not.toBeInTheDocument();
  logCoverage = { state: "partial", from_block: "0", to_block: "5" };
  await user.click(screen.getByRole("button", { name: "Reset" }));
  expect(await screen.findByText("No synced data available yet.")).toBeVisible();
  expect(screen.getByText("Showing synced data; some data is not ready yet.")).toBeVisible();
  expect(screen.getByText("Results through block 5")).toBeVisible();
});

function eventFixture(name: string, signature: string) {
  return {
    name,
    signature,
    event_id: keccak256(stringToHex(signature)),
    anonymous: false,
    indexed_fields: [],
  };
}

it("combines named indexed addresses and supports selecting second without first", async () => {
  typedDirectory = true;
  const user = userEvent.setup();
  const { router } = renderExplorer(`/address/${address}?tab=logs`);
  await screen.findByText("Block 5");
  await user.click(screen.getByRole("button", { name: /Event signature:/ }));
  await user.click(
    await screen.findByRole("option", { name: /Example\(address,address,uint256\)/ }),
  );
  await user.click(screen.getByRole("button", { name: "Add condition" }));
  expect(screen.queryByRole("option", { name: /third/ })).not.toBeInTheDocument();
  await user.selectOptions(screen.getByLabelText("Indexed field"), "2");
  expect(screen.getByLabelText("Indexed field")).toHaveFocus();
  await user.type(
    screen.getByLabelText(/second/, { selector: "input" }),
    "0x0000000000000000000000000000000000000022",
  );
  await user.click(screen.getByRole("button", { name: "Query" }));
  await waitFor(() => expect(router.state.location.search.topic2).toBe(`0x${"0".repeat(62)}22`));
  expect(router.state.location.search.topic1).toBeUndefined();
  await user.click(screen.getByRole("button", { name: "Add condition" }));
  await user.type(
    screen.getByLabelText(/first/, { selector: "input" }),
    "0x0000000000000000000000000000000000000011",
  );
  await user.click(screen.getByRole("button", { name: "Query" }));

  await waitFor(() => expect(router.state.location.search.topic1).toBe(`0x${"0".repeat(62)}11`));
  expect(screen.getByLabelText(/second/, { selector: "input" })).toHaveValue(
    "0x0000000000000000000000000000000000000022",
  );
  await user.click(screen.getByRole("button", { name: "Remove condition second" }));
  await user.click(screen.getByRole("button", { name: "Query" }));
  await waitFor(() => expect(router.state.location.search.topic2).toBeUndefined());
});

it("hashes original empty string and bytes locally and restores shared links in hash mode", async () => {
  typedDirectory = true;
  const user = userEvent.setup();
  const { router } = renderExplorer(`/address/${address}?tab=logs`);
  await screen.findByText("Block 5");
  await user.click(screen.getByRole("button", { name: /Event signature:/ }));
  await user.click(await screen.findByRole("option", { name: /Text\(string,bytes\)/ }));
  await user.click(screen.getByRole("button", { name: "Add condition" }));
  await user.click(screen.getByRole("button", { name: "Add condition" }));
  await user.type(screen.getByLabelText(/data/, { selector: "textarea" }), "0x");
  await user.click(screen.getByRole("button", { name: "Query" }));
  const emptyHash = keccak256("0x");
  await waitFor(() => expect(router.state.location.search.topic1).toBe(emptyHash));
  expect(router.state.location.search.topic2).toBe(emptyHash);
  expect(screen.getByLabelText(/text/, { selector: "textarea" })).toHaveValue("");
  const request = requests.filter((url) => url.pathname.endsWith("/logs")).at(-1)!;
  expect([...request.searchParams.keys()].sort()).toEqual([
    "event_id",
    "limit",
    "topic1",
    "topic2",
  ]);
  await act(async () => {
    await router.navigate({
      to: "/address/$address",
      params: { address },
      search: {
        tab: "logs",
        log_mode: "name",
        event_id: eventFixture("Text", "Text(string,bytes)").event_id,
        topic1: topic,
      },
    });
  });
  expect(await screen.findByLabelText("Topic hash")).toHaveValue(topic);
  expect(screen.queryByRole("textbox", { name: /text string/ })).not.toBeInTheDocument();
});

it("edits tuple members and dynamic arrays through the shared ABI inputs", async () => {
  typedDirectory = true;
  const user = userEvent.setup();
  renderExplorer(`/address/${address}?tab=logs`);
  await screen.findByText("Block 5");
  await user.click(screen.getByRole("button", { name: /Event signature:/ }));
  await user.click(await screen.findByRole("option", { name: /Tuple\(/ }));
  await user.click(screen.getByRole("button", { name: "Add condition" }));
  await user.type(screen.getByLabelText(/amount/, { selector: "input" }), "-123");
  await user.type(screen.getByLabelText(/label/, { selector: "textarea" }), "abc");
  await user.type(screen.getByLabelText(/payload/, { selector: "textarea" }), "0xf00d");
  await user.click(screen.getByRole("button", { name: "Add array item" }));
  await user.type(screen.getAllByLabelText(/values/, { selector: "input" })[0]!, "1");
  await user.click(screen.getByRole("button", { name: "Add array item" }));
  await user.type(screen.getAllByLabelText(/values/, { selector: "input" })[1]!, "65535");
  await user.click(screen.getByRole("button", { name: "Query" }));
  await waitFor(() =>
    expect(
      requests
        .filter((url) => url.pathname.endsWith("/logs"))
        .at(-1)
        ?.searchParams.get("topic1"),
    ).toBe(solidityFixture.cases[3]!.topics[1]),
  );
});

it.each(["function", "uint256[257]"])(
  "keeps %s indexed fields searchable by exact topic when the raw editor cannot represent them",
  async (type) => {
    extraDefinition = {
      ...eventFixture("Callback", `Callback(${type})`),
      indexed_fields: [{ name: "value", type, parameter_index: 0, topic_index: 1 }],
    };
    const user = userEvent.setup();
    const { router } = renderExplorer(`/address/${address}?tab=logs`);
    await screen.findByText("Block 5");
    await user.click(screen.getByRole("button", { name: /Event signature:/ }));
    await user.click(await screen.findByRole("option", { name: /Callback/ }));
    await user.click(screen.getByRole("button", { name: "Add condition" }));
    expect(screen.getByRole("option", { name: "Original value" })).toBeDisabled();
    await user.type(screen.getByLabelText("Topic hash"), topic);
    await user.click(screen.getByRole("button", { name: "Query" }));
    await waitFor(() => expect(router.state.location.search.topic1).toBe(topic));
    expect(router.state.location.search.event_id).toBe(extraDefinition.event_id);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  },
);
