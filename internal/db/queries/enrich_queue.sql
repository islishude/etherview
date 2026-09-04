-- name: EnrichSelectExhaustedCandidate :many
SELECT exhausted_job.id
FROM durable_jobs AS exhausted_job
WHERE exhausted_job.kind = 'enrichment'
  AND exhausted_job.attempts >= exhausted_job.max_attempts
  AND exhausted_job.claimed_generation > exhausted_job.completed_generation
  AND exhausted_job.requested_generation <= exhausted_job.claimed_generation
  AND $1::jsonb ? (exhausted_job.stage || '@' || exhausted_job.stage_version::text)
  AND (
      exhausted_job.stage <> 'abi'
      OR EXISTS (
          SELECT 1
          FROM published_block_stage_results AS dependency
          WHERE dependency.chain_id = exhausted_job.chain_id
            AND dependency.block_hash = decode(substr(exhausted_job.payload->>'block_hash', 3), 'hex')
            AND dependency.stage = 'proxy'
            AND dependency.stage_version = $2::bigint
            AND dependency.state IN ('complete', 'unavailable')
      )
  )
  AND (
      exhausted_job.stage <> 'holder'
      OR (
          EXISTS (
              SELECT 1 FROM published_block_stage_results AS dependency
              WHERE dependency.chain_id = exhausted_job.chain_id
                AND dependency.block_hash = decode(substr(exhausted_job.payload->>'block_hash', 3), 'hex')
                AND dependency.stage = 'token' AND dependency.stage_version = 1
                AND dependency.state = 'complete'
          )
          AND EXISTS (
              SELECT 1 FROM published_block_stage_results AS dependency
              WHERE dependency.chain_id = exhausted_job.chain_id
                AND dependency.block_hash = decode(substr(exhausted_job.payload->>'block_hash', 3), 'hex')
                AND dependency.stage = 'proxy' AND dependency.stage_version = $2::bigint
                AND dependency.state IN ('complete', 'unavailable')
          )
      )
  )
  AND (
      (exhausted_job.status = 'queued' AND exhausted_job.available_at <= clock_timestamp())
      OR (exhausted_job.status = 'leased' AND exhausted_job.lease_expires_at <= clock_timestamp())
  )
ORDER BY exhausted_job.available_at, exhausted_job.id
LIMIT 1;

-- name: EnrichLockExhaustedJob :many
SELECT exhausted_job.id, exhausted_job.chain_id::text,
       exhausted_job.stage, exhausted_job.stage_version,
       exhausted_job.attempts, exhausted_job.max_attempts,
       exhausted_job.payload, exhausted_job.claimed_generation,
       COALESCE(exhausted_job.last_error, 'maximum attempts exhausted')
FROM durable_jobs AS exhausted_job
WHERE exhausted_job.id = $1
  AND exhausted_job.kind = 'enrichment'
  AND exhausted_job.attempts >= exhausted_job.max_attempts
  AND exhausted_job.claimed_generation > exhausted_job.completed_generation
  AND exhausted_job.requested_generation <= exhausted_job.claimed_generation
  AND $2::jsonb ? (exhausted_job.stage || '@' || exhausted_job.stage_version::text)
  AND (
      exhausted_job.stage <> 'abi'
      OR EXISTS (
          SELECT 1
          FROM published_block_stage_results AS dependency
          WHERE dependency.chain_id = exhausted_job.chain_id
            AND dependency.block_hash = decode(substr(exhausted_job.payload->>'block_hash', 3), 'hex')
            AND dependency.stage = 'proxy'
            AND dependency.stage_version = $3::bigint
            AND dependency.state IN ('complete', 'unavailable')
      )
  )
  AND (
      exhausted_job.stage <> 'holder'
      OR (
          EXISTS (
              SELECT 1 FROM published_block_stage_results AS dependency
              WHERE dependency.chain_id = exhausted_job.chain_id
                AND dependency.block_hash = decode(substr(exhausted_job.payload->>'block_hash', 3), 'hex')
                AND dependency.stage = 'token' AND dependency.stage_version = 1
                AND dependency.state = 'complete'
          )
          AND EXISTS (
              SELECT 1 FROM published_block_stage_results AS dependency
              WHERE dependency.chain_id = exhausted_job.chain_id
                AND dependency.block_hash = decode(substr(exhausted_job.payload->>'block_hash', 3), 'hex')
                AND dependency.stage = 'proxy' AND dependency.stage_version = $3::bigint
                AND dependency.state IN ('complete', 'unavailable')
          )
      )
  )
  AND (
      (exhausted_job.status = 'queued' AND exhausted_job.available_at <= clock_timestamp())
      OR (exhausted_job.status = 'leased' AND exhausted_job.lease_expires_at <= clock_timestamp())
  )
FOR UPDATE;

