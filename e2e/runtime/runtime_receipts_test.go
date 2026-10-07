//go:build runtimee2e

package runtimee2e

import (
	"reflect"
	"testing"
)

func TestNormalizeAnvilReceipts(t *testing.T) {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"result": []any{
			map[string]any{
				"transactionHash": "0x01",
				"blobGasPrice":    "0x1",
			},
			map[string]any{
				"transactionHash": "0x03",
			},
			map[string]any{
				"transactionHash": "0x02",
				"blobGasPrice":    "0x2",
				"blobGasUsed":     "0x3",
			},
		},
	}
	if got := normalizeAnvilReceipts(payload); got != 1 {
		t.Fatalf("normalized receipts = %d, want 1", got)
	}
	receipts := payload["result"].([]any)
	first := receipts[0].(map[string]any)
	if _, present := first["blobGasPrice"]; present {
		t.Fatal("orphan blobGasPrice was not removed")
	}
	ordinary := receipts[1].(map[string]any)
	if !reflect.DeepEqual(ordinary, map[string]any{"transactionHash": "0x03"}) {
		t.Fatalf("ordinary receipt changed: %#v", ordinary)
	}
	second := receipts[2].(map[string]any)
	if second["blobGasPrice"] != "0x2" || second["blobGasUsed"] != "0x3" {
		t.Fatalf("complete blob fee observation changed: %#v", second)
	}
	if got := normalizeAnvilReceipts(payload); got != 0 {
		t.Fatalf("complete receipts required normalization: %d", got)
	}
}
