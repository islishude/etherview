package catalog

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgx "github.com/jackc/pgx/v5"
	pgtype "github.com/jackc/pgx/v5/pgtype"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/chainbundle"
	dbgen "github.com/islishude/etherview/internal/db/gen"
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
	defer dbaccess.Rollback(ctx, tx)
	page := TransactionTokenEventPage{Identity: resolution.identity, Items: []TokenEvent{}}
	if resolution.identity.State == StageComplete {
		rows, queryErr := func() ([]dbgen.CatalogTransactionTokenEventsRow, error) {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(request.ChainID); err != nil {
				return nil, err
			}
			if resolution.limit+1 < -2147483648 || resolution.limit+1 > 2147483647 {
				return nil, errors.New("invalid stored query value")
			}
			if resolution.offset < -2147483648 || resolution.offset > 2147483647 {
				return nil, errors.New("invalid stored query value")
			}
			return dbgen.New(tx).CatalogTransactionTokenEvents(ctx, dbgen.CatalogTransactionTokenEventsParams{ChainID: queryValue0, BlockHash: resolution.blockHash, TransactionHash: resolution.txHash, Limit: int32(resolution.limit + 1), Offset: int32(resolution.offset)})
		}()
		if queryErr != nil {
			return TransactionTokenEventPage{}, fmt.Errorf("list transaction token events: %w", queryErr)
		}

		for _, storedRow := range rows {
			event, scanErr := scanTokenEvent(dbgen.CatalogTransactionTokenEventsRow(storedRow))
			if scanErr != nil {
				return TransactionTokenEventPage{}, fmt.Errorf("scan transaction token event: %w", scanErr)
			}
			page.Items = append(page.Items, event)
		}

		if len(page.Items) > resolution.limit {
			page.Items = page.Items[:resolution.limit]
			page.NextCursor, err = resolution.nextCursor("token_transfers", resolution.offset+resolution.limit)
			if err != nil {
				return TransactionTokenEventPage{}, err
			}
		}
	}
	if err := commitRead(ctx, tx); err != nil {
		return TransactionTokenEventPage{}, err
	}
	return page, nil
}

func (catalog *Postgres) TransactionInternalTransactions(
	ctx context.Context,
	request TransactionResourceRequest,
) (TransactionInternalTransactionPage, error) {
	tx, resolution, err := catalog.beginTransactionResource(ctx, request, "internal_transactions", StageTrace)
	if err != nil {
		return TransactionInternalTransactionPage{}, err
	}
	defer dbaccess.Rollback(ctx, tx)
	page := TransactionInternalTransactionPage{
		Identity: resolution.identity,
		Items:    []TransactionInternalTransaction{},
	}
	if resolution.identity.State == StageComplete {
		rows, queryErr := func() ([]dbgen.CatalogTransactionInternalTransactionsRow, error) {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(request.ChainID); err != nil {
				return nil, err
			}
			if resolution.limit+1 < -2147483648 || resolution.limit+1 > 2147483647 {
				return nil, errors.New("invalid stored query value")
			}
			if resolution.offset < -2147483648 || resolution.offset > 2147483647 {
				return nil, errors.New("invalid stored query value")
			}
			return dbgen.New(tx).CatalogTransactionInternalTransactions(ctx, dbgen.CatalogTransactionInternalTransactionsParams{ChainID: queryValue0, BlockHash: resolution.blockHash, TransactionHash: resolution.txHash, Limit: int32(resolution.limit + 1), Offset: int32(resolution.offset)})
		}()
		if queryErr != nil {
			return TransactionInternalTransactionPage{}, fmt.Errorf("list transaction internal transactions: %w", queryErr)
		}

		for _, storedRow := range rows {
			item, scanErr := catalog.scanTransactionInternalTransaction(dbgen.CatalogTransactionInternalTransactionsRow(storedRow))
			if scanErr != nil {
				return TransactionInternalTransactionPage{}, fmt.Errorf("scan transaction internal transaction: %w", scanErr)
			}
			page.Items = append(page.Items, item)
		}

		if len(page.Items) > resolution.limit {
			page.Items = page.Items[:resolution.limit]
			page.NextCursor, err = resolution.nextCursor("internal_transactions", resolution.offset+resolution.limit)
			if err != nil {
				return TransactionInternalTransactionPage{}, err
			}
		}
	}
	if err := commitRead(ctx, tx); err != nil {
		return TransactionInternalTransactionPage{}, err
	}
	return page, nil
}

