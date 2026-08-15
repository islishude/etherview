package enrich

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

const debugTraceBlockByHashMethod = "debug_traceBlockByHash"

var (
	errBlockTraceHistoryUnavailable = errors.New("block trace historical state unavailable")
	errBlockTraceTransactionFailed  = errors.New("block trace transaction failed")
)

type blockTraceResultWire struct {
	TxHash    *common.Hash
	Result    json.RawMessage
	Error     string
	hasResult bool
	hasError  bool
}

type blockTraceResult struct {
	result json.RawMessage
	err    error
}

// decodeBlockTraceResults binds a block trace response to the exact canonical
// transaction order loaded before the RPC call. Provider-controlled item error
// text is reduced to stable local categories and is never returned or retained.
func decodeBlockTraceResults(raw json.RawMessage, expected []common.Hash) ([]blockTraceResult, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, errors.New("block trace response is null")
	}
	var wires []blockTraceResultWire
	if err := json.Unmarshal(trimmed, &wires); err != nil || wires == nil {
		return nil, errors.New("block trace response is malformed")
	}
	if len(wires) != len(expected) {
		return nil, fmt.Errorf(
			"block trace response has %d transactions, expected %d",
			len(wires), len(expected),
		)
	}
	results := make([]blockTraceResult, len(wires))
	for index, wire := range wires {
		if wire.TxHash == nil {
			return nil, fmt.Errorf("block trace transaction %d has no hash", index)
		}
		if *wire.TxHash != expected[index] {
			return nil, fmt.Errorf("block trace transaction %d hash does not match canonical inclusion", index)
		}
		result := bytes.TrimSpace(wire.Result)
		if wire.hasResult == wire.hasError {
			return nil, fmt.Errorf("block trace transaction %d must contain exactly one of result or error", index)
		}
		if wire.hasError {
			if wire.Error == "" {
				return nil, fmt.Errorf("block trace transaction %d has an empty error", index)
			}
			results[index].err = classifyBlockTraceItemError(wire.Error)
			continue
		}
		if len(result) == 0 || bytes.Equal(result, []byte("null")) {
			return nil, fmt.Errorf("block trace transaction %d has a null result", index)
		}
		results[index].result = append(json.RawMessage(nil), result...)
	}
	return results, nil
}

func (wire *blockTraceResultWire) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if encoded, exists := fields["txHash"]; exists && !bytes.Equal(bytes.TrimSpace(encoded), []byte("null")) {
		var hash common.Hash
		if err := json.Unmarshal(encoded, &hash); err != nil {
			return err
		}
		wire.TxHash = &hash
	}
	if encoded, exists := fields["result"]; exists {
		wire.hasResult = true
		wire.Result = append(json.RawMessage(nil), encoded...)
	}
	if encoded, exists := fields["error"]; exists {
		wire.hasError = true
		if bytes.Equal(bytes.TrimSpace(encoded), []byte("null")) {
			return errors.New("block trace item error is null")
		}
		if err := json.Unmarshal(encoded, &wire.Error); err != nil {
			return err
		}
	}
	return nil
}

func classifyBlockTraceItemError(message string) error {
	message = strings.ToLower(message)
	if strings.Contains(message, "pruned") || strings.Contains(message, "historical state") ||
		strings.Contains(message, "missing trie") {
		return errBlockTraceHistoryUnavailable
	}
	return errBlockTraceTransactionFailed
}
