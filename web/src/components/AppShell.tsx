import { FormEvent, useState } from "react";
import { Link, Outlet, useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { usePublicConfig } from "@/api/hooks";
import { useTheme } from "@/theme/ThemeProvider";
import { AppFrame } from "./DesignPrimitives";
import { WalletMenu } from "./WalletMenu";

export function AppShell() {
  const { i18n, t } = useTranslation();
  const { theme, toggleTheme } = useTheme();
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const publicConfig = usePublicConfig();

  const submitSearch = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const normalized = query.trim();
    if (normalized) void navigate({ to: "/search", search: { q: normalized } });
  };

  const toggleLanguage = () => {
    const next = i18n.resolvedLanguage?.startsWith("zh") ? "en" : "zh";
    void i18n.changeLanguage(next);
  };

  return (
    <AppFrame className="app-frame">
      <a className="skip-link" href="#main-content">
        {t("skip")}
      </a>
      <header className="site-header">
        <div className="header-primary shell-width">
          <Link className="brand" to="/" aria-label="Etherview home">
            <span className="brand-mark" aria-hidden="true">
              E
            </span>
            <span>
              <strong>Etherview</strong>
              <small>{publicConfig.data?.chain_name ?? t("app.tagline")}</small>
            </span>
          </Link>

          <form className="global-search" role="search" onSubmit={submitSearch}>
            <label className="sr-only" htmlFor="global-search-input">
              {t("actions.search")}
            </label>
            <input
              id="global-search-input"
              onChange={(event) => setQuery(event.target.value)}
              placeholder={t("actions.searchPlaceholder")}
              type="search"
              value={query}
            />
            <button type="submit">{t("actions.search")}</button>
          </form>

          <div className="header-controls">
            <button
              aria-label={t("actions.toggleTheme")}
              aria-pressed={theme === "dark"}
              className="control icon-control"
              onClick={toggleTheme}
              type="button"
            >
              <span aria-hidden="true">{theme === "dark" ? "☾" : "☼"}</span>
            </button>
            <button className="control language-control" onClick={toggleLanguage} type="button">
              {t("actions.toggleLanguage")}
            </button>
            <WalletMenu />
          </div>
        </div>

        <nav className="site-nav shell-width" aria-label={t("nav.primary")}>
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
          <Link activeProps={{ className: "active" }} to="/contracts">
            {t("nav.contracts")}
          </Link>
          <Link activeProps={{ className: "active" }} to="/charts">
            {t("nav.charts")}
          </Link>
          <Link activeProps={{ className: "active" }} to="/pending">
            {t("nav.pending")}
          </Link>
          <Link activeProps={{ className: "active" }} to="/status">
            {t("nav.status")}
          </Link>
        </nav>
      </header>

      <main id="main-content" className="shell-width site-main" tabIndex={-1}>
        <Outlet />
      </main>

      <footer className="site-footer">
        <div className="shell-width footer-inner">
          <span>Etherview</span>
          <span>{t("footer.description")}</span>
        </div>
      </footer>
    </AppFrame>
  );
}
