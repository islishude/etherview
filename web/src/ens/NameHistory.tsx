import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { ChevronDown } from "lucide-react";
import { listNameHistory, listNameHistoryEvents } from "@/api/heyapi/sdk.gen";
import type { NameHistoryField, NameHistoryTransaction } from "@/api/heyapi/types.gen";
import { apiClient, requireEnvelope } from "@/api/client";
import { useCursorHistory } from "@/api/pagination";
import { CopyableField } from "@/components/CopyButton";
import { QueryNotice } from "@/components/QueryNotice";
import { UIIcon } from "@/components/UIIcon";
import { shorten } from "@/components/format";
import { CursorPagination } from "@/pages/pages";

export function NameHistory({ name }: { name: string }) {
  const { t } = useTranslation();
  const pager = useCursorHistory(`name-history:${name}`);
  const query = useQuery({
    meta: { chainEvents: ["names", "reorg"], pagination: pager.request.identity },
    queryKey: ["name-history", name, pager.request],
    queryFn: async ({ signal }) =>
      requireEnvelope(
        await listNameHistory({
          client: apiClient,
          signal,
          path: { name },
          query: { limit: 25, cursor: pager.request.cursor },
        }),
      ),
    retry: false,
  });
  const data = query.data?.data;
  const reset = pager.reset;
  return (
    <section className="name-history" aria-labelledby="name-history-heading">
      <div className="name-section-heading">
        <h2 id="name-history-heading">{t("names.history.title")}</h2>
      </div>
      <QueryNotice loading={query.isPending} error={query.error} onReset={reset} />
      {data && (
        <>
          {data.coverage.state !== "complete" && (
            <p className="context-note" role="status">
              {t(`names.history.coverage.${data.coverage.state}`)}
            </p>
          )}
          {data.items.length === 0 && (
            <p className="name-history-empty">
              {t(
                data.state === "not_indexed"
                  ? "names.notIndexed"
                  : data.coverage.state === "complete"
                    ? "names.history.empty"
                    : "names.history.noKnown",
              )}
            </p>
          )}
          <div className="name-history-list">
            {data.items.map((item) => (
              <HistoryRow
                key={`${data.snapshot}:${item.block_hash}:${item.transaction_hash}`}
                name={name}
                snapshot={data.snapshot}
                item={item}
                onReset={reset}
              />
            ))}
          </div>
          {data.items.length > 0 && (
            <CursorPagination
              busy={query.isFetching}
              hasNext={Boolean(query.data?.meta.next_cursor)}
              hasPrevious={pager.hasPrevious}
              label={t("names.history.title")}
              onNext={() => pager.next(query.data?.meta.next_cursor)}
              onPrevious={pager.previous}
              page={pager.page}
            />
          )}
          <details className="name-data-details">
            <summary>{t("names.history.coverageDetails")}</summary>
            <p className="quiet">{t("names.history.evidenceNote")}</p>
            <p className="quiet">
              {t("names.history.range", {
                start: data.coverage.start,
                scanned: data.coverage.scanned ?? "—",
                target: data.coverage.target ?? "—",
                pending: data.coverage.pending,
              })}
            </p>
          </details>
        </>
      )}
    </section>
  );
}

