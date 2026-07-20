package enrich

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDirectStageResultCannotOverwriteLeaseBoundPublication(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name            string
		resultAffected  int64
		journalAffected int64
	}{
		{name: "result marker", resultAffected: 0, journalAffected: 1},
		{name: "journal marker", resultAffected: 1, journalAffected: 0},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var committed atomic.Bool
			var rolledBack atomic.Bool
			backend := &fakeSQLBackend{
				exec: func(query string, _ []driver.NamedValue) (driver.Result, error) {
					switch {
					case isPublicationControlSQL(query):
						return driver.RowsAffected(1), nil
					case strings.Contains(query, "INSERT INTO block_stage_results"):
						if !strings.Contains(query, "current.durable_job_id IS NULL") ||
							!strings.Contains(query, "current.job_generation IS NULL") {
							t.Errorf("direct result upsert is not publication guarded:\n%s", query)
						}
						return driver.RowsAffected(test.resultAffected), nil
					case strings.Contains(query, "INSERT INTO block_journals"):
						if !strings.Contains(query, "current.durable_job_id IS NULL") ||
							!strings.Contains(query, "current.job_generation IS NULL") {
							t.Errorf("direct journal upsert is not publication guarded:\n%s", query)
						}
						return driver.RowsAffected(test.journalAffected), nil
					default:
						return nil, fmt.Errorf("unexpected exec: %s", query)
					}
				},
				commit:   func() error { committed.Store(true); return nil },
				rollback: func() error { rolledBack.Store(true); return nil },
			}
			db := openFakeSQLDB(t, backend)
			job := Job{ID: "direct-fixture", Stage: TokenStage, ChainID: "1", BlockHash: uintWord(1), BlockNumber: 1}
			_, err := runStageTransaction(t.Context(), db, job, func(context.Context, *sql.Tx) (StageResult, error) {
				return StageResult{State: ResultComplete}, nil
			})
			if !errors.Is(err, ErrAtomicPublicationRequired) {
				t.Fatalf("direct publication error=%v, want ErrAtomicPublicationRequired", err)
			}
			if committed.Load() || !rolledBack.Load() {
				t.Fatalf("committed=%t rolledBack=%t", committed.Load(), rolledBack.Load())
			}
		})
	}
}

func TestPostgresFinishRejectsKnownDerivedSuccessBeforeMutation(t *testing.T) {
	t.Parallel()
	var began atomic.Bool
	queue, err := NewPostgresJobQueue(openFakeSQLDB(t, &fakeSQLBackend{
		begin: func() { began.Store(true) },
	}))
	if err != nil {
		t.Fatal(err)
	}
	lease := publicationTestLease(StageID{Name: TokenStage.Name, Version: 2}, 9, 1)
	err = queue.Finish(t.Context(), lease, StageResult{State: ResultComplete})
	if !errors.Is(err, ErrAtomicPublicationRequired) {
		t.Fatalf("Finish complete error=%v, want ErrAtomicPublicationRequired", err)
	}
	if began.Load() {
		t.Fatal("Finish opened a transaction before rejecting derived success")
	}
}

