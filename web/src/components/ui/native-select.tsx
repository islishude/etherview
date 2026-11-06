import type { ComponentProps } from "react";
import { DropdownChevron } from "@/components/DropdownChevron";
import { cn } from "@/lib/utils";
import "./native-select.css";

/** shadcn NativeSelect adapted for native size/multiple, forced colors and UIIcon. */
export function NativeSelect({ className, ...props }: ComponentProps<"select">) {
  const select = (
    <select
      data-slot="native-select"
      className={cn(
        "min-h-9 w-full min-w-0 rounded-ui-sm border border-input bg-card px-3 py-1.5 text-sm text-foreground disabled:cursor-not-allowed disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring aria-invalid:border-destructive",
        className,
      )}
      {...props}
    />
  );
  if (props.multiple || props.size) return select;
  return (
    <span className="select-control" data-slot="native-select-wrapper">
      {select}
      <DropdownChevron className="select-control-chevron" />
    </span>
  );
}