func (catalog *Postgres) scanTransactionInternalTransaction(
	row dbgen.CatalogTransactionInternalTransactionsRow,
) (TransactionInternalTransaction, error) {
	var (
		item              TransactionInternalTransaction
		path              string
		depth             int64
		from, to, created []byte
	)
	{
		path = row.TracePath
		depth = int64(row.Depth)
		item.CallType = row.CallType
		from = row.FromAddress
		to = row.ToAddress
		created = row.CreatedAddress
		item.Value = row.TraceValue
	}
	if depth <= 0 || depth > 128 || item.CallType == "" || len(item.CallType) > 128 ||
		!canonicalUint256(item.Value) || item.Value == "0" {
		return TransactionInternalTransaction{}, ErrCorruptData
	}
	var err error
	item.Path, err = parseTracePath(path)
	if err != nil || len(item.Path) != int(depth) {
		return TransactionInternalTransaction{}, ErrCorruptData
	}
	item.Depth = uint32(depth)
	fromAddress, err := checksumAddressBytes(from)
	if err != nil {
		return TransactionInternalTransaction{}, err
	}
	item.From = fromAddress
	if item.To, err = optionalChecksumAddress(to); err != nil {
		return TransactionInternalTransaction{}, err
	}
	if item.CreatedAddress, err = optionalChecksumAddress(created); err != nil {
		return TransactionInternalTransaction{}, err
	}
	if (item.CallType == "CREATE" || item.CallType == "CREATE2") != (item.CreatedAddress != nil) {
		return TransactionInternalTransaction{}, ErrCorruptData
	}
	if item.CreatedAddress == nil && item.To == nil {
		return TransactionInternalTransaction{}, ErrCorruptData
	}
	return item, nil
}

