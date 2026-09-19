-- name: EnrichLegacyAtomicConsumePendingReplay :execrows
UPDATE durable_jobs
SET status = 'queued',
    attempts = 0,
    available_at = clock_timestamp(),
    result = NULL,
    last_error = NULL,
    completed_generation = GREATEST(completed_generation, sqlc.arg('completed_generation')),
    leased_by = NULL,
    lease_token = NULL,
    lease_expires_at = NULL,
    leased_generation = NULL,
    updated_at = clock_timestamp()
WHERE id = sqlc.arg('id')
  AND kind = 'enrichment'
  AND chain_id = sqlc.arg('chain_id')::numeric
  AND stage = sqlc.arg('stage')
  AND stage_version = sqlc.arg('stage_version')
  AND payload->>'block_hash' = sqlc.arg('payload')
  AND payload->>'block_number' = sqlc.arg('payload_2')
  AND status = 'leased'
  AND lease_token = sqlc.arg('lease_token')
  AND lease_expires_at > clock_timestamp()
  AND claimed_generation = sqlc.arg('completed_generation')
  AND leased_generation = sqlc.arg('completed_generation')
  AND requested_generation > sqlc.arg('completed_generation')
  AND completed_generation < sqlc.arg('completed_generation');

-- name: EnrichLegacyAtomicPublishSuccess :execrows
UPDATE durable_jobs
SET status = 'succeeded',
    result = sqlc.arg('result')::jsonb,
    last_error = NULL,
    completed_generation = sqlc.arg('completed_generation'),
    leased_by = NULL,
    lease_token = NULL,
    lease_expires_at = NULL,
    leased_generation = NULL,
    updated_at = clock_timestamp()
WHERE id = sqlc.arg('id')
  AND kind = 'enrichment'
  AND chain_id = sqlc.arg('chain_id')::numeric
  AND stage = sqlc.arg('stage')
  AND stage_version = sqlc.arg('stage_version')
  AND payload->>'block_hash' = sqlc.arg('payload')
  AND payload->>'block_number' = sqlc.arg('payload_2')
  AND status = 'leased'
  AND lease_token = sqlc.arg('lease_token')
  AND lease_expires_at > clock_timestamp()
  AND claimed_generation = sqlc.arg('completed_generation')
  AND leased_generation = sqlc.arg('completed_generation')
  AND requested_generation = sqlc.arg('completed_generation')
  AND completed_generation < sqlc.arg('completed_generation');

-- name: EnrichLegacyBlockStatsSource :one
SELECT
block.raw,
count(inclusion.tx_index),
configuration.configured_start,
parent.number,
parent.timestamp,
(COALESCE(bool_or(canonical_parent.block_hash IS NOT NULL), FALSE))::boolean AS parent_canonical
FROM blocks AS block
JOIN core_index_configuration AS configuration
  ON configuration.chain_id = block.chain_id
LEFT JOIN transaction_inclusions AS inclusion
  ON inclusion.chain_id = block.chain_id
 AND inclusion.block_number = block.number
 AND inclusion.block_hash = block.hash
LEFT JOIN blocks AS parent
  ON parent.chain_id = block.chain_id
 AND parent.hash = block.parent_hash
LEFT JOIN canonical_blocks AS canonical_parent
  ON canonical_parent.chain_id = parent.chain_id
 AND canonical_parent.number = parent.number
 AND canonical_parent.block_hash = parent.hash
WHERE block.chain_id = sqlc.arg('chain_id')::numeric AND block.number = sqlc.arg('number')::numeric AND block.hash = sqlc.arg('hash')
GROUP BY block.raw, configuration.configured_start, parent.number, parent.timestamp;

-- name: EnrichLegacyCanonicalBlock :one
SELECT EXISTS (
    SELECT 1
    FROM canonical_blocks
    WHERE chain_id = sqlc.arg('chain_id')::numeric
      AND number = sqlc.arg('number')::numeric
      AND block_hash = sqlc.arg('block_hash')
);

