-- name: EnrichLegacyAtomicConsumePendingReplay :exec
UPDATE durable_jobs
SET status = 'queued',
    attempts = 0,
    available_at = clock_timestamp(),
    result = NULL,
    last_error = NULL,
    completed_generation = GREATEST(completed_generation, $3),
    leased_by = NULL,
    lease_token = NULL,
    lease_expires_at = NULL,
    leased_generation = NULL,
    updated_at = clock_timestamp()
WHERE id = $1
  AND kind = 'enrichment'
  AND chain_id = $4::numeric
  AND stage = $5
  AND stage_version = $6
  AND payload->>'block_hash' = $7
  AND payload->>'block_number' = $8
  AND status = 'leased'
  AND lease_token = $2
  AND lease_expires_at > clock_timestamp()
  AND claimed_generation = $3
  AND leased_generation = $3
  AND requested_generation > $3
  AND completed_generation < $3;

-- name: EnrichLegacyAtomicPublishSuccess :exec
UPDATE durable_jobs
SET status = 'succeeded',
    result = $4::jsonb,
    last_error = NULL,
    completed_generation = $3,
    leased_by = NULL,
    lease_token = NULL,
    lease_expires_at = NULL,
    leased_generation = NULL,
    updated_at = clock_timestamp()
WHERE id = $1
  AND kind = 'enrichment'
  AND chain_id = $5::numeric
  AND stage = $6
  AND stage_version = $7
  AND payload->>'block_hash' = $8
  AND payload->>'block_number' = $9
  AND status = 'leased'
  AND lease_token = $2
  AND lease_expires_at > clock_timestamp()
  AND claimed_generation = $3
  AND leased_generation = $3
  AND requested_generation = $3
  AND completed_generation < $3;

-- name: EnrichLegacyBlockStatsSource :many
SELECT block.raw, count(inclusion.tx_index), configuration.configured_start::text,
       parent.number::text, parent.timestamp::text,
       COALESCE(bool_or(canonical_parent.block_hash IS NOT NULL), FALSE)
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
WHERE block.chain_id = $1::numeric AND block.number = $2::numeric AND block.hash = $3
GROUP BY block.raw, configuration.configured_start, parent.number, parent.timestamp;

-- name: EnrichLegacyCanonicalBlock :many
SELECT EXISTS (
    SELECT 1
    FROM canonical_blocks
    WHERE chain_id = $1::numeric
      AND number = $2::numeric
      AND block_hash = $3
);

