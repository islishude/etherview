import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

// Native labels also wrap compound fields; preserve their layout and form semantics.
export function Label({ className, ...props }: ComponentProps<"label">) {
  return <label data-slot="label" className={cn("text-sm font-medium", className)} {...props} />;
}
