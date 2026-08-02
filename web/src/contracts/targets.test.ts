import { getAddress } from "viem";
import { describe, expect, it, vi } from "vitest";

import { WalletBoundaryError } from "@/wallet/eip6963";

import {
  assertFreshInteractionFence,
  assertInteractionFunctionAllowed,
  buildContractInteractionTargets,
  captureInteractionFence,
  type ContractInteractionTarget,
  InteractionFenceError,
  type InteractionFenceErrorCode,
  isInteractionFunctionAllowed,
  PROXIABLE_UUID_SIGNATURE,
  type ProxyDetails,
  type ProxyDetailsResponse,
  refreshInteractionTarget,
  submitFencedTransaction,
  type WalletInteractionSession,
} from "./targets";

const PROXY = getAddress("0xdc64a140aa3e981100a9beca4e685f962f0cf6c9");
const IMPLEMENTATION = getAddress("0x5fbdb2315678afecb367f032d93f642f64180aa3");
const ADMIN = getAddress("0xe7f1725e7734ce288f8367e1bb143e90bb3f0512");
const BEACON = getAddress("0x9fe46736679d2d9a65f0992f2272de9f3c7fa6e0");
const ACCOUNT = getAddress("0x70997970c51812dc3a010c7d01b50e0d17dc79c8");
const OTHER = getAddress("0x3c44cdddb6a900fa2b585dd299e03d12fa4293bc");
const PROXY_HASH = `0x${"11".repeat(32)}`;
const IMPLEMENTATION_HASH = `0x${"22".repeat(32)}`;
const ADMIN_HASH = `0x${"33".repeat(32)}`;
const BEACON_HASH = `0x${"44".repeat(32)}`;
const BLOCK_HASH = `0x${"55".repeat(32)}`;
const TRANSACTION_HASH = `0x${"66".repeat(32)}`;
const BINDING_ID = "11111111-1111-4111-8111-111111111111";
const NEXT_BINDING_ID = "22222222-2222-4222-8222-222222222222";

const wallet: WalletInteractionSession = Object.freeze({
  uuid: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
  account: ACCOUNT,
  chainID: "31337",
  revision: 7,
});

