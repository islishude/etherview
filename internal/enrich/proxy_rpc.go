package enrich

import (
	"context"
	"errors"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
)

type rpcProxyDetector struct {
	caller                    rpcCaller
	limits                    ProxyLimits
	artifact                  func(context.Context, common.Address, common.Hash) (proxyArtifactEvidence, bool, error)
	cloneCreation             func(context.Context, common.Address, []byte) (bool, error)
	codeCache                 map[common.Address][]byte
	beaconImplementationCache map[common.Address]cachedBeaconImplementation
	v2Enabled                 bool
	safeEnabled               bool
	diamondEnabled            bool
	observer                  ProxyDetectionObserver
}

type cachedBeaconImplementation struct {
	address common.Address
	valid   bool
}

func (detector rpcProxyDetector) detectBlock(ctx context.Context, job Job, candidates []proxyCandidate) ([]proxyDetection, error) {
	if detector.caller == nil {
		return nil, errors.New("proxy RPC detector is not configured")
	}
	if detector.codeCache == nil {
		detector.codeCache = make(map[common.Address][]byte)
	}
	if detector.beaconImplementationCache == nil {
		detector.beaconImplementationCache = make(map[common.Address]cachedBeaconImplementation)
	}
	memo := &proxyDetectionRPCMemo{}
	result := make([]proxyDetection, 0, len(candidates))
	for _, candidate := range candidates {
		if !detector.v2Enabled {
			detection, err := detector.detect(
				ctx, candidate, rpc.BlockNumberOrHashWithHash(job.BlockHash, true),
			)
			if err != nil {
				return nil, err
			}
			result = append(result, detection)
			continue
		}
		started := time.Now()
		detectionContext, err := newProxyDetectionContext(
			job.ChainID, candidate.address, job.BlockNumber, job.BlockHash,
			ProxyDetectionBulk, detector.caller, detector.limits.MaxCodeBytes, memo,
		)
		if err != nil {
			return nil, err
		}
		beforeCounters := detectionContext.Counters()
		adapter := &openZeppelinProxyDetectorAdapter{legacy: detector, candidate: candidate}
		detectors := []ProxyDetector{adapter}
		if detector.diamondEnabled {
			detectors = append(detectors, newDiamondProxyDetector(candidate))
		}
		if detector.safeEnabled {
			detectors = append(detectors, newSafeProxyDetector())
		}
		resolved, err := RunProxyDetectors(ctx, detectionContext, detectors)
		if err != nil {
			return nil, err
		}
		if adapter.err != nil {
			// The framework runs in compatibility mode until the V2 observation is
			// persisted. Preserve proxy@2's stage-level RPC failure contract.
			return nil, adapter.err
		}
		if adapter.legacyResult == nil {
			return nil, Permanent(errors.New("OpenZeppelin detector omitted its legacy result"))
		}
		detection := *adapter.legacyResult
		compareLegacyProxyProjection(detection, &resolved)
		detection.v2 = resolved
		detection.v2Active = true
		if detector.observer != nil {
			counters := detectionContext.Counters().since(beforeCounters)
			detector.observer.ObserveProxyDetectionRun(
				time.Since(started), counters.GetCode, counters.GetStorageAt, counters.Call,
				counters.GetCodeErrors, counters.GetStorageAtErrors, counters.CallErrors,
				len(resolved.Conflicts) != 0,
			)
			for _, outcome := range resolved.Outcomes {
				detector.observer.RecordProxyDetectionResult(
					outcome.Detector, string(outcome.Family), string(outcome.Status),
					string(outcome.Confidence),
				)
			}
		}
		result = append(result, detection)
	}
	return result, nil
}

