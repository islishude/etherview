package indexer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/chainbundle/testfixture"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
)

func TestCanonicalizerInitializesAndExtendsAcrossHeadGap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryRepository()
	chain := indexerTestChain(t, 4)
	genesis, blockOne, blockTwo, blockThree :=
		chain[0], chain[1], chain[2], chain[3]
	blockThreeRef := mustIndexerTestRef(t, blockThree)
	source := newMapSource(blockOne, blockTwo)
	canonicalizer := testCanonicalizer(repository, source)
	result, err := canonicalizer.Apply(ctx, genesis)
	if err != nil || result.Disposition != DispositionInitialized {
		t.Fatalf("initial result = %+v, error = %v", result, err)
	}
	result, err = canonicalizer.Apply(ctx, blockThree)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != DispositionExtended || len(result.Attached) != 3 {
		t.Fatalf("extension result = %+v", result)
	}
	tip, exists, err := repository.CanonicalTip(ctx, "1")
	if err != nil || !exists || tip.Number != 3 ||
		tip.Hash != blockThreeRef.Hash {
		t.Fatalf("tip = %+v, exists = %v, error = %v", tip, exists, err)
	}
	checkpoint, exists, err := repository.Checkpoint(ctx, "1", store.CoreCheckpoint)
	if err != nil || !exists || checkpoint.ContiguousThrough != 3 {
		t.Fatalf("checkpoint = %+v, exists = %v, error = %v", checkpoint, exists, err)
	}
}

func TestCanonicalizerReorgRetainsOldBranch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryRepository()
	oldChain := indexerTestChain(t, 4)
	canonicalizer := testCanonicalizer(repository, nil)
	for _, bundle := range oldChain {
		if _, err := canonicalizer.Apply(ctx, bundle); err != nil {
			t.Fatal(err)
		}
	}
	ancestorRef := mustIndexerTestRef(t, oldChain[1])
	newTwo := indexerTestBundle(2, ancestorRef.Hash, 12)
	newTwoRef := mustIndexerTestRef(t, newTwo)
	newThree := indexerTestBundle(3, newTwoRef.Hash, 13)
	newThreeRef := mustIndexerTestRef(t, newThree)
	newFour := indexerTestBundle(4, newThreeRef.Hash, 14)
	canonicalizer.Source = newMapSource(newTwo, newThree)
	result, err := canonicalizer.Apply(ctx, newFour)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != DispositionReorganized || result.Ancestor.Number != 1 {
		t.Fatalf("reorg result = %+v", result)
	}
	if len(result.Detached) != 2 || len(result.Attached) != 3 {
		t.Fatalf("detached = %d, attached = %d", len(result.Detached), len(result.Attached))
	}
	canonicalTwo, exists, err := repository.CanonicalBlock(ctx, "1", 2)
	if err != nil || !exists || canonicalTwo.Hash != newTwoRef.Hash {
		t.Fatalf("canonical block 2 = %+v, exists = %v, error = %v", canonicalTwo, exists, err)
	}
	oldTwoRef := mustIndexerTestRef(t, oldChain[2])
	if _, exists, err := repository.BundleByHash(ctx, "1", oldTwoRef.Hash); err != nil || !exists {
		t.Fatalf("old block retained = %v, error = %v", exists, err)
	}
}

func TestCanonicalizerStopsReorgAcrossFinalized(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryRepository()
	chain := indexerTestChain(t, 4)
	canonicalizer := testCanonicalizer(repository, nil)
	for _, bundle := range chain {
		if _, err := canonicalizer.Apply(ctx, bundle); err != nil {
			t.Fatal(err)
		}
	}
	safe, _, _ := repository.CanonicalBlock(ctx, "1", 3)
	finalized, _, _ := repository.CanonicalBlock(ctx, "1", 2)
	if err := canonicalizer.UpdateFinality(ctx, &safe, &finalized); err != nil {
		t.Fatal(err)
	}
	ancestorRef := mustIndexerTestRef(t, chain[1])
	newTwo := indexerTestBundle(2, ancestorRef.Hash, 12)
	newTwoRef := mustIndexerTestRef(t, newTwo)
	newThree := indexerTestBundle(3, newTwoRef.Hash, 13)
	newThreeRef := mustIndexerTestRef(t, newThree)
	newFour := indexerTestBundle(4, newThreeRef.Hash, 14)
	canonicalizer.Source = newMapSource(newTwo, newThree)
	_, err := canonicalizer.Apply(ctx, newFour)
	if !errors.Is(err, ErrFinalizedReorg) {
		t.Fatalf("error = %v, want ErrFinalizedReorg", err)
	}
	tip, _, _ := repository.CanonicalTip(ctx, "1")
	if tip.Hash != mustIndexerTestRef(t, chain[3]).Hash {
		t.Fatalf("tip changed despite rejected reorg: %+v", tip)
	}
}

