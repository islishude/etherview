-- name: GetSyncRuntimeStatus :one
SELECT COALESCE(latest_number::text, '')::text AS latest_number,
       COALESCE(indexed_number::text, '')::text AS indexed_number,
       COALESCE(highest_covered_number::text, '')::text AS highest_covered_number,
       backfill_complete,
       ready,
       last_poll_at,
       last_error_code
FROM sync_runtime_status
WHERE chain_id = sqlc.arg(chain_id)::numeric;

-- name: GetRuntimeEventReplayBounds :one
SELECT COALESCE(MIN(id), 0)::bigint AS minimum_id,
       COALESCE(MAX(id), 0)::bigint AS maximum_id
FROM runtime_events
WHERE chain_id = sqlc.arg(chain_id)::numeric;

-- name: ListRuntimeEvents :many
WITH selected AS (
    SELECT id, event_type, payload, created_at
    FROM runtime_events
    WHERE chain_id = sqlc.arg(chain_id)::numeric
      AND (NOT sqlc.arg(has_after)::boolean OR id > sqlc.arg(after_id)::bigint)
    ORDER BY
        CASE WHEN NOT sqlc.arg(has_after)::boolean THEN id END DESC,
        CASE WHEN sqlc.arg(has_after)::boolean THEN id END ASC
    LIMIT sqlc.arg(page_limit)
)
SELECT id, event_type, payload, created_at
FROM selected
ORDER BY id;
