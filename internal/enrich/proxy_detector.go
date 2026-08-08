package enrich

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
)

const ProxyDetectorFrameworkVersion = "proxy-detectors@1"

type ProxyDetectionMode string

const (
	ProxyDetectionBulk ProxyDetectionMode = "bulk"
	ProxyDetectionDeep ProxyDetectionMode = "deep"
)

type ProxyDetectionStatus string

const (
	ProxyStatusConfirmed    ProxyDetectionStatus = "confirmed"
	ProxyStatusCandidate    ProxyDetectionStatus = "candidate"
	ProxyStatusInconsistent ProxyDetectionStatus = "inconsistent"
	ProxyStatusNotDetected  ProxyDetectionStatus = "not-detected"
	ProxyStatusUnknown      ProxyDetectionStatus = "unknown"
)

type ProxyDetectionConfidence string

const (
	ProxyConfidenceHigh   ProxyDetectionConfidence = "high"
	ProxyConfidenceMedium ProxyDetectionConfidence = "medium"
	ProxyConfidenceLow    ProxyDetectionConfidence = "low"
)

type ProxyFamily string

const (
	ProxyFamilyERC1167 ProxyFamily = "erc1167"
	ProxyFamilyERC1967 ProxyFamily = "erc1967"
	ProxyFamilySafe    ProxyFamily = "safe"
	ProxyFamilyCustom  ProxyFamily = "custom"
)

type ProxyImplementationRole string

const (
	ProxyRoleImplementation ProxyImplementationRole = "implementation"
	ProxyRoleSingleton      ProxyImplementationRole = "singleton"
)

type ProxyEvidenceKind string

const (
	ProxyEvidenceRuntimeBytecode  ProxyEvidenceKind = "runtime-bytecode"
	ProxyEvidenceRuntimeCodeHash  ProxyEvidenceKind = "runtime-code-hash"
	ProxyEvidenceStorageSlot      ProxyEvidenceKind = "storage-slot"
	ProxyEvidenceContractCall     ProxyEvidenceKind = "contract-call"
	ProxyEvidenceFactoryLog       ProxyEvidenceKind = "factory-log"
	ProxyEvidenceDeploymentRecord ProxyEvidenceKind = "deployment-registry"
	ProxyEvidenceExecutionTrace   ProxyEvidenceKind = "execution-trace"
)

type ProxyDetectionEvidence struct {
	Kind        ProxyEvidenceKind
	Description string
	Address     *common.Address
	Slot        *common.Hash
	Value       []byte
}

type ProxyDetectionV2 struct {
	Detector                string
	DetectorVersion         string
	Priority                int
	Family                  ProxyFamily
	Variant                 string
	Status                  ProxyDetectionStatus
	Confidence              ProxyDetectionConfidence
	Proxy                   common.Address
	Implementation          *common.Address
	ImplementationRole      ProxyImplementationRole
	ImplementationPath      []common.Address
	Admin                   *common.Address
	Beacon                  *common.Address
	CanonicalProxyShell     bool
	ImplementationHasCode   bool
	OfficialSingleton       bool
	SingletonVersion        string
	SingletonDeploymentType string
	InitialSingleton        *common.Address
	SingletonChanged        bool
	Evidence                []ProxyDetectionEvidence
	Warnings                []string
	ChainID                 string
	BlockNumber             uint64
	BlockHash               common.Hash
}

func (detection ProxyDetectionV2) validate() error {
	if detection.Detector == "" || detection.DetectorVersion == "" {
		return errors.New("proxy detection is missing detector identity")
	}
	if detection.Proxy == (common.Address{}) || detection.ChainID == "" || detection.BlockHash == (common.Hash{}) {
		return errors.New("proxy detection is missing chain, block, or proxy identity")
	}
	chainID, ok := new(big.Int).SetString(detection.ChainID, 10)
	if !ok || chainID.Sign() <= 0 {
		return errors.New("proxy detection chain ID is not decimal")
	}
	switch detection.Status {
	case ProxyStatusConfirmed, ProxyStatusCandidate, ProxyStatusInconsistent,
		ProxyStatusNotDetected, ProxyStatusUnknown:
	default:
		return errors.New("proxy detection status is invalid")
	}
	switch detection.Confidence {
	case ProxyConfidenceHigh, ProxyConfidenceMedium, ProxyConfidenceLow:
	default:
		return errors.New("proxy detection confidence is invalid")
	}
	if detection.Status == ProxyStatusConfirmed || detection.Status == ProxyStatusCandidate || detection.Status == ProxyStatusInconsistent {
		if detection.Family == "" {
			return errors.New("positive proxy detection is missing family")
		}
	}
	for _, evidence := range detection.Evidence {
		if evidence.Kind == "" || evidence.Description == "" {
			return errors.New("proxy detection contains incomplete evidence")
		}
	}
	return nil
}

