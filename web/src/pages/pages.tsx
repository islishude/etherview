import {
  FormEvent,
  lazy,
  Suspense,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { getAddress, isAddress, toHex, type Hex } from "viem";

import {
  useAddressNFTBalances,
  useAggregateStats,
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
  AggregateStats,
  BlockStat,
  BlockSummary,
  ChainStatus,
  Completeness,
  NFTBalance,
  SearchResult,
  TokenEvent,
  TransactionSummary,
  VerificationJob,
  VerificationSubmission,
  VerifiedContract,
} from "@/api/types";
import { formatInteger, formatTimestamp, shorten } from "@/components/format";
import { QueryNotice } from "@/components/QueryNotice";
import {
  chainsMatch,
  isContractCalldata,
  MAX_CONTRACT_CALLDATA_BYTES,
  WalletBoundaryError,
  walletErrorTranslationKey,
} from "@/wallet/eip6963";
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
  const nftPager = useCursorHistory(`address-nfts:${address}`);
  const account = useAddress(address);
  const nfts = useAddressNFTBalances(
    address,
    nftPager.cursor,
    CORE_PAGE_SIZE,
    nftPager.refreshGeneration,
    isAddress(address),
  );
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
            <Detail
              label={t("detail.codeHash")}
              mono
              value={account.data.code_hash ? (
                <Link
                  to="/contract/$address"
                  params={{ address: account.data.address }}
                  search={{ code_hash: account.data.code_hash }}
                >
                  {account.data.code_hash}
                </Link>
              ) : undefined}
            />
          </DetailList>
          <p className="context-note" role="note">{t("context.addressSnapshot")}</p>
          <CompletenessPanel completeness={account.data.completeness} />
        </>
      )}
      <AddressNFTBalances
        balances={nfts.data?.items}
        busy={nfts.isFetching}
        coverageEnd={nfts.data?.meta.coverage_end}
        error={nfts.error}
        hasNext={Boolean(nfts.data?.next_cursor)}
        loading={nfts.isPending}
        locale={locale}
        onNext={() => nftPager.next(nfts.data?.next_cursor)}
        onPrevious={nftPager.previous}
        onReset={nftPager.reset}
        page={nftPager.page}
        hasPrevious={nftPager.hasPrevious}
      />
    </Page>
  );
}