-- name: EnrichLegacyCarryForwardProxyGeneration :one
WITH source_generation AS MATERIALIZED (
    SELECT publication.job_generation
    FROM durable_stage_publications AS publication
    WHERE publication.job_id = sqlc.narg('durable_job_id')::bigint
      AND publication.job_generation < sqlc.narg('job_generation')::bigint
      AND publication.chain_id = sqlc.arg('chain_id')::numeric
      AND publication.block_number = sqlc.arg('block_number')::numeric
      AND publication.block_hash = sqlc.arg('block_hash')
      AND publication.stage = 'proxy'
      AND publication.stage_version = sqlc.arg('stage_version')
      AND publication.state = 'complete'
    ORDER BY publication.job_generation DESC
    LIMIT 1
), redetected AS MATERIALIZED (
    SELECT generation.proxy_address AS address
    FROM proxy_observation_generations AS generation
    WHERE generation.chain_id = sqlc.arg('chain_id')::numeric
      AND generation.observation_block_hash = sqlc.arg('block_hash')
      AND generation.observation_stage_version = sqlc.arg('stage_version')
      AND generation.durable_job_id = sqlc.narg('durable_job_id')::bigint
      AND generation.job_generation = sqlc.narg('job_generation')::bigint
    UNION
    SELECT generation.beacon_address AS address
    FROM beacon_observation_generations AS generation
    WHERE generation.chain_id = sqlc.arg('chain_id')::numeric
      AND generation.observation_block_hash = sqlc.arg('block_hash')
      AND generation.observation_stage_version = sqlc.arg('stage_version')
      AND generation.durable_job_id = sqlc.narg('durable_job_id')::bigint
      AND generation.job_generation = sqlc.narg('job_generation')::bigint
    UNION
	SELECT generation.implementation_address AS address
	FROM uups_implementation_observation_generations AS generation
	WHERE generation.chain_id = sqlc.arg('chain_id')::numeric
	  AND generation.observation_block_hash = sqlc.arg('block_hash')
	  AND generation.observation_stage_version = sqlc.arg('stage_version')
	  AND generation.durable_job_id = sqlc.narg('durable_job_id')::bigint
	  AND generation.job_generation = sqlc.narg('job_generation')::bigint
	UNION
    SELECT evidence.address
    FROM proxy_detection_evidence AS evidence
    WHERE evidence.chain_id = sqlc.arg('chain_id')::numeric
      AND evidence.block_number = sqlc.arg('block_number')::numeric
      AND evidence.block_hash = sqlc.arg('block_hash')
      AND evidence.stage_version = sqlc.arg('stage_version')
      AND evidence.durable_job_id = sqlc.narg('durable_job_id')::bigint
      AND evidence.job_generation = sqlc.narg('job_generation')::bigint
), carried_proxies AS (
    INSERT INTO proxy_observation_generations (
        chain_id, proxy_address, observation_block_hash,
        observation_stage_version, durable_job_id, job_generation
    )
    SELECT source.chain_id, source.proxy_address, source.observation_block_hash,
           source.observation_stage_version, sqlc.narg('durable_job_id')::bigint, sqlc.narg('job_generation')::bigint
    FROM proxy_observation_generations AS source
    JOIN source_generation
      ON source.job_generation = source_generation.job_generation
    WHERE source.chain_id = sqlc.arg('chain_id')::numeric
      AND source.observation_block_hash = sqlc.arg('block_hash')
      AND source.observation_stage_version = sqlc.arg('stage_version')
      AND source.durable_job_id = sqlc.narg('durable_job_id')::bigint
      AND NOT EXISTS (
          SELECT 1 FROM redetected WHERE redetected.address = source.proxy_address
      )
    ON CONFLICT DO NOTHING
    RETURNING 1
), carried_beacons AS (
    INSERT INTO beacon_observation_generations (
        chain_id, beacon_address, observation_block_hash,
        observation_stage_version, durable_job_id, job_generation
    )
    SELECT source.chain_id, source.beacon_address, source.observation_block_hash,
           source.observation_stage_version, sqlc.narg('durable_job_id')::bigint, sqlc.narg('job_generation')::bigint
    FROM beacon_observation_generations AS source
    JOIN source_generation
      ON source.job_generation = source_generation.job_generation
    WHERE source.chain_id = sqlc.arg('chain_id')::numeric
      AND source.observation_block_hash = sqlc.arg('block_hash')
      AND source.observation_stage_version = sqlc.arg('stage_version')
      AND source.durable_job_id = sqlc.narg('durable_job_id')::bigint
      AND NOT EXISTS (
          SELECT 1 FROM redetected WHERE redetected.address = source.beacon_address
      )
    ON CONFLICT DO NOTHING
    RETURNING 1
), carried_uups AS (
	INSERT INTO uups_implementation_observation_generations (
		chain_id, implementation_address, observation_block_hash,
		observation_stage_version, verification_job_id,
		durable_job_id, job_generation
	)
	SELECT source.chain_id, source.implementation_address,
		   source.observation_block_hash, source.observation_stage_version,
		   source.verification_job_id, sqlc.narg('durable_job_id')::bigint, sqlc.narg('job_generation')::bigint
	FROM uups_implementation_observation_generations AS source
	JOIN source_generation
	  ON source.job_generation = source_generation.job_generation
	WHERE source.chain_id = sqlc.arg('chain_id')::numeric
	  AND source.observation_block_hash = sqlc.arg('block_hash')
	  AND source.observation_stage_version = sqlc.arg('stage_version')
	  AND source.durable_job_id = sqlc.narg('durable_job_id')::bigint
	  AND NOT EXISTS (
		  SELECT 1 FROM redetected
		  WHERE redetected.address = source.implementation_address
	  )
	ON CONFLICT DO NOTHING
	RETURNING 1
), carried_resolutions AS (
    INSERT INTO proxy_artifact_resolutions (
        chain_id, proxy_address, observation_block_hash,
        observation_stage_version, proxy_code_hash, proxy_kind,
        proxy_pattern, standard_version, implementation_address,
        implementation_code_hash, admin_address, admin_code_hash,
        beacon_address, beacon_code_hash, proxy_artifact_job_id,
        implementation_artifact_job_id, durable_job_id, job_generation,
        evidence
    )
    SELECT source.chain_id, source.proxy_address, source.observation_block_hash,
           source.observation_stage_version, source.proxy_code_hash,
           source.proxy_kind, source.proxy_pattern, source.standard_version,
           source.implementation_address, source.implementation_code_hash,
           source.admin_address, source.admin_code_hash,
           source.beacon_address, source.beacon_code_hash,
           source.proxy_artifact_job_id, source.implementation_artifact_job_id,
           sqlc.narg('durable_job_id')::bigint, sqlc.narg('job_generation')::bigint, source.evidence
    FROM proxy_artifact_resolutions AS source
    JOIN source_generation
      ON source.job_generation = source_generation.job_generation
    WHERE source.chain_id = sqlc.arg('chain_id')::numeric
      AND source.observation_block_hash = sqlc.arg('block_hash')
      AND source.observation_stage_version = sqlc.arg('stage_version')
      AND source.durable_job_id = sqlc.narg('durable_job_id')::bigint
      AND NOT EXISTS (
          SELECT 1 FROM redetected WHERE redetected.address = source.proxy_address
      )
    ON CONFLICT DO NOTHING
    RETURNING 1
), carried_negative_evidence AS (
    INSERT INTO proxy_detection_evidence (
        chain_id, address, block_number, block_hash, stage_version, code_hash,
        candidate_kind, detection_state, reason, canonical,
        durable_job_id, job_generation, details
    )
    SELECT source.chain_id, source.address, source.block_number,
           source.block_hash, source.stage_version, source.code_hash,
           source.candidate_kind, source.detection_state, source.reason, TRUE,
           sqlc.narg('durable_job_id')::bigint, sqlc.narg('job_generation')::bigint, source.details
    FROM proxy_detection_evidence AS source
    JOIN source_generation
      ON source.job_generation = source_generation.job_generation
    WHERE source.chain_id = sqlc.arg('chain_id')::numeric
      AND source.block_number = sqlc.arg('block_number')::numeric
      AND source.block_hash = sqlc.arg('block_hash')
      AND source.stage_version = sqlc.arg('stage_version')
      AND source.durable_job_id = sqlc.narg('durable_job_id')::bigint
      AND NOT EXISTS (
          SELECT 1 FROM redetected WHERE redetected.address = source.address
      )
    ON CONFLICT DO NOTHING
    RETURNING 1
)
SELECT (SELECT count(*) FROM carried_proxies),
       (SELECT count(*) FROM carried_beacons),
	   (SELECT count(*) FROM carried_uups),
       (SELECT count(*) FROM carried_resolutions),
       (SELECT count(*) FROM carried_negative_evidence);

-- name: EnrichLegacyClaimOutbox :one
SELECT id, chain_id::text, topic, message_key, payload, attempts, generation
FROM transactional_outbox
WHERE published_at IS NULL
  AND available_at <= clock_timestamp()
  AND topic IN ('core.block.canonical', 'core.block.orphaned')
ORDER BY available_at, id
FOR UPDATE SKIP LOCKED
LIMIT 1;

-- name: EnrichLegacyConfirmPublishedSuccess :one
SELECT EXISTS (
    SELECT 1
    FROM durable_stage_publications AS publication
    WHERE publication.job_id = $1
      AND publication.job_generation = $2
      AND publication.state = 'complete'
);

-- name: EnrichLegacyConfirmSupersededPublication :one
SELECT EXISTS (
    SELECT 1
    FROM durable_stage_publications AS publication
    WHERE publication.job_id = $1
      AND publication.job_generation = $2
      AND publication.state = 'superseded'
);

-- name: EnrichLegacyDeleteEIP7702AuthorizationsBlock :exec
DELETE FROM eip7702_authorizations
WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash');

-- name: EnrichLegacyDeleteExecutionCodeResolutionsBlock :exec
DELETE FROM transaction_execution_code_resolutions
WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash');

-- name: EnrichLegacyDeleteStageJournal :exec
DELETE FROM block_journals
WHERE chain_id = sqlc.arg('chain_id')::numeric
  AND block_hash = sqlc.arg('block_hash')
  AND stage = sqlc.arg('stage');

-- name: EnrichLegacyDeleteStageResult :exec
DELETE FROM block_stage_results
WHERE chain_id = sqlc.arg('chain_id')::numeric
  AND block_hash = sqlc.arg('block_hash')
  AND stage = sqlc.arg('stage')
  AND stage_version = sqlc.arg('stage_version');

-- name: EnrichLegacyDeleteStateDiffBlock :exec
DELETE FROM transaction_state_changes
WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash');

-- name: EnrichLegacyDeleteTraceBlock :exec
DELETE FROM normalized_traces
WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash');

-- name: EnrichLegacyDeleteTraceLogAttributions :exec
DELETE FROM trace_log_attributions
WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash');

-- name: EnrichLegacyDetectedToken :one
SELECT token.standard, token.confidence
FROM token_contracts AS token
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = token.chain_id
 AND canonical.number = token.observed_block_number
 AND canonical.block_hash = token.observed_block_hash
WHERE token.chain_id = sqlc.arg('chain_id')::numeric
  AND token.address = sqlc.arg('address')
  AND token.observed_block_number <= sqlc.arg('max_observed_block_number')::numeric
  AND token.standard <> 'unknown'
ORDER BY token.observed_block_number DESC, token.updated_at DESC
LIMIT 1;

-- name: EnrichLegacyEnablePublicationProtocol :exec
SELECT set_config('etherview.enrichment_publication_protocol', '2', true);

-- name: EnrichLegacyEnqueueJob :one
INSERT INTO durable_jobs (
    chain_id, kind, stage, stage_version, idempotency_key, payload,
    priority, max_attempts
) VALUES (sqlc.arg('chain_id')::numeric, sqlc.arg('kind'), sqlc.arg('stage'), sqlc.arg('stage_version'), sqlc.arg('idempotency_key'), sqlc.arg('payload')::jsonb, sqlc.arg('priority'), sqlc.arg('max_attempts'))
ON CONFLICT (chain_id, kind, idempotency_key) DO NOTHING
RETURNING id, chain_id::text, stage, stage_version, attempts, max_attempts, payload, requested_generation;

