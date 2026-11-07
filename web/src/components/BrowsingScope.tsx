import { Alert } from "@/components/ui/alert";
import { createContext, useContext, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { ApiMeta } from "@/api/types";

type Status = ApiMeta["browsing"];
const BrowsingContext = createContext<Status>(undefined);

export function BrowsingScope({ status, children }: { status?: Status; children: ReactNode }) {
  const { t } = useTranslation();
  return (
    <BrowsingContext.Provider value={status}>
      {status?.state === "partial" && (
        <Alert asChild>
          <p className="context-note" role="status">
            {t("state.browsingPartial")}
          </p>
        </Alert>
      )}
      {children}
    </BrowsingContext.Provider>
  );
}

export function BrowsingEmpty({ children }: { children: ReactNode }) {
  const status = useContext(BrowsingContext);
  const { t } = useTranslation();
  return <>{status && status.state !== "complete" ? t("state.browsingPending") : children}</>;
}
