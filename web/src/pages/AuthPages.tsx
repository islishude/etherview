import { type FormEvent, useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { getAddress, isAddress } from "viem";

import {
  listAdminUsers,
  type AdminUserUpdate,
  type User,
} from "@/api/auth";
import { usePublicConfig } from "@/api/hooks";
import { formatTimestamp, shorten } from "@/components/format";
import {
  authErrorTranslationKey,
  useAuth,
} from "@/auth/AuthProvider";
import { chainsMatch } from "@/wallet/eip6963";
import { useWallet } from "@/wallet/WalletProvider";
import { PersonalBillingHistory } from "./BillingPages";
import { Page } from "./pages";

const ADMIN_PAGE_SIZE = 25;

export function AccountPage() {
  const { i18n, t } = useTranslation();
  const auth = useAuth();
  const wallet = useWallet();
  const publicConfig = usePublicConfig();
  const [displayName, setDisplayName] = useState("");
  const [profileError, setProfileError] = useState<string>();
  const [profileSaved, setProfileSaved] = useState(false);
  const locale = i18n.resolvedLanguage ?? "en";
  const sessionUser = auth.session.user ?? undefined;
  const expectedChainID = publicConfig.data?.chain_id;
  const billingEnabled =
    publicConfig.data?.features.x402_billing === true;
  const walletOnChain =
    Boolean(wallet.active) &&
    chainsMatch(wallet.active?.chainID, expectedChainID);
  const walletMatchesUser =
    Boolean(wallet.active && sessionUser) &&
    addressesMatch(wallet.active?.account, sessionUser?.address) &&
    chainsMatch(wallet.active?.chainID, sessionUser?.chain_id);

  useEffect(() => {
    setDisplayName(sessionUser?.display_name ?? "");
  }, [sessionUser?.display_name]);

  useEffect(() => {
    setProfileError(undefined);
    setProfileSaved(false);
  }, [sessionUser?.id]);

  const submitProfile = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setProfileSaved(false);
    const normalized = normalizeDisplayName(displayName);
    if (!normalized.valid) {
      setProfileError(t("auth.profile.invalidDisplayName"));
      return;
    }
    setProfileError(undefined);
    try {
      await auth.updateDisplayName(normalized.value);
      setDisplayName(normalized.value ?? "");
      setProfileSaved(true);
    } catch {
      // AuthProvider maps the boundary to a stable code. Never render nested
      // API, database, or transport text from the thrown value.
    }
  };

  return (
    <Page title={t("auth.account.title")} description={t("auth.account.description")}>
      {!auth.enabled && !auth.loading ? (
        <UnavailablePanel />
      ) : (
        <>
          <section
            className="identity-grid"
            aria-label={t("auth.account.identityStatus")}
          >
            <article className="panel identity-card">
              <div className="identity-card-heading">
                <span
                  className={wallet.active ? "status-dot success" : "status-dot"}
                  aria-hidden="true"
                />
                <h2>{t("auth.walletState.title")}</h2>
              </div>
              <strong>
                {wallet.active
                  ? t("auth.walletState.connected")
                  : t("auth.walletState.disconnected")}
              </strong>
              {wallet.active ? (
                <>
                  <code>{wallet.active.account}</code>
                  <p>
                    {walletOnChain
                      ? t("auth.walletState.correctChain", {
                          chain: wallet.active.chainID,
                        })
                      : t("auth.walletState.wrongChain", {
                          expected: expectedChainID ?? "—",
                          actual: wallet.active.chainID,
                        })}
                  </p>
                </>
              ) : (
                <p>{t("auth.walletState.connectHint")}</p>
              )}
            </article>

            <article className="panel identity-card">
              <div className="identity-card-heading">
                <span
                  className={
                    auth.session.authenticated
                      ? "status-dot success"
                      : "status-dot"
                  }
                  aria-hidden="true"
                />
                <h2>{t("auth.sessionState.title")}</h2>
              </div>
              <strong>
                {auth.loading
                  ? t("auth.sessionState.checking")
                  : auth.session.authenticated
                    ? t("auth.sessionState.authenticated")
                    : t("auth.sessionState.anonymous")}
              </strong>
              {auth.session.authenticated && sessionUser ? (
                <>
                  <code>{sessionUser.address}</code>
                  <p>
                    {walletMatchesUser
                      ? t("auth.sessionState.walletMatches")
                      : t("auth.sessionState.independent")}
                  </p>
                </>
              ) : (
                <p>{t("auth.sessionState.loginHint")}</p>
              )}
            </article>
          </section>

          {auth.error && (
            <p className="form-error" role="alert">
              {t(authErrorTranslationKey(auth.error))}
            </p>
          )}

          {!auth.loading && !auth.session.authenticated && (
            <section
              className="panel auth-action-panel"
              aria-labelledby="sign-in-title"
            >
              <div>
                <span className="eyebrow">{t("auth.signIn.eyebrow")}</span>
                <h2 id="sign-in-title">{t("auth.signIn.title")}</h2>
                <p>{t("auth.signIn.description")}</p>
              </div>
              <button
                className="button primary"
                disabled={
                  auth.pending ||
                  !wallet.active ||
                  !walletOnChain
                }
                onClick={() => void auth.login()}
                type="button"
              >
                {auth.pending
                  ? t("auth.signIn.pending")
                  : t("auth.signIn.action")}
              </button>
            </section>
          )}

          {auth.session.authenticated && sessionUser && (
            <div className="account-layout">
              <section
                className="panel profile-panel"
                aria-labelledby="profile-title"
              >
                <div className="panel-heading">
                  <h2 id="profile-title">{t("auth.profile.title")}</h2>
                  <span
                    className={`user-state ${sessionUser.status}`}
                  >
                    {t(`auth.status.${sessionUser.status}`)}
                  </span>
                </div>
                <dl className="profile-details">
                  <div>
                    <dt>{t("auth.fields.address")}</dt>
                    <dd><code>{sessionUser.address}</code></dd>
                  </div>
                  <div>
                    <dt>{t("auth.fields.role")}</dt>
                    <dd>{t(`auth.role.${sessionUser.role}`)}</dd>
                  </div>
                  <div>
                    <dt>{t("auth.fields.expiresAt")}</dt>
                    <dd>
                      <time dateTime={auth.session.expires_at}>
                        {formatTimestamp(auth.session.expires_at ?? "", locale)}
                      </time>
                    </dd>
                  </div>
                  <div>
                    <dt>{t("auth.fields.lastLogin")}</dt>
                    <dd>
                      {sessionUser.last_login_at ? (
                        <time dateTime={sessionUser.last_login_at}>
                          {formatTimestamp(sessionUser.last_login_at, locale)}
                        </time>
                      ) : "—"}
                    </dd>
                  </div>
                </dl>
                <form className="profile-form" onSubmit={submitProfile}>
                  <div className="field-control">
                    <label htmlFor="auth-display-name">
                      {t("auth.profile.displayName")}
                    </label>
                    <input
                      aria-describedby="display-name-hint"
                      autoComplete="nickname"
                      id="auth-display-name"
                      maxLength={256}
                      onChange={(event) => {
                        setDisplayName(event.target.value);
                        setProfileError(undefined);
                        setProfileSaved(false);
                      }}
                      value={displayName}
                    />
                    <small id="display-name-hint">
                      {t("auth.profile.displayNameHint")}
                    </small>
                  </div>
                  {profileError && (
                    <p className="form-error" role="alert">{profileError}</p>
                  )}
                  {profileSaved && (
                    <p className="form-success" role="status">
                      {t("auth.profile.saved")}
                    </p>
                  )}
                  <button
                    className="button primary"
                    disabled={auth.pending}
                    type="submit"
                  >
                    {t("auth.profile.save")}
                  </button>
                </form>
              </section>

              <aside
                className="panel session-panel"
                aria-labelledby="session-actions-title"
              >
                <h2 id="session-actions-title">{t("auth.sessionActions.title")}</h2>
                <p>{t("auth.sessionActions.description")}</p>
                {sessionUser.role === "admin" && (
                  <>
                    <Link
                      className="button secondary inline-button"
                      to="/admin/users"
                    >
                      {t("auth.admin.openUsers")}
                    </Link>
                    {billingEnabled && (
                      <Link
                        className="button secondary inline-button"
                        to="/admin/billing"
                      >
                        {t("billing.admin.openBilling")}
                      </Link>
                    )}
                  </>
                )}
                <button
                  className="button secondary"
                  disabled={auth.pending}
                  onClick={() => void auth.logout()}
                  type="button"
                >
                  {t("auth.sessionActions.logout")}
                </button>
              </aside>
            </div>
          )}
          {billingEnabled &&
            auth.session.authenticated &&
            sessionUser && (
            <PersonalBillingHistory />
          )}
        </>
      )}
    </Page>
  );
}

