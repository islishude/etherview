import { NavigationTabs, NavigationTab } from "@/components/ui/tabs";
import {
  Table,
  TableCaption,
  TableHeader,
  TableRow,
  TableHead,
  TableBody,
  TableCell,
} from "@/components/ui/table";

import "@/i18n/resources/tokenPage";
import { BrowsingScope, BrowsingEmpty } from "@/components/BrowsingScope";

import { useBrowsingCursorHistory, useCursorHistory } from "@/api/pagination";

import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { useToken, useTokenHolders, useTokenHolderCount, useTokenTransfers } from "@/api/hooks";
import type { TokenEvent, TokenHolder, TokenHolderMeta } from "@/api/types";

import { formatInteger, formatTokenAmount, shorten } from "@/components/format";
import { CopyableField } from "@/components/CopyButton";
import { AddressIdentity } from "@/ens/AddressIdentity";
import { QueryNotice } from "@/components/QueryNotice";
import {
  CORE_PAGE_SIZE,
  CursorPagination,
  Detail,
  DetailList,
  Page,
  confidenceLabel,
  formatTokenEventAmount,
  isNFTStandard,
  stageStateLabel,
  tokenEventKindLabel,
  tokenStandardLabel,
} from "./pages";

export function TokenDetailPage({
  address,
  tab = "transfers",
}: {
  address: string;
  tab?: "transfers" | "holders" | "details";
}) {
  const { i18n, t } = useTranslation();
  const transferPager = useBrowsingCursorHistory(`token-transfers:${address}`);
  const holderPager = useCursorHistory(`token-holders:${address}`);
  const token = useToken(address);
  const activeTab =
    tab === "holders" && token.data && token.data.standard !== "erc20" ? "transfers" : tab;
  const transfers = useTokenTransfers(
    address,
    transferPager.request,
    activeTab === "transfers",
    CORE_PAGE_SIZE,
  );
  const holders = useTokenHolders(
    address,
    holderPager.request,
    token.data?.standard === "erc20" && activeTab === "holders",
  );
  const holderSummary = useTokenHolderCount(address, token.data?.standard === "erc20");
  const locale = i18n.resolvedLanguage ?? "en";
  const holderData = holderSummary.error ? undefined : holderSummary.data;
  const holderMeta = holderData?.meta;
  const supply = holderMeta?.total_supply ?? token.data?.total_supply;
  const supplyBlock = holderMeta?.snapshot_block_number ?? token.data?.observed_block_number;
  const supplyHash = holderMeta?.snapshot_block_hash ?? token.data?.observed_block_hash;

  return (
    <Page
      title={token.data?.name ?? token.data?.symbol ?? t("page.token")}
      description={address}
      mono
    >
      <QueryNotice loading={token.isLoading} error={token.error} />
      {token.data && (
        <DetailList label={t("tokenPage.summary")}>
          <Detail label={t("detail.name")} value={token.data.name} />
          <Detail label={t("detail.symbol")} value={token.data.symbol} />
          <Detail label={t("table.standard")} value={tokenStandardLabel(token.data.standard, t)} />
          <Detail
            label={t("tokenPage.contract")}
            mono
            value={
              <CopyableField value={address}>
                <Link to="/address/$address" params={{ address }} search={{}}>
                  {address}
                </Link>
              </CopyableField>
            }
          />
          <Detail
            label={t("tokenPage.totalSupply")}
            value={
              <>
                <span>{formatTokenAmount(supply, token.data.decimals, locale)}</span>
                {supply !== undefined && token.data.decimals === undefined && (
                  <small> {t("tokenPage.rawUnits")}</small>
                )}
                {supply !== undefined && supplyHash && (
                  <small className="table-secondary">
                    {t(holderMeta ? "tokenPage.snapshotSupply" : "tokenPage.observedSupply")}{" "}
                    <Link to="/blocks/$blockID" params={{ blockID: supplyHash }}>
                      {formatInteger(supplyBlock, locale)}
                    </Link>
                  </small>
                )}
              </>
            }
          />
          {token.data.standard === "erc20" && (
            <Detail
              label={t("tokenHolder.count")}
              value={
                <>
                  <span>{formatInteger(holderData?.data.holder_count, locale)}</span>
                  {activeTab !== "holders" && (
                    <QueryNotice
                      compact
                      loading={holderSummary.isLoading}
                      error={holderSummary.error}
                    />
                  )}
                </>
              }
            />
          )}
        </DetailList>
      )}
      <NavigationTabs className="transaction-tabs" aria-label={t("tokenPage.sections")}>
        {(
          [
            ["transfers", t("tokenPage.transfers")],
            ...(token.data?.standard === "erc20"
              ? ([["holders", t("tokenHolder.title")]] as const)
              : []),
            ["details", t("tokenPage.details")],
          ] as const
        ).map(([tabID, label]) => (
          <NavigationTab key={tabID}>
            <Link
              to="/token/$address"
              params={{ address }}
              search={{ tab: tabID }}
              activeProps={{ className: "" }}
              className={activeTab === tabID ? "transaction-tab active" : "transaction-tab"}
              aria-current={activeTab === tabID ? "page" : undefined}
            >
              {label}
            </Link>
          </NavigationTab>
        ))}
      </NavigationTabs>
      {activeTab === "details" && token.data && (
        <DetailList label={t("detail.tokenMetadata")}>
          <Detail label={t("table.confidence")} value={confidenceLabel(token.data.confidence, t)} />
          <Detail label={t("detail.decimals")} value={token.data.decimals?.toString()} />
          <Detail
            label={t("table.metadata")}
            value={stageStateLabel(token.data.metadata_state, t)}
          />
          <Detail
            label={t("detail.codeHash")}
            mono
            value={
              <Link
                hash="code"
                params={{ address: token.data.address }}
                search={{}}
                to="/address/$address"
              >
                {token.data.code_hash}
              </Link>
            }
          />
          <Detail
            label={t("detail.observedBlock")}
            value={formatInteger(token.data.observed_block_number, locale)}
          />
          <Detail
            label={t("detail.observedBlockHash")}
            mono
            value={
              <Link to="/blocks/$blockID" params={{ blockID: token.data.observed_block_hash }}>
                {token.data.observed_block_hash}
              </Link>
            }
          />
          {holderMeta && (
            <>
              <Detail
                label={t("tokenHolder.snapshot")}
                value={formatInteger(holderMeta.snapshot_block_number, locale)}
              />
              <Detail
                label={t("tokenHolder.snapshotHash")}
                mono
                value={
                  <Link to="/blocks/$blockID" params={{ blockID: holderMeta.snapshot_block_hash }}>
                    {holderMeta.snapshot_block_hash}
                  </Link>
                }
              />
            </>
          )}
        </DetailList>
      )}
      {activeTab === "holders" && token.data?.standard === "erc20" && (
        <TokenHolders
          busy={holders.isFetching}
          decimals={token.data.decimals}
          error={holders.error}
          hasNext={Boolean(holders.data?.next_cursor)}
          hasPrevious={holderPager.hasPrevious}
          holders={holders.error ? undefined : holders.data?.items}
          meta={holders.error ? undefined : holders.data?.meta}
          loading={holders.isLoading}
          locale={locale}
          onNext={() => holderPager.next(holders.data?.next_cursor)}
          onPrevious={holderPager.previous}
          onReset={holderPager.reset}
          page={holderPager.page}
        />
      )}
      {activeTab === "transfers" && (
        <BrowsingScope status={transfers.data?.meta.browsing}>
          <TokenTransfers
            busy={transfers.isFetching}
            error={transfers.error}
            events={transfers.data?.items}
            hasNext={Boolean(transfers.data?.next_cursor)}
            hasPrevious={transferPager.hasPrevious}
            loading={transfers.isLoading}
            locale={locale}
            onNext={() => transferPager.next(transfers.data?.next_cursor)}
            onPrevious={transferPager.previous}
            onReset={transferPager.reset}
            page={transferPager.page}
          />
        </BrowsingScope>
      )}
    </Page>
  );
}

