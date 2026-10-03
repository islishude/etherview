import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { ShellIcon } from "./DesignPrimitives";

/** A disclosure of ordinary navigation links, preserving native Tab navigation. */
export function NavigationDisclosure({ label, children }: { label: string; children: ReactNode }) {
  const [open, setOpen] = useState(false);
  const id = useId();
  const root = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) return;
    const dismiss = (event: PointerEvent) => {
      if (event.target instanceof Node && !root.current?.contains(event.target)) setOpen(false);
    };
    document.addEventListener("pointerdown", dismiss);
    return () => document.removeEventListener("pointerdown", dismiss);
  }, [open]);

  return (
    <div
      className="navigation-disclosure"
      ref={root}
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false);
      }}
      onKeyDown={(event) => {
        if (event.key === "Escape" && open) {
          event.preventDefault();
          event.stopPropagation();
          setOpen(false);
          trigger.current?.focus();
        }
      }}
    >
      <button
        className="navigation-trigger"
        type="button"
        aria-expanded={open}
        aria-controls={id}
        ref={trigger}
        onClick={() => setOpen(!open)}
      >
        {label}
        <ShellIcon name="chevron" />
      </button>
      <div
        className="navigation-popover"
        id={id}
        hidden={!open}
        onClick={(event) => {
          if (event.target instanceof Element && event.target.closest("a")) {
            setOpen(false);
            trigger.current?.focus();
          }
        }}
      >
        {children}
      </div>
    </div>
  );
}
