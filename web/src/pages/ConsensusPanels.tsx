import { useTranslation } from "react-i18next";
import { useBlockConsensus } from "@/api/consensus";
import type * as schemas from "@/api/heyapi/types.gen";
import { QueryNotice } from "@/components/QueryNotice";
import { formatInteger } from "@/components/format";
import { Detail } from "./pages";

const consensusDetailFields = [
  "root",
  "parent_root",
  "state_root",
  "body_root",
  "graffiti",
] as const;

export function ConsensusPanel({ hash }: { hash: string }) {
  const query = useBlockConsensus(hash);
  const { i18n, t } = useTranslation();
  const data = query.data;
  const locale = i18n.resolvedLanguage ?? "en";
  const summaryFields = ["slot", "epoch", "proposer_index", "fork"] as const;
  const hasSummary =
    data && (data.state === "complete" || summaryFields.some((key) => data[key] !== undefined));
  const hasDetails =
    data &&
    (consensusDetailFields.some((key) => data[key] !== undefined) ||
      Object.keys(data.operations ?? {}).length > 0);
  return (
    <section
      className="panel transaction-tab-panel block-consensus-panel"
      role="tabpanel"
      aria-label={t("consensus.title")}
    >
      <QueryNotice loading={query.isPending} error={query.error} compact />
      {data && (
        <>
          {data.state !== "complete" && (
            <div className="block-consensus-notice" role="status">
              <strong>{t(`consensus.states.${data.state}`)}</strong>
              {data.code && <p>{t(`consensus.codes.${data.code}`, { defaultValue: data.code })}</p>}
            </div>
          )}
          {data.slot_conflict && (
            <p className="block-consensus-notice" role="status">
              {t("consensus.slotConflict")}
            </p>
          )}
          {hasSummary && (
            <dl className="block-consensus-summary">
              {summaryFields.map((key) => (
                <div key={key}>
                  <dt>{t(`consensus.${key}`)}</dt>
                  <dd>{key === "fork" ? (data[key] ?? "—") : formatInteger(data[key], locale)}</dd>
                </div>
              ))}
            </dl>
          )}
          {hasDetails && (
            <details className="block-consensus-details">
              <summary>{t("consensus.technicalDetails")}</summary>
              <dl className="block-consensus-detail-list">
                {consensusDetailFields.map((key) =>
                  data[key] === undefined ? null : (
                    <Detail
                      key={key}
                      label={t(`consensus.${key}`)}
                      value={<code className="mono-wrap">{data[key]}</code>}
                    />
                  ),
                )}
                {data.graffiti && (
                  <Detail
                    label={t("consensus.graffitiText")}
                    value={graffitiText(data.graffiti) || "—"}
                  />
                )}
                {Object.entries(data.operations ?? {}).map(([key, value]) => (
                  <Detail
                    key={key}
                    label={t(`consensus.operations.${key}`)}
                    value={formatInteger(value, locale)}
                  />
                ))}
              </dl>
            </details>
          )}
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
  safeBlock,
  finalizedBlock,
  now,
}: {
  data?: schemas.ConsensusOverview;
  safeBlock?: string;
  finalizedBlock?: string;
  now: number;
}) {
  const { i18n, t } = useTranslation();
  const consensus = data?.state === "disabled" ? undefined : data;
  const locale = i18n.resolvedLanguage ?? "en";
  const notices = consensus
    ? [
        consensus.state !== "complete" && t(`consensus.overview.states.${consensus.state}`),
        consensus.syncing && t("consensus.overview.syncing"),
        consensus.execution_optimistic && t("consensus.overview.optimistic"),
        (consensus.stale || (consensus.stale_at && now > Date.parse(consensus.stale_at))) &&
          t("consensus.overview.stale"),
      ].filter(Boolean)
    : [];
  return (
    <section className="panel consensus-overview" aria-labelledby="consensus-overview-title">
      <header className="consensus-overview-heading">
        <h2 id="consensus-overview-title">
          {t(consensus ? "consensus.overview.title" : "consensus.overview.finalityTitle")}
        </h2>
        {notices.length > 0 && <p role="status">{notices.join(" · ")}</p>}
      </header>
      <dl className={`consensus-overview-metrics${consensus ? " has-consensus" : ""}`}>
        <div>
          <dt>{t("context.safeBlock")}</dt>
          <dd>{formatInteger(safeBlock, locale)}</dd>
        </div>
        <div>
          <dt>{t("home.finality")}</dt>
          <dd>{formatInteger(finalizedBlock, locale)}</dd>
        </div>
        {consensus && (
          <>
            <div>
              <dt>{t("consensus.overview.slot")}</dt>
              <dd>{formatInteger(consensus.slot, locale)}</dd>
            </div>
            <div>
              <dt>{t("consensus.overview.epoch")}</dt>
              <dd>{formatInteger(consensus.epoch, locale)}</dd>
            </div>
            <div>
              <dt>{t("consensus.overview.finalizedEpoch")}</dt>
              <dd>{formatInteger(consensus.finalized?.epoch, locale)}</dd>
            </div>
          </>
        )}
      </dl>
    </section>
  );
}