export function AdminUsersPage() {
  const { i18n, t } = useTranslation();
  const auth = useAuth();
  const publicConfig = usePublicConfig();
  const billingEnabled =
    publicConfig.data?.features.x402_billing === true;
  const [cursors, setCursors] = useState([""]);
  const [announcement, setAnnouncement] = useState<string>();
  const cursor = cursors.at(-1) || undefined;
  const isAdmin =
    auth.session.authenticated &&
    auth.session.user?.status === "active" &&
    auth.session.user.role === "admin";
  const locale = i18n.resolvedLanguage ?? "en";
  const users = useQuery({
    queryKey: ["admin-users", cursor ?? null],
    queryFn: () => listAdminUsers(ADMIN_PAGE_SIZE, cursor),
    enabled: auth.enabled && isAdmin,
    retry: false,
    staleTime: 0,
  });

  useEffect(() => {
    if (!isAdmin) setCursors([""]);
  }, [isAdmin]);

  const mutationComplete = async (message: string) => {
    setAnnouncement(message);
    await users.refetch();
  };

  return (
    <Page title={t("auth.admin.title")} description={t("auth.admin.description")}>
      {!auth.enabled && !auth.loading ? (
        <UnavailablePanel />
      ) : auth.loading ? (
        <p className="query-notice" role="status">{t("auth.sessionState.checking")}</p>
      ) : !auth.session.authenticated ? (
        <AuthGate
          detail={t("auth.admin.authenticationRequired")}
          title={t("auth.errors.authenticationRequired")}
        />
      ) : !isAdmin ? (
        <AuthGate
          detail={t("auth.admin.adminRequired")}
          title={t("auth.errors.adminRequired")}
        />
      ) : (
        <>
          <div className="admin-toolbar">
            <p>{t("auth.admin.page", { page: cursors.length })}</p>
            <div className="admin-toolbar-actions">
              {billingEnabled && (
                <Link className="button secondary" to="/admin/billing">
                  {t("billing.admin.openBilling")}
                </Link>
              )}
              <button
                className="button secondary"
                disabled={users.isFetching}
                onClick={() => void users.refetch()}
                type="button"
              >
                {t("auth.admin.refresh")}
              </button>
            </div>
          </div>
          {auth.error && (
            <p className="form-error" role="alert">
              {t(authErrorTranslationKey(auth.error))}
            </p>
          )}
          {announcement && (
            <p className="form-success" role="status">{announcement}</p>
          )}
          {users.isPending && (
            <p className="query-notice" role="status">
              {t("auth.admin.loading")}
            </p>
          )}
          {users.error && (
            <div className="query-notice degraded" role="alert">
              <span>
                <strong>{t("auth.admin.loadFailed")}</strong>
                <small>{t("auth.admin.loadFailedDetail")}</small>
              </span>
            </div>
          )}
          {users.data?.data.length === 0 && (
            <p className="empty-result" role="status">
              {t("auth.admin.empty")}
            </p>
          )}
          {users.data && users.data.data.length > 0 && (
            <div
              className="admin-user-list"
              aria-label={t("auth.admin.userList")}
            >
              {users.data.data.map((user) => (
                <AdminUserCard
                  currentUserID={auth.session.user?.id}
                  key={user.id}
                  locale={locale}
                  onMutationComplete={mutationComplete}
                  user={user}
                />
              ))}
            </div>
          )}
          {users.data && (
            <nav
              className="cursor-pagination"
              aria-label={t("auth.admin.pagination")}
            >
              <button
                className="button secondary"
                disabled={users.isFetching || cursors.length === 1}
                onClick={() => {
                  setAnnouncement(undefined);
                  setCursors((current) =>
                    current.length > 1 ? current.slice(0, -1) : current,
                  );
                }}
                type="button"
              >
                {t("pagination.previous")}
              </button>
              <span>{t("pagination.page", { page: cursors.length })}</span>
              <button
                className="button secondary"
                disabled={
                  users.isFetching ||
                  !users.data.meta.next_cursor
                }
                onClick={() => {
                  const next = users.data?.meta.next_cursor;
                  if (!next) return;
                  setAnnouncement(undefined);
                  setCursors((current) => [...current, next]);
                }}
                type="button"
              >
                {t("pagination.next")}
              </button>
            </nav>
          )}
        </>
      )}
    </Page>
  );
}

