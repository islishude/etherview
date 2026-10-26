import { Check, Copy } from "lucide-react";
import { UIIcon } from "@/components/UIIcon";
import { useEffect, useState, type MouseEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

export async function copyTextToClipboard(value: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(value);
    return;
  }

  const textarea = document.createElement("textarea");
  textarea.value = value;
  textarea.style.position = "fixed";
  textarea.style.opacity = "0";
  textarea.setAttribute("readonly", "");
  document.body.appendChild(textarea);
  textarea.select();
  const copied = document.execCommand("copy");
  document.body.removeChild(textarea);
  if (!copied) throw new Error("fallback copy failed");
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

  useEffect(() => {
    if (!copied) return;
    const timeout = window.setTimeout(() => setCopied(false), 1200);
    return () => window.clearTimeout(timeout);
  }, [copied, copyRevision]);

  const copy = async (event: MouseEvent<HTMLButtonElement>) => {
    event.preventDefault();
    event.stopPropagation();
    try {
      await copyTextToClipboard(value);
      setCopied(true);
      setCopyRevision((revision) => revision + 1);
    } catch {
      setCopied(false);
    }
  };

  return (
    <button
      aria-label={copied ? t("common.copied") : t("common.copy")}
      className={`${className}${copied ? " copied" : ""}`}
      onClick={copy}
      title={copied ? t("common.copied") : t("common.copy")}
      type="button"
    >
      <UIIcon icon={copied ? Check : Copy} />
    </button>
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
