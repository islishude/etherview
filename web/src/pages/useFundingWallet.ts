import { useEffect, useRef, useState, type RefObject } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { readTopupBalance, TopupBalanceError } from "@/billing/balance";
import { usePrivateIdentity, usePrivateLifetime } from "@/api/privateAccount";
import type { BillingConfig, BillingPaymentConfig } from "@/api/billing";
import { type ActiveWallet, useWallet } from "@/wallet/WalletProvider";
import { BillingChainError, toWalletBoundaryError } from "@/wallet/eip6963";

export interface ChainFailure {
  key: string;
  error: BillingChainError;
}

export function useFundingNetworkError(
  chainFailure: ChainFailure | undefined,
  walletKey: string,
  networkName: string | undefined,
  setNotification: (value: string | undefined) => void,
  setChainFailure: (value: ChainFailure | undefined) => void,
) {
  const { t } = useTranslation();
  useEffect(() => {
    setChainFailure(undefined);
    setNotification(undefined);
  }, [walletKey, setChainFailure, setNotification]);
  const networkError =
    chainFailure?.key === walletKey
      ? t(
          `billing.guide.switchError.${chainFailure.error.code === "USER_REJECTED" ? "rejected" : chainFailure.error.code === "CHAIN_MISMATCH" ? "mismatch" : chainFailure.error.code === "INVALID_PROVIDER_RESPONSE" ? "invalid" : "failed"}`,
          { network: networkName ?? chainFailure.error.targetChainID },
        )
      : undefined;
  useEffect(() => {
    if (networkError) setNotification(networkError);
  }, [networkError, chainFailure, setNotification]);
  return networkError;
}

export function useFundingView(view: string, close: (() => Promise<void>) | undefined) {
  const state = useRef({ view, revision: 0 });
  if (state.current.view !== view) state.current = { view, revision: state.current.revision + 1 };
  useEffect(() => {
    if (view !== "payment") return;
    return () => {
      void close?.();
    };
  }, [view, close]);
  return state.current.revision;
}

export function useFundingSelection({
  wallet,
  identity,
  viewRevision,
  currentWalletKey,
  config,
  payment,
  walletReady,
  checkingBalanceRef,
  submittingRef,
  setCheckingBalance,
  setChainFailure,
  setNotification,
  setPaymentNetwork,
  setPaymentOptionID,
  setAmount,
  setError,
  setNotice,
}: {
  wallet: ReturnType<typeof useWallet>;
  identity: string;
  viewRevision: number;
  currentWalletKey: RefObject<string>;
  config: BillingConfig | undefined;
  payment: BillingPaymentConfig | undefined;
  walletReady: boolean;
  checkingBalanceRef: RefObject<boolean>;
  submittingRef: RefObject<boolean>;
  setCheckingBalance: (value: boolean) => void;
  setChainFailure: (value: ChainFailure | undefined) => void;
  setNotification: (value: string | undefined) => void;
  setPaymentNetwork: (value: string) => void;
  setPaymentOptionID: (value: string) => void;
  setAmount: (value: string) => void;
  setError: (value: string | undefined) => void;
  setNotice: (value: string | undefined) => void;
}) {
  const client = useQueryClient();
  const prefix = ["private-account", identity, "billing"];
  return (network: string, optionID: string) => {
    if (checkingBalanceRef.current || submittingRef.current) return;
    setChainFailure(undefined);
    setNotification(undefined);
    setPaymentNetwork(network);
    setPaymentOptionID(optionID);
    setAmount("");
    setError(undefined);
    setNotice(undefined);
    if (network === payment?.network || !walletReady || !wallet.withBillingChain) return;
    const nextOption = config!.payment_chains.find((chain) => chain.network === network)?.tokens[0];
    if (!nextOption) return;
    const nextPayment = { ...config!, ...nextOption };
    const nextKey = fundingWalletKey(identity, wallet.active, nextPayment) + `:${viewRevision}`;
    client.removeQueries({
      queryKey: [...prefix, "wallet-balance", nextKey],
      exact: true,
    });
    checkingBalanceRef.current = true;
    setCheckingBalance(true);
    void wallet
      .withBillingChain(network, async () => undefined, true)
      .catch((cause) => {
        if (currentWalletKey.current !== nextKey) return;
        setChainFailure({
          key: nextKey,
          error:
            cause instanceof BillingChainError
              ? cause
              : new BillingChainError(
                  "entry",
                  network.slice("eip155:".length),
                  toWalletBoundaryError(cause).code,
                ),
        });
      })
      .finally(() => {
        checkingBalanceRef.current = false;
        setCheckingBalance(false);
      });
  };
}

