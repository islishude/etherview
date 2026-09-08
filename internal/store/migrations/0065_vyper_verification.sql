-- ADR-0047 / P30-T96: fresh-schema Vyper support; no historical restoration.
LOCK TABLE verification_results IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE verified_contracts IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE verification_jobs IN SHARE ROW EXCLUSIVE MODE;

ALTER TABLE verification_jobs DROP CONSTRAINT verification_jobs_kind_check;
ALTER TABLE verification_jobs ADD CONSTRAINT verification_jobs_kind_check CHECK (
 kind IN ('address','derived','proxy','sourcify','sourcify_from_etherscan',
 'solidity_multipart','solidity_standard_json','solidity_batch_multipart',
 'solidity_batch_standard_json','vyper_multipart','vyper_standard_json'));

ALTER TABLE verification_jobs
    DROP CONSTRAINT verification_jobs_language_check,
    DROP CONSTRAINT verification_jobs_compiler_check;

ALTER TABLE verification_jobs
    ADD CONSTRAINT verification_jobs_language_check
        CHECK (language IS NULL OR language IN ('solidity', 'yul', 'geas', 'vyper')),
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
              language = 'vyper' AND compiler_version = '0.4.3')) AND catalog_language IS NULL AND
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
        ) IS TRUE
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
            language IN ('solidity', 'yul', 'geas', 'vyper') AND
            length(compiler_version) BETWEEN 1 AND 128 AND
            match_type IN ('full', 'partial') AND
            (abi IS NULL OR jsonb_typeof(abi) = 'array') AND
            jsonb_typeof(sources) = 'object' AND jsonb_typeof(settings) = 'object' AND
            jsonb_typeof(compilation_artifacts) = 'object' AND
            jsonb_typeof(creation_code_artifacts) = 'object' AND
            jsonb_typeof(runtime_code_artifacts) = 'object' AND
            jsonb_typeof(libraries) = 'object' AND is_blueprint IS NOT NULL AND
            (language <> 'geas' OR (
                compiler_version = '0.3.3' AND match_type = 'full' AND
                abi = '[]'::jsonb AND compilation_artifacts = '{}'::jsonb AND
                creation_code_artifacts = '{}'::jsonb AND
                runtime_code_artifacts = '{}'::jsonb AND libraries = '{}'::jsonb AND
                constructor_arguments IS NULL AND is_blueprint = FALSE AND
                jsonb_typeof(settings->'runtime_entrypoint') = 'string' AND
                settings->'stack_check' = 'true'::jsonb AND
                settings - ARRAY[
                    'runtime_entrypoint', 'creation_entrypoint', 'stack_check'
                ]::text[] = '{}'::jsonb AND
                valid_geas_verification_sources(sources) AND
                outcome #>> '{runtime_match,match_type}' = 'full' AND
                outcome #> '{runtime_match,transformations}' = '[]'::jsonb AND
                outcome #> '{runtime_match,values}' = '{}'::jsonb AND
                (
                    (
                        jsonb_typeof(settings->'creation_entrypoint') = 'string' AND
                        outcome #>> '{creation_match,match_type}' = 'full' AND
                        outcome #> '{creation_match,transformations}' = '[]'::jsonb AND
                        outcome #> '{creation_match,values}' = '{}'::jsonb
                    ) OR (
                        NOT (settings ? 'creation_entrypoint') AND
                        outcome->'creation_match' = 'null'::jsonb
                    )
                )
            ) IS TRUE))
    );

ALTER TABLE verified_contracts
    DROP CONSTRAINT verified_contracts_artifact_check,
    ADD CONSTRAINT verified_contracts_artifact_check CHECK (
        length(file_name) BETWEEN 1 AND 512 AND
        length(contract_name) BETWEEN 1 AND 256 AND
        language IN ('solidity', 'yul', 'geas', 'vyper') AND
        length(compiler_version) BETWEEN 1 AND 128 AND
        match_type IN ('full', 'partial') AND
        (abi IS NULL OR jsonb_typeof(abi) = 'array') AND
        jsonb_typeof(sources) = 'object' AND jsonb_typeof(settings) = 'object' AND
        jsonb_typeof(compilation_artifacts) = 'object' AND
        jsonb_typeof(creation_code_artifacts) = 'object' AND
        jsonb_typeof(runtime_code_artifacts) = 'object' AND
        jsonb_typeof(libraries) = 'object' AND
        (language <> 'geas' OR (
            compiler_version = '0.3.3' AND match_type = 'full' AND
            abi = '[]'::jsonb AND compilation_artifacts = '{}'::jsonb AND
            creation_code_artifacts = '{}'::jsonb AND
            runtime_code_artifacts = '{}'::jsonb AND libraries = '{}'::jsonb AND
            constructor_arguments IS NULL AND is_blueprint = FALSE
        ) IS TRUE)
    );

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
               NEW.catalog_generation_id IS NULL AND
               NEW.compiler_digest = decode('3b9671727c888363740dc678e60336759871487d0e4e9fdd973048fa9635c4fd', 'hex') AND
               NEW.executor_kind = 'etherview_vyper_v1' AND
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

CREATE OR REPLACE FUNCTION enforce_verifier_v2_result_job()
RETURNS trigger
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


ALTER TABLE verification_jobs ADD CONSTRAINT verification_jobs_vyper_request_check CHECK (
 (kind NOT IN ('vyper_standard_json','vyper_multipart') OR language = 'vyper') AND
 (language IS DISTINCT FROM 'vyper' OR (
    request->>'target_file' IS NOT NULL AND
    request #>> '{standard_json,language}' = 'Vyper' AND
    jsonb_typeof(request #> '{standard_json,sources}') = 'object' AND
    (request #> '{standard_json,sources}') ? (request->>'target_file') AND
    compiler_version = '0.4.3'
 ) IS TRUE));