-- name: EnrichLegacyCarryForwardProxyGeneration :many
WITH source_generation AS MATERIALIZED (
    SELECT publication.job_generation
    FROM durable_stage_publications AS publication
    WHERE publication.job_id = $5::bigint
      AND publication.job_generation < $6::bigint
      AND publication.chain_id = $1::numeric
      AND publication.block_number = $2::numeric
      AND publication.block_hash = $3
      AND publication.stage = 'proxy'
      AND publication.stage_version = $4
      AND publication.state = 'complete'
    ORDER BY publication.job_generation DESC
    LIMIT 1
), redetected AS MATERIALIZED (
    SELECT generation.proxy_address AS address
    FROM proxy_observation_generations AS generation
    WHERE generation.chain_id = $1::numeric
      AND generation.observation_block_hash = $3
      AND generation.observation_stage_version = $4
      AND generation.durable_job_id = $5::bigint
      AND generation.job_generation = $6::bigint
    UNION
    SELECT generation.beacon_address AS address
    FROM beacon_observation_generations AS generation
    WHERE generation.chain_id = $1::numeric
      AND generation.observation_block_hash = $3
      AND generation.observation_stage_version = $4
      AND generation.durable_job_id = $5::bigint
      AND generation.job_generation = $6::bigint
    UNION
	SELECT generation.implementation_address AS address
	FROM uups_implementation_observation_generations AS generation
	WHERE generation.chain_id = $1::numeric
	  AND generation.observation_block_hash = $3
	  AND generation.observation_stage_version = $4
	  AND generation.durable_job_id = $5::bigint
	  AND generation.job_generation = $6::bigint
	UNION
    SELECT evidence.address
    FROM proxy_detection_evidence AS evidence
    WHERE evidence.chain_id = $1::numeric
      AND evidence.block_number = $2::numeric
      AND evidence.block_hash = $3
      AND evidence.stage_version = $4
      AND evidence.durable_job_id = $5::bigint
      AND evidence.job_generation = $6::bigint
), carried_proxies AS (
    INSERT INTO proxy_observation_generations (
        chain_id, proxy_address, observation_block_hash,
        observation_stage_version, durable_job_id, job_generation
    )
    SELECT source.chain_id, source.proxy_address, source.observation_block_hash,
           source.observation_stage_version, $5::bigint, $6::bigint
    FROM proxy_observation_generations AS source
    JOIN source_generation
      ON source.job_generation = source_generation.job_generation
    WHERE source.chain_id = $1::numeric
      AND source.observation_block_hash = $3
      AND source.observation_stage_version = $4
      AND source.durable_job_id = $5::bigint
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
           source.observation_stage_version, $5::bigint, $6::bigint
    FROM beacon_observation_generations AS source
    JOIN source_generation
      ON source.job_generation = source_generation.job_generation
    WHERE source.chain_id = $1::numeric
      AND source.observation_block_hash = $3
      AND source.observation_stage_version = $4
      AND source.durable_job_id = $5::bigint
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
		   source.verification_job_id, $5::bigint, $6::bigint
	FROM uups_implementation_observation_generations AS source
	JOIN source_generation
	  ON source.job_generation = source_generation.job_generation
	WHERE source.chain_id = $1::numeric
	  AND source.observation_block_hash = $3
	  AND source.observation_stage_version = $4
	  AND source.durable_job_id = $5::bigint
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
           $5::bigint, $6::bigint, source.evidence
    FROM proxy_artifact_resolutions AS source
    JOIN source_generation
      ON source.job_generation = source_generation.job_generation
    WHERE source.chain_id = $1::numeric
      AND source.observation_block_hash = $3
      AND source.observation_stage_version = $4
      AND source.durable_job_id = $5::bigint
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
           $5::bigint, $6::bigint, source.details
    FROM proxy_detection_evidence AS source
    JOIN source_generation
      ON source.job_generation = source_generation.job_generation
    WHERE source.chain_id = $1::numeric
      AND source.block_number = $2::numeric
      AND source.block_hash = $3
      AND source.stage_version = $4
      AND source.durable_job_id = $5::bigint
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

-- name: EnrichLegacyClaimOutbox :many
SELECT id, chain_id::text, topic, message_key, payload, attempts, generation
FROM transactional_outbox
WHERE published_at IS NULL
  AND available_at <= clock_timestamp()
  AND topic IN ('core.block.canonical', 'core.block.orphaned')
ORDER BY available_at, id
FOR UPDATE SKIP LOCKED
LIMIT 1;

-- name: EnrichLegacyConfirmPublishedSuccess :many
SELECT EXISTS (
    SELECT 1
    FROM durable_stage_publications AS publication
    WHERE publication.job_id = $1
      AND publication.job_generation = $2
      AND publication.state = 'complete'
);

-- name: EnrichLegacyConfirmSupersededPublication :many
SELECT EXISTS (
    SELECT 1
    FROM durable_stage_publications AS publication
    WHERE publication.job_id = $1
      AND publication.job_generation = $2
      AND publication.state = 'superseded'
);

-- name: EnrichLegacyDeleteEIP7702AuthorizationsBlock :exec
DELETE FROM eip7702_authorizations
WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3;

-- name: EnrichLegacyDeleteExecutionCodeResolutionsBlock :exec
DELETE FROM transaction_execution_code_resolutions
WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3;

-- name: EnrichLegacyDeleteStageJournal :exec
DELETE FROM block_journals
WHERE chain_id = $1::numeric
  AND block_hash = $2
  AND stage = $3;

-- name: EnrichLegacyDeleteStageResult :exec
DELETE FROM block_stage_results
WHERE chain_id = $1::numeric
  AND block_hash = $2
  AND stage = $3
  AND stage_version = $4;

-- name: EnrichLegacyDeleteStateDiffBlock :exec
DELETE FROM transaction_state_changes
WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3;

-- name: EnrichLegacyDeleteTraceBlock :exec
DELETE FROM normalized_traces
WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3;

-- name: EnrichLegacyDeleteTraceLogAttributions :exec
DELETE FROM trace_log_attributions
WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3;

-- name: EnrichLegacyDetectedToken :many
SELECT token.standard, token.confidence
FROM token_contracts AS token
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = token.chain_id
 AND canonical.number = token.observed_block_number
 AND canonical.block_hash = token.observed_block_hash
WHERE token.chain_id = $1::numeric
  AND token.address = $2
  AND token.observed_block_number <= $3::numeric
  AND token.standard <> 'unknown'
ORDER BY token.observed_block_number DESC, token.updated_at DESC
LIMIT 1;

-- name: EnrichLegacyEnablePublicationProtocol :many
SELECT set_config('etherview.enrichment_publication_protocol', '2', true);

-- name: EnrichLegacyEnqueueJob :many
INSERT INTO durable_jobs (
    chain_id, kind, stage, stage_version, idempotency_key, payload,
    priority, max_attempts
) VALUES ($1::numeric, $2, $3, $4, $5, $6::jsonb, $7, $8)
ON CONFLICT (chain_id, kind, idempotency_key) DO NOTHING
RETURNING id, chain_id::text, stage, stage_version, attempts, max_attempts, payload, requested_generation;

-- name: EnrichLegacyEnrichmentJobStatus :many
SELECT status
FROM durable_jobs
WHERE id = $1;

-- name: EnrichLegacyFinishJob :many
UPDATE durable_jobs
SET status = CASE
        WHEN requested_generation > leased_generation THEN 'queued'
        ELSE $3
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
        ELSE $4::jsonb
    END,
    last_error = CASE
        WHEN requested_generation > leased_generation THEN NULL
        ELSE $5
    END,
    completed_generation = GREATEST(completed_generation, leased_generation),
    leased_by = NULL,
    lease_token = NULL,
    lease_expires_at = NULL,
    leased_generation = NULL,
    updated_at = clock_timestamp()
WHERE id = $1
  AND kind = 'enrichment'
  AND chain_id = $7::numeric
  AND stage = $8
  AND stage_version = $9
  AND payload->>'block_hash' = $10
  AND payload->>'block_number' = $11
  AND status = 'leased'
  AND lease_token = $2
  AND lease_expires_at > clock_timestamp()
  AND claimed_generation = $6
  AND leased_generation = $6
  AND completed_generation < $6
RETURNING status = 'queued'
      AND attempts = 0
      AND completed_generation < requested_generation;

-- name: EnrichLegacyInsertBeaconObservationGeneration :exec
INSERT INTO beacon_observation_generations (
    chain_id, beacon_address, observation_block_hash,
    observation_stage_version, durable_job_id, job_generation
) VALUES ($1::numeric, $2, $3, $4, $5::bigint, $6::bigint)
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
    $1::numeric, $2::numeric, $3, $4, $5::numeric, $6::numeric, $7::numeric,
    $8::numeric, $9::numeric, $10::numeric, $11::numeric, $12::numeric,
    $13::numeric, $14::numeric, $15::numeric, $16::numeric, $17::numeric,
    $18, $19, true
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

-- name: EnrichLegacyInsertDurablePublication :many
INSERT INTO durable_stage_publications (
    job_id, job_generation, chain_id, block_number, block_hash,
    stage, stage_version, state, details, last_error
) VALUES (
    $1, $2, $3::numeric, $4::numeric, $5,
    $6, $7, $8, $9::jsonb, $10
)
RETURNING 1;

-- name: EnrichLegacyInsertEIP7702Authorization :exec
INSERT INTO eip7702_authorizations (
    chain_id, block_number, block_hash, transaction_hash, transaction_index,
    authorization_index, authorization_chain_id, authorization_nonce,
    delegate_address, y_parity, r, s, authority, signature_status,
    application_status, skip_reason, canonical
) VALUES (
    $1::numeric, $2::numeric, $3, $4, $5, $6, $7::numeric, $8::numeric,
    $9, $10, $11, $12, $13, $14, $15, $16, true
);

-- name: EnrichLegacyInsertExecutionCodeResolution :exec
INSERT INTO transaction_execution_code_resolutions (
    chain_id, block_number, block_hash, transaction_hash, transaction_index,
    context_address, execution_address, execution_code_hash, resolution,
    evidence_source, canonical
) VALUES (
    $1::numeric, $2::numeric, $3, $4, $5, $6, $7, $8, $9, $10, true
);

-- name: EnrichLegacyInsertProxyArtifactResolution :many
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
        $1::numeric, $2, $3, $4, $5, $6,
        $7, $8, $9, $10, $11, $12,
        $13, $14, $15::uuid, $16::uuid, $17::bigint, $18::bigint,
        $19::jsonb
    )
    ON CONFLICT DO NOTHING
    RETURNING id
)
SELECT id FROM inserted
UNION ALL
SELECT existing.id
FROM proxy_artifact_resolutions AS existing
WHERE existing.chain_id = $1::numeric
  AND existing.proxy_address = $2
  AND existing.observation_block_hash = $3
  AND existing.observation_stage_version = $4
  AND existing.durable_job_id IS NOT DISTINCT FROM $17::bigint
  AND existing.job_generation IS NOT DISTINCT FROM $18::bigint
  AND existing.proxy_code_hash = $5
  AND existing.proxy_kind = $6
  AND existing.proxy_pattern = $7
  AND existing.standard_version = $8
  AND existing.implementation_address = $9
  AND existing.implementation_code_hash = $10
  AND existing.admin_address IS NOT DISTINCT FROM $11::bytea
  AND existing.admin_code_hash IS NOT DISTINCT FROM $12::bytea
  AND existing.beacon_address IS NOT DISTINCT FROM $13::bytea
  AND existing.beacon_code_hash IS NOT DISTINCT FROM $14::bytea
  AND existing.proxy_artifact_job_id = $15::uuid
  AND existing.implementation_artifact_job_id IS NOT DISTINCT FROM $16::uuid
  AND existing.evidence = $19::jsonb
