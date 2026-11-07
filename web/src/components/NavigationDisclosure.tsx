import { Button } from "@/components/ui/button";
import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { DropdownChevron } from "./DropdownChevron";

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
      className="navigation-disclosure group/navigation"
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
      <Button
        variant="ghost"
        className="navigation-trigger aria-expanded:text-ui-brand group-has-[a.active]/navigation:border-b-ui-brand group-has-[a.active]/navigation:text-ui-brand"
        type="button"
        aria-expanded={open}
        aria-controls={id}
        ref={trigger}
        onClick={() => setOpen(!open)}
      >
        {label}
        <DropdownChevron />
      </Button>
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