-- name: EnrichLegacyEnrichmentJobStatus :one
SELECT status
FROM durable_jobs
WHERE id = $1;

-- name: EnrichLegacyFinishJob :one
UPDATE durable_jobs
SET status = CASE
        WHEN requested_generation > leased_generation THEN 'queued'
        ELSE sqlc.arg('status')
    END,
    attempts = CASE
        WHEN requested_generation > leased_generation THEN 0
        ELSE attempts
    END,
    available_at = CASE
        WHEN requested_generation > leased_generation THEN clock_timestamp()
        ELSE available_at
    END,
    result = CASE
        WHEN requested_generation > leased_generation THEN NULL
        ELSE sqlc.arg('result')::jsonb
    END,
    last_error = CASE
        WHEN requested_generation > leased_generation THEN NULL
        ELSE sqlc.narg('last_error')
    END,
    completed_generation = GREATEST(completed_generation, leased_generation),
    leased_by = NULL,
    lease_token = NULL,
    lease_expires_at = NULL,
    leased_generation = NULL,
    updated_at = clock_timestamp()
WHERE id = sqlc.arg('id')
  AND kind = 'enrichment'
  AND chain_id = sqlc.arg('chain_id')::numeric
  AND stage = sqlc.arg('stage')
  AND stage_version = sqlc.arg('stage_version')
  AND payload->>'block_hash' = sqlc.arg('payload')
  AND payload->>'block_number' = sqlc.arg('payload2')
  AND status = 'leased'
  AND lease_token = sqlc.arg('lease_token')
  AND lease_expires_at > clock_timestamp()
  AND claimed_generation = sqlc.arg('claimed_generation')
  AND leased_generation = sqlc.arg('claimed_generation')
  AND completed_generation < sqlc.arg('claimed_generation')
RETURNING status = 'queued'
      AND attempts = 0
      AND completed_generation < requested_generation AS followup_queued;

-- name: EnrichLegacyInsertBeaconObservationGeneration :execrows
INSERT INTO beacon_observation_generations (
    chain_id, beacon_address, observation_block_hash,
    observation_stage_version, durable_job_id, job_generation
) VALUES (sqlc.arg('chain_id')::numeric, sqlc.arg('beacon_address'), sqlc.arg('observation_block_hash'), sqlc.arg('observation_stage_version'), sqlc.narg('durable_job_id')::bigint, sqlc.narg('job_generation')::bigint)
ON CONFLICT DO NOTHING;

-- name: EnrichLegacyInsertBlockStats :exec
INSERT INTO block_statistics (
    chain_id, block_number, block_hash, transaction_count, gas_used, gas_limit,
    base_fee_per_gas, blob_gas_used, burned_wei, block_timestamp,
    block_interval_seconds, transactions_per_second, excess_blob_gas,
    blob_base_fee_per_gas, blob_burned_wei, execution_gas_fee_wei,
    priority_fee_wei, failed_transaction_count, contract_creation_count,
    canonical
) VALUES (
    sqlc.arg('chain_id')::numeric, sqlc.arg('block_number')::numeric, sqlc.arg('block_hash'), sqlc.arg('transaction_count'), sqlc.arg('gas_used')::numeric, sqlc.arg('gas_limit')::numeric, sqlc.narg('base_fee_per_gas')::numeric,
    sqlc.narg('blob_gas_used')::numeric, sqlc.narg('burned_wei')::numeric, sqlc.arg('block_timestamp')::numeric, sqlc.narg('block_interval_seconds')::numeric, sqlc.narg('transactions_per_second')::numeric,
    sqlc.narg('excess_blob_gas')::numeric, sqlc.narg('blob_base_fee_per_gas')::numeric, sqlc.narg('blob_burned_wei')::numeric, sqlc.arg('execution_gas_fee_wei')::numeric, sqlc.arg('priority_fee_wei')::numeric,
    sqlc.arg('failed_transaction_count'), sqlc.arg('contract_creation_count'), true
)
ON CONFLICT (chain_id, block_number, block_hash) DO UPDATE SET
    transaction_count = EXCLUDED.transaction_count,
    gas_used = EXCLUDED.gas_used,
    gas_limit = EXCLUDED.gas_limit,
    base_fee_per_gas = EXCLUDED.base_fee_per_gas,
    blob_gas_used = EXCLUDED.blob_gas_used,
    burned_wei = EXCLUDED.burned_wei,
    block_timestamp = EXCLUDED.block_timestamp,
    block_interval_seconds = EXCLUDED.block_interval_seconds,
    transactions_per_second = EXCLUDED.transactions_per_second,
    excess_blob_gas = EXCLUDED.excess_blob_gas,
    blob_base_fee_per_gas = EXCLUDED.blob_base_fee_per_gas,
    blob_burned_wei = EXCLUDED.blob_burned_wei,
    execution_gas_fee_wei = EXCLUDED.execution_gas_fee_wei,
    priority_fee_wei = EXCLUDED.priority_fee_wei,
    failed_transaction_count = EXCLUDED.failed_transaction_count,
    contract_creation_count = EXCLUDED.contract_creation_count,
    canonical = true,
    computed_at = now();

-- name: EnrichLegacyInsertDurablePublication :one
INSERT INTO durable_stage_publications (
    job_id, job_generation, chain_id, block_number, block_hash,
    stage, stage_version, state, details, last_error
) VALUES (
    sqlc.arg('job_id'), sqlc.arg('job_generation'), sqlc.arg('chain_id')::numeric, sqlc.arg('block_number')::numeric, sqlc.arg('block_hash'),
    sqlc.arg('stage'), sqlc.arg('stage_version'), sqlc.arg('state'), sqlc.arg('details')::jsonb, sqlc.narg('last_error')
)
RETURNING 1 AS inserted;

-- name: EnrichLegacyInsertEIP7702Authorization :exec
INSERT INTO eip7702_authorizations (
    chain_id, block_number, block_hash, transaction_hash, transaction_index,
    authorization_index, authorization_chain_id, authorization_nonce,
    delegate_address, y_parity, r, s, authority, signature_status,
    application_status, skip_reason, canonical
) VALUES (
    sqlc.arg('chain_id')::numeric, sqlc.arg('block_number')::numeric, sqlc.arg('block_hash'), sqlc.arg('transaction_hash'), sqlc.arg('transaction_index'), sqlc.arg('authorization_index'), sqlc.arg('authorization_chain_id')::numeric, sqlc.arg('authorization_nonce')::numeric,
    sqlc.arg('delegate_address'), sqlc.arg('yparity'), sqlc.arg('r'), sqlc.arg('s'), sqlc.arg('authority'), sqlc.arg('signature_status'), sqlc.arg('application_status'), sqlc.narg('skip_reason'), true
);

-- name: EnrichLegacyInsertExecutionCodeResolution :exec
INSERT INTO transaction_execution_code_resolutions (
    chain_id, block_number, block_hash, transaction_hash, transaction_index,
    context_address, execution_address, execution_code_hash, resolution,
    evidence_source, canonical
) VALUES (
    sqlc.arg('chain_id')::numeric, sqlc.arg('block_number')::numeric, sqlc.arg('block_hash'), sqlc.arg('transaction_hash'), sqlc.arg('transaction_index'), sqlc.arg('context_address'), sqlc.arg('execution_address'), sqlc.arg('execution_code_hash'), sqlc.arg('resolution'), sqlc.arg('evidence_source'), true
);

