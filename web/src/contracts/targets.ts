import {
  getAddress,
  toFunctionSelector,
  type Address,
  type Hex,
} from "viem";

import type { components } from "@/api/schema.gen";
import {
  isTransactionHash,
  normalizeChainID,
  WalletBoundaryError,
} from "@/wallet/eip6963";

export type ProxyDetails = components["schemas"]["ProxyDetails"];
export type ProxyDetailsResponse = components["schemas"]["ProxyDetailsResponse"];
export type ProxyPattern = components["schemas"]["ProxyPattern"];
export type FreshProxyDetails = ProxyDetails | ProxyDetailsResponse;
export type DelegationBinding = components["schemas"]["DelegationBinding"];

export const PROXIABLE_UUID_SIGNATURE = "proxiableUUID()" as const;

export interface WalletInteractionSession {
  readonly uuid: string;
  readonly account: string;
  readonly chainID: string;
  readonly revision: number;
}

interface BaseInteractionTarget {
  readonly transactionTarget: Address;
  readonly abiAddress: Address;
  readonly abiCodeHash?: string;
  readonly supportsWrites: boolean;
  readonly requiresFreshBinding: boolean;
}

export interface DirectContractInteractionTarget extends BaseInteractionTarget {
  readonly kind: "contract";
  readonly supportsWrites: true;
  readonly requiresFreshBinding: false;
}

interface BoundInteractionTarget extends BaseInteractionTarget {
  readonly proxyAddress: Address;
  readonly proxyCodeHash: string;
  readonly proxyChainID: string;
	readonly bindingId?: string;
	readonly proxyMechanism: components["schemas"]["ProxyMechanism"];
	readonly proxyPattern?: ProxyPattern;
	readonly beaconAddress?: Address;
	readonly beaconCodeHash?: string;
  readonly standardVersion?: "5.6.1";
  readonly requiresFreshBinding: true;
}

export interface DelegatedEOAInteractionTarget extends BaseInteractionTarget {
  readonly kind: "delegated_eoa";
  readonly authorityAddress: Address;
  readonly delegationChainID: string;
  readonly delegationBlockNumber: string;
  readonly delegationBlockHash: string;
  readonly supportsWrites: true;
  readonly requiresFreshBinding: true;
}

export interface DiamondFacetInteractionTarget extends BaseInteractionTarget {
  readonly kind: "diamond_facet";
  readonly proxyAddress: Address;
  readonly proxyChainID: string;
  readonly facetSelectors: readonly Hex[];
  readonly supportsWrites: true;
  readonly requiresFreshBinding: true;
}

export interface ImplementationAsProxyTarget extends BoundInteractionTarget {
  readonly kind: "implementation_as_proxy";
  readonly supportsWrites: true;
}

export interface UUPSImplementationDirectTarget extends BoundInteractionTarget {
  readonly kind: "uups_implementation_direct";
  readonly supportsWrites: false;
	readonly proxyPattern: "uups";
	readonly bindingId: string;
}

export interface TransparentProxyAdminTarget extends BoundInteractionTarget {
  readonly kind: "transparent_proxy_admin";
  readonly supportsWrites: true;
	readonly proxyPattern: "transparent";
	readonly bindingId: string;
  readonly affectedProxyCount?: string;
}

export interface BeaconManagementTarget extends BoundInteractionTarget {
  readonly kind: "beacon_management";
  readonly supportsWrites: true;
	readonly proxyPattern: "beacon";
	readonly bindingId: string;
  readonly affectedProxyCount?: string;
}

export type ContractInteractionTarget =
  | DirectContractInteractionTarget
  | ImplementationAsProxyTarget
  | UUPSImplementationDirectTarget
  | TransparentProxyAdminTarget
  | BeaconManagementTarget
  | DiamondFacetInteractionTarget
  | DelegatedEOAInteractionTarget;

export type BoundContractInteractionTarget = Exclude<
  ContractInteractionTarget,
  DirectContractInteractionTarget
>;

export interface InteractionFence {
  readonly chainID: string;
  readonly account: Address;
  readonly providerUUID: string;
  readonly providerRevision: number;
  readonly target: ContractInteractionTarget;
}

export type InteractionFenceErrorCode =
  | "INVALID_TARGET"
  | "INVALID_CHAIN"
  | "WALLET_NOT_CONNECTED"
  | "CHAIN_CHANGED"
  | "ACCOUNT_CHANGED"
  | "PROVIDER_CHANGED"
  | "PROVIDER_REVISION_CHANGED"
  | "FRESH_PROXY_REQUIRED"
  | "FRESH_BINDING_REQUIRED"
  | "BINDING_CHANGED"
  | "TARGET_CHANGED"
  | "FUNCTION_NOT_ALLOWED";

