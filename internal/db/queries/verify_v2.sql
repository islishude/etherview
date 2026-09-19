-- name: VerifyV2SubmitJob :one
INSERT INTO verification_jobs (
    id, kind, language, catalog_language, compiler_version, compiler_platform,
    catalog_generation_id,
    compiler_digest, executor_kind, execution_policy, executor_digest,
    chain_id, address, code_hash, block_hash,
    request, request_payload, request_digest, max_attempts
) VALUES (
    sqlc.arg('id')::uuid, sqlc.arg('kind'), sqlc.narg('language'), sqlc.narg('catalog_language'), sqlc.narg('compiler_version'), sqlc.narg('compiler_platform'), sqlc.narg('catalog_generation_id'), sqlc.arg('compiler_digest'), NULL, NULL, NULL,
    sqlc.narg('chain_id')::numeric, sqlc.arg('address'), sqlc.arg('code_hash'), sqlc.arg('block_hash'), sqlc.arg('request')::jsonb, sqlc.arg('request_payload'), sqlc.arg('request_digest'), sqlc.arg('max_attempts')
)
ON CONFLICT (request_digest) WHERE status IN ('queued', 'running', 'succeeded')
DO NOTHING
RETURNING id::text, kind, language, compiler_version, compiler_platform,
          catalog_generation_id, compiler_digest, executor_kind,
          execution_policy, executor_digest, request_payload, request_digest,
          status, outcome_kind, outcome, error_code, attempt_count,
          max_attempts, created_at, updated_at;

-- name: VerifyV2FindActiveJobByDigest :one
SELECT id::text, kind, language, compiler_version, compiler_platform,
       catalog_generation_id, compiler_digest, executor_kind,
       execution_policy, executor_digest, request_payload, request_digest,
       status, outcome_kind, outcome, error_code, attempt_count,
       max_attempts, created_at, updated_at
FROM verification_jobs
WHERE request_digest = sqlc.arg('request_digest')::bytea
  AND status IN ('queued', 'running', 'succeeded')
ORDER BY created_at, id
LIMIT 1;

-- name: VerifyV2ClaimRunnable :one
WITH exhausted AS (
    UPDATE verification_jobs
    SET status = 'failed', error_code = 'attempts_exhausted',
        leased_by = NULL, lease_token = NULL, lease_expires_at = NULL,
        updated_at = clock_timestamp()
    WHERE id = (
        SELECT id FROM verification_jobs
        WHERE (status = 'queued' OR (status = 'running' AND lease_expires_at <= clock_timestamp()))
          AND (kind IN ('proxy', 'sourcify', 'sourcify_from_etherscan')
               OR (sqlc.arg('solidity_enabled')::boolean AND language IN ('solidity', 'yul'))
               OR (sqlc.arg('geas_enabled')::boolean AND language = 'geas')
               OR (sqlc.arg('vyper_enabled')::boolean AND language = 'vyper')
               OR (sqlc.arg('vyper_prepared_enabled')::boolean AND language = 'vyper' AND compiler_digest IS NOT NULL AND executor_kind = 'etherview_vyper_v3'))
          AND attempt_count >= max_attempts
        ORDER BY created_at, id FOR UPDATE SKIP LOCKED LIMIT 1
    )
    RETURNING id
), candidate AS (
    SELECT id FROM verification_jobs
    WHERE (status = 'queued' OR (status = 'running' AND lease_expires_at <= clock_timestamp()))
      AND (kind IN ('proxy', 'sourcify', 'sourcify_from_etherscan')
           OR (sqlc.arg('solidity_enabled')::boolean AND language IN ('solidity', 'yul'))
           OR (sqlc.arg('geas_enabled')::boolean AND language = 'geas')
               OR (sqlc.arg('vyper_enabled')::boolean AND language = 'vyper')
               OR (sqlc.arg('vyper_prepared_enabled')::boolean AND language = 'vyper' AND compiler_digest IS NOT NULL AND executor_kind = 'etherview_vyper_v3'))
      AND attempt_count < max_attempts
      AND NOT EXISTS (SELECT 1 FROM exhausted WHERE exhausted.id = verification_jobs.id)
    ORDER BY created_at, id FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE verification_jobs AS job
SET status = 'running', leased_by = sqlc.arg('leased_by'), lease_token = sqlc.arg('lease_token'),
    lease_expires_at = clock_timestamp() + (sqlc.arg('lease_microseconds')::bigint * INTERVAL '1 microsecond'),
    attempt_count = job.attempt_count + 1, updated_at = clock_timestamp()
FROM candidate
WHERE job.id = candidate.id
RETURNING job.id::text AS id, job.kind, job.language, job.compiler_version,
          job.compiler_platform, job.catalog_generation_id,
          job.compiler_digest, job.executor_kind, job.execution_policy,
          job.executor_digest, job.request_payload, job.request_digest,
          job.status, job.outcome_kind, job.outcome, job.error_code,
          job.attempt_count, job.max_attempts, job.created_at, job.updated_at;

-- name: VerifyV2LockRunningJob :one
SELECT id::text, kind, language, compiler_version, compiler_platform,
       catalog_generation_id, compiler_digest, executor_kind,
       execution_policy, executor_digest, request_payload, request_digest,
       status, outcome_kind, outcome, error_code, attempt_count,
       max_attempts, created_at, updated_at
FROM verification_jobs
WHERE id = sqlc.arg('id')::uuid
  AND status = 'running'
  AND lease_token = sqlc.arg('lease_token')
  AND lease_expires_at > clock_timestamp()
FOR UPDATE;

-- name: VerifyV2GetJob :one
SELECT id::text, kind, language, compiler_version, compiler_platform,
       catalog_generation_id, compiler_digest, executor_kind,
       execution_policy, executor_digest, request_payload, request_digest,
       status, outcome_kind, outcome, error_code, attempt_count,
       max_attempts, created_at, updated_at
FROM verification_jobs
WHERE id = sqlc.arg('job_id')::uuid;