describe("contract interaction targets", () => {
  it("always returns one frozen direct contract target and fails closed without an exact binding", () => {
    const unverified = proxyDetails("uups", {
      status: "detected_unverified",
      binding_id: undefined,
    });

    for (const details of [undefined, unverified, { ...unverified, status: "verified" as const }]) {
      const targets = buildContractInteractionTargets(PROXY.toLowerCase(), details);
      expect(targets).toEqual([
        {
          kind: "contract",
          transactionTarget: PROXY,
          abiAddress: PROXY,
          supportsWrites: true,
          requiresFreshBinding: false,
        },
      ]);
      expect(Object.isFrozen(targets)).toBe(true);
      expect(Object.isFrozen(targets[0])).toBe(true);
    }
  });

  it("builds UUPS as-proxy and UUID-only direct implementation targets", () => {
    const targets = buildContractInteractionTargets(PROXY, proxyDetails("uups"));
    expect(targets.map(({ kind }) => kind)).toEqual([
      "contract",
      "implementation_as_proxy",
      "uups_implementation_direct",
    ]);

    const asProxy = targetOfKind(targets, "implementation_as_proxy");
    expect(asProxy).toMatchObject({
      transactionTarget: PROXY,
      abiAddress: IMPLEMENTATION,
      proxyAddress: PROXY,
      proxyCodeHash: PROXY_HASH,
      proxyChainID: "31337",
      bindingId: BINDING_ID,
      proxyPattern: "uups",
      supportsWrites: true,
      requiresFreshBinding: true,
    });
    const direct = targetOfKind(targets, "uups_implementation_direct");
    expect(direct).toMatchObject({
      transactionTarget: IMPLEMENTATION,
      abiAddress: IMPLEMENTATION,
      supportsWrites: false,
    });
    expect(isInteractionFunctionAllowed(asProxy, PROXIABLE_UUID_SIGNATURE)).toBe(false);
    expect(isInteractionFunctionAllowed(asProxy, "upgradeToAndCall(address,bytes)")).toBe(true);
    expect(isInteractionFunctionAllowed(direct, PROXIABLE_UUID_SIGNATURE)).toBe(true);
    expect(isInteractionFunctionAllowed(direct, PROXIABLE_UUID_SIGNATURE, true)).toBe(false);
    expect(isInteractionFunctionAllowed(direct, "upgradeToAndCall(address,bytes)")).toBe(false);
    expect(() => assertInteractionFunctionAllowed(asProxy, PROXIABLE_UUID_SIGNATURE))
      .toThrow(expect.objectContaining({ code: "FUNCTION_NOT_ALLOWED" }));
    expect(() => assertInteractionFunctionAllowed(direct, PROXIABLE_UUID_SIGNATURE, true))
      .toThrow(expect.objectContaining({ code: "FUNCTION_NOT_ALLOWED" }));
  });

  it("requires an exact verified proxy code identity before creating bound targets", () => {
    for (const proxy of [
      undefined,
      { ...identity(PROXY, PROXY_HASH), verification_state: "unverified" as const },
      identity(OTHER, PROXY_HASH),
      identity(PROXY, "0x1234"),
    ]) {
      const targets = buildContractInteractionTargets(
        PROXY,
        proxyDetails("uups", { proxy }),
      );
      expect(targets.map(({ kind }) => kind)).toEqual(["contract"]);
    }
  });

  it("uses only the exact verified immutable Transparent ProxyAdmin", () => {
    const details = proxyDetails("transparent");
    const targets = buildContractInteractionTargets(PROXY, details);
    expect(targets.map(({ kind }) => kind)).toEqual([
      "contract",
      "implementation_as_proxy",
      "transparent_proxy_admin",
    ]);
    expect(targetOfKind(targets, "transparent_proxy_admin")).toMatchObject({
      transactionTarget: ADMIN,
      abiAddress: ADMIN,
      abiCodeHash: ADMIN_HASH,
      affectedProxyCount: "1",
    });

    const mismatchedAdmin = proxyDetails("transparent", {
      admin: identity(OTHER, ADMIN_HASH, "proxy_admin"),
    });
    expect(
      buildContractInteractionTargets(PROXY, mismatchedAdmin).map(({ kind }) => kind),
    ).toEqual(["contract", "implementation_as_proxy"]);

    const unverifiedManager = proxyDetails("transparent", {
      management: {
        kind: "proxy_admin",
        target: {
          ...identity(ADMIN, ADMIN_HASH, "proxy_admin"),
          verification_state: "unverified",
        },
        affected_proxy_count: "1",
      },
    });
    expect(
      buildContractInteractionTargets(PROXY, unverifiedManager).map(({ kind }) => kind),
    ).toEqual(["contract", "implementation_as_proxy"]);
  });

  it("uses the verified UpgradeableBeacon as the shared management target", () => {
    const targets = buildContractInteractionTargets(PROXY, proxyDetails("beacon"));
    expect(targets.map(({ kind }) => kind)).toEqual([
      "contract",
      "implementation_as_proxy",
      "beacon_management",
    ]);
    expect(targetOfKind(targets, "beacon_management")).toMatchObject({
      transactionTarget: BEACON,
      abiAddress: BEACON,
      abiCodeHash: BEACON_HASH,
      affectedProxyCount: "2",
    });
  });

  it("treats an exact Clone as an implementation-as-proxy target without management", () => {
    const targets = buildContractInteractionTargets(PROXY, proxyDetails("clone"));
    expect(targets.map(({ kind }) => kind)).toEqual([
      "contract",
      "implementation_as_proxy",
    ]);
    expect(targetOfKind(targets, "implementation_as_proxy")).toMatchObject({
      transactionTarget: PROXY,
      abiAddress: IMPLEMENTATION,
      proxyCodeHash: PROXY_HASH,
      proxyPattern: "clone",
      supportsWrites: true,
    });
    expect(targetOfKind(targets, "implementation_as_proxy")).not.toHaveProperty(
      "standardVersion",
    );
  });
});

