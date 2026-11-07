import { AlertDialog } from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import "@/styles/account.css";
import { Card } from "@/components/ui/card";
import { NavigationTabs, NavigationTab, Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Alert } from "@/components/ui/alert";
import { Pagination } from "@/components/ui/pagination";
import { Message } from "@/components/Message";
import { CircleCheck, CircleDot, X } from "lucide-react";
import { UIIcon } from "@/components/UIIcon";
import { SelectControl } from "@/components/SelectControl";
import { WatchlistPanel } from "./WatchlistPages";
import { usePrivateIdentity, usePrivateLifetime } from "@/api/privateAccount";
import { type SubmitEvent, type RefObject, useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { getAddress, isAddress } from "viem";

import { listAdminUsers, type AdminUserUpdate, type User } from "@/api/auth";
import {
  createCurrentUserAPIKey,
  deleteCurrentUserAPIKey,
  listCurrentUserAPIKeys,
  revokeCurrentUserAPIKey,
  rotateCurrentUserAPIKey,
  type APIKeyScope,
  type UserAPIKey,
  type UserAPIKeyIssued,
  type UserAPIKeyPage,
} from "@/api/userApiKeys";
import { usePublicConfig } from "@/api/publicConfig";
import { CopyButton } from "@/components/CopyButton";
import { formatTimestamp, shorten } from "@/components/format";
import { SIWELoginControl } from "@/components/SIWELoginControl";
import { authErrorTranslationKey, useAuth } from "@/auth/AuthProvider";
import { chainsMatch } from "@/wallet/eip6963";
import { useWallet } from "@/wallet/WalletProvider";
import { PersonalBilling } from "./PersonalBilling";
import { Page } from "./pages";

const ADMIN_PAGE_SIZE = 25;

export function AccountPage({ tab }: { tab: "overview" | "api-keys" | "billing" | "watchlist" }) {
  const privateIdentity = usePrivateIdentity();
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
  const billingEnabled = publicConfig.data?.features.api_billing === true;
  const apiKeysEnabled = publicConfig.data?.features.user_api_keys === true;
  const activeTab = tab === "billing" && !billingEnabled ? "overview" : tab;
  const walletOnChain =
    Boolean(wallet.active) && chainsMatch(wallet.active?.chainID, expectedChainID);
  const walletMatchesUser =
    Boolean(wallet.active && sessionUser) &&
    addressesMatch(wallet.active?.account, sessionUser?.address) &&
    chainsMatch(wallet.active?.chainID, sessionUser?.chain_id);

  useEffect(() => {
    setDisplayName(sessionUser?.display_name ?? "");
  }, [sessionUser?.display_name]);

  useEffect(() => {
    void sessionUser?.id;
    setProfileError(undefined);
    setProfileSaved(false);
  }, [sessionUser?.id]);

  const submitProfile = async (event: SubmitEvent<HTMLFormElement>) => {
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
          <section className="identity-grid" aria-label={t("auth.account.identityStatus")}>
            <Card asChild>
              <article className="panel identity-card">
                <div className="identity-card-heading">
                  <UIIcon
                    icon={wallet.active ? CircleCheck : CircleDot}
                    size={14}
                    className={wallet.active ? "status-icon success" : "status-icon"}
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
                    <Link
                      className="account-address"
                      to="/address/$address"
                      params={{ address: wallet.active.account }}
                    >
                      <code>{wallet.active.account}</code>
                    </Link>
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
            </Card>

            <Card asChild>
              <article className="panel identity-card">
                <div className="identity-card-heading">
                  <UIIcon
                    icon={auth.session.authenticated ? CircleCheck : CircleDot}
                    size={14}
                    className={auth.session.authenticated ? "status-icon success" : "status-icon"}
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
                    <Link
                      className="account-address"
                      to="/address/$address"
                      params={{ address: sessionUser.address }}
                    >
                      <code>{sessionUser.address}</code>
                    </Link>
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
            </Card>
          </section>

          {auth.error && <Message type="error">{t(authErrorTranslationKey(auth.error))}</Message>}

          {!auth.loading && !auth.session.authenticated && (
            <Card asChild>
              <section className="panel auth-action-panel" aria-labelledby="sign-in-title">
                <div>
                  <span className="eyebrow">{t("auth.signIn.eyebrow")}</span>
                  <h2 id="sign-in-title">{t("auth.signIn.title")}</h2>
                  <p>{t("auth.signIn.description")}</p>
                </div>
                <SIWELoginControl />
              </section>
            </Card>
          )}

          {auth.session.authenticated && sessionUser && (
            <NavigationTabs className="account-tabs" aria-label={t("auth.account.sections")}>
              <NavigationTab>
                <Link
                  aria-current={activeTab === "overview" ? "page" : undefined}
                  search={{ tab: "overview" }}
                  to="/account"
                >
                  {t("auth.account.tabs.overview")}
                </Link>
              </NavigationTab>
              <NavigationTab>
                <Link
                  to="/account"
                  search={{ tab: "watchlist" }}
                  aria-current={activeTab === "watchlist" ? "page" : undefined}
                >
                  {t("watchlist.title")}
                </Link>
              </NavigationTab>
              <NavigationTab>
                <Link
                  aria-current={activeTab === "api-keys" ? "page" : undefined}
                  search={{ tab: "api-keys" }}
                  to="/account"
                >
                  {t("auth.account.tabs.apiKeys")}
                </Link>
              </NavigationTab>
              {billingEnabled && (
                <NavigationTab>
                  <Link
                    aria-current={activeTab === "billing" ? "page" : undefined}
                    search={{ tab: "billing" }}
                    to="/account"
                  >
                    {t("auth.account.tabs.billing")}
                  </Link>
                </NavigationTab>
              )}
            </NavigationTabs>
          )}

          {privateIdentity && activeTab === "watchlist" && <WatchlistPanel key={privateIdentity} />}
          {auth.session.authenticated && sessionUser && activeTab === "overview" && (
            <div
              className={`account-layout${sessionUser.role === "admin" ? "" : " account-layout-single"}`}
              data-account-tab="overview"
            >
              <Card asChild>
                <section className="panel profile-panel" aria-labelledby="profile-title">
                  <div className="panel-heading">
                    <h2 id="profile-title">{t("auth.profile.title")}</h2>
                    <Badge variant="status" className={`user-state ${sessionUser.status}`}>
                      {t(`auth.status.${sessionUser.status}`)}
                    </Badge>
                  </div>
                  <dl className="profile-details">
                    <div>
                      <dt>{t("auth.fields.address")}</dt>
                      <dd>
                        <Link
                          className="account-address"
                          to="/address/$address"
                          params={{ address: sessionUser.address }}
                        >
                          <code>{sessionUser.address}</code>
                        </Link>
                      </dd>
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
                        ) : (
                          "—"
                        )}
                      </dd>
                    </div>
                  </dl>
                  <form className="profile-form" onSubmit={submitProfile}>
                    <div className="field-control">
                      <Label htmlFor="auth-display-name">{t("auth.profile.displayName")}</Label>
                      <Input
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
                      <small id="display-name-hint">{t("auth.profile.displayNameHint")}</small>
                    </div>
                    {profileError && (
                      <p className="form-error" role="alert">
                        {profileError}
                      </p>
                    )}
                    {profileSaved && <Message type="success">{t("auth.profile.saved")}</Message>}
                    <Button className="button primary" disabled={auth.pending} type="submit">
                      {t("auth.profile.save")}
                    </Button>
                  </form>
                </section>
              </Card>

              {sessionUser.role === "admin" && (
                <Card asChild>
                  <aside
                    className="panel admin-actions-panel"
                    aria-labelledby="admin-actions-title"
                  >
                    <h2 id="admin-actions-title">{t("auth.admin.actionsTitle")}</h2>
                    <Button asChild variant="outline">
                      <Link className="button secondary inline-button" to="/admin/users">
                        {t("auth.admin.openUsers")}
                      </Link>
                    </Button>
                    {billingEnabled && (
                      <Button asChild variant="outline">
                        <Link className="button secondary inline-button" to="/admin/billing">
                          {t("billing.admin.openBilling")}
                        </Link>
                      </Button>
                    )}
                  </aside>
                </Card>
              )}
            </div>
          )}
          {auth.session.authenticated &&
            sessionUser &&
            activeTab === "api-keys" &&
            (apiKeysEnabled ? (
              <UserAPIKeysPanel key={privateIdentity} locale={locale} />
            ) : (
              <Card asChild>
                <section className="panel auth-gate" aria-labelledby="api-keys-unavailable-title">
                  <h2 id="api-keys-unavailable-title">{t("auth.apiKeys.unavailableTitle")}</h2>
                  <p>{t("auth.apiKeys.unavailableDescription")}</p>
                </section>
              </Card>
            ))}
          {billingEnabled &&
            auth.session.authenticated &&
            sessionUser &&
            activeTab === "billing" && <PersonalBilling key={privateIdentity} />}
        </>
      )}
    </Page>
  );
}

