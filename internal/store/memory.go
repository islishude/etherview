package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/islishude/etherview/internal/ethrpc"
)

// MemoryRepository implements exactly the same canonical transition contract
// as PostgreSQL. It is intended for deterministic unit tests, not production.
type MemoryRepository struct {
	mu     sync.RWMutex
	chains map[string]*memoryChain
}

type memoryChain struct {
	blocks          map[string]ethrpc.Bundle
	canonical       map[uint64]ethrpc.Hash
	checkpoints     map[string]Checkpoint
	finality        *Finality
	journals        map[string][]JournalEntry
	configuredStart *uint64
	coverage        []BlockRange
	backfillLeases  map[string]BackfillLease
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{chains: make(map[string]*memoryChain)}
}

func (r *MemoryRepository) CanonicalTip(_ context.Context, chainID string) (BlockRef, bool, error) {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return BlockRef{}, false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	chain := r.chains[chainID]
	if chain == nil || len(chain.canonical) == 0 {
		return BlockRef{}, false, nil
	}
	var height uint64
	var initialized bool
	for candidate := range chain.canonical {
		if !initialized || candidate > height {
			height = candidate
			initialized = true
		}
	}
	return memoryCanonicalRef(chain, height)
}

func (r *MemoryRepository) CanonicalBlock(_ context.Context, chainID string, number uint64) (BlockRef, bool, error) {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return BlockRef{}, false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	chain := r.chains[chainID]
	if chain == nil {
		return BlockRef{}, false, nil
	}
	return memoryCanonicalRef(chain, number)
}

func (r *MemoryRepository) BundleByHash(_ context.Context, chainID string, hash ethrpc.Hash) (ethrpc.Bundle, bool, error) {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return ethrpc.Bundle{}, false, err
	}
	if _, err := ethrpc.ParseHash(hash.String()); err != nil {
		return ethrpc.Bundle{}, false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	chain := r.chains[chainID]
	if chain == nil {
		return ethrpc.Bundle{}, false, nil
	}
	bundle, exists := chain.blocks[memoryHashKey(hash)]
	if !exists {
		return ethrpc.Bundle{}, false, nil
	}
	copy, err := cloneBundle(bundle)
	return copy, true, err
}

func (r *MemoryRepository) CommitCanonical(_ context.Context, chainID string, bundle ethrpc.Bundle, checkpoint Checkpoint) error {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return err
	}
	if err := ethrpc.ValidateBundle(bundle); err != nil {
		return err
	}
	reference, err := RefFromBundle(bundle)
	if err != nil {
		return err
	}
	if err := ValidateCheckpoint(checkpoint, reference); err != nil {
		return err
	}
	copy, err := cloneBundle(bundle)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	chain := r.chain(chainID)
	if err := checkMemoryCheckpoint(chain, checkpoint, false); err != nil {
		return err
	}
	if existingHash, exists := chain.canonical[reference.Number]; exists {
		if !existingHash.Equal(reference.Hash) {
			return fmt.Errorf("%w: height %d is already canonical with another hash", ErrConflict, reference.Number)
		}
		chain.blocks[memoryHashKey(reference.Hash)] = copy
		return setMemoryCheckpoint(chain, checkpoint)
	}
	if tip, exists, err := memoryTip(chain); err != nil {
		return err
	} else if exists {
		if reference.Number != tip.Number+1 || !reference.ParentHash.Equal(tip.Hash) {
			return fmt.Errorf("%w: block does not extend canonical tip", ErrConflict)
		}
	}
	chain.blocks[memoryHashKey(reference.Hash)] = copy
	chain.canonical[reference.Number] = reference.Hash
	return setMemoryCheckpoint(chain, checkpoint)
}

func (r *MemoryRepository) RefreshCanonical(
	_ context.Context,
	chainID string,
	bundle ethrpc.Bundle,
	options RefreshOptions,
) error {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return err
	}
	if err := ethrpc.ValidateBundle(bundle); err != nil {
		return err
	}
	reference, err := RefFromBundle(bundle)
	if err != nil {
		return err
	}
	copy, err := cloneBundle(bundle)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	chain := r.chains[chainID]
	if chain == nil {
		return fmt.Errorf("%w: canonical chain is empty", ErrConflict)
	}
	canonicalHash, exists := chain.canonical[reference.Number]
	if !exists || !canonicalHash.Equal(reference.Hash) {
		return fmt.Errorf("%w: block %d hash %s is not canonical", ErrConflict, reference.Number, reference.Hash)
	}
	if err := validateMemoryRefreshParent(chain, reference); err != nil {
		return err
	}
	if !options.AllowFinalized && chain.finality != nil && chain.finality.Finalized != nil &&
		reference.Number <= chain.finality.Finalized.Number {
		return fmt.Errorf(
			"%w: block %d is at or below finalized height %d",
			ErrFinalizedRefresh, reference.Number, chain.finality.Finalized.Number,
		)
	}
	delete(chain.journals, memoryHashKey(reference.Hash))
	chain.blocks[memoryHashKey(reference.Hash)] = copy
	return nil
}

