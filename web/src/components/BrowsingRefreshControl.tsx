import { createContext, useContext, type ReactNode } from "react";

export const BrowsingRefreshControlContext = createContext<ReactNode>(null);

export function BrowsingRefreshControl() {
  return useContext(BrowsingRefreshControlContext);
}
