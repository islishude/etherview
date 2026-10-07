import { useRef, useState } from "react";
import { Link } from "@tanstack/react-router";
import { ShellIcon } from "./DesignPrimitives";
import { useTranslation } from "react-i18next";
import { usePublicConfig } from "@/api/hooks";
import { useAuth } from "@/auth/AuthProvider";
import { NavigationDisclosure } from "./NavigationDisclosure";
import { NotificationBadge } from "./NotificationBadge";

export function AppNavigation() {
  const { t } = useTranslation();
  const config = usePublicConfig();
  const auth = useAuth();
  const [expanded, setExpanded] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  return (
    <div className="navigation-bar shell-width">
      <nav
        className="site-nav"
        aria-label={t("nav.primary")}
        onKeyDown={(event) => {
          if (event.key === "Escape" && expanded) {
            setExpanded(false);
            trigger.current?.focus();
          }
        }}
      >
        <button
          className="control mobile-navigation-trigger"
          type="button"
          aria-expanded={expanded}
          aria-controls="explorer-navigation-links"
          ref={trigger}
          onClick={() => setExpanded(!expanded)}
        >
          <ShellIcon name={expanded ? "close" : "menu"} />
          {t("nav.explore")}
        </button>
        <div
          className="navigation-links"
          id="explorer-navigation-links"
          data-expanded={expanded}
          onClick={(event) => {
            if (event.target instanceof Element && event.target.closest("a") && expanded) {
              setExpanded(false);
              trigger.current?.focus();
            }
          }}
        >
          <Link activeProps={{ className: "active" }} activeOptions={{ exact: true }} to="/">
            {t("nav.home")}
          </Link>
          <Link activeProps={{ className: "active" }} to="/blocks">
            {t("nav.blocks")}
          </Link>
          <Link activeProps={{ className: "active" }} to="/transactions">
            {t("nav.transactions")}
          </Link>
          <Link activeProps={{ className: "active" }} to="/tokens">
            {t("nav.tokens")}
          </Link>
          <Link activeProps={{ className: "active" }} to="/charts">
            {t("nav.charts")}
          </Link>
          <NavigationDisclosure label={t("nav.more")}>
            {config.data?.features.user_operations === true && (
              <Link activeProps={{ className: "active" }} to="/user-operations">
                {t("nav.userOperations")}
              </Link>
            )}
            <Link activeProps={{ className: "active" }} to="/pending">
              {t("nav.pending")}
            </Link>
            <Link activeProps={{ className: "active" }} to="/status">
              {t("nav.status")}
            </Link>
          </NavigationDisclosure>
        </div>
      </nav>
      <div className="account-navigation">
        <NotificationBadge />
        {auth.enabled && (
          <NavigationDisclosure label={t("nav.account")}>
            <Link to="/account">{t("nav.accountOverview")}</Link>
            {auth.session.authenticated && auth.session.user?.role === "admin" && (
              <>
                <Link to="/admin/users">{t("nav.adminUsers")}</Link>
                {config.data?.features.api_billing === true && (
                  <Link to="/admin/billing">{t("nav.adminBilling")}</Link>
                )}
              </>
            )}
          </NavigationDisclosure>
        )}
      </div>
    </div>
  );
}
