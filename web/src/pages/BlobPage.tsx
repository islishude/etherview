import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { useBlockBlobs } from "@/api/consensus";
import { QueryNotice } from "@/components/QueryNotice";
import { formatInteger } from "@/components/format";
import { BlobActions } from "./BlobActions";
import { BlobArchiveNotice } from "./BlobsPanel";
import { Detail, Page } from "./pages";

export function BlobPage({ hash, index }: { hash: string; index: string }) {
  const { i18n, t } = useTranslation();
  const query = useBlockBlobs(hash);
  const locale = i18n.resolvedLanguage ?? "en";
  const item = query.data?.items.find((blob) => blob.index === index);
  return (
    <Page title={t("consensus.blobDetails")} description={item?.versioned_hash} mono>
      <Link to="/blocks/$blockID" params={{ blockID: hash }} search={{ tab: "blobs" }}>
        <span aria-hidden="true">← </span>
        {t("consensus.blockBlobs")}
      </Link>
      <section className="panel blob-detail-panel" aria-label={t("consensus.blobDetails")}>
        <QueryNotice loading={query.isPending} error={query.error} compact />
        <BlobArchiveNotice data={query.data} />
        {query.data && !item && (
          <p className="quiet">
            {t(
              query.data.state === "complete"
                ? "consensus.blobNotFound"
                : "consensus.blobDetailsUnavailable",
            )}
          </p>
        )}
        {item && (
          <>
            <dl className="blob-metadata">
              <Detail
                label={t("table.block")}
                value={
                  <Link to="/blocks/$blockID" params={{ blockID: hash }} search={{ tab: "blobs" }}>
                    <code className="mono-wrap">{hash}</code>
                  </Link>
                }
              />
              <Detail
                label={t("consensus.transaction")}
                value={
                  <Link
                    to="/tx/$hash"
                    params={{ hash: item.transaction_hash }}
                    search={{ tab: "blob" }}
                  >
                    <code className="mono-wrap">{item.transaction_hash}</code>
                  </Link>
                }
              />
              {item.size !== undefined && (
                <Detail label={t("consensus.size")} value={formatInteger(item.size, locale)} />
              )}
              {item.commitment && (
                <Detail
                  label={t("consensus.commitment")}
                  value={<code className="mono-wrap">{item.commitment}</code>}
                />
              )}
              {item.sha256 && (
                <Detail label="SHA-256" value={<code className="mono-wrap">{item.sha256}</code>} />
              )}
            </dl>
            {query.data?.state === "complete" && (
              <BlobActions key={`${hash}:${index}`} hash={hash} index={index} />
            )}
          </>
        )}
      </section>
    </Page>
  );
}
