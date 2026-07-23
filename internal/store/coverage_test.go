package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/ethrpc"
)

func TestMemoryCoverageMergesOutOfOrderWithoutAdvancingAcrossGap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	if _, exists, err := repository.Coverage(ctx, "1"); err != nil || exists {
		t.Fatalf("unconfigured coverage exists=%v error=%v", exists, err)
	}
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	if err := repository.ConfigureIndex(ctx, "1", 1); !errors.Is(err, ErrIndexConfigurationMismatch) {
		t.Fatalf("configuration mismatch error=%v", err)
	}

	blockZero := storeTestBundle(0, storeTestHash(10), storeTestHash(0))
	blockOne := storeTestBundle(1, storeTestHash(11), storeTestHash(10))
	blockTwo := storeTestBundle(2, storeTestHash(12), storeTestHash(11))
	coverage, err := repository.CommitCanonicalSegment(ctx, "1", []ethrpc.Bundle{blockTwo})
	if err != nil {
		t.Fatal(err)
	}
	assertCoverage(t, coverage, []BlockRange{{Start: 2, End: 2}}, nil, 2)
	if _, exists, err := repository.Checkpoint(ctx, "1", CoreCheckpoint); err != nil || exists {
		t.Fatalf("live island advanced checkpoint: exists=%v error=%v", exists, err)
	}

	coverage, err = repository.CommitCanonicalSegment(ctx, "1", []ethrpc.Bundle{blockZero})
	if err != nil {
		t.Fatal(err)
	}
	checkpointZero := uint64(0)
	assertCoverage(t, coverage, []BlockRange{{Start: 0, End: 0}, {Start: 2, End: 2}}, &checkpointZero, 2)

	badBoundary := storeTestBundle(1, storeTestHash(11), storeTestHash(99))
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []ethrpc.Bundle{badBoundary}); !errors.Is(err, ErrConflict) {
		t.Fatalf("boundary mismatch error=%v", err)
	}
	if _, exists, err := repository.CanonicalBlock(ctx, "1", 1); err != nil || exists {
		t.Fatalf("failed segment left canonical block: exists=%v error=%v", exists, err)
	}
	if _, exists, err := repository.BundleByHash(ctx, "1", storeTestHash(11)); err != nil || exists {
		t.Fatalf("failed segment left block facts: exists=%v error=%v", exists, err)
	}
	coverage, exists, err := repository.Coverage(ctx, "1")
	if err != nil || !exists {
		t.Fatalf("coverage exists=%v error=%v", exists, err)
	}
	assertCoverage(t, coverage, []BlockRange{{Start: 0, End: 0}, {Start: 2, End: 2}}, &checkpointZero, 2)

	coverage, err = repository.CommitCanonicalSegment(ctx, "1", []ethrpc.Bundle{blockOne})
	if err != nil {
		t.Fatal(err)
	}
	checkpointTwo := uint64(2)
	assertCoverage(t, coverage, []BlockRange{{Start: 0, End: 2}}, &checkpointTwo, 2)
	checkpoint, exists, err := repository.Checkpoint(ctx, "1", CoreCheckpoint)
	if err != nil || !exists || checkpoint.ContiguousThrough != 2 || !checkpoint.BlockHash.Equal(storeTestHash(12)) {
		t.Fatalf("checkpoint=%+v exists=%v error=%v", checkpoint, exists, err)
	}
}

func TestConfigureIndexClearsLegacyTipCheckpointWhenConfiguredStartIsMissing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	legacy := storeTestBundle(5, storeTestHash(5), storeTestHash(4))
	legacyRef := mustStoreTestRef(t, legacy)
	if err := repository.CommitCanonical(ctx, "1", legacy, NewCoreCheckpoint(legacyRef)); err != nil {
		t.Fatal(err)
	}
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	coverage, exists, err := repository.Coverage(ctx, "1")
	if err != nil || !exists || coverage.Contiguous != nil || coverage.Highest == nil || coverage.Highest.Number != 5 {
		t.Fatalf("coverage=%+v exists=%v error=%v", coverage, exists, err)
	}
	if checkpoint, exists, err := repository.Checkpoint(ctx, "1", CoreCheckpoint); err != nil || exists {
		t.Fatalf("legacy checkpoint=%+v exists=%v error=%v", checkpoint, exists, err)
	}
}

