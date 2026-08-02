import { useQuery } from "@tanstack/react-query";
import { getAddress } from "viem";

import { ApiError, apiClient, requireEnvelope } from "@/api/client";
import type { components } from "@/api/schema.gen";

export type VerifiedContractArtifact = components["schemas"]["VerifiedContract"];
export type ContractProxyDetails = components["schemas"]["ProxyDetails"];
export type ContractProxyDetailsResponse = components["schemas"]["ProxyDetailsResponse"];
export type ContractProxyState = components["schemas"]["ProxyDetailStatus"];
export type ContractProxyUpgradeHistory =
  components["schemas"]["ProxyUpgradeHistory"];
export type ContractProxyInitializationHistory =
  components["schemas"]["ProxyInitializationHistory"];
export type ContractProxyManagementKind =
  components["schemas"]["ProxyManagementKind"];

type ApiMeta = components["schemas"]["Meta"];
type Address = components["schemas"]["Address"];
type Quantity = components["schemas"]["Quantity"];

export const DEFAULT_PROXY_HISTORY_LIMIT = 20;

export interface ContractProxyManagementArtifact {
  address: Address;
  kind: ContractProxyManagementKind;
  affectedProxyCount?: Quantity;
}

export interface ContractProxyView {
  detail: ContractProxyDetails;
  state: ContractProxyState;
  bindingId?: string;
  contractArtifactAddress: Address;
  implementationArtifactAddress?: Address;
  managementArtifact?: ContractProxyManagementArtifact;
}

export type ContractProxyUpgradePage = ContractProxyUpgradeHistory & {
  meta: ApiMeta;
  next_cursor?: string;
};

export type ContractProxyInitializationPage =
  ContractProxyInitializationHistory & {
    meta: ApiMeta;
    next_cursor?: string;
  };

export type ProxyReadIssueState =
  | "not_found"
  | "unavailable"
  | "failed"
  | "stale_cursor";

export interface ProxyReadIssue {
  state: ProxyReadIssueState;
  code: string;
  message: string;
  status?: number;
  requestId?: string;
  details?: unknown;
}

export class VerifiedArtifactIdentityError extends Error {
  constructor() {
    super("The verified artifact does not match the requested code identity");
    this.name = "VerifiedArtifactIdentityError";
  }
}

const unavailableErrorCodes = new Set([
  "capability_unavailable",
  "not_ready",
  "proxy_unavailable",
  "verification_target_unavailable",
  "verification_unavailable",
]);

export async function getVerifiedContractArtifact(
  address: string,
  expectedCodeHash?: string,
): Promise<VerifiedContractArtifact> {
  const artifact = requireEnvelope(
    await apiClient.GET("/contracts/{address}/verification", {
      params: { path: { address } },
    }),
  ).data;
  if (
    expectedCodeHash !== undefined &&
    !verifiedArtifactMatchesIdentity(artifact, address, expectedCodeHash)
  ) {
    throw new VerifiedArtifactIdentityError();
  }
  return artifact;
}

export async function getContractProxy(
  address: string,
): Promise<ContractProxyView> {
  return adaptContractProxy((await getContractProxyResponse(address)).data);
}

export async function getContractProxyResponse(
  address: string,
): Promise<ContractProxyDetailsResponse> {
  return requireEnvelope(
    await apiClient.GET("/contracts/{address}/proxy", {
      params: { path: { address } },
    }),
  );
}

export async function listContractProxyUpgrades(
  address: string,
  cursor?: string,
  limit = DEFAULT_PROXY_HISTORY_LIMIT,
): Promise<ContractProxyUpgradePage> {
  const response = requireEnvelope(
    await apiClient.GET("/contracts/{address}/proxy/upgrades", {
      params: { path: { address }, query: { cursor, limit } },
    }),
  );
  return {
    ...response.data,
    meta: response.meta,
    next_cursor: response.meta.next_cursor,
  };
}

export async function listContractProxyInitializations(
  address: string,
  cursor?: string,
  limit = DEFAULT_PROXY_HISTORY_LIMIT,
): Promise<ContractProxyInitializationPage> {
  const response = requireEnvelope(
    await apiClient.GET("/contracts/{address}/proxy/initializations", {
      params: { path: { address }, query: { cursor, limit } },
    }),
  );
  return {
    ...response.data,
    meta: response.meta,
    next_cursor: response.meta.next_cursor,
  };
}

