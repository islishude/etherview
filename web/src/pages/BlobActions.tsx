import { Message } from "@/components/Message";
import { UIIcon } from "@/components/UIIcon";
import { useEffect, useId, useRef, useState } from "react";
import { Download, Eye, EyeOff } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useAuth } from "@/auth/AuthProvider";
import { usePrivateIdentity, usePrivateLifetime } from "@/api/privateAccount";
import { SIWELoginControl } from "@/components/SIWELoginControl";
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
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState("");
  async function load() {
    if (preview) {
      setPreview("");
      return;
    }
    if (running.current) return;
    const signal = lifetime();
    if (signal.aborted) return;
    running.current = true;
    setBusy(true);
    setFailed("");
    try {
      if (!cached.current) {
        const result = await readBlob(hash, index, auth.session.csrf_token!, signal);
        if (signal.aborted) return;
        cached.current = result;
      }
      const { data } = cached.current;
      setPreview(
        Array.from(new Uint8Array(data, 0, 1024), (byte) =>
          byte.toString(16).padStart(2, "0"),
        ).join(""),
      );
    } catch {
      if (!signal.aborted) setFailed("consensus.storageUnavailable");
    } finally {
      running.current = false;
      if (!signal.aborted) {
        setBusy(false);
      }
    }
  }
  function download() {
    if (!cached.current || lifetime().aborted) return;
    const { data, filename } = cached.current;
    const url = URL.createObjectURL(new Blob([data], { type: "application/octet-stream" }));
    const link = document.createElement("a");
    link.href = url;
    link.download = filename;
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }
  return (
    <div className="blob-actions">
      <div className="blob-action-buttons" aria-busy={Boolean(busy)}>
        <button
          className="button secondary blob-action-button"
          type="button"
          disabled={busy}
          aria-expanded={Boolean(preview)}
          aria-controls={previewID}
          onClick={() => void load()}
        >
          {preview ? <UIIcon icon={EyeOff} size={16} /> : <UIIcon icon={Eye} size={16} />}
          {t(
            busy
              ? "consensus.loadingPreview"
              : preview
                ? "consensus.hidePreview"
                : "consensus.preview",
          )}
        </button>
      </div>
      {failed && <Message type="error">{t(failed)}</Message>}
      <div id={previewID} hidden={!preview} className="blob-preview">
        <span>{t("consensus.hexPreview")}</span>
        <pre tabIndex={0} aria-label={t("consensus.hexPreview")}>
          {preview}
        </pre>
        {preview && (
          <div className="blob-preview-actions">
            <button
              className="button secondary blob-action-button"
              type="button"
              aria-label={t("consensus.download")}
              title={t("consensus.download")}
              onClick={download}
            >
              <UIIcon icon={Download} size={16} />
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
