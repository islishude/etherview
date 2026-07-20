-- P30-T01 makes verification submissions, compiler provenance, attempts, and
-- results durable across split API/worker processes and lease recovery.

ALTER TABLE verification_jobs
    ADD COLUMN request_payload BYTEA,
    ADD COLUMN request_digest BYTEA,
    ADD COLUMN requires_hard_isolation BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN attempt_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN max_attempts INTEGER NOT NULL DEFAULT 3,
    ADD COLUMN compiler_kind TEXT,
    ADD COLUMN compiler_digest BYTEA,
    ADD COLUMN compiler_hard_isolated BOOLEAN;

UPDATE verification_jobs
SET request_payload = convert_to(request::text, 'UTF8')
WHERE request_payload IS NULL;

UPDATE verification_jobs
SET request_digest = sha256(
        convert_to('etherview:verification-request:v1', 'UTF8') ||
        CASE WHEN requires_hard_isolation THEN decode('01', 'hex') ELSE decode('00', 'hex') END ||
        request_payload
    )
WHERE request_digest IS NULL;

UPDATE verification_jobs
SET attempt_count = 1
WHERE status <> 'queued' AND attempt_count = 0;

-- Pre-0016 successful jobs cannot recover the exact compiler artifact that
-- produced them. Preserve that fact explicitly instead of fabricating a
-- process or container digest.
UPDATE verification_jobs
SET compiler_kind = 'legacy_unrecorded',
    compiler_digest = decode(repeat('00', 32), 'hex'),
    compiler_hard_isolated = FALSE
WHERE status = 'succeeded' AND compiler_digest IS NULL;

