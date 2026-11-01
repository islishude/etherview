import { describe, expect, it } from "vitest";
import { testTopupIntent } from "./testFixtures";
import { mergeTopupObservation } from "./progress";

describe("top-up observations", () => {
  it("keeps terminal results and known hashes across stale responses", () => {
    const old = testTopupIntent({
      id: "topup",
      user_id: "user",
      amount_atomic: "1",
      network: "eip155:1",
      asset: "0x1111111111111111111111111111111111111111",
      recipient: "0x1111111111111111111111111111111111111111",
      payer: "0x1111111111111111111111111111111111111111",
      created_at: "2026-11-01T00:00:00Z",
      updated_at: "2026-11-01T00:00:00Z",
      expires_at: "2026-11-02T00:00:00Z",
      state: "settling",
      submission_progress: "confirming",
      transaction_hash: `0x${"1".repeat(64)}`,
    });
    const stale = { ...old, submission_progress: "submitting" as const, transaction_hash: null };
    expect(mergeTopupObservation(old, stale)).toBe(old);
    const credited = { ...old, state: "credited" as const };
    expect(mergeTopupObservation(old, credited)).toBe(credited);
    expect(mergeTopupObservation(credited, old)).toBe(credited);
  });
});
