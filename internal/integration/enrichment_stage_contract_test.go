//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/islishude/etherview/internal/enrich"
	"github.com/islishude/etherview/internal/store"
)

func TestEnrichmentOutboxCrashRecoveryReplayAndIdempotency(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	bundle := testBundle(0, testHash(60_000), testHash(0), testHash(61_000), "stage-contract")
	commitCanonical(t, ctx, repository, bundle)
	reference := mustBlockRef(t, bundle)
	blockHash, err := enrich.ParseWord(reference.Hash.String())
	if err != nil {
		t.Fatal(err)
	}

	stage := enrich.StageID{Name: "stage-contract", Version: 1}
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := enrich.NewOutboxDispatcher(db, queue, enrich.OutboxDispatcherOptions{Stages: []enrich.StageID{stage}})
	if err != nil {
		t.Fatal(err)
	}
	dispatched, err := dispatcher.DispatchOne(ctx)
	if err != nil || dispatched.State != enrich.OutboxPublished {
		t.Fatalf("dispatch = %+v, err=%v", dispatched, err)
	}
	job := readEnrichmentJob(t, ctx, db, stage, blockHash, reference.Number)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM durable_jobs WHERE id = $1`, 1, job.ID)
	assertRowCount(t, ctx, db, `SELECT count(*) FROM transactional_outbox WHERE published_at IS NOT NULL`, 1)

	crashedLease, found, err := queue.Claim(ctx, "crashed-worker", []enrich.StageID{stage}, time.Minute)
	if err != nil || !found || crashedLease.Job.ID != job.ID || crashedLease.Job.Attempt != 1 {
		t.Fatalf("crashed lease = %+v, found=%t, err=%v", crashedLease, found, err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE durable_jobs
		SET lease_expires_at = clock_timestamp() - INTERVAL '1 second'
		WHERE id = $1`, job.ID); err != nil {
		t.Fatalf("expire crashed lease: %v", err)
	}

	var runs atomic.Int32
	processor := enrich.ProcessorFunc{ID: stage, Fn: func(context.Context, enrich.Job) (enrich.StageResult, error) {
		run := runs.Add(1)
		return enrich.StageResult{Details: map[string]string{"run": strconv.Itoa(int(run))}}, nil
	}}
	worker, err := enrich.NewWorker(queue, []enrich.Processor{processor}, enrich.WorkerOptions{
		ID: "recovery-worker", LeaseDuration: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := worker.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("recover expired lease processed=%t err=%v", processed, err)
	}
	assertEnrichmentJobTerminal(t, ctx, db, job.ID, "succeeded", 2)
	assertStageResult(t, ctx, db, job, enrich.ResultComplete, "", map[string]string{"run": "1"})

	if err := queue.Finish(ctx, crashedLease, enrich.StageResult{State: enrich.ResultFailed, Error: "stale worker"}); !errors.Is(err, enrich.ErrLeaseLost) {
		t.Fatalf("stale worker finish error = %v, want ErrLeaseLost", err)
	}
	assertStageResult(t, ctx, db, job, enrich.ResultComplete, "", map[string]string{"run": "1"})

	if err := queue.Requeue(ctx, job); err != nil {
		t.Fatalf("requeue terminal stage: %v", err)
	}
	assertRowCount(t, ctx, db, `
		SELECT count(*) FROM block_stage_results
		WHERE chain_id = 1 AND block_hash = $1 AND stage = $2 AND stage_version = $3`,
		0, blockHash[:], stage.Name, stage.Version)
	assertJobState(t, ctx, db, job.ID, jobState{Status: "queued", Attempts: 0})

	processed, err = worker.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("replay processed=%t err=%v", processed, err)
	}
	assertEnrichmentJobTerminal(t, ctx, db, job.ID, "succeeded", 1)
	assertStageResult(t, ctx, db, job, enrich.ResultComplete, "", map[string]string{"run": "2"})
	assertRowCount(t, ctx, db, `SELECT count(*) FROM durable_jobs WHERE id = $1`, 1, job.ID)

	duplicate, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
		Stage: stage, ChainID: job.ChainID, BlockHash: job.BlockHash, BlockNumber: job.BlockNumber,
	})
	if err != nil || duplicate.Created || duplicate.Job.ID != job.ID {
		t.Fatalf("duplicate enqueue = %+v, err=%v, want existing job %s", duplicate, err, job.ID)
	}
	idle, err := dispatcher.DispatchOne(ctx)
	if err != nil || idle.State != enrich.OutboxIdle {
		t.Fatalf("second outbox dispatch = %+v, err=%v", idle, err)
	}
}