export function useFundingBalance({
  wallet,
  walletKey,
  currentWalletKey,
  payment,
  walletReady,
  onPaymentChain,
  submitting,
  submittingRef,
  checkingBalance,
  checkingBalanceRef,
  setCheckingBalance,
  setChainFailure,
  notify,
  enabled,
}: {
  wallet: ReturnType<typeof useWallet>;
  walletKey: string;
  currentWalletKey: RefObject<string>;
  payment: BillingPaymentConfig | undefined;
  walletReady: boolean;
  onPaymentChain: boolean;
  submitting: boolean;
  submittingRef: RefObject<boolean>;
  checkingBalance: boolean;
  checkingBalanceRef: RefObject<boolean>;
  setCheckingBalance: (value: boolean) => void;
  setChainFailure: (value: ChainFailure | undefined) => void;
  notify: (message: string | undefined) => void;
  enabled: boolean;
}) {
  const identity = usePrivateIdentity();
  const lifetime = usePrivateLifetime();
  const client = useQueryClient();
  const prefix = ["private-account", identity, "billing"];
  const [balanceFailure, setBalanceFailure] = useState<string>();
  const balance = useQuery({
    queryKey: [...prefix, "wallet-balance", walletKey],
    queryFn: () =>
      readTopupBalance(
        wallet.readContract,
        wallet.active!.account,
        payment!.asset!,
        payment!.network!.slice("eip155:".length),
      ),
    enabled:
      enabled &&
      walletReady &&
      onPaymentChain &&
      !submitting &&
      !checkingBalance &&
      Boolean(payment?.x402_topups_enabled),
    retry: false,
    staleTime: Infinity,
  });
  const balanceError = balanceFailure === walletKey || (onPaymentChain && Boolean(balance.error));
  const refreshBalance = async () => {
    if (checkingBalanceRef.current || submittingRef.current || !walletReady || !payment) return;
    setChainFailure(undefined);
    notify(undefined);
    if (onPaymentChain) {
      setBalanceFailure(undefined);
      const result = await balance.refetch();
      if (currentWalletKey.current === walletKey && result.error) {
        setBalanceFailure(walletKey);
      }
      return;
    }
    if (!wallet.withBillingChain) return;
    checkingBalanceRef.current = true;
    setCheckingBalance(true);
    setBalanceFailure(undefined);
    const signal = lifetime();
    const isCurrent = () => !signal.aborted && currentWalletKey.current === walletKey;
    try {
      const value = await wallet.withBillingChain(
        payment.network!,
        async (selected) => {
          if (!isCurrent()) throw new TopupBalanceError("unavailable");
          return readTopupBalance(
            wallet.readContract,
            selected.account,
            payment.asset!,
            payment.network!.slice("eip155:".length),
          );
        },
        true,
      );
      if (isCurrent()) client.setQueryData([...prefix, "wallet-balance", walletKey], value);
    } catch (cause) {
      if (isCurrent()) {
        if (cause instanceof TopupBalanceError) {
          setBalanceFailure(walletKey);
        } else {
          setChainFailure({
            key: walletKey,
            error:
              cause instanceof BillingChainError
                ? cause
                : new BillingChainError(
                    "entry",
                    payment.network!.slice("eip155:".length),
                    toWalletBoundaryError(cause).code,
                  ),
          });
        }
      }
    } finally {
      checkingBalanceRef.current = false;
      if (!signal.aborted) setCheckingBalance(false);
    }
  };
  return {
    balance,
    balanceError,
    refreshBalance,
    markBalanceUnavailable: () => setBalanceFailure(walletKey),
  };
}

export function fundingWalletKey(
  identity: string,
  active: ActiveWallet | undefined,
  payment: BillingPaymentConfig | undefined,
) {
  return JSON.stringify([
    identity,
    active && [
      active.uuid,
      active.account,
      active.authChainID ?? active.chainID,
      active.authRevision ?? active.revision,
    ],
    payment?.network,
    payment?.asset,
    payment?.recipient,
    payment?.id,
  ]);
}

export function fundingBalanceBlocked(
  balance: { data?: bigint; isFetching: boolean; error: unknown },
  amount: string | undefined,
  crossChain: boolean,
) {
  if (crossChain) return amount === undefined;
  return (
    amount === undefined ||
    balance.data === undefined ||
    balance.data < BigInt(amount) ||
    balance.isFetching ||
    Boolean(balance.error)
  );
}