type ProxyDetector interface {
	ID() string
	Version() string
	Priority() int
	SupportedModes() []ProxyDetectionMode
	Detect(context.Context, *ProxyDetectionContext) (*ProxyDetectionV2, error)
}

type openZeppelinProxyDetectorAdapter struct {
	legacy       rpcProxyDetector
	candidate    proxyCandidate
	legacyResult *proxyDetection
	err          error
}

func (*openZeppelinProxyDetectorAdapter) ID() string      { return "openzeppelin" }
func (*openZeppelinProxyDetectorAdapter) Version() string { return OpenZeppelin561Standard }
func (*openZeppelinProxyDetectorAdapter) Priority() int   { return 100 }
func (*openZeppelinProxyDetectorAdapter) SupportedModes() []ProxyDetectionMode {
	return []ProxyDetectionMode{ProxyDetectionBulk, ProxyDetectionDeep}
}

func (detector *openZeppelinProxyDetectorAdapter) Detect(
	ctx context.Context,
	detectionContext *ProxyDetectionContext,
) (*ProxyDetectionV2, error) {
	code, err := detectionContext.GetCode(ctx, detectionContext.Address())
	if err != nil {
		detector.err = err
		return nil, err
	}
	if detector.legacy.codeCache == nil {
		detector.legacy.codeCache = make(map[common.Address][]byte)
	}
	detector.legacy.codeCache[detectionContext.Address()] = common.CopyBytes(code)
	legacy, err := detector.legacy.detect(ctx, detector.candidate, detectionContext.blockReference())
	if err != nil {
		detector.err = err
		return nil, err
	}
	detector.legacyResult = &legacy
	return openZeppelinDetectionV2(legacy), nil
}

