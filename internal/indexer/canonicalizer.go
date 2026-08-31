package indexer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/store"
)

var (
	ErrGap                = errors.New("candidate head does not connect to indexed coverage")
	ErrStaleHead          = errors.New("candidate head is behind the canonical tip")
	ErrNoCommonAncestor   = errors.New("no common canonical ancestor found")
	ErrReorgTooDeep       = errors.New("reorg exceeds configured maximum depth")
	ErrFinalizedReorg     = errors.New("reorg would cross finalized history")
	ErrSourceInconsistent = errors.New("ancestor source returned an inconsistent chain")
)

type BundleSource interface {
	BundleByHash(ctx context.Context, hash common.Hash) (chainbundle.Bundle, bool, error)
}

type Canonicalizer struct {
	ChainID       string
	StartBlock    uint64
	MaxReorgDepth uint64
	Repository    store.Repository
	Source        BundleSource
	HeadSource    BundleSource
	Now           func() time.Time
}

type Disposition string

const (
	DispositionInitialized  Disposition = "initialized"
	DispositionExtended     Disposition = "extended"
	DispositionReorganized  Disposition = "reorganized"
	DispositionAlreadyKnown Disposition = "already_known"
)

type ApplyResult struct {
	Disposition Disposition
	OldTip      *store.BlockRef
	NewTip      store.BlockRef
	Ancestor    *store.BlockRef
	Detached    []store.BlockRef
	Attached    []store.BlockRef
}

func (c *Canonicalizer) Apply(ctx context.Context, candidate chainbundle.Bundle) (ApplyResult, error) {
	return c.apply(ctx, candidate, false)
}

// Refresh atomically rewrites core facts only when the candidate is already
// the canonical block at that height. It must never call Apply: doing so could
// turn an operator repair into a chain extension or reorg before the
// identity-bound store refresh is reached.
func (c *Canonicalizer) Refresh(
	ctx context.Context,
	candidate chainbundle.Bundle,
	options store.RefreshOptions,
) (ApplyResult, error) {
	if c == nil || c.Repository == nil {
		return ApplyResult{}, errors.New("canonicalizer repository is nil")
	}
	if c.ChainID == "" {
		return ApplyResult{}, errors.New("canonicalizer chain ID is empty")
	}
	if err := chainbundle.Validate(candidate); err != nil {
		return ApplyResult{}, fmt.Errorf("validate refresh candidate: %w", err)
	}
	candidateRef, err := store.RefFromBundle(candidate)
	if err != nil {
		return ApplyResult{}, err
	}
	canonical, exists, err := c.Repository.CanonicalBlock(ctx, c.ChainID, candidateRef.Number)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("read refresh canonical height: %w", err)
	}
	if !exists {
		return ApplyResult{}, fmt.Errorf("%w: refresh block %d is not canonical", ErrGap, candidateRef.Number)
	}
	if canonical.Hash != candidateRef.Hash {
		return ApplyResult{}, fmt.Errorf(
			"%w: refresh block %d hash %s does not match canonical hash %s",
			ErrStaleHead, candidateRef.Number, candidateRef.Hash, canonical.Hash,
		)
	}
	tip, hasTip, err := c.Repository.CanonicalTip(ctx, c.ChainID)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("read canonical tip before refresh: %w", err)
	}
	if !hasTip {
		return ApplyResult{}, fmt.Errorf("%w: canonical chain is empty", ErrGap)
	}
	if err := c.Repository.RefreshCanonical(ctx, c.ChainID, candidate, options); err != nil {
		return ApplyResult{}, fmt.Errorf("refresh canonical block: %w", err)
	}
	return ApplyResult{
		Disposition: DispositionAlreadyKnown,
		OldTip:      &tip,
		NewTip:      tip,
	}, nil
}

