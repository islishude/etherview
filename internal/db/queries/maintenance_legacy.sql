-- name: MaintenanceLegacyClaimCandidates :many
SELECT request.id, request.chain_id::text, request.operation, request.stage,
       request.from_block::text, request.to_block::text,
       request.allow_finalized, request.reason, request.status,
       finality.finalized_number::text,
       CASE request.status WHEN 'queued' THEN 0 ELSE 1 END AS status_rank,
       request.requested_at
FROM repair_requests AS request
LEFT JOIN chain_finality AS finality ON finality.chain_id = request.chain_id
WHERE request.status IN ('queued', 'running')
  AND (
      $2 = FALSE
      OR (
          CASE request.status WHEN 'queued' THEN 0 ELSE 1 END,
          request.requested_at,
          request.id
      ) > ($3::integer, $4::timestamptz, $5::bigint)
  )
ORDER BY CASE request.status WHEN 'queued' THEN 0 ELSE 1 END,
         request.requested_at, request.id
FOR UPDATE OF request SKIP LOCKED
LIMIT $1;

-- name: MaintenanceLegacyCompleteRequest :exec
UPDATE repair_requests
SET status = 'done', completed_at = clock_timestamp(), last_error = NULL
WHERE id = $1 AND status = 'running';

-- name: MaintenanceLegacyCurrentFinality :many
SELECT request.status, finality.finalized_number::text
FROM repair_requests AS request
LEFT JOIN chain_finality AS finality ON finality.chain_id = request.chain_id
WHERE request.id = $1
  AND request.chain_id = $2::numeric;

-- name: MaintenanceLegacyFailRequest :exec
UPDATE repair_requests
SET status = 'failed', completed_at = clock_timestamp(), last_error = $2
WHERE id = $1 AND status = 'running';

-- name: MaintenanceLegacyMarkRunning :exec
UPDATE repair_requests
SET status = 'running',
    started_at = COALESCE(started_at, clock_timestamp()),
    completed_at = NULL,
    last_error = NULL
WHERE id = $1
  AND status IN ('queued', 'running');

-- name: MaintenanceLegacyRejectCandidate :exec
UPDATE repair_requests
SET status = 'failed',
    started_at = COALESCE(started_at, clock_timestamp()),
    completed_at = clock_timestamp(),
    last_error = $2
WHERE id = $1
  AND status IN ('queued', 'running');

-- name: MaintenanceLegacyTryAdvisoryLock :many
SELECT pg_try_advisory_lock($1);

-- name: MaintenanceLegacyUnlockAdvisory :many
SELECT pg_advisory_unlock($1);
