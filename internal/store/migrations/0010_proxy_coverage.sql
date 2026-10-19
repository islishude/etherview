-- Fresh-database baseline: proxy coverage.

-- Functions

CREATE FUNCTION proxy_interaction_coverage_contains(target_chain_id numeric, target_start_block numeric, target_start_hash bytea, target_end_block numeric, target_end_hash bytea) RETURNS boolean
    LANGUAGE sql STABLE STRICT PARALLEL SAFE
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

CREATE FUNCTION record_proxy_history_job_epoch() RETURNS trigger
    LANGUAGE plpgsql
    AS $_$
DECLARE
    block_number_text TEXT;
    block_hash_text TEXT;
BEGIN
    IF NEW.kind <> 'enrichment' OR NEW.stage <> 'proxy' OR
       NEW.stage_version <> 2 OR
       (TG_OP = 'UPDATE' AND
        NEW.requested_generation = OLD.requested_generation) THEN
        RETURN NEW;
    END IF;
    block_number_text := NEW.payload->>'block_number';
    block_hash_text := NEW.payload->>'block_hash';
    IF block_number_text IS NULL OR block_number_text !~ '^[0-9]+$' OR
       block_hash_text IS NULL OR block_hash_text !~ '^0x[0-9a-fA-F]{64}$' THEN
        RAISE EXCEPTION 'proxy history epoch requires an exact block identity';
    END IF;
    INSERT INTO proxy_history_epochs (
        chain_id, block_number, block_hash, durable_job_id,
        requested_generation, phase
    ) VALUES (
        NEW.chain_id, block_number_text::numeric,
        decode(substr(block_hash_text, 3), 'hex'), NEW.id,
        NEW.requested_generation, 'requested'
    ) ON CONFLICT (durable_job_id, requested_generation, phase) DO NOTHING;
    RETURN NEW;
END
$_$;

CREATE FUNCTION record_proxy_history_publication_epoch() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.stage = 'proxy' AND NEW.stage_version = 2 AND
       NEW.durable_job_id IS NOT NULL AND NEW.job_generation IS NOT NULL THEN
        INSERT INTO proxy_history_epochs (
            chain_id, block_number, block_hash, durable_job_id,
            requested_generation, phase
        ) VALUES (
            NEW.chain_id, NEW.block_number, NEW.block_hash,
            NEW.durable_job_id, NEW.job_generation, 'published'
        ) ON CONFLICT (durable_job_id, requested_generation, phase) DO NOTHING;
    END IF;
    RETURN NEW;
END
$$;

CREATE FUNCTION refresh_proxy_interaction_coverage_block(target_chain_id numeric, target_block_number numeric) RETURNS void
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
                ('trace'::text, 4),
                ('state_diff'::text, 3),
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

CREATE FUNCTION refresh_proxy_interaction_coverage_from_canonical() RETURNS trigger
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

CREATE FUNCTION refresh_proxy_interaction_coverage_from_job() RETURNS trigger
    LANGUAGE plpgsql
    AS $_$
DECLARE
    old_relevant BOOLEAN := FALSE;
    new_relevant BOOLEAN := FALSE;
    old_published BOOLEAN := FALSE;
    new_published BOOLEAN := FALSE;
