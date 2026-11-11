import { Card } from "@/components/ui/card";
import {
  Table,
  TableCaption,
  TableHeader,
  TableRow,
  TableHead,
  TableBody,
  TableCell,
} from "@/components/ui/table";
import { NavigationTabs, NavigationTab } from "@/components/ui/tabs";
import { Alert } from "@/components/ui/alert";
import { useCursorHistory } from "@/api/pagination";
import { RequestsPanel } from "./RequestsPanel";
import { ConsensusPanel } from "./ConsensusPanels";
import { BlobsPanel } from "./BlobsPanel";
import { Link, useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { usePublicConfig } from "@/api/publicConfig";
import { useBlock, useBlockTransactions } from "@/api/hooks";
import type { BlockSummary, TransactionSummary } from "@/api/types";
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
  yesNo,
} from "./pages";
import { TransactionStatusBadge } from "@/components/TransactionStatus";

const BLOCK_TABS = [
  "overview",
  "transactions",
  "withdrawals",
  "consensus",
  "requests",
  "blobs",
] as const;
type BlockTab = (typeof BLOCK_TABS)[number];

function blockTabsForBlock(block?: BlockSummary, consensus = false, blobs = false): BlockTab[] {
  return BLOCK_TABS.filter(
    (tab) =>
      (tab !== "withdrawals" || Boolean(block?.withdrawals)) &&
      (tab !== "consensus" || consensus) &&
      (tab !== "requests" || consensus) &&
      (tab !== "blobs" || blobs),
  );
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
    <Card asChild>
      <section className="panel transaction-tab-panel" aria-labelledby="block-withdrawals-title">
        <h2 id="block-withdrawals-title">{t("detail.withdrawals")}</h2>
        {withdrawals.length === 0 ? (
          <p className="empty-result">{t("state.noWithdrawals")}</p>
        ) : (
          <div className="table-scroll" tabIndex={0}>
            <Table>
              <TableCaption className="sr-only">{t("detail.withdrawals")}</TableCaption>
              <TableHeader>
                <TableRow>
                  <TableHead className="numeric">{t("detail.withdrawalIndex")}</TableHead>
                  <TableHead className="numeric">{t("detail.validatorIndex")}</TableHead>
                  <TableHead>{t("table.address")}</TableHead>
                  <TableHead className="numeric">{t("detail.withdrawalAmount")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {withdrawals.map((withdrawal) => (
                  <TableRow key={withdrawal.index}>
                    <TableCell className="numeric">
                      <code>{formatInteger(withdrawal.index, locale)}</code>
                    </TableCell>
                    <TableCell className="numeric">
                      <code>{formatInteger(withdrawal.validator_index, locale)}</code>
                    </TableCell>
                    <TableCell>
                      <AddressIdentity address={withdrawal.address} compact={false} />
                    </TableCell>
                    <TableCell className="numeric">
                      <code>{formatEtherFromGwei(withdrawal.amount, locale)} Ether</code>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </section>
    </Card>
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
    <Card asChild>
      <section
        className="panel transaction-tab-panel"
        role="tabpanel"
        aria-label={t("blockTabs.transactions")}
      >
        <QueryNotice loading={loading} error={error} onReset={onReset} />
        {items?.length === 0 ? <p className="empty-result">{t("state.noTransactions")}</p> : null}
        {items && items.length > 0 ? (
          <div className="table-scroll" tabIndex={0} aria-label={t("blockTabs.transactions")}>
            <Table>
              <TableCaption className="sr-only">{t("blockTabs.transactions")}</TableCaption>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("table.hash")}</TableHead>
                  <TableHead>{t("detail.transactionIndex")}</TableHead>
                  <TableHead>{t("table.status")}</TableHead>
                  <TableHead>{t("table.from")}</TableHead>
                  <TableHead>{t("table.to")}</TableHead>
                  <TableHead className="numeric">
                    {t("table.value", { symbol: nativeSymbol })}
                  </TableHead>
                  <TableHead>{t("detail.gasUsed")}</TableHead>
                  <TableHead>{t("table.finality")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((transaction) => (
                  <TableRow
                    key={`${blockHash}:${transaction.transaction_index ?? transaction.hash}:${transaction.hash}`}
                  >
                    <TableCell>
                      <Link
                        to="/tx/$hash"
                        params={{ hash: transaction.hash }}
                        search={{ tab: "overview" }}
                      >
                        <code>{shorten(transaction.hash)}</code>
                      </Link>
                    </TableCell>
                    <TableCell className="numeric">
                      <code>
                        {transaction.transaction_index == null
                          ? "—"
                          : formatInteger(String(transaction.transaction_index), locale)}
                      </code>
                    </TableCell>
                    <TableCell>
                      <TransactionStatusBadge transaction={transaction} />
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
                      <code>{formatNativeAmount(transaction.value, locale, nativeDecimals)}</code>
                    </TableCell>
                    <TableCell className="numeric">
                      <code>
                        {transaction.gas_used ? formatInteger(transaction.gas_used, locale) : "—"}
                      </code>
                    </TableCell>
                    <TableCell>
                      <FinalityBadge finality={transaction.finality} />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        ) : null}
        {items ? (
          <CursorPagination
            itemCount={items?.length ?? 0}
            onReset={onReset}
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
    </Card>
  );
}

export function BlockDetailPage({ identifier, tab }: { identifier: string; tab: string }) {
  const { i18n, t } = useTranslation();
  const navigate = useNavigate();
  const block = useBlock(identifier);
  const pager = useCursorHistory(`block-transactions:${block.data?.hash ?? identifier}`);
  const publicConfig = usePublicConfig();
  const blockTabs = blockTabsForBlock(
    block.data,
    publicConfig.data?.features.consensus,
    publicConfig.data?.features.blobs,
  );
  const activeTab: BlockTab = blockTabs.includes(tab as BlockTab) ? (tab as BlockTab) : "overview";
  const transactions = useBlockTransactions(
    block.data?.hash ?? "",
    pager.request,
    activeTab === "transactions" && Boolean(block.data),
  );
  const locale = i18n.resolvedLanguage ?? "en";
  const nativeDecimals = publicConfig.data?.native_decimals ?? 18;
  const nativeSymbol = publicConfig.data?.native_symbol ?? "";

  return (
    <Page title={t("page.block")} description={identifier} mono>
      <QueryNotice loading={block.isLoading} error={block.error} />
      {block.data && (
        <>
          {!block.data.canonical && <ReorgContext kind="block" hash={block.data.hash} />}
          <NavigationTabs
            className="transaction-tabs"
            role="tablist"
            aria-label={t("detail.blockSections")}
          >
            {blockTabs.map((tabID) => (
              <NavigationTab key={tabID}>
                <Link
                  aria-selected={activeTab === tabID}
                  className={activeTab === tabID ? "transaction-tab active" : "transaction-tab"}

                  onKeyDown={(event) => {
                    if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
                    event.preventDefault();
                    const currentIndex = blockTabs.indexOf(tabID);
                    const nextIndex =
                      event.key === "Home"
                        ? 0
                        : event.key === "End"
                          ? blockTabs.length - 1
                          : event.key === "ArrowLeft"
                            ? (currentIndex - 1 + blockTabs.length) % blockTabs.length
                            : (currentIndex + 1) % blockTabs.length;
                    const nextTab = blockTabs[nextIndex]!;
                    void navigate({
                      to: "/blocks/$blockID",
                      params: { blockID: identifier },
                      search: { tab: nextTab },
                    }).then(() => {
                      const tabs =
                        event.currentTarget.parentElement?.querySelectorAll<HTMLElement>(
                          '[role="tab"]',
                        );
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
              </NavigationTab>
            ))}
          </NavigationTabs>
          {activeTab === "overview" && (
            <>
              <DetailList label={t("detail.blockSummary")}>
                <Detail label={t("table.block")} value={formatInteger(block.data.number, locale)} />
                {block.data.slot_number !== undefined && (
                  <Detail
                    label={`${t("detail.slotNumber")} · ${t(`consensus.sources.${block.data.slot_number_source ?? "execution"}`)}`}
                    value={formatInteger(block.data.slot_number, locale)}
                  />
                )}
                <Detail label={t("table.hash")} value={block.data.hash} mono />
                {block.data.block_access_list_hash !== undefined && (
                  <Detail
                    label={t("detail.blockAccessListHash")}
                    value={block.data.block_access_list_hash}
                    mono
                  />
                )}
                <Detail
                  label={t("detail.parentHash")}
                  mono
                  value={
                    block.data.number === "0" ? (
                      block.data.parent_hash
                    ) : (
                      <Link to="/blocks/$blockID" params={{ blockID: block.data.parent_hash }}>
                        {block.data.parent_hash}
                      </Link>
                    )
                  }
                />
                <Detail
                  label={t("table.age")}
                  value={formatTimestamp(block.data.timestamp, locale)}
                />
                <Detail
                  label={t("table.transactions")}
                  value={formatInteger(block.data.transaction_count, locale)}
                />
                <Detail label={t("table.gas")} value={formatInteger(block.data.gas_used, locale)} />
                <Detail
                  label={t("detail.gasLimit")}
                  value={formatInteger(block.data.gas_limit, locale)}
                />
                <Detail
                  label={t("detail.baseFee")}
                  value={formatInteger(block.data.base_fee_per_gas, locale)}
                />
                <Detail
                  label={t("detail.miner")}
                  value={
                    block.data.miner ? (
                      <AddressIdentity address={block.data.miner} compact={false} />
                    ) : undefined
                  }
                />
                <Detail label={t("detail.canonical")} value={yesNo(block.data.canonical, t)} />
                <Detail label={t("table.finality")} value={finalityLabel(block.data.finality, t)} />
              </DetailList>
              {block.data.number === "0" && block.data.canonical && (
                <Alert asChild>
                  <p className="context-note">
                    <Link to="/genesis">{t("actions.viewGenesisAccounts")}</Link>
                  </p>
                </Alert>
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
              loading={transactions.isLoading}
              locale={locale}
              nativeDecimals={nativeDecimals}
              nativeSymbol={nativeSymbol}
              onNext={() => pager.next(transactions.data?.next_cursor)}
              onPrevious={pager.previous}
              onReset={pager.reset}
              page={pager.page}
            />
          )}
          {activeTab === "requests" && <RequestsPanel hash={block.data.hash} />}
          {activeTab === "consensus" && <ConsensusPanel hash={block.data.hash} />}
          {activeTab === "blobs" && <BlobsPanel hash={block.data.hash} />}
          {activeTab === "withdrawals" && block.data.withdrawals && (
            <BlockWithdrawalsPanel withdrawals={block.data.withdrawals} locale={locale} />
          )}
        </>
      )}
    </Page>
  );
}
