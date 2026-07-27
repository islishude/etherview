package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/chainbundle"
)

var (
	ErrConflict                   = errors.New("store state conflicts with requested canonical update")
	ErrCheckpointRegress          = errors.New("checkpoint would regress")
	ErrFinalityRegress            = errors.New("finality would regress")
	ErrFinalizedRefresh           = errors.New("canonical refresh intersects finalized history")
	ErrIndexNotConfigured         = errors.New("core index coverage is not configured")
	ErrIndexConfigurationMismatch = errors.New("configured index start does not match persisted value")
	ErrLeaseLost                  = errors.New("backfill range lease is no longer owned")
)

const CoreCheckpoint = "core"

type BlockRef struct {
	Number     uint64
	Hash       common.Hash
	ParentHash common.Hash
}

func RefFromBundle(bundle chainbundle.Bundle) (BlockRef, error) {
	number, err := bundle.Number()
	if err != nil {
		return BlockRef{}, err
	}
	hash, err := bundle.BlockHash()
	if err != nil {
		return BlockRef{}, err
	}
	return BlockRef{
		Number: number, Hash: hash, ParentHash: bundle.Block.ParentHash(),
	}, nil
}

type Checkpoint struct {
	Stage             string
	ContiguousThrough uint64
	BlockHash         common.Hash
	UpdatedAt         time.Time
}

func NewCoreCheckpoint(reference BlockRef) Checkpoint {
	return Checkpoint{
		Stage:             CoreCheckpoint,
		ContiguousThrough: reference.Number,
		BlockHash:         reference.Hash,
		UpdatedAt:         time.Now().UTC(),
	}
}

type Finality struct {
	Safe      *BlockRef
	Finalized *BlockRef
	UpdatedAt time.Time
}

type Reorg struct {
	Ancestor   BlockRef
	Detached   []BlockRef           // Current tip down to Ancestor+1.
	Attached   []chainbundle.Bundle // Ancestor+1 up to the new tip.
	Checkpoint Checkpoint
	Reason     string
}

type JournalEntry struct {
	BlockHash common.Hash
	Stage     string
	Sequence  uint64
	Payload   json.RawMessage
	Canonical bool
	CreatedAt time.Time
}

// RefreshOptions carries the explicit operator authorization required to
// rewrite facts at or below finalized height. Refresh never changes canonical
// identity or advances a checkpoint, even when this override is set.
type RefreshOptions struct {
	AllowFinalized bool
}

// Repository is the correctness boundary used by the core indexer. Every
// method that changes canonicality also changes the core checkpoint in the same
// database transaction.
type Repository interface {
	ConfigureIndex(ctx context.Context, chainID string, configuredStart uint64) error
	Coverage(ctx context.Context, chainID string) (CoreCoverage, bool, error)
	CommitCanonicalSegment(ctx context.Context, chainID string, bundles []chainbundle.Bundle) (CoreCoverage, error)
	ReplaceHighestCanonicalSegment(ctx context.Context, chainID string, replacement SparseCanonicalReplacement) (CoreCoverage, error)
	ClaimBackfillRange(ctx context.Context, chainID string, target BlockRange, owner string, now time.Time, ttl time.Duration) (BackfillLease, bool, error)
	RenewBackfillRange(ctx context.Context, lease BackfillLease, now time.Time, ttl time.Duration) (BackfillLease, error)
	ReleaseBackfillRange(ctx context.Context, lease BackfillLease) error
	CompleteBackfillRange(ctx context.Context, lease BackfillLease) error
	CanonicalTip(ctx context.Context, chainID string) (BlockRef, bool, error)
	CanonicalBlock(ctx context.Context, chainID string, number uint64) (BlockRef, bool, error)
	BundleByHash(ctx context.Context, chainID string, hash common.Hash) (chainbundle.Bundle, bool, error)
	CommitCanonical(ctx context.Context, chainID string, bundle chainbundle.Bundle, checkpoint Checkpoint) error
	RefreshCanonical(ctx context.Context, chainID string, bundle chainbundle.Bundle, options RefreshOptions) error
	ApplyReorg(ctx context.Context, chainID string, reorg Reorg) error
	Checkpoint(ctx context.Context, chainID, stage string) (Checkpoint, bool, error)
	Finality(ctx context.Context, chainID string) (Finality, bool, error)
	UpdateFinality(ctx context.Context, chainID string, finality Finality) error
	AppendJournal(ctx context.Context, chainID string, entry JournalEntry) error
	JournalsByBlock(ctx context.Context, chainID string, hash common.Hash) ([]JournalEntry, error)
}