func TestPostgresWorkerRejectsFutureDerivedVersionWithoutAtomicProcessor(t *testing.T) {
	t.Parallel()
	stage := StageID{Name: TraceStage.Name, Version: 2}
	queue, err := NewPostgresJobQueue(openFakeSQLDB(t, &fakeSQLBackend{}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewWorker(queue, []Processor{ProcessorFunc{
		ID: stage,
		Fn: func(context.Context, Job) (StageResult, error) {
			return StageResult{State: ResultComplete}, nil
		},
	}}, WorkerOptions{ID: "future-version-worker"})
	if err == nil || !strings.Contains(err.Error(), "lease-fenced atomic publication") {
		t.Fatalf("NewWorker error=%v, want future derived version rejection", err)
	}
}

func TestKnownDerivedTerminalResultCarriesExactLeaseGeneration(t *testing.T) {
	t.Parallel()
	for _, stage := range []StageID{ProxyStage, ABIStage, TokenStage, StatsStage, TraceStage} {
		stage := stage
		t.Run(stage.String(), func(t *testing.T) {
			t.Parallel()
			var committed atomic.Bool
			var sawJournal atomic.Bool
			backend := &fakeSQLBackend{
				query: func(query string, arguments []driver.NamedValue) (driver.Rows, error) {
					switch {
					case strings.Contains(query, "UPDATE durable_jobs") && strings.Contains(query, "RETURNING status = 'queued'"):
						if !strings.Contains(query, "claimed_generation = $6") || !strings.Contains(query, "leased_generation = $6") ||
							arguments[5].Value != int64(3) {
							t.Errorf("terminal CAS lacks exact generation: args=%+v\n%s", arguments, query)
						}
						return &fakeSQLRows{columns: []string{"pending"}, values: [][]driver.Value{{false}}}, nil
					case strings.Contains(query, "INSERT INTO block_stage_results"):
						if arguments[8].Value != int64(13) || arguments[9].Value != int64(3) {
							t.Errorf("terminal marker identity args=%+v", arguments)
						}
						return &fakeSQLRows{columns: []string{"inserted"}, values: [][]driver.Value{{int64(1)}}}, nil
					case strings.Contains(query, "INSERT INTO durable_stage_publications"):
						if arguments[0].Value != int64(13) || arguments[1].Value != int64(3) {
							t.Errorf("terminal proof identity args=%+v", arguments)
						}
						return &fakeSQLRows{columns: []string{"inserted"}, values: [][]driver.Value{{int64(1)}}}, nil
					default:
						return nil, fmt.Errorf("unexpected query: %s", query)
					}
				},
				exec: func(query string, _ []driver.NamedValue) (driver.Result, error) {
					if isPublicationControlSQL(query) {
						return driver.RowsAffected(1), nil
					}
					if strings.Contains(query, "block_journals") {
						sawJournal.Store(true)
					}
					return nil, fmt.Errorf("unexpected exec: %s", query)
				},
				commit: func() error { committed.Store(true); return nil },
			}
			queue, _ := NewPostgresJobQueue(openFakeSQLDB(t, backend))
			lease := publicationTestLease(stage, 13, 3)
			if err := queue.Finish(t.Context(), lease, StageResult{State: ResultUnavailable, Error: "capability unavailable"}); err != nil {
				t.Fatal(err)
			}
			if !committed.Load() || sawJournal.Load() {
				t.Fatalf("committed=%t journal=%t", committed.Load(), sawJournal.Load())
			}
		})
	}
}

func TestAtomicPublisherDiscardsStaleAndPendingGenerationOutput(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		pendingCAS int64
		want       error
		outcome    stagePublicationOutcome
	}{
		{name: "lost lease", pendingCAS: 0, want: ErrLeaseLost},
		{name: "pending replay", pendingCAS: 1, outcome: stagePublicationSuperseded},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var rolledToSavepoint atomic.Bool
			var committed atomic.Bool
			var rolledBack atomic.Bool
			backend := &fakeSQLBackend{
				query: publicationMarkerRows,
				exec: func(query string, _ []driver.NamedValue) (driver.Result, error) {
					switch {
					case isPublicationControlSQL(query):
						return driver.RowsAffected(1), nil
					case strings.HasPrefix(strings.TrimSpace(query), "SAVEPOINT"):
						return driver.RowsAffected(0), nil
					case strings.Contains(query, "INSERT INTO fixture_output"):
						return driver.RowsAffected(1), nil
					case strings.Contains(query, "SET status = 'succeeded'"):
						return driver.RowsAffected(0), nil
					case strings.HasPrefix(strings.TrimSpace(query), "ROLLBACK TO SAVEPOINT"):
						rolledToSavepoint.Store(true)
						return driver.RowsAffected(0), nil
					case strings.Contains(query, "SET status = 'queued'"):
						return driver.RowsAffected(test.pendingCAS), nil
					case strings.Contains(query, "DELETE FROM block_stage_results"), strings.Contains(query, "DELETE FROM block_journals"):
						return driver.RowsAffected(1), nil
					default:
						return nil, fmt.Errorf("unexpected exec: %s", query)
					}
				},
				commit:   func() error { committed.Store(true); return nil },
				rollback: func() error { rolledBack.Store(true); return nil },
			}
			queue, _ := NewPostgresJobQueue(openFakeSQLDB(t, backend))
			lease := publicationTestLease(TokenStage, 17, 1)
			result, err := queue.publishSuccess(t.Context(), lease, func(ctx context.Context, tx *sql.Tx) (StageResult, error) {
				if _, err := tx.ExecContext(ctx, "INSERT INTO fixture_output VALUES (1)"); err != nil {
					return StageResult{}, err
				}
				return StageResult{State: ResultComplete}, nil
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("publish error=%v, want %v", err, test.want)
			}
			if !rolledToSavepoint.Load() {
				t.Fatal("stale output was not rolled back to its savepoint")
			}
			if test.want != nil {
				if committed.Load() || !rolledBack.Load() {
					t.Fatalf("lost lease committed=%t rolledBack=%t", committed.Load(), rolledBack.Load())
				}
				return
			}
			if !committed.Load() || result.publication != test.outcome {
				t.Fatalf("committed=%t outcome=%d", committed.Load(), result.publication)
			}
		})
	}
}

