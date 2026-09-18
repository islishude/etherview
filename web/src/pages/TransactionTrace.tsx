import { useTransactionTrace } from "@/api/hooks";
import { formatNativeAmount, shorten } from "@/components/format";
import { QueryNotice } from "@/components/QueryNotice";
import {
  CapabilityDegraded,
  formatLogArgument,
  traceDecodingKey,
  traceOutputStatusKey,
  type Translate,
} from "./pages";

function TraceABIValues({
  title,
  values,
}: {
  title: string;
  values: Array<{ name: string; type: string; value: unknown }>;
}) {
  return (
    <section className="transaction-trace-values">
      <h4>{title}</h4>
      <dl>
        {values.map((value, index) => (
          <div key={`${value.name}:${value.type}:${index}`}>
            <dt>
              {value.name || `${index}`} <code>{value.type}</code>
            </dt>
            <dd>
              <code>{formatLogArgument(value.value)}</code>
            </dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

export function TransactionTracePanel({
  trace,
  traceIdentityCurrent,
  locale,
  nativeDecimals,
  nativeSymbol,
  t,
}: {
  trace: ReturnType<typeof useTransactionTrace>;
  traceIdentityCurrent: boolean;
  locale: string;
  nativeDecimals: number;
  nativeSymbol: string;
  t: Translate;
}) {
  return (
    <section className="panel transaction-tab-panel" role="tabpanel">
      <QueryNotice loading={trace.isPending} error={trace.error} />
      {trace.data && !traceIdentityCurrent && (
        <p className="capability-panel">{t("state.transactionIdentityChanged")}</p>
      )}
      {traceIdentityCurrent && trace.data && trace.data.state !== "complete" && (
        <CapabilityDegraded stage="trace" state={trace.data.state} />
      )}
      {traceIdentityCurrent &&
        trace.data?.state === "complete" &&
        trace.data.frames.length === 0 && (
          <p className="empty-result">{t("state.noTraceFrames")}</p>
        )}
      {traceIdentityCurrent && trace.data?.state === "complete" && trace.data.frames.length > 0 && (
        <div className="transaction-trace-list">
          {trace.data.frames.map((frame) => (
            <article
              className={
                frame.reverted ? "transaction-trace-frame reverted" : "transaction-trace-frame"
              }
              key={frame.path.join(".") || "root"}
              style={{ "--trace-depth": frame.depth } as React.CSSProperties}
            >
              <div className="transaction-trace-summary">
                <span className="transaction-trace-kind">{frame.call_type}</span>
                <div>
                  <strong>
                    <code>
                      {frame.decoding?.signature ??
                        t(traceDecodingKey(frame.decoding?.status ?? "unavailable"))}
                    </code>
                  </strong>
                  <code>
                    {frame.from ? shorten(frame.from) : "—"} →{" "}
                    {frame.to
                      ? shorten(frame.to)
                      : frame.created_address
                        ? shorten(frame.created_address)
                        : "—"}
                  </code>
                  <small className="table-secondary">
                    {t("detail.executionContext", { address: frame.execution.context_address })}
                    {frame.execution.address
                      ? ` · ${t("detail.executionCode", { address: frame.execution.address })}`
                      : ` · ${t(`detail.executionResolution.${frame.execution.resolution}`)}`}
                  </small>
                </div>
                <span>
                  {formatNativeAmount(frame.value, locale, nativeDecimals)} {nativeSymbol}
                </span>
                <span>
                  {frame.direct_reverted
                    ? (frame.error ?? t("detail.directReverted"))
                    : frame.reverted
                      ? t("detail.ancestorReverted")
                      : t("detail.succeeded")}
                </span>
              </div>
              <details className="transaction-more-details transaction-trace-details">
                <summary>{t("detail.traceDetails")}</summary>
                {frame.decoding && (
                  <>
                    <p className="quiet">
                      {t(traceDecodingKey(frame.decoding.status))}
                      {frame.decoding.abi_source?.address
                        ? ` · ${t("detail.abiSource", { kind: frame.decoding.abi_source.kind, address: frame.decoding.abi_source.address })}`
                        : ""}
                    </p>
                    <dl className="transaction-trace-decode-status">
                      <div>
                        <dt>{t("detail.callInputs")}</dt>
                        <dd>{t(traceDecodingKey(frame.decoding.status))}</dd>
                      </div>
                      <div>
                        <dt>{t("detail.callOutputs")}</dt>
                        <dd>{t(traceOutputStatusKey(frame.decoding.output_status))}</dd>
                      </div>
                      {frame.decoding.revert && (
                        <div>
                          <dt>{t("detail.revertData")}</dt>
                          <dd>{t(traceDecodingKey(frame.decoding.revert.status))}</dd>
                        </div>
                      )}
                    </dl>
                    {frame.decoding.inputs.length > 0 && (
                      <TraceABIValues
                        title={t("detail.callInputs")}
                        values={frame.decoding.inputs}
                      />
                    )}
                    {frame.decoding.outputs.length > 0 && (
                      <TraceABIValues
                        title={t("detail.callOutputs")}
                        values={frame.decoding.outputs}
                      />
                    )}
                    {frame.decoding.revert && frame.decoding.revert.arguments.length > 0 && (
                      <TraceABIValues
                        title={frame.decoding.revert.signature ?? t("detail.revertData")}
                        values={frame.decoding.revert.arguments}
                      />
                    )}
                    {(frame.decoding.warning || frame.decoding.revert?.warning) && (
                      <p className="quiet">
                        {frame.decoding.warning ?? frame.decoding.revert?.warning}
                      </p>
                    )}
                  </>
                )}
                <dl className="transaction-trace-raw">
                  <div>
                    <dt>{t("detail.executionContextLabel")}</dt>
                    <dd>
                      <code>{frame.execution.context_address}</code>
                    </dd>
                  </div>
                  <div>
                    <dt>{t("detail.executionCodeLabel")}</dt>
                    <dd>
                      <code>{frame.execution.address ?? "—"}</code>
                    </dd>
                  </div>
                  <div>
                    <dt>{t("detail.codeHash")}</dt>
                    <dd>
                      <code>{frame.execution.code_hash ?? "—"}</code>
                    </dd>
                  </div>
                  <div>
                    <dt>{t("detail.input")}</dt>
                    <dd>
                      <code>{frame.input ?? "0x"}</code>
                    </dd>
                  </div>
                  <div>
                    <dt>{t("detail.output")}</dt>
                    <dd>
                      <code>{frame.output ?? "0x"}</code>
                    </dd>
                  </div>
                </dl>
              </details>
            </article>
          ))}
        </div>
      )}
    </section>
  );
}