func TestCanonicalizerRejectsFinalityAcrossSparseCanonicalGap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryRepository()
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	genesis := indexerTestBundle(0, common.Hash{}, 1)
	island := indexerTestBundle(3, indexerTestHash(3), 4)
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{genesis}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{island}); err != nil {
		t.Fatal(err)
	}
	finalized, _, _ := repository.CanonicalBlock(ctx, "1", 0)
	safe, _, _ := repository.CanonicalBlock(ctx, "1", 3)
	canonicalizer := testCanonicalizer(repository, nil)
	if err := canonicalizer.UpdateFinality(ctx, &safe, &finalized); !errors.Is(err, ErrGap) {
		t.Fatalf("sparse finality error=%v", err)
	}
	if _, exists, err := repository.Finality(ctx, "1"); err != nil || exists {
		t.Fatalf("rejected sparse finality was persisted: exists=%v error=%v", exists, err)
	}
}

func TestCanonicalizerEnforcesReorgDepth(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryRepository()
	canonicalizer := testCanonicalizer(repository, nil)
	canonicalizer.MaxReorgDepth = 1
	chain := indexerTestChain(t, 3)
	for _, bundle := range chain {
		if _, err := canonicalizer.Apply(ctx, bundle); err != nil {
			t.Fatal(err)
		}
	}
	newOne := indexerTestBundle(
		1,
		mustIndexerTestRef(t, chain[0]).Hash,
		12,
	)
	newTwo := indexerTestBundle(
		2,
		mustIndexerTestRef(t, newOne).Hash,
		13,
	)
	source := newMapSource(newOne)
	canonicalizer.Source = source
	_, err := canonicalizer.Apply(ctx, newTwo)
	if !errors.Is(err, ErrReorgTooDeep) {
		t.Fatalf("error = %v, want ErrReorgTooDeep", err)
	}
	if source.CallCount() != 1 {
		t.Fatalf("parent source calls = %d, want 1", source.CallCount())
	}
}

func TestCanonicalizerBoundsRejectedAncestryBeforeExtraParentFetch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryRepository()
	canonicalizer := testCanonicalizer(repository, nil)
	canonicalizer.MaxReorgDepth = 2
	canonical := indexerTestChain(t, 5)
	for _, bundle := range canonical {
		if _, err := canonicalizer.Apply(ctx, bundle); err != nil {
			t.Fatal(err)
		}
	}
	newOne := indexerTestBundle(1, mustIndexerTestRef(t, canonical[0]).Hash, 21)
	newTwo := indexerTestBundle(2, mustIndexerTestRef(t, newOne).Hash, 22)
	newThree := indexerTestBundle(3, mustIndexerTestRef(t, newTwo).Hash, 23)
	newFour := indexerTestBundle(4, mustIndexerTestRef(t, newThree).Hash, 24)
	source := newMapSource(newOne, newTwo, newThree)
	canonicalizer.Source = source
	_, err := canonicalizer.Apply(ctx, newFour)
	if !errors.Is(err, ErrReorgTooDeep) {
		t.Fatalf("Apply() error = %v, want ErrReorgTooDeep", err)
	}
	if source.CallCount() != 2 {
		t.Fatalf("parent source calls = %d, want bounded 2", source.CallCount())
	}
}

