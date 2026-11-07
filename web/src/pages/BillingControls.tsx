import { useTranslation } from "react-i18next";
import { Pagination } from "@/components/ui/pagination";
import { Button } from "@/components/ui/button";
import { Alert } from "@/components/ui/alert";

export function BillingPagination({
  itemCount,
  busy,
  cursors,
  label,
  nextCursor,
  onChange,
}: {
  itemCount: number;
  busy: boolean;
  cursors: string[];
  label: string;
  nextCursor?: string;
  onChange: (value: string[] | ((current: string[]) => string[])) => void;
}) {
  const { t } = useTranslation();
  return (
    <Pagination
      itemCount={itemCount}
      onReset={() => onChange([""])}
      hasPrevious={cursors.length > 1}
      hasNext={!!nextCursor}
      busy={busy}
      className="cursor-pagination"
      aria-busy={busy}
      aria-label={label}
    >
      {cursors.length > 1 && (
        <Button
          variant="outline"
          className="button secondary"
          disabled={busy || cursors.length === 1}
          onClick={() =>
            onChange((current) => (current.length > 1 ? current.slice(0, -1) : current))
          }
          type="button"
        >
          {t("pagination.previous")}
        </Button>
      )}
      <span aria-live="polite">{t("pagination.page", { page: cursors.length })}</span>
      {nextCursor && (
        <Button
          variant="outline"
          className="button secondary"
          disabled={busy || !nextCursor}
          onClick={() => {
            if (nextCursor) onChange((current) => [...current, nextCursor]);
          }}
          type="button"
        >
          {t("pagination.next")}
        </Button>
      )}
    </Pagination>
  );
}

export function BillingLoadError({
  detail,
  onRetry,
  title,
}: {
  detail: string;
  onRetry: () => void;
  title: string;
}) {
  const { t } = useTranslation();
  return (
    <Alert asChild>
      <div className="query-notice degraded" role="alert">
        <span>
          <strong>{title}</strong>
          <small>{detail}</small>
        </span>
        <Button variant="outline" className="button secondary" onClick={onRetry} type="button">
          {t("actions.retry")}
        </Button>
      </div>
    </Alert>
  );
}
