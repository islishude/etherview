package testdbguard

import (
	"context"
	"errors"
	"testing"
	"time"

	dbgen "github.com/islishude/etherview/internal/db/gen"
)

func TestCompareDatabaseEvidence(t *testing.T) {
	baseline := dbgen.TestDeadlockSnapshotRow{DatabaseID: 10, Deadlocks: 3, StatsReset: "epoch", StartedAt: "start"}
	for _, name := range []string{"unchanged", "deadlock", "reset", "restart", "identity", "decrease"} {
		t.Run(name, func(t *testing.T) {
			final := baseline
			switch name {
			case "deadlock":
				final.Deadlocks++
			case "reset":
				final.StatsReset = "new"
			case "restart":
				final.StartedAt = "new"
			case "identity":
				final.DatabaseID++
			case "decrease":
				final.Deadlocks--
			}
			if err := compare(baseline, final); (err == nil) != (name == "unchanged") {
				t.Fatalf("comparison: %v", err)
			}
		})
	}
}

type fakeSource struct {
	samples   int
	snapshots int
	failure   string
	delayed   bool
}

func (s *fakeSource) TestDeadlockSessions(context.Context) ([]dbgen.TestDeadlockSessionsRow, error) {
	s.samples++
	if s.failure == "query" {
		return nil, errors.New("secret connection string")
	}
	if s.failure == "visibility" {
		return []dbgen.TestDeadlockSessionsRow{{Pid: 12}}, nil
	}
	if s.failure == "busy" {
		return []dbgen.TestDeadlockSessionsRow{{Pid: 12, State: "idle"}}, nil
	}
	return nil, nil
}
func (s *fakeSource) TestDeadlockSnapshot(context.Context) (dbgen.TestDeadlockSnapshotRow, error) {
	s.snapshots++
	result := dbgen.TestDeadlockSnapshotRow{DatabaseID: 1}
	if s.delayed && s.snapshots >= 3 {
		result.Deadlocks = 1
	}
	return result, nil
}
func TestFinishRequiresCompleteEvidence(t *testing.T) {
	for _, name := range []string{"clean", "query", "visibility", "busy", "delayed", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			source := &fakeSource{failure: name, delayed: name == "delayed"}
			g := &Guard{source: source, interval: time.Millisecond, drain: 3 * time.Millisecond, settle: 5 * time.Millisecond}
			g.report.Baseline.DatabaseID = 1
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if name == "cancelled" {
				cancel()
			}
			err := g.finish(ctx)
			if (err == nil) != (name == "clean") {
				t.Fatalf("finish: %v", err)
			}
			if name == "query" && err.Error() != "deadlock guard: final sessions unavailable" {
				t.Fatal("leaked source error", err)
			}
		})
	}
}
func TestWaitDiagnosticsAreBounded(t *testing.T) {
	g := &Guard{}
	for i := range 200 {
		g.record([]dbgen.TestDeadlockSessionsRow{{Pid: int32(i), Blockers: []int32{1000}}})
	}
	if len(g.report.Waits) != 128 || g.report.Waits[0].Sessions[0].Pid != 72 {
		t.Fatal("unbounded or stale wait history")
	}
}

func TestConcurrentCancelsPeersAndRetainsOperationName(t *testing.T) {
	entered := make(chan struct{})
	marker := errors.New("domain failure")
	err := Concurrent(t.Context(), map[string]func(context.Context) error{
		"publisher": func(context.Context) error { <-entered; return marker },
		"replay":    func(ctx context.Context) error { close(entered); <-ctx.Done(); return ctx.Err() },
	})
	if !errors.Is(err, marker) || !errors.Is(err, context.Canceled) {
		t.Fatalf("lost outcomes: %v", err)
	}
}
