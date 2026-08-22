package enrich

import (
	"bytes"
	"context"
	"encoding/binary"

	"github.com/ethereum/go-ethereum/common"
)

const (
	soladyLegacyCWIADetectorVersion = "1.0.0+solady.0.1.26"
	soladyLegacyCWIAVariant         = "solady-legacy-libcwia"
	soladyLegacyCWIARuntimePrefix   = 98
	soladyLegacyCWIALengthOffset    = 54
	soladyLegacyCWIAAddressOffset   = 65
	soladyLegacyCWIAMinimumRuntime  = soladyLegacyCWIARuntimePrefix + 2
)

var (
	soladyLegacyCWIABeforeLength = mustHex(
		"36602c57343d527f9e4ac34f21c619cefc926c8bd93b54bf5a39c7ab2127a895af1cc0691d7e3dff593da1005b363d3d373d3d3d3d61",
	)
	soladyLegacyCWIABeforeAddress = mustHex("806062363936013d73")
	soladyLegacyCWIAAfterAddress  = mustHex("5af43d3d93803e606057fd5bf3")
)

// SoladyLegacyCWIA is the exact deployed legacy LibCWIA shell payload. The
// two-byte footer is structural metadata and is not part of ImmutableArgs.
type SoladyLegacyCWIA struct {
	Implementation        common.Address
	ImmutableArgs         []byte
	InvalidLength         bool
	ImmutableArgsTooLarge bool
}

// DetectSoladyLegacyCWIA recognizes only the byte-for-byte legacy Solady
// LibCWIA runtime. A true match with InvalidLength or ImmutableArgsTooLarge is
// deliberately distinguishable from unrelated bytecode.
func DetectSoladyLegacyCWIA(runtime []byte) (SoladyLegacyCWIA, bool) {
	if len(runtime) < soladyLegacyCWIAMinimumRuntime ||
		!bytes.Equal(runtime[:soladyLegacyCWIALengthOffset], soladyLegacyCWIABeforeLength) ||
		!bytes.Equal(runtime[soladyLegacyCWIALengthOffset+2:soladyLegacyCWIAAddressOffset], soladyLegacyCWIABeforeAddress) ||
		!bytes.Equal(
			runtime[soladyLegacyCWIAAddressOffset+common.AddressLength:soladyLegacyCWIARuntimePrefix],
			soladyLegacyCWIAAfterAddress,
		) {
		return SoladyLegacyCWIA{}, false
	}
	result := SoladyLegacyCWIA{Implementation: common.BytesToAddress(
		runtime[soladyLegacyCWIAAddressOffset : soladyLegacyCWIAAddressOffset+common.AddressLength],
	)}
	embedded := int(binary.BigEndian.Uint16(
		runtime[soladyLegacyCWIALengthOffset : soladyLegacyCWIALengthOffset+2],
	))
	footer := int(binary.BigEndian.Uint16(runtime[len(runtime)-2:]))
	expected := len(runtime) - soladyLegacyCWIARuntimePrefix
	if embedded < 2 || embedded != footer || embedded != expected {
		result.InvalidLength = true
		return result, true
	}
	argumentBytes := expected - 2
	if argumentBytes > MaxCloneImmutableArgs {
		result.ImmutableArgsTooLarge = true
		return result, true
	}
	result.ImmutableArgs = common.CopyBytes(runtime[soladyLegacyCWIARuntimePrefix : len(runtime)-2])
	return result, true
}

type proxyCodeReader func(context.Context, common.Address) ([]byte, error)

func resolveSoladyLegacyCWIA(
	ctx context.Context,
	candidate proxyCandidate,
	runtime []byte,
	readCode proxyCodeReader,
) (proxyDetection, bool, error) {
	parsed, matched := DetectSoladyLegacyCWIA(runtime)
	if !matched {
		return proxyDetection{}, false, nil
	}
	detection := proxyDetection{candidate: candidate, code: common.CopyBytes(runtime), codeHash: codeHash(runtime)}
	switch {
	case parsed.InvalidLength:
		detection.rejected = "cwia_invalid_length"
	case parsed.ImmutableArgsTooLarge:
		detection.rejected = "cwia_immutable_args_too_large"
	case parsed.Implementation == (common.Address{}):
		detection.rejected = "cwia_zero_implementation"
	case parsed.Implementation == candidate.address:
		detection.rejected = "cwia_self_implementation"
	}
	if detection.rejected != "" {
		return detection, true, nil
	}
	implementationCode, err := readCode(ctx, parsed.Implementation)
	if err != nil {
		return detection, true, err
	}
	detection.proxy = &proxyResolution{
		kind: ProxyCWIA, pattern: ProxyPatternClone, evidenceState: "exact",
		implementation:     parsed.Implementation,
		implementationCode: common.CopyBytes(implementationCode),
		implementationHash: codeHash(implementationCode),
		immutableArgsExact: true,
		immutableArgs:      common.CopyBytes(parsed.ImmutableArgs),
	}
	return detection, true, nil
}

