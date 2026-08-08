-- Proxy interaction reads must prove that trace@1, state_diff@1, and proxy@2
-- are all published for every canonical block in an interval. Materialize the
-- exact canonical covered blocks and their maximal contiguous ranges so a
-- binding check is logarithmic in the number of gaps, not linear in chain
-- height. Block hashes remain part of both the block membership and range
-- endpoints; a detached hash can therefore never satisfy a later canonical
-- interval merely because its height is reused.
LOCK TABLE canonical_blocks, block_stage_results, durable_jobs,
           block_journals, transactional_outbox
    IN SHARE ROW EXCLUSIVE MODE;

CREATE TABLE proxy_interaction_covered_blocks (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (chain_id, block_number),
    UNIQUE (chain_id, block_number, block_hash),
    FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash),
    CHECK (block_number >= 0),
    CHECK (octet_length(block_hash) = 32)
);

CREATE TABLE proxy_interaction_coverage_ranges (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    start_block NUMERIC(78, 0) NOT NULL,
    start_block_hash BYTEA NOT NULL,
    end_block NUMERIC(78, 0) NOT NULL,
    end_block_hash BYTEA NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (chain_id, start_block),
    UNIQUE (chain_id, end_block),
    CHECK (start_block >= 0 AND end_block >= start_block),
    CHECK (octet_length(start_block_hash) = 32),
    CHECK (octet_length(end_block_hash) = 32)
);

CREATE INDEX proxy_interaction_coverage_ranges_end_idx
    ON proxy_interaction_coverage_ranges (chain_id, end_block, start_block);

CREATE OR REPLACE FUNCTION proxy_interaction_coverage_contains(
    target_chain_id NUMERIC,
    target_start_block NUMERIC,
    target_start_hash BYTEA,
    target_end_block NUMERIC,
    target_end_hash BYTEA
)
RETURNS BOOLEAN
LANGUAGE sql
STABLE
STRICT
PARALLEL SAFE
AS $$
    SELECT EXISTS (
        SELECT 1
        FROM (
            SELECT candidate.*
            FROM proxy_interaction_coverage_ranges AS candidate
            WHERE candidate.chain_id = target_chain_id
              AND candidate.start_block <= target_start_block
            ORDER BY candidate.start_block DESC
            LIMIT 1
        ) AS coverage
        JOIN proxy_interaction_covered_blocks AS required_start
          ON required_start.chain_id = target_chain_id
         AND required_start.block_number = target_start_block
         AND required_start.block_hash = target_start_hash
        JOIN proxy_interaction_covered_blocks AS required_end
          ON required_end.chain_id = target_chain_id
         AND required_end.block_number = target_end_block
         AND required_end.block_hash = target_end_hash
        JOIN proxy_interaction_covered_blocks AS range_start
          ON range_start.chain_id = coverage.chain_id
         AND range_start.block_number = coverage.start_block
         AND range_start.block_hash = coverage.start_block_hash
        JOIN proxy_interaction_covered_blocks AS range_end
          ON range_end.chain_id = coverage.chain_id
         AND range_end.block_number = coverage.end_block
         AND range_end.block_hash = coverage.end_block_hash
        WHERE coverage.end_block >= target_end_block
          AND target_start_block <= target_end_block
    )
$$;

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
                ('trace'::text, 1),
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
        old_relevant := (OLD.stage = 'trace' AND OLD.stage_version = 1)
            OR (OLD.stage = 'state_diff' AND OLD.stage_version = 1)
            OR (OLD.stage = 'proxy' AND OLD.stage_version = 2);
    END IF;
    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        new_relevant := (NEW.stage = 'trace' AND NEW.stage_version = 1)
            OR (NEW.stage = 'state_diff' AND NEW.stage_version = 1)
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
                (OLD.stage = 'trace' AND OLD.stage_version = 1)
                OR (OLD.stage = 'state_diff' AND OLD.stage_version = 1)
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
                (NEW.stage = 'trace' AND NEW.stage_version = 1)
                OR (NEW.stage = 'state_diff' AND NEW.stage_version = 1)
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
        old_relevant := OLD.stage IN ('trace@1', 'state_diff@1', 'proxy@2');
    END IF;
    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        new_relevant := NEW.stage IN ('trace@1', 'state_diff@1', 'proxy@2');
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

CREATE OR REPLACE FUNCTION refresh_proxy_interaction_coverage_from_outbox()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    old_relevant BOOLEAN := FALSE;
    new_relevant BOOLEAN := FALSE;
    old_number NUMERIC;
    new_number NUMERIC;
    old_hash BYTEA;
    new_hash BYTEA;
