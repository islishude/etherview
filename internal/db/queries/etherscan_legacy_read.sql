-- name: EtherscanBlockCountdown :many
WITH tip AS (
    SELECT number
    FROM canonical_blocks
    WHERE chain_id = $1::numeric
    ORDER BY number DESC
    LIMIT 1
), tip_coverage AS (
    SELECT configuration.configured_start,
           coverage.range_start, coverage.range_end
    FROM tip
    JOIN core_index_configuration AS configuration
      ON configuration.chain_id = $1::numeric
    JOIN core_coverage_ranges AS coverage
      ON coverage.chain_id = configuration.chain_id
     AND coverage.range_start <= tip.number
     AND coverage.range_end >= tip.number
    ORDER BY coverage.range_start DESC
    LIMIT 1
), recent AS (
    SELECT block.number, block.timestamp
    FROM blocks AS block
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = block.chain_id
     AND canonical.number = block.number
     AND canonical.block_hash = block.hash
    CROSS JOIN tip
    CROSS JOIN tip_coverage AS coverage
    WHERE block.chain_id = $1::numeric
      AND block.number >= coverage.range_start
      AND block.number <= tip.number
    ORDER BY block.number DESC
    LIMIT 128
), current_sample AS (
    SELECT number, timestamp FROM recent ORDER BY number DESC LIMIT 1
), anchor AS (
    SELECT number, timestamp FROM recent ORDER BY number ASC LIMIT 1
), sample_count AS (
    SELECT count(*) AS value FROM recent
)
SELECT current_sample.number::text, current_sample.timestamp::text,
       anchor.number::text, anchor.timestamp::text,
       sample_count.value::text, coverage.configured_start::text,
       coverage.range_start::text, coverage.range_end::text
FROM current_sample
CROSS JOIN anchor
CROSS JOIN sample_count
CROSS JOIN tip_coverage AS coverage;

-- name: EtherscanCanonicalCoreRange :many
WITH tip AS (
    SELECT number
    FROM canonical_blocks
    WHERE chain_id = $1::numeric
    ORDER BY number DESC
    LIMIT 1
), requested AS (
    SELECT tip.number,
           $2::numeric AS range_start,
           LEAST(COALESCE($3::numeric, tip.number), tip.number) AS range_end
    FROM tip
)
SELECT requested.number::text, configuration.configured_start::text,
       coverage.range_start::text, coverage.range_end::text
FROM requested
LEFT JOIN core_index_configuration AS configuration
  ON configuration.chain_id = $1::numeric
LEFT JOIN LATERAL (
    SELECT candidate.range_start, candidate.range_end
    FROM core_coverage_ranges AS candidate
    WHERE candidate.chain_id = configuration.chain_id
      AND candidate.range_start <= requested.range_start
      AND candidate.range_end >= requested.range_end
    ORDER BY candidate.range_start DESC
    LIMIT 1
) AS coverage ON true;

-- name: EtherscanCanonicalSnapshot :many
SELECT number::text, block_hash
FROM canonical_blocks
WHERE chain_id = $1::numeric
ORDER BY number DESC
LIMIT 1;

-- name: EtherscanCanonicalReference :many
SELECT EXISTS (
    SELECT 1
    FROM canonical_blocks
    WHERE chain_id = $1::numeric
      AND number = $2::numeric
      AND block_hash = $3
);

-- name: EtherscanCanonicalStageRange :many
WITH tip AS (
    SELECT number
    FROM canonical_blocks
    WHERE chain_id = $1::numeric
    ORDER BY number DESC
    LIMIT 1
), incomplete AS (
    SELECT canonical.number, canonical.block_hash, latest.state
    FROM canonical_blocks AS canonical
    CROSS JOIN tip
    LEFT JOIN LATERAL (
        SELECT result.state
        FROM published_block_stage_results AS result
        WHERE result.chain_id = canonical.chain_id
          AND result.block_number = canonical.number
          AND result.block_hash = canonical.block_hash
          AND result.stage = $4
        ORDER BY result.stage_version DESC
        LIMIT 1
    ) AS latest ON true
    WHERE canonical.chain_id = $1::numeric
      AND canonical.number >= $2::numeric
      AND canonical.number <= LEAST(COALESCE($3::numeric, tip.number), tip.number)
      AND latest.state IS DISTINCT FROM 'complete'
    ORDER BY canonical.number
    LIMIT 1
)
SELECT tip.number::text, incomplete.number::text,
       incomplete.block_hash, incomplete.state
FROM tip
LEFT JOIN incomplete ON true;

-- name: EtherscanCanonicalTokenContract :many
SELECT token.address, token.code_hash, token.standard, token.confidence,
       token.name, token.symbol, token.decimals, token.total_supply::text,
       token.metadata_state, token.observed_block_number::text, token.observed_block_hash
FROM token_contracts AS token
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = token.chain_id
 AND canonical.number = token.observed_block_number
 AND canonical.block_hash = token.observed_block_hash
WHERE token.chain_id = $1::numeric AND token.address = $2
ORDER BY token.observed_block_number DESC, token.updated_at DESC, token.code_hash DESC
LIMIT 1;

-- name: EtherscanCanonicalTransactionBlock :many
SELECT inclusion.block_number::text
FROM transaction_inclusions AS inclusion
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = inclusion.chain_id
 AND canonical.number = inclusion.block_number
 AND canonical.block_hash = inclusion.block_hash
WHERE inclusion.chain_id = $1::numeric
  AND inclusion.tx_hash = $2
LIMIT 1;

-- name: EtherscanContractCreation :many
WITH candidates AS (
    SELECT 'top_level'::text AS source_kind,
           receipt.raw AS receipt_raw, inclusion.raw AS transaction_raw,
           receipt.tx_hash AS transaction_hash, receipt.block_hash,
           receipt.block_number, block.timestamp, receipt.tx_index,
           NULL::text AS trace_path, NULL::integer AS trace_depth,
           NULL::text AS call_type, NULL::bytea AS factory_address,
           NULL::bytea AS trace_input, 0 AS source_rank
    FROM receipts AS receipt
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = receipt.chain_id
     AND canonical.number = receipt.block_number
     AND canonical.block_hash = receipt.block_hash
    JOIN transaction_inclusions AS inclusion
      ON inclusion.chain_id = receipt.chain_id
     AND inclusion.block_number = receipt.block_number
     AND inclusion.block_hash = receipt.block_hash
     AND inclusion.tx_index = receipt.tx_index
    JOIN blocks AS block
      ON block.chain_id = receipt.chain_id
     AND block.number = receipt.block_number
     AND block.hash = receipt.block_hash
    WHERE receipt.chain_id = $1::numeric
      AND lower(receipt.raw->>'contractAddress') = lower('0x' || encode($2, 'hex'))

    UNION ALL

    SELECT 'trace'::text AS source_kind,
           NULL::jsonb AS receipt_raw, inclusion.raw AS transaction_raw,
           trace.transaction_hash, trace.block_hash,
           trace.block_number, block.timestamp, trace.transaction_index,
           trace.trace_path, trace.depth, trace.call_type,
           trace.from_address AS factory_address, trace.input AS trace_input,
           1 AS source_rank
    FROM normalized_traces AS trace
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = trace.chain_id
     AND canonical.number = trace.block_number
     AND canonical.block_hash = trace.block_hash
    JOIN transaction_inclusions AS inclusion
      ON inclusion.chain_id = trace.chain_id
     AND inclusion.block_number = trace.block_number
     AND inclusion.block_hash = trace.block_hash
     AND inclusion.tx_index = trace.transaction_index
     AND inclusion.tx_hash = trace.transaction_hash
    JOIN blocks AS block
      ON block.chain_id = trace.chain_id
     AND block.number = trace.block_number
     AND block.hash = trace.block_hash
    WHERE trace.chain_id = $1::numeric
      AND trace.created_address = $2
      AND trace.canonical = TRUE
      AND trace.reverted = FALSE
      AND trace.depth > 0
      AND trace.call_type IN ('CREATE', 'CREATE2')
      AND trace.from_address IS NOT NULL
      AND trace.input IS NOT NULL
)
SELECT source_kind, receipt_raw, transaction_raw, transaction_hash,
       block_hash, block_number::text, timestamp::text, tx_index,
       trace_path, trace_depth, call_type, factory_address, trace_input
FROM candidates
ORDER BY block_number ASC, tx_index ASC, source_rank ASC, trace_path ASC
LIMIT 1;

