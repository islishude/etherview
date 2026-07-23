package enrich

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/islishude/etherview/internal/ethrpc"
)

type traceRPCInvocation struct {
	method string
	hash   string
}

type traceTestCaller struct {
	mu      sync.Mutex
	calls   []traceRPCInvocation
	handler func(method, hash string) (json.RawMessage, error)
}

func (caller *traceTestCaller) Call(_ context.Context, method string, params []any, result any) error {
	if len(params) == 0 {
		return fmt.Errorf("trace RPC %s has no transaction hash", method)
	}
	hash, ok := params[0].(string)
	if !ok {
		return fmt.Errorf("trace RPC %s hash is %T", method, params[0])
	}
	caller.mu.Lock()
	caller.calls = append(caller.calls, traceRPCInvocation{method: method, hash: hash})
	handler := caller.handler
	caller.mu.Unlock()
	if handler == nil {
		return errors.New("trace test caller has no handler")
	}
	raw, err := handler(method, hash)
	if err != nil {
		return err
	}
	pointer, ok := result.(*json.RawMessage)
	if !ok {
		return errors.New("trace result is not RawMessage")
	}
	*pointer = raw
	return nil
}

func callTracerRoot(from, to, value, input string) json.RawMessage {
	return json.RawMessage(`{
		"type":"CALL","from":"` + from + `","to":"` + to + `","value":"` + value + `",
		"gas":"0x100","gasUsed":"0x20","input":"` + input + `","output":"0x"
	}`)
}

func traceTransactionRow(index int64, hash Word) []driver.Value {
	return []driver.Value{index, hash[:], traceAddress1, traceAddress2, "0x5", "0x1234"}
}

