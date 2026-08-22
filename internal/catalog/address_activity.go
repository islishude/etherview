package catalog

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"github.com/islishude/etherview/internal/db/gen"
	"math"
	"strconv"
	"time"
)

type addressInternalCursor struct {
	Version          int    `json:"v"`
	Kind             string `json:"kind"`
	ChainID          string `json:"chain_id"`
	Address          string `json:"address"`
	SnapshotNumber   string `json:"snapshot_number"`
	SnapshotHash     string `json:"snapshot_hash"`
	BlockNumber      string `json:"block_number"`
	BlockHash        string `json:"block_hash"`
	TransactionHash  string `json:"transaction_hash"`
	TransactionIndex string `json:"transaction_index"`
	TracePath        string `json:"trace_path"`
}

type addressTokenCursor struct {
	Version          int    `json:"v"`
	Kind             string `json:"kind"`
	ChainID          string `json:"chain_id"`
	Address          string `json:"address"`
	SnapshotNumber   string `json:"snapshot_number"`
	SnapshotHash     string `json:"snapshot_hash"`
	BlockNumber      string `json:"block_number"`
	BlockHash        string `json:"block_hash"`
	TransactionHash  string `json:"transaction_hash"`
	TransactionIndex string `json:"transaction_index"`
	LogIndex         string `json:"log_index"`
	SubIndex         string `json:"sub_index"`
}

