import "@/i18n/admin";
import { useEffect, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { useAuth } from "@/auth/AuthProvider";
import { usePublicConfig } from "@/api/publicConfig";
import { usePrivateIdentity } from "@/api/privateAccount";
import { NavigationTabs, NavigationTab } from "./ui/tabs";
import { ApiError } from "@/api/client";

export function useAdminIdentity() {
  const auth = useAuth();
  const identity = usePrivateIdentity();
  return auth.enabled &&
    !auth.loading &&
    auth.session.user?.role === "admin" &&
    auth.session.user.status === "active"
    ? identity
    : "";
}

export function AdminSurface({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const auth = useAuth();
  const identity = useAdminIdentity();
  const { refresh } = auth;
  const client = useQueryClient();
  const config = usePublicConfig();
  useEffect(() => {
    if (
      identity &&
      auth.error &&
      [
        "authentication_required",
        "admin_required",
        "user_disabled",
        "AUTHENTICATION_REQUIRED",
      ].includes(auth.error)
    )
      void refresh();
  }, [identity, auth.error, refresh]);
  useEffect(() => {
    if (!identity) return;
    const key = ["private-account", identity, "admin"];
    const unsubscribe = client.getQueryCache().subscribe((event) => {
      if (event.type !== "updated" || event.query.queryKey.slice(0, 3).join(":") !== key.join(":"))
        return;
      const error = event.query.state.error;
      if (
        error instanceof ApiError &&
        ["authentication_required", "admin_required", "user_disabled", "session_invalid"].includes(
          error.code,
        )
      )
        void refresh();
    });
    return () => {
      unsubscribe();
      void client.cancelQueries({ queryKey: key });
      client.removeQueries({ queryKey: key });
    };
  }, [identity, client, refresh]);
  if (!identity)
    return (
      <section className="panel p-4" role="status">
        <p>
          {t(
            auth.loading
              ? "adminUI.checking"
              : !auth.enabled
                ? "adminUI.unavailable"
                : "adminUI.permission",
          )}
        </p>
        {!auth.loading && auth.enabled && <Link to="/account">{t("auth.account.open")}</Link>}
      </section>
    );
  return (
    <div key={identity} className="grid min-w-0 gap-4">
      <NavigationTabs aria-label={t("adminUI.title")}>
        <NavigationTab>
          <Link
            to="/account"
            search={{ tab: "admin", section: "overview" }}
            activeProps={{ "aria-current": "page" }}
          >
            {t("adminUI.overview")}
          </Link>
        </NavigationTab>
        <NavigationTab>
          <Link
            to="/account"
            search={{ tab: "admin", section: "users" }}
            activeProps={{ "aria-current": "page" }}
          >
            {t("nav.adminUsers")}
          </Link>
        </NavigationTab>
        {config.data?.features.api_billing === true && (
          <NavigationTab>
            <Link
              to="/account"
              search={{ tab: "admin", section: "billing" }}
              activeProps={{ "aria-current": "page" }}
            >
              {t("nav.adminBilling")}
            </Link>
          </NavigationTab>
        )}
        <NavigationTab>
          <Link
            to="/account"
            search={{ tab: "admin", section: "status" }}
            activeProps={{ "aria-current": "page" }}
          >
            {t("nav.status")}
          </Link>
        </NavigationTab>
      </NavigationTabs>
      {children}
    </div>
  );
}

export function AdminPanel({
  title,
  description,
  actions,
  children,
}: {
  title: string;
  description: string;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="grid min-w-0 gap-4">
      <header className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="m-0 text-lg font-semibold">{title}</h2>
          <p className="mt-1 text-sm text-muted-foreground">{description}</p>
        </div>
        {actions}
      </header>
      {children}
    </section>
  );
}