export function useVerifiedContractArtifact(
  address: string,
  enabled = true,
  expectedCodeHash?: string,
) {
  const normalizedCodeHash = expectedCodeHash?.toLowerCase();
  return useQuery({
    queryKey: ["verified-contract-artifact", address, normalizedCodeHash ?? null],
    queryFn: () => getVerifiedContractArtifact(address, normalizedCodeHash),
    enabled: enabled && address.length > 0,
    retry: false,
    staleTime: 30_000,
  });
}

export function verifiedArtifactMatchesIdentity(
  artifact: VerifiedContractArtifact | undefined,
  address: string,
  expectedCodeHash: string | undefined,
): artifact is VerifiedContractArtifact {
  if (
    artifact === undefined ||
    expectedCodeHash === undefined ||
    !/^0x[0-9a-f]{64}$/iu.test(expectedCodeHash) ||
    artifact.code_hash.toLowerCase() !== expectedCodeHash.toLowerCase()
  ) {
    return false;
  }
  try {
    return getAddress(artifact.address) === getAddress(address);
  } catch {
    return false;
  }
}

export function useContractProxy(address: string, enabled = true) {
  return useQuery({
    queryKey: ["contract-proxy", address],
    queryFn: () => getContractProxy(address),
    enabled: enabled && address.length > 0,
    retry: false,
    staleTime: 5_000,
  });
}

export function useContractProxyUpgrades(
  address: string,
  cursor?: string,
  limit = DEFAULT_PROXY_HISTORY_LIMIT,
  enabled = true,
) {
  return useQuery({
    queryKey: ["contract-proxy", address, "upgrades", limit, cursor ?? null],
    queryFn: () => listContractProxyUpgrades(address, cursor, limit),
    enabled: enabled && address.length > 0,
    retry: false,
    staleTime: 0,
  });
}

export function useContractProxyInitializations(
  address: string,
  cursor?: string,
  limit = DEFAULT_PROXY_HISTORY_LIMIT,
  enabled = true,
) {
  return useQuery({
    queryKey: [
      "contract-proxy",
      address,
      "initializations",
      limit,
      cursor ?? null,
    ],
    queryFn: () => listContractProxyInitializations(address, cursor, limit),
    enabled: enabled && address.length > 0,
    retry: false,
    staleTime: 0,
  });
}

export function adaptContractProxy(
  detail: ContractProxyDetails,
): ContractProxyView {
  const supportedStandard = detail.pattern === "clone"
    ? detail.standard_version === undefined
    : detail.standard_version === "5.6.1";
  const exactBinding =
    detail.status === "verified" &&
    detail.evidence_state === "exact" &&
    supportedStandard &&
    detail.pattern !== undefined &&
    detail.pattern !== "unknown" &&
    typeof detail.binding_id === "string" &&
    detail.binding_id.length > 0;
  const implementationArtifactAddress =
    exactBinding && detail.implementation?.verification_state === "verified"
      ? detail.implementation.address
      : undefined;
  const management = detail.management;
  const managementArtifact =
    exactBinding && management?.target.verification_state === "verified"
      ? {
          address: management.target.address,
          kind: management.kind,
          affectedProxyCount: management.affected_proxy_count,
        }
      : undefined;

  return {
    detail,
    state: detail.status,
    bindingId: exactBinding ? detail.binding_id : undefined,
    contractArtifactAddress: detail.address,
    implementationArtifactAddress,
    managementArtifact,
  };
}

export function classifyProxyReadError(error: unknown): ProxyReadIssue {
  if (error instanceof ApiError) {
    let state: ProxyReadIssueState = "failed";
    if (error.code === "invalid_cursor") {
      state = "stale_cursor";
    } else if (error.code === "not_found" || error.status === 404) {
      state = "not_found";
    } else if (
      unavailableErrorCodes.has(error.code) ||
      error.status === 503
    ) {
      state = "unavailable";
    }
    return {
      state,
      code: error.code,
      message: error.message,
      status: error.status,
      requestId: error.requestId,
      details: error.details,
    };
  }

  return {
    state: "failed",
    code: "request_failed",
    message: "Explorer API request failed",
  };
}