func TestTraceRPCProcessorUsesOneEndpointAndPersistsNormalizedFrames(t *testing.T) {
	t.Parallel()
	job := Job{ID: "12", Stage: TraceStage, ChainID: "1", BlockHash: uintWord(12), BlockNumber: 12}
	txHash1, txHash2 := uintWord(120), uintWord(121)
	queryCount, insertedFrames := 0, 0
	stageWritten, journalWritten := false, false
	backend := &fakeSQLBackend{
		query: func(query string, _ []driver.NamedValue) (driver.Rows, error) {
			queryCount++
			switch {
			case strings.Contains(query, "SELECT EXISTS"):
				return &fakeSQLRows{columns: []string{"canonical"}, values: [][]driver.Value{{true}}}, nil
			case strings.Contains(query, "FROM transaction_inclusions"):
				return &fakeSQLRows{columns: []string{"tx_index", "tx_hash", "from", "to", "value", "input"}, values: [][]driver.Value{
					traceTransactionRow(0, txHash1), traceTransactionRow(1, txHash2),
				}}, nil
			case strings.Contains(query, "FOR KEY SHARE"):
				return &fakeSQLRows{columns: []string{"one"}, values: [][]driver.Value{{int64(1)}}}, nil
			case strings.Contains(query, "FROM durable_jobs"):
				return emptyReplayTargetRows(), nil
			default:
				return nil, fmt.Errorf("unexpected query: %s", query)
			}
		},
		exec: func(query string, arguments []driver.NamedValue) (driver.Result, error) {
			switch {
			case strings.Contains(query, "UPDATE durable_jobs"):
				return driver.RowsAffected(0), nil
			case strings.Contains(query, "DELETE FROM normalized_traces"):
			case strings.Contains(query, "INSERT INTO normalized_traces"):
				insertedFrames++
				if arguments[5].Value != "" || arguments[8].Value != "CALL" || arguments[12].Value != "5" {
					t.Errorf("unexpected normalized frame arguments: %+v", arguments)
				}
			case strings.Contains(query, "INSERT INTO block_stage_results"):
				stageWritten = true
			case strings.Contains(query, "INSERT INTO block_journals"):
				journalWritten = true
			default:
				return nil, fmt.Errorf("unexpected exec: %s", query)
			}
			return driver.RowsAffected(1), nil
		},
	}
	first := &traceTestCaller{handler: func(method, _ string) (json.RawMessage, error) {
		if method != "debug_traceTransaction" {
			return nil, fmt.Errorf("unexpected RPC method %s", method)
		}
		return callTracerRoot(traceAddress1, traceAddress2, "0x5", "0x1234"), nil
	}}
	second := &traceTestCaller{handler: first.handler}
	endpoints := []ethrpc.Endpoint{
		traceEndpoint("trace-a", first, ethrpc.AvailabilityAvailable, ethrpc.AvailabilityUnavailable),
		traceEndpoint("trace-b", second, ethrpc.AvailabilityAvailable, ethrpc.AvailabilityUnavailable),
	}
	pool, err := ethrpc.NewPool(endpoints, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := NewTraceRPCProcessor(openFakeSQLDB(t, backend), pool, TraceLimits{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := processor.Process(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != ResultComplete || result.Details["transactions"] != "2" || result.Details["frames"] != "2" || result.Details["source"] != string(TraceCallTracer) {
		t.Fatalf("result=%+v", result)
	}
	if queryCount != 4 || insertedFrames != 2 || !stageWritten || !journalWritten || len(first.calls) != 2 || len(second.calls) != 0 {
		t.Fatalf("queries=%d frames=%d stage=%v journal=%v first=%v second=%v", queryCount, insertedFrames, stageWritten, journalWritten, first.calls, second.calls)
	}
	if first.calls[0].hash != txHash1.String() || first.calls[1].hash != txHash2.String() {
		t.Fatalf("calls=%+v", first.calls)
	}
}

func TestTraceRPCProcessorEnforcesWholeBlockBudgets(t *testing.T) {
	t.Parallel()
	job := Job{ID: "block-budget", Stage: TraceStage, ChainID: "1", BlockHash: uintWord(122), BlockNumber: 122}
	txHash1, txHash2 := uintWord(1220), uintWord(1221)
	raw := json.RawMessage(`{
		"type":"CALL","from":"` + traceAddress1 + `","to":"` + traceAddress2 + `","value":"0x5",
		"gas":"0x100","gasUsed":"0x20","input":"0x1234","output":"0x","error":"x"
	}`)
	for _, test := range []struct {
		name   string
		change func(*TraceLimits)
	}{
		{name: "payload", change: func(limits *TraceLimits) { limits.MaxBlockPayloadBytes = len(raw)*2 - 1 }},
		{name: "frames", change: func(limits *TraceLimits) { limits.MaxBlockFrames = 1 }},
		{name: "data", change: func(limits *TraceLimits) { limits.MaxBlockDataBytes = 3 }},
		{name: "text", change: func(limits *TraceLimits) { limits.MaxBlockTextBytes = 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			backend := traceBlockReadBackend(txHash1, txHash2)
			caller := &traceTestCaller{handler: func(method, _ string) (json.RawMessage, error) {
				if method != "debug_traceTransaction" {
					return nil, fmt.Errorf("unexpected RPC method %s", method)
				}
				return raw, nil
			}}
			pool, err := ethrpc.NewPool([]ethrpc.Endpoint{
				traceEndpoint("trace-budget", caller, ethrpc.AvailabilityAvailable, ethrpc.AvailabilityUnavailable),
			}, ethrpc.PoolOptions{})
			if err != nil {
				t.Fatal(err)
			}
			limits := DefaultTraceLimits()
			test.change(&limits)
			processor, err := NewTraceRPCProcessor(openFakeSQLDB(t, backend), pool, limits)
			if err != nil {
				t.Fatal(err)
			}
			_, err = processor.Process(context.Background(), job)
			var classified stageError
			if !errors.Is(err, ErrTraceLimit) || !errors.As(err, &classified) || classified.kind != "permanent" {
				t.Fatalf("err=%#v", err)
			}
			if len(caller.calls) != 2 {
				t.Fatalf("calls=%+v", caller.calls)
			}
		})
	}
}

func TestTraceRPCProcessorAppliesBlockBudgetToTraceAPI(t *testing.T) {
	t.Parallel()
	job := Job{ID: "trace-api-budget", Stage: TraceStage, ChainID: "1", BlockHash: uintWord(123), BlockNumber: 123}
	txHash1, txHash2 := uintWord(1230), uintWord(1231)
	backend := traceBlockReadBackend(txHash1, txHash2)
	caller := &traceTestCaller{handler: func(method, hash string) (json.RawMessage, error) {
		if method != "trace_transaction" {
			return nil, fmt.Errorf("unexpected RPC method %s", method)
		}
		transactionHash, transactionIndex := txHash1, uint64(0)
		if hash == txHash2.String() {
			transactionHash, transactionIndex = txHash2, 1
		}
		return traceAPIRoot(t, job, transactionHash, transactionIndex), nil
	}}
	pool, _ := ethrpc.NewPool([]ethrpc.Endpoint{
		traceEndpoint("trace-api-budget", caller, ethrpc.AvailabilityUnavailable, ethrpc.AvailabilityAvailable),
	}, ethrpc.PoolOptions{})
	limits := DefaultTraceLimits()
	limits.MaxBlockFrames = 1
	processor, _ := NewTraceRPCProcessor(openFakeSQLDB(t, backend), pool, limits)
	_, err := processor.Process(context.Background(), job)
	if !errors.Is(err, ErrTraceLimit) {
		t.Fatalf("err=%v", err)
	}
}

func TestTraceRPCProcessorDoesNotResetBlockBudgetOnAdapterFallback(t *testing.T) {
	t.Parallel()
	job := Job{ID: "fallback-budget", Stage: TraceStage, ChainID: "1", BlockHash: uintWord(124), BlockNumber: 124}
	txHash1, txHash2 := uintWord(1240), uintWord(1241)
	backend := traceBlockReadBackend(txHash1, txHash2)
	caller := &traceTestCaller{handler: func(method, hash string) (json.RawMessage, error) {
		switch method {
		case "debug_traceTransaction":
			if hash == txHash2.String() {
				return nil, &ethrpc.RPCError{Code: -32601, Message: "method not found"}
			}
			return callTracerRoot(traceAddress1, traceAddress2, "0x5", "0x1234"), nil
		case "trace_transaction":
			transactionHash, transactionIndex := txHash1, uint64(0)
			if hash == txHash2.String() {
				transactionHash, transactionIndex = txHash2, 1
			}
			return traceAPIRoot(t, job, transactionHash, transactionIndex), nil
		default:
			return nil, fmt.Errorf("unexpected RPC method %s", method)
		}
	}}
	pool, _ := ethrpc.NewPool([]ethrpc.Endpoint{
		traceEndpoint("trace-fallback-budget", caller, ethrpc.AvailabilityAvailable, ethrpc.AvailabilityAvailable),
	}, ethrpc.PoolOptions{})
	limits := DefaultTraceLimits()
	// One callTracer frame plus two trace_transaction frames must count as
	// three units. A fallback-local budget would incorrectly accept this job.
	limits.MaxBlockFrames = 2
	processor, _ := NewTraceRPCProcessor(openFakeSQLDB(t, backend), pool, limits)
	_, err := processor.Process(context.Background(), job)
	if !errors.Is(err, ErrTraceLimit) {
		t.Fatalf("err=%v calls=%+v", err, caller.calls)
	}
}

func TestTraceRPCProcessorFallsBackToSameEndpointTraceAPI(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "method_not_found", err: &ethrpc.RPCError{Code: -32601, Message: "method not found"}},
		{name: "pruned", err: &ethrpc.RPCError{Code: -32000, Message: "historical state pruned"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			job := Job{ID: "fallback", Stage: TraceStage, ChainID: "1", BlockHash: uintWord(14), BlockNumber: 14}
			txHash := uintWord(140)
			backend := successfulTraceBackend(t, txHash)
			caller := &traceTestCaller{handler: func(method, _ string) (json.RawMessage, error) {
				if method == "debug_traceTransaction" {
					return nil, test.err
				}
				if method != "trace_transaction" {
					return nil, fmt.Errorf("unexpected RPC method %s", method)
				}
				identity := TraceIdentity{BlockHash: job.BlockHash, BlockNumber: job.BlockNumber, TransactionHash: txHash, TransactionIndex: 0}
				root := mergeTraceFixture(identity, map[string]any{
					"type": "call", "traceAddress": []uint64{}, "subtraces": 0,
					"action": map[string]any{"callType": "call", "from": traceAddress1, "to": traceAddress2, "value": "0x5", "gas": "0x100", "input": "0x1234"},
					"result": map[string]any{"gasUsed": "0x20", "output": "0x"},
				})
				return marshalTraceFixture(t, []any{root}), nil
			}}
			pool, err := ethrpc.NewPool([]ethrpc.Endpoint{
				traceEndpoint("trace-combined", caller, ethrpc.AvailabilityAvailable, ethrpc.AvailabilityAvailable),
			}, ethrpc.PoolOptions{})
			if err != nil {
				t.Fatal(err)
			}
			processor, err := NewTraceRPCProcessor(openFakeSQLDB(t, backend), pool, TraceLimits{})
			if err != nil {
				t.Fatal(err)
			}
			result, err := processor.Process(context.Background(), job)
			if err != nil {
				t.Fatal(err)
			}
			methods := make([]string, len(caller.calls))
			for index := range caller.calls {
				methods[index] = caller.calls[index].method
			}
			if result.Details["source"] != string(TraceAPI) || !reflect.DeepEqual(methods, []string{"debug_traceTransaction", "trace_transaction"}) {
				t.Fatalf("result=%+v methods=%v", result, methods)
			}
		})
	}
}

func TestTraceRPCProcessorRejectsEmptyOrMismatchedTrace(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		raw  json.RawMessage
	}{
		{name: "empty_trace_api", raw: json.RawMessage(`[]`)},
		{name: "mismatched_call_root", raw: callTracerRoot(traceAddress3, traceAddress2, "0x5", "0x1234")},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			job := Job{ID: "invalid", Stage: TraceStage, ChainID: "1", BlockHash: uintWord(15), BlockNumber: 15}
			txHash := uintWord(150)
			backend := traceReadBackend(txHash)
			caller := &traceTestCaller{handler: func(method, _ string) (json.RawMessage, error) {
				if test.name == "empty_trace_api" && method == "debug_traceTransaction" {
					return nil, &ethrpc.RPCError{Code: -32601, Message: "method not found"}
				}
				return test.raw, nil
			}}
			parity := ethrpc.AvailabilityUnavailable
			if test.name == "empty_trace_api" {
				parity = ethrpc.AvailabilityAvailable
			}
			pool, _ := ethrpc.NewPool([]ethrpc.Endpoint{
				traceEndpoint("trace-invalid", caller, ethrpc.AvailabilityAvailable, parity),
			}, ethrpc.PoolOptions{})
			processor, _ := NewTraceRPCProcessor(openFakeSQLDB(t, backend), pool, TraceLimits{})
			_, err := processor.Process(context.Background(), job)
			var classified stageError
			if !errors.As(err, &classified) || classified.kind != "permanent" {
				t.Fatalf("err=%#v", err)
			}
		})
	}
}

