package etherscan

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func (b *PostgresBackend) internalTransactions(ctx context.Context, values url.Values) ([]internalTransaction, error) {
	var addressBytes any
	rawAddress := strings.TrimSpace(values.Get("address"))
	if rawAddress != "" {
		_, parsed, err := parseAddressParameter(rawAddress, "address")
		if err != nil {
			return nil, err
		}
		addressBytes = parsed
	}
	var transactionHashBytes any
	rawHash := strings.TrimSpace(values.Get("txhash"))
	if rawHash != "" {
		_, hashBytes, parseErr := parseHashParameter(rawHash, "txhash")
		if parseErr != nil {
			return nil, parseErr
		}
		transactionHashBytes = hashBytes
	}
	if rawAddress != "" && rawHash != "" {
		return nil, invalidParameter("txlistinternal accepts address or txhash, not both")
	}
	if rawAddress == "" && rawHash == "" &&
		(strings.TrimSpace(values.Get("startblock")) == "" || strings.TrimSpace(values.Get("endblock")) == "") {
		return nil, invalidParameter("txlistinternal requires address, txhash, or an explicit startblock/endblock range")
	}
	if rawHash != "" && (strings.TrimSpace(values.Get("startblock")) != "" || strings.TrimSpace(values.Get("endblock")) != "") {
		return nil, invalidParameter("txhash mode does not accept a block range")
	}
	page, err := parsePagination(values)
	if err != nil {
		return nil, err
	}
	tx, err := b.beginEnrichmentSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var start string
	var end *string
	if rawHash != "" {
		block, err := b.canonicalTransactionBlock(ctx, tx, transactionHashBytes.([]byte))
		if errors.Is(err, ErrNotFound) {
			if _, coverageErr := b.requireCanonicalCoreRange(ctx, tx, "0", nil); coverageErr != nil {
				return nil, coverageErr
			}
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		start, end = block, &block
	} else {
		start, end, err = decimalRange(values)
		if err != nil {
			return nil, err
		}
	}
	if _, err := b.requireCanonicalStageRange(ctx, tx, traceStage, start, end, ErrTraceUnavailable); err != nil {
		return nil, err
	}
	var endArgument any
	if end != nil {
		endArgument = *end
	}
	query := fmt.Sprintf(internalTransactionsSQL,
		page.direction, page.direction, page.direction, page.direction, page.direction,
	)
	rows, err := tx.QueryContext(ctx, query,
		b.chain, addressBytes, transactionHashBytes, start, endArgument, page.limit, page.offset,
	)
	if err != nil {
		return nil, fmt.Errorf("query internal transactions: %w", err)
	}
	defer rows.Close()
	result := make([]internalTransaction, 0, page.limit)
	for rows.Next() {
		item, scanErr := scanInternalTransaction(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate internal transactions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close internal transactions: %w", err)
	}
	if len(result) == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit internal transaction snapshot: %w", err)
	}
	return result, nil
}

func (b *PostgresBackend) canonicalTransactionBlock(
	ctx context.Context,
	queryer enrichmentQueryer,
	hash []byte,
) (string, error) {
	var block string
	err := queryer.QueryRowContext(ctx, canonicalTransactionBlockSQL, b.chain, hash).Scan(&block)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("query canonical transaction block: %w", err)
	}
	if _, err := storedUint256(block, "transaction block number"); err != nil {
		return "", err
	}
	return block, nil
}

