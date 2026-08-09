CREATE TABLE IF NOT EXISTS eip7702_authorizations (
    chain_id NUMERIC(78, 0) NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    transaction_hash BYTEA NOT NULL,
    transaction_index BIGINT NOT NULL,
    authorization_index BIGINT NOT NULL,
    authorization_chain_id NUMERIC(78, 0) NOT NULL,
    authorization_nonce NUMERIC(78, 0) NOT NULL,
    delegate_address BYTEA NOT NULL,
    y_parity SMALLINT NOT NULL,
    r BYTEA NOT NULL,
    s BYTEA NOT NULL,
    authority BYTEA,
    signature_status TEXT NOT NULL,
    application_status TEXT NOT NULL,
    skip_reason TEXT,
    canonical BOOLEAN NOT NULL,
    PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, authorization_index),
    FOREIGN KEY (chain_id, block_number, block_hash, transaction_index)
        REFERENCES transaction_inclusions(chain_id, block_number, block_hash, tx_index),
    CHECK (block_number >= 0), CHECK (transaction_index >= 0), CHECK (authorization_index >= 0),
    CHECK (authorization_chain_id >= 0), CHECK (authorization_nonce >= 0),
    CHECK (octet_length(block_hash) = 32), CHECK (octet_length(transaction_hash) = 32),
    CHECK (octet_length(delegate_address) = 20),
    CHECK (authority IS NULL OR octet_length(authority) = 20),
    CHECK (y_parity IN (0, 1)), CHECK (octet_length(r) = 32), CHECK (octet_length(s) = 32),
    CHECK (signature_status IN ('valid', 'invalid', 'unavailable')),
    CHECK (application_status IN ('applied', 'skipped', 'unavailable')),
    CHECK (
        (application_status = 'applied' AND skip_reason IS NULL AND signature_status = 'valid' AND authority IS NOT NULL)
        OR (application_status <> 'applied' AND skip_reason IS NOT NULL)
    )
) PARTITION BY RANGE (block_number);

CREATE TABLE IF NOT EXISTS eip7702_authorizations_p_0_1000000
    PARTITION OF eip7702_authorizations FOR VALUES FROM (0) TO (1000000);
CREATE TABLE IF NOT EXISTS eip7702_authorizations_default
    PARTITION OF eip7702_authorizations DEFAULT;
CREATE INDEX IF NOT EXISTS eip7702_authorizations_tx_idx
    ON eip7702_authorizations (chain_id, transaction_hash, authorization_index);
CREATE INDEX IF NOT EXISTS eip7702_authorizations_authority_idx
    ON eip7702_authorizations (
        chain_id, authority, block_number DESC, transaction_index DESC, authorization_index DESC
    ) WHERE canonical AND authority IS NOT NULL;

CREATE TABLE IF NOT EXISTS transaction_execution_code_resolutions (
    chain_id NUMERIC(78, 0) NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    transaction_hash BYTEA NOT NULL,
    transaction_index BIGINT NOT NULL,
    context_address BYTEA NOT NULL,
    execution_address BYTEA,
    execution_code_hash BYTEA,
    resolution TEXT NOT NULL,
    evidence_source TEXT NOT NULL,
    canonical BOOLEAN NOT NULL,
    PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, context_address),
    FOREIGN KEY (chain_id, block_number, block_hash, transaction_index)
        REFERENCES transaction_inclusions(chain_id, block_number, block_hash, tx_index),
    CHECK (block_number >= 0), CHECK (transaction_index >= 0),
    CHECK (octet_length(block_hash) = 32), CHECK (octet_length(transaction_hash) = 32),
    CHECK (octet_length(context_address) = 20),
    CHECK (execution_address IS NULL OR octet_length(execution_address) = 20),
    CHECK (execution_code_hash IS NULL OR octet_length(execution_code_hash) = 32),
    CHECK (resolution IN ('direct', 'eip7702_delegate', 'empty', 'unavailable')),
    CHECK (evidence_source IN ('prestate_tracer', 'unavailable')),
    CHECK (
        (resolution = 'direct' AND execution_address = context_address AND execution_code_hash IS NOT NULL AND evidence_source = 'prestate_tracer')
        OR (resolution = 'eip7702_delegate' AND execution_address IS NOT NULL AND execution_code_hash IS NOT NULL AND evidence_source = 'prestate_tracer')
        OR (resolution = 'empty' AND execution_address IS NULL AND execution_code_hash IS NULL AND evidence_source = 'prestate_tracer')
        OR (resolution = 'unavailable' AND execution_code_hash IS NULL AND evidence_source = 'unavailable')
    )
) PARTITION BY RANGE (block_number);

