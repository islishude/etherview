package observability

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"time"

	dbaccess "github.com/islishude/etherview/internal/db"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5/pgtype"
)

type durableSnapshot struct {
	jobs                map[pair]uint64
	verification        map[string]uint64
	repairs             map[pair]uint64
	repairOldestSeconds float64
}

type durableSnapshotSource interface {
	Snapshot(context.Context) (durableSnapshot, error)
}

// PostgresMetricSource reads only bounded aggregate control-plane state. The
// rows remain PostgreSQL truth; no in-process counter is used to infer queue or
// repair status across replicas.
type PostgresMetricSource struct {
	db      *sql.DB
	chainID pgtype.Numeric
}

func NewPostgresMetricSource(db *sql.DB, chainID uint64) (*PostgresMetricSource, error) {
	if db == nil {
		return nil, errors.New("PostgreSQL metric source database is nil")
	}
	if chainID == 0 {
		return nil, errors.New("PostgreSQL metric source chain ID is zero")
	}
	return &PostgresMetricSource{
		db: db, chainID: pgtype.Numeric{Int: new(big.Int).SetUint64(chainID), Valid: true},
	}, nil
}

func (source *PostgresMetricSource) Snapshot(ctx context.Context) (durableSnapshot, error) {
	var rows []dbgen.OperationalMetricSnapshotRow
	err := dbaccess.WithQueries(ctx, source.db, func(queries *dbgen.Queries) error {
		var queryErr error
		rows, queryErr = queries.OperationalMetricSnapshot(ctx, source.chainID)
		return queryErr
	})
	if err != nil {
		return durableSnapshot{}, fmt.Errorf("query durable metric snapshot: %w", err)
	}
	snapshot := durableSnapshot{
		jobs: make(map[pair]uint64), verification: make(map[string]uint64), repairs: make(map[pair]uint64),
	}
	seenSnapshot := false
	for _, row := range rows {
		if row.MetricCount < 0 {
			return durableSnapshot{}, errors.New("durable metric snapshot contains a negative count")
		}
		count := uint64(row.MetricCount)
		switch row.MetricKind {
		case "durable":
			key := pair{First: boundedJobStage(row.MetricName), Second: boundedJobStatus(row.MetricStatus)}
			snapshot.jobs[key] += count
		case "verification":
			snapshot.verification[boundedJobStatus(row.MetricStatus)] += count
		case "repair":
			key := pair{First: boundedMaintenanceOperation(row.MetricName), Second: boundedJobStatus(row.MetricStatus)}
			snapshot.repairs[key] += count
		case "snapshot":
			if seenSnapshot || row.RepairOldestSeconds == nil || *row.RepairOldestSeconds < 0 ||
				math.IsNaN(*row.RepairOldestSeconds) || math.IsInf(*row.RepairOldestSeconds, 0) {
				return durableSnapshot{}, errors.New("durable metric snapshot marker is invalid")
			}
			snapshot.repairOldestSeconds = *row.RepairOldestSeconds
			seenSnapshot = true
		default:
			return durableSnapshot{}, errors.New("durable metric snapshot contains an unknown kind")
		}
	}
	if !seenSnapshot {
		return durableSnapshot{}, errors.New("durable metric snapshot marker is missing")
	}
	return snapshot, nil
}

// DurableCollectorOptions bounds the optional metric refresh loop.
type DurableCollectorOptions struct {
	Interval time.Duration
	Timeout  time.Duration
	Now      func() time.Time
	Logger   *slog.Logger
}

// DurableCollector refreshes PostgreSQL-backed metrics. A refresh error keeps
// the previous successful snapshot and retries; it never withdraws readiness.
type DurableCollector struct {
	source   durableSnapshotSource
	registry *Registry
	options  DurableCollectorOptions
}

func NewDurableCollector(source durableSnapshotSource, registry *Registry, options DurableCollectorOptions) (*DurableCollector, error) {
	if source == nil || registry == nil {
		return nil, errors.New("durable metric collector dependencies are nil")
	}
	if options.Interval <= 0 {
		options.Interval = 15 * time.Second
	}
	if options.Timeout <= 0 || options.Timeout > options.Interval {
		options.Timeout = min(5*time.Second, options.Interval)
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &DurableCollector{source: source, registry: registry, options: options}, nil
}

func (*DurableCollector) Name() string { return "durable-metrics" }

func (collector *DurableCollector) Run(ctx context.Context) error {
	collector.refresh(ctx)
	ticker := time.NewTicker(collector.options.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			collector.refresh(ctx)
		}
	}
}

func (collector *DurableCollector) refresh(ctx context.Context) {
	refreshCtx, cancel := context.WithTimeout(ctx, collector.options.Timeout)
	defer cancel()
	snapshot, err := collector.source.Snapshot(refreshCtx)
	if err != nil {
		collector.registry.RecordMetricsRefreshFailure()
		collector.options.Logger.WarnContext(ctx, "PostgreSQL metric refresh failed; retaining the last snapshot",
			"error_code", "metrics_refresh_failed",
			"error_type", fmt.Sprintf("%T", err),
		)
		return
	}
	collector.registry.replaceDurableSnapshot(snapshot, collector.options.Now())
}

func (registry *Registry) replaceDurableSnapshot(snapshot durableSnapshot, collectedAt time.Time) {
	jobs := make(map[pair]float64, len(snapshot.jobs))
	pending := make(map[string]float64)
	for key, count := range snapshot.jobs {
		bounded := pair{First: boundedJobStage(key.First), Second: boundedJobStatus(key.Second)}
		if bounded.Second != "queued" && bounded.Second != "leased" {
			continue
		}
		jobs[bounded] += float64(count)
		if bounded.Second == "queued" {
			pending[bounded.First] += float64(count)
		}
	}
	verification := make(map[string]float64, len(snapshot.verification))
	for status, count := range snapshot.verification {
		bounded := boundedJobStatus(status)
		if bounded != "queued" && bounded != "running" {
			continue
		}
		verification[bounded] += float64(count)
	}
	repairs := make(map[pair]float64, len(snapshot.repairs))
	for key, count := range snapshot.repairs {
		bounded := pair{First: boundedMaintenanceOperation(key.First), Second: boundedJobStatus(key.Second)}
		if bounded.Second != "queued" && bounded.Second != "running" {
			continue
		}
		repairs[bounded] += float64(count)
	}
	registry.mu.Lock()
	registry.durableJobs = jobs
	registry.jobsPending = pending
	registry.verificationCurrent = verification
	registry.repairCurrent = repairs
	registry.repairOldestQueued = snapshot.repairOldestSeconds
	registry.metricsLastRefresh = float64(collectedAt.Unix())
	registry.durableSnapshotReady = true
	registry.mu.Unlock()
}
