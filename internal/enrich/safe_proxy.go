package enrich

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

//go:generate go run ../../cmd/safe-manifest -input manifests/safe-proxy-sources.json -output manifests/safe-proxy-fingerprints.json

const safeProxyDetectorVersion = "1.0.0+manifest.1"

var (
	safeSingletonSlot      common.Hash
	safeMasterCopySelector = []byte{0xa6, 0x19, 0x48, 0x6e}
	safeProxyCreationTopic = SignatureHash("ProxyCreation(address,address)")

	//go:embed manifests/safe-proxy-fingerprints.json
	safeProxyManifestJSON []byte

	defaultSafeProxyManifest = mustLoadSafeProxyManifest(safeProxyManifestJSON)
)

type safeProxyManifest struct {
	SchemaVersion int                    `json:"schemaVersion"`
	Fingerprints  []safeProxyFingerprint `json:"fingerprints"`
	Deployments   []safeDeployment       `json:"deployments"`
	byRuntimeHash map[common.Hash]safeProxyFingerprint
}

type safeProxyFingerprint struct {
	Name             string `json:"name"`
	Variant          string `json:"variant"`
	Version          string `json:"version"`
	DeploymentType   string `json:"deploymentType"`
	RuntimeCodeHash  string `json:"runtimeCodeHash"`
	RuntimeCodeBytes int    `json:"runtimeCodeBytes"`
	SourceRepository string `json:"sourceRepository"`
	SourceTag        string `json:"sourceTag"`
	SourceArtifact   string `json:"sourceArtifact"`
	CompilerVersion  string `json:"compilerVersion,omitempty"`
	PackageIntegrity string `json:"packageIntegrity,omitempty"`
	runtimeHash      common.Hash
}

type safeDeployment struct {
	ChainID          string `json:"chainId"`
	Kind             string `json:"kind"`
	Name             string `json:"name"`
	Version          string `json:"version"`
	DeploymentType   string `json:"deploymentType"`
	Address          string `json:"address"`
	CodeHash         string `json:"codeHash"`
	ProxyIndexed     *bool  `json:"proxyIndexed,omitempty"`
	SourceRepository string `json:"sourceRepository"`
	SourceTag        string `json:"sourceTag"`
	SourceArtifact   string `json:"sourceArtifact"`
	parsedAddress    common.Address
	parsedCodeHash   common.Hash
}

func mustLoadSafeProxyManifest(encoded []byte) *safeProxyManifest {
	var manifest safeProxyManifest
	if err := json.Unmarshal(encoded, &manifest); err != nil {
		panic(fmt.Errorf("decode embedded Safe proxy manifest: %w", err))
	}
	if manifest.SchemaVersion != 1 || len(manifest.Fingerprints) == 0 {
		panic("embedded Safe proxy manifest has unsupported or empty schema")
	}
	manifest.byRuntimeHash = make(map[common.Hash]safeProxyFingerprint, len(manifest.Fingerprints))
	for index := range manifest.Fingerprints {
		fingerprint := &manifest.Fingerprints[index]
		hash, err := ParseWord(fingerprint.RuntimeCodeHash)
		if err != nil || fingerprint.Name == "" || fingerprint.Variant == "" ||
			fingerprint.Version == "" || fingerprint.DeploymentType == "" || fingerprint.RuntimeCodeBytes <= 0 {
			panic("embedded Safe proxy fingerprint is invalid")
		}
		if _, exists := manifest.byRuntimeHash[hash]; exists {
			panic("embedded Safe proxy manifest contains a duplicate runtime hash")
		}
		fingerprint.runtimeHash = hash
		manifest.byRuntimeHash[hash] = *fingerprint
	}
	for index := range manifest.Deployments {
		deployment := &manifest.Deployments[index]
		address, err := ParseAddress(deployment.Address)
		if err != nil {
			panic("embedded Safe deployment address is invalid")
		}
		hash, err := ParseWord(deployment.CodeHash)
		if err != nil || deployment.ChainID == "" ||
			(deployment.Kind != "singleton" && deployment.Kind != "factory") {
			panic("embedded Safe deployment is invalid")
		}
		deployment.parsedAddress, deployment.parsedCodeHash = address, hash
	}
	return &manifest
}

func (manifest *safeProxyManifest) runtimeFingerprint(hash common.Hash) (safeProxyFingerprint, bool) {
	if manifest == nil {
		return safeProxyFingerprint{}, false
	}
	fingerprint, ok := manifest.byRuntimeHash[hash]
	return fingerprint, ok
}