// ApplyHead applies a block obtained from the authoritative latest-tag polling
// path. Unlike historical ingestion it may accept a shorter fork-choice head;
// this distinction prevents a stale backfill block from truncating canonical
// state while still handling a node whose latest height moves backwards.
func (c *Canonicalizer) ApplyHead(ctx context.Context, candidate chainbundle.Bundle) (ApplyResult, error) {
	return c.apply(ctx, candidate, true)
}

func (c *Canonicalizer) apply(ctx context.Context, candidate chainbundle.Bundle, authoritativeHead bool) (ApplyResult, error) {
	if c == nil || c.Repository == nil {
		return ApplyResult{}, errors.New("canonicalizer repository is nil")
	}
	if c.ChainID == "" {
		return ApplyResult{}, errors.New("canonicalizer chain ID is empty")
	}
	if err := chainbundle.Validate(candidate); err != nil {
		return ApplyResult{}, fmt.Errorf("validate candidate bundle: %w", err)
	}
	candidateRef, err := store.RefFromBundle(candidate)
	if err != nil {
		return ApplyResult{}, err
	}
	tip, hasTip, err := c.Repository.CanonicalTip(ctx, c.ChainID)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("read canonical tip: %w", err)
	}
	if !hasTip {
		if candidateRef.Number != c.StartBlock {
			return ApplyResult{}, fmt.Errorf("%w: expected start block %d, got %d", ErrGap, c.StartBlock, candidateRef.Number)
		}
		if err := c.commitCanonicalBundles(ctx, []chainbundle.Bundle{candidate}, []store.BlockRef{candidateRef}); err != nil {
			return ApplyResult{}, fmt.Errorf("commit initial canonical block: %w", err)
		}
		return ApplyResult{
			Disposition: DispositionInitialized,
			NewTip:      candidateRef,
			Attached:    []store.BlockRef{candidateRef},
		}, nil
	}
	oldTip := tip
	var sparseTop *store.BlockRange
	var coverage store.CoreCoverage
	if authoritativeHead {
		coverage, _, err = c.Repository.Coverage(ctx, c.ChainID)
		if err != nil {
			return ApplyResult{}, fmt.Errorf("read canonical coverage for live head: %w", err)
		}
		if len(coverage.Ranges) > 0 {
			top := coverage.Ranges[len(coverage.Ranges)-1]
			if top.Start != coverage.ConfiguredStart && tip.Number == top.End {
				sparseTop = &top
			}
		}
	}
	canonical, canonicalExists, err := c.Repository.CanonicalBlock(ctx, c.ChainID, candidateRef.Number)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("read candidate canonical height: %w", err)
	}
	if canonicalExists && canonical.Hash == candidateRef.Hash {
		if authoritativeHead && candidateRef.Number < tip.Number {
			if sparseTop != nil && candidateRef.Number < sparseTop.Start {
				return c.truncateSparseHead(ctx, oldTip, canonical, coverage, *sparseTop)
			}
			return c.truncateHead(ctx, oldTip, canonical)
		}
		if sparseTop == nil || !c.shouldResolveSparseHead(coverage, candidateRef, tip) {
			return ApplyResult{Disposition: DispositionAlreadyKnown, OldTip: &oldTip, NewTip: tip}, nil
		}
	}
	if candidateRef.Number < tip.Number && !authoritativeHead {
		return ApplyResult{}, fmt.Errorf("%w: candidate %d, canonical tip %d", ErrStaleHead, candidateRef.Number, tip.Number)
	}
	forwardGap := uint64(0)
	if candidateRef.Number > tip.Number {
		forwardGap = candidateRef.Number - tip.Number
		if forwardGap > c.maxReorgDepth() {
			return ApplyResult{}, fmt.Errorf(
				"%w: candidate gap %d exceeds bounded ancestry work %d",
				ErrGap, forwardGap, c.maxReorgDepth(),
			)
		}
	}
	resolvingKnownSparseHead := sparseTop != nil && canonicalExists && canonical.Hash == candidateRef.Hash

	backward := []chainbundle.Bundle{candidate}
	cursor := candidate
	var ancestor store.BlockRef
	var finalized *store.BlockRef
	finalityLoaded := false
	parentFetches := uint64(0)
	maximumParentFetches := c.maximumAncestryFetches(forwardGap)
	for {
		cursorRef, err := store.RefFromBundle(cursor)
		if err != nil {
			return ApplyResult{}, err
		}
		if cursorRef.Number <= tip.Number {
			canonical, exists, err := c.Repository.CanonicalBlock(ctx, c.ChainID, cursorRef.Number)
			if err != nil {
				return ApplyResult{}, fmt.Errorf("read canonical ancestor candidate %d: %w", cursorRef.Number, err)
			}
			insideKnownSparseRange := resolvingKnownSparseHead && cursorRef.Number >= sparseTop.Start
			if exists && canonical.Hash == cursorRef.Hash && !insideKnownSparseRange {
				ancestor = canonical
				backward = backward[:len(backward)-1]
				break
			}
			distanceBelowTip := tip.Number - cursorRef.Number
			if distanceBelowTip >= c.maxReorgDepth() {
				return ApplyResult{}, fmt.Errorf(
					"%w: alternate block %d requires depth greater than %d",
					ErrReorgTooDeep, cursorRef.Number, c.maxReorgDepth(),
				)
			}
			if !finalityLoaded {
				current, exists, finalityErr := c.Repository.Finality(ctx, c.ChainID)
				if finalityErr != nil {
					return ApplyResult{}, fmt.Errorf("read finality before ancestry traversal: %w", finalityErr)
				}
				if exists {
					finalized = current.Finalized
				}
				finalityLoaded = true
			}
			if finalized != nil && cursorRef.Number <= finalized.Number {
				return ApplyResult{}, fmt.Errorf(
					"%w: alternate block %d reaches finalized block %d",
					ErrFinalizedReorg, cursorRef.Number, finalized.Number,
				)
			}
		}
		if sparseTop != nil && cursorRef.Number == sparseTop.Start && !resolvingKnownSparseHead {
			return c.replaceSparseHead(ctx, oldTip, candidateRef, *sparseTop, backward)
		}
		if cursorRef.Number == 0 || cursorRef.Number <= c.StartBlock {
			return ApplyResult{}, fmt.Errorf("%w above configured start block %d", ErrNoCommonAncestor, c.StartBlock)
		}
		if parentFetches >= maximumParentFetches {
			boundary := ErrReorgTooDeep
			if cursorRef.Number > tip.Number {
				boundary = ErrGap
			}
			return ApplyResult{}, fmt.Errorf(
				"%w: ancestry traversal reached %d parent bundles",
				boundary, maximumParentFetches,
			)
		}
		parent, exists, err := c.parentBundle(ctx, cursorRef.ParentHash, authoritativeHead)
		if err != nil {
			return ApplyResult{}, err
		}
		if !exists {
			return ApplyResult{}, fmt.Errorf("%w: parent %s of block %s is unavailable", ErrGap, cursorRef.ParentHash, cursorRef.Hash)
		}
		if err := chainbundle.Validate(parent); err != nil {
			return ApplyResult{}, fmt.Errorf("%w: invalid parent bundle: %v", ErrSourceInconsistent, err)
		}
		if err := chainbundle.ValidateParent(cursor, parent); err != nil {
			return ApplyResult{}, fmt.Errorf("%w: %v", ErrSourceInconsistent, err)
		}
		backward = append(backward, parent)
		cursor = parent
		parentFetches++
	}

	attachedBundles := reverseBundles(backward)
	attachedRefs := make([]store.BlockRef, len(attachedBundles))
	for index, bundle := range attachedBundles {
		attachedRefs[index], _ = store.RefFromBundle(bundle)
	}
	if sparseTop != nil && ancestor.Number < sparseTop.Start {
		return c.replaceSparseHeadThroughAncestor(
			ctx, oldTip, candidateRef, ancestor, *sparseTop, coverage, attachedBundles, attachedRefs,
		)
	}
	if ancestor.Hash == tip.Hash {
		if err := c.commitCanonicalBundles(ctx, attachedBundles, attachedRefs); err != nil {
			return ApplyResult{}, fmt.Errorf("commit canonical extension through %d: %w", candidateRef.Number, err)
		}
		return ApplyResult{
			Disposition: DispositionExtended,
			OldTip:      &oldTip,
			NewTip:      candidateRef,
			Ancestor:    &ancestor,
			Attached:    attachedRefs,
		}, nil
	}

	depth := tip.Number - ancestor.Number
	if err := c.validateReorgBoundary(ctx, ancestor, depth); err != nil {
		return ApplyResult{}, err
	}
	detached, err := c.detachedBranch(ctx, tip, ancestor)
	if err != nil {
		return ApplyResult{}, err
	}
	reorg := store.Reorg{
		Ancestor:   ancestor,
		Detached:   detached,
		Attached:   attachedBundles,
		Checkpoint: c.checkpoint(candidateRef),
		Reason:     "canonical head ancestry changed",
	}
	if err := c.Repository.ApplyReorg(ctx, c.ChainID, reorg); err != nil {
		return ApplyResult{}, fmt.Errorf("apply canonical reorg: %w", err)
	}
	return ApplyResult{
		Disposition: DispositionReorganized,
		OldTip:      &oldTip,
		NewTip:      candidateRef,
		Ancestor:    &ancestor,
		Detached:    detached,
		Attached:    attachedRefs,
	}, nil
}

