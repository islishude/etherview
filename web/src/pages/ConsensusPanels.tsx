import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { readBlob, useBlockBlobs, useBlockConsensus } from "@/api/consensus";
import type { components } from "@/api/schema.gen";
import { QueryNotice } from "@/components/QueryNotice";
import { Detail, DetailList } from "./pages";

export function ConsensusPanel({ hash }: { hash: string }) {
  const query = useBlockConsensus(hash);
  const { t } = useTranslation();
  const data = query.data;
  return (
    <section
      className="panel transaction-tab-panel"
      role="tabpanel"
      aria-label={t("consensus.title")}
    >
      <h2>{t("consensus.title")}</h2>
      <QueryNotice loading={query.isPending} error={query.error} />
      {data && (
        <>
          <p>{t(`consensus.states.${data.state}`)}</p>
          {data.code && <p>{t(`consensus.codes.${data.code}`, { defaultValue: data.code })}</p>}
          {data.slot_conflict && <p role="status">{t("consensus.slotConflict")}</p>}
          <DetailList label={t("consensus.title")}>
            {(
              [
                "slot",
                "epoch",
                "proposer_index",
                "root",
                "parent_root",
                "state_root",
                "body_root",
                "fork",
                "graffiti",
              ] as const
            ).map((key) =>
              data[key] === undefined ? null : (
                <Detail
                  key={key}
                  label={t(`consensus.${key}`)}
                  value={<code className="mono-wrap">{data[key]}</code>}
                />
              ),
            )}
            {data.graffiti && (
              <Detail label={t("consensus.graffitiText")} value={graffitiText(data.graffiti)} />
            )}
            {Object.entries(data.operations ?? {}).map(([key, value]) => (
              <Detail key={key} label={t(`consensus.operations.${key}`)} value={value} />
            ))}
          </DetailList>
        </>
      )}
    </section>
  );
}
function graffitiText(hex: string) {
  const bytes = Uint8Array.from(hex.slice(2).match(/.{2}/g) ?? [], (value) =>
    Number.parseInt(value, 16),
  );
  return new TextDecoder()
    .decode(bytes)
    .replace(/[\u0000-\u001f\u007f]/g, " ")
    .trim();
}
export function ConsensusOverview({
  data,
  now,
}: {
  data?: components["schemas"]["ConsensusOverview"];
  now: number;
}) {
  const { t } = useTranslation();
  if (!data) return null;
  return (
    <section className="panel">
      <h2>{t("consensus.title")}</h2>
      <p>
        {t(`consensus.states.${data.state}`)}
        {data.stale || (data.stale_at && now > Date.parse(data.stale_at))
          ? ` · ${t("consensus.stale")}`
          : ""}
      </p>
      <DetailList label={t("consensus.title")}>
        <Detail label={t("consensus.slot")} value={data.slot ?? "—"} />
        <Detail label={t("consensus.epoch")} value={data.epoch ?? "—"} />
        <Detail
          label={t("consensus.root")}
          value={<code className="mono-wrap">{data.root ?? "—"}</code>}
        />
        <Detail
          label={t("consensus.justified")}
          value={data.justified ? `${data.justified.epoch} · ${data.justified.root}` : "—"}
        />
        <Detail
          label={t("consensus.finalized")}
          value={data.finalized ? `${data.finalized.epoch} · ${data.finalized.root}` : "—"}
        />
        <Detail
          label={t("consensus.syncing")}
          value={
            data.syncing === undefined ? "—" : t(data.syncing ? "consensus.yes" : "consensus.no")
          }
        />
        <Detail
          label={t("consensus.optimistic")}
          value={
            data.execution_optimistic === undefined
              ? "—"
              : t(data.execution_optimistic ? "consensus.yes" : "consensus.no")
          }
        />
        <Detail label={t("consensus.updated")} value={data.observed_at ?? "—"} />
        <Detail label={t("consensus.blobs")} value={t(`consensus.states.${data.blob_state}`)} />
        {data.code && (
          <Detail
            label={t("consensus.collectionStatus")}
            value={t(`consensus.codes.${data.code}`, { defaultValue: data.code })}
          />
        )}
        {data.blob_code && (
          <Detail
            label={t("consensus.archiveStatus")}
            value={t(`consensus.codes.${data.blob_code}`, { defaultValue: data.blob_code })}
          />
        )}
      </DetailList>
    </section>
  );
}
export function BlobsPanel({ hash, transaction }: { hash: string; transaction?: string }) {
  const query = useBlockBlobs(hash);
  const { t } = useTranslation();
  const items = query.data?.items.filter(
    (item) => !transaction || item.transaction_hash.toLowerCase() === transaction.toLowerCase(),
  );
  return (
    <section
      className="panel transaction-tab-panel"
      role={transaction ? undefined : "tabpanel"}
      aria-label={t("consensus.blobs")}
    >
      <h2>{t("consensus.blobs")}</h2>
      <QueryNotice loading={query.isPending} error={query.error} />
      {query.data && <p>{t(`consensus.states.${query.data.state}`)}</p>}
      {query.data?.code && (
        <p>{t(`consensus.codes.${query.data.code}`, { defaultValue: query.data.code })}</p>
      )}
      {transaction && (
        <Link to="/blocks/$blockID" params={{ blockID: hash }} search={{ tab: "blobs" }}>
          {t("consensus.blockBlobs")}
        </Link>
      )}
      {items?.map((item) => (
        <article className="transaction-log" key={item.index}>
          <h3>{t("consensus.blobIndex", { index: item.index })}</h3>
          <DetailList label={t("consensus.blobs")}>
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
            <Detail
              label={t("consensus.versionedHash")}
              value={<code className="mono-wrap">{item.versioned_hash}</code>}
            />
            {item.commitment && (
              <Detail
                label={t("consensus.commitment")}
                value={<code className="mono-wrap">{item.commitment}</code>}
              />
            )}
            {item.sha256 && (
              <Detail label="SHA-256" value={<code className="mono-wrap">{item.sha256}</code>} />
            )}
            {item.size && <Detail label={t("consensus.size")} value={item.size} />}
          </DetailList>
          {query.data?.state === "complete" && <BlobActions hash={hash} index={item.index} />}
        </article>
      ))}
    </section>
  );
}
function BlobActions({ hash, index }: { hash: string; index: string }) {
  const { t } = useTranslation();
  const [preview, setPreview] = useState("");
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
  async function load(download: boolean) {
    setBusy(true);
    setFailed(false);
    try {
      const data = await readBlob(hash, index);
      if (download) {
        const url = URL.createObjectURL(new Blob([data], { type: "application/octet-stream" }));
        const link = document.createElement("a");
        link.href = url;
        link.download = `blob-${index}.bin`;
        link.click();
        setTimeout(() => URL.revokeObjectURL(url), 1000);
      } else
        setPreview(
          Array.from(new Uint8Array(data, 0, 1024), (byte) =>
            byte.toString(16).padStart(2, "0"),
          ).join(""),
        );
    } catch {
      setFailed(true);
    } finally {
      setBusy(false);
    }
  }
  return (
    <div>
      <button type="button" disabled={busy} onClick={() => void load(false)}>
        {t("consensus.preview")}
      </button>{" "}
      <button type="button" disabled={busy} onClick={() => void load(true)}>
        {t("consensus.download")}
      </button>
      {failed && <p role="alert">{t("consensus.storageUnavailable")}</p>}
      {preview && (
        <pre className="mono-wrap max-h-72 max-w-full overflow-auto whitespace-pre-wrap break-all">
          {preview}
        </pre>
      )}
    </div>
  );
}
