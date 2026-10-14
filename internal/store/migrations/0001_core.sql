-- Fresh-database baseline. Earlier development schemas must be recreated.
-- Keep object identities and final constraints stable; Git retains migration history.
-- Functions and tables have circular dependencies. PostgreSQL validates function
-- bodies when first called; this transaction-local setting permits their creation.
SET LOCAL check_function_bodies = false;

-- Function: check_vyper_runtime_binding()

CREATE FUNCTION check_vyper_runtime_binding() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF NEW.executor_kind = 'node_vyper_wasm_v1' AND NOT EXISTS (
    SELECT 1 FROM compiler_catalog_entries e,
       jsonb_array_elements(e.vyper_runtimes) r,
       jsonb_each_text(r->'executor_digests') executor
    WHERE e.generation_id = NEW.catalog_generation_id AND e.language = 'vyper'
      AND e.version = NEW.compiler_version AND e.artifact_sha256 = NEW.compiler_digest
      AND executor.value = encode(NEW.executor_digest, 'hex')
      AND r->>'platform' = 'emscripten-wasm32'
      AND r->>'protocol' = 'etherview-vyper-wasm-package-v1'
      AND r->>'recipe_sha256' = NEW.request->>'vyper_lock_digest'
      AND e.artifact_url = 'embedded:vyper/' || (r->>'recipe_sha256')
 ) THEN RAISE EXCEPTION 'Vyper executor does not match application lock binding'; END IF;
 RETURN NEW;
END $$;

-- Function: enforce_enrichment_terminal_publication()

CREATE FUNCTION enforce_enrichment_terminal_publication() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    current_job durable_jobs%ROWTYPE;
    exact_result block_stage_results%ROWTYPE;
BEGIN
    SELECT * INTO current_job FROM durable_jobs WHERE id = NEW.id;
    IF NOT FOUND
       OR current_job.kind <> 'enrichment'
       OR current_job.stage NOT IN ('proxy', 'abi', 'token', 'stats', 'trace', 'native_transfer')
       OR current_job.status NOT IN ('succeeded', 'failed') THEN
        RETURN NULL;
    END IF;
    IF COALESCE(current_setting('etherview.enrichment_publication_protocol', true), '') <> '2' THEN
        RAISE EXCEPTION 'derived enrichment terminal transition requires publication protocol 2'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF current_job.claimed_generation <> current_job.completed_generation
       OR current_job.requested_generation <> current_job.completed_generation
       OR current_job.leased_generation IS NOT NULL
       OR current_job.lease_token IS NOT NULL
       OR current_job.lease_expires_at IS NOT NULL
       OR current_job.result IS NULL THEN
        RAISE EXCEPTION 'derived enrichment terminal job lacks exact completed generation'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;

    SELECT result.* INTO exact_result
    FROM block_stage_results AS result
    WHERE result.durable_job_id = current_job.id
      AND result.job_generation = current_job.completed_generation
      AND result.chain_id = current_job.chain_id
      AND result.stage = current_job.stage
      AND result.stage_version = current_job.stage_version
      AND current_job.payload->>'block_hash' = '0x' || encode(result.block_hash, 'hex')
      AND current_job.payload->>'block_number' = result.block_number::text;
    IF NOT FOUND
       OR current_job.result->>'state' IS DISTINCT FROM exact_result.state
       OR COALESCE(current_job.result->'details', '{}'::jsonb) IS DISTINCT FROM exact_result.details
       OR (current_job.result->>'error') IS DISTINCT FROM exact_result.last_error THEN
        RAISE EXCEPTION 'derived enrichment terminal job lacks exact stage result'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM durable_stage_publications AS publication
        WHERE publication.job_id = current_job.id
          AND publication.job_generation = current_job.completed_generation
          AND publication.chain_id = current_job.chain_id
          AND publication.block_number = exact_result.block_number
          AND publication.block_hash = exact_result.block_hash
          AND publication.stage = current_job.stage
          AND publication.stage_version = current_job.stage_version
          AND publication.state = exact_result.state
          AND publication.details = exact_result.details
          AND publication.last_error IS NOT DISTINCT FROM exact_result.last_error
    ) THEN
        RAISE EXCEPTION 'derived enrichment terminal job lacks durable publication proof'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;

    IF current_job.status = 'succeeded' THEN
        IF exact_result.state <> 'complete'
           OR NOT EXISTS (
               SELECT 1 FROM block_journals AS journal
               WHERE journal.durable_job_id = current_job.id
                 AND journal.job_generation = current_job.completed_generation
                 AND journal.chain_id = current_job.chain_id
                 AND journal.block_hash = exact_result.block_hash
                 AND journal.stage = current_job.stage || '@' || current_job.stage_version::text
                 AND journal.sequence = 1
           ) THEN
            RAISE EXCEPTION 'successful derived enrichment job lacks exact journal'
                USING ERRCODE = 'integrity_constraint_violation';
        END IF;
    ELSIF exact_result.state NOT IN ('failed', 'unavailable')
          OR EXISTS (
              SELECT 1 FROM block_journals AS journal
              WHERE journal.durable_job_id = current_job.id
                AND journal.job_generation = current_job.completed_generation
          ) THEN
        RAISE EXCEPTION 'terminal derived enrichment failure has invalid result or journal'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NULL;
END
$$;

-- Function: enforce_erc4337_canonical_user_op_hash()

CREATE FUNCTION enforce_erc4337_canonical_user_op_hash() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.canonical THEN
        PERFORM pg_advisory_xact_lock(hashtextextended(
            'etherview:erc4337-userop:' || NEW.chain_id::text || ':' ||
            encode(NEW.configuration_digest, 'hex') || ':' ||
            encode(NEW.user_op_hash, 'hex'), 0
        ));
        IF EXISTS (
            SELECT 1
            FROM erc4337_user_operations AS existing
            WHERE existing.chain_id = NEW.chain_id
              AND existing.configuration_digest = NEW.configuration_digest
              AND existing.user_op_hash = NEW.user_op_hash
              AND existing.canonical
              AND (
                  existing.block_number, existing.block_hash,
                  existing.transaction_hash, existing.operation_index
              ) IS DISTINCT FROM (
                  NEW.block_number, NEW.block_hash,
                  NEW.transaction_hash, NEW.operation_index
              )
        ) THEN
            RAISE EXCEPTION 'canonical userOpHash already belongs to another inclusion';
        END IF;
    END IF;
    RETURN NEW;
END
$$;

-- Function: enforce_proxy_artifact_resolution()

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

-- Function: enforce_proxy_detection_generation()

CREATE FUNCTION enforce_proxy_detection_generation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.durable_job_id IS NOT NULL AND NOT EXISTS (
        SELECT 1
        FROM durable_jobs AS job
        WHERE job.id = NEW.durable_job_id
          AND job.chain_id = NEW.chain_id
          AND job.stage = 'proxy'
          AND job.stage_version = NEW.stage_version
          AND job.payload->>'block_hash' =
              '0x' || encode(NEW.block_hash, 'hex')
          AND job.leased_generation = NEW.job_generation
          AND job.status = 'leased'
    ) THEN
        RAISE EXCEPTION 'proxy detection evidence is not from the active proxy@2 lease';
    END IF;
    RETURN NEW;
END
$$;

-- Function: enforce_proxy_observation_generation()

CREATE FUNCTION enforce_proxy_observation_generation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.durable_job_id IS NOT NULL AND NOT EXISTS (
        SELECT 1
        FROM durable_jobs AS job
        WHERE job.id = NEW.durable_job_id
          AND job.chain_id = NEW.chain_id
          AND job.stage = 'proxy'
          AND job.stage_version = NEW.observation_stage_version
          AND job.payload->>'block_hash' =
              '0x' || encode(NEW.observation_block_hash, 'hex')
          AND job.leased_generation = NEW.job_generation
          AND job.status = 'leased'
    ) THEN
        RAISE EXCEPTION 'proxy observation generation is not the active proxy@2 lease';
    END IF;
    RETURN NEW;
END
$$;

-- Function: enforce_unbound_verification_job_insert()

CREATE FUNCTION enforce_unbound_verification_job_insert() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.kind = 'derived' THEN
        IF NEW.status <> 'succeeded' OR
           NOT EXISTS (
               SELECT 1
               FROM verification_compilation_units AS unit
               WHERE unit.id = (NEW.request->>'compilation_id')::uuid
                 AND unit.language = NEW.language
                 AND unit.compiler_version = NEW.compiler_version
                 AND unit.compiler_platform = NEW.compiler_platform
                 AND unit.catalog_generation_id = NEW.catalog_generation_id
                 AND unit.compiler_sha256 = NEW.compiler_digest
                 AND unit.executor_kind = NEW.executor_kind
                 AND unit.execution_policy = NEW.execution_policy
                 AND unit.executor_sha256 = NEW.executor_digest
           ) THEN
            RAISE EXCEPTION 'derived verification job lacks authenticated compilation provenance';
        END IF;
        RETURN NEW;
    END IF;
    IF NEW.kind NOT IN ('sourcify', 'sourcify_from_etherscan', 'proxy') AND
       (
           NEW.compiler_platform IS NOT NULL OR
           NEW.catalog_generation_id IS NOT NULL OR
           NEW.compiler_digest IS NOT NULL OR
           NEW.executor_kind IS NOT NULL OR
           NEW.execution_policy IS NOT NULL OR
           NEW.executor_digest IS NOT NULL
       ) THEN
        RAISE EXCEPTION 'new verification jobs must bind compiler provenance under an active lease';
    END IF;
    RETURN NEW;
END
$$;

-- Function: enforce_uups_implementation_observation()

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

-- Function: enforce_uups_implementation_observation_mutation()

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

-- Function: enforce_uups_implementation_observation_witness()

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

-- Function: enforce_verification_compiler_provenance()

CREATE FUNCTION enforce_verification_compiler_provenance() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.compiler_platform IS NOT DISTINCT FROM OLD.compiler_platform AND
       NEW.catalog_generation_id IS NOT DISTINCT FROM OLD.catalog_generation_id AND
       NEW.compiler_digest IS NOT DISTINCT FROM OLD.compiler_digest AND
       NEW.executor_kind IS NOT DISTINCT FROM OLD.executor_kind AND
       NEW.execution_policy IS NOT DISTINCT FROM OLD.execution_policy AND
       NEW.executor_digest IS NOT DISTINCT FROM OLD.executor_digest THEN
        RETURN NEW;
    END IF;

    IF OLD.compiler_platform IS NULL AND
       OLD.catalog_generation_id IS NULL AND
       OLD.compiler_digest IS NULL AND
       OLD.executor_kind IS NULL AND
       OLD.execution_policy IS NULL AND
       OLD.executor_digest IS NULL AND
       (
           (
               OLD.language IN ('solidity', 'yul') AND
               NEW.compiler_platform = 'emscripten-wasm32' AND
               NEW.catalog_generation_id IS NOT NULL AND
               octet_length(NEW.compiler_digest) = 32 AND
               NEW.executor_kind = 'node_solcjs_v1' AND
               NEW.execution_policy = 'trusted_subprocess' AND
               octet_length(NEW.executor_digest) = 32
           ) OR (
               OLD.language = 'geas' AND
               NEW.compiler_platform = 'go-module' AND
               NEW.catalog_generation_id IS NULL AND
               octet_length(NEW.compiler_digest) = 32 AND
               NEW.executor_kind = 'etherview_geas_v1' AND
               NEW.execution_policy = 'trusted_subprocess' AND
               octet_length(NEW.executor_digest) = 32
           ) OR (
               OLD.language = 'vyper' AND NEW.compiler_platform = 'python-wheel' AND
               NEW.catalog_generation_id IS NOT NULL AND octet_length(NEW.compiler_digest) = 32 AND
               NEW.executor_kind = 'node_vyper_wasm_v1' AND
               NEW.execution_policy = 'trusted_subprocess' AND
               octet_length(NEW.executor_digest) = 32
           )
       ) AND
       OLD.status = 'running' AND NEW.status = 'running' AND
       OLD.lease_token IS NOT NULL AND
       OLD.lease_expires_at > clock_timestamp() AND
       NEW.lease_token IS NOT DISTINCT FROM OLD.lease_token AND
       NEW.leased_by IS NOT DISTINCT FROM OLD.leased_by AND
       NEW.lease_expires_at IS NOT DISTINCT FROM OLD.lease_expires_at THEN
        RETURN NEW;
    END IF;

    RAISE EXCEPTION 'verification compiler provenance is immutable after binding';
END
$$;

-- Function: enforce_verified_contract_proxy_artifact()

CREATE FUNCTION enforce_verified_contract_proxy_artifact() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM verification_results AS result
        JOIN verification_jobs AS job ON job.id = result.job_id
        JOIN verified_contracts AS verified
          ON verified.verification_job_id = result.job_id
         AND verified.request_digest = result.request_digest
        JOIN blocks AS target_block
          ON target_block.chain_id = job.chain_id
         AND target_block.hash = job.block_hash
        JOIN contract_code_observations AS observation
          ON observation.chain_id = job.chain_id
         AND observation.address = job.address
         AND observation.code_hash = job.code_hash
         AND observation.block_hash = job.block_hash
         AND observation.block_number = target_block.number
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = target_block.chain_id
         AND canonical.number = target_block.number
         AND canonical.block_hash = target_block.hash
        WHERE result.job_id = NEW.verification_job_id
          AND result.request_digest = NEW.request_digest
          AND result.outcome_kind = 'verification_success'
          AND result.proxy_artifact_kind = NEW.artifact_kind
          AND result.proxy_standard_version = NEW.standard_version
          AND result.proxy_runtime_immutable_address IS NOT DISTINCT FROM
              NEW.runtime_immutable_address
          AND result.proxy_source_manifest_sha256 = NEW.source_manifest_sha256
          AND job.kind IN ('address', 'derived')
          AND job.status = 'succeeded'
          AND job.chain_id = NEW.chain_id
          AND job.address = NEW.address
          AND job.code_hash = NEW.code_hash
          AND target_block.number = NEW.valid_from_block
          AND observation.canonical = TRUE
          AND verified.chain_id = NEW.chain_id
          AND verified.address = NEW.address
          AND verified.code_hash = NEW.code_hash
          AND verified.valid_from_block = NEW.valid_from_block
          AND (
              NEW.artifact_kind <> 'uups_implementation' OR
              (
                  result.proxy_runtime_immutable_address = job.address AND
                  NEW.runtime_immutable_address = job.address
              )
          )
    ) THEN
        RAISE EXCEPTION 'proxy artifact disagrees with verified contract publication';
    END IF;
    RETURN NEW;
END
$$;

-- Function: enforce_verified_proxy_binding()

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

-- Function: enforce_verifier_v2_publication()

CREATE FUNCTION enforce_verifier_v2_publication() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM verification_results AS result
        JOIN verification_jobs AS job ON job.id = result.job_id
        WHERE result.job_id = NEW.verification_job_id
          AND result.request_digest = NEW.request_digest
          AND result.outcome_kind = 'verification_success'
          AND job.kind IN ('address', 'derived')
          AND job.chain_id = NEW.chain_id
          AND job.address = NEW.address
          AND job.code_hash = NEW.code_hash
          AND result.file_name = NEW.file_name
          AND result.contract_name = NEW.contract_name
          AND result.language = NEW.language
          AND result.compiler_version = NEW.compiler_version
          AND result.match_type = NEW.match_type
          AND result.abi IS NOT DISTINCT FROM NEW.abi
          AND result.sources = NEW.sources
          AND result.settings = NEW.settings
          AND result.compilation_artifacts = NEW.compilation_artifacts
          AND result.creation_code_artifacts = NEW.creation_code_artifacts
          AND result.runtime_code_artifacts = NEW.runtime_code_artifacts
          AND result.constructor_arguments IS NOT DISTINCT FROM NEW.constructor_arguments
          AND result.libraries = NEW.libraries
          AND result.is_blueprint = NEW.is_blueprint
          AND COALESCE(result.outcome #>> '{runtime_match,match_type}' IN ('full', 'partial'), FALSE)
    ) THEN
        RAISE EXCEPTION 'verifier v2 publication disagrees with its immutable runtime result';
    END IF;
    RETURN NEW;
END
$$;

-- Function: enforce_verifier_v2_result_job()

CREATE FUNCTION enforce_verifier_v2_result_job() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    target_job UUID;
BEGIN
    IF TG_TABLE_NAME = 'verification_results' THEN
        target_job := NEW.job_id;
    ELSE
        target_job := NEW.id;
    END IF;
    IF EXISTS (
        SELECT 1
        FROM verification_results AS result
        JOIN verification_jobs AS job ON job.id = result.job_id
        WHERE result.job_id = target_job
          AND (
            job.status IS DISTINCT FROM 'succeeded' OR
            job.request_digest IS DISTINCT FROM result.request_digest OR
            job.outcome_kind IS DISTINCT FROM result.outcome_kind OR
            job.outcome IS DISTINCT FROM result.outcome OR
            job.error_code IS NOT NULL OR
            (
                result.outcome_kind = 'verification_success' AND
                (
                    (result.language = 'vyper' AND (
                        result.file_name IS DISTINCT FROM job.request->>'target_file' OR
                        result.sources IS DISTINCT FROM (
                            (job.request #> '{standard_json,sources}') ||
                            COALESCE(job.request #> '{standard_json,interfaces}', '{}'::jsonb)) OR
                        result.settings IS DISTINCT FROM job.request #> '{standard_json,settings}'
                    )) OR
                    job.language IS DISTINCT FROM result.language OR
                    job.compiler_version IS DISTINCT FROM result.compiler_version OR
                    (
                        result.language = 'geas' AND
                        (
                            job.kind <> 'address' OR
                            jsonb_typeof(job.request->'geas') IS DISTINCT FROM 'object' OR
                            result.file_name IS DISTINCT FROM
                                job.request #>> '{geas,runtime_entrypoint}' OR
                            result.contract_name IS DISTINCT FROM
                                job.request->>'contract_name_hint' OR
                            result.settings->>'runtime_entrypoint' IS DISTINCT FROM
                                job.request #>> '{geas,runtime_entrypoint}' OR
                            result.settings->>'creation_entrypoint' IS DISTINCT FROM
                                job.request #>> '{geas,creation_entrypoint}' OR
                            NOT (result.sources ? (job.request #>> '{geas,runtime_entrypoint}')) OR
                            (
                                job.request #>> '{geas,creation_entrypoint}' IS NOT NULL AND
                                NOT (result.sources ? (job.request #>> '{geas,creation_entrypoint}'))
                            ) OR
                            EXISTS (
                                SELECT 1
                                FROM jsonb_each(result.sources) AS source(name, value)
                                WHERE job.request #>> ARRAY[
                                    'geas', 'sources', source.name
                                ] IS DISTINCT FROM source.value->>'content'
                            )
                        )
                    )
                )
            )
          )
    ) OR EXISTS (
        SELECT 1
        FROM verification_jobs AS job
        WHERE job.id = target_job AND job.status = 'succeeded'
          AND NOT EXISTS (
            SELECT 1 FROM verification_results AS result
            WHERE result.job_id = job.id
              AND result.request_digest = job.request_digest
              AND result.outcome_kind = job.outcome_kind
              AND result.outcome = job.outcome
          )
    ) THEN
        RAISE EXCEPTION 'verifier v2 job and immutable result disagree';
    END IF;
    RETURN NEW;
END
$$;

-- Function: enqueue_derived_forward_event_after_stage_publication()

CREATE FUNCTION enqueue_derived_forward_event_after_stage_publication() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.state = 'complete' AND
       ((NEW.stage = 'trace' AND NEW.stage_version = 4) OR
        (NEW.stage = 'proxy' AND NEW.stage_version = 2)) THEN
        INSERT INTO derived_verification_forward_blocks (
            chain_id, block_number, block_hash, source_stage,
            source_job_id, source_generation
        ) VALUES (
            NEW.chain_id, NEW.block_number, NEW.block_hash, NEW.stage,
            NEW.job_id, NEW.job_generation
        )
        ON CONFLICT (source_job_id, source_generation) DO NOTHING;
    END IF;
    RETURN NEW;
END
$$;

-- Function: erc4337_add_covered_block(numeric, bytea, numeric, bytea, bigint, bigint)

CREATE FUNCTION erc4337_add_covered_block(target_chain_id numeric, target_configuration_digest bytea, target_block_number numeric, target_block_hash bytea, target_durable_job_id bigint, target_job_generation bigint) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE
    left_range erc4337_coverage_ranges%ROWTYPE;
    right_range erc4337_coverage_ranges%ROWTYPE;
BEGIN
    IF octet_length(target_block_hash) <> 32 OR
       target_durable_job_id <= 0 OR target_job_generation <= 0 THEN
        RAISE EXCEPTION 'ERC-4337 coverage insertion target is invalid';
    END IF;
    PERFORM erc4337_remove_covered_block(
        target_chain_id, target_configuration_digest, target_block_number
    );
    INSERT INTO erc4337_covered_blocks (
        chain_id, configuration_digest, block_number, block_hash,
        durable_job_id, job_generation
    ) VALUES (
        target_chain_id, target_configuration_digest, target_block_number,
        target_block_hash, target_durable_job_id, target_job_generation
    );

    SELECT * INTO left_range
    FROM erc4337_coverage_ranges
    WHERE chain_id = target_chain_id
      AND configuration_digest = target_configuration_digest
      AND end_block = target_block_number - 1;
    SELECT * INTO right_range
    FROM erc4337_coverage_ranges
    WHERE chain_id = target_chain_id
      AND configuration_digest = target_configuration_digest
      AND start_block = target_block_number + 1;

    IF left_range.start_block IS NOT NULL AND right_range.start_block IS NOT NULL THEN
        DELETE FROM erc4337_coverage_ranges
        WHERE chain_id = target_chain_id
          AND configuration_digest = target_configuration_digest
          AND start_block = right_range.start_block;
        UPDATE erc4337_coverage_ranges
        SET end_block = right_range.end_block,
            end_block_hash = right_range.end_block_hash,
            updated_at = clock_timestamp()
        WHERE chain_id = target_chain_id
          AND configuration_digest = target_configuration_digest
          AND start_block = left_range.start_block;
    ELSIF left_range.start_block IS NOT NULL THEN
        UPDATE erc4337_coverage_ranges
        SET end_block = target_block_number,
            end_block_hash = target_block_hash,
            updated_at = clock_timestamp()
        WHERE chain_id = target_chain_id
          AND configuration_digest = target_configuration_digest
          AND start_block = left_range.start_block;
    ELSIF right_range.start_block IS NOT NULL THEN
        DELETE FROM erc4337_coverage_ranges
        WHERE chain_id = target_chain_id
          AND configuration_digest = target_configuration_digest
          AND start_block = right_range.start_block;
        INSERT INTO erc4337_coverage_ranges (
            chain_id, configuration_digest, start_block, start_block_hash,
            end_block, end_block_hash
        ) VALUES (
            target_chain_id, target_configuration_digest,
            target_block_number, target_block_hash,
            right_range.end_block, right_range.end_block_hash
        );
    ELSE
        INSERT INTO erc4337_coverage_ranges (
            chain_id, configuration_digest, start_block, start_block_hash,
            end_block, end_block_hash
        ) VALUES (
            target_chain_id, target_configuration_digest,
            target_block_number, target_block_hash,
            target_block_number, target_block_hash
        );
    END IF;
END
$$;

-- Function: erc4337_remove_block_coverage(numeric, numeric, bytea)

CREATE FUNCTION erc4337_remove_block_coverage(target_chain_id numeric, target_block_number numeric, target_block_hash bytea) RETURNS integer
    LANGUAGE plpgsql
    AS $$
DECLARE
    digest_row RECORD;
    removed INTEGER := 0;
BEGIN
    FOR digest_row IN
        SELECT configuration_digest
        FROM erc4337_covered_blocks
        WHERE chain_id = target_chain_id
          AND block_number = target_block_number
          AND block_hash = target_block_hash
    LOOP
        PERFORM erc4337_remove_covered_block(
            target_chain_id, digest_row.configuration_digest, target_block_number
        );
        removed := removed + 1;
    END LOOP;
    RETURN removed;
END
$$;

-- Function: erc4337_remove_covered_block(numeric, bytea, numeric)

CREATE FUNCTION erc4337_remove_covered_block(target_chain_id numeric, target_configuration_digest bytea, target_block_number numeric) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE
    containing erc4337_coverage_ranges%ROWTYPE;
    next_hash BYTEA;
    previous_hash BYTEA;
BEGIN
    IF target_chain_id IS NULL OR target_configuration_digest IS NULL OR
       octet_length(target_configuration_digest) <> 32 OR
       target_block_number IS NULL OR target_block_number < 0 THEN
        RAISE EXCEPTION 'ERC-4337 coverage removal target is invalid';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended(
        'etherview:erc4337-coverage:' || target_chain_id::text || ':' ||
        encode(target_configuration_digest, 'hex'), 0
    ));
    SELECT * INTO containing
    FROM erc4337_coverage_ranges
    WHERE chain_id = target_chain_id
      AND configuration_digest = target_configuration_digest
      AND start_block <= target_block_number
      AND end_block >= target_block_number
    ORDER BY start_block DESC
    LIMIT 1;

    DELETE FROM erc4337_covered_blocks
    WHERE chain_id = target_chain_id
      AND configuration_digest = target_configuration_digest
      AND block_number = target_block_number;
    IF containing.start_block IS NULL THEN
        RETURN;
    END IF;

    IF containing.start_block = target_block_number AND containing.end_block = target_block_number THEN
        DELETE FROM erc4337_coverage_ranges
        WHERE chain_id = target_chain_id
          AND configuration_digest = target_configuration_digest
          AND start_block = containing.start_block;
    ELSIF containing.start_block = target_block_number THEN
        SELECT block_hash INTO next_hash
        FROM erc4337_covered_blocks
        WHERE chain_id = target_chain_id
          AND configuration_digest = target_configuration_digest
          AND block_number = target_block_number + 1;
        DELETE FROM erc4337_coverage_ranges
        WHERE chain_id = target_chain_id
          AND configuration_digest = target_configuration_digest
          AND start_block = containing.start_block;
        INSERT INTO erc4337_coverage_ranges (
            chain_id, configuration_digest, start_block, start_block_hash,
            end_block, end_block_hash
        ) VALUES (
            target_chain_id, target_configuration_digest,
            target_block_number + 1, next_hash,
            containing.end_block, containing.end_block_hash
        );
    ELSIF containing.end_block = target_block_number THEN
        SELECT block_hash INTO previous_hash
        FROM erc4337_covered_blocks
        WHERE chain_id = target_chain_id
          AND configuration_digest = target_configuration_digest
          AND block_number = target_block_number - 1;
        UPDATE erc4337_coverage_ranges
        SET end_block = target_block_number - 1,
            end_block_hash = previous_hash,
            updated_at = clock_timestamp()
        WHERE chain_id = target_chain_id
          AND configuration_digest = target_configuration_digest
          AND start_block = containing.start_block;
    ELSE
        SELECT block_hash INTO previous_hash
        FROM erc4337_covered_blocks
        WHERE chain_id = target_chain_id
          AND configuration_digest = target_configuration_digest
          AND block_number = target_block_number - 1;
        SELECT block_hash INTO next_hash
        FROM erc4337_covered_blocks
        WHERE chain_id = target_chain_id
          AND configuration_digest = target_configuration_digest
          AND block_number = target_block_number + 1;
        UPDATE erc4337_coverage_ranges
        SET end_block = target_block_number - 1,
            end_block_hash = previous_hash,
            updated_at = clock_timestamp()
        WHERE chain_id = target_chain_id
          AND configuration_digest = target_configuration_digest
          AND start_block = containing.start_block;
        INSERT INTO erc4337_coverage_ranges (
            chain_id, configuration_digest, start_block, start_block_hash,
            end_block, end_block_hash
        ) VALUES (
            target_chain_id, target_configuration_digest,
            target_block_number + 1, next_hash,
            containing.end_block, containing.end_block_hash
        );
    END IF;
END
$$;

-- Function: erc4337_remove_detached_coverage()

CREATE FUNCTION erc4337_remove_detached_coverage() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    digest_row RECORD;
BEGIN
    IF TG_OP = 'DELETE' OR NEW.block_hash IS DISTINCT FROM OLD.block_hash THEN
        FOR digest_row IN
            SELECT configuration_digest
            FROM erc4337_covered_blocks
            WHERE chain_id = OLD.chain_id
              AND block_number = OLD.number
              AND block_hash = OLD.block_hash
        LOOP
            PERFORM erc4337_remove_covered_block(
                OLD.chain_id, digest_row.configuration_digest, OLD.number
            );
        END LOOP;
    END IF;
    RETURN NULL;
END
$$;

-- Function: etherview_guard_completed_genesis_import()

CREATE FUNCTION etherview_guard_completed_genesis_import() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path TO 'pg_catalog'
    AS $$
BEGIN
    IF OLD.state = 'complete' AND NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'completed genesis state import is immutable'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END;
$$;

-- Function: etherview_guard_erc20_balance_reconciliation()

CREATE FUNCTION etherview_guard_erc20_balance_reconciliation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'exact ERC-20 balance observations are immutable'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END
$$;

-- Function: etherview_guard_erc20_holder_fact()

CREATE FUNCTION etherview_guard_erc20_holder_fact() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF (to_jsonb(NEW) - 'canonical') IS DISTINCT FROM (to_jsonb(OLD) - 'canonical') THEN
        RAISE EXCEPTION 'exact ERC-20 holder facts are immutable'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END
$$;

-- Function: etherview_guard_exact_nft_metadata()

CREATE FUNCTION etherview_guard_exact_nft_metadata() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path TO 'pg_catalog'
    AS $$
BEGIN
    IF OLD.resource_kind <> 'nft' OR OLD.observed_block_hash IS NULL THEN
        RETURN NEW;
    END IF;
    IF ROW(NEW.chain_id, NEW.resource_kind, NEW.resource_key, NEW.identity_hash,
           NEW.token_address, NEW.token_id, NEW.observed_block_number,
           NEW.observed_block_hash, NEW.source_uri)
       IS DISTINCT FROM
       ROW(OLD.chain_id, OLD.resource_kind, OLD.resource_key, OLD.identity_hash,
           OLD.token_address, OLD.token_id, OLD.observed_block_number,
           OLD.observed_block_hash, OLD.source_uri) THEN
        RAISE EXCEPTION 'exact NFT metadata identity is immutable'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF OLD.state <> 'pending' AND
       ROW(NEW.state, NEW.resolved_uri, NEW.media_type, NEW.content_hash,
           NEW.document, NEW.content_size, NEW.attempt_count,
           NEW.last_error_code, NEW.last_error, NEW.fetched_at, NEW.terminal_at)
       IS DISTINCT FROM
       ROW(OLD.state, OLD.resolved_uri, OLD.media_type, OLD.content_hash,
           OLD.document, OLD.content_size, OLD.attempt_count,
           OLD.last_error_code, OLD.last_error, OLD.fetched_at, OLD.terminal_at) THEN
        RAISE EXCEPTION 'terminal exact NFT metadata outcome is immutable'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END;
$$;

-- Function: etherview_guard_genesis_account_observation()

CREATE FUNCTION etherview_guard_genesis_account_observation() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path TO 'pg_catalog'
    AS $$
BEGIN
    IF NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'exact genesis account observation is immutable'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END;
$$;

-- Function: etherview_guard_nft_metadata_source()

CREATE FUNCTION etherview_guard_nft_metadata_source() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path TO 'pg_catalog'
    AS $$
BEGIN
    IF NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'exact NFT metadata source observation is immutable'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END;
$$;

-- Function: etherview_guard_nft_metadata_update_observation()

CREATE FUNCTION etherview_guard_nft_metadata_update_observation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'exact NFT metadata update observation is immutable'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END
$$;

-- Function: guard_billing_account_entry_mutation()

CREATE FUNCTION guard_billing_account_entry_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'billing account entries are append-only';
END;
$$;

-- Function: guard_billing_payment_event_mutation()

CREATE FUNCTION guard_billing_payment_event_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'billing payment events are append-only';
END;
$$;

-- Function: guard_billing_payment_mutation()

CREATE FUNCTION guard_billing_payment_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'billing payments are durable audit records';
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.chain_id IS DISTINCT FROM OLD.chain_id
        OR NEW.fingerprint IS DISTINCT FROM OLD.fingerprint
        OR NEW.reservation_owner IS DISTINCT FROM OLD.reservation_owner
        OR NEW.method IS DISTINCT FROM OLD.method
        OR NEW.operation IS DISTINCT FROM OLD.operation
        OR NEW.resource_digest IS DISTINCT FROM OLD.resource_digest
        OR NEW.requirement_digest IS DISTINCT FROM OLD.requirement_digest
        OR NEW.protocol_version IS DISTINCT FROM OLD.protocol_version
        OR NEW.scheme IS DISTINCT FROM OLD.scheme
        OR NEW.network IS DISTINCT FROM OLD.network
        OR NEW.asset IS DISTINCT FROM OLD.asset
        OR NEW.amount_atomic IS DISTINCT FROM OLD.amount_atomic
        OR NEW.recipient IS DISTINCT FROM OLD.recipient
        OR NEW.facilitator_digest IS DISTINCT FROM OLD.facilitator_digest
        OR NEW.purpose IS DISTINCT FROM OLD.purpose
        OR NEW.asset_transfer_method IS DISTINCT FROM OLD.asset_transfer_method
        OR NEW.payment_flow IS DISTINCT FROM OLD.payment_flow
        OR NEW.fingerprint_version IS DISTINCT FROM OLD.fingerprint_version
        OR NEW.topup_intent_id IS DISTINCT FROM OLD.topup_intent_id
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR NEW.reservation_expires_at IS DISTINCT FROM OLD.reservation_expires_at
    THEN
        RAISE EXCEPTION 'billing payment identity is immutable';
    END IF;

    IF OLD.payer IS NOT NULL AND NEW.payer IS DISTINCT FROM OLD.payer
        OR OLD.user_id IS NOT NULL AND NEW.user_id IS DISTINCT FROM OLD.user_id
        OR OLD.api_key_prefix IS NOT NULL AND NEW.api_key_prefix IS DISTINCT FROM OLD.api_key_prefix
        OR OLD.handler_started_at IS NOT NULL AND NEW.handler_started_at IS DISTINCT FROM OLD.handler_started_at
        OR OLD.verified_at IS NOT NULL AND NEW.verified_at IS DISTINCT FROM OLD.verified_at
        OR OLD.settling_at IS NOT NULL AND NEW.settling_at IS DISTINCT FROM OLD.settling_at
        OR OLD.transaction_hash IS NOT NULL AND NEW.transaction_hash IS DISTINCT FROM OLD.transaction_hash
    THEN
        RAISE EXCEPTION 'billing payment bound facts are immutable';
    END IF;

    IF OLD.state IN ('settled', 'failed', 'expired') THEN
        RAISE EXCEPTION 'terminal billing payments are immutable';
    END IF;
    IF NOT (
        (OLD.state = 'reserved' AND NEW.state IN ('reserved', 'verified', 'failed', 'expired'))
        OR (OLD.state = 'verified' AND NEW.state IN ('verified', 'settling', 'failed', 'expired'))
        OR (OLD.state = 'settling' AND NEW.state IN ('settling', 'settled', 'failed'))
    ) THEN
        RAISE EXCEPTION 'invalid billing payment state transition';
    END IF;
    RETURN NEW;
END;
$$;

-- Function: guard_billing_topup_intent_mutation()

CREATE FUNCTION guard_billing_topup_intent_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'billing top-up intents are durable audit records';
    END IF;
    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.user_id IS DISTINCT FROM OLD.user_id
        OR NEW.chain_id IS DISTINCT FROM OLD.chain_id
        OR NEW.network IS DISTINCT FROM OLD.network
        OR NEW.asset IS DISTINCT FROM OLD.asset
        OR NEW.amount_atomic IS DISTINCT FROM OLD.amount_atomic
        OR NEW.recipient IS DISTINCT FROM OLD.recipient
        OR NEW.payer IS DISTINCT FROM OLD.payer
        OR NEW.expires_at IS DISTINCT FROM OLD.expires_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
    THEN
        RAISE EXCEPTION 'billing top-up intent identity is immutable';
    END IF;
    IF OLD.transaction_hash IS NOT NULL
        AND NEW.transaction_hash IS DISTINCT FROM OLD.transaction_hash
    THEN
        RAISE EXCEPTION 'billing top-up settlement hash is immutable';
    END IF;
    IF NEW.updated_at < OLD.updated_at THEN
        RAISE EXCEPTION 'billing top-up intent time cannot move backward';
    END IF;
    IF OLD.state IN ('credited', 'failed', 'expired') THEN
        RAISE EXCEPTION 'terminal billing top-up intents are immutable';
    END IF;
    IF NOT (
        (OLD.state = 'open' AND NEW.state IN ('processing', 'expired'))
        OR (OLD.state = 'processing' AND NEW.state IN ('open', 'settling', 'expired'))
        OR (OLD.state = 'settling' AND NEW.state IN ('settling', 'open', 'credited', 'failed', 'expired'))
    ) THEN
        RAISE EXCEPTION 'invalid billing top-up intent transition';
    END IF;
    RETURN NEW;
END;
$$;

-- Function: guard_billing_usage_charge_mutation()

CREATE FUNCTION guard_billing_usage_charge_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'billing usage charges are durable audit records';
    END IF;
    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.reservation_owner IS DISTINCT FROM OLD.reservation_owner
        OR NEW.user_id IS DISTINCT FROM OLD.user_id
        OR NEW.api_key_prefix IS DISTINCT FROM OLD.api_key_prefix
        OR NEW.chain_id IS DISTINCT FROM OLD.chain_id
        OR NEW.network IS DISTINCT FROM OLD.network
        OR NEW.asset IS DISTINCT FROM OLD.asset
        OR NEW.method IS DISTINCT FROM OLD.method
        OR NEW.operation IS DISTINCT FROM OLD.operation
        OR NEW.resource_digest IS DISTINCT FROM OLD.resource_digest
        OR NEW.amount_atomic IS DISTINCT FROM OLD.amount_atomic
        OR NEW.reservation_expires_at IS DISTINCT FROM OLD.reservation_expires_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
    THEN
        RAISE EXCEPTION 'billing usage charge identity is immutable';
    END IF;
    IF OLD.state <> 'reserved' OR NEW.state NOT IN ('committed', 'released', 'expired') THEN
        RAISE EXCEPTION 'invalid billing usage charge transition';
    END IF;
    RETURN NEW;
END;
$$;

-- Function: invalidate_watch_block(numeric, numeric, bytea)

CREATE FUNCTION invalidate_watch_block(p_chain numeric, p_number numeric, p_hash bytea) RETURNS void
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM address_watches WHERE chain_id = p_chain AND deleted_at IS NULL AND enabled AND start_number < p_number)
       AND NOT EXISTS (SELECT 1 FROM watch_notifications WHERE chain_id = p_chain AND block_hash = p_hash) THEN
        RETURN;
    END IF;
    INSERT INTO watch_notification_work(chain_id, block_number, block_hash)
    VALUES(p_chain, p_number, p_hash)
    ON CONFLICT(chain_id, block_hash) DO UPDATE SET
        generation = watch_notification_work.generation + 1,
        after_watch = '00000000-0000-0000-0000-000000000000', after_source = '';
END
$$;

-- Function: mark_canonical_chart_rollup_dirty()

CREATE FUNCTION mark_canonical_chart_rollup_dirty() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    source_chain NUMERIC(78, 0);
    source_number NUMERIC(78, 0);
    source_hash BYTEA;
    source_timestamp NUMERIC(78, 0);
    hour_start TIMESTAMPTZ;
BEGIN
    IF TG_OP = 'DELETE' THEN
        source_chain := OLD.chain_id;
        source_number := OLD.number;
        source_hash := OLD.block_hash;
    ELSE
        source_chain := NEW.chain_id;
        source_number := NEW.number;
        source_hash := NEW.block_hash;
    END IF;
    SELECT block.timestamp INTO source_timestamp
    FROM blocks AS block
    WHERE block.chain_id = source_chain
      AND block.number = source_number
      AND block.hash = source_hash;
    IF source_timestamp IS NULL OR source_timestamp > 253402300799 THEN
        IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
        RETURN NEW;
    END IF;
    hour_start := date_trunc('hour', to_timestamp(source_timestamp::double precision), 'UTC');
    INSERT INTO chart_rollup_dirty_hours (chain_id, bucket_start)
    VALUES (source_chain, hour_start)
    ON CONFLICT (chain_id, bucket_start) DO UPDATE SET
        generation = chart_rollup_dirty_hours.generation + 1,
        attempts = 0,
        next_attempt_at = now(),
        dirtied_at = now();
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END
$$;

-- Function: mark_chart_rollup_dirty()

CREATE FUNCTION mark_chart_rollup_dirty() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    source_chain NUMERIC(78, 0);
    source_number NUMERIC(78, 0);
    source_hash BYTEA;
    source_timestamp NUMERIC(78, 0);
    hour_start TIMESTAMPTZ;
BEGIN
    IF TG_OP = 'DELETE' THEN
        source_chain := OLD.chain_id;
        source_number := OLD.block_number;
        source_hash := OLD.block_hash;
    ELSE
        source_chain := NEW.chain_id;
        source_number := NEW.block_number;
        source_hash := NEW.block_hash;
    END IF;
    SELECT block.timestamp INTO source_timestamp
    FROM blocks AS block
    WHERE block.chain_id = source_chain
      AND block.number = source_number
      AND block.hash = source_hash;
    IF source_timestamp IS NULL OR source_timestamp > 253402300799 THEN
        IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
        RETURN NEW;
    END IF;
    hour_start := date_trunc('hour', to_timestamp(source_timestamp::double precision), 'UTC');
    INSERT INTO chart_rollup_dirty_hours (chain_id, bucket_start)
    VALUES (source_chain, hour_start)
    ON CONFLICT (chain_id, bucket_start) DO UPDATE SET
        generation = chart_rollup_dirty_hours.generation + 1,
        attempts = 0,
        next_attempt_at = now(),
        dirtied_at = now();
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END
$$;

-- Function: mark_chart_rollup_dirty_for_block(numeric, numeric, bytea)

CREATE FUNCTION mark_chart_rollup_dirty_for_block(source_chain numeric, source_number numeric, source_hash bytea) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE
    source_timestamp NUMERIC(78, 0);
    hour_start TIMESTAMPTZ;
BEGIN
    SELECT block.timestamp INTO source_timestamp
    FROM blocks AS block
    WHERE block.chain_id = source_chain
      AND block.number = source_number
      AND block.hash = source_hash;
    IF source_timestamp IS NULL OR source_timestamp > 253402300799 THEN
        RETURN;
    END IF;
    hour_start := date_trunc('hour', to_timestamp(source_timestamp::double precision), 'UTC');
    INSERT INTO chart_rollup_dirty_hours (chain_id, bucket_start)
    VALUES (source_chain, hour_start)
    ON CONFLICT (chain_id, bucket_start) DO UPDATE SET
        generation = chart_rollup_dirty_hours.generation + 1,
        attempts = 0,
        next_attempt_at = now(),
        dirtied_at = now();
END
$$;

-- Function: mark_chart_rollup_dirty_from_job()

CREATE FUNCTION mark_chart_rollup_dirty_from_job() RETURNS trigger
    LANGUAGE plpgsql
    AS $_$
DECLARE
    source_number NUMERIC(78, 0);
    source_hash BYTEA;
BEGIN
    IF NEW.kind <> 'enrichment'
       OR NOT (
           (NEW.stage = 'stats' AND NEW.stage_version = 4)
           OR (NEW.stage = 'token' AND NEW.stage_version = 2)
       )
       OR NEW.payload->>'block_number' !~ '^(0|[1-9][0-9]*)$'
       OR NEW.payload->>'block_hash' !~ '^0x[0-9a-f]{64}$' THEN
        RETURN NEW;
    END IF;
    source_number := (NEW.payload->>'block_number')::numeric;
    source_hash := decode(substr(NEW.payload->>'block_hash', 3), 'hex');
    PERFORM mark_chart_rollup_dirty_for_block(NEW.chain_id, source_number, source_hash);
    RETURN NEW;
END
$_$;

-- Function: native_transfer_canonical_coverage()

CREATE FUNCTION native_transfer_canonical_coverage() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP='DELETE' THEN
 PERFORM refresh_native_transfer_coverage(OLD.chain_id,OLD.number); RETURN OLD;
 END IF;
 PERFORM refresh_native_transfer_coverage(NEW.chain_id,NEW.number); RETURN NEW;
END $$;

-- Function: native_transfer_job_coverage()

CREATE FUNCTION native_transfer_job_coverage() RETURNS trigger
    LANGUAGE plpgsql
    AS $_$
BEGIN
 -- Heartbeats run while holding the lease's in-process guard. They must not
 -- wait for the coverage lock held by a publisher before its final guarded
 -- CAS. Only changes to the publication view's job inputs can affect coverage;
 -- lease expiry matters by nullness, not by the renewed timestamp value.
 IF TG_OP='UPDATE' AND ROW(
  NEW.kind,NEW.chain_id,NEW.stage,NEW.stage_version,NEW.payload,NEW.status,
  NEW.requested_generation,NEW.claimed_generation,NEW.completed_generation,
  NEW.leased_generation,NEW.leased_by,NEW.lease_token,
  NEW.lease_expires_at IS NULL,NEW.result
 ) IS NOT DISTINCT FROM ROW(
  OLD.kind,OLD.chain_id,OLD.stage,OLD.stage_version,OLD.payload,OLD.status,
  OLD.requested_generation,OLD.claimed_generation,OLD.completed_generation,
  OLD.leased_generation,OLD.leased_by,OLD.lease_token,
  OLD.lease_expires_at IS NULL,OLD.result
 ) THEN RETURN NEW; END IF;
 IF NEW.kind='enrichment' AND NEW.stage='native_transfer' AND NEW.stage_version=1
 AND NEW.payload->>'block_number' ~ '^(0|[1-9][0-9]*)$' THEN
 PERFORM refresh_native_transfer_coverage(NEW.chain_id,(NEW.payload->>'block_number')::numeric);
 END IF;
 RETURN NEW;
END $_$;

-- Function: native_transfer_outbox_coverage()

CREATE FUNCTION native_transfer_outbox_coverage() RETURNS trigger
    LANGUAGE plpgsql
    AS $_$
DECLARE number NUMERIC; chain NUMERIC; key TEXT;
BEGIN
 IF TG_OP='DELETE' THEN chain:=OLD.chain_id; key:=OLD.message_key;
 ELSE chain:=NEW.chain_id; key:=NEW.message_key; END IF;
 IF key !~ '^0x[0-9a-f]{64}$' THEN
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
 END IF;
 SELECT c.number INTO number FROM canonical_blocks c
 WHERE c.chain_id=chain AND c.block_hash=decode(substr(key,3),'hex');
 IF number IS NOT NULL THEN PERFORM refresh_native_transfer_coverage(chain,number); END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $_$;

-- Function: native_transfer_result_coverage()

CREATE FUNCTION native_transfer_result_coverage() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.stage='native_transfer' THEN PERFORM refresh_native_transfer_coverage(OLD.chain_id,OLD.block_number); END IF;
  RETURN OLD;
 END IF;
 IF NEW.stage='native_transfer' THEN PERFORM refresh_native_transfer_coverage(NEW.chain_id,NEW.block_number); END IF;
 RETURN NEW;
END $$;

-- Function: proxy_interaction_coverage_contains(numeric, numeric, bytea, numeric, bytea)

CREATE FUNCTION proxy_interaction_coverage_contains(target_chain_id numeric, target_start_block numeric, target_start_hash bytea, target_end_block numeric, target_end_hash bytea) RETURNS boolean
    LANGUAGE sql STABLE STRICT PARALLEL SAFE
    AS $$
    SELECT EXISTS (
        SELECT 1
        FROM (
            SELECT candidate.*
            FROM proxy_interaction_coverage_ranges AS candidate
            WHERE candidate.chain_id = target_chain_id
              AND candidate.start_block <= target_start_block
            ORDER BY candidate.start_block DESC
            LIMIT 1
        ) AS coverage
        JOIN proxy_interaction_covered_blocks AS required_start
          ON required_start.chain_id = target_chain_id
         AND required_start.block_number = target_start_block
         AND required_start.block_hash = target_start_hash
        JOIN proxy_interaction_covered_blocks AS required_end
          ON required_end.chain_id = target_chain_id
         AND required_end.block_number = target_end_block
         AND required_end.block_hash = target_end_hash
        JOIN proxy_interaction_covered_blocks AS range_start
          ON range_start.chain_id = coverage.chain_id
         AND range_start.block_number = coverage.start_block
         AND range_start.block_hash = coverage.start_block_hash
        JOIN proxy_interaction_covered_blocks AS range_end
          ON range_end.chain_id = coverage.chain_id
         AND range_end.block_number = coverage.end_block
         AND range_end.block_hash = coverage.end_block_hash
        WHERE coverage.end_block >= target_end_block
          AND target_start_block <= target_end_block
    )
$$;

-- Function: prune_search_catalog(numeric, bigint)

CREATE FUNCTION prune_search_catalog(requested_chain_id numeric, requested_retention_generations bigint DEFAULT 100000) RETURNS bigint
    LANGUAGE plpgsql
    AS $$
DECLARE
    cleanup_generation BIGINT;
    cleanup_min_generation BIGINT;
BEGIN
    IF requested_retention_generations < 1000 OR requested_retention_generations > 10000000 THEN
        RAISE EXCEPTION 'search catalog retention must be between 1000 and 10000000 generations';
    END IF;
    INSERT INTO search_catalog_generations (chain_id)
    VALUES (requested_chain_id)
    ON CONFLICT (chain_id) DO NOTHING;
    UPDATE search_catalog_generations
    SET generation = generation + 1,
        retention_generations = requested_retention_generations,
        updated_at = now()
    WHERE chain_id = requested_chain_id
    RETURNING generation INTO cleanup_generation;

    WITH ranked AS (
        SELECT document.id,
               row_number() OVER (
                   PARTITION BY document.source_kind, document.logical_identity
                   ORDER BY document.block_number DESC NULLS LAST,
                            document.valid_from_generation DESC,
                            document.id DESC
               ) AS position
        FROM search_catalog_documents AS document
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = document.chain_id
         AND canonical.number = document.block_number
         AND canonical.block_hash = document.block_hash
        JOIN chain_finality AS finality
          ON finality.chain_id = document.chain_id
         AND finality.finalized_number IS NOT NULL
         AND document.block_number <= finality.finalized_number
        WHERE document.chain_id = requested_chain_id
          AND document.valid_to_generation IS NULL
          AND document.source_kind IN ('name', 'token', 'code')
          AND document.source_canonical IS TRUE
    )
    UPDATE search_catalog_documents AS document
    SET valid_to_generation = cleanup_generation
    FROM ranked
    WHERE document.id = ranked.id AND ranked.position > 1;

    cleanup_min_generation := GREATEST(0, cleanup_generation - requested_retention_generations);
    UPDATE search_catalog_generations
    SET min_generation = GREATEST(min_generation, cleanup_min_generation), updated_at = now()
    WHERE chain_id = requested_chain_id;

    DELETE FROM search_catalog_documents
    WHERE chain_id = requested_chain_id
      AND valid_to_generation IS NOT NULL
      AND valid_to_generation <= cleanup_min_generation;
    RETURN cleanup_min_generation;
END
$$;

-- Function: record_ens_name_search_document()

CREATE FUNCTION record_ens_name_search_document() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    next_generation BIGINT;
    local_number NUMERIC(78, 0);
    local_hash BYTEA;
    local_canonical BOOLEAN;
BEGIN
    IF NEW.direction <> 'forward' THEN
        RETURN NEW;
    END IF;

    INSERT INTO search_catalog_generations (chain_id)
    VALUES (NEW.chain_id)
    ON CONFLICT (chain_id) DO NOTHING;
    UPDATE search_catalog_generations
    SET generation = generation + 1, updated_at = now()
    WHERE chain_id = NEW.chain_id
    RETURNING generation INTO next_generation;

    UPDATE search_catalog_documents
    SET valid_to_generation = next_generation
    WHERE chain_id = NEW.chain_id
      AND source_kind = 'name'
      AND logical_identity = lower(NEW.name)
      AND valid_to_generation IS NULL;

    IF NEW.outcome = 'not_found' THEN
        RETURN NEW;
    END IF;

    IF NEW.source = 'custom_ens' THEN
        SELECT source.custom_block_number, source.custom_block_hash,
               EXISTS (
                   SELECT 1 FROM canonical_blocks AS canonical
                   WHERE canonical.chain_id = source.chain_id
                     AND canonical.number = source.custom_block_number
                     AND canonical.block_hash = source.custom_block_hash
               )
        INTO local_number, local_hash, local_canonical
        FROM ens_resolution_generations AS source
        WHERE source.id = NEW.generation_id;
    ELSE
        local_canonical := TRUE;
    END IF;

    INSERT INTO search_catalog_documents (
        chain_id, source_kind, source_identity, logical_identity,
        valid_from_generation, result_kind, result_key, result_label,
        exact_terms, partial_terms, block_number, block_hash, target_address,
        source_canonical, name_observation_id, name_source
    ) VALUES (
        NEW.chain_id, 'name', NEW.id::text, lower(NEW.name), next_generation,
        'address', '0x' || encode(NEW.address, 'hex'), NEW.name,
        ARRAY[lower(NEW.name)], ARRAY[lower(NEW.name)],
        local_number, local_hash, NEW.address, local_canonical,
        NEW.id, NEW.source
    );
    RETURN NEW;
END
$$;

-- Function: record_proxy_history_job_epoch()

CREATE FUNCTION record_proxy_history_job_epoch() RETURNS trigger
    LANGUAGE plpgsql
    AS $_$
DECLARE
    block_number_text TEXT;
    block_hash_text TEXT;
BEGIN
    IF NEW.kind <> 'enrichment' OR NEW.stage <> 'proxy' OR
       NEW.stage_version <> 2 OR
       (TG_OP = 'UPDATE' AND
        NEW.requested_generation = OLD.requested_generation) THEN
        RETURN NEW;
    END IF;
    block_number_text := NEW.payload->>'block_number';
    block_hash_text := NEW.payload->>'block_hash';
    IF block_number_text IS NULL OR block_number_text !~ '^[0-9]+$' OR
       block_hash_text IS NULL OR block_hash_text !~ '^0x[0-9a-fA-F]{64}$' THEN
        RAISE EXCEPTION 'proxy history epoch requires an exact block identity';
    END IF;
    INSERT INTO proxy_history_epochs (
        chain_id, block_number, block_hash, durable_job_id,
        requested_generation, phase
    ) VALUES (
        NEW.chain_id, block_number_text::numeric,
        decode(substr(block_hash_text, 3), 'hex'), NEW.id,
        NEW.requested_generation, 'requested'
    ) ON CONFLICT (durable_job_id, requested_generation, phase) DO NOTHING;
    RETURN NEW;
END
$_$;

-- Function: record_proxy_history_publication_epoch()

CREATE FUNCTION record_proxy_history_publication_epoch() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.stage = 'proxy' AND NEW.stage_version = 2 AND
       NEW.durable_job_id IS NOT NULL AND NEW.job_generation IS NOT NULL THEN
        INSERT INTO proxy_history_epochs (
            chain_id, block_number, block_hash, durable_job_id,
            requested_generation, phase
        ) VALUES (
            NEW.chain_id, NEW.block_number, NEW.block_hash,
            NEW.durable_job_id, NEW.job_generation, 'published'
        ) ON CONFLICT (durable_job_id, requested_generation, phase) DO NOTHING;
    END IF;
    RETURN NEW;
END
$$;

-- Function: record_search_catalog_document()

CREATE FUNCTION record_search_catalog_document() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    source_chain NUMERIC(78, 0);
    old_identity TEXT;
    new_identity TEXT;
    logical_identity_value TEXT;
    next_generation BIGINT;
BEGIN
    IF TG_OP = 'UPDATE' AND OLD.chain_id IS DISTINCT FROM NEW.chain_id THEN
        RAISE EXCEPTION 'search catalog source chain_id is immutable';
    END IF;
    -- Conditional name upserts deliberately perform a no-op UPDATE to make an
    -- identical concurrent observation visible to the losing statement. Do
    -- not create a catalog generation for that idempotent write.
    IF TG_OP = 'UPDATE' AND OLD IS NOT DISTINCT FROM NEW THEN
        RETURN NEW;
    END IF;
    IF TG_OP = 'DELETE' THEN
        source_chain := OLD.chain_id;
    ELSE
        source_chain := NEW.chain_id;
    END IF;

    IF TG_TABLE_NAME = 'operator_labels' THEN
        IF TG_OP <> 'INSERT' THEN old_identity := OLD.id::text; END IF;
        IF TG_OP <> 'DELETE' THEN
            new_identity := NEW.id::text;
            logical_identity_value := NEW.id::text;
        END IF;
    ELSIF TG_TABLE_NAME = 'name_records' THEN
        IF TG_OP <> 'INSERT' THEN
            old_identity := jsonb_build_array(encode(OLD.registry, 'hex'), OLD.name, encode(OLD.block_hash, 'hex'))::text;
        END IF;
        IF TG_OP <> 'DELETE' THEN
            new_identity := jsonb_build_array(encode(NEW.registry, 'hex'), NEW.name, encode(NEW.block_hash, 'hex'))::text;
            logical_identity_value := jsonb_build_array(encode(NEW.registry, 'hex'), lower(NEW.name))::text;
        END IF;
    ELSIF TG_TABLE_NAME = 'token_contracts' THEN
        IF TG_OP <> 'INSERT' THEN
            old_identity := jsonb_build_array(encode(OLD.address, 'hex'), encode(OLD.code_hash, 'hex'), encode(OLD.observed_block_hash, 'hex'))::text;
        END IF;
        IF TG_OP <> 'DELETE' THEN
            new_identity := jsonb_build_array(encode(NEW.address, 'hex'), encode(NEW.code_hash, 'hex'), encode(NEW.observed_block_hash, 'hex'))::text;
            logical_identity_value := encode(NEW.address, 'hex');
        END IF;
    ELSIF TG_TABLE_NAME = 'verified_contracts' THEN
        IF TG_OP <> 'INSERT' THEN
            old_identity := jsonb_build_array(encode(OLD.address, 'hex'), encode(OLD.code_hash, 'hex'), OLD.valid_from_block::text)::text;
        END IF;
        IF TG_OP <> 'DELETE' THEN
            new_identity := jsonb_build_array(encode(NEW.address, 'hex'), encode(NEW.code_hash, 'hex'), NEW.valid_from_block::text)::text;
            logical_identity_value := new_identity;
        END IF;
    ELSIF TG_TABLE_NAME = 'contract_code_observations' THEN
        IF TG_OP <> 'INSERT' THEN
            old_identity := jsonb_build_array(encode(OLD.address, 'hex'), encode(OLD.block_hash, 'hex'))::text;
        END IF;
        IF TG_OP <> 'DELETE' THEN
            new_identity := jsonb_build_array(encode(NEW.address, 'hex'), encode(NEW.block_hash, 'hex'))::text;
            logical_identity_value := encode(NEW.address, 'hex');
        END IF;
    ELSE
        RAISE EXCEPTION 'unsupported search catalog source table %', TG_TABLE_NAME;
    END IF;

    INSERT INTO search_catalog_generations (chain_id)
    VALUES (source_chain)
    ON CONFLICT (chain_id) DO NOTHING;
    UPDATE search_catalog_generations
    SET generation = generation + 1, updated_at = now()
    WHERE chain_id = source_chain
    RETURNING generation INTO next_generation;

    UPDATE search_catalog_documents
    SET valid_to_generation = next_generation
    WHERE chain_id = source_chain
      AND source_kind = TG_ARGV[0]
      AND source_identity = old_identity
      AND valid_to_generation IS NULL;

    IF TG_OP = 'UPDATE' AND old_identity IS DISTINCT FROM new_identity THEN
        UPDATE search_catalog_documents
        SET valid_to_generation = next_generation
        WHERE chain_id = source_chain
          AND source_kind = TG_ARGV[0]
          AND source_identity = new_identity
          AND valid_to_generation IS NULL;
    END IF;

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;

    IF TG_TABLE_NAME = 'operator_labels' THEN
        INSERT INTO search_catalog_documents (
            chain_id, source_kind, source_identity, logical_identity, valid_from_generation,
            result_kind, result_key, result_label, exact_terms, partial_terms
        ) VALUES (
            NEW.chain_id, 'label', new_identity, logical_identity_value, next_generation,
            NEW.object_kind, NEW.object_key, NEW.label,
            ARRAY[lower(NEW.label), lower(NEW.object_key)], ARRAY[lower(NEW.label)]
        );
    ELSIF TG_TABLE_NAME = 'name_records' THEN
        INSERT INTO search_catalog_documents (
            chain_id, source_kind, source_identity, logical_identity, valid_from_generation,
            result_kind, result_key, result_label, exact_terms, partial_terms,
            block_number, block_hash, target_address, source_canonical
        ) VALUES (
            NEW.chain_id, 'name', new_identity, logical_identity_value, next_generation,
            'address', '0x' || encode(NEW.address, 'hex'), NEW.name,
            ARRAY[lower(NEW.name)], ARRAY[lower(NEW.name)],
            NEW.block_number, NEW.block_hash, NEW.address, NEW.canonical
        );
    ELSIF TG_TABLE_NAME = 'token_contracts' THEN
        INSERT INTO search_catalog_documents (
            chain_id, source_kind, source_identity, logical_identity, valid_from_generation,
            result_kind, result_key, result_label, exact_terms, partial_terms,
            block_number, block_hash, target_address, code_hash, source_canonical
        ) VALUES (
            NEW.chain_id, 'token', new_identity, logical_identity_value, next_generation,
            'token', '0x' || encode(NEW.address, 'hex'),
            COALESCE(NULLIF(NEW.name, ''), NULLIF(NEW.symbol, ''), 'Token 0x' || encode(NEW.address, 'hex')),
            array_remove(ARRAY[lower('0x' || encode(NEW.address, 'hex')), lower(COALESCE(NEW.name, '')), lower(COALESCE(NEW.symbol, ''))], ''),
            array_remove(ARRAY[lower(COALESCE(NEW.name, '')), lower(COALESCE(NEW.symbol, ''))], ''),
            NEW.observed_block_number, NEW.observed_block_hash, NEW.address, NEW.code_hash, TRUE
        );
    ELSIF TG_TABLE_NAME = 'verified_contracts' THEN
        INSERT INTO search_catalog_documents (
            chain_id, source_kind, source_identity, logical_identity, valid_from_generation,
            result_kind, result_key, result_label, exact_terms, partial_terms,
            target_address, code_hash, valid_from_block, valid_to_block
        ) VALUES (
            NEW.chain_id, 'verified_contract', new_identity, logical_identity_value, next_generation,
            'contract', '0x' || encode(NEW.address, 'hex'), NEW.contract_name,
            ARRAY[lower('0x' || encode(NEW.address, 'hex')), lower(NEW.contract_name)],
            ARRAY[lower(NEW.contract_name)], NEW.address, NEW.code_hash,
            NEW.valid_from_block, NEW.valid_to_block
        );
    ELSIF TG_TABLE_NAME = 'contract_code_observations' THEN
        INSERT INTO search_catalog_documents (
            chain_id, source_kind, source_identity, logical_identity, valid_from_generation,
            block_number, block_hash, target_address, code_hash, source_canonical
        ) VALUES (
            NEW.chain_id, 'code', new_identity, logical_identity_value, next_generation,
            NEW.block_number, NEW.block_hash, NEW.address, NEW.code_hash, NEW.canonical
        );
    END IF;
    RETURN NEW;
END
$$;

-- Function: record_verified_contract_search_catalog_document()

CREATE FUNCTION record_verified_contract_search_catalog_document() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    source_chain NUMERIC(78, 0);
    old_identity TEXT;
    new_identity TEXT;
    logical_identity_value TEXT;
    next_generation BIGINT;
BEGIN
    IF TG_OP = 'UPDATE' AND OLD.chain_id IS DISTINCT FROM NEW.chain_id THEN
        RAISE EXCEPTION 'search catalog source chain_id is immutable';
    END IF;
    IF TG_OP = 'UPDATE' AND OLD IS NOT DISTINCT FROM NEW THEN
        RETURN NEW;
    END IF;
    IF TG_OP = 'DELETE' THEN
        source_chain := OLD.chain_id;
        old_identity := jsonb_build_array(
            encode(OLD.address, 'hex'), encode(OLD.code_hash, 'hex'),
            OLD.valid_from_block::text, OLD.verification_job_id::text
        )::text;
    ELSE
        source_chain := NEW.chain_id;
        new_identity := jsonb_build_array(
            encode(NEW.address, 'hex'), encode(NEW.code_hash, 'hex'),
            NEW.valid_from_block::text, NEW.verification_job_id::text
        )::text;
        logical_identity_value := jsonb_build_array(
            encode(NEW.address, 'hex'), encode(NEW.code_hash, 'hex'),
            NEW.valid_from_block::text
        )::text;
        IF TG_OP = 'UPDATE' THEN
            old_identity := jsonb_build_array(
                encode(OLD.address, 'hex'), encode(OLD.code_hash, 'hex'),
                OLD.valid_from_block::text, OLD.verification_job_id::text
            )::text;
        END IF;
    END IF;

    INSERT INTO search_catalog_generations (chain_id)
    VALUES (source_chain)
    ON CONFLICT (chain_id) DO NOTHING;
    UPDATE search_catalog_generations
    SET generation = generation + 1, updated_at = now()
    WHERE chain_id = source_chain
    RETURNING generation INTO next_generation;

    UPDATE search_catalog_documents
    SET valid_to_generation = next_generation
    WHERE chain_id = source_chain
      AND source_kind = 'verified_contract'
      AND source_identity = old_identity
      AND valid_to_generation IS NULL;

    IF TG_OP = 'UPDATE' AND old_identity IS DISTINCT FROM new_identity THEN
        UPDATE search_catalog_documents
        SET valid_to_generation = next_generation
        WHERE chain_id = source_chain
          AND source_kind = 'verified_contract'
          AND source_identity = new_identity
          AND valid_to_generation IS NULL;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;

    INSERT INTO search_catalog_documents (
        chain_id, source_kind, source_identity, logical_identity,
        valid_from_generation, result_kind, result_key, result_label,
        exact_terms, partial_terms, target_address, code_hash,
        valid_from_block, valid_to_block, verification_match_type,
        verification_request_digest, verification_job_id
    ) VALUES (
        NEW.chain_id, 'verified_contract', new_identity,
        logical_identity_value, next_generation, 'contract',
        '0x' || encode(NEW.address, 'hex'), NEW.contract_name,
        ARRAY[
            lower('0x' || encode(NEW.address, 'hex')),
            lower(NEW.contract_name)
        ], ARRAY[lower(NEW.contract_name)], NEW.address, NEW.code_hash,
        NEW.valid_from_block, NEW.valid_to_block, NEW.match_type,
        NEW.request_digest, NEW.verification_job_id
    );
    RETURN NEW;
END
$$;

-- Function: refresh_native_transfer_coverage(numeric, numeric)

CREATE FUNCTION refresh_native_transfer_coverage(p_chain numeric, p_number numeric) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE segment NUMMULTIRANGE; ready BOOLEAN;
BEGIN
 INSERT INTO native_transfer_coverage(chain_id) VALUES(p_chain) ON CONFLICT DO NOTHING;
 PERFORM 1 FROM native_transfer_coverage WHERE chain_id=p_chain FOR UPDATE;
 segment := nummultirange(numrange(p_number,p_number+1,'[)'));
 SELECT EXISTS(SELECT 1 FROM canonical_blocks c JOIN published_block_stage_results s
 ON s.chain_id=c.chain_id AND s.block_hash=c.block_hash AND s.block_number=c.number
 WHERE c.chain_id=p_chain AND c.number=p_number AND s.stage='native_transfer'
 AND s.stage_version=1 AND s.state='complete') INTO ready;
 UPDATE native_transfer_coverage SET covered=CASE WHEN ready THEN covered+segment ELSE covered-segment END WHERE chain_id=p_chain;
END $$;

-- Function: refresh_proxy_interaction_coverage_block(numeric, numeric)

CREATE FUNCTION refresh_proxy_interaction_coverage_block(target_chain_id numeric, target_block_number numeric) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE
    canonical_hash BYTEA;
    existing_hash BYTEA;
    fully_covered BOOLEAN := FALSE;
    existing_covered BOOLEAN := FALSE;
    containing_range proxy_interaction_coverage_ranges%ROWTYPE;
    left_range proxy_interaction_coverage_ranges%ROWTYPE;
    right_range proxy_interaction_coverage_ranges%ROWTYPE;
    left_found BOOLEAN := FALSE;
    right_found BOOLEAN := FALSE;
    neighbor_hash BYTEA;
    old_end_block NUMERIC;
    old_end_hash BYTEA;
BEGIN
    IF target_chain_id IS NULL OR target_block_number IS NULL
       OR target_block_number < 0 THEN
        RAISE EXCEPTION 'proxy interaction coverage target is invalid'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;

    -- All interval merge/split operations for one chain share this lock. A
    -- hash of the arbitrary-width chain id is sufficient: collisions only
    -- serialize unrelated chains and cannot weaken correctness.
    PERFORM pg_advisory_xact_lock(hashtextextended(
        'etherview:proxy-interaction-coverage:' || target_chain_id::text,
        0
    ));

    SELECT canonical.block_hash
    INTO canonical_hash
    FROM canonical_blocks AS canonical
    WHERE canonical.chain_id = target_chain_id
      AND canonical.number = target_block_number;

    IF FOUND THEN
        SELECT NOT EXISTS (
            SELECT 1
            FROM (VALUES
                ('trace'::text, 4),
                ('state_diff'::text, 3),
                ('proxy'::text, 2)
            ) AS required(stage, stage_version)
            WHERE NOT EXISTS (
                SELECT 1
                FROM published_block_stage_results AS published
                WHERE published.chain_id = target_chain_id
                  AND published.block_number = target_block_number
                  AND published.block_hash = canonical_hash
                  AND published.stage = required.stage
                  AND published.stage_version = required.stage_version
                  AND published.state = 'complete'
            )
        ) INTO fully_covered;
    END IF;

    SELECT covered.block_hash
    INTO existing_hash
    FROM proxy_interaction_covered_blocks AS covered
    WHERE covered.chain_id = target_chain_id
      AND covered.block_number = target_block_number
    FOR UPDATE;
    existing_covered := FOUND;

    IF fully_covered THEN
        IF existing_covered THEN
            IF existing_hash IS DISTINCT FROM canonical_hash THEN
                UPDATE proxy_interaction_covered_blocks
                SET block_hash = canonical_hash,
                    updated_at = clock_timestamp()
                WHERE chain_id = target_chain_id
                  AND block_number = target_block_number;
                UPDATE proxy_interaction_coverage_ranges
                SET start_block_hash = canonical_hash,
                    updated_at = clock_timestamp()
                WHERE chain_id = target_chain_id
                  AND start_block = target_block_number;
                UPDATE proxy_interaction_coverage_ranges
                SET end_block_hash = canonical_hash,
                    updated_at = clock_timestamp()
                WHERE chain_id = target_chain_id
                  AND end_block = target_block_number;
            END IF;
            IF NOT EXISTS (
                SELECT 1
                FROM proxy_interaction_coverage_ranges AS coverage
                WHERE coverage.chain_id = target_chain_id
                  AND coverage.start_block <= target_block_number
                  AND coverage.end_block >= target_block_number
            ) THEN
                RAISE EXCEPTION 'covered proxy interaction block lacks a range'
                    USING ERRCODE = 'integrity_constraint_violation';
            END IF;
            RETURN;
        END IF;

        INSERT INTO proxy_interaction_covered_blocks (
            chain_id, block_number, block_hash
        ) VALUES (
            target_chain_id, target_block_number, canonical_hash
        );

        SELECT coverage.*
        INTO left_range
        FROM proxy_interaction_coverage_ranges AS coverage
        WHERE coverage.chain_id = target_chain_id
          AND coverage.end_block = target_block_number - 1
        FOR UPDATE;
        left_found := FOUND;

        SELECT coverage.*
        INTO right_range
        FROM proxy_interaction_coverage_ranges AS coverage
        WHERE coverage.chain_id = target_chain_id
          AND coverage.start_block = target_block_number + 1
        FOR UPDATE;
        right_found := FOUND;

        IF left_found AND right_found THEN
            DELETE FROM proxy_interaction_coverage_ranges
            WHERE chain_id = target_chain_id
              AND start_block = right_range.start_block;
            UPDATE proxy_interaction_coverage_ranges
            SET end_block = right_range.end_block,
                end_block_hash = right_range.end_block_hash,
                updated_at = clock_timestamp()
            WHERE chain_id = target_chain_id
              AND start_block = left_range.start_block;
        ELSIF left_found THEN
            UPDATE proxy_interaction_coverage_ranges
            SET end_block = target_block_number,
                end_block_hash = canonical_hash,
                updated_at = clock_timestamp()
            WHERE chain_id = target_chain_id
              AND start_block = left_range.start_block;
        ELSIF right_found THEN
            UPDATE proxy_interaction_coverage_ranges
            SET start_block = target_block_number,
                start_block_hash = canonical_hash,
                updated_at = clock_timestamp()
            WHERE chain_id = target_chain_id
              AND start_block = right_range.start_block;
        ELSE
            INSERT INTO proxy_interaction_coverage_ranges (
                chain_id, start_block, start_block_hash,
                end_block, end_block_hash
            ) VALUES (
                target_chain_id, target_block_number, canonical_hash,
                target_block_number, canonical_hash
            );
        END IF;
        RETURN;
    END IF;

    IF NOT existing_covered THEN
        RETURN;
    END IF;

    SELECT coverage.*
    INTO containing_range
    FROM proxy_interaction_coverage_ranges AS coverage
    WHERE coverage.chain_id = target_chain_id
      AND coverage.start_block <= target_block_number
      AND coverage.end_block >= target_block_number
    ORDER BY coverage.start_block DESC
    LIMIT 1
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'covered proxy interaction block lacks a removable range'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;

    DELETE FROM proxy_interaction_covered_blocks
    WHERE chain_id = target_chain_id
      AND block_number = target_block_number;

    IF containing_range.start_block = containing_range.end_block THEN
        DELETE FROM proxy_interaction_coverage_ranges
        WHERE chain_id = target_chain_id
          AND start_block = containing_range.start_block;
    ELSIF target_block_number = containing_range.start_block THEN
        SELECT covered.block_hash
        INTO STRICT neighbor_hash
        FROM proxy_interaction_covered_blocks AS covered
        WHERE covered.chain_id = target_chain_id
          AND covered.block_number = target_block_number + 1;
        UPDATE proxy_interaction_coverage_ranges
        SET start_block = target_block_number + 1,
            start_block_hash = neighbor_hash,
            updated_at = clock_timestamp()
        WHERE chain_id = target_chain_id
          AND start_block = containing_range.start_block;
    ELSIF target_block_number = containing_range.end_block THEN
        SELECT covered.block_hash
        INTO STRICT neighbor_hash
        FROM proxy_interaction_covered_blocks AS covered
        WHERE covered.chain_id = target_chain_id
          AND covered.block_number = target_block_number - 1;
        UPDATE proxy_interaction_coverage_ranges
        SET end_block = target_block_number - 1,
            end_block_hash = neighbor_hash,
            updated_at = clock_timestamp()
        WHERE chain_id = target_chain_id
          AND start_block = containing_range.start_block;
    ELSE
        SELECT covered.block_hash
        INTO STRICT neighbor_hash
        FROM proxy_interaction_covered_blocks AS covered
        WHERE covered.chain_id = target_chain_id
          AND covered.block_number = target_block_number - 1;
        old_end_block := containing_range.end_block;
        old_end_hash := containing_range.end_block_hash;
        UPDATE proxy_interaction_coverage_ranges
        SET end_block = target_block_number - 1,
            end_block_hash = neighbor_hash,
            updated_at = clock_timestamp()
        WHERE chain_id = target_chain_id
          AND start_block = containing_range.start_block;

        SELECT covered.block_hash
        INTO STRICT neighbor_hash
        FROM proxy_interaction_covered_blocks AS covered
        WHERE covered.chain_id = target_chain_id
          AND covered.block_number = target_block_number + 1;
        INSERT INTO proxy_interaction_coverage_ranges (
            chain_id, start_block, start_block_hash,
            end_block, end_block_hash
        ) VALUES (
            target_chain_id, target_block_number + 1, neighbor_hash,
            old_end_block, old_end_hash
        );
    END IF;
END
$$;

-- Function: refresh_proxy_interaction_coverage_from_canonical()

CREATE FUNCTION refresh_proxy_interaction_coverage_from_canonical() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP IN ('UPDATE', 'DELETE') THEN
        PERFORM refresh_proxy_interaction_coverage_block(
            OLD.chain_id, OLD.number
        );
    END IF;
    IF TG_OP IN ('INSERT', 'UPDATE') AND (
        TG_OP <> 'UPDATE'
        OR NEW.chain_id IS DISTINCT FROM OLD.chain_id
        OR NEW.number IS DISTINCT FROM OLD.number
        OR NEW.block_hash IS DISTINCT FROM OLD.block_hash
    ) THEN
        PERFORM refresh_proxy_interaction_coverage_block(
            NEW.chain_id, NEW.number
        );
    END IF;
    RETURN NULL;
END
$$;

-- Function: refresh_proxy_interaction_coverage_from_job()

CREATE FUNCTION refresh_proxy_interaction_coverage_from_job() RETURNS trigger
    LANGUAGE plpgsql
    AS $_$
DECLARE
    old_relevant BOOLEAN := FALSE;
    new_relevant BOOLEAN := FALSE;
    old_published BOOLEAN := FALSE;
    new_published BOOLEAN := FALSE;
BEGIN
    IF TG_OP IN ('UPDATE', 'DELETE') THEN
        old_relevant := OLD.kind = 'enrichment'
            AND (
                (OLD.stage = 'trace' AND OLD.stage_version = 4)
                OR (OLD.stage = 'state_diff' AND OLD.stage_version = 3)
                OR (OLD.stage = 'proxy' AND OLD.stage_version = 2)
            )
            AND COALESCE(
                OLD.payload->>'block_number' ~ '^(0|[1-9][0-9]*)$',
                FALSE
            );
        old_published := old_relevant
            AND OLD.status IN ('succeeded', 'failed')
            AND OLD.claimed_generation = OLD.completed_generation
            AND OLD.claimed_generation = OLD.requested_generation
            AND OLD.leased_generation IS NULL
            AND OLD.leased_by IS NULL
            AND OLD.lease_token IS NULL
            AND OLD.lease_expires_at IS NULL
            AND OLD.result IS NOT NULL;
    END IF;
    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        new_relevant := NEW.kind = 'enrichment'
            AND (
                (NEW.stage = 'trace' AND NEW.stage_version = 4)
                OR (NEW.stage = 'state_diff' AND NEW.stage_version = 3)
                OR (NEW.stage = 'proxy' AND NEW.stage_version = 2)
            )
            AND COALESCE(
                NEW.payload->>'block_number' ~ '^(0|[1-9][0-9]*)$',
                FALSE
            );
        new_published := new_relevant
            AND NEW.status IN ('succeeded', 'failed')
            AND NEW.claimed_generation = NEW.completed_generation
            AND NEW.claimed_generation = NEW.requested_generation
            AND NEW.leased_generation IS NULL
            AND NEW.leased_by IS NULL
            AND NEW.lease_token IS NULL
            AND NEW.lease_expires_at IS NULL
            AND NEW.result IS NOT NULL;
    END IF;

    IF old_published AND (
        NOT new_published
        OR NEW.chain_id IS DISTINCT FROM OLD.chain_id
        OR NEW.payload->>'block_number' IS DISTINCT FROM
           OLD.payload->>'block_number'
    ) THEN
        PERFORM refresh_proxy_interaction_coverage_block(
            OLD.chain_id, (OLD.payload->>'block_number')::numeric
        );
    END IF;
    IF new_published AND (
        NOT old_published
        OR NEW.chain_id IS DISTINCT FROM OLD.chain_id
        OR NEW.payload->>'block_number' IS DISTINCT FROM
           OLD.payload->>'block_number'
        OR NEW.result IS DISTINCT FROM OLD.result
    ) THEN
        PERFORM refresh_proxy_interaction_coverage_block(
            NEW.chain_id, (NEW.payload->>'block_number')::numeric
        );
    END IF;
    RETURN NULL;
END
$_$;

-- Function: refresh_proxy_interaction_coverage_from_journal()

CREATE FUNCTION refresh_proxy_interaction_coverage_from_journal() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    old_relevant BOOLEAN := FALSE;
    new_relevant BOOLEAN := FALSE;
    old_number NUMERIC;
    new_number NUMERIC;
BEGIN
    IF TG_OP IN ('UPDATE', 'DELETE') THEN
        old_relevant := OLD.stage IN ('trace@4', 'state_diff@3', 'proxy@2');
    END IF;
    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        new_relevant := NEW.stage IN ('trace@4', 'state_diff@3', 'proxy@2');
    END IF;
    IF old_relevant THEN
        SELECT block.number
        INTO old_number
        FROM blocks AS block
        WHERE block.chain_id = OLD.chain_id
          AND block.hash = OLD.block_hash;
        IF FOUND THEN
            PERFORM refresh_proxy_interaction_coverage_block(
                OLD.chain_id, old_number
            );
        END IF;
    END IF;
    IF new_relevant AND (
        NOT old_relevant
        OR NEW.chain_id IS DISTINCT FROM OLD.chain_id
        OR NEW.block_hash IS DISTINCT FROM OLD.block_hash
    ) THEN
        SELECT block.number
        INTO new_number
        FROM blocks AS block
        WHERE block.chain_id = NEW.chain_id
          AND block.hash = NEW.block_hash;
        IF FOUND THEN
            PERFORM refresh_proxy_interaction_coverage_block(
                NEW.chain_id, new_number
            );
        END IF;
    END IF;
    RETURN NULL;
END
$$;

-- Function: refresh_proxy_interaction_coverage_from_outbox()

CREATE FUNCTION refresh_proxy_interaction_coverage_from_outbox() RETURNS trigger
    LANGUAGE plpgsql
    AS $_$
DECLARE
    old_relevant BOOLEAN := FALSE;
    new_relevant BOOLEAN := FALSE;
    old_number NUMERIC;
    new_number NUMERIC;
    old_hash BYTEA;
    new_hash BYTEA;
BEGIN
    IF TG_OP IN ('UPDATE', 'DELETE') THEN
        old_relevant := OLD.topic = 'core.block.canonical'
            AND COALESCE(OLD.message_key ~ '^0x[0-9a-f]{64}$', FALSE);
    END IF;
    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        new_relevant := NEW.topic = 'core.block.canonical'
            AND COALESCE(NEW.message_key ~ '^0x[0-9a-f]{64}$', FALSE);
    END IF;
    IF TG_OP = 'UPDATE'
       AND NEW.published_at IS NOT DISTINCT FROM OLD.published_at
       AND NEW.topic IS NOT DISTINCT FROM OLD.topic
       AND NEW.message_key IS NOT DISTINCT FROM OLD.message_key
       AND NEW.chain_id IS NOT DISTINCT FROM OLD.chain_id THEN
        RETURN NULL;
    END IF;
    IF old_relevant THEN
        old_hash := decode(substr(OLD.message_key, 3), 'hex');
        SELECT block.number
        INTO old_number
        FROM blocks AS block
        WHERE block.chain_id = OLD.chain_id
          AND block.hash = old_hash;
        IF FOUND THEN
            PERFORM refresh_proxy_interaction_coverage_block(
                OLD.chain_id, old_number
            );
        END IF;
    END IF;
    IF new_relevant AND (
        NOT old_relevant
        OR NEW.chain_id IS DISTINCT FROM OLD.chain_id
        OR NEW.message_key IS DISTINCT FROM OLD.message_key
    ) THEN
        new_hash := decode(substr(NEW.message_key, 3), 'hex');
        SELECT block.number
        INTO new_number
        FROM blocks AS block
        WHERE block.chain_id = NEW.chain_id
          AND block.hash = new_hash;
        IF FOUND THEN
            PERFORM refresh_proxy_interaction_coverage_block(
                NEW.chain_id, new_number
            );
        END IF;
    END IF;
    RETURN NULL;
END
$_$;

-- Function: refresh_proxy_interaction_coverage_from_stage_result()

CREATE FUNCTION refresh_proxy_interaction_coverage_from_stage_result() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    old_relevant BOOLEAN := FALSE;
    new_relevant BOOLEAN := FALSE;
BEGIN
    IF TG_OP IN ('UPDATE', 'DELETE') THEN
        old_relevant := (OLD.stage = 'trace' AND OLD.stage_version = 4)
            OR (OLD.stage = 'state_diff' AND OLD.stage_version = 3)
            OR (OLD.stage = 'proxy' AND OLD.stage_version = 2);
    END IF;
    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        new_relevant := (NEW.stage = 'trace' AND NEW.stage_version = 4)
            OR (NEW.stage = 'state_diff' AND NEW.stage_version = 3)
            OR (NEW.stage = 'proxy' AND NEW.stage_version = 2);
    END IF;
    IF old_relevant THEN
        PERFORM refresh_proxy_interaction_coverage_block(
            OLD.chain_id, OLD.block_number
        );
    END IF;
    IF new_relevant AND (
        NOT old_relevant
        OR NEW.chain_id IS DISTINCT FROM OLD.chain_id
        OR NEW.block_number IS DISTINCT FROM OLD.block_number
    ) THEN
        PERFORM refresh_proxy_interaction_coverage_block(
            NEW.chain_id, NEW.block_number
        );
    END IF;
    RETURN NULL;
END
$$;

-- Function: reject_durable_stage_publication_mutation()

CREATE FUNCTION reject_durable_stage_publication_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'durable stage publication ledger is append-only'
        USING ERRCODE = 'integrity_constraint_violation';
END
$$;

-- Function: reject_exact_nft_observation_mutation()

CREATE FUNCTION reject_exact_nft_observation_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'exact NFT state observations are immutable'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END
$$;

-- Function: reject_proxy_history_epoch_mutation()

CREATE FUNCTION reject_proxy_history_epoch_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'proxy history epochs are append-only';
END
$$;

-- Function: reject_proxy_v2_evidence_mutation()

CREATE FUNCTION reject_proxy_v2_evidence_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'proxy v2 evidence is append-only';
END
$$;

-- Function: reject_verification_compilation_mutation()

CREATE FUNCTION reject_verification_compilation_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'authenticated verification compilation units are immutable';
END
$$;

-- Function: reject_verified_proxy_binding_mutation()

CREATE FUNCTION reject_verified_proxy_binding_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'verified proxy bindings are immutable';
END
$$;

-- Function: reject_verifier_v2_result_mutation()

CREATE FUNCTION reject_verifier_v2_result_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'verifier v2 results are immutable';
END
$$;

-- Function: require_enrichment_publication_protocol()

CREATE FUNCTION require_enrichment_publication_protocol() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.kind = 'enrichment'
       AND NEW.stage IN ('proxy', 'abi', 'token', 'stats', 'trace', 'native_transfer')
       AND NEW.status = 'leased'
       AND (
           OLD.status <> 'leased'
           OR OLD.lease_token IS DISTINCT FROM NEW.lease_token
           OR OLD.leased_generation IS DISTINCT FROM NEW.leased_generation
       )
       AND COALESCE(current_setting('etherview.enrichment_publication_protocol', true), '') <> '2' THEN
        RAISE EXCEPTION 'derived enrichment lease requires publication protocol 2'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END
$$;

-- Function: synchronize_derived_attempt_trace_canonicality()

CREATE FUNCTION synchronize_derived_attempt_trace_canonicality() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.canonical IS NOT DISTINCT FROM OLD.canonical THEN
        RETURN NEW;
    END IF;
    IF NOT NEW.canonical THEN
        UPDATE derived_verification_attempts
        SET stale_from_status = status,
            status = 'stale',
            updated_at = clock_timestamp()
        WHERE chain_id = NEW.chain_id
          AND block_number = NEW.block_number
          AND block_hash = NEW.block_hash
          AND transaction_hash = NEW.transaction_hash
          AND trace_path = NEW.trace_path
          AND status <> 'stale';
    ELSE
        UPDATE derived_verification_attempts
        SET status = stale_from_status,
            stale_from_status = NULL,
            updated_at = clock_timestamp()
        WHERE chain_id = NEW.chain_id
          AND block_number = NEW.block_number
          AND block_hash = NEW.block_hash
          AND transaction_hash = NEW.transaction_hash
          AND trace_path = NEW.trace_path
          AND status = 'stale'
          AND stale_from_status IS NOT NULL;
    END IF;
    RETURN NEW;
END
$$;

-- Function: valid_geas_verification_sources(jsonb)

CREATE FUNCTION valid_geas_verification_sources(candidate jsonb) RETURNS boolean
    LANGUAGE plpgsql IMMUTABLE STRICT PARALLEL SAFE
    SET search_path TO 'pg_catalog'
    AS $$
BEGIN
    IF jsonb_typeof(candidate) IS DISTINCT FROM 'object' OR
       candidate = '{}'::jsonb THEN
        RETURN FALSE;
    END IF;
    RETURN NOT EXISTS (
        SELECT 1
        FROM jsonb_each(candidate) AS source(name, value)
        WHERE CASE
            WHEN jsonb_typeof(source.value) = 'object' THEN
                jsonb_typeof(source.value->'content') IS DISTINCT FROM 'string' OR
                source.value - 'content' <> '{}'::jsonb
            ELSE TRUE
        END
    );
END
$$;

-- Function: watch_core_change()

CREATE FUNCTION watch_core_change() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    PERFORM invalidate_watch_block(NEW.chain_id, (NEW.payload->>'block_number')::numeric, decode(substr(NEW.payload->>'block_hash',3),'hex'));
    RETURN NULL;
END
$$;

-- Function: watch_token_change()

CREATE FUNCTION watch_token_change() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    PERFORM invalidate_watch_block(NEW.chain_id, (NEW.payload->>'block_number')::numeric, decode(substr(NEW.payload->>'block_hash',3),'hex'));
    RETURN NULL;
END
$$;

-- Table: abi_decodings

CREATE TABLE abi_decodings (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    object_kind text NOT NULL,
    transaction_hash bytea NOT NULL,
    object_index text NOT NULL,
    target_address bytea NOT NULL,
    target_code_hash bytea NOT NULL,
    abi_kind text NOT NULL,
    status text NOT NULL,
    signature text,
    source text,
    confidence text,
    arguments jsonb DEFAULT '[]'::jsonb NOT NULL,
    candidates jsonb DEFAULT '[]'::jsonb NOT NULL,
    warning text DEFAULT ''::text NOT NULL,
    canonical boolean NOT NULL,
    decoded_at timestamp with time zone DEFAULT now() NOT NULL,
    source_address bytea,
    source_code_hash bytea,
    return_status text DEFAULT 'not_applicable'::text NOT NULL,
    return_arguments jsonb DEFAULT '[]'::jsonb NOT NULL,
    decoding_kind text DEFAULT 'function'::text NOT NULL,
    CONSTRAINT abi_decodings_abi_kind_v3_check CHECK ((abi_kind = ANY (ARRAY['function'::text, 'constructor'::text, 'event'::text, 'error'::text]))),
    CONSTRAINT abi_decodings_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT abi_decodings_check1 CHECK (((status = ANY (ARRAY['decoded'::text, 'ambiguous'::text])) OR (signature IS NULL))),
    CONSTRAINT abi_decodings_decoding_kind_check CHECK ((decoding_kind = ANY (ARRAY['function'::text, 'constructor'::text, 'event'::text, 'error'::text]))),
    CONSTRAINT abi_decodings_object_kind_v3_check CHECK ((object_kind = ANY (ARRAY['transaction_calldata'::text, 'log'::text, 'trace_calldata'::text, 'trace_constructor'::text, 'trace_revert'::text]))),
    CONSTRAINT abi_decodings_return_arguments_array_check CHECK ((jsonb_typeof(return_arguments) = 'array'::text)),
    CONSTRAINT abi_decodings_return_status_check CHECK ((return_status = ANY (ARRAY['decoded'::text, 'empty'::text, 'unknown'::text, 'malformed'::text, 'unavailable'::text, 'not_applicable'::text]))),
    CONSTRAINT abi_decodings_source_address_length_check CHECK (((source_address IS NULL) OR (octet_length(source_address) = 20))),
    CONSTRAINT abi_decodings_source_code_hash_length_check CHECK (((source_code_hash IS NULL) OR (octet_length(source_code_hash) = 32))),
    CONSTRAINT abi_decodings_source_confidence_v3_check CHECK ((((source IS NULL) AND (confidence IS NULL)) OR ((source = 'verified'::text) AND (confidence = 'verified'::text)) OR ((source = ANY (ARRAY['code_hash'::text, 'proxy_implementation'::text, 'diamond_facet'::text, 'builtin'::text])) AND (confidence = 'high'::text)) OR ((source = 'signature_database'::text) AND (confidence = 'guess'::text)))),
    CONSTRAINT abi_decodings_source_identity_pair_check CHECK (((source_address IS NULL) = (source_code_hash IS NULL))),
    CONSTRAINT abi_decodings_status_check CHECK ((status = ANY (ARRAY['decoded'::text, 'unknown'::text, 'malformed'::text, 'ambiguous'::text]))),
    CONSTRAINT abi_decodings_target_address_check CHECK ((octet_length(target_address) = 20)),
    CONSTRAINT abi_decodings_target_code_hash_check CHECK ((octet_length(target_code_hash) = 32)),
    CONSTRAINT abi_decodings_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32))
)
PARTITION BY RANGE (block_number);

-- Table: abi_decodings_default

CREATE TABLE abi_decodings_default (
    chain_id numeric(78,0) CONSTRAINT abi_decodings_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT abi_decodings_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT abi_decodings_block_hash_not_null NOT NULL,
    object_kind text CONSTRAINT abi_decodings_object_kind_not_null NOT NULL,
    transaction_hash bytea CONSTRAINT abi_decodings_transaction_hash_not_null NOT NULL,
    object_index text CONSTRAINT abi_decodings_object_index_not_null NOT NULL,
    target_address bytea CONSTRAINT abi_decodings_target_address_not_null NOT NULL,
    target_code_hash bytea CONSTRAINT abi_decodings_target_code_hash_not_null NOT NULL,
    abi_kind text CONSTRAINT abi_decodings_abi_kind_not_null NOT NULL,
    status text CONSTRAINT abi_decodings_status_not_null NOT NULL,
    signature text,
    source text,
    confidence text,
    arguments jsonb DEFAULT '[]'::jsonb CONSTRAINT abi_decodings_arguments_not_null NOT NULL,
    candidates jsonb DEFAULT '[]'::jsonb CONSTRAINT abi_decodings_candidates_not_null NOT NULL,
    warning text DEFAULT ''::text CONSTRAINT abi_decodings_warning_not_null NOT NULL,
    canonical boolean CONSTRAINT abi_decodings_canonical_not_null NOT NULL,
    decoded_at timestamp with time zone DEFAULT now() CONSTRAINT abi_decodings_decoded_at_not_null NOT NULL,
    source_address bytea,
    source_code_hash bytea,
    return_status text DEFAULT 'not_applicable'::text CONSTRAINT abi_decodings_return_status_not_null NOT NULL,
    return_arguments jsonb DEFAULT '[]'::jsonb CONSTRAINT abi_decodings_return_arguments_not_null NOT NULL,
    decoding_kind text DEFAULT 'function'::text CONSTRAINT abi_decodings_decoding_kind_not_null NOT NULL,
    CONSTRAINT abi_decodings_abi_kind_v3_check CHECK ((abi_kind = ANY (ARRAY['function'::text, 'constructor'::text, 'event'::text, 'error'::text]))),
    CONSTRAINT abi_decodings_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT abi_decodings_check1 CHECK (((status = ANY (ARRAY['decoded'::text, 'ambiguous'::text])) OR (signature IS NULL))),
    CONSTRAINT abi_decodings_decoding_kind_check CHECK ((decoding_kind = ANY (ARRAY['function'::text, 'constructor'::text, 'event'::text, 'error'::text]))),
    CONSTRAINT abi_decodings_object_kind_v3_check CHECK ((object_kind = ANY (ARRAY['transaction_calldata'::text, 'log'::text, 'trace_calldata'::text, 'trace_constructor'::text, 'trace_revert'::text]))),
    CONSTRAINT abi_decodings_return_arguments_array_check CHECK ((jsonb_typeof(return_arguments) = 'array'::text)),
    CONSTRAINT abi_decodings_return_status_check CHECK ((return_status = ANY (ARRAY['decoded'::text, 'empty'::text, 'unknown'::text, 'malformed'::text, 'unavailable'::text, 'not_applicable'::text]))),
    CONSTRAINT abi_decodings_source_address_length_check CHECK (((source_address IS NULL) OR (octet_length(source_address) = 20))),
    CONSTRAINT abi_decodings_source_code_hash_length_check CHECK (((source_code_hash IS NULL) OR (octet_length(source_code_hash) = 32))),
    CONSTRAINT abi_decodings_source_confidence_v3_check CHECK ((((source IS NULL) AND (confidence IS NULL)) OR ((source = 'verified'::text) AND (confidence = 'verified'::text)) OR ((source = ANY (ARRAY['code_hash'::text, 'proxy_implementation'::text, 'diamond_facet'::text, 'builtin'::text])) AND (confidence = 'high'::text)) OR ((source = 'signature_database'::text) AND (confidence = 'guess'::text)))),
    CONSTRAINT abi_decodings_source_identity_pair_check CHECK (((source_address IS NULL) = (source_code_hash IS NULL))),
    CONSTRAINT abi_decodings_status_check CHECK ((status = ANY (ARRAY['decoded'::text, 'unknown'::text, 'malformed'::text, 'ambiguous'::text]))),
    CONSTRAINT abi_decodings_target_address_check CHECK ((octet_length(target_address) = 20)),
    CONSTRAINT abi_decodings_target_code_hash_check CHECK ((octet_length(target_code_hash) = 32)),
    CONSTRAINT abi_decodings_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32))
);

-- Table: abi_decodings_p_0_1000000

CREATE TABLE abi_decodings_p_0_1000000 (
    chain_id numeric(78,0) CONSTRAINT abi_decodings_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT abi_decodings_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT abi_decodings_block_hash_not_null NOT NULL,
    object_kind text CONSTRAINT abi_decodings_object_kind_not_null NOT NULL,
    transaction_hash bytea CONSTRAINT abi_decodings_transaction_hash_not_null NOT NULL,
    object_index text CONSTRAINT abi_decodings_object_index_not_null NOT NULL,
    target_address bytea CONSTRAINT abi_decodings_target_address_not_null NOT NULL,
    target_code_hash bytea CONSTRAINT abi_decodings_target_code_hash_not_null NOT NULL,
    abi_kind text CONSTRAINT abi_decodings_abi_kind_not_null NOT NULL,
    status text CONSTRAINT abi_decodings_status_not_null NOT NULL,
    signature text,
    source text,
    confidence text,
    arguments jsonb DEFAULT '[]'::jsonb CONSTRAINT abi_decodings_arguments_not_null NOT NULL,
    candidates jsonb DEFAULT '[]'::jsonb CONSTRAINT abi_decodings_candidates_not_null NOT NULL,
    warning text DEFAULT ''::text CONSTRAINT abi_decodings_warning_not_null NOT NULL,
    canonical boolean CONSTRAINT abi_decodings_canonical_not_null NOT NULL,
    decoded_at timestamp with time zone DEFAULT now() CONSTRAINT abi_decodings_decoded_at_not_null NOT NULL,
    source_address bytea,
    source_code_hash bytea,
    return_status text DEFAULT 'not_applicable'::text CONSTRAINT abi_decodings_return_status_not_null NOT NULL,
    return_arguments jsonb DEFAULT '[]'::jsonb CONSTRAINT abi_decodings_return_arguments_not_null NOT NULL,
    decoding_kind text DEFAULT 'function'::text CONSTRAINT abi_decodings_decoding_kind_not_null NOT NULL,
    CONSTRAINT abi_decodings_abi_kind_v3_check CHECK ((abi_kind = ANY (ARRAY['function'::text, 'constructor'::text, 'event'::text, 'error'::text]))),
    CONSTRAINT abi_decodings_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT abi_decodings_check1 CHECK (((status = ANY (ARRAY['decoded'::text, 'ambiguous'::text])) OR (signature IS NULL))),
    CONSTRAINT abi_decodings_decoding_kind_check CHECK ((decoding_kind = ANY (ARRAY['function'::text, 'constructor'::text, 'event'::text, 'error'::text]))),
    CONSTRAINT abi_decodings_object_kind_v3_check CHECK ((object_kind = ANY (ARRAY['transaction_calldata'::text, 'log'::text, 'trace_calldata'::text, 'trace_constructor'::text, 'trace_revert'::text]))),
    CONSTRAINT abi_decodings_return_arguments_array_check CHECK ((jsonb_typeof(return_arguments) = 'array'::text)),
    CONSTRAINT abi_decodings_return_status_check CHECK ((return_status = ANY (ARRAY['decoded'::text, 'empty'::text, 'unknown'::text, 'malformed'::text, 'unavailable'::text, 'not_applicable'::text]))),
    CONSTRAINT abi_decodings_source_address_length_check CHECK (((source_address IS NULL) OR (octet_length(source_address) = 20))),
    CONSTRAINT abi_decodings_source_code_hash_length_check CHECK (((source_code_hash IS NULL) OR (octet_length(source_code_hash) = 32))),
    CONSTRAINT abi_decodings_source_confidence_v3_check CHECK ((((source IS NULL) AND (confidence IS NULL)) OR ((source = 'verified'::text) AND (confidence = 'verified'::text)) OR ((source = ANY (ARRAY['code_hash'::text, 'proxy_implementation'::text, 'diamond_facet'::text, 'builtin'::text])) AND (confidence = 'high'::text)) OR ((source = 'signature_database'::text) AND (confidence = 'guess'::text)))),
    CONSTRAINT abi_decodings_source_identity_pair_check CHECK (((source_address IS NULL) = (source_code_hash IS NULL))),
    CONSTRAINT abi_decodings_status_check CHECK ((status = ANY (ARRAY['decoded'::text, 'unknown'::text, 'malformed'::text, 'ambiguous'::text]))),
    CONSTRAINT abi_decodings_target_address_check CHECK ((octet_length(target_address) = 20)),
    CONSTRAINT abi_decodings_target_code_hash_check CHECK ((octet_length(target_code_hash) = 32)),
    CONSTRAINT abi_decodings_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32))
);

-- Table: abi_signature_candidates

CREATE TABLE abi_signature_candidates (
    kind text NOT NULL,
    identifier bytea NOT NULL,
    signature text NOT NULL,
    abi_entry jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT abi_signature_candidates_abi_entry_check CHECK ((jsonb_typeof(abi_entry) = 'object'::text)),
    CONSTRAINT abi_signature_candidates_check CHECK ((((kind = ANY (ARRAY['function'::text, 'error'::text])) AND (octet_length(identifier) = 4)) OR ((kind = 'event'::text) AND (octet_length(identifier) = 32)))),
    CONSTRAINT abi_signature_candidates_kind_check CHECK ((kind = ANY (ARRAY['function'::text, 'event'::text, 'error'::text]))),
    CONSTRAINT abi_signature_candidates_signature_check CHECK ((signature <> ''::text))
);

-- Table: address_activities

CREATE TABLE address_activities (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    transaction_hash bytea NOT NULL,
    activity_index integer NOT NULL,
    address bytea NOT NULL,
    activity_kind text NOT NULL,
    canonical boolean NOT NULL,
    details jsonb DEFAULT '{}'::jsonb NOT NULL,
    CONSTRAINT address_activities_activity_index_check CHECK ((activity_index >= 0)),
    CONSTRAINT address_activities_activity_kind_check CHECK ((activity_kind = ANY (ARRAY['transaction'::text, 'token'::text, 'trace'::text, 'contract_creation'::text]))),
    CONSTRAINT address_activities_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT address_activities_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT address_activities_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32))
)
PARTITION BY RANGE (block_number);

-- Table: address_activities_default

CREATE TABLE address_activities_default (
    chain_id numeric(78,0) CONSTRAINT address_activities_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT address_activities_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT address_activities_block_hash_not_null NOT NULL,
    transaction_hash bytea CONSTRAINT address_activities_transaction_hash_not_null NOT NULL,
    activity_index integer CONSTRAINT address_activities_activity_index_not_null NOT NULL,
    address bytea CONSTRAINT address_activities_address_not_null NOT NULL,
    activity_kind text CONSTRAINT address_activities_activity_kind_not_null NOT NULL,
    canonical boolean CONSTRAINT address_activities_canonical_not_null NOT NULL,
    details jsonb DEFAULT '{}'::jsonb CONSTRAINT address_activities_details_not_null NOT NULL,
    CONSTRAINT address_activities_activity_index_check CHECK ((activity_index >= 0)),
    CONSTRAINT address_activities_activity_kind_check CHECK ((activity_kind = ANY (ARRAY['transaction'::text, 'token'::text, 'trace'::text, 'contract_creation'::text]))),
    CONSTRAINT address_activities_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT address_activities_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT address_activities_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32))
);

-- Table: address_activities_p_0_1000000

CREATE TABLE address_activities_p_0_1000000 (
    chain_id numeric(78,0) CONSTRAINT address_activities_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT address_activities_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT address_activities_block_hash_not_null NOT NULL,
    transaction_hash bytea CONSTRAINT address_activities_transaction_hash_not_null NOT NULL,
    activity_index integer CONSTRAINT address_activities_activity_index_not_null NOT NULL,
    address bytea CONSTRAINT address_activities_address_not_null NOT NULL,
    activity_kind text CONSTRAINT address_activities_activity_kind_not_null NOT NULL,
    canonical boolean CONSTRAINT address_activities_canonical_not_null NOT NULL,
    details jsonb DEFAULT '{}'::jsonb CONSTRAINT address_activities_details_not_null NOT NULL,
    CONSTRAINT address_activities_activity_index_check CHECK ((activity_index >= 0)),
    CONSTRAINT address_activities_activity_kind_check CHECK ((activity_kind = ANY (ARRAY['transaction'::text, 'token'::text, 'trace'::text, 'contract_creation'::text]))),
    CONSTRAINT address_activities_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT address_activities_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT address_activities_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32))
);

-- Table: address_export_admissions

CREATE TABLE address_export_admissions (
    token uuid NOT NULL,
    user_id uuid NOT NULL,
    started_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    expires_at timestamp with time zone DEFAULT (clock_timestamp() + '00:00:20'::interval) NOT NULL,
    released boolean DEFAULT false NOT NULL
);

-- Table: address_watches

CREATE TABLE address_watches (
    id uuid NOT NULL,
    user_id uuid NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    address bytea NOT NULL,
    label text DEFAULT ''::text NOT NULL,
    kinds text[] NOT NULL,
    direction text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    start_number numeric(78,0) NOT NULL,
    start_hash bytea NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    deleted_at timestamp with time zone,
    CONSTRAINT address_watches_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT address_watches_direction_check CHECK ((direction = ANY (ARRAY['in'::text, 'out'::text, 'both'::text]))),
    CONSTRAINT address_watches_kinds_check CHECK (((cardinality(kinds) BETWEEN 1 AND 4) AND (kinds <@ ARRAY['transaction'::text, 'erc20'::text, 'erc721'::text, 'erc1155'::text]))),
    CONSTRAINT address_watches_label_check CHECK (((char_length(label) <= 64) AND (label !~ '[[:cntrl:]]'::text))),
    CONSTRAINT address_watches_start_hash_check CHECK ((octet_length(start_hash) = 32))
);

-- Table: api_keys

CREATE TABLE api_keys (
    prefix text NOT NULL,
    digest bytea NOT NULL,
    name text NOT NULL,
    rate_per_second integer NOT NULL,
    burst integer NOT NULL,
    created_at timestamp with time zone NOT NULL,
    revoked_at timestamp with time zone,
    owner_user_id uuid,
    scopes text[] NOT NULL,
    CONSTRAINT api_keys_check CHECK ((burst >= rate_per_second)),
    CONSTRAINT api_keys_digest_check CHECK ((octet_length(digest) = 32)),
    CONSTRAINT api_keys_prefix_check CHECK ((length(prefix) = 10)),
    CONSTRAINT api_keys_rate_per_second_check CHECK ((rate_per_second > 0)),
    CONSTRAINT api_keys_scopes_check CHECK (((scopes = ARRAY['api:read'::text]) OR (scopes = ARRAY['contract:verify'::text]) OR (scopes = ARRAY['api:read'::text, 'contract:verify'::text])))
);

-- Table: auth_challenges

CREATE TABLE auth_challenges (
    id uuid NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    address bytea NOT NULL,
    message text NOT NULL,
    nonce text NOT NULL,
    issued_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    consumed_at timestamp with time zone,
    CONSTRAINT auth_challenges_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT auth_challenges_check CHECK ((expires_at > issued_at)),
    CONSTRAINT auth_challenges_check1 CHECK (((consumed_at IS NULL) OR (consumed_at >= issued_at))),
    CONSTRAINT auth_challenges_message_check CHECK (((octet_length(message) >= 1) AND (octet_length(message) <= 4096))),
    CONSTRAINT auth_challenges_nonce_check CHECK (((char_length(nonce) >= 8) AND (char_length(nonce) <= 64))),
    CONSTRAINT auth_challenges_nonce_check1 CHECK ((nonce ~ '^[A-Za-z0-9]+$'::text))
);

-- Table: beacon_implementation_observations

CREATE TABLE beacon_implementation_observations (
    chain_id numeric(78,0) NOT NULL,
    beacon_address bytea NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    beacon_code_hash bytea NOT NULL,
    implementation_address bytea CONSTRAINT beacon_implementation_observati_implementation_address_not_null NOT NULL,
    implementation_code_hash bytea CONSTRAINT beacon_implementation_observa_implementation_code_hash_not_null NOT NULL,
    stage_version integer DEFAULT 2 NOT NULL,
    confidence text NOT NULL,
    canonical boolean NOT NULL,
    details jsonb DEFAULT '{}'::jsonb NOT NULL,
    observed_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT beacon_implementation_observatio_implementation_code_hash_check CHECK ((octet_length(implementation_code_hash) = 32)),
    CONSTRAINT beacon_implementation_observations_beacon_address_check CHECK ((octet_length(beacon_address) = 20)),
    CONSTRAINT beacon_implementation_observations_beacon_code_hash_check CHECK ((octet_length(beacon_code_hash) = 32)),
    CONSTRAINT beacon_implementation_observations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT beacon_implementation_observations_confidence_check CHECK ((confidence = ANY (ARRAY['verified'::text, 'high'::text, 'inferred'::text, 'guess'::text]))),
    CONSTRAINT beacon_implementation_observations_details_check CHECK ((jsonb_typeof(details) = 'object'::text)),
    CONSTRAINT beacon_implementation_observations_implementation_address_check CHECK ((octet_length(implementation_address) = 20)),
    CONSTRAINT beacon_implementation_observations_stage_version_check CHECK ((stage_version = 2))
);

-- Table: beacon_observation_generations

CREATE TABLE beacon_observation_generations (
    id bigint NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    beacon_address bytea NOT NULL,
    observation_block_hash bytea NOT NULL,
    observation_stage_version integer CONSTRAINT beacon_observation_generatio_observation_stage_version_not_null NOT NULL,
    durable_job_id bigint,
    job_generation bigint,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT beacon_observation_generations_check CHECK ((((durable_job_id IS NULL) AND (job_generation IS NULL)) OR ((durable_job_id IS NOT NULL) AND (job_generation > 0)))),
    CONSTRAINT beacon_observation_generations_observation_stage_version_check CHECK ((observation_stage_version = 2))
);

-- Sequence: beacon_observation_generations_id_seq

CREATE SEQUENCE beacon_observation_generations_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

-- Sequence Owned By: beacon_observation_generations_id_seq

ALTER SEQUENCE beacon_observation_generations_id_seq OWNED BY beacon_observation_generations.id;

-- Table: billing_account_entries

CREATE TABLE billing_account_entries (
    id uuid NOT NULL,
    user_id uuid NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    network text NOT NULL,
    asset bytea NOT NULL,
    direction text NOT NULL,
    kind text NOT NULL,
    amount_atomic numeric(78,0) NOT NULL,
    source_id uuid NOT NULL,
    reason text,
    occurred_at timestamp with time zone NOT NULL,
    CONSTRAINT billing_account_entries_amount_atomic_check CHECK ((amount_atomic > (0)::numeric)),
    CONSTRAINT billing_account_entries_check CHECK ((((kind = 'adjustment'::text) AND (reason IS NOT NULL) AND ((length(reason) >= 1) AND (length(reason) <= 256))) OR ((kind <> 'adjustment'::text) AND (reason IS NULL)))),
    CONSTRAINT billing_account_entries_direction_check CHECK ((direction = ANY (ARRAY['credit'::text, 'debit'::text]))),
    CONSTRAINT billing_account_entries_kind_check CHECK ((kind = ANY (ARRAY['topup'::text, 'usage'::text, 'adjustment'::text])))
);

-- Table: billing_accounts

CREATE TABLE billing_accounts (
    user_id uuid NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    network text NOT NULL,
    asset bytea NOT NULL,
    total_credit_atomic numeric(78,0) DEFAULT 0 NOT NULL,
    total_debit_atomic numeric(78,0) DEFAULT 0 NOT NULL,
    reserved_atomic numeric(78,0) DEFAULT 0 NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT billing_accounts_asset_check CHECK ((octet_length(asset) = 20)),
    CONSTRAINT billing_accounts_check CHECK ((total_credit_atomic >= (total_debit_atomic + reserved_atomic))),
    CONSTRAINT billing_accounts_check1 CHECK ((updated_at >= created_at)),
    CONSTRAINT billing_accounts_network_check CHECK (((network ~ '^eip155:[1-9][0-9]*$'::text) AND (length(network) <= 96))),
    CONSTRAINT billing_accounts_reserved_atomic_check CHECK ((reserved_atomic >= (0)::numeric)),
    CONSTRAINT billing_accounts_total_credit_atomic_check CHECK ((total_credit_atomic >= (0)::numeric)),
    CONSTRAINT billing_accounts_total_debit_atomic_check CHECK ((total_debit_atomic >= (0)::numeric))
);

-- Table: billing_payment_events

CREATE TABLE billing_payment_events (
    id bigint NOT NULL,
    payment_id uuid NOT NULL,
    from_state text,
    to_state text NOT NULL,
    code text NOT NULL,
    actor text NOT NULL,
    transaction_hash bytea,
    occurred_at timestamp with time zone NOT NULL,
    CONSTRAINT billing_payment_events_actor_check CHECK ((actor = ANY (ARRAY['runtime'::text, 'operator'::text]))),
    CONSTRAINT billing_payment_events_code_check CHECK (((length(code) >= 1) AND (length(code) <= 128))),
    CONSTRAINT billing_payment_events_from_state_check CHECK (((from_state IS NULL) OR (from_state = ANY (ARRAY['reserved'::text, 'verified'::text, 'settling'::text, 'settled'::text, 'failed'::text, 'expired'::text])))),
    CONSTRAINT billing_payment_events_to_state_check CHECK ((to_state = ANY (ARRAY['reserved'::text, 'verified'::text, 'settling'::text, 'settled'::text, 'failed'::text, 'expired'::text]))),
    CONSTRAINT billing_payment_events_transaction_hash_check CHECK (((transaction_hash IS NULL) OR (octet_length(transaction_hash) = 32)))
);

-- Sequence: billing_payment_events_id_seq

ALTER TABLE billing_payment_events ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME billing_payment_events_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

-- Table: billing_payments

CREATE TABLE billing_payments (
    id uuid NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    fingerprint bytea NOT NULL,
    reservation_owner uuid NOT NULL,
    method text NOT NULL,
    operation text NOT NULL,
    resource_digest bytea NOT NULL,
    requirement_digest bytea NOT NULL,
    protocol_version smallint NOT NULL,
    scheme text NOT NULL,
    network text NOT NULL,
    asset bytea NOT NULL,
    amount_atomic numeric(78,0) NOT NULL,
    recipient bytea NOT NULL,
    payer bytea,
    user_id uuid,
    api_key_prefix text,
    facilitator_digest bytea NOT NULL,
    transaction_hash bytea,
    state text NOT NULL,
    failure_code text,
    reservation_expires_at timestamp with time zone NOT NULL,
    handler_started_at timestamp with time zone,
    verified_at timestamp with time zone,
    settling_at timestamp with time zone,
    settled_at timestamp with time zone,
    failed_at timestamp with time zone,
    expired_at timestamp with time zone,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    purpose text NOT NULL,
    asset_transfer_method text NOT NULL,
    payment_flow text NOT NULL,
    fingerprint_version smallint NOT NULL,
    topup_intent_id uuid,
    CONSTRAINT billing_payments_amount_atomic_check CHECK (((amount_atomic > (0)::numeric) AND (amount_atomic <= '115792089237316195423570985008687907853269984665640564039457584007913129639935'::numeric))),
    CONSTRAINT billing_payments_asset_check CHECK ((octet_length(asset) = 20)),
    CONSTRAINT billing_payments_check CHECK ((reservation_expires_at > created_at)),
    CONSTRAINT billing_payments_check1 CHECK ((updated_at >= created_at)),
    CONSTRAINT billing_payments_check2 CHECK (((handler_started_at IS NULL) OR (handler_started_at >= created_at))),
    CONSTRAINT billing_payments_check3 CHECK (((verified_at IS NULL) OR (verified_at >= created_at))),
    CONSTRAINT billing_payments_check4 CHECK (((settling_at IS NULL) OR (settling_at >= created_at))),
    CONSTRAINT billing_payments_check5 CHECK (((settled_at IS NULL) OR (settled_at >= created_at))),
    CONSTRAINT billing_payments_check6 CHECK (((failed_at IS NULL) OR (failed_at >= created_at))),
    CONSTRAINT billing_payments_check7 CHECK (((expired_at IS NULL) OR (expired_at >= created_at))),
    CONSTRAINT billing_payments_facilitator_digest_check CHECK ((octet_length(facilitator_digest) = 32)),
    CONSTRAINT billing_payments_failure_code_check CHECK (((failure_code IS NULL) OR ((length(failure_code) >= 1) AND (length(failure_code) <= 128)))),
    CONSTRAINT billing_payments_fingerprint_check CHECK ((octet_length(fingerprint) = 32)),
    CONSTRAINT billing_payments_fingerprint_version_check CHECK ((fingerprint_version = ANY (ARRAY[1, 2]))),
    CONSTRAINT billing_payments_method_v2_check CHECK ((method = ANY (ARRAY['GET'::text, 'POST'::text]))),
    CONSTRAINT billing_payments_network_check CHECK (((network ~ '^eip155:[1-9][0-9]*$'::text) AND (length(network) <= 96))),
    CONSTRAINT billing_payments_operation_check CHECK (((length(operation) >= 1) AND (length(operation) <= 128))),
    CONSTRAINT billing_payments_payer_check CHECK (((payer IS NULL) OR (octet_length(payer) = 20))),
    CONSTRAINT billing_payments_payment_flow_check CHECK ((payment_flow = 'authorization'::text)),
    CONSTRAINT billing_payments_protocol_version_check CHECK ((protocol_version = 2)),
    CONSTRAINT billing_payments_purpose_check CHECK ((purpose = ANY (ARRAY['legacy_request'::text, 'account_topup'::text]))),
    CONSTRAINT billing_payments_recipient_check CHECK ((octet_length(recipient) = 20)),
    CONSTRAINT billing_payments_requirement_digest_check CHECK ((octet_length(requirement_digest) = 32)),
    CONSTRAINT billing_payments_resource_digest_check CHECK ((octet_length(resource_digest) = 32)),
    CONSTRAINT billing_payments_scheme_check CHECK ((scheme = 'exact'::text)),
    CONSTRAINT billing_payments_state_check CHECK ((state = ANY (ARRAY['reserved'::text, 'verified'::text, 'settling'::text, 'settled'::text, 'failed'::text, 'expired'::text]))),
    CONSTRAINT billing_payments_state_facts_v2_check CHECK ((((state = 'reserved'::text) AND (verified_at IS NULL) AND (handler_started_at IS NULL) AND (settling_at IS NULL) AND (settled_at IS NULL) AND (failed_at IS NULL) AND (expired_at IS NULL) AND (transaction_hash IS NULL) AND (failure_code IS NULL)) OR ((state = 'verified'::text) AND (verified_at IS NOT NULL) AND (settling_at IS NULL) AND (settled_at IS NULL) AND (failed_at IS NULL) AND (expired_at IS NULL) AND (transaction_hash IS NULL) AND (failure_code IS NULL)) OR ((state = 'settling'::text) AND (verified_at IS NOT NULL) AND (handler_started_at IS NOT NULL) AND (settling_at IS NOT NULL) AND (settled_at IS NULL) AND (failed_at IS NULL) AND (expired_at IS NULL) AND (((failure_code IS NULL) AND (transaction_hash IS NULL)) OR ((failure_code = 'settlement_unknown'::text) AND (transaction_hash IS NULL)) OR ((failure_code = 'settlement_pending'::text) AND (transaction_hash IS NOT NULL)))) OR ((state = 'settled'::text) AND (verified_at IS NOT NULL) AND (handler_started_at IS NOT NULL) AND (settling_at IS NOT NULL) AND (settled_at IS NOT NULL) AND (failed_at IS NULL) AND (expired_at IS NULL) AND (transaction_hash IS NOT NULL) AND (failure_code IS NULL)) OR ((state = 'failed'::text) AND (settled_at IS NULL) AND (failed_at IS NOT NULL) AND (expired_at IS NULL) AND (failure_code IS NOT NULL)) OR ((state = 'expired'::text) AND (settling_at IS NULL) AND (settled_at IS NULL) AND (failed_at IS NULL) AND (expired_at IS NOT NULL) AND (transaction_hash IS NULL) AND (failure_code = 'reservation_expired'::text)))),
    CONSTRAINT billing_payments_topup_binding_check CHECK ((((purpose = 'legacy_request'::text) AND (topup_intent_id IS NULL)) OR ((purpose = 'account_topup'::text) AND (topup_intent_id IS NOT NULL) AND (method = 'POST'::text)))),
    CONSTRAINT billing_payments_transaction_hash_check CHECK (((transaction_hash IS NULL) OR (octet_length(transaction_hash) = 32))),
    CONSTRAINT billing_payments_transfer_method_check CHECK ((asset_transfer_method = ANY (ARRAY['eip3009'::text, 'permit2'::text])))
);

-- Table: billing_topup_intents

CREATE TABLE billing_topup_intents (
    id uuid NOT NULL,
    user_id uuid NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    network text NOT NULL,
    asset bytea NOT NULL,
    amount_atomic numeric(78,0) NOT NULL,
    recipient bytea NOT NULL,
    payer bytea NOT NULL,
    state text NOT NULL,
    active_payment_id uuid,
    transaction_hash bytea,
    failure_code text,
    expires_at timestamp with time zone NOT NULL,
    processing_at timestamp with time zone,
    settling_at timestamp with time zone,
    credited_at timestamp with time zone,
    failed_at timestamp with time zone,
    expired_at timestamp with time zone,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT billing_topup_intents_amount_atomic_check CHECK (((amount_atomic > (0)::numeric) AND (amount_atomic <= '115792089237316195423570985008687907853269984665640564039457584007913129639935'::numeric))),
    CONSTRAINT billing_topup_intents_asset_check CHECK ((octet_length(asset) = 20)),
    CONSTRAINT billing_topup_intents_check CHECK ((expires_at > created_at)),
    CONSTRAINT billing_topup_intents_check1 CHECK ((updated_at >= created_at)),
    CONSTRAINT billing_topup_intents_check2 CHECK ((((state = 'open'::text) AND (active_payment_id IS NULL) AND (transaction_hash IS NULL) AND (processing_at IS NULL) AND (settling_at IS NULL) AND (credited_at IS NULL) AND (failed_at IS NULL) AND (expired_at IS NULL) AND (failure_code IS NULL)) OR ((state = 'processing'::text) AND (active_payment_id IS NOT NULL) AND (transaction_hash IS NULL) AND (processing_at IS NOT NULL) AND (settling_at IS NULL) AND (credited_at IS NULL) AND (failed_at IS NULL) AND (expired_at IS NULL) AND (failure_code IS NULL)) OR ((state = 'settling'::text) AND (active_payment_id IS NOT NULL) AND (processing_at IS NOT NULL) AND (settling_at IS NOT NULL) AND (credited_at IS NULL) AND (failed_at IS NULL) AND (expired_at IS NULL) AND (((failure_code IS NULL) AND (transaction_hash IS NULL)) OR ((failure_code = 'settlement_unknown'::text) AND (transaction_hash IS NULL)) OR ((failure_code = 'settlement_pending'::text) AND (transaction_hash IS NOT NULL)))) OR ((state = 'credited'::text) AND (active_payment_id IS NOT NULL) AND (transaction_hash IS NOT NULL) AND (processing_at IS NOT NULL) AND (settling_at IS NOT NULL) AND (credited_at IS NOT NULL) AND (failed_at IS NULL) AND (expired_at IS NULL) AND (failure_code IS NULL)) OR ((state = 'failed'::text) AND (active_payment_id IS NOT NULL) AND (failed_at IS NOT NULL) AND (credited_at IS NULL) AND (expired_at IS NULL) AND (failure_code IS NOT NULL)) OR ((state = 'expired'::text) AND (active_payment_id IS NULL) AND (transaction_hash IS NULL) AND (credited_at IS NULL) AND (failed_at IS NULL) AND (expired_at IS NOT NULL) AND (failure_code = 'topup_intent_expired'::text)))),
    CONSTRAINT billing_topup_intents_failure_code_check CHECK (((failure_code IS NULL) OR ((length(failure_code) >= 1) AND (length(failure_code) <= 128)))),
    CONSTRAINT billing_topup_intents_network_check CHECK (((network ~ '^eip155:[1-9][0-9]*$'::text) AND (length(network) <= 96))),
    CONSTRAINT billing_topup_intents_payer_check CHECK ((octet_length(payer) = 20)),
    CONSTRAINT billing_topup_intents_recipient_check CHECK ((octet_length(recipient) = 20)),
    CONSTRAINT billing_topup_intents_state_check CHECK ((state = ANY (ARRAY['open'::text, 'processing'::text, 'settling'::text, 'credited'::text, 'failed'::text, 'expired'::text]))),
    CONSTRAINT billing_topup_intents_transaction_hash_check CHECK (((transaction_hash IS NULL) OR (octet_length(transaction_hash) = 32)))
);

-- Table: billing_usage_charges

CREATE TABLE billing_usage_charges (
    id uuid NOT NULL,
    reservation_owner uuid NOT NULL,
    user_id uuid NOT NULL,
    api_key_prefix text NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    network text NOT NULL,
    asset bytea NOT NULL,
    method text NOT NULL,
    operation text NOT NULL,
    resource_digest bytea NOT NULL,
    amount_atomic numeric(78,0) NOT NULL,
    state text NOT NULL,
    failure_code text,
    response_digest bytea,
    response_bytes bigint,
    reservation_expires_at timestamp with time zone NOT NULL,
    committed_at timestamp with time zone,
    released_at timestamp with time zone,
    expired_at timestamp with time zone,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT billing_usage_charges_amount_atomic_check CHECK ((amount_atomic > (0)::numeric)),
    CONSTRAINT billing_usage_charges_check CHECK ((reservation_expires_at > created_at)),
    CONSTRAINT billing_usage_charges_check1 CHECK ((updated_at >= created_at)),
    CONSTRAINT billing_usage_charges_check2 CHECK ((((state = 'reserved'::text) AND (failure_code IS NULL) AND (response_digest IS NULL) AND (response_bytes IS NULL) AND (committed_at IS NULL) AND (released_at IS NULL) AND (expired_at IS NULL)) OR ((state = 'committed'::text) AND (failure_code IS NULL) AND (response_digest IS NOT NULL) AND (response_bytes IS NOT NULL) AND (committed_at IS NOT NULL) AND (released_at IS NULL) AND (expired_at IS NULL)) OR ((state = 'released'::text) AND (failure_code IS NOT NULL) AND (committed_at IS NULL) AND (released_at IS NOT NULL) AND (expired_at IS NULL)) OR ((state = 'expired'::text) AND (failure_code = 'usage_reservation_expired'::text) AND (committed_at IS NULL) AND (released_at IS NULL) AND (expired_at IS NOT NULL)))),
    CONSTRAINT billing_usage_charges_failure_code_check CHECK (((failure_code IS NULL) OR ((length(failure_code) >= 1) AND (length(failure_code) <= 128)))),
    CONSTRAINT billing_usage_charges_method_check CHECK ((method = ANY (ARRAY['GET'::text, 'POST'::text]))),
    CONSTRAINT billing_usage_charges_operation_check CHECK (((length(operation) >= 1) AND (length(operation) <= 128))),
    CONSTRAINT billing_usage_charges_resource_digest_check CHECK ((octet_length(resource_digest) = 32)),
    CONSTRAINT billing_usage_charges_response_bytes_check CHECK (((response_bytes IS NULL) OR (response_bytes >= 0))),
    CONSTRAINT billing_usage_charges_response_digest_check CHECK (((response_digest IS NULL) OR (octet_length(response_digest) = 32))),
    CONSTRAINT billing_usage_charges_state_check CHECK ((state = ANY (ARRAY['reserved'::text, 'committed'::text, 'released'::text, 'expired'::text])))
);

-- Table: block_journals

CREATE TABLE block_journals (
    chain_id numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    stage text NOT NULL,
    sequence numeric(78,0) NOT NULL,
    payload jsonb NOT NULL,
    canonical boolean NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    durable_job_id bigint,
    job_generation bigint,
    CONSTRAINT block_journals_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT block_journals_publication_pair_check CHECK ((((durable_job_id IS NULL) AND (job_generation IS NULL)) OR ((durable_job_id IS NOT NULL) AND (job_generation IS NOT NULL) AND (job_generation > 0))))
);

-- Table: block_stage_results

CREATE TABLE block_stage_results (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    stage text NOT NULL,
    stage_version integer NOT NULL,
    state text NOT NULL,
    details jsonb DEFAULT '{}'::jsonb NOT NULL,
    last_error text,
    completed_at timestamp with time zone DEFAULT now() NOT NULL,
    durable_job_id bigint,
    job_generation bigint,
    CONSTRAINT block_stage_results_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT block_stage_results_publication_pair_check CHECK ((((durable_job_id IS NULL) AND (job_generation IS NULL)) OR ((durable_job_id IS NOT NULL) AND (job_generation IS NOT NULL) AND (job_generation > 0)))),
    CONSTRAINT block_stage_results_stage_version_check CHECK ((stage_version > 0)),
    CONSTRAINT block_stage_results_state_check CHECK ((state = ANY (ARRAY['complete'::text, 'unavailable'::text, 'failed'::text])))
);

-- Table: block_statistics

CREATE TABLE block_statistics (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    transaction_count bigint NOT NULL,
    gas_used numeric(78,0) NOT NULL,
    gas_limit numeric(78,0) NOT NULL,
    base_fee_per_gas numeric(78,0),
    blob_gas_used numeric(78,0),
    burned_wei numeric(78,0),
    canonical boolean NOT NULL,
    computed_at timestamp with time zone DEFAULT now() NOT NULL,
    block_timestamp numeric(78,0),
    block_interval_seconds numeric(78,0),
    transactions_per_second numeric(78,18),
    excess_blob_gas numeric(78,0),
    blob_base_fee_per_gas numeric(78,0),
    blob_burned_wei numeric(78,0),
    execution_gas_fee_wei numeric(78,0),
    priority_fee_wei numeric(78,0),
    failed_transaction_count bigint,
    contract_creation_count bigint,
    CONSTRAINT block_statistics_base_fee_per_gas_check CHECK (((base_fee_per_gas IS NULL) OR (base_fee_per_gas >= (0)::numeric))),
    CONSTRAINT block_statistics_blob_gas_used_check CHECK (((blob_gas_used IS NULL) OR (blob_gas_used >= (0)::numeric))),
    CONSTRAINT block_statistics_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT block_statistics_burned_wei_check CHECK (((burned_wei IS NULL) OR (burned_wei >= (0)::numeric))),
    CONSTRAINT block_statistics_check CHECK (((gas_used >= (0)::numeric) AND (gas_limit >= (0)::numeric))),
    CONSTRAINT block_statistics_transaction_count_check CHECK ((transaction_count >= 0)),
    CONSTRAINT block_statistics_v2_blob_check CHECK ((((excess_blob_gas IS NULL) OR (excess_blob_gas >= (0)::numeric)) AND ((blob_base_fee_per_gas IS NULL) OR (blob_base_fee_per_gas >= (0)::numeric)) AND ((blob_burned_wei IS NULL) OR (blob_burned_wei >= (0)::numeric)))),
    CONSTRAINT block_statistics_v2_interval_check CHECK (((block_interval_seconds IS NULL) OR (block_interval_seconds > (0)::numeric))),
    CONSTRAINT block_statistics_v2_nonnegative_check CHECK (((block_timestamp IS NULL) OR (block_timestamp >= (0)::numeric))),
    CONSTRAINT block_statistics_v2_tps_check CHECK (((transactions_per_second IS NULL) OR (transactions_per_second >= (0)::numeric))),
    CONSTRAINT block_statistics_v3_execution_check CHECK ((((execution_gas_fee_wei IS NULL) OR (execution_gas_fee_wei >= (0)::numeric)) AND ((priority_fee_wei IS NULL) OR (priority_fee_wei >= (0)::numeric)) AND ((failed_transaction_count IS NULL) OR (failed_transaction_count >= 0)) AND ((contract_creation_count IS NULL) OR (contract_creation_count >= 0)) AND ((failed_transaction_count IS NULL) OR (failed_transaction_count <= transaction_count)) AND ((contract_creation_count IS NULL) OR (contract_creation_count <= transaction_count))))
);

-- Table: blocks

CREATE TABLE blocks (
    chain_id numeric(78,0) NOT NULL,
    number numeric(78,0) NOT NULL,
    hash bytea NOT NULL,
    parent_hash bytea NOT NULL,
    "timestamp" numeric(78,0) NOT NULL,
    raw jsonb NOT NULL,
    inserted_at timestamp with time zone DEFAULT now() NOT NULL,
    miner_text text GENERATED ALWAYS AS ((raw ->> 'miner'::text)) STORED,
    gas_used_quantity text GENERATED ALWAYS AS ((raw ->> 'gasUsed'::text)) STORED,
    gas_limit_quantity text GENERATED ALWAYS AS ((raw ->> 'gasLimit'::text)) STORED,
    base_fee_per_gas_quantity text GENERATED ALWAYS AS ((raw ->> 'baseFeePerGas'::text)) STORED,
    transaction_count bigint GENERATED ALWAYS AS (
CASE
    WHEN (jsonb_typeof((raw -> 'transactions'::text)) = 'array'::text) THEN (jsonb_array_length((raw -> 'transactions'::text)))::bigint
    ELSE NULL::bigint
END) STORED,
    withdrawals_present boolean GENERATED ALWAYS AS (((raw ? 'withdrawals'::text) AND ((raw -> 'withdrawals'::text) <> 'null'::jsonb))) STORED,
    withdrawal_count bigint GENERATED ALWAYS AS (
CASE
    WHEN (jsonb_typeof((raw -> 'withdrawals'::text)) = 'array'::text) THEN (jsonb_array_length((raw -> 'withdrawals'::text)))::bigint
    ELSE NULL::bigint
END) STORED,
    slot_number_raw jsonb GENERATED ALWAYS AS ((raw -> 'slotNumber'::text)) STORED,
    block_access_list_hash_raw jsonb GENERATED ALWAYS AS ((raw -> 'blockAccessListHash'::text)) STORED,
    CONSTRAINT blocks_hash_check CHECK ((octet_length(hash) = 32)),
    CONSTRAINT blocks_number_check CHECK ((number >= (0)::numeric)),
    CONSTRAINT blocks_parent_hash_check CHECK ((octet_length(parent_hash) = 32)),
    CONSTRAINT blocks_timestamp_check CHECK (("timestamp" >= (0)::numeric))
);

-- Table: canonical_blocks

CREATE TABLE canonical_blocks (
    chain_id numeric(78,0) NOT NULL,
    number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

-- Table: diamond_cut_events

CREATE TABLE diamond_cut_events (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    transaction_hash bytea NOT NULL,
    transaction_index bigint NOT NULL,
    log_index bigint NOT NULL,
    diamond_address bytea NOT NULL,
    init_address bytea NOT NULL,
    init_calldata bytea NOT NULL,
    cuts jsonb NOT NULL,
    stage_version integer DEFAULT 2 NOT NULL,
    canonical boolean NOT NULL,
    CONSTRAINT diamond_cut_events_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT diamond_cut_events_cuts_check CHECK ((jsonb_typeof(cuts) = 'array'::text)),
    CONSTRAINT diamond_cut_events_diamond_address_check CHECK ((octet_length(diamond_address) = 20)),
    CONSTRAINT diamond_cut_events_init_address_check CHECK ((octet_length(init_address) = 20)),
    CONSTRAINT diamond_cut_events_log_index_check CHECK ((log_index >= 0)),
    CONSTRAINT diamond_cut_events_stage_version_check CHECK ((stage_version = 2)),
    CONSTRAINT diamond_cut_events_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT diamond_cut_events_transaction_index_check CHECK ((transaction_index >= 0))
);

-- Table: diamond_selector_changes

CREATE TABLE diamond_selector_changes (
    chain_id numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    log_index bigint NOT NULL,
    stage_version integer DEFAULT 2 NOT NULL,
    cut_index integer NOT NULL,
    selector_index integer NOT NULL,
    selector bytea NOT NULL,
    action smallint NOT NULL,
    facet_address bytea NOT NULL,
    CONSTRAINT diamond_selector_changes_action_check CHECK ((action = ANY (ARRAY[0, 1, 2]))),
    CONSTRAINT diamond_selector_changes_cut_index_check CHECK ((cut_index >= 0)),
    CONSTRAINT diamond_selector_changes_facet_address_check CHECK ((octet_length(facet_address) = 20)),
    CONSTRAINT diamond_selector_changes_selector_check CHECK ((octet_length(selector) = 4)),
    CONSTRAINT diamond_selector_changes_selector_index_check CHECK ((selector_index >= 0))
);

-- Table: durable_jobs

CREATE TABLE durable_jobs (
    id bigint NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    kind text NOT NULL,
    stage text NOT NULL,
    stage_version integer DEFAULT 1 NOT NULL,
    idempotency_key text NOT NULL,
    payload jsonb DEFAULT '{}'::jsonb NOT NULL,
    priority integer DEFAULT 0 NOT NULL,
    status text DEFAULT 'queued'::text NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    max_attempts integer DEFAULT 10 NOT NULL,
    available_at timestamp with time zone DEFAULT now() NOT NULL,
    leased_by text,
    lease_token text,
    lease_expires_at timestamp with time zone,
    result jsonb,
    last_error text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    requested_generation bigint DEFAULT 1 NOT NULL,
    claimed_generation bigint DEFAULT 0 NOT NULL,
    leased_generation bigint,
    completed_generation bigint DEFAULT 0 NOT NULL,
    CONSTRAINT durable_jobs_check CHECK (((attempts >= 0) AND (max_attempts > 0))),
    CONSTRAINT durable_jobs_check1 CHECK (((leased_by IS NULL) = (lease_expires_at IS NULL))),
    CONSTRAINT durable_jobs_check2 CHECK (((leased_by IS NULL) = (lease_token IS NULL))),
    CONSTRAINT durable_jobs_generation_check CHECK (((requested_generation > 0) AND ((claimed_generation >= 0) AND (claimed_generation <= requested_generation)) AND ((completed_generation >= 0) AND (completed_generation <= requested_generation)) AND ((leased_generation IS NULL) OR ((leased_generation >= 1) AND (leased_generation <= requested_generation))))),
    CONSTRAINT durable_jobs_metadata_payload_size_check CHECK (((kind <> 'metadata'::text) OR (octet_length((payload)::text) <= 8192))),
    CONSTRAINT durable_jobs_metadata_stage_check CHECK (((kind <> 'metadata'::text) OR ((stage = 'nft-metadata'::text) AND (stage_version = 1)))),
    CONSTRAINT durable_jobs_stage_version_check CHECK ((stage_version > 0)),
    CONSTRAINT durable_jobs_status_check CHECK ((status = ANY (ARRAY['queued'::text, 'leased'::text, 'succeeded'::text, 'failed'::text, 'cancelled'::text])))
);

-- Table: transactional_outbox

CREATE TABLE transactional_outbox (
    id bigint NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    topic text NOT NULL,
    message_key text NOT NULL,
    payload jsonb NOT NULL,
    available_at timestamp with time zone DEFAULT now() NOT NULL,
    published_at timestamp with time zone,
    attempts integer DEFAULT 0 NOT NULL,
    last_error text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    generation bigint DEFAULT 1 NOT NULL,
    CONSTRAINT transactional_outbox_attempts_check CHECK ((attempts >= 0)),
    CONSTRAINT transactional_outbox_generation_check CHECK ((generation > 0))
);

-- View: published_block_stage_results

CREATE VIEW published_block_stage_results AS
 SELECT result.chain_id,
    result.block_number,
    result.block_hash,
    result.stage,
    result.stage_version,
    result.state,
    result.details,
    result.last_error,
    result.completed_at,
    result.durable_job_id,
    result.job_generation
   FROM (block_stage_results result
     JOIN durable_jobs job ON (((job.id = result.durable_job_id) AND (job.chain_id = result.chain_id) AND (job.kind = 'enrichment'::text) AND (job.stage = result.stage) AND (job.stage_version = result.stage_version) AND ((job.payload ->> 'block_hash'::text) = ('0x'::text || encode(result.block_hash, 'hex'::text))) AND ((job.payload ->> 'block_number'::text) = (result.block_number)::text))))
  WHERE ((result.durable_job_id IS NOT NULL) AND (result.job_generation IS NOT NULL) AND (job.status = ANY (ARRAY['succeeded'::text, 'failed'::text])) AND (job.claimed_generation = result.job_generation) AND (job.completed_generation = result.job_generation) AND (job.requested_generation = result.job_generation) AND (job.leased_generation IS NULL) AND (job.leased_by IS NULL) AND (job.lease_token IS NULL) AND (job.lease_expires_at IS NULL) AND (job.result IS NOT NULL) AND ((job.result ->> 'state'::text) = result.state) AND (COALESCE((result.details ->> 'outcome'::text), ''::text) <> 'stale_canonical_skipped'::text) AND (NOT (EXISTS ( SELECT 1
           FROM transactional_outbox pending_attach
          WHERE ((pending_attach.chain_id = result.chain_id) AND (pending_attach.topic = 'core.block.canonical'::text) AND (pending_attach.message_key = ('0x'::text || encode(result.block_hash, 'hex'::text))) AND (pending_attach.published_at IS NULL))))) AND (((job.status = 'succeeded'::text) AND (result.state = 'complete'::text) AND (EXISTS ( SELECT 1
           FROM block_journals journal
          WHERE ((journal.chain_id = result.chain_id) AND (journal.block_hash = result.block_hash) AND (journal.stage = ((result.stage || '@'::text) || (result.stage_version)::text)) AND (journal.sequence = (1)::numeric) AND (journal.durable_job_id = result.durable_job_id) AND (journal.job_generation = result.job_generation))))) OR ((job.status = 'failed'::text) AND (result.state = ANY (ARRAY['failed'::text, 'unavailable'::text])))) AND (COALESCE((job.result -> 'details'::text), '{}'::jsonb) = result.details) AND (NOT ((job.result ->> 'error'::text) IS DISTINCT FROM result.last_error)));

-- View: canonical_diamond_selector_intervals

CREATE VIEW canonical_diamond_selector_intervals AS
 WITH ordered AS (
         SELECT event.chain_id,
            event.diamond_address,
            change.selector,
            change.facet_address,
            change.action,
            event.block_number AS valid_from_block_number,
            event.block_hash AS valid_from_block_hash,
            event.transaction_index AS valid_from_transaction_index,
            event.log_index AS valid_from_log_index,
            change.cut_index AS valid_from_cut_index,
            change.selector_index AS valid_from_selector_index,
            lead(event.block_number) OVER route AS valid_to_block_number,
            lead(event.block_hash) OVER route AS valid_to_block_hash,
            lead(event.transaction_index) OVER route AS valid_to_transaction_index,
            lead(event.log_index) OVER route AS valid_to_log_index,
            lead(change.cut_index) OVER route AS valid_to_cut_index,
            lead(change.selector_index) OVER route AS valid_to_selector_index
           FROM (((diamond_cut_events event
             JOIN diamond_selector_changes change ON (((change.chain_id = event.chain_id) AND (change.block_hash = event.block_hash) AND (change.log_index = event.log_index) AND (change.stage_version = event.stage_version))))
             JOIN canonical_blocks canonical ON (((canonical.chain_id = event.chain_id) AND (canonical.number = event.block_number) AND (canonical.block_hash = event.block_hash))))
             JOIN published_block_stage_results published ON (((published.chain_id = event.chain_id) AND (published.block_number = event.block_number) AND (published.block_hash = event.block_hash) AND (published.stage = 'proxy'::text) AND (published.stage_version = event.stage_version) AND (published.state = 'complete'::text))))
          WHERE (event.canonical AND (event.stage_version = 2))
          WINDOW route AS (PARTITION BY event.chain_id, event.diamond_address, change.selector ORDER BY event.block_number, event.transaction_index, event.log_index, change.cut_index, change.selector_index)
        )
 SELECT chain_id,
    diamond_address,
    selector,
    facet_address,
    valid_from_block_number,
    valid_from_block_hash,
    valid_from_transaction_index,
    valid_from_log_index,
    valid_from_cut_index,
    valid_from_selector_index,
    valid_to_block_number,
    valid_to_block_hash,
    valid_to_transaction_index,
    valid_to_log_index,
    valid_to_cut_index,
    valid_to_selector_index
   FROM ordered
  WHERE (action <> 2);

-- Table: chain_finality

CREATE TABLE chain_finality (
    chain_id numeric(78,0) NOT NULL,
    safe_number numeric(78,0),
    safe_hash bytea,
    finalized_number numeric(78,0),
    finalized_hash bytea,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT chain_finality_check CHECK (((safe_number IS NULL) = (safe_hash IS NULL))),
    CONSTRAINT chain_finality_check1 CHECK (((finalized_number IS NULL) = (finalized_hash IS NULL))),
    CONSTRAINT chain_finality_finalized_hash_check CHECK (((finalized_hash IS NULL) OR (octet_length(finalized_hash) = 32))),
    CONSTRAINT chain_finality_safe_hash_check CHECK (((safe_hash IS NULL) OR (octet_length(safe_hash) = 32)))
);

-- Table: chains

CREATE TABLE chains (
    chain_id numeric(78,0) NOT NULL,
    genesis_hash bytea,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT chains_chain_id_check CHECK ((chain_id >= (0)::numeric)),
    CONSTRAINT chains_genesis_hash_check CHECK (((genesis_hash IS NULL) OR (octet_length(genesis_hash) = 32)))
);

-- Table: chart_hourly_rollups

CREATE TABLE chart_hourly_rollups (
    chain_id numeric(78,0) NOT NULL,
    bucket_start timestamp with time zone NOT NULL,
    source_generation bigint NOT NULL,
    from_block numeric(78,0) NOT NULL,
    to_block numeric(78,0) NOT NULL,
    block_count bigint NOT NULL,
    transaction_count numeric(78,0) NOT NULL,
    failed_transaction_count numeric(78,0) NOT NULL,
    contract_creation_count numeric(78,0) NOT NULL,
    gas_used numeric(78,0) NOT NULL,
    gas_limit numeric(78,0) NOT NULL,
    block_interval_seconds numeric(78,0) NOT NULL,
    block_interval_samples bigint NOT NULL,
    base_fee_per_gas_sum numeric(78,0) NOT NULL,
    base_fee_samples bigint NOT NULL,
    execution_gas_fee_wei numeric(78,0) NOT NULL,
    priority_fee_wei numeric(78,0) NOT NULL,
    burned_wei numeric(78,0) NOT NULL,
    blob_gas_used numeric(78,0) NOT NULL,
    blob_base_fee_per_gas_sum numeric(78,0) NOT NULL,
    blob_base_fee_samples bigint NOT NULL,
    blob_burned_wei numeric(78,0) NOT NULL,
    erc20_transfer_count numeric(78,0) NOT NULL,
    nft_transfer_count numeric(78,0) NOT NULL,
    computed_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT chart_hourly_rollups_bucket_start_check CHECK ((date_trunc('hour'::text, bucket_start, 'UTC'::text) = bucket_start)),
    CONSTRAINT chart_hourly_rollups_check CHECK (((from_block >= (0)::numeric) AND (to_block >= from_block))),
    CONSTRAINT chart_hourly_rollups_check1 CHECK (((block_count > 0) AND (transaction_count >= (0)::numeric) AND (failed_transaction_count >= (0)::numeric) AND (contract_creation_count >= (0)::numeric) AND (gas_used >= (0)::numeric) AND (gas_limit >= (0)::numeric) AND (block_interval_seconds >= (0)::numeric) AND (block_interval_samples >= 0) AND (base_fee_per_gas_sum >= (0)::numeric) AND (base_fee_samples >= 0) AND (execution_gas_fee_wei >= (0)::numeric) AND (priority_fee_wei >= (0)::numeric) AND (burned_wei >= (0)::numeric) AND (blob_gas_used >= (0)::numeric) AND (blob_base_fee_per_gas_sum >= (0)::numeric) AND (blob_base_fee_samples >= 0) AND (blob_burned_wei >= (0)::numeric) AND (erc20_transfer_count >= (0)::numeric) AND (nft_transfer_count >= (0)::numeric))),
    CONSTRAINT chart_hourly_rollups_source_generation_check CHECK ((source_generation > 0))
);

-- Table: chart_rollup_backfill

CREATE TABLE chart_rollup_backfill (
    chain_id numeric(78,0) NOT NULL,
    available_from timestamp with time zone,
    available_to timestamp with time zone,
    next_block numeric(78,0),
    target_start_block numeric(78,0),
    completed_blocks numeric(78,0) DEFAULT 0 NOT NULL,
    total_blocks numeric(78,0) DEFAULT 0 NOT NULL,
    complete boolean DEFAULT false NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT chart_rollup_backfill_check CHECK (((available_from IS NULL) = (available_to IS NULL))),
    CONSTRAINT chart_rollup_backfill_check1 CHECK (((available_to IS NULL) OR (available_to >= available_from))),
    CONSTRAINT chart_rollup_backfill_check2 CHECK (((completed_blocks >= (0)::numeric) AND (total_blocks >= completed_blocks))),
    CONSTRAINT chart_rollup_backfill_next_block_check CHECK (((next_block IS NULL) OR (next_block >= (0)::numeric))),
    CONSTRAINT chart_rollup_backfill_target_start_block_check CHECK (((target_start_block IS NULL) OR (target_start_block >= (0)::numeric)))
);

-- Table: chart_rollup_dirty_hours

CREATE TABLE chart_rollup_dirty_hours (
    chain_id numeric(78,0) NOT NULL,
    bucket_start timestamp with time zone NOT NULL,
    generation bigint DEFAULT 1 NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    next_attempt_at timestamp with time zone DEFAULT now() NOT NULL,
    dirtied_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT chart_rollup_dirty_hours_bucket_start_check CHECK ((date_trunc('hour'::text, bucket_start, 'UTC'::text) = bucket_start)),
    CONSTRAINT chart_rollup_dirty_hours_check CHECK (((generation > 0) AND (attempts >= 0)))
);

-- Table: compiler_catalog_entries

CREATE TABLE compiler_catalog_entries (
    generation_id bigint NOT NULL,
    language text NOT NULL,
    version text NOT NULL,
    platform text DEFAULT 'linux-amd64'::text NOT NULL,
    artifact_url text NOT NULL,
    artifact_sha256 bytea NOT NULL,
    max_bytes bigint NOT NULL,
    vyper_runtimes jsonb,
    expires_at timestamp with time zone,
    CONSTRAINT compiler_catalog_entries_digest_check CHECK ((octet_length(artifact_sha256) = 32)),
    CONSTRAINT compiler_catalog_entries_language_check CHECK ((language = ANY (ARRAY['solidity'::text, 'vyper'::text]))),
    CONSTRAINT compiler_catalog_entries_platform_check CHECK ((platform = ANY (ARRAY['bin'::text, 'emscripten-asmjs'::text, 'emscripten-wasm32'::text, 'linux-amd64'::text, 'linux-arm64'::text, 'macosx-amd64'::text, 'wasm'::text, 'windows-amd64'::text, 'python-wheel'::text]))),
    CONSTRAINT compiler_catalog_entries_size_check CHECK (((max_bytes >= 1) AND (max_bytes <= 209715200))),
    CONSTRAINT compiler_catalog_entries_url_check CHECK (((length(artifact_url) >= 9) AND (length(artifact_url) <= 4096))),
    CONSTRAINT compiler_catalog_entries_version_check CHECK (((length(version) >= 1) AND (length(version) <= 128)))
);

-- Table: compiler_catalog_generations

CREATE TABLE compiler_catalog_generations (
    id bigint NOT NULL,
    language text NOT NULL,
    source_url text NOT NULL,
    catalog_digest bytea NOT NULL,
    fetched_at timestamp with time zone DEFAULT now() NOT NULL,
    entry_count integer NOT NULL,
    CONSTRAINT compiler_catalog_generations_count_check CHECK (((entry_count >= 1) AND (entry_count <= 4096))),
    CONSTRAINT compiler_catalog_generations_digest_check CHECK ((octet_length(catalog_digest) = 32)),
    CONSTRAINT compiler_catalog_generations_language_check CHECK ((language = ANY (ARRAY['solidity'::text, 'vyper'::text]))),
    CONSTRAINT compiler_catalog_generations_source_check CHECK (((length(source_url) >= 9) AND (length(source_url) <= 4096)))
);

-- Sequence: compiler_catalog_generations_id_seq

ALTER TABLE compiler_catalog_generations ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME compiler_catalog_generations_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

-- Table: compiler_catalog_heads

CREATE TABLE compiler_catalog_heads (
    language text NOT NULL,
    generation_id bigint NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT compiler_catalog_heads_language_check CHECK ((language = ANY (ARRAY['solidity'::text, 'vyper'::text])))
);

-- Table: contract_abis

CREATE TABLE contract_abis (
    chain_id numeric(78,0) NOT NULL,
    address bytea NOT NULL,
    code_hash bytea NOT NULL,
    source text NOT NULL,
    confidence text NOT NULL,
    abi jsonb NOT NULL,
    valid_from_block numeric(78,0) NOT NULL,
    valid_to_block numeric(78,0),
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    source_address bytea NOT NULL,
    source_code_hash bytea NOT NULL,
    canonical boolean NOT NULL,
    selector_scope bytea DEFAULT decode(repeat('00'::text, 32), 'hex'::text) NOT NULL,
    CONSTRAINT contract_abis_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT contract_abis_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT contract_abis_check CHECK (((valid_to_block IS NULL) OR (valid_to_block >= valid_from_block))),
    CONSTRAINT contract_abis_check1 CHECK ((block_number >= valid_from_block)),
    CONSTRAINT contract_abis_check2 CHECK (((valid_to_block IS NULL) OR (block_number <= valid_to_block))),
    CONSTRAINT contract_abis_code_hash_check CHECK ((octet_length(code_hash) = 32)),
    CONSTRAINT contract_abis_confidence_v3_check CHECK ((((source = 'verified'::text) AND (confidence = 'verified'::text)) OR ((source = ANY (ARRAY['code_hash'::text, 'proxy_implementation'::text, 'diamond_facet'::text])) AND (confidence = 'high'::text)) OR ((source = 'signature_database'::text) AND (confidence = 'guess'::text)))),
    CONSTRAINT contract_abis_selector_scope_v3_check CHECK (((octet_length(selector_scope) = 32) AND (((source = 'diamond_facet'::text) AND (selector_scope <> decode(repeat('00'::text, 32), 'hex'::text))) OR ((source <> 'diamond_facet'::text) AND (selector_scope = decode(repeat('00'::text, 32), 'hex'::text)))))),
    CONSTRAINT contract_abis_source_address_check CHECK ((octet_length(source_address) = 20)),
    CONSTRAINT contract_abis_source_code_hash_check CHECK ((octet_length(source_code_hash) = 32)),
    CONSTRAINT contract_abis_source_identity_v3_check CHECK (((source = ANY (ARRAY['code_hash'::text, 'proxy_implementation'::text, 'diamond_facet'::text])) OR ((source_address = address) AND (source_code_hash = code_hash)))),
    CONSTRAINT contract_abis_source_v3_check CHECK ((source = ANY (ARRAY['verified'::text, 'code_hash'::text, 'proxy_implementation'::text, 'diamond_facet'::text, 'signature_database'::text]))),
    CONSTRAINT contract_abis_valid_from_block_check CHECK ((valid_from_block >= (0)::numeric))
);

-- Table: contract_code_observations

CREATE TABLE contract_code_observations (
    chain_id numeric(78,0) NOT NULL,
    address bytea NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    code_hash bytea NOT NULL,
    code bytea,
    canonical boolean NOT NULL,
    observed_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT contract_code_observations_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT contract_code_observations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT contract_code_observations_code_hash_check CHECK ((octet_length(code_hash) = 32))
);

-- Table: core_backfill_leases

CREATE TABLE core_backfill_leases (
    chain_id numeric(78,0) NOT NULL,
    range_start numeric(78,0) NOT NULL,
    range_end numeric(78,0) NOT NULL,
    owner text NOT NULL,
    lease_token uuid NOT NULL,
    claimed_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT core_backfill_leases_check CHECK ((range_end >= range_start)),
    CONSTRAINT core_backfill_leases_check1 CHECK (((range_end - range_start) < (256)::numeric)),
    CONSTRAINT core_backfill_leases_check2 CHECK ((expires_at > claimed_at)),
    CONSTRAINT core_backfill_leases_owner_check CHECK (((length(owner) >= 1) AND (length(owner) <= 128))),
    CONSTRAINT core_backfill_leases_range_start_check CHECK ((range_start >= (0)::numeric))
);

-- Table: core_coverage_ranges

CREATE TABLE core_coverage_ranges (
    chain_id numeric(78,0) NOT NULL,
    range_start numeric(78,0) NOT NULL,
    range_end numeric(78,0) NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT core_coverage_ranges_check CHECK ((range_end >= range_start)),
    CONSTRAINT core_coverage_ranges_range_start_check CHECK ((range_start >= (0)::numeric))
);

-- Table: core_index_configuration

CREATE TABLE core_index_configuration (
    chain_id numeric(78,0) NOT NULL,
    configured_start numeric(78,0) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT core_index_configuration_configured_start_check CHECK ((configured_start >= (0)::numeric))
);

-- Table: derived_verification_attempts

CREATE TABLE derived_verification_attempts (
    id uuid NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    transaction_hash bytea NOT NULL,
    trace_path text NOT NULL,
    creator_address bytea NOT NULL,
    created_address bytea NOT NULL,
    call_type text NOT NULL,
    compilation_id uuid NOT NULL,
    file_name text,
    contract_name text,
    status text NOT NULL,
    creation_match jsonb,
    runtime_match jsonb,
    verification_job_id uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    stale_from_status text,
    CONSTRAINT derived_verification_attempts_identity_check CHECK (((octet_length(block_hash) = 32) AND (octet_length(transaction_hash) = 32) AND ((length(trace_path) >= 1) AND (length(trace_path) <= 2048)) AND (octet_length(creator_address) = 20) AND (octet_length(created_address) = 20) AND (call_type = ANY (ARRAY['CREATE'::text, 'CREATE2'::text])))),
    CONSTRAINT derived_verification_attempts_outcome_check CHECK ((((status = 'matched'::text) AND (stale_from_status IS NULL) AND (file_name IS NOT NULL) AND (contract_name IS NOT NULL) AND (jsonb_typeof(creation_match) = 'object'::text) AND (jsonb_typeof(runtime_match) = 'object'::text) AND (verification_job_id IS NOT NULL)) OR ((status <> ALL (ARRAY['matched'::text, 'stale'::text])) AND (stale_from_status IS NULL) AND (file_name IS NULL) AND (contract_name IS NULL) AND (creation_match IS NULL) AND (runtime_match IS NULL) AND (verification_job_id IS NULL)) OR ((status = 'stale'::text) AND (stale_from_status IS NULL) AND (file_name IS NULL) AND (contract_name IS NULL) AND (creation_match IS NULL) AND (runtime_match IS NULL) AND (verification_job_id IS NULL)) OR ((status = 'stale'::text) AND (stale_from_status IS NOT NULL) AND (((stale_from_status = 'matched'::text) AND (file_name IS NOT NULL) AND (contract_name IS NOT NULL) AND (jsonb_typeof(creation_match) = 'object'::text) AND (jsonb_typeof(runtime_match) = 'object'::text) AND (verification_job_id IS NOT NULL)) OR ((stale_from_status <> 'matched'::text) AND (file_name IS NULL) AND (contract_name IS NULL) AND (creation_match IS NULL) AND (runtime_match IS NULL) AND (verification_job_id IS NULL)))))),
    CONSTRAINT derived_verification_attempts_status_check CHECK (((status = ANY (ARRAY['pending_runtime'::text, 'matched'::text, 'ambiguous'::text, 'no_match'::text, 'runtime_mismatch'::text, 'stale'::text])) AND ((stale_from_status IS NULL) OR (stale_from_status = ANY (ARRAY['pending_runtime'::text, 'matched'::text, 'ambiguous'::text, 'no_match'::text, 'runtime_mismatch'::text])))))
);

-- Table: derived_verification_backfill_requests

CREATE TABLE derived_verification_backfill_requests (
    id bigint NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    creator_address bytea,
    reason text NOT NULL,
    scan_count integer DEFAULT 0 NOT NULL,
    requested_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT derived_verification_backfill_requests_shape_check CHECK ((((creator_address IS NULL) OR (octet_length(creator_address) = 20)) AND ((length(reason) >= 1) AND (length(reason) <= 512)) AND (scan_count >= 0)))
);

-- Sequence: derived_verification_backfill_requests_id_seq

ALTER TABLE derived_verification_backfill_requests ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME derived_verification_backfill_requests_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

-- Table: derived_verification_forward_blocks

CREATE TABLE derived_verification_forward_blocks (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    status text DEFAULT 'queued'::text NOT NULL,
    leased_by text,
    lease_token text,
    lease_expires_at timestamp with time zone,
    attempt_count integer DEFAULT 0 NOT NULL,
    max_attempts integer DEFAULT 5 NOT NULL,
    last_error text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    id bigint NOT NULL,
    source_stage text,
    source_job_id bigint,
    source_generation bigint,
    CONSTRAINT derived_verification_forward_blocks_attempt_check CHECK (((attempt_count >= 0) AND ((max_attempts >= 1) AND (max_attempts <= 100)) AND (attempt_count <= max_attempts))),
    CONSTRAINT derived_verification_forward_blocks_identity_check CHECK (((block_number >= (0)::numeric) AND (octet_length(block_hash) = 32))),
    CONSTRAINT derived_verification_forward_blocks_lease_check CHECK ((((leased_by IS NULL) = (lease_token IS NULL)) AND ((leased_by IS NULL) = (lease_expires_at IS NULL)) AND ((status = 'running'::text) = (leased_by IS NOT NULL)))),
    CONSTRAINT derived_verification_forward_blocks_source_check CHECK ((((source_stage = ANY (ARRAY['trace'::text, 'proxy'::text])) AND (source_job_id IS NOT NULL) AND (source_generation IS NOT NULL) AND (source_generation > 0)) OR ((status = 'failed'::text) AND (last_error = 'unbound_source_generation'::text) AND (source_stage IS NULL) AND (source_job_id IS NULL) AND (source_generation IS NULL)))),
    CONSTRAINT derived_verification_forward_blocks_status_check CHECK ((status = ANY (ARRAY['queued'::text, 'running'::text, 'succeeded'::text, 'failed'::text])))
);

-- Sequence: derived_verification_forward_blocks_id_seq

ALTER TABLE derived_verification_forward_blocks ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME derived_verification_forward_blocks_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

-- Table: derived_verification_scans

CREATE TABLE derived_verification_scans (
    compilation_id uuid NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    creator_address bytea NOT NULL,
    creator_code_hash bytea NOT NULL,
    valid_from_block numeric(78,0) NOT NULL,
    valid_to_block numeric(78,0),
    cursor_block_number numeric(78,0) NOT NULL,
    cursor_transaction_hash bytea DEFAULT decode(repeat('00'::text, 32), 'hex'::text) NOT NULL,
    cursor_trace_path text DEFAULT ''::text NOT NULL,
    status text DEFAULT 'queued'::text NOT NULL,
    leased_by text,
    lease_token text,
    lease_expires_at timestamp with time zone,
    attempt_count integer DEFAULT 0 NOT NULL,
    max_attempts integer DEFAULT 5 NOT NULL,
    last_error text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    id bigint NOT NULL,
    rescan_from_block numeric(78,0),
    CONSTRAINT derived_verification_scans_attempt_check CHECK (((attempt_count >= 0) AND ((max_attempts >= 1) AND (max_attempts <= 100)) AND (attempt_count <= max_attempts))),
    CONSTRAINT derived_verification_scans_identity_check CHECK (((octet_length(creator_address) = 20) AND (octet_length(creator_code_hash) = 32) AND (valid_from_block >= (0)::numeric) AND ((valid_to_block IS NULL) OR (valid_to_block >= valid_from_block)) AND (cursor_block_number >= valid_from_block) AND (octet_length(cursor_transaction_hash) = 32) AND (length(cursor_trace_path) <= 2048))),
    CONSTRAINT derived_verification_scans_lease_check CHECK ((((leased_by IS NULL) = (lease_token IS NULL)) AND ((leased_by IS NULL) = (lease_expires_at IS NULL)) AND ((status = 'running'::text) = (leased_by IS NOT NULL)))),
    CONSTRAINT derived_verification_scans_rescan_check CHECK (((rescan_from_block IS NULL) OR ((rescan_from_block >= valid_from_block) AND ((valid_to_block IS NULL) OR (rescan_from_block <= valid_to_block))))),
    CONSTRAINT derived_verification_scans_status_check CHECK ((status = ANY (ARRAY['queued'::text, 'running'::text, 'succeeded'::text, 'failed'::text])))
);

-- Sequence: derived_verification_scans_id_seq

ALTER TABLE derived_verification_scans ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME derived_verification_scans_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

-- Table: diamond_loupe_facets

CREATE TABLE diamond_loupe_facets (
    snapshot_id bigint NOT NULL,
    facet_address bytea NOT NULL,
    facet_kind text NOT NULL,
    code_exists boolean NOT NULL,
    code_hash bytea,
    CONSTRAINT diamond_loupe_facets_check CHECK ((((facet_kind = 'facet'::text) AND (code_exists = (code_hash IS NOT NULL))) OR (facet_kind = 'immutable'::text))),
    CONSTRAINT diamond_loupe_facets_code_hash_check CHECK (((code_hash IS NULL) OR (octet_length(code_hash) = 32))),
    CONSTRAINT diamond_loupe_facets_facet_address_check CHECK ((octet_length(facet_address) = 20)),
    CONSTRAINT diamond_loupe_facets_facet_kind_check CHECK ((facet_kind = ANY (ARRAY['facet'::text, 'immutable'::text])))
);

-- Table: diamond_loupe_selectors

CREATE TABLE diamond_loupe_selectors (
    snapshot_id bigint NOT NULL,
    selector bytea NOT NULL,
    facet_address bytea NOT NULL,
    CONSTRAINT diamond_loupe_selectors_facet_address_check CHECK ((octet_length(facet_address) = 20)),
    CONSTRAINT diamond_loupe_selectors_selector_check CHECK ((octet_length(selector) = 4))
);

-- Table: diamond_loupe_snapshots

CREATE TABLE diamond_loupe_snapshots (
    id bigint NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    diamond_address bytea NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    stage_version integer DEFAULT 2 NOT NULL,
    detection_state text NOT NULL,
    completeness text NOT NULL,
    validation text NOT NULL,
    standard_diamond_cut text NOT NULL,
    standard_diamond_cut_facet bytea,
    loupe_interface_reported boolean,
    truncated boolean DEFAULT false NOT NULL,
    truncation_reason text,
    warnings jsonb DEFAULT '[]'::jsonb NOT NULL,
    canonical boolean NOT NULL,
    durable_job_id bigint,
    job_generation bigint,
    observed_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT diamond_loupe_snapshots_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT diamond_loupe_snapshots_check CHECK ((((standard_diamond_cut = 'present'::text) AND (standard_diamond_cut_facet IS NOT NULL) AND (octet_length(standard_diamond_cut_facet) = 20)) OR ((standard_diamond_cut <> 'present'::text) AND (standard_diamond_cut_facet IS NULL)))),
    CONSTRAINT diamond_loupe_snapshots_check1 CHECK ((truncated = (truncation_reason IS NOT NULL))),
    CONSTRAINT diamond_loupe_snapshots_check2 CHECK ((((durable_job_id IS NULL) AND (job_generation IS NULL)) OR ((durable_job_id IS NOT NULL) AND (job_generation > 0)))),
    CONSTRAINT diamond_loupe_snapshots_completeness_check CHECK ((completeness = ANY (ARRAY['complete'::text, 'partial'::text, 'unknown'::text]))),
    CONSTRAINT diamond_loupe_snapshots_detection_state_check CHECK ((detection_state = ANY (ARRAY['confirmed'::text, 'candidate'::text, 'inconsistent'::text]))),
    CONSTRAINT diamond_loupe_snapshots_diamond_address_check CHECK ((octet_length(diamond_address) = 20)),
    CONSTRAINT diamond_loupe_snapshots_stage_version_check CHECK ((stage_version = 2)),
    CONSTRAINT diamond_loupe_snapshots_standard_diamond_cut_check CHECK ((standard_diamond_cut = ANY (ARRAY['present'::text, 'absent'::text, 'unknown'::text]))),
    CONSTRAINT diamond_loupe_snapshots_truncation_reason_check CHECK (((truncation_reason IS NULL) OR (truncation_reason <> ''::text))),
    CONSTRAINT diamond_loupe_snapshots_validation_check CHECK ((validation = ANY (ARRAY['full'::text, 'sampled'::text, 'interface-only'::text]))),
    CONSTRAINT diamond_loupe_snapshots_warnings_check CHECK ((jsonb_typeof(warnings) = 'array'::text))
);

-- Sequence: diamond_loupe_snapshots_id_seq

CREATE SEQUENCE diamond_loupe_snapshots_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

-- Sequence Owned By: diamond_loupe_snapshots_id_seq

ALTER SEQUENCE diamond_loupe_snapshots_id_seq OWNED BY diamond_loupe_snapshots.id;

-- Table: durable_job_replay_requests

CREATE TABLE durable_job_replay_requests (
    job_id bigint NOT NULL,
    source_kind text NOT NULL,
    source_key text NOT NULL,
    requested_generation bigint NOT NULL,
    requested_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT durable_job_replay_requests_positive_generation_check CHECK ((requested_generation >= 1)),
    CONSTRAINT durable_job_replay_requests_source_key_check CHECK (((length(source_key) >= 1) AND (length(source_key) <= 256))),
    CONSTRAINT durable_job_replay_requests_source_kind_check CHECK (((length(source_kind) >= 1) AND (length(source_kind) <= 64)))
);

-- Sequence: durable_jobs_id_seq

ALTER TABLE durable_jobs ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME durable_jobs_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

-- Table: durable_stage_publications

CREATE TABLE durable_stage_publications (
    job_id bigint NOT NULL,
    job_generation bigint NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    stage text NOT NULL,
    stage_version integer NOT NULL,
    state text NOT NULL,
    details jsonb DEFAULT '{}'::jsonb NOT NULL,
    last_error text,
    committed_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT durable_stage_publications_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT durable_stage_publications_check CHECK ((((state = ANY (ARRAY['complete'::text, 'superseded'::text])) AND (last_error IS NULL)) OR ((state = ANY (ARRAY['failed'::text, 'unavailable'::text])) AND (last_error IS NOT NULL)))),
    CONSTRAINT durable_stage_publications_job_generation_check CHECK ((job_generation > 0)),
    CONSTRAINT durable_stage_publications_stage_version_check CHECK ((stage_version > 0)),
    CONSTRAINT durable_stage_publications_state_check CHECK ((state = ANY (ARRAY['complete'::text, 'failed'::text, 'unavailable'::text, 'superseded'::text])))
);

-- Table: eip7702_authorizations

CREATE TABLE eip7702_authorizations (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    transaction_hash bytea NOT NULL,
    transaction_index bigint NOT NULL,
    authorization_index bigint NOT NULL,
    authorization_chain_id numeric(78,0) NOT NULL,
    authorization_nonce numeric(78,0) NOT NULL,
    delegate_address bytea NOT NULL,
    y_parity smallint NOT NULL,
    r bytea NOT NULL,
    s bytea NOT NULL,
    authority bytea,
    signature_status text NOT NULL,
    application_status text NOT NULL,
    skip_reason text,
    canonical boolean NOT NULL,
    CONSTRAINT eip7702_authorizations_application_status_check CHECK ((application_status = ANY (ARRAY['applied'::text, 'skipped'::text, 'unavailable'::text]))),
    CONSTRAINT eip7702_authorizations_authority_check CHECK (((authority IS NULL) OR (octet_length(authority) = 20))),
    CONSTRAINT eip7702_authorizations_authorization_chain_id_check CHECK ((authorization_chain_id >= (0)::numeric)),
    CONSTRAINT eip7702_authorizations_authorization_index_check CHECK ((authorization_index >= 0)),
    CONSTRAINT eip7702_authorizations_authorization_nonce_check CHECK ((authorization_nonce >= (0)::numeric)),
    CONSTRAINT eip7702_authorizations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT eip7702_authorizations_block_number_check CHECK ((block_number >= (0)::numeric)),
    CONSTRAINT eip7702_authorizations_check CHECK ((((application_status = 'applied'::text) AND (skip_reason IS NULL) AND (signature_status = 'valid'::text) AND (authority IS NOT NULL)) OR ((application_status <> 'applied'::text) AND (skip_reason IS NOT NULL)))),
    CONSTRAINT eip7702_authorizations_delegate_address_check CHECK ((octet_length(delegate_address) = 20)),
    CONSTRAINT eip7702_authorizations_r_check CHECK ((octet_length(r) = 32)),
    CONSTRAINT eip7702_authorizations_s_check CHECK ((octet_length(s) = 32)),
    CONSTRAINT eip7702_authorizations_signature_status_check CHECK ((signature_status = ANY (ARRAY['valid'::text, 'invalid'::text, 'unavailable'::text]))),
    CONSTRAINT eip7702_authorizations_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT eip7702_authorizations_transaction_index_check CHECK ((transaction_index >= 0)),
    CONSTRAINT eip7702_authorizations_y_parity_check CHECK ((y_parity = ANY (ARRAY[0, 1])))
)
PARTITION BY RANGE (block_number);

-- Table: eip7702_authorizations_default

CREATE TABLE eip7702_authorizations_default (
    chain_id numeric(78,0) CONSTRAINT eip7702_authorizations_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT eip7702_authorizations_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT eip7702_authorizations_block_hash_not_null NOT NULL,
    transaction_hash bytea CONSTRAINT eip7702_authorizations_transaction_hash_not_null NOT NULL,
    transaction_index bigint CONSTRAINT eip7702_authorizations_transaction_index_not_null NOT NULL,
    authorization_index bigint CONSTRAINT eip7702_authorizations_authorization_index_not_null NOT NULL,
    authorization_chain_id numeric(78,0) CONSTRAINT eip7702_authorizations_authorization_chain_id_not_null NOT NULL,
    authorization_nonce numeric(78,0) CONSTRAINT eip7702_authorizations_authorization_nonce_not_null NOT NULL,
    delegate_address bytea CONSTRAINT eip7702_authorizations_delegate_address_not_null NOT NULL,
    y_parity smallint CONSTRAINT eip7702_authorizations_y_parity_not_null NOT NULL,
    r bytea CONSTRAINT eip7702_authorizations_r_not_null NOT NULL,
    s bytea CONSTRAINT eip7702_authorizations_s_not_null NOT NULL,
    authority bytea,
    signature_status text CONSTRAINT eip7702_authorizations_signature_status_not_null NOT NULL,
    application_status text CONSTRAINT eip7702_authorizations_application_status_not_null NOT NULL,
    skip_reason text,
    canonical boolean CONSTRAINT eip7702_authorizations_canonical_not_null NOT NULL,
    CONSTRAINT eip7702_authorizations_application_status_check CHECK ((application_status = ANY (ARRAY['applied'::text, 'skipped'::text, 'unavailable'::text]))),
    CONSTRAINT eip7702_authorizations_authority_check CHECK (((authority IS NULL) OR (octet_length(authority) = 20))),
    CONSTRAINT eip7702_authorizations_authorization_chain_id_check CHECK ((authorization_chain_id >= (0)::numeric)),
    CONSTRAINT eip7702_authorizations_authorization_index_check CHECK ((authorization_index >= 0)),
    CONSTRAINT eip7702_authorizations_authorization_nonce_check CHECK ((authorization_nonce >= (0)::numeric)),
    CONSTRAINT eip7702_authorizations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT eip7702_authorizations_block_number_check CHECK ((block_number >= (0)::numeric)),
    CONSTRAINT eip7702_authorizations_check CHECK ((((application_status = 'applied'::text) AND (skip_reason IS NULL) AND (signature_status = 'valid'::text) AND (authority IS NOT NULL)) OR ((application_status <> 'applied'::text) AND (skip_reason IS NOT NULL)))),
    CONSTRAINT eip7702_authorizations_delegate_address_check CHECK ((octet_length(delegate_address) = 20)),
    CONSTRAINT eip7702_authorizations_r_check CHECK ((octet_length(r) = 32)),
    CONSTRAINT eip7702_authorizations_s_check CHECK ((octet_length(s) = 32)),
    CONSTRAINT eip7702_authorizations_signature_status_check CHECK ((signature_status = ANY (ARRAY['valid'::text, 'invalid'::text, 'unavailable'::text]))),
    CONSTRAINT eip7702_authorizations_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT eip7702_authorizations_transaction_index_check CHECK ((transaction_index >= 0)),
    CONSTRAINT eip7702_authorizations_y_parity_check CHECK ((y_parity = ANY (ARRAY[0, 1])))
);

-- Table: eip7702_authorizations_p_0_1000000

CREATE TABLE eip7702_authorizations_p_0_1000000 (
    chain_id numeric(78,0) CONSTRAINT eip7702_authorizations_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT eip7702_authorizations_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT eip7702_authorizations_block_hash_not_null NOT NULL,
    transaction_hash bytea CONSTRAINT eip7702_authorizations_transaction_hash_not_null NOT NULL,
    transaction_index bigint CONSTRAINT eip7702_authorizations_transaction_index_not_null NOT NULL,
    authorization_index bigint CONSTRAINT eip7702_authorizations_authorization_index_not_null NOT NULL,
    authorization_chain_id numeric(78,0) CONSTRAINT eip7702_authorizations_authorization_chain_id_not_null NOT NULL,
    authorization_nonce numeric(78,0) CONSTRAINT eip7702_authorizations_authorization_nonce_not_null NOT NULL,
    delegate_address bytea CONSTRAINT eip7702_authorizations_delegate_address_not_null NOT NULL,
    y_parity smallint CONSTRAINT eip7702_authorizations_y_parity_not_null NOT NULL,
    r bytea CONSTRAINT eip7702_authorizations_r_not_null NOT NULL,
    s bytea CONSTRAINT eip7702_authorizations_s_not_null NOT NULL,
    authority bytea,
    signature_status text CONSTRAINT eip7702_authorizations_signature_status_not_null NOT NULL,
    application_status text CONSTRAINT eip7702_authorizations_application_status_not_null NOT NULL,
    skip_reason text,
    canonical boolean CONSTRAINT eip7702_authorizations_canonical_not_null NOT NULL,
    CONSTRAINT eip7702_authorizations_application_status_check CHECK ((application_status = ANY (ARRAY['applied'::text, 'skipped'::text, 'unavailable'::text]))),
    CONSTRAINT eip7702_authorizations_authority_check CHECK (((authority IS NULL) OR (octet_length(authority) = 20))),
    CONSTRAINT eip7702_authorizations_authorization_chain_id_check CHECK ((authorization_chain_id >= (0)::numeric)),
    CONSTRAINT eip7702_authorizations_authorization_index_check CHECK ((authorization_index >= 0)),
    CONSTRAINT eip7702_authorizations_authorization_nonce_check CHECK ((authorization_nonce >= (0)::numeric)),
    CONSTRAINT eip7702_authorizations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT eip7702_authorizations_block_number_check CHECK ((block_number >= (0)::numeric)),
    CONSTRAINT eip7702_authorizations_check CHECK ((((application_status = 'applied'::text) AND (skip_reason IS NULL) AND (signature_status = 'valid'::text) AND (authority IS NOT NULL)) OR ((application_status <> 'applied'::text) AND (skip_reason IS NOT NULL)))),
    CONSTRAINT eip7702_authorizations_delegate_address_check CHECK ((octet_length(delegate_address) = 20)),
    CONSTRAINT eip7702_authorizations_r_check CHECK ((octet_length(r) = 32)),
    CONSTRAINT eip7702_authorizations_s_check CHECK ((octet_length(s) = 32)),
    CONSTRAINT eip7702_authorizations_signature_status_check CHECK ((signature_status = ANY (ARRAY['valid'::text, 'invalid'::text, 'unavailable'::text]))),
    CONSTRAINT eip7702_authorizations_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT eip7702_authorizations_transaction_index_check CHECK ((transaction_index >= 0)),
    CONSTRAINT eip7702_authorizations_y_parity_check CHECK ((y_parity = ANY (ARRAY[0, 1])))
);

-- Table: ens_address_name_snapshots

CREATE TABLE ens_address_name_snapshots (
    id uuid NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    generation_id bigint NOT NULL,
    created_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    CONSTRAINT ens_address_name_snapshots_check CHECK ((expires_at > created_at))
);

-- Table: ens_name_observations

CREATE TABLE ens_name_observations (
    id bigint NOT NULL,
    generation_id bigint NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    source text NOT NULL,
    direction text NOT NULL,
    lookup_key text NOT NULL,
    outcome text NOT NULL,
    name text,
    address bytea,
    resolver bytea,
    reverse_resolver bytea,
    observed_at timestamp with time zone NOT NULL,
    publication_nonce bigint DEFAULT 0 NOT NULL,
    CONSTRAINT ens_name_observations_address_check CHECK (((address IS NULL) OR (octet_length(address) = 20))),
    CONSTRAINT ens_name_observations_check CHECK ((((direction = 'forward'::text) AND (outcome = 'resolved'::text) AND (name IS NOT NULL) AND (address IS NOT NULL) AND (resolver IS NOT NULL) AND (reverse_resolver IS NULL)) OR ((direction = 'forward'::text) AND (outcome = 'not_found'::text) AND (name IS NOT NULL) AND (address IS NULL) AND (resolver IS NULL) AND (reverse_resolver IS NULL)) OR ((direction = 'primary'::text) AND (outcome = 'resolved'::text) AND (name IS NOT NULL) AND (address IS NOT NULL) AND (resolver IS NOT NULL) AND (reverse_resolver IS NOT NULL)) OR ((direction = 'primary'::text) AND (outcome = 'not_found'::text) AND (name IS NULL) AND (address IS NOT NULL) AND (resolver IS NULL) AND (reverse_resolver IS NULL)))),
    CONSTRAINT ens_name_observations_direction_check CHECK ((direction = ANY (ARRAY['forward'::text, 'primary'::text]))),
    CONSTRAINT ens_name_observations_lookup_key_check CHECK (((length(lookup_key) >= 1) AND (length(lookup_key) <= 512))),
    CONSTRAINT ens_name_observations_name_check CHECK (((name IS NULL) OR ((length(name) >= 1) AND (length(name) <= 255)))),
    CONSTRAINT ens_name_observations_outcome_check CHECK ((outcome = ANY (ARRAY['resolved'::text, 'not_found'::text]))),
    CONSTRAINT ens_name_observations_publication_nonce_check CHECK ((publication_nonce >= 0)),
    CONSTRAINT ens_name_observations_resolver_check CHECK (((resolver IS NULL) OR (octet_length(resolver) = 20))),
    CONSTRAINT ens_name_observations_reverse_resolver_check CHECK (((reverse_resolver IS NULL) OR (octet_length(reverse_resolver) = 20))),
    CONSTRAINT ens_name_observations_source_check CHECK ((source = ANY (ARRAY['ens'::text, 'custom_ens'::text])))
);

-- Sequence: ens_name_observations_id_seq

ALTER TABLE ens_name_observations ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME ens_name_observations_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

-- Table: ens_resolution_failures

CREATE TABLE ens_resolution_failures (
    id bigint NOT NULL,
    generation_id bigint NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    source text NOT NULL,
    direction text NOT NULL,
    lookup_key text NOT NULL,
    code text NOT NULL,
    observed_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    CONSTRAINT ens_resolution_failures_check CHECK ((expires_at > observed_at)),
    CONSTRAINT ens_resolution_failures_code_check CHECK (((length(code) >= 1) AND (length(code) <= 128))),
    CONSTRAINT ens_resolution_failures_direction_check CHECK ((direction = ANY (ARRAY['forward'::text, 'primary'::text]))),
    CONSTRAINT ens_resolution_failures_lookup_key_check CHECK (((length(lookup_key) >= 1) AND (length(lookup_key) <= 512))),
    CONSTRAINT ens_resolution_failures_source_check CHECK ((source = ANY (ARRAY['ens'::text, 'custom_ens'::text])))
);

-- Sequence: ens_resolution_failures_id_seq

ALTER TABLE ens_resolution_failures ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME ens_resolution_failures_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

-- Table: ens_resolution_generations

CREATE TABLE ens_resolution_generations (
    id bigint NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    policy_key text NOT NULL,
    coin_type numeric(78,0) NOT NULL,
    official_endpoint text NOT NULL,
    official_block_number numeric(78,0) NOT NULL,
    official_block_hash bytea NOT NULL,
    custom_endpoint text,
    custom_coin_type numeric(78,0),
    custom_block_number numeric(78,0),
    custom_block_hash bytea,
    created_at timestamp with time zone NOT NULL,
    fresh_until timestamp with time zone NOT NULL,
    retain_until timestamp with time zone NOT NULL,
    CONSTRAINT ens_resolution_generations_check CHECK (((custom_endpoint IS NULL) = (custom_block_number IS NULL))),
    CONSTRAINT ens_resolution_generations_check1 CHECK (((custom_endpoint IS NULL) = (custom_block_hash IS NULL))),
    CONSTRAINT ens_resolution_generations_check2 CHECK (((custom_endpoint IS NULL) = (custom_coin_type IS NULL))),
    CONSTRAINT ens_resolution_generations_check3 CHECK ((fresh_until > created_at)),
    CONSTRAINT ens_resolution_generations_check4 CHECK ((retain_until >= fresh_until)),
    CONSTRAINT ens_resolution_generations_coin_type_check CHECK ((coin_type > (0)::numeric)),
    CONSTRAINT ens_resolution_generations_custom_block_hash_check CHECK (((custom_block_hash IS NULL) OR (octet_length(custom_block_hash) = 32))),
    CONSTRAINT ens_resolution_generations_custom_block_number_check CHECK (((custom_block_number IS NULL) OR (custom_block_number >= (0)::numeric))),
    CONSTRAINT ens_resolution_generations_custom_coin_type_check CHECK (((custom_coin_type IS NULL) OR (custom_coin_type > (0)::numeric))),
    CONSTRAINT ens_resolution_generations_custom_endpoint_check CHECK (((custom_endpoint IS NULL) OR ((length(custom_endpoint) >= 1) AND (length(custom_endpoint) <= 128)))),
    CONSTRAINT ens_resolution_generations_official_block_hash_check CHECK ((octet_length(official_block_hash) = 32)),
    CONSTRAINT ens_resolution_generations_official_block_number_check CHECK ((official_block_number >= (0)::numeric)),
    CONSTRAINT ens_resolution_generations_official_endpoint_check CHECK (((length(official_endpoint) >= 1) AND (length(official_endpoint) <= 128))),
    CONSTRAINT ens_resolution_generations_policy_key_check CHECK (((length(policy_key) >= 8) AND (length(policy_key) <= 128)))
);

-- Sequence: ens_resolution_generations_id_seq

ALTER TABLE ens_resolution_generations ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME ens_resolution_generations_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

-- Table: erc1155_balance_reconciliations

CREATE TABLE erc1155_balance_reconciliations (
    chain_id numeric(78,0) NOT NULL,
    token_address bytea NOT NULL,
    token_id numeric(78,0) NOT NULL,
    owner_address bytea NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    balance numeric(78,0) NOT NULL,
    confidence text NOT NULL,
    observed_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT erc1155_balance_reconciliations_balance_check CHECK ((balance >= (0)::numeric)),
    CONSTRAINT erc1155_balance_reconciliations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc1155_balance_reconciliations_confidence_check CHECK ((confidence = 'rpc_exact'::text)),
    CONSTRAINT erc1155_balance_reconciliations_owner_address_check CHECK ((octet_length(owner_address) = 20)),
    CONSTRAINT erc1155_balance_reconciliations_token_address_check CHECK ((octet_length(token_address) = 20)),
    CONSTRAINT erc1155_balance_reconciliations_token_id_check CHECK ((token_id >= (0)::numeric))
);

-- Table: erc20_balance_reconciliations

CREATE TABLE erc20_balance_reconciliations (
    chain_id numeric(78,0) NOT NULL,
    token_address bytea NOT NULL,
    owner_address bytea NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    balance numeric(78,0) NOT NULL,
    confidence text NOT NULL,
    observed_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT erc20_balance_reconciliations_balance_check CHECK (((balance >= (0)::numeric) AND (balance <= '115792089237316195423570985008687907853269984665640564039457584007913129639935'::numeric))),
    CONSTRAINT erc20_balance_reconciliations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc20_balance_reconciliations_confidence_check CHECK ((confidence = 'rpc_exact'::text)),
    CONSTRAINT erc20_balance_reconciliations_owner_address_check CHECK ((octet_length(owner_address) = 20)),
    CONSTRAINT erc20_balance_reconciliations_token_address_check CHECK ((octet_length(token_address) = 20))
);

-- Table: erc20_holder_balances

CREATE TABLE erc20_holder_balances (
    chain_id numeric(78,0) NOT NULL,
    token_address bytea NOT NULL,
    holder_address bytea NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    balance numeric(78,0) NOT NULL,
    confidence text NOT NULL,
    canonical boolean NOT NULL,
    observed_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT erc20_holder_balances_balance_check CHECK ((balance >= (0)::numeric)),
    CONSTRAINT erc20_holder_balances_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc20_holder_balances_confidence_check CHECK ((confidence = 'rpc_exact'::text)),
    CONSTRAINT erc20_holder_balances_holder_address_check CHECK ((octet_length(holder_address) = 20)),
    CONSTRAINT erc20_holder_balances_token_address_check CHECK ((octet_length(token_address) = 20))
)
PARTITION BY RANGE (block_number);

-- Table: erc20_holder_balances_default

CREATE TABLE erc20_holder_balances_default (
    chain_id numeric(78,0) CONSTRAINT erc20_holder_balances_chain_id_not_null NOT NULL,
    token_address bytea CONSTRAINT erc20_holder_balances_token_address_not_null NOT NULL,
    holder_address bytea CONSTRAINT erc20_holder_balances_holder_address_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT erc20_holder_balances_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT erc20_holder_balances_block_hash_not_null NOT NULL,
    balance numeric(78,0) CONSTRAINT erc20_holder_balances_balance_not_null NOT NULL,
    confidence text CONSTRAINT erc20_holder_balances_confidence_not_null NOT NULL,
    canonical boolean CONSTRAINT erc20_holder_balances_canonical_not_null NOT NULL,
    observed_at timestamp with time zone DEFAULT clock_timestamp() CONSTRAINT erc20_holder_balances_observed_at_not_null NOT NULL,
    CONSTRAINT erc20_holder_balances_balance_check CHECK ((balance >= (0)::numeric)),
    CONSTRAINT erc20_holder_balances_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc20_holder_balances_confidence_check CHECK ((confidence = 'rpc_exact'::text)),
    CONSTRAINT erc20_holder_balances_holder_address_check CHECK ((octet_length(holder_address) = 20)),
    CONSTRAINT erc20_holder_balances_token_address_check CHECK ((octet_length(token_address) = 20))
);

-- Table: erc20_holder_balances_p_0_1000000

CREATE TABLE erc20_holder_balances_p_0_1000000 (
    chain_id numeric(78,0) CONSTRAINT erc20_holder_balances_chain_id_not_null NOT NULL,
    token_address bytea CONSTRAINT erc20_holder_balances_token_address_not_null NOT NULL,
    holder_address bytea CONSTRAINT erc20_holder_balances_holder_address_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT erc20_holder_balances_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT erc20_holder_balances_block_hash_not_null NOT NULL,
    balance numeric(78,0) CONSTRAINT erc20_holder_balances_balance_not_null NOT NULL,
    confidence text CONSTRAINT erc20_holder_balances_confidence_not_null NOT NULL,
    canonical boolean CONSTRAINT erc20_holder_balances_canonical_not_null NOT NULL,
    observed_at timestamp with time zone DEFAULT clock_timestamp() CONSTRAINT erc20_holder_balances_observed_at_not_null NOT NULL,
    CONSTRAINT erc20_holder_balances_balance_check CHECK ((balance >= (0)::numeric)),
    CONSTRAINT erc20_holder_balances_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc20_holder_balances_confidence_check CHECK ((confidence = 'rpc_exact'::text)),
    CONSTRAINT erc20_holder_balances_holder_address_check CHECK ((octet_length(holder_address) = 20)),
    CONSTRAINT erc20_holder_balances_token_address_check CHECK ((octet_length(token_address) = 20))
);

-- Table: erc20_holder_snapshots

CREATE TABLE erc20_holder_snapshots (
    chain_id numeric(78,0) NOT NULL,
    token_address bytea NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    state text NOT NULL,
    holder_count numeric(78,0) NOT NULL,
    total_supply numeric(78,0) NOT NULL,
    reconciled_balance_sum numeric(78,0) NOT NULL,
    canonical boolean NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT erc20_holder_snapshots_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc20_holder_snapshots_check CHECK ((((state = 'complete'::text) AND (total_supply = reconciled_balance_sum)) OR (state = 'unavailable'::text))),
    CONSTRAINT erc20_holder_snapshots_holder_count_check CHECK ((holder_count >= (0)::numeric)),
    CONSTRAINT erc20_holder_snapshots_reconciled_balance_sum_check CHECK ((reconciled_balance_sum >= (0)::numeric)),
    CONSTRAINT erc20_holder_snapshots_state_check CHECK ((state = ANY (ARRAY['complete'::text, 'unavailable'::text]))),
    CONSTRAINT erc20_holder_snapshots_token_address_check CHECK ((octet_length(token_address) = 20)),
    CONSTRAINT erc20_holder_snapshots_total_supply_check CHECK ((total_supply >= (0)::numeric))
)
PARTITION BY RANGE (block_number);

-- Table: erc20_holder_snapshots_default

CREATE TABLE erc20_holder_snapshots_default (
    chain_id numeric(78,0) CONSTRAINT erc20_holder_snapshots_chain_id_not_null NOT NULL,
    token_address bytea CONSTRAINT erc20_holder_snapshots_token_address_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT erc20_holder_snapshots_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT erc20_holder_snapshots_block_hash_not_null NOT NULL,
    state text CONSTRAINT erc20_holder_snapshots_state_not_null NOT NULL,
    holder_count numeric(78,0) CONSTRAINT erc20_holder_snapshots_holder_count_not_null NOT NULL,
    total_supply numeric(78,0) CONSTRAINT erc20_holder_snapshots_total_supply_not_null NOT NULL,
    reconciled_balance_sum numeric(78,0) CONSTRAINT erc20_holder_snapshots_reconciled_balance_sum_not_null NOT NULL,
    canonical boolean CONSTRAINT erc20_holder_snapshots_canonical_not_null NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() CONSTRAINT erc20_holder_snapshots_created_at_not_null NOT NULL,
    CONSTRAINT erc20_holder_snapshots_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc20_holder_snapshots_check CHECK ((((state = 'complete'::text) AND (total_supply = reconciled_balance_sum)) OR (state = 'unavailable'::text))),
    CONSTRAINT erc20_holder_snapshots_holder_count_check CHECK ((holder_count >= (0)::numeric)),
    CONSTRAINT erc20_holder_snapshots_reconciled_balance_sum_check CHECK ((reconciled_balance_sum >= (0)::numeric)),
    CONSTRAINT erc20_holder_snapshots_state_check CHECK ((state = ANY (ARRAY['complete'::text, 'unavailable'::text]))),
    CONSTRAINT erc20_holder_snapshots_token_address_check CHECK ((octet_length(token_address) = 20)),
    CONSTRAINT erc20_holder_snapshots_total_supply_check CHECK ((total_supply >= (0)::numeric))
);

-- Table: erc20_holder_snapshots_p_0_1000000

CREATE TABLE erc20_holder_snapshots_p_0_1000000 (
    chain_id numeric(78,0) CONSTRAINT erc20_holder_snapshots_chain_id_not_null NOT NULL,
    token_address bytea CONSTRAINT erc20_holder_snapshots_token_address_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT erc20_holder_snapshots_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT erc20_holder_snapshots_block_hash_not_null NOT NULL,
    state text CONSTRAINT erc20_holder_snapshots_state_not_null NOT NULL,
    holder_count numeric(78,0) CONSTRAINT erc20_holder_snapshots_holder_count_not_null NOT NULL,
    total_supply numeric(78,0) CONSTRAINT erc20_holder_snapshots_total_supply_not_null NOT NULL,
    reconciled_balance_sum numeric(78,0) CONSTRAINT erc20_holder_snapshots_reconciled_balance_sum_not_null NOT NULL,
    canonical boolean CONSTRAINT erc20_holder_snapshots_canonical_not_null NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() CONSTRAINT erc20_holder_snapshots_created_at_not_null NOT NULL,
    CONSTRAINT erc20_holder_snapshots_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc20_holder_snapshots_check CHECK ((((state = 'complete'::text) AND (total_supply = reconciled_balance_sum)) OR (state = 'unavailable'::text))),
    CONSTRAINT erc20_holder_snapshots_holder_count_check CHECK ((holder_count >= (0)::numeric)),
    CONSTRAINT erc20_holder_snapshots_reconciled_balance_sum_check CHECK ((reconciled_balance_sum >= (0)::numeric)),
    CONSTRAINT erc20_holder_snapshots_state_check CHECK ((state = ANY (ARRAY['complete'::text, 'unavailable'::text]))),
    CONSTRAINT erc20_holder_snapshots_token_address_check CHECK ((octet_length(token_address) = 20)),
    CONSTRAINT erc20_holder_snapshots_total_supply_check CHECK ((total_supply >= (0)::numeric))
);

-- Table: erc4337_coverage_ranges

CREATE TABLE erc4337_coverage_ranges (
    chain_id numeric(78,0) NOT NULL,
    configuration_digest bytea NOT NULL,
    start_block numeric(78,0) NOT NULL,
    start_block_hash bytea NOT NULL,
    end_block numeric(78,0) NOT NULL,
    end_block_hash bytea NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT erc4337_coverage_ranges_check CHECK (((start_block >= (0)::numeric) AND (end_block >= start_block))),
    CONSTRAINT erc4337_coverage_ranges_configuration_digest_check CHECK ((octet_length(configuration_digest) = 32)),
    CONSTRAINT erc4337_coverage_ranges_end_block_hash_check CHECK ((octet_length(end_block_hash) = 32)),
    CONSTRAINT erc4337_coverage_ranges_start_block_hash_check CHECK ((octet_length(start_block_hash) = 32))
);

-- Table: erc4337_covered_blocks

CREATE TABLE erc4337_covered_blocks (
    chain_id numeric(78,0) NOT NULL,
    configuration_digest bytea NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    durable_job_id bigint NOT NULL,
    job_generation bigint NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT erc4337_covered_blocks_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc4337_covered_blocks_check CHECK (((block_number >= (0)::numeric) AND (durable_job_id > 0) AND (job_generation > 0))),
    CONSTRAINT erc4337_covered_blocks_configuration_digest_check CHECK ((octet_length(configuration_digest) = 32))
);

-- Table: erc4337_user_operation_events

CREATE TABLE erc4337_user_operation_events (
    chain_id numeric(78,0) NOT NULL,
    configuration_digest bytea NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    transaction_hash bytea NOT NULL,
    operation_index bigint NOT NULL,
    log_index bigint NOT NULL,
    event_kind text NOT NULL,
    sender bytea NOT NULL,
    nonce numeric(78,0),
    related_address bytea,
    paymaster bytea,
    raw_data bytea NOT NULL,
    reason text,
    panic_code numeric(78,0),
    canonical boolean NOT NULL,
    CONSTRAINT erc4337_user_operation_events_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc4337_user_operation_events_check CHECK (((operation_index >= 0) AND (log_index >= 0))),
    CONSTRAINT erc4337_user_operation_events_configuration_digest_check CHECK ((octet_length(configuration_digest) = 32)),
    CONSTRAINT erc4337_user_operation_events_event_kind_check CHECK ((event_kind = ANY (ARRAY['account_deployed'::text, 'ignored_init_code'::text, 'eip7702_initialized'::text, 'execution_revert'::text, 'post_op_revert'::text, 'prefund_too_low'::text]))),
    CONSTRAINT erc4337_user_operation_events_nonce_check CHECK (((nonce IS NULL) OR (nonce >= (0)::numeric))),
    CONSTRAINT erc4337_user_operation_events_panic_code_check CHECK (((panic_code IS NULL) OR (panic_code >= (0)::numeric))),
    CONSTRAINT erc4337_user_operation_events_paymaster_check CHECK (((paymaster IS NULL) OR (octet_length(paymaster) = 20))),
    CONSTRAINT erc4337_user_operation_events_related_address_check CHECK (((related_address IS NULL) OR (octet_length(related_address) = 20))),
    CONSTRAINT erc4337_user_operation_events_sender_check CHECK ((octet_length(sender) = 20)),
    CONSTRAINT erc4337_user_operation_events_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32))
)
PARTITION BY RANGE (block_number);

-- Table: erc4337_user_operation_events_default

CREATE TABLE erc4337_user_operation_events_default (
    chain_id numeric(78,0) CONSTRAINT erc4337_user_operation_events_chain_id_not_null NOT NULL,
    configuration_digest bytea CONSTRAINT erc4337_user_operation_events_configuration_digest_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT erc4337_user_operation_events_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT erc4337_user_operation_events_block_hash_not_null NOT NULL,
    transaction_hash bytea CONSTRAINT erc4337_user_operation_events_transaction_hash_not_null NOT NULL,
    operation_index bigint CONSTRAINT erc4337_user_operation_events_operation_index_not_null NOT NULL,
    log_index bigint CONSTRAINT erc4337_user_operation_events_log_index_not_null NOT NULL,
    event_kind text CONSTRAINT erc4337_user_operation_events_event_kind_not_null NOT NULL,
    sender bytea CONSTRAINT erc4337_user_operation_events_sender_not_null NOT NULL,
    nonce numeric(78,0),
    related_address bytea,
    paymaster bytea,
    raw_data bytea CONSTRAINT erc4337_user_operation_events_raw_data_not_null NOT NULL,
    reason text,
    panic_code numeric(78,0),
    canonical boolean CONSTRAINT erc4337_user_operation_events_canonical_not_null NOT NULL,
    CONSTRAINT erc4337_user_operation_events_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc4337_user_operation_events_check CHECK (((operation_index >= 0) AND (log_index >= 0))),
    CONSTRAINT erc4337_user_operation_events_configuration_digest_check CHECK ((octet_length(configuration_digest) = 32)),
    CONSTRAINT erc4337_user_operation_events_event_kind_check CHECK ((event_kind = ANY (ARRAY['account_deployed'::text, 'ignored_init_code'::text, 'eip7702_initialized'::text, 'execution_revert'::text, 'post_op_revert'::text, 'prefund_too_low'::text]))),
    CONSTRAINT erc4337_user_operation_events_nonce_check CHECK (((nonce IS NULL) OR (nonce >= (0)::numeric))),
    CONSTRAINT erc4337_user_operation_events_panic_code_check CHECK (((panic_code IS NULL) OR (panic_code >= (0)::numeric))),
    CONSTRAINT erc4337_user_operation_events_paymaster_check CHECK (((paymaster IS NULL) OR (octet_length(paymaster) = 20))),
    CONSTRAINT erc4337_user_operation_events_related_address_check CHECK (((related_address IS NULL) OR (octet_length(related_address) = 20))),
    CONSTRAINT erc4337_user_operation_events_sender_check CHECK ((octet_length(sender) = 20)),
    CONSTRAINT erc4337_user_operation_events_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32))
);

-- Table: erc4337_user_operation_events_p_0_1000000

CREATE TABLE erc4337_user_operation_events_p_0_1000000 (
    chain_id numeric(78,0) CONSTRAINT erc4337_user_operation_events_chain_id_not_null NOT NULL,
    configuration_digest bytea CONSTRAINT erc4337_user_operation_events_configuration_digest_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT erc4337_user_operation_events_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT erc4337_user_operation_events_block_hash_not_null NOT NULL,
    transaction_hash bytea CONSTRAINT erc4337_user_operation_events_transaction_hash_not_null NOT NULL,
    operation_index bigint CONSTRAINT erc4337_user_operation_events_operation_index_not_null NOT NULL,
    log_index bigint CONSTRAINT erc4337_user_operation_events_log_index_not_null NOT NULL,
    event_kind text CONSTRAINT erc4337_user_operation_events_event_kind_not_null NOT NULL,
    sender bytea CONSTRAINT erc4337_user_operation_events_sender_not_null NOT NULL,
    nonce numeric(78,0),
    related_address bytea,
    paymaster bytea,
    raw_data bytea CONSTRAINT erc4337_user_operation_events_raw_data_not_null NOT NULL,
    reason text,
    panic_code numeric(78,0),
    canonical boolean CONSTRAINT erc4337_user_operation_events_canonical_not_null NOT NULL,
    CONSTRAINT erc4337_user_operation_events_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc4337_user_operation_events_check CHECK (((operation_index >= 0) AND (log_index >= 0))),
    CONSTRAINT erc4337_user_operation_events_configuration_digest_check CHECK ((octet_length(configuration_digest) = 32)),
    CONSTRAINT erc4337_user_operation_events_event_kind_check CHECK ((event_kind = ANY (ARRAY['account_deployed'::text, 'ignored_init_code'::text, 'eip7702_initialized'::text, 'execution_revert'::text, 'post_op_revert'::text, 'prefund_too_low'::text]))),
    CONSTRAINT erc4337_user_operation_events_nonce_check CHECK (((nonce IS NULL) OR (nonce >= (0)::numeric))),
    CONSTRAINT erc4337_user_operation_events_panic_code_check CHECK (((panic_code IS NULL) OR (panic_code >= (0)::numeric))),
    CONSTRAINT erc4337_user_operation_events_paymaster_check CHECK (((paymaster IS NULL) OR (octet_length(paymaster) = 20))),
    CONSTRAINT erc4337_user_operation_events_related_address_check CHECK (((related_address IS NULL) OR (octet_length(related_address) = 20))),
    CONSTRAINT erc4337_user_operation_events_sender_check CHECK ((octet_length(sender) = 20)),
    CONSTRAINT erc4337_user_operation_events_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32))
);

-- Table: erc4337_user_operation_participants

CREATE TABLE erc4337_user_operation_participants (
    chain_id numeric(78,0) NOT NULL,
    configuration_digest bytea CONSTRAINT erc4337_user_operation_participan_configuration_digest_not_null NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    transaction_hash bytea NOT NULL,
    operation_index bigint NOT NULL,
    address bytea NOT NULL,
    role text NOT NULL,
    canonical boolean NOT NULL,
    CONSTRAINT erc4337_user_operation_participants_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT erc4337_user_operation_participants_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc4337_user_operation_participants_configuration_digest_check CHECK ((octet_length(configuration_digest) = 32)),
    CONSTRAINT erc4337_user_operation_participants_operation_index_check CHECK ((operation_index >= 0)),
    CONSTRAINT erc4337_user_operation_participants_role_check CHECK ((role = ANY (ARRAY['sender'::text, 'entry_point'::text, 'bundler'::text, 'beneficiary'::text, 'factory'::text, 'paymaster'::text, 'aggregator'::text, 'eip7702_delegate'::text]))),
    CONSTRAINT erc4337_user_operation_participants_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32))
)
PARTITION BY RANGE (block_number);

-- Table: erc4337_user_operation_participants_default

CREATE TABLE erc4337_user_operation_participants_default (
    chain_id numeric(78,0) CONSTRAINT erc4337_user_operation_participants_chain_id_not_null NOT NULL,
    configuration_digest bytea CONSTRAINT erc4337_user_operation_participan_configuration_digest_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT erc4337_user_operation_participants_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT erc4337_user_operation_participants_block_hash_not_null NOT NULL,
    transaction_hash bytea CONSTRAINT erc4337_user_operation_participants_transaction_hash_not_null NOT NULL,
    operation_index bigint CONSTRAINT erc4337_user_operation_participants_operation_index_not_null NOT NULL,
    address bytea CONSTRAINT erc4337_user_operation_participants_address_not_null NOT NULL,
    role text CONSTRAINT erc4337_user_operation_participants_role_not_null NOT NULL,
    canonical boolean CONSTRAINT erc4337_user_operation_participants_canonical_not_null NOT NULL,
    CONSTRAINT erc4337_user_operation_participants_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT erc4337_user_operation_participants_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc4337_user_operation_participants_configuration_digest_check CHECK ((octet_length(configuration_digest) = 32)),
    CONSTRAINT erc4337_user_operation_participants_operation_index_check CHECK ((operation_index >= 0)),
    CONSTRAINT erc4337_user_operation_participants_role_check CHECK ((role = ANY (ARRAY['sender'::text, 'entry_point'::text, 'bundler'::text, 'beneficiary'::text, 'factory'::text, 'paymaster'::text, 'aggregator'::text, 'eip7702_delegate'::text]))),
    CONSTRAINT erc4337_user_operation_participants_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32))
);

-- Table: erc4337_user_operation_participants_p_0_1000000

CREATE TABLE erc4337_user_operation_participants_p_0_1000000 (
    chain_id numeric(78,0) CONSTRAINT erc4337_user_operation_participants_chain_id_not_null NOT NULL,
    configuration_digest bytea CONSTRAINT erc4337_user_operation_participan_configuration_digest_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT erc4337_user_operation_participants_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT erc4337_user_operation_participants_block_hash_not_null NOT NULL,
    transaction_hash bytea CONSTRAINT erc4337_user_operation_participants_transaction_hash_not_null NOT NULL,
    operation_index bigint CONSTRAINT erc4337_user_operation_participants_operation_index_not_null NOT NULL,
    address bytea CONSTRAINT erc4337_user_operation_participants_address_not_null NOT NULL,
    role text CONSTRAINT erc4337_user_operation_participants_role_not_null NOT NULL,
    canonical boolean CONSTRAINT erc4337_user_operation_participants_canonical_not_null NOT NULL,
    CONSTRAINT erc4337_user_operation_participants_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT erc4337_user_operation_participants_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc4337_user_operation_participants_configuration_digest_check CHECK ((octet_length(configuration_digest) = 32)),
    CONSTRAINT erc4337_user_operation_participants_operation_index_check CHECK ((operation_index >= 0)),
    CONSTRAINT erc4337_user_operation_participants_role_check CHECK ((role = ANY (ARRAY['sender'::text, 'entry_point'::text, 'bundler'::text, 'beneficiary'::text, 'factory'::text, 'paymaster'::text, 'aggregator'::text, 'eip7702_delegate'::text]))),
    CONSTRAINT erc4337_user_operation_participants_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32))
);

-- Table: erc4337_user_operations

CREATE TABLE erc4337_user_operations (
    chain_id numeric(78,0) NOT NULL,
    configuration_digest bytea NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    transaction_hash bytea NOT NULL,
    transaction_index bigint NOT NULL,
    operation_index bigint NOT NULL,
    event_log_index bigint NOT NULL,
    user_op_hash bytea NOT NULL,
    entry_point bytea NOT NULL,
    entry_point_version text NOT NULL,
    sender bytea NOT NULL,
    nonce numeric(78,0) NOT NULL,
    nonce_key numeric(78,0) NOT NULL,
    nonce_sequence numeric(78,0) NOT NULL,
    bundler bytea NOT NULL,
    beneficiary bytea NOT NULL,
    init_kind text NOT NULL,
    factory bytea,
    paymaster bytea,
    aggregator bytea,
    success boolean NOT NULL,
    actual_gas_cost numeric(78,0) NOT NULL,
    actual_gas_used numeric(78,0) NOT NULL,
    call_gas_limit numeric(78,0) NOT NULL,
    verification_gas_limit numeric(78,0) NOT NULL,
    pre_verification_gas numeric(78,0) NOT NULL,
    max_fee_per_gas numeric(78,0) NOT NULL,
    max_priority_fee_per_gas numeric(78,0) NOT NULL,
    paymaster_verification_gas_limit numeric(78,0),
    paymaster_post_op_gas_limit numeric(78,0),
    init_code bytea NOT NULL,
    factory_data bytea NOT NULL,
    call_data bytea NOT NULL,
    paymaster_and_data bytea NOT NULL,
    paymaster_data bytea NOT NULL,
    paymaster_signature bytea NOT NULL,
    signature bytea NOT NULL,
    account_gas_limits bytea,
    gas_fees bytea,
    aggregated_signature bytea NOT NULL,
    canonical boolean NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT erc4337_user_operations_account_gas_limits_check CHECK (((account_gas_limits IS NULL) OR (octet_length(account_gas_limits) = 32))),
    CONSTRAINT erc4337_user_operations_aggregator_check CHECK (((aggregator IS NULL) OR (octet_length(aggregator) = 20))),
    CONSTRAINT erc4337_user_operations_beneficiary_check CHECK ((octet_length(beneficiary) = 20)),
    CONSTRAINT erc4337_user_operations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc4337_user_operations_bundler_check CHECK ((octet_length(bundler) = 20)),
    CONSTRAINT erc4337_user_operations_check CHECK (((transaction_index >= 0) AND (operation_index >= 0) AND (event_log_index >= 0))),
    CONSTRAINT erc4337_user_operations_check1 CHECK (((nonce >= (0)::numeric) AND (nonce_key >= (0)::numeric) AND (nonce_sequence >= (0)::numeric))),
    CONSTRAINT erc4337_user_operations_check2 CHECK (((actual_gas_cost >= (0)::numeric) AND (actual_gas_used >= (0)::numeric))),
    CONSTRAINT erc4337_user_operations_check3 CHECK (((call_gas_limit >= (0)::numeric) AND (verification_gas_limit >= (0)::numeric))),
    CONSTRAINT erc4337_user_operations_check4 CHECK (((pre_verification_gas >= (0)::numeric) AND (max_fee_per_gas >= (0)::numeric) AND (max_priority_fee_per_gas >= (0)::numeric))),
    CONSTRAINT erc4337_user_operations_configuration_digest_check CHECK ((octet_length(configuration_digest) = 32)),
    CONSTRAINT erc4337_user_operations_entry_point_check CHECK ((octet_length(entry_point) = 20)),
    CONSTRAINT erc4337_user_operations_entry_point_version_check CHECK ((entry_point_version = ANY (ARRAY['0.6'::text, '0.7'::text, '0.8'::text, '0.9'::text]))),
    CONSTRAINT erc4337_user_operations_factory_check CHECK (((factory IS NULL) OR (octet_length(factory) = 20))),
    CONSTRAINT erc4337_user_operations_gas_fees_check CHECK (((gas_fees IS NULL) OR (octet_length(gas_fees) = 32))),
    CONSTRAINT erc4337_user_operations_init_kind_check CHECK ((init_kind = ANY (ARRAY['none'::text, 'factory'::text, 'eip7702'::text]))),
    CONSTRAINT erc4337_user_operations_paymaster_check CHECK (((paymaster IS NULL) OR (octet_length(paymaster) = 20))),
    CONSTRAINT erc4337_user_operations_paymaster_post_op_gas_limit_check CHECK (((paymaster_post_op_gas_limit IS NULL) OR (paymaster_post_op_gas_limit >= (0)::numeric))),
    CONSTRAINT erc4337_user_operations_paymaster_verification_gas_limit_check CHECK (((paymaster_verification_gas_limit IS NULL) OR (paymaster_verification_gas_limit >= (0)::numeric))),
    CONSTRAINT erc4337_user_operations_sender_check CHECK ((octet_length(sender) = 20)),
    CONSTRAINT erc4337_user_operations_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT erc4337_user_operations_user_op_hash_check CHECK ((octet_length(user_op_hash) = 32))
)
PARTITION BY RANGE (block_number);

-- Table: erc4337_user_operations_default

CREATE TABLE erc4337_user_operations_default (
    chain_id numeric(78,0) CONSTRAINT erc4337_user_operations_chain_id_not_null NOT NULL,
    configuration_digest bytea CONSTRAINT erc4337_user_operations_configuration_digest_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT erc4337_user_operations_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT erc4337_user_operations_block_hash_not_null NOT NULL,
    transaction_hash bytea CONSTRAINT erc4337_user_operations_transaction_hash_not_null NOT NULL,
    transaction_index bigint CONSTRAINT erc4337_user_operations_transaction_index_not_null NOT NULL,
    operation_index bigint CONSTRAINT erc4337_user_operations_operation_index_not_null NOT NULL,
    event_log_index bigint CONSTRAINT erc4337_user_operations_event_log_index_not_null NOT NULL,
    user_op_hash bytea CONSTRAINT erc4337_user_operations_user_op_hash_not_null NOT NULL,
    entry_point bytea CONSTRAINT erc4337_user_operations_entry_point_not_null NOT NULL,
    entry_point_version text CONSTRAINT erc4337_user_operations_entry_point_version_not_null NOT NULL,
    sender bytea CONSTRAINT erc4337_user_operations_sender_not_null NOT NULL,
    nonce numeric(78,0) CONSTRAINT erc4337_user_operations_nonce_not_null NOT NULL,
    nonce_key numeric(78,0) CONSTRAINT erc4337_user_operations_nonce_key_not_null NOT NULL,
    nonce_sequence numeric(78,0) CONSTRAINT erc4337_user_operations_nonce_sequence_not_null NOT NULL,
    bundler bytea CONSTRAINT erc4337_user_operations_bundler_not_null NOT NULL,
    beneficiary bytea CONSTRAINT erc4337_user_operations_beneficiary_not_null NOT NULL,
    init_kind text CONSTRAINT erc4337_user_operations_init_kind_not_null NOT NULL,
    factory bytea,
    paymaster bytea,
    aggregator bytea,
    success boolean CONSTRAINT erc4337_user_operations_success_not_null NOT NULL,
    actual_gas_cost numeric(78,0) CONSTRAINT erc4337_user_operations_actual_gas_cost_not_null NOT NULL,
    actual_gas_used numeric(78,0) CONSTRAINT erc4337_user_operations_actual_gas_used_not_null NOT NULL,
    call_gas_limit numeric(78,0) CONSTRAINT erc4337_user_operations_call_gas_limit_not_null NOT NULL,
    verification_gas_limit numeric(78,0) CONSTRAINT erc4337_user_operations_verification_gas_limit_not_null NOT NULL,
    pre_verification_gas numeric(78,0) CONSTRAINT erc4337_user_operations_pre_verification_gas_not_null NOT NULL,
    max_fee_per_gas numeric(78,0) CONSTRAINT erc4337_user_operations_max_fee_per_gas_not_null NOT NULL,
    max_priority_fee_per_gas numeric(78,0) CONSTRAINT erc4337_user_operations_max_priority_fee_per_gas_not_null NOT NULL,
    paymaster_verification_gas_limit numeric(78,0),
    paymaster_post_op_gas_limit numeric(78,0),
    init_code bytea CONSTRAINT erc4337_user_operations_init_code_not_null NOT NULL,
    factory_data bytea CONSTRAINT erc4337_user_operations_factory_data_not_null NOT NULL,
    call_data bytea CONSTRAINT erc4337_user_operations_call_data_not_null NOT NULL,
    paymaster_and_data bytea CONSTRAINT erc4337_user_operations_paymaster_and_data_not_null NOT NULL,
    paymaster_data bytea CONSTRAINT erc4337_user_operations_paymaster_data_not_null NOT NULL,
    paymaster_signature bytea CONSTRAINT erc4337_user_operations_paymaster_signature_not_null NOT NULL,
    signature bytea CONSTRAINT erc4337_user_operations_signature_not_null NOT NULL,
    account_gas_limits bytea,
    gas_fees bytea,
    aggregated_signature bytea CONSTRAINT erc4337_user_operations_aggregated_signature_not_null NOT NULL,
    canonical boolean CONSTRAINT erc4337_user_operations_canonical_not_null NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() CONSTRAINT erc4337_user_operations_created_at_not_null NOT NULL,
    CONSTRAINT erc4337_user_operations_account_gas_limits_check CHECK (((account_gas_limits IS NULL) OR (octet_length(account_gas_limits) = 32))),
    CONSTRAINT erc4337_user_operations_aggregator_check CHECK (((aggregator IS NULL) OR (octet_length(aggregator) = 20))),
    CONSTRAINT erc4337_user_operations_beneficiary_check CHECK ((octet_length(beneficiary) = 20)),
    CONSTRAINT erc4337_user_operations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc4337_user_operations_bundler_check CHECK ((octet_length(bundler) = 20)),
    CONSTRAINT erc4337_user_operations_check CHECK (((transaction_index >= 0) AND (operation_index >= 0) AND (event_log_index >= 0))),
    CONSTRAINT erc4337_user_operations_check1 CHECK (((nonce >= (0)::numeric) AND (nonce_key >= (0)::numeric) AND (nonce_sequence >= (0)::numeric))),
    CONSTRAINT erc4337_user_operations_check2 CHECK (((actual_gas_cost >= (0)::numeric) AND (actual_gas_used >= (0)::numeric))),
    CONSTRAINT erc4337_user_operations_check3 CHECK (((call_gas_limit >= (0)::numeric) AND (verification_gas_limit >= (0)::numeric))),
    CONSTRAINT erc4337_user_operations_check4 CHECK (((pre_verification_gas >= (0)::numeric) AND (max_fee_per_gas >= (0)::numeric) AND (max_priority_fee_per_gas >= (0)::numeric))),
    CONSTRAINT erc4337_user_operations_configuration_digest_check CHECK ((octet_length(configuration_digest) = 32)),
    CONSTRAINT erc4337_user_operations_entry_point_check CHECK ((octet_length(entry_point) = 20)),
    CONSTRAINT erc4337_user_operations_entry_point_version_check CHECK ((entry_point_version = ANY (ARRAY['0.6'::text, '0.7'::text, '0.8'::text, '0.9'::text]))),
    CONSTRAINT erc4337_user_operations_factory_check CHECK (((factory IS NULL) OR (octet_length(factory) = 20))),
    CONSTRAINT erc4337_user_operations_gas_fees_check CHECK (((gas_fees IS NULL) OR (octet_length(gas_fees) = 32))),
    CONSTRAINT erc4337_user_operations_init_kind_check CHECK ((init_kind = ANY (ARRAY['none'::text, 'factory'::text, 'eip7702'::text]))),
    CONSTRAINT erc4337_user_operations_paymaster_check CHECK (((paymaster IS NULL) OR (octet_length(paymaster) = 20))),
    CONSTRAINT erc4337_user_operations_paymaster_post_op_gas_limit_check CHECK (((paymaster_post_op_gas_limit IS NULL) OR (paymaster_post_op_gas_limit >= (0)::numeric))),
    CONSTRAINT erc4337_user_operations_paymaster_verification_gas_limit_check CHECK (((paymaster_verification_gas_limit IS NULL) OR (paymaster_verification_gas_limit >= (0)::numeric))),
    CONSTRAINT erc4337_user_operations_sender_check CHECK ((octet_length(sender) = 20)),
    CONSTRAINT erc4337_user_operations_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT erc4337_user_operations_user_op_hash_check CHECK ((octet_length(user_op_hash) = 32))
);

-- Table: erc4337_user_operations_p_0_1000000

CREATE TABLE erc4337_user_operations_p_0_1000000 (
    chain_id numeric(78,0) CONSTRAINT erc4337_user_operations_chain_id_not_null NOT NULL,
    configuration_digest bytea CONSTRAINT erc4337_user_operations_configuration_digest_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT erc4337_user_operations_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT erc4337_user_operations_block_hash_not_null NOT NULL,
    transaction_hash bytea CONSTRAINT erc4337_user_operations_transaction_hash_not_null NOT NULL,
    transaction_index bigint CONSTRAINT erc4337_user_operations_transaction_index_not_null NOT NULL,
    operation_index bigint CONSTRAINT erc4337_user_operations_operation_index_not_null NOT NULL,
    event_log_index bigint CONSTRAINT erc4337_user_operations_event_log_index_not_null NOT NULL,
    user_op_hash bytea CONSTRAINT erc4337_user_operations_user_op_hash_not_null NOT NULL,
    entry_point bytea CONSTRAINT erc4337_user_operations_entry_point_not_null NOT NULL,
    entry_point_version text CONSTRAINT erc4337_user_operations_entry_point_version_not_null NOT NULL,
    sender bytea CONSTRAINT erc4337_user_operations_sender_not_null NOT NULL,
    nonce numeric(78,0) CONSTRAINT erc4337_user_operations_nonce_not_null NOT NULL,
    nonce_key numeric(78,0) CONSTRAINT erc4337_user_operations_nonce_key_not_null NOT NULL,
    nonce_sequence numeric(78,0) CONSTRAINT erc4337_user_operations_nonce_sequence_not_null NOT NULL,
    bundler bytea CONSTRAINT erc4337_user_operations_bundler_not_null NOT NULL,
    beneficiary bytea CONSTRAINT erc4337_user_operations_beneficiary_not_null NOT NULL,
    init_kind text CONSTRAINT erc4337_user_operations_init_kind_not_null NOT NULL,
    factory bytea,
    paymaster bytea,
    aggregator bytea,
    success boolean CONSTRAINT erc4337_user_operations_success_not_null NOT NULL,
    actual_gas_cost numeric(78,0) CONSTRAINT erc4337_user_operations_actual_gas_cost_not_null NOT NULL,
    actual_gas_used numeric(78,0) CONSTRAINT erc4337_user_operations_actual_gas_used_not_null NOT NULL,
    call_gas_limit numeric(78,0) CONSTRAINT erc4337_user_operations_call_gas_limit_not_null NOT NULL,
    verification_gas_limit numeric(78,0) CONSTRAINT erc4337_user_operations_verification_gas_limit_not_null NOT NULL,
    pre_verification_gas numeric(78,0) CONSTRAINT erc4337_user_operations_pre_verification_gas_not_null NOT NULL,
    max_fee_per_gas numeric(78,0) CONSTRAINT erc4337_user_operations_max_fee_per_gas_not_null NOT NULL,
    max_priority_fee_per_gas numeric(78,0) CONSTRAINT erc4337_user_operations_max_priority_fee_per_gas_not_null NOT NULL,
    paymaster_verification_gas_limit numeric(78,0),
    paymaster_post_op_gas_limit numeric(78,0),
    init_code bytea CONSTRAINT erc4337_user_operations_init_code_not_null NOT NULL,
    factory_data bytea CONSTRAINT erc4337_user_operations_factory_data_not_null NOT NULL,
    call_data bytea CONSTRAINT erc4337_user_operations_call_data_not_null NOT NULL,
    paymaster_and_data bytea CONSTRAINT erc4337_user_operations_paymaster_and_data_not_null NOT NULL,
    paymaster_data bytea CONSTRAINT erc4337_user_operations_paymaster_data_not_null NOT NULL,
    paymaster_signature bytea CONSTRAINT erc4337_user_operations_paymaster_signature_not_null NOT NULL,
    signature bytea CONSTRAINT erc4337_user_operations_signature_not_null NOT NULL,
    account_gas_limits bytea,
    gas_fees bytea,
    aggregated_signature bytea CONSTRAINT erc4337_user_operations_aggregated_signature_not_null NOT NULL,
    canonical boolean CONSTRAINT erc4337_user_operations_canonical_not_null NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() CONSTRAINT erc4337_user_operations_created_at_not_null NOT NULL,
    CONSTRAINT erc4337_user_operations_account_gas_limits_check CHECK (((account_gas_limits IS NULL) OR (octet_length(account_gas_limits) = 32))),
    CONSTRAINT erc4337_user_operations_aggregator_check CHECK (((aggregator IS NULL) OR (octet_length(aggregator) = 20))),
    CONSTRAINT erc4337_user_operations_beneficiary_check CHECK ((octet_length(beneficiary) = 20)),
    CONSTRAINT erc4337_user_operations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc4337_user_operations_bundler_check CHECK ((octet_length(bundler) = 20)),
    CONSTRAINT erc4337_user_operations_check CHECK (((transaction_index >= 0) AND (operation_index >= 0) AND (event_log_index >= 0))),
    CONSTRAINT erc4337_user_operations_check1 CHECK (((nonce >= (0)::numeric) AND (nonce_key >= (0)::numeric) AND (nonce_sequence >= (0)::numeric))),
    CONSTRAINT erc4337_user_operations_check2 CHECK (((actual_gas_cost >= (0)::numeric) AND (actual_gas_used >= (0)::numeric))),
    CONSTRAINT erc4337_user_operations_check3 CHECK (((call_gas_limit >= (0)::numeric) AND (verification_gas_limit >= (0)::numeric))),
    CONSTRAINT erc4337_user_operations_check4 CHECK (((pre_verification_gas >= (0)::numeric) AND (max_fee_per_gas >= (0)::numeric) AND (max_priority_fee_per_gas >= (0)::numeric))),
    CONSTRAINT erc4337_user_operations_configuration_digest_check CHECK ((octet_length(configuration_digest) = 32)),
    CONSTRAINT erc4337_user_operations_entry_point_check CHECK ((octet_length(entry_point) = 20)),
    CONSTRAINT erc4337_user_operations_entry_point_version_check CHECK ((entry_point_version = ANY (ARRAY['0.6'::text, '0.7'::text, '0.8'::text, '0.9'::text]))),
    CONSTRAINT erc4337_user_operations_factory_check CHECK (((factory IS NULL) OR (octet_length(factory) = 20))),
    CONSTRAINT erc4337_user_operations_gas_fees_check CHECK (((gas_fees IS NULL) OR (octet_length(gas_fees) = 32))),
    CONSTRAINT erc4337_user_operations_init_kind_check CHECK ((init_kind = ANY (ARRAY['none'::text, 'factory'::text, 'eip7702'::text]))),
    CONSTRAINT erc4337_user_operations_paymaster_check CHECK (((paymaster IS NULL) OR (octet_length(paymaster) = 20))),
    CONSTRAINT erc4337_user_operations_paymaster_post_op_gas_limit_check CHECK (((paymaster_post_op_gas_limit IS NULL) OR (paymaster_post_op_gas_limit >= (0)::numeric))),
    CONSTRAINT erc4337_user_operations_paymaster_verification_gas_limit_check CHECK (((paymaster_verification_gas_limit IS NULL) OR (paymaster_verification_gas_limit >= (0)::numeric))),
    CONSTRAINT erc4337_user_operations_sender_check CHECK ((octet_length(sender) = 20)),
    CONSTRAINT erc4337_user_operations_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT erc4337_user_operations_user_op_hash_check CHECK ((octet_length(user_op_hash) = 32))
);

-- Table: erc721_owner_reconciliations

CREATE TABLE erc721_owner_reconciliations (
    chain_id numeric(78,0) NOT NULL,
    token_address bytea NOT NULL,
    token_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    state text NOT NULL,
    owner_address bytea,
    confidence text NOT NULL,
    observed_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT erc721_owner_reconciliations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc721_owner_reconciliations_check CHECK ((((state = 'owned'::text) AND (owner_address IS NOT NULL)) OR ((state = 'not_found'::text) AND (owner_address IS NULL)))),
    CONSTRAINT erc721_owner_reconciliations_confidence_check CHECK ((confidence = 'rpc_exact'::text)),
    CONSTRAINT erc721_owner_reconciliations_owner_address_check CHECK (((owner_address IS NULL) OR (octet_length(owner_address) = 20))),
    CONSTRAINT erc721_owner_reconciliations_state_check CHECK ((state = ANY (ARRAY['owned'::text, 'not_found'::text]))),
    CONSTRAINT erc721_owner_reconciliations_token_address_check CHECK ((octet_length(token_address) = 20)),
    CONSTRAINT erc721_owner_reconciliations_token_id_check CHECK ((token_id >= (0)::numeric))
);

-- Table: etherview_schema_migrations

CREATE TABLE IF NOT EXISTS etherview_schema_migrations (
    version text PRIMARY KEY,
    checksum text NOT NULL,
    applied_at timestamp with time zone DEFAULT now() NOT NULL
);

-- Table: external_adapter_observations

CREATE TABLE external_adapter_observations (
    id bigint NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    capability text NOT NULL,
    observation_key text NOT NULL,
    state text NOT NULL,
    code text,
    value jsonb,
    block_number numeric(78,0),
    block_hash bytea,
    observed_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    recorded_at timestamp with time zone DEFAULT now() NOT NULL,
    provider_key text DEFAULT 'default'::text NOT NULL,
    CONSTRAINT external_adapter_observations_block_hash_check CHECK (((block_hash IS NULL) OR (octet_length(block_hash) = 32))),
    CONSTRAINT external_adapter_observations_capability_check CHECK ((capability = ANY (ARRAY['name'::text, 'price'::text]))),
    CONSTRAINT external_adapter_observations_check CHECK (((block_number IS NULL) = (block_hash IS NULL))),
    CONSTRAINT external_adapter_observations_check1 CHECK ((expires_at > observed_at)),
    CONSTRAINT external_adapter_observations_check2 CHECK ((((state = 'complete'::text) AND (code IS NULL) AND (value IS NOT NULL)) OR ((state = ANY (ARRAY['unavailable'::text, 'failed'::text])) AND (code IS NOT NULL) AND (value IS NULL)))),
    CONSTRAINT external_adapter_observations_code_check CHECK (((code IS NULL) OR ((length(code) >= 1) AND (length(code) <= 128)))),
    CONSTRAINT external_adapter_observations_observation_key_check CHECK (((length(observation_key) >= 1) AND (length(observation_key) <= 512))),
    CONSTRAINT external_adapter_observations_provider_key_check CHECK (((length(provider_key) >= 1) AND (length(provider_key) <= 128))),
    CONSTRAINT external_adapter_observations_state_check CHECK ((state = ANY (ARRAY['complete'::text, 'unavailable'::text, 'failed'::text])))
);

-- Comment: COLUMN external_adapter_observations.provider_key

COMMENT ON COLUMN external_adapter_observations.provider_key IS 'Stable non-secret namespace for the configured adapter provider';

-- Sequence: external_adapter_observations_id_seq

CREATE SEQUENCE external_adapter_observations_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

-- Sequence Owned By: external_adapter_observations_id_seq

ALTER SEQUENCE external_adapter_observations_id_seq OWNED BY external_adapter_observations.id;

-- Table: external_metadata

CREATE TABLE external_metadata (
    chain_id numeric(78,0) NOT NULL,
    resource_kind text NOT NULL,
    resource_key text NOT NULL,
    source_uri text NOT NULL,
    resolved_uri text,
    state text NOT NULL,
    media_type text,
    content_hash bytea,
    document jsonb,
    last_error text,
    fetched_at timestamp with time zone,
    expires_at timestamp with time zone,
    token_address bytea,
    token_id numeric(78,0),
    observed_block_number numeric(78,0),
    observed_block_hash bytea,
    content_size bigint,
    attempt_count integer DEFAULT 0 NOT NULL,
    last_error_code text,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    terminal_at timestamp with time zone,
    identity_hash bytea DEFAULT '\x'::bytea NOT NULL,
    CONSTRAINT external_metadata_attempt_count_check CHECK ((attempt_count >= 0)),
    CONSTRAINT external_metadata_content_hash_check CHECK (((content_hash IS NULL) OR (octet_length(content_hash) = 32))),
    CONSTRAINT external_metadata_content_size_check CHECK (((content_size IS NULL) OR ((content_size >= 0) AND (content_size <= 2097152)))),
    CONSTRAINT external_metadata_error_code_check CHECK (((last_error_code IS NULL) OR ((length(last_error_code) >= 1) AND (length(last_error_code) <= 64)))),
    CONSTRAINT external_metadata_identity_hash_check CHECK ((((resource_kind = 'nft'::text) AND (observed_block_hash IS NOT NULL) AND (identity_hash = observed_block_hash)) OR ((resource_kind <> 'nft'::text) AND (identity_hash = '\x'::bytea)) OR ((resource_kind = 'nft'::text) AND (observed_block_hash IS NULL) AND (identity_hash = '\x'::bytea)))),
    CONSTRAINT external_metadata_nft_attempt_bound_check CHECK (((resource_kind <> 'nft'::text) OR ((attempt_count >= 0) AND (attempt_count <= 100)))),
    CONSTRAINT external_metadata_nft_identity_check CHECK (((resource_kind <> 'nft'::text) OR ((token_address IS NULL) AND (token_id IS NULL) AND (observed_block_number IS NULL)) OR ((token_address IS NOT NULL) AND (token_id IS NOT NULL) AND (observed_block_number IS NOT NULL)))),
    CONSTRAINT external_metadata_nft_outcome_check CHECK (((resource_kind <> 'nft'::text) OR (observed_block_hash IS NULL) OR ((state = 'pending'::text) AND (resolved_uri IS NULL) AND (media_type IS NULL) AND (content_hash IS NULL) AND (document IS NULL) AND (content_size IS NULL) AND (terminal_at IS NULL)) OR ((state = 'available'::text) AND (resolved_uri IS NOT NULL) AND (media_type IS NOT NULL) AND (content_hash IS NOT NULL) AND (document IS NOT NULL) AND (content_size IS NOT NULL) AND (last_error_code IS NULL) AND (last_error IS NULL) AND (fetched_at IS NOT NULL) AND (terminal_at IS NOT NULL)) OR ((state = ANY (ARRAY['unavailable'::text, 'unsafe'::text, 'error'::text])) AND (resolved_uri IS NULL) AND (media_type IS NULL) AND (content_hash IS NULL) AND (document IS NULL) AND (content_size IS NULL) AND (last_error_code IS NOT NULL) AND (last_error IS NOT NULL) AND (fetched_at IS NOT NULL) AND (terminal_at IS NOT NULL)))),
    CONSTRAINT external_metadata_nft_source_uri_check CHECK (((resource_kind <> 'nft'::text) OR (observed_block_hash IS NULL) OR ((length(source_uri) >= 1) AND (length(source_uri) <= 4096)))),
    CONSTRAINT external_metadata_nft_uint256_check CHECK (((resource_kind <> 'nft'::text) OR (token_id IS NULL) OR (token_id <= '115792089237316195423570985008687907853269984665640564039457584007913129639935'::numeric))),
    CONSTRAINT external_metadata_observation_pair_check CHECK (((observed_block_number IS NULL) = (observed_block_hash IS NULL))),
    CONSTRAINT external_metadata_observed_hash_check CHECK (((observed_block_hash IS NULL) OR (octet_length(observed_block_hash) = 32))),
    CONSTRAINT external_metadata_resource_kind_check CHECK ((resource_kind = ANY (ARRAY['token'::text, 'nft'::text, 'name'::text]))),
    CONSTRAINT external_metadata_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'available'::text, 'unavailable'::text, 'unsafe'::text, 'error'::text]))),
    CONSTRAINT external_metadata_token_address_check CHECK (((token_address IS NULL) OR (octet_length(token_address) = 20))),
    CONSTRAINT external_metadata_token_id_check CHECK (((token_id IS NULL) OR (token_id >= (0)::numeric)))
);

-- Table: external_metadata_attempts

CREATE TABLE external_metadata_attempts (
    id bigint NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    resource_kind text NOT NULL,
    resource_key text NOT NULL,
    durable_job_id bigint NOT NULL,
    attempt integer NOT NULL,
    state text NOT NULL,
    source_uri text NOT NULL,
    resolved_uri text,
    media_type text,
    content_hash bytea,
    content_size bigint,
    error_code text,
    error_message text,
    attempted_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT external_metadata_attempts_attempt_check CHECK ((attempt > 0)),
    CONSTRAINT external_metadata_attempts_content_hash_check CHECK (((content_hash IS NULL) OR (octet_length(content_hash) = 32))),
    CONSTRAINT external_metadata_attempts_content_size_check CHECK (((content_size IS NULL) OR ((content_size >= 0) AND (content_size <= 2097152)))),
    CONSTRAINT external_metadata_attempts_error_code_check CHECK (((error_code IS NULL) OR ((length(error_code) >= 1) AND (length(error_code) <= 64)))),
    CONSTRAINT external_metadata_attempts_error_message_check CHECK (((error_message IS NULL) OR (length(error_message) <= 1024))),
    CONSTRAINT external_metadata_attempts_resource_kind_check CHECK ((resource_kind = ANY (ARRAY['token'::text, 'nft'::text, 'name'::text]))),
    CONSTRAINT external_metadata_attempts_state_check CHECK ((state = ANY (ARRAY['available'::text, 'unavailable'::text, 'unsafe'::text, 'error'::text])))
);

-- Sequence: external_metadata_attempts_id_seq

ALTER TABLE external_metadata_attempts ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME external_metadata_attempts_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

-- Table: genesis_account_observations

CREATE TABLE genesis_account_observations (
    chain_id numeric(78,0) NOT NULL,
    address bytea NOT NULL,
    block_hash bytea NOT NULL,
    balance numeric(78,0) NOT NULL,
    nonce numeric(78,0) NOT NULL,
    code_hash bytea NOT NULL,
    code bytea NOT NULL,
    storage_root bytea NOT NULL,
    observed_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT genesis_account_observations_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT genesis_account_observations_balance_check CHECK ((balance >= (0)::numeric)),
    CONSTRAINT genesis_account_observations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT genesis_account_observations_code_hash_check CHECK ((octet_length(code_hash) = 32)),
    CONSTRAINT genesis_account_observations_nonce_check CHECK ((nonce >= (0)::numeric)),
    CONSTRAINT genesis_account_observations_storage_root_check CHECK ((octet_length(storage_root) = 32))
);

-- Table: genesis_state_imports

CREATE TABLE genesis_state_imports (
    chain_id numeric(78,0) NOT NULL,
    block_hash bytea,
    state_root bytea,
    document_sha256 bytea,
    state text NOT NULL,
    account_count numeric(78,0),
    last_error_code text,
    imported_at timestamp with time zone,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT genesis_state_imports_account_count_check CHECK (((account_count IS NULL) OR (account_count >= (0)::numeric))),
    CONSTRAINT genesis_state_imports_block_hash_check CHECK (((block_hash IS NULL) OR (octet_length(block_hash) = 32))),
    CONSTRAINT genesis_state_imports_check CHECK ((((state = 'complete'::text) AND (block_hash IS NOT NULL) AND (state_root IS NOT NULL) AND (document_sha256 IS NOT NULL) AND (account_count IS NOT NULL) AND (imported_at IS NOT NULL) AND (last_error_code IS NULL)) OR ((state <> 'complete'::text) AND (account_count IS NULL) AND (imported_at IS NULL)))),
    CONSTRAINT genesis_state_imports_document_sha256_check CHECK (((document_sha256 IS NULL) OR (octet_length(document_sha256) = 32))),
    CONSTRAINT genesis_state_imports_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'complete'::text, 'unavailable'::text, 'failed'::text]))),
    CONSTRAINT genesis_state_imports_state_root_check CHECK (((state_root IS NULL) OR (octet_length(state_root) = 32)))
);

-- Table: index_checkpoints

CREATE TABLE index_checkpoints (
    chain_id numeric(78,0) NOT NULL,
    stage text NOT NULL,
    contiguous_through numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT index_checkpoints_block_hash_check CHECK ((octet_length(block_hash) = 32))
);

-- Table: logs

CREATE TABLE logs (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    log_index bigint NOT NULL,
    tx_index bigint NOT NULL,
    tx_hash bytea NOT NULL,
    address bytea NOT NULL,
    topic0 bytea,
    raw jsonb NOT NULL,
    CONSTRAINT logs_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT logs_log_index_check CHECK ((log_index >= 0)),
    CONSTRAINT logs_topic0_check CHECK (((topic0 IS NULL) OR (octet_length(topic0) = 32))),
    CONSTRAINT logs_tx_index_check CHECK ((tx_index >= 0))
)
PARTITION BY RANGE (block_number);

-- Table: logs_default

CREATE TABLE logs_default (
    chain_id numeric(78,0) CONSTRAINT logs_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT logs_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT logs_block_hash_not_null NOT NULL,
    log_index bigint CONSTRAINT logs_log_index_not_null NOT NULL,
    tx_index bigint CONSTRAINT logs_tx_index_not_null NOT NULL,
    tx_hash bytea CONSTRAINT logs_tx_hash_not_null NOT NULL,
    address bytea CONSTRAINT logs_address_not_null NOT NULL,
    topic0 bytea,
    raw jsonb CONSTRAINT logs_raw_not_null NOT NULL,
    CONSTRAINT logs_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT logs_log_index_check CHECK ((log_index >= 0)),
    CONSTRAINT logs_topic0_check CHECK (((topic0 IS NULL) OR (octet_length(topic0) = 32))),
    CONSTRAINT logs_tx_index_check CHECK ((tx_index >= 0))
);

-- Table: logs_p_0_1000000

CREATE TABLE logs_p_0_1000000 (
    chain_id numeric(78,0) CONSTRAINT logs_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT logs_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT logs_block_hash_not_null NOT NULL,
    log_index bigint CONSTRAINT logs_log_index_not_null NOT NULL,
    tx_index bigint CONSTRAINT logs_tx_index_not_null NOT NULL,
    tx_hash bytea CONSTRAINT logs_tx_hash_not_null NOT NULL,
    address bytea CONSTRAINT logs_address_not_null NOT NULL,
    topic0 bytea,
    raw jsonb CONSTRAINT logs_raw_not_null NOT NULL,
    CONSTRAINT logs_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT logs_log_index_check CHECK ((log_index >= 0)),
    CONSTRAINT logs_topic0_check CHECK (((topic0 IS NULL) OR (octet_length(topic0) = 32))),
    CONSTRAINT logs_tx_index_check CHECK ((tx_index >= 0))
);

-- Table: mempool_snapshot_transactions

CREATE TABLE mempool_snapshot_transactions (
    chain_id numeric(78,0) NOT NULL,
    snapshot_id bigint NOT NULL,
    tx_hash bytea NOT NULL,
    CONSTRAINT mempool_snapshot_transactions_tx_hash_check CHECK ((octet_length(tx_hash) = 32))
);

-- Table: mempool_snapshots

CREATE TABLE mempool_snapshots (
    chain_id numeric(78,0) NOT NULL,
    id bigint NOT NULL,
    endpoint_name text NOT NULL,
    observed_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    transaction_count integer NOT NULL,
    CONSTRAINT mempool_snapshots_check CHECK ((expires_at > observed_at)),
    CONSTRAINT mempool_snapshots_endpoint_name_check CHECK (((length(endpoint_name) >= 1) AND (length(endpoint_name) <= 128))),
    CONSTRAINT mempool_snapshots_transaction_count_check CHECK ((transaction_count >= 0))
);

-- Sequence: mempool_snapshots_id_seq

ALTER TABLE mempool_snapshots ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME mempool_snapshots_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

-- Table: mempool_status

CREATE TABLE mempool_status (
    chain_id numeric(78,0) NOT NULL,
    state text NOT NULL,
    endpoint_name text,
    latest_snapshot_id bigint,
    transaction_count integer,
    last_attempt_at timestamp with time zone NOT NULL,
    last_success_at timestamp with time zone,
    error_code text,
    error_message text,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    last_snapshot_write_id bigint,
    CONSTRAINT mempool_status_check CHECK (((latest_snapshot_id IS NULL) = (last_success_at IS NULL))),
    CONSTRAINT mempool_status_check1 CHECK (((state = ANY (ARRAY['unavailable'::text, 'failed'::text])) = (error_code IS NOT NULL))),
    CONSTRAINT mempool_status_check2 CHECK (((state = ANY (ARRAY['unavailable'::text, 'failed'::text])) = (error_message IS NOT NULL))),
    CONSTRAINT mempool_status_endpoint_name_check CHECK (((endpoint_name IS NULL) OR ((length(endpoint_name) >= 1) AND (length(endpoint_name) <= 128)))),
    CONSTRAINT mempool_status_error_code_check CHECK (((error_code IS NULL) OR ((length(error_code) >= 1) AND (length(error_code) <= 64)))),
    CONSTRAINT mempool_status_error_message_check CHECK (((error_message IS NULL) OR ((length(error_message) >= 1) AND (length(error_message) <= 1024)))),
    CONSTRAINT mempool_status_last_snapshot_write_id_positive CHECK (((last_snapshot_write_id IS NULL) OR (last_snapshot_write_id > 0))),
    CONSTRAINT mempool_status_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'complete'::text, 'unavailable'::text, 'failed'::text]))),
    CONSTRAINT mempool_status_transaction_count_check CHECK (((transaction_count IS NULL) OR (transaction_count >= 0)))
);

-- Table: mempool_transaction_replacements

CREATE TABLE mempool_transaction_replacements (
    chain_id numeric(78,0) NOT NULL,
    snapshot_id bigint NOT NULL,
    replaced_hash bytea NOT NULL,
    replacement_hash bytea NOT NULL,
    CONSTRAINT mempool_transaction_replacements_check CHECK ((replaced_hash <> replacement_hash)),
    CONSTRAINT mempool_transaction_replacements_replaced_hash_check CHECK ((octet_length(replaced_hash) = 32)),
    CONSTRAINT mempool_transaction_replacements_replacement_hash_check CHECK ((octet_length(replacement_hash) = 32))
);

-- Table: mempool_transactions

CREATE TABLE mempool_transactions (
    chain_id numeric(78,0) NOT NULL,
    tx_hash bytea NOT NULL,
    from_address bytea NOT NULL,
    to_address bytea,
    nonce numeric(78,0) NOT NULL,
    value numeric(78,0) NOT NULL,
    gas numeric(78,0) NOT NULL,
    gas_price numeric(78,0),
    max_fee_per_gas numeric(78,0),
    max_priority_fee_per_gas numeric(78,0),
    tx_type numeric(78,0),
    input bytea NOT NULL,
    raw jsonb NOT NULL,
    first_seen_at timestamp with time zone NOT NULL,
    last_seen_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    last_endpoint_name text NOT NULL,
    CONSTRAINT mempool_transactions_check CHECK (((nonce >= (0)::numeric) AND (value >= (0)::numeric) AND (gas >= (0)::numeric))),
    CONSTRAINT mempool_transactions_check1 CHECK ((last_seen_at >= first_seen_at)),
    CONSTRAINT mempool_transactions_check2 CHECK ((expires_at > last_seen_at)),
    CONSTRAINT mempool_transactions_from_address_check CHECK ((octet_length(from_address) = 20)),
    CONSTRAINT mempool_transactions_gas_price_check CHECK (((gas_price IS NULL) OR (gas_price >= (0)::numeric))),
    CONSTRAINT mempool_transactions_last_endpoint_name_check CHECK (((length(last_endpoint_name) >= 1) AND (length(last_endpoint_name) <= 128))),
    CONSTRAINT mempool_transactions_max_fee_per_gas_check CHECK (((max_fee_per_gas IS NULL) OR (max_fee_per_gas >= (0)::numeric))),
    CONSTRAINT mempool_transactions_max_priority_fee_per_gas_check CHECK (((max_priority_fee_per_gas IS NULL) OR (max_priority_fee_per_gas >= (0)::numeric))),
    CONSTRAINT mempool_transactions_to_address_check CHECK (((to_address IS NULL) OR (octet_length(to_address) = 20))),
    CONSTRAINT mempool_transactions_tx_hash_check CHECK ((octet_length(tx_hash) = 32)),
    CONSTRAINT mempool_transactions_tx_type_check CHECK (((tx_type IS NULL) OR (tx_type >= (0)::numeric)))
);

-- Table: native_transfer_coverage

CREATE TABLE native_transfer_coverage (
    chain_id numeric(78,0) NOT NULL,
    covered nummultirange DEFAULT '{}'::nummultirange NOT NULL
);

-- Table: native_transfers

CREATE TABLE native_transfers (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    transaction_hash bytea NOT NULL,
    transaction_index bigint NOT NULL,
    log_index bigint NOT NULL,
    from_address bytea NOT NULL,
    to_address bytea NOT NULL,
    amount numeric(78,0) NOT NULL,
    canonical boolean NOT NULL,
    CONSTRAINT native_transfers_amount_check CHECK (((amount > (0)::numeric) AND (amount < '115792089237316195423570985008687907853269984665640564039457584007913129639936'::numeric))),
    CONSTRAINT native_transfers_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT native_transfers_check CHECK ((from_address <> to_address)),
    CONSTRAINT native_transfers_from_address_check CHECK ((octet_length(from_address) = 20)),
    CONSTRAINT native_transfers_log_index_check CHECK ((log_index >= 0)),
    CONSTRAINT native_transfers_to_address_check CHECK ((octet_length(to_address) = 20)),
    CONSTRAINT native_transfers_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT native_transfers_transaction_index_check CHECK ((transaction_index >= 0))
)
PARTITION BY RANGE (block_number);

-- Table: native_transfers_default

CREATE TABLE native_transfers_default (
    chain_id numeric(78,0) CONSTRAINT native_transfers_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT native_transfers_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT native_transfers_block_hash_not_null NOT NULL,
    transaction_hash bytea CONSTRAINT native_transfers_transaction_hash_not_null NOT NULL,
    transaction_index bigint CONSTRAINT native_transfers_transaction_index_not_null NOT NULL,
    log_index bigint CONSTRAINT native_transfers_log_index_not_null NOT NULL,
    from_address bytea CONSTRAINT native_transfers_from_address_not_null NOT NULL,
    to_address bytea CONSTRAINT native_transfers_to_address_not_null NOT NULL,
    amount numeric(78,0) CONSTRAINT native_transfers_amount_not_null NOT NULL,
    canonical boolean CONSTRAINT native_transfers_canonical_not_null NOT NULL,
    CONSTRAINT native_transfers_amount_check CHECK (((amount > (0)::numeric) AND (amount < '115792089237316195423570985008687907853269984665640564039457584007913129639936'::numeric))),
    CONSTRAINT native_transfers_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT native_transfers_check CHECK ((from_address <> to_address)),
    CONSTRAINT native_transfers_from_address_check CHECK ((octet_length(from_address) = 20)),
    CONSTRAINT native_transfers_log_index_check CHECK ((log_index >= 0)),
    CONSTRAINT native_transfers_to_address_check CHECK ((octet_length(to_address) = 20)),
    CONSTRAINT native_transfers_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT native_transfers_transaction_index_check CHECK ((transaction_index >= 0))
);

-- Table: native_transfers_p_0_1000000

CREATE TABLE native_transfers_p_0_1000000 (
    chain_id numeric(78,0) CONSTRAINT native_transfers_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT native_transfers_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT native_transfers_block_hash_not_null NOT NULL,
    transaction_hash bytea CONSTRAINT native_transfers_transaction_hash_not_null NOT NULL,
    transaction_index bigint CONSTRAINT native_transfers_transaction_index_not_null NOT NULL,
    log_index bigint CONSTRAINT native_transfers_log_index_not_null NOT NULL,
    from_address bytea CONSTRAINT native_transfers_from_address_not_null NOT NULL,
    to_address bytea CONSTRAINT native_transfers_to_address_not_null NOT NULL,
    amount numeric(78,0) CONSTRAINT native_transfers_amount_not_null NOT NULL,
    canonical boolean CONSTRAINT native_transfers_canonical_not_null NOT NULL,
    CONSTRAINT native_transfers_amount_check CHECK (((amount > (0)::numeric) AND (amount < '115792089237316195423570985008687907853269984665640564039457584007913129639936'::numeric))),
    CONSTRAINT native_transfers_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT native_transfers_check CHECK ((from_address <> to_address)),
    CONSTRAINT native_transfers_from_address_check CHECK ((octet_length(from_address) = 20)),
    CONSTRAINT native_transfers_log_index_check CHECK ((log_index >= 0)),
    CONSTRAINT native_transfers_to_address_check CHECK ((octet_length(to_address) = 20)),
    CONSTRAINT native_transfers_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT native_transfers_transaction_index_check CHECK ((transaction_index >= 0))
);

-- Table: nft_metadata_source_observations

CREATE TABLE nft_metadata_source_observations (
    chain_id numeric(78,0) NOT NULL,
    token_address bytea NOT NULL,
    token_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    standard text NOT NULL,
    state text NOT NULL,
    source_uri text,
    error_code text,
    observed_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT nft_metadata_source_observations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT nft_metadata_source_observations_check CHECK ((((state = 'found'::text) AND (source_uri IS NOT NULL) AND (error_code IS NULL)) OR ((state = 'unavailable'::text) AND (source_uri IS NULL) AND (error_code IS NOT NULL)))),
    CONSTRAINT nft_metadata_source_observations_error_code_check CHECK (((error_code IS NULL) OR ((length(error_code) >= 1) AND (length(error_code) <= 64)))),
    CONSTRAINT nft_metadata_source_observations_source_uri_check CHECK (((source_uri IS NULL) OR ((length(source_uri) >= 1) AND (length(source_uri) <= 4096)))),
    CONSTRAINT nft_metadata_source_observations_standard_check CHECK ((standard = ANY (ARRAY['erc721'::text, 'erc1155'::text]))),
    CONSTRAINT nft_metadata_source_observations_state_check CHECK ((state = ANY (ARRAY['found'::text, 'unavailable'::text]))),
    CONSTRAINT nft_metadata_source_observations_token_address_check CHECK ((octet_length(token_address) = 20)),
    CONSTRAINT nft_metadata_source_observations_token_id_check CHECK (((token_id >= (0)::numeric) AND (token_id <= '115792089237316195423570985008687907853269984665640564039457584007913129639935'::numeric)))
);

-- Table: nft_metadata_update_observations

CREATE TABLE nft_metadata_update_observations (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    log_index bigint NOT NULL,
    token_address bytea NOT NULL,
    standard text NOT NULL,
    event_kind text NOT NULL,
    state text NOT NULL,
    from_token_id numeric(78,0),
    to_token_id numeric(78,0),
    error_code text,
    observed_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT nft_metadata_update_observations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT nft_metadata_update_observations_check CHECK ((((state = 'accepted'::text) AND (error_code IS NULL) AND (from_token_id IS NOT NULL) AND (to_token_id IS NOT NULL) AND (from_token_id <= to_token_id) AND (((event_kind = ANY (ARRAY['erc4906_single'::text, 'erc1155_uri'::text])) AND (from_token_id = to_token_id)) OR (event_kind = 'erc4906_batch'::text)) AND (((event_kind = ANY (ARRAY['erc4906_single'::text, 'erc4906_batch'::text])) AND (standard = 'erc721'::text)) OR ((event_kind = 'erc1155_uri'::text) AND (standard = 'erc1155'::text)))) OR ((state = 'malformed'::text) AND (error_code IS NOT NULL) AND (from_token_id IS NULL) AND (to_token_id IS NULL)))),
    CONSTRAINT nft_metadata_update_observations_error_code_check CHECK (((error_code IS NULL) OR ((length(error_code) >= 1) AND (length(error_code) <= 64)))),
    CONSTRAINT nft_metadata_update_observations_event_kind_check CHECK ((event_kind = ANY (ARRAY['erc4906_single'::text, 'erc4906_batch'::text, 'erc1155_uri'::text]))),
    CONSTRAINT nft_metadata_update_observations_from_token_id_check CHECK (((from_token_id IS NULL) OR ((from_token_id >= (0)::numeric) AND (from_token_id <= '115792089237316195423570985008687907853269984665640564039457584007913129639935'::numeric)))),
    CONSTRAINT nft_metadata_update_observations_log_index_check CHECK ((log_index >= 0)),
    CONSTRAINT nft_metadata_update_observations_standard_check CHECK ((standard = ANY (ARRAY['erc721'::text, 'erc1155'::text]))),
    CONSTRAINT nft_metadata_update_observations_state_check CHECK ((state = ANY (ARRAY['accepted'::text, 'malformed'::text]))),
    CONSTRAINT nft_metadata_update_observations_to_token_id_check CHECK (((to_token_id IS NULL) OR ((to_token_id >= (0)::numeric) AND (to_token_id <= '115792089237316195423570985008687907853269984665640564039457584007913129639935'::numeric)))),
    CONSTRAINT nft_metadata_update_observations_token_address_check CHECK ((octet_length(token_address) = 20))
);

-- Table: normalized_traces

CREATE TABLE normalized_traces (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    transaction_hash bytea NOT NULL,
    transaction_index bigint NOT NULL,
    trace_path text NOT NULL,
    parent_path text,
    depth integer NOT NULL,
    call_type text NOT NULL,
    from_address bytea,
    to_address bytea,
    created_address bytea,
    value numeric(78,0),
    gas numeric(78,0),
    gas_used numeric(78,0),
    input bytea,
    output bytea,
    error text,
    reverted boolean NOT NULL,
    canonical boolean NOT NULL,
    direct_reverted boolean DEFAULT false NOT NULL,
    execution_address bytea,
    execution_code_hash bytea,
    execution_resolution text DEFAULT 'unavailable'::text NOT NULL,
    CONSTRAINT normalized_traces_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT normalized_traces_created_address_check CHECK (((created_address IS NULL) OR (octet_length(created_address) = 20))),
    CONSTRAINT normalized_traces_depth_check CHECK ((depth >= 0)),
    CONSTRAINT normalized_traces_execution_address_length_check CHECK (((execution_address IS NULL) OR (octet_length(execution_address) = 20))),
    CONSTRAINT normalized_traces_execution_code_hash_length_check CHECK (((execution_code_hash IS NULL) OR (octet_length(execution_code_hash) = 32))),
    CONSTRAINT normalized_traces_execution_identity_check CHECK ((((execution_resolution = ANY (ARRAY['direct'::text, 'eip7702_delegate'::text])) AND (execution_address IS NOT NULL) AND (execution_code_hash IS NOT NULL)) OR ((execution_resolution = ANY (ARRAY['empty'::text, 'not_applicable'::text])) AND (execution_address IS NULL) AND (execution_code_hash IS NULL)) OR ((execution_resolution = 'unavailable'::text) AND (execution_code_hash IS NULL)))),
    CONSTRAINT normalized_traces_execution_resolution_check CHECK ((execution_resolution = ANY (ARRAY['direct'::text, 'eip7702_delegate'::text, 'empty'::text, 'unavailable'::text, 'not_applicable'::text]))),
    CONSTRAINT normalized_traces_from_address_check CHECK (((from_address IS NULL) OR (octet_length(from_address) = 20))),
    CONSTRAINT normalized_traces_gas_check CHECK (((gas IS NULL) OR (gas >= (0)::numeric))),
    CONSTRAINT normalized_traces_gas_used_check CHECK (((gas_used IS NULL) OR (gas_used >= (0)::numeric))),
    CONSTRAINT normalized_traces_to_address_check CHECK (((to_address IS NULL) OR (octet_length(to_address) = 20))),
    CONSTRAINT normalized_traces_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT normalized_traces_value_check CHECK (((value IS NULL) OR (value >= (0)::numeric)))
)
PARTITION BY RANGE (block_number);

-- Table: normalized_traces_default

CREATE TABLE normalized_traces_default (
    chain_id numeric(78,0) CONSTRAINT normalized_traces_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT normalized_traces_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT normalized_traces_block_hash_not_null NOT NULL,
    transaction_hash bytea CONSTRAINT normalized_traces_transaction_hash_not_null NOT NULL,
    transaction_index bigint CONSTRAINT normalized_traces_transaction_index_not_null NOT NULL,
    trace_path text CONSTRAINT normalized_traces_trace_path_not_null NOT NULL,
    parent_path text,
    depth integer CONSTRAINT normalized_traces_depth_not_null NOT NULL,
    call_type text CONSTRAINT normalized_traces_call_type_not_null NOT NULL,
    from_address bytea,
    to_address bytea,
    created_address bytea,
    value numeric(78,0),
    gas numeric(78,0),
    gas_used numeric(78,0),
    input bytea,
    output bytea,
    error text,
    reverted boolean CONSTRAINT normalized_traces_reverted_not_null NOT NULL,
    canonical boolean CONSTRAINT normalized_traces_canonical_not_null NOT NULL,
    direct_reverted boolean DEFAULT false CONSTRAINT normalized_traces_direct_reverted_not_null NOT NULL,
    execution_address bytea,
    execution_code_hash bytea,
    execution_resolution text DEFAULT 'unavailable'::text CONSTRAINT normalized_traces_execution_resolution_not_null NOT NULL,
    CONSTRAINT normalized_traces_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT normalized_traces_created_address_check CHECK (((created_address IS NULL) OR (octet_length(created_address) = 20))),
    CONSTRAINT normalized_traces_depth_check CHECK ((depth >= 0)),
    CONSTRAINT normalized_traces_execution_address_length_check CHECK (((execution_address IS NULL) OR (octet_length(execution_address) = 20))),
    CONSTRAINT normalized_traces_execution_code_hash_length_check CHECK (((execution_code_hash IS NULL) OR (octet_length(execution_code_hash) = 32))),
    CONSTRAINT normalized_traces_execution_identity_check CHECK ((((execution_resolution = ANY (ARRAY['direct'::text, 'eip7702_delegate'::text])) AND (execution_address IS NOT NULL) AND (execution_code_hash IS NOT NULL)) OR ((execution_resolution = ANY (ARRAY['empty'::text, 'not_applicable'::text])) AND (execution_address IS NULL) AND (execution_code_hash IS NULL)) OR ((execution_resolution = 'unavailable'::text) AND (execution_code_hash IS NULL)))),
    CONSTRAINT normalized_traces_execution_resolution_check CHECK ((execution_resolution = ANY (ARRAY['direct'::text, 'eip7702_delegate'::text, 'empty'::text, 'unavailable'::text, 'not_applicable'::text]))),
    CONSTRAINT normalized_traces_from_address_check CHECK (((from_address IS NULL) OR (octet_length(from_address) = 20))),
    CONSTRAINT normalized_traces_gas_check CHECK (((gas IS NULL) OR (gas >= (0)::numeric))),
    CONSTRAINT normalized_traces_gas_used_check CHECK (((gas_used IS NULL) OR (gas_used >= (0)::numeric))),
    CONSTRAINT normalized_traces_to_address_check CHECK (((to_address IS NULL) OR (octet_length(to_address) = 20))),
    CONSTRAINT normalized_traces_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT normalized_traces_value_check CHECK (((value IS NULL) OR (value >= (0)::numeric)))
);

-- Table: normalized_traces_p_0_1000000

CREATE TABLE normalized_traces_p_0_1000000 (
    chain_id numeric(78,0) CONSTRAINT normalized_traces_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT normalized_traces_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT normalized_traces_block_hash_not_null NOT NULL,
    transaction_hash bytea CONSTRAINT normalized_traces_transaction_hash_not_null NOT NULL,
    transaction_index bigint CONSTRAINT normalized_traces_transaction_index_not_null NOT NULL,
    trace_path text CONSTRAINT normalized_traces_trace_path_not_null NOT NULL,
    parent_path text,
    depth integer CONSTRAINT normalized_traces_depth_not_null NOT NULL,
    call_type text CONSTRAINT normalized_traces_call_type_not_null NOT NULL,
    from_address bytea,
    to_address bytea,
    created_address bytea,
    value numeric(78,0),
    gas numeric(78,0),
    gas_used numeric(78,0),
    input bytea,
    output bytea,
    error text,
    reverted boolean CONSTRAINT normalized_traces_reverted_not_null NOT NULL,
    canonical boolean CONSTRAINT normalized_traces_canonical_not_null NOT NULL,
    direct_reverted boolean DEFAULT false CONSTRAINT normalized_traces_direct_reverted_not_null NOT NULL,
    execution_address bytea,
    execution_code_hash bytea,
    execution_resolution text DEFAULT 'unavailable'::text CONSTRAINT normalized_traces_execution_resolution_not_null NOT NULL,
    CONSTRAINT normalized_traces_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT normalized_traces_created_address_check CHECK (((created_address IS NULL) OR (octet_length(created_address) = 20))),
    CONSTRAINT normalized_traces_depth_check CHECK ((depth >= 0)),
    CONSTRAINT normalized_traces_execution_address_length_check CHECK (((execution_address IS NULL) OR (octet_length(execution_address) = 20))),
    CONSTRAINT normalized_traces_execution_code_hash_length_check CHECK (((execution_code_hash IS NULL) OR (octet_length(execution_code_hash) = 32))),
    CONSTRAINT normalized_traces_execution_identity_check CHECK ((((execution_resolution = ANY (ARRAY['direct'::text, 'eip7702_delegate'::text])) AND (execution_address IS NOT NULL) AND (execution_code_hash IS NOT NULL)) OR ((execution_resolution = ANY (ARRAY['empty'::text, 'not_applicable'::text])) AND (execution_address IS NULL) AND (execution_code_hash IS NULL)) OR ((execution_resolution = 'unavailable'::text) AND (execution_code_hash IS NULL)))),
    CONSTRAINT normalized_traces_execution_resolution_check CHECK ((execution_resolution = ANY (ARRAY['direct'::text, 'eip7702_delegate'::text, 'empty'::text, 'unavailable'::text, 'not_applicable'::text]))),
    CONSTRAINT normalized_traces_from_address_check CHECK (((from_address IS NULL) OR (octet_length(from_address) = 20))),
    CONSTRAINT normalized_traces_gas_check CHECK (((gas IS NULL) OR (gas >= (0)::numeric))),
    CONSTRAINT normalized_traces_gas_used_check CHECK (((gas_used IS NULL) OR (gas_used >= (0)::numeric))),
    CONSTRAINT normalized_traces_to_address_check CHECK (((to_address IS NULL) OR (octet_length(to_address) = 20))),
    CONSTRAINT normalized_traces_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT normalized_traces_value_check CHECK (((value IS NULL) OR (value >= (0)::numeric)))
);

-- Table: operator_labels

CREATE TABLE operator_labels (
    id bigint NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    object_kind text NOT NULL,
    object_key text NOT NULL,
    label text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT operator_labels_label_check CHECK (((length(label) >= 1) AND (length(label) <= 256))),
    CONSTRAINT operator_labels_object_key_check CHECK (((length(object_key) >= 1) AND (length(object_key) <= 256))),
    CONSTRAINT operator_labels_object_kind_check CHECK ((object_kind = ANY (ARRAY['address'::text, 'block'::text, 'transaction'::text, 'token'::text, 'contract'::text])))
);

-- Sequence: operator_labels_id_seq

ALTER TABLE operator_labels ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME operator_labels_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

-- Table: proxy_artifact_resolutions

CREATE TABLE proxy_artifact_resolutions (
    id bigint NOT NULL,
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
    CONSTRAINT proxy_artifact_resolutions_standard_version_check CHECK ((standard_version = '5.6.1'::text))
);

-- Sequence: proxy_artifact_resolutions_id_seq

CREATE SEQUENCE proxy_artifact_resolutions_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

-- Sequence Owned By: proxy_artifact_resolutions_id_seq

ALTER SEQUENCE proxy_artifact_resolutions_id_seq OWNED BY proxy_artifact_resolutions.id;

-- Table: proxy_detection_evidence

CREATE TABLE proxy_detection_evidence (
    id bigint NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    address bytea NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    stage_version integer DEFAULT 2 NOT NULL,
    code_hash bytea NOT NULL,
    candidate_kind text NOT NULL,
    detection_state text NOT NULL,
    reason text NOT NULL,
    canonical boolean NOT NULL,
    durable_job_id bigint,
    job_generation bigint,
    details jsonb DEFAULT '{}'::jsonb NOT NULL,
    observed_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT proxy_detection_evidence_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT proxy_detection_evidence_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT proxy_detection_evidence_candidate_kind_check CHECK ((candidate_kind = ANY (ARRAY['proxy'::text, 'beacon'::text, 'proxy_v2'::text]))),
    CONSTRAINT proxy_detection_evidence_check CHECK ((((durable_job_id IS NULL) AND (job_generation IS NULL)) OR ((durable_job_id IS NOT NULL) AND (job_generation > 0)))),
    CONSTRAINT proxy_detection_evidence_code_hash_check CHECK ((octet_length(code_hash) = 32)),
    CONSTRAINT proxy_detection_evidence_details_check CHECK ((jsonb_typeof(details) = 'object'::text)),
    CONSTRAINT proxy_detection_evidence_detection_state_check CHECK ((detection_state = ANY (ARRAY['not_detected'::text, 'rejected'::text, 'confirmed'::text, 'candidate'::text, 'inconsistent'::text, 'unknown'::text]))),
    CONSTRAINT proxy_detection_evidence_reason_check CHECK ((reason = ANY (ARRAY['empty_code'::text, 'not_proxy'::text, 'minimal_zero_implementation'::text, 'immutable_args_too_large'::text, 'immutable_args_creation_unverified'::text, 'self_implementation'::text, 'cwia_invalid_length'::text, 'cwia_immutable_args_too_large'::text, 'cwia_zero_implementation'::text, 'cwia_self_implementation'::text, 'implementation_has_no_code'::text, 'invalid_slot_address'::text, 'ambiguous_slots'::text, 'beacon_has_no_code'::text, 'invalid_beacon_implementation'::text, 'resolver'::text]))),
    CONSTRAINT proxy_detection_evidence_stage_version_check CHECK ((stage_version = 2)),
    CONSTRAINT proxy_detection_evidence_v2_shape_check CHECK ((((candidate_kind = 'proxy_v2'::text) AND (reason = 'resolver'::text) AND (detection_state = ANY (ARRAY['confirmed'::text, 'candidate'::text, 'inconsistent'::text, 'not_detected'::text, 'unknown'::text]))) OR ((candidate_kind <> 'proxy_v2'::text) AND (reason <> 'resolver'::text) AND (detection_state = ANY (ARRAY['not_detected'::text, 'rejected'::text])))))
);

-- Sequence: proxy_detection_evidence_id_seq

CREATE SEQUENCE proxy_detection_evidence_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

-- Sequence Owned By: proxy_detection_evidence_id_seq

ALTER SEQUENCE proxy_detection_evidence_id_seq OWNED BY proxy_detection_evidence.id;

-- Table: proxy_history_epochs

CREATE TABLE proxy_history_epochs (
    epoch_id bigint NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    durable_job_id bigint NOT NULL,
    requested_generation bigint NOT NULL,
    phase text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT proxy_history_epochs_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT proxy_history_epochs_block_number_check CHECK ((block_number >= (0)::numeric)),
    CONSTRAINT proxy_history_epochs_phase_check CHECK ((phase = ANY (ARRAY['requested'::text, 'published'::text]))),
    CONSTRAINT proxy_history_epochs_requested_generation_check CHECK ((requested_generation > 0))
);

-- Sequence: proxy_history_epochs_epoch_id_seq

ALTER TABLE proxy_history_epochs ALTER COLUMN epoch_id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME proxy_history_epochs_epoch_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

-- Table: proxy_initialization_events

CREATE TABLE proxy_initialization_events (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    log_index bigint NOT NULL,
    transaction_hash bytea NOT NULL,
    contract_address bytea NOT NULL,
    version numeric(20,0) NOT NULL,
    stage_version integer DEFAULT 2 NOT NULL,
    canonical boolean NOT NULL,
    CONSTRAINT proxy_initialization_events_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT proxy_initialization_events_contract_address_check CHECK ((octet_length(contract_address) = 20)),
    CONSTRAINT proxy_initialization_events_log_index_check CHECK ((log_index >= 0)),
    CONSTRAINT proxy_initialization_events_stage_version_check CHECK ((stage_version = 2)),
    CONSTRAINT proxy_initialization_events_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT proxy_initialization_events_version_check CHECK (((version >= (0)::numeric) AND (version <= '18446744073709551615'::numeric)))
);

-- Table: proxy_interaction_coverage_ranges

CREATE TABLE proxy_interaction_coverage_ranges (
    chain_id numeric(78,0) NOT NULL,
    start_block numeric(78,0) NOT NULL,
    start_block_hash bytea NOT NULL,
    end_block numeric(78,0) NOT NULL,
    end_block_hash bytea NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT proxy_interaction_coverage_ranges_check CHECK (((start_block >= (0)::numeric) AND (end_block >= start_block))),
    CONSTRAINT proxy_interaction_coverage_ranges_end_block_hash_check CHECK ((octet_length(end_block_hash) = 32)),
    CONSTRAINT proxy_interaction_coverage_ranges_start_block_hash_check CHECK ((octet_length(start_block_hash) = 32))
);

-- Table: proxy_interaction_covered_blocks

CREATE TABLE proxy_interaction_covered_blocks (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT proxy_interaction_covered_blocks_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT proxy_interaction_covered_blocks_block_number_check CHECK ((block_number >= (0)::numeric))
);

-- Table: proxy_observation_generations

CREATE TABLE proxy_observation_generations (
    id bigint NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    proxy_address bytea NOT NULL,
    observation_block_hash bytea NOT NULL,
    observation_stage_version integer CONSTRAINT proxy_observation_generation_observation_stage_version_not_null NOT NULL,
    durable_job_id bigint,
    job_generation bigint,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT proxy_observation_generations_check CHECK ((((durable_job_id IS NULL) AND (job_generation IS NULL)) OR ((durable_job_id IS NOT NULL) AND (job_generation > 0)))),
    CONSTRAINT proxy_observation_generations_observation_stage_version_check CHECK ((observation_stage_version = 2))
);

-- Sequence: proxy_observation_generations_id_seq

CREATE SEQUENCE proxy_observation_generations_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

-- Sequence Owned By: proxy_observation_generations_id_seq

ALTER SEQUENCE proxy_observation_generations_id_seq OWNED BY proxy_observation_generations.id;

-- Table: proxy_observations

CREATE TABLE proxy_observations (
    chain_id numeric(78,0) NOT NULL,
    proxy_address bytea NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    proxy_code_hash bytea NOT NULL,
    proxy_kind text NOT NULL,
    implementation_address bytea,
    beacon_address bytea,
    implementation_code_hash bytea,
    confidence text NOT NULL,
    canonical boolean NOT NULL,
    details jsonb DEFAULT '{}'::jsonb NOT NULL,
    stage_version integer DEFAULT 2 NOT NULL,
    proxy_pattern text DEFAULT 'unknown'::text NOT NULL,
    standard_version text,
    admin_address bytea,
    admin_code_hash bytea,
    beacon_code_hash bytea,
    immutable_args bytea,
    evidence_state text DEFAULT 'generic'::text NOT NULL,
    CONSTRAINT proxy_observations_admin_identity_check CHECK ((((admin_address IS NULL) AND (admin_code_hash IS NULL)) OR ((octet_length(admin_address) = 20) AND (octet_length(admin_code_hash) = 32)))),
    CONSTRAINT proxy_observations_beacon_address_check CHECK (((beacon_address IS NULL) OR (octet_length(beacon_address) = 20))),
    CONSTRAINT proxy_observations_beacon_code_hash_check CHECK (((beacon_code_hash IS NULL) OR (octet_length(beacon_code_hash) = 32))),
    CONSTRAINT proxy_observations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT proxy_observations_confidence_check CHECK ((confidence = ANY (ARRAY['verified'::text, 'high'::text, 'inferred'::text, 'guess'::text]))),
    CONSTRAINT proxy_observations_evidence_state_check CHECK ((evidence_state = ANY (ARRAY['exact'::text, 'partial'::text, 'generic'::text]))),
    CONSTRAINT proxy_observations_immutable_args_check CHECK (((immutable_args IS NULL) OR (octet_length(immutable_args) <= 24531))),
    CONSTRAINT proxy_observations_implementation_address_check CHECK (((implementation_address IS NULL) OR (octet_length(implementation_address) = 20))),
    CONSTRAINT proxy_observations_implementation_code_hash_check CHECK (((implementation_code_hash IS NULL) OR (octet_length(implementation_code_hash) = 32))),
    CONSTRAINT proxy_observations_pattern_check CHECK ((proxy_pattern = ANY (ARRAY['clone'::text, 'erc1967'::text, 'transparent'::text, 'uups'::text, 'beacon'::text, 'unknown'::text]))),
    CONSTRAINT proxy_observations_proxy_address_check CHECK ((octet_length(proxy_address) = 20)),
    CONSTRAINT proxy_observations_proxy_code_hash_check CHECK ((octet_length(proxy_code_hash) = 32)),
    CONSTRAINT proxy_observations_proxy_kind_check CHECK ((proxy_kind = ANY (ARRAY['eip1167'::text, 'cwia'::text, 'eip1967'::text, 'beacon'::text, 'unknown'::text]))),
    CONSTRAINT proxy_observations_stage_version_check CHECK ((stage_version = ANY (ARRAY[1, 2]))),
    CONSTRAINT proxy_observations_standard_check CHECK (((standard_version IS NULL) OR (standard_version = '5.6.1'::text)))
);

-- Table: proxy_replay_targets

CREATE TABLE proxy_replay_targets (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    address bytea NOT NULL,
    target_kind text NOT NULL,
    source_kind text NOT NULL,
    source_verification_job_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT proxy_replay_targets_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT proxy_replay_targets_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT proxy_replay_targets_source_kind_check CHECK ((source_kind = 'verification_publication'::text)),
    CONSTRAINT proxy_replay_targets_target_kind_check CHECK ((target_kind = ANY (ARRAY['proxy'::text, 'beacon'::text, 'uups'::text])))
);

-- Table: proxy_upgrade_events

CREATE TABLE proxy_upgrade_events (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    log_index bigint NOT NULL,
    transaction_hash bytea NOT NULL,
    emitter_address bytea NOT NULL,
    event_kind text NOT NULL,
    target_address bytea NOT NULL,
    stage_version integer DEFAULT 2 NOT NULL,
    canonical boolean NOT NULL,
    CONSTRAINT proxy_upgrade_events_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT proxy_upgrade_events_emitter_address_check CHECK ((octet_length(emitter_address) = 20)),
    CONSTRAINT proxy_upgrade_events_event_kind_check CHECK ((event_kind = ANY (ARRAY['implementation'::text, 'beacon'::text]))),
    CONSTRAINT proxy_upgrade_events_log_index_check CHECK ((log_index >= 0)),
    CONSTRAINT proxy_upgrade_events_stage_version_check CHECK ((stage_version = 2)),
    CONSTRAINT proxy_upgrade_events_target_address_check CHECK ((octet_length(target_address) = 20)),
    CONSTRAINT proxy_upgrade_events_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32))
);

-- View: published_diamond_loupe_snapshots

CREATE VIEW published_diamond_loupe_snapshots AS
 SELECT snapshot.id,
    snapshot.chain_id,
    snapshot.diamond_address,
    snapshot.block_number,
    snapshot.block_hash,
    snapshot.stage_version,
    snapshot.detection_state,
    snapshot.completeness,
    snapshot.validation,
    snapshot.standard_diamond_cut,
    snapshot.standard_diamond_cut_facet,
    snapshot.loupe_interface_reported,
    snapshot.truncated,
    snapshot.truncation_reason,
    snapshot.warnings,
    snapshot.canonical,
    snapshot.durable_job_id,
    snapshot.job_generation,
    snapshot.observed_at
   FROM (diamond_loupe_snapshots snapshot
     JOIN published_block_stage_results published ON (((published.chain_id = snapshot.chain_id) AND (published.block_number = snapshot.block_number) AND (published.block_hash = snapshot.block_hash) AND (published.stage = 'proxy'::text) AND (published.stage_version = snapshot.stage_version) AND (published.state = 'complete'::text) AND (published.durable_job_id = snapshot.durable_job_id) AND (published.job_generation = snapshot.job_generation))))
  WHERE snapshot.canonical;

-- View: published_erc4337_user_operations

CREATE VIEW published_erc4337_user_operations AS
 SELECT operation.chain_id,
    operation.configuration_digest,
    operation.block_number,
    operation.block_hash,
    operation.transaction_hash,
    operation.transaction_index,
    operation.operation_index,
    operation.event_log_index,
    operation.user_op_hash,
    operation.entry_point,
    operation.entry_point_version,
    operation.sender,
    operation.nonce,
    operation.nonce_key,
    operation.nonce_sequence,
    operation.bundler,
    operation.beneficiary,
    operation.init_kind,
    operation.factory,
    operation.paymaster,
    operation.aggregator,
    operation.success,
    operation.actual_gas_cost,
    operation.actual_gas_used,
    operation.call_gas_limit,
    operation.verification_gas_limit,
    operation.pre_verification_gas,
    operation.max_fee_per_gas,
    operation.max_priority_fee_per_gas,
    operation.paymaster_verification_gas_limit,
    operation.paymaster_post_op_gas_limit,
    operation.init_code,
    operation.factory_data,
    operation.call_data,
    operation.paymaster_and_data,
    operation.paymaster_data,
    operation.paymaster_signature,
    operation.signature,
    operation.account_gas_limits,
    operation.gas_fees,
    operation.aggregated_signature,
    operation.canonical,
    operation.created_at,
    block."timestamp" AS block_timestamp,
    finality.safe_number,
    finality.finalized_number,
    covered.durable_job_id,
    covered.job_generation
   FROM (((((erc4337_user_operations operation
     JOIN canonical_blocks canonical ON (((canonical.chain_id = operation.chain_id) AND (canonical.number = operation.block_number) AND (canonical.block_hash = operation.block_hash))))
     JOIN blocks block ON (((block.chain_id = operation.chain_id) AND (block.number = operation.block_number) AND (block.hash = operation.block_hash))))
     JOIN erc4337_covered_blocks covered ON (((covered.chain_id = operation.chain_id) AND (covered.configuration_digest = operation.configuration_digest) AND (covered.block_number = operation.block_number) AND (covered.block_hash = operation.block_hash))))
     JOIN published_block_stage_results published ON (((published.chain_id = covered.chain_id) AND (published.block_number = covered.block_number) AND (published.block_hash = covered.block_hash) AND (published.stage = 'userop'::text) AND (published.stage_version = 1) AND (published.state = 'complete'::text) AND (published.durable_job_id = covered.durable_job_id) AND (published.job_generation = covered.job_generation) AND ((published.details ->> 'configuration_digest'::text) = encode(covered.configuration_digest, 'hex'::text)))))
     LEFT JOIN chain_finality finality ON ((finality.chain_id = operation.chain_id)))
  WHERE operation.canonical;

-- Table: receipts

CREATE TABLE receipts (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    tx_index bigint NOT NULL,
    tx_hash bytea NOT NULL,
    raw jsonb NOT NULL,
    CONSTRAINT receipts_tx_index_check CHECK ((tx_index >= 0))
)
PARTITION BY RANGE (block_number);

-- Table: receipts_default

CREATE TABLE receipts_default (
    chain_id numeric(78,0) CONSTRAINT receipts_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT receipts_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT receipts_block_hash_not_null NOT NULL,
    tx_index bigint CONSTRAINT receipts_tx_index_not_null NOT NULL,
    tx_hash bytea CONSTRAINT receipts_tx_hash_not_null NOT NULL,
    raw jsonb CONSTRAINT receipts_raw_not_null NOT NULL,
    CONSTRAINT receipts_tx_index_check CHECK ((tx_index >= 0))
);

-- Table: receipts_p_0_1000000

CREATE TABLE receipts_p_0_1000000 (
    chain_id numeric(78,0) CONSTRAINT receipts_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT receipts_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT receipts_block_hash_not_null NOT NULL,
    tx_index bigint CONSTRAINT receipts_tx_index_not_null NOT NULL,
    tx_hash bytea CONSTRAINT receipts_tx_hash_not_null NOT NULL,
    raw jsonb CONSTRAINT receipts_raw_not_null NOT NULL,
    CONSTRAINT receipts_tx_index_check CHECK ((tx_index >= 0))
);

-- Table: reorg_events

CREATE TABLE reorg_events (
    id bigint NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    ancestor_number numeric(78,0) NOT NULL,
    ancestor_hash bytea NOT NULL,
    old_tip_number numeric(78,0),
    old_tip_hash bytea,
    new_tip_number numeric(78,0),
    new_tip_hash bytea,
    detached jsonb NOT NULL,
    attached jsonb NOT NULL,
    reason text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

-- Sequence: reorg_events_id_seq

ALTER TABLE reorg_events ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME reorg_events_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

-- Table: repair_requests

CREATE TABLE repair_requests (
    id bigint NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    operation text NOT NULL,
    stage text NOT NULL,
    from_block numeric(78,0) NOT NULL,
    to_block numeric(78,0) NOT NULL,
    allow_finalized boolean DEFAULT false NOT NULL,
    reason text NOT NULL,
    status text DEFAULT 'queued'::text NOT NULL,
    requested_at timestamp with time zone DEFAULT now() NOT NULL,
    started_at timestamp with time zone,
    completed_at timestamp with time zone,
    last_error text,
    CONSTRAINT repair_requests_check CHECK (((from_block >= (0)::numeric) AND (to_block >= from_block))),
    CONSTRAINT repair_requests_operation_check CHECK ((operation = ANY (ARRAY['repair'::text, 'reindex'::text]))),
    CONSTRAINT repair_requests_reason_check CHECK (((length(reason) >= 1) AND (length(reason) <= 1024))),
    CONSTRAINT repair_requests_status_check CHECK ((status = ANY (ARRAY['queued'::text, 'running'::text, 'done'::text, 'failed'::text, 'cancelled'::text])))
);

-- Sequence: repair_requests_id_seq

ALTER TABLE repair_requests ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME repair_requests_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

-- Table: runtime_events

CREATE TABLE runtime_events (
    id bigint NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT runtime_events_event_type_check CHECK ((event_type = ANY (ARRAY['head'::text, 'reorg'::text, 'status'::text]))),
    CONSTRAINT runtime_events_payload_check CHECK ((octet_length((payload)::text) <= 8192))
);

-- Sequence: runtime_events_id_seq

ALTER TABLE runtime_events ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME runtime_events_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

-- Table: search_catalog_documents

CREATE TABLE search_catalog_documents (
    id bigint NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    source_kind text NOT NULL,
    source_identity text NOT NULL,
    logical_identity text NOT NULL,
    valid_from_generation bigint NOT NULL,
    valid_to_generation bigint,
    result_kind text,
    result_key text,
    result_label text,
    exact_terms text[] DEFAULT '{}'::text[] NOT NULL,
    partial_terms text[] DEFAULT '{}'::text[] NOT NULL,
    block_number numeric(78,0),
    block_hash bytea,
    target_address bytea,
    code_hash bytea,
    valid_from_block numeric(78,0),
    valid_to_block numeric(78,0),
    source_canonical boolean,
    recorded_at timestamp with time zone DEFAULT now() NOT NULL,
    verification_match_type text,
    verification_request_digest bytea,
    verification_job_id uuid,
    name_observation_id bigint,
    name_source text,
    CONSTRAINT search_catalog_documents_block_hash_check CHECK (((block_hash IS NULL) OR (octet_length(block_hash) = 32))),
    CONSTRAINT search_catalog_documents_check CHECK (((valid_to_generation IS NULL) OR (valid_to_generation > valid_from_generation))),
    CONSTRAINT search_catalog_documents_check1 CHECK (((result_kind IS NULL) = (result_key IS NULL))),
    CONSTRAINT search_catalog_documents_check2 CHECK (((result_kind IS NULL) = (result_label IS NULL))),
    CONSTRAINT search_catalog_documents_check3 CHECK (((block_number IS NULL) = (block_hash IS NULL))),
    CONSTRAINT search_catalog_documents_check4 CHECK (((valid_to_block IS NULL) OR (valid_from_block IS NOT NULL))),
    CONSTRAINT search_catalog_documents_check5 CHECK (((valid_to_block IS NULL) OR (valid_to_block >= valid_from_block))),
    CONSTRAINT search_catalog_documents_check6 CHECK ((((source_kind = 'code'::text) AND (result_kind IS NULL) AND (target_address IS NOT NULL) AND (code_hash IS NOT NULL)) OR ((source_kind <> 'code'::text) AND (result_kind IS NOT NULL)))),
    CONSTRAINT search_catalog_documents_code_hash_check CHECK (((code_hash IS NULL) OR (octet_length(code_hash) = 32))),
    CONSTRAINT search_catalog_documents_logical_identity_check CHECK (((length(logical_identity) >= 1) AND (length(logical_identity) <= 1024))),
    CONSTRAINT search_catalog_documents_result_key_check CHECK (((result_key IS NULL) OR ((length(result_key) >= 1) AND (length(result_key) <= 256)))),
    CONSTRAINT search_catalog_documents_result_kind_check CHECK (((result_kind IS NULL) OR (result_kind = ANY (ARRAY['address'::text, 'block'::text, 'transaction'::text, 'token'::text, 'contract'::text])))),
    CONSTRAINT search_catalog_documents_result_label_check CHECK (((result_label IS NULL) OR ((length(result_label) >= 1) AND (length(result_label) <= 4096)))),
    CONSTRAINT search_catalog_documents_source_identity_check CHECK (((length(source_identity) >= 1) AND (length(source_identity) <= 1024))),
    CONSTRAINT search_catalog_documents_source_kind_check CHECK ((source_kind = ANY (ARRAY['label'::text, 'name'::text, 'token'::text, 'verified_contract'::text, 'code'::text]))),
    CONSTRAINT search_catalog_documents_target_address_check CHECK (((target_address IS NULL) OR (octet_length(target_address) = 20))),
    CONSTRAINT search_catalog_documents_valid_from_generation_check CHECK ((valid_from_generation >= 1)),
    CONSTRAINT search_catalog_verified_result_shape CHECK ((((source_kind <> 'verified_contract'::text) AND (verification_match_type IS NULL) AND (verification_request_digest IS NULL) AND (verification_job_id IS NULL)) OR ((source_kind = 'verified_contract'::text) AND (((verification_match_type IS NULL) AND (verification_request_digest IS NULL) AND (verification_job_id IS NULL)) OR ((verification_match_type = ANY (ARRAY['full'::text, 'partial'::text])) AND (octet_length(verification_request_digest) = 32) AND (verification_job_id IS NOT NULL))))))
);

-- Sequence: search_catalog_documents_id_seq

ALTER TABLE search_catalog_documents ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME search_catalog_documents_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

-- Table: search_catalog_generations

CREATE TABLE search_catalog_generations (
    chain_id numeric(78,0) NOT NULL,
    generation bigint DEFAULT 0 NOT NULL,
    min_generation bigint DEFAULT 0 NOT NULL,
    retention_generations bigint DEFAULT 100000 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT search_catalog_generations_check CHECK (((min_generation >= 0) AND (min_generation <= generation))),
    CONSTRAINT search_catalog_generations_generation_check CHECK ((generation >= 0)),
    CONSTRAINT search_catalog_generations_retention_generations_check CHECK (((retention_generations >= 1000) AND (retention_generations <= 10000000)))
);

-- Table: sync_runtime_status

CREATE TABLE sync_runtime_status (
    chain_id numeric(78,0) NOT NULL,
    latest_number numeric(78,0),
    indexed_number numeric(78,0),
    ready boolean DEFAULT false NOT NULL,
    last_poll_at timestamp with time zone NOT NULL,
    last_error_code text DEFAULT ''::text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    highest_covered_number numeric(78,0),
    backfill_complete boolean DEFAULT false NOT NULL,
    CONSTRAINT sync_runtime_status_backfill_complete_check CHECK (((NOT backfill_complete) OR ((latest_number IS NOT NULL) AND (indexed_number IS NOT NULL) AND (highest_covered_number IS NOT NULL) AND (indexed_number >= latest_number) AND (indexed_number <= highest_covered_number)))),
    CONSTRAINT sync_runtime_status_check CHECK (((NOT ready) OR ((latest_number IS NOT NULL) AND (indexed_number IS NOT NULL) AND (indexed_number >= latest_number)))),
    CONSTRAINT sync_runtime_status_highest_covered_number_check CHECK (((highest_covered_number IS NULL) OR ((highest_covered_number >= (0)::numeric) AND ((indexed_number IS NULL) OR (indexed_number <= highest_covered_number))))),
    CONSTRAINT sync_runtime_status_indexed_number_check CHECK (((indexed_number IS NULL) OR (indexed_number >= (0)::numeric))),
    CONSTRAINT sync_runtime_status_last_error_code_check CHECK ((length(last_error_code) <= 64)),
    CONSTRAINT sync_runtime_status_last_error_code_check1 CHECK ((last_error_code ~ '^[a-z0-9_]*$'::text)),
    CONSTRAINT sync_runtime_status_latest_number_check CHECK (((latest_number IS NULL) OR (latest_number >= (0)::numeric))),
    CONSTRAINT sync_runtime_status_ready_requires_backfill_check CHECK (((NOT ready) OR backfill_complete))
);

-- Table: sync_runtime_status_writer_leases

CREATE TABLE sync_runtime_status_writer_leases (
    chain_id numeric(78,0) NOT NULL,
    reporter_id text NOT NULL,
    observed_latest_number numeric(78,0),
    observed_latest_known boolean CONSTRAINT sync_runtime_status_writer_lease_observed_latest_known_not_null NOT NULL,
    safety_halt boolean DEFAULT false NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT sync_runtime_status_writer_leases_check CHECK (((observed_latest_known AND (observed_latest_number IS NOT NULL) AND (observed_latest_number >= (0)::numeric)) OR ((NOT observed_latest_known) AND (observed_latest_number IS NULL)))),
    CONSTRAINT sync_runtime_status_writer_leases_reporter_id_check CHECK (((length(reporter_id) >= 1) AND (length(reporter_id) <= 128)))
);

-- Table: token_balance_deltas

CREATE TABLE token_balance_deltas (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    log_index bigint NOT NULL,
    sub_index integer DEFAULT 0 NOT NULL,
    token_address bytea NOT NULL,
    owner_address bytea NOT NULL,
    token_id numeric(78,0),
    delta numeric(79,0) NOT NULL,
    canonical boolean NOT NULL,
    CONSTRAINT token_balance_deltas_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT token_balance_deltas_owner_address_check CHECK ((octet_length(owner_address) = 20)),
    CONSTRAINT token_balance_deltas_token_address_check CHECK ((octet_length(token_address) = 20)),
    CONSTRAINT token_balance_deltas_token_id_check CHECK (((token_id IS NULL) OR (token_id >= (0)::numeric)))
)
PARTITION BY RANGE (block_number);

-- Table: token_balance_deltas_default

CREATE TABLE token_balance_deltas_default (
    chain_id numeric(78,0) CONSTRAINT token_balance_deltas_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT token_balance_deltas_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT token_balance_deltas_block_hash_not_null NOT NULL,
    log_index bigint CONSTRAINT token_balance_deltas_log_index_not_null NOT NULL,
    sub_index integer DEFAULT 0 CONSTRAINT token_balance_deltas_sub_index_not_null NOT NULL,
    token_address bytea CONSTRAINT token_balance_deltas_token_address_not_null NOT NULL,
    owner_address bytea CONSTRAINT token_balance_deltas_owner_address_not_null NOT NULL,
    token_id numeric(78,0),
    delta numeric(79,0) CONSTRAINT token_balance_deltas_delta_not_null NOT NULL,
    canonical boolean CONSTRAINT token_balance_deltas_canonical_not_null NOT NULL,
    CONSTRAINT token_balance_deltas_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT token_balance_deltas_owner_address_check CHECK ((octet_length(owner_address) = 20)),
    CONSTRAINT token_balance_deltas_token_address_check CHECK ((octet_length(token_address) = 20)),
    CONSTRAINT token_balance_deltas_token_id_check CHECK (((token_id IS NULL) OR (token_id >= (0)::numeric)))
);

-- Table: token_balance_deltas_p_0_1000000

CREATE TABLE token_balance_deltas_p_0_1000000 (
    chain_id numeric(78,0) CONSTRAINT token_balance_deltas_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT token_balance_deltas_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT token_balance_deltas_block_hash_not_null NOT NULL,
    log_index bigint CONSTRAINT token_balance_deltas_log_index_not_null NOT NULL,
    sub_index integer DEFAULT 0 CONSTRAINT token_balance_deltas_sub_index_not_null NOT NULL,
    token_address bytea CONSTRAINT token_balance_deltas_token_address_not_null NOT NULL,
    owner_address bytea CONSTRAINT token_balance_deltas_owner_address_not_null NOT NULL,
    token_id numeric(78,0),
    delta numeric(79,0) CONSTRAINT token_balance_deltas_delta_not_null NOT NULL,
    canonical boolean CONSTRAINT token_balance_deltas_canonical_not_null NOT NULL,
    CONSTRAINT token_balance_deltas_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT token_balance_deltas_owner_address_check CHECK ((octet_length(owner_address) = 20)),
    CONSTRAINT token_balance_deltas_token_address_check CHECK ((octet_length(token_address) = 20)),
    CONSTRAINT token_balance_deltas_token_id_check CHECK (((token_id IS NULL) OR (token_id >= (0)::numeric)))
);

-- Table: token_contracts

CREATE TABLE token_contracts (
    chain_id numeric(78,0) NOT NULL,
    address bytea NOT NULL,
    code_hash bytea NOT NULL,
    standard text NOT NULL,
    confidence text NOT NULL,
    name text,
    symbol text,
    decimals integer,
    total_supply numeric(78,0),
    metadata_state text DEFAULT 'pending'::text NOT NULL,
    observed_block_number numeric(78,0) NOT NULL,
    observed_block_hash bytea NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT token_contracts_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT token_contracts_code_hash_check CHECK ((octet_length(code_hash) = 32)),
    CONSTRAINT token_contracts_confidence_check CHECK ((confidence = ANY (ARRAY['verified'::text, 'high'::text, 'inferred'::text, 'guess'::text]))),
    CONSTRAINT token_contracts_decimals_check CHECK (((decimals IS NULL) OR ((decimals >= 0) AND (decimals <= 255)))),
    CONSTRAINT token_contracts_metadata_state_check CHECK ((metadata_state = ANY (ARRAY['pending'::text, 'complete'::text, 'unavailable'::text, 'failed'::text]))),
    CONSTRAINT token_contracts_observed_block_hash_check CHECK ((octet_length(observed_block_hash) = 32)),
    CONSTRAINT token_contracts_standard_check CHECK ((standard = ANY (ARRAY['erc20'::text, 'erc721'::text, 'erc1155'::text, 'unknown'::text]))),
    CONSTRAINT token_contracts_total_supply_check CHECK (((total_supply IS NULL) OR (total_supply >= (0)::numeric)))
);

-- Table: token_events

CREATE TABLE token_events (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    log_index bigint NOT NULL,
    sub_index integer DEFAULT 0 NOT NULL,
    transaction_hash bytea NOT NULL,
    token_address bytea NOT NULL,
    standard text NOT NULL,
    event_kind text NOT NULL,
    operator bytea,
    from_address bytea,
    to_address bytea,
    token_id numeric(78,0),
    amount numeric(78,0),
    canonical boolean NOT NULL,
    confidence text NOT NULL,
    raw jsonb NOT NULL,
    CONSTRAINT token_events_amount_check CHECK (((amount IS NULL) OR (amount >= (0)::numeric))),
    CONSTRAINT token_events_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT token_events_check CHECK (((log_index >= 0) AND (sub_index >= 0))),
    CONSTRAINT token_events_confidence_check CHECK ((confidence = ANY (ARRAY['verified'::text, 'high'::text, 'inferred'::text, 'guess'::text]))),
    CONSTRAINT token_events_event_kind_check CHECK ((event_kind = ANY (ARRAY['transfer'::text, 'approval'::text, 'approval_for_all'::text, 'mint'::text, 'burn'::text]))),
    CONSTRAINT token_events_from_address_check CHECK (((from_address IS NULL) OR (octet_length(from_address) = 20))),
    CONSTRAINT token_events_operator_check CHECK (((operator IS NULL) OR (octet_length(operator) = 20))),
    CONSTRAINT token_events_standard_check CHECK ((standard = ANY (ARRAY['erc20'::text, 'erc721'::text, 'erc1155'::text]))),
    CONSTRAINT token_events_to_address_check CHECK (((to_address IS NULL) OR (octet_length(to_address) = 20))),
    CONSTRAINT token_events_token_address_check CHECK ((octet_length(token_address) = 20)),
    CONSTRAINT token_events_token_id_check CHECK (((token_id IS NULL) OR (token_id >= (0)::numeric))),
    CONSTRAINT token_events_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32))
)
PARTITION BY RANGE (block_number);

-- Table: token_events_default

CREATE TABLE token_events_default (
    chain_id numeric(78,0) CONSTRAINT token_events_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT token_events_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT token_events_block_hash_not_null NOT NULL,
    log_index bigint CONSTRAINT token_events_log_index_not_null NOT NULL,
    sub_index integer DEFAULT 0 CONSTRAINT token_events_sub_index_not_null NOT NULL,
    transaction_hash bytea CONSTRAINT token_events_transaction_hash_not_null NOT NULL,
    token_address bytea CONSTRAINT token_events_token_address_not_null NOT NULL,
    standard text CONSTRAINT token_events_standard_not_null NOT NULL,
    event_kind text CONSTRAINT token_events_event_kind_not_null NOT NULL,
    operator bytea,
    from_address bytea,
    to_address bytea,
    token_id numeric(78,0),
    amount numeric(78,0),
    canonical boolean CONSTRAINT token_events_canonical_not_null NOT NULL,
    confidence text CONSTRAINT token_events_confidence_not_null NOT NULL,
    raw jsonb CONSTRAINT token_events_raw_not_null NOT NULL,
    CONSTRAINT token_events_amount_check CHECK (((amount IS NULL) OR (amount >= (0)::numeric))),
    CONSTRAINT token_events_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT token_events_check CHECK (((log_index >= 0) AND (sub_index >= 0))),
    CONSTRAINT token_events_confidence_check CHECK ((confidence = ANY (ARRAY['verified'::text, 'high'::text, 'inferred'::text, 'guess'::text]))),
    CONSTRAINT token_events_event_kind_check CHECK ((event_kind = ANY (ARRAY['transfer'::text, 'approval'::text, 'approval_for_all'::text, 'mint'::text, 'burn'::text]))),
    CONSTRAINT token_events_from_address_check CHECK (((from_address IS NULL) OR (octet_length(from_address) = 20))),
    CONSTRAINT token_events_operator_check CHECK (((operator IS NULL) OR (octet_length(operator) = 20))),
    CONSTRAINT token_events_standard_check CHECK ((standard = ANY (ARRAY['erc20'::text, 'erc721'::text, 'erc1155'::text]))),
    CONSTRAINT token_events_to_address_check CHECK (((to_address IS NULL) OR (octet_length(to_address) = 20))),
    CONSTRAINT token_events_token_address_check CHECK ((octet_length(token_address) = 20)),
    CONSTRAINT token_events_token_id_check CHECK (((token_id IS NULL) OR (token_id >= (0)::numeric))),
    CONSTRAINT token_events_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32))
);

-- Table: token_events_p_0_1000000

CREATE TABLE token_events_p_0_1000000 (
    chain_id numeric(78,0) CONSTRAINT token_events_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT token_events_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT token_events_block_hash_not_null NOT NULL,
    log_index bigint CONSTRAINT token_events_log_index_not_null NOT NULL,
    sub_index integer DEFAULT 0 CONSTRAINT token_events_sub_index_not_null NOT NULL,
    transaction_hash bytea CONSTRAINT token_events_transaction_hash_not_null NOT NULL,
    token_address bytea CONSTRAINT token_events_token_address_not_null NOT NULL,
    standard text CONSTRAINT token_events_standard_not_null NOT NULL,
    event_kind text CONSTRAINT token_events_event_kind_not_null NOT NULL,
    operator bytea,
    from_address bytea,
    to_address bytea,
    token_id numeric(78,0),
    amount numeric(78,0),
    canonical boolean CONSTRAINT token_events_canonical_not_null NOT NULL,
    confidence text CONSTRAINT token_events_confidence_not_null NOT NULL,
    raw jsonb CONSTRAINT token_events_raw_not_null NOT NULL,
    CONSTRAINT token_events_amount_check CHECK (((amount IS NULL) OR (amount >= (0)::numeric))),
    CONSTRAINT token_events_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT token_events_check CHECK (((log_index >= 0) AND (sub_index >= 0))),
    CONSTRAINT token_events_confidence_check CHECK ((confidence = ANY (ARRAY['verified'::text, 'high'::text, 'inferred'::text, 'guess'::text]))),
    CONSTRAINT token_events_event_kind_check CHECK ((event_kind = ANY (ARRAY['transfer'::text, 'approval'::text, 'approval_for_all'::text, 'mint'::text, 'burn'::text]))),
    CONSTRAINT token_events_from_address_check CHECK (((from_address IS NULL) OR (octet_length(from_address) = 20))),
    CONSTRAINT token_events_operator_check CHECK (((operator IS NULL) OR (octet_length(operator) = 20))),
    CONSTRAINT token_events_standard_check CHECK ((standard = ANY (ARRAY['erc20'::text, 'erc721'::text, 'erc1155'::text]))),
    CONSTRAINT token_events_to_address_check CHECK (((to_address IS NULL) OR (octet_length(to_address) = 20))),
    CONSTRAINT token_events_token_address_check CHECK ((octet_length(token_address) = 20)),
    CONSTRAINT token_events_token_id_check CHECK (((token_id IS NULL) OR (token_id >= (0)::numeric))),
    CONSTRAINT token_events_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32))
);

-- Table: trace_log_attributions

CREATE TABLE trace_log_attributions (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    transaction_hash bytea NOT NULL,
    log_index bigint NOT NULL,
    trace_path text NOT NULL,
    call_type text NOT NULL,
    execution_address bytea NOT NULL,
    canonical boolean NOT NULL,
    CONSTRAINT trace_log_attributions_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT trace_log_attributions_call_type_check CHECK ((call_type = ANY (ARRAY['CALL'::text, 'CALLCODE'::text, 'DELEGATECALL'::text, 'STATICCALL'::text, 'CREATE'::text, 'CREATE2'::text]))),
    CONSTRAINT trace_log_attributions_execution_address_check CHECK ((octet_length(execution_address) = 20)),
    CONSTRAINT trace_log_attributions_log_index_check CHECK ((log_index >= 0)),
    CONSTRAINT trace_log_attributions_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32))
)
PARTITION BY RANGE (block_number);

-- Table: trace_log_attributions_default

CREATE TABLE trace_log_attributions_default (
    chain_id numeric(78,0) CONSTRAINT trace_log_attributions_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT trace_log_attributions_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT trace_log_attributions_block_hash_not_null NOT NULL,
    transaction_hash bytea CONSTRAINT trace_log_attributions_transaction_hash_not_null NOT NULL,
    log_index bigint CONSTRAINT trace_log_attributions_log_index_not_null NOT NULL,
    trace_path text CONSTRAINT trace_log_attributions_trace_path_not_null NOT NULL,
    call_type text CONSTRAINT trace_log_attributions_call_type_not_null NOT NULL,
    execution_address bytea CONSTRAINT trace_log_attributions_execution_address_not_null NOT NULL,
    canonical boolean CONSTRAINT trace_log_attributions_canonical_not_null NOT NULL,
    CONSTRAINT trace_log_attributions_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT trace_log_attributions_call_type_check CHECK ((call_type = ANY (ARRAY['CALL'::text, 'CALLCODE'::text, 'DELEGATECALL'::text, 'STATICCALL'::text, 'CREATE'::text, 'CREATE2'::text]))),
    CONSTRAINT trace_log_attributions_execution_address_check CHECK ((octet_length(execution_address) = 20)),
    CONSTRAINT trace_log_attributions_log_index_check CHECK ((log_index >= 0)),
    CONSTRAINT trace_log_attributions_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32))
);

-- Table: trace_log_attributions_p_0_1000000

CREATE TABLE trace_log_attributions_p_0_1000000 (
    chain_id numeric(78,0) CONSTRAINT trace_log_attributions_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT trace_log_attributions_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT trace_log_attributions_block_hash_not_null NOT NULL,
    transaction_hash bytea CONSTRAINT trace_log_attributions_transaction_hash_not_null NOT NULL,
    log_index bigint CONSTRAINT trace_log_attributions_log_index_not_null NOT NULL,
    trace_path text CONSTRAINT trace_log_attributions_trace_path_not_null NOT NULL,
    call_type text CONSTRAINT trace_log_attributions_call_type_not_null NOT NULL,
    execution_address bytea CONSTRAINT trace_log_attributions_execution_address_not_null NOT NULL,
    canonical boolean CONSTRAINT trace_log_attributions_canonical_not_null NOT NULL,
    CONSTRAINT trace_log_attributions_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT trace_log_attributions_call_type_check CHECK ((call_type = ANY (ARRAY['CALL'::text, 'CALLCODE'::text, 'DELEGATECALL'::text, 'STATICCALL'::text, 'CREATE'::text, 'CREATE2'::text]))),
    CONSTRAINT trace_log_attributions_execution_address_check CHECK ((octet_length(execution_address) = 20)),
    CONSTRAINT trace_log_attributions_log_index_check CHECK ((log_index >= 0)),
    CONSTRAINT trace_log_attributions_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32))
);

-- Table: transaction_effective_execution_identities

CREATE TABLE transaction_effective_execution_identities (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) CONSTRAINT transaction_effective_execution_identitie_block_number_not_null NOT NULL,
    block_hash bytea NOT NULL,
    transaction_hash bytea CONSTRAINT transaction_effective_execution_ident_transaction_hash_not_null NOT NULL,
    transaction_index bigint CONSTRAINT transaction_effective_execution_iden_transaction_index_not_null NOT NULL,
    context_address bytea CONSTRAINT transaction_effective_execution_identi_context_address_not_null NOT NULL,
    execution_address bytea,
    execution_code_hash bytea,
    resolution text NOT NULL,
    evidence_source text CONSTRAINT transaction_effective_execution_identi_evidence_source_not_null NOT NULL,
    root_trace_path text,
    canonical boolean NOT NULL,
    CONSTRAINT transaction_effective_execution_ident_execution_code_hash_check CHECK (((execution_code_hash IS NULL) OR (octet_length(execution_code_hash) = 32))),
    CONSTRAINT transaction_effective_execution_identit_execution_address_check CHECK (((execution_address IS NULL) OR (octet_length(execution_address) = 20))),
    CONSTRAINT transaction_effective_execution_identit_transaction_index_check CHECK ((transaction_index >= 0)),
    CONSTRAINT transaction_effective_execution_identiti_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT transaction_effective_execution_identitie_context_address_check CHECK ((octet_length(context_address) = 20)),
    CONSTRAINT transaction_effective_execution_identitie_evidence_source_check CHECK ((evidence_source = ANY (ARRAY['prestate_tracer'::text, 'root_trace_code_observation'::text, 'unavailable'::text]))),
    CONSTRAINT transaction_effective_execution_identities_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT transaction_effective_execution_identities_block_number_check CHECK ((block_number >= (0)::numeric)),
    CONSTRAINT transaction_effective_execution_identities_check CHECK ((((evidence_source = 'root_trace_code_observation'::text) AND (root_trace_path = ''::text)) OR ((evidence_source <> 'root_trace_code_observation'::text) AND (root_trace_path IS NULL)))),
    CONSTRAINT transaction_effective_execution_identities_check1 CHECK ((((resolution = 'direct'::text) AND (execution_address = context_address) AND (execution_code_hash IS NOT NULL) AND (evidence_source = 'prestate_tracer'::text)) OR ((resolution = 'eip7702_delegate'::text) AND (execution_address IS NOT NULL) AND (execution_code_hash IS NOT NULL) AND (evidence_source = ANY (ARRAY['prestate_tracer'::text, 'root_trace_code_observation'::text]))) OR ((resolution = 'empty'::text) AND (execution_address IS NULL) AND (execution_code_hash IS NULL) AND (evidence_source = ANY (ARRAY['prestate_tracer'::text, 'root_trace_code_observation'::text]))) OR ((resolution = 'unavailable'::text) AND (execution_code_hash IS NULL) AND (evidence_source = 'unavailable'::text)))),
    CONSTRAINT transaction_effective_execution_identities_resolution_check CHECK ((resolution = ANY (ARRAY['direct'::text, 'eip7702_delegate'::text, 'empty'::text, 'unavailable'::text])))
)
PARTITION BY RANGE (block_number);

-- Table: transaction_effective_execution_identities_default

CREATE TABLE transaction_effective_execution_identities_default (
    chain_id numeric(78,0) CONSTRAINT transaction_effective_execution_identities_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT transaction_effective_execution_identitie_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT transaction_effective_execution_identities_block_hash_not_null NOT NULL,
    transaction_hash bytea CONSTRAINT transaction_effective_execution_ident_transaction_hash_not_null NOT NULL,
    transaction_index bigint CONSTRAINT transaction_effective_execution_iden_transaction_index_not_null NOT NULL,
    context_address bytea CONSTRAINT transaction_effective_execution_identi_context_address_not_null NOT NULL,
    execution_address bytea,
    execution_code_hash bytea,
    resolution text CONSTRAINT transaction_effective_execution_identities_resolution_not_null NOT NULL,
    evidence_source text CONSTRAINT transaction_effective_execution_identi_evidence_source_not_null NOT NULL,
    root_trace_path text,
    canonical boolean CONSTRAINT transaction_effective_execution_identities_canonical_not_null NOT NULL,
    CONSTRAINT transaction_effective_execution_ident_execution_code_hash_check CHECK (((execution_code_hash IS NULL) OR (octet_length(execution_code_hash) = 32))),
    CONSTRAINT transaction_effective_execution_identit_execution_address_check CHECK (((execution_address IS NULL) OR (octet_length(execution_address) = 20))),
    CONSTRAINT transaction_effective_execution_identit_transaction_index_check CHECK ((transaction_index >= 0)),
    CONSTRAINT transaction_effective_execution_identiti_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT transaction_effective_execution_identitie_context_address_check CHECK ((octet_length(context_address) = 20)),
    CONSTRAINT transaction_effective_execution_identitie_evidence_source_check CHECK ((evidence_source = ANY (ARRAY['prestate_tracer'::text, 'root_trace_code_observation'::text, 'unavailable'::text]))),
    CONSTRAINT transaction_effective_execution_identities_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT transaction_effective_execution_identities_block_number_check CHECK ((block_number >= (0)::numeric)),
    CONSTRAINT transaction_effective_execution_identities_check CHECK ((((evidence_source = 'root_trace_code_observation'::text) AND (root_trace_path = ''::text)) OR ((evidence_source <> 'root_trace_code_observation'::text) AND (root_trace_path IS NULL)))),
    CONSTRAINT transaction_effective_execution_identities_check1 CHECK ((((resolution = 'direct'::text) AND (execution_address = context_address) AND (execution_code_hash IS NOT NULL) AND (evidence_source = 'prestate_tracer'::text)) OR ((resolution = 'eip7702_delegate'::text) AND (execution_address IS NOT NULL) AND (execution_code_hash IS NOT NULL) AND (evidence_source = ANY (ARRAY['prestate_tracer'::text, 'root_trace_code_observation'::text]))) OR ((resolution = 'empty'::text) AND (execution_address IS NULL) AND (execution_code_hash IS NULL) AND (evidence_source = ANY (ARRAY['prestate_tracer'::text, 'root_trace_code_observation'::text]))) OR ((resolution = 'unavailable'::text) AND (execution_code_hash IS NULL) AND (evidence_source = 'unavailable'::text)))),
    CONSTRAINT transaction_effective_execution_identities_resolution_check CHECK ((resolution = ANY (ARRAY['direct'::text, 'eip7702_delegate'::text, 'empty'::text, 'unavailable'::text])))
);

-- Table: transaction_effective_execution_identities_p_0_1000000

CREATE TABLE transaction_effective_execution_identities_p_0_1000000 (
    chain_id numeric(78,0) CONSTRAINT transaction_effective_execution_identities_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT transaction_effective_execution_identitie_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT transaction_effective_execution_identities_block_hash_not_null NOT NULL,
    transaction_hash bytea CONSTRAINT transaction_effective_execution_ident_transaction_hash_not_null NOT NULL,
    transaction_index bigint CONSTRAINT transaction_effective_execution_iden_transaction_index_not_null NOT NULL,
    context_address bytea CONSTRAINT transaction_effective_execution_identi_context_address_not_null NOT NULL,
    execution_address bytea,
    execution_code_hash bytea,
    resolution text CONSTRAINT transaction_effective_execution_identities_resolution_not_null NOT NULL,
    evidence_source text CONSTRAINT transaction_effective_execution_identi_evidence_source_not_null NOT NULL,
    root_trace_path text,
    canonical boolean CONSTRAINT transaction_effective_execution_identities_canonical_not_null NOT NULL,
    CONSTRAINT transaction_effective_execution_ident_execution_code_hash_check CHECK (((execution_code_hash IS NULL) OR (octet_length(execution_code_hash) = 32))),
    CONSTRAINT transaction_effective_execution_identit_execution_address_check CHECK (((execution_address IS NULL) OR (octet_length(execution_address) = 20))),
    CONSTRAINT transaction_effective_execution_identit_transaction_index_check CHECK ((transaction_index >= 0)),
    CONSTRAINT transaction_effective_execution_identiti_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT transaction_effective_execution_identitie_context_address_check CHECK ((octet_length(context_address) = 20)),
    CONSTRAINT transaction_effective_execution_identitie_evidence_source_check CHECK ((evidence_source = ANY (ARRAY['prestate_tracer'::text, 'root_trace_code_observation'::text, 'unavailable'::text]))),
    CONSTRAINT transaction_effective_execution_identities_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT transaction_effective_execution_identities_block_number_check CHECK ((block_number >= (0)::numeric)),
    CONSTRAINT transaction_effective_execution_identities_check CHECK ((((evidence_source = 'root_trace_code_observation'::text) AND (root_trace_path = ''::text)) OR ((evidence_source <> 'root_trace_code_observation'::text) AND (root_trace_path IS NULL)))),
    CONSTRAINT transaction_effective_execution_identities_check1 CHECK ((((resolution = 'direct'::text) AND (execution_address = context_address) AND (execution_code_hash IS NOT NULL) AND (evidence_source = 'prestate_tracer'::text)) OR ((resolution = 'eip7702_delegate'::text) AND (execution_address IS NOT NULL) AND (execution_code_hash IS NOT NULL) AND (evidence_source = ANY (ARRAY['prestate_tracer'::text, 'root_trace_code_observation'::text]))) OR ((resolution = 'empty'::text) AND (execution_address IS NULL) AND (execution_code_hash IS NULL) AND (evidence_source = ANY (ARRAY['prestate_tracer'::text, 'root_trace_code_observation'::text]))) OR ((resolution = 'unavailable'::text) AND (execution_code_hash IS NULL) AND (evidence_source = 'unavailable'::text)))),
    CONSTRAINT transaction_effective_execution_identities_resolution_check CHECK ((resolution = ANY (ARRAY['direct'::text, 'eip7702_delegate'::text, 'empty'::text, 'unavailable'::text])))
);

-- Table: transaction_execution_code_resolutions

CREATE TABLE transaction_execution_code_resolutions (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    transaction_hash bytea CONSTRAINT transaction_execution_code_resolution_transaction_hash_not_null NOT NULL,
    transaction_index bigint CONSTRAINT transaction_execution_code_resolutio_transaction_index_not_null NOT NULL,
    context_address bytea NOT NULL,
    execution_address bytea,
    execution_code_hash bytea,
    resolution text NOT NULL,
    evidence_source text NOT NULL,
    canonical boolean NOT NULL,
    CONSTRAINT transaction_execution_code_resolution_execution_code_hash_check CHECK (((execution_code_hash IS NULL) OR (octet_length(execution_code_hash) = 32))),
    CONSTRAINT transaction_execution_code_resolutions_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT transaction_execution_code_resolutions_block_number_check CHECK ((block_number >= (0)::numeric)),
    CONSTRAINT transaction_execution_code_resolutions_check CHECK ((((resolution = 'direct'::text) AND (execution_address = context_address) AND (execution_code_hash IS NOT NULL) AND (evidence_source = 'prestate_tracer'::text)) OR ((resolution = 'eip7702_delegate'::text) AND (execution_address IS NOT NULL) AND (execution_code_hash IS NOT NULL) AND (evidence_source = 'prestate_tracer'::text)) OR ((resolution = 'empty'::text) AND (execution_address IS NULL) AND (execution_code_hash IS NULL) AND (evidence_source = 'prestate_tracer'::text)) OR ((resolution = 'unavailable'::text) AND (execution_code_hash IS NULL) AND (evidence_source = 'unavailable'::text)))),
    CONSTRAINT transaction_execution_code_resolutions_context_address_check CHECK ((octet_length(context_address) = 20)),
    CONSTRAINT transaction_execution_code_resolutions_evidence_source_check CHECK ((evidence_source = ANY (ARRAY['prestate_tracer'::text, 'unavailable'::text]))),
    CONSTRAINT transaction_execution_code_resolutions_execution_address_check CHECK (((execution_address IS NULL) OR (octet_length(execution_address) = 20))),
    CONSTRAINT transaction_execution_code_resolutions_resolution_check CHECK ((resolution = ANY (ARRAY['direct'::text, 'eip7702_delegate'::text, 'empty'::text, 'unavailable'::text]))),
    CONSTRAINT transaction_execution_code_resolutions_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT transaction_execution_code_resolutions_transaction_index_check CHECK ((transaction_index >= 0))
)
PARTITION BY RANGE (block_number);

-- Table: transaction_execution_code_resolutions_default

CREATE TABLE transaction_execution_code_resolutions_default (
    chain_id numeric(78,0) CONSTRAINT transaction_execution_code_resolutions_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT transaction_execution_code_resolutions_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT transaction_execution_code_resolutions_block_hash_not_null NOT NULL,
    transaction_hash bytea CONSTRAINT transaction_execution_code_resolution_transaction_hash_not_null NOT NULL,
    transaction_index bigint CONSTRAINT transaction_execution_code_resolutio_transaction_index_not_null NOT NULL,
    context_address bytea CONSTRAINT transaction_execution_code_resolutions_context_address_not_null NOT NULL,
    execution_address bytea,
    execution_code_hash bytea,
    resolution text CONSTRAINT transaction_execution_code_resolutions_resolution_not_null NOT NULL,
    evidence_source text CONSTRAINT transaction_execution_code_resolutions_evidence_source_not_null NOT NULL,
    canonical boolean CONSTRAINT transaction_execution_code_resolutions_canonical_not_null NOT NULL,
    CONSTRAINT transaction_execution_code_resolution_execution_code_hash_check CHECK (((execution_code_hash IS NULL) OR (octet_length(execution_code_hash) = 32))),
    CONSTRAINT transaction_execution_code_resolutions_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT transaction_execution_code_resolutions_block_number_check CHECK ((block_number >= (0)::numeric)),
    CONSTRAINT transaction_execution_code_resolutions_check CHECK ((((resolution = 'direct'::text) AND (execution_address = context_address) AND (execution_code_hash IS NOT NULL) AND (evidence_source = 'prestate_tracer'::text)) OR ((resolution = 'eip7702_delegate'::text) AND (execution_address IS NOT NULL) AND (execution_code_hash IS NOT NULL) AND (evidence_source = 'prestate_tracer'::text)) OR ((resolution = 'empty'::text) AND (execution_address IS NULL) AND (execution_code_hash IS NULL) AND (evidence_source = 'prestate_tracer'::text)) OR ((resolution = 'unavailable'::text) AND (execution_code_hash IS NULL) AND (evidence_source = 'unavailable'::text)))),
    CONSTRAINT transaction_execution_code_resolutions_context_address_check CHECK ((octet_length(context_address) = 20)),
    CONSTRAINT transaction_execution_code_resolutions_evidence_source_check CHECK ((evidence_source = ANY (ARRAY['prestate_tracer'::text, 'unavailable'::text]))),
    CONSTRAINT transaction_execution_code_resolutions_execution_address_check CHECK (((execution_address IS NULL) OR (octet_length(execution_address) = 20))),
    CONSTRAINT transaction_execution_code_resolutions_resolution_check CHECK ((resolution = ANY (ARRAY['direct'::text, 'eip7702_delegate'::text, 'empty'::text, 'unavailable'::text]))),
    CONSTRAINT transaction_execution_code_resolutions_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT transaction_execution_code_resolutions_transaction_index_check CHECK ((transaction_index >= 0))
);

-- Table: transaction_execution_code_resolutions_p_0_1000000

CREATE TABLE transaction_execution_code_resolutions_p_0_1000000 (
    chain_id numeric(78,0) CONSTRAINT transaction_execution_code_resolutions_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT transaction_execution_code_resolutions_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT transaction_execution_code_resolutions_block_hash_not_null NOT NULL,
    transaction_hash bytea CONSTRAINT transaction_execution_code_resolution_transaction_hash_not_null NOT NULL,
    transaction_index bigint CONSTRAINT transaction_execution_code_resolutio_transaction_index_not_null NOT NULL,
    context_address bytea CONSTRAINT transaction_execution_code_resolutions_context_address_not_null NOT NULL,
    execution_address bytea,
    execution_code_hash bytea,
    resolution text CONSTRAINT transaction_execution_code_resolutions_resolution_not_null NOT NULL,
    evidence_source text CONSTRAINT transaction_execution_code_resolutions_evidence_source_not_null NOT NULL,
    canonical boolean CONSTRAINT transaction_execution_code_resolutions_canonical_not_null NOT NULL,
    CONSTRAINT transaction_execution_code_resolution_execution_code_hash_check CHECK (((execution_code_hash IS NULL) OR (octet_length(execution_code_hash) = 32))),
    CONSTRAINT transaction_execution_code_resolutions_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT transaction_execution_code_resolutions_block_number_check CHECK ((block_number >= (0)::numeric)),
    CONSTRAINT transaction_execution_code_resolutions_check CHECK ((((resolution = 'direct'::text) AND (execution_address = context_address) AND (execution_code_hash IS NOT NULL) AND (evidence_source = 'prestate_tracer'::text)) OR ((resolution = 'eip7702_delegate'::text) AND (execution_address IS NOT NULL) AND (execution_code_hash IS NOT NULL) AND (evidence_source = 'prestate_tracer'::text)) OR ((resolution = 'empty'::text) AND (execution_address IS NULL) AND (execution_code_hash IS NULL) AND (evidence_source = 'prestate_tracer'::text)) OR ((resolution = 'unavailable'::text) AND (execution_code_hash IS NULL) AND (evidence_source = 'unavailable'::text)))),
    CONSTRAINT transaction_execution_code_resolutions_context_address_check CHECK ((octet_length(context_address) = 20)),
    CONSTRAINT transaction_execution_code_resolutions_evidence_source_check CHECK ((evidence_source = ANY (ARRAY['prestate_tracer'::text, 'unavailable'::text]))),
    CONSTRAINT transaction_execution_code_resolutions_execution_address_check CHECK (((execution_address IS NULL) OR (octet_length(execution_address) = 20))),
    CONSTRAINT transaction_execution_code_resolutions_resolution_check CHECK ((resolution = ANY (ARRAY['direct'::text, 'eip7702_delegate'::text, 'empty'::text, 'unavailable'::text]))),
    CONSTRAINT transaction_execution_code_resolutions_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT transaction_execution_code_resolutions_transaction_index_check CHECK ((transaction_index >= 0))
);

-- Table: transaction_inclusions

CREATE TABLE transaction_inclusions (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    tx_index bigint NOT NULL,
    tx_hash bytea NOT NULL,
    raw jsonb NOT NULL,
    CONSTRAINT transaction_inclusions_tx_index_check CHECK ((tx_index >= 0))
)
PARTITION BY RANGE (block_number);

-- Table: transaction_inclusions_default

CREATE TABLE transaction_inclusions_default (
    chain_id numeric(78,0) CONSTRAINT transaction_inclusions_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT transaction_inclusions_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT transaction_inclusions_block_hash_not_null NOT NULL,
    tx_index bigint CONSTRAINT transaction_inclusions_tx_index_not_null NOT NULL,
    tx_hash bytea CONSTRAINT transaction_inclusions_tx_hash_not_null NOT NULL,
    raw jsonb CONSTRAINT transaction_inclusions_raw_not_null NOT NULL,
    CONSTRAINT transaction_inclusions_tx_index_check CHECK ((tx_index >= 0))
);

-- Table: transaction_inclusions_p_0_1000000

CREATE TABLE transaction_inclusions_p_0_1000000 (
    chain_id numeric(78,0) CONSTRAINT transaction_inclusions_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT transaction_inclusions_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT transaction_inclusions_block_hash_not_null NOT NULL,
    tx_index bigint CONSTRAINT transaction_inclusions_tx_index_not_null NOT NULL,
    tx_hash bytea CONSTRAINT transaction_inclusions_tx_hash_not_null NOT NULL,
    raw jsonb CONSTRAINT transaction_inclusions_raw_not_null NOT NULL,
    CONSTRAINT transaction_inclusions_tx_index_check CHECK ((tx_index >= 0))
);

-- Table: transaction_state_changes

CREATE TABLE transaction_state_changes (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    transaction_hash bytea NOT NULL,
    transaction_index bigint NOT NULL,
    address bytea NOT NULL,
    field_kind text NOT NULL,
    storage_key bytea DEFAULT '\x'::bytea NOT NULL,
    before_value text,
    after_value text,
    canonical boolean NOT NULL,
    CONSTRAINT transaction_state_changes_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT transaction_state_changes_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT transaction_state_changes_check CHECK ((((field_kind = 'storage'::text) AND (octet_length(storage_key) = 32)) OR ((field_kind <> 'storage'::text) AND (octet_length(storage_key) = 0)))),
    CONSTRAINT transaction_state_changes_check1 CHECK (((before_value IS NOT NULL) OR (after_value IS NOT NULL))),
    CONSTRAINT transaction_state_changes_field_kind_check CHECK ((field_kind = ANY (ARRAY['balance'::text, 'nonce'::text, 'code'::text, 'storage'::text]))),
    CONSTRAINT transaction_state_changes_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32))
)
PARTITION BY RANGE (block_number);

-- Table: transaction_state_changes_default

CREATE TABLE transaction_state_changes_default (
    chain_id numeric(78,0) CONSTRAINT transaction_state_changes_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT transaction_state_changes_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT transaction_state_changes_block_hash_not_null NOT NULL,
    transaction_hash bytea CONSTRAINT transaction_state_changes_transaction_hash_not_null NOT NULL,
    transaction_index bigint CONSTRAINT transaction_state_changes_transaction_index_not_null NOT NULL,
    address bytea CONSTRAINT transaction_state_changes_address_not_null NOT NULL,
    field_kind text CONSTRAINT transaction_state_changes_field_kind_not_null NOT NULL,
    storage_key bytea DEFAULT '\x'::bytea CONSTRAINT transaction_state_changes_storage_key_not_null NOT NULL,
    before_value text,
    after_value text,
    canonical boolean CONSTRAINT transaction_state_changes_canonical_not_null NOT NULL,
    CONSTRAINT transaction_state_changes_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT transaction_state_changes_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT transaction_state_changes_check CHECK ((((field_kind = 'storage'::text) AND (octet_length(storage_key) = 32)) OR ((field_kind <> 'storage'::text) AND (octet_length(storage_key) = 0)))),
    CONSTRAINT transaction_state_changes_check1 CHECK (((before_value IS NOT NULL) OR (after_value IS NOT NULL))),
    CONSTRAINT transaction_state_changes_field_kind_check CHECK ((field_kind = ANY (ARRAY['balance'::text, 'nonce'::text, 'code'::text, 'storage'::text]))),
    CONSTRAINT transaction_state_changes_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32))
);

-- Table: transaction_state_changes_p_0_1000000

CREATE TABLE transaction_state_changes_p_0_1000000 (
    chain_id numeric(78,0) CONSTRAINT transaction_state_changes_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT transaction_state_changes_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT transaction_state_changes_block_hash_not_null NOT NULL,
    transaction_hash bytea CONSTRAINT transaction_state_changes_transaction_hash_not_null NOT NULL,
    transaction_index bigint CONSTRAINT transaction_state_changes_transaction_index_not_null NOT NULL,
    address bytea CONSTRAINT transaction_state_changes_address_not_null NOT NULL,
    field_kind text CONSTRAINT transaction_state_changes_field_kind_not_null NOT NULL,
    storage_key bytea DEFAULT '\x'::bytea CONSTRAINT transaction_state_changes_storage_key_not_null NOT NULL,
    before_value text,
    after_value text,
    canonical boolean CONSTRAINT transaction_state_changes_canonical_not_null NOT NULL,
    CONSTRAINT transaction_state_changes_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT transaction_state_changes_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT transaction_state_changes_check CHECK ((((field_kind = 'storage'::text) AND (octet_length(storage_key) = 32)) OR ((field_kind <> 'storage'::text) AND (octet_length(storage_key) = 0)))),
    CONSTRAINT transaction_state_changes_check1 CHECK (((before_value IS NOT NULL) OR (after_value IS NOT NULL))),
    CONSTRAINT transaction_state_changes_field_kind_check CHECK ((field_kind = ANY (ARRAY['balance'::text, 'nonce'::text, 'code'::text, 'storage'::text]))),
    CONSTRAINT transaction_state_changes_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32))
);

-- Sequence: transactional_outbox_id_seq

ALTER TABLE transactional_outbox ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME transactional_outbox_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

-- Table: transactions

CREATE TABLE transactions (
    chain_id numeric(78,0) NOT NULL,
    hash bytea NOT NULL,
    tx_type numeric(78,0),
    raw jsonb NOT NULL,
    inserted_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT transactions_hash_check CHECK ((octet_length(hash) = 32))
);

-- Table: user_sessions

CREATE TABLE user_sessions (
    id uuid NOT NULL,
    user_id uuid NOT NULL,
    token_digest bytea NOT NULL,
    csrf_digest bytea NOT NULL,
    created_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    last_used_at timestamp with time zone NOT NULL,
    revoked_at timestamp with time zone,
    CONSTRAINT user_sessions_check CHECK ((expires_at > created_at)),
    CONSTRAINT user_sessions_check1 CHECK ((last_used_at >= created_at)),
    CONSTRAINT user_sessions_check2 CHECK (((revoked_at IS NULL) OR (revoked_at >= created_at))),
    CONSTRAINT user_sessions_csrf_digest_check CHECK ((octet_length(csrf_digest) = 32)),
    CONSTRAINT user_sessions_token_digest_check CHECK ((octet_length(token_digest) = 32))
);

-- Table: users

CREATE TABLE users (
    id uuid NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    address bytea NOT NULL,
    display_name text,
    role text DEFAULT 'user'::text NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    last_login_at timestamp with time zone,
    CONSTRAINT users_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT users_display_name_check CHECK (((display_name IS NULL) OR ((char_length(display_name) BETWEEN 1 AND 64) AND (octet_length(display_name) <= 256) AND (display_name !~ '[[:cntrl:]]'::text)))),
    CONSTRAINT users_role_check CHECK ((role = ANY (ARRAY['user'::text, 'admin'::text]))),
    CONSTRAINT users_status_check CHECK ((status = ANY (ARRAY['active'::text, 'disabled'::text])))
);

-- Table: uups_implementation_observation_generations

CREATE TABLE uups_implementation_observation_generations (
    id bigint NOT NULL,
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
    CONSTRAINT uups_implementation_observation_observation_stage_version_check CHECK ((observation_stage_version = 2))
);

-- Sequence: uups_implementation_observation_generations_id_seq

CREATE SEQUENCE uups_implementation_observation_generations_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

-- Sequence Owned By: uups_implementation_observation_generations_id_seq

ALTER SEQUENCE uups_implementation_observation_generations_id_seq OWNED BY uups_implementation_observation_generations.id;

-- Table: uups_implementation_observations

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
    CONSTRAINT uups_implementation_observations_standard_version_check CHECK ((standard_version = '5.6.1'::text))
);

-- Table: verification_compilation_contracts

CREATE TABLE verification_compilation_contracts (
    compilation_id uuid NOT NULL,
    file_name text NOT NULL,
    contract_name text NOT NULL,
    abi jsonb NOT NULL,
    creation_bytecode bytea NOT NULL,
    runtime_bytecode bytea NOT NULL,
    compilation_artifacts jsonb CONSTRAINT verification_compilation_contrac_compilation_artifacts_not_null NOT NULL,
    creation_code_artifacts jsonb CONSTRAINT verification_compilation_contr_creation_code_artifacts_not_null NOT NULL,
    runtime_code_artifacts jsonb CONSTRAINT verification_compilation_contra_runtime_code_artifacts_not_null NOT NULL,
    CONSTRAINT verification_compilation_contracts_shape_check CHECK (((length(file_name) BETWEEN 1 AND 384) AND ((length(contract_name) >= 1) AND (length(contract_name) <= 256)) AND (jsonb_typeof(abi) = 'array'::text) AND ((octet_length(creation_bytecode) >= 1) AND (octet_length(creation_bytecode) <= 67108864)) AND ((octet_length(runtime_bytecode) >= 1) AND (octet_length(runtime_bytecode) <= 67108864)) AND (jsonb_typeof(compilation_artifacts) = 'object'::text) AND (jsonb_typeof(creation_code_artifacts) = 'object'::text) AND (jsonb_typeof(runtime_code_artifacts) = 'object'::text)))
);

-- Table: verification_compilation_units

CREATE TABLE verification_compilation_units (
    id uuid NOT NULL,
    source_job_id uuid NOT NULL,
    request_digest bytea NOT NULL,
    language text NOT NULL,
    compiler_version text NOT NULL,
    compiler_platform text NOT NULL,
    catalog_generation_id bigint NOT NULL,
    compiler_sha256 bytea NOT NULL,
    executor_kind text NOT NULL,
    execution_policy text NOT NULL,
    executor_sha256 bytea NOT NULL,
    standard_json jsonb NOT NULL,
    standard_json_payload bytea NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT verification_compilation_units_shape_check CHECK (((octet_length(request_digest) = 32) AND (language = 'solidity'::text) AND ((length(compiler_version) >= 1) AND (length(compiler_version) <= 128)) AND ((length(compiler_platform) >= 1) AND (length(compiler_platform) <= 64)) AND (catalog_generation_id > 0) AND (octet_length(compiler_sha256) = 32) AND ((length(executor_kind) >= 1) AND (length(executor_kind) <= 64)) AND ((length(execution_policy) >= 1) AND (length(execution_policy) <= 64)) AND (octet_length(executor_sha256) = 32) AND (jsonb_typeof(standard_json) = 'object'::text) AND ((octet_length(standard_json_payload) >= 2) AND (octet_length(standard_json_payload) <= 67108864)) AND (standard_json = (convert_from(standard_json_payload, 'UTF8'::name))::jsonb)))
);

-- Table: verification_jobs

CREATE TABLE verification_jobs (
    id uuid NOT NULL,
    kind text NOT NULL,
    language text,
    catalog_language text,
    compiler_version text,
    compiler_platform text,
    catalog_generation_id bigint,
    compiler_digest bytea,
    executor_digest bytea,
    chain_id numeric(78,0),
    address bytea,
    code_hash bytea,
    block_hash bytea,
    request jsonb NOT NULL,
    request_payload bytea NOT NULL,
    request_digest bytea NOT NULL,
    status text DEFAULT 'queued'::text NOT NULL,
    leased_by text,
    lease_token text,
    lease_expires_at timestamp with time zone,
    attempt_count integer DEFAULT 0 NOT NULL,
    max_attempts integer DEFAULT 3 NOT NULL,
    outcome_kind text,
    outcome jsonb,
    error_code text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    executor_kind text,
    execution_policy text,
    CONSTRAINT verification_jobs_attempt_check CHECK (((attempt_count >= 0) AND ((max_attempts >= 1) AND (max_attempts <= 100)) AND (attempt_count <= max_attempts))),
    CONSTRAINT verification_jobs_catalog_language_check CHECK (((catalog_language IS NULL) OR (catalog_language = ANY (ARRAY['solidity'::text, 'vyper'::text])))),
    CONSTRAINT verification_jobs_compiler_check CHECK ((((kind = ANY (ARRAY['sourcify'::text, 'sourcify_from_etherscan'::text, 'proxy'::text])) AND (language IS NULL) AND (compiler_version IS NULL) AND (compiler_platform IS NULL) AND (catalog_language IS NULL) AND (catalog_generation_id IS NULL) AND (compiler_digest IS NULL) AND (executor_kind IS NULL) AND (execution_policy IS NULL) AND (executor_digest IS NULL)) OR ((kind <> ALL (ARRAY['sourcify'::text, 'sourcify_from_etherscan'::text, 'proxy'::text])) AND (language = ANY (ARRAY['solidity'::text, 'yul'::text])) AND ((length(compiler_version) >= 1) AND (length(compiler_version) <= 128)) AND (catalog_language = 'solidity'::text) AND (((compiler_platform IS NULL) AND (catalog_generation_id IS NULL) AND (compiler_digest IS NULL) AND (executor_kind IS NULL) AND (execution_policy IS NULL) AND (executor_digest IS NULL) AND (status <> 'succeeded'::text)) OR ((compiler_platform = ANY (ARRAY['bin'::text, 'emscripten-asmjs'::text, 'emscripten-wasm32'::text, 'linux-amd64'::text, 'linux-arm64'::text, 'macosx-amd64'::text, 'wasm'::text, 'windows-amd64'::text])) AND (catalog_generation_id IS NOT NULL) AND (octet_length(compiler_digest) = 32) AND (((executor_kind = 'node_solcjs_v1'::text) AND (execution_policy = 'trusted_subprocess'::text) AND (octet_length(executor_digest) = 32) AND (compiler_platform = 'emscripten-wasm32'::text)) OR ((executor_kind = 'legacy_runner'::text) AND (execution_policy = 'legacy_hard_isolation'::text) AND (octet_length(executor_digest) = 32)) OR ((executor_kind = 'legacy_process'::text) AND (execution_policy = 'legacy_trusted_process'::text) AND (executor_digest IS NULL)))))) OR ((((kind = 'address'::text) AND (language = 'geas'::text) AND (compiler_version = '0.3.3'::text)) OR ((kind = ANY (ARRAY['address'::text, 'vyper_standard_json'::text, 'vyper_multipart'::text])) AND (language = 'vyper'::text) AND (compiler_version = '0.4.3'::text) AND (status = ANY (ARRAY['succeeded'::text, 'failed'::text, 'cancelled'::text])))) AND (catalog_language IS NULL) AND (((compiler_platform IS NULL) AND (catalog_generation_id IS NULL) AND (compiler_digest IS NULL) AND (executor_kind IS NULL) AND (execution_policy IS NULL) AND (executor_digest IS NULL) AND (status <> 'succeeded'::text)) OR ((((language = 'geas'::text) AND (compiler_platform = 'go-module'::text) AND (executor_kind = 'etherview_geas_v1'::text)) OR ((language = 'vyper'::text) AND (compiler_platform = 'python-wheel'::text) AND (executor_kind = 'etherview_vyper_v1'::text) AND (compiler_digest = decode('3b9671727c888363740dc678e60336759871487d0e4e9fdd973048fa9635c4fd'::text, 'hex'::text)))) AND (catalog_generation_id IS NULL) AND (octet_length(compiler_digest) = 32) AND (execution_policy = 'trusted_subprocess'::text) AND (octet_length(executor_digest) = 32)))) OR (((kind = ANY (ARRAY['address'::text, 'vyper_standard_json'::text, 'vyper_multipart'::text])) AND (language = 'vyper'::text) AND (catalog_language = 'vyper'::text) AND (compiler_version ~ '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'::text) AND (((compiler_platform IS NULL) AND (catalog_generation_id IS NULL) AND (compiler_digest IS NULL) AND (executor_kind IS NULL) AND (execution_policy IS NULL) AND (executor_digest IS NULL) AND (status <> 'succeeded'::text)) OR ((compiler_platform = 'python-wheel'::text) AND (catalog_generation_id IS NOT NULL) AND (octet_length(compiler_digest) = 32) AND ((executor_kind = 'node_vyper_wasm_v1'::text) OR ((executor_kind = 'etherview_vyper_v3'::text) AND (status = ANY (ARRAY['succeeded'::text, 'failed'::text, 'cancelled'::text])))) AND (execution_policy = 'trusted_subprocess'::text) AND (octet_length(executor_digest) = 32)))) IS TRUE))),
    CONSTRAINT verification_jobs_kind_check CHECK ((kind = ANY (ARRAY['address'::text, 'derived'::text, 'proxy'::text, 'sourcify'::text, 'sourcify_from_etherscan'::text, 'solidity_multipart'::text, 'solidity_standard_json'::text, 'solidity_batch_multipart'::text, 'solidity_batch_standard_json'::text, 'vyper_multipart'::text, 'vyper_standard_json'::text]))),
    CONSTRAINT verification_jobs_language_check CHECK (((language IS NULL) OR (language = ANY (ARRAY['solidity'::text, 'yul'::text, 'geas'::text, 'vyper'::text])))),
    CONSTRAINT verification_jobs_lease_check CHECK ((((leased_by IS NULL) = (lease_token IS NULL)) AND ((leased_by IS NULL) = (lease_expires_at IS NULL)) AND ((status = 'running'::text) = (leased_by IS NOT NULL)))),
    CONSTRAINT verification_jobs_request_check CHECK (((jsonb_typeof(request) = 'object'::text) AND ((octet_length(request_payload) >= 2) AND (octet_length(request_payload) <= 67108864)) AND (request = (convert_from(request_payload, 'UTF8'::name))::jsonb) AND (octet_length(request_digest) = 32))),
    CONSTRAINT verification_jobs_result_size_check CHECK (((outcome IS NULL) OR (octet_length((outcome)::text) <= 268435456))),
    CONSTRAINT verification_jobs_status_check CHECK ((status = ANY (ARRAY['queued'::text, 'running'::text, 'succeeded'::text, 'failed'::text, 'cancelled'::text]))),
    CONSTRAINT verification_jobs_target_check CHECK ((((kind = ANY (ARRAY['address'::text, 'proxy'::text, 'derived'::text])) AND (chain_id IS NOT NULL) AND (octet_length(address) = 20) AND (octet_length(code_hash) = 32) AND (octet_length(block_hash) = 32)) OR ((kind <> ALL (ARRAY['address'::text, 'proxy'::text, 'derived'::text])) AND (chain_id IS NULL) AND (address IS NULL) AND (code_hash IS NULL) AND (block_hash IS NULL)))),
    CONSTRAINT verification_jobs_terminal_check CHECK ((((status = ANY (ARRAY['queued'::text, 'running'::text, 'cancelled'::text])) AND (outcome_kind IS NULL) AND (outcome IS NULL) AND (error_code IS NULL)) OR ((status = 'succeeded'::text) AND (outcome_kind = ANY (ARRAY['compilation_failure'::text, 'verification_failure'::text, 'verification_success'::text, 'batch_results'::text, 'sourcify_success'::text, 'proxy_verification_success'::text])) AND (jsonb_typeof(outcome) = 'object'::text) AND (error_code IS NULL)) OR ((status = 'failed'::text) AND (outcome_kind IS NULL) AND (outcome IS NULL) AND ((length(error_code) >= 1) AND (length(error_code) <= 64))))),
    CONSTRAINT verification_jobs_vyper_request_check CHECK ((((kind <> ALL (ARRAY['vyper_standard_json'::text, 'vyper_multipart'::text])) OR (language = 'vyper'::text)) AND ((language IS DISTINCT FROM 'vyper'::text) OR ((((request ->> 'target_file'::text) IS NOT NULL) AND ((request #>> '{standard_json,language}'::text[]) = 'Vyper'::text) AND (jsonb_typeof((request #> '{standard_json,sources}'::text[])) = 'object'::text) AND ((request #> '{standard_json,sources}'::text[]) ? (request ->> 'target_file'::text))) IS TRUE))))
);

-- Table: verification_results

CREATE TABLE verification_results (
    job_id uuid NOT NULL,
    request_digest bytea NOT NULL,
    outcome_kind text NOT NULL,
    outcome jsonb NOT NULL,
    file_name text,
    contract_name text,
    language text,
    compiler_version text,
    match_type text,
    abi jsonb,
    sources jsonb,
    settings jsonb,
    compilation_artifacts jsonb,
    creation_code_artifacts jsonb,
    runtime_code_artifacts jsonb,
    constructor_arguments bytea,
    libraries jsonb,
    is_blueprint boolean,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    proxy_artifact_kind text,
    proxy_standard_version text,
    proxy_runtime_immutable_address bytea,
    proxy_source_manifest_sha256 bytea,
    CONSTRAINT verification_results_digest_check CHECK ((octet_length(request_digest) = 32)),
    CONSTRAINT verification_results_outcome_check CHECK (((outcome_kind = ANY (ARRAY['compilation_failure'::text, 'verification_failure'::text, 'verification_success'::text, 'batch_results'::text, 'sourcify_success'::text, 'proxy_verification_success'::text])) AND (jsonb_typeof(outcome) = 'object'::text) AND (octet_length((outcome)::text) <= 268435456))),
    CONSTRAINT verification_results_proxy_attestation_shape CHECK ((((proxy_artifact_kind IS NULL) AND (proxy_standard_version IS NULL) AND (proxy_runtime_immutable_address IS NULL) AND (proxy_source_manifest_sha256 IS NULL)) OR ((outcome_kind = 'verification_success'::text) AND (proxy_artifact_kind IS NOT NULL) AND (proxy_artifact_kind = ANY (ARRAY['erc1967_proxy'::text, 'transparent_proxy'::text, 'beacon_proxy'::text, 'uups_implementation'::text, 'proxy_admin'::text, 'upgradeable_beacon'::text])) AND (proxy_standard_version = '5.6.1'::text) AND (proxy_source_manifest_sha256 IS NOT NULL) AND (octet_length(proxy_source_manifest_sha256) = 32) AND (proxy_source_manifest_sha256 <> decode(repeat('00'::text, 32), 'hex'::text)) AND (((proxy_artifact_kind = ANY (ARRAY['transparent_proxy'::text, 'beacon_proxy'::text, 'uups_implementation'::text])) AND (proxy_runtime_immutable_address IS NOT NULL) AND (octet_length(proxy_runtime_immutable_address) = 20) AND (proxy_runtime_immutable_address <> decode(repeat('00'::text, 20), 'hex'::text))) OR ((proxy_artifact_kind <> ALL (ARRAY['transparent_proxy'::text, 'beacon_proxy'::text, 'uups_implementation'::text])) AND (proxy_runtime_immutable_address IS NULL)))))),
    CONSTRAINT verification_results_success_shape_check CHECK ((((outcome_kind <> 'verification_success'::text) AND (file_name IS NULL) AND (contract_name IS NULL) AND (language IS NULL) AND (compiler_version IS NULL) AND (match_type IS NULL) AND (abi IS NULL) AND (sources IS NULL) AND (settings IS NULL) AND (compilation_artifacts IS NULL) AND (creation_code_artifacts IS NULL) AND (runtime_code_artifacts IS NULL) AND (constructor_arguments IS NULL) AND (libraries IS NULL) AND (is_blueprint IS NULL)) OR ((outcome_kind = 'verification_success'::text) AND (length(file_name) BETWEEN 1 AND 512) AND ((length(contract_name) >= 1) AND (length(contract_name) <= 256)) AND (language = ANY (ARRAY['solidity'::text, 'yul'::text, 'geas'::text, 'vyper'::text])) AND ((length(compiler_version) >= 1) AND (length(compiler_version) <= 128)) AND (match_type = ANY (ARRAY['full'::text, 'partial'::text])) AND ((abi IS NULL) OR (jsonb_typeof(abi) = 'array'::text)) AND (jsonb_typeof(sources) = 'object'::text) AND (jsonb_typeof(settings) = 'object'::text) AND (jsonb_typeof(compilation_artifacts) = 'object'::text) AND (jsonb_typeof(creation_code_artifacts) = 'object'::text) AND (jsonb_typeof(runtime_code_artifacts) = 'object'::text) AND (jsonb_typeof(libraries) = 'object'::text) AND (is_blueprint IS NOT NULL) AND ((language <> 'geas'::text) OR (((compiler_version = '0.3.3'::text) AND (match_type = 'full'::text) AND (abi = '[]'::jsonb) AND (compilation_artifacts = '{}'::jsonb) AND (creation_code_artifacts = '{}'::jsonb) AND (runtime_code_artifacts = '{}'::jsonb) AND (libraries = '{}'::jsonb) AND (constructor_arguments IS NULL) AND (is_blueprint = false) AND (jsonb_typeof((settings -> 'runtime_entrypoint'::text)) = 'string'::text) AND ((settings -> 'stack_check'::text) = 'true'::jsonb) AND ((settings - ARRAY['runtime_entrypoint'::text, 'creation_entrypoint'::text, 'stack_check'::text]) = '{}'::jsonb) AND valid_geas_verification_sources(sources) AND ((outcome #>> '{runtime_match,match_type}'::text[]) = 'full'::text) AND ((outcome #> '{runtime_match,transformations}'::text[]) = '[]'::jsonb) AND ((outcome #> '{runtime_match,values}'::text[]) = '{}'::jsonb) AND (((jsonb_typeof((settings -> 'creation_entrypoint'::text)) = 'string'::text) AND ((outcome #>> '{creation_match,match_type}'::text[]) = 'full'::text) AND ((outcome #> '{creation_match,transformations}'::text[]) = '[]'::jsonb) AND ((outcome #> '{creation_match,values}'::text[]) = '{}'::jsonb)) OR ((NOT (settings ? 'creation_entrypoint'::text)) AND ((outcome -> 'creation_match'::text) = 'null'::jsonb)))) IS TRUE)))))
);

-- Table: verified_contract_proxy_artifacts

CREATE TABLE verified_contract_proxy_artifacts (
    chain_id numeric(78,0) NOT NULL,
    address bytea NOT NULL,
    code_hash bytea NOT NULL,
    valid_from_block numeric(78,0) NOT NULL,
    verification_job_id uuid NOT NULL,
    request_digest bytea NOT NULL,
    artifact_kind text NOT NULL,
    standard_version text NOT NULL,
    runtime_immutable_address bytea,
    source_manifest_sha256 bytea CONSTRAINT verified_contract_proxy_artifac_source_manifest_sha256_not_null NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT verified_contract_proxy_artifacts_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT verified_contract_proxy_artifacts_artifact_kind_check CHECK ((artifact_kind = ANY (ARRAY['erc1967_proxy'::text, 'transparent_proxy'::text, 'beacon_proxy'::text, 'uups_implementation'::text, 'proxy_admin'::text, 'upgradeable_beacon'::text]))),
    CONSTRAINT verified_contract_proxy_artifacts_check CHECK ((((artifact_kind = ANY (ARRAY['transparent_proxy'::text, 'beacon_proxy'::text, 'uups_implementation'::text])) AND (runtime_immutable_address IS NOT NULL) AND (octet_length(runtime_immutable_address) = 20) AND (runtime_immutable_address <> decode(repeat('00'::text, 20), 'hex'::text))) OR ((artifact_kind <> ALL (ARRAY['transparent_proxy'::text, 'beacon_proxy'::text, 'uups_implementation'::text])) AND (runtime_immutable_address IS NULL)))),
    CONSTRAINT verified_contract_proxy_artifacts_code_hash_check CHECK ((octet_length(code_hash) = 32)),
    CONSTRAINT verified_contract_proxy_artifacts_request_digest_check CHECK ((octet_length(request_digest) = 32)),
    CONSTRAINT verified_contract_proxy_artifacts_source_manifest_sha256_check CHECK (((octet_length(source_manifest_sha256) = 32) AND (source_manifest_sha256 <> decode(repeat('00'::text, 32), 'hex'::text)))),
    CONSTRAINT verified_contract_proxy_artifacts_standard_version_check CHECK ((standard_version = '5.6.1'::text)),
    CONSTRAINT verified_contract_proxy_artifacts_valid_from_block_check CHECK ((valid_from_block >= (0)::numeric))
);

-- Table: verified_contracts

CREATE TABLE verified_contracts (
    chain_id numeric(78,0) NOT NULL,
    address bytea NOT NULL,
    code_hash bytea NOT NULL,
    valid_from_block numeric(78,0) NOT NULL,
    valid_to_block numeric(78,0),
    verification_job_id uuid NOT NULL,
    request_digest bytea NOT NULL,
    file_name text NOT NULL,
    contract_name text NOT NULL,
    language text NOT NULL,
    compiler_version text NOT NULL,
    match_type text NOT NULL,
    abi jsonb,
    sources jsonb NOT NULL,
    settings jsonb NOT NULL,
    compilation_artifacts jsonb NOT NULL,
    creation_code_artifacts jsonb NOT NULL,
    runtime_code_artifacts jsonb NOT NULL,
    constructor_arguments bytea,
    libraries jsonb NOT NULL,
    is_blueprint boolean NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT verified_contracts_artifact_check CHECK (((length(file_name) BETWEEN 1 AND 512) AND ((length(contract_name) >= 1) AND (length(contract_name) <= 256)) AND (language = ANY (ARRAY['solidity'::text, 'yul'::text, 'geas'::text, 'vyper'::text])) AND ((length(compiler_version) >= 1) AND (length(compiler_version) <= 128)) AND (match_type = ANY (ARRAY['full'::text, 'partial'::text])) AND ((abi IS NULL) OR (jsonb_typeof(abi) = 'array'::text)) AND (jsonb_typeof(sources) = 'object'::text) AND (jsonb_typeof(settings) = 'object'::text) AND (jsonb_typeof(compilation_artifacts) = 'object'::text) AND (jsonb_typeof(creation_code_artifacts) = 'object'::text) AND (jsonb_typeof(runtime_code_artifacts) = 'object'::text) AND (jsonb_typeof(libraries) = 'object'::text) AND ((language <> 'geas'::text) OR (((compiler_version = '0.3.3'::text) AND (match_type = 'full'::text) AND (abi = '[]'::jsonb) AND (compilation_artifacts = '{}'::jsonb) AND (creation_code_artifacts = '{}'::jsonb) AND (runtime_code_artifacts = '{}'::jsonb) AND (libraries = '{}'::jsonb) AND (constructor_arguments IS NULL) AND (is_blueprint = false)) IS TRUE)))),
    CONSTRAINT verified_contracts_identity_check CHECK (((octet_length(address) = 20) AND (octet_length(code_hash) = 32) AND (valid_from_block >= (0)::numeric) AND ((valid_to_block IS NULL) OR (valid_to_block >= valid_from_block)) AND (octet_length(request_digest) = 32)))
);

-- Table: verified_function_selector_sets

CREATE TABLE verified_function_selector_sets (
    verification_job_id uuid NOT NULL,
    request_digest bytea NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    address bytea NOT NULL,
    code_hash bytea NOT NULL,
    valid_from_block numeric(78,0) NOT NULL,
    status text NOT NULL,
    function_count integer NOT NULL,
    warning text DEFAULT ''::text NOT NULL,
    indexed_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT verified_function_selector_sets_identity_check CHECK (((octet_length(request_digest) = 32) AND (octet_length(address) = 20) AND (octet_length(code_hash) = 32) AND (valid_from_block >= (0)::numeric))),
    CONSTRAINT verified_function_selector_sets_status_check CHECK (((status = ANY (ARRAY['complete'::text, 'invalid'::text])) AND (function_count >= 0) AND (((status = 'complete'::text) AND (warning = ''::text)) OR ((status = 'invalid'::text) AND (function_count = 0) AND (warning <> ''::text)))))
);

-- Table: verified_function_selectors

CREATE TABLE verified_function_selectors (
    verification_job_id uuid NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    address bytea NOT NULL,
    code_hash bytea NOT NULL,
    selector bytea NOT NULL,
    signature text NOT NULL,
    function_name text NOT NULL,
    abi_entry jsonb NOT NULL,
    CONSTRAINT verified_function_selectors_shape_check CHECK (((octet_length(address) = 20) AND (octet_length(code_hash) = 32) AND (octet_length(selector) = 4) AND ((length(signature) >= 1) AND (length(signature) <= 4096)) AND ((length(function_name) >= 1) AND (length(function_name) <= 4096)) AND (jsonb_typeof(abi_entry) = 'object'::text) AND ((abi_entry ->> 'type'::text) = 'function'::text)))
);

-- Table: verified_proxy_bindings

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
    CONSTRAINT verified_proxy_bindings_uups_generation_shape CHECK (((proxy_pattern = 'uups'::text) = (uups_generation_id IS NOT NULL)))
);

-- View: watch_activity_sources

CREATE VIEW watch_activity_sources AS
 SELECT inclusion.chain_id,
    inclusion.block_number,
    inclusion.block_hash,
    block."timestamp" AS block_timestamp,
    inclusion.tx_index,
    ('tx:'::text || encode(inclusion.tx_hash, 'hex'::text)) AS source_key,
    'transaction'::text AS source_kind,
    (0)::bigint AS source_generation,
    lower((inclusion.raw ->> 'from'::text)) AS from_address,
    lower(COALESCE((inclusion.raw ->> 'to'::text), (receipt.raw ->> 'contractAddress'::text))) AS to_address,
    jsonb_build_object('block_number', (inclusion.block_number)::text, 'block_hash', ('0x'::text || encode(inclusion.block_hash, 'hex'::text)), 'timestamp', (block."timestamp")::text, 'transaction_hash', ('0x'::text || encode(inclusion.tx_hash, 'hex'::text)), 'transaction_index', (inclusion.tx_index)::text, 'kind', 'transaction', 'from', lower((inclusion.raw ->> 'from'::text)), 'to', lower(COALESCE((inclusion.raw ->> 'to'::text), (receipt.raw ->> 'contractAddress'::text))), 'value', (inclusion.raw ->> 'value'::text), 'status', (receipt.raw ->> 'status'::text)) AS activity
   FROM (((transaction_inclusions inclusion
     JOIN blocks block ON (((block.chain_id = inclusion.chain_id) AND (block.number = inclusion.block_number) AND (block.hash = inclusion.block_hash))))
     JOIN canonical_blocks canonical ON (((canonical.chain_id = inclusion.chain_id) AND (canonical.number = inclusion.block_number) AND (canonical.block_hash = inclusion.block_hash))))
     JOIN receipts receipt ON (((receipt.chain_id = inclusion.chain_id) AND (receipt.block_number = inclusion.block_number) AND (receipt.block_hash = inclusion.block_hash) AND (receipt.tx_index = inclusion.tx_index))))
UNION ALL
 SELECT event.chain_id,
    event.block_number,
    event.block_hash,
    block."timestamp" AS block_timestamp,
    inclusion.tx_index,
    ((('log:'::text || (event.log_index)::text) || ':'::text) || (event.sub_index)::text) AS source_key,
    event.standard AS source_kind,
    publication.job_generation AS source_generation,
    ('0x'::text || encode(event.from_address, 'hex'::text)) AS from_address,
    ('0x'::text || encode(event.to_address, 'hex'::text)) AS to_address,
    jsonb_build_object('block_number', (event.block_number)::text, 'block_hash', ('0x'::text || encode(event.block_hash, 'hex'::text)), 'timestamp', (block."timestamp")::text, 'transaction_hash', ('0x'::text || encode(event.transaction_hash, 'hex'::text)), 'transaction_index', (inclusion.tx_index)::text, 'kind', event.standard, 'from', ('0x'::text || encode(event.from_address, 'hex'::text)), 'to', ('0x'::text || encode(event.to_address, 'hex'::text)), 'token_address', ('0x'::text || encode(event.token_address, 'hex'::text)), 'event_kind', event.event_kind, 'log_index', (event.log_index)::text, 'sub_index', (event.sub_index)::text, 'token_id', (event.token_id)::text, 'amount', (event.amount)::text, 'decimals', (metadata.decimals)::text) AS activity
   FROM (((((token_events event
     JOIN canonical_blocks canonical ON (((canonical.chain_id = event.chain_id) AND (canonical.number = event.block_number) AND (canonical.block_hash = event.block_hash))))
     JOIN blocks block ON (((block.chain_id = event.chain_id) AND (block.number = event.block_number) AND (block.hash = event.block_hash))))
     JOIN transaction_inclusions inclusion ON (((inclusion.chain_id = event.chain_id) AND (inclusion.block_number = event.block_number) AND (inclusion.block_hash = event.block_hash) AND (inclusion.tx_hash = event.transaction_hash))))
     JOIN published_block_stage_results publication ON (((publication.chain_id = event.chain_id) AND (publication.block_hash = event.block_hash) AND (publication.stage = 'token'::text) AND (publication.stage_version = 2) AND (publication.state = 'complete'::text))))
     LEFT JOIN LATERAL ( SELECT contract.decimals
           FROM token_contracts contract
          WHERE ((contract.chain_id = event.chain_id) AND (contract.address = event.token_address) AND (contract.observed_block_number = event.block_number) AND (contract.observed_block_hash = event.block_hash) AND (contract.standard = 'erc20'::text) AND (contract.metadata_state = 'complete'::text))
          ORDER BY contract.code_hash
         LIMIT 1) metadata ON ((event.standard = 'erc20'::text)))
  WHERE (event.canonical AND (event.event_kind = ANY (ARRAY['transfer'::text, 'mint'::text, 'burn'::text])));

-- Table: watch_notification_work

CREATE TABLE watch_notification_work (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    generation bigint DEFAULT 1 NOT NULL,
    done_generation bigint DEFAULT 0 NOT NULL,
    lease_token uuid,
    lease_until timestamp with time zone,
    after_watch uuid DEFAULT '00000000-0000-0000-0000-000000000000'::uuid NOT NULL,
    after_source text DEFAULT ''::text NOT NULL
);

-- Table: watch_notifications

CREATE TABLE watch_notifications (
    id bigint NOT NULL,
    watch_id uuid NOT NULL,
    user_id uuid NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    source_key text NOT NULL,
    source_kind text NOT NULL,
    source_generation bigint NOT NULL,
    activity jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    read_at timestamp with time zone,
    CONSTRAINT watch_notifications_block_hash_check CHECK ((octet_length(block_hash) = 32))
);

-- Sequence: watch_notifications_id_seq

ALTER TABLE watch_notifications ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME watch_notifications_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

-- Table: withdrawals

CREATE TABLE withdrawals (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    withdrawal_index numeric(78,0) NOT NULL,
    validator_index numeric(78,0) NOT NULL,
    address bytea NOT NULL,
    amount numeric(78,0) NOT NULL,
    raw jsonb NOT NULL,
    CONSTRAINT withdrawals_address_check CHECK ((octet_length(address) = 20))
)
PARTITION BY RANGE (block_number);

-- Table: withdrawals_default

CREATE TABLE withdrawals_default (
    chain_id numeric(78,0) CONSTRAINT withdrawals_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT withdrawals_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT withdrawals_block_hash_not_null NOT NULL,
    withdrawal_index numeric(78,0) CONSTRAINT withdrawals_withdrawal_index_not_null NOT NULL,
    validator_index numeric(78,0) CONSTRAINT withdrawals_validator_index_not_null NOT NULL,
    address bytea CONSTRAINT withdrawals_address_not_null NOT NULL,
    amount numeric(78,0) CONSTRAINT withdrawals_amount_not_null NOT NULL,
    raw jsonb CONSTRAINT withdrawals_raw_not_null NOT NULL,
    CONSTRAINT withdrawals_address_check CHECK ((octet_length(address) = 20))
);

-- Table: withdrawals_p_0_1000000

CREATE TABLE withdrawals_p_0_1000000 (
    chain_id numeric(78,0) CONSTRAINT withdrawals_chain_id_not_null NOT NULL,
    block_number numeric(78,0) CONSTRAINT withdrawals_block_number_not_null NOT NULL,
    block_hash bytea CONSTRAINT withdrawals_block_hash_not_null NOT NULL,
    withdrawal_index numeric(78,0) CONSTRAINT withdrawals_withdrawal_index_not_null NOT NULL,
    validator_index numeric(78,0) CONSTRAINT withdrawals_validator_index_not_null NOT NULL,
    address bytea CONSTRAINT withdrawals_address_not_null NOT NULL,
    amount numeric(78,0) CONSTRAINT withdrawals_amount_not_null NOT NULL,
    raw jsonb CONSTRAINT withdrawals_raw_not_null NOT NULL,
    CONSTRAINT withdrawals_address_check CHECK ((octet_length(address) = 20))
);

-- Table Attach: abi_decodings_default

ALTER TABLE ONLY abi_decodings ATTACH PARTITION abi_decodings_default DEFAULT;

-- Table Attach: abi_decodings_p_0_1000000

ALTER TABLE ONLY abi_decodings ATTACH PARTITION abi_decodings_p_0_1000000 FOR VALUES FROM ('0') TO ('1000000');

-- Table Attach: address_activities_default

ALTER TABLE ONLY address_activities ATTACH PARTITION address_activities_default DEFAULT;

-- Table Attach: address_activities_p_0_1000000

ALTER TABLE ONLY address_activities ATTACH PARTITION address_activities_p_0_1000000 FOR VALUES FROM ('0') TO ('1000000');

-- Table Attach: eip7702_authorizations_default

ALTER TABLE ONLY eip7702_authorizations ATTACH PARTITION eip7702_authorizations_default DEFAULT;

-- Table Attach: eip7702_authorizations_p_0_1000000

ALTER TABLE ONLY eip7702_authorizations ATTACH PARTITION eip7702_authorizations_p_0_1000000 FOR VALUES FROM ('0') TO ('1000000');

-- Table Attach: erc20_holder_balances_default

ALTER TABLE ONLY erc20_holder_balances ATTACH PARTITION erc20_holder_balances_default DEFAULT;

-- Table Attach: erc20_holder_balances_p_0_1000000

ALTER TABLE ONLY erc20_holder_balances ATTACH PARTITION erc20_holder_balances_p_0_1000000 FOR VALUES FROM ('0') TO ('1000000');

-- Table Attach: erc20_holder_snapshots_default

ALTER TABLE ONLY erc20_holder_snapshots ATTACH PARTITION erc20_holder_snapshots_default DEFAULT;

-- Table Attach: erc20_holder_snapshots_p_0_1000000

ALTER TABLE ONLY erc20_holder_snapshots ATTACH PARTITION erc20_holder_snapshots_p_0_1000000 FOR VALUES FROM ('0') TO ('1000000');

-- Table Attach: erc4337_user_operation_events_default

ALTER TABLE ONLY erc4337_user_operation_events ATTACH PARTITION erc4337_user_operation_events_default DEFAULT;

-- Table Attach: erc4337_user_operation_events_p_0_1000000

ALTER TABLE ONLY erc4337_user_operation_events ATTACH PARTITION erc4337_user_operation_events_p_0_1000000 FOR VALUES FROM ('0') TO ('1000000');

-- Table Attach: erc4337_user_operation_participants_default

ALTER TABLE ONLY erc4337_user_operation_participants ATTACH PARTITION erc4337_user_operation_participants_default DEFAULT;

-- Table Attach: erc4337_user_operation_participants_p_0_1000000

ALTER TABLE ONLY erc4337_user_operation_participants ATTACH PARTITION erc4337_user_operation_participants_p_0_1000000 FOR VALUES FROM ('0') TO ('1000000');

-- Table Attach: erc4337_user_operations_default

ALTER TABLE ONLY erc4337_user_operations ATTACH PARTITION erc4337_user_operations_default DEFAULT;

-- Table Attach: erc4337_user_operations_p_0_1000000

ALTER TABLE ONLY erc4337_user_operations ATTACH PARTITION erc4337_user_operations_p_0_1000000 FOR VALUES FROM ('0') TO ('1000000');

-- Table Attach: logs_default

ALTER TABLE ONLY logs ATTACH PARTITION logs_default DEFAULT;

-- Table Attach: logs_p_0_1000000

ALTER TABLE ONLY logs ATTACH PARTITION logs_p_0_1000000 FOR VALUES FROM ('0') TO ('1000000');

-- Table Attach: native_transfers_default

ALTER TABLE ONLY native_transfers ATTACH PARTITION native_transfers_default DEFAULT;

-- Table Attach: native_transfers_p_0_1000000

ALTER TABLE ONLY native_transfers ATTACH PARTITION native_transfers_p_0_1000000 FOR VALUES FROM ('0') TO ('1000000');

-- Table Attach: normalized_traces_default

ALTER TABLE ONLY normalized_traces ATTACH PARTITION normalized_traces_default DEFAULT;

-- Table Attach: normalized_traces_p_0_1000000

ALTER TABLE ONLY normalized_traces ATTACH PARTITION normalized_traces_p_0_1000000 FOR VALUES FROM ('0') TO ('1000000');

-- Table Attach: receipts_default

ALTER TABLE ONLY receipts ATTACH PARTITION receipts_default DEFAULT;

-- Table Attach: receipts_p_0_1000000

ALTER TABLE ONLY receipts ATTACH PARTITION receipts_p_0_1000000 FOR VALUES FROM ('0') TO ('1000000');

-- Table Attach: token_balance_deltas_default

ALTER TABLE ONLY token_balance_deltas ATTACH PARTITION token_balance_deltas_default DEFAULT;

-- Table Attach: token_balance_deltas_p_0_1000000

ALTER TABLE ONLY token_balance_deltas ATTACH PARTITION token_balance_deltas_p_0_1000000 FOR VALUES FROM ('0') TO ('1000000');

-- Table Attach: token_events_default

ALTER TABLE ONLY token_events ATTACH PARTITION token_events_default DEFAULT;

-- Table Attach: token_events_p_0_1000000

ALTER TABLE ONLY token_events ATTACH PARTITION token_events_p_0_1000000 FOR VALUES FROM ('0') TO ('1000000');

-- Table Attach: trace_log_attributions_default

ALTER TABLE ONLY trace_log_attributions ATTACH PARTITION trace_log_attributions_default DEFAULT;

-- Table Attach: trace_log_attributions_p_0_1000000

ALTER TABLE ONLY trace_log_attributions ATTACH PARTITION trace_log_attributions_p_0_1000000 FOR VALUES FROM ('0') TO ('1000000');

-- Table Attach: transaction_effective_execution_identities_default

ALTER TABLE ONLY transaction_effective_execution_identities ATTACH PARTITION transaction_effective_execution_identities_default DEFAULT;

-- Table Attach: transaction_effective_execution_identities_p_0_1000000

ALTER TABLE ONLY transaction_effective_execution_identities ATTACH PARTITION transaction_effective_execution_identities_p_0_1000000 FOR VALUES FROM ('0') TO ('1000000');

-- Table Attach: transaction_execution_code_resolutions_default

ALTER TABLE ONLY transaction_execution_code_resolutions ATTACH PARTITION transaction_execution_code_resolutions_default DEFAULT;

-- Table Attach: transaction_execution_code_resolutions_p_0_1000000

ALTER TABLE ONLY transaction_execution_code_resolutions ATTACH PARTITION transaction_execution_code_resolutions_p_0_1000000 FOR VALUES FROM ('0') TO ('1000000');

-- Table Attach: transaction_inclusions_default

ALTER TABLE ONLY transaction_inclusions ATTACH PARTITION transaction_inclusions_default DEFAULT;

-- Table Attach: transaction_inclusions_p_0_1000000

ALTER TABLE ONLY transaction_inclusions ATTACH PARTITION transaction_inclusions_p_0_1000000 FOR VALUES FROM ('0') TO ('1000000');

-- Table Attach: transaction_state_changes_default

ALTER TABLE ONLY transaction_state_changes ATTACH PARTITION transaction_state_changes_default DEFAULT;

-- Table Attach: transaction_state_changes_p_0_1000000

ALTER TABLE ONLY transaction_state_changes ATTACH PARTITION transaction_state_changes_p_0_1000000 FOR VALUES FROM ('0') TO ('1000000');

-- Table Attach: withdrawals_default

ALTER TABLE ONLY withdrawals ATTACH PARTITION withdrawals_default DEFAULT;

-- Table Attach: withdrawals_p_0_1000000

ALTER TABLE ONLY withdrawals ATTACH PARTITION withdrawals_p_0_1000000 FOR VALUES FROM ('0') TO ('1000000');

-- Default: beacon_observation_generations id

ALTER TABLE ONLY beacon_observation_generations ALTER COLUMN id SET DEFAULT nextval('beacon_observation_generations_id_seq'::regclass);

-- Default: diamond_loupe_snapshots id

ALTER TABLE ONLY diamond_loupe_snapshots ALTER COLUMN id SET DEFAULT nextval('diamond_loupe_snapshots_id_seq'::regclass);

-- Default: external_adapter_observations id

ALTER TABLE ONLY external_adapter_observations ALTER COLUMN id SET DEFAULT nextval('external_adapter_observations_id_seq'::regclass);

-- Default: proxy_artifact_resolutions id

ALTER TABLE ONLY proxy_artifact_resolutions ALTER COLUMN id SET DEFAULT nextval('proxy_artifact_resolutions_id_seq'::regclass);

-- Default: proxy_detection_evidence id

ALTER TABLE ONLY proxy_detection_evidence ALTER COLUMN id SET DEFAULT nextval('proxy_detection_evidence_id_seq'::regclass);

-- Default: proxy_observation_generations id

ALTER TABLE ONLY proxy_observation_generations ALTER COLUMN id SET DEFAULT nextval('proxy_observation_generations_id_seq'::regclass);

-- Default: uups_implementation_observation_generations id

ALTER TABLE ONLY uups_implementation_observation_generations ALTER COLUMN id SET DEFAULT nextval('uups_implementation_observation_generations_id_seq'::regclass);

-- Constraint: abi_decodings abi_decodings_pkey

ALTER TABLE ONLY abi_decodings
    ADD CONSTRAINT abi_decodings_pkey PRIMARY KEY (chain_id, block_number, block_hash, object_kind, transaction_hash, object_index);

-- Constraint: abi_decodings_default abi_decodings_default_pkey

ALTER TABLE ONLY abi_decodings_default
    ADD CONSTRAINT abi_decodings_default_pkey PRIMARY KEY (chain_id, block_number, block_hash, object_kind, transaction_hash, object_index);

-- Constraint: abi_decodings_p_0_1000000 abi_decodings_p_0_1000000_pkey

ALTER TABLE ONLY abi_decodings_p_0_1000000
    ADD CONSTRAINT abi_decodings_p_0_1000000_pkey PRIMARY KEY (chain_id, block_number, block_hash, object_kind, transaction_hash, object_index);

-- Constraint: abi_signature_candidates abi_signature_candidates_pkey

ALTER TABLE ONLY abi_signature_candidates
    ADD CONSTRAINT abi_signature_candidates_pkey PRIMARY KEY (kind, identifier, signature);

-- Constraint: address_activities address_activities_pkey

ALTER TABLE ONLY address_activities
    ADD CONSTRAINT address_activities_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, activity_index, address);

-- Constraint: address_activities_default address_activities_default_pkey

ALTER TABLE ONLY address_activities_default
    ADD CONSTRAINT address_activities_default_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, activity_index, address);

-- Constraint: address_activities_p_0_1000000 address_activities_p_0_1000000_pkey

ALTER TABLE ONLY address_activities_p_0_1000000
    ADD CONSTRAINT address_activities_p_0_1000000_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, activity_index, address);

-- Constraint: address_export_admissions address_export_admissions_pkey

ALTER TABLE ONLY address_export_admissions
    ADD CONSTRAINT address_export_admissions_pkey PRIMARY KEY (token);

-- Constraint: address_watches address_watches_pkey

ALTER TABLE ONLY address_watches
    ADD CONSTRAINT address_watches_pkey PRIMARY KEY (id);

-- Constraint: api_keys api_keys_pkey

ALTER TABLE ONLY api_keys
    ADD CONSTRAINT api_keys_pkey PRIMARY KEY (prefix);

-- Constraint: auth_challenges auth_challenges_chain_id_nonce_key

ALTER TABLE ONLY auth_challenges
    ADD CONSTRAINT auth_challenges_chain_id_nonce_key UNIQUE (chain_id, nonce);

-- Constraint: auth_challenges auth_challenges_pkey

ALTER TABLE ONLY auth_challenges
    ADD CONSTRAINT auth_challenges_pkey PRIMARY KEY (id);

-- Constraint: beacon_implementation_observations beacon_implementation_observations_pkey

ALTER TABLE ONLY beacon_implementation_observations
    ADD CONSTRAINT beacon_implementation_observations_pkey PRIMARY KEY (chain_id, beacon_address, block_hash, stage_version);

-- Constraint: beacon_observation_generations beacon_observation_generation_chain_id_beacon_address_obser_key

ALTER TABLE ONLY beacon_observation_generations
    ADD CONSTRAINT beacon_observation_generation_chain_id_beacon_address_obser_key UNIQUE NULLS NOT DISTINCT (chain_id, beacon_address, observation_block_hash, observation_stage_version, durable_job_id, job_generation);

-- Constraint: beacon_observation_generations beacon_observation_generations_pkey

ALTER TABLE ONLY beacon_observation_generations
    ADD CONSTRAINT beacon_observation_generations_pkey PRIMARY KEY (id);

-- Constraint: billing_account_entries billing_account_entries_pkey

ALTER TABLE ONLY billing_account_entries
    ADD CONSTRAINT billing_account_entries_pkey PRIMARY KEY (id);

-- Constraint: billing_account_entries billing_account_entries_source_id_key

ALTER TABLE ONLY billing_account_entries
    ADD CONSTRAINT billing_account_entries_source_id_key UNIQUE (source_id);

-- Constraint: billing_accounts billing_accounts_pkey

ALTER TABLE ONLY billing_accounts
    ADD CONSTRAINT billing_accounts_pkey PRIMARY KEY (user_id, chain_id, network, asset);

-- Constraint: billing_payment_events billing_payment_events_pkey

ALTER TABLE ONLY billing_payment_events
    ADD CONSTRAINT billing_payment_events_pkey PRIMARY KEY (id);

-- Constraint: billing_payments billing_payments_fingerprint_key

ALTER TABLE ONLY billing_payments
    ADD CONSTRAINT billing_payments_fingerprint_key UNIQUE (fingerprint);

-- Constraint: billing_payments billing_payments_pkey

ALTER TABLE ONLY billing_payments
    ADD CONSTRAINT billing_payments_pkey PRIMARY KEY (id);

-- Constraint: billing_topup_intents billing_topup_intents_pkey

ALTER TABLE ONLY billing_topup_intents
    ADD CONSTRAINT billing_topup_intents_pkey PRIMARY KEY (id);

-- Constraint: billing_usage_charges billing_usage_charges_pkey

ALTER TABLE ONLY billing_usage_charges
    ADD CONSTRAINT billing_usage_charges_pkey PRIMARY KEY (id);

-- Constraint: block_journals block_journals_pkey

ALTER TABLE ONLY block_journals
    ADD CONSTRAINT block_journals_pkey PRIMARY KEY (chain_id, block_hash, stage, sequence);

-- Constraint: block_stage_results block_stage_results_pkey

ALTER TABLE ONLY block_stage_results
    ADD CONSTRAINT block_stage_results_pkey PRIMARY KEY (chain_id, block_hash, stage, stage_version);

-- Constraint: block_statistics block_statistics_pkey

ALTER TABLE ONLY block_statistics
    ADD CONSTRAINT block_statistics_pkey PRIMARY KEY (chain_id, block_number, block_hash);

-- Constraint: blocks blocks_pkey

ALTER TABLE ONLY blocks
    ADD CONSTRAINT blocks_pkey PRIMARY KEY (chain_id, number, hash);

-- Constraint: canonical_blocks canonical_blocks_pkey

ALTER TABLE ONLY canonical_blocks
    ADD CONSTRAINT canonical_blocks_pkey PRIMARY KEY (chain_id, number);

-- Constraint: chain_finality chain_finality_pkey

ALTER TABLE ONLY chain_finality
    ADD CONSTRAINT chain_finality_pkey PRIMARY KEY (chain_id);

-- Constraint: chains chains_pkey

ALTER TABLE ONLY chains
    ADD CONSTRAINT chains_pkey PRIMARY KEY (chain_id);

-- Constraint: chart_hourly_rollups chart_hourly_rollups_pkey

ALTER TABLE ONLY chart_hourly_rollups
    ADD CONSTRAINT chart_hourly_rollups_pkey PRIMARY KEY (chain_id, bucket_start);

-- Constraint: chart_rollup_backfill chart_rollup_backfill_pkey

ALTER TABLE ONLY chart_rollup_backfill
    ADD CONSTRAINT chart_rollup_backfill_pkey PRIMARY KEY (chain_id);

-- Constraint: chart_rollup_dirty_hours chart_rollup_dirty_hours_pkey

ALTER TABLE ONLY chart_rollup_dirty_hours
    ADD CONSTRAINT chart_rollup_dirty_hours_pkey PRIMARY KEY (chain_id, bucket_start);

-- Constraint: compiler_catalog_entries compiler_catalog_entries_generation_language_key

ALTER TABLE ONLY compiler_catalog_entries
    ADD CONSTRAINT compiler_catalog_entries_generation_language_key UNIQUE (generation_id, language, version, platform, artifact_sha256);

-- Constraint: compiler_catalog_entries compiler_catalog_entries_pkey

ALTER TABLE ONLY compiler_catalog_entries
    ADD CONSTRAINT compiler_catalog_entries_pkey PRIMARY KEY (generation_id, version);

-- Constraint: compiler_catalog_generations compiler_catalog_generations_id_language_key

ALTER TABLE ONLY compiler_catalog_generations
    ADD CONSTRAINT compiler_catalog_generations_id_language_key UNIQUE (id, language);

-- Constraint: compiler_catalog_generations compiler_catalog_generations_identity_key

ALTER TABLE ONLY compiler_catalog_generations
    ADD CONSTRAINT compiler_catalog_generations_identity_key UNIQUE (language, catalog_digest);

-- Constraint: compiler_catalog_generations compiler_catalog_generations_pkey

ALTER TABLE ONLY compiler_catalog_generations
    ADD CONSTRAINT compiler_catalog_generations_pkey PRIMARY KEY (id);

-- Constraint: compiler_catalog_heads compiler_catalog_heads_pkey

ALTER TABLE ONLY compiler_catalog_heads
    ADD CONSTRAINT compiler_catalog_heads_pkey PRIMARY KEY (language);

-- Constraint: contract_abis contract_abis_pkey

ALTER TABLE ONLY contract_abis
    ADD CONSTRAINT contract_abis_pkey PRIMARY KEY (chain_id, address, code_hash, source, source_address, source_code_hash, selector_scope, valid_from_block, block_hash);

-- Constraint: contract_code_observations contract_code_observations_pkey

ALTER TABLE ONLY contract_code_observations
    ADD CONSTRAINT contract_code_observations_pkey PRIMARY KEY (chain_id, address, block_hash);

-- Constraint: core_backfill_leases core_backfill_leases_lease_token_key

ALTER TABLE ONLY core_backfill_leases
    ADD CONSTRAINT core_backfill_leases_lease_token_key UNIQUE (lease_token);

-- Constraint: core_backfill_leases core_backfill_leases_pkey

ALTER TABLE ONLY core_backfill_leases
    ADD CONSTRAINT core_backfill_leases_pkey PRIMARY KEY (chain_id, range_start, range_end);

-- Constraint: core_coverage_ranges core_coverage_ranges_pkey

ALTER TABLE ONLY core_coverage_ranges
    ADD CONSTRAINT core_coverage_ranges_pkey PRIMARY KEY (chain_id, range_start);

-- Constraint: core_index_configuration core_index_configuration_pkey

ALTER TABLE ONLY core_index_configuration
    ADD CONSTRAINT core_index_configuration_pkey PRIMARY KEY (chain_id);

-- Constraint: derived_verification_attempts derived_verification_attempts_chain_id_block_hash_transacti_key

ALTER TABLE ONLY derived_verification_attempts
    ADD CONSTRAINT derived_verification_attempts_chain_id_block_hash_transacti_key UNIQUE (chain_id, block_hash, transaction_hash, trace_path, compilation_id);

-- Constraint: derived_verification_attempts derived_verification_attempts_pkey

ALTER TABLE ONLY derived_verification_attempts
    ADD CONSTRAINT derived_verification_attempts_pkey PRIMARY KEY (id);

-- Constraint: derived_verification_backfill_requests derived_verification_backfill_requests_pkey

ALTER TABLE ONLY derived_verification_backfill_requests
    ADD CONSTRAINT derived_verification_backfill_requests_pkey PRIMARY KEY (id);

-- Constraint: derived_verification_forward_blocks derived_verification_forward_blocks_pkey

ALTER TABLE ONLY derived_verification_forward_blocks
    ADD CONSTRAINT derived_verification_forward_blocks_pkey PRIMARY KEY (id);

-- Constraint: derived_verification_forward_blocks derived_verification_forward_blocks_source_unique

ALTER TABLE ONLY derived_verification_forward_blocks
    ADD CONSTRAINT derived_verification_forward_blocks_source_unique UNIQUE (source_job_id, source_generation);

-- Constraint: derived_verification_scans derived_verification_scans_pkey

ALTER TABLE ONLY derived_verification_scans
    ADD CONSTRAINT derived_verification_scans_pkey PRIMARY KEY (id);

-- Constraint: derived_verification_scans derived_verification_scans_target_key

ALTER TABLE ONLY derived_verification_scans
    ADD CONSTRAINT derived_verification_scans_target_key UNIQUE (compilation_id, creator_address, creator_code_hash, valid_from_block);

-- Constraint: diamond_cut_events diamond_cut_events_pkey

ALTER TABLE ONLY diamond_cut_events
    ADD CONSTRAINT diamond_cut_events_pkey PRIMARY KEY (chain_id, block_hash, log_index, stage_version);

-- Constraint: diamond_loupe_facets diamond_loupe_facets_pkey

ALTER TABLE ONLY diamond_loupe_facets
    ADD CONSTRAINT diamond_loupe_facets_pkey PRIMARY KEY (snapshot_id, facet_address);

-- Constraint: diamond_loupe_selectors diamond_loupe_selectors_pkey

ALTER TABLE ONLY diamond_loupe_selectors
    ADD CONSTRAINT diamond_loupe_selectors_pkey PRIMARY KEY (snapshot_id, selector);

-- Constraint: diamond_loupe_snapshots diamond_loupe_snapshots_chain_id_diamond_address_block_hash_key

ALTER TABLE ONLY diamond_loupe_snapshots
    ADD CONSTRAINT diamond_loupe_snapshots_chain_id_diamond_address_block_hash_key UNIQUE NULLS NOT DISTINCT (chain_id, diamond_address, block_hash, stage_version, durable_job_id, job_generation);

-- Constraint: diamond_loupe_snapshots diamond_loupe_snapshots_pkey

ALTER TABLE ONLY diamond_loupe_snapshots
    ADD CONSTRAINT diamond_loupe_snapshots_pkey PRIMARY KEY (id);

-- Constraint: diamond_selector_changes diamond_selector_changes_pkey

ALTER TABLE ONLY diamond_selector_changes
    ADD CONSTRAINT diamond_selector_changes_pkey PRIMARY KEY (chain_id, block_hash, log_index, stage_version, cut_index, selector_index);

-- Constraint: durable_job_replay_requests durable_job_replay_requests_job_id_requested_generation_key

ALTER TABLE ONLY durable_job_replay_requests
    ADD CONSTRAINT durable_job_replay_requests_job_id_requested_generation_key UNIQUE (job_id, requested_generation);

-- Constraint: durable_job_replay_requests durable_job_replay_requests_pkey

ALTER TABLE ONLY durable_job_replay_requests
    ADD CONSTRAINT durable_job_replay_requests_pkey PRIMARY KEY (job_id, source_kind, source_key);

-- Constraint: durable_jobs durable_jobs_chain_id_kind_idempotency_key_key

ALTER TABLE ONLY durable_jobs
    ADD CONSTRAINT durable_jobs_chain_id_kind_idempotency_key_key UNIQUE (chain_id, kind, idempotency_key);

-- Constraint: durable_jobs durable_jobs_pkey

ALTER TABLE ONLY durable_jobs
    ADD CONSTRAINT durable_jobs_pkey PRIMARY KEY (id);

-- Constraint: durable_stage_publications durable_stage_publications_pkey

ALTER TABLE ONLY durable_stage_publications
    ADD CONSTRAINT durable_stage_publications_pkey PRIMARY KEY (job_id, job_generation);

-- Constraint: eip7702_authorizations eip7702_authorizations_pkey

ALTER TABLE ONLY eip7702_authorizations
    ADD CONSTRAINT eip7702_authorizations_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, authorization_index);

-- Constraint: eip7702_authorizations_default eip7702_authorizations_default_pkey

ALTER TABLE ONLY eip7702_authorizations_default
    ADD CONSTRAINT eip7702_authorizations_default_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, authorization_index);

-- Constraint: eip7702_authorizations_p_0_1000000 eip7702_authorizations_p_0_1000000_pkey

ALTER TABLE ONLY eip7702_authorizations_p_0_1000000
    ADD CONSTRAINT eip7702_authorizations_p_0_1000000_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, authorization_index);

-- Constraint: ens_address_name_snapshots ens_address_name_snapshots_pkey

ALTER TABLE ONLY ens_address_name_snapshots
    ADD CONSTRAINT ens_address_name_snapshots_pkey PRIMARY KEY (id);

-- Constraint: ens_name_observations ens_name_observations_generation_id_source_direction_lookup_key

ALTER TABLE ONLY ens_name_observations
    ADD CONSTRAINT ens_name_observations_generation_id_source_direction_lookup_key UNIQUE (generation_id, source, direction, lookup_key);

-- Constraint: ens_name_observations ens_name_observations_pkey

ALTER TABLE ONLY ens_name_observations
    ADD CONSTRAINT ens_name_observations_pkey PRIMARY KEY (id);

-- Constraint: ens_resolution_failures ens_resolution_failures_pkey

ALTER TABLE ONLY ens_resolution_failures
    ADD CONSTRAINT ens_resolution_failures_pkey PRIMARY KEY (id);

-- Constraint: ens_resolution_generations ens_resolution_generations_pkey

ALTER TABLE ONLY ens_resolution_generations
    ADD CONSTRAINT ens_resolution_generations_pkey PRIMARY KEY (id);

-- Constraint: erc1155_balance_reconciliations erc1155_balance_reconciliations_pkey

ALTER TABLE ONLY erc1155_balance_reconciliations
    ADD CONSTRAINT erc1155_balance_reconciliations_pkey PRIMARY KEY (chain_id, token_address, token_id, owner_address, block_hash);

-- Constraint: erc20_balance_reconciliations erc20_balance_reconciliations_pkey

ALTER TABLE ONLY erc20_balance_reconciliations
    ADD CONSTRAINT erc20_balance_reconciliations_pkey PRIMARY KEY (chain_id, token_address, owner_address, block_hash);

-- Constraint: erc20_holder_balances erc20_holder_balances_pkey

ALTER TABLE ONLY erc20_holder_balances
    ADD CONSTRAINT erc20_holder_balances_pkey PRIMARY KEY (chain_id, token_address, block_number, block_hash, holder_address);

-- Constraint: erc20_holder_balances_default erc20_holder_balances_default_pkey

ALTER TABLE ONLY erc20_holder_balances_default
    ADD CONSTRAINT erc20_holder_balances_default_pkey PRIMARY KEY (chain_id, token_address, block_number, block_hash, holder_address);

-- Constraint: erc20_holder_balances_p_0_1000000 erc20_holder_balances_p_0_1000000_pkey

ALTER TABLE ONLY erc20_holder_balances_p_0_1000000
    ADD CONSTRAINT erc20_holder_balances_p_0_1000000_pkey PRIMARY KEY (chain_id, token_address, block_number, block_hash, holder_address);

-- Constraint: erc20_holder_snapshots erc20_holder_snapshots_pkey

ALTER TABLE ONLY erc20_holder_snapshots
    ADD CONSTRAINT erc20_holder_snapshots_pkey PRIMARY KEY (chain_id, token_address, block_number, block_hash);

-- Constraint: erc20_holder_snapshots_default erc20_holder_snapshots_default_pkey

ALTER TABLE ONLY erc20_holder_snapshots_default
    ADD CONSTRAINT erc20_holder_snapshots_default_pkey PRIMARY KEY (chain_id, token_address, block_number, block_hash);

-- Constraint: erc20_holder_snapshots_p_0_1000000 erc20_holder_snapshots_p_0_1000000_pkey

ALTER TABLE ONLY erc20_holder_snapshots_p_0_1000000
    ADD CONSTRAINT erc20_holder_snapshots_p_0_1000000_pkey PRIMARY KEY (chain_id, token_address, block_number, block_hash);

-- Constraint: erc4337_coverage_ranges erc4337_coverage_ranges_chain_id_configuration_digest_end_b_key

ALTER TABLE ONLY erc4337_coverage_ranges
    ADD CONSTRAINT erc4337_coverage_ranges_chain_id_configuration_digest_end_b_key UNIQUE (chain_id, configuration_digest, end_block);

-- Constraint: erc4337_coverage_ranges erc4337_coverage_ranges_pkey

ALTER TABLE ONLY erc4337_coverage_ranges
    ADD CONSTRAINT erc4337_coverage_ranges_pkey PRIMARY KEY (chain_id, configuration_digest, start_block);

-- Constraint: erc4337_covered_blocks erc4337_covered_blocks_pkey

ALTER TABLE ONLY erc4337_covered_blocks
    ADD CONSTRAINT erc4337_covered_blocks_pkey PRIMARY KEY (chain_id, configuration_digest, block_number);

-- Constraint: erc4337_user_operation_events erc4337_user_operation_events_pkey

ALTER TABLE ONLY erc4337_user_operation_events
    ADD CONSTRAINT erc4337_user_operation_events_pkey PRIMARY KEY (chain_id, configuration_digest, block_number, block_hash, transaction_hash, operation_index, log_index);

-- Constraint: erc4337_user_operation_events_default erc4337_user_operation_events_default_pkey

ALTER TABLE ONLY erc4337_user_operation_events_default
    ADD CONSTRAINT erc4337_user_operation_events_default_pkey PRIMARY KEY (chain_id, configuration_digest, block_number, block_hash, transaction_hash, operation_index, log_index);

-- Constraint: erc4337_user_operation_events_p_0_1000000 erc4337_user_operation_events_p_0_1000000_pkey

ALTER TABLE ONLY erc4337_user_operation_events_p_0_1000000
    ADD CONSTRAINT erc4337_user_operation_events_p_0_1000000_pkey PRIMARY KEY (chain_id, configuration_digest, block_number, block_hash, transaction_hash, operation_index, log_index);

-- Constraint: erc4337_user_operation_participants erc4337_user_operation_participants_pkey

ALTER TABLE ONLY erc4337_user_operation_participants
    ADD CONSTRAINT erc4337_user_operation_participants_pkey PRIMARY KEY (chain_id, configuration_digest, block_number, block_hash, transaction_hash, operation_index, address, role);

-- Constraint: erc4337_user_operation_participants_default erc4337_user_operation_participants_default_pkey

ALTER TABLE ONLY erc4337_user_operation_participants_default
    ADD CONSTRAINT erc4337_user_operation_participants_default_pkey PRIMARY KEY (chain_id, configuration_digest, block_number, block_hash, transaction_hash, operation_index, address, role);

-- Constraint: erc4337_user_operation_participants_p_0_1000000 erc4337_user_operation_participants_p_0_1000000_pkey

ALTER TABLE ONLY erc4337_user_operation_participants_p_0_1000000
    ADD CONSTRAINT erc4337_user_operation_participants_p_0_1000000_pkey PRIMARY KEY (chain_id, configuration_digest, block_number, block_hash, transaction_hash, operation_index, address, role);

-- Constraint: erc4337_user_operations erc4337_user_operations_pkey

ALTER TABLE ONLY erc4337_user_operations
    ADD CONSTRAINT erc4337_user_operations_pkey PRIMARY KEY (chain_id, configuration_digest, block_number, block_hash, transaction_hash, operation_index);

-- Constraint: erc4337_user_operations_default erc4337_user_operations_default_pkey

ALTER TABLE ONLY erc4337_user_operations_default
    ADD CONSTRAINT erc4337_user_operations_default_pkey PRIMARY KEY (chain_id, configuration_digest, block_number, block_hash, transaction_hash, operation_index);

-- Constraint: erc4337_user_operations_p_0_1000000 erc4337_user_operations_p_0_1000000_pkey

ALTER TABLE ONLY erc4337_user_operations_p_0_1000000
    ADD CONSTRAINT erc4337_user_operations_p_0_1000000_pkey PRIMARY KEY (chain_id, configuration_digest, block_number, block_hash, transaction_hash, operation_index);

-- Constraint: erc721_owner_reconciliations erc721_owner_reconciliations_pkey

ALTER TABLE ONLY erc721_owner_reconciliations
    ADD CONSTRAINT erc721_owner_reconciliations_pkey PRIMARY KEY (chain_id, token_address, token_id, block_hash);

-- Constraint: external_adapter_observations external_adapter_observations_pkey

ALTER TABLE ONLY external_adapter_observations
    ADD CONSTRAINT external_adapter_observations_pkey PRIMARY KEY (id);

-- Constraint: external_metadata_attempts external_metadata_attempts_durable_job_id_attempt_key

ALTER TABLE ONLY external_metadata_attempts
    ADD CONSTRAINT external_metadata_attempts_durable_job_id_attempt_key UNIQUE (durable_job_id, attempt);

-- Constraint: external_metadata_attempts external_metadata_attempts_pkey

ALTER TABLE ONLY external_metadata_attempts
    ADD CONSTRAINT external_metadata_attempts_pkey PRIMARY KEY (id);

-- Constraint: external_metadata external_metadata_pkey

ALTER TABLE ONLY external_metadata
    ADD CONSTRAINT external_metadata_pkey PRIMARY KEY (chain_id, resource_kind, resource_key, identity_hash);

-- Constraint: genesis_account_observations genesis_account_observations_pkey

ALTER TABLE ONLY genesis_account_observations
    ADD CONSTRAINT genesis_account_observations_pkey PRIMARY KEY (chain_id, address, block_hash);

-- Constraint: genesis_state_imports genesis_state_imports_chain_id_block_hash_key

ALTER TABLE ONLY genesis_state_imports
    ADD CONSTRAINT genesis_state_imports_chain_id_block_hash_key UNIQUE (chain_id, block_hash);

-- Constraint: genesis_state_imports genesis_state_imports_pkey

ALTER TABLE ONLY genesis_state_imports
    ADD CONSTRAINT genesis_state_imports_pkey PRIMARY KEY (chain_id);

-- Constraint: index_checkpoints index_checkpoints_pkey

ALTER TABLE ONLY index_checkpoints
    ADD CONSTRAINT index_checkpoints_pkey PRIMARY KEY (chain_id, stage);

-- Constraint: logs logs_pkey

ALTER TABLE ONLY logs
    ADD CONSTRAINT logs_pkey PRIMARY KEY (chain_id, block_number, block_hash, log_index);

-- Constraint: logs_default logs_default_pkey

ALTER TABLE ONLY logs_default
    ADD CONSTRAINT logs_default_pkey PRIMARY KEY (chain_id, block_number, block_hash, log_index);

-- Constraint: logs_p_0_1000000 logs_p_0_1000000_pkey

ALTER TABLE ONLY logs_p_0_1000000
    ADD CONSTRAINT logs_p_0_1000000_pkey PRIMARY KEY (chain_id, block_number, block_hash, log_index);

-- Constraint: mempool_snapshot_transactions mempool_snapshot_transactions_pkey

ALTER TABLE ONLY mempool_snapshot_transactions
    ADD CONSTRAINT mempool_snapshot_transactions_pkey PRIMARY KEY (chain_id, snapshot_id, tx_hash);

-- Constraint: mempool_snapshots mempool_snapshots_pkey

ALTER TABLE ONLY mempool_snapshots
    ADD CONSTRAINT mempool_snapshots_pkey PRIMARY KEY (chain_id, id);

-- Constraint: mempool_status mempool_status_pkey

ALTER TABLE ONLY mempool_status
    ADD CONSTRAINT mempool_status_pkey PRIMARY KEY (chain_id);

-- Constraint: mempool_transaction_replacements mempool_transaction_replaceme_chain_id_snapshot_id_replacem_key

ALTER TABLE ONLY mempool_transaction_replacements
    ADD CONSTRAINT mempool_transaction_replaceme_chain_id_snapshot_id_replacem_key UNIQUE (chain_id, snapshot_id, replacement_hash);

-- Constraint: mempool_transaction_replacements mempool_transaction_replacements_pkey

ALTER TABLE ONLY mempool_transaction_replacements
    ADD CONSTRAINT mempool_transaction_replacements_pkey PRIMARY KEY (chain_id, snapshot_id, replaced_hash);

-- Constraint: mempool_transactions mempool_transactions_pkey

ALTER TABLE ONLY mempool_transactions
    ADD CONSTRAINT mempool_transactions_pkey PRIMARY KEY (chain_id, tx_hash);

-- Constraint: native_transfer_coverage native_transfer_coverage_pkey

ALTER TABLE ONLY native_transfer_coverage
    ADD CONSTRAINT native_transfer_coverage_pkey PRIMARY KEY (chain_id);

-- Constraint: native_transfers native_transfers_pkey

ALTER TABLE ONLY native_transfers
    ADD CONSTRAINT native_transfers_pkey PRIMARY KEY (chain_id, block_number, block_hash, log_index);

-- Constraint: native_transfers_default native_transfers_default_pkey

ALTER TABLE ONLY native_transfers_default
    ADD CONSTRAINT native_transfers_default_pkey PRIMARY KEY (chain_id, block_number, block_hash, log_index);

-- Constraint: native_transfers_p_0_1000000 native_transfers_p_0_1000000_pkey

ALTER TABLE ONLY native_transfers_p_0_1000000
    ADD CONSTRAINT native_transfers_p_0_1000000_pkey PRIMARY KEY (chain_id, block_number, block_hash, log_index);

-- Constraint: nft_metadata_source_observations nft_metadata_source_observations_pkey

ALTER TABLE ONLY nft_metadata_source_observations
    ADD CONSTRAINT nft_metadata_source_observations_pkey PRIMARY KEY (chain_id, token_address, token_id, block_hash);

-- Constraint: nft_metadata_update_observations nft_metadata_update_observations_pkey

ALTER TABLE ONLY nft_metadata_update_observations
    ADD CONSTRAINT nft_metadata_update_observations_pkey PRIMARY KEY (chain_id, block_number, block_hash, log_index);

-- Constraint: normalized_traces normalized_traces_pkey

ALTER TABLE ONLY normalized_traces
    ADD CONSTRAINT normalized_traces_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, trace_path);

-- Constraint: normalized_traces_default normalized_traces_default_pkey

ALTER TABLE ONLY normalized_traces_default
    ADD CONSTRAINT normalized_traces_default_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, trace_path);

-- Constraint: normalized_traces_p_0_1000000 normalized_traces_p_0_1000000_pkey

ALTER TABLE ONLY normalized_traces_p_0_1000000
    ADD CONSTRAINT normalized_traces_p_0_1000000_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, trace_path);

-- Constraint: operator_labels operator_labels_chain_id_object_kind_object_key_key

ALTER TABLE ONLY operator_labels
    ADD CONSTRAINT operator_labels_chain_id_object_kind_object_key_key UNIQUE (chain_id, object_kind, object_key);

-- Constraint: operator_labels operator_labels_pkey

ALTER TABLE ONLY operator_labels
    ADD CONSTRAINT operator_labels_pkey PRIMARY KEY (id);

-- Constraint: proxy_artifact_resolutions proxy_artifact_resolutions_chain_id_proxy_address_observati_key

ALTER TABLE ONLY proxy_artifact_resolutions
    ADD CONSTRAINT proxy_artifact_resolutions_chain_id_proxy_address_observati_key UNIQUE NULLS NOT DISTINCT (chain_id, proxy_address, observation_block_hash, observation_stage_version, durable_job_id, job_generation);

-- Constraint: proxy_artifact_resolutions proxy_artifact_resolutions_pkey

ALTER TABLE ONLY proxy_artifact_resolutions
    ADD CONSTRAINT proxy_artifact_resolutions_pkey PRIMARY KEY (id);

-- Constraint: proxy_detection_evidence proxy_detection_evidence_chain_id_address_block_hash_stage__key

ALTER TABLE ONLY proxy_detection_evidence
    ADD CONSTRAINT proxy_detection_evidence_chain_id_address_block_hash_stage__key UNIQUE NULLS NOT DISTINCT (chain_id, address, block_hash, stage_version, candidate_kind, durable_job_id, job_generation);

-- Constraint: proxy_detection_evidence proxy_detection_evidence_pkey

ALTER TABLE ONLY proxy_detection_evidence
    ADD CONSTRAINT proxy_detection_evidence_pkey PRIMARY KEY (id);

-- Constraint: proxy_history_epochs proxy_history_epochs_durable_job_id_requested_generation_ph_key

ALTER TABLE ONLY proxy_history_epochs
    ADD CONSTRAINT proxy_history_epochs_durable_job_id_requested_generation_ph_key UNIQUE (durable_job_id, requested_generation, phase);

-- Constraint: proxy_history_epochs proxy_history_epochs_pkey

ALTER TABLE ONLY proxy_history_epochs
    ADD CONSTRAINT proxy_history_epochs_pkey PRIMARY KEY (epoch_id);

-- Constraint: proxy_initialization_events proxy_initialization_events_pkey

ALTER TABLE ONLY proxy_initialization_events
    ADD CONSTRAINT proxy_initialization_events_pkey PRIMARY KEY (chain_id, block_hash, log_index, stage_version);

-- Constraint: proxy_interaction_coverage_ranges proxy_interaction_coverage_ranges_chain_id_end_block_key

ALTER TABLE ONLY proxy_interaction_coverage_ranges
    ADD CONSTRAINT proxy_interaction_coverage_ranges_chain_id_end_block_key UNIQUE (chain_id, end_block);

-- Constraint: proxy_interaction_coverage_ranges proxy_interaction_coverage_ranges_pkey

ALTER TABLE ONLY proxy_interaction_coverage_ranges
    ADD CONSTRAINT proxy_interaction_coverage_ranges_pkey PRIMARY KEY (chain_id, start_block);

-- Constraint: proxy_interaction_covered_blocks proxy_interaction_covered_blo_chain_id_block_number_block_h_key

ALTER TABLE ONLY proxy_interaction_covered_blocks
    ADD CONSTRAINT proxy_interaction_covered_blo_chain_id_block_number_block_h_key UNIQUE (chain_id, block_number, block_hash);

-- Constraint: proxy_interaction_covered_blocks proxy_interaction_covered_blocks_pkey

ALTER TABLE ONLY proxy_interaction_covered_blocks
    ADD CONSTRAINT proxy_interaction_covered_blocks_pkey PRIMARY KEY (chain_id, block_number);

-- Constraint: proxy_observation_generations proxy_observation_generations_chain_id_proxy_address_observ_key

ALTER TABLE ONLY proxy_observation_generations
    ADD CONSTRAINT proxy_observation_generations_chain_id_proxy_address_observ_key UNIQUE NULLS NOT DISTINCT (chain_id, proxy_address, observation_block_hash, observation_stage_version, durable_job_id, job_generation);

-- Constraint: proxy_observation_generations proxy_observation_generations_pkey

ALTER TABLE ONLY proxy_observation_generations
    ADD CONSTRAINT proxy_observation_generations_pkey PRIMARY KEY (id);

-- Constraint: proxy_observations proxy_observations_pkey

ALTER TABLE ONLY proxy_observations
    ADD CONSTRAINT proxy_observations_pkey PRIMARY KEY (chain_id, proxy_address, block_hash, stage_version);

-- Constraint: proxy_replay_targets proxy_replay_targets_pkey

ALTER TABLE ONLY proxy_replay_targets
    ADD CONSTRAINT proxy_replay_targets_pkey PRIMARY KEY (chain_id, block_hash, address, target_kind, source_kind, source_verification_job_id);

-- Constraint: proxy_upgrade_events proxy_upgrade_events_pkey

ALTER TABLE ONLY proxy_upgrade_events
    ADD CONSTRAINT proxy_upgrade_events_pkey PRIMARY KEY (chain_id, block_hash, log_index, stage_version);

-- Constraint: receipts receipts_chain_id_block_number_block_hash_tx_hash_key

ALTER TABLE ONLY receipts
    ADD CONSTRAINT receipts_chain_id_block_number_block_hash_tx_hash_key UNIQUE (chain_id, block_number, block_hash, tx_hash);

-- Constraint: receipts_default receipts_default_chain_id_block_number_block_hash_tx_hash_key

ALTER TABLE ONLY receipts_default
    ADD CONSTRAINT receipts_default_chain_id_block_number_block_hash_tx_hash_key UNIQUE (chain_id, block_number, block_hash, tx_hash);

-- Constraint: receipts receipts_pkey

ALTER TABLE ONLY receipts
    ADD CONSTRAINT receipts_pkey PRIMARY KEY (chain_id, block_number, block_hash, tx_index);

-- Constraint: receipts_default receipts_default_pkey

ALTER TABLE ONLY receipts_default
    ADD CONSTRAINT receipts_default_pkey PRIMARY KEY (chain_id, block_number, block_hash, tx_index);

-- Constraint: receipts_p_0_1000000 receipts_p_0_1000000_chain_id_block_number_block_hash_tx_ha_key

ALTER TABLE ONLY receipts_p_0_1000000
    ADD CONSTRAINT receipts_p_0_1000000_chain_id_block_number_block_hash_tx_ha_key UNIQUE (chain_id, block_number, block_hash, tx_hash);

-- Constraint: receipts_p_0_1000000 receipts_p_0_1000000_pkey

ALTER TABLE ONLY receipts_p_0_1000000
    ADD CONSTRAINT receipts_p_0_1000000_pkey PRIMARY KEY (chain_id, block_number, block_hash, tx_index);

-- Constraint: reorg_events reorg_events_pkey

ALTER TABLE ONLY reorg_events
    ADD CONSTRAINT reorg_events_pkey PRIMARY KEY (id);

-- Constraint: repair_requests repair_requests_pkey

ALTER TABLE ONLY repair_requests
    ADD CONSTRAINT repair_requests_pkey PRIMARY KEY (id);

-- Constraint: runtime_events runtime_events_pkey

ALTER TABLE ONLY runtime_events
    ADD CONSTRAINT runtime_events_pkey PRIMARY KEY (id);

-- Constraint: search_catalog_documents search_catalog_documents_chain_id_source_kind_source_identi_key

ALTER TABLE ONLY search_catalog_documents
    ADD CONSTRAINT search_catalog_documents_chain_id_source_kind_source_identi_key UNIQUE (chain_id, source_kind, source_identity, valid_from_generation);

-- Check Constraint: search_catalog_documents search_catalog_documents_name_source_check

ALTER TABLE search_catalog_documents
    ADD CONSTRAINT search_catalog_documents_name_source_check CHECK ((((source_kind = 'name'::text) AND (name_observation_id IS NOT NULL) AND (name_source = ANY (ARRAY['ens'::text, 'custom_ens'::text]))) OR ((source_kind <> 'name'::text) AND (name_observation_id IS NULL) AND (name_source IS NULL)))) NOT VALID;

-- Constraint: search_catalog_documents search_catalog_documents_pkey

ALTER TABLE ONLY search_catalog_documents
    ADD CONSTRAINT search_catalog_documents_pkey PRIMARY KEY (id);

-- Constraint: search_catalog_generations search_catalog_generations_pkey

ALTER TABLE ONLY search_catalog_generations
    ADD CONSTRAINT search_catalog_generations_pkey PRIMARY KEY (chain_id);

-- Constraint: sync_runtime_status sync_runtime_status_pkey

ALTER TABLE ONLY sync_runtime_status
    ADD CONSTRAINT sync_runtime_status_pkey PRIMARY KEY (chain_id);

-- Constraint: sync_runtime_status_writer_leases sync_runtime_status_writer_leases_pkey

ALTER TABLE ONLY sync_runtime_status_writer_leases
    ADD CONSTRAINT sync_runtime_status_writer_leases_pkey PRIMARY KEY (chain_id);

-- Constraint: token_balance_deltas token_balance_deltas_pkey

ALTER TABLE ONLY token_balance_deltas
    ADD CONSTRAINT token_balance_deltas_pkey PRIMARY KEY (chain_id, block_number, block_hash, log_index, sub_index, token_address, owner_address);

-- Constraint: token_balance_deltas_default token_balance_deltas_default_pkey

ALTER TABLE ONLY token_balance_deltas_default
    ADD CONSTRAINT token_balance_deltas_default_pkey PRIMARY KEY (chain_id, block_number, block_hash, log_index, sub_index, token_address, owner_address);

-- Constraint: token_balance_deltas_p_0_1000000 token_balance_deltas_p_0_1000000_pkey

ALTER TABLE ONLY token_balance_deltas_p_0_1000000
    ADD CONSTRAINT token_balance_deltas_p_0_1000000_pkey PRIMARY KEY (chain_id, block_number, block_hash, log_index, sub_index, token_address, owner_address);

-- Constraint: token_contracts token_contracts_pkey

ALTER TABLE ONLY token_contracts
    ADD CONSTRAINT token_contracts_pkey PRIMARY KEY (chain_id, address, code_hash, observed_block_hash);

-- Constraint: token_events token_events_pkey

ALTER TABLE ONLY token_events
    ADD CONSTRAINT token_events_pkey PRIMARY KEY (chain_id, block_number, block_hash, log_index, sub_index);

-- Constraint: token_events_default token_events_default_pkey

ALTER TABLE ONLY token_events_default
    ADD CONSTRAINT token_events_default_pkey PRIMARY KEY (chain_id, block_number, block_hash, log_index, sub_index);

-- Constraint: token_events_p_0_1000000 token_events_p_0_1000000_pkey

ALTER TABLE ONLY token_events_p_0_1000000
    ADD CONSTRAINT token_events_p_0_1000000_pkey PRIMARY KEY (chain_id, block_number, block_hash, log_index, sub_index);

-- Constraint: trace_log_attributions trace_log_attributions_pkey

ALTER TABLE ONLY trace_log_attributions
    ADD CONSTRAINT trace_log_attributions_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, log_index);

-- Constraint: trace_log_attributions_default trace_log_attributions_default_pkey

ALTER TABLE ONLY trace_log_attributions_default
    ADD CONSTRAINT trace_log_attributions_default_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, log_index);

-- Constraint: trace_log_attributions_p_0_1000000 trace_log_attributions_p_0_1000000_pkey

ALTER TABLE ONLY trace_log_attributions_p_0_1000000
    ADD CONSTRAINT trace_log_attributions_p_0_1000000_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, log_index);

-- Constraint: transaction_effective_execution_identities transaction_effective_execution_identities_pkey

ALTER TABLE ONLY transaction_effective_execution_identities
    ADD CONSTRAINT transaction_effective_execution_identities_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, context_address);

-- Constraint: transaction_effective_execution_identities_default transaction_effective_execution_identities_default_pkey

ALTER TABLE ONLY transaction_effective_execution_identities_default
    ADD CONSTRAINT transaction_effective_execution_identities_default_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, context_address);

-- Constraint: transaction_effective_execution_identities_p_0_1000000 transaction_effective_execution_identities_p_0_1000000_pkey

ALTER TABLE ONLY transaction_effective_execution_identities_p_0_1000000
    ADD CONSTRAINT transaction_effective_execution_identities_p_0_1000000_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, context_address);

-- Constraint: transaction_execution_code_resolutions transaction_execution_code_resolutions_pkey

ALTER TABLE ONLY transaction_execution_code_resolutions
    ADD CONSTRAINT transaction_execution_code_resolutions_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, context_address);

-- Constraint: transaction_execution_code_resolutions_default transaction_execution_code_resolutions_default_pkey

ALTER TABLE ONLY transaction_execution_code_resolutions_default
    ADD CONSTRAINT transaction_execution_code_resolutions_default_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, context_address);

-- Constraint: transaction_execution_code_resolutions_p_0_1000000 transaction_execution_code_resolutions_p_0_1000000_pkey

ALTER TABLE ONLY transaction_execution_code_resolutions_p_0_1000000
    ADD CONSTRAINT transaction_execution_code_resolutions_p_0_1000000_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, context_address);

-- Constraint: transaction_inclusions transaction_inclusions_chain_id_block_number_block_hash_tx__key

ALTER TABLE ONLY transaction_inclusions
    ADD CONSTRAINT transaction_inclusions_chain_id_block_number_block_hash_tx__key UNIQUE (chain_id, block_number, block_hash, tx_hash);

-- Constraint: transaction_inclusions transaction_inclusions_position_hash_key

ALTER TABLE ONLY transaction_inclusions
    ADD CONSTRAINT transaction_inclusions_position_hash_key UNIQUE (chain_id, block_number, block_hash, tx_index, tx_hash);

-- Constraint: transaction_inclusions_default transaction_inclusions_defaul_chain_id_block_number_block__key1

ALTER TABLE ONLY transaction_inclusions_default
    ADD CONSTRAINT transaction_inclusions_defaul_chain_id_block_number_block__key1 UNIQUE (chain_id, block_number, block_hash, tx_index, tx_hash);

-- Constraint: transaction_inclusions_default transaction_inclusions_defaul_chain_id_block_number_block_h_key

ALTER TABLE ONLY transaction_inclusions_default
    ADD CONSTRAINT transaction_inclusions_defaul_chain_id_block_number_block_h_key UNIQUE (chain_id, block_number, block_hash, tx_hash);

-- Constraint: transaction_inclusions transaction_inclusions_pkey

ALTER TABLE ONLY transaction_inclusions
    ADD CONSTRAINT transaction_inclusions_pkey PRIMARY KEY (chain_id, block_number, block_hash, tx_index);

-- Constraint: transaction_inclusions_default transaction_inclusions_default_pkey

ALTER TABLE ONLY transaction_inclusions_default
    ADD CONSTRAINT transaction_inclusions_default_pkey PRIMARY KEY (chain_id, block_number, block_hash, tx_index);

-- Constraint: transaction_inclusions_p_0_1000000 transaction_inclusions_p_0_1000000_pkey

ALTER TABLE ONLY transaction_inclusions_p_0_1000000
    ADD CONSTRAINT transaction_inclusions_p_0_1000000_pkey PRIMARY KEY (chain_id, block_number, block_hash, tx_index);

-- Constraint: transaction_inclusions_p_0_1000000 transaction_inclusions_p_0_10_chain_id_block_number_block__key1

ALTER TABLE ONLY transaction_inclusions_p_0_1000000
    ADD CONSTRAINT transaction_inclusions_p_0_10_chain_id_block_number_block__key1 UNIQUE (chain_id, block_number, block_hash, tx_index, tx_hash);

-- Constraint: transaction_inclusions_p_0_1000000 transaction_inclusions_p_0_10_chain_id_block_number_block_h_key

ALTER TABLE ONLY transaction_inclusions_p_0_1000000
    ADD CONSTRAINT transaction_inclusions_p_0_10_chain_id_block_number_block_h_key UNIQUE (chain_id, block_number, block_hash, tx_hash);

-- Constraint: transaction_state_changes transaction_state_changes_pkey

ALTER TABLE ONLY transaction_state_changes
    ADD CONSTRAINT transaction_state_changes_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, address, field_kind, storage_key);

-- Constraint: transaction_state_changes_default transaction_state_changes_default_pkey

ALTER TABLE ONLY transaction_state_changes_default
    ADD CONSTRAINT transaction_state_changes_default_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, address, field_kind, storage_key);

-- Constraint: transaction_state_changes_p_0_1000000 transaction_state_changes_p_0_1000000_pkey

ALTER TABLE ONLY transaction_state_changes_p_0_1000000
    ADD CONSTRAINT transaction_state_changes_p_0_1000000_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, address, field_kind, storage_key);

-- Constraint: transactional_outbox transactional_outbox_chain_id_topic_message_key_key

ALTER TABLE ONLY transactional_outbox
    ADD CONSTRAINT transactional_outbox_chain_id_topic_message_key_key UNIQUE (chain_id, topic, message_key);

-- Constraint: transactional_outbox transactional_outbox_pkey

ALTER TABLE ONLY transactional_outbox
    ADD CONSTRAINT transactional_outbox_pkey PRIMARY KEY (id);

-- Constraint: transactions transactions_pkey

ALTER TABLE ONLY transactions
    ADD CONSTRAINT transactions_pkey PRIMARY KEY (chain_id, hash);

-- Constraint: user_sessions user_sessions_pkey

ALTER TABLE ONLY user_sessions
    ADD CONSTRAINT user_sessions_pkey PRIMARY KEY (id);

-- Constraint: user_sessions user_sessions_token_digest_key

ALTER TABLE ONLY user_sessions
    ADD CONSTRAINT user_sessions_token_digest_key UNIQUE (token_digest);

-- Constraint: users users_chain_id_address_key

ALTER TABLE ONLY users
    ADD CONSTRAINT users_chain_id_address_key UNIQUE (chain_id, address);

-- Constraint: users users_pkey

ALTER TABLE ONLY users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);

-- Constraint: uups_implementation_observation_generations uups_implementation_observati_chain_id_implementation_addre_key

ALTER TABLE ONLY uups_implementation_observation_generations
    ADD CONSTRAINT uups_implementation_observati_chain_id_implementation_addre_key UNIQUE (chain_id, implementation_address, observation_block_hash, observation_stage_version, verification_job_id, durable_job_id, job_generation);

-- Constraint: uups_implementation_observation_generations uups_implementation_observation_generations_pkey

ALTER TABLE ONLY uups_implementation_observation_generations
    ADD CONSTRAINT uups_implementation_observation_generations_pkey PRIMARY KEY (id);

-- Constraint: uups_implementation_observations uups_implementation_observations_pkey

ALTER TABLE ONLY uups_implementation_observations
    ADD CONSTRAINT uups_implementation_observations_pkey PRIMARY KEY (chain_id, implementation_address, block_hash, stage_version, verification_job_id);

-- Constraint: verification_compilation_contracts verification_compilation_contracts_pkey

ALTER TABLE ONLY verification_compilation_contracts
    ADD CONSTRAINT verification_compilation_contracts_pkey PRIMARY KEY (compilation_id, file_name, contract_name);

-- Constraint: verification_compilation_units verification_compilation_units_pkey

ALTER TABLE ONLY verification_compilation_units
    ADD CONSTRAINT verification_compilation_units_pkey PRIMARY KEY (id);

-- Constraint: verification_compilation_units verification_compilation_units_source_job_id_key

ALTER TABLE ONLY verification_compilation_units
    ADD CONSTRAINT verification_compilation_units_source_job_id_key UNIQUE (source_job_id);

-- Constraint: verification_jobs verification_jobs_pkey

ALTER TABLE ONLY verification_jobs
    ADD CONSTRAINT verification_jobs_pkey PRIMARY KEY (id);

-- Constraint: verification_results verification_results_job_digest_key

ALTER TABLE ONLY verification_results
    ADD CONSTRAINT verification_results_job_digest_key UNIQUE (job_id, request_digest);

-- Constraint: verification_results verification_results_pkey

ALTER TABLE ONLY verification_results
    ADD CONSTRAINT verification_results_pkey PRIMARY KEY (job_id);

-- Constraint: verified_contract_proxy_artifacts verified_contract_proxy_artifacts_pkey

ALTER TABLE ONLY verified_contract_proxy_artifacts
    ADD CONSTRAINT verified_contract_proxy_artifacts_pkey PRIMARY KEY (chain_id, address, code_hash, valid_from_block, verification_job_id);

-- Constraint: verified_contract_proxy_artifacts verified_contract_proxy_artifacts_verification_job_id_key

ALTER TABLE ONLY verified_contract_proxy_artifacts
    ADD CONSTRAINT verified_contract_proxy_artifacts_verification_job_id_key UNIQUE (verification_job_id);

-- Constraint: verified_contracts verified_contracts_pkey

ALTER TABLE ONLY verified_contracts
    ADD CONSTRAINT verified_contracts_pkey PRIMARY KEY (chain_id, address, code_hash, valid_from_block, verification_job_id);

-- Constraint: verified_contracts verified_contracts_selector_identity_unique

ALTER TABLE ONLY verified_contracts
    ADD CONSTRAINT verified_contracts_selector_identity_unique UNIQUE (chain_id, address, code_hash, valid_from_block, verification_job_id);

-- Constraint: verified_function_selector_sets verified_function_selector_sets_lookup_identity_unique

ALTER TABLE ONLY verified_function_selector_sets
    ADD CONSTRAINT verified_function_selector_sets_lookup_identity_unique UNIQUE (verification_job_id, chain_id, address, code_hash);

-- Constraint: verified_function_selector_sets verified_function_selector_sets_pkey

ALTER TABLE ONLY verified_function_selector_sets
    ADD CONSTRAINT verified_function_selector_sets_pkey PRIMARY KEY (verification_job_id);

-- Constraint: verified_function_selectors verified_function_selectors_pkey

ALTER TABLE ONLY verified_function_selectors
    ADD CONSTRAINT verified_function_selectors_pkey PRIMARY KEY (verification_job_id, selector, signature);

-- Constraint: verified_proxy_bindings verified_proxy_bindings_pkey

ALTER TABLE ONLY verified_proxy_bindings
    ADD CONSTRAINT verified_proxy_bindings_pkey PRIMARY KEY (verification_job_id);

-- Constraint: watch_notification_work watch_notification_work_pkey

ALTER TABLE ONLY watch_notification_work
    ADD CONSTRAINT watch_notification_work_pkey PRIMARY KEY (chain_id, block_hash);

-- Constraint: watch_notifications watch_notifications_pkey

ALTER TABLE ONLY watch_notifications
    ADD CONSTRAINT watch_notifications_pkey PRIMARY KEY (id);

-- Constraint: watch_notifications watch_notifications_watch_id_block_hash_source_key_key

ALTER TABLE ONLY watch_notifications
    ADD CONSTRAINT watch_notifications_watch_id_block_hash_source_key_key UNIQUE (watch_id, block_hash, source_key);

-- Constraint: withdrawals withdrawals_pkey

ALTER TABLE ONLY withdrawals
    ADD CONSTRAINT withdrawals_pkey PRIMARY KEY (chain_id, block_number, block_hash, withdrawal_index);

-- Constraint: withdrawals_default withdrawals_default_pkey

ALTER TABLE ONLY withdrawals_default
    ADD CONSTRAINT withdrawals_default_pkey PRIMARY KEY (chain_id, block_number, block_hash, withdrawal_index);

-- Constraint: withdrawals_p_0_1000000 withdrawals_p_0_1000000_pkey

ALTER TABLE ONLY withdrawals_p_0_1000000
    ADD CONSTRAINT withdrawals_p_0_1000000_pkey PRIMARY KEY (chain_id, block_number, block_hash, withdrawal_index);

-- Index: abi_decodings_target_idx

CREATE INDEX abi_decodings_target_idx ON ONLY abi_decodings USING btree (chain_id, target_address, target_code_hash, block_number DESC) WHERE canonical;

-- Index: abi_decodings_default_chain_id_target_address_target_code_h_idx

CREATE INDEX abi_decodings_default_chain_id_target_address_target_code_h_idx ON abi_decodings_default USING btree (chain_id, target_address, target_code_hash, block_number DESC) WHERE canonical;

-- Index: abi_decodings_tx_idx

CREATE INDEX abi_decodings_tx_idx ON ONLY abi_decodings USING btree (chain_id, transaction_hash, block_number DESC);

-- Index: abi_decodings_default_chain_id_transaction_hash_block_numbe_idx

CREATE INDEX abi_decodings_default_chain_id_transaction_hash_block_numbe_idx ON abi_decodings_default USING btree (chain_id, transaction_hash, block_number DESC);

-- Index: abi_decodings_p_0_1000000_chain_id_target_address_target_co_idx

CREATE INDEX abi_decodings_p_0_1000000_chain_id_target_address_target_co_idx ON abi_decodings_p_0_1000000 USING btree (chain_id, target_address, target_code_hash, block_number DESC) WHERE canonical;

-- Index: abi_decodings_p_0_1000000_chain_id_transaction_hash_block_n_idx

CREATE INDEX abi_decodings_p_0_1000000_chain_id_transaction_hash_block_n_idx ON abi_decodings_p_0_1000000 USING btree (chain_id, transaction_hash, block_number DESC);

-- Index: address_activities_address_idx

CREATE INDEX address_activities_address_idx ON ONLY address_activities USING btree (chain_id, address, block_number DESC, activity_index DESC) WHERE canonical;

-- Index: address_activities_default_chain_id_address_block_number_ac_idx

CREATE INDEX address_activities_default_chain_id_address_block_number_ac_idx ON address_activities_default USING btree (chain_id, address, block_number DESC, activity_index DESC) WHERE canonical;

-- Index: address_activities_p_0_100000_chain_id_address_block_number_idx

CREATE INDEX address_activities_p_0_100000_chain_id_address_block_number_idx ON address_activities_p_0_1000000 USING btree (chain_id, address, block_number DESC, activity_index DESC) WHERE canonical;

-- Index: address_export_admissions_active

CREATE INDEX address_export_admissions_active ON address_export_admissions USING btree (expires_at) WHERE (NOT released);

-- Index: address_export_admissions_user

CREATE INDEX address_export_admissions_user ON address_export_admissions USING btree (user_id, started_at);

-- Index: address_watches_match

CREATE INDEX address_watches_match ON address_watches USING btree (chain_id, address, id) WHERE ((deleted_at IS NULL) AND enabled);

-- Index: address_watches_owner_address

CREATE UNIQUE INDEX address_watches_owner_address ON address_watches USING btree (user_id, chain_id, address) WHERE (deleted_at IS NULL);

-- Index: api_keys_owner_active_idx

CREATE INDEX api_keys_owner_active_idx ON api_keys USING btree (owner_user_id, prefix) WHERE ((owner_user_id IS NOT NULL) AND (revoked_at IS NULL));

-- Index: api_keys_owner_page_idx

CREATE INDEX api_keys_owner_page_idx ON api_keys USING btree (owner_user_id, created_at DESC, prefix DESC) WHERE (owner_user_id IS NOT NULL);

-- Index: auth_challenges_active_idx

CREATE INDEX auth_challenges_active_idx ON auth_challenges USING btree (chain_id, address, expires_at, id) WHERE (consumed_at IS NULL);

-- Index: auth_challenges_cleanup_idx

CREATE INDEX auth_challenges_cleanup_idx ON auth_challenges USING btree (expires_at, id);

-- Index: beacon_implementation_history_idx

CREATE INDEX beacon_implementation_history_idx ON beacon_implementation_observations USING btree (chain_id, beacon_address, block_number DESC, block_hash DESC) WHERE canonical;

-- Index: billing_account_entries_user_time_idx

CREATE INDEX billing_account_entries_user_time_idx ON billing_account_entries USING btree (user_id, occurred_at DESC, id DESC);

-- Index: billing_accounts_chain_updated_idx

CREATE INDEX billing_accounts_chain_updated_idx ON billing_accounts USING btree (chain_id, updated_at DESC, user_id);

-- Index: billing_payment_events_payment_idx

CREATE INDEX billing_payment_events_payment_idx ON billing_payment_events USING btree (payment_id, id);

-- Index: billing_payments_active_expiry_idx

CREATE INDEX billing_payments_active_expiry_idx ON billing_payments USING btree (reservation_expires_at, id) WHERE (state = ANY (ARRAY['reserved'::text, 'verified'::text]));

-- Index: billing_payments_admin_filter_idx

CREATE INDEX billing_payments_admin_filter_idx ON billing_payments USING btree (chain_id, state, operation, network, asset, created_at DESC, id DESC);

-- Index: billing_payments_chain_created_idx

CREATE INDEX billing_payments_chain_created_idx ON billing_payments USING btree (chain_id, created_at DESC, id DESC);

-- Index: billing_payments_settling_metrics_idx

CREATE INDEX billing_payments_settling_metrics_idx ON billing_payments USING btree (chain_id, operation, failure_code, settling_at) WHERE (state = 'settling'::text);

-- Index: billing_payments_topup_settled_idx

CREATE UNIQUE INDEX billing_payments_topup_settled_idx ON billing_payments USING btree (topup_intent_id) WHERE ((purpose = 'account_topup'::text) AND (state = 'settled'::text));

-- Index: billing_payments_user_created_idx

CREATE INDEX billing_payments_user_created_idx ON billing_payments USING btree (user_id, created_at DESC, id DESC) WHERE (user_id IS NOT NULL);

-- Index: billing_topup_intents_active_payment_idx

CREATE UNIQUE INDEX billing_topup_intents_active_payment_idx ON billing_topup_intents USING btree (active_payment_id) WHERE (active_payment_id IS NOT NULL);

-- Index: billing_topup_intents_expiry_idx

CREATE INDEX billing_topup_intents_expiry_idx ON billing_topup_intents USING btree (expires_at, id) WHERE (state = 'open'::text);

-- Index: billing_topup_intents_user_created_idx

CREATE INDEX billing_topup_intents_user_created_idx ON billing_topup_intents USING btree (user_id, created_at DESC, id DESC);

-- Index: billing_usage_charges_expiry_idx

CREATE INDEX billing_usage_charges_expiry_idx ON billing_usage_charges USING btree (reservation_expires_at, id) WHERE (state = 'reserved'::text);

-- Index: billing_usage_charges_operation_idx

CREATE INDEX billing_usage_charges_operation_idx ON billing_usage_charges USING btree (chain_id, operation, state, created_at DESC);

-- Index: billing_usage_charges_user_created_idx

CREATE INDEX billing_usage_charges_user_created_idx ON billing_usage_charges USING btree (user_id, created_at DESC, id DESC);

-- Index: block_journals_canonical_idx

CREATE INDEX block_journals_canonical_idx ON block_journals USING btree (chain_id, stage, canonical, block_hash);

-- Index: block_journals_job_generation_idx

CREATE UNIQUE INDEX block_journals_job_generation_idx ON block_journals USING btree (durable_job_id, job_generation) WHERE (durable_job_id IS NOT NULL);

-- Index: block_stage_results_height_idx

CREATE INDEX block_stage_results_height_idx ON block_stage_results USING btree (chain_id, block_number, stage, stage_version);

-- Index: block_stage_results_job_generation_idx

CREATE UNIQUE INDEX block_stage_results_job_generation_idx ON block_stage_results USING btree (durable_job_id, job_generation) WHERE (durable_job_id IS NOT NULL);

-- Index: blocks_amsterdam_idx

CREATE INDEX blocks_amsterdam_idx ON blocks USING btree (chain_id, number, hash) WHERE ((slot_number_raw IS NOT NULL) AND (slot_number_raw <> 'null'::jsonb));

-- Index: blocks_chain_hash_uq

CREATE UNIQUE INDEX blocks_chain_hash_uq ON blocks USING btree (chain_id, hash);

-- Index: blocks_miner_number_idx

CREATE INDEX blocks_miner_number_idx ON blocks USING btree (chain_id, lower(miner_text), number DESC, hash DESC);

-- Index: blocks_parent_idx

CREATE INDEX blocks_parent_idx ON blocks USING btree (chain_id, parent_hash);

-- Index: blocks_timestamp_number_idx

CREATE INDEX blocks_timestamp_number_idx ON blocks USING btree (chain_id, "timestamp", number, hash);

-- Index: canonical_blocks_hash_uq

CREATE UNIQUE INDEX canonical_blocks_hash_uq ON canonical_blocks USING btree (chain_id, block_hash);

-- Index: chart_hourly_rollups_range_idx

CREATE INDEX chart_hourly_rollups_range_idx ON chart_hourly_rollups USING btree (chain_id, bucket_start DESC);

-- Index: chart_rollup_dirty_recent_idx

CREATE INDEX chart_rollup_dirty_recent_idx ON chart_rollup_dirty_hours USING btree (chain_id, bucket_start DESC);

-- Index: contract_abis_context_idx

CREATE INDEX contract_abis_context_idx ON contract_abis USING btree (chain_id, address, code_hash, block_number DESC, source, canonical);

-- Index: contract_code_history_idx

CREATE INDEX contract_code_history_idx ON contract_code_observations USING btree (chain_id, address, block_number DESC) WHERE canonical;

-- Index: core_backfill_leases_expiry_idx

CREATE INDEX core_backfill_leases_expiry_idx ON core_backfill_leases USING btree (chain_id, expires_at, range_start);

-- Index: core_coverage_ranges_end_uq

CREATE UNIQUE INDEX core_coverage_ranges_end_uq ON core_coverage_ranges USING btree (chain_id, range_end);

-- Index: derived_verification_attempts_created_idx

CREATE INDEX derived_verification_attempts_created_idx ON derived_verification_attempts USING btree (chain_id, created_address, block_number DESC);

-- Index: derived_verification_attempts_creator_idx

CREATE INDEX derived_verification_attempts_creator_idx ON derived_verification_attempts USING btree (chain_id, creator_address, block_number DESC);

-- Index: derived_verification_forward_blocks_claim_idx

CREATE INDEX derived_verification_forward_blocks_claim_idx ON derived_verification_forward_blocks USING btree (status, updated_at, id) WHERE (status = ANY (ARRAY['queued'::text, 'running'::text]));

-- Index: derived_verification_scans_claim_idx

CREATE INDEX derived_verification_scans_claim_idx ON derived_verification_scans USING btree (status, updated_at, compilation_id) WHERE (status = ANY (ARRAY['queued'::text, 'running'::text]));

-- Index: diamond_cut_events_history_idx

CREATE INDEX diamond_cut_events_history_idx ON diamond_cut_events USING btree (chain_id, diamond_address, block_number, transaction_index, log_index) WHERE (canonical AND (stage_version = 2));

-- Index: diamond_loupe_selectors_facet_idx

CREATE INDEX diamond_loupe_selectors_facet_idx ON diamond_loupe_selectors USING btree (snapshot_id, facet_address, selector);

-- Index: diamond_loupe_snapshots_current_idx

CREATE INDEX diamond_loupe_snapshots_current_idx ON diamond_loupe_snapshots USING btree (chain_id, diamond_address, block_number DESC, id DESC) WHERE (canonical AND (stage_version = 2));

-- Index: diamond_selector_changes_selector_idx

CREATE INDEX diamond_selector_changes_selector_idx ON diamond_selector_changes USING btree (chain_id, selector, block_hash, log_index);

-- Index: durable_jobs_claim_idx

CREATE INDEX durable_jobs_claim_idx ON durable_jobs USING btree (chain_id, stage, priority DESC, available_at, id) WHERE (status = ANY (ARRAY['queued'::text, 'leased'::text]));

-- Index: durable_jobs_replay_pending_idx

CREATE INDEX durable_jobs_replay_pending_idx ON durable_jobs USING btree (available_at, id) WHERE ((requested_generation > completed_generation) AND (status = ANY (ARRAY['queued'::text, 'leased'::text])));

-- Index: eip7702_authorizations_authority_idx

CREATE INDEX eip7702_authorizations_authority_idx ON ONLY eip7702_authorizations USING btree (chain_id, authority, block_number DESC, transaction_index DESC, authorization_index DESC) WHERE (canonical AND (authority IS NOT NULL));

-- Index: eip7702_authorizations_defaul_chain_id_authority_block_numb_idx

CREATE INDEX eip7702_authorizations_defaul_chain_id_authority_block_numb_idx ON eip7702_authorizations_default USING btree (chain_id, authority, block_number DESC, transaction_index DESC, authorization_index DESC) WHERE (canonical AND (authority IS NOT NULL));

-- Index: eip7702_authorizations_tx_idx

CREATE INDEX eip7702_authorizations_tx_idx ON ONLY eip7702_authorizations USING btree (chain_id, transaction_hash, authorization_index);

-- Index: eip7702_authorizations_defaul_chain_id_transaction_hash_aut_idx

CREATE INDEX eip7702_authorizations_defaul_chain_id_transaction_hash_aut_idx ON eip7702_authorizations_default USING btree (chain_id, transaction_hash, authorization_index);

-- Index: eip7702_authorizations_p_0_10_chain_id_authority_block_numb_idx

CREATE INDEX eip7702_authorizations_p_0_10_chain_id_authority_block_numb_idx ON eip7702_authorizations_p_0_1000000 USING btree (chain_id, authority, block_number DESC, transaction_index DESC, authorization_index DESC) WHERE (canonical AND (authority IS NOT NULL));

-- Index: eip7702_authorizations_p_0_10_chain_id_transaction_hash_aut_idx

CREATE INDEX eip7702_authorizations_p_0_10_chain_id_transaction_hash_aut_idx ON eip7702_authorizations_p_0_1000000 USING btree (chain_id, transaction_hash, authorization_index);

-- Index: ens_address_name_snapshots_expiry_idx

CREATE INDEX ens_address_name_snapshots_expiry_idx ON ens_address_name_snapshots USING btree (expires_at, id);

-- Index: ens_name_observations_address_idx

CREATE INDEX ens_name_observations_address_idx ON ens_name_observations USING btree (generation_id, source, direction, address) WHERE (direction = 'primary'::text);

-- Index: ens_resolution_failures_fresh_idx

CREATE INDEX ens_resolution_failures_fresh_idx ON ens_resolution_failures USING btree (generation_id, source, direction, lookup_key, expires_at DESC, id DESC);

-- Index: ens_resolution_generations_current_idx

CREATE INDEX ens_resolution_generations_current_idx ON ens_resolution_generations USING btree (chain_id, policy_key, fresh_until DESC, id DESC);

-- Index: ens_resolution_generations_retention_idx

CREATE INDEX ens_resolution_generations_retention_idx ON ens_resolution_generations USING btree (retain_until, id);

-- Index: erc1155_balance_reconciliations_owner_idx

CREATE INDEX erc1155_balance_reconciliations_owner_idx ON erc1155_balance_reconciliations USING btree (chain_id, owner_address, token_address, token_id, block_number DESC);

-- Index: erc20_balance_reconciliations_owner_snapshot_idx

CREATE INDEX erc20_balance_reconciliations_owner_snapshot_idx ON erc20_balance_reconciliations USING btree (chain_id, owner_address, block_number, block_hash, token_address);

-- Index: erc20_holder_balances_page_idx

CREATE INDEX erc20_holder_balances_page_idx ON ONLY erc20_holder_balances USING btree (chain_id, token_address, holder_address, block_number DESC, block_hash) WHERE canonical;

-- Index: erc20_holder_balances_default_chain_id_token_address_holder_idx

CREATE INDEX erc20_holder_balances_default_chain_id_token_address_holder_idx ON erc20_holder_balances_default USING btree (chain_id, token_address, holder_address, block_number DESC, block_hash) WHERE canonical;

-- Index: erc20_holder_balances_p_0_100_chain_id_token_address_holder_idx

CREATE INDEX erc20_holder_balances_p_0_100_chain_id_token_address_holder_idx ON erc20_holder_balances_p_0_1000000 USING btree (chain_id, token_address, holder_address, block_number DESC, block_hash) WHERE canonical;

-- Index: erc20_holder_snapshots_current_idx

CREATE INDEX erc20_holder_snapshots_current_idx ON ONLY erc20_holder_snapshots USING btree (chain_id, token_address, block_number DESC, block_hash) WHERE canonical;

-- Index: erc20_holder_snapshots_defaul_chain_id_token_address_block__idx

CREATE INDEX erc20_holder_snapshots_defaul_chain_id_token_address_block__idx ON erc20_holder_snapshots_default USING btree (chain_id, token_address, block_number DESC, block_hash) WHERE canonical;

-- Index: erc20_holder_snapshots_p_0_10_chain_id_token_address_block__idx

CREATE INDEX erc20_holder_snapshots_p_0_10_chain_id_token_address_block__idx ON erc20_holder_snapshots_p_0_1000000 USING btree (chain_id, token_address, block_number DESC, block_hash) WHERE canonical;

-- Index: erc4337_coverage_ranges_end_idx

CREATE INDEX erc4337_coverage_ranges_end_idx ON erc4337_coverage_ranges USING btree (chain_id, configuration_digest, end_block, start_block);

-- Index: erc4337_user_operation_participants_address_idx

CREATE INDEX erc4337_user_operation_participants_address_idx ON ONLY erc4337_user_operation_participants USING btree (chain_id, configuration_digest, address, block_number DESC, operation_index DESC) WHERE canonical;

-- Index: erc4337_user_operation_partic_chain_id_configuration_diges_idx1

CREATE INDEX erc4337_user_operation_partic_chain_id_configuration_diges_idx1 ON erc4337_user_operation_participants_default USING btree (chain_id, configuration_digest, address, block_number DESC, operation_index DESC) WHERE canonical;

-- Index: erc4337_user_operation_partic_chain_id_configuration_digest_idx

CREATE INDEX erc4337_user_operation_partic_chain_id_configuration_digest_idx ON erc4337_user_operation_participants_p_0_1000000 USING btree (chain_id, configuration_digest, address, block_number DESC, operation_index DESC) WHERE canonical;

-- Index: erc4337_user_operations_canonical_hash_idx

CREATE INDEX erc4337_user_operations_canonical_hash_idx ON ONLY erc4337_user_operations USING btree (chain_id, configuration_digest, user_op_hash) WHERE canonical;

-- Index: erc4337_user_operations_global_idx

CREATE INDEX erc4337_user_operations_global_idx ON ONLY erc4337_user_operations USING btree (chain_id, configuration_digest, block_number DESC, transaction_index DESC, operation_index DESC, user_op_hash DESC) WHERE canonical;

-- Index: erc4337_user_operations_defau_chain_id_configuration_diges_idx1

CREATE INDEX erc4337_user_operations_defau_chain_id_configuration_diges_idx1 ON erc4337_user_operations_default USING btree (chain_id, configuration_digest, block_number DESC, transaction_index DESC, operation_index DESC, user_op_hash DESC) WHERE canonical;

-- Index: erc4337_user_operations_transaction_idx

CREATE INDEX erc4337_user_operations_transaction_idx ON ONLY erc4337_user_operations USING btree (chain_id, configuration_digest, transaction_hash, operation_index) WHERE canonical;

-- Index: erc4337_user_operations_defau_chain_id_configuration_diges_idx2

CREATE INDEX erc4337_user_operations_defau_chain_id_configuration_diges_idx2 ON erc4337_user_operations_default USING btree (chain_id, configuration_digest, transaction_hash, operation_index) WHERE canonical;

-- Index: erc4337_user_operations_defau_chain_id_configuration_digest_idx

CREATE INDEX erc4337_user_operations_defau_chain_id_configuration_digest_idx ON erc4337_user_operations_default USING btree (chain_id, configuration_digest, user_op_hash) WHERE canonical;

-- Index: erc4337_user_operations_p_0_1_chain_id_configuration_diges_idx1

CREATE INDEX erc4337_user_operations_p_0_1_chain_id_configuration_diges_idx1 ON erc4337_user_operations_p_0_1000000 USING btree (chain_id, configuration_digest, block_number DESC, transaction_index DESC, operation_index DESC, user_op_hash DESC) WHERE canonical;

-- Index: erc4337_user_operations_p_0_1_chain_id_configuration_diges_idx2

CREATE INDEX erc4337_user_operations_p_0_1_chain_id_configuration_diges_idx2 ON erc4337_user_operations_p_0_1000000 USING btree (chain_id, configuration_digest, transaction_hash, operation_index) WHERE canonical;

-- Index: erc4337_user_operations_p_0_1_chain_id_configuration_digest_idx

CREATE INDEX erc4337_user_operations_p_0_1_chain_id_configuration_digest_idx ON erc4337_user_operations_p_0_1000000 USING btree (chain_id, configuration_digest, user_op_hash) WHERE canonical;

-- Index: erc721_owner_reconciliations_owner_idx

CREATE INDEX erc721_owner_reconciliations_owner_idx ON erc721_owner_reconciliations USING btree (chain_id, owner_address, token_address, token_id, block_number DESC) WHERE (state = 'owned'::text);

-- Index: external_adapter_observations_expiry_idx

CREATE INDEX external_adapter_observations_expiry_idx ON external_adapter_observations USING btree (expires_at);

-- Index: external_adapter_observations_latest_idx

CREATE INDEX external_adapter_observations_latest_idx ON external_adapter_observations USING btree (chain_id, capability, provider_key, observation_key, observed_at DESC, id DESC);

-- Index: external_metadata_attempts_resource_idx

CREATE INDEX external_metadata_attempts_resource_idx ON external_metadata_attempts USING btree (chain_id, resource_kind, resource_key, attempted_at DESC);

-- Index: external_metadata_nft_canonical_lookup_idx

CREATE INDEX external_metadata_nft_canonical_lookup_idx ON external_metadata USING btree (chain_id, token_address, token_id, observed_block_number DESC, observed_block_hash);

-- Index: external_metadata_nft_identity_uq

CREATE UNIQUE INDEX external_metadata_nft_identity_uq ON external_metadata USING btree (chain_id, token_address, token_id, observed_block_hash) WHERE ((resource_kind = 'nft'::text) AND (token_address IS NOT NULL) AND (token_id IS NOT NULL) AND (observed_block_hash IS NOT NULL));

-- Index: external_metadata_pending_idx

CREATE INDEX external_metadata_pending_idx ON external_metadata USING btree (chain_id, updated_at, resource_key) WHERE (state = 'pending'::text);

-- Index: genesis_account_observations_page_idx

CREATE INDEX genesis_account_observations_page_idx ON genesis_account_observations USING btree (chain_id, block_hash, address);

-- Index: logs_address_idx

CREATE INDEX logs_address_idx ON ONLY logs USING btree (chain_id, address, block_number DESC);

-- Index: logs_default_chain_id_address_block_number_idx

CREATE INDEX logs_default_chain_id_address_block_number_idx ON logs_default USING btree (chain_id, address, block_number DESC);

-- Index: logs_topic0_idx

CREATE INDEX logs_topic0_idx ON ONLY logs USING btree (chain_id, topic0, block_number DESC);

-- Index: logs_default_chain_id_topic0_block_number_idx

CREATE INDEX logs_default_chain_id_topic0_block_number_idx ON logs_default USING btree (chain_id, topic0, block_number DESC);

-- Index: logs_p_0_1000000_chain_id_address_block_number_idx

CREATE INDEX logs_p_0_1000000_chain_id_address_block_number_idx ON logs_p_0_1000000 USING btree (chain_id, address, block_number DESC);

-- Index: logs_p_0_1000000_chain_id_topic0_block_number_idx

CREATE INDEX logs_p_0_1000000_chain_id_topic0_block_number_idx ON logs_p_0_1000000 USING btree (chain_id, topic0, block_number DESC);

-- Index: mempool_snapshot_transactions_tx_idx

CREATE INDEX mempool_snapshot_transactions_tx_idx ON mempool_snapshot_transactions USING btree (chain_id, tx_hash, snapshot_id);

-- Index: mempool_snapshots_expiry_idx

CREATE INDEX mempool_snapshots_expiry_idx ON mempool_snapshots USING btree (chain_id, expires_at, id);

-- Index: mempool_transaction_replacements_new_idx

CREATE INDEX mempool_transaction_replacements_new_idx ON mempool_transaction_replacements USING btree (chain_id, replacement_hash, snapshot_id DESC);

-- Index: mempool_transaction_replacements_old_idx

CREATE INDEX mempool_transaction_replacements_old_idx ON mempool_transaction_replacements USING btree (chain_id, replaced_hash, snapshot_id DESC);

-- Index: mempool_transactions_expiry_idx

CREATE INDEX mempool_transactions_expiry_idx ON mempool_transactions USING btree (chain_id, expires_at, tx_hash);

-- Index: native_transfers_from_idx

CREATE INDEX native_transfers_from_idx ON ONLY native_transfers USING btree (chain_id, from_address, block_number DESC, transaction_index DESC, log_index DESC) WHERE canonical;

-- Index: native_transfers_default_chain_id_from_address_block_number_idx

CREATE INDEX native_transfers_default_chain_id_from_address_block_number_idx ON native_transfers_default USING btree (chain_id, from_address, block_number DESC, transaction_index DESC, log_index DESC) WHERE canonical;

-- Index: native_transfers_to_idx

CREATE INDEX native_transfers_to_idx ON ONLY native_transfers USING btree (chain_id, to_address, block_number DESC, transaction_index DESC, log_index DESC) WHERE canonical;

-- Index: native_transfers_default_chain_id_to_address_block_number_t_idx

CREATE INDEX native_transfers_default_chain_id_to_address_block_number_t_idx ON native_transfers_default USING btree (chain_id, to_address, block_number DESC, transaction_index DESC, log_index DESC) WHERE canonical;

-- Index: native_transfers_transaction_idx

CREATE INDEX native_transfers_transaction_idx ON ONLY native_transfers USING btree (chain_id, transaction_hash, log_index) WHERE canonical;

-- Index: native_transfers_default_chain_id_transaction_hash_log_inde_idx

CREATE INDEX native_transfers_default_chain_id_transaction_hash_log_inde_idx ON native_transfers_default USING btree (chain_id, transaction_hash, log_index) WHERE canonical;

-- Index: native_transfers_p_0_1000000_chain_id_from_address_block_nu_idx

CREATE INDEX native_transfers_p_0_1000000_chain_id_from_address_block_nu_idx ON native_transfers_p_0_1000000 USING btree (chain_id, from_address, block_number DESC, transaction_index DESC, log_index DESC) WHERE canonical;

-- Index: native_transfers_p_0_1000000_chain_id_to_address_block_numb_idx

CREATE INDEX native_transfers_p_0_1000000_chain_id_to_address_block_numb_idx ON native_transfers_p_0_1000000 USING btree (chain_id, to_address, block_number DESC, transaction_index DESC, log_index DESC) WHERE canonical;

-- Index: native_transfers_p_0_1000000_chain_id_transaction_hash_log__idx

CREATE INDEX native_transfers_p_0_1000000_chain_id_transaction_hash_log__idx ON native_transfers_p_0_1000000 USING btree (chain_id, transaction_hash, log_index) WHERE canonical;

-- Index: nft_metadata_source_candidate_idx

CREATE INDEX nft_metadata_source_candidate_idx ON nft_metadata_source_observations USING btree (chain_id, token_address, token_id, block_number DESC, block_hash);

-- Index: nft_metadata_update_candidate_idx

CREATE INDEX nft_metadata_update_candidate_idx ON nft_metadata_update_observations USING btree (chain_id, token_address, block_number DESC, block_hash, from_token_id, to_token_id) WHERE (state = 'accepted'::text);

-- Index: normalized_traces_created_idx

CREATE INDEX normalized_traces_created_idx ON ONLY normalized_traces USING btree (chain_id, created_address, block_number DESC) WHERE (canonical AND (created_address IS NOT NULL));

-- Index: normalized_traces_default_chain_id_created_address_block_nu_idx

CREATE INDEX normalized_traces_default_chain_id_created_address_block_nu_idx ON normalized_traces_default USING btree (chain_id, created_address, block_number DESC) WHERE (canonical AND (created_address IS NOT NULL));

-- Index: normalized_traces_from_idx

CREATE INDEX normalized_traces_from_idx ON ONLY normalized_traces USING btree (chain_id, from_address, block_number DESC) WHERE canonical;

-- Index: normalized_traces_default_chain_id_from_address_block_numbe_idx

CREATE INDEX normalized_traces_default_chain_id_from_address_block_numbe_idx ON normalized_traces_default USING btree (chain_id, from_address, block_number DESC) WHERE canonical;

-- Index: normalized_traces_to_idx

CREATE INDEX normalized_traces_to_idx ON ONLY normalized_traces USING btree (chain_id, to_address, block_number DESC) WHERE canonical;

-- Index: normalized_traces_default_chain_id_to_address_block_number_idx

CREATE INDEX normalized_traces_default_chain_id_to_address_block_number_idx ON normalized_traces_default USING btree (chain_id, to_address, block_number DESC) WHERE canonical;

-- Index: normalized_traces_tx_idx

CREATE INDEX normalized_traces_tx_idx ON ONLY normalized_traces USING btree (chain_id, transaction_hash, trace_path);

-- Index: normalized_traces_default_chain_id_transaction_hash_trace_p_idx

CREATE INDEX normalized_traces_default_chain_id_transaction_hash_trace_p_idx ON normalized_traces_default USING btree (chain_id, transaction_hash, trace_path);

-- Index: normalized_traces_p_0_1000000_chain_id_created_address_bloc_idx

CREATE INDEX normalized_traces_p_0_1000000_chain_id_created_address_bloc_idx ON normalized_traces_p_0_1000000 USING btree (chain_id, created_address, block_number DESC) WHERE (canonical AND (created_address IS NOT NULL));

-- Index: normalized_traces_p_0_1000000_chain_id_from_address_block_n_idx

CREATE INDEX normalized_traces_p_0_1000000_chain_id_from_address_block_n_idx ON normalized_traces_p_0_1000000 USING btree (chain_id, from_address, block_number DESC) WHERE canonical;

-- Index: normalized_traces_p_0_1000000_chain_id_to_address_block_num_idx

CREATE INDEX normalized_traces_p_0_1000000_chain_id_to_address_block_num_idx ON normalized_traces_p_0_1000000 USING btree (chain_id, to_address, block_number DESC) WHERE canonical;

-- Index: normalized_traces_p_0_1000000_chain_id_transaction_hash_tra_idx

CREATE INDEX normalized_traces_p_0_1000000_chain_id_transaction_hash_tra_idx ON normalized_traces_p_0_1000000 USING btree (chain_id, transaction_hash, trace_path);

-- Index: proxy_artifact_resolutions_beacon_idx

CREATE INDEX proxy_artifact_resolutions_beacon_idx ON proxy_artifact_resolutions USING btree (chain_id, beacon_address, observation_block_hash, id DESC) WHERE (beacon_address IS NOT NULL);

-- Index: proxy_artifact_resolutions_current_idx

CREATE INDEX proxy_artifact_resolutions_current_idx ON proxy_artifact_resolutions USING btree (chain_id, proxy_address, id DESC);

-- Index: proxy_detection_evidence_current_idx

CREATE INDEX proxy_detection_evidence_current_idx ON proxy_detection_evidence USING btree (chain_id, address, block_number DESC, block_hash DESC) WHERE (canonical AND (stage_version = 2));

-- Index: proxy_detection_evidence_negative_shadow_idx

CREATE INDEX proxy_detection_evidence_negative_shadow_idx ON proxy_detection_evidence USING btree (chain_id, address, candidate_kind, code_hash, block_number DESC, block_hash DESC) WHERE (canonical AND (stage_version = 2));

-- Index: proxy_history_epochs_latest_idx

CREATE INDEX proxy_history_epochs_latest_idx ON proxy_history_epochs USING btree (chain_id, epoch_id DESC) INCLUDE (block_number, block_hash);

-- Index: proxy_history_epochs_snapshot_idx

CREATE INDEX proxy_history_epochs_snapshot_idx ON proxy_history_epochs USING btree (chain_id, block_number, epoch_id DESC);

-- Index: proxy_history_idx

CREATE INDEX proxy_history_idx ON proxy_observations USING btree (chain_id, proxy_address, block_number DESC) WHERE canonical;

-- Index: proxy_initialization_events_contract_idx

CREATE INDEX proxy_initialization_events_contract_idx ON proxy_initialization_events USING btree (chain_id, contract_address, block_number DESC, log_index DESC) WHERE (canonical AND (stage_version = 2));

-- Index: proxy_interaction_coverage_ranges_end_idx

CREATE INDEX proxy_interaction_coverage_ranges_end_idx ON proxy_interaction_coverage_ranges USING btree (chain_id, end_block, start_block);

-- Index: proxy_observations_v2_beacon_idx

CREATE INDEX proxy_observations_v2_beacon_idx ON proxy_observations USING btree (chain_id, beacon_address, block_number DESC, block_hash DESC) WHERE (canonical AND (stage_version = 2) AND (beacon_address IS NOT NULL));

-- Index: proxy_observations_v2_history_idx

CREATE INDEX proxy_observations_v2_history_idx ON proxy_observations USING btree (chain_id, proxy_address, block_number DESC, block_hash DESC) WHERE (canonical AND (stage_version = 2));

-- Index: proxy_replay_targets_verification_source_idx

CREATE INDEX proxy_replay_targets_verification_source_idx ON proxy_replay_targets USING btree (((source_verification_job_id)::text), chain_id, block_hash);

-- Index: proxy_upgrade_events_emitter_idx

CREATE INDEX proxy_upgrade_events_emitter_idx ON proxy_upgrade_events USING btree (chain_id, emitter_address, block_number DESC, log_index DESC) WHERE (canonical AND (stage_version = 2));

-- Index: receipts_contract_address_idx

CREATE INDEX receipts_contract_address_idx ON ONLY receipts USING btree (chain_id, lower((raw ->> 'contractAddress'::text)), block_number DESC, tx_index DESC) WHERE ((raw ->> 'contractAddress'::text) IS NOT NULL);

-- Index: receipts_default_chain_id_lower_block_number_tx_index_idx

CREATE INDEX receipts_default_chain_id_lower_block_number_tx_index_idx ON receipts_default USING btree (chain_id, lower((raw ->> 'contractAddress'::text)), block_number DESC, tx_index DESC) WHERE ((raw ->> 'contractAddress'::text) IS NOT NULL);

-- Index: receipts_p_0_1000000_chain_id_lower_block_number_tx_index_idx

CREATE INDEX receipts_p_0_1000000_chain_id_lower_block_number_tx_index_idx ON receipts_p_0_1000000 USING btree (chain_id, lower((raw ->> 'contractAddress'::text)), block_number DESC, tx_index DESC) WHERE ((raw ->> 'contractAddress'::text) IS NOT NULL);

-- Index: repair_requests_active_metrics_idx

CREATE INDEX repair_requests_active_metrics_idx ON repair_requests USING btree (chain_id, operation, status) WHERE (status = ANY (ARRAY['queued'::text, 'running'::text]));

-- Index: repair_requests_pending_idx

CREATE INDEX repair_requests_pending_idx ON repair_requests USING btree (chain_id, requested_at, id) WHERE (status = 'queued'::text);

-- Index: runtime_events_chain_id_idx

CREATE INDEX runtime_events_chain_id_idx ON runtime_events USING btree (chain_id, id);

-- Index: search_catalog_documents_generation_idx

CREATE INDEX search_catalog_documents_generation_idx ON search_catalog_documents USING btree (chain_id, valid_from_generation, valid_to_generation, source_kind);

-- Index: search_catalog_documents_terms_idx

CREATE INDEX search_catalog_documents_terms_idx ON search_catalog_documents USING gin (exact_terms);

-- Index: token_balance_deltas_owner_idx

CREATE INDEX token_balance_deltas_owner_idx ON ONLY token_balance_deltas USING btree (chain_id, owner_address, token_address, token_id, block_number) WHERE canonical;

-- Index: token_balance_deltas_default_chain_id_owner_address_token_a_idx

CREATE INDEX token_balance_deltas_default_chain_id_owner_address_token_a_idx ON token_balance_deltas_default USING btree (chain_id, owner_address, token_address, token_id, block_number) WHERE canonical;

-- Index: token_balance_deltas_p_0_1000_chain_id_owner_address_token__idx

CREATE INDEX token_balance_deltas_p_0_1000_chain_id_owner_address_token__idx ON token_balance_deltas_p_0_1000000 USING btree (chain_id, owner_address, token_address, token_id, block_number) WHERE canonical;

-- Index: token_contracts_canonical_lookup_idx

CREATE INDEX token_contracts_canonical_lookup_idx ON token_contracts USING btree (chain_id, address, observed_block_number DESC, observed_block_hash, code_hash);

-- Index: token_events_from_idx

CREATE INDEX token_events_from_idx ON ONLY token_events USING btree (chain_id, from_address, block_number DESC) WHERE canonical;

-- Index: token_events_default_chain_id_from_address_block_number_idx

CREATE INDEX token_events_default_chain_id_from_address_block_number_idx ON token_events_default USING btree (chain_id, from_address, block_number DESC) WHERE canonical;

-- Index: token_events_to_idx

CREATE INDEX token_events_to_idx ON ONLY token_events USING btree (chain_id, to_address, block_number DESC) WHERE canonical;

-- Index: token_events_default_chain_id_to_address_block_number_idx

CREATE INDEX token_events_default_chain_id_to_address_block_number_idx ON token_events_default USING btree (chain_id, to_address, block_number DESC) WHERE canonical;

-- Index: token_events_token_idx

CREATE INDEX token_events_token_idx ON ONLY token_events USING btree (chain_id, token_address, block_number DESC, log_index DESC);

-- Index: token_events_default_chain_id_token_address_block_number_lo_idx

CREATE INDEX token_events_default_chain_id_token_address_block_number_lo_idx ON token_events_default USING btree (chain_id, token_address, block_number DESC, log_index DESC);

-- Index: token_events_metadata_known_id_idx

CREATE INDEX token_events_metadata_known_id_idx ON ONLY token_events USING btree (chain_id, token_address, token_id, block_number DESC, block_hash) WHERE ((token_id IS NOT NULL) AND (standard = ANY (ARRAY['erc721'::text, 'erc1155'::text])));

-- Index: token_events_default_chain_id_token_address_token_id_block__idx

CREATE INDEX token_events_default_chain_id_token_address_token_id_block__idx ON token_events_default USING btree (chain_id, token_address, token_id, block_number DESC, block_hash) WHERE ((token_id IS NOT NULL) AND (standard = ANY (ARRAY['erc721'::text, 'erc1155'::text])));

-- Index: token_events_p_0_1000000_chain_id_from_address_block_number_idx

CREATE INDEX token_events_p_0_1000000_chain_id_from_address_block_number_idx ON token_events_p_0_1000000 USING btree (chain_id, from_address, block_number DESC) WHERE canonical;

-- Index: token_events_p_0_1000000_chain_id_to_address_block_number_idx

CREATE INDEX token_events_p_0_1000000_chain_id_to_address_block_number_idx ON token_events_p_0_1000000 USING btree (chain_id, to_address, block_number DESC) WHERE canonical;

-- Index: token_events_p_0_1000000_chain_id_token_address_block_numbe_idx

CREATE INDEX token_events_p_0_1000000_chain_id_token_address_block_numbe_idx ON token_events_p_0_1000000 USING btree (chain_id, token_address, block_number DESC, log_index DESC);

-- Index: token_events_p_0_1000000_chain_id_token_address_token_id_bl_idx

CREATE INDEX token_events_p_0_1000000_chain_id_token_address_token_id_bl_idx ON token_events_p_0_1000000 USING btree (chain_id, token_address, token_id, block_number DESC, block_hash) WHERE ((token_id IS NOT NULL) AND (standard = ANY (ARRAY['erc721'::text, 'erc1155'::text])));

-- Index: trace_log_attributions_frame_idx

CREATE INDEX trace_log_attributions_frame_idx ON ONLY trace_log_attributions USING btree (chain_id, transaction_hash, trace_path, log_index) WHERE canonical;

-- Index: trace_log_attributions_defaul_chain_id_transaction_hash_tra_idx

CREATE INDEX trace_log_attributions_defaul_chain_id_transaction_hash_tra_idx ON trace_log_attributions_default USING btree (chain_id, transaction_hash, trace_path, log_index) WHERE canonical;

-- Index: trace_log_attributions_p_0_10_chain_id_transaction_hash_tra_idx

CREATE INDEX trace_log_attributions_p_0_10_chain_id_transaction_hash_tra_idx ON trace_log_attributions_p_0_1000000 USING btree (chain_id, transaction_hash, trace_path, log_index) WHERE canonical;

-- Index: transaction_effective_execution_identities_code_idx

CREATE INDEX transaction_effective_execution_identities_code_idx ON ONLY transaction_effective_execution_identities USING btree (chain_id, execution_address, execution_code_hash, block_number DESC, transaction_index DESC) WHERE (canonical AND (execution_address IS NOT NULL));

-- Index: transaction_effective_executi_chain_id_execution_address_e_idx1

CREATE INDEX transaction_effective_executi_chain_id_execution_address_e_idx1 ON transaction_effective_execution_identities_default USING btree (chain_id, execution_address, execution_code_hash, block_number DESC, transaction_index DESC) WHERE (canonical AND (execution_address IS NOT NULL));

-- Index: transaction_effective_executi_chain_id_execution_address_ex_idx

CREATE INDEX transaction_effective_executi_chain_id_execution_address_ex_idx ON transaction_effective_execution_identities_p_0_1000000 USING btree (chain_id, execution_address, execution_code_hash, block_number DESC, transaction_index DESC) WHERE (canonical AND (execution_address IS NOT NULL));

-- Index: transaction_effective_execution_identities_tx_idx

CREATE INDEX transaction_effective_execution_identities_tx_idx ON ONLY transaction_effective_execution_identities USING btree (chain_id, transaction_hash, context_address) WHERE canonical;

-- Index: transaction_effective_executi_chain_id_transaction_hash_co_idx1

CREATE INDEX transaction_effective_executi_chain_id_transaction_hash_co_idx1 ON transaction_effective_execution_identities_default USING btree (chain_id, transaction_hash, context_address) WHERE canonical;

-- Index: transaction_effective_executi_chain_id_transaction_hash_con_idx

CREATE INDEX transaction_effective_executi_chain_id_transaction_hash_con_idx ON transaction_effective_execution_identities_p_0_1000000 USING btree (chain_id, transaction_hash, context_address) WHERE canonical;

-- Index: transaction_execution_code_resolutions_execution_idx

CREATE INDEX transaction_execution_code_resolutions_execution_idx ON ONLY transaction_execution_code_resolutions USING btree (chain_id, execution_address, block_number DESC) WHERE (canonical AND (execution_address IS NOT NULL));

-- Index: transaction_execution_code_re_chain_id_execution_address_b_idx1

CREATE INDEX transaction_execution_code_re_chain_id_execution_address_b_idx1 ON transaction_execution_code_resolutions_default USING btree (chain_id, execution_address, block_number DESC) WHERE (canonical AND (execution_address IS NOT NULL));

-- Index: transaction_execution_code_re_chain_id_execution_address_bl_idx

CREATE INDEX transaction_execution_code_re_chain_id_execution_address_bl_idx ON transaction_execution_code_resolutions_p_0_1000000 USING btree (chain_id, execution_address, block_number DESC) WHERE (canonical AND (execution_address IS NOT NULL));

-- Index: transaction_execution_code_resolutions_tx_idx

CREATE INDEX transaction_execution_code_resolutions_tx_idx ON ONLY transaction_execution_code_resolutions USING btree (chain_id, transaction_hash, context_address) WHERE canonical;

-- Index: transaction_execution_code_re_chain_id_transaction_hash_co_idx1

CREATE INDEX transaction_execution_code_re_chain_id_transaction_hash_co_idx1 ON transaction_execution_code_resolutions_default USING btree (chain_id, transaction_hash, context_address) WHERE canonical;

-- Index: transaction_execution_code_re_chain_id_transaction_hash_con_idx

CREATE INDEX transaction_execution_code_re_chain_id_transaction_hash_con_idx ON transaction_execution_code_resolutions_p_0_1000000 USING btree (chain_id, transaction_hash, context_address) WHERE canonical;

-- Index: transaction_inclusions_to_address_idx

CREATE INDEX transaction_inclusions_to_address_idx ON ONLY transaction_inclusions USING btree (chain_id, lower((raw ->> 'to'::text)), block_number DESC, tx_index DESC);

-- Index: transaction_inclusions_defaul_chain_id_lower_block_number__idx1

CREATE INDEX transaction_inclusions_defaul_chain_id_lower_block_number__idx1 ON transaction_inclusions_default USING btree (chain_id, lower((raw ->> 'to'::text)), block_number DESC, tx_index DESC);

-- Index: transaction_inclusions_from_address_idx

CREATE INDEX transaction_inclusions_from_address_idx ON ONLY transaction_inclusions USING btree (chain_id, lower((raw ->> 'from'::text)), block_number DESC, tx_index DESC);

-- Index: transaction_inclusions_defaul_chain_id_lower_block_number_t_idx

CREATE INDEX transaction_inclusions_defaul_chain_id_lower_block_number_t_idx ON transaction_inclusions_default USING btree (chain_id, lower((raw ->> 'from'::text)), block_number DESC, tx_index DESC);

-- Index: transaction_inclusions_p_0_10_chain_id_lower_block_number__idx1

CREATE INDEX transaction_inclusions_p_0_10_chain_id_lower_block_number__idx1 ON transaction_inclusions_p_0_1000000 USING btree (chain_id, lower((raw ->> 'to'::text)), block_number DESC, tx_index DESC);

-- Index: transaction_inclusions_p_0_10_chain_id_lower_block_number_t_idx

CREATE INDEX transaction_inclusions_p_0_10_chain_id_lower_block_number_t_idx ON transaction_inclusions_p_0_1000000 USING btree (chain_id, lower((raw ->> 'from'::text)), block_number DESC, tx_index DESC);

-- Index: transaction_state_changes_address_idx

CREATE INDEX transaction_state_changes_address_idx ON ONLY transaction_state_changes USING btree (chain_id, address, block_number DESC) WHERE canonical;

-- Index: transaction_state_changes_code_epoch_idx

CREATE INDEX transaction_state_changes_code_epoch_idx ON ONLY transaction_state_changes USING btree (chain_id, address, block_number DESC) INCLUDE (block_hash) WHERE (canonical AND (field_kind = 'code'::text));

-- Index: transaction_state_changes_def_chain_id_address_block_numbe_idx1

CREATE INDEX transaction_state_changes_def_chain_id_address_block_numbe_idx1 ON transaction_state_changes_default USING btree (chain_id, address, block_number DESC) INCLUDE (block_hash) WHERE (canonical AND (field_kind = 'code'::text));

-- Index: transaction_state_changes_def_chain_id_address_block_number_idx

CREATE INDEX transaction_state_changes_def_chain_id_address_block_number_idx ON transaction_state_changes_default USING btree (chain_id, address, block_number DESC) WHERE canonical;

-- Index: transaction_state_changes_tx_idx

CREATE INDEX transaction_state_changes_tx_idx ON ONLY transaction_state_changes USING btree (chain_id, transaction_hash, block_number DESC, address, field_kind, storage_key);

-- Index: transaction_state_changes_def_chain_id_transaction_hash_blo_idx

CREATE INDEX transaction_state_changes_def_chain_id_transaction_hash_blo_idx ON transaction_state_changes_default USING btree (chain_id, transaction_hash, block_number DESC, address, field_kind, storage_key);

-- Index: transaction_state_changes_p_0_chain_id_address_block_numbe_idx1

CREATE INDEX transaction_state_changes_p_0_chain_id_address_block_numbe_idx1 ON transaction_state_changes_p_0_1000000 USING btree (chain_id, address, block_number DESC) INCLUDE (block_hash) WHERE (canonical AND (field_kind = 'code'::text));

-- Index: transaction_state_changes_p_0_chain_id_address_block_number_idx

CREATE INDEX transaction_state_changes_p_0_chain_id_address_block_number_idx ON transaction_state_changes_p_0_1000000 USING btree (chain_id, address, block_number DESC) WHERE canonical;

-- Index: transaction_state_changes_p_0_chain_id_transaction_hash_blo_idx

CREATE INDEX transaction_state_changes_p_0_chain_id_transaction_hash_blo_idx ON transaction_state_changes_p_0_1000000 USING btree (chain_id, transaction_hash, block_number DESC, address, field_kind, storage_key);

-- Index: transactional_outbox_pending_idx

CREATE INDEX transactional_outbox_pending_idx ON transactional_outbox USING btree (available_at, id) WHERE (published_at IS NULL);

-- Index: user_sessions_active_user_idx

CREATE INDEX user_sessions_active_user_idx ON user_sessions USING btree (user_id, expires_at, id) WHERE (revoked_at IS NULL);

-- Index: user_sessions_cleanup_idx

CREATE INDEX user_sessions_cleanup_idx ON user_sessions USING btree (expires_at, id);

-- Index: users_chain_created_idx

CREATE INDEX users_chain_created_idx ON users USING btree (chain_id, created_at DESC, id DESC);

-- Index: uups_implementation_observation_generations_job_idx

CREATE INDEX uups_implementation_observation_generations_job_idx ON uups_implementation_observation_generations USING btree (durable_job_id, job_generation, id);

-- Index: uups_implementation_observations_current_idx

CREATE INDEX uups_implementation_observations_current_idx ON uups_implementation_observations USING btree (chain_id, implementation_address, implementation_code_hash, block_number DESC, block_hash DESC) WHERE (canonical AND (probe_state = 'compatible'::text));

-- Index: uups_implementation_observations_latest_idx

CREATE INDEX uups_implementation_observations_latest_idx ON uups_implementation_observations USING btree (chain_id, implementation_address, implementation_code_hash, block_number DESC, block_hash DESC) WHERE canonical;

-- Index: uups_implementation_observations_verification_idx

CREATE INDEX uups_implementation_observations_verification_idx ON uups_implementation_observations USING btree (verification_job_id, chain_id, block_hash);

-- Index: verification_jobs_active_request_key

CREATE UNIQUE INDEX verification_jobs_active_request_key ON verification_jobs USING btree (request_digest) WHERE (status = ANY (ARRAY['queued'::text, 'running'::text, 'succeeded'::text]));

-- Index: verification_jobs_claim_idx

CREATE INDEX verification_jobs_claim_idx ON verification_jobs USING btree (status, created_at, id) WHERE (status = ANY (ARRAY['queued'::text, 'running'::text]));

-- Index: verification_jobs_target_idx

CREATE INDEX verification_jobs_target_idx ON verification_jobs USING btree (chain_id, address, code_hash, created_at DESC) WHERE (kind = 'address'::text);

-- Index: verified_contract_proxy_artifacts_current_idx

CREATE INDEX verified_contract_proxy_artifacts_current_idx ON verified_contract_proxy_artifacts USING btree (chain_id, address, valid_from_block DESC, code_hash);

-- Index: verified_contracts_code_hash_artifact_idx

CREATE INDEX verified_contracts_code_hash_artifact_idx ON verified_contracts USING btree (chain_id, code_hash, ((abi IS NOT NULL)) DESC, ((match_type = 'full'::text)) DESC, request_digest, verification_job_id, address, valid_from_block DESC);

-- Index: verified_function_selector_sets_identity_idx

CREATE INDEX verified_function_selector_sets_identity_idx ON verified_function_selector_sets USING btree (chain_id, code_hash, address, valid_from_block, verification_job_id);

-- Index: verified_function_selectors_lookup_idx

CREATE INDEX verified_function_selectors_lookup_idx ON verified_function_selectors USING btree (chain_id, code_hash, selector, verification_job_id, signature);

-- Index: verified_proxy_bindings_current_idx

CREATE INDEX verified_proxy_bindings_current_idx ON verified_proxy_bindings USING btree (chain_id, proxy_address, observation_block_number DESC);

-- Index: verified_proxy_bindings_uups_generation_idx

CREATE INDEX verified_proxy_bindings_uups_generation_idx ON verified_proxy_bindings USING btree (uups_generation_id) WHERE (uups_generation_id IS NOT NULL);

-- Index: watch_notification_work_pending

CREATE INDEX watch_notification_work_pending ON watch_notification_work USING btree (chain_id, block_number, block_hash) WHERE (generation > done_generation);

-- Index: watch_notifications_block

CREATE INDEX watch_notifications_block ON watch_notifications USING btree (chain_id, block_hash, source_key, watch_id);

-- Index: watch_notifications_expiry

CREATE INDEX watch_notifications_expiry ON watch_notifications USING btree (created_at, id);

-- Index: watch_notifications_owner_page

CREATE INDEX watch_notifications_owner_page ON watch_notifications USING btree (user_id, id DESC);

-- Index: watch_notifications_unread

CREATE INDEX watch_notifications_unread ON watch_notifications USING btree (user_id, id) WHERE (read_at IS NULL);

-- Index: withdrawals_address_index_idx

CREATE INDEX withdrawals_address_index_idx ON ONLY withdrawals USING btree (chain_id, address, withdrawal_index DESC);

-- Index: withdrawals_default_chain_id_address_withdrawal_index_idx

CREATE INDEX withdrawals_default_chain_id_address_withdrawal_index_idx ON withdrawals_default USING btree (chain_id, address, withdrawal_index DESC);

-- Index: withdrawals_p_0_1000000_chain_id_address_withdrawal_index_idx

CREATE INDEX withdrawals_p_0_1000000_chain_id_address_withdrawal_index_idx ON withdrawals_p_0_1000000 USING btree (chain_id, address, withdrawal_index DESC);

-- Index Attach: abi_decodings_default_chain_id_target_address_target_code_h_idx

ALTER INDEX abi_decodings_target_idx ATTACH PARTITION abi_decodings_default_chain_id_target_address_target_code_h_idx;

-- Index Attach: abi_decodings_default_chain_id_transaction_hash_block_numbe_idx

ALTER INDEX abi_decodings_tx_idx ATTACH PARTITION abi_decodings_default_chain_id_transaction_hash_block_numbe_idx;

-- Index Attach: abi_decodings_default_pkey

ALTER INDEX abi_decodings_pkey ATTACH PARTITION abi_decodings_default_pkey;

-- Index Attach: abi_decodings_p_0_1000000_chain_id_target_address_target_co_idx

ALTER INDEX abi_decodings_target_idx ATTACH PARTITION abi_decodings_p_0_1000000_chain_id_target_address_target_co_idx;

-- Index Attach: abi_decodings_p_0_1000000_chain_id_transaction_hash_block_n_idx

ALTER INDEX abi_decodings_tx_idx ATTACH PARTITION abi_decodings_p_0_1000000_chain_id_transaction_hash_block_n_idx;

-- Index Attach: abi_decodings_p_0_1000000_pkey

ALTER INDEX abi_decodings_pkey ATTACH PARTITION abi_decodings_p_0_1000000_pkey;

-- Index Attach: address_activities_default_chain_id_address_block_number_ac_idx

ALTER INDEX address_activities_address_idx ATTACH PARTITION address_activities_default_chain_id_address_block_number_ac_idx;

-- Index Attach: address_activities_default_pkey

ALTER INDEX address_activities_pkey ATTACH PARTITION address_activities_default_pkey;

-- Index Attach: address_activities_p_0_1000000_pkey

ALTER INDEX address_activities_pkey ATTACH PARTITION address_activities_p_0_1000000_pkey;

-- Index Attach: address_activities_p_0_100000_chain_id_address_block_number_idx

ALTER INDEX address_activities_address_idx ATTACH PARTITION address_activities_p_0_100000_chain_id_address_block_number_idx;

-- Index Attach: eip7702_authorizations_defaul_chain_id_authority_block_numb_idx

ALTER INDEX eip7702_authorizations_authority_idx ATTACH PARTITION eip7702_authorizations_defaul_chain_id_authority_block_numb_idx;

-- Index Attach: eip7702_authorizations_defaul_chain_id_transaction_hash_aut_idx

ALTER INDEX eip7702_authorizations_tx_idx ATTACH PARTITION eip7702_authorizations_defaul_chain_id_transaction_hash_aut_idx;

-- Index Attach: eip7702_authorizations_default_pkey

ALTER INDEX eip7702_authorizations_pkey ATTACH PARTITION eip7702_authorizations_default_pkey;

-- Index Attach: eip7702_authorizations_p_0_1000000_pkey

ALTER INDEX eip7702_authorizations_pkey ATTACH PARTITION eip7702_authorizations_p_0_1000000_pkey;

-- Index Attach: eip7702_authorizations_p_0_10_chain_id_authority_block_numb_idx

ALTER INDEX eip7702_authorizations_authority_idx ATTACH PARTITION eip7702_authorizations_p_0_10_chain_id_authority_block_numb_idx;

-- Index Attach: eip7702_authorizations_p_0_10_chain_id_transaction_hash_aut_idx

ALTER INDEX eip7702_authorizations_tx_idx ATTACH PARTITION eip7702_authorizations_p_0_10_chain_id_transaction_hash_aut_idx;

-- Index Attach: erc20_holder_balances_default_chain_id_token_address_holder_idx

ALTER INDEX erc20_holder_balances_page_idx ATTACH PARTITION erc20_holder_balances_default_chain_id_token_address_holder_idx;

-- Index Attach: erc20_holder_balances_default_pkey

ALTER INDEX erc20_holder_balances_pkey ATTACH PARTITION erc20_holder_balances_default_pkey;

-- Index Attach: erc20_holder_balances_p_0_1000000_pkey

ALTER INDEX erc20_holder_balances_pkey ATTACH PARTITION erc20_holder_balances_p_0_1000000_pkey;

-- Index Attach: erc20_holder_balances_p_0_100_chain_id_token_address_holder_idx

ALTER INDEX erc20_holder_balances_page_idx ATTACH PARTITION erc20_holder_balances_p_0_100_chain_id_token_address_holder_idx;

-- Index Attach: erc20_holder_snapshots_defaul_chain_id_token_address_block__idx

ALTER INDEX erc20_holder_snapshots_current_idx ATTACH PARTITION erc20_holder_snapshots_defaul_chain_id_token_address_block__idx;

-- Index Attach: erc20_holder_snapshots_default_pkey

ALTER INDEX erc20_holder_snapshots_pkey ATTACH PARTITION erc20_holder_snapshots_default_pkey;

-- Index Attach: erc20_holder_snapshots_p_0_1000000_pkey

ALTER INDEX erc20_holder_snapshots_pkey ATTACH PARTITION erc20_holder_snapshots_p_0_1000000_pkey;

-- Index Attach: erc20_holder_snapshots_p_0_10_chain_id_token_address_block__idx

ALTER INDEX erc20_holder_snapshots_current_idx ATTACH PARTITION erc20_holder_snapshots_p_0_10_chain_id_token_address_block__idx;

-- Index Attach: erc4337_user_operation_events_default_pkey

ALTER INDEX erc4337_user_operation_events_pkey ATTACH PARTITION erc4337_user_operation_events_default_pkey;

-- Index Attach: erc4337_user_operation_events_p_0_1000000_pkey

ALTER INDEX erc4337_user_operation_events_pkey ATTACH PARTITION erc4337_user_operation_events_p_0_1000000_pkey;

-- Index Attach: erc4337_user_operation_partic_chain_id_configuration_diges_idx1

ALTER INDEX erc4337_user_operation_participants_address_idx ATTACH PARTITION erc4337_user_operation_partic_chain_id_configuration_diges_idx1;

-- Index Attach: erc4337_user_operation_partic_chain_id_configuration_digest_idx

ALTER INDEX erc4337_user_operation_participants_address_idx ATTACH PARTITION erc4337_user_operation_partic_chain_id_configuration_digest_idx;

-- Index Attach: erc4337_user_operation_participants_default_pkey

ALTER INDEX erc4337_user_operation_participants_pkey ATTACH PARTITION erc4337_user_operation_participants_default_pkey;

-- Index Attach: erc4337_user_operation_participants_p_0_1000000_pkey

ALTER INDEX erc4337_user_operation_participants_pkey ATTACH PARTITION erc4337_user_operation_participants_p_0_1000000_pkey;

-- Index Attach: erc4337_user_operations_defau_chain_id_configuration_diges_idx1

ALTER INDEX erc4337_user_operations_global_idx ATTACH PARTITION erc4337_user_operations_defau_chain_id_configuration_diges_idx1;

-- Index Attach: erc4337_user_operations_defau_chain_id_configuration_diges_idx2

ALTER INDEX erc4337_user_operations_transaction_idx ATTACH PARTITION erc4337_user_operations_defau_chain_id_configuration_diges_idx2;

-- Index Attach: erc4337_user_operations_defau_chain_id_configuration_digest_idx

ALTER INDEX erc4337_user_operations_canonical_hash_idx ATTACH PARTITION erc4337_user_operations_defau_chain_id_configuration_digest_idx;

-- Index Attach: erc4337_user_operations_default_pkey

ALTER INDEX erc4337_user_operations_pkey ATTACH PARTITION erc4337_user_operations_default_pkey;

-- Index Attach: erc4337_user_operations_p_0_1000000_pkey

ALTER INDEX erc4337_user_operations_pkey ATTACH PARTITION erc4337_user_operations_p_0_1000000_pkey;

-- Index Attach: erc4337_user_operations_p_0_1_chain_id_configuration_diges_idx1

ALTER INDEX erc4337_user_operations_global_idx ATTACH PARTITION erc4337_user_operations_p_0_1_chain_id_configuration_diges_idx1;

-- Index Attach: erc4337_user_operations_p_0_1_chain_id_configuration_diges_idx2

ALTER INDEX erc4337_user_operations_transaction_idx ATTACH PARTITION erc4337_user_operations_p_0_1_chain_id_configuration_diges_idx2;

-- Index Attach: erc4337_user_operations_p_0_1_chain_id_configuration_digest_idx

ALTER INDEX erc4337_user_operations_canonical_hash_idx ATTACH PARTITION erc4337_user_operations_p_0_1_chain_id_configuration_digest_idx;

-- Index Attach: logs_default_chain_id_address_block_number_idx

ALTER INDEX logs_address_idx ATTACH PARTITION logs_default_chain_id_address_block_number_idx;

-- Index Attach: logs_default_chain_id_topic0_block_number_idx

ALTER INDEX logs_topic0_idx ATTACH PARTITION logs_default_chain_id_topic0_block_number_idx;

-- Index Attach: logs_default_pkey

ALTER INDEX logs_pkey ATTACH PARTITION logs_default_pkey;

-- Index Attach: logs_p_0_1000000_chain_id_address_block_number_idx

ALTER INDEX logs_address_idx ATTACH PARTITION logs_p_0_1000000_chain_id_address_block_number_idx;

-- Index Attach: logs_p_0_1000000_chain_id_topic0_block_number_idx

ALTER INDEX logs_topic0_idx ATTACH PARTITION logs_p_0_1000000_chain_id_topic0_block_number_idx;

-- Index Attach: logs_p_0_1000000_pkey

ALTER INDEX logs_pkey ATTACH PARTITION logs_p_0_1000000_pkey;

-- Index Attach: native_transfers_default_chain_id_from_address_block_number_idx

ALTER INDEX native_transfers_from_idx ATTACH PARTITION native_transfers_default_chain_id_from_address_block_number_idx;

-- Index Attach: native_transfers_default_chain_id_to_address_block_number_t_idx

ALTER INDEX native_transfers_to_idx ATTACH PARTITION native_transfers_default_chain_id_to_address_block_number_t_idx;

-- Index Attach: native_transfers_default_chain_id_transaction_hash_log_inde_idx

ALTER INDEX native_transfers_transaction_idx ATTACH PARTITION native_transfers_default_chain_id_transaction_hash_log_inde_idx;

-- Index Attach: native_transfers_default_pkey

ALTER INDEX native_transfers_pkey ATTACH PARTITION native_transfers_default_pkey;

-- Index Attach: native_transfers_p_0_1000000_chain_id_from_address_block_nu_idx

ALTER INDEX native_transfers_from_idx ATTACH PARTITION native_transfers_p_0_1000000_chain_id_from_address_block_nu_idx;

-- Index Attach: native_transfers_p_0_1000000_chain_id_to_address_block_numb_idx

ALTER INDEX native_transfers_to_idx ATTACH PARTITION native_transfers_p_0_1000000_chain_id_to_address_block_numb_idx;

-- Index Attach: native_transfers_p_0_1000000_chain_id_transaction_hash_log__idx

ALTER INDEX native_transfers_transaction_idx ATTACH PARTITION native_transfers_p_0_1000000_chain_id_transaction_hash_log__idx;

-- Index Attach: native_transfers_p_0_1000000_pkey

ALTER INDEX native_transfers_pkey ATTACH PARTITION native_transfers_p_0_1000000_pkey;

-- Index Attach: normalized_traces_default_chain_id_created_address_block_nu_idx

ALTER INDEX normalized_traces_created_idx ATTACH PARTITION normalized_traces_default_chain_id_created_address_block_nu_idx;

-- Index Attach: normalized_traces_default_chain_id_from_address_block_numbe_idx

ALTER INDEX normalized_traces_from_idx ATTACH PARTITION normalized_traces_default_chain_id_from_address_block_numbe_idx;

-- Index Attach: normalized_traces_default_chain_id_to_address_block_number_idx

ALTER INDEX normalized_traces_to_idx ATTACH PARTITION normalized_traces_default_chain_id_to_address_block_number_idx;

-- Index Attach: normalized_traces_default_chain_id_transaction_hash_trace_p_idx

ALTER INDEX normalized_traces_tx_idx ATTACH PARTITION normalized_traces_default_chain_id_transaction_hash_trace_p_idx;

-- Index Attach: normalized_traces_default_pkey

ALTER INDEX normalized_traces_pkey ATTACH PARTITION normalized_traces_default_pkey;

-- Index Attach: normalized_traces_p_0_1000000_chain_id_created_address_bloc_idx

ALTER INDEX normalized_traces_created_idx ATTACH PARTITION normalized_traces_p_0_1000000_chain_id_created_address_bloc_idx;

-- Index Attach: normalized_traces_p_0_1000000_chain_id_from_address_block_n_idx

ALTER INDEX normalized_traces_from_idx ATTACH PARTITION normalized_traces_p_0_1000000_chain_id_from_address_block_n_idx;

-- Index Attach: normalized_traces_p_0_1000000_chain_id_to_address_block_num_idx

ALTER INDEX normalized_traces_to_idx ATTACH PARTITION normalized_traces_p_0_1000000_chain_id_to_address_block_num_idx;

-- Index Attach: normalized_traces_p_0_1000000_chain_id_transaction_hash_tra_idx

ALTER INDEX normalized_traces_tx_idx ATTACH PARTITION normalized_traces_p_0_1000000_chain_id_transaction_hash_tra_idx;

-- Index Attach: normalized_traces_p_0_1000000_pkey

ALTER INDEX normalized_traces_pkey ATTACH PARTITION normalized_traces_p_0_1000000_pkey;

-- Index Attach: receipts_default_chain_id_block_number_block_hash_tx_hash_key

ALTER INDEX receipts_chain_id_block_number_block_hash_tx_hash_key ATTACH PARTITION receipts_default_chain_id_block_number_block_hash_tx_hash_key;

-- Index Attach: receipts_default_chain_id_lower_block_number_tx_index_idx

ALTER INDEX receipts_contract_address_idx ATTACH PARTITION receipts_default_chain_id_lower_block_number_tx_index_idx;

-- Index Attach: receipts_default_pkey

ALTER INDEX receipts_pkey ATTACH PARTITION receipts_default_pkey;

-- Index Attach: receipts_p_0_1000000_chain_id_block_number_block_hash_tx_ha_key

ALTER INDEX receipts_chain_id_block_number_block_hash_tx_hash_key ATTACH PARTITION receipts_p_0_1000000_chain_id_block_number_block_hash_tx_ha_key;

-- Index Attach: receipts_p_0_1000000_chain_id_lower_block_number_tx_index_idx

ALTER INDEX receipts_contract_address_idx ATTACH PARTITION receipts_p_0_1000000_chain_id_lower_block_number_tx_index_idx;

-- Index Attach: receipts_p_0_1000000_pkey

ALTER INDEX receipts_pkey ATTACH PARTITION receipts_p_0_1000000_pkey;

-- Index Attach: token_balance_deltas_default_chain_id_owner_address_token_a_idx

ALTER INDEX token_balance_deltas_owner_idx ATTACH PARTITION token_balance_deltas_default_chain_id_owner_address_token_a_idx;

-- Index Attach: token_balance_deltas_default_pkey

ALTER INDEX token_balance_deltas_pkey ATTACH PARTITION token_balance_deltas_default_pkey;

-- Index Attach: token_balance_deltas_p_0_1000000_pkey

ALTER INDEX token_balance_deltas_pkey ATTACH PARTITION token_balance_deltas_p_0_1000000_pkey;

-- Index Attach: token_balance_deltas_p_0_1000_chain_id_owner_address_token__idx

ALTER INDEX token_balance_deltas_owner_idx ATTACH PARTITION token_balance_deltas_p_0_1000_chain_id_owner_address_token__idx;

-- Index Attach: token_events_default_chain_id_from_address_block_number_idx

ALTER INDEX token_events_from_idx ATTACH PARTITION token_events_default_chain_id_from_address_block_number_idx;

-- Index Attach: token_events_default_chain_id_to_address_block_number_idx

ALTER INDEX token_events_to_idx ATTACH PARTITION token_events_default_chain_id_to_address_block_number_idx;

-- Index Attach: token_events_default_chain_id_token_address_block_number_lo_idx

ALTER INDEX token_events_token_idx ATTACH PARTITION token_events_default_chain_id_token_address_block_number_lo_idx;

-- Index Attach: token_events_default_chain_id_token_address_token_id_block__idx

ALTER INDEX token_events_metadata_known_id_idx ATTACH PARTITION token_events_default_chain_id_token_address_token_id_block__idx;

-- Index Attach: token_events_default_pkey

ALTER INDEX token_events_pkey ATTACH PARTITION token_events_default_pkey;

-- Index Attach: token_events_p_0_1000000_chain_id_from_address_block_number_idx

ALTER INDEX token_events_from_idx ATTACH PARTITION token_events_p_0_1000000_chain_id_from_address_block_number_idx;

-- Index Attach: token_events_p_0_1000000_chain_id_to_address_block_number_idx

ALTER INDEX token_events_to_idx ATTACH PARTITION token_events_p_0_1000000_chain_id_to_address_block_number_idx;

-- Index Attach: token_events_p_0_1000000_chain_id_token_address_block_numbe_idx

ALTER INDEX token_events_token_idx ATTACH PARTITION token_events_p_0_1000000_chain_id_token_address_block_numbe_idx;

-- Index Attach: token_events_p_0_1000000_chain_id_token_address_token_id_bl_idx

ALTER INDEX token_events_metadata_known_id_idx ATTACH PARTITION token_events_p_0_1000000_chain_id_token_address_token_id_bl_idx;

-- Index Attach: token_events_p_0_1000000_pkey

ALTER INDEX token_events_pkey ATTACH PARTITION token_events_p_0_1000000_pkey;

-- Index Attach: trace_log_attributions_defaul_chain_id_transaction_hash_tra_idx

ALTER INDEX trace_log_attributions_frame_idx ATTACH PARTITION trace_log_attributions_defaul_chain_id_transaction_hash_tra_idx;

-- Index Attach: trace_log_attributions_default_pkey

ALTER INDEX trace_log_attributions_pkey ATTACH PARTITION trace_log_attributions_default_pkey;

-- Index Attach: trace_log_attributions_p_0_1000000_pkey

ALTER INDEX trace_log_attributions_pkey ATTACH PARTITION trace_log_attributions_p_0_1000000_pkey;

-- Index Attach: trace_log_attributions_p_0_10_chain_id_transaction_hash_tra_idx

ALTER INDEX trace_log_attributions_frame_idx ATTACH PARTITION trace_log_attributions_p_0_10_chain_id_transaction_hash_tra_idx;

-- Index Attach: transaction_effective_executi_chain_id_execution_address_e_idx1

ALTER INDEX transaction_effective_execution_identities_code_idx ATTACH PARTITION transaction_effective_executi_chain_id_execution_address_e_idx1;

-- Index Attach: transaction_effective_executi_chain_id_execution_address_ex_idx

ALTER INDEX transaction_effective_execution_identities_code_idx ATTACH PARTITION transaction_effective_executi_chain_id_execution_address_ex_idx;

-- Index Attach: transaction_effective_executi_chain_id_transaction_hash_co_idx1

ALTER INDEX transaction_effective_execution_identities_tx_idx ATTACH PARTITION transaction_effective_executi_chain_id_transaction_hash_co_idx1;

-- Index Attach: transaction_effective_executi_chain_id_transaction_hash_con_idx

ALTER INDEX transaction_effective_execution_identities_tx_idx ATTACH PARTITION transaction_effective_executi_chain_id_transaction_hash_con_idx;

-- Index Attach: transaction_effective_execution_identities_default_pkey

ALTER INDEX transaction_effective_execution_identities_pkey ATTACH PARTITION transaction_effective_execution_identities_default_pkey;

-- Index Attach: transaction_effective_execution_identities_p_0_1000000_pkey

ALTER INDEX transaction_effective_execution_identities_pkey ATTACH PARTITION transaction_effective_execution_identities_p_0_1000000_pkey;

-- Index Attach: transaction_execution_code_re_chain_id_execution_address_b_idx1

ALTER INDEX transaction_execution_code_resolutions_execution_idx ATTACH PARTITION transaction_execution_code_re_chain_id_execution_address_b_idx1;

-- Index Attach: transaction_execution_code_re_chain_id_execution_address_bl_idx

ALTER INDEX transaction_execution_code_resolutions_execution_idx ATTACH PARTITION transaction_execution_code_re_chain_id_execution_address_bl_idx;

-- Index Attach: transaction_execution_code_re_chain_id_transaction_hash_co_idx1

ALTER INDEX transaction_execution_code_resolutions_tx_idx ATTACH PARTITION transaction_execution_code_re_chain_id_transaction_hash_co_idx1;

-- Index Attach: transaction_execution_code_re_chain_id_transaction_hash_con_idx

ALTER INDEX transaction_execution_code_resolutions_tx_idx ATTACH PARTITION transaction_execution_code_re_chain_id_transaction_hash_con_idx;

-- Index Attach: transaction_execution_code_resolutions_default_pkey

ALTER INDEX transaction_execution_code_resolutions_pkey ATTACH PARTITION transaction_execution_code_resolutions_default_pkey;

-- Index Attach: transaction_execution_code_resolutions_p_0_1000000_pkey

ALTER INDEX transaction_execution_code_resolutions_pkey ATTACH PARTITION transaction_execution_code_resolutions_p_0_1000000_pkey;

-- Index Attach: transaction_inclusions_defaul_chain_id_block_number_block__key1

ALTER INDEX transaction_inclusions_position_hash_key ATTACH PARTITION transaction_inclusions_defaul_chain_id_block_number_block__key1;

-- Index Attach: transaction_inclusions_defaul_chain_id_block_number_block_h_key

ALTER INDEX transaction_inclusions_chain_id_block_number_block_hash_tx__key ATTACH PARTITION transaction_inclusions_defaul_chain_id_block_number_block_h_key;

-- Index Attach: transaction_inclusions_defaul_chain_id_lower_block_number__idx1

ALTER INDEX transaction_inclusions_to_address_idx ATTACH PARTITION transaction_inclusions_defaul_chain_id_lower_block_number__idx1;

-- Index Attach: transaction_inclusions_defaul_chain_id_lower_block_number_t_idx

ALTER INDEX transaction_inclusions_from_address_idx ATTACH PARTITION transaction_inclusions_defaul_chain_id_lower_block_number_t_idx;

-- Index Attach: transaction_inclusions_default_pkey

ALTER INDEX transaction_inclusions_pkey ATTACH PARTITION transaction_inclusions_default_pkey;

-- Index Attach: transaction_inclusions_p_0_1000000_pkey

ALTER INDEX transaction_inclusions_pkey ATTACH PARTITION transaction_inclusions_p_0_1000000_pkey;

-- Index Attach: transaction_inclusions_p_0_10_chain_id_block_number_block__key1

ALTER INDEX transaction_inclusions_position_hash_key ATTACH PARTITION transaction_inclusions_p_0_10_chain_id_block_number_block__key1;

-- Index Attach: transaction_inclusions_p_0_10_chain_id_block_number_block_h_key

ALTER INDEX transaction_inclusions_chain_id_block_number_block_hash_tx__key ATTACH PARTITION transaction_inclusions_p_0_10_chain_id_block_number_block_h_key;

-- Index Attach: transaction_inclusions_p_0_10_chain_id_lower_block_number__idx1

ALTER INDEX transaction_inclusions_to_address_idx ATTACH PARTITION transaction_inclusions_p_0_10_chain_id_lower_block_number__idx1;

-- Index Attach: transaction_inclusions_p_0_10_chain_id_lower_block_number_t_idx

ALTER INDEX transaction_inclusions_from_address_idx ATTACH PARTITION transaction_inclusions_p_0_10_chain_id_lower_block_number_t_idx;

-- Index Attach: transaction_state_changes_def_chain_id_address_block_numbe_idx1

ALTER INDEX transaction_state_changes_code_epoch_idx ATTACH PARTITION transaction_state_changes_def_chain_id_address_block_numbe_idx1;

-- Index Attach: transaction_state_changes_def_chain_id_address_block_number_idx

ALTER INDEX transaction_state_changes_address_idx ATTACH PARTITION transaction_state_changes_def_chain_id_address_block_number_idx;

-- Index Attach: transaction_state_changes_def_chain_id_transaction_hash_blo_idx

ALTER INDEX transaction_state_changes_tx_idx ATTACH PARTITION transaction_state_changes_def_chain_id_transaction_hash_blo_idx;

-- Index Attach: transaction_state_changes_default_pkey

ALTER INDEX transaction_state_changes_pkey ATTACH PARTITION transaction_state_changes_default_pkey;

-- Index Attach: transaction_state_changes_p_0_1000000_pkey

ALTER INDEX transaction_state_changes_pkey ATTACH PARTITION transaction_state_changes_p_0_1000000_pkey;

-- Index Attach: transaction_state_changes_p_0_chain_id_address_block_numbe_idx1

ALTER INDEX transaction_state_changes_code_epoch_idx ATTACH PARTITION transaction_state_changes_p_0_chain_id_address_block_numbe_idx1;

-- Index Attach: transaction_state_changes_p_0_chain_id_address_block_number_idx

ALTER INDEX transaction_state_changes_address_idx ATTACH PARTITION transaction_state_changes_p_0_chain_id_address_block_number_idx;

-- Index Attach: transaction_state_changes_p_0_chain_id_transaction_hash_blo_idx

ALTER INDEX transaction_state_changes_tx_idx ATTACH PARTITION transaction_state_changes_p_0_chain_id_transaction_hash_blo_idx;

-- Index Attach: withdrawals_default_chain_id_address_withdrawal_index_idx

ALTER INDEX withdrawals_address_index_idx ATTACH PARTITION withdrawals_default_chain_id_address_withdrawal_index_idx;

-- Index Attach: withdrawals_default_pkey

ALTER INDEX withdrawals_pkey ATTACH PARTITION withdrawals_default_pkey;

-- Index Attach: withdrawals_p_0_1000000_chain_id_address_withdrawal_index_idx

ALTER INDEX withdrawals_address_index_idx ATTACH PARTITION withdrawals_p_0_1000000_chain_id_address_withdrawal_index_idx;

-- Index Attach: withdrawals_p_0_1000000_pkey

ALTER INDEX withdrawals_pkey ATTACH PARTITION withdrawals_p_0_1000000_pkey;

-- Trigger: beacon_observation_generations beacon_observation_generations_immutable

CREATE TRIGGER beacon_observation_generations_immutable BEFORE DELETE OR UPDATE ON beacon_observation_generations FOR EACH ROW EXECUTE FUNCTION reject_proxy_v2_evidence_mutation();

-- Trigger: beacon_observation_generations beacon_observation_generations_source_guard

CREATE TRIGGER beacon_observation_generations_source_guard BEFORE INSERT ON beacon_observation_generations FOR EACH ROW EXECUTE FUNCTION enforce_proxy_observation_generation();

-- Trigger: billing_account_entries billing_account_entries_guard

CREATE TRIGGER billing_account_entries_guard BEFORE DELETE OR UPDATE ON billing_account_entries FOR EACH ROW EXECUTE FUNCTION guard_billing_account_entry_mutation();

-- Trigger: billing_payment_events billing_payment_events_guard

CREATE TRIGGER billing_payment_events_guard BEFORE DELETE OR UPDATE ON billing_payment_events FOR EACH ROW EXECUTE FUNCTION guard_billing_payment_event_mutation();

-- Trigger: billing_payments billing_payments_guard

CREATE TRIGGER billing_payments_guard BEFORE DELETE OR UPDATE ON billing_payments FOR EACH ROW EXECUTE FUNCTION guard_billing_payment_mutation();

-- Trigger: billing_topup_intents billing_topup_intents_guard

CREATE TRIGGER billing_topup_intents_guard BEFORE DELETE OR UPDATE ON billing_topup_intents FOR EACH ROW EXECUTE FUNCTION guard_billing_topup_intent_mutation();

-- Trigger: billing_usage_charges billing_usage_charges_guard

CREATE TRIGGER billing_usage_charges_guard BEFORE DELETE OR UPDATE ON billing_usage_charges FOR EACH ROW EXECUTE FUNCTION guard_billing_usage_charge_mutation();

-- Trigger: block_stage_results block_stage_results_chart_dirty_delete_trigger

CREATE TRIGGER block_stage_results_chart_dirty_delete_trigger AFTER DELETE ON block_stage_results FOR EACH ROW WHEN ((((old.stage = 'stats'::text) AND (old.stage_version = 4)) OR ((old.stage = 'token'::text) AND (old.stage_version = 2)))) EXECUTE FUNCTION mark_chart_rollup_dirty();

-- Trigger: block_stage_results block_stage_results_chart_dirty_insert_trigger

CREATE TRIGGER block_stage_results_chart_dirty_insert_trigger AFTER INSERT ON block_stage_results FOR EACH ROW WHEN ((((new.stage = 'stats'::text) AND (new.stage_version = 4)) OR ((new.stage = 'token'::text) AND (new.stage_version = 2)))) EXECUTE FUNCTION mark_chart_rollup_dirty();

-- Trigger: block_stage_results block_stage_results_chart_dirty_update_trigger

CREATE TRIGGER block_stage_results_chart_dirty_update_trigger AFTER UPDATE ON block_stage_results FOR EACH ROW WHEN ((((new.stage = 'stats'::text) AND (new.stage_version = 4)) OR ((new.stage = 'token'::text) AND (new.stage_version = 2)) OR ((old.stage = 'stats'::text) AND (old.stage_version = 4)) OR ((old.stage = 'token'::text) AND (old.stage_version = 2)))) EXECUTE FUNCTION mark_chart_rollup_dirty();

-- Trigger: block_stage_results block_stage_results_proxy_history_epoch

CREATE TRIGGER block_stage_results_proxy_history_epoch AFTER INSERT OR UPDATE ON block_stage_results FOR EACH ROW EXECUTE FUNCTION record_proxy_history_publication_epoch();

-- Trigger: block_statistics block_statistics_chart_dirty_trigger

CREATE TRIGGER block_statistics_chart_dirty_trigger AFTER INSERT OR DELETE OR UPDATE ON block_statistics FOR EACH ROW EXECUTE FUNCTION mark_chart_rollup_dirty();

-- Trigger: canonical_blocks canonical_blocks_chart_dirty_trigger

CREATE TRIGGER canonical_blocks_chart_dirty_trigger AFTER INSERT OR DELETE OR UPDATE ON canonical_blocks FOR EACH ROW EXECUTE FUNCTION mark_canonical_chart_rollup_dirty();

-- Trigger: contract_code_observations contract_code_observations_search_catalog_trigger

CREATE TRIGGER contract_code_observations_search_catalog_trigger AFTER INSERT OR DELETE OR UPDATE ON contract_code_observations FOR EACH ROW EXECUTE FUNCTION record_search_catalog_document('code');

-- Trigger: diamond_loupe_snapshots diamond_loupe_snapshots_source_guard

CREATE TRIGGER diamond_loupe_snapshots_source_guard BEFORE INSERT ON diamond_loupe_snapshots FOR EACH ROW EXECUTE FUNCTION enforce_proxy_detection_generation();

-- Trigger: durable_jobs durable_jobs_chart_dirty_trigger

CREATE TRIGGER durable_jobs_chart_dirty_trigger AFTER INSERT OR UPDATE OF status, requested_generation, claimed_generation, completed_generation, leased_generation, result ON durable_jobs FOR EACH ROW EXECUTE FUNCTION mark_chart_rollup_dirty_from_job();

-- Trigger: durable_jobs durable_jobs_proxy_history_epoch

CREATE TRIGGER durable_jobs_proxy_history_epoch AFTER INSERT OR UPDATE ON durable_jobs FOR EACH ROW EXECUTE FUNCTION record_proxy_history_job_epoch();

-- Trigger: durable_jobs durable_jobs_publication_protocol

CREATE TRIGGER durable_jobs_publication_protocol BEFORE UPDATE ON durable_jobs FOR EACH ROW EXECUTE FUNCTION require_enrichment_publication_protocol();

-- Trigger: durable_jobs durable_jobs_terminal_publication

CREATE CONSTRAINT TRIGGER durable_jobs_terminal_publication AFTER INSERT OR UPDATE ON durable_jobs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION enforce_enrichment_terminal_publication();

-- Trigger: durable_stage_publications durable_stage_publications_append_only

CREATE TRIGGER durable_stage_publications_append_only BEFORE DELETE OR UPDATE ON durable_stage_publications FOR EACH ROW EXECUTE FUNCTION reject_durable_stage_publication_mutation();

-- Trigger: durable_stage_publications durable_stage_publications_derived_forward_enqueue

CREATE TRIGGER durable_stage_publications_derived_forward_enqueue AFTER INSERT ON durable_stage_publications FOR EACH ROW EXECUTE FUNCTION enqueue_derived_forward_event_after_stage_publication();

-- Trigger: ens_name_observations ens_name_observations_search_catalog_trigger

CREATE TRIGGER ens_name_observations_search_catalog_trigger AFTER INSERT OR UPDATE OF publication_nonce ON ens_name_observations FOR EACH ROW EXECUTE FUNCTION record_ens_name_search_document();

-- Trigger: erc1155_balance_reconciliations erc1155_balance_reconciliations_immutable_trigger

CREATE TRIGGER erc1155_balance_reconciliations_immutable_trigger BEFORE UPDATE ON erc1155_balance_reconciliations FOR EACH ROW EXECUTE FUNCTION reject_exact_nft_observation_mutation();

-- Trigger: erc20_balance_reconciliations erc20_balance_reconciliation_immutable

CREATE TRIGGER erc20_balance_reconciliation_immutable BEFORE UPDATE ON erc20_balance_reconciliations FOR EACH ROW EXECUTE FUNCTION etherview_guard_erc20_balance_reconciliation();

-- Trigger: erc20_holder_balances erc20_holder_balances_immutable

CREATE TRIGGER erc20_holder_balances_immutable BEFORE UPDATE ON erc20_holder_balances FOR EACH ROW EXECUTE FUNCTION etherview_guard_erc20_holder_fact();

-- Trigger: erc20_holder_snapshots erc20_holder_snapshots_immutable

CREATE TRIGGER erc20_holder_snapshots_immutable BEFORE UPDATE ON erc20_holder_snapshots FOR EACH ROW EXECUTE FUNCTION etherview_guard_erc20_holder_fact();

-- Trigger: erc4337_user_operations erc4337_canonical_user_op_hash_trigger

CREATE TRIGGER erc4337_canonical_user_op_hash_trigger BEFORE INSERT OR UPDATE OF canonical ON erc4337_user_operations FOR EACH ROW EXECUTE FUNCTION enforce_erc4337_canonical_user_op_hash();

-- Trigger: canonical_blocks erc4337_coverage_canonical_detach_trigger

CREATE TRIGGER erc4337_coverage_canonical_detach_trigger AFTER DELETE OR UPDATE ON canonical_blocks FOR EACH ROW EXECUTE FUNCTION erc4337_remove_detached_coverage();

-- Trigger: erc721_owner_reconciliations erc721_owner_reconciliations_immutable_trigger

CREATE TRIGGER erc721_owner_reconciliations_immutable_trigger BEFORE UPDATE ON erc721_owner_reconciliations FOR EACH ROW EXECUTE FUNCTION reject_exact_nft_observation_mutation();

-- Trigger: external_metadata external_metadata_exact_nft_immutable

CREATE TRIGGER external_metadata_exact_nft_immutable BEFORE UPDATE ON external_metadata FOR EACH ROW EXECUTE FUNCTION etherview_guard_exact_nft_metadata();

-- Trigger: genesis_account_observations genesis_account_observation_immutable

CREATE TRIGGER genesis_account_observation_immutable BEFORE UPDATE ON genesis_account_observations FOR EACH ROW EXECUTE FUNCTION etherview_guard_genesis_account_observation();

-- Trigger: genesis_state_imports genesis_state_import_complete_immutable

CREATE TRIGGER genesis_state_import_complete_immutable BEFORE UPDATE ON genesis_state_imports FOR EACH ROW EXECUTE FUNCTION etherview_guard_completed_genesis_import();

-- Trigger: canonical_blocks native_transfer_canonical_coverage_trigger

CREATE TRIGGER native_transfer_canonical_coverage_trigger AFTER INSERT OR DELETE OR UPDATE ON canonical_blocks FOR EACH ROW EXECUTE FUNCTION native_transfer_canonical_coverage();

-- Trigger: durable_jobs native_transfer_job_coverage_trigger

CREATE TRIGGER native_transfer_job_coverage_trigger AFTER INSERT OR UPDATE ON durable_jobs FOR EACH ROW WHEN ((new.stage = 'native_transfer'::text)) EXECUTE FUNCTION native_transfer_job_coverage();

-- Trigger: transactional_outbox native_transfer_outbox_coverage_trigger

CREATE TRIGGER native_transfer_outbox_coverage_trigger AFTER INSERT OR DELETE OR UPDATE ON transactional_outbox FOR EACH ROW EXECUTE FUNCTION native_transfer_outbox_coverage();

-- Trigger: block_stage_results native_transfer_result_coverage_trigger

CREATE TRIGGER native_transfer_result_coverage_trigger AFTER INSERT OR DELETE OR UPDATE ON block_stage_results FOR EACH ROW EXECUTE FUNCTION native_transfer_result_coverage();

-- Trigger: nft_metadata_source_observations nft_metadata_source_observation_immutable

CREATE TRIGGER nft_metadata_source_observation_immutable BEFORE UPDATE ON nft_metadata_source_observations FOR EACH ROW EXECUTE FUNCTION etherview_guard_nft_metadata_source();

-- Trigger: nft_metadata_update_observations nft_metadata_update_observation_immutable

CREATE TRIGGER nft_metadata_update_observation_immutable BEFORE UPDATE ON nft_metadata_update_observations FOR EACH ROW EXECUTE FUNCTION etherview_guard_nft_metadata_update_observation();

-- Trigger: normalized_traces normalized_traces_derived_attempt_canonicality

CREATE TRIGGER normalized_traces_derived_attempt_canonicality AFTER UPDATE OF canonical ON normalized_traces FOR EACH ROW EXECUTE FUNCTION synchronize_derived_attempt_trace_canonicality();

-- Trigger: operator_labels operator_labels_search_catalog_trigger

CREATE TRIGGER operator_labels_search_catalog_trigger AFTER INSERT OR DELETE OR UPDATE ON operator_labels FOR EACH ROW EXECUTE FUNCTION record_search_catalog_document('label');

-- Trigger: proxy_artifact_resolutions proxy_artifact_resolutions_immutable

CREATE TRIGGER proxy_artifact_resolutions_immutable BEFORE DELETE OR UPDATE ON proxy_artifact_resolutions FOR EACH ROW EXECUTE FUNCTION reject_proxy_v2_evidence_mutation();

-- Trigger: proxy_artifact_resolutions proxy_artifact_resolutions_source_guard

CREATE TRIGGER proxy_artifact_resolutions_source_guard BEFORE INSERT ON proxy_artifact_resolutions FOR EACH ROW EXECUTE FUNCTION enforce_proxy_artifact_resolution();

-- Trigger: proxy_detection_evidence proxy_detection_evidence_source_guard

CREATE TRIGGER proxy_detection_evidence_source_guard BEFORE INSERT ON proxy_detection_evidence FOR EACH ROW EXECUTE FUNCTION enforce_proxy_detection_generation();

-- Trigger: proxy_history_epochs proxy_history_epochs_append_only

CREATE TRIGGER proxy_history_epochs_append_only BEFORE DELETE OR UPDATE ON proxy_history_epochs FOR EACH ROW EXECUTE FUNCTION reject_proxy_history_epoch_mutation();

-- Trigger: canonical_blocks proxy_interaction_coverage_canonical_trigger

CREATE TRIGGER proxy_interaction_coverage_canonical_trigger AFTER INSERT OR DELETE OR UPDATE ON canonical_blocks FOR EACH ROW EXECUTE FUNCTION refresh_proxy_interaction_coverage_from_canonical();

-- Trigger: durable_jobs proxy_interaction_coverage_job_trigger

CREATE TRIGGER proxy_interaction_coverage_job_trigger AFTER INSERT OR DELETE OR UPDATE ON durable_jobs FOR EACH ROW EXECUTE FUNCTION refresh_proxy_interaction_coverage_from_job();

-- Trigger: transactional_outbox proxy_interaction_coverage_outbox_trigger

CREATE TRIGGER proxy_interaction_coverage_outbox_trigger AFTER INSERT OR DELETE OR UPDATE ON transactional_outbox FOR EACH ROW EXECUTE FUNCTION refresh_proxy_interaction_coverage_from_outbox();

-- Trigger: proxy_observation_generations proxy_observation_generations_immutable

CREATE TRIGGER proxy_observation_generations_immutable BEFORE DELETE OR UPDATE ON proxy_observation_generations FOR EACH ROW EXECUTE FUNCTION reject_proxy_v2_evidence_mutation();

-- Trigger: proxy_observation_generations proxy_observation_generations_source_guard

CREATE TRIGGER proxy_observation_generations_source_guard BEFORE INSERT ON proxy_observation_generations FOR EACH ROW EXECUTE FUNCTION enforce_proxy_observation_generation();

-- Trigger: proxy_replay_targets proxy_replay_targets_immutable

CREATE TRIGGER proxy_replay_targets_immutable BEFORE DELETE OR UPDATE ON proxy_replay_targets FOR EACH ROW EXECUTE FUNCTION reject_proxy_v2_evidence_mutation();

-- Trigger: token_contracts token_contracts_search_catalog_trigger

CREATE TRIGGER token_contracts_search_catalog_trigger AFTER INSERT OR DELETE OR UPDATE ON token_contracts FOR EACH ROW EXECUTE FUNCTION record_search_catalog_document('token');

-- Trigger: token_events token_events_chart_dirty_trigger

CREATE TRIGGER token_events_chart_dirty_trigger AFTER INSERT OR DELETE OR UPDATE ON token_events FOR EACH ROW EXECUTE FUNCTION mark_chart_rollup_dirty();

-- Trigger: uups_implementation_observation_generations uups_implementation_observation_generations_immutable

CREATE TRIGGER uups_implementation_observation_generations_immutable BEFORE DELETE OR UPDATE ON uups_implementation_observation_generations FOR EACH ROW EXECUTE FUNCTION reject_proxy_v2_evidence_mutation();

-- Trigger: uups_implementation_observation_generations uups_implementation_observation_generations_source_guard

CREATE TRIGGER uups_implementation_observation_generations_source_guard BEFORE INSERT ON uups_implementation_observation_generations FOR EACH ROW EXECUTE FUNCTION enforce_proxy_observation_generation();

-- Trigger: uups_implementation_observations uups_implementation_observations_immutable

CREATE TRIGGER uups_implementation_observations_immutable BEFORE DELETE OR UPDATE ON uups_implementation_observations FOR EACH ROW EXECUTE FUNCTION enforce_uups_implementation_observation_mutation();

-- Trigger: uups_implementation_observations uups_implementation_observations_source_guard

CREATE TRIGGER uups_implementation_observations_source_guard BEFORE INSERT ON uups_implementation_observations FOR EACH ROW EXECUTE FUNCTION enforce_uups_implementation_observation();

-- Trigger: uups_implementation_observations uups_implementation_observations_witness_guard

CREATE CONSTRAINT TRIGGER uups_implementation_observations_witness_guard AFTER INSERT ON uups_implementation_observations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION enforce_uups_implementation_observation_witness();

-- Trigger: verification_compilation_contracts verification_compilation_contracts_immutable

CREATE TRIGGER verification_compilation_contracts_immutable BEFORE DELETE OR UPDATE ON verification_compilation_contracts FOR EACH ROW EXECUTE FUNCTION reject_verification_compilation_mutation();

-- Trigger: verification_compilation_units verification_compilation_units_immutable

CREATE TRIGGER verification_compilation_units_immutable BEFORE DELETE OR UPDATE ON verification_compilation_units FOR EACH ROW EXECUTE FUNCTION reject_verification_compilation_mutation();

-- Trigger: verification_jobs verification_jobs_compiler_provenance

CREATE TRIGGER verification_jobs_compiler_provenance BEFORE UPDATE OF compiler_platform, catalog_generation_id, compiler_digest, executor_kind, execution_policy, executor_digest ON verification_jobs FOR EACH ROW EXECUTE FUNCTION enforce_verification_compiler_provenance();

-- Trigger: verification_jobs verification_jobs_unbound_compiler_insert

CREATE TRIGGER verification_jobs_unbound_compiler_insert BEFORE INSERT ON verification_jobs FOR EACH ROW EXECUTE FUNCTION enforce_unbound_verification_job_insert();

-- Trigger: verification_jobs verification_jobs_v2_result_insert

CREATE CONSTRAINT TRIGGER verification_jobs_v2_result_insert AFTER INSERT ON verification_jobs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION enforce_verifier_v2_result_job();

-- Trigger: verification_jobs verification_jobs_v2_result_update

CREATE CONSTRAINT TRIGGER verification_jobs_v2_result_update AFTER UPDATE OF status, outcome_kind, outcome, error_code ON verification_jobs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION enforce_verifier_v2_result_job();

-- Trigger: verification_jobs verification_jobs_vyper_runtime_binding

CREATE TRIGGER verification_jobs_vyper_runtime_binding BEFORE INSERT OR UPDATE ON verification_jobs FOR EACH ROW EXECUTE FUNCTION check_vyper_runtime_binding();

-- Trigger: verification_results verification_results_v2_immutable

CREATE TRIGGER verification_results_v2_immutable BEFORE DELETE OR UPDATE ON verification_results FOR EACH ROW EXECUTE FUNCTION reject_verifier_v2_result_mutation();

-- Trigger: verification_results verification_results_v2_terminal_job

CREATE CONSTRAINT TRIGGER verification_results_v2_terminal_job AFTER INSERT ON verification_results DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION enforce_verifier_v2_result_job();

-- Trigger: verified_contract_proxy_artifacts verified_contract_proxy_artifacts_immutable

CREATE TRIGGER verified_contract_proxy_artifacts_immutable BEFORE DELETE OR UPDATE ON verified_contract_proxy_artifacts FOR EACH ROW EXECUTE FUNCTION reject_proxy_v2_evidence_mutation();

-- Trigger: verified_contract_proxy_artifacts verified_contract_proxy_artifacts_source_guard

CREATE TRIGGER verified_contract_proxy_artifacts_source_guard BEFORE INSERT ON verified_contract_proxy_artifacts FOR EACH ROW EXECUTE FUNCTION enforce_verified_contract_proxy_artifact();

-- Trigger: verified_contracts verified_contracts_search_catalog_trigger

CREATE TRIGGER verified_contracts_search_catalog_trigger AFTER INSERT OR DELETE OR UPDATE ON verified_contracts FOR EACH ROW EXECUTE FUNCTION record_verified_contract_search_catalog_document();

-- Trigger: verified_contracts verified_contracts_v2_source_guard

CREATE TRIGGER verified_contracts_v2_source_guard BEFORE INSERT OR UPDATE ON verified_contracts FOR EACH ROW EXECUTE FUNCTION enforce_verifier_v2_publication();

-- Trigger: verified_proxy_bindings verified_proxy_bindings_immutable

CREATE TRIGGER verified_proxy_bindings_immutable BEFORE DELETE OR UPDATE ON verified_proxy_bindings FOR EACH ROW EXECUTE FUNCTION reject_verified_proxy_binding_mutation();

-- Trigger: verified_proxy_bindings verified_proxy_bindings_source_guard

CREATE TRIGGER verified_proxy_bindings_source_guard BEFORE INSERT ON verified_proxy_bindings FOR EACH ROW EXECUTE FUNCTION enforce_verified_proxy_binding();

-- Trigger: transactional_outbox watch_core_change

CREATE TRIGGER watch_core_change AFTER INSERT OR UPDATE OF generation ON transactional_outbox FOR EACH ROW WHEN ((new.topic = ANY (ARRAY['core.block.canonical'::text, 'core.block.orphaned'::text]))) EXECUTE FUNCTION watch_core_change();

-- Trigger: durable_jobs watch_token_change

CREATE TRIGGER watch_token_change AFTER INSERT OR UPDATE OF requested_generation, completed_generation, status ON durable_jobs FOR EACH ROW WHEN (((new.kind = 'enrichment'::text) AND (new.stage = 'token'::text) AND (new.stage_version = 2))) EXECUTE FUNCTION watch_token_change();

-- Fk Constraint: abi_decodings abi_decodings_chain_id_block_number_block_hash_fkey

ALTER TABLE abi_decodings
    ADD CONSTRAINT abi_decodings_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: address_activities address_activities_chain_id_transaction_hash_fkey

ALTER TABLE address_activities
    ADD CONSTRAINT address_activities_chain_id_transaction_hash_fkey FOREIGN KEY (chain_id, transaction_hash) REFERENCES transactions(chain_id, hash);

-- Fk Constraint: address_export_admissions address_export_admissions_user_id_fkey

ALTER TABLE ONLY address_export_admissions
    ADD CONSTRAINT address_export_admissions_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id);

-- Fk Constraint: address_watches address_watches_chain_id_fkey

ALTER TABLE ONLY address_watches
    ADD CONSTRAINT address_watches_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: address_watches address_watches_user_id_fkey

ALTER TABLE ONLY address_watches
    ADD CONSTRAINT address_watches_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id);

-- Fk Constraint: api_keys api_keys_owner_user_id_fkey

ALTER TABLE ONLY api_keys
    ADD CONSTRAINT api_keys_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES users(id);

-- Fk Constraint: auth_challenges auth_challenges_chain_id_fkey

ALTER TABLE ONLY auth_challenges
    ADD CONSTRAINT auth_challenges_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: beacon_implementation_observations beacon_implementation_observa_chain_id_block_number_block__fkey

ALTER TABLE ONLY beacon_implementation_observations
    ADD CONSTRAINT beacon_implementation_observa_chain_id_block_number_block__fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: beacon_implementation_observations beacon_implementation_observations_chain_id_fkey

ALTER TABLE ONLY beacon_implementation_observations
    ADD CONSTRAINT beacon_implementation_observations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: beacon_observation_generations beacon_observation_generation_chain_id_beacon_address_obse_fkey

ALTER TABLE ONLY beacon_observation_generations
    ADD CONSTRAINT beacon_observation_generation_chain_id_beacon_address_obse_fkey FOREIGN KEY (chain_id, beacon_address, observation_block_hash, observation_stage_version) REFERENCES beacon_implementation_observations(chain_id, beacon_address, block_hash, stage_version) ON DELETE RESTRICT;

-- Fk Constraint: beacon_observation_generations beacon_observation_generations_chain_id_fkey

ALTER TABLE ONLY beacon_observation_generations
    ADD CONSTRAINT beacon_observation_generations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: beacon_observation_generations beacon_observation_generations_durable_job_id_fkey

ALTER TABLE ONLY beacon_observation_generations
    ADD CONSTRAINT beacon_observation_generations_durable_job_id_fkey FOREIGN KEY (durable_job_id) REFERENCES durable_jobs(id);

-- Fk Constraint: billing_account_entries billing_account_entries_user_id_chain_id_network_asset_fkey

ALTER TABLE ONLY billing_account_entries
    ADD CONSTRAINT billing_account_entries_user_id_chain_id_network_asset_fkey FOREIGN KEY (user_id, chain_id, network, asset) REFERENCES billing_accounts(user_id, chain_id, network, asset);

-- Fk Constraint: billing_accounts billing_accounts_chain_id_fkey

ALTER TABLE ONLY billing_accounts
    ADD CONSTRAINT billing_accounts_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: billing_accounts billing_accounts_user_id_fkey

ALTER TABLE ONLY billing_accounts
    ADD CONSTRAINT billing_accounts_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id);

-- Fk Constraint: billing_payment_events billing_payment_events_payment_id_fkey

ALTER TABLE ONLY billing_payment_events
    ADD CONSTRAINT billing_payment_events_payment_id_fkey FOREIGN KEY (payment_id) REFERENCES billing_payments(id);

-- Fk Constraint: billing_payments billing_payments_api_key_prefix_fkey

ALTER TABLE ONLY billing_payments
    ADD CONSTRAINT billing_payments_api_key_prefix_fkey FOREIGN KEY (api_key_prefix) REFERENCES api_keys(prefix) ON DELETE SET NULL;

-- Fk Constraint: billing_payments billing_payments_chain_id_fkey

ALTER TABLE ONLY billing_payments
    ADD CONSTRAINT billing_payments_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: billing_payments billing_payments_topup_intent_id_fkey

ALTER TABLE ONLY billing_payments
    ADD CONSTRAINT billing_payments_topup_intent_id_fkey FOREIGN KEY (topup_intent_id) REFERENCES billing_topup_intents(id);

-- Fk Constraint: billing_payments billing_payments_user_id_fkey

ALTER TABLE ONLY billing_payments
    ADD CONSTRAINT billing_payments_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL;

-- Fk Constraint: billing_topup_intents billing_topup_intents_active_payment_fk

ALTER TABLE ONLY billing_topup_intents
    ADD CONSTRAINT billing_topup_intents_active_payment_fk FOREIGN KEY (active_payment_id) REFERENCES billing_payments(id);

-- Fk Constraint: billing_topup_intents billing_topup_intents_chain_id_fkey

ALTER TABLE ONLY billing_topup_intents
    ADD CONSTRAINT billing_topup_intents_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: billing_topup_intents billing_topup_intents_user_id_fkey

ALTER TABLE ONLY billing_topup_intents
    ADD CONSTRAINT billing_topup_intents_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id);

-- Fk Constraint: billing_usage_charges billing_usage_charges_api_key_prefix_fkey

ALTER TABLE ONLY billing_usage_charges
    ADD CONSTRAINT billing_usage_charges_api_key_prefix_fkey FOREIGN KEY (api_key_prefix) REFERENCES api_keys(prefix);

-- Fk Constraint: billing_usage_charges billing_usage_charges_user_id_chain_id_network_asset_fkey

ALTER TABLE ONLY billing_usage_charges
    ADD CONSTRAINT billing_usage_charges_user_id_chain_id_network_asset_fkey FOREIGN KEY (user_id, chain_id, network, asset) REFERENCES billing_accounts(user_id, chain_id, network, asset);

-- Fk Constraint: block_journals block_journals_chain_id_fkey

ALTER TABLE ONLY block_journals
    ADD CONSTRAINT block_journals_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: block_journals block_journals_durable_job_fkey

ALTER TABLE ONLY block_journals
    ADD CONSTRAINT block_journals_durable_job_fkey FOREIGN KEY (durable_job_id) REFERENCES durable_jobs(id);

-- Fk Constraint: block_stage_results block_stage_results_chain_id_block_number_block_hash_fkey

ALTER TABLE ONLY block_stage_results
    ADD CONSTRAINT block_stage_results_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: block_stage_results block_stage_results_chain_id_fkey

ALTER TABLE ONLY block_stage_results
    ADD CONSTRAINT block_stage_results_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: block_stage_results block_stage_results_durable_job_fkey

ALTER TABLE ONLY block_stage_results
    ADD CONSTRAINT block_stage_results_durable_job_fkey FOREIGN KEY (durable_job_id) REFERENCES durable_jobs(id);

-- Fk Constraint: block_statistics block_statistics_chain_id_block_number_block_hash_fkey

ALTER TABLE ONLY block_statistics
    ADD CONSTRAINT block_statistics_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: blocks blocks_chain_id_fkey

ALTER TABLE ONLY blocks
    ADD CONSTRAINT blocks_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: canonical_blocks canonical_blocks_chain_id_number_block_hash_fkey

ALTER TABLE ONLY canonical_blocks
    ADD CONSTRAINT canonical_blocks_chain_id_number_block_hash_fkey FOREIGN KEY (chain_id, number, block_hash) REFERENCES blocks(chain_id, number, hash) DEFERRABLE INITIALLY DEFERRED;

-- Fk Constraint: chain_finality chain_finality_chain_id_fkey

ALTER TABLE ONLY chain_finality
    ADD CONSTRAINT chain_finality_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: chart_hourly_rollups chart_hourly_rollups_chain_id_fkey

ALTER TABLE ONLY chart_hourly_rollups
    ADD CONSTRAINT chart_hourly_rollups_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: chart_rollup_backfill chart_rollup_backfill_chain_id_fkey

ALTER TABLE ONLY chart_rollup_backfill
    ADD CONSTRAINT chart_rollup_backfill_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: chart_rollup_dirty_hours chart_rollup_dirty_hours_chain_id_fkey

ALTER TABLE ONLY chart_rollup_dirty_hours
    ADD CONSTRAINT chart_rollup_dirty_hours_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: compiler_catalog_entries compiler_catalog_entries_generation_fk

ALTER TABLE ONLY compiler_catalog_entries
    ADD CONSTRAINT compiler_catalog_entries_generation_fk FOREIGN KEY (generation_id, language) REFERENCES compiler_catalog_generations(id, language) ON DELETE RESTRICT;

-- Fk Constraint: compiler_catalog_heads compiler_catalog_heads_generation_fk

ALTER TABLE ONLY compiler_catalog_heads
    ADD CONSTRAINT compiler_catalog_heads_generation_fk FOREIGN KEY (generation_id, language) REFERENCES compiler_catalog_generations(id, language) ON DELETE RESTRICT;

-- Fk Constraint: contract_abis contract_abis_chain_id_block_number_block_hash_fkey

ALTER TABLE ONLY contract_abis
    ADD CONSTRAINT contract_abis_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: contract_abis contract_abis_chain_id_fkey

ALTER TABLE ONLY contract_abis
    ADD CONSTRAINT contract_abis_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: contract_code_observations contract_code_observations_chain_id_block_number_block_has_fkey

ALTER TABLE ONLY contract_code_observations
    ADD CONSTRAINT contract_code_observations_chain_id_block_number_block_has_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: contract_code_observations contract_code_observations_chain_id_fkey

ALTER TABLE ONLY contract_code_observations
    ADD CONSTRAINT contract_code_observations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: core_backfill_leases core_backfill_leases_chain_id_fkey

ALTER TABLE ONLY core_backfill_leases
    ADD CONSTRAINT core_backfill_leases_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: core_coverage_ranges core_coverage_ranges_chain_id_fkey

ALTER TABLE ONLY core_coverage_ranges
    ADD CONSTRAINT core_coverage_ranges_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: core_index_configuration core_index_configuration_chain_id_fkey

ALTER TABLE ONLY core_index_configuration
    ADD CONSTRAINT core_index_configuration_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: derived_verification_attempts derived_verification_attempts_compilation_id_fkey

ALTER TABLE ONLY derived_verification_attempts
    ADD CONSTRAINT derived_verification_attempts_compilation_id_fkey FOREIGN KEY (compilation_id) REFERENCES verification_compilation_units(id) ON DELETE RESTRICT;

-- Fk Constraint: derived_verification_attempts derived_verification_attempts_trace_fk

ALTER TABLE ONLY derived_verification_attempts
    ADD CONSTRAINT derived_verification_attempts_trace_fk FOREIGN KEY (chain_id, block_number, block_hash, transaction_hash, trace_path) REFERENCES normalized_traces(chain_id, block_number, block_hash, transaction_hash, trace_path) ON DELETE RESTRICT;

-- Fk Constraint: derived_verification_attempts derived_verification_attempts_verification_job_id_fkey

ALTER TABLE ONLY derived_verification_attempts
    ADD CONSTRAINT derived_verification_attempts_verification_job_id_fkey FOREIGN KEY (verification_job_id) REFERENCES verification_jobs(id) ON DELETE RESTRICT;

-- Fk Constraint: derived_verification_backfill_requests derived_verification_backfill_requests_chain_id_fkey

ALTER TABLE ONLY derived_verification_backfill_requests
    ADD CONSTRAINT derived_verification_backfill_requests_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: derived_verification_forward_blocks derived_verification_forward__chain_id_block_number_block__fkey

ALTER TABLE ONLY derived_verification_forward_blocks
    ADD CONSTRAINT derived_verification_forward__chain_id_block_number_block__fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash) ON DELETE RESTRICT;

-- Fk Constraint: derived_verification_forward_blocks derived_verification_forward_blocks_source_fk

ALTER TABLE ONLY derived_verification_forward_blocks
    ADD CONSTRAINT derived_verification_forward_blocks_source_fk FOREIGN KEY (source_job_id, source_generation) REFERENCES durable_stage_publications(job_id, job_generation) ON DELETE RESTRICT;

-- Fk Constraint: derived_verification_scans derived_verification_scans_chain_id_fkey

ALTER TABLE ONLY derived_verification_scans
    ADD CONSTRAINT derived_verification_scans_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: derived_verification_scans derived_verification_scans_compilation_id_fkey

ALTER TABLE ONLY derived_verification_scans
    ADD CONSTRAINT derived_verification_scans_compilation_id_fkey FOREIGN KEY (compilation_id) REFERENCES verification_compilation_units(id) ON DELETE RESTRICT;

-- Fk Constraint: diamond_cut_events diamond_cut_events_chain_id_block_number_block_hash_fkey

ALTER TABLE ONLY diamond_cut_events
    ADD CONSTRAINT diamond_cut_events_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: diamond_cut_events diamond_cut_events_chain_id_block_number_block_hash_log_in_fkey

ALTER TABLE ONLY diamond_cut_events
    ADD CONSTRAINT diamond_cut_events_chain_id_block_number_block_hash_log_in_fkey FOREIGN KEY (chain_id, block_number, block_hash, log_index) REFERENCES logs(chain_id, block_number, block_hash, log_index);

-- Fk Constraint: diamond_cut_events diamond_cut_events_chain_id_fkey

ALTER TABLE ONLY diamond_cut_events
    ADD CONSTRAINT diamond_cut_events_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: diamond_loupe_facets diamond_loupe_facets_snapshot_id_fkey

ALTER TABLE ONLY diamond_loupe_facets
    ADD CONSTRAINT diamond_loupe_facets_snapshot_id_fkey FOREIGN KEY (snapshot_id) REFERENCES diamond_loupe_snapshots(id) ON DELETE CASCADE;

-- Fk Constraint: diamond_loupe_selectors diamond_loupe_selectors_snapshot_id_facet_address_fkey

ALTER TABLE ONLY diamond_loupe_selectors
    ADD CONSTRAINT diamond_loupe_selectors_snapshot_id_facet_address_fkey FOREIGN KEY (snapshot_id, facet_address) REFERENCES diamond_loupe_facets(snapshot_id, facet_address) ON DELETE CASCADE;

-- Fk Constraint: diamond_loupe_snapshots diamond_loupe_snapshots_chain_id_block_number_block_hash_fkey

ALTER TABLE ONLY diamond_loupe_snapshots
    ADD CONSTRAINT diamond_loupe_snapshots_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: diamond_loupe_snapshots diamond_loupe_snapshots_chain_id_fkey

ALTER TABLE ONLY diamond_loupe_snapshots
    ADD CONSTRAINT diamond_loupe_snapshots_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: diamond_loupe_snapshots diamond_loupe_snapshots_durable_job_id_fkey

ALTER TABLE ONLY diamond_loupe_snapshots
    ADD CONSTRAINT diamond_loupe_snapshots_durable_job_id_fkey FOREIGN KEY (durable_job_id) REFERENCES durable_jobs(id);

-- Fk Constraint: diamond_selector_changes diamond_selector_changes_chain_id_block_hash_log_index_sta_fkey

ALTER TABLE ONLY diamond_selector_changes
    ADD CONSTRAINT diamond_selector_changes_chain_id_block_hash_log_index_sta_fkey FOREIGN KEY (chain_id, block_hash, log_index, stage_version) REFERENCES diamond_cut_events(chain_id, block_hash, log_index, stage_version) ON DELETE CASCADE;

-- Fk Constraint: durable_job_replay_requests durable_job_replay_requests_job_id_fkey

ALTER TABLE ONLY durable_job_replay_requests
    ADD CONSTRAINT durable_job_replay_requests_job_id_fkey FOREIGN KEY (job_id) REFERENCES durable_jobs(id) ON DELETE CASCADE;

-- Fk Constraint: durable_jobs durable_jobs_chain_id_fkey

ALTER TABLE ONLY durable_jobs
    ADD CONSTRAINT durable_jobs_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: durable_stage_publications durable_stage_publications_chain_id_block_number_block_has_fkey

ALTER TABLE ONLY durable_stage_publications
    ADD CONSTRAINT durable_stage_publications_chain_id_block_number_block_has_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: durable_stage_publications durable_stage_publications_chain_id_fkey

ALTER TABLE ONLY durable_stage_publications
    ADD CONSTRAINT durable_stage_publications_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: durable_stage_publications durable_stage_publications_job_id_fkey

ALTER TABLE ONLY durable_stage_publications
    ADD CONSTRAINT durable_stage_publications_job_id_fkey FOREIGN KEY (job_id) REFERENCES durable_jobs(id);

-- Fk Constraint: eip7702_authorizations eip7702_authorizations_chain_id_block_number_block_hash_tr_fkey

ALTER TABLE eip7702_authorizations
    ADD CONSTRAINT eip7702_authorizations_chain_id_block_number_block_hash_tr_fkey FOREIGN KEY (chain_id, block_number, block_hash, transaction_index) REFERENCES transaction_inclusions(chain_id, block_number, block_hash, tx_index);

-- Fk Constraint: ens_address_name_snapshots ens_address_name_snapshots_chain_id_fkey

ALTER TABLE ONLY ens_address_name_snapshots
    ADD CONSTRAINT ens_address_name_snapshots_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: ens_address_name_snapshots ens_address_name_snapshots_generation_id_fkey

ALTER TABLE ONLY ens_address_name_snapshots
    ADD CONSTRAINT ens_address_name_snapshots_generation_id_fkey FOREIGN KEY (generation_id) REFERENCES ens_resolution_generations(id) ON DELETE CASCADE;

-- Fk Constraint: ens_name_observations ens_name_observations_chain_id_fkey

ALTER TABLE ONLY ens_name_observations
    ADD CONSTRAINT ens_name_observations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: ens_name_observations ens_name_observations_generation_id_fkey

ALTER TABLE ONLY ens_name_observations
    ADD CONSTRAINT ens_name_observations_generation_id_fkey FOREIGN KEY (generation_id) REFERENCES ens_resolution_generations(id) ON DELETE CASCADE;

-- Fk Constraint: ens_resolution_failures ens_resolution_failures_chain_id_fkey

ALTER TABLE ONLY ens_resolution_failures
    ADD CONSTRAINT ens_resolution_failures_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: ens_resolution_failures ens_resolution_failures_generation_id_fkey

ALTER TABLE ONLY ens_resolution_failures
    ADD CONSTRAINT ens_resolution_failures_generation_id_fkey FOREIGN KEY (generation_id) REFERENCES ens_resolution_generations(id) ON DELETE CASCADE;

-- Fk Constraint: ens_resolution_generations ens_resolution_generations_chain_id_custom_block_number_cu_fkey

ALTER TABLE ONLY ens_resolution_generations
    ADD CONSTRAINT ens_resolution_generations_chain_id_custom_block_number_cu_fkey FOREIGN KEY (chain_id, custom_block_number, custom_block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: ens_resolution_generations ens_resolution_generations_chain_id_fkey

ALTER TABLE ONLY ens_resolution_generations
    ADD CONSTRAINT ens_resolution_generations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: erc1155_balance_reconciliations erc1155_balance_reconciliatio_chain_id_block_number_block__fkey

ALTER TABLE ONLY erc1155_balance_reconciliations
    ADD CONSTRAINT erc1155_balance_reconciliatio_chain_id_block_number_block__fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: erc1155_balance_reconciliations erc1155_balance_reconciliations_chain_id_fkey

ALTER TABLE ONLY erc1155_balance_reconciliations
    ADD CONSTRAINT erc1155_balance_reconciliations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: erc20_balance_reconciliations erc20_balance_reconciliations_chain_id_block_number_block__fkey

ALTER TABLE ONLY erc20_balance_reconciliations
    ADD CONSTRAINT erc20_balance_reconciliations_chain_id_block_number_block__fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: erc20_balance_reconciliations erc20_balance_reconciliations_chain_id_fkey

ALTER TABLE ONLY erc20_balance_reconciliations
    ADD CONSTRAINT erc20_balance_reconciliations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: erc20_holder_balances erc20_holder_balances_chain_id_token_address_block_number__fkey

ALTER TABLE erc20_holder_balances
    ADD CONSTRAINT erc20_holder_balances_chain_id_token_address_block_number__fkey FOREIGN KEY (chain_id, token_address, block_number, block_hash) REFERENCES erc20_holder_snapshots(chain_id, token_address, block_number, block_hash);

-- Fk Constraint: erc20_holder_snapshots erc20_holder_snapshots_chain_id_block_number_block_hash_fkey

ALTER TABLE erc20_holder_snapshots
    ADD CONSTRAINT erc20_holder_snapshots_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: erc20_holder_snapshots erc20_holder_snapshots_chain_id_fkey

ALTER TABLE erc20_holder_snapshots
    ADD CONSTRAINT erc20_holder_snapshots_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: erc4337_coverage_ranges erc4337_coverage_ranges_chain_id_fkey

ALTER TABLE ONLY erc4337_coverage_ranges
    ADD CONSTRAINT erc4337_coverage_ranges_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: erc4337_covered_blocks erc4337_covered_blocks_chain_id_block_number_block_hash_fkey

ALTER TABLE ONLY erc4337_covered_blocks
    ADD CONSTRAINT erc4337_covered_blocks_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: erc4337_covered_blocks erc4337_covered_blocks_chain_id_fkey

ALTER TABLE ONLY erc4337_covered_blocks
    ADD CONSTRAINT erc4337_covered_blocks_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: erc4337_user_operation_events erc4337_user_operation_events_chain_id_configuration_diges_fkey

ALTER TABLE erc4337_user_operation_events
    ADD CONSTRAINT erc4337_user_operation_events_chain_id_configuration_diges_fkey FOREIGN KEY (chain_id, configuration_digest, block_number, block_hash, transaction_hash, operation_index) REFERENCES erc4337_user_operations(chain_id, configuration_digest, block_number, block_hash, transaction_hash, operation_index);

-- Fk Constraint: erc4337_user_operation_participants erc4337_user_operation_partic_chain_id_configuration_diges_fkey

ALTER TABLE erc4337_user_operation_participants
    ADD CONSTRAINT erc4337_user_operation_partic_chain_id_configuration_diges_fkey FOREIGN KEY (chain_id, configuration_digest, block_number, block_hash, transaction_hash, operation_index) REFERENCES erc4337_user_operations(chain_id, configuration_digest, block_number, block_hash, transaction_hash, operation_index);

-- Fk Constraint: erc4337_user_operations erc4337_user_operations_chain_id_block_number_block_hash_t_fkey

ALTER TABLE erc4337_user_operations
    ADD CONSTRAINT erc4337_user_operations_chain_id_block_number_block_hash_t_fkey FOREIGN KEY (chain_id, block_number, block_hash, transaction_index) REFERENCES transaction_inclusions(chain_id, block_number, block_hash, tx_index);

-- Fk Constraint: erc4337_user_operations erc4337_user_operations_chain_id_fkey

ALTER TABLE erc4337_user_operations
    ADD CONSTRAINT erc4337_user_operations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: erc721_owner_reconciliations erc721_owner_reconciliations_chain_id_block_number_block_h_fkey

ALTER TABLE ONLY erc721_owner_reconciliations
    ADD CONSTRAINT erc721_owner_reconciliations_chain_id_block_number_block_h_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: erc721_owner_reconciliations erc721_owner_reconciliations_chain_id_fkey

ALTER TABLE ONLY erc721_owner_reconciliations
    ADD CONSTRAINT erc721_owner_reconciliations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: external_adapter_observations external_adapter_observations_chain_id_block_number_block__fkey

ALTER TABLE ONLY external_adapter_observations
    ADD CONSTRAINT external_adapter_observations_chain_id_block_number_block__fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: external_adapter_observations external_adapter_observations_chain_id_fkey

ALTER TABLE ONLY external_adapter_observations
    ADD CONSTRAINT external_adapter_observations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: external_metadata_attempts external_metadata_attempts_chain_id_fkey

ALTER TABLE ONLY external_metadata_attempts
    ADD CONSTRAINT external_metadata_attempts_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: external_metadata_attempts external_metadata_attempts_durable_job_id_fkey

ALTER TABLE ONLY external_metadata_attempts
    ADD CONSTRAINT external_metadata_attempts_durable_job_id_fkey FOREIGN KEY (durable_job_id) REFERENCES durable_jobs(id);

-- Fk Constraint: external_metadata external_metadata_chain_id_fkey

ALTER TABLE ONLY external_metadata
    ADD CONSTRAINT external_metadata_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: external_metadata external_metadata_observed_block_fk

ALTER TABLE ONLY external_metadata
    ADD CONSTRAINT external_metadata_observed_block_fk FOREIGN KEY (chain_id, observed_block_number, observed_block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: genesis_account_observations genesis_account_observations_chain_id_block_hash_fkey

ALTER TABLE ONLY genesis_account_observations
    ADD CONSTRAINT genesis_account_observations_chain_id_block_hash_fkey FOREIGN KEY (chain_id, block_hash) REFERENCES genesis_state_imports(chain_id, block_hash);

-- Fk Constraint: genesis_state_imports genesis_state_imports_chain_id_block_hash_fkey

ALTER TABLE ONLY genesis_state_imports
    ADD CONSTRAINT genesis_state_imports_chain_id_block_hash_fkey FOREIGN KEY (chain_id, block_hash) REFERENCES blocks(chain_id, hash);

-- Fk Constraint: genesis_state_imports genesis_state_imports_chain_id_fkey

ALTER TABLE ONLY genesis_state_imports
    ADD CONSTRAINT genesis_state_imports_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: index_checkpoints index_checkpoints_chain_id_fkey

ALTER TABLE ONLY index_checkpoints
    ADD CONSTRAINT index_checkpoints_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: logs logs_chain_id_block_number_block_hash_tx_index_fkey

ALTER TABLE logs
    ADD CONSTRAINT logs_chain_id_block_number_block_hash_tx_index_fkey FOREIGN KEY (chain_id, block_number, block_hash, tx_index) REFERENCES receipts(chain_id, block_number, block_hash, tx_index);

-- Fk Constraint: mempool_snapshot_transactions mempool_snapshot_transactions_chain_id_snapshot_id_fkey

ALTER TABLE ONLY mempool_snapshot_transactions
    ADD CONSTRAINT mempool_snapshot_transactions_chain_id_snapshot_id_fkey FOREIGN KEY (chain_id, snapshot_id) REFERENCES mempool_snapshots(chain_id, id) ON DELETE CASCADE;

-- Fk Constraint: mempool_snapshot_transactions mempool_snapshot_transactions_chain_id_tx_hash_fkey

ALTER TABLE ONLY mempool_snapshot_transactions
    ADD CONSTRAINT mempool_snapshot_transactions_chain_id_tx_hash_fkey FOREIGN KEY (chain_id, tx_hash) REFERENCES mempool_transactions(chain_id, tx_hash);

-- Fk Constraint: mempool_snapshots mempool_snapshots_chain_id_fkey

ALTER TABLE ONLY mempool_snapshots
    ADD CONSTRAINT mempool_snapshots_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: mempool_status mempool_status_chain_id_fkey

ALTER TABLE ONLY mempool_status
    ADD CONSTRAINT mempool_status_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: mempool_status mempool_status_chain_id_latest_snapshot_id_fkey

ALTER TABLE ONLY mempool_status
    ADD CONSTRAINT mempool_status_chain_id_latest_snapshot_id_fkey FOREIGN KEY (chain_id, latest_snapshot_id) REFERENCES mempool_snapshots(chain_id, id);

-- Fk Constraint: mempool_transaction_replacements mempool_transaction_replacements_chain_id_fkey

ALTER TABLE ONLY mempool_transaction_replacements
    ADD CONSTRAINT mempool_transaction_replacements_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: mempool_transaction_replacements mempool_transaction_replacements_chain_id_replaced_hash_fkey

ALTER TABLE ONLY mempool_transaction_replacements
    ADD CONSTRAINT mempool_transaction_replacements_chain_id_replaced_hash_fkey FOREIGN KEY (chain_id, replaced_hash) REFERENCES mempool_transactions(chain_id, tx_hash);

-- Fk Constraint: mempool_transaction_replacements mempool_transaction_replacements_chain_id_replacement_hash_fkey

ALTER TABLE ONLY mempool_transaction_replacements
    ADD CONSTRAINT mempool_transaction_replacements_chain_id_replacement_hash_fkey FOREIGN KEY (chain_id, replacement_hash) REFERENCES mempool_transactions(chain_id, tx_hash);

-- Fk Constraint: mempool_transaction_replacements mempool_transaction_replacements_chain_id_snapshot_id_fkey

ALTER TABLE ONLY mempool_transaction_replacements
    ADD CONSTRAINT mempool_transaction_replacements_chain_id_snapshot_id_fkey FOREIGN KEY (chain_id, snapshot_id) REFERENCES mempool_snapshots(chain_id, id) ON DELETE CASCADE;

-- Fk Constraint: mempool_transactions mempool_transactions_chain_id_fkey

ALTER TABLE ONLY mempool_transactions
    ADD CONSTRAINT mempool_transactions_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: native_transfer_coverage native_transfer_coverage_chain_id_fkey

ALTER TABLE ONLY native_transfer_coverage
    ADD CONSTRAINT native_transfer_coverage_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: native_transfers native_transfers_chain_id_block_number_block_hash_transact_fkey

ALTER TABLE native_transfers
    ADD CONSTRAINT native_transfers_chain_id_block_number_block_hash_transact_fkey FOREIGN KEY (chain_id, block_number, block_hash, transaction_index, transaction_hash) REFERENCES transaction_inclusions(chain_id, block_number, block_hash, tx_index, tx_hash);

-- Fk Constraint: native_transfers native_transfers_chain_id_fkey

ALTER TABLE native_transfers
    ADD CONSTRAINT native_transfers_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: nft_metadata_source_observations nft_metadata_source_observati_chain_id_block_number_block__fkey

ALTER TABLE ONLY nft_metadata_source_observations
    ADD CONSTRAINT nft_metadata_source_observati_chain_id_block_number_block__fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: nft_metadata_source_observations nft_metadata_source_observations_chain_id_fkey

ALTER TABLE ONLY nft_metadata_source_observations
    ADD CONSTRAINT nft_metadata_source_observations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: nft_metadata_update_observations nft_metadata_update_observati_chain_id_block_number_block__fkey

ALTER TABLE ONLY nft_metadata_update_observations
    ADD CONSTRAINT nft_metadata_update_observati_chain_id_block_number_block__fkey FOREIGN KEY (chain_id, block_number, block_hash, log_index) REFERENCES logs(chain_id, block_number, block_hash, log_index);

-- Fk Constraint: nft_metadata_update_observations nft_metadata_update_observations_chain_id_fkey

ALTER TABLE ONLY nft_metadata_update_observations
    ADD CONSTRAINT nft_metadata_update_observations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: normalized_traces normalized_traces_chain_id_block_number_block_hash_transac_fkey

ALTER TABLE normalized_traces
    ADD CONSTRAINT normalized_traces_chain_id_block_number_block_hash_transac_fkey FOREIGN KEY (chain_id, block_number, block_hash, transaction_index) REFERENCES transaction_inclusions(chain_id, block_number, block_hash, tx_index);

-- Fk Constraint: operator_labels operator_labels_chain_id_fkey

ALTER TABLE ONLY operator_labels
    ADD CONSTRAINT operator_labels_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: proxy_artifact_resolutions proxy_artifact_resolutions_chain_id_fkey

ALTER TABLE ONLY proxy_artifact_resolutions
    ADD CONSTRAINT proxy_artifact_resolutions_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: proxy_artifact_resolutions proxy_artifact_resolutions_chain_id_proxy_address_observat_fkey

ALTER TABLE ONLY proxy_artifact_resolutions
    ADD CONSTRAINT proxy_artifact_resolutions_chain_id_proxy_address_observat_fkey FOREIGN KEY (chain_id, proxy_address, observation_block_hash, observation_stage_version) REFERENCES proxy_observations(chain_id, proxy_address, block_hash, stage_version) ON DELETE RESTRICT;

-- Fk Constraint: proxy_artifact_resolutions proxy_artifact_resolutions_durable_job_id_fkey

ALTER TABLE ONLY proxy_artifact_resolutions
    ADD CONSTRAINT proxy_artifact_resolutions_durable_job_id_fkey FOREIGN KEY (durable_job_id) REFERENCES durable_jobs(id);

-- Fk Constraint: proxy_artifact_resolutions proxy_artifact_resolutions_implementation_artifact_job_id_fkey

ALTER TABLE ONLY proxy_artifact_resolutions
    ADD CONSTRAINT proxy_artifact_resolutions_implementation_artifact_job_id_fkey FOREIGN KEY (implementation_artifact_job_id) REFERENCES verified_contract_proxy_artifacts(verification_job_id);

-- Fk Constraint: proxy_artifact_resolutions proxy_artifact_resolutions_proxy_artifact_job_id_fkey

ALTER TABLE ONLY proxy_artifact_resolutions
    ADD CONSTRAINT proxy_artifact_resolutions_proxy_artifact_job_id_fkey FOREIGN KEY (proxy_artifact_job_id) REFERENCES verified_contract_proxy_artifacts(verification_job_id);

-- Fk Constraint: proxy_detection_evidence proxy_detection_evidence_chain_id_block_number_block_hash_fkey

ALTER TABLE ONLY proxy_detection_evidence
    ADD CONSTRAINT proxy_detection_evidence_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: proxy_detection_evidence proxy_detection_evidence_chain_id_fkey

ALTER TABLE ONLY proxy_detection_evidence
    ADD CONSTRAINT proxy_detection_evidence_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: proxy_detection_evidence proxy_detection_evidence_durable_job_id_fkey

ALTER TABLE ONLY proxy_detection_evidence
    ADD CONSTRAINT proxy_detection_evidence_durable_job_id_fkey FOREIGN KEY (durable_job_id) REFERENCES durable_jobs(id);

-- Fk Constraint: proxy_history_epochs proxy_history_epochs_chain_id_fkey

ALTER TABLE ONLY proxy_history_epochs
    ADD CONSTRAINT proxy_history_epochs_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: proxy_history_epochs proxy_history_epochs_durable_job_id_fkey

ALTER TABLE ONLY proxy_history_epochs
    ADD CONSTRAINT proxy_history_epochs_durable_job_id_fkey FOREIGN KEY (durable_job_id) REFERENCES durable_jobs(id);

-- Fk Constraint: proxy_initialization_events proxy_initialization_events_chain_id_block_number_block_h_fkey1

ALTER TABLE ONLY proxy_initialization_events
    ADD CONSTRAINT proxy_initialization_events_chain_id_block_number_block_h_fkey1 FOREIGN KEY (chain_id, block_number, block_hash, log_index) REFERENCES logs(chain_id, block_number, block_hash, log_index);

-- Fk Constraint: proxy_initialization_events proxy_initialization_events_chain_id_block_number_block_ha_fkey

ALTER TABLE ONLY proxy_initialization_events
    ADD CONSTRAINT proxy_initialization_events_chain_id_block_number_block_ha_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: proxy_initialization_events proxy_initialization_events_chain_id_fkey

ALTER TABLE ONLY proxy_initialization_events
    ADD CONSTRAINT proxy_initialization_events_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: proxy_interaction_coverage_ranges proxy_interaction_coverage_ranges_chain_id_fkey

ALTER TABLE ONLY proxy_interaction_coverage_ranges
    ADD CONSTRAINT proxy_interaction_coverage_ranges_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: proxy_interaction_covered_blocks proxy_interaction_covered_blo_chain_id_block_number_block__fkey

ALTER TABLE ONLY proxy_interaction_covered_blocks
    ADD CONSTRAINT proxy_interaction_covered_blo_chain_id_block_number_block__fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: proxy_interaction_covered_blocks proxy_interaction_covered_blocks_chain_id_fkey

ALTER TABLE ONLY proxy_interaction_covered_blocks
    ADD CONSTRAINT proxy_interaction_covered_blocks_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: proxy_observation_generations proxy_observation_generations_chain_id_fkey

ALTER TABLE ONLY proxy_observation_generations
    ADD CONSTRAINT proxy_observation_generations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: proxy_observation_generations proxy_observation_generations_chain_id_proxy_address_obser_fkey

ALTER TABLE ONLY proxy_observation_generations
    ADD CONSTRAINT proxy_observation_generations_chain_id_proxy_address_obser_fkey FOREIGN KEY (chain_id, proxy_address, observation_block_hash, observation_stage_version) REFERENCES proxy_observations(chain_id, proxy_address, block_hash, stage_version) ON DELETE RESTRICT;

-- Fk Constraint: proxy_observation_generations proxy_observation_generations_durable_job_id_fkey

ALTER TABLE ONLY proxy_observation_generations
    ADD CONSTRAINT proxy_observation_generations_durable_job_id_fkey FOREIGN KEY (durable_job_id) REFERENCES durable_jobs(id);

-- Fk Constraint: proxy_observations proxy_observations_chain_id_block_number_block_hash_fkey

ALTER TABLE ONLY proxy_observations
    ADD CONSTRAINT proxy_observations_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: proxy_observations proxy_observations_chain_id_fkey

ALTER TABLE ONLY proxy_observations
    ADD CONSTRAINT proxy_observations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: proxy_replay_targets proxy_replay_targets_chain_id_block_number_block_hash_fkey

ALTER TABLE ONLY proxy_replay_targets
    ADD CONSTRAINT proxy_replay_targets_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: proxy_replay_targets proxy_replay_targets_chain_id_fkey

ALTER TABLE ONLY proxy_replay_targets
    ADD CONSTRAINT proxy_replay_targets_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: proxy_replay_targets proxy_replay_targets_source_verification_job_id_fkey

ALTER TABLE ONLY proxy_replay_targets
    ADD CONSTRAINT proxy_replay_targets_source_verification_job_id_fkey FOREIGN KEY (source_verification_job_id) REFERENCES verification_jobs(id);

-- Fk Constraint: proxy_upgrade_events proxy_upgrade_events_chain_id_block_number_block_hash_fkey

ALTER TABLE ONLY proxy_upgrade_events
    ADD CONSTRAINT proxy_upgrade_events_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: proxy_upgrade_events proxy_upgrade_events_chain_id_block_number_block_hash_log__fkey

ALTER TABLE ONLY proxy_upgrade_events
    ADD CONSTRAINT proxy_upgrade_events_chain_id_block_number_block_hash_log__fkey FOREIGN KEY (chain_id, block_number, block_hash, log_index) REFERENCES logs(chain_id, block_number, block_hash, log_index);

-- Fk Constraint: proxy_upgrade_events proxy_upgrade_events_chain_id_fkey

ALTER TABLE ONLY proxy_upgrade_events
    ADD CONSTRAINT proxy_upgrade_events_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: receipts receipts_chain_id_block_number_block_hash_tx_index_fkey

ALTER TABLE receipts
    ADD CONSTRAINT receipts_chain_id_block_number_block_hash_tx_index_fkey FOREIGN KEY (chain_id, block_number, block_hash, tx_index) REFERENCES transaction_inclusions(chain_id, block_number, block_hash, tx_index);

-- Fk Constraint: reorg_events reorg_events_chain_id_fkey

ALTER TABLE ONLY reorg_events
    ADD CONSTRAINT reorg_events_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: repair_requests repair_requests_chain_id_fkey

ALTER TABLE ONLY repair_requests
    ADD CONSTRAINT repair_requests_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: runtime_events runtime_events_chain_id_fkey

ALTER TABLE ONLY runtime_events
    ADD CONSTRAINT runtime_events_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: search_catalog_documents search_catalog_documents_chain_id_fkey

ALTER TABLE ONLY search_catalog_documents
    ADD CONSTRAINT search_catalog_documents_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: search_catalog_generations search_catalog_generations_chain_id_fkey

ALTER TABLE ONLY search_catalog_generations
    ADD CONSTRAINT search_catalog_generations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: sync_runtime_status sync_runtime_status_chain_id_fkey

ALTER TABLE ONLY sync_runtime_status
    ADD CONSTRAINT sync_runtime_status_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: sync_runtime_status_writer_leases sync_runtime_status_writer_leases_chain_id_fkey

ALTER TABLE ONLY sync_runtime_status_writer_leases
    ADD CONSTRAINT sync_runtime_status_writer_leases_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: token_balance_deltas token_balance_deltas_chain_id_block_number_block_hash_log__fkey

ALTER TABLE token_balance_deltas
    ADD CONSTRAINT token_balance_deltas_chain_id_block_number_block_hash_log__fkey FOREIGN KEY (chain_id, block_number, block_hash, log_index, sub_index) REFERENCES token_events(chain_id, block_number, block_hash, log_index, sub_index);

-- Fk Constraint: token_contracts token_contracts_chain_id_fkey

ALTER TABLE ONLY token_contracts
    ADD CONSTRAINT token_contracts_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: token_contracts token_contracts_chain_id_observed_block_number_observed_bl_fkey

ALTER TABLE ONLY token_contracts
    ADD CONSTRAINT token_contracts_chain_id_observed_block_number_observed_bl_fkey FOREIGN KEY (chain_id, observed_block_number, observed_block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: token_events token_events_chain_id_block_number_block_hash_log_index_fkey

ALTER TABLE token_events
    ADD CONSTRAINT token_events_chain_id_block_number_block_hash_log_index_fkey FOREIGN KEY (chain_id, block_number, block_hash, log_index) REFERENCES logs(chain_id, block_number, block_hash, log_index);

-- Fk Constraint: trace_log_attributions trace_log_attributions_chain_id_block_number_block_hash_lo_fkey

ALTER TABLE trace_log_attributions
    ADD CONSTRAINT trace_log_attributions_chain_id_block_number_block_hash_lo_fkey FOREIGN KEY (chain_id, block_number, block_hash, log_index) REFERENCES logs(chain_id, block_number, block_hash, log_index);

-- Fk Constraint: trace_log_attributions trace_log_attributions_chain_id_block_number_block_hash_tr_fkey

ALTER TABLE trace_log_attributions
    ADD CONSTRAINT trace_log_attributions_chain_id_block_number_block_hash_tr_fkey FOREIGN KEY (chain_id, block_number, block_hash, transaction_hash, trace_path) REFERENCES normalized_traces(chain_id, block_number, block_hash, transaction_hash, trace_path);

-- Fk Constraint: transaction_effective_execution_identities transaction_effective_executi_chain_id_block_number_block__fkey

ALTER TABLE transaction_effective_execution_identities
    ADD CONSTRAINT transaction_effective_executi_chain_id_block_number_block__fkey FOREIGN KEY (chain_id, block_number, block_hash, transaction_index, transaction_hash) REFERENCES transaction_inclusions(chain_id, block_number, block_hash, tx_index, tx_hash);

-- Fk Constraint: transaction_execution_code_resolutions transaction_execution_code_re_chain_id_block_number_block__fkey

ALTER TABLE transaction_execution_code_resolutions
    ADD CONSTRAINT transaction_execution_code_re_chain_id_block_number_block__fkey FOREIGN KEY (chain_id, block_number, block_hash, transaction_index) REFERENCES transaction_inclusions(chain_id, block_number, block_hash, tx_index);

-- Fk Constraint: transaction_inclusions transaction_inclusions_chain_id_block_number_block_hash_fkey

ALTER TABLE transaction_inclusions
    ADD CONSTRAINT transaction_inclusions_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: transaction_inclusions transaction_inclusions_chain_id_tx_hash_fkey

ALTER TABLE transaction_inclusions
    ADD CONSTRAINT transaction_inclusions_chain_id_tx_hash_fkey FOREIGN KEY (chain_id, tx_hash) REFERENCES transactions(chain_id, hash);

-- Fk Constraint: transaction_state_changes transaction_state_changes_chain_id_block_number_block_hash_fkey

ALTER TABLE transaction_state_changes
    ADD CONSTRAINT transaction_state_changes_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash, transaction_index) REFERENCES transaction_inclusions(chain_id, block_number, block_hash, tx_index);

-- Fk Constraint: transactional_outbox transactional_outbox_chain_id_fkey

ALTER TABLE ONLY transactional_outbox
    ADD CONSTRAINT transactional_outbox_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: transactions transactions_chain_id_fkey

ALTER TABLE ONLY transactions
    ADD CONSTRAINT transactions_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: user_sessions user_sessions_user_id_fkey

ALTER TABLE ONLY user_sessions
    ADD CONSTRAINT user_sessions_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id);

-- Fk Constraint: users users_chain_id_fkey

ALTER TABLE ONLY users
    ADD CONSTRAINT users_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: uups_implementation_observations uups_implementation_observati_chain_id_block_number_block__fkey

ALTER TABLE ONLY uups_implementation_observations
    ADD CONSTRAINT uups_implementation_observati_chain_id_block_number_block__fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash);

-- Fk Constraint: uups_implementation_observation_generations uups_implementation_observati_chain_id_implementation_addr_fkey

ALTER TABLE ONLY uups_implementation_observation_generations
    ADD CONSTRAINT uups_implementation_observati_chain_id_implementation_addr_fkey FOREIGN KEY (chain_id, implementation_address, observation_block_hash, observation_stage_version, verification_job_id) REFERENCES uups_implementation_observations(chain_id, implementation_address, block_hash, stage_version, verification_job_id) ON DELETE RESTRICT;

-- Fk Constraint: uups_implementation_observation_generations uups_implementation_observation_generations_chain_id_fkey

ALTER TABLE ONLY uups_implementation_observation_generations
    ADD CONSTRAINT uups_implementation_observation_generations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: uups_implementation_observation_generations uups_implementation_observation_generations_durable_job_id_fkey

ALTER TABLE ONLY uups_implementation_observation_generations
    ADD CONSTRAINT uups_implementation_observation_generations_durable_job_id_fkey FOREIGN KEY (durable_job_id) REFERENCES durable_jobs(id);

-- Fk Constraint: uups_implementation_observations uups_implementation_observations_chain_id_fkey

ALTER TABLE ONLY uups_implementation_observations
    ADD CONSTRAINT uups_implementation_observations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: uups_implementation_observations uups_implementation_observations_verification_job_id_fkey

ALTER TABLE ONLY uups_implementation_observations
    ADD CONSTRAINT uups_implementation_observations_verification_job_id_fkey FOREIGN KEY (verification_job_id) REFERENCES verified_contract_proxy_artifacts(verification_job_id);

-- Fk Constraint: verification_compilation_contracts verification_compilation_contracts_compilation_id_fkey

ALTER TABLE ONLY verification_compilation_contracts
    ADD CONSTRAINT verification_compilation_contracts_compilation_id_fkey FOREIGN KEY (compilation_id) REFERENCES verification_compilation_units(id) ON DELETE RESTRICT;

-- Fk Constraint: verification_compilation_units verification_compilation_units_job_digest_fk

ALTER TABLE ONLY verification_compilation_units
    ADD CONSTRAINT verification_compilation_units_job_digest_fk FOREIGN KEY (source_job_id, request_digest) REFERENCES verification_results(job_id, request_digest) ON DELETE RESTRICT;

-- Fk Constraint: verification_compilation_units verification_compilation_units_source_job_id_fkey

ALTER TABLE ONLY verification_compilation_units
    ADD CONSTRAINT verification_compilation_units_source_job_id_fkey FOREIGN KEY (source_job_id) REFERENCES verification_jobs(id) ON DELETE RESTRICT;

-- Fk Constraint: verification_jobs verification_jobs_catalog_fk

ALTER TABLE ONLY verification_jobs
    ADD CONSTRAINT verification_jobs_catalog_fk FOREIGN KEY (catalog_generation_id, catalog_language, compiler_version, compiler_platform, compiler_digest) REFERENCES compiler_catalog_entries(generation_id, language, version, platform, artifact_sha256) ON DELETE RESTRICT;

-- Fk Constraint: verification_jobs verification_jobs_chain_id_fkey

ALTER TABLE ONLY verification_jobs
    ADD CONSTRAINT verification_jobs_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: verification_results verification_results_job_id_fkey

ALTER TABLE ONLY verification_results
    ADD CONSTRAINT verification_results_job_id_fkey FOREIGN KEY (job_id) REFERENCES verification_jobs(id) ON DELETE RESTRICT;

-- Fk Constraint: verified_contract_proxy_artifacts verified_contract_proxy_artif_chain_id_address_code_hash_v_fkey

ALTER TABLE ONLY verified_contract_proxy_artifacts
    ADD CONSTRAINT verified_contract_proxy_artif_chain_id_address_code_hash_v_fkey FOREIGN KEY (chain_id, address, code_hash, valid_from_block, verification_job_id) REFERENCES verified_contracts(chain_id, address, code_hash, valid_from_block, verification_job_id) ON DELETE RESTRICT;

-- Fk Constraint: verified_contract_proxy_artifacts verified_contract_proxy_artif_verification_job_id_request__fkey

ALTER TABLE ONLY verified_contract_proxy_artifacts
    ADD CONSTRAINT verified_contract_proxy_artif_verification_job_id_request__fkey FOREIGN KEY (verification_job_id, request_digest) REFERENCES verification_results(job_id, request_digest) ON DELETE RESTRICT;

-- Fk Constraint: verified_contract_proxy_artifacts verified_contract_proxy_artifacts_chain_id_fkey

ALTER TABLE ONLY verified_contract_proxy_artifacts
    ADD CONSTRAINT verified_contract_proxy_artifacts_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: verified_contracts verified_contracts_chain_id_fkey

ALTER TABLE ONLY verified_contracts
    ADD CONSTRAINT verified_contracts_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: verified_contracts verified_contracts_result_fk

ALTER TABLE ONLY verified_contracts
    ADD CONSTRAINT verified_contracts_result_fk FOREIGN KEY (verification_job_id, request_digest) REFERENCES verification_results(job_id, request_digest) ON DELETE RESTRICT;

-- Fk Constraint: verified_function_selector_sets verified_function_selector_sets_contract_fk

ALTER TABLE ONLY verified_function_selector_sets
    ADD CONSTRAINT verified_function_selector_sets_contract_fk FOREIGN KEY (chain_id, address, code_hash, valid_from_block, verification_job_id) REFERENCES verified_contracts(chain_id, address, code_hash, valid_from_block, verification_job_id) ON DELETE CASCADE;

-- Fk Constraint: verified_function_selector_sets verified_function_selector_sets_result_fk

ALTER TABLE ONLY verified_function_selector_sets
    ADD CONSTRAINT verified_function_selector_sets_result_fk FOREIGN KEY (verification_job_id, request_digest) REFERENCES verification_results(job_id, request_digest) ON DELETE RESTRICT;

-- Fk Constraint: verified_function_selectors verified_function_selectors_set_fk

ALTER TABLE ONLY verified_function_selectors
    ADD CONSTRAINT verified_function_selectors_set_fk FOREIGN KEY (verification_job_id, chain_id, address, code_hash) REFERENCES verified_function_selector_sets(verification_job_id, chain_id, address, code_hash) ON DELETE CASCADE;

-- Fk Constraint: verified_proxy_bindings verified_proxy_bindings_artifact_resolution_id_fkey

ALTER TABLE ONLY verified_proxy_bindings
    ADD CONSTRAINT verified_proxy_bindings_artifact_resolution_id_fkey FOREIGN KEY (artifact_resolution_id) REFERENCES proxy_artifact_resolutions(id);

-- Fk Constraint: verified_proxy_bindings verified_proxy_bindings_beacon_generation_id_fkey

ALTER TABLE ONLY verified_proxy_bindings
    ADD CONSTRAINT verified_proxy_bindings_beacon_generation_id_fkey FOREIGN KEY (beacon_generation_id) REFERENCES beacon_observation_generations(id);

-- Fk Constraint: verified_proxy_bindings verified_proxy_bindings_chain_id_fkey

ALTER TABLE ONLY verified_proxy_bindings
    ADD CONSTRAINT verified_proxy_bindings_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id);

-- Fk Constraint: verified_proxy_bindings verified_proxy_bindings_observation_fk

ALTER TABLE ONLY verified_proxy_bindings
    ADD CONSTRAINT verified_proxy_bindings_observation_fk FOREIGN KEY (chain_id, proxy_address, observation_block_hash, observation_stage_version) REFERENCES proxy_observations(chain_id, proxy_address, block_hash, stage_version) ON DELETE RESTRICT;

-- Fk Constraint: verified_proxy_bindings verified_proxy_bindings_observation_generation_id_fkey

ALTER TABLE ONLY verified_proxy_bindings
    ADD CONSTRAINT verified_proxy_bindings_observation_generation_id_fkey FOREIGN KEY (observation_generation_id) REFERENCES proxy_observation_generations(id);

-- Fk Constraint: verified_proxy_bindings verified_proxy_bindings_result_fk

ALTER TABLE ONLY verified_proxy_bindings
    ADD CONSTRAINT verified_proxy_bindings_result_fk FOREIGN KEY (verification_job_id, request_digest) REFERENCES verification_results(job_id, request_digest) ON DELETE RESTRICT;

-- Fk Constraint: verified_proxy_bindings verified_proxy_bindings_uups_generation_id_fkey

ALTER TABLE ONLY verified_proxy_bindings
    ADD CONSTRAINT verified_proxy_bindings_uups_generation_id_fkey FOREIGN KEY (uups_generation_id) REFERENCES uups_implementation_observation_generations(id);

-- Fk Constraint: watch_notifications watch_notifications_user_id_fkey

ALTER TABLE ONLY watch_notifications
    ADD CONSTRAINT watch_notifications_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id);

-- Fk Constraint: watch_notifications watch_notifications_watch_id_fkey

ALTER TABLE ONLY watch_notifications
    ADD CONSTRAINT watch_notifications_watch_id_fkey FOREIGN KEY (watch_id) REFERENCES address_watches(id);

-- Fk Constraint: withdrawals withdrawals_chain_id_block_number_block_hash_fkey

ALTER TABLE withdrawals
    ADD CONSTRAINT withdrawals_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash);

-- Pin function lookup to this migration schema, including isolated test schemas.
DO $$
BEGIN
    EXECUTE format('ALTER FUNCTION %I.enforce_enrichment_terminal_publication() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.enforce_erc4337_canonical_user_op_hash() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.enforce_proxy_artifact_resolution() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.enforce_proxy_detection_generation() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.enforce_proxy_observation_generation() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.enforce_unbound_verification_job_insert() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.enforce_uups_implementation_observation() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.enforce_uups_implementation_observation_mutation() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.enforce_uups_implementation_observation_witness() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.enforce_verified_contract_proxy_artifact() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.enforce_verified_proxy_binding() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.enforce_verifier_v2_publication() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.enqueue_derived_forward_event_after_stage_publication() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.erc4337_add_covered_block(target_chain_id numeric, target_configuration_digest bytea, target_block_number numeric, target_block_hash bytea, target_durable_job_id bigint, target_job_generation bigint) SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.erc4337_remove_block_coverage(target_chain_id numeric, target_block_number numeric, target_block_hash bytea) SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.erc4337_remove_covered_block(target_chain_id numeric, target_configuration_digest bytea, target_block_number numeric) SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.erc4337_remove_detached_coverage() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.etherview_guard_erc20_balance_reconciliation() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.etherview_guard_erc20_holder_fact() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.etherview_guard_nft_metadata_update_observation() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.native_transfer_canonical_coverage() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.native_transfer_job_coverage() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.native_transfer_outbox_coverage() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.native_transfer_result_coverage() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.proxy_interaction_coverage_contains(target_chain_id numeric, target_start_block numeric, target_start_hash bytea, target_end_block numeric, target_end_hash bytea) SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.prune_search_catalog(requested_chain_id numeric, requested_retention_generations bigint) SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.record_ens_name_search_document() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.record_search_catalog_document() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.record_verified_contract_search_catalog_document() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.refresh_native_transfer_coverage(p_chain numeric, p_number numeric) SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.refresh_proxy_interaction_coverage_block(target_chain_id numeric, target_block_number numeric) SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.refresh_proxy_interaction_coverage_from_canonical() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.refresh_proxy_interaction_coverage_from_journal() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.refresh_proxy_interaction_coverage_from_outbox() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.refresh_proxy_interaction_coverage_from_stage_result() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.reject_durable_stage_publication_mutation() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.reject_proxy_v2_evidence_mutation() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.reject_verified_proxy_binding_mutation() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.reject_verifier_v2_result_mutation() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.require_enrichment_publication_protocol() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.synchronize_derived_attempt_trace_canonicality() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
END;
$$;
