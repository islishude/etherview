import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Bell, BellRing } from "lucide-react";
import { useAuth } from "@/auth/AuthProvider";
import { usePrivateIdentity, usePrivateLifetime } from "@/api/privateAccount";
import { ApiError } from "@/api/client";
import {
  listPushSubscriptions,
  savePushSubscription,
  deletePushSubscription,
} from "@/api/watchlist";
import {
  browserPushSupported,
  clearBrowserPush,
  endpointHash,
  withPushLock,
  usePushSessionExpired,
} from "@/api/browserPush";

export function BrowserPushPanel() {
  const { t, i18n } = useTranslation();
  const auth = useAuth();
  const identity = usePrivateIdentity();
  const getSignal = usePrivateLifetime();
  const expired = usePushSessionExpired(auth.session.expires_at);
  const supported = browserPushSupported();
  const [current, setCurrent] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState(false);
  const [checking, setChecking] = useState(supported);
  const [permission, setPermission] = useState(supported ? Notification.permission : "default");
  const query = useQuery({
    queryKey: ["private-account", identity, "push"],
    queryFn: ({ signal }) => listPushSubscriptions(signal),
    enabled: Boolean(identity),
    retry: false,
  });
  useEffect(() => {
    if (!supported) return;
    let disposed = false;
    let permissionStatus: PermissionStatus | undefined;
    const refresh = () => setPermission(Notification.permission);
    const onVisible = () => {
      if (document.visibilityState === "visible") refresh();
    };
    refresh();
    window.addEventListener("focus", refresh);
    document.addEventListener("visibilitychange", onVisible);
    if (navigator.permissions?.query) {
      void navigator.permissions
        .query({ name: "notifications" })
        .then((status) => {
          if (disposed) return;
          permissionStatus = status;
          status.addEventListener("change", refresh);
          refresh();
        })
        .catch(() => {});
    }
    return () => {
      disposed = true;
      window.removeEventListener("focus", refresh);
      document.removeEventListener("visibilitychange", onVisible);
      permissionStatus?.removeEventListener("change", refresh);
    };
  }, [supported]);
  useEffect(() => {
    if (!supported || !query.data) return;
    let disposed = false;
    setChecking(true);
    void withPushLock(async () => {
      const registration = await navigator.serviceWorker.getRegistration("/");
      const subscription = await registration?.pushManager.getSubscription();
      if (!subscription) {
        if (!disposed) setCurrent("");
        return;
      }
      const hash = await endpointHash(subscription.endpoint);
      let item = query.data.items.find((item) => item.endpoint_hash === hash);
      // A different tab may have enabled push after this query completed.
      if (!item && !disposed) {
        const latest = await listPushSubscriptions(getSignal());
        item = latest.items.find((item) => item.endpoint_hash === hash);
      }
      if (!disposed) setCurrent(item?.id ?? "");
      // An old session must never silently subscribe a new login.
      if (!item && !disposed) await subscription.unsubscribe();
    })
      .catch(() => {
        if (!disposed) setError(true);
      })
      .finally(() => {
        if (!disposed) setChecking(false);
      });
    return () => {
      disposed = true;
    };
  }, [supported, query.data, getSignal]);
  async function enable() {
    if (!auth.session.csrf_token || pending) return;
    setPending(true);
    setError(false);
    const csrf = auth.session.csrf_token;
    const signal = getSignal();
    try {
      const granted =
        Notification.permission === "default"
          ? await Notification.requestPermission()
          : Notification.permission;
      setPermission(granted);
      if (granted !== "granted" || signal.aborted) return;
      await withPushLock(async () => {
        if (signal.aborted) return;
        const registration = await navigator.serviceWorker.register("/push-sw.js", {
          scope: "/",
          updateViaCache: "none",
        });
        await navigator.serviceWorker.ready;
        const key = Uint8Array.from(
          atob(query.data!.public_key.replaceAll("-", "+").replaceAll("_", "/")),
          (c) => c.charCodeAt(0),
        );
        const existing = await registration.pushManager.getSubscription();
        const subscription =
          existing ??
          (await registration.pushManager.subscribe({
            userVisibleOnly: true,
            applicationServerKey: key,
          }));
        const json = subscription.toJSON();
        try {
          const result = await savePushSubscription(
            {
              endpoint: subscription.endpoint,
              p256dh: json.keys!.p256dh!,
              auth: json.keys!.auth!,
              language: i18n.language.startsWith("zh") ? "zh" : "en",
            },
            csrf,
            signal,
          );
          if (!signal.aborted) setCurrent(result.id);
        } catch (cause) {
          if (!existing) await subscription.unsubscribe();
          throw cause;
        }
      });
      if (!getSignal().aborted) await query.refetch();
    } catch {
      if (!getSignal().aborted) setError(true);
    } finally {
      if (!getSignal().aborted) setPending(false);
    }
  }
  async function disable() {
    if (!auth.session.csrf_token || pending) return;
    setPending(true);
    setError(false);
    try {
      await deletePushSubscription(current, auth.session.csrf_token, getSignal());
      await clearBrowserPush(auth.session.csrf_token);
      setCurrent("");
      if (!getSignal().aborted) await query.refetch();
    } catch {
      if (!getSignal().aborted) setError(true);
    } finally {
      if (!getSignal().aborted) setPending(false);
    }
  }
  const sessionExpired = expired || (query.error instanceof ApiError && query.error.status === 401);
  const resolving = supported && !query.isError && (query.isPending || checking);
  const status =
    !identity || sessionExpired
      ? "sessionExpired"
      : !supported
        ? "unsupported"
        : permission === "denied"
          ? "denied"
          : query.data && !query.data.enabled
            ? "unconfigured"
            : resolving
              ? "checking"
              : current
                ? "enabled"
                : permission === "granted"
                  ? "allowed"
                  : "hint";
  const active = status === "enabled";
  const badge = active
    ? "on"
    : status === "allowed"
      ? "permissionGranted"
      : status === "denied"
        ? "blocked"
        : resolving
          ? "checking"
          : ["unsupported", "unconfigured", "sessionExpired"].includes(status)
            ? "unavailable"
            : "off";
  return (
    <section className="browser-push-panel" aria-label={t("browserPush.title")}>
      <div className="browser-push-heading">
        <span className="browser-push-icon" aria-hidden="true">
          {active ? <BellRing size={20} /> : <Bell size={20} />}
        </span>
        <h3>{t("browserPush.title")}</h3>
        <span className={`browser-push-badge${active ? " is-active" : ""}`}>
          {t(`browserPush.${badge}`)}
        </span>
      </div>
      <p id="browser-push-status" role="status">
        {t(`browserPush.${status}`)}
      </p>
      <button
        className="button secondary inline-button account-action browser-push-action"
        type="button"
        aria-describedby="browser-push-status"
        aria-busy={pending}
        disabled={
          pending ||
          resolving ||
          !identity ||
          sessionExpired ||
          (!current && (!supported || !query.data?.enabled || permission === "denied"))
        }
        onClick={() => void (current ? disable() : enable())}
      >
        {t(
          pending
            ? "browserPush.updating"
            : current
              ? "browserPush.disable"
              : permission === "granted"
                ? "browserPush.enable"
                : "browserPush.allow",
        )}
      </button>
      {(error || query.isError) && <p role="alert">{t("browserPush.error")}</p>}
    </section>
  );
}
