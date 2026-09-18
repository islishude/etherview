import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { TransactionLog } from "@/api/types";
import { formatInteger } from "@/components/format";
import { CopyableField } from "@/components/CopyButton";
import { AddressIdentity } from "@/ens/AddressIdentity";
import {
  flattenLogArgument,
  formatTopicValue,
  isAnonymousDecodedLog,
  type LogArgumentRow,
  type TopicDisplayMode,
} from "@/components/logFormat";
import {
  abiSourceKindLabel,
  attributionLabel,
  confidenceLabel,
  formatLogArgument,
  logDecodingKey,
  yesNo,
} from "./pages";

export function TransactionLogCard({ log, locale }: { log: TransactionLog; locale: string }) {
  const { t } = useTranslation();
  const [topicModes, setTopicModes] = useState<Record<string, TopicDisplayMode>>({});
  const signature =
    log.decoding.signature ??
    log.decoding.event_name ??
    t("detail.logIndex", { index: formatInteger(log.log_index, locale) });
  const decoded = log.decoding.status === "decoded";
  const anonymous = isAnonymousDecodedLog(
    log.decoding.status,
    log.topics.length,
    log.decoding.arguments,
  );
  const setTopicMode = (index: number, mode: TopicDisplayMode) => {
    setTopicModes((current) => ({ ...current, [String(index)]: mode }));
  };

  return (
    <article className="transaction-log">
      <header className="transaction-log-header">
        <div className="transaction-log-heading">
          <strong>{signature}</strong>
          {!decoded && (
            <small className="transaction-log-status">
              {t(logDecodingKey(log.decoding.status))}
            </small>
          )}
        </div>
        <span className="transaction-log-index">
          <span className="sr-only">
            {t("detail.logIndex", { index: formatInteger(log.log_index, locale) })}
          </span>
          {formatInteger(log.log_index, locale)}
        </span>
      </header>

      <div className="transaction-log-address">
        <span className="transaction-log-label">{t("detail.address")}</span>
        <CopyableField value={log.address}>
          <AddressIdentity address={log.address} compact={false} />
        </CopyableField>
      </div>

      {log.decoding.arguments.length > 0 && (
        <TransactionLogArgumentsTable arguments={log.decoding.arguments} />
      )}

      {log.decoding.warning && (
        <p className="quiet transaction-log-warning">{log.decoding.warning}</p>
      )}

      <details className="transaction-more-details transaction-log-details">
        <summary>{t("detail.moreDetails")}</summary>
        <div className="transaction-log-details-content">
          <TransactionLogProvenance log={log} />
          <TransactionLogTopicsAndData
            allowTopicZeroConversion={anonymous}
            data={log.data}
            logIndex={log.log_index}
            onTopicModeChange={setTopicMode}
            topicModes={topicModes}
            topics={log.topics}
          />
        </div>
      </details>
    </article>
  );
}

function TransactionLogArgumentsTable({
  arguments: values,
}: {
  arguments: TransactionLog["decoding"]["arguments"];
}) {
  const { t } = useTranslation();
  const rows = values.flatMap((argument, index) => flattenLogArgument(argument, index));

  return (
    <div className="transaction-log-arguments-scroll" tabIndex={0}>
      <div
        className="transaction-log-arguments-table"
        role="table"
        aria-label={t("detail.eventArguments")}
      >
        <div className="transaction-log-arguments-row transaction-log-arguments-header" role="row">
          <span role="columnheader">{t("detail.argumentName")}</span>
          <span role="columnheader">{t("detail.argumentType")}</span>
          <span role="columnheader">{t("detail.argumentIndexed")}</span>
          <span role="columnheader">{t("detail.argumentData")}</span>
        </div>
        {rows.map((row, index) => (
          <TransactionLogArgumentRow key={`${row.path}:${index}`} row={row} />
        ))}
      </div>
    </div>
  );
}

function TransactionLogArgumentRow({ row }: { row: LogArgumentRow }) {
  const { t } = useTranslation();
  const value = formatLogArgument(row.value);
  return (
    <div
      className={`transaction-log-arguments-row transaction-log-argument-depth-${Math.min(row.depth, 6)}${row.composite ? " transaction-log-argument-composite" : ""}`}
      role="row"
    >
      <span className="transaction-log-argument-name" role="cell">
        {row.path}
      </span>
      <code className="transaction-log-argument-type" role="cell">
        {row.type || "—"}
      </code>
      <span className="transaction-log-argument-indexed" role="cell">
        {row.indexed === undefined ? "—" : yesNo(row.indexed, t)}
      </span>
      <span className="transaction-log-argument-data" role="cell">
        <CopyableField value={value}>
          <code>{value}</code>
        </CopyableField>
      </span>
    </div>
  );
}

