import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  encodeErrorResult,
  encodeFunctionData,
  encodeFunctionResult,
  getAddress,
  toHex,
  type Abi,
  type Hex,
} from "viem";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { usePublicConfig } from "@/api/hooks";
import i18n from "@/i18n";
import { getContractProxyResponse } from "@/contracts/proxy";
import {
  buildContractInteractionTargets,
  type ContractInteractionTarget,
  type ProxyDetails,
  type ProxyDetailsResponse,
} from "@/contracts/targets";
import { WalletBoundaryError } from "@/wallet/eip6963";
import { useWallet } from "@/wallet/WalletProvider";

import { AbiFunctionExplorer } from "./AbiFunctionForm";

vi.mock("@/api/hooks", () => ({
  usePublicConfig: vi.fn(),
}));

vi.mock("@/contracts/proxy", () => ({
  getContractProxyResponse: vi.fn(),
}));

vi.mock("@/wallet/WalletProvider", () => ({
  useWallet: vi.fn(),
}));

const PROXY = getAddress("0xdc64a140aa3e981100a9beca4e685f962f0cf6c9");
const IMPLEMENTATION = getAddress("0x5fbdb2315678afecb367f032d93f642f64180aa3");
const ADMIN = getAddress("0xe7f1725e7734ce288f8367e1bb143e90bb3f0512");
const ACCOUNT = getAddress("0x70997970c51812dc3a010c7d01b50e0d17dc79c8");
const OTHER = getAddress("0x3c44cdddb6a900fa2b585dd299e03d12fa4293bc");
const PROXY_HASH = `0x${"11".repeat(32)}`;
const IMPLEMENTATION_HASH = `0x${"22".repeat(32)}`;
const ADMIN_HASH = `0x${"66".repeat(32)}`;
const BLOCK_HASH = `0x${"33".repeat(32)}`;
const TRANSACTION_HASH = `0x${"44".repeat(32)}` as Hex;
const NEXT_TRANSACTION_HASH = `0x${"55".repeat(32)}` as Hex;
const BINDING_ID = "11111111-1111-4111-8111-111111111111";
const NEXT_BINDING_ID = "22222222-2222-4222-8222-222222222222";

const activeWallet = Object.freeze({
  uuid: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
  name: "Test Wallet",
  account: ACCOUNT,
  chainID: "31337",
  revision: 7,
});

const overloadABI = [
  {
    type: "function",
    name: "lookup",
    stateMutability: "view",
    inputs: [{ name: "id", type: "uint256" }],
    outputs: [{ name: "label", type: "string" }],
  },
  {
    type: "function",
    name: "lookup",
    stateMutability: "view",
    inputs: [{ name: "owner", type: "address" }],
    outputs: [{ name: "balance", type: "uint256" }],
  },
] as const satisfies Abi;

const configureABI = [
  {
    type: "function",
    name: "configure",
    stateMutability: "nonpayable",
    inputs: [
      {
        name: "config",
        type: "tuple",
        components: [
          { name: "owner", type: "address" },
          { name: "threshold", type: "uint8" },
        ],
      },
      {
        name: "batches",
        type: "tuple[][]",
        components: [
          { name: "recipient", type: "address" },
          { name: "amount", type: "uint256" },
        ],
      },
    ],
    outputs: [],
  },
] as const satisfies Abi;

const multipleOutputABI = [
  {
    type: "function",
    name: "summary",
    stateMutability: "view",
    inputs: [],
    outputs: [
      { name: "total", type: "uint256" },
      { name: "owner", type: "address" },
      { name: "enabled", type: "bool" },
    ],
  },
] as const satisfies Abi;

const erc20DecimalsABI = [
  {
    type: "function",
    name: "decimals",
    stateMutability: "view",
    inputs: [],
    outputs: [{ name: "", type: "uint8" }],
  },
] as const satisfies Abi;