-- name: EtherscanProxyVerificationTarget :many
WITH canonical_tip AS (
    SELECT number, block_hash
    FROM canonical_blocks
    WHERE chain_id = $1::numeric
    ORDER BY number DESC
    LIMIT 1
), latest_raw AS (
    SELECT observation.*, tip.number AS context_number,
           tip.block_hash AS context_hash
    FROM canonical_tip AS tip
    JOIN LATERAL (
        SELECT observation.*
        FROM proxy_observations AS observation
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = observation.chain_id
         AND canonical.number = observation.block_number
         AND canonical.block_hash = observation.block_hash
        WHERE observation.chain_id = $1::numeric
          AND observation.proxy_address = $2::bytea
          AND observation.canonical = TRUE
          AND observation.stage_version = 2
          AND observation.confidence IN ('verified', 'high')
          AND observation.block_number <= tip.number
        ORDER BY observation.block_number DESC, observation.block_hash DESC
        LIMIT 1
    ) AS observation ON TRUE
), published_raw AS (
    SELECT raw.*, generation.id AS observation_generation_id,
           generation.durable_job_id AS observation_durable_job_id,
           generation.job_generation AS observation_job_generation
    FROM latest_raw AS raw
    JOIN LATERAL (
        SELECT witness.id, witness.durable_job_id, witness.job_generation
        FROM proxy_observation_generations AS witness
        JOIN published_block_stage_results AS published
          ON published.chain_id = witness.chain_id
         AND published.block_hash = witness.observation_block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = witness.observation_stage_version
         AND published.durable_job_id = witness.durable_job_id
         AND published.job_generation = witness.job_generation
         AND published.state = 'complete'
        WHERE witness.chain_id = raw.chain_id
          AND witness.proxy_address = raw.proxy_address
          AND witness.observation_block_hash = raw.block_hash
          AND witness.observation_stage_version = raw.stage_version
        ORDER BY witness.id DESC
        LIMIT 1
    ) AS generation ON TRUE
), unshadowed_raw AS (
    SELECT raw.*
    FROM published_raw AS raw
    WHERE NOT EXISTS (
        SELECT 1
        FROM proxy_detection_evidence AS evidence
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = evidence.chain_id
         AND canonical.number = evidence.block_number
         AND canonical.block_hash = evidence.block_hash
        JOIN published_block_stage_results AS published
          ON published.chain_id = evidence.chain_id
         AND published.block_hash = evidence.block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = evidence.stage_version
         AND published.durable_job_id = evidence.durable_job_id
         AND published.job_generation = evidence.job_generation
         AND published.state = 'complete'
        WHERE evidence.chain_id = raw.chain_id
          AND evidence.address = raw.proxy_address
          AND evidence.code_hash = raw.proxy_code_hash
          AND evidence.candidate_kind = 'proxy'
          AND evidence.stage_version = raw.stage_version
          AND evidence.canonical = TRUE
          AND evidence.block_number <= raw.context_number
          AND NOT (
              evidence.reason = 'immutable_args_creation_unverified'
              AND raw.proxy_pattern = 'clone'
              AND raw.evidence_state = 'exact'
              AND octet_length(raw.immutable_args) > 0
              AND raw.details->>'immutable_args_creation_authenticated' = 'true'
          )
          AND (
              evidence.block_number > raw.block_number OR (
                  evidence.block_number = raw.block_number
                  AND evidence.block_hash = raw.block_hash
                  AND evidence.durable_job_id = raw.observation_durable_job_id
                  AND evidence.job_generation >= raw.observation_job_generation
              )
          )
    )
), resolved_proxy AS (
    SELECT raw.*, resolution.id AS artifact_resolution_id,
           resolution.proxy_artifact_job_id,
           resolution.implementation_artifact_job_id,
           CASE WHEN raw.proxy_pattern = 'clone' THEN raw.proxy_kind
                ELSE resolution.proxy_kind END AS effective_kind,
           CASE WHEN raw.proxy_pattern = 'clone' THEN raw.proxy_pattern
                WHEN resolution.proxy_pattern = 'uups' THEN 'erc1967'
                ELSE resolution.proxy_pattern END AS effective_pattern,
           CASE WHEN raw.proxy_pattern = 'clone' THEN NULL::text
                ELSE resolution.standard_version END AS effective_standard,
           CASE WHEN raw.proxy_pattern = 'clone' THEN raw.implementation_address
                ELSE resolution.implementation_address END AS effective_implementation,
           CASE WHEN raw.proxy_pattern = 'clone' THEN raw.implementation_code_hash
                ELSE resolution.implementation_code_hash END AS effective_implementation_hash,
           resolution.admin_address AS effective_admin,
           resolution.admin_code_hash AS effective_admin_hash,
           resolution.beacon_address AS effective_beacon,
           resolution.beacon_code_hash AS effective_beacon_hash
    FROM unshadowed_raw AS raw
    LEFT JOIN LATERAL (
        SELECT candidate.*
        FROM proxy_artifact_resolutions AS candidate
        JOIN published_block_stage_results AS published
          ON published.chain_id = candidate.chain_id
         AND published.block_hash = candidate.observation_block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = candidate.observation_stage_version
         AND published.durable_job_id = candidate.durable_job_id
         AND published.job_generation = candidate.job_generation
         AND published.state = 'complete'
        WHERE candidate.chain_id = raw.chain_id
          AND candidate.proxy_address = raw.proxy_address
          AND candidate.observation_block_hash = raw.block_hash
          AND candidate.observation_stage_version = raw.stage_version
          AND candidate.proxy_code_hash = raw.proxy_code_hash
          AND candidate.proxy_pattern <> 'uups'
        ORDER BY candidate.id DESC
        LIMIT 1
    ) AS resolution ON raw.proxy_pattern <> 'clone'
    WHERE (raw.proxy_pattern = 'clone' AND raw.evidence_state = 'exact')
       OR resolution.id IS NOT NULL
), resolved_epoch AS (
    SELECT proxy.*,
           COALESCE(code_epoch.block_number, 0::numeric) AS implementation_epoch_block
    FROM resolved_proxy AS proxy
    LEFT JOIN LATERAL (
        SELECT max(change.block_number) AS block_number
        FROM transaction_state_changes AS change
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = change.chain_id
         AND canonical.number = change.block_number
         AND canonical.block_hash = change.block_hash
        WHERE change.chain_id = proxy.chain_id
          AND change.address = proxy.effective_implementation
          AND change.field_kind = 'code'
          AND change.canonical = TRUE
          AND change.block_number <= proxy.context_number
          AND lower(change.before_value) IS DISTINCT FROM lower(change.after_value)
    ) AS code_epoch ON TRUE
), latest_uups_probe AS (
    SELECT proxy.chain_id, proxy.proxy_address,
           latest.block_number, latest.block_hash,
           latest.implementation_code_hash, latest.verification_job_id,
           latest.standard_version, latest.probe_state,
           latest.proxiable_uuid, latest.upgrade_interface_version,
           latest.uups_generation_id
    FROM resolved_epoch AS proxy
    JOIN LATERAL (
        SELECT candidate.*
        FROM (
            SELECT observation.*,
                   generation.id AS uups_generation_id
            FROM uups_implementation_observations AS observation
            JOIN canonical_blocks AS canonical
              ON canonical.chain_id = observation.chain_id
             AND canonical.number = observation.block_number
             AND canonical.block_hash = observation.block_hash
            JOIN uups_implementation_observation_generations AS generation
              ON generation.chain_id = observation.chain_id
             AND generation.implementation_address = observation.implementation_address
             AND generation.observation_block_hash = observation.block_hash
             AND generation.observation_stage_version = observation.stage_version
             AND generation.verification_job_id = observation.verification_job_id
            JOIN published_block_stage_results AS published
              ON published.chain_id = generation.chain_id
             AND published.block_hash = generation.observation_block_hash
             AND published.stage = 'proxy'
             AND published.stage_version = generation.observation_stage_version
             AND published.durable_job_id = generation.durable_job_id
             AND published.job_generation = generation.job_generation
             AND published.state = 'complete'
            WHERE observation.chain_id = proxy.chain_id
              AND observation.implementation_address = proxy.effective_implementation
              AND observation.implementation_code_hash = proxy.effective_implementation_hash
              AND observation.stage_version = 2
              AND observation.canonical = TRUE
              AND observation.block_number <= proxy.context_number
            ORDER BY observation.block_number DESC,
                     observation.block_hash DESC,
                     generation.id DESC,
                     observation.verification_job_id DESC
            LIMIT 1
        ) AS candidate
        WHERE NOT EXISTS (
            SELECT 1
            FROM uups_implementation_observations AS conflict
            JOIN canonical_blocks AS conflict_canonical
              ON conflict_canonical.chain_id = conflict.chain_id
             AND conflict_canonical.number = conflict.block_number
             AND conflict_canonical.block_hash = conflict.block_hash
            JOIN uups_implementation_observation_generations AS conflict_generation
              ON conflict_generation.chain_id = conflict.chain_id
             AND conflict_generation.implementation_address = conflict.implementation_address
             AND conflict_generation.observation_block_hash = conflict.block_hash
             AND conflict_generation.observation_stage_version = conflict.stage_version
             AND conflict_generation.verification_job_id = conflict.verification_job_id
            JOIN published_block_stage_results AS conflict_published
              ON conflict_published.chain_id = conflict_generation.chain_id
             AND conflict_published.block_hash = conflict_generation.observation_block_hash
             AND conflict_published.stage = 'proxy'
             AND conflict_published.stage_version = conflict_generation.observation_stage_version
             AND conflict_published.durable_job_id = conflict_generation.durable_job_id
             AND conflict_published.job_generation = conflict_generation.job_generation
             AND conflict_published.state = 'complete'
            WHERE conflict.chain_id = candidate.chain_id
              AND conflict.implementation_address = candidate.implementation_address
              AND conflict.implementation_code_hash = candidate.implementation_code_hash
              AND conflict.block_number = candidate.block_number
              AND conflict.block_hash = candidate.block_hash
              AND conflict.stage_version = candidate.stage_version
              AND conflict.canonical = TRUE
              AND (
                  conflict.probe_state || ':' ||
                  COALESCE(conflict.rejection_reason, '')
              ) IS DISTINCT FROM (
                  candidate.probe_state || ':' ||
                  COALESCE(candidate.rejection_reason, '')
              )
        )
    ) AS latest ON proxy.effective_pattern = 'erc1967'
), uups_overlay AS (
    SELECT proxy.chain_id, proxy.proxy_address,
           probe.verification_job_id AS implementation_artifact_job_id,
           probe.uups_generation_id
    FROM resolved_epoch AS proxy
    JOIN latest_uups_probe AS probe
      ON probe.chain_id = proxy.chain_id
     AND probe.proxy_address = proxy.proxy_address
    JOIN verified_contract_proxy_artifacts AS artifact
      ON artifact.verification_job_id = probe.verification_job_id
     AND artifact.chain_id = probe.chain_id
     AND artifact.address = proxy.effective_implementation
     AND artifact.code_hash = proxy.effective_implementation_hash
     AND artifact.artifact_kind = 'uups_implementation'
     AND artifact.standard_version = '5.6.1'
     AND artifact.runtime_immutable_address = proxy.effective_implementation
    JOIN verified_contracts AS verified
      ON verified.chain_id = artifact.chain_id
     AND verified.address = artifact.address
     AND verified.code_hash = artifact.code_hash
     AND verified.valid_from_block = artifact.valid_from_block
     AND verified.verification_job_id = artifact.verification_job_id
     AND verified.request_digest = artifact.request_digest
    JOIN verification_jobs AS artifact_job
      ON artifact_job.id = artifact.verification_job_id
     AND artifact_job.kind = 'address'
     AND artifact_job.chain_id = artifact.chain_id
     AND artifact_job.address = artifact.address
     AND artifact_job.code_hash = artifact.code_hash
     AND artifact_job.status = 'succeeded'
    JOIN blocks AS artifact_block
      ON artifact_block.chain_id = artifact_job.chain_id
     AND artifact_block.hash = artifact_job.block_hash
    JOIN canonical_blocks AS artifact_canonical
      ON artifact_canonical.chain_id = artifact_block.chain_id
     AND artifact_canonical.number = artifact_block.number
     AND artifact_canonical.block_hash = artifact_block.hash
    WHERE proxy.effective_pattern = 'erc1967'
      AND probe.probe_state = 'compatible'
      AND probe.standard_version = '5.6.1'
      AND probe.proxiable_uuid = decode(
          '360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc',
          'hex'
      )
      AND probe.upgrade_interface_version = '5.0.0'
      AND probe.block_number >= proxy.implementation_epoch_block
      AND artifact.valid_from_block >= proxy.implementation_epoch_block
      AND artifact.valid_from_block <= proxy.context_number
      AND (verified.valid_to_block IS NULL OR
           verified.valid_to_block >= proxy.context_number)
      AND proxy_interaction_coverage_contains(
          proxy.chain_id,
          probe.block_number, probe.block_hash,
          proxy.context_number, proxy.context_hash
      )
), effective_proxy AS (
    SELECT proxy.*,
           CASE WHEN overlay.uups_generation_id IS NOT NULL THEN 'uups'
                ELSE proxy.effective_pattern END AS current_pattern,
           CASE WHEN overlay.uups_generation_id IS NOT NULL
                THEN overlay.implementation_artifact_job_id
                ELSE NULL::uuid END AS current_implementation_artifact_job_id,
           overlay.uups_generation_id
    FROM resolved_epoch AS proxy
    LEFT JOIN uups_overlay AS overlay
      ON overlay.chain_id = proxy.chain_id
     AND overlay.proxy_address = proxy.proxy_address
), latest_beacon AS (
    SELECT observation.*, proxy.context_number AS proxy_context_number
    FROM effective_proxy AS proxy
    JOIN LATERAL (
        SELECT observation.*
        FROM beacon_implementation_observations AS observation
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = observation.chain_id
         AND canonical.number = observation.block_number
         AND canonical.block_hash = observation.block_hash
        WHERE observation.chain_id = proxy.chain_id
          AND observation.beacon_address = proxy.effective_beacon
          AND observation.beacon_code_hash = proxy.effective_beacon_hash
          AND observation.stage_version = 2
          AND observation.canonical
          AND observation.confidence IN ('verified', 'high')
          AND observation.block_number <= proxy.context_number
        ORDER BY observation.block_number DESC, observation.block_hash DESC
        LIMIT 1
    ) AS observation ON proxy.current_pattern = 'beacon'
), published_beacon AS (
    SELECT beacon.*, generation.id AS beacon_generation_id,
           generation.durable_job_id AS beacon_durable_job_id,
           generation.job_generation AS beacon_job_generation
    FROM latest_beacon AS beacon
    JOIN LATERAL (
        SELECT witness.id, witness.durable_job_id, witness.job_generation
        FROM beacon_observation_generations AS witness
        JOIN published_block_stage_results AS published
          ON published.chain_id = witness.chain_id
         AND published.block_hash = witness.observation_block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = witness.observation_stage_version
         AND published.durable_job_id = witness.durable_job_id
         AND published.job_generation = witness.job_generation
         AND published.state = 'complete'
        WHERE witness.chain_id = beacon.chain_id
          AND witness.beacon_address = beacon.beacon_address
          AND witness.observation_block_hash = beacon.block_hash
          AND witness.observation_stage_version = beacon.stage_version
        ORDER BY witness.id DESC
        LIMIT 1
    ) AS generation ON TRUE
), unshadowed_beacon AS (
    SELECT beacon.*
    FROM published_beacon AS beacon
    WHERE NOT EXISTS (
        SELECT 1
        FROM proxy_detection_evidence AS evidence
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = evidence.chain_id
         AND canonical.number = evidence.block_number
         AND canonical.block_hash = evidence.block_hash
        JOIN published_block_stage_results AS published
          ON published.chain_id = evidence.chain_id
         AND published.block_hash = evidence.block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = evidence.stage_version
         AND published.durable_job_id = evidence.durable_job_id
         AND published.job_generation = evidence.job_generation
         AND published.state = 'complete'
        WHERE evidence.chain_id = beacon.chain_id
          AND evidence.address = beacon.beacon_address
          AND evidence.code_hash = beacon.beacon_code_hash
          AND evidence.candidate_kind = 'beacon'
          AND evidence.stage_version = beacon.stage_version
          AND evidence.canonical = TRUE
          AND evidence.block_number <= beacon.proxy_context_number
          AND (
              evidence.block_number > beacon.block_number OR (
                  evidence.block_number = beacon.block_number
                  AND evidence.block_hash = beacon.block_hash
                  AND evidence.durable_job_id = beacon.beacon_durable_job_id
                  AND evidence.job_generation >= beacon.beacon_job_generation
              )
          )
    )
), current_proxy AS (
    SELECT proxy.block_number, proxy.proxy_code_hash, proxy.block_hash,
           proxy.effective_kind AS proxy_kind,
           proxy.current_pattern AS proxy_pattern,
           proxy.effective_standard AS standard_version,
           CASE WHEN proxy.current_pattern = 'beacon'
                THEN beacon.implementation_address
                ELSE proxy.effective_implementation END AS implementation_address,
           CASE WHEN proxy.current_pattern = 'beacon'
                THEN beacon.implementation_code_hash
                ELSE proxy.effective_implementation_hash END AS implementation_code_hash,
           proxy.effective_admin AS admin_address,
           proxy.effective_admin_hash AS admin_code_hash,
           proxy.effective_beacon AS beacon_address,
           proxy.effective_beacon_hash AS beacon_code_hash,
           proxy.observation_generation_id, proxy.artifact_resolution_id,
           proxy.proxy_artifact_job_id,
           proxy.current_implementation_artifact_job_id AS implementation_artifact_job_id,
           beacon.beacon_generation_id, proxy.uups_generation_id,
           proxy.context_number,
           proxy.context_hash,
           CASE proxy.current_pattern
               WHEN 'transparent' THEN 'proxy_admin'
               WHEN 'beacon' THEN 'upgradeable_beacon'
               ELSE 'none'
           END AS management_kind,
           CASE proxy.current_pattern
               WHEN 'transparent' THEN proxy.effective_admin
               WHEN 'beacon' THEN proxy.effective_beacon
               ELSE NULL
           END AS management_address,
           CASE proxy.current_pattern
               WHEN 'transparent' THEN proxy.effective_admin_hash
               WHEN 'beacon' THEN proxy.effective_beacon_hash
               ELSE NULL
           END AS management_code_hash
    FROM effective_proxy AS proxy
    LEFT JOIN unshadowed_beacon AS beacon
      ON proxy.current_pattern = 'beacon'
     AND beacon.beacon_address = proxy.effective_beacon
    WHERE proxy.current_pattern <> 'beacon' OR beacon.beacon_generation_id IS NOT NULL
), identity_candidates(address, code_hash, context_number) AS (
    SELECT $2::bytea, proxy_code_hash, context_number FROM current_proxy
    UNION ALL SELECT implementation_address, implementation_code_hash, context_number FROM current_proxy
    UNION ALL SELECT admin_address, admin_code_hash, context_number FROM current_proxy
    UNION ALL SELECT beacon_address, beacon_code_hash, context_number FROM current_proxy
    UNION ALL SELECT management_address, management_code_hash, context_number FROM current_proxy
), expected_identity(address, code_hash, epoch_block) AS (
    SELECT DISTINCT identity.address, identity.code_hash,
           COALESCE(code_epoch.block_number, 0::numeric)
    FROM identity_candidates AS identity
    LEFT JOIN LATERAL (
        SELECT max(change.block_number) AS block_number
        FROM transaction_state_changes AS change
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = change.chain_id
         AND canonical.number = change.block_number
         AND canonical.block_hash = change.block_hash
        WHERE change.chain_id = $1::numeric
          AND change.address = identity.address
          AND change.field_kind = 'code'
          AND change.canonical = TRUE
          AND change.block_number <= identity.context_number
          AND lower(change.before_value) IS DISTINCT FROM
              lower(change.after_value)
    ) AS code_epoch ON TRUE
    WHERE identity.address IS NOT NULL
), current_identity AS (
    SELECT expected.address, expected.code_hash,
           current_code.code_hash AS current_code_hash
    FROM canonical_tip AS tip
    CROSS JOIN expected_identity AS expected
    LEFT JOIN LATERAL (
        SELECT observation.code_hash
        FROM contract_code_observations AS observation
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = observation.chain_id
         AND canonical.number = observation.block_number
         AND canonical.block_hash = observation.block_hash
        WHERE observation.chain_id = $1::numeric
          AND observation.address = expected.address
          AND observation.canonical = TRUE
          AND observation.block_number <= tip.number
        ORDER BY observation.block_number DESC,
                 observation.observed_at DESC,
                 observation.code_hash DESC
        LIMIT 1
    ) AS current_code ON TRUE
 )
