package enrich

import (
	"encoding/binary"
	"reflect"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func testSoladyLegacyCWIARuntime(implementation common.Address, immutableArgs []byte) []byte {
	extraLength := len(immutableArgs) + 2
	runtime := make([]byte, 0, soladyLegacyCWIARuntimePrefix+extraLength)
	runtime = append(runtime, soladyLegacyCWIABeforeLength...)
	runtime = binary.BigEndian.AppendUint16(runtime, uint16(extraLength))
	runtime = append(runtime, soladyLegacyCWIABeforeAddress...)
	runtime = append(runtime, implementation[:]...)
	runtime = append(runtime, soladyLegacyCWIAAfterAddress...)
	runtime = append(runtime, immutableArgs...)
	runtime = binary.BigEndian.AppendUint16(runtime, uint16(extraLength))
	return runtime
}

func TestDetectSoladyLegacyCWIAExactRuntime(t *testing.T) {
	t.Parallel()
	implementation := testAddress(0x71)
	args := make([]byte, 52)
	copy(args[:20], testAddress(0x72).Bytes())
	args[len(args)-1] = 42
	runtime := testSoladyLegacyCWIARuntime(implementation, args)

	detected, matched := DetectSoladyLegacyCWIA(runtime)
	if !matched || detected.InvalidLength || detected.ImmutableArgsTooLarge ||
		detected.Implementation != implementation || !reflect.DeepEqual(detected.ImmutableArgs, args) {
		t.Fatalf("detected=%+v matched=%v", detected, matched)
	}
	if len(runtime) != soladyLegacyCWIARuntimePrefix+len(args)+2 ||
		binary.BigEndian.Uint16(runtime[soladyLegacyCWIALengthOffset:soladyLegacyCWIALengthOffset+2]) != uint16(len(args)+2) ||
		binary.BigEndian.Uint16(runtime[len(runtime)-2:]) != uint16(len(args)+2) {
		t.Fatalf("runtime layout is inconsistent: bytes=%d", len(runtime))
	}

	empty, matched := DetectSoladyLegacyCWIA(testSoladyLegacyCWIARuntime(implementation, nil))
	if !matched || empty.InvalidLength || empty.ImmutableArgsTooLarge || len(empty.ImmutableArgs) != 0 {
		t.Fatalf("empty=%+v matched=%v", empty, matched)
	}
}

func TestDetectSoladyLegacyCWIARejectsShellMutations(t *testing.T) {
	t.Parallel()
	implementation := testAddress(0x73)
	runtime := testSoladyLegacyCWIARuntime(implementation, []byte{1, 2, 3})
	fixed := make([]int, 0, soladyLegacyCWIARuntimePrefix)
	for index := range soladyLegacyCWIARuntimePrefix {
		if index >= soladyLegacyCWIALengthOffset && index < soladyLegacyCWIALengthOffset+2 ||
			index >= soladyLegacyCWIAAddressOffset && index < soladyLegacyCWIAAddressOffset+common.AddressLength {
			continue
		}
		fixed = append(fixed, index)
	}
	for _, index := range fixed {
		mutated := common.CopyBytes(runtime)
		mutated[index] ^= 1
		if _, matched := DetectSoladyLegacyCWIA(mutated); matched {
			t.Fatalf("accepted fixed shell mutation at byte %d", index)
		}
	}
}

func TestDetectSoladyLegacyCWIADistinguishesInvalidLengthAndLimit(t *testing.T) {
	t.Parallel()
	implementation := testAddress(0x74)
	runtime := testSoladyLegacyCWIARuntime(implementation, []byte{1, 2, 3})

	for name, mutate := range map[string]func([]byte) []byte{
		"embedded": func(value []byte) []byte {
			value[soladyLegacyCWIALengthOffset+1]++
			return value
		},
		"footer": func(value []byte) []byte {
			value[len(value)-1]++
			return value
		},
		"extra-byte": func(value []byte) []byte {
			return append(value, 0)
		},
	} {
		t.Run(name, func(t *testing.T) {
			mutated := mutate(common.CopyBytes(runtime))
			detected, matched := DetectSoladyLegacyCWIA(mutated)
			if !matched || !detected.InvalidLength || detected.ImmutableArgsTooLarge || len(detected.ImmutableArgs) != 0 {
				t.Fatalf("detected=%+v matched=%v", detected, matched)
			}
		})
	}
	if _, matched := DetectSoladyLegacyCWIA(runtime[:soladyLegacyCWIAMinimumRuntime-1]); matched {
		t.Fatal("accepted a truncated CWIA runtime")
	}

	oversized := testSoladyLegacyCWIARuntime(implementation, make([]byte, MaxCloneImmutableArgs+1))
	detected, matched := DetectSoladyLegacyCWIA(oversized)
	if !matched || detected.InvalidLength || !detected.ImmutableArgsTooLarge || len(detected.ImmutableArgs) != 0 {
		t.Fatalf("oversized=%+v matched=%v", detected, matched)
	}
}

func TestRPCProxyDetectorRecognizesSoladyLegacyCWIA(t *testing.T) {
	t.Parallel()
	proxy, implementation := testAddress(0x75), testAddress(0x76)
	args := append(testAddress(0x77).Bytes(), make([]byte, 32)...)
	args[len(args)-1] = 99
	runtime := testSoladyLegacyCWIARuntime(implementation, args)
	caller := &proxyStateCaller{code: map[common.Address][]byte{
		proxy: runtime, implementation: {0x60, 0x00},
	}}
	detector := rpcProxyDetector{
		caller:    caller,
		limits:    ProxyLimits{MaxCandidates: 2, MaxCodeBytes: 1 << 20, MaxDetailsBytes: 1024},
		v2Enabled: true,
	}
	job := Job{ID: "cwia", Stage: ProxyStage, ChainID: "1", BlockHash: uintWord(901), BlockNumber: 901}
	detections, err := detector.detectBlock(t.Context(), job, []proxyCandidate{{address: proxy, force: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(detections) != 1 || detections[0].proxy == nil || detections[0].rejected != "" {
		t.Fatalf("detections=%+v", detections)
	}
	resolved := detections[0].proxy
	if resolved.kind != ProxyCWIA || resolved.pattern != ProxyPatternClone ||
		resolved.implementation != implementation || !resolved.immutableArgsExact ||
		!reflect.DeepEqual(resolved.immutableArgs, args) || resolved.evidenceState != "exact" {
		t.Fatalf("resolved=%+v", resolved)
	}
	if detections[0].v2.Primary == nil || detections[0].v2.Primary.Detector != "solady-cwia" ||
		detections[0].v2.Primary.DetectorVersion != soladyLegacyCWIADetectorVersion ||
		detections[0].v2.Primary.Family != ProxyFamilyCWIA ||
		detections[0].v2.Primary.Variant != soladyLegacyCWIAVariant ||
		detections[0].v2.Primary.Status != ProxyStatusConfirmed {
		t.Fatalf("v2=%+v", detections[0].v2)
	}
	caller.mu.Lock()
	calls := append([]proxyRPCCall(nil), caller.calls...)
	caller.mu.Unlock()
	if len(calls) != 2 || calls[0].method != "eth_getCode" || calls[1].method != "eth_getCode" {
		t.Fatalf("RPC calls=%+v", calls)
	}
	for _, call := range calls {
		if call.blockHash != job.BlockHash.Hex() {
			t.Fatalf("RPC call=%+v, want block %s", call, job.BlockHash)
		}
	}
}

func TestRPCProxyDetectorRejectsInvalidSoladyLegacyCWIAIdentity(t *testing.T) {
	t.Parallel()
	proxy := testAddress(0x78)
	for name, runtime := range map[string][]byte{
		"zero": testSoladyLegacyCWIARuntime(common.Address{}, nil),
		"self": testSoladyLegacyCWIARuntime(proxy, nil),
		"length": func() []byte {
			value := testSoladyLegacyCWIARuntime(testAddress(0x79), nil)
			value[len(value)-1]++
			return value
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			caller := &proxyStateCaller{code: map[common.Address][]byte{proxy: runtime}}
			detector := rpcProxyDetector{
				caller:    caller,
				limits:    ProxyLimits{MaxCandidates: 2, MaxCodeBytes: 1 << 20, MaxDetailsBytes: 1024},
				v2Enabled: true,
			}
			detections, err := detector.detectBlock(t.Context(), Job{
				ID: name, Stage: ProxyStage, ChainID: "1", BlockHash: uintWord(902), BlockNumber: 902,
			}, []proxyCandidate{{address: proxy, force: true}})
			if err != nil {
				t.Fatal(err)
			}
			if len(detections) != 1 || detections[0].proxy != nil || detections[0].rejected == "" ||
				detections[0].v2.Primary == nil || detections[0].v2.Primary.Status != ProxyStatusInconsistent {
				t.Fatalf("detections=%+v", detections)
			}
		})
	}
}

func TestCWIAAndDiamondDetectionsAreComposedLayers(t *testing.T) {
	t.Parallel()
	proxy := testAddress(0x7a)
	left := ProxyDetectionV2{Proxy: proxy, Family: ProxyFamilyCWIA}
	right := ProxyDetectionV2{Proxy: proxy, Family: ProxyFamilyERC2535}
	if proxyDetectionsConflict(left, right) || proxyDetectionsConflict(right, left) {
		t.Fatal("CWIA and Diamond layers were treated as a detector conflict")
	}
}

func FuzzDetectSoladyLegacyCWIA(f *testing.F) {
	implementation := testAddress(0x7b)
	f.Add(testSoladyLegacyCWIARuntime(implementation, nil))
	f.Add(testSoladyLegacyCWIARuntime(implementation, []byte{1, 2, 3}))
	f.Add([]byte{0x36, 0x60, 0x2c})
	f.Fuzz(func(t *testing.T, runtime []byte) {
		detected, matched := DetectSoladyLegacyCWIA(runtime)
		if !matched || detected.InvalidLength || detected.ImmutableArgsTooLarge {
			return
		}
		rebuilt := testSoladyLegacyCWIARuntime(detected.Implementation, detected.ImmutableArgs)
		if !reflect.DeepEqual(rebuilt, runtime) {
			t.Fatalf("accepted runtime does not round trip: runtime=%x rebuilt=%x", runtime, rebuilt)
		}
	})
}
