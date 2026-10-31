import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { usePublicConfig } from "@/api/publicConfig";
import { usePrivateIdentity } from "@/api/privateAccount";
import "./TurnstileChallenge.css";

type Action = "verification" | "csv_export" | "blob_content";
type WidgetOptions = {
  sitekey: string;
  action: Action;
  theme: "light" | "dark";
  language: "en" | "zh-cn";
  size: "flexible";
  "response-field": false;
  callback: (token: string) => void;
  "expired-callback": () => void;
  "error-callback": () => void;
  "timeout-callback": () => void;
};
type TurnstileAPI = {
  render: (container: HTMLElement, options: WidgetOptions) => string;
  remove: (id: string) => void;
};
declare global {
  interface Window {
    turnstile?: TurnstileAPI;
  }
}
let loading: Promise<TurnstileAPI> | undefined;
function loadTurnstile(): Promise<TurnstileAPI> {
  if (window.turnstile) return Promise.resolve(window.turnstile);
  if (loading) return loading;
  loading = new Promise((resolve, reject) => {
    const script = document.createElement("script");
    script.src = "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit";
    script.async = true;
    const fail = () => {
      clearTimeout(timer);
      script.onload = script.onerror = null;
      script.remove();
      loading = undefined;
      reject(new Error("turnstile_unavailable"));
    };
    const timer = setTimeout(fail, 15_000);
    script.onerror = fail;
    script.onload = () => {
      if (!window.turnstile) {
        fail();
        return;
      }
      clearTimeout(timer);
      script.onload = script.onerror = null;
      resolve(window.turnstile);
    };
    document.head.append(script);
  });
  return loading;
}

// Tokens live only in this mounted form, fenced by the current private identity.
export function useTurnstile(action: Action) {
  const config = usePublicConfig();
  const identity = usePrivateIdentity();
  const settings = config.data?.turnstile;
  const enabled = settings?.enabled;
  const [generation, setGeneration] = useState(0);
  const [token, setToken] = useState("");
  const current = useRef<{ owner: string; token: string } | null>(null);
  const accept = useCallback(
    (value: string) => {
      current.current = value ? { owner: identity, token: value } : null;
      setToken(value);
    },
    [identity],
  );
  const ready =
    config.isSuccess && (!enabled || Boolean(token && current.current?.owner === identity));
  function refresh() {
    current.current = null;
    setToken("");
    setGeneration((n) => n + 1);
  }
  function consume({ deferRefresh = false } = {}) {
    if (!enabled) return undefined;
    const value = current.current;
    if (!value || value.owner !== identity) throw new Error("turnstile_invalid");
    current.current = null;
    setToken("");
    if (!deferRefresh) setGeneration((n) => n + 1);
    return value.token;
  }
  return {
    ready,
    consume,
    refresh,
    challenge:
      enabled && identity && settings ? (
        <TurnstileChallenge
          key={`${identity}:${action}:${generation}`}
          siteKey={settings.site_key}
          action={action}
          onToken={accept}
        />
      ) : null,
  };
}

function TurnstileChallenge({
  siteKey,
  action,
  onToken,
}: {
  siteKey: string;
  action: Action;
  onToken: (token: string) => void;
}) {
  const { t, i18n } = useTranslation();
  const container = useRef<HTMLDivElement>(null);
  const [attempt, setAttempt] = useState(0);
  const [status, setStatus] = useState("loading");
  const [theme, setTheme] = useState<"light" | "dark">(() =>
    document.documentElement.dataset.theme === "dark" ? "dark" : "light",
  );
  const language = i18n.resolvedLanguage?.startsWith("zh") ? "zh-cn" : "en";
  useEffect(() => {
    const observer = new MutationObserver(() =>
      setTheme(document.documentElement.dataset.theme === "dark" ? "dark" : "light"),
    );
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-theme"],
    });
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    let cancelled = false;
    let api: TurnstileAPI | undefined;
    let widget: string | undefined;
    onToken("");
    setStatus("loading");
    function invalid(next: string) {
      if (cancelled) return;
      onToken("");
      setStatus(next);
    }
    void loadTurnstile()
      .then((loaded) => {
        if (cancelled || !container.current) return;
        api = loaded;
        widget = api.render(container.current, {
          sitekey: siteKey,
          action,
          theme,
          language,
          size: "flexible",
          "response-field": false,
          callback: (value) => {
            if (cancelled) return;
            onToken(value);
            setStatus("verified");
          },
          "expired-callback": () => invalid("expired"),
          "error-callback": () => invalid("failed"),
          "timeout-callback": () => invalid("expired"),
        });
      })
      .catch(() => invalid("failed"));
    return () => {
      cancelled = true;
      onToken("");
      if (api && widget !== undefined) api.remove(widget);
    };
  }, [siteKey, action, theme, language, attempt, onToken]);
  return (
    <div className="turnstile-challenge">
      <div ref={container} />
      <p role="status">{t(`turnstile.${status}`)}</p>
      {(status === "failed" || status === "expired") && (
        <button type="button" className="button secondary" onClick={() => setAttempt((n) => n + 1)}>
          {t("turnstile.retry")}
        </button>
      )}
    </div>
  );
}
