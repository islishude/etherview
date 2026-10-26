import { useEffect, useState, type RefObject } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { X } from "lucide-react";
import { UIIcon } from "@/components/UIIcon";

/** Adapt the pinned editor's search and go-to-line close controls without changing commands. */
export function SourceEditorIcons({ host }: { host: RefObject<HTMLDivElement | null> }) {
  const { t } = useTranslation();
  const [buttons, setButtons] = useState<HTMLButtonElement[]>([]);
  useEffect(() => {
    const element = host.current;
    if (!element) return;
    const synchronize = () => {
      const next = Array.from(
        element.querySelectorAll<HTMLButtonElement>(
          '.cm-search button[name="close"], .cm-dialog-close',
        ),
      );
      for (const button of next) {
        // CodeMirror owns the button, event handlers and focus restoration;
        // React owns only its decorative SVG. Remove the original text glyph.
        for (const child of Array.from(button.childNodes)) {
          if (child.nodeType === Node.TEXT_NODE) child.remove();
        }
        button.setAttribute("aria-label", t("common.close"));
        button.title = t("common.close");
      }
      setButtons((current) =>
        current.length === next.length && current.every((button, index) => button === next[index])
          ? current
          : next,
      );
    };
    const observer = new MutationObserver(synchronize);
    observer.observe(element, { childList: true, subtree: true });
    synchronize();
    return () => observer.disconnect();
  }, [host, t]);
  return buttons.map((button, index) => createPortal(<UIIcon icon={X} />, button, String(index)));
}
