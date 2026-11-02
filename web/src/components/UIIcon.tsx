import type { LucideIcon } from "lucide-react";

/** Decorative icons inherit the control's color; the control owns its accessible name. */
export function UIIcon({
  icon: Icon,
  size = 16,
  className = "",
  strokeWidth = 2,
}: {
  icon: LucideIcon;
  size?: 14 | 16 | 20 | 24;
  className?: string;
  strokeWidth?: 1.5 | 2;
}) {
  return (
    <Icon
      aria-hidden="true"
      focusable="false"
      className={`ui-icon ${className}`}
      size={size}
      strokeWidth={strokeWidth}
    />
  );
}
