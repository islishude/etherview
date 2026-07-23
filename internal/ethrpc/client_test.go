package ethrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPClientCall(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var envelope rpcRequest
		if err := json.NewDecoder(request.Body).Decode(&envelope); err != nil {
			t.Error(err)
			return
		}
		if envelope.Method != "eth_chainId" {
			t.Errorf("method = %q", envelope.Method)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      envelope.ID,
			"result":  "0x1",
		})
	}))
	defer server.Close()
	client, err := NewHTTPClient(server.URL, HTTPClientOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var chainID Quantity
	if err := client.Call(context.Background(), "eth_chainId", nil, &chainID); err != nil {
		t.Fatal(err)
	}
	if chainID != "0x1" {
		t.Fatalf("chainID = %q", chainID)
	}
}

func TestHTTPClientBatchMatchesOutOfOrderResponses(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var envelopes []rpcRequest
		if err := json.NewDecoder(request.Body).Decode(&envelopes); err != nil {
			t.Error(err)
			return
		}
		responses := []map[string]any{
			{"jsonrpc": "2.0", "id": envelopes[1].ID, "result": "second"},
			{"jsonrpc": "2.0", "id": envelopes[0].ID, "result": "first"},
		}
		_ = json.NewEncoder(writer).Encode(responses)
	}))
	defer server.Close()
	client, _ := NewHTTPClient(server.URL, HTTPClientOptions{})
	var first, second string
	elements := []BatchElem{
		{Method: "first", Result: &first},
		{Method: "second", Result: &second},
	}
	if err := client.BatchCall(context.Background(), elements); err != nil {
		t.Fatal(err)
	}
	if first != "first" || second != "second" {
		t.Fatalf("results = %q, %q", first, second)
	}
}

func TestHTTPClientRejectsDuplicateBatchResponseID(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var envelopes []rpcRequest
		_ = json.NewDecoder(request.Body).Decode(&envelopes)
		_ = json.NewEncoder(writer).Encode([]map[string]any{
			{"jsonrpc": "2.0", "id": envelopes[0].ID, "result": "first"},
			{"jsonrpc": "2.0", "id": envelopes[0].ID, "result": "duplicate"},
		})
	}))
	defer server.Close()
	client, _ := NewHTTPClient(server.URL, HTTPClientOptions{})
	var first, second string
	err := client.BatchCall(context.Background(), []BatchElem{{Method: "a", Result: &first}, {Method: "b", Result: &second}})
	if _, ok := errors.AsType[*ProtocolError](err); !ok {
		t.Fatalf("error = %v, want ProtocolError", err)
	}
}

func TestHTTPClientDoesNotIncludeEndpointCredentialInStatusError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = fmt.Fprint(writer, "secret response")
	}))
	defer server.Close()
	client, _ := NewHTTPClient(server.URL+"?api_key=highly-secret", HTTPClientOptions{})
	var result string
	err := client.Call(context.Background(), "eth_chainId", nil, &result)
	var statusError *HTTPStatusError
	if !errors.As(err, &statusError) || statusError.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("error = %v", err)
	}
	if got := err.Error(); got == "" || containsAny(got, "highly-secret", "secret response") {
		t.Fatalf("credential or response body leaked in error: %q", got)
	}
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if len(candidate) > 0 && len(value) >= len(candidate) {
			for index := 0; index+len(candidate) <= len(value); index++ {
				if value[index:index+len(candidate)] == candidate {
					return true
				}
			}
		}
	}
	return false
}
