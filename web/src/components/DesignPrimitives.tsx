import { ChevronDown, Menu, Monitor, Moon, Sun, X } from "lucide-react";
import { UIIcon } from "@/components/UIIcon";
import type { ComponentPropsWithoutRef, ReactNode } from "react";

export function AppFrame({ className, ...props }: ComponentPropsWithoutRef<"div">) {
  return (
    <div
      className={classes(
        "min-h-screen text-ui-text flex flex-col dark:[color-scheme:dark]",
        className,
      )}
      {...props}
    />
  );
}

export function Surface({ className, ...props }: ComponentPropsWithoutRef<"section">) {
  return (
    <section
      className={classes("rounded-ui-md border border-ui-line bg-ui-surface", className)}
      {...props}
    />
  );
}

function classes(base: string, extra: string | undefined): string {
  return extra ? `${base} ${extra}` : base;
}

export function ShellIcon({
  name,
}: {
  name: "menu" | "close" | "chevron" | "moon" | "sun" | "monitor";
}) {
  const icons = {
    menu: Menu,
    close: X,
    chevron: ChevronDown,
    moon: Moon,
    sun: Sun,
    monitor: Monitor,
  };
  return <UIIcon icon={icons[name]} />;
}

export function PageHeading({
  title,
  description,
  mono = false,
}: {
  title: string;
  description?: ReactNode;
  mono?: boolean;
}) {
  return (
    <header className="page-header">
      <h1>{title}</h1>
      {description && (
        <div className={mono ? "page-description mono-wrap" : "page-description"}>
          {description}
        </div>
      )}
    </header>
  );
}
