//go:build integration

package integration_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/store"
	"github.com/islishude/etherview/internal/testdbguard"
	"github.com/islishude/etherview/internal/verify"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type observedVerificationDatabase struct {
	dbaccess.Database
	begun    chan int32
	evidence chan struct{}
	resume   chan struct{}
}

func (db *observedVerificationDatabase) BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	tx, err := db.Database.BeginTx(ctx, options)
	if err == nil {
		db.begun <- int32(tx.Conn().PgConn().PID())
	}
	if err == nil && db.evidence != nil {
		return &pausedEvidenceTx{Tx: tx, evidence: db.evidence, resume: db.resume}, nil
	}
	return tx, err
}

type pausedEvidenceTx struct {
	pgx.Tx
	evidence chan struct{}
	resume   chan struct{}
}

func (tx *pausedEvidenceTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "-- name: VerifyLegacyVerificationCanonicalTarget ") || strings.Contains(sql, "-- name: DerivedVerifyPublicationEvidence ") {
		close(tx.evidence)
		select {
		case <-tx.resume:
		case <-ctx.Done():
		}
	}
	return tx.Tx.QueryRow(ctx, sql, args...)
}

// The publisher owns the advisory lock before verification starts. Verification
// must wait without taking a SHARE lock on the publisher's code observation.
func verificationWaitsBeforeEvidence(t *testing.T, ctx context.Context, db *pgxpool.Pool, hash common.Hash, number uint64, complete func(*verify.PostgresRepository) error) {
	t.Helper()
	verificationPublicationLockOrder(t, ctx, db, hash, number, false, complete)
}

