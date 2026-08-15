//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
)

type integrationBlockTraceResult struct {
	TxHash common.Hash     `json:"txHash"`
	Result json.RawMessage `json:"result"`
}

func marshalIntegrationBlockTraceResults(
	hashes []common.Hash,
	result func(common.Hash) (json.RawMessage, error),
) (json.RawMessage, error) {
	items := make([]integrationBlockTraceResult, len(hashes))
	for index, hash := range hashes {
		raw, err := result(hash)
		if err != nil {
			return nil, err
		}
		items[index] = integrationBlockTraceResult{
			TxHash: hash,
			Result: append(json.RawMessage(nil), raw...),
		}
	}
	return json.Marshal(items)
}

func marshalDatabaseBlockTraceResults(
	ctx context.Context,
	db *sql.DB,
	blockHash common.Hash,
	result func(common.Hash) (json.RawMessage, error),
) (json.RawMessage, error) {
	if db == nil {
		return nil, fmt.Errorf("block trace fixture database is not configured")
	}
	rows, err := db.QueryContext(ctx, `
		SELECT tx_hash
		FROM transaction_inclusions
		WHERE chain_id = 1 AND block_hash = $1
		ORDER BY tx_index`, blockHash.Bytes())
	if err != nil {
		return nil, fmt.Errorf("read block trace transaction inclusions: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	hashes := make([]common.Hash, 0)
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			return nil, fmt.Errorf("scan block trace transaction inclusion: %w", err)
		}
		if len(encoded) != common.HashLength {
			return nil, fmt.Errorf("block trace transaction hash has %d bytes", len(encoded))
		}
		hashes = append(hashes, common.BytesToHash(encoded))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate block trace transaction inclusions: %w", err)
	}
	return marshalIntegrationBlockTraceResults(hashes, result)
}

func integrationPrestateTraceResult(
	stateDiff json.RawMessage,
	options map[string]any,
) (json.RawMessage, error) {
	tracerConfig, ok := options["tracerConfig"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("prestate trace fixture omitted tracerConfig")
	}
	diffMode, ok := tracerConfig["diffMode"].(bool)
	if !ok {
		return nil, fmt.Errorf("prestate trace fixture omitted diffMode")
	}
	if diffMode {
		return append(json.RawMessage(nil), stateDiff...), nil
	}
	var envelope struct {
		Pre json.RawMessage `json:"pre"`
	}
	if err := json.Unmarshal(stateDiff, &envelope); err != nil {
		return nil, fmt.Errorf("decode prestate trace fixture: %w", err)
	}
	if len(envelope.Pre) == 0 {
		return nil, fmt.Errorf("prestate trace fixture omitted pre")
	}
	return append(json.RawMessage(nil), envelope.Pre...), nil
}
