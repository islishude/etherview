-- P70-T29 removes Vyper and the remote compiler-runner. Operators must stop
-- legacy verifier workers and back up verification data before applying this
-- irreversible migration. Old and new application versions may not overlap.

DROP TRIGGER verification_results_v2_immutable ON verification_results;
DROP TRIGGER verified_proxy_contracts_immutable ON verified_proxy_contracts;

CREATE TEMPORARY TABLE migration_0031_vyper_jobs (
    id UUID PRIMARY KEY
) ON COMMIT DROP;

INSERT INTO migration_0031_vyper_jobs (id)
SELECT id
FROM verification_jobs
WHERE language = 'vyper'
   OR kind IN ('vyper_multipart', 'vyper_standard_json');

-- A proxy publication whose implementation source came from a Vyper
-- publication must not survive the removal of that source authority.
INSERT INTO migration_0031_vyper_jobs (id)
SELECT DISTINCT proxy.verification_job_id
FROM verified_proxy_contracts AS proxy
JOIN verified_contracts AS implementation
  ON implementation.chain_id = proxy.chain_id
 AND implementation.address = proxy.implementation_address
 AND implementation.code_hash = proxy.implementation_code_hash
WHERE implementation.language = 'vyper'
ON CONFLICT (id) DO NOTHING;

DELETE FROM verified_proxy_contracts
WHERE verification_job_id IN (SELECT id FROM migration_0031_vyper_jobs);

DELETE FROM verified_contracts
WHERE language = 'vyper';

DELETE FROM verification_results
WHERE job_id IN (SELECT id FROM migration_0031_vyper_jobs);

DELETE FROM verification_jobs
WHERE id IN (SELECT id FROM migration_0031_vyper_jobs);

DELETE FROM compiler_catalog_heads WHERE language = 'vyper';
DELETE FROM compiler_catalog_entries WHERE language = 'vyper';
DELETE FROM compiler_catalog_generations WHERE language = 'vyper';

CREATE TRIGGER verification_results_v2_immutable
BEFORE UPDATE OR DELETE ON verification_results
FOR EACH ROW EXECUTE FUNCTION reject_verifier_v2_result_mutation();

CREATE TRIGGER verified_proxy_contracts_immutable
BEFORE UPDATE OR DELETE ON verified_proxy_contracts
FOR EACH ROW EXECUTE FUNCTION reject_verified_proxy_contract_mutation();

-- No active job may cross the executor cutover.
UPDATE verification_jobs
SET status = 'failed',
    outcome_kind = NULL,
    outcome = NULL,
    error_code = 'executor_migrated',
    leased_by = NULL,
    lease_token = NULL,
    lease_expires_at = NULL,
    updated_at = clock_timestamp()
WHERE status IN ('queued', 'running')
  AND kind NOT IN ('sourcify', 'sourcify_from_etherscan', 'proxy');

-- The terminal-state update queues the verifier-v2 deferred consistency
-- trigger. Settle it before altering verification_jobs, then restore the
-- transaction's normal deferred behavior for later writes.
SET CONSTRAINTS ALL IMMEDIATE;
SET CONSTRAINTS ALL DEFERRED;

DROP TRIGGER verification_jobs_deferred_compiler_provenance
    ON verification_jobs;
DROP FUNCTION enforce_deferred_compiler_provenance();

ALTER TABLE verification_jobs
    DROP CONSTRAINT verification_jobs_kind_check,
    DROP CONSTRAINT verification_jobs_language_check,
    DROP CONSTRAINT verification_jobs_catalog_language_check,
    DROP CONSTRAINT verification_jobs_compiler_check,
    DROP CONSTRAINT verification_jobs_runner_check;

ALTER TABLE verification_jobs
    RENAME COLUMN runner_digest TO executor_digest;

ALTER TABLE verification_jobs
    ADD COLUMN executor_kind TEXT,
    ADD COLUMN execution_policy TEXT;

