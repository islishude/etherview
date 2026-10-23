import { useEffect, useRef, useState } from "react";

type Refetch = (options?: { cancelRefetch?: boolean }) => Promise<unknown>;

// Each resource owns one attempt for its current mismatch. The ref only guards
// dispatch and completion; state drives the rendered result.
export function useIdentityRecovery(
  mismatch: string | undefined,
  enabled: boolean,
  refetchOverview: Refetch,
  refetchResource: Refetch,
): boolean {
  const dispatched = useRef<{ key: string; settled: boolean } | undefined>(undefined);
  const [attempt, setAttempt] = useState<{
    key: string;
    phase: "recovering" | "exhausted";
  }>();
  useEffect(() => {
    if (!enabled || mismatch === undefined || dispatched.current?.key === mismatch) return;
    const current = { key: mismatch, settled: false };
    dispatched.current = current;
    setAttempt({ key: mismatch, phase: "recovering" });
    void Promise.allSettled([
      refetchOverview({ cancelRefetch: false }),
      refetchResource({ cancelRefetch: false }),
    ]).then(() => {
      current.settled = true;
      if (dispatched.current === current) {
        setAttempt({ key: mismatch, phase: "exhausted" });
      }
    });
    return () => {
      // Observer cleanup may abort an unfinished query (including StrictMode).
      // Allow the next setup to resume it without cancelling shared requests.
      if (dispatched.current === current && !current.settled) dispatched.current = undefined;
    };
  }, [enabled, mismatch, refetchOverview, refetchResource]);
  return (
    enabled &&
    mismatch !== undefined &&
    (attempt?.key !== mismatch || attempt.phase === "recovering")
  );
}