func (c *Canonicalizer) replaceSparseHead(
	ctx context.Context,
	oldTip, candidate store.BlockRef,
	covered store.BlockRange,
	backward []chainbundle.Bundle,
) (ApplyResult, error) {
	if covered.End < covered.Start {
		return ApplyResult{}, fmt.Errorf("%w: sparse range ends before it starts", ErrSourceInconsistent)
	}
	distance := covered.End - covered.Start
	if distance >= c.maxReorgDepth() {
		return ApplyResult{}, fmt.Errorf(
			"%w: sparse depth exceeds %d",
			ErrReorgTooDeep, c.maxReorgDepth(),
		)
	}
	depth := distance + 1
	if err := c.validateSparseRangeBoundary(ctx, covered, depth); err != nil {
		return ApplyResult{}, err
	}
	detached := make([]store.BlockRef, 0, depth)
	for number := covered.End; ; number-- {
		reference, exists, err := c.Repository.CanonicalBlock(ctx, c.ChainID, number)
		if err != nil {
			return ApplyResult{}, fmt.Errorf("read sparse canonical block %d: %w", number, err)
		}
		if !exists {
			return ApplyResult{}, fmt.Errorf("%w: sparse canonical range is missing height %d", ErrGap, number)
		}
		detached = append(detached, reference)
		if number == covered.Start {
			break
		}
	}
	attachedBundles := reverseBundles(backward)
	attached := make([]store.BlockRef, len(attachedBundles))
	for index := range attachedBundles {
		attached[index], _ = store.RefFromBundle(attachedBundles[index])
	}
	replacement := store.SparseCanonicalReplacement{
		Range: covered, Detached: detached, Attached: attachedBundles,
		Reason: "authoritative live head replaced an isolated canonical range",
	}
	if _, err := c.Repository.ReplaceHighestCanonicalSegment(ctx, c.ChainID, replacement); err != nil {
		return ApplyResult{}, fmt.Errorf("replace sparse canonical live range: %w", err)
	}
	return ApplyResult{
		Disposition: DispositionReorganized,
		OldTip:      &oldTip, NewTip: candidate,
		Detached: detached, Attached: attached,
	}, nil
}

