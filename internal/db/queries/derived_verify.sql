-- name: DerivedVerifyEnqueueHistoricalScan :exec
INSERT INTO derived_verification_scans (
    compilation_id, chain_id, creator_address, creator_code_hash,
    valid_from_block, valid_to_block, cursor_block_number
) VALUES ($1::uuid, $2::numeric, $3, $4, $5::numeric, $6::numeric, $5::numeric)
ON CONFLICT (
    compilation_id, creator_address, creator_code_hash, valid_from_block
) DO NOTHING;

-- name: DerivedVerifyCreatorCodeEpochStart :many
WITH context_observation AS (
    SELECT observation.block_number
    FROM contract_code_observations AS observation
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = observation.chain_id
     AND canonical.number = observation.block_number
     AND canonical.block_hash = observation.block_hash
    WHERE observation.chain_id = $1::numeric
      AND observation.address = $2
      AND observation.code_hash = $3
      AND observation.block_number = $4::numeric
      AND observation.block_hash = $5
      AND observation.canonical
), last_different AS (
    SELECT max(observation.block_number) AS block_number
    FROM contract_code_observations AS observation
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = observation.chain_id
     AND canonical.number = observation.block_number
     AND canonical.block_hash = observation.block_hash
    CROSS JOIN context_observation AS context
    WHERE observation.chain_id = $1::numeric
      AND observation.address = $2
      AND observation.code_hash <> $3
      AND observation.block_number <= context.block_number
      AND observation.canonical
)
SELECT observation.block_number::text
FROM contract_code_observations AS observation
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = observation.chain_id
 AND canonical.number = observation.block_number
 AND canonical.block_hash = observation.block_hash
CROSS JOIN context_observation AS context
CROSS JOIN last_different AS boundary
WHERE observation.chain_id = $1::numeric
  AND observation.address = $2
  AND observation.code_hash = $3
  AND observation.block_number > COALESCE(boundary.block_number, -1::numeric)
  AND observation.block_number <= context.block_number
  AND observation.canonical
ORDER BY observation.block_number, observation.observed_at, observation.block_hash
LIMIT 1;