func openZeppelinDetectionV2(legacy proxyDetection) *ProxyDetectionV2 {
	result := &ProxyDetectionV2{
		Status: ProxyStatusNotDetected, Confidence: ProxyConfidenceLow,
		Evidence: []ProxyDetectionEvidence{{
			Kind: ProxyEvidenceRuntimeCodeHash, Description: "target runtime code hash",
			Address: addressCopy(legacy.candidate.address), Value: common.CopyBytes(legacy.codeHash[:]),
		}},
	}
	if len(legacy.code) == 0 {
		result.Evidence[0].Description = "target has no runtime code"
		return result
	}
	if legacy.rejected != "" {
		result.Warnings = []string{legacy.rejected}
		switch legacy.rejected {
		case "minimal_zero_implementation", "self_implementation":
			result.Family = ProxyFamilyERC1167
			result.Variant = string(ProxyPatternClone)
			result.Status = ProxyStatusInconsistent
			result.Confidence = ProxyConfidenceHigh
			result.Evidence[0].Kind = ProxyEvidenceRuntimeBytecode
			result.Evidence[0].Description = "exact ERC-1167 shell has an invalid implementation target"
		case "immutable_args_too_large", "immutable_args_creation_unverified":
			result.Family = ProxyFamilyERC1167
			result.Variant = string(ProxyPatternClone)
			result.Status = ProxyStatusCandidate
			result.Confidence = ProxyConfidenceMedium
			result.Evidence[0].Kind = ProxyEvidenceRuntimeBytecode
			result.Evidence[0].Description = "ERC-1167-compatible runtime requires additional creation evidence"
		case "ambiguous_slots", "implementation_has_no_code", "beacon_has_no_code", "invalid_beacon_implementation":
			result.Family = ProxyFamilyERC1967
			result.Status = ProxyStatusCandidate
			result.Confidence = ProxyConfidenceLow
		default:
			result.Status = ProxyStatusNotDetected
		}
		return result
	}
	resolved := legacy.proxy
	authenticated := false
	if legacy.exact != nil {
		resolved = &legacy.exact.proxyResolution
		authenticated = true
	}
	if resolved == nil {
		return result
	}
	result.Family = proxyFamilyForKind(resolved.kind)
	result.Variant = string(resolved.pattern)
	result.Implementation = addressCopy(resolved.implementation)
	result.ImplementationRole = ProxyRoleImplementation
	result.ImplementationHasCode = len(resolved.implementationCode) != 0
	result.ImplementationPath = []common.Address{legacy.candidate.address}
	if resolved.beacon != nil {
		result.Beacon = addressCopy(*resolved.beacon)
		result.ImplementationPath = append(result.ImplementationPath, *resolved.beacon)
	}
	if resolved.implementation != (common.Address{}) {
		result.ImplementationPath = append(result.ImplementationPath, resolved.implementation)
	}
	if resolved.admin != nil {
		result.Admin = addressCopy(*resolved.admin)
	}
	result.Status = ProxyStatusCandidate
	result.Confidence = ProxyConfidenceLow
	if authenticated || resolved.kind == ProxyMinimal1167 && (resolved.minimalExact || resolved.immutableArgsExact) {
		result.Status = ProxyStatusConfirmed
		result.Confidence = ProxyConfidenceHigh
	} else if resolved.evidenceState == "partial" {
		result.Confidence = ProxyConfidenceMedium
	}
	if len(resolved.implementationCode) == 0 {
		result.Warnings = append(result.Warnings, "implementation_has_no_code")
		if result.Status == ProxyStatusConfirmed {
			result.Status = ProxyStatusInconsistent
		}
	}
	if resolved.implementation != (common.Address{}) {
		result.Evidence = append(result.Evidence, ProxyDetectionEvidence{
			Kind: ProxyEvidenceStorageSlot, Description: "current proxy implementation target",
			Address: addressCopy(resolved.implementation),
		})
	}
	if resolved.beacon != nil {
		result.Evidence = append(result.Evidence, ProxyDetectionEvidence{
			Kind: ProxyEvidenceContractCall, Description: "beacon implementation path",
			Address: addressCopy(*resolved.beacon),
		})
	}
	if authenticated {
		result.Evidence = append(result.Evidence, ProxyDetectionEvidence{
			Kind: ProxyEvidenceDeploymentRecord, Description: "source-authenticated OpenZeppelin artifact",
		})
		if value, ok := legacy.exact.evidence["admin_slot_matches"].(bool); ok && !value {
			result.Warnings = append(result.Warnings, "admin compatibility slot conflicts with runtime immutable")
		}
		if value, ok := legacy.exact.evidence["beacon_slot_matches"].(bool); ok && !value {
			result.Warnings = append(result.Warnings, "beacon compatibility slot conflicts with runtime immutable")
		}
	}
	return result
}

func proxyFamilyForKind(kind ProxyKind) ProxyFamily {
	switch kind {
	case ProxyMinimal1167:
		return ProxyFamilyERC1167
	case ProxyEIP1967, ProxyBeacon:
		return ProxyFamilyERC1967
	default:
		return ProxyFamilyCustom
	}
}

func addressCopy(address common.Address) *common.Address {
	copy := address
	return &copy
}

type ProxyCallInput struct {
	To   common.Address
	Data []byte
	From *common.Address
}

type ProxyCallResult struct {
	Success bool
	Data    []byte
	Error   string
}

type ProxyRPCCounters struct {
	GetCode            uint64
	GetCodeErrors      uint64
	GetStorageAt       uint64
	GetStorageAtErrors uint64
	Call               uint64
	CallErrors         uint64
}

