package store

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/islishude/etherview/internal/ethrpc"
)

func (r *MemoryRepository) ConfigureIndex(_ context.Context, chainID string, configuredStart uint64) error {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	chain := r.chain(chainID)
	if chain.configuredStart != nil {
		if *chain.configuredStart != configuredStart {
			return fmt.Errorf("%w: stored=%d requested=%d", ErrIndexConfigurationMismatch, *chain.configuredStart, configuredStart)
		}
		return nil
	}
	references, err := memoryCanonicalReferences(chain)
	if err != nil {
		return err
	}
	ranges, err := coverageRangesFromRefs(references, configuredStart)
	if err != nil {
		return err
	}
	start := configuredStart
	chain.configuredStart = &start
	chain.coverage = ranges
	coverage, err := memoryCoverageLocked(chain)
	if err != nil {
		chain.configuredStart = nil
		chain.coverage = nil
		return err
	}
	if coverage.Contiguous != nil {
		checkpoint := NewCoreCheckpoint(*coverage.Contiguous)
		if err := checkMemoryCheckpoint(chain, checkpoint, true); err != nil {
			chain.configuredStart = nil
			chain.coverage = nil
			return err
		}
		_ = setMemoryCheckpoint(chain, checkpoint)
	} else {
		delete(chain.checkpoints, CoreCheckpoint)
	}
	return nil
}

func (r *MemoryRepository) Coverage(_ context.Context, chainID string) (CoreCoverage, bool, error) {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return CoreCoverage{}, false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	chain := r.chains[chainID]
	if chain == nil || chain.configuredStart == nil {
		return CoreCoverage{}, false, nil
	}
	coverage, err := memoryCoverageLocked(chain)
	return cloneCoverage(coverage), true, err
}