const FENCE_ERROR_MESSAGES: Record<InteractionFenceErrorCode, string> = {
  INVALID_TARGET: "The contract interaction target is invalid",
  INVALID_CHAIN: "The explorer chain identity is invalid",
  WALLET_NOT_CONNECTED: "A connected wallet account is required",
  CHAIN_CHANGED: "The interaction chain changed",
  ACCOUNT_CHANGED: "The interaction account changed",
  PROVIDER_CHANGED: "The injected wallet provider changed",
  PROVIDER_REVISION_CHANGED: "The injected wallet session changed",
  FRESH_PROXY_REQUIRED: "A fresh proxy response is required",
  FRESH_BINDING_REQUIRED: "A fresh delegation binding is required",
  BINDING_CHANGED: "The verified proxy binding changed; refresh before continuing",
  TARGET_CHANGED: "The verified interaction target changed; refresh before continuing",
  FUNCTION_NOT_ALLOWED: "The function cannot be called through this interaction target",
};

export class InteractionFenceError extends Error {
  readonly code: InteractionFenceErrorCode;

  constructor(code: InteractionFenceErrorCode, options?: ErrorOptions) {
    super(FENCE_ERROR_MESSAGES[code], options);
    this.name = "InteractionFenceError";
    this.code = code;
  }
}

export interface RefreshInteractionOptions {
  readonly fence: InteractionFence;
  readonly getCurrentWallet: () => WalletInteractionSession | undefined;
  readonly loadFreshProxy?: (
    proxyAddress: Address,
  ) => Promise<FreshProxyDetails>;
  readonly loadFreshDelegation?: (
    authorityAddress: Address,
  ) => Promise<DelegationBinding>;
  /** Reads may observe a newer canonical tip; writes require the exact fenced tip. */
  readonly requireExactDelegationSnapshot?: boolean;
}

export interface SubmitFencedTransactionOptions extends RefreshInteractionOptions {
  readonly send: (
    target: ContractInteractionTarget,
    expectedChainID: string,
  ) => Promise<unknown>;
}

export type InteractionSendOutcome =
  | {
      readonly status: "submitted";
      readonly transactionHash: Hex;
      readonly target: ContractInteractionTarget;
    }
  | {
      readonly status: "unknown";
      readonly error: unknown;
    }
  | {
      readonly status: "not_submitted";
      readonly error: unknown;
    };

const HASH_PATTERN = /^0x[0-9a-f]{64}$/iu;
const BINDING_ID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/iu;

/**
 * Builds only targets justified by the current public proxy binding. The
 * contract target is always direct; callers still decide whether its own ABI
 * is available. High-confidence standard proxy details may create an ordinary
 * implementation interaction target; management targets remain exact-only.
 */