func (catalog *Postgres) AddressInternalTransactions(
	ctx context.Context,
	request AddressActivityRequest,
) (AddressInternalTransactionPage, error) {
	if err := validateChainID(request.ChainID); err != nil {
		return AddressInternalTransactionPage{}, err
	}
	address, normalizedAddress, err := checksumInputAddress(request.Address)
	if err != nil {
		return AddressInternalTransactionPage{}, err
	}
	limit, err := catalog.pageLimit(request.Limit)
	if err != nil {
		return AddressInternalTransactionPage{}, err
	}
	tx, err := catalog.beginRead(ctx)
	if err != nil {
		return AddressInternalTransactionPage{}, err
	}
	defer tx.Rollback() //nolint:errcheck

	snapshot, boundary, hasBoundary, err := catalog.resolveAddressInternalCursor(
		ctx, tx, request, normalizedAddress,
	)
	if err != nil {
		return AddressInternalTransactionPage{}, err
	}
	if err := requireStage(ctx, tx, snapshot, StageTrace); err != nil {
		return AddressInternalTransactionPage{}, err
	}
	blockHash, txHash, err := addressInternalBoundaryBytes(boundary, hasBoundary)
	if err != nil {
		return AddressInternalTransactionPage{}, err
	}
	rows, err := tx.QueryContext(ctx, dbgen.CatalogAddressInternalTransactions, request.ChainID, snapshot.BlockNumber, address, hasBoundary,
		boundary.BlockNumber, boundary.TransactionIndex, boundary.TracePath,
		blockHash, txHash, limit+1,
	)
	if err != nil {
		return AddressInternalTransactionPage{}, fmt.Errorf("list address internal transactions: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	items := make([]AddressInternalTransaction, 0, limit+1)
	for rows.Next() {
		item, scanErr := catalog.scanAddressInternalTransaction(rows)
		if scanErr != nil {
			return AddressInternalTransactionPage{}, fmt.Errorf("scan address internal transaction: %w", scanErr)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return AddressInternalTransactionPage{}, fmt.Errorf("iterate address internal transactions: %w", err)
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		next, err = encodeCursor(addressInternalCursor{
			Version: cursorVersion, Kind: "internal", ChainID: request.ChainID,
			Address: normalizedAddress, SnapshotNumber: snapshot.BlockNumber, SnapshotHash: snapshot.BlockHash,
			BlockNumber: last.BlockNumber, BlockHash: last.BlockHash,
			TransactionHash: last.TransactionHash, TransactionIndex: last.TransactionIndex,
			TracePath: tracePathText(last.Path),
		})
		if err != nil {
			return AddressInternalTransactionPage{}, err
		}
	}
	if err := commitRead(tx); err != nil {
		return AddressInternalTransactionPage{}, err
	}
	return AddressInternalTransactionPage{Items: items, NextCursor: next, Snapshot: snapshot}, nil
}

func (catalog *Postgres) resolveAddressInternalCursor(
	ctx context.Context,
	tx *sql.Tx,
	request AddressActivityRequest,
	normalizedAddress string,
) (Snapshot, addressInternalCursor, bool, error) {
	if request.Cursor == "" {
		snapshot, err := readCanonicalSnapshot(ctx, tx, request.ChainID)
		return snapshot, addressInternalCursor{
			BlockNumber: "0", TransactionIndex: "0", TracePath: "0",
		}, false, err
	}
	var cursor addressInternalCursor
	if decodeCursor(request.Cursor, &cursor) != nil || cursor.Version != cursorVersion ||
		cursor.Kind != "internal" || cursor.ChainID != request.ChainID ||
		cursor.Address != normalizedAddress || !canonicalUint256(cursor.BlockNumber) ||
		!canonicalUint256(cursor.TransactionIndex) || len(cursor.TracePath) > catalog.options.MaxTextBytes {
		return Snapshot{}, addressInternalCursor{}, false, ErrInvalidCursor
	}
	if _, err := parseTracePath(cursor.TracePath); err != nil {
		return Snapshot{}, addressInternalCursor{}, false, ErrInvalidCursor
	}
	snapshot := Snapshot{
		ChainID: cursor.ChainID, BlockNumber: cursor.SnapshotNumber, BlockHash: cursor.SnapshotHash,
	}
	if err := validateCanonicalSnapshot(ctx, tx, snapshot); err != nil {
		return Snapshot{}, addressInternalCursor{}, false, err
	}
	if compareUnsignedDecimal(cursor.BlockNumber, snapshot.BlockNumber) > 0 {
		return Snapshot{}, addressInternalCursor{}, false, ErrInvalidCursor
	}
	return snapshot, cursor, true, nil
}

func addressInternalBoundaryBytes(cursor addressInternalCursor, hasBoundary bool) ([]byte, []byte, error) {
	if !hasBoundary {
		return make([]byte, 32), make([]byte, 32), nil
	}
	blockHash, err := decodeFixedHex(cursor.BlockHash, 32)
	if err != nil || cursor.BlockHash != "0x"+hex.EncodeToString(blockHash) {
		return nil, nil, ErrInvalidCursor
	}
	txHash, err := decodeFixedHex(cursor.TransactionHash, 32)
	if err != nil || cursor.TransactionHash != "0x"+hex.EncodeToString(txHash) {
		return nil, nil, ErrInvalidCursor
	}
	return blockHash, txHash, nil
}

func (catalog *Postgres) scanAddressInternalTransaction(row rowScanner) (AddressInternalTransaction, error) {
	var (
		item                       AddressInternalTransaction
		blockHash, txHash          []byte
		timestamp, path            string
		depth                      int64
		from, to, created, input   []byte
		value, gas, gasUsed, cause sql.NullString
	)
	if err := row.Scan(
		&item.BlockNumber, &blockHash, &timestamp, &txHash, &item.TransactionIndex,
		&path, &depth, &item.CallType, &from, &to, &created,
		&value, &gas, &gasUsed, &input, &cause, &item.Reverted,
	); err != nil {
		return AddressInternalTransaction{}, err
	}
	if !canonicalUint256(item.BlockNumber) || !canonicalUint256(item.TransactionIndex) ||
		depth <= 0 || depth > 128 || item.CallType == "" || len(item.CallType) > 128 {
		return AddressInternalTransaction{}, ErrCorruptData
	}
	seconds, err := strconv.ParseUint(timestamp, 10, 64)
	if err != nil || seconds > math.MaxInt64 {
		return AddressInternalTransaction{}, ErrCorruptData
	}
	item.BlockTimestamp = time.Unix(int64(seconds), 0).UTC()
	if item.BlockHash, err = lowerHex(blockHash); err != nil {
		return AddressInternalTransaction{}, err
	}
	if item.TransactionHash, err = lowerHex(txHash); err != nil {
		return AddressInternalTransaction{}, err
	}
	item.Path, err = parseTracePath(path)
	if err != nil || len(item.Path) != int(depth) {
		return AddressInternalTransaction{}, ErrCorruptData
	}
	item.Depth = uint32(depth)
	if item.From, err = optionalChecksumAddress(from); err != nil {
		return AddressInternalTransaction{}, err
	}
	if item.To, err = optionalChecksumAddress(to); err != nil {
		return AddressInternalTransaction{}, err
	}
	if item.CreatedAddress, err = optionalChecksumAddress(created); err != nil {
		return AddressInternalTransaction{}, err
	}
	for _, optional := range []struct {
		source      sql.NullString
		destination **string
	}{
		{value, &item.Value}, {gas, &item.Gas}, {gasUsed, &item.GasUsed},
	} {
		if optional.source.Valid {
			if !canonicalUint256(optional.source.String) {
				return AddressInternalTransaction{}, ErrCorruptData
			}
			copy := optional.source.String
			*optional.destination = &copy
		}
	}
	if input != nil {
		encoded := "0x" + hex.EncodeToString(input)
		item.Input = &encoded
	}
	if cause.Valid {
		if len(cause.String) > catalog.options.MaxTextBytes {
			return AddressInternalTransaction{}, ErrLimitExceeded
		}
		item.Error = &cause.String
	}
	return item, nil
}

func (catalog *Postgres) AddressERC20Transfers(
	ctx context.Context,
	request AddressActivityRequest,
) (AddressTokenTransferPage, error) {
	return catalog.addressTokenTransfers(ctx, request, "erc20")
}

func (catalog *Postgres) AddressNFTTransfers(
	ctx context.Context,
	request AddressActivityRequest,
) (AddressTokenTransferPage, error) {
	return catalog.addressTokenTransfers(ctx, request, "nft")
}

func (catalog *Postgres) addressTokenTransfers(
	ctx context.Context,
	request AddressActivityRequest,
	kind string,
) (AddressTokenTransferPage, error) {
	if err := validateChainID(request.ChainID); err != nil {
		return AddressTokenTransferPage{}, err
	}
	address, normalizedAddress, err := checksumInputAddress(request.Address)
	if err != nil {
		return AddressTokenTransferPage{}, err
	}
	limit, err := catalog.pageLimit(request.Limit)
	if err != nil {
		return AddressTokenTransferPage{}, err
	}
	tx, err := catalog.beginRead(ctx)
	if err != nil {
		return AddressTokenTransferPage{}, err
	}
	defer tx.Rollback() //nolint:errcheck
	snapshot, boundary, hasBoundary, err := catalog.resolveAddressTokenCursor(
		ctx, tx, request, normalizedAddress, kind,
	)
	if err != nil {
		return AddressTokenTransferPage{}, err
	}
	if err := requireStage(ctx, tx, snapshot, StageToken); err != nil {
		return AddressTokenTransferPage{}, err
	}
	blockHash, txHash, err := addressTokenBoundaryBytes(boundary, hasBoundary)
	if err != nil {
		return AddressTokenTransferPage{}, err
	}
	rows, err := tx.QueryContext(ctx, dbgen.CatalogAddressTokenTransfers, request.ChainID, snapshot.BlockNumber, address, kind, hasBoundary,
		boundary.BlockNumber, boundary.TransactionIndex, boundary.LogIndex, boundary.SubIndex,
		blockHash, txHash, limit+1,
	)
	if err != nil {
		return AddressTokenTransferPage{}, fmt.Errorf("list address %s transfers: %w", kind, err)
	}
	defer rows.Close() //nolint:errcheck
	items := make([]AddressTokenTransfer, 0, limit+1)
	for rows.Next() {
		item, scanErr := catalog.scanAddressTokenTransfer(rows)
		if scanErr != nil {
			return AddressTokenTransferPage{}, fmt.Errorf("scan address %s transfer: %w", kind, scanErr)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return AddressTokenTransferPage{}, fmt.Errorf("iterate address %s transfers: %w", kind, err)
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		next, err = encodeCursor(addressTokenCursor{
			Version: cursorVersion, Kind: kind, ChainID: request.ChainID, Address: normalizedAddress,
			SnapshotNumber: snapshot.BlockNumber, SnapshotHash: snapshot.BlockHash,
			BlockNumber: last.BlockNumber, BlockHash: last.BlockHash,
			TransactionHash: last.TransactionHash, TransactionIndex: last.TransactionIndex,
			LogIndex: last.LogIndex, SubIndex: last.SubIndex,
		})
		if err != nil {
			return AddressTokenTransferPage{}, err
		}
	}
	if err := commitRead(tx); err != nil {
		return AddressTokenTransferPage{}, err
	}
	return AddressTokenTransferPage{Items: items, NextCursor: next, Snapshot: snapshot}, nil
}

func (catalog *Postgres) resolveAddressTokenCursor(
	ctx context.Context,
	tx *sql.Tx,
	request AddressActivityRequest,
	normalizedAddress string,
	kind string,
) (Snapshot, addressTokenCursor, bool, error) {
	if request.Cursor == "" {
		snapshot, err := readCanonicalSnapshot(ctx, tx, request.ChainID)
		return snapshot, addressTokenCursor{
			BlockNumber: "0", TransactionIndex: "0", LogIndex: "0", SubIndex: "0",
		}, false, err
	}
	var cursor addressTokenCursor
	if decodeCursor(request.Cursor, &cursor) != nil || cursor.Version != cursorVersion ||
		cursor.Kind != kind || cursor.ChainID != request.ChainID ||
		cursor.Address != normalizedAddress || !canonicalUint256(cursor.BlockNumber) ||
		!canonicalUint256(cursor.TransactionIndex) || !canonicalUint256(cursor.LogIndex) ||
		!canonicalUint256(cursor.SubIndex) {
		return Snapshot{}, addressTokenCursor{}, false, ErrInvalidCursor
	}
	snapshot := Snapshot{
		ChainID: cursor.ChainID, BlockNumber: cursor.SnapshotNumber, BlockHash: cursor.SnapshotHash,
	}
	if err := validateCanonicalSnapshot(ctx, tx, snapshot); err != nil {
		return Snapshot{}, addressTokenCursor{}, false, err
	}
	if compareUnsignedDecimal(cursor.BlockNumber, snapshot.BlockNumber) > 0 {
		return Snapshot{}, addressTokenCursor{}, false, ErrInvalidCursor
	}
	return snapshot, cursor, true, nil
}

func addressTokenBoundaryBytes(cursor addressTokenCursor, hasBoundary bool) ([]byte, []byte, error) {
	if !hasBoundary {
		return make([]byte, 32), make([]byte, 32), nil
	}
	blockHash, err := decodeFixedHex(cursor.BlockHash, 32)
	if err != nil || cursor.BlockHash != "0x"+hex.EncodeToString(blockHash) {
		return nil, nil, ErrInvalidCursor
	}
	txHash, err := decodeFixedHex(cursor.TransactionHash, 32)
	if err != nil || cursor.TransactionHash != "0x"+hex.EncodeToString(txHash) {
		return nil, nil, ErrInvalidCursor
	}
	return blockHash, txHash, nil
}

func (catalog *Postgres) scanAddressTokenTransfer(row rowScanner) (AddressTokenTransfer, error) {
	var (
		item                               AddressTokenTransfer
		blockHash, txHash, token, from, to []byte
		timestamp                          string
		tokenID, amount                    sql.NullString
		decimals                           sql.NullInt64
	)
	if err := row.Scan(
		&item.BlockNumber, &blockHash, &timestamp, &txHash, &item.TransactionIndex,
		&item.LogIndex, &item.SubIndex, &token, &item.Standard, &item.Kind,
		&from, &to, &tokenID, &amount, &item.Confidence, &decimals,
	); err != nil {
		return AddressTokenTransfer{}, err
	}
	for _, value := range []string{
		item.BlockNumber, item.TransactionIndex, item.LogIndex, item.SubIndex,
	} {
		if !canonicalUint256(value) {
			return AddressTokenTransfer{}, ErrCorruptData
		}
	}
	seconds, err := strconv.ParseUint(timestamp, 10, 64)
	if err != nil || seconds > math.MaxInt64 {
		return AddressTokenTransfer{}, ErrCorruptData
	}
	item.BlockTimestamp = time.Unix(int64(seconds), 0).UTC()
	if item.BlockHash, err = lowerHex(blockHash); err != nil {
		return AddressTokenTransfer{}, err
	}
	if item.TransactionHash, err = lowerHex(txHash); err != nil {
		return AddressTokenTransfer{}, err
	}
	if item.TokenAddress, err = checksumAddressBytes(token); err != nil {
		return AddressTokenTransfer{}, err
	}
	if item.From, err = optionalChecksumAddress(from); err != nil {
		return AddressTokenTransfer{}, err
	}
	if item.To, err = optionalChecksumAddress(to); err != nil {
		return AddressTokenTransfer{}, err
	}
	switch item.Standard {
	case "erc20", "erc721", "erc1155":
	default:
		return AddressTokenTransfer{}, ErrCorruptData
	}
	switch item.Kind {
	case "transfer", "mint", "burn":
	default:
		return AddressTokenTransfer{}, ErrCorruptData
	}
	switch item.Confidence {
	case "verified", "high", "inferred", "guess":
	default:
		return AddressTokenTransfer{}, ErrCorruptData
	}
	if tokenID.Valid {
		if !canonicalUint256(tokenID.String) {
			return AddressTokenTransfer{}, ErrCorruptData
		}
		item.TokenID = &tokenID.String
	}
	if amount.Valid {
		if !canonicalUint256(amount.String) {
			return AddressTokenTransfer{}, ErrCorruptData
		}
		item.Amount = &amount.String
	}
	if decimals.Valid {
		if item.Standard != "erc20" || decimals.Int64 < 0 || decimals.Int64 > 255 {
			return AddressTokenTransfer{}, ErrCorruptData
		}
		value := uint8(decimals.Int64)
		item.Decimals = &value
	}
	if item.Standard == "erc20" && (item.Amount == nil || item.TokenID != nil) {
		return AddressTokenTransfer{}, ErrCorruptData
	}
	if item.Standard == "erc721" && item.TokenID == nil {
		return AddressTokenTransfer{}, ErrCorruptData
	}
	if item.Standard == "erc1155" && (item.TokenID == nil || item.Amount == nil) {
		return AddressTokenTransfer{}, ErrCorruptData
	}
	return item, nil
}