UPDATE verification_jobs
SET executor_kind = CASE
        WHEN executor_digest IS NULL THEN 'legacy_process'
        ELSE 'legacy_runner'
    END,
    execution_policy = CASE
        WHEN executor_digest IS NULL THEN 'legacy_trusted_process'
        ELSE 'legacy_hard_isolation'
    END
WHERE compiler_digest IS NOT NULL;

ALTER TABLE verification_jobs
    DROP COLUMN requires_hard_isolation;

ALTER TABLE compiler_catalog_generations
    DROP CONSTRAINT compiler_catalog_generations_language_check,
    ADD CONSTRAINT compiler_catalog_generations_language_check
        CHECK (language = 'solidity');

ALTER TABLE compiler_catalog_entries
    DROP CONSTRAINT compiler_catalog_entries_language_check,
    ADD CONSTRAINT compiler_catalog_entries_language_check
        CHECK (language = 'solidity');

ALTER TABLE compiler_catalog_heads
    DROP CONSTRAINT compiler_catalog_heads_language_check,
    ADD CONSTRAINT compiler_catalog_heads_language_check
        CHECK (language = 'solidity');

ALTER TABLE verification_jobs
    ADD CONSTRAINT verification_jobs_kind_check CHECK (
        kind IN (
            'address',
            'solidity_multipart',
            'solidity_standard_json',
            'solidity_batch_multipart',
            'solidity_batch_standard_json',
            'sourcify',
            'sourcify_from_etherscan',
            'proxy'
        )
    ),
    ADD CONSTRAINT verification_jobs_language_check
        CHECK (language IS NULL OR language IN ('solidity', 'yul')),
    ADD CONSTRAINT verification_jobs_catalog_language_check
        CHECK (catalog_language IS NULL OR catalog_language = 'solidity'),
    ADD CONSTRAINT verification_jobs_compiler_check CHECK (
        (
            kind IN ('sourcify', 'sourcify_from_etherscan', 'proxy') AND
            language IS NULL AND compiler_version IS NULL AND
            compiler_platform IS NULL AND catalog_language IS NULL AND
            catalog_generation_id IS NULL AND compiler_digest IS NULL AND
            executor_kind IS NULL AND execution_policy IS NULL AND
            executor_digest IS NULL
        ) OR (
            kind NOT IN ('sourcify', 'sourcify_from_etherscan', 'proxy') AND
            language IN ('solidity', 'yul') AND
            length(compiler_version) BETWEEN 1 AND 128 AND
            catalog_language = 'solidity' AND
            (
                (
                    compiler_platform IS NULL AND
                    catalog_generation_id IS NULL AND
                    compiler_digest IS NULL AND
                    executor_kind IS NULL AND
                    execution_policy IS NULL AND
                    executor_digest IS NULL AND
                    status <> 'succeeded'
                ) OR (
                    compiler_platform IN (
                        'bin', 'emscripten-asmjs', 'emscripten-wasm32',
                        'linux-amd64', 'linux-arm64', 'macosx-amd64',
                        'wasm', 'windows-amd64'
                    ) AND
                    catalog_generation_id IS NOT NULL AND
                    octet_length(compiler_digest) = 32 AND
                    (
                        (
                            executor_kind = 'node_solcjs_v1' AND
                            execution_policy = 'trusted_subprocess' AND
                            octet_length(executor_digest) = 32 AND
                            compiler_platform = 'emscripten-wasm32'
                        ) OR (
                            executor_kind = 'legacy_runner' AND
                            execution_policy = 'legacy_hard_isolation' AND
                            octet_length(executor_digest) = 32
                        ) OR (
                            executor_kind = 'legacy_process' AND
                            execution_policy = 'legacy_trusted_process' AND
                            executor_digest IS NULL
                        )
                    )
                )
            )
        )
    );