LIMIT 1;

-- name: EnrichLegacyInsertProxyObservationGeneration :exec
INSERT INTO proxy_observation_generations (
    chain_id, proxy_address, observation_block_hash,
    observation_stage_version, durable_job_id, job_generation
) VALUES ($1::numeric, $2, $3, $4, $5::bigint, $6::bigint)
ON CONFLICT DO NOTHING;

-- name: EnrichLegacyInsertPublishedStageResult :many
INSERT INTO block_stage_results AS current (
    chain_id, block_number, block_hash, stage, stage_version,
    state, details, last_error, durable_job_id, job_generation
) VALUES (
    $1::numeric, $2::numeric, $3, $4, $5,
    $6, $7::jsonb, $8, $9, $10
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
RETURNING 1;

-- name: EnrichLegacyInsertReplayRequest :exec
INSERT INTO durable_job_replay_requests (
    job_id, source_kind, source_key, requested_generation
) VALUES ($1, $2, $3, $4)
ON CONFLICT (job_id, source_kind, source_key) DO NOTHING;

-- name: EnrichLegacyInsertStageResult :exec
INSERT INTO block_stage_results AS current (
    chain_id, block_number, block_hash, stage, stage_version, state, details, last_error
) VALUES ($1::numeric, $2::numeric, $3, $4, $5, $6, $7::jsonb, $8)
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
    $1::numeric, $2::numeric, $3, $4, $5, $6, $7, $8, $9, $10, true
);

