//go:build previewmetadatae2e

package previewmetadatae2e

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"
)

func TestReceiptProbe(t *testing.T) {
	hash := common.HexToHash("0x1234")
	cases := []struct {
		name            string
		response        map[string]any
		pending, failed bool
	}{
		{"indexing", map[string]any{"error": map[string]any{"code": -32000, "message": "transaction indexing is in progress", "data": "transaction indexing is in progress"}}, true, false},
		{"null", map[string]any{"result": nil}, true, false},
		{"mined", map[string]any{"result": map[string]any{"transactionHash": hash.Hex(), "status": "0x1"}}, false, false},
		{"other server error", map[string]any{"error": map[string]any{"code": -32000, "message": "backend unavailable"}}, false, true},
		{"wrong code", map[string]any{"error": map[string]any{"code": -32603, "message": "transaction indexing is in progress"}}, false, true},
		{"wrong hash", map[string]any{"result": map[string]any{"transactionHash": common.HexToHash("0x5678").Hex()}}, false, true},
	}
	// One connection sees indexing, null and then a mined receipt, exercising the
	// same transition used by both deployment and metadata-update polling.
	var next int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params []string        `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request.Method != "eth_getTransactionReceipt" || len(request.Params) != 1 || request.Params[0] != hash.Hex() {
			t.Error("incorrect receipt lookup")
		}
		response := cases[next].response
		next++
		response["id"] = request.ID
		response["jsonrpc"] = "2.0"
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	client, err := rpc.DialHTTP(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			receipt, err := probeReceipt(context.Background(), client, hash)
			if (err != nil) != test.failed {
				t.Fatalf("error = %v", err)
			}
			if !test.failed && (receipt == nil) != test.pending {
				t.Fatalf("receipt = %#v", receipt)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := probeReceipt(ctx, client, hash); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	server.Close()
	if _, err := probeReceipt(context.Background(), client, hash); err == nil {
		t.Fatal("transport failure treated as pending")
	}
}
