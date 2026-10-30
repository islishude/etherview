import { Message } from "@/components/Message";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { useAuth } from "@/auth/AuthProvider";
import { ApiError } from "@/api/client";
import { usePrivateIdentity, usePrivateLifetime } from "@/api/privateAccount";

export function useAccountAction() {
  const auth = useAuth();
  const getSignal = usePrivateLifetime();
  const identity = usePrivateIdentity();
  const client = useQueryClient();
  const running = useRef(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  async function run(
    action: (csrf: string, signal: AbortSignal) => Promise<unknown>,
    message = "",
  ) {
    const signal = getSignal();
    const csrf = auth.session.csrf_token;
    if (!csrf || running.current || signal.aborted) return false;
    running.current = true;
    setPending(true);
    setError("");
    setSuccess("");
    try {
      await action(csrf, signal);
      if (signal.aborted) return false;
      await client.invalidateQueries({
        queryKey: ["private-account", identity],
      });
      if (!signal.aborted) setSuccess(message);
      return !signal.aborted;
    } catch (err) {
      if (!signal.aborted) setError(err instanceof ApiError ? err.code : "activity_unavailable");
      return false;
    } finally {
      running.current = false;
      if (!signal.aborted) setPending(false);
    }
  }
  return { run, pending, error, success };
}
export function AccountActionError({ code, inline = false }: { code: string; inline?: boolean }) {
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
  const Tag = inline ? "span" : Message;
  return code ? (
    <Tag {...(inline ? { className: "form-error", role: "alert" } : { type: "error" as const })}>
      {t(
        code === "turnstile_invalid" || code === "turnstile_unavailable"
          ? `turnstile.${code}`
          : `watchlist.errors.${known.includes(code) ? code : "activity_unavailable"}`,
      )}
    </Tag>
  ) : null;
}
