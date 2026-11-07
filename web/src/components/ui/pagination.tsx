import type { ComponentProps } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import { Button } from "./button";

// The owning view supplies its visible row count and cursor reset.
export function Pagination({
  itemCount,
  onReset,
  hasPrevious,
  busy,
  className,
  ...props
}: ComponentProps<"nav"> & {
  itemCount: number;
  onReset: () => void;
  hasPrevious: boolean;
  busy?: boolean;
}) {
  const { t } = useTranslation();
  if (itemCount === 0)
    return hasPrevious ? (
      <div>
        <Button variant="outline" disabled={busy} type="button" onClick={onReset}>
          {t("actions.restartPagination")}
        </Button>
      </div>
    ) : null;
  return (
    <nav
      data-slot="pagination"
      className={cn("flex flex-wrap items-center justify-between gap-3", className)}
      {...props}
    />
  );
}
