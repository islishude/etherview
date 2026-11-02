import { BillingTransactionHash } from "./BillingTransactionHash";
import {
  fundingBalanceBlocked,
  fundingWalletKey,
  useFundingView,
  useFundingNetworkError,
  useFundingSelection,
  useFundingBalance,
  type ChainFailure,
} from "./useFundingWallet";
import { TopupProgressSteps } from "./TopupProgressSteps";
import { mergeTopupObservation, type TopupProgress } from "@/billing/progress";
import { BillingWalletConnect } from "./BillingWalletConnect";
import { BillingSteps } from "./BillingSteps";
import { checkTopupBalance, TopupBalanceError } from "@/billing/balance";
import "@/i18n/resources/billingPrices";
import "./PersonalBilling.css";
import { type ReactNode, type SubmitEvent, useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { CircleCheck, LoaderCircle, RefreshCw, X } from "lucide-react";

import {
  createBillingTopupIntent,
  getBillingConfig,
  getBillingTopupIntent,
  getCurrentBillingAccount,
  listCurrentBillingTopupIntents,
  listCurrentUserBillingPayments,
  type BillingAccount,
  type BillingConfig,
  type BillingPaymentConfig,
  type BillingPaymentOption,
  type BillingPayment,
  type BillingTopupIntent,
  type BillingTransferMethod,
} from "@/api/billing";
import { usePrivateIdentity, usePrivateLifetime } from "@/api/privateAccount";
import { useAuth } from "@/auth/AuthProvider";
import { formatTimestamp } from "@/components/format";
import { SelectControl } from "@/components/SelectControl";
import { Message } from "@/components/Message";
import { UIIcon } from "@/components/UIIcon";
import { type ActiveWallet, useWallet } from "@/wallet/WalletProvider";
import { BillingChainError, chainsMatch } from "@/wallet/eip6963";
import { BillingLoadError, BillingPagination } from "./BillingPages";

import {
  amountInputLength,
  creditAmount,
  tokenCreditRate,
  formatTokenAmount,
  displayBillingAmount,
  matchingAssetConfig,
  topupAmount,
} from "@/billing/amount";
import { BillingPrices } from "./BillingPrices";

const PAGE_SIZE = 10;
type HistoryKind = "topups" | "payments";

// The account route keys this workspace by session identity. Switching its views
// keeps payment state alive; replacing the session cancels further wallet work.
export function PersonalBilling() {
  const { t } = useTranslation();
  const auth = useAuth();
  const wallet = useWallet();
  const identity = usePrivateIdentity();
  const lifetime = usePrivateLifetime();
  const client = useQueryClient();
  const [view, setView] = useState<"payment" | "history" | "prices">("payment");
  const [amount, setAmount] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const submittingRef = useRef(false);
  const [checkingBalance, setCheckingBalance] = useState(false);
  const checkingBalanceRef = useRef(false);
  const [chainFailure, setChainFailure] = useState<ChainFailure>();
  const [notification, setNotification] = useState<string>();
  const [notice, setNotice] = useState<string>();
  const [historyRevision, setHistoryRevision] = useState(0);
  const [error, setError] = useState<string>();
  const [pendingID, setPendingID] = useState<string>();
  const completed = useRef(new Set<string>());
  const prefix = ["private-account", identity, "billing"];
  const config = useQuery({
    queryKey: [...prefix, "config"],
    queryFn: getBillingConfig,
    retry: false,
  });
  const {
    paymentChain,
    paymentOption,
    payment,
    setPaymentNetwork,
    setPaymentOptionID,
    method,
    setMethod,
  } = usePaymentSelection(config.data);
  const account = useQuery({
    queryKey: [...prefix, "account"],
    queryFn: getCurrentBillingAccount,
    retry: false,
  });
  // Recovery has its own first-page query, independent of history pagination.
  const recovery = useQuery({
    queryKey: [...prefix, "recovery"],
    queryFn: () => listCurrentBillingTopupIntents(PAGE_SIZE),
    retry: false,
    staleTime: 0,
  });
  const recoverable = recovery.data?.data.find(
    (intent) =>
      (intent.state === "processing" || intent.state === "settling") &&
      !completed.current.has(intent.id),
  );
  const pending = useQuery({
    queryKey: [...prefix, "intent", pendingID],
    queryFn: async () => {
      const next = await getBillingTopupIntent(pendingID!);
      const previous = client.getQueryData<BillingTopupIntent>([...prefix, "intent", pendingID]);
      return mergeTopupObservation(previous, next);
    },
    // Seed only this intent so its known transaction survives a failed lookup
    // and recovery-list reordering. Still fetch its authoritative current state.
    initialData: () => recovery.data?.data.find((intent) => intent.id === pendingID),
    initialDataUpdatedAt: 0,
    enabled: Boolean(pendingID),
    retry: false,
    refetchInterval: pendingID ? 3_000 : false,
  });
  const {
    progress,
    setProgress,
    approvalRequired,
    setApprovalRequired,
    transaction,
    setResultTransaction,
    approvalNetwork,
    setApprovalNetwork,
    resetProgress,
    waiting,
    stage,
  } = useTopupProgress(pendingID, pending.data, recovery.data?.data, recoverable, submitting);
  const refreshBilling = () => client.invalidateQueries({ queryKey: prefix });

  useEffect(() => {
    if (!pendingID && !submitting && recoverable) setPendingID(recoverable.id);
  }, [pendingID, recoverable, submitting]);

  useEffect(() => {
    const intent = pending.data;
    if (!pendingID || intent?.id !== pendingID) return;
    if (!["credited", "failed", "expired"].includes(intent.state)) return;
    completed.current.add(intent.id);
    setResultTransaction(topupTransaction(intent));
    setPendingID(undefined);
    setError(undefined);
    setNotice(undefined);
    setChainFailure(undefined);
    if (intent.state === "credited") {
      setNotice(
        t("billing.topup.credited", {
          amount: displayBillingAmount(intent.credit_amount_atomic, { credit_decimals: 6 }),
        }),
      );
      setAmount("");
    } else {
      setError(t("billing.topup.failed"));
    }
    void client.invalidateQueries({ queryKey: ["private-account", identity, "billing"] });
  }, [client, identity, pending.data, pendingID, setResultTransaction, t]);

  const { onPaymentChain, crossChain, walletReady } = walletFundingState(
    wallet,
    auth.session.user?.address,
    payment,
  );
  const viewRevision = useFundingView(view, wallet.closeBillingChain);
  const walletKey = fundingWalletKey(identity, wallet.active, payment) + `:${viewRevision}`;
  const currentWalletKey = useRef(walletKey);
  currentWalletKey.current = walletKey;
  const networkError = useFundingNetworkError(
    chainFailure,
    walletKey,
    paymentChain?.name,
    setNotification,
    setChainFailure,
  );
  const { balance, balanceError, refreshBalance, markBalanceUnavailable } = useFundingBalance({
    wallet,
    walletKey,
    currentWalletKey,
    payment,
    walletReady,
    onPaymentChain,
    submitting,
    submittingRef,
    checkingBalance,
    checkingBalanceRef,
    setCheckingBalance,
    setChainFailure,
    notify: setNotification,
    enabled: view === "payment" && !chainFailure,
  });
  const selectPayment = useFundingSelection({
    wallet,
    identity,
    viewRevision,
    currentWalletKey,
    config: config.data,
    payment,
    walletReady,
    checkingBalanceRef,
    submittingRef,
    setCheckingBalance,
    setChainFailure,
    setNotification,
    setPaymentNetwork,
    setPaymentOptionID,
    setAmount,
    setError,
    setNotice,
  });
  const atomicAmount = payment ? topupAmount(amount, payment) : undefined;
  const balanceBlocked = fundingBalanceBlocked(balance, atomicAmount, crossChain);
  const blocked =
    submitting || checkingBalance || waiting || recovery.isPending || Boolean(recovery.error);

  const { panelState, setFocusAmount } = useTopupPanel(waiting, submitting, notice, error, view);

  const submit = async (event?: SubmitEvent<HTMLFormElement>) => {
    event?.preventDefault();
    if (blocked || submittingRef.current || checkingBalanceRef.current || balanceBlocked) return;
    setError(undefined);
    setNotice(undefined);
    setChainFailure(undefined);
    setNotification(undefined);
    if (
      !walletReady ||
      !wallet.active ||
      !wallet.signBillingTypedData ||
      !wallet.waitForBillingTransaction ||
      !auth.session.csrf_token ||
      !payment?.x402_topups_enabled
    ) {
      setError(t("billing.topup.walletRequired"));
      return;
    }
    const atomic = topupAmount(amount, payment);
    if (!atomic) return;
    const signal = lifetime();
    let executingIntentID: string | undefined;
    resetProgress();
    submittingRef.current = true;
    setSubmitting(true);
    try {
      const { payBillingTopup } = await import("@/billing/topup");
      const assertCurrent = () => {
        signal.throwIfAborted();
        if (currentWalletKey.current !== walletKey) throw new TopupBalanceError("unavailable");
      };
      assertCurrent();
      const paymentConfig = payment;
      const csrfToken = auth.session.csrf_token;
      const execute = async (selected: ActiveWallet) => {
        await checkTopupBalance(
          wallet.readContract,
          selected.account,
          paymentConfig.asset!,
          paymentConfig.network!.slice("eip155:".length),
          atomic,
        );
        assertCurrent();
        const intent = await createBillingTopupIntent(atomic, csrfToken, paymentConfig.id);
        assertCurrent();
        executingIntentID = intent.id;
        setApprovalNetwork(intent.network);
        return await payBillingTopup({
          config: paymentConfig,
          intent,
          onProgress: (next) => {
            if (signal.aborted || completed.current.has(intent.id)) return;
            setProgress((previous) => ({ ...previous, ...next }));
            if (next.stage === "approval") setApprovalRequired(true);
            if (next.stage === "submitting") setPendingID(intent.id);
          },
          method,
          csrfToken: csrfToken,
          wallet: boundTopupWallet(wallet, selected, assertCurrent),
        });
      };
      const receipt = wallet.withBillingChain
        ? await wallet.withBillingChain(paymentConfig.network!, execute, true)
        : await execute(wallet.active);
      signal.throwIfAborted();
      if (completed.current.has(receipt.intent.id)) return;
      completed.current.add(receipt.intent.id);
      setPendingID(undefined);
      setResultTransaction(topupTransaction(receipt.intent));
      setNotice(
        t("billing.topup.credited", {
          amount: displayBillingAmount(receipt.intent.credit_amount_atomic, { credit_decimals: 6 }),
        }),
      );
      setAmount("");
      await refreshBilling();
    } catch (cause) {
      if (signal.aborted || (executingIntentID && completed.current.has(executingIntentID))) return;
      setPendingID(undefined);
      if (
        cause instanceof Error &&
        cause.name === "TopupPendingError" &&
        "intentID" in cause &&
        typeof cause.intentID === "string"
      ) {
        setProgress((previous) => ({ ...previous, stage: "unknown" }));
        setPendingID(cause.intentID);
        await refreshBilling();
      } else if (cause instanceof BillingChainError) {
        if (currentWalletKey.current === walletKey) {
          setChainFailure({ key: walletKey, error: cause });
        }
      } else if (cause instanceof TopupBalanceError) {
        if (cause.reason === "unavailable") markBalanceUnavailable();
        else {
          setError(t("billing.guide.insufficient"));
          void balance.refetch();
        }
      } else if (cause instanceof Error && cause.name === "TopupAuthorizationError") {
        setError(t("billing.topup.notSubmitted"));
      } else {
        setError(t("billing.topup.failed"));
      }
    } finally {
      if (!signal.aborted) {
        submittingRef.current = false;
        setSubmitting(false);
      }
    }
  };

  return (
    <div className="personal-billing">
      <section className="panel billing-balance" aria-label={t("billing.workspace.balance")}>
        {account.isPending && <p role="status">{t("billing.workspace.loadingBalance")}</p>}
        {account.error && (
          <BillingError
            title={t("billing.workspace.balanceFailed")}
            retry={() => void account.refetch()}
          />
        )}
        {account.data && !account.error && (
          <BalanceSummary account={account.data} config={config.data} />
        )}
      </section>
      {notification && <Message type="error">{notification}</Message>}
      <BillingTabs view={view} setView={setView} />
      <div id="billing-view" role="tabpanel" aria-labelledby={`billing-tab-${view}`}>
        {view === "payment" && (
          <section className="panel billing-payment" aria-labelledby="billing-payment-title">
            <h2 id="billing-payment-title">{t("billing.workspace.paymentTitle")}</h2>
            {panelState !== "form" ? (
              <TopupStatus
                state={panelState}
                detail={notice ?? error ?? t(`billing.topup.progress.${stage}`)}
                transactionHash={transaction?.hash}
                transactionNetwork={transaction?.network}
                approvalNetwork={approvalNetwork}
                progress={{ ...progress, stage }}
                approvalRequired={approvalRequired}
                finishing={submitting}
                queryFailed={waiting && Boolean(pending.error)}
                retry={() => void pending.refetch()}
                continueTopup={() => {
                  setNotice(undefined);
                  setError(undefined);
                  setAmount("");
                  resetProgress();
                  setFocusAmount(true);
                }}
                showHistory={() => {
                  setHistoryRevision((value) => value + 1);
                  setView("history");
                  document.getElementById("billing-tab-history")?.focus();
                }}
              />
            ) : (
              <>
                {config.isPending && <p role="status">{t("billing.workspace.loadingPayment")}</p>}
                {config.error && (
                  <BillingError
                    title={t("billing.workspace.configFailed")}
                    retry={() => void config.refetch()}
                  />
                )}
                {config.data &&
                  !config.error &&
                  (config.data.x402_topups_enabled && payment ? (
                    <>
                      {recovery.error && (
                        <BillingError
                          title={t("billing.workspace.recoveryFailed")}
                          retry={() => void recovery.refetch()}
                        />
                      )}
                      <BillingSteps active={0} complete={false} failed={false} />
                      <PaymentSelectors
                        config={config.data}
                        network={paymentChain!.network}
                        optionID={paymentOption!.id}
                        networkError={networkError}
                        blocked={blocked}
                        onChange={selectPayment}
                      />
                      <TopupForm
                        config={payment}
                        amount={amount}
                        method={method}
                        balance={
                          wallet.active && (
                            <WalletBalance
                              ready={walletReady}
                              busy={checkingBalance || balance.isFetching}
                              disabled={submitting}
                              failed={balanceError || Boolean(networkError)}
                              value={balance.data}
                              amount={atomicAmount}
                              payment={payment}
                              refresh={refreshBalance}
                            />
                          )
                        }
                        payer={wallet.active?.account}
                        balanceBlocked={balanceBlocked}
                        onAmount={(value) => {
                          setAmount(value);
                          setNotice(undefined);
                          setError(undefined);
                        }}
                        onMethod={setMethod}
                        blocked={blocked}
                        busy={submitting || waiting}
                        walletReady={walletReady}
                        onSubmit={submit}
                      />
                    </>
                  ) : (
                    <FundingDisabled />
                  ))}
              </>
            )}
          </section>
        )}
        <PersonalHistory key={historyRevision} visible={view === "history"} config={config.data} />
        {view === "prices" &&
          (config.isPending ? (
            <p role="status">{t("billing.workspace.loadingPayment")}</p>
          ) : config.error ? (
            <BillingError
              title={t("billing.workspace.configFailed")}
              retry={() => void config.refetch()}
            />
          ) : (
            config.data && <BillingPrices config={config.data} />
          ))}
      </div>
    </div>
  );
}

function FundingDisabled() {
  const { t } = useTranslation();
  return (
    <p className="billing-muted" role="status">
      {t("billing.workspace.disabled")}
    </p>
  );
}

function boundTopupWallet(
  wallet: ReturnType<typeof useWallet>,
  selected: ActiveWallet,
  assertCurrent: () => void,
): import("@/billing/topup").TopupWallet {
  return {
    active: selected,
    readContract: (...args) => {
      assertCurrent();
      return wallet.readContract(...args);
    },
    sendTransaction: (...args) => {
      assertCurrent();
      return wallet.sendTransaction(...args);
    },
    signBillingTypedData: async (...args) => {
      assertCurrent();
      const signature = await wallet.signBillingTypedData!(...args);
      assertCurrent();
      return signature;
    },
    waitForBillingTransaction: (...args) => {
      assertCurrent();
      return wallet.waitForBillingTransaction!(...args);
    },
  };
}

function topupTransaction(intent: BillingTopupIntent) {
  return { hash: intent.transaction_hash ?? undefined, network: intent.network };
}

function useTopupProgress(
  pendingID: string | undefined,
  pending: BillingTopupIntent | undefined,
  recovery: BillingTopupIntent[] | undefined,
  recoverable: BillingTopupIntent | undefined,
  submitting: boolean,
) {
  const [progress, setProgress] = useState<TopupProgress>({ stage: "preparing" });
  const [approvalRequired, setApprovalRequired] = useState(false);
  const [resultTransaction, setResultTransaction] = useState<{ hash?: string; network: string }>();
  const [approvalNetwork, setApprovalNetwork] = useState<string>();
  const waiting = Boolean(pendingID || recoverable);
  const observedIntent = pendingID
    ? pending?.id === pendingID
      ? pending
      : recovery?.find((intent) => intent.id === pendingID)
    : recoverable;
  const stage =
    progress.stage === "unknown" && observedIntent?.submission_progress !== "confirming"
      ? "unknown"
      : (observedIntent?.submission_progress ??
        (waiting && !submitting ? "unknown" : progress.stage));
  const pendingTransaction = observedIntent?.transaction_hash
    ? { hash: observedIntent.transaction_hash, network: observedIntent.network }
    : undefined;
  useEffect(() => {
    if (observedIntent?.transaction_hash) {
      setResultTransaction({
        hash: observedIntent.transaction_hash,
        network: observedIntent.network,
      });
    }
    if (observedIntent?.submission_progress === "confirming") {
      setProgress((previous) => ({ ...previous, stage: "confirming" }));
    }
  }, [
    observedIntent?.transaction_hash,
    observedIntent?.network,
    observedIntent?.submission_progress,
  ]);
  return {
    progress,
    setProgress,
    approvalRequired,
    setApprovalRequired,
    transaction: pendingTransaction ?? resultTransaction,
    setResultTransaction,
    approvalNetwork,
    setApprovalNetwork,
    resetProgress: () => {
      setProgress({ stage: "preparing" });
      setApprovalRequired(false);
      setResultTransaction(undefined);
      setApprovalNetwork(undefined);
    },
    waiting,
    stage,
  };
}

type TopupPanelState = "form" | "processing" | "pending" | "success" | "failure";

function useTopupPanel(
  waiting: boolean,
  submitting: boolean,
  notice: string | undefined,
  error: string | undefined,
  view: string,
) {
  const [focusAmount, setFocusAmount] = useState(false);
  const panelState: TopupPanelState = notice
    ? "success"
    : error
      ? "failure"
      : waiting
        ? "pending"
        : submitting
          ? "processing"
          : "form";
  useEffect(() => {
    if (focusAmount && view === "payment" && panelState === "form") {
      document.getElementById("billing-topup-amount")?.focus();
      setFocusAmount(false);
    }
  }, [focusAmount, view, panelState]);

  return { panelState, setFocusAmount };
}

function TopupStatus({
  state,
  detail,
  transactionHash,
  transactionNetwork,
  approvalNetwork,
  progress,
  approvalRequired,
  finishing,
  queryFailed,
  retry,
  continueTopup,
  showHistory,
}: {
  state: Exclude<TopupPanelState, "form">;
  detail: string;
  transactionHash?: string;
  transactionNetwork?: string;
  approvalNetwork?: string;
  progress: TopupProgress;
  approvalRequired: boolean;
  finishing: boolean;
  queryFailed: boolean;
  retry: () => void;
  continueTopup: () => void;
  showHistory: () => void;
}) {
  const { t } = useTranslation();
  const ref = useRef<HTMLDivElement>(null);
  const busy = state === "processing" || state === "pending";
  useEffect(() => {
    ref.current?.focus();
  }, [state]);
  return (
    <div
      ref={ref}
      tabIndex={-1}
      className={`billing-topup-status ${state}`}
      role={state === "failure" ? "alert" : "status"}
    >
      <UIIcon
        icon={busy ? LoaderCircle : state === "success" ? CircleCheck : X}
        size={24}
        className={busy ? "query-loading-spinner" : undefined}
      />
      {!busy && <h3>{t(`billing.result.${state}`)}</h3>}
      <p>{detail}</p>
      <TopupProgressSteps
        progress={progress}
        approvalRequired={approvalRequired}
        state={state}
        transactionHash={transactionHash}
        transactionNetwork={transactionNetwork}
        approvalNetwork={approvalNetwork}
      />
      {queryFailed && <BillingError title={t("billing.workspace.pendingFailed")} retry={retry} />}
      {!busy && (
        <div className="billing-result-actions">
          <button
            type="button"
            className="button primary"
            onClick={continueTopup}
            disabled={finishing}
          >
            {t("billing.result.continue")}
          </button>
          <button type="button" className="button secondary" onClick={showHistory}>
            {t("billing.result.history")}
          </button>
        </div>
      )}
    </div>
  );
}

function BillingTabs({
  view,
  setView,
}: {
  view: "payment" | "history" | "prices";
  setView: (view: "payment" | "history" | "prices") => void;
}) {
  const { t } = useTranslation();
  return (
    <div
      className="billing-view-switch"
      role="tablist"
      aria-label={t("billing.workspace.sections")}
    >
      {(["payment", "history", "prices"] as const).map((value) => (
        <button
          key={value}
          type="button"
          role="tab"
          id={`billing-tab-${value}`}
          aria-selected={view === value}
          tabIndex={view === value ? 0 : -1}
          onKeyDown={(event) => {
            const tabs = ["payment", "history", "prices"] as const;
            const index = tabs.indexOf(value);
            const next =
              event.key === "ArrowRight"
                ? tabs[(index + 1) % 3]
                : event.key === "ArrowLeft"
                  ? tabs[(index + 2) % 3]
                  : event.key === "Home"
                    ? tabs[0]
                    : event.key === "End"
                      ? tabs[2]
                      : undefined;
            if (next) {
              event.preventDefault();
              setView(next);
              document.getElementById(`billing-tab-${next}`)?.focus();
            }
          }}
          aria-controls="billing-view"
          onClick={() => setView(value)}
        >
          {t(`billing.workspace.${value}`)}
        </button>
      ))}
    </div>
  );
}

function WalletBalance({
  ready,
  busy,
  disabled,
  failed,
  value,
  amount,
  payment,
  refresh,
}: {
  ready: boolean;
  busy: boolean;
  disabled: boolean;
  failed: boolean;
  value: bigint | undefined;
  amount: string | undefined;
  payment: BillingPaymentConfig;
  refresh: () => Promise<void>;
}) {
  const { t } = useTranslation();
  return (
    <div role="status" className="billing-wallet-balance">
      <span>{t("billing.guide.walletBalance")}</span>{" "}
      {!ready
        ? t("billing.topup.walletRequired")
        : busy
          ? t("billing.guide.loading")
          : failed
            ? t("billing.guide.balanceUnavailable")
            : value !== undefined
              ? displayBillingAmount(value.toString(), payment)
              : t("billing.guide.balanceUnavailable")}
      <button
        type="button"
        className="billing-balance-refresh"
        aria-label={t("billing.guide.refresh")}
        title={t("billing.guide.refresh")}
        disabled={!ready || busy || disabled}
        onClick={() => void refresh()}
      >
        <UIIcon icon={RefreshCw} size={14} className={busy ? "query-loading-spinner" : undefined} />
      </button>
      {ready && !busy && !failed && value !== undefined && amount && value < BigInt(amount) && (
        <p>
          {t("billing.guide.shortfall", {
            amount: displayBillingAmount((BigInt(amount) - value).toString(), payment),
          })}
        </p>
      )}
    </div>
  );
}

function BalanceSummary({ account, config }: { account: BillingAccount; config?: BillingConfig }) {
  const { t } = useTranslation();
  return (
    <>
      <dl className="billing-balance-values">
        {(
          [
            ["available", account.available_atomic],
            ["reserved", account.reserved_atomic],
            ["spent", account.total_debit_atomic],
          ] as const
        ).map(([label, value]) => (
          <div key={label}>
            <dt>{t(`billing.topup.${label}`)}</dt>
            <dd className="numeric">{displayBillingAmount(value, config)}</dd>
          </div>
        ))}
      </dl>
      <p className="billing-muted">{t("billing.workspace.creditUnit")}</p>
    </>
  );
}

function TopupForm({
  balance,
  payer,
  balanceBlocked,
  config,
  amount,
  method,
  onAmount,
  onMethod,
  blocked,
  busy,
  walletReady,
  onSubmit,
}: {
  balance: ReactNode;
  payer?: string;
  balanceBlocked: boolean;
  config: BillingPaymentConfig;
  amount: string;
  method: BillingTransferMethod;
  onAmount: (value: string) => void;
  onMethod: (value: BillingTransferMethod) => void;
  blocked: boolean;
  busy: boolean;
  walletReady: boolean;
  onSubmit: (event: SubmitEvent<HTMLFormElement>) => Promise<void>;
}) {
  const { t } = useTranslation();
  const validAmount = topupAmount(amount, config) !== undefined;
  return (
    <form
      className="billing-topup-form"
      onSubmit={(event) => {
        if (!validAmount || !config.asset_transfer_methods.includes(method)) {
          event.preventDefault();
          return;
        }
        void onSubmit(event);
      }}
    >
      <div className="field-control">
        <label htmlFor="billing-topup-amount">{t("billing.topup.amount")}</label>
        <input
          id="billing-topup-amount"
          inputMode="decimal"
          maxLength={amountInputLength(config.asset_decimals ?? 0)}
          required
          value={amount}
          disabled={blocked}
          aria-describedby="billing-amount-range"
          onChange={(event) => onAmount(event.target.value)}
        />
        {balance}
        <small>{t("billing.workspace.exchangeRate", { rate: tokenCreditRate(config) })}</small>
        <p aria-live="polite">
          {t("billing.workspace.expectedCredit", {
            amount: topupAmount(amount, config)
              ? formatTokenAmount(creditAmount(topupAmount(amount, config)!, config)!, 6)
              : "—",
          })}
        </p>
        <small id="billing-amount-range">
          {t("billing.workspace.range", {
            min: config.minimum_topup_amount_atomic
              ? displayBillingAmount(config.minimum_topup_amount_atomic, config)
              : "—",
            max: config.maximum_topup_amount_atomic
              ? displayBillingAmount(config.maximum_topup_amount_atomic, config)
              : "—",
          })}
        </small>
      </div>
      {config.asset_transfer_methods.length > 1 ? (
        <label className="field-control">
          <span>{t("billing.topup.method")}</span>
          <SelectControl
            value={method}
            disabled={blocked}
            onChange={(event) => onMethod(event.target.value as BillingTransferMethod)}
          >
            {config.asset_transfer_methods.map((value) => (
              <option key={value} value={value}>
                {t(`billing.workspace.methods.${value}`)}
              </option>
            ))}
          </SelectControl>
        </label>
      ) : (
        <p className="billing-method">
          {t("billing.topup.method")}{" "}
          <strong>
            {config.asset_transfer_methods[0]
              ? t(`billing.workspace.methods.${config.asset_transfer_methods[0]}`)
              : "—"}
          </strong>
        </p>
      )}
      <p className="billing-muted">{t("billing.guide.authorization")}</p>
      {method === "permit2" && <p className="billing-muted">{t("billing.guide.permit2")}</p>}
      {!payer ? (
        <BillingWalletConnect />
      ) : (
        !walletReady && (
          <p className="billing-muted" role="status">
            {t("billing.topup.walletRequired")}
          </p>
        )
      )}
      <button
        className="button primary"
        aria-busy={busy}
        disabled={
          blocked ||
          balanceBlocked ||
          !walletReady ||
          !validAmount ||
          !config.asset_transfer_methods.includes(method)
        }
        type="submit"
      >
        {busy && <UIIcon icon={LoaderCircle} className="query-loading-spinner" />}
        {t("billing.guide.confirm")}
      </button>
    </form>
  );
}

function PersonalHistory({ visible, config }: { visible: boolean; config?: BillingConfig }) {
  const { t, i18n } = useTranslation();
  const identity = usePrivateIdentity();
  const [kind, setKind] = useState<HistoryKind>("topups");
  const [topupCursors, setTopupCursors] = useState([""]);
  const [paymentCursors, setPaymentCursors] = useState([""]);
  const topups = useQuery({
    queryKey: ["private-account", identity, "billing", "topups", topupCursors.at(-1)],
    queryFn: () => listCurrentBillingTopupIntents(PAGE_SIZE, topupCursors.at(-1) || undefined),
    enabled: visible && kind === "topups",
    retry: false,
  });
  const payments = useQuery({
    queryKey: ["private-account", identity, "billing", "payments", paymentCursors.at(-1)],
    queryFn: () => listCurrentUserBillingPayments(PAGE_SIZE, paymentCursors.at(-1) || undefined),
    enabled: visible && kind === "payments",
    retry: false,
  });
  const query = kind === "topups" ? topups : payments;
  if (!visible) return null;
  return (
    <section
      className="panel billing-history-section personal-billing-history"
      aria-labelledby="personal-billing-title"
    >
      <div className="billing-history-toolbar">
        <h2 id="personal-billing-title">{t("billing.workspace.history")}</h2>
        <div className="billing-history-controls">
          <label className="field-control">
            <span className="sr-only">{t("billing.workspace.historyType")}</span>
            <SelectControl
              value={kind}
              onChange={(event) => setKind(event.target.value as HistoryKind)}
            >
              <option value="topups">{t("billing.topup.history")}</option>
              <option value="payments">{t("billing.personal.title")}</option>
            </SelectControl>
          </label>
          <button
            type="button"
            className="button secondary"
            disabled={query.isFetching}
            onClick={() => void query.refetch()}
          >
            {t("billing.actions.refresh")}
          </button>
        </div>
      </div>
      {query.isPending && <p role="status">{t("billing.personal.loading")}</p>}
      {query.error && (
        <BillingError title={t("billing.personal.loadFailed")} retry={() => void query.refetch()} />
      )}
      {query.data && !query.error && (
        <>
          {query.data.data.length === 0 ? (
            <p className="billing-empty" role="status">
              {t("billing.workspace.empty")}
            </p>
          ) : (
            <ul
              className="billing-records"
              aria-label={
                kind === "topups" ? t("billing.topup.history") : t("billing.personal.tableLabel")
              }
            >
              {query.data.data.map((record) => (
                <HistoryRecord
                  key={`${kind}:${record.id}`}
                  record={record}
                  config={matchingAssetConfig(record, config)}
                  locale={i18n.resolvedLanguage ?? "en"}
                />
              ))}
            </ul>
          )}
          <BillingPagination
            busy={query.isFetching}
            cursors={kind === "topups" ? topupCursors : paymentCursors}
            onChange={kind === "topups" ? setTopupCursors : setPaymentCursors}
            nextCursor={query.data.meta.next_cursor}
            label={t("billing.personal.pagination")}
          />
        </>
      )}
    </section>
  );
}

function HistoryRecord({
  record,
  locale,
  config,
}: {
  record: BillingTopupIntent | BillingPayment;
  locale: string;
  config?: BillingPaymentOption;
}) {
  const { t } = useTranslation();
  const state =
    record.state === "settling" && record.failure_code === "settlement_unknown"
      ? "settlementUnknown"
      : record.state;
  return (
    <li>
      <div className="billing-record-summary">
        <time dateTime={record.created_at}>{formatTimestamp(record.created_at, locale)}</time>
        <span className="billing-record-amount numeric">
          <span>{displayBillingAmount(record.amount_atomic, config)}</span>
          <small>
            {config?.asset_decimals !== undefined && t("billing.workspace.tokenAmount")}
          </small>
        </span>
        <span className={`billing-state ${state}`}>{t(`billing.states.${state}`)}</span>
      </div>
      <details className="billing-record-details">
        <summary>{t("billing.workspace.details")}</summary>
        <dl>
          {"credit_amount_atomic" in record && (
            <div>
              <dt>{t("billing.workspace.creditUnit")}</dt>
              <dd>{formatTokenAmount(record.credit_amount_atomic, 6)}</dd>
            </div>
          )}
          <div>
            <dt>{t("billing.fields.network")}</dt>
            <dd>{record.network}</dd>
          </div>
          <div>
            <dt>{t("billing.fields.asset")}</dt>
            <dd>
              <code>{record.asset}</code>
            </dd>
          </div>
          <div>
            <dt>{t("billing.fields.recipient")}</dt>
            <dd>
              <code>{record.recipient}</code>
            </dd>
          </div>
          <div>
            <dt>{t("billing.fields.payer")}</dt>
            <dd>
              <code>{record.payer ?? "—"}</code>
            </dd>
          </div>
          {"asset_transfer_method" in record && (
            <div>
              <dt>{t("billing.topup.method")}</dt>
              <dd>{t(`billing.workspace.methods.${record.asset_transfer_method}`)}</dd>
            </div>
          )}
          {"operation" in record && (
            <div>
              <dt>{t("billing.fields.operation")}</dt>
              <dd>{record.operation}</dd>
            </div>
          )}
          <div>
            <dt>{t("billing.fields.transactionHash")}</dt>
            <dd>
              <BillingTransactionHash
                hash={record.transaction_hash ?? undefined}
                network={record.network}
              />
            </dd>
          </div>
        </dl>
      </details>
    </li>
  );
}

function BillingError({ title, retry }: { title: string; retry: () => void }) {
  const { t } = useTranslation();
  return (
    <BillingLoadError title={title} detail={t("billing.workspace.retryHint")} onRetry={retry} />
  );
}

function PaymentSelectors({
  config,
  network,
  optionID,
  blocked,
  networkError,
  onChange,
}: {
  config: BillingConfig;
  network: string;
  optionID: string;
  blocked: boolean;
  networkError?: string;
  onChange: (network: string, optionID: string) => void;
}) {
  const { t } = useTranslation();
  const chain = config.payment_chains.find((value) => value.network === network);
  return (
    <div className="field-control">
      <label htmlFor="billing-payment-network">{t("billing.fields.network")}</label>
      <SelectControl
        id="billing-payment-network"
        aria-invalid={Boolean(networkError)}
        aria-describedby={networkError ? "billing-network-error" : undefined}
        disabled={blocked}
        value={network}
        onChange={(event) => onChange(event.target.value, "")}
      >
        {config.payment_chains.map((chain) => (
          <option key={chain.network} value={chain.network}>
            {chain.name} ({chain.network})
          </option>
        ))}
      </SelectControl>
      {networkError && (
        <p className="billing-network-error" id="billing-network-error">
          {networkError}
        </p>
      )}
      <label htmlFor="billing-payment-token">{t("billing.fields.asset")}</label>
      <SelectControl
        id="billing-payment-token"
        disabled={blocked}
        value={optionID}
        onChange={(event) => onChange(network, event.target.value)}
      >
        {chain?.tokens.map((token) => (
          <option key={token.id} value={token.id}>
            {token.name} ({token.asset})
          </option>
        ))}
      </SelectControl>
    </div>
  );
}

function usePaymentSelection(config: BillingConfig | undefined) {
  const [paymentNetwork, setPaymentNetwork] = useState("");
  const [paymentOptionID, setPaymentOptionID] = useState("");
  const paymentChain =
    config?.payment_chains.find((chain) => chain.network === paymentNetwork) ??
    config?.payment_chains[0];
  const paymentOption =
    paymentChain?.tokens.find((token) => token.id === paymentOptionID) ?? paymentChain?.tokens[0];
  const payment: BillingPaymentConfig | undefined =
    config && paymentOption ? { ...config, ...paymentOption } : undefined;
  const [method, setMethod] = useState<BillingTransferMethod>("eip3009");
  useEffect(() => {
    const methods = payment?.asset_transfer_methods;
    if (methods?.length && !methods.includes(method)) setMethod(methods[0]!);
  }, [payment?.asset_transfer_methods, method]);
  return {
    method,
    setMethod,
    paymentChain,
    paymentOption,
    payment,
    setPaymentNetwork,
    setPaymentOptionID,
  };
}

function walletFundingState(
  wallet: ReturnType<typeof useWallet>,
  address: string | undefined,
  payment: BillingPaymentConfig | undefined,
) {
  const onPaymentChain = Boolean(
    wallet.active &&
    payment?.network &&
    chainsMatch(wallet.active.chainID, payment.network.slice("eip155:".length)),
  );
  const crossChain = Boolean(wallet.withBillingChain && !onPaymentChain);
  const walletReady = Boolean(
    wallet.active &&
    address &&
    wallet.active.account.toLowerCase() === address.toLowerCase() &&
    payment?.network &&
    (onPaymentChain || wallet.withBillingChain) &&
    wallet.signBillingTypedData &&
    wallet.waitForBillingTransaction,
  );
  return { onPaymentChain, crossChain, walletReady };
}
