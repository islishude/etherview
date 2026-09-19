package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	testpgx "github.com/islishude/etherview/internal/testpgx"
	pgx "github.com/jackc/pgx/v5"
	pgconn "github.com/jackc/pgx/v5/pgconn"

	"github.com/ethereum/go-ethereum/common"
)

type fakeSQLBackend struct {
	query        func(string, []any) (pgx.Rows, error)
	exec         func(string, []any) (pgconn.CommandTag, error)
	begin        func()
	beginOptions func(pgx.TxOptions)
	commit       func() error
	rollback     func() error
}

type fakeSQLConn struct {
	pgx.Tx
	backend *fakeSQLBackend
	done    bool
}

func (connection *fakeSQLConn) BeginTx(_ context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	if connection.backend.beginOptions != nil {
		connection.backend.beginOptions(options)
	}
	if connection.backend.begin != nil {
		connection.backend.begin()
	}
	return &fakeSQLConn{backend: connection.backend}, nil
}
func (connection *fakeSQLConn) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	return testpgx.Row(connection.Query(ctx, query, args...))
}
func (connection *fakeSQLConn) Commit(context.Context) error {
	if connection.done {
		return pgx.ErrTxClosed
	}
	connection.done = true
	if connection.backend.commit != nil {
		return connection.backend.commit()
	}
	return nil
}
func (connection *fakeSQLConn) Rollback(context.Context) error {
	if connection.done {
		return pgx.ErrTxClosed
	}
	connection.done = true
	if connection.backend.rollback != nil {
		return connection.backend.rollback()
	}
	return nil
}
func openFakeSQLDB(t *testing.T, backend *fakeSQLBackend) *fakeSQLConn {
	t.Helper()
	return &fakeSQLConn{backend: backend}
}
func (connection *fakeSQLConn) Query(_ context.Context, query string, arguments ...any) (pgx.Rows, error) {
	if connection.backend.query == nil {
		return nil, errors.New("unexpected query")
	}
	return connection.backend.query(query, arguments)
}
func (connection *fakeSQLConn) Exec(_ context.Context, query string, arguments ...any) (pgconn.CommandTag, error) {
	if connection.backend.exec == nil {
		return pgconn.CommandTag{}, errors.New("unexpected exec")
	}
	return connection.backend.exec(query, arguments)
}
func durableJobRow(id, attempt int64, stage StageID, hash common.Hash, block uint64) pgx.Rows {
	payload, _ := json.Marshal(durableJobPayload{BlockHash: hash.String(), BlockNumber: fmt.Sprint(block)})
	return &testpgx.Rows{
		ColumnNames: []string{"id", "chain_id", "stage", "stage_version", "attempts", "max_attempts", "payload", "requested_generation"},
		ValuesList:  [][]any{{id, "1", stage.Name, int64(stage.Version), attempt, int64(10), payload, int64(1)}},
	}
}

func emptyJobRows() pgx.Rows {
	return &testpgx.Rows{ColumnNames: []string{"id", "chain_id", "stage", "stage_version", "attempts", "max_attempts", "payload", "requested_generation"}}
}

func emptyReplayTargetRows() pgx.Rows {
	return &testpgx.Rows{ColumnNames: []string{
		"id", "chain_id", "stage", "stage_version", "attempts", "max_attempts", "payload", "requested_generation", "status",
	}}
}

func replayTargetRow(id, attempt, generation int64, stage StageID, hash common.Hash, block uint64, status string) pgx.Rows {
	payload, _ := json.Marshal(durableJobPayload{BlockHash: hash.String(), BlockNumber: fmt.Sprint(block)})
	return &testpgx.Rows{
		ColumnNames: []string{"id", "chain_id", "stage", "stage_version", "attempts", "max_attempts", "payload", "requested_generation", "status"},
		ValuesList: [][]any{{
			id, "1", stage.Name, int64(stage.Version), attempt, int64(10), payload, generation, status,
		}},
	}
}

