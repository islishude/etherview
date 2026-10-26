import { ArrowRight } from "lucide-react";
import { UIIcon } from "@/components/UIIcon";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { useBlockBlobs } from "@/api/consensus";
import type * as schemas from "@/api/heyapi/types.gen";
import { QueryNotice } from "@/components/QueryNotice";
import { formatInteger, shorten } from "@/components/format";

export function BlobArchiveNotice({ data }: { data?: schemas.BlockBlobs }) {
  const { t } = useTranslation();
  if (!data || data.state === "complete") return null;
  return (
    <div className="blob-notice" role="status">
      <strong>{t(`consensus.states.${data.state}`)}</strong>
      {data.code && <p>{t(`consensus.codes.${data.code}`, { defaultValue: data.code })}</p>}
    </div>
  );
}

export function BlobHashList({ hashes }: { hashes: string[] }) {
  const { t } = useTranslation();
  if (!hashes.length) return <p className="quiet">{t("state.noBlobHashes")}</p>;
  return (
    <dl className="blob-hash-list">
      {hashes.map((hash, index) => (
        <div key={`${index}:${hash}`}>
          <dt>{t("detail.blobIndex", { index })}</dt>
          <dd>
            <code className="mono-wrap">{hash}</code>
          </dd>
        </div>
      ))}
    </dl>
  );
}

export function BlobsPanel({
  hash,
  transaction,
  hashes,
}: {
  hash: string;
  transaction?: string;
  hashes?: string[];
}) {
  const query = useBlockBlobs(hash);
  const { i18n, t } = useTranslation();
  const locale = i18n.resolvedLanguage ?? "en";
  const items = query.data?.items.filter(
    (item) => !transaction || item.transaction_hash.toLowerCase() === transaction.toLowerCase(),
  );
  return (
    <section
      className={transaction ? "blob-archive" : "panel transaction-tab-panel blob-archive"}
      role={transaction ? undefined : "tabpanel"}
      aria-label={t("consensus.blobs")}
    >
      <QueryNotice loading={query.isPending} error={query.error} compact />
      <BlobArchiveNotice data={query.data} />
      <div className="blob-list-heading">
        {!transaction && items && (
          <span>
            {t("consensus.blobCount", {
              count: items.length,
              total: formatInteger(items.length, locale),
            })}
          </span>
        )}
        {transaction && (
          <Link
            className="blob-block-link"
            to="/blocks/$blockID"
            params={{ blockID: hash }}
            search={{ tab: "blobs" }}
          >
            {t("consensus.blockBlobs")} <UIIcon icon={ArrowRight} />
          </Link>
        )}
      </div>
      {query.data?.state === "complete" && items?.length === 0 && !hashes?.length && (
        <p className="quiet">{t("consensus.noBlobs")}</p>
      )}
      {Boolean(items?.length) && (
        <ul className="blob-list">
          {items?.map((item) => (
            <li className="blob-list-row" key={`${hash}:${item.index}`}>
              <Link
                className="blob-detail-link"
                to="/blocks/$blockHash/blobs/$index"
                params={{ blockHash: hash, index: item.index }}
                aria-label={t("consensus.viewBlob", { index: item.index })}
              >
                <strong>
                  {t("consensus.blobIndex", { index: formatInteger(item.index, locale) })}
                </strong>
                <code title={item.versioned_hash}>{shorten(item.versioned_hash, 12, 8)}</code>
                <span className="blob-list-size">
                  {item.size !== undefined &&
                    t("consensus.sizeBytes", { size: formatInteger(item.size, locale) })}
                </span>
                <UIIcon icon={ArrowRight} />
              </Link>
              {!transaction && (
                <Link
                  className="blob-transaction-link"
                  to="/tx/$hash"
                  params={{ hash: item.transaction_hash }}
                  search={{ tab: "blob" }}
                  title={item.transaction_hash}
                >
                  <span>{t("consensus.transaction")}</span>
                  <code>{shorten(item.transaction_hash)}</code>
                </Link>
              )}
            </li>
          ))}
        </ul>
      )}
      {!items?.length && hashes && <BlobHashList hashes={hashes} />}
    </section>
  );
}
