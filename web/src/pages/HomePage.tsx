import { useEffect, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { usePublicConfig } from "@/api/hooks";
import { useHomeSnapshot } from "@/api/homeStream";
import type { BlockSummary, TransactionSummary } from "@/api/types";
import { formatInteger, formatRelativeTimestamp, shorten } from "@/components/format";
import { PageHeading } from "@/components/DesignPrimitives";
import { QueryNotice } from "@/components/QueryNotice";
import { AddressIdentity } from "@/ens/AddressIdentity";
import { IncludedTransactionStatus } from "./TransactionPage";
import { ChainContextPanel, FinalityBadge } from "./pages";

export function HomePage() {
  const { i18n, t } = useTranslation();
  const snapshot = useHomeSnapshot();
  const config = usePublicConfig();
  const [relativeNow, setRelativeNow] = useState(() => Date.now());
  const locale = i18n.resolvedLanguage ?? "en";

  useEffect(() => {
    const intervalID = window.setInterval(() => setRelativeNow(Date.now()), 1_000);
    return () => window.clearInterval(intervalID);
  }, []);

  return (
    <div className="page-stack home-page">
      <PageHeading
        title={config.data?.chain_name ?? t("nav.home")}
        description={t("home.description")}
      />
      <QueryNotice
        loading={snapshot.isPending}
        error={snapshot.data ? undefined : snapshot.error}
      />

      <section className="metrics-grid" aria-label={t("home.metrics")}>
        <Metric
          label={t("home.indexed")}
          value={formatInteger(snapshot.data?.status.indexed_block, locale)}
        />
        <Metric
          label={t("home.networkHead")}
          value={formatInteger(snapshot.data?.status.latest_block, locale)}
        />
        <Metric
          label={t("home.finality")}
          value={formatInteger(snapshot.data?.status.finalized_block, locale)}
        />
        <Metric
          label={t("home.lag")}
          value={
            snapshot.data
              ? snapshot.data.status.core_ready && snapshot.data.status.lag === "0"
                ? t("home.caughtUp")
                : t("home.syncing")
              : "—"
          }
          accent={snapshot.data?.status.core_ready && snapshot.data.status.lag === "0"}
        />
      </section>

      {snapshot.data && <ChainContextPanel status={snapshot.data.status} />}

      <div className="activity-grid">
        <section className="panel activity-panel" aria-labelledby="recent-blocks-title">
          <PanelHeading id="recent-blocks-title" title={t("home.recentBlocks")} to="/blocks" />
          {snapshot.data?.blocks.length === 0 && (
            <p className="empty-result compact-empty">{t("state.noBlocks")}</p>
          )}
          {snapshot.data?.blocks.map((block) => (
            <BlockRow block={block} key={block.hash} locale={locale} now={relativeNow} />
          ))}
        </section>
        <section className="panel activity-panel" aria-labelledby="recent-transactions-title">
          <PanelHeading
            id="recent-transactions-title"
            title={t("home.recentTransactions")}
            to="/transactions"
          />
          {snapshot.data?.transactions.length === 0 && (
            <p className="empty-result compact-empty">{t("state.noTransactions")}</p>
          )}
          {snapshot.data?.transactions.map((transaction) => (
            <TransactionRow key={transaction.hash} transaction={transaction} />
          ))}
        </section>
      </div>
    </div>
  );
}

function Metric({ label, value, accent }: { label: string; value: string; accent?: boolean }) {
  return (
    <article className="metric-card">
      <span>{label}</span>
      <strong className={accent ? "positive" : undefined} title={value}>
        {value}
      </strong>
    </article>
  );
}

function PanelHeading({
  id,
  title,
  to,
}: {
  id: string;
  title: string;
  to: "/blocks" | "/transactions";
}) {
  return (
    <header className="panel-heading">
      <h2 id={id}>{title}</h2>
      <Link to={to} aria-label={title}>
        <span aria-hidden="true">→</span>
      </Link>
    </header>
  );
}

function BlockRow({ block, locale, now }: { block: BlockSummary; locale: string; now: number }) {
  const { t } = useTranslation();
  return (
    <div className="activity-row">
      <span className="block-cube" aria-hidden="true">
        B
      </span>
      <span className="activity-primary">
        <Link to="/blocks/$blockID" params={{ blockID: block.hash }}>
          #{formatInteger(block.number, locale)}
        </Link>
        <small>
          <time dateTime={block.timestamp}>
            {formatRelativeTimestamp(block.timestamp, locale, now)}
          </time>
        </small>
      </span>
      <span className="activity-meta">
        <strong>{formatInteger(block.transaction_count, locale)}</strong>
        <small>{t("common.transactionsShort")}</small>
      </span>
      <FinalityBadge finality={block.finality} />
    </div>
  );
}

function TransactionRow({ transaction }: { transaction: TransactionSummary }) {
  return (
    <div className="activity-row transaction-row">
      <span className="tx-mark" aria-hidden="true">
        ↗
      </span>
      <span className="activity-primary">
        <Link to="/tx/$hash" params={{ hash: transaction.hash }} search={{ tab: "overview" }}>
          {shorten(transaction.hash)}
        </Link>
        <small>
          <AddressIdentity address={transaction.from} /> →{" "}
          {transaction.to ? <AddressIdentity address={transaction.to} /> : "∅"}
        </small>
      </span>
      <IncludedTransactionStatus transaction={transaction} />
    </div>
  );
}
