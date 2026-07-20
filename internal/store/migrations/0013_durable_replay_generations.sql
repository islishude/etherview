-- A canonical block hash can be detached and later become canonical again.
-- Keep one outbox identity, but advance a durable generation and make the row
-- pending again for every new canonical/orphan transition of that identity.
ALTER TABLE transactional_outbox
    ADD COLUMN IF NOT EXISTS generation BIGINT NOT NULL DEFAULT 1;

-- A replay request may race an owned lease. requested_generation advances
-- without changing that lease; leased_generation records what the owner is
-- processing, and completed_generation records the last consumed generation.
ALTER TABLE durable_jobs
    ADD COLUMN IF NOT EXISTS requested_generation BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS claimed_generation BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS leased_generation BIGINT,
    ADD COLUMN IF NOT EXISTS completed_generation BIGINT NOT NULL DEFAULT 0;

UPDATE durable_jobs
SET claimed_generation = requested_generation
WHERE attempts > 0 AND claimed_generation = 0;

UPDATE durable_jobs
SET leased_generation = requested_generation
WHERE status = 'leased' AND leased_generation IS NULL;

UPDATE durable_jobs
SET completed_generation = requested_generation
WHERE status IN ('succeeded', 'failed', 'cancelled')
  AND completed_generation = 0;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'transactional_outbox_generation_check'
          AND conrelid = 'transactional_outbox'::regclass
    ) THEN
        ALTER TABLE transactional_outbox
            ADD CONSTRAINT transactional_outbox_generation_check
            CHECK (generation > 0);
    END IF;
END
$$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'durable_jobs_generation_check'
          AND conrelid = 'durable_jobs'::regclass
    ) THEN
        ALTER TABLE durable_jobs
            ADD CONSTRAINT durable_jobs_generation_check
            CHECK (
                requested_generation > 0
                AND claimed_generation BETWEEN 0 AND requested_generation
                AND completed_generation BETWEEN 0 AND requested_generation
                AND (
                    leased_generation IS NULL
                    OR leased_generation BETWEEN 1 AND requested_generation
                )
            );
    END IF;
END
$$;

-- Source identities make a replay request idempotent across a processor crash:
-- re-running one source generation cannot keep bumping its dependent forever.
CREATE TABLE IF NOT EXISTS durable_job_replay_requests (
    job_id BIGINT NOT NULL REFERENCES durable_jobs(id) ON DELETE CASCADE,
    source_kind TEXT NOT NULL,
    source_key TEXT NOT NULL,
    requested_generation BIGINT NOT NULL,
    requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (job_id, source_kind, source_key),
    UNIQUE (job_id, requested_generation),
    CHECK (length(source_kind) BETWEEN 1 AND 64),
    CHECK (length(source_key) BETWEEN 1 AND 256),
    CHECK (requested_generation > 1)
);

CREATE INDEX IF NOT EXISTS durable_jobs_replay_pending_idx
    ON durable_jobs (available_at, id)
    WHERE requested_generation > completed_generation
      AND status IN ('queued', 'leased');
