
import { Link, useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import {
  useBlock,
  useBlockTransactions,
  usePublicConfig,
} from "@/api/hooks";
import type {
  BlockSummary,
  TransactionSummary,
} from "@/api/types";
import {
  formatEtherFromGwei,
  formatInteger,
  formatNativeAmount,
  formatTimestamp,
  shorten,
} from "@/components/format";
import { AddressIdentity } from "@/ens/AddressIdentity";
import { QueryNotice } from "@/components/QueryNotice";
import {
  CursorPagination,
  Detail,
  DetailList,
  FinalityBadge,
  Page,
  ReorgContext,
  finalityLabel,
  useCursorHistory,
  yesNo,
} from "./pages";
import { TransactionStatusBadge } from "./TransactionPage";

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
                <td><AddressIdentity address={withdrawal.address} compact={false} /></td>
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
                <td><AddressIdentity address={transaction.from} /></td>
                <td>{transaction.to ? <AddressIdentity address={transaction.to} /> : t("common.contractCreation")}</td>
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

export function BlockDetailPage({ identifier, tab }: { identifier: string; tab: string }) {
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
                  value={block.data.miner ? (
                    <AddressIdentity address={block.data.miner} compact={false} />
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
