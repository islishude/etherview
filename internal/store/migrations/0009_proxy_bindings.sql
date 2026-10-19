-- Fresh-database baseline: proxy bindings.

-- Functions

CREATE FUNCTION enforce_proxy_artifact_resolution() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    expected_proxy_artifact TEXT;
BEGIN
    IF NEW.proxy_pattern = 'uups' THEN
        RAISE EXCEPTION 'UUPS compatibility is shared implementation evidence, not a proxy artifact resolution';
    END IF;
    expected_proxy_artifact := CASE NEW.proxy_pattern
        WHEN 'transparent' THEN 'transparent_proxy'
        WHEN 'beacon' THEN 'beacon_proxy'
        ELSE 'erc1967_proxy'
    END;
    IF NOT EXISTS (
        SELECT 1
        FROM verified_contract_proxy_artifacts AS artifact
        JOIN proxy_observations AS observation
          ON observation.chain_id = NEW.chain_id
         AND observation.proxy_address = NEW.proxy_address
         AND observation.block_hash = NEW.observation_block_hash
         AND observation.stage_version = NEW.observation_stage_version
        WHERE artifact.verification_job_id = NEW.proxy_artifact_job_id
          AND artifact.chain_id = NEW.chain_id
          AND artifact.address = NEW.proxy_address
          AND artifact.code_hash = NEW.proxy_code_hash
          AND observation.proxy_code_hash = NEW.proxy_code_hash
          AND artifact.artifact_kind = expected_proxy_artifact
          AND artifact.standard_version = NEW.standard_version
          AND (
              (NEW.proxy_pattern = 'transparent'
               AND artifact.runtime_immutable_address = NEW.admin_address) OR
              (NEW.proxy_pattern = 'beacon'
               AND artifact.runtime_immutable_address = NEW.beacon_address) OR
              (NEW.proxy_pattern = 'erc1967'
               AND artifact.runtime_immutable_address IS NULL)
          )
    ) THEN
        RAISE EXCEPTION 'proxy artifact resolution lacks exact proxy evidence';
    END IF;
    IF NEW.durable_job_id IS NOT NULL AND NOT EXISTS (
        SELECT 1
        FROM durable_jobs AS job
        WHERE job.id = NEW.durable_job_id
          AND job.chain_id = NEW.chain_id
          AND job.stage = 'proxy'
          AND job.stage_version = 2
          AND job.payload->>'block_hash' =
              '0x' || encode(NEW.observation_block_hash, 'hex')
          AND job.leased_generation = NEW.job_generation
          AND job.status = 'leased'
    ) THEN
        RAISE EXCEPTION 'proxy artifact resolution generation is not the active proxy@2 lease';
    END IF;
    RETURN NEW;
END
$$;

CREATE FUNCTION enforce_uups_implementation_observation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.canonical IS NOT TRUE OR NOT EXISTS (
        SELECT 1
        FROM verified_contract_proxy_artifacts AS artifact
        JOIN verified_contracts AS verified
          ON verified.chain_id = artifact.chain_id
         AND verified.address = artifact.address
         AND verified.code_hash = artifact.code_hash
         AND verified.valid_from_block = artifact.valid_from_block
         AND verified.verification_job_id = artifact.verification_job_id
         AND verified.request_digest = artifact.request_digest
        JOIN contract_code_observations AS code_observation
          ON code_observation.chain_id = NEW.chain_id
         AND code_observation.address = NEW.implementation_address
         AND code_observation.block_number = NEW.block_number
         AND code_observation.block_hash = NEW.block_hash
         AND code_observation.code_hash = NEW.implementation_code_hash
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = code_observation.chain_id
         AND canonical.number = code_observation.block_number
         AND canonical.block_hash = code_observation.block_hash
        WHERE artifact.verification_job_id = NEW.verification_job_id
          AND artifact.chain_id = NEW.chain_id
          AND artifact.address = NEW.implementation_address
          AND artifact.code_hash = NEW.implementation_code_hash
          AND artifact.artifact_kind = 'uups_implementation'
          AND artifact.standard_version = NEW.standard_version
          AND artifact.runtime_immutable_address = NEW.implementation_address
          AND artifact.valid_from_block <= NEW.block_number
          AND (verified.valid_to_block IS NULL OR
               verified.valid_to_block >= NEW.block_number)
          AND code_observation.code IS NOT NULL
          AND code_observation.canonical = TRUE
    ) THEN
        RAISE EXCEPTION 'UUPS observation lacks exact canonical artifact and code evidence';
    END IF;
    RETURN NEW;
END
$$;

CREATE FUNCTION enforce_uups_implementation_observation_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' OR
       (to_jsonb(NEW) - 'canonical') IS DISTINCT FROM
       (to_jsonb(OLD) - 'canonical') THEN
        RAISE EXCEPTION 'UUPS implementation observations are append-only';
    END IF;
    RETURN NEW;
END
$$;

CREATE FUNCTION enforce_uups_implementation_observation_witness() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM uups_implementation_observation_generations AS generation
        WHERE generation.chain_id = NEW.chain_id
          AND generation.implementation_address = NEW.implementation_address
          AND generation.observation_block_hash = NEW.block_hash
          AND generation.observation_stage_version = NEW.stage_version
          AND generation.verification_job_id = NEW.verification_job_id
    ) THEN
        RAISE EXCEPTION 'UUPS implementation observation lacks a lease-fenced generation witness';
    END IF;
    RETURN NEW;
END
$$;