type soladyLegacyCWIAProxyDetector struct {
	candidate    proxyCandidate
	legacyResult *proxyDetection
	err          error
}

func (*soladyLegacyCWIAProxyDetector) ID() string { return "solady-cwia" }
func (*soladyLegacyCWIAProxyDetector) Version() string {
	return soladyLegacyCWIADetectorVersion
}
func (*soladyLegacyCWIAProxyDetector) Priority() int { return 175 }
func (*soladyLegacyCWIAProxyDetector) SupportedModes() []ProxyDetectionMode {
	return []ProxyDetectionMode{ProxyDetectionBulk, ProxyDetectionDeep}
}

func (detector *soladyLegacyCWIAProxyDetector) Detect(
	ctx context.Context,
	detectionContext *ProxyDetectionContext,
) (*ProxyDetectionV2, error) {
	runtime, err := detectionContext.GetCode(ctx, detectionContext.Address())
	if err != nil {
		detector.err = err
		return nil, err
	}
	legacy, matched, err := resolveSoladyLegacyCWIA(
		ctx,
		detector.candidate,
		runtime,
		detectionContext.GetCode,
	)
	if err != nil {
		detector.err = err
		return nil, err
	}
	if !matched {
		runtimeHash := codeHash(runtime)
		return &ProxyDetectionV2{
			Status: ProxyStatusNotDetected, Confidence: ProxyConfidenceLow,
			Evidence: []ProxyDetectionEvidence{{
				Kind: ProxyEvidenceRuntimeCodeHash, Description: "target runtime is not the Solady legacy LibCWIA shell",
				Address: addressCopy(detectionContext.Address()), Value: common.CopyBytes(runtimeHash[:]),
			}},
		}, nil
	}
	detector.legacyResult = &legacy
	return soladyLegacyCWIADetectionV2(legacy), nil
}

func soladyLegacyCWIADetectionV2(legacy proxyDetection) *ProxyDetectionV2 {
	result := &ProxyDetectionV2{
		Family: ProxyFamilyCWIA, Variant: soladyLegacyCWIAVariant,
		Status: ProxyStatusConfirmed, Confidence: ProxyConfidenceHigh,
		CanonicalProxyShell: true,
		Evidence: []ProxyDetectionEvidence{{
			Kind: ProxyEvidenceRuntimeBytecode, Description: "exact Solady legacy LibCWIA runtime shell",
			Address: addressCopy(legacy.candidate.address), Value: common.CopyBytes(legacy.codeHash[:]),
		}},
	}
	if legacy.rejected != "" {
		result.Warnings = []string{legacy.rejected}
		result.CanonicalProxyShell = false
		switch legacy.rejected {
		case "cwia_immutable_args_too_large":
			result.Status = ProxyStatusCandidate
			result.Confidence = ProxyConfidenceMedium
		default:
			result.Status = ProxyStatusInconsistent
		}
		return result
	}
	resolved := legacy.proxy
	if resolved == nil {
		result.Status = ProxyStatusUnknown
		result.Confidence = ProxyConfidenceLow
		result.CanonicalProxyShell = false
		return result
	}
	result.Implementation = addressCopy(resolved.implementation)
	result.ImplementationRole = ProxyRoleImplementation
	result.ImplementationPath = []common.Address{legacy.candidate.address, resolved.implementation}
	result.ImplementationHasCode = len(resolved.implementationCode) != 0
	implementationHash := resolved.implementationHash
	result.Evidence = append(result.Evidence, ProxyDetectionEvidence{
		Kind: ProxyEvidenceRuntimeCodeHash, Description: "fixed CWIA implementation code identity",
		Address: addressCopy(resolved.implementation), Value: common.CopyBytes(implementationHash[:]),
	})
	if !result.ImplementationHasCode {
		result.Status = ProxyStatusInconsistent
		result.Warnings = []string{"implementation_has_no_code"}
	}
	return result
}