function AdminUserCard({
  currentUserID,
  locale,
  onMutationComplete,
  user,
}: {
  currentUserID?: string;
  locale: string;
  onMutationComplete: (message: string) => Promise<void>;
  user: User;
}) {
  const { t } = useTranslation();
  const auth = useAuth();
  const [role, setRole] = useState(user.role);
  const [status, setStatus] = useState(user.status);
  const changed = role !== user.role || status !== user.status;

  useEffect(() => {
    setRole(user.role);
    setStatus(user.status);
  }, [user.role, user.status]);

  const save = async () => {
    const update: AdminUserUpdate = {};
    if (role !== user.role) update.role = role;
    if (status !== user.status) update.status = status;
    if (Object.keys(update).length === 0) return;
    try {
      await auth.updateUser(user.id, update);
      await onMutationComplete(
        t("auth.admin.updated", { address: shorten(user.address) }),
      );
    } catch {
      // Stable AuthProvider error is rendered by the owning page.
    }
  };

  const revoke = async () => {
    try {
      const count = await auth.revokeSessions(user.id);
      await onMutationComplete(
        t("auth.admin.revoked", {
          address: shorten(user.address),
          count,
        }),
      );
    } catch {
      // Stable AuthProvider error is rendered by the owning page.
    }
  };

  return (
    <article className="panel admin-user-card">
      <header>
        <div>
          <strong>{user.display_name ?? t("auth.profile.noDisplayName")}</strong>
          {user.id === currentUserID && (
            <span className="current-user-label">{t("auth.admin.you")}</span>
          )}
        </div>
        <code title={user.address}>{user.address}</code>
      </header>
      <dl>
        <div>
          <dt>{t("auth.fields.lastLogin")}</dt>
          <dd>
            {user.last_login_at ? (
              <time dateTime={user.last_login_at}>
                {formatTimestamp(user.last_login_at, locale)}
              </time>
            ) : "—"}
          </dd>
        </div>
        <div>
          <dt>{t("auth.fields.createdAt")}</dt>
          <dd>
            <time dateTime={user.created_at}>
              {formatTimestamp(user.created_at, locale)}
            </time>
          </dd>
        </div>
      </dl>
      <div className="admin-user-controls">
        <label className="field-control">
          <span>{t("auth.fields.role")}</span>
          <select
            aria-label={t("auth.admin.roleFor", { address: user.address })}
            disabled={auth.pending}
            onChange={(event) =>
              setRole(event.target.value as User["role"])
            }
            value={role}
          >
            <option value="user">{t("auth.role.user")}</option>
            <option value="admin">{t("auth.role.admin")}</option>
          </select>
        </label>
        <label className="field-control">
          <span>{t("auth.fields.status")}</span>
          <select
            aria-label={t("auth.admin.statusFor", { address: user.address })}
            disabled={auth.pending}
            onChange={(event) =>
              setStatus(event.target.value as User["status"])
            }
            value={status}
          >
            <option value="active">{t("auth.status.active")}</option>
            <option value="disabled">{t("auth.status.disabled")}</option>
          </select>
        </label>
      </div>
      <div className="admin-user-actions">
        <button
          className="button primary"
          disabled={auth.pending || !changed}
          onClick={() => void save()}
          type="button"
        >
          {t("auth.admin.saveUser")}
        </button>
        <button
          className="button secondary"
          disabled={auth.pending}
          onClick={() => void revoke()}
          type="button"
        >
          {t("auth.admin.revokeSessions")}
        </button>
      </div>
    </article>
  );
}

