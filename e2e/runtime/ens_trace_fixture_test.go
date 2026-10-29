//go:build runtimee2e

package runtimee2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"
)

// Anvil's callTracer reports transaction-local log indices; Geth reports block
// indices. Convert only indices whose complete log matches the real receipt.
// Production trace validation remains unchanged and rejects any other mismatch.
func normalizeAnvilTraceFrame(frame map[string]any, receipt map[string]any) error {
	logs, _ := receipt["logs"].([]any)
	captured, _ := frame["logs"].([]any)
	for _, raw := range captured {
		log, ok := raw.(map[string]any)
		if !ok {
			return errors.New("invalid Anvil trace log")
		}
		index, ok := log["index"].(string)
		n, err := hexutil.DecodeUint64(index)
		if !ok || err != nil || n >= uint64(len(logs)) {
			return errors.New("Anvil trace index outside receipt")
		}
		expected, ok := logs[n].(map[string]any)
		if !ok {
			return errors.New("invalid Anvil receipt log")
		}
		for _, key := range []string{"address", "topics", "data"} {
			if !reflect.DeepEqual(log[key], expected[key]) {
				return errors.New("Anvil trace differs from receipt")
			}
		}
		log["index"] = expected["logIndex"]
	}
	calls, _ := frame["calls"].([]any)
	for _, raw := range calls {
		child, ok := raw.(map[string]any)
		if !ok {
			return errors.New("invalid Anvil trace frame")
		}
		if err := normalizeAnvilTraceFrame(child, receipt); err != nil {
			return err
		}
	}
	return nil
}
func (p *receiptProxy) normalizeTraceIndices(ctx context.Context, requestBody []byte, payload any) error {
	var request struct {
		Method string
		Params []json.RawMessage
	}
	if json.Unmarshal(requestBody, &request) != nil || len(request.Params) != 2 || (request.Method != "debug_traceBlockByHash" && request.Method != "debug_traceTransaction") {
		return nil
	}
	var options struct {
		Tracer       string
		TracerConfig struct{ WithLog bool }
	}
	if json.Unmarshal(request.Params[1], &options) != nil || options.Tracer != "callTracer" || !options.TracerConfig.WithLog {
		return nil
	}
	method := "eth_getBlockReceipts"
	if request.Method == "debug_traceTransaction" {
		method = "eth_getTransactionReceipt"
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": request.Params[:1]})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.upstream, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	var envelope struct{ Result json.RawMessage }
	if err = json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&envelope); err != nil {
		return err
	}
	result, ok := payload.(map[string]any)
	if !ok {
		return errors.New("invalid Anvil trace envelope")
	}
	if request.Method == "debug_traceTransaction" {
		var receipt map[string]any
		if err = json.Unmarshal(envelope.Result, &receipt); err != nil {
			return err
		}
		frame, ok := result["result"].(map[string]any)
		if !ok {
			return errors.New("invalid Anvil transaction trace")
		}
		return normalizeAnvilTraceFrame(frame, receipt)
	}
	var receipts []map[string]any
	if err = json.Unmarshal(envelope.Result, &receipts); err != nil {
		return err
	}
	byHash := map[string]map[string]any{}
	for _, receipt := range receipts {
		hash, _ := receipt["transactionHash"].(string)
		byHash[hash] = receipt
	}
	traces, ok := result["result"].([]any)
	if !ok {
		return errors.New("invalid Anvil block trace")
	}
	for _, raw := range traces {
		trace, ok := raw.(map[string]any)
		if !ok {
			return errors.New("invalid Anvil block trace entry")
		}
		hash, _ := trace["txHash"].(string)
		frame, ok := trace["result"].(map[string]any)
		if !ok || byHash[hash] == nil {
			return errors.New("Anvil trace lacks matching receipt")
		}
		if err = normalizeAnvilTraceFrame(frame, byHash[hash]); err != nil {
			return err
		}
	}
	return nil
}
func TestNormalizeAnvilTraceIndices(t *testing.T) {
	log := map[string]any{"index": "0x0", "address": "0xab", "topics": []any{"0xcd"}, "data": "0xef"}
	receipt := map[string]any{"logs": []any{map[string]any{"logIndex": "0x8", "address": "0xab", "topics": []any{"0xcd"}, "data": "0xef"}}}
	frame := map[string]any{"calls": []any{map[string]any{"logs": []any{log}}}}
	if err := normalizeAnvilTraceFrame(frame, receipt); err != nil || log["index"] != "0x8" {
		t.Fatal(log, err)
	}
	log["index"] = "0x0"
	log["data"] = "0xff"
	if normalizeAnvilTraceFrame(frame, receipt) == nil {
		t.Fatal("mismatched log was normalized")
	}
	log["index"] = "0x1"
	if normalizeAnvilTraceFrame(frame, receipt) == nil {
		t.Fatal("out of bounds log was normalized")
	}
}
