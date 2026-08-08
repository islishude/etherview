package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"reflect"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
)

type safeSourceFixture struct {
	Fingerprints []struct {
		Version         string `json:"version"`
		RuntimeBytecode string `json:"runtimeBytecode"`
	} `json:"fingerprints"`
}

type safeMainnetFixture struct {
	Name              string `json:"name"`
	ChainID           string `json:"chainId"`
	Address           string `json:"address"`
	BlockNumber       string `json:"blockNumber"`
	BlockHash         string `json:"blockHash"`
	RuntimeCodeHash   string `json:"runtimeCodeHash"`
	Singleton         string `json:"singleton"`
	SingletonCodeHash string `json:"singletonCodeHash"`
	Family            string `json:"family"`
	Variant           string `json:"variant"`
	Source            string `json:"source"`
}

func TestGeneratedSafeManifestMatchesOfficialArtifactsAndFixedMainnetFixtures(t *testing.T) {
	t.Parallel()
	if defaultSafeProxyManifest.SchemaVersion != 1 || len(defaultSafeProxyManifest.Fingerprints) != 2 {
		t.Fatalf("manifest=%+v", defaultSafeProxyManifest)
	}
	for hash, want := range map[string]string{
		"1.3.0": "0xb89c1b3bdf2cf8827818646bce9a8f6e372885f8c55e5c07acbd307cb133b000",
		"1.4.1": "0xd7d408ebcd99b2b70be43e20253d6d92a8ea8fab29bd3be7f55b10032331fb4c",
	} {
		fingerprintHash := common.HexToHash(want)
		fingerprint, ok := defaultSafeProxyManifest.runtimeFingerprint(fingerprintHash)
		if !ok || fingerprint.Version != hash || fingerprint.RuntimeCodeBytes != 171 ||
			fingerprint.SourceTag != "v"+hash || fingerprint.SourceArtifact == "" ||
			fingerprint.PackageIntegrity == "" {
			t.Fatalf("fingerprint %s=%+v found=%v", hash, fingerprint, ok)
		}
	}
	encoded, err := os.ReadFile("testdata/safe-mainnet-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []safeMainnetFixture
	if err := json.Unmarshal(encoded, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 2 {
		t.Fatalf("fixtures=%+v", fixtures)
	}
	for _, fixture := range fixtures {
		proxy, err := ParseAddress(fixture.Address)
		if err != nil || proxy == (common.Address{}) || fixture.BlockNumber != "25711126" ||
			fixture.BlockHash == "" || fixture.Family != string(ProxyFamilySafe) || fixture.Source == "" {
			t.Fatalf("invalid fixed fixture=%+v err=%v", fixture, err)
		}
		fingerprint, found := defaultSafeProxyManifest.runtimeFingerprint(common.HexToHash(fixture.RuntimeCodeHash))
		if !found || fingerprint.Variant != fixture.Variant {
			t.Fatalf("fixture fingerprint=%+v found=%v fixture=%+v", fingerprint, found, fixture)
		}
		singleton := common.HexToAddress(fixture.Singleton)
		deployment, official, addressKnown := defaultSafeProxyManifest.singleton(
			fixture.ChainID, singleton, common.HexToHash(fixture.SingletonCodeHash),
		)
		if !official || !addressKnown || deployment.parsedAddress != singleton {
			t.Fatalf("fixture singleton=%+v official=%v known=%v", deployment, official, addressKnown)
		}
	}
}

func TestSafeDetectorBulkRecognizesCanonicalShellsWithoutMasterCopyCall(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"1.3.0", "1.4.1"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			proxy, singleton := testAddress(100), testAddress(101)
			runtime := safeRuntimeFixture(t, version)
			caller := &proxyStateCaller{
				code: map[common.Address][]byte{proxy: runtime, singleton: {0x60, 0x01}},
				storage: map[string]common.Hash{
					proxy.Hex() + ":" + safeSingletonSlot.Hex(): addressWord(singleton),
				},
			}
			detectionContext, err := newProxyDetectionContext(
				"1", proxy, 1000, uintWord(1000), ProxyDetectionBulk, caller, 4096, nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := RunProxyDetectors(t.Context(), detectionContext, []ProxyDetector{newSafeProxyDetector()})
			if err != nil {
				t.Fatal(err)
			}
			if resolved.Status != ProxyStatusConfirmed || resolved.Primary == nil ||
				resolved.Primary.Family != ProxyFamilySafe || !resolved.Primary.CanonicalProxyShell ||
				resolved.Primary.Implementation == nil || *resolved.Primary.Implementation != singleton ||
				resolved.Primary.ImplementationRole != ProxyRoleSingleton ||
				!resolved.Primary.ImplementationHasCode || resolved.Primary.OfficialSingleton {
				t.Fatalf("resolved=%+v", resolved)
			}
			if got, want := detectionContext.Counters(), (ProxyRPCCounters{GetCode: 2, GetStorageAt: 1}); got != want {
				t.Fatalf("bulk RPC counts=%+v want=%+v", got, want)
			}
		})
	}
}

