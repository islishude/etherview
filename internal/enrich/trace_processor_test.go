package enrich

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/ethrpc"
)

type traceRPCInvocation struct {
	method  string
	hash    string
	withLog *bool
}

type traceTestCaller struct {
	mu      sync.Mutex
	calls   []traceRPCInvocation
	handler func(method, hash string) (json.RawMessage, error)
}

type debugTraceRPCService struct {
	caller *traceTestCaller
}

type parityTraceRPCService struct {
	caller *traceTestCaller
}

func (caller *traceTestCaller) CallContext(_ context.Context, result any, method string, params ...any) error {
	if len(params) == 0 {
		return fmt.Errorf("trace RPC %s has no transaction hash", method)
	}
	hash, ok := params[0].(string)
	if !ok {
		return fmt.Errorf("trace RPC %s hash is %T", method, params[0])
	}
	var withLog *bool
	if len(params) > 1 {
		if options, ok := params[1].(map[string]any); ok {
			if tracerConfig, ok := options["tracerConfig"].(map[string]any); ok {
				if value, ok := tracerConfig["withLog"].(bool); ok {
					withLog = &value
				}
			}
		}
	}
	raw, err := caller.invoke(method, hash, withLog)
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

func (service debugTraceRPCService) TraceTransaction(
	_ context.Context,
	hash string,
	config map[string]any,
) (json.RawMessage, error) {
	var withLog *bool
	if tracerConfig, ok := config["tracerConfig"].(map[string]any); ok {
		if value, ok := tracerConfig["withLog"].(bool); ok {
			withLog = &value
		}
	}
	return service.caller.invoke("debug_traceTransaction", hash, withLog)
}

func (service parityTraceRPCService) Transaction(
	_ context.Context,
	hash string,
) (json.RawMessage, error) {
	return service.caller.invoke("trace_transaction", hash, nil)
}

func (caller *traceTestCaller) invoke(method, hash string, withLog *bool) (json.RawMessage, error) {
	caller.mu.Lock()
	caller.calls = append(caller.calls, traceRPCInvocation{method: method, hash: hash, withLog: withLog})
	handler := caller.handler
	caller.mu.Unlock()
	if handler == nil {
		return nil, errors.New("trace test caller has no handler")
	}
	return handler(method, hash)
}

func callTracerRoot(from string) json.RawMessage {
	return json.RawMessage(`{
		"type":"CALL","from":"` + from + `","to":"` + traceAddress2 + `","value":"0x5",
		"gas":"0x100","gasUsed":"0x20","input":"0x1234","output":"0x"
	}`)
}

func traceTransactionRow(index int64, hash common.Hash) []driver.Value {
	return []driver.Value{index, hash[:], traceAddress1, traceAddress2, "0x5", "0x1234"}
}

func TestTraceRPCProcessorUsesOneEndpointAndPersistsNormalizedFrames(t *testing.T) {
	t.Parallel()
	job := Job{ID: "12", Stage: TraceStage, ChainID: "1", BlockHash: uintWord(12), BlockNumber: 12}
	txHash1, txHash2 := uintWord(120), uintWord(121)
	queryCount, insertedFrames := 0, 0
	var replayStages []string
	stageWritten, journalWritten := false, false
	backend := &fakeSQLBackend{
		query: func(query string, arguments []driver.NamedValue) (driver.Rows, error) {
			queryCount++
			switch {
			case strings.Contains(query, "SELECT EXISTS"):
				return &fakeSQLRows{columns: []string{"canonical"}, values: [][]driver.Value{{true}}}, nil
			case strings.Contains(query, "FROM transaction_inclusions"):
				return &fakeSQLRows{columns: []string{"tx_index", "tx_hash", "from", "to", "value", "input"}, values: [][]driver.Value{
					traceTransactionRow(0, txHash1), traceTransactionRow(1, txHash2),
				}}, nil
			case strings.Contains(query, "FROM transaction_execution_code_resolutions"):
				return emptyExecutionResolutionRows(), nil
			case strings.Contains(query, "FROM logs"):
				return &fakeSQLRows{columns: []string{"log_index", "raw"}}, nil
			case strings.Contains(query, "FOR KEY SHARE"):
				return &fakeSQLRows{columns: []string{"one"}, values: [][]driver.Value{{int64(1)}}}, nil
			case strings.Contains(query, "FROM durable_jobs"):
				if len(arguments) >= 3 {
					replayStages = append(replayStages, fmt.Sprint(arguments[2].Value))
				}
				return emptyReplayTargetRows(), nil
			default:
				return nil, fmt.Errorf("unexpected query: %s", query)
			}
		},
		exec: func(query string, arguments []driver.NamedValue) (driver.Result, error) {
			switch {
			case strings.Contains(query, "UPDATE durable_jobs"):
				return driver.RowsAffected(0), nil
			case strings.Contains(query, "DELETE FROM trace_log_attributions"):
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
		return callTracerRoot(traceAddress1), nil
	}}
	second := &traceTestCaller{handler: first.handler}
	endpoints := []ethrpc.Endpoint{
		traceEndpoint(t, "trace-a", first, ethrpc.AvailabilityAvailable, ethrpc.AvailabilityUnavailable),
		traceEndpoint(t, "trace-b", second, ethrpc.AvailabilityAvailable, ethrpc.AvailabilityUnavailable),
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
	if result.State != ResultComplete || result.Details["transactions"] != "2" ||
		result.Details["frames"] != "2" || result.Details["source"] != string(TraceCallTracer) ||
		result.Details["creation_targets"] != "0" ||
		result.Details["proxy_requeued"] != "false" ||
		result.Details["abi_requeued"] != "false" {
		t.Fatalf("result=%+v", result)
	}
	if queryCount != 8 || insertedFrames != 2 || !stageWritten || !journalWritten || len(first.calls) != 2 || len(second.calls) != 0 {
		t.Fatalf("queries=%d frames=%d stage=%v journal=%v first=%v second=%v", queryCount, insertedFrames, stageWritten, journalWritten, first.calls, second.calls)
	}
	if !reflect.DeepEqual(replayStages, []string{"proxy", "abi"}) {
		t.Fatalf("ordinary CALL replay stages = %v, want Proxy then ABI", replayStages)
	}
	if first.calls[0].hash != txHash1.String() || first.calls[1].hash != txHash2.String() {
		t.Fatalf("calls=%+v", first.calls)
	}
}

func TestCallTracerWithLogInvalidParamsDowngradesOncePerBlock(t *testing.T) {
	t.Parallel()
	from, to := common.HexToAddress(traceAddress1), common.HexToAddress(traceAddress2)
	transactions := []traceTransaction{
		{hash: uintWord(880), from: from, to: &to, value: "0x5", input: []byte{0x12, 0x34}},
		{hash: uintWord(881), from: from, to: &to, value: "0x5", input: []byte{0x12, 0x34}},
	}
	calls := 0
	caller := &traceTestCaller{handler: func(method, _ string) (json.RawMessage, error) {
		calls++
		if method != "debug_traceTransaction" {
			return nil, fmt.Errorf("unexpected method %s", method)
		}
		if calls == 1 {
			return nil, testRPCError{code: -32602, message: "unknown tracerConfig field withLog"}
		}
		return callTracerRoot(traceAddress1), nil
	}}
	processor := &TraceRPCProcessor{limits: DefaultTraceLimits()}
	if err := processor.fetchCallTracer(context.Background(), caller, transactions, newTraceBlockBudget(processor.limits)); err != nil {
		t.Fatal(err)
	}
	if len(caller.calls) != 3 || caller.calls[0].withLog == nil || !*caller.calls[0].withLog ||
		caller.calls[1].withLog == nil || *caller.calls[1].withLog ||
		caller.calls[2].withLog == nil || *caller.calls[2].withLog {
		t.Fatalf("calls=%+v", caller.calls)
	}
}

func TestTraceLogAttributionUsesExecutionFrameAndRejectsContradictions(t *testing.T) {
	t.Parallel()
	job := Job{ID: "trace-logs", Stage: TraceStage, ChainID: "1", BlockHash: uintWord(77), BlockNumber: 77}
	txHash := uintWord(770)
	emitter, implementation := testAddress(0xa1), testAddress(0xb2)
	topic := uintWord(123)
	stored := types.Log{
		Address: emitter, Topics: []common.Hash{topic}, Data: []byte{1, 2, 3},
		BlockNumber: job.BlockNumber, TxHash: txHash, TxIndex: 0,
		BlockHash: job.BlockHash, Index: 9,
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	storedSecond := stored
	storedSecond.Index = 10
	storedSecond.Topics = []common.Hash{uintWord(124)}
	rawSecond, err := json.Marshal(storedSecond)
	if err != nil {
		t.Fatal(err)
	}
	baseFrame := CallFrame{
		Type: "DELEGATECALL", To: &emitter, CodeAddress: &implementation, TraceAddress: []uint32{2, 1},
		ExecutionAddress: &implementation, ExecutionCodeHash: &topic, ExecutionResolution: "direct",
		Logs: []TraceLog{{Address: emitter, Topics: []common.Hash{topic}, Data: []byte{1, 2, 3}, Index: 9}},
	}
	addressExactFrame := baseFrame
	addressExactFrame.ExecutionCodeHash = nil
	addressExactFrame.ExecutionResolution = "unavailable"
	missingCodeFrame := baseFrame
	missingCodeFrame.ExecutionCodeHash = nil
	for _, test := range []struct {
		name       string
		frame      CallFrame
		expected   int
		attributed bool
		wantError  bool
		fallback   int
	}{
		{name: "exact delegate execution", frame: baseFrame, expected: 1, attributed: true},
		{name: "exact delegate address with unavailable code hash", frame: addressExactFrame, expected: 1, attributed: true},
		{name: "direct execution missing code hash falls back", frame: missingCodeFrame, expected: 1, fallback: 1},
		{name: "missing withLog falls back", frame: CallFrame{Type: "DELEGATECALL", To: &implementation}, expected: 1, fallback: 1},
		{name: "partial response", frame: baseFrame, expected: 2, wantError: true},
		{name: "duplicate index", frame: func() CallFrame {
			frame := baseFrame
			frame.Logs = append(append([]TraceLog(nil), baseFrame.Logs...), baseFrame.Logs[0])
			return frame
		}(), expected: 2, wantError: true},
		{name: "staticcall log", frame: func() CallFrame { frame := baseFrame; frame.Type = "STATICCALL"; return frame }(), expected: 1, wantError: true},
		{name: "no receipt logs", frame: CallFrame{Type: "CALL", To: &implementation}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			backend := &fakeSQLBackend{query: func(query string, _ []driver.NamedValue) (driver.Rows, error) {
				if !strings.Contains(query, "FROM logs") {
					return nil, fmt.Errorf("unexpected query: %s", query)
				}
				rows := [][]driver.Value{}
				if test.expected >= 1 {
					rows = append(rows, []driver.Value{int64(stored.Index), raw})
				}
				if test.expected >= 2 {
					rows = append(rows, []driver.Value{int64(storedSecond.Index), rawSecond})
				}
				return &fakeSQLRows{columns: []string{"log_index", "raw"}, values: rows}, nil
			}}
			db := openFakeSQLDB(t, backend)
			tx, err := db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback() //nolint:errcheck
			attributions, fallback, err := loadTraceLogAttributions(context.Background(), tx, job, traceTransaction{
				hash: txHash, trace: NormalizedTrace{Frames: []CallFrame{test.frame}},
			})
			if test.wantError {
				var classified stageError
				if !errors.As(err, &classified) || classified.kind != "permanent" {
					t.Fatalf("err=%#v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if test.attributed {
				if fallback != 0 || len(attributions) != 1 || attributions[0].tracePath != "2.1" ||
					attributions[0].callType != "DELEGATECALL" || attributions[0].executionAddress != implementation {
					t.Fatalf("attributions=%+v fallback=%d", attributions, fallback)
				}
			} else if len(attributions) != 0 || fallback != test.fallback {
				t.Fatalf("fallback attributions=%+v fallback=%d", attributions, fallback)
			}
		})
	}
}

func TestABILogsUseAttributedEIP7702ExecutionAddressWithoutStoredCodeHash(t *testing.T) {
	t.Parallel()
	job := Job{ID: "abi-7702-log", Stage: ABIStage, ChainID: "1", BlockHash: uintWord(78), BlockNumber: 78}
	transactionHash := uintWord(780)
	emitter, delegate := testAddress(0xa1), testAddress(0xb2)
	stored := types.Log{
		Address: emitter, Topics: []common.Hash{uintWord(123)}, Data: []byte{1, 2, 3},
		BlockNumber: job.BlockNumber, TxHash: transactionHash, TxIndex: 0,
		BlockHash: job.BlockHash, Index: 9,
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	backend := &fakeSQLBackend{query: func(query string, _ []driver.NamedValue) (driver.Rows, error) {
		if !strings.Contains(query, "FROM logs AS log") {
			return nil, fmt.Errorf("unexpected query: %s", query)
		}
		return &fakeSQLRows{
			columns: []string{"log_index", "tx_hash", "address", "raw", "execution_address"},
			values:  [][]driver.Value{{int64(9), transactionHash[:], emitter[:], raw, delegate[:]}},
		}, nil
	}}
	db := openFakeSQLDB(t, backend)
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback() //nolint:errcheck
	observations, err := loadABILogs(context.Background(), tx, job)
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 1 || observations[0].target != delegate ||
		observations[0].transactionHash != transactionHash || observations[0].objectIndex != "9" {
		t.Fatalf("observations=%+v", observations)
	}
}

func TestSuccessfulCreationTargetsRejectsCallsAndRevertedCreations(t *testing.T) {
	t.Parallel()
	created := common.Address{19: 1}
	tests := []struct {
		name  string
		frame CallFrame
		want  int
	}{
		{name: "ordinary call", frame: CallFrame{Type: "CALL", To: &created}},
		{name: "reverted create", frame: CallFrame{Type: "CREATE", To: &created, Reverted: true}},
		{name: "reverted create2", frame: CallFrame{Type: "CREATE2", To: &created, Reverted: true}},
		{name: "missing created address", frame: CallFrame{Type: "CREATE"}},
		{name: "successful create", frame: CallFrame{Type: "CREATE", To: &created}, want: 1},
		{name: "successful create2", frame: CallFrame{Type: "CREATE2", To: &created}, want: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			transactions := []traceTransaction{{trace: NormalizedTrace{Frames: []CallFrame{test.frame}}}}
			if got := successfulCreationTargets(transactions); got != test.want {
				t.Fatalf("successful creation targets = %d, want %d", got, test.want)
			}
		})
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
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*TraceLimits)
	}{
		{name: "payload", change: func(limits *TraceLimits) { limits.MaxBlockPayloadBytes = compact.Len()*2 - 1 }},
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
				traceEndpoint(t, "trace-budget", caller, ethrpc.AvailabilityAvailable, ethrpc.AvailabilityUnavailable),
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
		traceEndpoint(t, "trace-api-budget", caller, ethrpc.AvailabilityUnavailable, ethrpc.AvailabilityAvailable),
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
				return nil, testRPCError{code: -32601, message: "method not found"}
			}
			return callTracerRoot(traceAddress1), nil
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
		traceEndpoint(t, "trace-fallback-budget", caller, ethrpc.AvailabilityAvailable, ethrpc.AvailabilityAvailable),
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
		{name: "method_not_found", err: testRPCError{code: -32601, message: "method not found"}},
		{name: "pruned", err: testRPCError{code: -32000, message: "historical state pruned"}},
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
				traceEndpoint(t, "trace-combined", caller, ethrpc.AvailabilityAvailable, ethrpc.AvailabilityAvailable),
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
		{name: "mismatched_call_root", raw: callTracerRoot(traceAddress3)},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			job := Job{ID: "invalid", Stage: TraceStage, ChainID: "1", BlockHash: uintWord(15), BlockNumber: 15}
			txHash := uintWord(150)
			backend := traceReadBackend(txHash)
			caller := &traceTestCaller{handler: func(method, _ string) (json.RawMessage, error) {
				if test.name == "empty_trace_api" && method == "debug_traceTransaction" {
					return nil, testRPCError{code: -32601, message: "method not found"}
				}
				return test.raw, nil
			}}
			parity := ethrpc.AvailabilityUnavailable
			if test.name == "empty_trace_api" {
				parity = ethrpc.AvailabilityAvailable
			}
			pool, _ := ethrpc.NewPool([]ethrpc.Endpoint{
				traceEndpoint(t, "trace-invalid", caller, ethrpc.AvailabilityAvailable, parity),
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
		traceEndpoint(t, "trace-disabled", caller, ethrpc.AvailabilityUnavailable, ethrpc.AvailabilityUnavailable),
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
	err := testRPCError{code: -32000, message: "transaction not found"}
	if traceCapabilityUnavailable(err) || traceAdapterFallback(err) {
		t.Fatal("a temporarily missing transaction was classified as a terminal trace capability gap")
	}
}

func TestTraceRPCProviderErrorIsSanitizedBeforeDurableRetry(t *testing.T) {
	t.Parallel()
	const secret = "trace-provider-secret"
	job := Job{
		ID:          "trace-provider-error",
		Stage:       TraceStage,
		ChainID:     "1",
		BlockHash:   uintWord(16),
		BlockNumber: 16,
		Attempt:     1,
	}
	transactionHash := uintWord(160)
	caller := &traceTestCaller{handler: func(string, string) (json.RawMessage, error) {
		return nil, testRPCError{code: -32000, message: secret}
	}}
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{
		traceEndpoint(
			t,
			"trace-hostile",
			caller,
			ethrpc.AvailabilityAvailable,
			ethrpc.AvailabilityUnavailable,
		),
	}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := NewTraceRPCProcessor(
		openFakeSQLDB(t, traceReadBackend(transactionHash)),
		pool,
		TraceLimits{},
	)
	if err != nil {
		t.Fatal(err)
	}
	queue := &testJobQueue{
		cancel: func() {},
		lease:  Lease{Job: job, Token: "trace-provider-error-lease"},
	}
	worker, err := NewWorker(
		queue,
		[]Processor{processor},
		WorkerOptions{ID: "trace-provider-error-worker"},
	)
	if err != nil {
		t.Fatal(err)
	}
	found, err := worker.ProcessOne(t.Context())
	if err != nil || !found {
		t.Fatalf("ProcessOne() = %t, %v", found, err)
	}
	if queue.retried == nil ||
		strings.Contains(queue.retried.Reason, secret) ||
		queue.retried.Reason != "JSON-RPC error code -32000" {
		t.Fatalf("durable retry = %#v", queue.retried)
	}
}

func traceEndpoint(
	t *testing.T,
	name string,
	caller *traceTestCaller,
	debug,
	parity ethrpc.Availability,
) ethrpc.Endpoint {
	t.Helper()
	return ethrpc.Endpoint{
		Name: name,
		Client: inProcessRPCClient(t, map[string]any{
			"debug": debugTraceRPCService{caller: caller},
			"trace": parityTraceRPCService{caller: caller},
		}),
		Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeTrace: true},
		Capabilities: ethrpc.CapabilityReport{Methods: map[string]ethrpc.Availability{
			ethrpc.CapabilityDebugTrace: debug, ethrpc.CapabilityParityTrace: parity,
		}},
	}
}

func traceReadBackend(txHash common.Hash) *fakeSQLBackend {
	return &fakeSQLBackend{query: func(query string, _ []driver.NamedValue) (driver.Rows, error) {
		switch {
		case strings.Contains(query, "SELECT EXISTS"):
			return &fakeSQLRows{columns: []string{"canonical"}, values: [][]driver.Value{{true}}}, nil
		case strings.Contains(query, "FROM transaction_inclusions"):
			return &fakeSQLRows{columns: []string{"tx_index", "tx_hash", "from", "to", "value", "input"}, values: [][]driver.Value{traceTransactionRow(0, txHash)}}, nil
		case strings.Contains(query, "FROM transaction_execution_code_resolutions"):
			return emptyExecutionResolutionRows(), nil
		default:
			return nil, fmt.Errorf("unexpected query: %s", query)
		}
	}}
}

func traceBlockReadBackend(transactionHashes ...common.Hash) *fakeSQLBackend {
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
		case strings.Contains(query, "FROM transaction_execution_code_resolutions"):
			return emptyExecutionResolutionRows(), nil
		default:
			return nil, fmt.Errorf("unexpected query: %s", query)
		}
	}}
}

func emptyExecutionResolutionRows() driver.Rows {
	return &fakeSQLRows{columns: []string{
		"transaction_hash", "context_address", "execution_address",
		"execution_code_hash", "resolution", "evidence_source",
	}}
}

func traceAPIRoot(t *testing.T, job Job, transactionHash common.Hash, transactionIndex uint64) json.RawMessage {
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

func successfulTraceBackend(t *testing.T, txHash common.Hash) *fakeSQLBackend {
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
			if strings.Contains(query, "FROM logs") {
				return &fakeSQLRows{columns: []string{"log_index", "raw"}}, nil
			}
			return original(query, arguments)
		}
	}(backend.query)
	backend.exec = func(query string, _ []driver.NamedValue) (driver.Result, error) {
		switch {
		case strings.Contains(query, "UPDATE durable_jobs"):
			return driver.RowsAffected(0), nil
		case strings.Contains(query, "DELETE FROM trace_log_attributions"),
			strings.Contains(query, "DELETE FROM normalized_traces"),
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
