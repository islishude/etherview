import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { CircleCheck, CircleX, Info, TriangleAlert, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { UIIcon } from "./UIIcon";
import "./Message.css";

type MessageType = "success" | "error" | "warning" | "info";
const icons = { success: CircleCheck, error: CircleX, warning: TriangleAlert, info: Info };
const activeMessages = new Set<string>();
const stacks = new WeakMap<HTMLElement, { element: HTMLDivElement; users: number }>();

/** Operation feedback lives with its caller, so navigation/account teardown removes it. */
export default function MessageView({
  children,
  type = "info",
}: {
  children: string;
  type?: MessageType;
}) {
  return <MessageInstance key={`${type}:${children}`} type={type} text={children} />;
}

function MessageInstance({ type, text }: { type: MessageType; text: string }) {
  const { t } = useTranslation();
  const anchor = useRef<HTMLSpanElement>(null);
  const previousFocus = useRef<HTMLElement | null>(null);
  const [host, setHost] = useState<HTMLDivElement>();
  const [dismissed, setDismissed] = useState(false);
  const [hovered, setHovered] = useState(false);
  const [focused, setFocused] = useState(false);

  useLayoutEffect(() => {
    if (dismissed) return;
    const key = `${type}:${text}`;
    // Auth failures can be observed by both the shell and the current page.
    if (activeMessages.has(key)) return;
    activeMessages.add(key);
    // Native modal dialogs make body siblings inert. Keep their messages in the
    // dialog's top layer and keyboard scope, including failures emitted by the shell.
    const dialogs = document.querySelectorAll<HTMLDialogElement>("dialog[open]");
    const scope =
      anchor.current?.closest("dialog") ?? dialogs.item(dialogs.length - 1) ?? document.body;
    let stack = stacks.get(scope);
    if (!stack) {
      const element = document.createElement("div");
      element.className = "message-stack";
      scope.appendChild(element);
      stack = { element, users: 0 };
      stacks.set(scope, stack);
    }
    stack.users++;
    setHost(stack.element);
    const observer =
      scope instanceof HTMLDialogElement
        ? new MutationObserver(() => {
            if (!scope.isConnected || !scope.open) setDismissed(true);
          })
        : undefined;
    observer?.observe(document.body, {
      childList: true,
      subtree: true,
      attributes: true,
      attributeFilter: ["open"],
    });
    return () => {
      observer?.disconnect();
      activeMessages.delete(key);
      if (--stack.users === 0) {
        stack.element.remove();
        stacks.delete(scope);
      }
    };
  }, [dismissed, text, type]);

  useEffect(() => {
    if (!host || dismissed || hovered || focused) return;
    const timer = window.setTimeout(() => setDismissed(true), 3000);
    return () => window.clearTimeout(timer);
  }, [host, dismissed, hovered, focused]);

  return (
    <>
      <span ref={anchor} hidden />
      {host &&
        !dismissed &&
        createPortal(
          <div
            className={`message message-${type}`}
            onMouseEnter={() => setHovered(true)}
            onMouseLeave={() => setHovered(false)}
            onFocus={(event) => {
              if (event.relatedTarget instanceof HTMLElement)
                previousFocus.current = event.relatedTarget;
              setFocused(true);
            }}
            onBlur={() => setFocused(false)}
          >
            <UIIcon icon={icons[type]} size={20} />
            <span
              className="message-text"
              role={type === "error" ? "alert" : "status"}
              aria-atomic="true"
            >
              {text}
            </span>
            <button
              type="button"
              className="message-close"
              aria-label={t("common.close")}
              onClick={(event) => {
                event.preventDefault();
                event.stopPropagation();
                setDismissed(true);
                if (previousFocus.current?.isConnected)
                  previousFocus.current.focus({ preventScroll: true });
              }}
            >
              <UIIcon icon={X} size={16} />
            </button>
          </div>,
          host,
        )}
    </>
  );
}
