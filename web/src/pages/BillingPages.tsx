import { AdminSurface, AdminPanel, useAdminIdentity } from "@/components/AdminSurface";
import { Sheet } from "@/components/ui/sheet";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { shorten } from "@/components/format";
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
import { BillingPagination, BillingLoadError } from "./BillingControls";
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
  return (
    <AdminSurface>
      <AdminBillingTabs />
    </AdminSurface>
  );
}
function AdminBillingTabs() {
  const { t } = useTranslation();
  const [tab, setTab] = useState("overview");
  return (
    <AdminPanel title={t("billing.admin.title")} description={t("adminUI.billingDescription")}>
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList aria-label={t("billing.admin.title")}>
          <TabsTrigger
            value="overview"
            id="billing-overview-tab"
            aria-controls="billing-overview-panel"
          >
            {t("adminUI.overview")}
          </TabsTrigger>
          <TabsTrigger
            value="payments"
            id="billing-payments-tab"
            aria-controls="billing-payments-panel"
          >
            {t("adminUI.payments")}
          </TabsTrigger>
        </TabsList>
        {(["overview", "payments"] as const).map((mode) => (
          <div
            key={mode}
            role="tabpanel"
            id={`billing-${mode}-panel`}
            aria-labelledby={`billing-${mode}-tab`}
            hidden={tab !== mode}
          >
            <AdminBillingContent mode={mode} active={tab === mode} />
          </div>
        ))}
      </Tabs>
    </AdminPanel>
  );
}
function AdminBillingContent({ mode, active }: { mode: "overview" | "payments"; active: boolean }) {
  const { i18n, t } = useTranslation();
  const auth = useAuth();
  const identity = useAdminIdentity();
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
    queryKey: ["private-account", identity, "admin", "billing-payments", filters, cursor ?? null],
    queryFn: ({ signal }) => listAdminBillingPayments(ADMIN_PAGE_SIZE, cursor, filters, signal),
    enabled: auth.enabled && billingEnabled && isAdmin && active && mode === "payments",
    retry: false,
    staleTime: 0,
  });
  const summary = useQuery({
    queryKey: ["private-account", identity, "admin", "billing-summary", filters],
    queryFn: ({ signal }) => getAdminBillingSummary(filters, signal),
    enabled: auth.enabled && billingEnabled && isAdmin && active && mode === "overview",
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
    <div className="grid min-w-0 gap-4 pt-4">
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
          {mode === "overview" ? (
            <details>
              <summary>{t("billing.filters.title")}</summary>
              <BillingFilterPanel
                draft={draft}
                error={filterError}
                onChange={setDraft}
                onClear={clearFilters}
                onSubmit={applyFilters}
              />
            </details>
          ) : (
            <BillingFilterPanel
              draft={draft}
              error={filterError}
              onChange={setDraft}
              onClear={clearFilters}
              onSubmit={applyFilters}
            />
          )}

          {Object.values(filters).some(Boolean) && (
            <p className="break-all text-sm text-muted-foreground">
              {t("adminUI.applied")}:{" "}
              {Object.entries(filters)
                .filter(([, value]) => value)
                .map(
                  ([key, value]) =>
                    `${t(key === "from_time" ? "billing.filters.fromTime" : key === "to_time" ? "billing.filters.toTime" : `billing.fields.${key}`)}: ${value}`,
                )
                .join(" · ")}
            </p>
          )}
          {mode === "overview" && (
            <Card asChild>
              <section
                className="panel billing-summary-section"
                aria-labelledby="billing-summary-title"
              >
                <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border pb-3">
                  <div>
                    <h2 id="billing-summary-title">{t("billing.admin.summaryTitle")}</h2>
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
          )}

          {mode === "payments" && (
            <Card asChild>
              <section
                className="panel billing-ledger-section"
                aria-labelledby="billing-ledger-title"
              >
                <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border pb-3">
                  <div>
                    <h2 id="billing-ledger-title">{t("billing.admin.ledgerTitle")}</h2>
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
                    tableDescription={t("billing.admin.tableDescription")}
                    tableLabel={t("billing.admin.tableLabel")}
                  />
                )}
                {payments.data && (
                  <BillingPagination
                    itemCount={payments.data.data.length}
                    busy={payments.isFetching}
                    cursors={cursors}
                    label={t("billing.admin.pagination")}
                    nextCursor={payments.data.meta.next_cursor}
                    onChange={setCursors}
                  />
                )}
              </section>
            </Card>
          )}
        </>
      )}
    </div>
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

          <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
            <Label className="field-control col-span-2 sm:col-span-1">
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
          <details className="mt-3">
            <summary>{t("adminUI.advanced")}</summary>
            <div className="billing-filter-grid pt-3">
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
            </div>
          </details>
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