-- name: EnrichLegacyInsertProxyArtifactResolution :one
WITH inserted AS (
    INSERT INTO proxy_artifact_resolutions (
        chain_id, proxy_address, observation_block_hash,
        observation_stage_version, proxy_code_hash, proxy_kind,
        proxy_pattern, standard_version, implementation_address,
        implementation_code_hash, admin_address, admin_code_hash,
        beacon_address, beacon_code_hash, proxy_artifact_job_id,
        implementation_artifact_job_id, durable_job_id, job_generation,
        evidence
    ) VALUES (
        sqlc.arg('chain_id')::numeric, sqlc.arg('proxy_address'), sqlc.arg('observation_block_hash'), sqlc.arg('observation_stage_version'), sqlc.arg('proxy_code_hash'), sqlc.arg('proxy_kind'),
        sqlc.arg('proxy_pattern'), sqlc.arg('standard_version'), sqlc.arg('implementation_address'), sqlc.arg('implementation_code_hash'), sqlc.arg('admin_address'), sqlc.arg('admin_code_hash'),
        sqlc.arg('beacon_address'), sqlc.arg('beacon_code_hash'), sqlc.arg('proxy_artifact_job_id')::uuid, sqlc.narg('implementation_artifact_job_id')::uuid, sqlc.narg('durable_job_id')::bigint, sqlc.narg('job_generation')::bigint,
        sqlc.arg('evidence')::jsonb
    )
    ON CONFLICT DO NOTHING
    RETURNING id
)
SELECT id FROM inserted
UNION ALL
SELECT existing.id
FROM proxy_artifact_resolutions AS existing
WHERE existing.chain_id = sqlc.arg('chain_id')::numeric
  AND existing.proxy_address = sqlc.arg('proxy_address')
  AND existing.observation_block_hash = sqlc.arg('observation_block_hash')
  AND existing.observation_stage_version = sqlc.arg('observation_stage_version')
  AND existing.durable_job_id IS NOT DISTINCT FROM sqlc.narg('durable_job_id')::bigint
  AND existing.job_generation IS NOT DISTINCT FROM sqlc.narg('job_generation')::bigint
  AND existing.proxy_code_hash = sqlc.arg('proxy_code_hash')
  AND existing.proxy_kind = sqlc.arg('proxy_kind')
  AND existing.proxy_pattern = sqlc.arg('proxy_pattern')
  AND existing.standard_version = sqlc.arg('standard_version')
  AND existing.implementation_address = sqlc.arg('implementation_address')
  AND existing.implementation_code_hash = sqlc.arg('implementation_code_hash')
  AND existing.admin_address IS NOT DISTINCT FROM sqlc.arg('admin_address')::bytea
  AND existing.admin_code_hash IS NOT DISTINCT FROM sqlc.arg('admin_code_hash')::bytea
  AND existing.beacon_address IS NOT DISTINCT FROM sqlc.arg('beacon_address')::bytea
  AND existing.beacon_code_hash IS NOT DISTINCT FROM sqlc.arg('beacon_code_hash')::bytea
  AND existing.proxy_artifact_job_id = sqlc.arg('proxy_artifact_job_id')::uuid
  AND existing.implementation_artifact_job_id IS NOT DISTINCT FROM sqlc.narg('implementation_artifact_job_id')::uuid
  AND existing.evidence = sqlc.arg('evidence')::jsonb
LIMIT 1;

-- name: EnrichLegacyInsertProxyObservationGeneration :execrows
INSERT INTO proxy_observation_generations (
    chain_id, proxy_address, observation_block_hash,
    observation_stage_version, durable_job_id, job_generation
) VALUES (sqlc.arg('chain_id')::numeric, sqlc.arg('proxy_address'), sqlc.arg('observation_block_hash'), sqlc.arg('observation_stage_version'), sqlc.narg('durable_job_id')::bigint, sqlc.narg('job_generation')::bigint)
ON CONFLICT DO NOTHING;

-- name: EnrichLegacyInsertPublishedStageResult :one
INSERT INTO block_stage_results AS current (
    chain_id, block_number, block_hash, stage, stage_version,
    state, details, last_error, durable_job_id, job_generation
) VALUES (
    sqlc.arg('chain_id')::numeric, sqlc.arg('block_number')::numeric, sqlc.arg('block_hash'), sqlc.arg('stage'), sqlc.arg('stage_version'),
    sqlc.arg('state'), sqlc.arg('details')::jsonb, sqlc.narg('last_error'), sqlc.arg('durable_job_id'), sqlc.arg('job_generation')
)
ON CONFLICT (chain_id, block_hash, stage, stage_version) DO UPDATE SET
    block_number = EXCLUDED.block_number,
    state = EXCLUDED.state,
    details = EXCLUDED.details,
    last_error = EXCLUDED.last_error,
    durable_job_id = EXCLUDED.durable_job_id,
    job_generation = EXCLUDED.job_generation,
    completed_at = clock_timestamp()
WHERE (
        current.durable_job_id IS NULL
        AND current.job_generation IS NULL
      ) OR (
        current.durable_job_id = EXCLUDED.durable_job_id
        AND current.job_generation <= EXCLUDED.job_generation
      )
RETURNING 1 AS inserted;

-- name: EnrichLegacyInsertReplayRequest :execrows
INSERT INTO durable_job_replay_requests (
    job_id, source_kind, source_key, requested_generation
) VALUES ($1, $2, $3, $4)
ON CONFLICT (job_id, source_kind, source_key) DO NOTHING;

-- name: EnrichLegacyInsertStageResult :execrows
INSERT INTO block_stage_results AS current (
    chain_id, block_number, block_hash, stage, stage_version, state, details, last_error
) VALUES (sqlc.arg('chain_id')::numeric, sqlc.arg('block_number')::numeric, sqlc.arg('block_hash'), sqlc.arg('stage'), sqlc.arg('stage_version'), sqlc.arg('state'), sqlc.arg('details')::jsonb, sqlc.narg('last_error'))
ON CONFLICT (chain_id, block_hash, stage, stage_version) DO UPDATE SET
    block_number = EXCLUDED.block_number,
    state = EXCLUDED.state,
    details = EXCLUDED.details,
    last_error = EXCLUDED.last_error,
    completed_at = now()
WHERE current.durable_job_id IS NULL
  AND current.job_generation IS NULL;

-- name: EnrichLegacyInsertStateChange :exec
INSERT INTO transaction_state_changes (
    chain_id, block_number, block_hash, transaction_hash, transaction_index,
    address, field_kind, storage_key, before_value, after_value, canonical
) VALUES (
    sqlc.arg('chain_id')::numeric, sqlc.arg('block_number')::numeric, sqlc.arg('block_hash'), sqlc.arg('transaction_hash'), sqlc.arg('transaction_index'), sqlc.arg('address'), sqlc.arg('field_kind'), sqlc.arg('storage_key'), sqlc.narg('before_value'), sqlc.narg('after_value'), true
);

-- name: EnrichLegacyInsertTokenDelta :exec
INSERT INTO token_balance_deltas (
    chain_id, block_number, block_hash, log_index, sub_index,
    token_address, owner_address, token_id, delta, canonical
) VALUES (sqlc.arg('chain_id')::numeric, sqlc.arg('block_number')::numeric, sqlc.arg('block_hash'), sqlc.arg('log_index'), sqlc.arg('sub_index'), sqlc.arg('token_address'), sqlc.arg('owner_address'), sqlc.narg('token_id')::numeric, sqlc.arg('delta')::numeric, true)
ON CONFLICT (
    chain_id, block_number, block_hash, log_index, sub_index, token_address, owner_address
) DO UPDATE SET token_id = EXCLUDED.token_id, delta = EXCLUDED.delta, canonical = true;

-- name: EnrichLegacyInsertTokenEvent :exec
INSERT INTO token_events (
    chain_id, block_number, block_hash, log_index, sub_index, transaction_hash,
    token_address, standard, event_kind, operator, from_address, to_address,
    token_id, amount, canonical, confidence, raw
) VALUES (
    sqlc.arg('chain_id')::numeric, sqlc.arg('block_number')::numeric, sqlc.arg('block_hash'), sqlc.arg('log_index'), sqlc.arg('sub_index'), sqlc.arg('transaction_hash'), sqlc.arg('token_address'), sqlc.arg('standard'), sqlc.arg('event_kind'), sqlc.arg('operator'), sqlc.arg('from_address'), sqlc.arg('to_address'),
    sqlc.narg('token_id')::numeric, sqlc.narg('amount')::numeric, true, sqlc.arg('confidence'), sqlc.arg('raw')::jsonb
)
ON CONFLICT (chain_id, block_number, block_hash, log_index, sub_index) DO UPDATE SET
    transaction_hash = EXCLUDED.transaction_hash,
    token_address = EXCLUDED.token_address,
    standard = EXCLUDED.standard,
    event_kind = EXCLUDED.event_kind,
    operator = EXCLUDED.operator,
    from_address = EXCLUDED.from_address,
    to_address = EXCLUDED.to_address,
    token_id = EXCLUDED.token_id,
    amount = EXCLUDED.amount,
    canonical = true,
    confidence = EXCLUDED.confidence,
    raw = EXCLUDED.raw;