CREATE FUNCTION enforce_verified_proxy_binding() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM verification_results AS result
        JOIN verification_jobs AS job ON job.id = result.job_id
        JOIN proxy_observations AS observation
          ON observation.chain_id = NEW.chain_id
         AND observation.proxy_address = NEW.proxy_address
         AND observation.block_hash = NEW.observation_block_hash
         AND observation.stage_version = NEW.observation_stage_version
        WHERE result.job_id = NEW.verification_job_id
          AND result.request_digest = NEW.request_digest
          AND result.outcome_kind = 'proxy_verification_success'
          AND job.kind = 'proxy'
          AND job.status = 'succeeded'
          AND job.chain_id = NEW.chain_id
          AND job.address = NEW.proxy_address
          AND job.code_hash = NEW.proxy_code_hash
          AND job.block_hash = NEW.observation_block_hash
          AND observation.block_number = NEW.observation_block_number
          AND observation.proxy_code_hash = NEW.proxy_code_hash
          AND (
              NEW.proxy_pattern <> 'clone' OR (
                  observation.proxy_kind = NEW.proxy_kind
                  AND observation.proxy_pattern = 'clone'
                  AND observation.evidence_state = 'exact'
                  AND observation.implementation_address = NEW.implementation_address
                  AND observation.implementation_code_hash = NEW.implementation_code_hash
              )
          )
          AND observation.canonical = TRUE
          AND observation.confidence IN ('verified', 'high')
          AND result.outcome->>'proxy_address' =
              '0x' || encode(NEW.proxy_address, 'hex')
          AND result.outcome->>'proxy_code_hash' =
              '0x' || encode(NEW.proxy_code_hash, 'hex')
          AND result.outcome->>'observation_block_hash' =
              '0x' || encode(NEW.observation_block_hash, 'hex')
          AND (result.outcome->>'observation_stage_version')::integer =
              NEW.observation_stage_version
          AND result.outcome->>'proxy_kind' = NEW.proxy_kind
          AND result.outcome->>'proxy_pattern' = NEW.proxy_pattern
          AND result.outcome->>'standard_version' IS NOT DISTINCT FROM
              NEW.standard_version
          AND result.outcome->>'management_kind' = NEW.management_kind
          AND result.outcome->>'implementation_address' =
              '0x' || encode(NEW.implementation_address, 'hex')
          AND result.outcome->>'implementation_code_hash' =
              '0x' || encode(NEW.implementation_code_hash, 'hex')
          AND result.outcome->>'admin_address' IS NOT DISTINCT FROM
              CASE WHEN NEW.admin_address IS NULL THEN NULL
                   ELSE '0x' || encode(NEW.admin_address, 'hex') END
          AND result.outcome->>'admin_code_hash' IS NOT DISTINCT FROM
              CASE WHEN NEW.admin_code_hash IS NULL THEN NULL
                   ELSE '0x' || encode(NEW.admin_code_hash, 'hex') END
          AND result.outcome->>'beacon_address' IS NOT DISTINCT FROM
              CASE WHEN NEW.beacon_address IS NULL THEN NULL
                   ELSE '0x' || encode(NEW.beacon_address, 'hex') END
          AND result.outcome->>'beacon_code_hash' IS NOT DISTINCT FROM
              CASE WHEN NEW.beacon_code_hash IS NULL THEN NULL
                   ELSE '0x' || encode(NEW.beacon_code_hash, 'hex') END
          AND result.outcome->>'management_address' IS NOT DISTINCT FROM
              CASE WHEN NEW.management_address IS NULL THEN NULL
                   ELSE '0x' || encode(NEW.management_address, 'hex') END
          AND result.outcome->>'management_code_hash' IS NOT DISTINCT FROM
              CASE WHEN NEW.management_code_hash IS NULL THEN NULL
                   ELSE '0x' || encode(NEW.management_code_hash, 'hex') END
          AND (result.outcome->>'observation_generation_id')::bigint =
              NEW.observation_generation_id
          AND (result.outcome->>'artifact_resolution_id')::bigint
              IS NOT DISTINCT FROM NEW.artifact_resolution_id
          AND (result.outcome->>'beacon_generation_id')::bigint
              IS NOT DISTINCT FROM NEW.beacon_generation_id
          AND (result.outcome->>'uups_generation_id')::bigint
              IS NOT DISTINCT FROM NEW.uups_generation_id
          AND (result.outcome->>'context_block_number')::numeric =
              NEW.context_block_number
          AND result.outcome->>'context_block_hash' =
              '0x' || encode(NEW.context_block_hash, 'hex')
    ) THEN
        RAISE EXCEPTION 'verified proxy binding disagrees with its immutable result';
    END IF;

    -- Serialize with every 0033 coverage refresh for this chain, then recheck
    -- both the canonical tip and coverage in the INSERT statement itself.
    -- CompleteProxyV2 takes this same lock before resolving current state, so
    -- a tip advance or same-block replay cannot replace facts between its
    -- current-state query and this immutable publication. UUPS additionally
    -- checks probe-to-tip coverage below.
    PERFORM pg_advisory_xact_lock(hashtextextended(
        'etherview:proxy-interaction-coverage:' || NEW.chain_id::text,
        0
    ));
    IF NOT EXISTS (
        SELECT 1
        FROM canonical_blocks AS context
        WHERE context.chain_id = NEW.chain_id
          AND context.number = NEW.context_block_number
          AND context.block_hash = NEW.context_block_hash
          AND NOT EXISTS (
              SELECT 1
              FROM canonical_blocks AS newer
              WHERE newer.chain_id = NEW.chain_id
                AND newer.number > context.number
          )
    ) THEN
        RAISE EXCEPTION 'proxy binding context is not the canonical tip';
    END IF;
    IF NOT proxy_interaction_coverage_contains(
        NEW.chain_id,
        NEW.observation_block_number, NEW.observation_block_hash,
        NEW.context_block_number, NEW.context_block_hash
    ) THEN
        RAISE EXCEPTION 'proxy binding lacks continuous observation-to-context coverage';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM proxy_observation_generations AS generation
        JOIN published_block_stage_results AS published
          ON published.chain_id = generation.chain_id
         AND published.block_hash = generation.observation_block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = generation.observation_stage_version
         AND published.durable_job_id = generation.durable_job_id
         AND published.job_generation = generation.job_generation
         AND published.state = 'complete'
        WHERE generation.id = NEW.observation_generation_id
          AND generation.chain_id = NEW.chain_id
          AND generation.proxy_address = NEW.proxy_address
          AND generation.observation_block_hash = NEW.observation_block_hash
    ) THEN
        RAISE EXCEPTION 'proxy observation generation is not published';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM proxy_observation_generations AS generation
        JOIN proxy_detection_evidence AS evidence
          ON evidence.chain_id = generation.chain_id
         AND evidence.address = generation.proxy_address
         AND evidence.stage_version = generation.observation_stage_version
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
        WHERE generation.id = NEW.observation_generation_id
          AND evidence.code_hash = NEW.proxy_code_hash
          AND evidence.candidate_kind = 'proxy'
          AND evidence.canonical = TRUE
          AND evidence.block_number <= NEW.context_block_number
          AND NOT (
              evidence.reason = 'immutable_args_creation_unverified'
              AND NEW.proxy_pattern = 'clone'
              AND EXISTS (
                  SELECT 1
                  FROM proxy_observations AS exact_clone
                  JOIN published_block_stage_results AS exact_published
                    ON exact_published.chain_id = generation.chain_id
                   AND exact_published.block_hash = generation.observation_block_hash
                   AND exact_published.stage = 'proxy'
                   AND exact_published.stage_version = generation.observation_stage_version
                   AND exact_published.durable_job_id = generation.durable_job_id
                   AND exact_published.job_generation = generation.job_generation
                   AND exact_published.state = 'complete'
                  WHERE exact_clone.chain_id = generation.chain_id
                    AND exact_clone.proxy_address = generation.proxy_address
                    AND exact_clone.block_hash = generation.observation_block_hash
                    AND exact_clone.stage_version = generation.observation_stage_version
                    AND exact_clone.proxy_code_hash = evidence.code_hash
                    AND exact_clone.proxy_pattern = 'clone'
                    AND exact_clone.evidence_state = 'exact'
                    AND octet_length(exact_clone.immutable_args) > 0
                    AND exact_clone.details->>'immutable_args_creation_authenticated' = 'true'
              )
          )
          AND (
              evidence.block_number > NEW.observation_block_number OR (
                  evidence.block_number = NEW.observation_block_number
                  AND evidence.block_hash = NEW.observation_block_hash
                  AND evidence.durable_job_id = generation.durable_job_id
                  AND evidence.job_generation >= generation.job_generation
              )
          )
    ) THEN
        RAISE EXCEPTION 'proxy binding is shadowed by published negative detection evidence';
    END IF;

    IF NEW.artifact_resolution_id IS NOT NULL AND NOT EXISTS (
        SELECT 1
        FROM proxy_artifact_resolutions AS resolution
        JOIN published_block_stage_results AS published
          ON published.chain_id = resolution.chain_id
         AND published.block_hash = resolution.observation_block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = resolution.observation_stage_version
         AND published.durable_job_id = resolution.durable_job_id
         AND published.job_generation = resolution.job_generation
         AND published.state = 'complete'
        WHERE resolution.id = NEW.artifact_resolution_id
          AND resolution.chain_id = NEW.chain_id
          AND resolution.proxy_address = NEW.proxy_address
          AND resolution.observation_block_hash = NEW.observation_block_hash
          AND resolution.proxy_code_hash = NEW.proxy_code_hash
          AND resolution.proxy_kind = NEW.proxy_kind
          AND resolution.proxy_pattern = CASE NEW.proxy_pattern
              WHEN 'uups' THEN 'erc1967'
              ELSE NEW.proxy_pattern
          END
          AND resolution.standard_version = NEW.standard_version
          AND (
              NEW.proxy_pattern = 'beacon' OR (
                  resolution.implementation_address = NEW.implementation_address
                  AND resolution.implementation_code_hash = NEW.implementation_code_hash
              )
          )
          AND resolution.admin_address IS NOT DISTINCT FROM NEW.admin_address
          AND resolution.admin_code_hash IS NOT DISTINCT FROM NEW.admin_code_hash
          AND resolution.beacon_address IS NOT DISTINCT FROM NEW.beacon_address
          AND resolution.beacon_code_hash IS NOT DISTINCT FROM NEW.beacon_code_hash
          AND (NEW.proxy_pattern <> 'uups' OR
               resolution.implementation_artifact_job_id IS NULL)
    ) THEN
        RAISE EXCEPTION 'proxy artifact resolution is not published';
    END IF;

    IF NEW.beacon_generation_id IS NOT NULL AND NOT EXISTS (
        SELECT 1
        FROM beacon_observation_generations AS generation
        JOIN beacon_implementation_observations AS observation
          ON observation.chain_id = generation.chain_id
         AND observation.beacon_address = generation.beacon_address
         AND observation.block_hash = generation.observation_block_hash
         AND observation.stage_version = generation.observation_stage_version
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = observation.chain_id
         AND canonical.number = observation.block_number
         AND canonical.block_hash = observation.block_hash
        JOIN published_block_stage_results AS published
          ON published.chain_id = generation.chain_id
         AND published.block_hash = generation.observation_block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = generation.observation_stage_version
         AND published.durable_job_id = generation.durable_job_id
         AND published.job_generation = generation.job_generation
         AND published.state = 'complete'
        WHERE generation.id = NEW.beacon_generation_id
          AND generation.chain_id = NEW.chain_id
          AND generation.beacon_address = NEW.beacon_address
          AND observation.beacon_code_hash = NEW.beacon_code_hash
          AND observation.implementation_address = NEW.implementation_address
          AND observation.implementation_code_hash = NEW.implementation_code_hash
          AND observation.canonical = TRUE
          AND observation.confidence IN ('verified', 'high')
          AND observation.block_number <= NEW.context_block_number
          AND NOT EXISTS (
              SELECT 1
              FROM beacon_implementation_observations AS newer_observation
              JOIN canonical_blocks AS newer_canonical
                ON newer_canonical.chain_id = newer_observation.chain_id
               AND newer_canonical.number = newer_observation.block_number
               AND newer_canonical.block_hash = newer_observation.block_hash
              JOIN beacon_observation_generations AS newer_generation
                ON newer_generation.chain_id = newer_observation.chain_id
               AND newer_generation.beacon_address = newer_observation.beacon_address
               AND newer_generation.observation_block_hash = newer_observation.block_hash
               AND newer_generation.observation_stage_version = newer_observation.stage_version
              JOIN published_block_stage_results AS newer_published
                ON newer_published.chain_id = newer_generation.chain_id
               AND newer_published.block_hash = newer_generation.observation_block_hash
               AND newer_published.stage = 'proxy'
               AND newer_published.stage_version = newer_generation.observation_stage_version
               AND newer_published.durable_job_id = newer_generation.durable_job_id
               AND newer_published.job_generation = newer_generation.job_generation
               AND newer_published.state = 'complete'
              WHERE newer_observation.chain_id = observation.chain_id
                AND newer_observation.beacon_address = observation.beacon_address
                AND newer_observation.beacon_code_hash = NEW.beacon_code_hash
                AND newer_observation.stage_version = 2
                AND newer_observation.canonical = TRUE
                AND newer_observation.confidence IN ('verified', 'high')
                AND newer_observation.block_number <= NEW.context_block_number
                AND (
                    newer_observation.block_number > observation.block_number OR (
                        newer_observation.block_number = observation.block_number
                        AND newer_generation.id > generation.id
                    )
                )
          )
    ) THEN
        RAISE EXCEPTION 'beacon implementation generation is not published';
    END IF;

    IF NEW.beacon_generation_id IS NOT NULL AND EXISTS (
        SELECT 1
        FROM beacon_observation_generations AS generation
        JOIN beacon_implementation_observations AS observation
          ON observation.chain_id = generation.chain_id
         AND observation.beacon_address = generation.beacon_address
         AND observation.block_hash = generation.observation_block_hash
         AND observation.stage_version = generation.observation_stage_version
        JOIN proxy_detection_evidence AS evidence
          ON evidence.chain_id = observation.chain_id
         AND evidence.address = observation.beacon_address
         AND evidence.stage_version = observation.stage_version
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
        WHERE generation.id = NEW.beacon_generation_id
          AND evidence.code_hash = NEW.beacon_code_hash
          AND evidence.candidate_kind = 'beacon'
          AND evidence.canonical = TRUE
          AND evidence.block_number <= NEW.context_block_number
          AND (
              evidence.block_number > observation.block_number OR (
                  evidence.block_number = observation.block_number
                  AND evidence.block_hash = observation.block_hash
                  AND evidence.durable_job_id = generation.durable_job_id
                  AND evidence.job_generation >= generation.job_generation
              )
          )
    ) THEN
        RAISE EXCEPTION 'proxy binding beacon is shadowed by published negative detection evidence';
    END IF;

    IF NEW.uups_generation_id IS NOT NULL AND NOT EXISTS (
        SELECT 1
        FROM uups_implementation_observation_generations AS generation
        JOIN uups_implementation_observations AS observation
          ON observation.chain_id = generation.chain_id
         AND observation.implementation_address = generation.implementation_address
         AND observation.block_hash = generation.observation_block_hash
         AND observation.stage_version = generation.observation_stage_version
         AND observation.verification_job_id = generation.verification_job_id
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = observation.chain_id
         AND canonical.number = observation.block_number
         AND canonical.block_hash = observation.block_hash
        JOIN published_block_stage_results AS published
          ON published.chain_id = generation.chain_id
         AND published.block_hash = generation.observation_block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = generation.observation_stage_version
         AND published.durable_job_id = generation.durable_job_id
         AND published.job_generation = generation.job_generation
         AND published.state = 'complete'
        JOIN verified_contract_proxy_artifacts AS artifact
          ON artifact.verification_job_id = observation.verification_job_id
         AND artifact.chain_id = observation.chain_id
         AND artifact.address = observation.implementation_address
         AND artifact.code_hash = observation.implementation_code_hash
         AND artifact.artifact_kind = 'uups_implementation'
         AND artifact.standard_version = '5.6.1'
         AND artifact.runtime_immutable_address = observation.implementation_address
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
         AND artifact_job.status = 'succeeded'
         AND artifact_job.chain_id = artifact.chain_id
         AND artifact_job.address = artifact.address
         AND artifact_job.code_hash = artifact.code_hash
        JOIN blocks AS artifact_block
          ON artifact_block.chain_id = artifact_job.chain_id
         AND artifact_block.hash = artifact_job.block_hash
        JOIN canonical_blocks AS artifact_canonical
          ON artifact_canonical.chain_id = artifact_block.chain_id
         AND artifact_canonical.number = artifact_block.number
         AND artifact_canonical.block_hash = artifact_block.hash
        CROSS JOIN LATERAL (
            SELECT COALESCE(max(change.block_number), 0::numeric) AS block_number
            FROM transaction_state_changes AS change
            JOIN canonical_blocks AS change_canonical
              ON change_canonical.chain_id = change.chain_id
             AND change_canonical.number = change.block_number
             AND change_canonical.block_hash = change.block_hash
            WHERE change.chain_id = NEW.chain_id
              AND change.address = NEW.implementation_address
              AND change.field_kind = 'code'
              AND change.canonical = TRUE
              AND change.block_number <= NEW.context_block_number
              AND lower(change.before_value) IS DISTINCT FROM lower(change.after_value)
        ) AS code_epoch
        JOIN LATERAL (
            SELECT code.code_hash
            FROM contract_code_observations AS code
            JOIN canonical_blocks AS code_canonical
              ON code_canonical.chain_id = code.chain_id
             AND code_canonical.number = code.block_number
             AND code_canonical.block_hash = code.block_hash
            WHERE code.chain_id = NEW.chain_id
              AND code.address = NEW.implementation_address
              AND code.canonical = TRUE
              AND code.block_number <= NEW.context_block_number
            ORDER BY code.block_number DESC, code.observed_at DESC,
                     code.code_hash DESC
            LIMIT 1
        ) AS current_code ON TRUE
        WHERE generation.id = NEW.uups_generation_id
          AND NEW.proxy_pattern = 'uups'
          AND generation.chain_id = NEW.chain_id
          AND generation.implementation_address = NEW.implementation_address
          AND observation.implementation_code_hash = NEW.implementation_code_hash
          AND observation.standard_version = '5.6.1'
          AND observation.probe_state = 'compatible'
          AND observation.rejection_reason IS NULL
          AND observation.proxiable_uuid = decode(
              '360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc',
              'hex'
          )
          AND observation.upgrade_interface_version = '5.0.0'
          AND observation.canonical = TRUE
          AND observation.block_number >= code_epoch.block_number
          AND observation.block_number <= NEW.context_block_number
          AND artifact.valid_from_block >= code_epoch.block_number
          AND artifact.valid_from_block <= NEW.context_block_number
          AND (verified.valid_to_block IS NULL OR
               verified.valid_to_block >= NEW.context_block_number)
          AND current_code.code_hash = NEW.implementation_code_hash
          AND proxy_interaction_coverage_contains(
              NEW.chain_id,
              observation.block_number, observation.block_hash,
              NEW.context_block_number, NEW.context_block_hash
          )
          AND NOT EXISTS (
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
              WHERE conflict.chain_id = observation.chain_id
                AND conflict.implementation_address = observation.implementation_address
                AND conflict.implementation_code_hash = observation.implementation_code_hash
                AND conflict.stage_version = 2
                AND conflict.canonical = TRUE
                AND conflict.block_number <= NEW.context_block_number
                AND (
                    conflict.block_number > observation.block_number OR (
                        conflict.block_number = observation.block_number
                        AND (
                            (
                                conflict.probe_state || ':' ||
                                COALESCE(conflict.rejection_reason, '')
                            ) IS DISTINCT FROM (
                                observation.probe_state || ':' ||
                                COALESCE(observation.rejection_reason, '')
                            ) OR conflict_generation.id > generation.id
                        )
                    )
                )
          )
    ) THEN
        RAISE EXCEPTION 'UUPS implementation generation is not the latest compatible published evidence';
    END IF;

    IF NEW.management_kind <> 'none' AND NOT EXISTS (
        SELECT 1
        FROM verified_contract_proxy_artifacts AS artifact
        JOIN verified_contracts AS verified
          ON verified.chain_id = artifact.chain_id
         AND verified.address = artifact.address
         AND verified.code_hash = artifact.code_hash
         AND verified.valid_from_block = artifact.valid_from_block
         AND verified.verification_job_id = artifact.verification_job_id
         AND verified.request_digest = artifact.request_digest
        WHERE artifact.chain_id = NEW.chain_id
          AND artifact.address = NEW.management_address
          AND artifact.code_hash = NEW.management_code_hash
          AND artifact.standard_version = '5.6.1'
          AND artifact.artifact_kind = CASE NEW.management_kind
              WHEN 'proxy_admin' THEN 'proxy_admin'
              WHEN 'upgradeable_beacon' THEN 'upgradeable_beacon'
          END
          AND artifact.valid_from_block <= NEW.context_block_number
          AND (verified.valid_to_block IS NULL OR
               verified.valid_to_block >= NEW.context_block_number)
    ) THEN
        RAISE EXCEPTION 'proxy management contract is not authenticated OpenZeppelin 5.6.1';
    END IF;
    RETURN NEW;