function TransactionLogProvenance({ log }: { log: TransactionLog }) {
  const { t } = useTranslation();
  const source = log.decoding.abi_source;
  const attribution = log.decoding.attribution;
  return (
    <section
      className="transaction-log-provenance"
      aria-labelledby={`log-provenance-${log.log_index}`}
    >
      <h3 id={`log-provenance-${log.log_index}`}>{t("detail.abiProvenance")}</h3>
      <div className="transaction-log-provenance-grid">
        {source && (
          <div className="transaction-log-provenance-card">
            <h4>{t("detail.abiSourceLabel")}</h4>
            <dl>
              <div>
                <dt>{t("detail.sourceKind")}</dt>
                <dd>
                  <span className="transaction-provenance-badge">
                    {abiSourceKindLabel(source.kind, t)}
                  </span>
                </dd>
              </div>
              {source.address && (
                <div>
                  <dt>{t("detail.sourceAddress")}</dt>
                  <dd>
                    <AddressIdentity address={source.address} compact={false} copy />
                  </dd>
                </div>
              )}
              {source.code_hash && (
                <div>
                  <dt>{t("detail.sourceCodeHash")}</dt>
                  <dd>
                    <CopyableField value={source.code_hash}>
                      <code>{source.code_hash}</code>
                    </CopyableField>
                  </dd>
                </div>
              )}
              {log.decoding.confidence && (
                <div>
                  <dt>{t("detail.confidence")}</dt>
                  <dd>
                    <span className="transaction-provenance-badge">
                      {confidenceLabel(log.decoding.confidence, t)}
                    </span>
                  </dd>
                </div>
              )}
            </dl>
          </div>
        )}

        <div className="transaction-log-provenance-card">
          <h4>{t("detail.executionProvenance")}</h4>
          <dl>
            <div>
              <dt>{t("detail.emitterAddress")}</dt>
              <dd>
                <AddressIdentity address={log.address} compact={false} copy />
              </dd>
            </div>
            {attribution.execution_address && (
              <div>
                <dt>{t("detail.executionAddress")}</dt>
                <dd>
                  <AddressIdentity address={attribution.execution_address} compact={false} copy />
                </dd>
              </div>
            )}
            <div>
              <dt>{t("detail.attribution")}</dt>
              <dd>
                <span className="transaction-provenance-badge">
                  {attributionLabel(attribution.mode, t)}
                </span>
              </dd>
            </div>
            <div>
              <dt>{t("detail.tracePath")}</dt>
              <dd>
                <code>[{attribution.trace_path.join(", ")}]</code>
              </dd>
            </div>
          </dl>
        </div>
      </div>
    </section>
  );
}

function TransactionLogTopicsAndData({
  allowTopicZeroConversion,
  data,
  logIndex,
  onTopicModeChange,
  topicModes,
  topics,
}: {
  allowTopicZeroConversion: boolean;
  data: string;
  logIndex: string;
  onTopicModeChange: (index: number, mode: TopicDisplayMode) => void;
  topicModes: Record<string, TopicDisplayMode>;
  topics: readonly string[];
}) {
  const { t } = useTranslation();
  return (
    <>
      <section className="transaction-log-topics" aria-labelledby={`log-topics-${logIndex}`}>
        <h3 id={`log-topics-${logIndex}`}>{t("detail.topics")}</h3>
        <div className="transaction-topic-list">
          {topics.map((topic, index) => {
            const convertible = index > 0 || allowTopicZeroConversion;
            const mode = topicModes[String(index)] ?? "hex";
            const value = formatTopicValue(topic, mode);
            const displayValue = value ?? t("detail.topicUnavailable");
            return (
              <div
                className={`transaction-topic${convertible ? " transaction-topic-convertible" : ""}`}
                key={`${topic}:${index}`}
              >
                <span className="transaction-topic-index" aria-hidden="true">
                  {index}
                </span>
                {!convertible ? (
                  <CopyableField value={topic}>
                    <code>{topic}</code>
                  </CopyableField>
                ) : (
                  <>
                    <label className="sr-only" htmlFor={`topic-mode-${logIndex}-${index}`}>
                      {t("detail.topicMode", { index })}
                    </label>
                    <select
                      aria-label={t("detail.topicMode", { index })}
                      className="topic-mode-select"
                      id={`topic-mode-${logIndex}-${index}`}
                      onChange={(event) =>
                        onTopicModeChange(index, event.target.value as TopicDisplayMode)
                      }
                      value={mode}
                    >
                      <option value="hex">{t("detail.topicModes.hex")}</option>
                      <option value="address">{t("detail.topicModes.address")}</option>
                      <option value="text">{t("detail.topicModes.text")}</option>
                      <option value="number">{t("detail.topicModes.number")}</option>
                    </select>
                    <CopyableField value={value ?? topic}>
                      <code>{displayValue}</code>
                    </CopyableField>
                  </>
                )}
              </div>
            );
          })}
        </div>
      </section>
      <section className="transaction-log-data" aria-labelledby={`log-data-${logIndex}`}>
        <h3 id={`log-data-${logIndex}`}>{t("detail.data")}</h3>
        <div className="transaction-log-data-value">
          <CopyableField value={data}>
            <code>{data}</code>
          </CopyableField>
        </div>
      </section>
    </>
  );
}
