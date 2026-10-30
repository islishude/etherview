import { CircleAlert, CircleHelp, LoaderCircle } from "lucide-react";
import { UIIcon } from "@/components/UIIcon";
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
        <UIIcon icon={LoaderCircle} size={20} className="query-loading-spinner" />
        <span>{t("state.loading")}</span>
      </div>
    );
  }
  if (!error) return null;
  if (
    error instanceof ApiError &&
    [
      "stage_unavailable",
      "token_holders_unavailable",
      "analytics_pending",
      "capability_unavailable",
    ].includes(error.code.toLowerCase())
  ) {
    if (detailString(error.details, "capability") === "name") {
      return (
        <p className="context-note" role="status">
          {t("state.namePending")}
        </p>
      );
    }
    const state =
      detailString(error.details, "state") ??
      (error.code.toLowerCase() === "capability_unavailable" ? "unavailable" : "pending");
    return (
      <p className="context-note" role="status">
        {t(
          state === "unavailable" || state === "failed"
            ? "state.browsingUnavailable"
            : "state.browsingPending",
        )}
      </p>
    );
  }
  if (error instanceof ApiError) {
    const code = error.code.toLowerCase();
    const typed =
      code === "not_ready"
        ? { title: t("state.coreNotReady"), detail: t("state.coreNotReadyDetail") }
        : code === "mempool_unavailable"
          ? {
              title: t("pending.unavailable"),
              detail: t("pending.unavailableDetail"),
              diagnosticCode: detailString(error.details, "reason"),
            }
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
          <UIIcon icon={CircleAlert} size={14} className="status-icon warning" />
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
      <UIIcon icon={CircleHelp} size={14} className="status-icon muted" />
      <span>
        <strong>{t("state.apiUnavailable")}</strong>
        {!compact && <small>{t("state.apiUnavailableDetail")}</small>}
      </span>
    </div>
  );
}

function detailString(details: unknown, key: string): string | undefined {
  if (typeof details !== "object" || details === null) return undefined;
  const value = (details as Record<string, unknown>)[key];
  return typeof value === "string" ? value : undefined;
}
