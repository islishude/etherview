ALTER TABLE normalized_traces
    ADD COLUMN IF NOT EXISTS direct_reverted BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE abi_decodings
    ADD COLUMN IF NOT EXISTS source_address BYTEA,
    ADD COLUMN IF NOT EXISTS source_code_hash BYTEA,
    ADD COLUMN IF NOT EXISTS return_status TEXT NOT NULL DEFAULT 'not_applicable',
    ADD COLUMN IF NOT EXISTS return_arguments JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE abi_decodings
    ADD CONSTRAINT abi_decodings_source_address_length_check
        CHECK (source_address IS NULL OR octet_length(source_address) = 20),
    ADD CONSTRAINT abi_decodings_source_code_hash_length_check
        CHECK (source_code_hash IS NULL OR octet_length(source_code_hash) = 32),
    ADD CONSTRAINT abi_decodings_source_identity_pair_check
        CHECK ((source_address IS NULL) = (source_code_hash IS NULL)),
    ADD CONSTRAINT abi_decodings_return_status_check
        CHECK (return_status IN (
            'decoded', 'empty', 'unknown', 'malformed', 'unavailable',
            'not_applicable'
        )),
    ADD CONSTRAINT abi_decodings_return_arguments_array_check
        CHECK (jsonb_typeof(return_arguments) = 'array');

-- Upgrade ABI source provenance without relying on PostgreSQL's generated
-- names for the checks introduced by migration 0008.
DO $$
DECLARE
    constraint_name TEXT;
    definition TEXT;
BEGIN
    FOR constraint_name, definition IN
        SELECT constraint_row.conname,
               pg_get_constraintdef(constraint_row.oid)
        FROM pg_constraint AS constraint_row
        WHERE constraint_row.conrelid = 'contract_abis'::regclass
          AND constraint_row.contype = 'c'
    LOOP
        IF position('signature_database' IN definition) > 0 OR
           (position('source_address' IN definition) > 0 AND
            position('source_code_hash' IN definition) > 0 AND
            position('address' IN definition) > 0 AND
            position('code_hash' IN definition) > 0) THEN
            EXECUTE format(
                'ALTER TABLE contract_abis DROP CONSTRAINT %I',
                constraint_name
            );
        END IF;
    END LOOP;

    FOR constraint_name, definition IN
        SELECT constraint_row.conname,
               pg_get_constraintdef(constraint_row.oid)
        FROM pg_constraint AS constraint_row
        WHERE constraint_row.conrelid = 'abi_decodings'::regclass
          AND constraint_row.contype = 'c'
    LOOP
        IF position('signature_database' IN definition) > 0 AND
           position('builtin' IN definition) > 0 AND
           position('confidence' IN definition) > 0 THEN
            EXECUTE format(
                'ALTER TABLE abi_decodings DROP CONSTRAINT %I',
                constraint_name
            );
        END IF;
    END LOOP;
END
$$;

ALTER TABLE contract_abis
    ADD CONSTRAINT contract_abis_source_v2_check
        CHECK (source IN (
            'verified', 'code_hash', 'proxy_implementation',
            'signature_database'
        )),
    ADD CONSTRAINT contract_abis_confidence_v2_check
        CHECK (
            (source = 'verified' AND confidence = 'verified') OR
            (source IN ('code_hash', 'proxy_implementation') AND
             confidence = 'high') OR
            (source = 'signature_database' AND confidence = 'guess')
        ),
    ADD CONSTRAINT contract_abis_source_identity_v2_check
        CHECK (
            source IN ('code_hash', 'proxy_implementation') OR
            (source_address = address AND source_code_hash = code_hash)
        );

ALTER TABLE abi_decodings
    ADD CONSTRAINT abi_decodings_source_confidence_v2_check
        CHECK (
            (source IS NULL AND confidence IS NULL) OR
            (source = 'verified' AND confidence = 'verified') OR
            (source IN ('code_hash', 'proxy_implementation', 'builtin') AND
             confidence = 'high') OR
            (source = 'signature_database' AND confidence = 'guess')
        );