export function buildContractInteractionTargets(
  contractAddress: string,
  proxy?: ProxyDetails,
): readonly ContractInteractionTarget[] {
  const address = checkedAddress(contractAddress);
  const targets: ContractInteractionTarget[] = [
    freezeTarget({
      kind: "contract",
      transactionTarget: address,
      abiAddress: address,
      supportsWrites: true,
      requiresFreshBinding: false,
    }),
  ];

	const diamond = confirmedDiamond(proxy, address);
	if (diamond) {
		for (const facet of diamond.facets) {
			targets.push(freezeTarget({
				kind: "diamond_facet",
				transactionTarget: address,
				abiAddress: facet.address,
				abiCodeHash: facet.codeHash,
				proxyAddress: address,
				proxyChainID: diamond.chainID,
				facetSelectors: Object.freeze([...facet.selectors]),
				supportsWrites: true,
				requiresFreshBinding: true,
			}));
		}
	}

	const binding = exactBinding(proxy, address);
	const interaction = standardImplementationInteraction(proxy, address);
	if (interaction) {
		targets.push(freezeTarget({
			kind: "implementation_as_proxy",
			transactionTarget: address,
			abiAddress: interaction.implementation.address,
			abiCodeHash: interaction.implementation.codeHash,
			proxyAddress: address,
			proxyCodeHash: interaction.proxy.codeHash,
			proxyChainID: interaction.chainID,
			proxyMechanism: interaction.mechanism,
			...(interaction.pattern === undefined ? {} : { proxyPattern: interaction.pattern }),
			...(interaction.beacon === undefined ? {} : {
				beaconAddress: interaction.beacon.address,
				beaconCodeHash: interaction.beacon.codeHash,
			}),
			...(interaction.standardVersion === undefined
				? {}
				: { standardVersion: interaction.standardVersion }),
			...(binding?.pattern === "uups" ? { bindingId: binding.bindingId } : {}),
			supportsWrites: true,
			requiresFreshBinding: true,
		}));
	}

	if (!binding) return Object.freeze(targets);

  if (
    binding.pattern === "uups" &&
    binding.implementation.artifactKind === "uups_implementation" &&
    proxy?.implementation?.standard_version === "5.6.1"
  ) {
    targets.push(
      freezeTarget({
        kind: "uups_implementation_direct",
        transactionTarget: binding.implementation.address,
        abiAddress: binding.implementation.address,
        abiCodeHash: binding.implementation.codeHash,
        proxyAddress: address,
        proxyCodeHash: binding.proxy.codeHash,
        proxyChainID: binding.chainID,
        bindingId: binding.bindingId,
			proxyPattern: "uups",
			proxyMechanism: binding.mechanism,
        standardVersion: "5.6.1",
        supportsWrites: false,
        requiresFreshBinding: true,
      }),
    );
  }

  if (binding.pattern === "transparent") {
    const management = exactManagement(proxy, "proxy_admin", "proxy_admin");
    const admin = exactVerifiedIdentity(proxy?.admin);
    if (management && admin && identitiesMatch(management, admin)) {
      targets.push(
        freezeTarget({
          kind: "transparent_proxy_admin",
          transactionTarget: management.address,
          abiAddress: management.address,
          abiCodeHash: management.codeHash,
          proxyAddress: address,
          proxyCodeHash: binding.proxy.codeHash,
          proxyChainID: binding.chainID,
          bindingId: binding.bindingId,
			proxyPattern: "transparent",
			proxyMechanism: binding.mechanism,
          standardVersion: "5.6.1",
          supportsWrites: true,
          requiresFreshBinding: true,
          ...(proxy?.management?.affected_proxy_count === undefined
            ? {}
            : { affectedProxyCount: proxy.management.affected_proxy_count }),
        }),
      );
    }
  }

  if (binding.pattern === "beacon") {
    const management = exactManagement(
      proxy,
      "upgradeable_beacon",
      "upgradeable_beacon",
    );
    const beacon = exactVerifiedIdentity(proxy?.beacon);
    if (management && beacon && identitiesMatch(management, beacon)) {
      targets.push(
        freezeTarget({
          kind: "beacon_management",
          transactionTarget: management.address,
          abiAddress: management.address,
          abiCodeHash: management.codeHash,
          proxyAddress: address,
          proxyCodeHash: binding.proxy.codeHash,
          proxyChainID: binding.chainID,
          bindingId: binding.bindingId,
			proxyPattern: "beacon",
			proxyMechanism: binding.mechanism,
			...(binding.beacon === undefined ? {} : {
				beaconAddress: binding.beacon.address,
				beaconCodeHash: binding.beacon.codeHash,
			}),
          standardVersion: "5.6.1",
          supportsWrites: true,
          requiresFreshBinding: true,
          ...(proxy?.management?.affected_proxy_count === undefined
            ? {}
            : { affectedProxyCount: proxy.management.affected_proxy_count }),
        }),
      );
    }
  }

  return Object.freeze(targets);
}

export function buildDelegatedEOAInteractionTarget(
  authorityAddress: string,
  binding: DelegationBinding,
): DelegatedEOAInteractionTarget {
  const authority = checkedAddress(authorityAddress);
  if (
    binding.status !== "delegated" ||
    !binding.delegate ||
    !binding.delegate_code_hash ||
    !addressesMatch(binding.authority, authority) ||
    !normalizeChainID(binding.chain_id) ||
    !/^\d+$/u.test(binding.block_number) ||
    !HASH_PATTERN.test(binding.block_hash) ||
    !HASH_PATTERN.test(binding.delegate_code_hash)
  ) {
    throw new InteractionFenceError("INVALID_TARGET");
  }
  return freezeTarget({
    kind: "delegated_eoa",
    transactionTarget: authority,
    abiAddress: checkedAddress(binding.delegate),
    abiCodeHash: binding.delegate_code_hash.toLowerCase(),
    authorityAddress: authority,
    delegationChainID: binding.chain_id,
    delegationBlockNumber: binding.block_number,
    delegationBlockHash: binding.block_hash.toLowerCase(),
    supportsWrites: true,
    requiresFreshBinding: true,
  });
}

