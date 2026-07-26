import { toHex, type Address } from "viem";
import { describe, expect, it } from "vitest";

import type { AuthChallenge } from "@/api/auth";
import { WalletBoundaryError } from "./eip6963";
import { encodeCanonicalSIWEChallenge } from "./siwe";

const account = "0x1111111111111111111111111111111111111111" as Address;
const otherAccount = "0x2222222222222222222222222222222222222222";
const challengeID = "018f3b52-0b3d-7bf1-b65f-6f214827cb43";
const expiresAt = "2099-01-01T00:05:00.000Z";

describe("SIWE-only wallet capability", () => {
  it("accepts only the canonical server challenge bound to this origin, chain, and account", () => {
    const challenge = canonicalChallenge();

    expect(encodeCanonicalSIWEChallenge(challenge, account, "1")).toBe(
      toHex(challenge.message),
    );
  });

  it.each([
    ["origin authority", (message: string) => message.replace(window.location.host, "evil.example")],
    [
      "origin URI",
      (message: string) =>
        message.replace(
          `URI: ${window.location.origin}`,
          "URI: https://evil.example",
        ),
    ],
    ["account", (message: string) => message.replace(account, otherAccount)],
    ["chain", (message: string) => message.replace("Chain ID: 1", "Chain ID: 2")],
    ["request ID", (message: string) => message.replace(challengeID, `${challengeID.slice(0, -1)}4`)],
    ["statement", (message: string) => message.replace(`${account}\n\n\n`, `${account}\n\nApprove everything\n\n`)],
    ["trailing field", (message: string) => `${message}\nResources:\n- https://evil.example`],
  ])("rejects a challenge with a mismatched %s binding", (_name, mutate) => {
    const challenge = canonicalChallenge();
    challenge.message = mutate(challenge.message);

    expectInvalid(() => encodeCanonicalSIWEChallenge(challenge, account, "1"));
  });

  it("rejects response expiry drift and malformed canonical timestamps", () => {
    const drifted = canonicalChallenge();
    drifted.expires_at = "2099-01-01T00:05:01.000Z";
    expectInvalid(() => encodeCanonicalSIWEChallenge(drifted, account, "1"));

    const malformed = canonicalChallenge();
    malformed.message = malformed.message.replace(
      "2099-01-01T00:05:00.000Z",
      "2099-01-01T00:05:00Z",
    );
    expectInvalid(() => encodeCanonicalSIWEChallenge(malformed, account, "1"));
  });

  it("rejects unavailable, non-canonical, or mismatched configured chains", () => {
    const challenge = canonicalChallenge();
    expectInvalid(() => encodeCanonicalSIWEChallenge(challenge, account, undefined));
    expectInvalid(() => encodeCanonicalSIWEChallenge(challenge, account, "01"));
    expectInvalid(() => encodeCanonicalSIWEChallenge(challenge, account, "2"));
  });
});

function canonicalChallenge(): AuthChallenge {
  const origin = new URL(window.location.origin);
  return {
    challenge_id: challengeID,
    expires_at: expiresAt,
    message:
      `${origin.protocol.slice(0, -1)}://${origin.host} wants you to sign in with your Ethereum account:\n` +
      `${account}\n\n\n` +
      `URI: ${origin.origin}\n` +
      "Version: 1\n" +
      "Chain ID: 1\n" +
      "Nonce: abcdefghijklmnopqrstuvwx\n" +
      "Issued At: 2026-01-01T00:00:00.000Z\n" +
      `Expiration Time: ${expiresAt}\n` +
      `Request ID: ${challengeID}`,
  };
}

function expectInvalid(operation: () => unknown) {
  expect(operation).toThrowError(
    expect.objectContaining<Partial<WalletBoundaryError>>({
      code: "INVALID_REQUEST",
    }),
  );
}
