-- Pagination snapshots bind not only the canonical block identity but also
-- every proxy@2 replay request/publication at or below that block. A replay
-- therefore invalidates an older cursor before it can mix generations.
CREATE TABLE proxy_history_epochs (
    epoch_id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    durable_job_id BIGINT NOT NULL REFERENCES durable_jobs(id),
    requested_generation BIGINT NOT NULL,
    phase TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (durable_job_id, requested_generation, phase),
    CHECK (block_number >= 0),
    CHECK (octet_length(block_hash) = 32),
    CHECK (requested_generation > 0),
    CHECK (phase IN ('requested', 'published'))
);

CREATE INDEX proxy_history_epochs_snapshot_idx
    ON proxy_history_epochs (chain_id, block_number, epoch_id DESC);

CREATE INDEX proxy_history_epochs_latest_idx
    ON proxy_history_epochs (chain_id, epoch_id DESC)
    INCLUDE (block_number, block_hash);

INSERT INTO proxy_history_epochs (
    chain_id, block_number, block_hash, durable_job_id,
    requested_generation, phase
)
SELECT job.chain_id, (job.payload->>'block_number')::numeric,
       decode(substr(job.payload->>'block_hash', 3), 'hex'),
       job.id, job.requested_generation, 'requested'
FROM durable_jobs AS job
WHERE job.kind = 'enrichment' AND job.stage = 'proxy'
  AND job.stage_version = 2
  AND job.payload->>'block_number' ~ '^[0-9]+$'
  AND job.payload->>'block_hash' ~ '^0x[0-9a-fA-F]{64}$'
ON CONFLICT (durable_job_id, requested_generation, phase) DO NOTHING;

INSERT INTO proxy_history_epochs (
    chain_id, block_number, block_hash, durable_job_id,
    requested_generation, phase
)
SELECT result.chain_id, result.block_number, result.block_hash,
       result.durable_job_id, result.job_generation, 'published'
FROM block_stage_results AS result
WHERE result.stage = 'proxy' AND result.stage_version = 2
  AND result.durable_job_id IS NOT NULL
  AND result.job_generation IS NOT NULL
ON CONFLICT (durable_job_id, requested_generation, phase) DO NOTHING;

CREATE OR REPLACE FUNCTION record_proxy_history_job_epoch()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
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
$$;

CREATE TRIGGER durable_jobs_proxy_history_epoch
AFTER INSERT OR UPDATE ON durable_jobs
FOR EACH ROW EXECUTE FUNCTION record_proxy_history_job_epoch();

CREATE OR REPLACE FUNCTION record_proxy_history_publication_epoch()
RETURNS TRIGGER
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

CREATE TRIGGER block_stage_results_proxy_history_epoch
AFTER INSERT OR UPDATE ON block_stage_results
FOR EACH ROW EXECUTE FUNCTION record_proxy_history_publication_epoch();

CREATE OR REPLACE FUNCTION reject_proxy_history_epoch_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'proxy history epochs are append-only';
END
$$;

CREATE TRIGGER proxy_history_epochs_append_only
BEFORE UPDATE OR DELETE ON proxy_history_epochs
FOR EACH ROW EXECUTE FUNCTION reject_proxy_history_epoch_mutation();
