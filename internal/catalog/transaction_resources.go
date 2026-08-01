package catalog

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/chainbundle"
)

type transactionResourceCursor struct {
	Version         int    `json:"v"`
	Kind            string `json:"kind"`
	ChainID         string `json:"chain_id"`
	TransactionHash string `json:"transaction_hash"`
	BlockHash       string `json:"block_hash"`
	Generation      int64  `json:"generation"`
	Offset          int    `json:"offset"`
}

type transactionResourceResolution struct {
	identity   TransactionResourceIdentity
	blockHash  []byte
	txHash     []byte
	txIndex    int64
	canonical  bool
	generation int64
	offset     int
	limit      int
}

func (catalog *Postgres) TransactionTokenEvents(
	ctx context.Context,
	request TransactionResourceRequest,
) (TransactionTokenEventPage, error) {
	tx, resolution, err := catalog.beginTransactionResource(ctx, request, "token_transfers", StageToken)
	if err != nil {
		return TransactionTokenEventPage{}, err
	}
	defer tx.Rollback() //nolint:errcheck
	page := TransactionTokenEventPage{Identity: resolution.identity, Items: []TokenEvent{}}
	if resolution.identity.State == StageComplete {
		rows, queryErr := tx.QueryContext(ctx, transactionTokenEventsSQL,
			request.ChainID, resolution.blockHash, resolution.txHash,
			resolution.limit+1, resolution.offset,
		)
		if queryErr != nil {
			return TransactionTokenEventPage{}, fmt.Errorf("list transaction token events: %w", queryErr)
		}
		defer rows.Close() //nolint:errcheck
		for rows.Next() {
			event, scanErr := scanTokenEvent(rows)
			if scanErr != nil {
				return TransactionTokenEventPage{}, fmt.Errorf("scan transaction token event: %w", scanErr)
			}
			page.Items = append(page.Items, event)
		}
		if err := rows.Err(); err != nil {
			return TransactionTokenEventPage{}, fmt.Errorf("iterate transaction token events: %w", err)
		}
		if len(page.Items) > resolution.limit {
			page.Items = page.Items[:resolution.limit]
			page.NextCursor, err = resolution.nextCursor("token_transfers", resolution.offset+resolution.limit)
			if err != nil {
				return TransactionTokenEventPage{}, err
			}
		}
	}
	if err := commitRead(tx); err != nil {
		return TransactionTokenEventPage{}, err
	}
	return page, nil
}

