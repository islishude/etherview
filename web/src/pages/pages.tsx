import { FormEvent, lazy, Suspense, useEffect, useMemo, useState } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { getAddress, isAddress, isHex, toHex, type Hex } from "viem";

import {
  useAddress,
  useBlock,
  useBlockStats,
  useBlocks,
  useChainStatus,
  useNFTOwnership,
  usePublicConfig,
  useSearchResults,
  useToken,
  useTokenTransfers,
  useTokens,
  useTransaction,
  useTransactionTrace,
  useTransactions,
  useSubmitVerification,
  useVerificationJob,
  useVerifiedContract,
} from "@/api/hooks";
import type {
  BlockStat,
  BlockSummary,
  ChainStatus,
  Completeness,
  SearchResult,
  TokenEvent,
  TransactionSummary,
  VerificationJob,
  VerificationSubmission,
  VerifiedContract,
} from "@/api/types";
import { formatInteger, formatTimestamp, shorten } from "@/components/format";
import { QueryNotice } from "@/components/QueryNotice";
import { chainsMatch } from "@/wallet/eip6963";
import { useWallet } from "@/wallet/WalletProvider";

const StatsChart = lazy(async () => {
  const module = await import("@/components/StatsChart");
  return { default: module.StatsChart };
});

const CORE_PAGE_SIZE = 25;
const SEARCH_PAGE_SIZE = 20;