func TestPostgresEnqueueIsIdempotent(t *testing.T) {
	t.Parallel()
	stage := StageID{Name: "token", Version: 2}
	hash := uintWord(10)
	var mu sync.Mutex
	inserts := 0
	var storedPayload []byte
	backend := &fakeSQLBackend{query: func(query string, arguments []any) (pgx.Rows, error) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(query, "INSERT INTO durable_jobs"):
			if !strings.Contains(query, "ON CONFLICT (chain_id, kind, idempotency_key) DO NOTHING") {
				t.Errorf("enqueue lacks idempotent conflict handling:\n%s", query)
			}
			inserts++
			if inserts == 1 {
				storedPayload = arguments[5].([]byte)
				return &testpgx.Rows{
					ColumnNames: []string{"id", "chain_id", "stage", "stage_version", "attempts", "max_attempts", "payload", "requested_generation"},
					ValuesList:  [][]any{{int64(41), "1", stage.Name, int64(stage.Version), int64(0), int64(10), storedPayload, int64(1)}},
				}, nil
			}
			return emptyJobRows(), nil
		case strings.Contains(query, "FROM durable_jobs"):
			return &testpgx.Rows{
				ColumnNames: []string{"id", "chain_id", "stage", "stage_version", "attempts", "max_attempts", "payload", "requested_generation"},
				ValuesList:  [][]any{{int64(41), "1", stage.Name, int64(stage.Version), int64(0), int64(10), storedPayload, int64(1)}},
			}, nil
		default:
			return nil, fmt.Errorf("unexpected query: %s", query)
		}
	}}
	queue, err := NewPostgresJobQueue(openFakeSQLDB(t, backend))
	if err != nil {
		t.Fatal(err)
	}
	request := EnqueueRequest{Stage: stage, ChainID: "1", BlockHash: hash, BlockNumber: 99, Payload: json.RawMessage(`{"source":"receipt"}`)}
	first, err := queue.Enqueue(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := queue.Enqueue(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || second.Created || first.Job.ID != "41" || second.Job.ID != first.Job.ID || inserts != 2 {
		t.Fatalf("first=%+v second=%+v inserts=%d", first, second, inserts)
	}
}

func TestPostgresEnqueueRecordsInitialReplaySource(t *testing.T) {
	t.Parallel()
	stage := StageID{Name: "proxy", Version: 2}
	hash := uintWord(11)
	recorded := false
	backend := &fakeSQLBackend{
		query: func(query string, _ []any) (pgx.Rows, error) {
			switch {
			case strings.Contains(query, "FROM durable_jobs"):
				return emptyJobRows(), nil
			case strings.Contains(query, "INSERT INTO durable_jobs"):
				return durableJobRow(42, 0, stage, hash, 100), nil
			default:
				return nil, fmt.Errorf("unexpected query: %s", query)
			}
		},
		exec: func(query string, arguments []any) (pgconn.CommandTag, error) {
			if !strings.Contains(query, "INSERT INTO durable_job_replay_requests") {
				return pgconn.CommandTag{}, fmt.Errorf("unexpected exec: %s", query)
			}
			if len(arguments) != 4 || arguments[0] != int64(42) ||
				arguments[1] != "verification-publication" ||
				arguments[2] != "verification-job" || arguments[3] != int64(1) {
				t.Fatalf("initial replay source arguments = %+v", arguments)
			}
			recorded = true
			return testpgx.Affected(1), nil
		},
	}
	queue, err := NewPostgresJobQueue(openFakeSQLDB(t, backend))
	if err != nil {
		t.Fatal(err)
	}
	result, err := queue.Enqueue(t.Context(), EnqueueRequest{
		Stage: stage, ChainID: "1", BlockHash: hash, BlockNumber: 100,
		Replay: ReplaySource{Kind: "verification-publication", Key: "verification-job"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || result.Replayed || result.Job.Generation != 1 || !recorded {
		t.Fatalf("initial replay enqueue = %+v recorded=%t", result, recorded)
	}
}

func TestPostgresClaimUsesAdvisoryFirstRevalidationAndConcurrentTokens(t *testing.T) {
	t.Parallel()
	stage := StageID{Name: "trace", Version: 3}
	const jobs = 24
	var mu sync.Mutex
	next := int64(1)
	seenCandidate, seenCAS := false, false
	backend := &fakeSQLBackend{query: func(query string, arguments []any) (pgx.Rows, error) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(query, "SELECT exhausted_job.id"):
			return &testpgx.Rows{ColumnNames: []string{"id"}}, nil
		case strings.Contains(query, "SELECT candidate_job.id"):
			seenCandidate = true
			if !strings.Contains(query, "kind = 'enrichment'") || strings.Contains(query, "FOR UPDATE") ||
				!strings.Contains(query, "lease_expires_at <= clock_timestamp()") {
				t.Errorf("candidate selection is not no-lock/identity-bound:\n%s", query)
			}
			if next > jobs {
				return &testpgx.Rows{ColumnNames: []string{"id"}}, nil
			}
			id := next
			next++
			return durableJobRow(id, 0, stage, uintWord(uint64(id)), uint64(id)), nil
		case strings.Contains(query, "UPDATE durable_jobs AS job") && strings.Contains(query, "RETURNING job.id"):
			seenCAS = true
			if !strings.Contains(query, "job.kind = 'enrichment'") || !strings.Contains(query, "job.id = $4") {
				t.Errorf("claim CAS lacks exact identity:\n%s", query)
			}
			id := arguments[3].(int64)
			return durableJobRow(id, 1, stage, uintWord(uint64(id)), uint64(id)), nil
		default:
			return nil, fmt.Errorf("unexpected query: %s", query)
		}
	}, exec: func(query string, _ []any) (pgconn.CommandTag, error) {
		if isPublicationControlSQL(query) {
			return testpgx.Affected(1), nil
		}
		return pgconn.CommandTag{}, fmt.Errorf("unexpected exec: %s", query)
	}}
	queue, err := NewPostgresJobQueue(openFakeSQLDB(t, backend))
	if err != nil {
		t.Fatal(err)
	}
	tokens := make(chan string, jobs)
	ids := make(chan string, jobs)
	var group sync.WaitGroup
	for range jobs {
		group.Go(func() {
			lease, found, claimErr := queue.Claim(context.Background(), "worker", []StageID{stage}, time.Minute)
			if claimErr != nil || !found {
				t.Errorf("claim found=%v err=%v", found, claimErr)
				return
			}
			tokens <- lease.Token
			ids <- lease.Job.ID
		})
	}
	group.Wait()
	close(tokens)
	close(ids)
	uniqueTokens := make(map[string]bool)
	for token := range tokens {
		if len(token) != 43 || uniqueTokens[token] {
			t.Fatalf("invalid or duplicate lease token %q", token)
		}
		uniqueTokens[token] = true
	}
	uniqueIDs := make(map[string]bool)
	for id := range ids {
		uniqueIDs[id] = true
	}
	if !seenCandidate || !seenCAS || len(uniqueTokens) != jobs || len(uniqueIDs) != jobs {
		t.Fatalf("candidate=%v cas=%v tokens=%d ids=%d", seenCandidate, seenCAS, len(uniqueTokens), len(uniqueIDs))
	}
}

func TestPostgresExpiredLeaseCanBeReclaimedWithNewToken(t *testing.T) {
	t.Parallel()
	stage := StageID{Name: "abi", Version: 1}
	var mu sync.Mutex
	attempt := int64(0)
	backend := &fakeSQLBackend{query: func(query string, arguments []any) (pgx.Rows, error) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(query, "SELECT exhausted_job.id"):
			return &testpgx.Rows{ColumnNames: []string{"id"}}, nil
		case strings.Contains(query, "SELECT candidate_job.id"):
			if !strings.Contains(query, "lease_expires_at <= clock_timestamp()") {
				t.Errorf("claim does not select expired leases")
			}
			return durableJobRow(7, attempt, stage, uintWord(7), 7), nil
		case strings.Contains(query, "UPDATE durable_jobs AS job"):
			attempt++
			return durableJobRow(arguments[3].(int64), attempt, stage, uintWord(7), 7), nil
		default:
			return nil, fmt.Errorf("unexpected query: %s", query)
		}
	}, exec: func(query string, _ []any) (pgconn.CommandTag, error) {
		if isPublicationControlSQL(query) {
			return testpgx.Affected(1), nil
		}
		return pgconn.CommandTag{}, fmt.Errorf("unexpected exec: %s", query)
	}}
	queue, _ := NewPostgresJobQueue(openFakeSQLDB(t, backend))
	first, found, err := queue.Claim(context.Background(), "worker-a", []StageID{stage}, time.Second)
	if err != nil || !found {
		t.Fatalf("first found=%v err=%v", found, err)
	}
	second, found, err := queue.Claim(context.Background(), "worker-b", []StageID{stage}, time.Second)
	if err != nil || !found {
		t.Fatalf("second found=%v err=%v", found, err)
	}
	if first.Job.ID != second.Job.ID || first.Token == second.Token || second.Job.Attempt != 2 {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}

func TestPostgresClaimContentionBudgetReturnsIdle(t *testing.T) {
	t.Parallel()
	stage := StageID{Name: "fixture-contention", Version: 1}
	backend := &fakeSQLBackend{
		query: func(query string, _ []any) (pgx.Rows, error) {
			switch {
			case strings.Contains(query, "SELECT exhausted_job.id"):
				return &testpgx.Rows{ColumnNames: []string{"id"}}, nil
			case strings.Contains(query, "SELECT candidate_job.id"):
				return durableJobRow(44, 0, stage, uintWord(44), 44), nil
			case strings.Contains(query, "UPDATE durable_jobs AS job"):
				return emptyJobRows(), nil
			default:
				return nil, fmt.Errorf("unexpected query: %s", query)
			}
		},
		exec: func(query string, _ []any) (pgconn.CommandTag, error) {
			if isPublicationControlSQL(query) {
				return testpgx.Affected(1), nil
			}
			return pgconn.CommandTag{}, fmt.Errorf("unexpected exec: %s", query)
		},
	}
	queue, _ := NewPostgresJobQueue(openFakeSQLDB(t, backend))
	lease, found, err := queue.Claim(t.Context(), "contention-worker", []StageID{stage}, time.Second)
	if err != nil || found || lease.Token != "" {
		t.Fatalf("contention claim=%+v found=%t err=%v, want transient idle", lease, found, err)
	}
}

func TestLeaseMutationsBindExactEnrichmentJobIdentity(t *testing.T) {
	t.Parallel()
	for name, query := range map[string]string{
		"renew":               testpgx.Statement("EnrichLegacyRenewJob"),
		"finish":              testpgx.Statement("EnrichLegacyFinishJob"),
		"retry":               testpgx.Statement("EnrichLegacyRetryJob"),
		"publish success":     testpgx.Statement("EnrichLegacyAtomicPublishSuccess"),
		"consume replay":      testpgx.Statement("EnrichLegacyAtomicConsumePendingReplay"),
		"claim":               testpgx.Statement("EnrichClaimCandidate"),
		"terminal exhaustion": testpgx.Statement("EnrichLegacyTerminalizeExhaustedJob"),
	} {
		for _, fragment := range []string{
			"kind = 'enrichment'", "chain_id =", "stage =", "stage_version =",
			"payload->>'block_hash'", "payload->>'block_number'",
		} {
			if !strings.Contains(query, fragment) {
				t.Errorf("%s SQL lacks %q:\n%s", name, fragment, query)
			}
		}
	}
}

func TestClaimQueriesUseCanonicalStageKeys(t *testing.T) {
	t.Parallel()
	for name, query := range map[string]string{
		"claim candidate":     testpgx.Statement("EnrichSelectClaimCandidate"),
		"claim update":        testpgx.Statement("EnrichClaimCandidate"),
		"exhausted candidate": testpgx.Statement("EnrichSelectExhaustedCandidate"),
		"exhausted lock":      testpgx.Statement("EnrichLockExhaustedJob"),
	} {
		if !strings.Contains(query, "stage || '@' ||") {
			t.Errorf("%s query does not use StageID.String canonical keys:\n%s", name, query)
		}
	}
}

func TestPostgresLeaseMutationsAreTokenAndExpiryConditional(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var finishResults []map[string]any
	var finishStatuses []string
	var finishErrors []any
	var stageStates []string
	var retryReason string
	backend := &fakeSQLBackend{
		exec: func(query string, arguments []any) (pgconn.CommandTag, error) {
			mu.Lock()
			defer mu.Unlock()
			if isPublicationControlSQL(query) {
				return testpgx.Affected(1), nil
			}
			if !strings.Contains(query, "lease_token = $8") || !strings.Contains(query, "lease_expires_at > clock_timestamp()") ||
				!strings.Contains(query, "kind = 'enrichment'") || !strings.Contains(query, "payload->>'block_hash'") {
				t.Errorf("mutation lacks token/expiry predicate:\n%s", query)
			}
			if !testpgx.TextPointerEquals(arguments[7], "owned-token") {
				return testpgx.Affected(0), nil
			}
			if strings.Contains(query, "result = $4::jsonb") {
				var persisted map[string]any
				if err := json.Unmarshal(arguments[3].([]byte), &persisted); err != nil {
					t.Errorf("decode result: %v", err)
				}
				finishResults = append(finishResults, persisted)
				finishStatuses = append(finishStatuses, arguments[2].(string))
				finishErrors = append(finishErrors, arguments[4])
			}
			return testpgx.Affected(1), nil
		},
		query: func(query string, arguments []any) (pgx.Rows, error) {
			mu.Lock()
			defer mu.Unlock()
			if strings.Contains(query, "INSERT INTO block_stage_results") {
				stageStates = append(stageStates, arguments[5].(string))
				if *arguments[8].(*int64) != int64(9) || *arguments[9].(*int64) != int64(1) {
					t.Errorf("stage result lacks exact job/generation identity: args=%+v", arguments)
				}
				return &testpgx.Rows{ColumnNames: []string{"inserted"}, ValuesList: [][]any{{int64(1)}}}, nil
			}
			if strings.Contains(query, "completed_generation = GREATEST") && strings.Contains(query, "RETURNING status = 'queued'") {
				if !testpgx.TextPointerEquals(arguments[9], "owned-token") {
					return &testpgx.Rows{ColumnNames: []string{"replay_pending"}}, nil
				}
				var persisted map[string]any
				if err := json.Unmarshal(arguments[1].([]byte), &persisted); err != nil {
					t.Errorf("decode result: %v", err)
				}
				finishResults = append(finishResults, persisted)
				finishStatuses = append(finishStatuses, arguments[0].(string))
				finishErrors = append(finishErrors, arguments[2])
				return &testpgx.Rows{ColumnNames: []string{"replay_pending"}, ValuesList: [][]any{{false}}}, nil
			}
			if !strings.Contains(query, "available_at = CASE") || !strings.Contains(query, "RETURNING status") {
				return nil, fmt.Errorf("unexpected query: %s", query)
			}
			if !strings.Contains(query, "WHEN attempts >= max_attempts THEN 'failed'") {
				t.Errorf("retry does not terminally fail an exhausted job:\n%s", query)
			}
			if !testpgx.TextPointerEquals(arguments[8], "owned-token") {
				return &testpgx.Rows{ColumnNames: []string{"status", "replay_pending"}}, nil
			}
			retryReason = *arguments[1].(*string)
			return &testpgx.Rows{ColumnNames: []string{"status", "replay_pending"}, ValuesList: [][]any{{"queued", false}}}, nil
		},
	}
	queue, _ := NewPostgresJobQueue(openFakeSQLDB(t, backend))
	job := Job{ID: "9", Stage: StageID{Name: "fixture", Version: 1}, ChainID: "1", BlockHash: uintWord(9), BlockNumber: 9, Attempt: 1, Generation: 1}
	owned := Lease{Job: job, Token: "owned-token"}
	if err := queue.Renew(context.Background(), owned, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := queue.Finish(context.Background(), owned, StageResult{State: ResultComplete, Details: map[string]string{"events": "2"}}); err != nil {
		t.Fatal(err)
	}
	if err := queue.Finish(context.Background(), owned, StageResult{State: ResultUnavailable, Error: "trace RPC disabled"}); err != nil {
		t.Fatal(err)
	}
	if len(finishResults) != 2 || finishResults[0]["state"] != string(ResultComplete) || finishResults[0]["details"].(map[string]any)["events"] != "2" {
		t.Fatalf("persisted results=%+v", finishResults)
	}
	if finishStatuses[0] != "succeeded" || finishErrors[0] != (*string)(nil) || finishStatuses[1] != "failed" || !testpgx.TextPointerEquals(finishErrors[1], "trace RPC disabled") || finishResults[1]["state"] != string(ResultUnavailable) {
		t.Fatalf("statuses=%v errors=%v results=%+v", finishStatuses, finishErrors, finishResults)
	}
	if strings.Join(stageStates, ",") != "complete,unavailable" {
		t.Fatalf("stage states=%v", stageStates)
	}
	if err := queue.Retry(context.Background(), owned, Retry{Reason: "RPC unavailable", After: 1500 * time.Microsecond}); err != nil {
		t.Fatal(err)
	}
	if retryReason != "RPC unavailable" {
		t.Fatalf("retry reason=%q", retryReason)
	}
	lost := Lease{Job: job, Token: "expired-or-replaced"}
	if err := queue.Renew(context.Background(), lost, time.Second); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("renew err=%v", err)
	}
	if err := queue.Finish(context.Background(), lost, StageResult{State: ResultFailed, Error: "bad data"}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("finish err=%v", err)
	}
	if err := queue.Retry(context.Background(), lost, Retry{Reason: "retry", After: 0}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("retry err=%v", err)
	}
}

func TestPostgresRequeueResetsOnlyAnUnleasedMatchingJob(t *testing.T) {
	t.Parallel()
	var sawRequeue, sawStageClear bool
	backend := &fakeSQLBackend{query: func(query string, _ []any) (pgx.Rows, error) {
		if strings.Contains(query, "SELECT durable_job_id, job_generation") {
			return &testpgx.Rows{ColumnNames: []string{"durable_job_id", "job_generation"}}, nil
		}
		return nil, fmt.Errorf("unexpected query: %s", query)
	}, exec: func(query string, arguments []any) (pgconn.CommandTag, error) {
		if isPublicationControlSQL(query) {
			return testpgx.Affected(1), nil
		}
		if strings.Contains(query, "DELETE FROM block_stage_results") {
			sawStageClear = true
			if len(arguments) != 4 || !testpgx.NumericEquals(arguments[0], "1") || arguments[2] != "token" || arguments[3] != int32(1) {
				t.Fatalf("stage clear arguments=%+v", arguments)
			}
			return testpgx.Affected(1), nil
		}
		if strings.Contains(query, "DELETE FROM block_journals") {
			return testpgx.Affected(1), nil
		}
		sawRequeue = true
		for _, fragment := range []string{
			"attempts = 0",
			"result = NULL",
			"last_error = NULL",
			"kind = 'enrichment'",
			"idempotency_key = $5",
			"status IN ('succeeded', 'failed')",
		} {
			if !strings.Contains(query, fragment) {
				t.Errorf("requeue SQL lacks %q:\n%s", fragment, query)
			}
		}
		if len(arguments) != 5 || arguments[0] != int64(17) || !testpgx.NumericEquals(arguments[1], "1") || arguments[2] != "token" || arguments[3] != int32(1) {
			t.Fatalf("requeue arguments=%+v", arguments)
		}
		return testpgx.Affected(1), nil
	}}
	queue, _ := NewPostgresJobQueue(openFakeSQLDB(t, backend))
	job := Job{ID: "17", Stage: StageID{Name: "token", Version: 1}, ChainID: "1", BlockHash: uintWord(17), BlockNumber: 17, Generation: 1}
	if err := queue.Requeue(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if !sawRequeue || !sawStageClear {
		t.Fatalf("requeue=%t stageClear=%t", sawRequeue, sawStageClear)
	}
}

func TestPostgresRequeueDoesNotStealActiveLease(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"queued", "leased"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			backend := &fakeSQLBackend{
				exec: func(query string, _ []any) (pgconn.CommandTag, error) {
					if isPublicationControlSQL(query) {
						return testpgx.Affected(1), nil
					}
					return testpgx.Affected(0), nil
				},
				query: func(query string, arguments []any) (pgx.Rows, error) {
					if !strings.Contains(query, "SELECT status") || len(arguments) != 1 || arguments[0] != int64(19) {
						t.Fatalf("status query=%q arguments=%+v", query, arguments)
					}
					return &testpgx.Rows{ColumnNames: []string{"status"}, ValuesList: [][]any{{status}}}, nil
				},
			}
			queue, _ := NewPostgresJobQueue(openFakeSQLDB(t, backend))
			job := Job{ID: "19", Stage: StageID{Name: "stats", Version: 1}, ChainID: "1", BlockHash: uintWord(19), BlockNumber: 19, Generation: 1}
			if err := queue.Requeue(context.Background(), job); !errors.Is(err, ErrJobBusy) {
				t.Fatalf("requeue %s job err=%v", status, err)
			}
		})
	}
}