END
$$;

CREATE FUNCTION reject_verified_proxy_binding_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'verified proxy bindings are immutable';
END
$$;


-- Sequences

CREATE SEQUENCE proxy_artifact_resolutions_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

CREATE SEQUENCE uups_implementation_observation_generations_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


-- Tables

CREATE TABLE proxy_artifact_resolutions (
    id bigint NOT NULL DEFAULT nextval('proxy_artifact_resolutions_id_seq'::regclass),
    chain_id numeric(78,0) NOT NULL,
    proxy_address bytea NOT NULL,
    observation_block_hash bytea NOT NULL,
    observation_stage_version integer NOT NULL,
    proxy_code_hash bytea NOT NULL,
    proxy_kind text NOT NULL,
    proxy_pattern text NOT NULL,
    standard_version text NOT NULL,
    implementation_address bytea NOT NULL,
    implementation_code_hash bytea NOT NULL,
    admin_address bytea,
    admin_code_hash bytea,
    beacon_address bytea,
    beacon_code_hash bytea,
    proxy_artifact_job_id uuid NOT NULL,
    implementation_artifact_job_id uuid,
    durable_job_id bigint,
    job_generation bigint,
    evidence jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT proxy_artifact_resolutions_check CHECK ((((admin_address IS NULL) AND (admin_code_hash IS NULL)) OR ((octet_length(admin_address) = 20) AND (octet_length(admin_code_hash) = 32)))),
    CONSTRAINT proxy_artifact_resolutions_check1 CHECK ((((beacon_address IS NULL) AND (beacon_code_hash IS NULL)) OR ((octet_length(beacon_address) = 20) AND (octet_length(beacon_code_hash) = 32)))),
    CONSTRAINT proxy_artifact_resolutions_check2 CHECK ((((proxy_pattern = 'transparent'::text) AND (admin_address IS NOT NULL) AND (beacon_address IS NULL)) OR ((proxy_pattern = 'beacon'::text) AND (beacon_address IS NOT NULL) AND (admin_address IS NULL)) OR ((proxy_pattern = ANY (ARRAY['erc1967'::text, 'uups'::text])) AND (admin_address IS NULL) AND (beacon_address IS NULL)))),
    CONSTRAINT proxy_artifact_resolutions_check3 CHECK ((((proxy_pattern = 'uups'::text) AND (implementation_artifact_job_id IS NOT NULL)) OR (proxy_pattern <> 'uups'::text))),
    CONSTRAINT proxy_artifact_resolutions_check4 CHECK ((((durable_job_id IS NULL) AND (job_generation IS NULL)) OR ((durable_job_id IS NOT NULL) AND (job_generation > 0)))),
    CONSTRAINT proxy_artifact_resolutions_evidence_check CHECK ((jsonb_typeof(evidence) = 'object'::text)),
    CONSTRAINT proxy_artifact_resolutions_implementation_address_check CHECK ((octet_length(implementation_address) = 20)),
    CONSTRAINT proxy_artifact_resolutions_implementation_code_hash_check CHECK ((octet_length(implementation_code_hash) = 32)),
    CONSTRAINT proxy_artifact_resolutions_observation_stage_version_check CHECK ((observation_stage_version = 2)),
    CONSTRAINT proxy_artifact_resolutions_proxy_code_hash_check CHECK ((octet_length(proxy_code_hash) = 32)),
    CONSTRAINT proxy_artifact_resolutions_proxy_kind_check CHECK ((proxy_kind = ANY (ARRAY['eip1967'::text, 'beacon'::text]))),
    CONSTRAINT proxy_artifact_resolutions_proxy_pattern_check CHECK ((proxy_pattern = ANY (ARRAY['erc1967'::text, 'transparent'::text, 'uups'::text, 'beacon'::text]))),
    CONSTRAINT proxy_artifact_resolutions_standard_version_check CHECK ((standard_version = '5.6.1'::text)),
    CONSTRAINT proxy_artifact_resolutions_chain_id_proxy_address_observati_key UNIQUE NULLS NOT DISTINCT (chain_id, proxy_address, observation_block_hash, observation_stage_version, durable_job_id, job_generation),
    CONSTRAINT proxy_artifact_resolutions_pkey PRIMARY KEY (id),
    CONSTRAINT proxy_artifact_resolutions_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT proxy_artifact_resolutions_chain_id_proxy_address_observat_fkey FOREIGN KEY (chain_id, proxy_address, observation_block_hash, observation_stage_version) REFERENCES proxy_observations(chain_id, proxy_address, block_hash, stage_version) ON DELETE RESTRICT,
    CONSTRAINT proxy_artifact_resolutions_durable_job_id_fkey FOREIGN KEY (durable_job_id) REFERENCES durable_jobs(id),
    CONSTRAINT proxy_artifact_resolutions_implementation_artifact_job_id_fkey FOREIGN KEY (implementation_artifact_job_id) REFERENCES verified_contract_proxy_artifacts(verification_job_id),
    CONSTRAINT proxy_artifact_resolutions_proxy_artifact_job_id_fkey FOREIGN KEY (proxy_artifact_job_id) REFERENCES verified_contract_proxy_artifacts(verification_job_id)
);

