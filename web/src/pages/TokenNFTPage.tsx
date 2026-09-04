import {
  useEffect,
  useRef,
  useState,
} from "react";
import { Link, } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import {
  useNFTMetadata,
  useNFTOwnership,
  useToken,
  useTokenHolders,
  useTokenTransfers,
} from "@/api/hooks";
import type {
  NFTMetadata,
  TokenEvent,
  TokenHolder,
  TokenHolderMeta,
} from "@/api/types";
import { ApiError } from "@/api/client";
import {
  formatInteger,
  formatTokenAmount,
  shorten,
} from "@/components/format";
import { CopyButton, } from "@/components/CopyButton";
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
  useCursorHistory,
} from "./pages";

export function TokenDetailPage({ address }: { address: string }) {
  const { i18n, t } = useTranslation();
  const transferPager = useCursorHistory(`token-transfers:${address}`);
  const holderPager = useCursorHistory(`token-holders:${address}`);
  const token = useToken(address);
  const transfers = useTokenTransfers(
    address,
    CORE_PAGE_SIZE,
    transferPager.cursor,
    transferPager.refreshGeneration,
  );
  const holders = useTokenHolders(
    address,
    50,
    holderPager.cursor,
    holderPager.refreshGeneration,
    token.data?.standard === "erc20",
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
      {token.data?.standard === "erc20" && (
        <TokenHolders
          busy={holders.isFetching}
          decimals={token.data.decimals}
          error={holders.error}
          hasNext={Boolean(holders.data?.next_cursor)}
          hasPrevious={holderPager.hasPrevious}
          holders={holders.data?.items}
          loading={holders.isPending}
          locale={locale}
          meta={holders.data?.meta}
          onNext={() => holderPager.next(holders.data?.next_cursor)}
          onPrevious={holderPager.previous}
          onReset={holderPager.reset}
          page={holderPager.page}
        />
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
        <DetailList label={t("tokenHolder.summary")}>
          <Detail label={t("tokenHolder.count")} value={formatInteger(meta.holder_count, locale)} />
          <Detail label={t("table.supply")} value={formatTokenAmount(meta.total_supply, decimals, locale)} />
          <Detail label={t("tokenHolder.snapshot")} value={formatInteger(meta.snapshot_block_number, locale)} />
          <Detail label={t("tokenHolder.snapshotHash")} mono value={meta.snapshot_block_hash} />
        </DetailList>
      )}
      {holders && holders.length === 0 && (
        <p className="empty-result" role="status">{t("tokenHolder.empty")}</p>
      )}
      {holders && holders.length > 0 && (
        <div className="table-scroll" tabIndex={0} aria-label={t("tokenHolder.title")}>
          <table>
            <caption className="sr-only">{t("tokenHolder.caption")}</caption>
            <thead>
              <tr>
                <th>{t("tokenHolder.address")}</th>
                <th>{t("detail.amount")}</th>
                <th>{t("detail.observedBlock")}</th>
              </tr>
            </thead>
            <tbody>
              {holders.map((holder) => (
                <tr key={holder.holder_address}>
                  <td><AddressIdentity address={holder.holder_address} /></td>
                  <td>{formatTokenAmount(holder.balance, decimals, locale)}</td>
                  <td>
                    <Link to="/blocks/$blockID" params={{ blockID: holder.observed_block_hash }}>
                      {formatInteger(holder.observed_block_number, locale)}
                    </Link>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <CursorPagination
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
                  <td>{event.from ? <AddressIdentity address={event.from} /> : "—"}</td>
                  <td>{event.to ? <AddressIdentity address={event.to} /> : "—"}</td>
                  <td>{event.operator ? <AddressIdentity address={event.operator} /> : "—"}</td>
                  <td>
                    {event.token_id && isNFTStandard(event.standard) ? (
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

export function NFTDetailPage({ address, tokenID }: { address: string; tokenID: string }) {
  const { i18n, t } = useTranslation();
  const token = useToken(address);
  const nftMetadata = useNFTMetadata(address, tokenID);
  const isERC721 = token.data?.standard === "erc721";
  const ownership = useNFTOwnership(address, tokenID, isERC721);
  const locale = i18n.resolvedLanguage ?? "en";

  return (
    <Page title={nftMetadata.data?.name || t("page.nft")} description={`${address} / ${tokenID}`} mono>
      <QueryNotice loading={token.isPending} error={token.error} />
      {token.data && isNFTStandard(token.data.standard) ? (
        <DetailList label={t("nftMetadata.instance")}>
          <Detail
            label={t("page.token")}
            mono
            value={(
              <Link to="/token/$address" params={{ address: token.data.address }}>
                {token.data.address}
              </Link>
            )}
          />
          <Detail label={t("detail.tokenID")} value={tokenID} />
          <Detail label={t("table.standard")} value={tokenStandardLabel(token.data.standard, t)} />
        </DetailList>
      ) : null}
      {token.data && !isNFTStandard(token.data.standard) ? (
        <div className="query-notice degraded" role="status">
          <span className="status-dot warning" aria-hidden="true" />
          <span><strong>{t("nftMetadata.notNFT")}</strong></span>
        </div>
      ) : null}
      {isERC721 ? <QueryNotice loading={ownership.isPending} error={ownership.error} /> : null}
      {isERC721 && ownership.data ? (
        <DetailList label={t("detail.nftOwnership")}>
          <Detail
            label={t("detail.owner")}
            value={<AddressIdentity address={ownership.data.owner} compact={false} />}
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
      ) : null}
      {token.data?.standard === "erc1155" ? (
        <section className="panel detail-card" aria-label={t("detail.nftOwnership")}>
          <h2>{t("detail.nftOwnership")}</h2>
          <p className="context-note" role="note">{t("nftMetadata.erc1155Ownership")}</p>
        </section>
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
    <section className="panel detail-card nft-metadata-card" aria-labelledby="nft-metadata-title">
      <h2 id="nft-metadata-title">{t("nftMetadata.title")}</h2>
      <NFTMetadataQueryNotice error={error} loading={loading} />
      {data ? (
        <>
          {data.content_stale && data.content_observation ? (
            <div className="query-notice degraded compact" role="status">
              <span className="status-dot warning" aria-hidden="true" />
              <span>
                <strong>{t("nftMetadata.staleTitle")}</strong>
                <small>{t("nftMetadata.staleDetail", {
                  state: nftMetadataStateLabel(data.state, t),
                  latest: formatInteger(data.observation.block_number, locale),
                  content: formatInteger(data.content_observation.block_number, locale),
                })}</small>
              </span>
            </div>
          ) : data.state !== "available" ? (
            <div className="query-notice degraded compact" role="status">
              <span className="status-dot warning" aria-hidden="true" />
              <span>
                <strong>{nftMetadataStateLabel(data.state, t)}</strong>
                <small>{t("nftMetadata.stateDetail")}</small>
              </span>
            </div>
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
              value={(
                <Link to="/blocks/$blockID" params={{ blockID: data.observation.block_hash }}>
                  {data.observation.block_hash}
                </Link>
              )}
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
                  value={(
                    <Link to="/blocks/$blockID" params={{ blockID: data.content_observation.block_hash }}>
                      {data.content_observation.block_hash}
                    </Link>
                  )}
                />
              </>
            ) : null}
            {contentAvailable ? (
              <>
                <Detail
                  label={t("detail.name")}
                  value={(
                    <MetadataText value={data.name} truncated={data.name_truncated} />
                  )}
                />
                <Detail
                  label={t("nftMetadata.description")}
                  wide
                  value={(
                    <MetadataText description value={data.description} truncated={data.description_truncated} />
                  )}
                />
                <Detail
                  label={t("nftMetadata.imageLink")}
                  wide
                  value={data.image.state === "available" && data.image.url ? (
                    <NFTExternalImageLink url={data.image.url} />
                  ) : nftMetadataImageStateLabel(data.image.state, t)}
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
                <p className="context-note" role="note">
                  {t("nftMetadata.omittedTraits", { count: data.omitted_attribute_count })}
                </p>
              ) : null}
            </div>
          ) : null}
        </>
      ) : null}
    </section>
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
      {truncated ? <small className="nft-metadata-truncated">{t("nftMetadata.textTruncated")}</small> : null}
    </span>
  );
}

function NFTMetadataQueryNotice({ error, loading }: { error: unknown; loading: boolean }) {
  const { t } = useTranslation();
  if (loading) return <QueryNotice compact loading />;
  if (!(error instanceof ApiError)) return <QueryNotice compact error={error} />;
  const message = (() => {
    switch (error.code.toLowerCase()) {
      case "nft_metadata_disabled": return t("nftMetadata.disabled");
      case "nft_metadata_not_found": return t("nftMetadata.notFound");
      case "nft_metadata_noncanonical": return t("nftMetadata.noncanonical");
      default: return undefined;
    }
  })();
  if (!message) return <QueryNotice compact error={error} />;
  return (
    <div className="query-notice degraded compact" role="status">
      <span className="status-dot warning" aria-hidden="true" />
      <span><strong>{message}</strong></span>
    </div>
  );
}

function NFTExternalImageLink({ url }: { url: string }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const openButtonRef = useRef<HTMLButtonElement>(null);
  const dialogRef = useRef<HTMLDivElement>(null);
  const label = externalTargetLabel(url);

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
      <span className="nft-external-link">
        <button
          aria-haspopup="dialog"
          aria-label={t("nftMetadata.reviewImageLink", { target: url })}
          className="nft-image-link"
          onClick={() => setOpen(true)}
          ref={openButtonRef}
          title={url}
          type="button"
        >
          {label}
        </button>
        <CopyButton value={url} />
        <span className="result-kind warning">{t("nftMetadata.unverifiedLink")}</span>
      </span>
      {open ? (
        <div
          className="dialog-backdrop"
          onMouseDown={(event) => {
            if (event.currentTarget === event.target) setOpen(false);
          }}
          role="presentation"
        >
          <div
            aria-describedby="nft-external-link-warning"
            aria-labelledby="nft-external-link-title"
            aria-modal="true"
            className="external-link-dialog"
            onKeyDown={trapDialogFocus}
            ref={dialogRef}
            role="dialog"
            tabIndex={-1}
          >
            <div className="qr-dialog-heading">
              <h2 id="nft-external-link-title">{t("nftMetadata.dialogTitle")}</h2>
              <button
                aria-label={t("common.close")}
                className="dialog-close"
                onClick={() => setOpen(false)}
                type="button"
              >
                ×
              </button>
            </div>
            <p className="external-link-warning" id="nft-external-link-warning" role="alert">
              {t("nftMetadata.externalWarning")}
            </p>
            <div className="external-link-target">
              <strong>{t("nftMetadata.target")}</strong>
              <span>{label}</span>
              <CopyButton value={url} />
            </div>
            <div className="external-link-dialog-actions">
              <button className="button secondary" onClick={() => setOpen(false)} type="button">
                {t("nftMetadata.cancel")}
              </button>
              <a
                className="button"
                href={url}
                onClick={() => setOpen(false)}
                referrerPolicy="no-referrer"
                rel="external noopener noreferrer"
                target="_blank"
              >
                {t("nftMetadata.openExternal")}
              </a>
            </div>
          </div>
        </div>
      ) : null}
    </>
  );
}

function trapDialogFocus(event: React.KeyboardEvent<HTMLDivElement>) {
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
    case "available": return t("nftMetadata.state.available");
    case "pending": return t("nftMetadata.state.pending");
    case "unavailable": return t("nftMetadata.state.unavailable");
    case "unsafe": return t("nftMetadata.state.unsafe");
    case "error": return t("nftMetadata.state.error");
  }
}

function nftMetadataImageStateLabel(value: NFTMetadata["image"]["state"], t: Translate): string {
  switch (value) {
    case "available": return t("nftMetadata.imageState.available");
    case "unavailable": return t("nftMetadata.imageState.unavailable");
    case "missing": return t("nftMetadata.imageState.missing");
    case "unsafe": return t("nftMetadata.imageState.unsafe");
    case "unsupported": return t("nftMetadata.imageState.unsupported");
    case "gateway_unavailable": return t("nftMetadata.imageState.gatewayUnavailable");
  }
}
