import { useEffect, useRef, useState } from "react";
import { useAuth } from "@/auth/AuthProvider";

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
export async function clearBrowserPush(expectedCSRF: string) {
  const { clearBrowserPush: clear } = await import("./browserPushCleanup");
  await clear(expectedCSRF);
}
export async function endpointHash(endpoint: string) {
  const bytes = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(endpoint));
  return Array.from(new Uint8Array(bytes), (byte) => byte.toString(16).padStart(2, "0")).join("");
}
export function BrowserPushLifetime() {
  const { csrf_token: csrf, expires_at: expiry } = useAuth().session;
  const expired = usePushSessionExpired(expiry);
  const previous = useRef(csrf);
  useEffect(() => {
    const ended = previous.current !== csrf ? previous.current : expired ? csrf : undefined;
    previous.current = csrf;
    if (ended) void clearBrowserPush(ended).catch(() => {});
  }, [csrf, expired]);
  return null;
}
export function usePushSessionExpired(expiry: string | undefined) {
  const [expiredAt, setExpiredAt] = useState<string>();
  useEffect(() => {
    if (!expiry) return;
    let timer: ReturnType<typeof setTimeout>;
    const expiresAt = Date.parse(expiry);
    const check = () => {
      const remaining = expiresAt - Date.now();
      if (!Number.isFinite(remaining) || remaining <= 0) {
        setExpiredAt(expiry);
        return;
      }
      setExpiredAt(undefined);
      timer = setTimeout(check, Math.min(remaining, 2_147_483_647));
    };
    check();
    return () => clearTimeout(timer);
  }, [expiry]);
  return Boolean(expiry && expiredAt === expiry);
}
