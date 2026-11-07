import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

// Keep real form inputs: native validity, FormData, label activation and forced colors.
export function Checkbox({ className, ...props }: Omit<ComponentProps<"input">, "type">) {
  return (
    <input
      {...props}
      type="checkbox"
      data-slot="checkbox"
      className={cn(
        "size-4 shrink-0 accent-primary focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring disabled:cursor-not-allowed disabled:opacity-50",
        className,
      )}
    />
  );
}

export function Radio({ className, ...props }: Omit<ComponentProps<"input">, "type">) {
  return (
    <input
      {...props}
      type="radio"
      data-slot="radio"
      className={cn(
        "size-4 shrink-0 accent-primary focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring disabled:cursor-not-allowed disabled:opacity-50",
        className,
      )}
    />
  );
}
