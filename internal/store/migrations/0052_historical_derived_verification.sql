ALTER TABLE verification_jobs
    DROP CONSTRAINT verification_jobs_kind_check,
    DROP CONSTRAINT verification_jobs_target_check;

ALTER TABLE verification_jobs
    ADD CONSTRAINT verification_jobs_kind_check CHECK (
        kind IN (
            'address', 'solidity_multipart', 'solidity_standard_json',
            'solidity_batch_multipart', 'solidity_batch_standard_json',
            'sourcify', 'sourcify_from_etherscan', 'proxy', 'derived'
        )
    ),
    ADD CONSTRAINT verification_jobs_target_check CHECK (
        (kind IN ('address', 'proxy', 'derived') AND chain_id IS NOT NULL AND
            octet_length(address) = 20 AND octet_length(code_hash) = 32 AND
            octet_length(block_hash) = 32) OR
        (kind NOT IN ('address', 'proxy', 'derived') AND chain_id IS NULL AND
            address IS NULL AND code_hash IS NULL AND block_hash IS NULL)
    );

CREATE TABLE derived_verification_scans (
    compilation_id UUID PRIMARY KEY
        REFERENCES verification_compilation_units(id) ON DELETE RESTRICT,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    creator_address BYTEA NOT NULL,
    creator_code_hash BYTEA NOT NULL,
    valid_from_block NUMERIC(78, 0) NOT NULL,
    valid_to_block NUMERIC(78, 0),
    cursor_block_number NUMERIC(78, 0) NOT NULL,
    cursor_transaction_hash BYTEA NOT NULL DEFAULT decode(repeat('00', 32), 'hex'),
    cursor_trace_path TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'queued',
    leased_by TEXT,
    lease_token TEXT,
    lease_expires_at TIMESTAMPTZ,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 5,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT derived_verification_scans_identity_check CHECK (
        octet_length(creator_address) = 20 AND
        octet_length(creator_code_hash) = 32 AND
        valid_from_block >= 0 AND
        (valid_to_block IS NULL OR valid_to_block >= valid_from_block) AND
        cursor_block_number >= valid_from_block AND
        octet_length(cursor_transaction_hash) = 32 AND
        length(cursor_trace_path) <= 2048
    ),
    CONSTRAINT derived_verification_scans_status_check CHECK (
        status IN ('queued', 'running', 'succeeded', 'failed')
    ),
    CONSTRAINT derived_verification_scans_lease_check CHECK (
        (leased_by IS NULL) = (lease_token IS NULL) AND
        (leased_by IS NULL) = (lease_expires_at IS NULL) AND
        (status = 'running') = (leased_by IS NOT NULL)
    ),
    CONSTRAINT derived_verification_scans_attempt_check CHECK (
        attempt_count >= 0 AND max_attempts BETWEEN 1 AND 100 AND
        attempt_count <= max_attempts
    )
);

CREATE INDEX derived_verification_scans_claim_idx
    ON derived_verification_scans (status, updated_at, compilation_id)
    WHERE status IN ('queued', 'running');

CREATE TABLE derived_verification_attempts (
    id UUID PRIMARY KEY,
    chain_id NUMERIC(78, 0) NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    transaction_hash BYTEA NOT NULL,
    trace_path TEXT NOT NULL,
    creator_address BYTEA NOT NULL,
    created_address BYTEA NOT NULL,
    call_type TEXT NOT NULL,
    compilation_id UUID NOT NULL
        REFERENCES verification_compilation_units(id) ON DELETE RESTRICT,
    file_name TEXT,
    contract_name TEXT,
    status TEXT NOT NULL,
    creation_match JSONB,
    runtime_match JSONB,
    verification_job_id UUID REFERENCES verification_jobs(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (chain_id, block_hash, transaction_hash, trace_path, compilation_id),
    CONSTRAINT derived_verification_attempts_trace_fk FOREIGN KEY (
        chain_id, block_number, block_hash, transaction_hash, trace_path
    ) REFERENCES normalized_traces (
        chain_id, block_number, block_hash, transaction_hash, trace_path
    ) ON DELETE RESTRICT,
    CONSTRAINT derived_verification_attempts_identity_check CHECK (
        octet_length(block_hash) = 32 AND
        octet_length(transaction_hash) = 32 AND
        length(trace_path) BETWEEN 1 AND 2048 AND
        octet_length(creator_address) = 20 AND
        octet_length(created_address) = 20 AND
        call_type IN ('CREATE', 'CREATE2')
    ),
    CONSTRAINT derived_verification_attempts_status_check CHECK (
        status IN (
            'pending_runtime', 'matched', 'ambiguous', 'no_match',
            'runtime_mismatch', 'stale'
        )
    ),
    CONSTRAINT derived_verification_attempts_outcome_check CHECK (
        (status = 'matched' AND file_name IS NOT NULL AND contract_name IS NOT NULL AND
            jsonb_typeof(creation_match) = 'object' AND
            jsonb_typeof(runtime_match) = 'object' AND verification_job_id IS NOT NULL) OR
        (status <> 'matched' AND file_name IS NULL AND contract_name IS NULL AND
            creation_match IS NULL AND runtime_match IS NULL AND
            verification_job_id IS NULL)
    )
);

CREATE INDEX derived_verification_attempts_creator_idx
    ON derived_verification_attempts (chain_id, creator_address, block_number DESC);

CREATE INDEX derived_verification_attempts_created_idx
    ON derived_verification_attempts (chain_id, created_address, block_number DESC);

CREATE OR REPLACE FUNCTION enforce_unbound_verification_job_insert()
RETURNS trigger
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

DO $migration$
DECLARE
    migration_schema TEXT := current_schema();
BEGIN
    IF migration_schema IS NULL THEN
        RAISE EXCEPTION 'derived verification migration requires a current schema';
    END IF;
    EXECUTE format(
        'ALTER FUNCTION %I.enforce_verifier_v2_publication() SET search_path = %I, pg_catalog',
        migration_schema,
        migration_schema
    );
    EXECUTE format(
        'ALTER FUNCTION %I.enforce_unbound_verification_job_insert() SET search_path = %I, pg_catalog',
        migration_schema,
        migration_schema
    );
END
$migration$;
