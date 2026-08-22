-- name: AnalyticsWriteDeferDirty :exec
UPDATE chart_rollup_dirty_hours
SET attempts = attempts + 1, next_attempt_at = $4
WHERE chain_id = $1::numeric AND bucket_start = $2 AND generation = $3;

-- name: AnalyticsWriteDeleteDirty :exec
DELETE FROM chart_rollup_dirty_hours
WHERE chain_id = $1::numeric AND bucket_start = $2 AND generation = $3;

-- name: AnalyticsWriteDeleteRollup :exec
DELETE FROM chart_hourly_rollups
WHERE chain_id = $1::numeric AND bucket_start = $2;

-- name: AnalyticsWriteNextDirty :many
SELECT bucket_start, generation
FROM chart_rollup_dirty_hours
WHERE chain_id = $1::numeric AND next_attempt_at <= $2
ORDER BY bucket_start DESC
LIMIT 1
FOR UPDATE SKIP LOCKED;

-- name: AnalyticsWriteRecomputeRollup :exec
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
    computed_at = now();

-- name: AnalyticsWriteRefreshBackfill :exec
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
    updated_at = now();

-- name: AnalyticsWriteRollupLock :many
SELECT pg_try_advisory_xact_lock(hashtextextended('chart-rollup:' || $1, 0));

-- name: AnalyticsWriteRollupMetrics :many
SELECT count(dirty.bucket_start),
       COALESCE(extract(epoch FROM ($2::timestamptz - min(dirty.dirtied_at))), 0)::double precision,
       COALESCE(
           backfill.completed_blocks * 100.0 / NULLIF(backfill.total_blocks, 0),
           0
       )::double precision
FROM chart_rollup_backfill AS backfill
LEFT JOIN chart_rollup_dirty_hours AS dirty ON dirty.chain_id = backfill.chain_id
WHERE backfill.chain_id = $1::numeric
GROUP BY backfill.completed_blocks, backfill.total_blocks;

-- name: AnalyticsWriteSourceReadiness :many
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
FROM source;