ALTER TABLE verification_results
    DROP CONSTRAINT verification_results_success_shape_check,
    ADD CONSTRAINT verification_results_success_shape_check CHECK (
        (outcome_kind <> 'verification_success' AND
            file_name IS NULL AND contract_name IS NULL AND language IS NULL AND
            compiler_version IS NULL AND match_type IS NULL AND abi IS NULL AND
            sources IS NULL AND settings IS NULL AND compilation_artifacts IS NULL AND
            creation_code_artifacts IS NULL AND runtime_code_artifacts IS NULL AND
            constructor_arguments IS NULL AND libraries IS NULL AND is_blueprint IS NULL) OR
        (outcome_kind = 'verification_success' AND
            length(file_name) BETWEEN 1 AND 512 AND
            length(contract_name) BETWEEN 1 AND 256 AND
            language IN ('solidity', 'yul') AND
            length(compiler_version) BETWEEN 1 AND 128 AND
            match_type IN ('full', 'partial') AND
            (abi IS NULL OR jsonb_typeof(abi) = 'array') AND
            jsonb_typeof(sources) = 'object' AND jsonb_typeof(settings) = 'object' AND
            jsonb_typeof(compilation_artifacts) = 'object' AND
            jsonb_typeof(creation_code_artifacts) = 'object' AND
            jsonb_typeof(runtime_code_artifacts) = 'object' AND
            jsonb_typeof(libraries) = 'object' AND is_blueprint IS NOT NULL)
    );

ALTER TABLE verified_contracts
    DROP CONSTRAINT verified_contracts_artifact_check,
    ADD CONSTRAINT verified_contracts_artifact_check CHECK (
        length(file_name) BETWEEN 1 AND 512 AND
        length(contract_name) BETWEEN 1 AND 256 AND
        language IN ('solidity', 'yul') AND
        length(compiler_version) BETWEEN 1 AND 128 AND
        match_type IN ('full', 'partial') AND
        (abi IS NULL OR jsonb_typeof(abi) = 'array') AND
        jsonb_typeof(sources) = 'object' AND jsonb_typeof(settings) = 'object' AND
        jsonb_typeof(compilation_artifacts) = 'object' AND
        jsonb_typeof(creation_code_artifacts) = 'object' AND
        jsonb_typeof(runtime_code_artifacts) = 'object' AND
        jsonb_typeof(libraries) = 'object'
    );

CREATE OR REPLACE FUNCTION enforce_solcjs_compiler_provenance()
RETURNS trigger
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
       NEW.compiler_platform = 'emscripten-wasm32' AND
       NEW.catalog_generation_id IS NOT NULL AND
       octet_length(NEW.compiler_digest) = 32 AND
       NEW.executor_kind = 'node_solcjs_v1' AND
       NEW.execution_policy = 'trusted_subprocess' AND
       octet_length(NEW.executor_digest) = 32 AND
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

CREATE TRIGGER verification_jobs_solcjs_compiler_provenance
BEFORE UPDATE OF
    compiler_platform, catalog_generation_id, compiler_digest,
    executor_kind, execution_policy, executor_digest
ON verification_jobs
FOR EACH ROW EXECUTE FUNCTION enforce_solcjs_compiler_provenance();

CREATE OR REPLACE FUNCTION enforce_unbound_solcjs_job_insert()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.kind NOT IN ('sourcify', 'sourcify_from_etherscan', 'proxy') AND
       (
           NEW.compiler_platform IS NOT NULL OR
           NEW.catalog_generation_id IS NOT NULL OR
           NEW.compiler_digest IS NOT NULL OR
           NEW.executor_kind IS NOT NULL OR
           NEW.execution_policy IS NOT NULL OR
           NEW.executor_digest IS NOT NULL
       ) THEN
        RAISE EXCEPTION 'new verification jobs must bind solc-js provenance under an active lease';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER verification_jobs_unbound_solcjs_insert
BEFORE INSERT ON verification_jobs
FOR EACH ROW EXECUTE FUNCTION enforce_unbound_solcjs_job_insert();

DO $migration$
DECLARE
    migration_schema TEXT := current_schema();
BEGIN
    IF migration_schema IS NULL THEN
        RAISE EXCEPTION 'solc-js executor migration requires a current schema';
    END IF;
    EXECUTE format(
        'ALTER FUNCTION %I.enforce_solcjs_compiler_provenance() SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
    EXECUTE format(
        'ALTER FUNCTION %I.enforce_unbound_solcjs_job_insert() SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
END
$migration$;