, reusable_binding AS (
    SELECT binding.verification_job_id
    FROM current_proxy
    JOIN verified_proxy_bindings AS binding
      ON binding.chain_id = $1::numeric
     AND binding.proxy_address = $2::bytea
     AND binding.observation_stage_version = 2
     AND binding.observation_block_number = current_proxy.block_number
     AND binding.observation_block_hash = current_proxy.block_hash
     AND binding.observation_generation_id = current_proxy.observation_generation_id
     AND binding.artifact_resolution_id IS NOT DISTINCT FROM
         current_proxy.artifact_resolution_id
     AND binding.beacon_generation_id IS NOT DISTINCT FROM
         current_proxy.beacon_generation_id
     AND binding.uups_generation_id IS NOT DISTINCT FROM
         current_proxy.uups_generation_id
     AND binding.proxy_code_hash = current_proxy.proxy_code_hash
     AND binding.proxy_kind = current_proxy.proxy_kind
     AND binding.proxy_pattern = current_proxy.proxy_pattern
     AND binding.standard_version IS NOT DISTINCT FROM current_proxy.standard_version
     AND binding.implementation_address = current_proxy.implementation_address
     AND binding.implementation_code_hash = current_proxy.implementation_code_hash
     AND binding.admin_address IS NOT DISTINCT FROM current_proxy.admin_address
     AND binding.admin_code_hash IS NOT DISTINCT FROM current_proxy.admin_code_hash
     AND binding.beacon_address IS NOT DISTINCT FROM current_proxy.beacon_address
     AND binding.beacon_code_hash IS NOT DISTINCT FROM current_proxy.beacon_code_hash
     AND binding.management_kind = current_proxy.management_kind
     AND binding.management_address IS NOT DISTINCT FROM
         current_proxy.management_address
     AND binding.management_code_hash IS NOT DISTINCT FROM
         current_proxy.management_code_hash
    JOIN canonical_blocks AS binding_context
      ON binding_context.chain_id = binding.chain_id
     AND binding_context.number = binding.context_block_number
     AND binding_context.block_hash = binding.context_block_hash
    WHERE proxy_interaction_coverage_contains(
              binding.chain_id,
              binding.observation_block_number,
              binding.observation_block_hash,
              current_proxy.context_number,
              current_proxy.context_hash
          )
      AND NOT EXISTS (
        SELECT 1
        FROM (VALUES
            (binding.proxy_address, binding.proxy_code_hash),
            (binding.implementation_address, binding.implementation_code_hash),
            (binding.admin_address, binding.admin_code_hash),
            (binding.beacon_address, binding.beacon_code_hash),
            (binding.management_address, binding.management_code_hash)
        ) AS identity(address, code_hash)
        JOIN contract_code_observations AS observation
          ON observation.chain_id = binding.chain_id
         AND observation.address = identity.address
         AND observation.canonical = TRUE
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = observation.chain_id
         AND canonical.number = observation.block_number
         AND canonical.block_hash = observation.block_hash
        WHERE identity.address IS NOT NULL
          AND observation.block_number > binding.context_block_number
          AND observation.block_number <= current_proxy.context_number
          AND observation.code_hash IS DISTINCT FROM identity.code_hash
    )
      AND NOT EXISTS (
          SELECT 1
          FROM expected_identity AS identity
          JOIN transaction_state_changes AS change
            ON change.chain_id = binding.chain_id
           AND change.address = identity.address
           AND change.field_kind = 'code'
           AND change.canonical = TRUE
          JOIN canonical_blocks AS canonical
            ON canonical.chain_id = change.chain_id
           AND canonical.number = change.block_number
           AND canonical.block_hash = change.block_hash
          WHERE change.block_number > binding.context_block_number
            AND change.block_number <= current_proxy.context_number
            AND lower(change.before_value) IS DISTINCT FROM
                lower(change.after_value)
      )
    ORDER BY binding.created_at DESC, binding.verification_job_id DESC
    LIMIT 1
)
SELECT current_proxy.proxy_code_hash, current_proxy.block_hash,
       current_proxy.context_number::text, current_proxy.context_hash,
       current_proxy.proxy_kind, current_proxy.proxy_pattern,
       current_proxy.standard_version, current_proxy.implementation_address,
       current_proxy.implementation_code_hash, current_proxy.admin_address,
       current_proxy.admin_code_hash, current_proxy.beacon_address,
       current_proxy.beacon_code_hash, current_proxy.management_kind,
       current_proxy.management_address, current_proxy.management_code_hash,
       current_proxy.observation_generation_id,
       current_proxy.artifact_resolution_id,
       current_proxy.beacon_generation_id,
       current_proxy.uups_generation_id,
       current_proxy.proxy_pattern = 'clone' OR (
           EXISTS (
               SELECT 1
               FROM expected_identity AS identity
               JOIN verified_contracts AS verified
                 ON verified.chain_id = $1::numeric
                AND verified.address = identity.address
                AND verified.code_hash = identity.code_hash
               WHERE identity.address = $2::bytea
                 AND identity.code_hash = current_proxy.proxy_code_hash
                 AND verified.valid_from_block >= identity.epoch_block
                 AND verified.valid_from_block <= current_proxy.context_number
                 AND (verified.valid_to_block IS NULL
                      OR verified.valid_to_block >= current_proxy.context_number)
           )
           AND EXISTS (
               SELECT 1
               FROM verified_contract_proxy_artifacts AS artifact
               JOIN verified_contracts AS verified
                 ON verified.chain_id = artifact.chain_id
                AND verified.address = artifact.address
                AND verified.code_hash = artifact.code_hash
                AND verified.valid_from_block = artifact.valid_from_block
                AND verified.verification_job_id = artifact.verification_job_id
                AND verified.request_digest = artifact.request_digest
               JOIN expected_identity AS identity
                 ON identity.address = artifact.address
                AND identity.code_hash = artifact.code_hash
               WHERE artifact.verification_job_id =
                     current_proxy.proxy_artifact_job_id
                 AND artifact.chain_id = $1::numeric
                 AND artifact.address = $2
                 AND artifact.code_hash = current_proxy.proxy_code_hash
                 AND artifact.standard_version = '5.6.1'
                 AND artifact.artifact_kind = CASE current_proxy.proxy_pattern
                     WHEN 'erc1967' THEN 'erc1967_proxy'
                     WHEN 'transparent' THEN 'transparent_proxy'
                     WHEN 'uups' THEN 'erc1967_proxy'
                     WHEN 'beacon' THEN 'beacon_proxy'
                 END
                 AND artifact.valid_from_block >= identity.epoch_block
                 AND artifact.valid_from_block <= current_proxy.context_number
                 AND (verified.valid_to_block IS NULL
                      OR verified.valid_to_block >= current_proxy.context_number)
           )
       ),
       EXISTS (
           SELECT 1
           FROM expected_identity AS identity
           JOIN verified_contracts AS verified
             ON verified.chain_id = $1::numeric
            AND verified.address = identity.address
            AND verified.code_hash = identity.code_hash
           WHERE identity.address = current_proxy.implementation_address
             AND identity.code_hash = current_proxy.implementation_code_hash
             AND verified.valid_from_block >= identity.epoch_block
             AND verified.valid_from_block <= current_proxy.context_number
             AND (verified.valid_to_block IS NULL
                  OR verified.valid_to_block >= current_proxy.context_number)
       ) AND (
           current_proxy.proxy_pattern <> 'uups' OR EXISTS (
               SELECT 1
               FROM verified_contract_proxy_artifacts AS artifact
               JOIN verified_contracts AS verified
                 ON verified.chain_id = artifact.chain_id
                AND verified.address = artifact.address
                AND verified.code_hash = artifact.code_hash
                AND verified.valid_from_block = artifact.valid_from_block
                AND verified.verification_job_id = artifact.verification_job_id
                AND verified.request_digest = artifact.request_digest
               JOIN expected_identity AS identity
                 ON identity.address = artifact.address
                AND identity.code_hash = artifact.code_hash
               WHERE artifact.verification_job_id =
                     current_proxy.implementation_artifact_job_id
                 AND artifact.chain_id = $1::numeric
                 AND artifact.address = current_proxy.implementation_address
                 AND artifact.code_hash = current_proxy.implementation_code_hash
                 AND artifact.standard_version = '5.6.1'
                 AND artifact.artifact_kind = 'uups_implementation'
                 AND artifact.valid_from_block >= identity.epoch_block
                 AND artifact.valid_from_block <= current_proxy.context_number
                 AND (verified.valid_to_block IS NULL
                      OR verified.valid_to_block >= current_proxy.context_number)
           )
       ),
       current_proxy.management_kind = 'none' OR EXISTS (
           SELECT 1
           FROM verified_contract_proxy_artifacts AS artifact
           JOIN verified_contracts AS verified
             ON verified.chain_id = artifact.chain_id
            AND verified.address = artifact.address
            AND verified.code_hash = artifact.code_hash
            AND verified.valid_from_block = artifact.valid_from_block
            AND verified.verification_job_id = artifact.verification_job_id
            AND verified.request_digest = artifact.request_digest
           JOIN expected_identity AS identity
             ON identity.address = artifact.address
            AND identity.code_hash = artifact.code_hash
           WHERE artifact.chain_id = $1::numeric
             AND artifact.address = current_proxy.management_address
             AND artifact.code_hash = current_proxy.management_code_hash
             AND artifact.standard_version = '5.6.1'
             AND artifact.artifact_kind = CASE current_proxy.management_kind
                 WHEN 'proxy_admin' THEN 'proxy_admin'
                 WHEN 'upgradeable_beacon' THEN 'upgradeable_beacon'
             END
             AND artifact.valid_from_block >= identity.epoch_block
             AND artifact.valid_from_block <= current_proxy.context_number
             AND (verified.valid_to_block IS NULL
                  OR verified.valid_to_block >= current_proxy.context_number)
       ),
       (SELECT binding.verification_job_id::text FROM reusable_binding AS binding)
