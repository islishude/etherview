package ethrpc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"
)

type fakeCaller struct {
	mu      sync.Mutex
	call    func(method string, params []any, result any) error
	batch   func(elements []BatchElem) error
	methods []string
}

func (f *fakeCaller) Call(_ context.Context, method string, params []any, result any) error {
	f.mu.Lock()
	f.methods = append(f.methods, method)
	f.mu.Unlock()
	return f.call(method, params, result)
}

func (f *fakeCaller) BatchCall(_ context.Context, elements []BatchElem) error {
	if f.batch == nil {
		return errors.New("unexpected batch call")
	}
	return f.batch(elements)
}

func (f *fakeCaller) called(method string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.methods, method)
}

func TestPoolSelectsOnlyPurposeEligibleEndpoints(t *testing.T) {
	t.Parallel()
	noop := &fakeCaller{call: func(string, []any, any) error { return nil }}
	pool, err := NewPool([]Endpoint{
		{Name: "head", Purposes: map[Purpose]bool{PurposeHead: true}, Client: noop},
		{Name: "archive", Purposes: map[Purpose]bool{PurposeHistory: true, PurposeState: true}, Client: noop},
	}, PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := pool.Acquire(PurposeState)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.Name != "archive" {
		t.Fatalf("endpoint = %q", endpoint.Name)
	}
	if _, err := pool.Acquire(PurposeTrace); err == nil {
		t.Fatal("Acquire(trace) succeeded without a trace endpoint")
	}
}

func TestPoolSkipsCoolingEndpoint(t *testing.T) {
	t.Parallel()
	now := time.Unix(100, 0)
	noop := &fakeCaller{call: func(string, []any, any) error { return nil }}
	pool, err := NewPool([]Endpoint{
		{Name: "one", Purposes: map[Purpose]bool{PurposeHead: true}, Client: noop},
		{Name: "two", Purposes: map[Purpose]bool{PurposeHead: true}, Client: noop},
	}, PoolOptions{Now: func() time.Time { return now }, FailureCooldown: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := pool.Acquire(PurposeHead)
	pool.ReportFailure(first.Name)
	second, _ := pool.Acquire(PurposeHead)
	if second.Name == first.Name {
		t.Fatalf("cooling endpoint %q was selected while another was healthy", first.Name)
	}
}

func TestPoolExcludesEndpointFromHistoryWhenProbeMarkedItUnavailable(t *testing.T) {
	t.Parallel()
	noop := &fakeCaller{call: func(string, []any, any) error { return nil }}
	pool, err := NewPool([]Endpoint{{
		Name: "pruned",
		Purposes: map[Purpose]bool{
			PurposeHead:    true,
			PurposeHistory: true,
		},
		Client: noop,
		Capabilities: CapabilityReport{Methods: map[string]Availability{
			CapabilityHistoricalData: AvailabilityUnavailable,
		}},
	}}, PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if names := pool.Names(PurposeHistory); len(names) != 0 {
		t.Fatalf("history pool contains unavailable endpoint: %v", names)
	}
	if names := pool.Names(PurposeHead); len(names) != 1 || names[0] != "pruned" {
		t.Fatalf("unrelated head purpose was removed: %v", names)
	}
	head, err := pool.Acquire(PurposeHead)
	if err != nil {
		t.Fatal(err)
	}
	if head.Supports(PurposeHistory) {
		t.Fatal("endpoint acquired for head still advertises unavailable history")
	}
	if _, err := pool.Acquire(PurposeHistory); err == nil {
		t.Fatal("Acquire(history) succeeded with only an unavailable history endpoint")
	}
}

func TestPoolSkipsHistoryOnlyUnavailableEndpointWhenAlternativeExists(t *testing.T) {
	t.Parallel()
	noop := &fakeCaller{call: func(string, []any, any) error { return nil }}
	pool, err := NewPool([]Endpoint{
		{
			Name: "pruned", Purposes: map[Purpose]bool{PurposeHistory: true}, Client: noop,
			Capabilities: CapabilityReport{Methods: map[string]Availability{
				CapabilityHistoricalData: AvailabilityUnavailable,
			}},
		},
		{
			Name: "archive", Purposes: map[Purpose]bool{PurposeHistory: true}, Client: noop,
			Capabilities: CapabilityReport{Methods: map[string]Availability{
				CapabilityHistoricalData: AvailabilityAvailable,
			}},
		},
	}, PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if names := pool.Names(PurposeHistory); len(names) != 1 || names[0] != "archive" {
		t.Fatalf("history endpoints = %v", names)
	}
}

func TestProbeEndpointChecksIdentityAndPurposeCapabilities(t *testing.T) {
	t.Parallel()
	genesisHash := testHash(1)
	caller := &fakeCaller{call: func(method string, params []any, result any) error {
		switch method {
		case "eth_chainId":
			return assignJSON(result, "0x1")
		case "eth_getBlockByNumber":
			return assignJSON(result, map[string]any{"number": params[0], "hash": genesisHash})
		case "eth_getBlockReceipts":
			return assignJSON(result, []any{})
		default:
			return fmt.Errorf("unexpected method %s", method)
		}
	}}
	endpoint := &Endpoint{
		Name:     "history",
		Purposes: map[Purpose]bool{PurposeHistory: true},
		Client:   caller,
	}
	report, err := ProbeEndpoint(context.Background(), endpoint, ProbeOptions{
		Expected:   &ChainIdentity{ChainID: "1", GenesisHash: genesisHash},
		StartBlock: QuantityFromUint64(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status(CapabilityHistoricalData) != AvailabilityAvailable || report.Status(CapabilityBlockReceipts) != AvailabilityAvailable {
		t.Fatalf("capabilities = %+v", report.Methods)
	}
	if caller.called("rpc_modules") || caller.called("eth_getBalance") {
		t.Fatalf("probe called methods outside endpoint purposes: %v", caller.methods)
	}
}

func TestProbeEndpointRejectsWrongGenesis(t *testing.T) {
	t.Parallel()
	caller := &fakeCaller{call: func(method string, _ []any, result any) error {
		switch method {
		case "eth_chainId":
			return assignJSON(result, "0x1")
		case "eth_getBlockByNumber":
			return assignJSON(result, map[string]any{"number": "0x0", "hash": testHash(2)})
		default:
			return nil
		}
	}}
	endpoint := &Endpoint{Name: "wrong", Purposes: map[Purpose]bool{PurposeHistory: true}, Client: caller}
	_, err := ProbeEndpoint(context.Background(), endpoint, ProbeOptions{Expected: &ChainIdentity{ChainID: "1", GenesisHash: testHash(1)}})
	var mismatch *IdentityMismatchError
	if !errors.As(err, &mismatch) || mismatch.Field != "genesis_hash" {
		t.Fatalf("error = %v", err)
	}
}

func TestProbeEndpointRejectsWrongChainID(t *testing.T) {
	t.Parallel()
	caller := &fakeCaller{call: func(method string, _ []any, result any) error {
		switch method {
		case "eth_chainId":
			return assignJSON(result, "0x2")
		case "eth_getBlockByNumber":
			return assignJSON(result, map[string]any{"number": "0x0", "hash": testHash(1)})
		default:
			return fmt.Errorf("unexpected method %s", method)
		}
	}}
	endpoint := &Endpoint{Name: "wrong", Purposes: map[Purpose]bool{PurposeHistory: true}, Client: caller}
	_, err := ProbeEndpoint(context.Background(), endpoint, ProbeOptions{
		Expected: &ChainIdentity{ChainID: "1", GenesisHash: testHash(1)},
	})
	var mismatch *IdentityMismatchError
	if !errors.As(err, &mismatch) || mismatch.Field != "chain_id" || mismatch.Actual != "2" {
		t.Fatalf("error = %v", err)
	}
}

func TestProbeEndpointClassifiesUnavailableAndPrunedHistory(t *testing.T) {
	t.Parallel()
	start := QuantityFromUint64(64)
	tests := []struct {
		name       string
		historyErr error
		nullResult bool
		wantKind   HistoryUnavailableKind
		wantPruned bool
	}{
		{name: "null", nullResult: true, wantKind: HistoryUnavailableResult},
		{
			name: "explicit-pruned",
			historyErr: &RPCError{
				Code: -32000, Message: "requested historical data has been pruned",
			},
			wantKind: HistoryPrunedResult, wantPruned: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			caller := historyProbeCaller(start, func(result any) error {
				if test.nullResult {
					return assignJSON(result, nil)
				}
				return test.historyErr
			})
			report, err := ProbeEndpoint(context.Background(), &Endpoint{
				Name: "history", Purposes: map[Purpose]bool{PurposeHistory: true}, Client: caller,
			}, ProbeOptions{
				Expected:   &ChainIdentity{ChainID: "1", GenesisHash: testHash(1)},
				StartBlock: start,
			})
			if err != nil {
				t.Fatal(err)
			}
			issue := report.HistoryUnavailable
			if report.Status(CapabilityHistoricalData) != AvailabilityUnavailable || issue == nil {
				t.Fatalf("report = %+v", report)
			}
			if issue.Kind != test.wantKind || issue.StartBlock != start ||
				!errors.Is(issue, ErrHistoryUnavailable) || errors.Is(issue, ErrHistoryPruned) != test.wantPruned {
				t.Fatalf("history issue = %#v, error = %v", issue, issue)
			}
		})
	}
}

func TestProbeEndpointClassifiesUnavailableGenesisWhenConfiguredStartIsZero(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		genesisErr error
		nullResult bool
		wantPruned bool
	}{
		{name: "null", nullResult: true},
		{
			name: "pruned", genesisErr: &RPCError{
				Code: -32000, Message: "genesis history is pruned",
			}, wantPruned: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			caller := &fakeCaller{call: func(method string, _ []any, result any) error {
				switch method {
				case "eth_chainId":
					return assignJSON(result, "0x1")
				case "eth_getBlockByNumber":
					if test.nullResult {
						return assignJSON(result, nil)
					}
					return test.genesisErr
				default:
					return fmt.Errorf("unexpected method %s", method)
				}
			}}
			report, err := ProbeEndpoint(context.Background(), &Endpoint{
				Name: "history", Purposes: map[Purpose]bool{PurposeHistory: true}, Client: caller,
			}, ProbeOptions{Expected: &ChainIdentity{ChainID: "1"}})
			var issue *HistoryUnavailableError
			if !errors.As(err, &issue) || !errors.Is(err, ErrHistoryUnavailable) ||
				errors.Is(err, ErrHistoryPruned) != test.wantPruned {
				t.Fatalf("error = %v", err)
			}
			if report.Status(CapabilityHistoricalData) != AvailabilityUnavailable ||
				report.HistoryUnavailable == nil || report.HistoryUnavailable.StartBlock != QuantityFromUint64(0) {
				t.Fatalf("report = %+v", report)
			}
		})
	}
}

func TestProbeEndpointLeavesTransientHistoryFailuresUnknown(t *testing.T) {
	t.Parallel()
	start := QuantityFromUint64(64)
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "deadline", err: context.DeadlineExceeded},
		{name: "throttled", err: &HTTPStatusError{StatusCode: http.StatusTooManyRequests}},
		{name: "server-error", err: &HTTPStatusError{StatusCode: http.StatusServiceUnavailable}},
		{name: "generic", err: errors.New("temporary pruned-cache lookup timeout")},
		{name: "rpc-retry", err: &RPCError{Code: -32000, Message: "history pruning in progress; retry later"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			report, err := ProbeEndpoint(context.Background(), &Endpoint{
				Name: "history", Purposes: map[Purpose]bool{PurposeHistory: true},
				Client: historyProbeCaller(start, func(any) error { return test.err }),
			}, ProbeOptions{
				Expected:   &ChainIdentity{ChainID: "1", GenesisHash: testHash(1)},
				StartBlock: start,
			})
			if err != nil {
				t.Fatal(err)
			}
			if report.Status(CapabilityHistoricalData) != AvailabilityUnknown || report.HistoryUnavailable != nil {
				t.Fatalf("transient error %T was misclassified: %+v", test.err, report)
			}
		})
	}
}

func historyProbeCaller(start Quantity, historical func(any) error) *fakeCaller {
	return &fakeCaller{call: func(method string, params []any, result any) error {
		switch method {
		case "eth_chainId":
			return assignJSON(result, "0x1")
		case "eth_getBlockByNumber":
			if len(params) == 0 || params[0] == "0x0" {
				return assignJSON(result, map[string]any{"number": "0x0", "hash": testHash(1)})
			}
			if params[0] != start.String() {
				return fmt.Errorf("unexpected historical block %v", params[0])
			}
			return historical(result)
		case "eth_getBlockReceipts":
			return assignJSON(result, []any{})
		default:
			return fmt.Errorf("unexpected method %s", method)
		}
	}}
}

func TestFetcherFallsBackToBatchReceiptsOnMethodNotFound(t *testing.T) {
	t.Parallel()
	bundle := testBundle(1, testHash(2), testHash(1), 2)
	caller := &fakeCaller{}
	caller.call = func(method string, _ []any, result any) error {
		switch method {
		case "eth_getBlockByNumber":
			return assignJSON(result, bundle.Block)
		case "eth_getBlockReceipts":
			return &RPCError{Code: -32601, Message: "method not found"}
		default:
			return fmt.Errorf("unexpected call %s", method)
		}
	}
	caller.batch = func(elements []BatchElem) error {
		if len(elements) != len(bundle.Receipts) {
			return fmt.Errorf("batch size = %d", len(elements))
		}
		for index := range elements {
			if err := assignJSON(elements[index].Result, bundle.Receipts[index]); err != nil {
				return err
			}
		}
		return nil
	}
	endpoint := &Endpoint{Name: "rpc-a", Purposes: map[Purpose]bool{PurposeHistory: true}, Client: caller}
	fetched, err := (Fetcher{}).ByNumber(context.Background(), endpoint, QuantityFromUint64(1))
	if err != nil {
		t.Fatal(err)
	}
	if len(fetched.Receipts) != 2 {
		t.Fatalf("receipt count = %d", len(fetched.Receipts))
	}
}

func TestFetcherDoesNotMaskTransientBlockReceiptFailure(t *testing.T) {
	t.Parallel()
	bundle := testBundle(1, testHash(2), testHash(1), 0)
	caller := &fakeCaller{call: func(method string, _ []any, result any) error {
		switch method {
		case "eth_getBlockByNumber":
			return assignJSON(result, bundle.Block)
		case "eth_getBlockReceipts":
			return errors.New("upstream timeout")
		default:
			return nil
		}
	}}
	endpoint := &Endpoint{Name: "rpc-a", Purposes: map[Purpose]bool{PurposeHistory: true}, Client: caller}
	_, err := (Fetcher{}).ByNumber(context.Background(), endpoint, QuantityFromUint64(1))
	if err == nil || !caller.called("eth_getBlockReceipts") {
		t.Fatalf("error = %v", err)
	}
}

func TestCapabilityReportCloneDoesNotAlias(t *testing.T) {
	t.Parallel()
	report := CapabilityReport{
		Methods: map[string]Availability{"x": AvailabilityAvailable},
		HistoryUnavailable: &HistoryUnavailableError{
			Kind: HistoryPrunedResult, StartBlock: QuantityFromUint64(7),
		},
		Warnings: []string{"a"},
	}
	clone := report.Clone()
	clone.Methods["x"] = AvailabilityUnavailable
	clone.HistoryUnavailable.Kind = HistoryUnavailableResult
	clone.Warnings[0] = "b"
	if report.Methods["x"] != AvailabilityAvailable ||
		report.HistoryUnavailable.Kind != HistoryPrunedResult || report.Warnings[0] != "a" {
		t.Fatalf("clone mutated original: %+v", report)
	}
}