func (manifest *safeProxyManifest) singleton(
	chainID string,
	address common.Address,
	codeHash common.Hash,
) (safeDeployment, bool, bool) {
	addressKnown := false
	for _, deployment := range manifest.Deployments {
		if deployment.Kind != "singleton" || deployment.ChainID != chainID || deployment.parsedAddress != address {
			continue
		}
		addressKnown = true
		if deployment.parsedCodeHash == codeHash {
			return deployment, true, true
		}
	}
	return safeDeployment{}, false, addressKnown
}

func (manifest *safeProxyManifest) factory(chainID string, address common.Address) (safeDeployment, bool) {
	for _, deployment := range manifest.Deployments {
		if deployment.Kind == "factory" && deployment.ChainID == chainID && deployment.parsedAddress == address {
			return deployment, true
		}
	}
	return safeDeployment{}, false
}

type SafeFactoryProvenance struct {
	Factory          common.Address
	InitialSingleton common.Address
	DeploymentType   string
	Version          string
	EventLayout      string
}

type safeFactoryLookup func(
	context.Context,
	string,
	common.Address,
	uint64,
	common.Hash,
) (SafeFactoryProvenance, bool, error)

type safeProxyDetector struct {
	manifest      *safeProxyManifest
	factoryLookup safeFactoryLookup
}

func newSafeProxyDetector() *safeProxyDetector {
	return &safeProxyDetector{manifest: defaultSafeProxyManifest}
}

func (*safeProxyDetector) ID() string      { return "safe" }
func (*safeProxyDetector) Version() string { return safeProxyDetectorVersion }
func (*safeProxyDetector) Priority() int   { return 200 }
func (*safeProxyDetector) SupportedModes() []ProxyDetectionMode {
	return []ProxyDetectionMode{ProxyDetectionBulk, ProxyDetectionDeep}
}

func (detector *safeProxyDetector) Detect(
	ctx context.Context,
	detectionContext *ProxyDetectionContext,
) (*ProxyDetectionV2, error) {
	code, err := detectionContext.GetCode(ctx, detectionContext.Address())
	if err != nil {
		return nil, err
	}
	if len(code) == 0 {
		return nil, nil
	}
	manifest := detector.manifest
	if manifest == nil {
		manifest = defaultSafeProxyManifest
		detector.manifest = manifest
	}
	fingerprint, canonical := manifest.runtimeFingerprint(codeHash(code))
	if !canonical {
		if detectionContext.Mode() == ProxyDetectionBulk {
			return nil, nil
		}
		return detector.detectCompatible(ctx, detectionContext)
	}
	return detector.detectCanonical(ctx, detectionContext, fingerprint)
}

func (detector *safeProxyDetector) detectCanonical(
	ctx context.Context,
	detectionContext *ProxyDetectionContext,
	fingerprint safeProxyFingerprint,
) (*ProxyDetectionV2, error) {
	proxy := detectionContext.Address()
	result := &ProxyDetectionV2{
		Family: ProxyFamilySafe, Variant: fingerprint.Variant,
		Status: ProxyStatusConfirmed, Confidence: ProxyConfidenceHigh,
		CanonicalProxyShell: true,
		Evidence: []ProxyDetectionEvidence{{
			Kind:        ProxyEvidenceRuntimeCodeHash,
			Description: "runtime hash matches " + fingerprint.Name + " " + fingerprint.Version,
			Address:     addressCopy(proxy), Value: common.CopyBytes(fingerprint.runtimeHash[:]),
		}},
	}
	word, err := detectionContext.GetStorageAt(ctx, proxy, safeSingletonSlot)
	if err != nil {
		result.Status = ProxyStatusUnknown
		result.Warnings = append(result.Warnings, "slot 0 could not be read at the fixed block")
		return result, nil
	}
	result.Evidence = append(result.Evidence, ProxyDetectionEvidence{
		Kind: ProxyEvidenceStorageSlot, Description: "Safe singleton storage slot 0",
		Address: addressCopy(proxy), Slot: hashCopy(safeSingletonSlot), Value: common.CopyBytes(word[:]),
	})
	singleton, err := parseSafeAddressWord(word)
	if err != nil {
		result.Status = ProxyStatusInconsistent
		result.Warnings = append(result.Warnings, err.Error())
		return result, nil
	}
	result.Implementation = addressCopy(singleton)
	result.ImplementationRole = ProxyRoleSingleton
	result.ImplementationPath = []common.Address{proxy, singleton}
	code, err := detectionContext.GetCode(ctx, singleton)
	if err != nil {
		result.Status = ProxyStatusUnknown
		result.Warnings = append(result.Warnings, "singleton code could not be read at the fixed block")
		return result, nil
	}
	result.ImplementationHasCode = len(code) != 0
	if len(code) == 0 {
		result.Status = ProxyStatusInconsistent
		result.Warnings = append(result.Warnings, "singleton_has_no_code")
		return result, nil
	}
	if deployment, official, addressKnown := detector.manifest.singleton(
		detectionContext.ChainID(), singleton, codeHash(code),
	); official {
		result.OfficialSingleton = true
		result.SingletonVersion = deployment.Version
		result.SingletonDeploymentType = deployment.DeploymentType
		result.Evidence = append(result.Evidence, ProxyDetectionEvidence{
			Kind:        ProxyEvidenceDeploymentRecord,
			Description: "singleton address and code hash match the chain-aware Safe deployment manifest",
			Address:     addressCopy(singleton), Value: common.CopyBytes(deployment.parsedCodeHash[:]),
		})
	} else if addressKnown {
		result.Warnings = append(result.Warnings, "official singleton address has an unexpected code hash")
	}
	if detectionContext.Mode() == ProxyDetectionDeep {
		detector.validateMasterCopy(ctx, detectionContext, singleton, result)
		detector.addFactoryProvenance(ctx, detectionContext, singleton, result)
	}
	return result, nil
}

