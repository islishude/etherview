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
import { getAddress, isAddress } from "viem";

import {
  createAuthChallenge,
  getAuthSession,
  logoutAuthSession,
  revokeAdminUserSessions,
  type AdminUserUpdate,
  type AuthSession,
  type User,
  updateAdminUser,
  updateCurrentUser,
  verifyAuthChallenge,
} from "@/api/auth";
import { ApiError } from "@/api/client";
import { usePublicConfig } from "@/api/hooks";
import {
  chainsMatch,
  WalletBoundaryError,
  type WalletBoundaryErrorCode,
  walletErrorTranslationKey,
} from "@/wallet/eip6963";
import {
  type ActiveWallet,
  useWallet,
} from "@/wallet/WalletProvider";

export type AuthErrorCode =
  | WalletBoundaryErrorCode
  | "AUTH_UNAVAILABLE"
  | "AUTHENTICATION_REQUIRED"
  | "INVALID_AUTH_RESPONSE"
  | "WALLET_IDENTITY_CHANGED"
  | "challenge_invalid"
  | "challenge_expired"
  | "challenge_consumed"
  | "signature_invalid"
  | "user_disabled"
  | "user_auth_unavailable"
  | "origin_invalid"
  | "csrf_invalid"
  | "authentication_required"
  | "admin_required"
  | "user_not_found"
  | "invalid_auth_request";

interface AuthContextValue {
  enabled: boolean;
  loading: boolean;
  pending: boolean;
  session: AuthSession;
  error?: AuthErrorCode;
  refresh: () => Promise<void>;
  login: (providerUUID?: string) => Promise<void>;
  logout: () => Promise<void>;
  updateDisplayName: (displayName: string | null) => Promise<void>;
  updateUser: (id: string, update: AdminUserUpdate) => Promise<void>;
  revokeSessions: (id: string) => Promise<string>;
  clearError: () => void;
}

const unauthenticatedSession: AuthSession = Object.freeze({ authenticated: false });
const AuthContext = createContext<AuthContextValue | undefined>(undefined);
const UUID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/iu;

interface LoginAttempt {
  connectionObserved: boolean;
  finished: boolean;
  phase: "connecting" | "authenticating";
  providerUUID?: string;
  startedDisconnected: boolean;
  wallet?: ActiveWallet;
}