func (r *MemoryRepository) ApplyReorg(_ context.Context, chainID string, reorg Reorg) error {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return err
	}
	if err := ValidateReorg(reorg); err != nil {
		return err
	}
	attachedCopies := make([]ethrpc.Bundle, len(reorg.Attached))
	for index := range reorg.Attached {
		copy, err := cloneBundle(reorg.Attached[index])
		if err != nil {
			return err
		}
		attachedCopies[index] = copy
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	chain := r.chain(chainID)
	ancestor, exists, err := memoryCanonicalRef(chain, reorg.Ancestor.Number)
	if err != nil {
		return err
	}
	if !exists || !ancestor.Hash.Equal(reorg.Ancestor.Hash) {
		return fmt.Errorf("%w: common ancestor is not canonical", ErrConflict)
	}
	tip, exists, err := memoryTip(chain)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: canonical chain is empty", ErrConflict)
	}
	if len(reorg.Detached) > 0 && !tip.Hash.Equal(reorg.Detached[0].Hash) {
		return fmt.Errorf("%w: detached branch does not start at canonical tip", ErrConflict)
	}
	for _, reference := range reorg.Detached {
		hash, canonical := chain.canonical[reference.Number]
		if !canonical || !hash.Equal(reference.Hash) {
			return fmt.Errorf("%w: detached block %d is not canonical", ErrConflict, reference.Number)
		}
	}
	if chain.finality != nil && chain.finality.Finalized != nil && reorg.Ancestor.Number < chain.finality.Finalized.Number {
		return fmt.Errorf("%w: reorg ancestor is below finalized height", ErrConflict)
	}
	var nextRanges []BlockRange
	if chain.configuredStart != nil {
		nextRanges, err = coverageRangesAfterReorg(chain.coverage, reorg)
		if err != nil {
			return err
		}
	}
	nextBlocks := make(map[string]ethrpc.Bundle, len(chain.blocks)+len(attachedCopies))
	for key, bundle := range chain.blocks {
		nextBlocks[key] = bundle
	}
	nextCanonical := make(map[uint64]ethrpc.Hash, len(chain.canonical)+len(attachedCopies))
	for number, hash := range chain.canonical {
		nextCanonical[number] = hash
	}
	for _, bundle := range attachedCopies {
		reference, _ := RefFromBundle(bundle)
		nextBlocks[memoryHashKey(reference.Hash)] = bundle
	}
	for _, reference := range reorg.Detached {
		delete(nextCanonical, reference.Number)
	}
	for _, bundle := range attachedCopies {
		reference, _ := RefFromBundle(bundle)
		nextCanonical[reference.Number] = reference.Hash
	}
	checkpoint := reorg.Checkpoint
	checkpointExists := true
	if chain.configuredStart != nil {
		shadow := *chain
		shadow.blocks, shadow.canonical, shadow.coverage = nextBlocks, nextCanonical, nextRanges
		coverage, coverageErr := memoryCoverageLocked(&shadow)
		if coverageErr != nil {
			return coverageErr
		}
		if coverage.Contiguous == nil {
			checkpointExists = false
		} else {
			checkpoint = NewCoreCheckpoint(*coverage.Contiguous)
		}
	}
	chain.blocks, chain.canonical = nextBlocks, nextCanonical
	if chain.configuredStart != nil {
		chain.coverage = nextRanges
	}
	for _, reference := range reorg.Detached {
		markMemoryJournals(chain, reference.Hash, false)
	}
	for _, bundle := range attachedCopies {
		reference, _ := RefFromBundle(bundle)
		markMemoryJournals(chain, reference.Hash, true)
	}
	if !checkpointExists {
		delete(chain.checkpoints, CoreCheckpoint)
		return nil
	}
	return setMemoryCheckpoint(chain, checkpoint)
}