func (detector rpcProxyDetector) detect(
	ctx context.Context,
	candidate proxyCandidate,
	blockReference rpc.BlockNumberOrHash,
) (proxyDetection, error) {
	code, err := detector.getCode(ctx, candidate.address, blockReference)
	if err != nil {
		return proxyDetection{}, err
	}
	detection := proxyDetection{candidate: candidate, code: code, codeHash: codeHash(code)}
	if len(code) == 0 {
		return detection, nil
	}
	if minimal, ok := DetectEIP1167(code); ok {
		if minimal.Implementation == (common.Address{}) {
			detection.rejected = "minimal_zero_implementation"
			return detection, nil
		}
		if minimal.ImmutableArgsTooLarge {
			detection.rejected = "immutable_args_too_large"
			return detection, nil
		}
		if minimal.Implementation == candidate.address {
			detection.rejected = "self_implementation"
			return detection, nil
		}
		immutableArgsExact := false
		if len(minimal.TrailingData) != 0 {
			if detector.cloneCreation != nil {
				immutableArgsExact, err = detector.cloneCreation(ctx, candidate.address, code)
				if err != nil {
					return proxyDetection{}, err
				}
			}
			if !immutableArgsExact {
				// A canonical EIP-1167 prefix with trailing bytes is only a
				// candidate until a published CREATE/CREATE2 trace proves that
				// the exact OpenZeppelin initcode returned this runtime. Keep the
				// negative generation so a later Trace publication can promote it
				// without mutating an earlier proxy observation.
				detection.rejected = "immutable_args_creation_unverified"
				return detection, nil
			}
		}
		implementationCode, err := detector.getCode(ctx, minimal.Implementation, blockReference)
		if err != nil {
			return proxyDetection{}, err
		}
		detection.proxy = &proxyResolution{
			kind: ProxyMinimal1167, pattern: ProxyPatternClone, evidenceState: "exact",
			implementation:     minimal.Implementation,
			implementationCode: implementationCode, implementationHash: codeHash(implementationCode),
			minimalExact: minimal.Exact, immutableArgsExact: immutableArgsExact,
			immutableArgs: common.CopyBytes(minimal.TrailingData),
		}
		return detection, nil
	}
	implementationWord, err := detector.getStorage(ctx, candidate.address, EIP1967ImplementationSlot, blockReference)
	if err != nil {
		return proxyDetection{}, err
	}
	beaconWord, err := detector.getStorage(ctx, candidate.address, EIP1967BeaconSlot, blockReference)
	if err != nil {
		return proxyDetection{}, err
	}
	adminWord, err := detector.getStorage(ctx, candidate.address, EIP1967AdminSlot, blockReference)
	if err != nil {
		return proxyDetection{}, err
	}
	implementationSlot, implementationSlotValid := strictStorageAddress(implementationWord)
	beaconSlot, beaconSlotValid := strictStorageAddress(beaconWord)
	adminSlot, adminSlotValid := strictStorageAddress(adminWord)

	var proxyArtifact proxyArtifactEvidence
	var artifactFound bool
	if detector.artifact != nil {
		proxyArtifact, artifactFound, err = detector.artifact(ctx, candidate.address, detection.codeHash)
		if err != nil {
			return proxyDetection{}, err
		}
	}
	if artifactFound {
		detection.exact, err = detector.resolveAuthenticatedArtifact(
			ctx, candidate, detection.codeHash, proxyArtifact,
			implementationSlot, implementationSlotValid,
			beaconSlot, beaconSlotValid, adminSlot, adminSlotValid,
			blockReference,
		)
		if err != nil {
			return proxyDetection{}, err
		}
	}

	references, slotsErr := ParseEIP1967Storage(implementationWord, beaconWord)
	if slotsErr != nil || len(references) > 1 {
		if detection.exact == nil {
			if slotsErr != nil {
				detection.rejected = "invalid_slot_address"
			} else {
				detection.rejected = "ambiguous_slots"
			}
			return detection, nil
		}
		detection.proxy = genericResolutionFromArtifact(detection.exact, adminSlot, adminSlotValid, beaconSlot, beaconSlotValid)
		return detection, nil
	}
	if len(references) == 0 {
		if detection.exact != nil {
			detection.proxy = genericResolutionFromArtifact(detection.exact, adminSlot, adminSlotValid, beaconSlot, beaconSlotValid)
		}
		return detection, nil
	}
	reference := references[0]
	resolution := &proxyResolution{kind: reference.Kind, pattern: ProxyPatternUnknown, evidenceState: "generic"}
	implementation := reference.Target
	if reference.Kind == ProxyBeacon {
		beaconAddress := reference.Target
		beaconCode, codeErr := detector.getCode(ctx, beaconAddress, blockReference)
		if codeErr != nil {
			return proxyDetection{}, codeErr
		}
		if len(beaconCode) == 0 {
			if detection.exact != nil {
				detection.proxy = genericResolutionFromArtifact(detection.exact, adminSlot, adminSlotValid, beaconSlot, beaconSlotValid)
				return detection, nil
			}
			detection.rejected = "beacon_has_no_code"
			return detection, nil
		}
		var valid bool
		implementation, valid, err = detector.beaconImplementation(ctx, beaconAddress, blockReference)
		if err != nil {
			return proxyDetection{}, err
		}
		if !valid {
			if detection.exact != nil {
				detection.proxy = genericResolutionFromArtifact(detection.exact, adminSlot, adminSlotValid, beaconSlot, beaconSlotValid)
				return detection, nil
			}
			detection.rejected = "invalid_beacon_implementation"
			return detection, nil
		}
		resolution.evidenceState = "partial"
		resolution.beacon = &beaconAddress
		resolution.beaconCode = beaconCode
		resolution.beaconHash = codeHash(beaconCode)
	}
	if implementation == candidate.address {
		detection.rejected = "self_implementation"
		return detection, nil
	}
	implementationCode, err := detector.getCode(ctx, implementation, blockReference)
	if err != nil {
		return proxyDetection{}, err
	}
	if len(implementationCode) == 0 {
		if detection.exact != nil {
			detection.proxy = genericResolutionFromArtifact(detection.exact, adminSlot, adminSlotValid, beaconSlot, beaconSlotValid)
			return detection, nil
		}
		detection.rejected = "implementation_has_no_code"
		return detection, nil
	}
	resolution.implementation = implementation
	resolution.implementationCode = implementationCode
	resolution.implementationHash = codeHash(implementationCode)
	if reference.Kind == ProxyEIP1967 && adminSlotValid && adminSlot != (common.Address{}) {
		adminCode, codeErr := detector.getCode(ctx, adminSlot, blockReference)
		if codeErr != nil {
			return proxyDetection{}, codeErr
		}
		resolution.admin = &adminSlot
		resolution.adminCode = adminCode
		resolution.adminHash = codeHash(adminCode)
		resolution.evidenceState = "partial"
	}
	detection.proxy = resolution
	return detection, nil
}

