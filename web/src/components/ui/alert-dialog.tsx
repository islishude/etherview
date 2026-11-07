import { useEffect, useId, useRef, type ReactElement, type ReactNode, type RefObject } from "react";
import { createPortal } from "react-dom";
import { Button } from "./button";

// Adapted from shadcn/ui Alert Dialog (MIT). Native modality preserves our CSP
// and Message top-layer boundary without a floating-layer styling runtime.
export function AlertDialog({
  title,
  description,
  cancelLabel,
  confirmLabel,
  onClose,
  onConfirm,
  children,
  action,
  destructive = false,
  returnFocus,
}: {
  title: string;
  description: string;
  cancelLabel: string;
  confirmLabel: string;
  onClose: () => void;
  onConfirm?: () => void;
  children?: ReactNode;
  action?: ReactElement;
  destructive?: boolean;
  returnFocus?: RefObject<HTMLElement | null>;
}) {
  const id = useId();
  const dialog = useRef<HTMLDialogElement>(null);
  const cancel = useRef<HTMLButtonElement>(null);
  const completed = useRef(false);
  useEffect(() => {
    const previous = document.activeElement;
    const restoreTarget = returnFocus?.current ?? previous;
    const element = dialog.current!;
    completed.current = false;
    element.showModal();
    cancel.current?.focus();
    return () => {
      element.close();
      const target = restoreTarget;
      if (target instanceof HTMLElement && target.isConnected)
        target.focus({ preventScroll: true });
    };
  }, [returnFocus]);
  return createPortal(
    <dialog
      ref={dialog}
      role="alertdialog"
      aria-modal="true"
      aria-labelledby={`${id}-title`}
      aria-describedby={`${id}-description`}
      data-slot="alert-dialog-content"
      className="fixed inset-0 m-auto max-h-[calc(100%-2rem)] w-[calc(100%-2rem)] max-w-lg overflow-y-auto rounded-lg border border-border bg-background p-6 text-foreground shadow-lg backdrop:bg-black/50"
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
      onKeyDown={(event) => {
        if (event.key === "Escape") {
          event.preventDefault();
          onClose();
          return;
        }
        if (event.key !== "Tab") return;
        const controls = dialog.current!.querySelectorAll<HTMLElement>(
          "button:not(:disabled), a[href]",
        );
        const first = controls[0];
        const last = controls[controls.length - 1];
        if (event.shiftKey && document.activeElement === first) {
          event.preventDefault();
          last?.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault();
          first?.focus();
        }
      }}
    >
      <div className="grid gap-4">
        <div className="grid gap-2">
          <h2 id={`${id}-title`} className="m-0 text-lg font-semibold">
            {title}
          </h2>
          <p id={`${id}-description`} className="m-0 text-sm text-muted-foreground">
            {description}
          </p>
        </div>
        {children}
        <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
          <Button ref={cancel} type="button" variant="outline" onClick={onClose}>
            {cancelLabel}
          </Button>
          <Button
            type={action ? undefined : "button"}
            variant={destructive ? "destructive" : "default"}
            asChild={Boolean(action)}
            onClick={(event) => {
              if (completed.current) {
                event.preventDefault();
                return;
              }
              completed.current = true;
              onClose();
              onConfirm?.();
            }}
          >
            {action ?? confirmLabel}
          </Button>
        </div>
      </div>
    </dialog>,
    document.body,
  );
}
