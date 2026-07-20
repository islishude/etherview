import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { ApiError } from "@/api/client";
import { usePendingTransactions, usePublicConfig } from "@/api/hooks";
import type { PendingMeta, PendingTransaction } from "@/api/types";
import { formatInteger, formatTimestamp, shorten } from "@/components/format";
import { QueryNotice } from "@/components/QueryNotice";
import { Page } from "@/pages/pages";

const PAGE_SIZE = 25;

export function PendingPage() {
  const { i18n, t } = useTranslation();
  const [cursorHistory, setCursorHistory] = useState<string[]>([""]);
  const publicConfig = usePublicConfig();
  const mempoolEnabled = publicConfig.data?.features.mempool !== false;
  const canLoadPending = publicConfig.isSuccess && mempoolEnabled;
  const cursor = cursorHistory.at(-1) || undefined;
  const pending = usePendingTransactions(cursor, canLoadPending, PAGE_SIZE);
  const locale = i18n.resolvedLanguage ?? "en";
  const unavailable = pending.error instanceof ApiError &&
    pending.error.status === 503 &&
    pending.error.code === "mempool_unavailable"
      ? pendingUnavailableDetails(pending.error.details)
      : undefined;
  const unexpectedError = unavailable ? undefined : pending.error;

  const goToNextPage = () => {
    const nextCursor = pending.data?.meta.next_cursor;
    if (!nextCursor) return;
    setCursorHistory((history) => [...history, nextCursor]);
  };

  const goToPreviousPage = () => {
    setCursorHistory((history) => (history.length > 1 ? history.slice(0, -1) : history));
  };

  return (
    <Page title={t("page.pending")} description={t("page.pendingDescription")}>
      <QueryNotice
        loading={publicConfig.isPending || (canLoadPending && pending.isPending)}
        error={publicConfig.error ?? unexpectedError}
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

      {pending.data && (
        <>
          <PendingSnapshotSummary meta={pending.data.meta} locale={locale} />

          {pending.data.items.length === 0 ? (
            <p className="empty-result pending-empty" role="status">
              {t("pending.empty")}
            </p>
          ) : (
            <PendingTable transactions={pending.data.items} locale={locale} />
          )}

          <nav className="pending-pagination" aria-label={t("pending.pagination")}>
            <button
              className="button secondary"
              disabled={cursorHistory.length === 1}
              onClick={goToPreviousPage}
              type="button"
            >
              {t("pending.previousPage")}
            </button>
            <span aria-live="polite">
              {t("pending.pageNumber", { page: formatInteger(cursorHistory.length, locale) })}
            </span>
            <button
              className="button secondary"
              disabled={!pending.data.meta.next_cursor}
              onClick={goToNextPage}
              type="button"
            >
              {t("pending.nextPage")}
            </button>
          </nav>
        </>
      )}
    </Page>
  );
}

function PendingSnapshotSummary({ meta, locale }: { meta: PendingMeta; locale: string }) {
  const { t } = useTranslation();
  return (
    <section className="panel pending-snapshot" aria-labelledby="pending-snapshot-title">
      <div className="panel-heading pending-snapshot-heading">
        <h2 id="pending-snapshot-title">{t("pending.snapshot")}</h2>
        <span className={`availability ${meta.capability === "complete" ? "yes" : "no"}`}>
          {meta.capability}
        </span>
      </div>
      <dl className="pending-snapshot-grid">
        <div>
          <dt>{t("pending.snapshotTime")}</dt>
          <dd><time dateTime={meta.snapshot_at}>{formatTimestamp(meta.snapshot_at, locale)}</time></dd>
        </div>
        <div>
          <dt>{t("pending.expiresAt")}</dt>
          <dd><time dateTime={meta.expires_at}>{formatTimestamp(meta.expires_at, locale)}</time></dd>
        </div>
        <div>
          <dt>{t("pending.endpoint")}</dt>
          <dd><code>{meta.endpoint}</code></dd>
        </div>
        <div>
          <dt>{t("pending.total")}</dt>
          <dd>{formatInteger(meta.transaction_count, locale)}</dd>
        </div>
      </dl>
    </section>
  );
}

function PendingTable({
  transactions,
  locale,
}: {
  transactions: PendingTransaction[];
  locale: string;
}) {
  const { t } = useTranslation();
  return (
    <div className="table-scroll pending-table" tabIndex={0} aria-label={t("pending.tableLabel")}>
      <table>
        <caption className="sr-only">{t("pending.tableDescription")}</caption>
        <thead>
          <tr>
            <th>{t("table.hash")}</th>
            <th>{t("table.from")}</th>
            <th>{t("table.to")}</th>
            <th>{t("detail.nonce")}</th>
            <th>{t("table.value")}</th>
            <th>{t("pending.fees")}</th>
            <th>{t("pending.firstSeen")}</th>
            <th>{t("pending.lastSeen")}</th>
          </tr>
        </thead>
        <tbody>
          {transactions.map((transaction) => (
            <tr key={transaction.hash}>
              <td><code title={transaction.hash}>{shorten(transaction.hash)}</code></td>
              <td>
                <Link to="/address/$address" params={{ address: transaction.from }}>
                  <code title={transaction.from}>{shorten(transaction.from)}</code>
                </Link>
              </td>
              <td>
                {transaction.to ? (
                  <Link to="/address/$address" params={{ address: transaction.to }}>
                    <code title={transaction.to}>{shorten(transaction.to)}</code>
                  </Link>
                ) : (
                  t("common.contractCreation")
                )}
              </td>
              <td><code>{formatInteger(transaction.nonce, locale)}</code></td>
              <td><code>{formatInteger(transaction.value, locale)}</code></td>
              <td><PendingFees transaction={transaction} locale={locale} /></td>
              <td><time dateTime={transaction.first_seen_at}>{formatTimestamp(transaction.first_seen_at, locale)}</time></td>
              <td><time dateTime={transaction.last_seen_at}>{formatTimestamp(transaction.last_seen_at, locale)}</time></td>
            </tr>
          ))}
        </tbody>
      </table>
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
          <dd><code>{formatInteger(value, locale)}</code></dd>
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
      <span className="capability-mark" aria-hidden="true">!</span>
      <div>
        <h2>{title}</h2>
        <p>{detail}</p>
        <dl className="pending-unavailable-details">
          <div><dt>{t("pending.state")}</dt><dd><code>{state}</code></dd></div>
          <div><dt>{t("pending.reason")}</dt><dd><code>{reason}</code></dd></div>
          {lastAttemptAt && (
            <div>
              <dt>{t("pending.lastAttempt")}</dt>
              <dd><time dateTime={lastAttemptAt}>{formatTimestamp(lastAttemptAt, locale)}</time></dd>
            </div>
          )}
        </dl>
      </div>
    </section>
  );
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
