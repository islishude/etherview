//go:build integration

package integration_test

import (
	"bytes"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	beacontypes "github.com/ethereum/go-ethereum/beacon/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/consensus"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/store"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestConsensusUnavailableKeepsProductionWorkerAlive(t *testing.T) {
	for _, test := range []struct {
		name, code string
		status     int
		pow, blobs bool
	}{
		{"history-404", "beacon_not_found", 404, false, false},
		{"unsupported-endpoint", "beacon_unsupported_endpoint", 501, false, false},
		{"unknown-fork", "unsupported_fork", 200, false, false},
		{"premerge-consensus", "not_applicable", 200, true, false},
		{"premerge-blob", "not_applicable", 200, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := newMigratedPostgres(t)
			ctx := t.Context()
			repo, _ := store.NewPostgresRepository(db)
			if err := repo.ConfigureIndex(ctx, "1", 0); err != nil {
				t.Fatal(err)
			}
			difficulty := big.NewInt(0)
			if test.pow {
				difficulty.SetUint64(1)
			}
			block, err := newIntegrationBundle(integrationBundleOptions{Number: 0, Timestamp: 124, Difficulty: difficulty})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := repo.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{block}); err != nil {
				t.Fatal(err)
			}
			header := beacontypes.Header{Slot: 2}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if test.status != 200 {
					w.WriteHeader(test.status)
					return
				}
				var response any
				switch r.URL.Path {
				case "/eth/v1/beacon/genesis":
					response = map[string]any{"data": map[string]string{"genesis_time": "100", "genesis_fork_version": "0x00000001", "genesis_validators_root": common.Hash{1}.Hex()}}
				case "/eth/v1/config/spec":
					response = map[string]any{"data": map[string]string{"SECONDS_PER_SLOT": "12", "SLOTS_PER_EPOCH": "32"}}
				case "/eth/v1/config/deposit_contract":
					response = map[string]any{"data": map[string]string{"chain_id": "1"}}
				case "/eth/v1/beacon/headers/2":
					response = map[string]any{"data": map[string]any{"root": header.Hash().Hex(), "header": map[string]any{"message": map[string]string{"slot": "2", "proposer_index": "0", "parent_root": header.ParentRoot.Hex(), "state_root": header.StateRoot.Hex(), "body_root": header.BodyRoot.Hex()}}}}
				default:
					response = map[string]any{"version": "unknown", "data": map[string]any{"message": map[string]any{"slot": "2", "proposer_index": "0", "parent_root": header.ParentRoot.Hex(), "state_root": header.StateRoot.Hex(), "body": map[string]any{}}}}
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			cfg := config.DefaultConsensusConfig()
			cfg.URL = server.URL
			p := &enrich.ConsensusProcessor{DB: db, Client: consensus.NewClient(cfg), Blobs: test.blobs}
			queue, _ := enrich.NewPostgresJobQueue(db)
			execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at=now()`)
			if _, err := queue.Enqueue(ctx, enrich.EnqueueRequest{Stage: p.Stage(), ChainID: "1", BlockHash: block.Block.Hash()}); err != nil {
				t.Fatal(err)
			}
			worker, err := enrich.NewWorker(queue, []enrich.Processor{p}, enrich.WorkerOptions{ID: "optional", LeaseDuration: time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			if found, err := worker.ProcessOne(ctx); err != nil || !found {
				t.Fatalf("optional result stopped worker: found=%v err=%v", found, err)
			}
			var chain pgtype.Numeric
			_ = chain.Scan("1")
			result, err := dbgen.New(db).ConsensusStageState(ctx, chain, block.Block.Hash().Bytes(), p.Stage().Name)
			if err != nil || result.State.String != "unavailable" || result.Code != test.code {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if found, err := worker.ProcessOne(ctx); err != nil || found {
				t.Fatalf("worker did not remain usable: found=%v err=%v", found, err)
			}
		})
	}
}

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
