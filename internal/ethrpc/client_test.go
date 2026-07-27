package ethrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
)

type testRequest struct {
	JSONRPC string            `json:"jsonrpc"`
	ID      json.RawMessage   `json:"id"`
	Method  string            `json:"method"`
	Params  []json.RawMessage `json:"params"`
}

func TestClientUsesUpstreamRPCClient(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var envelope testRequest
		if err := json.NewDecoder(request.Body).Decode(&envelope); err != nil {
			t.Error(err)
			return
		}
		if envelope.Method != "eth_chainId" {
			t.Errorf("method = %q", envelope.Method)
		}
		writeResult(t, writer, envelope.ID, json.RawMessage(`"0x1"`))
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(t.Context(), server.URL, ClientOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	var chainID hexutil.Big
	if err := client.CallContext(t.Context(), &chainID, "eth_chainId"); err != nil {
		t.Fatal(err)
	}
	if chainID.String() != "0x1" {
		t.Fatalf("chain ID = %s", chainID.String())
	}
}

func TestGuardAllowsOutOfOrderBatchAndRejectsDuplicateID(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		duplicate bool
		wantError bool
	}{
		{name: "out of order"},
		{name: "duplicate", duplicate: true, wantError: true},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				var requests []testRequest
				if err := json.NewDecoder(request.Body).Decode(&requests); err != nil {
					t.Error(err)
					return
				}
				secondID := requests[1].ID
				if test.duplicate {
					secondID = requests[0].ID
				}
				writeBatch(t, writer, []map[string]json.RawMessage{
					response(requests[0].ID, json.RawMessage(`"first"`)),
					response(secondID, json.RawMessage(`"second"`)),
				})
			}))
			t.Cleanup(server.Close)
			client, err := NewClient(t.Context(), server.URL, ClientOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(client.Close)
			var first, second string
			elements := []rpc.BatchElem{
				{Method: "first", Result: &first},
				{Method: "second", Result: &second},
			}
			err = client.BatchCallContext(t.Context(), elements)
			if test.wantError {
				if !errors.Is(err, ErrInvalidResponse) {
					t.Fatalf("BatchCallContext() error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if first != "first" || second != "second" {
				t.Fatalf("results = %q, %q", first, second)
			}
		})
	}
}

func TestGuardRejectsWrongSingleIDVersionAndTrailingJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body func(json.RawMessage) string
	}{
		{name: "wrong ID", body: func(json.RawMessage) string {
			return `{"jsonrpc":"2.0","id":999,"result":"ok"}`
		}},
		{name: "wrong version", body: func(id json.RawMessage) string {
			return `{"jsonrpc":"1.0","id":` + string(id) + `,"result":"ok"}`
		}},
		{name: "trailing JSON", body: func(id json.RawMessage) string {
			return `{"jsonrpc":"2.0","id":` + string(id) + `,"result":"ok"} {}`
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				var envelope testRequest
				_ = json.NewDecoder(request.Body).Decode(&envelope)
				_, _ = writer.Write([]byte(test.body(envelope.ID)))
			}))
			t.Cleanup(server.Close)
			client, err := NewClient(t.Context(), server.URL, ClientOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(client.Close)
			var result string
			if err := client.CallContext(t.Context(), &result, "test"); !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("CallContext() error = %v", err)
			}
		})
	}
}

