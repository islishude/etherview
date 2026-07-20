package enrich

import "testing"

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
		minimum := len(minimalProxyPrefix) + len(Address{}) + len(minimalProxySuffix)
		if len(code) < minimum || detected.Exact != (len(code) == minimum) ||
			len(detected.TrailingData) != len(code)-minimum {
			t.Fatalf("accepted inconsistent minimal proxy: code=%x detected=%+v", code, detected)
		}
	})
}