-- name: EnrichLegacyInsertTokenDelta :exec
INSERT INTO token_balance_deltas (
    chain_id, block_number, block_hash, log_index, sub_index,
    token_address, owner_address, token_id, delta, canonical
) VALUES ($1::numeric, $2::numeric, $3, $4, $5, $6, $7, $8::numeric, $9::numeric, true)
ON CONFLICT (
    chain_id, block_number, block_hash, log_index, sub_index, token_address, owner_address
) DO UPDATE SET token_id = EXCLUDED.token_id, delta = EXCLUDED.delta, canonical = true;

-- name: EnrichLegacyInsertTokenEvent :exec
INSERT INTO token_events (
    chain_id, block_number, block_hash, log_index, sub_index, transaction_hash,
    token_address, standard, event_kind, operator, from_address, to_address,
    token_id, amount, canonical, confidence, raw
) VALUES (
    $1::numeric, $2::numeric, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
    $13::numeric, $14::numeric, true, $15, $16::jsonb
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
    $1::numeric, $2::numeric, $3, $4, $5, $6, $7, $8, $9, $10, $11,
    $12, $13::numeric, $14::numeric, $15::numeric, $16, $17, $18, $19, $20,
    $21, $22, $23, true
);

-- name: EnrichLegacyInsertTraceLogAttribution :exec
INSERT INTO trace_log_attributions (
    chain_id, block_number, block_hash, transaction_hash, log_index,
    trace_path, call_type, execution_address, canonical
) VALUES (
    $1::numeric, $2::numeric, $3, $4, $5, $6, $7, $8, TRUE
);

