import { Message } from "@/components/Message";
import { UIIcon } from "@/components/UIIcon";
import { useEffect, useId, useRef, useState } from "react";
import { Download, Eye, EyeOff } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useAuth } from "@/auth/AuthProvider";
import { usePrivateIdentity, usePrivateLifetime } from "@/api/privateAccount";
import { SIWELoginControl } from "@/components/SIWELoginControl";
import { useTurnstile } from "@/components/TurnstileChallenge";
import { ApiError } from "@/api/client";
import { readBlob } from "@/api/consensus";

export function BlobActions({ hash, index }: { hash: string; index: string }) {
  const { t } = useTranslation();
  const auth = useAuth();
  const identity = usePrivateIdentity();
  if (auth.loading) return null;
  if (!auth.enabled || !auth.session.authenticated)
    return (
      <div className="blob-actions">
        <p>{t(auth.enabled ? "consensus.signInRequired" : "consensus.signInUnavailable")}</p>
        {auth.enabled && <SIWELoginControl label={t("consensus.signIn")} />}
      </div>
    );
  return (
    <AuthenticatedBlobActions key={`${identity}:${hash}:${index}`} hash={hash} index={index} />
  );
}

function AuthenticatedBlobActions({ hash, index }: { hash: string; index: string }) {
  const { t } = useTranslation();
  const auth = useAuth();
  const lifetime = usePrivateLifetime();
  const challenge = useTurnstile("blob_content");
  const cached = useRef<Awaited<ReturnType<typeof readBlob>> | null>(null);
  const running = useRef(false);
  useEffect(
    () => () => {
      cached.current = null;
    },
    [],
  );
  const previewID = useId();
  const [preview, setPreview] = useState("");
  const [busy, setBusy] = useState<"preview" | "download" | undefined>();
  const [failed, setFailed] = useState("");
  async function load(download: boolean) {
    if (!download && preview) {
      setPreview("");
      return;
    }
    if (running.current || (!cached.current && !challenge.ready)) return;
    const signal = lifetime();
    if (signal.aborted) return;
    running.current = true;
    const fetching = !cached.current;
    setBusy(download ? "download" : "preview");
    setFailed("");
    try {
      if (!cached.current) {
        const token = challenge.consume({ deferRefresh: true });
        const result = await readBlob(hash, index, auth.session.csrf_token!, signal, token);
        if (signal.aborted) return;
        cached.current = result;
      }
      const { data, filename } = cached.current;
      if (download) {
        const url = URL.createObjectURL(new Blob([data], { type: "application/octet-stream" }));
        const link = document.createElement("a");
        link.href = url;
        link.download = filename;
        link.click();
        setTimeout(() => URL.revokeObjectURL(url), 1000);
      } else
        setPreview(
          Array.from(new Uint8Array(data, 0, 1024), (byte) =>
            byte.toString(16).padStart(2, "0"),
          ).join(""),
        );
    } catch (error) {
      if (!signal.aborted)
        setFailed(
          error instanceof ApiError && error.code.startsWith("turnstile_")
            ? `turnstile.${error.code}`
            : "consensus.storageUnavailable",
        );
    } finally {
      running.current = false;
      if (!signal.aborted) {
        setBusy(undefined);
        if (fetching) challenge.refresh();
      }
    }
  }
  return (
    <div className="blob-actions">
      {!cached.current && challenge.challenge}
      <div className="blob-action-buttons" aria-busy={Boolean(busy)}>
        <button
          className="button secondary blob-action-button"
          type="button"
          disabled={Boolean(busy) || (!cached.current && !challenge.ready)}
          aria-expanded={Boolean(preview)}
          aria-controls={previewID}
          onClick={() => void load(false)}
        >
          {preview ? <UIIcon icon={EyeOff} size={16} /> : <UIIcon icon={Eye} size={16} />}
          {t(
            busy === "preview"
              ? "consensus.loadingPreview"
              : preview
                ? "consensus.hidePreview"
                : "consensus.preview",
          )}
        </button>
        <button
          className="button primary blob-action-button"
          type="button"
          disabled={Boolean(busy) || (!cached.current && !challenge.ready)}
          onClick={() => void load(true)}
        >
          <UIIcon icon={Download} size={16} />
          {t(busy === "download" ? "consensus.preparingDownload" : "consensus.download")}
        </button>
      </div>
      {failed && <Message type="error">{t(failed)}</Message>}
      <div id={previewID} hidden={!preview} className="blob-preview">
        <span>{t("consensus.hexPreview")}</span>
        <pre tabIndex={0} aria-label={t("consensus.hexPreview")}>
          {preview}
        </pre>
      </div>
    </div>
  );
}
