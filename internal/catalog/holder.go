package catalog

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"

	"github.com/jackc/pgx/v5/pgtype"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgx "github.com/jackc/pgx/v5"

	dbgen "github.com/islishude/etherview/internal/db/gen"
)

func (catalog *Postgres) TokenHolders(
	ctx context.Context,
	request TokenHolderRequest,
) (TokenHolderPage, error) {
	if err := validateChainID(request.ChainID); err != nil {
		return TokenHolderPage{}, err
	}
	tokenAddress, checksummedToken, err := checksumInputAddress(request.TokenAddress)
	if err != nil {
		return TokenHolderPage{}, err
	}
	limit, err := catalog.pageLimit(request.Limit)
	if err != nil {
		return TokenHolderPage{}, err
	}
	tx, err := catalog.beginRead(ctx)
	if err != nil {
		return TokenHolderPage{}, err
	}
	defer dbaccess.Rollback(ctx, tx)

	snapshot, afterAddress, hasAfter, cursorEpoch, err := holderRequestSnapshot(ctx, tx, request, tokenAddress)
	if err != nil {
		return TokenHolderPage{}, err
	}
	epoch, err := requireHolderCoverage(ctx, tx, snapshot)
	if err != nil {
		return TokenHolderPage{}, err
	}
	if hasAfter && epoch != cursorEpoch {
		return TokenHolderPage{}, ErrInvalidCursor
	}
	contract, err := catalog.tokenContractAtSnapshot(ctx, tx, snapshot, tokenAddress)
	if err != nil {
		return TokenHolderPage{}, err
	}
	if contract.Standard != "erc20" || contract.Confidence != "high" && contract.Confidence != "verified" {
		return TokenHolderPage{}, ErrNotApplicable
	}
	summary, err := readHolderSummary(
		ctx, tx, snapshot, tokenAddress, checksummedToken, epoch,
	)
	if err != nil {
		return TokenHolderPage{}, err
	}
	rows, err := func() ([]dbgen.CatalogHolderPageRow, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(request.ChainID); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(summary.ObservedBlockNumber); err != nil {
			return nil, err
		}
		return dbgen.New(tx).CatalogHolderPage(ctx, dbgen.CatalogHolderPageParams{RowLimit: int64(limit + 1), ChainID: queryValue0, TokenAddress: tokenAddress, BlockNumber: queryValue1, HasAfter: hasAfter, AfterAddress: afterAddress})
	}()
	if err != nil {
		return TokenHolderPage{}, fmt.Errorf("query token holder page: %w", err)
	}

	items := make([]TokenHolder, 0, limit+1)
	for _, storedRow := range rows {
		var holder, blockHash []byte
		var balance, blockNumber, confidence string
		{
			holder = storedRow.HolderAddress
			balance = storedRow.LatestBalance
			blockNumber = storedRow.LatestBlockNumber
			blockHash = storedRow.BlockHash
			confidence = storedRow.Confidence
		}
		checksummedHolder, checksumErr := checksumAddressBytes(holder)
		observedHash, hashErr := lowerHex(blockHash)
		if checksumErr != nil || hashErr != nil || !canonicalUint256(balance) ||
			!canonicalUint256(blockNumber) || confidence != NFTStateConfidenceRPCExact {
			return TokenHolderPage{}, ErrCorruptData
		}
		items = append(items, TokenHolder{
			ChainID: request.ChainID, TokenAddress: checksummedToken,
			HolderAddress: checksummedHolder, Balance: balance, Confidence: confidence,
			ObservedBlockNumber: blockNumber, ObservedBlockHash: observedHash,
		})
	}

	next := ""
	if len(items) > limit {
		items = items[:limit]
		boundary, _, decodeErr := checksumInputAddress(items[len(items)-1].HolderAddress)
		if decodeErr != nil {
			return TokenHolderPage{}, ErrCorruptData
		}
		next, err = encodeCursor(tokenHolderCursor{
			Version: cursorVersion, ChainID: request.ChainID,
			TokenAddress:   "0x" + hex.EncodeToString(tokenAddress),
			SnapshotNumber: snapshot.BlockNumber, SnapshotHash: snapshot.BlockHash,
			PublicationEpoch: epoch, AfterAddress: "0x" + hex.EncodeToString(boundary),
		})
		if err != nil {
			return TokenHolderPage{}, err
		}
	}
	if err := commitRead(ctx, tx); err != nil {
		return TokenHolderPage{}, err
	}
	return TokenHolderPage{Items: items, NextCursor: next, Summary: summary}, nil
}

func (catalog *Postgres) TokenHolderCount(
	ctx context.Context,
	chainID string,
	tokenAddressText string,
) (TokenHolderSummary, error) {
	if err := validateChainID(chainID); err != nil {
		return TokenHolderSummary{}, err
	}
	tokenAddress, checksummedToken, err := checksumInputAddress(tokenAddressText)
	if err != nil {
		return TokenHolderSummary{}, err
	}
	tx, err := catalog.beginRead(ctx)
	if err != nil {
		return TokenHolderSummary{}, err
	}
	defer dbaccess.Rollback(ctx, tx)
	snapshot, err := readCanonicalSnapshot(ctx, tx, chainID)
	if err != nil {
		return TokenHolderSummary{}, err
	}
	epoch, err := requireHolderCoverage(ctx, tx, snapshot)
	if err != nil {
		return TokenHolderSummary{}, err
	}
	contract, err := catalog.tokenContractAtSnapshot(ctx, tx, snapshot, tokenAddress)
	if err != nil {
		return TokenHolderSummary{}, err
	}
	if contract.Standard != "erc20" || contract.Confidence != "high" && contract.Confidence != "verified" {
		return TokenHolderSummary{}, ErrNotApplicable
	}
	summary, err := readHolderSummary(ctx, tx, snapshot, tokenAddress, checksummedToken, epoch)
	if err != nil {
		return TokenHolderSummary{}, err
	}
	if err := commitRead(ctx, tx); err != nil {
		return TokenHolderSummary{}, err
	}
	return summary, nil
}

