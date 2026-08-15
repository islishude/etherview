package enrich

import (
	"bytes"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestCodeAtTransactionStartUsesOnlyPriorTransactions(t *testing.T) {
	t.Parallel()
	codeA := []byte{0x60, 0x01}
	codeB := []byte{0x60, 0x02}
	codeC := []byte{0x60, 0x03}
	changes := []transactionCodeChange{
		{index: 0, before: codeA, after: codeB},
		{index: 2, before: codeB, after: codeC},
	}
	for _, test := range []struct {
		name  string
		index uint64
		want  []byte
	}{
		{name: "before first change", index: 0, want: codeA},
		{name: "after first change", index: 1, want: codeB},
		{name: "before current change", index: 2, want: codeB},
		{name: "after second change", index: 3, want: codeC},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := codeAtTransactionStart(changes, test.index)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, test.want) {
				t.Fatalf("code at transaction %d = %x, want %x", test.index, got, test.want)
			}
		})
	}
}

func TestCodeAtTransactionStartRejectsAmbiguousHistory(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		changes []transactionCodeChange
	}{
		{name: "empty"},
		{name: "duplicate index", changes: []transactionCodeChange{
			{index: 1, before: []byte{1}, after: []byte{2}},
			{index: 1, before: []byte{2}, after: []byte{3}},
		}},
		{name: "discontinuous", changes: []transactionCodeChange{
			{index: 1, before: []byte{1}, after: []byte{2}},
			{index: 2, before: []byte{3}, after: []byte{4}},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := codeAtTransactionStart(test.changes, 3); err == nil {
				t.Fatal("ambiguous code history was accepted")
			}
		})
	}
}

func TestCodeAtTransactionStartRejectsFutureDiscontinuity(t *testing.T) {
	t.Parallel()
	changes := []transactionCodeChange{
		{index: 1, before: []byte{1}, after: []byte{2}},
		{index: 2, before: []byte{3}, after: []byte{4}},
	}
	if _, err := codeAtTransactionStart(changes, 1); err == nil {
		t.Fatal("future discontinuity was accepted")
	}
}

func TestValidateEffectiveExecutionWitnessRejectsConflict(t *testing.T) {
	t.Parallel()
	contextAddress := common.HexToAddress("0x0000000000000000000000000000000000000001")
	executionAddress := common.HexToAddress("0x0000000000000000000000000000000000000002")
	otherAddress := common.HexToAddress("0x0000000000000000000000000000000000000003")
	codeHash := common.HexToHash("0x01")
	execution := effectiveTransactionExecution{
		contextAddress: contextAddress, executionAddress: &executionAddress,
		executionCodeHash: &codeHash, resolution: "eip7702_delegate",
	}
	witness := transactionRootWitness{
		executionAddress: &otherAddress, executionCodeHash: &codeHash,
		resolution: "eip7702_delegate",
	}
	if err := validateEffectiveExecutionWitness(execution, witness); err == nil {
		t.Fatal("contradictory exact root witness was accepted")
	}
}