-- name: EnrichLegacyInsertUUPSImplementationObservationGeneration :exec
INSERT INTO uups_implementation_observation_generations (
    chain_id, implementation_address, observation_block_hash,
    observation_stage_version, verification_job_id,
    durable_job_id, job_generation
) VALUES (
    $1::numeric, $2, $3, $4, $5::uuid, $6::bigint, $7::bigint
)
ON CONFLICT DO NOTHING;

-- name: EnrichLegacyLockCanonicalBlock :many
SELECT 1
FROM canonical_blocks
WHERE chain_id = $1::numeric AND number = $2::numeric AND block_hash = $3
FOR KEY SHARE;

-- name: EnrichLegacyLockPublicationJob :many
SELECT pg_advisory_xact_lock(-($1::bigint));

-- name: EnrichLegacyOrphanJournals :many
SELECT NOT EXISTS (
    SELECT 1
    FROM block_journals
    WHERE chain_id = $1::numeric
      AND block_hash = $2
      AND canonical
);

-- name: EnrichLegacyProxyCanonical :many
SELECT EXISTS (
    SELECT 1 FROM canonical_blocks
    WHERE chain_id = $1::numeric AND number = $2::numeric AND block_hash = $3
);

-- name: EnrichLegacyProxyReplayCandidates :many
SELECT target.address, target.target_kind, $5::text AS source,
		       verified.code_hash, verified.verification_job_id::text
		FROM proxy_replay_targets AS target
		JOIN durable_job_replay_requests AS replay_request
		  ON replay_request.job_id = $6::bigint
		 AND replay_request.source_kind = 'verification-publication'
		 AND target.source_verification_job_id::text = replay_request.source_key
		JOIN durable_jobs AS replay_job
		  ON replay_job.id = replay_request.job_id
		 AND replay_job.chain_id = target.chain_id
		 AND replay_job.kind = 'enrichment'
		 AND replay_job.stage = 'proxy'
		 AND replay_job.stage_version = $4
		 AND replay_job.payload->>'block_hash' = '0x' || encode(target.block_hash, 'hex')
		 AND replay_job.payload->>'block_number' = target.block_number::text
		 AND replay_job.status = 'leased'
		 AND replay_job.claimed_generation = $7::bigint
		 AND replay_job.leased_generation = $7::bigint
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
		WHERE target.chain_id = $1::numeric
		  AND target.block_number = $2::numeric
		  AND target.block_hash = $3
		  AND target.source_kind = 'verification_publication'
		  AND replay_request.requested_generation > replay_job.completed_generation
		  AND replay_request.requested_generation <= $7::bigint
		ORDER BY target.address, target.target_kind, source,
		         verified.verification_job_id;

-- name: EnrichLegacyPublishOutbox :exec
UPDATE transactional_outbox
SET published_at = clock_timestamp(),
    last_error = NULL,
    payload = jsonb_set(payload, '{_etherview_dispatch}', $2::jsonb, true)
WHERE id = $1 AND published_at IS NULL;

-- name: EnrichLegacyRenewJob :exec
UPDATE durable_jobs
SET lease_expires_at = clock_timestamp() + ($3 * INTERVAL '1 microsecond'),
    updated_at = clock_timestamp()
WHERE id = $1
  AND kind = 'enrichment'
  AND chain_id = $5::numeric
  AND stage = $6
  AND stage_version = $7
  AND payload->>'block_hash' = $8
  AND payload->>'block_number' = $9
  AND status = 'leased'
  AND lease_token = $2
  AND lease_expires_at > clock_timestamp()
  AND claimed_generation = $4
  AND leased_generation = $4;

-- name: EnrichLegacyRequestReplayJob :exec
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