func (r *MemoryRepository) Checkpoint(_ context.Context, chainID, stage string) (Checkpoint, bool, error) {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return Checkpoint{}, false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	chain := r.chains[chainID]
	if chain == nil {
		return Checkpoint{}, false, nil
	}
	checkpoint, exists := chain.checkpoints[stage]
	return checkpoint, exists, nil
}

func (r *MemoryRepository) Finality(_ context.Context, chainID string) (Finality, bool, error) {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return Finality{}, false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	chain := r.chains[chainID]
	if chain == nil || chain.finality == nil {
		return Finality{}, false, nil
	}
	return cloneFinality(*chain.finality), true, nil
}

func (r *MemoryRepository) UpdateFinality(_ context.Context, chainID string, finality Finality) error {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return err
	}
	if err := ValidateFinality(finality); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	chain := r.chain(chainID)
	for name, reference := range map[string]*BlockRef{"safe": finality.Safe, "finalized": finality.Finalized} {
		if reference == nil {
			continue
		}
		canonical, exists, err := memoryCanonicalRef(chain, reference.Number)
		if err != nil {
			return err
		}
		if !exists || !canonical.Hash.Equal(reference.Hash) {
			return fmt.Errorf("%w: %s block is not canonical", ErrConflict, name)
		}
	}
	if chain.finality != nil {
		if err := checkFinalityRegression(*chain.finality, finality); err != nil {
			return err
		}
	}
	copy := cloneFinality(finality)
	if copy.UpdatedAt.IsZero() {
		copy.UpdatedAt = time.Now().UTC()
	}
	chain.finality = &copy
	return nil
}

func (r *MemoryRepository) AppendJournal(_ context.Context, chainID string, entry JournalEntry) error {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return err
	}
	if entry.Stage == "" {
		return errors.New("journal stage is empty")
	}
	if !json.Valid(entry.Payload) {
		return errors.New("journal payload is not valid JSON")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	chain := r.chain(chainID)
	if _, exists := chain.blocks[memoryHashKey(entry.BlockHash)]; !exists {
		return fmt.Errorf("%w: journal block is unknown", ErrConflict)
	}
	entries := chain.journals[memoryHashKey(entry.BlockHash)]
	for _, existing := range entries {
		if existing.Stage == entry.Stage && existing.Sequence == entry.Sequence {
			return fmt.Errorf("%w: duplicate journal sequence", ErrConflict)
		}
	}
	entry.Payload = append(json.RawMessage(nil), entry.Payload...)
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	entry.Canonical = memoryHashCanonical(chain, entry.BlockHash)
	chain.journals[memoryHashKey(entry.BlockHash)] = append(entries, entry)
	return nil
}

func (r *MemoryRepository) JournalsByBlock(_ context.Context, chainID string, hash ethrpc.Hash) ([]JournalEntry, error) {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return nil, err
	}
	if _, err := ethrpc.ParseHash(hash.String()); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	chain := r.chains[chainID]
	if chain == nil {
		return nil, nil
	}
	entries := append([]JournalEntry(nil), chain.journals[memoryHashKey(hash)]...)
	for index := range entries {
		entries[index].Payload = append(json.RawMessage(nil), entries[index].Payload...)
	}
	sort.Slice(entries, func(left, right int) bool {
		if entries[left].Stage == entries[right].Stage {
			return entries[left].Sequence < entries[right].Sequence
		}
		return entries[left].Stage < entries[right].Stage
	})
	return entries, nil
}

func (r *MemoryRepository) chain(chainID string) *memoryChain {
	chain := r.chains[chainID]
	if chain == nil {
		chain = &memoryChain{
			blocks:         make(map[string]ethrpc.Bundle),
			canonical:      make(map[uint64]ethrpc.Hash),
			checkpoints:    make(map[string]Checkpoint),
			journals:       make(map[string][]JournalEntry),
			backfillLeases: make(map[string]BackfillLease),
		}
		r.chains[chainID] = chain
	}
	return chain
}

func memoryCanonicalRef(chain *memoryChain, number uint64) (BlockRef, bool, error) {
	hash, exists := chain.canonical[number]
	if !exists {
		return BlockRef{}, false, nil
	}
	bundle, exists := chain.blocks[memoryHashKey(hash)]
	if !exists {
		return BlockRef{}, false, fmt.Errorf("canonical block %d has no stored bundle", number)
	}
	reference, err := RefFromBundle(bundle)
	return reference, true, err
}