func TestCanonicalizerRejectsOversizedForwardGapWithoutParentFetch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryRepository()
	canonicalizer := testCanonicalizer(repository, nil)
	canonicalizer.MaxReorgDepth = 2
	genesis := indexerTestBundle(0, common.Hash{}, 1)
	if _, err := canonicalizer.Apply(ctx, genesis); err != nil {
		t.Fatal(err)
	}
	one := indexerTestBundle(1, mustIndexerTestRef(t, genesis).Hash, 2)
	two := indexerTestBundle(2, mustIndexerTestRef(t, one).Hash, 3)
	three := indexerTestBundle(3, mustIndexerTestRef(t, two).Hash, 4)
	source := newMapSource(one, two)
	canonicalizer.Source = source
	_, err := canonicalizer.Apply(ctx, three)
	if !errors.Is(err, ErrGap) {
		t.Fatalf("Apply() error = %v, want ErrGap", err)
	}
	if source.CallCount() != 0 {
		t.Fatalf("parent source calls = %d, want 0", source.CallCount())
	}
}

func TestCanonicalizerRejectsOversizedDetachBeforeCanonicalTraversal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	counting := &countingCanonicalRepository{Repository: store.NewMemoryRepository()}
	canonicalizer := testCanonicalizer(counting, nil)
	canonicalizer.MaxReorgDepth = 2
	_, err := canonicalizer.truncateHead(
		ctx,
		store.BlockRef{Number: 1_000, Hash: indexerTestHash(1)},
		store.BlockRef{Number: 1, Hash: indexerTestHash(2)},
	)
	if !errors.Is(err, ErrReorgTooDeep) {
		t.Fatalf("truncateHead() error = %v, want ErrReorgTooDeep", err)
	}
	if counting.CallCount() != 0 {
		t.Fatalf("canonical row calls = %d, want 0", counting.CallCount())
	}
	_, err = canonicalizer.replaceSparseHead(
		ctx,
		store.BlockRef{Number: 1_000, Hash: indexerTestHash(3)},
		store.BlockRef{Number: 1_001, Hash: indexerTestHash(4)},
		store.BlockRange{Start: 10, End: 1_000},
		nil,
	)
	if !errors.Is(err, ErrReorgTooDeep) {
		t.Fatalf("replaceSparseHead() error = %v, want ErrReorgTooDeep", err)
	}
	if counting.CallCount() != 0 {
		t.Fatalf("canonical row calls after sparse rejection = %d, want 0", counting.CallCount())
	}
}

func TestCanonicalizerRejectsStaleAlternateHead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryRepository()
	canonicalizer := testCanonicalizer(repository, nil)
	chain := indexerTestChain(t, 3)
	for _, bundle := range chain {
		if _, err := canonicalizer.Apply(ctx, bundle); err != nil {
			t.Fatal(err)
		}
	}
	_, err := canonicalizer.Apply(
		ctx,
		indexerTestBundle(
			1,
			mustIndexerTestRef(t, chain[0]).Hash,
			12,
		),
	)
	if !errors.Is(err, ErrStaleHead) {
		t.Fatalf("error = %v, want ErrStaleHead", err)
	}
}

func TestCanonicalizerAllowsAuthoritativeHeadToMoveBackward(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryRepository()
	canonicalizer := testCanonicalizer(repository, nil)
	chain := indexerTestChain(t, 3)
	for _, bundle := range chain {
		if _, err := canonicalizer.Apply(ctx, bundle); err != nil {
			t.Fatal(err)
		}
	}
	result, err := canonicalizer.ApplyHead(ctx, chain[1])
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != DispositionReorganized || result.NewTip.Number != 1 || len(result.Detached) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if _, exists, err := repository.CanonicalBlock(ctx, "1", 2); err != nil || exists {
		t.Fatalf("height 2 remains canonical: exists=%v err=%v", exists, err)
	}
}

