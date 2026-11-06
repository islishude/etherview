import { Badge } from "@/components/ui/badge";
import "@/styles/account.css";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import {
  Table,
  TableCaption,
  TableHeader,
  TableRow,
  TableHead,
  TableBody,
  TableCell,
} from "@/components/ui/table";
import { Pagination } from "@/components/ui/pagination";
import { SelectControl } from "@/components/SelectControl";
import { type SubmitEvent, useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import {
  getAdminBillingSummary,
  listAdminBillingPayments,
  type AdminBillingFilters,
  type BillingPayment,
  type BillingPaymentState,
} from "@/api/billing";
import { usePublicConfig } from "@/api/publicConfig";
import { useAuth } from "@/auth/AuthProvider";
import { formatTimestamp } from "@/components/format";
import { Page } from "./pages";

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
  "getTransactionFailure",
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
  "getNFTMetadata",
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
  const billingEnabled = publicConfig.data?.features.api_billing === true;
  const payments = useQuery({
    queryKey: ["admin-billing-payments", filters, cursor ?? null],
    queryFn: () => listAdminBillingPayments(ADMIN_PAGE_SIZE, cursor, filters),
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

  const applyFilters = (event: SubmitEvent<HTMLFormElement>) => {
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
    <Page title={t("billing.admin.title")} description={t("billing.admin.description")}>
      {!auth.enabled && !auth.loading ? (
        <BillingUnavailable />
      ) : !billingEnabled && !publicConfig.isPending ? (
        <BillingFeatureUnavailable />
      ) : auth.loading ? (
        <Alert asChild>
          <p className="query-notice" role="status">
            {t("auth.sessionState.checking")}
          </p>
        </Alert>
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
          <nav className="admin-surface-links" aria-label={t("billing.admin.adminNavigation")}>
            <Button asChild variant="outline">
              <Link className="button secondary" to="/admin/users">
                {t("auth.admin.openUsers")}
              </Link>
            </Button>
            <span aria-current="page">{t("billing.admin.openBilling")}</span>
          </nav>

          <BillingFilterPanel
            draft={draft}
            error={filterError}
            onChange={setDraft}
            onClear={clearFilters}
            onSubmit={applyFilters}
          />

          <Card asChild>
            <section
              className="panel billing-summary-section"
              aria-labelledby="billing-summary-title"
            >
              <div className="panel-heading billing-section-heading">
                <div>
                  <span className="eyebrow">{t("billing.admin.defaultWindow")}</span>
                  <h2 id="billing-summary-title">{t("billing.admin.summaryTitle")}</h2>
                  <p>{t("billing.admin.summaryDescription")}</p>
                </div>
                <Button
                  variant="outline"
                  className="button secondary"
                  disabled={summary.isFetching}
                  onClick={() => void summary.refetch()}
                  type="button"
                >
                  {t("billing.actions.refreshSummary")}
                </Button>
              </div>
              {summary.isPending && (
                <Alert asChild>
                  <p className="query-notice" role="status">
                    {t("billing.admin.summaryLoading")}
                  </p>
                </Alert>
              )}
              {summary.error && (
                <BillingLoadError
                  detail={t("billing.admin.summaryFailedDetail")}
                  onRetry={() => void summary.refetch()}
                  title={t("billing.admin.summaryFailed")}
                />
              )}
              {summary.data && <BillingSummaryView locale={locale} summary={summary.data} />}
            </section>
          </Card>

          <Card asChild>
            <section
              className="panel billing-ledger-section"
              aria-labelledby="billing-ledger-title"
            >
              <div className="panel-heading billing-section-heading">
                <div>
                  <h2 id="billing-ledger-title">{t("billing.admin.ledgerTitle")}</h2>
                  <p>{t("billing.admin.ledgerDescription")}</p>
                </div>
                <div className="billing-ledger-actions">
                  <span>{t("pagination.page", { page: cursors.length })}</span>
                  <Button
                    variant="outline"
                    className="button secondary"
                    disabled={payments.isFetching}
                    onClick={() => void payments.refetch()}
                    type="button"
                  >
                    {t("billing.actions.refreshLedger")}
                  </Button>
                </div>
              </div>
              {payments.isPending && (
                <Alert asChild>
                  <p className="query-notice" role="status">
                    {t("billing.admin.ledgerLoading")}
                  </p>
                </Alert>
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
          </Card>
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
  onSubmit: (event: SubmitEvent<HTMLFormElement>) => void;
}) {
  const { t } = useTranslation();
  return (
    <Card asChild>
      <form className="panel billing-filter-form" onSubmit={onSubmit}>
        <fieldset>
          <legend>{t("billing.filters.title")}</legend>
          <p id="billing-filter-hint">{t("billing.filters.description")}</p>
          <div className="billing-filter-grid">
            <Label className="field-control">
              <span>{t("billing.fields.state")}</span>
              <SelectControl
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
              </SelectControl>
            </Label>
            <Label className="field-control">
              <span>{t("billing.fields.operation")}</span>
              <SelectControl
                onChange={(event) => onChange({ ...draft, operation: event.target.value })}
                value={draft.operation}
              >
                <option value="">{t("billing.filters.anyOperation")}</option>
                {BILLABLE_OPERATIONS.map((operation) => (
                  <option key={operation} value={operation}>
                    {operation}
                  </option>
                ))}
              </SelectControl>
            </Label>
            <Label className="field-control">
              <span>{t("billing.fields.network")}</span>
              <Input
                autoCapitalize="none"
                maxLength={96}
                onChange={(event) => onChange({ ...draft, network: event.target.value })}
                placeholder="eip155:84532"
                spellCheck={false}
                value={draft.network}
              />
            </Label>
            <Label className="field-control">
              <span>{t("billing.fields.asset")}</span>
              <Input
                autoCapitalize="none"
                maxLength={42}
                onChange={(event) => onChange({ ...draft, asset: event.target.value })}
                placeholder="0x…"
                spellCheck={false}
                value={draft.asset}
              />
            </Label>
            <Label className="field-control">
              <span>{t("billing.filters.fromTime")}</span>
              <Input
                onChange={(event) => onChange({ ...draft, fromTime: event.target.value })}
                type="datetime-local"
                value={draft.fromTime}
              />
            </Label>
            <Label className="field-control">
              <span>{t("billing.filters.toTime")}</span>
              <Input
                onChange={(event) => onChange({ ...draft, toTime: event.target.value })}
                type="datetime-local"
                value={draft.toTime}
              />
            </Label>
          </div>
        </fieldset>
        {error && (
          <p className="form-error" role="alert">
            {error}
          </p>
        )}
        <div className="billing-filter-actions">
          <Button className="button primary" type="submit">
            {t("billing.filters.apply")}
          </Button>
          <Button variant="outline" className="button secondary" onClick={onClear} type="button">
            {t("billing.filters.clear")}
          </Button>
        </div>
      </form>
    </Card>
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
        <Card asChild>
          <div className="panel">
            <dt>{t("billing.fields.paymentCount")}</dt>
            <dd>{summary.payment_count}</dd>
          </div>
        </Card>
        <Card asChild>
          <div className="panel">
            <dt>{t("billing.fields.amountAtomic")}</dt>
            <dd>{summary.amount_atomic}</dd>
          </div>
        </Card>
        <Card asChild>
          <div className="panel">
            <dt>{t("billing.filters.fromTime")}</dt>
            <dd>
              <time dateTime={summary.from_time}>{formatTimestamp(summary.from_time, locale)}</time>
            </dd>
          </div>
        </Card>
        <Card asChild>
          <div className="panel">
            <dt>{t("billing.filters.toTime")}</dt>
            <dd>
              <time dateTime={summary.to_time}>{formatTimestamp(summary.to_time, locale)}</time>
            </dd>
          </div>
        </Card>
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
          <Table>
            <TableCaption className="sr-only">
              {t("billing.admin.summaryTableDescription")}
            </TableCaption>
            <TableHeader>
              <TableRow>
                <TableHead scope="col">{t("billing.fields.operation")}</TableHead>
                <TableHead scope="col">{t("billing.fields.state")}</TableHead>
                <TableHead scope="col">{t("billing.fields.network")}</TableHead>
                <TableHead scope="col">{t("billing.fields.asset")}</TableHead>
                <TableHead scope="col" className="numeric">
                  {t("billing.fields.paymentCount")}
                </TableHead>
                <TableHead scope="col" className="numeric">
                  {t("billing.fields.amountAtomic")}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {summary.rows.map((row) => (
                <TableRow key={[row.operation, row.state, row.network, row.asset].join(":")}>
                  <TableCell>
                    <code>{row.operation}</code>
                  </TableCell>
                  <TableCell>
                    <PaymentState state={row.state} />
                  </TableCell>
                  <TableCell>
                    <code>{row.network}</code>
                  </TableCell>
                  <TableCell>
                    <code className="billing-address">{row.asset}</code>
                  </TableCell>
                  <TableCell className="billing-quantity">{row.payment_count}</TableCell>
                  <TableCell className="billing-quantity">{row.amount_atomic}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
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
    <div className="table-scroll billing-table" tabIndex={0} aria-label={tableLabel}>
      <Table>
        <TableCaption className="sr-only">{tableDescription}</TableCaption>
        <TableHeader>
          <TableRow>
            <TableHead scope="col">{t("billing.fields.operation")}</TableHead>
            <TableHead scope="col">{t("billing.fields.state")}</TableHead>
            <TableHead scope="col" className="numeric">
              {t("billing.fields.amountAtomic")}
            </TableHead>
            <TableHead scope="col">{t("billing.fields.network")}</TableHead>
            <TableHead scope="col">{t("billing.fields.asset")}</TableHead>
            <TableHead scope="col">{t("billing.fields.payer")}</TableHead>
            <TableHead scope="col">{t("billing.fields.recipient")}</TableHead>
            {showAttribution && (
              <>
                <TableHead scope="col">{t("billing.fields.userID")}</TableHead>
                <TableHead scope="col">{t("billing.fields.apiKeyPrefix")}</TableHead>
              </>
            )}
            <TableHead scope="col">{t("billing.fields.transactionHash")}</TableHead>
            <TableHead scope="col">{t("billing.fields.createdAt")}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {payments.map((payment) => (
            <TableRow key={payment.id}>
              <TableCell>
                <code>{payment.operation}</code>
              </TableCell>
              <TableCell>
                <PaymentState failureCode={payment.failure_code} state={payment.state} />
              </TableCell>
              <TableCell className="billing-quantity">{payment.amount_atomic}</TableCell>
              <TableCell>
                <code>{payment.network}</code>
              </TableCell>
              <TableCell>
                <code className="billing-address">{payment.asset}</code>
              </TableCell>
              <TableCell>
                {payment.payer ? <code className="billing-address">{payment.payer}</code> : "—"}
              </TableCell>
              <TableCell>
                <code className="billing-address">{payment.recipient}</code>
              </TableCell>
              {showAttribution && (
                <>
                  <TableCell>{payment.user_id ? <code>{payment.user_id}</code> : "—"}</TableCell>
                  <TableCell>
                    {payment.api_key_prefix ? <code>{payment.api_key_prefix}</code> : "—"}
                  </TableCell>
                </>
              )}
              <TableCell>
                {payment.transaction_hash ? (
                  <code className="billing-hash">{payment.transaction_hash}</code>
                ) : (
                  "—"
                )}
              </TableCell>
              <TableCell>
                <time dateTime={payment.created_at}>
                  {formatTimestamp(payment.created_at, locale)}
                </time>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
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
  const settlementUnknown = state === "settling" && failureCode === "settlement_unknown";
  return (
    <Badge variant="status" className={`billing-state ${settlementUnknown ? "unknown" : state}`}>
      {settlementUnknown ? t("billing.states.settlementUnknown") : t(`billing.states.${state}`)}
    </Badge>
  );
}

export function BillingPagination({
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
    <Pagination className="cursor-pagination" aria-busy={busy} aria-label={label}>
      <Button
        variant="outline"
        className="button secondary"
        disabled={busy || cursors.length === 1}
        onClick={() => onChange((current) => (current.length > 1 ? current.slice(0, -1) : current))}
        type="button"
      >
        {t("pagination.previous")}
      </Button>
      <span aria-live="polite">{t("pagination.page", { page: cursors.length })}</span>
      <Button
        variant="outline"
        className="button secondary"
        disabled={busy || !nextCursor}
        onClick={() => {
          if (nextCursor) onChange((current) => [...current, nextCursor]);
        }}
        type="button"
      >
        {t("pagination.next")}
      </Button>
    </Pagination>
  );
}

export function BillingLoadError({
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
    <Alert asChild>
      <div className="query-notice degraded" role="alert">
        <span>
          <strong>{title}</strong>
          <small>{detail}</small>
        </span>
        <Button variant="outline" className="button secondary" onClick={onRetry} type="button">
          {t("actions.retry")}
        </Button>
      </div>
    </Alert>
  );
}

function BillingAuthGate({ detail, title }: { detail: string; title: string }) {
  const { t } = useTranslation();
  return (
    <Card asChild>
      <section className="panel auth-gate" aria-labelledby="billing-auth-title">
        <h2 id="billing-auth-title">{title}</h2>
        <p>{detail}</p>
        <Button asChild variant="default">
          <Link className="button primary inline-button" to="/account">
            {t("auth.account.open")}
          </Link>
        </Button>
      </section>
    </Card>
  );
}

function BillingUnavailable() {
  const { t } = useTranslation();
  return (
    <Card asChild>
      <section className="panel auth-gate" aria-labelledby="billing-unavailable-title">
        <h2 id="billing-unavailable-title">{t("auth.unavailable.title")}</h2>
        <p>{t("billing.admin.authUnavailable")}</p>
      </section>
    </Card>
  );
}

function BillingFeatureUnavailable() {
  const { t } = useTranslation();
  return (
    <Card asChild>
      <section className="panel auth-gate" aria-labelledby="billing-feature-unavailable-title">
        <h2 id="billing-feature-unavailable-title">{t("billing.unavailable.title")}</h2>
        <p>{t("billing.unavailable.description")}</p>
      </section>
    </Card>
  );
}

function parseBillingFilters(
  draft: BillingFilterForm,
): { valid: true; filters: AdminBillingFilters } | { valid: false; errorKey: string } {
  const network = draft.network.trim();
  const asset = draft.asset.trim();
  if (network && (network.length > 96 || !NETWORK_PATTERN.test(network))) {
    return { valid: false, errorKey: "billing.filters.invalidNetwork" };
  }
  if (asset && !ADDRESS_PATTERN.test(asset)) {
    return { valid: false, errorKey: "billing.filters.invalidAsset" };
  }
  if (draft.operation && !BILLABLE_OPERATIONS.some((operation) => operation === draft.operation)) {
    return { valid: false, errorKey: "billing.filters.invalidOperation" };
  }
  if (draft.state && !PAYMENT_STATES.some((state) => state === draft.state)) {
    return { valid: false, errorKey: "billing.filters.invalidState" };
  }

  const fromTime = parseDateTime(draft.fromTime);
  const toTime = parseDateTime(draft.toTime);
  if ((draft.fromTime && !fromTime) || (draft.toTime && !toTime)) {
    return { valid: false, errorKey: "billing.filters.invalidTime" };
  }
  const toMilliseconds = toTime?.milliseconds ?? Date.now();
  const fromMilliseconds = fromTime?.milliseconds ?? toMilliseconds - 24 * 60 * 60 * 1_000;
  if (fromMilliseconds >= toMilliseconds) {
    return { valid: false, errorKey: "billing.filters.invalidOrder" };
  }
  if (toMilliseconds - fromMilliseconds > MAX_SUMMARY_RANGE_MILLISECONDS) {
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

function parseDateTime(value: string): { iso: string; milliseconds: number } | undefined {
  if (!value) return undefined;
  const milliseconds = new Date(value).getTime();
  if (!Number.isFinite(milliseconds)) return undefined;
  return { iso: new Date(milliseconds).toISOString(), milliseconds };
}