FROM current_proxy
WHERE proxy_interaction_coverage_contains(
          $1::numeric,
          current_proxy.block_number,
          current_proxy.block_hash,
          current_proxy.context_number,
          current_proxy.context_hash
      )
  AND NOT EXISTS (
    SELECT 1
    FROM current_identity AS identity
    WHERE identity.current_code_hash IS DISTINCT FROM identity.code_hash
);

-- name: EtherscanTransactionStatus :many
SELECT receipt.raw, receipt.tx_hash, receipt.block_hash,
       receipt.block_number::text, receipt.tx_index
FROM receipts AS receipt
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = receipt.chain_id
 AND canonical.number = receipt.block_number
 AND canonical.block_hash = receipt.block_hash
WHERE receipt.chain_id = $1::numeric
  AND receipt.tx_hash = $2
LIMIT 1;

-- name: EtherscanVerificationTarget :many
WITH current_code AS (
    SELECT observation.code_hash, observation.block_hash,
           observation.block_number, observation.code
    FROM contract_code_observations AS observation
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = observation.chain_id
     AND canonical.number = observation.block_number
     AND canonical.block_hash = observation.block_hash
    WHERE observation.chain_id = $1::numeric
      AND observation.address = $2
      AND observation.canonical = TRUE
    ORDER BY observation.block_number DESC,
             observation.observed_at DESC,
             observation.code_hash DESC
    LIMIT 1
)
SELECT current_code.code_hash, current_code.block_hash, current_code.code,
       creation.creation_bytecode,
       EXISTS (
           SELECT 1
           FROM genesis_state_imports AS imported
           JOIN canonical_blocks AS genesis_canonical
             ON genesis_canonical.chain_id = imported.chain_id
            AND genesis_canonical.number = 0
            AND genesis_canonical.block_hash = imported.block_hash
           JOIN genesis_account_observations AS account
             ON account.chain_id = imported.chain_id
            AND account.block_hash = imported.block_hash
            AND account.address = $2
           WHERE imported.chain_id = $1::numeric
             AND imported.state = 'complete'
             AND octet_length(account.code) > 0
             AND account.code_hash = current_code.code_hash
             AND account.code = current_code.code
       ) AS genesis_predeploy
