import "@/i18n/resources/billingPrices";
import { Message } from "@/components/Message";
import "./PersonalBilling.css";
import { type SubmitEvent, useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import {
  createBillingTopupIntent,
  getBillingConfig,
  getBillingTopupIntent,
  getCurrentBillingAccount,
  listCurrentBillingTopupIntents,
  listCurrentUserBillingPayments,
  type BillingAccount,
  type BillingConfig,
  type BillingPayment,
  type BillingTopupIntent,
  type BillingTransferMethod,
} from "@/api/billing";
import { usePrivateIdentity, usePrivateLifetime } from "@/api/privateAccount";
import { useAuth } from "@/auth/AuthProvider";
import { formatTimestamp } from "@/components/format";
import { SelectControl } from "@/components/SelectControl";
import { useWallet } from "@/wallet/WalletProvider";
import { chainsMatch } from "@/wallet/eip6963";
import { BillingLoadError, BillingPagination } from "./BillingPages";

import {
  amountInputLength,
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
  const [view, setView] = useState<"payment" | "history">("payment");
  const [amount, setAmount] = useState("");
  const [method, setMethod] = useState<BillingTransferMethod>("eip3009");
  const [submitting, setSubmitting] = useState(false);
  const submittingRef = useRef(false);
  const [notice, setNotice] = useState<string>();
  const [error, setError] = useState<string>();
  const [pendingID, setPendingID] = useState<string>();
  const completed = useRef(new Set<string>());
  const prefix = ["private-account", identity, "billing"];
  const config = useQuery({
    queryKey: [...prefix, "config"],
    queryFn: getBillingConfig,
    retry: false,
  });
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
    queryFn: () => getBillingTopupIntent(pendingID!),
    // Seed only this intent so its known transaction survives a failed lookup
    // and recovery-list reordering. Still fetch its authoritative current state.
    initialData: () => recovery.data?.data.find((intent) => intent.id === pendingID),
    initialDataUpdatedAt: 0,
    enabled: Boolean(pendingID),
    retry: false,
    refetchInterval: pendingID ? 3_000 : false,
  });
  const refreshBilling = () => client.invalidateQueries({ queryKey: prefix });

  useEffect(() => {
    const methods = config.data?.asset_transfer_methods;
    if (methods?.length && !methods.includes(method)) setMethod(methods[0]!);
  }, [config.data?.asset_transfer_methods, method]);

  useEffect(() => {
    if (!pendingID && !submitting && recoverable) setPendingID(recoverable.id);
  }, [pendingID, recoverable, submitting]);

  useEffect(() => {
    const intent = pending.data;
    if (!pendingID || intent?.id !== pendingID) return;
    if (!["credited", "failed", "expired"].includes(intent.state)) return;
    completed.current.add(intent.id);
    setPendingID(undefined);
    if (intent.state === "credited") {
      setNotice(
        t("billing.topup.credited", {
          amount: displayBillingAmount(
            intent.amount_atomic,
            matchingAssetConfig(intent, config.data),
          ),
        }),
      );
      setAmount("");
    } else {
      setError(t("billing.topup.failed"));
    }
    void client.invalidateQueries({ queryKey: ["private-account", identity, "billing"] });
  }, [client, config.data, identity, pending.data, pendingID, t]);

  const walletReady = Boolean(
    wallet.active &&
    auth.session.user &&
    wallet.active.account.toLowerCase() === auth.session.user.address.toLowerCase() &&
    config.data?.network &&
    chainsMatch(wallet.active.chainID, config.data.network.slice("eip155:".length)) &&
    wallet.signBillingTypedData &&
    wallet.waitForBillingTransaction,
  );
  const waiting = Boolean(pendingID || recoverable);
  const pendingHash =
    pending.data?.transaction_hash ??
    recovery.data?.data.find((intent) => intent.id === (pendingID ?? recoverable?.id))
      ?.transaction_hash;
  const blocked = submitting || waiting || recovery.isPending || Boolean(recovery.error);

  const submit = async (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (blocked || submittingRef.current) return;
    setError(undefined);
    setNotice(undefined);
    if (
      !walletReady ||
      !wallet.active ||
      !wallet.signBillingTypedData ||
      !wallet.waitForBillingTransaction ||
      !auth.session.csrf_token ||
      !config.data?.x402_topups_enabled
    ) {
      setError(t("billing.topup.walletRequired"));
      return;
    }
    const atomic = topupAmount(amount, config.data);
    if (!atomic) return;
    const signal = lifetime();
    submittingRef.current = true;
    setSubmitting(true);
    try {
      const { payBillingTopup } = await import("@/billing/topup");
      signal.throwIfAborted();
      const intent = await createBillingTopupIntent(atomic, auth.session.csrf_token);
      signal.throwIfAborted();
      const receipt = await payBillingTopup({
        config: config.data,
        intent,
        method,
        csrfToken: auth.session.csrf_token,
        wallet: {
          active: wallet.active,
          readContract: (...args) => {
            signal.throwIfAborted();
            return wallet.readContract(...args);
          },
          sendTransaction: (...args) => {
            signal.throwIfAborted();
            return wallet.sendTransaction(...args);
          },
          signBillingTypedData: async (...args) => {
            signal.throwIfAborted();
            const signature = await wallet.signBillingTypedData!(...args);
            signal.throwIfAborted();
            return signature;
          },
          waitForBillingTransaction: (...args) => {
            signal.throwIfAborted();
            return wallet.waitForBillingTransaction!(...args);
          },
        },
      });
      signal.throwIfAborted();
      completed.current.add(receipt.intent.id);
      setNotice(
        t("billing.topup.credited", {
          amount: displayBillingAmount(
            receipt.intent.amount_atomic,
            matchingAssetConfig(receipt.intent, config.data),
          ),
        }),
      );
      setAmount("");
      await refreshBilling();
    } catch (cause) {
      if (signal.aborted) return;
      if (
        cause instanceof Error &&
        cause.name === "TopupPendingError" &&
        "intentID" in cause &&
        typeof cause.intentID === "string"
      ) {
        setPendingID(cause.intentID);
        await refreshBilling();
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
          <BalanceSummary
            account={account.data}
            config={matchingAssetConfig(account.data, config.data)}
          />
        )}
      </section>
      <div
        className="billing-view-switch"
        role="group"
        aria-label={t("billing.workspace.sections")}
      >
        {(["payment", "history"] as const).map((value) => (
          <button
            key={value}
            type="button"
            aria-pressed={view === value}
            aria-controls="billing-view"
            onClick={() => setView(value)}
          >
            {t(`billing.workspace.${value}`)}
          </button>
        ))}
      </div>
      {submitting && (
        <p className="query-notice" role="status">
          {t("billing.topup.processing")}
        </p>
      )}
      {waiting && (
        <div className="query-notice billing-pending" role="status">
          <p>{t("billing.topup.pending")}</p>
          {pendingHash && <code>{pendingHash}</code>}
        </div>
      )}
      {pending.error && (
        <BillingError
          title={t("billing.workspace.pendingFailed")}
          retry={() => void pending.refetch()}
        />
      )}
      {notice && <Message type="success">{notice}</Message>}
      {error && <Message type="error">{error}</Message>}
      <div id="billing-view">
        {view === "payment" && (
          <section className="panel billing-payment" aria-labelledby="billing-payment-title">
            <h2 id="billing-payment-title">{t("billing.workspace.paymentTitle")}</h2>
            {config.isPending && <p role="status">{t("billing.workspace.loadingPayment")}</p>}
            {config.error && (
              <BillingError
                title={t("billing.workspace.configFailed")}
                retry={() => void config.refetch()}
              />
            )}
            {config.data &&
              !config.error &&
              (config.data.x402_topups_enabled ? (
                <>
                  {recovery.error && (
                    <BillingError
                      title={t("billing.workspace.recoveryFailed")}
                      retry={() => void recovery.refetch()}
                    />
                  )}
                  <TopupForm
                    config={config.data}
                    amount={amount}
                    method={method}
                    onAmount={setAmount}
                    onMethod={setMethod}
                    blocked={blocked}
                    walletReady={walletReady}
                    onSubmit={submit}
                  />
                </>
              ) : (
                <p className="billing-muted" role="status">
                  {t("billing.workspace.disabled")}
                </p>
              ))}
          </section>
        )}
        <PersonalHistory visible={view === "history"} config={config.data} />
        {config.data && !config.error && <BillingPrices config={config.data} />}
      </div>
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
      <p className="billing-muted">
        {t("billing.workspace.tokenAmount")} · <code>{account.asset}</code> · {account.network}
      </p>
    </>
  );
}

