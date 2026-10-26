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
    <span className={`transaction-status ${status}`} data-status={status}>
      <UIIcon icon={Icon} className="transaction-status-icon" size={14} />
      <span>{label}</span>
    </span>
  );
}
