package enrich

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

type testProxyDetector struct {
	id       string
	version  string
	priority int
	modes    []ProxyDetectionMode
	result   *ProxyDetectionV2
	err      error
	call     func(context.Context, *ProxyDetectionContext) (*ProxyDetectionV2, error)
}

func (detector testProxyDetector) ID() string      { return detector.id }
func (detector testProxyDetector) Version() string { return detector.version }
func (detector testProxyDetector) Priority() int   { return detector.priority }
func (detector testProxyDetector) SupportedModes() []ProxyDetectionMode {
	return append([]ProxyDetectionMode(nil), detector.modes...)
}
func (detector testProxyDetector) Detect(
	ctx context.Context,
	detectionContext *ProxyDetectionContext,
) (*ProxyDetectionV2, error) {
	if detector.call != nil {
		return detector.call(ctx, detectionContext)
	}
	if detector.result == nil {
		return nil, detector.err
	}
	result := *detector.result
	return &result, detector.err
}

func TestProxyDetectionContextPinsAndMemoizesEveryRPC(t *testing.T) {
	t.Parallel()
	proxy, target := testAddress(1), testAddress(2)
	blockHash := uintWord(999)
	slot := uintWord(3)
	selector := []byte{0xde, 0xad, 0xbe, 0xef}
	caller := &proxyStateCaller{
		code: map[common.Address][]byte{target: {0x60, 0x01}},
		storage: map[string]common.Hash{
			target.String() + ":" + slot.String(): addressWord(proxy),
		},
		probeRaw: map[string][]byte{
			proxyProbeKey(target, selector): wordBytes(addressWord(proxy)),
		},
	}
	detectionContext, err := newProxyDetectionContext(
		"1", proxy, 999, blockHash, ProxyDetectionBulk, caller, 4096, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		code, codeErr := detectionContext.GetCode(t.Context(), target)
		if codeErr != nil || !reflect.DeepEqual(code, []byte{0x60, 0x01}) {
			t.Fatalf("code=%x err=%v", code, codeErr)
		}
		word, storageErr := detectionContext.GetStorageAt(t.Context(), target, slot)
		if storageErr != nil || word != addressWord(proxy) {
			t.Fatalf("storage=%s err=%v", word, storageErr)
		}
		result, callErr := detectionContext.Call(t.Context(), ProxyCallInput{To: target, Data: selector})
		if callErr != nil || !result.Success || !reflect.DeepEqual(result.Data, wordBytes(addressWord(proxy))) {
			t.Fatalf("call=%+v err=%v", result, callErr)
		}
	}
	if got, want := detectionContext.Counters(), (ProxyRPCCounters{GetCode: 1, GetStorageAt: 1, Call: 1}); got != want {
		t.Fatalf("counters=%+v want=%+v", got, want)
	}
	caller.mu.Lock()
	calls := append([]proxyRPCCall(nil), caller.calls...)
	caller.mu.Unlock()
	if len(calls) != 3 {
		t.Fatalf("RPC calls=%+v", calls)
	}
	for _, call := range calls {
		if call.blockHash != blockHash.Hex() {
			t.Fatalf("RPC call used mixed block: %+v", call)
		}
	}
	key := detectionContext.CacheKey()
	if key.ChainID != "1" || key.Address != proxy || key.BlockNumber != 999 ||
		key.BlockHash != blockHash || key.DetectorVersion != ProxyDetectorFrameworkVersion {
		t.Fatalf("cache key=%+v", key)
	}
	if proxyDetectionCacheKeyString(key) == "" {
		t.Fatal("cache key string is empty")
	}
}