func (c *Canonicalizer) replaceSparseHeadThroughAncestor(
	ctx context.Context,
	oldTip, candidate, ancestor store.BlockRef,
	covered store.BlockRange,
	coverage store.CoreCoverage,
	attachedBundles []chainbundle.Bundle,
	attached []store.BlockRef,
) (ApplyResult, error) {
	depth, err := coveredDepthAbove(oldTip, ancestor, coverage, c.maxReorgDepth())
	if err != nil {
		return ApplyResult{}, err
	}
	if err := c.validateReorgBoundary(ctx, ancestor, depth); err != nil {
		return ApplyResult{}, err
	}
	detached, err := c.detachedCoveredAbove(ctx, oldTip, ancestor, coverage, depth)
	if err != nil {
		return ApplyResult{}, err
	}
	ancestorCopy := ancestor
	replacement := store.SparseCanonicalReplacement{
		Range: covered, Ancestor: &ancestorCopy,
		Detached: detached, Attached: attachedBundles,
		Reason: "authoritative live ancestry repaired canonical coverage across a gap",
	}
	if _, err := c.Repository.ReplaceHighestCanonicalSegment(ctx, c.ChainID, replacement); err != nil {
		return ApplyResult{}, fmt.Errorf("replace sparse canonical coverage through ancestor: %w", err)
	}
	return ApplyResult{
		Disposition: DispositionReorganized,
		OldTip:      &oldTip, NewTip: candidate, Ancestor: &ancestorCopy,
		Detached: detached, Attached: attached,
	}, nil
}