func TestCanonicalizerRepairsKnownSparseHeadAcrossShallowLowerFork(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryRepository()
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	oldChain := indexerTestChain(t, 101)
	if _, err := repository.CommitCanonicalSegment(ctx, "1", oldChain); err != nil {
		t.Fatal(err)
	}
	ancestor, _ := store.RefFromBundle(oldChain[98])
	newNinetyNine := indexerTestBundle(99, ancestor.Hash, 209)
	newHundred := indexerTestBundle(
		100,
		mustIndexerTestRef(t, newNinetyNine).Hash,
		210,
	)
	newHundredOne := indexerTestBundle(
		101,
		mustIndexerTestRef(t, newHundred).Hash,
		211,
	)
	newHundredTwo := indexerTestBundle(
		102,
		mustIndexerTestRef(t, newHundredOne).Hash,
		212,
	)
	newHundredTwoRef := mustIndexerTestRef(t, newHundredTwo)
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{newHundredTwo}); err != nil {
		t.Fatal(err)
	}
	source := newMapSource(newNinetyNine, newHundred, newHundredOne)
	canonicalizer := testCanonicalizer(repository, nil)
	canonicalizer.HeadSource = source
	result, err := canonicalizer.ApplyHead(ctx, newHundredTwo)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != DispositionReorganized || result.Ancestor == nil ||
		result.Ancestor.Number != 98 || len(result.Detached) != 3 || len(result.Attached) != 4 {
		t.Fatalf("result=%+v", result)
	}
	coverage, exists, err := repository.Coverage(ctx, "1")
	if err != nil || !exists || len(coverage.Ranges) != 1 || coverage.Ranges[0] != (store.BlockRange{Start: 0, End: 102}) ||
		coverage.Contiguous == nil ||
		coverage.Contiguous.Hash != newHundredTwoRef.Hash {
		t.Fatalf("coverage=%+v exists=%v error=%v", coverage, exists, err)
	}
}

func TestCanonicalizerSparseHeadRollbackDropsIslandWithoutAdvancingCheckpoint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryRepository()
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	chain := indexerTestChain(t, 101)
	if _, err := repository.CommitCanonicalSegment(ctx, "1", chain); err != nil {
		t.Fatal(err)
	}
	island := indexerTestBundle(102, indexerTestHash(211), 212)
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{island}); err != nil {
		t.Fatal(err)
	}
	canonicalizer := testCanonicalizer(repository, nil)
	result, err := canonicalizer.ApplyHead(ctx, chain[100])
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != DispositionReorganized || result.NewTip.Number != 100 || len(result.Detached) != 1 {
		t.Fatalf("result=%+v", result)
	}
	coverage, exists, err := repository.Coverage(ctx, "1")
	if err != nil || !exists || len(coverage.Ranges) != 1 || coverage.Ranges[0].End != 100 ||
		coverage.Contiguous == nil || coverage.Contiguous.Number != 100 || coverage.Highest == nil || coverage.Highest.Number != 100 {
		t.Fatalf("coverage=%+v exists=%v error=%v", coverage, exists, err)
	}
	checkpoint, exists, err := repository.Checkpoint(ctx, "1", store.CoreCheckpoint)
	if err != nil || !exists || checkpoint.ContiguousThrough != 100 {
		t.Fatalf("checkpoint=%+v exists=%v error=%v", checkpoint, exists, err)
	}
}

func TestCanonicalizerSparseReplacementAllowsAuthoritativeHeadAboveOldIsland(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryRepository()
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	oldHundred := indexerTestBundle(100, indexerTestHash(99), 100)
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{oldHundred}); err != nil {
		t.Fatal(err)
	}
	newHundred := indexerTestBundle(100, indexerTestHash(208), 210)
	newHundredOne := indexerTestBundle(
		101,
		mustIndexerTestRef(t, newHundred).Hash,
		211,
	)
	newHundredTwo := indexerTestBundle(
		102,
		mustIndexerTestRef(t, newHundredOne).Hash,
		212,
	)
	canonicalizer := testCanonicalizer(repository, nil)
	canonicalizer.HeadSource = newMapSource(newHundred, newHundredOne)
	result, err := canonicalizer.ApplyHead(ctx, newHundredTwo)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != DispositionReorganized || result.NewTip.Number != 102 ||
		len(result.Detached) != 1 || len(result.Attached) != 3 {
		t.Fatalf("result=%+v", result)
	}
	coverage, _, _ := repository.Coverage(ctx, "1")
	if len(coverage.Ranges) != 1 || coverage.Ranges[0] != (store.BlockRange{Start: 100, End: 102}) ||
		coverage.Contiguous != nil || coverage.Highest == nil || coverage.Highest.Number != 102 {
		t.Fatalf("coverage=%+v", coverage)
	}
}

