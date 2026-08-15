package ethrpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/chainbundle/testfixture"
)

func TestPoolSelectsPurposeAndSkipsCoolingOrUnavailableHistory(t *testing.T) {
	t.Parallel()
	client := rpc.DialInProc(rpc.NewServer())
	t.Cleanup(client.Close)
	now := time.Unix(100, 0)
	pool, err := NewPool([]Endpoint{
		{Name: "one", Purposes: map[Purpose]bool{PurposeHead: true}, Client: client},
		{
			Name: "pruned",
			Purposes: map[Purpose]bool{
				PurposeHead:    true,
				PurposeHistory: true,
			},
			Client: client,
			Capabilities: CapabilityReport{Methods: map[string]Availability{
				CapabilityHistoricalData: AvailabilityUnavailable,
			}},
		},
		{
			Name:     "archive",
			Purposes: map[Purpose]bool{PurposeHistory: true, PurposeState: true},
			Client:   client,
		},
	}, PoolOptions{Now: func() time.Time { return now }, FailureCooldown: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if got := pool.Names(PurposeHistory); !slices.Equal(got, []string{"archive"}) {
		t.Fatalf("history endpoints = %v", got)
	}
	first, err := pool.Acquire(PurposeHead)
	if err != nil {
		t.Fatal(err)
	}
	pool.ReportFailure(first.Name)
	second, err := pool.Acquire(PurposeHead)
	if err != nil {
		t.Fatal(err)
	}
	if first.Name == second.Name {
		t.Fatalf("cooling endpoint %q was selected", first.Name)
	}
	state, err := pool.Acquire(PurposeState)
	if err != nil || state.Name != "archive" {
		t.Fatalf("state endpoint = %#v, %v", state, err)
	}
	if _, err := pool.Acquire(PurposeTrace); err == nil {
		t.Fatal("Acquire(trace) succeeded without a trace endpoint")
	}
}

func TestProbeEndpointUsesGethHeadersAndPurposeCapabilities(t *testing.T) {
	t.Parallel()
	genesis := mustFixture(t, testfixture.Options{Number: 0, ExtraData: []byte("genesis")})
	history := mustFixture(t, testfixture.Options{
		Number: 64, ParentHash: common.Hash{31: 0x10}, ExtraData: []byte("history"),
	})
	var methods []string
	server := newSingleRPCServer(t, func(request testRequest) (json.RawMessage, *testRPCError) {
		methods = append(methods, request.Method)
		switch request.Method {
		case "eth_chainId":
			return json.RawMessage(`"0x1"`), nil
		case "eth_getBlockByNumber":
			var tag string
			_ = json.Unmarshal(request.Params[0], &tag)
			switch tag {
			case "0x0", "safe", "finalized":
				return genesis.RawBlock, nil
			case "0x40":
				return history.RawBlock, nil
			default:
				t.Fatalf("unexpected tag %q", tag)
			}
		case "eth_getBlockReceipts":
			return json.RawMessage("[]"), nil
		default:
			t.Fatalf("unexpected method %q", request.Method)
		}
		return nil, nil
	})
	client := mustClient(t, server.URL)
	endpoint := &Endpoint{
		Name:     "history",
		Purposes: map[Purpose]bool{PurposeHead: true, PurposeHistory: true},
		Client:   client,
	}
	report, err := ProbeEndpoint(t.Context(), endpoint, ProbeOptions{
		Expected: &ChainIdentity{
			ChainID:     "1",
			GenesisHash: genesis.Block.Hash(),
		},
		StartBlock: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.GenesisHash != genesis.Block.Hash() ||
		report.Status(CapabilityHistoricalData) != AvailabilityAvailable ||
		report.Status(CapabilityBlockReceipts) != AvailabilityAvailable ||
		report.Status(CapabilitySafeTag) != AvailabilityAvailable ||
		report.Status(CapabilityFinalizedTag) != AvailabilityAvailable {
		t.Fatalf("capability report = %+v", report)
	}
	if slices.Contains(methods, "eth_getBalance") || slices.Contains(methods, "rpc_modules") {
		t.Fatalf("probe called outside configured purposes: %v", methods)
	}
}

func TestProbeDebugTraceRequiresBlockByHashMethod(t *testing.T) {
	t.Parallel()
	genesis := mustFixture(t, testfixture.Options{Number: 0, ExtraData: []byte("trace-genesis")})
	for _, test := range []struct {
		name      string
		supported bool
		want      Availability
	}{
		{name: "block method", supported: true, want: AvailabilityAvailable},
		{name: "transaction only", supported: false, want: AvailabilityUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := newSingleRPCServer(t, func(request testRequest) (json.RawMessage, *testRPCError) {
				switch request.Method {
				case "eth_chainId":
					return json.RawMessage(`"0x1"`), nil
				case "eth_getBlockByNumber":
					return genesis.RawBlock, nil
				case "rpc_modules":
					return json.RawMessage(`{"debug":"1.0"}`), nil
				case "debug_traceBlockByHash":
					var hash common.Hash
					if len(request.Params) < 2 || json.Unmarshal(request.Params[0], &hash) != nil || hash != genesis.Block.Hash() {
						t.Fatalf("block trace probe params = %s", request.Params)
					}
					if test.supported {
						return json.RawMessage(`[]`), nil
					}
					return nil, &testRPCError{Code: -32601, Message: "debug_traceBlockByHash unavailable"}
				default:
					t.Fatalf("unexpected method %q", request.Method)
				}
				return nil, nil
			})
			client := mustClient(t, server.URL)
			report, err := ProbeEndpoint(t.Context(), &Endpoint{
				Name: "trace", Purposes: map[Purpose]bool{PurposeTrace: true}, Client: client,
			}, ProbeOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if got := report.Status(CapabilityDebugTrace); got != test.want {
				t.Fatalf("debug trace capability = %s, want %s; report=%+v", got, test.want, report)
			}
		})
	}
}

func TestProbeClassifiesPrunedHistoryWithoutRetainingProviderMessage(t *testing.T) {
	t.Parallel()
	const providerSecret = "provider-secret"
	genesis := mustFixture(t, testfixture.Options{Number: 0})
	server := newSingleRPCServer(t, func(request testRequest) (json.RawMessage, *testRPCError) {
		switch request.Method {
		case "eth_chainId":
			return json.RawMessage(`"0x1"`), nil
		case "eth_getBlockByNumber":
			var tag string
			_ = json.Unmarshal(request.Params[0], &tag)
			if tag == "0x0" {
				return genesis.RawBlock, nil
			}
			return nil, &testRPCError{Code: -32000, Message: "history pruned " + providerSecret}
		case "eth_getBlockReceipts":
			return json.RawMessage("[]"), nil
		default:
			t.Fatalf("unexpected method %q", request.Method)
		}
		return nil, nil
	})
	client := mustClient(t, server.URL)
	report, err := ProbeEndpoint(t.Context(), &Endpoint{
		Name:     "history",
		Purposes: map[Purpose]bool{PurposeHistory: true},
		Client:   client,
	}, ProbeOptions{
		Expected:   &ChainIdentity{ChainID: "1", GenesisHash: genesis.Block.Hash()},
		StartBlock: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	issue := report.HistoryUnavailable
	if issue == nil || issue.Kind != HistoryPrunedResult ||
		!errors.Is(issue, ErrHistoryUnavailable) || !errors.Is(issue, ErrHistoryPruned) {
		t.Fatalf("history issue = %#v", issue)
	}
	if stringsContain(issue.Error(), providerSecret) {
		t.Fatalf("history issue leaked provider message: %v", issue)
	}
}

func TestFetcherFallsBackToBoundedGethBatchReceipts(t *testing.T) {
	t.Parallel()
	expected := mustFixture(t, testfixture.Options{
		Number:           1,
		ExtraData:        []byte("fetch"),
		TransactionTypes: []uint8{types.LegacyTxType, types.DynamicFeeTxType},
	})
	var batchCalls atomic.Int64
	receiptsByHash := make(map[string]json.RawMessage, len(expected.RawReceipts))
	for index, transaction := range expected.Block.Transactions() {
		receiptsByHash[transaction.Hash().Hex()] = expected.RawReceipts[index]
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := ioReadAll(request)
		if err != nil {
			t.Error(err)
			return
		}
		if bytes.HasPrefix(bytes.TrimSpace(body), []byte("[")) {
			batchCalls.Add(1)
			var requests []testRequest
			if err := json.Unmarshal(body, &requests); err != nil {
				t.Error(err)
				return
			}
			responses := make([]map[string]json.RawMessage, len(requests))
			for index := range requests {
				var hash string
				if err := json.Unmarshal(requests[index].Params[0], &hash); err != nil {
					t.Error(err)
					return
				}
				receipt, exists := receiptsByHash[hash]
				if !exists {
					t.Errorf("unknown receipt hash %q", hash)
					return
				}
				responses[len(requests)-1-index] = response(requests[index].ID, receipt)
			}
			writeBatch(t, writer, responses)
			return
		}
		var envelope testRequest
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Error(err)
			return
		}
		switch envelope.Method {
		case "eth_getBlockByNumber":
			writeResult(t, writer, envelope.ID, expected.RawBlock)
		case CapabilityBlockReceipts:
			writeRPCError(t, writer, envelope.ID, -32601, "method not found")
		default:
			t.Errorf("unexpected method %q", envelope.Method)
		}
	}))
	t.Cleanup(server.Close)
	client := mustClient(t, server.URL)
	endpoint := &Endpoint{Name: "rpc-a", Client: client, Purposes: map[Purpose]bool{PurposeHistory: true}}
	fetched, err := (Fetcher{ReceiptBatchSize: 1}).ByNumber(t.Context(), endpoint, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := chainbundle.Validate(fetched); err != nil {
		t.Fatal(err)
	}
	if fetched.Block.Hash() != expected.Block.Hash() || len(fetched.Receipts) != 2 || batchCalls.Load() != 2 {
		t.Fatalf("fetched hash=%s receipts=%d batches=%d", fetched.Block.Hash(), len(fetched.Receipts), batchCalls.Load())
	}
}

func TestFetcherUnknownTransactionTypeFailsBeforeReceiptFetch(t *testing.T) {
	t.Parallel()
	expected := mustFixture(t, testfixture.Options{
		Number:           1,
		TransactionTypes: []uint8{types.LegacyTxType},
	})
	rawBlock := mutateRPCBlockTransaction(t, expected.RawBlock, func(fields map[string]json.RawMessage) {
		fields["type"] = json.RawMessage(`"0x7f"`)
	})
	var receiptCalls atomic.Int64
	server := newSingleRPCServer(t, func(request testRequest) (json.RawMessage, *testRPCError) {
		switch request.Method {
		case "eth_getBlockByNumber":
			return rawBlock, nil
		case CapabilityBlockReceipts, "eth_getTransactionReceipt":
			receiptCalls.Add(1)
			return nil, &testRPCError{Code: -32000, Message: "must not be called"}
		default:
			t.Fatalf("unexpected method %q", request.Method)
		}
		return nil, nil
	})
	client := mustClient(t, server.URL)
	fetched, err := (Fetcher{}).ByNumber(t.Context(), &Endpoint{
		Name: "rpc-a", Client: client, Purposes: map[Purpose]bool{PurposeHistory: true},
	}, 1)
	if !errors.Is(err, chainbundle.ErrUnsupportedTransactionType) || !chainbundle.IsPermanent(err) {
		t.Fatalf("Fetcher.ByNumber() error = %v", err)
	}
	if fetched.Block != nil || fetched.RawBlock != nil || receiptCalls.Load() != 0 {
		t.Fatalf("Fetcher exposed partial data or fetched receipts: %#v calls=%d", fetched, receiptCalls.Load())
	}
}

func TestFetcherRetainsAndValidatesRawUncleHeaders(t *testing.T) {
	t.Parallel()
	rawBlock, rawUncle, blockHash := blockAndUncleFixture(t, 8)
	server := newSingleRPCServer(t, func(request testRequest) (json.RawMessage, *testRPCError) {
		switch request.Method {
		case "eth_getBlockByNumber":
			return rawBlock, nil
		case "eth_getUncleByBlockHashAndIndex":
			return rawUncle, nil
		case CapabilityBlockReceipts:
			return json.RawMessage("[]"), nil
		default:
			t.Fatalf("unexpected method %q", request.Method)
		}
		return nil, nil
	})
	client := mustClient(t, server.URL)
	fetched, err := (Fetcher{}).ByNumber(t.Context(), &Endpoint{
		Name: "pow", Client: client, Purposes: map[Purpose]bool{PurposeHistory: true},
	}, 8)
	if err != nil {
		t.Fatal(err)
	}
	if fetched.Block.Hash() != blockHash || len(fetched.Block.Uncles()) != 1 || len(fetched.RawUncles) != 1 {
		t.Fatalf("fetched uncle bundle = hash:%s uncles:%d raw:%d", fetched.Block.Hash(), len(fetched.Block.Uncles()), len(fetched.RawUncles))
	}
	if err := chainbundle.Validate(fetched); err != nil {
		t.Fatal(err)
	}
}

func TestCapabilityReportCloneAndChainIDNormalization(t *testing.T) {
	t.Parallel()
	report := CapabilityReport{
		Methods: map[string]Availability{"x": AvailabilityAvailable},
		HistoryUnavailable: &HistoryUnavailableError{
			Kind: HistoryPrunedResult, StartBlock: 7,
		},
		Warnings: []string{"a"},
	}
	clone := report.Clone()
	clone.Methods["x"] = AvailabilityUnavailable
	clone.HistoryUnavailable.Kind = HistoryUnavailableResult
	clone.Warnings[0] = "b"
	if report.Methods["x"] != AvailabilityAvailable ||
		report.HistoryUnavailable.Kind != HistoryPrunedResult ||
		report.Warnings[0] != "a" {
		t.Fatalf("clone mutated original: %+v", report)
	}
	if value, err := NormalizeChainID("0x2a"); err != nil || value != "42" {
		t.Fatalf("NormalizeChainID() = %q, %v", value, err)
	}
}

type testRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func newSingleRPCServer(
	t *testing.T,
	handle func(testRequest) (json.RawMessage, *testRPCError),
) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var raw json.RawMessage
		if err := json.NewDecoder(request.Body).Decode(&raw); err != nil {
			t.Error(err)
			return
		}
		if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
			var requests []testRequest
			if err := json.Unmarshal(raw, &requests); err != nil {
				t.Error(err)
				return
			}
			responses := make([]map[string]json.RawMessage, len(requests))
			for index := range requests {
				result, rpcErr := handle(requests[index])
				if rpcErr != nil {
					responses[index] = errorResponse(requests[index].ID, rpcErr.Code, rpcErr.Message)
				} else {
					responses[index] = response(requests[index].ID, result)
				}
			}
			writeBatch(t, writer, responses)
			return
		}
		var envelope testRequest
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Error(err)
			return
		}
		result, rpcErr := handle(envelope)
		if rpcErr != nil {
			writeRPCError(t, writer, envelope.ID, rpcErr.Code, rpcErr.Message)
			return
		}
		writeResult(t, writer, envelope.ID, result)
	}))
	t.Cleanup(server.Close)
	return server
}