func verificationPublicationLockOrder(t *testing.T, ctx context.Context, db *pgxpool.Pool, hash common.Hash, number uint64, reverse bool, complete func(*verify.PostgresRepository) error) {
	t.Helper()
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	result, err := queue.Enqueue(ctx, enrich.EnqueueRequest{ChainID: "1", Stage: enrich.ProxyStage, BlockHash: hash, BlockNumber: number})
	if err != nil {
		t.Fatal(err)
	}
	id, err := strconv.ParseInt(result.Job.ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer dbaccess.Rollback(ctx, publisher)
	if !reverse {
		if err := dbgen.New(publisher).EnrichLegacyLockPublicationJob(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	observed := &observedVerificationDatabase{Database: db, begun: make(chan int32, 8)}
	if reverse {
		observed.evidence = make(chan struct{})
		observed.resume = make(chan struct{})
	}
	repository, err := verify.NewPostgresRepository(observed, verify.RepositoryOptions{MaxRequestBytes: 1 << 20, MaxResultBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- complete(repository) }()
	var pid int32
	select {
	case pid = <-observed.begun:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if reverse {
		select {
		case <-observed.evidence:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		acquired := make(chan error, 1)
		go func() { acquired <- dbgen.New(publisher).EnrichLegacyLockPublicationJob(ctx, id) }()
		if err := testdbguard.WaitBlocked(ctx, db, int32(publisher.Conn().PgConn().PID()), pid); err != nil {
			t.Fatal(err)
		}
		close(observed.resume)
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		if err := <-acquired; err != nil {
			t.Fatal(err)
		}
		if _, err := publisher.Exec(ctx, `UPDATE contract_code_observations SET code=code WHERE chain_id=1 AND block_hash=$1`, hash.Bytes()); err != nil {
			t.Fatal(err)
		}
		if err := publisher.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := testdbguard.WaitBlocked(ctx, db, pid, int32(publisher.Conn().PgConn().PID())); err != nil {
		t.Fatal(err)
	}
	// Use a bounded write; the old ordering either deadlocks here or times out.
	writeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err := publisher.Exec(writeCtx, `UPDATE contract_code_observations SET code=code WHERE chain_id=1 AND block_hash=$1`, hash.Bytes()); err != nil {
		t.Fatalf("publisher blocked by verification evidence: %v", err)
	}
	if err := publisher.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestPreparedPublicationReplayPreservesGenerations(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	request := enrich.EnqueueRequest{ChainID: "1", Stage: enrich.ProxyStage, BlockHash: testHash(88001), BlockNumber: 1}
	// Use a canonical fixture so the queue's chain foreign key is present.
	core, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(88000), testHash(0), testHash(89000), "prepared-genesis")
	block := testBundle(1, request.BlockHash, genesis.Block.Hash(), testHash(89001), "prepared-block")
	commitCanonical(t, ctx, core, genesis)
	commitCanonical(t, ctx, core, block)
	first, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer dbaccess.Rollback(ctx, first)
	prepared, err := queue.PreparePublicationTx(ctx, first, request)
	if err != nil {
		t.Fatal(err)
	}
	// A concurrent creator waits for the uncommitted unique identity, then uses
	// that exact job. It cannot create a duplicate or acquire output locks first.
	second, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer dbaccess.Rollback(ctx, second)
	done := make(chan error, 1)
	replay := request
	replay.Replay = enrich.ReplaySource{Kind: "verification-publication", Key: "first"}
	go func() {
		other, err := queue.PreparePublicationTx(ctx, second, request)
		if err == nil {
			err = queue.ReplayPreparedTx(ctx, second, other, replay)
		}
		if err == nil {
			err = second.Commit(ctx)
		}
		done <- err
	}()
	if err := testdbguard.WaitBlocked(ctx, db, int32(second.Conn().PgConn().PID()), int32(first.Conn().PgConn().PID())); err != nil {
		t.Fatal(err)
	}
	if err := queue.ReplayPreparedTx(ctx, first, prepared, replay); err != nil {
		t.Fatal(err)
	}
	if err := queue.ReplayPreparedTx(ctx, first, prepared, replay); err != nil {
		t.Fatal(err)
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM durable_jobs WHERE stage='proxy' AND requested_generation=1`, 1)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM durable_job_replay_requests`, 1)
	// A different publication requests precisely one new generation.
	third, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer dbaccess.Rollback(ctx, third)
	prepared, err = queue.PreparePublicationTx(ctx, third, request)
	if err != nil {
		t.Fatal(err)
	}
	replay.Replay.Key = "second"
	if err := queue.ReplayPreparedTx(ctx, third, prepared, replay); err != nil {
		t.Fatal(err)
	}
	if err := queue.ReplayPreparedTx(ctx, third, prepared, replay); err != nil {
		t.Fatal(err)
	}
	if err := third.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM durable_jobs WHERE stage='proxy' AND requested_generation=2`, 1)
	lease, found, err := queue.Claim(ctx, "prepared-publication-heartbeat", []enrich.StageID{enrich.ProxyStage}, time.Minute)
	if err != nil || !found {
		t.Fatalf("claim prepared job: found=%t err=%v", found, err)
	}
	// Cancellation while waiting releases the transaction without a new replay.
	holder, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer dbaccess.Rollback(ctx, holder)
	if _, err := queue.PreparePublicationTx(ctx, holder, request); err != nil {
		t.Fatal(err)
	}
	// Holding preparation must not lock the job row needed by heartbeat renewal.
	renewCtx, renewCancel := context.WithTimeout(ctx, time.Second)
	defer renewCancel()
	if err := queue.Renew(renewCtx, lease, time.Minute); err != nil {
		t.Fatalf("preparation blocked heartbeat: %v", err)
	}
	waiter, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitCtx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		_, err := queue.PreparePublicationTx(waitCtx, waiter, request)
		dbaccess.Rollback(waitCtx, waiter)
		done <- err
	}()
	if err := testdbguard.WaitBlocked(ctx, db, int32(waiter.Conn().PgConn().PID()), int32(holder.Conn().PgConn().PID())); err != nil {
		t.Fatal(err)
	}
	stop()
	if err := <-done; err == nil {
		t.Fatal("cancelled lock wait succeeded")
	}
	if err := holder.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM durable_job_replay_requests`, 2)
	// A failed publication rolls back even a newly prepared job.
	rollback, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	request.BlockHash = testHash(88002)
	request.BlockNumber = 2
	if _, err := queue.PreparePublicationTx(ctx, rollback, request); err != nil {
		t.Fatal(err)
	}
	if err := rollback.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM durable_jobs WHERE stage='proxy'`, 1)
}

func TestVerificationPublicationRechecksEvidenceAfterWaiting(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	core, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	genesis := testBundle(0, testHash(88100), testHash(0), testHash(89100), "publication-stale-genesis")
	block := testBundle(1, testHash(88101), genesis.Block.Hash(), testHash(89101), "publication-stale-block")
	commitCanonical(t, ctx, core, genesis)
	commitCanonical(t, ctx, core, block)
	address := testAddress(881)
	runtime := []byte{0x60, 0x01}
	hash := keccak256(runtime)
	execFixture(t, ctx, db, `INSERT INTO contract_code_observations(chain_id,address,block_number,block_hash,code_hash,code,canonical) VALUES(1,$1,1,$2,$3,$4,true)`, address.Bytes(), block.Block.Hash().Bytes(), hash, runtime)
	generation, compilerDigest, executorDigest := insertVerifierV2Compiler(t, ctx, db)
	repository, err := verify.NewPostgresRepository(db, verify.RepositoryOptions{MaxRequestBytes: 1 << 20, MaxResultBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = repository.SubmitV2(ctx, verifierV2AddressSubmission(address.Bytes(), hash, block.Block.Hash().Bytes(), runtime))
	if err != nil {
		t.Fatal(err)
	}
	lease, found, err := repository.Claim(ctx, "stale-publication", time.Minute)
	if err != nil || !found {
		t.Fatalf("claim: found=%t err=%v", found, err)
	}
	if err := repository.BindCompiler(ctx, lease, solcJSProvenance(generation, compilerDigest, executorDigest)); err != nil {
		t.Fatal(err)
	}
	outcome := verifierV2SuccessOutcome(t, "full")
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	request := enrich.EnqueueRequest{ChainID: "1", Stage: enrich.ProxyStage, BlockHash: block.Block.Hash(), BlockNumber: 1}
	publisher, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer dbaccess.Rollback(ctx, publisher)
	if _, err := queue.PreparePublicationTx(ctx, publisher, request); err != nil {
		t.Fatal(err)
	}
	observed := &observedVerificationDatabase{Database: db, begun: make(chan int32, 1)}
	waitingRepository, err := verify.NewPostgresRepository(observed, verify.RepositoryOptions{MaxRequestBytes: 1 << 20, MaxResultBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- waitingRepository.CompleteV2(ctx, lease, "verification_success", outcome) }()
	var pid int32
	select {
	case pid = <-observed.begun:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := testdbguard.WaitBlocked(ctx, db, pid, int32(publisher.Conn().PgConn().PID())); err != nil {
		t.Fatal(err)
	}
	if _, err := publisher.Exec(ctx, `UPDATE contract_code_observations SET canonical=false WHERE chain_id=1 AND address=$1`, address.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := publisher.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, verify.ErrTargetNotCanonical) {
		t.Fatalf("stale evidence: %v", err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM verification_results`, 0)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM verified_contracts`, 0)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM proxy_replay_targets`, 0)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM durable_job_replay_requests`, 0)
	// A missing observation at a different known block creates a new proxy job
	// during preparation, but failed evidence validation must roll it back.
	_, _, err = repository.SubmitV2(ctx, verifierV2AddressSubmission(address.Bytes(), hash, genesis.Block.Hash().Bytes(), runtime))
	if err != nil {
		t.Fatal(err)
	}
	secondLease, found, err := repository.Claim(ctx, "stale-new-publication", time.Minute)
	if err != nil || !found {
		t.Fatalf("claim second: found=%t err=%v", found, err)
	}
	if err := repository.BindCompiler(ctx, secondLease, solcJSProvenance(generation, compilerDigest, executorDigest)); err != nil {
		t.Fatal(err)
	}
	if err := repository.CompleteV2(ctx, secondLease, "verification_success", outcome); !errors.Is(err, verify.ErrTargetNotCanonical) {
		t.Fatalf("stale new-job evidence: %v", err)
	}
	assertRowCount(t, ctx, db, `SELECT count(*) FROM durable_jobs WHERE stage='proxy' AND payload->>'block_hash'=$1`, 0, genesis.Block.Hash().Hex())
}
