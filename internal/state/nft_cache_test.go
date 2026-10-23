package state

import (
	"errors"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/catalog"
)

func TestNFTBalancesValidateWholeBatchBeforeIO(t *testing.T) {
	t.Parallel()
	valid := catalog.NFTBalanceCandidate{Standard: "erc721", TokenAddress: common.HexToAddress("0x1").Hex(), TokenID: "1"}
	for _, test := range []struct {
		name       string
		candidates []catalog.NFTBalanceCandidate
		want       string
	}{
		{"too many", make([]catalog.NFTBalanceCandidate, 257), "too many NFT balance candidates"},
		{"invalid final candidate", []catalog.NFTBalanceCandidate{valid, {Standard: "erc1155", TokenAddress: valid.TokenAddress, TokenID: "01"}}, "invalid NFT candidate token ID 1"},
		{"duplicate", []catalog.NFTBalanceCandidate{valid, valid}, "duplicate NFT balance candidate"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &testStateRPC{err: errors.New("unexpected RPC")}
			reconciler := &TokenReconciler{db: stateTestDatabase(t), pool: newERC20TestPool(t, service, nil), canonical: testCanonical{canonical: true}}
			_, err := reconciler.Balances(t.Context(), catalog.Snapshot{ChainID: "1", BlockNumber: "42", BlockHash: testStateHash(42).Hex()}, valid.TokenAddress, test.candidates)
			if err == nil || err.Error() != test.want || service.method != "" {
				t.Fatalf("error=%v, want %q; RPC=%q", err, test.want, service.method)
			}
		})
	}
}

func TestNFTBalancesBulkCachePreservesCandidateIdentityAndOrder(t *testing.T) {
	t.Parallel()
	contract, owner, other := common.HexToAddress("0x1"), common.HexToAddress("0x2"), common.HexToAddress("0x3")
	confidence := catalog.NFTStateConfidenceRPCExact
	service := &testStateRPC{err: errors.New("cache hit must not call RPC")}
	reconciler := &TokenReconciler{
		db: stateTestDatabase(t,
			stateSQLExpectation{kind: "query", contains: "StateERC721OwnerObservations", rows: [][]any{
				{contract.Bytes(), "2", "owned", other.Bytes(), confidence},
				{contract.Bytes(), "1", "owned", owner.Bytes(), confidence},
				{contract.Bytes(), "3", "not_found", []byte(nil), confidence},
			}},
			stateSQLExpectation{kind: "query", contains: "StateERC1155BalanceObservations", rows: [][]any{
				{contract.Bytes(), "1", "7", confidence},
			}},
		),
		pool: newERC20TestPool(t, service, nil), canonical: testCanonical{canonical: true},
	}
	got, err := reconciler.Balances(t.Context(), catalog.Snapshot{ChainID: "1", BlockNumber: "42", BlockHash: testStateHash(42).Hex()}, owner.Hex(), []catalog.NFTBalanceCandidate{
		{Standard: "erc1155", TokenAddress: contract.Hex(), TokenID: "1"},
		{Standard: "erc721", TokenAddress: contract.Hex(), TokenID: "1"},
		{Standard: "erc721", TokenAddress: contract.Hex(), TokenID: "2"},
		{Standard: "erc721", TokenAddress: contract.Hex(), TokenID: "3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("observations=%+v", got)
	}
	for i, want := range []string{"7", "1", "0", "0"} {
		if got[i].Balance != want || got[i].Confidence != confidence {
			t.Fatalf("observation[%d]=%+v", i, got[i])
		}
	}
	if service.method != "" {
		t.Fatalf("unexpected RPC: %s", service.method)
	}
}

func TestParseUint256RejectsOversizedDecimal(t *testing.T) {
	t.Parallel()
	if _, err := parseUint256(strings.Repeat("1", 10000)); err == nil {
		t.Fatal("oversized uint256 accepted")
	}
	if got, err := parseUint256(maximumUint256.String()); err != nil || got.Cmp(maximumUint256) != 0 {
		t.Fatalf("uint256 maximum=%v error=%v", got, err)
	}
}

func TestNFTBalancesRejectCorruptBulkCacheWithoutRPC(t *testing.T) {
	t.Parallel()
	contract := common.HexToAddress("0x1")
	valid := []any{contract.Bytes(), "1", "7", catalog.NFTStateConfidenceRPCExact}
	for _, test := range []struct {
		name string
		rows [][]any
	}{
		{"wrong token ID", [][]any{{contract.Bytes(), "2", "7", catalog.NFTStateConfidenceRPCExact}}},
		{"short address", [][]any{{[]byte{1}, "1", "7", catalog.NFTStateConfidenceRPCExact}}},
		{"noncanonical token ID", [][]any{{contract.Bytes(), "01", "7", catalog.NFTStateConfidenceRPCExact}}},
		{"noncanonical balance", [][]any{{contract.Bytes(), "1", "+7", catalog.NFTStateConfidenceRPCExact}}},
		{"confidence", [][]any{{contract.Bytes(), "1", "7", "inferred"}}},
		{"duplicate", [][]any{valid, valid}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &testStateRPC{err: errors.New("corrupt cache must not fall back to RPC")}
			reconciler := &TokenReconciler{
				db:   stateTestDatabase(t, stateSQLExpectation{kind: "query", contains: "StateERC1155BalanceObservations", rows: test.rows}),
				pool: newERC20TestPool(t, service, nil), canonical: testCanonical{canonical: true},
			}
			got, err := reconciler.Balances(t.Context(), catalog.Snapshot{ChainID: "1", BlockNumber: "42", BlockHash: testStateHash(42).Hex()}, contract.Hex(), []catalog.NFTBalanceCandidate{{Standard: "erc1155", TokenAddress: contract.Hex(), TokenID: "1"}})
			if err == nil || got != nil || service.method != "" {
				t.Fatalf("balances=%+v error=%v RPC=%s", got, err, service.method)
			}
		})
	}
}
