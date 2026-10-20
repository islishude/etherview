//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	beacontypes "github.com/ethereum/go-ethereum/beacon/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto/kzg4844"
	"github.com/islishude/etherview/internal/accelerator"
	"github.com/islishude/etherview/internal/api/gen"
	"github.com/islishude/etherview/internal/chainbundle"
	"github.com/islishude/etherview/internal/config"
	"github.com/islishude/etherview/internal/consensus"
	"github.com/islishude/etherview/internal/consensus/testfixture"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/query"
	"github.com/islishude/etherview/internal/store"
)

func TestConsensusBlobPublicationReorgAndReplay(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	repo, _ := store.NewPostgresRepository(db)
	if err := repo.ConfigureIndex(ctx, "1", 0); err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(191000), testHash(0), testHash(191001), "beacon-genesis")
	if _, err := repo.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{genesis}); err != nil {
		t.Fatal(err)
	}
	var blob kzg4844.Blob
	blob[31] = 9
	commitment, err := kzg4844.BlobToCommitment(&blob)
	if err != nil {
		t.Fatal(err)
	}
	hash := common.Hash(kzg4844.CalcBlobHashV1(sha256.New(), &commitment))
	commitmentHex := "0x" + hex.EncodeToString(commitment[:])
	block, err := newIntegrationBundle(integrationBundleOptions{Number: 1, ParentHash: genesis.Block.Hash(), Timestamp: 124, Amsterdam: true, Transactions: []integrationTransactionOptions{{Type: types.BlobTxType, BlobHashes: []common.Hash{hash, hash}}, {Type: types.BlobTxType, BlobHashes: []common.Hash{hash}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CommitCanonicalSegment(ctx, "1", []chainbundle.Bundle{block}); err != nil {
		t.Fatal(err)
	}
	header := beacontypes.Header{Slot: 2, ProposerIndex: 9007199254740993, ParentRoot: common.Hash{1}, StateRoot: common.Hash{2}, BodyRoot: common.Hash{3}}
	header, envelope, err := testfixture.Block("fulu", block.Block, header, []string{commitmentHex, commitmentHex, commitmentHex}, nil)
	if err != nil {
		t.Fatal(err)
	}
	root := header.Hash().Hex()
	var networkConflict atomic.Bool
	var pruned atomic.Bool
	var blobRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var response any
		switch r.URL.Path {
		case "/eth/v1/beacon/genesis":
			networkRoot := common.Hash{4}.Hex()
			if networkConflict.Load() {
				networkRoot = common.Hash{5}.Hex()
			}
			response = map[string]any{"data": map[string]string{"genesis_time": "100", "genesis_fork_version": "0x00000001", "genesis_validators_root": networkRoot}}
		case "/eth/v1/config/spec":
			response = map[string]any{"data": map[string]string{"SECONDS_PER_SLOT": "12", "SLOTS_PER_EPOCH": "32"}}
		case "/eth/v1/config/deposit_contract":
			response = map[string]any{"data": map[string]string{"chain_id": "1"}}
		case "/eth/v1/beacon/headers/2", "/eth/v1/beacon/headers/head":
			response = map[string]any{"data": map[string]any{"root": root, "header": map[string]any{"message": map[string]string{"slot": "2", "proposer_index": "9007199254740993", "parent_root": header.ParentRoot.Hex(), "state_root": header.StateRoot.Hex(), "body_root": header.BodyRoot.Hex()}}}}
		case "/eth/v2/beacon/blocks/" + root:
			if pruned.Load() {
				http.NotFound(w, r)
				return
			}
			response = envelope
		case "/eth/v1/beacon/states/" + header.StateRoot.Hex() + "/finality_checkpoints":
			response = map[string]any{"data": map[string]any{"current_justified": map[string]string{"epoch": "0", "root": root}, "finalized": map[string]string{"epoch": "0", "root": root}}}
		case "/eth/v1/node/syncing":
			response = map[string]any{"data": map[string]bool{"is_syncing": false, "is_optimistic": false}}
		case "/eth/v1/beacon/blobs/" + root:
			blobRequests.Add(1)
			if pruned.Load() {
				http.NotFound(w, r)
				return
			}
			response = map[string]any{"data": []string{"0x" + hex.EncodeToString(blob[:])}}
		default:
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()
	cfg := config.DefaultConsensusConfig()
	cfg.Enabled = true
	cfg.Blobs.Enabled = true
	cfg.URL = server.URL
	archive := &integrationBlobArchive{objects: map[string][]byte{}}
	reader, err := query.NewPostgresReader(db, query.Options{ChainID: 1, Consensus: cfg, BlobArchive: archive})
	if err != nil {
		t.Fatal(err)
	}
	before, err := reader.Block(ctx, "1")
	if err != nil || before.SlotNumber == nil || *before.SlotNumber != "1" || *before.SlotNumberSource != gen.Execution {
		t.Fatalf("fallback=%+v err=%v", before, err)
	}
	pending, err := reader.BlockBlobs(ctx, "1")
	if err != nil || len(pending.Items) != 3 || pending.Items[2].TransactionHash != block.Block.Transactions()[1].Hash().Hex() || pending.Items[2].TransactionIndex != "0" {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	queue, _ := enrich.NewPostgresJobQueue(db)
	cp := &enrich.ConsensusProcessor{DB: db, Client: consensus.NewClient(cfg)}
	bp := &enrich.ConsensusProcessor{DB: db, Client: cp.Client, Archive: archive, Namespace: "1/genesis", Blobs: true}
	publish := func(p *enrich.ConsensusProcessor, replay string) {
		t.Helper()
		execFixture(t, ctx, db, `UPDATE transactional_outbox SET published_at=now()`)
		_, err := queue.Enqueue(ctx, enrich.EnqueueRequest{Stage: p.Stage(), ChainID: "1", BlockNumber: 1, BlockHash: block.Block.Hash(), Replay: enrich.ReplaySource{Kind: "consensus-test", Key: replay}})
		if err != nil {
			t.Fatal(err)
		}
		worker, err := enrich.NewWorker(queue, []enrich.Processor{p}, enrich.WorkerOptions{ID: "consensus-test", LeaseDuration: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		if found, err := worker.ProcessOne(ctx); err != nil || !found {
			t.Fatalf("publish %s found=%v err=%v", p.Stage(), found, err)
		}
	}
	publish(cp, "initial")
	observed, err := reader.BlockConsensus(ctx, "1")
	if err != nil || observed.State != gen.ConsensusStateComplete || observed.SlotConflict == nil || !*observed.SlotConflict {
		var jobs string
		_ = db.QueryRow(ctx, `SELECT json_agg(json_build_object('stage',stage,'status',status,'last_error',last_error))::text FROM durable_jobs`).Scan(&jobs)
		t.Fatalf("consensus=%+v err=%v jobs=%s", observed, err, jobs)
	}
	got, err := reader.Block(ctx, "1")
	if err != nil || got.SlotNumber == nil || *got.SlotNumber != "2" || *got.SlotNumberSource != gen.Consensus {
		t.Fatalf("slot=%+v err=%v", got, err)
	}
	publish(bp, "initial")
	manifest, err := reader.BlockBlobs(ctx, "1")
	if err != nil || manifest.State != gen.ConsensusStateComplete || len(manifest.Items) != 3 {
		t.Fatalf("manifest=%+v err=%v", manifest, err)
	}
	if len(archive.objects) != 1 {
		t.Fatalf("duplicate content created %d objects", len(archive.objects))
	}
	for i := range 3 {
		data, err := reader.BlobContent(ctx, block.Block.Hash().Hex(), uint64(i))
		if err != nil || !bytes.Equal(data, blob[:]) {
			t.Fatalf("download=%d err=%v", len(data), err)
		}
	}
	archive.mu.Lock()
	archive.unavailable = true
	archive.mu.Unlock()
	if _, err := reader.BlobContent(ctx, block.Block.Hash().Hex(), 0); err == nil {
		t.Fatal("missing object returned success")
	}
	// Store recovery followed by replay keeps the same immutable manifest.
	archive.mu.Lock()
	archive.unavailable = false
	archive.mu.Unlock()
	publish(bp, "repair")
	// A replay racing independent archive readback invalidates the old generation.
	if _, err := queue.Enqueue(ctx, enrich.EnqueueRequest{Stage: enrich.BlobStage, ChainID: "1", BlockNumber: 1, BlockHash: block.Block.Hash(), Replay: enrich.ReplaySource{Kind: "consensus-test", Key: "readback-race"}}); err != nil {
		t.Fatal(err)
	}
	reading, found, err := queue.Claim(ctx, "archive-reader", []enrich.StageID{enrich.BlobStage}, time.Minute)
	if err != nil || !found {
		t.Fatalf("readback claim=%v err=%v", found, err)
	}
	bp.Archive = &replayDuringArchiveRead{Archive: archive, after: func() {
		if _, err := queue.Enqueue(ctx, enrich.EnqueueRequest{Stage: enrich.BlobStage, ChainID: "1", BlockNumber: 1, BlockHash: block.Block.Hash(), Replay: enrich.ReplaySource{Kind: "consensus-test", Key: "newer-during-readback"}}); err != nil {
			t.Fatal(err)
		}
	}}
	if _, err := bp.ProcessLease(ctx, reading, queue); err != nil {
		t.Fatal(err)
	}
	hidden, err := reader.BlockBlobs(ctx, "1")
	if err != nil || hidden.State == gen.ConsensusStateComplete {
		t.Fatalf("superseded archive published: %+v err=%v", hidden, err)
	}
	bp.Archive = archive
	publish(bp, "after-readback-race")

	alternative, err := newIntegrationBundle(integrationBundleOptions{Number: 1, ParentHash: genesis.Block.Hash(), Timestamp: 124, ExtraData: []byte("alternate")})
	if err != nil {
		t.Fatal(err)
	}
	applyDerivedReorg(t, ctx, repo, genesis, []chainbundle.Bundle{block}, []chainbundle.Bundle{alternative}, "consensus replacement")
	orphan, err := reader.Block(ctx, block.Block.Hash().Hex())
	if err != nil || orphan.Canonical || orphan.SlotNumber == nil || *orphan.SlotNumber != "2" {
		t.Fatalf("orphan=%+v err=%v", orphan, err)
	}
	if data, err := reader.BlobContent(ctx, block.Block.Hash().Hex(), 0); err != nil || !bytes.Equal(data, blob[:]) {
		t.Fatalf("orphan download err=%v", err)
	}
	applyDerivedReorg(t, ctx, repo, genesis, []chainbundle.Bundle{alternative}, []chainbundle.Bundle{block}, "consensus reattach")
	pruned.Store(true)
	beforeRequests := blobRequests.Load()
	publish(cp, "reattach")
	publish(bp, "reattach")
	publish(bp, "retained-reindex")
	if blobRequests.Load() != beforeRequests {
		t.Fatal("intact archive replay contacted pruned upstream")
	}
	if data, err := reader.BlobContent(ctx, block.Block.Hash().Hex(), 0); err != nil || !bytes.Equal(data, blob[:]) {
		t.Fatalf("archive-only replay: %v", err)
	}
	pruned.Store(false)
	if len(archive.objects) != 1 {
		t.Fatal("reattach duplicated content")
	}
	// A crashed/expired writer may upload bytes but cannot publish its generation.
	_, err = queue.Enqueue(ctx, enrich.EnqueueRequest{Stage: enrich.BlobStage, ChainID: "1", BlockNumber: 1, BlockHash: block.Block.Hash(), Replay: enrich.ReplaySource{Kind: "consensus-test", Key: "expired"}})
	if err != nil {
		t.Fatal(err)
	}
	old, found, err := queue.Claim(ctx, "expired-blob", []enrich.StageID{enrich.BlobStage}, time.Minute)
	if err != nil || !found {
		t.Fatalf("claim=%v %v", found, err)
	}
	execFixture(t, ctx, db, `UPDATE durable_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE stage='blob'`)
	archive.mu.Lock()
	archive.objects = map[string][]byte{}
	archive.mu.Unlock()
	if _, err := bp.ProcessLease(ctx, old, queue); !errors.Is(err, enrich.ErrLeaseLost) {
		t.Fatalf("expired publish=%v", err)
	}
	unpublished, err := reader.BlockBlobs(ctx, "1")
	if err != nil || unpublished.State == gen.ConsensusStateComplete {
		t.Fatalf("unpublished=%+v err=%v", unpublished, err)
	}
	if len(archive.objects) != 1 {
		t.Fatal("expected upload before failed database publication")
	}
	newer, found, err := queue.Claim(ctx, "replacement-blob", []enrich.StageID{enrich.BlobStage}, time.Minute)
	if err != nil || !found {
		t.Fatalf("replacement claim=%v %v", found, err)
	}
	if _, err := bp.ProcessLease(ctx, newer, queue); err != nil {
		t.Fatal(err)
	}
	if data, err := reader.BlobContent(ctx, block.Block.Hash().Hex(), 0); err != nil || !bytes.Equal(data, blob[:]) {
		t.Fatalf("recovered publication=%v", err)
	}
	// An intact archive needs no upstream; a genuinely lost object must report
	// historical unavailability without terminating its production worker.
	archive.mu.Lock()
	archive.objects = map[string][]byte{}
	archive.mu.Unlock()
	pruned.Store(true)
	publish(bp, "missing-pruned")
	unavailable, err := reader.BlockBlobs(ctx, "1")
	if err != nil || unavailable.State != gen.ConsensusStateUnavailable || unavailable.Code == nil || *unavailable.Code != "beacon_not_found" {
		t.Fatalf("missing archive state=%+v err=%v", unavailable, err)
	}
	pruned.Store(false)
	publish(bp, "restore-missing")
	// Restartable discovery persists its bounded progress and overview atomically.
	discoveryConfig := config.Default()
	discoveryConfig.Consensus = cfg
	discoveryConfig.Chain.ID = 1
	discovery := &enrich.ConsensusDiscovery{DB: db, Queue: queue, Client: cp.Client, Config: discoveryConfig}
	poll := func(wantCode string) {
		t.Helper()
		runCtx, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- discovery.Run(runCtx) }()
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		for {
			var code string
			var observed bool
			err := db.QueryRow(ctx, `SELECT last_error,observed_at IS NOT NULL FROM consensus_overviews WHERE chain_id=1`).Scan(&code, &observed)
			if err == nil && observed && code == wantCode {
				break
			}
			select {
			case <-deadline.C:
				stop()
				<-done
				t.Fatalf("overview code=%s observed=%v err=%v", code, observed, err)
			case <-time.After(10 * time.Millisecond):
			}
		}
		stop()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	poll("")
	assertRowCount(t, ctx, db, `SELECT count(*) FROM consensus_discovery_cursors WHERE next_number=-1`, 2)
	// Simulate a completed historical scan followed by blocks indexed while the
	// optional collector was stopped. Resume forward discovery without resetting history.
	execFixture(t, ctx, db, `UPDATE consensus_discovery_cursors SET latest_number=0`)
	networkConflict.Store(true)
	execFixture(t, ctx, db, `UPDATE consensus_overviews SET next_poll_at='-infinity'`)
	poll("beacon_network_mismatch")
	assertRowCount(t, ctx, db, `SELECT count(*) FROM consensus_discovery_cursors WHERE next_number=-1 AND latest_number=1`, 2)
	var retained string
	if err := db.QueryRow(ctx, `SELECT overview->>'root' FROM consensus_overviews WHERE chain_id=1`).Scan(&retained); err != nil || retained != root {
		t.Fatalf("retained overview=%s err=%v", retained, err)
	}
	// Retained observations must not bypass a freshly detected network conflict.
	publish(cp, "network-conflict")
	conflict, err := reader.BlockConsensus(ctx, "1")
	if err != nil || conflict.State != gen.ConsensusStateFailed || conflict.Code == nil || *conflict.Code != "beacon_network_mismatch" {
		t.Fatalf("network conflict=%+v err=%v", conflict, err)
	}
	networkConflict.Store(false)
	pruned.Store(true)
	publish(cp, "network-restored")
	publish(bp, "archive-restored")
	pruned.Store(false)
	disabled, err := query.NewPostgresReader(db, query.Options{ChainID: 1})
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := disabled.Block(ctx, "1")
	if err != nil || *fallback.SlotNumber != "1" || *fallback.SlotNumberSource != gen.Execution {
		t.Fatalf("disabled=%+v err=%v", fallback, err)
	}
}

type integrationBlobArchive struct {
	mu          sync.Mutex
	objects     map[string][]byte
	unavailable bool
}

func (a *integrationBlobArchive) Create(_ context.Context, key string, data []byte) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.unavailable {
		return "", accelerator.ErrArchiveUnavailable
	}
	sum := sha256.Sum256(data)
	if old, ok := a.objects[key]; ok && !bytes.Equal(old, data) {
		return "", errors.New("immutable conflict")
	}
	a.objects[key] = bytes.Clone(data)
	return "0x" + hex.EncodeToString(sum[:]), nil
}
func (a *integrationBlobArchive) Read(_ context.Context, key, digest string) ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.unavailable {
		return nil, accelerator.ErrArchiveUnavailable
	}
	data, ok := a.objects[key]
	if !ok {
		return nil, accelerator.ErrArchiveUnavailable
	}
	sum := sha256.Sum256(data)
	if "0x"+hex.EncodeToString(sum[:]) != digest {
		return nil, accelerator.ErrArchiveIntegrity
	}
	return bytes.Clone(data), nil
}

type replayDuringArchiveRead struct {
	consensus.Archive
	after func()
}

func (a *replayDuringArchiveRead) Read(ctx context.Context, key, digest string) ([]byte, error) {
	data, err := a.Archive.Read(ctx, key, digest)
	if a.after != nil {
		after := a.after
		a.after = nil
		after()
	}
	return data, err
}
