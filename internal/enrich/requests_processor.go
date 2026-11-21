package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/consensus"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
)

// RequestsProcessor publishes a whole verified request set in one fenced transaction.
type RequestsProcessor struct {
	DB dbaccess.Database
}

func (*RequestsProcessor) Stage() StageID { return RequestsStage }
func (p *RequestsProcessor) ProcessLease(ctx context.Context, lease Lease, queue *PostgresJobQueue) (StageResult, error) {
	return p.Process(ctx, bindStagePublication(lease.Job, lease, queue))
}
func (p *RequestsProcessor) Process(ctx context.Context, job Job) (StageResult, error) {
	if job.Validate() != nil || job.Stage != RequestsStage {
		return StageResult{}, Permanent(errors.New("invalid_requests_job"))
	}
	chain, err := consensusNumeric(job.ChainID)
	if err != nil {
		return StageResult{}, err
	}
	number, _ := consensusNumeric(strconv.FormatUint(job.BlockNumber, 10))
	raw, err := dbgen.New(p.DB).ConsensusSource(ctx, chain, number, job.BlockHash[:])
	if err != nil {
		return StageResult{}, err
	}
	bundle, err := chainbundle.DecodeStoredBlock(raw)
	if err != nil || bundle.Block == nil || bundle.Block.Hash() != job.BlockHash || bundle.Block.NumberU64() != job.BlockNumber {
		return StageResult{}, Permanent(errors.New("requests_execution_identity"))
	}
	block := bundle.Block
	if block.Difficulty().Sign() != 0 || block.RequestsHash() == nil {
		return StageResult{}, Unavailable(errors.New("not_applicable"))
	}
	input, err := readBeaconInput(ctx, dbgen.New(p.DB), job, chain, number)
	if err != nil {
		return consensusFailure(err)
	}
	network := input.Network
	set, err := input.RequestSet(block)
	if err != nil {
		return retryBeaconInput(ctx, dbgen.New(p.DB), job, chain, number, input, err)
	}
	if err := consensus.ValidateRequestSet(set, block); err != nil {
		return consensusFailure(err)
	}
	items, err := json.Marshal(set.Items)
	if err != nil {
		return StageResult{}, err
	}
	manifest := set
	manifest.Items = nil
	metadata, err := json.Marshal(manifest)
	if err != nil {
		return StageResult{}, err
	}
	identity, err := json.Marshal(network)
	if err != nil {
		return StageResult{}, err
	}
	return runStageTransaction(ctx, p.DB, job, func(ctx context.Context, tx pgx.Tx) (StageResult, error) {
		canonical, err := lockCanonicalBlock(ctx, tx, job)
		if err != nil {
			return StageResult{}, err
		}
		if !canonical {
			return StageResult{State: ResultComplete, Details: map[string]string{"outcome": "stale_canonical_skipped"}}, nil
		}
		q := dbgen.New(tx)
		n, err := q.ConsensusBindNetwork(ctx, chain, identity)
		if err != nil {
			return StageResult{}, err
		}
		if n != 1 {
			return StageResult{}, Permanent(errors.New("beacon_network_mismatch"))
		}
		jobID, generation, err := durableJobGeneration(job)
		if err != nil {
			return StageResult{}, err
		}
		n, err = q.RequestsWriteSet(ctx, dbgen.RequestsWriteSetParams{ChainID: chain, BlockNumber: number, BlockHash: job.BlockHash[:], Manifest: metadata, DurableJobID: jobID, JobGeneration: generation})
		if err != nil {
			return StageResult{}, err
		}
		if n != 1 {
			return StageResult{}, Permanent(errors.New("requests_observation_conflict"))
		}
		rows, err := q.RequestsWriteItems(ctx, dbgen.RequestsWriteItemsParams{ChainID: chain, BlockNumber: number, BlockHash: job.BlockHash[:], Items: items})
		if err != nil {
			return StageResult{}, err
		}
		if len(rows) != len(set.Items) {
			return StageResult{}, Permanent(errors.New("requests_observation_conflict"))
		}
		if err := q.ConsensusEvent(ctx, chain); err != nil {
			return StageResult{}, err
		}
		return StageResult{State: ResultComplete}, nil
	})
}
