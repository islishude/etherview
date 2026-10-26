import { UIIcon } from "@/components/UIIcon";
import "@/styles/explorer/requests.css";
import i18n from "@/i18n";
import { requestsResources } from "@/i18n/resources/requests";
import { useState } from "react";
import {
  ChevronRight,
  UserRoundPlus,
  UserRoundMinus,
  PackagePlus,
  PackageMinus,
  ArrowRight,
  Merge,
  Layers,
  ChevronDown,
  KeyRound,
  Info,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import type { ExecutionRequest, ExecutionRequestType } from "@/api/heyapi/types.gen";
import { useBlockRequests } from "@/api/requests";
import { useCursorHistory } from "@/api/pagination";
import { CopyableField } from "@/components/CopyButton";
import { QueryNotice } from "@/components/QueryNotice";
import { formatEtherFromGwei, formatInteger } from "@/components/format";
import { AddressIdentity } from "@/ens/AddressIdentity";
import { CursorPagination, Detail } from "./pages";
import { RequestTypeFilter } from "./RequestTypeFilter";

for (const [language, resources] of Object.entries(requestsResources)) {
  i18n.addResourceBundle(language, "translation", resources, true, false);
}

const requestIcons = {
  "0x00": UserRoundPlus,
  "0x01": UserRoundMinus,
  "0x02": Merge,
  "0x03": PackagePlus,
  "0x04": PackageMinus,
};

function PublicKeyPreview({ label, value }: { label: string; value: string }) {
  return (
    <div className="request-participant">
      <span>{label}</span>
      <code title={value}>
        {value.slice(0, 14)}…{value.slice(-10)}
      </code>
    </div>
  );
}

function RequestDetails({ item, locale }: { item: ExecutionRequest; locale: string }) {
  const { t } = useTranslation();
  const Icon = requestIcons[item.request_type];
  const exit = item.request_type === "0x01" && item.amount === "0";
  return (
    <article className="request-record">
      <header className="request-card-header">
        <span className="request-type-icon" aria-hidden="true">
          <UIIcon icon={Icon} size={20} />
        </span>
        <div className="request-card-title">
          <h3>{t(`requests.types.${item.request_type}`)}</h3>
          <span className="request-card-meta">
            <code>{item.request_type}</code>
            <span>
              {t("requests.sequence", { index: formatInteger(item.sequence_index, locale) })}
            </span>
          </span>
        </div>
        {"amount" in item && (
          <div className="request-amount">
            {exit ? (
              <span className="request-exit">{t("requests.exit")}</span>
            ) : (
              <>
                <strong>{formatEtherFromGwei(item.amount, locale)}</strong> <span>ETH</span>
              </>
            )}
            <small>{formatInteger(item.amount, locale)} Gwei</small>
          </div>
        )}
      </header>
      <div className="request-participants">
        {"source_address" in item && (
          <div className="request-participant request-source">
            <span>{t("requests.fields.source_address")}</span>
            <AddressIdentity address={item.source_address} compact={false} copy />
          </div>
        )}
        {item.request_type === "0x02" ? (
          <div className="request-key-flow">
            <PublicKeyPreview
              label={t("requests.fields.source_pubkey")}
              value={item.source_pubkey}
            />
            <UIIcon icon={ArrowRight} size={16} />
            <PublicKeyPreview
              label={t("requests.fields.target_pubkey")}
              value={item.target_pubkey}
            />
          </div>
        ) : (
          <PublicKeyPreview
            label={t(
              item.request_type === "0x01"
                ? "requests.fields.validator_pubkey"
                : "requests.fields.pubkey",
            )}
            value={item.request_type === "0x01" ? item.validator_pubkey : item.pubkey}
          />
        )}
      </div>
      <details className="request-disclosure">
        <summary>
          <UIIcon icon={KeyRound} size={14} />
          <span>{t("requests.details")}</span>
          <UIIcon icon={ChevronDown} className="request-chevron" size={16} />
        </summary>
        <dl className="request-detail-grid">
          {Object.entries(item)
            .filter(
              ([key]) =>
                !["request_type", "sequence_index", "source_address", "amount"].includes(key),
            )
            .map(([key, value]) => (
              <div key={key} className={key === "signature" ? "request-detail-wide" : undefined}>
                <dt>{t(`requests.fields.${key}`)}</dt>
                <dd>
                  {key === "index" ? (
                    <code>{formatInteger(value, locale)}</code>
                  ) : (
                    <CopyableField value={value}>
                      <code className="mono-wrap">{value}</code>
                    </CopyableField>
                  )}
                </dd>
              </div>
            ))}
        </dl>
      </details>
    </article>
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
      className="panel transaction-tab-panel requests-panel"
      role="tabpanel"
      aria-label={t("blockTabs.requests")}
    >
      <header className="requests-heading">
        <div className="requests-heading-title">
          <span className="requests-heading-icon" aria-hidden="true">
            <UIIcon icon={Layers} size={24} />
          </span>
          <div>
            <h2>{t("requests.heading")}</h2>
            <p>{t("requests.subtitle")}</p>
          </div>
        </div>
        {data?.state === "complete" && !query.error && (
          <span className="requests-included">{t("requests.included")}</span>
        )}
      </header>
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
              <div className="requests-toolbar">
                <RequestTypeFilter
                  value={type}
                  counts={data.counts}
                  total={data.total}
                  onChange={setType}
                />
                <span className="requests-total">
                  {t("requests.total", { count: formatInteger(data.total, locale) })}
                </span>
              </div>
              <p className="requests-context">
                <UIIcon icon={Info} size={16} />
                {t("requests.description")}
              </p>
              {data.items.length === 0 ? (
                <div className="requests-empty">
                  <UIIcon icon={Layers} size={24} />
                  <p>{t("requests.empty")}</p>
                </div>
              ) : (
                <div className="request-list">
                  {data.items.map((item) => (
                    <RequestDetails
                      key={`${hash}:${item.request_type}:${item.sequence_index}`}
                      item={item}
                      locale={locale}
                    />
                  ))}
                </div>
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
              <details className="block-consensus-details requests-technical">
                <summary>
                  <UIIcon icon={ChevronRight} className="native-disclosure-icon" />
                  {t("consensus.technicalDetails")}
                </summary>
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