func ValidateCheckpoint(checkpoint Checkpoint, expected BlockRef) error {
	if checkpoint.Stage == "" {
		return errors.New("checkpoint stage is empty")
	}
	if checkpoint.ContiguousThrough != expected.Number {
		return fmt.Errorf("checkpoint height %d does not match block height %d", checkpoint.ContiguousThrough, expected.Number)
	}
	if checkpoint.BlockHash != expected.Hash {
		return errors.New("checkpoint block hash does not match canonical block")
	}
	return nil
}

func ValidateReorg(reorg Reorg) error {
	if len(reorg.Detached) == 0 && len(reorg.Attached) == 0 {
		return errors.New("reorg contains no canonical changes")
	}
	if err := validateBlockRef(reorg.Ancestor); err != nil {
		return fmt.Errorf("invalid common ancestor: %w", err)
	}
	for index, detached := range reorg.Detached {
		if err := validateBlockRef(detached); err != nil {
			return fmt.Errorf("invalid detached block %d: %w", index, err)
		}
		expectedNumber := reorg.Ancestor.Number + uint64(len(reorg.Detached)-index)
		if detached.Number != expectedNumber {
			return fmt.Errorf("detached block %d has height %d, expected %d", index, detached.Number, expectedNumber)
		}
		if index+1 < len(reorg.Detached) &&
			detached.ParentHash != reorg.Detached[index+1].Hash {
			return fmt.Errorf("detached block %d does not descend from the next detached block", index)
		}
	}
	if len(reorg.Detached) > 0 {
		last := reorg.Detached[len(reorg.Detached)-1]
		if last.ParentHash != reorg.Ancestor.Hash {
			return errors.New("lowest detached block does not descend from the common ancestor")
		}
	}
	parent := reorg.Ancestor
	for index, bundle := range reorg.Attached {
		if err := chainbundle.Validate(bundle); err != nil {
			return fmt.Errorf("attached block %d: %w", index, err)
		}
		ref, err := RefFromBundle(bundle)
		if err != nil {
			return fmt.Errorf("attached block %d: %w", index, err)
		}
		if ref.Number != parent.Number+1 || ref.ParentHash != parent.Hash {
			return fmt.Errorf("attached block %d does not immediately descend from its predecessor", index)
		}
		parent = ref
	}
	if err := ValidateCheckpoint(reorg.Checkpoint, parent); err != nil {
		return err
	}
	return nil
}

func ValidateFinality(finality Finality) error {
	for name, reference := range map[string]*BlockRef{"safe": finality.Safe, "finalized": finality.Finalized} {
		if reference != nil {
			if err := validateBlockRef(*reference); err != nil {
				return fmt.Errorf("invalid %s block: %w", name, err)
			}
		}
	}
	if finality.Finalized != nil && finality.Safe != nil {
		if finality.Finalized.Number > finality.Safe.Number {
			return errors.New("finalized height exceeds safe height")
		}
		if finality.Finalized.Number == finality.Safe.Number &&
			finality.Finalized.Hash != finality.Safe.Hash {
			return errors.New("safe and finalized hashes differ at the same height")
		}
	}
	return nil
}

func validateBlockRef(BlockRef) error { return nil }
