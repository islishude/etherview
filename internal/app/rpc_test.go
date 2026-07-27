package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/ethrpc"
)

func TestBuildRPCCollectsOptionalWebSocketWakeEndpointsWithoutLoggingCredentials(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var envelope struct {
			JSONRPC string            `json:"jsonrpc"`
			ID      uint64            `json:"id"`
			Method  string            `json:"method"`
			Params  []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(request.Body).Decode(&envelope); err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		var result any
		switch envelope.Method {
		case "eth_chainId":
			result = "0x1"
		case "eth_getBlockByNumber":
			result = probeHeaderJSON(t, 0)
		case "eth_getBlockReceipts":
			result = []any{}
		case "eth_getBalance":
			result = "0x0"
		default:
			t.Errorf("unexpected RPC method %q", envelope.Method)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"jsonrpc": "2.0", "id": envelope.ID, "result": result,
		})
	}))
	defer server.Close()

	wakeURLs := []string{
		"wss://operator:first-secret@wake-one.example/ws?key=first-secret",
		"ws://wake-two.example/ws?key=second-secret",
	}
	cfg := config.Config{
		Chain: config.ChainConfig{ID: 1},
		RPC: config.RPCConfig{
			RequestTimeout: time.Second,
			Endpoints: []config.RPCEndpoint{
				{Name: "poll", URL: server.URL, Purposes: []string{"head", "history"}},
				{Name: "wake-one", URL: wakeURLs[0]},
				{Name: "wake-two", URL: wakeURLs[1]},
			},
		},
	}
	var logs bytes.Buffer
	built, err := buildRPC(context.Background(), cfg, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(built.WakeURLs, wakeURLs) {
		t.Fatalf("wake URLs = %q, want %q", built.WakeURLs, wakeURLs)
	}
	if built.EndpointNum != 1 {
		t.Fatalf("authoritative endpoint count = %d", built.EndpointNum)
	}
	if output := logs.String(); strings.Contains(output, "first-secret") || strings.Contains(output, "second-secret") {
		t.Fatalf("WebSocket credentials leaked into logs: %s", output)
	}
}

func TestBuildRPCDoesNotEchoMalformedURLCredentials(t *testing.T) {
	t.Parallel()
	const secret = "do-not-echo"
	_, err := buildRPC(context.Background(), config.Config{
		Chain: config.ChainConfig{ID: 1},
		RPC: config.RPCConfig{Endpoints: []config.RPCEndpoint{{
			Name: "wake", URL: "wss://operator:" + secret + "@wake.example/%zz",
		}}},
	}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("buildRPC() error = %v", err)
	}
}

