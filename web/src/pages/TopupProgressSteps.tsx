import { Check } from "lucide-react";
import { UIIcon } from "@/components/UIIcon";
import { useTranslation } from "react-i18next";
import { CopyableField } from "@/components/CopyButton";
import type { TopupProgress } from "@/billing/progress";

export function TopupProgressSteps({
  progress,
  approvalRequired,
  state,
  transactionHash,
}: {
  progress: TopupProgress;
  approvalRequired: boolean;
  state: "processing" | "pending" | "success" | "failure";
  transactionHash?: string;
}) {
  const { t } = useTranslation();
  const steps = [
    ...(approvalRequired ? ["approval", "approvalConfirming"] : []),
    "signing",
    "submitting",
    "confirming",
    "success",
  ];
  const active =
    state === "success"
      ? steps.length
      : steps.indexOf(progress.stage === "unknown" ? "submitting" : progress.stage);
  return (
    <>
      <ol className="billing-topup-steps" aria-label={t("billing.topup.progress.label")}>
        {steps.map((step, index) => (
          <li
            key={step}
            className={index < active ? "complete" : index === active ? "current" : "upcoming"}
            aria-current={index === active ? "step" : undefined}
          >
            <span aria-hidden="true">{index < active ? <UIIcon icon={Check} /> : index + 1}</span>
            {t(`billing.topup.progress.${step}`)}
            {index < active && (
              <span className="sr-only">{t("billing.topup.progress.completed")}</span>
            )}
          </li>
        ))}
      </ol>
      {progress.approvalHash && (
        <div className="billing-progress-hash">
          <span>{t("billing.topup.progress.approvalHash")}</span>
          <CopyableField value={progress.approvalHash}>
            <code>{progress.approvalHash}</code>
          </CopyableField>
        </div>
      )}
      {transactionHash && (
        <div className="billing-progress-hash">
          <span>{t("billing.topup.progress.paymentHash")}</span>
          <CopyableField value={transactionHash}>
            <code>{transactionHash}</code>
          </CopyableField>
        </div>
      )}
    </>
  );
}