/** Captures the exact wallet and proxy target identity shown to the user. */
export function captureInteractionFence(
  target: ContractInteractionTarget,
  expectedChainID: string,
  wallet: WalletInteractionSession | undefined,
): InteractionFence {
  const chainID = normalizeChainID(expectedChainID);
  if (!chainID) throw new InteractionFenceError("INVALID_CHAIN");
  const checkedWallet = checkWallet(wallet);
  if (normalizeChainID(checkedWallet.chainID) !== chainID) {
    throw new InteractionFenceError("CHAIN_CHANGED");
  }
  const targetChainID = target.kind === "delegated_eoa"
    ? target.delegationChainID
    : target.requiresFreshBinding ? target.proxyChainID : chainID;
  if (target.requiresFreshBinding && targetChainID !== chainID) {
    throw new InteractionFenceError("CHAIN_CHANGED");
  }
  validateTarget(target);

  return Object.freeze({
    chainID,
    account: checkedAddress(checkedWallet.account, "WALLET_NOT_CONNECTED"),
    providerUUID: checkedWallet.uuid,
    providerRevision: checkedWallet.revision,
    target: freezeTarget({ ...target }),
  });
}

/**
 * Compares a loaded interaction fence to a fresh same-origin proxy response.
 * The response snapshot may advance, but its chain, binding, code identities,
 * transaction target, and displayed management impact must remain identical.
 */
export function assertFreshInteractionFence(
  fence: InteractionFence,
  currentWallet: WalletInteractionSession | undefined,
  fresh?: FreshProxyDetails,
): ContractInteractionTarget {
  assertWalletFence(fence, currentWallet);
  if (!fence.target.requiresFreshBinding) return fence.target;
  if (fence.target.kind === "delegated_eoa") {
    throw new InteractionFenceError("FRESH_BINDING_REQUIRED");
  }
  if (!fresh) throw new InteractionFenceError("FRESH_PROXY_REQUIRED");

  const freshResponse = freshProxyResponse(fresh);
  if (normalizeChainID(freshResponse.details.snapshot.chain_id) !== fence.chainID) {
    throw new InteractionFenceError("CHAIN_CHANGED");
  }
  if (
    freshResponse.metaChainID !== undefined &&
    normalizeChainID(freshResponse.metaChainID) !== fence.chainID
  ) {
    throw new InteractionFenceError("CHAIN_CHANGED");
  }
  if (!addressesMatch(freshResponse.details.address, fence.target.proxyAddress)) {
    throw new InteractionFenceError("TARGET_CHANGED");
  }
	if (
		fence.target.kind !== "diamond_facet" &&
		fence.target.bindingId !== undefined &&
		freshResponse.details.binding_id !== fence.target.bindingId
	) {
    throw new InteractionFenceError("BINDING_CHANGED");
  }

	const freshTargets = buildContractInteractionTargets(
		fence.target.proxyAddress,
		freshResponse.details,
	);
	const freshTarget = fence.target.kind === "diamond_facet"
		? freshTargets.find((candidate) =>
			candidate.kind === "diamond_facet" &&
			candidate.abiAddress === fence.target.abiAddress)
		: freshTargets.find((candidate) => candidate.kind === fence.target.kind);
  if (!freshTarget || !targetsMatch(fence.target, freshTarget)) {
    throw new InteractionFenceError("TARGET_CHANGED");
  }
  return freshTarget;
}

export function assertFreshDelegationFence(
  fence: InteractionFence,
  currentWallet: WalletInteractionSession | undefined,
  fresh?: DelegationBinding,
  requireExactSnapshot = true,
): DelegatedEOAInteractionTarget {
  assertWalletFence(fence, currentWallet);
  if (fence.target.kind !== "delegated_eoa" || !fresh) {
    throw new InteractionFenceError("FRESH_BINDING_REQUIRED");
  }
  let freshTarget: DelegatedEOAInteractionTarget;
  try {
    freshTarget = buildDelegatedEOAInteractionTarget(fence.target.authorityAddress, fresh);
  } catch (error) {
    throw new InteractionFenceError("BINDING_CHANGED", { cause: error });
  }
  if (
    freshTarget.delegationChainID !== fence.chainID ||
    !delegatedTargetsMatch(fence.target, freshTarget, requireExactSnapshot)
  ) {
    throw new InteractionFenceError(
      freshTarget.delegationChainID !== fence.chainID ? "CHAIN_CHANGED" : "BINDING_CHANGED",
    );
  }
  return freshTarget;
}

