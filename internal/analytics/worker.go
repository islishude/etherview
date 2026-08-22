package analytics

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/islishude/etherview/internal/db/gen"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

type RollupWorkerOptions struct {
	ServiceName   string
	ChainID       uint64
	PollInterval  time.Duration
	RetryInterval time.Duration
	Now           func() time.Time
	Logger        *slog.Logger
	Observer      RollupObserver
}

type RollupObserver interface {
	RecordAnalyticsRollup(result string)
	SetAnalyticsRollupState(dirtyHours int64, oldestDirtySeconds, backfillProgress float64)
}

func (options *RollupWorkerOptions) defaults() {
	if options.ServiceName == "" {
		options.ServiceName = "historical-analytics-rollup"
	}
	if options.PollInterval <= 0 {
		options.PollInterval = time.Second
	}
	if options.RetryInterval <= 0 {
		options.RetryInterval = min(options.PollInterval, 5*time.Second)
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
}

type RollupWorker struct {
	db      *sql.DB
	options RollupWorkerOptions
}

func NewRollupWorker(db *sql.DB, options RollupWorkerOptions) (*RollupWorker, error) {
	if db == nil {
		return nil, errors.New("analytics rollup worker requires a database")
	}
	options.defaults()
	options.ServiceName = strings.TrimSpace(options.ServiceName)
	if options.ServiceName == "" || len(options.ServiceName) > 128 || options.ChainID == 0 ||
		options.PollInterval <= 0 || options.RetryInterval <= 0 || options.Now == nil {
		return nil, errors.New("analytics rollup worker options are invalid")
	}
	return &RollupWorker{db: db, options: options}, nil
}

func (worker *RollupWorker) Name() string {
	if worker == nil || worker.options.ServiceName == "" {
		return "historical-analytics-rollup"
	}
	return worker.options.ServiceName
}

func (worker *RollupWorker) Run(ctx context.Context) error {
	if worker == nil || worker.db == nil {
		return errors.New("run nil analytics rollup worker")
	}
	delay := time.Duration(0)
	for {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		result, err := worker.RunOnce(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			worker.options.Logger.WarnContext(ctx, "analytics rollup recompute failed",
				"event", "analytics_rollup_failed", "component", worker.Name(),
				"error_code", "analytics_rollup_failed",
				"error_type", fmt.Sprintf("%T", err),
				"retry_in_ms", worker.options.RetryInterval.Milliseconds(),
			)
			if worker.options.Observer != nil {
				worker.options.Observer.RecordAnalyticsRollup("failed")
			}
			delay = worker.options.RetryInterval
			continue
		}
		if worker.options.Observer != nil {
			worker.options.Observer.SetAnalyticsRollupState(
				result.DirtyHours, result.OldestDirtySeconds, result.BackfillProgress,
			)
		}
		if result.Published {
			worker.options.Logger.InfoContext(ctx, "analytics rollup recompute completed",
				"event", "analytics_rollup_completed", "component", worker.Name(),
				"bucket_start", result.BucketStart.UTC().Format(time.RFC3339),
				"source_generation", result.Generation,
			)
			if worker.options.Observer != nil {
				worker.options.Observer.RecordAnalyticsRollup("succeeded")
			}
			delay = 0
		} else if !result.BucketStart.IsZero() {
			if worker.options.Observer != nil {
				worker.options.Observer.RecordAnalyticsRollup("retry")
			}
			delay = worker.options.RetryInterval
		} else {
			delay = worker.options.PollInterval
		}
	}
}

type RollupResult struct {
	Locked             bool
	Published          bool
	SourceReady        bool
	BucketStart        time.Time
	Generation         int64
	DirtyHours         int64
	OldestDirtySeconds float64
	BackfillProgress   float64
}

func (worker *RollupWorker) RunOnce(ctx context.Context) (RollupResult, error) {
	if worker == nil || worker.db == nil {
		return RollupResult{}, errors.New("run nil analytics rollup worker")
	}
	now := worker.options.Now().UTC()
	tx, err := worker.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return RollupResult{}, fmt.Errorf("begin analytics rollup: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	chainID := strconv.FormatUint(worker.options.ChainID, 10)
	var locked bool
	if err := tx.QueryRowContext(ctx, dbgen.AnalyticsWriteRollupLock, chainID).Scan(&locked); err != nil {
		return RollupResult{}, fmt.Errorf("lock analytics rollup: %w", err)
	}
	if !locked {
		return RollupResult{}, nil
	}
	result := RollupResult{Locked: true}
	err = tx.QueryRowContext(ctx, dbgen.AnalyticsWriteNextDirty, chainID, now).Scan(&result.BucketStart, &result.Generation)
	if errors.Is(err, sql.ErrNoRows) {
		if err := refreshBackfillState(ctx, tx, chainID); err != nil {
			return RollupResult{}, err
		}
		if err := readRollupMetrics(ctx, tx, chainID, now, &result); err != nil {
			return RollupResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return RollupResult{}, fmt.Errorf("commit analytics idle state: %w", err)
		}
		return result, nil
	}
	if err != nil {
		return RollupResult{}, fmt.Errorf("select analytics dirty hour: %w", err)
	}
	var canonicalCount, readyCount int64
	if err := tx.QueryRowContext(ctx, dbgen.AnalyticsWriteSourceReadiness, chainID, result.BucketStart).Scan(
		&canonicalCount, &readyCount,
	); err != nil {
		return RollupResult{}, fmt.Errorf("check analytics source readiness: %w", err)
	}
	if canonicalCount == 0 {
		if _, err := tx.ExecContext(ctx, dbgen.AnalyticsWriteDeleteRollup, chainID, result.BucketStart); err != nil {
			return RollupResult{}, fmt.Errorf("delete empty analytics rollup: %w", err)
		}
		if err := deleteDirtyGeneration(ctx, tx, chainID, result); err != nil {
			return RollupResult{}, err
		}
		result.Published, result.SourceReady = true, true
	} else if readyCount != canonicalCount {
		if _, err := tx.ExecContext(
			ctx, dbgen.AnalyticsWriteDeferDirty, chainID,
			result.BucketStart,
			result.Generation,
			now.Add(worker.options.RetryInterval),
		); err != nil {
			return RollupResult{}, fmt.Errorf("defer incomplete analytics hour: %w", err)
		}
	} else {
		write, err := tx.ExecContext(
			ctx, dbgen.AnalyticsWriteRecomputeRollup, chainID, result.BucketStart, result.Generation,
		)
		if err != nil {
			return RollupResult{}, fmt.Errorf("recompute analytics hour: %w", err)
		}
		affected, err := write.RowsAffected()
		if err != nil || affected != 1 {
			return RollupResult{}, fmt.Errorf("%w: analytics recompute affected %d rows", ErrCorruptData, affected)
		}
		if err := deleteDirtyGeneration(ctx, tx, chainID, result); err != nil {
			return RollupResult{}, err
		}
		result.Published, result.SourceReady = true, true
	}
	if err := refreshBackfillState(ctx, tx, chainID); err != nil {
		return RollupResult{}, err
	}
	if err := readRollupMetrics(ctx, tx, chainID, now, &result); err != nil {
		return RollupResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return RollupResult{}, fmt.Errorf("commit analytics rollup: %w", err)
	}
	return result, nil
}

func readRollupMetrics(
	ctx context.Context,
	tx *sql.Tx,
	chainID string,
	now time.Time,
	result *RollupResult,
) error {
	if err := tx.QueryRowContext(ctx, dbgen.AnalyticsWriteRollupMetrics, chainID, now).Scan(
		&result.DirtyHours,
		&result.OldestDirtySeconds,
		&result.BackfillProgress,
	); err != nil {
		return fmt.Errorf("read analytics rollup metrics: %w", err)
	}
	return nil
}

func deleteDirtyGeneration(ctx context.Context, tx *sql.Tx, chainID string, result RollupResult) error {
	deleted, err := tx.ExecContext(ctx, dbgen.AnalyticsWriteDeleteDirty, chainID, result.BucketStart, result.Generation)
	if err != nil {
		return fmt.Errorf("delete analytics dirty generation: %w", err)
	}
	affected, err := deleted.RowsAffected()
	if err != nil || affected != 1 {
		return fmt.Errorf("%w: analytics dirty generation changed during recompute", ErrCorruptData)
	}
	return nil
}

func refreshBackfillState(ctx context.Context, tx *sql.Tx, chainID string) error {
	if _, err := tx.ExecContext(ctx, dbgen.AnalyticsWriteRefreshBackfill, chainID); err != nil {
		return fmt.Errorf("refresh analytics backfill state: %w", err)
	}
	return nil
}
