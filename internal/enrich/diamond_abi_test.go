package enrich

import (
	"encoding/json"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestFilterABIFunctionSelectorKeepsOnlyActiveFacetFunctions(t *testing.T) {
	document := []byte(`[
		{"type":"function","name":"value","stateMutability":"view","inputs":[],"outputs":[{"type":"uint256"}]},
		{"type":"function","name":"setValue","stateMutability":"nonpayable","inputs":[{"type":"uint256"}],"outputs":[]},
		{"type":"event","name":"ValueChanged","anonymous":false,"inputs":[{"type":"uint256","indexed":false}]}
	]`)
	filtered, err := filterABIFunctionSelector(
		document, ABISourceDiamondFacet, selectorForSignature("setValue(uint256)"), DefaultDecodeLimits(),
	)
	if err != nil {
		t.Fatal(err)
	}
	var entries []map[string]any
	if err := json.Unmarshal(filtered, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0]["name"] != "setValue" || entries[0]["type"] != "function" {
		t.Fatalf("filtered ABI=%s", filtered)
	}
}

func TestFilterABIAuxiliaryEntriesMergesOnlyEventsAndErrors(t *testing.T) {
	document := []byte(`[
		{"type":"function","name":"value","stateMutability":"view","inputs":[],"outputs":[{"type":"uint256"}]},
		{"type":"event","name":"Changed","anonymous":false,"inputs":[{"type":"uint256","indexed":false}]},
		{"type":"error","name":"Denied","inputs":[{"type":"address"}]}
	]`)
	filtered, err := filterABIAuxiliaryEntries(document, DefaultDecodeLimits())
	if err != nil {
		t.Fatal(err)
	}
	var entries []map[string]any
	if err := json.Unmarshal(filtered, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0]["type"] != "event" || entries[1]["type"] != "error" {
		t.Fatalf("auxiliary ABI=%s", filtered)
	}
}

func TestDiamondAmbiguityCandidatesRetainFacetProvenance(t *testing.T) {
	left := common.HexToAddress("0x1000000000000000000000000000000000000001")
	right := common.HexToAddress("0x2000000000000000000000000000000000000002")
	entries := []decodedABICandidate{
		{entry: abiEntry{signature: "Changed(uint256)", source: ABISourceDiamondFacet, sourceAddress: left}},
		{entry: abiEntry{signature: "Changed(uint256)", source: ABISourceDiamondFacet, sourceAddress: right}},
	}
	result, selected := chooseDecodedABICandidate(ABIKindEvent, entries)
	if result.Status != DecodeAmbiguous || selected != nil || len(result.Candidates) != 2 ||
		result.Candidates[0] == result.Candidates[1] {
		t.Fatalf("result=%+v selected=%+v", result, selected)
	}
}

func TestFilterABIFunctionSelectorPreservesSelectorCollisionAsAmbiguousCandidates(t *testing.T) {
	// These two canonical signatures intentionally share 0x42966c68.
	document := []byte(`[
		{"type":"function","name":"burn","stateMutability":"nonpayable","inputs":[{"type":"uint256"}],"outputs":[]},
		{"type":"function","name":"collate_propagate_storage","stateMutability":"nonpayable","inputs":[{"type":"bytes16"}],"outputs":[]}
	]`)
	selector := selectorForSignature("burn(uint256)")
	if selector != selectorForSignature("collate_propagate_storage(bytes16)") {
		t.Fatal("test signatures no longer collide")
	}
	filtered, err := filterABIFunctionSelector(
		document, ABISourceDiamondFacet, selector, DefaultDecodeLimits(),
	)
	if err != nil {
		t.Fatal(err)
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(filtered, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("selector collision was silently reduced: %s", filtered)
	}
}

func selectorForSignature(signature string) [4]byte {
	hash := crypto.Keccak256([]byte(signature))
	var selector [4]byte
	copy(selector[:], hash[:4])
	return selector
}
