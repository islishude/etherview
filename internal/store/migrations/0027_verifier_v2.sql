-- P30-T08 intentionally replaces the verifier-v1 persistence contract.
-- Operators must back up verification data before applying this migration.

DROP TABLE IF EXISTS verified_contracts CASCADE;
DROP TABLE IF EXISTS verification_results CASCADE;
DROP TABLE IF EXISTS verification_jobs CASCADE;

DROP FUNCTION IF EXISTS enforce_verified_contract_source() CASCADE;
DROP FUNCTION IF EXISTS enforce_verification_result_job_state() CASCADE;
DROP FUNCTION IF EXISTS reject_verification_result_mutation() CASCADE;
DROP FUNCTION IF EXISTS enforce_verification_job_immutability() CASCADE;

CREATE TABLE compiler_catalog_generations (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    language TEXT NOT NULL,
    source_url TEXT NOT NULL,
    catalog_digest BYTEA NOT NULL,
    fetched_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    entry_count INTEGER NOT NULL,
    CONSTRAINT compiler_catalog_generations_language_check
        CHECK (language IN ('solidity', 'vyper')),
    CONSTRAINT compiler_catalog_generations_source_check
        CHECK (length(source_url) BETWEEN 9 AND 4096),
    CONSTRAINT compiler_catalog_generations_digest_check
        CHECK (octet_length(catalog_digest) = 32),
    CONSTRAINT compiler_catalog_generations_count_check
        CHECK (entry_count BETWEEN 1 AND 4096),
    CONSTRAINT compiler_catalog_generations_identity_key
        UNIQUE (language, catalog_digest),
    CONSTRAINT compiler_catalog_generations_id_language_key
        UNIQUE (id, language)
);

CREATE TABLE compiler_catalog_entries (
    generation_id BIGINT NOT NULL,
    language TEXT NOT NULL,
    version TEXT NOT NULL,
    platform TEXT NOT NULL DEFAULT 'linux-amd64',
    artifact_url TEXT NOT NULL,
    artifact_sha256 BYTEA NOT NULL,
    max_bytes BIGINT NOT NULL,
    PRIMARY KEY (generation_id, version),
    CONSTRAINT compiler_catalog_entries_generation_language_key
        UNIQUE (generation_id, language, version, platform, artifact_sha256),
    CONSTRAINT compiler_catalog_entries_generation_fk
        FOREIGN KEY (generation_id, language)
        REFERENCES compiler_catalog_generations(id, language) ON DELETE RESTRICT,
    CONSTRAINT compiler_catalog_entries_language_check
        CHECK (language IN ('solidity', 'vyper')),
    CONSTRAINT compiler_catalog_entries_version_check
        CHECK (length(version) BETWEEN 1 AND 128),
    CONSTRAINT compiler_catalog_entries_platform_check CHECK (
        platform IN (
            'bin', 'emscripten-asmjs', 'emscripten-wasm32',
            'linux-amd64', 'linux-arm64', 'macosx-amd64',
            'wasm', 'windows-amd64'
        )
    ),
    CONSTRAINT compiler_catalog_entries_url_check
        CHECK (length(artifact_url) BETWEEN 9 AND 4096),
    CONSTRAINT compiler_catalog_entries_digest_check
        CHECK (octet_length(artifact_sha256) = 32),
    CONSTRAINT compiler_catalog_entries_size_check
        CHECK (max_bytes BETWEEN 1 AND 209715200)
);

CREATE TABLE compiler_catalog_heads (
    language TEXT PRIMARY KEY,
    generation_id BIGINT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT compiler_catalog_heads_language_check
        CHECK (language IN ('solidity', 'vyper')),
    CONSTRAINT compiler_catalog_heads_generation_fk
        FOREIGN KEY (generation_id, language)
        REFERENCES compiler_catalog_generations(id, language) ON DELETE RESTRICT
);