CREATE TABLE uups_implementation_observations (
    chain_id numeric(78,0) NOT NULL,
    implementation_address bytea CONSTRAINT uups_implementation_observation_implementation_address_not_null NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    implementation_code_hash bytea CONSTRAINT uups_implementation_observati_implementation_code_hash_not_null NOT NULL,
    verification_job_id uuid NOT NULL,
    stage_version integer DEFAULT 2 NOT NULL,
    standard_version text NOT NULL,
    probe_state text NOT NULL,
    rejection_reason text,
    proxiable_uuid bytea,
    upgrade_interface_version text,
    canonical boolean NOT NULL,
    observed_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT uups_implementation_observations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT uups_implementation_observations_block_number_check CHECK ((block_number >= (0)::numeric)),
    CONSTRAINT uups_implementation_observations_exact_probe CHECK ((((probe_state = 'compatible'::text) AND (rejection_reason IS NULL) AND (proxiable_uuid = decode('360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc'::text, 'hex'::text)) AND (upgrade_interface_version = '5.0.0'::text)) OR ((probe_state = 'rejected'::text) AND (rejection_reason IS NOT NULL) AND (proxiable_uuid IS NULL) AND (upgrade_interface_version IS NULL)))),
    CONSTRAINT uups_implementation_observations_implementation_address_check CHECK (((octet_length(implementation_address) = 20) AND (implementation_address <> decode(repeat('00'::text, 20), 'hex'::text)))),
    CONSTRAINT uups_implementation_observations_implementation_code_hash_check CHECK ((octet_length(implementation_code_hash) = 32)),
    CONSTRAINT uups_implementation_observations_probe_state_check CHECK ((probe_state = ANY (ARRAY['compatible'::text, 'rejected'::text]))),
    CONSTRAINT uups_implementation_observations_rejection_reason_check CHECK (((rejection_reason IS NULL) OR (rejection_reason = ANY (ARRAY['proxiable_uuid_unavailable'::text, 'proxiable_uuid_invalid'::text, 'upgrade_interface_version_unavailable'::text, 'upgrade_interface_version_invalid'::text])))),
    CONSTRAINT uups_implementation_observations_stage_version_check CHECK ((stage_version = 2)),
    CONSTRAINT uups_implementation_observations_standard_version_check CHECK ((standard_version = '5.6.1'::text)),
    CONSTRAINT uups_implementation_observations_pkey PRIMARY KEY (chain_id, implementation_address, block_hash, stage_version, verification_job_id),
    CONSTRAINT uups_implementation_observati_chain_id_block_number_block__fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash),
    CONSTRAINT uups_implementation_observations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT uups_implementation_observations_verification_job_id_fkey FOREIGN KEY (verification_job_id) REFERENCES verified_contract_proxy_artifacts(verification_job_id)
);

