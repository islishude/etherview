import type { LucideIcon } from "lucide-react";

/** Decorative icons inherit the control's color; the control owns its accessible name. */
export function UIIcon({
  icon: Icon,
  size = 16,
  className,
}: {
  icon: LucideIcon;
  size?: 14 | 16 | 20 | 24;
  className?: string;
}) {
  return (
    <Icon
      aria-hidden="true"
      focusable="false"
      className={className ? `ui-icon ${className}` : "ui-icon"}
      size={size}
      strokeWidth={2}
    />
  );
}
