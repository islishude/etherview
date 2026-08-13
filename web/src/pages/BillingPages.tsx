import { type FormEvent, useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import {
  getAdminBillingSummary,
  listAdminBillingPayments,
  listCurrentUserBillingPayments,
  type AdminBillingFilters,
  type BillingPayment,
  type BillingPaymentState,
} from "@/api/billing";
import { usePublicConfig } from "@/api/hooks";
import { useAuth } from "@/auth/AuthProvider";
import { formatTimestamp } from "@/components/format";
import { Page } from "./pages";

const PERSONAL_PAGE_SIZE = 10;
const ADMIN_PAGE_SIZE = 25;
const MAX_SUMMARY_RANGE_MILLISECONDS = 31 * 24 * 60 * 60 * 1_000;
const NETWORK_PATTERN = /^eip155:[1-9][0-9]*$/u;
const ADDRESS_PATTERN = /^0x[0-9a-fA-F]{40}$/u;

const PAYMENT_STATES: BillingPaymentState[] = [
  "reserved",
  "verified",
  "settling",
  "settled",
  "failed",
  "expired",
];

const BILLABLE_OPERATIONS = [
  "listBlocks",
  "getBlock",
  "listTransactions",
  "getTransaction",
  "listPendingTransactions",
  "getTransactionCalldata",
  "getTransactionTrace",
  "listTransactionInternalTransactions",
  "listTransactionTokenTransfers",
  "listTransactionLogs",
  "listTransactionStateChanges",
  "getAddress",
  "listAddressTransactions",
  "listAddressWithdrawals",
  "listAddressInternalTransactions",
  "listAddressERC20Transfers",
  "listAddressNFTTransfers",
  "listAddressERC20Balances",
  "listAddressNFTBalances",
  "listTokens",
  "getToken",
  "listTokenTransfers",
  "getNFTOwner",
  "getBlockStats",
  "getAggregateStats",
  "getChartOverview",
  "getChartMetric",
  "search",
  "getVerifierJob",
] as const;

interface BillingFilterForm {
  asset: string;
  fromTime: string;
  network: string;
  operation: string;
  state: "" | BillingPaymentState;
  toTime: string;
}

const EMPTY_FILTER_FORM: BillingFilterForm = Object.freeze({
  asset: "",
  fromTime: "",
  network: "",
  operation: "",
  state: "",
  toTime: "",
});

export function PersonalBillingHistory() {
  const { i18n, t } = useTranslation();
  const auth = useAuth();
  const publicConfig = usePublicConfig();
  const [cursors, setCursors] = useState([""]);
  const cursor = cursors.at(-1) || undefined;
  const userID =
    auth.session.authenticated ? auth.session.user?.id : undefined;
  const locale = i18n.resolvedLanguage ?? "en";
  const billingEnabled =
    publicConfig.data?.features.x402_billing === true;
  const payments = useQuery({
    queryKey: ["current-user-billing-payments", userID ?? null, cursor ?? null],
    queryFn: () =>
      listCurrentUserBillingPayments(PERSONAL_PAGE_SIZE, cursor),
    enabled: Boolean(auth.enabled && billingEnabled && userID),
    retry: false,
    staleTime: 0,
  });

  useEffect(() => {
    setCursors([""]);
  }, [userID]);

  if (!billingEnabled || !userID) return null;

  return (
    <section
      className="panel billing-history-section"
      aria-labelledby="personal-billing-title"
    >
      <div className="panel-heading billing-section-heading">
        <div>
          <span className="eyebrow">{t("billing.personal.eyebrow")}</span>
          <h2 id="personal-billing-title">{t("billing.personal.title")}</h2>
          <p>{t("billing.personal.description")}</p>
          <p className="billing-boundary-note">
            {t("billing.personal.sessionBoundary")}
          </p>
        </div>
        <button
          className="button secondary"
          disabled={payments.isFetching}
          onClick={() => void payments.refetch()}
          type="button"
        >
          {t("billing.actions.refresh")}
        </button>
      </div>

      {payments.isPending && (
        <p className="query-notice" role="status">
          {t("billing.personal.loading")}
        </p>
      )}
      {payments.error && (
        <BillingLoadError
          detail={t("billing.personal.loadFailedDetail")}
          onRetry={() => void payments.refetch()}
          title={t("billing.personal.loadFailed")}
        />
      )}
      {payments.data?.data.length === 0 && (
        <p className="empty-result billing-empty" role="status">
          {t("billing.personal.empty")}
        </p>
      )}
      {payments.data && payments.data.data.length > 0 && (
        <PaymentLedgerTable
          locale={locale}
          payments={payments.data.data}
          showAttribution={false}
          tableDescription={t("billing.personal.tableDescription")}
          tableLabel={t("billing.personal.tableLabel")}
        />
      )}
      {payments.data && (
        <BillingPagination
          busy={payments.isFetching}
          cursors={cursors}
          label={t("billing.personal.pagination")}
          nextCursor={payments.data.meta.next_cursor}
          onChange={setCursors}
        />
      )}
    </section>
  );
}

export function AdminBillingPage() {
  const { i18n, t } = useTranslation();
  const auth = useAuth();
  const publicConfig = usePublicConfig();
  const [draft, setDraft] = useState<BillingFilterForm>(EMPTY_FILTER_FORM);
  const [filters, setFilters] = useState<AdminBillingFilters>({});
  const [filterError, setFilterError] = useState<string>();
  const [cursors, setCursors] = useState([""]);
  const cursor = cursors.at(-1) || undefined;
  const isAdmin =
    auth.session.authenticated &&
    auth.session.user?.status === "active" &&
    auth.session.user.role === "admin";
  const locale = i18n.resolvedLanguage ?? "en";
  const billingEnabled =
    publicConfig.data?.features.x402_billing === true;
  const payments = useQuery({
    queryKey: ["admin-billing-payments", filters, cursor ?? null],
    queryFn: () =>
      listAdminBillingPayments(ADMIN_PAGE_SIZE, cursor, filters),
    enabled: auth.enabled && billingEnabled && isAdmin,
    retry: false,
    staleTime: 0,
  });
  const summary = useQuery({
    queryKey: ["admin-billing-summary", filters],
    queryFn: () => getAdminBillingSummary(filters),
    enabled: auth.enabled && billingEnabled && isAdmin,
    retry: false,
    staleTime: 0,
  });

  useEffect(() => {
    if (!isAdmin) {
      setCursors([""]);
      setFilters({});
      setDraft(EMPTY_FILTER_FORM);
      setFilterError(undefined);
    }
  }, [isAdmin]);

  const applyFilters = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const parsed = parseBillingFilters(draft);
    if (!parsed.valid) {
      setFilterError(t(parsed.errorKey));
      return;
    }
    setFilterError(undefined);
    setCursors([""]);
    setFilters(parsed.filters);
  };

  const clearFilters = () => {
    setDraft(EMPTY_FILTER_FORM);
    setFilters({});
    setFilterError(undefined);
    setCursors([""]);
  };

  return (
    <Page
      title={t("billing.admin.title")}
      description={t("billing.admin.description")}
    >
      {!auth.enabled && !auth.loading ? (
        <BillingUnavailable />
      ) : !billingEnabled && !publicConfig.isPending ? (
        <BillingFeatureUnavailable />
      ) : auth.loading ? (
        <p className="query-notice" role="status">
          {t("auth.sessionState.checking")}
        </p>
      ) : !auth.session.authenticated ? (
        <BillingAuthGate
          detail={t("billing.admin.authenticationRequired")}
          title={t("auth.errors.authenticationRequired")}
        />
      ) : !isAdmin ? (
        <BillingAuthGate
          detail={t("billing.admin.adminRequired")}
          title={t("auth.errors.adminRequired")}
        />
      ) : (
        <>
          <nav
            className="admin-surface-links"
            aria-label={t("billing.admin.adminNavigation")}
          >
            <Link className="button secondary" to="/admin/users">
              {t("auth.admin.openUsers")}
            </Link>
            <span aria-current="page">{t("billing.admin.openBilling")}</span>
          </nav>

          <BillingFilterPanel
            draft={draft}
            error={filterError}
            onChange={setDraft}
            onClear={clearFilters}
            onSubmit={applyFilters}
          />

          <section
            className="panel billing-summary-section"
            aria-labelledby="billing-summary-title"
          >
            <div className="panel-heading billing-section-heading">
              <div>
                <span className="eyebrow">
                  {t("billing.admin.defaultWindow")}
                </span>
                <h2 id="billing-summary-title">
                  {t("billing.admin.summaryTitle")}
                </h2>
                <p>{t("billing.admin.summaryDescription")}</p>
              </div>
              <button
                className="button secondary"
                disabled={summary.isFetching}
                onClick={() => void summary.refetch()}
                type="button"
              >
                {t("billing.actions.refreshSummary")}
              </button>
            </div>
            {summary.isPending && (
              <p className="query-notice" role="status">
                {t("billing.admin.summaryLoading")}
              </p>
            )}
            {summary.error && (
              <BillingLoadError
                detail={t("billing.admin.summaryFailedDetail")}
                onRetry={() => void summary.refetch()}
                title={t("billing.admin.summaryFailed")}
              />
            )}
            {summary.data && (
              <BillingSummaryView locale={locale} summary={summary.data} />
            )}
          </section>

          <section
            className="panel billing-ledger-section"
            aria-labelledby="billing-ledger-title"
          >
            <div className="panel-heading billing-section-heading">
              <div>
                <h2 id="billing-ledger-title">
                  {t("billing.admin.ledgerTitle")}
                </h2>
                <p>{t("billing.admin.ledgerDescription")}</p>
              </div>
              <div className="billing-ledger-actions">
                <span>
                  {t("pagination.page", { page: cursors.length })}
                </span>
                <button
                  className="button secondary"
                  disabled={payments.isFetching}
                  onClick={() => void payments.refetch()}
                  type="button"
                >
                  {t("billing.actions.refreshLedger")}
                </button>
              </div>
            </div>
            {payments.isPending && (
              <p className="query-notice" role="status">
                {t("billing.admin.ledgerLoading")}
              </p>
            )}
            {payments.error && (
              <BillingLoadError
                detail={t("billing.admin.ledgerFailedDetail")}
                onRetry={() => void payments.refetch()}
                title={t("billing.admin.ledgerFailed")}
              />
            )}
            {payments.data?.data.length === 0 && (
              <p className="empty-result billing-empty" role="status">
                {t("billing.admin.ledgerEmpty")}
              </p>
            )}
            {payments.data && payments.data.data.length > 0 && (
              <PaymentLedgerTable
                locale={locale}
                payments={payments.data.data}
                showAttribution
                tableDescription={t("billing.admin.tableDescription")}
                tableLabel={t("billing.admin.tableLabel")}
              />
            )}
            {payments.data && (
              <BillingPagination
                busy={payments.isFetching}
                cursors={cursors}
                label={t("billing.admin.pagination")}
                nextCursor={payments.data.meta.next_cursor}
                onChange={setCursors}
              />
            )}
          </section>
        </>
      )}
    </Page>
  );
}

