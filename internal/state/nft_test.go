package state

import (
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/httpapi"
)

func TestERC721OwnerObservationUsesExactBlockHashAndABI(t *testing.T) {
	t.Parallel()
	reference := CanonicalRef{Number: 42, Hash: testStateHash(9)}
	contract, _ := ethrpc.ParseAddress("0x1111111111111111111111111111111111111111")
	owner, _ := ethrpc.ParseAddress("0x000000000000000000000000000000000000dEaD")
	ownerBytes := owner.Bytes()
	result := make([]byte, 32)
	copy(result[12:], ownerBytes)
	service := &testStateRPC{callResult: result}

	observation, err := callERC721Owner(t.Context(), newTestStateEndpoint(t, service), reference, contract, big.NewInt(123))
	if err != nil {
		t.Fatal(err)
	}
	if !observation.Exists || observation.Owner != "0x000000000000000000000000000000000000dEaD" ||
		observation.Confidence != catalog.NFTStateConfidenceRPCExact {
		t.Fatalf("observation=%+v", observation)
	}
	if service.method != "eth_call" || len(service.params) != 2 {
		t.Fatalf("method=%q params=%#v", service.method, service.params)
	}
	call := service.params[0].(map[string]any)
	wantData := "0x6352211e" + strings.Repeat("0", 62) + "7b"
	if call["to"] != contract.Hex() || call["data"] != wantData {
		t.Fatalf("call=%#v want data=%s", call, wantData)
	}
	if selector := service.params[1]; !reflect.DeepEqual(selector, canonicalSelector(reference)) {
		t.Fatalf("selector=%#v", selector)
	}
}

func TestERC721OwnerRevertIsExactNotFound(t *testing.T) {
	t.Parallel()
	contract, _ := ethrpc.ParseAddress("0x1111111111111111111111111111111111111111")
	service := &testStateRPC{err: &testRPCError{code: 3, message: "execution reverted"}}
	observation, err := callERC721Owner(t.Context(), newTestStateEndpoint(t, service), CanonicalRef{Number: 1, Hash: testStateHash(1)}, contract, big.NewInt(1))
	if err != nil || observation.Exists || observation.Owner != "" || observation.Confidence != catalog.NFTStateConfidenceRPCExact {
		t.Fatalf("observation=%+v error=%v", observation, err)
	}
}

func TestERC1155BalanceObservationUsesExactBlockHashAndABI(t *testing.T) {
	t.Parallel()
	reference := CanonicalRef{Number: 7, Hash: testStateHash(7)}
	contract, _ := ethrpc.ParseAddress("0x1111111111111111111111111111111111111111")
	owner, _ := ethrpc.ParseAddress("0x2222222222222222222222222222222222222222")
	result := make([]byte, 32)
	big.NewInt(987).FillBytes(result)
	service := &testStateRPC{callResult: result}
	balance, err := callERC1155Balance(t.Context(), newTestStateEndpoint(t, service), reference, contract, owner, big.NewInt(123))
	if err != nil || balance != "987" {
		t.Fatalf("balance=%q error=%v", balance, err)
	}
	call := service.params[0].(map[string]any)
	wantData := "0x00fdd58e" + strings.Repeat("0", 24) + strings.Repeat("22", 20) + strings.Repeat("0", 62) + "7b"
	if call["data"] != wantData || !reflect.DeepEqual(service.params[1], canonicalSelector(reference)) {
		t.Fatalf("call=%#v selector=%#v", call, service.params[1])
	}
}

func TestNFTStateRejectsMalformedResultsAndNonCanonicalSnapshot(t *testing.T) {
	t.Parallel()
	contract, _ := ethrpc.ParseAddress("0x1111111111111111111111111111111111111111")
	owner, _ := ethrpc.ParseAddress("0x2222222222222222222222222222222222222222")
	malformed := &testStateRPC{callResult: hexutil.Bytes{1}}
	if _, err := callERC1155Balance(t.Context(), newTestStateEndpoint(t, malformed), CanonicalRef{Number: 1, Hash: testStateHash(1)}, contract, owner, big.NewInt(1)); !errors.Is(err, httpapi.ErrUnavailable) {
		t.Fatalf("malformed result error=%v", err)
	}
	if _, _, err := validateNFTSnapshot(catalog.Snapshot{
		ChainID: "1", BlockNumber: "01", BlockHash: testStateHash(1).String(),
	}); err == nil {
		t.Fatal("non-canonical block number was accepted")
	}
}
