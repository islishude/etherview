package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFixtureServesDeterministicChainAndTrace(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(newFixture())
	defer server.Close()

	assertRPCResult(t, server.URL, "eth_chainId", nil, func(result json.RawMessage) {
		var value string
		if err := json.Unmarshal(result, &value); err != nil {
			t.Fatal(err)
		}
		if value != chainID {
			t.Fatalf("chain ID = %q", value)
		}
	})
	assertRPCResult(t, server.URL, "eth_getBlockByNumber", []any{"0x1", true}, func(result json.RawMessage) {
		var block map[string]any
		if err := json.Unmarshal(result, &block); err != nil {
			t.Fatal(err)
		}
		transactions, ok := block["transactions"].([]any)
		if !ok || len(transactions) != 1 {
			t.Fatalf("transactions = %#v", block["transactions"])
		}
		transaction, ok := transactions[0].(map[string]any)
		if !ok || transaction["hash"] != transactionHash {
			t.Fatalf("transaction = %#v", transactions[0])
		}
	})
	assertRPCResult(t, server.URL, "debug_traceTransaction", []any{transactionHash, map[string]any{}}, func(result json.RawMessage) {
		var trace map[string]any
		if err := json.Unmarshal(result, &trace); err != nil {
			t.Fatal(err)
		}
		if trace["from"] != fromAddress || trace["to"] != toAddress || trace["input"] != "0x1234" {
			t.Fatalf("trace = %#v", trace)
		}
	})
	assertRPCResult(t, server.URL, "eth_getBalance", []any{
		fromAddress,
		map[string]any{"blockHash": blockOneHash, "requireCanonical": true},
	}, func(result json.RawMessage) {
		var value string
		if err := json.Unmarshal(result, &value); err != nil {
			t.Fatal(err)
		}
		if value != "0x5" {
			t.Fatalf("balance = %q", value)
		}
	})
}

func TestFixtureServesPendingTransactionWithoutMinedIdentity(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(newFixture())
	defer server.Close()

	assertRPCResult(t, server.URL, "eth_getBlockByNumber", []any{"pending", true}, func(result json.RawMessage) {
		var block map[string]json.RawMessage
		if err := json.Unmarshal(result, &block); err != nil {
			t.Fatal(err)
		}
		if _, exists := block["number"]; exists {
			t.Fatal("pending block unexpectedly has a number")
		}
		if _, exists := block["hash"]; exists {
			t.Fatal("pending block unexpectedly has a hash")
		}
	})
}

func TestFixtureAdvancesHeadOnDemand(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(newFixture())
	defer server.Close()

	assertRPCResult(t, server.URL, "eth_blockNumber", nil, func(result json.RawMessage) {
		var value string
		if err := json.Unmarshal(result, &value); err != nil {
			t.Fatal(err)
		}
		if value != "0x1" {
			t.Fatalf("initial head = %q", value)
		}
	})
	assertRPCResult(t, server.URL, "eth_getBlockByNumber", []any{"0x2", true}, func(result json.RawMessage) {
		if string(result) != "null" {
			t.Fatalf("future block = %s", result)
		}
	})
	response, err := http.Post(server.URL+"/advance", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("advance status = %d", response.StatusCode)
	}
	assertRPCResult(t, server.URL, "eth_blockNumber", nil, func(result json.RawMessage) {
		var value string
		if err := json.Unmarshal(result, &value); err != nil {
			t.Fatal(err)
		}
		if value != "0x2" {
			t.Fatalf("advanced head = %q", value)
		}
	})
	assertRPCResult(t, server.URL, "eth_getBlockByNumber", []any{"0x2", true}, func(result json.RawMessage) {
		var block map[string]any
		if err := json.Unmarshal(result, &block); err != nil {
			t.Fatal(err)
		}
		if block["hash"] != blockTwoHash || block["parentHash"] != blockOneHash {
			t.Fatalf("block two = %#v", block)
		}
	})
}

func TestFixtureRejectsUnpinnedStateRequests(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(newFixture())
	defer server.Close()

	tests := []struct {
		name   string
		params []any
	}{
		{name: "missing selector", params: []any{fromAddress}},
		{name: "height tag", params: []any{fromAddress, "latest"}},
		{name: "block number object", params: []any{
			fromAddress, map[string]any{"blockNumber": "0x1", "requireCanonical": true},
		}},
		{name: "canonicality not required", params: []any{
			fromAddress, map[string]any{"blockHash": blockOneHash, "requireCanonical": false},
		}},
		{name: "unknown block hash", params: []any{
			fromAddress, map[string]any{"blockHash": zeroHash, "requireCanonical": true},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertRPCError(t, server.URL, "eth_getBalance", test.params, -32602)
		})
	}
}

func assertRPCResult(t *testing.T, serverURL, method string, params any, check func(json.RawMessage)) {
	t.Helper()
	if params == nil {
		params = []any{}
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(serverURL, "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close() //nolint:errcheck
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error != nil {
		t.Fatalf("RPC error = %+v", envelope.Error)
	}
	check(envelope.Result)
}

func assertRPCError(t *testing.T, serverURL, method string, params any, code int) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(serverURL, "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close() //nolint:errcheck
	var envelope struct {
		Error *rpcError `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error == nil || envelope.Error.Code != code {
		t.Fatalf("RPC error = %+v, want code %d", envelope.Error, code)
	}
}
