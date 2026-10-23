import { useCallback, useState } from "react";

// Cursor and generation travel together: restarting always selects a new cache entry.
export interface CursorRequest {
  readonly cursor?: string;
  readonly refreshGeneration: number;
}
export const firstPage: CursorRequest = { refreshGeneration: 0 };
let nextRefreshGeneration = 0;

export function useCursorHistory(identity: string) {
  const [state, setState] = useState({ identity, cursors: [""], refreshGeneration: 0 });
  // Commit identity changes immediately so A -> B -> A cannot resurrect A's old cursor.
  if (state.identity !== identity) {
    setState({ identity, cursors: [""], refreshGeneration: 0 });
  }
  const cursors = state.identity === identity ? state.cursors : [""];
  const refreshGeneration = state.identity === identity ? state.refreshGeneration : 0;
  const next = useCallback(
    (cursor: string | undefined) => {
      if (!cursor) return;
      setState((current) => ({
        identity,
        cursors: [...(current.identity === identity ? current.cursors : [""]), cursor],
        refreshGeneration: current.identity === identity ? current.refreshGeneration : 0,
      }));
    },
    [identity],
  );
  const previous = useCallback(() => {
    setState((current) => {
      const prior = current.identity === identity ? current.cursors : [""];
      return {
        identity,
        cursors: prior.length > 1 ? prior.slice(0, -1) : prior,
        refreshGeneration: current.identity === identity ? current.refreshGeneration : 0,
      };
    });
  }, [identity]);
  const reset = useCallback(() => {
    // Query caches outlive individual pagers, so a restart must also be unique
    // after remounting or returning to an earlier identity.
    const refreshGeneration = ++nextRefreshGeneration;
    setState({
      identity,
      cursors: [""],
      refreshGeneration,
    });
  }, [identity]);
  return {
    request: { cursor: cursors.at(-1) || undefined, refreshGeneration } satisfies CursorRequest,
    page: cursors.length,
    hasPrevious: cursors.length > 1,
    next,
    previous,
    reset,
  };
}