CREATE TABLE uups_implementation_observation_generations (
    id bigint NOT NULL DEFAULT nextval('uups_implementation_observation_generations_id_seq'::regclass),
    chain_id numeric(78,0) NOT NULL,
    implementation_address bytea CONSTRAINT uups_implementation_observatio_implementation_address_not_null1 NOT NULL,
    observation_block_hash bytea CONSTRAINT uups_implementation_observation_observation_block_hash_not_null NOT NULL,
    observation_stage_version integer CONSTRAINT uups_implementation_observat_observation_stage_version_not_null NOT NULL,
    verification_job_id uuid CONSTRAINT uups_implementation_observation_ge_verification_job_id_not_null NOT NULL,
    durable_job_id bigint CONSTRAINT uups_implementation_observation_generat_durable_job_id_not_null NOT NULL,
    job_generation bigint CONSTRAINT uups_implementation_observation_generat_job_generation_not_null NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT uups_implementation_observation_ge_implementation_address_check CHECK ((octet_length(implementation_address) = 20)),
    CONSTRAINT uups_implementation_observation_ge_observation_block_hash_check CHECK ((octet_length(observation_block_hash) = 32)),
    CONSTRAINT uups_implementation_observation_generation_job_generation_check CHECK ((job_generation > 0)),
    CONSTRAINT uups_implementation_observation_observation_stage_version_check CHECK ((observation_stage_version = 2)),
    CONSTRAINT uups_implementation_observati_chain_id_implementation_addre_key UNIQUE (chain_id, implementation_address, observation_block_hash, observation_stage_version, verification_job_id, durable_job_id, job_generation),
    CONSTRAINT uups_implementation_observation_generations_pkey PRIMARY KEY (id),
    CONSTRAINT uups_implementation_observati_chain_id_implementation_addr_fkey FOREIGN KEY (chain_id, implementation_address, observation_block_hash, observation_stage_version, verification_job_id) REFERENCES uups_implementation_observations(chain_id, implementation_address, block_hash, stage_version, verification_job_id) ON DELETE RESTRICT,
    CONSTRAINT uups_implementation_observation_generations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT uups_implementation_observation_generations_durable_job_id_fkey FOREIGN KEY (durable_job_id) REFERENCES durable_jobs(id)
);