CREATE TABLE IF NOT EXISTS trace_log_attributions (
    chain_id NUMERIC(78, 0) NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    transaction_hash BYTEA NOT NULL,
    log_index BIGINT NOT NULL,
    trace_path TEXT NOT NULL,
    call_type TEXT NOT NULL,
    execution_address BYTEA NOT NULL,
    canonical BOOLEAN NOT NULL,
    PRIMARY KEY (
        chain_id, block_number, block_hash, transaction_hash, log_index
    ),
    FOREIGN KEY (
        chain_id, block_number, block_hash, transaction_hash, trace_path
    ) REFERENCES normalized_traces (
        chain_id, block_number, block_hash, transaction_hash, trace_path
    ),
    FOREIGN KEY (chain_id, block_number, block_hash, log_index)
        REFERENCES logs(chain_id, block_number, block_hash, log_index),
    CHECK (log_index >= 0),
    CHECK (octet_length(block_hash) = 32),
    CHECK (octet_length(transaction_hash) = 32),
    CHECK (octet_length(execution_address) = 20),
    CHECK (call_type IN (
        'CALL', 'CALLCODE', 'DELEGATECALL', 'STATICCALL', 'CREATE', 'CREATE2'
    ))
) PARTITION BY RANGE (block_number);

CREATE TABLE IF NOT EXISTS trace_log_attributions_p_0_1000000
    PARTITION OF trace_log_attributions FOR VALUES FROM (0) TO (1000000);
CREATE TABLE IF NOT EXISTS trace_log_attributions_default
    PARTITION OF trace_log_attributions DEFAULT;
CREATE INDEX IF NOT EXISTS trace_log_attributions_frame_idx
    ON trace_log_attributions (
        chain_id, transaction_hash, trace_path, log_index
    ) WHERE canonical;


-- Rebind proxy-interaction coverage to the trace@2 publication fence.
CREATE OR REPLACE FUNCTION refresh_proxy_interaction_coverage_block(
    target_chain_id NUMERIC,
    target_block_number NUMERIC
)
RETURNS void
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
                ('trace'::text, 2),
                ('state_diff'::text, 1),
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

CREATE OR REPLACE FUNCTION refresh_proxy_interaction_coverage_from_canonical()
RETURNS trigger
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

CREATE OR REPLACE FUNCTION refresh_proxy_interaction_coverage_from_stage_result()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    old_relevant BOOLEAN := FALSE;
    new_relevant BOOLEAN := FALSE;
BEGIN
    IF TG_OP IN ('UPDATE', 'DELETE') THEN
        old_relevant := (OLD.stage = 'trace' AND OLD.stage_version = 2)
            OR (OLD.stage = 'state_diff' AND OLD.stage_version = 2)
            OR (OLD.stage = 'proxy' AND OLD.stage_version = 2);
    END IF;
    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        new_relevant := (NEW.stage = 'trace' AND NEW.stage_version = 2)
            OR (NEW.stage = 'state_diff' AND NEW.stage_version = 2)
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

CREATE OR REPLACE FUNCTION refresh_proxy_interaction_coverage_from_job()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    old_relevant BOOLEAN := FALSE;
    new_relevant BOOLEAN := FALSE;
