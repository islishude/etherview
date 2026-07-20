package enrich

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSQLBackend struct {
	query    func(string, []driver.NamedValue) (driver.Rows, error)
	exec     func(string, []driver.NamedValue) (driver.Result, error)
	begin    func()
	commit   func() error
	rollback func() error
}

type fakeSQLDriver struct{ backend *fakeSQLBackend }
type fakeSQLConn struct{ backend *fakeSQLBackend }
type fakeSQLTx struct{ backend *fakeSQLBackend }

func (driverValue *fakeSQLDriver) Open(string) (driver.Conn, error) {
	return &fakeSQLConn{backend: driverValue.backend}, nil
}

func (*fakeSQLConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*fakeSQLConn) Close() error                        { return nil }
func (connection *fakeSQLConn) Begin() (driver.Tx, error) {
	if connection.backend.begin != nil {
		connection.backend.begin()
	}
	return &fakeSQLTx{backend: connection.backend}, nil
}
func (connection *fakeSQLConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	if connection.backend.begin != nil {
		connection.backend.begin()
	}
	return &fakeSQLTx{backend: connection.backend}, nil
}

func (connection *fakeSQLConn) QueryContext(_ context.Context, query string, arguments []driver.NamedValue) (driver.Rows, error) {
	if connection.backend.query == nil {
		return nil, errors.New("unexpected query")
	}
	return connection.backend.query(query, arguments)
}

func (connection *fakeSQLConn) ExecContext(_ context.Context, query string, arguments []driver.NamedValue) (driver.Result, error) {
	if connection.backend.exec == nil {
		return nil, errors.New("unexpected exec")
	}
	return connection.backend.exec(query, arguments)
}

func (tx *fakeSQLTx) Commit() error {
	if tx.backend.commit != nil {
		return tx.backend.commit()
	}
	return nil
}

func (tx *fakeSQLTx) Rollback() error {
	if tx.backend.rollback != nil {
		return tx.backend.rollback()
	}
	return nil
}

type fakeSQLRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (rows *fakeSQLRows) Columns() []string { return rows.columns }
func (*fakeSQLRows) Close() error           { return nil }
func (rows *fakeSQLRows) Next(destination []driver.Value) error {
	if rows.index >= len(rows.values) {
		return io.EOF
	}
	copy(destination, rows.values[rows.index])
	rows.index++
	return nil
}

var fakeDriverSequence atomic.Uint64

