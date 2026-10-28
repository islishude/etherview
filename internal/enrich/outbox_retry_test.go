package enrich

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/islishude/etherview/internal/testpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestOutboxAbortedPublicationRetriesWholeTransaction(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		failure       error
		persistent    bool
		commitFailure bool
		attempts      int
	}{
		{name: "deadlock", failure: &pgconn.PgError{Code: "40P01"}, attempts: 2},
		{name: "serialization", failure: &pgconn.PgError{Code: "40001"}, attempts: 2},
		{name: "bounded deadlock", failure: &pgconn.PgError{Code: "40P01"}, persistent: true, attempts: queueTransactionAttempts},
		{name: "other SQL error", failure: &pgconn.PgError{Code: "23505"}, attempts: 1},
		{name: "ambiguous commit", failure: errors.New("connection lost during commit"), commitFailure: true, attempts: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			begins, rollbacks, claims, canonicalChecks, publishes, commits := 0, 0, 0, 0, 0, 0
			backend := &fakeSQLBackend{
				begin: func() {
					if begins != rollbacks {
						t.Fatalf("new transaction before rollback: begins=%d rollbacks=%d", begins, rollbacks)
					}
					begins++
				},
				rollback: func() error { rollbacks++; return nil },
				commit: func() error {
					commits++
					if tc.commitFailure {
						return tc.failure
					}
					return nil
				},
				query: func(query string, _ []any) (pgx.Rows, error) {
					switch {
					case strings.Contains(query, "FROM transactional_outbox"):
						claims++
						return outboxRow(5, CoreBlockCanonical, uintWord(101), 101), nil
					case strings.Contains(query, "FROM canonical_blocks"):
						canonicalChecks++
						// A reorg during backoff must be observed, not reuse the old decision.
						return boolRows(begins == 1), nil
					default:
						return nil, fmt.Errorf("unexpected query: %s", query)
					}
				},
				exec: func(query string, _ []any) (pgconn.CommandTag, error) {
					if strings.Contains(query, "SET published_at") {
						publishes++
						if !tc.commitFailure && (begins == 1 || tc.persistent) {
							return pgconn.CommandTag{}, tc.failure
						}
					}
					return testpgx.Affected(1), nil
				},
			}
			enqueuer := &recordingEnqueuer{}
			dispatcher, err := NewOutboxDispatcher(openFakeSQLDB(t, backend), enqueuer,
				OutboxDispatcherOptions{Stages: []StageID{{Name: "token", Version: 2}}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := dispatcher.DispatchOne(t.Context())
			recovered := tc.attempts == 2
			if recovered {
				if err != nil || result.State != OutboxPublished || result.Transition.Result != "stale_canonical_skipped" {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			} else if !errors.Is(err, tc.failure) || result.Transition != nil {
				t.Fatalf("result=%+v err=%v want=%v", result, err, tc.failure)
			}
			if begins != tc.attempts || claims != begins || canonicalChecks != begins || publishes != begins {
				t.Fatalf("begins=%d claims=%d canonical=%d publishes=%d want=%d", begins, claims, canonicalChecks, publishes, tc.attempts)
			}
			wantCommits := 0
			if recovered || tc.commitFailure {
				wantCommits = 1
			}
			if commits != wantCommits || rollbacks != begins-commits || len(enqueuer.requests) != 1 {
				t.Fatalf("commits=%d rollbacks=%d enqueues=%d", commits, rollbacks, len(enqueuer.requests))
			}
		})
	}
}
