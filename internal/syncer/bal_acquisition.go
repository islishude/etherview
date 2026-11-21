package syncer

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/chainbundle"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type BALCollector struct {
	DB           dbaccess.Database
	ChainID      string
	Fetcher      *ethrpc.BALFetcher
	Timeout      time.Duration
	PollInterval time.Duration
	Logger       *slog.Logger
}

func (*BALCollector) Name() string { return "bal-acquisition" }
func (c *BALCollector) Run(ctx context.Context) error {
	ticker := time.NewTicker(c.PollInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		found, err := c.CollectOne(ctx)
		if err == nil && found {
			continue
		}
		if err != nil && c.Logger != nil && ctx.Err() == nil {
			c.Logger.WarnContext(ctx, "BAL acquisition deferred", "error_code", "bal_acquisition_unavailable")
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
	return nil
}
func (c *BALCollector) CollectOne(ctx context.Context) (bool, error) {
	var chain pgtype.Numeric
	if err := chain.Scan(c.ChainID); err != nil {
		return false, err
	}
	q := dbgen.New(c.DB)
	job, err := q.BALDemandClaim(ctx, (c.Timeout + time.Second).Milliseconds(), chain)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	fetchCtx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	raw, err := q.NativeTransferSourceBlock(fetchCtx, chain, job.BlockNumber, job.BlockHash)
	if err != nil {
		return true, err
	}
	block, err := chainbundle.DecodeStoredBlock(raw)
	height, errHeight := dbaccess.NumericText(job.BlockNumber)
	var result ethrpc.BALResult
	code := "invalid"
	if err == nil && errHeight == nil && block.Block != nil && block.Block.Hash() == common.BytesToHash(job.BlockHash) && strconv.FormatUint(block.Block.NumberU64(), 10) == height.String {
		result, err = c.Fetcher.Fetch(fetchCtx, block.Block)
		switch {
		case err == nil:
			code = "ready"
		case errors.Is(err, ethrpc.ErrBALUnavailable):
			code = "unavailable"
		case errors.Is(err, ethrpc.ErrBALInvalid), errors.Is(err, ethrpc.ErrBALNotApplicable):
		case job.Attempts >= 4:
			code = "unavailable"
		default:
			return true, err
		}
	}
	err = dbaccess.WithTransaction(ctx, c.DB, func(q *dbgen.Queries) error {
		rows, err := q.BALDemandFinish(ctx, dbgen.BALDemandFinishParams{Code: code, Endpoint: result.Endpoint, ChainID: chain, BlockNumber: job.BlockNumber, BlockHash: job.BlockHash, Generation: job.Generation, Attempts: job.Attempts})
		if err != nil || rows == 0 || code != "ready" {
			return err
		}
		rows, err = q.BALStoreAcquiredBody(ctx, dbgen.BALStoreAcquiredBodyParams{ChainID: chain, BlockNumber: job.BlockNumber, BlockHash: job.BlockHash, Method: result.Method, Raw: result.Raw})
		if err == nil && rows != 1 {
			return ethrpc.ErrBALInvalid
		}
		return err
	})
	return true, err
}
