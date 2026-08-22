import {
  useEffect,
  useState,
} from "react";
import { Link, } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import {
  useBlocks,
  useChainStatus,
  useGenesisAccounts,
  usePublicConfig,
  useSearchResults,
  useTokens,
  useTransactions,
} from "@/api/hooks";
import { useHomeSnapshotStream } from "@/api/homeStream";
import type {
  BlockSummary,
  ChainStatus,
  SearchResult,
  TransactionSummary,
  VerificationJob,
} from "@/api/types";
import {
  formatInteger,
  formatNativeAmount,
  formatRelativeTimestamp,
  formatTokenAmount,
  formatTimestamp,
  shorten,
} from "@/components/format";
import { AddressIdentity } from "@/ens/AddressIdentity";
import { QueryNotice } from "@/components/QueryNotice";
import { TransactionMethodCell } from "./AddressPage";
import { IncludedTransactionStatus } from "./TransactionPage";

export const CORE_PAGE_SIZE = 25;
const SEARCH_PAGE_SIZE = 20;

export function formatTokenEventAmount(
  event: { amount?: string; decimals?: number; standard: string },
  locale: string,
): string {
  if (event.amount === undefined) return "—";
  return event.standard === "erc20"
    ? formatTokenAmount(event.amount, event.decimals, locale)
    : formatInteger(event.amount, locale);
}

export function isNFTStandard(standard: string): boolean {
  return standard === "erc721" || standard === "erc1155";
}

export function NFTTokenIDLink({
  address,
  prefix = false,
  tokenID,
}: {
  address: string;
  prefix?: boolean;
  tokenID: string;
}) {
  return (
    <Link to="/nft/$address/$tokenID" params={{ address, tokenID }}>
      <code>{prefix ? `#${tokenID}` : tokenID}</code>
    </Link>
  );
}

export function useCursorHistory(identity: string) {
  const [state, setState] = useState<{
    identity: string;
    cursors: string[];
    refreshGeneration: number;
  }>({
    identity,
    cursors: [""],
    refreshGeneration: 0,
  });
  const cursors = state.identity === identity ? state.cursors : [""];
  const refreshGeneration = state.identity === identity ? state.refreshGeneration : 0;

  return {
    cursor: cursors.at(-1) || undefined,
    refreshGeneration,
    page: cursors.length,
    hasPrevious: cursors.length > 1,
    next(nextCursor: string | undefined) {
      if (!nextCursor) return;
      setState((current) => ({
        identity,
        cursors: [
          ...(current.identity === identity ? current.cursors : [""]),
          nextCursor,
        ],
        refreshGeneration: current.identity === identity ? current.refreshGeneration : 0,
      }));
    },
    previous() {
      setState((current) => {
        const currentCursors = current.identity === identity ? current.cursors : [""];
        return {
          identity,
          cursors: currentCursors.length > 1 ? currentCursors.slice(0, -1) : currentCursors,
          refreshGeneration: current.identity === identity ? current.refreshGeneration : 0,
        };
      });
    },
    reset() {
      setState((current) => ({
        identity,
        cursors: [""],
        refreshGeneration:
          (current.identity === identity ? current.refreshGeneration : 0) + 1,
      }));
    },
  };
}

export function HomePage() {
  const { i18n, t } = useTranslation();
  const snapshot = useHomeSnapshotStream();
  const [relativeNow, setRelativeNow] = useState(() => Date.now());
  const locale = i18n.resolvedLanguage ?? "en";

  useEffect(() => {
    const intervalID = window.setInterval(() => setRelativeNow(Date.now()), 1_000);
    return () => window.clearInterval(intervalID);
  }, []);

  return (
    <div className="page-stack">
      <QueryNotice
        loading={snapshot.isPending}
        error={snapshot.data ? undefined : snapshot.error}
      />

      <section className="metrics-grid" aria-label={t("home.metrics")}>
        <Metric label={t("home.indexed")} value={formatInteger(snapshot.data?.status.indexed_block, locale)} />
        <Metric label={t("home.networkHead")} value={formatInteger(snapshot.data?.status.latest_block, locale)} />
        <Metric label={t("home.finality")} value={formatInteger(snapshot.data?.status.finalized_block, locale)} />
        <Metric
          label={t("home.lag")}
          value={snapshot.data ? (snapshot.data.status.core_ready && snapshot.data.status.lag === "0" ? t("home.caughtUp") : t("home.syncing")) : "—"}
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
      <strong className={accent ? "positive" : undefined}>{value}</strong>
    </article>
  );
}

function PanelHeading({ id, title, to }: { id: string; title: string; to: "/blocks" | "/transactions" }) {
  return (
    <header className="panel-heading">
      <h2 id={id}>{title}</h2>
      <Link to={to} aria-label={title}>
        <span aria-hidden="true">→</span>
      </Link>
    </header>
  );
}