func strictStorageAddress(word common.Hash) (common.Address, bool) {
	if word == (common.Hash{}) {
		return common.Address{}, true
	}
	address, err := AddressFromWord(word)
	return address, err == nil
}

func genericResolutionFromArtifact(
	exact *proxyArtifactResolution,
	adminSlot common.Address,
	adminSlotValid bool,
	beaconSlot common.Address,
	beaconSlotValid bool,
) *proxyResolution {
	_ = adminSlot
	_ = adminSlotValid
	_ = beaconSlot
	_ = beaconSlotValid
	resolution := exact.proxyResolution
	resolution.pattern = ProxyPatternUnknown
	resolution.standardVersion = ""
	resolution.evidenceState = "generic"
	resolution.admin = nil
	resolution.adminCode = nil
	resolution.adminHash = common.Hash{}
	resolution.beacon = nil
	resolution.beaconCode = nil
	resolution.beaconHash = common.Hash{}
	return &resolution
}

func (detector rpcProxyDetector) resolveAuthenticatedArtifact(
	ctx context.Context,
	candidate proxyCandidate,
	proxyCodeHash common.Hash,
	artifact proxyArtifactEvidence,
	implementationSlot common.Address,
	implementationSlotValid bool,
	beaconSlot common.Address,
	beaconSlotValid bool,
	adminSlot common.Address,
	adminSlotValid bool,
	blockReference rpc.BlockNumberOrHash,
) (*proxyArtifactResolution, error) {
	if artifact.standardVersion != OpenZeppelin561Standard {
		return nil, Permanent(errors.New("authenticated proxy artifact has an unsupported version"))
	}
	exact := &proxyArtifactResolution{
		proxyArtifactJob: artifact.verificationJob,
		evidence:         map[string]any{"official_sources": true},
	}
	loadImplementation := func(address common.Address) (bool, error) {
		if address == (common.Address{}) || address == candidate.address {
			return false, nil
		}
		implementationCode, err := detector.getCode(ctx, address, blockReference)
		if err != nil {
			return false, err
		}
		if len(implementationCode) == 0 {
			return false, nil
		}
		exact.implementation = address
		exact.implementationCode = implementationCode
		exact.implementationHash = codeHash(implementationCode)
		return true, nil
	}
	switch artifact.kind {
	case "transparent_proxy":
		if artifact.runtimeImmutable == nil || !implementationSlotValid {
			return nil, nil
		}
		ok, err := loadImplementation(implementationSlot)
		if err != nil || !ok {
			return nil, err
		}
		adminCode, err := detector.getCode(ctx, *artifact.runtimeImmutable, blockReference)
		if err != nil {
			return nil, err
		}
		if len(adminCode) == 0 {
			return nil, nil
		}
		admin := *artifact.runtimeImmutable
		exact.kind, exact.pattern = ProxyEIP1967, ProxyPatternTransparent
		exact.standardVersion, exact.evidenceState = OpenZeppelin561Standard, "exact"
		exact.admin = &admin
		exact.adminCode, exact.adminHash = adminCode, codeHash(adminCode)
		exact.evidence["admin_authority"] = "runtime_immutable"
		exact.evidence["admin_slot_matches"] = adminSlotValid && adminSlot == admin
	case "beacon_proxy":
		if artifact.runtimeImmutable == nil {
			return nil, nil
		}
		beacon := *artifact.runtimeImmutable
		beaconCode, err := detector.getCode(ctx, beacon, blockReference)
		if err != nil {
			return nil, err
		}
		if len(beaconCode) == 0 {
			return nil, nil
		}
		implementation, valid, err := detector.beaconImplementation(ctx, beacon, blockReference)
		if err != nil || !valid {
			return nil, err
		}
		ok, err := loadImplementation(implementation)
		if err != nil || !ok {
			return nil, err
		}
		exact.kind, exact.pattern = ProxyBeacon, ProxyPatternBeacon
		exact.standardVersion, exact.evidenceState = OpenZeppelin561Standard, "exact"
		exact.beacon = &beacon
		exact.beaconCode, exact.beaconHash = beaconCode, codeHash(beaconCode)
		exact.evidence["beacon_authority"] = "runtime_immutable"
		exact.evidence["beacon_slot_matches"] = beaconSlotValid && beaconSlot == beacon
	case "erc1967_proxy":
		if !implementationSlotValid {
			return nil, nil
		}
		ok, err := loadImplementation(implementationSlot)
		if err != nil || !ok {
			return nil, err
		}
		exact.kind, exact.pattern = ProxyEIP1967, ProxyPatternERC1967
		exact.standardVersion, exact.evidenceState = OpenZeppelin561Standard, "exact"
	default:
		return nil, nil
	}
	exact.evidence["proxy_code_hash"] = proxyCodeHash.Hex()
	return exact, nil
}

