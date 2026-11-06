// Package testdbguard checks database-wide deadlock evidence for owned test runs.
// It is tooling, not an application retry or database admission policy.
package testdbguard

import (
	"context"
	"errors"
	"fmt"
	"time"

	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Report contains bounded diagnostics without query text, parameters or URLs.
type Report struct {
	Baseline         dbgen.TestDeadlockSnapshotRow   `json:"baseline"`
	Final            dbgen.TestDeadlockSnapshotRow   `json:"final"`
	Remaining        []dbgen.TestDeadlockSessionsRow `json:"remaining_sessions,omitempty"`
	Waits            []WaitSnapshot                  `json:"waits"`
	SamplingFailures int                             `json:"sampling_failures"`
	Complete         bool                            `json:"complete"`
	Failure          string                          `json:"failure,omitempty"`
}

type WaitSnapshot struct {
	At       time.Time                       `json:"at"`
	Sessions []dbgen.TestDeadlockSessionsRow `json:"sessions"`
}

type source interface {
	TestDeadlockSnapshot(context.Context) (dbgen.TestDeadlockSnapshotRow, error)
	TestDeadlockSessions(context.Context) ([]dbgen.TestDeadlockSessionsRow, error)
}

// Guard owns an observer connection and a sampler. Finish must run after the
// caller has stopped all test work and closed its business connections.
type Guard struct {
	conn                    *pgxpool.Pool
	source                  source
	report                  Report
	stop                    context.CancelFunc
	done                    chan struct{}
	interval, drain, settle time.Duration
}

func Start(ctx context.Context, databaseURL string) (*Guard, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("deadlock guard: invalid database configuration")
	}
	config.ConnConfig.RuntimeParams["application_name"] = "etherview-deadlock-observer"
	config.ConnConfig.RuntimeParams["stats_fetch_consistency"] = "none"
	config.ConnConfig.RuntimeParams["statement_timeout"] = "2000"
	config.ConnConfig.ConnectTimeout = 2 * time.Second
	config.MaxConns = 1
	config.MinConns = 0
	conn, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("deadlock guard: connect failed")
	}
	g := &Guard{conn: conn, source: dbgen.New(conn), interval: 250 * time.Millisecond, drain: 15 * time.Second, settle: 5 * time.Second}
	if err := g.begin(ctx); err != nil {
		conn.Close()
		return nil, err
	}
	return g, nil
}

func (g *Guard) begin(ctx context.Context) error {
	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	baseline, err := g.source.TestDeadlockSnapshot(queryCtx)
	if err != nil {
		return errors.New("deadlock guard: baseline unavailable")
	}
	sessions, err := g.source.TestDeadlockSessions(queryCtx)
	if err != nil {
		return errors.New("deadlock guard: session diagnostics unavailable")
	}
	if err := visible(sessions); err != nil {
		return err
	}
	if len(sessions) != 0 {
		return errors.New("deadlock guard: database must be exclusive and idle before starting")
	}
	g.report.Baseline = baseline
	g.done = make(chan struct{})
	sampleCtx, stop := context.WithCancel(context.Background())
	g.stop = stop
	go func() {
		defer close(g.done)
		ticker := time.NewTicker(g.interval)
		defer ticker.Stop()
		for {
			select {
			case <-sampleCtx.Done():
				return
			case <-ticker.C:
				queryCtx, cancel := context.WithTimeout(sampleCtx, 2*time.Second)
				rows, err := g.source.TestDeadlockSessions(queryCtx)
				cancel()
				if err != nil {
					g.report.SamplingFailures++
					continue
				}
				g.record(rows)
			}
		}
	}()
	return nil
}

func visible(rows []dbgen.TestDeadlockSessionsRow) error {
	if len(rows) > 1024 {
		return errors.New("deadlock guard: session diagnostic limit exceeded")
	}
	for _, row := range rows {
		if row.State == "" {
			return errors.New("deadlock guard: insufficient session visibility")
		}
	}
	return nil
}

func (g *Guard) record(rows []dbgen.TestDeadlockSessionsRow) {

	participants := make(map[int32]bool)
	for _, row := range rows {
		if len(row.Blockers) > 0 {
			participants[row.Pid] = true
			for _, pid := range row.Blockers {
				participants[pid] = true
			}
		}
	}
	if len(participants) == 0 {
		return
	}
	waiting := make([]dbgen.TestDeadlockSessionsRow, 0, len(participants))
	for _, row := range rows {
		if participants[row.Pid] {
			waiting = append(waiting, row)
		}
	}

	if len(g.report.Waits) == 128 {
		copy(g.report.Waits, g.report.Waits[1:])
		g.report.Waits = g.report.Waits[:127]
	}
	g.report.Waits = append(g.report.Waits, WaitSnapshot{At: time.Now().UTC(), Sessions: waiting})
}

// Finish stops sampling, waits for client exit, then reads statistics over a
// bounded settling window. It never resets counters or terminates sessions.
// The caller supplies a detached cleanup context; cancellation means failure.
func (g *Guard) Finish(ctx context.Context) (Report, error) {
	g.stop()
	<-g.done
	defer func() {
		if g.conn != nil {
			g.conn.Close()
		}
	}()
	err := g.finish(ctx)
	g.report.Complete = err == nil
	if err != nil {
		g.report.Failure = err.Error()
	}
	return g.report, err
}

func (g *Guard) finish(ctx context.Context) error {
	deadline := time.Now().Add(g.drain)
	var quietSince time.Time
	for {
		queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		rows, err := g.source.TestDeadlockSessions(queryCtx)
		cancel()
		if err != nil {
			return errors.New("deadlock guard: final sessions unavailable")
		}
		if err := visible(rows); err != nil {
			return err
		}
		g.record(rows)
		g.report.Remaining = rows
		queryCtx, cancel = context.WithTimeout(ctx, 2*time.Second)
		final, err := g.source.TestDeadlockSnapshot(queryCtx)
		cancel()
		if err != nil {
			return errors.New("deadlock guard: final statistics unavailable")
		}
		g.report.Final = final
		if err := compare(g.report.Baseline, final); err != nil {
			return err
		}
		if len(rows) != 0 {
			if !quietSince.IsZero() || time.Now().After(deadline) {
				return errors.New("deadlock guard: business sessions did not quiesce")
			}
		} else {
			if quietSince.IsZero() {
				quietSince = time.Now()
			}
			if time.Since(quietSince) >= g.settle {
				return nil
			}
		}
		timer := time.NewTimer(g.interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.New("deadlock guard: incomplete after cancellation")
		case <-timer.C:
		}
	}
}

func compare(before, after dbgen.TestDeadlockSnapshotRow) error {
	if before.DatabaseID != after.DatabaseID || before.StatsReset != after.StatsReset || before.StartedAt != after.StartedAt || after.Deadlocks < before.Deadlocks {
		return errors.New("deadlock guard: database identity or statistics epoch changed")
	}
	if delta := after.Deadlocks - before.Deadlocks; delta != 0 {
		return fmt.Errorf("deadlock guard: %d new database deadlocks", delta)
	}
	return nil
}