-- name: DerivedVerifyClaimScan :many
WITH exhausted AS (
    UPDATE derived_verification_scans
    SET status = 'failed', last_error = 'attempts_exhausted',
        leased_by = NULL, lease_token = NULL, lease_expires_at = NULL,
        updated_at = clock_timestamp()
    WHERE id = (
        SELECT id
        FROM derived_verification_scans
        WHERE (status = 'queued' OR
               (status = 'running' AND lease_expires_at <= clock_timestamp()))
          AND attempt_count >= max_attempts
        ORDER BY updated_at, id
        FOR UPDATE SKIP LOCKED LIMIT 1
    )
    RETURNING id
), candidate AS (
    SELECT id
    FROM derived_verification_scans
    WHERE (status = 'queued' OR
           (status = 'running' AND lease_expires_at <= clock_timestamp()))
      AND attempt_count < max_attempts
      AND NOT EXISTS (
          SELECT 1 FROM exhausted
          WHERE exhausted.id = derived_verification_scans.id
      )
    ORDER BY updated_at, id
    FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE derived_verification_scans AS scan
SET status = 'running', leased_by = $1, lease_token = $2,
    lease_expires_at = clock_timestamp() + ($3 * INTERVAL '1 microsecond'),
    attempt_count = scan.attempt_count + 1, last_error = NULL,
    updated_at = clock_timestamp()
FROM candidate
WHERE scan.id = candidate.id
RETURNING scan.id::text, scan.compilation_id::text, scan.chain_id::text,
          scan.creator_address, scan.creator_code_hash,
          scan.valid_from_block::text, scan.valid_to_block::text,
          scan.cursor_block_number::text, scan.cursor_transaction_hash,
          scan.cursor_trace_path;

-- name: DerivedVerifyLoadCompilationCandidates :many
SELECT unit.language, unit.compiler_version, unit.standard_json_payload,
       candidate.file_name, candidate.contract_name, candidate.abi,
       candidate.creation_bytecode, candidate.runtime_bytecode,
       candidate.compilation_artifacts, candidate.creation_code_artifacts,
       candidate.runtime_code_artifacts
FROM verification_compilation_units AS unit
JOIN verification_compilation_contracts AS candidate
  ON candidate.compilation_id = unit.id
WHERE unit.id = $1::uuid
ORDER BY candidate.file_name, candidate.contract_name;

-- name: DerivedVerifyListHistoricalTraces :many
SELECT trace.block_number::text, trace.block_hash, trace.transaction_hash,
       trace.trace_path, trace.call_type, trace.from_address,
       trace.created_address, trace.input, runtime.code
FROM normalized_traces AS trace
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = trace.chain_id
 AND canonical.number = trace.block_number
 AND canonical.block_hash = trace.block_hash
LEFT JOIN contract_code_observations AS runtime
  ON runtime.chain_id = trace.chain_id
 AND runtime.address = trace.created_address
 AND runtime.block_number = trace.block_number
 AND runtime.block_hash = trace.block_hash
 AND runtime.canonical
LEFT JOIN derived_verification_attempts AS attempt
  ON attempt.chain_id = trace.chain_id
 AND attempt.block_hash = trace.block_hash
 AND attempt.transaction_hash = trace.transaction_hash
 AND attempt.trace_path = trace.trace_path
 AND attempt.compilation_id = $1::uuid
WHERE trace.chain_id = $2::numeric
  AND trace.from_address = $3
  AND $4 = (
      SELECT observation.code_hash
      FROM contract_code_observations AS observation
      JOIN canonical_blocks AS observation_canonical
        ON observation_canonical.chain_id = observation.chain_id
       AND observation_canonical.number = observation.block_number
       AND observation_canonical.block_hash = observation.block_hash
      WHERE observation.chain_id = trace.chain_id
        AND observation.address = trace.from_address
        AND observation.canonical
        AND observation.block_number <= trace.block_number
      ORDER BY observation.block_number DESC, observation.observed_at DESC,
               observation.code_hash DESC
      LIMIT 1
  )
  AND trace.canonical AND NOT trace.reverted
  AND trace.call_type IN ('CREATE', 'CREATE2')
  AND trace.created_address IS NOT NULL
  AND octet_length(trace.input) > 0
  AND trace.block_number >= $5::numeric
  AND ($6::numeric IS NULL OR trace.block_number <= $6::numeric)
  AND (trace.block_number, trace.transaction_hash, trace.trace_path) >
      ($7::numeric, $8::bytea, $9::text)
  AND (attempt.id IS NULL OR attempt.status = 'pending_runtime')
ORDER BY trace.block_number, trace.transaction_hash, trace.trace_path
LIMIT $10;

-- name: DerivedVerifyRecordAttempt :many
WITH evidence AS (
    SELECT trace.chain_id, trace.block_number, trace.block_hash,
           trace.transaction_hash, trace.trace_path,
           (trace.canonical AND NOT trace.reverted AND canonical.block_hash IS NOT NULL) AS live
    FROM normalized_traces AS trace
    LEFT JOIN canonical_blocks AS canonical
      ON canonical.chain_id = trace.chain_id
     AND canonical.number = trace.block_number
     AND canonical.block_hash = trace.block_hash
    WHERE trace.chain_id = $2::numeric
      AND trace.block_number = $3::numeric
      AND trace.block_hash = $4
      AND trace.transaction_hash = $5
      AND trace.trace_path = $6
      AND trace.from_address = $7
      AND trace.created_address = $8
      AND trace.call_type = $9
)
INSERT INTO derived_verification_attempts (
    id, chain_id, block_number, block_hash, transaction_hash, trace_path,
    creator_address, created_address, call_type, compilation_id, status,
    stale_from_status
) VALUES (
    $1::uuid, $2::numeric, $3::numeric, $4, $5, $6, $7, $8, $9,
    $10::uuid,
    CASE WHEN (SELECT live FROM evidence) THEN $11 ELSE 'stale' END,
    CASE WHEN (SELECT live FROM evidence) THEN NULL ELSE $11 END
)
ON CONFLICT (chain_id, block_hash, transaction_hash, trace_path, compilation_id)
DO UPDATE SET status = EXCLUDED.status,
              stale_from_status = EXCLUDED.stale_from_status,
              updated_at = clock_timestamp()
WHERE derived_verification_attempts.status = 'pending_runtime'
RETURNING status;

-- name: DerivedVerifyAdvanceScan :exec
UPDATE derived_verification_scans
SET status = CASE
        WHEN rescan_from_block IS NOT NULL THEN 'queued'
        WHEN $4::boolean THEN 'succeeded'
        ELSE 'queued'
    END,
    cursor_block_number = CASE
        WHEN rescan_from_block IS NOT NULL AND rescan_from_block <= $5::numeric
        THEN rescan_from_block ELSE $5::numeric END,
    cursor_transaction_hash = CASE
        WHEN rescan_from_block IS NOT NULL AND rescan_from_block <= $5::numeric
        THEN decode(repeat('00', 32), 'hex') ELSE $6 END,
    cursor_trace_path = CASE
        WHEN rescan_from_block IS NOT NULL AND rescan_from_block <= $5::numeric
        THEN '' ELSE $7 END,
    rescan_from_block = NULL,
    attempt_count = 0,
    leased_by = NULL, lease_token = NULL, lease_expires_at = NULL,
    last_error = NULL, updated_at = clock_timestamp()
WHERE id = $1::bigint AND status = 'running'
  AND lease_token = $2 AND lease_expires_at > clock_timestamp()
  AND leased_by = $3;

-- name: DerivedVerifyRetryScan :exec
UPDATE derived_verification_scans
SET status = 'queued', leased_by = NULL, lease_token = NULL,
    lease_expires_at = NULL,
    cursor_block_number = CASE
        WHEN rescan_from_block IS NOT NULL AND rescan_from_block <= cursor_block_number
        THEN rescan_from_block ELSE cursor_block_number END,
    cursor_transaction_hash = CASE
        WHEN rescan_from_block IS NOT NULL AND rescan_from_block <= cursor_block_number
        THEN decode(repeat('00', 32), 'hex') ELSE cursor_transaction_hash END,
    cursor_trace_path = CASE
        WHEN rescan_from_block IS NOT NULL AND rescan_from_block <= cursor_block_number
        THEN '' ELSE cursor_trace_path END,
    rescan_from_block = NULL,
    last_error = $4, updated_at = clock_timestamp()
WHERE id = $1::bigint AND status = 'running'
  AND lease_token = $2 AND leased_by = $3;

-- name: DerivedVerifyPublicationEvidence :many
SELECT trace.chain_id::text, trace.block_number::text, trace.block_hash,
       trace.transaction_hash, trace.trace_path, trace.call_type,
       trace.from_address, trace.created_address, trace.input, runtime.code,
       runtime.code_hash, unit.request_digest, unit.language,
       unit.compiler_version, unit.compiler_platform,
       unit.catalog_generation_id, unit.compiler_sha256,
       unit.executor_kind, unit.execution_policy, unit.executor_sha256,
       unit.standard_json_payload, parent.verification_job_id::text
FROM verification_compilation_units AS unit
JOIN derived_verification_scans AS scan ON scan.compilation_id = unit.id
JOIN verified_contracts AS parent
  ON parent.chain_id = scan.chain_id
 AND parent.address = scan.creator_address
 AND parent.code_hash = scan.creator_code_hash
 AND (
      parent.verification_job_id = unit.source_job_id OR
      EXISTS (
          SELECT 1
          FROM derived_verification_attempts AS parent_attempt
          WHERE parent_attempt.compilation_id = unit.id
            AND parent_attempt.verification_job_id = parent.verification_job_id
            AND parent_attempt.status = 'matched'
      )
 )
JOIN normalized_traces AS trace
  ON trace.chain_id = scan.chain_id
 AND trace.block_number = $2::numeric
 AND trace.block_hash = $3
 AND trace.transaction_hash = $4
 AND trace.trace_path = $5
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = trace.chain_id
 AND canonical.number = trace.block_number
 AND canonical.block_hash = trace.block_hash
JOIN contract_code_observations AS runtime
  ON runtime.chain_id = trace.chain_id
 AND runtime.address = trace.created_address
 AND runtime.block_number = trace.block_number
 AND runtime.block_hash = trace.block_hash
 AND runtime.canonical
WHERE unit.id = $1::uuid
  AND trace.from_address = scan.creator_address
  AND scan.creator_code_hash = (
      SELECT observation.code_hash
      FROM contract_code_observations AS observation
      JOIN canonical_blocks AS observation_canonical
        ON observation_canonical.chain_id = observation.chain_id
       AND observation_canonical.number = observation.block_number
       AND observation_canonical.block_hash = observation.block_hash
      WHERE observation.chain_id = trace.chain_id
        AND observation.address = trace.from_address
        AND observation.canonical
        AND observation.block_number <= trace.block_number
      ORDER BY observation.block_number DESC, observation.observed_at DESC,
               observation.code_hash DESC
      LIMIT 1
  )
  AND trace.canonical AND NOT trace.reverted
  AND trace.call_type IN ('CREATE', 'CREATE2')
  AND trace.created_address IS NOT NULL
  AND octet_length(trace.input) > 0
  AND trace.block_number >= scan.valid_from_block
  AND (scan.valid_to_block IS NULL OR trace.block_number <= scan.valid_to_block)
FOR SHARE OF unit, scan, parent, trace, canonical, runtime;

-- name: DerivedVerifyLockTarget :many
SELECT pg_advisory_xact_lock(hashtextextended(
    'etherview:derived-verification:' || $1::numeric::text || ':' || encode($2::bytea, 'hex'),
    0
));

-- name: DerivedVerifyExistingPublication :many
SELECT verification_job_id::text
FROM verified_contracts
WHERE chain_id = $1::numeric AND address = $2 AND code_hash = $3
  AND valid_from_block = $4::numeric
LIMIT 1;

-- name: DerivedVerifyInsertJob :exec
INSERT INTO verification_jobs (
    id, kind, language, catalog_language, compiler_version,
    compiler_platform, catalog_generation_id, compiler_digest,
    executor_kind, execution_policy, executor_digest,
    chain_id, address, code_hash, block_hash,
    request, request_payload, request_digest, status, attempt_count,
    max_attempts, outcome_kind, outcome
) VALUES (
    $1::uuid, 'derived', 'solidity', 'solidity', $2,
    $3, $4::bigint, $5, $6, $7, $8,
    $9::numeric, $10, $11, $12,
    $13::jsonb, $14, $15, 'succeeded', 1, 1,
    'verification_success', $16::jsonb
);

-- name: DerivedVerifyMatchAttempt :exec
INSERT INTO derived_verification_attempts (
    id, chain_id, block_number, block_hash, transaction_hash, trace_path,
    creator_address, created_address, call_type, compilation_id,
    file_name, contract_name, status, creation_match, runtime_match,
    verification_job_id
) VALUES (
    $1::uuid, $2::numeric, $3::numeric, $4, $5, $6, $7, $8, $9,
    $10::uuid, $11, $12, 'matched', $13::jsonb, $14::jsonb, $15::uuid
)
ON CONFLICT (chain_id, block_hash, transaction_hash, trace_path, compilation_id)
DO UPDATE SET file_name = EXCLUDED.file_name,
              contract_name = EXCLUDED.contract_name,
              status = 'matched',
              creation_match = EXCLUDED.creation_match,
              runtime_match = EXCLUDED.runtime_match,
              verification_job_id = EXCLUDED.verification_job_id,
              updated_at = clock_timestamp()
WHERE derived_verification_attempts.status = 'pending_runtime';

-- name: DerivedVerifyClaimForwardBlock :many
WITH exhausted AS (
    UPDATE derived_verification_forward_blocks
    SET status = 'failed', last_error = 'attempts_exhausted',
        leased_by = NULL, lease_token = NULL, lease_expires_at = NULL,
        updated_at = clock_timestamp()
    WHERE id = (
        SELECT id
        FROM derived_verification_forward_blocks
        WHERE (status = 'queued' OR
               (status = 'running' AND lease_expires_at <= clock_timestamp()))
          AND attempt_count >= max_attempts
        ORDER BY updated_at, id
        FOR UPDATE SKIP LOCKED LIMIT 1
    )
    RETURNING id
), candidate AS (
    SELECT event.id
    FROM derived_verification_forward_blocks AS event
    JOIN durable_stage_publications AS publication
      ON publication.job_id = event.source_job_id
     AND publication.job_generation = event.source_generation
     AND publication.chain_id = event.chain_id
     AND publication.block_number = event.block_number
     AND publication.block_hash = event.block_hash
     AND publication.stage = event.source_stage
     AND publication.state = 'complete'
     AND ((publication.stage = 'trace' AND publication.stage_version = 3) OR
          (publication.stage = 'proxy' AND publication.stage_version = 2))
    WHERE (event.status = 'queued' OR
           (event.status = 'running' AND event.lease_expires_at <= clock_timestamp()))
      AND event.attempt_count < event.max_attempts
      AND NOT EXISTS (
          SELECT 1 FROM exhausted
          WHERE exhausted.id = event.id
      )
    ORDER BY event.updated_at, event.id
    FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE derived_verification_forward_blocks AS block
SET status = 'running', leased_by = $1, lease_token = $2,
    lease_expires_at = clock_timestamp() + ($3 * INTERVAL '1 microsecond'),
    attempt_count = block.attempt_count + 1, last_error = NULL,
    updated_at = clock_timestamp()
FROM candidate
WHERE block.id = candidate.id
RETURNING block.id::text, block.chain_id::text, block.block_number::text,
          block.block_hash, block.source_stage, block.source_job_id,
          block.source_generation;

-- name: DerivedVerifyDispatchTraceEvent :execrows
UPDATE derived_verification_scans AS scan
SET status = CASE WHEN scan.status = 'running' THEN 'running' ELSE 'queued' END,
    rescan_from_block = CASE
        WHEN scan.status = 'running' THEN LEAST(
            COALESCE(scan.rescan_from_block, $2::numeric), $2::numeric
        )
        ELSE NULL
    END,
    cursor_block_number = CASE
        WHEN scan.status <> 'running' AND scan.cursor_block_number >= $2::numeric
        THEN $2::numeric ELSE scan.cursor_block_number END,
    cursor_transaction_hash = CASE
        WHEN scan.status <> 'running' AND scan.cursor_block_number >= $2::numeric
        THEN decode(repeat('00', 32), 'hex') ELSE scan.cursor_transaction_hash END,
    cursor_trace_path = CASE
        WHEN scan.status <> 'running' AND scan.cursor_block_number >= $2::numeric
        THEN '' ELSE scan.cursor_trace_path END,
    attempt_count = CASE WHEN scan.status = 'running' THEN scan.attempt_count ELSE 0 END,
    last_error = CASE WHEN scan.status = 'running' THEN scan.last_error ELSE NULL END,
    updated_at = clock_timestamp()
WHERE scan.status IN ('queued', 'succeeded', 'running', 'failed')
  AND scan.last_error IS DISTINCT FROM 'superseded_epoch_start'
  AND scan.chain_id = $1::numeric
  AND EXISTS (
      SELECT 1
      FROM normalized_traces AS trace
      WHERE trace.chain_id = scan.chain_id
        AND trace.block_number = $2::numeric
        AND trace.block_hash = $3
        AND trace.from_address = scan.creator_address
        AND trace.canonical AND NOT trace.reverted
        AND trace.call_type IN ('CREATE', 'CREATE2')
        AND trace.created_address IS NOT NULL
        AND octet_length(trace.input) > 0
        AND trace.block_number >= scan.valid_from_block
        AND (scan.valid_to_block IS NULL OR trace.block_number <= scan.valid_to_block)
        AND scan.creator_code_hash = (
            SELECT observation.code_hash
            FROM contract_code_observations AS observation
            JOIN canonical_blocks AS observation_canonical
              ON observation_canonical.chain_id = observation.chain_id
             AND observation_canonical.number = observation.block_number
             AND observation_canonical.block_hash = observation.block_hash
            WHERE observation.chain_id = trace.chain_id
              AND observation.address = trace.from_address
              AND observation.canonical
              AND observation.block_number <= trace.block_number
            ORDER BY observation.block_number DESC, observation.observed_at DESC,
                     observation.code_hash DESC
            LIMIT 1
        )
  );

-- name: DerivedVerifyDispatchProxyEvent :execrows
WITH pending AS (
    SELECT scan.id, min(attempt.block_number) AS rewind_block
    FROM derived_verification_scans AS scan
    JOIN derived_verification_attempts AS attempt
      ON attempt.compilation_id = scan.compilation_id
     AND attempt.chain_id = scan.chain_id
     AND attempt.creator_address = scan.creator_address
     AND attempt.status = 'pending_runtime'
    JOIN normalized_traces AS trace
      ON trace.chain_id = attempt.chain_id
     AND trace.block_number = attempt.block_number
     AND trace.block_hash = attempt.block_hash
     AND trace.transaction_hash = attempt.transaction_hash
     AND trace.trace_path = attempt.trace_path
     AND trace.canonical AND NOT trace.reverted
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = trace.chain_id
     AND canonical.number = trace.block_number
     AND canonical.block_hash = trace.block_hash
    JOIN contract_code_observations AS runtime
      ON runtime.chain_id = attempt.chain_id
     AND runtime.address = attempt.created_address
     AND runtime.block_number = attempt.block_number
     AND runtime.block_hash = attempt.block_hash
     AND runtime.canonical
     AND octet_length(runtime.code) > 0
    WHERE scan.chain_id = $1::numeric
      AND attempt.block_number = $2::numeric
      AND attempt.block_hash = $3
      AND attempt.block_number >= scan.valid_from_block
      AND (scan.valid_to_block IS NULL OR attempt.block_number <= scan.valid_to_block)
      AND scan.status IN ('queued', 'succeeded', 'running', 'failed')
      AND scan.last_error IS DISTINCT FROM 'superseded_epoch_start'
    GROUP BY scan.id
)
UPDATE derived_verification_scans AS scan
SET status = CASE WHEN scan.status = 'running' THEN 'running' ELSE 'queued' END,
    rescan_from_block = CASE
        WHEN scan.status = 'running' THEN LEAST(
            COALESCE(scan.rescan_from_block, pending.rewind_block), pending.rewind_block
        )
        ELSE NULL
    END,
    cursor_block_number = CASE
        WHEN scan.status <> 'running' AND scan.cursor_block_number >= pending.rewind_block
        THEN pending.rewind_block ELSE scan.cursor_block_number END,
    cursor_transaction_hash = CASE
        WHEN scan.status <> 'running' AND scan.cursor_block_number >= pending.rewind_block
        THEN decode(repeat('00', 32), 'hex') ELSE scan.cursor_transaction_hash END,
    cursor_trace_path = CASE
        WHEN scan.status <> 'running' AND scan.cursor_block_number >= pending.rewind_block
        THEN '' ELSE scan.cursor_trace_path END,
    attempt_count = CASE WHEN scan.status = 'running' THEN scan.attempt_count ELSE 0 END,
    last_error = CASE WHEN scan.status = 'running' THEN scan.last_error ELSE NULL END,
    updated_at = clock_timestamp()
FROM pending
WHERE scan.id = pending.id;

-- name: DerivedVerifyFinishForwardBlock :exec
UPDATE derived_verification_forward_blocks
SET status = 'succeeded', attempt_count = 0,
    leased_by = NULL, lease_token = NULL, lease_expires_at = NULL,
    last_error = NULL, updated_at = clock_timestamp()
WHERE id = $1::bigint AND chain_id = $2::numeric AND block_hash = $3
  AND source_job_id = $4::bigint AND source_generation = $5::bigint
  AND status = 'running' AND leased_by = $6 AND lease_token = $7
  AND lease_expires_at > clock_timestamp();

-- name: DerivedVerifyRetryForwardBlock :exec
UPDATE derived_verification_forward_blocks
SET status = 'queued', leased_by = NULL, lease_token = NULL,
    lease_expires_at = NULL, last_error = $8, updated_at = clock_timestamp()
WHERE id = $1::bigint AND chain_id = $2::numeric AND block_number = $3::numeric
  AND block_hash = $4 AND source_job_id = $5::bigint
  AND source_generation = $6::bigint
  AND status = 'running' AND leased_by = $7;

-- name: DerivedVerifyArtifactProvenance :many
SELECT attempt.creator_address, attempt.created_address,
       attempt.transaction_hash, attempt.trace_path, attempt.call_type,
       attempt.block_number::text, attempt.block_hash,
       parent.file_name, parent.contract_name
FROM derived_verification_attempts AS attempt
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = attempt.chain_id
 AND canonical.number = attempt.block_number
 AND canonical.block_hash = attempt.block_hash
JOIN normalized_traces AS trace
  ON trace.chain_id = attempt.chain_id
 AND trace.block_number = attempt.block_number
 AND trace.block_hash = attempt.block_hash
 AND trace.transaction_hash = attempt.transaction_hash
 AND trace.trace_path = attempt.trace_path
 AND trace.canonical
JOIN derived_verification_scans AS scan
  ON scan.compilation_id = attempt.compilation_id
 AND scan.chain_id = attempt.chain_id
 AND scan.creator_address = attempt.creator_address
 AND scan.valid_from_block <= attempt.block_number
 AND (scan.valid_to_block IS NULL OR scan.valid_to_block >= attempt.block_number)
JOIN verified_contracts AS parent
  ON parent.chain_id = scan.chain_id
 AND parent.address = scan.creator_address
 AND parent.code_hash = scan.creator_code_hash
WHERE attempt.verification_job_id = $1::uuid
  AND attempt.status = 'matched'
  AND scan.creator_code_hash = (
      SELECT observation.code_hash
      FROM contract_code_observations AS observation
      JOIN canonical_blocks AS observation_canonical
        ON observation_canonical.chain_id = observation.chain_id
       AND observation_canonical.number = observation.block_number
       AND observation_canonical.block_hash = observation.block_hash
      WHERE observation.chain_id = attempt.chain_id
        AND observation.address = attempt.creator_address
        AND observation.canonical
        AND observation.block_number <= attempt.block_number
      ORDER BY observation.block_number DESC, observation.observed_at DESC,
               observation.code_hash DESC
      LIMIT 1
  )
ORDER BY parent.verification_job_id
LIMIT 1;

-- name: DerivedVerifyArtifactJobKind :many
SELECT kind
FROM verification_jobs
WHERE id = $1::uuid
  AND status = 'succeeded';

-- name: DerivedVerifyCreatedContracts :many
SELECT attempt.created_address, attempt.transaction_hash, attempt.trace_path,
       attempt.call_type, attempt.block_number::text, attempt.block_hash,
       attempt.status, attempt.file_name, attempt.contract_name,
       (attempt.verification_job_id IS NOT NULL) AS auto_verified
FROM derived_verification_attempts AS attempt
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = attempt.chain_id
 AND canonical.number = attempt.block_number
 AND canonical.block_hash = attempt.block_hash
JOIN normalized_traces AS trace
  ON trace.chain_id = attempt.chain_id
 AND trace.block_number = attempt.block_number
 AND trace.block_hash = attempt.block_hash
 AND trace.transaction_hash = attempt.transaction_hash
 AND trace.trace_path = attempt.trace_path
 AND trace.canonical
WHERE attempt.chain_id = $1::numeric
  AND attempt.creator_address = $2
  AND attempt.status <> 'stale'
ORDER BY attempt.block_number DESC, attempt.transaction_hash DESC,
         attempt.trace_path DESC, attempt.compilation_id
LIMIT 100;

-- name: DerivedVerifyRequestBackfill :many
WITH selected AS (
    SELECT scan.*,
           epoch.block_number AS epoch_start
    FROM derived_verification_scans AS scan
    JOIN LATERAL (
        SELECT observation.block_number
        FROM contract_code_observations AS observation
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = observation.chain_id
         AND canonical.number = observation.block_number
         AND canonical.block_hash = observation.block_hash
        WHERE observation.chain_id = scan.chain_id
          AND observation.address = scan.creator_address
          AND observation.code_hash = scan.creator_code_hash
          AND observation.block_number > COALESCE((
              SELECT max(different.block_number)
              FROM contract_code_observations AS different
              JOIN canonical_blocks AS different_canonical
                ON different_canonical.chain_id = different.chain_id
               AND different_canonical.number = different.block_number
               AND different_canonical.block_hash = different.block_hash
              WHERE different.chain_id = scan.chain_id
                AND different.address = scan.creator_address
                AND different.code_hash <> scan.creator_code_hash
                AND different.block_number <= scan.valid_from_block
                AND different.canonical
          ), -1::numeric)
          AND observation.block_number <= scan.valid_from_block
          AND observation.canonical
          AND EXISTS (
              SELECT 1
              FROM contract_code_observations AS context
              JOIN canonical_blocks AS context_canonical
                ON context_canonical.chain_id = context.chain_id
               AND context_canonical.number = context.block_number
               AND context_canonical.block_hash = context.block_hash
              WHERE context.chain_id = scan.chain_id
                AND context.address = scan.creator_address
                AND context.code_hash = scan.creator_code_hash
                AND context.block_number = scan.valid_from_block
                AND context.canonical
          )
        ORDER BY observation.block_number, observation.observed_at,
                 observation.block_hash
        LIMIT 1
    ) AS epoch ON TRUE
    WHERE scan.chain_id = $1::numeric
      AND ($2::bytea IS NULL OR scan.creator_address = $2)
      AND scan.status <> 'running'
      AND scan.last_error IS DISTINCT FROM 'superseded_epoch_start'
    FOR UPDATE OF scan
), targets AS (
    SELECT DISTINCT ON (
        compilation_id, creator_address, creator_code_hash, epoch_start
    ) compilation_id, chain_id, creator_address, creator_code_hash,
      epoch_start, valid_to_block, max_attempts
    FROM selected
    ORDER BY compilation_id, creator_address, creator_code_hash, epoch_start,
             (valid_from_block = epoch_start) DESC, id
), corrected AS (
    INSERT INTO derived_verification_scans (
        compilation_id, chain_id, creator_address, creator_code_hash,
        valid_from_block, valid_to_block, cursor_block_number,
        cursor_transaction_hash, cursor_trace_path, status,
        rescan_from_block, attempt_count, max_attempts, last_error
    )
    SELECT compilation_id, chain_id, creator_address, creator_code_hash,
           epoch_start, valid_to_block, epoch_start,
           decode(repeat('00', 32), 'hex'), '', 'queued', NULL, 0,
           max_attempts, NULL
    FROM targets
    ON CONFLICT (
        compilation_id, creator_address, creator_code_hash, valid_from_block
    ) DO UPDATE SET
        status = 'queued', rescan_from_block = NULL,
        cursor_block_number = EXCLUDED.valid_from_block,
        cursor_transaction_hash = decode(repeat('00', 32), 'hex'),
        cursor_trace_path = '', attempt_count = 0, last_error = NULL,
        leased_by = NULL, lease_token = NULL, lease_expires_at = NULL,
        updated_at = clock_timestamp()
    WHERE derived_verification_scans.status <> 'running'
    RETURNING id
), superseded AS (
    UPDATE derived_verification_scans AS scan
    SET status = 'failed', rescan_from_block = NULL,
        leased_by = NULL, lease_token = NULL, lease_expires_at = NULL,
        last_error = 'superseded_epoch_start', updated_at = clock_timestamp()
    FROM selected
    WHERE scan.id = selected.id
      AND scan.valid_from_block <> selected.epoch_start
    RETURNING scan.id
)
INSERT INTO derived_verification_backfill_requests (
    chain_id, creator_address, reason, scan_count
)
SELECT $1::numeric, $2, $3, count(*)::integer
FROM corrected
RETURNING id, scan_count, requested_at;
