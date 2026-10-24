import i18n from "@/i18n";
import { requestsResources } from "@/i18n/resources/requests";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { ExecutionRequest, ExecutionRequestType } from "@/api/heyapi/types.gen";
import { useBlockRequests } from "@/api/requests";
import { useCursorHistory } from "@/api/pagination";
import { CopyableField } from "@/components/CopyButton";
import { QueryNotice } from "@/components/QueryNotice";
import { formatEtherFromGwei, formatInteger } from "@/components/format";
import { AddressIdentity } from "@/ens/AddressIdentity";
import { CursorPagination, Detail } from "./pages";

for (const [language, resources] of Object.entries(requestsResources)) {
  i18n.addResourceBundle(language, "translation", resources, true, false);
}

const requestTypes = ["0x00", "0x01", "0x02", "0x03", "0x04"] as const;

function RequestDetails({ item, locale }: { item: ExecutionRequest; locale: string }) {
  const { t } = useTranslation();
  return (
    <details className="block-consensus-details request-record">
      <summary>
        {t(`requests.types.${item.request_type}`)} · #{formatInteger(item.sequence_index, locale)}
        {"amount" in item && ` · ${formatEtherFromGwei(item.amount, locale)} Ether`}
        {item.request_type === "0x01" && item.amount === "0" && ` · ${t("requests.exit")}`}
      </summary>
      <dl className="block-consensus-detail-list">
        {Object.entries(item).map(([key, value]) => (
          <Detail
            key={key}
            label={t(`requests.fields.${key}`)}
            value={
              key === "source_address" ? (
                <AddressIdentity address={value} compact={false} />
              ) : key === "amount" ? (
                <code>{formatInteger(value, locale)} Gwei</code>
              ) : key === "index" || key === "sequence_index" ? (
                <code>{formatInteger(value, locale)}</code>
              ) : (
                <CopyableField value={value}>
                  <code className="mono-wrap">{value}</code>
                </CopyableField>
              )
            }
          />
        ))}
      </dl>
    </details>
  );
}

export function RequestsPanel({ hash }: { hash: string }) {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage ?? "en";
  const [type, setType] = useState<ExecutionRequestType | "">("");
  const pager = useCursorHistory(`block-requests:${hash}:${type}`);
  const query = useBlockRequests(hash, type, pager.request);
  const data = query.data?.data;
  return (
    <section
      className="panel transaction-tab-panel block-consensus-panel"
      role="tabpanel"
      aria-label={t("blockTabs.requests")}
    >
      <p>{t("requests.description")}</p>
      <QueryNotice loading={query.isPending} error={query.error} onReset={pager.reset} compact />
      {data && !query.error && (
        <>
          {data.state !== "complete" ? (
            <div className="block-consensus-notice" role="status">
              <strong>{t(`consensus.states.${data.state}`)}</strong>
              {data.code && (
                <p>
                  {t(`consensus.codes.${data.code}`, { defaultValue: t("requests.unavailable") })}
                </p>
              )}
            </div>
          ) : (
            <>
              <p>{t("requests.total", { count: formatInteger(data.total, locale) })}</p>
              <label className="request-filter">
                {t("requests.filter")}
                <select
                  value={type}
                  onChange={(event) => setType(event.target.value as ExecutionRequestType | "")}
                >
                  <option value="">{t("requests.all")}</option>
                  {requestTypes.map((kind) => (
                    <option key={kind} value={kind}>
                      {t(`requests.types.${kind}`)} (
                      {formatInteger(data.counts?.[kind] ?? "0", locale)})
                    </option>
                  ))}
                </select>
              </label>
              {data.items.length === 0 ? (
                <p className="empty-result">{t("requests.empty")}</p>
              ) : (
                data.items.map((item) => (
                  <RequestDetails
                    key={`${hash}:${item.request_type}:${item.sequence_index}`}
                    item={item}
                    locale={locale}
                  />
                ))
              )}
              <CursorPagination
                busy={query.isFetching}
                hasNext={Boolean(query.data?.meta.next_cursor)}
                hasPrevious={pager.hasPrevious}
                label={t("blockTabs.requests")}
                onNext={() => pager.next(query.data?.meta.next_cursor)}
                onPrevious={pager.previous}
                page={pager.page}
              />
              <details className="block-consensus-details">
                <summary>{t("consensus.technicalDetails")}</summary>
                <dl className="block-consensus-detail-list">
                  <Detail
                    label={t("requests.hash")}
                    value={<code className="mono-wrap">{data.requests_hash}</code>}
                  />
                  <Detail
                    label={t("consensus.root")}
                    value={<code className="mono-wrap">{data.beacon_root}</code>}
                  />
                </dl>
              </details>
            </>
          )}
        </>
      )}
    </section>
  );
}
