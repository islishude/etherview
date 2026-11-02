import "./DropdownChevron.css";
import { ChevronDown } from "lucide-react";
import { UIIcon } from "./UIIcon";

/** Decorative indicator; CSS follows the owning control's aria-expanded state. */
export function DropdownChevron({ className = "" }: { className?: string }) {
  return (
    <UIIcon
      icon={ChevronDown}
      size={14}
      strokeWidth={1.5}
      className={`dropdown-chevron ${className}`}
    />
  );
}
