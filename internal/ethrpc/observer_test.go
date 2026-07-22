package ethrpc

import (
	"context"
	"errors"
	"testing"
)

type metricCaller struct {
	callErr  error
	batchErr error
}

func (caller *metricCaller) Call(context.Context, string, []any, any) error { return caller.callErr }

func (caller *metricCaller) BatchCall(_ context.Context, elements []BatchElem) error {
	if caller.batchErr == nil && len(elements) > 1 {
		elements[1].Error = errors.New("controlled element failure")
	}
	return caller.batchErr
}

type metricObserver struct{ values map[string]int }

func (observer *metricObserver) RecordRPC(purpose, result string) {
	observer.values[purpose+":"+result]++
}

func TestPoolObservesRPCOutcomesByAcquiredPurpose(t *testing.T) {
	observer := &metricObserver{values: make(map[string]int)}
	next := &metricCaller{}
	pool, err := NewPool([]Endpoint{{
		Name: "primary", Client: next,
		Purposes: map[Purpose]bool{PurposeHead: true, PurposeTrace: true},
	}}, PoolOptions{Observer: observer})
	if err != nil {
		t.Fatal(err)
	}
	head, err := pool.Acquire(PurposeHead)
	if err != nil {
		t.Fatal(err)
	}
	if err := head.Client.Call(t.Context(), "eth_blockNumber", nil, new(Quantity)); err != nil {
		t.Fatal(err)
	}
	trace, err := pool.Acquire(PurposeTrace)
	if err != nil {
		t.Fatal(err)
	}
	batch, ok := trace.Client.(BatchCaller)
	if !ok {
		t.Fatal("observed caller lost batch support")
	}
	elements := []BatchElem{{Result: new(string)}, {Result: new(string)}}
	if err := batch.BatchCall(t.Context(), elements); err != nil {
		t.Fatal(err)
	}
	if observer.values["head:success"] != 1 || observer.values["trace:success"] != 1 || observer.values["trace:error"] != 1 {
		t.Fatalf("unexpected observations: %#v", observer.values)
	}

	next.batchErr = errors.New("transport unavailable")
	if err := batch.BatchCall(t.Context(), elements); err == nil {
		t.Fatal("expected batch transport error")
	}
	if observer.values["trace:error"] != 3 {
		t.Fatalf("batch transport error was not counted per call: %#v", observer.values)
	}
}