FROM current_code
LEFT JOIN LATERAL (
    SELECT candidate.creation_bytecode
    FROM (
        SELECT inclusion.raw->>'input' AS creation_bytecode,
               receipt.block_number, receipt.tx_index,
               ''::text AS trace_path, 0 AS source_rank
        FROM receipts AS receipt
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = receipt.chain_id
         AND canonical.number = receipt.block_number
         AND canonical.block_hash = receipt.block_hash
        JOIN transaction_inclusions AS inclusion
          ON inclusion.chain_id = receipt.chain_id
         AND inclusion.block_number = receipt.block_number
         AND inclusion.block_hash = receipt.block_hash
         AND inclusion.tx_index = receipt.tx_index
        WHERE receipt.chain_id = $1::numeric
          AND lower(receipt.raw->>'contractAddress') = $3
          AND receipt.block_number <= current_code.block_number
          AND inclusion.raw->>'input' IS NOT NULL

        UNION ALL

        SELECT '0x' || encode(trace.input, 'hex') AS creation_bytecode,
               trace.block_number, trace.transaction_index,
               trace.trace_path, 1 AS source_rank
        FROM normalized_traces AS trace
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = trace.chain_id
         AND canonical.number = trace.block_number
         AND canonical.block_hash = trace.block_hash
        WHERE trace.chain_id = $1::numeric
          AND trace.created_address = $2
          AND trace.canonical = TRUE
          AND trace.reverted = FALSE
          AND trace.input IS NOT NULL
          AND trace.block_number <= current_code.block_number
    ) AS candidate
    ORDER BY candidate.block_number DESC, candidate.tx_index DESC,
             candidate.source_rank DESC, candidate.trace_path DESC
       LIMIT 1
    ) AS creation ON TRUE;

