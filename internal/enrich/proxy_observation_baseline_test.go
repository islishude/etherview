package enrich

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// This fixture was captured before framework unification. It independently
// locks the authoritative relation, including empty code and rejected shapes.
func TestProxyObservationBaseline(t *testing.T) {
	proxy, implementation, beacon := testAddress(10), testAddress(11), testAddress(12)
	clone := append(append(append([]byte(nil), minimalProxyPrefix...), implementation[:]...), minimalProxySuffix...)
	fixtures := []struct {
		name    string
		code    []byte
		storage map[string]common.Hash
	}{
		{name: "empty"},
		{name: "ordinary", code: []byte{0x60, 1}},
		{name: "clone", code: clone},
		{name: "cwia", code: testSoladyLegacyCWIARuntime(implementation, []byte{1, 2, 3})},
		{name: "implementation", code: []byte{0x60, 2}, storage: map[string]common.Hash{proxy.Hex() + ":" + EIP1967ImplementationSlot.Hex(): addressWord(implementation)}},
		{name: "ambiguous", code: []byte{0x60, 2}, storage: map[string]common.Hash{proxy.Hex() + ":" + EIP1967ImplementationSlot.Hex(): addressWord(implementation), proxy.Hex() + ":" + EIP1967BeaconSlot.Hex(): addressWord(beacon)}},
		{name: "safe", code: safeRuntimeFixture(t, "1.4.1"), storage: map[string]common.Hash{proxy.Hex() + ":" + safeSingletonSlot.Hex(): addressWord(implementation)}},
	}
	actual := make(map[string]any)
	for _, fixture := range fixtures {
		caller := &proxyStateCaller{code: map[common.Address][]byte{proxy: fixture.code, implementation: {0x60, 3}}, storage: fixture.storage}
		detector := rpcProxyDetector{caller: caller, safeEnabled: true, limits: ProxyLimits{MaxCodeBytes: 4096}}
		results, err := detector.detectBlock(t.Context(), Job{ChainID: "1", BlockNumber: 60, BlockHash: uintWord(60)}, []proxyCandidate{{address: proxy}})
		if err != nil {
			t.Fatal(err)
		}
		result := results[0]
		observation := map[string]any{"code": result.code, "code_hash": result.codeHash.Hex(), "rejected": result.rejected, "exact": result.exact != nil}
		if relation := result.proxy; relation != nil {
			observation["relation"] = map[string]any{
				"kind": relation.kind, "pattern": relation.pattern, "evidence_state": relation.evidenceState,
				"implementation": relation.implementation.Hex(), "implementation_code": relation.implementationCode,
				"implementation_hash": relation.implementationHash.Hex(), "admin": relation.admin, "beacon": relation.beacon,
				"immutable_args": relation.immutableArgs, "minimal_exact": relation.minimalExact, "immutable_args_exact": relation.immutableArgsExact,
			}
		}
		actual[fixture.name] = observation
	}
	encoded, err := json.MarshalIndent(actual, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile("testdata/proxy-observation-baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(expected, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("authoritative observations changed:\n%s", encoded)
	}
}