func TestMemoryBackfillRangeLeasesExpireRenewReleaseAndCompleteFromCoverage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	lease, claimed, err := repository.ClaimBackfillRange(ctx, "1", BlockRange{Start: 2, End: 3}, "worker-a", now, time.Minute)
	if err != nil || !claimed {
		t.Fatalf("claim=%+v claimed=%v error=%v", lease, claimed, err)
	}
	if _, claimed, err := repository.ClaimBackfillRange(ctx, "1", BlockRange{Start: 3, End: 4}, "worker-b", now, time.Minute); err != nil || claimed {
		t.Fatalf("overlap claimed=%v error=%v", claimed, err)
	}

	reclaimed, claimed, err := repository.ClaimBackfillRange(
		ctx, "1", lease.Range, "worker-b", now.Add(2*time.Minute), time.Minute,
	)
	if err != nil || !claimed || reclaimed.Token == lease.Token {
		t.Fatalf("reclaim=%+v claimed=%v error=%v", reclaimed, claimed, err)
	}
	if _, err := repository.RenewBackfillRange(ctx, lease, now.Add(2*time.Minute), time.Minute); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale renew error=%v", err)
	}
	renewed, err := repository.RenewBackfillRange(ctx, reclaimed, now.Add(2*time.Minute+time.Second), 2*time.Minute)
	if err != nil || !renewed.ExpiresAt.After(reclaimed.ExpiresAt) {
		t.Fatalf("renewed=%+v error=%v", renewed, err)
	}
	if err := repository.CompleteBackfillRange(ctx, renewed); !errors.Is(err, ErrConflict) {
		t.Fatalf("uncovered completion error=%v", err)
	}

	blockTwo := storeTestBundle(2, storeTestHash(22), storeTestHash(21))
	blockThree := storeTestBundle(3, storeTestHash(23), storeTestHash(22))
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []ethrpc.Bundle{blockTwo, blockThree}); err != nil {
		t.Fatal(err)
	}
	if err := repository.CompleteBackfillRange(ctx, renewed); err != nil {
		t.Fatal(err)
	}
	if err := repository.CompleteBackfillRange(ctx, renewed); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("completed lease reused with error=%v", err)
	}
	if _, claimed, err := repository.ClaimBackfillRange(ctx, "1", BlockRange{Start: 1, End: 2}, "worker-c", now, time.Minute); err != nil || claimed {
		t.Fatalf("partly covered range claimed=%v error=%v", claimed, err)
	}

	released, claimed, err := repository.ClaimBackfillRange(ctx, "1", BlockRange{Start: 4, End: 4}, "worker-c", now, time.Minute)
	if err != nil || !claimed {
		t.Fatalf("release fixture claim=%v error=%v", claimed, err)
	}
	if err := repository.ReleaseBackfillRange(ctx, released); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := repository.ClaimBackfillRange(ctx, "1", released.Range, "worker-d", now, time.Minute); err != nil || !claimed {
		t.Fatalf("released range reclaimed=%v error=%v", claimed, err)
	}
	if _, _, err := repository.ClaimBackfillRange(ctx, "1", BlockRange{Start: 10, End: 266}, "worker", now, time.Minute); err == nil {
		t.Fatal("oversized backfill range was accepted")
	}
}

