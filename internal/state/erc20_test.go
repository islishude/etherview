package state

import (
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/httpapi"
)

func TestERC20BalancesUseOneExactEndpointAndBlockHash(t *testing.T) {
	t.Parallel()
	reference := CanonicalRef{Number: 42, Hash: testStateHash(42)}
	result := make([]byte, 32)
	new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)).FillBytes(result)
	service := &testStateRPC{callResult: result}
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
		Name: "state", Client: newTestStateClient(t, service),
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true},
	}}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	reconciler := &NFTReconciler{
		pool:      pool,
		canonical: testCanonical{reference: reference, canonical: true},
	}
	observations, err := reconciler.ERC20Balances(t.Context(), catalog.Snapshot{
		ChainID:     "1",
		BlockNumber: "42",
		BlockHash:   reference.Hash.Hex(),
	}, "0x2222222222222222222222222222222222222222", []catalog.ERC20BalanceCandidate{{
		TokenAddress: "0x1111111111111111111111111111111111111111",
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)).String()
	if len(observations) != 1 || observations[0].Balance != want ||
		observations[0].Confidence != catalog.NFTStateConfidenceRPCExact {
		t.Fatalf("observations=%+v", observations)
	}
	call := service.params[0].(map[string]any)
	if data, ok := call["data"].(string); !ok || !strings.HasPrefix(data, "0x70a08231") {
		t.Fatalf("balanceOf call=%#v", call)
	}
	if !reflect.DeepEqual(service.params[1], canonicalSelector(reference)) {
		t.Fatalf("selector=%#v", service.params[1])
	}
}

func TestERC20BalancesRejectMalformedRPCAndCanonicalChange(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		result    []byte
		canonical bool
		want      error
	}{
		{name: "malformed", result: []byte{1}, canonical: true, want: httpapi.ErrUnavailable},
		{name: "reorg", result: make([]byte, 32), canonical: false, want: httpapi.ErrNotReady},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &testStateRPC{callResult: test.result}
			pool, err := ethrpc.NewPool([]ethrpc.Endpoint{{
				Name: "state", Client: newTestStateClient(t, service),
				Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeState: true},
			}}, ethrpc.PoolOptions{})
			if err != nil {
				t.Fatal(err)
			}
			reconciler := &NFTReconciler{
				pool:      pool,
				canonical: testCanonical{canonical: test.canonical},
			}
			_, err = reconciler.ERC20Balances(t.Context(), catalog.Snapshot{
				ChainID: "1", BlockNumber: "1", BlockHash: testStateHash(1).Hex(),
			}, "0x2222222222222222222222222222222222222222", []catalog.ERC20BalanceCandidate{{
				TokenAddress: "0x1111111111111111111111111111111111111111",
			}})
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v want %v", err, test.want)
			}
		})
	}
}