func (r *MemoryRepository) CommitCanonicalSegment(
	_ context.Context,
	chainID string,
	bundles []ethrpc.Bundle,
) (CoreCoverage, error) {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return CoreCoverage{}, err
	}
	references, copies, err := validateCanonicalSegment(bundles)
	if err != nil {
		return CoreCoverage{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	chain := r.chains[chainID]
	if chain == nil || chain.configuredStart == nil {
		return CoreCoverage{}, ErrIndexNotConfigured
	}
	first, last := references[0], references[len(references)-1]
	if first.Number < *chain.configuredStart {
		return CoreCoverage{}, fmt.Errorf("%w: segment starts before configured height %d", ErrConflict, *chain.configuredStart)
	}
	for _, reference := range references {
		existing, exists, err := memoryCanonicalRef(chain, reference.Number)
		if err != nil {
			return CoreCoverage{}, err
		}
		if exists && (!existing.Hash.Equal(reference.Hash) || !existing.ParentHash.Equal(reference.ParentHash)) {
			return CoreCoverage{}, fmt.Errorf("%w: height %d already maps to another canonical identity", ErrConflict, reference.Number)
		}
	}
	if first.Number > 0 {
		lower, exists, err := memoryCanonicalRef(chain, first.Number-1)
		if err != nil {
			return CoreCoverage{}, err
		}
		if exists && !first.ParentHash.Equal(lower.Hash) {
			return CoreCoverage{}, fmt.Errorf("%w: segment parent does not match lower canonical boundary", ErrConflict)
		}
	}
	if last.Number < math.MaxUint64 {
		upper, exists, err := memoryCanonicalRef(chain, last.Number+1)
		if err != nil {
			return CoreCoverage{}, err
		}
		if exists && !upper.ParentHash.Equal(last.Hash) {
			return CoreCoverage{}, fmt.Errorf("%w: upper canonical boundary does not descend from segment", ErrConflict)
		}
	}
	nextRanges, err := normalizeCoverageRanges(append(append([]BlockRange(nil), chain.coverage...), BlockRange{Start: first.Number, End: last.Number}))
	if err != nil {
		return CoreCoverage{}, err
	}
	nextBlocks := make(map[string]ethrpc.Bundle, len(chain.blocks)+len(copies))
	for key, bundle := range chain.blocks {
		nextBlocks[key] = bundle
	}
	nextCanonical := make(map[uint64]ethrpc.Hash, len(chain.canonical)+len(references))
	for number, hash := range chain.canonical {
		nextCanonical[number] = hash
	}
	for index, reference := range references {
		nextBlocks[memoryHashKey(reference.Hash)] = copies[index]
		nextCanonical[reference.Number] = reference.Hash
	}
	shadow := *chain
	shadow.blocks, shadow.canonical, shadow.coverage = nextBlocks, nextCanonical, nextRanges
	coverage, err := memoryCoverageLocked(&shadow)
	if err != nil {
		return CoreCoverage{}, err
	}
	var checkpoint *Checkpoint
	if coverage.Contiguous != nil {
		value := NewCoreCheckpoint(*coverage.Contiguous)
		if err := checkMemoryCheckpoint(chain, value, false); err != nil {
			return CoreCoverage{}, err
		}
		checkpoint = &value
	}
	chain.blocks, chain.canonical, chain.coverage = nextBlocks, nextCanonical, nextRanges
	if checkpoint != nil {
		_ = setMemoryCheckpoint(chain, *checkpoint)
	}
	return cloneCoverage(coverage), nil
}

func (r *MemoryRepository) ReplaceHighestCanonicalSegment(
	_ context.Context,
	chainID string,
	replacement SparseCanonicalReplacement,
) (CoreCoverage, error) {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return CoreCoverage{}, err
	}
	attached, copies, err := validateSparseCanonicalReplacement(replacement)
	if err != nil {
		return CoreCoverage{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	chain := r.chains[chainID]
	if chain == nil || chain.configuredStart == nil {
		return CoreCoverage{}, ErrIndexNotConfigured
	}
	if err := validateHighestDisconnectedRange(chain.coverage, *chain.configuredStart, replacement.Range); err != nil {
		return CoreCoverage{}, err
	}
	tip, exists, err := memoryTip(chain)
	if err != nil {
		return CoreCoverage{}, err
	}
	if !exists || tip.Number != replacement.Range.End || !tip.Hash.Equal(replacement.Detached[0].Hash) {
		return CoreCoverage{}, fmt.Errorf("%w: replacement range is not the canonical tip", ErrConflict)
	}
	for _, detached := range replacement.Detached {
		canonical, exists, err := memoryCanonicalRef(chain, detached.Number)
		if err != nil {
			return CoreCoverage{}, err
		}
		if !exists || !sameBlockIdentity(canonical, detached) {
			return CoreCoverage{}, fmt.Errorf("%w: detached block %d is not canonical", ErrConflict, detached.Number)
		}
	}
	if replacement.Ancestor != nil {
		canonicalAbove := 0
		for number := range chain.canonical {
			if number > replacement.Ancestor.Number {
				canonicalAbove++
			}
		}
		if canonicalAbove != len(replacement.Detached) {
			return CoreCoverage{}, fmt.Errorf("%w: generalized replacement does not detach every canonical block above its ancestor", ErrConflict)
		}
	}
	if chain.finality != nil && chain.finality.Finalized != nil {
		if replacement.Ancestor != nil && replacement.Ancestor.Number < chain.finality.Finalized.Number {
			return CoreCoverage{}, fmt.Errorf("%w: replacement ancestor is below finalized height", ErrConflict)
		}
		if replacement.Ancestor == nil && replacement.Range.Start <= chain.finality.Finalized.Number {
			return CoreCoverage{}, fmt.Errorf("%w: replacement starts at or below finalized height", ErrConflict)
		}
	}
	for _, reference := range attached {
		if reference.Number <= replacement.Range.End || replacement.Ancestor != nil {
			continue
		}
		if _, exists := chain.canonical[reference.Number]; exists {
			return CoreCoverage{}, fmt.Errorf("%w: attached height %d is already canonical", ErrConflict, reference.Number)
		}
	}

	nextBlocks := make(map[string]ethrpc.Bundle, len(chain.blocks)+len(copies))
	for key, bundle := range chain.blocks {
		nextBlocks[key] = bundle
	}
	nextCanonical := make(map[uint64]ethrpc.Hash, len(chain.canonical)+len(attached))
	for number, hash := range chain.canonical {
		nextCanonical[number] = hash
	}
	for _, detached := range replacement.Detached {
		delete(nextCanonical, detached.Number)
	}
	for index, reference := range attached {
		nextBlocks[memoryHashKey(reference.Hash)] = copies[index]
		nextCanonical[reference.Number] = reference.Hash
	}
	nextRanges, err := coverageRangesAfterSparseReplacement(chain.coverage, replacement, attached)
	if err != nil {
		return CoreCoverage{}, err
	}
	shadow := *chain
	shadow.blocks, shadow.canonical, shadow.coverage = nextBlocks, nextCanonical, nextRanges
	coverage, err := memoryCoverageLocked(&shadow)
	if err != nil {
		return CoreCoverage{}, err
	}
	var checkpoint *Checkpoint
	if coverage.Contiguous != nil {
		value := NewCoreCheckpoint(*coverage.Contiguous)
		if err := checkMemoryCheckpoint(chain, value, replacement.Ancestor != nil); err != nil {
			return CoreCoverage{}, err
		}
		checkpoint = &value
	}

	chain.blocks, chain.canonical, chain.coverage = nextBlocks, nextCanonical, nextRanges
	for _, detached := range replacement.Detached {
		markMemoryJournals(chain, detached.Hash, false)
	}
	for _, reference := range attached {
		markMemoryJournals(chain, reference.Hash, true)
	}
	if checkpoint != nil {
		_ = setMemoryCheckpoint(chain, *checkpoint)
	}
	return cloneCoverage(coverage), nil
}

func (r *MemoryRepository) ClaimBackfillRange(
	_ context.Context,
	chainID string,
	target BlockRange,
	owner string,
	now time.Time,
	ttl time.Duration,
) (BackfillLease, bool, error) {
	chainID, err := normalizeChainID(chainID)
	if err != nil {
		return BackfillLease{}, false, err
	}
	if err := validateBackfillClaim(target, owner, now, ttl); err != nil {
		return BackfillLease{}, false, err
	}
	now = now.UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	chain := r.chains[chainID]
	if chain == nil || chain.configuredStart == nil {
		return BackfillLease{}, false, ErrIndexNotConfigured
	}
	if target.Start < *chain.configuredStart {
		return BackfillLease{}, false, fmt.Errorf("%w: backfill range starts before configured height %d", ErrConflict, *chain.configuredStart)
	}
	if rangeIntersectsCoverage(chain.coverage, target) {
		return BackfillLease{}, false, nil
	}
	for key, lease := range chain.backfillLeases {
		if !lease.ExpiresAt.After(now) {
			delete(chain.backfillLeases, key)
			continue
		}
		if rangesOverlap(lease.Range, target) {
			return BackfillLease{}, false, nil
		}
	}
	lease := newBackfillLease(chainID, target, owner, now, ttl)
	chain.backfillLeases[memoryLeaseKey(target)] = lease
	return lease, true, nil
}

func (r *MemoryRepository) RenewBackfillRange(
	_ context.Context,
	lease BackfillLease,
	now time.Time,
	ttl time.Duration,
) (BackfillLease, error) {
	if err := validateBackfillLease(lease); err != nil {
		return BackfillLease{}, err
	}
	if err := validateBackfillClaim(lease.Range, lease.Owner, now, ttl); err != nil {
		return BackfillLease{}, err
	}
	chainID, _ := normalizeChainID(lease.ChainID)
	now = now.UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	chain := r.chains[chainID]
	current, exists := memoryLease(chain, lease.Range)
	if !exists || !sameLeaseOwner(current, lease) || !current.ExpiresAt.After(now) {
		return BackfillLease{}, ErrLeaseLost
	}
	current.ExpiresAt = now.Add(ttl)
	chain.backfillLeases[memoryLeaseKey(lease.Range)] = current
	return current, nil
}

func (r *MemoryRepository) ReleaseBackfillRange(_ context.Context, lease BackfillLease) error {
	if err := validateBackfillLease(lease); err != nil {
		return err
	}
	chainID, _ := normalizeChainID(lease.ChainID)
	r.mu.Lock()
	defer r.mu.Unlock()
	chain := r.chains[chainID]
	current, exists := memoryLease(chain, lease.Range)
	if !exists || !sameLeaseOwner(current, lease) || !current.ExpiresAt.After(time.Now().UTC()) {
		return ErrLeaseLost
	}
	delete(chain.backfillLeases, memoryLeaseKey(lease.Range))
	return nil
}

func (r *MemoryRepository) CompleteBackfillRange(_ context.Context, lease BackfillLease) error {
	if err := validateBackfillLease(lease); err != nil {
		return err
	}
	chainID, _ := normalizeChainID(lease.ChainID)
	r.mu.Lock()
	defer r.mu.Unlock()
	chain := r.chains[chainID]
	current, exists := memoryLease(chain, lease.Range)
	if !exists || !sameLeaseOwner(current, lease) || !current.ExpiresAt.After(time.Now().UTC()) {
		return ErrLeaseLost
	}
	if !rangeCovered(chain.coverage, lease.Range) {
		return fmt.Errorf("%w: backfill range is not fully covered", ErrConflict)
	}
	delete(chain.backfillLeases, memoryLeaseKey(lease.Range))
	return nil
}

func memoryCanonicalReferences(chain *memoryChain) ([]BlockRef, error) {
	numbers := make([]uint64, 0, len(chain.canonical))
	for number := range chain.canonical {
		numbers = append(numbers, number)
	}
	sort.Slice(numbers, func(left, right int) bool { return numbers[left] < numbers[right] })
	references := make([]BlockRef, 0, len(numbers))
	for _, number := range numbers {
		reference, exists, err := memoryCanonicalRef(chain, number)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("canonical block %d disappeared", number)
		}
		references = append(references, reference)
	}
	return references, nil
}

func memoryCoverageLocked(chain *memoryChain) (CoreCoverage, error) {
	if chain == nil || chain.configuredStart == nil {
		return CoreCoverage{}, ErrIndexNotConfigured
	}
	if err := validateNormalizedCoverageRanges(chain.coverage); err != nil {
		return CoreCoverage{}, err
	}
	coverage := CoreCoverage{
		ConfiguredStart: *chain.configuredStart,
		Ranges:          append([]BlockRange(nil), chain.coverage...),
	}
	if len(chain.coverage) == 0 {
		return coverage, nil
	}
	highest, exists, err := memoryCanonicalRef(chain, chain.coverage[len(chain.coverage)-1].End)
	if err != nil || !exists {
		if err == nil {
			err = fmt.Errorf("coverage highest canonical block is missing")
		}
		return CoreCoverage{}, err
	}
	coverage.Highest = &highest
	if chain.coverage[0].Start == *chain.configuredStart {
		contiguous, exists, err := memoryCanonicalRef(chain, chain.coverage[0].End)
		if err != nil || !exists {
			if err == nil {
				err = fmt.Errorf("coverage checkpoint canonical block is missing")
			}
			return CoreCoverage{}, err
		}
		coverage.Contiguous = &contiguous
	}
	return coverage, nil
}

func memoryLeaseKey(blockRange BlockRange) string {
	return strconv.FormatUint(blockRange.Start, 10) + ":" + strconv.FormatUint(blockRange.End, 10)
}

func memoryLease(chain *memoryChain, blockRange BlockRange) (BackfillLease, bool) {
	if chain == nil || chain.backfillLeases == nil {
		return BackfillLease{}, false
	}
	lease, exists := chain.backfillLeases[memoryLeaseKey(blockRange)]
	return lease, exists
}

func sameLeaseOwner(left, right BackfillLease) bool {
	return left.ChainID == right.ChainID && left.Range == right.Range && left.Owner == right.Owner && left.Token == right.Token
}

func sameBlockIdentity(left, right BlockRef) bool {
	return left.Number == right.Number && left.Hash.Equal(right.Hash) && left.ParentHash.Equal(right.ParentHash)
}
