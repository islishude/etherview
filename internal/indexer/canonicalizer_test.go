package indexer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/store"
)

func TestCanonicalizerInitializesAndExtendsAcrossHeadGap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryRepository()
	genesis := indexerTestBundle(0, indexerTestHash(1), indexerTestHash(0))
	blockOne := indexerTestBundle(1, indexerTestHash(2), indexerTestHash(1))
	blockTwo := indexerTestBundle(2, indexerTestHash(3), indexerTestHash(2))
	blockThree := indexerTestBundle(3, indexerTestHash(4), indexerTestHash(3))
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
	if err != nil || !exists || tip.Number != 3 || !tip.Hash.Equal(indexerTestHash(4)) {
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
	oldChain := []ethrpc.Bundle{
		indexerTestBundle(0, indexerTestHash(1), indexerTestHash(0)),
		indexerTestBundle(1, indexerTestHash(2), indexerTestHash(1)),
		indexerTestBundle(2, indexerTestHash(3), indexerTestHash(2)),
		indexerTestBundle(3, indexerTestHash(4), indexerTestHash(3)),
	}
	canonicalizer := testCanonicalizer(repository, nil)
	for _, bundle := range oldChain {
		if _, err := canonicalizer.Apply(ctx, bundle); err != nil {
			t.Fatal(err)
		}
	}
	newTwo := indexerTestBundle(2, indexerTestHash(12), indexerTestHash(2))
	newThree := indexerTestBundle(3, indexerTestHash(13), indexerTestHash(12))
	newFour := indexerTestBundle(4, indexerTestHash(14), indexerTestHash(13))
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
	if err != nil || !exists || !canonicalTwo.Hash.Equal(indexerTestHash(12)) {
		t.Fatalf("canonical block 2 = %+v, exists = %v, error = %v", canonicalTwo, exists, err)
	}
	if _, exists, err := repository.BundleByHash(ctx, "1", indexerTestHash(3)); err != nil || !exists {
		t.Fatalf("old block retained = %v, error = %v", exists, err)
	}
}

func TestCanonicalizerStopsReorgAcrossFinalized(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryRepository()
	chain := []ethrpc.Bundle{
		indexerTestBundle(0, indexerTestHash(1), indexerTestHash(0)),
		indexerTestBundle(1, indexerTestHash(2), indexerTestHash(1)),
		indexerTestBundle(2, indexerTestHash(3), indexerTestHash(2)),
		indexerTestBundle(3, indexerTestHash(4), indexerTestHash(3)),
	}
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
	newTwo := indexerTestBundle(2, indexerTestHash(12), indexerTestHash(2))
	newThree := indexerTestBundle(3, indexerTestHash(13), indexerTestHash(12))
	newFour := indexerTestBundle(4, indexerTestHash(14), indexerTestHash(13))
	canonicalizer.Source = newMapSource(newTwo, newThree)
	_, err := canonicalizer.Apply(ctx, newFour)
	if !errors.Is(err, ErrFinalizedReorg) {
		t.Fatalf("error = %v, want ErrFinalizedReorg", err)
	}
	tip, _, _ := repository.CanonicalTip(ctx, "1")
	if !tip.Hash.Equal(indexerTestHash(4)) {
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
	genesis := indexerTestBundle(0, indexerTestHash(1), indexerTestHash(0))
	island := indexerTestBundle(3, indexerTestHash(4), indexerTestHash(3))
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []ethrpc.Bundle{genesis}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []ethrpc.Bundle{island}); err != nil {
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
	chain := []ethrpc.Bundle{
		indexerTestBundle(0, indexerTestHash(1), indexerTestHash(0)),
		indexerTestBundle(1, indexerTestHash(2), indexerTestHash(1)),
		indexerTestBundle(2, indexerTestHash(3), indexerTestHash(2)),
	}
	for _, bundle := range chain {
		if _, err := canonicalizer.Apply(ctx, bundle); err != nil {
			t.Fatal(err)
		}
	}
	newOne := indexerTestBundle(1, indexerTestHash(12), indexerTestHash(1))
	newTwo := indexerTestBundle(2, indexerTestHash(13), indexerTestHash(12))
	canonicalizer.Source = newMapSource(newOne)
	_, err := canonicalizer.Apply(ctx, newTwo)
	if !errors.Is(err, ErrReorgTooDeep) {
		t.Fatalf("error = %v, want ErrReorgTooDeep", err)
	}
}

func TestCanonicalizerRejectsStaleAlternateHead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryRepository()
	canonicalizer := testCanonicalizer(repository, nil)
	chain := []ethrpc.Bundle{
		indexerTestBundle(0, indexerTestHash(1), indexerTestHash(0)),
		indexerTestBundle(1, indexerTestHash(2), indexerTestHash(1)),
		indexerTestBundle(2, indexerTestHash(3), indexerTestHash(2)),
	}
	for _, bundle := range chain {
		if _, err := canonicalizer.Apply(ctx, bundle); err != nil {
			t.Fatal(err)
		}
	}
	_, err := canonicalizer.Apply(ctx, indexerTestBundle(1, indexerTestHash(12), indexerTestHash(1)))
	if !errors.Is(err, ErrStaleHead) {
		t.Fatalf("error = %v, want ErrStaleHead", err)
	}
}

