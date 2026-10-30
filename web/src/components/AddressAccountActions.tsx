import { Message } from "@/components/Message";
import "@/i18n/resources/addressActions";
import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import { createPortal } from "react-dom";
import { Link } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { Bell, BellRing, Download, LoaderCircle, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { authErrorTranslationKey, useAuth } from "@/auth/AuthProvider";
import { usePrivateIdentity, useWatches } from "@/api/privateAccount";
import { deleteWatch, saveWatch, type AddressWatch } from "@/api/watchlist";
import { AccountActionError, useAccountAction } from "./AccountAction";
import { SIWELoginControl } from "./SIWELoginControl";
import { UIIcon } from "./UIIcon";
import "./AddressAccountActions.css";

export function AddressWatchButton({ address }: { address: string }) {
  const auth = useAuth();
  const identity = usePrivateIdentity();
  const [loginOpen, setLoginOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    setLoginOpen(false);
  }, [identity, address]);
  if (!auth.enabled) return null;
  return (
    <>
      <WatchButton
        key={`${identity}:${address}`}
        address={address}
        trigger={trigger}
        onLogin={() => setLoginOpen(true)}
      />
      {loginOpen && <WatchLoginDialog trigger={trigger} onClose={() => setLoginOpen(false)} />}
    </>
  );
}

function WatchButton({
  address,
  trigger,
  onLogin,
}: {
  address: string;
  trigger: RefObject<HTMLButtonElement | null>;
  onLogin: () => void;
}) {
  const auth = useAuth();
  const identity = usePrivateIdentity();
  const { t } = useTranslation();
  const query = useWatches();
  const client = useQueryClient();
  const action = useAccountAction();
  const watch = query.data?.find((item) => item.address.toLowerCase() === address.toLowerCase());
  const signedIn = auth.session.authenticated;
  const busy = auth.loading || action.pending || (signedIn && query.isPending);
  const limit = signedIn && !watch && (query.data?.length ?? 0) >= 5;
  const unknown = signedIn && (!query.data || query.isError);
  const label = t(
    busy
      ? "watchlist.loading"
      : unknown
        ? "watchlist.watchUnknown"
        : watch
          ? "watchlist.remove"
          : "watchlist.add",
  );
  async function toggle() {
    if (!signedIn) {
      onLogin();
      return;
    }
    if (unknown || action.pending || limit) return;
    await action.run(
      async (csrf, signal) => {
        const saved = watch
          ? (await deleteWatch(watch.id, csrf, signal), undefined)
          : await saveWatch(
              {
                address,
                label: "",
                kinds: ["transaction", "erc20", "erc721", "erc1155"],
                direction: "both",
                enabled: true,
              },
              csrf,
              signal,
            );
        if (signal.aborted) return;
        const queryKey = ["private-account", identity, "watches"];
        await client.cancelQueries({ queryKey });
        if (signal.aborted) return;
        client.setQueryData<AddressWatch[]>(queryKey, (items) => {
          const remaining = (items ?? []).filter(
            (item) => item.address.toLowerCase() !== address.toLowerCase(),
          );
          return saved ? [...remaining, saved] : remaining;
        });
      },
      t(watch ? "watchlist.removed" : "watchlist.added"),
    );
  }
  return (
    <>
      <button
        type="button"
        ref={trigger}
        className="address-qr-button address-watch-button"
        data-watching={!busy && Boolean(watch)}
        aria-pressed={unknown ? undefined : Boolean(watch)}
        aria-label={label}
        title={label}
        aria-busy={busy}
        disabled={auth.loading || action.pending || limit}
        aria-disabled={busy || Boolean(unknown) || limit}
        onClick={() => void toggle()}
      >
        <UIIcon
          icon={busy ? LoaderCircle : watch ? BellRing : Bell}
          className={busy ? "query-loading-spinner" : undefined}
          size={16}
        />
      </button>
      {action.error && <AccountActionError code={action.error} />}
      {action.success && <Message type="success">{action.success}</Message>}
      {(query.isError || limit) && (
        <span className="address-watch-feedback">
          <AccountActionError
            inline
            code={query.isError ? "activity_unavailable" : "watchlist_limit"}
          />
          {query.isError && (
            <button
              type="button"
              className="address-action"
              disabled={query.isFetching}
              onClick={() => void query.refetch()}
            >
              {t("watchlist.retry")}
            </button>
          )}
        </span>
      )}
    </>
  );
}

function WatchLoginDialog({
  trigger,
  onClose,
}: {
  trigger: RefObject<HTMLButtonElement | null>;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const auth = useAuth();
  const dialog = useRef<HTMLDialogElement>(null);
  // Login remounts the account-bound button; restore focus to the current trigger.
  const restoreFocus = useCallback(() => trigger.current?.focus(), [trigger]);
  useEffect(() => {
    const element = dialog.current!;
    element.showModal();
    // Use the native dialog boundary: a shell-owned Message can portal here.
    const containFocus = (event: KeyboardEvent) => {
      if (event.key !== "Tab") return;
      const controls = element.querySelectorAll<HTMLButtonElement>("button:not(:disabled)");
      const first = controls[0];
      const last = controls[controls.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last?.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first?.focus();
      }
    };
    element.addEventListener("keydown", containFocus);
    return () => {
      element.removeEventListener("keydown", containFocus);
      element.close();
      restoreFocus();
    };
  }, [restoreFocus]);
  return createPortal(
    <dialog
      ref={dialog}
      className="address-watch-dialog"
      aria-labelledby="watch-login-title"
      onCancel={onClose}
      onClick={(event) => {
        if (event.target !== event.currentTarget) return;
        const rect = event.currentTarget.getBoundingClientRect();
        if (
          event.clientX < rect.left ||
          event.clientX > rect.right ||
          event.clientY < rect.top ||
          event.clientY > rect.bottom
        )
          onClose();
      }}
    >
      <div className="qr-dialog-heading">
        <h2 id="watch-login-title">{t("watchlist.add")}</h2>
        <button
          type="button"
          className="dialog-close"
          aria-label={t("common.close")}
          onClick={onClose}
        >
          <UIIcon icon={X} size={16} />
        </button>
      </div>
      <SIWELoginControl full label={t("watchlist.watchSignIn")} />
      {auth.error && <Message type="error">{t(authErrorTranslationKey(auth.error))}</Message>}
    </dialog>,
    document.body,
  );
}

export function AddressExportLink({ address }: { address: string }) {
  const auth = useAuth();
  const { t } = useTranslation();
  if (!auth.enabled) return null;
  return (
    <div className="address-export-footer">
      <Link className="address-action" to="/address/$address/export" params={{ address }}>
        <UIIcon icon={Download} />
        {t("watchlist.export")}
      </Link>
    </div>
  );
}
