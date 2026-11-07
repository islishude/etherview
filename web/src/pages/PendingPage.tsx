import { Pagination } from "@/components/ui/pagination";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableCaption,
  TableHeader,
  TableRow,
  TableHead,
  TableBody,
  TableCell,
} from "@/components/ui/table";
import { CircleAlert } from "lucide-react";
import { UIIcon } from "@/components/UIIcon";
import { useCursorHistory } from "@/api/pagination";
import { useEffect, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { ApiError } from "@/api/client";
import { usePublicConfig } from "@/api/publicConfig";
import { usePendingTransactions } from "@/api/hooks";
import type { PendingMeta, PendingTransaction } from "@/api/types";
import { formatInteger, formatNativeAmount, formatTimestamp, shorten } from "@/components/format";
import { QueryNotice } from "@/components/QueryNotice";
import { TransactionStatus } from "@/components/TransactionStatus";
import { AddressIdentity } from "@/ens/AddressIdentity";
import { Page } from "@/pages/pages";

const PAGE_SIZE = 25;

export function PendingPage() {
  const { i18n, t } = useTranslation();
  const pager = useCursorHistory("pending-transactions");
  const { reset } = pager;
  const [expiredSnapshot, setExpiredSnapshot] = useState(false);
  const publicConfig = usePublicConfig();
  const mempoolEnabled = publicConfig.data?.features.mempool !== false;
  const canLoadPending = publicConfig.isSuccess && mempoolEnabled;
  const pending = usePendingTransactions(pager.request, canLoadPending, PAGE_SIZE);
  const locale = i18n.resolvedLanguage ?? "en";
  const nativeDecimals = publicConfig.data?.native_decimals ?? 18;
  const nativeSymbol = publicConfig.data?.native_symbol ?? "";
  const expiresAt = pending.data ? Date.parse(pending.data.meta.expires_at) : Number.NaN;
  const snapshotExpired =
    expiredSnapshot || (Number.isFinite(expiresAt) && expiresAt <= Date.now());
  const snapshot = snapshotExpired ? undefined : pending.data;
  const unavailable =
    pending.error instanceof ApiError &&
    pending.error.status === 503 &&
    pending.error.code === "mempool_unavailable"
      ? pendingUnavailableDetails(pending.error.details)
      : undefined;
  const unexpectedError = unavailable ? undefined : pending.error;

  useEffect(() => {
    if (!pending.data) return;
    const expiry = Date.parse(pending.data.meta.expires_at);
    if (!Number.isFinite(expiry)) return;
    if (expiry > Date.now()) setExpiredSnapshot(false);
    const maximumDelay = 2_147_483_647;
    const delay = Math.min(Math.max(0, expiry - Date.now()), maximumDelay);
    const timer = window.setTimeout(() => {
      setExpiredSnapshot(true);
    }, delay);
    return () => window.clearTimeout(timer);
  }, [pending.data]);

  const resetPagination = () => {
    setExpiredSnapshot(false);
    reset();
  };

  return (
    <Page title={t("page.pending")} description={t("page.pendingDescription")}>
      <QueryNotice
        loading={publicConfig.isPending || (canLoadPending && pending.isPending)}
        error={publicConfig.error ?? unexpectedError}
        onReset={resetPagination}
      />

      {publicConfig.isSuccess && !mempoolEnabled && (
        <PendingUnavailablePanel
          title={t("pending.disabled")}
          detail={t("pending.disabledDetail")}
          state="unavailable"
          reason="feature_disabled"
        />
      )}

      {unavailable && (
        <PendingUnavailablePanel
          title={t("pending.unavailable")}
          detail={t("pending.unavailableDetail")}
          state={unavailable.state}
          reason={unavailable.reason}
          lastAttemptAt={unavailable.lastAttemptAt}
        />
      )}

      {snapshotExpired && !unavailable && (
        <PendingUnavailablePanel
          title={t("pending.unavailable")}
          detail={t("pending.expiredDetail")}
          state="unavailable"
          reason="snapshot_expired"
        />
      )}

      {snapshot && (
        <>
          <PendingSnapshotSummary meta={snapshot.meta} locale={locale} />

          {snapshot.items.length === 0 ? (
            <p className="empty-result pending-empty" role="status">
              {t("pending.empty")}
            </p>
          ) : (
            <PendingTable
              transactions={snapshot.items}
              locale={locale}
              nativeDecimals={nativeDecimals}
              nativeSymbol={nativeSymbol}
            />
          )}

          <Pagination
            itemCount={snapshot.items.length}
            onReset={resetPagination}
            hasPrevious={pager.page > 1}
            className="pending-pagination"
            aria-label={t("pending.pagination")}
          >
            <Button
              variant="outline"
              className="button secondary"
              disabled={pager.invalid || !pager.hasPrevious}
              onClick={pager.previous}
              type="button"
            >
              {t("pending.previousPage")}
            </Button>
            <span aria-live="polite">
              {t("pending.pageNumber", { page: formatInteger(pager.page, locale) })}
            </span>
            <Button
              variant="outline"
              className="button secondary"
              disabled={pager.invalid || !snapshot.meta.next_cursor}
              onClick={() => pager.next(snapshot.meta.next_cursor)}
              type="button"
            >
              {t("pending.nextPage")}
            </Button>
          </Pagination>
        </>
      )}
    </Page>
  );
}

function PendingSnapshotSummary({ meta, locale }: { meta: PendingMeta; locale: string }) {
  const { t } = useTranslation();
  return (
    <Card asChild>
      <section className="panel pending-snapshot" aria-labelledby="pending-snapshot-title">
        <div className="panel-heading pending-snapshot-heading">
          <h2 id="pending-snapshot-title">{t("pending.snapshot")}</h2>
          <Badge
            variant="status"
            className={`availability ${meta.capability === "complete" ? "yes" : "no"}`}
          >
            {pendingStateLabel(meta.capability, t)}
          </Badge>
        </div>
        <dl className="pending-snapshot-grid">
          <div>
            <dt>{t("pending.snapshotTime")}</dt>
            <dd>
              <time dateTime={meta.snapshot_at}>{formatTimestamp(meta.snapshot_at, locale)}</time>
            </dd>
          </div>
          <div>
            <dt>{t("pending.expiresAt")}</dt>
            <dd>
              <time dateTime={meta.expires_at}>{formatTimestamp(meta.expires_at, locale)}</time>
            </dd>
          </div>
          <div>
            <dt>{t("pending.endpoint")}</dt>
            <dd>
              <code>{meta.endpoint}</code>
            </dd>
          </div>
          <div>
            <dt>{t("pending.total")}</dt>
            <dd>{formatInteger(meta.transaction_count, locale)}</dd>
          </div>
        </dl>
      </section>
    </Card>
  );
}

function PendingTable({
  transactions,
  locale,
  nativeDecimals,
  nativeSymbol,
}: {
  transactions: PendingTransaction[];
  locale: string;
  nativeDecimals: number;
  nativeSymbol: string;
}) {
  const { t } = useTranslation();
  return (
    <div className="table-scroll pending-table" tabIndex={0} aria-label={t("pending.tableLabel")}>
      <Table>
        <TableCaption className="sr-only">{t("pending.tableDescription")}</TableCaption>
        <TableHeader>
          <TableRow>
            <TableHead>{t("table.hash")}</TableHead>
            <TableHead>{t("table.status")}</TableHead>
            <TableHead>{t("table.from")}</TableHead>
            <TableHead>{t("table.to")}</TableHead>
            <TableHead className="numeric">{t("detail.nonce")}</TableHead>
            <TableHead className="numeric">{t("table.value", { symbol: nativeSymbol })}</TableHead>
            <TableHead>{t("pending.fees")}</TableHead>
            <TableHead>{t("pending.firstSeen")}</TableHead>
            <TableHead>{t("pending.lastSeen")}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {transactions.map((transaction) => (
            <TableRow key={transaction.hash}>
              <TableCell>
                <Link
                  to="/tx/$hash"
                  params={{ hash: transaction.hash }}
                  search={{ tab: "overview" }}
                >
                  <code title={transaction.hash}>{shorten(transaction.hash)}</code>
                </Link>
              </TableCell>
              <TableCell>
                <TransactionStatus label={t("transactionStatus.pending")} status="pending" />
              </TableCell>
              <TableCell>
                <AddressIdentity address={transaction.from} />
              </TableCell>
              <TableCell>
                {transaction.to ? (
                  <AddressIdentity address={transaction.to} />
                ) : (
                  t("common.contractCreation")
                )}
              </TableCell>
              <TableCell className="numeric">
                <code>{formatInteger(transaction.nonce, locale)}</code>
              </TableCell>
              <TableCell className="numeric">
                <code>{formatNativeAmount(transaction.value, locale, nativeDecimals)}</code>
              </TableCell>
              <TableCell>
                <PendingFees transaction={transaction} locale={locale} />
              </TableCell>
              <TableCell>
                <time dateTime={transaction.first_seen_at}>
                  {formatTimestamp(transaction.first_seen_at, locale)}
                </time>
              </TableCell>
              <TableCell>
                <time dateTime={transaction.last_seen_at}>
                  {formatTimestamp(transaction.last_seen_at, locale)}
                </time>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

function PendingFees({ transaction, locale }: { transaction: PendingTransaction; locale: string }) {
  const { t } = useTranslation();
  const fees = [
    [t("pending.gasPrice"), transaction.gas_price],
    [t("pending.maxFee"), transaction.max_fee_per_gas],
    [t("pending.priorityFee"), transaction.max_priority_fee_per_gas],
  ].filter((entry): entry is [string, string] => typeof entry[1] === "string");

  if (fees.length === 0) return <span aria-label={t("pending.noFeeData")}>—</span>;
  return (
    <dl className="pending-fees">
      {fees.map(([label, value]) => (
        <div key={label}>
          <dt>{label}</dt>
          <dd>
            <code>{formatInteger(value, locale)}</code>
          </dd>
        </div>
      ))}
    </dl>
  );
}

function PendingUnavailablePanel({
  title,
  detail,
  state,
  reason,
  lastAttemptAt,
}: {
  title: string;
  detail: string;
  state: string;
  reason: string;
  lastAttemptAt?: string;
}) {
  const { i18n, t } = useTranslation();
  const locale = i18n.resolvedLanguage ?? "en";
  return (
    <section className="capability-panel pending-unavailable" role="status">
      <span className="capability-mark" aria-hidden="true">
        <UIIcon icon={CircleAlert} size={24} />
      </span>
      <div>
        <h2>{title}</h2>
        <p>{detail}</p>
        <dl className="pending-unavailable-details">
          <div>
            <dt>{t("pending.state")}</dt>
            <dd>
              <code>{state}</code>
            </dd>
          </div>
          <div>
            <dt>{t("pending.reason")}</dt>
            <dd>
              <code>{reason}</code>
            </dd>
          </div>
          {lastAttemptAt && (
            <div>
              <dt>{t("pending.lastAttempt")}</dt>
              <dd>
                <time dateTime={lastAttemptAt}>{formatTimestamp(lastAttemptAt, locale)}</time>
              </dd>
            </div>
          )}
        </dl>
      </div>
    </section>
  );
}

type Translate = ReturnType<typeof useTranslation>["t"];

function pendingStateLabel(value: string, t: Translate): string {
  switch (value) {
    case "complete":
      return t("stageState.complete");
    case "pending":
      return t("stageState.pending");
    case "unavailable":
      return t("stageState.unavailable");
    case "failed":
      return t("stageState.failed");
    default:
      return value;
  }
}

function pendingUnavailableDetails(details: unknown): {
  state: string;
  reason: string;
  lastAttemptAt?: string;
} {
  if (typeof details !== "object" || details === null) {
    return { state: "unavailable", reason: "unknown" };
  }
  const record = details as Record<string, unknown>;
  return {
    state: typeof record.state === "string" ? record.state : "unavailable",
    reason: typeof record.reason === "string" ? record.reason : "unknown",
    ...(typeof record.last_attempt_at === "string"
      ? { lastAttemptAt: record.last_attempt_at }
      : {}),
  };
}