CREATE TABLE verified_proxy_bindings (
    chain_id numeric(78,0) NOT NULL,
    proxy_address bytea NOT NULL,
    proxy_code_hash bytea NOT NULL,
    observation_block_number numeric(78,0) NOT NULL,
    observation_block_hash bytea NOT NULL,
    observation_stage_version integer NOT NULL,
    proxy_kind text NOT NULL,
    proxy_pattern text NOT NULL,
    standard_version text,
    implementation_address bytea NOT NULL,
    implementation_code_hash bytea NOT NULL,
    admin_address bytea,
    admin_code_hash bytea,
    beacon_address bytea,
    beacon_code_hash bytea,
    management_kind text NOT NULL,
    management_address bytea,
    management_code_hash bytea,
    observation_generation_id bigint NOT NULL,
    artifact_resolution_id bigint,
    beacon_generation_id bigint,
    context_block_number numeric(78,0) NOT NULL,
    context_block_hash bytea NOT NULL,
    verification_job_id uuid NOT NULL,
    request_digest bytea NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    uups_generation_id bigint,
    CONSTRAINT verified_proxy_bindings_check CHECK ((((admin_address IS NULL) AND (admin_code_hash IS NULL)) OR ((octet_length(admin_address) = 20) AND (octet_length(admin_code_hash) = 32)))),
    CONSTRAINT verified_proxy_bindings_check1 CHECK ((((beacon_address IS NULL) AND (beacon_code_hash IS NULL)) OR ((octet_length(beacon_address) = 20) AND (octet_length(beacon_code_hash) = 32)))),
    CONSTRAINT verified_proxy_bindings_check2 CHECK ((((management_kind = 'none'::text) AND (management_address IS NULL) AND (management_code_hash IS NULL)) OR ((management_kind <> 'none'::text) AND (octet_length(management_address) = 20) AND (octet_length(management_code_hash) = 32)))),
    CONSTRAINT verified_proxy_bindings_check3 CHECK ((((proxy_pattern = 'clone'::text) AND (artifact_resolution_id IS NULL) AND (standard_version IS NULL)) OR ((proxy_pattern <> 'clone'::text) AND (artifact_resolution_id IS NOT NULL) AND (standard_version = '5.6.1'::text)))),
    CONSTRAINT verified_proxy_bindings_check4 CHECK ((((proxy_pattern = 'beacon'::text) AND (beacon_generation_id IS NOT NULL)) OR ((proxy_pattern <> 'beacon'::text) AND (beacon_generation_id IS NULL)))),
    CONSTRAINT verified_proxy_bindings_check5 CHECK ((context_block_number >= observation_block_number)),
    CONSTRAINT verified_proxy_bindings_context_block_hash_check CHECK ((octet_length(context_block_hash) = 32)),
    CONSTRAINT verified_proxy_bindings_implementation_address_check CHECK ((octet_length(implementation_address) = 20)),
    CONSTRAINT verified_proxy_bindings_implementation_code_hash_check CHECK ((octet_length(implementation_code_hash) = 32)),
    CONSTRAINT verified_proxy_bindings_management_kind_check CHECK ((management_kind = ANY (ARRAY['none'::text, 'proxy_admin'::text, 'upgradeable_beacon'::text]))),
    CONSTRAINT verified_proxy_bindings_management_semantics CHECK ((((proxy_pattern = 'transparent'::text) AND (proxy_kind = 'eip1967'::text) AND (admin_address IS NOT NULL) AND (admin_code_hash IS NOT NULL) AND (beacon_address IS NULL) AND (beacon_code_hash IS NULL) AND (management_kind = 'proxy_admin'::text) AND (NOT (management_address IS DISTINCT FROM admin_address)) AND (NOT (management_code_hash IS DISTINCT FROM admin_code_hash))) OR ((proxy_pattern = 'beacon'::text) AND (proxy_kind = 'beacon'::text) AND (admin_address IS NULL) AND (admin_code_hash IS NULL) AND (beacon_address IS NOT NULL) AND (beacon_code_hash IS NOT NULL) AND (management_kind = 'upgradeable_beacon'::text) AND (NOT (management_address IS DISTINCT FROM beacon_address)) AND (NOT (management_code_hash IS DISTINCT FROM beacon_code_hash))) OR ((proxy_pattern = 'clone'::text) AND (proxy_kind = ANY (ARRAY['eip1167'::text, 'cwia'::text])) AND (admin_address IS NULL) AND (admin_code_hash IS NULL) AND (beacon_address IS NULL) AND (beacon_code_hash IS NULL) AND (management_kind = 'none'::text)) OR ((proxy_pattern = ANY (ARRAY['erc1967'::text, 'uups'::text])) AND (proxy_kind = 'eip1967'::text) AND (admin_address IS NULL) AND (admin_code_hash IS NULL) AND (beacon_address IS NULL) AND (beacon_code_hash IS NULL) AND (management_kind = 'none'::text)))),
    CONSTRAINT verified_proxy_bindings_observation_block_hash_check CHECK ((octet_length(observation_block_hash) = 32)),
    CONSTRAINT verified_proxy_bindings_observation_block_number_check CHECK ((observation_block_number >= (0)::numeric)),
    CONSTRAINT verified_proxy_bindings_observation_stage_version_check CHECK ((observation_stage_version = 2)),
    CONSTRAINT verified_proxy_bindings_proxy_address_check CHECK ((octet_length(proxy_address) = 20)),
    CONSTRAINT verified_proxy_bindings_proxy_code_hash_check CHECK ((octet_length(proxy_code_hash) = 32)),
    CONSTRAINT verified_proxy_bindings_proxy_kind_check CHECK ((proxy_kind = ANY (ARRAY['eip1167'::text, 'cwia'::text, 'eip1967'::text, 'beacon'::text]))),
    CONSTRAINT verified_proxy_bindings_proxy_pattern_check CHECK ((proxy_pattern = ANY (ARRAY['clone'::text, 'erc1967'::text, 'transparent'::text, 'uups'::text, 'beacon'::text]))),
    CONSTRAINT verified_proxy_bindings_request_digest_check CHECK ((octet_length(request_digest) = 32)),
    CONSTRAINT verified_proxy_bindings_standard_version_check CHECK (((standard_version IS NULL) OR (standard_version = '5.6.1'::text))),
    CONSTRAINT verified_proxy_bindings_uups_generation_shape CHECK (((proxy_pattern = 'uups'::text) = (uups_generation_id IS NOT NULL))),
    CONSTRAINT verified_proxy_bindings_pkey PRIMARY KEY (verification_job_id),
    CONSTRAINT verified_proxy_bindings_artifact_resolution_id_fkey FOREIGN KEY (artifact_resolution_id) REFERENCES proxy_artifact_resolutions(id),
    CONSTRAINT verified_proxy_bindings_beacon_generation_id_fkey FOREIGN KEY (beacon_generation_id) REFERENCES beacon_observation_generations(id),
    CONSTRAINT verified_proxy_bindings_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT verified_proxy_bindings_observation_fk FOREIGN KEY (chain_id, proxy_address, observation_block_hash, observation_stage_version) REFERENCES proxy_observations(chain_id, proxy_address, block_hash, stage_version) ON DELETE RESTRICT,
    CONSTRAINT verified_proxy_bindings_observation_generation_id_fkey FOREIGN KEY (observation_generation_id) REFERENCES proxy_observation_generations(id),
    CONSTRAINT verified_proxy_bindings_result_fk FOREIGN KEY (verification_job_id, request_digest) REFERENCES verification_results(job_id, request_digest) ON DELETE RESTRICT,
    CONSTRAINT verified_proxy_bindings_uups_generation_id_fkey FOREIGN KEY (uups_generation_id) REFERENCES uups_implementation_observation_generations(id)
);