-- name: EnrichLegacyInsertTraceFrame :exec
INSERT INTO normalized_traces (
    chain_id, block_number, block_hash, transaction_hash, transaction_index,
    trace_path, parent_path, depth, call_type, from_address, to_address,
    created_address, value, gas, gas_used, input, output, error,
    direct_reverted, reverted, execution_address, execution_code_hash,
    execution_resolution, canonical
) VALUES (
    sqlc.arg('chain_id')::numeric, sqlc.arg('block_number')::numeric, sqlc.arg('block_hash'), sqlc.arg('transaction_hash'), sqlc.arg('transaction_index'), sqlc.arg('trace_path'), sqlc.narg('parent_path'), sqlc.arg('depth'), sqlc.arg('call_type'), sqlc.arg('from_address'), sqlc.arg('to_address'),
    sqlc.arg('created_address'), sqlc.narg('value')::numeric, sqlc.narg('gas')::numeric, sqlc.narg('gas_used')::numeric, sqlc.arg('input'), sqlc.arg('output'), sqlc.narg('error'), sqlc.arg('direct_reverted'), sqlc.arg('reverted'),
    sqlc.arg('execution_address'), sqlc.arg('execution_code_hash'), sqlc.arg('execution_resolution'), true
);

-- name: EnrichLegacyInsertTraceLogAttribution :exec
INSERT INTO trace_log_attributions (
    chain_id, block_number, block_hash, transaction_hash, log_index,
    trace_path, call_type, execution_address, canonical
) VALUES (
    sqlc.arg('chain_id')::numeric, sqlc.arg('block_number')::numeric, sqlc.arg('block_hash'), sqlc.arg('transaction_hash'), sqlc.arg('log_index'), sqlc.arg('trace_path'), sqlc.arg('call_type'), sqlc.arg('execution_address'), TRUE
);

-- name: EnrichLegacyInsertUUPSImplementationObservationGeneration :execrows
INSERT INTO uups_implementation_observation_generations (
    chain_id, implementation_address, observation_block_hash,
    observation_stage_version, verification_job_id,
    durable_job_id, job_generation
) VALUES (
    sqlc.arg('chain_id')::numeric, sqlc.arg('implementation_address'), sqlc.arg('observation_block_hash'), sqlc.arg('observation_stage_version'), sqlc.arg('verification_job_id')::uuid, sqlc.arg('durable_job_id')::bigint, sqlc.arg('job_generation')::bigint
)
ON CONFLICT DO NOTHING;

-- name: EnrichLegacyLockCanonicalBlock :one
SELECT 1 AS locked
FROM canonical_blocks
WHERE chain_id = sqlc.arg('chain_id')::numeric AND number = sqlc.arg('number')::numeric AND block_hash = sqlc.arg('block_hash')
FOR KEY SHARE;

-- name: EnrichLegacyLockPublicationJob :exec
SELECT pg_advisory_xact_lock(-(sqlc.arg('job_id')::bigint));

-- name: EnrichLegacyOrphanJournals :one
SELECT NOT EXISTS (
    SELECT 1
    FROM block_journals
    WHERE chain_id = sqlc.arg('chain_id')::numeric
      AND block_hash = sqlc.arg('block_hash')
      AND canonical
);

-- name: EnrichLegacyProxyCanonical :one
SELECT EXISTS (
    SELECT 1 FROM canonical_blocks
    WHERE chain_id = sqlc.arg('chain_id')::numeric AND number = sqlc.arg('number')::numeric AND block_hash = sqlc.arg('block_hash')
);

-- name: EnrichLegacyProxyReplayCandidates :many
SELECT target.address, target.target_kind, sqlc.arg('source')::text AS source,
		       verified.code_hash, verified.verification_job_id
		FROM proxy_replay_targets AS target
		JOIN durable_job_replay_requests AS replay_request
		  ON replay_request.job_id = sqlc.arg('job_id')::bigint
		 AND replay_request.source_kind = 'verification-publication'
		 AND target.source_verification_job_id::text = replay_request.source_key
		JOIN durable_jobs AS replay_job
		  ON replay_job.id = replay_request.job_id
		 AND replay_job.chain_id = target.chain_id
		 AND replay_job.kind = 'enrichment'
		 AND replay_job.stage = 'proxy'
		 AND replay_job.stage_version = sqlc.arg('stage_version')
		 AND replay_job.payload->>'block_hash' = '0x' || encode(target.block_hash, 'hex')
		 AND replay_job.payload->>'block_number' = target.block_number::text
		 AND replay_job.status = 'leased'
		 AND replay_job.claimed_generation = sqlc.arg('claimed_generation')::bigint
		 AND replay_job.leased_generation = sqlc.arg('claimed_generation')::bigint
		LEFT JOIN verified_contract_proxy_artifacts AS artifact
		  ON target.target_kind = 'uups'
		 AND artifact.verification_job_id = target.source_verification_job_id
		 AND artifact.chain_id = target.chain_id
		 AND artifact.address = target.address
		 AND artifact.artifact_kind = 'uups_implementation'
		 AND artifact.standard_version = '5.6.1'
		 AND artifact.runtime_immutable_address = target.address
		 AND artifact.valid_from_block <= target.block_number
		LEFT JOIN verified_contracts AS verified
		  ON verified.chain_id = artifact.chain_id
		 AND verified.address = artifact.address
		 AND verified.code_hash = artifact.code_hash
		 AND verified.valid_from_block = artifact.valid_from_block
		 AND verified.verification_job_id = artifact.verification_job_id
		 AND verified.request_digest = artifact.request_digest
		 AND (verified.valid_to_block IS NULL OR
		      verified.valid_to_block >= target.block_number)
		WHERE target.chain_id = sqlc.arg('chain_id')::numeric
		  AND target.block_number = sqlc.arg('block_number')::numeric
		  AND target.block_hash = sqlc.arg('block_hash')
		  AND target.source_kind = 'verification_publication'
		  AND replay_request.requested_generation > replay_job.completed_generation
		  AND replay_request.requested_generation <= sqlc.arg('claimed_generation')::bigint
		ORDER BY target.address, target.target_kind, source,
		         verified.verification_job_id;

-- name: EnrichLegacyPublishOutbox :execrows
UPDATE transactional_outbox
SET published_at = clock_timestamp(),
    last_error = NULL,
    payload = jsonb_set(payload, '{_etherview_dispatch}', sqlc.arg('dispatch')::jsonb, true)
WHERE id = sqlc.arg('i_d') AND published_at IS NULL;

-- name: EnrichLegacyRenewJob :execrows
UPDATE durable_jobs
SET lease_expires_at = clock_timestamp() + (sqlc.arg('lease_microseconds')::bigint * INTERVAL '1 microsecond'),
    updated_at = clock_timestamp()
WHERE id = sqlc.arg('id')
  AND kind = 'enrichment'
  AND chain_id = sqlc.arg('chain_id')::numeric
  AND stage = sqlc.arg('stage')
  AND stage_version = sqlc.arg('stage_version')
  AND payload->>'block_hash' = sqlc.arg('payload')
  AND payload->>'block_number' = sqlc.arg('payload_2')
  AND status = 'leased'
  AND lease_token = sqlc.arg('lease_token')
  AND lease_expires_at > clock_timestamp()
  AND claimed_generation = sqlc.arg('claimed_generation')
  AND leased_generation = sqlc.arg('claimed_generation');