func TestCanonicalizerRejectsInconsistentSourceParent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryRepository()
	canonicalizer := testCanonicalizer(repository, nil)
	genesis := indexerTestBundle(0, common.Hash{}, 1)
	if _, err := canonicalizer.Apply(ctx, genesis); err != nil {
		t.Fatal(err)
	}
	wrongParent := indexerTestBundle(7, indexerTestHash(99), 2)
	blockTwo := indexerTestBundle(
		2,
		mustIndexerTestRef(t, wrongParent).Hash,
		3,
	)
	canonicalizer.Source = newMapSource(wrongParent)
	_, err := canonicalizer.Apply(ctx, blockTwo)
	if !errors.Is(err, ErrSourceInconsistent) {
		t.Fatalf("error = %v, want ErrSourceInconsistent", err)
	}
}

func TestCanonicalizerAlreadyKnownHistoricalBlockIsNoop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryRepository()
	canonicalizer := testCanonicalizer(repository, nil)
	chain := indexerTestChain(t, 2)
	genesis, blockOne := chain[0], chain[1]
	for _, bundle := range []chainbundle.Bundle{genesis, blockOne} {
		if _, err := canonicalizer.Apply(ctx, bundle); err != nil {
			t.Fatal(err)
		}
	}
	result, err := canonicalizer.Apply(ctx, genesis)
	if err != nil || result.Disposition != DispositionAlreadyKnown || result.NewTip.Number != 1 {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
}

func TestCanonicalizerRefreshRewritesKnownFactsWithoutMovingCanonicalState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryRepository()
	canonicalizer := testCanonicalizer(repository, nil)
	chain := indexerTestChain(t, 3)
	for _, bundle := range chain {
		if _, err := canonicalizer.Apply(ctx, bundle); err != nil {
			t.Fatal(err)
		}
	}
	checkpointBefore, _, _ := repository.Checkpoint(ctx, "1", store.CoreCheckpoint)
	refreshed := indexerTestBundle(
		1,
		mustIndexerTestRef(t, chain[0]).Hash,
		2,
	)
	result, err := canonicalizer.Refresh(ctx, refreshed, store.RefreshOptions{})
	if err != nil || result.Disposition != DispositionAlreadyKnown {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	blockOneRef := mustIndexerTestRef(t, chain[1])
	stored, exists, err := repository.BundleByHash(
		ctx,
		"1",
		blockOneRef.Hash,
	)
	if err != nil || !exists ||
		!bytes.Equal(stored.Block.Extra(), []byte{2}) {
		t.Fatalf(
			"stored extra=%x exists=%v error=%v",
			stored.Block.Extra(),
			exists,
			err,
		)
	}
	tip, exists, err := repository.CanonicalTip(ctx, "1")
	if err != nil || !exists || tip.Number != 2 ||
		tip.Hash != mustIndexerTestRef(t, chain[2]).Hash {
		t.Fatalf("tip=%+v exists=%v error=%v", tip, exists, err)
	}
	checkpointAfter, _, _ := repository.Checkpoint(ctx, "1", store.CoreCheckpoint)
	if checkpointAfter != checkpointBefore {
		t.Fatalf("checkpoint moved: before=%+v after=%+v", checkpointBefore, checkpointAfter)
	}
}

func TestCanonicalizerRefreshOverrideCannotBypassReorgBoundary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryRepository()
	canonicalizer := testCanonicalizer(repository, nil)
	chain := indexerTestChain(t, 3)
	for _, bundle := range chain {
		if _, err := canonicalizer.Apply(ctx, bundle); err != nil {
			t.Fatal(err)
		}
	}
	finalized, _, _ := repository.CanonicalBlock(ctx, "1", 1)
	if err := canonicalizer.UpdateFinality(ctx, &finalized, &finalized); err != nil {
		t.Fatal(err)
	}
	refreshed := indexerTestBundle(
		1,
		mustIndexerTestRef(t, chain[0]).Hash,
		2,
	)
	if _, err := canonicalizer.Refresh(ctx, refreshed, store.RefreshOptions{}); !errors.Is(err, store.ErrFinalizedRefresh) {
		t.Fatalf("finalized refresh error=%v", err)
	}
	if _, err := canonicalizer.Refresh(ctx, refreshed, store.RefreshOptions{AllowFinalized: true}); err != nil {
		t.Fatal(err)
	}
	alternate := indexerTestBundle(
		1,
		mustIndexerTestRef(t, chain[0]).Hash,
		12,
	)
	if _, err := canonicalizer.Refresh(ctx, alternate, store.RefreshOptions{AllowFinalized: true}); !errors.Is(err, ErrStaleHead) {
		t.Fatalf("alternate historical refresh error=%v", err)
	}
	tip, _, _ := repository.CanonicalTip(ctx, "1")
	if tip.Hash != mustIndexerTestRef(t, chain[2]).Hash {
		t.Fatalf("refresh override changed canonical tip: %+v", tip)
	}
}

