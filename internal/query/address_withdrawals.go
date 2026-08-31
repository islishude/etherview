package query

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/publicquery"
)

type addressWithdrawalCursor struct {
	Version           int    `json:"v"`
	ChainID           string `json:"chain_id"`
	Address           string `json:"address"`
	SnapshotNumber    uint64 `json:"snapshot_number"`
	SnapshotHash      string `json:"snapshot_hash"`
	BeforeIndex       uint64 `json:"before_index"`
	BeforeBlockNumber uint64 `json:"before_block_number"`
	BeforeBlockHash   string `json:"before_block_hash"`
}

func (r *PostgresReader) AddressWithdrawals(
	ctx context.Context,
	rawAddress string,
	encodedCursor string,
	limit int,
) ([]gen.AddressWithdrawal, string, error) {
	if limit <= 0 || limit > 100 {
		return nil, "", fmt.Errorf("address withdrawal limit %d is outside 1..100", limit)
	}
	address, err := ethrpc.ParseAddress(rawAddress)
	if err != nil {
		return nil, "", fmt.Errorf("invalid address: %w", err)
	}
	normalizedAddress := strings.ToLower(address.Hex())
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, "", fmt.Errorf("begin stable address withdrawal query: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var cursor addressWithdrawalCursor
	if encodedCursor == "" {
		snapshot, snapshotErr := r.currentBlockCursor(ctx, tx)
		if snapshotErr != nil {
			return nil, "", snapshotErr
		}
		cursor = addressWithdrawalCursor{
			Version: 1, ChainID: r.chainID, Address: normalizedAddress,
			SnapshotNumber: snapshot.SnapshotNumber, SnapshotHash: snapshot.SnapshotHash,
		}
	} else {
		if err := publicquery.DecodeCursor(encodedCursor, &cursor); err != nil ||
			cursor.Version != 1 || cursor.ChainID != r.chainID || cursor.Address != normalizedAddress ||
			cursor.BeforeBlockNumber > cursor.SnapshotNumber {
			return nil, "", ErrInvalidCursor
		}
		if err := r.validateAddressWithdrawalCursor(ctx, tx, cursor, address); err != nil {
			return nil, "", err
		}
	}

	query := dbgen.ListAddressWithdrawalsFirst
	arguments := []any{r.chainID, address.Bytes(), strconv.FormatUint(cursor.SnapshotNumber, 10), limit + 1}
	if encodedCursor != "" {
		query = dbgen.ListAddressWithdrawalsAfter
		arguments = []any{r.chainID, address.Bytes(), strconv.FormatUint(cursor.SnapshotNumber, 10), strconv.FormatUint(cursor.BeforeIndex, 10), limit + 1}
	}
	rows, err := tx.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, "", fmt.Errorf("query canonical address withdrawal page: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	type record struct {
		model       gen.AddressWithdrawal
		index       uint64
		blockNumber uint64
		blockHash   common.Hash
	}
	records := make([]record, 0, limit+1)
	for rows.Next() {
		var indexText, validatorText, amountText, blockNumberText, timestampText string
		var storedAddress, blockHashBytes []byte
		if err := rows.Scan(
			&indexText, &validatorText, &storedAddress, &amountText,
			&blockNumberText, &blockHashBytes, &timestampText,
		); err != nil {
			return nil, "", fmt.Errorf("scan address withdrawal: %w", err)
		}
		index, err := canonicalUint64(indexText, "withdrawal index")
		if err != nil {
			return nil, "", err
		}
		if _, err := canonicalUint64(validatorText, "validator index"); err != nil {
			return nil, "", err
		}
		if _, err := canonicalUint64(amountText, "withdrawal amount"); err != nil {
			return nil, "", err
		}
		blockNumber, err := canonicalUint64(blockNumberText, "withdrawal block number")
		if err != nil {
			return nil, "", err
		}
		timestampSeconds, err := canonicalUint64(timestampText, "withdrawal block timestamp")
		if err != nil {
			return nil, "", err
		}
		timestamp, err := quantityTime(timestampSeconds)
		if err != nil {
			return nil, "", fmt.Errorf("decode withdrawal block timestamp: %w", err)
		}
		if len(storedAddress) != common.AddressLength || common.BytesToAddress(storedAddress) != address {
			return nil, "", errors.New("canonical address withdrawal query returned an invalid address")
		}
		blockHash, err := decodeHashBytes(blockHashBytes)
		if err != nil {
			return nil, "", fmt.Errorf("decode withdrawal block hash: %w", err)
		}
		records = append(records, record{
			model: gen.AddressWithdrawal{
				Index: indexText, ValidatorIndex: validatorText, Address: address.Hex(),
				Amount: amountText, BlockNumber: blockNumberText, BlockHash: blockHash.String(),
				BlockTimestamp: timestamp,
			},
			index: index, blockNumber: blockNumber, blockHash: blockHash,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate canonical address withdrawal page: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, "", fmt.Errorf("commit stable address withdrawal query: %w", err)
	}

	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}
	items := make([]gen.AddressWithdrawal, len(records))
	for index := range records {
		items[index] = records[index].model
	}
	if !hasMore || len(records) == 0 {
		return items, "", nil
	}
	last := records[len(records)-1]
	next, err := publicquery.EncodeCursor(addressWithdrawalCursor{
		Version: 1, ChainID: r.chainID, Address: normalizedAddress,
		SnapshotNumber: cursor.SnapshotNumber, SnapshotHash: cursor.SnapshotHash,
		BeforeIndex: last.index, BeforeBlockNumber: last.blockNumber, BeforeBlockHash: last.blockHash.String(),
	})
	if err != nil {
		return nil, "", fmt.Errorf("encode address withdrawal cursor: %w", err)
	}
	return items, next, nil
}

func canonicalUint64(value, field string) (uint64, error) {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || strconv.FormatUint(parsed, 10) != value {
		return 0, fmt.Errorf("stored %s is malformed", field)
	}
	return parsed, nil
}

func (r *PostgresReader) validateAddressWithdrawalCursor(
	ctx context.Context,
	tx *sql.Tx,
	cursor addressWithdrawalCursor,
	address common.Address,
) error {
	snapshotHash, err := ethrpc.ParseHash(cursor.SnapshotHash)
	if err != nil {
		return ErrInvalidCursor
	}
	beforeHash, err := ethrpc.ParseHash(cursor.BeforeBlockHash)
	if err != nil {
		return ErrInvalidCursor
	}
	var valid bool
	if err := tx.QueryRowContext(ctx, dbgen.ValidateAddressWithdrawalCursor,
		r.chainID,
		strconv.FormatUint(cursor.SnapshotNumber, 10), snapshotHash.Bytes(),
		address.Bytes(), strconv.FormatUint(cursor.BeforeIndex, 10),
		strconv.FormatUint(cursor.BeforeBlockNumber, 10), beforeHash.Bytes(),
	).Scan(&valid); err != nil {
		return fmt.Errorf("validate address withdrawal cursor: %w", err)
	}
	if !valid {
		return ErrInvalidCursor
	}
	return nil
}
