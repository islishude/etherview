//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/events"
	"github.com/islishude/etherview/internal/query"
	"github.com/islishude/etherview/internal/store"
)

func TestHomeSnapshotIsWriterAuthoritativeBoundedAndReorgConsistent(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	eventStore, err := events.NewPostgresStore(db, "1", events.PostgresOptions{ReplayLimit: 32})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := query.NewPostgresReader(db, query.Options{ChainID: 1, StartBlock: 0})
	if err != nil {
		t.Fatal(err)
	}

	bundles := make([]chainbundle.Bundle, 0, 8)
	parent := testHash(0)
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	for number := range uint64(8) {
		bundle := testBundle(
			number,
			testHash(20_000+number),
			parent,
			testHash(21_000+number),
			"home-snapshot",
		)
		if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{bundle}); err != nil {
			t.Fatalf("commit canonical segment at %d: %v", number, err)
		}
		bundles = append(bundles, bundle)
		parent = bundle.Block.Hash()
	}
	statusEvent, err := eventStore.RecordStatus(ctx, events.SyncStatus{
		Latest: 7, Indexed: 7, HighestCovered: 7,
		LatestKnown: true, IndexedKnown: true, HighestCoveredKnown: true,
		BackfillComplete: true, Ready: true, PolledAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := reader.HomeSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.EventID != statusEvent.ID || !first.Status.CoreReady ||
		first.Status.CoverageStart != 0 || first.Status.CoverageEnd != 7 ||
		len(first.Blocks) != 6 || len(first.Transactions) != 6 ||
		first.Blocks[0].Number != "7" || first.Blocks[5].Number != "2" ||
		first.Transactions[0].BlockNumber == nil || *first.Transactions[0].BlockNumber != "7" {
		t.Fatalf("first home snapshot = %+v", first)
	}

	alternateSix := testBundle(
		6, testHash(22_006), bundles[5].Block.Hash(), testHash(23_006), "home-reorg-six",
	)
	alternateSeven := testBundle(
		7, testHash(22_007), alternateSix.Block.Hash(), testHash(23_007), "home-reorg-seven",
	)
	reorg := store.Reorg{
		Ancestor:   mustBlockRef(t, bundles[5]),
		Detached:   []store.BlockRef{mustBlockRef(t, bundles[7]), mustBlockRef(t, bundles[6])},
		Attached:   []chainbundle.Bundle{alternateSix, alternateSeven},
		Checkpoint: store.NewCoreCheckpoint(mustBlockRef(t, alternateSeven)),
		Reason:     "home snapshot integration",
	}
	if err := repository.ApplyReorg(ctx, "1", reorg); err != nil {
		t.Fatal(err)
	}
	statusEvent, err = eventStore.RecordStatus(ctx, events.SyncStatus{
		Latest: 7, Indexed: 7, HighestCovered: 7,
		LatestKnown: true, IndexedKnown: true, HighestCoveredKnown: true,
		BackfillComplete: true, Ready: true, PolledAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := reader.HomeSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.EventID != statusEvent.ID || len(second.Blocks) != 6 ||
		second.Blocks[0].Hash != alternateSeven.Block.Hash().Hex() ||
		second.Blocks[1].Hash != alternateSix.Block.Hash().Hex() ||
		second.Transactions[0].BlockHash == nil ||
		*second.Transactions[0].BlockHash != alternateSeven.Block.Hash().Hex() {
		t.Fatalf("post-reorg home snapshot = %+v", second)
	}
	for _, block := range second.Blocks {
		if block.Hash == bundles[6].Block.Hash().Hex() || block.Hash == bundles[7].Block.Hash().Hex() {
			t.Fatalf("post-reorg snapshot mixed detached block %s", block.Hash)
		}
	}
}
