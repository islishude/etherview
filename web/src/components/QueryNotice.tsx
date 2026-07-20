import { useTranslation } from "react-i18next";

import { ApiError } from "@/api/client";

interface QueryNoticeProps {
  loading?: boolean;
  error?: unknown;
  compact?: boolean;
}

export function QueryNotice({ loading, error, compact }: QueryNoticeProps) {
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
          <strong>{t("state.stageUnavailable", { stage: details.stage })}</strong>
          {!compact && (
            <small>
              {t("state.stageUnavailableDetail", {
                state: details.state,
                block: details.blockNumber ? t("state.atBlock", { block: details.blockNumber }) : "",
              })}
            </small>
          )}
        </span>
      </div>
    );
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
