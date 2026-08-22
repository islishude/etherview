ALTER TABLE derived_verification_scans
    DROP CONSTRAINT derived_verification_scans_pkey,
    ADD COLUMN id BIGINT GENERATED ALWAYS AS IDENTITY,
    ADD COLUMN redispatch_requested BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE derived_verification_scans
    ADD PRIMARY KEY (id),
    ADD CONSTRAINT derived_verification_scans_target_key UNIQUE (
        compilation_id, creator_address, creator_code_hash, valid_from_block
    );

CREATE TABLE derived_verification_forward_blocks (
    chain_id NUMERIC(78, 0) NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued',
    redispatch_requested BOOLEAN NOT NULL DEFAULT FALSE,
    leased_by TEXT,
    lease_token TEXT,
    lease_expires_at TIMESTAMPTZ,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 5,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, block_hash),
    FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash) ON DELETE RESTRICT,
    CONSTRAINT derived_verification_forward_blocks_identity_check CHECK (
        block_number >= 0 AND octet_length(block_hash) = 32
    ),
    CONSTRAINT derived_verification_forward_blocks_status_check CHECK (
        status IN ('queued', 'running', 'succeeded', 'failed')
    ),
    CONSTRAINT derived_verification_forward_blocks_lease_check CHECK (
        (leased_by IS NULL) = (lease_token IS NULL) AND
        (leased_by IS NULL) = (lease_expires_at IS NULL) AND
        (status = 'running') = (leased_by IS NOT NULL)
    ),
    CONSTRAINT derived_verification_forward_blocks_attempt_check CHECK (
        attempt_count >= 0 AND max_attempts BETWEEN 1 AND 100 AND
        attempt_count <= max_attempts
    )
);

CREATE INDEX derived_verification_forward_blocks_claim_idx
    ON derived_verification_forward_blocks (status, updated_at, chain_id, block_number)
    WHERE status IN ('queued', 'running');

CREATE FUNCTION enqueue_derived_forward_block_after_trace_publication()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.stage = 'trace' AND NEW.stage_version = 3 AND NEW.state = 'complete' AND
       EXISTS (
           SELECT 1 FROM canonical_blocks AS canonical
           WHERE canonical.chain_id = NEW.chain_id
             AND canonical.number = NEW.block_number
             AND canonical.block_hash = NEW.block_hash
       ) THEN
        INSERT INTO derived_verification_forward_blocks (
            chain_id, block_number, block_hash
        ) VALUES (NEW.chain_id, NEW.block_number, NEW.block_hash)
        ON CONFLICT (chain_id, block_hash) DO UPDATE
        SET status = CASE
                WHEN derived_verification_forward_blocks.status = 'running'
                THEN derived_verification_forward_blocks.status
                ELSE 'queued'
            END,
            redispatch_requested =
                derived_verification_forward_blocks.status = 'running',
            last_error = NULL,
            updated_at = clock_timestamp();
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER block_stage_results_derived_forward_enqueue
AFTER INSERT OR UPDATE OF state ON block_stage_results
FOR EACH ROW EXECUTE FUNCTION enqueue_derived_forward_block_after_trace_publication();

DO $migration$
DECLARE
    migration_schema TEXT := current_schema();
BEGIN
    IF migration_schema IS NULL THEN
        RAISE EXCEPTION 'forward derived verification migration requires a current schema';
    END IF;
    EXECUTE format(
        'ALTER FUNCTION %I.enqueue_derived_forward_block_after_trace_publication() SET search_path = %I, pg_catalog',
        migration_schema,
        migration_schema
    );
END
$migration$;

ALTER TABLE derived_verification_attempts
    DROP CONSTRAINT derived_verification_attempts_outcome_check,
    ADD CONSTRAINT derived_verification_attempts_outcome_check CHECK (
        (
            status = 'matched' AND stale_from_status IS NULL AND
            file_name IS NOT NULL AND contract_name IS NOT NULL AND
            jsonb_typeof(creation_match) = 'object' AND
            jsonb_typeof(runtime_match) = 'object' AND
            verification_job_id IS NOT NULL
        ) OR (
            status NOT IN ('matched', 'stale') AND stale_from_status IS NULL AND
            file_name IS NULL AND contract_name IS NULL AND
            creation_match IS NULL AND runtime_match IS NULL AND
            verification_job_id IS NULL
        ) OR (
            status = 'stale' AND stale_from_status IS NULL AND
            file_name IS NULL AND contract_name IS NULL AND
            creation_match IS NULL AND runtime_match IS NULL AND
            verification_job_id IS NULL
        ) OR (
            status = 'stale' AND stale_from_status IS NOT NULL AND
            (
                (stale_from_status = 'matched' AND file_name IS NOT NULL AND
                 contract_name IS NOT NULL AND
                 jsonb_typeof(creation_match) = 'object' AND
                 jsonb_typeof(runtime_match) = 'object' AND
                 verification_job_id IS NOT NULL) OR
                (stale_from_status <> 'matched' AND file_name IS NULL AND
                 contract_name IS NULL AND creation_match IS NULL AND
                 runtime_match IS NULL AND verification_job_id IS NULL)
            )
        )
    );
