package state

import (
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/httpapi"
)

type nftBatchObserver struct {
	observations []ethrpc.Observation
}

func (observer *nftBatchObserver) RecordRPC(observation ethrpc.Observation) {
	observer.observations = append(observer.observations, observation)
}

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

func TestNFTBalanceBatchUsesOneExactMixedBatch(t *testing.T) {
	t.Parallel()
	reference := CanonicalRef{Number: 42, Hash: testStateHash(42)}
	owner, _ := ethrpc.ParseAddress("0x2222222222222222222222222222222222222222")
	erc721, _ := ethrpc.ParseAddress("0x1111111111111111111111111111111111111111")
	erc1155, _ := ethrpc.ParseAddress("0x3333333333333333333333333333333333333333")
	var calls []map[string]any
	var selectors []rpc.BlockNumberOrHash
	service := &testStateRPC{callFn: func(call map[string]any, selector rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
		calls = append(calls, call)
		selectors = append(selectors, selector)
		data, _ := call["data"].(string)
		result := make([]byte, 32)
		switch {
		case strings.HasPrefix(data, "0x6352211e"):
			copy(result[12:], owner.Bytes())
		case strings.HasPrefix(data, "0x00fdd58e"):
			big.NewInt(987).FillBytes(result)
		default:
			return nil, errors.New("unexpected NFT call data")
		}
		return hexutil.Bytes(result), nil
	}}
	observer := &nftBatchObserver{}
	endpoint := newNFTBatchTestEndpoint(t, service, observer)
	results, err := callNFTBalanceBatch(t.Context(), endpoint, reference, owner, []parsedNFTCandidate{
		{standard: standardERC721, address: erc721, tokenID: big.NewInt(123)},
		{standard: standardERC1155, address: erc1155, tokenID: big.NewInt(456)},
	}, []int{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].index != 0 || results[0].balance.Balance != "1" ||
		results[0].ownerObservation == nil || !results[0].ownerObservation.Exists ||
		results[1].index != 1 || results[1].balance.Balance != "987" || results[1].ownerObservation != nil {
		t.Fatalf("batch results=%+v", results)
	}
	if len(calls) != 2 || calls[0]["to"] != erc721.Hex() || calls[1]["to"] != erc1155.Hex() {
		t.Fatalf("calls=%#v", calls)
	}
	for _, selector := range selectors {
		if !reflect.DeepEqual(selector, canonicalSelector(reference)) {
			t.Fatalf("selector=%#v", selector)
		}
	}
	if len(observer.observations) != 1 {
		t.Fatalf("RPC observations=%+v", observer.observations)
	}
	observation := observer.observations[0]
	if observation.Purpose != ethrpc.PurposeState || observation.Method != "eth_call" ||
		observation.BatchSize != 2 || observation.SuccessCount != 2 || observation.ErrorCount != 0 {
		t.Fatalf("RPC observation=%+v", observation)
	}
}

func TestNFTBalanceBatchTreatsERC721RevertAsZero(t *testing.T) {
	t.Parallel()
	reference := CanonicalRef{Number: 7, Hash: testStateHash(7)}
	owner, _ := ethrpc.ParseAddress("0x2222222222222222222222222222222222222222")
	erc721, _ := ethrpc.ParseAddress("0x1111111111111111111111111111111111111111")
	erc1155, _ := ethrpc.ParseAddress("0x3333333333333333333333333333333333333333")
	service := &testStateRPC{callFn: func(call map[string]any, _ rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
		data, _ := call["data"].(string)
		if strings.HasPrefix(data, "0x6352211e") {
			return nil, &testRPCError{code: 3, message: "execution reverted"}
		}
		result := make([]byte, 32)
		big.NewInt(7).FillBytes(result)
		return hexutil.Bytes(result), nil
	}}
	results, err := callNFTBalanceBatch(
		t.Context(),
		newNFTBatchTestEndpoint(t, service, nil),
		reference,
		owner,
		[]parsedNFTCandidate{
			{standard: standardERC721, address: erc721, tokenID: big.NewInt(1)},
			{standard: standardERC1155, address: erc1155, tokenID: big.NewInt(2)},
		},
		[]int{0, 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].balance.Balance != "0" || results[0].ownerObservation == nil ||
		results[0].ownerObservation.Exists || results[1].balance.Balance != "7" {
		t.Fatalf("batch results=%+v", results)
	}
}

func TestNFTBalanceBatchRejectsElementAndMalformedResults(t *testing.T) {
	t.Parallel()
	invalidOwner := make([]byte, 32)
	invalidOwner[0] = 1
	for _, test := range []struct {
		name     string
		standard catalogStandard
		result   []byte
		err      error
	}{
		{name: "non-revert element error", standard: standardERC721, err: errors.New("state unavailable")},
		{name: "invalid ERC-721 owner word", standard: standardERC721, result: invalidOwner},
		{name: "zero ERC-721 owner", standard: standardERC721, result: make([]byte, 32)},
		{name: "short ERC-1155 balance", standard: standardERC1155, result: []byte{1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			owner, _ := ethrpc.ParseAddress("0x2222222222222222222222222222222222222222")
			contract, _ := ethrpc.ParseAddress("0x1111111111111111111111111111111111111111")
			service := &testStateRPC{callFn: func(map[string]any, rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
				return hexutil.Bytes(test.result), test.err
			}}
			results, err := callNFTBalanceBatch(
				t.Context(),
				newNFTBatchTestEndpoint(t, service, nil),
				CanonicalRef{Number: 1, Hash: testStateHash(1)},
				owner,
				[]parsedNFTCandidate{{standard: test.standard, address: contract, tokenID: big.NewInt(1)}},
				[]int{0},
			)
			if !errors.Is(err, httpapi.ErrUnavailable) || results != nil {
				t.Fatalf("results=%+v error=%v", results, err)
			}
		})
	}
}

func TestNFTBalanceBatchRejectsTransportFailure(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	client, err := ethrpc.NewClient(t.Context(), server.URL, ethrpc.ClientOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	owner, _ := ethrpc.ParseAddress("0x2222222222222222222222222222222222222222")
	contract, _ := ethrpc.ParseAddress("0x1111111111111111111111111111111111111111")
	results, err := callNFTBalanceBatch(
		t.Context(),
		&ethrpc.Endpoint{Name: "transport", Client: client},
		CanonicalRef{Number: 1, Hash: testStateHash(1)},
		owner,
		[]parsedNFTCandidate{{standard: standardERC1155, address: contract, tokenID: big.NewInt(1)}},
		[]int{0},
	)
	if !errors.Is(err, httpapi.ErrUnavailable) || results != nil {
		t.Fatalf("results=%+v error=%v", results, err)
	}
}

func newNFTBatchTestEndpoint(
	t *testing.T,
	service *testStateRPC,
	observer ethrpc.Observer,
) *ethrpc.Endpoint {
	t.Helper()
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name:   "state",
		Client: newTestStateClient(t, service),
		Purposes: map[ethrpc.Purpose]bool{
			ethrpc.PurposeState: true,
		},
	}}, ethrpc.PoolOptions{Observer: observer})
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := pool.Acquire(ethrpc.PurposeState)
	if err != nil {
		t.Fatal(err)
	}
	return endpoint
}