-- Dependency completion

ALTER SEQUENCE proxy_artifact_resolutions_id_seq OWNED BY proxy_artifact_resolutions.id;

ALTER SEQUENCE uups_implementation_observation_generations_id_seq OWNED BY uups_implementation_observation_generations.id;


-- Indexes

CREATE INDEX proxy_artifact_resolutions_beacon_idx ON proxy_artifact_resolutions USING btree (chain_id, beacon_address, observation_block_hash, id DESC) WHERE (beacon_address IS NOT NULL);

CREATE INDEX proxy_artifact_resolutions_current_idx ON proxy_artifact_resolutions USING btree (chain_id, proxy_address, id DESC);

CREATE INDEX uups_implementation_observation_generations_job_idx ON uups_implementation_observation_generations USING btree (durable_job_id, job_generation, id);

CREATE INDEX uups_implementation_observations_current_idx ON uups_implementation_observations USING btree (chain_id, implementation_address, implementation_code_hash, block_number DESC, block_hash DESC) WHERE (canonical AND (probe_state = 'compatible'::text));

CREATE INDEX uups_implementation_observations_latest_idx ON uups_implementation_observations USING btree (chain_id, implementation_address, implementation_code_hash, block_number DESC, block_hash DESC) WHERE canonical;

CREATE INDEX uups_implementation_observations_verification_idx ON uups_implementation_observations USING btree (verification_job_id, chain_id, block_hash);

