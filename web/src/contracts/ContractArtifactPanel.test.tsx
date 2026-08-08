import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { encodeAbiParameters } from "viem";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import i18n from "@/i18n";

import {
  ContractArtifactPanel,
  parseArtifactSources,
  summarizeABI,
  summarizeCompilerSettings,
} from "./ContractArtifactPanel";
import type { VerifiedContractArtifact } from "./proxy";

beforeEach(async () => {
  await i18n.changeLanguage("en");
});

afterEach(() => {
  Reflect.deleteProperty(document, "fullscreenEnabled");
  Reflect.deleteProperty(document, "fullscreenElement");
  Reflect.deleteProperty(document, "exitFullscreen");
  Reflect.deleteProperty(HTMLElement.prototype, "requestFullscreen");
});

describe("contract artifact view models", () => {
  it("orders the main source first and rejects non-inline source entries", () => {
    expect(parseArtifactSources({
      "src/Z.sol": { content: "contract Z {}" },
      "src/Main.sol": { content: "contract Main {}" },
      "src/A.sol": { content: "contract A {}" },
      "src/Remote.sol": { urls: ["https://example.invalid/Remote.sol"] },
    }, "src/Main.sol")).toEqual({
      files: [
        { name: "src/Main.sol", content: "contract Main {}" },
        { name: "src/A.sol", content: "contract A {}" },
        { name: "src/Z.sol", content: "contract Z {}" },
      ],
      invalidEntries: 1,
    });
  });

  it("summarizes bounded ABI and explicit compiler settings", () => {
    expect(summarizeABI([
      { type: "function" }, { type: "function" }, { type: "event" },
      { type: "error" }, { type: "constructor" }, null,
    ])).toEqual({ functions: 2, events: 1, errors: 1, constructors: 1 });
    expect(summarizeCompilerSettings({
      optimizer: { enabled: true, runs: 500 },
      evmVersion: "cancun",
      viaIR: true,
      metadata: { bytecodeHash: "ipfs", appendCBOR: false, nested: {} },
      remappings: ["@openzeppelin/=lib/openzeppelin/", 4],
    })).toEqual({
      optimizerEnabled: true,
      optimizerRuns: 500,
      evmVersion: "cancun",
      viaIR: true,
      metadata: [
        { label: "bytecodeHash", value: "ipfs" },
        { label: "appendCBOR", value: "false" },
      ],
      remappings: ["@openzeppelin/=lib/openzeppelin/"],
    });
  });
});

