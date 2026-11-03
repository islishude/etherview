//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/chainbundle"
	dbaccess "github.com/islishude/etherview/internal/db"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/ethrpc"
	"github.com/islishude/etherview/internal/indexer"
	"github.com/islishude/etherview/internal/maintenance"
	"github.com/islishude/etherview/internal/mempool"
	"github.com/islishude/etherview/internal/metadata"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/syncer"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type replicaHeadSource struct {
	bundle  chainbundle.Bundle
	entered chan struct{}
	release chan struct{}
}

func (s *replicaHeadSource) Head(context.Context) (uint64, error) { return s.bundle.Number() }
func (s *replicaHeadSource) Finality(context.Context) (*store.BlockRef, *store.BlockRef, error) {
	return nil, nil, nil
}
func (s *replicaHeadSource) BundleByNumber(ctx context.Context, _ ethrpc.Purpose, _ uint64) (chainbundle.Bundle, error) {
	if s.entered != nil {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
			return chainbundle.Bundle{}, ctx.Err()
		}
	}
	return s.bundle, nil
}
func TestLiveSyncReplicasFenceLostSessionAndDelayedHead(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	repo, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	b0 := testBundle(0, testHash(1), testHash(0), testHash(10), "replica-0")
	b1 := testBundle(1, testHash(2), b0.Block.Hash(), testHash(11), "replica-1")
	b2 := testBundle(2, testHash(3), b1.Block.Hash(), testHash(12), "replica-2")
	commitCanonical(t, ctx, repo, b0)
	commitCanonical(t, ctx, repo, b1)
	slow := &replicaHeadSource{bundle: b1, entered: make(chan struct{}), release: make(chan struct{})}
	makeService := func(src *replicaHeadSource) *syncer.Service {
		return &syncer.Service{ChainID: "1", Workers: 1, Source: src, Repository: repo, Canonicalizer: &indexer.Canonicalizer{ChainID: "1", StartBlock: 0, MaxReorgDepth: 128, Repository: repo}, Tracker: &syncer.Tracker{}}
	}
	done := make(chan error, 1)
	go func() { done <- makeService(slow).Cycle(ctx) }()
	select {
	case <-slow.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	if err := makeService(&replicaHeadSource{bundle: b2}).Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	before, _, err := repo.CanonicalTip(ctx, "1")
	if err != nil || before.Number != 1 {
		t.Fatalf("contender changed head: %v %v", before, err)
	}
	terminateRuntimeSession(t, ctx, db, "etherview:live-head:1")
	if err := makeService(&replicaHeadSource{bundle: b2}).Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	tip, _, err := repo.CanonicalTip(ctx, "1")
	if err != nil || tip.Number != 2 {
		t.Fatalf("new tip=%v err=%v", tip, err)
	}
	close(slow.release)
	if err := <-done; err == nil {
		t.Fatal("lost live session accepted delayed response")
	}
	tip, _, err = repo.CanonicalTip(ctx, "1")
	if err != nil || tip.Number != 2 {
		t.Fatalf("stale live session changed tip=%v err=%v", tip, err)
	}
}

func TestMaintenanceReplicasFenceLostSessionRepair(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	repo, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	original := testBundle(0, testHash(41), testHash(0), testHash(42), "replica-repair")
	commitCanonical(t, ctx, repo, original)
	older, err := withIntegrationBlockRawField(original, "replicaMarker", "old")
	if err != nil {
		t.Fatal(err)
	}
	newer, err := withIntegrationBlockRawField(original, "replicaMarker", "new")
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := db.QueryRow(ctx, `INSERT INTO repair_requests(chain_id,operation,stage,from_block,to_block,reason) VALUES(1,'repair','core',0,0,'replica recovery regression') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	slow := &replicaHeadSource{bundle: older, entered: make(chan struct{}), release: make(chan struct{})}
	makeWorker := func(src *replicaHeadSource, name string) *maintenance.Worker {
		r, e := maintenance.NewPostgresRepository(db)
		if e != nil {
			t.Fatal(e)
		}
		ex, e := maintenance.NewExecutor("1", src, &indexer.Canonicalizer{ChainID: "1", MaxReorgDepth: 128, Repository: repo}, repo, queue)
		if e != nil {
			t.Fatal(e)
		}
		w, e := maintenance.NewWorker(r, ex, maintenance.WorkerOptions{WorkerID: name})
		if e != nil {
			t.Fatal(e)
		}
		return w
	}
	a, b := makeWorker(slow, "replica-a"), makeWorker(&replicaHeadSource{bundle: newer}, "replica-b")
	done := make(chan error, 1)
	go func() { _, e := a.ProcessOne(ctx); done <- e }()
	select {
	case <-slow.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var pid int32
	if err := db.QueryRow(ctx, `SELECT pid FROM pg_locks WHERE locktype='advisory' AND classid=2147483648::oid AND objid=$1::oid AND objsubid=1 AND granted`, id).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	var killed bool
	if err := db.QueryRow(ctx, `SELECT pg_terminate_backend($1,5000)`, pid).Scan(&killed); err != nil || !killed {
		t.Fatalf("terminate owned test session=%t err=%v", killed, err)
	}
	if found, e := b.ProcessOne(ctx); e != nil || !found {
		t.Fatalf("takeover found=%t err=%v", found, e)
	}
	var value string
	read := func() string {
		if e := db.QueryRow(ctx, `SELECT raw->>'replicaMarker' FROM blocks WHERE chain_id=1 AND hash=$1`, original.Block.Hash().Bytes()).Scan(&value); e != nil {
			t.Fatal(e)
		}
		return value
	}
	if got := read(); got != "new" {
		t.Fatalf("takeover wrote %q", got)
	}
	close(slow.release)
	e := <-done
	if e == nil {
		t.Fatal("expected terminal transition on lost connection to fail")
	}
	if got := read(); got != "new" {
		t.Fatalf("stale repair overwrote takeover: final raw=%q", got)
	}

}

type replicaMempoolSource struct {
	raw              json.RawMessage
	entered, release chan struct{}
}

func (s replicaMempoolSource) PendingTransactions(ctx context.Context) (json.RawMessage, string, error) {
	if s.entered != nil {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, "rpc", ctx.Err()
		}
	}
	return s.raw, "rpc", nil
}
func TestMempoolReplicasFenceLostSessionSnapshot(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if err := store.BindChainIdentity(ctx, db, "1", testHash(1)); err != nil {
		t.Fatal(err)
	}
	repo, err := mempool.NewPostgres(db, mempool.PostgresOptions{ChainID: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	tx := mempoolIntegrationTransaction(t, 1, time.Now(), time.Now().Add(time.Minute))
	raw := json.RawMessage(fmt.Sprintf(`{"pending":{"%s":{"1":%s}},"queued":{}}`, tx.From, tx.Raw))
	entered, release := make(chan struct{}), make(chan struct{})
	var clockMu sync.Mutex
	now := time.Now().UTC()
	opts := mempool.PollerOptions{ChainID: 1, PollInterval: time.Second, Retention: time.Minute, MaxTransactions: 10, MaxResponseBytes: 1 << 20, Now: func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); now = now.Add(time.Millisecond); return now }}
	a, err := mempool.NewPoller(replicaMempoolSource{raw, entered, release}, repo, opts)
	if err != nil {
		t.Fatal(err)
	}
	b, err := mempool.NewPoller(replicaMempoolSource{raw: json.RawMessage(`{"pending":{},"queued":{}}`)}, repo, opts)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Cycle(ctx) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := b.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	var initial int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM mempool_status`).Scan(&initial); err != nil || initial != 0 {
		t.Fatalf("contender published: %d %v", initial, err)
	}
	terminateRuntimeSession(t, ctx, db, "etherview:mempool-poll:1")
	if err := b.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err == nil {
		t.Fatal("lost mempool session accepted old response")
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT transaction_count FROM mempool_status WHERE chain_id=1`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("stale response overwrote snapshot: count=%d", count)
	}

}

func TestMetadataReplicasSerializeIdenticalEnqueue(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	core, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	blockHash := testHash(920)
	bundle := testBundle(0, blockHash, testHash(0), testHash(9_200), "metadata-source")
	blockHash = bundle.Block.Hash()
	commitCanonical(t, ctx, core, bundle)
	token := testAddress(921)
	if _, err := db.Exec(ctx, `
		INSERT INTO token_contracts (
			chain_id, address, code_hash, standard, confidence, metadata_state,
			observed_block_number, observed_block_hash
		) VALUES (1, $1, $2, 'erc721', 'high', 'pending', 0, $3)`,
		mustBytes(t, token), mustBytes(t, testHash(922)), mustBytes(t, blockHash)); err != nil {
		t.Fatalf("insert NFT contract: %v", err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO token_events (
			chain_id, block_number, block_hash, log_index, sub_index,
			transaction_hash, token_address, standard, event_kind,
			from_address, to_address, token_id, amount, canonical, confidence, raw
		) VALUES (1, 0, $1, 0, 0, $2, $3, 'erc721', 'transfer',
			$4, $5, 42, 1, TRUE, 'high', '{}')`,
		mustBytes(t, blockHash), mustBytes(t, bundle.Block.Transactions()[0].Hash()),
		mustBytes(t, token), mustBytes(t, testAddress(923)), mustBytes(t, testAddress(924))); err != nil {
		t.Fatalf("insert NFT event candidate: %v", err)
	}
	repository, err := metadata.NewPostgresRepository(db, "1")
	if err != nil {
		t.Fatal(err)
	}
	candidate, found, err := repository.NextNFTSource(ctx)
	if err != nil || !found || candidate.Token != token || candidate.TokenID != "42" ||
		candidate.BlockHash != blockHash || candidate.Standard != metadata.NFTStandardERC721 {
		t.Fatalf("source candidate = %+v found=%t err=%v", candidate, found, err)
	}
	other, err := metadata.NewPostgresRepository(db, "1")
	if err != nil {
		t.Fatal(err)
	}
	duplicate, found, err := other.NextNFTSource(ctx)
	if err != nil || !found || duplicate != candidate {
		t.Fatalf("duplicate=%+v found=%v error=%v", duplicate, found, err)
	}
	gate := &concurrentMetadataDB{Pool: db, ready: make(chan struct{}, 2), release: make(chan struct{})}
	request := metadata.NFTRequest{ChainID: "1", Token: candidate.Token, TokenID: candidate.TokenID, BlockNumber: candidate.BlockNumber, BlockHash: candidate.BlockHash, SourceURI: "https://metadata.example/42.json"}
	outcomes := make(chan error, 2)
	for range 2 {
		r, e := metadata.NewPostgresRepository(gate, "1")
		if e != nil {
			t.Fatal(e)
		}
		go func() { _, e := r.EnqueueNFT(ctx, request); outcomes <- e }()
	}
	for range 2 {
		select {
		case <-gate.ready:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	close(gate.release)
	for range 2 {
		if err := <-outcomes; err != nil {
			t.Fatalf("identical concurrent enqueue: %v", err)
		}
	}
	var jobs int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM durable_jobs WHERE kind='metadata'`).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("jobs=%d error=%v", jobs, err)
	}
	// Source discovery must acquire before selecting/fetching and publish using
	// the same session. Two independent repositories model separate replicas.
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, e := repository.WithSourceDiscovery(ctx, func(scoped metadata.NFTSourceRepository) error {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			return scoped.RecordNFTSource(ctx, metadata.NFTSourceObservation{Candidate: candidate, State: metadata.NFTSourceFound, SourceURI: request.SourceURI})
		})
		done <- e
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	ran, e := other.WithSourceDiscovery(ctx, func(metadata.NFTSourceRepository) error { return fmt.Errorf("contender executed discovery") })
	if e != nil || ran {
		t.Fatalf("discovery contention ran=%v error=%v", ran, e)
	}
	terminateRuntimeSession(t, ctx, db, "etherview:nft-source:1")
	ran, e = other.WithSourceDiscovery(ctx, func(scoped metadata.NFTSourceRepository) error {
		return scoped.RecordNFTSource(ctx, metadata.NFTSourceObservation{Candidate: candidate, State: metadata.NFTSourceUnavailable, ErrorCode: "token_uri_unavailable"})
	})
	if e != nil || !ran {
		t.Fatalf("discovery takeover ran=%v error=%v", ran, e)
	}
	close(release)
	if e := <-done; e == nil {
		t.Fatal("stale discovery published")
	}

}

type concurrentMetadataDB struct {
	*pgxpool.Pool
	ready   chan struct{}
	release chan struct{}
}

func (d *concurrentMetadataDB) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	tx, e := d.Pool.BeginTx(ctx, opts)
	if e != nil {
		return nil, e
	}
	if _, e = tx.Exec(ctx, "SELECT 1"); e != nil {
		_ = tx.Rollback(ctx)
		return nil, e
	}
	d.ready <- struct{}{}
	select {
	case <-d.release:
		return tx, nil
	case <-ctx.Done():
		_ = tx.Rollback(ctx)
		return nil, ctx.Err()
	}
}

// The lock is scoped to this test's isolated migration schema; terminate only
// its owned connection to model a server-side disconnect during external work.
func terminateRuntimeSession(t *testing.T, ctx context.Context, db *pgxpool.Pool, key string) {
	t.Helper()
	var killed bool
	err := db.QueryRow(ctx, `SELECT pg_terminate_backend(pid,5000) FROM pg_locks
 WHERE locktype='advisory' AND granted AND objsubid=1
 AND classid=((hashtextextended(current_schema() || ':' || $1,0)>>32)&4294967295)::oid
 AND objid=(hashtextextended(current_schema() || ':' || $1,0)&4294967295)::oid`, key).Scan(&killed)
	if err != nil || !killed {
		t.Fatalf("terminate test runtime session: killed=%v error=%v", killed, err)
	}
}

func TestRuntimeSessionLockCleanupAndSnapshotBoundary(t *testing.T) {
	db := newMigratedPostgres(t)
	for _, mode := range []string{"success", "failure", "cancel", "panic"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			key := "test:session-cleanup:" + mode
			func() {
				defer func() {
					if p := recover(); p != nil && mode != "panic" {
						t.Fatalf("unexpected panic: %v", p)
					}
				}()
				ran, err := dbaccess.WithSessionLock(ctx, db, key, func(session dbaccess.Database) error {
					var pid int32
					if e := session.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); e != nil {
						return e
					}
					var idle bool
					if e := db.QueryRow(ctx, "SELECT state='idle' AND xact_start IS NULL FROM pg_stat_activity WHERE pid=$1", pid).Scan(&idle); e != nil {
						return e
					}
					if !idle {
						return errors.New("external work retains a database snapshot")
					}
					switch mode {
					case "failure":
						return errors.New("controlled failure")
					case "cancel":
						cancel()
						return ctx.Err()
					case "panic":
						panic("controlled panic")
					}
					return nil
				})
				if !ran || (mode == "success" && err != nil) || (mode != "success" && err == nil) {
					t.Fatalf("ran=%v error=%v", ran, err)
				}
			}()
			// A pooled connection may reacquire its own advisory lock recursively,
			// so successful reacquisition alone does not prove that cleanup ran.
			var remaining int
			if err := db.QueryRow(t.Context(), `SELECT count(*) FROM pg_locks
 WHERE locktype='advisory' AND granted AND objsubid=1
 AND classid=((hashtextextended(current_schema() || ':' || $1,0)>>32)&4294967295)::oid
 AND objid=(hashtextextended(current_schema() || ':' || $1,0)&4294967295)::oid`, key).Scan(&remaining); err != nil || remaining != 0 {
				t.Fatalf("session lock survived cleanup: count=%d error=%v", remaining, err)
			}
			ran, err := dbaccess.WithSessionLock(t.Context(), db, key, func(dbaccess.Database) error { return nil })
			if err != nil || !ran {
				t.Fatalf("released session not reacquirable: ran=%v error=%v", ran, err)
			}
		})
	}
}
