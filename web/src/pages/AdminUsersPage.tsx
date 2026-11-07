import { AdminSurface, AdminPanel, useAdminIdentity } from "@/components/AdminSurface";
import { Sheet } from "@/components/ui/sheet";
import { AlertDialog } from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import "@/styles/account.css";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { Alert } from "@/components/ui/alert";
import { Pagination } from "@/components/ui/pagination";
import { Message } from "@/components/Message";
import { X, RotateCw } from "lucide-react";
import { UIIcon } from "@/components/UIIcon";
import { SelectControl } from "@/components/SelectControl";
import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { listAdminUsers, type AdminUserUpdate, type User } from "@/api/auth";
import { formatTimestamp, shorten } from "@/components/format";
import { authErrorTranslationKey, useAuth } from "@/auth/AuthProvider";
import { AuthGate, UnavailablePanel } from "./AccountGates";
const ADMIN_PAGE_SIZE = 25;

export function AdminUsersPage() {
  return (
    <AdminSurface>
      <AdminUsersContent />
    </AdminSurface>
  );
}

function AdminUsersContent() {
  const { i18n, t } = useTranslation();
  const auth = useAuth();
  const identity = useAdminIdentity();
  const [draft, setDraft] = useState("");
  const [search, setSearch] = useState("");
  const [searchError, setSearchError] = useState(false);
  const searchInput = useRef<HTMLInputElement>(null);
  const [cursors, setCursors] = useState([""]);
  const [announcement, setAnnouncement] = useState<{ text: string; revision: number }>();
  const cursor = cursors.at(-1) || undefined;
  const isAdmin =
    auth.session.authenticated &&
    auth.session.user?.status === "active" &&
    auth.session.user.role === "admin";
  const locale = i18n.resolvedLanguage ?? "en";
  const users = useQuery({
    queryKey: ["private-account", identity, "admin", "users", search, cursor ?? null],
    queryFn: ({ signal }) => listAdminUsers(ADMIN_PAGE_SIZE, cursor, search || undefined, signal),
    enabled: auth.enabled && isAdmin,
    retry: false,
    staleTime: 0,
  });

  useEffect(() => {
    if (!isAdmin) setCursors([""]);
  }, [isAdmin]);

  const mutationComplete = async (message: string) => {
    await users.refetch();
    setAnnouncement((previous) => ({ text: message, revision: (previous?.revision ?? 0) + 1 }));
  };

  return (
    <AdminPanel
      title={t("auth.admin.title")}
      description={t("adminUI.usersDescription")}
      actions={
        <Button
          variant="ghost"
          size="icon"
          type="button"
          aria-label={t(users.isFetching ? "refresh.loading" : "adminUI.refresh")}
          title={t("adminUI.refresh")}
          aria-busy={users.isFetching}
          disabled={users.isFetching}
          onClick={() => void users.refetch()}
        >
          <UIIcon icon={RotateCw} />
        </Button>
      }
    >
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
          <form
            className="flex flex-wrap items-end gap-2"
            onSubmit={(event) => {
              event.preventDefault();
              const value = draft.trim();
              if (
                [...value].length > 64 ||
                new TextEncoder().encode(value).length > 256 ||
                /\p{Cc}/u.test(draft)
              ) {
                setSearchError(true);
                return;
              }
              setSearchError(false);
              setSearch(value);
              setCursors([""]);
            }}
          >
            <div className="grid w-full min-w-0 max-w-80 gap-2">
              <Label htmlFor="admin-user-search">{t("adminUI.search")}</Label>
              <span className="relative block">
                <Input
                  id="admin-user-search"
                  ref={searchInput}
                  className="pr-10"
                  value={draft}
                  placeholder={t("adminUI.searchHint")}
                  onChange={(event) => setDraft(event.target.value)}
                />
                {(draft || search) && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="absolute right-0 top-0"
                    aria-label={t("adminUI.clear")}
                    onClick={() => {
                      setDraft("");
                      setSearch("");
                      setCursors([""]);
                      setSearchError(false);
                      searchInput.current?.focus();
                    }}
                  >
                    <UIIcon icon={X} />
                  </Button>
                )}
              </span>
            </div>
            <Button type="submit" size="sm">
              {t("adminUI.search")}
            </Button>
          </form>
          {searchError && <p role="alert">{t("adminUI.searchInvalid")}</p>}
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
              {t(search ? "adminUI.noMatches" : "auth.admin.empty")}
            </p>
          )}
          {users.data && users.data.data.length > 0 && (
            <div className="admin-user-list" aria-label={t("auth.admin.userList")}>
              {users.data.data.map((user) => (
                <AdminUserRow
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
            <Pagination
              itemCount={users.data.data.length}
              onReset={() => setCursors([""])}
              hasPrevious={cursors.length > 1}
              busy={users.isFetching}
              className="cursor-pagination"
              aria-label={t("auth.admin.pagination")}
            >
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
    </AdminPanel>
  );
}

function AdminUserRow({
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
  const [open, setOpen] = useState(false);
  const [confirmation, setConfirmation] = useState<"discard" | "revoke">();
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
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
      if (!mounted.current) return;
      setOpen(false);
      await onMutationComplete(t("auth.admin.updated", { address: shorten(user.address) }));
    } catch {
      // Stable AuthProvider error is rendered by the owning page.
    }
  };

  const revoke = async () => {
    try {
      const count = await auth.revokeSessions(user.id);
      if (!mounted.current) return;
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

  const close = () => {
    if (changed) setConfirmation("discard");
    else setOpen(false);
  };
  return (
    <>
      <article className="flex min-w-0 flex-wrap items-center gap-3 border-b border-border py-3">
        <div className="min-w-0 flex-1 basis-48">
          <strong>
            <code title={user.address}>{shorten(user.address)}</code>{" "}
            {user.id === currentUserID && <small>{t("auth.admin.you")}</small>}
          </strong>
        </div>
        <Badge variant="secondary">{t(`auth.role.${user.role}`)}</Badge>
        <Badge variant="outline">{t(`auth.status.${user.status}`)}</Badge>
        <span className="text-sm text-muted-foreground" title={t("auth.fields.lastLogin")}>
          {user.last_login_at ? formatTimestamp(user.last_login_at, locale) : "—"}
        </span>
        <Button
          size="sm"
          variant="ghost"
          onClick={() => {
            setRole(user.role);
            setStatus(user.status);
            setOpen(true);
          }}
        >
          {t("adminUI.settings")}
        </Button>
      </article>
      {open && (
        <Sheet
          closeLabel={t("adminUI.close")}
          title={t("adminUI.userSettings")}
          onClose={close}
          footer={
            <>
              <Button variant="outline" onClick={close}>
                {t("adminUI.cancel")}
              </Button>
              <Button disabled={auth.pending || !changed} onClick={() => void save()}>
                {t("auth.admin.saveUser")}
              </Button>
            </>
          }
        >
          <div className="grid gap-5">
            <div>
              <code className="block break-all">{user.address}</code>
            </div>
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

            <details>
              <summary>{t("adminUI.identity")}</summary>
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
                    <time dateTime={user.created_at}>
                      {formatTimestamp(user.created_at, locale)}
                    </time>
                  </dd>
                </div>
              </dl>
            </details>
            <section className="border-t border-border pt-4">
              <h3>{t("adminUI.security")}</h3>
              <Button
                variant="outline"
                disabled={auth.pending}
                onClick={() => setConfirmation("revoke")}
              >
                {t("auth.admin.revokeSessions")}
              </Button>
            </section>
            {auth.error && <Message type="error">{t(authErrorTranslationKey(auth.error))}</Message>}
          </div>
        </Sheet>
      )}
      {confirmation && (
        <AlertDialog
          title={t(confirmation === "discard" ? "adminUI.discardTitle" : "adminUI.revokeTitle")}
          description={t(
            confirmation === "discard" ? "adminUI.discardDescription" : "adminUI.revokeDescription",
          )}
          cancelLabel={t("adminUI.cancel")}
          confirmLabel={t(
            confirmation === "discard" ? "adminUI.discard" : "auth.admin.revokeSessions",
          )}
          destructive
          onClose={() => setConfirmation(undefined)}
          onConfirm={() => {
            if (confirmation === "discard") {
              setRole(user.role);
              setStatus(user.status);
              setOpen(false);
            } else void revoke();
          }}
        />
      )}
    </>
  );
}