CREATE INDEX verified_proxy_bindings_current_idx ON verified_proxy_bindings USING btree (chain_id, proxy_address, observation_block_number DESC);

CREATE INDEX verified_proxy_bindings_uups_generation_idx ON verified_proxy_bindings USING btree (uups_generation_id) WHERE (uups_generation_id IS NOT NULL);


-- Triggers

CREATE TRIGGER proxy_artifact_resolutions_immutable BEFORE DELETE OR UPDATE ON proxy_artifact_resolutions FOR EACH ROW EXECUTE FUNCTION reject_proxy_evidence_mutation();

CREATE TRIGGER proxy_artifact_resolutions_source_guard BEFORE INSERT ON proxy_artifact_resolutions FOR EACH ROW EXECUTE FUNCTION enforce_proxy_artifact_resolution();

CREATE TRIGGER uups_implementation_observation_generations_immutable BEFORE DELETE OR UPDATE ON uups_implementation_observation_generations FOR EACH ROW EXECUTE FUNCTION reject_proxy_evidence_mutation();

CREATE TRIGGER uups_implementation_observation_generations_source_guard BEFORE INSERT ON uups_implementation_observation_generations FOR EACH ROW EXECUTE FUNCTION enforce_proxy_observation_generation();

CREATE TRIGGER uups_implementation_observations_immutable BEFORE DELETE OR UPDATE ON uups_implementation_observations FOR EACH ROW EXECUTE FUNCTION enforce_uups_implementation_observation_mutation();

CREATE TRIGGER uups_implementation_observations_source_guard BEFORE INSERT ON uups_implementation_observations FOR EACH ROW EXECUTE FUNCTION enforce_uups_implementation_observation();

CREATE CONSTRAINT TRIGGER uups_implementation_observations_witness_guard AFTER INSERT ON uups_implementation_observations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION enforce_uups_implementation_observation_witness();

CREATE TRIGGER verified_proxy_bindings_immutable BEFORE DELETE OR UPDATE ON verified_proxy_bindings FOR EACH ROW EXECUTE FUNCTION reject_verified_proxy_binding_mutation();

CREATE TRIGGER verified_proxy_bindings_source_guard BEFORE INSERT ON verified_proxy_bindings FOR EACH ROW EXECUTE FUNCTION enforce_verified_proxy_binding();