describe("ContractArtifactPanel", () => {
  it("shows decoded constructor parameters and preserves copyable raw encoding", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
    const encoded = encodeAbiParameters(
      [{ name: "owner", type: "address" }, { name: "count", type: "uint256" }],
      ["0x1111111111111111111111111111111111111111", 42n],
    );

    render(<ContractArtifactPanel artifact={fixtureArtifact({
      constructor_arguments: encoded,
      abi: [
        { type: "constructor", stateMutability: "nonpayable", inputs: [
          { name: "owner", type: "address" }, { name: "count", type: "uint256" },
        ] },
      ],
    })} />);

    await user.click(screen.getByText("Constructor arguments"));
    const decoded = screen.getByRole("region", { name: "Decoded parameters" });
    expect(within(decoded).getByText("owner · address")).toBeVisible();
    expect(within(decoded).getByText("0x1111111111111111111111111111111111111111")).toBeVisible();
    expect(within(decoded).getByText("count · uint256")).toBeVisible();
    expect(within(decoded).getByText("42")).toBeVisible();
    const raw = screen.getByRole("region", { name: "Raw encoded arguments" });
    expect(within(raw).getByText(encoded)).toBeVisible();
    await user.click(within(raw).getByRole("button", { name: "Copy" }));
    expect(writeText).toHaveBeenCalledWith(encoded);
  });

  it("retains raw constructor arguments when ABI decoding is unavailable", async () => {
    const user = userEvent.setup();
    const encoded = `0x${"00".repeat(32)}`;
    render(<ContractArtifactPanel artifact={fixtureArtifact({
      constructor_arguments: encoded,
      abi: [],
    })} />);

    await user.click(screen.getByText("Constructor arguments"));
    expect(screen.getByRole("status")).toHaveTextContent(
      "ABI decoding is unavailable; showing the raw encoding only.",
    );
    expect(screen.getByRole("region", { name: "Raw encoded arguments" })).toHaveTextContent(encoded);
  });

  it("renders a read-only multi-file editor and structured settings", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });

    render(<ContractArtifactPanel artifact={fixtureArtifact()} />);

    expect(screen.getByRole("heading", { name: "Example" })).toBeVisible();
    expect(screen.getByText("Full match")).toBeVisible();
    expect(screen.getAllByText("Enabled")).toHaveLength(2);
    expect(screen.getByText("500")).toBeVisible();
    expect(screen.getByText("cancun")).toBeVisible();
    expect(screen.getByText("@openzeppelin/=lib/openzeppelin/")).toBeVisible();
    expect(screen.getByText(/2 functions · 1 events · 1 errors · 1 constructors/u)).toBeVisible();
    expect(screen.getByText("Optimizer details")).toBeVisible();
    expect(screen.getByText("Output selection")).toBeVisible();
    expect(screen.getByText("Model checker")).toBeVisible();
    expect(screen.getByText("Linked library addresses")).toBeVisible();

    const mainEditor = screen.getByRole("textbox", {
      name: "Read-only source editor for src/Example.sol",
    });
    expect(mainEditor).toHaveAttribute("contenteditable", "false");
    expect(mainEditor).toHaveTextContent("contract Example");
    expect(mainEditor.querySelector(".tok-keyword")).toHaveTextContent("contract");

    const fileNavigation = screen.getByRole("complementary", { name: "Source files" });
    await user.click(within(fileNavigation).getByRole("button", { name: /Library\.sol/u }));
    const libraryEditor = screen.getByRole("textbox", {
      name: "Read-only source editor for src/Library.sol",
    });
    expect(libraryEditor).toHaveAttribute("contenteditable", "false");
    expect(libraryEditor).toHaveTextContent("library Library");

    const wrap = screen.getByRole("button", { name: "Wrap lines" });
    expect(wrap).toHaveAttribute("aria-pressed", "false");
    await user.click(wrap);
    expect(wrap).toHaveAttribute("aria-pressed", "true");

    const editorShell = document.querySelector(".source-editor-shell");
    expect(editorShell).not.toBeNull();
    await user.click(within(editorShell as HTMLElement).getByRole("button", { name: "Copy" }));
    expect(writeText).toHaveBeenCalledWith("library Library { function value() internal pure returns (uint256) { return 1; } }");
  });

  it("fails closed for malformed sources while retaining raw diagnostics", async () => {
    const user = userEvent.setup();
    render(<ContractArtifactPanel artifact={fixtureArtifact({
      file_name: "Broken.sol",
      sources: { "Broken.sol": { urls: ["file:///private/source.sol"] } },
    })} />);

    expect(screen.getByRole("alert")).toHaveTextContent("No verified source entry");
    expect(screen.queryByRole("textbox", { name: /read-only source editor/iu })).toBeNull();
    await user.click(screen.getByText("Raw source manifest"));
    expect(screen.getByText(/file:\/\/\/private\/source\.sol/u)).toBeVisible();
  });

  it("renders legacy match details with null transformations", () => {
    render(<ContractArtifactPanel artifact={fixtureArtifact({
      creation_match: {
        match_type: "full",
        transformations: null as never,
        values: {},
      },
    })} />);

    expect(screen.getAllByText("0 declared transformations")).toHaveLength(2);
    expect(screen.getByRole("heading", { name: "Example" })).toBeVisible();
  });

  it("supports Yul search and browser fullscreen without enabling edits", async () => {
    const user = userEvent.setup();
    let fullscreenElement: Element | null = null;
    const requestFullscreen = vi.fn(async function (this: Element) {
      fullscreenElement = this;
      document.dispatchEvent(new Event("fullscreenchange"));
    });
    const exitFullscreen = vi.fn(async () => {
      fullscreenElement = null;
      document.dispatchEvent(new Event("fullscreenchange"));
    });
    Object.defineProperties(document, {
      fullscreenEnabled: { configurable: true, value: true },
      fullscreenElement: { configurable: true, get: () => fullscreenElement },
      exitFullscreen: { configurable: true, value: exitFullscreen },
    });
    Object.defineProperty(HTMLElement.prototype, "requestFullscreen", {
      configurable: true,
      value: requestFullscreen,
    });

    render(<ContractArtifactPanel artifact={fixtureArtifact({
      language: "yul",
      file_name: "main.yul",
      sources: { "main.yul": { content: "object \"Main\" { code { let value := 1 } }" } },
    })} />);

    const editor = screen.getByRole("textbox", { name: "Read-only source editor for main.yul" });
    expect(editor).toHaveAttribute("contenteditable", "false");
    expect(editor.querySelector(".tok-keyword")).toHaveTextContent("object");
    await user.click(screen.getByRole("button", { name: "Search" }));
    expect(document.querySelector(".cm-search")).not.toBeNull();

    await user.click(screen.getByRole("button", { name: "Fullscreen" }));
    expect(requestFullscreen).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: "Exit fullscreen" })).toHaveAttribute("aria-pressed", "true");
    await user.click(screen.getByRole("button", { name: "Exit fullscreen" }));
    expect(exitFullscreen).toHaveBeenCalledTimes(1);

  });
});