function useCursorHistory(identity: string) {
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
  const status = useChainStatus();
  const blocks = useBlocks(6);
  const transactions = useTransactions(6);
  const locale = i18n.resolvedLanguage ?? "en";

  return (
    <div className="page-stack">
      <section className="hero">
        <div className="hero-copy">
          <span className="eyebrow">{t("home.eyebrow")}</span>
          <h1>{t("home.title")}</h1>
          <p>{t("home.description")}</p>
        </div>
        <div className="chain-orbit" aria-hidden="true">
          <span className="orbit-ring outer" />
          <span className="orbit-ring inner" />
          <span className="orbit-core">E</span>
          <span className="orbit-node one" />
          <span className="orbit-node two" />
          <span className="orbit-node three" />
        </div>
      </section>

      <QueryNotice loading={status.isPending} error={status.error} />

      <section className="metrics-grid" aria-label={t("home.metrics")}>
        <Metric label={t("home.indexed")} value={formatInteger(status.data?.indexed_block, locale)} />
        <Metric label={t("home.networkHead")} value={formatInteger(status.data?.latest_block, locale)} />
        <Metric label={t("home.finality")} value={formatInteger(status.data?.finalized_block, locale)} />
        <Metric
          label={t("home.lag")}
          value={status.data ? (status.data.core_ready && status.data.lag === "0" ? t("home.caughtUp") : t("home.syncing")) : "—"}
          accent={status.data?.core_ready && status.data.lag === "0"}
        />
      </section>

      {status.data && <ChainContextPanel status={status.data} />}

      <div className="activity-grid">
        <section className="panel activity-panel" aria-labelledby="recent-blocks-title">
          <PanelHeading id="recent-blocks-title" title={t("home.recentBlocks")} to="/blocks" />
          <QueryNotice compact loading={blocks.isPending} error={blocks.error} />
          {blocks.data?.items.length === 0 && (
            <p className="empty-result compact-empty">{t("state.noBlocks")}</p>
          )}
          {blocks.data?.items.map((block) => (
            <BlockRow block={block} key={block.hash} locale={locale} />
          ))}
        </section>
        <section className="panel activity-panel" aria-labelledby="recent-transactions-title">
          <PanelHeading
            id="recent-transactions-title"
            title={t("home.recentTransactions")}
            to="/transactions"
          />
          <QueryNotice compact loading={transactions.isPending} error={transactions.error} />
          {transactions.data?.items.length === 0 && (
            <p className="empty-result compact-empty">{t("state.noTransactions")}</p>
          )}
          {transactions.data?.items.map((transaction) => (
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

function BlockRow({ block, locale }: { block: BlockSummary; locale: string }) {
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
        <small>{formatTimestamp(block.timestamp, locale)}</small>
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
  const { t } = useTranslation();
  return (
    <div className="activity-row transaction-row">
      <span className="tx-mark" aria-hidden="true">
        ↗
      </span>
      <span className="activity-primary">
        <Link to="/tx/$hash" params={{ hash: transaction.hash }}>
          {shorten(transaction.hash)}
        </Link>
        <small>
          {shorten(transaction.from)} → {transaction.to ? shorten(transaction.to) : "∅"}
        </small>
      </span>
      <span className={`transaction-status ${transaction.status ?? "unknown"}`}>
        {transactionStatusLabel(transaction.status, t)}
      </span>
    </div>
  );
}

function FinalityBadge({ finality }: { finality: string }) {
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

export function TransactionsPage() {
  const { i18n, t } = useTranslation();
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
                <th>{t("table.block")}</th>
                <th>{t("table.status")}</th>
                <th>{t("table.from")}</th>
                <th>{t("table.to")}</th>
                <th>{t("table.value")}</th>
                <th>{t("table.finality")}</th>
              </tr>
            </thead>
            <tbody>
              {transactions.data.items.map((transaction) => (
                <tr key={transaction.hash}>
                  <td>
                    <Link to="/tx/$hash" params={{ hash: transaction.hash }}>
                      {shorten(transaction.hash)}
                    </Link>
                  </td>
                  <td>
                    {transaction.block_hash ? (
                      <Link to="/blocks/$blockID" params={{ blockID: transaction.block_hash }}>
                        {formatInteger(transaction.block_number, locale)}
                      </Link>
                    ) : "—"}
                  </td>
                  <td>
                    <span className={`transaction-status ${transaction.status ?? "unknown"}`}>
                      {transactionStatusLabel(transaction.status, t)}
                    </span>
                  </td>
                  <td>
                    <Link to="/address/$address" params={{ address: transaction.from }}>
                      <code>{shorten(transaction.from)}</code>
                    </Link>
                  </td>
                  <td>
                    {transaction.to ? (
                      <Link to="/address/$address" params={{ address: transaction.to }}>
                        <code>{shorten(transaction.to)}</code>
                      </Link>
                    ) : t("common.contractCreation")}
                  </td>
                  <td><code>{formatInteger(transaction.value, locale)}</code></td>
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
  const tokens = useTokens(25);
  const locale = i18n.resolvedLanguage ?? "en";

  return (
    <Page title={t("page.tokens")} description={t("page.tokensDescription")}>
      <QueryNotice loading={tokens.isPending} error={tokens.error} />
      {tokens.data && tokens.data.items.length === 0 && (
        <p className="empty-result">{t("state.noTokens")}</p>
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
                  <td><span className="result-kind">{token.standard}</span></td>
                  <td>{token.confidence}</td>
                  <td><code>{formatInteger(token.total_supply, locale)}</code></td>
                  <td>{token.metadata_state}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Page>
  );
}

export type EntityKind = "block" | "transaction" | "address" | "token" | "nft";

export function EntityPage({ kind, identifier, secondary }: { kind: EntityKind; identifier: string; secondary?: string }) {
  switch (kind) {
    case "block":
      return <BlockDetailPage identifier={identifier} />;
    case "transaction":
      return <TransactionDetailPage hash={identifier} />;
    case "address":
      return <AddressDetailPage address={identifier} />;
    case "token":
      return <TokenDetailPage address={identifier} />;
    case "nft":
      return <NFTDetailPage address={identifier} tokenID={secondary ?? ""} />;
  }
}

function BlockDetailPage({ identifier }: { identifier: string }) {
  const { i18n, t } = useTranslation();
  const block = useBlock(identifier);
  const locale = i18n.resolvedLanguage ?? "en";

  return (
    <Page title={t("page.block")} description={identifier} mono>
      <QueryNotice loading={block.isPending} error={block.error} />
      {block.data && (
        <>
          {!block.data.canonical && (
            <ReorgContext kind="block" hash={block.data.hash} />
          )}
          <DetailList label={t("detail.blockSummary")}>
            <Detail label={t("table.block")} value={formatInteger(block.data.number, locale)} />
            <Detail label={t("table.hash")} value={block.data.hash} mono />
            <Detail
              label={t("detail.parentHash")}
              mono
              value={block.data.number === "0" ? block.data.parent_hash : (
                <Link to="/blocks/$blockID" params={{ blockID: block.data.parent_hash }}>
                  {block.data.parent_hash}
                </Link>
              )}
            />
            <Detail label={t("table.age")} value={formatTimestamp(block.data.timestamp, locale)} />
            <Detail label={t("table.transactions")} value={formatInteger(block.data.transaction_count, locale)} />
            <Detail label={t("table.gas")} value={formatInteger(block.data.gas_used, locale)} />
            <Detail label={t("detail.gasLimit")} value={formatInteger(block.data.gas_limit, locale)} />
            <Detail label={t("detail.baseFee")} value={formatInteger(block.data.base_fee_per_gas, locale)} />
            <Detail
              label={t("detail.miner")}
              mono
              value={block.data.miner ? (
                <Link to="/address/$address" params={{ address: block.data.miner }}>
                  {block.data.miner}
                </Link>
              ) : undefined}
            />
            <Detail label={t("detail.canonical")} value={yesNo(block.data.canonical, t)} />
            <Detail label={t("table.finality")} value={finalityLabel(block.data.finality, t)} />
          </DetailList>
          <CompletenessPanel completeness={block.data.completeness} />
        </>
      )}
    </Page>
  );
}

function TransactionDetailPage({ hash }: { hash: string }) {
  const { i18n, t } = useTranslation();
  const transaction = useTransaction(hash);
  const trace = useTransactionTrace(hash);
  const locale = i18n.resolvedLanguage ?? "en";

  return (
    <Page title={t("page.transaction")} description={hash} mono>
      <QueryNotice loading={transaction.isPending} error={transaction.error} />
      {transaction.data && (
        <>
          {!transaction.data.canonical && (
            <ReorgContext kind="transaction" hash={transaction.data.hash} />
          )}
          <DetailList label={t("detail.transactionSummary")}>
            <Detail label={t("table.hash")} value={transaction.data.hash} mono />
            <Detail label={t("table.status")} value={transactionStatusLabel(transaction.data.status, t)} />
            <Detail
              label={t("table.block")}
              value={transaction.data.block_hash ? (
                <Link to="/blocks/$blockID" params={{ blockID: transaction.data.block_hash }}>
                  {formatInteger(transaction.data.block_number, locale)}
                </Link>
              ) : undefined}
            />
            <Detail
              label={t("detail.blockHash")}
              mono
              value={transaction.data.block_hash ? (
                <Link to="/blocks/$blockID" params={{ blockID: transaction.data.block_hash }}>
                  {transaction.data.block_hash}
                </Link>
              ) : undefined}
            />
            <Detail
              label={t("table.from")}
              mono
              value={(
                <Link to="/address/$address" params={{ address: transaction.data.from }}>
                  {transaction.data.from}
                </Link>
              )}
            />
            <Detail
              label={t("table.to")}
              mono={Boolean(transaction.data.to)}
              value={transaction.data.to ? (
                <Link to="/address/$address" params={{ address: transaction.data.to }}>
                  {transaction.data.to}
                </Link>
              ) : t("common.contractCreation")}
            />
            <Detail label={t("detail.nonce")} value={formatInteger(transaction.data.nonce, locale)} />
            <Detail label={t("table.value")} value={formatInteger(transaction.data.value, locale)} />
            <Detail label={t("detail.gasLimit")} value={formatInteger(transaction.data.gas, locale)} />
            <Detail label={t("detail.gasPrice")} value={formatInteger(transaction.data.gas_price, locale)} />
            <Detail label={t("detail.type")} value={transaction.data.type} />
            <Detail label={t("detail.input")} value={transaction.data.input} mono wide />
            <Detail label={t("detail.canonical")} value={yesNo(transaction.data.canonical, t)} />
            <Detail label={t("table.finality")} value={finalityLabel(transaction.data.finality, t)} />
          </DetailList>
          <CompletenessPanel completeness={transaction.data.completeness} />
        </>
      )}

      <section className="detail-section" aria-labelledby="trace-title">
        <h2 id="trace-title">{t("detail.trace")}</h2>
        <QueryNotice loading={trace.isPending} error={trace.error} />
        {trace.data && trace.data.state !== "complete" && (
          <CapabilityDegraded stage="trace" state={trace.data.state} />
        )}
        {trace.data?.state === "complete" && trace.data.frames.length === 0 && (
          <p className="empty-result">{t("state.noTraceFrames")}</p>
        )}
        {trace.data?.state === "complete" && trace.data.frames.length > 0 && (
          <div className="table-scroll" tabIndex={0} aria-label={t("detail.trace") }>
            <table>
              <caption className="sr-only">{t("detail.traceFrames")}</caption>
              <thead>
                <tr>
                  <th>{t("detail.path")}</th>
                  <th>{t("detail.callType")}</th>
                  <th>{t("table.from")}</th>
                  <th>{t("table.to")}</th>
                  <th>{t("table.value")}</th>
                  <th>{t("table.status")}</th>
                </tr>
              </thead>
              <tbody>
                {trace.data.frames.map((frame) => (
                  <tr key={frame.path.join(".") || "root"}>
                    <td><code>{frame.path.join(".") || "root"}</code></td>
                    <td>{frame.call_type}</td>
                    <td><code>{frame.from ? shorten(frame.from) : "—"}</code></td>
                    <td><code>{frame.to ? shorten(frame.to) : frame.created_address ? shorten(frame.created_address) : "—"}</code></td>
                    <td><code>{formatInteger(frame.value, locale)}</code></td>
                    <td>{frame.reverted ? frame.error ?? t("detail.reverted") : t("detail.succeeded")}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </Page>
  );
}

function AddressDetailPage({ address }: { address: string }) {
  const { i18n, t } = useTranslation();
  const account = useAddress(address);
  const locale = i18n.resolvedLanguage ?? "en";

  return (
    <Page title={t("page.address")} description={address} mono>
      <QueryNotice loading={account.isPending} error={account.error} />
      {account.data && (
        <>
          <DetailList label={t("detail.addressSummary")}>
            <Detail label={t("page.address")} value={account.data.address} mono />
            <Detail label={t("detail.name")} value={account.data.name} />
            <Detail label={t("detail.type")} value={accountTypeLabel(account.data.type, t)} />
            <Detail label={t("detail.balance")} value={formatInteger(account.data.balance, locale)} />
            <Detail label={t("detail.nonce")} value={formatInteger(account.data.nonce, locale)} />
            <Detail
              label={t("detail.atBlock")}
              mono
              value={(
                <Link to="/blocks/$blockID" params={{ blockID: account.data.at_block }}>
                  {account.data.at_block}
                </Link>
              )}
            />
            <Detail label={t("detail.codeHash")} value={account.data.code_hash} mono />
          </DetailList>
          <p className="context-note" role="note">{t("context.addressSnapshot")}</p>
          <CompletenessPanel completeness={account.data.completeness} />
        </>
      )}
    </Page>
  );
}

function TokenDetailPage({ address }: { address: string }) {
  const { i18n, t } = useTranslation();
  const token = useToken(address);
  const transfers = useTokenTransfers(address, 25);
  const locale = i18n.resolvedLanguage ?? "en";

  return (
    <Page title={token.data?.name ?? token.data?.symbol ?? t("page.token")} description={address} mono>
      <QueryNotice loading={token.isPending} error={token.error} />
      {token.data && (
        <DetailList label={t("detail.tokenMetadata")}>
          <Detail label={t("detail.name")} value={token.data.name} />
          <Detail label={t("detail.symbol")} value={token.data.symbol} />
          <Detail label={t("table.standard")} value={token.data.standard} />
          <Detail label={t("table.confidence")} value={token.data.confidence} />
          <Detail label={t("detail.decimals")} value={token.data.decimals?.toString()} />
          <Detail label={t("table.supply")} value={formatInteger(token.data.total_supply, locale)} />
          <Detail label={t("table.metadata")} value={token.data.metadata_state} />
          <Detail label={t("detail.codeHash")} value={token.data.code_hash} mono />
          <Detail label={t("detail.observedBlock")} value={formatInteger(token.data.observed_block_number, locale)} />
        </DetailList>
      )}
      <TokenTransfers events={transfers.data?.items} loading={transfers.isPending} error={transfers.error} locale={locale} />
    </Page>
  );
}

function TokenTransfers({ events, loading, error, locale }: { events?: TokenEvent[]; loading: boolean; error: unknown; locale: string }) {
  const { t } = useTranslation();
  return (
    <section className="detail-section" aria-labelledby="token-transfers-title">
      <h2 id="token-transfers-title">{t("detail.transfers")}</h2>
      <QueryNotice loading={loading} error={error} />
      {events && events.length === 0 && <p className="empty-result">{t("state.noTransfers")}</p>}
      {events && events.length > 0 && (
        <div className="table-scroll" tabIndex={0} aria-label={t("detail.transfers")}>
          <table>
            <caption className="sr-only">{t("detail.tokenTransferHistory")}</caption>
            <thead>
              <tr>
                <th>{t("table.block")}</th>
                <th>{t("table.hash")}</th>
                <th>{t("detail.event")}</th>
                <th>{t("table.from")}</th>
                <th>{t("table.to")}</th>
                <th>{t("detail.tokenID")}</th>
                <th>{t("detail.amount")}</th>
              </tr>
            </thead>
            <tbody>
              {events.map((event) => (
                <tr key={`${event.block_hash}:${event.log_index}:${event.sub_index}`}>
                  <td>{formatInteger(event.block_number, locale)}</td>
                  <td>
                    <Link to="/tx/$hash" params={{ hash: event.transaction_hash }}>
                      {shorten(event.transaction_hash)}
                    </Link>
                  </td>
                  <td>{event.kind}</td>
                  <td><code>{event.from ? shorten(event.from) : "—"}</code></td>
                  <td><code>{event.to ? shorten(event.to) : "—"}</code></td>
                  <td><code>{event.token_id ?? "—"}</code></td>
                  <td><code>{event.amount ?? "—"}</code></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

function NFTDetailPage({ address, tokenID }: { address: string; tokenID: string }) {
  const { i18n, t } = useTranslation();
  const ownership = useNFTOwnership(address, tokenID);
  const locale = i18n.resolvedLanguage ?? "en";

  return (
    <Page title={t("page.nft")} description={`${address} / ${tokenID}`} mono>
      <QueryNotice loading={ownership.isPending} error={ownership.error} />
      {ownership.data && (
        <DetailList label={t("detail.nftOwnership")}>
          <Detail label={t("page.token")} value={ownership.data.token_address} mono />
          <Detail label={t("detail.tokenID")} value={ownership.data.token_id} />
          <Detail label={t("detail.owner")} value={ownership.data.owner} mono />
          <Detail label={t("detail.balance")} value={formatInteger(ownership.data.balance, locale)} />
          <Detail label={t("detail.snapshotBlock")} value={formatInteger(ownership.data.snapshot.block_number, locale)} />
          <Detail label={t("detail.snapshotHash")} value={ownership.data.snapshot.block_hash} mono />
        </DetailList>
      )}
    </Page>
  );
}

type ChainStatusContext = ChainStatus & {
  coverage_start?: string;
  coverage_end?: string;
};

function ChainContextPanel({ status }: { status: ChainStatusContext }) {
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

function ReorgContext({ kind, hash }: { kind: "block" | "transaction"; hash: string }) {
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

function CursorPagination({
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

function DetailList({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <section className="panel detail-card" aria-label={label}>
      <h2>{label}</h2>
      <dl className="detail-grid">{children}</dl>
    </section>
  );
}

function Detail({ label, value, mono, wide }: { label: string; value?: React.ReactNode; mono?: boolean; wide?: boolean }) {
  return (
    <div className={wide ? "detail-item wide" : "detail-item"}>
      <dt>{label}</dt>
      <dd className={mono ? "mono-wrap" : undefined}>{value ?? "—"}</dd>
    </div>
  );
}

function CompletenessPanel({ completeness }: { completeness: Completeness }) {
  const { t } = useTranslation();
  return (
    <section className="panel completeness-card" aria-labelledby="entity-completeness-title">
      <h2 id="entity-completeness-title">{t("detail.completeness")}</h2>
      <ul>
        {Object.entries(completeness).map(([stage, state]) => (
          <li key={stage}>
            <code>{stageLabel(stage, t)}</code>
            <span className={state === "complete" ? "availability yes" : "availability no"}>
              {stageStateLabel(state, t)}
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}

function CapabilityDegraded({ stage, state }: { stage: string; state: string }) {
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

type Translate = ReturnType<typeof useTranslation>["t"];

function finalityLabel(value: string, t: Translate): string {
  switch (value) {
    case "pending": return t("finality.pending");
    case "latest": return t("finality.latest");
    case "safe": return t("finality.safe");
    case "finalized": return t("finality.finalized");
    case "orphan": return t("finality.orphan");
    default: return value;
  }
}

function transactionStatusLabel(value: string | undefined, t: Translate): string {
  switch (value) {
    case "pending": return t("transactionStatus.pending");
    case "success": return t("transactionStatus.success");
    case "failed": return t("transactionStatus.failed");
    case "unknown": return t("transactionStatus.unknown");
    default: return t("common.indexed");
  }
}

function accountTypeLabel(value: string, t: Translate): string {
  switch (value) {
    case "eoa": return t("accountType.eoa");
    case "contract": return t("accountType.contract");
    case "delegated_eoa": return t("accountType.delegatedEoa");
    case "unknown": return t("accountType.unknown");
    default: return t("accountType.unknown");
  }
}

function stageLabel(value: string, t: Translate): string {
  switch (value) {
    case "core": return t("stage.core");
    case "trace": return t("stage.trace");
    case "metadata": return t("stage.metadata");
    case "state": return t("stage.state");
    default: return value;
  }
}

function stageStateLabel(value: string, t: Translate): string {
  switch (value) {
    case "complete": return t("stageState.complete");
    case "pending": return t("stageState.pending");
    case "unavailable": return t("stageState.unavailable");
    case "failed": return t("stageState.failed");
    default: return value;
  }
}

function searchKindLabel(value: SearchResult["kind"], t: Translate): string {
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

function yesNo(value: boolean, t: ReturnType<typeof useTranslation>["t"]): string {
  return value ? t("common.yes") : t("common.no");
}

const MAX_STANDARD_JSON_BYTES = 5 * 1024 * 1024;
const HASH_PATTERN = /^0x[0-9a-fA-F]{64}$/;
const QUANTITY_PATTERN = /^(0|[1-9][0-9]*)$/;

export function ChartsPage() {
  const { i18n, t } = useTranslation();
  const status = useChainStatus();
  const [draftFrom, setDraftFrom] = useState("");
  const [draftTo, setDraftTo] = useState("");
  const [range, setRange] = useState<{ from: string; to: string }>();
  const [rangeError, setRangeError] = useState<string>();
  const stats = useBlockStats(range?.from ?? "", range?.to ?? "", Boolean(range));
  const locale = i18n.resolvedLanguage ?? "en";

  useEffect(() => {
    if (!status.data || range || draftFrom || draftTo) return;
    const to = BigInt(status.data.indexed_block);
    const from = to > 99n ? to - 99n : 0n;
    const next = { from: from.toString(), to: to.toString() };
    setDraftFrom(next.from);
    setDraftTo(next.to);
    setRange(next);
  }, [draftFrom, draftTo, range, status.data]);

  const submitRange = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setRangeError(undefined);
    if (!QUANTITY_PATTERN.test(draftFrom) || !QUANTITY_PATTERN.test(draftTo)) {
      setRangeError(t("charts.invalidRange"));
      return;
    }
    const from = BigInt(draftFrom);
    const to = BigInt(draftTo);
    if (from > to || to - from + 1n > 5_000n) {
      setRangeError(t("charts.invalidRange"));
      return;
    }
    setRange({ from: draftFrom, to: draftTo });
  };

  return (
    <Page title={t("page.charts")} description={t("page.chartsDescription")}>
      <form className="panel range-form" onSubmit={submitRange}>
        <label htmlFor="chart-from-block">{t("charts.fromBlock")}</label>
        <input
          id="chart-from-block"
          inputMode="numeric"
          onChange={(event) => setDraftFrom(event.target.value)}
          pattern="[0-9]*"
          value={draftFrom}
        />
        <label htmlFor="chart-to-block">{t("charts.toBlock")}</label>
        <input
          id="chart-to-block"
          inputMode="numeric"
          onChange={(event) => setDraftTo(event.target.value)}
          pattern="[0-9]*"
          value={draftTo}
        />
        <button className="button primary" type="submit">{t("charts.load")}</button>
      </form>
      {rangeError && <p className="form-error" role="alert">{rangeError}</p>}
      <QueryNotice loading={status.isPending && !range} error={status.error} />
      <QueryNotice loading={stats.isPending && Boolean(range)} error={stats.error} />
      {stats.data?.length === 0 && <p className="empty-result">{t("charts.empty")}</p>}
      {stats.data && stats.data.length > 0 && (
        <section className="panel chart-panel" aria-labelledby="block-stats-title">
          <h2 id="block-stats-title">{t("charts.title")}</h2>
          <Suspense fallback={<div className="stats-chart chart-loading" aria-hidden="true" />}>
            <StatsChart data={stats.data} />
          </Suspense>
          <BlockStatsTable data={stats.data} locale={locale} />
        </section>
      )}
    </Page>
  );
}

function BlockStatsTable({ data, locale }: { data: BlockStat[]; locale: string }) {
  const { t } = useTranslation();
  return (
    <div className="table-scroll chart-table" tabIndex={0} aria-label={t("charts.tableLabel")}>
      <table>
        <caption>{t("charts.tableFallback")}</caption>
        <thead>
          <tr>
            <th>{t("table.block")}</th>
            <th>{t("charts.transactions")}</th>
            <th>{t("charts.gasUsed")}</th>
            <th>{t("charts.baseFee")}</th>
            <th>{t("charts.burned")}</th>
          </tr>
        </thead>
        <tbody>
          {data.map((item) => (
            <tr key={item.block_hash}>
              <td>{formatInteger(item.block_number, locale)}</td>
              <td><code>{item.transaction_count}</code></td>
              <td><code>{item.gas_used}</code></td>
              <td><code>{item.base_fee_per_gas ?? "—"}</code></td>
              <td><code>{item.burned_wei ?? "—"}</code></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function VerifyPage() {
  const { t } = useTranslation();
  const publicConfig = usePublicConfig();
  const [apiKey, setAPIKey] = useState("");
  const [address, setAddress] = useState("");
  const [language, setLanguage] = useState<VerificationSubmission["language"]>("solidity");
  const [compilerVersion, setCompilerVersion] = useState("");
  const [contractIdentifier, setContractIdentifier] = useState("");
  const [constructorArguments, setConstructorArguments] = useState("");
  const [standardJSON, setStandardJSON] = useState('{\n  "language": "Solidity",\n  "sources": {},\n  "settings": {}\n}');
  const [submitToSourcify, setSubmitToSourcify] = useState(false);
  const [formError, setFormError] = useState<string>();
  const submission = useSubmitVerification(apiKey);
  const job = useVerificationJob(submission.data?.id ?? "", apiKey, Boolean(submission.data));
  const currentJob = job.data ?? submission.data;
  const verificationDisabled = publicConfig.data?.features.verification === false;

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setFormError(undefined);
    submission.reset();

    if (new TextEncoder().encode(standardJSON).byteLength > MAX_STANDARD_JSON_BYTES) {
      setFormError(t("verification.inputTooLarge"));
      return;
    }

    let parsed: unknown;
    try {
      parsed = JSON.parse(standardJSON) as unknown;
    } catch {
      setFormError(t("verification.invalidJSON"));
      return;
    }
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
      setFormError(t("verification.invalidJSONObject"));
      return;
    }
    if (
      !apiKey ||
      !isAddress(address) ||
      !compilerVersion.trim() ||
      !contractIdentifier.trim() ||
      !/^(?:0x)?[0-9a-fA-F]*$/.test(constructorArguments)
    ) {
      setFormError(t("verification.invalidFields"));
      return;
    }

    submission.mutate({
      address: getAddress(address),
      compiler_version: compilerVersion.trim(),
      constructor_arguments: constructorArguments || undefined,
      contract_identifier: contractIdentifier.trim(),
      language,
      standard_json: parsed as Record<string, unknown>,
      submit_to_sourcify: submitToSourcify,
    });
  };

  return (
    <Page title={t("page.verify")} description={t("page.verifyDescription")}>
      <QueryNotice loading={publicConfig.isPending} error={publicConfig.error} />
      {verificationDisabled ? (
        <UnavailablePanel title={t("verification.unavailable")} detail={t("verification.unavailableDetail")} />
      ) : (
        <div className="verification-layout">
          <form className="panel verification-form" autoComplete="off" onSubmit={submit}>
            <h2>{t("verification.request")}</h2>
            <p className="quiet">{t("verification.securityNotice")}</p>
            <div className="form-grid">
              <FormField id="verification-address" label={t("page.address")} value={address} onChange={setAddress} />
              <label className="field-control" htmlFor="verification-language">
                <span>{t("verification.language")}</span>
                <select id="verification-language" value={language} onChange={(event) => setLanguage(event.target.value as VerificationSubmission["language"])}>
                  <option value="solidity">Solidity</option>
                  <option value="vyper">Vyper</option>
                </select>
              </label>
              <FormField id="verification-compiler" label={t("verification.compilerVersion")} value={compilerVersion} onChange={setCompilerVersion} />
              <FormField id="verification-contract" label={t("verification.contractIdentifier")} value={contractIdentifier} onChange={setContractIdentifier} />
              <FormField id="verification-constructor" label={t("verification.constructorArguments")} value={constructorArguments} onChange={setConstructorArguments} wide />
              <label className="field-control wide" htmlFor="verification-standard-json">
                <span>{t("verification.standardJSON")}</span>
                <textarea id="verification-standard-json" spellCheck={false} value={standardJSON} onChange={(event) => setStandardJSON(event.target.value)} />
                <small>{t("verification.sizeLimit")}</small>
              </label>
              <label className="field-control wide" htmlFor="verification-api-key">
                <span>{t("verification.apiKey")}</span>
                <input
                  autoComplete="off"
                  id="verification-api-key"
                  name="verification-api-key"
                  onChange={(event) => setAPIKey(event.target.value)}
                  spellCheck={false}
                  type="password"
                  value={apiKey}
                />
                <small>{t("verification.apiKeyNotice")}</small>
              </label>
              {publicConfig.data?.features.sourcify && (
                <label className="checkbox-control wide">
                  <input checked={submitToSourcify} onChange={(event) => setSubmitToSourcify(event.target.checked)} type="checkbox" />
                  <span>{t("verification.sourcifyConsent")}</span>
                </label>
              )}
            </div>
            {(formError || submission.error) && (
              <p className="form-error" role="alert">{formError ?? errorMessage(submission.error, t("verification.submitFailed"))}</p>
            )}
            <button className="button primary" disabled={submission.isPending} type="submit">
              {submission.isPending ? t("verification.submitting") : t("verification.submit")}
            </button>
          </form>
          <VerificationJobPanel job={currentJob} loading={job.isPending && Boolean(submission.data)} error={job.error} />
        </div>
      )}
    </Page>
  );
}

function FormField({ id, label, value, onChange, wide }: { id: string; label: string; value: string; onChange: (value: string) => void; wide?: boolean }) {
  return (
    <label className={wide ? "field-control wide" : "field-control"} htmlFor={id}>
      <span>{label}</span>
      <input id={id} onChange={(event) => onChange(event.target.value)} spellCheck={false} value={value} />
    </label>
  );
}

function VerificationJobPanel({ job, loading, error }: { job?: VerificationJob; loading: boolean; error: unknown }) {
  const { t } = useTranslation();
  return (
    <section className="panel job-panel" aria-labelledby="verification-job-title">
      <h2 id="verification-job-title">{t("verification.job")}</h2>
      {!job && !loading && !error && <p className="quiet">{t("verification.jobEmpty")}</p>}
      <QueryNotice loading={loading} error={error} />
      {job && (
        <dl className="job-details" aria-live="polite">
          <div><dt>{t("verification.jobID")}</dt><dd><code>{job.id}</code></dd></div>
          <div><dt>{t("table.status")}</dt><dd><span className={`job-status ${job.status}`}>{job.status}</span></dd></div>
          <div><dt>{t("verification.result")}</dt><dd>{job.result_kind ?? "—"}</dd></div>
          <div><dt>{t("verification.runtimeMatch")}</dt><dd>{job.runtime_match ?? "—"}</dd></div>
          <div><dt>{t("verification.creationMatch")}</dt><dd>{job.creation_match ?? "—"}</dd></div>
          <div><dt>{t("verification.published")}</dt><dd>{job.published === undefined ? "—" : String(job.published)}</dd></div>
          <div><dt>{t("verification.errorCode")}</dt><dd><code>{job.error_code ?? "—"}</code></dd></div>
          <div><dt>{t("verification.updated")}</dt><dd>{job.updated_at}</dd></div>
        </dl>
      )}
    </section>
  );
}

export function ContractsPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [address, setAddress] = useState("");
  const [codeHash, setCodeHash] = useState("");
  const [error, setError] = useState<string>();

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError(undefined);
    if (!isAddress(address) || !HASH_PATTERN.test(codeHash)) {
      setError(t("contracts.invalidIdentity"));
      return;
    }
    void navigate({
      to: "/contract/$address",
      params: { address: getAddress(address) },
      search: { code_hash: codeHash },
    });
  };

  return (
    <Page title={t("page.contracts")} description={t("page.contractsDescription")}>
      <form className="panel contract-lookup" onSubmit={submit}>
        <h2>{t("contracts.lookup")}</h2>
        <p className="quiet">{t("contracts.codeHashIdentity")}</p>
        <FormField id="contract-address-lookup" label={t("page.address")} value={address} onChange={setAddress} />
        <FormField id="contract-code-hash-lookup" label={t("detail.codeHash")} value={codeHash} onChange={setCodeHash} />
        {error && <p className="form-error" role="alert">{error}</p>}
        <button className="button primary" type="submit">{t("contracts.open")}</button>
      </form>
      <Link className="button secondary inline-button" to="/verify">{t("contracts.submitVerification")}</Link>
    </Page>
  );
}

function UnavailablePanel({ title, detail }: { title: string; detail: string }) {
  return (
    <section className="capability-panel" role="status">
      <span className="capability-mark" aria-hidden="true">!</span>
      <div><h2>{title}</h2><p>{detail}</p></div>
    </section>
  );
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
          <section className="panel status-card">
            <span>{publicConfig.data?.chain_name ?? t("app.tagline")}</span>
            <strong>{t("common.chain")} {status.data.chain_id}</strong>
            <dl>
              <div><dt>{t("home.indexed")}</dt><dd>{formatInteger(status.data.indexed_block, locale)}</dd></div>
              <div>
                <dt>{t("home.highestCovered")}</dt>
                <dd>{formatInteger(status.data.highest_covered_block, locale)}</dd>
              </div>
              <div><dt>{t("home.networkHead")}</dt><dd>{formatInteger(status.data.latest_block, locale)}</dd></div>
              <div><dt>{t("home.finality")}</dt><dd>{formatInteger(status.data.finalized_block, locale)}</dd></div>
              <div>
                <dt>{t("home.backfill")}</dt>
                <dd>{status.data.backfill_complete ? t("home.backfillComplete") : t("home.backfillIncomplete")}</dd>
              </div>
            </dl>
          </section>
          <section className="panel capability-list">
            <h2>{t("common.capabilities")}</h2>
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
        <strong>{result.label}</strong>
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
      return <Link className="search-result" to="/tx/$hash" params={{ hash: result.key }}>{content}</Link>;
    case "address":
      return <Link className="search-result" to="/address/$address" params={{ address: result.key }}>{content}</Link>;
    case "contract":
      return <Link className="search-result" to="/contract/$address" params={{ address: result.key }} search={{ code_hash: "" }}>{content}</Link>;
    case "token":
      return <Link className="search-result" to="/token/$address" params={{ address: result.key }}>{content}</Link>;
    default:
      return <a className="search-result" href={`/search?q=${encodeURIComponent(result.key)}`}>{content}</a>;
  }
}

export function ContractPage({ address, codeHash }: { address: string; codeHash: string }) {
  const { t } = useTranslation();
  const publicConfig = usePublicConfig();
  const [codeHashInput, setCodeHashInput] = useState(codeHash);
  const [apiKey, setAPIKey] = useState("");
  const [submittedCodeHash, setSubmittedCodeHash] = useState("");
  const [submittedAPIKey, setSubmittedAPIKey] = useState("");
  const [requestRevision, setRequestRevision] = useState(0);
  const [formError, setFormError] = useState<string>();
  const validAddress = isAddress(address);
  const verificationDisabled = publicConfig.data?.features.verification === false;
  const contract = useVerifiedContract(
    address,
    submittedCodeHash,
    submittedAPIKey,
    requestRevision,
    validAddress && !verificationDisabled,
  );

  useEffect(() => setCodeHashInput(codeHash), [codeHash]);

  const loadVerification = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setFormError(undefined);
    if (!validAddress || !HASH_PATTERN.test(codeHashInput) || !apiKey) {
      setFormError(t("contracts.invalidRequest"));
      return;
    }
    setSubmittedCodeHash(codeHashInput);
    setSubmittedAPIKey(apiKey);
    setRequestRevision((current) => current + 1);
  };

  return (
    <Page title={t("page.contract")} description={address} mono>
      <div className="contract-detail-stack">
        <QueryNotice loading={publicConfig.isPending} error={publicConfig.error} />
        {verificationDisabled ? (
          <UnavailablePanel title={t("verification.unavailable")} detail={t("verification.unavailableDetail")} />
        ) : (
          <section className="panel verified-contract-card" aria-labelledby="verified-contract-title">
            <h2 id="verified-contract-title">{t("contracts.verifiedArtifact")}</h2>
            <form className="contract-query-form" autoComplete="off" onSubmit={loadVerification}>
              <label className="field-control" htmlFor="contract-detail-code-hash">
                <span>{t("detail.codeHash")}</span>
                <input id="contract-detail-code-hash" onChange={(event) => setCodeHashInput(event.target.value)} spellCheck={false} value={codeHashInput} />
              </label>
              <label className="field-control" htmlFor="contract-detail-api-key">
                <span>{t("verification.apiKey")}</span>
                <input
                  autoComplete="off"
                  id="contract-detail-api-key"
                  name="contract-detail-api-key"
                  onChange={(event) => setAPIKey(event.target.value)}
                  spellCheck={false}
                  type="password"
                  value={apiKey}
                />
              </label>
              <button className="button primary" type="submit">{t("contracts.load")}</button>
            </form>
            <p className="quiet api-key-note">{t("verification.apiKeyNotice")}</p>
            {formError && <p className="form-error" role="alert">{formError}</p>}
            <QueryNotice loading={contract.isPending && requestRevision > 0} error={contract.error} />
            {contract.data && <VerifiedContractView contract={contract.data} />}
          </section>
        )}
        <ContractWorkbench address={address} />
      </div>
    </Page>
  );
}

function VerifiedContractView({ contract }: { contract: VerifiedContract }) {
  const { t } = useTranslation();
  return (
    <div className="verified-artifacts">
      <DetailList label={t("contracts.identity") }>
        <Detail label={t("contracts.contractName")} value={contract.contract_name} />
        <Detail label={t("verification.language")} value={contract.language} />
        <Detail label={t("verification.compilerVersion")} value={contract.compiler_version} />
        <Detail label={t("contracts.matchKind")} value={contract.match_kind} />
        <Detail label={t("detail.codeHash")} value={contract.code_hash} mono />
        <Detail label={t("contracts.validBlocks")} value={`${contract.valid_from_block} – ${contract.valid_to_block ?? "∞"}`} />
      </DetailList>
      <TextArtifact title={t("contracts.abi")} value={contract.abi} />
      <TextArtifact title={t("contracts.sources")} value={contract.sources} />
      <TextArtifact title={t("contracts.settings")} value={contract.settings} />
    </div>
  );
}

function TextArtifact({ title, value }: { title: string; value: unknown }) {
  return (
    <section className="artifact-panel">
      <h3>{title}</h3>
      <pre tabIndex={0}>{JSON.stringify(value, null, 2)}</pre>
    </section>
  );
}

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

function ContractWorkbench({ address }: { address: string }) {
  const { t } = useTranslation();
  const status = useChainStatus();
  const wallet = useWallet();
  const [calldata, setCalldata] = useState("0x");
  const [value, setValue] = useState("");
  const [result, setResult] = useState<Hex>();
  const [error, setError] = useState<string>();
  const expectedChainID = status.data?.chain_id;
  const validAddress = isAddress(address);
  const chainReady = wallet.active && chainsMatch(wallet.active.chainID, expectedChainID);
  const ready = Boolean(validAddress && chainReady && expectedChainID);

  const chainMessage = useMemo(() => {
    if (!expectedChainID) return t("wallet.chainUnknown");
    if (wallet.active && !chainsMatch(wallet.active.chainID, expectedChainID)) {
      return t("wallet.wrongChain", { expected: expectedChainID, actual: wallet.active.chainID });
    }
    return undefined;
  }, [expectedChainID, t, wallet.active]);

  const read = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError(undefined);
    setResult(undefined);
    if (!validAddress || !isHex(calldata)) {
      setError("Address or calldata is invalid");
      return;
    }
    try {
      const output = await wallet.readContract(
        { to: getAddress(address), data: calldata },
        expectedChainID,
      );
      setResult(output);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Contract call failed");
    }
  };

  const write = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError(undefined);
    setResult(undefined);
    if (!validAddress || !isHex(calldata) || (value !== "" && !/^\d+$/.test(value))) {
      setError("Address, calldata or value is invalid");
      return;
    }
    try {
      const hash = await wallet.sendTransaction(
        {
          to: getAddress(address),
          data: calldata,
          ...(value === "" ? {} : { value: toHex(BigInt(value)) }),
        },
        expectedChainID,
      );
      setResult(hash);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Transaction request failed");
    }
  };

  return (
    <section className="panel contract-workbench" aria-labelledby="wallet-workbench-title">
      <div className="panel-heading">
        <div>
          <span className="eyebrow">{t("common.walletBoundary")}</span>
          <h2 id="wallet-workbench-title">{t("wallet.title")}</h2>
        </div>
        <span className={ready ? "availability yes" : "availability no"}>
          {ready ? t("wallet.connected") : t("actions.connect")}
        </span>
      </div>
      <p className="wallet-notice">{t("wallet.directNotice")}</p>
      {chainMessage && <p className="chain-warning" role="status">{chainMessage}</p>}
      <form className="contract-form" onSubmit={read}>
        <label htmlFor="contract-calldata">{t("wallet.calldata")}</label>
        <textarea
          id="contract-calldata"
          spellCheck={false}
          value={calldata}
          onChange={(event) => setCalldata(event.target.value)}
        />
        <button className="button primary" disabled={!ready} type="submit">
          {t("actions.read")}
        </button>
      </form>
      <form className="contract-form write-form" onSubmit={write}>
        <label htmlFor="transaction-value">{t("wallet.value")}</label>
        <input
          id="transaction-value"
          inputMode="numeric"
          pattern="[0-9]*"
          value={value}
          onChange={(event) => setValue(event.target.value)}
        />
        <button className="button secondary" disabled={!ready} type="submit">
          {t("actions.write")}
        </button>
      </form>
      {error && <p className="form-error" role="alert">{error}</p>}
      {result && (
        <div className="call-result" role="status">
          <span>{t("wallet.result")}</span>
          <code>{result}</code>
        </div>
      )}
    </section>
  );
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
  description: string;
  children: React.ReactNode;
  mono?: boolean;
}) {
  return (
    <div className="page-stack inner-page">
      <header className="page-header">
        <h1>{title}</h1>
        <p className={mono ? "mono-wrap" : undefined}>{description}</p>
      </header>
      {children}
    </div>
  );
}
