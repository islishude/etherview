-- ADR-0049 / P30-T107. Do not rebind work owned by the removed executor.
LOCK TABLE verification_jobs IN SHARE ROW EXCLUSIVE MODE;
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM verification_jobs WHERE language = 'vyper' AND status IN ('queued','running')) THEN
        RAISE EXCEPTION 'drain bound Vyper verification jobs before upgrading';
    END IF;
END $$;
ALTER TABLE compiler_catalog_generations DROP CONSTRAINT compiler_catalog_generations_language_check, ADD CONSTRAINT compiler_catalog_generations_language_check CHECK (language IN ('solidity','vyper'));
ALTER TABLE compiler_catalog_entries DROP CONSTRAINT compiler_catalog_entries_language_check, ADD CONSTRAINT compiler_catalog_entries_language_check CHECK (language IN ('solidity','vyper'));
ALTER TABLE compiler_catalog_heads DROP CONSTRAINT compiler_catalog_heads_language_check, ADD CONSTRAINT compiler_catalog_heads_language_check CHECK (language IN ('solidity','vyper'));
ALTER TABLE compiler_catalog_entries DROP CONSTRAINT compiler_catalog_entries_platform_check,
 ADD CONSTRAINT compiler_catalog_entries_platform_check CHECK (platform IN ('bin','emscripten-asmjs','emscripten-wasm32','linux-amd64','linux-arm64','macosx-amd64','wasm','windows-amd64','python-wheel'));
ALTER TABLE compiler_catalog_entries ADD COLUMN vyper_runtimes JSONB;
ALTER TABLE compiler_catalog_entries ADD COLUMN expires_at TIMESTAMPTZ;
ALTER TABLE verification_jobs DROP CONSTRAINT verification_jobs_catalog_language_check,
 ADD CONSTRAINT verification_jobs_catalog_language_check CHECK (catalog_language IS NULL OR catalog_language IN ('solidity','vyper'));
ALTER TABLE verification_jobs DROP CONSTRAINT verification_jobs_compiler_check;
ALTER TABLE verification_jobs
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
        ) OR (
            ((kind = 'address' AND language = 'geas' AND compiler_version = '0.3.3') OR
             (kind IN ('address', 'vyper_standard_json', 'vyper_multipart') AND
              language = 'vyper' AND compiler_version = '0.4.3' AND status IN ('succeeded','failed'))) AND catalog_language IS NULL AND
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
                    ((language = 'geas' AND compiler_platform = 'go-module' AND executor_kind = 'etherview_geas_v1') OR
                     (language = 'vyper' AND compiler_platform = 'python-wheel' AND executor_kind = 'etherview_vyper_v1' AND
                      compiler_digest = decode('3b9671727c888363740dc678e60336759871487d0e4e9fdd973048fa9635c4fd', 'hex'))) AND
                    catalog_generation_id IS NULL AND octet_length(compiler_digest) = 32 AND
                    execution_policy = 'trusted_subprocess' AND
                    octet_length(executor_digest) = 32
                )
            )
        ) OR (
            kind IN ('address','vyper_standard_json','vyper_multipart') AND
            language = 'vyper' AND catalog_language = 'vyper' AND
            compiler_version ~ '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' AND
            ((compiler_platform IS NULL AND catalog_generation_id IS NULL AND compiler_digest IS NULL AND executor_kind IS NULL AND execution_policy IS NULL AND executor_digest IS NULL AND status <> 'succeeded') OR
             (compiler_platform = 'python-wheel' AND catalog_generation_id IS NOT NULL AND octet_length(compiler_digest) = 32 AND executor_kind = 'etherview_vyper_v3' AND execution_policy = 'trusted_subprocess' AND octet_length(executor_digest) = 32))
        ) IS TRUE
    );

ALTER TABLE verification_jobs DROP CONSTRAINT verification_jobs_vyper_request_check;
ALTER TABLE verification_jobs ADD CONSTRAINT verification_jobs_vyper_request_check CHECK (
 (kind NOT IN ('vyper_standard_json','vyper_multipart') OR language = 'vyper') AND
 (language IS DISTINCT FROM 'vyper' OR (
    request->>'target_file' IS NOT NULL AND request #>> '{standard_json,language}' = 'Vyper' AND
    jsonb_typeof(request #> '{standard_json,sources}') = 'object' AND
    (request #> '{standard_json,sources}') ? (request->>'target_file')
 ) IS TRUE));

CREATE FUNCTION check_vyper_runtime_binding() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.executor_kind = 'etherview_vyper_v3' AND NOT EXISTS (
    SELECT 1 FROM compiler_catalog_entries e,
       jsonb_array_elements(e.vyper_runtimes) r
    WHERE e.generation_id = NEW.catalog_generation_id AND e.language = 'vyper'
      AND e.version = NEW.compiler_version AND e.artifact_sha256 = NEW.compiler_digest
      AND r->>'manifest_sha256' = encode(NEW.executor_digest, 'hex')
      AND r->>'protocol' = 'etherview-vyper-runtime-v3'
 ) THEN RAISE EXCEPTION 'Vyper runtime is not in bound catalog generation'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER verification_jobs_vyper_runtime_binding BEFORE INSERT OR UPDATE ON verification_jobs
 FOR EACH ROW EXECUTE FUNCTION check_vyper_runtime_binding();

CREATE OR REPLACE FUNCTION enforce_verification_compiler_provenance()
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
               NEW.executor_kind = 'etherview_vyper_v3' AND
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

