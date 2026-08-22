-- name: ContractArtifactArtifactSource :many
SELECT
       (verified.address = $2
        AND verified.valid_from_block <= $4::numeric
        AND (verified.valid_to_block IS NULL OR verified.valid_to_block >= $4::numeric)) AS exact,
       verified.address, verified.code_hash, verified.valid_from_block::text,
       verified.valid_to_block::text, verified.verification_job_id::text,
       verified.request_digest, verified.file_name, verified.contract_name,
       verified.language, verified.compiler_version, verified.match_type,
       verified.abi, verified.sources, verified.settings,
       verified.compilation_artifacts, verified.creation_code_artifacts,
       verified.runtime_code_artifacts, result.outcome->'creation_match',
       result.outcome->'runtime_match', verified.constructor_arguments,
       verified.libraries, verified.is_blueprint, verified.created_at
FROM verified_contracts AS verified
JOIN verification_results AS result
  ON result.job_id = verified.verification_job_id
 AND result.request_digest = verified.request_digest
 AND result.outcome_kind = 'verification_success'
WHERE verified.chain_id = $1::numeric
  AND verified.code_hash = $3
ORDER BY
    (verified.address = $2
     AND verified.valid_from_block <= $4::numeric
     AND (verified.valid_to_block IS NULL OR verified.valid_to_block >= $4::numeric)) DESC,
    (verified.abi IS NOT NULL) DESC,
    (verified.match_type = 'full') DESC,
    verified.request_digest ASC,
    verified.verification_job_id ASC,
    verified.address ASC,
    verified.valid_from_block DESC
LIMIT 1;

-- name: ContractArtifactCurrentTarget :many
WITH canonical_tip AS (
    SELECT number
    FROM canonical_blocks
    WHERE chain_id = $1::numeric
    ORDER BY number DESC
    LIMIT 1
)
SELECT observation.code_hash, observation.block_number::text,
       observation.block_hash, tip.number::text
FROM canonical_tip AS tip
JOIN LATERAL (
    SELECT candidate.code_hash, candidate.block_number, candidate.block_hash
    FROM contract_code_observations AS candidate
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = candidate.chain_id
     AND canonical.number = candidate.block_number
     AND canonical.block_hash = candidate.block_hash
    WHERE candidate.chain_id = $1::numeric
      AND candidate.address = $2
      AND candidate.canonical
      AND candidate.block_number <= tip.number
    ORDER BY candidate.block_number DESC,
             candidate.observed_at DESC,
             candidate.code_hash DESC
    LIMIT 1
) AS observation ON TRUE;

-- name: ContractArtifactTargetAtBlock :many
WITH context_block AS (
    SELECT canonical.number, canonical.block_hash
    FROM canonical_blocks AS canonical
    WHERE canonical.chain_id = $1::numeric
      AND canonical.number = $3::numeric
      AND canonical.block_hash = $4
)
SELECT observation.code_hash, context.number::text, context.block_hash,
       context.number::text
FROM context_block AS context
JOIN LATERAL (
    SELECT candidate.code_hash
    FROM contract_code_observations AS candidate
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = candidate.chain_id
     AND canonical.number = candidate.block_number
     AND canonical.block_hash = candidate.block_hash
    WHERE candidate.chain_id = $1::numeric
      AND candidate.address = $2
      AND candidate.canonical
      AND candidate.block_number <= context.number
    ORDER BY candidate.block_number DESC,
             candidate.observed_at DESC,
             candidate.code_hash DESC
    LIMIT 1
) AS observation ON TRUE;
