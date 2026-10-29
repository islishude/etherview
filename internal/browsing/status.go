// Package browsing owns publication-aware native explorer read metadata.
package browsing

import (
	"context"
	"fmt"

	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/stagecontract"
	"github.com/jackc/pgx/v5/pgtype"
)

type Status struct {
	State, ChainID, BlockNumber, BlockHash, FromBlock, ToBlock, Revision string
}

// Read shares the caller's repeatable-read transaction with data and cursor checks.
func Read(ctx context.Context, db dbgen.DBTX, chain, number, hash string, stage stagecontract.ID, from, digest string) (*Status, error) {
	var chainID, end, start pgtype.Numeric
	if err := chainID.Scan(chain); err != nil {
		return nil, err
	}
	if err := end.Scan(number); err != nil {
		return nil, err
	}
	if from != "" {
		if err := start.Scan(from); err != nil {
			return nil, err
		}
	}
	row, err := dbgen.New(db).BrowsingStatus(ctx, dbgen.BrowsingStatusParams{
		ChainID: chainID, ToBlock: end, FromBlock: start, Stage: stage.Name,
		StageVersion: int32(stage.Version), ConfigurationDigest: digest,
	})
	if err != nil {
		return nil, fmt.Errorf("read browsing publication: %w", err)
	}
	return &Status{State: row.State, ChainID: chain, BlockNumber: number, BlockHash: hash, FromBlock: row.FromBlock, ToBlock: number, Revision: row.Revision}, nil
}
