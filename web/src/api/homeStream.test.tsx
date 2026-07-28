import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  parseHomeSnapshot,
  useHomeSnapshotStream,
} from "./homeStream";

class FakeEventSource {
  static instances: FakeEventSource[] = [];

  readonly url: string;
  closed = false;
  private readonly listeners = new Map<string, Set<EventListener>>();

  constructor(url: string | URL) {
    this.url = String(url);
    FakeEventSource.instances.push(this);
  }

  addEventListener(type: string, listener: EventListenerOrEventListenerObject | null) {
    if (typeof listener !== "function") return;
    const listeners = this.listeners.get(type) ?? new Set<EventListener>();
    listeners.add(listener);
    this.listeners.set(type, listeners);
  }

  removeEventListener(type: string, listener: EventListenerOrEventListenerObject | null) {
    if (typeof listener === "function") {
      this.listeners.get(type)?.delete(listener);
    }
  }

  close() {
    this.closed = true;
  }

  emit(type: string, data = "") {
    const event = type === "snapshot"
      ? new MessageEvent(type, { data })
      : new Event(type);
    for (const listener of this.listeners.get(type) ?? []) {
      listener(event);
    }
  }
}

function Probe() {
  const stream = useHomeSnapshotStream();
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

describe("home snapshot EventSource", () => {
  beforeEach(() => {
    FakeEventSource.instances = [];
    vi.stubGlobal("EventSource", FakeEventSource as unknown as typeof EventSource);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("uses one same-origin stream and atomically replaces complete snapshots", () => {
    const view = render(<Probe />);
    expect(screen.getByText("pending")).toBeVisible();
    expect(FakeEventSource.instances).toHaveLength(1);
    const source = FakeEventSource.instances[0]!;
    expect(source.url).toBe("/api/v1/home/stream");

    act(() => source.emit("snapshot", JSON.stringify(snapshot("10", ["10", "9"]))));
    expect(screen.getByText("10:10,9")).toBeVisible();

    act(() => source.emit("snapshot", JSON.stringify(snapshot("11", ["11"]))));
    expect(screen.getByText("11:11")).toBeVisible();
    expect(screen.queryByText(/10,9/)).not.toBeInTheDocument();

    act(() => source.emit("snapshot", `{"data":{"status":{}}}`));
    expect(screen.getByText("11:11")).toBeVisible();

    act(() => source.emit("error"));
    expect(screen.getByText("11:11")).toBeVisible();
    act(() => source.emit("snapshot", JSON.stringify(snapshot("12", ["12"]))));
    expect(screen.getByText("12:12")).toBeVisible();
    expect(FakeEventSource.instances).toHaveLength(1);

    view.unmount();
    expect(source.closed).toBe(true);
  });

  it("shows an error when the connection fails before a valid snapshot", () => {
    render(<Probe />);
    act(() => FakeEventSource.instances[0]!.emit("error"));
    expect(screen.getByText("error")).toBeVisible();
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
});

function snapshot(latest: string, blockNumbers: string[]) {
  const completeness = {
    core: "complete",
    trace: "unavailable",
    metadata: "pending",
    state: "complete",
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