func (detector *safeProxyDetector) detectCompatible(
	ctx context.Context,
	detectionContext *ProxyDetectionContext,
) (*ProxyDetectionV2, error) {
	proxy := detectionContext.Address()
	word, err := detectionContext.GetStorageAt(ctx, proxy, safeSingletonSlot)
	if err != nil {
		return &ProxyDetectionV2{
			Status: ProxyStatusUnknown, Confidence: ProxyConfidenceLow,
			Warnings: []string{"slot 0 could not be read at the fixed block"},
		}, nil
	}
	singleton, err := parseSafeAddressWord(word)
	if err != nil {
		return nil, nil
	}
	evidence := []ProxyDetectionEvidence{{
		Kind: ProxyEvidenceStorageSlot, Description: "slot 0 contains a possible singleton address",
		Address: addressCopy(proxy), Slot: hashCopy(safeSingletonSlot), Value: common.CopyBytes(word[:]),
	}}
	call, err := detectionContext.Call(ctx, ProxyCallInput{To: proxy, Data: safeMasterCopySelector})
	if err != nil {
		return &ProxyDetectionV2{
			Status: ProxyStatusUnknown, Confidence: ProxyConfidenceLow, Evidence: evidence,
			Warnings: []string{"masterCopy() could not be called at the fixed block"},
		}, nil
	}
	if !call.Success {
		return nil, nil
	}
	calledSingleton, err := ParseSafeMasterCopy(call.Data)
	if err != nil || calledSingleton != singleton {
		return &ProxyDetectionV2{
			Status: ProxyStatusNotDetected, Confidence: ProxyConfidenceLow, Evidence: evidence,
			Warnings: []string{"masterCopy() does not strictly match slot 0"},
		}, nil
	}
	evidence = append(evidence, ProxyDetectionEvidence{
		Kind: ProxyEvidenceContractCall, Description: "masterCopy() strictly matches slot 0",
		Address: addressCopy(proxy), Value: common.CopyBytes(call.Data),
	})
	code, err := detectionContext.GetCode(ctx, singleton)
	if err != nil {
		return &ProxyDetectionV2{
			Status: ProxyStatusUnknown, Confidence: ProxyConfidenceLow, Evidence: evidence,
			Warnings: []string{"singleton code could not be read at the fixed block"},
		}, nil
	}
	if len(code) == 0 {
		return &ProxyDetectionV2{
			Status: ProxyStatusNotDetected, Confidence: ProxyConfidenceLow, Evidence: evidence,
			Warnings: []string{"masterCopy() and slot 0 target has no code"},
		}, nil
	}
	return &ProxyDetectionV2{
		Family: ProxyFamilySafe, Variant: "safe-compatible-proxy",
		Status: ProxyStatusCandidate, Confidence: ProxyConfidenceMedium,
		Implementation: addressCopy(singleton), ImplementationRole: ProxyRoleSingleton,
		ImplementationPath: []common.Address{proxy, singleton}, ImplementationHasCode: true,
		Evidence: evidence,
	}, nil
}

