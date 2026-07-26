import { getAddress, isAddress, type Address, type Hex } from "viem";

import type { AuthChallenge } from "@/api/auth";
import { MAX_SIGN_MESSAGE_BYTES, normalizeChainID, WalletBoundaryError } from "./eip6963";

const CHALLENGE_ID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/u;
const CANONICAL_TIMESTAMP_PATTERN =
  /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/u;
const CANONICAL_SIWE_PATTERN =
  /^(?<scheme>[a-zA-Z][a-zA-Z0-9+.-]*):\/\/(?<domain>[^\s/]+) wants you to sign in with your Ethereum account:\n(?<address>0x[0-9a-fA-F]{40})\n\n\nURI: (?<uri>[^\r\n]+)\nVersion: (?<version>[^\r\n]+)\nChain ID: (?<chainID>[1-9][0-9]*)\nNonce: (?<nonce>[a-zA-Z0-9]{24})\nIssued At: (?<issuedAt>[^\r\n]+)\nExpiration Time: (?<expirationTime>[^\r\n]+)\nRequest ID: (?<requestID>[^\r\n]+)$/u;

/**
 * Encodes only the exact canonical challenge shape authored by Etherview's
 * EIP-4361 service. This is intentionally not a general message encoder.
 */
export function encodeCanonicalSIWEChallenge(
  challenge: AuthChallenge,
  account: Address,
  expectedChainID: string | undefined,
): Hex {
  try {
    if (
      typeof challenge !== "object" ||
      challenge === null ||
      typeof challenge.challenge_id !== "string" ||
      !CHALLENGE_ID_PATTERN.test(challenge.challenge_id) ||
      typeof challenge.message !== "string" ||
      typeof challenge.expires_at !== "string"
    ) {
      throw new WalletBoundaryError("INVALID_REQUEST");
    }

    const bytes = new TextEncoder().encode(challenge.message);
    if (bytes.length === 0 || bytes.length > MAX_SIGN_MESSAGE_BYTES) {
      throw new WalletBoundaryError("INVALID_REQUEST");
    }

    const origin = currentPublicOrigin();
    const expectedChain = normalizeChainID(expectedChainID);
    const fields = CANONICAL_SIWE_PATTERN.exec(challenge.message)?.groups;
    if (
      !fields ||
      expectedChain === undefined ||
      expectedChainID !== expectedChain
    ) {
      throw new WalletBoundaryError("INVALID_REQUEST");
    }

    const messageChain = fields.chainID;
    const messageAddress = fields.address;
    const issuedAt = canonicalTimestamp(fields.issuedAt);
    const expirationTime = canonicalTimestamp(fields.expirationTime);
    const responseExpiration = Date.parse(challenge.expires_at);
    const canonicalAccount = getAddress(account);
    if (
      fields.scheme !== origin.protocol.slice(0, -1) ||
      fields.domain !== origin.host ||
      fields.uri !== origin.origin ||
      fields.version !== "1" ||
      messageChain !== expectedChain ||
      normalizeChainID(messageChain) !== messageChain ||
      typeof messageAddress !== "string" ||
      !isAddress(messageAddress) ||
      getAddress(messageAddress) !== messageAddress ||
      getAddress(messageAddress) !== canonicalAccount ||
      fields.requestID !== challenge.challenge_id ||
      issuedAt === undefined ||
      expirationTime === undefined ||
      !Number.isFinite(responseExpiration) ||
      expirationTime !== responseExpiration ||
      issuedAt >= expirationTime ||
      expirationTime <= Date.now()
    ) {
      throw new WalletBoundaryError("INVALID_REQUEST");
    }

    let encoded = "0x";
    for (const byte of bytes) encoded += byte.toString(16).padStart(2, "0");
    return encoded as Hex;
  } catch (cause) {
    if (cause instanceof WalletBoundaryError) throw cause;
    throw new WalletBoundaryError("INVALID_REQUEST");
  }
}

function currentPublicOrigin(): URL {
  if (typeof window === "undefined") {
    throw new WalletBoundaryError("INVALID_REQUEST");
  }
  const origin = new URL(window.location.origin);
  if (
    (origin.protocol !== "https:" && origin.protocol !== "http:") ||
    origin.username !== "" ||
    origin.password !== "" ||
    origin.origin !== window.location.origin
  ) {
    throw new WalletBoundaryError("INVALID_REQUEST");
  }
  return origin;
}

function canonicalTimestamp(value: string | undefined): number | undefined {
  if (value === undefined || !CANONICAL_TIMESTAMP_PATTERN.test(value)) {
    return undefined;
  }
  const timestamp = Date.parse(value);
  return Number.isFinite(timestamp) && new Date(timestamp).toISOString() === value
    ? timestamp
    : undefined;
}