function BlockRow({
  block,
  locale,
  now,
}: {
  block: BlockSummary;
  locale: string;
  now: number;
}) {
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
          <AddressIdentity address={transaction.from} /> → {transaction.to ? <AddressIdentity address={transaction.to} /> : "∅"}
        </small>
      </span>
      <IncludedTransactionStatus transaction={transaction} />
    </div>
  );
}

export function FinalityBadge({ finality }: { finality: string }) {
  const { t } = useTranslation();
  return <span className={`finality-badge ${finality}`}>{finalityLabel(finality, t)}</span>;
}

export function BlocksPage() {
  const { i18n, t } = useTranslation();
  const pager = useCursorHistory("blocks");
  const blocks = useBlocks(CORE_PAGE_SIZE, pager.cursor, pager.refreshGeneration);
  const status = useChainStatus();
  const locale = i18n.resolvedLanguage ?? "en";
  return (
    <Page title={t("page.blocks")} description={t("page.blocksDescription")}>
      <QueryNotice loading={status.isPending} error={status.error} />
      {status.data && <ChainContextPanel status={status.data} />}
      <p className="context-note" role="note">{t("context.canonicalBlocksOnly")}</p>
      <QueryNotice loading={blocks.isPending} error={blocks.error} onReset={pager.reset} />
      {blocks.data?.items.length === 0 && (
        <p className="empty-result" role="status">{t("state.noBlocks")}</p>
      )}
      {blocks.data && blocks.data.items.length > 0 && (
        <div className="table-scroll" tabIndex={0} aria-label={t("page.blocks")}>
          <table>
            <caption className="sr-only">{t("context.canonicalBlocksOnly")}</caption>
            <thead>
              <tr>
                <th>{t("table.block")}</th>
                <th>{t("table.age")}</th>
                <th>{t("table.transactions")}</th>
                <th>{t("table.gas")}</th>
                <th>{t("table.finality")}</th>
              </tr>
            </thead>
            <tbody>
              {blocks.data.items.map((block) => (
                <tr key={block.hash}>
                  <td>
                    <Link to="/blocks/$blockID" params={{ blockID: block.hash }}>
                      {formatInteger(block.number, locale)}
                    </Link>
                    <code className="table-secondary" title={block.hash}>{shorten(block.hash)}</code>
                  </td>
                  <td>{formatTimestamp(block.timestamp, locale)}</td>
                  <td>{formatInteger(block.transaction_count, locale)}</td>
                  <td>{formatInteger(block.gas_used, locale)}</td>
                  <td><FinalityBadge finality={block.finality} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {blocks.data && (
        <CursorPagination
          busy={blocks.isFetching}
          hasNext={Boolean(blocks.data.next_cursor)}
          hasPrevious={pager.hasPrevious}
          label={t("pagination.blocks")}
          onNext={() => pager.next(blocks.data?.next_cursor)}
          onPrevious={pager.previous}
          page={pager.page}
        />
      )}
    </Page>
  );
}

export function GenesisPage() {
  const { i18n, t } = useTranslation();
  const publicConfig = usePublicConfig();
  const nativeDecimals = publicConfig.data?.native_decimals ?? 18;
  const nativeSymbol = publicConfig.data?.native_symbol ?? "";
  const pager = useCursorHistory("genesis");
  const accounts = useGenesisAccounts(
    CORE_PAGE_SIZE,
    pager.cursor,
    pager.refreshGeneration,
  );
  const locale = i18n.resolvedLanguage ?? "en";
  return (
    <Page title={t("page.genesis")} description={t("page.genesisDescription")}>
      <p className="context-note" role="note">{t("context.genesisAuthenticated")}</p>
      <QueryNotice loading={accounts.isPending} error={accounts.error} onReset={pager.reset} />
      {accounts.data?.items.length === 0 && (
        <p className="empty-result" role="status">{t("state.noGenesisAccounts")}</p>
      )}
      {accounts.data && accounts.data.items.length > 0 && (
        <div className="table-scroll" tabIndex={0} aria-label={t("page.genesis")}>
          <table>
            <caption className="sr-only">{t("context.genesisAuthenticated")}</caption>
            <thead>
              <tr>
                <th>{t("table.address")}</th>
                <th>{t("detail.type")}</th>
                <th>{t("table.balance", { symbol: nativeSymbol })}</th>
                <th>{t("detail.nonce")}</th>
                <th>{t("detail.codeHash")}</th>
                <th>{t("detail.storageRoot")}</th>
              </tr>
            </thead>
            <tbody>
              {accounts.data.items.map((account) => (
                <tr key={account.address}>
                  <td>
                    <AddressIdentity address={account.address} />
                  </td>
                  <td>{t(`accountType.${account.type}`)}</td>
                  <td>{formatNativeAmount(account.balance, locale, nativeDecimals)}</td>
                  <td>{formatInteger(account.nonce, locale)}</td>
                  <td><code title={account.code_hash}>{shorten(account.code_hash)}</code></td>
                  <td><code title={account.storage_root}>{shorten(account.storage_root)}</code></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {accounts.data && (
        <CursorPagination
          busy={accounts.isFetching}
          hasNext={Boolean(accounts.data.next_cursor)}
          hasPrevious={pager.hasPrevious}
          label={t("pagination.genesis")}
          onNext={() => pager.next(accounts.data?.next_cursor)}
          onPrevious={pager.previous}
          page={pager.page}
        />
      )}
    </Page>
  );
}

export function TransactionsPage() {
  const { i18n, t } = useTranslation();
  const publicConfig = usePublicConfig();
  const nativeDecimals = publicConfig.data?.native_decimals ?? 18;
  const nativeSymbol = publicConfig.data?.native_symbol ?? "";
  const pager = useCursorHistory("transactions");
  const transactions = useTransactions(
    CORE_PAGE_SIZE,
    pager.cursor,
    pager.refreshGeneration,
  );
  const status = useChainStatus();
  const locale = i18n.resolvedLanguage ?? "en";
  return (
    <Page title={t("page.transactions")} description={t("page.transactionsDescription")}>
      <QueryNotice loading={status.isPending} error={status.error} />
      {status.data && <ChainContextPanel status={status.data} />}
      <p className="context-note" role="note">{t("context.canonicalTransactionsOnly")}</p>
      <QueryNotice
        loading={transactions.isPending}
        error={transactions.error}
        onReset={pager.reset}
      />
      {transactions.data?.items.length === 0 && (
        <p className="empty-result" role="status">{t("state.noTransactions")}</p>
      )}
      {transactions.data && transactions.data.items.length > 0 && (
        <div className="table-scroll" tabIndex={0} aria-label={t("page.transactions")}>
          <table>
            <caption className="sr-only">{t("context.canonicalTransactionsOnly")}</caption>
            <thead>
              <tr>
                <th>{t("table.hash")}</th>
                <th>{t("table.method")}</th>
                <th>{t("table.block")}</th>
                <th>{t("table.status")}</th>
                <th>{t("table.from")}</th>
                <th>{t("table.to")}</th>
                <th>{t("table.value", { symbol: nativeSymbol })}</th>
                <th>{t("table.finality")}</th>
              </tr>
            </thead>
            <tbody>
              {transactions.data.items.map((transaction) => (
                <tr key={transaction.hash}>
                  <td>
                    <Link to="/tx/$hash" params={{ hash: transaction.hash }} search={{ tab: "overview" }}>
                      {shorten(transaction.hash)}
                    </Link>
                  </td>
                  <TransactionMethodCell
                    method={transaction.method}
                    signature={transaction.method_signature}
                  />
                  <td>
                    {transaction.block_hash ? (
                      <Link to="/blocks/$blockID" params={{ blockID: transaction.block_hash }}>
                        {formatInteger(transaction.block_number, locale)}
                      </Link>
                    ) : "—"}
                  </td>
                  <td>
                    <IncludedTransactionStatus transaction={transaction} />
                  </td>
                  <td>
                    <AddressIdentity address={transaction.from} />
                  </td>
                  <td>
                    {transaction.to ? (
                      <AddressIdentity address={transaction.to} />
                    ) : t("common.contractCreation")}
                  </td>
                  <td><code>{formatNativeAmount(transaction.value, locale, nativeDecimals)}</code></td>
                  <td><FinalityBadge finality={transaction.finality} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {transactions.data && (
        <CursorPagination
          busy={transactions.isFetching}
          hasNext={Boolean(transactions.data.next_cursor)}
          hasPrevious={pager.hasPrevious}
          label={t("pagination.transactions")}
          onNext={() => pager.next(transactions.data?.next_cursor)}
          onPrevious={pager.previous}
          page={pager.page}
        />
      )}
    </Page>
  );
}

export function TokensPage() {
  const { i18n, t } = useTranslation();
  const pager = useCursorHistory("tokens");
  const tokens = useTokens(CORE_PAGE_SIZE, pager.cursor, pager.refreshGeneration);
  const locale = i18n.resolvedLanguage ?? "en";

  return (
    <Page title={t("page.tokens")} description={t("page.tokensDescription")}>
      <QueryNotice loading={tokens.isPending} error={tokens.error} onReset={pager.reset} />
      {tokens.data && tokens.data.items.length === 0 && (
        <p className="empty-result" role="status">{t("state.noTokens")}</p>
      )}
      {tokens.data && tokens.data.items.length > 0 && (
        <div className="table-scroll" tabIndex={0} aria-label={t("page.tokens")}>
          <table>
            <caption className="sr-only">{t("page.tokensDescription")}</caption>
            <thead>
              <tr>
                <th>{t("table.token")}</th>
                <th>{t("table.standard")}</th>
                <th>{t("table.confidence")}</th>
                <th>{t("table.supply")}</th>
                <th>{t("table.metadata")}</th>
              </tr>
            </thead>
            <tbody>
              {tokens.data.items.map((token) => (
                <tr key={token.address}>
                  <td>
                    <span className="table-primary">
                      <Link to="/token/$address" params={{ address: token.address }}>
                        {token.name ?? token.symbol ?? shorten(token.address)}
                      </Link>
                      <code>{shorten(token.address)}</code>
                    </span>
                  </td>
                  <td><span className="result-kind">{tokenStandardLabel(token.standard, t)}</span></td>
                  <td>{confidenceLabel(token.confidence, t)}</td>
                  <td><code>{formatInteger(token.total_supply, locale)}</code></td>
                  <td>{stageStateLabel(token.metadata_state, t)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {tokens.data && (
        <CursorPagination
          busy={tokens.isFetching}
          hasNext={Boolean(tokens.data.next_cursor)}
          hasPrevious={pager.hasPrevious}
          label={t("pagination.tokens")}
          onNext={() => pager.next(tokens.data?.next_cursor)}
          onPrevious={pager.previous}
          page={pager.page}
        />
      )}
    </Page>
  );
}

type ChainStatusContext = ChainStatus & {
  coverage_start?: string;
  coverage_end?: string;
};

export function ChainContextPanel({ status }: { status: ChainStatusContext }) {
  const { i18n, t } = useTranslation();
  const locale = i18n.resolvedLanguage ?? "en";
  const coverageStart = status.coverage_start;
  const coverageEnd = status.coverage_end ?? status.indexed_block;

  return (
    <section className="panel chain-context" aria-labelledby="chain-context-title">
      <div className="panel-heading chain-context-heading">
        <div>
          <span className="eyebrow">{t("context.canonicalSnapshot")}</span>
          <h2 id="chain-context-title">{t("context.coverageTitle")}</h2>
        </div>
        <span className={status.core_ready ? "availability yes" : "availability no"}>
          {status.core_ready ? t("context.coreReady") : t("context.coreNotReady")}
        </span>
      </div>
      <dl className="chain-context-grid">
        <div>
          <dt>{t("context.contiguousEnd")}</dt>
          <dd>{formatInteger(status.indexed_block, locale)}</dd>
        </div>
        <div>
          <dt>{t("context.coverageBounds")}</dt>
          <dd>
            {formatInteger(coverageStart, locale)} – {formatInteger(coverageEnd, locale)}
          </dd>
        </div>
        <div>
          <dt>{t("home.highestCovered")}</dt>
          <dd>{formatInteger(status.highest_covered_block, locale)}</dd>
        </div>
        <div>
          <dt>{t("context.safeBlock")}</dt>
          <dd>{formatInteger(status.safe_block, locale)}</dd>
        </div>
        <div>
          <dt>{t("home.finality")}</dt>
          <dd>{formatInteger(status.finalized_block, locale)}</dd>
        </div>
        <div>
          <dt>{t("home.backfill")}</dt>
          <dd>{status.backfill_complete ? t("home.backfillComplete") : t("home.backfillIncomplete")}</dd>
        </div>
      </dl>
      {!status.backfill_complete && (
        <p className="coverage-warning" role="status">{t("context.coverageIslandWarning")}</p>
      )}
    </section>
  );
}

export function ReorgContext({ kind, hash }: { kind: "block" | "transaction"; hash: string }) {
  const { t } = useTranslation();
  return (
    <section className="reorg-context" role="status" aria-labelledby="reorg-context-title">
      <span className="reorg-mark" aria-hidden="true">↺</span>
      <div>
        <h2 id="reorg-context-title">
          {kind === "block" ? t("context.orphanBlock") : t("context.orphanTransaction")}
        </h2>
        <p>{t("context.orphanDetail")}</p>
        <code>{hash}</code>
      </div>
    </section>
  );
}

export function CursorPagination({
  busy,
  hasNext,
  hasPrevious,
  label,
  onNext,
  onPrevious,
  page,
}: {
  busy: boolean;
  hasNext: boolean;
  hasPrevious: boolean;
  label: string;
  onNext: () => void;
  onPrevious: () => void;
  page: number;
}) {
  const { i18n, t } = useTranslation();
  const locale = i18n.resolvedLanguage ?? "en";
  return (
    <nav className="cursor-pagination" aria-busy={busy} aria-label={label}>
      <button
        className="button secondary"
        disabled={!hasPrevious || busy}
        onClick={onPrevious}
        type="button"
      >
        {t("pagination.previous")}
      </button>
      <span aria-live="polite">{t("pagination.page", { page: formatInteger(page, locale) })}</span>
      <button
        className="button secondary"
        disabled={!hasNext || busy}
        onClick={onNext}
        type="button"
      >
        {t("pagination.next")}
      </button>
    </nav>
  );
}

export function DetailList({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <section className="panel detail-card" aria-label={label}>
      <h2>{label}</h2>
      <dl className="detail-grid">{children}</dl>
    </section>
  );
}

export function Detail({ label, value, mono, wide }: { label: string; value?: React.ReactNode; mono?: boolean; wide?: boolean }) {
  return (
    <div className={wide ? "detail-item wide" : "detail-item"}>
      <dt>{label}</dt>
      <dd className={mono ? "mono-wrap" : undefined}>{value ?? "—"}</dd>
    </div>
  );
}

export function CapabilityDegraded({ stage, state }: { stage: string; state: string }) {
  const { t } = useTranslation();
  return (
    <div className="query-notice degraded" role="status">
      <span className="status-dot warning" aria-hidden="true" />
      <span>
        <strong>{t("state.stageUnavailable", { stage: stageLabel(stage, t) })}</strong>
        <small>{t("state.stageUnavailableDetail", { state: stageStateLabel(state, t), block: "" })}</small>
      </span>
    </div>
  );
}

export type Translate = ReturnType<typeof useTranslation>["t"];

export function finalityLabel(value: string, t: Translate): string {
  switch (value) {
    case "pending": return t("finality.pending");
    case "latest": return t("finality.latest");
    case "safe": return t("finality.safe");
    case "finalized": return t("finality.finalized");
    case "orphan": return t("finality.orphan");
    default: return value;
  }
}

export function transactionStatusLabel(value: string | undefined, t: Translate): string {
  switch (value) {
    case "pending": return t("transactionStatus.pending");
    case "success": return t("transactionStatus.success");
    case "failed": return t("transactionStatus.failed");
    case "unknown": return t("transactionStatus.unknown");
    default: return t("common.indexed");
  }
}

export function transactionTypeLabel(value: string | undefined, t: Translate): string {
  if (!value) return "—";
  const normalized = value.trim().toLowerCase();
  const parsed = normalized.startsWith("0x")
    ? Number.parseInt(normalized.slice(2), 16)
    : Number.parseInt(normalized, 10);
  if (!Number.isNaN(parsed)) {
    switch (String(parsed)) {
      case "0": return t("transactionType.legacy");
      case "1": return t("transactionType.accessList");
      case "2": return t("transactionType.dynamicFee");
      case "3": return t("transactionType.blob");
      case "4": return t("transactionType.eip7702");
    }
  }
  return value;
}

export function accountTypeLabel(value: string, t: Translate): string {
  switch (value) {
    case "eoa": return t("accountType.eoa");
    case "contract": return t("accountType.contract");
    case "delegated_eoa": return t("accountType.delegatedEoa");
    case "unknown": return t("accountType.unknown");
    default: return t("accountType.unknown");
  }
}

export function stageLabel(value: string, t: Translate): string {
  switch (value) {
    case "core": return t("stage.core");
    case "token": return t("stage.token");
    case "stats":
    case "statistics":
      return t("stage.stats");
    case "trace": return t("stage.trace");
    case "metadata": return t("stage.metadata");
    case "state": return t("stage.state");
    default: return value;
  }
}

export function stageStateLabel(value: string, t: Translate): string {
  switch (value) {
    case "complete": return t("stageState.complete");
    case "pending": return t("stageState.pending");
    case "unavailable": return t("stageState.unavailable");
    case "failed": return t("stageState.failed");
    default: return value;
  }
}

export function tokenStandardLabel(value: string, t: Translate): string {
  switch (value) {
    case "erc20": return t("tokenStandard.erc20");
    case "erc721": return t("tokenStandard.erc721");
    case "erc1155": return t("tokenStandard.erc1155");
    default: return t("tokenStandard.unknown");
  }
}

export function confidenceLabel(value: string, t: Translate): string {
  switch (value) {
    case "verified": return t("confidence.verified");
    case "high": return t("confidence.high");
    case "inferred": return t("confidence.inferred");
    case "guess": return t("confidence.guess");
    case "rpc_exact": return t("confidence.rpcExact");
    default: return value;
  }
}

export function tokenEventKindLabel(value: string, t: Translate): string {
  switch (value) {
    case "transfer": return t("tokenEvent.transfer");
    case "mint": return t("tokenEvent.mint");
    case "burn": return t("tokenEvent.burn");
    case "approval": return t("tokenEvent.approval");
    case "approval_for_all": return t("tokenEvent.approvalForAll");
    default: return value;
  }
}

export function featureLabel(value: string, t: Translate): string {
  switch (value) {
    case "trace": return t("feature.trace");
    case "mempool": return t("feature.mempool");
    case "historical_state": return t("feature.historicalState");
    case "verification": return t("feature.verification");
    case "sourcify": return t("feature.sourcify");
    case "nft_metadata": return t("feature.nftMetadata");
    case "pricing": return t("feature.pricing");
    default: return value;
  }
}

export function verificationJobStatusLabel(value: VerificationJob["status"], t: Translate): string {
  switch (value) {
    case "queued": return t("verificationStatus.queued");
    case "running": return t("verificationStatus.running");
    case "succeeded": return t("verificationStatus.succeeded");
    case "failed": return t("verificationStatus.failed");
    case "cancelled": return t("verificationStatus.cancelled");
  }
}

export function verificationMatchLabel(value: string | undefined, t: Translate): string {
  switch (value) {
    case "full": return t("verificationMatch.full");
    case "partial": return t("verificationMatch.partial");
    default: return "—";
  }
}

export function verificationLanguageLabel(value: string, t: Translate): string {
  switch (value) {
    case "solidity": return t("verificationLanguage.solidity");
    case "yul": return t("verificationLanguage.yul");
    case "geas": return t("verificationLanguage.geas");
    default: return value;
  }
}

export function searchKindLabel(value: SearchResult["kind"], t: Translate): string {
  switch (value) {
    case "block": return t("searchKind.block");
    case "transaction": return t("searchKind.transaction");
    case "address": return t("searchKind.address");
    case "contract": return t("searchKind.contract");
    case "token": return t("searchKind.token");
    case "nft": return t("searchKind.nft");
    case "label": return t("searchKind.label");
  }
}

export function yesNo(value: boolean, t: ReturnType<typeof useTranslation>["t"]): string {
  return value ? t("common.yes") : t("common.no");
}

export function StatusPage() {
  const { i18n, t } = useTranslation();
  const status = useChainStatus();
  const publicConfig = usePublicConfig();
  const locale = i18n.resolvedLanguage ?? "en";
  return (
    <Page title={t("page.status")} description={t("page.statusDescription")}>
      <QueryNotice loading={status.isPending || publicConfig.isPending} error={status.error ?? publicConfig.error} />
      {status.data && (
        <div className="status-layout">
          <section className="panel status-card" aria-labelledby="sync-status-title">
            <span>{publicConfig.data?.chain_name ?? t("app.tagline")}</span>
            <strong id="sync-status-title">
              {t("common.chain")} {formatInteger(status.data.chain_id, locale)}
            </strong>
            <dl>
              <div>
                <dt>{t("context.coreReadiness")}</dt>
                <dd>
                  <span className={status.data.core_ready ? "availability yes" : "availability no"}>
                    {status.data.core_ready ? t("context.coreReady") : t("context.coreNotReady")}
                  </span>
                </dd>
              </div>
              <div><dt>{t("home.indexed")}</dt><dd>{formatInteger(status.data.indexed_block, locale)}</dd></div>
              <div>
                <dt>{t("home.highestCovered")}</dt>
                <dd>{formatInteger(status.data.highest_covered_block, locale)}</dd>
              </div>
              <div><dt>{t("home.networkHead")}</dt><dd>{formatInteger(status.data.latest_block, locale)}</dd></div>
              <div><dt>{t("home.lagBlocks")}</dt><dd>{formatInteger(status.data.lag, locale)}</dd></div>
              <div><dt>{t("context.safeBlock")}</dt><dd>{formatInteger(status.data.safe_block, locale)}</dd></div>
              <div><dt>{t("home.finality")}</dt><dd>{formatInteger(status.data.finalized_block, locale)}</dd></div>
              <div>
                <dt>{t("context.coverageBounds")}</dt>
                <dd>
                  {formatInteger(status.data.coverage_start, locale)} –{" "}
                  {formatInteger(status.data.coverage_end, locale)}
                </dd>
              </div>
              <div>
                <dt>{t("home.backfill")}</dt>
                <dd>{status.data.backfill_complete ? t("home.backfillComplete") : t("home.backfillIncomplete")}</dd>
              </div>
            </dl>
          </section>
          <div className="status-capabilities-stack">
            <section className="panel capability-list" aria-labelledby="data-capabilities-title">
              <h2 id="data-capabilities-title">{t("status.dataCapabilities")}</h2>
              <ul>
                {Object.entries(status.data.completeness).map(([name, state]) => (
                  <li key={name}>
                    <code>{stageLabel(name, t)}</code>
                    <span className={state === "complete" ? "availability yes" : "availability no"}>
                      {stageStateLabel(state, t)}
                    </span>
                  </li>
                ))}
              </ul>
            </section>
            {publicConfig.data && (
              <section className="panel capability-list" aria-labelledby="configured-features-title">
                <h2 id="configured-features-title">{t("status.configuredFeatures")}</h2>
                <ul>
                  {Object.entries(publicConfig.data.features)
                    .sort(([left], [right]) => left.localeCompare(right))
                    .map(([name, enabled]) => (
                      <li key={name}>
                        <code>{featureLabel(name, t)}</code>
                        <span className={enabled ? "availability yes" : "availability no"}>
                          {enabled ? t("status.enabled") : t("status.disabled")}
                        </span>
                      </li>
                    ))}
                </ul>
              </section>
            )}
          </div>
        </div>
      )}
    </Page>
  );
}

export function SearchPage({ query }: { query: string }) {
  const { t } = useTranslation();
  const normalizedQuery = query.trim();
  const pager = useCursorHistory(`search:${normalizedQuery}`);
  const search = useSearchResults(
    normalizedQuery,
    pager.cursor,
    SEARCH_PAGE_SIZE,
    pager.refreshGeneration,
  );
  return (
    <Page title={t("page.search")} description={query} mono>
      {normalizedQuery.length === 0 && <p className="context-note">{t("search.prompt")}</p>}
      <QueryNotice
        loading={search.isPending && normalizedQuery.length > 0}
        error={search.error}
        onReset={pager.reset}
      />
      {search.data && search.data.items.length === 0 && (
        <p className="empty-result" role="status">{t("state.noResults")}</p>
      )}
      <div className="search-results">
        {search.data?.items.map((result) => (
          <SearchResultLink key={`${result.kind}:${result.key}`} result={result} />
        ))}
      </div>
      {search.data && (
        <CursorPagination
          busy={search.isFetching}
          hasNext={Boolean(search.data.next_cursor)}
          hasPrevious={pager.hasPrevious}
          label={t("pagination.search")}
          onNext={() => pager.next(search.data?.next_cursor)}
          onPrevious={pager.previous}
          page={pager.page}
        />
      )}
    </Page>
  );
}

function SearchResultLink({ result }: { result: SearchResult }) {
  const { t } = useTranslation();
  const content = (
    <>
      <span className="result-kind">{searchKindLabel(result.kind, t)}</span>
      <span>
        <strong><bdi>{result.label}</bdi></strong>
        {result.name_source === "custom_ens" ? <small className="custom-ens-badge">Custom ENS</small> : null}
        <small>{result.key}</small>
      </span>
      <span className="search-result-tail">
        {result.canonical !== undefined && (
          <span className={result.canonical ? "availability yes" : "orphan-label"}>
            {result.canonical ? t("common.canonical") : t("common.orphan")}
          </span>
        )}
        <span aria-hidden="true">→</span>
      </span>
    </>
  );

  switch (result.kind) {
    case "block":
      return <Link className="search-result" to="/blocks/$blockID" params={{ blockID: result.key }}>{content}</Link>;
    case "transaction":
      return <Link className="search-result" to="/tx/$hash" params={{ hash: result.key }} search={{ tab: "overview" }}>{content}</Link>;
    case "address":
      return <Link className="search-result" to="/address/$address" params={{ address: result.key }}>{content}</Link>;
    case "contract":
      return <Link className="search-result" hash="code" params={{ address: result.key }} search={{}} to="/address/$address">{content}</Link>;
    case "token":
      return <Link className="search-result" to="/token/$address" params={{ address: result.key }}>{content}</Link>;
    default:
      return <a className="search-result" href={`/search?q=${encodeURIComponent(result.key)}`}>{content}</a>;
  }
}

export function TextArtifact({ title, value }: { title: string; value: unknown }) {
  return (
    <section className="artifact-panel">
      <h3>{title}</h3>
      <pre tabIndex={0}>{JSON.stringify(value, null, 2)}</pre>
    </section>
  );
}

export function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

export class DuplicateJSONKeyError extends Error {}
export class JSONStructureLimitError extends Error {}
export class UnsafeJSONNumberError extends Error {}

export function assertNoDuplicateJSONKeys(source: string): void {
  let offset = 0;
  const maximumDepth = 256;
  const whitespace = /\s/;

  const skipWhitespace = () => {
    while (offset < source.length && whitespace.test(source[offset] ?? "")) offset += 1;
  };

  const parseString = (): string => {
    if (source[offset] !== '"') throw new SyntaxError("expected JSON string");
    const start = offset;
    offset += 1;
    while (offset < source.length) {
      const character = source[offset];
      if (character === "\\") {
        offset += 2;
        continue;
      }
      offset += 1;
      if (character === '"') {
        return JSON.parse(source.slice(start, offset)) as string;
      }
    }
    throw new SyntaxError("unterminated JSON string");
  };

  const parseValue = (depth: number): void => {
    if (depth > maximumDepth) throw new JSONStructureLimitError();
    skipWhitespace();
    const character = source[offset];
    if (character === "{") {
      offset += 1;
      skipWhitespace();
      const keys = new Set<string>();
      if (source[offset] === "}") {
        offset += 1;
        return;
      }
      while (offset < source.length) {
        const key = parseString();
        if (keys.has(key)) throw new DuplicateJSONKeyError();
        keys.add(key);
        skipWhitespace();
        if (source[offset] !== ":") throw new SyntaxError("expected JSON colon");
        offset += 1;
        parseValue(depth + 1);
        skipWhitespace();
        if (source[offset] === "}") {
          offset += 1;
          return;
        }
        if (source[offset] !== ",") throw new SyntaxError("expected JSON comma");
        offset += 1;
        skipWhitespace();
      }
      throw new SyntaxError("unterminated JSON object");
    }
    if (character === "[") {
      offset += 1;
      skipWhitespace();
      if (source[offset] === "]") {
        offset += 1;
        return;
      }
      while (offset < source.length) {
        parseValue(depth + 1);
        skipWhitespace();
        if (source[offset] === "]") {
          offset += 1;
          return;
        }
        if (source[offset] !== ",") throw new SyntaxError("expected JSON comma");
        offset += 1;
      }
      throw new SyntaxError("unterminated JSON array");
    }
    if (character === '"') {
      parseString();
      return;
    }
    const start = offset;
    while (
      offset < source.length &&
      !whitespace.test(source[offset] ?? "") &&
      !",]}".includes(source[offset] ?? "")
    ) {
      offset += 1;
    }
    if (offset === start) throw new SyntaxError("expected JSON value");
    const primitive = source.slice(start, offset);
    if (/^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$/.test(primitive)) {
      const parsed = Number(primitive);
      if (
        !Number.isSafeInteger(parsed) ||
        !/^-?(?:0|[1-9]\d*)$/.test(primitive) ||
        String(parsed) !== primitive
      ) {
        throw new UnsafeJSONNumberError();
      }
    }
  };

  parseValue(0);
  skipWhitespace();
  if (offset !== source.length) throw new SyntaxError("unexpected JSON suffix");
}

export function logDecodingKey(status: string) {
  switch (status) {
    case "decoded": return "detail.logDecoded" as const;
    case "ambiguous": return "detail.logAmbiguous" as const;
    case "unknown": return "detail.logUnknown" as const;
    case "malformed": return "detail.logMalformed" as const;
    default: return "detail.logUnavailable" as const;
  }
}

export function abiSourceKindLabel(value: string, t: Translate): string {
  switch (value) {
    case "exact_address": return t("detail.abiSourceKinds.exactAddress");
    case "code_hash": return t("detail.abiSourceKinds.codeHash");
    case "proxy_implementation": return t("detail.abiSourceKinds.proxyImplementation");
    case "signature_database": return t("detail.abiSourceKinds.signatureDatabase");
    case "builtin": return t("detail.abiSourceKinds.builtin");
    default: return value;
  }
}

export function attributionLabel(value: string, t: Translate): string {
  return value === "exact_trace"
    ? t("detail.attributionKinds.exactTrace")
    : t("detail.attributionKinds.addressFallback");
}

export function traceDecodingKey(status: string) {
  switch (status) {
    case "decoded": return "detail.traceDecoded" as const;
    case "ambiguous": return "detail.traceAmbiguous" as const;
    case "unknown": return "detail.traceUnknown" as const;
    case "malformed": return "detail.traceMalformed" as const;
    case "not_applicable": return "detail.traceNotApplicable" as const;
    default: return "detail.traceUnavailable" as const;
  }
}

export function traceOutputStatusKey(status: string) {
  switch (status) {
    case "decoded": return "detail.outputDecoded" as const;
    case "empty": return "detail.outputEmpty" as const;
    case "unknown": return "detail.outputUnknown" as const;
    case "malformed": return "detail.outputMalformed" as const;
    case "not_applicable": return "detail.outputNotApplicable" as const;
    default: return "detail.outputUnavailable" as const;
  }
}

export function formatLogArgument(value: unknown): string {
  if (typeof value === "string") return value;
  if (typeof value === "boolean" || typeof value === "number" || value === null) {
    return String(value);
  }
  try {
    return JSON.stringify(value) ?? "[unavailable]";
  } catch {
    return "[unavailable]";
  }
}

export function NotFoundPage() {
  const { t } = useTranslation();
  return (
    <Page title="404 ·" description={t("page.notFound")}>
      <p>{t("page.notFoundDescription")}</p>
      <Link className="button primary inline-button" to="/">
        {t("nav.home")}
      </Link>
    </Page>
  );
}

export function Page({
  title,
  description,
  children,
  mono,
}: {
  title: string;
  description: React.ReactNode;
  children: React.ReactNode;
  mono?: boolean;
}) {
  return (
    <div className="page-stack inner-page">
      <header className="page-header">
        <h1>{title}</h1>
        <div className={mono ? "page-description mono-wrap" : "page-description"}>
          {description}
        </div>
      </header>
      {children}
    </div>
  );
}
