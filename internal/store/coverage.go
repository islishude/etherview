package store

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/ethrpc"
)

const MaxBackfillRangeBlocks uint64 = 256

// BlockRange is an inclusive canonical-height interval.
type BlockRange struct {
	Start uint64
	End   uint64
}

// CoreCoverage is the normalized, durable view of committed core history.
// Contiguous is present only when coverage starts at ConfiguredStart. Highest
// can be ahead of Contiguous when live indexing has created an island.
type CoreCoverage struct {
	ConfiguredStart uint64
	Ranges          []BlockRange
	Contiguous      *BlockRef
	Highest         *BlockRef
}

type BackfillLease struct {
	ChainID   string
	Range     BlockRange
	Owner     string
	Token     string
	ExpiresAt time.Time
}

// SparseCanonicalReplacement replaces the highest disconnected coverage
// island. Range is always the complete old highest range. In the ordinary
// form Detached is that range from tip to start and Attached starts at
// Range.Start. When Ancestor is present, the transition also replaces every
// covered canonical block above that lower common ancestor; Detached remains
// height-descending but can cross uncovered gaps, and Attached starts at
// Ancestor+1. The generalized form lets one transaction repair a shallow fork
// discovered while closing a gap below a live island.
type SparseCanonicalReplacement struct {
	Range    BlockRange
	Ancestor *BlockRef
	Detached []BlockRef
	Attached []ethrpc.Bundle
	Reason   string
}

func validateSparseCanonicalReplacement(
	replacement SparseCanonicalReplacement,
) ([]BlockRef, []ethrpc.Bundle, error) {
	if replacement.Range.End < replacement.Range.Start {
		return nil, nil, errors.New("sparse canonical replacement range is invalid")
	}
	wantDetached := replacement.Range.End - replacement.Range.Start + 1
	if replacement.Ancestor == nil && uint64(len(replacement.Detached)) != wantDetached {
		return nil, nil, errors.New("sparse canonical replacement must detach the complete old range")
	}
	if replacement.Ancestor != nil {
		if err := validateBlockRef(*replacement.Ancestor); err != nil {
			return nil, nil, fmt.Errorf("invalid sparse replacement ancestor: %w", err)
		}
		if replacement.Ancestor.Number >= replacement.Range.Start ||
			uint64(len(replacement.Detached)) < wantDetached {
			return nil, nil, errors.New("generalized sparse replacement has an invalid ancestor or detached branch")
		}
	}
	for index, reference := range replacement.Detached {
		if err := validateBlockRef(reference); err != nil {
			return nil, nil, fmt.Errorf("invalid sparse detached block %d: %w", index, err)
		}
		if uint64(index) < wantDetached {
			expected := replacement.Range.End - uint64(index)
			if reference.Number != expected {
				return nil, nil, fmt.Errorf("sparse detached block %d has height %d, expected %d", index, reference.Number, expected)
			}
		}
		if index+1 < len(replacement.Detached) {
			next := replacement.Detached[index+1]
			if next.Number >= reference.Number {
				return nil, nil, errors.New("sparse detached branch is not strictly height-descending")
			}
			if next.Number+1 == reference.Number && !reference.ParentHash.Equal(next.Hash) {
				return nil, nil, errors.New("sparse detached branch is not internally continuous")
			}
		}
	}
	if replacement.Ancestor != nil {
		lowest := replacement.Detached[len(replacement.Detached)-1]
		if lowest.Number == replacement.Ancestor.Number+1 && !lowest.ParentHash.Equal(replacement.Ancestor.Hash) {
			return nil, nil, errors.New("lowest sparse detached block does not descend from the ancestor")
		}
	}
	if len(replacement.Attached) == 0 {
		if replacement.Ancestor == nil {
			return nil, nil, errors.New("sparse replacement without an ancestor requires an attached branch")
		}
		reason := strings.TrimSpace(replacement.Reason)
		if reason == "" || len(reason) > 1024 {
			return nil, nil, errors.New("sparse canonical replacement reason must contain 1 to 1024 characters")
		}
		return nil, nil, nil
	}
	attached, copies, err := validateCanonicalSegment(replacement.Attached)
	if err != nil {
		return nil, nil, fmt.Errorf("validate sparse attached branch: %w", err)
	}
	expectedAttachedStart := replacement.Range.Start
	if replacement.Ancestor != nil {
		if replacement.Ancestor.Number == math.MaxUint64 {
			return nil, nil, errors.New("sparse replacement ancestor height overflows")
		}
		expectedAttachedStart = replacement.Ancestor.Number + 1
		if !attached[0].ParentHash.Equal(replacement.Ancestor.Hash) {
			return nil, nil, errors.New("sparse attached branch does not descend from the ancestor")
		}
	}
	if attached[0].Number != expectedAttachedStart {
		return nil, nil, errors.New("sparse attached branch starts at the wrong height")
	}
	reason := strings.TrimSpace(replacement.Reason)
	if reason == "" || len(reason) > 1024 {
		return nil, nil, errors.New("sparse canonical replacement reason must contain 1 to 1024 characters")
	}
	return attached, copies, nil
}