/**
 * Forces one fresh proxy GET for every bound implementation or management
 * operation and rechecks the wallet after that network boundary.
 */
export async function refreshInteractionTarget({
  fence,
  getCurrentWallet,
  loadFreshProxy,
  loadFreshDelegation,
  requireExactDelegationSnapshot,
}: RefreshInteractionOptions): Promise<ContractInteractionTarget> {
  assertWalletFence(fence, getCurrentWallet());
  if (!fence.target.requiresFreshBinding) return fence.target;
  if (fence.target.kind === "delegated_eoa") {
    if (!loadFreshDelegation) throw new InteractionFenceError("FRESH_BINDING_REQUIRED");
    const fresh = await loadFreshDelegation(fence.target.authorityAddress);
    return assertFreshDelegationFence(
      fence,
      getCurrentWallet(),
      fresh,
      requireExactDelegationSnapshot ?? true,
    );
  }
  if (!loadFreshProxy) throw new InteractionFenceError("FRESH_PROXY_REQUIRED");

  const fresh = await loadFreshProxy(fence.target.proxyAddress);
  return assertFreshInteractionFence(fence, getCurrentWallet(), fresh);
}

/**
 * Submits at most once. Once send starts, an invalid hash, provider-declared
 * unknown outcome, or wallet drift is returned as `unknown`, never as a safe
 * retry candidate.
 */
export async function submitFencedTransaction(
  options: SubmitFencedTransactionOptions,
): Promise<InteractionSendOutcome> {
  let target: ContractInteractionTarget;
  try {
    target = await refreshInteractionTarget(options);
  } catch (error) {
    return Object.freeze({ status: "not_submitted" as const, error });
  }

  try {
    const transactionHash = await options.send(target, options.fence.chainID);
    if (!isTransactionHash(transactionHash)) {
      return unknownOutcome(new WalletBoundaryError("TRANSACTION_OUTCOME_UNKNOWN"));
    }
    try {
      assertWalletFence(options.fence, options.getCurrentWallet());
    } catch (error) {
      return unknownOutcome(error);
    }
    return Object.freeze({ status: "submitted", transactionHash, target });
  } catch (error) {
    if (isUnknownTransactionOutcome(error)) return unknownOutcome(error);
    return Object.freeze({ status: "not_submitted" as const, error });
  }
}

export function isUnknownTransactionOutcome(
  error: unknown,
): error is WalletBoundaryError {
  return (
    error instanceof WalletBoundaryError &&
    error.code === "TRANSACTION_OUTCOME_UNKNOWN"
  );
}

/**
 * UUPS proxiableUUID deliberately reverts through delegatecall. The as-proxy
 * surface therefore excludes it; the dedicated direct implementation target
 * exposes no other function.
 */
