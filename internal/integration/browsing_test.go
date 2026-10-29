//go:build integration

package integration_test

import (
	"errors"
	"testing"

	"github.com/islishude/etherview/internal/catalog"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/store"
)

func TestBrowsingReturnsPublishedIslandsAndFencesLatePublication(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	contract, from, recipient := testAddress(30), testAddress(31), testAddress(32)
	var blocks []chainbundle.Bundle
	parent := testHash(0)
	for n := range uint64(4) {
		block := derivedTokenBundle(t, n, testHash(230000+n), parent, testHash(231000+n), "browsing", contract, from, recipient, 10+n)
		if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{block}); err != nil {
			t.Fatal(err)
		}
		blocks = append(blocks, block)
		parent = block.Block.Hash()
	}
	reader, _ := catalog.NewPostgres(db, catalog.Options{})
	request := catalog.TokenEventRequest{ChainID: "1", TokenAddress: contract.String(), Limit: 1}
	pending, err := reader.TokenEvents(ctx, request)
	if err != nil || len(pending.Items) != 0 || pending.Snapshot.Browsing.State != "pending" {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	// A missing latest block and a hole at block 1 must not hide blocks 0 and 2.
	markTokenStageComplete(t, ctx, db, blocks[0])
	markTokenStageComplete(t, ctx, db, blocks[2])
	first, err := reader.TokenEvents(ctx, request)
	if err != nil || len(first.Items) != 1 || first.Items[0].BlockNumber != "2" || first.NextCursor == "" || first.Snapshot.Browsing.State != "partial" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	request.Cursor = first.NextCursor
	second, err := reader.TokenEvents(ctx, request)
	if err != nil || len(second.Items) != 1 || second.Items[0].BlockNumber != "0" {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	// Publishing above a frozen snapshot must not invalidate its traversal.
	extra := derivedTokenBundle(t, 4, testHash(230004), parent, testHash(231004), "browsing-later", contract, from, recipient, 14)
	if _, err := repository.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{extra}); err != nil {
		t.Fatal(err)
	}
	markTokenStageComplete(t, ctx, db, extra)
	if _, err := reader.TokenEvents(ctx, request); err != nil {
		t.Fatalf("later block invalidated cursor: %v", err)
	}
	markTokenStageComplete(t, ctx, db, blocks[1])
	if _, err := reader.TokenEvents(ctx, request); !errors.Is(err, catalog.ErrInvalidCursor) {
		t.Fatalf("late publication cursor=%v", err)
	}
	markTokenStageComplete(t, ctx, db, blocks[3])
	request.Cursor = ""
	full, err := reader.TokenEvents(ctx, request)
	if err != nil || full.Snapshot.Browsing.State != "complete" {
		t.Fatalf("full=%+v err=%v", full, err)
	}
	queue, _ := enrich.NewPostgresJobQueue(db)
	word, _ := enrich.ParseWord(blocks[2].Block.Hash().Hex())
	if _, err := queue.Enqueue(ctx, enrich.EnqueueRequest{Stage: enrich.TokenStage, ChainID: "1", BlockHash: word, BlockNumber: 2, Replay: enrich.ReplaySource{Kind: "browsing-test", Key: "replay"}}); err != nil {
		t.Fatal(err)
	}
	request.Cursor = full.NextCursor
	if _, err := reader.TokenEvents(ctx, request); !errors.Is(err, catalog.ErrInvalidCursor) {
		t.Fatalf("replay cursor=%v", err)
	}
	request.Cursor = ""
	request.Limit = 10
	partial, err := reader.TokenEvents(ctx, request)
	if err != nil || partial.Snapshot.Browsing.State != "partial" {
		t.Fatalf("replay partial=%+v err=%v", partial, err)
	}
	for _, item := range partial.Items {
		if item.BlockNumber == "2" {
			t.Fatal("replay exposed revoked rows")
		}
	}
}
