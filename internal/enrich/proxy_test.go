package enrich

import (
	"encoding/hex"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestEIP1967SlotsMatchCanonicalConstants(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		got  common.Hash
		want string
	}{
		"implementation": {
			got:  EIP1967ImplementationSlot,
			want: "360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc",
		},
		"beacon": {
			got:  EIP1967BeaconSlot,
			want: "a3f0ad74e5423aebfd80d3ef4346578335a9a72aeaee59ff6cb3582b35133d50",
		},
		"admin": {
			got:  EIP1967AdminSlot,
			want: "b53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d6103",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := hex.EncodeToString(test.got[:]); got != test.want {
				t.Fatalf("slot = %s, want canonical %s", got, test.want)
			}
		})
	}
}

func TestDetectEIP1167AndImmutableArgs(t *testing.T) {
	t.Parallel()
	implementation := testAddress(0x77)
	code := append(append(append([]byte(nil), minimalProxyPrefix...), implementation[:]...), minimalProxySuffix...)
	detected, ok := DetectEIP1167(code)
	if !ok || detected.Implementation != implementation || !detected.Exact || len(detected.TrailingData) != 0 {
		t.Fatalf("detected=%+v ok=%v", detected, ok)
	}
	code = append(code, 1, 2, 3)
	detected, ok = DetectEIP1167(code)
	if !ok || detected.Exact || string(detected.TrailingData) != string([]byte{1, 2, 3}) {
		t.Fatalf("detected=%+v ok=%v", detected, ok)
	}
	code[len(minimalProxyPrefix)+len(implementation)] ^= 1
	if _, ok := DetectEIP1167(code); ok {
		t.Fatal("accepted malformed EIP-1167 suffix")
	}
	oversized := append(append(append([]byte(nil), minimalProxyPrefix...), implementation[:]...), minimalProxySuffix...)
	oversized = append(oversized, make([]byte, MaxCloneImmutableArgs+1)...)
	detected, ok = DetectEIP1167(oversized)
	if !ok || !detected.ImmutableArgsTooLarge || detected.Implementation != implementation ||
		len(detected.TrailingData) != 0 {
		t.Fatalf("oversized detection=%+v ok=%v", detected, ok)
	}
}

func TestAuthenticateOpenZeppelinImmutableCloneRequiresExactInitcodeAndRuntime(t *testing.T) {
	t.Parallel()
	implementation := testAddress(0x78)
	runtime := append(append(append([]byte(nil), minimalProxyPrefix...), implementation[:]...), minimalProxySuffix...)
	runtime = append(runtime, []byte("immutable-args")...)
	size := len(runtime)
	initcode := append([]byte{
		0x61, byte(size >> 8), byte(size), 0x3d, 0x81, 0x60, 0x0a, 0x3d, 0x39, 0xf3,
	}, runtime...)
	if !AuthenticateOpenZeppelinImmutableClone(initcode, runtime) {
		t.Fatal("rejected exact OpenZeppelin immutable clone initcode")
	}
	for name, mutate := range map[string]func([]byte){
		"length":  func(value []byte) { value[2]++ },
		"header":  func(value []byte) { value[4] ^= 1 },
		"runtime": func(value []byte) { value[len(value)-1] ^= 1 },
	} {
		t.Run(name, func(t *testing.T) {
			malformed := append([]byte(nil), initcode...)
			mutate(malformed)
			if AuthenticateOpenZeppelinImmutableClone(malformed, runtime) {
				t.Fatal("accepted malformed immutable clone initcode")
			}
		})
	}
	if AuthenticateOpenZeppelinImmutableClone(initcode, runtime[:len(runtime)-1]) {
		t.Fatal("accepted mismatched deployed runtime")
	}
}

func TestEIP1967StorageAndVersionTimeline(t *testing.T) {
	t.Parallel()
	implementation1, implementation2 := testAddress(1), testAddress(2)
	beacon := testAddress(3)
	references, err := ParseEIP1967Storage(addressWord(implementation1), addressWord(beacon))
	if err != nil {
		t.Fatal(err)
	}
	if len(references) != 2 || references[0].Kind != ProxyEIP1967 || references[0].Target != implementation1 || references[1].Kind != ProxyBeacon || references[1].Target != beacon {
		t.Fatalf("references=%+v", references)
	}
	proxy := testAddress(9)
	codeHash := uintWord(123)
	versions, changed, err := ApplyProxyObservation(nil, ProxyObservation{
		BlockNumber: 10, Proxy: proxy, CodeHash: codeHash,
		Reference: ProxyReference{Kind: ProxyEIP1967, Target: implementation1, Confidence: ConfidenceHigh},
	})
	if err != nil || !changed || len(versions) != 1 {
		t.Fatalf("versions=%+v changed=%v err=%v", versions, changed, err)
	}
	versions, changed, err = ApplyProxyObservation(versions, ProxyObservation{
		BlockNumber: 12, Proxy: proxy, CodeHash: codeHash,
		Reference: ProxyReference{Kind: ProxyEIP1967, Target: implementation1, Confidence: ConfidenceHigh},
	})
	if err != nil || changed || len(versions) != 1 {
		t.Fatalf("idempotent versions=%+v changed=%v err=%v", versions, changed, err)
	}
	versions, changed, err = ApplyProxyObservation(versions, ProxyObservation{
		BlockNumber: 20, Proxy: proxy, CodeHash: codeHash,
		Reference: ProxyReference{Kind: ProxyEIP1967, Target: implementation2, Confidence: ConfidenceHigh},
	})
	if err != nil || !changed || len(versions) != 2 || versions[0].ThroughBlock == nil || *versions[0].ThroughBlock != 19 || versions[1].FromBlock != 20 {
		t.Fatalf("upgraded versions=%+v changed=%v err=%v", versions, changed, err)
	}
}

