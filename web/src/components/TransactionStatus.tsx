import { useTranslation } from "react-i18next";
import type { TransactionSummary } from "@/api/types";
import { Badge } from "@/components/ui/badge";
import { UIIcon } from "@/components/UIIcon";
import {
  ArrowRightLeft,
  CircleCheck,
  CircleHelp,
  CircleX,
  Clock3,
  GitBranch,
  type LucideIcon,
} from "lucide-react";

export type TransactionVisualStatus =
  | "success"
  | "failed"
  | "pending"
  | "replaced"
  | "orphan"
  | "unknown";

const statusIcons: Record<TransactionVisualStatus, LucideIcon> = {
  success: CircleCheck,
  failed: CircleX,
  pending: Clock3,
  replaced: ArrowRightLeft,
  orphan: GitBranch,
  unknown: CircleHelp,
};

export function TransactionStatus({
  label,
  status,
}: {
  label: string;
  status: TransactionVisualStatus;
}) {
  const Icon = statusIcons[status];
  return (
    <Badge variant="status" className={`transaction-status ${status}`} data-status={status}>
      <UIIcon icon={Icon} className="transaction-status-icon" size={14} />
      <span>{label}</span>
    </Badge>
  );
}

export function TransactionStatusBadge({
  transaction,
  showFinality = true,
}: {
  transaction: TransactionSummary;
  showFinality?: boolean;
}) {
  const { t } = useTranslation();
  const state = !transaction.canonical
    ? "orphan"
    : transaction.status === "success"
      ? "success"
      : transaction.status === "failed"
        ? "failed"
        : "unknown";
  return (
    <Badge variant="status" className="transaction-status-group">
      <TransactionStatus
        label={
          state === "orphan" ? t("detail.orphaned") : transactionStatusLabel(transaction.status, t)
        }
        status={state}
      />
      {showFinality && transaction.canonical && transaction.finality === "finalized" ? (
        <Badge variant="status" className="finality-badge finalized">
          {finalityLabel(transaction.finality, t)}
        </Badge>
      ) : null}
    </Badge>
  );
}

export function IncludedTransactionStatus({ transaction }: { transaction: TransactionSummary }) {
  const { t } = useTranslation();
  const state: TransactionVisualStatus = !transaction.canonical
    ? "orphan"
    : transaction.status === "success"
      ? "success"
      : transaction.status === "failed"
        ? "failed"
        : transaction.status === "pending"
          ? "pending"
          : "unknown";
  return (
    <TransactionStatus
      label={
        state === "orphan" ? t("detail.orphaned") : transactionStatusLabel(transaction.status, t)
      }
      status={state}
    />
  );
}

type Translate = ReturnType<typeof useTranslation>["t"];
export function finalityLabel(value: string, t: Translate): string {
  switch (value) {
    case "pending":
      return t("finality.pending");
    case "latest":
      return t("finality.latest");
    case "safe":
      return t("finality.safe");
    case "finalized":
      return t("finality.finalized");
    case "orphan":
      return t("finality.orphan");
    default:
      return value;
  }
}

export function transactionStatusLabel(value: string | undefined, t: Translate): string {
  switch (value) {
    case "pending":
      return t("transactionStatus.pending");
    case "success":
      return t("transactionStatus.success");
    case "failed":
      return t("transactionStatus.failed");
    case "unknown":
      return t("transactionStatus.unknown");
    default:
      return t("common.indexed");
  }
}
