package maintenance

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestNewPostgresRepositoryRejectsNilDatabase(t *testing.T) {
	t.Parallel()
	if _, err := NewPostgresRepository(nil); err == nil {
		t.Fatal("nil database was accepted")
	}
}

func TestPostgresClaimGuardCompleteIsLeaseOwnedAndIdempotent(t *testing.T) {
	t.Parallel()
	db := maintenanceDatabase(t,
		sqlStep{
			kind: "query", contains: "FOR UPDATE OF request SKIP LOCKED", columns: maintenanceColumns(12),
			rows: [][]any{maintenanceCandidate(7, "queued", false, "50")},
			check: func(arguments []any) error {
				if len(arguments) != 5 || arguments[4] != int32(claimBatchSize) || arguments[0] != false {
					return fmt.Errorf("claim arguments=%v", arguments)
				}
				return nil
			},
		},
		advisoryStep(7, true),
		sqlStep{kind: "exec", contains: "SET status = 'running'", affected: 1},
		sqlStep{
			kind: "query", contains: "SELECT request.status, finality.finalized_number",
			columns: maintenanceColumns(2), rows: [][]any{{"running", "50"}},
		},
		sqlStep{kind: "exec", contains: "SET status = 'done'", affected: 1},
		unlockStep(7),
	)

	repository := mustPostgresRepository(t, db)
	lease, found, err := repository.Claim(context.Background(), "worker-a")
	if err != nil || !found {
		t.Fatalf("found=%v error=%v", found, err)
	}
	if lease.Request != validRequest() || lease.session == nil || lease.session.workerID != "worker-a" {
		t.Fatalf("lease=%+v", lease)
	}
	if err := repository.GuardFinalized(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
	if err := repository.Complete(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
	if err := repository.Complete(context.Background(), lease); err != nil {
		t.Fatalf("repeated completion is not idempotent: %v", err)
	}
	if err := repository.Release(context.Background(), lease); err != nil {
		t.Fatalf("release after completion is not idempotent: %v", err)
	}
}

func TestPostgresClaimRejectsFinalizedRangeAndRecordsFailure(t *testing.T) {
	t.Parallel()
	db := maintenanceDatabase(t,
		sqlStep{kind: "query", contains: "FOR UPDATE OF request SKIP LOCKED", columns: maintenanceColumns(12), rows: [][]any{maintenanceCandidate(7, "queued", false, "150")}},
		advisoryStep(7, true),
		sqlStep{
			kind: "exec", contains: "SET status = 'failed'", affected: 1,
			check: func(arguments []any) error {
				if len(arguments) != 2 || arguments[0] != int64(7) || !strings.Contains(*arguments[1].(*string), "intersects finalized height 150") {
					return fmt.Errorf("rejection arguments=%v", arguments)
				}
				return nil
			},
		},
		unlockStep(7),
	)
	repository := mustPostgresRepository(t, db)
	lease, found, err := repository.Claim(context.Background(), "worker")
	if err != nil || found || lease.Request.ID != 0 {
		t.Fatalf("lease=%+v found=%v error=%v", lease, found, err)
	}
}

func TestPostgresClaimSkipsOwnedRunningRequestAndCanRecoverReleasedOne(t *testing.T) {
	t.Parallel()
	db := maintenanceDatabase(t,
		sqlStep{
			kind: "query", contains: "CASE request.status WHEN 'queued' THEN 0 ELSE 1 END", columns: maintenanceColumns(12),
			rows: [][]any{maintenanceCandidate(7, "running", false, nil), maintenanceCandidate(8, "queued", false, nil)},
		},
		advisoryStep(7, false),
		advisoryStep(8, true),
		sqlStep{kind: "exec", contains: "SET status = 'running'", affected: 1},
		unlockStep(8),
	)
	repository := mustPostgresRepository(t, db)
	lease, found, err := repository.Claim(context.Background(), "worker")
	if err != nil || !found || lease.Request.ID != 8 {
		t.Fatalf("lease=%+v found=%v error=%v", lease, found, err)
	}
	if err := repository.Release(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresClaimKeysetDoesNotStarveBeyondOwnedBatch(t *testing.T) {
	t.Parallel()
	firstBatch := make([][]any, 0, claimBatchSize)
	steps := []sqlStep{{
		kind: "query", contains: "FOR UPDATE OF request SKIP LOCKED", columns: maintenanceColumns(12), rows: firstBatch,
	}}
	for id := int64(1); id <= claimBatchSize; id++ {
		firstBatch = append(firstBatch, maintenanceCandidate(id, "running", false, nil))
		steps = append(steps, advisoryStep(id, false))
	}
	steps[0].rows = firstBatch
	steps = append(steps,
		sqlStep{
			kind: "query", contains: ") > ($2::integer, $3::timestamptz, $4::bigint)", columns: maintenanceColumns(12),
			rows: [][]any{maintenanceCandidate(65, "running", false, nil)},
			check: func(arguments []any) error {
				if len(arguments) != 5 || arguments[0] != true || arguments[1] != int32(1) || arguments[3] != int64(64) {
					return fmt.Errorf("cursor arguments=%v", arguments)
				}
				return nil
			},
		},
		advisoryStep(65, true),
		sqlStep{kind: "exec", contains: "SET status = 'running'", affected: 1},
		unlockStep(65),
	)
	db := maintenanceDatabase(t, steps...)
	repository := mustPostgresRepository(t, db)
	lease, found, err := repository.Claim(context.Background(), "worker")
	if err != nil || !found || lease.Request.ID != 65 {
		t.Fatalf("lease=%+v found=%v error=%v", lease, found, err)
	}
	if err := repository.Release(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresGuardCatchesFinalityAdvanceBeforeExecution(t *testing.T) {
	t.Parallel()
	db := maintenanceDatabase(t,
		sqlStep{kind: "query", contains: "FOR UPDATE OF request SKIP LOCKED", columns: maintenanceColumns(12), rows: [][]any{maintenanceCandidate(7, "queued", false, "50")}},
		advisoryStep(7, true),
		sqlStep{kind: "exec", contains: "SET status = 'running'", affected: 1},
		sqlStep{kind: "query", contains: "WHERE request.id = $1", columns: maintenanceColumns(2), rows: [][]any{{"running", "150"}}},
		sqlStep{
			kind: "exec", contains: "SET status = 'failed'", affected: 1,
			check: func(arguments []any) error {
				if len(arguments) != 2 || !strings.Contains(*arguments[1].(*string), "finalized height 150") {
					return fmt.Errorf("failure arguments=%v", arguments)
				}
				return nil
			},
		},
		unlockStep(7),
	)
	repository := mustPostgresRepository(t, db)
	lease, found, err := repository.Claim(context.Background(), "worker")
	if err != nil || !found {
		t.Fatalf("found=%v error=%v", found, err)
	}
	err = repository.GuardFinalized(context.Background(), lease)
	if !errors.Is(err, ErrFinalizedRange) {
		t.Fatalf("guard error=%v", err)
	}
	if err := repository.Fail(context.Background(), lease, err); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresExplicitFinalityOverrideStillVerifiesRunningLease(t *testing.T) {
	t.Parallel()
	candidate := maintenanceCandidate(7, "queued", true, "150")
	db := maintenanceDatabase(t,
		sqlStep{kind: "query", contains: "FOR UPDATE OF request SKIP LOCKED", columns: maintenanceColumns(12), rows: [][]any{candidate}},
		advisoryStep(7, true),
		sqlStep{kind: "exec", contains: "SET status = 'running'", affected: 1},
		sqlStep{kind: "query", contains: "WHERE request.id = $1", columns: maintenanceColumns(2), rows: [][]any{{"running", "150"}}},
		sqlStep{kind: "exec", contains: "SET status = 'done'", affected: 1},
		unlockStep(7),
	)
	repository := mustPostgresRepository(t, db)
	lease, found, err := repository.Claim(context.Background(), "worker")
	if err != nil || !found || !lease.Request.AllowFinalized {
		t.Fatalf("lease=%+v found=%v error=%v", lease, found, err)
	}
	if err := repository.GuardFinalized(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
	if err := repository.Complete(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresFailureIsBoundedValidUTF8AndIdempotent(t *testing.T) {
	t.Parallel()
	candidate := maintenanceCandidate(7, "queued", true, nil)
	db := maintenanceDatabase(t,
		sqlStep{kind: "query", contains: "FOR UPDATE OF request SKIP LOCKED", columns: maintenanceColumns(12), rows: [][]any{candidate}},
		advisoryStep(7, true),
		sqlStep{kind: "exec", contains: "SET status = 'running'", affected: 1},
		sqlStep{
			kind: "exec", contains: "SET status = 'failed'", affected: 1,
			check: func(arguments []any) error {
				message := *arguments[1].(*string)
				if len(message) > maximumFailureBytes || !utf8.ValidString(message) || !strings.Contains(message, "bad") {
					return fmt.Errorf("invalid normalized failure length=%d valid=%v", len(message), utf8.ValidString(message))
				}
				return nil
			},
		},
		unlockStep(7),
	)
	repository := mustPostgresRepository(t, db)
	lease, found, err := repository.Claim(context.Background(), "worker")
	if err != nil || !found {
		t.Fatalf("found=%v error=%v", found, err)
	}
	cause := errors.New("bad\xff" + strings.Repeat("界", 2000))
	if err := repository.Fail(context.Background(), lease, cause); err != nil {
		t.Fatal(err)
	}
	if err := repository.Fail(context.Background(), lease, errors.New("different")); err != nil {
		t.Fatalf("repeated failure is not idempotent: %v", err)
	}
	if err := repository.Complete(context.Background(), lease); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("completion after failure error=%v", err)
	}
}

func TestPostgresInvalidPersistedRequestIsFailedNotDispatched(t *testing.T) {
	t.Parallel()
	candidate := maintenanceCandidate(7, "queued", false, nil)
	candidate[4] = "18446744073709551616"
	db := maintenanceDatabase(t,
		sqlStep{kind: "query", contains: "FOR UPDATE OF request SKIP LOCKED", columns: maintenanceColumns(12), rows: [][]any{candidate}},
		advisoryStep(7, true),
		sqlStep{
			kind: "exec", contains: "SET status = 'failed'", affected: 1,
			check: func(arguments []any) error {
				if !strings.Contains(*arguments[1].(*string), "from_block is not a canonical uint64") {
					return fmt.Errorf("failure=%v", arguments)
				}
				return nil
			},
		},
		unlockStep(7),
	)
	repository := mustPostgresRepository(t, db)
	_, found, err := repository.Claim(context.Background(), "worker")
	if err != nil || found {
		t.Fatalf("found=%v error=%v", found, err)
	}
}

func maintenanceCandidate(id int64, status string, allowFinalized bool, finalized any) []any {
	statusRank := int64(0)
	if status == "running" {
		statusRank = 1
	}
	return []any{
		id, "1", "repair", "core", "100", "199", allowFinalized,
		"operator requested gap repair", status, finalized, statusRank,
		time.Date(2026, 7, 20, 10, 0, int(id), 0, time.UTC),
	}
}

func advisoryStep(id int64, acquired bool) sqlStep {
	return sqlStep{
		kind: "query", contains: "pg_try_advisory_lock", columns: maintenanceColumns(1), rows: [][]any{{acquired}},
		check: func(arguments []any) error {
			want := id | int64(math.MinInt64)
			if len(arguments) != 1 || arguments[0] != want {
				return fmt.Errorf("advisory arguments=%v want=%d", arguments, want)
			}
			return nil
		},
	}
}

func unlockStep(id int64) sqlStep {
	return sqlStep{
		kind: "query", contains: "pg_advisory_unlock", columns: maintenanceColumns(1), rows: [][]any{{true}},
		check: func(arguments []any) error {
			want := id | int64(math.MinInt64)
			if len(arguments) != 1 || arguments[0] != want {
				return fmt.Errorf("unlock arguments=%v want=%d", arguments, want)
			}
			return nil
		},
	}
}

func mustPostgresRepository(t *testing.T, db *maintenanceFakeConn) *PostgresRepository {
	t.Helper()
	return &PostgresRepository{acquire: func(context.Context) (sessionConnection, error) { return db, nil }}
}