function TopupForm({
  config,
  amount,
  method,
  onAmount,
  onMethod,
  blocked,
  walletReady,
  onSubmit,
}: {
  config: BillingConfig;
  amount: string;
  method: BillingTransferMethod;
  onAmount: (value: string) => void;
  onMethod: (value: BillingTransferMethod) => void;
  blocked: boolean;
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
      <dl className="billing-payment-destination">
        <div>
          <dt>{t("billing.fields.network")}</dt>
          <dd>{config.network ?? "—"}</dd>
        </div>
        <div>
          <dt>{t("billing.fields.asset")}</dt>
          <dd>
            <code>{config.asset ?? "—"}</code>
          </dd>
        </div>
        <div>
          <dt>{t("billing.fields.recipient")}</dt>
          <dd>
            <code>{config.recipient ?? "—"}</code>
          </dd>
        </div>
      </dl>
      <p className="billing-muted">{t("billing.workspace.authorization")}</p>
      {!walletReady && (
        <p className="billing-muted" role="status">
          {t("billing.topup.walletRequired")}
        </p>
      )}
      <button
        className="button primary"
        disabled={
          blocked || !walletReady || !validAmount || !config.asset_transfer_methods.includes(method)
        }
        type="submit"
      >
        {t("billing.topup.submit")}
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
  config?: BillingConfig;
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
              <code>{record.transaction_hash ?? "—"}</code>
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