func (c *Canonicalizer) truncateSparseHead(
	ctx context.Context,
	oldTip, newTip store.BlockRef,
	coverage store.CoreCoverage,
	covered store.BlockRange,
) (ApplyResult, error) {
	depth, err := coveredDepthAbove(oldTip, newTip, coverage, c.maxReorgDepth())
	if err != nil {
		return ApplyResult{}, err
	}
	if err := c.validateReorgBoundary(ctx, newTip, depth); err != nil {
		return ApplyResult{}, err
	}
	detached, err := c.detachedCoveredAbove(ctx, oldTip, newTip, coverage, depth)
	if err != nil {
		return ApplyResult{}, err
	}
	ancestor := newTip
	replacement := store.SparseCanonicalReplacement{
		Range: covered, Ancestor: &ancestor, Detached: detached,
		Reason: "authoritative head moved below disconnected live coverage",
	}
	if _, err := c.Repository.ReplaceHighestCanonicalSegment(ctx, c.ChainID, replacement); err != nil {
		return ApplyResult{}, fmt.Errorf("truncate disconnected live coverage: %w", err)
	}
	return ApplyResult{
		Disposition: DispositionReorganized,
		OldTip:      &oldTip, NewTip: newTip, Ancestor: &ancestor, Detached: detached,
	}, nil
}

func (c *Canonicalizer) shouldResolveSparseHead(
	coverage store.CoreCoverage,
	candidate, tip store.BlockRef,
) bool {
	if len(coverage.Ranges) < 2 || candidate.Number != tip.Number {
		return false
	}
	top := coverage.Ranges[len(coverage.Ranges)-1]
	previous := coverage.Ranges[len(coverage.Ranges)-2]
	if top.End != tip.Number || top.Start <= previous.End || previous.End == ^uint64(0) {
		return false
	}
	gap := top.Start - previous.End - 1
	return gap <= c.maxReorgDepth()
}

func (c *Canonicalizer) maxReorgDepth() uint64 {
	if c.MaxReorgDepth == 0 {
		return 128
	}
	return c.MaxReorgDepth
}

func (c *Canonicalizer) maximumAncestryFetches(forwardGap uint64) uint64 {
	maximum := c.maxReorgDepth()
	if ^uint64(0)-maximum < forwardGap {
		return ^uint64(0)
	}
	return maximum + forwardGap
}

