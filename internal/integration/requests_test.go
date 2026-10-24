//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/attestantio/go-eth2-client/spec/bellatrix"
	"github.com/attestantio/go-eth2-client/spec/electra"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	beacontypes "github.com/ethereum/go-ethereum/beacon/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/consensus"
	"github.com/islishude/etherview/internal/consensus/testfixture"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/query"
	"github.com/islishude/etherview/internal/store"
)

func TestRequestsPublicationPaginationReorgAndReplay(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	repo, _ := store.NewPostgresRepository(db)
	if err := repo.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(194000), testHash(0), testHash(194001), "requests-genesis")
	if _, err := repo.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{genesis}); err != nil {
		t.Fatal(err)
	}
	const checksummedSource = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"
	sourceAddress := bellatrix.ExecutionAddress(common.HexToAddress(checksummedSource))
	requests := &gloas.ExecutionRequests{Deposits: []*electra.DepositRequest{{WithdrawalCredentials: make([]byte, 32), Amount: 9007199254740993, Index: 9007199254740993}, {WithdrawalCredentials: make([]byte, 32), Amount: 1, Index: 9007199254740994}}, Withdrawals: []*electra.WithdrawalRequest{{SourceAddress: sourceAddress}}, Consolidations: []*electra.ConsolidationRequest{{SourceAddress: sourceAddress}}, BuilderDeposits: []*gloas.BuilderDepositRequest{{WithdrawalCredentials: make([]byte, 32), Amount: 1}}, BuilderExits: []*gloas.BuilderExitRequest{{SourceAddress: sourceAddress}}}
	commitment, err := testfixture.RequestsHash(requests)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := newIntegrationBundle(integrationBundleOptions{Number: 1, ParentHash: genesis.Block.Hash(), Timestamp: 124, Amsterdam: true, RequestsHash: &commitment})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{bundle}); err != nil {
		t.Fatal(err)
	}
	header, body, envelope, err := testfixture.Requests("gloas", bundle.Block, beacontypes.Header{Slot: 2, ParentRoot: common.Hash{1}}, requests)
	if err != nil {
		t.Fatal(err)
	}
	var pruned atomic.Bool
	var reads atomic.Int64
	var allReads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allReads.Add(1)
		var value any
		switch r.URL.Path {
		case "/eth/v1/beacon/genesis":
			value = map[string]any{"data": map[string]string{"genesis_time": "100", "genesis_fork_version": "0x00000001", "genesis_validators_root": common.Hash{4}.Hex()}}
		case "/eth/v1/config/spec":
			value = map[string]any{"data": map[string]string{"SECONDS_PER_SLOT": "12", "SLOTS_PER_EPOCH": "32"}}
		case "/eth/v1/config/deposit_contract":
			value = map[string]any{"data": map[string]string{"chain_id": "1"}}
		case "/eth/v1/beacon/headers/2":
			reads.Add(1)
			value = testfixture.HeaderResponse(header)
		case "/eth/v2/beacon/blocks/" + header.Hash().Hex():
			reads.Add(1)
			value = body
		case "/eth/v1/beacon/execution_payload_envelopes/" + header.Hash().Hex():
			reads.Add(1)
			value = envelope
		default:
			http.NotFound(w, r)
			return
		}
		if pruned.Load() && !strings.Contains(r.URL.Path, "/config/") && r.URL.Path != "/eth/v1/beacon/genesis" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(value)
	}))
	defer server.Close()
	cfg := config.DefaultConsensusConfig()
	cfg.Enabled = true
	cfg.URL = server.URL
	reader, err := query.NewPostgresReader(db, query.Options{ChainID: 1, Consensus: cfg})
	if err != nil {
		t.Fatal(err)
	}
	before, _, err := reader.BlockRequests(ctx, "1", "", "", 50)
	if err != nil || before.State != gen.ConsensusStatePending {
		t.Fatalf("before=%+v err=%v", before, err)
	}
	queue, _ := enrich.NewPostgresJobQueue(db)
	processor := &enrich.RequestsProcessor{DB: db, Client: consensus.NewClient(cfg)}
	publishBlock := func(key string, target chainbundle.Bundle) {
		t.Helper()
		execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at=now()`)
		_, err := queue.Enqueue(ctx, enrich.EnqueueRequest{Stage: enrich.RequestsStage, ChainID: "1", BlockNumber: target.Block.NumberU64(), BlockHash: target.Block.Hash(), Replay: enrich.ReplaySource{Kind: "requests-test", Key: key}})
		if err != nil {
			t.Fatal(err)
		}
		worker, err := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{ID: "requests-test", LeaseDuration: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		if found, err := worker.ProcessOne(ctx); err != nil || !found {
			t.Fatalf("process=%v err=%v", found, err)
		}
	}
	publish := func(key string) { publishBlock(key, bundle) }
	publish("initial")
	readCount := reads.Load()
	apiReadCount := allReads.Load()
	page, next, err := reader.BlockRequests(ctx, "1", "", "", 2)
	if err != nil || page.State != gen.ConsensusStateComplete || len(page.Items) != 2 || next == "" || page.Total == nil || *page.Total != "6" {
		var jobs string
		_ = db.QueryRow(ctx, `SELECT json_agg(json_build_object('stage',stage,'status',status,'last_error',last_error))::text FROM durable_jobs`).Scan(&jobs)
		t.Fatalf("page=%+v next=%s err=%v jobs=%s", page, next, err, jobs)
	}
	second, _, err := reader.BlockRequests(ctx, "1", "", next, 100)
	if err != nil || len(second.Items) != 4 {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	// Exercise the public JSON boundary for every type carrying a source address.
	for _, kind := range []string{"0x01", "0x02", "0x04"} {
		response, _, err := reader.BlockRequests(ctx, "1", kind, "", 50)
		if err != nil || len(response.Items) != 1 {
			t.Fatalf("%s response=%+v err=%v", kind, response, err)
		}
		encoded, err := json.Marshal(response.Items[0])
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]string
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		if fields["source_address"] != checksummedSource {
			t.Errorf("%s source_address=%q, want %q", kind, fields["source_address"], checksummedSource)
		}
	}
	// Presentation formatting must not rewrite the authenticated retained evidence.
	var normalizedSources int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM execution_requests WHERE record->>'source_address'=$1`, strings.ToLower(checksummedSource)).Scan(&normalizedSources); err != nil || normalizedSources != 3 {
		t.Fatalf("normalized retained sources=%d err=%v", normalizedSources, err)
	}
	if _, _, err := reader.BlockRequests(ctx, "1", "0x04", next, 2); !errors.Is(err, query.ErrInvalidCursor) {
		t.Fatalf("cross-filter cursor=%v", err)
	}
	filtered, _, err := reader.BlockRequests(ctx, "1", "0x04", "", 50)
	if err != nil || len(filtered.Items) != 1 {
		t.Fatalf("filtered=%+v err=%v", filtered, err)
	}
	if allReads.Load() != apiReadCount {
		t.Fatal("API contacted upstream")
	}
	if _, _, err := reader.BlockRequests(ctx, genesis.Block.Hash().Hex(), "", next, 2); !errors.Is(err, query.ErrInvalidCursor) {
		t.Fatalf("cross-block cursor=%v", err)
	}
	disabled, err := query.NewPostgresReader(db, query.Options{ChainID: 1})
	if err != nil {
		t.Fatal(err)
	}
	disabledPage, _, err := disabled.BlockRequests(ctx, "1", "", "", 50)
	if err != nil || disabledPage.State != gen.ConsensusStateDisabled || len(disabledPage.Items) != 0 {
		t.Fatalf("disabled=%+v err=%v", disabledPage, err)
	}
	pruned.Store(true)
	publish("retained")
	if reads.Load() != readCount {
		t.Fatal("replay fetched pruned body")
	}
	if _, _, err := reader.BlockRequests(ctx, "1", "", next, 2); !errors.Is(err, query.ErrInvalidCursor) {
		t.Fatalf("old publication cursor=%v", err)
	}
	alternate, err := newIntegrationBundle(integrationBundleOptions{Number: 1, ParentHash: genesis.Block.Hash(), Timestamp: 124, ExtraData: []byte("alternate")})
	if err != nil {
		t.Fatal(err)
	}
	applyDerivedReorg(t, ctx, repo, genesis, []chainbundle.Bundle{bundle}, []chainbundle.Bundle{alternate}, "requests replacement")
	orphan, _, err := reader.BlockRequests(ctx, bundle.Block.Hash().Hex(), "", "", 100)
	if err != nil || len(orphan.Items) != 6 {
		t.Fatalf("orphan=%+v err=%v", orphan, err)
	}
	publishBlock("preactivation", alternate)
	preactivation, _, err := reader.BlockRequests(ctx, "1", "", "", 100)
	if err != nil || preactivation.State != gen.ConsensusStateNotApplicable || len(preactivation.Items) != 0 {
		t.Fatalf("preactivation=%+v err=%v", preactivation, err)
	}
	applyDerivedReorg(t, ctx, repo, genesis, []chainbundle.Bundle{alternate}, []chainbundle.Bundle{bundle}, "requests reattach")
	publish("reattach")
	restored, _, err := reader.BlockRequests(ctx, "1", "", "", 100)
	if err != nil || restored.State != gen.ConsensusStateComplete || len(restored.Items) != 6 {
		t.Fatalf("restored=%+v err=%v", restored, err)
	}
	// Expired publication attempts cannot expose even intact retained rows.
	_, err = queue.Enqueue(ctx, enrich.EnqueueRequest{Stage: enrich.RequestsStage, ChainID: "1", BlockNumber: 1, BlockHash: bundle.Block.Hash(), Replay: enrich.ReplaySource{Kind: "requests-test", Key: "expired"}})
	if err != nil {
		t.Fatal(err)
	}
	old, found, err := queue.Claim(ctx, "expired-requests", []enrich.StageID{enrich.RequestsStage}, time.Minute)
	if err != nil || !found {
		t.Fatalf("expired claim=%v err=%v", found, err)
	}
	execFixture(t, ctx, db, `UPDATE durable_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE stage='requests' AND status='leased'`)
	if _, err := processor.ProcessLease(ctx, old, queue); !errors.Is(err, enrich.ErrLeaseLost) {
		t.Fatalf("expired publication=%v", err)
	}
	hidden, _, err := reader.BlockRequests(ctx, "1", "", "", 100)
	if err != nil || hidden.State == gen.ConsensusStateComplete {
		t.Fatalf("expired visibility=%+v err=%v", hidden, err)
	}
	publish("after-expiry")
	// Retained evidence is not trusted without checking the execution commitment again.
	execFixture(t, ctx, db, `UPDATE execution_requests SET record=jsonb_set(record,'{amount}','"2"') WHERE request_type=0 AND sequence_index=0`)
	publish("tampered")
	invalid, _, err := reader.BlockRequests(ctx, "1", "", "", 100)
	if err != nil || invalid.State == gen.ConsensusStateComplete || len(invalid.Items) != 0 {
		t.Fatalf("tampered=%+v err=%v", invalid, err)
	}
}