export function BillingSummaryView({
  locale,
  summary,
}: {
  locale: string;
  summary: Awaited<ReturnType<typeof getAdminBillingSummary>>;
}) {
  const { t } = useTranslation();
  const totals = new Map<string, { network: string; asset: string; amount: bigint }>();
  for (const row of summary.rows) {
    const key = `${row.network}:${row.asset}`;
    const group = totals.get(key) ?? { network: row.network, asset: row.asset, amount: 0n };
    group.amount += BigInt(row.amount_atomic);
    totals.set(key, group);
  }
  return (
    <>
      <dl className="m-0 grid gap-4 py-4 sm:grid-cols-2">
        <div>
          <dt className="text-sm text-muted-foreground">{t("billing.fields.paymentCount")}</dt>
          <dd className="m-0 break-all text-lg font-semibold tabular-nums">
            {summary.payment_count}
          </dd>
        </div>
        <div>
          <dt className="text-sm text-muted-foreground">{t("adminUI.period")}</dt>
          <dd className="m-0 text-sm">
            <time dateTime={summary.from_time}>{formatTimestamp(summary.from_time, locale)}</time> –{" "}
            <time dateTime={summary.to_time}>{formatTimestamp(summary.to_time, locale)}</time>
          </dd>
        </div>
      </dl>
      <section aria-label={t("adminUI.assetTotals")}>
        <h3 className="text-base">{t("adminUI.assetTotals")}</h3>
        {[...totals.entries()].map(([key, group]) => (
          <div
            key={key}
            className="flex flex-wrap justify-between gap-2 border-b border-border py-2"
          >
            <span>
              {group.network} · <code title={group.asset}>{shorten(group.asset)}</code>
            </span>
            <span
              className="min-w-0 break-all text-right tabular-nums"
              title={t("billing.fields.amountAtomic")}
            >
              {group.amount.toString()}
            </span>
          </div>
        ))}
      </section>
      <details className="mt-4">
        <summary>{t("adminUI.breakdown")}</summary>
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
                  <TableHead scope="col" className="numeric text-right">
                    {t("billing.fields.paymentCount")}
                  </TableHead>
                  <TableHead scope="col" className="numeric text-right">
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
                    <TableCell className="billing-quantity text-right">
                      {row.payment_count}
                    </TableCell>
                    <TableCell className="billing-quantity text-right">
                      {row.amount_atomic}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </details>
    </>
  );
}

function PaymentLedgerTable({
  locale,
  payments,
  tableDescription,
  tableLabel,
}: {
  locale: string;
  payments: BillingPayment[];
  tableDescription: string;
  tableLabel: string;
}) {
  const { t } = useTranslation();
  const [selected, setSelected] = useState<BillingPayment>();
  return (
    <>
      <div className="table-scroll billing-table" tabIndex={0} aria-label={tableLabel}>
        <Table>
          <TableCaption className="sr-only">{tableDescription}</TableCaption>
          <TableHeader>
            <TableRow>
              <TableHead>{t("billing.fields.createdAt")}</TableHead>
              <TableHead>{t("billing.fields.operation")}</TableHead>
              <TableHead>{t("billing.fields.state")}</TableHead>
              <TableHead className="numeric text-right">
                {t("billing.fields.amountAtomic")}
              </TableHead>
              <TableHead>{t("adminUI.details")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {payments.map((payment) => (
              <TableRow key={payment.id}>
                <TableCell>
                  <time dateTime={payment.created_at}>
                    {formatTimestamp(payment.created_at, locale)}
                  </time>
                </TableCell>
                <TableCell>
                  <code>{payment.operation}</code>
                </TableCell>
                <TableCell>
                  <PaymentState state={payment.state} failureCode={payment.failure_code} />
                </TableCell>
                <TableCell className="numeric text-right">
                  <div>{payment.amount_atomic}</div>
                  <small title={payment.asset}>{shorten(payment.asset)}</small>
                </TableCell>
                <TableCell>
                  <Button size="sm" variant="ghost" onClick={() => setSelected(payment)}>
                    {t("adminUI.details")}
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      {selected && (
        <Sheet
          closeLabel={t("adminUI.close")}
          title={t("adminUI.paymentDetails")}
          onClose={() => setSelected(undefined)}
        >
          <dl className="grid gap-4">
            <div>
              <dt>{t("adminUI.paymentID")}</dt>
              <dd className="m-0 break-all">
                <code>{selected.id}</code>
              </dd>
            </div>
            {(
              [
                ["operation", selected.operation],
                ["state", t(`billing.states.${selected.state}`)],
                ["amountAtomic", selected.amount_atomic],
                ["network", selected.network],
                ["asset", selected.asset],
                ["payer", selected.payer],
                ["recipient", selected.recipient],
                ["userID", selected.user_id],
                ["apiKeyPrefix", selected.api_key_prefix],
                ["transactionHash", selected.transaction_hash],
                ["createdAt", formatTimestamp(selected.created_at, locale)],
              ] as const
            ).map(([field, value]) => (
              <div key={field}>
                <dt className="text-sm text-muted-foreground">{t(`billing.fields.${field}`)}</dt>
                <dd className="m-0 break-all">
                  {field === "state" ? (
                    <PaymentState state={selected.state} failureCode={selected.failure_code} />
                  ) : (
                    <code>{value ?? "—"}</code>
                  )}
                </dd>
              </div>
            ))}
          </dl>
        </Sheet>
      )}
    </>
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