export function isInteractionFunctionAllowed(
  target: ContractInteractionTarget,
  canonicalSignature: string,
  write = false,
): boolean {
  if (write && !target.supportsWrites) return false;
	if (target.kind === "diamond_facet") {
		try {
			return target.facetSelectors.includes(
				toFunctionSelector(canonicalSignature) as Hex,
			);
		} catch {
			return false;
		}
	}
  if (target.kind === "uups_implementation_direct") {
    return canonicalSignature === PROXIABLE_UUID_SIGNATURE;
  }
  if (target.kind === "implementation_as_proxy") {
    if (canonicalSignature === PROXIABLE_UUID_SIGNATURE) return false;
		if (/^(?:upgrade|changeAdmin|transferOwnership\(|renounceOwnership\()/u.test(canonicalSignature)) {
			return target.bindingId !== undefined;
		}
		return true;
  }
  return true;
}

export function assertInteractionFunctionAllowed(
  target: ContractInteractionTarget,
  canonicalSignature: string,
  write = false,
): void {
  if (!isInteractionFunctionAllowed(target, canonicalSignature, write)) {
    throw new InteractionFenceError("FUNCTION_NOT_ALLOWED");
  }
}

function assertWalletFence(
  fence: InteractionFence,
  currentWallet: WalletInteractionSession | undefined,
): void {
  const wallet = checkWallet(currentWallet);
  if (normalizeChainID(wallet.chainID) !== fence.chainID) {
    throw new InteractionFenceError("CHAIN_CHANGED");
  }
  let account: Address;
  try {
    account = getAddress(wallet.account);
  } catch {
    throw new InteractionFenceError("ACCOUNT_CHANGED");
  }
  if (account !== fence.account) {
    throw new InteractionFenceError("ACCOUNT_CHANGED");
  }
  if (wallet.uuid !== fence.providerUUID) {
    throw new InteractionFenceError("PROVIDER_CHANGED");
  }
  if (wallet.revision !== fence.providerRevision) {
    throw new InteractionFenceError("PROVIDER_REVISION_CHANGED");
  }
}

function checkWallet(
  wallet: WalletInteractionSession | undefined,
): WalletInteractionSession {
  if (
    !wallet ||
    typeof wallet.uuid !== "string" ||
    wallet.uuid.length < 1 ||
    wallet.uuid.length > 256 ||
    !Number.isSafeInteger(wallet.revision) ||
    wallet.revision < 1
  ) {
    throw new InteractionFenceError("WALLET_NOT_CONNECTED");
  }
  return wallet;
}

function exactBinding(proxy: ProxyDetails | undefined, address: Address) {
  const chainID = normalizeChainID(proxy?.snapshot.chain_id);
  if (
    !proxy ||
    !addressesMatch(proxy.address, address) ||
    proxy.status !== "verified" ||
    proxy.evidence_state !== "exact" ||
    !proxy.pattern ||
    proxy.pattern === "unknown" ||
    (proxy.pattern === "clone"
      ? proxy.standard_version !== undefined
      : proxy.standard_version !== "5.6.1") ||
    !chainID ||
    !proxy.binding_id ||
    !BINDING_ID_PATTERN.test(proxy.binding_id)
  ) {
    return undefined;
  }
  const implementation = exactVerifiedIdentity(proxy.implementation);
  const proxyIdentity = exactCodeIdentity(
    proxy.proxy,
    proxy.pattern === "clone" ? "unverified" : "verified",
  );
  if (
    !implementation ||
    !proxyIdentity ||
    !addressesMatch(proxyIdentity.address, address)
  ) {
    return undefined;
  }
	return {
    bindingId: proxy.binding_id,
    chainID,
		pattern: proxy.pattern,
		mechanism: proxy.mechanism!,
		proxy: proxyIdentity,
		standardVersion: proxy.standard_version,
		implementation,
		beacon: currentCodeIdentity(proxy.beacon),
	} as const;
}

function confirmedDiamond(proxy: ProxyDetails | undefined, address: Address) {
	const chainID = normalizeChainID(proxy?.snapshot.chain_id);
	const outcome = proxy?.proxy_detection_v2?.outcomes.find((candidate) =>
		candidate.family === "erc2535" && candidate.status === "confirmed" &&
		candidate.diamond?.completeness === "complete" &&
		!candidate.diamond.truncated,
	);
	if (!proxy || !chainID || !outcome?.diamond ||
		!addressesMatch(proxy.address, address) || !addressesMatch(outcome.proxy, address)) {
		return undefined;
	}
	const facets: Array<{
		address: Address;
		codeHash: string;
		selectors: readonly Hex[];
	}> = [];
	for (const facet of outcome.diamond.facets) {
		if (facet.role !== "facet" || !facet.code_exists ||
			!facet.code_hash || !HASH_PATTERN.test(facet.code_hash)) {
			continue;
		}
		const facetAddress = checkedAddress(facet.address);
		const selectors: Hex[] = [];
		for (const selector of facet.selectors) {
			if (!/^0x[0-9a-f]{8}$/iu.test(selector) ||
				!addressesMatch(outcome.diamond.selector_to_facet[selector.toLowerCase()] ?? "", facetAddress)) {
				throw new InteractionFenceError("INVALID_TARGET");
			}
			selectors.push(selector.toLowerCase() as Hex);
		}
		if (selectors.length === 0) continue;
		facets.push({
			address: facetAddress,
			codeHash: facet.code_hash.toLowerCase(),
			selectors: Object.freeze(selectors),
		});
	}
	return { chainID, facets: Object.freeze(facets) } as const;
}

function standardImplementationInteraction(
	proxy: ProxyDetails | undefined,
	address: Address,
) {
	const interaction = proxy?.implementation_interaction;
	const chainID = normalizeChainID(proxy?.snapshot.chain_id);
	if (!proxy || !interaction || !chainID || !addressesMatch(proxy.address, address)) {
		return undefined;
	}
	const proxyIdentity = currentCodeIdentity(interaction.proxy);
	const implementation = currentCodeIdentity(interaction.implementation);
	const beacon = currentCodeIdentity(interaction.beacon);
	if (
		!proxyIdentity || !implementation ||
		!addressesMatch(proxyIdentity.address, address) ||
		proxyIdentity.address === implementation.address ||
		(interaction.mechanism === "beacon" && !beacon)
	) {
		return undefined;
	}
	return {
		chainID,
		mechanism: interaction.mechanism,
		pattern: interaction.pattern,
		proxy: proxyIdentity,
		implementation,
		beacon,
		standardVersion: proxy.standard_version,
	} as const;
}

function exactManagement(
  proxy: ProxyDetails | undefined,
  managementKind: "proxy_admin" | "upgradeable_beacon",
  artifactKind: "proxy_admin" | "upgradeable_beacon",
) {
  if (proxy?.management?.kind !== managementKind) return undefined;
  const target = exactVerifiedIdentity(proxy.management.target);
  if (
    !target ||
    target.artifactKind !== artifactKind ||
    proxy.management.target.standard_version !== "5.6.1"
  ) {
    return undefined;
  }
  return target;
}

function exactVerifiedIdentity(
  identity: components["schemas"]["ProxyContractIdentity"] | undefined,
) {
  return exactCodeIdentity(identity, "verified");
}

function currentCodeIdentity(
	identity: components["schemas"]["ProxyContractIdentity"] | undefined,
) {
	if (!identity) return undefined;
	return exactCodeIdentity(identity, identity.verification_state);
}

function exactCodeIdentity(
  identity: components["schemas"]["ProxyContractIdentity"] | undefined,
  verificationState: "unverified" | "verified",
) {
  if (
    !identity ||
    identity.verification_state !== verificationState ||
    !HASH_PATTERN.test(identity.code_hash)
  ) {
    return undefined;
  }
  try {
    return {
      address: getAddress(identity.address),
      codeHash: identity.code_hash.toLowerCase(),
      artifactKind: identity.artifact_kind,
    } as const;
  } catch {
    return undefined;
  }
}

function identitiesMatch(
  left: { readonly address: Address; readonly codeHash: string },
  right: { readonly address: Address; readonly codeHash: string },
): boolean {
  return left.address === right.address && left.codeHash === right.codeHash;
}

function validateTarget(target: ContractInteractionTarget): void {
  try {
    getAddress(target.transactionTarget);
    getAddress(target.abiAddress);
    if (target.abiCodeHash !== undefined && !HASH_PATTERN.test(target.abiCodeHash)) {
      throw new Error("invalid code hash");
    }
    if (target.requiresFreshBinding) {
      if (target.kind === "delegated_eoa") {
        getAddress(target.authorityAddress);
        if (target.authorityAddress !== target.transactionTarget ||
            !normalizeChainID(target.delegationChainID) ||
            !/^\d+$/u.test(target.delegationBlockNumber) ||
            !HASH_PATTERN.test(target.delegationBlockHash)) {
          throw new Error("invalid delegation identity");
        }
        return;
      }
		if (target.kind === "diamond_facet") {
			getAddress(target.proxyAddress);
			if (target.proxyAddress !== target.transactionTarget ||
				!normalizeChainID(target.proxyChainID) ||
				!target.abiCodeHash || !HASH_PATTERN.test(target.abiCodeHash) ||
				target.facetSelectors.length === 0 ||
				new Set(target.facetSelectors).size !== target.facetSelectors.length ||
				target.facetSelectors.some((selector) => !/^0x[0-9a-f]{8}$/u.test(selector))) {
				throw new Error("invalid Diamond facet identity");
			}
			return;
		}
      getAddress(target.proxyAddress);
      if (!HASH_PATTERN.test(target.proxyCodeHash)) {
        throw new Error("invalid proxy code hash");
      }
		if (!normalizeChainID(target.proxyChainID)) {
			throw new Error("invalid proxy chain ID");
		}
		if (target.bindingId !== undefined && !BINDING_ID_PATTERN.test(target.bindingId)) {
			throw new Error("invalid binding ID");
		}
		if (target.beaconAddress !== undefined) getAddress(target.beaconAddress);
		if (target.beaconCodeHash !== undefined && !HASH_PATTERN.test(target.beaconCodeHash)) {
			throw new Error("invalid beacon code hash");
		}
		if ((target.beaconAddress === undefined) !== (target.beaconCodeHash === undefined)) {
			throw new Error("incomplete beacon identity");
		}
    }
  } catch (error) {
    throw new InteractionFenceError("INVALID_TARGET", { cause: error });
  }
}

function checkedAddress(
  value: string,
  code: InteractionFenceErrorCode = "INVALID_TARGET",
): Address {
  try {
    return getAddress(value);
  } catch (error) {
    throw new InteractionFenceError(code, { cause: error });
  }
}

function addressesMatch(left: string, right: string): boolean {
  try {
    return getAddress(left) === getAddress(right);
  } catch {
    return false;
  }
}

function targetsMatch(
  loaded: ContractInteractionTarget,
  fresh: ContractInteractionTarget,
): boolean {
  return (
    loaded.kind === fresh.kind &&
    loaded.transactionTarget === fresh.transactionTarget &&
    loaded.abiAddress === fresh.abiAddress &&
    loaded.abiCodeHash === fresh.abiCodeHash &&
    loaded.supportsWrites === fresh.supportsWrites &&
    loaded.requiresFreshBinding === fresh.requiresFreshBinding &&
    boundTargetFieldsMatch(loaded, fresh)
  );
}

function boundTargetFieldsMatch(
  loaded: ContractInteractionTarget,
  fresh: ContractInteractionTarget,
): boolean {
  if (!loaded.requiresFreshBinding || !fresh.requiresFreshBinding) {
    return !loaded.requiresFreshBinding && !fresh.requiresFreshBinding;
  }
  if (loaded.kind === "delegated_eoa" || fresh.kind === "delegated_eoa") {
    return loaded.kind === "delegated_eoa" && fresh.kind === "delegated_eoa" &&
      loaded.authorityAddress === fresh.authorityAddress &&
      loaded.delegationChainID === fresh.delegationChainID &&
      loaded.delegationBlockNumber === fresh.delegationBlockNumber &&
      loaded.delegationBlockHash === fresh.delegationBlockHash;
  }
	if (loaded.kind === "diamond_facet" || fresh.kind === "diamond_facet") {
		return loaded.kind === "diamond_facet" && fresh.kind === "diamond_facet" &&
			loaded.proxyAddress === fresh.proxyAddress &&
			loaded.proxyChainID === fresh.proxyChainID &&
			loaded.facetSelectors.length === fresh.facetSelectors.length &&
			loaded.facetSelectors.every((selector, index) => selector === fresh.facetSelectors[index]);
	}
  return (
    loaded.proxyAddress === fresh.proxyAddress &&
    loaded.proxyCodeHash === fresh.proxyCodeHash &&
		loaded.proxyChainID === fresh.proxyChainID &&
		loaded.bindingId === fresh.bindingId &&
		loaded.proxyMechanism === fresh.proxyMechanism &&
		loaded.proxyPattern === fresh.proxyPattern &&
		loaded.beaconAddress === fresh.beaconAddress &&
		loaded.beaconCodeHash === fresh.beaconCodeHash &&
    loaded.standardVersion === fresh.standardVersion &&
    managementImpact(loaded) === managementImpact(fresh)
  );
}

function delegatedTargetsMatch(
  loaded: DelegatedEOAInteractionTarget,
  fresh: DelegatedEOAInteractionTarget,
  requireExactSnapshot: boolean,
): boolean {
  return (
    targetsMatch(loaded, fresh) ||
    (!requireExactSnapshot &&
      loaded.kind === "delegated_eoa" &&
      fresh.kind === "delegated_eoa" &&
      loaded.transactionTarget === fresh.transactionTarget &&
      loaded.abiAddress === fresh.abiAddress &&
      loaded.abiCodeHash === fresh.abiCodeHash &&
      loaded.supportsWrites === fresh.supportsWrites &&
      loaded.requiresFreshBinding === fresh.requiresFreshBinding &&
      loaded.authorityAddress === fresh.authorityAddress &&
      loaded.delegationChainID === fresh.delegationChainID)
  );
}

function managementImpact(target: ContractInteractionTarget): string | undefined {
  return target.kind === "transparent_proxy_admin" || target.kind === "beacon_management"
    ? target.affectedProxyCount
    : undefined;
}

function freshProxyResponse(fresh: FreshProxyDetails): {
  readonly details: ProxyDetails;
  readonly metaChainID?: string;
} {
  if ("data" in fresh) {
    return { details: fresh.data, metaChainID: fresh.meta.chain_id };
  }
  return { details: fresh };
}

function freezeTarget<Target extends ContractInteractionTarget>(target: Target): Target {
  return Object.freeze(target);
}

function unknownOutcome(error: unknown): InteractionSendOutcome {
  return Object.freeze({ status: "unknown" as const, error });
}
