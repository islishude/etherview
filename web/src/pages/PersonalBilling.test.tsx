import type { ReactNode } from "react";
import { shorten } from "@/components/format";
import { testBillingConfig, testTopupIntent } from "@/billing/testFixtures";
import { encodeAbiParameters } from "viem";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import * as billing from "@/api/billing";
import { payBillingTopup, TopupPendingError } from "@/billing/topup";
import i18n from "@/i18n";
import { BillingChainError } from "@/wallet/eip6963";
import { PersonalBilling } from "./PersonalBilling";

const state = vi.hoisted(() => ({
  userID: "user-a",
  connected: true,
  connect: vi.fn(),
  chainID: "1",
  readContract: vi.fn(),
  crossChain: false,
  withBillingChain: vi.fn(),
  closeBillingChain: vi.fn(),
}));
vi.mock("@/api/publicConfig", () => ({
  usePublicConfig: () => ({ data: { chain_id: "1" } }),
}));
vi.mock("@tanstack/react-router", () => ({
  Link: ({ params, children }: { params: { hash: string }; children: ReactNode }) => (
    <a href={`/tx/${params.hash}?tab=overview`}>{children}</a>
  ),
}));
vi.mock("@/auth/AuthProvider", () => ({
  useAuth: () => ({
    session: {
      authenticated: true,
      csrf_token: "test-csrf",
      expires_at: "2099-01-01",
      user: { id: state.userID, address: "0x1111111111111111111111111111111111111111" },
    },
  }),
}));
vi.mock("@/wallet/WalletProvider", () => ({
  useWallet: () => ({
    active: state.connected
      ? {
          account: "0x1111111111111111111111111111111111111111",
          chainID: state.chainID,
          authChainID: state.crossChain ? "31337" : state.chainID,
        }
      : undefined,
    providers: [{ uuid: "test-wallet", name: "Test wallet" }],
    connect: state.connect,
    discover: vi.fn(),
    connecting: false,
    readContract: state.readContract,
    withBillingChain: state.crossChain ? state.withBillingChain : undefined,
    closeBillingChain: state.closeBillingChain,
    sendTransaction: vi.fn(),
    signBillingTypedData: vi.fn(),
    waitForBillingTransaction: vi.fn(),
  }),
}));
vi.mock("@/api/billing", () => ({
  createBillingTopupIntent: vi.fn(),
  getBillingConfig: vi.fn(),
  getBillingTopupIntent: vi.fn(),
  getCurrentBillingAccount: vi.fn(),
  listCurrentBillingTopupIntents: vi.fn(),
  listCurrentUserBillingPayments: vi.fn(),
}));
vi.mock("@/billing/topup", () => ({
  payBillingTopup: vi.fn(),
  TopupPendingError: class extends Error {
    name = "TopupPendingError";
    constructor(public intentID: string) {
      super("pending");
    }
  },
}));
const address = "0x1111111111111111111111111111111111111111";
const amount = "340282366920938463463374607431768211455";
const transactionHash = `0x${"6".repeat(64)}` as const;
const config = testBillingConfig({
  api_billing_enabled: true,
  x402_topups_enabled: true,
  x402_version: 2,
  scheme: "exact",
  network: "eip155:1",
  asset: address,
  recipient: address,
  asset_transfer_methods: ["eip3009", "permit2"],
  minimum_topup_amount_atomic: "10",
  maximum_topup_amount_atomic: amount,
  asset_decimals: 6,
  operations: [
    { operation: "etherview.account.balance", amount_atomic: "2", complexity_weight: 2 },
  ],
});
const account: billing.BillingAccount = {
  user_id: "user-a",

  available_atomic: amount,
  reserved_atomic: "0",
  total_credit_atomic: amount,
  total_debit_atomic: "10",
  created_at: "2026-10-30T00:00:00Z",
  updated_at: "2026-10-30T00:00:00Z",
};
const intent: billing.BillingTopupIntent = testTopupIntent(
  {
    id: "topup-a",
    user_id: "user-a",
    amount_atomic: "123",
    network: "eip155:1",
    asset: address,
    recipient: address,
    payer: address,
    state: "open",
    created_at: "2026-10-30T00:00:00Z",
    updated_at: "2026-10-30T00:00:00Z",
    expires_at: "2099-01-01T00:00:00Z",
  },
  config,
);
const payment: billing.BillingPayment = {
  id: "payment-a",
  amount_atomic: "456",
  network: "eip155:1",
  asset: address,
  recipient: address,
  payer: address,
  state: "settled",
  purpose: "account_topup",
  asset_transfer_method: "eip3009",
  payment_flow: "authorization",
  operation: "account_topup",
  method: "POST",
  created_at: intent.created_at,
  updated_at: intent.updated_at,
  transaction_hash: transactionHash,
  user_id: "hidden-user",
  api_key_prefix: "hidden-key",
};
function list<T>(data: T[], next_cursor?: string) {
  return { data, meta: { request_id: "test", chain_id: "1", next_cursor } };
}
function setup() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const tree = () => (
    <QueryClientProvider client={client}>
      <PersonalBilling key={state.userID} />
    </QueryClientProvider>
  );
  const result = render(tree());
  return { client, user: userEvent.setup(), rerender: () => result.rerender(tree()) };
}
async function openHistory(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("tab", { name: "History" }));
}
async function ready() {
  await waitFor(() =>
    expect(screen.getByRole("textbox", { name: "Top-up amount (whole tokens)" })).toBeEnabled(),
  );
}

