import type { ReactNode } from "react";

export function PageHeading({
  title,
  description,
  mono = false,
  actions,
}: {
  title: string;
  description?: ReactNode;
  mono?: boolean;
  actions?: ReactNode;
}) {
  return (
    <header className="page-header">
      <div className="page-title-row">
        <h1>{title}</h1>
        {actions}
      </div>
      {description && (
        <div className={mono ? "page-description mono-wrap" : "page-description"}>
          {description}
        </div>
      )}
    </header>
  );
}