func TestGuardBoundsResponseAndRedactsHTTPBodyAndEndpoint(t *testing.T) {
	t.Parallel()
	t.Run("response limit", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			var envelope testRequest
			_ = json.NewDecoder(request.Body).Decode(&envelope)
			writeResult(t, writer, envelope.ID, json.RawMessage(`"`+strings.Repeat("x", 128)+`"`))
		}))
		t.Cleanup(server.Close)
		client, err := NewClient(t.Context(), server.URL, ClientOptions{MaxResponseBytes: 64})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(client.Close)
		var result string
		if err := client.CallContext(t.Context(), &result, "test"); !errors.Is(err, ErrResponseTooLarge) {
			t.Fatalf("CallContext() error = %v", err)
		}
	})

	t.Run("status body", func(t *testing.T) {
		t.Parallel()
		const secret = "highly-secret"
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusTooManyRequests)
			_, _ = fmt.Fprint(writer, "secret response")
		}))
		t.Cleanup(server.Close)
		client, err := NewClient(t.Context(), server.URL+"?api_key="+secret, ClientOptions{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(client.Close)
		var result string
		err = client.CallContext(t.Context(), &result, "test")
		var status rpc.HTTPError
		if !errors.As(err, &status) || status.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("CallContext() error = %v", err)
		}
		if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "secret response") {
			t.Fatalf("HTTP error leaked secret material: %q", err)
		}
	})

	t.Run("status reason", func(t *testing.T) {
		t.Parallel()
		const secret = "provider-controlled-reason"
		client, err := NewClient(
			t.Context(),
			"https://example.invalid/rpc",
			ClientOptions{HTTPClient: &http.Client{Transport: responseRoundTripper{
				response: &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Status:     "429 " + secret,
					Body:       http.NoBody,
				},
			}}},
		)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(client.Close)
		var result string
		err = client.CallContext(t.Context(), &result, "test")
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("HTTP error retained provider reason: %v", err)
		}
	})

	t.Run("provider JSON-RPC error", func(t *testing.T) {
		t.Parallel()
		const secret = "provider-json-rpc-secret"
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			var envelope testRequest
			if err := json.NewDecoder(request.Body).Decode(&envelope); err != nil {
				t.Error(err)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(writer).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      envelope.ID,
				"error": map[string]any{
					"code":    -32000,
					"message": secret,
					"data":    map[string]string{"nested": secret},
				},
			}); err != nil {
				t.Error(err)
			}
		}))
		t.Cleanup(server.Close)
		client, err := NewClient(t.Context(), server.URL, ClientOptions{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(client.Close)
		var result string
		rawError := client.CallContext(t.Context(), &result, "test")
		var rpcError rpc.Error
		if !errors.As(rawError, &rpcError) {
			t.Fatalf("CallContext() error = %v", rawError)
		}
		sanitized := SanitizeError(rawError)
		if sanitized == nil ||
			strings.Contains(sanitized.Error(), secret) ||
			sanitized.Error() != "JSON-RPC error code -32000" {
			t.Fatalf("SanitizeError() = %v", sanitized)
		}
	})

	t.Run("transport error", func(t *testing.T) {
		t.Parallel()
		const secret = "transport-secret"
		client, err := NewClient(
			t.Context(),
			"https://operator:"+secret+"@example.invalid/rpc?key="+secret,
			ClientOptions{HTTPClient: &http.Client{Transport: failingRoundTripper{}}},
		)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(client.Close)
		var result string
		err = client.CallContext(t.Context(), &result, "test")
		if !errors.Is(err, ErrTransport) {
			t.Fatalf("CallContext() error = %v", err)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("transport error leaked endpoint: %q", err)
		}
	})
}

func TestTransportRateLimitHonorsCancellationBeforeSecondRequest(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		var envelope testRequest
		_ = json.NewDecoder(request.Body).Decode(&envelope)
		writeResult(t, writer, envelope.ID, json.RawMessage(`"ok"`))
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(t.Context(), server.URL, ClientOptions{RequestsPerSecond: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	var result string
	if err := client.CallContext(t.Context(), &result, "first"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := client.CallContext(ctx, &result, "second"); !errors.Is(err, context.Canceled) {
		t.Fatalf("second call error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("server calls = %d", calls.Load())
	}
}

type failingRoundTripper struct{}

func (failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("nested transport detail must not escape")
}

type responseRoundTripper struct {
	response *http.Response
}

func (transport responseRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return transport.response, nil
}

func response(id, result json.RawMessage) map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"jsonrpc": json.RawMessage(`"2.0"`),
		"id":      id,
		"result":  result,
	}
}

func writeResult(t *testing.T, writer http.ResponseWriter, id, result json.RawMessage) {
	t.Helper()
	if err := json.NewEncoder(writer).Encode(response(id, result)); err != nil {
		t.Error(err)
	}
}

func writeBatch(t *testing.T, writer http.ResponseWriter, responses []map[string]json.RawMessage) {
	t.Helper()
	if err := json.NewEncoder(writer).Encode(responses); err != nil {
		t.Error(err)
	}
}