BEGIN
    IF TG_OP IN ('UPDATE', 'DELETE') THEN
        old_relevant := OLD.kind = 'enrichment'
            AND (
                (OLD.stage = 'trace' AND OLD.stage_version = 4)
                OR (OLD.stage = 'state_diff' AND OLD.stage_version = 3)
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
                (NEW.stage = 'trace' AND NEW.stage_version = 4)
                OR (NEW.stage = 'state_diff' AND NEW.stage_version = 3)
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
$_$;

CREATE FUNCTION refresh_proxy_interaction_coverage_from_journal() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    old_relevant BOOLEAN := FALSE;
    new_relevant BOOLEAN := FALSE;
    old_number NUMERIC;
    new_number NUMERIC;
BEGIN
    IF TG_OP IN ('UPDATE', 'DELETE') THEN
        old_relevant := OLD.stage IN ('trace@4', 'state_diff@3', 'proxy@2');
    END IF;
    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        new_relevant := NEW.stage IN ('trace@4', 'state_diff@3', 'proxy@2');
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

CREATE FUNCTION refresh_proxy_interaction_coverage_from_outbox() RETURNS trigger
    LANGUAGE plpgsql
    AS $_$
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
$_$;

CREATE FUNCTION refresh_proxy_interaction_coverage_from_stage_result() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    old_relevant BOOLEAN := FALSE;
    new_relevant BOOLEAN := FALSE;
BEGIN
    IF TG_OP IN ('UPDATE', 'DELETE') THEN
        old_relevant := (OLD.stage = 'trace' AND OLD.stage_version = 4)
            OR (OLD.stage = 'state_diff' AND OLD.stage_version = 3)
            OR (OLD.stage = 'proxy' AND OLD.stage_version = 2);
    END IF;
    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        new_relevant := (NEW.stage = 'trace' AND NEW.stage_version = 4)
            OR (NEW.stage = 'state_diff' AND NEW.stage_version = 3)
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

CREATE FUNCTION reject_proxy_history_epoch_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'proxy history epochs are append-only';
END
$$;


-- Tables

CREATE TABLE proxy_history_epochs (
    epoch_id bigint NOT NULL GENERATED ALWAYS AS IDENTITY (
        SEQUENCE NAME proxy_history_epochs_epoch_id_seq
        START WITH 1
        INCREMENT BY 1
        NO MINVALUE
        NO MAXVALUE
        CACHE 1
    ),
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    durable_job_id bigint NOT NULL,
    requested_generation bigint NOT NULL,
    phase text NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT proxy_history_epochs_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT proxy_history_epochs_block_number_check CHECK ((block_number >= (0)::numeric)),
    CONSTRAINT proxy_history_epochs_phase_check CHECK ((phase = ANY (ARRAY['requested'::text, 'published'::text]))),
    CONSTRAINT proxy_history_epochs_requested_generation_check CHECK ((requested_generation > 0)),
    CONSTRAINT proxy_history_epochs_durable_job_id_requested_generation_ph_key UNIQUE (durable_job_id, requested_generation, phase),
    CONSTRAINT proxy_history_epochs_pkey PRIMARY KEY (epoch_id),
    CONSTRAINT proxy_history_epochs_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT proxy_history_epochs_durable_job_id_fkey FOREIGN KEY (durable_job_id) REFERENCES durable_jobs(id)
);

CREATE TABLE proxy_interaction_coverage_ranges (
    chain_id numeric(78,0) NOT NULL,
    start_block numeric(78,0) NOT NULL,
    start_block_hash bytea NOT NULL,
    end_block numeric(78,0) NOT NULL,
    end_block_hash bytea NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT proxy_interaction_coverage_ranges_check CHECK (((start_block >= (0)::numeric) AND (end_block >= start_block))),
    CONSTRAINT proxy_interaction_coverage_ranges_end_block_hash_check CHECK ((octet_length(end_block_hash) = 32)),
    CONSTRAINT proxy_interaction_coverage_ranges_start_block_hash_check CHECK ((octet_length(start_block_hash) = 32)),
    CONSTRAINT proxy_interaction_coverage_ranges_chain_id_end_block_key UNIQUE (chain_id, end_block),
    CONSTRAINT proxy_interaction_coverage_ranges_pkey PRIMARY KEY (chain_id, start_block),
    CONSTRAINT proxy_interaction_coverage_ranges_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE proxy_interaction_covered_blocks (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT proxy_interaction_covered_blocks_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT proxy_interaction_covered_blocks_block_number_check CHECK ((block_number >= (0)::numeric)),
    CONSTRAINT proxy_interaction_covered_blo_chain_id_block_number_block_h_key UNIQUE (chain_id, block_number, block_hash),
    CONSTRAINT proxy_interaction_covered_blocks_pkey PRIMARY KEY (chain_id, block_number),
    CONSTRAINT proxy_interaction_covered_blo_chain_id_block_number_block__fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash),
    CONSTRAINT proxy_interaction_covered_blocks_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE proxy_replay_targets (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    address bytea NOT NULL,
    target_kind text NOT NULL,
    source_kind text NOT NULL,
    source_verification_job_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT proxy_replay_targets_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT proxy_replay_targets_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT proxy_replay_targets_source_kind_check CHECK ((source_kind = 'verification_publication'::text)),
    CONSTRAINT proxy_replay_targets_target_kind_check CHECK ((target_kind = ANY (ARRAY['proxy'::text, 'beacon'::text, 'uups'::text]))),
    CONSTRAINT proxy_replay_targets_pkey PRIMARY KEY (chain_id, block_hash, address, target_kind, source_kind, source_verification_job_id),
    CONSTRAINT proxy_replay_targets_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash),
    CONSTRAINT proxy_replay_targets_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT proxy_replay_targets_source_verification_job_id_fkey FOREIGN KEY (source_verification_job_id) REFERENCES verification_jobs(id)
);


-- Indexes

CREATE INDEX proxy_history_epochs_latest_idx ON proxy_history_epochs USING btree (chain_id, epoch_id DESC) INCLUDE (block_number, block_hash);

CREATE INDEX proxy_history_epochs_snapshot_idx ON proxy_history_epochs USING btree (chain_id, block_number, epoch_id DESC);

CREATE INDEX proxy_interaction_coverage_ranges_end_idx ON proxy_interaction_coverage_ranges USING btree (chain_id, end_block, start_block);

CREATE INDEX proxy_replay_targets_verification_source_idx ON proxy_replay_targets USING btree (((source_verification_job_id)::text), chain_id, block_hash);


-- Triggers

CREATE TRIGGER block_stage_results_proxy_history_epoch AFTER INSERT OR UPDATE ON block_stage_results FOR EACH ROW EXECUTE FUNCTION record_proxy_history_publication_epoch();

CREATE TRIGGER durable_jobs_proxy_history_epoch AFTER INSERT OR UPDATE ON durable_jobs FOR EACH ROW EXECUTE FUNCTION record_proxy_history_job_epoch();

CREATE TRIGGER proxy_history_epochs_append_only BEFORE DELETE OR UPDATE ON proxy_history_epochs FOR EACH ROW EXECUTE FUNCTION reject_proxy_history_epoch_mutation();

CREATE TRIGGER proxy_interaction_coverage_canonical_trigger AFTER INSERT OR DELETE OR UPDATE ON canonical_blocks FOR EACH ROW EXECUTE FUNCTION refresh_proxy_interaction_coverage_from_canonical();

CREATE TRIGGER proxy_interaction_coverage_job_trigger AFTER INSERT OR DELETE OR UPDATE ON durable_jobs FOR EACH ROW EXECUTE FUNCTION refresh_proxy_interaction_coverage_from_job();

CREATE TRIGGER proxy_interaction_coverage_outbox_trigger AFTER INSERT OR DELETE OR UPDATE ON transactional_outbox FOR EACH ROW EXECUTE FUNCTION refresh_proxy_interaction_coverage_from_outbox();

CREATE TRIGGER proxy_replay_targets_immutable BEFORE DELETE OR UPDATE ON proxy_replay_targets FOR EACH ROW EXECUTE FUNCTION reject_proxy_evidence_mutation();