export function AuthProvider({ children }: PropsWithChildren) {
  const wallet = useWallet();
  const publicConfig = usePublicConfig();
  const enabled = publicConfig.data?.features.user_auth === true;
  const expectedChainID = publicConfig.data?.chain_id;
  const [session, setSession] = useState<AuthSession>(unauthenticatedSession);
  const [loading, setLoading] = useState(true);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<AuthErrorCode>();
  const generationRef = useRef(0);
  const sessionRef = useRef<AuthSession>(unauthenticatedSession);
  const activeWalletRef = useRef(wallet.active);
  const priorWalletIdentityRef = useRef<string | undefined>(undefined);
  const loginAttemptRef = useRef<LoginAttempt | undefined>(undefined);
  activeWalletRef.current = wallet.active;

  const commitSession = useCallback((next: AuthSession) => {
    sessionRef.current = next;
    setSession(next);
  }, []);

  const refresh = useCallback(async () => {
    const generation = generationRef.current + 1;
    generationRef.current = generation;
    setLoading(true);
    setError(undefined);
    if (!enabled || !expectedChainID) {
      commitSession(unauthenticatedSession);
      setLoading(false);
      return;
    }
    let responseCSRF: string | undefined;
    try {
      const response = await getAuthSession();
      responseCSRF = extractCSRFToken(response);
      const next = validateAuthSession(response, expectedChainID);
      if (generationRef.current !== generation) return;
      if (
        next.authenticated &&
        activeWalletRef.current !== undefined &&
        !sessionMatchesWallet(next, activeWalletRef.current)
      ) {
        bestEffortLogout(next);
        commitSession(unauthenticatedSession);
        return;
      }
      commitSession(next);
    } catch (cause) {
      if (responseCSRF) bestEffortLogoutToken(responseCSRF);
      if (generationRef.current === generation) {
        commitSession(unauthenticatedSession);
        setError(toAuthErrorCode(cause));
      }
    } finally {
      if (generationRef.current === generation) setLoading(false);
    }
  }, [commitSession, enabled, expectedChainID]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const walletIdentity = wallet.active
    ? [
        wallet.active.uuid,
        wallet.active.account,
        wallet.active.chainID,
        wallet.active.revision,
      ].join(":")
    : "disconnected";

  useEffect(() => {
    const previous = priorWalletIdentityRef.current;
    priorWalletIdentityRef.current = walletIdentity;
    if (previous === undefined || previous === walletIdentity) return;

    const attempt = loginAttemptRef.current;
    const currentWallet = activeWalletRef.current;
    const firstObservedConnection =
      previous === "disconnected" && currentWallet !== undefined;
    const allowedLoginConnection =
      firstObservedConnection &&
      ((attempt?.phase === "connecting" &&
        currentWallet.uuid === attempt.providerUUID) ||
        (attempt?.phase === "authenticating" &&
          attempt.wallet !== undefined &&
          sameWallet(currentWallet, attempt.wallet)));
    if (allowedLoginConnection) {
      attempt.connectionObserved = true;
      if (attempt.finished) loginAttemptRef.current = undefined;
      return;
    }
    if (firstObservedConnection && attempt === undefined) {
      // WalletProvider intentionally keeps its active provider in memory, so a
      // full page load starts disconnected even when the HttpOnly session
      // Cookie is still valid. The first later connection is an observation,
      // not an identity change: keep an anonymous or matching restored session
      // and let a mismatching connection fall through to revocation.
      const current = sessionRef.current;
      if (!current.authenticated || sessionMatchesWallet(current, currentWallet)) {
        return;
      }
    }

    generationRef.current += 1;
    loginAttemptRef.current = undefined;
    const current = sessionRef.current;
    commitSession(unauthenticatedSession);
    setPending(false);
    setError(undefined);
    bestEffortLogout(current);
  }, [commitSession, walletIdentity]);

  const login = useCallback(async (providerUUID?: string) => {
    if (!enabled || !expectedChainID) {
      setError("AUTH_UNAVAILABLE");
      return;
    }
    if (loginAttemptRef.current) return;
    if (!activeWalletRef.current && !providerUUID) {
      setError("NOT_CONNECTED");
      return;
    }
    if (
      activeWalletRef.current &&
      providerUUID &&
      activeWalletRef.current.uuid !== providerUUID
    ) {
      setError("WALLET_IDENTITY_CHANGED");
      return;
    }
    const generation = generationRef.current + 1;
    generationRef.current = generation;
    const attempt: LoginAttempt = activeWalletRef.current
      ? {
          connectionObserved: true,
          finished: false,
          phase: "authenticating",
          startedDisconnected: false,
          wallet: activeWalletRef.current,
        }
      : {
          connectionObserved: false,
          finished: false,
          phase: "connecting",
          providerUUID,
          startedDisconnected: true,
        };
    loginAttemptRef.current = attempt;
    setPending(true);
    setError(undefined);
    let verified: AuthSession | undefined;
    let verificationCSRF: string | undefined;
    let accepted = false;
    try {
      const selected =
        activeWalletRef.current ?? await wallet.connect(providerUUID!);
      attempt.phase = "authenticating";
      attempt.wallet = selected;
      if (!chainsMatch(selected.chainID, expectedChainID)) {
        throw new WalletBoundaryError("CHAIN_MISMATCH");
      }
      const challenge = validateAuthChallenge(
        await createAuthChallenge(selected.account),
      );
      if (!wallet.isActiveWallet(selected)) {
        throw new AuthBoundaryError("WALLET_IDENTITY_CHANGED");
      }
      const signature = await wallet.signSIWEChallenge(challenge, selected);
      const verificationResponse = await verifyAuthChallenge(
        challenge.challenge_id,
        signature,
      );
      verificationCSRF = extractCSRFToken(verificationResponse);
      verified = validateAuthSession(verificationResponse, expectedChainID);
      if (
        !verified.authenticated ||
        !verified.user ||
        !addressesMatch(verified.user.address, selected.account)
      ) {
        throw new AuthBoundaryError("INVALID_AUTH_RESPONSE");
      }
      if (!wallet.isActiveWallet(selected)) {
        throw new AuthBoundaryError("WALLET_IDENTITY_CHANGED");
      }
      if (generationRef.current !== generation) return;
      commitSession(verified);
      accepted = true;
    } catch (cause) {
      if (generationRef.current === generation) {
        commitSession(unauthenticatedSession);
        setError(toAuthErrorCode(cause));
      }
    } finally {
      // /auth/verify may already have installed an HttpOnly Cookie. A wallet
      // identity/generation race must revoke that server session even though
      // the stale result is never committed to React state.
      if (verificationCSRF && !accepted) {
        bestEffortLogoutToken(verificationCSRF);
      }
      attempt.finished = true;
      if (
        loginAttemptRef.current === attempt &&
        (!attempt.startedDisconnected ||
          attempt.connectionObserved ||
          attempt.wallet === undefined)
      ) {
        loginAttemptRef.current = undefined;
      }
      if (generationRef.current === generation) setPending(false);
    }
  }, [
    commitSession,
    enabled,
    expectedChainID,
    wallet.connect,
    wallet.isActiveWallet,
    wallet.signSIWEChallenge,
  ]);

  const logout = useCallback(async () => {
    const current = sessionRef.current;
    if (!current.authenticated || !current.csrf_token) {
      commitSession(unauthenticatedSession);
      return;
    }
    const generation = generationRef.current + 1;
    generationRef.current = generation;
    setPending(true);
    setError(undefined);
    try {
      await logoutAuthSession(current.csrf_token);
      if (generationRef.current === generation) {
        commitSession(unauthenticatedSession);
      }
    } catch (cause) {
      if (generationRef.current === generation) setError(toAuthErrorCode(cause));
    } finally {
      if (generationRef.current === generation) setPending(false);
    }
  }, [commitSession]);

  const updateDisplayName = useCallback(async (displayName: string | null) => {
    const current = requireAuthenticated(sessionRef.current);
    const generation = generationRef.current + 1;
    generationRef.current = generation;
    setPending(true);
    setError(undefined);
    try {
      const user = validateUpdatedUser(
        await updateCurrentUser(current.csrf_token, displayName),
        current.user.chain_id,
        current.user.id,
        current.user.address,
      );
      if ((user.display_name ?? null) !== displayName) {
        throw new AuthBoundaryError("INVALID_AUTH_RESPONSE");
      }
      if (generationRef.current === generation) {
        commitSession({ ...current, user });
      }
    } catch (cause) {
      if (generationRef.current === generation) setError(toAuthErrorCode(cause));
      throw cause;
    } finally {
      if (generationRef.current === generation) setPending(false);
    }
  }, [commitSession]);

  const updateUser = useCallback(async (id: string, update: AdminUserUpdate) => {
    const current = requireAdmin(sessionRef.current);
    const generation = generationRef.current + 1;
    generationRef.current = generation;
    setPending(true);
    setError(undefined);
    try {
      const user = validateUpdatedUser(
        await updateAdminUser(current.csrf_token, id, update),
        current.user.chain_id,
        id,
        current.user.id === id ? current.user.address : undefined,
      );
      if (
        generationRef.current === generation &&
        current.user?.id === user.id
      ) {
        commitSession(
          user.status === "active"
            ? { ...current, user }
            : unauthenticatedSession,
        );
      }
    } catch (cause) {
      if (generationRef.current === generation) {
        setError(toAuthErrorCode(cause));
      }
      throw cause;
    } finally {
      if (generationRef.current === generation) setPending(false);
    }
  }, [commitSession]);

  const revokeSessions = useCallback(async (id: string) => {
    const current = requireAdmin(sessionRef.current);
    const generation = generationRef.current + 1;
    generationRef.current = generation;
    setPending(true);
    setError(undefined);
    try {
      const count = await revokeAdminUserSessions(current.csrf_token, id);
      if (!/^(?:0|[1-9][0-9]*)$/u.test(count) || count.length > 78) {
        throw new AuthBoundaryError("INVALID_AUTH_RESPONSE");
      }
      if (generationRef.current === generation && current.user?.id === id) {
        commitSession(unauthenticatedSession);
      }
      return count;
    } catch (cause) {
      if (generationRef.current === generation) {
        setError(toAuthErrorCode(cause));
      }
      throw cause;
    } finally {
      if (generationRef.current === generation) setPending(false);
    }
  }, [commitSession]);

  const value = useMemo<AuthContextValue>(
    () => ({
      enabled,
      loading: loading || publicConfig.isPending,
      pending,
      session,
      error,
      refresh,
      login,
      logout,
      updateDisplayName,
      updateUser,
      revokeSessions,
      clearError: () => setError(undefined),
    }),
    [
      enabled,
      error,
      loading,
      login,
      logout,
      pending,
      publicConfig.isPending,
      refresh,
      revokeSessions,
      session,
      updateDisplayName,
      updateUser,
    ],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

function validateAuthSession(value: unknown, expectedChainID: string): AuthSession {
  if (
    typeof value !== "object" ||
    value === null ||
    !("authenticated" in value) ||
    typeof value.authenticated !== "boolean"
  ) {
    throw new AuthBoundaryError("INVALID_AUTH_RESPONSE");
  }
  if (!value.authenticated) return unauthenticatedSession;
  const candidate = value as AuthSession;
  if (
    !isValidUserRecord(candidate.user, expectedChainID) ||
    candidate.user.status !== "active" ||
    typeof candidate.expires_at !== "string" ||
    !isFutureTimestamp(candidate.expires_at) ||
    typeof candidate.csrf_token !== "string" ||
    !/^[A-Za-z0-9_-]{43}$/u.test(candidate.csrf_token)
  ) {
    throw new AuthBoundaryError("INVALID_AUTH_RESPONSE");
  }
  return candidate;
}

function requireAuthenticated(session: AuthSession): AuthSession & {
  authenticated: true;
  csrf_token: string;
  expires_at: string;
  user: User;
} {
  if (
    !session.authenticated ||
    !session.csrf_token ||
    !session.expires_at ||
    !session.user
  ) {
    throw new AuthBoundaryError("AUTHENTICATION_REQUIRED");
  }
  return session as AuthSession & {
    authenticated: true;
    csrf_token: string;
    expires_at: string;
    user: User;
  };
}

function requireAdmin(session: AuthSession) {
  const authenticated = requireAuthenticated(session);
  if (authenticated.user?.role !== "admin") {
    throw new AuthBoundaryError("admin_required");
  }
  return authenticated;
}

function sameWallet(
  current: ReturnType<typeof useWallet>["active"],
  expected: NonNullable<ReturnType<typeof useWallet>["active"]>,
) {
  return Boolean(
    current &&
      current.uuid === expected.uuid &&
      current.account === expected.account &&
      current.chainID === expected.chainID &&
      current.revision === expected.revision,
  );
}

function isValidUserRecord(
  value: AuthSession["user"],
  expectedChainID: string,
): value is NonNullable<AuthSession["user"]> {
  if (
    !value ||
    value.chain_id !== expectedChainID ||
    (value.status !== "active" && value.status !== "disabled") ||
    (value.role !== "user" && value.role !== "admin") ||
    typeof value.id !== "string" ||
    !UUID_PATTERN.test(value.id) ||
    typeof value.address !== "string" ||
    !isAddress(value.address) ||
    getAddress(value.address) !== value.address ||
    typeof value.created_at !== "string" ||
    !Number.isFinite(Date.parse(value.created_at)) ||
    typeof value.updated_at !== "string" ||
    !Number.isFinite(Date.parse(value.updated_at)) ||
    (value.last_login_at !== undefined &&
      !Number.isFinite(Date.parse(value.last_login_at)))
  ) {
    return false;
  }
  return isValidDisplayName(value.display_name);
}

function validateUpdatedUser(
  value: User,
  expectedChainID: string,
  expectedID: string,
  expectedAddress?: string,
): User {
  if (
    !isValidUserRecord(value, expectedChainID) ||
    value.id !== expectedID ||
    (expectedAddress !== undefined &&
      !addressesMatch(value.address, expectedAddress))
  ) {
    throw new AuthBoundaryError("INVALID_AUTH_RESPONSE");
  }
  return value;
}

function validateAuthChallenge(value: unknown): {
  challenge_id: string;
  expires_at: string;
  message: string;
} {
  if (
    typeof value !== "object" ||
    value === null ||
    !("challenge_id" in value) ||
    typeof value.challenge_id !== "string" ||
    !UUID_PATTERN.test(value.challenge_id) ||
    !("message" in value) ||
    typeof value.message !== "string" ||
    value.message.length === 0 ||
    new TextEncoder().encode(value.message).length > 4096 ||
    !("expires_at" in value) ||
    typeof value.expires_at !== "string" ||
    !isFutureTimestamp(value.expires_at)
  ) {
    throw new AuthBoundaryError("INVALID_AUTH_RESPONSE");
  }
  return {
    challenge_id: value.challenge_id,
    expires_at: value.expires_at,
    message: value.message,
  };
}

function isValidDisplayName(value: string | null | undefined): boolean {
  if (value === null || value === undefined) return true;
  return (
    typeof value === "string" &&
    value.length > 0 &&
    value.trim() === value &&
    [...value].length <= 64 &&
    new TextEncoder().encode(value).length <= 256 &&
    !/\p{Cc}/u.test(value)
  );
}

function isFutureTimestamp(value: string): boolean {
  const timestamp = Date.parse(value);
  return Number.isFinite(timestamp) && timestamp > Date.now();
}

function addressesMatch(left: string, right: string): boolean {
  try {
    return isAddress(left) && getAddress(left) === getAddress(right);
  } catch {
    return false;
  }
}

function sessionMatchesWallet(
  session: AuthSession,
  wallet: ReturnType<typeof useWallet>["active"],
): boolean {
  return Boolean(
    session.authenticated &&
      session.user &&
      wallet &&
      addressesMatch(session.user.address, wallet.account) &&
      session.user.chain_id === wallet.chainID,
  );
}

function bestEffortLogout(value: AuthSession): void {
  if (!value.authenticated || !value.csrf_token) return;
  bestEffortLogoutToken(value.csrf_token);
}

function bestEffortLogoutToken(csrfToken: string): void {
  void logoutAuthSession(csrfToken).catch(() => {
    // Identity changes clear browser authority immediately. Server revocation
    // is best effort and hostile transport/error text is never rendered.
  });
}

function extractCSRFToken(value: unknown): string | undefined {
  if (
    typeof value !== "object" ||
    value === null ||
    !("authenticated" in value) ||
    value.authenticated !== true ||
    !("csrf_token" in value) ||
    typeof value.csrf_token !== "string" ||
    !/^[A-Za-z0-9_-]{43}$/u.test(value.csrf_token)
  ) {
    return undefined;
  }
  return value.csrf_token;
}

class AuthBoundaryError extends Error {
  readonly code: AuthErrorCode;

  constructor(code: AuthErrorCode) {
    super(code);
    this.name = "AuthBoundaryError";
    this.code = code;
  }
}

function toAuthErrorCode(cause: unknown): AuthErrorCode {
  if (cause instanceof AuthBoundaryError || cause instanceof WalletBoundaryError) {
    return cause.code;
  }
  if (cause instanceof ApiError && isAuthAPIErrorCode(cause.code)) {
    return cause.code;
  }
  return "AUTH_UNAVAILABLE";
}

function isAuthAPIErrorCode(value: string): value is AuthErrorCode {
  return AUTH_API_ERROR_CODES.has(value);
}

const AUTH_API_ERROR_CODES = new Set<string>([
  "challenge_invalid",
  "challenge_expired",
  "challenge_consumed",
  "signature_invalid",
  "user_disabled",
  "user_auth_unavailable",
  "origin_invalid",
  "csrf_invalid",
  "authentication_required",
  "admin_required",
  "user_not_found",
  "invalid_auth_request",
]);

export function authErrorTranslationKey(code: AuthErrorCode): string {
  if (isWalletErrorCode(code)) return walletErrorTranslationKey(code);
  switch (code) {
    case "AUTH_UNAVAILABLE":
    case "user_auth_unavailable":
      return "auth.errors.unavailable";
    case "AUTHENTICATION_REQUIRED":
    case "authentication_required":
      return "auth.errors.authenticationRequired";
    case "INVALID_AUTH_RESPONSE":
      return "auth.errors.invalidResponse";
    case "WALLET_IDENTITY_CHANGED":
      return "auth.errors.walletChanged";
    case "challenge_invalid":
      return "auth.errors.challengeInvalid";
    case "challenge_expired":
      return "auth.errors.challengeExpired";
    case "challenge_consumed":
      return "auth.errors.challengeConsumed";
    case "signature_invalid":
      return "auth.errors.signatureInvalid";
    case "user_disabled":
      return "auth.errors.disabled";
    case "origin_invalid":
      return "auth.errors.originInvalid";
    case "csrf_invalid":
      return "auth.errors.csrfInvalid";
    case "admin_required":
      return "auth.errors.adminRequired";
    case "user_not_found":
      return "auth.errors.userNotFound";
    case "invalid_auth_request":
      return "auth.errors.invalidRequest";
  }
}

function isWalletErrorCode(code: AuthErrorCode): code is WalletBoundaryErrorCode {
  return new Set<string>([
    "NOT_CONNECTED",
    "CHAIN_UNAVAILABLE",
    "CHAIN_MISMATCH",
    "ACCOUNT_CHANGED",
    "SESSION_CHANGED",
    "TRANSACTION_OUTCOME_UNKNOWN",
    "PROVIDER_DISCONNECTED",
    "INVALID_PROVIDER_RESPONSE",
    "INVALID_REQUEST",
    "USER_REJECTED",
    "REQUEST_FAILED",
  ]).has(code);
}

export function useAuth(): AuthContextValue {
  const context = useContext(AuthContext);
  if (!context) throw new Error("useAuth must be used inside AuthProvider");
  return context;
}
