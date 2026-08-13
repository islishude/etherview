# ADR-0038: Selector-scoped ERC-2535 Diamond identity

Status: accepted

## Context

The existing proxy contract projects one implementation, optional Beacon, and
one management target. ERC-2535 instead routes each four-byte function selector
to one of many facets. A selector may also name an immutable function executed
by the Diamond's own runtime. Treating the first facet as an implementation
would corrupt ABI attribution, historical decoding, interaction targets, and
upgrade history.

ERC-2535 defines Loupe calls rather than a universal implementation storage
slot. Loupe enumeration is unbounded by the standard and is hostile external
input. DiamondCut logs describe ordered selector mutations but do not by
themselves prove that an emitter is a conforming Diamond.

## Decision

- Diamond identity is additive to the existing proxy model. It owns a Diamond
  address, selector-scoped targets, completeness, validation strength,
  standard-DiamondCut presence, evidence, and warnings. It never fills the
  singular legacy implementation with an arbitrary facet.
- A target has an address, role, optional selector set, and exact code identity.
  External routes use role `facet`; a Loupe row whose address equals the Diamond
  uses role `immutable`. Compatibility exposes a de-duplicated, unordered list
  of external implementation addresses only.
- Current Diamond snapshots are identified by chain ID and exact block hash.
  All code and call evidence uses one state endpoint and the same EIP-1898 block
  selector. RPC, historical-state, revert, malformed-return, and product-limit
  outcomes remain distinct.
- Deep detection prefers `facets()`, falls back to `facetAddresses()` plus
  `facetFunctionSelectors(address)`, and validates `facetAddress(bytes4)` fully
  or with deterministic sampling. ERC-165 Loupe support and DiamondCut events
  are corroborating, never independently confirming, evidence.
- The detector checks zero and duplicate facets, exact bytes4 selectors, global
  selector uniqueness, empty selector lists, external facet code, Loupe address
  and selector consistency, an absent-selector zero result, and the current
  `diamondCut` selector. A Diamond-address target is valid immutable evidence,
  not a cycle.
- Status, completeness, and validation are independent. A truncated
  enumeration cannot be reported as complete; deterministic sampling of a
  fully loaded selector map remains explicit through `validation=sampled`. A
  limit after other confirming evidence may be confirmed/partial; an
  unavailable required read is unknown; contradictory decoded evidence is
  inconsistent.
- Default limits are 2 MiB raw return data, 256 facets, 16,384 selectors total,
  4,096 selectors per facet, 256 cross-check calls, and 12 concurrent calls.
  Limits are product safety boundaries, not claims about the ERC-2535 standard,
  and every truncation reason is public.
- Raw DiamondCut events retain block, transaction, log, cut index, action,
  facet, selectors, init address, and calldata by immutable block identity.
  Canonical selector intervals order changes by block, transaction, log, cut,
  and selector position; reorgs retain orphan facts and rebuild canonical
  intervals. `_init` is never inferred to be a facet.
- Current Loupe snapshots reconcile against complete event coverage. A mismatch
  is inconsistent, not silently repaired. A missing standard `diamondCut`
  selector means only that the standard entry point was not detected; it does
  not prove immutability or absence of another upgrade authority.
- Function ABI candidates are filtered to selectors actively routed to their
  source facet at the queried historical position. Calls use the Diamond as
  target. Exact trace execution identity wins when a cut and later internal
  call occur in one transaction. Selector collisions, event topics, and custom
  errors retain candidate provenance and explicit ambiguity.
- OpenZeppelin, Safe, and Diamond detectors all run through the evidence
  resolver. An ERC-1967 shell delegating to a Diamond router is a compositional
  layer, not automatically a conflict. Security-sensitive write authorization
  remains separately fenced to the exact selected layer and target identities.

## Consequences

Public schemas and PostgreSQL gain selector-scoped records and bounded pages.
The web can display facets and active functions without inventing a primary
implementation. Historical decoding becomes position-aware. Detection costs
remain bounded and deep work is candidate- or request-triggered rather than
issued against every indexed runtime.