CREATE TABLE IF NOT EXISTS transaction_execution_code_resolutions_p_0_1000000
    PARTITION OF transaction_execution_code_resolutions FOR VALUES FROM (0) TO (1000000);
CREATE TABLE IF NOT EXISTS transaction_execution_code_resolutions_default
    PARTITION OF transaction_execution_code_resolutions DEFAULT;
CREATE INDEX IF NOT EXISTS transaction_execution_code_resolutions_tx_idx
    ON transaction_execution_code_resolutions (chain_id, transaction_hash, context_address) WHERE canonical;
CREATE INDEX IF NOT EXISTS transaction_execution_code_resolutions_execution_idx
    ON transaction_execution_code_resolutions (chain_id, execution_address, block_number DESC)
    WHERE canonical AND execution_address IS NOT NULL;

ALTER TABLE normalized_traces
    ADD COLUMN IF NOT EXISTS execution_address BYTEA,
    ADD COLUMN IF NOT EXISTS execution_code_hash BYTEA,
    ADD COLUMN IF NOT EXISTS execution_resolution TEXT NOT NULL DEFAULT 'unavailable';
ALTER TABLE normalized_traces
    ADD CONSTRAINT normalized_traces_execution_address_length_check
        CHECK (execution_address IS NULL OR octet_length(execution_address) = 20),
    ADD CONSTRAINT normalized_traces_execution_code_hash_length_check
        CHECK (execution_code_hash IS NULL OR octet_length(execution_code_hash) = 32),
    ADD CONSTRAINT normalized_traces_execution_resolution_check
        CHECK (execution_resolution IN ('direct', 'eip7702_delegate', 'empty', 'unavailable', 'not_applicable')),
    ADD CONSTRAINT normalized_traces_execution_identity_check
        CHECK (
            (execution_resolution IN ('direct', 'eip7702_delegate') AND execution_address IS NOT NULL AND execution_code_hash IS NOT NULL)
            OR (execution_resolution IN ('empty', 'not_applicable') AND execution_address IS NULL AND execution_code_hash IS NULL)
            OR (execution_resolution = 'unavailable' AND execution_code_hash IS NULL)
        );

ALTER TABLE abi_decodings
    ADD COLUMN IF NOT EXISTS decoding_kind TEXT NOT NULL DEFAULT 'function';

DO $$
DECLARE
    constraint_name TEXT;
    definition TEXT;
BEGIN
    FOR constraint_name, definition IN
        SELECT conname, pg_get_constraintdef(oid)
        FROM pg_constraint
        WHERE conrelid = 'abi_decodings'::regclass AND contype = 'c'
    LOOP
        IF (position('object_kind' IN definition) > 0 AND position('trace_calldata' IN definition) > 0)
           OR (position('abi_kind' IN definition) > 0 AND position('function' IN definition) > 0) THEN
            EXECUTE format('ALTER TABLE abi_decodings DROP CONSTRAINT %I', constraint_name);
        END IF;
    END LOOP;
END
$$;