-- name: EtherscanVerifiedProxy :many
WITH canonical_tip AS (
    SELECT number, block_hash
    FROM canonical_blocks
    WHERE chain_id = $1::numeric
    ORDER BY number DESC
    LIMIT 1
), latest_raw AS (
    SELECT observation.*, tip.number AS context_number,
           tip.block_hash AS context_hash
    FROM canonical_tip AS tip
    JOIN LATERAL (
        SELECT observation.*
        FROM proxy_observations AS observation
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = observation.chain_id
         AND canonical.number = observation.block_number
         AND canonical.block_hash = observation.block_hash
        WHERE observation.chain_id = $1::numeric
          AND observation.proxy_address = $2::bytea
          AND observation.canonical = TRUE
          AND observation.stage_version = 2
          AND observation.confidence IN ('verified', 'high')
          AND observation.block_number <= tip.number
        ORDER BY observation.block_number DESC, observation.block_hash DESC
        LIMIT 1
    ) AS observation ON TRUE
), published_raw AS (
    SELECT raw.*, generation.id AS observation_generation_id,
           generation.durable_job_id AS observation_durable_job_id,
           generation.job_generation AS observation_job_generation
    FROM latest_raw AS raw
    JOIN LATERAL (
        SELECT witness.id, witness.durable_job_id, witness.job_generation
        FROM proxy_observation_generations AS witness
        JOIN published_block_stage_results AS published
          ON published.chain_id = witness.chain_id
         AND published.block_hash = witness.observation_block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = witness.observation_stage_version
         AND published.durable_job_id = witness.durable_job_id
         AND published.job_generation = witness.job_generation
         AND published.state = 'complete'
        WHERE witness.chain_id = raw.chain_id
          AND witness.proxy_address = raw.proxy_address
          AND witness.observation_block_hash = raw.block_hash
          AND witness.observation_stage_version = raw.stage_version
        ORDER BY witness.id DESC
        LIMIT 1
    ) AS generation ON TRUE
), unshadowed_raw AS (
    SELECT raw.*
    FROM published_raw AS raw
    WHERE NOT EXISTS (
        SELECT 1
        FROM proxy_detection_evidence AS evidence
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = evidence.chain_id
         AND canonical.number = evidence.block_number
         AND canonical.block_hash = evidence.block_hash
        JOIN published_block_stage_results AS published
          ON published.chain_id = evidence.chain_id
         AND published.block_hash = evidence.block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = evidence.stage_version
         AND published.durable_job_id = evidence.durable_job_id
         AND published.job_generation = evidence.job_generation
         AND published.state = 'complete'
        WHERE evidence.chain_id = raw.chain_id
          AND evidence.address = raw.proxy_address
          AND evidence.code_hash = raw.proxy_code_hash
          AND evidence.candidate_kind = 'proxy'
          AND evidence.stage_version = raw.stage_version
          AND evidence.canonical = TRUE
          AND evidence.block_number <= raw.context_number
          AND NOT (
              evidence.reason = 'immutable_args_creation_unverified'
              AND raw.proxy_pattern = 'clone'
              AND raw.evidence_state = 'exact'
              AND octet_length(raw.immutable_args) > 0
              AND raw.details->>'immutable_args_creation_authenticated' = 'true'
          )
          AND (
              evidence.block_number > raw.block_number OR (
                  evidence.block_number = raw.block_number
                  AND evidence.block_hash = raw.block_hash
                  AND evidence.durable_job_id = raw.observation_durable_job_id
                  AND evidence.job_generation >= raw.observation_job_generation
              )
          )
    )
), resolved_proxy AS (
    SELECT raw.*, resolution.id AS artifact_resolution_id,
           resolution.proxy_artifact_job_id,
           resolution.implementation_artifact_job_id,
           CASE WHEN raw.proxy_pattern = 'clone' THEN raw.proxy_kind
                ELSE resolution.proxy_kind END AS effective_kind,
           CASE WHEN raw.proxy_pattern = 'clone' THEN raw.proxy_pattern
                WHEN resolution.proxy_pattern = 'uups' THEN 'erc1967'
                ELSE resolution.proxy_pattern END AS effective_pattern,
           CASE WHEN raw.proxy_pattern = 'clone' THEN NULL::text
                ELSE resolution.standard_version END AS effective_standard,
           CASE WHEN raw.proxy_pattern = 'clone' THEN raw.implementation_address
                ELSE resolution.implementation_address END AS effective_implementation,
           CASE WHEN raw.proxy_pattern = 'clone' THEN raw.implementation_code_hash
                ELSE resolution.implementation_code_hash END AS effective_implementation_hash,
           resolution.admin_address AS effective_admin,
           resolution.admin_code_hash AS effective_admin_hash,
           resolution.beacon_address AS effective_beacon,
           resolution.beacon_code_hash AS effective_beacon_hash
    FROM unshadowed_raw AS raw
    LEFT JOIN LATERAL (
        SELECT candidate.*
        FROM proxy_artifact_resolutions AS candidate
        JOIN published_block_stage_results AS published
          ON published.chain_id = candidate.chain_id
         AND published.block_hash = candidate.observation_block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = candidate.observation_stage_version
         AND published.durable_job_id = candidate.durable_job_id
         AND published.job_generation = candidate.job_generation
         AND published.state = 'complete'
        WHERE candidate.chain_id = raw.chain_id
          AND candidate.proxy_address = raw.proxy_address
          AND candidate.observation_block_hash = raw.block_hash
          AND candidate.observation_stage_version = raw.stage_version
          AND candidate.proxy_code_hash = raw.proxy_code_hash
          AND candidate.proxy_pattern <> 'uups'
        ORDER BY candidate.id DESC
        LIMIT 1
    ) AS resolution ON raw.proxy_pattern <> 'clone'
    WHERE (raw.proxy_pattern = 'clone' AND raw.evidence_state = 'exact')
       OR resolution.id IS NOT NULL
), resolved_epoch AS (
    SELECT proxy.*,
           COALESCE(code_epoch.block_number, 0::numeric) AS implementation_epoch_block
    FROM resolved_proxy AS proxy
    LEFT JOIN LATERAL (
        SELECT max(change.block_number) AS block_number
        FROM transaction_state_changes AS change
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = change.chain_id
         AND canonical.number = change.block_number
         AND canonical.block_hash = change.block_hash
        WHERE change.chain_id = proxy.chain_id
          AND change.address = proxy.effective_implementation
          AND change.field_kind = 'code'
          AND change.canonical = TRUE
          AND change.block_number <= proxy.context_number
          AND lower(change.before_value) IS DISTINCT FROM lower(change.after_value)
    ) AS code_epoch ON TRUE
), latest_uups_probe AS (
    SELECT proxy.chain_id, proxy.proxy_address,
           latest.block_number, latest.block_hash,
           latest.implementation_code_hash, latest.verification_job_id,
           latest.standard_version, latest.probe_state,
           latest.proxiable_uuid, latest.upgrade_interface_version,
           latest.uups_generation_id
    FROM resolved_epoch AS proxy
    JOIN LATERAL (
        SELECT candidate.*
        FROM (
            SELECT observation.*,
                   generation.id AS uups_generation_id
            FROM uups_implementation_observations AS observation
            JOIN canonical_blocks AS canonical
              ON canonical.chain_id = observation.chain_id
             AND canonical.number = observation.block_number
             AND canonical.block_hash = observation.block_hash
            JOIN uups_implementation_observation_generations AS generation
              ON generation.chain_id = observation.chain_id
             AND generation.implementation_address = observation.implementation_address
             AND generation.observation_block_hash = observation.block_hash
             AND generation.observation_stage_version = observation.stage_version
             AND generation.verification_job_id = observation.verification_job_id
            JOIN published_block_stage_results AS published
              ON published.chain_id = generation.chain_id
             AND published.block_hash = generation.observation_block_hash
             AND published.stage = 'proxy'
             AND published.stage_version = generation.observation_stage_version
             AND published.durable_job_id = generation.durable_job_id
             AND published.job_generation = generation.job_generation
             AND published.state = 'complete'
            WHERE observation.chain_id = proxy.chain_id
              AND observation.implementation_address = proxy.effective_implementation
              AND observation.implementation_code_hash = proxy.effective_implementation_hash
              AND observation.stage_version = 2
              AND observation.canonical = TRUE
              AND observation.block_number <= proxy.context_number
            ORDER BY observation.block_number DESC,
                     observation.block_hash DESC,
                     generation.id DESC,
                     observation.verification_job_id DESC
            LIMIT 1
        ) AS candidate
        WHERE NOT EXISTS (
            SELECT 1
            FROM uups_implementation_observations AS conflict
            JOIN canonical_blocks AS conflict_canonical
              ON conflict_canonical.chain_id = conflict.chain_id
             AND conflict_canonical.number = conflict.block_number
             AND conflict_canonical.block_hash = conflict.block_hash
            JOIN uups_implementation_observation_generations AS conflict_generation
              ON conflict_generation.chain_id = conflict.chain_id
             AND conflict_generation.implementation_address = conflict.implementation_address
             AND conflict_generation.observation_block_hash = conflict.block_hash
             AND conflict_generation.observation_stage_version = conflict.stage_version
             AND conflict_generation.verification_job_id = conflict.verification_job_id
            JOIN published_block_stage_results AS conflict_published
              ON conflict_published.chain_id = conflict_generation.chain_id
             AND conflict_published.block_hash = conflict_generation.observation_block_hash
             AND conflict_published.stage = 'proxy'
             AND conflict_published.stage_version = conflict_generation.observation_stage_version
             AND conflict_published.durable_job_id = conflict_generation.durable_job_id
             AND conflict_published.job_generation = conflict_generation.job_generation
             AND conflict_published.state = 'complete'
            WHERE conflict.chain_id = candidate.chain_id
              AND conflict.implementation_address = candidate.implementation_address
              AND conflict.implementation_code_hash = candidate.implementation_code_hash
              AND conflict.block_number = candidate.block_number
              AND conflict.block_hash = candidate.block_hash
              AND conflict.stage_version = candidate.stage_version
              AND conflict.canonical = TRUE
              AND (
                  conflict.probe_state || ':' ||
                  COALESCE(conflict.rejection_reason, '')
              ) IS DISTINCT FROM (
                  candidate.probe_state || ':' ||
                  COALESCE(candidate.rejection_reason, '')
              )
        )
    ) AS latest ON proxy.effective_pattern = 'erc1967'
), uups_overlay AS (
    SELECT proxy.chain_id, proxy.proxy_address,
           probe.verification_job_id AS implementation_artifact_job_id,
           probe.uups_generation_id
    FROM resolved_epoch AS proxy
    JOIN latest_uups_probe AS probe
      ON probe.chain_id = proxy.chain_id
     AND probe.proxy_address = proxy.proxy_address
    JOIN verified_contract_proxy_artifacts AS artifact
      ON artifact.verification_job_id = probe.verification_job_id
     AND artifact.chain_id = probe.chain_id
     AND artifact.address = proxy.effective_implementation
     AND artifact.code_hash = proxy.effective_implementation_hash
     AND artifact.artifact_kind = 'uups_implementation'
     AND artifact.standard_version = '5.6.1'
     AND artifact.runtime_immutable_address = proxy.effective_implementation
    JOIN verified_contracts AS verified
      ON verified.chain_id = artifact.chain_id
     AND verified.address = artifact.address
     AND verified.code_hash = artifact.code_hash
     AND verified.valid_from_block = artifact.valid_from_block
     AND verified.verification_job_id = artifact.verification_job_id
     AND verified.request_digest = artifact.request_digest
    JOIN verification_jobs AS artifact_job
      ON artifact_job.id = artifact.verification_job_id
     AND artifact_job.kind = 'address'
     AND artifact_job.chain_id = artifact.chain_id
     AND artifact_job.address = artifact.address
     AND artifact_job.code_hash = artifact.code_hash
     AND artifact_job.status = 'succeeded'
    JOIN blocks AS artifact_block
      ON artifact_block.chain_id = artifact_job.chain_id
     AND artifact_block.hash = artifact_job.block_hash
    JOIN canonical_blocks AS artifact_canonical
      ON artifact_canonical.chain_id = artifact_block.chain_id
     AND artifact_canonical.number = artifact_block.number
     AND artifact_canonical.block_hash = artifact_block.hash
    WHERE proxy.effective_pattern = 'erc1967'
      AND probe.probe_state = 'compatible'
      AND probe.standard_version = '5.6.1'
      AND probe.proxiable_uuid = decode(
          '360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc',
          'hex'
      )
      AND probe.upgrade_interface_version = '5.0.0'
      AND probe.block_number >= proxy.implementation_epoch_block
      AND artifact.valid_from_block >= proxy.implementation_epoch_block
      AND artifact.valid_from_block <= proxy.context_number
      AND (verified.valid_to_block IS NULL OR
           verified.valid_to_block >= proxy.context_number)
      AND proxy_interaction_coverage_contains(
          proxy.chain_id,
          probe.block_number, probe.block_hash,
          proxy.context_number, proxy.context_hash
      )
), effective_proxy AS (
    SELECT proxy.*,
           CASE WHEN overlay.uups_generation_id IS NOT NULL THEN 'uups'
                ELSE proxy.effective_pattern END AS current_pattern,
           CASE WHEN overlay.uups_generation_id IS NOT NULL
                THEN overlay.implementation_artifact_job_id
                ELSE NULL::uuid END AS current_implementation_artifact_job_id,
           overlay.uups_generation_id
    FROM resolved_epoch AS proxy
    LEFT JOIN uups_overlay AS overlay
      ON overlay.chain_id = proxy.chain_id
     AND overlay.proxy_address = proxy.proxy_address
), latest_beacon AS (
    SELECT observation.*, proxy.context_number AS proxy_context_number
    FROM effective_proxy AS proxy
    JOIN LATERAL (
        SELECT observation.*
        FROM beacon_implementation_observations AS observation
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = observation.chain_id
         AND canonical.number = observation.block_number
         AND canonical.block_hash = observation.block_hash
        WHERE observation.chain_id = proxy.chain_id
          AND observation.beacon_address = proxy.effective_beacon
          AND observation.beacon_code_hash = proxy.effective_beacon_hash
          AND observation.stage_version = 2
          AND observation.canonical
          AND observation.confidence IN ('verified', 'high')
          AND observation.block_number <= proxy.context_number
        ORDER BY observation.block_number DESC, observation.block_hash DESC
        LIMIT 1
    ) AS observation ON proxy.current_pattern = 'beacon'
), published_beacon AS (
    SELECT beacon.*, generation.id AS beacon_generation_id,
           generation.durable_job_id AS beacon_durable_job_id,
           generation.job_generation AS beacon_job_generation
    FROM latest_beacon AS beacon
    JOIN LATERAL (
        SELECT witness.id, witness.durable_job_id, witness.job_generation
        FROM beacon_observation_generations AS witness
        JOIN published_block_stage_results AS published
          ON published.chain_id = witness.chain_id
         AND published.block_hash = witness.observation_block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = witness.observation_stage_version
         AND published.durable_job_id = witness.durable_job_id
         AND published.job_generation = witness.job_generation
         AND published.state = 'complete'
        WHERE witness.chain_id = beacon.chain_id
          AND witness.beacon_address = beacon.beacon_address
          AND witness.observation_block_hash = beacon.block_hash
          AND witness.observation_stage_version = beacon.stage_version
        ORDER BY witness.id DESC
        LIMIT 1
    ) AS generation ON TRUE
), unshadowed_beacon AS (
    SELECT beacon.*
    FROM published_beacon AS beacon
    WHERE NOT EXISTS (
        SELECT 1
        FROM proxy_detection_evidence AS evidence
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = evidence.chain_id
         AND canonical.number = evidence.block_number
         AND canonical.block_hash = evidence.block_hash
        JOIN published_block_stage_results AS published
          ON published.chain_id = evidence.chain_id
         AND published.block_hash = evidence.block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = evidence.stage_version
         AND published.durable_job_id = evidence.durable_job_id
         AND published.job_generation = evidence.job_generation
         AND published.state = 'complete'
        WHERE evidence.chain_id = beacon.chain_id
          AND evidence.address = beacon.beacon_address
          AND evidence.code_hash = beacon.beacon_code_hash
          AND evidence.candidate_kind = 'beacon'
          AND evidence.stage_version = beacon.stage_version
          AND evidence.canonical = TRUE
          AND evidence.block_number <= beacon.proxy_context_number
          AND (
              evidence.block_number > beacon.block_number OR (
                  evidence.block_number = beacon.block_number
                  AND evidence.block_hash = beacon.block_hash
                  AND evidence.durable_job_id = beacon.beacon_durable_job_id
                  AND evidence.job_generation >= beacon.beacon_job_generation
              )
          )
    )
), current_proxy AS (
    SELECT proxy.block_number, proxy.proxy_code_hash, proxy.block_hash,
           proxy.effective_kind AS proxy_kind,
           proxy.current_pattern AS proxy_pattern,
           proxy.effective_standard AS standard_version,
           CASE WHEN proxy.current_pattern = 'beacon'
                THEN beacon.implementation_address
                ELSE proxy.effective_implementation END AS implementation_address,
           CASE WHEN proxy.current_pattern = 'beacon'
                THEN beacon.implementation_code_hash
                ELSE proxy.effective_implementation_hash END AS implementation_code_hash,
           proxy.effective_admin AS admin_address,
           proxy.effective_admin_hash AS admin_code_hash,
           proxy.effective_beacon AS beacon_address,
           proxy.effective_beacon_hash AS beacon_code_hash,
           proxy.observation_generation_id, proxy.artifact_resolution_id,
           proxy.proxy_artifact_job_id,
           proxy.current_implementation_artifact_job_id AS implementation_artifact_job_id,
           beacon.beacon_generation_id, proxy.uups_generation_id,
           proxy.context_number,
           proxy.context_hash,
           CASE proxy.current_pattern
               WHEN 'transparent' THEN 'proxy_admin'
               WHEN 'beacon' THEN 'upgradeable_beacon'
               ELSE 'none'
           END AS management_kind,
           CASE proxy.current_pattern
               WHEN 'transparent' THEN proxy.effective_admin
               WHEN 'beacon' THEN proxy.effective_beacon
               ELSE NULL
           END AS management_address,
           CASE proxy.current_pattern
               WHEN 'transparent' THEN proxy.effective_admin_hash
               WHEN 'beacon' THEN proxy.effective_beacon_hash
               ELSE NULL
           END AS management_code_hash
    FROM effective_proxy AS proxy
    LEFT JOIN unshadowed_beacon AS beacon
      ON proxy.current_pattern = 'beacon'
     AND beacon.beacon_address = proxy.effective_beacon
    WHERE proxy.current_pattern <> 'beacon' OR beacon.beacon_generation_id IS NOT NULL
), identity_candidates(address, code_hash, context_number) AS (
    SELECT $2::bytea, proxy_code_hash, context_number FROM current_proxy
    UNION ALL SELECT implementation_address, implementation_code_hash, context_number FROM current_proxy
    UNION ALL SELECT admin_address, admin_code_hash, context_number FROM current_proxy
    UNION ALL SELECT beacon_address, beacon_code_hash, context_number FROM current_proxy
    UNION ALL SELECT management_address, management_code_hash, context_number FROM current_proxy
), expected_identity(address, code_hash, epoch_block) AS (
    SELECT DISTINCT identity.address, identity.code_hash,
           COALESCE(code_epoch.block_number, 0::numeric)
    FROM identity_candidates AS identity
    LEFT JOIN LATERAL (
        SELECT max(change.block_number) AS block_number
        FROM transaction_state_changes AS change
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = change.chain_id
         AND canonical.number = change.block_number
         AND canonical.block_hash = change.block_hash
        WHERE change.chain_id = $1::numeric
          AND change.address = identity.address
          AND change.field_kind = 'code'
          AND change.canonical = TRUE
          AND change.block_number <= identity.context_number
          AND lower(change.before_value) IS DISTINCT FROM
              lower(change.after_value)
    ) AS code_epoch ON TRUE
    WHERE identity.address IS NOT NULL
), current_identity AS (
    SELECT expected.address, expected.code_hash,
           current_code.code_hash AS current_code_hash
    FROM canonical_tip AS tip
    CROSS JOIN expected_identity AS expected
    LEFT JOIN LATERAL (
        SELECT observation.code_hash
        FROM contract_code_observations AS observation
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = observation.chain_id
         AND canonical.number = observation.block_number
         AND canonical.block_hash = observation.block_hash
        WHERE observation.chain_id = $1::numeric
          AND observation.address = expected.address
          AND observation.canonical = TRUE
          AND observation.block_number <= tip.number
        ORDER BY observation.block_number DESC,
                 observation.observed_at DESC,
                 observation.code_hash DESC
        LIMIT 1
    ) AS current_code ON TRUE
 )