func (c *Canonicalizer) validateSparseRangeBoundary(
	ctx context.Context,
	covered store.BlockRange,
	depth uint64,
) error {
	if depth > c.maxReorgDepth() {
		return fmt.Errorf(
			"%w: sparse depth %d exceeds %d",
			ErrReorgTooDeep, depth, c.maxReorgDepth(),
		)
	}
	finality, exists, err := c.Repository.Finality(ctx, c.ChainID)
	if err != nil {
		return fmt.Errorf("read finality for sparse reorg: %w", err)
	}
	if exists && finality.Finalized != nil && covered.Start <= finality.Finalized.Number {
		return fmt.Errorf(
			"%w: sparse range starts at %d at or below finalized block %d",
			ErrFinalizedReorg, covered.Start, finality.Finalized.Number,
		)
	}
	return nil
}

func coveredDepthAbove(
	tip, ancestor store.BlockRef,
	coverage store.CoreCoverage,
	limit uint64,
) (uint64, error) {
	if ancestor.Number > tip.Number {
		return 0, fmt.Errorf("%w: sparse ancestor is above the canonical tip", ErrSourceInconsistent)
	}
	total := uint64(0)
	for _, covered := range coverage.Ranges {
		end := min(covered.End, tip.Number)
		if end <= ancestor.Number {
			continue
		}
		start := covered.Start
		if start <= ancestor.Number {
			start = ancestor.Number + 1
		}
		if end < start {
			continue
		}
		span := end - start + 1
		if total > limit || span > limit-total {
			return 0, fmt.Errorf("%w: covered depth exceeds %d", ErrReorgTooDeep, limit)
		}
		total += span
	}
	return total, nil
}

func (c *Canonicalizer) detachedCoveredAbove(
	ctx context.Context,
	tip, ancestor store.BlockRef,
	coverage store.CoreCoverage,
	expectedDepth uint64,
) ([]store.BlockRef, error) {
	detached := make([]store.BlockRef, 0, expectedDepth)
	for rangeIndex := len(coverage.Ranges); rangeIndex > 0; rangeIndex-- {
		covered := coverage.Ranges[rangeIndex-1]
		end := min(covered.End, tip.Number)
		if end <= ancestor.Number {
			continue
		}
		start := covered.Start
		if start <= ancestor.Number {
			start = ancestor.Number + 1
		}
		for number := end; ; number-- {
			reference, exists, err := c.Repository.CanonicalBlock(ctx, c.ChainID, number)
			if err != nil {
				return nil, fmt.Errorf("read covered canonical block %d for sparse reorg: %w", number, err)
			}
			if !exists {
				return nil, fmt.Errorf("%w: sparse coverage is missing canonical height %d", ErrGap, number)
			}
			if len(detached) > 0 && detached[len(detached)-1].Number == reference.Number+1 &&
				detached[len(detached)-1].ParentHash != reference.Hash {
				return nil, fmt.Errorf("%w: canonical ancestry breaks at height %d", ErrSourceInconsistent, detached[len(detached)-1].Number)
			}
			detached = append(detached, reference)
			if number == start {
				break
			}
		}
	}
	if len(detached) == 0 || detached[0].Number != tip.Number || detached[0].Hash != tip.Hash {
		return nil, fmt.Errorf("%w: highest covered canonical range does not match the tip", ErrGap)
	}
	if uint64(len(detached)) != expectedDepth {
		return nil, fmt.Errorf("%w: covered detach depth changed during traversal", ErrSourceInconsistent)
	}
	return detached, nil
}

func (c *Canonicalizer) commitCanonicalBundles(
	ctx context.Context,
	bundles []chainbundle.Bundle,
	references []store.BlockRef,
) error {
	if len(bundles) == 0 {
		return nil
	}
	if _, configured, err := c.Repository.Coverage(ctx, c.ChainID); err != nil {
		return fmt.Errorf("read canonical coverage before commit: %w", err)
	} else if configured {
		_, err := c.Repository.CommitCanonicalSegment(ctx, c.ChainID, bundles)
		return err
	}
	for index, bundle := range bundles {
		if err := c.Repository.CommitCanonical(ctx, c.ChainID, bundle, c.checkpoint(references[index])); err != nil {
			return err
		}
	}
	return nil
}