-- name: EnrichLegacyRequeueJob :exec
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
WHERE id = $1
  AND chain_id = $2::numeric
  AND kind = 'enrichment'
  AND stage = $3
  AND stage_version = $4
  AND idempotency_key = $5
  AND status IN ('succeeded', 'failed');

-- name: EnrichLegacyRetryJob :many
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
        ELSE clock_timestamp() + ($4 * INTERVAL '1 microsecond')
    END,
    last_error = CASE
        WHEN requested_generation > leased_generation THEN NULL
        ELSE $3
    END,
    result = CASE
        WHEN requested_generation > leased_generation THEN NULL
        WHEN attempts >= max_attempts
            THEN jsonb_build_object('state', 'failed', 'error', $3::text)
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
WHERE id = $1
  AND kind = 'enrichment'
  AND chain_id = $6::numeric
  AND stage = $7
  AND stage_version = $8
  AND payload->>'block_hash' = $9
  AND payload->>'block_number' = $10
  AND status = 'leased'
  AND lease_token = $2
  AND lease_expires_at > clock_timestamp()
  AND claimed_generation = $5
  AND leased_generation = $5
  AND completed_generation < $5
RETURNING status,
          status = 'queued'
          AND attempts = 0
          AND completed_generation < requested_generation;

-- name: EnrichLegacyRetryOutbox :exec
UPDATE transactional_outbox
SET attempts = LEAST(attempts + 1, 2147483647),
    last_error = $2,
    available_at = clock_timestamp() + ($3 * INTERVAL '1 microsecond')
WHERE id = $1 AND published_at IS NULL;

-- name: EnrichLegacySelectDependentReplayTargetID :many
SELECT id
FROM durable_jobs
WHERE chain_id = $1::numeric
  AND kind = 'enrichment'
  AND payload->>'block_hash' = $2
  AND stage = $3
  AND stage_version = $4;

-- name: EnrichLegacySelectExistingJob :many
SELECT id, chain_id::text, stage, stage_version, attempts, max_attempts, payload, requested_generation
FROM durable_jobs
WHERE chain_id = $1::numeric AND kind = $2 AND idempotency_key = $3;

-- name: EnrichLegacySelectReplayTargetByID :many
SELECT id, chain_id::text, stage, stage_version, attempts, max_attempts, payload,
       requested_generation, status
FROM durable_jobs
WHERE id = $1
FOR UPDATE;

-- name: EnrichLegacySelectStageJournalPublications :many
SELECT durable_job_id, job_generation
FROM block_journals
WHERE chain_id = $1::numeric
  AND block_hash = $2
  AND stage = $3
ORDER BY sequence
FOR UPDATE;

-- name: EnrichLegacySelectStageResultPublication :many
SELECT durable_job_id, job_generation
FROM block_stage_results
WHERE chain_id = $1::numeric
  AND block_hash = $2
  AND stage = $3
  AND stage_version = $4
FOR UPDATE;

-- name: EnrichLegacyStateDiffTransactions :many
SELECT tx_index, tx_hash, raw
FROM transaction_inclusions
WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3
ORDER BY tx_index;

-- name: EnrichLegacyStatsReceiptSource :many
SELECT receipt.raw
FROM receipts AS receipt
WHERE receipt.chain_id = $1::numeric
  AND receipt.block_number = $2::numeric
  AND receipt.block_hash = $3
ORDER BY receipt.tx_index;

-- name: EnrichLegacyTerminalizeExhaustedJob :exec
UPDATE durable_jobs
SET status = 'failed',
    result = $3::jsonb,
    last_error = $4,
    completed_generation = $2,
    leased_by = NULL,
    lease_token = NULL,
    lease_expires_at = NULL,
    leased_generation = NULL,
    updated_at = clock_timestamp()
WHERE id = $1
  AND kind = 'enrichment'
  AND chain_id = $5::numeric
  AND stage = $6
  AND stage_version = $7
  AND payload->>'block_hash' = $8
  AND payload->>'block_number' = $9
  AND attempts >= max_attempts
  AND claimed_generation = $2
  AND requested_generation <= $2
  AND completed_generation < $2
  AND (
      (status = 'queued' AND available_at <= clock_timestamp())
      OR (status = 'leased' AND lease_expires_at <= clock_timestamp())
  );

