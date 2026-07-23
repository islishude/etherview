import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";

import { shorten } from "./format";
import { walletErrorTranslationKey } from "@/wallet/eip6963";
import { useWallet } from "@/wallet/WalletProvider";

export function WalletMenu() {
  const { t } = useTranslation();
  const wallet = useWallet();
  const summaryRef = useRef<HTMLElement | null>(null);
  const focusAfterTransition = useRef(false);
  const focusWithinMenu = useRef(false);
  const wasConnected = useRef(Boolean(wallet.active));
  const errorMessage = wallet.error
    ? t(walletErrorTranslationKey(wallet.error))
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

  return (
    <>
      <details
        className="wallet-menu"
        onBlurCapture={(event) => {
          const next = event.relatedTarget;
          if (next instanceof Node && !event.currentTarget.contains(next)) {
            focusWithinMenu.current = false;
          }
        }}
        onFocusCapture={() => {
          focusWithinMenu.current = true;
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
        </div>
      </details>
      {errorMessage && <span className="sr-only" role="alert">{errorMessage}</span>}
    </>
  );
}
