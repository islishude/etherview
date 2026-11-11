import { AlertDialog } from "@/components/ui/alert-dialog";

import { Alert } from "@/components/ui/alert";
import { Card } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import "@/i18n/resources/tokenPage";

import { CircleAlert } from "lucide-react";
import { UIIcon } from "@/components/UIIcon";

import { useEffect, useRef, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { useNFTMetadata, useNFTOwnership, useToken } from "@/api/hooks";
import type { NFTMetadata } from "@/api/types";
import { ApiError } from "@/api/client";
import { formatInteger } from "@/components/format";
import { CopyButton } from "@/components/CopyButton";
import { AddressIdentity } from "@/ens/AddressIdentity";
import { QueryNotice } from "@/components/QueryNotice";
import {
  Detail,
  DetailList,
  Page,
  confidenceLabel,
  isNFTStandard,
  tokenStandardLabel,
  type Translate,
} from "./pages";

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
      <QueryNotice loading={token.isLoading} error={token.error} />
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
      {isERC721 ? <QueryNotice loading={ownership.isLoading} error={ownership.error} /> : null}
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
        loading={nftMetadata.isLoading}
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
