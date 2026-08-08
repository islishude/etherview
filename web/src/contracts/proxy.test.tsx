import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { PropsWithChildren } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "@/api/client";

import {
  adaptContractProxy,
  classifyProxyReadError,
  getContractProxy,
  listContractProxyInitializations,
  listContractProxyUpgrades,
  useVerifiedContractArtifact,
  type ContractProxyDetails,
} from "./proxy";

const proxyAddress = "0x1111111111111111111111111111111111111111";
const implementationAddress = "0x2222222222222222222222222222222222222222";
const managementAddress = "0x3333333333333333333333333333333333333333";
const oldImplementationAddress = "0x4444444444444444444444444444444444444444";
const hash = `0x${"ab".repeat(32)}`;
const oldHash = `0x${"cd".repeat(32)}`;
const bindingId = "018f3b52-0b3d-7bf1-b65f-6f214827cb41";
const opaqueCursor = "proxy/snapshot + page=2?fork=canonical/#";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("proxy API adapter", () => {
  it("automatically reads a verified artifact anonymously through the generated client", async () => {
    const storageWrite = vi.spyOn(window.localStorage, "setItem");
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValue(envelope(verifiedArtifact()));
    vi.stubGlobal("fetch", fetcher);
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });

    const { result } = renderHook(
      () => useVerifiedContractArtifact(proxyAddress),
      { wrapper: queryWrapper(queryClient) },
    );

	await waitFor(() => expect(result.current.data?.target.address).toBe(proxyAddress));
    expect(result.current.data?.abi?.[0]).toMatchObject({
      name: "value",
      stateMutability: "view",
      type: "function",
    });
    const [url, request] = fetcher.mock.calls[0] ?? [];
    expect(url).toBe(`/api/v1/contracts/${proxyAddress}/verification`);
    expect(request).toMatchObject({
      method: "GET",
      credentials: "same-origin",
      cache: "no-store",
    });
    const headers = new Headers(request?.headers);
    expect(headers.has("X-API-Key")).toBe(false);
    expect(headers.has("PAYMENT-SIGNATURE")).toBe(false);
    expect(headers.has("X-CSRF-Token")).toBe(false);
    expect(storageWrite).not.toHaveBeenCalled();
  });

  it("uses the expected code hash as part of the artifact cache identity", async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(envelope(verifiedArtifact()))
		.mockResolvedValueOnce(envelope({
			...verifiedArtifact(),
			target: { ...verifiedArtifact().target, code_hash: oldHash },
		}));
    vi.stubGlobal("fetch", fetcher);
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });

    const { result, rerender } = renderHook(
      ({ expectedCodeHash }: { expectedCodeHash: string }) =>
        useVerifiedContractArtifact(proxyAddress, true, expectedCodeHash),
      {
        initialProps: { expectedCodeHash: hash },
        wrapper: queryWrapper(queryClient),
      },
    );
	await waitFor(() => expect(result.current.data?.target.code_hash).toBe(hash));

    rerender({ expectedCodeHash: oldHash });
	await waitFor(() => expect(result.current.data?.target.code_hash).toBe(oldHash));

    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(
      queryClient.getQueryData([
        "verified-contract-artifact",
        proxyAddress,
        hash,
      ]),
	).toMatchObject({ target: { code_hash: hash } });
    expect(
      queryClient.getQueryData([
		"verified-contract-artifact",
		proxyAddress,
		oldHash,
	]),
	).toMatchObject({ target: { code_hash: oldHash } });
  });

  it("exposes implementation and management artifacts only for an exact verified binding", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn<typeof fetch>().mockResolvedValue(envelope(verifiedProxyDetail())),
    );

    const view = await getContractProxy(proxyAddress);

    expect(view).toMatchObject({
      state: "verified",
      bindingId,
      contractArtifactAddress: proxyAddress,
      implementationArtifactAddress: implementationAddress,
      managementArtifact: {
        address: managementAddress,
        kind: "proxy_admin",
        affectedProxyCount: "1",
      },
    });

    const clone = adaptContractProxy({
      ...verifiedProxyDetail(),
      mechanism: "eip1167",
      pattern: "clone",
      standard_version: undefined,
      proxy: {
        address: proxyAddress,
        code_hash: hash,
        verification_state: "unverified",
      },
      management: undefined,
    });
    expect(clone).toMatchObject({
      state: "verified",
      bindingId,
      implementationArtifactAddress: implementationAddress,
    });
    expect(clone.managementArtifact).toBeUndefined();

    const inexact = adaptContractProxy({
      ...verifiedProxyDetail(),
      status: "detected_unverified",
    });
    expect(inexact.state).toBe("detected_unverified");
    expect(inexact.bindingId).toBeUndefined();
    expect(inexact.implementationArtifactAddress).toBeUndefined();
    expect(inexact.managementArtifact).toBeUndefined();

    for (const detail of [
      { ...verifiedProxyDetail(), evidence_state: "partial" as const },
      { ...verifiedProxyDetail(), standard_version: undefined },
      { ...verifiedProxyDetail(), pattern: "unknown" as const },
    ]) {
      const adapted = adaptContractProxy(detail);
      expect(adapted.bindingId).toBeUndefined();
      expect(adapted.implementationArtifactAddress).toBeUndefined();
      expect(adapted.managementArtifact).toBeUndefined();
    }

    for (const state of ["unavailable", "failed"] as const) {
      const adapted = adaptContractProxy({
        address: proxyAddress,
        status: state,
        snapshot: snapshot(),
        evidence: [],
      });
      expect(adapted.state).toBe(state);
      expect(adapted.contractArtifactAddress).toBe(proxyAddress);
    }
  });

  it("round-trips snapshot cursors and exact decimal history values", async () => {
    const fetcher = vi.fn<typeof fetch>().mockImplementation(async (input) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname.endsWith("/upgrades")) {
        return envelope(upgradeHistory(), { next_cursor: opaqueCursor });
      }
      if (url.pathname.endsWith("/initializations")) {
        return envelope(initializationHistory(), { next_cursor: opaqueCursor });
      }
      return Response.json({}, { status: 404 });
    });
    vi.stubGlobal("fetch", fetcher);

    const upgrades = await listContractProxyUpgrades(
      proxyAddress,
      opaqueCursor,
      7,
    );
    const initializations = await listContractProxyInitializations(proxyAddress);

    expect(upgrades.next_cursor).toBe(opaqueCursor);
    expect(upgrades.items[0]?.new_implementation.address).toBe(
      implementationAddress,
    );
    expect(initializations.next_cursor).toBe(opaqueCursor);
    expect(initializations.items[0]?.version).toBe(
      "18446744073709551615",
    );

    const upgradeURL = new URL(String(fetcher.mock.calls[0]?.[0]), "http://localhost");
    expect(upgradeURL.pathname).toBe(
      `/api/v1/contracts/${proxyAddress}/proxy/upgrades`,
    );
    expect(Object.fromEntries(upgradeURL.searchParams)).toEqual({
      cursor: opaqueCursor,
      limit: "7",
    });
    const initializationURL = new URL(
      String(fetcher.mock.calls[1]?.[0]),
      "http://localhost",
    );
    expect(Object.fromEntries(initializationURL.searchParams)).toEqual({
      limit: "20",
    });
  });

  it("classifies stale cursors, unavailable reads, failures, and missing artifacts", () => {
    expect(classifyProxyReadError(apiError("invalid_cursor", 400))).toMatchObject({
      state: "stale_cursor",
      code: "invalid_cursor",
      requestId: "proxy-api-test",
    });
    expect(classifyProxyReadError(apiError("proxy_unavailable", 503))).toMatchObject({
      state: "unavailable",
      code: "proxy_unavailable",
    });
    expect(classifyProxyReadError(apiError("query_failed", 500))).toMatchObject({
      state: "failed",
      code: "query_failed",
    });
    expect(classifyProxyReadError(apiError("not_found", 404))).toMatchObject({
      state: "not_found",
      code: "not_found",
    });
    expect(classifyProxyReadError(new TypeError("network details"))).toEqual({
      state: "failed",
      code: "request_failed",
      message: "Explorer API request failed",
    });
  });
});

