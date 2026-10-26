import { UIIcon } from "@/components/UIIcon";
import { Cpu } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

export function SystemAddressMarker() {
  const { t } = useTranslation();
  const id = useId();
  const [open, setOpen] = useState(false);
  const markerRef = useRef<HTMLSpanElement>(null);
  const tooltipRef = useRef<HTMLSpanElement>(null);

  useEffect(() => {
    if (!open) return;
    // The top layer keeps the hint visible inside scrolling tables and log cards.
    tooltipRef.current?.showPopover?.();
    const dismissOutside = (event: PointerEvent) => {
      if (event.target instanceof Node && !markerRef.current?.contains(event.target)) {
        setOpen(false);
      }
    };
    const dismissEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("pointerdown", dismissOutside);
    document.addEventListener("keydown", dismissEscape);
    return () => {
      document.removeEventListener("pointerdown", dismissOutside);
      document.removeEventListener("keydown", dismissEscape);
    };
  }, [open]);

  return (
    <span
      className="system-address-marker"
      onBlur={() => setOpen(false)}
      onMouseEnter={() => setOpen(true)}
      onMouseLeave={() => setOpen(false)}
      ref={markerRef}
    >
      <button
        aria-describedby={open ? id : undefined}
        aria-label={t("detail.systemAddress")}
        className="system-address-button"
        onClick={(event) => {
          event.stopPropagation();
          setOpen(true);
        }}
        onFocus={() => setOpen(true)}
        type="button"
      >
        <UIIcon icon={Cpu} size={14} />
      </button>
      {open && (
        <span
          className="system-address-tooltip"
          id={id}
          popover="manual"
          ref={tooltipRef}
          role="tooltip"
        >
          {t("detail.systemAddress")}
        </span>
      )}
    </span>
  );
}
