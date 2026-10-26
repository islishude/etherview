import { UIIcon } from "@/components/UIIcon";
import { Check, ShieldCheck, CloudDownload } from "lucide-react";
import { useTranslation } from "react-i18next";

export function VerificationProviderPicker({
  value,
  onChange,
}: {
  value: string;
  onChange: (value: string) => void;
}) {
  const { t } = useTranslation();
  return (
    <fieldset className="verification-provider-picker">
      <legend>{t("verification.provider")}</legend>
      <div className="verification-provider-options">
        {[
          {
            id: "local",
            title: "verification.localProvider",
            detail: "sourcify.localChoice",
            Icon: ShieldCheck,
          },
          {
            id: "sourcify",
            title: "sourcify.sourcifyChoice",
            detail: "sourcify.sourcifyChoiceHelp",
            Icon: CloudDownload,
          },
        ].map(({ id, title, detail, Icon }) => (
          <label className="verification-provider-option" key={id}>
            <input
              type="radio"
              name="verification-provider"
              value={id}
              checked={value === id}
              onChange={() => onChange(id)}
            />
            <span className="verification-provider-icon">
              <UIIcon icon={Icon} size={24} />
            </span>
            <span className="verification-provider-copy">
              <strong>{t(title)}</strong>
              <span>{t(detail)}</span>
            </span>
            <span className="verification-provider-check" aria-hidden="true">
              {value === id && <UIIcon icon={Check} size={14} />}
            </span>
          </label>
        ))}
      </div>
    </fieldset>
  );
}