func (current ProxyRPCCounters) since(previous ProxyRPCCounters) ProxyRPCCounters {
	return ProxyRPCCounters{
		GetCode:            current.GetCode - previous.GetCode,
		GetCodeErrors:      current.GetCodeErrors - previous.GetCodeErrors,
		GetStorageAt:       current.GetStorageAt - previous.GetStorageAt,
		GetStorageAtErrors: current.GetStorageAtErrors - previous.GetStorageAtErrors,
		Call:               current.Call - previous.Call,
		CallErrors:         current.CallErrors - previous.CallErrors,
	}
}

type ProxyDetectionCacheKey struct {
	ChainID         string
	Address         common.Address
	BlockNumber     uint64
	BlockHash       common.Hash
	DetectorVersion string
}

type proxyDetectionRPCMemo struct {
	code    map[proxyCodeCacheKey][]byte
	storage map[proxyStorageCacheKey]common.Hash
	calls   map[proxyCallCacheKey]ProxyCallResult
	counts  ProxyRPCCounters
}

type proxyCodeCacheKey struct {
	identity ProxyDetectionCacheKey
	address  common.Address
}

type proxyStorageCacheKey struct {
	identity ProxyDetectionCacheKey
	address  common.Address
	slot     common.Hash
}

type proxyCallCacheKey struct {
	identity ProxyDetectionCacheKey
	to       common.Address
	from     common.Address
	hasFrom  bool
	data     string
}

type ProxyDetectionContext struct {
	chainID       string
	address       common.Address
	blockNumber   uint64
	blockHash     common.Hash
	mode          ProxyDetectionMode
	caller        rpcCaller
	maximumBytes  int
	detectorSuite string
	memo          *proxyDetectionRPCMemo
}

func newProxyDetectionContext(
	chainID string,
	address common.Address,
	blockNumber uint64,
	blockHash common.Hash,
	mode ProxyDetectionMode,
	caller rpcCaller,
	maximumBytes int,
	memo *proxyDetectionRPCMemo,
) (*ProxyDetectionContext, error) {
	if chainID == "" || address == (common.Address{}) || blockHash == (common.Hash{}) || caller == nil {
		return nil, errors.New("proxy detection context is missing chain, block, address, or RPC caller")
	}
	if mode != ProxyDetectionBulk && mode != ProxyDetectionDeep {
		return nil, errors.New("proxy detection mode is invalid")
	}
	if maximumBytes <= 0 {
		return nil, errors.New("proxy detection response limit is invalid")
	}
	if memo == nil {
		memo = &proxyDetectionRPCMemo{}
	}
	if memo.code == nil {
		memo.code = make(map[proxyCodeCacheKey][]byte)
	}
	if memo.storage == nil {
		memo.storage = make(map[proxyStorageCacheKey]common.Hash)
	}
	if memo.calls == nil {
		memo.calls = make(map[proxyCallCacheKey]ProxyCallResult)
	}
	return &ProxyDetectionContext{
		chainID: chainID, address: address, blockNumber: blockNumber,
		blockHash: blockHash, mode: mode, caller: caller,
		maximumBytes: maximumBytes, detectorSuite: ProxyDetectorFrameworkVersion,
		memo: memo,
	}, nil
}

func (detectionContext *ProxyDetectionContext) ChainID() string { return detectionContext.chainID }
func (detectionContext *ProxyDetectionContext) Address() common.Address {
	return detectionContext.address
}
func (detectionContext *ProxyDetectionContext) BlockNumber() uint64 {
	return detectionContext.blockNumber
}
func (detectionContext *ProxyDetectionContext) BlockHash() common.Hash {
	return detectionContext.blockHash
}
func (detectionContext *ProxyDetectionContext) Mode() ProxyDetectionMode {
	return detectionContext.mode
}

func (detectionContext *ProxyDetectionContext) CacheKey() ProxyDetectionCacheKey {
	return ProxyDetectionCacheKey{
		ChainID: detectionContext.chainID, Address: detectionContext.address,
		BlockNumber: detectionContext.blockNumber, BlockHash: detectionContext.blockHash,
		DetectorVersion: detectionContext.detectorSuite,
	}
}

func (detectionContext *ProxyDetectionContext) Counters() ProxyRPCCounters {
	if detectionContext == nil || detectionContext.memo == nil {
		return ProxyRPCCounters{}
	}
	return detectionContext.memo.counts
}

