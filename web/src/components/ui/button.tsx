import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";
import { Slot } from "@radix-ui/react-slot";

const buttonVariants = cva(
  "inline-flex shrink-0 items-center justify-center gap-2 rounded-ui-sm text-sm font-medium whitespace-normal focus-visible:border-ring focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40",
  {
    variants: {
      variant: {
        default: "border border-transparent bg-primary text-primary-foreground hover:bg-primary/90",
        destructive:
          "bg-destructive/10 text-destructive hover:bg-destructive/20 focus-visible:ring-destructive/20 ",
        outline: "border border-input bg-card text-foreground hover:bg-accent",
        secondary: "border border-input bg-secondary text-secondary-foreground hover:bg-accent",
        ghost:
          "border border-transparent bg-transparent text-foreground hover:bg-accent hover:text-accent-foreground",
        link: "border-0 bg-transparent text-primary underline-offset-4 hover:underline",
      },
      size: {
        default: "min-h-9 px-3 py-2",
        xs: "h-6 gap-1 rounded-ui-sm px-2 text-xs has-[>svg]:px-1.5 ",
        sm: "min-h-8 gap-1.5 px-2.5 py-1 text-xs",
        lg: "h-10 rounded-ui-sm px-6 has-[>svg]:px-4",
        icon: "size-9",
        "icon-xs": "size-6 rounded-ui-sm ",
        "icon-sm": "size-8",
        "icon-lg": "size-10",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  },
);

function Button({
  className,
  variant = "default",
  size = "default",
  asChild = false,
  ...props
}: React.ComponentProps<"button"> &
  VariantProps<typeof buttonVariants> & {
    asChild?: boolean;
  }) {
  const Comp = asChild ? Slot : "button";

  return (
    <Comp
      data-slot="button"
      data-variant={variant}
      data-size={size}
      className={cn(buttonVariants({ variant, size, className }))}
      {...props}
    />
  );
}

export { Button, buttonVariants };