function BillingFilterPanel({
  draft,
  error,
  onChange,
  onClear,
  onSubmit,
}: {
  draft: BillingFilterForm;
  error?: string;
  onChange: (value: BillingFilterForm) => void;
  onClear: () => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
}) {
  const { t } = useTranslation();
  return (
    <form className="panel billing-filter-form" onSubmit={onSubmit}>
      <fieldset>
        <legend>{t("billing.filters.title")}</legend>
        <p id="billing-filter-hint">{t("billing.filters.description")}</p>
        <div className="billing-filter-grid">
          <label className="field-control">
            <span>{t("billing.fields.state")}</span>
            <select
              onChange={(event) =>
                onChange({
                  ...draft,
                  state: event.target.value as BillingFilterForm["state"],
                })
              }
              value={draft.state}
            >
              <option value="">{t("billing.filters.anyState")}</option>
              {PAYMENT_STATES.map((state) => (
                <option key={state} value={state}>
                  {t(`billing.states.${state}`)}
                </option>
              ))}
            </select>
          </label>
          <label className="field-control">
            <span>{t("billing.fields.operation")}</span>
            <select
              onChange={(event) =>
                onChange({ ...draft, operation: event.target.value })
              }
              value={draft.operation}
            >
              <option value="">{t("billing.filters.anyOperation")}</option>
              {BILLABLE_OPERATIONS.map((operation) => (
                <option key={operation} value={operation}>
                  {operation}
                </option>
              ))}
            </select>
          </label>
          <label className="field-control">
            <span>{t("billing.fields.network")}</span>
            <input
              autoCapitalize="none"
              maxLength={96}
              onChange={(event) =>
                onChange({ ...draft, network: event.target.value })
              }
              placeholder="eip155:84532"
              spellCheck={false}
              value={draft.network}
            />
          </label>
          <label className="field-control">
            <span>{t("billing.fields.asset")}</span>
            <input
              autoCapitalize="none"
              maxLength={42}
              onChange={(event) =>
                onChange({ ...draft, asset: event.target.value })
              }
              placeholder="0x…"
              spellCheck={false}
              value={draft.asset}
            />
          </label>
          <label className="field-control">
            <span>{t("billing.filters.fromTime")}</span>
            <input
              onChange={(event) =>
                onChange({ ...draft, fromTime: event.target.value })
              }
              type="datetime-local"
              value={draft.fromTime}
            />
          </label>
          <label className="field-control">
            <span>{t("billing.filters.toTime")}</span>
            <input
              onChange={(event) =>
                onChange({ ...draft, toTime: event.target.value })
              }
              type="datetime-local"
              value={draft.toTime}
            />
          </label>
        </div>
      </fieldset>
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
      <div className="billing-filter-actions">
        <button className="button primary" type="submit">
          {t("billing.filters.apply")}
        </button>
        <button className="button secondary" onClick={onClear} type="button">
          {t("billing.filters.clear")}
        </button>
      </div>
    </form>
  );
}