, current_binding AS (
    SELECT binding.*, current_proxy.context_number,
           current_proxy.proxy_artifact_job_id,
           current_proxy.implementation_artifact_job_id
    FROM current_proxy
    JOIN verified_proxy_bindings AS binding
      ON binding.chain_id = $1::numeric
     AND binding.proxy_address = $2::bytea
     AND binding.observation_stage_version = 2
     AND binding.observation_block_number = current_proxy.block_number
     AND binding.observation_block_hash = current_proxy.block_hash
     AND binding.observation_generation_id = current_proxy.observation_generation_id
     AND binding.artifact_resolution_id IS NOT DISTINCT FROM
         current_proxy.artifact_resolution_id
     AND binding.beacon_generation_id IS NOT DISTINCT FROM
         current_proxy.beacon_generation_id
     AND binding.uups_generation_id IS NOT DISTINCT FROM
         current_proxy.uups_generation_id
     AND binding.proxy_code_hash = current_proxy.proxy_code_hash
     AND binding.proxy_kind = current_proxy.proxy_kind
     AND binding.proxy_pattern = current_proxy.proxy_pattern
     AND binding.standard_version IS NOT DISTINCT FROM current_proxy.standard_version
     AND binding.implementation_address = current_proxy.implementation_address
     AND binding.implementation_code_hash = current_proxy.implementation_code_hash
     AND binding.admin_address IS NOT DISTINCT FROM current_proxy.admin_address
     AND binding.admin_code_hash IS NOT DISTINCT FROM current_proxy.admin_code_hash
     AND binding.beacon_address IS NOT DISTINCT FROM current_proxy.beacon_address
     AND binding.beacon_code_hash IS NOT DISTINCT FROM current_proxy.beacon_code_hash
     AND binding.management_kind = current_proxy.management_kind
     AND binding.management_address IS NOT DISTINCT FROM
         current_proxy.management_address
     AND binding.management_code_hash IS NOT DISTINCT FROM
         current_proxy.management_code_hash
    JOIN canonical_blocks AS binding_context
      ON binding_context.chain_id = binding.chain_id
     AND binding_context.number = binding.context_block_number
     AND binding_context.block_hash = binding.context_block_hash
    WHERE current_proxy.proxy_code_hash = $3::bytea
      AND (
          (binding.proxy_pattern = 'transparent'
           AND binding.management_kind = 'proxy_admin'
           AND binding.management_address = binding.admin_address
           AND binding.management_code_hash = binding.admin_code_hash)
       OR (binding.proxy_pattern = 'beacon'
           AND binding.management_kind = 'upgradeable_beacon'
           AND binding.management_address = binding.beacon_address
           AND binding.management_code_hash = binding.beacon_code_hash)
       OR (binding.proxy_pattern IN ('clone', 'erc1967', 'uups')
           AND binding.management_kind = 'none'
           AND binding.management_address IS NULL
           AND binding.management_code_hash IS NULL)
      )
      AND proxy_interaction_coverage_contains(
              binding.chain_id,
              binding.observation_block_number,
              binding.observation_block_hash,
              current_proxy.context_number,
              current_proxy.context_hash
          )
      AND NOT EXISTS (
          SELECT 1
          FROM (VALUES
              (binding.proxy_address, binding.proxy_code_hash),
              (binding.implementation_address, binding.implementation_code_hash),
              (binding.admin_address, binding.admin_code_hash),
              (binding.beacon_address, binding.beacon_code_hash),
              (binding.management_address, binding.management_code_hash)
          ) AS identity(address, code_hash)
          JOIN contract_code_observations AS observation
            ON observation.chain_id = binding.chain_id
           AND observation.address = identity.address
           AND observation.canonical = TRUE
          JOIN canonical_blocks AS canonical
            ON canonical.chain_id = observation.chain_id
           AND canonical.number = observation.block_number
           AND canonical.block_hash = observation.block_hash
          WHERE identity.address IS NOT NULL
            AND observation.block_number > binding.context_block_number
            AND observation.block_number <= current_proxy.context_number
            AND observation.code_hash IS DISTINCT FROM identity.code_hash
      )
      AND NOT EXISTS (
          SELECT 1
          FROM expected_identity AS identity
          JOIN transaction_state_changes AS change
            ON change.chain_id = binding.chain_id
           AND change.address = identity.address
           AND change.field_kind = 'code'
           AND change.canonical = TRUE
          JOIN canonical_blocks AS canonical
            ON canonical.chain_id = change.chain_id
           AND canonical.number = change.block_number
           AND canonical.block_hash = change.block_hash
          WHERE change.block_number > binding.context_block_number
            AND change.block_number <= current_proxy.context_number
            AND lower(change.before_value) IS DISTINCT FROM
                lower(change.after_value)
      )
    ORDER BY binding.created_at DESC, binding.verification_job_id DESC
    LIMIT 1
), publication_candidates(address, code_hash, required) AS (
    SELECT binding.proxy_address, binding.proxy_code_hash,
           binding.proxy_pattern <> 'clone'
    FROM current_binding AS binding
    UNION ALL
    SELECT binding.implementation_address, binding.implementation_code_hash, TRUE
    FROM current_binding AS binding
    UNION ALL
    SELECT binding.management_address, binding.management_code_hash,
           binding.management_kind <> 'none'
    FROM current_binding AS binding
), required_publication(address, code_hash, epoch_block) AS (
    SELECT publication.address, publication.code_hash, identity.epoch_block
    FROM publication_candidates AS publication
    JOIN expected_identity AS identity
      ON identity.address = publication.address
     AND identity.code_hash = publication.code_hash
    WHERE publication.required
)
SELECT binding.implementation_address
FROM current_binding AS binding
WHERE NOT EXISTS (
    SELECT 1
    FROM current_identity AS identity
    WHERE identity.current_code_hash IS DISTINCT FROM identity.code_hash
)
  AND NOT EXISTS (
      SELECT 1
      FROM required_publication AS publication
      WHERE NOT EXISTS (
          SELECT 1
          FROM verified_contracts AS verified
          WHERE verified.chain_id = $1::numeric
            AND verified.address = publication.address
            AND verified.code_hash = publication.code_hash
            AND verified.valid_from_block >= publication.epoch_block
            AND verified.valid_from_block <= binding.context_number
            AND (verified.valid_to_block IS NULL
                 OR verified.valid_to_block >= binding.context_number)
      )
  )
  AND (
      binding.management_kind = 'none' OR EXISTS (
          SELECT 1
          FROM verified_contract_proxy_artifacts AS artifact
          JOIN verified_contracts AS verified
            ON verified.chain_id = artifact.chain_id
           AND verified.address = artifact.address
           AND verified.code_hash = artifact.code_hash
           AND verified.valid_from_block = artifact.valid_from_block
           AND verified.verification_job_id = artifact.verification_job_id
           AND verified.request_digest = artifact.request_digest
          JOIN expected_identity AS identity
            ON identity.address = artifact.address
           AND identity.code_hash = artifact.code_hash
          WHERE artifact.chain_id = $1::numeric
            AND artifact.address = binding.management_address
            AND artifact.code_hash = binding.management_code_hash
            AND artifact.standard_version = '5.6.1'
            AND artifact.artifact_kind = CASE binding.management_kind
                WHEN 'proxy_admin' THEN 'proxy_admin'
                WHEN 'upgradeable_beacon' THEN 'upgradeable_beacon'
            END
            AND artifact.valid_from_block >= identity.epoch_block
            AND artifact.valid_from_block <= binding.context_number
            AND (verified.valid_to_block IS NULL
                 OR verified.valid_to_block >= binding.context_number)
      )
  )
  AND (
      binding.proxy_pattern = 'clone' OR EXISTS (
          SELECT 1
          FROM verified_contract_proxy_artifacts AS artifact
          JOIN verified_contracts AS verified
            ON verified.chain_id = artifact.chain_id
           AND verified.address = artifact.address
           AND verified.code_hash = artifact.code_hash
           AND verified.valid_from_block = artifact.valid_from_block
           AND verified.verification_job_id = artifact.verification_job_id
           AND verified.request_digest = artifact.request_digest
          JOIN expected_identity AS identity
            ON identity.address = artifact.address
           AND identity.code_hash = artifact.code_hash
          WHERE artifact.verification_job_id = binding.proxy_artifact_job_id
            AND artifact.chain_id = $1::numeric
            AND artifact.address = binding.proxy_address
            AND artifact.code_hash = binding.proxy_code_hash
            AND artifact.standard_version = '5.6.1'
            AND artifact.artifact_kind = CASE binding.proxy_pattern
                WHEN 'erc1967' THEN 'erc1967_proxy'
                WHEN 'transparent' THEN 'transparent_proxy'
                WHEN 'uups' THEN 'erc1967_proxy'
                WHEN 'beacon' THEN 'beacon_proxy'
            END
            AND artifact.valid_from_block >= identity.epoch_block
            AND artifact.valid_from_block <= binding.context_number
            AND (verified.valid_to_block IS NULL
                 OR verified.valid_to_block >= binding.context_number)
      )
  )
  AND (
      binding.proxy_pattern <> 'uups' OR EXISTS (
          SELECT 1
          FROM verified_contract_proxy_artifacts AS artifact
          JOIN verified_contracts AS verified
            ON verified.chain_id = artifact.chain_id
           AND verified.address = artifact.address
           AND verified.code_hash = artifact.code_hash
           AND verified.valid_from_block = artifact.valid_from_block
           AND verified.verification_job_id = artifact.verification_job_id
           AND verified.request_digest = artifact.request_digest
          JOIN expected_identity AS identity
            ON identity.address = artifact.address
           AND identity.code_hash = artifact.code_hash
          WHERE artifact.verification_job_id =
                binding.implementation_artifact_job_id
            AND artifact.chain_id = $1::numeric
            AND artifact.address = binding.implementation_address
            AND artifact.code_hash = binding.implementation_code_hash
            AND artifact.standard_version = '5.6.1'
            AND artifact.artifact_kind = 'uups_implementation'
            AND artifact.valid_from_block >= identity.epoch_block
            AND artifact.valid_from_block <= binding.context_number
            AND (verified.valid_to_block IS NULL
                 OR verified.valid_to_block >= binding.context_number)
      )
  );