func TestCanonicalizerRefreshCannotExtendCanonicalChain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryRepository()
	canonicalizer := testCanonicalizer(repository, nil)
	chain := indexerTestChain(t, 2)
	for _, bundle := range chain {
		if _, err := canonicalizer.Apply(ctx, bundle); err != nil {
			t.Fatal(err)
		}
	}
	candidate := indexerTestBundle(
		2,
		mustIndexerTestRef(t, chain[1]).Hash,
		3,
	)
	candidateRef := mustIndexerTestRef(t, candidate)
	if _, err := canonicalizer.Refresh(ctx, candidate, store.RefreshOptions{}); !errors.Is(err, ErrGap) {
		t.Fatalf("tip extension refresh error=%v", err)
	}
	tip, exists, err := repository.CanonicalTip(ctx, "1")
	if err != nil || !exists || tip.Number != 1 ||
		tip.Hash != mustIndexerTestRef(t, chain[1]).Hash {
		t.Fatalf("tip changed after rejected refresh: tip=%+v exists=%v error=%v", tip, exists, err)
	}
	if _, exists, err := repository.CanonicalBlock(ctx, "1", 2); err != nil || exists {
		t.Fatalf("refresh created canonical block 2: exists=%v error=%v", exists, err)
	}
	if _, exists, err := repository.BundleByHash(ctx, "1", candidateRef.Hash); err != nil || exists {
		t.Fatalf("refresh persisted noncanonical candidate: exists=%v error=%v", exists, err)
	}
}

func TestCanonicalizerRefreshCannotReplaceCanonicalIdentityAtTip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryRepository()
	canonicalizer := testCanonicalizer(repository, nil)
	chain := indexerTestChain(t, 2)
	for _, bundle := range chain {
		if _, err := canonicalizer.Apply(ctx, bundle); err != nil {
			t.Fatal(err)
		}
	}
	alternate := indexerTestBundle(
		1,
		mustIndexerTestRef(t, chain[0]).Hash,
		12,
	)
	alternateRef := mustIndexerTestRef(t, alternate)
	if _, err := canonicalizer.Refresh(ctx, alternate, store.RefreshOptions{AllowFinalized: true}); !errors.Is(err, ErrStaleHead) {
		t.Fatalf("alternate tip refresh error=%v", err)
	}
	tip, exists, err := repository.CanonicalTip(ctx, "1")
	if err != nil || !exists ||
		tip.Hash != mustIndexerTestRef(t, chain[1]).Hash {
		t.Fatalf("tip changed after rejected identity replacement: tip=%+v exists=%v error=%v", tip, exists, err)
	}
	if _, exists, err := repository.BundleByHash(ctx, "1", alternateRef.Hash); err != nil || exists {
		t.Fatalf("refresh persisted alternate identity: exists=%v error=%v", exists, err)
	}
}

func TestIngestorUsesOnePurposeEndpointForBlockAndReceipts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	bundle := indexerTestBundle(0, common.Hash{}, 1)
	first := newIndexerFakeRPC(t, bundle)
	second := newIndexerFakeRPC(t, bundle)
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{
		{Name: "history-a", Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeHistory: true}, Client: first.client,
			Capabilities: ethrpc.CapabilityReport{Methods: map[string]ethrpc.Availability{ethrpc.CapabilityBlockReceipts: ethrpc.AvailabilityAvailable}}},
		{Name: "history-b", Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeHistory: true}, Client: second.client,
			Capabilities: ethrpc.CapabilityReport{Methods: map[string]ethrpc.Availability{ethrpc.CapabilityBlockReceipts: ethrpc.AvailabilityAvailable}}},
	}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	repository := store.NewMemoryRepository()
	ingestor := &Ingestor{Pool: pool, Canonicalizer: testCanonicalizer(repository, nil)}
	if _, err := ingestor.ByNumber(ctx, ethrpc.PurposeHistory, 0); err != nil {
		t.Fatal(err)
	}
	if got := first.Methods(); len(got) != 2 || got[0] != "eth_getBlockByNumber" || got[1] != "eth_getBlockReceipts" {
		t.Fatalf("first endpoint methods = %v", got)
	}
	if got := second.Methods(); len(got) != 0 {
		t.Fatalf("second endpoint unexpectedly used: %v", got)
	}
}

