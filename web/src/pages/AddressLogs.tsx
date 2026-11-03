import { UIIcon } from "@/components/UIIcon";
import { useState } from "react";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { Search, RotateCcw } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useAddressLogs, validLogRange, type AddressLogFilter } from "@/api/addressLogs";
import { useBrowsingCursorHistory } from "@/api/pagination";
import { BrowsingScope, BrowsingEmpty } from "@/components/BrowsingScope";
import { SelectControl } from "@/components/SelectControl";
import { QueryNotice } from "@/components/QueryNotice";
import { formatInteger, formatRelativeTimestamp, shorten } from "@/components/format";
import { CursorPagination } from "./pages";
import { TransactionLogCard } from "./TransactionLogs";
import { EventNameSelect } from "./EventNameSelect";
import "./AddressLogs.css";

export function AddressLogs({ address }: { address: string }) {
  const search = useSearch({ from: "/address/$address" });
  return <AddressLogsContent key={JSON.stringify([address, search])} address={address} />;
}
function AddressLogsContent({ address }: { address: string }) {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage ?? "en";
  const search = useSearch({ from: "/address/$address" });
  const navigate = useNavigate();
  const [mode, setMode] = useState(search.log_mode ?? "name");
  const [signature, setSignature] = useState(search.event_signature ?? "");
  const [topic, setTopic] = useState(search.topic ?? "");
  const [position, setPosition] = useState(search.topic_index ?? 0);
  const [from, setFrom] = useState(search.from_block ?? "");
  const [to, setTo] = useState(search.to_block ?? "");
  const [error, setError] = useState("");
  const rangeValid = validLogRange(from, to);
  const filter: AddressLogFilter = {
    from_block: search.from_block,
    to_block: search.to_block,
    ...(search.log_mode === "topic"
      ? { topic_index: search.topic ? (search.topic_index ?? 0) : undefined, topic: search.topic }
      : { event_signature: search.event_signature }),
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
    const value = topic.trim();
    if (mode === "topic" && value && !/^0x[0-9a-fA-F]{64}$/u.test(value)) {
      setError("addressLogs.invalidTopic");
      return;
    }
    setError("");
    void navigate({
      to: "/address/$address",
      params: { address },
      hash: "",
      search: {
        tab: "logs",
        log_mode: mode,
        from_block: from || undefined,
        to_block: to || undefined,
        ...(mode === "name"
          ? { event_signature: signature || undefined }
          : { topic: value.toLowerCase() || undefined, topic_index: value ? position : undefined }),
      },
    });
    pager.reset();
  };
  return (
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
            <button
              type="button"
              key={value}
              aria-pressed={mode === value}
              onClick={() => {
                if (mode === value) return;
                setMode(value);
                setSignature("");
                setTopic("");
                setError("");
              }}
            >
              {t(value === "name" ? "addressLogs.byName" : "addressLogs.byTopic")}
            </button>
          ))}
        </div>
        <div className="address-log-filter-row">
          <div className="address-log-main-filter">
            {mode === "name" ? (
              <EventNameSelect
                address={address}
                value={signature}
                range={{ from_block: from || undefined, to_block: to || undefined }}
                disabled={!rangeValid}
                onChange={setSignature}
              />
            ) : (
              <div className="address-log-topic-fields">
                <label>
                  {t("addressLogs.position")}
                  <SelectControl
                    value={position}
                    onChange={(e) => setPosition(Number(e.target.value))}
                  >
                    {[0, 1, 2, 3].map((index) => (
                      <option key={index} value={index}>
                        topic{index}
                      </option>
                    ))}
                  </SelectControl>
                </label>
                <label className="address-log-topic-input">
                  {t("addressLogs.topic")}
                  <input
                    value={topic}
                    maxLength={66}
                    spellCheck={false}
                    aria-invalid={error === "addressLogs.invalidTopic"}
                    onChange={(e) => {
                      setTopic(e.target.value);
                      setError("");
                    }}
                    placeholder="0x…"
                  />
                </label>
              </div>
            )}
          </div>
          <div className="address-log-range">
            <label>
              {t("addressLogs.fromBlock")}
              <input
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
            </label>
            <span aria-hidden="true">–</span>
            <label>
              {t("addressLogs.toBlock")}
              <input
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
            </label>
          </div>
          <div className="address-log-actions">
            <button className="address-log-submit" type="submit" disabled={logs.isFetching}>
              <UIIcon icon={Search} size={16} />
              {t("addressLogs.apply")}
            </button>
            <button
              type="button"
              onClick={() => {
                setMode("name");
                setSignature("");
                setTopic("");
                setPosition(0);
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
            </button>
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
          {filter.topic && (
            <span title={filter.topic}>
              topic{filter.topic_index}: {shorten(filter.topic)}
            </span>
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
          loading={logs.isPending && validLogRange(filter.from_block ?? "", filter.to_block ?? "")}
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
                      {t("addressLogs.block", { number: formatInteger(item.block_number, locale) })}
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
  );
}