func (detectionContext *ProxyDetectionContext) GetCode(ctx context.Context, address common.Address) ([]byte, error) {
	if detectionContext == nil || detectionContext.caller == nil {
		return nil, errors.New("proxy detection context is not configured")
	}
	identity := detectionContext.CacheKey()
	identity.Address = address
	key := proxyCodeCacheKey{identity: identity, address: address}
	if cached, ok := detectionContext.memo.code[key]; ok {
		return common.CopyBytes(cached), nil
	}
	var encoded hexutil.Bytes
	detectionContext.memo.counts.GetCode++
	if err := detectionContext.caller.CallContext(
		ctx, &encoded, "eth_getCode", address, detectionContext.blockReference(),
	); err != nil {
		detectionContext.memo.counts.GetCodeErrors++
		return nil, exactStateRPCError(ctx, "eth_getCode", err)
	}
	value := []byte(encoded)
	if len(value) > detectionContext.maximumBytes {
		return nil, Permanent(errors.New("proxy detection code exceeds configured limit"))
	}
	detectionContext.memo.code[key] = common.CopyBytes(value)
	return common.CopyBytes(value), nil
}

func (detectionContext *ProxyDetectionContext) GetStorageAt(
	ctx context.Context,
	address common.Address,
	slot common.Hash,
) (common.Hash, error) {
	if detectionContext == nil || detectionContext.caller == nil {
		return common.Hash{}, errors.New("proxy detection context is not configured")
	}
	identity := detectionContext.CacheKey()
	identity.Address = address
	key := proxyStorageCacheKey{identity: identity, address: address, slot: slot}
	if cached, ok := detectionContext.memo.storage[key]; ok {
		return cached, nil
	}
	var encoded hexutil.Bytes
	detectionContext.memo.counts.GetStorageAt++
	if err := detectionContext.caller.CallContext(
		ctx, &encoded, "eth_getStorageAt", address, slot, detectionContext.blockReference(),
	); err != nil {
		detectionContext.memo.counts.GetStorageAtErrors++
		return common.Hash{}, exactStateRPCError(ctx, "eth_getStorageAt", err)
	}
	value, err := WordFromBytes([]byte(encoded))
	if err != nil {
		return common.Hash{}, Permanent(errors.New("eth_getStorageAt returned a non-word value"))
	}
	detectionContext.memo.storage[key] = value
	return value, nil
}

func (detectionContext *ProxyDetectionContext) Call(
	ctx context.Context,
	input ProxyCallInput,
) (ProxyCallResult, error) {
	if detectionContext == nil || detectionContext.caller == nil {
		return ProxyCallResult{}, errors.New("proxy detection context is not configured")
	}
	if input.To == (common.Address{}) {
		return ProxyCallResult{}, Permanent(errors.New("proxy detection call target is zero"))
	}
	identity := detectionContext.CacheKey()
	identity.Address = input.To
	key := proxyCallCacheKey{
		identity: identity, to: input.To,
		data: string(common.CopyBytes(input.Data)),
	}
	if input.From != nil {
		key.from, key.hasFrom = *input.From, true
	}
	if cached, ok := detectionContext.memo.calls[key]; ok {
		cached.Data = common.CopyBytes(cached.Data)
		return cached, nil
	}
	request := map[string]any{"to": input.To, "data": hexutil.Bytes(common.CopyBytes(input.Data))}
	if input.From != nil {
		request["from"] = *input.From
	}
	var encoded hexutil.Bytes
	detectionContext.memo.counts.Call++
	if err := detectionContext.caller.CallContext(
		ctx, &encoded, "eth_call", request, detectionContext.blockReference(),
	); err != nil {
		if executionReverted(err) {
			result := ProxyCallResult{Error: "execution_reverted"}
			detectionContext.memo.calls[key] = result
			return result, nil
		}
		detectionContext.memo.counts.CallErrors++
		return ProxyCallResult{}, exactStateRPCError(ctx, "eth_call", err)
	}
	value := []byte(encoded)
	if len(value) > detectionContext.maximumBytes {
		result := ProxyCallResult{Error: "return_too_large"}
		detectionContext.memo.calls[key] = result
		return result, nil
	}
	result := ProxyCallResult{Success: true, Data: common.CopyBytes(value)}
	detectionContext.memo.calls[key] = result
	return result, nil
}