func TestHeartbeatCannotRenewAfterAtomicCommit(t *testing.T) {
	t.Parallel()
	commitEntered := make(chan struct{})
	releaseCommit := make(chan struct{})
	var renewals atomic.Int32
	var once sync.Once
	backend := &fakeSQLBackend{
		query: publicationMarkerRows,
		exec: func(query string, _ []driver.NamedValue) (driver.Result, error) {
			switch {
			case isPublicationControlSQL(query):
				return driver.RowsAffected(1), nil
			case strings.HasPrefix(strings.TrimSpace(query), "SAVEPOINT"):
				return driver.RowsAffected(0), nil
			case strings.Contains(query, "SET status = 'succeeded'"):
				return driver.RowsAffected(1), nil
			case strings.Contains(query, "SET lease_expires_at"):
				renewals.Add(1)
				return driver.RowsAffected(1), nil
			default:
				return nil, fmt.Errorf("unexpected exec: %s", query)
			}
		},
		commit: func() error {
			once.Do(func() { close(commitEntered) })
			<-releaseCommit
			return nil
		},
	}
	queue, _ := NewPostgresJobQueue(openFakeSQLDB(t, backend))
	guard := &leaseHeartbeatGuard{}
	lease := publicationTestLease(TokenStage, 19, 1)
	lease.heartbeat = guard
	published := make(chan error, 1)
	go func() {
		_, err := queue.publishSuccess(t.Context(), lease, func(context.Context, *sql.Tx) (StageResult, error) {
			return StageResult{State: ResultComplete}, nil
		})
		published <- err
	}()
	<-commitEntered
	worker := &Worker{queue: queue}
	renewed := make(chan error, 1)
	go func() { renewed <- worker.renew(t.Context(), lease) }()
	select {
	case err := <-renewed:
		t.Fatalf("renew passed the commit guard early: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseCommit)
	if err := <-published; err != nil {
		t.Fatal(err)
	}
	if err := <-renewed; err != nil {
		t.Fatal(err)
	}
	if renewals.Load() != 0 || !guard.finished {
		t.Fatalf("renewals=%d finished=%t", renewals.Load(), guard.finished)
	}
}

func TestAtomicPublisherTreatsCommittedGenerationSupersededDuringConfirmationAsSuccess(t *testing.T) {
	t.Parallel()
	var confirmed atomic.Bool
	backend := &fakeSQLBackend{
		query: func(query string, _ []driver.NamedValue) (driver.Rows, error) {
			switch {
			case strings.Contains(query, "INSERT INTO block_stage_results"), strings.Contains(query, "INSERT INTO block_journals"), strings.Contains(query, "INSERT INTO durable_stage_publications"):
				return &fakeSQLRows{columns: []string{"inserted"}, values: [][]driver.Value{{int64(1)}}}, nil
			case strings.Contains(query, "FROM durable_stage_publications AS publication"):
				if !strings.Contains(query, "publication.job_generation = $2") ||
					!strings.Contains(query, "publication.state = 'complete'") {
					t.Errorf("ambiguous commit confirmation lacks exact immutable proof:\n%s", query)
				}
				confirmed.Store(true)
				return &fakeSQLRows{columns: []string{"confirmed"}, values: [][]driver.Value{{true}}}, nil
			default:
				return nil, fmt.Errorf("unexpected query: %s", query)
			}
		},
		exec: func(query string, _ []driver.NamedValue) (driver.Result, error) {
			switch {
			case isPublicationControlSQL(query):
				return driver.RowsAffected(1), nil
			case strings.HasPrefix(strings.TrimSpace(query), "SAVEPOINT"):
				return driver.RowsAffected(0), nil
			case strings.Contains(query, "SET status = 'succeeded'"):
				return driver.RowsAffected(1), nil
			default:
				return nil, fmt.Errorf("unexpected exec: %s", query)
			}
		},
		commit: func() error { return errors.New("ambiguous transport failure after COMMIT") },
	}
	queue, _ := NewPostgresJobQueue(openFakeSQLDB(t, backend))
	result, err := queue.publishSuccess(t.Context(), publicationTestLease(TokenStage, 23, 1), func(context.Context, *sql.Tx) (StageResult, error) {
		return StageResult{State: ResultComplete}, nil
	})
	if err != nil || result.publication != stagePublicationSucceeded || !confirmed.Load() {
		t.Fatalf("result=%+v error=%v confirmed=%t", result, err, confirmed.Load())
	}
}

func TestAmbiguousSuccessDoesNotAcceptFailedGenerationCounters(t *testing.T) {
	t.Parallel()
	backend := &fakeSQLBackend{query: func(query string, _ []driver.NamedValue) (driver.Rows, error) {
		if strings.Contains(query, "durable_jobs") || strings.Contains(query, "requested_generation") ||
			strings.Contains(query, "completed_generation") {
			t.Errorf("ambiguous success relied on mutable job counters:\n%s", query)
		}
		if !strings.Contains(query, "durable_stage_publications") ||
			!strings.Contains(query, "publication.state = 'complete'") {
			t.Errorf("ambiguous success lacks exact complete ledger proof:\n%s", query)
		}
		// Model generation 1 having failed, then generation 2 being requested:
		// its mutable counters advanced, but no generation-1 complete proof exists.
		return &fakeSQLRows{columns: []string{"confirmed"}, values: [][]driver.Value{{false}}}, nil
	}}
	queue, _ := NewPostgresJobQueue(openFakeSQLDB(t, backend))
	if queue.confirmPublishedSuccess(t.Context(), durablePublicationIdentity{jobID: 31, generation: 1}) {
		t.Fatal("failed generation followed by replay was misconfirmed as successful")
	}
}

func TestAtomicPublisherRollsBackWhenPublicationMarkerCannotCommit(t *testing.T) {
	t.Parallel()
	var rolledBack atomic.Bool
	backend := &fakeSQLBackend{
		query: func(query string, _ []driver.NamedValue) (driver.Rows, error) {
			switch {
			case strings.Contains(query, "INSERT INTO block_stage_results"):
				return &fakeSQLRows{columns: []string{"inserted"}, values: [][]driver.Value{{int64(1)}}}, nil
			case strings.Contains(query, "INSERT INTO block_journals"):
				return &fakeSQLRows{columns: []string{"inserted"}}, nil
			default:
				return nil, fmt.Errorf("unexpected query: %s", query)
			}
		},
		exec: func(query string, _ []driver.NamedValue) (driver.Result, error) {
			switch {
			case isPublicationControlSQL(query):
				return driver.RowsAffected(1), nil
			case strings.HasPrefix(strings.TrimSpace(query), "SAVEPOINT"), strings.Contains(query, "INSERT INTO fixture_output"):
				return driver.RowsAffected(1), nil
			default:
				return nil, fmt.Errorf("unexpected exec: %s", query)
			}
		},
		rollback: func() error { rolledBack.Store(true); return nil },
	}
	queue, _ := NewPostgresJobQueue(openFakeSQLDB(t, backend))
	_, err := queue.publishSuccess(t.Context(), publicationTestLease(TokenStage, 29, 1), func(ctx context.Context, tx *sql.Tx) (StageResult, error) {
		if _, err := tx.ExecContext(ctx, "INSERT INTO fixture_output VALUES (1)"); err != nil {
			return StageResult{}, err
		}
		return StageResult{State: ResultComplete}, nil
	})
	if !errors.Is(err, ErrLeaseLost) || !rolledBack.Load() {
		t.Fatalf("error=%v rolledBack=%t", err, rolledBack.Load())
	}
}

func TestAllKnownDerivedProcessorsImplementLeasePublication(t *testing.T) {
	t.Parallel()
	processors := []leaseProcessor{
		(*PostgresProxyProcessor)(nil),
		(*PostgresABIProcessor)(nil),
		(*PostgresTokenProcessor)(nil),
		(*PostgresStatsProcessor)(nil),
		(*TraceRPCProcessor)(nil),
	}
	if len(processors) != 5 {
		t.Fatalf("lease processor count=%d", len(processors))
	}
}

func publicationTestLease(stage StageID, jobID, generation int64) Lease {
	return Lease{Job: Job{
		ID: fmt.Sprint(jobID), Stage: stage, ChainID: "1", BlockHash: uintWord(uint64(jobID)),
		BlockNumber: uint64(jobID), Attempt: 1, Generation: uint64(generation),
	}, Token: "publication-token"}
}

func publicationMarkerRows(query string, _ []driver.NamedValue) (driver.Rows, error) {
	switch {
	case strings.Contains(query, "INSERT INTO block_stage_results"), strings.Contains(query, "INSERT INTO block_journals"), strings.Contains(query, "INSERT INTO durable_stage_publications"):
		return &fakeSQLRows{columns: []string{"inserted"}, values: [][]driver.Value{{int64(1)}}}, nil
	case strings.Contains(query, "SELECT durable_job_id, job_generation"):
		return &fakeSQLRows{columns: []string{"durable_job_id", "job_generation"}}, nil
	default:
		return nil, fmt.Errorf("unexpected query: %s", query)
	}
}

func isPublicationControlSQL(query string) bool {
	return strings.Contains(query, "set_config('etherview.enrichment_publication_protocol'") ||
		strings.Contains(query, "pg_advisory_xact_lock(-(")
}
