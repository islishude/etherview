import { useEffect, useRef, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import {
  authErrorTranslationKey,
  useAuth,
} from "@/auth/AuthProvider";
import { shorten } from "./format";
import { walletErrorTranslationKey } from "@/wallet/eip6963";
import { useWallet } from "@/wallet/WalletProvider";
import { AddNetworkControl } from "./AddNetworkControl";
import { SIWELoginControl } from "./SIWELoginControl";

export function WalletMenu() {
  const { t } = useTranslation();
  const wallet = useWallet();
  const auth = useAuth();
  const [open, setOpen] = useState(false);
  const menuRef = useRef<HTMLDetailsElement | null>(null);
  const summaryRef = useRef<HTMLElement | null>(null);
  const focusAfterTransition = useRef(false);
  const focusWithinMenu = useRef(false);
  const wasConnected = useRef(Boolean(wallet.active));
  const errorMessage = wallet.error
    ? t(walletErrorTranslationKey(wallet.error))
    : undefined;
  const authErrorMessage = auth.error
    ? t(authErrorTranslationKey(auth.error))
    : undefined;

  useEffect(() => {
    const connected = Boolean(wallet.active);
    const transitioned = wasConnected.current !== connected;
    wasConnected.current = connected;
    if (!transitioned || (!focusAfterTransition.current && !focusWithinMenu.current)) {
      return;
    }
    summaryRef.current?.focus({ preventScroll: true });
    focusAfterTransition.current = false;
  }, [wallet.active]);

  useEffect(() => {
    if (!open) return;
    const dismissOnOutsidePointer = (event: PointerEvent) => {
      const target = event.target;
      if (!(target instanceof Node) || !menuRef.current?.contains(target)) {
        if (menuRef.current) menuRef.current.open = false;
        setOpen(false);
      }
    };
    document.addEventListener("pointerdown", dismissOnOutsidePointer);
    return () => document.removeEventListener("pointerdown", dismissOnOutsidePointer);
  }, [open]);

  return (
    <>
      <details
        className="wallet-menu"
        ref={menuRef}
        onBlurCapture={(event) => {
          const next = event.relatedTarget;
          if (next instanceof Node && !event.currentTarget.contains(next)) {
            focusWithinMenu.current = false;
          }
        }}
        onFocusCapture={() => {
          focusWithinMenu.current = true;
        }}
        onToggle={(event) => {
          setOpen(event.currentTarget.open);
        }}
      >
        <summary
          aria-label={
            wallet.active
              ? t("wallet.menuConnected", {
                  account: wallet.active.account,
                  name: wallet.active.name,
                })
              : t("actions.connect")
          }
          className="control wallet-summary"
          ref={summaryRef}
        >
          <span className={wallet.active ? "status-dot success" : "status-dot"} aria-hidden="true" />
          {wallet.active ? shorten(wallet.active.account, 6, 4) : t("actions.connect")}
        </summary>
        <div className="wallet-popover">
          <div className="popover-heading">
            <strong>{t("wallet.title")}</strong>
            {wallet.active && <span className="quiet">{t("common.chain")} {wallet.active.chainID}</span>}
          </div>
          {wallet.active ? (
            <>
              <code className="wallet-account">{wallet.active.account}</code>
              <button
                className="button secondary full"
                type="button"
                onClick={() => {
                  focusAfterTransition.current = true;
                  wallet.disconnect();
                }}
              >
                {t("actions.disconnect")}
              </button>
            </>
          ) : wallet.providers.length > 0 ? (
            <div className="wallet-list" aria-label={t("wallet.choose")} role="group">
              {wallet.providers.map((provider) => (
                <button
                  className="wallet-option"
                  disabled={wallet.connecting}
                  key={provider.uuid}
                  onClick={() => {
                    focusAfterTransition.current = true;
                    void wallet.connect(provider.uuid).catch(() => {
                      focusAfterTransition.current = false;
                    });
                  }}
                  type="button"
                >
                  <span className="wallet-monogram" aria-hidden="true">
                    {provider.name.slice(0, 1).toUpperCase()}
                  </span>
                  <span>
                    <strong>{provider.name}</strong>
                    <small>{provider.rdns}</small>
                  </span>
                </button>
              ))}
            </div>
          ) : (
            <div className="empty-wallet">
              <strong>{t("wallet.none")}</strong>
              <small>{t("wallet.installHint")}</small>
              <button className="button secondary full" type="button" onClick={wallet.discover}>
                {t("actions.retry")}
              </button>
            </div>
          )}
          {wallet.connecting && <p role="status">{t("actions.connecting")}</p>}
          {errorMessage && <p className="form-error">{errorMessage}</p>}
          <AddNetworkControl menuOpen={open} />
          {auth.enabled && (
            <section
              className="wallet-auth-section"
              aria-labelledby="wallet-auth-title"
            >
              <div className="popover-heading">
                <strong id="wallet-auth-title">{t("auth.menu.title")}</strong>
                <span className="quiet">
                  {auth.session.authenticated
                    ? t("auth.sessionState.authenticated")
                    : t("auth.sessionState.anonymous")}
                </span>
              </div>
              {auth.loading ? (
                <p role="status">{t("auth.sessionState.checking")}</p>
              ) : auth.session.authenticated && auth.session.user ? (
                <>
                  <span className="authenticated-user">
                    <span className="status-dot success" aria-hidden="true" />
                    <span>
                      <strong>
                        {auth.session.user.display_name ??
                          shorten(auth.session.user.address, 6, 4)}
                      </strong>
                      <small>{t(`auth.role.${auth.session.user.role}`)}</small>
                    </span>
                  </span>
                  <Link
                    className="button secondary inline-button full"
                    to="/account"
                  >
                    {t("auth.account.open")}
                  </Link>
                  <button
                    className="button secondary full"
                    disabled={auth.pending}
                    onClick={() => void auth.logout()}
                    type="button"
                  >
                    {t("auth.sessionActions.logout")}
                  </button>
                </>
              ) : (
                <>
                  <p className="quiet">{t("auth.menu.walletIsNotLogin")}</p>
                  <SIWELoginControl full />
                  <Link
                    className="button secondary inline-button full"
                    to="/account"
                  >
                    {t("auth.account.open")}
                  </Link>
                </>
              )}
              {authErrorMessage && (
                <p className="form-error">{authErrorMessage}</p>
              )}
            </section>
          )}
        </div>
      </details>
      {errorMessage && <span className="sr-only" role="alert">{errorMessage}</span>}
      {authErrorMessage && (
        <span className="sr-only" role="alert">{authErrorMessage}</span>
      )}
    </>
  );
}
