import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { getName } from "@/api/heyapi/sdk.gen";
import type { NameField, NameObservation } from "@/api/heyapi/types.gen";
import { apiClient, requireEnvelope } from "@/api/client";
import { CopyButton, CopyableField } from "@/components/CopyButton";
import { BrowsingRefreshControl } from "@/components/BrowsingRefreshControl";
import { QueryNotice } from "@/components/QueryNotice";
import { useQueryWithdrawn } from "@/api/browsingHooks";
import { expirationText, freshNameDetail, nameDeadlines } from "@/ens/nameDetails";
import { NameHistory } from "@/ens/NameHistory";
import "./NamePage.css";

export function NamePage({ name }: { name: string }) {
  const { t, i18n } = useTranslation();
  const [now, setNow] = useState(Date.now);
  const invalid = useQueryWithdrawn(["name-detail", name]);
  const query = useQuery({
    queryKey: ["name-detail", name],
    meta: { chainEvents: ["names", "reorg"] },
    queryFn: async ({ signal }) =>
      requireEnvelope(await getName({ client: apiClient, signal, path: { name } })).data,
    retry: false,
  });
  useEffect(() => {
    setNow(Date.now());
  }, [query.dataUpdatedAt]);
  useEffect(() => {
    const deadline = Math.min(...nameDeadlines(query.data).filter((value) => value > now));
    if (!Number.isFinite(deadline)) return;
    const timer = setTimeout(
      () => setNow(Date.now()),
      Math.min(Math.max(1, deadline - Date.now()), 2_147_483_647),
    );
    return () => clearTimeout(timer);
  }, [query.data, now]);
  const detail = query.data ? freshNameDetail(query.data, now, invalid) : undefined;
  const locale = i18n.resolvedLanguage ?? "en";
  return (
    <div className="page-stack inner-page name-page">
      <header className="name-heading">
        <p className="name-eyebrow">ENS</p>
        <div className="name-title-row">
          <h1>{detail?.name ?? name}</h1>
          <CopyButton value={detail?.name ?? name} />
          <BrowsingRefreshControl />
        </div>
        {detail?.state === "indexed" && (
          <span className={`name-status name-status-${detail.registration_state}`}>
            {t(`names.${detail.registration_state}`)}
          </span>
        )}
      </header>
      <QueryNotice loading={query.isPending} error={query.error} />
      {detail?.state === "not_indexed" && <p className="context-note">{t("names.notIndexed")}</p>}
      {detail?.state === "indexed" && (
        <section className="name-overview" aria-label={t("names.title")}>
          <dl className="name-facts">
            <div>
              <dt>{t("names.registrant")}</dt>
              <dd>
                <AddressField field={detail.registrant} />
              </dd>
            </div>
            <div>
              <dt>{t("names.controller")}</dt>
              <dd>
                <AddressField field={detail.controller} />
              </dd>
            </div>
            <div>
              <dt>{t("names.address")}</dt>
              <dd>
                <AddressField field={detail.resolved_address} />
              </dd>
            </div>
            <div>
              <dt>{t("names.expiration")}</dt>
              <dd>
                {detail.expiration_time.state === "value"
                  ? expirationText(detail.expiration_time.value, locale)
                  : t(`names.${detail.expiration_time.state}`)}
              </dd>
            </div>
          </dl>
          <details className="name-data-details">
            <summary>{t("names.dataDetails")}</summary>
            <p className="quiet">{t("names.managerNote")}</p>
            {detail.profile_observation && (
              <Observation label={t("names.profile")} value={detail.profile_observation} />
            )}
            {detail.resolution_observation && (
              <Observation label={t("names.resolution")} value={detail.resolution_observation} />
            )}
          </details>
        </section>
      )}
      <NameHistory key={name} name={detail?.name ?? name} />
    </div>
  );
}
function AddressField({ field }: { field: NameField }) {
  const { t } = useTranslation();
  if (field.state !== "value" || !field.value) return <span>{t(`names.${field.state}`)}</span>;
  return (
    <CopyableField value={field.value}>
      <Link to="/address/$address" params={{ address: field.value }}>
        <code className="mono-wrap">{field.value}</code>
      </Link>
    </CopyableField>
  );
}
function Observation({ label, value }: { label: string; value: NameObservation }) {
  const { t, i18n } = useTranslation();
  return (
    <p className="quiet">
      {label}: {t("names.observed")}{" "}
      <Link to="/blocks/$blockID" params={{ blockID: value.observed_block_hash }}>
        {value.observed_block_number}
      </Link>
      {" · "}
      {t("names.validUntil")}{" "}
      {new Intl.DateTimeFormat(i18n.resolvedLanguage ?? "en", {
        dateStyle: "long",
        timeStyle: "long",
      }).format(new Date(value.valid_until))}
    </p>
  );
}