function APIKeyTabs({
  creating,
  disabled,
  onChange,
}: {
  creating: boolean;
  disabled: boolean;
  onChange: (creating: boolean) => void;
}) {
  const { t } = useTranslation();
  return (
    <Tabs value={String(creating)} onValueChange={(value) => onChange(value === "true")}>
      <TabsList className="contract-tabs" aria-label={t("auth.apiKeys.title")}>
        {([false, true] as const).map((create) => (
          <TabsTrigger
            key={String(create)}
            value={String(create)}
            id={`api-key-tab-${create}`}
            aria-controls="api-key-panel"
            disabled={disabled}
          >
            {t(create ? "auth.apiKeys.createTab" : "auth.apiKeys.listTab")}
          </TabsTrigger>
        ))}
      </TabsList>
    </Tabs>
  );
}

function APIKeyPolicyCard({ policy }: { policy: UserAPIKeyPage["policy"] | undefined }) {
  const { t } = useTranslation();
  return (
    <div className="api-key-summary-grid">
      <Card asChild>
        <article className="panel api-key-policy-card">
          <span className="eyebrow">{t("auth.apiKeys.eyebrow")}</span>
          <p>{t("auth.apiKeys.description")}</p>
          {policy && (
            <dl>
              <div>
                <dt>{t("auth.apiKeys.active")}</dt>
                <dd>
                  {policy.active_count} / {policy.maximum_active}
                </dd>
              </div>
              <div>
                <dt>{t("auth.apiKeys.rate")}</dt>
                <dd>{policy.rate_per_second} /s</dd>
              </div>
              <div>
                <dt>{t("auth.apiKeys.burst")}</dt>
                <dd>{policy.burst}</dd>
              </div>
            </dl>
          )}
        </article>
      </Card>
    </div>
  );
}