ALTER TABLE verification_jobs
    ALTER COLUMN request_payload SET NOT NULL,
    ALTER COLUMN request_digest SET NOT NULL,
    DROP CONSTRAINT verification_jobs_chain_id_address_code_hash_block_hash_key,
    ADD CONSTRAINT verification_jobs_request_digest_check
        CHECK (
            octet_length(request_digest) = 32 AND
            request_digest = sha256(
                convert_to('etherview:verification-request:v1', 'UTF8') ||
                CASE WHEN requires_hard_isolation THEN decode('01', 'hex') ELSE decode('00', 'hex') END ||
                request_payload
            )
        ),
    ADD CONSTRAINT verification_jobs_request_payload_check
        CHECK (
            octet_length(request_payload) BETWEEN 2 AND 67108864 AND
            request = convert_from(request_payload, 'UTF8')::jsonb
        ),
    ADD CONSTRAINT verification_jobs_request_object_check
        CHECK (jsonb_typeof(request) = 'object'),
    ADD CONSTRAINT verification_jobs_request_binding_check
        CHECK (
            COALESCE(request ->> 'chain_id' = chain_id::text, FALSE) AND
            COALESCE(lower(request ->> 'address') = '0x' || encode(address, 'hex'), FALSE) AND
            COALESCE(lower(request ->> 'code_hash') = '0x' || encode(code_hash, 'hex'), FALSE) AND
            COALESCE(lower(request ->> 'at_block_hash') = '0x' || encode(block_hash, 'hex'), FALSE) AND
            COALESCE(request ->> 'language' = language, FALSE) AND
            COALESCE(request ->> 'compiler_version' = compiler_version, FALSE)
        ),
    ADD CONSTRAINT verification_jobs_text_bounds_check
        CHECK (
            length(compiler_version) BETWEEN 1 AND 128 AND
            (leased_by IS NULL OR length(leased_by) BETWEEN 1 AND 128) AND
            (lease_token IS NULL OR length(lease_token) BETWEEN 1 AND 128) AND
            (error_code IS NULL OR length(error_code) BETWEEN 1 AND 64)
        ),
    ADD CONSTRAINT verification_jobs_attempt_budget_check
        CHECK (attempt_count >= 0 AND max_attempts BETWEEN 1 AND 100),
    ADD CONSTRAINT verification_jobs_terminal_state_check
        CHECK (
            (status IN ('queued', 'running', 'cancelled') AND
                result_kind IS NULL AND result IS NULL AND error_code IS NULL) OR
            (status = 'succeeded' AND result_kind IS NOT NULL AND result IS NOT NULL AND
                jsonb_typeof(result) = 'object' AND error_code IS NULL) OR
            (status = 'failed' AND result_kind IS NULL AND result IS NULL AND
                error_code IN (
                    'compile_failed', 'compiler_output_invalid',
                    'compiler_output_too_large', 'match_failed',
                    'sandbox_required', 'compiler_provenance_mismatch',
                    'compiler_unavailable', 'target_not_canonical',
                    'attempts_exhausted'
                ))
        ),
    ADD CONSTRAINT verification_jobs_result_size_check
        CHECK (result IS NULL OR octet_length(result::text) <= 268435456),
    ADD CONSTRAINT verification_jobs_result_consistency_check
        CHECK (
            status <> 'succeeded' OR
            COALESCE(
                (result_kind = 'exact' AND
                    result #>> '{match,creation}' = 'exact' AND
                    result #>> '{match,runtime}' = 'exact' AND
                    result @> '{"published":true}'::jsonb) OR
                (result_kind = 'metadata_only' AND
                    result #>> '{match,creation}' IN ('exact', 'metadata_only') AND
                    result #>> '{match,runtime}' IN ('exact', 'metadata_only') AND
                    (result #>> '{match,creation}' = 'metadata_only' OR
                     result #>> '{match,runtime}' = 'metadata_only') AND
                    result @> '{"published":true}'::jsonb) OR
                (result_kind = 'mismatch' AND
                    (result #>> '{match,creation}' = 'mismatch' OR
                     result #>> '{match,runtime}' = 'mismatch') AND
                    result @> '{"published":false}'::jsonb),
                FALSE
            )
        ),
    ADD CONSTRAINT verification_jobs_compiler_provenance_check
        CHECK (
            (compiler_kind IS NULL AND compiler_digest IS NULL AND compiler_hard_isolated IS NULL) OR
            (compiler_kind IN ('process', 'container', 'legacy_unrecorded') AND
                compiler_digest IS NOT NULL AND octet_length(compiler_digest) = 32 AND
                compiler_hard_isolated IS NOT NULL)
        ),
    ADD CONSTRAINT verification_jobs_success_provenance_check
        CHECK (status <> 'succeeded' OR compiler_digest IS NOT NULL),
    ADD CONSTRAINT verification_jobs_hard_isolation_check
        CHECK (
            NOT requires_hard_isolation OR compiler_digest IS NULL OR
            compiler_hard_isolated = TRUE
        ),
    ADD CONSTRAINT verification_jobs_result_identity_key
        UNIQUE (
            id, chain_id, address, code_hash, block_hash, request_digest,
            compiler_kind, compiler_digest, compiler_hard_isolated
        );

CREATE UNIQUE INDEX verification_jobs_active_request_key
    ON verification_jobs (
        chain_id, address, code_hash, block_hash, request_digest
    )
    WHERE status IN ('queued', 'running', 'succeeded');

CREATE OR REPLACE FUNCTION enforce_verification_job_immutability()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id OR
       NEW.chain_id IS DISTINCT FROM OLD.chain_id OR
       NEW.address IS DISTINCT FROM OLD.address OR
       NEW.code_hash IS DISTINCT FROM OLD.code_hash OR
       NEW.block_hash IS DISTINCT FROM OLD.block_hash OR
       NEW.language IS DISTINCT FROM OLD.language OR
       NEW.compiler_version IS DISTINCT FROM OLD.compiler_version OR
       NEW.request IS DISTINCT FROM OLD.request OR
       NEW.request_payload IS DISTINCT FROM OLD.request_payload OR
       NEW.request_digest IS DISTINCT FROM OLD.request_digest OR
       NEW.requires_hard_isolation IS DISTINCT FROM OLD.requires_hard_isolation OR
       NEW.max_attempts IS DISTINCT FROM OLD.max_attempts OR
       NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'verification job identity is immutable';
    END IF;
    IF OLD.status IN ('succeeded', 'failed', 'cancelled') AND
       (NEW.status IS DISTINCT FROM OLD.status OR
        NEW.result_kind IS DISTINCT FROM OLD.result_kind OR
        NEW.result IS DISTINCT FROM OLD.result OR
        NEW.error_code IS DISTINCT FROM OLD.error_code) THEN
        RAISE EXCEPTION 'terminal verification job state is immutable';
    END IF;
    IF OLD.status = 'queued' AND
       NEW.status NOT IN ('queued', 'running', 'failed', 'cancelled') THEN
        RAISE EXCEPTION 'invalid queued verification job transition';
    END IF;
    IF OLD.status = 'running' AND
       NEW.status NOT IN ('running', 'succeeded', 'failed', 'cancelled') THEN
        RAISE EXCEPTION 'invalid running verification job transition';
    END IF;
    IF NEW.attempt_count IS DISTINCT FROM OLD.attempt_count AND
       (NEW.attempt_count <> OLD.attempt_count + 1 OR
        NEW.status <> 'running' OR NEW.lease_token IS NULL) THEN
        RAISE EXCEPTION 'verification attempt count may advance only on claim';
    END IF;
    IF OLD.compiler_digest IS NOT NULL AND
       (NEW.compiler_kind IS DISTINCT FROM OLD.compiler_kind OR
        NEW.compiler_digest IS DISTINCT FROM OLD.compiler_digest OR
        NEW.compiler_hard_isolated IS DISTINCT FROM OLD.compiler_hard_isolated) THEN
        RAISE EXCEPTION 'verification compiler provenance is immutable';
    END IF;
    IF OLD.compiler_digest IS NULL AND NEW.compiler_digest IS NOT NULL AND
       (OLD.status <> 'running' OR NEW.status <> 'running' OR
        OLD.lease_token IS NULL OR NEW.lease_token IS DISTINCT FROM OLD.lease_token) THEN
        RAISE EXCEPTION 'verification compiler provenance requires an owned running lease';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER verification_jobs_immutable_identity
BEFORE UPDATE ON verification_jobs
FOR EACH ROW EXECUTE FUNCTION enforce_verification_job_immutability();

CREATE TABLE verification_results (
    job_id UUID PRIMARY KEY,
    chain_id NUMERIC(78, 0) NOT NULL,
    address BYTEA NOT NULL,
    code_hash BYTEA NOT NULL,
    block_hash BYTEA NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    request_digest BYTEA NOT NULL,
    compiler_kind TEXT NOT NULL,
    compiler_digest BYTEA NOT NULL,
    compiler_hard_isolated BOOLEAN NOT NULL,
    result_kind TEXT NOT NULL,
    result JSONB NOT NULL,
    contract_name TEXT,
    abi JSONB,
    sources JSONB,
    settings JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT verification_results_job_request_key UNIQUE (job_id, request_digest),
    CONSTRAINT verification_results_job_identity_fk
        FOREIGN KEY (
            job_id, chain_id, address, code_hash, block_hash, request_digest,
            compiler_kind, compiler_digest, compiler_hard_isolated
        ) REFERENCES verification_jobs (
            id, chain_id, address, code_hash, block_hash, request_digest,
            compiler_kind, compiler_digest, compiler_hard_isolated
        ) ON DELETE RESTRICT,
    CONSTRAINT verification_results_target_check CHECK (
        octet_length(address) = 20 AND octet_length(code_hash) = 32 AND
        octet_length(block_hash) = 32 AND block_number >= 0 AND
        octet_length(request_digest) = 32
    ),
    CONSTRAINT verification_results_compiler_check CHECK (
        compiler_kind IN ('process', 'container') AND
        compiler_digest IS NOT NULL AND octet_length(compiler_digest) = 32
    ),
    CONSTRAINT verification_results_result_check CHECK (
        result_kind IN ('exact', 'metadata_only', 'mismatch') AND
        result IS NOT NULL AND jsonb_typeof(result) = 'object' AND
        COALESCE(result #>> '{match,creation}' IN ('exact', 'metadata_only', 'mismatch'), FALSE) AND
        COALESCE(result #>> '{match,runtime}' IN ('exact', 'metadata_only', 'mismatch'), FALSE) AND
        ((result_kind = 'mismatch' AND result @> '{"published":false}'::jsonb) OR
         (result_kind IN ('exact', 'metadata_only') AND result @> '{"published":true}'::jsonb)) AND
        ((result_kind = 'exact' AND
            result #>> '{match,creation}' = 'exact' AND result #>> '{match,runtime}' = 'exact') OR
         (result_kind = 'metadata_only' AND
            result #>> '{match,creation}' IN ('exact', 'metadata_only') AND
            result #>> '{match,runtime}' IN ('exact', 'metadata_only') AND
            (result #>> '{match,creation}' = 'metadata_only' OR
             result #>> '{match,runtime}' = 'metadata_only')) OR
         (result_kind = 'mismatch' AND
            (result #>> '{match,creation}' = 'mismatch' OR
             result #>> '{match,runtime}' = 'mismatch'))) AND
        octet_length(result::text) <= 268435456
    ),
    CONSTRAINT verification_results_artifact_check CHECK (
        (result_kind = 'mismatch' AND contract_name IS NULL AND abi IS NULL AND
            sources IS NULL AND settings IS NULL) OR
        (result_kind IN ('exact', 'metadata_only') AND
            contract_name IS NOT NULL AND abi IS NOT NULL AND sources IS NOT NULL AND settings IS NOT NULL AND
            length(contract_name) BETWEEN 1 AND 512 AND
            jsonb_typeof(abi) = 'array' AND jsonb_typeof(sources) = 'object' AND
            jsonb_typeof(settings) = 'object' AND
            octet_length(abi::text) + octet_length(sources::text) +
                octet_length(settings::text) <= 268435456)
    )
);

CREATE OR REPLACE FUNCTION reject_verification_result_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'verification results are immutable';
END
$$;

CREATE TRIGGER verification_results_immutable
BEFORE UPDATE OR DELETE ON verification_results
FOR EACH ROW EXECUTE FUNCTION reject_verification_result_mutation();

CREATE OR REPLACE FUNCTION enforce_verification_result_job_state()
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
        FROM verification_results AS immutable_result
        JOIN verification_jobs AS job ON job.id = immutable_result.job_id
        WHERE immutable_result.job_id = target_job
          AND (
              job.status IS DISTINCT FROM 'succeeded' OR
              job.result_kind IS DISTINCT FROM immutable_result.result_kind OR
              job.result IS DISTINCT FROM immutable_result.result OR
              job.error_code IS NOT NULL
          )
    ) THEN
        RAISE EXCEPTION 'verification result and terminal job state disagree';
    END IF;
    RETURN NEW;
END
$$;

CREATE CONSTRAINT TRIGGER verification_results_terminal_job
AFTER INSERT ON verification_results
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION enforce_verification_result_job_state();

CREATE CONSTRAINT TRIGGER verification_jobs_immutable_result
AFTER UPDATE OF status, result_kind, result, error_code ON verification_jobs
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION enforce_verification_result_job_state();

CREATE OR REPLACE FUNCTION enforce_verified_contract_source()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.verification_job_id IS NULL THEN
        RETURN NEW;
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM verification_results AS immutable_result
        JOIN verification_jobs AS job ON job.id = immutable_result.job_id
        WHERE immutable_result.job_id = NEW.verification_job_id
          AND immutable_result.request_digest = NEW.request_digest
          AND immutable_result.chain_id = NEW.chain_id
          AND immutable_result.address = NEW.address
          AND immutable_result.code_hash = NEW.code_hash
          AND immutable_result.block_number = NEW.valid_from_block
          AND immutable_result.result_kind = NEW.match_kind
          AND immutable_result.contract_name IS NOT DISTINCT FROM NEW.contract_name
          AND immutable_result.abi IS NOT DISTINCT FROM NEW.abi
          AND immutable_result.sources IS NOT DISTINCT FROM NEW.sources
          AND immutable_result.settings IS NOT DISTINCT FROM NEW.settings
          AND job.language = NEW.language
          AND job.compiler_version = NEW.compiler_version
    ) THEN
        RAISE EXCEPTION 'verified contract projection disagrees with immutable result';
    END IF;
    RETURN NEW;
END
$$;

DO $migration$
DECLARE
    migration_schema TEXT := current_schema();
BEGIN
    IF migration_schema IS NULL THEN
        RAISE EXCEPTION 'verification migration requires a current schema';
    END IF;
    EXECUTE format(
        'ALTER FUNCTION %I.reject_verification_result_mutation() SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
    EXECUTE format(
        'ALTER FUNCTION %I.enforce_verification_job_immutability() SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
    EXECUTE format(
        'ALTER FUNCTION %I.enforce_verification_result_job_state() SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
    EXECUTE format(
        'ALTER FUNCTION %I.enforce_verified_contract_source() SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
END
$migration$;

ALTER TABLE verified_contracts
    ADD COLUMN verification_job_id UUID,
    ADD COLUMN request_digest BYTEA,
    ADD CONSTRAINT verified_contracts_verification_source_check
        CHECK (
            (verification_job_id IS NULL AND request_digest IS NULL) OR
            (verification_job_id IS NOT NULL AND request_digest IS NOT NULL AND
                octet_length(request_digest) = 32)
        ),
    ADD CONSTRAINT verified_contracts_verification_result_fk
        FOREIGN KEY (verification_job_id, request_digest)
        REFERENCES verification_results (job_id, request_digest) ON DELETE RESTRICT;

CREATE TRIGGER verified_contracts_source_guard
BEFORE INSERT OR UPDATE ON verified_contracts
FOR EACH ROW EXECUTE FUNCTION enforce_verified_contract_source();