func (c *Canonicalizer) truncateHead(ctx context.Context, oldTip, newTip store.BlockRef) (ApplyResult, error) {
	if newTip.Number > oldTip.Number {
		return ApplyResult{}, fmt.Errorf("%w: truncated tip is above the current tip", ErrSourceInconsistent)
	}
	depth := oldTip.Number - newTip.Number
	if err := c.validateReorgBoundary(ctx, newTip, depth); err != nil {
		return ApplyResult{}, err
	}
	detached, err := c.detachedBranch(ctx, oldTip, newTip)
	if err != nil {
		return ApplyResult{}, err
	}
	reorg := store.Reorg{
		Ancestor:   newTip,
		Detached:   detached,
		Checkpoint: c.checkpoint(newTip),
		Reason:     "authoritative head moved to an earlier canonical block",
	}
	if err := c.Repository.ApplyReorg(ctx, c.ChainID, reorg); err != nil {
		return ApplyResult{}, fmt.Errorf("apply canonical head truncation: %w", err)
	}
	return ApplyResult{
		Disposition: DispositionReorganized,
		OldTip:      &oldTip,
		NewTip:      newTip,
		Ancestor:    &newTip,
		Detached:    detached,
	}, nil
}

func (c *Canonicalizer) validateReorgBoundary(ctx context.Context, ancestor store.BlockRef, depth uint64) error {
	maxDepth := c.maxReorgDepth()
	if depth > maxDepth {
		return fmt.Errorf("%w: depth %d exceeds %d", ErrReorgTooDeep, depth, maxDepth)
	}
	finality, hasFinality, err := c.Repository.Finality(ctx, c.ChainID)
	if err != nil {
		return fmt.Errorf("read finality: %w", err)
	}
	if hasFinality && finality.Finalized != nil && ancestor.Number < finality.Finalized.Number {
		return fmt.Errorf("%w: ancestor %d is below finalized block %d", ErrFinalizedReorg, ancestor.Number, finality.Finalized.Number)
	}
	return nil
}

func (c *Canonicalizer) UpdateFinality(ctx context.Context, safe, finalized *store.BlockRef) error {
	if c == nil || c.Repository == nil {
		return errors.New("canonicalizer repository is nil")
	}
	tip, exists, err := c.Repository.CanonicalTip(ctx, c.ChainID)
	if err != nil {
		return fmt.Errorf("read canonical tip: %w", err)
	}
	if !exists {
		return errors.New("cannot update finality before canonical indexing starts")
	}
	resolved := store.Finality{UpdatedAt: c.now()}
	var ancestryFloor *store.BlockRef
	for name, requested := range map[string]*store.BlockRef{"safe": safe, "finalized": finalized} {
		if requested == nil {
			continue
		}
		if requested.Number > tip.Number {
			return fmt.Errorf("%s height %d exceeds canonical tip %d", name, requested.Number, tip.Number)
		}
		canonical, exists, err := c.Repository.CanonicalBlock(ctx, c.ChainID, requested.Number)
		if err != nil {
			return fmt.Errorf("resolve %s block: %w", name, err)
		}
		if !exists || canonical.Hash != requested.Hash {
			return fmt.Errorf("%w: %s block is not canonical", store.ErrConflict, name)
		}
		switch name {
		case "safe":
			resolved.Safe = &canonical
		case "finalized":
			resolved.Finalized = &canonical
		}
		if ancestryFloor == nil || canonical.Number < ancestryFloor.Number {
			candidate := canonical
			ancestryFloor = &candidate
		}
	}
	if ancestryFloor != nil {
		if err := c.validateCanonicalAncestry(ctx, tip, *ancestryFloor); err != nil {
			return err
		}
	}
	return c.Repository.UpdateFinality(ctx, c.ChainID, resolved)
}

