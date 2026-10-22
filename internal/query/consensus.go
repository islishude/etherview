package query

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/accelerator"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/consensus"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/publicquery"
	"github.com/jackc/pgx/v5"
)

func (r *PostgresReader) consensusSlots(ctx context.Context, q dbgen.DBTX, blocks []gen.Block) error {
	if !r.consensus.Enabled || len(blocks) == 0 {
		return nil
	}
	hashes := make([][]byte, len(blocks))
	for i, b := range blocks {
		hashes[i] = common.FromHex(b.Hash)
	}
	chain, err := r.chainNumeric()
	if err != nil {
		return err
	}
	rows, err := dbgen.New(q).ConsensusSlots(ctx, chain, hashes)
	if err != nil {
		return err
	}
	slots := make(map[common.Hash]string, len(rows))
	for _, row := range rows {
		if _, err := strconv.ParseUint(row.Slot, 10, 64); err != nil {
			return errors.New("invalid consensus slot")
		}
		slots[common.BytesToHash(row.BlockHash)] = row.Slot
	}
	for i := range blocks {
		if slot, ok := slots[common.HexToHash(blocks[i].Hash)]; ok {
			blocks[i].SlotNumber = &slot
			source := gen.Consensus
			blocks[i].SlotNumberSource = &source
		}
	}
	return nil
}
func (r *PostgresReader) consensusOverview(ctx context.Context, q dbgen.DBTX) *gen.ConsensusOverview {
	if !r.consensus.Enabled {
		return nil
	}
	result := gen.ConsensusOverview{State: gen.ConsensusStatePending, Stale: true, BlobsEnabled: r.consensus.Blobs.Enabled}
	defer func() { r.consensusBlobHealth(ctx, q, &result) }()
	chain, err := r.chainNumeric()
	if err != nil {
		return &result
	}
	row, err := dbgen.New(q).ConsensusReadOverview(ctx, chain)
	if errors.Is(err, pgx.ErrNoRows) {
		return &result
	}
	if err != nil || json.Unmarshal(row.Overview, &result) != nil {
		result.State = gen.ConsensusStateFailed
		result.Code = new("consensus_read_failed")
		return &result
	}
	if result.State == "" {
		result.State = gen.ConsensusStatePending
	}
	result.BlobsEnabled = r.consensus.Blobs.Enabled
	result.Stale = !row.ObservedAt.Valid || time.Since(row.ObservedAt.Time) > 3*r.consensus.PollInterval
	if row.ObservedAt.Valid {
		result.ObservedAt = &row.ObservedAt.Time
		deadline := row.ObservedAt.Time.Add(3 * r.consensus.PollInterval)
		result.StaleAt = &deadline
	}
	if row.LastError != "" {
		result.State = gen.ConsensusStateUnavailable
		result.Code = &row.LastError
	}
	return &result
}
func (r *PostgresReader) consensusState(ctx context.Context, hash, stage string) (gen.ConsensusState, *string, error) {
	chain, err := r.chainNumeric()
	if err != nil {
		return "", nil, err
	}
	row, err := dbgen.New(r.db).ConsensusStageState(ctx, chain, common.FromHex(hash), stage)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.ConsensusStatePending, nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	state := gen.ConsensusState(row.State.String)
	if row.Code == "not_applicable" {
		state = gen.ConsensusStateNotApplicable
	}
	return state, &row.Code, nil
}
func (r *PostgresReader) BlockConsensus(ctx context.Context, id string) (gen.ConsensusSummary, error) {
	block, err := r.Block(ctx, id)
	if err != nil {
		return gen.ConsensusSummary{}, err
	}
	result := gen.ConsensusSummary{BlockHash: block.Hash, State: gen.ConsensusStateDisabled}
	if !r.consensus.Enabled {
		return result, nil
	}
	chain, err := r.chainNumeric()
	if err != nil {
		return result, err
	}
	row, err := dbgen.New(r.db).ConsensusReadObservation(ctx, chain, common.FromHex(block.Hash))
	if errors.Is(err, pgx.ErrNoRows) {
		result.State, result.Code, err = r.consensusState(ctx, block.Hash, "consensus")
		return result, err
	}
	if err != nil {
		return result, err
	}
	var o consensus.Observation
	if json.Unmarshal(row.Observation, &o) != nil {
		return result, errors.New("invalid consensus observation")
	}
	result.State = gen.ConsensusStateComplete
	result.Slot = new(strconv.FormatUint(o.Slot, 10))
	result.Epoch = new(strconv.FormatUint(o.Epoch, 10))
	result.ProposerIndex = new(strconv.FormatUint(o.Proposer, 10))
	result.Root = &o.Root
	result.ParentRoot = &o.Parent
	result.StateRoot = &o.State
	result.BodyRoot = &o.Body
	result.Fork = &o.Fork
	result.Graffiti = &o.Graffiti
	result.SlotConflict = &o.SlotConflict
	result.Operations = &o.Operations
	return result, nil
}
func (r *PostgresReader) BlockBlobs(ctx context.Context, id string) (gen.BlockBlobs, error) {
	block, err := r.Block(ctx, id)
	if err != nil {
		return gen.BlockBlobs{}, err
	}
	result := gen.BlockBlobs{BlockHash: block.Hash, State: gen.ConsensusStateDisabled, Items: []gen.BlobInfo{}}
	if !r.consensus.Blobs.Enabled {
		return result, nil
	}
	chain, err := r.chainNumeric()
	if err != nil {
		return result, err
	}
	row, err := dbgen.New(r.db).ConsensusReadManifest(ctx, chain, common.FromHex(block.Hash))
	if errors.Is(err, pgx.ErrNoRows) {
		result.State, result.Code, err = r.consensusState(ctx, block.Hash, "blob")
		if err != nil {
			return result, err
		}
		pending, err := dbgen.New(r.db).ConsensusBlobReferences(ctx, chain, common.FromHex(block.Hash))
		if err != nil {
			return result, err
		}
		for _, ref := range pending {
			result.Items = append(result.Items, gen.BlobInfo{Index: ref.BlobIndex, TransactionHash: ref.TransactionHash, TransactionIndex: ref.TransactionIndex, VersionedHash: ref.VersionedHash})
		}
		return result, nil
	}
	if err != nil {
		return result, err
	}
	var refs []consensus.BlobReference
	if json.Unmarshal(row.Manifest, &refs) != nil {
		return result, errors.New("invalid blob manifest")
	}
	for _, ref := range refs {
		result.Items = append(result.Items, gen.BlobInfo{Index: ref.Index, TransactionHash: ref.TransactionHash, TransactionIndex: ref.TransactionIndex, VersionedHash: ref.VersionedHash, Commitment: &ref.Commitment, Size: &ref.Size, Sha256: &ref.SHA256})
	}
	result.State = gen.ConsensusStateComplete
	return result, nil
}
func (r *PostgresReader) BlobContent(ctx context.Context, hash string, index uint64) ([]byte, error) {
	if !r.consensus.Blobs.Enabled || r.blobArchive == nil {
		return nil, publicquery.ErrUnavailable
	}
	parsed, isHash, err := parseHashIdentifier(hash)
	if err != nil || !isHash {
		return nil, publicquery.ErrInvalidInput
	}
	chain, err := r.chainNumeric()
	if err != nil {
		return nil, err
	}
	row, err := dbgen.New(r.db).ConsensusReadManifest(ctx, chain, parsed[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, publicquery.ErrUnavailable
	}
	if err != nil {
		return nil, err
	}
	var refs []consensus.BlobReference
	if json.Unmarshal(row.Manifest, &refs) != nil {
		return nil, publicquery.ErrUnavailable
	}
	if index >= uint64(len(refs)) {
		return nil, publicquery.ErrNotFound
	}
	ref := refs[index]
	if ref.Index != strconv.FormatUint(index, 10) {
		return nil, publicquery.ErrUnavailable
	}
	data, err := r.blobArchive.Read(ctx, ref.ObjectKey, ref.SHA256)
	if err != nil {
		code := "blob_storage_unavailable"
		if errors.Is(err, accelerator.ErrArchiveIntegrity) {
			code = "blob_storage_integrity"
		}
		return nil, publicquery.NewCapabilityUnavailableError("blob", "unavailable", code)
	}
	// The original read is closed before object access. Recheck the exact published generation.
	current, err := dbgen.New(r.db).ConsensusReadManifest(ctx, chain, parsed[:])
	if err != nil || current.DurableJobID != row.DurableJobID || current.JobGeneration != row.JobGeneration {
		return nil, publicquery.ErrUnavailable
	}
	return data, nil
}

func (r *PostgresReader) consensusBlobHealth(ctx context.Context, q dbgen.DBTX, result *gen.ConsensusOverview) {
	result.BlobState = gen.ConsensusStateDisabled
	if !r.consensus.Blobs.Enabled {
		return
	}
	result.BlobState = gen.ConsensusStatePending
	chain, err := r.chainNumeric()
	if err != nil {
		return
	}
	row, err := dbgen.New(q).ConsensusBlobHealth(ctx, chain)
	if errors.Is(err, pgx.ErrNoRows) {
		return
	}
	if err != nil {
		result.BlobState = gen.ConsensusStateFailed
		return
	}
	result.BlobState = gen.ConsensusState(row.State.String)
	if row.Code == "not_applicable" {
		result.BlobState = gen.ConsensusStateNotApplicable
	}
	if row.Code != "" {
		result.BlobCode = &row.Code
	}
}