func TestEnrichmentTerminalOutcomesAndExhaustionAreDurable(t *testing.T) {
	db := newMigratedPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	repository, err := store.NewPostgresRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	bundle := testBundle(0, testHash(62_000), testHash(0), testHash(63_000), "stage-terminals")
	commitCanonical(t, ctx, repository, bundle)
	reference := mustBlockRef(t, bundle)
	blockHash, err := enrich.ParseWord(reference.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	queue, err := enrich.NewPostgresJobQueue(db)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name      string
		stage     enrich.StageID
		process   func(context.Context, enrich.Job) (enrich.StageResult, error)
		wantState enrich.ResultState
		wantError string
	}{
		{
			name: "unavailable", stage: enrich.StageID{Name: "contract-unavailable", Version: 1},
			process: func(context.Context, enrich.Job) (enrich.StageResult, error) {
				return enrich.StageResult{}, enrich.Unavailable(errors.New("fixture capability unavailable"))
			},
			wantState: enrich.ResultUnavailable, wantError: "fixture capability unavailable",
		},
		{
			name: "failed", stage: enrich.StageID{Name: "contract-failed", Version: 1},
			process: func(context.Context, enrich.Job) (enrich.StageResult, error) {
				return enrich.StageResult{}, enrich.Permanent(errors.New("fixture invalid input"))
			},
			wantState: enrich.ResultFailed, wantError: "fixture invalid input",
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			enqueued, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
				Stage: test.stage, ChainID: "1", BlockHash: blockHash, BlockNumber: reference.Number,
			})
			if err != nil || !enqueued.Created {
				t.Fatalf("enqueue = %+v, err=%v", enqueued, err)
			}
			worker, err := enrich.NewWorker(queue, []enrich.Processor{
				enrich.ProcessorFunc{ID: test.stage, Fn: test.process},
			}, enrich.WorkerOptions{ID: "terminal-" + test.name, LeaseDuration: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			processed, err := worker.ProcessOne(ctx)
			if err != nil || !processed {
				t.Fatalf("process terminal job=%t err=%v", processed, err)
			}
			assertEnrichmentJobTerminal(t, ctx, db, enqueued.Job.ID, "failed", 1)
			assertStageResult(t, ctx, db, enqueued.Job, test.wantState, test.wantError, map[string]string{})
		})
	}

	t.Run("retry-exhaustion", func(t *testing.T) {
		stage := enrich.StageID{Name: "contract-retry", Version: 1}
		enqueued, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
			Stage: stage, ChainID: "1", BlockHash: blockHash, BlockNumber: reference.Number, MaxAttempts: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		lease, found, err := queue.Claim(ctx, "retry-worker", []enrich.StageID{stage}, time.Minute)
		if err != nil || !found {
			t.Fatalf("claim retry job = %+v, found=%t, err=%v", lease, found, err)
		}
		if err := queue.Retry(ctx, lease, enrich.Retry{Reason: "fixture retry exhausted"}); err != nil {
			t.Fatalf("exhaust retry: %v", err)
		}
		assertEnrichmentJobTerminal(t, ctx, db, enqueued.Job.ID, "failed", 1)
		assertStageResult(t, ctx, db, enqueued.Job, enrich.ResultFailed, "fixture retry exhausted", map[string]string{})
	})

	t.Run("crashed-exhausted-lease", func(t *testing.T) {
		stage := enrich.StageID{Name: "contract-crash", Version: 1}
		enqueued, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
			Stage: stage, ChainID: "1", BlockHash: blockHash, BlockNumber: reference.Number, MaxAttempts: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		lease, found, err := queue.Claim(ctx, "crashed-terminal-worker", []enrich.StageID{stage}, time.Minute)
		if err != nil || !found {
			t.Fatalf("claim crash job = %+v, found=%t, err=%v", lease, found, err)
		}
		if _, err := db.ExecContext(ctx, `
			UPDATE durable_jobs
			SET lease_expires_at = clock_timestamp() - INTERVAL '1 second'
			WHERE id = $1`, enqueued.Job.ID); err != nil {
			t.Fatal(err)
		}
		if next, found, err := queue.Claim(ctx, "replacement-worker", []enrich.StageID{stage}, time.Minute); err != nil || found {
			t.Fatalf("claim exhausted crash = %+v, found=%t, err=%v", next, found, err)
		}
		assertEnrichmentJobTerminal(t, ctx, db, enqueued.Job.ID, "failed", 1)
		assertStageResult(t, ctx, db, enqueued.Job, enrich.ResultFailed, "maximum attempts exhausted", map[string]string{})
	})

	t.Run("terminal-transaction-rolls-back", func(t *testing.T) {
		stage := enrich.StageID{Name: "contract-atomic", Version: 1}
		missingHash, err := enrich.ParseWord(testHash(62_999).String())
		if err != nil {
			t.Fatal(err)
		}
		enqueued, err := queue.Enqueue(ctx, enrich.EnqueueRequest{
			Stage: stage, ChainID: "1", BlockHash: missingHash, BlockNumber: 99,
		})
		if err != nil {
			t.Fatal(err)
		}
		lease, found, err := queue.Claim(ctx, "atomic-worker", []enrich.StageID{stage}, time.Minute)
		if err != nil || !found {
			t.Fatalf("claim atomic job = %+v, found=%t, err=%v", lease, found, err)
		}
		finishErr := queue.Finish(ctx, lease, enrich.StageResult{
			State: enrich.ResultUnavailable, Error: "fixture unavailable",
		})
		if finishErr == nil {
			t.Fatal("terminal job without a referenced block unexpectedly committed")
		}
		state := readJobState(t, ctx, db, enqueued.Job.ID)
		if state.Status != "leased" || state.Attempts != 1 || !state.LeaseToken.Valid || state.LeaseToken.String != lease.Token || state.Result.Valid {
			t.Fatalf("rolled-back terminal job = %+v, want original owned lease", state)
		}
		assertRowCount(t, ctx, db, `
			SELECT count(*) FROM block_stage_results
			WHERE chain_id = 1 AND block_hash = $1 AND stage = $2 AND stage_version = $3`,
			0, missingHash[:], stage.Name, stage.Version)
	})
}

