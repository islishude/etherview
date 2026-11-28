package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/consensus"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type BeaconCollector struct {
	Archive               consensus.Archive
	Namespace             string
	DB                    dbaccess.Database
	ChainID               uint64
	Client                *consensus.Client
	Timeout, PollInterval time.Duration
	Logger                *slog.Logger
}

func (*BeaconCollector) Name() string { return "beacon-inputs" }
func (c *BeaconCollector) Run(ctx context.Context) error {
	ticker := time.NewTicker(c.PollInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		found, err := c.CollectOne(ctx)
		if found && err == nil {
			continue
		}
		if err != nil && c.Logger != nil && ctx.Err() == nil {
			c.Logger.WarnContext(ctx, "Beacon acquisition deferred", "error_code", "beacon_acquisition_unavailable")
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
	return nil
}
func (c *BeaconCollector) CollectOne(ctx context.Context) (bool, error) {
	q := dbgen.New(c.DB)
	var chain pgtype.Numeric
	if err := chain.Scan(strconv.FormatUint(c.ChainID, 10)); err != nil {
		return false, err
	}
	job, err := q.BlockInputClaim(ctx, (c.Timeout + time.Second).Milliseconds(), chain, []string{"consensus", "requests", "blob"})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	raw, err := q.ConsensusSource(ctx, chain, job.BlockNumber, job.BlockHash)
	if err != nil {
		return true, err
	}
	finish := dbgen.BlockInputFinishParams{ChainID: chain, BlockNumber: job.BlockNumber, BlockHash: job.BlockHash, Kind: job.Kind, Generation: job.Generation, Attempts: job.Attempts}
	block, err := chainbundle.DecodeStoredBlock(raw)
	height, heightErr := dbaccess.NumericText(job.BlockNumber)
	if err != nil || heightErr != nil || block.Block == nil || block.Block.Hash() != common.BytesToHash(job.BlockHash) || strconv.FormatUint(block.Block.NumberU64(), 10) != height.String {
		finish.ErrorCode = "consensus_execution_identity"
	} else {
		fetchCtx, cancel := context.WithTimeout(ctx, c.Timeout)
		defer cancel()
		var evidence consensus.Evidence
		var fetchErr error
		if job.Kind == "blob" {
			row, e := q.ConsensusReadObservation(fetchCtx, chain, job.BlockHash)
			if e != nil {
				return true, e
			}
			var observation consensus.Observation
			if json.Unmarshal(row.Observation, &observation) != nil || observation.ExecutionHash != block.Block.Hash().Hex() {
				return true, errors.New("consensus_observation_invalid")
			}
			refs, e := consensus.References(block.Block, observation)
			if e == nil {
				refs, e = c.Client.ArchiveBlock(fetchCtx, c.Archive, c.Namespace, observation, refs)
			}
			evidence.Blobs, fetchErr = refs, e
		} else {
			var network consensus.Network
			network, fetchErr = c.Client.Network(fetchCtx, c.ChainID)
			if fetchErr == nil {
				evidence, fetchErr = c.Client.FetchBlock(fetchCtx, network, block.Block, job.Kind == "requests")
			}
		}
		if fetchErr != nil {
			remote, ok := errors.AsType[*consensus.Error](fetchErr)
			if !ok || remote.Retry && job.Attempts < 4 {
				return true, fetchErr
			}
			finish.ErrorCode = remote.Code
		} else {
			finish.Evidence, err = json.Marshal(evidence)
			if err != nil {
				return true, err
			}
		}
	}
	_, err = q.BlockInputFinish(ctx, finish)
	return true, err
}
