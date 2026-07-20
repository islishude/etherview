import { useTranslation } from "react-i18next";

import { shorten } from "./format";
import { useWallet } from "@/wallet/WalletProvider";

export function WalletMenu() {
  const { t } = useTranslation();
  const wallet = useWallet();

  return (
    <details className="wallet-menu">
      <summary className="control wallet-summary">
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
            <button className="button secondary full" type="button" onClick={wallet.disconnect}>
              {t("actions.disconnect")}
            </button>
          </>
        ) : wallet.providers.length > 0 ? (
          <div className="wallet-list" aria-label={t("wallet.choose")}>
            {wallet.providers.map((provider) => (
              <button
                className="wallet-option"
                disabled={wallet.connecting}
                key={provider.info.uuid}
                onClick={() => void wallet.connect(provider.info.uuid).catch(() => undefined)}
                type="button"
              >
                <span className="wallet-monogram" aria-hidden="true">
                  {provider.info.name.slice(0, 1).toUpperCase()}
                </span>
                <span>
                  <strong>{provider.info.name}</strong>
                  <small>{provider.info.rdns}</small>
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
        {wallet.error && <p className="form-error" role="alert">{wallet.error}</p>}
      </div>
    </details>
  );
}
