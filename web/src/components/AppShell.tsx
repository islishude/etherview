import { AppNavigation } from "./AppNavigation";
import { FormEvent, useState } from "react";
import { Link, Outlet, useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { normalize as normalizeENSName } from "viem/ens";

import { usePublicConfig } from "@/api/hooks";
import etherviewMark from "@/assets/etherview-mark.svg";
import { useTheme } from "@/theme/ThemeProvider";
import { AppFrame, ShellIcon } from "./DesignPrimitives";
import { WalletMenu } from "./WalletMenu";
import { AddressNamesProvider } from "@/ens/AddressNamesProvider";

export function AppShell() {
  const { i18n, t } = useTranslation();
  const { theme, toggleTheme } = useTheme();
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const [searchError, setSearchError] = useState("");
  const publicConfig = usePublicConfig();

  const submitSearch = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    let normalized = query.trim();
    if (!normalized) return;
    if (normalized.includes(".")) {
      try {
        normalized = normalizeENSName(normalized);
      } catch {
        setSearchError(t("actions.invalidENSName"));
        return;
      }
    }
    setSearchError("");
    setQuery(normalized);
    void navigate({ to: "/search", search: { q: normalized } });
  };

  const toggleLanguage = () => {
    const next = i18n.resolvedLanguage?.startsWith("zh") ? "en" : "zh";
    void i18n.changeLanguage(next);
  };

  return (
    <AddressNamesProvider>
      <AppFrame className="app-frame">
        <a className="skip-link" href="#main-content">
          {t("skip")}
        </a>
        <header className="site-header">
          <div className="header-primary shell-width">
            <Link className="brand" to="/" aria-label="Etherview home">
              <img alt="" aria-hidden="true" className="brand-mark" src={etherviewMark} />
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
                aria-describedby={searchError ? "global-search-error" : undefined}
                aria-invalid={Boolean(searchError)}
                onChange={(event) => {
                  setQuery(event.target.value);
                  setSearchError("");
                }}
                placeholder={t("actions.searchPlaceholder")}
                type="search"
                value={query}
              />
              <button type="submit">{t("actions.search")}</button>
              {searchError ? (
                <small className="global-search-error" id="global-search-error" role="alert">
                  {searchError}
                </small>
              ) : null}
            </form>

            <div className="header-controls">
              <button
                aria-label={t("actions.toggleTheme")}
                aria-pressed={theme === "dark"}
                className="control icon-control"
                onClick={toggleTheme}
                type="button"
              >
                <ShellIcon name={theme === "dark" ? "moon" : "sun"} />
              </button>
              <button
                aria-label={t("actions.toggleLanguage")}
                className="control language-control"
                onClick={toggleLanguage}
                type="button"
              >
                {i18n.resolvedLanguage?.startsWith("zh") ? "EN" : "中文"}
              </button>
              <WalletMenu />
            </div>
          </div>

          <AppNavigation />
        </header>

        <main id="main-content" className="shell-width site-main" tabIndex={-1}>
          <Outlet />
        </main>

        <footer className="site-footer">
          <div className="shell-width footer-inner">
            <span className="footer-brand">Etherview</span>
            <span>{t("footer.description")}</span>
          </div>
        </footer>
      </AppFrame>
    </AddressNamesProvider>
  );
}