-- name: EnrichLegacyRequestReplayJob :execrows
UPDATE durable_jobs
SET requested_generation = $2,
    status = CASE WHEN status = 'leased' THEN status ELSE 'queued' END,
    attempts = CASE WHEN status = 'leased' THEN attempts ELSE 0 END,
    available_at = CASE WHEN status = 'leased' THEN available_at ELSE clock_timestamp() END,
    leased_by = CASE WHEN status = 'leased' THEN leased_by ELSE NULL END,
    lease_token = CASE WHEN status = 'leased' THEN lease_token ELSE NULL END,
    lease_expires_at = CASE WHEN status = 'leased' THEN lease_expires_at ELSE NULL END,
    leased_generation = CASE WHEN status = 'leased' THEN leased_generation ELSE NULL END,
    result = CASE WHEN status = 'leased' THEN result ELSE NULL END,
    last_error = CASE WHEN status = 'leased' THEN last_error ELSE NULL END,
    updated_at = clock_timestamp()
WHERE id = $1
  AND requested_generation = $2 - 1;

-- name: EnrichLegacyRequeueJob :execrows
UPDATE durable_jobs
SET status = 'queued',
    attempts = 0,
    requested_generation = requested_generation + 1,
    available_at = clock_timestamp(),
    leased_by = NULL,
    lease_token = NULL,
    lease_expires_at = NULL,
    leased_generation = NULL,
    result = NULL,
    last_error = NULL,
    updated_at = clock_timestamp()
WHERE id = sqlc.arg('id')
  AND chain_id = sqlc.arg('chain_id')::numeric
  AND kind = 'enrichment'
  AND stage = sqlc.arg('stage')
  AND stage_version = sqlc.arg('stage_version')
  AND idempotency_key = sqlc.arg('idempotency_key')
  AND status IN ('succeeded', 'failed');

-- name: EnrichLegacyRetryJob :one
UPDATE durable_jobs
SET status = CASE
        WHEN requested_generation > leased_generation THEN 'queued'
        WHEN attempts >= max_attempts THEN 'failed'
        ELSE 'queued'
    END,
    attempts = CASE
        WHEN requested_generation > leased_generation THEN 0
        ELSE attempts
    END,
    available_at = CASE
        WHEN requested_generation > leased_generation THEN clock_timestamp()
        ELSE clock_timestamp() + (sqlc.arg('retry_microseconds')::bigint * INTERVAL '1 microsecond')
    END,
    last_error = CASE
        WHEN requested_generation > leased_generation THEN NULL
        ELSE sqlc.arg('last_error')
    END,
    result = CASE
        WHEN requested_generation > leased_generation THEN NULL
        WHEN attempts >= max_attempts
            THEN jsonb_build_object('state', 'failed', 'error', sqlc.arg('last_error')::text)
        ELSE NULL
    END,
    completed_generation = CASE
        WHEN requested_generation > leased_generation
            THEN GREATEST(completed_generation, leased_generation)
        WHEN attempts >= max_attempts
            THEN GREATEST(completed_generation, leased_generation)
        ELSE completed_generation
    END,
    leased_by = NULL,
    lease_token = NULL,
    lease_expires_at = NULL,
    leased_generation = NULL,
    updated_at = clock_timestamp()
WHERE id = sqlc.arg('id')
  AND kind = 'enrichment'
  AND chain_id = sqlc.arg('chain_id')::numeric
  AND stage = sqlc.arg('stage')
  AND stage_version = sqlc.arg('stage_version')
  AND payload->>'block_hash' = sqlc.arg('payload')
  AND payload->>'block_number' = sqlc.arg('payload_2')
  AND status = 'leased'
  AND lease_token = sqlc.arg('lease_token')
  AND lease_expires_at > clock_timestamp()
  AND claimed_generation = sqlc.arg('claimed_generation')
  AND leased_generation = sqlc.arg('claimed_generation')
  AND completed_generation < sqlc.arg('claimed_generation')
RETURNING status,
          status = 'queued'
          AND attempts = 0
          AND completed_generation < requested_generation AS followup_queued;

-- name: EnrichLegacyRetryOutbox :execrows
UPDATE transactional_outbox
SET attempts = LEAST(attempts + 1, 2147483647),
    last_error = sqlc.arg('last_error'),
    available_at = clock_timestamp() + (sqlc.arg('retry_microseconds')::bigint * INTERVAL '1 microsecond')
WHERE id = sqlc.arg('i_d') AND published_at IS NULL;

-- name: EnrichLegacySelectDependentReplayTargetID :one
SELECT id
FROM durable_jobs
WHERE chain_id = sqlc.arg('chain_id')::numeric
  AND kind = 'enrichment'
  AND payload->>'block_hash' = sqlc.arg('payload')
  AND stage = sqlc.arg('stage')
  AND stage_version = sqlc.arg('stage_version');

-- name: EnrichLegacySelectExistingJob :one
SELECT id, chain_id::text, stage, stage_version, attempts, max_attempts, payload, requested_generation
FROM durable_jobs
WHERE chain_id = sqlc.arg('chain_id')::numeric AND kind = sqlc.arg('kind') AND idempotency_key = sqlc.arg('idempotency_key');

-- name: EnrichLegacySelectReplayTargetByID :one
SELECT id, chain_id::text, stage, stage_version, attempts, max_attempts, payload,
       requested_generation, status
FROM durable_jobs
WHERE id = $1
FOR UPDATE;

-- name: EnrichLegacySelectStageJournalPublications :many
SELECT durable_job_id, job_generation
FROM block_journals
WHERE chain_id = sqlc.arg('chain_id')::numeric
  AND block_hash = sqlc.arg('block_hash')
  AND stage = sqlc.arg('stage')
ORDER BY sequence
FOR UPDATE;

-- name: EnrichLegacySelectStageResultPublication :one
SELECT durable_job_id, job_generation
FROM block_stage_results
WHERE chain_id = sqlc.arg('chain_id')::numeric
  AND block_hash = sqlc.arg('block_hash')
  AND stage = sqlc.arg('stage')
  AND stage_version = sqlc.arg('stage_version')
FOR UPDATE;

-- name: EnrichLegacyStateDiffTransactions :many
SELECT tx_index, tx_hash, raw
FROM transaction_inclusions
WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')
ORDER BY tx_index;

-- name: EnrichLegacyStatsReceiptSource :many
SELECT receipt.raw
FROM receipts AS receipt
WHERE receipt.chain_id = sqlc.arg('chain_id')::numeric
  AND receipt.block_number = sqlc.arg('block_number')::numeric
  AND receipt.block_hash = sqlc.arg('block_hash')
ORDER BY receipt.tx_index;

-- name: EnrichLegacyTerminalizeExhaustedJob :execrows
UPDATE durable_jobs
SET status = 'failed',
    result = sqlc.arg('result')::jsonb,
    last_error = sqlc.arg('last_error'),
    completed_generation = sqlc.arg('completed_generation'),
    leased_by = NULL,
    lease_token = NULL,
    lease_expires_at = NULL,
    leased_generation = NULL,
    updated_at = clock_timestamp()
WHERE id = sqlc.arg('id')
  AND kind = 'enrichment'
  AND chain_id = sqlc.arg('chain_id')::numeric
  AND stage = sqlc.arg('stage')
  AND stage_version = sqlc.arg('stage_version')
  AND payload->>'block_hash' = sqlc.arg('payload')
  AND payload->>'block_number' = sqlc.arg('payload_2')
  AND attempts >= max_attempts
  AND claimed_generation = sqlc.arg('completed_generation')
  AND requested_generation <= sqlc.arg('completed_generation')
  AND completed_generation < sqlc.arg('completed_generation')
  AND (
      (status = 'queued' AND available_at <= clock_timestamp())
      OR (status = 'leased' AND lease_expires_at <= clock_timestamp())
  );

-- name: EnrichLegacyTokenCanonical :one
SELECT EXISTS (
    SELECT 1
    FROM canonical_blocks
    WHERE chain_id = sqlc.arg('chain_id')::numeric AND number = sqlc.arg('number')::numeric AND block_hash = sqlc.arg('block_hash')
);

