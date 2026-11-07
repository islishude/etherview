import type { ComponentProps } from "react";
import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const alertVariants = cva(
  "min-w-0 wrap-anywhere rounded-ui-md border border-border px-4 py-3 text-sm",
  {
    variants: {
      variant: {
        default: "bg-muted text-muted-foreground",
        destructive: "border-destructive/30 bg-destructive/5 text-destructive",
        warning: "border-warning/30 bg-warning/5 text-warning",
        success: "border-success/30 bg-success/5 text-success",
      },
    },
    defaultVariants: { variant: "default" },
  },
);

// Callers own live-region semantics: durable data notices must not become alerts.
export function Alert({
  className,
  variant,
  asChild = false,
  ...props
}: ComponentProps<"div"> & VariantProps<typeof alertVariants> & { asChild?: boolean }) {
  const Comp = asChild ? Slot : "div";
  return (
    <Comp data-slot="alert" className={cn(alertVariants({ variant }), className)} {...props} />
  );
}