BEGIN
    IF TG_OP IN ('UPDATE', 'DELETE') THEN
        old_relevant := OLD.topic = 'core.block.canonical'
            AND COALESCE(OLD.message_key ~ '^0x[0-9a-f]{64}$', FALSE);
    END IF;
    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        new_relevant := NEW.topic = 'core.block.canonical'
            AND COALESCE(NEW.message_key ~ '^0x[0-9a-f]{64}$', FALSE);
    END IF;
    IF TG_OP = 'UPDATE'
       AND NEW.published_at IS NOT DISTINCT FROM OLD.published_at
       AND NEW.topic IS NOT DISTINCT FROM OLD.topic
       AND NEW.message_key IS NOT DISTINCT FROM OLD.message_key
       AND NEW.chain_id IS NOT DISTINCT FROM OLD.chain_id THEN
        RETURN NULL;
    END IF;
    IF old_relevant THEN
        old_hash := decode(substr(OLD.message_key, 3), 'hex');
        SELECT block.number
        INTO old_number
        FROM blocks AS block
        WHERE block.chain_id = OLD.chain_id
          AND block.hash = old_hash;
        IF FOUND THEN
            PERFORM refresh_proxy_interaction_coverage_block(
                OLD.chain_id, old_number
            );
        END IF;
    END IF;
    IF new_relevant AND (
        NOT old_relevant
        OR NEW.chain_id IS DISTINCT FROM OLD.chain_id
        OR NEW.message_key IS DISTINCT FROM OLD.message_key
    ) THEN
        new_hash := decode(substr(NEW.message_key, 3), 'hex');
        SELECT block.number
        INTO new_number
        FROM blocks AS block
        WHERE block.chain_id = NEW.chain_id
          AND block.hash = new_hash;
        IF FOUND THEN
            PERFORM refresh_proxy_interaction_coverage_block(
                NEW.chain_id, new_number
            );
        END IF;
    END IF;
    RETURN NULL;
END
$$;

-- Backfill under the writer locks above before enabling incremental upkeep.
INSERT INTO proxy_interaction_covered_blocks (
    chain_id, block_number, block_hash
)
SELECT canonical.chain_id, canonical.number, canonical.block_hash
FROM canonical_blocks AS canonical
WHERE NOT EXISTS (
    SELECT 1
    FROM (VALUES
        ('trace'::text, 1),
        ('state_diff'::text, 1),
        ('proxy'::text, 2)
    ) AS required(stage, stage_version)
    WHERE NOT EXISTS (
        SELECT 1
        FROM published_block_stage_results AS published
        WHERE published.chain_id = canonical.chain_id
          AND published.block_number = canonical.number
          AND published.block_hash = canonical.block_hash
          AND published.stage = required.stage
          AND published.stage_version = required.stage_version
          AND published.state = 'complete'
    )
);

WITH numbered AS (
    SELECT covered.*,
           covered.block_number - row_number() OVER (
               PARTITION BY covered.chain_id ORDER BY covered.block_number
           )::numeric AS island
    FROM proxy_interaction_covered_blocks AS covered
), grouped AS (
    SELECT chain_id, island,
           min(block_number) AS start_block,
           max(block_number) AS end_block
    FROM numbered
    GROUP BY chain_id, island
)
INSERT INTO proxy_interaction_coverage_ranges (
    chain_id, start_block, start_block_hash, end_block, end_block_hash
)
SELECT grouped.chain_id, grouped.start_block, range_start.block_hash,
       grouped.end_block, range_end.block_hash
FROM grouped
JOIN proxy_interaction_covered_blocks AS range_start
  ON range_start.chain_id = grouped.chain_id
 AND range_start.block_number = grouped.start_block
JOIN proxy_interaction_covered_blocks AS range_end
  ON range_end.chain_id = grouped.chain_id
 AND range_end.block_number = grouped.end_block;

CREATE TRIGGER proxy_interaction_coverage_canonical_trigger
AFTER INSERT OR UPDATE OR DELETE ON canonical_blocks
FOR EACH ROW EXECUTE FUNCTION refresh_proxy_interaction_coverage_from_canonical();

CREATE TRIGGER proxy_interaction_coverage_stage_result_trigger
AFTER INSERT OR UPDATE OR DELETE ON block_stage_results
FOR EACH ROW EXECUTE FUNCTION refresh_proxy_interaction_coverage_from_stage_result();

CREATE TRIGGER proxy_interaction_coverage_job_trigger
AFTER INSERT OR UPDATE OR DELETE ON durable_jobs
FOR EACH ROW EXECUTE FUNCTION refresh_proxy_interaction_coverage_from_job();

CREATE TRIGGER proxy_interaction_coverage_journal_trigger
AFTER INSERT OR UPDATE OR DELETE ON block_journals
FOR EACH ROW EXECUTE FUNCTION refresh_proxy_interaction_coverage_from_journal();

CREATE TRIGGER proxy_interaction_coverage_outbox_trigger
AFTER INSERT OR UPDATE OR DELETE ON transactional_outbox
FOR EACH ROW EXECUTE FUNCTION refresh_proxy_interaction_coverage_from_outbox();

-- Trigger relation lookup must stay inside the installation schema even when
-- callers supply a hostile or merely different search_path.
DO $$
DECLARE
    migration_schema TEXT := current_schema();
BEGIN
    EXECUTE format(
        'ALTER FUNCTION %I.proxy_interaction_coverage_contains(numeric, numeric, bytea, numeric, bytea) SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
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
    EXECUTE format(
        'ALTER FUNCTION %I.refresh_proxy_interaction_coverage_from_outbox() SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
END
$$;