func (catalog *Postgres) TransactionLogs(
	ctx context.Context,
	request TransactionResourceRequest,
) (TransactionLogPage, error) {
	tx, resolution, err := catalog.beginTransactionResource(ctx, request, "logs", StageCore)
	if err != nil {
		return TransactionLogPage{}, err
	}
	defer dbaccess.Rollback(ctx, tx)
	page := TransactionLogPage{Identity: resolution.identity, Items: []TransactionLog{}}
	rows, err := func() ([]dbgen.CatalogTransactionLogsRow, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(request.ChainID); err != nil {
			return nil, err
		}
		if resolution.limit+1 < -2147483648 || resolution.limit+1 > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		if resolution.offset < -2147483648 || resolution.offset > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		return dbgen.New(tx).CatalogTransactionLogs(ctx, dbgen.CatalogTransactionLogsParams{ChainID: queryValue0, BlockHash: resolution.blockHash, TxHash: resolution.txHash, Limit: int32(resolution.limit + 1), Offset: int32(resolution.offset)})
	}()
	if err != nil {
		return TransactionLogPage{}, fmt.Errorf("list transaction logs: %w", err)
	}

	type rawLog struct {
		index            int64
		raw              []byte
		persisted        persistedLogDecoding
		tracePath        pgtype.Text
		executionAddress []byte
	}
	rawLogs := make([]rawLog, 0, resolution.limit+1)
	for _, storedRow := range rows {
		var raw []byte
		var logIndex int64
		var persisted persistedLogDecoding
		var storedTracePath pgtype.Text
		var executionAddress []byte
		{
			logIndex = storedRow.LogIndex
			raw = storedRow.Raw
			var queryValue2 pgtype.Text
			if storedRow.Status != nil {
				queryValue2 = pgtype.Text{String: *storedRow.Status, Valid: true}
			}
			persisted.status = queryValue2
			var queryValue4 pgtype.Text
			if storedRow.Signature != nil {
				queryValue4 = pgtype.Text{String: *storedRow.Signature, Valid: true}
			}
			persisted.signature = queryValue4
			var queryValue6 pgtype.Text
			if storedRow.Source != nil {
				queryValue6 = pgtype.Text{String: *storedRow.Source, Valid: true}
			}
			persisted.source = queryValue6
			var queryValue8 pgtype.Text
			if storedRow.Confidence != nil {
				queryValue8 = pgtype.Text{String: *storedRow.Confidence, Valid: true}
			}
			persisted.confidence = queryValue8
			persisted.arguments = storedRow.Arguments
			persisted.candidates = storedRow.Candidates
			var queryValue12 pgtype.Text
			if storedRow.Warning != nil {
				queryValue12 = pgtype.Text{String: *storedRow.Warning, Valid: true}
			}
			persisted.warning = queryValue12
			persisted.targetAddress = storedRow.TargetAddress
			persisted.targetCodeHash = storedRow.TargetCodeHash
			persisted.sourceAddress = storedRow.SourceAddress
			persisted.sourceCodeHash = storedRow.SourceCodeHash
			var queryValue18 pgtype.Text
			if storedRow.TracePath != nil {
				queryValue18 = pgtype.Text{String: *storedRow.TracePath, Valid: true}
			}
			storedTracePath = queryValue18
			executionAddress = storedRow.ExecutionAddress
		}
		if logIndex < 0 {
			return TransactionLogPage{}, ErrCorruptData
		}
		rawLogs = append(rawLogs, rawLog{
			index: logIndex, raw: raw, persisted: persisted,
			tracePath: storedTracePath, executionAddress: executionAddress,
		})
	}

	for _, stored := range rawLogs {
		blockNumber, parseErr := strconv.ParseUint(resolution.identity.BlockNumber, 10, 64)
		if parseErr != nil {
			return TransactionLogPage{}, ErrCorruptData
		}
		decoded, decodeErr := chainbundle.DecodeLog(
			stored.raw, common.BytesToHash(resolution.txHash), common.BytesToHash(resolution.blockHash),
			blockNumber, uint64(resolution.txIndex), uint64(stored.index),
		)
		if decodeErr != nil {
			return TransactionLogPage{}, ErrCorruptData
		}
		topics := make([]string, len(decoded.Topics))
		for index := range decoded.Topics {
			topics[index] = decoded.Topics[index].Hex()
		}
		decodeAddress := decoded.Address
		attribution := TransactionLogAttribution{Mode: "address_fallback", TracePath: []uint32{}}
		if stored.tracePath.Valid || len(stored.executionAddress) != 0 {
			if !stored.tracePath.Valid || len(stored.executionAddress) != common.AddressLength {
				return TransactionLogPage{}, ErrCorruptData
			}
			path, pathErr := parseTracePath(stored.tracePath.String)
			if pathErr != nil {
				return TransactionLogPage{}, ErrCorruptData
			}
			decodeAddress = common.BytesToAddress(stored.executionAddress)
			attribution = TransactionLogAttribution{
				Mode: "exact_trace", TracePath: path, ExecutionAddress: decodeAddress.Hex(),
			}
		}
		decoding, decodeErr := resolveTransactionLogDecoding(
			ctx, tx, request.ChainID, blockNumber, resolution.blockHash,
			decodeAddress, decoded.Topics, decoded.Data, stored.persisted,
		)
		if decodeErr != nil {
			return TransactionLogPage{}, fmt.Errorf("decode transaction log ABI: %w", decodeErr)
		}
		page.Items = append(page.Items, TransactionLog{
			Address: decoded.Address.Hex(), LogIndex: strconv.FormatInt(stored.index, 10),
			Topics: topics, Data: "0x" + hex.EncodeToString(decoded.Data), Decoding: decoding,
		})
		page.Items[len(page.Items)-1].Decoding.Attribution = attribution
	}
	if len(page.Items) > resolution.limit {
		page.Items = page.Items[:resolution.limit]
		page.NextCursor, err = resolution.nextCursor("logs", resolution.offset+resolution.limit)
		if err != nil {
			return TransactionLogPage{}, err
		}
	}
	if err := commitRead(ctx, tx); err != nil {
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
	defer dbaccess.Rollback(ctx, tx)
	page := TransactionStateChangePage{Identity: resolution.identity, Items: []TransactionStateChange{}}
	if resolution.identity.State == StageComplete {
		rows, queryErr := func() ([]dbgen.CatalogTransactionStateChangesRow, error) {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(request.ChainID); err != nil {
				return nil, err
			}
			if resolution.limit+1 < -2147483648 || resolution.limit+1 > 2147483647 {
				return nil, errors.New("invalid stored query value")
			}
			if resolution.offset < -2147483648 || resolution.offset > 2147483647 {
				return nil, errors.New("invalid stored query value")
			}
			return dbgen.New(tx).CatalogTransactionStateChanges(ctx, dbgen.CatalogTransactionStateChangesParams{ChainID: queryValue0, BlockHash: resolution.blockHash, TransactionHash: resolution.txHash, Limit: int32(resolution.limit + 1), Offset: int32(resolution.offset)})
		}()
		if queryErr != nil {
			return TransactionStateChangePage{}, fmt.Errorf("list transaction state changes: %w", queryErr)
		}

		for _, storedRow := range rows {
			var address, storageKey []byte
			var change TransactionStateChange
			var before, after pgtype.Text
			{
				address = storedRow.Address
				change.Kind = storedRow.FieldKind
				storageKey = storedRow.StorageKey
				var queryValue3 pgtype.Text
				if storedRow.BeforeValue != nil {
					queryValue3 = pgtype.Text{String: *storedRow.BeforeValue, Valid: true}
				}
				before = queryValue3
				var queryValue5 pgtype.Text
				if storedRow.AfterValue != nil {
					queryValue5 = pgtype.Text{String: *storedRow.AfterValue, Valid: true}
				}
				after = queryValue5
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

		if len(page.Items) > resolution.limit {
			page.Items = page.Items[:resolution.limit]
			page.NextCursor, err = resolution.nextCursor("state_changes", resolution.offset+resolution.limit)
			if err != nil {
				return TransactionStateChangePage{}, err
			}
		}
	}
	if err := commitRead(ctx, tx); err != nil {
		return TransactionStateChangePage{}, err
	}
	return page, nil
}

func (catalog *Postgres) beginTransactionResource(
	ctx context.Context,
	request TransactionResourceRequest,
	kind string,
	stage Stage,
) (pgx.Tx, transactionResourceResolution, error) {
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
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(request.ChainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).CatalogTransactionResourceIdentity(ctx, queryValue0, txHash)
		if err != nil {
			return err
		}
		blockNumber = queryRow.InclusionBlockNumber
		blockHash = queryRow.BlockHash
		resolution.txIndex = queryRow.TxIndex
		resolution.canonical = queryRow.Canonical
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
		dbaccess.Rollback(ctx, tx) //nolint:errcheck
		return nil, transactionResourceResolution{}, ErrNotFound
	}
	if err != nil {
		dbaccess.Rollback(ctx, tx) //nolint:errcheck
		return nil, transactionResourceResolution{}, fmt.Errorf("resolve transaction resource identity: %w", err)
	}
	blockHashText, err := lowerHex(blockHash)
	if err != nil || !canonicalUint256(blockNumber) || resolution.txIndex < 0 {
		dbaccess.Rollback(ctx, tx) //nolint:errcheck
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
			dbaccess.Rollback(ctx, tx) //nolint:errcheck
			return nil, transactionResourceResolution{}, err
		}
	}
	if request.Cursor != "" {
		var cursor transactionResourceCursor
		if decodeCursor(request.Cursor, &cursor) != nil || cursor.Version != cursorVersion ||
			cursor.Kind != kind || cursor.ChainID != request.ChainID ||
			cursor.TransactionHash != normalizedHash || cursor.BlockHash != blockHashText ||
			cursor.Generation != resolution.generation || cursor.Offset <= 0 {
			dbaccess.Rollback(ctx, tx) //nolint:errcheck
			return nil, transactionResourceResolution{}, ErrInvalidCursor
		}
		resolution.offset = cursor.Offset
	}
	return tx, resolution, nil
}

func transactionStageState(
	ctx context.Context,
	tx pgx.Tx,
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
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(blockNumber); err != nil {
			return err
		}
		if stage.Version() < -2147483648 || stage.Version() > 2147483647 {
			return errors.New("invalid stored query value")
		}
		queryRow, err := dbgen.New(tx).CatalogTransactionStageState(ctx, dbgen.CatalogTransactionStageStateParams{ChainID: queryValue0, BlockNumber: queryValue1, BlockHash: blockHash, Stage: string(stage), StageVersion: int32(stage.Version())})
		if err != nil {
			return err
		}
		if !queryRow.State.Valid {
			return errors.New("invalid stored query value")
		}
		state = queryRow.State.String
		if queryRow.JobGeneration == nil {
			return errors.New("invalid stored query value")
		}
		generation = *queryRow.JobGeneration
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
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