CREATE TABLE verification_jobs (
    id UUID PRIMARY KEY,
    kind TEXT NOT NULL,
    language TEXT,
    catalog_language TEXT,
    compiler_version TEXT,
    compiler_platform TEXT,
    catalog_generation_id BIGINT,
    compiler_digest BYTEA,
    runner_digest BYTEA,
    chain_id NUMERIC(78, 0) REFERENCES chains(chain_id),
    address BYTEA,
    code_hash BYTEA,
    block_hash BYTEA,
    request JSONB NOT NULL,
    request_payload BYTEA NOT NULL,
    request_digest BYTEA NOT NULL,
    requires_hard_isolation BOOLEAN NOT NULL DEFAULT FALSE,
    status TEXT NOT NULL DEFAULT 'queued',
    leased_by TEXT,
    lease_token TEXT,
    lease_expires_at TIMESTAMPTZ,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    outcome_kind TEXT,
    outcome JSONB,
    error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT verification_jobs_kind_check CHECK (
        kind IN (
            'address',
            'solidity_multipart',
            'solidity_standard_json',
            'solidity_batch_multipart',
            'solidity_batch_standard_json',
            'vyper_multipart',
            'vyper_standard_json',
            'sourcify',
            'sourcify_from_etherscan'
        )
    ),
    CONSTRAINT verification_jobs_language_check
        CHECK (language IS NULL OR language IN ('solidity', 'yul', 'vyper')),
    CONSTRAINT verification_jobs_catalog_language_check CHECK (
        catalog_language IS NULL OR catalog_language IN ('solidity', 'vyper')
    ),
    CONSTRAINT verification_jobs_compiler_check CHECK (
        (kind IN ('sourcify', 'sourcify_from_etherscan') AND
            language IS NULL AND compiler_version IS NULL AND
            compiler_platform IS NULL AND
            catalog_language IS NULL AND catalog_generation_id IS NULL AND
            compiler_digest IS NULL) OR
        (kind NOT IN ('sourcify', 'sourcify_from_etherscan') AND
            language IS NOT NULL AND length(compiler_version) BETWEEN 1 AND 128 AND
            compiler_platform IN (
                'bin', 'emscripten-asmjs', 'emscripten-wasm32',
                'linux-amd64', 'linux-arm64', 'macosx-amd64',
                'wasm', 'windows-amd64'
            ) AND
            catalog_language = CASE WHEN language = 'yul' THEN 'solidity' ELSE language END AND
            catalog_generation_id IS NOT NULL AND
            octet_length(compiler_digest) = 32)
    ),
    CONSTRAINT verification_jobs_catalog_fk FOREIGN KEY (
        catalog_generation_id, catalog_language, compiler_version,
        compiler_platform, compiler_digest
    ) REFERENCES compiler_catalog_entries (
        generation_id, language, version, platform, artifact_sha256
    ) ON DELETE RESTRICT,
    CONSTRAINT verification_jobs_runner_check CHECK (
        (requires_hard_isolation AND octet_length(runner_digest) = 32) OR
        (NOT requires_hard_isolation AND
            (runner_digest IS NULL OR octet_length(runner_digest) = 32))
    ),
    CONSTRAINT verification_jobs_target_check CHECK (
        (kind = 'address' AND chain_id IS NOT NULL AND
            octet_length(address) = 20 AND octet_length(code_hash) = 32 AND
            octet_length(block_hash) = 32) OR
        (kind <> 'address' AND chain_id IS NULL AND address IS NULL AND
            code_hash IS NULL AND block_hash IS NULL)
    ),
    CONSTRAINT verification_jobs_request_check CHECK (
        jsonb_typeof(request) = 'object' AND
        octet_length(request_payload) BETWEEN 2 AND 67108864 AND
        request = convert_from(request_payload, 'UTF8')::jsonb AND
        octet_length(request_digest) = 32
    ),
    CONSTRAINT verification_jobs_status_check
        CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
    CONSTRAINT verification_jobs_lease_check CHECK (
        (leased_by IS NULL) = (lease_token IS NULL) AND
        (leased_by IS NULL) = (lease_expires_at IS NULL) AND
        (status = 'running') = (leased_by IS NOT NULL)
    ),
    CONSTRAINT verification_jobs_attempt_check
        CHECK (attempt_count >= 0 AND max_attempts BETWEEN 1 AND 100 AND
            attempt_count <= max_attempts),
    CONSTRAINT verification_jobs_terminal_check CHECK (
        (status IN ('queued', 'running', 'cancelled') AND
            outcome_kind IS NULL AND outcome IS NULL AND error_code IS NULL) OR
        (status = 'succeeded' AND
            outcome_kind IN (
                'compilation_failure', 'verification_failure',
                'verification_success', 'batch_results', 'sourcify_success'
            ) AND jsonb_typeof(outcome) = 'object' AND error_code IS NULL) OR
        (status = 'failed' AND outcome_kind IS NULL AND outcome IS NULL AND
            length(error_code) BETWEEN 1 AND 64)
    ),
    CONSTRAINT verification_jobs_result_size_check
        CHECK (outcome IS NULL OR octet_length(outcome::text) <= 268435456)
);