func TestParseBeaconImplementationRejectsTruncation(t *testing.T) {
	t.Parallel()
	implementation := testAddress(4)
	parsed, err := ParseBeaconImplementation(wordBytes(addressWord(implementation)))
	if err != nil || parsed != implementation {
		t.Fatalf("parsed=%v err=%v", parsed, err)
	}
	if _, err := ParseBeaconImplementation([]byte{1}); err == nil {
		t.Fatal("accepted short beacon response")
	}
}

func TestParseOpenZeppelin5UUPSResponsesStrictly(t *testing.T) {
	t.Parallel()
	if err := ParseUUPSProxiableUUID(wordBytes(EIP1967ImplementationSlot)); err != nil {
		t.Fatal(err)
	}
	wrong := EIP1967ImplementationSlot
	wrong[31] ^= 1
	if err := ParseUUPSProxiableUUID(wordBytes(wrong)); err == nil {
		t.Fatal("accepted wrong UUPS UUID")
	}
	if err := ParseUUPSProxiableUUID(append(wordBytes(EIP1967ImplementationSlot), 0)); err == nil {
		t.Fatal("accepted overlong UUPS UUID")
	}
	version := make([]byte, 96)
	version[31] = 32
	version[63] = 5
	copy(version[64:], "5.0.0")
	if err := ParseUUPSInterfaceVersion(version); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func([]byte){
		"offset":  func(value []byte) { value[31] = 64 },
		"version": func(value []byte) { value[68] = '1' },
		"padding": func(value []byte) { value[95] = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			malformed := append([]byte(nil), version...)
			mutate(malformed)
			if err := ParseUUPSInterfaceVersion(malformed); err == nil {
				t.Fatal("accepted malformed UUPS interface version")
			}
		})
	}
}

func TestProxyLifecycleEventsRequireCanonicalABIShapes(t *testing.T) {
	t.Parallel()
	target := testAddress(0x88)
	address, ok := parseStrictIndexedAddressEvent(types.Log{
		Topics: []common.Hash{proxyUpgradedTopic, addressWord(target)},
	})
	if !ok || address != target {
		t.Fatalf("upgrade address=%s ok=%v", address, ok)
	}
	if _, ok := parseStrictIndexedAddressEvent(types.Log{
		Topics: []common.Hash{proxyUpgradedTopic, addressWord(target)}, Data: []byte{0},
	}); ok {
		t.Fatal("accepted upgrade event with data")
	}
	initialized := types.Log{Topics: []common.Hash{proxyInitializedTopic}, Data: make([]byte, 32)}
	initialized.Data[24] = 1
	version, ok := parseStrictInitializedEvent(initialized)
	if !ok || version != 1<<56 {
		t.Fatalf("initialized version=%d ok=%v", version, ok)
	}
	initialized.Data[0] = 1
	if _, ok := parseStrictInitializedEvent(initialized); ok {
		t.Fatal("accepted initialized version wider than uint64")
	}
}

func FuzzDetectEIP1167IsBoundedAndExact(f *testing.F) {
	implementation := testAddress(0x77)
	valid := append(append(append([]byte(nil), minimalProxyPrefix...), implementation[:]...), minimalProxySuffix...)
	f.Add(valid)
	f.Add(append(append([]byte(nil), valid...), []byte("immutable")...))
	f.Add([]byte{0x36, 0x3d})
	f.Fuzz(func(t *testing.T, code []byte) {
		if len(code) > 1<<20 {
			t.Skip()
		}
		detected, ok := DetectEIP1167(code)
		if !ok {
			return
		}
		minimum := len(minimalProxyPrefix) + len(common.Address{}) + len(minimalProxySuffix)
		if detected.ImmutableArgsTooLarge {
			if len(code)-minimum <= MaxCloneImmutableArgs || len(detected.TrailingData) != 0 {
				t.Fatalf("accepted inconsistent oversized minimal proxy: code_bytes=%d detected=%+v", len(code), detected)
			}
			return
		}
		if len(code) < minimum || detected.Exact != (len(code) == minimum) ||
			len(detected.TrailingData) != len(code)-minimum {
			t.Fatalf("accepted inconsistent minimal proxy: code=%x detected=%+v", code, detected)
		}
	})
}
