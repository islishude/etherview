import type { BrowserVerificationJob } from "@/api/types";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Download, Upload } from "lucide-react";
import { SourcifyImportForm } from "./SourcifyImportForm";
import { SourcifyVerificationForm } from "./SourcifyVerificationForm";

export function SourcifyWorkspace({
  active = true,
  onSubmitted,
  initialAddress,
  chainID,
  onAddressChange,
}: {
  active?: boolean;
  onSubmitted: (job: BrowserVerificationJob) => void;
  initialAddress: string;
  chainID: string;
  onAddressChange: (address: string) => void;
}) {
  const { t } = useTranslation();
  const [mode, setMode] = useState("import");
  return (
    <div className="verification-workspace">
      <div className="verification-actions" role="group" aria-label={t("sourcify.workflow")}>
        <button type="button" aria-pressed={mode === "import"} onClick={() => setMode("import")}>
          <Download aria-hidden="true" />
          {t("sourcify.importTab")}
        </button>
        <button type="button" aria-pressed={mode === "publish"} onClick={() => setMode("publish")}>
          <Upload aria-hidden="true" />
          {t("sourcify.publishTab")}
        </button>
      </div>
      {mode === "import" ? (
        <SourcifyImportForm
          active={active}
          onSubmitted={onSubmitted}
          initialAddress={initialAddress}
          onAddressChange={onAddressChange}
          chainID={chainID}
          onPublish={() => setMode("publish")}
        />
      ) : (
        <SourcifyVerificationForm
          active={active}
          onSubmitted={onSubmitted}
          initialAddress={initialAddress}
          onAddressChange={onAddressChange}
          chainID={chainID}
        />
      )}
    </div>
  );
}
