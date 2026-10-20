import { render, screen, within } from "@testing-library/react";
import { beforeEach, expect, it } from "vitest";
import type { components } from "@/api/schema.gen";
import i18n from "@/i18n";
import { ConsensusOverview } from "./ConsensusPanels";

const now = Date.parse("2026-10-20T00:00:00Z");
const root = `0x${"ab".repeat(32)}`;
const overview: components["schemas"]["ConsensusOverview"] = {
  state: "complete",
  stale: false,
  stale_at: "2026-10-20T00:00:10Z",
  blobs_enabled: true,
  blob_state: "failed",
  blob_code: "blob_storage_unavailable",
  slot: "9007199254740993",
  epoch: "281474976710656",
  finalized: { epoch: "281474976710654", root },
  justified: { epoch: "281474976710655", root },
  root,
  observed_at: "2026-10-19T23:59:50Z",
  syncing: false,
  execution_optimistic: false,
};

beforeEach(async () => {
  await i18n.changeLanguage("en");
});

it.each(["en", "zh"])(
  "combines execution finality and consensus observations in %s",
  async (language) => {
    await i18n.changeLanguage(language);
    render(
      <ConsensusOverview
        data={overview}
        safeBlock="9007199254740995"
        finalizedBlock="9007199254740994"
        now={now}
      />,
    );
    const panel = screen.getByRole("region", {
      name: language === "en" ? "Consensus & finality" : "共识与最终性",
    });
    expect(
      within(panel)
        .getAllByRole("term")
        .map((term) => term.textContent),
    ).toEqual([
      language === "en" ? "Safe block" : "安全区块",
      language === "en" ? "Finalized block" : "已最终确认区块",
      "Slot",
      "Epoch",
      language === "en" ? "Finalized epoch" : "已最终确定的 Epoch",
    ]);
    expect(
      within(panel)
        .getAllByRole("definition")
        .map((value) => value.textContent),
    ).toEqual([
      "9,007,199,254,740,995",
      "9,007,199,254,740,994",
      "9,007,199,254,740,993",
      "281,474,976,710,656",
      "281,474,976,710,654",
    ]);
    expect(within(panel).queryByRole("status")).not.toBeInTheDocument();
    expect(panel).not.toHaveTextContent(/Available|可用|Blob|Justified|justified/);
    expect(panel).not.toHaveTextContent(root);
    expect(panel).not.toHaveTextContent(overview.observed_at!);
  },
);

it("retains execution finality when consensus is absent or disabled", () => {
  const { rerender } = render(<ConsensusOverview safeBlock="42" finalizedBlock="40" now={now} />);
  expect(screen.getByRole("region", { name: "Finality" })).toBeVisible();
  expect(screen.getAllByRole("definition").map((value) => value.textContent)).toEqual(["42", "40"]);
  rerender(
    <ConsensusOverview
      data={{ ...overview, state: "disabled", stale: true }}
      safeBlock="43"
      finalizedBlock="41"
      now={now}
    />,
  );
  expect(screen.getAllByRole("definition").map((value) => value.textContent)).toEqual(["43", "41"]);
  expect(screen.queryByRole("status")).not.toBeInTheDocument();
  expect(screen.queryByText("Slot")).not.toBeInTheDocument();
});

it("uses placeholders for absent values and preserves zero", () => {
  const { rerender } = render(
    <ConsensusOverview
      data={{ state: "pending", stale: false, blobs_enabled: false, blob_state: "disabled" }}
      now={now}
    />,
  );
  expect(screen.getAllByRole("definition").map((value) => value.textContent)).toEqual([
    "—",
    "—",
    "—",
    "—",
    "—",
  ]);
  rerender(
    <ConsensusOverview
      data={{ ...overview, slot: "0", epoch: "0", finalized: { epoch: "0", root } }}
      safeBlock="0"
      finalizedBlock="0"
      now={now}
    />,
  );
  expect(screen.getAllByRole("definition").map((value) => value.textContent)).toEqual([
    "0",
    "0",
    "0",
    "0",
    "0",
  ]);
});

it.each([
  ["pending", "Awaiting data"],
  ["unavailable", "Consensus data unavailable"],
  ["failed", "Collection failed"],
  ["not_applicable", "Not applicable"],
] as const)("explains %s while retaining the last observation", (state, message) => {
  render(<ConsensusOverview data={{ ...overview, state }} now={now} />);
  expect(screen.getByRole("status")).toHaveTextContent(message);
  expect(screen.getByText("9,007,199,254,740,993")).toBeVisible();
});

it.each(["en", "zh"])(
  "keeps sync, optimistic and stale warnings visible in %s",
  async (language) => {
    await i18n.changeLanguage(language);
    render(
      <ConsensusOverview
        data={{ ...overview, syncing: true, execution_optimistic: true, stale: true }}
        now={now}
      />,
    );
    const status = screen.getByRole("status");
    for (const message of language === "en"
      ? ["Beacon node syncing", "Execution not fully verified", "Data is out of date"]
      : ["共识节点同步中", "执行状态尚未完全验证", "数据已过期"]) {
      expect(status).toHaveTextContent(message);
    }
  },
);

it("expires the displayed snapshot without another response, then clears on a fresh observation", () => {
  const { rerender } = render(<ConsensusOverview data={overview} now={now} />);
  expect(screen.queryByRole("status")).not.toBeInTheDocument();
  rerender(<ConsensusOverview data={overview} now={now + 11_000} />);
  expect(screen.getByRole("status")).toHaveTextContent("Data is out of date");
  expect(screen.getByText("9,007,199,254,740,993")).toBeVisible();
  rerender(
    <ConsensusOverview
      data={{ ...overview, stale_at: "2026-10-20T00:01:00Z" }}
      now={now + 11_000}
    />,
  );
  expect(screen.queryByRole("status")).not.toBeInTheDocument();
});

it("does not substitute consensus checkpoints for execution finality during an outage", () => {
  render(
    <ConsensusOverview
      data={{ ...overview, state: "unavailable", stale: true }}
      safeBlock="42"
      now={now}
    />,
  );
  expect(screen.getAllByRole("definition").map((value) => value.textContent)).toEqual([
    "42",
    "—",
    "9,007,199,254,740,993",
    "281,474,976,710,656",
    "281,474,976,710,654",
  ]);
  expect(screen.getByRole("status")).toHaveTextContent("Consensus data unavailable");
});
