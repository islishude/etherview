package catalog

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	pgx "github.com/jackc/pgx/v5"
	pgtype "github.com/jackc/pgx/v5/pgtype"
)

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
	defer dbaccess.Rollback(ctx, tx)
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
	if err := commitRead(ctx, tx); err != nil {
		return TokenContract{}, err
	}
	return contract, nil
}

func (catalog *Postgres) tokenContractAtSnapshot(ctx context.Context, tx pgx.Tx, snapshot Snapshot, address []byte) (TokenContract, error) {
	var chain, number pgtype.Numeric
	if err := chain.Scan(snapshot.ChainID); err != nil {
		return TokenContract{}, err
	}
	if err := number.Scan(snapshot.BlockNumber); err != nil {
		return TokenContract{}, err
	}
	row, err := dbgen.New(catalog.db).WithTx(tx).CatalogTokenContract(ctx, chain, address, number)
	if errors.Is(err, pgx.ErrNoRows) {
		return TokenContract{}, ErrNotFound
	}
	if err != nil {
		return TokenContract{}, fmt.Errorf("query token contract: %w", err)
	}
	return catalog.scanTokenContract(dbgen.CatalogTokenContractsRow(row))
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
	defer dbaccess.Rollback(ctx, tx)

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
	rows, err := func() ([]dbgen.CatalogTokenContractsRow, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(request.ChainID); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(snapshot.BlockNumber); err != nil {
			return nil, err
		}
		if limit+1 < -2147483648 || limit+1 > 2147483647 {
			return nil, errors.New("invalid stored query value")
		}
		return dbgen.New(tx).CatalogTokenContracts(ctx, dbgen.CatalogTokenContractsParams{ChainID: queryValue0, MaxObservedBlockNumber: queryValue1, HasCursor: hasAfter, Address: afterAddress, Limit: int32(limit + 1)})
	}()
	if err != nil {
		return TokenPage{}, fmt.Errorf("list token contracts: %w", err)
	}

	items := make([]TokenContract, 0, limit+1)
	for _, storedRow := range rows {
		contract, scanErr := catalog.scanTokenContract(dbgen.CatalogTokenContractsRow(storedRow))
		if scanErr != nil {
			return TokenPage{}, fmt.Errorf("scan token contract: %w", scanErr)
		}
		items = append(items, contract)
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
	if err := commitRead(ctx, tx); err != nil {
		return TokenPage{}, err
	}
	return TokenPage{Items: items, NextCursor: next, Snapshot: snapshot}, nil
}

func (catalog *Postgres) scanTokenContract(row dbgen.CatalogTokenContractsRow) (TokenContract, error) {
	var (
		contract                     TokenContract
		address, codeHash, blockHash []byte
		name, symbol                 pgtype.Text
		decimals                     pgtype.Int8
		totalSupply                  pgtype.Text
	)
	if err := func() error {
		contract.ChainID = row.ChainID
		address = row.Address
		codeHash = row.CodeHash
		contract.Standard = row.Standard
		contract.Confidence = row.Confidence
		var queryValue5 pgtype.Text
		if row.Name != nil {
			queryValue5 = pgtype.Text{String: *row.Name, Valid: true}
		}
		name = queryValue5
		var queryValue7 pgtype.Text
		if row.Symbol != nil {
			queryValue7 = pgtype.Text{String: *row.Symbol, Valid: true}
		}
		symbol = queryValue7
		var queryValue9 pgtype.Int8
		if row.Decimals != nil {
			queryValue9 = pgtype.Int8{Int64: int64(*row.Decimals), Valid: true}
		}
		decimals = queryValue9
		var err error
		totalSupply, err = dbaccess.NumericText(row.TotalSupply)
		if err != nil {
			return err
		}
		contract.MetadataState = row.MetadataState
		contract.ObservedBlockNumber = row.ObservedBlockNumber
		blockHash = row.ObservedBlockHash
		if !row.UpdatedAt.Valid {
			return errors.New("invalid stored query value")
		}
		if row.UpdatedAt.InfinityModifier != pgtype.Finite {
			return errors.New("invalid stored query value")
		}
		contract.UpdatedAt = row.UpdatedAt.Time
		return nil
	}(); err != nil {
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
	contract.CodeHash, err = lowerHex(codeHash)
	if err != nil {
		return TokenContract{}, err
	}
	contract.ObservedBlockHash, err = lowerHex(blockHash)
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
	defer dbaccess.Rollback(ctx, tx)

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
	rows, err := dbgen.New(catalog.db).WithTx(tx).CatalogTokenEvents(ctx, dbgen.CatalogTokenEventsParams{
		ChainID: request.ChainID, SnapshotNumber: snapshot.BlockNumber, TokenAddress: tokenAddress, HasCursor: hasBoundary,
		BeforeNumber: boundaryNumber, BeforeLogIndex: boundaryLog, BeforeSubIndex: boundarySub, BeforeBlockHash: boundaryHash, PageLimit: int32(limit + 1),
	})
	if err != nil {
		return TokenEventPage{}, fmt.Errorf("list canonical token events: %w", err)
	}
	items := make([]TokenEvent, 0, len(rows))
	for _, row := range rows {
		event, err := scanTokenEvent(dbgen.CatalogTransactionTokenEventsRow(row))
		if err != nil {
			return TokenEventPage{}, fmt.Errorf("decode token event: %w", err)
		}
		items = append(items, event)
	}

	next := ""
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		next, err = encodeCursor(tokenEventCursor{Version: cursorVersion, ChainID: request.ChainID, TokenAddress: normalizedToken, SnapshotNumber: snapshot.BlockNumber, SnapshotHash: snapshot.BlockHash, BlockNumber: last.BlockNumber, BlockHash: last.BlockHash, LogIndex: last.LogIndex, SubIndex: last.SubIndex})
		if err != nil {
			return TokenEventPage{}, err
		}
	}

	if err := commitRead(ctx, tx); err != nil {
		return TokenEventPage{}, err
	}
	return TokenEventPage{Items: items, NextCursor: next, Snapshot: snapshot}, nil
}

func scanTokenEvent(row dbgen.CatalogTransactionTokenEventsRow) (TokenEvent, error) {
	event := TokenEvent{ChainID: row.ChainID, BlockNumber: row.BlockNumber, LogIndex: row.LogIndex, SubIndex: row.SubIndex, Standard: row.Standard, Kind: row.EventKind, Confidence: row.Confidence}
	blockHash, txHash, tokenAddress := row.BlockHash, row.TransactionHash, row.TokenAddress
	operator, from, to := row.Operator, row.FromAddress, row.ToAddress
	tokenID, err := dbaccess.NumericText(row.TokenID)
	if err != nil {
		return TokenEvent{}, err
	}
	amount, err := dbaccess.NumericText(row.Amount)
	if err != nil {
		return TokenEvent{}, err
	}
	decimals, err := row.Decimals.Int64Value()
	if err != nil {
		return TokenEvent{}, err
	}
	if err := validateChainID(event.ChainID); err != nil || !canonicalUint256(event.BlockNumber) ||
		!canonicalInt64(event.LogIndex) || !canonicalInt32(event.SubIndex) {
		return TokenEvent{}, ErrCorruptData
	}
	if event.BlockHash, err = lowerHex(blockHash); err != nil {
		return TokenEvent{}, err
	}
	if event.TransactionHash, err = lowerHex(txHash); err != nil {
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
	if decimals.Valid {
		if event.Standard != "erc20" || decimals.Int64 < 0 || decimals.Int64 > 255 {
			return TokenEvent{}, ErrCorruptData
		}
		value := uint8(decimals.Int64)
		event.Decimals = &value
	}
	return event, nil
}
