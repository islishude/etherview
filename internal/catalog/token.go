package catalog

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
)

type rowScanner interface{ Scan(...any) error }

func (catalog *Postgres) TokenContract(ctx context.Context, chainID, addressText string) (TokenContract, error) {
	if err := validateChainID(chainID); err != nil {
		return TokenContract{}, err
	}
	address, _, err := checksumInputAddress(addressText)
	if err != nil {
		return TokenContract{}, err
	}
	tx, err := catalog.beginRead(ctx)
	if err != nil {
		return TokenContract{}, err
	}
	defer tx.Rollback()
	snapshot, err := readCanonicalSnapshot(ctx, tx, chainID)
	if err != nil {
		return TokenContract{}, err
	}
	if err := requireStage(ctx, tx, snapshot, StageToken); err != nil {
		return TokenContract{}, err
	}
	contract, err := catalog.tokenContractAtSnapshot(ctx, tx, snapshot, address)
	if err != nil {
		return TokenContract{}, err
	}
	if err := commitRead(tx); err != nil {
		return TokenContract{}, err
	}
	return contract, nil
}

func (catalog *Postgres) tokenContractAtSnapshot(ctx context.Context, tx *sql.Tx, snapshot Snapshot, address []byte) (TokenContract, error) {
	contract, err := catalog.scanTokenContract(tx.QueryRowContext(ctx, tokenContractSQL,
		snapshot.ChainID, address, snapshot.BlockNumber,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return TokenContract{}, ErrNotFound
	}
	if err != nil {
		return TokenContract{}, fmt.Errorf("query token contract: %w", err)
	}
	return contract, nil
}

func (catalog *Postgres) TokenContracts(ctx context.Context, request TokenListRequest) (TokenPage, error) {
	if err := validateChainID(request.ChainID); err != nil {
		return TokenPage{}, err
	}
	limit, err := catalog.pageLimit(request.Limit)
	if err != nil {
		return TokenPage{}, err
	}
	tx, err := catalog.beginRead(ctx)
	if err != nil {
		return TokenPage{}, err
	}
	defer tx.Rollback()

	var snapshot Snapshot
	afterAddress := make([]byte, 20)
	hasAfter := false
	if request.Cursor == "" {
		snapshot, err = readCanonicalSnapshot(ctx, tx, request.ChainID)
	} else {
		var cursor tokenListCursor
		if decodeErr := decodeCursor(request.Cursor, &cursor); decodeErr != nil || cursor.Version != cursorVersion || cursor.ChainID != request.ChainID {
			return TokenPage{}, ErrInvalidCursor
		}
		snapshot = Snapshot{ChainID: cursor.ChainID, BlockNumber: cursor.SnapshotNumber, BlockHash: cursor.SnapshotHash}
		afterAddress, err = decodeFixedHex(cursor.AfterAddress, 20)
		if err != nil || cursor.AfterAddress != "0x"+hex.EncodeToString(afterAddress) {
			return TokenPage{}, ErrInvalidCursor
		}
		hasAfter = true
		if err = validateCanonicalSnapshot(ctx, tx, snapshot); err != nil {
			return TokenPage{}, err
		}
	}
	if err != nil {
		return TokenPage{}, err
	}
	if err := requireStage(ctx, tx, snapshot, StageToken); err != nil {
		return TokenPage{}, err
	}
	rows, err := tx.QueryContext(ctx, tokenContractsSQL,
		request.ChainID, snapshot.BlockNumber, hasAfter, afterAddress, limit+1,
	)
	if err != nil {
		return TokenPage{}, fmt.Errorf("list token contracts: %w", err)
	}
	defer rows.Close()
	items := make([]TokenContract, 0, limit+1)
	for rows.Next() {
		contract, scanErr := catalog.scanTokenContract(rows)
		if scanErr != nil {
			return TokenPage{}, fmt.Errorf("scan token contract: %w", scanErr)
		}
		items = append(items, contract)
	}
	if err := rows.Err(); err != nil {
		return TokenPage{}, fmt.Errorf("iterate token contracts: %w", err)
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		lastAddress, _, decodeErr := checksumInputAddress(items[len(items)-1].Address)
		if decodeErr != nil {
			return TokenPage{}, ErrCorruptData
		}
		next, err = encodeCursor(tokenListCursor{
			Version: cursorVersion, ChainID: request.ChainID,
			SnapshotNumber: snapshot.BlockNumber, SnapshotHash: snapshot.BlockHash,
			AfterAddress: "0x" + hex.EncodeToString(lastAddress),
		})
		if err != nil {
			return TokenPage{}, err
		}
	}
	if err := commitRead(tx); err != nil {
		return TokenPage{}, err
	}
	return TokenPage{Items: items, NextCursor: next, Snapshot: snapshot}, nil
}

func (catalog *Postgres) scanTokenContract(row rowScanner) (TokenContract, error) {
	var (
		contract                     TokenContract
		address, codeHash, blockHash []byte
		name, symbol                 sql.NullString
		decimals                     sql.NullInt64
		totalSupply                  sql.NullString
	)
	if err := row.Scan(
		&contract.ChainID, &address, &codeHash, &contract.Standard, &contract.Confidence,
		&name, &symbol, &decimals, &totalSupply, &contract.MetadataState,
		&contract.ObservedBlockNumber, &blockHash, &contract.UpdatedAt,
	); err != nil {
		return TokenContract{}, err
	}
	if err := validateChainID(contract.ChainID); err != nil || !canonicalUint256(contract.ObservedBlockNumber) {
		return TokenContract{}, ErrCorruptData
	}
	checksummed, err := checksumAddressBytes(address)
	if err != nil {
		return TokenContract{}, err
	}
	contract.Address = checksummed
	contract.CodeHash, err = lowerHex(codeHash, 32)
	if err != nil {
		return TokenContract{}, err
	}
	contract.ObservedBlockHash, err = lowerHex(blockHash, 32)
	if err != nil {
		return TokenContract{}, err
	}
	switch contract.Standard {
	case "erc20", "erc721", "erc1155", "unknown":
	default:
		return TokenContract{}, ErrCorruptData
	}
	switch contract.Confidence {
	case "verified", "high", "inferred", "guess":
	default:
		return TokenContract{}, ErrCorruptData
	}
	switch contract.MetadataState {
	case "pending", "complete", "unavailable", "failed":
	default:
		return TokenContract{}, ErrCorruptData
	}
	if name.Valid {
		if len(name.String) > catalog.options.MaxTextBytes {
			return TokenContract{}, ErrLimitExceeded
		}
		contract.Name = &name.String
	}
	if symbol.Valid {
		if len(symbol.String) > catalog.options.MaxTextBytes {
			return TokenContract{}, ErrLimitExceeded
		}
		contract.Symbol = &symbol.String
	}
	if decimals.Valid {
		if decimals.Int64 < 0 || decimals.Int64 > 255 {
			return TokenContract{}, ErrCorruptData
		}
		value := uint8(decimals.Int64)
		contract.Decimals = &value
	}
	if totalSupply.Valid {
		if !canonicalUint256(totalSupply.String) {
			return TokenContract{}, ErrCorruptData
		}
		contract.TotalSupply = &totalSupply.String
	}
	return contract, nil
}

func (catalog *Postgres) TokenEvents(ctx context.Context, request TokenEventRequest) (TokenEventPage, error) {
	if err := validateChainID(request.ChainID); err != nil {
		return TokenEventPage{}, err
	}
	tokenAddress, _, err := checksumInputAddress(request.TokenAddress)
	if err != nil {
		return TokenEventPage{}, err
	}
	normalizedToken := "0x" + hex.EncodeToString(tokenAddress)
	limit, err := catalog.pageLimit(request.Limit)
	if err != nil {
		return TokenEventPage{}, err
	}
	tx, err := catalog.beginRead(ctx)
	if err != nil {
		return TokenEventPage{}, err
	}
	defer tx.Rollback()

	var snapshot Snapshot
	hasBoundary := false
	boundaryNumber, boundaryLog, boundarySub := "0", "0", "0"
	boundaryHash := make([]byte, 32)
	if request.Cursor == "" {
		snapshot, err = readCanonicalSnapshot(ctx, tx, request.ChainID)
	} else {
		var cursor tokenEventCursor
		if decodeErr := decodeCursor(request.Cursor, &cursor); decodeErr != nil || cursor.Version != cursorVersion ||
			cursor.ChainID != request.ChainID || cursor.TokenAddress != normalizedToken ||
			!canonicalUint256(cursor.BlockNumber) || !canonicalInt64(cursor.LogIndex) || !canonicalInt32(cursor.SubIndex) {
			return TokenEventPage{}, ErrInvalidCursor
		}
		snapshot = Snapshot{ChainID: cursor.ChainID, BlockNumber: cursor.SnapshotNumber, BlockHash: cursor.SnapshotHash}
		boundaryHash, err = decodeFixedHex(cursor.BlockHash, 32)
		if err != nil || cursor.BlockHash != "0x"+hex.EncodeToString(boundaryHash) {
			return TokenEventPage{}, ErrInvalidCursor
		}
		boundaryNumber, boundaryLog, boundarySub = cursor.BlockNumber, cursor.LogIndex, cursor.SubIndex
		hasBoundary = true
		if err = validateCanonicalSnapshot(ctx, tx, snapshot); err != nil {
			return TokenEventPage{}, err
		}
		if compareUnsignedDecimal(boundaryNumber, snapshot.BlockNumber) > 0 {
			return TokenEventPage{}, ErrInvalidCursor
		}
	}
	if err != nil {
		return TokenEventPage{}, err
	}
	if err := requireStage(ctx, tx, snapshot, StageToken); err != nil {
		return TokenEventPage{}, err
	}
	rows, err := tx.QueryContext(ctx, tokenEventsSQL,
		request.ChainID, snapshot.BlockNumber, tokenAddress, hasBoundary,
		boundaryNumber, boundaryLog, boundarySub, boundaryHash, limit+1,
	)
	if err != nil {
		return TokenEventPage{}, fmt.Errorf("list canonical token events: %w", err)
	}
	defer rows.Close()
	items := make([]TokenEvent, 0, limit+1)
	for rows.Next() {
		event, scanErr := scanTokenEvent(rows)
		if scanErr != nil {
			return TokenEventPage{}, fmt.Errorf("scan token event: %w", scanErr)
		}
		items = append(items, event)
	}
	if err := rows.Err(); err != nil {
		return TokenEventPage{}, fmt.Errorf("iterate token events: %w", err)
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		next, err = encodeCursor(tokenEventCursor{
			Version: cursorVersion, ChainID: request.ChainID, TokenAddress: normalizedToken,
			SnapshotNumber: snapshot.BlockNumber, SnapshotHash: snapshot.BlockHash,
			BlockNumber: last.BlockNumber, BlockHash: last.BlockHash,
			LogIndex: last.LogIndex, SubIndex: last.SubIndex,
		})
		if err != nil {
			return TokenEventPage{}, err
		}
	}
	if err := commitRead(tx); err != nil {
		return TokenEventPage{}, err
	}
	return TokenEventPage{Items: items, NextCursor: next, Snapshot: snapshot}, nil
}

func scanTokenEvent(row rowScanner) (TokenEvent, error) {
	var (
		event                           TokenEvent
		blockHash, txHash, tokenAddress []byte
		operator, from, to              []byte
		tokenID, amount                 sql.NullString
	)
	if err := row.Scan(
		&event.ChainID, &event.BlockNumber, &blockHash, &event.LogIndex, &event.SubIndex,
		&txHash, &tokenAddress, &event.Standard, &event.Kind, &operator, &from, &to,
		&tokenID, &amount, &event.Confidence,
	); err != nil {
		return TokenEvent{}, err
	}
	if err := validateChainID(event.ChainID); err != nil || !canonicalUint256(event.BlockNumber) ||
		!canonicalInt64(event.LogIndex) || !canonicalInt32(event.SubIndex) {
		return TokenEvent{}, ErrCorruptData
	}
	var err error
	if event.BlockHash, err = lowerHex(blockHash, 32); err != nil {
		return TokenEvent{}, err
	}
	if event.TransactionHash, err = lowerHex(txHash, 32); err != nil {
		return TokenEvent{}, err
	}
	if event.TokenAddress, err = checksumAddressBytes(tokenAddress); err != nil {
		return TokenEvent{}, err
	}
	if event.Operator, err = optionalChecksumAddress(operator); err != nil {
		return TokenEvent{}, err
	}
	if event.From, err = optionalChecksumAddress(from); err != nil {
		return TokenEvent{}, err
	}
	if event.To, err = optionalChecksumAddress(to); err != nil {
		return TokenEvent{}, err
	}
	switch event.Standard {
	case "erc20", "erc721", "erc1155":
	default:
		return TokenEvent{}, ErrCorruptData
	}
	switch event.Kind {
	case "transfer", "approval", "approval_for_all", "mint", "burn":
	default:
		return TokenEvent{}, ErrCorruptData
	}
	switch event.Confidence {
	case "verified", "high", "inferred", "guess":
	default:
		return TokenEvent{}, ErrCorruptData
	}
	if tokenID.Valid {
		if !canonicalUint256(tokenID.String) {
			return TokenEvent{}, ErrCorruptData
		}
		event.TokenID = &tokenID.String
	}
	if amount.Valid {
		if !canonicalUint256(amount.String) {
			return TokenEvent{}, ErrCorruptData
		}
		event.Amount = &amount.String
	}
	return event, nil
}

const tokenContractColumns = `
tc.chain_id::text, tc.address, tc.code_hash, tc.standard, tc.confidence,
tc.name, tc.symbol, tc.decimals, tc.total_supply::text, tc.metadata_state,
tc.observed_block_number::text, tc.observed_block_hash, tc.updated_at`

var tokenContractSQL = `
SELECT ` + tokenContractColumns + `
FROM token_contracts AS tc
JOIN canonical_blocks AS cb
  ON cb.chain_id = tc.chain_id
 AND cb.number = tc.observed_block_number
 AND cb.block_hash = tc.observed_block_hash
WHERE tc.chain_id = $1::numeric
  AND tc.address = $2
  AND tc.observed_block_number <= $3::numeric
ORDER BY tc.observed_block_number DESC, tc.code_hash DESC
LIMIT 1`

var tokenContractsSQL = `
WITH current_tokens AS (
    SELECT DISTINCT ON (tc.address) ` + tokenContractColumns + `
    FROM token_contracts AS tc
    JOIN canonical_blocks AS cb
      ON cb.chain_id = tc.chain_id
     AND cb.number = tc.observed_block_number
     AND cb.block_hash = tc.observed_block_hash
    WHERE tc.chain_id = $1::numeric
      AND tc.observed_block_number <= $2::numeric
      AND ($3::boolean = false OR tc.address > $4)
    ORDER BY tc.address, tc.observed_block_number DESC, tc.code_hash DESC
)
SELECT chain_id::text, address, code_hash, standard, confidence,
       name, symbol, decimals, total_supply::text, metadata_state,
       observed_block_number::text, observed_block_hash, updated_at
FROM current_tokens
ORDER BY address
LIMIT $5`

const tokenEventsSQL = `
SELECT e.chain_id::text, e.block_number::text, e.block_hash,
       e.log_index::text, e.sub_index::text, e.transaction_hash,
       e.token_address, e.standard, e.event_kind, e.operator,
       e.from_address, e.to_address, e.token_id::text, e.amount::text,
       e.confidence
FROM token_events AS e
JOIN canonical_blocks AS cb
  ON cb.chain_id = e.chain_id
 AND cb.number = e.block_number
 AND cb.block_hash = e.block_hash
WHERE e.chain_id = $1::numeric
  AND e.block_number <= $2::numeric
  AND e.token_address = $3
  AND e.canonical = true
  AND (
      $4::boolean = false OR
      (e.block_number, e.log_index, e.sub_index, e.block_hash) <
      ($5::numeric, $6::bigint, $7::integer, $8)
  )
ORDER BY e.block_number DESC, e.log_index DESC, e.sub_index DESC, e.block_hash DESC
LIMIT $9`