-- name: EnrichLegacyTokenCanonical :many
SELECT EXISTS (
    SELECT 1
    FROM canonical_blocks
    WHERE chain_id = $1::numeric AND number = $2::numeric AND block_hash = $3
);

-- name: EnrichLegacyTokenLogs :many
SELECT log_index, tx_hash, address, raw
FROM logs
WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3
ORDER BY log_index;

-- name: EnrichLegacyTraceCanonical :many
SELECT EXISTS (
    SELECT 1 FROM canonical_blocks
    WHERE chain_id = $1::numeric AND number = $2::numeric AND block_hash = $3
);

-- name: EnrichLegacyTraceExecutionResolutions :many
SELECT resolution.transaction_hash, resolution.context_address,
       resolution.execution_address, resolution.execution_code_hash,
       resolution.resolution, resolution.evidence_source
FROM transaction_execution_code_resolutions AS resolution
WHERE resolution.chain_id = $1::numeric
  AND resolution.block_number = $2::numeric
  AND resolution.block_hash = $3
  AND resolution.canonical
  AND EXISTS (
      SELECT 1
      FROM published_block_stage_results AS published
      WHERE published.chain_id = resolution.chain_id
        AND published.block_number = resolution.block_number
        AND published.block_hash = resolution.block_hash
        AND published.stage = $4
        AND published.stage_version = $5
        AND published.state = 'complete'
  )
ORDER BY resolution.transaction_index, resolution.context_address;

-- name: EnrichLegacyTraceReceiptLogs :many
SELECT log_index, raw
FROM logs
WHERE chain_id = $1::numeric
  AND block_number = $2::numeric
  AND block_hash = $3
  AND tx_hash = $4
ORDER BY log_index;

-- name: EnrichLegacyTraceTransactions :many
SELECT tx_index, tx_hash,
       raw->>'from', raw->>'to', raw->>'value', raw->>'input'
FROM transaction_inclusions
WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3
ORDER BY tx_index;

-- name: EnrichLegacyUpsertBeaconImplementationObservation :exec
INSERT INTO beacon_implementation_observations AS current (
    chain_id, beacon_address, block_number, block_hash, beacon_code_hash,
    implementation_address, implementation_code_hash, stage_version,
    confidence, canonical, details
) VALUES (
    $1::numeric, $2, $3::numeric, $4, $5,
    $6, $7, $8, $9, TRUE, $10::jsonb
)
ON CONFLICT (chain_id, beacon_address, block_hash, stage_version) DO UPDATE SET
    canonical = EXCLUDED.canonical,
    details = current.details || EXCLUDED.details
WHERE current.block_number = EXCLUDED.block_number
  AND current.beacon_code_hash = EXCLUDED.beacon_code_hash
  AND current.implementation_address = EXCLUDED.implementation_address
  AND current.implementation_code_hash = EXCLUDED.implementation_code_hash
  AND current.confidence = EXCLUDED.confidence;

-- name: EnrichLegacyUpsertDerivedJournal :exec
INSERT INTO block_journals AS current (
    chain_id, block_hash, stage, sequence, payload, canonical
)
SELECT $1::numeric, $2, $3, $4::numeric, $5::jsonb,
       EXISTS (
           SELECT 1
           FROM canonical_blocks
           WHERE chain_id = $1::numeric
             AND number = $6::numeric
             AND block_hash = $2
       )
ON CONFLICT (chain_id, block_hash, stage, sequence) DO UPDATE SET
    payload = EXCLUDED.payload,
    canonical = EXCLUDED.canonical
WHERE current.durable_job_id IS NULL
  AND current.job_generation IS NULL;

-- name: EnrichLegacyUpsertProxyCodeObservation :exec
INSERT INTO contract_code_observations AS current (
    chain_id, address, block_number, block_hash, code_hash, code, canonical
) VALUES ($1::numeric, $2, $3::numeric, $4, $5, $6, TRUE)
ON CONFLICT (chain_id, address, block_hash) DO UPDATE SET
    code = COALESCE(current.code, EXCLUDED.code),
    canonical = EXCLUDED.canonical
WHERE current.code_hash = EXCLUDED.code_hash
  AND (current.code IS NULL OR current.code = EXCLUDED.code);