function UserAPIKeysPanel({ locale }: { locale: string }) {
  const { t } = useTranslation();
  const auth = useAuth();
  const queryClient = useQueryClient();
  const getSignal = usePrivateLifetime();
  const [creating, setCreating] = useState(false);
  const [cursors, setCursors] = useState([""]);
  const [name, setName] = useState("");
  const [scopes, setScopes] = useState<APIKeyScope[]>(["api:read"]);
  const [issued, setIssued] = useState<UserAPIKeyIssued>();
  const [pendingPrefix, setPendingPrefix] = useState<string>();
  const [confirmation, setConfirmation] = useState<{
    operation: "rotate" | "revoke" | "delete";
    key: UserAPIKey;
    csrf: string;
  }>();
  const [formError, setFormError] = useState<string>();
  const dialogRef = useRef<HTMLDivElement>(null);
  const cursor = cursors.at(-1) || undefined;
  const csrfToken = auth.session.csrf_token;
  const keys = useQuery({
    queryKey: [
      "current-user-api-keys",
      auth.session.user?.id,
      auth.session.expires_at,
      cursor ?? null,
    ],
    queryFn: ({ signal }) => listCurrentUserAPIKeys(25, cursor, signal),
    enabled: auth.session.authenticated,
    retry: false,
    staleTime: 0,
  });

  useEffect(() => {
    if (!issued) return;
    dialogRef.current?.focus();
    const close = (event: KeyboardEvent) => {
      if (event.key === "Escape") setIssued(undefined);
    };
    window.addEventListener("keydown", close);
    return () => window.removeEventListener("keydown", close);
  }, [issued]);

  const toggleScope = (scope: APIKeyScope) => {
    setScopes((current) =>
      current.includes(scope)
        ? current.filter((candidate) => candidate !== scope)
        : [...current, scope],
    );
    setFormError(undefined);
  };

  const createKey = async (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (pendingPrefix) return;
    const normalizedName = name.trim();
    if (!csrfToken || !normalizedName || scopes.length === 0) {
      setFormError(t("auth.apiKeys.invalidRequest"));
      return;
    }
    setPendingPrefix("create");
    setFormError(undefined);
    try {
      const next = await createCurrentUserAPIKey(csrfToken, normalizedName, scopes, getSignal());
      if (getSignal().aborted) return;
      setIssued(next);
      setCreating(false);
      setCursors([""]);
      setName("");
      setScopes(["api:read"]);
      await queryClient.invalidateQueries({
        queryKey: ["current-user-api-keys", auth.session.user?.id],
      });
    } catch {
      setFormError(t("auth.apiKeys.mutationFailed"));
    } finally {
      setPendingPrefix(undefined);
    }
  };

  const rotateKey = async (key: UserAPIKey) => {
    if (!csrfToken || pendingPrefix || getSignal().aborted) return;
    setPendingPrefix(key.prefix);
    setFormError(undefined);
    try {
      const next = await rotateCurrentUserAPIKey(csrfToken, key.prefix, getSignal());
      if (getSignal().aborted) return;
      setIssued(next);
      await keys.refetch();
    } catch {
      setFormError(t("auth.apiKeys.mutationFailed"));
    } finally {
      setPendingPrefix(undefined);
    }
  };

  const revokeKey = async (key: UserAPIKey) => {
    if (!csrfToken || pendingPrefix || getSignal().aborted) return;
    setPendingPrefix(key.prefix);
    setFormError(undefined);
    try {
      await revokeCurrentUserAPIKey(csrfToken, key.prefix, getSignal());
      await keys.refetch();
    } catch {
      setFormError(t("auth.apiKeys.mutationFailed"));
    } finally {
      setPendingPrefix(undefined);
    }
  };

  const deleteKey = async (key: UserAPIKey) => {
    if (!csrfToken || pendingPrefix || getSignal().aborted) return;
    setPendingPrefix(key.prefix);
    setFormError(undefined);
    try {
      await deleteCurrentUserAPIKey(csrfToken, key.prefix, getSignal());
      if (keys.data?.data.items.length === 1) setCursors([""]);
      await queryClient.invalidateQueries({
        queryKey: ["current-user-api-keys", auth.session.user?.id],
      });
    } catch {
      setFormError(t("auth.apiKeys.mutationFailed"));
    } finally {
      setPendingPrefix(undefined);
    }
  };

  const confirmationValid =
    confirmation &&
    confirmation.csrf === csrfToken &&
    keys.data?.data.items.some(
      (key) =>
        key.prefix === confirmation.key.prefix &&
        key.status === confirmation.key.status &&
        key.name === confirmation.key.name,
    );
  useEffect(() => {
    if (!confirmationValid) setConfirmation(undefined);
  }, [confirmationValid]);

  const policy = keys.data?.data.policy;

  return (
    <section className="api-key-workspace" aria-labelledby="api-key-workspace-title">
      <h2 id="api-key-workspace-title">{t("auth.apiKeys.title")}</h2>
      <APIKeyTabs
        creating={creating}
        disabled={Boolean(pendingPrefix)}
        onChange={(value) => {
          setCreating(value);
          setFormError(undefined);
        }}
      />
      <div
        role="tabpanel"
        className="api-key-workspace"
        id="api-key-panel"
        tabIndex={0}
        aria-labelledby={`api-key-tab-${creating}`}
      >
        {creating && <APIKeyPolicyCard policy={policy} />}

        {creating && (
          <Card asChild>
            <form className="panel api-key-create-form account-create-form" onSubmit={createKey}>
              <h2>{t("auth.apiKeys.createTitle")}</h2>
              <Label className="field-control" htmlFor="api-key-name">
                <span>{t("auth.apiKeys.name")}</span>
                <Input
                  id="api-key-name"
                  maxLength={128}
                  onChange={(event) => {
                    setName(event.target.value);
                    setFormError(undefined);
                  }}
                  placeholder={t("auth.apiKeys.namePlaceholder")}
                  value={name}
                />
              </Label>
              <fieldset className="api-key-scopes">
                <legend>{t("auth.apiKeys.scopes")}</legend>
                {(["api:read", "contract:verify"] as APIKeyScope[]).map((scope) => (
                  <Label key={scope}>
                    <Checkbox
                      checked={scopes.includes(scope)}
                      onChange={() => toggleScope(scope)}
                    />
                    <span>
                      <strong>
                        {t(`auth.apiKeys.scope.${apiKeyScopeTranslationKey(scope)}.title`)}
                      </strong>
                      <small>
                        {t(`auth.apiKeys.scope.${apiKeyScopeTranslationKey(scope)}.description`)}
                      </small>
                    </span>
                  </Label>
                ))}
              </fieldset>
              <div className="account-form-actions">
                <Button
                  className="button primary"
                  disabled={
                    Boolean(pendingPrefix) ||
                    !policy ||
                    policy.active_count >= policy.maximum_active ||
                    !name.trim() ||
                    scopes.length === 0
                  }
                  type="submit"
                >
                  {t("auth.apiKeys.create")}
                </Button>
                <Button
                  variant="outline"
                  className="button secondary"
                  type="button"
                  disabled={Boolean(pendingPrefix)}
                  onClick={() => {
                    setCreating(false);
                    setFormError(undefined);
                  }}
                >
                  {t("auth.apiKeys.cancel")}
                </Button>
              </div>
            </form>
          </Card>
        )}

        {formError && <Message type="error">{formError}</Message>}
        {keys.isPending && (
          <Alert asChild>
            <p className="query-notice" role="status">
              {t("auth.apiKeys.loading")}
            </p>
          </Alert>
        )}
        {keys.error && (
          <Alert asChild>
            <div className="query-notice degraded" role="alert">
              <span>
                <strong>{t("auth.apiKeys.loadFailed")}</strong>
                <small>{t("auth.apiKeys.loadFailedDescription")}</small>
              </span>
            </div>
          </Alert>
        )}
        {!creating && keys.data?.data.items.length === 0 && (
          <p className="empty-result" role="status">
            {t("auth.apiKeys.empty")}
          </p>
        )}
        {!creating && keys.data && keys.data.data.items.length > 0 && (
          <div className="api-key-list" aria-label={t("auth.apiKeys.listLabel")}>
            {keys.data.data.items.map((key) => (
              <Card key={key.prefix} asChild>
                <article className="panel api-key-card">
                  <div className="api-key-card-heading">
                    <div>
                      <h3>{key.name}</h3>
                      <code>{key.prefix}</code>
                    </div>
                    <Badge variant="status" className={`user-state ${key.status}`}>
                      {t(`auth.apiKeys.status.${key.status}`)}
                    </Badge>
                  </div>
                  <dl>
                    <div>
                      <dt>{t("auth.apiKeys.scopes")}</dt>
                      <dd>
                        {key.scopes
                          .map((scope) =>
                            t(`auth.apiKeys.scope.${apiKeyScopeTranslationKey(scope)}.short`),
                          )
                          .join(", ")}
                      </dd>
                    </div>
                    <div>
                      <dt>{t("auth.apiKeys.quota")}</dt>
                      <dd>
                        {key.rate_per_second}/s · {key.burst}
                      </dd>
                    </div>
                    <div>
                      <dt>{t("auth.apiKeys.created")}</dt>
                      <dd>
                        <time dateTime={key.created_at}>
                          {formatTimestamp(key.created_at, locale)}
                        </time>
                      </dd>
                    </div>
                    {key.revoked_at && (
                      <div>
                        <dt>{t("auth.apiKeys.revoked")}</dt>
                        <dd>
                          <time dateTime={key.revoked_at}>
                            {formatTimestamp(key.revoked_at, locale)}
                          </time>
                        </dd>
                      </div>
                    )}
                  </dl>
                  {key.status === "revoked" && (
                    <div className="api-key-actions">
                      <Button
                        variant="destructive"
                        className="button danger"
                        disabled={Boolean(pendingPrefix)}
                        onClick={() => {
                          if (csrfToken)
                            setConfirmation({ operation: "delete", key, csrf: csrfToken });
                        }}
                        type="button"
                      >
                        {t("auth.apiKeys.delete")}
                      </Button>
                    </div>
                  )}
                  {key.status === "active" && (
                    <div className="api-key-actions">
                      <Button
                        variant="outline"
                        className="button secondary"
                        disabled={Boolean(pendingPrefix)}
                        onClick={() => {
                          if (csrfToken)
                            setConfirmation({ operation: "rotate", key, csrf: csrfToken });
                        }}
                        type="button"
                      >
                        {t("auth.apiKeys.rotate")}
                      </Button>
                      <Button
                        variant="destructive"
                        className="button danger"
                        disabled={Boolean(pendingPrefix)}
                        onClick={() => {
                          if (csrfToken)
                            setConfirmation({ operation: "revoke", key, csrf: csrfToken });
                        }}
                        type="button"
                      >
                        {t("auth.apiKeys.revoke")}
                      </Button>
                    </div>
                  )}
                </article>
              </Card>
            ))}
          </div>
        )}
        {!creating && keys.data && (
          <Pagination className="cursor-pagination" aria-label={t("auth.apiKeys.pagination")}>
            <Button
              variant="outline"
              className="button secondary"
              disabled={Boolean(pendingPrefix) || keys.isFetching || cursors.length === 1}
              onClick={() =>
                setCursors((current) => (current.length > 1 ? current.slice(0, -1) : current))
              }
              type="button"
            >
              {t("pagination.previous")}
            </Button>
            <span>{t("pagination.page", { page: cursors.length })}</span>
            <Button
              variant="outline"
              className="button secondary"
              disabled={Boolean(pendingPrefix) || keys.isFetching || !keys.data.meta.next_cursor}
              onClick={() => {
                const next = keys.data?.meta.next_cursor;
                if (next) setCursors((current) => [...current, next]);
              }}
              type="button"
            >
              {t("pagination.next")}
            </Button>
          </Pagination>
        )}
      </div>

      {confirmation && confirmationValid && (
        <AlertDialog
          title={t(`auth.apiKeys.${confirmation.operation}`)}
          description={t(
            `auth.apiKeys.${{ rotate: "confirmRotate", revoke: "confirmRevoke", delete: "confirmDelete" }[confirmation.operation]}`,
            { name: confirmation.key.name },
          )}
          cancelLabel={t("auth.apiKeys.cancel")}
          confirmLabel={t(`auth.apiKeys.${confirmation.operation}`)}
          destructive
          onClose={() => setConfirmation(undefined)}
          onConfirm={() => {
            void { rotate: rotateKey, revoke: revokeKey, delete: deleteKey }[
              confirmation.operation
            ](confirmation.key);
          }}
        />
      )}
      {issued && (
        <IssuedKeyDialog
          token={issued.token}
          dialogRef={dialogRef}
          onClose={() => setIssued(undefined)}
        />
      )}
    </section>
  );
}

