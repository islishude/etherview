package ethrpc

import "context"

// Observer receives only bounded architectural purpose/result labels. It
// never receives endpoint URLs, JSON-RPC methods, parameters, or errors.
type Observer interface {
	RecordRPC(purpose, result string)
}

type observedCaller struct {
	next     Caller
	purpose  Purpose
	observer Observer
}

func observeCaller(next Caller, purpose Purpose, observer Observer) Caller {
	if next == nil || observer == nil {
		return next
	}
	base := observedCaller{next: next, purpose: purpose, observer: observer}
	if batch, ok := next.(BatchCaller); ok {
		return &observedBatchCaller{observedCaller: base, batch: batch}
	}
	return &base
}

func (caller *observedCaller) Call(ctx context.Context, method string, params []any, result any) error {
	err := caller.next.Call(ctx, method, params, result)
	caller.record(err)
	return err
}

func (caller *observedCaller) record(err error) {
	result := "success"
	if err != nil {
		result = "error"
	}
	caller.observer.RecordRPC(string(caller.purpose), result)
}

type observedBatchCaller struct {
	observedCaller
	batch BatchCaller
}

func (caller *observedBatchCaller) BatchCall(ctx context.Context, elements []BatchElem) error {
	err := caller.batch.BatchCall(ctx, elements)
	if err != nil {
		for range elements {
			caller.record(err)
		}
		return err
	}
	for index := range elements {
		caller.record(elements[index].Error)
	}
	return nil
}
