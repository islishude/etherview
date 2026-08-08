-- UUPS compatibility is shared implementation evidence, not a synthetic
-- per-proxy artifact resolution. Bind an effective UUPS proxy to the exact
-- published probe generation that promoted its underlying ERC-1967
-- resolution, so rejected probes, reorgs, and code epochs invalidate reuse.

DROP TRIGGER IF EXISTS verified_proxy_bindings_source_guard
    ON verified_proxy_bindings;
DROP TRIGGER IF EXISTS verified_proxy_bindings_immutable
    ON verified_proxy_bindings;

-- Older builds could publish UUPS as a per-proxy resolution and therefore
-- have no shared probe-generation identity. Those bindings cannot be safely
-- upgraded in place and must be rebound through the new request digest.
DELETE FROM verified_proxy_bindings WHERE proxy_pattern = 'uups';

ALTER TABLE verified_proxy_bindings
    ADD COLUMN uups_generation_id BIGINT
        REFERENCES uups_implementation_observation_generations(id);

ALTER TABLE verified_proxy_bindings
    ADD CONSTRAINT verified_proxy_bindings_uups_generation_shape CHECK (
        (proxy_pattern = 'uups') = (uups_generation_id IS NOT NULL)
    );

CREATE INDEX verified_proxy_bindings_uups_generation_idx
    ON verified_proxy_bindings (uups_generation_id)
    WHERE uups_generation_id IS NOT NULL;

-- Code epochs are read on every proxy binding/current-state lookup. Busy
-- contracts may have millions of balance/storage rows, none of which may be
-- scanned to find the last code transition.
CREATE INDEX transaction_state_changes_code_epoch_idx
    ON transaction_state_changes (
        chain_id, address, block_number DESC
    ) INCLUDE (block_hash)
    WHERE canonical AND field_kind = 'code';

CREATE INDEX proxy_detection_evidence_negative_shadow_idx
    ON proxy_detection_evidence (
        chain_id, address, candidate_kind, code_hash,
        block_number DESC, block_hash DESC
    ) WHERE canonical AND stage_version = 2;

CREATE OR REPLACE FUNCTION enforce_verified_proxy_binding()
RETURNS trigger
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

-- Forward-upgrade the 0032 guard as well: no caller may recreate the old
-- synthetic per-proxy UUPS resolution shape after this migration.
CREATE OR REPLACE FUNCTION enforce_proxy_artifact_resolution()
RETURNS trigger
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

CREATE TRIGGER verified_proxy_bindings_immutable
BEFORE UPDATE OR DELETE ON verified_proxy_bindings
FOR EACH ROW EXECUTE FUNCTION reject_verified_proxy_binding_mutation();

CREATE TRIGGER verified_proxy_bindings_source_guard
BEFORE INSERT ON verified_proxy_bindings
FOR EACH ROW EXECUTE FUNCTION enforce_verified_proxy_binding();

DO $migration$
DECLARE
    migration_schema TEXT := current_schema();
BEGIN
    IF migration_schema IS NULL THEN
        RAISE EXCEPTION 'UUPS proxy binding migration requires a current schema';
    END IF;
    EXECUTE format(
        'ALTER FUNCTION %I.enforce_verified_proxy_binding() SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
    EXECUTE format(
        'ALTER FUNCTION %I.enforce_proxy_artifact_resolution() SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
END
$migration$;
