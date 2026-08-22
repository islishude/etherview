import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
	createMemoryHistory,
	createRootRoute,
	createRoute,
	createRouter,
	RouterContextProvider,
} from "@tanstack/react-router";
import { encodeAbiParameters } from "viem";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import i18n from "@/i18n";

import {
  buildSourceTree,
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
  document.querySelector('meta[name="etherview-csp-nonce"]')?.remove();
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

  it("builds nested directories before files while preserving source paths", () => {
    expect(buildSourceTree([
      { name: "src/z/Last.sol", content: "" },
      { name: "README.md", content: "" },
      { name: "src/Main.sol", content: "" },
      { name: "src/a/First.sol", content: "" },
    ])).toEqual([
      {
        id: "directory:src",
        kind: "directory",
        name: "src",
        path: "src",
        children: [
          {
            id: "directory:src/a",
            kind: "directory",
            name: "a",
            path: "src/a",
            children: [
              {
                id: "file:src/a/First.sol",
                kind: "file",
                name: "First.sol",
                file: { name: "src/a/First.sol", content: "" },
              },
            ],
          },
          {
            id: "directory:src/z",
            kind: "directory",
            name: "z",
            path: "src/z",
            children: [
              {
                id: "file:src/z/Last.sol",
                kind: "file",
                name: "Last.sol",
                file: { name: "src/z/Last.sol", content: "" },
              },
            ],
          },
          {
            id: "file:src/Main.sol",
            kind: "file",
            name: "Main.sol",
            file: { name: "src/Main.sol", content: "" },
          },
        ],
      },
      {
        id: "file:README.md",
        kind: "file",
        name: "README.md",
        file: { name: "README.md", content: "" },
      },
    ]);
  });

  it("compacts consecutive directories with one directory child", () => {
    expect(buildSourceTree([
      { name: "src/contracts/interfaces/IFoo.sol", content: "interface IFoo {}" },
      { name: "src/contracts/interfaces/IBar.sol", content: "interface IBar {}" },
    ])).toEqual([
      {
        id: "directory:src/contracts/interfaces",
        kind: "directory",
        name: "src/contracts/interfaces",
        path: "src/contracts/interfaces",
        children: [
          {
            id: "file:src/contracts/interfaces/IBar.sol",
            kind: "file",
            name: "IBar.sol",
            file: { name: "src/contracts/interfaces/IBar.sol", content: "interface IBar {}" },
          },
          {
            id: "file:src/contracts/interfaces/IFoo.sol",
            kind: "file",
            name: "IFoo.sol",
            file: { name: "src/contracts/interfaces/IFoo.sol", content: "interface IFoo {}" },
          },
        ],
      },
    ]);
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
	it("explains factory-derived verification and lists bounded created contracts", () => {
		const creator = "0x2222222222222222222222222222222222222222";
		const child = "0x3333333333333333333333333333333333333333";
		const transaction = `0x${"44".repeat(32)}`;
		const rootRoute = createRootRoute();
		const addressRoute = createRoute({
			getParentRoute: () => rootRoute,
			path: "/address/$address",
			component: () => null,
		});
		const router = createRouter({
			history: createMemoryHistory({ initialEntries: ["/"] }),
			routeTree: rootRoute.addChildren([addressRoute]),
		});
		render(<RouterContextProvider router={router}><ContractArtifactPanel artifact={fixtureArtifact({
			verification_origin: "factory_derived",
			derived_from: {
				creator_address: creator,
				created_address: "0x1111111111111111111111111111111111111111",
				transaction_hash: transaction,
				trace_path: "0.1",
				call_type: "CREATE2",
				block_number: "2",
				block_hash: `0x${"55".repeat(32)}`,
				parent_file_name: "Factory.sol",
				parent_contract_name: "Factory",
			},
			derived_children: [{
				address: child,
				transaction_hash: transaction,
				trace_path: "0.2",
				call_type: "CREATE",
				block_number: "3",
				block_hash: `0x${"66".repeat(32)}`,
				status: "matched",
				auto_verified: true,
				contract_name: "Child",
				file_name: "Child.sol",
			}],
		})} /></RouterContextProvider>);

		expect(screen.getByRole("status")).toHaveTextContent("Auto-verified from verified factory:");
		expect(screen.getByText("Factory-derived")).toBeVisible();
		expect(screen.getByRole("heading", { name: "Created contracts" })).toBeVisible();
		expect(screen.getByRole("link", { name: child })).toBeVisible();
		expect(screen.getByText("Auto-verified")).toBeVisible();
	});

	it("presents a code-hash artifact as verified source without claiming address verification", () => {
		const sourceAddress = "0x2222222222222222222222222222222222222222";
		const rootRoute = createRootRoute();
		const addressRoute = createRoute({
			getParentRoute: () => rootRoute,
			path: "/address/$address",
			component: () => null,
		});
		const router = createRouter({
			history: createMemoryHistory({ initialEntries: ["/"] }),
			routeTree: rootRoute.addChildren([addressRoute]),
		});
		render(<RouterContextProvider router={router}><ContractArtifactPanel artifact={fixtureArtifact({
			resolution: "code_hash",
			source: {
				address: sourceAddress,
				code_hash: `0x${"ab".repeat(32)}`,
				valid_from_block: "1",
				created_at: "2026-08-02T00:00:01Z",
			},
		})} /></RouterContextProvider>);

		expect(screen.getAllByText("Source verified by code hash")).toHaveLength(2);
		expect(screen.queryByText("Source code verified", { exact: true })).toBeNull();
		expect(screen.getByRole("status")).toHaveTextContent(
			"Source verified by identical runtime code hash:",
		);
		expect(screen.getAllByRole("link", { name: sourceAddress })).not.toHaveLength(0);
	});

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
    expect(within(fileNavigation).getByRole("tree")).toBeVisible();
    const sourceFolder = within(fileNavigation).getByRole("treeitem", { name: "Collapse folder: src" });
    expect(sourceFolder).toHaveAttribute("aria-expanded", "true");
    await user.click(within(fileNavigation).getByRole("treeitem", { name: "Library.sol" }));
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

  it("applies the current document CSP nonce to CodeMirror runtime styles", () => {
    const nonce = "A".repeat(43);
    const meta = document.createElement("meta");
    meta.name = "etherview-csp-nonce";
    meta.content = nonce;
    document.head.append(meta);

    render(<ContractArtifactPanel artifact={fixtureArtifact()} />);

    expect(document.head.querySelector(`style[nonce="${nonce}"]`)).not.toBeNull();
    expect(screen.getByRole("textbox", {
      name: "Read-only source editor for src/Example.sol",
    })).toHaveAttribute("contenteditable", "false");
  });

  it("supports directory toggling and keyboard tree navigation", async () => {
    const user = userEvent.setup();
    render(<ContractArtifactPanel artifact={fixtureArtifact({
      sources: {
        "src/Example.sol": { content: "contract Example {}" },
        "src/lib/Library.sol": { content: "library Library {}" },
      },
    })} />);

    const fileNavigation = screen.getByRole("complementary", { name: "Source files" });
    const sourceFolder = within(fileNavigation).getByRole("treeitem", { name: "Collapse folder: src" });
    await user.click(sourceFolder);
    expect(sourceFolder).toHaveAttribute("aria-expanded", "false");
    expect(within(fileNavigation).queryByRole("button", { name: "Example.sol" })).toBeNull();

    await user.keyboard(" ");
    expect(sourceFolder).toHaveAttribute("aria-expanded", "true");
    await user.keyboard("{ArrowDown}");
    await user.keyboard("{ArrowDown}");
    await user.keyboard("{ArrowDown}");
    await user.keyboard("{Enter}");
    expect(screen.getByRole("textbox", { name: "Read-only source editor for src/Example.sol" })).toHaveTextContent("contract Example");
    expect(within(fileNavigation).getByRole("treeitem", { name: "Example.sol" })).toHaveAttribute("aria-selected", "true");
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

  it("renders Geas as read-only plain text with its pinned entrypoint settings", async () => {
    const user = userEvent.setup();
    render(<ContractArtifactPanel artifact={fixtureArtifact({
      language: "geas",
      compiler_version: "0.3.3",
      file_name: "withdrawals/main.eas",
      contract_name: "Withdrawals",
      sources: {
        "withdrawals/main.eas": { content: "#include \"../common/fake_expo.eas\"\npush 1\n" },
        "common/fake_expo.eas": { content: "#define %fake_expo { add }\n" },
      },
      settings: {
        runtime_entrypoint: "withdrawals/main.eas",
        creation_entrypoint: "withdrawals/ctor.eas",
        stack_check: true,
      },
      abi: [],
      compilation_artifacts: {},
      creation_code_artifacts: {},
      runtime_code_artifacts: {},
      libraries: {},
    })} />);

    expect(screen.getByRole("heading", { name: "Withdrawals" })).toBeVisible();
    expect(screen.getAllByText("0 functions · 0 events · 0 errors · 0 constructors").length).toBeGreaterThan(0);
    const editor = screen.getByRole("textbox", {
      name: "Read-only source editor for withdrawals/main.eas",
    });
    expect(editor).toHaveAttribute("contenteditable", "false");
    expect(editor).toHaveTextContent("#include");
    expect(editor.querySelector(".tok-keyword")).toBeNull();
    expect(screen.getByText("GEAS")).toBeVisible();
    const settingsDisclosure = screen.getByText("Complete compiler settings").closest("details");
    expect(settingsDisclosure).not.toBeNull();
    await user.click(screen.getByText("Complete compiler settings"));
    expect(within(settingsDisclosure as HTMLElement).getByText(/runtime_entrypoint/u)).toBeVisible();
    expect(within(settingsDisclosure as HTMLElement).getByText(/stack_check/u)).toBeVisible();
  });
});

function fixtureArtifact(
  overrides: Partial<VerifiedContractArtifact> = {},
): VerifiedContractArtifact {
  return {
    kind: "verification_success",
		verification_origin: "submitted",
		derived_children: [],
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