type mapSource struct {
	mu      sync.Mutex
	bundles map[common.Hash]chainbundle.Bundle
	calls   int
}

func newMapSource(bundles ...chainbundle.Bundle) *mapSource {
	source := &mapSource{
		bundles: make(map[common.Hash]chainbundle.Bundle, len(bundles)),
	}
	for _, bundle := range bundles {
		hash, _ := bundle.BlockHash()
		source.bundles[hash] = bundle
	}
	return source
}

func (s *mapSource) BundleByHash(
	_ context.Context,
	hash common.Hash,
) (chainbundle.Bundle, bool, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	bundle, exists := s.bundles[hash]
	return bundle, exists, nil
}

func (s *mapSource) CallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type countingCanonicalRepository struct {
	store.Repository
	mu    sync.Mutex
	calls int
}

func (repository *countingCanonicalRepository) CanonicalBlock(
	ctx context.Context,
	chainID string,
	number uint64,
) (store.BlockRef, bool, error) {
	repository.mu.Lock()
	repository.calls++
	repository.mu.Unlock()
	return repository.Repository.CanonicalBlock(ctx, chainID, number)
}

func (repository *countingCanonicalRepository) CallCount() int {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.calls
}

type indexerFakeRPC struct {
	mu      sync.Mutex
	bundle  chainbundle.Bundle
	methods []string
	server  *rpc.Server
	client  *rpc.Client
}

func newIndexerFakeRPC(
	t *testing.T,
	bundle chainbundle.Bundle,
) *indexerFakeRPC {
	t.Helper()
	service := &indexerFakeRPC{bundle: bundle, server: rpc.NewServer()}
	if err := service.server.RegisterName("eth", service); err != nil {
		t.Fatal(err)
	}
	service.client = rpc.DialInProc(service.server)
	t.Cleanup(func() {
		service.client.Close()
		service.server.Stop()
	})
	return service
}

func (f *indexerFakeRPC) GetBlockByNumber(
	context.Context,
	string,
	bool,
) (json.RawMessage, error) {
	f.record("eth_getBlockByNumber")
	return append(json.RawMessage(nil), f.bundle.RawBlock...), nil
}

func (f *indexerFakeRPC) GetBlockReceipts(
	context.Context,
	common.Hash,
) (json.RawMessage, error) {
	f.record("eth_getBlockReceipts")
	return json.Marshal(f.bundle.RawReceipts)
}

func (f *indexerFakeRPC) record(method string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.methods = append(f.methods, method)
}

func (f *indexerFakeRPC) Methods() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.methods...)
}

func testCanonicalizer(repository store.Repository, source BundleSource) *Canonicalizer {
	return &Canonicalizer{
		ChainID:       "1",
		StartBlock:    0,
		MaxReorgDepth: 128,
		Repository:    repository,
		Source:        source,
		Now:           func() time.Time { return time.Unix(1_700_000_000, 0) },
	}
}

func indexerTestBundle(
	number uint64,
	parent common.Hash,
	extraData byte,
) chainbundle.Bundle {
	bundle, err := testfixture.New(testfixture.Options{
		Number:     number,
		ParentHash: parent,
		ExtraData:  []byte{extraData},
	})
	if err != nil {
		panic(err)
	}
	return bundle
}

func indexerTestChain(
	t *testing.T,
	length int,
) []chainbundle.Bundle {
	t.Helper()
	chain := make([]chainbundle.Bundle, length)
	var parent common.Hash
	for index := range length {
		chain[index] = indexerTestBundle(
			uint64(index),
			parent,
			1+byte(index),
		)
		parent = mustIndexerTestRef(t, chain[index]).Hash
	}
	return chain
}

func mustIndexerTestRef(
	t *testing.T,
	bundle chainbundle.Bundle,
) store.BlockRef {
	t.Helper()
	reference, err := store.RefFromBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	return reference
}

func indexerTestHash(value byte) common.Hash {
	return common.Hash{common.HashLength - 1: value}
}
