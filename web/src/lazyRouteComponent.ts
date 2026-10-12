import { lazyRouteComponent as routerLazyRouteComponent } from "@tanstack/react-router";
import { lazy } from "react";

// React owns the suspended render. TanStack's component is rendered only after
// its importer settles, avoiding its conditional use(promise) retry path while
// retaining preload, import-error handling and stale-chunk reload behavior.
export const lazyRouteComponent: typeof routerLazyRouteComponent = (...args) => {
  const Route = routerLazyRouteComponent(...args);
  const Component = lazy(async () => {
    await Route.preload?.();
    return { default: Route };
  });
  return Object.defineProperty(Component, "preload", {
    get: () => Route.preload,
  }) as unknown as typeof Route;
};