func writeRPCError(t *testing.T, writer http.ResponseWriter, id json.RawMessage, code int, message string) {
	t.Helper()
	payload := errorResponse(id, code, message)
	if err := json.NewEncoder(writer).Encode(payload); err != nil {
		t.Error(err)
	}
}

func errorResponse(id json.RawMessage, code int, message string) map[string]json.RawMessage {
	errorData, _ := json.Marshal(map[string]any{"code": code, "message": message})
	return map[string]json.RawMessage{
		"jsonrpc": json.RawMessage(`"2.0"`),
		"id":      id,
		"error":   errorData,
	}
}

func mustClient(t *testing.T, endpoint string) *rpc.Client {
	t.Helper()
	client, err := NewClient(t.Context(), endpoint, ClientOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}

func mustFixture(t *testing.T, options testfixture.Options) chainbundle.Bundle {
	t.Helper()
	bundle, err := testfixture.New(options)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func ioReadAll(request *http.Request) ([]byte, error) {
	defer request.Body.Close() //nolint:errcheck
	var buffer bytes.Buffer
	_, err := buffer.ReadFrom(request.Body)
	return buffer.Bytes(), err
}

func mutateRPCBlockTransaction(
	t *testing.T,
	raw json.RawMessage,
	mutate func(map[string]json.RawMessage),
) json.RawMessage {
	t.Helper()
	var block map[string]json.RawMessage
	if err := json.Unmarshal(raw, &block); err != nil {
		t.Fatal(err)
	}
	var transactions []json.RawMessage
	if err := json.Unmarshal(block["transactions"], &transactions); err != nil {
		t.Fatal(err)
	}
	var transaction map[string]json.RawMessage
	if err := json.Unmarshal(transactions[0], &transaction); err != nil {
		t.Fatal(err)
	}
	mutate(transaction)
	transactions[0] = mustRPCJSON(t, transaction)
	block["transactions"] = mustRPCJSON(t, transactions)
	return mustRPCJSON(t, block)
}

func blockAndUncleFixture(t *testing.T, number uint64) (json.RawMessage, json.RawMessage, common.Hash) {
	t.Helper()
	base := mustFixture(t, testfixture.Options{Number: number, ExtraData: []byte("main")})
	var header types.Header
	if err := json.Unmarshal(base.RawBlock, &header); err != nil {
		t.Fatal(err)
	}
	uncle := &types.Header{
		ParentHash:  common.Hash{31: 0x11},
		UncleHash:   types.EmptyUncleHash,
		Coinbase:    common.Address{19: 0x12},
		Root:        common.Hash{31: 0x13},
		TxHash:      types.EmptyTxsHash,
		ReceiptHash: types.EmptyReceiptsHash,
		Difficulty:  big.NewInt(1),
		Number:      new(big.Int).SetUint64(number - 1),
		GasLimit:    30_000_000,
		Time:        1_700_000_000 + number - 1,
		Extra:       []byte("uncle"),
	}
	rawUncle := mustRPCJSON(t, uncle)
	header.UncleHash = types.CalcUncleHash([]*types.Header{uncle})
	rawHeader := mustRPCJSON(t, &header)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rawHeader, &fields); err != nil {
		t.Fatal(err)
	}
	fields["transactions"] = json.RawMessage("[]")
	fields["uncles"] = mustRPCJSON(t, []common.Hash{uncle.Hash()})
	return mustRPCJSON(t, fields), rawUncle, header.Hash()
}

func mustRPCJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func stringsContain(value, fragment string) bool {
	return bytes.Contains([]byte(value), []byte(fragment))
}