CREATE UNIQUE INDEX verification_jobs_active_request_key
    ON verification_jobs (request_digest)
    WHERE status IN ('queued', 'running', 'succeeded');

CREATE INDEX verification_jobs_claim_idx
    ON verification_jobs (status, created_at, id)
    WHERE status IN ('queued', 'running');

CREATE INDEX verification_jobs_target_idx
    ON verification_jobs (chain_id, address, code_hash, created_at DESC)
    WHERE kind = 'address';

CREATE TABLE verification_results (
    job_id UUID PRIMARY KEY REFERENCES verification_jobs(id) ON DELETE RESTRICT,
    request_digest BYTEA NOT NULL,
    outcome_kind TEXT NOT NULL,
    outcome JSONB NOT NULL,
    file_name TEXT,
    contract_name TEXT,
    language TEXT,
    compiler_version TEXT,
    match_type TEXT,
    abi JSONB,
    sources JSONB,
    settings JSONB,
    compilation_artifacts JSONB,
    creation_code_artifacts JSONB,
    runtime_code_artifacts JSONB,
    constructor_arguments BYTEA,
    libraries JSONB,
    is_blueprint BOOLEAN,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT verification_results_job_digest_key
        UNIQUE (job_id, request_digest),
    CONSTRAINT verification_results_digest_check
        CHECK (octet_length(request_digest) = 32),
    CONSTRAINT verification_results_outcome_check CHECK (
        outcome_kind IN (
            'compilation_failure', 'verification_failure',
            'verification_success', 'batch_results', 'sourcify_success'
        ) AND jsonb_typeof(outcome) = 'object' AND
        octet_length(outcome::text) <= 268435456
    ),
    CONSTRAINT verification_results_success_shape_check CHECK (
        (outcome_kind <> 'verification_success' AND
            file_name IS NULL AND contract_name IS NULL AND language IS NULL AND
            compiler_version IS NULL AND match_type IS NULL AND abi IS NULL AND
            sources IS NULL AND settings IS NULL AND compilation_artifacts IS NULL AND
            creation_code_artifacts IS NULL AND runtime_code_artifacts IS NULL AND
            constructor_arguments IS NULL AND libraries IS NULL AND is_blueprint IS NULL) OR
        (outcome_kind = 'verification_success' AND
            length(file_name) BETWEEN 1 AND 512 AND
            length(contract_name) BETWEEN 1 AND 256 AND
            language IN ('solidity', 'yul', 'vyper') AND
            length(compiler_version) BETWEEN 1 AND 128 AND
            match_type IN ('full', 'partial') AND
            (abi IS NULL OR jsonb_typeof(abi) = 'array') AND
            jsonb_typeof(sources) = 'object' AND jsonb_typeof(settings) = 'object' AND
            jsonb_typeof(compilation_artifacts) = 'object' AND
            jsonb_typeof(creation_code_artifacts) = 'object' AND
            jsonb_typeof(runtime_code_artifacts) = 'object' AND
            jsonb_typeof(libraries) = 'object' AND is_blueprint IS NOT NULL)
    )
);

