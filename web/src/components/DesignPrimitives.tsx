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

// Keep shell icons local so route-only icon chunks remain lazy.
export function ShellIcon({ name }: { name: "menu" | "close" | "chevron" | "moon" | "sun" }) {
  const paths = {
    menu: "M4 6h16M4 12h16M4 18h16",
    close: "M6 6l12 12M6 18L18 6",
    chevron: "m6 9 6 6 6-6",
    moon: "M20 14A8 8 0 0 1 10 4a8.5 8.5 0 1 0 10 10Z",
    sun: "M12 2v2M12 20v2M2 12h2M20 12h2M5 5l1.5 1.5M17.5 17.5 19 19M5 19l1.5-1.5M17.5 6.5 19 5M16 12a4 4 0 1 1-8 0 4 4 0 0 1 8 0",
  };
  return (
    <svg
      width="17"
      height="17"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d={paths[name]} />
    </svg>
  );
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
