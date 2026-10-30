import { Message } from "./Message";
import { Check, Copy } from "lucide-react";
import { UIIcon } from "@/components/UIIcon";
import { useEffect, useState, type MouseEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

export async function copyTextToClipboard(value: string): Promise<void> {
  if (!navigator.clipboard?.writeText) throw new Error("clipboard unavailable");
  await navigator.clipboard.writeText(value);
}

export function CopyButton({
  value,
  className = "copyable-copy-button",
}: {
  value: string;
  className?: string;
}) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  const [copyRevision, setCopyRevision] = useState(0);
  const [message, setMessage] = useState<"success" | "error">();

  useEffect(() => {
    if (!copied) return;
    const timeout = window.setTimeout(() => setCopied(false), 1200);
    return () => window.clearTimeout(timeout);
  }, [copied, copyRevision]);

  const copy = async (event: MouseEvent<HTMLButtonElement>) => {
    event.preventDefault();
    event.stopPropagation();
    setMessage(undefined);
    try {
      await copyTextToClipboard(value);
      setCopied(true);
      setMessage("success");
      setCopyRevision((revision) => revision + 1);
    } catch {
      setCopied(false);
      setMessage("error");
      setCopyRevision((revision) => revision + 1);
    }
  };

  return (
    <>
      {message && (
        <Message key={copyRevision} type={message}>
          {t(message === "success" ? "common.copied" : "common.copyFailed")}
        </Message>
      )}
      <button
        aria-label={copied ? t("common.copied") : t("common.copy")}
        className={`${className}${copied ? " copied" : ""}`}
        onClick={copy}
        title={copied ? t("common.copied") : t("common.copy")}
        type="button"
      >
        <UIIcon icon={copied ? Check : Copy} />
      </button>
    </>
  );
}

export function CopyableField({ children, value }: { children: ReactNode; value: string }) {
  return (
    <span className="copyable-field">
      <span className="copyable-content">{children}</span>
      <CopyButton value={value} />
    </span>
  );
}
