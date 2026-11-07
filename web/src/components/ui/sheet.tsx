import { useEffect, useId, useRef, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { X } from "lucide-react";
import { Button } from "./button";
import { UIIcon } from "../UIIcon";

// Repository-owned shadcn-style sheet hosted in the native modal top layer.
export function Sheet({
  title,
  closeLabel,
  children,
  footer,
  onClose,
}: {
  title: string;
  closeLabel: string;
  children: ReactNode;
  footer?: ReactNode;
  onClose: () => void;
}) {
  const id = useId();
  const dialog = useRef<HTMLDialogElement>(null);
  const close = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    const previous = document.activeElement;
    const element = dialog.current!;
    element.showModal();
    close.current?.focus();
    return () => {
      element.close();
      if (previous instanceof HTMLElement && previous.isConnected)
        previous.focus({ preventScroll: true });
    };
  }, []);
  return createPortal(
    <dialog
      ref={dialog}
      aria-labelledby={id}
      aria-modal="true"
      className="fixed inset-y-0 left-auto right-0 m-0 h-dvh max-h-none w-full max-w-lg border-0 border-l border-border bg-background p-0 text-foreground shadow-lg backdrop:bg-black/50"
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
    >
      <div className="flex h-full min-h-0 flex-col">
        <header className="flex items-center justify-between gap-3 border-b border-border p-4">
          <h2 id={id} className="m-0 text-lg font-semibold">
            {title}
          </h2>
          <Button ref={close} variant="ghost" size="icon" aria-label={closeLabel} onClick={onClose}>
            <UIIcon icon={X} />
          </Button>
        </header>
        <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain p-4">{children}</div>
        {footer && (
          <footer className="flex flex-wrap justify-end gap-2 border-t border-border p-4">
            {footer}
          </footer>
        )}
      </div>
    </dialog>,
    document.body,
  );
}