function fixtureArtifact(
  overrides: Partial<VerifiedContractArtifact> = {},
): VerifiedContractArtifact {
  return {
    kind: "verification_success",
		resolution: "exact_address",
		target: {
			chain_id: "1",
			address: "0x1111111111111111111111111111111111111111",
			code_hash: `0x${"ab".repeat(32)}`,
			block_number: "2",
			block_hash: `0x${"cd".repeat(32)}`,
		},
		source: {
			address: "0x1111111111111111111111111111111111111111",
			code_hash: `0x${"ab".repeat(32)}`,
			valid_from_block: "1",
			created_at: "2026-08-02T00:00:01Z",
		},
    language: "solidity",
    compiler_version: "0.8.30+commit.73712a01",
    file_name: "src/Example.sol",
    contract_name: "Example",
    is_blueprint: false,
    sources: {
      "src/Example.sol": { content: "contract Example { function value() external pure returns (uint256) { return Library.value(); } }" },
      "src/Library.sol": { content: "library Library { function value() internal pure returns (uint256) { return 1; } }" },
    },
    settings: {
      optimizer: { enabled: true, runs: 500, details: { yul: true } },
      evmVersion: "cancun",
      viaIR: true,
      metadata: { bytecodeHash: "ipfs" },
      remappings: ["@openzeppelin/=lib/openzeppelin/"],
      outputSelection: { "*": { "*": ["abi"] } },
      modelChecker: { engine: "chc" },
    },
    abi: [
      { type: "function", name: "value", inputs: [], outputs: [] },
      { type: "function", name: "owner", inputs: [], outputs: [] },
      { type: "event", name: "Changed", inputs: [] },
      { type: "error", name: "Unauthorized", inputs: [] },
      { type: "constructor", inputs: [] },
    ],
    compilation_artifacts: { storageLayout: {} },
    creation_code_artifacts: { sourceMap: "1:2:3" },
    runtime_code_artifacts: { sourceMap: "4:5:6" },
    runtime_match: { match_type: "full", transformations: [], values: {} },
    libraries: { Library: "0x2222222222222222222222222222222222222222" },
		...overrides,
  };
}