func (detector rpcProxyDetector) detectBeaconBlock(
	ctx context.Context,
	job Job,
	candidates []proxyCandidate,
) ([]beaconDetection, error) {
	if detector.caller == nil {
		return nil, errors.New("proxy RPC detector is not configured")
	}
	blockReference := rpc.BlockNumberOrHashWithHash(job.BlockHash, true)
	if detector.codeCache == nil {
		detector.codeCache = make(map[common.Address][]byte)
	}
	if detector.beaconImplementationCache == nil {
		detector.beaconImplementationCache = make(map[common.Address]cachedBeaconImplementation)
	}
	result := make([]beaconDetection, 0, len(candidates))
	for _, candidate := range candidates {
		detection, err := detector.detectBeacon(ctx, candidate, blockReference)
		if err != nil {
			return nil, err
		}
		result = append(result, detection)
	}
	return result, nil
}

func (detector rpcProxyDetector) detectBeacon(
	ctx context.Context,
	candidate proxyCandidate,
	blockReference rpc.BlockNumberOrHash,
) (beaconDetection, error) {
	code, err := detector.getCode(ctx, candidate.address, blockReference)
	if err != nil {
		return beaconDetection{}, err
	}
	detection := beaconDetection{candidate: candidate, code: code, codeHash: codeHash(code)}
	if len(code) == 0 {
		return detection, nil
	}
	implementation, valid, err := detector.beaconImplementation(ctx, candidate.address, blockReference)
	if err != nil {
		return beaconDetection{}, err
	}
	if !valid {
		detection.rejected = "invalid_beacon_implementation"
		return detection, nil
	}
	if implementation == candidate.address {
		detection.rejected = "self_implementation"
		return detection, nil
	}
	implementationCode, err := detector.getCode(ctx, implementation, blockReference)
	if err != nil {
		return beaconDetection{}, err
	}
	if len(implementationCode) == 0 {
		detection.rejected = "implementation_has_no_code"
		return detection, nil
	}
	detection.implementation = implementation
	detection.implementationCode = implementationCode
	detection.implementationHash = codeHash(implementationCode)
	return detection, nil
}

