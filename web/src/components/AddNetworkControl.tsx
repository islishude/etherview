import { useState } from "react";
import { useTranslation } from "react-i18next";

import { usePublicConfig } from "@/api/hooks";
import {
  WalletBoundaryError,
  walletErrorTranslationKey,
  type WalletBoundaryErrorCode,
} from "@/wallet/eip6963";
import { useWallet } from "@/wallet/WalletProvider";

export function AddNetworkControl() {
  const { t } = useTranslation();
  const config = usePublicConfig();
  const wallet = useWallet();
  const [choosing, setChoosing] = useState(false);
  const [status, setStatus] = useState<"idle" | "pending" | "success" | "error">("idle");
  const [error, setError] = useState<WalletBoundaryErrorCode>();
  const chainName = config.data?.wallet_add_chain?.chain_name;
  if (!chainName) return null;

  const add = async (uuid: string) => {
    setChoosing(false);
    setStatus("pending");
    setError(undefined);
    try {
      await wallet.addChain(uuid);
      setStatus("success");
    } catch (cause) {
      setError(cause instanceof WalletBoundaryError ? cause.code : "REQUEST_FAILED");
      setStatus("error");
    }
  };

  const start = () => {
    const preferred = wallet.active?.uuid;
    if (preferred) {
      void add(preferred);
      return;
    }
    if (wallet.providers.length === 1) {
      void add(wallet.providers[0]!.uuid);
      return;
    }
    wallet.discover();
    setChoosing(true);
    setStatus("idle");
  };

  return (
    <div className="add-network-control">
      <button
        className="add-network-button"
        disabled={wallet.addingChain}
        onClick={start}
        type="button"
      >
        {t("wallet.addNetwork", { chain: chainName })}
      </button>
      {choosing ? (
        <div className="add-network-picker">
          <strong>{t("wallet.addNetworkChoose", { chain: chainName })}</strong>
          {wallet.providers.length === 0 ? (
            <>
              <span>{t("wallet.installHint")}</span>
              <button className="button secondary" onClick={wallet.discover} type="button">
                {t("wallet.addNetworkRetry")}
              </button>
            </>
          ) : wallet.providers.slice(0, 32).map((provider) => (
            <button
              className="button secondary"
              key={provider.uuid}
              onClick={() => void add(provider.uuid)}
              type="button"
            >
              {provider.name}
            </button>
          ))}
        </div>
      ) : null}
      <span className={`add-network-status ${status}`} role="status" aria-live="polite">
        {status === "pending"
          ? t("wallet.addNetworkPending", { chain: chainName })
          : status === "success"
            ? t("wallet.addNetworkSuccess", { chain: chainName })
            : status === "error" && error
              ? t(walletErrorTranslationKey(error))
              : null}
      </span>
    </div>
  );
}
