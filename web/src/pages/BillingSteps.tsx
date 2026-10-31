import { Check, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { UIIcon } from "@/components/UIIcon";

export function BillingSteps({
  active,
  complete,
  failed,
}: {
  active: number;
  complete: boolean;
  failed: boolean;
}) {
  const { t } = useTranslation();
  return (
    <ol className="billing-steps" aria-label={t("billing.guide.steps")}>
      {(["enter", "review", "result"] as const).map((step, index) => {
        const status =
          complete || index < active
            ? "success"
            : index === active
              ? failed
                ? "error"
                : "process"
              : "wait";
        return (
          <li
            key={step}
            className={`billing-step ${status}`}
            aria-current={!complete && index === active ? "step" : undefined}
          >
            <span className="billing-step-head" aria-hidden="true">
              <span className="billing-step-icon">
                {status === "success" ? (
                  <UIIcon icon={Check} size={16} />
                ) : status === "error" ? (
                  <UIIcon icon={X} size={16} />
                ) : (
                  index + 1
                )}
              </span>
            </span>
            <strong>{t(`billing.guide.${step}`)}</strong>
            <span className="billing-step-description">
              {t(`billing.guide.${step}Description`)}
            </span>
            <span className="sr-only">{t(`billing.guide.${status}`)}</span>
          </li>
        );
      })}
    </ol>
  );
}