func TestPostgresRequeueRejectsNonCanonicalJobID(t *testing.T) {
	t.Parallel()
	queue, _ := NewPostgresJobQueue(openFakeSQLDB(t, &fakeSQLBackend{}))
	job := Job{ID: "017", Stage: StageID{Name: "token", Version: 1}, ChainID: "1", BlockHash: uintWord(17), BlockNumber: 17}
	if err := queue.Requeue(context.Background(), job); err == nil {
		t.Fatal("accepted non-canonical durable job ID")
	}
}

func TestDependentReplayGenerationPersistsAcrossActiveLeaseWithoutStealing(t *testing.T) {
	t.Parallel()
	hash := uintWord(29)
	source := Job{
		ID: "29", Stage: ProxyStage, ChainID: "1", BlockHash: hash,
		BlockNumber: 29, Attempt: 1, Generation: 1,
	}
	requestedGeneration := int64(1)
	insertCalls, updateCalls, cleanupCalls := 0, 0, 0
	backend := &fakeSQLBackend{
		query: func(query string, _ []any) (pgx.Rows, error) {
			switch {
			case strings.Contains(query, "SELECT id") && !strings.Contains(query, "FOR UPDATE"):
				return &testpgx.Rows{ColumnNames: []string{"id"}, ValuesList: [][]any{{int64(30)}}}, nil
			case strings.Contains(query, "FROM durable_jobs") && strings.Contains(query, "FOR UPDATE"):
				return replayTargetRow(30, 1, requestedGeneration, ABIStage, hash, 29, "leased"), nil
			default:
				return nil, fmt.Errorf("unexpected query: %s", query)
			}
		},
		exec: func(query string, _ []any) (pgconn.CommandTag, error) {
			switch {
			case isPublicationControlSQL(query):
				return testpgx.Affected(1), nil
			case strings.Contains(query, "INSERT INTO durable_job_replay_requests"):
				insertCalls++
				if insertCalls == 1 {
					return testpgx.Affected(1), nil
				}
				return testpgx.Affected(0), nil
			case strings.Contains(query, "UPDATE durable_jobs"):
				updateCalls++
				if !strings.Contains(query, "CASE WHEN status = 'leased' THEN status") || strings.Contains(query, "status IN ('succeeded', 'failed')") {
					t.Errorf("active replay update can steal the lease:\n%s", query)
				}
				requestedGeneration++
				return testpgx.Affected(1), nil
			case strings.Contains(query, "DELETE FROM"):
				cleanupCalls++
				return testpgx.Affected(1), nil
			default:
				return pgconn.CommandTag{}, fmt.Errorf("unexpected exec: %s", query)
			}
		},
	}
	db := openFakeSQLDB(t, backend)
	for call, want := range []bool{true, false} {
		tx, err := db.BeginTx(context.Background(), pgx.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		got, err := requestDependentStageReplayTx(context.Background(), tx, source, ABIStage)
		if err != nil {
			_ = tx.Rollback(context.Background())
			t.Fatal(err)
		}
		if err := tx.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("request %d=%t want=%t", call+1, got, want)
		}
	}
	if insertCalls != 2 || updateCalls != 1 || cleanupCalls != 0 || requestedGeneration != 2 {
		t.Fatalf("insert=%d update=%d cleanup=%d generation=%d", insertCalls, updateCalls, cleanupCalls, requestedGeneration)
	}
}

func TestEnqueueValidationRejectsNonCanonicalIdentity(t *testing.T) {
	t.Parallel()
	queue, _ := NewPostgresJobQueue(openFakeSQLDB(t, &fakeSQLBackend{}))
	request := EnqueueRequest{Stage: StageID{Name: "token", Version: 1}, ChainID: "01", BlockHash: uintWord(1)}
	if _, err := queue.Enqueue(context.Background(), request); err == nil {
		t.Fatal("accepted non-canonical chain ID")
	}
	request.ChainID = "1"
	request.Kind = "foreign-worker-kind"
	if _, err := queue.Enqueue(context.Background(), request); err == nil || !strings.Contains(err.Error(), DefaultJobKind) {
		t.Fatalf("custom kind error=%v, want enrichment-only rejection", err)
	}
	request.Kind = ""
	request.Payload = json.RawMessage(`{`)
	if _, err := queue.Enqueue(context.Background(), request); err == nil {
		t.Fatal("accepted malformed payload JSON")
	}
}