function BillingSummaryView({
  locale,
  summary,
}: {
  locale: string;
  summary: Awaited<ReturnType<typeof getAdminBillingSummary>>;
}) {
  const { t } = useTranslation();
  return (
    <>
      <dl className="billing-summary-grid">
        <div className="panel">
          <dt>{t("billing.fields.paymentCount")}</dt>
          <dd>{summary.payment_count}</dd>
        </div>
        <div className="panel">
          <dt>{t("billing.fields.amountAtomic")}</dt>
          <dd>{summary.amount_atomic}</dd>
        </div>
        <div className="panel">
          <dt>{t("billing.filters.fromTime")}</dt>
          <dd>
            <time dateTime={summary.from_time}>
              {formatTimestamp(summary.from_time, locale)}
            </time>
          </dd>
        </div>
        <div className="panel">
          <dt>{t("billing.filters.toTime")}</dt>
          <dd>
            <time dateTime={summary.to_time}>
              {formatTimestamp(summary.to_time, locale)}
            </time>
          </dd>
        </div>
      </dl>
      {summary.rows.length === 0 ? (
        <p className="empty-result billing-empty" role="status">
          {t("billing.admin.summaryEmpty")}
        </p>
      ) : (
        <div
          className="table-scroll billing-table"
          tabIndex={0}
          aria-label={t("billing.admin.summaryTableLabel")}
        >
          <table>
            <caption className="sr-only">
              {t("billing.admin.summaryTableDescription")}
            </caption>
            <thead>
              <tr>
                <th scope="col">{t("billing.fields.operation")}</th>
                <th scope="col">{t("billing.fields.state")}</th>
                <th scope="col">{t("billing.fields.network")}</th>
                <th scope="col">{t("billing.fields.asset")}</th>
                <th scope="col">{t("billing.fields.paymentCount")}</th>
                <th scope="col">{t("billing.fields.amountAtomic")}</th>
              </tr>
            </thead>
            <tbody>
              {summary.rows.map((row) => (
                <tr
                  key={[
                    row.operation,
                    row.state,
                    row.network,
                    row.asset,
                  ].join(":")}
                >
                  <td><code>{row.operation}</code></td>
                  <td><PaymentState state={row.state} /></td>
                  <td><code>{row.network}</code></td>
                  <td><code className="billing-address">{row.asset}</code></td>
                  <td className="billing-quantity">{row.payment_count}</td>
                  <td className="billing-quantity">{row.amount_atomic}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

function PaymentLedgerTable({
  locale,
  payments,
  showAttribution,
  tableDescription,
  tableLabel,
}: {
  locale: string;
  payments: BillingPayment[];
  showAttribution: boolean;
  tableDescription: string;
  tableLabel: string;
}) {
  const { t } = useTranslation();
  return (
    <div
      className="table-scroll billing-table"
      tabIndex={0}
      aria-label={tableLabel}
    >
      <table>
        <caption className="sr-only">{tableDescription}</caption>
        <thead>
          <tr>
            <th scope="col">{t("billing.fields.operation")}</th>
            <th scope="col">{t("billing.fields.state")}</th>
            <th scope="col">{t("billing.fields.amountAtomic")}</th>
            <th scope="col">{t("billing.fields.network")}</th>
            <th scope="col">{t("billing.fields.asset")}</th>
            <th scope="col">{t("billing.fields.payer")}</th>
            <th scope="col">{t("billing.fields.recipient")}</th>
            {showAttribution && (
              <>
                <th scope="col">{t("billing.fields.userID")}</th>
                <th scope="col">{t("billing.fields.apiKeyPrefix")}</th>
              </>
            )}
            <th scope="col">{t("billing.fields.transactionHash")}</th>
            <th scope="col">{t("billing.fields.createdAt")}</th>
          </tr>
        </thead>
        <tbody>
          {payments.map((payment) => (
            <tr key={payment.id}>
              <td><code>{payment.operation}</code></td>
              <td>
                <PaymentState
                  failureCode={payment.failure_code}
                  state={payment.state}
                />
              </td>
              <td className="billing-quantity">{payment.amount_atomic}</td>
              <td><code>{payment.network}</code></td>
              <td><code className="billing-address">{payment.asset}</code></td>
              <td>
                {payment.payer ? (
                  <code className="billing-address">{payment.payer}</code>
                ) : "—"}
              </td>
              <td>
                <code className="billing-address">{payment.recipient}</code>
              </td>
              {showAttribution && (
                <>
                  <td>
                    {payment.user_id ? (
                      <code>{payment.user_id}</code>
                    ) : "—"}
                  </td>
                  <td>
                    {payment.api_key_prefix ? (
                      <code>{payment.api_key_prefix}</code>
                    ) : "—"}
                  </td>
                </>
              )}
              <td>
                {payment.transaction_hash ? (
                  <code className="billing-hash">
                    {payment.transaction_hash}
                  </code>
                ) : "—"}
              </td>
              <td>
                <time dateTime={payment.created_at}>
                  {formatTimestamp(payment.created_at, locale)}
                </time>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function PaymentState({
  failureCode,
  state,
}: {
  failureCode?: string | null;
  state: BillingPaymentState;
}) {
  const { t } = useTranslation();
  const settlementUnknown =
    state === "settling" && failureCode === "settlement_unknown";
  return (
    <span
      className={`billing-state ${settlementUnknown ? "unknown" : state}`}
    >
      {settlementUnknown
        ? t("billing.states.settlementUnknown")
        : t(`billing.states.${state}`)}
    </span>
  );
}

function BillingPagination({
  busy,
  cursors,
  label,
  nextCursor,
  onChange,
}: {
  busy: boolean;
  cursors: string[];
  label: string;
  nextCursor?: string;
  onChange: (value: string[] | ((current: string[]) => string[])) => void;
}) {
  const { t } = useTranslation();
  return (
    <nav className="cursor-pagination" aria-busy={busy} aria-label={label}>
      <button
        className="button secondary"
        disabled={busy || cursors.length === 1}
        onClick={() =>
          onChange((current) =>
            current.length > 1 ? current.slice(0, -1) : current,
          )
        }
        type="button"
      >
        {t("pagination.previous")}
      </button>
      <span aria-live="polite">
        {t("pagination.page", { page: cursors.length })}
      </span>
      <button
        className="button secondary"
        disabled={busy || !nextCursor}
        onClick={() => {
          if (nextCursor) onChange((current) => [...current, nextCursor]);
        }}
        type="button"
      >
        {t("pagination.next")}
      </button>
    </nav>
  );
}

function BillingLoadError({
  detail,
  onRetry,
  title,
}: {
  detail: string;
  onRetry: () => void;
  title: string;
}) {
  const { t } = useTranslation();
  return (
    <div className="query-notice degraded" role="alert">
      <span>
        <strong>{title}</strong>
        <small>{detail}</small>
      </span>
      <button className="button secondary" onClick={onRetry} type="button">
        {t("actions.retry")}
      </button>
    </div>
  );
}

function BillingAuthGate({
  detail,
  title,
}: {
  detail: string;
  title: string;
}) {
  const { t } = useTranslation();
  return (
    <section className="panel auth-gate" aria-labelledby="billing-auth-title">
      <h2 id="billing-auth-title">{title}</h2>
      <p>{detail}</p>
      <Link className="button primary inline-button" to="/account">
        {t("auth.account.open")}
      </Link>
    </section>
  );
}

function BillingUnavailable() {
  const { t } = useTranslation();
  return (
    <section
      className="panel auth-gate"
      aria-labelledby="billing-unavailable-title"
    >
      <h2 id="billing-unavailable-title">
        {t("auth.unavailable.title")}
      </h2>
      <p>{t("billing.admin.authUnavailable")}</p>
    </section>
  );
}

function BillingFeatureUnavailable() {
  const { t } = useTranslation();
  return (
    <section
      className="panel auth-gate"
      aria-labelledby="billing-feature-unavailable-title"
    >
      <h2 id="billing-feature-unavailable-title">
        {t("billing.unavailable.title")}
      </h2>
      <p>{t("billing.unavailable.description")}</p>
    </section>
  );
}

function parseBillingFilters(
  draft: BillingFilterForm,
):
  | { valid: true; filters: AdminBillingFilters }
  | { valid: false; errorKey: string } {
  const network = draft.network.trim();
  const asset = draft.asset.trim();
  if (network && (network.length > 96 || !NETWORK_PATTERN.test(network))) {
    return { valid: false, errorKey: "billing.filters.invalidNetwork" };
  }
  if (asset && !ADDRESS_PATTERN.test(asset)) {
    return { valid: false, errorKey: "billing.filters.invalidAsset" };
  }
  if (
    draft.operation &&
    !BILLABLE_OPERATIONS.some((operation) => operation === draft.operation)
  ) {
    return { valid: false, errorKey: "billing.filters.invalidOperation" };
  }
  if (
    draft.state &&
    !PAYMENT_STATES.some((state) => state === draft.state)
  ) {
    return { valid: false, errorKey: "billing.filters.invalidState" };
  }

  const fromTime = parseDateTime(draft.fromTime);
  const toTime = parseDateTime(draft.toTime);
  if (
    (draft.fromTime && !fromTime) ||
    (draft.toTime && !toTime)
  ) {
    return { valid: false, errorKey: "billing.filters.invalidTime" };
  }
  const toMilliseconds = toTime?.milliseconds ?? Date.now();
  const fromMilliseconds =
    fromTime?.milliseconds ?? toMilliseconds - 24 * 60 * 60 * 1_000;
  if (fromMilliseconds >= toMilliseconds) {
    return { valid: false, errorKey: "billing.filters.invalidOrder" };
  }
  if (
    toMilliseconds - fromMilliseconds >
    MAX_SUMMARY_RANGE_MILLISECONDS
  ) {
    return { valid: false, errorKey: "billing.filters.rangeTooLong" };
  }

  return {
    valid: true,
    filters: {
      ...(asset ? { asset } : {}),
      ...(fromTime ? { from_time: fromTime.iso } : {}),
      ...(network ? { network } : {}),
      ...(draft.operation ? { operation: draft.operation } : {}),
      ...(draft.state ? { state: draft.state } : {}),
      ...(toTime ? { to_time: toTime.iso } : {}),
    },
  };
}

function parseDateTime(
  value: string,
): { iso: string; milliseconds: number } | undefined {
  if (!value) return undefined;
  const milliseconds = new Date(value).getTime();
  if (!Number.isFinite(milliseconds)) return undefined;
  return { iso: new Date(milliseconds).toISOString(), milliseconds };
}