func (detector rpcProxyDetector) getCode(
	ctx context.Context,
	address common.Address,
	blockReference rpc.BlockNumberOrHash,
) ([]byte, error) {
	if cached, exists := detector.codeCache[address]; exists {
		return common.CopyBytes(cached), nil
	}
	var encoded hexutil.Bytes
	if err := detector.caller.CallContext(
		ctx, &encoded, "eth_getCode", address, blockReference,
	); err != nil {
		return nil, exactStateRPCError(ctx, "eth_getCode", err)
	}
	code := []byte(encoded)
	if len(code) > detector.limits.MaxCodeBytes {
		return nil, Permanent(errors.New("contract bytecode exceeds proxy detection limit"))
	}
	if code == nil {
		// An exact empty-code observation is different from SQL NULL (code bytes
		// deliberately omitted). Keep an allocated zero-length slice so the
		// Keccak(empty) fact can be audited and reused.
		code = make([]byte, 0)
	}
	if detector.codeCache != nil {
		detector.codeCache[address] = common.CopyBytes(code)
	}
	return code, nil
}

func (detector rpcProxyDetector) getStorage(
	ctx context.Context,
	address common.Address,
	slot common.Hash,
	blockReference rpc.BlockNumberOrHash,
) (common.Hash, error) {
	var encoded hexutil.Bytes
	if err := detector.caller.CallContext(
		ctx, &encoded, "eth_getStorageAt", address, slot, blockReference,
	); err != nil {
		return common.Hash{}, exactStateRPCError(ctx, "eth_getStorageAt", err)
	}
	value := []byte(encoded)
	if len(value) != common.HashLength {
		return common.Hash{}, Permanent(errors.New("eth_getStorageAt returned a non-word value"))
	}
	return WordFromBytes(value)
}

func (detector rpcProxyDetector) beaconImplementation(
	ctx context.Context,
	beacon common.Address,
	blockReference rpc.BlockNumberOrHash,
) (common.Address, bool, error) {
	if cached, exists := detector.beaconImplementationCache[beacon]; exists {
		return cached.address, cached.valid, nil
	}
	input, err := packStateProbe("implementation")
	if err != nil {
		return common.Address{}, false, Permanent(err)
	}
	request := map[string]any{"to": beacon, "data": hexutil.Bytes(input)}
	var encoded hexutil.Bytes
	if err := detector.caller.CallContext(
		ctx, &encoded, "eth_call", request, blockReference,
	); err != nil {
		if executionReverted(err) {
			if detector.beaconImplementationCache != nil {
				detector.beaconImplementationCache[beacon] = cachedBeaconImplementation{}
			}
			return common.Address{}, false, nil
		}
		return common.Address{}, false, exactStateRPCError(ctx, "eth_call", err)
	}
	value := []byte(encoded)
	implementation, err := ParseBeaconImplementation(value)
	if err != nil {
		if detector.beaconImplementationCache != nil {
			detector.beaconImplementationCache[beacon] = cachedBeaconImplementation{}
		}
		return common.Address{}, false, nil
	}
	if detector.beaconImplementationCache != nil {
		detector.beaconImplementationCache[beacon] = cachedBeaconImplementation{
			address: implementation, valid: true,
		}
	}
	return implementation, true, nil
}