func TestMemoryBackfillLeaseContentionHasSingleWinner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	const contenders = 16
	start := make(chan struct{})
	type result struct {
		lease   BackfillLease
		claimed bool
		err     error
	}
	results := make(chan result, contenders)
	var wait sync.WaitGroup
	for index := range contenders {
		index := index
		wait.Go(func() {
			<-start
			lease, claimed, err := repository.ClaimBackfillRange(
				ctx, "1", BlockRange{Start: 10, End: 20}, fmt.Sprintf("worker-%d", index),
				time.Now().UTC(), time.Minute,
			)
			results <- result{lease: lease, claimed: claimed, err: err}
		})
	}
	close(start)
	wait.Wait()
	close(results)
	winners := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.claimed {
			winners++
			if err := validateBackfillLease(result.lease); err != nil {
				t.Fatalf("winner lease=%+v error=%v", result.lease, err)
			}
		}
	}
	if winners != 1 {
		t.Fatalf("lease winners=%d, want 1", winners)
	}
}

func TestCommittedCoverageSurvivesCrashBeforeLeaseCompletion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	lease, claimed, err := repository.ClaimBackfillRange(
		ctx, "1", BlockRange{Start: 0, End: 1}, "crashing-worker", now, time.Minute,
	)
	if err != nil || !claimed {
		t.Fatalf("lease=%+v claimed=%v error=%v", lease, claimed, err)
	}
	chain := storeTestChain(2, 1)
	if _, err := repository.CommitCanonicalSegment(ctx, "1", chain); err != nil {
		t.Fatal(err)
	}
	// Simulate a process stop here: no CompleteBackfillRange call is made.
	if _, claimed, err := repository.ClaimBackfillRange(
		ctx, "1", lease.Range, "restarted-worker", now.Add(2*time.Minute), time.Minute,
	); err != nil || claimed {
		t.Fatalf("covered range was reclaimed after restart: claimed=%v error=%v", claimed, err)
	}
	coverage, exists, err := repository.Coverage(ctx, "1")
	if err != nil || !exists || coverage.Contiguous == nil || coverage.Contiguous.Number != 1 {
		t.Fatalf("coverage=%+v exists=%v error=%v", coverage, exists, err)
	}
}

func TestMemorySparseReplacementCanExtendHighestIslandWithoutAdvancingCheckpoint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	genesis := storeTestBundle(0, storeTestHash(1), storeTestHash(0))
	oldHundred := storeTestBundle(100, storeTestHash(100), storeTestHash(99))
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []ethrpc.Bundle{genesis}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []ethrpc.Bundle{oldHundred}); err != nil {
		t.Fatal(err)
	}
	if err := repository.AppendJournal(ctx, "1", JournalEntry{
		BlockHash: storeTestHash(100), Stage: "token", Sequence: 1,
		Payload: json.RawMessage(`{"old":true}`),
	}); err != nil {
		t.Fatal(err)
	}
	newHundred := storeTestBundle(100, storeTestHash(110), storeTestHash(98))
	newHundredOne := storeTestBundle(101, storeTestHash(111), storeTestHash(110))
	newHundredTwo := storeTestBundle(102, storeTestHash(112), storeTestHash(111))
	coverage, err := repository.ReplaceHighestCanonicalSegment(ctx, "1", SparseCanonicalReplacement{
		Range:    BlockRange{Start: 100, End: 100},
		Detached: []BlockRef{mustStoreTestRef(t, oldHundred)},
		Attached: []ethrpc.Bundle{newHundred, newHundredOne, newHundredTwo},
		Reason:   "replace and extend isolated head",
	})
	if err != nil {
		t.Fatal(err)
	}
	checkpointZero := uint64(0)
	assertCoverage(t, coverage, []BlockRange{{Start: 0, End: 0}, {Start: 100, End: 102}}, &checkpointZero, 102)
	checkpoint, exists, err := repository.Checkpoint(ctx, "1", CoreCheckpoint)
	if err != nil || !exists || checkpoint.ContiguousThrough != 0 || !checkpoint.BlockHash.Equal(storeTestHash(1)) {
		t.Fatalf("checkpoint=%+v exists=%v error=%v", checkpoint, exists, err)
	}
	journals, err := repository.JournalsByBlock(ctx, "1", storeTestHash(100))
	if err != nil || len(journals) != 1 || journals[0].Canonical {
		t.Fatalf("old island journals=%+v error=%v", journals, err)
	}
	if _, exists, err := repository.BundleByHash(ctx, "1", storeTestHash(100)); err != nil || !exists {
		t.Fatalf("old island retained=%v error=%v", exists, err)
	}
}