func (detectionContext *ProxyDetectionContext) blockReference() rpc.BlockNumberOrHash {
	return rpc.BlockNumberOrHashWithHash(detectionContext.blockHash, true)
}

type ProxyDetectionResolution struct {
	Status                  ProxyDetectionStatus
	Primary                 *ProxyDetectionV2
	Outcomes                []ProxyDetectionV2
	Conflicts               []string
	LegacyProjectionChanged bool
	LegacyDiffReasons       []string
}

func compareLegacyProxyProjection(legacy proxyDetection, resolution *ProxyDetectionResolution) {
	if resolution == nil {
		return
	}
	legacyPositive := legacy.proxy != nil || legacy.exact != nil
	v2Positive := resolution.Primary != nil && (resolution.Status == ProxyStatusConfirmed ||
		resolution.Status == ProxyStatusCandidate || resolution.Status == ProxyStatusInconsistent)
	if legacyPositive != v2Positive {
		resolution.LegacyProjectionChanged = true
		if v2Positive {
			resolution.LegacyDiffReasons = append(resolution.LegacyDiffReasons, "v2_positive_legacy_not_detected")
		} else {
			resolution.LegacyDiffReasons = append(resolution.LegacyDiffReasons, "legacy_positive_v2_not_detected")
		}
		return
	}
	if !legacyPositive || resolution.Primary == nil {
		return
	}
	legacyResolution := legacy.proxy
	if legacy.exact != nil {
		legacyResolution = &legacy.exact.proxyResolution
	}
	if legacyResolution == nil {
		return
	}
	if proxyFamilyForKind(legacyResolution.kind) != resolution.Primary.Family {
		resolution.LegacyProjectionChanged = true
		resolution.LegacyDiffReasons = append(resolution.LegacyDiffReasons, "proxy_family_changed")
	}
	if resolution.Primary.Implementation != nil &&
		legacyResolution.implementation != *resolution.Primary.Implementation {
		resolution.LegacyProjectionChanged = true
		resolution.LegacyDiffReasons = append(resolution.LegacyDiffReasons, "implementation_changed")
	}
}

func ResolveProxyDetections(detections []ProxyDetectionV2) (ProxyDetectionResolution, error) {
	resolved := ProxyDetectionResolution{Status: ProxyStatusNotDetected}
	if len(detections) == 0 {
		return resolved, nil
	}
	resolved.Outcomes = append([]ProxyDetectionV2(nil), detections...)
	for index := range resolved.Outcomes {
		if err := resolved.Outcomes[index].validate(); err != nil {
			return ProxyDetectionResolution{}, fmt.Errorf("validate detector result %d: %w", index, err)
		}
	}
	slices.SortStableFunc(resolved.Outcomes, compareProxyDetections)
	positive := make([]int, 0, len(resolved.Outcomes))
	unknown := false
	for index := range resolved.Outcomes {
		switch resolved.Outcomes[index].Status {
		case ProxyStatusConfirmed, ProxyStatusCandidate, ProxyStatusInconsistent:
			positive = append(positive, index)
		case ProxyStatusUnknown:
			unknown = true
		}
	}
	if len(positive) == 0 {
		if unknown {
			resolved.Status = ProxyStatusUnknown
		}
		return resolved, nil
	}
	resolved.Primary = &resolved.Outcomes[positive[0]]
	resolved.Status = resolved.Primary.Status
	for _, index := range positive[1:] {
		other := resolved.Outcomes[index]
		if proxyDetectionsConflict(*resolved.Primary, other) {
			resolved.Conflicts = append(resolved.Conflicts,
				resolved.Primary.Detector+" conflicts with "+other.Detector,
			)
		}
	}
	if len(resolved.Conflicts) != 0 || resolved.Primary.Status == ProxyStatusInconsistent {
		resolved.Status = ProxyStatusInconsistent
	}
	return resolved, nil
}

