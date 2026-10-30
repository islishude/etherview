import { lazy, Suspense } from "react";

const MessageView = lazy(() => import("./MessageView"));

/** Load transient feedback only when an operation has an outcome to show. */
export function Message({
  children,
  type = "info",
}: {
  children: string;
  type?: "success" | "error" | "warning" | "info";
}) {
  return (
    <Suspense fallback={null}>
      <MessageView type={type}>{children}</MessageView>
    </Suspense>
  );
}
