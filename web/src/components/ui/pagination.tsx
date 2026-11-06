import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

// Cursor ownership and translated accessible names stay with the data view.
export function Pagination({ className, ...props }: ComponentProps<"nav">) {
  return (
    <nav
      data-slot="pagination"
      className={cn("flex flex-wrap items-center justify-between gap-3", className)}
      {...props}
    />
  );
}
