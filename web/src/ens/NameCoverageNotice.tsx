import { usePublicConfig } from "@/api/publicConfig";
import i18n from "@/i18n";
import { useTranslation } from "react-i18next";
import { useChainStatus } from "@/api/chainStatus";

// Load this feature's translations with its optional presentation component.
i18n.addResourceBundle("en", "names", {
  coveragePending: "Name history syncing; verified names available.",
});
i18n.addResourceBundle("zh", "names", { coveragePending: "名称历史同步中，已验证名称可用。" });

export default function NameCoverageNotice() {
  const { t } = useTranslation("names");
  const enabled = usePublicConfig().data?.features.ens === true;
  const status = useChainStatus(enabled);
  const source = status.data?.names?.[0];
  if (!enabled || !source || source.coverage === "complete") return null;
  return (
    <p className="context-note" role="status">
      {t("coveragePending")}
    </p>
  );
}
