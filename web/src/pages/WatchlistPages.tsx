import { type FormEvent, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useAuth } from "@/auth/AuthProvider";
import { ApiError } from "@/api/client";
import {
  useWatches,
  useNotifications,
  usePrivateIdentity,
  usePrivateLifetime,
} from "@/api/privateAccount";
import {
  deleteWatch,
  saveWatch,
  readNotification,
  type WatchInput,
  type AddressWatch,
} from "@/api/watchlist";

export function useAccountAction() {
  const auth = useAuth();
  const getSignal = usePrivateLifetime();
  const identity = usePrivateIdentity();
  const client = useQueryClient();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  async function run(action: (csrf: string, signal: AbortSignal) => Promise<unknown>) {
    const signal = getSignal();
    const csrf = auth.session.csrf_token;
    if (!csrf || pending || signal.aborted) return false;
    setPending(true);
    setError("");
    try {
      await action(csrf, signal);
      if (signal.aborted) return false;
      await client.invalidateQueries({
        queryKey: ["private-account", identity],
      });
      return true;
    } catch (err) {
      if (!signal.aborted) setError(err instanceof ApiError ? err.code : "activity_unavailable");
      return false;
    } finally {
      if (!signal.aborted) setPending(false);
    }
  }
  return { run, pending, error };
}
export function AccountActionError({ code }: { code: string }) {
  const { t } = useTranslation();
  const known = [
    "watchlist_limit",
    "address_already_watched",
    "export_rate_limit",
    "export_limit",
    "invalid_watchlist_request",
    "activity_unavailable",
    "authentication_required",
  ];
  return code ? (
    <p className="form-error" role="alert">
      {t(`watchlist.errors.${known.includes(code) ? code : "activity_unavailable"}`)}
    </p>
  ) : null;
}
const allKinds: WatchInput["kinds"] = ["transaction", "erc20", "erc721", "erc1155"];
export function WatchlistPanel() {
  const { t } = useTranslation();
  const query = useWatches();
  const action = useAccountAction();
  const [editing, setEditing] = useState<AddressWatch>();
  const [form, setForm] = useState<WatchInput>({
    address: "",
    label: "",
    kinds: allKinds,
    direction: "both",
    enabled: true,
  });
  function reset() {
    setEditing(undefined);
    setForm({
      address: "",
      label: "",
      kinds: allKinds,
      direction: "both",
      enabled: true,
    });
  }
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (await action.run((csrf, signal) => saveWatch(form, csrf, signal, editing?.id))) reset();
  }
  return (
    <section className="panel watchlist-panel">
      <h2>{t("watchlist.title")}</h2>
      <p>{t("watchlist.hint")}</p>
      <form className="profile-form" onSubmit={submit}>
        <label>
          {t("watchlist.address")}
          <input
            required
            value={form.address}
            disabled={Boolean(editing)}
            onChange={(e) => setForm({ ...form, address: e.target.value })}
          />
        </label>
        <label>
          {t("watchlist.label")}
          <input
            maxLength={64}
            value={form.label}
            onChange={(e) => setForm({ ...form, label: e.target.value })}
          />
        </label>
        <fieldset>
          <legend>{t("watchlist.kinds")}</legend>
          {allKinds.map((kind) => (
            <label key={kind}>
              <input
                type="checkbox"
                checked={form.kinds.includes(kind)}
                onChange={(e) =>
                  setForm({
                    ...form,
                    kinds: e.target.checked
                      ? [...form.kinds, kind]
                      : form.kinds.filter((k) => k !== kind),
                  })
                }
              />
              {kind === "transaction" ? t("watchlist.transactions") : kind.toUpperCase()}
            </label>
          ))}
        </fieldset>
        <label>
          {t("watchlist.direction")}
          <select
            value={form.direction}
            onChange={(e) =>
              setForm({
                ...form,
                direction: e.target.value as WatchInput["direction"],
              })
            }
          >
            {["both", "in", "out"].map((d) => (
              <option key={d} value={d}>
                {t(`watchlist.${d}`)}
              </option>
            ))}
          </select>
        </label>
        <label>
          <input
            type="checkbox"
            checked={form.enabled}
            onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
          />
          {t("watchlist.enabled")}
        </label>
        <button className="button primary" disabled={action.pending || !form.kinds.length}>
          {editing ? t("watchlist.save") : t("watchlist.add")}
        </button>
        {editing && (
          <button className="button secondary" type="button" onClick={reset}>
            {t("watchlist.cancel")}
          </button>
        )}
      </form>
      <AccountActionError code={action.error || (query.isError ? "activity_unavailable" : "")} />
      {query.isPending ? (
        <p role="status">{t("watchlist.loading")}</p>
      ) : (
        <ul className="watchlist-items">
          {query.data?.map((item) => (
            <li key={item.id}>
              <Link to="/address/$address" params={{ address: item.address }}>
                {item.label || item.address}
              </Link>
              <code>{item.address}</code>
              <span>
                {item.kinds
                  .map((kind) =>
                    kind === "transaction" ? t("watchlist.transactions") : kind.toUpperCase(),
                  )
                  .join(", ")}{" "}
                · {t(`watchlist.${item.direction}`)} ·{" "}
                {t(item.enabled ? "watchlist.enabled" : "watchlist.paused")}
              </span>
              <div>
                <button
                  className="button secondary"
                  onClick={() => {
                    setEditing(item);
                    setForm({
                      address: item.address,
                      label: item.label,
                      kinds: item.kinds,
                      direction: item.direction,
                      enabled: item.enabled,
                    });
                  }}
                >
                  {t("watchlist.edit")}
                </button>
                <button
                  className="button secondary"
                  disabled={action.pending}
                  onClick={() =>
                    void action.run((csrf, signal) => deleteWatch(item.id, csrf, signal))
                  }
                >
                  {t("watchlist.remove")}
                </button>
              </div>
            </li>
          ))}
        </ul>
      )}
      {query.data?.length === 0 && <p>{t("watchlist.empty")}</p>}
    </section>
  );
}
export function NotificationsPanel() {
  const { t } = useTranslation();
  const [cursor, setCursor] = useState("");
  const [unread, setUnread] = useState(false);
  const query = useNotifications(cursor, unread);
  const action = useAccountAction();
  return (
    <section className="panel watchlist-panel">
      <h2>{t("watchlist.notifications")}</h2>
      <p>{t("watchlist.retention")}</p>
      <label>
        <input
          type="checkbox"
          checked={unread}
          onChange={(e) => {
            setUnread(e.target.checked);
            setCursor("");
          }}
        />
        {t("watchlist.unread")}
      </label>
      <button
        className="button secondary"
        disabled={action.pending || !query.data || query.data.unread_count === "0"}
        onClick={() => {
          const id = query.data?.watermark;
          if (id) void action.run((csrf, signal) => readNotification(id, true, csrf, signal));
        }}
      >
        {t("watchlist.readAll")}
      </button>
      <AccountActionError code={action.error || (query.isError ? "activity_unavailable" : "")} />
      {query.isPending && <p role="status">{t("watchlist.loading")}</p>}
      <ul className="watchlist-items">
        {query.data?.items.map((n) => (
          <li key={n.id}>
            <strong>{n.label || n.address}</strong>
            <span>
              {t(`watchlist.${n.activity.direction}`)} ·{" "}
              {n.activity.kind === "transaction"
                ? t("watchlist.transactions")
                : n.activity.kind.toUpperCase()}
            </span>
            <Link
              to="/tx/$hash"
              search={{ tab: "overview" }}
              params={{ hash: n.activity.transaction_hash }}
            >
              {n.activity.transaction_hash}
            </Link>
            <span>
              {n.activity.status
                ? t(`watchlist.${n.activity.status}`)
                : (n.activity.amount ?? n.activity.token_id)}
            </span>
            {(!n.canonical || !n.published) && (
              <strong>{t(!n.canonical ? "watchlist.orphaned" : "watchlist.unpublished")}</strong>
            )}
            {!n.read && (
              <button
                className="button secondary"
                disabled={action.pending}
                onClick={() =>
                  void action.run((csrf, signal) => readNotification(n.id, false, csrf, signal))
                }
              >
                {t("watchlist.read")}
              </button>
            )}
          </li>
        ))}
      </ul>
      {query.data?.items.length === 0 && <p>{t("watchlist.noNotifications")}</p>}
      {cursor && (
        <button className="button secondary" onClick={() => setCursor("")}>
          {t("watchlist.newest")}
        </button>
      )}
      {query.data?.next_cursor && (
        <button
          className="button secondary"
          onClick={() => setCursor(query.data?.next_cursor ?? "")}
        >
          {t("watchlist.more")}
        </button>
      )}
    </section>
  );
}