func memoryTip(chain *memoryChain) (BlockRef, bool, error) {
	if len(chain.canonical) == 0 {
		return BlockRef{}, false, nil
	}
	var height uint64
	var initialized bool
	for candidate := range chain.canonical {
		if !initialized || candidate > height {
			height = candidate
			initialized = true
		}
	}
	return memoryCanonicalRef(chain, height)
}

func validateMemoryRefreshParent(chain *memoryChain, reference BlockRef) error {
	if reference.Number == 0 {
		return nil
	}
	parent, exists, err := memoryCanonicalRef(chain, reference.Number-1)
	if err != nil {
		return err
	}
	if exists {
		if !reference.ParentHash.Equal(parent.Hash) {
			return fmt.Errorf(
				"%w: block %d parent %s does not match canonical block %d hash %s",
				ErrConflict, reference.Number, reference.ParentHash, parent.Number, parent.Hash,
			)
		}
		return nil
	}
	for number := range chain.canonical {
		if number < reference.Number {
			return fmt.Errorf("%w: canonical gap before block %d", ErrConflict, reference.Number)
		}
	}
	return nil
}

func checkMemoryCheckpoint(chain *memoryChain, checkpoint Checkpoint, allowRegression bool) error {
	if previous, exists := chain.checkpoints[checkpoint.Stage]; exists && !allowRegression {
		if checkpoint.ContiguousThrough < previous.ContiguousThrough {
			return ErrCheckpointRegress
		}
		if checkpoint.ContiguousThrough == previous.ContiguousThrough && !checkpoint.BlockHash.Equal(previous.BlockHash) {
			return fmt.Errorf("%w: hash changed at height %d", ErrCheckpointRegress, checkpoint.ContiguousThrough)
		}
	}
	return nil
}

func setMemoryCheckpoint(chain *memoryChain, checkpoint Checkpoint) error {
	if checkpoint.UpdatedAt.IsZero() {
		checkpoint.UpdatedAt = time.Now().UTC()
	}
	chain.checkpoints[checkpoint.Stage] = checkpoint
	return nil
}

func markMemoryJournals(chain *memoryChain, hash ethrpc.Hash, canonical bool) {
	key := memoryHashKey(hash)
	entries := chain.journals[key]
	for index := range entries {
		entries[index].Canonical = canonical
	}
	chain.journals[key] = entries
}

func memoryHashCanonical(chain *memoryChain, hash ethrpc.Hash) bool {
	for _, canonicalHash := range chain.canonical {
		if canonicalHash.Equal(hash) {
			return true
		}
	}
	return false
}

func memoryHashKey(hash ethrpc.Hash) string { return strings.ToLower(hash.String()) }

func cloneBundle(bundle ethrpc.Bundle) (ethrpc.Bundle, error) {
	blockJSON, err := json.Marshal(bundle.Block)
	if err != nil {
		return ethrpc.Bundle{}, err
	}
	receiptsJSON, err := json.Marshal(bundle.Receipts)
	if err != nil {
		return ethrpc.Bundle{}, err
	}
	var clone ethrpc.Bundle
	if err := json.Unmarshal(blockJSON, &clone.Block); err != nil {
		return ethrpc.Bundle{}, err
	}
	if err := json.Unmarshal(receiptsJSON, &clone.Receipts); err != nil {
		return ethrpc.Bundle{}, err
	}
	return clone, nil
}

func cloneFinality(finality Finality) Finality {
	copy := finality
	if finality.Safe != nil {
		safe := *finality.Safe
		copy.Safe = &safe
	}
	if finality.Finalized != nil {
		finalized := *finality.Finalized
		copy.Finalized = &finalized
	}
	return copy
}

func checkFinalityRegression(previous, next Finality) error {
	for name, pair := range map[string][2]*BlockRef{
		"safe":      {previous.Safe, next.Safe},
		"finalized": {previous.Finalized, next.Finalized},
	} {
		if pair[0] == nil {
			continue
		}
		if pair[1] == nil {
			return fmt.Errorf("%w: %s marker was cleared", ErrFinalityRegress, name)
		}
		if pair[1].Number < pair[0].Number {
			return fmt.Errorf("%w: %s height moved from %d to %d", ErrFinalityRegress, name, pair[0].Number, pair[1].Number)
		}
		if pair[1].Number == pair[0].Number && !pair[1].Hash.Equal(pair[0].Hash) {
			return fmt.Errorf("%w: %s hash changed at height %d", ErrFinalityRegress, name, pair[1].Number)
		}
	}
	return nil
}
