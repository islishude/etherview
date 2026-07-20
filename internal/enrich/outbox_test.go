package enrich

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func outboxRow(id int64, topic string, hash Word, block uint64, attempts int64) driver.Rows {
	payload, _ := json.Marshal(coreOutboxPayload{BlockHash: hash.String(), BlockNumber: fmt.Sprint(block)})
	return &fakeSQLRows{
		columns: []string{"id", "chain_id", "topic", "message_key", "payload", "attempts", "generation"},
		values:  [][]driver.Value{{id, "1", topic, hash.String(), payload, attempts, int64(1)}},
	}
}

func emptyOutboxRows() driver.Rows {
	return &fakeSQLRows{columns: []string{"id", "chain_id", "topic", "message_key", "payload", "attempts", "generation"}}
}

func boolRows(value bool) driver.Rows {
	return &fakeSQLRows{columns: []string{"exists"}, values: [][]driver.Value{{value}}}
}

type recordingEnqueuer struct {
	mu       sync.Mutex
	requests []EnqueueRequest
	result   func(int, EnqueueRequest) (EnqueueResult, error)
}

func (enqueuer *recordingEnqueuer) Enqueue(_ context.Context, request EnqueueRequest) (EnqueueResult, error) {
	enqueuer.mu.Lock()
	defer enqueuer.mu.Unlock()
	index := len(enqueuer.requests)
	enqueuer.requests = append(enqueuer.requests, request)
	if enqueuer.result != nil {
		return enqueuer.result(index, request)
	}
	return EnqueueResult{Created: true}, nil
}

