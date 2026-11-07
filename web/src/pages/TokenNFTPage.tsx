import { AlertDialog } from "@/components/ui/alert-dialog";
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
import { Alert } from "@/components/ui/alert";
import { Card } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import "@/i18n/resources/tokenPage";
import { BrowsingScope, BrowsingEmpty } from "@/components/BrowsingScope";
import { CircleAlert } from "lucide-react";
import { UIIcon } from "@/components/UIIcon";
import { useBrowsingCursorHistory, useCursorHistory } from "@/api/pagination";
import { useEffect, useRef, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import {
  useNFTMetadata,
  useNFTOwnership,
  useToken,
  useTokenHolders,
  useTokenTransfers,
} from "@/api/hooks";
import type { NFTMetadata, TokenEvent, TokenHolder } from "@/api/types";
import { ApiError } from "@/api/client";
import { formatInteger, formatTokenAmount, shorten } from "@/components/format";
import { CopyableField, CopyButton } from "@/components/CopyButton";
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
  type Translate,
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
  const holders = useTokenHolders(address, holderPager.request, token.data?.standard === "erc20");
  const locale = i18n.resolvedLanguage ?? "en";
  const holderData = holders.error ? undefined : holders.data;
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
      <QueryNotice loading={token.isPending} error={token.error} />
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
                  <span>{formatInteger(holderMeta?.holder_count, locale)}</span>
                  {activeTab !== "holders" && (
                    <QueryNotice compact loading={holders.isPending} error={holders.error} />
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
          holders={holderData?.items}
          loading={holders.isPending}
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
            loading={transfers.isPending}
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

export function NFTDetailPage({ address, tokenID }: { address: string; tokenID: string }) {
  const { i18n, t } = useTranslation();
  const token = useToken(address);
  const nftMetadata = useNFTMetadata(address, tokenID);
  const isERC721 = token.data?.standard === "erc721";
  const ownership = useNFTOwnership(address, tokenID, isERC721);
  const locale = i18n.resolvedLanguage ?? "en";

  return (
    <Page
      title={nftMetadata.data?.name || t("page.nft")}
      description={`${address} / ${tokenID}`}
      mono
    >
      <QueryNotice loading={token.isPending} error={token.error} />
      {token.data && isNFTStandard(token.data.standard) ? (
        <DetailList label={t("nftMetadata.instance")}>
          <Detail label={t("tokenPage.collectionName")} value={token.data.name} />
          <Detail label={t("tokenPage.collectionSymbol")} value={token.data.symbol} />
          <Detail
            label={t("page.token")}
            mono
            value={
              <Link to="/token/$address" params={{ address: token.data.address }}>
                {token.data.address}
              </Link>
            }
          />
          <Detail label={t("detail.tokenID")} value={tokenID} />
          <Detail label={t("table.standard")} value={tokenStandardLabel(token.data.standard, t)} />
        </DetailList>
      ) : null}
      {token.data && !isNFTStandard(token.data.standard) ? (
        <Alert asChild>
          <div className="query-notice degraded" role="status">
            <UIIcon icon={CircleAlert} size={14} className="status-icon warning" />
            <span>
              <strong>{t("nftMetadata.notNFT")}</strong>
            </span>
          </div>
        </Alert>
      ) : null}
      {isERC721 ? <QueryNotice loading={ownership.isPending} error={ownership.error} /> : null}
      {isERC721 && ownership.data ? (
        <DetailList label={t("detail.nftOwnership")}>
          <Detail
            label={t("detail.owner")}
            value={<AddressIdentity address={ownership.data.owner} compact={false} />}
          />
          <Detail label={t("detail.balance")} value={ownership.data.balance} />
          <Detail
            label={t("table.confidence")}
            value={confidenceLabel(ownership.data.confidence, t)}
          />
          <Detail
            label={t("detail.snapshotBlock")}
            value={formatInteger(ownership.data.snapshot.block_number, locale)}
          />
          <Detail
            label={t("detail.snapshotHash")}
            mono
            value={
              <Link to="/blocks/$blockID" params={{ blockID: ownership.data.snapshot.block_hash }}>
                {ownership.data.snapshot.block_hash}
              </Link>
            }
          />
        </DetailList>
      ) : null}
      {token.data?.standard === "erc1155" ? (
        <Card asChild>
          <section className="panel detail-card" aria-label={t("detail.nftOwnership")}>
            <h2>{t("detail.nftOwnership")}</h2>
            <Alert asChild>
              <p className="context-note" role="note">
                {t("nftMetadata.erc1155Ownership")}
              </p>
            </Alert>
          </section>
        </Card>
      ) : null}
      <NFTMetadataPanel
        data={nftMetadata.data}
        error={nftMetadata.error}
        loading={nftMetadata.isPending}
        locale={locale}
      />
    </Page>
  );
}

function NFTMetadataPanel({
  data,
  error,
  loading,
  locale,
}: {
  data?: NFTMetadata;
  error: unknown;
  loading: boolean;
  locale: string;
}) {
  const { t } = useTranslation();
  const contentAvailable = Boolean(data?.content_observation);
  return (
    <Card asChild>
      <section className="panel detail-card nft-metadata-card" aria-labelledby="nft-metadata-title">
        <h2 id="nft-metadata-title">{t("nftMetadata.title")}</h2>
        <NFTMetadataQueryNotice error={error} loading={loading} />
        {data ? (
          <>
            {data.content_stale && data.content_observation ? (
              <Alert asChild>
                <div className="query-notice degraded compact" role="status">
                  <UIIcon icon={CircleAlert} size={14} className="status-icon warning" />
                  <span>
                    <strong>{t("nftMetadata.staleTitle")}</strong>
                    <small>
                      {t("nftMetadata.staleDetail", {
                        state: nftMetadataStateLabel(data.state, t),
                        latest: formatInteger(data.observation.block_number, locale),
                        content: formatInteger(data.content_observation.block_number, locale),
                      })}
                    </small>
                  </span>
                </div>
              </Alert>
            ) : data.state !== "available" ? (
              <Alert asChild>
                <div className="query-notice degraded compact" role="status">
                  <UIIcon icon={CircleAlert} size={14} className="status-icon warning" />
                  <span>
                    <strong>{nftMetadataStateLabel(data.state, t)}</strong>
                    <small>{t("nftMetadata.stateDetail")}</small>
                  </span>
                </div>
              </Alert>
            ) : null}
            <dl className="detail-grid">
              <Detail label={t("table.status")} value={nftMetadataStateLabel(data.state, t)} />
              <Detail
                label={t("detail.observedBlock")}
                value={formatInteger(data.observation.block_number, locale)}
              />
              <Detail
                label={t("detail.observedBlockHash")}
                mono
                value={
                  <Link to="/blocks/$blockID" params={{ blockID: data.observation.block_hash }}>
                    {data.observation.block_hash}
                  </Link>
                }
              />
              {data.content_stale && data.content_observation ? (
                <>
                  <Detail
                    label={t("nftMetadata.contentBlock")}
                    value={formatInteger(data.content_observation.block_number, locale)}
                  />
                  <Detail
                    label={t("nftMetadata.contentBlockHash")}
                    mono
                    value={
                      <Link
                        to="/blocks/$blockID"
                        params={{ blockID: data.content_observation.block_hash }}
                      >
                        {data.content_observation.block_hash}
                      </Link>
                    }
                  />
                </>
              ) : null}
              {contentAvailable ? (
                <>
                  <Detail
                    label={t("detail.name")}
                    value={<MetadataText value={data.name} truncated={data.name_truncated} />}
                  />
                  <Detail
                    label={t("nftMetadata.description")}
                    wide
                    value={
                      <MetadataText
                        description
                        value={data.description}
                        truncated={data.description_truncated}
                      />
                    }
                  />
                  <Detail
                    label={t("nftMetadata.imageLink")}
                    wide
                    value={
                      data.image.state === "available" && data.image.url ? (
                        <NFTExternalImageLink url={data.image.url} />
                      ) : (
                        nftMetadataImageStateLabel(data.image.state, t)
                      )
                    }
                  />
                </>
              ) : null}
            </dl>
            {contentAvailable ? (
              <div className="nft-traits">
                <h3>{t("nftMetadata.traits")}</h3>
                {data.attributes.length > 0 ? (
                  <dl className="nft-traits-grid">
                    {data.attributes.map((attribute, index) => (
                      <div className="nft-trait" key={`${attribute.trait_type}:${index}`}>
                        <dt>{attribute.trait_type}</dt>
                        <dd>
                          {attribute.value}
                          {attribute.display_type ? <small>{attribute.display_type}</small> : null}
                        </dd>
                      </div>
                    ))}
                  </dl>
                ) : (
                  <p className="empty-result">{t("nftMetadata.noTraits")}</p>
                )}
                {data.omitted_attribute_count > 0 ? (
                  <Alert asChild>
                    <p className="context-note" role="note">
                      {t("nftMetadata.omittedTraits", { count: data.omitted_attribute_count })}
                    </p>
                  </Alert>
                ) : null}
              </div>
            ) : null}
          </>
        ) : null}
      </section>
    </Card>
  );
}

function MetadataText({
  description = false,
  truncated,
  value,
}: {
  description?: boolean;
  truncated: boolean;
  value?: string;
}) {
  const { t } = useTranslation();
  if (!value) return <>—</>;
  return (
    <span className={description ? "nft-metadata-description" : undefined}>
      {value}
      {truncated ? (
        <small className="nft-metadata-truncated">{t("nftMetadata.textTruncated")}</small>
      ) : null}
    </span>
  );
}

function NFTMetadataQueryNotice({ error, loading }: { error: unknown; loading: boolean }) {
  const { t } = useTranslation();
  if (loading) return <QueryNotice compact loading />;
  if (!(error instanceof ApiError)) return <QueryNotice compact error={error} />;
  const message = (() => {
    switch (error.code.toLowerCase()) {
      case "nft_metadata_disabled":
        return t("nftMetadata.disabled");
      case "nft_metadata_not_found":
        return t("nftMetadata.notFound");
      case "nft_metadata_noncanonical":
        return t("nftMetadata.noncanonical");
      default:
        return undefined;
    }
  })();
  if (!message) return <QueryNotice compact error={error} />;
  return (
    <Alert asChild>
      <div className="query-notice degraded compact" role="status">
        <UIIcon icon={CircleAlert} size={14} className="status-icon warning" />
        <span>
          <strong>{message}</strong>
        </span>
      </div>
    </Alert>
  );
}

function NFTExternalImageLink({ url }: { url: string }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const openButtonRef = useRef<HTMLButtonElement>(null);
  const label = externalTargetLabel(url);
  useEffect(() => {
    setOpen(false);
  }, [url]);

  return (
    <>
      <span className="nft-external-link">
        <Button
          variant="ghost"
          aria-haspopup="dialog"
          aria-label={t("nftMetadata.reviewImageLink", { target: url })}
          className="nft-image-link"
          onClick={() => setOpen(true)}
          ref={openButtonRef}
          title={url}
          type="button"
        >
          {label}
        </Button>
        <CopyButton value={url} />
        <Badge variant="status" className="result-kind warning">
          {t("nftMetadata.unverifiedLink")}
        </Badge>
      </span>
      {open && (
        <AlertDialog
          title={t("nftMetadata.dialogTitle")}
          description={t("nftMetadata.externalWarning")}
          cancelLabel={t("nftMetadata.cancel")}
          confirmLabel={t("nftMetadata.openExternal")}
          returnFocus={openButtonRef}
          onClose={() => setOpen(false)}
          action={
            <a
              href={url}
              referrerPolicy="no-referrer"
              rel="external noopener noreferrer"
              target="_blank"
            >
              {t("nftMetadata.openExternal")}
            </a>
          }
        >
          <div className="external-link-target">
            <strong>{t("nftMetadata.target")}</strong>
            <span>{label}</span>
            <CopyButton value={url} />
          </div>
        </AlertDialog>
      )}
    </>
  );
}

function externalTargetLabel(value: string): string {
  try {
    const parsed = new URL(value);
    const target = `${parsed.host}${parsed.pathname === "/" ? "" : parsed.pathname}`;
    if ([...target].length <= 64) return target;
    const characters = [...target];
    return `${characters.slice(0, 42).join("")}…${characters.slice(-18).join("")}`;
  } catch {
    return value;
  }
}

function nftMetadataStateLabel(value: NFTMetadata["state"], t: Translate): string {
  switch (value) {
    case "available":
      return t("nftMetadata.state.available");
    case "pending":
      return t("nftMetadata.state.pending");
    case "unavailable":
      return t("nftMetadata.state.unavailable");
    case "unsafe":
      return t("nftMetadata.state.unsafe");
    case "error":
      return t("nftMetadata.state.error");
  }
}

function nftMetadataImageStateLabel(value: NFTMetadata["image"]["state"], t: Translate): string {
  switch (value) {
    case "available":
      return t("nftMetadata.imageState.available");
    case "unavailable":
      return t("nftMetadata.imageState.unavailable");
    case "missing":
      return t("nftMetadata.imageState.missing");
    case "unsafe":
      return t("nftMetadata.imageState.unsafe");
    case "unsupported":
      return t("nftMetadata.imageState.unsupported");
    case "gateway_unavailable":
      return t("nftMetadata.imageState.gatewayUnavailable");
  }
}
