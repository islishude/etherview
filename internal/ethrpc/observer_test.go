package ethrpc

import (
	"context"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
)

type metricService struct{}

func (metricService) BlockNumber(context.Context) (hexutil.Uint64, error) {
	return hexutil.Uint64(1), nil
}

type metricObserver struct{ values map[string]int }

func (observer *metricObserver) RecordRPC(purpose, result string) {
	observer.values[purpose+":"+result]++
}

func TestEndpointObservesRPCOutcomesByAcquiredPurpose(t *testing.T) {
	server := rpc.NewServer()
	if err := server.RegisterName("eth", metricService{}); err != nil {
		t.Fatal(err)
	}
	client := rpc.DialInProc(server)
	t.Cleanup(client.Close)
	observer := &metricObserver{values: make(map[string]int)}
	pool, err := NewPool([]Endpoint{{
		Name: "primary", Client: client,
		Purposes: map[Purpose]bool{PurposeHead: true, PurposeTrace: true},
	}}, PoolOptions{Observer: observer})
	if err != nil {
		t.Fatal(err)
	}
	head, err := pool.Acquire(PurposeHead)
	if err != nil {
		t.Fatal(err)
	}
	var number hexutil.Uint64
	if err := head.CallContext(t.Context(), &number, "eth_blockNumber"); err != nil {
		t.Fatal(err)
	}
	trace, err := pool.Acquire(PurposeTrace)
	if err != nil {
		t.Fatal(err)
	}
	var first, second string
	elements := []rpc.BatchElem{
		{Method: "eth_blockNumber", Result: &first},
		{Method: "eth_missing", Result: &second},
	}
	if err := trace.BatchCallContext(t.Context(), elements); err != nil {
		t.Fatal(err)
	}
	if elements[0].Error != nil || elements[1].Error == nil {
		t.Fatalf("batch element errors = %v, %v", elements[0].Error, elements[1].Error)
	}
	if observer.values["head:success"] != 1 ||
		observer.values["trace:success"] != 1 ||
		observer.values["trace:error"] != 1 {
		t.Fatalf("observations = %#v", observer.values)
	}
}

func TestNilEndpointMethodsFailWithoutPanic(t *testing.T) {
	t.Parallel()
	var endpoint *Endpoint
	if err := endpoint.CallContext(t.Context(), nil, "eth_test"); err == nil {
		t.Fatal("nil endpoint call succeeded")
	}
	if err := endpoint.BatchCallContext(t.Context(), nil); err == nil {
		t.Fatal("nil endpoint batch succeeded")
	}
}