func TestBuildRPCExcludesUnavailableEndpointOnlyFromHistoryPool(t *testing.T) {
	t.Parallel()
	bad := newHistoryProbeServer(t, false)
	good := newHistoryProbeServer(t, true)
	cfg := config.Config{
		Chain: config.ChainConfig{ID: 1, StartBlock: 64},
		RPC: config.RPCConfig{
			RequestTimeout: time.Second,
			Endpoints: []config.RPCEndpoint{
				{Name: "bad", URL: bad.URL, Purposes: []string{"head", "history"}},
				{Name: "good", URL: good.URL, Purposes: []string{"head", "history"}},
			},
		},
	}
	var logs bytes.Buffer
	built, err := buildRPC(context.Background(), cfg, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if got := built.Pool.Names(ethrpc.PurposeHistory); !slices.Equal(got, []string{"good"}) {
		t.Fatalf("history endpoints = %v", got)
	}
	if got := built.Pool.Names(ethrpc.PurposeHead); !slices.Equal(got, []string{"bad", "good"}) {
		t.Fatalf("head endpoints = %v", got)
	}
	issue := built.Reports["bad"].HistoryUnavailable
	if issue == nil || issue.Kind != ethrpc.HistoryUnavailableResult ||
		!errors.Is(issue, ethrpc.ErrHistoryUnavailable) || errors.Is(issue, ethrpc.ErrHistoryPruned) {
		t.Fatalf("bad endpoint history issue = %#v", issue)
	}
	if !strings.Contains(logs.String(), "error_code=history_unavailable") {
		t.Fatalf("history exclusion was not logged with a stable code: %s", logs.String())
	}
}

func TestBuildRPCDropsUnavailableMempoolPurpose(t *testing.T) {
	t.Parallel()
	server := newTxpoolProbeServer(t, false)
	built, err := buildRPC(context.Background(), config.Config{
		Chain: config.ChainConfig{ID: 1, StartBlock: 64},
		RPC: config.RPCConfig{
			RequestTimeout: time.Second,
			Endpoints: []config.RPCEndpoint{{
				Name: "txpool-head", URL: server.URL, Purposes: []string{"head", "mempool"},
			}},
		},
	}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if got := built.Pool.Names(ethrpc.PurposeMempool); len(got) != 0 {
		t.Fatalf("mempool endpoints = %v", got)
	}
	if got := built.Pool.Names(ethrpc.PurposeHead); !slices.Equal(got, []string{"txpool-head"}) {
		t.Fatalf("head endpoints = %v", got)
	}
}

func TestBuildRPCFailsExplicitlyWhenOnlyHTTPPurposeIsUnavailableHistory(t *testing.T) {
	t.Parallel()
	server := newHistoryProbeServer(t, false)
	_, err := buildRPC(context.Background(), config.Config{
		Chain: config.ChainConfig{ID: 1, StartBlock: 64},
		RPC: config.RPCConfig{
			RequestTimeout: time.Second,
			Endpoints: []config.RPCEndpoint{{
				Name: "pruned", URL: server.URL, Purposes: []string{"history"},
			}},
		},
	}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	if !errors.Is(err, ethrpc.ErrHistoryUnavailable) || errors.Is(err, ethrpc.ErrHistoryPruned) {
		t.Fatalf("buildRPC() error = %v", err)
	}
}

func TestBuildRPCAcceptsAPIOnlyStateEndpointWithoutHistoryPurpose(t *testing.T) {
	t.Parallel()
	server := newHistoryProbeServer(t, false)
	built, err := buildRPC(context.Background(), config.Config{
		Chain: config.ChainConfig{ID: 1, StartBlock: 64},
		RPC: config.RPCConfig{
			RequestTimeout: time.Second,
			Endpoints:      []config.RPCEndpoint{{Name: "state-only", URL: server.URL, Purposes: []string{"state"}}},
		},
	}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if got := built.Pool.Names(ethrpc.PurposeState); !slices.Equal(got, []string{"state-only"}) {
		t.Fatalf("state endpoints=%v", got)
	}
	if got := built.Pool.Names(ethrpc.PurposeHistory); len(got) != 0 {
		t.Fatalf("history endpoints=%v", got)
	}
}

func newHistoryProbeServer(t *testing.T, historyAvailable bool) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var envelope struct {
			JSONRPC string            `json:"jsonrpc"`
			ID      uint64            `json:"id"`
			Method  string            `json:"method"`
			Params  []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(request.Body).Decode(&envelope); err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		var result any
		switch envelope.Method {
		case "eth_chainId":
			result = "0x1"
		case "eth_getBlockByNumber":
			var tag string
			if len(envelope.Params) == 0 || json.Unmarshal(envelope.Params[0], &tag) != nil {
				t.Errorf("invalid block tag params: %s", envelope.Params)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			switch tag {
			case "0x0", "safe", "finalized":
				result = probeHeaderJSON(t, 0)
			case "0x40":
				if historyAvailable {
					result = probeHeaderJSON(t, 64)
				} else {
					result = nil
				}
			default:
				t.Errorf("unexpected block tag %q", tag)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
		case "eth_getBlockReceipts":
			result = []any{}
		case "eth_getBalance":
			result = "0x0"
		default:
			t.Errorf("unexpected RPC method %q", envelope.Method)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"jsonrpc": "2.0", "id": envelope.ID, "result": result,
		})
	}))
	t.Cleanup(server.Close)
	return server
}

func newTxpoolProbeServer(t *testing.T, hasTxPool bool) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var envelope struct {
			JSONRPC string            `json:"jsonrpc"`
			ID      uint64            `json:"id"`
			Method  string            `json:"method"`
			Params  []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(request.Body).Decode(&envelope); err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		var result any
		switch envelope.Method {
		case "eth_chainId":
			result = "0x1"
		case "eth_getBlockByNumber":
			var tag string
			if len(envelope.Params) == 0 || json.Unmarshal(envelope.Params[0], &tag) != nil {
				t.Errorf("invalid block tag params: %s", envelope.Params)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			switch tag {
			case "0x0", "safe", "finalized":
				result = probeHeaderJSON(t, 0)
			case "0x40":
				result = probeHeaderJSON(t, 64)
			default:
				t.Errorf("unexpected block tag %q", tag)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
		case "eth_getBlockReceipts":
			result = []any{}
		case "eth_getBalance":
			result = "0x0"
		case "rpc_modules":
			modules := map[string]string{"debug": "debug"}
			if hasTxPool {
				modules["txpool"] = "txpool"
			}
			result = modules
		default:
			t.Errorf("unexpected RPC method %q", envelope.Method)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"jsonrpc": "2.0", "id": envelope.ID, "result": result,
		})
	}))
	t.Cleanup(server.Close)
	return server
}

func probeHeaderJSON(t *testing.T, number uint64) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(&types.Header{
		ParentHash:  common.Hash{},
		UncleHash:   types.EmptyUncleHash,
		Coinbase:    common.Address{19: 1},
		Root:        common.Hash{31: 2},
		TxHash:      types.EmptyTxsHash,
		ReceiptHash: types.EmptyReceiptsHash,
		Difficulty:  big.NewInt(0),
		Number:      new(big.Int).SetUint64(number),
		GasLimit:    30_000_000,
		Time:        1_700_000_000 + number,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
