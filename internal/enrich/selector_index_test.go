package enrich

import (
	"encoding/hex"
	"testing"
)

func TestNormalizeVerifiedFunctionSelectorsCanonicalizesAndKeepsCollisions(t *testing.T) {
	t.Parallel()
	document := []byte(`[
      {"type":"function","name":"mix","inputs":[{"name":"value","type":"tuple","components":[{"name":"amount","type":"uint256"},{"name":"owner","type":"address"}]}]},
      {"type":"function","name":"burn","inputs":[{"name":"value","type":"uint256"}]},
      {"type":"function","name":"collate_propagate_storage","inputs":[{"name":"value","type":"bytes16"}]},
      {"type":"event","name":"Ignored","inputs":[]}
    ]`)
	selectors, err := NormalizeVerifiedFunctionSelectors(document)
	if err != nil {
		t.Fatal(err)
	}
	if len(selectors) != 3 {
		t.Fatalf("selectors = %+v", selectors)
	}
	bySignature := make(map[string]VerifiedFunctionSelector, len(selectors))
	for _, selector := range selectors {
		bySignature[selector.Signature] = selector
	}
	if got := bySignature["mix((uint256,address))"]; got.Name != "mix" || len(got.ABIEntry) == 0 {
		t.Fatalf("tuple selector = %+v", got)
	}
	for _, signature := range []string{"burn(uint256)", "collate_propagate_storage(bytes16)"} {
		entry := bySignature[signature]
		if got := hex.EncodeToString(entry.Selector[:]); got != "42966c68" {
			t.Fatalf("%s selector = %s", signature, got)
		}
	}
}

func TestNormalizeVerifiedFunctionSelectorsRejectsMalformedAndBounds(t *testing.T) {
	t.Parallel()
	if _, err := NormalizeVerifiedFunctionSelectors([]byte(`[{"type":"function","name":"bad","inputs":[{"type":"tuple"}]}]`)); err == nil {
		t.Fatal("malformed tuple ABI was accepted")
	}
	oversized := make([]byte, DefaultDecodeLimits().MaxDocumentBytes+1)
	if _, err := NormalizeVerifiedFunctionSelectors(oversized); err == nil {
		t.Fatal("oversized ABI was accepted")
	}
}

func TestVerifiedFunctionCalldataMatchesRequiresExactReencoding(t *testing.T) {
	t.Parallel()
	selectors, err := NormalizeVerifiedFunctionSelectors([]byte(`[
      {"type":"function","name":"setValue","inputs":[{"name":"value","type":"uint256"}]}
    ]`))
	if err != nil || len(selectors) != 1 {
		t.Fatalf("selectors=%+v err=%v", selectors, err)
	}
	calldata := append(selectors[0].Selector[:], make([]byte, 32)...)
	calldata[len(calldata)-1] = 1
	if !VerifiedFunctionCalldataMatches(selectors[0].ABIEntry, calldata) {
		t.Fatal("canonical calldata did not match")
	}
	if VerifiedFunctionCalldataMatches(selectors[0].ABIEntry, append(calldata, make([]byte, 32)...)) {
		t.Fatal("calldata with a trailing word matched")
	}
}
