package enrich

import (
	"bytes"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestParseStrictDiamondCutEventPreservesOrderedCutsAndInitMetadata(t *testing.T) {
	selectorA := [4]byte{0x11, 0x22, 0x33, 0x44}
	selectorB := [4]byte{0xaa, 0xbb, 0xcc, 0xdd}
	facet := common.HexToAddress("0x1000000000000000000000000000000000000001")
	init := common.HexToAddress("0x2000000000000000000000000000000000000002")
	data := encodeDiamondCutEvent(t, []diamondFacetCut{{
		FacetAddress: facet, Action: 0,
		FunctionSelectors: [][4]byte{selectorA, selectorB},
	}}, init, []byte{0xde, 0xad})

	record, ok := parseStrictDiamondCutEvent(types.Log{
		Topics: []common.Hash{proxyDiamondCutTopic}, Data: data, TxIndex: 7,
	})
	if !ok {
		t.Fatal("valid DiamondCut event was rejected")
	}
	if record.transactionIndex != 7 || record.init != init ||
		!bytes.Equal(record.calldata, []byte{0xde, 0xad}) || len(record.cuts) != 1 {
		t.Fatalf("record=%+v", record)
	}
	if record.cuts[0].FacetAddress != facet ||
		!bytes.Equal(record.cuts[0].FunctionSelectors[0][:], selectorA[:]) ||
		!bytes.Equal(record.cuts[0].FunctionSelectors[1][:], selectorB[:]) {
		t.Fatalf("cuts=%+v", record.cuts)
	}
	if record.cuts[0].FacetAddress == record.init {
		t.Fatal("one-shot init target was treated as a facet")
	}
}

func TestParseStrictDiamondCutEventPreservesEmptyCalldataAsBytes(t *testing.T) {
	selector := [4]byte{0x11, 0x22, 0x33, 0x44}
	facet := common.HexToAddress("0x1000000000000000000000000000000000000001")
	record, ok := parseStrictDiamondCutEvent(types.Log{
		Topics: []common.Hash{proxyDiamondCutTopic},
		Data: encodeDiamondCutEvent(t, []diamondFacetCut{{
			FacetAddress: facet, Action: 0,
			FunctionSelectors: [][4]byte{selector},
		}}, common.Address{}, []byte{}),
	})
	if !ok {
		t.Fatal("valid empty-calldata DiamondCut event was rejected")
	}
	if record.calldata == nil || len(record.calldata) != 0 {
		t.Fatalf("empty DiamondCut calldata=%#v, want non-nil empty bytes", record.calldata)
	}
}

func TestParseStrictDiamondCutEventRejectsNonCanonicalOrInvalidPayload(t *testing.T) {
	selector := [4]byte{1, 2, 3, 4}
	facet := common.HexToAddress("0x1000000000000000000000000000000000000001")
	valid := encodeDiamondCutEvent(t, []diamondFacetCut{{
		FacetAddress: facet, Action: 0, FunctionSelectors: [][4]byte{selector},
	}}, common.Address{}, nil)

	tests := []types.Log{
		{Topics: []common.Hash{proxyDiamondCutTopic, {}}, Data: valid},
		{Topics: []common.Hash{proxyDiamondCutTopic}, Data: append(append([]byte(nil), valid...), 0)},
		{Topics: []common.Hash{proxyDiamondCutTopic}, Data: encodeDiamondCutEvent(t, []diamondFacetCut{{
			FacetAddress: facet, Action: 3, FunctionSelectors: [][4]byte{selector},
		}}, common.Address{}, nil)},
		{Topics: []common.Hash{proxyDiamondCutTopic}, Data: encodeDiamondCutEvent(t, []diamondFacetCut{{
			FacetAddress: facet, Action: 0, FunctionSelectors: nil,
		}}, common.Address{}, nil)},
	}
	for index, event := range tests {
		if _, ok := parseStrictDiamondCutEvent(event); ok {
			t.Fatalf("invalid event %d was accepted", index)
		}
	}
}

func TestReplayDiamondSelectorChangesAddReplaceRemove(t *testing.T) {
	selectorA := [4]byte{0x11, 0x22, 0x33, 0x44}
	selectorB := [4]byte{0xaa, 0xbb, 0xcc, 0xdd}
	facetA := common.HexToAddress("0x1000000000000000000000000000000000000001")
	facetB := common.HexToAddress("0x2000000000000000000000000000000000000002")
	changes := []diamondSelectorChange{
		{selector: selectorA, action: 0, facet: facetA},
		{selector: selectorB, action: 0, facet: facetA},
		{selector: selectorA, action: 1, facet: facetB},
		{selector: selectorB, action: 2, facet: common.Address{}},
	}
	status, warning := replayDiamondSelectorChanges(changes, map[[4]byte]common.Address{
		selectorA: facetB,
	})
	if status != diamondHistoryConsistent || warning != "" {
		t.Fatalf("status=%d warning=%q", status, warning)
	}
}

func TestReplayDiamondSelectorChangesRejectsInvalidTransitions(t *testing.T) {
	selector := [4]byte{0x11, 0x22, 0x33, 0x44}
	facetA := common.HexToAddress("0x1000000000000000000000000000000000000001")
	facetB := common.HexToAddress("0x2000000000000000000000000000000000000002")
	tests := []struct {
		name    string
		changes []diamondSelectorChange
	}{
		{name: "duplicate add", changes: []diamondSelectorChange{
			{selector: selector, action: 0, facet: facetA},
			{selector: selector, action: 0, facet: facetB},
		}},
		{name: "replace absent", changes: []diamondSelectorChange{{selector: selector, action: 1, facet: facetB}}},
		{name: "replace same", changes: []diamondSelectorChange{
			{selector: selector, action: 0, facet: facetA},
			{selector: selector, action: 1, facet: facetA},
		}},
		{name: "remove absent", changes: []diamondSelectorChange{{selector: selector, action: 2}}},
		{name: "remove target nonzero", changes: []diamondSelectorChange{
			{selector: selector, action: 0, facet: facetA},
			{selector: selector, action: 2, facet: facetA},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, warning := replayDiamondSelectorChanges(test.changes, nil)
			if status != diamondHistoryInconsistent || warning == "" {
				t.Fatalf("status=%d warning=%q", status, warning)
			}
		})
	}
}

func FuzzParseStrictDiamondCutEvent(f *testing.F) {
	selector := [4]byte{1, 2, 3, 4}
	facet := common.HexToAddress("0x1000000000000000000000000000000000000001")
	f.Add(encodeDiamondCutEvent(f, []diamondFacetCut{{
		FacetAddress: facet, Action: 0, FunctionSelectors: [][4]byte{selector},
	}}, common.Address{}, nil))
	f.Add([]byte{0x01, 0x02})
	f.Fuzz(func(t *testing.T, data []byte) {
		record, ok := parseStrictDiamondCutEvent(types.Log{
			Topics: []common.Hash{proxyDiamondCutTopic}, Data: data,
		})
		if ok && (len(record.cuts) > DiamondMaxFacets || len(record.calldata) > DiamondMaxRawReturnBytes) {
			t.Fatal("accepted DiamondCut event exceeds configured bounds")
		}
	})
}

type testingFataler interface {
	Helper()
	Fatal(...any)
}

func encodeDiamondCutEvent(
	t testingFataler,
	cuts []diamondFacetCut,
	init common.Address,
	calldata []byte,
) []byte {
	t.Helper()
	definition := diamondCutEventABI.Events["DiamondCut"]
	data, err := definition.Inputs.Pack(cuts, init, calldata)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