BEGIN
    IF TG_OP = 'UPDATE'
       AND NEW.chain_id IS NOT DISTINCT FROM OLD.chain_id
       AND NEW.kind IS NOT DISTINCT FROM OLD.kind
       AND NEW.stage IS NOT DISTINCT FROM OLD.stage
       AND NEW.stage_version IS NOT DISTINCT FROM OLD.stage_version
       AND NEW.payload IS NOT DISTINCT FROM OLD.payload
       AND NEW.status IS NOT DISTINCT FROM OLD.status
       AND NEW.requested_generation IS NOT DISTINCT FROM OLD.requested_generation
       AND NEW.claimed_generation IS NOT DISTINCT FROM OLD.claimed_generation
       AND NEW.leased_generation IS NOT DISTINCT FROM OLD.leased_generation
       AND NEW.completed_generation IS NOT DISTINCT FROM OLD.completed_generation
       AND NEW.leased_by IS NOT DISTINCT FROM OLD.leased_by
       AND NEW.lease_token IS NOT DISTINCT FROM OLD.lease_token
       AND NEW.lease_expires_at IS NOT DISTINCT FROM OLD.lease_expires_at
       AND NEW.result IS NOT DISTINCT FROM OLD.result THEN
        RETURN NULL;
    END IF;
    IF TG_OP IN ('UPDATE', 'DELETE') THEN
        old_relevant := OLD.kind = 'enrichment'
            AND (
                (OLD.stage = 'trace' AND OLD.stage_version = 2)
                OR (OLD.stage = 'state_diff' AND OLD.stage_version = 2)
                OR (OLD.stage = 'proxy' AND OLD.stage_version = 2)
            )
            AND COALESCE(
                OLD.payload->>'block_number' ~ '^(0|[1-9][0-9]*)$',
                FALSE
            );
    END IF;
    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        new_relevant := NEW.kind = 'enrichment'
            AND (
                (NEW.stage = 'trace' AND NEW.stage_version = 2)
                OR (NEW.stage = 'state_diff' AND NEW.stage_version = 2)
                OR (NEW.stage = 'proxy' AND NEW.stage_version = 2)
            )
            AND COALESCE(
                NEW.payload->>'block_number' ~ '^(0|[1-9][0-9]*)$',
                FALSE
            );
    END IF;
    IF old_relevant THEN
        PERFORM refresh_proxy_interaction_coverage_block(
            OLD.chain_id, (OLD.payload->>'block_number')::numeric
        );
    END IF;
    IF new_relevant AND (
        NOT old_relevant
        OR NEW.chain_id IS DISTINCT FROM OLD.chain_id
        OR NEW.payload->>'block_number' IS DISTINCT FROM
           OLD.payload->>'block_number'
    ) THEN
        PERFORM refresh_proxy_interaction_coverage_block(
            NEW.chain_id, (NEW.payload->>'block_number')::numeric
        );
    END IF;
    RETURN NULL;
END
$$;

CREATE OR REPLACE FUNCTION refresh_proxy_interaction_coverage_from_journal()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    old_relevant BOOLEAN := FALSE;
    new_relevant BOOLEAN := FALSE;
    old_number NUMERIC;
    new_number NUMERIC;
BEGIN
    IF TG_OP IN ('UPDATE', 'DELETE') THEN
        old_relevant := OLD.stage IN ('trace@2', 'state_diff@1', 'proxy@2');
    END IF;
    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        new_relevant := NEW.stage IN ('trace@2', 'state_diff@1', 'proxy@2');
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



-- trace@2 is a correctness boundary for proxy-interaction coverage. Existing
-- trace@1-derived membership is discarded and rebuilt incrementally by the
-- explicit trace reindex; the migration does not enqueue historical work.
TRUNCATE TABLE proxy_interaction_coverage_ranges,
               proxy_interaction_covered_blocks;

DO $$
DECLARE
    migration_schema TEXT := current_schema();
BEGIN
    EXECUTE format(
        'ALTER FUNCTION %I.refresh_proxy_interaction_coverage_block(numeric, numeric) SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
    EXECUTE format(
        'ALTER FUNCTION %I.refresh_proxy_interaction_coverage_from_canonical() SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
    EXECUTE format(
        'ALTER FUNCTION %I.refresh_proxy_interaction_coverage_from_stage_result() SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
    EXECUTE format(
        'ALTER FUNCTION %I.refresh_proxy_interaction_coverage_from_job() SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
    EXECUTE format(
        'ALTER FUNCTION %I.refresh_proxy_interaction_coverage_from_journal() SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
END
$$;