func TestMemoryGeneralizedSparseReplacementClosesGapAndReorgsLowerCoverageAtomically(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	oldChain := storeTestChain(101, 1)
	if _, err := repository.CommitCanonicalSegment(ctx, "1", oldChain); err != nil {
		t.Fatal(err)
	}
	oldIsland := storeTestBundle(102, storeTestHash(202), storeTestHash(201))
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []ethrpc.Bundle{oldIsland}); err != nil {
		t.Fatal(err)
	}
	ancestor := mustStoreTestRef(t, oldChain[98])
	newNinetyNine := storeTestBundle(99, storeTestHash(209), ancestor.Hash)
	newHundred := storeTestBundle(100, storeTestHash(210), storeTestHash(209))
	newHundredOne := storeTestBundle(101, storeTestHash(211), storeTestHash(210))
	newHundredTwo := storeTestBundle(102, storeTestHash(212), storeTestHash(211))
	coverage, err := repository.ReplaceHighestCanonicalSegment(ctx, "1", SparseCanonicalReplacement{
		Range:    BlockRange{Start: 102, End: 102},
		Ancestor: &ancestor,
		Detached: []BlockRef{
			mustStoreTestRef(t, oldIsland),
			mustStoreTestRef(t, oldChain[100]),
			mustStoreTestRef(t, oldChain[99]),
		},
		Attached: []ethrpc.Bundle{newNinetyNine, newHundred, newHundredOne, newHundredTwo},
		Reason:   "repair shallow fork across live gap",
	})
	if err != nil {
		t.Fatal(err)
	}
	checkpointHeight := uint64(102)
	assertCoverage(t, coverage, []BlockRange{{Start: 0, End: 102}}, &checkpointHeight, 102)
	for number, hash := range map[uint64]ethrpc.Hash{
		99: storeTestHash(209), 100: storeTestHash(210), 101: storeTestHash(211), 102: storeTestHash(212),
	} {
		canonical, exists, err := repository.CanonicalBlock(ctx, "1", number)
		if err != nil || !exists || !canonical.Hash.Equal(hash) {
			t.Fatalf("canonical %d=%+v exists=%v error=%v", number, canonical, exists, err)
		}
	}
}

func assertCoverage(t *testing.T, coverage CoreCoverage, ranges []BlockRange, contiguous *uint64, highest uint64) {
	t.Helper()
	if len(coverage.Ranges) != len(ranges) {
		t.Fatalf("coverage ranges=%+v want=%+v", coverage.Ranges, ranges)
	}
	for index := range ranges {
		if coverage.Ranges[index] != ranges[index] {
			t.Fatalf("coverage ranges=%+v want=%+v", coverage.Ranges, ranges)
		}
	}
	if coverage.Highest == nil || coverage.Highest.Number != highest {
		t.Fatalf("coverage highest=%+v want=%d", coverage.Highest, highest)
	}
	if contiguous == nil {
		if coverage.Contiguous != nil {
			t.Fatalf("coverage contiguous=%+v want=nil", coverage.Contiguous)
		}
		return
	}
	if coverage.Contiguous == nil || coverage.Contiguous.Number != *contiguous {
		t.Fatalf("coverage contiguous=%+v want=%d", coverage.Contiguous, *contiguous)
	}
}

func mustStoreTestRef(t *testing.T, bundle ethrpc.Bundle) BlockRef {
	t.Helper()
	reference, err := RefFromBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	return reference
}

func storeTestChain(length int, firstHash byte) []ethrpc.Bundle {
	chain := make([]ethrpc.Bundle, length)
	parent := storeTestHash(0)
	for index := range length {
		hash := storeTestHash(firstHash + byte(index))
		chain[index] = storeTestBundle(uint64(index), hash, parent)
		parent = hash
	}
	return chain
}
