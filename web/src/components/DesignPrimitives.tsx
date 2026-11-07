import { Card } from "./ui/card";
import { cn } from "@/lib/utils";
import { Menu, Monitor, Moon, Sun, X } from "lucide-react";
import { UIIcon } from "@/components/UIIcon";
import type { ComponentPropsWithoutRef } from "react";

export function AppFrame({ className, ...props }: ComponentPropsWithoutRef<"div">) {
  return (
    <div
      className={cn("min-h-screen text-ui-text flex flex-col dark:[color-scheme:dark]", className)}
      {...props}
    />
  );
}

export function Surface({ className, ...props }: ComponentPropsWithoutRef<"section">) {
  return (
    <Card asChild>
      <section className={className} {...props} />
    </Card>
  );
}

export function ShellIcon({ name }: { name: "menu" | "close" | "moon" | "sun" | "monitor" }) {
  const icons = {
    menu: Menu,
    close: X,
    moon: Moon,
    sun: Sun,
    monitor: Monitor,
  };
  return <UIIcon icon={icons[name]} />;
}
