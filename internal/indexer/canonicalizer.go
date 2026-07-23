package indexer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/islishude/etherview/internal/ethrpc"
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
	BundleByHash(ctx context.Context, hash ethrpc.Hash) (ethrpc.Bundle, bool, error)
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

func (c *Canonicalizer) Apply(ctx context.Context, candidate ethrpc.Bundle) (ApplyResult, error) {
	return c.apply(ctx, candidate, false)
}

// Refresh atomically rewrites core facts only when the candidate is already
// the canonical block at that height. It must never call Apply: doing so could
// turn an operator repair into a chain extension or reorg before the
// identity-bound store refresh is reached.
func (c *Canonicalizer) Refresh(
	ctx context.Context,
	candidate ethrpc.Bundle,
	options store.RefreshOptions,
) (ApplyResult, error) {
	if c == nil || c.Repository == nil {
		return ApplyResult{}, errors.New("canonicalizer repository is nil")
	}
	if c.ChainID == "" {
		return ApplyResult{}, errors.New("canonicalizer chain ID is empty")
	}
	if err := ethrpc.ValidateBundle(candidate); err != nil {
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
	if !canonical.Hash.Equal(candidateRef.Hash) {
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
func (c *Canonicalizer) ApplyHead(ctx context.Context, candidate ethrpc.Bundle) (ApplyResult, error) {
	return c.apply(ctx, candidate, true)
}

func (c *Canonicalizer) apply(ctx context.Context, candidate ethrpc.Bundle, authoritativeHead bool) (ApplyResult, error) {
	if c == nil || c.Repository == nil {
		return ApplyResult{}, errors.New("canonicalizer repository is nil")
	}
	if c.ChainID == "" {
		return ApplyResult{}, errors.New("canonicalizer chain ID is empty")
	}
	if err := ethrpc.ValidateBundle(candidate); err != nil {
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
		if err := c.commitCanonicalBundles(ctx, []ethrpc.Bundle{candidate}, []store.BlockRef{candidateRef}); err != nil {
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
	if canonicalExists && canonical.Hash.Equal(candidateRef.Hash) {
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
	resolvingKnownSparseHead := sparseTop != nil && canonicalExists && canonical.Hash.Equal(candidateRef.Hash)

	backward := []ethrpc.Bundle{candidate}
	cursor := candidate
	var ancestor store.BlockRef
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
			if exists && canonical.Hash.Equal(cursorRef.Hash) && !insideKnownSparseRange {
				ancestor = canonical
				backward = backward[:len(backward)-1]
				break
			}
		}
		if sparseTop != nil && cursorRef.Number == sparseTop.Start && !resolvingKnownSparseHead {
			return c.replaceSparseHead(ctx, oldTip, candidateRef, *sparseTop, backward)
		}
		if cursorRef.Number == 0 || cursorRef.Number <= c.StartBlock {
			return ApplyResult{}, fmt.Errorf("%w above configured start block %d", ErrNoCommonAncestor, c.StartBlock)
		}
		parent, exists, err := c.parentBundle(ctx, cursorRef.ParentHash, authoritativeHead)
		if err != nil {
			return ApplyResult{}, err
		}
		if !exists {
			return ApplyResult{}, fmt.Errorf("%w: parent %s of block %s is unavailable", ErrGap, cursorRef.ParentHash, cursorRef.Hash)
		}
		if err := ethrpc.ValidateBundle(parent); err != nil {
			return ApplyResult{}, fmt.Errorf("%w: invalid parent bundle: %v", ErrSourceInconsistent, err)
		}
		if err := ethrpc.ValidateParent(cursor, parent); err != nil {
			return ApplyResult{}, fmt.Errorf("%w: %v", ErrSourceInconsistent, err)
		}
		backward = append(backward, parent)
		cursor = parent
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
	if ancestor.Hash.Equal(tip.Hash) {
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

	detached, err := c.detachedBranch(ctx, tip, ancestor)
	if err != nil {
		return ApplyResult{}, err
	}
	if err := c.validateReorgBoundary(ctx, ancestor, detached); err != nil {
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
	backward []ethrpc.Bundle,
) (ApplyResult, error) {
	detached := make([]store.BlockRef, 0, covered.End-covered.Start+1)
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
	maxDepth := c.MaxReorgDepth
	if maxDepth == 0 {
		maxDepth = 128
	}
	if uint64(len(detached)) > maxDepth {
		return ApplyResult{}, fmt.Errorf("%w: sparse depth %d exceeds %d", ErrReorgTooDeep, len(detached), maxDepth)
	}
	finality, hasFinality, err := c.Repository.Finality(ctx, c.ChainID)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("read finality for sparse reorg: %w", err)
	}
	if hasFinality && finality.Finalized != nil && covered.Start <= finality.Finalized.Number {
		return ApplyResult{}, fmt.Errorf("%w: sparse range starts at %d at or below finalized block %d",
			ErrFinalizedReorg, covered.Start, finality.Finalized.Number)
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
	attachedBundles []ethrpc.Bundle,
	attached []store.BlockRef,
) (ApplyResult, error) {
	detached, err := c.detachedCoveredAbove(ctx, oldTip, ancestor, coverage)
	if err != nil {
		return ApplyResult{}, err
	}
	if err := c.validateReorgBoundary(ctx, ancestor, detached); err != nil {
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
	detached, err := c.detachedCoveredAbove(ctx, oldTip, newTip, coverage)
	if err != nil {
		return ApplyResult{}, err
	}
	if err := c.validateReorgBoundary(ctx, newTip, detached); err != nil {
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
	maxDepth := c.MaxReorgDepth
	if maxDepth == 0 {
		maxDepth = 128
	}
	return gap <= maxDepth
}

func (c *Canonicalizer) detachedCoveredAbove(
	ctx context.Context,
	tip, ancestor store.BlockRef,
	coverage store.CoreCoverage,
) ([]store.BlockRef, error) {
	detached := make([]store.BlockRef, 0)
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
				!detached[len(detached)-1].ParentHash.Equal(reference.Hash) {
				return nil, fmt.Errorf("%w: canonical ancestry breaks at height %d", ErrSourceInconsistent, detached[len(detached)-1].Number)
			}
			detached = append(detached, reference)
			if number == start {
				break
			}
		}
	}
	if len(detached) == 0 || detached[0].Number != tip.Number || !detached[0].Hash.Equal(tip.Hash) {
		return nil, fmt.Errorf("%w: highest covered canonical range does not match the tip", ErrGap)
	}
	return detached, nil
}

func (c *Canonicalizer) commitCanonicalBundles(
	ctx context.Context,
	bundles []ethrpc.Bundle,
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
	detached, err := c.detachedBranch(ctx, oldTip, newTip)
	if err != nil {
		return ApplyResult{}, err
	}
	if err := c.validateReorgBoundary(ctx, newTip, detached); err != nil {
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

func (c *Canonicalizer) validateReorgBoundary(ctx context.Context, ancestor store.BlockRef, detached []store.BlockRef) error {
	maxDepth := c.MaxReorgDepth
	if maxDepth == 0 {
		maxDepth = 128
	}
	if uint64(len(detached)) > maxDepth {
		return fmt.Errorf("%w: depth %d exceeds %d", ErrReorgTooDeep, len(detached), maxDepth)
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
		if !exists || !canonical.Hash.Equal(requested.Hash) {
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
		if !child.ParentHash.Equal(parent.Hash) {
			return fmt.Errorf(
				"%w: canonical block %d parent %s does not match block %d hash %s",
				ErrSourceInconsistent, child.Number, child.ParentHash, parent.Number, parent.Hash,
			)
		}
		child = parent
	}
	if !child.Hash.Equal(floor.Hash) {
		return fmt.Errorf("%w: finality floor is not an ancestor of the canonical tip", store.ErrConflict)
	}
	return nil
}

func (c *Canonicalizer) parentBundle(
	ctx context.Context,
	hash ethrpc.Hash,
	authoritativeHead bool,
) (ethrpc.Bundle, bool, error) {
	if bundle, exists, err := c.Repository.BundleByHash(ctx, c.ChainID, hash); err != nil {
		return ethrpc.Bundle{}, false, fmt.Errorf("read parent bundle from store: %w", err)
	} else if exists {
		return bundle, true, nil
	}
	source := c.Source
	if authoritativeHead && c.HeadSource != nil {
		source = c.HeadSource
	}
	if source == nil {
		return ethrpc.Bundle{}, false, nil
	}
	bundle, exists, err := source.BundleByHash(ctx, hash)
	if err != nil {
		return ethrpc.Bundle{}, false, fmt.Errorf("fetch parent bundle %s: %w", hash, err)
	}
	return bundle, exists, nil
}

func (c *Canonicalizer) detachedBranch(ctx context.Context, tip, ancestor store.BlockRef) ([]store.BlockRef, error) {
	detached := make([]store.BlockRef, 0, tip.Number-ancestor.Number)
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
		if index+1 < len(detached) && !detached[index].ParentHash.Equal(detached[index+1].Hash) {
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

func reverseBundles(backward []ethrpc.Bundle) []ethrpc.Bundle {
	forward := make([]ethrpc.Bundle, len(backward))
	for index := range backward {
		forward[len(backward)-1-index] = backward[index]
	}
	return forward
}