func TestCanonicalizerAllowsAuthoritativeHeadToMoveBackward(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryRepository()
	canonicalizer := testCanonicalizer(repository, nil)
	chain := []ethrpc.Bundle{
		indexerTestBundle(0, indexerTestHash(1), indexerTestHash(0)),
		indexerTestBundle(1, indexerTestHash(2), indexerTestHash(1)),
		indexerTestBundle(2, indexerTestHash(3), indexerTestHash(2)),
	}
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
	oldChain := indexerTestChain(101, 1)
	if _, err := repository.CommitCanonicalSegment(ctx, "1", oldChain); err != nil {
		t.Fatal(err)
	}
	ancestor, _ := store.RefFromBundle(oldChain[98])
	newNinetyNine := indexerTestBundle(99, indexerTestHash(209), ancestor.Hash)
	newHundred := indexerTestBundle(100, indexerTestHash(210), indexerTestHash(209))
	newHundredOne := indexerTestBundle(101, indexerTestHash(211), indexerTestHash(210))
	newHundredTwo := indexerTestBundle(102, indexerTestHash(212), indexerTestHash(211))
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []ethrpc.Bundle{newHundredTwo}); err != nil {
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
		coverage.Contiguous == nil || !coverage.Contiguous.Hash.Equal(indexerTestHash(212)) {
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
	chain := indexerTestChain(101, 1)
	if _, err := repository.CommitCanonicalSegment(ctx, "1", chain); err != nil {
		t.Fatal(err)
	}
	island := indexerTestBundle(102, indexerTestHash(212), indexerTestHash(211))
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []ethrpc.Bundle{island}); err != nil {
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
	oldHundred := indexerTestBundle(100, indexerTestHash(100), indexerTestHash(99))
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []ethrpc.Bundle{oldHundred}); err != nil {
		t.Fatal(err)
	}
	newHundred := indexerTestBundle(100, indexerTestHash(210), indexerTestHash(208))
	newHundredOne := indexerTestBundle(101, indexerTestHash(211), indexerTestHash(210))
	newHundredTwo := indexerTestBundle(102, indexerTestHash(212), indexerTestHash(211))
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
	genesis := indexerTestBundle(0, indexerTestHash(1), indexerTestHash(0))
	if _, err := canonicalizer.Apply(ctx, genesis); err != nil {
		t.Fatal(err)
	}
	blockTwo := indexerTestBundle(2, indexerTestHash(3), indexerTestHash(2))
	wrongParent := indexerTestBundle(7, indexerTestHash(2), indexerTestHash(99))
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
	genesis := indexerTestBundle(0, indexerTestHash(1), indexerTestHash(0))
	blockOne := indexerTestBundle(1, indexerTestHash(2), indexerTestHash(1))
	for _, bundle := range []ethrpc.Bundle{genesis, blockOne} {
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
	chain := []ethrpc.Bundle{
		indexerTestBundle(0, indexerTestHash(1), indexerTestHash(0)),
		indexerTestBundle(1, indexerTestHash(2), indexerTestHash(1)),
		indexerTestBundle(2, indexerTestHash(3), indexerTestHash(2)),
	}
	for _, bundle := range chain {
		if _, err := canonicalizer.Apply(ctx, bundle); err != nil {
			t.Fatal(err)
		}
	}
	checkpointBefore, _, _ := repository.Checkpoint(ctx, "1", store.CoreCheckpoint)
	refreshed := chain[1]
	refreshed.Block.ExtraData = ethrpc.Data("0xcafe")
	result, err := canonicalizer.Refresh(ctx, refreshed, store.RefreshOptions{})
	if err != nil || result.Disposition != DispositionAlreadyKnown {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	stored, exists, err := repository.BundleByHash(ctx, "1", indexerTestHash(2))
	if err != nil || !exists || stored.Block.ExtraData.String() != "0xcafe" {
		t.Fatalf("stored=%+v exists=%v error=%v", stored.Block.ExtraData, exists, err)
	}
	tip, exists, err := repository.CanonicalTip(ctx, "1")
	if err != nil || !exists || tip.Number != 2 || !tip.Hash.Equal(indexerTestHash(3)) {
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
	chain := []ethrpc.Bundle{
		indexerTestBundle(0, indexerTestHash(1), indexerTestHash(0)),
		indexerTestBundle(1, indexerTestHash(2), indexerTestHash(1)),
		indexerTestBundle(2, indexerTestHash(3), indexerTestHash(2)),
	}
	for _, bundle := range chain {
		if _, err := canonicalizer.Apply(ctx, bundle); err != nil {
			t.Fatal(err)
		}
	}
	finalized, _, _ := repository.CanonicalBlock(ctx, "1", 1)
	if err := canonicalizer.UpdateFinality(ctx, &finalized, &finalized); err != nil {
		t.Fatal(err)
	}
	refreshed := chain[1]
	refreshed.Block.ExtraData = ethrpc.Data("0xab")
	if _, err := canonicalizer.Refresh(ctx, refreshed, store.RefreshOptions{}); !errors.Is(err, store.ErrFinalizedRefresh) {
		t.Fatalf("finalized refresh error=%v", err)
	}
	if _, err := canonicalizer.Refresh(ctx, refreshed, store.RefreshOptions{AllowFinalized: true}); err != nil {
		t.Fatal(err)
	}
	alternate := indexerTestBundle(1, indexerTestHash(12), indexerTestHash(1))
	if _, err := canonicalizer.Refresh(ctx, alternate, store.RefreshOptions{AllowFinalized: true}); !errors.Is(err, ErrStaleHead) {
		t.Fatalf("alternate historical refresh error=%v", err)
	}
	tip, _, _ := repository.CanonicalTip(ctx, "1")
	if !tip.Hash.Equal(indexerTestHash(3)) {
		t.Fatalf("refresh override changed canonical tip: %+v", tip)
	}
}

func TestCanonicalizerRefreshCannotExtendCanonicalChain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryRepository()
	canonicalizer := testCanonicalizer(repository, nil)
	chain := []ethrpc.Bundle{
		indexerTestBundle(0, indexerTestHash(1), indexerTestHash(0)),
		indexerTestBundle(1, indexerTestHash(2), indexerTestHash(1)),
	}
	for _, bundle := range chain {
		if _, err := canonicalizer.Apply(ctx, bundle); err != nil {
			t.Fatal(err)
		}
	}
	candidate := indexerTestBundle(2, indexerTestHash(3), indexerTestHash(2))
	if _, err := canonicalizer.Refresh(ctx, candidate, store.RefreshOptions{}); !errors.Is(err, ErrGap) {
		t.Fatalf("tip extension refresh error=%v", err)
	}
	tip, exists, err := repository.CanonicalTip(ctx, "1")
	if err != nil || !exists || tip.Number != 1 || !tip.Hash.Equal(indexerTestHash(2)) {
		t.Fatalf("tip changed after rejected refresh: tip=%+v exists=%v error=%v", tip, exists, err)
	}
	if _, exists, err := repository.CanonicalBlock(ctx, "1", 2); err != nil || exists {
		t.Fatalf("refresh created canonical block 2: exists=%v error=%v", exists, err)
	}
	if _, exists, err := repository.BundleByHash(ctx, "1", indexerTestHash(3)); err != nil || exists {
		t.Fatalf("refresh persisted noncanonical candidate: exists=%v error=%v", exists, err)
	}
}

func TestCanonicalizerRefreshCannotReplaceCanonicalIdentityAtTip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryRepository()
	canonicalizer := testCanonicalizer(repository, nil)
	chain := []ethrpc.Bundle{
		indexerTestBundle(0, indexerTestHash(1), indexerTestHash(0)),
		indexerTestBundle(1, indexerTestHash(2), indexerTestHash(1)),
	}
	for _, bundle := range chain {
		if _, err := canonicalizer.Apply(ctx, bundle); err != nil {
			t.Fatal(err)
		}
	}
	alternate := indexerTestBundle(1, indexerTestHash(12), indexerTestHash(1))
	if _, err := canonicalizer.Refresh(ctx, alternate, store.RefreshOptions{AllowFinalized: true}); !errors.Is(err, ErrStaleHead) {
		t.Fatalf("alternate tip refresh error=%v", err)
	}
	tip, exists, err := repository.CanonicalTip(ctx, "1")
	if err != nil || !exists || !tip.Hash.Equal(indexerTestHash(2)) {
		t.Fatalf("tip changed after rejected identity replacement: tip=%+v exists=%v error=%v", tip, exists, err)
	}
	if _, exists, err := repository.BundleByHash(ctx, "1", indexerTestHash(12)); err != nil || exists {
		t.Fatalf("refresh persisted alternate identity: exists=%v error=%v", exists, err)
	}
}

func TestIngestorUsesOnePurposeEndpointForBlockAndReceipts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	bundle := indexerTestBundle(0, indexerTestHash(1), indexerTestHash(0))
	first := &indexerFakeCaller{bundle: bundle}
	second := &indexerFakeCaller{bundle: bundle}
	pool, err := ethrpc.NewPool([]ethrpc.Endpoint{
		{Name: "history-a", Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeHistory: true}, Client: first,
			Capabilities: ethrpc.CapabilityReport{Methods: map[string]ethrpc.Availability{ethrpc.CapabilityBlockReceipts: ethrpc.AvailabilityAvailable}}},
		{Name: "history-b", Purposes: map[ethrpc.Purpose]bool{ethrpc.PurposeHistory: true}, Client: second,
			Capabilities: ethrpc.CapabilityReport{Methods: map[string]ethrpc.Availability{ethrpc.CapabilityBlockReceipts: ethrpc.AvailabilityAvailable}}},
	}, ethrpc.PoolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	repository := store.NewMemoryRepository()
	ingestor := &Ingestor{Pool: pool, Canonicalizer: testCanonicalizer(repository, nil)}
	if _, err := ingestor.ByNumber(ctx, ethrpc.PurposeHistory, ethrpc.QuantityFromUint64(0)); err != nil {
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
	bundles map[string]ethrpc.Bundle
}

func newMapSource(bundles ...ethrpc.Bundle) *mapSource {
	source := &mapSource{bundles: make(map[string]ethrpc.Bundle, len(bundles))}
	for _, bundle := range bundles {
		hash, _ := bundle.BlockHash()
		source.bundles[strings.ToLower(hash.String())] = bundle
	}
	return source
}

func (s *mapSource) BundleByHash(_ context.Context, hash ethrpc.Hash) (ethrpc.Bundle, bool, error) {
	bundle, exists := s.bundles[strings.ToLower(hash.String())]
	return bundle, exists, nil
}

type indexerFakeCaller struct {
	mu      sync.Mutex
	bundle  ethrpc.Bundle
	methods []string
}

func (f *indexerFakeCaller) Call(_ context.Context, method string, _ []any, result any) error {
	f.mu.Lock()
	f.methods = append(f.methods, method)
	f.mu.Unlock()
	var value any
	switch method {
	case "eth_getBlockByNumber":
		value = f.bundle.Block
	case "eth_getBlockReceipts":
		value = f.bundle.Receipts
	default:
		return fmt.Errorf("unexpected method %s", method)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, result)
}

func (f *indexerFakeCaller) Methods() []string {
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

func indexerTestBundle(number uint64, hash, parent ethrpc.Hash) ethrpc.Bundle {
	numberQuantity := ethrpc.QuantityFromUint64(number)
	zeroHash := indexerTestHash(0)
	return ethrpc.Bundle{Block: ethrpc.Block{
		Number:           &numberQuantity,
		Hash:             new(hash),
		ParentHash:       parent,
		Sha3Uncles:       zeroHash,
		TransactionsRoot: zeroHash,
		StateRoot:        zeroHash,
		ReceiptsRoot:     zeroHash,
		ExtraData:        ethrpc.Data("0x"),
		GasLimit:         ethrpc.QuantityFromUint64(30_000_000),
		GasUsed:          ethrpc.QuantityFromUint64(0),
		Timestamp:        ethrpc.QuantityFromUint64(1_700_000_000 + number),
		Transactions:     []ethrpc.TransactionRef{},
		Uncles:           []ethrpc.Hash{},
	}, Receipts: []ethrpc.Receipt{}}
}

func indexerTestChain(length int, firstHash byte) []ethrpc.Bundle {
	chain := make([]ethrpc.Bundle, length)
	parent := indexerTestHash(0)
	for index := range length {
		hash := indexerTestHash(firstHash + byte(index))
		chain[index] = indexerTestBundle(uint64(index), hash, parent)
		parent = hash
	}
	return chain
}

func indexerTestHash(value byte) ethrpc.Hash {
	hash, err := ethrpc.ParseHash(fmt.Sprintf("0x%064x", value))
	if err != nil {
		panic(err)
	}
	return hash
}
