import type { ComponentProps } from "react";
import * as TabsPrimitive from "@radix-ui/react-tabs";
import { Slot } from "@radix-ui/react-slot";
import { cn } from "@/lib/utils";

const listStyles = "flex min-w-0 gap-3 overflow-x-auto border-b border-border bg-transparent py-1";
const triggerStyles =
  "inline-flex shrink-0 items-center justify-center gap-1.5 whitespace-nowrap border-0 border-b-2 border-transparent bg-transparent px-2 py-2.5 text-sm font-medium text-muted-foreground no-underline hover:bg-accent hover:text-primary focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring disabled:cursor-not-allowed disabled:opacity-50 data-[state=active]:border-primary data-[state=active]:text-primary aria-selected:border-primary aria-selected:text-primary aria-[current=page]:border-primary aria-[current=page]:text-primary";

export function Tabs({ className, ...props }: ComponentProps<typeof TabsPrimitive.Root>) {
  return <TabsPrimitive.Root data-slot="tabs" className={cn("min-w-0", className)} {...props} />;
}

export function TabsList({ className, ...props }: ComponentProps<typeof TabsPrimitive.List>) {
  return (
    <TabsPrimitive.List data-slot="tabs-list" className={cn(listStyles, className)} {...props} />
  );
}

export function TabsTrigger({ className, ...props }: ComponentProps<typeof TabsPrimitive.Trigger>) {
  return (
    <TabsPrimitive.Trigger
      data-slot="tabs-trigger"
      className={cn(triggerStyles, className)}
      {...props}
    />
  );
}

// Router links keep real hrefs, modified-click behavior and existing URL/keyboard ownership.
export function NavigationTabs({ className, ...props }: ComponentProps<"nav">) {
  return <nav data-slot="tabs-list" className={cn(listStyles, className)} {...props} />;
}

export function NavigationTab({ className, ...props }: ComponentProps<typeof Slot>) {
  return <Slot data-slot="tabs-trigger" className={cn(triggerStyles, className)} {...props} />;
}
