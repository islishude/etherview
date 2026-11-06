import { Card } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { UIIcon } from "@/components/UIIcon";
import { useRef, useState } from "react";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { Search, RotateCcw } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useAddressLogs, validLogRange, type AddressLogFilter } from "@/api/addressLogs";
import { useBrowsingCursorHistory } from "@/api/pagination";
import { BrowsingScope, BrowsingEmpty } from "@/components/BrowsingScope";
import { QueryNotice } from "@/components/QueryNotice";
import { formatInteger, formatRelativeTimestamp, shorten } from "@/components/format";
import { CursorPagination } from "./pages";
import { TransactionLogCard } from "./TransactionLogs";
import { AddressLogFilters } from "./AddressLogFilters";
import {
  appliedLogFilter,
  logDraft,
  logFilterKey,
  topicKeys,
  type LogDraft,
} from "./logFilterState";
import "./AddressLogs.css";

export function AddressLogs({ address }: { address: string }) {
  const search = useSearch({ from: "/address/$address" });
  const retained = useRef<{ key: string; draft: LogDraft } | undefined>(undefined);
  const key = JSON.stringify([address, logFilterKey(search)]);
  return (
    <AddressLogsContent
      key={key}
      address={address}
      initialDraft={retained.current?.key === key ? retained.current.draft : undefined}
      retain={(filter, draft) => {
        retained.current = { key: JSON.stringify([address, logFilterKey(filter)]), draft };
      }}
    />
  );
}
function AddressLogsContent({
  address,
  initialDraft,
  retain,
}: {
  address: string;
  initialDraft?: LogDraft;
  retain: (filter: AddressLogFilter & { log_mode?: string }, draft: LogDraft) => void;
}) {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage ?? "en";
  const search = useSearch({ from: "/address/$address" });
  const navigate = useNavigate();
  const [draft, setDraft] = useState(initialDraft ?? logDraft(search));
  const mode = draft.mode;
  const [from, setFrom] = useState(search.from_block ?? "");
  const [to, setTo] = useState(search.to_block ?? "");
  const [error, setError] = useState("");
  const rangeValid = validLogRange(from, to);
  const filter: AddressLogFilter = {
    from_block: search.from_block,
    to_block: search.to_block,
    event_signature: search.log_mode === "topic" ? undefined : search.event_signature,
    event_id: search.log_mode === "topic" ? undefined : search.event_id,
    ...Object.fromEntries(topicKeys.map((key) => [key, search[key]])),
  };
  const pager = useBrowsingCursorHistory(`address-logs:${address}:${JSON.stringify(filter)}`);
  const logs = useAddressLogs(address, filter, pager.request);
  const status = logs.data?.meta.browsing;
  const future = Boolean(status && BigInt(status.from_block) > BigInt(status.to_block));
  const appliedRange = `${search.from_block || "0"} – ${search.to_block || t("addressLogs.snapshot")}`;
  const submit = () => {
    if (!rangeValid) {
      setError("addressLogs.invalidRange");
      return;
    }
    let applied: AddressLogFilter;
    try {
      applied = appliedLogFilter(draft);
    } catch {
      setError(mode === "topic" ? "addressLogs.invalidTopic" : "addressLogs.invalidValue");
      return;
    }
    const next = {
      tab: "logs",
      log_mode: mode,
      from_block: from || undefined,
      to_block: to || undefined,
      ...applied,
    };
    retain(next, draft);
    setError("");
    void navigate({
      to: "/address/$address",
      params: { address },
      hash: "",
      search: next,
    });
    pager.reset();
  };
  return (
    <Card asChild>
      <section
        className="panel transaction-tab-panel address-logs"
        aria-labelledby="address-logs-title"
      >
        <div className="address-logs-heading">
          <div>
            <h2 id="address-logs-title">{t("addressLogs.title")}</h2>
            <p>{t("addressLogs.subtitle")}</p>
          </div>
        </div>
        <form
          className="address-log-toolbar"
          onSubmit={(event) => {
            event.preventDefault();
            submit();
          }}
        >
          <div className="address-log-segments" role="group" aria-label={t("addressLogs.mode")}>
            {(["name", "topic"] as const).map((value) => (
              <Button
                variant="outline"
                type="button"
                key={value}
                aria-pressed={mode === value}
                onClick={() => {
                  if (mode === value) return;
                  setDraft({ mode: value, eventID: "", signature: "", conditions: [] });
                  setError("");
                }}
              >
                {t(value === "name" ? "addressLogs.byName" : "addressLogs.byTopic")}
              </Button>
            ))}
          </div>
          <div className="address-log-filter-row">
            <div className="address-log-main-filter">
              <AddressLogFilters
                address={address}
                draft={draft}
                range={{ from_block: from || undefined, to_block: to || undefined }}
                disabled={!rangeValid}
                invalid={!!error}
                onChange={setDraft}
              />
            </div>
            <div className="address-log-range">
              <Label>
                {t("addressLogs.fromBlock")}
                <Input
                  inputMode="numeric"
                  maxLength={78}
                  value={from}
                  placeholder="0"
                  aria-invalid={!rangeValid}
                  onChange={(e) => {
                    setFrom(e.target.value);
                    setError("");
                  }}
                />
              </Label>
              <span aria-hidden="true">–</span>
              <Label>
                {t("addressLogs.toBlock")}
                <Input
                  inputMode="numeric"
                  maxLength={78}
                  value={to}
                  placeholder={t("addressLogs.snapshot")}
                  aria-invalid={!rangeValid}
                  onChange={(e) => {
                    setTo(e.target.value);
                    setError("");
                  }}
                />
              </Label>
            </div>
            <div className="address-log-actions">
              <Button className="address-log-submit" type="submit" disabled={logs.isFetching}>
                <UIIcon icon={Search} size={16} />
                {t("addressLogs.apply")}
              </Button>
              <Button
                variant="outline"
                type="button"
                onClick={() => {
                  setDraft(logDraft({}));
                  setFrom("");
                  setTo("");
                  setError("");
                  void navigate({
                    to: "/address/$address",
                    params: { address },
                    hash: "",
                    search: { tab: "logs" },
                  });
                  pager.reset();
                }}
              >
                <UIIcon icon={RotateCcw} size={16} />
                {t("addressLogs.clear")}
              </Button>
            </div>
          </div>
          {(error || !rangeValid) && (
            <p className="address-log-error" role="alert">
              {t(error || "addressLogs.invalidRange")}
            </p>
          )}
        </form>
        <div className="address-log-results-bar">
          <span>{t("addressLogs.pageCount", { count: logs.data?.data.length ?? 0 })}</span>
          <div className="address-log-applied" aria-label={t("addressLogs.applied")}>
            <span>{appliedRange}</span>
            {filter.event_signature && <span>{filter.event_signature}</span>}
            {filter.event_id && (
              <span>
                {draft.event?.event_id === filter.event_id
                  ? draft.event.signature
                  : filter.event_id}
              </span>
            )}
            {topicKeys.map(
              (key) =>
                filter[key] && (
                  <span key={key} title={filter[key]}>
                    {key}: {shorten(filter[key]!)}
                  </span>
                ),
            )}
          </div>
        </div>
        {status && !future && (
          <p className="address-log-coverage">
            {t("addressLogs.throughBlock", { number: formatInteger(status.to_block, locale) })}
          </p>
        )}
        <BrowsingScope status={status}>
          <QueryNotice
            loading={
              logs.isPending && validLogRange(filter.from_block ?? "", filter.to_block ?? "")
            }
            error={logs.error}
            onReset={pager.reset}
          />
          {!logs.error && logs.data?.data.length === 0 && (
            <p className="empty-result">
              {future ? (
                t("addressLogs.futureRange")
              ) : (
                <BrowsingEmpty>{t("addressLogs.empty")}</BrowsingEmpty>
              )}
            </p>
          )}
          {!logs.error &&
            logs.data?.data.map((item) => (
              <div key={`${item.block_hash}:${item.log.log_index}`} className="address-log-entry">
                <TransactionLogCard
                  compact
                  log={item.log}
                  locale={locale}
                  context={
                    <div className="address-log-context">
                      <Link
                        to="/tx/$hash"
                        params={{ hash: item.transaction_hash }}
                        search={{ tab: "logs" }}
                        title={item.transaction_hash}
                      >
                        {shorten(item.transaction_hash)}
                      </Link>
                      <Link to="/block/$blockID" params={{ blockID: item.block_hash }}>
                        {t("addressLogs.block", {
                          number: formatInteger(item.block_number, locale),
                        })}
                      </Link>
                      <Link to="/block/$blockID" params={{ blockID: item.block_hash }}>
                        <time dateTime={item.block_timestamp} title={item.block_timestamp}>
                          {formatRelativeTimestamp(item.block_timestamp, locale)}
                        </time>
                      </Link>
                    </div>
                  }
                />
              </div>
            ))}
          {logs.data && !logs.error && (
            <CursorPagination
              label={t("addressLogs.title")}
              busy={logs.isFetching}
              hasNext={!pager.invalid && Boolean(logs.data.meta.next_cursor)}
              hasPrevious={pager.hasPrevious}
              onNext={() => pager.next(logs.data?.meta.next_cursor)}
              onPrevious={pager.previous}
              page={pager.page}
            />
          )}
        </BrowsingScope>
      </section>
    </Card>
  );
}