function queryWrapper(queryClient: QueryClient) {
  return function QueryWrapper({ children }: PropsWithChildren) {
    return (
      <QueryClientProvider client={queryClient}>
        {children}
      </QueryClientProvider>
    );
  };
}

function envelope(data: unknown, meta: Record<string, unknown> = {}) {
  return Response.json({
    data,
    meta: {
      request_id: "proxy-api-test",
      chain_id: "1",
      ...meta,
    },
  });
}

function apiError(code: string, status: number) {
  return new ApiError(status, {
    error: {
      code,
      message: `stable ${code}`,
      request_id: "proxy-api-test",
    },
  });
}

function snapshot() {
  return {
    chain_id: "1",
    block_number: "42",
    block_hash: hash,
  };
}

function verifiedProxyDetail(): ContractProxyDetails {
  return {
    address: proxyAddress,
    status: "verified",
    snapshot: snapshot(),
    mechanism: "eip1967",
    pattern: "transparent",
    standard_version: "5.6.1",
    evidence_state: "exact",
    confidence: "verified",
    binding_id: bindingId,
    proxy: {
      address: proxyAddress,
      code_hash: hash,
      verification_state: "verified",
      artifact_kind: "transparent_proxy",
      standard_version: "5.6.1",
    },
    implementation: {
      address: implementationAddress,
      code_hash: hash,
      verification_state: "verified",
    },
    management: {
      kind: "proxy_admin",
      target: {
        address: managementAddress,
        code_hash: hash,
        verification_state: "verified",
        artifact_kind: "proxy_admin",
        standard_version: "5.6.1",
      },
      affected_proxy_count: "1",
    },
    evidence: [],
  };
}