func TestTraceRPCProcessorReportsMissingCapabilityUnavailable(t *testing.T) {
	t.Parallel()
	job := Job{ID: "13", Stage: TraceStage, ChainID: "1", BlockHash: uintWord(13), BlockNumber: 13}
	txHash := uintWord(130)
	backend := traceReadBackend(txHash)
	caller := &traceTestCaller{handler: func(string, string) (json.RawMessage, error) {
		return nil, errors.New("RPC must not be called")
	}}
	pool, _ := ethrpc.NewPool([]ethrpc.Endpoint{
		traceEndpoint("trace-disabled", caller, ethrpc.AvailabilityUnavailable, ethrpc.AvailabilityUnavailable),
	}, ethrpc.PoolOptions{})
	processor, _ := NewTraceRPCProcessor(openFakeSQLDB(t, backend), pool, TraceLimits{})
	_, err := processor.Process(context.Background(), job)
	var classified stageError
	if !errors.As(err, &classified) || classified.kind != "unavailable" || len(caller.calls) != 0 {
		t.Fatalf("err=%v calls=%v", err, caller.calls)
	}
}

func TestTraceCapabilityClassificationKeepsMissingTransactionRetryable(t *testing.T) {
	t.Parallel()
	err := &ethrpc.RPCError{Code: -32000, Message: "transaction not found"}
	if traceCapabilityUnavailable(err) || traceAdapterFallback(err) {
		t.Fatal("a temporarily missing transaction was classified as a terminal trace capability gap")
	}
}