func openFakeSQLDB(t *testing.T, backend *fakeSQLBackend) *sql.DB {
	t.Helper()
	name := fmt.Sprintf("etherview-enrich-fake-%d", fakeDriverSequence.Add(1))
	sql.Register(name, &fakeSQLDriver{backend: backend})
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(64)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func durableJobRow(id, attempt int64, stage StageID, hash Word, block uint64) driver.Rows {
	payload, _ := json.Marshal(durableJobPayload{BlockHash: hash.String(), BlockNumber: fmt.Sprint(block)})
	return &fakeSQLRows{
		columns: []string{"id", "chain_id", "stage", "stage_version", "attempts", "payload", "requested_generation"},
		values:  [][]driver.Value{{id, "1", stage.Name, int64(stage.Version), attempt, payload, int64(1)}},
	}
}

func emptyJobRows() driver.Rows {
	return &fakeSQLRows{columns: []string{"id", "chain_id", "stage", "stage_version", "attempts", "payload", "requested_generation"}}
}

func emptyReplayTargetRows() driver.Rows {
	return &fakeSQLRows{columns: []string{
		"id", "chain_id", "stage", "stage_version", "attempts", "payload", "requested_generation", "status",
	}}
}

func replayTargetRow(id, attempt, generation int64, stage StageID, hash Word, block uint64, status string) driver.Rows {
	payload, _ := json.Marshal(durableJobPayload{BlockHash: hash.String(), BlockNumber: fmt.Sprint(block)})
	return &fakeSQLRows{
		columns: []string{"id", "chain_id", "stage", "stage_version", "attempts", "payload", "requested_generation", "status"},
		values: [][]driver.Value{{
			id, "1", stage.Name, int64(stage.Version), attempt, payload, generation, status,
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
	backend := &fakeSQLBackend{query: func(query string, arguments []driver.NamedValue) (driver.Rows, error) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(query, "INSERT INTO durable_jobs"):
			if !strings.Contains(query, "ON CONFLICT (chain_id, kind, idempotency_key) DO NOTHING") {
				t.Errorf("enqueue lacks idempotent conflict handling:\n%s", query)
			}
			inserts++
			if inserts == 1 {
				storedPayload = []byte(arguments[5].Value.(string))
				return &fakeSQLRows{
					columns: []string{"id", "chain_id", "stage", "stage_version", "attempts", "payload", "requested_generation"},
					values:  [][]driver.Value{{int64(41), "1", stage.Name, int64(stage.Version), int64(0), storedPayload, int64(1)}},
				}, nil
			}
			return emptyJobRows(), nil
		case strings.Contains(query, "FROM durable_jobs"):
			return &fakeSQLRows{
				columns: []string{"id", "chain_id", "stage", "stage_version", "attempts", "payload", "requested_generation"},
				values:  [][]driver.Value{{int64(41), "1", stage.Name, int64(stage.Version), int64(0), storedPayload, int64(1)}},
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

func TestPostgresClaimUsesAdvisoryFirstRevalidationAndConcurrentTokens(t *testing.T) {
	t.Parallel()
	stage := StageID{Name: "trace", Version: 3}
	const jobs = 24
	var mu sync.Mutex
	next := int64(1)
	seenCandidate, seenCAS := false, false
	backend := &fakeSQLBackend{query: func(query string, arguments []driver.NamedValue) (driver.Rows, error) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(query, "SELECT exhausted_job.id"):
			return &fakeSQLRows{columns: []string{"id"}}, nil
		case strings.Contains(query, "SELECT candidate_job.id"):
			seenCandidate = true
			if !strings.Contains(query, "kind = 'enrichment'") || strings.Contains(query, "FOR UPDATE") ||
				!strings.Contains(query, "lease_expires_at <= clock_timestamp()") {
				t.Errorf("candidate selection is not no-lock/identity-bound:\n%s", query)
			}
			if next > jobs {
				return &fakeSQLRows{columns: []string{"id"}}, nil
			}
			id := next
			next++
			return durableJobRow(id, 0, stage, uintWord(uint64(id)), uint64(id)), nil
		case strings.Contains(query, "UPDATE durable_jobs AS job") && strings.Contains(query, "RETURNING job.id"):
			seenCAS = true
			if !strings.Contains(query, "job.kind = 'enrichment'") || !strings.Contains(query, "job.id = $4") {
				t.Errorf("claim CAS lacks exact identity:\n%s", query)
			}
			id := arguments[3].Value.(int64)
			return durableJobRow(id, 1, stage, uintWord(uint64(id)), uint64(id)), nil
		default:
			return nil, fmt.Errorf("unexpected query: %s", query)
		}
	}, exec: func(query string, _ []driver.NamedValue) (driver.Result, error) {
		if isPublicationControlSQL(query) {
			return driver.RowsAffected(1), nil
		}
		return nil, fmt.Errorf("unexpected exec: %s", query)
	}}
	queue, err := NewPostgresJobQueue(openFakeSQLDB(t, backend))
	if err != nil {
		t.Fatal(err)
	}
	tokens := make(chan string, jobs)
	ids := make(chan string, jobs)
	var group sync.WaitGroup
	for index := 0; index < jobs; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			lease, found, claimErr := queue.Claim(context.Background(), "worker", []StageID{stage}, time.Minute)
			if claimErr != nil || !found {
				t.Errorf("claim found=%v err=%v", found, claimErr)
				return
			}
			tokens <- lease.Token
			ids <- lease.Job.ID
		}()
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
	backend := &fakeSQLBackend{query: func(query string, arguments []driver.NamedValue) (driver.Rows, error) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(query, "SELECT exhausted_job.id"):
			return &fakeSQLRows{columns: []string{"id"}}, nil
		case strings.Contains(query, "SELECT candidate_job.id"):
			if !strings.Contains(query, "lease_expires_at <= clock_timestamp()") {
				t.Errorf("claim does not select expired leases")
			}
			return durableJobRow(7, attempt, stage, uintWord(7), 7), nil
		case strings.Contains(query, "UPDATE durable_jobs AS job"):
			attempt++
			return durableJobRow(arguments[3].Value.(int64), attempt, stage, uintWord(7), 7), nil
		default:
			return nil, fmt.Errorf("unexpected query: %s", query)
		}
	}, exec: func(query string, _ []driver.NamedValue) (driver.Result, error) {
		if isPublicationControlSQL(query) {
			return driver.RowsAffected(1), nil
		}
		return nil, fmt.Errorf("unexpected exec: %s", query)
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
		query: func(query string, _ []driver.NamedValue) (driver.Rows, error) {
			switch {
			case strings.Contains(query, "SELECT exhausted_job.id"):
				return &fakeSQLRows{columns: []string{"id"}}, nil
			case strings.Contains(query, "SELECT candidate_job.id"):
				return durableJobRow(44, 0, stage, uintWord(44), 44), nil
			case strings.Contains(query, "UPDATE durable_jobs AS job"):
				return emptyJobRows(), nil
			default:
				return nil, fmt.Errorf("unexpected query: %s", query)
			}
		},
		exec: func(query string, _ []driver.NamedValue) (driver.Result, error) {
			if isPublicationControlSQL(query) {
				return driver.RowsAffected(1), nil
			}
			return nil, fmt.Errorf("unexpected exec: %s", query)
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
		"renew":               renewJobSQL,
		"finish":              finishJobSQL,
		"retry":               retryJobSQL,
		"publish success":     atomicPublishSuccessSQL,
		"consume replay":      atomicConsumePendingReplaySQL,
		"claim":               claimCandidateJobSQL,
		"terminal exhaustion": terminalizeExhaustedJobSQL,
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

func TestPostgresLeaseMutationsAreTokenAndExpiryConditional(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var finishResults []map[string]any
	var finishStatuses []string
	var finishErrors []any
	var stageStates []string
	var retryReason string
	backend := &fakeSQLBackend{
		exec: func(query string, arguments []driver.NamedValue) (driver.Result, error) {
			mu.Lock()
			defer mu.Unlock()
			if isPublicationControlSQL(query) {
				return driver.RowsAffected(1), nil
			}
			if !strings.Contains(query, "lease_token = $2") || !strings.Contains(query, "lease_expires_at > clock_timestamp()") ||
				!strings.Contains(query, "kind = 'enrichment'") || !strings.Contains(query, "payload->>'block_hash'") {
				t.Errorf("mutation lacks token/expiry predicate:\n%s", query)
			}
			if arguments[1].Value != "owned-token" {
				return driver.RowsAffected(0), nil
			}
			if strings.Contains(query, "result = $4::jsonb") {
				var persisted map[string]any
				if err := json.Unmarshal([]byte(arguments[3].Value.(string)), &persisted); err != nil {
					t.Errorf("decode result: %v", err)
				}
				finishResults = append(finishResults, persisted)
				finishStatuses = append(finishStatuses, arguments[2].Value.(string))
				finishErrors = append(finishErrors, arguments[4].Value)
			}
			return driver.RowsAffected(1), nil
		},
		query: func(query string, arguments []driver.NamedValue) (driver.Rows, error) {
			mu.Lock()
			defer mu.Unlock()
			if strings.Contains(query, "INSERT INTO block_stage_results") {
				stageStates = append(stageStates, arguments[5].Value.(string))
				if arguments[8].Value != int64(9) || arguments[9].Value != int64(1) {
					t.Errorf("stage result lacks exact job/generation identity: args=%+v", arguments)
				}
				return &fakeSQLRows{columns: []string{"inserted"}, values: [][]driver.Value{{int64(1)}}}, nil
			}
			if strings.Contains(query, "completed_generation = GREATEST") && strings.Contains(query, "RETURNING status = 'queued'") {
				if arguments[1].Value != "owned-token" {
					return &fakeSQLRows{columns: []string{"replay_pending"}}, nil
				}
				var persisted map[string]any
				if err := json.Unmarshal([]byte(arguments[3].Value.(string)), &persisted); err != nil {
					t.Errorf("decode result: %v", err)
				}
				finishResults = append(finishResults, persisted)
				finishStatuses = append(finishStatuses, arguments[2].Value.(string))
				finishErrors = append(finishErrors, arguments[4].Value)
				return &fakeSQLRows{columns: []string{"replay_pending"}, values: [][]driver.Value{{false}}}, nil
			}
			if !strings.Contains(query, "available_at = CASE") || !strings.Contains(query, "RETURNING status") {
				return nil, fmt.Errorf("unexpected query: %s", query)
			}
			if !strings.Contains(query, "WHEN attempts >= max_attempts THEN 'failed'") {
				t.Errorf("retry does not terminally fail an exhausted job:\n%s", query)
			}
			if arguments[1].Value != "owned-token" {
				return &fakeSQLRows{columns: []string{"status", "replay_pending"}}, nil
			}
			retryReason = arguments[2].Value.(string)
			return &fakeSQLRows{columns: []string{"status", "replay_pending"}, values: [][]driver.Value{{"queued", false}}}, nil
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
	if finishStatuses[0] != "succeeded" || finishErrors[0] != nil || finishStatuses[1] != "failed" || finishErrors[1] != "trace RPC disabled" || finishResults[1]["state"] != string(ResultUnavailable) {
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
	backend := &fakeSQLBackend{query: func(query string, _ []driver.NamedValue) (driver.Rows, error) {
		if strings.Contains(query, "SELECT durable_job_id, job_generation") {
			return &fakeSQLRows{columns: []string{"durable_job_id", "job_generation"}}, nil
		}
		return nil, fmt.Errorf("unexpected query: %s", query)
	}, exec: func(query string, arguments []driver.NamedValue) (driver.Result, error) {
		if isPublicationControlSQL(query) {
			return driver.RowsAffected(1), nil
		}
		if strings.Contains(query, "DELETE FROM block_stage_results") {
			sawStageClear = true
			if len(arguments) != 4 || arguments[0].Value != "1" || arguments[2].Value != "token" || arguments[3].Value != int64(1) {
				t.Fatalf("stage clear arguments=%+v", arguments)
			}
			return driver.RowsAffected(1), nil
		}
		if strings.Contains(query, "DELETE FROM block_journals") {
			return driver.RowsAffected(1), nil
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
		if len(arguments) != 5 || arguments[0].Value != int64(17) || arguments[1].Value != "1" || arguments[2].Value != "token" || arguments[3].Value != int64(1) {
			t.Fatalf("requeue arguments=%+v", arguments)
		}
		return driver.RowsAffected(1), nil
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
		status := status
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			backend := &fakeSQLBackend{
				exec: func(query string, _ []driver.NamedValue) (driver.Result, error) {
					if isPublicationControlSQL(query) {
						return driver.RowsAffected(1), nil
					}
					return driver.RowsAffected(0), nil
				},
				query: func(query string, arguments []driver.NamedValue) (driver.Rows, error) {
					if !strings.Contains(query, "SELECT status") || len(arguments) != 1 || arguments[0].Value != int64(19) {
						t.Fatalf("status query=%q arguments=%+v", query, arguments)
					}
					return &fakeSQLRows{columns: []string{"status"}, values: [][]driver.Value{{status}}}, nil
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
		query: func(query string, _ []driver.NamedValue) (driver.Rows, error) {
			switch {
			case strings.Contains(query, "SELECT id") && !strings.Contains(query, "FOR UPDATE"):
				return &fakeSQLRows{columns: []string{"id"}, values: [][]driver.Value{{int64(30)}}}, nil
			case strings.Contains(query, "FROM durable_jobs") && strings.Contains(query, "FOR UPDATE"):
				return replayTargetRow(30, 1, requestedGeneration, ABIStage, hash, 29, "leased"), nil
			default:
				return nil, fmt.Errorf("unexpected query: %s", query)
			}
		},
		exec: func(query string, _ []driver.NamedValue) (driver.Result, error) {
			switch {
			case isPublicationControlSQL(query):
				return driver.RowsAffected(1), nil
			case strings.Contains(query, "INSERT INTO durable_job_replay_requests"):
				insertCalls++
				if insertCalls == 1 {
					return driver.RowsAffected(1), nil
				}
				return driver.RowsAffected(0), nil
			case strings.Contains(query, "UPDATE durable_jobs"):
				updateCalls++
				if !strings.Contains(query, "CASE WHEN status = 'leased' THEN status") || strings.Contains(query, "status IN ('succeeded', 'failed')") {
					t.Errorf("active replay update can steal the lease:\n%s", query)
				}
				requestedGeneration++
				return driver.RowsAffected(1), nil
			case strings.Contains(query, "DELETE FROM"):
				cleanupCalls++
				return driver.RowsAffected(1), nil
			default:
				return nil, fmt.Errorf("unexpected exec: %s", query)
			}
		},
	}
	db := openFakeSQLDB(t, backend)
	for call, want := range []bool{true, false} {
		tx, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		got, err := requestDependentStageReplayTx(context.Background(), tx, source, ABIStage)
		if err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
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
