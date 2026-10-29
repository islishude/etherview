import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { getName } from "@/api/heyapi/sdk.gen";
import type { NameField, NameObservation } from "@/api/heyapi/types.gen";
import { apiClient, requireEnvelope } from "@/api/client";
import { CopyableField } from "@/components/CopyButton";
import { QueryNotice } from "@/components/QueryNotice";
import { subscribeNames } from "@/ens/nameEvents";
import { expirationText, freshNameDetail, nameDeadlines } from "@/ens/nameDetails";
import { Detail, DetailList, Page } from "./pages";

export function NamePage({ name }: { name: string }) {
  const { t, i18n } = useTranslation();
  const client = useQueryClient();
  const [now, setNow] = useState(Date.now);
  const [invalid, setInvalid] = useState(false);
  const query = useQuery({
    queryKey: ["name-detail", name],
    queryFn: async ({ signal }) =>
      requireEnvelope(await getName({ client: apiClient, signal, path: { name } })).data,
    retry: false,
  });
  useEffect(
    () =>
      subscribeNames(client, (change) => {
        if (change === "invalidate") setInvalid(true);
        void client.invalidateQueries({ queryKey: ["name-detail", name] });
      }),
    [client, name],
  );
  useEffect(() => {
    setInvalid(false);
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
    <Page title={t("names.title")} description={detail?.name ?? name} mono>
      <QueryNotice loading={query.isPending} error={query.error} />
      {detail?.state === "not_indexed" && <p className="quiet">{t("names.notIndexed")}</p>}
      {detail?.state === "indexed" && (
        <>
          <p>{t(`names.${detail.registration_state}`)}</p>
          <DetailList label={t("names.title")}>
            <Detail
              label={t("names.registrant")}
              value={<AddressField field={detail.registrant} />}
            />
            <Detail
              label={t("names.controller")}
              value={<AddressField field={detail.controller} />}
            />
            <Detail
              label={t("names.expiration")}
              value={
                detail.expiration_time.state === "value"
                  ? expirationText(detail.expiration_time.value, locale)
                  : t(`names.${detail.expiration_time.state}`)
              }
            />
            <Detail
              label={t("names.address")}
              value={<AddressField field={detail.resolved_address} />}
            />
          </DetailList>
          <p className="quiet">{t("names.managerNote")}</p>
          {detail.profile_observation && (
            <Observation label={t("names.profile")} value={detail.profile_observation} />
          )}
          {detail.resolution_observation && (
            <Observation label={t("names.resolution")} value={detail.resolution_observation} />
          )}
        </>
      )}
    </Page>
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
