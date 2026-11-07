import { Button } from "@/components/ui/button";
import { useRef, useState } from "react";
import { Link } from "@tanstack/react-router";
import { ShellIcon } from "./DesignPrimitives";
import { useTranslation } from "react-i18next";
import { usePublicConfig } from "@/api/publicConfig";
import { NavigationDisclosure } from "./NavigationDisclosure";

export function AppNavigation() {
  const { t } = useTranslation();
  const config = usePublicConfig();
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
        <Button
          variant="ghost"
          className="control min-[1024px]:hidden"
          type="button"
          aria-expanded={expanded}
          aria-controls="explorer-navigation-links"
          ref={trigger}
          onClick={() => setExpanded(!expanded)}
        >
          <ShellIcon name={expanded ? "close" : "menu"} />
          {t("nav.explore")}
        </Button>
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
          <NavigationDisclosure label={t("nav.more")}>
            <Link activeProps={{ className: "active" }} to="/charts">
              {t("nav.charts")}
            </Link>

            <Link activeProps={{ className: "active" }} to="/verify">
              {t("page.verify")}
            </Link>
            {config.data?.features.user_operations === true && (
              <Link activeProps={{ className: "active" }} to="/user-operations">
                {t("nav.userOperations")}
              </Link>
            )}
            <Link activeProps={{ className: "active" }} to="/pending">
              {t("nav.pending")}
            </Link>
          </NavigationDisclosure>
        </div>
      </nav>
    </div>
  );
}