const payableABI = [
  {
    type: "function",
    name: "deposit",
    stateMutability: "payable",
    inputs: [],
    outputs: [],
  },
  {
    type: "function",
    name: "setLimit",
    stateMutability: "nonpayable",
    inputs: [{ name: "limit", type: "uint256" }],
    outputs: [],
  },
] as const satisfies Abi;

const oversizedFixedInputABI = [
  {
    type: "function",
    name: "configureHugeFixed",
    stateMutability: "nonpayable",
    inputs: [{ name: "values", type: "uint256[65][65]" }],
    outputs: [],
  },
] as const satisfies Abi;

const oversizedDynamicItemABI = [
  {
    type: "function",
    name: "configureHugeDynamic",
    stateMutability: "nonpayable",
    inputs: [{ name: "values", type: "uint256[65][65][]" }],
    outputs: [],
  },
] as const satisfies Abi;

const uupsABI = [
  {
    type: "function",
    name: "proxiableUUID",
    stateMutability: "view",
    inputs: [],
    outputs: [{ name: "slot", type: "bytes32" }],
  },
  {
    type: "function",
    name: "value",
    stateMutability: "view",
    inputs: [],
    outputs: [{ name: "current", type: "uint256" }],
  },
  {
    type: "function",
    name: "upgradeToAndCall",
    stateMutability: "payable",
    inputs: [
      { name: "newImplementation", type: "address" },
      { name: "data", type: "bytes" },
    ],
    outputs: [],
  },
] as const satisfies Abi;

const uupsABIWithCustomError = [
  {
    type: "error",
    name: "Unauthorized",
    inputs: [{ name: "account", type: "address" }],
  },
  ...uupsABI,
] as const satisfies Abi;

const malformedWriteUUIDABI = [
  {
    type: "function",
    name: "proxiableUUID",
    stateMutability: "nonpayable",
    inputs: [],
    outputs: [{ name: "slot", type: "bytes32" }],
  },
] as const satisfies Abi;

const proxyAdminABI = [
  {
    type: "function",
    name: "transferOwnership",
    stateMutability: "nonpayable",
    inputs: [{ name: "newOwner", type: "address" }],
    outputs: [],
  },
  {
    type: "function",
    name: "renounceOwnership",
    stateMutability: "nonpayable",
    inputs: [],
    outputs: [],
  },
  {
    type: "function",
    name: "upgradeAndCall",
    stateMutability: "payable",
    inputs: [
      { name: "proxy", type: "address" },
      { name: "implementation", type: "address" },
      { name: "data", type: "bytes" },
    ],
    outputs: [],
  },
] as const satisfies Abi;

const beaconManagementABI = [
  {
    type: "function",
    name: "upgradeTo",
    stateMutability: "nonpayable",
    inputs: [{ name: "newImplementation", type: "address" }],
    outputs: [],
  },
] as const satisfies Abi;