function AuthGate({ detail, title }: { detail: string; title: string }) {
  const { t } = useTranslation();
  return (
    <section className="panel auth-gate" aria-labelledby="auth-gate-title">
      <h2 id="auth-gate-title">{title}</h2>
      <p>{detail}</p>
      <Link className="button primary inline-button" to="/account">
        {t("auth.account.open")}
      </Link>
    </section>
  );
}

function UnavailablePanel() {
  const { t } = useTranslation();
  return (
    <section className="panel auth-gate" aria-labelledby="auth-unavailable-title">
      <h2 id="auth-unavailable-title">{t("auth.unavailable.title")}</h2>
      <p>{t("auth.unavailable.description")}</p>
    </section>
  );
}

function normalizeDisplayName(
  value: string,
): { valid: true; value: string | null } | { valid: false } {
  const normalized = value.trim();
  if (normalized === "") return { valid: true, value: null };
  if (
    [...normalized].length > 64 ||
    new TextEncoder().encode(normalized).length > 256 ||
    /\p{Cc}/u.test(normalized)
  ) {
    return { valid: false };
  }
  return { valid: true, value: normalized };
}

function addressesMatch(
  left: string | undefined,
  right: string | undefined,
): boolean {
  if (!left || !right || !isAddress(left) || !isAddress(right)) return false;
  try {
    return getAddress(left) === getAddress(right);
  } catch {
    return false;
  }
}
