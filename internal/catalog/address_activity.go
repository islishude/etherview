package catalog

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	pgx "github.com/jackc/pgx/v5"
	pgtype "github.com/jackc/pgx/v5/pgtype"
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
	defer dbaccess.Rollback(ctx, tx)

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
	rows, err := func() ([]dbgen.CatalogAddressInternalTransactionsRow, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(request.ChainID); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(snapshot.BlockNumber); err != nil {
			return nil, err
		}
		var queryValue2 pgtype.Numeric
		if err := queryValue2.Scan(boundary.BlockNumber); err != nil {
			return nil, err
		}
		queryValue3, err := strconv.ParseInt(boundary.TransactionIndex, 10, 64)
		if err != nil {
			return nil, err
		}
		if limit+1 < -2147483648 || limit+1 > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		return dbgen.New(tx).CatalogAddressInternalTransactions(ctx, dbgen.CatalogAddressInternalTransactionsParams{ChainID: queryValue0, MaxBlockNumber: queryValue1, FromAddress: address, HasCursor: hasBoundary, CursorBlockNumber: queryValue2, CursorTransactionIndex: int64(queryValue3), StringToArray: boundary.TracePath, CursorBlockHash: blockHash, CursorTransactionHash: txHash, Limit: int32(limit + 1)})
	}()
	if err != nil {
		return AddressInternalTransactionPage{}, fmt.Errorf("list address internal transactions: %w", err)
	}

	items := make([]AddressInternalTransaction, 0, limit+1)
	for _, storedRow := range rows {
		item, scanErr := catalog.scanAddressInternalTransaction(dbgen.CatalogAddressInternalTransactionsRow(storedRow))
		if scanErr != nil {
			return AddressInternalTransactionPage{}, fmt.Errorf("scan address internal transaction: %w", scanErr)
		}
		items = append(items, item)
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
	if err := commitRead(ctx, tx); err != nil {
		return AddressInternalTransactionPage{}, err
	}
	return AddressInternalTransactionPage{Items: items, NextCursor: next, Snapshot: snapshot}, nil
}

func (catalog *Postgres) resolveAddressInternalCursor(
	ctx context.Context,
	tx pgx.Tx,
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

func (catalog *Postgres) scanAddressInternalTransaction(row dbgen.CatalogAddressInternalTransactionsRow) (AddressInternalTransaction, error) {
	var (
		item                       AddressInternalTransaction
		blockHash, txHash          []byte
		timestamp, path            string
		depth                      int64
		from, to, created, input   []byte
		value, gas, gasUsed, cause pgtype.Text
	)
	if err := func() error {
		item.BlockNumber = row.TraceBlockNumber
		blockHash = row.BlockHash
		timestamp = row.BlockTimestamp
		txHash = row.TransactionHash
		item.TransactionIndex = row.TraceTransactionIndex
		path = row.TracePath
		depth = int64(row.Depth)
		item.CallType = row.CallType
		from = row.FromAddress
		to = row.ToAddress
		created = row.CreatedAddress
		if numericValue, err := dbaccess.NumericText(row.TraceValue); err != nil {
			return err
		} else {
			value = numericValue
		}
		if value, err := dbaccess.NumericText(row.TraceGas); err != nil {
			return err
		} else {
			gas = value
		}
		if value, err := dbaccess.NumericText(row.TraceGasUsed); err != nil {
			return err
		} else {
			gasUsed = value
		}
		input = row.Input
		var queryValue15 pgtype.Text
		if row.Error != nil {
			queryValue15 = pgtype.Text{String: *row.Error, Valid: true}
		}
		cause = queryValue15
		item.Reverted = row.Reverted
		return nil
	}(); err != nil {
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
		source      pgtype.Text
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
	defer dbaccess.Rollback(ctx, tx)
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
	rows, err := func() ([]dbgen.CatalogAddressTokenTransfersRow, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(request.ChainID); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(snapshot.BlockNumber); err != nil {
			return nil, err
		}
		var queryValue2 pgtype.Numeric
		if err := queryValue2.Scan(boundary.BlockNumber); err != nil {
			return nil, err
		}
		queryValue3, err := strconv.ParseInt(boundary.TransactionIndex, 10, 64)
		if err != nil {
			return nil, err
		}
		queryValue4, err := strconv.ParseInt(boundary.LogIndex, 10, 64)
		if err != nil {
			return nil, err
		}
		queryValue5, err := strconv.ParseInt(boundary.SubIndex, 10, 32)
		if err != nil {
			return nil, err
		}
		if limit+1 < -2147483648 || limit+1 > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		return dbgen.New(tx).CatalogAddressTokenTransfers(ctx, dbgen.CatalogAddressTokenTransfersParams{ChainID: queryValue0, MaxBlockNumber: queryValue1, FromAddress: address, TokenFamily: kind, HasCursor: hasBoundary, CursorBlockNumber: queryValue2, CursorTransactionIndex: int64(queryValue3), CursorLogIndex: int64(queryValue4), CursorBatchIndex: int32(queryValue5), CursorBlockHash: blockHash, CursorTransactionHash: txHash, Limit: int32(limit + 1)})
	}()
	if err != nil {
		return AddressTokenTransferPage{}, fmt.Errorf("list address %s transfers: %w", kind, err)
	}

	items := make([]AddressTokenTransfer, 0, limit+1)
	for _, storedRow := range rows {
		item, scanErr := catalog.scanAddressTokenTransfer(dbgen.CatalogAddressTokenTransfersRow(storedRow))
		if scanErr != nil {
			return AddressTokenTransferPage{}, fmt.Errorf("scan address %s transfer: %w", kind, scanErr)
		}
		items = append(items, item)
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
	if err := commitRead(ctx, tx); err != nil {
		return AddressTokenTransferPage{}, err
	}
	return AddressTokenTransferPage{Items: items, NextCursor: next, Snapshot: snapshot}, nil
}

func (catalog *Postgres) resolveAddressTokenCursor(
	ctx context.Context,
	tx pgx.Tx,
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

func (catalog *Postgres) scanAddressTokenTransfer(row dbgen.CatalogAddressTokenTransfersRow) (AddressTokenTransfer, error) {
	var (
		item                               AddressTokenTransfer
		blockHash, txHash, token, from, to []byte
		timestamp                          string
		tokenID, amount                    pgtype.Text
		decimals                           pgtype.Int8
	)
	if err := func() error {
		item.BlockNumber = row.EventBlockNumber
		blockHash = row.BlockHash
		timestamp = row.BlockTimestamp
		txHash = row.TransactionHash
		item.TransactionIndex = row.InclusionTxIndex
		item.LogIndex = row.EventLogIndex
		item.SubIndex = row.EventSubIndex
		token = row.TokenAddress
		item.Standard = row.Standard
		item.Kind = row.EventKind
		from = row.FromAddress
		to = row.ToAddress
		queryValue12, err := dbaccess.NumericText(row.EventTokenID)
		if err != nil {
			return err
		}
		tokenID = queryValue12
		queryValue14, err := dbaccess.NumericText(row.EventAmount)
		if err != nil {
			return err
		}
		amount = queryValue14
		item.Confidence = row.Confidence
		queryValue17, err := row.Decimals.Int64Value()
		if err != nil {
			return err
		}
		decimals = queryValue17
		return nil
	}(); err != nil {
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