func TestOutboxPostgresQueueEnqueuesAndPublishesInOneTransaction(t *testing.T) {
	t.Parallel()
	hash := uintWord(101)
	stages := []StageID{{Name: "abi", Version: 1}, {Name: "token", Version: 2}}
	var mu sync.Mutex
	begins := 0
	insertedStages := make([]string, 0, len(stages))
	var audit dispatchAudit
	backend := &fakeSQLBackend{
		begin: func() {
			mu.Lock()
			begins++
			mu.Unlock()
		},
		query: func(query string, arguments []driver.NamedValue) (driver.Rows, error) {
			mu.Lock()
			defer mu.Unlock()
			switch {
			case strings.Contains(query, "FROM transactional_outbox"):
				if !strings.Contains(query, "FOR UPDATE SKIP LOCKED") {
					t.Errorf("outbox claim lacks SKIP LOCKED:\n%s", query)
				}
				return outboxRow(5, CoreBlockCanonical, hash, 101, 0), nil
			case strings.Contains(query, "FROM canonical_blocks"):
				return boolRows(true), nil
			case strings.Contains(query, "FROM durable_jobs") && !strings.Contains(query, "INSERT INTO"):
				return emptyJobRows(), nil
			case strings.Contains(query, "INSERT INTO durable_jobs"):
				stageName := arguments[2].Value.(string)
				insertedStages = append(insertedStages, stageName)
				payload := []byte(arguments[5].Value.(string))
				return &fakeSQLRows{
					columns: []string{"id", "chain_id", "stage", "stage_version", "attempts", "payload", "requested_generation"},
					values: [][]driver.Value{{
						int64(100 + len(insertedStages)), "1", stageName, arguments[3].Value.(int64), int64(0), payload, int64(1),
					}},
				}, nil
			default:
				return nil, fmt.Errorf("unexpected query: %s", query)
			}
		},
		exec: func(query string, arguments []driver.NamedValue) (driver.Result, error) {
			mu.Lock()
			defer mu.Unlock()
			if strings.Contains(query, "SET published_at") {
				if err := json.Unmarshal([]byte(arguments[1].Value.(string)), &audit); err != nil {
					t.Errorf("decode dispatch audit: %v", err)
				}
			}
			return driver.RowsAffected(1), nil
		},
	}
	db := openFakeSQLDB(t, backend)
	queue, _ := NewPostgresJobQueue(db)
	dispatcher, err := NewOutboxDispatcher(db, queue, OutboxDispatcherOptions{Stages: stages})
	if err != nil {
		t.Fatal(err)
	}
	result, err := dispatcher.DispatchOne(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.State != OutboxPublished || begins != 1 {
		t.Fatalf("result=%+v transaction begins=%d", result, begins)
	}
	if strings.Join(insertedStages, ",") != "abi,token" || audit.Outcome != "enrichment_enqueued" || audit.JobsCreated != 2 || !audit.Replayed {
		t.Fatalf("stages=%v audit=%+v", insertedStages, audit)
	}
	if dispatcher.options.JobMaxAttempts != DefaultEnrichmentMaxAttempts {
		t.Fatalf("durable retry budget=%d want=%d", dispatcher.options.JobMaxAttempts, DefaultEnrichmentMaxAttempts)
	}
}

func TestOutboxPartialFailureRetriesAndRecoversIdempotently(t *testing.T) {
	t.Parallel()
	hash := uintWord(202)
	stages := []StageID{{Name: "abi", Version: 1}, {Name: "token", Version: 1}}
	var mu sync.Mutex
	retries, publishes := 0, 0
	var publishedAudit dispatchAudit
	backend := &fakeSQLBackend{
		query: func(query string, _ []driver.NamedValue) (driver.Rows, error) {
			switch {
			case strings.Contains(query, "FROM transactional_outbox"):
				return outboxRow(8, CoreBlockCanonical, hash, 202, 0), nil
			case strings.Contains(query, "FROM canonical_blocks"):
				return boolRows(true), nil
			default:
				return nil, fmt.Errorf("unexpected query: %s", query)
			}
		},
		exec: func(query string, arguments []driver.NamedValue) (driver.Result, error) {
			mu.Lock()
			defer mu.Unlock()
			switch {
			case strings.Contains(query, "SET attempts = LEAST"):
				retries++
				if arguments[2].Value != int64(time.Second/time.Microsecond) {
					t.Errorf("first retry delay=%v", arguments[2].Value)
				}
			case strings.Contains(query, "SET published_at"):
				publishes++
				if err := json.Unmarshal([]byte(arguments[1].Value.(string)), &publishedAudit); err != nil {
					t.Errorf("decode audit: %v", err)
				}
			}
			return driver.RowsAffected(1), nil
		},
	}
	enqueuer := &recordingEnqueuer{result: func(index int, _ EnqueueRequest) (EnqueueResult, error) {
		switch index {
		case 0:
			return EnqueueResult{Created: true}, nil
		case 1:
			return EnqueueResult{}, errors.New("database unavailable")
		case 2:
			return EnqueueResult{Created: false}, nil
		default:
			return EnqueueResult{Created: true}, nil
		}
	}}
	dispatcher, err := NewOutboxDispatcher(openFakeSQLDB(t, backend), enqueuer, OutboxDispatcherOptions{Stages: stages})
	if err != nil {
		t.Fatal(err)
	}
	first, err := dispatcher.DispatchOne(context.Background())
	if err != nil || first.State != OutboxRetry || !strings.Contains(first.LastError, "database unavailable") {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := dispatcher.DispatchOne(context.Background())
	if err != nil || second.State != OutboxPublished {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	enqueuer.mu.Lock()
	requests := append([]EnqueueRequest(nil), enqueuer.requests...)
	enqueuer.mu.Unlock()
	if retries != 1 || publishes != 1 || len(requests) != 4 || publishedAudit.JobsExisting != 1 || publishedAudit.JobsCreated != 1 {
		t.Fatalf("retries=%d publishes=%d requests=%d audit=%+v", retries, publishes, len(requests), publishedAudit)
	}
	if requests[0].Stage != stages[0] || requests[1].Stage != stages[1] || requests[2].Stage != stages[0] || requests[3].Stage != stages[1] {
		t.Fatalf("request stages=%v,%v,%v,%v", requests[0].Stage, requests[1].Stage, requests[2].Stage, requests[3].Stage)
	}
	for _, request := range requests {
		if request.Replay != (ReplaySource{Kind: "canonical-attach", Key: "8:1"}) || request.MaxAttempts != DefaultEnrichmentMaxAttempts {
			t.Fatalf("dispatch replay identity/budget=%+v max=%d", request.Replay, request.MaxAttempts)
		}
	}
}

func TestOutboxOrphanRequiresNonCanonicalJournalsAndAuditsNoReplay(t *testing.T) {
	t.Parallel()
	hash := uintWord(303)
	stage := StageID{Name: "token", Version: 1}
	for _, test := range []struct {
		name       string
		journalsOK bool
		wantState  OutboxDispatchState
	}{
		{name: "confirmed", journalsOK: true, wantState: OutboxPublished},
		{name: "still-canonical", journalsOK: false, wantState: OutboxRetry},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			var audit dispatchAudit
			published, retried := 0, 0
			backend := &fakeSQLBackend{
				query: func(query string, _ []driver.NamedValue) (driver.Rows, error) {
					switch {
					case strings.Contains(query, "FROM transactional_outbox"):
						return outboxRow(11, CoreBlockOrphaned, hash, 303, 0), nil
					case strings.Contains(query, "FROM canonical_blocks"):
						return boolRows(false), nil
					case strings.Contains(query, "FROM block_journals"):
						if !strings.Contains(query, "AND canonical") {
							t.Errorf("orphan check does not inspect canonical journals")
						}
						return boolRows(test.journalsOK), nil
					default:
						return nil, fmt.Errorf("unexpected query: %s", query)
					}
				},
				exec: func(query string, arguments []driver.NamedValue) (driver.Result, error) {
					switch {
					case strings.Contains(query, "SET published_at"):
						published++
						if err := json.Unmarshal([]byte(arguments[1].Value.(string)), &audit); err != nil {
							t.Errorf("decode audit: %v", err)
						}
					case strings.Contains(query, "SET attempts = LEAST"):
						retried++
					}
					return driver.RowsAffected(1), nil
				},
			}
			enqueuer := &recordingEnqueuer{result: func(int, EnqueueRequest) (EnqueueResult, error) {
				t.Error("orphan event must not enqueue enrichment replay")
				return EnqueueResult{}, nil
			}}
			dispatcher, _ := NewOutboxDispatcher(openFakeSQLDB(t, backend), enqueuer, OutboxDispatcherOptions{Stages: []StageID{stage}})
			result, err := dispatcher.DispatchOne(context.Background())
			if err != nil || result.State != test.wantState {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if test.journalsOK {
				if published != 1 || retried != 0 || audit.Outcome != "orphan_acknowledged" || audit.Replayed || audit.JournalsCanonical == nil || *audit.JournalsCanonical {
					t.Fatalf("published=%d retried=%d audit=%+v", published, retried, audit)
				}
			} else if published != 0 || retried != 1 || !strings.Contains(result.LastError, "canonical journal") {
				t.Fatalf("published=%d retried=%d result=%+v", published, retried, result)
			}
		})
	}
}

func TestOutboxSkipsAnOrphanGenerationAfterSameHashReattaches(t *testing.T) {
	t.Parallel()
	hash := uintWord(304)
	var audit dispatchAudit
	backend := &fakeSQLBackend{
		query: func(query string, _ []driver.NamedValue) (driver.Rows, error) {
			switch {
			case strings.Contains(query, "FROM transactional_outbox"):
				return outboxRow(12, CoreBlockOrphaned, hash, 304, 0), nil
			case strings.Contains(query, "FROM canonical_blocks"):
				return boolRows(true), nil
			default:
				return nil, fmt.Errorf("unexpected query: %s", query)
			}
		},
		exec: func(query string, arguments []driver.NamedValue) (driver.Result, error) {
			if strings.Contains(query, "SET published_at") {
				if err := json.Unmarshal([]byte(arguments[1].Value.(string)), &audit); err != nil {
					t.Fatal(err)
				}
			}
			return driver.RowsAffected(1), nil
		},
	}
	dispatcher, _ := NewOutboxDispatcher(
		openFakeSQLDB(t, backend), &recordingEnqueuer{},
		OutboxDispatcherOptions{Stages: []StageID{TokenStage}},
	)
	result, err := dispatcher.DispatchOne(context.Background())
	if err != nil || result.State != OutboxPublished || audit.Outcome != "stale_orphan_skipped" || audit.Replayed {
		t.Fatalf("result=%+v err=%v audit=%+v", result, err, audit)
	}
}

func TestOutboxConcurrentDispatchUsesDistinctSkipLockedClaims(t *testing.T) {
	t.Parallel()
	const count = 16
	stage := StageID{Name: "token", Version: 1}
	var mu sync.Mutex
	next := 1
	published := 0
	claimUsesSkipLocked := true
	backend := &fakeSQLBackend{
		query: func(query string, _ []driver.NamedValue) (driver.Rows, error) {
			if strings.Contains(query, "FROM transactional_outbox") {
				mu.Lock()
				defer mu.Unlock()
				claimUsesSkipLocked = claimUsesSkipLocked && strings.Contains(query, "FOR UPDATE SKIP LOCKED")
				id := next
				next++
				return outboxRow(int64(id), CoreBlockCanonical, uintWord(uint64(id)), uint64(id), 0), nil
			}
			if strings.Contains(query, "FROM canonical_blocks") {
				return boolRows(true), nil
			}
			return nil, fmt.Errorf("unexpected query: %s", query)
		},
		exec: func(query string, _ []driver.NamedValue) (driver.Result, error) {
			if strings.Contains(query, "SET published_at") {
				mu.Lock()
				published++
				mu.Unlock()
			}
			return driver.RowsAffected(1), nil
		},
	}
	enqueuer := &recordingEnqueuer{}
	dispatcher, _ := NewOutboxDispatcher(openFakeSQLDB(t, backend), enqueuer, OutboxDispatcherOptions{Stages: []StageID{stage}})
	var group sync.WaitGroup
	for index := 0; index < count; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := dispatcher.DispatchOne(context.Background())
			if err != nil || result.State != OutboxPublished {
				t.Errorf("result=%+v err=%v", result, err)
			}
		}()
	}
	group.Wait()
	enqueuer.mu.Lock()
	requests := append([]EnqueueRequest(nil), enqueuer.requests...)
	enqueuer.mu.Unlock()
	mu.Lock()
	defer mu.Unlock()
	if !claimUsesSkipLocked || published != count || len(requests) != count {
		t.Fatalf("skipLocked=%v published=%d requests=%d", claimUsesSkipLocked, published, len(requests))
	}
}