describe("AbiFunctionExplorer", () => {
  beforeEach(async () => {
    vi.clearAllMocks();
    await i18n.changeLanguage("en");
    vi.mocked(usePublicConfig).mockReturnValue({
      data: { chain_id: "31337" },
    } as ReturnType<typeof usePublicConfig>);
    vi.mocked(getContractProxyResponse).mockResolvedValue(proxyResponse());
  });

  it("keeps overload signatures separate and submits viem-encoded calldata for each", async () => {
    const uintCall = encodeFunctionData({
      abi: [overloadABI[0]],
      functionName: "lookup",
      args: [17n],
    });
    const addressCall = encodeFunctionData({
      abi: [overloadABI[1]],
      functionName: "lookup",
      args: [OTHER],
    });
    const readContract = vi.fn(async ({ data }: { data: Hex }) => {
      if (data === uintCall) {
        return encodeFunctionResult({
          abi: [overloadABI[0]],
          functionName: "lookup",
          result: "seventeen",
        });
      }
      if (data === addressCall) {
        return encodeFunctionResult({
          abi: [overloadABI[1]],
          functionName: "lookup",
          result: 99n,
        });
      }
      throw new Error(`unexpected calldata ${data}`);
    });
    mockWallet({ readContract });
    renderExplorer(overloadABI, "read", directTargets());
    const user = userEvent.setup();

    const uintCard = await openFunctionCard(user, "lookup(uint256)");
    await user.type(uintCard.getByLabelText(/^id/u), "17");
    await user.click(uintCard.getByRole("button", { name: "Read contract" }));
    expect(await uintCard.findByText("seventeen")).toBeVisible();

    const addressCard = await openFunctionCard(user, "lookup(address)");
    await user.type(addressCard.getByLabelText(/^owner/u), OTHER);
    await user.click(addressCard.getByRole("button", { name: "Read contract" }));
    expect(await addressCard.findByText("99")).toBeVisible();

    expect(readContract).toHaveBeenCalledTimes(2);
    expect(readContract.mock.calls.map(([call]) => call.data)).toEqual([
      uintCall,
      addressCall,
    ]);
  });

  it("edits tuple and nested dynamic-array values into real viem calldata", async () => {
    const sendTransaction = vi.fn(async () => TRANSACTION_HASH);
    mockWallet({ sendTransaction });
    renderExplorer(configureABI, "write", directTargets());
    const user = userEvent.setup();
    const card = await openFunctionCard(
      user,
      "configure((address,uint8),(address,uint256)[][])",
    );

    await user.type(card.getByLabelText(/^owner/u), ACCOUNT);
    await user.type(card.getByLabelText(/^threshold/u), "7");
    await user.click(card.getByRole("button", { name: "Add array item" }));
    await user.click(card.getAllByRole("button", { name: "Add array item" })[0]!);
    await user.type(card.getByLabelText(/^recipient/u), OTHER);
    await user.type(card.getByLabelText(/^amount/u), "9");
    await user.click(card.getByRole("button", { name: "Send transaction" }));

    await waitFor(() => expect(sendTransaction).toHaveBeenCalledOnce());
    expect(sendTransaction).toHaveBeenCalledWith(
      {
        to: PROXY,
        data: encodeFunctionData({
          abi: configureABI,
          functionName: "configure",
          args: [
            { owner: ACCOUNT, threshold: 7 },
            [[{ recipient: OTHER, amount: 9n }]],
          ],
        }),
      },
      "31337",
    );
  });

  it("decodes and renders multiple viem return values without numeric loss", async () => {
    const total = 11_579_208_923_731_619_542_357_098_500n;
    const readContract = vi.fn(async () => encodeFunctionResult({
      abi: multipleOutputABI,
      functionName: "summary",
      result: [total, OTHER, true],
    }));
    mockWallet({ readContract });
    renderExplorer(multipleOutputABI, "read", directTargets());
    const user = userEvent.setup();
    const card = await openFunctionCard(user, "summary()");

    await user.click(card.getByRole("button", { name: "Read contract" }));

    const output = await card.findByRole("status");
    expect(within(output).getByText(total.toString())).toBeVisible();
    expect(within(output).getByText(OTHER)).toBeVisible();
    expect(within(output).getByText("true")).toBeVisible();
  });

  it("decodes and renders an ERC-20 decimals() uint8 result", async () => {
    const readContract = vi.fn(async () => encodeFunctionResult({
      abi: erc20DecimalsABI,
      functionName: "decimals",
      result: 18,
    }));
    mockWallet({ readContract });
    renderExplorer(erc20DecimalsABI, "read", directTargets(IMPLEMENTATION));
    const user = userEvent.setup();
    const card = await openFunctionCard(user, "decimals()");

    await user.click(card.getByRole("button", { name: "Read contract" }));

    const output = await card.findByRole("status");
    expect(within(output).getByText("18")).toBeVisible();
    expect(readContract).toHaveBeenCalledWith(
      expect.objectContaining({ to: IMPLEMENTATION }),
      "31337",
    );
  });

  it("shows and forwards native value only for payable functions", async () => {
    const sendTransaction = vi.fn(async () => TRANSACTION_HASH);
    mockWallet({ sendTransaction });
    renderExplorer(payableABI, "write", directTargets());
    const user = userEvent.setup();
    const payable = await openFunctionCard(user, "deposit()");
    const nonpayable = await openFunctionCard(user, "setLimit(uint256)");

    expect(payable.getByLabelText(/^Native value \(wei\)/u)).toBeVisible();
    expect(nonpayable.queryByLabelText(/^Native value \(wei\)/u)).not.toBeInTheDocument();
    await user.type(payable.getByLabelText(/^Native value \(wei\)/u), "15");
    await user.click(payable.getByRole("button", { name: "Send transaction" }));

    await waitFor(() => expect(sendTransaction).toHaveBeenCalledOnce());
    expect(sendTransaction).toHaveBeenCalledWith(
      {
        to: PROXY,
        data: encodeFunctionData({
          abi: [payableABI[0]],
          functionName: "deposit",
        }),
        value: toHex(15n),
      },
      "31337",
    );
  });

  it("contains an oversized fixed input shape inside its function card", () => {
    mockWallet();
    renderExplorer(oversizedFixedInputABI, "write", directTargets());

    const signature = screen.getByText(
      "configureHugeFixed(uint256[65][65])",
      { selector: "code" },
    );
    const card = signature.closest("details");
    expect(card).toBeInstanceOf(HTMLDetailsElement);
    expect(within(card as HTMLDetailsElement).getByRole("alert")).toHaveTextContent(
      "expanded array or tuple inputs exceed the browser work limit",
    );
    expect(screen.queryByRole("button", { name: "Send transaction" })).toBeNull();
  });

  it("rejects an oversized dynamic item without crashing or mutating the form", async () => {
    mockWallet();
    renderExplorer(oversizedDynamicItemABI, "write", directTargets());
    const user = userEvent.setup();
    const card = await openFunctionCard(
      user,
      "configureHugeDynamic(uint256[65][65][])",
    );

    await user.click(card.getByRole("button", { name: "Add array item" }));

    expect(card.getByRole("alert")).toHaveTextContent(
      "expanded array or tuple inputs exceed the browser work limit",
    );
    expect(card.queryByLabelText(/^#0/u)).toBeNull();
    expect(card.getByRole("button", { name: "Send transaction" })).toBeEnabled();
  });

  it("calls proxiableUUID directly on the implementation and other UUPS reads through the proxy", async () => {
    const uuidResult = `0x${"aa".repeat(32)}` as Hex;
    const readContract = vi.fn(async ({ data }: { data: Hex }) => {
      if (data === encodeFunctionData({
        abi: [uupsABI[0]],
        functionName: "proxiableUUID",
      })) {
        return encodeFunctionResult({
          abi: [uupsABI[0]],
          functionName: "proxiableUUID",
          result: uuidResult,
        });
      }
      return encodeFunctionResult({
        abi: [uupsABI[1]],
        functionName: "value",
        result: 42n,
      });
    });
    mockWallet({ readContract });
    renderExplorer(uupsABI, "read", implementationTargets());
    const user = userEvent.setup();

    const uuidCard = await openFunctionCard(user, "proxiableUUID()");
    expect(uuidCard.getByText(/called directly on the implementation/u)).toBeVisible();
    await user.click(uuidCard.getByRole("button", { name: "Read contract" }));
    await waitFor(() => expect(readContract).toHaveBeenCalledTimes(1));
    expect(readContract.mock.calls[0]?.[0]).toMatchObject({ to: IMPLEMENTATION });

    const valueCard = await openFunctionCard(user, "value()");
    await user.click(valueCard.getByRole("button", { name: "Read contract" }));
    await waitFor(() => expect(readContract).toHaveBeenCalledTimes(2));
    expect(readContract.mock.calls[1]?.[0]).toMatchObject({ to: PROXY });
    expect(getContractProxyResponse).toHaveBeenCalledTimes(2);
    expect(getContractProxyResponse).toHaveBeenNthCalledWith(1, PROXY);
    expect(getContractProxyResponse).toHaveBeenNthCalledWith(2, PROXY);
  });

  it("never exposes a malformed state-changing proxiableUUID through the direct UUPS target", () => {
    const sendTransaction = vi.fn(async () => TRANSACTION_HASH);
    mockWallet({ sendTransaction });

    renderExplorer(malformedWriteUUIDABI, "write", implementationTargets());

    expect(screen.getByText(
      "This ABI has no callable state-changing functions for this target.",
    )).toBeVisible();
    expect(screen.queryByText("proxiableUUID()", { selector: "code" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Send transaction" })).toBeNull();
    expect(sendTransaction).not.toHaveBeenCalled();
  });

  it("marks ownership management calls as high risk without overstating Transparent scope", async () => {
    mockWallet();
    renderExplorer(proxyAdminABI, "write", managementTargets("transparent"));
    const user = userEvent.setup();

    const transfer = await openFunctionCard(user, "transferOwnership(address)");
    expect(transfer.getByText(/high-risk ownership operation/iu)).toBeVisible();
    expect(transfer.getByText(/management target linked to 3 proxies/iu)).toBeVisible();
    expect(transfer.getByText(ADMIN)).toBeVisible();

    const renounce = await openFunctionCard(user, "renounceOwnership()");
    expect(renounce.getByText(/high-risk ownership operation/iu)).toBeVisible();
    expect(renounce.getByText(/management target linked to 3 proxies/iu)).toBeVisible();

    const upgrade = await openFunctionCard(
      user,
      "upgradeAndCall(address,address,bytes)",
    );
    expect(upgrade.getByText(/high-risk upgrade operation/iu)).toBeVisible();
    expect(upgrade.getByText(/management target linked to 3 proxies/iu)).toBeVisible();
    expect(upgrade.queryByText(/affects 3 linked proxies/iu)).toBeNull();
  });

  it("shows the shared Beacon impact only for an actual Beacon upgrade", async () => {
    mockWallet();
    renderExplorer(beaconManagementABI, "write", managementTargets("beacon"));
    const user = userEvent.setup();
    const upgrade = await openFunctionCard(user, "upgradeTo(address)");

    expect(upgrade.getByText(/high-risk upgrade operation/iu)).toBeVisible();
    expect(upgrade.getByText(/affects 4 linked proxies/iu)).toBeVisible();
    expect(upgrade.getByText(ADMIN)).toBeVisible();
  });

  it("performs a fresh binding GET before every bound write", async () => {
    const sendTransaction = vi
      .fn()
      .mockResolvedValueOnce(TRANSACTION_HASH)
      .mockResolvedValueOnce(NEXT_TRANSACTION_HASH);
    mockWallet({ sendTransaction });
    renderExplorer(uupsABI, "write", implementationTargets());
    const user = userEvent.setup();
    const card = await openFunctionCard(
      user,
      "upgradeToAndCall(address,bytes)",
    );
    await user.type(card.getByLabelText(/^newImplementation/u), OTHER);
    await user.type(card.getByLabelText(/^data/u), "0x");

    await user.click(card.getByRole("button", { name: "Send transaction" }));
    await waitFor(() => expect(sendTransaction).toHaveBeenCalledTimes(1));
    await user.click(card.getByRole("button", { name: "Send transaction" }));
    await waitFor(() => expect(sendTransaction).toHaveBeenCalledTimes(2));

    expect(getContractProxyResponse).toHaveBeenCalledTimes(2);
    expect(vi.mocked(getContractProxyResponse).mock.invocationCallOrder[0])
      .toBeLessThan(sendTransaction.mock.invocationCallOrder[0]!);
    expect(vi.mocked(getContractProxyResponse).mock.invocationCallOrder[1])
      .toBeLessThan(sendTransaction.mock.invocationCallOrder[1]!);
    expect(sendTransaction.mock.calls.map(([transaction]) => transaction.to))
      .toEqual([PROXY, PROXY]);
  });

  it("blocks a changed binding before send and requests a visible refresh", async () => {
    vi.mocked(getContractProxyResponse).mockResolvedValue(
      proxyResponse(uupsDetails({ binding_id: NEXT_BINDING_ID })),
    );
    const sendTransaction = vi.fn(async () => TRANSACTION_HASH);
    const onBindingChanged = vi.fn();
    mockWallet({ sendTransaction });
    renderExplorer(uupsABI, "write", implementationTargets(), onBindingChanged);
    const user = userEvent.setup();
    const card = await openFunctionCard(
      user,
      "upgradeToAndCall(address,bytes)",
    );
    await user.type(card.getByLabelText(/^newImplementation/u), OTHER);
    await user.type(card.getByLabelText(/^data/u), "0x");
    await user.click(card.getByRole("button", { name: "Send transaction" }));

    expect(await card.findByRole("alert")).toHaveTextContent(
      "The verified proxy binding or target changed",
    );
    expect(onBindingChanged).toHaveBeenCalledOnce();
    expect(getContractProxyResponse).toHaveBeenCalledOnce();
    expect(sendTransaction).not.toHaveBeenCalled();
  });

  it("decodes bounded revert data while keeping an unknown outcome sticky and single-shot", async () => {
    const revertData = encodeErrorResult({
      abi: uupsABIWithCustomError,
      errorName: "Unauthorized",
      args: [ACCOUNT],
    });
    const sendTransaction = vi.fn(async () => {
      throw new WalletBoundaryError("TRANSACTION_OUTCOME_UNKNOWN", revertData);
    });
    mockWallet({ sendTransaction });
    renderExplorer(uupsABIWithCustomError, "write", implementationTargets());
    const user = userEvent.setup();
    const card = await openFunctionCard(
      user,
      "upgradeToAndCall(address,bytes)",
    );
    await user.type(card.getByLabelText(/^newImplementation/u), OTHER);
    await user.type(card.getByLabelText(/^data/u), "0x");
    await user.click(card.getByRole("button", { name: "Send transaction" }));

    const alert = await card.findByRole("alert");
    expect(alert).toHaveTextContent(/outcome is unknown/u);
    expect(alert).toHaveTextContent(`Unauthorized(address): ${ACCOUNT}`);
    expect(getContractProxyResponse).toHaveBeenCalledOnce();
    expect(sendTransaction).toHaveBeenCalledOnce();
  });

  it("decodes a bounded revert reported before submission without changing its outcome class", async () => {
    const revertData = encodeErrorResult({
      abi: uupsABIWithCustomError,
      errorName: "Unauthorized",
      args: [ACCOUNT],
    });
    const sendTransaction = vi.fn(async () => {
      throw new WalletBoundaryError("USER_REJECTED", revertData);
    });
    mockWallet({ sendTransaction });
    renderExplorer(uupsABIWithCustomError, "write", implementationTargets());
    const user = userEvent.setup();
    const card = await openFunctionCard(
      user,
      "upgradeToAndCall(address,bytes)",
    );
    await user.type(card.getByLabelText(/^newImplementation/u), OTHER);
    await user.type(card.getByLabelText(/^data/u), "0x");
    await user.click(card.getByRole("button", { name: "Send transaction" }));

    const alert = await card.findByRole("alert");
    expect(alert).toHaveTextContent(/wallet request was rejected/iu);
    expect(alert).toHaveTextContent(/wallet also reported revert data/iu);
    expect(alert).toHaveTextContent(`Unauthorized(address): ${ACCOUNT}`);
    expect(alert).not.toHaveTextContent(/outcome is unknown/iu);
    expect(sendTransaction).toHaveBeenCalledOnce();
  });
});

function renderExplorer(
  abi: Abi,
  mode: "read" | "write" | "all",
  targets: readonly ContractInteractionTarget[],
  onBindingChanged?: () => void,
): void {
  render(
    <AbiFunctionExplorer
      abi={abi}
      mode={mode}
      onBindingChanged={onBindingChanged}
      targets={targets}
    />,
  );
}

async function openFunctionCard(
  user: ReturnType<typeof userEvent.setup>,
  signature: string,
) {
  const signatureElement = screen.getByText(signature, { selector: "code" });
  const details = signatureElement.closest("details");
  if (!(details instanceof HTMLDetailsElement)) {
    throw new Error(`missing ${signature} details`);
  }
  if (!details.open) {
    const summary = signatureElement.closest("summary");
    if (!summary) throw new Error(`missing ${signature} summary`);
    await user.click(summary);
  }
  return within(details);
}

function directTargets(address = PROXY): readonly ContractInteractionTarget[] {
  return buildContractInteractionTargets(address).filter(
    (target) => target.kind === "contract",
  );
}

function implementationTargets(): readonly ContractInteractionTarget[] {
  return buildContractInteractionTargets(PROXY, uupsDetails()).filter(
    (target) =>
      target.kind === "implementation_as_proxy" ||
      target.kind === "uups_implementation_direct",
  );
}

function managementTargets(
  pattern: "transparent" | "beacon",
): readonly ContractInteractionTarget[] {
  return buildContractInteractionTargets(PROXY, managementDetails(pattern)).filter(
    (target) => target.kind === "transparent_proxy_admin" || target.kind === "beacon_management",
  );
}

function mockWallet(
  overrides: Partial<ReturnType<typeof useWallet>> = {},
): ReturnType<typeof useWallet> {
  const state = {
    providers: [],
    active: activeWallet,
    connecting: false,
    addingChain: false,
    discover: vi.fn(),
    connect: vi.fn(async () => activeWallet),
    addChain: vi.fn(async () => {}),
    disconnect: vi.fn(),
    getActiveWallet: vi.fn(() => activeWallet),
    isActiveWallet: vi.fn(() => true),
    readContract: vi.fn(async () => "0x" as Hex),
    sendTransaction: vi.fn(async () => TRANSACTION_HASH),
    signSIWEChallenge: vi.fn(async () => `0x${"11".repeat(65)}` as Hex),
    ...overrides,
  } as ReturnType<typeof useWallet>;
  vi.mocked(useWallet).mockReturnValue(state);
  return state;
}

function uupsDetails(overrides: Partial<ProxyDetails> = {}): ProxyDetails {
  return {
    address: PROXY,
    status: "verified",
    snapshot: {
      chain_id: "31337",
      block_number: "20",
      block_hash: BLOCK_HASH,
    },
    mechanism: "eip1967",
    pattern: "uups",
    standard_version: "5.6.1",
    evidence_state: "exact",
    confidence: "verified",
    proxy: {
      address: PROXY,
      code_hash: PROXY_HASH,
      verification_state: "verified",
      artifact_kind: "erc1967_proxy",
      standard_version: "5.6.1",
    },
    implementation: {
      address: IMPLEMENTATION,
      code_hash: IMPLEMENTATION_HASH,
      verification_state: "verified",
      artifact_kind: "uups_implementation",
      standard_version: "5.6.1",
    },
    binding_id: BINDING_ID,
    evidence: [],
    ...overrides,
  };
}

function managementDetails(pattern: "transparent" | "beacon"): ProxyDetails {
  const managementIdentity = {
    address: ADMIN,
    code_hash: ADMIN_HASH,
    verification_state: "verified" as const,
    artifact_kind: pattern === "transparent"
      ? "proxy_admin" as const
      : "upgradeable_beacon" as const,
    standard_version: "5.6.1" as const,
  };
  return uupsDetails({
    pattern,
    implementation: {
      address: IMPLEMENTATION,
      code_hash: IMPLEMENTATION_HASH,
      verification_state: "verified",
    },
    ...(pattern === "transparent"
      ? { admin: managementIdentity }
      : { beacon: managementIdentity }),
    management: {
      kind: pattern === "transparent" ? "proxy_admin" : "upgradeable_beacon",
      target: managementIdentity,
      affected_proxy_count: pattern === "transparent" ? "3" : "4",
    },
  });
}

function proxyResponse(
  data: ProxyDetails = uupsDetails(),
): ProxyDetailsResponse {
  return {
    data,
    meta: {
      chain_id: "31337",
      request_id: "request-1",
    },
  };
}
