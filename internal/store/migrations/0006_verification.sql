-- Fresh-database baseline: verification.

-- Functions

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

CREATE FUNCTION reject_verification_compilation_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'authenticated verification compilation units are immutable';
END
$$;

CREATE FUNCTION reject_verifier_v2_result_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'verifier v2 results are immutable';
END
$$;

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


-- Tables

CREATE TABLE compiler_catalog_generations (
    id bigint NOT NULL GENERATED ALWAYS AS IDENTITY (
        SEQUENCE NAME compiler_catalog_generations_id_seq
        START WITH 1
        INCREMENT BY 1
        NO MINVALUE
        NO MAXVALUE
        CACHE 1
    ),
    language text NOT NULL,
    source_url text NOT NULL,
    catalog_digest bytea NOT NULL,
    fetched_at timestamp with time zone DEFAULT now() NOT NULL,
    entry_count integer NOT NULL,
    CONSTRAINT compiler_catalog_generations_count_check CHECK (((entry_count >= 1) AND (entry_count <= 4096))),
    CONSTRAINT compiler_catalog_generations_digest_check CHECK ((octet_length(catalog_digest) = 32)),
    CONSTRAINT compiler_catalog_generations_language_check CHECK ((language = ANY (ARRAY['solidity'::text, 'vyper'::text]))),
    CONSTRAINT compiler_catalog_generations_source_check CHECK (((length(source_url) >= 9) AND (length(source_url) <= 4096))),
    CONSTRAINT compiler_catalog_generations_id_language_key UNIQUE (id, language),
    CONSTRAINT compiler_catalog_generations_identity_key UNIQUE (language, catalog_digest),
    CONSTRAINT compiler_catalog_generations_pkey PRIMARY KEY (id)
);

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
    CONSTRAINT compiler_catalog_entries_version_check CHECK (((length(version) >= 1) AND (length(version) <= 128))),
    CONSTRAINT compiler_catalog_entries_generation_language_key UNIQUE (generation_id, language, version, platform, artifact_sha256),
    CONSTRAINT compiler_catalog_entries_pkey PRIMARY KEY (generation_id, version),
    CONSTRAINT compiler_catalog_entries_generation_fk FOREIGN KEY (generation_id, language) REFERENCES compiler_catalog_generations(id, language) ON DELETE RESTRICT
);

