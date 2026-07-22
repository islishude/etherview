import { useTranslation } from "react-i18next";

import { ApiError } from "@/api/client";

interface QueryNoticeProps {
  loading?: boolean;
  error?: unknown;
  compact?: boolean;
  onReset?: () => void;
}

export function QueryNotice({ loading, error, compact, onReset }: QueryNoticeProps) {
  const { t } = useTranslation();
  if (loading) {
    return (
      <div className={compact ? "query-notice compact" : "query-notice"} role="status">
        <span className="pulse-dot" aria-hidden="true" />
        {t("state.loading")}
      </div>
    );
  }
  if (!error) return null;
  if (error instanceof ApiError && error.code === "stage_unavailable") {
    const details = stageDetails(error.details);
    return (
      <div className={compact ? "query-notice degraded compact" : "query-notice degraded"} role="status">
        <span className="status-dot warning" aria-hidden="true" />
        <span>
          <strong>
            {t("state.stageUnavailable", { stage: stageLabel(details.stage, t) })}
          </strong>
          {!compact && (
            <small>
              {t("state.stageUnavailableDetail", {
                state: capabilityStateLabel(details.state, t),
                block: details.blockNumber ? t("state.atBlock", { block: details.blockNumber }) : "",
              })}
            </small>
          )}
          {!compact && (
            <small className="notice-diagnostic">
              {t("state.diagnosticCode")}: <code>{error.code.toLowerCase()}</code>
            </small>
          )}
        </span>
      </div>
    );
  }
  if (error instanceof ApiError) {
    const code = error.code.toLowerCase();
    const details = capabilityDetails(error.details);
    const typed = code === "capability_unavailable"
      ? {
          title: t("state.capabilityUnavailable", {
            capability: capabilityLabel(details.capability, t),
          }),
          detail: t("state.capabilityUnavailableDetail", {
            state: capabilityStateLabel(details.state, t),
          }),
          diagnosticCode: details.code,
        }
      : code === "not_ready"
        ? { title: t("state.coreNotReady"), detail: t("state.coreNotReadyDetail") }
        : code === "invalid_cursor"
          ? { title: t("state.cursorInvalid"), detail: t("state.cursorInvalidDetail") }
          : code === "not_found"
            ? { title: t("state.notFound"), detail: t("state.notFoundDetail") }
            : code.startsWith("invalid_")
              ? { title: t("state.invalidRequest"), detail: t("state.invalidRequestDetail") }
              : undefined;
    if (typed) {
      return (
        <div
          className={compact ? "query-notice degraded compact" : "query-notice degraded"}
          role="status"
        >
          <span className="status-dot warning" aria-hidden="true" />
          <span>
            <strong>{typed.title}</strong>
            {!compact && <small>{typed.detail}</small>}
            {!compact && typed.diagnosticCode && (
              <small className="notice-diagnostic">
                {t("state.diagnosticCode")}: <code>{typed.diagnosticCode}</code>
              </small>
            )}
            {!compact && code === "invalid_cursor" && onReset && (
              <button className="notice-action" onClick={onReset} type="button">
                {t("actions.restartPagination")}
              </button>
            )}
          </span>
        </div>
      );
    }
  }
  return (
    <div className={compact ? "query-notice compact" : "query-notice"} role="status">
      <span className="status-dot muted" aria-hidden="true" />
      <span>
        <strong>{t("state.apiUnavailable")}</strong>
        {!compact && <small>{t("state.apiUnavailableDetail")}</small>}
      </span>
    </div>
  );
}

type Translate = ReturnType<typeof useTranslation>["t"];

function capabilityLabel(value: string, t: Translate): string {
  switch (value) {
    case "state": return t("capabilityName.state");
    case "name": return t("capabilityName.name");
    case "core": return t("capabilityName.core");
    case "trace": return t("capabilityName.trace");
    case "search": return t("capabilityName.search");
    default: return t("capabilityName.optional");
  }
}

function capabilityStateLabel(value: string, t: Translate): string {
  switch (value) {
    case "complete": return t("stageState.complete");
    case "pending": return t("stageState.pending");
    case "missing": return t("stageState.missing");
    case "unavailable": return t("stageState.unavailable");
    case "failed": return t("stageState.failed");
    default: return t("stageState.unavailable");
  }
}

function stageLabel(value: string, t: Translate): string {
  switch (value) {
    case "core": return t("stage.core");
    case "token": return t("stage.token");
    case "stats":
    case "statistics":
      return t("stage.stats");
    case "trace": return t("stage.trace");
    case "metadata": return t("stage.metadata");
    case "state": return t("stage.state");
    default: return t("stage.optional");
  }
}

function capabilityDetails(details: unknown): {
  capability: string;
  state: string;
  code: string;
} {
  if (typeof details !== "object" || details === null) {
    return { capability: "state", state: "unavailable", code: "not_available" };
  }
  const record = details as Record<string, unknown>;
  return {
    capability: typeof record.capability === "string" ? record.capability : "state",
    state: typeof record.state === "string" ? record.state : "unavailable",
    code: typeof record.code === "string" ? record.code : "not_available",
  };
}

function stageDetails(details: unknown): {
  stage: string;
  state: string;
  blockNumber?: string;
} {
  if (typeof details !== "object" || details === null) {
    return { stage: "optional", state: "unavailable" };
  }
  const record = details as Record<string, unknown>;
  return {
    stage: typeof record.stage === "string" ? record.stage : "optional",
    state: typeof record.state === "string" ? record.state : "unavailable",
    ...(typeof record.block_number === "string" ? { blockNumber: record.block_number } : {}),
  };
}
