-- name: VerifyV2SubmitJob :many
INSERT INTO verification_jobs (
    id, kind, language, catalog_language, compiler_version, compiler_platform,
    catalog_generation_id,
    compiler_digest, executor_kind, execution_policy, executor_digest,
    chain_id, address, code_hash, block_hash,
    request, request_payload, request_digest, max_attempts
) VALUES (
    $1::uuid, $2, $3, $4, $5, $6, $7, $8, NULL, NULL, NULL,
    $9::numeric, $10, $11, $12, $13::jsonb, $14, $15, $16
)
ON CONFLICT (request_digest) WHERE status IN ('queued', 'running', 'succeeded')
DO NOTHING
RETURNING id::text, kind, language, compiler_version, compiler_platform,
          catalog_generation_id, compiler_digest, executor_kind,
          execution_policy, executor_digest, request_payload, request_digest,
          status, outcome_kind, outcome, error_code, attempt_count,
          max_attempts, created_at, updated_at;

-- name: VerifyV2FindActiveJobByDigest :many
SELECT id::text, kind, language, compiler_version, compiler_platform,
       catalog_generation_id, compiler_digest, executor_kind,
       execution_policy, executor_digest, request_payload, request_digest,
       status, outcome_kind, outcome, error_code, attempt_count,
       max_attempts, created_at, updated_at
FROM verification_jobs
WHERE request_digest = $1::bytea
  AND status IN ('queued', 'running', 'succeeded')
ORDER BY created_at, id
LIMIT 1;

-- name: VerifyV2ClaimRunnable :many
WITH exhausted AS (
    UPDATE verification_jobs
    SET status = 'failed', error_code = 'attempts_exhausted',
        leased_by = NULL, lease_token = NULL, lease_expires_at = NULL,
        updated_at = clock_timestamp()
    WHERE id = (
        SELECT id FROM verification_jobs
        WHERE (status = 'queued' OR (status = 'running' AND lease_expires_at <= clock_timestamp()))
          AND (kind IN ('proxy', 'sourcify', 'sourcify_from_etherscan')
               OR ($4::boolean AND language IN ('solidity', 'yul'))
               OR ($5::boolean AND language = 'geas'))
          AND attempt_count >= max_attempts
        ORDER BY created_at, id FOR UPDATE SKIP LOCKED LIMIT 1
    )
    RETURNING id
), candidate AS (
    SELECT id FROM verification_jobs
    WHERE (status = 'queued' OR (status = 'running' AND lease_expires_at <= clock_timestamp()))
      AND (kind IN ('proxy', 'sourcify', 'sourcify_from_etherscan')
           OR ($4::boolean AND language IN ('solidity', 'yul'))
           OR ($5::boolean AND language = 'geas'))
      AND attempt_count < max_attempts
      AND NOT EXISTS (SELECT 1 FROM exhausted WHERE exhausted.id = verification_jobs.id)
    ORDER BY created_at, id FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE verification_jobs AS job
SET status = 'running', leased_by = $1, lease_token = $2,
    lease_expires_at = clock_timestamp() + ($3 * INTERVAL '1 microsecond'),
    attempt_count = job.attempt_count + 1, updated_at = clock_timestamp()
FROM candidate
WHERE job.id = candidate.id
RETURNING job.id::text, job.kind, job.language, job.compiler_version,
          job.compiler_platform, job.catalog_generation_id,
          job.compiler_digest, job.executor_kind, job.execution_policy,
          job.executor_digest, job.request_payload, job.request_digest,
          job.status, job.outcome_kind, job.outcome, job.error_code,
          job.attempt_count, job.max_attempts, job.created_at, job.updated_at;

-- name: VerifyV2LockRunningJob :many
SELECT id::text, kind, language, compiler_version, compiler_platform,
       catalog_generation_id, compiler_digest, executor_kind,
       execution_policy, executor_digest, request_payload, request_digest,
       status, outcome_kind, outcome, error_code, attempt_count,
       max_attempts, created_at, updated_at
FROM verification_jobs
WHERE id = $1::uuid
  AND status = 'running'
  AND lease_token = $2
  AND lease_expires_at > clock_timestamp()
FOR UPDATE;

-- name: VerifyV2GetJob :many
SELECT id::text, kind, language, compiler_version, compiler_platform,
       catalog_generation_id, compiler_digest, executor_kind,
       execution_policy, executor_digest, request_payload, request_digest,
       status, outcome_kind, outcome, error_code, attempt_count,
       max_attempts, created_at, updated_at
FROM verification_jobs
WHERE id = $1::uuid;
