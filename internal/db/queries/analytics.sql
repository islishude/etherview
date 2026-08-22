-- name: GetAnalyticsSnapshot :one
SELECT canonical.number::text AS block_number, canonical.block_hash
FROM canonical_blocks AS canonical
WHERE canonical.chain_id = sqlc.arg(chain_id)::numeric
ORDER BY canonical.number DESC
LIMIT 1;

-- name: GetAnalyticsCoverage :one
SELECT backfill.available_from,
       backfill.available_to,
       COALESCE(backfill.complete, false)::boolean AS complete,
       (SELECT count(*)::text
        FROM chart_rollup_dirty_hours
        WHERE chain_id = sqlc.arg(chain_id)::numeric)::text AS dirty_hours,
       CASE
           WHEN COALESCE(backfill.total_blocks, 0) = 0 THEN '0'::text
           ELSE trim(trailing '.' FROM trim(trailing '0' FROM
               round(backfill.completed_blocks::numeric * 100 / backfill.total_blocks, 18)::text
           ))::text
       END AS progress
FROM (SELECT 1) AS singleton
LEFT JOIN chart_rollup_backfill AS backfill
  ON backfill.chain_id = sqlc.arg(chain_id)::numeric;

-- name: CountDirtyAnalyticsHours :one
SELECT count(*)::text AS dirty_count
FROM chart_rollup_dirty_hours
WHERE chain_id = sqlc.arg(chain_id)::numeric
  AND bucket_start < sqlc.arg(to_time)::timestamptz
  AND bucket_start + interval '1 hour' > sqlc.arg(from_time)::timestamptz;

-- name: CountPendingAnalyticsSources :one
SELECT count(*)::text AS pending_count
FROM canonical_blocks AS canonical
JOIN blocks AS block
  ON block.chain_id = canonical.chain_id
 AND block.number = canonical.number
 AND block.hash = canonical.block_hash
LEFT JOIN published_block_stage_results AS stats_result
  ON stats_result.chain_id = canonical.chain_id
 AND stats_result.block_number = canonical.number
 AND stats_result.block_hash = canonical.block_hash
 AND stats_result.stage = 'stats'
 AND stats_result.stage_version = 3
 AND stats_result.state = 'complete'
LEFT JOIN published_block_stage_results AS token_result
  ON token_result.chain_id = canonical.chain_id
 AND token_result.block_number = canonical.number
 AND token_result.block_hash = canonical.block_hash
 AND token_result.stage = 'token'
 AND token_result.stage_version = 1
 AND token_result.state = 'complete'
WHERE canonical.chain_id = sqlc.arg(chain_id)::numeric
  AND block.timestamp >= extract(epoch FROM sqlc.arg(from_time)::timestamptz)::numeric
  AND block.timestamp < extract(epoch FROM sqlc.arg(to_time)::timestamptz)::numeric
  AND (stats_result.block_number IS NULL OR token_result.block_number IS NULL);

-- name: CountMissingAnalyticsRollups :one
SELECT count(*)::text AS missing_count
FROM canonical_blocks AS canonical
JOIN blocks AS block
  ON block.chain_id = canonical.chain_id
 AND block.number = canonical.number
 AND block.hash = canonical.block_hash
LEFT JOIN chart_hourly_rollups AS rollup
  ON rollup.chain_id = canonical.chain_id
 AND rollup.bucket_start = date_trunc(
     'hour',
     to_timestamp(block.timestamp::double precision),
     'UTC'
 )
WHERE canonical.chain_id = sqlc.arg(chain_id)::numeric
  AND block.timestamp >= extract(epoch FROM sqlc.arg(from_time)::timestamptz)::numeric
  AND block.timestamp < extract(epoch FROM sqlc.arg(to_time)::timestamptz)::numeric
  AND rollup.bucket_start IS NULL;

-- name: ListAnalyticsHours :many
WITH requested AS (
    SELECT (CASE sqlc.arg(bucket_interval)::text
               WHEN 'hour' THEN date_trunc('hour', bucket_start, 'UTC')
               WHEN 'day' THEN date_trunc('day', bucket_start, 'UTC')
               WHEN 'week' THEN date_trunc('week', bucket_start, 'UTC')
               WHEN 'month' THEN date_trunc('month', bucket_start, 'UTC')
           END)::timestamptz AS bucket_start,
           from_block, to_block, block_count, transaction_count,
           failed_transaction_count, contract_creation_count, gas_used,
           gas_limit, block_interval_samples, block_interval_seconds,
           base_fee_samples, base_fee_per_gas_sum, execution_gas_fee_wei,
           priority_fee_wei, burned_wei, blob_gas_used, blob_base_fee_samples,
           blob_base_fee_per_gas_sum, blob_burned_wei, erc20_transfer_count,
           nft_transfer_count
    FROM chart_hourly_rollups
    WHERE chain_id = sqlc.arg(chain_id)::numeric
      AND bucket_start < sqlc.arg(to_time)::timestamptz
      AND bucket_start + interval '1 hour' > sqlc.arg(from_time)::timestamptz
)
SELECT bucket_start,
       min(from_block)::text AS from_block,
       max(to_block)::text AS to_block,
       sum(block_count)::bigint AS block_count,
       sum(transaction_count)::text AS transaction_count,
       sum(failed_transaction_count)::text AS failed_transaction_count,
       sum(contract_creation_count)::text AS contract_creation_count,
       sum(gas_used)::text AS gas_used,
       sum(gas_limit)::text AS gas_limit,
       sum(block_interval_samples)::bigint AS block_interval_samples,
       sum(block_interval_seconds)::text AS block_interval_seconds,
       sum(base_fee_samples)::bigint AS base_fee_samples,
       sum(base_fee_per_gas_sum)::text AS base_fee_per_gas_sum,
       sum(execution_gas_fee_wei)::text AS execution_gas_fee_wei,
       sum(priority_fee_wei)::text AS priority_fee_wei,
       sum(burned_wei)::text AS burned_wei,
       sum(blob_gas_used)::text AS blob_gas_used,
       sum(blob_base_fee_samples)::bigint AS blob_base_fee_samples,
       sum(blob_base_fee_per_gas_sum)::text AS blob_base_fee_per_gas_sum,
       sum(blob_burned_wei)::text AS blob_burned_wei,
       sum(erc20_transfer_count)::text AS erc20_transfer_count,
       sum(nft_transfer_count)::text AS nft_transfer_count
FROM requested
GROUP BY bucket_start
ORDER BY bucket_start;