CREATE TABLE verified_contracts (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    address BYTEA NOT NULL,
    code_hash BYTEA NOT NULL,
    valid_from_block NUMERIC(78, 0) NOT NULL,
    valid_to_block NUMERIC(78, 0),
    verification_job_id UUID NOT NULL,
    request_digest BYTEA NOT NULL,
    file_name TEXT NOT NULL,
    contract_name TEXT NOT NULL,
    language TEXT NOT NULL,
    compiler_version TEXT NOT NULL,
    match_type TEXT NOT NULL,
    abi JSONB,
    sources JSONB NOT NULL,
    settings JSONB NOT NULL,
    compilation_artifacts JSONB NOT NULL,
    creation_code_artifacts JSONB NOT NULL,
    runtime_code_artifacts JSONB NOT NULL,
    constructor_arguments BYTEA,
    libraries JSONB NOT NULL,
    is_blueprint BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, address, code_hash, valid_from_block),
    CONSTRAINT verified_contracts_result_fk FOREIGN KEY (
        verification_job_id, request_digest
    ) REFERENCES verification_results (job_id, request_digest) ON DELETE RESTRICT,
    CONSTRAINT verified_contracts_identity_check CHECK (
        octet_length(address) = 20 AND octet_length(code_hash) = 32 AND
        valid_from_block >= 0 AND
        (valid_to_block IS NULL OR valid_to_block >= valid_from_block) AND
        octet_length(request_digest) = 32
    ),
    CONSTRAINT verified_contracts_artifact_check CHECK (
        length(file_name) BETWEEN 1 AND 512 AND
        length(contract_name) BETWEEN 1 AND 256 AND
        language IN ('solidity', 'yul', 'vyper') AND
        length(compiler_version) BETWEEN 1 AND 128 AND
        match_type IN ('full', 'partial') AND
        (abi IS NULL OR jsonb_typeof(abi) = 'array') AND
        jsonb_typeof(sources) = 'object' AND jsonb_typeof(settings) = 'object' AND
        jsonb_typeof(compilation_artifacts) = 'object' AND
        jsonb_typeof(creation_code_artifacts) = 'object' AND
        jsonb_typeof(runtime_code_artifacts) = 'object' AND
        jsonb_typeof(libraries) = 'object'
    )
);

-- The destructive verifier cutover must not leave searchable v1 publications
-- behind after their authoritative rows have been removed.
DELETE FROM search_catalog_documents
WHERE source_kind = 'verified_contract';

CREATE TRIGGER verified_contracts_search_catalog_trigger
AFTER INSERT OR UPDATE OR DELETE ON verified_contracts
FOR EACH ROW EXECUTE FUNCTION record_search_catalog_document('verified_contract');

CREATE OR REPLACE FUNCTION reject_verifier_v2_result_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'verifier v2 results are immutable';
END
$$;

CREATE TRIGGER verification_results_v2_immutable
BEFORE UPDATE OR DELETE ON verification_results
FOR EACH ROW EXECUTE FUNCTION reject_verifier_v2_result_mutation();

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
            job.error_code IS NOT NULL
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

CREATE CONSTRAINT TRIGGER verification_results_v2_terminal_job
AFTER INSERT ON verification_results
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION enforce_verifier_v2_result_job();

CREATE CONSTRAINT TRIGGER verification_jobs_v2_result_insert
AFTER INSERT ON verification_jobs
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION enforce_verifier_v2_result_job();

CREATE CONSTRAINT TRIGGER verification_jobs_v2_result_update
AFTER UPDATE OF status, outcome_kind, outcome, error_code ON verification_jobs
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION enforce_verifier_v2_result_job();

CREATE OR REPLACE FUNCTION enforce_verifier_v2_publication()
RETURNS trigger
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
          AND job.kind = 'address'
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

CREATE TRIGGER verified_contracts_v2_source_guard
BEFORE INSERT OR UPDATE ON verified_contracts
FOR EACH ROW EXECUTE FUNCTION enforce_verifier_v2_publication();

DO $migration$
DECLARE
    migration_schema TEXT := current_schema();
BEGIN
    IF migration_schema IS NULL THEN
        RAISE EXCEPTION 'verifier v2 migration requires a current schema';
    END IF;
    EXECUTE format(
        'ALTER FUNCTION %I.reject_verifier_v2_result_mutation() SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
    EXECUTE format(
        'ALTER FUNCTION %I.enforce_verifier_v2_result_job() SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
    EXECUTE format(
        'ALTER FUNCTION %I.enforce_verifier_v2_publication() SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
END
$migration$;