CREATE TABLE compiler_catalog_heads (
    language text NOT NULL,
    generation_id bigint NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT compiler_catalog_heads_language_check CHECK ((language = ANY (ARRAY['solidity'::text, 'vyper'::text]))),
    CONSTRAINT compiler_catalog_heads_pkey PRIMARY KEY (language),
    CONSTRAINT compiler_catalog_heads_generation_fk FOREIGN KEY (generation_id, language) REFERENCES compiler_catalog_generations(id, language) ON DELETE RESTRICT
);

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
    CONSTRAINT verification_jobs_vyper_request_check CHECK ((((kind <> ALL (ARRAY['vyper_standard_json'::text, 'vyper_multipart'::text])) OR (language = 'vyper'::text)) AND ((language IS DISTINCT FROM 'vyper'::text) OR ((((request ->> 'target_file'::text) IS NOT NULL) AND ((request #>> '{standard_json,language}'::text[]) = 'Vyper'::text) AND (jsonb_typeof((request #> '{standard_json,sources}'::text[])) = 'object'::text) AND ((request #> '{standard_json,sources}'::text[]) ? (request ->> 'target_file'::text))) IS TRUE)))),
    CONSTRAINT verification_jobs_pkey PRIMARY KEY (id),
    CONSTRAINT verification_jobs_catalog_fk FOREIGN KEY (catalog_generation_id, catalog_language, compiler_version, compiler_platform, compiler_digest) REFERENCES compiler_catalog_entries(generation_id, language, version, platform, artifact_sha256) ON DELETE RESTRICT,
    CONSTRAINT verification_jobs_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

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
    CONSTRAINT verification_results_success_shape_check CHECK ((((outcome_kind <> 'verification_success'::text) AND (file_name IS NULL) AND (contract_name IS NULL) AND (language IS NULL) AND (compiler_version IS NULL) AND (match_type IS NULL) AND (abi IS NULL) AND (sources IS NULL) AND (settings IS NULL) AND (compilation_artifacts IS NULL) AND (creation_code_artifacts IS NULL) AND (runtime_code_artifacts IS NULL) AND (constructor_arguments IS NULL) AND (libraries IS NULL) AND (is_blueprint IS NULL)) OR ((outcome_kind = 'verification_success'::text) AND (length(file_name) BETWEEN 1 AND 512) AND ((length(contract_name) >= 1) AND (length(contract_name) <= 256)) AND (language = ANY (ARRAY['solidity'::text, 'yul'::text, 'geas'::text, 'vyper'::text])) AND ((length(compiler_version) >= 1) AND (length(compiler_version) <= 128)) AND (match_type = ANY (ARRAY['full'::text, 'partial'::text])) AND ((abi IS NULL) OR (jsonb_typeof(abi) = 'array'::text)) AND (jsonb_typeof(sources) = 'object'::text) AND (jsonb_typeof(settings) = 'object'::text) AND (jsonb_typeof(compilation_artifacts) = 'object'::text) AND (jsonb_typeof(creation_code_artifacts) = 'object'::text) AND (jsonb_typeof(runtime_code_artifacts) = 'object'::text) AND (jsonb_typeof(libraries) = 'object'::text) AND (is_blueprint IS NOT NULL) AND ((language <> 'geas'::text) OR (((compiler_version = '0.3.3'::text) AND (match_type = 'full'::text) AND (abi = '[]'::jsonb) AND (compilation_artifacts = '{}'::jsonb) AND (creation_code_artifacts = '{}'::jsonb) AND (runtime_code_artifacts = '{}'::jsonb) AND (libraries = '{}'::jsonb) AND (constructor_arguments IS NULL) AND (is_blueprint = false) AND (jsonb_typeof((settings -> 'runtime_entrypoint'::text)) = 'string'::text) AND ((settings -> 'stack_check'::text) = 'true'::jsonb) AND ((settings - ARRAY['runtime_entrypoint'::text, 'creation_entrypoint'::text, 'stack_check'::text]) = '{}'::jsonb) AND valid_geas_verification_sources(sources) AND ((outcome #>> '{runtime_match,match_type}'::text[]) = 'full'::text) AND ((outcome #> '{runtime_match,transformations}'::text[]) = '[]'::jsonb) AND ((outcome #> '{runtime_match,values}'::text[]) = '{}'::jsonb) AND (((jsonb_typeof((settings -> 'creation_entrypoint'::text)) = 'string'::text) AND ((outcome #>> '{creation_match,match_type}'::text[]) = 'full'::text) AND ((outcome #> '{creation_match,transformations}'::text[]) = '[]'::jsonb) AND ((outcome #> '{creation_match,values}'::text[]) = '{}'::jsonb)) OR ((NOT (settings ? 'creation_entrypoint'::text)) AND ((outcome -> 'creation_match'::text) = 'null'::jsonb)))) IS TRUE))))),
    CONSTRAINT verification_results_job_digest_key UNIQUE (job_id, request_digest),
    CONSTRAINT verification_results_pkey PRIMARY KEY (job_id),
    CONSTRAINT verification_results_job_id_fkey FOREIGN KEY (job_id) REFERENCES verification_jobs(id) ON DELETE RESTRICT
);

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
    CONSTRAINT verification_compilation_units_shape_check CHECK (((octet_length(request_digest) = 32) AND (language = 'solidity'::text) AND ((length(compiler_version) >= 1) AND (length(compiler_version) <= 128)) AND ((length(compiler_platform) >= 1) AND (length(compiler_platform) <= 64)) AND (catalog_generation_id > 0) AND (octet_length(compiler_sha256) = 32) AND ((length(executor_kind) >= 1) AND (length(executor_kind) <= 64)) AND ((length(execution_policy) >= 1) AND (length(execution_policy) <= 64)) AND (octet_length(executor_sha256) = 32) AND (jsonb_typeof(standard_json) = 'object'::text) AND ((octet_length(standard_json_payload) >= 2) AND (octet_length(standard_json_payload) <= 67108864)) AND (standard_json = (convert_from(standard_json_payload, 'UTF8'::name))::jsonb))),
    CONSTRAINT verification_compilation_units_pkey PRIMARY KEY (id),
    CONSTRAINT verification_compilation_units_source_job_id_key UNIQUE (source_job_id),
    CONSTRAINT verification_compilation_units_job_digest_fk FOREIGN KEY (source_job_id, request_digest) REFERENCES verification_results(job_id, request_digest) ON DELETE RESTRICT,
    CONSTRAINT verification_compilation_units_source_job_id_fkey FOREIGN KEY (source_job_id) REFERENCES verification_jobs(id) ON DELETE RESTRICT
);

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
    CONSTRAINT verified_contracts_identity_check CHECK (((octet_length(address) = 20) AND (octet_length(code_hash) = 32) AND (valid_from_block >= (0)::numeric) AND ((valid_to_block IS NULL) OR (valid_to_block >= valid_from_block)) AND (octet_length(request_digest) = 32))),
    CONSTRAINT verified_contracts_pkey PRIMARY KEY (chain_id, address, code_hash, valid_from_block, verification_job_id),
    CONSTRAINT verified_contracts_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT verified_contracts_result_fk FOREIGN KEY (verification_job_id, request_digest) REFERENCES verification_results(job_id, request_digest) ON DELETE RESTRICT
);

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
    CONSTRAINT verified_contract_proxy_artifacts_valid_from_block_check CHECK ((valid_from_block >= (0)::numeric)),
    CONSTRAINT verified_contract_proxy_artifacts_pkey PRIMARY KEY (chain_id, address, code_hash, valid_from_block, verification_job_id),
    CONSTRAINT verified_contract_proxy_artifacts_verification_job_id_key UNIQUE (verification_job_id),
    CONSTRAINT verified_contract_proxy_artif_chain_id_address_code_hash_v_fkey FOREIGN KEY (chain_id, address, code_hash, valid_from_block, verification_job_id) REFERENCES verified_contracts(chain_id, address, code_hash, valid_from_block, verification_job_id) ON DELETE RESTRICT,
    CONSTRAINT verified_contract_proxy_artif_verification_job_id_request__fkey FOREIGN KEY (verification_job_id, request_digest) REFERENCES verification_results(job_id, request_digest) ON DELETE RESTRICT,
    CONSTRAINT verified_contract_proxy_artifacts_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

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
    CONSTRAINT verification_compilation_contracts_shape_check CHECK (((length(file_name) BETWEEN 1 AND 384) AND ((length(contract_name) >= 1) AND (length(contract_name) <= 256)) AND (jsonb_typeof(abi) = 'array'::text) AND ((octet_length(creation_bytecode) >= 1) AND (octet_length(creation_bytecode) <= 67108864)) AND ((octet_length(runtime_bytecode) >= 1) AND (octet_length(runtime_bytecode) <= 67108864)) AND (jsonb_typeof(compilation_artifacts) = 'object'::text) AND (jsonb_typeof(creation_code_artifacts) = 'object'::text) AND (jsonb_typeof(runtime_code_artifacts) = 'object'::text))),
    CONSTRAINT verification_compilation_contracts_pkey PRIMARY KEY (compilation_id, file_name, contract_name),
    CONSTRAINT verification_compilation_contracts_compilation_id_fkey FOREIGN KEY (compilation_id) REFERENCES verification_compilation_units(id) ON DELETE RESTRICT
);

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
    CONSTRAINT verified_function_selector_sets_status_check CHECK (((status = ANY (ARRAY['complete'::text, 'invalid'::text])) AND (function_count >= 0) AND (((status = 'complete'::text) AND (warning = ''::text)) OR ((status = 'invalid'::text) AND (function_count = 0) AND (warning <> ''::text))))),
    CONSTRAINT verified_function_selector_sets_lookup_identity_unique UNIQUE (verification_job_id, chain_id, address, code_hash),
    CONSTRAINT verified_function_selector_sets_pkey PRIMARY KEY (verification_job_id),
    CONSTRAINT verified_function_selector_sets_contract_fk FOREIGN KEY (chain_id, address, code_hash, valid_from_block, verification_job_id) REFERENCES verified_contracts(chain_id, address, code_hash, valid_from_block, verification_job_id) ON DELETE CASCADE,
    CONSTRAINT verified_function_selector_sets_result_fk FOREIGN KEY (verification_job_id, request_digest) REFERENCES verification_results(job_id, request_digest) ON DELETE RESTRICT
);

CREATE TABLE verified_function_selectors (
    verification_job_id uuid NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    address bytea NOT NULL,
    code_hash bytea NOT NULL,
    selector bytea NOT NULL,
    signature text NOT NULL,
    function_name text NOT NULL,
    abi_entry jsonb NOT NULL,
    CONSTRAINT verified_function_selectors_shape_check CHECK (((octet_length(address) = 20) AND (octet_length(code_hash) = 32) AND (octet_length(selector) = 4) AND ((length(signature) >= 1) AND (length(signature) <= 4096)) AND ((length(function_name) >= 1) AND (length(function_name) <= 4096)) AND (jsonb_typeof(abi_entry) = 'object'::text) AND ((abi_entry ->> 'type'::text) = 'function'::text))),
    CONSTRAINT verified_function_selectors_pkey PRIMARY KEY (verification_job_id, selector, signature),
    CONSTRAINT verified_function_selectors_set_fk FOREIGN KEY (verification_job_id, chain_id, address, code_hash) REFERENCES verified_function_selector_sets(verification_job_id, chain_id, address, code_hash) ON DELETE CASCADE
);


-- Dependency completion

-- Preserve the named UNIQUE equivalent to the primary key; CREATE TABLE merges them.
ALTER TABLE ONLY verified_contracts
    ADD CONSTRAINT verified_contracts_selector_identity_unique UNIQUE (chain_id, address, code_hash, valid_from_block, verification_job_id);


-- Indexes

CREATE UNIQUE INDEX verification_jobs_active_request_key ON verification_jobs USING btree (request_digest) WHERE (status = ANY (ARRAY['queued'::text, 'running'::text, 'succeeded'::text]));

CREATE INDEX verification_jobs_claim_idx ON verification_jobs USING btree (status, created_at, id) WHERE (status = ANY (ARRAY['queued'::text, 'running'::text]));

CREATE INDEX verification_jobs_target_idx ON verification_jobs USING btree (chain_id, address, code_hash, created_at DESC) WHERE (kind = 'address'::text);

CREATE INDEX verified_contract_proxy_artifacts_current_idx ON verified_contract_proxy_artifacts USING btree (chain_id, address, valid_from_block DESC, code_hash);

CREATE INDEX verified_contracts_code_hash_artifact_idx ON verified_contracts USING btree (chain_id, code_hash, ((abi IS NOT NULL)) DESC, ((match_type = 'full'::text)) DESC, request_digest, verification_job_id, address, valid_from_block DESC);

CREATE INDEX verified_function_selector_sets_identity_idx ON verified_function_selector_sets USING btree (chain_id, code_hash, address, valid_from_block, verification_job_id);

CREATE INDEX verified_function_selectors_lookup_idx ON verified_function_selectors USING btree (chain_id, code_hash, selector, verification_job_id, signature);


-- Triggers

CREATE TRIGGER verification_compilation_contracts_immutable BEFORE DELETE OR UPDATE ON verification_compilation_contracts FOR EACH ROW EXECUTE FUNCTION reject_verification_compilation_mutation();

CREATE TRIGGER verification_compilation_units_immutable BEFORE DELETE OR UPDATE ON verification_compilation_units FOR EACH ROW EXECUTE FUNCTION reject_verification_compilation_mutation();

CREATE TRIGGER verification_jobs_compiler_provenance BEFORE UPDATE OF compiler_platform, catalog_generation_id, compiler_digest, executor_kind, execution_policy, executor_digest ON verification_jobs FOR EACH ROW EXECUTE FUNCTION enforce_verification_compiler_provenance();

CREATE TRIGGER verification_jobs_unbound_compiler_insert BEFORE INSERT ON verification_jobs FOR EACH ROW EXECUTE FUNCTION enforce_unbound_verification_job_insert();

CREATE CONSTRAINT TRIGGER verification_jobs_v2_result_insert AFTER INSERT ON verification_jobs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION enforce_verifier_v2_result_job();

CREATE CONSTRAINT TRIGGER verification_jobs_v2_result_update AFTER UPDATE OF status, outcome_kind, outcome, error_code ON verification_jobs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION enforce_verifier_v2_result_job();

CREATE TRIGGER verification_jobs_vyper_runtime_binding BEFORE INSERT OR UPDATE ON verification_jobs FOR EACH ROW EXECUTE FUNCTION check_vyper_runtime_binding();

CREATE TRIGGER verification_results_v2_immutable BEFORE DELETE OR UPDATE ON verification_results FOR EACH ROW EXECUTE FUNCTION reject_verifier_v2_result_mutation();

CREATE CONSTRAINT TRIGGER verification_results_v2_terminal_job AFTER INSERT ON verification_results DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION enforce_verifier_v2_result_job();

CREATE TRIGGER verified_contract_proxy_artifacts_source_guard BEFORE INSERT ON verified_contract_proxy_artifacts FOR EACH ROW EXECUTE FUNCTION enforce_verified_contract_proxy_artifact();

CREATE TRIGGER verified_contracts_v2_source_guard BEFORE INSERT OR UPDATE ON verified_contracts FOR EACH ROW EXECUTE FUNCTION enforce_verifier_v2_publication();
