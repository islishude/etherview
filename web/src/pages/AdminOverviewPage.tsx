import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { RotateCw } from "lucide-react";
import { AdminSurface, AdminPanel, useAdminIdentity } from "@/components/AdminSurface";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { UIIcon } from "@/components/UIIcon";
import { getAdminOverview } from "@/api/auth";
import { getAdminBillingSummary } from "@/api/billing";
import { usePublicConfig } from "@/api/publicConfig";
import { BillingSummaryView } from "./BillingPages";
import { BillingLoadError } from "./BillingControls";

export function AdminOverviewPage() {
  return (
    <AdminSurface>
      <AdminOverviewContent />
    </AdminSurface>
  );
}

function AdminOverviewContent() {
  const { t, i18n } = useTranslation();
  const identity = useAdminIdentity();
  const config = usePublicConfig();
  const billingEnabled = config.data?.features.api_billing === true;
  const users = useQuery({
    queryKey: ["private-account", identity, "admin", "overview"],
    queryFn: ({ signal }) => getAdminOverview(signal),
    enabled: Boolean(identity),
    retry: false,
    staleTime: 0,
  });
  const summary = useQuery({
    queryKey: ["private-account", identity, "admin", "billing-summary", {}],
    queryFn: ({ signal }) => getAdminBillingSummary({}, signal),
    enabled: Boolean(identity) && billingEnabled,
    retry: false,
    staleTime: 0,
  });
  const busy = users.isFetching || (billingEnabled && summary.isFetching);
  return (
    <AdminPanel
      title={t("adminUI.overview")}
      description={t("adminUI.overviewDescription")}
      actions={
        <Button
          variant="ghost"
          size="icon"
          type="button"
          aria-label={t("adminUI.refresh")}
          title={t("adminUI.refresh")}
          aria-busy={busy}
          disabled={busy}
          onClick={() => {
            void users.refetch();
            if (billingEnabled) void summary.refetch();
          }}
        >
          <UIIcon icon={RotateCw} />
        </Button>
      }
    >
      <Card className="p-4">
        <h3 className="m-0 text-sm text-muted-foreground">{t("adminUI.userCount")}</h3>
        {users.isPending && <p role="status">{t("auth.admin.loading")}</p>}
        {users.error && (
          <BillingLoadError
            title={t("adminUI.overviewFailed")}
            detail={t("auth.admin.loadFailedDetail")}
            onRetry={() => void users.refetch()}
          />
        )}
        {users.data && (
          <p className="my-2 text-lg font-semibold tabular-nums">{users.data.user_count}</p>
        )}
      </Card>
      {billingEnabled && (
        <Card className="min-w-0 p-4">
          <h3 className="m-0 text-base font-semibold">{t("billing.admin.summaryTitle")}</h3>
          {summary.isPending && <p role="status">{t("billing.admin.summaryLoading")}</p>}
          {summary.error && (
            <BillingLoadError
              title={t("billing.admin.summaryFailed")}
              detail={t("billing.admin.summaryFailedDetail")}
              onRetry={() => void summary.refetch()}
            />
          )}
          {summary.data && (
            <BillingSummaryView locale={i18n.resolvedLanguage ?? "en"} summary={summary.data} />
          )}
        </Card>
      )}
    </AdminPanel>
  );
}
