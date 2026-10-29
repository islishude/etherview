//go:build integration

package integration_test

import (
	"bytes"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/chainbundle"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/store"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestConsensusDiscoveryReorgWhileDisabledAndConcurrentAdvance(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repo, _ := store.NewPostgresRepository(db)
	if err := repo.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(991000), testHash(0), testHash(991001), "discovery-genesis")
	original := testBundle(1, testHash(991010), genesis.Block.Hash(), testHash(991011), "discovery-original")
	replacement := testBundle(1, testHash(991020), genesis.Block.Hash(), testHash(991021), "discovery-replacement")
	if _, err := repo.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{genesis, original}); err != nil {
		t.Fatal(err)
	}
	execFixture(t, ctx, db, `INSERT INTO consensus_discovery_cursors(chain_id,stage,next_number,latest_number) VALUES(1,'consensus',-1,1),(1,'blob',-1,1),(1,'requests',-1,1)`)
	applyDerivedReorg(t, ctx, repo, genesis, []chainbundle.Bundle{original}, []chainbundle.Bundle{replacement}, "collector disabled")
	execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at=now()`)
	var chain, zero, one pgtype.Numeric
	_ = chain.Scan("1")
	_ = zero.Scan("0")
	_ = one.Scan("1")
	q := dbgen.New(db)
	for _, stage := range []string{"consensus", "blob", "requests"} {
		// A scan begun before the reorg must not erase its rewind when it finishes.
		if err := q.ConsensusAdvanceDiscovery(ctx, dbgen.ConsensusAdvanceDiscoveryParams{ChainID: chain, Stage: stage, LastNumber: zero, LatestNumber: one, Generation: 0}); err != nil {
			t.Fatal(err)
		}
		rows, err := q.ConsensusDiscover(ctx, chain, stage)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range rows {
			if bytes.Equal(row.BlockHash, replacement.Block.Hash().Bytes()) {
				found = true
				if row.Generation != 1 || row.ReplayKey == "" {
					t.Fatalf("missing generation/attach identity: %+v", row)
				}
			}
		}
		if !found {
			t.Fatalf("%s skipped canonical replacement", stage)
		}
		if err := q.ConsensusAdvanceDiscovery(ctx, dbgen.ConsensusAdvanceDiscoveryParams{ChainID: chain, Stage: stage, LastNumber: zero, LatestNumber: one, Generation: 1}); err != nil {
			t.Fatal(err)
		}
		rows, err = q.ConsensusDiscover(ctx, chain, stage)
		if err != nil || len(rows) != 0 {
			t.Fatalf("current scan did not advance: %+v %v", rows, err)
		}
	}
	// Cursor rewinds must also observe same-hash reattachment after completion.
	applyDerivedReorg(t, ctx, repo, genesis, []chainbundle.Bundle{replacement}, []chainbundle.Bundle{original}, "same hash reattach")
	rows, err := q.ConsensusDiscover(ctx, chain, "consensus")
	if err != nil || len(rows) != 2 || !bytes.Equal(rows[0].BlockHash, original.Block.Hash().Bytes()) || rows[0].Generation != 2 {
		t.Fatalf("reattach rows=%+v err=%v", rows, err)
	}
}

func TestConsensusDiscoveryInitialScanDoesNotSkipMiddle(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx := t.Context()
	repo, _ := store.NewPostgresRepository(db)
	if err := repo.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	blocks := make([]chainbundle.Bundle, 70)
	parent := common.Hash{}
	for i := range blocks {
		block, err := newIntegrationBundle(integrationBundleOptions{Number: uint64(i), ParentHash: parent})
		if err != nil {
			t.Fatal(err)
		}
		blocks[i] = block
		parent = block.Block.Hash()
	}
	if _, err := repo.CommitCanonicalSegment(ctx, "1", blocks); err != nil {
		t.Fatal(err)
	}
	var chain pgtype.Numeric
	_ = chain.Scan("1")
	q := dbgen.New(db)
	if err := q.ConsensusInitializeDiscovery(ctx, chain, "consensus"); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for range 4 {
		rows, err := q.ConsensusDiscover(ctx, chain, "consensus")
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			break
		}
		if len(rows) > 64 {
			t.Fatal("unbounded discovery")
		}
		for _, row := range rows {
			seen[row.BlockNumber] = true
		}
		var last, latest pgtype.Numeric
		_ = last.Scan(rows[len(rows)-1].BlockNumber)
		_ = latest.Scan(rows[0].BlockNumber)
		if err := q.ConsensusAdvanceDiscovery(ctx, dbgen.ConsensusAdvanceDiscoveryParams{ChainID: chain, Stage: "consensus", LastNumber: last, LatestNumber: latest, Generation: rows[0].Generation}); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != len(blocks) {
		t.Fatalf("discovered %d of %d blocks", len(seen), len(blocks))
	}
}