describe("interaction fences", () => {
  it("freezes canonical chain, account, provider revision, target, and binding", () => {
    const target = targetOfKind(
      buildContractInteractionTargets(PROXY, proxyDetails("uups")),
      "implementation_as_proxy",
    );
    const fence = captureInteractionFence(
      target,
      "0x7a69",
      { ...wallet, account: ACCOUNT.toLowerCase() },
    );

    expect(fence).toMatchObject({
      chainID: "31337",
      account: ACCOUNT,
      providerUUID: wallet.uuid,
      providerRevision: 7,
      target: { bindingId: BINDING_ID },
    });
    expect(Object.isFrozen(fence)).toBe(true);
    expect(Object.isFrozen(fence.target)).toBe(true);
  });

  it("accepts an advanced fresh snapshot only when every fenced identity still matches", () => {
    const loaded = proxyDetails("transparent");
    const target = targetOfKind(
      buildContractInteractionTargets(PROXY, loaded),
      "transparent_proxy_admin",
    );
    const fence = captureInteractionFence(target, "31337", wallet);
    const fresh = proxyResponse({
      ...loaded,
      snapshot: {
        ...loaded.snapshot,
        block_number: "99",
        block_hash: `0x${"77".repeat(32)}`,
      },
    });

    expect(assertFreshInteractionFence(fence, wallet, fresh)).toMatchObject({
      kind: "transparent_proxy_admin",
      bindingId: BINDING_ID,
      transactionTarget: ADMIN,
    });
    expect(assertFreshInteractionFence(fence, wallet, fresh.data)).toMatchObject({
      kind: "transparent_proxy_admin",
      bindingId: BINDING_ID,
    });
  });

  it.each([
    ["CHAIN_CHANGED", { ...wallet, chainID: "1" }],
    ["ACCOUNT_CHANGED", { ...wallet, account: OTHER }],
    ["PROVIDER_CHANGED", { ...wallet, uuid: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" }],
    ["PROVIDER_REVISION_CHANGED", { ...wallet, revision: 8 }],
  ] as const)("rejects a stale wallet with %s", (code, current) => {
    const fence = uupsFence();
    expectFenceCode(
      () => assertFreshInteractionFence(fence, current, proxyResponse()),
      code,
    );
  });

  it("distinguishes fresh chain, binding, target identity, and management-scope changes", () => {
    const fence = uupsFence();
    expectFenceCode(
      () => assertFreshInteractionFence(fence, wallet, proxyResponse(undefined, "1")),
      "CHAIN_CHANGED",
    );
    expectFenceCode(
      () =>
        assertFreshInteractionFence(
          fence,
          wallet,
          proxyResponse(proxyDetails("uups", { binding_id: NEXT_BINDING_ID })),
        ),
      "BINDING_CHANGED",
    );
    expectFenceCode(
      () =>
        assertFreshInteractionFence(
          fence,
          wallet,
          proxyResponse(
            proxyDetails("uups", {
              implementation: identity(
                IMPLEMENTATION,
                `0x${"88".repeat(32)}`,
                "uups_implementation",
              ),
            }),
          ),
        ),
      "TARGET_CHANGED",
    );
    expectFenceCode(
      () =>
        assertFreshInteractionFence(
          fence,
          wallet,
          proxyResponse(
            proxyDetails("uups", {
              proxy: identity(PROXY, `0x${"99".repeat(32)}`),
            }),
          ),
        ),
      "TARGET_CHANGED",
    );

    const beacon = proxyDetails("beacon");
    const beaconFence = captureInteractionFence(
      targetOfKind(
        buildContractInteractionTargets(PROXY, beacon),
        "beacon_management",
      ),
      "31337",
      wallet,
    );
    expectFenceCode(
      () =>
        assertFreshInteractionFence(
          beaconFence,
          wallet,
          proxyResponse({
            ...beacon,
            management: { ...beacon.management!, affected_proxy_count: "3" },
          }),
        ),
      "TARGET_CHANGED",
    );
  });

  it("forces one fresh load for bound targets and rechecks wallet drift afterward", async () => {
    const fence = uupsFence();
    let current = wallet;
    const loadFreshProxy = vi.fn(async () => {
      current = { ...wallet, revision: 8 };
      return proxyResponse();
    });

    await expect(
      refreshInteractionTarget({
        fence,
        getCurrentWallet: () => current,
        loadFreshProxy,
      }),
    ).rejects.toMatchObject({ code: "PROVIDER_REVISION_CHANGED" });
    expect(loadFreshProxy).toHaveBeenCalledOnce();
    expect(loadFreshProxy).toHaveBeenCalledWith(PROXY);
  });

  it("does not fetch proxy details for a direct contract target", async () => {
    const direct = targetOfKind(buildContractInteractionTargets(PROXY), "contract");
    const fence = captureInteractionFence(direct, "31337", wallet);
    const loadFreshProxy = vi.fn(async () => proxyResponse());

    await expect(
      refreshInteractionTarget({
        fence,
        getCurrentWallet: () => wallet,
        loadFreshProxy,
      }),
    ).resolves.toBe(fence.target);
    expect(loadFreshProxy).not.toHaveBeenCalled();
  });
});

describe("fenced transaction outcomes", () => {
  it("submits exactly once after a matching fresh binding", async () => {
    const send = vi.fn(async () => TRANSACTION_HASH);
    const loadFreshProxy = vi.fn(async () => proxyResponse());
    const outcome = await submitFencedTransaction({
      fence: uupsFence(),
      getCurrentWallet: () => wallet,
      loadFreshProxy,
      send,
    });

    expect(outcome).toMatchObject({
      status: "submitted",
      transactionHash: TRANSACTION_HASH,
      target: { kind: "implementation_as_proxy" },
    });
    expect(loadFreshProxy).toHaveBeenCalledOnce();
    expect(send).toHaveBeenCalledOnce();
    expect(send).toHaveBeenCalledWith(
      expect.objectContaining({ transactionTarget: PROXY }),
      "31337",
    );
  });

  it("blocks a changed binding before send and never retries", async () => {
    const send = vi.fn(async () => TRANSACTION_HASH);
    const loadFreshProxy = vi.fn(async () =>
      proxyResponse(proxyDetails("uups", { binding_id: NEXT_BINDING_ID })),
    );
    const outcome = await submitFencedTransaction({
      fence: uupsFence(),
      getCurrentWallet: () => wallet,
      loadFreshProxy,
      send,
    });

    expect(outcome).toMatchObject({
      status: "not_submitted",
      error: { code: "BINDING_CHANGED" },
    });
    expect(loadFreshProxy).toHaveBeenCalledOnce();
    expect(send).not.toHaveBeenCalled();
  });

  it.each([
    ["invalid hash", async () => "0x1234"],
    [
      "provider unknown outcome",
      async () => {
        throw new WalletBoundaryError("TRANSACTION_OUTCOME_UNKNOWN");
      },
    ],
  ])("keeps %s distinct as an unknown, non-retryable outcome", async (_label, send) => {
    const submit = vi.fn(send);
    const outcome = await submitFencedTransaction({
      fence: uupsFence(),
      getCurrentWallet: () => wallet,
      loadFreshProxy: async () => proxyResponse(),
      send: submit,
    });

    expect(outcome.status).toBe("unknown");
    expect(submit).toHaveBeenCalledOnce();
  });

  it("marks wallet drift after send as unknown", async () => {
    let current = wallet;
    const send = vi.fn(async () => {
      current = { ...wallet, revision: 8 };
      return TRANSACTION_HASH;
    });
    const outcome = await submitFencedTransaction({
      fence: uupsFence(),
      getCurrentWallet: () => current,
      loadFreshProxy: async () => proxyResponse(),
      send,
    });

    expect(outcome).toMatchObject({
      status: "unknown",
      error: { code: "PROVIDER_REVISION_CHANGED" },
    });
    expect(send).toHaveBeenCalledOnce();
  });

  it("keeps an explicit wallet rejection in the not-submitted state", async () => {
    const outcome = await submitFencedTransaction({
      fence: uupsFence(),
      getCurrentWallet: () => wallet,
      loadFreshProxy: async () => proxyResponse(),
      send: async () => {
        throw new WalletBoundaryError("USER_REJECTED");
      },
    });

    expect(outcome).toMatchObject({
      status: "not_submitted",
      error: { code: "USER_REJECTED" },
    });
  });
});

function uupsFence() {
  return captureInteractionFence(
    targetOfKind(
      buildContractInteractionTargets(PROXY, proxyDetails("uups")),
      "implementation_as_proxy",
    ),
    "31337",
    wallet,
  );
}

function targetOfKind<Kind extends ContractInteractionTarget["kind"]>(
  targets: readonly ContractInteractionTarget[],
  kind: Kind,
): Extract<ContractInteractionTarget, { kind: Kind }> {
  const target = targets.find(
    (candidate): candidate is Extract<ContractInteractionTarget, { kind: Kind }> =>
      candidate.kind === kind,
  );
  if (!target) throw new Error(`missing ${kind} target`);
  return target;
}

function proxyDetails(
  pattern: NonNullable<ProxyDetails["pattern"]> = "uups",
  overrides: Partial<ProxyDetails> = {},
): ProxyDetails {
  const details: ProxyDetails = {
    address: PROXY,
    status: "verified",
    snapshot: {
      chain_id: "31337",
      block_number: "20",
      block_hash: BLOCK_HASH,
    },
    mechanism: pattern === "clone" ? "eip1167" : pattern === "beacon" ? "beacon" : "eip1967",
    pattern,
    ...(pattern === "clone" ? {} : { standard_version: "5.6.1" as const }),
    evidence_state: "exact",
    confidence: "verified",
    proxy: pattern === "clone"
      ? { ...identity(PROXY, PROXY_HASH), verification_state: "unverified" }
      : identity(PROXY, PROXY_HASH),
    implementation: identity(
      IMPLEMENTATION,
      IMPLEMENTATION_HASH,
      pattern === "uups" ? "uups_implementation" : undefined,
    ),
    binding_id: BINDING_ID,
    evidence: [],
    ...(pattern === "transparent"
      ? {
          admin: identity(ADMIN, ADMIN_HASH, "proxy_admin"),
          management: {
            kind: "proxy_admin" as const,
            target: identity(ADMIN, ADMIN_HASH, "proxy_admin"),
            affected_proxy_count: "1",
          },
        }
      : {}),
    ...(pattern === "beacon"
      ? {
          beacon: identity(BEACON, BEACON_HASH, "upgradeable_beacon"),
          management: {
            kind: "upgradeable_beacon" as const,
            target: identity(BEACON, BEACON_HASH, "upgradeable_beacon"),
            affected_proxy_count: "2",
          },
        }
      : {}),
  };
  return { ...details, ...overrides };
}

function proxyResponse(
  data: ProxyDetails = proxyDetails("uups"),
  chainID = "31337",
): ProxyDetailsResponse {
  return {
    data,
    meta: {
      chain_id: chainID,
      request_id: "request-1",
    },
  };
}

function identity(
  address: string,
  codeHash: string,
  artifactKind?: NonNullable<
    ProxyDetails["implementation"]
  >["artifact_kind"],
): NonNullable<ProxyDetails["implementation"]> {
  return {
    address,
    code_hash: codeHash,
    verification_state: "verified",
    ...(artifactKind === undefined
      ? {}
      : { artifact_kind: artifactKind, standard_version: "5.6.1" }),
  };
}

function expectFenceCode(action: () => unknown, code: InteractionFenceErrorCode): void {
  try {
    action();
  } catch (error) {
    expect(error).toBeInstanceOf(InteractionFenceError);
    expect(error).toMatchObject({ code });
    return;
  }
  throw new Error(`expected ${code}`);
}
