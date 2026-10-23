import { useEffect, useRef, useState } from "react";
import { useAuth } from "@/auth/AuthProvider";
import { usePrivateIdentity } from "./privateAccount";

export function browserPushSupported() {
  return (
    window.isSecureContext &&
    "serviceWorker" in navigator &&
    "PushManager" in window &&
    "Notification" in window
  );
}
export async function withPushLock<T>(operation: () => Promise<T>): Promise<T> {
  if ("locks" in navigator) return navigator.locks.request("etherview-browser-push", operation);
  return operation();
}
export async function clearBrowserPush() {
  if (!("serviceWorker" in navigator)) return;
  await withPushLock(async () => {
    const registration = await navigator.serviceWorker.getRegistration("/");
    if (!registration?.active?.scriptURL.endsWith("/push-sw.js")) return;
    const subscription = await registration.pushManager.getSubscription();
    await subscription?.unsubscribe();
    for (const notification of await registration.getNotifications()) notification.close();
  });
}
export async function endpointHash(endpoint: string) {
  const bytes = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(endpoint));
  return Array.from(new Uint8Array(bytes), (byte) => byte.toString(16).padStart(2, "0")).join("");
}
export function BrowserPushLifetime() {
  const identity = usePrivateIdentity();
  const expired = usePushSessionExpired();
  const previous = useRef(identity);
  useEffect(() => {
    const old = previous.current;
    previous.current = identity;
    if (old && old !== identity) void clearBrowserPush().catch(() => {});
  }, [identity]);
  useEffect(() => {
    if (identity && expired) void clearBrowserPush().catch(() => {});
  }, [identity, expired]);
  return null;
}
export function usePushSessionExpired() {
  const auth = useAuth();
  const expiry = auth.session.expires_at;
  const [expired, setExpired] = useState(false);
  useEffect(() => {
    if (!expiry) return;
    let timer: ReturnType<typeof setTimeout>;
    const expiresAt = Date.parse(expiry);
    const check = () => {
      const remaining = expiresAt - Date.now();
      if (!Number.isFinite(remaining) || remaining <= 0) {
        setExpired(true);
        return;
      }
      setExpired(false);
      timer = setTimeout(check, Math.min(remaining, 2_147_483_647));
    };
    check();
    return () => clearTimeout(timer);
  }, [expiry]);
  return expired;
}
