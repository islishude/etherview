import {
  createContext,
  type PropsWithChildren,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { getAddress, isAddress, toHex, type Address, type Hex } from "viem";

import { Message } from "@/components/Message";
import { useTranslation } from "react-i18next";

import type { AuthChallenge } from "@/api/auth";
import { usePublicConfig } from "@/api/publicConfig";
import {
  BillingChainError,
  assertWalletChain,
  buildAddEthereumChainParameter,
  EIP6963_ANNOUNCE_EVENT,
  EIP6963_REQUEST_EVENT,
  type EIP1193Provider,
  type EIP1193RequestArguments,
  type EIP6963ProviderDetail,
  isContractCalldata,
  isContractResult,
  isTransactionHash,
  isUint256Quantity,
  isWalletSignature,
  normalizeChainID,
  normalizeProviderChainID,
  providerErrorCode,
  snapshotProviderDetail,
  toWalletBoundaryError,
  WalletBoundaryError,
  type WalletBoundaryErrorCode,
} from "./eip6963";
import type { BillingSigningBinding, BillingTypedData } from "./billing";

export interface ContractCall {
  to: Address;
  data: Hex;
  value?: Hex;
}

export type ContractTransaction = ContractCall;

interface InternalActiveWallet {
  detail: EIP6963ProviderDetail;
  account: Address;
  chainID: string;
  revision: number;
  authRevision?: number;
  authChainID?: string;
}

interface WalletOption {
  uuid: string;
  name: string;
  rdns: string;
}

export interface ActiveWallet {
  uuid: string;
  name: string;
  account: Address;
  chainID: string;
  revision: number;
  authRevision?: number;
  authChainID?: string;
}

interface WalletContextValue {
  withBillingChain?: <T>(
    network: string,
    operation: (wallet: ActiveWallet) => Promise<T>,
  ) => Promise<T>;
  providers: WalletOption[];
  active?: ActiveWallet;
  connecting: boolean;
  addingChain: boolean;
  error?: WalletBoundaryErrorCode;
  discover: () => void;
  connect: (uuid: string) => Promise<ActiveWallet>;
  addChain: (uuid: string) => Promise<void>;
  prepareLogin: (expected: ActiveWallet) => Promise<ActiveWallet>;
  disconnect: () => void;
  getActiveWallet: () => ActiveWallet | undefined;
  isActiveWallet: (expected: ActiveWallet) => boolean;
  readContract: (call: ContractCall, expectedChainID: string | undefined) => Promise<Hex>;
  sendTransaction: (
    transaction: ContractTransaction,
    expectedChainID: string | undefined,
  ) => Promise<Hex>;
  signSIWEChallenge: (challenge: AuthChallenge, expected: ActiveWallet) => Promise<Hex>;
  signBillingTypedData?: (
    typedData: BillingTypedData,
    binding: BillingSigningBinding,
    expected: ActiveWallet,
  ) => Promise<Hex>;
  waitForBillingTransaction?: (transactionHash: Hex, expected: ActiveWallet) => Promise<void>;
}

const WalletContext = createContext<WalletContextValue | undefined>(undefined);
const MAX_DISCOVERED_PROVIDERS = 32;
const MAX_PROVIDER_ACCOUNTS = 256;

export function WalletProvider({ children }: PropsWithChildren) {
  const [restorationFailure, setRestorationFailure] = useState<BillingChainError>();
  const publicConfig = usePublicConfig();
  const configuredChainID = publicConfig.data?.chain_id;
  const [providersByID, setProvidersByID] = useState<Map<string, EIP6963ProviderDetail>>(
    () => new Map(),
  );
  const [internalActive, setInternalActive] = useState<InternalActiveWallet>();
  const [connecting, setConnecting] = useState(false);
  const [addingChain, setAddingChain] = useState(false);
  const [error, setError] = useState<WalletBoundaryErrorCode>();
  const activeRef = useRef<InternalActiveWallet | undefined>(undefined);
  const connectionAttemptRef = useRef(0);
  const walletRevisionRef = useRef(0);
  const billingScopeRef = useRef<
    { original: InternalActiveWallet; expectedChain?: string; paymentChain: string } | undefined
  >(undefined);

  const loginPreparationRef = useRef<{ target: string; arrived: boolean } | undefined>(undefined);

  const commitActive = useCallback((next: InternalActiveWallet | undefined, controlled = false) => {
    loginPreparationRef.current = undefined;
    const scope = controlled ? billingScopeRef.current : undefined;
    if (!controlled) billingScopeRef.current = undefined;
    walletRevisionRef.current += 1;
    const committed = next
      ? {
          ...next,
          revision: walletRevisionRef.current,
          authRevision: scope
            ? (scope.original.authRevision ?? scope.original.revision)
            : walletRevisionRef.current,
          authChainID: scope
            ? (scope.original.authChainID ?? scope.original.chainID)
            : next.chainID,
        }
      : undefined;
    activeRef.current = committed;
    setInternalActive(committed);
    return committed;
  }, []);

  const failActiveSession = useCallback(
    (session: InternalActiveWallet, code: WalletBoundaryErrorCode) => {
      if (activeRef.current !== session) return;
      commitActive(undefined);
      setError(code);
    },
    [commitActive],
  );

  const markTransactionOutcomeUnknown = useCallback(
    (session: InternalActiveWallet) => {
      // A provider call may complete after an account/chain ABA transition.
      // Preserve the newly committed wallet session in that case, but retain
      // the warning independently of the stale caller so the UI cannot lose
      // the unknown transaction outcome when its operation context changes.
      const current = activeRef.current;
      if (current === session) {
        commitActive(undefined);
        setError("TRANSACTION_OUTCOME_UNKNOWN");
      } else if (current !== undefined) {
        setError("TRANSACTION_OUTCOME_UNKNOWN");
      }
    },
    [commitActive],
  );

  const requestActiveProvider = useCallback(
    async (session: InternalActiveWallet, arguments_: EIP1193RequestArguments) => {
      try {
        return await requestProvider(session.detail.provider, arguments_);
      } catch (cause) {
        const boundaryError = toWalletBoundaryError(cause);
        if (
          boundaryError.code === "NOT_CONNECTED" ||
          boundaryError.code === "CHAIN_MISMATCH" ||
          boundaryError.code === "PROVIDER_DISCONNECTED"
        ) {
          failActiveSession(session, boundaryError.code);
        }
        throw boundaryError;
      }
    },
    [failActiveSession],
  );

  const discover = useCallback(() => {
    window.dispatchEvent(new Event(EIP6963_REQUEST_EVENT));
  }, []);

  useEffect(() => {
    const announce = (event: Event) => {
      if (!(event instanceof CustomEvent)) return;
      const detail = snapshotProviderDetail(event.detail);
      if (!detail) return;

      setProvidersByID((current) => {
        // A UUID identifies one provider for the page lifetime. Preserve the
        // first announcement so a colliding event cannot replace a selected
        // wallet with another provider object.
        if (current.has(detail.info.uuid) || current.size >= MAX_DISCOVERED_PROVIDERS) {
          return current;
        }
        const next = new Map(current);
        next.set(detail.info.uuid, detail);
        return next;
      });
    };

    window.addEventListener(EIP6963_ANNOUNCE_EVENT, announce);
    discover();
    return () => window.removeEventListener(EIP6963_ANNOUNCE_EVENT, announce);
  }, [discover]);

  useEffect(() => {
    if (!internalActive) return;

    const provider = internalActive.detail.provider;
    const isCurrentProvider = () => activeRef.current?.detail.provider === provider;
    const failClosed = (code: WalletBoundaryErrorCode) => {
      if (!isCurrentProvider()) return;
      commitActive(undefined);
      setError(code);
    };
    const accountsChanged = (value: unknown) => {
      if (!isCurrentProvider()) return;
      const accounts = parseAccounts(value);
      if (!accounts) {
        failClosed("INVALID_PROVIDER_RESPONSE");
        return;
      }
      if (accounts.length === 0) {
        failClosed("NOT_CONNECTED");
        return;
      }
      const current = activeRef.current;
      if (!current) return;
      // Some providers announce the same selected account during a chain switch.
      // This is not an identity transition; real changes still revoke the session.
      if (current.account === accounts[0]) return;
      commitActive({ ...current, account: accounts[0]! });
      setError(undefined);
    };
    const chainChanged = (value: unknown) => {
      if (!isCurrentProvider()) return;
      const chainID = normalizeProviderChainID(value);
      if (!chainID) {
        failClosed("INVALID_PROVIDER_RESPONSE");
        return;
      }
      const current = activeRef.current;
      if (!current) return;
      // The preparation listener fences the transition until final RPC checks.
      if (loginPreparationRef.current?.target === chainID) return;
      const scope = billingScopeRef.current;
      // The verified RPC fallback may precede its delayed chainChanged event.
      // This acknowledgement may also arrive after restoration closes the scope.
      if (current.chainID === chainID) return;
      const controlled =
        scope !== undefined &&
        scope.expectedChain === chainID &&
        current.detail.provider === scope.original.detail.provider &&
        current.account === scope.original.account;
      if (controlled) scope.expectedChain = undefined;
      commitActive({ ...current, chainID }, controlled);
      setError(undefined);
    };
    const disconnected = () => failClosed("PROVIDER_DISCONNECTED");

    try {
      provider.on("accountsChanged", accountsChanged);
      provider.on("chainChanged", chainChanged);
      provider.on("disconnect", disconnected);
    } catch {
      failClosed("INVALID_PROVIDER_RESPONSE");
    }

    return () => {
      removeProviderListener(provider, "accountsChanged", accountsChanged);
      removeProviderListener(provider, "chainChanged", chainChanged);
      removeProviderListener(provider, "disconnect", disconnected);
    };
  }, [commitActive, internalActive]);

  const connect = useCallback(
    async (uuid: string) => {
      const detail = providersByID.get(uuid);
      if (!detail) throw new WalletBoundaryError("NOT_CONNECTED");

      const attempt = connectionAttemptRef.current + 1;
      connectionAttemptRef.current = attempt;
      setConnecting(true);
      setError(undefined);
      try {
        const accountsResponse = await requestProvider(detail.provider, {
          method: "eth_requestAccounts",
        });
        const chainResponse = await requestProvider(detail.provider, { method: "eth_chainId" });
        const accounts = parseAccounts(accountsResponse);
        const chainID = normalizeProviderChainID(chainResponse);
        if (!accounts || accounts.length === 0 || !chainID) {
          throw new WalletBoundaryError("INVALID_PROVIDER_RESPONSE");
        }
        if (connectionAttemptRef.current !== attempt) {
          throw new WalletBoundaryError("SESSION_CHANGED");
        }
        const connected = commitActive({
          detail,
          account: accounts[0]!,
          chainID,
          revision: 0,
        });
        if (!connected) throw new WalletBoundaryError("SESSION_CHANGED");
        return publicActiveWallet(connected);
      } catch (cause) {
        const boundaryError = toWalletBoundaryError(cause);
        if (connectionAttemptRef.current === attempt) {
          commitActive(undefined);
          setError(boundaryError.code);
        }
        throw boundaryError;
      } finally {
        if (connectionAttemptRef.current === attempt) setConnecting(false);
      }
    },
    [commitActive, providersByID],
  );

  const isActiveWallet = useCallback((expected: ActiveWallet) => {
    const current = activeRef.current;
    return current !== undefined && walletIdentityMatches(current, expected);
  }, []);

  const getActiveWallet = useCallback(() => {
    const current = activeRef.current;
    return current ? publicActiveWallet(current) : undefined;
  }, []);

  const addChain = useCallback(
    async (uuid: string) => {
      const detail = providersByID.get(uuid);
      if (!detail) throw new WalletBoundaryError("NOT_CONNECTED");
      const parameter = buildAddEthereumChainParameter(publicConfig.data?.wallet_add_chain);
      setAddingChain(true);
      try {
        const result = await requestProvider(detail.provider, {
          method: "wallet_addEthereumChain",
          params: [parameter],
        });
        if (result !== null) {
          throw new WalletBoundaryError("INVALID_PROVIDER_RESPONSE");
        }
      } catch (cause) {
        throw toWalletBoundaryError(cause);
      } finally {
        setAddingChain(false);
      }
    },
    [providersByID, publicConfig.data?.wallet_add_chain],
  );

  const requireProvider = useCallback(
    async (expectedChainID: string | undefined): Promise<InternalActiveWallet> => {
      const normalizedExpectedChainID = normalizeChainID(expectedChainID);
      if (normalizedExpectedChainID === undefined) {
        throw new WalletBoundaryError("CHAIN_UNAVAILABLE");
      }
      const selected = activeRef.current;
      if (!selected) throw new WalletBoundaryError("NOT_CONNECTED");

      const chainResponse = await requestActiveProvider(selected, {
        method: "eth_chainId",
      });
      assertCurrentWallet(activeRef.current, selected);
      const chainID = normalizeProviderChainID(chainResponse);
      if (chainID === undefined) {
        failActiveSession(selected, "INVALID_PROVIDER_RESPONSE");
        throw new WalletBoundaryError("INVALID_PROVIDER_RESPONSE");
      }
      if (chainID !== selected.chainID) {
        commitActive({ ...selected, chainID });
        const code = chainID === normalizedExpectedChainID ? "SESSION_CHANGED" : "CHAIN_MISMATCH";
        setError(code);
        throw new WalletBoundaryError(code);
      }
      try {
        assertWalletChain(chainResponse, normalizedExpectedChainID);
      } catch (cause) {
        const boundaryError = toWalletBoundaryError(cause);
        if (boundaryError.code === "CHAIN_MISMATCH") setError(boundaryError.code);
        throw boundaryError;
      }

      const accountsResponse = await requestActiveProvider(selected, {
        method: "eth_accounts",
      });
      assertCurrentWallet(activeRef.current, selected);
      const accounts = parseAccounts(accountsResponse);
      if (!accounts) {
        failActiveSession(selected, "INVALID_PROVIDER_RESPONSE");
        throw new WalletBoundaryError("INVALID_PROVIDER_RESPONSE");
      }
      if (accounts.length === 0) {
        failActiveSession(selected, "NOT_CONNECTED");
        throw new WalletBoundaryError("NOT_CONNECTED");
      }
      if (accounts[0] !== selected.account) {
        failActiveSession(selected, "ACCOUNT_CHANGED");
        throw new WalletBoundaryError("ACCOUNT_CHANGED");
      }

      setError(undefined);
      return selected;
    },
    [commitActive, failActiveSession, requestActiveProvider],
  );

  const prepareLogin = useLoginPreparation({
    activeRef,
    loginPreparationRef,
    billingScopeRef,
    configuredChainID,
    addChainConfig: publicConfig.data?.wallet_add_chain,
    commitActive,
    setError,
  });

  const withBillingChain = useBillingChain({
    activeRef,
    billingScopeRef,
    commitActive,
    configuredChainID,
    requireProvider,
    setError,
    setRestorationFailure,
  });

  const {
    readContract,
    sendTransaction,
    signSIWEChallenge,
    signBillingTypedData,
    waitForBillingTransaction,
  } = useWalletOperations({
    activeRef,
    configuredChainID,
    requestActiveProvider,
    requireProvider,
    markTransactionOutcomeUnknown,
    billingScopeRef,
  });

  const disconnect = useCallback(() => {
    connectionAttemptRef.current += 1;
    setConnecting(false);
    setError(undefined);
    commitActive(undefined);
  }, [commitActive]);

  const value = useMemo<WalletContextValue>(
    () => ({
      withBillingChain,
      providers: [...providersByID.values()]
        .map(({ info }) => ({ uuid: info.uuid, name: info.name, rdns: info.rdns }))
        .sort(
          (left, right) =>
            left.name.localeCompare(right.name) ||
            left.rdns.localeCompare(right.rdns) ||
            left.uuid.localeCompare(right.uuid),
        ),
      active: internalActive ? publicActiveWallet(internalActive) : undefined,
      connecting,
      addingChain,
      error,
      discover,
      connect,
      addChain,
      prepareLogin,
      disconnect,
      getActiveWallet,
      isActiveWallet,
      readContract,
      sendTransaction,
      signSIWEChallenge,
      signBillingTypedData,
      waitForBillingTransaction,
    }),
    [
      withBillingChain,
      connect,
      addChain,
      prepareLogin,
      addingChain,
      connecting,
      disconnect,
      discover,
      error,
      getActiveWallet,
      internalActive,
      isActiveWallet,
      providersByID,
      readContract,
      sendTransaction,
      signSIWEChallenge,
      signBillingTypedData,
      waitForBillingTransaction,
    ],
  );

  return (
    <WalletContext.Provider value={value}>
      {restorationFailure && <BillingRestorationWarning error={restorationFailure} />}
      {children}
    </WalletContext.Provider>
  );
}

function BillingRestorationWarning({ error }: { error: BillingChainError }) {
  const { t } = useTranslation();
  return (
    <Message type="error">
      {t("wallet.errors.restoreFailed", { network: error.targetChainID })}
    </Message>
  );
}

function useLoginPreparation({
  activeRef,
  loginPreparationRef,
  billingScopeRef,
  configuredChainID,
  addChainConfig,
  commitActive,
  setError,
}: {
  activeRef: React.RefObject<InternalActiveWallet | undefined>;
  loginPreparationRef: React.RefObject<{ target: string; arrived: boolean } | undefined>;
  billingScopeRef: React.RefObject<unknown>;
  configuredChainID: string | undefined;
  addChainConfig: unknown;
  commitActive: (wallet: InternalActiveWallet | undefined) => InternalActiveWallet | undefined;
  setError: (code: WalletBoundaryErrorCode) => void;
}) {
  return useCallback(
    async (expected: ActiveWallet): Promise<ActiveWallet> => {
      const target = normalizeChainID(configuredChainID);
      if (!target) throw new WalletBoundaryError("CHAIN_UNAVAILABLE");
      const original = activeRef.current;
      if (!original || !walletIdentityMatches(original, expected))
        throw new WalletBoundaryError("SESSION_CHANGED");
      if (loginPreparationRef.current || billingScopeRef.current)
        throw new WalletBoundaryError("INVALID_REQUEST");
      const scope = { target, arrived: false };
      loginPreparationRef.current = scope;
      const assertPreparation = () => {
        if (loginPreparationRef.current !== scope || activeRef.current !== original)
          throw new WalletBoundaryError("SESSION_CHANGED");
      };
      const readChain = async () => {
        const response = await requestProvider(original.detail.provider, { method: "eth_chainId" });
        assertPreparation();
        const chain = normalizeProviderChainID(response);
        if (!chain) throw new WalletBoundaryError("INVALID_PROVIDER_RESPONSE");
        if (scope.arrived && chain !== target) throw new WalletBoundaryError("SESSION_CHANGED");
        if (chain !== original.chainID) scope.arrived = true;
        return chain;
      };
      const switchChain = async () => {
        // Inspect only the bounded top-level code before sanitizing provider errors.
        const result = await requestProvider(
          original.detail.provider,
          {
            method: "wallet_switchEthereumChain",
            params: [{ chainId: toHex(BigInt(target)) }],
          },
          true,
        );
        assertPreparation();
        if (result !== null) throw new WalletBoundaryError("INVALID_PROVIDER_RESPONSE");
      };
      const invalidate = () => {
        if (loginPreparationRef.current === scope) {
          commitActive(undefined);
          setError("SESSION_CHANGED");
        }
      };
      const onChain = (value: unknown) => {
        const chain = normalizeProviderChainID(value);
        if (chain === target) scope.arrived = true;
        else if (!chain || scope.arrived || chain !== original.chainID) invalidate();
      };
      const onAccounts = (value: unknown) => {
        if (parseAccounts(value)?.[0] !== original.account) invalidate();
      };
      try {
        // Connection may have just completed, before React installs its listeners.
        original.detail.provider.on("chainChanged", onChain);
        original.detail.provider.on("accountsChanged", onAccounts);
        original.detail.provider.on("disconnect", invalidate);
        const initialChain = await readChain();
        if (initialChain !== original.chainID) throw new WalletBoundaryError("SESSION_CHANGED");
        if (initialChain !== target) {
          try {
            await switchChain();
          } catch (cause) {
            assertPreparation();
            if (!(cause instanceof UnknownWalletChainError)) throw cause;
            const parameter = buildAddEthereumChainParameter(addChainConfig);
            if (normalizeProviderChainID(parameter.chainId) !== target)
              throw new WalletBoundaryError("CHAIN_UNAVAILABLE");
            const result = await requestProvider(original.detail.provider, {
              method: "wallet_addEthereumChain",
              params: [parameter],
            });
            assertPreparation();
            if (result !== null) throw new WalletBoundaryError("INVALID_PROVIDER_RESPONSE");
            if ((await readChain()) !== target) await switchChain();
          }
        }
        if ((await readChain()) !== target) throw new WalletBoundaryError("CHAIN_MISMATCH");
        scope.arrived = true;
        const accounts = parseAccounts(
          await requestProvider(original.detail.provider, {
            method: "eth_accounts",
          }),
        );
        assertPreparation();
        if (!accounts) throw new WalletBoundaryError("INVALID_PROVIDER_RESPONSE");
        if (accounts[0] !== original.account) throw new WalletBoundaryError("ACCOUNT_CHANGED");
        const prepared =
          original.chainID === target ? original : commitActive({ ...original, chainID: target });
        return publicActiveWallet(prepared!);
      } catch (cause) {
        const error = toWalletBoundaryError(cause);
        if (loginPreparationRef.current === scope) {
          // Failed account validation or a partial switch must not retain stale identity.
          if (scope.arrived) commitActive(undefined);
          setError(error.code);
        }
        throw error;
      } finally {
        removeProviderListener(original.detail.provider, "chainChanged", onChain);
        removeProviderListener(original.detail.provider, "accountsChanged", onAccounts);
        removeProviderListener(original.detail.provider, "disconnect", invalidate);
        if (loginPreparationRef.current === scope) loginPreparationRef.current = undefined;
      }
    },
    [
      activeRef,
      loginPreparationRef,
      billingScopeRef,
      commitActive,
      configuredChainID,
      addChainConfig,
      setError,
    ],
  );
}

function useBillingChain({
  activeRef,
  billingScopeRef,
  commitActive,
  configuredChainID,
  requireProvider,
  setError,
  setRestorationFailure,
}: {
  activeRef: React.RefObject<InternalActiveWallet | undefined>;
  billingScopeRef: React.RefObject<
    { original: InternalActiveWallet; expectedChain?: string; paymentChain: string } | undefined
  >;
  commitActive: (wallet: InternalActiveWallet | undefined, controlled?: boolean) => void;
  configuredChainID: string | undefined;
  requireProvider: (chainID: string | undefined) => Promise<InternalActiveWallet>;
  setRestorationFailure: (error: BillingChainError) => void;
  setError: (error: WalletBoundaryError["code"] | undefined) => void;
}) {
  return useCallback(
    async <T,>(network: string, operation: (wallet: ActiveWallet) => Promise<T>): Promise<T> => {
      if (billingScopeRef.current) throw new WalletBoundaryError("INVALID_REQUEST");
      const original = await requireProvider(configuredChainID);
      const { getBillingConfig } = await import("@/api/billing");
      const config = await getBillingConfig();
      assertCurrentWallet(activeRef.current, original);
      if (
        !config.x402_topups_enabled ||
        config.payment_chains.every((chain) => chain.network !== network) ||
        !/^eip155:[1-9][0-9]*$/u.test(network) ||
        !normalizeChainID(network.slice("eip155:".length))
      ) {
        throw new WalletBoundaryError("INVALID_REQUEST");
      }
      const paymentChain = network.slice("eip155:".length);
      if (paymentChain === original.chainID) return operation(publicActiveWallet(original));
      if (billingScopeRef.current) throw new WalletBoundaryError("INVALID_REQUEST");
      const scope = { original, paymentChain, expectedChain: undefined as string | undefined };
      billingScopeRef.current = scope;
      const assertScope = () => {
        const current = activeRef.current;
        if (
          billingScopeRef.current !== scope ||
          !current ||
          current.detail.provider !== original.detail.provider ||
          current.account !== original.account
        ) {
          throw new WalletBoundaryError("SESSION_CHANGED");
        }
        return current;
      };
      const switchTo = async (chainID: string) => {
        assertScope();
        scope.expectedChain = chainID;
        const result = await requestProvider(original.detail.provider, {
          method: "wallet_switchEthereumChain",
          params: [{ chainId: toHex(BigInt(chainID)) }],
        });
        const current = assertScope();
        if (result !== null) throw new WalletBoundaryError("INVALID_PROVIDER_RESPONSE");
        const observed = await requestProvider(original.detail.provider, { method: "eth_chainId" });
        assertScope();
        if (normalizeProviderChainID(observed) !== chainID)
          throw new WalletBoundaryError("CHAIN_MISMATCH");
        // A provider may omit chainChanged. Commit the verified transition once.
        if (scope.expectedChain === chainID) {
          scope.expectedChain = undefined;
          commitActive({ ...current, chainID }, true);
        }
        const selected = await requireProvider(chainID);
        assertScope();
        return publicActiveWallet(selected);
      };
      try {
        let selected: ActiveWallet;
        try {
          selected = await switchTo(paymentChain);
        } catch (cause) {
          throw new BillingChainError("entry", paymentChain, toWalletBoundaryError(cause).code);
        }
        const result = await operation(selected);
        assertScope();
        return result;
      } finally {
        if (billingScopeRef.current === scope) {
          try {
            const current = assertScope();
            await requireProvider(current.chainID);
            assertScope();
            if (current.chainID !== original.chainID) await switchTo(original.chainID);
            else await requireProvider(original.chainID);
          } catch (cause) {
            setRestorationFailure(
              new BillingChainError("restore", original.chainID, toWalletBoundaryError(cause).code),
            );
            commitActive(undefined);
            setError(undefined);
          } finally {
            billingScopeRef.current = undefined;
          }
        }
      }
    },
    [
      activeRef,
      billingScopeRef,
      commitActive,
      configuredChainID,
      requireProvider,
      setError,
      setRestorationFailure,
    ],
  );
}

function useWalletOperations({
  activeRef,
  configuredChainID,
  requestActiveProvider,
  requireProvider,
  markTransactionOutcomeUnknown,
  billingScopeRef,
}: {
  billingScopeRef: React.RefObject<{ paymentChain: string } | undefined>;
  activeRef: React.RefObject<InternalActiveWallet | undefined>;
  configuredChainID: string | undefined;
  requestActiveProvider: (
    wallet: InternalActiveWallet,
    arguments_: EIP1193RequestArguments,
  ) => Promise<unknown>;
  requireProvider: (expectedChainID: string | undefined) => Promise<InternalActiveWallet>;
  markTransactionOutcomeUnknown: (wallet: InternalActiveWallet) => void;
}) {
  const readContract = useCallback(
    async (call: ContractCall, expectedChainID: string | undefined) => {
      assertContractCall(call);
      const wallet = await requireProvider(expectedChainID);
      const result = await requestActiveProvider(wallet, {
        method: "eth_call",
        params: [
          {
            to: getAddress(call.to),
            data: call.data,
            from: wallet.account,
            chainId: toHex(BigInt(wallet.chainID)),
            ...(call.value === undefined ? {} : { value: call.value }),
          },
          "latest",
        ],
      });
      assertCompletedWalletOperation(activeRef.current, wallet);
      if (!isContractResult(result)) {
        throw new WalletBoundaryError("INVALID_PROVIDER_RESPONSE");
      }
      return result;
    },
    [activeRef, requestActiveProvider, requireProvider],
  );

  const sendTransaction = useCallback(
    async (transaction: ContractTransaction, expectedChainID: string | undefined) => {
      assertContractCall(transaction);
      const wallet = await requireProvider(expectedChainID);
      let result: unknown;
      try {
        result = await requestActiveProvider(wallet, {
          method: "eth_sendTransaction",
          params: [
            {
              to: getAddress(transaction.to),
              data: transaction.data,
              from: wallet.account,
              chainId: toHex(BigInt(wallet.chainID)),
              ...(transaction.value === undefined ? {} : { value: transaction.value }),
            },
          ],
        });
      } catch (cause) {
        const boundaryError = toWalletBoundaryError(cause);
        if (
          boundaryError.code === "REQUEST_FAILED" ||
          boundaryError.code === "PROVIDER_DISCONNECTED"
        ) {
          markTransactionOutcomeUnknown(wallet);
          throw new WalletBoundaryError("TRANSACTION_OUTCOME_UNKNOWN", boundaryError.revertData);
        }
        throw boundaryError;
      }
      try {
        assertCompletedWalletOperation(activeRef.current, wallet);
      } catch (cause) {
        const boundaryError = toWalletBoundaryError(cause);
        if (boundaryError.code === "SESSION_CHANGED") {
          markTransactionOutcomeUnknown(wallet);
          throw new WalletBoundaryError("TRANSACTION_OUTCOME_UNKNOWN");
        }
        throw boundaryError;
      }
      if (!isTransactionHash(result)) {
        markTransactionOutcomeUnknown(wallet);
        throw new WalletBoundaryError("TRANSACTION_OUTCOME_UNKNOWN");
      }
      return result;
    },
    [activeRef, markTransactionOutcomeUnknown, requestActiveProvider, requireProvider],
  );

  const signSIWEChallenge = useCallback(
    async (challenge: AuthChallenge, expected: ActiveWallet) => {
      const { encodeCanonicalSIWEChallenge } = await import("./siwe");
      const wallet = await requireProvider(configuredChainID);
      assertExpectedWalletIdentity(wallet, expected);
      const encodedMessage = encodeCanonicalSIWEChallenge(
        challenge,
        wallet.account,
        configuredChainID,
      );
      const result = await requestActiveProvider(wallet, {
        method: "personal_sign",
        params: [encodedMessage, wallet.account],
      });
      // Re-run the bounded account and chain checks after signing. This catches
      // silent provider drift as well as event-driven revision changes.
      assertCompletedWalletOperation(activeRef.current, wallet);
      const completed = await requireProvider(configuredChainID);
      assertCompletedWalletOperation(completed, wallet);
      assertExpectedWalletIdentity(completed, expected);
      if (!isWalletSignature(result)) {
        throw new WalletBoundaryError("INVALID_PROVIDER_RESPONSE");
      }
      return result;
    },
    [activeRef, configuredChainID, requestActiveProvider, requireProvider],
  );

  const signBillingTypedData = useCallback(
    async (typedData: BillingTypedData, binding: BillingSigningBinding, expected: ActiveWallet) => {
      const { encodeBillingTypedData } = await import("./billing");
      const wallet = await requireProvider(
        billingScopeRef.current?.paymentChain ?? configuredChainID,
      );
      assertExpectedWalletIdentity(wallet, expected);
      if (binding.account !== wallet.account || binding.chainID !== wallet.chainID) {
        throw new WalletBoundaryError("INVALID_REQUEST");
      }
      const encoded = encodeBillingTypedData(typedData, binding);
      const result = await requestActiveProvider(wallet, {
        method: "eth_signTypedData_v4",
        params: [wallet.account, encoded],
      });
      assertCompletedWalletOperation(activeRef.current, wallet);
      const completed = await requireProvider(
        billingScopeRef.current?.paymentChain ?? configuredChainID,
      );
      assertCompletedWalletOperation(completed, wallet);
      assertExpectedWalletIdentity(completed, expected);
      if (!isWalletSignature(result)) {
        throw new WalletBoundaryError("INVALID_PROVIDER_RESPONSE");
      }
      return result;
    },
    [activeRef, billingScopeRef, configuredChainID, requestActiveProvider, requireProvider],
  );

  const waitForBillingTransaction = useCallback(
    async (transactionHash: Hex, expected: ActiveWallet) => {
      if (!isTransactionHash(transactionHash)) {
        throw new WalletBoundaryError("INVALID_REQUEST");
      }
      const wallet = await requireProvider(
        billingScopeRef.current?.paymentChain ?? configuredChainID,
      );
      assertExpectedWalletIdentity(wallet, expected);
      for (let attempt = 0; attempt < 120; attempt += 1) {
        const receipt = await requestActiveProvider(wallet, {
          method: "eth_getTransactionReceipt",
          params: [transactionHash],
        });
        assertCompletedWalletOperation(activeRef.current, wallet);
        if (receipt !== null) {
          if (typeof receipt !== "object" || !("status" in receipt) || receipt.status !== "0x1") {
            throw new WalletBoundaryError("TRANSACTION_OUTCOME_UNKNOWN");
          }
          const completed = await requireProvider(
            billingScopeRef.current?.paymentChain ?? configuredChainID,
          );
          assertExpectedWalletIdentity(completed, expected);
          return;
        }
        await new Promise((resolve) => globalThis.setTimeout(resolve, 1_000));
      }
      throw new WalletBoundaryError("TRANSACTION_OUTCOME_UNKNOWN");
    },
    [activeRef, billingScopeRef, configuredChainID, requestActiveProvider, requireProvider],
  );

  return {
    readContract,
    sendTransaction,
    signSIWEChallenge,
    signBillingTypedData,
    waitForBillingTransaction,
  };
}

class UnknownWalletChainError extends WalletBoundaryError {
  constructor() {
    super("CHAIN_UNAVAILABLE");
  }
}

async function requestProvider(
  provider: EIP1193Provider,
  arguments_: EIP1193RequestArguments,
  allowUnknownChain = false,
): Promise<unknown> {
  try {
    return await provider.request(arguments_);
  } catch (cause) {
    if (allowUnknownChain && providerErrorCode(cause) === 4902) throw new UnknownWalletChainError();
    throw toWalletBoundaryError(cause);
  }
}

function parseAccounts(value: unknown): Address[] | undefined {
  try {
    if (!Array.isArray(value)) return undefined;
    const length = value.length;
    if (length > MAX_PROVIDER_ACCOUNTS) return undefined;
    const accounts: Address[] = [];
    for (let index = 0; index < length; index += 1) {
      const candidate: unknown = value[index];
      if (typeof candidate !== "string" || candidate.length !== 42 || !isAddress(candidate)) {
        return undefined;
      }
      accounts.push(getAddress(candidate));
    }
    return accounts;
  } catch {
    return undefined;
  }
}

function assertContractCall(call: ContractCall): void {
  if (
    !isAddress(call.to) ||
    !isContractCalldata(call.data) ||
    (call.value !== undefined && !isUint256Quantity(call.value))
  ) {
    throw new WalletBoundaryError("INVALID_REQUEST");
  }
}

function assertCurrentWallet(
  current: InternalActiveWallet | undefined,
  requested: InternalActiveWallet,
): void {
  if (current === requested) return;
  if (current?.detail.provider !== requested.detail.provider) {
    throw new WalletBoundaryError("NOT_CONNECTED");
  }
  if (current.account !== requested.account) {
    throw new WalletBoundaryError("ACCOUNT_CHANGED");
  }
  if (current.chainID !== requested.chainID) {
    throw new WalletBoundaryError("CHAIN_MISMATCH");
  }
  throw new WalletBoundaryError("SESSION_CHANGED");
}

function assertCompletedWalletOperation(
  current: InternalActiveWallet | undefined,
  requested: InternalActiveWallet,
): void {
  if (current !== requested) {
    throw new WalletBoundaryError("SESSION_CHANGED");
  }
}

function publicActiveWallet(wallet: InternalActiveWallet): ActiveWallet {
  return Object.freeze({
    uuid: wallet.detail.info.uuid,
    name: wallet.detail.info.name,
    account: wallet.account,
    chainID: wallet.chainID,
    revision: wallet.revision,
    authRevision: wallet.authRevision,
    authChainID: wallet.authChainID,
  });
}

function walletIdentityMatches(current: InternalActiveWallet, expected: ActiveWallet): boolean {
  return (
    current.detail.info.uuid === expected.uuid &&
    current.detail.info.name === expected.name &&
    current.account === expected.account &&
    current.chainID === expected.chainID &&
    current.revision === expected.revision
  );
}

function assertExpectedWalletIdentity(current: InternalActiveWallet, expected: ActiveWallet): void {
  if (!walletIdentityMatches(current, expected)) {
    throw new WalletBoundaryError("SESSION_CHANGED");
  }
}

function removeProviderListener(
  provider: EIP1193Provider,
  event: Parameters<EIP1193Provider["removeListener"]>[0],
  listener: (value: unknown) => void,
): void {
  try {
    provider.removeListener(event, listener);
  } catch {
    // A hostile or broken provider must not break React cleanup.
  }
}

export function useWallet(): WalletContextValue {
  const context = useContext(WalletContext);
  if (!context) throw new Error("useWallet must be used inside WalletProvider");
  return context;
}

// Authentication survives only the provider-owned, explicitly expected payment
// transitions; operation identity always retains the actual chain and revision.
export function authenticationWallet(wallet: ActiveWallet | undefined): ActiveWallet | undefined {
  return wallet
    ? {
        ...wallet,
        chainID: wallet.authChainID ?? wallet.chainID,
        revision: wallet.authRevision ?? wallet.revision,
      }
    : undefined;
}