-- name: EnrichLegacyUpsertProxyDetectionEvidence :exec
INSERT INTO proxy_detection_evidence AS current (
    chain_id, address, block_number, block_hash, stage_version, code_hash,
    candidate_kind, detection_state, reason, canonical,
    durable_job_id, job_generation, details
) VALUES (
    $1::numeric, $2, $3::numeric, $4, $5, $6,
    $7, $8, $9, TRUE, $10::bigint, $11::bigint, $12::jsonb
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

-- name: EnrichLegacyUpsertProxyInitializationEvent :exec
INSERT INTO proxy_initialization_events AS current (
    chain_id, block_number, block_hash, log_index, transaction_hash,
    contract_address, version, stage_version, canonical
) VALUES (
    $1::numeric, $2::numeric, $3, $4::bigint, $5,
    $6, $7::numeric, $8, TRUE
)
ON CONFLICT (chain_id, block_hash, log_index, stage_version) DO UPDATE SET
    canonical = EXCLUDED.canonical
WHERE current.block_number = EXCLUDED.block_number
  AND current.transaction_hash = EXCLUDED.transaction_hash
  AND current.contract_address = EXCLUDED.contract_address
  AND current.version = EXCLUDED.version;

-- name: EnrichLegacyUpsertProxyObservation :exec
INSERT INTO proxy_observations AS current (
    chain_id, proxy_address, block_number, block_hash, stage_version,
    proxy_code_hash, proxy_kind, proxy_pattern, standard_version,
    implementation_address, admin_address, admin_code_hash,
    beacon_address, beacon_code_hash, immutable_args,
    implementation_code_hash, confidence, evidence_state, canonical, details
) VALUES (
    $1::numeric, $2, $3::numeric, $4, $5,
    $6, $7, $8, $9, $10, $11, $12,
    $13, $14, $15, $16, $17, $18, TRUE, $19::jsonb
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

-- name: EnrichLegacyUpsertProxyUpgradeEvent :exec
INSERT INTO proxy_upgrade_events AS current (
    chain_id, block_number, block_hash, log_index, transaction_hash,
    emitter_address, event_kind, target_address, stage_version, canonical
) VALUES (
    $1::numeric, $2::numeric, $3, $4::bigint, $5,
    $6, $7, $8, $9, TRUE
)
ON CONFLICT (chain_id, block_hash, log_index, stage_version) DO UPDATE SET
    canonical = EXCLUDED.canonical
WHERE current.block_number = EXCLUDED.block_number
  AND current.transaction_hash = EXCLUDED.transaction_hash
  AND current.emitter_address = EXCLUDED.emitter_address
  AND current.event_kind = EXCLUDED.event_kind
  AND current.target_address = EXCLUDED.target_address;

-- name: EnrichLegacyUpsertPublishedDerivedJournal :many
INSERT INTO block_journals AS current (
    chain_id, block_hash, stage, sequence, payload, canonical,
    durable_job_id, job_generation
)
SELECT $1::numeric, $2, $3, $4::numeric, $5::jsonb,
       EXISTS (
           SELECT 1
           FROM canonical_blocks
           WHERE chain_id = $1::numeric
             AND number = $6::numeric
             AND block_hash = $2
       ),
       $7, $8
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
RETURNING 1;

-- name: EnrichLegacyUpsertTokenContract :exec
INSERT INTO token_contracts AS current (
    chain_id, address, code_hash, standard, confidence,
    name, symbol, decimals, total_supply, metadata_state,
    observed_block_number, observed_block_hash
) VALUES (
    $1::numeric, $2, $3, $4, $5,
    $6, $7, $8, $9::numeric, $10,
    $11::numeric, $12
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

-- name: EnrichLegacyUpsertUUPSImplementationObservation :exec
INSERT INTO uups_implementation_observations AS current (
    chain_id, implementation_address, block_number, block_hash,
    implementation_code_hash, verification_job_id, stage_version,
    standard_version, probe_state, rejection_reason, proxiable_uuid,
    upgrade_interface_version, canonical
) VALUES (
    $1::numeric, $2, $3::numeric, $4,
    $5, $6::uuid, $7, $8, $9, $10, $11, $12, TRUE
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