-- name: EnrichSelectClaimCandidate :many
SELECT candidate_job.id, candidate_job.chain_id::text,
       candidate_job.stage, candidate_job.stage_version,
       candidate_job.attempts, candidate_job.max_attempts,
       candidate_job.payload, candidate_job.requested_generation
FROM durable_jobs AS candidate_job
WHERE candidate_job.kind = 'enrichment'
  AND (candidate_job.attempts < candidate_job.max_attempts
       OR candidate_job.requested_generation > candidate_job.claimed_generation)
  AND $1::jsonb ? (candidate_job.stage || '@' || candidate_job.stage_version::text)
  AND (
      candidate_job.stage <> 'abi'
      OR EXISTS (
          SELECT 1
          FROM published_block_stage_results AS dependency
          WHERE dependency.chain_id = candidate_job.chain_id
            AND dependency.block_hash = decode(substr(candidate_job.payload->>'block_hash', 3), 'hex')
            AND dependency.stage = 'proxy'
            AND dependency.stage_version = $2::bigint
            AND dependency.state IN ('complete', 'unavailable')
      )
  )
  AND (
      candidate_job.stage <> 'holder'
      OR (
          EXISTS (
              SELECT 1 FROM published_block_stage_results AS dependency
              WHERE dependency.chain_id = candidate_job.chain_id
                AND dependency.block_hash = decode(substr(candidate_job.payload->>'block_hash', 3), 'hex')
                AND dependency.stage = 'token' AND dependency.stage_version = 1
                AND dependency.state = 'complete'
          )
          AND EXISTS (
              SELECT 1 FROM published_block_stage_results AS dependency
              WHERE dependency.chain_id = candidate_job.chain_id
                AND dependency.block_hash = decode(substr(candidate_job.payload->>'block_hash', 3), 'hex')
                AND dependency.stage = 'proxy' AND dependency.stage_version = $2::bigint
                AND dependency.state IN ('complete', 'unavailable')
          )
      )
  )
  AND (
      (candidate_job.status = 'queued' AND candidate_job.available_at <= clock_timestamp())
      OR (candidate_job.status = 'leased' AND candidate_job.lease_expires_at <= clock_timestamp())
  )
ORDER BY candidate_job.priority DESC, candidate_job.available_at,
         candidate_job.id
LIMIT 1;

-- name: EnrichClaimCandidate :many
UPDATE durable_jobs AS job
SET status = 'leased',
    attempts = CASE
        WHEN job.requested_generation > job.claimed_generation THEN 1
        ELSE job.attempts + 1
    END,
    claimed_generation = job.requested_generation,
    leased_generation = job.requested_generation,
    leased_by = $1,
    lease_token = $2,
    lease_expires_at = clock_timestamp() + ($3 * INTERVAL '1 microsecond'),
    result = NULL,
    last_error = CASE
        WHEN job.requested_generation > job.claimed_generation THEN NULL
        ELSE job.last_error
    END,
    updated_at = clock_timestamp()
WHERE job.id = $4
  AND job.kind = 'enrichment'
  AND job.chain_id = $5::numeric
  AND job.stage = $6
  AND job.stage_version = $7
  AND job.payload->>'block_hash' = $8
  AND job.payload->>'block_number' = $9
  AND (job.attempts < job.max_attempts
       OR job.requested_generation > job.claimed_generation)
  AND $10::jsonb ? (job.stage || '@' || job.stage_version::text)
  AND (
      job.stage <> 'abi'
      OR EXISTS (
          SELECT 1
          FROM published_block_stage_results AS dependency
          WHERE dependency.chain_id = job.chain_id
            AND dependency.block_hash = decode(substr(job.payload->>'block_hash', 3), 'hex')
            AND dependency.stage = 'proxy'
            AND dependency.stage_version = $11::bigint
            AND dependency.state IN ('complete', 'unavailable')
      )
  )
  AND (
      job.stage <> 'holder'
      OR (
          EXISTS (
              SELECT 1 FROM published_block_stage_results AS dependency
              WHERE dependency.chain_id = job.chain_id
                AND dependency.block_hash = decode(substr(job.payload->>'block_hash', 3), 'hex')
                AND dependency.stage = 'token' AND dependency.stage_version = 1
                AND dependency.state = 'complete'
          )
          AND EXISTS (
              SELECT 1 FROM published_block_stage_results AS dependency
              WHERE dependency.chain_id = job.chain_id
                AND dependency.block_hash = decode(substr(job.payload->>'block_hash', 3), 'hex')
                AND dependency.stage = 'proxy' AND dependency.stage_version = $11::bigint
                AND dependency.state IN ('complete', 'unavailable')
          )
      )
  )
  AND (
      (job.status = 'queued' AND job.available_at <= clock_timestamp())
      OR (job.status = 'leased' AND job.lease_expires_at <= clock_timestamp())
  )
RETURNING job.id, job.chain_id::text, job.stage, job.stage_version,
          job.attempts, job.max_attempts, job.payload, job.leased_generation;
