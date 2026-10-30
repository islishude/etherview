package catalog

import (
	"context"
	"errors"
	"fmt"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func completedSnapshot(chainID, number string, hash []byte, stage Stage, err error) (Snapshot, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return Snapshot{}, StageUnavailableError{Stage: stage, State: StageMissing}
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("read completed catalog snapshot: %w", err)
	}
	if !canonicalUint256(number) || len(hash) != 32 {
		return Snapshot{}, ErrCorruptData
	}
	encoded, err := lowerHex(hash)
	if err != nil {
		return Snapshot{}, ErrCorruptData
	}
	return Snapshot{ChainID: chainID, BlockNumber: number, BlockHash: encoded}, nil
}

func readCompletedTokenSnapshot(ctx context.Context, tx pgx.Tx, chainID string) (Snapshot, error) {
	var chain pgtype.Numeric
	if err := chain.Scan(chainID); err != nil {
		return Snapshot{}, err
	}
	row, err := dbgen.New(tx).CatalogCompletedSnapshot(ctx, chain, string(StageToken), int32(StageToken.Version()))
	return completedSnapshot(chainID, row.CanonicalNumber, row.BlockHash, StageToken, err)
}

func readCompletedHolderSnapshot(ctx context.Context, tx pgx.Tx, chainID string, token []byte) (Snapshot, error) {
	var chain pgtype.Numeric
	if err := chain.Scan(chainID); err != nil {
		return Snapshot{}, err
	}
	row, err := dbgen.New(tx).CatalogHolderReadSnapshot(ctx, chain, token)
	return completedSnapshot(chainID, row.Number, row.BlockHash, StageHolder, err)
}

func readCompletedStatsSnapshot(ctx context.Context, tx pgx.Tx, chainID, from, to string) (Snapshot, error) {
	var chain, start, end pgtype.Numeric
	if err := chain.Scan(chainID); err != nil {
		return Snapshot{}, err
	}
	if err := start.Scan(from); err != nil {
		return Snapshot{}, err
	}
	if err := end.Scan(to); err != nil {
		return Snapshot{}, err
	}
	row, err := dbgen.New(tx).CatalogStatsReadSnapshot(ctx, start, chain, end)
	return completedSnapshot(chainID, row.Number, row.BlockHash, StageStats, err)
}
