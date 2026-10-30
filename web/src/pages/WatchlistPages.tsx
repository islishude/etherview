import { Message } from "@/components/Message";
import { Link } from "@tanstack/react-router";
import { type SubmitEvent, useState } from "react";
import { BrowserPushPanel } from "./BrowserPushPanel";
import { useTranslation } from "react-i18next";
import { useWatches } from "@/api/privateAccount";
import { AccountActionError, useAccountAction } from "@/components/AccountAction";
import { deleteWatch, saveWatch, type WatchInput, type AddressWatch } from "@/api/watchlist";

const allKinds: WatchInput["kinds"] = ["transaction", "erc20", "erc721", "erc1155"];
export function WatchlistPanel() {
  const { t } = useTranslation();
  const query = useWatches();
  const action = useAccountAction();
  const [showForm, setShowForm] = useState(false);
  const [editing, setEditing] = useState<AddressWatch>();
  const [form, setForm] = useState<WatchInput>({
    address: "",
    label: "",
    kinds: allKinds,
    direction: "both",
    enabled: true,
  });
  function reset() {
    setShowForm(false);
    setEditing(undefined);
    setForm({
      address: "",
      label: "",
      kinds: allKinds,
      direction: "both",
      enabled: true,
    });
  }
  async function submit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault();
    if (
      await action.run(
        (csrf, signal) => saveWatch(form, csrf, signal, editing?.id),
        t(editing ? "watchlist.saved" : "watchlist.added"),
      )
    )
      reset();
  }
  return (
    <section className="panel watchlist-panel">
      <h2>{t("watchlist.title")}</h2>
      <p>{t("watchlist.hint")}</p>
      <BrowserPushPanel />
      {!showForm && (
        <button
          className="button secondary inline-button account-action"
          disabled={query.isPending || query.isError || (query.data?.length ?? 0) >= 5}
          onClick={() => setShowForm(true)}
        >
          {t("watchlist.add")}
        </button>
      )}
      {showForm && (
        <form className="profile-form account-create-form" onSubmit={submit}>
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
          <fieldset className="watch-direction">
            <legend>{t("watchlist.direction")}</legend>
            <div className="watch-direction-options">
              {(["both", "in", "out"] as const).map((direction) => (
                <label key={direction}>
                  <input
                    className="sr-only"
                    type="radio"
                    name="watch-direction"
                    value={direction}
                    checked={form.direction === direction}
                    onChange={() => setForm({ ...form, direction })}
                  />
                  <span>{t(`watchlist.${direction}`)}</span>
                </label>
              ))}
            </div>
          </fieldset>
          <label>
            <input
              type="checkbox"
              checked={form.enabled}
              onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
            />
            {t("watchlist.enabled")}
          </label>
          <div className="account-form-actions">
            <button className="button primary" disabled={action.pending || !form.kinds.length}>
              {editing ? t("watchlist.save") : t("watchlist.add")}
            </button>
            <button
              className="button secondary"
              type="button"
              disabled={action.pending}
              onClick={reset}
            >
              {t("watchlist.cancel")}
            </button>
          </div>
        </form>
      )}
      <AccountActionError code={action.error} />
      {action.success && <Message type="success">{action.success}</Message>}
      {query.isError && <AccountActionError inline code="activity_unavailable" />}
      {!showForm &&
        (query.isPending ? (
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
                      setShowForm(true);
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
                      void action.run(
                        (csrf, signal) => deleteWatch(item.id, csrf, signal),
                        t("watchlist.removed"),
                      )
                    }
                  >
                    {t("watchlist.remove")}
                  </button>
                </div>
              </li>
            ))}
          </ul>
        ))}
      {!showForm && query.data?.length === 0 && <p>{t("watchlist.empty")}</p>}
    </section>
  );
}
