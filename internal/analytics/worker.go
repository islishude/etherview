package analytics

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	if err := tx.QueryRowContext(ctx, rollupLockSQL, chainID).Scan(&locked); err != nil {
		return RollupResult{}, fmt.Errorf("lock analytics rollup: %w", err)
	}
	if !locked {
		return RollupResult{}, nil
	}
	result := RollupResult{Locked: true}
	err = tx.QueryRowContext(ctx, nextDirtySQL, chainID, now).Scan(&result.BucketStart, &result.Generation)
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
	if err := tx.QueryRowContext(ctx, sourceReadinessSQL, chainID, result.BucketStart).Scan(
		&canonicalCount, &readyCount,
	); err != nil {
		return RollupResult{}, fmt.Errorf("check analytics source readiness: %w", err)
	}
	if canonicalCount == 0 {
		if _, err := tx.ExecContext(ctx, deleteRollupSQL, chainID, result.BucketStart); err != nil {
			return RollupResult{}, fmt.Errorf("delete empty analytics rollup: %w", err)
		}
		if err := deleteDirtyGeneration(ctx, tx, chainID, result); err != nil {
			return RollupResult{}, err
		}
		result.Published, result.SourceReady = true, true
	} else if readyCount != canonicalCount {
		if _, err := tx.ExecContext(
			ctx,
			deferDirtySQL,
			chainID,
			result.BucketStart,
			result.Generation,
			now.Add(worker.options.RetryInterval),
		); err != nil {
			return RollupResult{}, fmt.Errorf("defer incomplete analytics hour: %w", err)
		}
	} else {
		write, err := tx.ExecContext(
			ctx, recomputeRollupSQL, chainID, result.BucketStart, result.Generation,
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
	if err := tx.QueryRowContext(ctx, rollupMetricsSQL, chainID, now).Scan(
		&result.DirtyHours,
		&result.OldestDirtySeconds,
		&result.BackfillProgress,
	); err != nil {
		return fmt.Errorf("read analytics rollup metrics: %w", err)
	}
	return nil
}

func deleteDirtyGeneration(ctx context.Context, tx *sql.Tx, chainID string, result RollupResult) error {
	deleted, err := tx.ExecContext(ctx, deleteDirtySQL, chainID, result.BucketStart, result.Generation)
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
	if _, err := tx.ExecContext(ctx, refreshBackfillSQL, chainID); err != nil {
		return fmt.Errorf("refresh analytics backfill state: %w", err)
	}
	return nil
}

const rollupLockSQL = `
SELECT pg_try_advisory_xact_lock(hashtextextended('chart-rollup:' || $1, 0))`

const rollupMetricsSQL = `
SELECT count(dirty.bucket_start),
       COALESCE(extract(epoch FROM ($2::timestamptz - min(dirty.dirtied_at))), 0)::double precision,
       COALESCE(
           backfill.completed_blocks * 100.0 / NULLIF(backfill.total_blocks, 0),
           0
       )::double precision
FROM chart_rollup_backfill AS backfill
LEFT JOIN chart_rollup_dirty_hours AS dirty ON dirty.chain_id = backfill.chain_id
WHERE backfill.chain_id = $1::numeric
GROUP BY backfill.completed_blocks, backfill.total_blocks`

const nextDirtySQL = `
SELECT bucket_start, generation
FROM chart_rollup_dirty_hours
WHERE chain_id = $1::numeric AND next_attempt_at <= $2
ORDER BY bucket_start DESC
LIMIT 1
FOR UPDATE SKIP LOCKED`

const sourceReadinessSQL = `
WITH source AS (
    SELECT canonical.number, canonical.block_hash, stats.block_number AS stats_number,
           stats_result.state AS stats_state, token_result.state AS token_state,
           stats.execution_gas_fee_wei, stats.priority_fee_wei,
           stats.failed_transaction_count, stats.contract_creation_count
    FROM canonical_blocks AS canonical
    JOIN blocks AS block
      ON block.chain_id = canonical.chain_id
     AND block.number = canonical.number
     AND block.hash = canonical.block_hash
    LEFT JOIN block_statistics AS stats
      ON stats.chain_id = canonical.chain_id
     AND stats.block_number = canonical.number
     AND stats.block_hash = canonical.block_hash
     AND stats.canonical
    LEFT JOIN published_block_stage_results AS stats_result
      ON stats_result.chain_id = canonical.chain_id
     AND stats_result.block_number = canonical.number
     AND stats_result.block_hash = canonical.block_hash
     AND stats_result.stage = 'stats'
     AND stats_result.stage_version = 3
    LEFT JOIN published_block_stage_results AS token_result
      ON token_result.chain_id = canonical.chain_id
     AND token_result.block_number = canonical.number
     AND token_result.block_hash = canonical.block_hash
     AND token_result.stage = 'token'
     AND token_result.stage_version = 1
    WHERE canonical.chain_id = $1::numeric
      AND block.timestamp >= extract(epoch FROM $2::timestamptz)::numeric
      AND block.timestamp < extract(epoch FROM ($2::timestamptz + interval '1 hour'))::numeric
)
SELECT count(*),
       count(*) FILTER (
           WHERE stats_number IS NOT NULL
             AND stats_state = 'complete'
             AND token_state = 'complete'
             AND execution_gas_fee_wei IS NOT NULL
             AND priority_fee_wei IS NOT NULL
             AND failed_transaction_count IS NOT NULL
             AND contract_creation_count IS NOT NULL
       )
FROM source`

const recomputeRollupSQL = `
WITH source AS (
    SELECT canonical.number, stats.*
    FROM canonical_blocks AS canonical
    JOIN blocks AS block
      ON block.chain_id = canonical.chain_id
     AND block.number = canonical.number
     AND block.hash = canonical.block_hash
    JOIN block_statistics AS stats
      ON stats.chain_id = canonical.chain_id
     AND stats.block_number = canonical.number
     AND stats.block_hash = canonical.block_hash
     AND stats.canonical
    JOIN published_block_stage_results AS stats_result
      ON stats_result.chain_id = canonical.chain_id
     AND stats_result.block_number = canonical.number
     AND stats_result.block_hash = canonical.block_hash
     AND stats_result.stage = 'stats'
     AND stats_result.stage_version = 3
     AND stats_result.state = 'complete'
    JOIN published_block_stage_results AS token_result
      ON token_result.chain_id = canonical.chain_id
     AND token_result.block_number = canonical.number
     AND token_result.block_hash = canonical.block_hash
     AND token_result.stage = 'token'
     AND token_result.stage_version = 1
     AND token_result.state = 'complete'
    WHERE canonical.chain_id = $1::numeric
      AND block.timestamp >= extract(epoch FROM $2::timestamptz)::numeric
      AND block.timestamp < extract(epoch FROM ($2::timestamptz + interval '1 hour'))::numeric
), tokens AS (
    SELECT count(*) FILTER (
               WHERE event.standard = 'erc20'
                 AND event.event_kind IN ('transfer', 'mint', 'burn')
           ) AS erc20_transfer_count,
           count(*) FILTER (
               WHERE event.standard IN ('erc721', 'erc1155')
                 AND event.event_kind IN ('transfer', 'mint', 'burn')
           ) AS nft_transfer_count
    FROM token_events AS event
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = event.chain_id
     AND canonical.number = event.block_number
     AND canonical.block_hash = event.block_hash
    JOIN blocks AS block
      ON block.chain_id = canonical.chain_id
     AND block.number = canonical.number
     AND block.hash = canonical.block_hash
    WHERE event.chain_id = $1::numeric
      AND event.canonical
      AND block.timestamp >= extract(epoch FROM $2::timestamptz)::numeric
      AND block.timestamp < extract(epoch FROM ($2::timestamptz + interval '1 hour'))::numeric
)
INSERT INTO chart_hourly_rollups AS current (
    chain_id, bucket_start, source_generation, from_block, to_block, block_count,
    transaction_count, failed_transaction_count, contract_creation_count,
    gas_used, gas_limit, block_interval_seconds, block_interval_samples,
    base_fee_per_gas_sum, base_fee_samples, execution_gas_fee_wei,
    priority_fee_wei, burned_wei, blob_gas_used, blob_base_fee_per_gas_sum,
    blob_base_fee_samples, blob_burned_wei, erc20_transfer_count,
    nft_transfer_count
)
SELECT $1::numeric, $2::timestamptz, $3, min(number), max(number), count(*),
       sum(transaction_count), sum(failed_transaction_count), sum(contract_creation_count),
       sum(gas_used), sum(gas_limit), COALESCE(sum(block_interval_seconds), 0),
       count(block_interval_seconds), COALESCE(sum(base_fee_per_gas), 0),
       count(base_fee_per_gas), sum(execution_gas_fee_wei), sum(priority_fee_wei),
       COALESCE(sum(burned_wei), 0), COALESCE(sum(blob_gas_used), 0),
       COALESCE(sum(blob_base_fee_per_gas), 0), count(blob_base_fee_per_gas),
       COALESCE(sum(blob_burned_wei), 0),
       (SELECT erc20_transfer_count FROM tokens),
       (SELECT nft_transfer_count FROM tokens)
FROM source
HAVING count(*) > 0
ON CONFLICT (chain_id, bucket_start) DO UPDATE SET
    source_generation = EXCLUDED.source_generation,
    from_block = EXCLUDED.from_block,
    to_block = EXCLUDED.to_block,
    block_count = EXCLUDED.block_count,
    transaction_count = EXCLUDED.transaction_count,
    failed_transaction_count = EXCLUDED.failed_transaction_count,
    contract_creation_count = EXCLUDED.contract_creation_count,
    gas_used = EXCLUDED.gas_used,
    gas_limit = EXCLUDED.gas_limit,
    block_interval_seconds = EXCLUDED.block_interval_seconds,
    block_interval_samples = EXCLUDED.block_interval_samples,
    base_fee_per_gas_sum = EXCLUDED.base_fee_per_gas_sum,
    base_fee_samples = EXCLUDED.base_fee_samples,
    execution_gas_fee_wei = EXCLUDED.execution_gas_fee_wei,
    priority_fee_wei = EXCLUDED.priority_fee_wei,
    burned_wei = EXCLUDED.burned_wei,
    blob_gas_used = EXCLUDED.blob_gas_used,
    blob_base_fee_per_gas_sum = EXCLUDED.blob_base_fee_per_gas_sum,
    blob_base_fee_samples = EXCLUDED.blob_base_fee_samples,
    blob_burned_wei = EXCLUDED.blob_burned_wei,
    erc20_transfer_count = EXCLUDED.erc20_transfer_count,
    nft_transfer_count = EXCLUDED.nft_transfer_count,
    computed_at = now()`

const deleteRollupSQL = `
DELETE FROM chart_hourly_rollups
WHERE chain_id = $1::numeric AND bucket_start = $2`

const deleteDirtySQL = `
DELETE FROM chart_rollup_dirty_hours
WHERE chain_id = $1::numeric AND bucket_start = $2 AND generation = $3`

const deferDirtySQL = `
UPDATE chart_rollup_dirty_hours
SET attempts = attempts + 1, next_attempt_at = $4
WHERE chain_id = $1::numeric AND bucket_start = $2 AND generation = $3`

const refreshBackfillSQL = `
INSERT INTO chart_rollup_backfill AS current (
    chain_id, available_from, available_to, next_block, target_start_block,
    completed_blocks, total_blocks, complete, updated_at
)
SELECT $1::numeric,
       (SELECT min(bucket_start) FROM chart_hourly_rollups WHERE chain_id = $1::numeric),
       (SELECT max(bucket_start) FROM chart_hourly_rollups WHERE chain_id = $1::numeric),
       (
           SELECT min(canonical.number)
           FROM canonical_blocks AS canonical
           WHERE canonical.chain_id = $1::numeric
             AND (
                 NOT EXISTS (
                     SELECT 1 FROM published_block_stage_results AS result
                     WHERE result.chain_id = canonical.chain_id
                       AND result.block_number = canonical.number
                       AND result.block_hash = canonical.block_hash
                       AND result.stage = 'stats' AND result.stage_version = 3
                       AND result.state = 'complete'
                 )
                 OR NOT EXISTS (
                     SELECT 1 FROM published_block_stage_results AS result
                     WHERE result.chain_id = canonical.chain_id
                       AND result.block_number = canonical.number
                       AND result.block_hash = canonical.block_hash
                       AND result.stage = 'token' AND result.stage_version = 1
                       AND result.state = 'complete'
                 )
             )
       ),
       (SELECT configured_start FROM core_index_configuration WHERE chain_id = $1::numeric),
       (
           SELECT count(*)
           FROM canonical_blocks AS canonical
           WHERE canonical.chain_id = $1::numeric
             AND EXISTS (
                 SELECT 1 FROM published_block_stage_results AS result
                 WHERE result.chain_id = canonical.chain_id
                   AND result.block_number = canonical.number
                   AND result.block_hash = canonical.block_hash
                   AND result.stage = 'stats' AND result.stage_version = 3
                   AND result.state = 'complete'
             )
             AND EXISTS (
                 SELECT 1 FROM published_block_stage_results AS result
                 WHERE result.chain_id = canonical.chain_id
                   AND result.block_number = canonical.number
                   AND result.block_hash = canonical.block_hash
                   AND result.stage = 'token' AND result.stage_version = 1
                   AND result.state = 'complete'
             )
       ),
       (SELECT count(*) FROM canonical_blocks WHERE chain_id = $1::numeric),
       NOT EXISTS (
           SELECT 1
           FROM canonical_blocks AS canonical
           WHERE canonical.chain_id = $1::numeric
             AND (
                 NOT EXISTS (
                     SELECT 1 FROM published_block_stage_results AS result
                     WHERE result.chain_id = canonical.chain_id
                       AND result.block_number = canonical.number
                       AND result.block_hash = canonical.block_hash
                       AND result.stage = 'stats' AND result.stage_version = 3
                       AND result.state = 'complete'
                 )
                 OR NOT EXISTS (
                     SELECT 1 FROM published_block_stage_results AS result
                     WHERE result.chain_id = canonical.chain_id
                       AND result.block_number = canonical.number
                       AND result.block_hash = canonical.block_hash
                       AND result.stage = 'token' AND result.stage_version = 1
                       AND result.state = 'complete'
                 )
             )
       ) AND NOT EXISTS (
           SELECT 1 FROM chart_rollup_dirty_hours WHERE chain_id = $1::numeric
       ),
       now()
ON CONFLICT (chain_id) DO UPDATE SET
    available_from = EXCLUDED.available_from,
    available_to = EXCLUDED.available_to,
    next_block = EXCLUDED.next_block,
    target_start_block = EXCLUDED.target_start_block,
    completed_blocks = EXCLUDED.completed_blocks,
    total_blocks = EXCLUDED.total_blocks,
    complete = EXCLUDED.complete,
    updated_at = now()`