func validateCanonicalSegment(bundles []ethrpc.Bundle) ([]BlockRef, []ethrpc.Bundle, error) {
	if len(bundles) == 0 {
		return nil, nil, errors.New("canonical segment is empty")
	}
	references := make([]BlockRef, len(bundles))
	copies := make([]ethrpc.Bundle, len(bundles))
	for index, bundle := range bundles {
		if err := ethrpc.ValidateBundle(bundle); err != nil {
			return nil, nil, fmt.Errorf("canonical segment block %d: %w", index, err)
		}
		reference, err := RefFromBundle(bundle)
		if err != nil {
			return nil, nil, fmt.Errorf("canonical segment block %d: %w", index, err)
		}
		if index > 0 {
			previous := references[index-1]
			if previous.Number == math.MaxUint64 || reference.Number != previous.Number+1 ||
				!reference.ParentHash.Equal(previous.Hash) {
				return nil, nil, fmt.Errorf("%w: canonical segment block %d does not descend from block %d", ErrConflict, reference.Number, previous.Number)
			}
		}
		copy, err := cloneBundle(bundle)
		if err != nil {
			return nil, nil, fmt.Errorf("clone canonical segment block %d: %w", index, err)
		}
		references[index], copies[index] = reference, copy
	}
	return references, copies, nil
}

func normalizeCoverageRanges(ranges []BlockRange) ([]BlockRange, error) {
	if len(ranges) == 0 {
		return []BlockRange{}, nil
	}
	normalized := append([]BlockRange(nil), ranges...)
	for _, blockRange := range normalized {
		if blockRange.End < blockRange.Start {
			return nil, errors.New("coverage range ends before it starts")
		}
	}
	sort.Slice(normalized, func(left, right int) bool {
		if normalized[left].Start == normalized[right].Start {
			return normalized[left].End < normalized[right].End
		}
		return normalized[left].Start < normalized[right].Start
	})
	merged := make([]BlockRange, 0, len(normalized))
	for _, current := range normalized {
		if len(merged) == 0 {
			merged = append(merged, current)
			continue
		}
		last := &merged[len(merged)-1]
		adjacent := last.End != math.MaxUint64 && current.Start == last.End+1
		if current.Start <= last.End || adjacent {
			if current.End > last.End {
				last.End = current.End
			}
			continue
		}
		merged = append(merged, current)
	}
	return merged, nil
}

func coverageRangesFromRefs(references []BlockRef, configuredStart uint64) ([]BlockRange, error) {
	if len(references) == 0 {
		return []BlockRange{}, nil
	}
	ordered := append([]BlockRef(nil), references...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].Number < ordered[right].Number })
	ranges := make([]BlockRange, 0)
	var previous *BlockRef
	for index := range ordered {
		reference := ordered[index]
		if reference.Number < configuredStart {
			continue
		}
		if previous != nil && reference.Number == previous.Number {
			return nil, fmt.Errorf("%w: multiple canonical hashes at height %d", ErrConflict, reference.Number)
		}
		continues := previous != nil && previous.Number != math.MaxUint64 &&
			reference.Number == previous.Number+1 && reference.ParentHash.Equal(previous.Hash)
		if !continues {
			ranges = append(ranges, BlockRange{Start: reference.Number, End: reference.Number})
		} else {
			ranges[len(ranges)-1].End = reference.Number
		}
		previous = &ordered[index]
	}
	return ranges, nil
}

func validateNormalizedCoverageRanges(ranges []BlockRange) error {
	normalized, err := normalizeCoverageRanges(ranges)
	if err != nil {
		return err
	}
	if len(normalized) != len(ranges) {
		return errors.New("stored coverage ranges overlap or are adjacent")
	}
	for index := range ranges {
		if normalized[index] != ranges[index] {
			return errors.New("stored coverage ranges are not normalized")
		}
	}
	return nil
}

func validateBackfillClaim(target BlockRange, owner string, now time.Time, ttl time.Duration) error {
	if target.End < target.Start {
		return errors.New("backfill range ends before it starts")
	}
	if target.End-target.Start >= MaxBackfillRangeBlocks {
		return fmt.Errorf("backfill range exceeds %d blocks", MaxBackfillRangeBlocks)
	}
	owner = strings.TrimSpace(owner)
	if owner == "" || len(owner) > 128 {
		return errors.New("backfill lease owner must contain 1 to 128 characters")
	}
	if now.IsZero() {
		return errors.New("backfill lease time is required")
	}
	if ttl <= 0 {
		return errors.New("backfill lease TTL must be positive")
	}
	if now.Add(ttl).Before(now) {
		return errors.New("backfill lease expiry overflows time")
	}
	return nil
}

