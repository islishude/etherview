import { AdminSurface, useAdminIdentity } from "@/components/AdminSurface";
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
import { BillingPagination } from "./BillingControls";
import { Message } from "@/components/Message";
import { X } from "lucide-react";
import { UIIcon } from "@/components/UIIcon";
import { WatchlistPanel } from "./WatchlistPages";
import { usePrivateIdentity, usePrivateLifetime } from "@/api/privateAccount";
import {
  type SubmitEvent,
  type RefObject,
  Suspense,
  lazy,
  useEffect,
  useRef,
  useState,
} from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

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
import { formatTimestamp } from "@/components/format";
import { SIWELoginControl } from "@/components/SIWELoginControl";
import { authErrorTranslationKey, useAuth } from "@/auth/AuthProvider";
import { PersonalBilling } from "./PersonalBilling";
import { Page } from "./pages";
import { UnavailablePanel } from "./AccountGates";
import { AccountWalletNotice } from "./AccountWalletNotice";

const AdminOverviewPage = lazy(() =>
  import("./AdminOverviewPage").then((module) => ({ default: module.AdminOverviewPage })),
);
const AdminUsersPage = lazy(() =>
  import("./AdminUsersPage").then((module) => ({ default: module.AdminUsersPage })),
);
const AdminBillingPage = lazy(() =>
  import("./BillingPages").then((module) => ({ default: module.AdminBillingPage })),
);
const StatusPage = lazy(() =>
  import("./StatusPage").then((module) => ({ default: module.StatusPage })),
);

export function AccountPage({
  tab,
  section = "overview",
}: {
  tab: "overview" | "api-keys" | "billing" | "watchlist" | "admin";
  section?: "overview" | "users" | "billing" | "status";
}) {
  const privateIdentity = usePrivateIdentity();
  const { i18n, t } = useTranslation();
  const auth = useAuth();
  const adminIdentity = useAdminIdentity();
  const publicConfig = usePublicConfig();
  const locale = i18n.resolvedLanguage ?? "en";
  const sessionUser = auth.session.user ?? undefined;
  const billingEnabled = publicConfig.data?.features.api_billing === true;
  const apiKeysEnabled = publicConfig.data?.features.user_api_keys === true;
  const activeTab = tab === "billing" && !billingEnabled ? "overview" : tab;

  return (
    <Page title={t("auth.account.title")} description={t("auth.account.description")}>
      {!auth.enabled && !auth.loading ? (
        <UnavailablePanel />
      ) : (
        <>
          {auth.error && <Message type="error">{t(authErrorTranslationKey(auth.error))}</Message>}

          {auth.loading && <p role="status">{t("auth.sessionState.checking")}</p>}
          {!auth.loading && !auth.session.authenticated && (
            <section
              data-account-sign-in
              className="flex flex-wrap items-center justify-between gap-3 border-b border-border py-3"
              aria-label={t("auth.signIn.title")}
            >
              <p className="m-0 text-sm text-muted-foreground">
                {t("auth.sessionState.loginHint")}
              </p>
              <SIWELoginControl />
            </section>
          )}

          {auth.session.authenticated && sessionUser && (
            <NavigationTabs
              className="account-tabs gap-2 [&>a]:px-1"
              aria-label={t("auth.account.sections")}
            >
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
              {adminIdentity && (
                <NavigationTab>
                  <Link
                    to="/account"
                    search={{ tab: "admin", section: "overview" }}
                    aria-current={activeTab === "admin" ? "page" : undefined}
                  >
                    {t("adminUI.title")}
                  </Link>
                </NavigationTab>
              )}
            </NavigationTabs>
          )}

          <AccountWalletNotice />
          {activeTab === "admin" &&
            (adminIdentity ? (
              <Suspense fallback={<p role="status">{t("adminUI.checking")}</p>}>
                {section === "billing" ? (
                  <AdminBillingPage />
                ) : section === "status" ? (
                  <StatusPage />
                ) : section === "users" ? (
                  <AdminUsersPage />
                ) : (
                  <AdminOverviewPage />
                )}
              </Suspense>
            ) : (
              <AdminSurface>{null}</AdminSurface>
            ))}
          {privateIdentity && activeTab === "watchlist" && <WatchlistPanel key={privateIdentity} />}
          {auth.session.authenticated && sessionUser && activeTab === "overview" && (
            <div className="account-layout account-layout-single" data-account-tab="overview">
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
                </section>
              </Card>
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
          <BillingPagination
            itemCount={keys.data.data.items.length}
            busy={Boolean(pendingPrefix) || keys.isFetching}
            cursors={cursors}
            onChange={setCursors}
            nextCursor={keys.data.meta.next_cursor}
            label={t("auth.apiKeys.pagination")}
          />
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

function apiKeyScopeTranslationKey(scope: APIKeyScope): "read" | "verify" {
  return scope === "api:read" ? "read" : "verify";
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
