import type { ChainStatus } from "@/api/types";
import i18n from "@/i18n";
import { useTranslation } from "react-i18next";

// Load this feature's translations with its optional presentation component.
i18n.addResourceBundle("en", "names", {
  coveragePending: "Name history syncing; verified names available.",
});
i18n.addResourceBundle("zh", "names", { coveragePending: "名称历史同步中，已验证名称可用。" });

export default function NameCoverageNotice({
  enabled,
  names,
}: {
  enabled: boolean;
  names: ChainStatus["names"];
}) {
  const { t } = useTranslation("names");
  const source = names?.[0];
  if (!enabled || !source || source.coverage === "complete") return null;
  return (
    <p className="context-note" role="status">
      {t("coveragePending")}
    </p>
  );
}
