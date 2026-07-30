import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import { useAuth } from "@/auth/AuthProvider";
import { usePublicConfig } from "@/api/hooks";
import { chainsMatch } from "@/wallet/eip6963";
import { useWallet } from "@/wallet/WalletProvider";

interface SIWELoginControlProps {
  full?: boolean;
}

export function SIWELoginControl({ full = false }: SIWELoginControlProps) {
  const { t } = useTranslation();
  const auth = useAuth();
  const config = usePublicConfig();
  const wallet = useWallet();
  const [choosing, setChoosing] = useState(false);
  const expectedChainID = config.data?.chain_id;
  const walletOnChain =
    wallet.active !== undefined &&
    chainsMatch(wallet.active.chainID, expectedChainID);
  const busy = auth.pending || wallet.connecting;

  useEffect(() => {
    if (
      wallet.active ||
      auth.session.authenticated ||
      wallet.providers.length <= 1
    ) {
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
        disabled={
          busy ||
          (wallet.active ? !walletOnChain : wallet.providers.length === 0)
        }
        onClick={start}
        type="button"
      >
        {busy ? t("auth.signIn.pending") : t("auth.signIn.action")}
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
