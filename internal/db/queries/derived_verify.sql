-- name: DerivedVerifyEnqueueHistoricalScan :exec
INSERT INTO derived_verification_scans (
    compilation_id, chain_id, creator_address, creator_code_hash,
    valid_from_block, valid_to_block, cursor_block_number
) VALUES ($1::uuid, $2::numeric, $3, $4, $5::numeric, $6::numeric, $5::numeric)
ON CONFLICT (compilation_id) DO NOTHING;

-- name: DerivedVerifyClaimScan :many
WITH exhausted AS (
    UPDATE derived_verification_scans
    SET status = 'failed', last_error = 'attempts_exhausted',
        leased_by = NULL, lease_token = NULL, lease_expires_at = NULL,
        updated_at = clock_timestamp()
    WHERE compilation_id = (
        SELECT compilation_id
        FROM derived_verification_scans
        WHERE (status = 'queued' OR
               (status = 'running' AND lease_expires_at <= clock_timestamp()))
          AND attempt_count >= max_attempts
        ORDER BY updated_at, compilation_id
        FOR UPDATE SKIP LOCKED LIMIT 1
    )
    RETURNING compilation_id
), candidate AS (
    SELECT compilation_id
    FROM derived_verification_scans
    WHERE (status = 'queued' OR
           (status = 'running' AND lease_expires_at <= clock_timestamp()))
      AND attempt_count < max_attempts
      AND NOT EXISTS (
          SELECT 1 FROM exhausted
          WHERE exhausted.compilation_id = derived_verification_scans.compilation_id
      )
    ORDER BY updated_at, compilation_id
    FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE derived_verification_scans AS scan
SET status = 'running', leased_by = $1, lease_token = $2,
    lease_expires_at = clock_timestamp() + ($3 * INTERVAL '1 microsecond'),
    attempt_count = scan.attempt_count + 1, last_error = NULL,
    updated_at = clock_timestamp()
FROM candidate
WHERE scan.compilation_id = candidate.compilation_id
RETURNING scan.compilation_id::text, scan.chain_id::text,
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
  AND trace.canonical AND NOT trace.reverted
  AND trace.call_type IN ('CREATE', 'CREATE2')
  AND trace.created_address IS NOT NULL
  AND octet_length(trace.input) > 0
  AND trace.block_number >= $4::numeric
  AND ($5::numeric IS NULL OR trace.block_number <= $5::numeric)
  AND (trace.block_number, trace.transaction_hash, trace.trace_path) >
      ($6::numeric, $7::bytea, $8::text)
  AND (attempt.id IS NULL OR attempt.status = 'pending_runtime')
ORDER BY trace.block_number, trace.transaction_hash, trace.trace_path
LIMIT $9;

-- name: DerivedVerifyRecordAttempt :exec
INSERT INTO derived_verification_attempts (
    id, chain_id, block_number, block_hash, transaction_hash, trace_path,
    creator_address, created_address, call_type, compilation_id, status
) VALUES (
    $1::uuid, $2::numeric, $3::numeric, $4, $5, $6, $7, $8, $9,
    $10::uuid, $11
)
ON CONFLICT (chain_id, block_hash, transaction_hash, trace_path, compilation_id)
DO UPDATE SET status = EXCLUDED.status, updated_at = clock_timestamp()
WHERE derived_verification_attempts.status = 'pending_runtime';

-- name: DerivedVerifyAdvanceScan :exec
UPDATE derived_verification_scans
SET status = CASE WHEN $4::boolean THEN 'succeeded' ELSE 'queued' END,
    cursor_block_number = $5::numeric,
    cursor_transaction_hash = $6,
    cursor_trace_path = $7,
    leased_by = NULL, lease_token = NULL, lease_expires_at = NULL,
    last_error = NULL, updated_at = clock_timestamp()
WHERE compilation_id = $1::uuid AND status = 'running'
  AND lease_token = $2 AND lease_expires_at > clock_timestamp()
  AND leased_by = $3;

-- name: DerivedVerifyRetryScan :exec
UPDATE derived_verification_scans
SET status = 'queued', leased_by = NULL, lease_token = NULL,
    lease_expires_at = NULL, last_error = $4, updated_at = clock_timestamp()
WHERE compilation_id = $1::uuid AND status = 'running'
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
  ON parent.verification_job_id = unit.source_job_id
 AND parent.chain_id = scan.chain_id
 AND parent.address = scan.creator_address
 AND parent.code_hash = scan.creator_code_hash
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
  AND trace.canonical AND NOT trace.reverted
  AND trace.call_type IN ('CREATE', 'CREATE2')
  AND trace.created_address IS NOT NULL
  AND octet_length(trace.input) > 0
  AND trace.block_number >= parent.valid_from_block
  AND (parent.valid_to_block IS NULL OR trace.block_number <= parent.valid_to_block)
FOR SHARE OF unit, scan, parent, trace, canonical, runtime;

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
