import {
  FormEvent,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from "react";
import { Link, useLocation, useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { QRCodeSVG } from "qrcode.react";
import { hexToBytes, isAddress, type Hex } from "viem";

import {
  useAddressERC20Balances,
  useAddressERC20Transfers,
  useAddressInternalTransactions,
  useAddressNFTBalances,
  useAddressNFTTransfers,
  useAddressTransactions,
  useAddressWithdrawals,
  useAddress,
  useBlock,
  useBlockTransactions,
  useBlocks,
  useChainStatus,
  useGenesisAccounts,
  useNFTOwnership,
  usePublicConfig,
  useSearchResults,
  useToken,
  useTokenTransfers,
  useTokens,
  useTransaction,
  useTransactionCalldata,
  useTransactionInternalTransactions,
  useTransactionAuthorizations,
  useTransactionLogs,
  useTransactionStateChanges,
  useTransactionTokenTransfers,
  useTransactionTrace,
  useTransactions,
  useSubmitVerification,
  useCompilerCatalog,
  useVerificationJob,
} from "@/api/hooks";
import { useHomeSnapshotStream } from "@/api/homeStream";
import { ContractPage, isContractTabHash } from "./ContractPage";
import {
  DelegatedAccountPanel,
  isDelegatedAccountTabHash,
} from "@/contracts/DelegatedAccountPanel";
import type {
  AddressInternalTransaction,
  AddressSummary,
  AddressTokenTransfer,
  AddressWithdrawal,
  BlockSummary,
  ChainStatus,
  ERC20Balance,
  NFTBalance,
  SearchResult,
  TokenEvent,
  TransactionDetail,
  TransactionLog,
  TransactionCalldata as TransactionCalldataResource,
  TransactionSummary,
  VerificationJob,
  VerificationMatchDetails,
  VerificationSuccess,
  VerificationSubmission,
} from "@/api/types";
import {
  formatGweiFromWei,
  formatEtherFromGwei,
  formatInteger,
  formatNativeAmount,
  formatPercentageRatio,
  formatRelativeTimestamp,
  formatTokenAmount,
  formatTimestamp,
  shorten,
} from "@/components/format";
import { CopyButton, CopyableField } from "@/components/CopyButton";
import {
  TransactionStatus,
  type TransactionVisualStatus,
} from "@/components/TransactionStatus";
import { QueryNotice } from "@/components/QueryNotice";
import {
  flattenLogArgument,
  formatTopicValue,
  isAnonymousDecodedLog,
  type LogArgumentRow,
  type TopicDisplayMode,
} from "@/components/logFormat";

const CORE_PAGE_SIZE = 25;
const SEARCH_PAGE_SIZE = 20;

function formatTokenEventAmount(
  event: { amount?: string; decimals?: number; standard: string },
  locale: string,
): string {
  if (event.amount === undefined) return "—";
  return event.standard === "erc20"
    ? formatTokenAmount(event.amount, event.decimals, locale)
    : formatInteger(event.amount, locale);
}

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
  const { t } = useTranslation();
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
          {shorten(transaction.from)} → {transaction.to ? shorten(transaction.to) : "∅"}
        </small>
      </span>
      <IncludedTransactionStatus transaction={transaction} />
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
                    <Link to="/address/$address" params={{ address: account.address }}>
                      <code title={account.address}>{shorten(account.address)}</code>
                    </Link>
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
                  <td className="transaction-method-cell">
                    <code
                      aria-label={transaction.method_signature ?? transaction.method ?? undefined}
                      className="transaction-method"
                      title={transaction.method_signature ?? transaction.method}
                    >{transaction.method ?? "—"}</code>
                  </td>
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

export type EntityKind = "block" | "transaction" | "address" | "token" | "nft";

export function EntityPage({
  kind,
  identifier,
  secondary,
  transactionTab,
  addressTab,
  blockTab,
}: {
  kind: EntityKind;
  identifier: string;
  secondary?: string;
  transactionTab?: string;
  addressTab?: string;
  blockTab?: string;
}) {
  switch (kind) {
    case "block":
      return <BlockDetailPage identifier={identifier} tab={blockTab ?? "overview"} />;
    case "transaction":
      return <TransactionDetailPage hash={identifier} tab={transactionTab ?? "overview"} />;
    case "address":
      return <AddressDetailPage address={identifier} tab={addressTab ?? "transactions"} />;
    case "token":
      return <TokenDetailPage address={identifier} />;
    case "nft":
      return <NFTDetailPage address={identifier} tokenID={secondary ?? ""} />;
  }
}

const BLOCK_TABS = ["overview", "transactions", "withdrawals"] as const;
type BlockTab = typeof BLOCK_TABS[number];

function blockTabsForBlock(block?: BlockSummary): BlockTab[] {
  return BLOCK_TABS.filter((tab) => tab !== "withdrawals" || Boolean(block?.withdrawals));
}

function BlockWithdrawalsPanel({
  withdrawals,
  locale,
}: {
  withdrawals: NonNullable<BlockSummary["withdrawals"]>;
  locale: string;
}) {
  const { t } = useTranslation();
  return (
    <section className="panel transaction-tab-panel" aria-labelledby="block-withdrawals-title">
      <h2 id="block-withdrawals-title">{t("detail.withdrawals")}</h2>
      {withdrawals.length === 0 ? (
        <p className="empty-result">{t("state.noWithdrawals")}</p>
      ) : (
        <div className="table-scroll" tabIndex={0}>
          <table>
            <caption className="sr-only">{t("detail.withdrawals")}</caption>
            <thead><tr>
              <th>{t("detail.withdrawalIndex")}</th>
              <th>{t("detail.validatorIndex")}</th>
              <th>{t("table.address")}</th>
              <th>{t("detail.withdrawalAmount")}</th>
            </tr></thead>
            <tbody>{withdrawals.map((withdrawal) => (
              <tr key={withdrawal.index}>
                <td><code>{formatInteger(withdrawal.index, locale)}</code></td>
                <td><code>{formatInteger(withdrawal.validator_index, locale)}</code></td>
                <td>
                  <Link to="/address/$address" params={{ address: withdrawal.address }}>
                    <code>{withdrawal.address}</code>
                  </Link>
                </td>
                <td><code>{formatEtherFromGwei(withdrawal.amount, locale)} Ether</code></td>
              </tr>
            ))}</tbody>
          </table>
        </div>
      )}
    </section>
  );
}

function BlockTransactionsPanel({
  blockHash,
  busy,
  error,
  hasNext,
  hasPrevious,
  items,
  loading,
  locale,
  nativeDecimals,
  nativeSymbol,
  onNext,
  onPrevious,
  onReset,
  page,
}: {
  blockHash: string;
  busy: boolean;
  error: unknown;
  hasNext: boolean;
  hasPrevious: boolean;
  items?: TransactionSummary[];
  loading: boolean;
  locale: string;
  nativeDecimals: number;
  nativeSymbol: string;
  onNext: () => void;
  onPrevious: () => void;
  onReset: () => void;
  page: number;
}) {
  const { t } = useTranslation();
  return (
    <section className="panel transaction-tab-panel" role="tabpanel" aria-label={t("blockTabs.transactions")}>
      <QueryNotice loading={loading} error={error} onReset={onReset} />
      {items?.length === 0 ? <p className="empty-result">{t("state.noTransactions")}</p> : null}
      {items && items.length > 0 ? (
        <div className="table-scroll" tabIndex={0} aria-label={t("blockTabs.transactions")}>
          <table>
            <caption className="sr-only">{t("blockTabs.transactions")}</caption>
            <thead><tr>
              <th>{t("table.hash")}</th>
              <th>{t("detail.transactionIndex")}</th>
              <th>{t("table.status")}</th>
              <th>{t("table.from")}</th>
              <th>{t("table.to")}</th>
              <th>{t("table.value", { symbol: nativeSymbol })}</th>
              <th>{t("detail.gasUsed")}</th>
              <th>{t("table.finality")}</th>
            </tr></thead>
            <tbody>{items.map((transaction) => (
              <tr key={`${blockHash}:${transaction.transaction_index ?? transaction.hash}:${transaction.hash}`}>
                <td>
                  <Link to="/tx/$hash" params={{ hash: transaction.hash }} search={{ tab: "overview" }}>
                    <code>{shorten(transaction.hash)}</code>
                  </Link>
                </td>
                <td><code>{transaction.transaction_index == null ? "—" : formatInteger(String(transaction.transaction_index), locale)}</code></td>
                <td><TransactionStatusBadge transaction={transaction} /></td>
                <td><Link to="/address/$address" params={{ address: transaction.from }}><code>{shorten(transaction.from)}</code></Link></td>
                <td>{transaction.to ? <Link to="/address/$address" params={{ address: transaction.to }}><code>{shorten(transaction.to)}</code></Link> : t("common.contractCreation")}</td>
                <td><code>{formatNativeAmount(transaction.value, locale, nativeDecimals)}</code></td>
                <td><code>{transaction.gas_used ? formatInteger(transaction.gas_used, locale) : "—"}</code></td>
                <td><FinalityBadge finality={transaction.finality} /></td>
              </tr>
            ))}</tbody>
          </table>
        </div>
      ) : null}
      {items ? (
        <CursorPagination
          busy={busy}
          hasNext={hasNext}
          hasPrevious={hasPrevious}
          label={t("pagination.blockTransactions")}
          onNext={onNext}
          onPrevious={onPrevious}
          page={page}
        />
      ) : null}
    </section>
  );
}

function BlockDetailPage({ identifier, tab }: { identifier: string; tab: string }) {
  const { i18n, t } = useTranslation();
  const navigate = useNavigate();
  const block = useBlock(identifier);
  const pager = useCursorHistory(`block-transactions:${block.data?.hash ?? identifier}`);
  const blockTabs = blockTabsForBlock(block.data);
  const activeTab: BlockTab = blockTabs.includes(tab as BlockTab) ? tab as BlockTab : "overview";
  const transactions = useBlockTransactions(
    identifier,
    pager.cursor,
    activeTab === "transactions" && Boolean(block.data),
  );
  const locale = i18n.resolvedLanguage ?? "en";
  const publicConfig = usePublicConfig();
  const nativeDecimals = publicConfig.data?.native_decimals ?? 18;
  const nativeSymbol = publicConfig.data?.native_symbol ?? "";

  return (
    <Page title={t("page.block")} description={identifier} mono>
      <QueryNotice loading={block.isPending} error={block.error} />
      {block.data && (
        <>
          {!block.data.canonical && (
            <ReorgContext kind="block" hash={block.data.hash} />
          )}
          <nav className="transaction-tabs" role="tablist" aria-label={t("detail.blockSections")}>
            {blockTabs.map((tabID) => (
              <Link
                aria-selected={activeTab === tabID}
                className={activeTab === tabID ? "transaction-tab active" : "transaction-tab"}
                key={tabID}
                onKeyDown={(event) => {
                  if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
                  event.preventDefault();
                  const currentIndex = blockTabs.indexOf(tabID);
                  const nextIndex = event.key === "Home" ? 0
                    : event.key === "End" ? blockTabs.length - 1
                    : event.key === "ArrowLeft"
                      ? (currentIndex - 1 + blockTabs.length) % blockTabs.length
                      : (currentIndex + 1) % blockTabs.length;
                  const nextTab = blockTabs[nextIndex]!;
                  void navigate({
                    to: "/blocks/$blockID",
                    params: { blockID: identifier },
                    search: { tab: nextTab },
                  }).then(() => {
                    const tabs = event.currentTarget.parentElement?.querySelectorAll<HTMLElement>('[role="tab"]');
                    tabs?.[nextIndex]?.focus();
                  });
                }}
                params={{ blockID: identifier }}
                role="tab"
                search={{ tab: tabID }}
                tabIndex={activeTab === tabID ? 0 : -1}
                to="/blocks/$blockID"
              >
                {t(`blockTabs.${tabID}`)}
              </Link>
            ))}
          </nav>
          {activeTab === "overview" && (
            <>
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
              {block.data.number === "0" && block.data.canonical && (
                <p className="context-note">
                  <Link to="/genesis">{t("actions.viewGenesisAccounts")}</Link>
                </p>
              )}
            </>
          )}
          {activeTab === "transactions" && (
            <BlockTransactionsPanel
              blockHash={block.data.hash}
              busy={transactions.isFetching}
              error={transactions.error}
              hasNext={Boolean(transactions.data?.next_cursor)}
              hasPrevious={pager.hasPrevious}
              items={transactions.data?.items}
              loading={transactions.isPending}
              locale={locale}
              nativeDecimals={nativeDecimals}
              nativeSymbol={nativeSymbol}
              onNext={() => pager.next(transactions.data?.next_cursor)}
              onPrevious={pager.previous}
              onReset={pager.reset}
              page={pager.page}
            />
          )}
          {activeTab === "withdrawals" && block.data.withdrawals && (
            <BlockWithdrawalsPanel withdrawals={block.data.withdrawals} locale={locale} />
          )}
        </>
      )}
    </Page>
  );
}

const TRANSACTION_TABS = ["overview", "access-list", "blob", "authorizations", "internal-transactions", "token-transfers", "logs", "trace", "state-changes"] as const;
type TransactionTab = typeof TRANSACTION_TABS[number];

function transactionTabsForType(type?: string): TransactionTab[] {
  const tabs: TransactionTab[] = ["overview"];
  if (type === "1" || type === "2" || type === "3" || type === "4") tabs.push("access-list");
  if (type === "3") tabs.push("blob");
  if (type === "4") tabs.push("authorizations");
  tabs.push("internal-transactions", "token-transfers", "logs", "trace", "state-changes");
  return tabs;
}

function gasUsageValue(gasLimit: string, gasUsed: string | undefined, locale: string): string {
  const quantities = `${formatInteger(gasLimit, locale)} | ${formatInteger(gasUsed, locale)}`;
  const percentage = formatPercentageRatio(gasUsed, gasLimit, locale);
  return percentage ? `${quantities} (${percentage})` : quantities;
}

function FeeSettings({ entries, locale }: {
  entries: ReadonlyArray<{ label: string; value?: string }>;
  locale: string;
}) {
  return <span className="transaction-fee-values">
    {entries.map((entry, index) => (
      <span className="transaction-fee-value" key={entry.label}>
        <span><span className="transaction-fee-label">{entry.label}:</span>{" "}
          {entry.value === undefined ? "—" : `${formatGweiFromWei(entry.value, locale)} Gwei`}
        </span>
        {index < entries.length - 1 ? <span className="transaction-fee-separator" aria-hidden="true">|</span> : null}
      </span>
    ))}
  </span>;
}

function calldataDecodedValue(value: unknown, type: string, locale: string): string {
  const baseType = type.replace(/\[[0-9]*\]/gu, "");
  if (typeof value === "string" && /^(?:u?int)(?:[0-9]*)$/u.test(baseType)) {
    return formatInteger(value, locale);
  }
  return formatLogArgument(value);
}

function TransactionCalldataValues({
  signature,
  values,
  locale,
  columnLabels,
}: {
  signature: string;
  values: TransactionCalldataResource["decoding"]["inputs"];
  locale: string;
  columnLabels: Readonly<{ index: string; params: string; type: string; data: string }>;
}) {
  return (
    <div className="calldata-value-tree">
      <div className="calldata-table-scroll">
        <div className="calldata-table" role="table" aria-label={signature}>
          <div className="calldata-table-row calldata-table-header" role="row">
            <span role="columnheader">{columnLabels.index}</span>
            <span role="columnheader">{columnLabels.params}</span>
            <span role="columnheader">{columnLabels.type}</span>
            <span role="columnheader">{columnLabels.data}</span>
          </div>
        {values.map((value, index) => (
          <div className="calldata-table-row calldata-scalar calldata-depth-0" role="row" key={`${value.name}:${value.type}:${index}`}>
            <span className="calldata-row-index" role="cell">{index + 1}</span>
            <span className="calldata-row-name" role="cell">{value.name || `#${index}`}</span>
            <small className="calldata-row-type" role="cell">{value.type}</small>
            <code className="calldata-row-data" role="cell">{calldataDecodedValue(value.value, value.type, locale)}</code>
          </div>
        ))}
        </div>
      </div>
    </div>
  );
}

function TransactionCalldata({
  transaction,
  resource,
  loading,
  identityCurrent,
}: {
  transaction: TransactionSummary;
  resource?: TransactionCalldataResource;
  loading: boolean;
  identityCurrent: boolean;
}) {
  const { i18n, t } = useTranslation();
  const decodedHeadingID = useId();
  const rawHeadingID = useId();
  const input = transaction.input;
  const targetAddress = transaction.to ?? "";
  const hasCalldata = input.length > 2;
  const enabled = hasCalldata && targetAddress.length > 0;
  const resourceCurrent = identityCurrent && resource !== undefined
    && resource.input.toLowerCase() === input.toLowerCase()
    && resource.execution.context_address.toLowerCase() === targetAddress.toLowerCase();
  const decoding = resourceCurrent ? resource.decoding : undefined;
  const [rawMode, setRawMode] = useState<"hex" | "utf8">("hex");
  const [utf8Unavailable, setUtf8Unavailable] = useState(false);
  useEffect(() => {
    setRawMode("hex");
    setUtf8Unavailable(false);
  }, [input]);
  const utf8 = useMemo(() => {
    if (!/^0x(?:[0-9a-f]{2})*$/iu.test(input)) return undefined;
    try {
      return new TextDecoder("utf-8", { fatal: true }).decode(hexToBytes(input as Hex));
    } catch {
      return undefined;
    }
  }, [input]);
  const loadingABI = enabled && loading;
  const displayValue = rawMode === "utf8" && utf8 !== undefined ? utf8 : input;
  const toggleRawMode = () => {
    if (rawMode === "utf8") {
      setRawMode("hex");
      setUtf8Unavailable(false);
      return;
    }
    if (utf8 === undefined) {
      setUtf8Unavailable(true);
      return;
    }
    setRawMode("utf8");
    setUtf8Unavailable(false);
  };

  return (
    <div className="transaction-calldata">
      {enabled && <section className="transaction-calldata-decoded" aria-labelledby={decodedHeadingID}>
        <h3 className="transaction-calldata-heading" id={decodedHeadingID}>
          {t("detail.calldataDecoded")}
          {enabled && !loadingABI && decoding?.status === "decoded" && decoding.signature && (
            <> · <code>{decoding.signature}</code></>
          )}
        </h3>
        {enabled && loadingABI && <p className="quiet" role="status">{t("detail.calldataDecodeLoading")}</p>}
        {enabled && !loadingABI && decoding?.status === "decoded" && decoding.signature && resource && (
          <>
            <div className="calldata-abi-sources" aria-label={t("detail.calldataAbiEvidence")}>
              <div className="calldata-abi-source">
                <span>{t("detail.calldataExecutionEvidence")}</span>
                <span aria-hidden="true">·</span>
                <span>{t(`detail.executionResolution.${resource.execution.resolution}`)}</span>
                {resource.execution.address && <>
                  <span aria-hidden="true">·</span>
                  <CopyableField value={resource.execution.address}>
                    <Link to="/address/$address" params={{ address: resource.execution.address }}><code>{resource.execution.address}</code></Link>
                  </CopyableField>
                </>}
              </div>
              {decoding.abi_source?.address && (
                <div className="calldata-abi-source">
                  <span>{t("detail.calldataAbiSource", { kind: decoding.abi_source.kind })}</span>
                  <span aria-hidden="true">·</span>
                  <CopyableField value={decoding.abi_source.address}>
                    <Link to="/address/$address" params={{ address: decoding.abi_source.address }}><code>{decoding.abi_source.address}</code></Link>
                  </CopyableField>
                </div>
              )}
            </div>
            {decoding.inputs.length > 0 ? (
              <TransactionCalldataValues
                values={decoding.inputs}
                columnLabels={{
                  index: t("detail.calldataIndex"),
                  params: t("detail.calldataParams"),
                  type: t("detail.calldataType"),
                  data: t("detail.calldataData"),
                }}
                locale={i18n.resolvedLanguage ?? "en"}
                signature={decoding.signature}
              />
            ) : <p className="quiet">{t("detail.calldataNoParameters")}</p>}
          </>
        )}
        {enabled && !loadingABI && decoding?.status !== "decoded" && (
          <p className="capability-panel" role="status">
            {t(!identityCurrent || resource !== undefined && !resourceCurrent
              ? "state.transactionIdentityChanged"
              : decoding?.status === "not_applicable"
                ? "detail.calldataNoExecutionCode"
                : decoding?.status === "unknown"
                  ? "detail.calldataUnknownSelector"
                  : decoding?.status === "malformed"
                    ? "detail.calldataMalformed"
                    : decoding?.status === "ambiguous"
                      ? "detail.calldataAmbiguous"
                      : "detail.calldataUnavailable")}
            {decoding?.status === "ambiguous" && decoding.candidates.length > 0
              ? <> · <code>{decoding.candidates.join(" · ")}</code></>
              : null}
          </p>
        )}
      </section>}
      <section className="transaction-calldata-raw" aria-labelledby={rawHeadingID}>
        <header className="transaction-calldata-raw-header">
          <h3 className="transaction-calldata-heading" id={rawHeadingID}>{t("detail.rawCalldata")}</h3>
          <div className="transaction-calldata-raw-actions">
            <button
              className="transaction-calldata-mode-link"
              onClick={toggleRawMode}
              type="button"
            >{t(rawMode === "hex" ? "detail.rawViewAsUtf8" : "detail.rawViewAsHex")}</button>
            <CopyButton value={input} />
          </div>
        </header>
        {utf8Unavailable && (
          <p className="quiet transaction-calldata-raw-status" role="status">{t("detail.rawUtf8Unavailable")}</p>
        )}
        <textarea
          aria-label={t("detail.rawCalldataValue", { mode: t(rawMode === "hex" ? "detail.rawHex" : "detail.rawUtf8") })}
          className="transaction-calldata-raw-value transaction-data"
          readOnly
          rows={4}
          spellCheck={false}
          value={displayValue}
          wrap="soft"
        />
      </section>
    </div>
  );
}

function TransactionDetailPage({ hash, tab }: { hash: string; tab: string }) {
  const { i18n, t } = useTranslation();
  const navigate = useNavigate();
  const transactionDetail = useTransaction(hash);
  const [mempoolDetailExpired, setMempoolDetailExpired] = useState(false);
  const observedDetail = transactionDetail.data;
  const observedExpiry = observedDetail?.kind === "pending" || observedDetail?.kind === "replaced"
    ? Date.parse(observedDetail.transaction.expires_at)
    : Number.NaN;
  const detailExpired = mempoolDetailExpired
    || (Number.isFinite(observedExpiry) && observedExpiry <= Date.now());
  const detail = transactionDetail.error || detailExpired ? undefined : observedDetail;
  const included = detail?.kind === "included";
  const transaction = detail?.kind === "included"
    ? { ...transactionDetail, data: detail.transaction }
    : { ...transactionDetail, data: undefined };
  const transactionTabs = transactionTabsForType(transaction.data?.type);
  const activeTab: TransactionTab = transactionTabs.includes(tab as TransactionTab)
    ? tab as TransactionTab
    : "overview";
  const calldataEnabled = included && activeTab === "overview"
    && Boolean(transaction.data?.to);
  const calldata = useTransactionCalldata(hash, calldataEnabled);
  const internalPager = useCursorHistory(`transaction-internal-transactions:${hash}`);
  const tokenPager = useCursorHistory(`transaction-token-transfers:${hash}`);
  const logPager = useCursorHistory(`transaction-logs:${hash}`);
  const statePager = useCursorHistory(`transaction-state-changes:${hash}`);
  const authorizationPager = useCursorHistory(`transaction-authorizations:${hash}`);
  const tokenTransfers = useTransactionTokenTransfers(
    hash,
    tokenPager.cursor,
    included && (activeTab === "overview" || activeTab === "token-transfers"),
  );
  const internalTransactions = useTransactionInternalTransactions(
    hash,
    internalPager.cursor,
    included && activeTab === "internal-transactions",
  );
  const logs = useTransactionLogs(hash, logPager.cursor, included && activeTab === "logs");
  const trace = useTransactionTrace(hash, included && activeTab === "trace");
  const authorizations = useTransactionAuthorizations(
    hash,
    authorizationPager.cursor,
    included && activeTab === "authorizations",
  );
  const stateChanges = useTransactionStateChanges(
    hash,
    statePager.cursor,
    included && activeTab === "state-changes",
  );
  const publicConfig = usePublicConfig();
  const nativeDecimals = publicConfig.data?.native_decimals ?? 18;
  const nativeSymbol = publicConfig.data?.native_symbol ?? "";
  const locale = i18n.resolvedLanguage ?? "en";
  const lastIdentityRetry = useRef("");
  const identityMatches = (blockHash?: string) =>
    !blockHash || !transaction.data?.block_hash || blockHash === transaction.data.block_hash;
  const tokenIdentityCurrent = identityMatches(tokenTransfers.data?.block_hash);
  const internalIdentityCurrent = identityMatches(internalTransactions.data?.block_hash);
  const calldataIdentityCurrent = calldata.data === undefined || transaction.data === undefined
    || transactionCalldataIdentityMatches(transaction.data, calldata.data);
  const calldataIdentityRetryKey = !calldataIdentityCurrent && transaction.data && calldata.data
    ? transactionCalldataRetryKey(transaction.data, calldata.data)
    : undefined;
  const calldataIdentityRetryPending = calldataIdentityRetryKey !== undefined
    && lastIdentityRetry.current !== calldataIdentityRetryKey;
  const transactionActionEvidence: TransactionActionEvidence = !transaction.data?.to
    ? { state: "unavailable" }
    : calldata.isPending || !calldataIdentityCurrent
      && (calldata.isFetching || calldataIdentityRetryPending)
      ? { state: "loading" }
      : calldata.error !== null || calldata.data === undefined || !calldataIdentityCurrent
        ? { state: "unavailable" }
        : { state: "current", resolution: calldata.data.execution.resolution };
  const logIdentityCurrent = identityMatches(logs.data?.block_hash);
  const traceIdentityCurrent = identityMatches(trace.data?.block_hash);
  const stateIdentityCurrent = identityMatches(stateChanges.data?.block_hash);
  const authorizationIdentityCurrent = identityMatches(authorizations.data?.block_hash);
  useEffect(() => {
    if (activeTab === "access-list" || activeTab === "blob") return;
    const resource = activeTab === "token-transfers" || activeTab === "overview"
      ? tokenTransfers
      : activeTab === "internal-transactions" ? internalTransactions
      : activeTab === "logs" ? logs
      : activeTab === "trace" ? trace
      : activeTab === "authorizations" ? authorizations
      : stateChanges;
    const resourceBlockHash = resource.data?.block_hash;
    const overviewBlockHash = transaction.data?.block_hash;
    if (!resourceBlockHash || !overviewBlockHash || resourceBlockHash === overviewBlockHash) return;
    const retryKey = `${activeTab}:${overviewBlockHash}:${resourceBlockHash}`;
    if (lastIdentityRetry.current === retryKey) return;
    lastIdentityRetry.current = retryKey;
    void Promise.all([transaction.refetch(), resource.refetch()]);
  }, [
    activeTab,
    authorizations,
    internalTransactions,
    logs,
    stateChanges,
    tokenTransfers,
    trace,
    transaction,
  ]);
  useEffect(() => {
    if (!calldataEnabled || calldataIdentityCurrent) return;
    if (calldataIdentityRetryKey === undefined
      || lastIdentityRetry.current === calldataIdentityRetryKey) return;
    lastIdentityRetry.current = calldataIdentityRetryKey;
    void Promise.all([transaction.refetch(), calldata.refetch()]);
  }, [
    calldata,
    calldataEnabled,
    calldataIdentityCurrent,
    calldataIdentityRetryKey,
    transaction,
  ]);
  const stateGroups = useMemo(() => {
    const groups = new Map<string, NonNullable<typeof stateChanges.data>["items"]>();
    for (const change of stateChanges.data?.items ?? []) {
      const group = groups.get(change.address) ?? [];
      group.push(change);
      groups.set(change.address, group);
    }
    return [...groups.entries()];
  }, [stateChanges.data]);

  useEffect(() => {
    if (!Number.isFinite(observedExpiry)) {
      setMempoolDetailExpired(false);
      return;
    }
    if (observedExpiry <= Date.now()) {
      setMempoolDetailExpired(true);
      return;
    }
    setMempoolDetailExpired(false);
    const maximumDelay = 2_147_483_647;
    const timer = window.setTimeout(
      () => setMempoolDetailExpired(true),
      Math.min(observedExpiry - Date.now(), maximumDelay),
    );
    return () => window.clearTimeout(timer);
  }, [observedExpiry]);

  if (detail?.kind === "pending" || detail?.kind === "replaced") {
    return (
      <Page title={t("page.transactionDetails")} description={hash} mono>
        <QueryNotice loading={transactionDetail.isPending} error={transactionDetail.error} />
        <MempoolTransactionOverview
          detail={detail}
          locale={locale}
          nativeDecimals={nativeDecimals}
          nativeSymbol={nativeSymbol}
        />
      </Page>
    );
  }

  return (
    <Page title={t("page.transactionDetails")} description={hash} mono>
      <QueryNotice loading={transactionDetail.isPending} error={transactionDetail.error} />
      {detailExpired && !transactionDetail.error ? <MempoolDetailExpired /> : null}
      {transaction.data && (
        <>
          {!transaction.data.canonical && (
            <ReorgContext kind="transaction" hash={transaction.data.hash} />
          )}
          <nav className="transaction-tabs" role="tablist" aria-label={t("detail.transactionSections")}>
            {transactionTabs.map((tabID) => (
              <Link
                aria-selected={activeTab === tabID}
                className={activeTab === tabID ? "transaction-tab active" : "transaction-tab"}
                key={tabID}
                onKeyDown={(event) => {
                  if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
                  event.preventDefault();
                  const currentIndex = transactionTabs.indexOf(tabID);
                  const nextIndex = event.key === "Home" ? 0
                    : event.key === "End" ? transactionTabs.length - 1
                    : event.key === "ArrowLeft"
                      ? (currentIndex - 1 + transactionTabs.length) % transactionTabs.length
                      : (currentIndex + 1) % transactionTabs.length;
                  const nextTab = transactionTabs[nextIndex];
                  if (!nextTab) return;
                  const tabList = event.currentTarget.parentElement;
                  void navigate({
                    to: "/tx/$hash",
                    params: { hash },
                    search: { tab: nextTab },
                  }).then(() => {
                    const tabs = tabList?.querySelectorAll<HTMLElement>('[role="tab"]');
                    tabs?.[nextIndex]?.focus();
                  });
                }}
                params={{ hash }}
                role="tab"
                search={{ tab: tabID }}
                tabIndex={activeTab === tabID ? 0 : -1}
                to="/tx/$hash"
              >
                {t(`transactionTabs.${tabID}`)}
              </Link>
            ))}
          </nav>

          {activeTab === "overview" && (
            <div className="transaction-overview" role="tabpanel">
              <section className="panel transaction-action-card" aria-labelledby="transaction-action-title">
                <div className="transaction-action-icon" aria-hidden="true">↗</div>
                <div>
                  <span id="transaction-action-title">{t("detail.transactionAction")}</span>
                  <strong aria-live="polite">
                    {transactionActionLabel(transaction.data, transactionActionEvidence, t)}
                  </strong>
                  <p>
                    {formatNativeAmount(transaction.data.value, locale, nativeDecimals)} {nativeSymbol}
                    {transaction.data.to ? (
                      <> · <Link to="/address/$address" params={{ address: transaction.data.to }}>
                        {shorten(transaction.data.to)}
                      </Link></>
                    ) : null}
                    {tokenIdentityCurrent && (tokenTransfers.data?.items.length ?? 0) > 0
                      ? <> · {t("detail.actionTokenEvents", {
                          count: tokenTransfers.data?.items.length ?? 0,
                        })}</>
                      : null}
                  </p>
                </div>
              </section>

              <section className="panel transaction-detail-card" aria-label={t("detail.transactionSummary")}>
                <h2 className="sr-only">{t("detail.transactionSummary")}</h2>
                <dl className="transaction-detail-list">
                  <TransactionDetailRow label={t("table.hash")}>
                    <CopyableField value={transaction.data.hash}>
                      <code>{transaction.data.hash}</code>
                    </CopyableField>
                  </TransactionDetailRow>
                  <TransactionDetailRow label={t("table.status")}>
                    <TransactionStatusBadge transaction={transaction.data} />
                  </TransactionDetailRow>
                  <TransactionDetailRow label={t("table.block")}>
                    {transaction.data.block_hash ? (
                      <span className="transaction-inline-values">
                        <Link to="/blocks/$blockID" params={{ blockID: transaction.data.block_hash }}>
                          {formatInteger(transaction.data.block_number, locale)}
                        </Link>
                        {transaction.data.confirmations && (
                          <span className="transaction-confirmations">
                            {t("detail.confirmationCount", {
                              count: formatInteger(transaction.data.confirmations, locale),
                            })}
                          </span>
                        )}
                      </span>
                    ) : "—"}
                  </TransactionDetailRow>
                  <TransactionDetailRow label={t("detail.blockTimestamp")}>
                    {transaction.data.block_timestamp
                      ? <time dateTime={transaction.data.block_timestamp}>
                          {formatTimestamp(transaction.data.block_timestamp, locale)}
                        </time>
                      : "—"}
                  </TransactionDetailRow>
                  <TransactionDetailRow label={t("table.from")}>
                    <CopyableField value={transaction.data.from}>
                      <Link to="/address/$address" params={{ address: transaction.data.from }}>
                        <code>{transaction.data.from}</code>
                      </Link>
                    </CopyableField>
                  </TransactionDetailRow>
                  <TransactionDetailRow label={t("table.to")}>
                    {transaction.data.to ? (
                      <CopyableField value={transaction.data.to}>
                        <Link to="/address/$address" params={{ address: transaction.data.to }}>
                          <code>{transaction.data.to}</code>
                        </Link>
                      </CopyableField>
                    ) : transaction.data.contract_address ? (
                      <span className="transaction-inline-values">
                        <CopyableField value={transaction.data.contract_address}>
                          <Link
                            to="/address/$address"
                            params={{ address: transaction.data.contract_address }}
                          >
                            <code>{transaction.data.contract_address}</code>
                          </Link>
                        </CopyableField>
                        <span className="transaction-creation-label">
                          {t("common.contractCreation")}
                        </span>
                      </span>
                    ) : t("common.contractCreation")}
                  </TransactionDetailRow>
                  <TransactionDetailRow label={t("table.value", { symbol: nativeSymbol })}>
                    <strong>{formatNativeAmount(transaction.data.value, locale, nativeDecimals)} {nativeSymbol}</strong>
                  </TransactionDetailRow>
                  <TransactionDetailRow label={t("detail.transactionFee")}>
                    {formatNativeAmount(transaction.data.tx_fee_wei, locale, nativeDecimals)} {nativeSymbol}
                  </TransactionDetailRow>
                </dl>
                <details className="transaction-more-details">
                  <summary>{t("detail.moreDetails")}</summary>
                  <dl className="transaction-detail-list advanced">
                    <TransactionDetailRow label={t("detail.nonce")}>
                      {formatInteger(transaction.data.nonce, locale)}
                    </TransactionDetailRow>
                    <TransactionDetailRow label={t("detail.type")}>
                      {transactionTypeLabel(transaction.data.type, t)}
                    </TransactionDetailRow>
                    <TransactionDetailRow label={t("detail.gasLimitAndUsage")}>
                      {gasUsageValue(transaction.data.gas, transaction.data.gas_used, locale)}
                    </TransactionDetailRow>
                    <TransactionDetailRow label={t("detail.gasFees")}>
                      <FeeSettings locale={locale} entries={[
                        { label: t("detail.feeBase"), value: transaction.data.base_fee_per_gas },
                        { label: t("detail.feeMax"), value: transaction.data.max_fee_per_gas ?? transaction.data.gas_price },
                        { label: t("detail.feeMaxPriority"), value: transaction.data.max_priority_fee_per_gas ?? transaction.data.gas_price },
                      ]} />
                    </TransactionDetailRow>
                    {transaction.data.type === "3" ? (
                      <TransactionDetailRow label={t("detail.blobGasFees")}>
                        <FeeSettings locale={locale} entries={[
                          { label: t("detail.blobBaseFee"), value: transaction.data.blob_base_fee_per_gas },
                          { label: t("detail.feeMax"), value: transaction.data.max_fee_per_blob_gas },
                        ]} />
                      </TransactionDetailRow>
                    ) : null}
                    <TransactionDetailRow label={t("detail.effectiveGasPrice")}>
                      {formatGweiFromWei(transaction.data.effective_gas_price, locale)}
                    </TransactionDetailRow>
                    <TransactionDetailRow label={t("detail.burned")}>
                      {formatNativeAmount(transaction.data.burned_wei, locale, nativeDecimals)} {nativeSymbol}
                    </TransactionDetailRow>
                    <TransactionDetailRow label={t("detail.canonical")}>
                      {yesNo(transaction.data.canonical, t)}
                    </TransactionDetailRow>
                    <TransactionDetailRow label={t("table.finality")}>
                      {finalityLabel(transaction.data.finality, t)}
                    </TransactionDetailRow>
                    <TransactionDetailRow label={t("detail.input")} wide>
                      <TransactionCalldata
                        identityCurrent={calldataIdentityCurrent}
                        loading={calldata.isPending}
                        resource={calldata.data}
                        transaction={transaction.data}
                      />
                    </TransactionDetailRow>
                  </dl>
                </details>
              </section>

            </div>
          )}

          {activeTab === "internal-transactions" && (
            <section
              className="panel transaction-tab-panel"
              role="tabpanel"
              aria-labelledby="transaction-internal-transactions-title"
            >
                <h2 id="transaction-internal-transactions-title">
                  {t("addressTab.internalTransactions")}
                </h2>
                <QueryNotice
                  loading={internalTransactions.isPending}
                  error={internalTransactions.error}
                />
                {internalTransactions.data && !internalIdentityCurrent ? (
                  <p className="capability-panel">{t("state.transactionIdentityChanged")}</p>
                ) : null}
                {internalIdentityCurrent && internalTransactions.data?.state !== "complete"
                  && internalTransactions.data ? (
                    <CapabilityDegraded stage="trace" state={internalTransactions.data.state} />
                  ) : null}
                {internalIdentityCurrent && internalTransactions.data?.state === "complete"
                  && internalTransactions.data.items.length === 0 ? (
                    <p className="empty-result">{t("state.noTransactionInternalTransactions")}</p>
                  ) : null}
                {internalIdentityCurrent && internalTransactions.data?.state === "complete"
                  && internalTransactions.data.items.length > 0 ? (
                    <div
                      className="table-scroll"
                      tabIndex={0}
                      aria-label={t("addressTab.internalTransactions")}
                    >
                      <table>
                        <caption className="sr-only">{t("addressTab.internalTransactions")}</caption>
                        <thead>
                          <tr>
                            <th>{t("detail.callType")}</th>
                            <th>{t("table.from")}</th>
                            <th>{t("table.to")}</th>
                            <th>{t("table.value", { symbol: nativeSymbol })}</th>
                          </tr>
                        </thead>
                        <tbody>
                          {internalTransactions.data.items.map((item) => {
                            const destination = item.created_address ?? item.to;
                            return (
                              <tr key={item.path.join(".")}>
                                <td><span className="transaction-trace-kind">{item.call_type}</span></td>
                                <td>
                                  <CopyableField value={item.from}>
                                    <Link to="/address/$address" params={{ address: item.from }}>
                                      <code>{shorten(item.from)}</code>
                                    </Link>
                                  </CopyableField>
                                </td>
                                <td>
                                  {destination ? (
                                    <span className="table-primary">
                                      <CopyableField value={destination}>
                                        <Link to="/address/$address" params={{ address: destination }}>
                                          <code>{shorten(destination)}</code>
                                        </Link>
                                      </CopyableField>
                                      {item.created_address ? <small>{t("activity.created")}</small> : null}
                                    </span>
                                  ) : "—"}
                                </td>
                                <td>
                                  <code>{formatNativeAmount(item.value, locale, nativeDecimals)}</code>
                                  {nativeSymbol ? ` ${nativeSymbol}` : ""}
                                </td>
                              </tr>
                            );
                          })}
                        </tbody>
                      </table>
                    </div>
                  ) : null}
                {internalIdentityCurrent && internalTransactions.data ? (
                  <CursorPagination
                    busy={internalTransactions.isFetching}
                    hasNext={Boolean(internalTransactions.data.next_cursor)}
                    hasPrevious={internalPager.hasPrevious}
                    label={t("addressTab.internalTransactions")}
                    onNext={() => internalPager.next(internalTransactions.data?.next_cursor)}
                    onPrevious={internalPager.previous}
                    page={internalPager.page}
                  />
                ) : null}
            </section>
          )}

          {activeTab === "access-list" && (
            <section className="panel transaction-tab-panel" role="tabpanel">
              <h2>{t("detail.accessList")}</h2>
              {transaction.data.access_list?.length ? (
                <div className="transaction-log-list">
                  {transaction.data.access_list.map((entry) => (
                    <article className="transaction-log" key={entry.address}>
                      <header><strong><Link to="/address/$address" params={{ address: entry.address }}><code>{entry.address}</code></Link></strong></header>
                      {entry.storage_keys.length > 0 ? (
                        <dl>
                          {entry.storage_keys.map((key) => (
                            <div key={key}><dt>{t("detail.storageKey")}</dt><dd><code>{key}</code></dd></div>
                          ))}
                        </dl>
                      ) : <p className="quiet">{t("state.noStorageKeys")}</p>}
                    </article>
                  ))}
                </div>
              ) : <p className="empty-result">{t("state.emptyAccessList")}</p>}
            </section>
          )}

          {activeTab === "blob" && (
            <section className="panel transaction-tab-panel" role="tabpanel">
              <h2>{t("detail.blobData")}</h2>
              <DetailList label={t("detail.blobData")}>
                <Detail label={t("detail.blobGasFees")} value={(
                  <FeeSettings locale={locale} entries={[
                    { label: t("detail.blobBaseFee"), value: transaction.data.blob_base_fee_per_gas },
                    { label: t("detail.feeMax"), value: transaction.data.max_fee_per_blob_gas },
                  ]} />
                )} />
                <Detail label={t("detail.blobCount")} value={formatInteger(transaction.data.blob_versioned_hashes?.length ?? 0, locale)} />
              </DetailList>
              {transaction.data.blob_versioned_hashes?.length ? (
                <div className="transaction-log-list">
                  {transaction.data.blob_versioned_hashes.map((hash, index) => (
                    <article className="transaction-log" key={hash}>
                      <header><strong>{t("detail.blobIndex", { index })}</strong></header>
                      <code className="mono-wrap">{hash}</code>
                    </article>
                  ))}
                </div>
              ) : <p className="empty-result">{t("state.noBlobHashes")}</p>}
            </section>
          )}

          {activeTab === "authorizations" && (
            <section className="panel transaction-tab-panel" role="tabpanel">
              <QueryNotice loading={authorizations.isPending} error={authorizations.error} />
              {authorizations.data && !authorizationIdentityCurrent ? (
                <p className="capability-panel">{t("state.transactionIdentityChanged")}</p>
              ) : null}
              {authorizationIdentityCurrent && authorizations.data?.state !== "complete" && authorizations.data ? (
                <CapabilityDegraded stage="state_diff" state={authorizations.data.state} />
              ) : null}
              {authorizationIdentityCurrent && authorizations.data?.state === "complete" && authorizations.data.items.length === 0 ? (
                <p className="empty-result">{t("state.noAuthorizations")}</p>
              ) : null}
              {authorizationIdentityCurrent && authorizations.data?.state === "complete" && authorizations.data.items.length > 0 ? (
                <div className="transaction-log-list">
                  {authorizations.data.items.map((authorization) => (
                    <article className="transaction-log" key={authorization.index}>
                      <header>
                        <strong>{t("detail.authorizationIndex", { index: authorization.index })}</strong>
                        <span>{authorization.application_status}</span>
                      </header>
                      <dl>
                        <div><dt>{t("delegation.authority")}</dt><dd><code>{authorization.authority ?? "—"}</code></dd></div>
                        <div><dt>{t("delegation.delegate")}</dt><dd><code>{authorization.delegate}</code></dd></div>
                        <div><dt>{t("detail.chainID")}</dt><dd><code>{authorization.chain_id}</code></dd></div>
                        <div><dt>{t("detail.nonce")}</dt><dd><code>{authorization.nonce}</code></dd></div>
                        <div><dt>{t("detail.signatureStatus")}</dt><dd>{authorization.signature_status}</dd></div>
                        <div><dt>{t("detail.skipReason")}</dt><dd>{authorization.skip_reason ?? "—"}</dd></div>
                      </dl>
                      <details className="transaction-more-details">
                        <summary>{t("detail.rawAuthorization")}</summary>
                        <dl>
                          <div><dt>yParity</dt><dd><code>{authorization.y_parity}</code></dd></div>
                          <div><dt>r</dt><dd><code>{authorization.r}</code></dd></div>
                          <div><dt>s</dt><dd><code>{authorization.s}</code></dd></div>
                        </dl>
                      </details>
                    </article>
                  ))}
                </div>
              ) : null}
              {authorizationIdentityCurrent && authorizations.data ? (
                <CursorPagination
                  busy={authorizations.isFetching}
                  hasNext={Boolean(authorizations.data.next_cursor)}
                  hasPrevious={authorizationPager.hasPrevious}
                  label={t("transactionTabs.authorizations")}
                  onNext={() => authorizationPager.next(authorizations.data?.next_cursor)}
                  onPrevious={authorizationPager.previous}
                  page={authorizationPager.page}
                />
              ) : null}
            </section>
          )}

          {activeTab === "token-transfers" && (
            <section className="panel transaction-tab-panel" role="tabpanel">
              <QueryNotice loading={tokenTransfers.isPending} error={tokenTransfers.error} />
              {tokenTransfers.data && !tokenIdentityCurrent && (
                <p className="capability-panel">{t("state.transactionIdentityChanged")}</p>
              )}
              {tokenIdentityCurrent && tokenTransfers.data?.state !== "complete" && tokenTransfers.data && (
                <CapabilityDegraded stage="token" state={tokenTransfers.data.state} />
              )}
              {tokenIdentityCurrent && tokenTransfers.data?.state === "complete" && tokenTransfers.data.items.length === 0 && (
                <p className="empty-result">{t("state.noTransactionTokenTransfers")}</p>
              )}
              {tokenIdentityCurrent && tokenTransfers.data?.state === "complete" && tokenTransfers.data.items.length > 0 && (
                <div className="table-scroll" tabIndex={0}>
                  <table>
                    <thead><tr>
                      <th>{t("table.token")}</th><th>{t("detail.event")}</th>
                      <th>{t("table.from")}</th><th>{t("table.to")}</th>
                      <th>{t("detail.amountOrTokenID")}</th>
                    </tr></thead>
                    <tbody>{tokenTransfers.data.items.map((event) => (
                      <tr key={`${event.log_index}:${event.sub_index}`}>
                        <td><Link to="/token/$address" params={{ address: event.token_address }}>
                          <code>{shorten(event.token_address)}</code>
                        </Link><small className="table-secondary">{event.standard}</small></td>
                        <td>{event.kind}</td>
                        <td><code>{event.from ? shorten(event.from) : "—"}</code></td>
                        <td><code>{event.to ? shorten(event.to) : "—"}</code></td>
                        <td><code>{event.amount !== undefined
                          ? formatTokenEventAmount(event, locale)
                          : formatInteger(event.token_id, locale)}</code></td>
                      </tr>
                    ))}</tbody>
                  </table>
                </div>
              )}
              {tokenIdentityCurrent && tokenTransfers.data && (
                <CursorPagination
                  busy={tokenTransfers.isFetching}
                  hasNext={Boolean(tokenTransfers.data.next_cursor)}
                  hasPrevious={tokenPager.hasPrevious}
                  label={t("transactionTabs.token-transfers")}
                  onNext={() => tokenPager.next(tokenTransfers.data?.next_cursor)}
                  onPrevious={tokenPager.previous}
                  page={tokenPager.page}
                />
              )}
            </section>
          )}

          {activeTab === "logs" && (
            <section className="panel transaction-tab-panel" role="tabpanel">
              <QueryNotice loading={logs.isPending} error={logs.error} />
              {logs.data && !logIdentityCurrent && (
                <p className="capability-panel">{t("state.transactionIdentityChanged")}</p>
              )}
              {logIdentityCurrent && logs.data?.items.length === 0 && <p className="empty-result">{t("state.noTransactionLogs")}</p>}
              {logIdentityCurrent && logs.data && logs.data.items.length > 0 && (
                <div className="transaction-log-list">
                  {logs.data.items.map((log) => (
                    <TransactionLogCard key={log.log_index} log={log} locale={locale} />
                  ))}
                </div>
              )}
              {logIdentityCurrent && logs.data && (
                <CursorPagination
                  busy={logs.isFetching} hasNext={Boolean(logs.data.next_cursor)}
                  hasPrevious={logPager.hasPrevious} label={t("transactionTabs.logs")}
                  onNext={() => logPager.next(logs.data?.next_cursor)}
                  onPrevious={logPager.previous} page={logPager.page}
                />
              )}
            </section>
          )}

          {activeTab === "trace" && (
            <section className="panel transaction-tab-panel" role="tabpanel">
              <QueryNotice loading={trace.isPending} error={trace.error} />
              {trace.data && !traceIdentityCurrent && (
                <p className="capability-panel">{t("state.transactionIdentityChanged")}</p>
              )}
              {traceIdentityCurrent && trace.data && trace.data.state !== "complete" && (
                <CapabilityDegraded stage="trace" state={trace.data.state} />
              )}
              {traceIdentityCurrent && trace.data?.state === "complete" && trace.data.frames.length === 0 && (
                <p className="empty-result">{t("state.noTraceFrames")}</p>
              )}
              {traceIdentityCurrent && trace.data?.state === "complete" && trace.data.frames.length > 0 && (
                <div className="transaction-trace-list">
                  {trace.data.frames.map((frame) => (
                    <article
                      className={frame.reverted ? "transaction-trace-frame reverted" : "transaction-trace-frame"}
                      key={frame.path.join(".") || "root"}
                      style={{ "--trace-depth": frame.depth } as React.CSSProperties}
                    >
                      <div className="transaction-trace-summary">
                        <span className="transaction-trace-kind">{frame.call_type}</span>
                        <div>
                          <strong><code>{frame.decoding?.signature ?? t(traceDecodingKey(frame.decoding?.status ?? "unavailable"))}</code></strong>
                          <code>{frame.from ? shorten(frame.from) : "—"} → {frame.to
                            ? shorten(frame.to)
                            : frame.created_address ? shorten(frame.created_address) : "—"}</code>
                          <small className="table-secondary">
                            {t("detail.executionContext", { address: frame.execution.context_address })}
                            {frame.execution.address
                              ? ` · ${t("detail.executionCode", { address: frame.execution.address })}`
                              : ` · ${t(`detail.executionResolution.${frame.execution.resolution}`)}`}
                          </small>
                        </div>
                        <span>{formatNativeAmount(frame.value, locale, nativeDecimals)} {nativeSymbol}</span>
                        <span>{frame.direct_reverted
                          ? frame.error ?? t("detail.directReverted")
                          : frame.reverted ? t("detail.ancestorReverted") : t("detail.succeeded")}</span>
                      </div>
                      <details className="transaction-more-details transaction-trace-details">
                        <summary>{t("detail.traceDetails")}</summary>
                        {frame.decoding && (
                          <>
                            <p className="quiet">{t(traceDecodingKey(frame.decoding.status))}
                              {frame.decoding.abi_source?.address
                                ? ` · ${t("detail.abiSource", { kind: frame.decoding.abi_source.kind, address: frame.decoding.abi_source.address })}`
                                : ""}
                            </p>
                            <dl className="transaction-trace-decode-status">
                              <div><dt>{t("detail.callInputs")}</dt><dd>{t(traceDecodingKey(frame.decoding.status))}</dd></div>
                              <div><dt>{t("detail.callOutputs")}</dt><dd>{t(traceOutputStatusKey(frame.decoding.output_status))}</dd></div>
                              {frame.decoding.revert && (
                                <div><dt>{t("detail.revertData")}</dt><dd>{t(traceDecodingKey(frame.decoding.revert.status))}</dd></div>
                              )}
                            </dl>
                            {frame.decoding.inputs.length > 0 && (
                              <TraceABIValues title={t("detail.callInputs")} values={frame.decoding.inputs} />
                            )}
                            {frame.decoding.outputs.length > 0 && (
                              <TraceABIValues title={t("detail.callOutputs")} values={frame.decoding.outputs} />
                            )}
                            {frame.decoding.revert && frame.decoding.revert.arguments.length > 0 && (
                              <TraceABIValues
                                title={frame.decoding.revert.signature ?? t("detail.revertData")}
                                values={frame.decoding.revert.arguments}
                              />
                            )}
                            {(frame.decoding.warning || frame.decoding.revert?.warning) && (
                              <p className="quiet">{frame.decoding.warning ?? frame.decoding.revert?.warning}</p>
                            )}
                          </>
                        )}
                        <dl className="transaction-trace-raw">
                          <div><dt>{t("detail.executionContextLabel")}</dt><dd><code>{frame.execution.context_address}</code></dd></div>
                          <div><dt>{t("detail.executionCodeLabel")}</dt><dd><code>{frame.execution.address ?? "—"}</code></dd></div>
                          <div><dt>{t("detail.codeHash")}</dt><dd><code>{frame.execution.code_hash ?? "—"}</code></dd></div>
                          <div><dt>{t("detail.input")}</dt><dd><code>{frame.input ?? "0x"}</code></dd></div>
                          <div><dt>{t("detail.output")}</dt><dd><code>{frame.output ?? "0x"}</code></dd></div>
                        </dl>
                      </details>
                    </article>
                  ))}
                </div>
              )}
            </section>
          )}

          {activeTab === "state-changes" && (
            <section className="panel transaction-tab-panel" role="tabpanel">
              <QueryNotice loading={stateChanges.isPending} error={stateChanges.error} />
              {stateChanges.data && !stateIdentityCurrent && (
                <p className="capability-panel">{t("state.transactionIdentityChanged")}</p>
              )}
              {stateIdentityCurrent && stateChanges.data?.state !== "complete" && stateChanges.data && (
                <CapabilityDegraded stage="state_diff" state={stateChanges.data.state} />
              )}
              {stateIdentityCurrent && stateChanges.data?.state === "complete" && stateGroups.length === 0 && (
                <p className="empty-result">{t("state.noTransactionStateChanges")}</p>
              )}
              {stateIdentityCurrent && stateChanges.data?.state === "complete" && stateGroups.map(([address, changes]) => (
                <article className="transaction-state-account" key={address}>
                  <header><Link to="/address/$address" params={{ address }}><code>{address}</code></Link></header>
                  <dl>{changes.map((change) => (
                    <div key={`${change.kind}:${change.storage_key ?? ""}`}>
                      <dt>{change.kind}{change.storage_key ? <code>{shorten(change.storage_key)}</code> : null}</dt>
                      <dd><code>{change.before ?? "∅"}</code><span aria-hidden="true">→</span><code>{change.after ?? "∅"}</code></dd>
                    </div>
                  ))}</dl>
                </article>
              ))}
              {stateIdentityCurrent && stateChanges.data && (
                <CursorPagination
                  busy={stateChanges.isFetching} hasNext={Boolean(stateChanges.data.next_cursor)}
                  hasPrevious={statePager.hasPrevious} label={t("transactionTabs.state-changes")}
                  onNext={() => statePager.next(stateChanges.data?.next_cursor)}
                  onPrevious={statePager.previous} page={statePager.page}
                />
              )}
            </section>
          )}
        </>
      )}
    </Page>
  );
}

function MempoolDetailExpired() {
  const { t } = useTranslation();
  return (
    <section className="capability-panel pending-unavailable" role="status">
      <span className="capability-mark" aria-hidden="true">!</span>
      <div>
        <h2>{t("pending.unavailable")}</h2>
        <p>{t("pending.expiredDetail")}</p>
        <code>snapshot_expired</code>
      </div>
    </section>
  );
}

function TransactionLogCard({
  log,
  locale,
}: {
  log: TransactionLog;
  locale: string;
}) {
  const { t } = useTranslation();
  const [topicModes, setTopicModes] = useState<Record<string, TopicDisplayMode>>({});
  const signature = log.decoding.signature ?? log.decoding.event_name
    ?? t("detail.logIndex", { index: formatInteger(log.log_index, locale) });
  const decoded = log.decoding.status === "decoded";
  const anonymous = isAnonymousDecodedLog(log.decoding.status, log.topics.length, log.decoding.arguments);
  const setTopicMode = (index: number, mode: TopicDisplayMode) => {
    setTopicModes((current) => ({ ...current, [String(index)]: mode }));
  };

  return (
    <article className="transaction-log">
      <header className="transaction-log-header">
        <div className="transaction-log-heading">
          <strong>{signature}</strong>
          {!decoded && <small className="transaction-log-status">{t(logDecodingKey(log.decoding.status))}</small>}
        </div>
        <span className="transaction-log-index">
          <span className="sr-only">{t("detail.logIndex", { index: formatInteger(log.log_index, locale) })}</span>
          {formatInteger(log.log_index, locale)}
        </span>
      </header>

      <div className="transaction-log-address">
        <span className="transaction-log-label">{t("detail.address")}</span>
        <CopyableField value={log.address}>
          <Link to="/address/$address" params={{ address: log.address }}><code>{log.address}</code></Link>
        </CopyableField>
      </div>

      {log.decoding.arguments.length > 0 && (
        <TransactionLogArgumentsTable arguments={log.decoding.arguments} />
      )}

      {log.decoding.warning && <p className="quiet transaction-log-warning">{log.decoding.warning}</p>}

      <details className="transaction-more-details transaction-log-details">
        <summary>{t("detail.moreDetails")}</summary>
        <div className="transaction-log-details-content">
          <TransactionLogProvenance log={log} />
          <TransactionLogTopicsAndData
            allowTopicZeroConversion={anonymous}
            data={log.data}
            logIndex={log.log_index}
            onTopicModeChange={setTopicMode}
            topicModes={topicModes}
            topics={log.topics}
          />
        </div>
      </details>
    </article>
  );
}

function TransactionLogArgumentsTable({
  arguments: values,
}: {
  arguments: TransactionLog["decoding"]["arguments"];
}) {
  const { t } = useTranslation();
  const rows = values.flatMap((argument, index) => flattenLogArgument(argument, index));

  return (
    <div className="transaction-log-arguments-scroll" tabIndex={0}>
      <div className="transaction-log-arguments-table" role="table" aria-label={t("detail.eventArguments")}>
        <div className="transaction-log-arguments-row transaction-log-arguments-header" role="row">
          <span role="columnheader">{t("detail.argumentName")}</span>
          <span role="columnheader">{t("detail.argumentType")}</span>
          <span role="columnheader">{t("detail.argumentIndexed")}</span>
          <span role="columnheader">{t("detail.argumentData")}</span>
        </div>
        {rows.map((row, index) => (
          <TransactionLogArgumentRow key={`${row.path}:${index}`} row={row} />
        ))}
      </div>
    </div>
  );
}

function TransactionLogArgumentRow({ row }: { row: LogArgumentRow }) {
  const { t } = useTranslation();
  const value = formatLogArgument(row.value);
  return (
    <div
      className={`transaction-log-arguments-row transaction-log-argument-depth-${Math.min(row.depth, 6)}${row.composite ? " transaction-log-argument-composite" : ""}`}
      role="row"
    >
      <span className="transaction-log-argument-name" role="cell">{row.path}</span>
      <code className="transaction-log-argument-type" role="cell">{row.type || "—"}</code>
      <span className="transaction-log-argument-indexed" role="cell">
        {row.indexed === undefined ? "—" : yesNo(row.indexed, t)}
      </span>
      <span className="transaction-log-argument-data" role="cell">
        <CopyableField value={value}><code>{value}</code></CopyableField>
      </span>
    </div>
  );
}

function TransactionLogProvenance({ log }: { log: TransactionLog }) {
  const { t } = useTranslation();
  const source = log.decoding.abi_source;
  const attribution = log.decoding.attribution;
  return (
    <section className="transaction-log-provenance" aria-labelledby={`log-provenance-${log.log_index}`}>
      <h3 id={`log-provenance-${log.log_index}`}>{t("detail.abiProvenance")}</h3>
      <div className="transaction-log-provenance-grid">
        {source && (
          <div className="transaction-log-provenance-card">
            <h4>{t("detail.abiSourceLabel")}</h4>
            <dl>
              <div>
                <dt>{t("detail.sourceKind")}</dt>
                <dd><span className="transaction-provenance-badge">{abiSourceKindLabel(source.kind, t)}</span></dd>
              </div>
              {source.address && (
                <div>
                  <dt>{t("detail.sourceAddress")}</dt>
                  <dd><CopyableField value={source.address}><Link to="/address/$address" params={{ address: source.address }}><code>{source.address}</code></Link></CopyableField></dd>
                </div>
              )}
              {source.code_hash && (
                <div>
                  <dt>{t("detail.sourceCodeHash")}</dt>
                  <dd><CopyableField value={source.code_hash}><code>{source.code_hash}</code></CopyableField></dd>
                </div>
              )}
              {log.decoding.confidence && (
                <div>
                  <dt>{t("detail.confidence")}</dt>
                  <dd><span className="transaction-provenance-badge">{confidenceLabel(log.decoding.confidence, t)}</span></dd>
                </div>
              )}
            </dl>
          </div>
        )}

        <div className="transaction-log-provenance-card">
          <h4>{t("detail.executionProvenance")}</h4>
          <dl>
            <div>
              <dt>{t("detail.emitterAddress")}</dt>
              <dd><CopyableField value={log.address}><Link to="/address/$address" params={{ address: log.address }}><code>{log.address}</code></Link></CopyableField></dd>
            </div>
            {attribution.execution_address && (
              <div>
                <dt>{t("detail.executionAddress")}</dt>
                <dd><CopyableField value={attribution.execution_address}><Link to="/address/$address" params={{ address: attribution.execution_address }}><code>{attribution.execution_address}</code></Link></CopyableField></dd>
              </div>
            )}
            <div>
              <dt>{t("detail.attribution")}</dt>
              <dd><span className="transaction-provenance-badge">{attributionLabel(attribution.mode, t)}</span></dd>
            </div>
            <div>
              <dt>{t("detail.tracePath")}</dt>
              <dd><code>[{attribution.trace_path.join(", ")}]</code></dd>
            </div>
          </dl>
        </div>
      </div>
    </section>
  );
}

function TransactionLogTopicsAndData({
  allowTopicZeroConversion,
  data,
  logIndex,
  onTopicModeChange,
  topicModes,
  topics,
}: {
  allowTopicZeroConversion: boolean;
  data: string;
  logIndex: string;
  onTopicModeChange: (index: number, mode: TopicDisplayMode) => void;
  topicModes: Record<string, TopicDisplayMode>;
  topics: readonly string[];
}) {
  const { t } = useTranslation();
  return (
    <>
      <section className="transaction-log-topics" aria-labelledby={`log-topics-${logIndex}`}>
        <h3 id={`log-topics-${logIndex}`}>{t("detail.topics")}</h3>
        <div className="transaction-topic-list">
          {topics.map((topic, index) => {
            const convertible = index > 0 || allowTopicZeroConversion;
            const mode = topicModes[String(index)] ?? "hex";
            const value = formatTopicValue(topic, mode);
            const displayValue = value ?? t("detail.topicUnavailable");
            return (
              <div
                className={`transaction-topic${convertible ? " transaction-topic-convertible" : ""}`}
                key={`${topic}:${index}`}
              >
                <span className="transaction-topic-index" aria-hidden="true">{index}</span>
                {!convertible ? (
                  <CopyableField value={topic}><code>{topic}</code></CopyableField>
                ) : (
                  <>
                    <label className="sr-only" htmlFor={`topic-mode-${logIndex}-${index}`}>
                      {t("detail.topicMode", { index })}
                    </label>
                    <select
                      aria-label={t("detail.topicMode", { index })}
                      className="topic-mode-select"
                      id={`topic-mode-${logIndex}-${index}`}
                      onChange={(event) => onTopicModeChange(index, event.target.value as TopicDisplayMode)}
                      value={mode}
                    >
                      <option value="hex">{t("detail.topicModes.hex")}</option>
                      <option value="address">{t("detail.topicModes.address")}</option>
                      <option value="text">{t("detail.topicModes.text")}</option>
                      <option value="number">{t("detail.topicModes.number")}</option>
                    </select>
                    <CopyableField value={value ?? topic}><code>{displayValue}</code></CopyableField>
                  </>
                )}
              </div>
            );
          })}
        </div>
      </section>
      <section className="transaction-log-data" aria-labelledby={`log-data-${logIndex}`}>
        <h3 id={`log-data-${logIndex}`}>{t("detail.data")}</h3>
        <div className="transaction-log-data-value">
          <CopyableField value={data}><code>{data}</code></CopyableField>
        </div>
      </section>
    </>
  );
}

function TraceABIValues({ title, values }: {
  title: string;
  values: Array<{ name: string; type: string; value: unknown }>;
}) {
  return (
    <section className="transaction-trace-values">
      <h4>{title}</h4>
      <dl>{values.map((value, index) => (
        <div key={`${value.name}:${value.type}:${index}`}>
          <dt>{value.name || `${index}`} <code>{value.type}</code></dt>
          <dd><code>{formatLogArgument(value.value)}</code></dd>
        </div>
      ))}</dl>
    </section>
  );
}

function TransactionDetailRow({
  label,
  children,
  wide,
}: {
  label: string;
  children: React.ReactNode;
  wide?: boolean;
}) {
  return <div className={wide ? "transaction-detail-row detail-item wide" : "transaction-detail-row detail-item"}>
    <dt>{label}</dt><dd>{children}</dd>
  </div>;
}

function MempoolTransactionOverview({
  detail,
  locale,
  nativeDecimals,
  nativeSymbol,
}: {
  detail: Exclude<TransactionDetail, { kind: "included" }>;
  locale: string;
  nativeDecimals: number;
  nativeSymbol: string;
}) {
  const { t } = useTranslation();
  const transaction = detail.transaction;
  const pending = detail.kind === "pending";
  return (
    <div className="transaction-overview mempool-transaction-overview">
      <section className={`panel mempool-transaction-state ${pending ? "pending" : "replaced"}`} role="status">
        <TransactionStatus
          label={t(pending ? "transactionStatus.pending" : "transactionStatus.replaced")}
          status={pending ? "pending" : "replaced"}
        />
        <div>
          <h2>{t(pending ? "detail.pendingTitle" : "detail.replacedTitle")}</h2>
          <p>{t(pending ? "detail.pendingDetail" : "detail.replacedDetail")}</p>
        </div>
      </section>
      <section className="panel transaction-detail-card" aria-label={t("detail.transactionSummary")}>
        <h2 className="sr-only">{t("detail.transactionSummary")}</h2>
        <dl className="transaction-detail-list">
          <TransactionDetailRow label={t("table.hash")}>
            <CopyableField value={transaction.hash}><code>{transaction.hash}</code></CopyableField>
          </TransactionDetailRow>
          <TransactionDetailRow label={t("table.status")}>
            <TransactionStatus
              label={t(pending ? "transactionStatus.pending" : "transactionStatus.replaced")}
              status={pending ? "pending" : "replaced"}
            />
          </TransactionDetailRow>
          {!pending ? (
            <TransactionDetailRow label={t("detail.replacementHash")}>
              <CopyableField value={detail.replacement_hash}>
                <Link to="/tx/$hash" params={{ hash: detail.replacement_hash }} search={{ tab: "overview" }}>
                  <code>{detail.replacement_hash}</code>
                </Link>
              </CopyableField>
            </TransactionDetailRow>
          ) : null}
          {transaction.replaces_hash ? (
            <TransactionDetailRow label={t("detail.replacesHash")}>
              <CopyableField value={transaction.replaces_hash}>
                <Link to="/tx/$hash" params={{ hash: transaction.replaces_hash }} search={{ tab: "overview" }}>
                  <code>{transaction.replaces_hash}</code>
                </Link>
              </CopyableField>
            </TransactionDetailRow>
          ) : null}
          <TransactionDetailRow label={t("table.from")}>
            <CopyableField value={transaction.from}>
              <Link to="/address/$address" params={{ address: transaction.from }}><code>{transaction.from}</code></Link>
            </CopyableField>
          </TransactionDetailRow>
          <TransactionDetailRow label={t("table.to")}>
            {transaction.to ? (
              <CopyableField value={transaction.to}>
                <Link to="/address/$address" params={{ address: transaction.to }}><code>{transaction.to}</code></Link>
              </CopyableField>
            ) : t("common.contractCreation")}
          </TransactionDetailRow>
          <TransactionDetailRow label={t("table.value", { symbol: nativeSymbol })}>
            <strong>{formatNativeAmount(transaction.value, locale, nativeDecimals)} {nativeSymbol}</strong>
          </TransactionDetailRow>
          <TransactionDetailRow label={t("detail.nonce")}>
            {formatInteger(transaction.nonce, locale)}
          </TransactionDetailRow>
          <TransactionDetailRow label={t("detail.type")}>
            {transactionTypeLabel(transaction.type, t)}
          </TransactionDetailRow>
          <TransactionDetailRow label={t("detail.gasLimit")}>
            {formatInteger(transaction.gas, locale)}
          </TransactionDetailRow>
          <TransactionDetailRow label={t("detail.gasFees")}>
            <FeeSettings locale={locale} entries={[
              { label: t("pending.gasPrice"), value: transaction.gas_price },
              { label: t("detail.feeMax"), value: transaction.max_fee_per_gas },
              { label: t("detail.feeMaxPriority"), value: transaction.max_priority_fee_per_gas },
            ]} />
          </TransactionDetailRow>
          <TransactionDetailRow label={t("detail.firstSeen")}>
            <time dateTime={transaction.first_seen_at}>{formatTimestamp(transaction.first_seen_at, locale)}</time>
          </TransactionDetailRow>
          <TransactionDetailRow label={t("detail.lastSeen")}>
            <time dateTime={transaction.last_seen_at}>{formatTimestamp(transaction.last_seen_at, locale)}</time>
          </TransactionDetailRow>
          {!pending ? (
            <TransactionDetailRow label={t("detail.replacedAt")}>
              <time dateTime={detail.replaced_at}>{formatTimestamp(detail.replaced_at, locale)}</time>
            </TransactionDetailRow>
          ) : null}
          <TransactionDetailRow label={t("detail.expiresAt")}>
            <time dateTime={transaction.expires_at}>{formatTimestamp(transaction.expires_at, locale)}</time>
          </TransactionDetailRow>
          <TransactionDetailRow label={t("detail.endpoint")}><code>{transaction.endpoint}</code></TransactionDetailRow>
          <TransactionDetailRow label={t("detail.input")} wide>
            <textarea
              aria-label={t("detail.rawCalldataValue", { mode: t("detail.rawHex") })}
              className="transaction-calldata-raw-value transaction-data"
              readOnly
              rows={4}
              spellCheck={false}
              value={transaction.input}
              wrap="soft"
            />
          </TransactionDetailRow>
        </dl>
      </section>
    </div>
  );
}

function TransactionStatusBadge({ transaction }: { transaction: TransactionSummary }) {
  const { t } = useTranslation();
  const state = !transaction.canonical
    ? "orphan"
    : transaction.status === "success" ? "success"
    : transaction.status === "failed" ? "failed"
    : "unknown";
  return <span className="transaction-status-group">
    <TransactionStatus
      label={state === "orphan" ? t("detail.orphaned") : transactionStatusLabel(transaction.status, t)}
      status={state}
    />
    {transaction.canonical && transaction.finality === "finalized"
      ? <span className="finality-badge finalized">{finalityLabel(transaction.finality, t)}</span>
      : null}
  </span>;
}

function IncludedTransactionStatus({ transaction }: { transaction: TransactionSummary }) {
  const { t } = useTranslation();
  const state: TransactionVisualStatus = !transaction.canonical
    ? "orphan"
    : transaction.status === "success" ? "success"
    : transaction.status === "failed" ? "failed"
    : transaction.status === "pending" ? "pending"
    : "unknown";
  return (
    <TransactionStatus
      label={state === "orphan" ? t("detail.orphaned") : transactionStatusLabel(transaction.status, t)}
      status={state}
    />
  );
}

type TransactionActionEvidence =
  | { state: "loading" }
  | { state: "unavailable" }
  | {
      state: "current";
      resolution: TransactionCalldataResource["execution"]["resolution"];
    };

function transactionCalldataIdentityMatches(
  transaction: TransactionSummary,
  resource: TransactionCalldataResource,
): boolean {
  return resource.state === "complete"
    && Boolean(transaction.to)
    && Boolean(transaction.block_hash)
    && resource.transaction_hash.toLowerCase() === transaction.hash.toLowerCase()
    && resource.block_hash.toLowerCase() === transaction.block_hash?.toLowerCase()
    && resource.block_number === transaction.block_number
    && resource.transaction_index === String(transaction.transaction_index)
    && resource.input.toLowerCase() === transaction.input.toLowerCase()
    && resource.execution.context_address.toLowerCase() === transaction.to?.toLowerCase();
}

function transactionCalldataRetryKey(
  transaction: TransactionSummary,
  resource: TransactionCalldataResource,
): string | undefined {
  if (!transaction.to || !transaction.block_hash) return undefined;
  return [
    "calldata",
    transaction.hash,
    resource.transaction_hash,
    transaction.block_hash,
    resource.block_hash,
    transaction.block_number,
    resource.block_number,
    String(transaction.transaction_index),
    resource.transaction_index,
    transaction.input,
    resource.input,
    transaction.to,
    resource.execution.context_address,
  ].join(":").toLowerCase();
}

function transactionActionLabel(
  transaction: TransactionSummary,
  evidence: TransactionActionEvidence,
  t: Translate,
): string {
  if (!transaction.to) return t("detail.actionContractCreation");
  if (evidence.state === "loading") return t("detail.actionDetermining");
  if (evidence.state === "unavailable") return t("detail.actionUnavailable");
  if (evidence.resolution === "direct" || evidence.resolution === "eip7702_delegate") {
    return t("detail.actionContractCall");
  }
  if (evidence.resolution === "empty") {
    return transaction.input === "0x"
      ? t("detail.actionNativeTransfer")
      : t("detail.actionEOATransaction");
  }
  return t("detail.actionUnavailable");
}

type AddressTab =
  | "transactions"
  | "internal-transactions"
  | "withdrawals"
  | "erc20-transfers"
  | "nft-transfers"
  | "assets"
  | "delegation"
  | "contract";

function AddressDetailPage({ address, tab }: { address: string; tab: string }) {
  const { i18n, t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const requestedHash = location.hash.replace(/^#/u, "");
  const contractHash = isContractTabHash(requestedHash);
  const delegationHash = isDelegatedAccountTabHash(requestedHash);
  const transactionPager = useCursorHistory(`address-transactions:${address}`);
  const internalPager = useCursorHistory(`address-internal-transactions:${address}`);
  const withdrawalPager = useCursorHistory(`address-withdrawals:${address}`);
  const erc20Pager = useCursorHistory(`address-erc20-transfers:${address}`);
  const nftTransferPager = useCursorHistory(`address-nft-transfers:${address}`);
  const erc20BalancePager = useCursorHistory(`address-erc20-balances:${address}`);
  const nftPager = useCursorHistory(`address-nfts:${address}`);
  const account = useAddress(address);
  const contractAvailable = account.data?.type === "contract" && Boolean(account.data.code_hash);
  const currentlyDelegated = account.data?.type === "delegated_eoa";
  const delegationAvailable = currentlyDelegated || Boolean(account.data?.has_delegation_history);
  const activeTab: AddressTab = account.data?.type === "contract" && contractHash
    ? "contract"
    : (account.isPending || delegationAvailable) && delegationHash
      ? "delegation"
      : isAddressTab(tab) && (tab !== "delegation" || account.isPending || delegationAvailable) ? tab : "transactions";
  const transactions = useAddressTransactions(
    address,
    transactionPager.cursor,
    CORE_PAGE_SIZE,
    transactionPager.refreshGeneration,
    activeTab === "transactions" && isAddress(address),
  );
  const internalTransactions = useAddressInternalTransactions(
    address,
    internalPager.cursor,
    CORE_PAGE_SIZE,
    internalPager.refreshGeneration,
    activeTab === "internal-transactions" && isAddress(address),
  );
  const withdrawals = useAddressWithdrawals(
    address,
    withdrawalPager.cursor,
    CORE_PAGE_SIZE,
    withdrawalPager.refreshGeneration,
    activeTab === "withdrawals" && isAddress(address),
  );
  const erc20Transfers = useAddressERC20Transfers(
    address,
    erc20Pager.cursor,
    CORE_PAGE_SIZE,
    erc20Pager.refreshGeneration,
    activeTab === "erc20-transfers" && isAddress(address),
  );
  const nftTransfers = useAddressNFTTransfers(
    address,
    nftTransferPager.cursor,
    CORE_PAGE_SIZE,
    nftTransferPager.refreshGeneration,
    activeTab === "nft-transfers" && isAddress(address),
  );
  const nfts = useAddressNFTBalances(
    address,
    nftPager.cursor,
    CORE_PAGE_SIZE,
    nftPager.refreshGeneration,
    activeTab === "assets" && isAddress(address),
  );
  const erc20Balances = useAddressERC20Balances(
    address,
    erc20BalancePager.cursor,
    CORE_PAGE_SIZE,
    erc20BalancePager.refreshGeneration,
    activeTab === "assets" && isAddress(address),
  );
  const publicConfig = usePublicConfig();
  const nativeDecimals = publicConfig.data?.native_decimals ?? 18;
  const nativeSymbol = publicConfig.data?.native_symbol ?? "";
  const locale = i18n.resolvedLanguage ?? "en";
  const displayAddress = account.data?.address ?? address;
  const title = account.data?.type === "contract"
    ? t("page.contract")
    : account.data?.type === "delegated_eoa"
      ? t("page.delegatedAccount")
      : t("page.address");
  const qrPayload = publicConfig.data
    ? `ethereum:${displayAddress}@${publicConfig.data.chain_id}`
    : undefined;
  useEffect(() => {
    if (!contractHash || delegationHash || account.isPending || account.error || contractAvailable) return;
    void navigate({
      to: "/address/$address",
      params: { address },
      search: {},
      hash: "",
      replace: true,
    });
  }, [account.error, account.isPending, address, contractAvailable, contractHash, delegationHash, navigate]);

  return (
    <Page
      title={title}
      description={(
        <AddressHeader address={displayAddress} qrPayload={qrPayload} />
      )}
      mono
    >
      <QueryNotice loading={account.isPending} error={account.error} />
      {account.data && (
        <>
          <DetailList label={t("detail.addressSummary")}>
            <Detail
              label={t("detail.nativeBalance", { symbol: nativeSymbol })}
              value={`${formatNativeAmount(account.data.balance, locale, nativeDecimals)} ${nativeSymbol}`.trim()}
            />
            <Detail label={t("detail.nonce")} value={formatInteger(account.data.nonce, locale)} />
            <AddressOriginDetails origin={account.data.origin} />
          </DetailList>
          <p className="context-note" role="note">{t("context.addressSnapshot")}</p>
        </>
      )}
      <nav className="transaction-tabs" aria-label={t("detail.addressSections")}>
        {([
          ["transactions", t("addressTab.transactions")],
          ["internal-transactions", t("addressTab.internalTransactions")],
          ["withdrawals", t("addressTab.withdrawals")],
          ["erc20-transfers", t("addressTab.erc20Transfers")],
          ["nft-transfers", t("addressTab.nftTransfers")],
          ["assets", t("addressTab.assets")],
        ] as const).map(([tabID, label]) => (
          <Link
            key={tabID}
            activeOptions={{ exact: true, includeHash: true }}
            activeProps={{ className: "" }}
            className={activeTab === tabID ? "transaction-tab active" : "transaction-tab"}
            to="/address/$address"
            params={{ address }}
            search={{ tab: tabID }}
            hash={() => ""}
            aria-current={activeTab === tabID ? "page" : undefined}
          >
            {label}
          </Link>
        ))}
        {contractAvailable ? (
          <Link
            activeOptions={{ exact: true, includeHash: true }}
            activeProps={{ className: "" }}
            aria-current={activeTab === "contract" ? "page" : undefined}
            className={activeTab === "contract" ? "transaction-tab active" : "transaction-tab"}
            hash="code"
            params={{ address: account.data?.address ?? address }}
            search={{}}
            to="/address/$address"
          >
            {t("addressTab.contract")}
          </Link>
        ) : null}
        {delegationAvailable ? (
          <Link
            activeOptions={{ exact: true, includeHash: true }}
            activeProps={{ className: "" }}
            aria-current={activeTab === "delegation" ? "page" : undefined}
            className={activeTab === "delegation" ? "transaction-tab active" : "transaction-tab"}
            hash={currentlyDelegated ? "code" : "history"}
            params={{ address }}
            search={{ tab: "delegation" }}
            to="/address/$address"
          >
            {t("addressTab.delegation")}
          </Link>
        ) : null}
      </nav>
      {activeTab === "transactions" && (
        <AddressTransactions
          address={address}
          busy={transactions.isFetching}
          error={transactions.error}
          hasNext={Boolean(transactions.data?.next_cursor)}
          items={transactions.data?.items}
          loading={transactions.isPending}
          locale={locale}
          nativeDecimals={nativeDecimals}
          nativeSymbol={nativeSymbol}
          onNext={() => transactionPager.next(transactions.data?.next_cursor)}
          onPrevious={transactionPager.previous}
          onReset={transactionPager.reset}
          page={transactionPager.page}
          hasPrevious={transactionPager.hasPrevious}
        />
      )}
      {activeTab === "internal-transactions" && (
        <AddressInternalTransactions
          address={address}
          busy={internalTransactions.isFetching}
          error={internalTransactions.error}
          hasNext={Boolean(internalTransactions.data?.next_cursor)}
          items={internalTransactions.data?.items}
          loading={internalTransactions.isPending}
          locale={locale}
          nativeDecimals={nativeDecimals}
          nativeSymbol={nativeSymbol}
          onNext={() => internalPager.next(internalTransactions.data?.next_cursor)}
          onPrevious={internalPager.previous}
          onReset={internalPager.reset}
          page={internalPager.page}
          hasPrevious={internalPager.hasPrevious}
        />
      )}
      {activeTab === "withdrawals" && (
        <AddressWithdrawals
          busy={withdrawals.isFetching}
          error={withdrawals.error}
          hasNext={Boolean(withdrawals.data?.next_cursor)}
          hasPrevious={withdrawalPager.hasPrevious}
          items={withdrawals.data?.items}
          loading={withdrawals.isPending}
          locale={locale}
          onNext={() => withdrawalPager.next(withdrawals.data?.next_cursor)}
          onPrevious={withdrawalPager.previous}
          onReset={withdrawalPager.reset}
          page={withdrawalPager.page}
        />
      )}
      {activeTab === "erc20-transfers" && (
        <AddressTokenTransfers
          address={address}
          busy={erc20Transfers.isFetching}
          error={erc20Transfers.error}
          hasNext={Boolean(erc20Transfers.data?.next_cursor)}
          items={erc20Transfers.data?.items}
          loading={erc20Transfers.isPending}
          locale={locale}
          onNext={() => erc20Pager.next(erc20Transfers.data?.next_cursor)}
          onPrevious={erc20Pager.previous}
          onReset={erc20Pager.reset}
          page={erc20Pager.page}
          hasPrevious={erc20Pager.hasPrevious}
          title={t("addressTab.erc20Transfers")}
        />
      )}
      {activeTab === "nft-transfers" && (
        <AddressTokenTransfers
          address={address}
          busy={nftTransfers.isFetching}
          error={nftTransfers.error}
          hasNext={Boolean(nftTransfers.data?.next_cursor)}
          items={nftTransfers.data?.items}
          loading={nftTransfers.isPending}
          locale={locale}
          onNext={() => nftTransferPager.next(nftTransfers.data?.next_cursor)}
          onPrevious={nftTransferPager.previous}
          onReset={nftTransferPager.reset}
          page={nftTransferPager.page}
          hasPrevious={nftTransferPager.hasPrevious}
          title={t("addressTab.nftTransfers")}
        />
      )}
      {activeTab === "assets" && (
        <div className="address-assets">
          <AddressERC20Balances
            balances={erc20Balances.data?.items}
            busy={erc20Balances.isFetching}
            coverageEnd={erc20Balances.data?.meta.coverage_end}
            error={erc20Balances.error}
            hasNext={Boolean(erc20Balances.data?.next_cursor)}
            loading={erc20Balances.isPending}
            locale={locale}
            onNext={() => erc20BalancePager.next(erc20Balances.data?.next_cursor)}
            onPrevious={erc20BalancePager.previous}
            onReset={erc20BalancePager.reset}
            page={erc20BalancePager.page}
            hasPrevious={erc20BalancePager.hasPrevious}
          />
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
        </div>
      )}
      {activeTab === "contract" && contractAvailable ? <ContractPage address={address} /> : null}
      {activeTab === "delegation" && delegationAvailable ? (
        <DelegatedAccountPanel authority={displayAddress} currentlyDelegated={currentlyDelegated} />
      ) : null}
    </Page>
  );
}

function AddressHeader({ address, qrPayload }: { address: string; qrPayload?: string }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const openButtonRef = useRef<HTMLButtonElement>(null);
  const dialogRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    dialogRef.current?.focus();
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      setOpen(false);
    };
    window.addEventListener("keydown", closeOnEscape);
    return () => {
      window.removeEventListener("keydown", closeOnEscape);
      openButtonRef.current?.focus();
    };
  }, [open]);

  return (
    <>
      <span className="address-header-actions">
        <CopyableField value={address}><code>{address}</code></CopyableField>
        {qrPayload ? (
          <button
            aria-label={t("actions.showQRCode")}
            aria-haspopup="dialog"
            className="address-qr-button"
            onClick={() => setOpen(true)}
            ref={openButtonRef}
            type="button"
          >
            <span aria-hidden="true">▦</span>
          </button>
        ) : null}
      </span>
      {open && qrPayload ? (
        <div
          className="dialog-backdrop"
          onMouseDown={(event) => {
            if (event.currentTarget === event.target) setOpen(false);
          }}
          role="presentation"
        >
          <div
            aria-labelledby="address-qr-title"
            aria-modal="true"
            className="qr-dialog"
            onKeyDown={(event) => {
              if (event.key !== "Tab") return;
              const focusable = [...event.currentTarget.querySelectorAll<HTMLElement>(
                "button:not([disabled]), a[href], [tabindex]:not([tabindex='-1'])",
              )];
              if (focusable.length === 0) {
                event.preventDefault();
                event.currentTarget.focus();
                return;
              }
              const first = focusable[0]!;
              const last = focusable.at(-1)!;
              if (event.shiftKey && document.activeElement === first) {
                event.preventDefault();
                last.focus();
              } else if (!event.shiftKey && document.activeElement === last) {
                event.preventDefault();
                first.focus();
              } else if (document.activeElement === event.currentTarget) {
                event.preventDefault();
                (event.shiftKey ? last : first).focus();
              }
            }}
            ref={dialogRef}
            role="dialog"
            tabIndex={-1}
          >
            <div className="qr-dialog-heading">
              <h2 id="address-qr-title">{t("detail.addressQRCode")}</h2>
              <button
                aria-label={t("common.close")}
                className="dialog-close"
                onClick={() => setOpen(false)}
                type="button"
              >
                ×
              </button>
            </div>
            <div className="qr-code-surface">
              <QRCodeSVG
                bgColor="#ffffff"
                fgColor="#111111"
                includeMargin
                level="M"
                size={224}
                title={t("detail.addressQRCode")}
                value={qrPayload}
              />
            </div>
            <CopyableField value={qrPayload}><code>{qrPayload}</code></CopyableField>
          </div>
        </div>
      ) : null}
    </>
  );
}

function AddressOriginDetails({ origin }: { origin?: AddressSummary["origin"] }) {
  const { t } = useTranslation();
  if (!origin) {
    return <Detail label={t("detail.origin")} value={t("state.originUnavailable")} wide />;
  }
  const blockOrigin = origin.kind === "withdrawal" || origin.kind === "block_fee_recipient";
  const sourceLabel = origin.kind === "contract_creation"
    ? t("detail.contractCreator")
    : origin.kind === "withdrawal"
      ? t("detail.blockWithdrawal")
      : origin.kind === "block_fee_recipient"
        ? t("detail.blockFeeRecipient")
        : t("detail.fundedBy");
  const transactionLabel = origin.kind === "contract_creation"
    ? t("detail.creationTransaction")
    : t("detail.fundingTransaction");
  if (origin.state === "genesis") {
    return (
      <>
        <Detail label={sourceLabel} value={t("state.originGenesis")} />
        <Detail label={transactionLabel} value={t("state.originGenesis")} />
      </>
    );
  }
  if (origin.state === "unavailable") {
    return <Detail label={sourceLabel} value={t("state.originUnavailable")} wide />;
  }
  if (origin.state === "not_found") {
    return <Detail label={sourceLabel} value="—" />;
  }
  if (blockOrigin) {
    return (
      <>
        <Detail
          label={sourceLabel}
          mono
          value={origin.block_hash ? (
            <Link to="/blocks/$blockID" params={{ blockID: origin.block_hash }} search={{ tab: "overview" }}>
              {origin.block_hash}
            </Link>
          ) : undefined}
        />
        {origin.kind === "withdrawal" ? (
          <Detail label={t("detail.withdrawalIndex")} value={origin.withdrawal_index} mono />
        ) : null}
      </>
    );
  }
  return (
    <>
      <Detail
        label={sourceLabel}
        mono
        value={origin.source_address ? (
          <CopyableField value={origin.source_address}>
            <Link
              params={{ address: origin.source_address }}
              search={{ tab: "transactions" }}
              to="/address/$address"
            >
              {origin.source_address}
            </Link>
          </CopyableField>
        ) : undefined}
      />
      <Detail
        label={transactionLabel}
        mono
        value={origin.transaction_hash ? (
          <CopyableField value={origin.transaction_hash}>
            <Link
              params={{ hash: origin.transaction_hash }}
              search={{ tab: "overview" }}
              to="/tx/$hash"
            >
              {origin.transaction_hash}
            </Link>
          </CopyableField>
        ) : undefined}
      />
    </>
  );
}

function isAddressTab(tab: string): tab is AddressTab {
  return [
    "transactions",
    "internal-transactions",
    "withdrawals",
    "erc20-transfers",
    "nft-transfers",
    "assets",
    "delegation",
  ].includes(tab);
}

interface AddressActivityProps {
  address: string;
  busy: boolean;
  error: unknown;
  hasNext: boolean;
  hasPrevious: boolean;
  loading: boolean;
  locale: string;
  onNext: () => void;
  onPrevious: () => void;
  onReset: () => void;
  page: number;
}

function AddressTransactions({
  address,
  busy,
  error,
  hasNext,
  hasPrevious,
  items,
  loading,
  locale,
  nativeDecimals,
  nativeSymbol,
  onNext,
  onPrevious,
  onReset,
  page,
}: AddressActivityProps & {
  items?: TransactionSummary[];
  nativeDecimals: number;
  nativeSymbol: string;
}) {
  const { t } = useTranslation();
  return (
    <AddressActivitySection
      busy={busy}
      error={error}
      hasNext={hasNext}
      hasPrevious={hasPrevious}
      loading={loading}
      onNext={onNext}
      onPrevious={onPrevious}
      onReset={onReset}
      page={page}
      title={t("addressTab.transactions")}
      empty={items?.length === 0}
    >
      {items && items.length > 0 ? (
        <AddressActivityTable label={t("addressTab.transactions")} nativeSymbol={nativeSymbol} status>
          {items.map((transaction) => {
            const destination = transaction.to ?? transaction.contract_address;
            return (
              <tr key={`${transaction.block_hash}:${transaction.hash}`}>
                <ActivityIdentity
                  blockNumber={transaction.block_number}
                  hash={transaction.hash}
                  timestamp={transaction.block_timestamp}
                  locale={locale}
                />
                <td><TransactionStatusBadge transaction={transaction} /></td>
                <AddressCell address={transaction.from} currentAddress={address} />
                <DirectionCell
                  direction={addressDirection(address, transaction.from, destination)}
                />
                <AddressCell
                  address={destination}
                  created={!transaction.to && Boolean(destination)}
                  currentAddress={address}
                />
                <td><code>{formatNativeAmount(transaction.value, locale, nativeDecimals)}</code></td>
              </tr>
            );
          })}
        </AddressActivityTable>
      ) : null}
    </AddressActivitySection>
  );
}

function AddressInternalTransactions({
  address,
  busy,
  error,
  hasNext,
  hasPrevious,
  items,
  loading,
  locale,
  nativeDecimals,
  nativeSymbol,
  onNext,
  onPrevious,
  onReset,
  page,
}: AddressActivityProps & {
  items?: AddressInternalTransaction[];
  nativeDecimals: number;
  nativeSymbol: string;
}) {
  const { t } = useTranslation();
  return (
    <AddressActivitySection
      busy={busy}
      error={error}
      hasNext={hasNext}
      hasPrevious={hasPrevious}
      loading={loading}
      onNext={onNext}
      onPrevious={onPrevious}
      onReset={onReset}
      page={page}
      title={t("addressTab.internalTransactions")}
      empty={items?.length === 0}
    >
      {items && items.length > 0 ? (
        <AddressActivityTable
          label={t("addressTab.internalTransactions")}
          action
          nativeSymbol={nativeSymbol}
        >
          {items.map((transaction) => {
            const destination = transaction.created_address ?? transaction.to;
            return (
              <tr key={`${transaction.block_hash}:${transaction.transaction_hash}:${transaction.path.join(".")}`}>
                <ActivityIdentity
                  blockNumber={transaction.block_number}
                  hash={transaction.transaction_hash}
                  timestamp={transaction.block_timestamp}
                  locale={locale}
                />
                <td>
                  <span className="table-primary">
                    {transaction.call_type}
                    {transaction.reverted ? <small>{t("activity.reverted")}</small> : null}
                  </span>
                </td>
                <AddressCell address={transaction.from} currentAddress={address} />
                <DirectionCell
                  direction={addressDirection(address, transaction.from, destination)}
                />
                <AddressCell
                  address={destination}
                  created={Boolean(transaction.created_address)}
                  currentAddress={address}
                />
                <td><code>{formatNativeAmount(transaction.value, locale, nativeDecimals)}</code></td>
              </tr>
            );
          })}
        </AddressActivityTable>
      ) : null}
    </AddressActivitySection>
  );
}

function AddressWithdrawals({
  busy,
  error,
  hasNext,
  hasPrevious,
  items,
  loading,
  locale,
  onNext,
  onPrevious,
  onReset,
  page,
}: Omit<AddressActivityProps, "address"> & { items?: AddressWithdrawal[] }) {
  const { t } = useTranslation();
  const title = t("addressTab.withdrawals");
  return (
    <section className="detail-section address-activity" aria-label={title}>
      <QueryNotice loading={loading} error={error} onReset={onReset} />
      {items?.length === 0 ? (
        <p className="empty-result" role="status">{t("state.noAddressWithdrawals")}</p>
      ) : null}
      {items && items.length > 0 ? (
        <div className="table-scroll" tabIndex={0} aria-label={title}>
          <table className="address-activity-table">
            <caption className="sr-only">{title}</caption>
            <thead>
              <tr>
                <th>{t("detail.withdrawalIndex")}</th>
                <th>{t("detail.validatorIndex")}</th>
                <th>{t("table.block")}</th>
                <th>{t("table.age")}</th>
                <th>{t("detail.withdrawalAmount")}</th>
              </tr>
            </thead>
            <tbody>
              {items.map((withdrawal) => (
                <tr key={`${withdrawal.block_hash}:${withdrawal.index}`}>
                  <td><code>{formatInteger(withdrawal.index, locale)}</code></td>
                  <td><code>{formatInteger(withdrawal.validator_index, locale)}</code></td>
                  <td>
                    <Link to="/blocks/$blockID" params={{ blockID: withdrawal.block_hash }}>
                      {formatInteger(withdrawal.block_number, locale)}
                    </Link>
                  </td>
                  <td>{formatRelativeTimestamp(withdrawal.block_timestamp, locale)}</td>
                  <td><code>{formatEtherFromGwei(withdrawal.amount, locale)} Ether</code></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
      {!loading && !error ? (
        <CursorPagination
          busy={busy}
          hasNext={hasNext}
          hasPrevious={hasPrevious}
          label={t("pagination.addressWithdrawals")}
          onNext={onNext}
          onPrevious={onPrevious}
          page={page}
        />
      ) : null}
    </section>
  );
}

function AddressTokenTransfers({
  address,
  busy,
  error,
  hasNext,
  hasPrevious,
  items,
  loading,
  locale,
  onNext,
  onPrevious,
  onReset,
  page,
  title,
}: AddressActivityProps & { items?: AddressTokenTransfer[]; title: string }) {
  const { t } = useTranslation();
  return (
    <AddressActivitySection
      busy={busy}
      error={error}
      hasNext={hasNext}
      hasPrevious={hasPrevious}
      loading={loading}
      onNext={onNext}
      onPrevious={onPrevious}
      onReset={onReset}
      page={page}
      title={title}
      empty={items?.length === 0}
    >
      {items && items.length > 0 ? (
        <AddressActivityTable label={title} token>
          {items.map((transfer) => (
            <tr key={`${transfer.block_hash}:${transfer.transaction_hash}:${transfer.log_index}:${transfer.sub_index}`}>
              <ActivityIdentity
                blockNumber={transfer.block_number}
                hash={transfer.transaction_hash}
                timestamp={transfer.block_timestamp}
                locale={locale}
              />
              <td>
                <span className="table-primary">
                  <CopyableField value={transfer.token_address}>
                    {sameAddress(transfer.token_address, address) ? (
                      <code>{shorten(transfer.token_address)}</code>
                    ) : (
                      <Link to="/token/$address" params={{ address: transfer.token_address }}>
                        <code>{shorten(transfer.token_address)}</code>
                      </Link>
                    )}
                  </CopyableField>
                  <small>{tokenStandardLabel(transfer.standard, t)}</small>
                </span>
              </td>
              <AddressCell address={transfer.from} currentAddress={address} />
              <DirectionCell direction={addressDirection(address, transfer.from, transfer.to)} />
              <AddressCell address={transfer.to} currentAddress={address} />
              <td>
                <span className="table-primary">
                  <code>
                    {transfer.amount !== undefined
                      ? formatTokenEventAmount(transfer, locale)
                      : transfer.token_id !== undefined ? `#${transfer.token_id}` : "—"}
                  </code>
                  {transfer.amount !== undefined && transfer.token_id !== undefined
                    ? <small>#{transfer.token_id}</small>
                    : null}
                </span>
              </td>
            </tr>
          ))}
        </AddressActivityTable>
      ) : null}
    </AddressActivitySection>
  );
}

function AddressActivitySection({
  busy,
  children,
  empty,
  error,
  hasNext,
  hasPrevious,
  loading,
  onNext,
  onPrevious,
  onReset,
  page,
  title,
}: Omit<AddressActivityProps, "address" | "locale"> & {
  children: React.ReactNode;
  empty: boolean;
  title: string;
}) {
  const { t } = useTranslation();
  return (
    <section className="detail-section address-activity" aria-label={title}>
      <QueryNotice loading={loading} error={error} onReset={onReset} />
      {empty ? <p className="empty-result" role="status">{t("state.noAddressActivity")}</p> : null}
      {children}
      {!loading && !error ? (
        <CursorPagination
          busy={busy}
          hasNext={hasNext}
          hasPrevious={hasPrevious}
          label={t("pagination.addressActivity")}
          onNext={onNext}
          onPrevious={onPrevious}
          page={page}
        />
      ) : null}
    </section>
  );
}

function AddressActivityTable({
  action,
  children,
  label,
  nativeSymbol,
  status,
  token,
}: {
  action?: boolean;
  children: React.ReactNode;
  label: string;
  nativeSymbol?: string;
  status?: boolean;
  token?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <div className="table-scroll" tabIndex={0} aria-label={label}>
      <table className="address-activity-table">
        <caption className="sr-only">{label}</caption>
        <thead>
          <tr>
            <th>{t("table.hash")}</th>
            <th>{t("table.block")}</th>
            <th>{t("table.age")}</th>
            {status ? <th>{t("table.status")}</th> : null}
            {action ? <th>{t("table.action")}</th> : null}
            {token ? <th>{t("table.token")}</th> : null}
            <th>{t("table.from")}</th>
            <th>{t("table.direction")}</th>
            <th>{t("table.to")}</th>
            <th>
              {token
                ? t("detail.amountOrTokenID")
                : t("detail.nativeAmount", { symbol: nativeSymbol ?? "" })}
            </th>
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
    </div>
  );
}

function ActivityIdentity({
  blockNumber,
  hash,
  locale,
  timestamp,
}: {
  blockNumber?: string;
  hash: string;
  locale: string;
  timestamp?: string;
}) {
  return (
    <>
      <td>
        <Link to="/tx/$hash" params={{ hash }} search={{ tab: "overview" }}>
          <code>{shorten(hash)}</code>
        </Link>
      </td>
      <td>
        {blockNumber ? (
          <Link to="/blocks/$blockID" params={{ blockID: blockNumber }}>
            {formatInteger(blockNumber, locale)}
          </Link>
        ) : "—"}
      </td>
      <td>{timestamp ? formatRelativeTimestamp(timestamp, locale) : "—"}</td>
    </>
  );
}

function AddressCell({
  address,
  created,
  currentAddress,
}: {
  address?: string;
  created?: boolean;
  currentAddress: string;
}) {
  const { t } = useTranslation();
  if (!address) return <td>—</td>;
  return (
    <td>
      <span className="table-primary">
        <CopyableField value={address}>
          {sameAddress(address, currentAddress) ? (
            <code>{shorten(address)}</code>
          ) : (
            <Link to="/address/$address" params={{ address }} search={{ tab: "transactions" }}>
              <code>{shorten(address)}</code>
            </Link>
          )}
        </CopyableField>
        {created ? <small>{t("activity.created")}</small> : null}
      </span>
    </td>
  );
}

function sameAddress(left: string, right: string): boolean {
  return left.toLowerCase() === right.toLowerCase();
}

function DirectionCell({ direction }: { direction: "in" | "out" | "self" }) {
  const { t } = useTranslation();
  return (
    <td>
      <span className={`address-direction ${direction}`}>
        {t(`activity.direction.${direction}`)}
      </span>
    </td>
  );
}

function addressDirection(
  address: string,
  from?: string,
  destination?: string,
): "in" | "out" | "self" {
  const subject = address.toLowerCase();
  const outgoing = from?.toLowerCase() === subject;
  const incoming = destination?.toLowerCase() === subject;
  if (outgoing && incoming) return "self";
  return outgoing ? "out" : "in";
}

function AddressERC20Balances({
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
  balances?: ERC20Balance[];
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
    <section className="detail-section" aria-labelledby="erc20-balances-title">
      <h2 id="erc20-balances-title">{t("detail.erc20Balances")}</h2>
      {coverageEnd ? (
        <p className="context-note" role="note">
          {t("detail.assetSnapshot", { block: formatInteger(coverageEnd, locale) })}
        </p>
      ) : null}
      <QueryNotice loading={loading} error={error} onReset={onReset} />
      {balances && balances.length === 0 ? (
        <p className="empty-result" role="status">{t("state.noERC20Balances")}</p>
      ) : null}
      {balances && balances.length > 0 ? (
        <div className="table-scroll" tabIndex={0} aria-label={t("detail.erc20Balances")}>
          <table>
            <caption className="sr-only">{t("detail.erc20BalanceDescription")}</caption>
            <thead>
              <tr>
                <th>{t("table.token")}</th>
                <th>{t("detail.balance")}</th>
                <th>{t("table.confidence")}</th>
              </tr>
            </thead>
            <tbody>
              {balances.map((balance) => {
                const amount = balance.decimals === undefined
                  ? formatInteger(balance.balance, locale)
                  : formatNativeAmount(balance.balance, locale, balance.decimals);
                const tokenLabel = balance.symbol ?? balance.token_address;
                return (
                  <tr key={balance.token_address}>
                    <td>
                      <span className="table-primary">
                        <Link to="/token/$address" params={{ address: balance.token_address }}>
                          {balance.name ?? tokenLabel}
                        </Link>
                        <small><code>{balance.symbol ?? shorten(balance.token_address)}</code></small>
                      </span>
                    </td>
                    <td>
                      <code>{amount}{balance.symbol ? ` ${balance.symbol}` : ""}</code>
                    </td>
                    <td><span className="result-kind">{confidenceLabel(balance.confidence, t)}</span></td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      ) : null}
      {balances ? (
        <CursorPagination
          busy={busy}
          hasNext={hasNext}
          hasPrevious={hasPrevious}
          label={t("pagination.erc20Balances")}
          onNext={onNext}
          onPrevious={onPrevious}
          page={page}
        />
      ) : null}
    </section>
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
                hash="code"
                params={{ address: token.data.address }}
                search={{}}
                to="/address/$address"
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
                    <Link to="/tx/$hash" params={{ hash: event.transaction_hash }} search={{ tab: "overview" }}>
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
                  <td><code>{formatTokenEventAmount(event, locale)}</code></td>
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

function transactionTypeLabel(value: string | undefined, t: Translate): string {
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
    case "full": return t("verificationMatch.full");
    case "partial": return t("verificationMatch.partial");
    default: return "—";
  }
}

function verificationLanguageLabel(value: string, t: Translate): string {
  switch (value) {
    case "solidity": return t("verificationLanguage.solidity");
    case "yul": return t("verificationLanguage.yul");
    case "geas": return t("verificationLanguage.geas");
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

export function VerifyPage({ initialAddress = "" }: { initialAddress?: string }) {
  const { t } = useTranslation();
  const publicConfig = usePublicConfig();
  const [apiKey, setAPIKey] = useState("");
  const [submittedAPIKey, setSubmittedAPIKey] = useState("");
  const [address, setAddress] = useState(initialAddress);
  const [language, setLanguage] = useState<VerificationSubmission["language"]>("solidity");
  const [inputKind, setInputKind] = useState<VerificationSubmission["input_kind"]>("standard_json");
  const [compilerVersion, setCompilerVersion] = useState("");
  const [standardJSON, setStandardJSON] = useState('{\n  "language": "Solidity",\n  "sources": {},\n  "settings": {}\n}');
  const [multipartSources, setMultipartSources] = useState('{\n  "Contract.sol": "contract Contract {}"\n}');
  const [geasSources, setGeasSources] = useState('{\n  "main.eas": "push 1"\n}');
  const [runtimeEntrypoint, setRuntimeEntrypoint] = useState("main.eas");
  const [creationEntrypoint, setCreationEntrypoint] = useState("");
  const [contractName, setContractName] = useState("");
  const [formError, setFormError] = useState<string>();
  const submissionEnabled =
    publicConfig.isSuccess && publicConfig.data.features.verification === true;
  const compilerCatalog = useCompilerCatalog(language, submissionEnabled);
  const submission = useSubmitVerification(address, apiKey);
  const job = useVerificationJob(
    submission.data?.id ?? "",
    submittedAPIKey,
    submission.data ? 1 : 0,
    Boolean(submission.data),
  );
  const currentJob = job.data ?? submission.data;

  useEffect(() => {
    setAddress(initialAddress);
  }, [initialAddress]);

  useEffect(() => {
    const versions = compilerCatalog.data?.versions ?? [];
    if (versions.length > 0 && !versions.includes(compilerVersion)) {
      setCompilerVersion(versions[0] ?? "");
    }
  }, [compilerCatalog.data, compilerVersion]);

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setFormError(undefined);
    submission.reset();
    if (!submissionEnabled) return;

    const isGeas = language === "geas";
    const rawInput = isGeas
      ? geasSources
      : inputKind === "standard_json"
        ? standardJSON
        : multipartSources;
    if (new TextEncoder().encode(rawInput).byteLength > MAX_STANDARD_JSON_BYTES) {
      setFormError(t("verification.inputTooLarge"));
      return;
    }

    let parsed: unknown;
    try {
      assertNoDuplicateJSONKeys(rawInput);
      parsed = JSON.parse(rawInput) as unknown;
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
      (isGeas && !runtimeEntrypoint.trim())
    ) {
      setFormError(t("verification.invalidFields"));
      return;
    }
    if (
      (inputKind === "multipart" || isGeas) &&
      (Object.keys(parsed).length === 0 || Object.values(parsed).some((value) => typeof value !== "string"))
    ) {
      setFormError(t(isGeas ? "verification.invalidGeasSources" : "verification.invalidMultipart"));
      return;
    }

    setSubmittedAPIKey(apiKey);
    const request: VerificationSubmission = {
      compiler_version: compilerVersion.trim(),
      input_kind: isGeas ? "geas_sources" : inputKind,
      language,
    };
    if (isGeas) {
      request.sources = parsed as Record<string, string>;
      request.runtime_entrypoint = runtimeEntrypoint.trim();
      if (creationEntrypoint.trim()) request.creation_entrypoint = creationEntrypoint.trim();
      if (contractName.trim()) request.contract_name_hint = contractName.trim();
    } else if (inputKind === "standard_json") {
      request.input = parsed as Record<string, unknown>;
    } else {
      request.sources = parsed as Record<string, string>;
    }
    submission.mutate(request);
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
                <select
                  id="verification-language"
                  value={language}
                  onChange={(event) => {
                    const nextLanguage = event.target.value as VerificationSubmission["language"];
                    setLanguage(nextLanguage);
                    setInputKind((current) => nextLanguage === "geas"
                      ? "geas_sources"
                      : current === "geas_sources" ? "standard_json" : current);
                  }}
                >
                  <option value="solidity">{t("verificationLanguage.solidity")}</option>
                  <option value="yul">{t("verificationLanguage.yul")}</option>
                  <option value="geas">{t("verificationLanguage.geas")}</option>
                </select>
              </label>
              <label className="field-control" htmlFor="verification-input-kind">
                <span>{t("verification.inputKind")}</span>
                <select
                  disabled={language === "geas"}
                  id="verification-input-kind"
                  value={inputKind}
                  onChange={(event) => setInputKind(event.target.value as VerificationSubmission["input_kind"])}
                >
                  {language === "geas" ? (
                    <option value="geas_sources">{t("verification.geasSources")}</option>
                  ) : (
                    <>
                      <option value="standard_json">{t("verification.standardJSON")}</option>
                      <option value="multipart">{t("verification.multipart")}</option>
                    </>
                  )}
                </select>
              </label>
              <label className="field-control" htmlFor="verification-compiler">
                <span>{t("verification.compilerVersion")}</span>
                <select
                  disabled={compilerCatalog.isPending || !compilerCatalog.data}
                  id="verification-compiler"
                  onChange={(event) => setCompilerVersion(event.target.value)}
                  value={compilerVersion}
                >
                  {(compilerCatalog.data?.versions ?? []).map((version) => <option key={version} value={version}>{version}</option>)}
                </select>
                <QueryNotice loading={compilerCatalog.isPending} error={compilerCatalog.error} />
              </label>
              {language === "geas" ? (
                <>
                  <FormField
                    id="verification-runtime-entrypoint"
                    label={t("verification.runtimeEntrypoint")}
                    onChange={setRuntimeEntrypoint}
                    value={runtimeEntrypoint}
                  />
                  <FormField
                    id="verification-creation-entrypoint"
                    label={t("verification.creationEntrypoint")}
                    onChange={setCreationEntrypoint}
                    value={creationEntrypoint}
                  />
                  <FormField
                    id="verification-contract-name"
                    label={t("verification.contractName")}
                    onChange={setContractName}
                    value={contractName}
                  />
                </>
              ) : null}
              <label className="field-control wide" htmlFor="verification-input">
                <span>{language === "geas"
                  ? t("verification.geasSources")
                  : inputKind === "standard_json"
                    ? t("verification.standardJSON")
                    : t("verification.multipartSources")}</span>
                <textarea
                  id="verification-input"
                  spellCheck={false}
                  value={language === "geas"
                    ? geasSources
                    : inputKind === "standard_json" ? standardJSON : multipartSources}
                  onChange={(event) => language === "geas"
                    ? setGeasSources(event.target.value)
                    : inputKind === "standard_json"
                      ? setStandardJSON(event.target.value)
                      : setMultipartSources(event.target.value)}
                />
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
  const success = job?.outcome?.kind === "verification_success"
    ? job.outcome as VerificationSuccess
    : undefined;
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
          <div><dt>{t("verification.jobKind")}</dt><dd><code>{job.kind}</code></dd></div>
          <div><dt>{t("table.status")}</dt><dd><span className={`job-status ${job.status}`}>{verificationJobStatusLabel(job.status, t)}</span></dd></div>
          <div><dt>{t("verification.result")}</dt><dd><code>{job.outcome?.kind ?? "—"}</code></dd></div>
          <div><dt>{t("verification.runtimeMatch")}</dt><dd>{verificationMatchLabel(success?.runtime_match?.match_type, t)}</dd></div>
          <div><dt>{t("verification.creationMatch")}</dt><dd>{verificationMatchLabel(success?.creation_match?.match_type, t)}</dd></div>
          <div><dt>{t("verification.errorCode")}</dt><dd><code>{job.error_code ?? "—"}</code></dd></div>
          <div><dt>{t("verification.updated")}</dt><dd>{job.updated_at}</dd></div>
        </dl>
      )}
      {success?.creation_match && (
        <VerificationMatchView title={t("verification.creationTransformations")} match={success.creation_match} />
      )}
      {success?.runtime_match && (
        <VerificationMatchView title={t("verification.runtimeTransformations")} match={success.runtime_match} />
      )}
      {job?.outcome?.kind === "batch_results" && (
        <TextArtifact title={t("verification.batchResults")} value={job.outcome.results} />
      )}
    </section>
  );
}

function VerificationMatchView({ title, match }: { title: string; match: VerificationMatchDetails }) {
  return (
    <section className="artifact-panel">
      <h3>{title}</h3>
      <p><code>{match.match_type}</code></p>
      <pre tabIndex={0}>{JSON.stringify({
        transformations: match.transformations,
        values: match.values,
      }, null, 2)}</pre>
    </section>
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

function logDecodingKey(status: string) {
  switch (status) {
    case "decoded": return "detail.logDecoded" as const;
    case "ambiguous": return "detail.logAmbiguous" as const;
    case "unknown": return "detail.logUnknown" as const;
    case "malformed": return "detail.logMalformed" as const;
    default: return "detail.logUnavailable" as const;
  }
}

function abiSourceKindLabel(value: string, t: Translate): string {
  switch (value) {
    case "exact_address": return t("detail.abiSourceKinds.exactAddress");
    case "code_hash": return t("detail.abiSourceKinds.codeHash");
    case "proxy_implementation": return t("detail.abiSourceKinds.proxyImplementation");
    case "signature_database": return t("detail.abiSourceKinds.signatureDatabase");
    case "builtin": return t("detail.abiSourceKinds.builtin");
    default: return value;
  }
}

function attributionLabel(value: string, t: Translate): string {
  return value === "exact_trace"
    ? t("detail.attributionKinds.exactTrace")
    : t("detail.attributionKinds.addressFallback");
}

function traceDecodingKey(status: string) {
  switch (status) {
    case "decoded": return "detail.traceDecoded" as const;
    case "ambiguous": return "detail.traceAmbiguous" as const;
    case "unknown": return "detail.traceUnknown" as const;
    case "malformed": return "detail.traceMalformed" as const;
    case "not_applicable": return "detail.traceNotApplicable" as const;
    default: return "detail.traceUnavailable" as const;
  }
}

function traceOutputStatusKey(status: string) {
  switch (status) {
    case "decoded": return "detail.outputDecoded" as const;
    case "empty": return "detail.outputEmpty" as const;
    case "unknown": return "detail.outputUnknown" as const;
    case "malformed": return "detail.outputMalformed" as const;
    case "not_applicable": return "detail.outputNotApplicable" as const;
    default: return "detail.outputUnavailable" as const;
  }
}

function formatLogArgument(value: unknown): string {
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