func validateBackfillLease(lease BackfillLease) error {
	if _, err := normalizeChainID(lease.ChainID); err != nil {
		return err
	}
	if lease.Range.End < lease.Range.Start {
		return errors.New("backfill lease range ends before it starts")
	}
	if strings.TrimSpace(lease.Owner) == "" || len(lease.Owner) > 128 {
		return errors.New("backfill lease owner is invalid")
	}
	if _, err := uuid.Parse(lease.Token); err != nil {
		return errors.New("backfill lease token is invalid")
	}
	if lease.ExpiresAt.IsZero() {
		return errors.New("backfill lease expiry is required")
	}
	return nil
}

func newBackfillLease(chainID string, target BlockRange, owner string, now time.Time, ttl time.Duration) BackfillLease {
	return BackfillLease{
		ChainID: chainID, Range: target, Owner: strings.TrimSpace(owner),
		Token: uuid.NewString(), ExpiresAt: now.UTC().Add(ttl),
	}
}

func rangesOverlap(left, right BlockRange) bool {
	return left.Start <= right.End && right.Start <= left.End
}

func rangeCovered(ranges []BlockRange, target BlockRange) bool {
	for _, candidate := range ranges {
		if candidate.Start <= target.Start && candidate.End >= target.End {
			return true
		}
		if candidate.Start > target.Start {
			return false
		}
	}
	return false
}

func rangeIntersectsCoverage(ranges []BlockRange, target BlockRange) bool {
	for _, candidate := range ranges {
		if rangesOverlap(candidate, target) {
			return true
		}
		if candidate.Start > target.End {
			return false
		}
	}
	return false
}

func coverageRangesAfterReorg(ranges []BlockRange, reorg Reorg) ([]BlockRange, error) {
	next := append([]BlockRange(nil), ranges...)
	if len(reorg.Detached) > 0 {
		if reorg.Ancestor.Number == math.MaxUint64 {
			return nil, fmt.Errorf("%w: reorg ancestor height overflows", ErrConflict)
		}
		removed := BlockRange{Start: reorg.Ancestor.Number + 1, End: reorg.Detached[0].Number}
		if !rangeCovered(next, removed) {
			return nil, fmt.Errorf("%w: detached reorg branch is not fully covered", ErrConflict)
		}
		trimmed := make([]BlockRange, 0, len(next)+1)
		for _, current := range next {
			if !rangesOverlap(current, removed) {
				trimmed = append(trimmed, current)
				continue
			}
			if current.Start < removed.Start {
				trimmed = append(trimmed, BlockRange{Start: current.Start, End: removed.Start - 1})
			}
			if current.End > removed.End && removed.End != math.MaxUint64 {
				trimmed = append(trimmed, BlockRange{Start: removed.End + 1, End: current.End})
			}
		}
		next = trimmed
	}
	if len(reorg.Attached) > 0 {
		first, err := RefFromBundle(reorg.Attached[0])
		if err != nil {
			return nil, err
		}
		last, err := RefFromBundle(reorg.Attached[len(reorg.Attached)-1])
		if err != nil {
			return nil, err
		}
		next = append(next, BlockRange{Start: first.Number, End: last.Number})
	}
	return normalizeCoverageRanges(next)
}

func coverageRangesAfterSparseReplacement(
	ranges []BlockRange,
	replacement SparseCanonicalReplacement,
	attached []BlockRef,
) ([]BlockRange, error) {
	next := append([]BlockRange(nil), ranges...)
	for _, detached := range replacement.Detached {
		removed := false
		trimmed := make([]BlockRange, 0, len(next)+1)
		for _, current := range next {
			if detached.Number < current.Start || detached.Number > current.End {
				trimmed = append(trimmed, current)
				continue
			}
			removed = true
			if current.Start < detached.Number {
				trimmed = append(trimmed, BlockRange{Start: current.Start, End: detached.Number - 1})
			}
			if detached.Number < current.End && detached.Number != math.MaxUint64 {
				trimmed = append(trimmed, BlockRange{Start: detached.Number + 1, End: current.End})
			}
		}
		if !removed {
			return nil, fmt.Errorf("%w: detached sparse block %d is not covered", ErrConflict, detached.Number)
		}
		next = trimmed
	}
	if len(attached) > 0 {
		next = append(next, BlockRange{Start: attached[0].Number, End: attached[len(attached)-1].Number})
	}
	return normalizeCoverageRanges(next)
}

func cloneCoverage(coverage CoreCoverage) CoreCoverage {
	copy := coverage
	copy.Ranges = append([]BlockRange(nil), coverage.Ranges...)
	if coverage.Contiguous != nil {
		value := *coverage.Contiguous
		copy.Contiguous = &value
	}
	if coverage.Highest != nil {
		value := *coverage.Highest
		copy.Highest = &value
	}
	return copy
}
