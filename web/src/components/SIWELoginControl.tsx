import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import { useAuth } from "@/auth/AuthProvider";
import { useWallet } from "@/wallet/WalletProvider";

interface SIWELoginControlProps {
  full?: boolean;
  label?: string;
}

export function SIWELoginControl({ full = false, label }: SIWELoginControlProps) {
  const { t } = useTranslation();
  const auth = useAuth();
  const wallet = useWallet();
  const [choosing, setChoosing] = useState(false);
  const busy = auth.pending || wallet.connecting || wallet.addingChain;

  useEffect(() => {
    if (wallet.active || auth.session.authenticated || wallet.providers.length <= 1) {
      setChoosing(false);
    }
  }, [auth.session.authenticated, wallet.active, wallet.providers.length]);

  const start = () => {
    if (wallet.active) {
      void auth.login();
      return;
    }
    if (wallet.providers.length === 1) {
      void auth.login(wallet.providers[0]!.uuid);
      return;
    }
    if (wallet.providers.length === 0) return;
    wallet.discover();
    setChoosing(true);
  };

  return (
    <div className={`siwe-login-control${full ? " full" : ""}`}>
      <button
        className={`button primary${full ? " full" : ""}`}
        disabled={busy || (!wallet.active && wallet.providers.length === 0)}
        onClick={start}
        type="button"
      >
        {busy ? t("auth.signIn.pending") : (label ?? t("auth.signIn.action"))}
      </button>
      {choosing && wallet.providers.length > 1 ? (
        <div
          aria-label={t("auth.signIn.chooseWallet")}
          className="wallet-list siwe-wallet-picker"
          role="group"
        >
          {wallet.providers.slice(0, 32).map((provider) => (
            <button
              className="wallet-option"
              disabled={busy}
              key={provider.uuid}
              onClick={() => {
                setChoosing(false);
                void auth.login(provider.uuid);
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
      ) : null}
    </div>
  );
}