func holderRequestSnapshot(
	ctx context.Context,
	tx pgx.Tx,
	request TokenHolderRequest,
	tokenAddress []byte,
) (Snapshot, []byte, bool, string, error) {
	if request.Cursor == "" {
		snapshot, err := readCanonicalSnapshot(ctx, tx, request.ChainID)
		return snapshot, make([]byte, 20), false, "", err
	}
	var cursor tokenHolderCursor
	if err := decodeCursor(request.Cursor, &cursor); err != nil ||
		cursor.Version != cursorVersion || cursor.ChainID != request.ChainID ||
		cursor.TokenAddress != "0x"+hex.EncodeToString(tokenAddress) ||
		!canonicalUint256(cursor.PublicationEpoch) {
		return Snapshot{}, nil, false, "", ErrInvalidCursor
	}
	after, err := decodeFixedHex(cursor.AfterAddress, 20)
	if err != nil || cursor.AfterAddress != "0x"+hex.EncodeToString(after) {
		return Snapshot{}, nil, false, "", ErrInvalidCursor
	}
	snapshot := Snapshot{
		ChainID: cursor.ChainID, BlockNumber: cursor.SnapshotNumber, BlockHash: cursor.SnapshotHash,
	}
	if err := validateCanonicalSnapshot(ctx, tx, snapshot); err != nil {
		return Snapshot{}, nil, false, "", err
	}
	return snapshot, after, true, cursor.PublicationEpoch, nil
}

func requireHolderCoverage(ctx context.Context, tx pgx.Tx, snapshot Snapshot) (string, error) {
	var configuredStart, covered, tokenBlocks, proxyBlocks, epoch string
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(snapshot.BlockNumber); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(snapshot.ChainID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).CatalogHolderCoverage(ctx, queryValue0, queryValue1)
		if err != nil {
			return err
		}
		configuredStart = queryRow.ConfigurationConfiguredStart
		covered = queryRow.CoveredBlocks
		tokenBlocks = queryRow.TokenBlocks
		proxyBlocks = queryRow.ProxyBlocks
		epoch = queryRow.PublicationEpoch
		return nil
	}(); err != nil {
		return "", fmt.Errorf("read holder coverage: %w", err)
	}
	want, ok := new(big.Int).SetString(snapshot.BlockNumber, 10)
	if !ok {
		return "", ErrCorruptData
	}
	want.Add(want, big.NewInt(1))
	if configuredStart != "0" || covered != want.String() || tokenBlocks != want.String() ||
		proxyBlocks != want.String() || !canonicalUint256(epoch) {
		return "", StageUnavailableError{
			Stage: StageHolder, State: StageMissing,
			BlockNumber: snapshot.BlockNumber, BlockHash: snapshot.BlockHash,
		}
	}
	return epoch, nil
}

func readHolderSummary(
	ctx context.Context,
	tx pgx.Tx,
	snapshot Snapshot,
	tokenAddress []byte,
	checksummedToken string,
	epoch string,
) (TokenHolderSummary, error) {
	var blockNumber, state, holderCount, totalSupply, balanceSum string
	var blockHash []byte
	var coherent bool
	err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(snapshot.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(snapshot.BlockNumber); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).CatalogHolderTokenSnapshot(ctx, queryValue0, tokenAddress, queryValue1)
		if err != nil {
			return err
		}
		blockNumber = queryRow.SnapshotBlockNumber
		blockHash = queryRow.BlockHash
		state = queryRow.State
		holderCount = queryRow.SnapshotHolderCount
		totalSupply = queryRow.SnapshotTotalSupply
		balanceSum = queryRow.SnapshotReconciledBalanceSum
		coherent = queryRow.Coherent
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
		return TokenHolderSummary{}, StageUnavailableError{
			Stage: StageHolder, State: StageMissing,
			BlockNumber: snapshot.BlockNumber, BlockHash: snapshot.BlockHash,
		}
	}
	if err != nil {
		return TokenHolderSummary{}, fmt.Errorf("read holder token snapshot: %w", err)
	}
	if state != "complete" || !coherent {
		return TokenHolderSummary{}, StageUnavailableError{
			Stage: StageHolder, State: StageUnavailable,
			BlockNumber: snapshot.BlockNumber, BlockHash: snapshot.BlockHash,
		}
	}
	observedHash, err := lowerHex(blockHash)
	if err != nil || !canonicalUint256(blockNumber) || !canonicalUint256(holderCount) ||
		!canonicalUint256(totalSupply) || !canonicalUint256(balanceSum) || totalSupply != balanceSum {
		return TokenHolderSummary{}, ErrCorruptData
	}
	return TokenHolderSummary{
		ChainID: snapshot.ChainID, TokenAddress: checksummedToken,
		HolderCount: holderCount, TotalSupply: totalSupply, ReconciledBalanceSum: balanceSum,
		ObservedBlockNumber: blockNumber, ObservedBlockHash: observedHash,
		PublicationEpoch: epoch, Snapshot: snapshot,
	}, nil
}