-- name: EnrichLegacyTokenLogs :many
SELECT log_index, tx_hash, address, raw
FROM logs
WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')
ORDER BY log_index;

-- name: EnrichLegacyTraceCanonical :one
SELECT EXISTS (
    SELECT 1 FROM canonical_blocks
    WHERE chain_id = sqlc.arg('chain_id')::numeric AND number = sqlc.arg('number')::numeric AND block_hash = sqlc.arg('block_hash')
);

-- name: EnrichLegacyTraceExecutionResolutions :many
SELECT resolution.transaction_hash, resolution.context_address,
       resolution.execution_address, resolution.execution_code_hash,
       resolution.resolution, resolution.evidence_source
FROM transaction_execution_code_resolutions AS resolution
WHERE resolution.chain_id = sqlc.arg('chain_id')::numeric
  AND resolution.block_number = sqlc.arg('block_number')::numeric
  AND resolution.block_hash = sqlc.arg('block_hash')
  AND resolution.canonical
  AND EXISTS (
      SELECT 1
      FROM published_block_stage_results AS published
      WHERE published.chain_id = resolution.chain_id
        AND published.block_number = resolution.block_number
        AND published.block_hash = resolution.block_hash
        AND published.stage = sqlc.arg('stage')
        AND published.stage_version = sqlc.arg('stage_version')
        AND published.state = 'complete'
  )
ORDER BY resolution.transaction_index, resolution.context_address;

-- name: EnrichLegacyTraceReceiptLogs :many
SELECT log_index, raw
FROM logs
WHERE chain_id = sqlc.arg('chain_id')::numeric
  AND block_number = sqlc.arg('block_number')::numeric
  AND block_hash = sqlc.arg('block_hash')
  AND tx_hash = sqlc.arg('tx_hash')
ORDER BY log_index;

-- name: EnrichLegacyTraceTransactions :many
SELECT tx_index, tx_hash, (raw->>'from')::text AS from_address, COALESCE(raw->>'to','')::text AS to_address, (raw->>'value')::text AS value, (raw->>'input')::text AS input, ((raw->>'to') IS NOT NULL)::boolean AS to_present
FROM transaction_inclusions
WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')
ORDER BY tx_index;

-- name: EnrichLegacyUpsertBeaconImplementationObservation :execrows
INSERT INTO beacon_implementation_observations AS current (
    chain_id, beacon_address, block_number, block_hash, beacon_code_hash,
    implementation_address, implementation_code_hash, stage_version,
    confidence, canonical, details
) VALUES (
    sqlc.arg('chain_id')::numeric, sqlc.arg('beacon_address'), sqlc.arg('block_number')::numeric, sqlc.arg('block_hash'), sqlc.arg('beacon_code_hash'),
    sqlc.arg('implementation_address'), sqlc.arg('implementation_code_hash'), sqlc.arg('stage_version'), sqlc.arg('confidence'), TRUE, sqlc.arg('details')::jsonb
)
ON CONFLICT (chain_id, beacon_address, block_hash, stage_version) DO UPDATE SET
    canonical = EXCLUDED.canonical,
    details = current.details || EXCLUDED.details
WHERE current.block_number = EXCLUDED.block_number
  AND current.beacon_code_hash = EXCLUDED.beacon_code_hash
  AND current.implementation_address = EXCLUDED.implementation_address
  AND current.implementation_code_hash = EXCLUDED.implementation_code_hash
  AND current.confidence = EXCLUDED.confidence;

-- name: EnrichLegacyUpsertDerivedJournal :execrows
INSERT INTO block_journals AS current (
    chain_id, block_hash, stage, sequence, payload, canonical
)
SELECT sqlc.arg('chain_id')::numeric, sqlc.arg('block_hash'), sqlc.arg('stage'), sqlc.arg('sequence')::numeric, sqlc.arg('payload')::jsonb,
       EXISTS (
           SELECT 1
           FROM canonical_blocks
           WHERE chain_id = sqlc.arg('chain_id')::numeric
             AND number = sqlc.arg('number')::numeric
             AND block_hash = sqlc.arg('block_hash')
       )
ON CONFLICT (chain_id, block_hash, stage, sequence) DO UPDATE SET
    payload = EXCLUDED.payload,
    canonical = EXCLUDED.canonical
WHERE current.durable_job_id IS NULL
  AND current.job_generation IS NULL;

-- name: EnrichLegacyUpsertProxyCodeObservation :execrows
INSERT INTO contract_code_observations AS current (
    chain_id, address, block_number, block_hash, code_hash, code, canonical
) VALUES (sqlc.arg('chain_id')::numeric, sqlc.arg('address'), sqlc.arg('block_number')::numeric, sqlc.arg('block_hash'), sqlc.arg('code_hash'), sqlc.arg('code'), TRUE)
ON CONFLICT (chain_id, address, block_hash) DO UPDATE SET
    code = COALESCE(current.code, EXCLUDED.code),
    canonical = EXCLUDED.canonical
WHERE current.code_hash = EXCLUDED.code_hash
  AND (current.code IS NULL OR current.code = EXCLUDED.code);

-- name: EnrichLegacyUpsertProxyDetectionEvidence :execrows
INSERT INTO proxy_detection_evidence AS current (
    chain_id, address, block_number, block_hash, stage_version, code_hash,
    candidate_kind, detection_state, reason, canonical,
    durable_job_id, job_generation, details
) VALUES (
    sqlc.arg('chain_id')::numeric, sqlc.arg('address'), sqlc.arg('block_number')::numeric, sqlc.arg('block_hash'), sqlc.arg('stage_version'), sqlc.arg('code_hash'),
    sqlc.arg('candidate_kind'), sqlc.arg('detection_state'), sqlc.arg('reason'), TRUE, sqlc.narg('durable_job_id')::bigint, sqlc.narg('job_generation')::bigint, sqlc.arg('details')::jsonb
)
ON CONFLICT (
    chain_id, address, block_hash, stage_version, candidate_kind,
    durable_job_id, job_generation
) DO UPDATE SET
    canonical = EXCLUDED.canonical,
    details = current.details || EXCLUDED.details
WHERE current.block_number = EXCLUDED.block_number
  AND current.code_hash = EXCLUDED.code_hash
  AND current.detection_state = EXCLUDED.detection_state
  AND current.reason = EXCLUDED.reason;

-- name: EnrichLegacyUpsertProxyInitializationEvent :execrows
INSERT INTO proxy_initialization_events AS current (
    chain_id, block_number, block_hash, log_index, transaction_hash,
    contract_address, version, stage_version, canonical
) VALUES (
    sqlc.arg('chain_id')::numeric, sqlc.arg('block_number')::numeric, sqlc.arg('block_hash'), sqlc.arg('log_index')::bigint, sqlc.arg('transaction_hash'),
    sqlc.arg('contract_address'), sqlc.arg('version')::numeric, sqlc.arg('stage_version'), TRUE
)
ON CONFLICT (chain_id, block_hash, log_index, stage_version) DO UPDATE SET
    canonical = EXCLUDED.canonical
WHERE current.block_number = EXCLUDED.block_number
  AND current.transaction_hash = EXCLUDED.transaction_hash
  AND current.contract_address = EXCLUDED.contract_address
  AND current.version = EXCLUDED.version;

-- name: EnrichLegacyUpsertProxyObservation :execrows
INSERT INTO proxy_observations AS current (
    chain_id, proxy_address, block_number, block_hash, stage_version,
    proxy_code_hash, proxy_kind, proxy_pattern, standard_version,
    implementation_address, admin_address, admin_code_hash,
    beacon_address, beacon_code_hash, immutable_args,
    implementation_code_hash, confidence, evidence_state, canonical, details
) VALUES (
    sqlc.arg('chain_id')::numeric, sqlc.arg('proxy_address'), sqlc.arg('block_number')::numeric, sqlc.arg('block_hash'), sqlc.arg('stage_version'),
    sqlc.arg('proxy_code_hash'), sqlc.arg('proxy_kind'), sqlc.arg('proxy_pattern'), sqlc.narg('standard_version'), sqlc.arg('implementation_address'), sqlc.arg('admin_address'), sqlc.arg('admin_code_hash'),
    sqlc.arg('beacon_address'), sqlc.arg('beacon_code_hash'), sqlc.arg('immutable_args'), sqlc.arg('implementation_code_hash'), sqlc.arg('confidence'), sqlc.arg('evidence_state'), TRUE, sqlc.arg('details')::jsonb
)
ON CONFLICT (chain_id, proxy_address, block_hash, stage_version) DO UPDATE SET
    canonical = EXCLUDED.canonical,
    details = current.details || EXCLUDED.details