func readEnrichmentJob(t *testing.T, ctx context.Context, db *sql.DB, stage enrich.StageID, blockHash enrich.Word, blockNumber uint64) enrich.Job {
	t.Helper()
	var id int64
	if err := db.QueryRowContext(ctx, `
		SELECT id
		FROM durable_jobs
		WHERE chain_id = 1 AND stage = $1 AND stage_version = $2`, stage.Name, stage.Version).Scan(&id); err != nil {
		t.Fatalf("read enrichment job: %v", err)
	}
	return enrich.Job{
		ID: strconv.FormatInt(id, 10), Stage: stage, ChainID: "1", BlockHash: blockHash, BlockNumber: blockNumber,
	}
}

func assertEnrichmentJobTerminal(t *testing.T, ctx context.Context, db *sql.DB, id, wantStatus string, wantAttempts int) {
	t.Helper()
	got := readJobState(t, ctx, db, id)
	if got.Status != wantStatus || got.Attempts != wantAttempts || got.LeasedBy.Valid || got.LeaseToken.Valid || !got.Result.Valid {
		t.Fatalf("terminal enrichment job %s = %+v, want status=%s attempts=%d with result and no lease", id, got, wantStatus, wantAttempts)
	}
}

func assertStageResult(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	job enrich.Job,
	wantState enrich.ResultState,
	wantError string,
	wantDetails map[string]string,
) {
	t.Helper()
	var state string
	var details []byte
	var lastError sql.NullString
	if err := db.QueryRowContext(ctx, `
		SELECT state, details, last_error
		FROM block_stage_results
		WHERE chain_id = $1::numeric AND block_hash = $2 AND stage = $3 AND stage_version = $4`,
		job.ChainID, job.BlockHash[:], job.Stage.Name, job.Stage.Version,
	).Scan(&state, &details, &lastError); err != nil {
		t.Fatalf("read block stage result: %v", err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(details, &decoded); err != nil {
		t.Fatalf("decode block stage details: %v", err)
	}
	if state != string(wantState) || lastError.String != wantError || lastError.Valid != (wantError != "") || !reflect.DeepEqual(decoded, wantDetails) {
		t.Fatalf("stage result state=%q details=%v error=%+v, want state=%q details=%v error=%q", state, decoded, lastError, wantState, wantDetails, wantError)
	}
}