func TestProxyBlockDetectorResolvesSafeWithoutChangingLegacyProjection(t *testing.T) {
	t.Parallel()
	proxy, singleton := testAddress(119), testAddress(120)
	caller := &proxyStateCaller{
		code: map[common.Address][]byte{
			proxy: safeRuntimeFixture(t, "1.4.1"), singleton: {0x60, 0x05},
		},
		storage: map[string]common.Hash{
			proxy.Hex() + ":" + safeSingletonSlot.Hex(): addressWord(singleton),
		},
	}
	detector := rpcProxyDetector{
		caller:    caller,
		v2Enabled: true, safeEnabled: true,
		limits: ProxyLimits{MaxCandidates: 2, MaxCodeBytes: 4096, MaxDetailsBytes: 512},
	}
	job := Job{
		ID: "safe-shadow", Stage: ProxyStage, ChainID: "1",
		BlockNumber: 1007, BlockHash: uintWord(1007),
	}
	detections, err := detector.detectBlock(t.Context(), job, []proxyCandidate{{address: proxy, force: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(detections) != 1 || detections[0].proxy != nil || detections[0].exact != nil || detections[0].rejected != "" {
		t.Fatalf("legacy Safe projection changed: %+v", detections)
	}
	if detections[0].v2.Status != ProxyStatusConfirmed || detections[0].v2.Primary == nil ||
		detections[0].v2.Primary.Detector != "safe" || detections[0].v2.Primary.Family != ProxyFamilySafe ||
		detections[0].v2.Primary.Implementation == nil || *detections[0].v2.Primary.Implementation != singleton {
		t.Fatalf("Safe shadow result=%+v", detections[0].v2)
	}
	if !detections[0].v2.LegacyProjectionChanged ||
		!reflect.DeepEqual(detections[0].v2.LegacyDiffReasons, []string{"v2_positive_legacy_not_detected"}) {
		t.Fatalf("Safe shadow diff=%+v", detections[0].v2)
	}
	caller.mu.Lock()
	calls := append([]proxyRPCCall(nil), caller.calls...)
	caller.mu.Unlock()
	for _, call := range calls {
		if call.blockHash != job.BlockHash.Hex() {
			t.Fatalf("Safe detection mixed block selectors: %+v", calls)
		}
	}
}

func TestSafeDetectorBulkFingerprintMissAddsNoRPCToCachedRuntime(t *testing.T) {
	t.Parallel()
	proxy := testAddress(102)
	caller := &proxyStateCaller{code: map[common.Address][]byte{proxy: {0x60, 0x01}}}
	detectionContext, err := newProxyDetectionContext(
		"1", proxy, 1001, uintWord(1001), ProxyDetectionBulk, caller, 4096, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := detectionContext.GetCode(t.Context(), proxy); err != nil {
		t.Fatal(err)
	}
	before := detectionContext.Counters()
	resolved, err := RunProxyDetectors(t.Context(), detectionContext, []ProxyDetector{newSafeProxyDetector()})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != ProxyStatusNotDetected || len(resolved.Outcomes) != 0 {
		t.Fatalf("resolved=%+v", resolved)
	}
	if after := detectionContext.Counters(); after != before {
		t.Fatalf("fingerprint miss added RPC: before=%+v after=%+v", before, after)
	}
}

func TestSafeDetectorSeparatesCanonicalShellAndOfficialSingleton(t *testing.T) {
	t.Parallel()
	proxy, singleton := testAddress(103), testAddress(104)
	runtime, singletonCode := safeRuntimeFixture(t, "1.4.1"), []byte{0x60, 0x02}
	manifest := cloneSafeManifest(defaultSafeProxyManifest)
	manifest.Deployments = append(manifest.Deployments, safeDeployment{
		ChainID: "999", Kind: "singleton", Name: "TestSafe", Version: "test",
		DeploymentType: "fixture", Address: singleton.Hex(), CodeHash: codeHash(singletonCode).Hex(),
		parsedAddress: singleton, parsedCodeHash: codeHash(singletonCode),
	})
	caller := &proxyStateCaller{
		code: map[common.Address][]byte{proxy: runtime, singleton: singletonCode},
		storage: map[string]common.Hash{
			proxy.Hex() + ":" + safeSingletonSlot.Hex(): addressWord(singleton),
		},
	}
	detectionContext, err := newProxyDetectionContext(
		"999", proxy, 1002, uintWord(1002), ProxyDetectionBulk, caller, 4096, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	detector := &safeProxyDetector{manifest: manifest}
	resolved, err := RunProxyDetectors(t.Context(), detectionContext, []ProxyDetector{detector})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Primary == nil || !resolved.Primary.CanonicalProxyShell || !resolved.Primary.OfficialSingleton ||
		resolved.Primary.SingletonVersion != "test" || resolved.Primary.SingletonDeploymentType != "fixture" {
		t.Fatalf("resolved=%+v", resolved)
	}
}

func TestSafeDetectorMarksBrokenCanonicalShellInconsistent(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		word common.Hash
		code map[common.Address][]byte
	}{
		{name: "zero singleton", word: common.Hash{}},
		{name: "dirty high bytes", word: func() common.Hash { word := addressWord(testAddress(106)); word[0] = 1; return word }()},
		{name: "singleton without code", word: addressWord(testAddress(107))},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			proxy := testAddress(105)
			code := map[common.Address][]byte{proxy: safeRuntimeFixture(t, "1.4.1")}
			maps.Copy(code, test.code)
			caller := &proxyStateCaller{
				code:    code,
				storage: map[string]common.Hash{proxy.Hex() + ":" + safeSingletonSlot.Hex(): test.word},
			}
			detectionContext, err := newProxyDetectionContext(
				"1", proxy, 1003, uintWord(1003), ProxyDetectionBulk, caller, 4096, nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := RunProxyDetectors(t.Context(), detectionContext, []ProxyDetector{newSafeProxyDetector()})
			if err != nil {
				t.Fatal(err)
			}
			if resolved.Status != ProxyStatusInconsistent || resolved.Primary == nil ||
				!resolved.Primary.CanonicalProxyShell || resolved.Primary.Family != ProxyFamilySafe {
				t.Fatalf("resolved=%+v", resolved)
			}
		})
	}
}

func TestSafeDetectorDeepValidatesMasterCopyAndFactoryMigration(t *testing.T) {
	t.Parallel()
	proxy, initialSingleton, currentSingleton := testAddress(108), testAddress(109), testAddress(110)
	runtime := safeRuntimeFixture(t, "1.4.1")
	caller := &proxyStateCaller{
		code: map[common.Address][]byte{proxy: runtime, currentSingleton: {0x60, 0x03}},
		storage: map[string]common.Hash{
			proxy.Hex() + ":" + safeSingletonSlot.Hex(): addressWord(currentSingleton),
		},
		probeRaw: map[string][]byte{
			proxyProbeKey(proxy, safeMasterCopySelector): wordBytes(addressWord(currentSingleton)),
		},
	}
	detectionContext, err := newProxyDetectionContext(
		"1", proxy, 1004, uintWord(1004), ProxyDetectionDeep, caller, 4096, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	factory := common.HexToAddress("0x4e1DCf7AD4e460CfD30791CCC4F9c8a4f820ec67")
	detector := newSafeProxyDetector()
	detector.factoryLookup = func(
		_ context.Context, chainID string, address common.Address, block uint64, blockHash common.Hash,
	) (SafeFactoryProvenance, bool, error) {
		if chainID != "1" || address != proxy || block != 1004 || blockHash != uintWord(1004) {
			t.Fatalf("factory lookup chain=%s address=%s block=%d hash=%s", chainID, address, block, blockHash)
		}
		return SafeFactoryProvenance{
			Factory: factory, InitialSingleton: initialSingleton,
			DeploymentType: "canonical", Version: "1.4.1", EventLayout: "indexed-proxy",
		}, true, nil
	}
	resolved, err := RunProxyDetectors(t.Context(), detectionContext, []ProxyDetector{detector})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != ProxyStatusConfirmed || resolved.Primary == nil ||
		resolved.Primary.InitialSingleton == nil || *resolved.Primary.InitialSingleton != initialSingleton ||
		!resolved.Primary.SingletonChanged || resolved.Primary.Implementation == nil ||
		*resolved.Primary.Implementation != currentSingleton {
		t.Fatalf("resolved=%+v", resolved)
	}
	if detectionContext.Counters().Call != 1 {
		t.Fatalf("deep masterCopy calls=%+v", detectionContext.Counters())
	}
}

func TestSafeCompatibleDeepRequiresSlotCallAgreementAndCode(t *testing.T) {
	t.Parallel()
	proxy, singleton := testAddress(111), testAddress(112)
	caller := &proxyStateCaller{
		code: map[common.Address][]byte{proxy: {0x60, 0x99}, singleton: {0x60, 0x04}},
		storage: map[string]common.Hash{
			proxy.Hex() + ":" + safeSingletonSlot.Hex(): addressWord(singleton),
		},
		probeRaw: map[string][]byte{
			proxyProbeKey(proxy, safeMasterCopySelector): wordBytes(addressWord(singleton)),
		},
	}
	detectionContext, err := newProxyDetectionContext(
		"1", proxy, 1005, uintWord(1005), ProxyDetectionDeep, caller, 4096, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := RunProxyDetectors(t.Context(), detectionContext, []ProxyDetector{newSafeProxyDetector()})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != ProxyStatusCandidate || resolved.Primary == nil ||
		resolved.Primary.Variant != "safe-compatible-proxy" || resolved.Primary.CanonicalProxyShell ||
		resolved.Primary.Confidence != ProxyConfidenceMedium || resolved.Primary.Implementation == nil ||
		*resolved.Primary.Implementation != singleton {
		t.Fatalf("resolved=%+v", resolved)
	}
}

func TestSafeCanonicalRPCFailureIsUnknownNotNotDetected(t *testing.T) {
	t.Parallel()
	proxy := testAddress(113)
	caller := &proxyStateCaller{
		code:         map[common.Address][]byte{proxy: safeRuntimeFixture(t, "1.4.1")},
		methodErrors: map[string]error{"eth_getStorageAt": errors.New("timeout")},
	}
	detectionContext, err := newProxyDetectionContext(
		"1", proxy, 1006, uintWord(1006), ProxyDetectionBulk, caller, 4096, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := RunProxyDetectors(t.Context(), detectionContext, []ProxyDetector{newSafeProxyDetector()})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != ProxyStatusUnknown || len(resolved.Outcomes) != 1 ||
		resolved.Outcomes[0].Status != ProxyStatusUnknown || !resolved.Outcomes[0].CanonicalProxyShell {
		t.Fatalf("resolved=%+v", resolved)
	}
}

func TestParseSafeProxyCreationLogSupportsHistoricalLayouts(t *testing.T) {
	t.Parallel()
	proxy, singleton := testAddress(114), testAddress(115)
	for _, test := range []struct {
		name   string
		log    types.Log
		layout string
	}{
		{
			name:   "indexed proxy",
			log:    types.Log{Topics: []common.Hash{safeProxyCreationTopic, addressWord(proxy)}, Data: wordBytes(addressWord(singleton))},
			layout: "indexed-proxy",
		},
		{
			name:   "unindexed proxy",
			log:    types.Log{Topics: []common.Hash{safeProxyCreationTopic}, Data: append(wordBytes(addressWord(proxy)), wordBytes(addressWord(singleton))...)},
			layout: "unindexed-proxy",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			gotProxy, gotSingleton, gotLayout, err := ParseSafeProxyCreationLog(test.log)
			if err != nil || gotProxy != proxy || gotSingleton != singleton || gotLayout != test.layout {
				t.Fatalf("proxy=%s singleton=%s layout=%s err=%v", gotProxy, gotSingleton, gotLayout, err)
			}
		})
	}
	if _, _, _, err := ParseSafeProxyCreationLog(types.Log{
		Topics: []common.Hash{safeProxyCreationTopic}, Data: []byte{1},
	}); err == nil {
		t.Fatal("accepted malformed ProxyCreation log")
	}
}

func TestParseSafeMasterCopyIsStrict(t *testing.T) {
	t.Parallel()
	address := testAddress(116)
	parsed, err := ParseSafeMasterCopy(wordBytes(addressWord(address)))
	if err != nil || parsed != address {
		t.Fatalf("address=%s err=%v", parsed, err)
	}
	for _, data := range [][]byte{
		nil,
		{1},
		append(wordBytes(addressWord(address)), 0),
		wordBytes(common.Hash{}),
		func() []byte { word := addressWord(address); word[0] = 1; return wordBytes(word) }(),
	} {
		if _, err := ParseSafeMasterCopy(data); err == nil {
			t.Fatalf("accepted malformed masterCopy response %x", data)
		}
	}
}

func FuzzParseSafeMasterCopyNeverTruncates(f *testing.F) {
	f.Add(wordBytes(addressWord(testAddress(117))))
	f.Add([]byte{1})
	f.Fuzz(func(t *testing.T, data []byte) {
		address, err := ParseSafeMasterCopy(data)
		if err != nil {
			return
		}
		if len(data) != 32 || address == (common.Address{}) {
			t.Fatalf("accepted non-canonical response: address=%s data=%x", address, data)
		}
		for _, value := range data[:12] {
			if value != 0 {
				t.Fatalf("truncated dirty high bytes: %x", data)
			}
		}
	})
}

func safeRuntimeFixture(t *testing.T, version string) []byte {
	t.Helper()
	encoded, err := os.ReadFile("manifests/safe-proxy-sources.json")
	if err != nil {
		t.Fatal(err)
	}
	var source safeSourceFixture
	if err := json.Unmarshal(encoded, &source); err != nil {
		t.Fatal(err)
	}
	for _, fingerprint := range source.Fingerprints {
		if fingerprint.Version != version {
			continue
		}
		runtime, err := hexutil.Decode(fingerprint.RuntimeBytecode)
		if err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	t.Fatalf("Safe runtime fixture version %s not found", version)
	return nil
}

func cloneSafeManifest(source *safeProxyManifest) *safeProxyManifest {
	clone := *source
	clone.Fingerprints = append([]safeProxyFingerprint(nil), source.Fingerprints...)
	clone.Deployments = append([]safeDeployment(nil), source.Deployments...)
	clone.byRuntimeHash = make(map[common.Hash]safeProxyFingerprint, len(source.byRuntimeHash))
	maps.Copy(clone.byRuntimeHash, source.byRuntimeHash)
	return &clone
}

func TestSafeDetectorOutcomesAreStable(t *testing.T) {
	t.Parallel()
	proxy := testAddress(118)
	result := ProxyDetectionV2{
		Detector: "safe", DetectorVersion: safeProxyDetectorVersion,
		Family: ProxyFamilySafe, Variant: "safe-compatible-proxy",
		Status: ProxyStatusCandidate, Confidence: ProxyConfidenceMedium,
		Proxy: proxy, ChainID: "1", BlockNumber: 1, BlockHash: uintWord(1),
	}
	first, err := ResolveProxyDetections([]ProxyDetectionV2{result})
	if err != nil {
		t.Fatal(err)
	}
	second, err := ResolveProxyDetections([]ProxyDetectionV2{result})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("nondeterministic Safe result: first=%+v second=%+v", first, second)
	}
}
