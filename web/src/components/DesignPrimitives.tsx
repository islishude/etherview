import type { ComponentPropsWithoutRef } from "react";

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
