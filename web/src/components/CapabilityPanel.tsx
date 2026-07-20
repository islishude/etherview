import { useTranslation } from "react-i18next";

import { Surface } from "./DesignPrimitives";

export function CapabilityPanel({ detail }: { detail?: string }) {
  const { t } = useTranslation();
  return (
    <Surface
      className="capability-panel min-h-[180px] flex items-center gap-[1.2rem] border-dashed border-ui-line-strong p-[clamp(1.5rem,4vw,2.5rem)] max-[700px]:items-start"
      aria-labelledby="capability-title"
    >
      <span className="capability-mark" aria-hidden="true">
        ···
      </span>
      <div>
        <h2 id="capability-title">{t("capability.foundation")}</h2>
        <p>{detail ?? t("capability.pendingApi")}</p>
      </div>
    </Surface>
  );
}
