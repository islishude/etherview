import type { Hex } from "viem";
import type { BillingTopupIntent } from "@/api/billing";

export type TopupStage =
  | "preparing"
  | "approval"
  | "approvalConfirming"
  | "signing"
  | "submitting"
  | "confirming"
  | "unknown";
export interface TopupProgress {
  stage: TopupStage;
  approvalHash?: Hex;
}

// Terminal writer observations and newer responses must survive stale fetches.
export function mergeTopupObservation(
  previous: BillingTopupIntent | undefined,
  next: BillingTopupIntent,
): BillingTopupIntent {
  if (!previous || previous.id !== next.id) return next;
  if (["credited", "failed", "expired"].includes(previous.state)) return previous;
  if (["credited", "failed", "expired"].includes(next.state)) return next;
  if (Date.parse(previous.updated_at) > Date.parse(next.updated_at)) return previous;
  if (previous.submission_progress === "confirming" && next.submission_progress === "submitting")
    return previous;
  return { ...next, transaction_hash: next.transaction_hash ?? previous.transaction_hash };
}
