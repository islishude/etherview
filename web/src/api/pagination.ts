import { useCallback, useState } from "react";
import { useRefreshPager } from "./browsingHooks";

// Cursor and generation travel together: restarting always selects a new cache entry.
export interface CursorRequest {
  readonly cursor?: string;
  readonly identity?: string;
  readonly refreshGeneration: number;
}
export const firstPage: CursorRequest = { refreshGeneration: 0 };
let nextRefreshGeneration = 0;
export const useBrowsingCursorHistory = useCursorHistory;

export function useCursorHistory(identity: string, maxItems = Infinity) {
  const [state, setState] = useState({
    identity,
    cursors: [{ cursor: "", offset: 0 }],
    refreshGeneration: 0,
  });
  // Commit identity changes immediately so A -> B -> A cannot resurrect A's old cursor.
  if (state.identity !== identity) {
    setState({ identity, cursors: [{ cursor: "", offset: 0 }], refreshGeneration: 0 });
  }
  const cursors = state.identity === identity ? state.cursors : [{ cursor: "", offset: 0 }];
  const refreshGeneration = state.identity === identity ? state.refreshGeneration : 0;
  const next = useCallback(
    (cursor: string | undefined, itemCount = 0) => {
      if (!cursor) return;
      setState((current) => {
        const prior = current.identity === identity ? current.cursors : [{ cursor: "", offset: 0 }];
        const offset = (prior.at(-1)?.offset ?? 0) + itemCount;
        if (offset >= maxItems) return current;
        return {
          identity,
          cursors: [...prior, { cursor, offset }],
          refreshGeneration: current.identity === identity ? current.refreshGeneration : 0,
        };
      });
    },
    [identity, maxItems],
  );
  const previous = useCallback(() => {
    setState((current) => {
      const prior = current.identity === identity ? current.cursors : [{ cursor: "", offset: 0 }];
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
      cursors: [{ cursor: "", offset: 0 }],
      refreshGeneration,
    });
  }, [identity]);
  const invalid = useRefreshPager(identity, reset);
  return {
    request: {
      cursor: cursors.at(-1)?.cursor || undefined,
      refreshGeneration,
      identity,
    } satisfies CursorRequest,
    remaining: Math.max(0, maxItems - (cursors.at(-1)?.offset ?? 0)),
    page: cursors.length,
    invalid,
    hasPrevious: !invalid && cursors.length > 1,
    next: invalid ? () => {} : next,
    previous: invalid ? () => {} : previous,
    reset,
  };
}