func TestProxyDetectionContextDoesNotShareStateAcrossBlocks(t *testing.T) {
	t.Parallel()
	proxy := testAddress(3)
	caller := &proxyStateCaller{code: map[common.Address][]byte{proxy: {0x60}}}
	memo := &proxyDetectionRPCMemo{}
	first, err := newProxyDetectionContext("1", proxy, 10, uintWord(10), ProxyDetectionBulk, caller, 4096, memo)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newProxyDetectionContext("1", proxy, 11, uintWord(11), ProxyDetectionBulk, caller, 4096, memo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.GetCode(t.Context(), proxy); err != nil {
		t.Fatal(err)
	}
	if _, err := second.GetCode(t.Context(), proxy); err != nil {
		t.Fatal(err)
	}
	if memo.counts.GetCode != 2 {
		t.Fatalf("different blocks shared code cache: %+v", memo.counts)
	}
	caller.mu.Lock()
	calls := append([]proxyRPCCall(nil), caller.calls...)
	caller.mu.Unlock()
	if len(calls) != 2 || calls[0].blockHash == calls[1].blockHash {
		t.Fatalf("block-specific calls=%+v", calls)
	}
}

func TestProxyDetectionContextSharesRequestedAddressWithinOneBlock(t *testing.T) {
	t.Parallel()
	firstProxy, secondProxy, sharedImplementation := testAddress(31), testAddress(32), testAddress(33)
	caller := &proxyStateCaller{code: map[common.Address][]byte{sharedImplementation: {0x60}}}
	memo := &proxyDetectionRPCMemo{}
	first, err := newProxyDetectionContext(
		"1", firstProxy, 12, uintWord(12), ProxyDetectionBulk, caller, 4096, memo,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newProxyDetectionContext(
		"1", secondProxy, 12, uintWord(12), ProxyDetectionBulk, caller, 4096, memo,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.GetCode(t.Context(), sharedImplementation); err != nil {
		t.Fatal(err)
	}
	if _, err := second.GetCode(t.Context(), sharedImplementation); err != nil {
		t.Fatal(err)
	}
	if memo.counts.GetCode != 1 {
		t.Fatalf("same fixed-block target did not share code: %+v", memo.counts)
	}
}

func TestRunProxyDetectorsKeepsAllOutcomesAndResolvesOrderIndependently(t *testing.T) {
	t.Parallel()
	proxy, firstImplementation, secondImplementation := testAddress(4), testAddress(5), testAddress(6)
	caller := &proxyStateCaller{}
	detectionContext, err := newProxyDetectionContext(
		"1", proxy, 20, uintWord(20), ProxyDetectionBulk, caller, 4096, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	detectors := []ProxyDetector{
		testProxyDetector{
			id: "weaker", version: "1", priority: 1, modes: []ProxyDetectionMode{ProxyDetectionBulk},
			result: &ProxyDetectionV2{
				Family: ProxyFamilyERC1967, Status: ProxyStatusCandidate, Confidence: ProxyConfidenceMedium,
				Implementation: &firstImplementation,
			},
		},
		testProxyDetector{
			id: "exact", version: "1", priority: 10, modes: []ProxyDetectionMode{ProxyDetectionBulk},
			result: &ProxyDetectionV2{
				Family: ProxyFamilySafe, Status: ProxyStatusConfirmed, Confidence: ProxyConfidenceHigh,
				Implementation: &secondImplementation, ImplementationRole: ProxyRoleSingleton,
			},
		},
	}
	forward, err := RunProxyDetectors(t.Context(), detectionContext, detectors)
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := RunProxyDetectors(t.Context(), detectionContext, []ProxyDetector{detectors[1], detectors[0]})
	if err != nil {
		t.Fatal(err)
	}
	if forward.Status != ProxyStatusInconsistent || reverse.Status != ProxyStatusInconsistent ||
		forward.Primary == nil || reverse.Primary == nil ||
		forward.Primary.Detector != "exact" || reverse.Primary.Detector != "exact" ||
		len(forward.Outcomes) != 2 || len(reverse.Outcomes) != 2 ||
		!reflect.DeepEqual(forward.Conflicts, reverse.Conflicts) {
		t.Fatalf("forward=%+v reverse=%+v", forward, reverse)
	}
}

func TestRunProxyDetectorsDistinguishesNotApplicableAndUnknown(t *testing.T) {
	t.Parallel()
	proxy := testAddress(7)
	detectionContext, err := newProxyDetectionContext(
		"1", proxy, 30, uintWord(30), ProxyDetectionBulk, &proxyStateCaller{}, 4096, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	notDetected, err := RunProxyDetectors(t.Context(), detectionContext, []ProxyDetector{
		testProxyDetector{id: "not-applicable", version: "1", modes: []ProxyDetectionMode{ProxyDetectionBulk}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if notDetected.Status != ProxyStatusNotDetected || len(notDetected.Outcomes) != 0 {
		t.Fatalf("not-applicable resolution=%+v", notDetected)
	}
	unknown, err := RunProxyDetectors(t.Context(), detectionContext, []ProxyDetector{
		testProxyDetector{
			id: "unavailable", version: "1", modes: []ProxyDetectionMode{ProxyDetectionBulk},
			err: errors.New("upstream secret must not escape"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if unknown.Status != ProxyStatusUnknown || len(unknown.Outcomes) != 1 ||
		unknown.Outcomes[0].Status != ProxyStatusUnknown ||
		reflect.DeepEqual(unknown.Outcomes[0].Warnings, []string{"upstream secret must not escape"}) {
		t.Fatalf("unknown resolution=%+v", unknown)
	}
}

func TestProxyDetectionContextCachesExecutionRevertAsCallEvidence(t *testing.T) {
	t.Parallel()
	proxy := testAddress(8)
	caller := &proxyStateCaller{methodErrors: map[string]error{
		"eth_call": testRPCError{code: 3, message: "execution reverted"},
	}}
	detectionContext, err := newProxyDetectionContext(
		"1", proxy, 40, uintWord(40), ProxyDetectionDeep, caller, 4096, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		result, callErr := detectionContext.Call(t.Context(), ProxyCallInput{To: proxy, Data: []byte{1, 2, 3, 4}})
		if callErr != nil || result.Success || result.Error != "execution_reverted" || len(result.Data) != 0 {
			t.Fatalf("result=%+v err=%v", result, callErr)
		}
	}
	if detectionContext.Counters().Call != 1 {
		t.Fatalf("revert was not memoized: %+v", detectionContext.Counters())
	}
}

func TestOpenZeppelinAdapterPreservesLegacyAndAddsStructuredShadowResult(t *testing.T) {
	t.Parallel()
	proxy, implementation := testAddress(10), testAddress(11)
	runtime := append(append(append([]byte(nil), minimalProxyPrefix...), implementation[:]...), minimalProxySuffix...)
	caller := &proxyStateCaller{code: map[common.Address][]byte{proxy: runtime}}
	detector := rpcProxyDetector{
		caller:    caller,
		v2Enabled: true,
		limits:    ProxyLimits{MaxCandidates: 2, MaxCodeBytes: 4096, MaxDetailsBytes: 512},
	}
	job := Job{
		ID: "oz-adapter", Stage: ProxyStage, ChainID: "1",
		BlockNumber: 60, BlockHash: uintWord(60),
	}
	detections, err := detector.detectBlock(t.Context(), job, []proxyCandidate{{address: proxy, force: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(detections) != 1 || detections[0].proxy == nil || detections[0].rejected != "" ||
		detections[0].proxy.kind != ProxyMinimal1167 || !detections[0].proxy.minimalExact {
		t.Fatalf("legacy detection changed: %+v", detections)
	}
	shadow := detections[0].v2
	if shadow.Status != ProxyStatusInconsistent || shadow.Primary == nil ||
		shadow.Primary.Detector != "openzeppelin" || shadow.Primary.DetectorVersion != OpenZeppelin561Standard ||
		shadow.Primary.Family != ProxyFamilyERC1167 || shadow.Primary.Variant != string(ProxyPatternClone) ||
		shadow.Primary.Implementation == nil || *shadow.Primary.Implementation != implementation ||
		shadow.Primary.ImplementationRole != ProxyRoleImplementation ||
		!reflect.DeepEqual(shadow.Primary.Warnings, []string{"implementation_has_no_code"}) {
		t.Fatalf("shadow detection=%+v", shadow)
	}
	caller.mu.Lock()
	calls := append([]proxyRPCCall(nil), caller.calls...)
	caller.mu.Unlock()
	if len(calls) != 2 || calls[0].method != "eth_getCode" || calls[0].address != proxy.Hex() ||
		calls[1].method != "eth_getCode" || calls[1].address != implementation.Hex() {
		t.Fatalf("adapter added or reordered RPC calls: %+v", calls)
	}
	for _, call := range calls {
		if call.blockHash != job.BlockHash.Hex() {
			t.Fatalf("adapter mixed block selectors: %+v", calls)
		}
	}
}

func TestOpenZeppelinAdapterKeepsGenericSlotEvidenceAsCandidate(t *testing.T) {
	t.Parallel()
	proxy, implementation := testAddress(12), testAddress(13)
	caller := &proxyStateCaller{
		code: map[common.Address][]byte{proxy: {0x60, 0x01}, implementation: {0x60, 0x02}},
		storage: map[string]common.Hash{
			proxy.Hex() + ":" + EIP1967ImplementationSlot.Hex(): addressWord(implementation),
		},
	}
	detector := rpcProxyDetector{
		caller:    caller,
		v2Enabled: true,
		limits:    ProxyLimits{MaxCandidates: 2, MaxCodeBytes: 4096, MaxDetailsBytes: 512},
	}
	detections, err := detector.detectBlock(t.Context(), Job{
		ID: "oz-generic-adapter", Stage: ProxyStage, ChainID: "1",
		BlockNumber: 61, BlockHash: uintWord(61),
	}, []proxyCandidate{{address: proxy}})
	if err != nil {
		t.Fatal(err)
	}
	if len(detections) != 1 || detections[0].proxy == nil ||
		detections[0].proxy.kind != ProxyEIP1967 || detections[0].proxy.evidenceState != "generic" {
		t.Fatalf("legacy detection changed: %+v", detections)
	}
	shadow := detections[0].v2
	if shadow.Status != ProxyStatusCandidate || shadow.Primary == nil ||
		shadow.Primary.Family != ProxyFamilyERC1967 || shadow.Primary.Status != ProxyStatusCandidate ||
		shadow.Primary.Confidence != ProxyConfidenceLow || shadow.Primary.Implementation == nil ||
		*shadow.Primary.Implementation != implementation {
		t.Fatalf("shadow detection=%+v", shadow)
	}
}

func FuzzResolveProxyDetectionsIsRegistrationOrderIndependent(f *testing.F) {
	f.Add(uint8(0), uint8(1), true)
	f.Add(uint8(2), uint8(4), false)
	f.Fuzz(func(t *testing.T, leftStatus, rightStatus uint8, sameFamily bool) {
		statuses := []ProxyDetectionStatus{
			ProxyStatusConfirmed, ProxyStatusCandidate, ProxyStatusInconsistent,
			ProxyStatusNotDetected, ProxyStatusUnknown,
		}
		proxy := testAddress(9)
		leftFamily, rightFamily := ProxyFamilyERC1967, ProxyFamilySafe
		if sameFamily {
			rightFamily = leftFamily
		}
		left := ProxyDetectionV2{
			Detector: "left", DetectorVersion: "1", Family: leftFamily,
			Status: statuses[int(leftStatus)%len(statuses)], Confidence: ProxyConfidenceHigh,
			Proxy: proxy, ChainID: "1", BlockNumber: 50, BlockHash: uintWord(50),
		}
		right := ProxyDetectionV2{
			Detector: "right", DetectorVersion: "1", Family: rightFamily,
			Status: statuses[int(rightStatus)%len(statuses)], Confidence: ProxyConfidenceMedium,
			Proxy: proxy, ChainID: "1", BlockNumber: 50, BlockHash: uintWord(50),
		}
		forward, forwardErr := ResolveProxyDetections([]ProxyDetectionV2{left, right})
		reverse, reverseErr := ResolveProxyDetections([]ProxyDetectionV2{right, left})
		if (forwardErr != nil) != (reverseErr != nil) {
			t.Fatalf("errors differ: forward=%v reverse=%v", forwardErr, reverseErr)
		}
		if forwardErr != nil {
			return
		}
		if forward.Status != reverse.Status || !reflect.DeepEqual(forward.Outcomes, reverse.Outcomes) ||
			!reflect.DeepEqual(forward.Conflicts, reverse.Conflicts) {
			t.Fatalf("forward=%+v reverse=%+v", forward, reverse)
		}
	})
}

func TestProxyDetectionResolutionDocumentUsesPublicQuantitiesAndRoles(t *testing.T) {
	t.Parallel()
	proxy, singleton := testAddress(70), testAddress(71)
	resolution := ProxyDetectionResolution{
		Status: ProxyStatusConfirmed,
		Outcomes: []ProxyDetectionV2{{
			Detector: "safe", DetectorVersion: "safe-proxy@1", Priority: 200,
			Family: ProxyFamilySafe, Variant: "safe-proxy", Status: ProxyStatusConfirmed,
			Confidence: ProxyConfidenceHigh, Proxy: proxy, Implementation: &singleton,
			ImplementationRole: ProxyRoleSingleton, ImplementationPath: []common.Address{proxy, singleton},
			CanonicalProxyShell: true, ImplementationHasCode: true,
			Evidence: []ProxyDetectionEvidence{{
				Kind: ProxyEvidenceStorageSlot, Description: "slot 0 singleton", Address: &singleton,
			}},
			Warnings: []string{}, ChainID: "1", BlockNumber: 9007199254740993, BlockHash: uintWord(70),
		}},
		Conflicts: []string{},
	}
	resolution.Primary = &resolution.Outcomes[0]
	encoded, err := marshalProxyDetectionResolution(resolution)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, expected := range []string{
		`"block_number":"9007199254740993"`, `"chain_id":"1"`,
		`"implementation_role":"singleton"`, `"canonical_proxy_shell":true`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("document missing %s: %s", expected, text)
		}
	}
}

func TestDiamondDetectionDocumentKeepsSelectorScopedTargetsWithoutImplementation(t *testing.T) {
	t.Parallel()
	diamond, facet := testAddress(72), testAddress(73)
	immutableSelector := [4]byte{0x7a, 0x0e, 0xd6, 0x27}
	facetSelector := [4]byte{0xa9, 0x05, 0x9c, 0xbb}
	facetCodeHash := uintWord(73)
	implementationAddresses := []common.Address{facet}
	detection := ProxyDetectionV2{
		Detector: "erc2535", DetectorVersion: "erc2535@1", Priority: 150,
		Family: ProxyFamilyERC2535, Variant: "diamond", Status: ProxyStatusConfirmed,
		Confidence: ProxyConfidenceHigh, Proxy: diamond,
		Targets: []ProxyTarget{
			{Address: diamond, Role: ProxyTargetImmutable, Selectors: [][4]byte{immutableSelector}, CodeExists: true},
			{Address: facet, Role: ProxyTargetFacet, Selectors: [][4]byte{facetSelector}, CodeExists: true, CodeHash: &facetCodeHash},
		},
		Diamond: &DiamondDetection{
			Completeness: DiamondComplete, Validation: DiamondValidationFull,
			Facets: []ProxyTarget{
				{Address: diamond, Role: ProxyTargetImmutable, Selectors: [][4]byte{immutableSelector}, CodeExists: true},
				{Address: facet, Role: ProxyTargetFacet, Selectors: [][4]byte{facetSelector}, CodeExists: true, CodeHash: &facetCodeHash},
			},
			SelectorToFacet: map[[4]byte]common.Address{
				immutableSelector: diamond, facetSelector: facet,
			},
			ImplementationAddresses: implementationAddresses,
			StandardDiamondCut:      DiamondStandardCut{Status: DiamondCutAbsent},
		},
		Evidence: []ProxyDetectionEvidence{{
			Kind: ProxyEvidenceContractCall, Description: "Loupe results agree",
		}},
		Warnings: []string{}, ChainID: "1", BlockNumber: 99, BlockHash: uintWord(99),
	}
	if err := detection.validate(); err != nil {
		t.Fatal(err)
	}
	resolution := ProxyDetectionResolution{
		Status: ProxyStatusConfirmed, Primary: &detection,
		Outcomes: []ProxyDetectionV2{detection}, Conflicts: []string{},
	}
	encoded, err := marshalProxyDetectionResolution(resolution)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, expected := range []string{
		`"family":"erc2535"`, `"targets":[`, `"diamond":{`,
		`"0x7a0ed627":"` + diamond.Hex() + `"`,
		`"implementation_addresses":["` + facet.Hex() + `"]`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("Diamond document missing %s: %s", expected, text)
		}
	}
	if strings.Contains(text, `"implementation":"`) {
		t.Fatalf("Diamond document invented a singular implementation: %s", text)
	}
}

func TestDiamondDetectionRejectsSelectorAssignedToTwoFacets(t *testing.T) {
	t.Parallel()
	diamond, first, second := testAddress(74), testAddress(75), testAddress(76)
	selector := [4]byte{1, 2, 3, 4}
	detection := ProxyDetectionV2{
		Detector: "erc2535", DetectorVersion: "erc2535@1", Family: ProxyFamilyERC2535,
		Status: ProxyStatusInconsistent, Confidence: ProxyConfidenceHigh,
		Proxy: diamond, ChainID: "1", BlockNumber: 1, BlockHash: uintWord(1),
		Diamond: &DiamondDetection{
			Completeness: DiamondComplete, Validation: DiamondValidationFull,
			Facets: []ProxyTarget{
				{Address: first, Role: ProxyTargetFacet, Selectors: [][4]byte{selector}, CodeExists: true},
				{Address: second, Role: ProxyTargetFacet, Selectors: [][4]byte{selector}, CodeExists: true},
			},
			SelectorToFacet:         map[[4]byte]common.Address{selector: first},
			StandardDiamondCut:      DiamondStandardCut{Status: DiamondCutUnknown},
			ImplementationAddresses: []common.Address{first, second},
		},
	}
	if err := detection.validate(); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("duplicate Diamond selector validation error=%v", err)
	}
}

func TestProxyRPCCountersReportPerCandidateDelta(t *testing.T) {
	t.Parallel()
	before := ProxyRPCCounters{
		GetCode: 2, GetStorageAt: 3, Call: 4,
		GetCodeErrors: 1, GetStorageAtErrors: 1, CallErrors: 2,
	}
	after := ProxyRPCCounters{
		GetCode: 5, GetStorageAt: 4, Call: 6,
		GetCodeErrors: 1, GetStorageAtErrors: 2, CallErrors: 2,
	}
	want := ProxyRPCCounters{
		GetCode: 3, GetStorageAt: 1, Call: 2, GetStorageAtErrors: 1,
	}
	if got := after.since(before); got != want {
		t.Fatalf("counter delta=%+v want=%+v", got, want)
	}
}