func (catalog *Postgres) TransactionLogs(
	ctx context.Context,
	request TransactionResourceRequest,
) (TransactionLogPage, error) {
	tx, resolution, err := catalog.beginTransactionResource(ctx, request, "logs", StageCore)
	if err != nil {
		return TransactionLogPage{}, err
	}
	defer tx.Rollback() //nolint:errcheck
	page := TransactionLogPage{Identity: resolution.identity, Items: []TransactionLog{}}
	rows, err := tx.QueryContext(ctx, transactionLogsSQL,
		request.ChainID, resolution.blockHash, resolution.txHash,
		resolution.limit+1, resolution.offset,
	)
	if err != nil {
		return TransactionLogPage{}, fmt.Errorf("list transaction logs: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var raw []byte
		var logIndex int64
		if err := rows.Scan(&logIndex, &raw); err != nil {
			return TransactionLogPage{}, fmt.Errorf("scan transaction log: %w", err)
		}
		if logIndex < 0 {
			return TransactionLogPage{}, ErrCorruptData
		}
		blockNumber, parseErr := strconv.ParseUint(resolution.identity.BlockNumber, 10, 64)
		if parseErr != nil {
			return TransactionLogPage{}, ErrCorruptData
		}
		decoded, decodeErr := chainbundle.DecodeLog(
			raw, common.BytesToHash(resolution.txHash), common.BytesToHash(resolution.blockHash),
			blockNumber, uint64(resolution.txIndex), uint64(logIndex),
		)
		if decodeErr != nil {
			return TransactionLogPage{}, ErrCorruptData
		}
		topics := make([]string, len(decoded.Topics))
		for index := range decoded.Topics {
			topics[index] = decoded.Topics[index].Hex()
		}
		page.Items = append(page.Items, TransactionLog{
			Address: decoded.Address.Hex(), LogIndex: strconv.FormatInt(logIndex, 10),
			Topics: topics, Data: "0x" + hex.EncodeToString(decoded.Data),
		})
	}
	if err := rows.Err(); err != nil {
		return TransactionLogPage{}, fmt.Errorf("iterate transaction logs: %w", err)
	}
	if len(page.Items) > resolution.limit {
		page.Items = page.Items[:resolution.limit]
		page.NextCursor, err = resolution.nextCursor("logs", resolution.offset+resolution.limit)
		if err != nil {
			return TransactionLogPage{}, err
		}
	}
	if err := commitRead(tx); err != nil {
		return TransactionLogPage{}, err
	}
	return page, nil
}

func (catalog *Postgres) TransactionStateChanges(
	ctx context.Context,
	request TransactionResourceRequest,
) (TransactionStateChangePage, error) {
	tx, resolution, err := catalog.beginTransactionResource(ctx, request, "state_changes", StageStateDiff)
	if err != nil {
		return TransactionStateChangePage{}, err
	}
	defer tx.Rollback() //nolint:errcheck
	page := TransactionStateChangePage{Identity: resolution.identity, Items: []TransactionStateChange{}}
	if resolution.identity.State == StageComplete {
		rows, queryErr := tx.QueryContext(ctx, transactionStateChangesSQL,
			request.ChainID, resolution.blockHash, resolution.txHash,
			resolution.limit+1, resolution.offset,
		)
		if queryErr != nil {
			return TransactionStateChangePage{}, fmt.Errorf("list transaction state changes: %w", queryErr)
		}
		defer rows.Close() //nolint:errcheck
		for rows.Next() {
			var address, storageKey []byte
			var change TransactionStateChange
			var before, after sql.NullString
			if err := rows.Scan(&address, &change.Kind, &storageKey, &before, &after); err != nil {
				return TransactionStateChangePage{}, fmt.Errorf("scan transaction state change: %w", err)
			}
			if change.Address, err = checksumAddressBytes(address); err != nil {
				return TransactionStateChangePage{}, ErrCorruptData
			}
			if change.Kind != "balance" && change.Kind != "nonce" &&
				change.Kind != "code" && change.Kind != "storage" {
				return TransactionStateChangePage{}, ErrCorruptData
			}
			if change.Kind == "storage" {
				key, keyErr := lowerHex(storageKey)
				if keyErr != nil {
					return TransactionStateChangePage{}, ErrCorruptData
				}
				change.StorageKey = &key
			} else if len(storageKey) != 0 {
				return TransactionStateChangePage{}, ErrCorruptData
			}
			if before.Valid {
				change.Before = &before.String
			}
			if after.Valid {
				change.After = &after.String
			}
			if change.Before == nil && change.After == nil {
				return TransactionStateChangePage{}, ErrCorruptData
			}
			page.Items = append(page.Items, change)
		}
		if err := rows.Err(); err != nil {
			return TransactionStateChangePage{}, fmt.Errorf("iterate transaction state changes: %w", err)
		}
		if len(page.Items) > resolution.limit {
			page.Items = page.Items[:resolution.limit]
			page.NextCursor, err = resolution.nextCursor("state_changes", resolution.offset+resolution.limit)
			if err != nil {
				return TransactionStateChangePage{}, err
			}
		}
	}
	if err := commitRead(tx); err != nil {
		return TransactionStateChangePage{}, err
	}
	return page, nil
}

func (catalog *Postgres) beginTransactionResource(
	ctx context.Context,
	request TransactionResourceRequest,
	kind string,
	stage Stage,
) (*sql.Tx, transactionResourceResolution, error) {
	if err := validateChainID(request.ChainID); err != nil {
		return nil, transactionResourceResolution{}, err
	}
	txHash, err := decodeFixedHex(request.TransactionHash, 32)
	if err != nil {
		return nil, transactionResourceResolution{}, ErrInvalidInput
	}
	normalizedHash := "0x" + hex.EncodeToString(txHash)
	limit, err := catalog.pageLimit(request.Limit)
	if err != nil {
		return nil, transactionResourceResolution{}, err
	}
	tx, err := catalog.beginRead(ctx)
	if err != nil {
		return nil, transactionResourceResolution{}, err
	}
	var resolution transactionResourceResolution
	resolution.txHash = txHash
	resolution.limit = limit
	var blockNumber string
	var blockHash []byte
	err = tx.QueryRowContext(ctx, transactionResourceIdentitySQL, request.ChainID, txHash).Scan(
		&blockNumber, &blockHash, &resolution.txIndex, &resolution.canonical,
	)
	if errors.Is(err, sql.ErrNoRows) {
		tx.Rollback() //nolint:errcheck
		return nil, transactionResourceResolution{}, ErrNotFound
	}
	if err != nil {
		tx.Rollback() //nolint:errcheck
		return nil, transactionResourceResolution{}, fmt.Errorf("resolve transaction resource identity: %w", err)
	}
	blockHashText, err := lowerHex(blockHash)
	if err != nil || !canonicalUint256(blockNumber) || resolution.txIndex < 0 {
		tx.Rollback() //nolint:errcheck
		return nil, transactionResourceResolution{}, ErrCorruptData
	}
	resolution.blockHash = blockHash
	resolution.identity = TransactionResourceIdentity{
		ChainID: request.ChainID, BlockNumber: blockNumber, BlockHash: blockHashText,
		TransactionHash: normalizedHash, TransactionIndex: strconv.FormatInt(resolution.txIndex, 10),
		State: StageComplete,
	}
	if stage != StageCore {
		resolution.identity.State, resolution.generation, err = transactionStageState(
			ctx, tx, request.ChainID, blockNumber, blockHash, resolution.canonical, stage,
		)
		if err != nil {
			tx.Rollback() //nolint:errcheck
			return nil, transactionResourceResolution{}, err
		}
	}
	if request.Cursor != "" {
		var cursor transactionResourceCursor
		if decodeCursor(request.Cursor, &cursor) != nil || cursor.Version != cursorVersion ||
			cursor.Kind != kind || cursor.ChainID != request.ChainID ||
			cursor.TransactionHash != normalizedHash || cursor.BlockHash != blockHashText ||
			cursor.Generation != resolution.generation || cursor.Offset <= 0 {
			tx.Rollback() //nolint:errcheck
			return nil, transactionResourceResolution{}, ErrInvalidCursor
		}
		resolution.offset = cursor.Offset
	}
	return tx, resolution, nil
}

func transactionStageState(
	ctx context.Context,
	tx *sql.Tx,
	chainID, blockNumber string,
	blockHash []byte,
	canonical bool,
	stage Stage,
) (StageState, int64, error) {
	if !canonical {
		return StageMissing, 0, nil
	}
	var state string
	var generation int64
	err := tx.QueryRowContext(ctx, transactionStageStateSQL,
		chainID, blockNumber, blockHash, string(stage), stage.Version(),
	).Scan(&state, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		return StageMissing, 0, nil
	}
	if err != nil {
		return "", 0, fmt.Errorf("read transaction stage state: %w", err)
	}
	switch StageState(state) {
	case StageComplete, StageUnavailable, StageFailed:
		return StageState(state), generation, nil
	default:
		return "", 0, ErrCorruptData
	}
}

func (resolution transactionResourceResolution) nextCursor(kind string, offset int) (string, error) {
	return encodeCursor(transactionResourceCursor{
		Version: cursorVersion, Kind: kind, ChainID: resolution.identity.ChainID,
		TransactionHash: resolution.identity.TransactionHash, BlockHash: resolution.identity.BlockHash,
		Generation: resolution.generation, Offset: offset,
	})
}

const transactionResourceIdentitySQL = `
SELECT inclusion.block_number::text, inclusion.block_hash, inclusion.tx_index,
       (canonical.block_hash IS NOT NULL)
FROM transaction_inclusions AS inclusion
LEFT JOIN canonical_blocks AS canonical
  ON canonical.chain_id = inclusion.chain_id
 AND canonical.number = inclusion.block_number
 AND canonical.block_hash = inclusion.block_hash
WHERE inclusion.chain_id = $1::numeric AND inclusion.tx_hash = $2
ORDER BY (canonical.block_hash IS NOT NULL) DESC, inclusion.block_number DESC
LIMIT 1`

const transactionStageStateSQL = `
SELECT state, job_generation
FROM published_block_stage_results
WHERE chain_id = $1::numeric
  AND block_number = $2::numeric
  AND block_hash = $3
  AND stage = $4
  AND stage_version = $5`

const transactionTokenEventsSQL = `
SELECT event.chain_id::text, event.block_number::text, event.block_hash,
       event.log_index::text, event.sub_index::text, event.transaction_hash,
       event.token_address, event.standard, event.event_kind, event.operator,
       event.from_address, event.to_address, event.token_id::text, event.amount::text,
       event.confidence
FROM token_events AS event
WHERE event.chain_id = $1::numeric
  AND event.block_hash = $2
  AND event.transaction_hash = $3
  AND event.canonical = true
ORDER BY event.log_index, event.sub_index
LIMIT $4 OFFSET $5`

const transactionLogsSQL = `
SELECT log_index, raw
FROM logs
WHERE chain_id = $1::numeric AND block_hash = $2 AND tx_hash = $3
ORDER BY log_index
LIMIT $4 OFFSET $5`

const transactionStateChangesSQL = `
SELECT address, field_kind, storage_key, before_value, after_value
FROM transaction_state_changes
WHERE chain_id = $1::numeric
  AND block_hash = $2
  AND transaction_hash = $3
  AND canonical = true
ORDER BY address, field_kind, storage_key
LIMIT $4 OFFSET $5`