function IssuedKeyDialog({
  token,
  dialogRef,
  onClose,
}: {
  token: string;
  dialogRef: RefObject<HTMLDivElement | null>;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="dialog-backdrop" onMouseDown={onClose}>
      <div
        aria-labelledby="api-key-issued-title"
        aria-modal="true"
        className="api-key-issued-dialog"
        onMouseDown={(event) => event.stopPropagation()}
        ref={dialogRef}
        role="dialog"
        tabIndex={-1}
      >
        <div className="qr-dialog-heading">
          <div>
            <span className="eyebrow">{t("auth.apiKeys.issuedEyebrow")}</span>
            <h2 id="api-key-issued-title">{t("auth.apiKeys.issuedTitle")}</h2>
          </div>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={t("auth.apiKeys.closeIssued")}
            className="dialog-close"
            onClick={onClose}
            type="button"
          >
            <UIIcon icon={X} />
          </Button>
        </div>
        <IssuedToken token={token} />
        <Button className="button primary" onClick={onClose} type="button">
          {t("auth.apiKeys.savedToken")}
        </Button>
      </div>
    </div>
  );
}

export function AdminUsersPage() {
  const { i18n, t } = useTranslation();
  const auth = useAuth();
  const publicConfig = usePublicConfig();
  const billingEnabled = publicConfig.data?.features.api_billing === true;
  const [cursors, setCursors] = useState([""]);
  const [announcement, setAnnouncement] = useState<{ text: string; revision: number }>();
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
    setAnnouncement((previous) => ({ text: message, revision: (previous?.revision ?? 0) + 1 }));
    await users.refetch();
  };

  return (
    <Page title={t("auth.admin.title")} description={t("auth.admin.description")}>
      {!auth.enabled && !auth.loading ? (
        <UnavailablePanel />
      ) : auth.loading ? (
        <Alert asChild>
          <p className="query-notice" role="status">
            {t("auth.sessionState.checking")}
          </p>
        </Alert>
      ) : !auth.session.authenticated ? (
        <AuthGate
          detail={t("auth.admin.authenticationRequired")}
          title={t("auth.errors.authenticationRequired")}
        />
      ) : !isAdmin ? (
        <AuthGate detail={t("auth.admin.adminRequired")} title={t("auth.errors.adminRequired")} />
      ) : (
        <>
          <div className="admin-toolbar">
            <p>{t("auth.admin.page", { page: cursors.length })}</p>
            <div className="admin-toolbar-actions">
              {billingEnabled && (
                <Button asChild variant="outline">
                  <Link className="button secondary" to="/admin/billing">
                    {t("billing.admin.openBilling")}
                  </Link>
                </Button>
              )}
              <Button
                variant="outline"
                className="button secondary"
                disabled={users.isFetching}
                onClick={() => void users.refetch()}
                type="button"
              >
                {t("auth.admin.refresh")}
              </Button>
            </div>
          </div>
          {auth.error && <Message type="error">{t(authErrorTranslationKey(auth.error))}</Message>}
          {announcement && (
            <Message key={announcement.revision} type="success">
              {announcement.text}
            </Message>
          )}
          {users.isPending && (
            <Alert asChild>
              <p className="query-notice" role="status">
                {t("auth.admin.loading")}
              </p>
            </Alert>
          )}
          {users.error && (
            <Alert asChild>
              <div className="query-notice degraded" role="alert">
                <span>
                  <strong>{t("auth.admin.loadFailed")}</strong>
                  <small>{t("auth.admin.loadFailedDetail")}</small>
                </span>
              </div>
            </Alert>
          )}
          {users.data?.data.length === 0 && (
            <p className="empty-result" role="status">
              {t("auth.admin.empty")}
            </p>
          )}
          {users.data && users.data.data.length > 0 && (
            <div className="admin-user-list" aria-label={t("auth.admin.userList")}>
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
            <Pagination className="cursor-pagination" aria-label={t("auth.admin.pagination")}>
              <Button
                variant="outline"
                className="button secondary"
                disabled={users.isFetching || cursors.length === 1}
                onClick={() => {
                  setAnnouncement(undefined);
                  setCursors((current) => (current.length > 1 ? current.slice(0, -1) : current));
                }}
                type="button"
              >
                {t("pagination.previous")}
              </Button>
              <span>{t("pagination.page", { page: cursors.length })}</span>
              <Button
                variant="outline"
                className="button secondary"
                disabled={users.isFetching || !users.data.meta.next_cursor}
                onClick={() => {
                  const next = users.data?.meta.next_cursor;
                  if (!next) return;
                  setAnnouncement(undefined);
                  setCursors((current) => [...current, next]);
                }}
                type="button"
              >
                {t("pagination.next")}
              </Button>
            </Pagination>
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
      await onMutationComplete(t("auth.admin.updated", { address: shorten(user.address) }));
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
    <Card asChild>
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
              ) : (
                "—"
              )}
            </dd>
          </div>
          <div>
            <dt>{t("auth.fields.createdAt")}</dt>
            <dd>
              <time dateTime={user.created_at}>{formatTimestamp(user.created_at, locale)}</time>
            </dd>
          </div>
        </dl>
        <div className="admin-user-controls">
          <Label className="field-control">
            <span>{t("auth.fields.role")}</span>
            <SelectControl
              aria-label={t("auth.admin.roleFor", { address: user.address })}
              disabled={auth.pending}
              onChange={(event) => setRole(event.target.value as User["role"])}
              value={role}
            >
              <option value="user">{t("auth.role.user")}</option>
              <option value="admin">{t("auth.role.admin")}</option>
            </SelectControl>
          </Label>
          <Label className="field-control">
            <span>{t("auth.fields.status")}</span>
            <SelectControl
              aria-label={t("auth.admin.statusFor", { address: user.address })}
              disabled={auth.pending}
              onChange={(event) => setStatus(event.target.value as User["status"])}
              value={status}
            >
              <option value="active">{t("auth.status.active")}</option>
              <option value="disabled">{t("auth.status.disabled")}</option>
            </SelectControl>
          </Label>
        </div>
        <div className="admin-user-actions">
          <Button
            className="button primary"
            disabled={auth.pending || !changed}
            onClick={() => void save()}
            type="button"
          >
            {t("auth.admin.saveUser")}
          </Button>
          <Button
            variant="outline"
            className="button secondary"
            disabled={auth.pending}
            onClick={() => void revoke()}
            type="button"
          >
            {t("auth.admin.revokeSessions")}
          </Button>
        </div>
      </article>
    </Card>
  );
}

function AuthGate({ detail, title }: { detail: string; title: string }) {
  const { t } = useTranslation();
  return (
    <Card asChild>
      <section className="panel auth-gate" aria-labelledby="auth-gate-title">
        <h2 id="auth-gate-title">{title}</h2>
        <p>{detail}</p>
        <Button asChild variant="default">
          <Link className="button primary inline-button" to="/account">
            {t("auth.account.open")}
          </Link>
        </Button>
      </section>
    </Card>
  );
}

function apiKeyScopeTranslationKey(scope: APIKeyScope): "read" | "verify" {
  return scope === "api:read" ? "read" : "verify";
}

function UnavailablePanel() {
  const { t } = useTranslation();
  return (
    <Card asChild>
      <section className="panel auth-gate" aria-labelledby="auth-unavailable-title">
        <h2 id="auth-unavailable-title">{t("auth.unavailable.title")}</h2>
        <p>{t("auth.unavailable.description")}</p>
      </section>
    </Card>
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

function addressesMatch(left: string | undefined, right: string | undefined): boolean {
  if (!left || !right || !isAddress(left) || !isAddress(right)) return false;
  try {
    return getAddress(left) === getAddress(right);
  } catch {
    return false;
  }
}

function IssuedToken({ token }: { token: string }) {
  const { t } = useTranslation();
  return (
    <>
      <p>{t("auth.apiKeys.issuedWarning")}</p>
      <div className="api-key-token">
        <code>{token}</code>
        <CopyButton value={token} />
      </div>
    </>
  );
}
