package enrich

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestDiamondDetectorConfirmsCrossCheckedFacetsAndImmutableFunctions(t *testing.T) {
	t.Parallel()
	diamond, facet := testAddress(80), testAddress(81)
	facetSelector := [4]byte{0xa9, 0x05, 0x9c, 0xbb}
	rows := []diamondFacetRow{
		{FacetAddress: diamond, FunctionSelectors: append([][4]byte(nil), diamondRequiredLoupeSelectors[:]...)},
		{FacetAddress: facet, FunctionSelectors: [][4]byte{facetSelector}},
	}
	rows[0].FunctionSelectors = append(rows[0].FunctionSelectors, diamondCutSelector)
	caller := &proxyStateCaller{
		code:     map[common.Address][]byte{diamond: {0x60, 0x01}, facet: {0x60, 0x02}},
		probeRaw: diamondProbeFixture(t, diamond, rows, true, diamond),
	}
	detectionContext, err := newProxyDetectionContext(
		"1", diamond, 100, uintWord(100), ProxyDetectionBulk, caller,
		DiamondMaxRawReturnBytes, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := RunProxyDetectors(t.Context(), detectionContext, []ProxyDetector{
		newDiamondProxyDetector(proxyCandidate{address: diamond, sources: map[string]struct{}{proxySourceDiamondCut: {}}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != ProxyStatusConfirmed || resolved.Primary == nil ||
		resolved.Primary.Family != ProxyFamilyERC2535 || resolved.Primary.Implementation != nil ||
		resolved.Primary.Diamond == nil || resolved.Primary.Diamond.Completeness != DiamondComplete ||
		resolved.Primary.Diamond.Validation != DiamondValidationFull ||
		len(resolved.Primary.Diamond.Facets) != 2 ||
		!reflect.DeepEqual(resolved.Primary.Diamond.ImplementationAddresses, []common.Address{facet}) ||
		resolved.Primary.Diamond.StandardDiamondCut.Status != DiamondCutPresent ||
		resolved.Primary.Diamond.StandardDiamondCut.Facet == nil ||
		*resolved.Primary.Diamond.StandardDiamondCut.Facet != diamond ||
		resolved.Primary.Diamond.LoupeInterfaceReported == nil || !*resolved.Primary.Diamond.LoupeInterfaceReported {
		t.Fatalf("Diamond detection=%+v", resolved)
	}
	for _, call := range caller.calls {
		if call.blockHash != uintWord(100).Hex() {
			t.Fatalf("Diamond detector mixed block identity: %+v", caller.calls)
		}
	}
}

func TestDiamondDetectorFallsBackWhenFacetsReverts(t *testing.T) {
	t.Parallel()
	diamond, facet := testAddress(82), testAddress(83)
	selector := [4]byte{1, 2, 3, 4}
	externalLoupeSelectors := append([][4]byte(nil), diamondRequiredLoupeSelectors[:]...)
	externalLoupeSelectors = append(externalLoupeSelectors, selector)
	rows := []diamondFacetRow{{FacetAddress: facet, FunctionSelectors: externalLoupeSelectors}}
	probes := diamondProbeFixture(t, diamond, rows, false, common.Address{})
	delete(probes, diamondProbeKey(t, diamond, "facets"))
	caller := &proxyStateCaller{
		code: map[common.Address][]byte{diamond: {0x60}, facet: {0x61}}, probeRaw: probes,
	}
	detectionContext, err := newProxyDetectionContext(
		"1", diamond, 101, uintWord(101), ProxyDetectionDeep, caller,
		DiamondMaxRawReturnBytes, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := RunProxyDetectors(t.Context(), detectionContext, []ProxyDetector{
		newDiamondProxyDetector(proxyCandidate{address: diamond}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != ProxyStatusConfirmed || resolved.Primary == nil ||
		resolved.Primary.Diamond == nil ||
		resolved.Primary.Diamond.StandardDiamondCut.Status != DiamondCutAbsent ||
		!warningsContain(resolved.Primary.Warnings, "used facetAddresses()") {
		t.Fatalf("fallback Diamond detection=%+v", resolved)
	}
}

func TestDiamondDetectorDoesNotConfirmFacetAddressesAlone(t *testing.T) {
	t.Parallel()
	diamond, claimedFacet := testAddress(92), testAddress(93)
	caller := &proxyStateCaller{
		code: map[common.Address][]byte{diamond: {0x60}, claimedFacet: {0x61}},
		probeRaw: map[string][]byte{
			diamondProbeKey(t, diamond, "facetAddresses"): diamondPackOutput(
				t, "facetAddresses", []common.Address{claimedFacet},
			),
		},
	}
	detectionContext, err := newProxyDetectionContext(
		"1", diamond, 106, uintWord(106), ProxyDetectionDeep, caller,
		DiamondMaxRawReturnBytes, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := RunProxyDetectors(t.Context(), detectionContext, []ProxyDetector{
		newDiamondProxyDetector(proxyCandidate{address: diamond}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != ProxyStatusNotDetected || resolved.Primary != nil {
		t.Fatalf("facetAddresses-only detection=%+v", resolved)
	}
}

func TestDiamondCutCandidateAloneRemainsCandidate(t *testing.T) {
	t.Parallel()
	diamond := testAddress(94)
	caller := &proxyStateCaller{code: map[common.Address][]byte{diamond: {0x60}}}
	detectionContext, err := newProxyDetectionContext(
		"1", diamond, 107, uintWord(107), ProxyDetectionBulk, caller,
		DiamondMaxRawReturnBytes, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := RunProxyDetectors(t.Context(), detectionContext, []ProxyDetector{
		newDiamondProxyDetector(proxyCandidate{address: diamond, sources: map[string]struct{}{
			proxySourceDiamondCut: {},
		}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != ProxyStatusCandidate || resolved.Primary == nil ||
		resolved.Primary.Diamond == nil || resolved.Primary.Diamond.Completeness != DiamondUnknown {
		t.Fatalf("DiamondCut-only detection=%+v", resolved)
	}
}

func TestDelegatecallGateAloneDoesNotPublishDiamondCandidate(t *testing.T) {
	t.Parallel()
	diamond := testAddress(102)
	caller := &proxyStateCaller{code: map[common.Address][]byte{diamond: {0x60}}}
	detectionContext, err := newProxyDetectionContext(
		"1", diamond, 110, uintWord(110), ProxyDetectionBulk, caller,
		DiamondMaxRawReturnBytes, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := RunProxyDetectors(t.Context(), detectionContext, []ProxyDetector{
		newDiamondProxyDetector(proxyCandidate{address: diamond, sources: map[string]struct{}{
			proxySourceDelegatecallRouter: {},
		}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != ProxyStatusNotDetected || resolved.Primary != nil {
		t.Fatalf("delegatecall-only Diamond detection=%+v", resolved)
	}
}

func TestDiamondInterfaceProbePreservesRawReturnLimitWithoutCandidateEvidence(t *testing.T) {
	t.Parallel()
	diamond := testAddress(95)
	caller := &proxyStateCaller{code: map[common.Address][]byte{diamond: {0x60}}}
	detectionContext, err := newProxyDetectionContext(
		"1", diamond, 108, uintWord(108), ProxyDetectionDeep, caller,
		DiamondMaxRawReturnBytes, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	detector := newDiamondProxyDetector(proxyCandidate{address: diamond})
	result, err := detector.interfaceProbe(
		t.Context(), detectionContext, detector.baseResult(), diamondCallTooLarge,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Status != ProxyStatusCandidate || result.Diamond == nil ||
		result.Diamond.Completeness != DiamondPartial || !result.Diamond.Truncated ||
		result.Diamond.TruncationReason != "max-raw-return-bytes-exceeded" {
		t.Fatalf("return-limit result=%+v", result)
	}
}

func TestDiamondNormalizerRejectsDuplicateSelector(t *testing.T) {
	t.Parallel()
	selector := [4]byte{1, 2, 3, 4}
	_, err := normalizeDiamondFacetRows(testAddress(96), []diamondFacetRow{
		{FacetAddress: testAddress(97), FunctionSelectors: [][4]byte{selector}},
		{FacetAddress: testAddress(98), FunctionSelectors: [][4]byte{selector}},
	})
	if err == nil || !strings.Contains(err.Error(), "multiple facets") {
		t.Fatalf("duplicate selector error=%v", err)
	}
}

func TestDiamondDetectorRejectsSnapshotMissingRequiredLoupeSelector(t *testing.T) {
	t.Parallel()
	diamond, facet := testAddress(103), testAddress(104)
	selectors := append([][4]byte(nil), diamondRequiredLoupeSelectors[:len(diamondRequiredLoupeSelectors)-1]...)
	rows := []diamondFacetRow{{FacetAddress: facet, FunctionSelectors: selectors}}
	caller := &proxyStateCaller{
		code:     map[common.Address][]byte{diamond: {0x60}, facet: {0x61}},
		probeRaw: diamondProbeFixture(t, diamond, rows, false, common.Address{}),
	}
	detectionContext, err := newProxyDetectionContext(
		"1", diamond, 111, uintWord(111), ProxyDetectionDeep, caller,
		DiamondMaxRawReturnBytes, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := RunProxyDetectors(t.Context(), detectionContext, []ProxyDetector{
		newDiamondProxyDetector(proxyCandidate{address: diamond}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != ProxyStatusInconsistent || resolved.Primary == nil ||
		!warningsContain(resolved.Primary.Warnings, "required ERC-2535 Loupe selector") {
		t.Fatalf("missing-Loupe-selector detection=%+v", resolved)
	}
}

func TestDiamondDetectorUsesDeterministicSamplePastCrossCheckLimit(t *testing.T) {
	t.Parallel()
	diamond, facet := testAddress(99), testAddress(100)
	selectors := make([][4]byte, DiamondMaxCrossCheckCalls+32)
	for index := range selectors {
		selectors[index] = [4]byte{0xaa, byte(index >> 8), byte(index), 0x01}
	}
	selectors = append(append([][4]byte(nil), diamondRequiredLoupeSelectors[:]...), selectors...)
	rows := []diamondFacetRow{{FacetAddress: facet, FunctionSelectors: selectors}}
	caller := &proxyStateCaller{
		code:     map[common.Address][]byte{diamond: {0x60}, facet: {0x61}},
		probeRaw: diamondProbeFixture(t, diamond, rows, false, common.Address{}),
	}
	detectionContext, err := newProxyDetectionContext(
		"1", diamond, 109, uintWord(109), ProxyDetectionDeep, caller,
		DiamondMaxRawReturnBytes, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := RunProxyDetectors(t.Context(), detectionContext, []ProxyDetector{
		newDiamondProxyDetector(proxyCandidate{address: diamond}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != ProxyStatusConfirmed || resolved.Primary == nil ||
		resolved.Primary.Diamond == nil ||
		resolved.Primary.Diamond.Validation != DiamondValidationSampled ||
		resolved.Primary.Diamond.Completeness != DiamondComplete {
		t.Fatalf("sampled Diamond detection=%+v", resolved)
	}
}

func TestDiamondDetectorMarksContradictoryLoupeInconsistent(t *testing.T) {
	t.Parallel()
	diamond, first, second := testAddress(84), testAddress(85), testAddress(86)
	selector := [4]byte{4, 3, 2, 1}
	selectors := append([][4]byte(nil), diamondRequiredLoupeSelectors[:]...)
	selectors = append(selectors, selector)
	rows := []diamondFacetRow{{FacetAddress: first, FunctionSelectors: selectors}}
	probes := diamondProbeFixture(t, diamond, rows, false, common.Address{})
	probes[diamondProbeKey(t, diamond, "facetAddresses")] = diamondPackOutput(t, "facetAddresses", []common.Address{second})
	caller := &proxyStateCaller{
		code: map[common.Address][]byte{diamond: {0x60}, first: {0x61}, second: {0x62}}, probeRaw: probes,
	}
	detectionContext, err := newProxyDetectionContext(
		"1", diamond, 102, uintWord(102), ProxyDetectionBulk, caller,
		DiamondMaxRawReturnBytes, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := RunProxyDetectors(t.Context(), detectionContext, []ProxyDetector{
		newDiamondProxyDetector(proxyCandidate{address: diamond, sources: map[string]struct{}{proxySourceDiamondCut: {}}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != ProxyStatusInconsistent || resolved.Primary == nil ||
		!warningsContain(resolved.Primary.Warnings, "address set differs") {
		t.Fatalf("contradictory Diamond detection=%+v", resolved)
	}
}

func TestDiamondDetectorRejectsNonzeroRouteForUnknownSelector(t *testing.T) {
	t.Parallel()
	diamond, facet := testAddress(105), testAddress(106)
	rows := []diamondFacetRow{{
		FacetAddress:      facet,
		FunctionSelectors: append([][4]byte(nil), diamondRequiredLoupeSelectors[:]...),
	}}
	probes := diamondProbeFixture(t, diamond, rows, false, common.Address{})
	unknown := unknownDiamondSelector(diamondRowsToMap(rows))
	probes[diamondProbeKey(t, diamond, "facetAddress", unknown)] =
		diamondPackOutput(t, "facetAddress", facet)
	caller := &proxyStateCaller{
		code: map[common.Address][]byte{diamond: {0x60}, facet: {0x61}}, probeRaw: probes,
	}
	detectionContext, err := newProxyDetectionContext(
		"1", diamond, 112, uintWord(112), ProxyDetectionDeep, caller,
		DiamondMaxRawReturnBytes, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := RunProxyDetectors(t.Context(), detectionContext, []ProxyDetector{
		newDiamondProxyDetector(proxyCandidate{address: diamond}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != ProxyStatusInconsistent || resolved.Primary == nil ||
		!warningsContain(resolved.Primary.Warnings, "did not return zero") {
		t.Fatalf("unknown-selector detection=%+v", resolved)
	}
}

func TestDiamondDetectorRejectsFacetWithoutCode(t *testing.T) {
	t.Parallel()
	diamond, facet := testAddress(87), testAddress(88)
	selectors := append([][4]byte(nil), diamondRequiredLoupeSelectors[:]...)
	selectors = append(selectors, [4]byte{9, 8, 7, 6})
	rows := []diamondFacetRow{{FacetAddress: facet, FunctionSelectors: selectors}}
	caller := &proxyStateCaller{
		code:     map[common.Address][]byte{diamond: {0x60}},
		probeRaw: diamondProbeFixture(t, diamond, rows, false, common.Address{}),
	}
	detectionContext, err := newProxyDetectionContext(
		"1", diamond, 103, uintWord(103), ProxyDetectionBulk, caller,
		DiamondMaxRawReturnBytes, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := RunProxyDetectors(t.Context(), detectionContext, []ProxyDetector{
		newDiamondProxyDetector(proxyCandidate{address: diamond, sources: map[string]struct{}{proxySourceDiamondCut: {}}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != ProxyStatusInconsistent || resolved.Primary == nil ||
		!warningsContain(resolved.Primary.Warnings, "without runtime code") {
		t.Fatalf("no-code facet detection=%+v", resolved)
	}
}

func TestDiamondDetectorBulkMissAddsNoLoupeRPC(t *testing.T) {
	t.Parallel()
	diamond := testAddress(89)
	caller := &proxyStateCaller{code: map[common.Address][]byte{diamond: {0x60}}}
	detectionContext, err := newProxyDetectionContext(
		"1", diamond, 104, uintWord(104), ProxyDetectionBulk, caller,
		DiamondMaxRawReturnBytes, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := RunProxyDetectors(t.Context(), detectionContext, []ProxyDetector{
		newDiamondProxyDetector(proxyCandidate{address: diamond}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != ProxyStatusNotDetected || len(caller.calls) != 0 {
		t.Fatalf("bulk Diamond miss=%+v calls=%+v", resolved, caller.calls)
	}
}

func TestDiamondDetectorTransportFailureIsUnknown(t *testing.T) {
	t.Parallel()
	diamond := testAddress(90)
	caller := &proxyStateCaller{
		code:         map[common.Address][]byte{diamond: {0x60}},
		methodErrors: map[string]error{"eth_call": testRPCError{code: -32000, message: "historical state unavailable"}},
	}
	detectionContext, err := newProxyDetectionContext(
		"1", diamond, 105, uintWord(105), ProxyDetectionBulk, caller,
		DiamondMaxRawReturnBytes, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := RunProxyDetectors(t.Context(), detectionContext, []ProxyDetector{
		newDiamondProxyDetector(proxyCandidate{address: diamond, sources: map[string]struct{}{proxySourceDiamondCut: {}}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != ProxyStatusUnknown || len(resolved.Outcomes) != 1 ||
		resolved.Outcomes[0].Status != ProxyStatusUnknown {
		t.Fatalf("transport failure detection=%+v", resolved)
	}
}

func TestDiamondNormalizerReturnsPartialLimitWithoutSilentTruncation(t *testing.T) {
	t.Parallel()
	rows := make([]diamondFacetRow, DiamondMaxFacets+1)
	for index := range rows {
		rows[index] = diamondFacetRow{
			FacetAddress:      testAddress(byte(index%250 + 1)),
			FunctionSelectors: [][4]byte{{byte(index >> 8), byte(index), 0, 1}},
		}
	}
	_, err := normalizeDiamondFacetRows(testAddress(254), rows)
	if !errorsIsDiamondLimit(err) || diamondLimitReason(err) != "max-facets-exceeded" {
		t.Fatalf("facet limit error=%v reason=%s", err, diamondLimitReason(err))
	}
}

func TestERC1967AndDiamondLayersDoNotConflictSolelyByFamily(t *testing.T) {
	t.Parallel()
	proxy := testAddress(91)
	diamondTargets := []ProxyTarget{{
		Address: proxy, Role: ProxyTargetImmutable,
		Selectors: [][4]byte{{1, 2, 3, 4}}, CodeExists: true,
	}}
	left := ProxyDetectionV2{
		Detector: "openzeppelin", DetectorVersion: "1", Family: ProxyFamilyERC1967,
		Status: ProxyStatusConfirmed, Confidence: ProxyConfidenceHigh,
		Proxy: proxy, ChainID: "1", BlockHash: uintWord(1),
	}
	right := ProxyDetectionV2{
		Detector: "erc2535", DetectorVersion: "1", Family: ProxyFamilyERC2535,
		Status: ProxyStatusConfirmed, Confidence: ProxyConfidenceHigh,
		Proxy: proxy, ChainID: "1", BlockHash: uintWord(1),
		Targets: diamondTargets,
		Diamond: &DiamondDetection{
			Completeness: DiamondComplete, Validation: DiamondValidationFull,
			Facets:                  diamondTargets,
			SelectorToFacet:         map[[4]byte]common.Address{{1, 2, 3, 4}: proxy},
			ImplementationAddresses: []common.Address{},
			StandardDiamondCut:      DiamondStandardCut{Status: DiamondCutAbsent},
		},
	}
	resolution, err := ResolveProxyDetections([]ProxyDetectionV2{left, right})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolution.Conflicts) != 0 {
		t.Fatalf("composed proxy layers conflict=%+v", resolution)
	}
}

func diamondProbeFixture(
	t *testing.T,
	diamond common.Address,
	rows []diamondFacetRow,
	supportsInterface bool,
	diamondCutFacet common.Address,
) map[string][]byte {
	t.Helper()
	probes := map[string][]byte{
		diamondProbeKey(t, diamond, "facets"):         diamondPackOutput(t, "facets", rows),
		diamondProbeKey(t, diamond, "facetAddresses"): diamondPackOutput(t, "facetAddresses", diamondRowAddresses(rows)),
	}
	for _, row := range rows {
		probes[diamondProbeKey(t, diamond, "facetFunctionSelectors", row.FacetAddress)] =
			diamondPackOutput(t, "facetFunctionSelectors", row.FunctionSelectors)
		for _, selector := range row.FunctionSelectors {
			probes[diamondProbeKey(t, diamond, "facetAddress", selector)] =
				diamondPackOutput(t, "facetAddress", row.FacetAddress)
		}
	}
	unknown := unknownDiamondSelector(diamondRowsToMap(rows))
	probes[diamondProbeKey(t, diamond, "facetAddress", unknown)] = diamondPackOutput(t, "facetAddress", common.Address{})
	probes[diamondProbeKey(t, diamond, "facetAddress", diamondCutSelector)] = diamondPackOutput(t, "facetAddress", diamondCutFacet)
	probes[diamondProbeKey(t, diamond, "supportsInterface", diamondLoupeInterfaceID)] = diamondPackOutput(t, "supportsInterface", supportsInterface)
	return probes
}

func diamondProbeKey(t *testing.T, address common.Address, method string, arguments ...any) string {
	t.Helper()
	data, err := diamondLoupeABI.Pack(method, arguments...)
	if err != nil {
		t.Fatal(err)
	}
	return proxyProbeKey(address, data)
}

func diamondPackOutput(t *testing.T, method string, value any) []byte {
	t.Helper()
	data, err := diamondLoupeABI.Methods[method].Outputs.Pack(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func diamondRowAddresses(rows []diamondFacetRow) []common.Address {
	result := make([]common.Address, len(rows))
	for index := range rows {
		result[index] = rows[index].FacetAddress
	}
	return result
}

func diamondRowsToMap(rows []diamondFacetRow) map[[4]byte]common.Address {
	result := make(map[[4]byte]common.Address)
	for _, row := range rows {
		for _, selector := range row.FunctionSelectors {
			result[selector] = row.FacetAddress
		}
	}
	return result
}

func warningsContain(warnings []string, fragment string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, fragment) {
			return true
		}
	}
	return false
}

func errorsIsDiamondLimit(err error) bool {
	return err != nil && strings.Contains(err.Error(), "configured limit")
}

func FuzzDiamondLoupeOutputNeverPanics(f *testing.F) {
	valid, err := diamondLoupeABI.Methods["facets"].Outputs.Pack([]diamondFacetRow{{
		FacetAddress: testAddress(101), FunctionSelectors: [][4]byte{{1, 2, 3, 4}},
	}})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add([]byte{})
	f.Add([]byte{0, 1, 2, 3})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = unpackDiamondOutput[[]diamondFacetRow]("facets", data)
	})
}