func traceEndpoint(name string, caller ethrpc.Caller, debug, parity ethrpc.Availability) ethrpc.Endpoint {
	return ethrpc.Endpoint{
		Name: name, Client: caller, Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeTrace: true},
		Capabilities: ethrpc.CapabilityReport{Methods: map[string]ethrpc.Availability{
			ethrpc.CapabilityDebugTrace: debug, ethrpc.CapabilityParityTrace: parity,
		}},
	}
}

func traceReadBackend(txHash Word) *fakeSQLBackend {
	return &fakeSQLBackend{query: func(query string, _ []driver.NamedValue) (driver.Rows, error) {
		switch {
		case strings.Contains(query, "SELECT EXISTS"):
			return &fakeSQLRows{columns: []string{"canonical"}, values: [][]driver.Value{{true}}}, nil
		case strings.Contains(query, "FROM transaction_inclusions"):
			return &fakeSQLRows{columns: []string{"tx_index", "tx_hash", "from", "to", "value", "input"}, values: [][]driver.Value{traceTransactionRow(0, txHash)}}, nil
		default:
			return nil, fmt.Errorf("unexpected query: %s", query)
		}
	}}
}

func traceBlockReadBackend(transactionHashes ...Word) *fakeSQLBackend {
	return &fakeSQLBackend{query: func(query string, _ []driver.NamedValue) (driver.Rows, error) {
		switch {
		case strings.Contains(query, "SELECT EXISTS"):
			return &fakeSQLRows{columns: []string{"canonical"}, values: [][]driver.Value{{true}}}, nil
		case strings.Contains(query, "FROM transaction_inclusions"):
			values := make([][]driver.Value, len(transactionHashes))
			for index, hash := range transactionHashes {
				values[index] = traceTransactionRow(int64(index), hash)
			}
			return &fakeSQLRows{columns: []string{"tx_index", "tx_hash", "from", "to", "value", "input"}, values: values}, nil
		default:
			return nil, fmt.Errorf("unexpected query: %s", query)
		}
	}}
}

