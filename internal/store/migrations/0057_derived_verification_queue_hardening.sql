-- P72 makes forward derived work generation-bound and gives a running scan a
-- durable rewind floor that an older lease cannot overwrite.
ALTER TABLE derived_verification_scans
    ADD COLUMN rescan_from_block NUMERIC(78, 0);

UPDATE derived_verification_scans
SET rescan_from_block = cursor_block_number
WHERE redispatch_requested;

ALTER TABLE derived_verification_scans
    DROP COLUMN redispatch_requested,
    ADD CONSTRAINT derived_verification_scans_rescan_check CHECK (
        rescan_from_block IS NULL OR
        (rescan_from_block >= valid_from_block AND
         (valid_to_block IS NULL OR rescan_from_block <= valid_to_block))
    );

DROP TRIGGER IF EXISTS block_stage_results_derived_forward_enqueue
    ON block_stage_results;
DROP FUNCTION IF EXISTS enqueue_derived_forward_block_after_trace_publication();

ALTER TABLE derived_verification_forward_blocks
    DROP CONSTRAINT derived_verification_forward_blocks_pkey,
    DROP COLUMN redispatch_requested,
    ADD COLUMN id BIGINT GENERATED ALWAYS AS IDENTITY,
    ADD COLUMN source_stage TEXT,
    ADD COLUMN source_job_id BIGINT,
    ADD COLUMN source_generation BIGINT;

-- Existing forward state remains audit evidence. Bind it to the most recent
-- exact trace publication where one exists; an unbound legacy row fails closed
-- and is never claimed by the generation-aware worker.
WITH bound AS (
    SELECT event.id, source.job_id, source.job_generation
    FROM derived_verification_forward_blocks AS event
    JOIN LATERAL (
        SELECT publication.job_id, publication.job_generation
        FROM durable_stage_publications AS publication
        WHERE publication.chain_id = event.chain_id
          AND publication.block_number = event.block_number
          AND publication.block_hash = event.block_hash
          AND publication.stage = 'trace'
          AND publication.stage_version = 3
          AND publication.state = 'complete'
        ORDER BY publication.job_generation DESC, publication.job_id DESC
        LIMIT 1
    ) AS source ON TRUE
)
UPDATE derived_verification_forward_blocks AS event
SET source_stage = 'trace',
    source_job_id = bound.job_id,
    source_generation = bound.job_generation,
    status = CASE WHEN event.status = 'running' THEN 'queued' ELSE event.status END,
    leased_by = NULL,
    lease_token = NULL,
    lease_expires_at = NULL
FROM bound
WHERE event.id = bound.id;

UPDATE derived_verification_forward_blocks
SET status = 'failed',
    last_error = 'unbound_source_generation',
    leased_by = NULL,
    lease_token = NULL,
    lease_expires_at = NULL
WHERE source_job_id IS NULL;

ALTER TABLE derived_verification_forward_blocks
    ADD PRIMARY KEY (id),
    ADD CONSTRAINT derived_verification_forward_blocks_source_unique
        UNIQUE (source_job_id, source_generation),
    ADD CONSTRAINT derived_verification_forward_blocks_source_fk
        FOREIGN KEY (source_job_id, source_generation)
        REFERENCES durable_stage_publications(job_id, job_generation)
        ON DELETE RESTRICT,
    ADD CONSTRAINT derived_verification_forward_blocks_source_check CHECK (
        (
            source_stage IN ('trace', 'proxy') AND
            source_job_id IS NOT NULL AND source_generation IS NOT NULL AND
            source_generation > 0
        ) OR (
            status = 'failed' AND last_error = 'unbound_source_generation' AND
            source_stage IS NULL AND source_job_id IS NULL AND
            source_generation IS NULL
        )
    );

DROP INDEX IF EXISTS derived_verification_forward_blocks_claim_idx;
CREATE INDEX derived_verification_forward_blocks_claim_idx
    ON derived_verification_forward_blocks (status, updated_at, id)
    WHERE status IN ('queued', 'running');

CREATE FUNCTION enqueue_derived_forward_event_after_stage_publication()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.state = 'complete' AND
       ((NEW.stage = 'trace' AND NEW.stage_version = 3) OR
        (NEW.stage = 'proxy' AND NEW.stage_version = 2)) THEN
        INSERT INTO derived_verification_forward_blocks (
            chain_id, block_number, block_hash, source_stage,
            source_job_id, source_generation
        ) VALUES (
            NEW.chain_id, NEW.block_number, NEW.block_hash, NEW.stage,
            NEW.job_id, NEW.job_generation
        )
        ON CONFLICT (source_job_id, source_generation) DO NOTHING;
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER durable_stage_publications_derived_forward_enqueue
AFTER INSERT ON durable_stage_publications
FOR EACH ROW EXECUTE FUNCTION enqueue_derived_forward_event_after_stage_publication();

DO $migration$
DECLARE
    migration_schema TEXT := current_schema();
BEGIN
    IF migration_schema IS NULL THEN
        RAISE EXCEPTION 'derived queue hardening migration requires a current schema';
    END IF;
    EXECUTE format(
        'ALTER FUNCTION %I.enqueue_derived_forward_event_after_stage_publication() SET search_path = %I, pg_catalog',
        migration_schema,
        migration_schema
    );
END
$migration$;
