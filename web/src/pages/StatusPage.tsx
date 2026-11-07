import { useTranslation } from "react-i18next";
import { AdminSurface, AdminPanel, useAdminIdentity } from "@/components/AdminSurface";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { useChainStatus } from "@/api/chainStatus";
import { usePublicConfig } from "@/api/publicConfig";
import { QueryNotice } from "@/components/QueryNotice";
import { formatInteger } from "@/components/format";
import NameCoverageNotice from "@/ens/NameCoverageNotice";
import { stageLabel, stageStateLabel, featureLabel } from "./pages";

export function StatusPage() {
  return (
    <AdminSurface>
      <StatusContent />
    </AdminSurface>
  );
}

function StatusContent() {
  const { i18n, t } = useTranslation();
  const identity = useAdminIdentity();
  const status = useChainStatus(Boolean(identity), identity);
  const publicConfig = usePublicConfig();
  const locale = i18n.resolvedLanguage ?? "en";
  return (
    <AdminPanel title={t("page.status")} description={t("page.statusDescription")}>
      <QueryNotice
        loading={status.isPending || publicConfig.isPending}
        error={status.error ?? publicConfig.error}
      />
      <NameCoverageNotice
        enabled={publicConfig.data?.features.ens === true}
        names={status.data?.names}
      />
      {status.data && (
        <div className="status-layout">
          <Card asChild>
            <section className="panel status-card" aria-labelledby="sync-status-title">
              <span>{publicConfig.data?.chain_name ?? t("app.tagline")}</span>
              <strong id="sync-status-title">
                {t("common.chain")} {formatInteger(status.data.chain_id, locale)}
              </strong>
              <dl>
                <div>
                  <dt>{t("context.coreReadiness")}</dt>
                  <dd>
                    <Badge
                      variant="status"
                      className={status.data.core_ready ? "availability yes" : "availability no"}
                    >
                      {status.data.core_ready ? t("context.coreReady") : t("context.coreNotReady")}
                    </Badge>
                  </dd>
                </div>
                <div>
                  <dt>{t("home.indexed")}</dt>
                  <dd>{formatInteger(status.data.indexed_block, locale)}</dd>
                </div>
                <div>
                  <dt>{t("home.highestCovered")}</dt>
                  <dd>{formatInteger(status.data.highest_covered_block, locale)}</dd>
                </div>
                <div>
                  <dt>{t("home.networkHead")}</dt>
                  <dd>{formatInteger(status.data.latest_block, locale)}</dd>
                </div>
                <div>
                  <dt>{t("home.lagBlocks")}</dt>
                  <dd>{formatInteger(status.data.lag, locale)}</dd>
                </div>
                <div>
                  <dt>{t("context.safeBlock")}</dt>
                  <dd>{formatInteger(status.data.safe_block, locale)}</dd>
                </div>
                <div>
                  <dt>{t("home.finality")}</dt>
                  <dd>{formatInteger(status.data.finalized_block, locale)}</dd>
                </div>
                <div>
                  <dt>{t("context.coverageBounds")}</dt>
                  <dd>
                    {formatInteger(status.data.coverage_start, locale)} –{" "}
                    {formatInteger(status.data.coverage_end, locale)}
                  </dd>
                </div>
                <div>
                  <dt>{t("home.backfill")}</dt>
                  <dd>
                    {status.data.backfill_complete
                      ? t("home.backfillComplete")
                      : t("home.backfillIncomplete")}
                  </dd>
                </div>
              </dl>
            </section>
          </Card>
          <div className="status-capabilities-stack">
            <Card asChild>
              <section className="panel capability-list" aria-labelledby="data-capabilities-title">
                <h2 id="data-capabilities-title">{t("status.dataCapabilities")}</h2>
                <ul>
                  {Object.entries(status.data.completeness).map(([name, state]) => (
                    <li key={name}>
                      <code>{stageLabel(name, t)}</code>
                      <Badge
                        variant="status"
                        className={state === "complete" ? "availability yes" : "availability no"}
                      >
                        {stageStateLabel(state, t)}
                      </Badge>
                    </li>
                  ))}
                </ul>
              </section>
            </Card>
            {publicConfig.data && (
              <Card asChild>
                <section
                  className="panel capability-list"
                  aria-labelledby="configured-features-title"
                >
                  <h2 id="configured-features-title">{t("status.configuredFeatures")}</h2>
                  <ul>
                    {Object.entries(publicConfig.data.features)
                      .sort(([left], [right]) => left.localeCompare(right))
                      .map(([name, enabled]) => (
                        <li key={name}>
                          <code>{featureLabel(name, t)}</code>
                          <Badge
                            variant="status"
                            className={enabled ? "availability yes" : "availability no"}
                          >
                            {enabled ? t("status.enabled") : t("status.disabled")}
                          </Badge>
                        </li>
                      ))}
                  </ul>
                </section>
              </Card>
            )}
          </div>
        </div>
      )}
    </AdminPanel>
  );
}