-- A stage-result or journal row is not published while its durable job is
-- leased. Refreshing coverage from those writes therefore cannot make the
-- projection visible, but it does take the chain coverage lock before the
-- processor requests dependent-job replay locks. Keep the canonical/outbox
-- refreshes and move publication/removal refreshes to the durable job's exact
-- unpublished <-> published transition below so every path orders job locks
-- before the coverage lock.
DROP TRIGGER IF EXISTS proxy_interaction_coverage_stage_result_trigger
    ON block_stage_results;
DROP TRIGGER IF EXISTS proxy_interaction_coverage_journal_trigger
    ON block_journals;
DROP TRIGGER IF EXISTS proxy_interaction_coverage_job_trigger ON durable_jobs;

ALTER TABLE abi_decodings
    ADD CONSTRAINT abi_decodings_decoding_kind_check
        CHECK (decoding_kind IN ('function', 'constructor', 'event', 'error')),
    ADD CONSTRAINT abi_decodings_object_kind_v3_check
        CHECK (object_kind IN (
            'transaction_calldata', 'log', 'trace_calldata',
            'trace_constructor', 'trace_revert'
        )),
    ADD CONSTRAINT abi_decodings_abi_kind_v3_check
        CHECK (abi_kind IN ('function', 'constructor', 'event', 'error'));

-- Versioned meaning changes are rebuilt only by explicit bounded reindex.
TRUNCATE TABLE proxy_interaction_coverage_ranges, proxy_interaction_covered_blocks;

DO $$
DECLARE
    target REGPROCEDURE;
    definition TEXT;
BEGIN
    FOREACH target IN ARRAY ARRAY[
        'refresh_proxy_interaction_coverage_block(numeric,numeric)'::regprocedure,
        'refresh_proxy_interaction_coverage_from_stage_result()'::regprocedure,
        'refresh_proxy_interaction_coverage_from_job()'::regprocedure,
        'refresh_proxy_interaction_coverage_from_journal()'::regprocedure
    ] LOOP
        definition := pg_get_functiondef(target);
        definition := replace(definition, '''trace''::text, 2', '''trace''::text, 3');
        definition := replace(definition, '''trace'' AND OLD.stage_version = 2', '''trace'' AND OLD.stage_version = 3');
        definition := replace(definition, '''trace'' AND NEW.stage_version = 2', '''trace'' AND NEW.stage_version = 3');
        definition := replace(definition, '''state_diff''::text, 1', '''state_diff''::text, 2');
        definition := replace(definition, '''state_diff'' AND OLD.stage_version = 1', '''state_diff'' AND OLD.stage_version = 2');
        definition := replace(definition, '''state_diff'' AND NEW.stage_version = 1', '''state_diff'' AND NEW.stage_version = 2');
        definition := replace(definition, '''trace@2''', '''trace@3''');
        definition := replace(definition, '''state_diff@1''', '''state_diff@2''');
        EXECUTE definition;
    END LOOP;
END
$$;

CREATE OR REPLACE FUNCTION refresh_proxy_interaction_coverage_from_job()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    old_relevant BOOLEAN := FALSE;
    new_relevant BOOLEAN := FALSE;
    old_published BOOLEAN := FALSE;
    new_published BOOLEAN := FALSE;
BEGIN
    IF TG_OP IN ('UPDATE', 'DELETE') THEN
        old_relevant := OLD.kind = 'enrichment'
            AND (
                (OLD.stage = 'trace' AND OLD.stage_version = 3)
                OR (OLD.stage = 'state_diff' AND OLD.stage_version = 2)
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
                (NEW.stage = 'trace' AND NEW.stage_version = 3)
                OR (NEW.stage = 'state_diff' AND NEW.stage_version = 2)
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
$$;

CREATE TRIGGER proxy_interaction_coverage_job_trigger
AFTER INSERT OR UPDATE OR DELETE ON durable_jobs
FOR EACH ROW EXECUTE FUNCTION refresh_proxy_interaction_coverage_from_job();