function HistoryRow({
  name,
  snapshot,
  item,
  onReset,
}: {
  name: string;
  snapshot: string;
  item: NameHistoryTransaction;
  onReset: () => void;
}) {
  const { t, i18n } = useTranslation();
  const [open, setOpen] = useState(false);
  const actions = item.actions.map((action) => t(`names.history.actions.${action}`));
  const timestamp = new Intl.DateTimeFormat(i18n.resolvedLanguage ?? "en", {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(item.timestamp));
  const id = `events-${item.block_hash}-${item.transaction_hash}`;
  return (
    <article className={`name-history-row${open ? " is-open" : ""}`}>
      <div className="name-history-summary">
        <button
          type="button"
          className="name-history-toggle"
          aria-expanded={open}
          aria-controls={id}
          onClick={() => setOpen(!open)}
        >
          <UIIcon icon={ChevronDown} className="name-history-chevron" />
          <span>
            <span className="name-history-action">
              {actions.slice(0, 3).join(" · ")}
              {actions.length > 3 && ` +${actions.length - 3}`}
            </span>
            <time dateTime={item.timestamp}>{timestamp}</time>
          </span>
        </button>
        <span className="name-history-transaction">
          <Link
            to="/tx/$hash"
            params={{ hash: item.transaction_hash }}
            search={{ tab: "overview" }}
            title={item.transaction_hash}
          >
            <code>{shorten(item.transaction_hash)}</code>
          </Link>
          <span className="quiet">
            {t("names.history.events", { count: Number(item.event_count) })}
          </span>
        </span>
      </div>
      <div id={id} hidden={!open}>
        {open && <HistoryEvents name={name} snapshot={snapshot} item={item} onReset={onReset} />}
      </div>
    </article>
  );
}

function HistoryEvents({
  name,
  snapshot,
  item,
  onReset,
}: {
  name: string;
  snapshot: string;
  item: NameHistoryTransaction;
  onReset: () => void;
}) {
  const { t } = useTranslation();
  const pager = useCursorHistory(`${name}:${snapshot}:${item.block_hash}:${item.transaction_hash}`);
  const query = useQuery({
    meta: { chainEvents: ["names", "reorg"], pagination: pager.request.identity },
    queryKey: [
      "name-history-events",
      name,
      snapshot,
      item.block_hash,
      item.transaction_hash,
      pager.request,
    ],
    queryFn: async ({ signal }) =>
      requireEnvelope(
        await listNameHistoryEvents({
          client: apiClient,
          signal,
          path: { name, transactionHash: item.transaction_hash },
          query: { snapshot, block_hash: item.block_hash, cursor: pager.request.cursor, limit: 50 },
        }),
      ),
    retry: false,
  });
  return (
    <div className="name-history-events">
      <p className="quiet">
        {t("names.history.block")}{" "}
        <Link to="/blocks/$blockID" params={{ blockID: item.block_hash }}>
          {item.block_number}
        </Link>
      </p>
      <QueryNotice loading={query.isPending} error={query.error} onReset={onReset} />
      {query.data?.data.items.map((event) => (
        <section
          className="name-history-event"
          key={event.log_index}
          aria-label={`${event.event_name} #${event.log_index}`}
        >
          <div className="name-event-heading">
            <h3>{t(`names.history.actions.${event.action}`)}</h3>
            <span className="quiet">
              {event.event_name} · #{event.log_index}
            </span>
          </div>
          <dl>
            <div>
              <dt>{t("names.history.contract")}</dt>
              <dd>
                <HistoryValue
                  field={{
                    key: "emitter",
                    type: "address",
                    value: event.emitter,
                    truncated: false,
                  }}
                />
              </dd>
            </div>
            {event.fields.map((field) => (
              <div key={field.key}>
                <dt>{field.key}</dt>
                <dd>
                  <HistoryValue field={field} />
                </dd>
              </div>
            ))}
          </dl>
        </section>
      ))}
      {query.data && (query.data.meta.next_cursor || pager.hasPrevious) && (
        <CursorPagination
          busy={query.isFetching}
          hasNext={Boolean(query.data.meta.next_cursor)}
          hasPrevious={pager.hasPrevious}
          label={t("names.history.eventPages")}
          onNext={() => pager.next(query.data?.meta.next_cursor)}
          onPrevious={pager.previous}
          page={pager.page}
        />
      )}
    </div>
  );
}
function HistoryValue({ field }: { field: NameHistoryField }) {
  const { t } = useTranslation();
  if (field.type === "address" && /^0x[0-9a-fA-F]{40}$/.test(field.value))
    return (
      <CopyableField value={field.value}>
        <Link to="/address/$address" params={{ address: field.value }}>
          <code>{field.value}</code>
        </Link>
      </CopyableField>
    );
  return (
    <>
      <code className="name-event-value">
        {field.value === "" ? t("names.history.emptyValue") : field.value}
      </code>
      {field.truncated && <span className="quiet"> {t("names.history.truncated")}</span>}
    </>
  );
}