func traceAPIRoot(t *testing.T, job Job, transactionHash Word, transactionIndex uint64) json.RawMessage {
	t.Helper()
	identity := TraceIdentity{
		BlockHash: job.BlockHash, BlockNumber: job.BlockNumber,
		TransactionHash: transactionHash, TransactionIndex: transactionIndex,
	}
	root := mergeTraceFixture(identity, map[string]any{
		"type": "call", "traceAddress": []uint64{}, "subtraces": 0,
		"action": map[string]any{
			"callType": "call", "from": traceAddress1, "to": traceAddress2,
			"value": "0x5", "gas": "0x100", "input": "0x1234",
		},
		"result": map[string]any{"gasUsed": "0x20", "output": "0x"},
	})
	return marshalTraceFixture(t, []any{root})
}

func successfulTraceBackend(t *testing.T, txHash Word) *fakeSQLBackend {
	t.Helper()
	backend := traceReadBackend(txHash)
	backend.query = func(original func(string, []driver.NamedValue) (driver.Rows, error)) func(string, []driver.NamedValue) (driver.Rows, error) {
		return func(query string, arguments []driver.NamedValue) (driver.Rows, error) {
			if strings.Contains(query, "FOR KEY SHARE") {
				return &fakeSQLRows{columns: []string{"one"}, values: [][]driver.Value{{int64(1)}}}, nil
			}
			if strings.Contains(query, "FROM durable_jobs") {
				return emptyReplayTargetRows(), nil
			}
			return original(query, arguments)
		}
	}(backend.query)
	backend.exec = func(query string, _ []driver.NamedValue) (driver.Result, error) {
		switch {
		case strings.Contains(query, "UPDATE durable_jobs"):
			return driver.RowsAffected(0), nil
		case strings.Contains(query, "DELETE FROM normalized_traces"),
			strings.Contains(query, "INSERT INTO normalized_traces"),
			strings.Contains(query, "INSERT INTO block_stage_results"),
			strings.Contains(query, "INSERT INTO block_journals"):
			return driver.RowsAffected(1), nil
		default:
			return nil, fmt.Errorf("unexpected exec: %s", query)
		}
	}
	return backend
}
