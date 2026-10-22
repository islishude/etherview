package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/consensus"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type ConsensusProcessor struct {
	DB        dbaccess.Database
	Client    *consensus.Client
	Archive   consensus.Archive
	Namespace string
	Blobs     bool
}

func (p *ConsensusProcessor) Stage() StageID {
	if p.Blobs {
		return BlobStage
	}
	return ConsensusStage
}
func (p *ConsensusProcessor) ProcessLease(ctx context.Context, lease Lease, queue *PostgresJobQueue) (StageResult, error) {
	return p.Process(ctx, bindStagePublication(lease.Job, lease, queue))
}
func (p *ConsensusProcessor) Process(ctx context.Context, job Job) (StageResult, error) {
	if job.Validate() != nil || job.Stage != p.Stage() {
		return StageResult{}, Permanent(errors.New("invalid_consensus_job"))
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
		return StageResult{}, Permanent(errors.New("consensus_execution_identity"))
	}
	block := bundle.Block
	if block.Difficulty().Sign() != 0 {
		return StageResult{}, Unavailable(errors.New("not_applicable"))
	}
	if p.Blobs {
		return p.archiveBlock(ctx, job, chain, number, block)
	}
	id, err := strconv.ParseUint(job.ChainID, 10, 64)
	if err != nil {
		return StageResult{}, Permanent(errors.New("consensus_chain_id"))
	}
	network, err := p.Client.Network(ctx, id)
	if err != nil {
		return consensusFailure(err)
	}
	observation, err := p.observeBlock(ctx, job, chain, number, network, block)
	if err != nil {
		return consensusFailure(err)
	}
	identity, err := json.Marshal(network)
	if err != nil {
		return StageResult{}, err
	}
	encoded, err := json.Marshal(observation)
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
		slot, _ := consensusNumeric(strconv.FormatUint(observation.Slot, 10))
		n, err = q.ConsensusWriteObservation(ctx, dbgen.ConsensusWriteObservationParams{ChainID: chain, BlockNumber: number, BlockHash: job.BlockHash[:], Slot: slot, Observation: encoded, DurableJobID: jobID, JobGeneration: generation})
		if err != nil {
			return StageResult{}, err
		}
		if n != 1 {
			return StageResult{}, Permanent(errors.New("consensus_observation_conflict"))
		}
		if _, err := requestDependentStageReplayTx(ctx, tx, job, BlobStage); err != nil {
			return StageResult{}, err
		}
		if err := q.ConsensusEvent(ctx, chain); err != nil {
			return StageResult{}, err
		}
		return StageResult{State: ResultComplete}, nil
	})
}
func (p *ConsensusProcessor) observeBlock(ctx context.Context, job Job, chain, number pgtype.Numeric, network consensus.Network, block *types.Block) (consensus.Observation, error) {
	raw, err := dbgen.New(p.DB).ConsensusRetainedObservation(ctx, chain, number, job.BlockHash[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return p.Client.Observe(ctx, network, block)
	}
	if err != nil {
		return consensus.Observation{}, err
	}
	// Reuse immutable, previously authenticated evidence after a reattach.
	// The publication transaction still checks the freshly read network identity.
	var observed consensus.Observation
	if json.Unmarshal(raw, &observed) != nil || observed.ExecutionHash != block.Hash().Hex() || observed.ExecutionParent != block.ParentHash().Hex() {
		return consensus.Observation{}, Permanent(errors.New("consensus_observation_invalid"))
	}
	return observed, nil
}
func (p *ConsensusProcessor) archiveBlock(ctx context.Context, job Job, chain, number pgtype.Numeric, block *types.Block) (StageResult, error) {
	row, err := dbgen.New(p.DB).ConsensusReadObservation(ctx, chain, job.BlockHash[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return StageResult{}, errors.New("consensus_dependency_pending")
	}
	if err != nil {
		return StageResult{}, err
	}
	var observation consensus.Observation
	if json.Unmarshal(row.Observation, &observation) != nil {
		return StageResult{}, Permanent(errors.New("consensus_observation_invalid"))
	}
	refs, err := consensus.References(block, observation)
	if err != nil {
		return consensusFailure(err)
	}
	refs, err = p.restoreManifest(ctx, job, chain, number, refs)
	if err != nil {
		return consensusFailure(err)
	}
	refs, err = p.Client.ArchiveBlock(ctx, p.Archive, p.Namespace, observation, refs)
	if err != nil {
		return consensusFailure(err)
	}
	encoded, err := json.Marshal(refs)
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
		current, err := q.ConsensusReadObservation(ctx, chain, job.BlockHash[:])
		if err != nil {
			return StageResult{}, err
		}
		if current.DurableJobID != row.DurableJobID || current.JobGeneration != row.JobGeneration {
			return StageResult{}, errors.New("consensus_dependency_changed")
		}
		id, generation, err := durableJobGeneration(job)
		if err != nil {
			return StageResult{}, err
		}
		n, err := q.ConsensusWriteManifest(ctx, dbgen.ConsensusWriteManifestParams{ChainID: chain, BlockNumber: number, BlockHash: job.BlockHash[:], Manifest: encoded, DurableJobID: id, JobGeneration: generation})
		if err != nil {
			return StageResult{}, err
		}
		if n != 1 {
			return StageResult{}, Permanent(errors.New("blob_manifest_conflict"))
		}
		if err := q.ConsensusEvent(ctx, chain); err != nil {
			return StageResult{}, err
		}
		return StageResult{State: ResultComplete}, nil
	})
}
func (p *ConsensusProcessor) restoreManifest(ctx context.Context, job Job, chain, number pgtype.Numeric, refs []consensus.BlobReference) ([]consensus.BlobReference, error) {
	raw, err := dbgen.New(p.DB).ConsensusRetainedManifest(ctx, chain, number, job.BlockHash[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return refs, nil
	}
	if err != nil {
		return nil, err
	}
	var retained []consensus.BlobReference
	if json.Unmarshal(raw, &retained) != nil {
		return nil, Permanent(errors.New("blob_manifest_invalid"))
	}
	return consensus.RestoreReferences(refs, retained, p.Namespace)
}
func consensusFailure(err error) (StageResult, error) {
	var remote *consensus.Error
	if !errors.As(err, &remote) {
		return StageResult{}, err
	}
	if remote.Retry {
		return StageResult{}, err
	}
	switch remote.Code {
	case "not_applicable", "beacon_not_found", "unsupported_fork", "beacon_unsupported_endpoint":
		return StageResult{}, Unavailable(remote)
	}
	return StageResult{}, Permanent(err)
}
func consensusNumeric(value string) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	err := n.Scan(value)
	return n, err
}