func TestOutboxDispatcherServicePollsUntilCancellation(t *testing.T) {
	t.Parallel()
	backend := &fakeSQLBackend{query: func(query string, _ []driver.NamedValue) (driver.Rows, error) {
		if !strings.Contains(query, "FROM transactional_outbox") {
			return nil, fmt.Errorf("unexpected query: %s", query)
		}
		return emptyOutboxRows(), nil
	}}
	dispatcher, err := NewOutboxDispatcher(
		openFakeSQLDB(t, backend),
		&recordingEnqueuer{},
		OutboxDispatcherOptions{ServiceName: "outbox-test", Stages: []StageID{{Name: "token", Version: 1}}, PollInterval: time.Millisecond},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if err := dispatcher.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("run err=%v", err)
	}
	if dispatcher.Name() != "outbox-test" {
		t.Fatalf("name=%q", dispatcher.Name())
	}
}

func TestOutboxRetryBackoffCaps(t *testing.T) {
	t.Parallel()
	dispatcher := &OutboxDispatcher{options: OutboxDispatcherOptions{RetryBase: time.Second, RetryMax: 4 * time.Second}}
	for _, test := range []struct {
		attempts int64
		want     time.Duration
	}{
		{attempts: 0, want: time.Second},
		{attempts: 1, want: 2 * time.Second},
		{attempts: 2, want: 4 * time.Second},
		{attempts: 100, want: 4 * time.Second},
	} {
		if got := dispatcher.retryDelay(test.attempts); got != test.want {
			t.Fatalf("attempts=%d delay=%s want=%s", test.attempts, got, test.want)
		}
	}
}
