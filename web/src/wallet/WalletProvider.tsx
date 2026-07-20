import {
  createContext,
  type PropsWithChildren,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";
import { getAddress, isAddress, type Address, type Hex } from "viem";

import {
  assertWalletChain,
  EIP6963_ANNOUNCE_EVENT,
  EIP6963_REQUEST_EVENT,
  type EIP6963ProviderDetail,
  isProviderDetail,
  normalizeChainID,
  WalletBoundaryError,
} from "./eip6963";

export interface ContractCall {
  to: Address;
  data: Hex;
  from?: Address;
  value?: Hex;
}

export interface ContractTransaction extends ContractCall {
  from?: Address;
}

interface ActiveWallet {
  detail: EIP6963ProviderDetail;
  account: Address;
  chainID: string;
}

interface WalletContextValue {
  providers: EIP6963ProviderDetail[];
  active?: ActiveWallet;
  connecting: boolean;
  error?: string;
  discover: () => void;
  connect: (uuid: string) => Promise<void>;
  disconnect: () => void;
  readContract: (call: ContractCall, expectedChainID: string | undefined) => Promise<Hex>;
  sendTransaction: (
    transaction: ContractTransaction,
    expectedChainID: string | undefined,
  ) => Promise<Hex>;
}

const WalletContext = createContext<WalletContextValue | undefined>(undefined);

export function WalletProvider({ children }: PropsWithChildren) {
  const [providersByID, setProvidersByID] = useState<Map<string, EIP6963ProviderDetail>>(
    () => new Map(),
  );
  const [active, setActive] = useState<ActiveWallet>();
  const [connecting, setConnecting] = useState(false);
  const [error, setError] = useState<string>();

  const discover = useCallback(() => {
    window.dispatchEvent(new Event(EIP6963_REQUEST_EVENT));
  }, []);

  useEffect(() => {
    const announce = (event: Event) => {
      if (!(event instanceof CustomEvent) || !isProviderDetail(event.detail)) return;
      setProvidersByID((current) => {
        const next = new Map(current);
        next.set(event.detail.info.uuid, event.detail);
        return next;
      });
    };

    window.addEventListener(EIP6963_ANNOUNCE_EVENT, announce);
    discover();
    return () => window.removeEventListener(EIP6963_ANNOUNCE_EVENT, announce);
  }, [discover]);

  useEffect(() => {
    if (!active) return;

    const provider = active.detail.provider;
    const accountsChanged = (value: unknown) => {
      const account = firstValidAccount(value);
      if (!account) {
        setActive(undefined);
        return;
      }
      setActive((current) => (current ? { ...current, account } : undefined));
    };
    const chainChanged = (value: unknown) => {
      const chainID = normalizeChainID(value);
      if (chainID) setActive((current) => (current ? { ...current, chainID } : undefined));
    };

    provider.on?.("accountsChanged", accountsChanged);
    provider.on?.("chainChanged", chainChanged);
    return () => {
      provider.removeListener?.("accountsChanged", accountsChanged);
      provider.removeListener?.("chainChanged", chainChanged);
    };
  }, [active?.detail.provider]);

  const connect = useCallback(
    async (uuid: string) => {
      const detail = providersByID.get(uuid);
      if (!detail) throw new WalletBoundaryError("NOT_CONNECTED", "Wallet provider is unavailable");

      setConnecting(true);
      setError(undefined);
      try {
        const accounts = await detail.provider.request<unknown>({ method: "eth_requestAccounts" });
        const chain = await detail.provider.request<unknown>({ method: "eth_chainId" });
        const account = firstValidAccount(accounts);
        const chainID = normalizeChainID(chain);
        if (!account || !chainID) {
          throw new WalletBoundaryError(
            "INVALID_PROVIDER_RESPONSE",
            "Wallet returned invalid accounts or chain ID",
          );
        }
        setActive({ detail, account, chainID });
      } catch (cause) {
        const message = cause instanceof Error ? cause.message : "Wallet connection failed";
        setError(message);
        throw cause;
      } finally {
        setConnecting(false);
      }
    },
    [providersByID],
  );

  const requireProvider = useCallback(
    async (expectedChainID: string | undefined): Promise<ActiveWallet> => {
      if (!active) throw new WalletBoundaryError("NOT_CONNECTED", "Connect a wallet first");
      const currentChain = await active.detail.provider.request<unknown>({ method: "eth_chainId" });
      assertWalletChain(currentChain, expectedChainID);
      return { ...active, chainID: normalizeChainID(currentChain) ?? active.chainID };
    },
    [active],
  );

  const readContract = useCallback(
    async (call: ContractCall, expectedChainID: string | undefined) => {
      const wallet = await requireProvider(expectedChainID);
      return wallet.detail.provider.request<Hex>({
        method: "eth_call",
        params: [{ ...call, from: call.from ?? wallet.account }, "latest"],
      });
    },
    [requireProvider],
  );

  const sendTransaction = useCallback(
    async (transaction: ContractTransaction, expectedChainID: string | undefined) => {
      const wallet = await requireProvider(expectedChainID);
      if (transaction.from && getAddress(transaction.from) !== wallet.account) {
        throw new WalletBoundaryError("NOT_CONNECTED", "Transaction sender is not the active account");
      }
      return wallet.detail.provider.request<Hex>({
        method: "eth_sendTransaction",
        params: [{ ...transaction, from: wallet.account }],
      });
    },
    [requireProvider],
  );

  const value = useMemo<WalletContextValue>(
    () => ({
      providers: [...providersByID.values()].sort((left, right) =>
        left.info.name.localeCompare(right.info.name),
      ),
      active,
      connecting,
      error,
      discover,
      connect,
      disconnect: () => {
        setActive(undefined);
        setError(undefined);
      },
      readContract,
      sendTransaction,
    }),
    [active, connect, connecting, discover, error, providersByID, readContract, sendTransaction],
  );

  return <WalletContext.Provider value={value}>{children}</WalletContext.Provider>;
}

function firstValidAccount(value: unknown): Address | undefined {
  if (!Array.isArray(value)) return undefined;
  const account = value.find((candidate): candidate is string =>
    typeof candidate === "string" && isAddress(candidate),
  );
  return account ? getAddress(account) : undefined;
}

export function useWallet(): WalletContextValue {
  const context = useContext(WalletContext);
  if (!context) throw new Error("useWallet must be used inside WalletProvider");
  return context;
}