WHERE current.block_number = EXCLUDED.block_number
  AND current.proxy_code_hash = EXCLUDED.proxy_code_hash
  AND current.proxy_kind = EXCLUDED.proxy_kind
  AND current.proxy_pattern = EXCLUDED.proxy_pattern
  AND current.standard_version IS NOT DISTINCT FROM EXCLUDED.standard_version
  AND current.implementation_address IS NOT DISTINCT FROM EXCLUDED.implementation_address
  AND current.admin_address IS NOT DISTINCT FROM EXCLUDED.admin_address
  AND current.admin_code_hash IS NOT DISTINCT FROM EXCLUDED.admin_code_hash
  AND current.beacon_address IS NOT DISTINCT FROM EXCLUDED.beacon_address
  AND current.beacon_code_hash IS NOT DISTINCT FROM EXCLUDED.beacon_code_hash
  AND current.immutable_args IS NOT DISTINCT FROM EXCLUDED.immutable_args
  AND current.implementation_code_hash IS NOT DISTINCT FROM EXCLUDED.implementation_code_hash
  AND current.confidence = EXCLUDED.confidence
  AND current.evidence_state = EXCLUDED.evidence_state;

-- name: EnrichLegacyUpsertProxyUpgradeEvent :execrows
INSERT INTO proxy_upgrade_events AS current (
    chain_id, block_number, block_hash, log_index, transaction_hash,
    emitter_address, event_kind, target_address, stage_version, canonical
) VALUES (
    sqlc.arg('chain_id')::numeric, sqlc.arg('block_number')::numeric, sqlc.arg('block_hash'), sqlc.arg('log_index')::bigint, sqlc.arg('transaction_hash'),
    sqlc.arg('emitter_address'), sqlc.arg('event_kind'), sqlc.arg('target_address'), sqlc.arg('stage_version'), TRUE
)
ON CONFLICT (chain_id, block_hash, log_index, stage_version) DO UPDATE SET
    canonical = EXCLUDED.canonical
WHERE current.block_number = EXCLUDED.block_number
  AND current.transaction_hash = EXCLUDED.transaction_hash
  AND current.emitter_address = EXCLUDED.emitter_address
  AND current.event_kind = EXCLUDED.event_kind
  AND current.target_address = EXCLUDED.target_address;

-- name: EnrichLegacyUpsertPublishedDerivedJournal :one
INSERT INTO block_journals AS current (
    chain_id, block_hash, stage, sequence, payload, canonical,
    durable_job_id, job_generation
)
SELECT sqlc.arg('chain_id')::numeric, sqlc.arg('block_hash'), sqlc.arg('stage'), sqlc.arg('sequence')::numeric, sqlc.arg('payload')::jsonb,
       EXISTS (
           SELECT 1
           FROM canonical_blocks
           WHERE chain_id = sqlc.arg('chain_id')::numeric
             AND number = sqlc.arg('number')::numeric
             AND block_hash = sqlc.arg('block_hash')
       ),
       sqlc.arg('durable_job_id'), sqlc.arg('job_generation')
ON CONFLICT (chain_id, block_hash, stage, sequence) DO UPDATE SET
    payload = EXCLUDED.payload,
    canonical = EXCLUDED.canonical,
    durable_job_id = EXCLUDED.durable_job_id,
    job_generation = EXCLUDED.job_generation
WHERE (
        current.durable_job_id IS NULL
        AND current.job_generation IS NULL
      ) OR (
        current.durable_job_id = EXCLUDED.durable_job_id
        AND current.job_generation <= EXCLUDED.job_generation
      )
RETURNING 1 AS inserted;

-- name: EnrichLegacyUpsertTokenContract :exec
INSERT INTO token_contracts AS current (
    chain_id, address, code_hash, standard, confidence,
    name, symbol, decimals, total_supply, metadata_state,
    observed_block_number, observed_block_hash
) VALUES (
    sqlc.arg('chain_id')::numeric, sqlc.arg('address'), sqlc.arg('code_hash'), sqlc.arg('standard'), sqlc.arg('confidence'),
    sqlc.narg('name'), sqlc.narg('symbol'), sqlc.narg('decimals'), sqlc.narg('total_supply')::numeric, sqlc.arg('metadata_state'),
    sqlc.arg('observed_block_number')::numeric, sqlc.arg('observed_block_hash')
)
ON CONFLICT (chain_id, address, code_hash, observed_block_hash) DO UPDATE SET
    standard = CASE
        WHEN (CASE EXCLUDED.confidence WHEN 'verified' THEN 4 WHEN 'high' THEN 3 WHEN 'inferred' THEN 2 ELSE 1 END) >
             (CASE current.confidence WHEN 'verified' THEN 4 WHEN 'high' THEN 3 WHEN 'inferred' THEN 2 ELSE 1 END)
          OR (
             EXCLUDED.confidence = current.confidence AND current.standard = 'unknown'
          )
        THEN EXCLUDED.standard
        ELSE current.standard
    END,
    confidence = CASE
        WHEN (CASE EXCLUDED.confidence WHEN 'verified' THEN 4 WHEN 'high' THEN 3 WHEN 'inferred' THEN 2 ELSE 1 END) >
             (CASE current.confidence WHEN 'verified' THEN 4 WHEN 'high' THEN 3 WHEN 'inferred' THEN 2 ELSE 1 END)
        THEN EXCLUDED.confidence
        ELSE current.confidence
    END,
    name = COALESCE(EXCLUDED.name, current.name),
    symbol = COALESCE(EXCLUDED.symbol, current.symbol),
    decimals = COALESCE(EXCLUDED.decimals, current.decimals),
    total_supply = COALESCE(EXCLUDED.total_supply, current.total_supply),
    metadata_state = CASE
        WHEN EXCLUDED.metadata_state = 'complete' OR current.metadata_state = 'complete' THEN 'complete'
        ELSE EXCLUDED.metadata_state
    END,
    updated_at = now();

-- name: EnrichLegacyUpsertUUPSImplementationObservation :execrows
INSERT INTO uups_implementation_observations AS current (
    chain_id, implementation_address, block_number, block_hash,
    implementation_code_hash, verification_job_id, stage_version,
    standard_version, probe_state, rejection_reason, proxiable_uuid,
    upgrade_interface_version, canonical
) VALUES (
    sqlc.arg('chain_id')::numeric, sqlc.arg('implementation_address'), sqlc.arg('block_number')::numeric, sqlc.arg('block_hash'),
    sqlc.arg('implementation_code_hash'), sqlc.arg('verification_job_id')::uuid, sqlc.arg('stage_version'), sqlc.arg('standard_version'), sqlc.arg('probe_state'), sqlc.narg('rejection_reason'), sqlc.arg('proxiable_uuid'), sqlc.narg('upgrade_interface_version'), TRUE
)
ON CONFLICT (
    chain_id, implementation_address, block_hash,
    stage_version, verification_job_id
) DO UPDATE SET canonical = EXCLUDED.canonical
WHERE current.block_number = EXCLUDED.block_number
  AND current.implementation_code_hash = EXCLUDED.implementation_code_hash
  AND current.standard_version = EXCLUDED.standard_version
  AND current.probe_state = EXCLUDED.probe_state
  AND current.rejection_reason IS NOT DISTINCT FROM EXCLUDED.rejection_reason
  AND current.proxiable_uuid IS NOT DISTINCT FROM EXCLUDED.proxiable_uuid
  AND current.upgrade_interface_version IS NOT DISTINCT FROM EXCLUDED.upgrade_interface_version;
