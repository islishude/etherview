import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { usePublicConfig } from "@/api/hooks";
import {
  WalletBoundaryError,
  walletErrorTranslationKey,
  type WalletBoundaryErrorCode,
} from "@/wallet/eip6963";
import { useWallet } from "@/wallet/WalletProvider";

interface AddNetworkControlProps {
  menuOpen: boolean;
}

export function AddNetworkControl({ menuOpen }: AddNetworkControlProps) {
  const { t } = useTranslation();
  const config = usePublicConfig();
  const wallet = useWallet();
  const [choosing, setChoosing] = useState(false);
  const [status, setStatus] = useState<"idle" | "pending" | "success" | "error">("idle");
  const [error, setError] = useState<WalletBoundaryErrorCode>();
  const requestRevision = useRef(0);
  const menuOpenRef = useRef(menuOpen);
  const chainName = config.data?.wallet_add_chain?.chain_name;

  useEffect(() => {
    menuOpenRef.current = menuOpen;
    if (menuOpen) return;
    requestRevision.current += 1;
    setChoosing(false);
    setStatus("idle");
    setError(undefined);
  }, [menuOpen]);

  if (!chainName) return null;

  const add = async (uuid: string) => {
    const revision = requestRevision.current + 1;
    requestRevision.current = revision;
    setChoosing(false);
    setStatus("pending");
    setError(undefined);
    try {
      await wallet.addChain(uuid);
      if (requestRevision.current !== revision || !menuOpenRef.current) return;
      setStatus("success");
    } catch (cause) {
      if (requestRevision.current !== revision || !menuOpenRef.current) return;
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
    if (wallet.providers.length === 0) return;
    wallet.discover();
    setChoosing(true);
    setStatus("idle");
    setError(undefined);
  };

  return (
    <section
      aria-labelledby="wallet-network-title"
      className="wallet-network-section"
    >
      <div className="popover-heading">
        <strong id="wallet-network-title">{t("common.chain")}</strong>
        <span className="quiet">{chainName}</span>
      </div>
      <button
        className="button secondary full add-network-button"
        disabled={wallet.addingChain || (!wallet.active && wallet.providers.length === 0)}
        onClick={start}
        type="button"
      >
        {t("wallet.addNetwork", { chain: chainName })}
      </button>
      {choosing && wallet.providers.length > 1 ? (
        <div
          aria-label={t("wallet.addNetworkChoose", { chain: chainName })}
          className="add-network-picker"
          role="group"
        >
          {wallet.providers.slice(0, 32).map((provider) => (
            <button
              className="add-network-option"
              disabled={wallet.addingChain}
              key={provider.uuid}
              onClick={() => void add(provider.uuid)}
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
      <span className={`add-network-status ${status}`} role="status" aria-live="polite">
        {status === "pending"
          ? t("wallet.addNetworkPending", { chain: chainName })
          : status === "success"
            ? t("wallet.addNetworkSuccess", { chain: chainName })
            : status === "error" && error
              ? t(walletErrorTranslationKey(error))
              : null}
      </span>
    </section>
  );
}
