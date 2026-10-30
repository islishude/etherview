import { Message } from "@/components/Message";
import { UIIcon } from "@/components/UIIcon";
import { useId, useState } from "react";
import { Download, Eye, EyeOff } from "lucide-react";
import { useTranslation } from "react-i18next";
import { readBlob } from "@/api/consensus";

export function BlobActions({ hash, index }: { hash: string; index: string }) {
  const { t } = useTranslation();
  const previewID = useId();
  const [preview, setPreview] = useState("");
  const [busy, setBusy] = useState<"preview" | "download" | undefined>();
  const [failed, setFailed] = useState(false);
  async function load(download: boolean) {
    if (!download && preview) {
      setPreview("");
      return;
    }
    setBusy(download ? "download" : "preview");
    setFailed(false);
    try {
      const { data, filename } = await readBlob(hash, index);
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
    } catch {
      setFailed(true);
    } finally {
      setBusy(undefined);
    }
  }
  return (
    <div className="blob-actions">
      <div className="blob-action-buttons" aria-busy={Boolean(busy)}>
        <button
          className="button secondary blob-action-button"
          type="button"
          disabled={Boolean(busy)}
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
          disabled={Boolean(busy)}
          onClick={() => void load(true)}
        >
          <UIIcon icon={Download} size={16} />
          {t(busy === "download" ? "consensus.preparingDownload" : "consensus.download")}
        </button>
      </div>
      {failed && <Message type="error">{t("consensus.storageUnavailable")}</Message>}
      <div id={previewID} hidden={!preview} className="blob-preview">
        <span>{t("consensus.hexPreview")}</span>
        <pre tabIndex={0} aria-label={t("consensus.hexPreview")}>
          {preview}
        </pre>
      </div>
    </div>
  );
}