func (c *Canonicalizer) validateCanonicalAncestry(ctx context.Context, tip, floor store.BlockRef) error {
	child := tip
	for child.Number > floor.Number {
		parent, exists, err := c.Repository.CanonicalBlock(ctx, c.ChainID, child.Number-1)
		if err != nil {
			return fmt.Errorf("read canonical finality ancestor %d: %w", child.Number-1, err)
		}
		if !exists {
			return fmt.Errorf("%w: canonical finality ancestry has a gap at height %d", ErrGap, child.Number-1)
		}
		if child.ParentHash != parent.Hash {
			return fmt.Errorf(
				"%w: canonical block %d parent %s does not match block %d hash %s",
				ErrSourceInconsistent, child.Number, child.ParentHash, parent.Number, parent.Hash,
			)
		}
		child = parent
	}
	if child.Hash != floor.Hash {
		return fmt.Errorf("%w: finality floor is not an ancestor of the canonical tip", store.ErrConflict)
	}
	return nil
}

func (c *Canonicalizer) parentBundle(
	ctx context.Context,
	hash common.Hash,
	authoritativeHead bool,
) (chainbundle.Bundle, bool, error) {
	if bundle, exists, err := c.Repository.BundleByHash(ctx, c.ChainID, hash); err != nil {
		return chainbundle.Bundle{}, false, fmt.Errorf("read parent bundle from store: %w", err)
	} else if exists {
		return bundle, true, nil
	}
	source := c.Source
	if authoritativeHead && c.HeadSource != nil {
		source = c.HeadSource
	}
	if source == nil {
		return chainbundle.Bundle{}, false, nil
	}
	bundle, exists, err := source.BundleByHash(ctx, hash)
	if err != nil {
		return chainbundle.Bundle{}, false, fmt.Errorf("fetch parent bundle %s: %w", hash, err)
	}
	return bundle, exists, nil
}

func (c *Canonicalizer) detachedBranch(ctx context.Context, tip, ancestor store.BlockRef) ([]store.BlockRef, error) {
	if ancestor.Number > tip.Number {
		return nil, fmt.Errorf("%w: ancestor is above the canonical tip", ErrSourceInconsistent)
	}
	depth := tip.Number - ancestor.Number
	if depth > c.maxReorgDepth() {
		return nil, fmt.Errorf("%w: depth %d exceeds %d", ErrReorgTooDeep, depth, c.maxReorgDepth())
	}
	detached := make([]store.BlockRef, 0, depth)
	for number := tip.Number; number > ancestor.Number; number-- {
		reference, exists, err := c.Repository.CanonicalBlock(ctx, c.ChainID, number)
		if err != nil {
			return nil, fmt.Errorf("read canonical block %d for reorg: %w", number, err)
		}
		if !exists {
			return nil, fmt.Errorf("%w: canonical gap at height %d", ErrGap, number)
		}
		detached = append(detached, reference)
	}
	for index := range detached {
		if index+1 < len(detached) && detached[index].ParentHash != detached[index+1].Hash {
			return nil, fmt.Errorf("%w: canonical ancestry breaks at height %d", ErrSourceInconsistent, detached[index].Number)
		}
	}
	return detached, nil
}

func (c *Canonicalizer) checkpoint(reference store.BlockRef) store.Checkpoint {
	return store.Checkpoint{
		Stage:             store.CoreCheckpoint,
		ContiguousThrough: reference.Number,
		BlockHash:         reference.Hash,
		UpdatedAt:         c.now(),
	}
}

func (c *Canonicalizer) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

func reverseBundles(backward []chainbundle.Bundle) []chainbundle.Bundle {
	forward := make([]chainbundle.Bundle, len(backward))
	for index := range backward {
		forward[len(backward)-1-index] = backward[index]
	}
	return forward
}
