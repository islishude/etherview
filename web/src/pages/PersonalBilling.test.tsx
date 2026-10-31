import { encodeAbiParameters } from "viem";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import * as billing from "@/api/billing";
import { payBillingTopup, TopupPendingError } from "@/billing/topup";
import i18n from "@/i18n";
import { PersonalBilling } from "./PersonalBilling";

const state = vi.hoisted(() => ({
  userID: "user-a",
  connected: true,
  connect: vi.fn(),
  chainID: "1",
  readContract: vi.fn(),
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
      ? { account: "0x1111111111111111111111111111111111111111", chainID: state.chainID }
      : undefined,
    providers: [{ uuid: "test-wallet", name: "Test wallet" }],
    connect: state.connect,
    discover: vi.fn(),
    connecting: false,
    readContract: state.readContract,
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
const config: billing.BillingConfig = {
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
    { operation: "etherscan.account.balance", amount_atomic: "2", complexity_weight: 2 },
  ],
};
const account: billing.BillingAccount = {
  user_id: "user-a",
  network: "eip155:1",
  asset: address,
  available_atomic: amount,
  reserved_atomic: "0",
  total_credit_atomic: amount,
  total_debit_atomic: "10",
  created_at: "2026-10-30T00:00:00Z",
  updated_at: "2026-10-30T00:00:00Z",
};
const intent: billing.BillingTopupIntent = {
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
};
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
  beforeEach(async () => {
    vi.resetAllMocks();
    state.userID = "user-a";
    state.connected = true;
    state.connect.mockResolvedValue(undefined);
    state.chainID = "1";
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

  it("shows failed balance reads without a false zero and supports retry", async () => {
    state.readContract.mockRejectedValue(new Error("private RPC URL"));
    const { user } = setup();
    expect(
      await screen.findByText("Unable to check wallet balance. Refresh to retry."),
    ).toBeVisible();
    await user.type(screen.getByRole("textbox"), "1");
    expect(screen.getByRole("button", { name: "Top up & authorize" })).toBeDisabled();
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
    state.readContract.mockResolvedValue(encodeAbiParameters([{ type: "uint256" }], [987654321n]));
    await act(async () => {
      resolve({ intent: { ...intent, state: "credited" }, account });
    });
    await waitFor(() =>
      expect(document.querySelector(".billing-wallet-balance")).toHaveTextContent("987.654321"),
    );
  });

  it("shows unmatched historical assets in atomic units exactly once", async () => {
    vi.mocked(billing.listCurrentBillingTopupIntents).mockResolvedValue(
      list([{ ...intent, network: "eip155:84532" }]),
    );
    const { user } = setup();
    await ready();
    await openHistory(user);
    expect(await screen.findByText("123 atomic", { exact: true })).toBeVisible();
    expect(document.querySelector(".billing-record-amount")?.textContent).toBe("123 atomic");
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
    expect(await screen.findByText("0.000123", { exact: true })).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Next page" }));
    await waitFor(() =>
      expect(billing.listCurrentBillingTopupIntents).toHaveBeenCalledWith(10, "topup/page 2"),
    );
    await user.selectOptions(screen.getByRole("combobox", { name: "Record type" }), "payments");
    expect(await screen.findByText("0.000456", { exact: true })).toBeVisible();
    expect(screen.getByText("Page 1")).toBeVisible();
    await user.click(screen.getByText("Details", { exact: true }));
    expect(screen.getByText(transactionHash)).toBeVisible();
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
    vi.mocked(billing.getBillingConfig).mockResolvedValue({
      ...config,
      x402_topups_enabled: false,
    });
    const { user } = setup();
    expect(await screen.findByText(/Top-ups are currently unavailable/)).toBeVisible();
    expect(screen.getByText("340282366920938463463374607431768.211455")).toBeVisible();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    await openHistory(user);
    expect(await screen.findByText("No records yet.")).toBeVisible();
    await user.selectOptions(screen.getByRole("combobox"), "payments");
    expect(await screen.findByText("0.000456")).toBeVisible();
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
    expect(await screen.findByText("0.000456")).toBeVisible();
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
    expect(screen.getByText(transactionHash)).toBeVisible();
    expect(screen.getByRole("button", { name: "Top up & authorize" })).toBeDisabled();
    expect(document.body).not.toHaveTextContent("private status error");
    vi.mocked(billing.getBillingTopupIntent).mockResolvedValue({ ...intent, state: "failed" });
    await user.click(within(pendingError).getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("Top-up failed or needs inspection.")).toBeVisible();
    await ready();
    expect(billing.createBillingTopupIntent).not.toHaveBeenCalled();
  });

  it("shows a single method without a selector, validates bounds, and submits once", async () => {
    vi.mocked(billing.getBillingConfig).mockResolvedValue({
      ...config,
      asset_transfer_methods: ["permit2"],
    });
    const { user } = setup();
    await ready();
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
    await user.type(screen.getByRole("textbox"), "0.000009");
    expect(screen.getByRole("button", { name: "Top up & authorize" })).toBeDisabled();
    await user.clear(screen.getByRole("textbox"));
    await user.type(screen.getByRole("textbox"), "1.25");
    expect(billing.createBillingTopupIntent).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Top up & authorize" }));
    expect(await screen.findByText("0.000123 was credited.")).toBeVisible();
    expect(document.querySelectorAll(".billing-step.success")).toHaveLength(3);
    expect(billing.createBillingTopupIntent).toHaveBeenCalledExactlyOnceWith(
      "1250000",
      "test-csrf",
    );
    expect(payBillingTopup).toHaveBeenCalledWith(expect.objectContaining({ method: "permit2" }));
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
    expect(document.querySelector(".billing-step.error")).toHaveTextContent("Enter amount");
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
    expect(await screen.findByText(transactionHash)).toBeVisible();
    expect(document.querySelector(".billing-step[aria-current=step]")).toHaveTextContent(
      "Top-up result",
    );
    expect(screen.getByRole("button", { name: "Top up & authorize" })).toBeDisabled();
    await openHistory(user);
    expect(screen.getByText(transactionHash, { selector: ".billing-pending code" })).toBeVisible();
    vi.mocked(billing.getBillingTopupIntent).mockResolvedValue({
      ...pendingIntent,
      state: "credited",
    });
    await act(async () => {
      await client.invalidateQueries({
        queryKey: ["private-account", "user-a:2099-01-01", "billing", "intent"],
      });
    });
    expect(await screen.findByText("0.000123 was credited.")).toBeVisible();
    expect(billing.createBillingTopupIntent).not.toHaveBeenCalled();
    expect(payBillingTopup).not.toHaveBeenCalled();
    await user.click(screen.getByRole("tab", { name: "Payment" }));
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
    expect(screen.getByText(transactionHash)).toBeVisible();
    // A newer pending intent can push the currently tracked one off page one.
    vi.mocked(billing.listCurrentBillingTopupIntents).mockResolvedValue(
      list([{ ...intent, id: "topup-b", state: "settling", transaction_hash: otherHash }]),
    );
    await act(async () => {
      await client.invalidateQueries({
        queryKey: ["private-account", "user-a:2099-01-01", "billing", "recovery"],
      });
    });
    expect(screen.getByText(transactionHash)).toBeVisible();
    expect(screen.queryByText(otherHash)).not.toBeInTheDocument();
    expect(billing.getBillingTopupIntent).toHaveBeenCalledWith(intent.id);
    expect(billing.getBillingTopupIntent).not.toHaveBeenCalledWith("topup-b");
    expect(screen.getByRole("button", { name: "Top up & authorize" })).toBeDisabled();
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
    expect(await screen.findByText(transactionHash)).toBeVisible();
    await openHistory(user);
    await user.click(screen.getByRole("tab", { name: "Payment" }));
    expect(screen.getByRole("button", { name: "Top up & authorize" })).toBeDisabled();
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