func scanInternalTransaction(scanner rowScanner) (internalTransaction, error) {
	var (
		blockNumber, timestamp, tracePath, callType string
		blockHash, transactionHash                  []byte
		from, to, created, input                    []byte
		depth                                       int64
		value, gas, gasUsed, traceError             sql.NullString
		reverted                                    bool
	)
	if err := scanner.Scan(
		&blockNumber, &blockHash, &transactionHash, &timestamp,
		&tracePath, &depth, &callType, &from, &to, &created,
		&value, &gas, &gasUsed, &input, &traceError, &reverted,
	); err != nil {
		return internalTransaction{}, fmt.Errorf("scan internal transaction: %w", err)
	}
	if _, err := storedUint256(blockNumber, "trace block number"); err != nil {
		return internalTransaction{}, err
	}
	if _, err := storedUint256(timestamp, "trace block timestamp"); err != nil {
		return internalTransaction{}, err
	}
	if depth <= 0 {
		return internalTransaction{}, errors.New("stored internal transaction depth is invalid")
	}
	components, err := validateTracePath(tracePath)
	if err != nil || int64(components) != depth {
		return internalTransaction{}, errors.New("stored internal transaction trace path is invalid")
	}
	if _, err := hashFromBytes(blockHash); err != nil {
		return internalTransaction{}, err
	}
	indexedTransactionHash, err := hashFromBytes(transactionHash)
	if err != nil {
		return internalTransaction{}, err
	}
	item := internalTransaction{
		BlockNumber: blockNumber,
		TimeStamp:   timestamp,
		Hash:        strings.ToLower(indexedTransactionHash.String()),
		Value:       "0",
		Gas:         "0",
		GasUsed:     "0",
		Input:       "",
		Type:        strings.ToLower(strings.TrimSpace(callType)),
		TraceID:     strings.ReplaceAll(tracePath, ".", "_"),
		IsError:     "0",
	}
	if item.Type == "" {
		return internalTransaction{}, errors.New("stored internal transaction call type is empty")
	}
	switch item.Type {
	case "call", "callcode", "delegatecall", "staticcall", "create", "create2", "selfdestruct", "reward":
	default:
		return internalTransaction{}, errors.New("stored internal transaction call type is invalid")
	}
	if item.From, err = optionalChecksumAddress(from); err != nil {
		return internalTransaction{}, fmt.Errorf("checksum internal transaction sender: %w", err)
	}
	if item.To, err = optionalChecksumAddress(to); err != nil {
		return internalTransaction{}, fmt.Errorf("checksum internal transaction recipient: %w", err)
	}
	if item.ContractAddress, err = optionalChecksumAddress(created); err != nil {
		return internalTransaction{}, fmt.Errorf("checksum internal transaction created contract: %w", err)
	}
	if item.To == "" && item.ContractAddress == "" && item.Type != "selfdestruct" && item.Type != "reward" {
		return internalTransaction{}, errors.New("stored internal transaction has no recipient")
	}
	for name, source := range map[string]sql.NullString{
		"value": value, "gas": gas, "gas used": gasUsed,
	} {
		if !source.Valid {
			continue
		}
		parsed, parseErr := storedUint256(source.String, "trace "+name)
		if parseErr != nil {
			return internalTransaction{}, parseErr
		}
		switch name {
		case "value":
			item.Value = parsed.String()
		case "gas":
			item.Gas = parsed.String()
		case "gas used":
			item.GasUsed = parsed.String()
		}
	}
	if input != nil {
		item.Input = "0x" + hex.EncodeToString(input)
	}
	if traceError.Valid {
		if len(traceError.String) > 1<<20 {
			return internalTransaction{}, errors.New("stored internal transaction error is too large")
		}
		if !reverted {
			return internalTransaction{}, errors.New("stored internal transaction error is inconsistent")
		}
		item.ErrCode = traceError.String
	}
	if reverted {
		item.IsError = "1"
		if item.ErrCode == "" {
			item.ErrCode = "execution reverted"
		}
	}
	return item, nil
}

func optionalChecksumAddress(raw []byte) (string, error) {
	if raw == nil {
		return "", nil
	}
	address, err := addressFromBytes(raw)
	if err != nil {
		return "", err
	}
	return checksumAddress(address)
}

func validateTracePath(value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	parts := strings.Split(value, ".")
	if len(parts) > 128 {
		return 0, errors.New("trace path is too deep")
	}
	for _, part := range parts {
		if part == "" || len(part) > 1 && part[0] == '0' {
			return 0, errors.New("trace path is not canonical")
		}
		if _, err := strconv.ParseUint(part, 10, 32); err != nil {
			return 0, errors.New("trace path component is invalid")
		}
	}
	return len(parts), nil
}

const internalTransactionsSQL = `
SELECT trace.block_number::text, trace.block_hash, trace.transaction_hash,
       block.timestamp::text, trace.trace_path, trace.depth, trace.call_type,
       trace.from_address, trace.to_address, trace.created_address,
       trace.value::text, trace.gas::text, trace.gas_used::text,
       trace.input, trace.error, trace.reverted
FROM normalized_traces AS trace
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = trace.chain_id
 AND canonical.number = trace.block_number
 AND canonical.block_hash = trace.block_hash
JOIN blocks AS block
  ON block.chain_id = trace.chain_id
 AND block.number = trace.block_number
 AND block.hash = trace.block_hash
WHERE trace.chain_id = $1::numeric
  AND trace.canonical = TRUE
  AND trace.depth > 0
  AND ($2::bytea IS NULL OR trace.from_address = $2 OR trace.to_address = $2 OR trace.created_address = $2)
  AND ($3::bytea IS NULL OR trace.transaction_hash = $3)
  AND trace.block_number >= $4::numeric
  AND ($5::numeric IS NULL OR trace.block_number <= $5::numeric)
ORDER BY trace.block_number %s, trace.transaction_index %s,
         string_to_array(trace.trace_path, '.')::bigint[] %s,
         trace.block_hash %s, trace.transaction_hash %s
LIMIT $6 OFFSET $7`

const canonicalTransactionBlockSQL = `
SELECT inclusion.block_number::text
FROM transaction_inclusions AS inclusion
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = inclusion.chain_id
 AND canonical.number = inclusion.block_number
 AND canonical.block_hash = inclusion.block_hash
WHERE inclusion.chain_id = $1::numeric
  AND inclusion.tx_hash = $2
LIMIT 1`