function AddressNFTBalances({
  balances,
  busy,
  coverageEnd,
  error,
  hasNext,
  hasPrevious,
  loading,
  locale,
  onNext,
  onPrevious,
  onReset,
  page,
}: {
  balances?: NFTBalance[];
  busy: boolean;
  coverageEnd?: string;
  error: unknown;
  hasNext: boolean;
  hasPrevious: boolean;
  loading: boolean;
  locale: string;
  onNext: () => void;
  onPrevious: () => void;
  onReset: () => void;
  page: number;
}) {
  const { t } = useTranslation();
  return (
    <section className="detail-section" aria-labelledby="nft-balances-title">
      <h2 id="nft-balances-title">{t("detail.nftBalances")}</h2>
      {coverageEnd && (
        <p className="context-note" role="note">
          {t("detail.nftSnapshot", { block: formatInteger(coverageEnd, locale) })}
        </p>
      )}
      <QueryNotice loading={loading} error={error} onReset={onReset} />
      {balances && balances.length === 0 && (
        <p className="empty-result" role="status">{t("state.noNFTBalances")}</p>
      )}
      {balances && balances.length > 0 && (
        <div className="table-scroll" tabIndex={0} aria-label={t("detail.nftBalances")}>
          <table>
            <caption className="sr-only">{t("detail.nftBalanceDescription")}</caption>
            <thead>
              <tr>
                <th>{t("table.token")}</th>
                <th>{t("detail.tokenID")}</th>
                <th>{t("detail.balance")}</th>
                <th>{t("table.confidence")}</th>
              </tr>
            </thead>
            <tbody>
              {balances.map((balance) => (
                <tr key={`${balance.token_address}:${balance.token_id}`}>
                  <td>
                    <Link to="/token/$address" params={{ address: balance.token_address }}>
                      <code>{shorten(balance.token_address)}</code>
                    </Link>
                  </td>
                  <td><code>{balance.token_id}</code></td>
                  <td><code>{balance.balance}</code></td>
                  <td><span className="result-kind">{confidenceLabel(balance.confidence, t)}</span></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {balances && (
        <CursorPagination
          busy={busy}
          hasNext={hasNext}
          hasPrevious={hasPrevious}
          label={t("pagination.nfts")}
          onNext={onNext}
          onPrevious={onPrevious}
          page={page}
        />
      )}
    </section>
  );
}

function TokenDetailPage({ address }: { address: string }) {
  const { i18n, t } = useTranslation();
  const transferPager = useCursorHistory(`token-transfers:${address}`);
  const token = useToken(address);
  const transfers = useTokenTransfers(
    address,
    CORE_PAGE_SIZE,
    transferPager.cursor,
    transferPager.refreshGeneration,
  );
  const locale = i18n.resolvedLanguage ?? "en";

  return (
    <Page title={token.data?.name ?? token.data?.symbol ?? t("page.token")} description={address} mono>
      <QueryNotice loading={token.isPending} error={token.error} />
      {token.data && (
        <DetailList label={t("detail.tokenMetadata")}>
          <Detail label={t("detail.name")} value={token.data.name} />
          <Detail label={t("detail.symbol")} value={token.data.symbol} />
          <Detail label={t("table.standard")} value={tokenStandardLabel(token.data.standard, t)} />
          <Detail label={t("table.confidence")} value={confidenceLabel(token.data.confidence, t)} />
          <Detail label={t("detail.decimals")} value={token.data.decimals?.toString()} />
          <Detail label={t("table.supply")} value={formatInteger(token.data.total_supply, locale)} />
          <Detail label={t("table.metadata")} value={stageStateLabel(token.data.metadata_state, t)} />
          <Detail
            label={t("detail.codeHash")}
            mono
            value={(
              <Link
                to="/contract/$address"
                params={{ address: token.data.address }}
                search={{ code_hash: token.data.code_hash }}
              >
                {token.data.code_hash}
              </Link>
            )}
          />
          <Detail label={t("detail.observedBlock")} value={formatInteger(token.data.observed_block_number, locale)} />
          <Detail
            label={t("detail.observedBlockHash")}
            mono
            value={(
              <Link
                to="/blocks/$blockID"
                params={{ blockID: token.data.observed_block_hash }}
              >
                {token.data.observed_block_hash}
              </Link>
            )}
          />
        </DetailList>
      )}
      <TokenTransfers
        busy={transfers.isFetching}
        error={transfers.error}
        events={transfers.data?.items}
        hasNext={Boolean(transfers.data?.next_cursor)}
        hasPrevious={transferPager.hasPrevious}
        loading={transfers.isPending}
        locale={locale}
        onNext={() => transferPager.next(transfers.data?.next_cursor)}
        onPrevious={transferPager.previous}
        onReset={transferPager.reset}
        page={transferPager.page}
      />
    </Page>
  );
}

function TokenTransfers({
  busy,
  error,
  events,
  hasNext,
  hasPrevious,
  loading,
  locale,
  onNext,
  onPrevious,
  onReset,
  page,
}: {
  busy: boolean;
  error: unknown;
  events?: TokenEvent[];
  hasNext: boolean;
  hasPrevious: boolean;
  loading: boolean;
  locale: string;
  onNext: () => void;
  onPrevious: () => void;
  onReset: () => void;
  page: number;
}) {
  const { t } = useTranslation();
  return (
    <section className="detail-section" aria-labelledby="token-events-title">
      <h2 id="token-events-title">{t("detail.tokenEvents")}</h2>
      <QueryNotice loading={loading} error={error} onReset={onReset} />
      {events && events.length === 0 && (
        <p className="empty-result" role="status">{t("state.noTransfers")}</p>
      )}
      {events && events.length > 0 && (
        <div className="table-scroll" tabIndex={0} aria-label={t("detail.tokenEvents")}>
          <table>
            <caption className="sr-only">{t("detail.tokenEventHistory")}</caption>
            <thead>
              <tr>
                <th>{t("table.block")}</th>
                <th>{t("table.hash")}</th>
                <th>{t("detail.event")}</th>
                <th>{t("table.from")}</th>
                <th>{t("table.to")}</th>
                <th>{t("detail.operator")}</th>
                <th>{t("detail.tokenID")}</th>
                <th>{t("detail.amount")}</th>
                <th>{t("table.standard")}</th>
                <th>{t("table.confidence")}</th>
              </tr>
            </thead>
            <tbody>
              {events.map((event) => (
                <tr key={`${event.block_hash}:${event.log_index}:${event.sub_index}`}>
                  <td>
                    <span className="table-primary">
                      <Link to="/blocks/$blockID" params={{ blockID: event.block_hash }}>
                        {formatInteger(event.block_number, locale)}
                      </Link>
                      <code title={event.block_hash}>{shorten(event.block_hash)}</code>
                    </span>
                  </td>
                  <td>
                    <Link to="/tx/$hash" params={{ hash: event.transaction_hash }}>
                      {shorten(event.transaction_hash)}
                    </Link>
                  </td>
                  <td>{tokenEventKindLabel(event.kind, t)}</td>
                  <td>{event.from ? (
                    <Link to="/address/$address" params={{ address: event.from }}>
                      <code title={event.from}>{shorten(event.from)}</code>
                    </Link>
                  ) : "—"}</td>
                  <td>{event.to ? (
                    <Link to="/address/$address" params={{ address: event.to }}>
                      <code title={event.to}>{shorten(event.to)}</code>
                    </Link>
                  ) : "—"}</td>
                  <td>{event.operator ? (
                    <Link to="/address/$address" params={{ address: event.operator }}>
                      <code title={event.operator}>{shorten(event.operator)}</code>
                    </Link>
                  ) : "—"}</td>
                  <td>
                    {event.token_id && event.standard === "erc721" ? (
                      <Link
                        to="/nft/$address/$tokenID"
                        params={{ address: event.token_address, tokenID: event.token_id }}
                      >
                        <code>{event.token_id}</code>
                      </Link>
                    ) : <code>{event.token_id ?? "—"}</code>}
                  </td>
                  <td><code>{event.amount ?? "—"}</code></td>
                  <td>{tokenStandardLabel(event.standard, t)}</td>
                  <td>{confidenceLabel(event.confidence, t)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {events && (
        <CursorPagination
          busy={busy}
          hasNext={hasNext}
          hasPrevious={hasPrevious}
          label={t("pagination.tokenEvents")}
          onNext={onNext}
          onPrevious={onPrevious}
          page={page}
        />
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
          <Detail
            label={t("page.token")}
            mono
            value={(
              <Link to="/token/$address" params={{ address: ownership.data.token_address }}>
                {ownership.data.token_address}
              </Link>
            )}
          />
          <Detail label={t("detail.tokenID")} value={ownership.data.token_id} />
          <Detail
            label={t("detail.owner")}
            mono
            value={(
              <Link to="/address/$address" params={{ address: ownership.data.owner }}>
                {ownership.data.owner}
              </Link>
            )}
          />
          <Detail label={t("detail.balance")} value={ownership.data.balance} />
          <Detail label={t("table.confidence")} value={confidenceLabel(ownership.data.confidence, t)} />
          <Detail label={t("detail.snapshotBlock")} value={formatInteger(ownership.data.snapshot.block_number, locale)} />
          <Detail
            label={t("detail.snapshotHash")}
            mono
            value={(
              <Link
                to="/blocks/$blockID"
                params={{ blockID: ownership.data.snapshot.block_hash }}
              >
                {ownership.data.snapshot.block_hash}
              </Link>
            )}
          />
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

function stageStateLabel(value: string, t: Translate): string {
  switch (value) {
    case "complete": return t("stageState.complete");
    case "pending": return t("stageState.pending");
    case "unavailable": return t("stageState.unavailable");
    case "failed": return t("stageState.failed");
    default: return value;
  }
}

function tokenStandardLabel(value: string, t: Translate): string {
  switch (value) {
    case "erc20": return t("tokenStandard.erc20");
    case "erc721": return t("tokenStandard.erc721");
    case "erc1155": return t("tokenStandard.erc1155");
    default: return t("tokenStandard.unknown");
  }
}

function confidenceLabel(value: string, t: Translate): string {
  switch (value) {
    case "verified": return t("confidence.verified");
    case "high": return t("confidence.high");
    case "inferred": return t("confidence.inferred");
    case "guess": return t("confidence.guess");
    case "rpc_exact": return t("confidence.rpcExact");
    default: return value;
  }
}

function tokenEventKindLabel(value: string, t: Translate): string {
  switch (value) {
    case "transfer": return t("tokenEvent.transfer");
    case "mint": return t("tokenEvent.mint");
    case "burn": return t("tokenEvent.burn");
    case "approval": return t("tokenEvent.approval");
    case "approval_for_all": return t("tokenEvent.approvalForAll");
    default: return value;
  }
}

function featureLabel(value: string, t: Translate): string {
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

function verificationJobStatusLabel(value: VerificationJob["status"], t: Translate): string {
  switch (value) {
    case "queued": return t("verificationStatus.queued");
    case "running": return t("verificationStatus.running");
    case "succeeded": return t("verificationStatus.succeeded");
    case "failed": return t("verificationStatus.failed");
    case "cancelled": return t("verificationStatus.cancelled");
  }
}

function verificationMatchLabel(value: string | undefined, t: Translate): string {
  switch (value) {
    case "exact": return t("verificationMatch.exact");
    case "metadata_only": return t("verificationMatch.metadataOnly");
    case "mismatch": return t("verificationMatch.mismatch");
    default: return "—";
  }
}

function verificationLanguageLabel(value: string, t: Translate): string {
  switch (value) {
    case "solidity": return t("verificationLanguage.solidity");
    case "vyper": return t("verificationLanguage.vyper");
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
const UUID_PATTERN = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;

export function ChartsPage() {
  const { i18n, t } = useTranslation();
  const status = useChainStatus();
  const [draftFrom, setDraftFrom] = useState("");
  const [draftTo, setDraftTo] = useState("");
  const [range, setRange] = useState<{ from: string; to: string }>();
  const [rangeError, setRangeError] = useState<string>();
  const stats = useBlockStats(range?.from ?? "", range?.to ?? "", Boolean(range));
  const aggregate = useAggregateStats(
    range?.from ?? "",
    range?.to ?? "",
    Boolean(range),
  );
  const locale = i18n.resolvedLanguage ?? "en";
  const coverageNotStarted = status.data?.coverage_start !== undefined &&
    BigInt(status.data.indexed_block) < BigInt(status.data.coverage_start);

  useEffect(() => {
    if (!status.data || range || draftFrom || draftTo || coverageNotStarted) return;
    const to = BigInt(status.data.indexed_block);
    const configuredStart = status.data.coverage_start
      ? BigInt(status.data.coverage_start)
      : 0n;
    const candidate = to > 99n ? to - 99n : 0n;
    const from = candidate < configuredStart ? configuredStart : candidate;
    const next = { from: from.toString(), to: to.toString() };
    setDraftFrom(next.from);
    setDraftTo(next.to);
    setRange(next);
  }, [coverageNotStarted, draftFrom, draftTo, range, status.data]);

  const submitRange = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setRangeError(undefined);
    if (!QUANTITY_PATTERN.test(draftFrom) || !QUANTITY_PATTERN.test(draftTo)) {
      setRangeError(t("charts.invalidRange"));
      return;
    }
    const from = BigInt(draftFrom);
    const to = BigInt(draftTo);
    const configuredStart = status.data?.coverage_start
      ? BigInt(status.data.coverage_start)
      : 0n;
    if (from > to || from < configuredStart || to - from + 1n > 5_000n) {
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
      {coverageNotStarted && (
        <UnavailablePanel
          title={t("state.coreNotReady")}
          detail={t("charts.coverageNotStarted", {
            start: status.data?.coverage_start,
          })}
        />
      )}
      <QueryNotice loading={stats.isPending && Boolean(range)} error={stats.error} />
      <QueryNotice
        loading={aggregate.isPending && Boolean(range)}
        error={aggregate.error}
      />
      {aggregate.data && <AggregateStatsPanel data={aggregate.data} />}
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

function AggregateStatsPanel({ data }: { data: AggregateStats }) {
  const { t } = useTranslation();
  return (
    <section className="panel aggregate-stats" aria-labelledby="aggregate-stats-title">
      <h2 id="aggregate-stats-title">{t("charts.summary")}</h2>
      <dl className="aggregate-stats-grid">
        <div><dt>{t("charts.range")}</dt><dd><code>{data.from_block} – {data.to_block}</code></dd></div>
        <div><dt>{t("charts.blocks")}</dt><dd><code>{data.block_count}</code></dd></div>
        <div><dt>{t("charts.transactions")}</dt><dd><code>{data.transaction_count}</code></dd></div>
        <div><dt>{t("charts.gasUsed")}</dt><dd><code>{data.gas_used}</code></dd></div>
        <div><dt>{t("charts.averageTPS")}</dt><dd><code>{data.average_tps ?? "—"}</code></dd></div>
        <div><dt>{t("charts.burned")}</dt><dd><code>{data.burned_wei}</code></dd></div>
        <div><dt>{t("charts.blobBurned")}</dt><dd><code>{data.blob_burned_wei}</code></dd></div>
        <div><dt>{t("charts.tokenEvents")}</dt><dd><code>{data.token_event_count}</code></dd></div>
        <div><dt>{t("charts.tokenTransfers")}</dt><dd><code>{data.token_transfer_count}</code></dd></div>
        <div><dt>{t("charts.nftTransfers")}</dt><dd><code>{data.nft_transfer_count}</code></dd></div>
        <div>
          <dt>{t("charts.snapshot")}</dt>
          <dd>
            <Link
              to="/blocks/$blockID"
              params={{ blockID: data.snapshot.block_hash }}
            >
              <code>{data.snapshot.block_number}</code>
            </Link>
          </dd>
        </div>
      </dl>
      <ul className="aggregate-completeness" aria-label={t("charts.completeness")}>
        {Object.entries(data.completeness).map(([stage, complete]) => (
          <li key={stage}>
            <code>{stageLabel(stage, t)}</code>
            <span className={complete ? "availability yes" : "availability no"}>
              {complete ? t("stageState.complete") : t("stageState.missing")}
            </span>
          </li>
        ))}
      </ul>
    </section>
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
            <th>{t("charts.gasLimit")}</th>
            <th>{t("charts.baseFee")}</th>
            <th>{t("charts.burned")}</th>
            <th>{t("charts.blockTimestamp")}</th>
            <th>{t("charts.interval")}</th>
            <th>{t("charts.tps")}</th>
            <th>{t("charts.blobGasUsed")}</th>
            <th>{t("charts.excessBlobGas")}</th>
            <th>{t("charts.blobBaseFee")}</th>
            <th>{t("charts.blobBurned")}</th>
            <th>{t("charts.tokenEvents")}</th>
            <th>{t("charts.tokenTransfers")}</th>
            <th>{t("charts.nftTransfers")}</th>
            <th>{t("charts.computedAt")}</th>
          </tr>
        </thead>
        <tbody>
          {data.map((item) => (
            <tr key={item.block_hash}>
              <td>
                <span className="table-primary">
                  <Link to="/blocks/$blockID" params={{ blockID: item.block_hash }}>
                    <code>{item.block_number}</code>
                  </Link>
                  <code title={item.block_hash}>{shorten(item.block_hash)}</code>
                </span>
              </td>
              <td><code>{item.transaction_count}</code></td>
              <td><code>{item.gas_used}</code></td>
              <td><code>{item.gas_limit}</code></td>
              <td><code>{item.base_fee_per_gas ?? "—"}</code></td>
              <td><code>{item.burned_wei ?? "—"}</code></td>
              <td><code>{item.block_timestamp}</code></td>
              <td><code>{item.block_interval_seconds ?? "—"}</code></td>
              <td><code>{item.transactions_per_second ?? "—"}</code></td>
              <td><code>{item.blob_gas_used ?? "—"}</code></td>
              <td><code>{item.excess_blob_gas ?? "—"}</code></td>
              <td><code>{item.blob_base_fee_per_gas ?? "—"}</code></td>
              <td><code>{item.blob_burned_wei ?? "—"}</code></td>
              <td><code>{item.token_event_count}</code></td>
              <td><code>{item.token_transfer_count}</code></td>
              <td><code>{item.nft_transfer_count}</code></td>
              <td><time dateTime={item.computed_at}>{formatTimestamp(item.computed_at, locale)}</time></td>
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
  const [submittedAPIKey, setSubmittedAPIKey] = useState("");
  const [address, setAddress] = useState("");
  const [language, setLanguage] = useState<VerificationSubmission["language"]>("solidity");
  const [compilerVersion, setCompilerVersion] = useState("");
  const [contractIdentifier, setContractIdentifier] = useState("");
  const [constructorArguments, setConstructorArguments] = useState("");
  const [standardJSON, setStandardJSON] = useState('{\n  "language": "Solidity",\n  "sources": {},\n  "settings": {}\n}');
  const [submitToSourcify, setSubmitToSourcify] = useState(false);
  const [formError, setFormError] = useState<string>();
  const submission = useSubmitVerification(apiKey);
  const job = useVerificationJob(
    submission.data?.id ?? "",
    submittedAPIKey,
    submission.data ? 1 : 0,
    Boolean(submission.data),
  );
  const currentJob = job.data ?? submission.data;
  const submissionEnabled =
    publicConfig.isSuccess && publicConfig.data.features.verification === true;

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setFormError(undefined);
    submission.reset();
    if (!submissionEnabled) return;

    if (new TextEncoder().encode(standardJSON).byteLength > MAX_STANDARD_JSON_BYTES) {
      setFormError(t("verification.inputTooLarge"));
      return;
    }

    let parsed: unknown;
    try {
      assertNoDuplicateJSONKeys(standardJSON);
      parsed = JSON.parse(standardJSON) as unknown;
    } catch (cause) {
      if (cause instanceof DuplicateJSONKeyError) {
        setFormError(t("verification.duplicateJSONKey"));
        return;
      }
      if (cause instanceof JSONStructureLimitError) {
        setFormError(t("verification.inputTooComplex"));
        return;
      }
      if (cause instanceof UnsafeJSONNumberError) {
        setFormError(t("verification.unsafeJSONNumber"));
        return;
      }
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

    setSubmittedAPIKey(apiKey);
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
      {publicConfig.isSuccess && !submissionEnabled && (
        <UnavailablePanel title={t("verification.unavailable")} detail={t("verification.unavailableDetail")} />
      )}
      {submissionEnabled && (
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
      <VerificationJobLookup />
    </Page>
  );
}

function VerificationJobLookup() {
  const { t } = useTranslation();
  const [jobID, setJobID] = useState("");
  const [apiKey, setAPIKey] = useState("");
  const [submittedJobID, setSubmittedJobID] = useState("");
  const [submittedAPIKey, setSubmittedAPIKey] = useState("");
  const [requestRevision, setRequestRevision] = useState(0);
  const [formError, setFormError] = useState<string>();
  const job = useVerificationJob(
    submittedJobID,
    submittedAPIKey,
    requestRevision,
    requestRevision > 0,
  );

  const load = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setFormError(undefined);
    if (!UUID_PATTERN.test(jobID) || !apiKey) {
      setFormError(t("verification.invalidJobLookup"));
      return;
    }
    setSubmittedJobID(jobID.toLowerCase());
    setSubmittedAPIKey(apiKey);
    setRequestRevision((current) => current + 1);
  };

  return (
    <div className="verification-read-layout">
      <form className="panel verification-job-lookup" autoComplete="off" onSubmit={load}>
        <h2>{t("verification.openJob")}</h2>
        <p className="quiet">{t("verification.readNotice")}</p>
        <FormField
          id="verification-job-lookup-id"
          label={t("verification.jobID")}
          onChange={setJobID}
          value={jobID}
        />
        <label className="field-control" htmlFor="verification-job-lookup-api-key">
          <span>{t("verification.jobAPIKey")}</span>
          <input
            autoComplete="off"
            id="verification-job-lookup-api-key"
            name="verification-job-lookup-api-key"
            onChange={(event) => setAPIKey(event.target.value)}
            spellCheck={false}
            type="password"
            value={apiKey}
          />
        </label>
        {formError && <p className="form-error" role="alert">{formError}</p>}
        <button className="button primary" type="submit">{t("verification.loadJob")}</button>
      </form>
      <VerificationJobPanel
        emptyMessage={t("verification.lookupEmpty")}
        error={job.error}
        job={job.data}
        loading={job.isPending && requestRevision > 0}
      />
    </div>
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

function VerificationJobPanel({
  emptyMessage,
  error,
  job,
  loading,
}: {
  emptyMessage?: string;
  error: unknown;
  job?: VerificationJob;
  loading: boolean;
}) {
  const { t } = useTranslation();
  const headingID = useId();
  return (
    <section className="panel job-panel" aria-labelledby={headingID}>
      <h2 id={headingID}>{t("verification.job")}</h2>
      {!job && !loading && !error && (
        <p className="quiet">{emptyMessage ?? t("verification.jobEmpty")}</p>
      )}
      <QueryNotice loading={loading} error={error} />
      {job && (
        <dl className="job-details" aria-live="polite">
          <div><dt>{t("verification.jobID")}</dt><dd><code>{job.id}</code></dd></div>
          <div><dt>{t("table.status")}</dt><dd><span className={`job-status ${job.status}`}>{verificationJobStatusLabel(job.status, t)}</span></dd></div>
          <div><dt>{t("verification.result")}</dt><dd>{verificationMatchLabel(job.result_kind, t)}</dd></div>
          <div><dt>{t("verification.runtimeMatch")}</dt><dd>{verificationMatchLabel(job.runtime_match, t)}</dd></div>
          <div><dt>{t("verification.creationMatch")}</dt><dd>{verificationMatchLabel(job.creation_match, t)}</dd></div>
          <div><dt>{t("verification.published")}</dt><dd>{job.published === undefined ? "—" : yesNo(job.published, t)}</dd></div>
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
    if (!isAddress(address) || (codeHash !== "" && !HASH_PATTERN.test(codeHash))) {
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
        <p className="quiet">{t("contracts.addressFirst")}</p>
        <FormField id="contract-address-lookup" label={t("page.address")} value={address} onChange={setAddress} />
        <FormField id="contract-code-hash-lookup" label={t("contracts.optionalCodeHash")} value={codeHash} onChange={setCodeHash} />
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
  const [codeHashInput, setCodeHashInput] = useState(codeHash);
  const [apiKey, setAPIKey] = useState("");
  const [submittedCodeHash, setSubmittedCodeHash] = useState("");
  const [submittedAPIKey, setSubmittedAPIKey] = useState("");
  const [requestRevision, setRequestRevision] = useState(0);
  const [formError, setFormError] = useState<string>();
  const validAddress = isAddress(address);
  const contract = useVerifiedContract(
    address,
    submittedCodeHash,
    submittedAPIKey,
    requestRevision,
    validAddress,
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
        <section className="panel verified-contract-card" aria-labelledby="verified-contract-title">
          <h2 id="verified-contract-title">{t("contracts.verifiedArtifact")}</h2>
          <p className="quiet">{t("contracts.readIndependent")}</p>
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
        <Detail label={t("verification.language")} value={verificationLanguageLabel(contract.language, t)} />
        <Detail label={t("verification.compilerVersion")} value={contract.compiler_version} />
        <Detail label={t("contracts.matchKind")} value={verificationMatchLabel(contract.match_kind, t)} />
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

class DuplicateJSONKeyError extends Error {}
class JSONStructureLimitError extends Error {}
class UnsafeJSONNumberError extends Error {}

function assertNoDuplicateJSONKeys(source: string): void {
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

function ContractWorkbench({ address }: { address: string }) {
  const { t } = useTranslation();
  const publicConfig = usePublicConfig();
  const wallet = useWallet();
  const [calldata, setCalldata] = useState("0x");
  const [value, setValue] = useState("");
  const [result, setResult] = useState<{
    kind: "read" | "write";
    value: Hex;
    context: string;
  }>();
  const [error, setError] = useState<{ message: string; context: string }>();
  const [pending, setPending] = useState<"read" | "write">();
  const operationSequence = useRef(0);
  const activeOperation = useRef<
    | {
        id: number;
        kind: "read" | "write";
        context: string;
      }
    | undefined
  >(undefined);
  const expectedChainID = publicConfig.data?.chain_id;
  const validAddress = isAddress(address);
  const chainReady = wallet.active && chainsMatch(wallet.active.chainID, expectedChainID);
  const ready = Boolean(validAddress && chainReady && expectedChainID);
  const canSubmit = ready && pending === undefined;
  const walletIdentity = wallet.active
    ? `${wallet.active.uuid}:${wallet.active.account}:${wallet.active.chainID}`
    : "";
  const operationContext = JSON.stringify([address, expectedChainID ?? "", walletIdentity]);
  const latestOperationContext = useRef(operationContext);
  latestOperationContext.current = operationContext;
  const visibleError = error?.context === operationContext ? error.message : undefined;
  const visibleResult = result?.context === operationContext ? result : undefined;

  const chainMessage = useMemo(() => {
    if (!validAddress) return t("wallet.invalidTarget");
    if (publicConfig.isPending) return t("wallet.chainLoading");
    if (!expectedChainID) return t("wallet.chainUnknown");
    if (wallet.active && !chainsMatch(wallet.active.chainID, expectedChainID)) {
      return t("wallet.wrongChain", { expected: expectedChainID, actual: wallet.active.chainID });
    }
    return undefined;
  }, [expectedChainID, publicConfig.isPending, t, validAddress, wallet.active]);

  const read = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (activeOperation.current) return;
    setError(undefined);
    setResult(undefined);
    if (!validAddress) {
      setError({ context: operationContext, message: t("wallet.invalidTarget") });
      return;
    }
    if (!isContractCalldata(calldata)) {
      setError({ context: operationContext, message: t("wallet.invalidCalldata") });
      return;
    }
    const transactionValue = parseContractValue(value);
    if (transactionValue === null) {
      setError({ context: operationContext, message: t("wallet.invalidValue") });
      return;
    }
    const operation = {
      id: operationSequence.current + 1,
      kind: "read" as const,
      context: operationContext,
    };
    operationSequence.current = operation.id;
    activeOperation.current = operation;
    setPending("read");
    try {
      const output = await wallet.readContract(
        {
          to: getAddress(address),
          data: calldata,
          ...(transactionValue === undefined ? {} : { value: transactionValue }),
        },
        expectedChainID,
      );
      if (activeOperation.current === operation) {
        if (operation.context !== latestOperationContext.current) {
          setError({
            context: latestOperationContext.current,
            message: t("wallet.errors.sessionChanged"),
          });
          return;
        }
        setResult({ kind: "read", value: output, context: operation.context });
      }
    } catch (cause) {
      if (activeOperation.current === operation) {
        setError({
          context: latestOperationContext.current,
          message: t(
            walletErrorTranslationKey(
              cause instanceof WalletBoundaryError ? cause.code : "REQUEST_FAILED",
            ),
          ),
        });
      }
    } finally {
      if (activeOperation.current === operation) {
        activeOperation.current = undefined;
        setPending(undefined);
      }
    }
  };

  const write = async () => {
    if (activeOperation.current) return;
    setError(undefined);
    setResult(undefined);
    if (!validAddress) {
      setError({ context: operationContext, message: t("wallet.invalidTarget") });
      return;
    }
    if (!isContractCalldata(calldata)) {
      setError({ context: operationContext, message: t("wallet.invalidCalldata") });
      return;
    }
    const transactionValue = parseContractValue(value);
    if (transactionValue === null) {
      setError({ context: operationContext, message: t("wallet.invalidValue") });
      return;
    }
    const operation = {
      id: operationSequence.current + 1,
      kind: "write" as const,
      context: operationContext,
    };
    operationSequence.current = operation.id;
    activeOperation.current = operation;
    setPending("write");
    try {
      const hash = await wallet.sendTransaction(
        {
          to: getAddress(address),
          data: calldata,
          ...(transactionValue === undefined ? {} : { value: transactionValue }),
        },
        expectedChainID,
      );
      if (activeOperation.current === operation) {
        if (operation.context !== latestOperationContext.current) {
          setError({
            context: latestOperationContext.current,
            message: t("wallet.errors.transactionOutcomeUnknown"),
          });
          return;
        }
        setResult({ kind: "write", value: hash, context: operation.context });
      }
    } catch (cause) {
      if (activeOperation.current === operation) {
        setError({
          context: latestOperationContext.current,
          message: t(
            walletErrorTranslationKey(
              cause instanceof WalletBoundaryError ? cause.code : "REQUEST_FAILED",
            ),
          ),
        });
      }
    } finally {
      if (activeOperation.current === operation) {
        activeOperation.current = undefined;
        setPending(undefined);
      }
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
          {ready || wallet.active ? t("wallet.connected") : t("actions.connect")}
        </span>
      </div>
      <p className="wallet-notice" id="wallet-boundary-notice">{t("wallet.directNotice")}</p>
      {chainMessage && <p className="chain-warning" role="status">{chainMessage}</p>}
      {wallet.error && (
        <p className="form-error">
          {t(walletErrorTranslationKey(wallet.error))}
        </p>
      )}
      <form
        aria-busy={pending !== undefined}
        aria-describedby="wallet-boundary-notice"
        className="contract-form"
        onSubmit={read}
      >
        <label htmlFor="contract-calldata">{t("wallet.calldata")}</label>
        <textarea
          id="contract-calldata"
          aria-describedby="wallet-boundary-notice contract-calldata-hint"
          disabled={pending !== undefined}
          maxLength={MAX_CONTRACT_CALLDATA_BYTES * 2 + 2}
          spellCheck={false}
          value={calldata}
          onChange={(event) => {
            setCalldata(event.target.value);
            setError(undefined);
            setResult(undefined);
          }}
        />
        <p className="field-hint" id="contract-calldata-hint">{t("wallet.calldataHint")}</p>
        <label htmlFor="transaction-value">{t("wallet.value")}</label>
        <input
          disabled={pending !== undefined}
          id="transaction-value"
          inputMode="numeric"
          maxLength={78}
          pattern="[0-9]*"
          value={value}
          onChange={(event) => {
            setValue(event.target.value);
            setError(undefined);
            setResult(undefined);
          }}
        />
        <div className="contract-actions">
          <button className="button primary" disabled={!canSubmit} type="submit">
            {t("actions.read")}
          </button>
          <button
            className="button secondary"
            disabled={!canSubmit}
            onClick={() => void write()}
            type="button"
          >
            {t("actions.write")}
          </button>
        </div>
      </form>
      {pending && (
        <p role="status">
          {pending === "read" ? t("wallet.pendingRead") : t("wallet.pendingWrite")}
        </p>
      )}
      {visibleError && <p className="form-error" role="alert">{visibleError}</p>}
      {visibleResult && (
        <div className="call-result" role="status">
          <span>
            {visibleResult.kind === "read" ? t("wallet.result") : t("wallet.transactionHash")}
          </span>
          <code>{visibleResult.value}</code>
        </div>
      )}
    </section>
  );
}

function parseContractValue(value: string): Hex | null | undefined {
  if (value === "") return undefined;
  if (!/^(?:0|[1-9][0-9]{0,77})$/u.test(value)) return null;
  const numericValue = BigInt(value);
  return numericValue >= 1n << 256n ? null : toHex(numericValue);
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