describe("personal billing workspace", () => {
  it.each([false, true])("locks network entry and fences departure: %s", async (depart) => {
    state.crossChain = true;
    const other = testBillingConfig({ id: "other", network: "eip155:2" });
    vi.mocked(billing.getBillingConfig).mockResolvedValue({
      ...config,
      payment_chains: [...config.payment_chains, ...other.payment_chains],
    });
    let release!: () => void;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const { user, rerender } = setup();
    state.withBillingChain.mockImplementation(async (_network, operation) => {
      await gate;
      state.chainID = "2";
      rerender();
      return operation({ account: address, chainID: "2", revision: 2 });
    });
    await ready();
    await waitFor(() => expect(state.readContract).toHaveBeenCalledTimes(1));
    await user.selectOptions(screen.getByLabelText("Network", { exact: true }), "eip155:2");
    expect(state.withBillingChain).toHaveBeenCalledWith("eip155:2", expect.any(Function), true);
    for (const select of screen.getAllByRole("combobox")) expect(select).toBeDisabled();
    expect(screen.getByRole("button", { name: "Refresh balance" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Top up & authorize" })).toBeDisabled();
    if (depart) await openHistory(user);
    await act(async () => {
      release();
    });
    if (depart) {
      expect(state.readContract).toHaveBeenCalledTimes(1);
      expect(state.closeBillingChain).toHaveBeenCalledTimes(1);
    } else {
      await waitFor(() => expect(state.readContract).toHaveBeenCalledTimes(2));
      expect(state.closeBillingChain).not.toHaveBeenCalled();
      await openHistory(user);
      expect(state.closeBillingChain).toHaveBeenCalledTimes(1);
    }
    expect(billing.createBillingTopupIntent).not.toHaveBeenCalled();
  });

  it("retains a failed selection, hides old funds and retries the selected network", async () => {
    state.crossChain = true;
    state.readContract.mockResolvedValue(encodeAbiParameters([{ type: "uint256" }], [123456789n]));
    const other = testBillingConfig({ id: "other", network: "eip155:2", name: "Other USD" });
    vi.mocked(billing.getBillingConfig).mockResolvedValue({
      ...config,
      payment_chains: [...config.payment_chains, ...other.payment_chains],
    });
    state.withBillingChain.mockRejectedValueOnce(
      new BillingChainError("entry", "2", "USER_REJECTED"),
    );
    const { user } = setup();
    await ready();
    await waitFor(() =>
      expect(document.querySelector(".billing-wallet-balance")).toHaveTextContent("123.456789"),
    );
    await user.type(screen.getByRole("textbox", { name: "Top-up amount (whole tokens)" }), "1");
    await user.selectOptions(screen.getByLabelText("Network", { exact: true }), "eip155:2");
    expect(await screen.findByRole("alert")).toHaveTextContent("Network switch cancelled");
    expect(screen.getByLabelText("Network", { exact: true })).toHaveValue("eip155:2");
    expect(document.querySelector("#billing-payment-token")).toHaveValue("other");
    expect(screen.getByRole("textbox", { name: "Top-up amount (whole tokens)" })).toHaveValue("");
    expect(document.querySelector(".billing-wallet-balance")).toHaveTextContent("Unavailable");
    expect(document.querySelector(".billing-wallet-balance")).not.toHaveTextContent("123.456789");
    expect(state.readContract).toHaveBeenCalledTimes(1);
    expect(billing.createBillingTopupIntent).not.toHaveBeenCalled();
    const refresh = screen.getByRole("button", { name: "Refresh balance" });
    expect(refresh).toHaveTextContent("");
    state.withBillingChain.mockImplementation(async (_network, operation) =>
      operation({ account: address, chainID: "2", revision: 2 }),
    );
    await user.click(refresh);
    await waitFor(() =>
      expect(state.withBillingChain).toHaveBeenLastCalledWith(
        "eip155:2",
        expect.any(Function),
        true,
      ),
    );
    await openHistory(user);
    expect(state.closeBillingChain).toHaveBeenCalled();
  });

  it("resets amount and token when choosing another payment chain", async () => {
    const other = testBillingConfig({
      id: "other",
      name: "Other USD",
      network: "eip155:31337",
      asset_decimals: 18,
      credit_rate_numerator: "2",
      credit_rate_denominator: "1000000000000",
    });
    vi.mocked(billing.getBillingConfig).mockResolvedValue({
      ...config,
      payment_chains: [
        {
          ...config.payment_chains[0]!,
          tokens: [
            ...config.payment_chains[0]!.tokens,
            { ...config.payment_chains[0]!.tokens[0]!, id: "second", name: "Second USD" },
          ],
        },
        ...other.payment_chains,
      ],
    });
    const { user } = setup();
    await ready();
    await user.type(screen.getByRole("textbox"), "1");
    expect(screen.getByRole("combobox", { name: "Network" })).toHaveTextContent(
      "Test Network (eip155:1)",
    );
    expect(screen.getByRole("combobox", { name: "Asset" })).toHaveTextContent(config.asset!);
    expect(document.querySelector(".billing-payment-destination")).not.toBeInTheDocument();
    await user.selectOptions(screen.getByRole("combobox", { name: "Asset" }), "second");
    expect(screen.getByRole("textbox")).toHaveValue("");
    await user.type(screen.getByRole("textbox"), "2");
    await user.selectOptions(screen.getByRole("combobox", { name: "Network" }), "eip155:31337");
    expect(screen.getByRole("combobox", { name: "Asset" })).toHaveValue("other");
    expect(screen.getByRole("textbox")).toHaveValue("");
    await user.type(screen.getByRole("textbox"), "1");
    expect(screen.getByText("Expected credit: 2 credits")).toBeVisible();
  });
  beforeEach(async () => {
    vi.resetAllMocks();
    state.userID = "user-a";
    state.connected = true;
    state.connect.mockResolvedValue(undefined);
    state.chainID = "1";
    state.crossChain = false;
    state.readContract.mockResolvedValue(encodeAbiParameters([{ type: "uint256" }], [10n ** 40n]));
    await i18n.changeLanguage("en");
    vi.mocked(billing.getBillingConfig).mockResolvedValue(config);
    vi.mocked(billing.getCurrentBillingAccount).mockResolvedValue(account);
    vi.mocked(billing.listCurrentBillingTopupIntents).mockResolvedValue(list([]));
    vi.mocked(billing.listCurrentUserBillingPayments).mockResolvedValue(list([payment]));
    vi.mocked(billing.createBillingTopupIntent).mockResolvedValue(intent);
    vi.mocked(payBillingTopup).mockResolvedValue({
      intent: { ...intent, state: "credited" },
      account,
    });
  });

  it("checks payment-network funds only after explicit cross-chain confirmation", async () => {
    state.chainID = "31337";
    state.crossChain = true;
    state.withBillingChain.mockImplementation(async (_network, operation) => {
      return operation({ account: address, chainID: "1", revision: 2 });
    });
    const { user } = setup();
    await ready();
    expect(state.readContract).not.toHaveBeenCalled();
    await user.type(screen.getByRole("textbox", { name: "Top-up amount (whole tokens)" }), "1");
    await user.click(screen.getByRole("button", { name: "Top up & authorize" }));
    await waitFor(() => expect(payBillingTopup).toHaveBeenCalledTimes(1));
    expect(state.withBillingChain).toHaveBeenCalledWith("eip155:1", expect.any(Function), true);
    expect(state.readContract).toHaveBeenCalledTimes(1);
    expect(vi.mocked(payBillingTopup).mock.calls[0]![0].wallet.active.chainID).toBe("1");
  });

  it.each([
    "USER_REJECTED",
    "CHAIN_MISMATCH",
    "INVALID_PROVIDER_RESPONSE",
    "REQUEST_FAILED",
  ] as const)("blocks payment after %s and retries the original payment", async (code) => {
    state.chainID = "31337";
    state.crossChain = true;
    state.withBillingChain.mockRejectedValueOnce(new BillingChainError("entry", "1", code));
    state.withBillingChain.mockImplementation(async (_network, operation) =>
      operation({ account: address, chainID: "1", revision: 2 }),
    );
    const { user } = setup();
    await ready();
    await user.type(screen.getByRole("textbox", { name: "Top-up amount (whole tokens)" }), "1");
    await user.click(screen.getByRole("button", { name: "Top up & authorize" }));
    await screen.findByRole("alert");
    expect(document.querySelector("#billing-network-error button")).toBeNull();
    const retry = screen.getByRole("button", { name: "Top up & authorize" });
    expect(state.readContract).not.toHaveBeenCalled();
    expect(billing.createBillingTopupIntent).not.toHaveBeenCalled();
    expect(payBillingTopup).not.toHaveBeenCalled();
    expect(
      screen.queryByText("Unable to check wallet balance. Refresh to retry."),
    ).not.toBeInTheDocument();
    await user.click(retry);
    await waitFor(() => expect(payBillingTopup).toHaveBeenCalledTimes(1));
    expect(state.readContract).toHaveBeenCalledTimes(1);
    expect(billing.createBillingTopupIntent).toHaveBeenCalledTimes(1);
  });

  it("queries cross-chain balances without payment and retains zero after restoration", async () => {
    state.chainID = "31337";
    state.crossChain = true;
    state.withBillingChain.mockImplementation(async (_network, operation) =>
      operation({ account: address, chainID: "1", revision: 2 }),
    );
    state.readContract.mockResolvedValue(encodeAbiParameters([{ type: "uint256" }], [0n]));
    const { user } = setup();
    await ready();
    expect(state.readContract).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Refresh balance" }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Refresh balance" })).toBeEnabled(),
    );
    expect(document.querySelector(".billing-wallet-balance")).toHaveTextContent(
      "Available in wallet 0",
    );
    await user.type(screen.getByRole("textbox", { name: "Top-up amount (whole tokens)" }), "1");
    expect(screen.getByText("Insufficient balance. Missing 1 tokens.")).toBeVisible();
    expect(state.readContract).toHaveBeenCalledTimes(1);
    expect(state.withBillingChain).toHaveBeenCalledWith("eip155:1", expect.any(Function), true);
    expect(billing.createBillingTopupIntent).not.toHaveBeenCalled();
    expect(payBillingTopup).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Top up & authorize" })).toBeEnabled();
  });

  it("does not duplicate reads during temporary chain transitions or switch on passive refresh", async () => {
    state.chainID = "31337";
    state.crossChain = true;
    let finish!: (value: string) => void;
    state.readContract.mockReturnValue(
      new Promise<string>((resolve) => {
        finish = resolve;
      }),
    );
    const { user, rerender, client } = setup();
    state.withBillingChain.mockImplementation(async (_network, operation) => {
      state.chainID = "1";
      rerender();
      const value = await operation({ account: address, chainID: "1", revision: 2 });
      state.chainID = "31337";
      rerender();
      return value;
    });
    await ready();
    await user.click(screen.getByRole("button", { name: "Refresh balance" }));
    expect(state.readContract).toHaveBeenCalledTimes(1);
    await act(async () => {
      finish(encodeAbiParameters([{ type: "uint256" }], [123456789n]));
    });
    await waitFor(() =>
      expect(document.querySelector(".billing-wallet-balance")).toHaveTextContent("123.456789"),
    );
    await act(async () => {
      await client.invalidateQueries();
      window.dispatchEvent(new Event("focus"));
    });
    expect(state.withBillingChain).toHaveBeenCalledTimes(1);
    expect(state.readContract).toHaveBeenCalledTimes(1);
  });

  it("allows retry after rejected cross-chain entry and failed reads", async () => {
    state.chainID = "31337";
    state.crossChain = true;
    state.withBillingChain.mockRejectedValueOnce(
      new BillingChainError("entry", "1", "USER_REJECTED"),
    );
    state.withBillingChain.mockImplementation(async (_network, operation) =>
      operation({ account: address, chainID: "1", revision: 2 }),
    );
    const { user } = setup();
    await ready();
    await user.click(screen.getByRole("button", { name: "Refresh balance" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Network switch cancelled");
    const network = screen.getByLabelText("Network", { exact: true });
    expect(network).toHaveAttribute("aria-invalid", "true");
    expect(network).toHaveAccessibleDescription(/Network switch cancelled/);
    expect(document.querySelector(".billing-wallet-balance")).toHaveTextContent("Unavailable");
    await user.click(screen.getByRole("button", { name: "Close" }));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(network).toHaveAccessibleDescription(/Network switch cancelled/);
    expect(state.readContract).not.toHaveBeenCalled();
    state.readContract.mockRejectedValueOnce(new Error("unavailable"));
    await user.click(screen.getByRole("button", { name: "Refresh balance" }));
    await waitFor(() => expect(state.readContract).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Refresh balance" }));
    await waitFor(() =>
      expect(
        screen.queryByText("Unable to check wallet balance. Refresh to retry."),
      ).not.toBeInTheDocument(),
    );
    expect(billing.createBillingTopupIntent).not.toHaveBeenCalled();
  });

  it("locks cross-chain queries and discards results after wallet disconnect", async () => {
    state.chainID = "31337";
    state.crossChain = true;
    let finish!: (value: bigint) => void;
    state.withBillingChain.mockReturnValue(
      new Promise<bigint>((resolve) => {
        finish = resolve;
      }),
    );
    const { user, rerender } = setup();
    await ready();
    await user.dblClick(screen.getByRole("button", { name: "Refresh balance" }));
    expect(state.withBillingChain).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: "Refresh balance" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Top up & authorize" })).toBeDisabled();
    for (const select of screen.getAllByRole("combobox")) expect(select).toBeDisabled();
    state.connected = false;
    rerender();
    await act(async () => {
      finish(123456789n);
    });
    expect(document.querySelector(".billing-wallet-balance")).toBeNull();
    state.connected = true;
    rerender();
    expect(document.querySelector(".billing-wallet-balance")).not.toHaveTextContent("123.456789");
  });

  it("offers wallet connection without signing or creating a payment", async () => {
    state.connected = false;
    const { user, rerender } = setup();
    await ready();
    expect(
      screen.getByText(
        "Connect the wallet for your signed-in account to check your balance and top up.",
      ),
    ).toBeVisible();
    expect(screen.getByRole("button", { name: "Top up & authorize" })).toBeDisabled();
    expect(state.readContract).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Connect wallet" }));
    expect(state.connect).toHaveBeenCalledExactlyOnceWith("test-wallet");
    expect(billing.createBillingTopupIntent).not.toHaveBeenCalled();
    expect(payBillingTopup).not.toHaveBeenCalled();
    state.connected = true;
    rerender();
    await waitFor(() => expect(state.readContract).toHaveBeenCalled());
    expect(screen.queryByRole("button", { name: "Connect wallet" })).not.toBeInTheDocument();
  });

  it("shows prices only in its keyboard-accessible tab and preserves the editable draft", async () => {
    const { user } = setup();
    await ready();
    expect(screen.queryByRole("heading", { name: "Query prices" })).not.toBeInTheDocument();
    await user.type(screen.getByRole("textbox"), "1");
    expect(billing.createBillingTopupIntent).not.toHaveBeenCalled();
    expect(document.querySelector(".billing-step[aria-current=step]")).toHaveTextContent(
      "Enter amount",
    );
    expect(document.querySelectorAll(".billing-step.success")).toHaveLength(0);
    await user.click(screen.getByRole("tab", { name: "Query prices" }));
    expect(screen.getByRole("heading", { name: "Query prices" })).toBeVisible();
    expect(screen.getByText("etherview.account.balance")).toBeVisible();
    expect(screen.queryByText(/etherscan\./)).not.toBeInTheDocument();
    await user.keyboard("{Home}");
    expect(screen.getByRole("tab", { name: "Payment" })).toHaveFocus();
    expect(screen.getByRole("button", { name: "Top up & authorize" })).toBeVisible();
    expect(screen.getByRole("textbox")).toBeEnabled();
    expect(document.querySelector(".billing-step[aria-current=step]")).toHaveTextContent(
      "Enter amount",
    );
  });

  it("blocks a balance decrease after display before creating an intent", async () => {
    const { user } = setup();
    await ready();
    await user.type(screen.getByRole("textbox"), "1");
    state.readContract.mockResolvedValue(encodeAbiParameters([{ type: "uint256" }], [0n]));
    await user.click(screen.getByRole("button", { name: "Top up & authorize" }));
    expect(
      await screen.findByText("Wallet token balance is insufficient. Payment was not submitted."),
    ).toBeVisible();
    expect(billing.createBillingTopupIntent).not.toHaveBeenCalled();
    expect(payBillingTopup).not.toHaveBeenCalled();
  });

  it("blocks authorization on a wallet network change", async () => {
    const { user, rerender } = setup();
    await ready();
    await user.type(screen.getByRole("textbox"), "1");
    state.chainID = "2";
    rerender();
    expect(screen.getByRole("button", { name: "Top up & authorize" })).toBeDisabled();
    expect(billing.createBillingTopupIntent).not.toHaveBeenCalled();
  });

  it("shows only unavailable when the pre-payment balance read fails", async () => {
    const { user } = setup();
    await ready();
    await user.type(screen.getByRole("textbox"), "1");
    state.readContract.mockRejectedValue(new Error("private RPC URL"));
    await user.click(screen.getByRole("button", { name: "Top up & authorize" }));
    await waitFor(() =>
      expect(document.querySelector(".billing-wallet-balance")).toHaveTextContent("Unavailable"),
    );
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(
      screen.queryByText("Unable to check wallet balance. Refresh to retry."),
    ).not.toBeInTheDocument();
    expect(billing.createBillingTopupIntent).not.toHaveBeenCalled();
  });

  it("shows failed balance reads without a false zero and supports retry", async () => {
    state.readContract.mockRejectedValue(new Error("private RPC URL"));
    const { user } = setup();
    expect(await screen.findByText(/Unavailable/)).toBeVisible();
    await user.type(screen.getByRole("textbox"), "1");
    expect(screen.getByRole("button", { name: "Top up & authorize" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Refresh balance" }));
    await waitFor(() => expect(state.readContract).toHaveBeenCalledTimes(2));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(document.querySelector(".billing-wallet-balance")).toHaveTextContent("Unavailable");
    state.readContract.mockResolvedValue(encodeAbiParameters([{ type: "uint256" }], [1000000n]));
    await user.click(screen.getByRole("button", { name: "Refresh balance" }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Top up & authorize" })).toBeEnabled(),
    );
    expect(document.body).not.toHaveTextContent("private RPC URL");
  });

  it("ignores a late balance read after switching wallets", async () => {
    let resolve!: (value: string) => void;
    state.readContract.mockReturnValueOnce(
      new Promise<string>((done) => {
        resolve = done;
      }),
    );
    const { rerender } = setup();
    await ready();
    await waitFor(() => expect(state.readContract).toHaveBeenCalledOnce());
    state.chainID = "2";
    rerender();
    await act(async () => {
      resolve(encodeAbiParameters([{ type: "uint256" }], [123456789n]));
    });
    expect(document.querySelector(".billing-wallet-balance")).not.toHaveTextContent("123.456789");
    expect(screen.getByRole("button", { name: "Top up & authorize" })).toBeDisabled();
  });

  it("submits once while confirmation is pending and refreshes wallet balance on credit", async () => {
    let resolve!: (value: billing.BillingTopupReceipt) => void;
    vi.mocked(payBillingTopup).mockReturnValue(
      new Promise((done) => {
        resolve = done;
      }),
    );
    const { user } = setup();
    await ready();
    await user.type(screen.getByRole("textbox"), "1");
    await user.dblClick(screen.getByRole("button", { name: "Top up & authorize" }));
    await waitFor(() => expect(payBillingTopup).toHaveBeenCalledOnce());
    expect(billing.createBillingTopupIntent).toHaveBeenCalledOnce();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    expect(document.querySelector(".billing-steps")).not.toBeInTheDocument();
    expect(document.querySelector(".billing-topup-status")).toHaveTextContent("Preparing top-up…");
    state.readContract.mockResolvedValue(encodeAbiParameters([{ type: "uint256" }], [987654321n]));
    await act(async () => {
      resolve({ intent: { ...intent, state: "credited" }, account });
    });
    expect(await screen.findByRole("heading", { name: "Top-up successful" })).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Continue topping up" }));
    expect(screen.getByRole("textbox")).toHaveFocus();
    await waitFor(() =>
      expect(document.querySelector(".billing-wallet-balance")).toHaveTextContent("987.654321"),
    );
  });

  it("polls after signed submission and preserves a terminal result against late request failure", async () => {
    let reject!: (reason: Error) => void;
    vi.mocked(payBillingTopup).mockImplementation((options) => {
      options.onProgress?.({ stage: "signing" });
      return new Promise((_resolve, fail) => {
        reject = fail;
      });
    });
    vi.mocked(billing.getBillingTopupIntent).mockResolvedValue({
      ...intent,
      state: "settling",
      submission_progress: "confirming",
      transaction_hash: transactionHash,
    });
    const { user, client } = setup();
    await ready();
    await user.type(screen.getByRole("textbox"), "1");
    await user.click(screen.getByRole("button", { name: "Top up & authorize" }));
    await waitFor(() =>
      expect(document.querySelector("[aria-current=step]")).toHaveTextContent(
        "Waiting for your signature",
      ),
    );
    expect(billing.getBillingTopupIntent).not.toHaveBeenCalled();
    await act(async () => {
      vi.mocked(payBillingTopup).mock.calls[0]![0].onProgress?.({ stage: "submitting" });
    });
    await waitFor(() =>
      expect(document.querySelector("[aria-current=step]")).toHaveTextContent(
        "Waiting for transaction confirmation",
      ),
    );
    expect(screen.getByText(shorten(transactionHash))).toBeVisible();
    expect(screen.getByText(shorten(transactionHash)).closest("a")).toHaveAttribute(
      "href",
      `/tx/${transactionHash}?tab=overview`,
    );
    vi.mocked(billing.getBillingTopupIntent).mockResolvedValue({
      ...intent,
      state: "credited",
      transaction_hash: transactionHash,
    });
    await act(async () => {
      await client.invalidateQueries({
        queryKey: ["private-account", "user-a:2099-01-01", "billing", "intent", intent.id],
      });
    });
    expect(await screen.findByRole("heading", { name: "Top-up successful" })).toBeVisible();
    await act(async () => {
      reject(new TopupPendingError(intent.id));
    });
    expect(screen.getByRole("heading", { name: "Top-up successful" })).toBeVisible();
    expect(payBillingTopup).toHaveBeenCalledOnce();
  });

  it("recovers confirmation progress without opening a wallet or submitting payment", async () => {
    const pending = {
      ...intent,
      state: "settling" as const,
      submission_progress: "confirming" as const,
      transaction_hash: transactionHash,
    };
    vi.mocked(billing.listCurrentBillingTopupIntents).mockResolvedValue(list([pending]));
    vi.mocked(billing.getBillingTopupIntent).mockResolvedValue(pending);
    setup();
    await waitFor(() =>
      expect(document.querySelector("[aria-current=step]")).toHaveTextContent(
        "Waiting for transaction confirmation",
      ),
    );
    expect(screen.getByText(shorten(transactionHash))).toBeVisible();
    expect(screen.getByText(shorten(transactionHash)).closest("a")).toHaveAttribute(
      "href",
      `/tx/${transactionHash}?tab=overview`,
    );
    expect(payBillingTopup).not.toHaveBeenCalled();
  });

  it.each(["eip155:1", "eip155:2"])(
    "keeps immediate results bound to intent network %s",
    async (network) => {
      vi.mocked(payBillingTopup).mockResolvedValue({
        intent: { ...intent, state: "credited", network, transaction_hash: transactionHash },
        account,
      });
      const { user, rerender } = setup();
      await ready();
      await user.type(screen.getByRole("textbox"), "1");
      await user.click(screen.getByRole("button", { name: "Top up & authorize" }));
      await screen.findByRole("heading", { name: "Top-up successful" });
      state.chainID = "2";
      rerender();
      const displayed = screen.getByTitle(transactionHash);
      expect(displayed).toHaveTextContent(shorten(transactionHash));
      if (network === "eip155:1")
        expect(displayed.closest("a")).toHaveAttribute(
          "href",
          `/tx/${transactionHash}?tab=overview`,
        );
      else expect(displayed.closest("a")).toBeNull();
      await user.click(screen.getByRole("button", { name: "Continue topping up" }));
      expect(screen.queryByTitle(transactionHash)).not.toBeInTheDocument();
    },
  );

  it("uses the recovered transaction network instead of the selected payment option", async () => {
    const recovered = {
      ...intent,
      network: "eip155:2",
      state: "settling" as const,
      transaction_hash: transactionHash,
    };
    vi.mocked(billing.listCurrentBillingTopupIntents).mockResolvedValue(list([recovered]));
    vi.mocked(billing.getBillingTopupIntent).mockResolvedValue(recovered);
    setup();
    expect(await screen.findByTitle(transactionHash)).toHaveTextContent(shorten(transactionHash));
    expect(screen.getByTitle(transactionHash).closest("a")).toBeNull();
  });

  it("keeps authorization hashes bound to the originating intent on failure", async () => {
    const approvalHash = `0x${"a".repeat(64)}` as const;
    vi.mocked(payBillingTopup).mockImplementation(async ({ onProgress }) => {
      onProgress?.({ stage: "approval", approvalHash });
      throw new Error("failed");
    });
    const { user, rerender } = setup();
    await ready();
    await user.type(screen.getByRole("textbox"), "1");
    await user.click(screen.getByRole("button", { name: "Top up & authorize" }));
    await screen.findByRole("alert");
    state.chainID = "2";
    rerender();
    expect(screen.getByTitle(approvalHash).closest("a")).toHaveAttribute(
      "href",
      `/tx/${approvalHash}?tab=overview`,
    );
  });

  it("does not link historical transactions from a different chain", async () => {
    vi.mocked(billing.listCurrentBillingTopupIntents).mockResolvedValue(
      list([
        { ...intent, network: "eip155:2", state: "credited", transaction_hash: transactionHash },
      ]),
    );
    const { user } = setup();
    await ready();
    await openHistory(user);
    await user.click(await screen.findByText("Details", { exact: true }));
    expect(screen.getByTitle(transactionHash)).toHaveTextContent(shorten(transactionHash));
    expect(screen.getByTitle(transactionHash).closest("a")).toBeNull();
  });

  it("formats historical amounts using their immutable option snapshot", async () => {
    vi.mocked(billing.listCurrentBillingTopupIntents).mockResolvedValue(
      list([{ ...intent, network: "eip155:84532" }]),
    );
    const { user } = setup();
    await ready();
    await openHistory(user);
    expect((await screen.findAllByText("0.000123", { exact: true }))[0]).toBeVisible();
    expect(document.querySelector(".billing-record-amount")?.textContent).toBe(
      "0.000123Token amount",
    );
  });

  it("preserves the draft and exact balances while lazily switching histories and independent cursors", async () => {
    vi.mocked(billing.listCurrentBillingTopupIntents).mockImplementation(async (_limit, cursor) =>
      list([{ ...intent, id: cursor ?? "first" }], cursor ? undefined : "topup/page 2"),
    );
    vi.mocked(billing.listCurrentUserBillingPayments).mockImplementation(async (_limit, cursor) =>
      list([payment], cursor ? undefined : "payment/page 2"),
    );
    const { user } = setup();
    await ready();
    expect(screen.getByRole("tab", { name: "Payment" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("340282366920938463463374607431768.211455")).toBeVisible();
    expect(billing.listCurrentUserBillingPayments).not.toHaveBeenCalled();
    await user.type(screen.getByRole("textbox"), "120");
    await user.selectOptions(screen.getByRole("combobox", { name: "Payment method" }), "permit2");
    await openHistory(user);
    expect((await screen.findAllByText("0.000123", { exact: true }))[0]).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Next page" }));
    await waitFor(() =>
      expect(billing.listCurrentBillingTopupIntents).toHaveBeenCalledWith(10, "topup/page 2"),
    );
    await user.selectOptions(screen.getByRole("combobox", { name: "Record type" }), "payments");
    expect(await screen.findByText("456 atomic", { exact: true })).toBeVisible();
    expect(screen.getByText("Page 1")).toBeVisible();
    await user.click(screen.getByText("Details", { exact: true }));
    expect(screen.getByText(shorten(transactionHash))).toBeVisible();
    expect(screen.getByText(shorten(transactionHash)).closest("a")).toHaveAttribute(
      "href",
      `/tx/${transactionHash}?tab=overview`,
    );
    expect(document.body).not.toHaveTextContent("hidden-user");
    expect(document.body).not.toHaveTextContent("hidden-key");
    await user.click(screen.getByRole("button", { name: "Next page" }));
    await waitFor(() =>
      expect(billing.listCurrentUserBillingPayments).toHaveBeenCalledWith(10, "payment/page 2"),
    );
    await user.selectOptions(screen.getByRole("combobox", { name: "Record type" }), "topups");
    expect(screen.getByText("Page 2")).toBeVisible();
    await user.click(screen.getByRole("tab", { name: "Payment" }));
    expect(screen.getByRole("textbox")).toHaveValue("120");
    expect(screen.getByRole("combobox", { name: "Payment method" })).toHaveValue("permit2");
  });

  it("keeps balance and histories available when funding is disabled", async () => {
    vi.mocked(billing.getBillingConfig).mockResolvedValue(
      testBillingConfig({
        ...config,
        x402_topups_enabled: false,
      }),
    );
    const { user } = setup();
    expect(await screen.findByText(/Top-ups are currently unavailable/)).toBeVisible();
    expect(screen.getByText("340282366920938463463374607431768.211455")).toBeVisible();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    await openHistory(user);
    expect(await screen.findByText("No records yet.")).toBeVisible();
    await user.selectOptions(screen.getByRole("combobox"), "payments");
    expect(await screen.findByText("456 atomic")).toBeVisible();
  });

  it("retries balance and history failures without rendering false empty results", async () => {
    vi.mocked(billing.getCurrentBillingAccount).mockRejectedValueOnce(
      new Error("private backend detail"),
    );
    vi.mocked(billing.listCurrentUserBillingPayments).mockRejectedValueOnce(
      new Error("private backend detail"),
    );
    const { user } = setup();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Balance is unavailable");
    expect(document.body).not.toHaveTextContent("private backend detail");
    await user.click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("340282366920938463463374607431768.211455")).toBeVisible();
    await openHistory(user);
    await user.selectOptions(screen.getByRole("combobox"), "payments");
    const historyError = await screen.findByRole("alert");
    expect(screen.queryByText("No records yet.")).not.toBeInTheDocument();
    await user.click(within(historyError).getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("456 atomic")).toBeVisible();
  });

  it("blocks new payments until recovery succeeds and keeps pending failures inspectable", async () => {
    vi.mocked(billing.listCurrentBillingTopupIntents).mockRejectedValueOnce(
      new Error("private recovery error"),
    );
    const { user, client } = setup();
    const recoveryError = await screen.findByRole("alert");
    expect(recoveryError).toHaveTextContent("Recent payment status could not be checked");
    expect(screen.getByRole("button", { name: "Top up & authorize" })).toBeDisabled();
    await user.click(within(recoveryError).getByRole("button", { name: "Try again" }));
    await ready();
    vi.mocked(billing.listCurrentBillingTopupIntents).mockResolvedValue(
      list([{ ...intent, state: "settling", transaction_hash: transactionHash }]),
    );
    vi.mocked(billing.getBillingTopupIntent).mockRejectedValueOnce(
      new Error("private status error"),
    );
    await act(async () => {
      await client.invalidateQueries({
        queryKey: ["private-account", "user-a:2099-01-01", "billing", "recovery"],
      });
    });
    const pendingError = await screen.findByRole("alert");
    expect(pendingError).toHaveTextContent("Payment status is unavailable");
    expect(screen.getByText(shorten(transactionHash))).toBeVisible();
    expect(screen.getByText(shorten(transactionHash)).closest("a")).toHaveAttribute(
      "href",
      `/tx/${transactionHash}?tab=overview`,
    );
    expect(screen.queryByRole("button", { name: "Top up & authorize" })).not.toBeInTheDocument();
    expect(document.body).not.toHaveTextContent("private status error");
    vi.mocked(billing.getBillingTopupIntent).mockResolvedValue({ ...intent, state: "failed" });
    await user.click(within(pendingError).getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("Top-up failed or needs inspection.")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Continue topping up" }));
    await ready();
    expect(billing.createBillingTopupIntent).not.toHaveBeenCalled();
  });

  it("shows a single method without a selector, validates bounds, and submits once", async () => {
    vi.mocked(billing.getBillingConfig).mockResolvedValue(
      testBillingConfig({
        ...config,
        payment_chains: undefined,
        asset_transfer_methods: ["permit2"],
      }),
    );
    const { user } = setup();
    await ready();
    expect(screen.queryByRole("combobox", { name: "Payment method" })).not.toBeInTheDocument();
    await user.type(screen.getByRole("textbox"), "0.000009");
    expect(screen.getByRole("button", { name: "Top up & authorize" })).toBeDisabled();
    await user.clear(screen.getByRole("textbox"));
    await user.type(screen.getByRole("textbox"), "1.25");
    expect(billing.createBillingTopupIntent).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Top up & authorize" }));
    expect(await screen.findByText("0.000123 was credited.")).toBeVisible();
    expect(document.querySelector(".billing-steps")).not.toBeInTheDocument();
    expect(billing.createBillingTopupIntent).toHaveBeenCalledExactlyOnceWith(
      "1250000",
      "test-csrf",
      "default",
    );
    expect(payBillingTopup).toHaveBeenCalledWith(expect.objectContaining({ method: "permit2" }));
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Continue topping up" }));
    expect(screen.getByRole("textbox")).toHaveValue("");
  });

  it("distinguishes authorization failure from uncertain settlement without exposing details", async () => {
    vi.mocked(payBillingTopup).mockRejectedValueOnce(
      Object.assign(new Error("private SDK detail"), { name: "TopupAuthorizationError" }),
    );
    const { user } = setup();
    await ready();
    await user.type(screen.getByRole("textbox"), "1.25");
    expect(billing.createBillingTopupIntent).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Top up & authorize" }));
    expect(await screen.findByText("Payment was not submitted.")).toBeVisible();
    expect(document.querySelector(".billing-topup-status.failure")).toBeVisible();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    expect(document.body).not.toHaveTextContent("private SDK detail");
    expect(payBillingTopup).toHaveBeenCalledOnce();
  });

  it("recovers a pending transaction across views without repaying and refreshes on credit", async () => {
    const pendingIntent = {
      ...intent,
      state: "settling" as const,
      transaction_hash: transactionHash,
    };
    vi.mocked(billing.listCurrentBillingTopupIntents).mockResolvedValue(list([pendingIntent]));
    vi.mocked(billing.getBillingTopupIntent).mockResolvedValue(pendingIntent);
    const { user, client } = setup();
    expect(await screen.findByText(shorten(transactionHash))).toBeVisible();
    expect(document.querySelector(".billing-topup-status.pending")).toBeVisible();
    expect(screen.queryByRole("button", { name: "Continue topping up" })).not.toBeInTheDocument();
    await openHistory(user);
    expect(document.querySelector(".billing-topup-status")).not.toBeInTheDocument();
    vi.mocked(billing.getBillingTopupIntent).mockResolvedValue({
      ...pendingIntent,
      state: "credited",
    });
    await act(async () => {
      await client.invalidateQueries({
        queryKey: ["private-account", "user-a:2099-01-01", "billing", "intent"],
      });
    });
    await user.click(screen.getByRole("tab", { name: "Payment" }));
    expect(await screen.findByText("0.000123 was credited.")).toBeVisible();
    expect(billing.createBillingTopupIntent).not.toHaveBeenCalled();
    expect(payBillingTopup).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Continue topping up" }));
    await ready();
  });

  it("retains the exact pending hash when lookups fail and the recovery list changes", async () => {
    const otherHash = `0x${"7".repeat(64)}` as const;
    vi.mocked(billing.listCurrentBillingTopupIntents).mockResolvedValue(
      list([{ ...intent, state: "settling", transaction_hash: transactionHash }]),
    );
    vi.mocked(billing.getBillingTopupIntent).mockRejectedValue(new Error("lookup unavailable"));
    const { client } = setup();
    expect(await screen.findByText("Payment status is unavailable")).toBeVisible();
    expect(screen.getByText(shorten(transactionHash))).toBeVisible();
    expect(screen.getByText(shorten(transactionHash)).closest("a")).toHaveAttribute(
      "href",
      `/tx/${transactionHash}?tab=overview`,
    );
    // A newer pending intent can push the currently tracked one off page one.
    vi.mocked(billing.listCurrentBillingTopupIntents).mockResolvedValue(
      list([{ ...intent, id: "topup-b", state: "settling", transaction_hash: otherHash }]),
    );
    await act(async () => {
      await client.invalidateQueries({
        queryKey: ["private-account", "user-a:2099-01-01", "billing", "recovery"],
      });
    });
    expect(screen.getByText(shorten(transactionHash))).toBeVisible();
    expect(screen.getByText(shorten(transactionHash)).closest("a")).toHaveAttribute(
      "href",
      `/tx/${transactionHash}?tab=overview`,
    );
    expect(screen.queryByText(otherHash)).not.toBeInTheDocument();
    expect(billing.getBillingTopupIntent).toHaveBeenCalledWith(intent.id);
    expect(billing.getBillingTopupIntent).not.toHaveBeenCalledWith("topup-b");
    expect(screen.queryByRole("button", { name: "Top up & authorize" })).not.toBeInTheDocument();
    expect(billing.createBillingTopupIntent).not.toHaveBeenCalled();
  });

  it("keeps an uncertain submitted payment pending through history navigation", async () => {
    vi.mocked(payBillingTopup).mockRejectedValue(new TopupPendingError(intent.id));
    vi.mocked(billing.getBillingTopupIntent).mockResolvedValue({
      ...intent,
      state: "settling",
      transaction_hash: transactionHash,
    });
    const { user } = setup();
    await ready();
    await user.type(screen.getByRole("textbox"), "1.25");
    expect(billing.createBillingTopupIntent).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Top up & authorize" }));
    expect(await screen.findByText(shorten(transactionHash))).toBeVisible();
    await openHistory(user);
    await user.click(screen.getByRole("tab", { name: "Payment" }));
    expect(screen.queryByRole("button", { name: "Top up & authorize" })).not.toBeInTheDocument();
    expect(payBillingTopup).toHaveBeenCalledTimes(1);
  });

  it("discards old drafts and late intent creation on identity change before wallet authorization", async () => {
    let resolve!: (value: billing.BillingTopupIntent) => void;
    vi.mocked(billing.createBillingTopupIntent).mockReturnValue(
      new Promise((done) => {
        resolve = done;
      }),
    );
    const { user, rerender } = setup();
    await ready();
    await user.type(screen.getByRole("textbox"), "1.25");
    expect(billing.createBillingTopupIntent).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Top up & authorize" }));
    await waitFor(() => expect(billing.createBillingTopupIntent).toHaveBeenCalledTimes(1));
    state.userID = "user-b";
    rerender();
    await ready();
    expect(screen.getByRole("textbox")).toHaveValue("");
    await act(async () => {
      resolve(intent);
    });
    expect(payBillingTopup).not.toHaveBeenCalled();
    expect(screen.queryByText("0.000123 was credited.")).not.toBeInTheDocument();
  });
});
