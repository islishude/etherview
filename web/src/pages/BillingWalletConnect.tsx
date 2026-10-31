import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useWallet } from "@/wallet/WalletProvider";
import { walletErrorTranslationKey } from "@/wallet/eip6963";

export function BillingWalletConnect() {
  const { t } = useTranslation();
  const wallet = useWallet();
  const [choosing, setChoosing] = useState(false);
  const connect = (uuid: string) => {
    void wallet.connect(uuid).catch(() => {
      // WalletProvider owns the sanitized error and retryable connection state.
    });
  };
  return (
    <div className="billing-connect">
      <p role="status">{t("billing.guide.connectHint")}</p>
      <button
        className="button secondary"
        type="button"
        disabled={wallet.connecting}
        onClick={() => {
          if (wallet.providers.length === 1) connect(wallet.providers[0]!.uuid);
          else {
            wallet.discover();
            setChoosing(true);
          }
        }}
      >
        {t(wallet.connecting ? "actions.connecting" : "actions.connect")}
      </button>
      {choosing && wallet.providers.length > 1 && (
        <div className="wallet-list" role="group" aria-label={t("wallet.choose")}>
          {wallet.providers.slice(0, 32).map((provider) => (
            <button
              className="wallet-option"
              type="button"
              disabled={wallet.connecting}
              key={provider.uuid}
              onClick={() => connect(provider.uuid)}
            >
              {provider.name}
            </button>
          ))}
        </div>
      )}
      {choosing && wallet.providers.length === 0 && <p>{t("wallet.installHint")}</p>}
      {wallet.error && <p role="alert">{t(walletErrorTranslationKey(wallet.error))}</p>}
    </div>
  );
}
