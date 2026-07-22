-- name: ListRepairRequests :many
SELECT id, operation, stage,
       from_block::text AS from_block,
       to_block::text AS to_block,
       allow_finalized, reason, status,
       requested_at, started_at, completed_at,
       CAST(last_error IS NOT NULL AS boolean) AS failure_present
FROM repair_requests
WHERE chain_id = sqlc.arg(chain_id)::numeric
ORDER BY requested_at DESC, id DESC
LIMIT sqlc.arg(row_limit);