func (detector *safeProxyDetector) validateMasterCopy(
	ctx context.Context,
	detectionContext *ProxyDetectionContext,
	singleton common.Address,
	result *ProxyDetectionV2,
) {
	call, err := detectionContext.Call(ctx, ProxyCallInput{
		To: detectionContext.Address(), Data: safeMasterCopySelector,
	})
	if err != nil {
		result.Status = ProxyStatusUnknown
		result.Warnings = append(result.Warnings, "masterCopy() could not be called at the fixed block")
		return
	}
	if !call.Success {
		result.Status = ProxyStatusInconsistent
		result.Warnings = append(result.Warnings, "canonical Safe proxy masterCopy() reverted")
		return
	}
	calledSingleton, err := ParseSafeMasterCopy(call.Data)
	if err != nil {
		result.Status = ProxyStatusInconsistent
		result.Warnings = append(result.Warnings, "canonical Safe proxy masterCopy() returned malformed data")
		return
	}
	result.Evidence = append(result.Evidence, ProxyDetectionEvidence{
		Kind: ProxyEvidenceContractCall, Description: "masterCopy() singleton response",
		Address: addressCopy(detectionContext.Address()), Value: common.CopyBytes(call.Data),
	})
	if calledSingleton != singleton {
		result.Status = ProxyStatusInconsistent
		result.Warnings = append(result.Warnings, "masterCopy() conflicts with slot 0")
	}
}

func (detector *safeProxyDetector) addFactoryProvenance(
	ctx context.Context,
	detectionContext *ProxyDetectionContext,
	currentSingleton common.Address,
	result *ProxyDetectionV2,
) {
	if detector.factoryLookup == nil {
		return
	}
	provenance, found, err := detector.factoryLookup(
		ctx, detectionContext.ChainID(), detectionContext.Address(), detectionContext.BlockNumber(),
		detectionContext.BlockHash(),
	)
	if err != nil {
		result.Warnings = append(result.Warnings, "trusted factory provenance is unavailable")
		return
	}
	if !found {
		return
	}
	if _, trusted := detector.manifest.factory(detectionContext.ChainID(), provenance.Factory); !trusted {
		result.Warnings = append(result.Warnings, "factory provenance is not in the chain allowlist")
		return
	}
	result.InitialSingleton = addressCopy(provenance.InitialSingleton)
	result.SingletonChanged = provenance.InitialSingleton != currentSingleton
	result.Evidence = append(result.Evidence, ProxyDetectionEvidence{
		Kind: ProxyEvidenceFactoryLog, Description: "trusted Safe factory ProxyCreation event (" + provenance.EventLayout + ")",
		Address: addressCopy(provenance.Factory), Value: common.CopyBytes(provenance.InitialSingleton[:]),
	})
}

func parseSafeAddressWord(word common.Hash) (common.Address, error) {
	address, err := AddressFromWord(word)
	if err != nil {
		return common.Address{}, errors.New("slot 0 has non-zero high bytes")
	}
	if address == (common.Address{}) {
		return common.Address{}, errors.New("slot 0 contains the zero singleton")
	}
	return address, nil
}

func ParseSafeMasterCopy(data []byte) (common.Address, error) {
	if len(data) != common.HashLength {
		return common.Address{}, fmt.Errorf("masterCopy response is %d bytes; want 32", len(data))
	}
	word, _ := WordFromBytes(data)
	address, err := AddressFromWord(word)
	if err != nil {
		return common.Address{}, err
	}
	if address == (common.Address{}) {
		return common.Address{}, errors.New("masterCopy returned the zero address")
	}
	return address, nil
}

func ParseSafeProxyCreationLog(log types.Log) (
	proxy common.Address,
	singleton common.Address,
	layout string,
	err error,
) {
	if len(log.Topics) == 0 || log.Topics[0] != safeProxyCreationTopic {
		return common.Address{}, common.Address{}, "", errors.New("log is not ProxyCreation")
	}
	switch {
	case len(log.Topics) == 2 && len(log.Data) == common.HashLength:
		proxy, err = AddressFromWord(log.Topics[1])
		if err != nil {
			return common.Address{}, common.Address{}, "", errors.New("indexed proxy is malformed")
		}
		word, _ := WordFromBytes(log.Data)
		singleton, err = AddressFromWord(word)
		layout = "indexed-proxy"
	case len(log.Topics) == 1 && len(log.Data) == 2*common.HashLength:
		proxyWord, _ := WordFromBytes(log.Data[:common.HashLength])
		singletonWord, _ := WordFromBytes(log.Data[common.HashLength:])
		proxy, err = AddressFromWord(proxyWord)
		if err == nil {
			singleton, err = AddressFromWord(singletonWord)
		}
		layout = "unindexed-proxy"
	default:
		return common.Address{}, common.Address{}, "", errors.New("ProxyCreation log has a non-canonical ABI shape")
	}
	if err != nil || proxy == (common.Address{}) || singleton == (common.Address{}) {
		return common.Address{}, common.Address{}, "", errors.New("ProxyCreation log contains an invalid address")
	}
	return proxy, singleton, layout, nil
}

func hashCopy(hash common.Hash) *common.Hash {
	copy := hash
	return &copy
}
