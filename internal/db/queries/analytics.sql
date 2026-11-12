-- name: GetAnalyticsSnapshot :one
SELECT canonical.number::text AS block_number, canonical.block_hash,
       (rollup.bucket_start + interval '1 hour')::timestamptz AS completed_to
FROM chart_hourly_rollups AS rollup
JOIN canonical_blocks AS canonical
 ON canonical.chain_id = rollup.chain_id AND canonical.number = rollup.to_block
WHERE rollup.chain_id = sqlc.arg('chain_id')::numeric
ORDER BY canonical.number DESC, rollup.bucket_start DESC
LIMIT 1;

-- name: GetAnalyticsCoverage :one
SELECT backfill.available_from,
       backfill.available_to,
       COALESCE(backfill.complete, false)::boolean AS complete,
       COALESCE((SELECT dirty_hours FROM chart_dirty_totals WHERE chain_id=sqlc.arg('chain_id')::numeric),0)::text AS dirty_hours,
       CASE
           WHEN COALESCE(backfill.total_blocks, 0) = 0 THEN '0'::text
           ELSE trim(trailing '.' FROM trim(trailing '0' FROM
               round(backfill.completed_blocks::numeric * 100 / backfill.total_blocks, 18)::text
           ))::text
       END AS progress
FROM (SELECT 1) AS singleton
LEFT JOIN chart_rollup_backfill AS backfill
  ON backfill.chain_id = sqlc.arg('chain_id')::numeric;

-- name: CountDirtyAnalyticsHours :one
SELECT CASE WHEN EXISTS(
    SELECT 1 FROM chart_source_hours h
    JOIN chart_rollup_dirty_hours d USING(chain_id,bucket_start)
    LEFT JOIN chart_hourly_rollups r USING(chain_id,bucket_start)
    WHERE h.chain_id=sqlc.arg('chain_id')::numeric
      AND h.bucket_start<sqlc.arg('to_time')::timestamptz
      AND h.bucket_start+interval '1 hour'>sqlc.arg('from_time')::timestamptz
      AND h.first_block<=sqlc.arg('max_block_number')::numeric AND r.bucket_start IS NULL
) THEN '1' ELSE '0' END::text AS dirty_count;

-- name: CountPendingAnalyticsSources :one
SELECT CASE WHEN EXISTS(
    SELECT 1 FROM chart_source_hours h WHERE h.chain_id=sqlc.arg('chain_id')::numeric
      AND h.bucket_start<sqlc.arg('to_time')::timestamptz
      AND h.bucket_start+interval '1 hour'>sqlc.arg('from_time')::timestamptz
      AND h.first_pending<=sqlc.arg('max_block_number')::numeric
) THEN '1' ELSE '0' END::text AS pending_count;

-- name: CountMissingAnalyticsRollups :one
SELECT CASE WHEN EXISTS(
    SELECT 1 FROM chart_source_hours h LEFT JOIN chart_hourly_rollups r USING(chain_id,bucket_start)
    WHERE h.chain_id=sqlc.arg('chain_id')::numeric
      AND h.bucket_start<sqlc.arg('to_time')::timestamptz
      AND h.bucket_start+interval '1 hour'>sqlc.arg('from_time')::timestamptz
      AND h.first_block<=sqlc.arg('max_block_number')::numeric
      AND (r.bucket_start IS NULL OR LEAST(h.last_block,sqlc.arg('max_block_number')::numeric)>r.to_block)
) THEN '1' ELSE '0' END::text AS missing_count;

-- name: ListAnalyticsHours :many
WITH requested AS (
    SELECT (CASE sqlc.arg('bucket_interval')::text
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
    WHERE chain_id = sqlc.arg('chain_id')::numeric
      AND bucket_start < sqlc.arg('to_time')::timestamptz
      AND bucket_start + interval '1 hour' > sqlc.arg('from_time')::timestamptz
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
