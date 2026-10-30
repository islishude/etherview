import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import * as billing from "@/api/billing";
import { payBillingTopup, TopupPendingError } from "@/billing/topup";
import i18n from "@/i18n";
import { PersonalBilling } from "./PersonalBilling";

const state = vi.hoisted(() => ({ userID: "user-a" }));
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
    active: { account: "0x1111111111111111111111111111111111111111", chainID: "1" },
    readContract: vi.fn(),
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
  operations: [],
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
  await user.click(screen.getByRole("button", { name: "History" }));
}
async function ready() {
  await waitFor(() =>
    expect(screen.getByRole("textbox", { name: "Top-up amount (atomic units)" })).toBeEnabled(),
  );
}

describe("personal billing workspace", () => {
  beforeEach(async () => {
    vi.resetAllMocks();
    state.userID = "user-a";
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

  it("preserves the draft and exact balances while lazily switching histories and independent cursors", async () => {
    vi.mocked(billing.listCurrentBillingTopupIntents).mockImplementation(async (_limit, cursor) =>
      list([{ ...intent, id: cursor ?? "first" }], cursor ? undefined : "topup/page 2"),
    );
    vi.mocked(billing.listCurrentUserBillingPayments).mockImplementation(async (_limit, cursor) =>
      list([payment], cursor ? undefined : "payment/page 2"),
    );
    const { user } = setup();
    await ready();
    expect(screen.getByRole("button", { name: "Payment" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByText(amount)).toBeVisible();
    expect(billing.listCurrentUserBillingPayments).not.toHaveBeenCalled();
    await user.type(screen.getByRole("textbox"), "120");
    await user.selectOptions(screen.getByRole("combobox", { name: "Payment method" }), "permit2");
    await openHistory(user);
    expect(await screen.findByText("123", { exact: true })).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Next page" }));
    await waitFor(() =>
      expect(billing.listCurrentBillingTopupIntents).toHaveBeenCalledWith(10, "topup/page 2"),
    );
    await user.selectOptions(screen.getByRole("combobox", { name: "Record type" }), "payments");
    expect(await screen.findByText("456", { exact: true })).toBeVisible();
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
    await user.click(screen.getByRole("button", { name: "Payment" }));
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
    expect(screen.getByText(amount)).toBeVisible();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    await openHistory(user);
    expect(await screen.findByText("No records yet.")).toBeVisible();
    await user.selectOptions(screen.getByRole("combobox"), "payments");
    expect(await screen.findByText("456")).toBeVisible();
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
    expect(await screen.findByText(amount)).toBeVisible();
    await openHistory(user);
    await user.selectOptions(screen.getByRole("combobox"), "payments");
    const historyError = await screen.findByRole("alert");
    expect(screen.queryByText("No records yet.")).not.toBeInTheDocument();
    await user.click(within(historyError).getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("456")).toBeVisible();
  });

  it("blocks new payments until recovery succeeds and keeps pending failures inspectable", async () => {
    vi.mocked(billing.listCurrentBillingTopupIntents).mockRejectedValueOnce(
      new Error("private recovery error"),
    );
    const { user, client } = setup();
    const recoveryError = await screen.findByRole("alert");
    expect(recoveryError).toHaveTextContent("Recent payment status could not be checked");
    expect(screen.getByRole("button", { name: "Confirm and top up" })).toBeDisabled();
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
    expect(screen.getByRole("button", { name: "Confirm and top up" })).toBeDisabled();
    expect(document.body).not.toHaveTextContent("private status error");
    vi.mocked(billing.getBillingTopupIntent).mockResolvedValue({ ...intent, state: "failed" });
    await user.click(within(pendingError).getByRole("button", { name: "Try again" }));
    expect(
      await screen.findByText("The top-up failed or its outcome requires inspection."),
    ).toBeVisible();
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
    await user.type(screen.getByRole("textbox"), "9");
    expect(screen.getByRole("button", { name: "Confirm and top up" })).toBeDisabled();
    await user.clear(screen.getByRole("textbox"));
    await user.type(screen.getByRole("textbox"), "123");
    await user.click(screen.getByRole("button", { name: "Confirm and top up" }));
    expect(await screen.findByText("123 atomic units were credited.")).toBeVisible();
    expect(billing.createBillingTopupIntent).toHaveBeenCalledExactlyOnceWith("123", "test-csrf");
    expect(payBillingTopup).toHaveBeenCalledWith(expect.objectContaining({ method: "permit2" }));
    expect(screen.getByRole("textbox")).toHaveValue("");
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
    expect(screen.getByRole("button", { name: "Confirm and top up" })).toBeDisabled();
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
    expect(await screen.findByText("123 atomic units were credited.")).toBeVisible();
    expect(billing.createBillingTopupIntent).not.toHaveBeenCalled();
    expect(payBillingTopup).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Payment" }));
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
    expect(screen.getByRole("button", { name: "Confirm and top up" })).toBeDisabled();
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
    await user.type(screen.getByRole("textbox"), "123");
    await user.click(screen.getByRole("button", { name: "Confirm and top up" }));
    expect(await screen.findByText(transactionHash)).toBeVisible();
    await openHistory(user);
    await user.click(screen.getByRole("button", { name: "Payment" }));
    expect(screen.getByRole("button", { name: "Confirm and top up" })).toBeDisabled();
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
    await user.type(screen.getByRole("textbox"), "123");
    await user.click(screen.getByRole("button", { name: "Confirm and top up" }));
    await waitFor(() => expect(billing.createBillingTopupIntent).toHaveBeenCalledTimes(1));
    state.userID = "user-b";
    rerender();
    await ready();
    expect(screen.getByRole("textbox")).toHaveValue("");
    await act(async () => {
      resolve(intent);
    });
    expect(payBillingTopup).not.toHaveBeenCalled();
    expect(screen.queryByText("123 atomic units were credited.")).not.toBeInTheDocument();
  });
});