function verifiedArtifact() {
	return {
    kind: "verification_success",
    file_name: "Implementation.sol",
    contract_name: "Implementation",
    language: "solidity",
    compiler_version: "0.8.30",
    settings: {},
    sources: {},
    abi: [
      {
        type: "function",
        name: "value",
        stateMutability: "view",
        inputs: [],
        outputs: [{ name: "", type: "uint256" }],
      },
    ],
    compilation_artifacts: {},
    creation_code_artifacts: {},
    runtime_code_artifacts: {},
    libraries: {},
    is_blueprint: false,
		resolution: "exact_address",
		target: {
			chain_id: "1", address: proxyAddress, code_hash: hash,
			block_number: "42", block_hash: hash,
		},
		source: {
			address: proxyAddress, code_hash: hash, valid_from_block: "1",
			created_at: "2026-08-02T00:00:00Z",
		},
	};
}

function upgradeHistory() {
  return {
    proxy_address: proxyAddress,
    snapshot: snapshot(),
    coverage: { state: "complete", from_block: "1", to_block: "42" },
    items: [
      {
        change_type: "implementation",
        evidence_type: "event",
        old_implementation: {
          address: oldImplementationAddress,
          code_hash: oldHash,
          verification_state: "verified",
        },
        new_implementation: {
          address: implementationAddress,
          code_hash: hash,
          verification_state: "verified",
        },
        block_number: "40",
        block_hash: hash,
        block_timestamp: "2026-08-02T00:00:00Z",
        transaction_hash: hash,
        log_index: "0",
      },
    ],
  };
}

function initializationHistory() {
  return {
    contract_address: proxyAddress,
    snapshot: snapshot(),
    coverage: { state: "partial", from_block: "1", to_block: "42" },
    items: [
      {
        version: "18446744073709551615",
        block_number: "41",
        block_hash: hash,
        block_timestamp: "2026-08-02T00:00:00Z",
        transaction_hash: hash,
        log_index: "1",
        implementation: {
          address: implementationAddress,
          code_hash: hash,
          verification_state: "verified",
        },
      },
    ],
  };
}