function TokenHolders({
  busy,
  decimals,
  error,
  hasNext,
  hasPrevious,
  holders,
  loading,
  locale,
  meta,
  onNext,
  onPrevious,
  onReset,
  page,
}: {
  busy: boolean;
  decimals?: number;
  error: unknown;
  hasNext: boolean;
  hasPrevious: boolean;
  holders?: TokenHolder[];
  loading: boolean;
  locale: string;
  meta?: TokenHolderMeta;
  onNext: () => void;
  onPrevious: () => void;
  onReset: () => void;
  page: number;
}) {
  const { t } = useTranslation();
  return (
    <section className="detail-section" aria-labelledby="token-holders-title">
      <h2 id="token-holders-title">{t("tokenHolder.title")}</h2>
      <QueryNotice loading={loading} error={error} onReset={onReset} />
      {meta && (
        <p className="table-secondary">
          {t("tokenHolder.snapshot")}:{" "}
          <Link
            to="/blocks/$blockID"
            params={{ blockID: meta.snapshot_block_hash }}
            title={meta.snapshot_block_hash}
          >
            {formatInteger(meta.snapshot_block_number, locale)}
          </Link>
        </p>
      )}
      {holders && holders.length === 0 && (
        <p className="empty-result" role="status">
          {t("tokenHolder.empty")}
        </p>
      )}
      {holders && holders.length > 0 && (
        <div className="table-scroll" tabIndex={0} aria-label={t("tokenHolder.title")}>
          <Table>
            <TableCaption className="sr-only">{t("tokenHolder.caption")}</TableCaption>
            <TableHeader>
              <TableRow>
                <TableHead>{t("tokenHolder.address")}</TableHead>
                <TableHead className="numeric">{t("detail.amount")}</TableHead>
                <TableHead className="numeric">{t("detail.observedBlock")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {holders.map((holder) => (
                <TableRow key={holder.holder_address}>
                  <TableCell>
                    <AddressIdentity address={holder.holder_address} />
                  </TableCell>
                  <TableCell className="numeric">
                    {formatTokenAmount(holder.balance, decimals, locale)}
                  </TableCell>
                  <TableCell className="numeric">
                    <Link to="/blocks/$blockID" params={{ blockID: holder.observed_block_hash }}>
                      {formatInteger(holder.observed_block_number, locale)}
                    </Link>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      <CursorPagination
        itemCount={holders?.length ?? 0}
        onReset={onReset}
        busy={busy}
        hasNext={hasNext}
        hasPrevious={hasPrevious}
        label={t("tokenHolder.title")}
        onNext={onNext}
        onPrevious={onPrevious}
        page={page}
      />
    </section>
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
        <p className="empty-result" role="status">
          <BrowsingEmpty>{t("state.noTransfers")}</BrowsingEmpty>
        </p>
      )}
      {events && events.length > 0 && (
        <div className="table-scroll" tabIndex={0} aria-label={t("detail.tokenEvents")}>
          <Table>
            <TableCaption className="sr-only">{t("detail.tokenEventHistory")}</TableCaption>
            <TableHeader>
              <TableRow>
                <TableHead className="numeric">{t("table.block")}</TableHead>
                <TableHead>{t("table.hash")}</TableHead>
                <TableHead>{t("detail.event")}</TableHead>
                <TableHead>{t("table.from")}</TableHead>
                <TableHead>{t("table.to")}</TableHead>
                <TableHead>{t("detail.operator")}</TableHead>
                <TableHead>{t("detail.tokenID")}</TableHead>
                <TableHead className="numeric">{t("detail.amount")}</TableHead>
                <TableHead>{t("table.standard")}</TableHead>
                <TableHead>{t("table.confidence")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {events.map((event) => (
                <TableRow key={`${event.block_hash}:${event.log_index}:${event.sub_index}`}>
                  <TableCell className="numeric">
                    <span className="table-primary">
                      <Link to="/blocks/$blockID" params={{ blockID: event.block_hash }}>
                        {formatInteger(event.block_number, locale)}
                      </Link>
                      <code title={event.block_hash}>{shorten(event.block_hash)}</code>
                    </span>
                  </TableCell>
                  <TableCell>
                    <Link
                      to="/tx/$hash"
                      params={{ hash: event.transaction_hash }}
                      search={{ tab: "overview" }}
                    >
                      {shorten(event.transaction_hash)}
                    </Link>
                  </TableCell>
                  <TableCell>{tokenEventKindLabel(event.kind, t)}</TableCell>
                  <TableCell>
                    {event.from ? <AddressIdentity address={event.from} /> : "—"}
                  </TableCell>
                  <TableCell>{event.to ? <AddressIdentity address={event.to} /> : "—"}</TableCell>
                  <TableCell>
                    {event.operator ? <AddressIdentity address={event.operator} /> : "—"}
                  </TableCell>
                  <TableCell>
                    {event.token_id && isNFTStandard(event.standard) ? (
                      <Link
                        to="/nft/$address/$tokenID"
                        params={{ address: event.token_address, tokenID: event.token_id }}
                      >
                        <code>{event.token_id}</code>
                      </Link>
                    ) : (
                      <code>{event.token_id ?? "—"}</code>
                    )}
                  </TableCell>
                  <TableCell className="numeric">
                    <code>{formatTokenEventAmount(event, locale)}</code>
                  </TableCell>
                  <TableCell>{tokenStandardLabel(event.standard, t)}</TableCell>
                  <TableCell>{confidenceLabel(event.confidence, t)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      {events && (
        <CursorPagination
          itemCount={events?.length ?? 0}
          onReset={onReset}
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
