-- name: MaintenanceLegacyClaimCandidates :many
SELECT request.id, request.chain_id::text, request.operation, request.stage,
       request.from_block::text, request.to_block::text,
       request.allow_finalized, request.reason, request.status,
       finality.finalized_number,
       CASE request.status WHEN 'queued' THEN 0 ELSE 1 END AS status_rank,
       request.requested_at
FROM repair_requests AS request
LEFT JOIN chain_finality AS finality ON finality.chain_id = request.chain_id
WHERE request.status IN ('queued', 'running')
  AND (
      sqlc.arg('has_cursor')::boolean = FALSE
      OR (
          CASE request.status WHEN 'queued' THEN 0 ELSE 1 END,
          request.requested_at,
          request.id
      ) > (sqlc.arg('cursor_status_rank')::integer, sqlc.arg('cursor_requested_at')::timestamptz, sqlc.arg('cursor_id')::bigint)
  )
ORDER BY CASE request.status WHEN 'queued' THEN 0 ELSE 1 END,
         request.requested_at, request.id
FOR UPDATE OF request SKIP LOCKED
LIMIT sqlc.arg('limit');

-- name: MaintenanceLegacyCompleteRequest :execrows
UPDATE repair_requests
SET status = 'done', completed_at = clock_timestamp(), last_error = NULL
WHERE id = $1 AND status = 'running';

-- name: MaintenanceLegacyCurrentFinality :one
SELECT request.status, finality.finalized_number
FROM repair_requests AS request
LEFT JOIN chain_finality AS finality ON finality.chain_id = request.chain_id
WHERE request.id = sqlc.arg('i_d')
  AND request.chain_id = sqlc.arg('chain_id')::numeric;

-- name: MaintenanceLegacyFailRequest :execrows
UPDATE repair_requests
SET status = 'failed', completed_at = clock_timestamp(), last_error = $2
WHERE id = $1 AND status = 'running';

-- name: MaintenanceLegacyMarkRunning :execrows
UPDATE repair_requests
SET status = 'running',
    started_at = COALESCE(started_at, clock_timestamp()),
    completed_at = NULL,
    last_error = NULL
WHERE id = $1
  AND status IN ('queued', 'running');

-- name: MaintenanceLegacyRejectCandidate :execrows
UPDATE repair_requests
SET status = 'failed',
    started_at = COALESCE(started_at, clock_timestamp()),
    completed_at = clock_timestamp(),
    last_error = $2
WHERE id = $1
  AND status IN ('queued', 'running');

-- name: MaintenanceLegacyTryAdvisoryLock :one
SELECT pg_try_advisory_lock($1);

-- name: MaintenanceLegacyUnlockAdvisory :one
SELECT pg_advisory_unlock($1);