func RunProxyDetectors(
	ctx context.Context,
	detectionContext *ProxyDetectionContext,
	detectors []ProxyDetector,
) (ProxyDetectionResolution, error) {
	if detectionContext == nil {
		return ProxyDetectionResolution{}, errors.New("proxy detection context is nil")
	}
	outcomes := make([]ProxyDetectionV2, 0, len(detectors))
	seen := make(map[string]struct{}, len(detectors))
	for _, detector := range detectors {
		if detector == nil || detector.ID() == "" || detector.Version() == "" {
			return ProxyDetectionResolution{}, errors.New("proxy detector registration is invalid")
		}
		identity := detector.ID() + "@" + detector.Version()
		if _, exists := seen[identity]; exists {
			return ProxyDetectionResolution{}, errors.New("proxy detector registration is duplicated")
		}
		seen[identity] = struct{}{}
		if !slices.Contains(detector.SupportedModes(), detectionContext.Mode()) {
			continue
		}
		outcome, err := detector.Detect(ctx, detectionContext)
		if err != nil {
			outcomes = append(outcomes, ProxyDetectionV2{
				Detector: detector.ID(), DetectorVersion: detector.Version(), Priority: detector.Priority(),
				Status: ProxyStatusUnknown, Confidence: ProxyConfidenceLow,
				Proxy: detectionContext.Address(), ChainID: detectionContext.ChainID(),
				BlockNumber: detectionContext.BlockNumber(), BlockHash: detectionContext.BlockHash(),
				Warnings: []string{"detector could not obtain required fixed-block state"},
			})
			continue
		}
		if outcome == nil {
			continue
		}
		outcome.Detector = detector.ID()
		outcome.DetectorVersion = detector.Version()
		outcome.Priority = detector.Priority()
		outcome.Proxy = detectionContext.Address()
		outcome.ChainID = detectionContext.ChainID()
		outcome.BlockNumber = detectionContext.BlockNumber()
		outcome.BlockHash = detectionContext.BlockHash()
		outcomes = append(outcomes, *outcome)
	}
	return ResolveProxyDetections(outcomes)
}

func compareProxyDetections(left, right ProxyDetectionV2) int {
	if difference := proxyStatusRank(right.Status) - proxyStatusRank(left.Status); difference != 0 {
		return difference
	}
	if difference := proxyConfidenceRank(right.Confidence) - proxyConfidenceRank(left.Confidence); difference != 0 {
		return difference
	}
	if left.Priority != right.Priority {
		return right.Priority - left.Priority
	}
	if left.Detector != right.Detector {
		return bytes.Compare([]byte(left.Detector), []byte(right.Detector))
	}
	return bytes.Compare([]byte(left.DetectorVersion), []byte(right.DetectorVersion))
}

func proxyStatusRank(status ProxyDetectionStatus) int {
	switch status {
	case ProxyStatusInconsistent:
		return 5
	case ProxyStatusConfirmed:
		return 4
	case ProxyStatusCandidate:
		return 3
	case ProxyStatusUnknown:
		return 2
	case ProxyStatusNotDetected:
		return 1
	default:
		return 0
	}
}

func proxyConfidenceRank(confidence ProxyDetectionConfidence) int {
	switch confidence {
	case ProxyConfidenceHigh:
		return 3
	case ProxyConfidenceMedium:
		return 2
	case ProxyConfidenceLow:
		return 1
	default:
		return 0
	}
}

func proxyDetectionsConflict(left, right ProxyDetectionV2) bool {
	if left.Family != right.Family {
		return true
	}
	if left.Implementation != nil && right.Implementation != nil && *left.Implementation != *right.Implementation {
		return true
	}
	if left.Beacon != nil && right.Beacon != nil && *left.Beacon != *right.Beacon {
		return true
	}
	if left.Admin != nil && right.Admin != nil && *left.Admin != *right.Admin {
		return true
	}
	return false
}

func proxyDetectionCacheKeyString(key ProxyDetectionCacheKey) string {
	return key.ChainID + ":" + key.Address.Hex() + ":" + strconv.FormatUint(key.BlockNumber, 10) +
		":" + key.BlockHash.Hex() + ":" + key.DetectorVersion
}
