-- Upgrade the derived-stage publication protocol while excluding concurrent
-- durable-job transitions for the duration of this migration transaction.
-- API/core readers may continue; enrichment queue writers wait at this table
-- lock and resume under the database guards installed below.
LOCK TABLE durable_jobs IN SHARE ROW EXCLUSIVE MODE;

-- Production stage rows are published only by the exact durable job
-- generation that completed them. NULL markers are retained solely for
-- direct processor fixtures and pre-publication legacy data.
ALTER TABLE block_stage_results
    ADD COLUMN IF NOT EXISTS durable_job_id BIGINT,
    ADD COLUMN IF NOT EXISTS job_generation BIGINT;

ALTER TABLE block_journals
    ADD COLUMN IF NOT EXISTS durable_job_id BIGINT,
    ADD COLUMN IF NOT EXISTS job_generation BIGINT;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'block_stage_results_durable_job_fkey'
          AND conrelid = 'block_stage_results'::regclass
    ) THEN
        ALTER TABLE block_stage_results
            ADD CONSTRAINT block_stage_results_durable_job_fkey
            FOREIGN KEY (durable_job_id) REFERENCES durable_jobs(id);
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'block_stage_results_publication_pair_check'
          AND conrelid = 'block_stage_results'::regclass
    ) THEN
        ALTER TABLE block_stage_results
            ADD CONSTRAINT block_stage_results_publication_pair_check
            CHECK (
                (durable_job_id IS NULL AND job_generation IS NULL)
                OR (
                    durable_job_id IS NOT NULL
                    AND job_generation IS NOT NULL
                    AND job_generation > 0
                )
            );
    END IF;
END
$$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'block_journals_durable_job_fkey'
          AND conrelid = 'block_journals'::regclass
    ) THEN
        ALTER TABLE block_journals
            ADD CONSTRAINT block_journals_durable_job_fkey
            FOREIGN KEY (durable_job_id) REFERENCES durable_jobs(id);
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'block_journals_publication_pair_check'
          AND conrelid = 'block_journals'::regclass
    ) THEN
        ALTER TABLE block_journals
            ADD CONSTRAINT block_journals_publication_pair_check
            CHECK (
                (durable_job_id IS NULL AND job_generation IS NULL)
                OR (
                    durable_job_id IS NOT NULL
                    AND job_generation IS NOT NULL
                    AND job_generation > 0
                )
            );
    END IF;
END
$$;

CREATE UNIQUE INDEX IF NOT EXISTS block_stage_results_job_generation_idx
    ON block_stage_results (durable_job_id, job_generation)
    WHERE durable_job_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS block_journals_job_generation_idx
    ON block_journals (durable_job_id, job_generation)
    WHERE durable_job_id IS NOT NULL;

-- This append-only ledger is the commit-confirmation witness. Unlike the
-- current stage marker, replay never removes a generation's ledger entry.
CREATE TABLE IF NOT EXISTS durable_stage_publications (
    job_id BIGINT NOT NULL REFERENCES durable_jobs(id),
    job_generation BIGINT NOT NULL,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    stage TEXT NOT NULL,
    stage_version INTEGER NOT NULL,
    state TEXT NOT NULL,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    last_error TEXT,
    committed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (job_id, job_generation),
    FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash),
    CHECK (job_generation > 0),
    CHECK (octet_length(block_hash) = 32),
    CHECK (stage_version > 0),
    CHECK (state IN ('complete', 'failed', 'unavailable', 'superseded')),
    CHECK ((state IN ('complete', 'superseded') AND last_error IS NULL)
        OR (state IN ('failed', 'unavailable') AND last_error IS NOT NULL))
);

CREATE OR REPLACE FUNCTION reject_durable_stage_publication_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'durable stage publication ledger is append-only'
        USING ERRCODE = 'integrity_constraint_violation';
END
$$;

DROP TRIGGER IF EXISTS durable_stage_publications_append_only
    ON durable_stage_publications;
CREATE TRIGGER durable_stage_publications_append_only
BEFORE UPDATE OR DELETE ON durable_stage_publications
FOR EACH ROW EXECUTE FUNCTION reject_durable_stage_publication_mutation();

-- Pre-0014 terminal rows have no proof that output and terminal completion
-- shared a transaction. Source-deduplicated replay advances each one exactly
-- once and removes only its legacy/current generation publication. Queued and
-- leased jobs are not stolen; an old leased owner is rejected at terminal
-- commit below and the new binary reclaims it after lease expiry.
DO $$
DECLARE
    target RECORD;
    next_generation BIGINT;
BEGIN
    FOR target IN
        SELECT job.id, job.chain_id, job.stage, job.stage_version,
               job.payload, job.requested_generation
        FROM durable_jobs AS job
        WHERE job.kind = 'enrichment'
          AND job.stage IN ('proxy', 'abi', 'token', 'stats', 'trace')
          AND job.status IN ('succeeded', 'failed')
        ORDER BY job.id
    LOOP
        PERFORM pg_advisory_xact_lock(-target.id);
        next_generation := NULL;
        INSERT INTO durable_job_replay_requests (
            job_id, source_kind, source_key, requested_generation
        ) VALUES (
            target.id, 'schema-upgrade',
            '0014-lease-fenced-stage-publication',
            target.requested_generation + 1
        )
        ON CONFLICT (job_id, source_kind, source_key) DO NOTHING
        RETURNING requested_generation INTO next_generation;

        IF next_generation IS NOT NULL THEN
            DELETE FROM block_stage_results AS result
            WHERE result.chain_id = target.chain_id
              AND target.payload->>'block_hash' ~ '^0x[0-9a-f]{64}$'
              AND result.block_hash = decode(substr(target.payload->>'block_hash', 3), 'hex')
              AND result.stage = target.stage
              AND result.stage_version = target.stage_version
              AND (
                  (result.durable_job_id IS NULL AND result.job_generation IS NULL)
                  OR (
                      result.durable_job_id = target.id
                      AND result.job_generation <= target.requested_generation
                  )
              );
            DELETE FROM block_journals AS journal
            WHERE journal.chain_id = target.chain_id
              AND target.payload->>'block_hash' ~ '^0x[0-9a-f]{64}$'
              AND journal.block_hash = decode(substr(target.payload->>'block_hash', 3), 'hex')
              AND journal.stage = target.stage || '@' || target.stage_version::text
              AND (
                  (journal.durable_job_id IS NULL AND journal.job_generation IS NULL)
                  OR (
                      journal.durable_job_id = target.id
                      AND journal.job_generation <= target.requested_generation
                  )
              );
            IF target.stage = 'abi'
               AND target.payload->>'block_number' ~ '^(0|[1-9][0-9]*)$'
               AND target.payload->>'block_hash' ~ '^0x[0-9a-f]{64}$' THEN
                DELETE FROM abi_decodings
                WHERE chain_id = target.chain_id
                  AND block_number = (target.payload->>'block_number')::numeric
                  AND block_hash = decode(substr(target.payload->>'block_hash', 3), 'hex');
                DELETE FROM contract_abis
                WHERE chain_id = target.chain_id
                  AND block_number = (target.payload->>'block_number')::numeric
                  AND block_hash = decode(substr(target.payload->>'block_hash', 3), 'hex');
            END IF;
            UPDATE durable_jobs
            SET requested_generation = next_generation,
                status = 'queued',
                attempts = 0,
                available_at = clock_timestamp(),
                leased_by = NULL,
                lease_token = NULL,
                lease_expires_at = NULL,
                leased_generation = NULL,
                result = NULL,
                last_error = NULL,
                updated_at = clock_timestamp()
            WHERE id = target.id
              AND status IN ('succeeded', 'failed')
              AND requested_generation = next_generation - 1;
        END IF;
    END LOOP;
END
$$;

-- Old binaries do not set this transaction-local protocol marker. They may
-- finish a lease already held during migration (the deferred terminal guard
-- rejects that old two-transaction completion), but cannot acquire/reacquire a
-- derived-stage lease after migration.
CREATE OR REPLACE FUNCTION require_enrichment_publication_protocol()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.kind = 'enrichment'
       AND NEW.stage IN ('proxy', 'abi', 'token', 'stats', 'trace')
       AND NEW.status = 'leased'
       AND (
           OLD.status <> 'leased'
           OR OLD.lease_token IS DISTINCT FROM NEW.lease_token
           OR OLD.leased_generation IS DISTINCT FROM NEW.leased_generation
       )
       AND COALESCE(current_setting('etherview.enrichment_publication_protocol', true), '') <> '2' THEN
        RAISE EXCEPTION 'derived enrichment lease requires publication protocol 2'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS durable_jobs_publication_protocol
    ON durable_jobs;
CREATE TRIGGER durable_jobs_publication_protocol
BEFORE UPDATE ON durable_jobs
FOR EACH ROW EXECUTE FUNCTION require_enrichment_publication_protocol();

-- Fail closed at COMMIT if any running old binary attempts its former
-- output-then-Finish protocol. The trigger queries the final row/marker state,
-- so statement order inside the new atomic transaction is irrelevant.
CREATE OR REPLACE FUNCTION enforce_enrichment_terminal_publication()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    current_job durable_jobs%ROWTYPE;
    exact_result block_stage_results%ROWTYPE;
BEGIN
    SELECT * INTO current_job FROM durable_jobs WHERE id = NEW.id;
    IF NOT FOUND
       OR current_job.kind <> 'enrichment'
       OR current_job.stage NOT IN ('proxy', 'abi', 'token', 'stats', 'trace')
       OR current_job.status NOT IN ('succeeded', 'failed') THEN
        RETURN NULL;
    END IF;
    IF COALESCE(current_setting('etherview.enrichment_publication_protocol', true), '') <> '2' THEN
        RAISE EXCEPTION 'derived enrichment terminal transition requires publication protocol 2'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF current_job.claimed_generation <> current_job.completed_generation
       OR current_job.requested_generation <> current_job.completed_generation
       OR current_job.leased_generation IS NOT NULL
       OR current_job.lease_token IS NOT NULL
       OR current_job.lease_expires_at IS NOT NULL
       OR current_job.result IS NULL THEN
        RAISE EXCEPTION 'derived enrichment terminal job lacks exact completed generation'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;

    SELECT result.* INTO exact_result
    FROM block_stage_results AS result
    WHERE result.durable_job_id = current_job.id
      AND result.job_generation = current_job.completed_generation
      AND result.chain_id = current_job.chain_id
      AND result.stage = current_job.stage
      AND result.stage_version = current_job.stage_version
      AND current_job.payload->>'block_hash' = '0x' || encode(result.block_hash, 'hex')
      AND current_job.payload->>'block_number' = result.block_number::text;
    IF NOT FOUND
       OR current_job.result->>'state' IS DISTINCT FROM exact_result.state
       OR COALESCE(current_job.result->'details', '{}'::jsonb) IS DISTINCT FROM exact_result.details
       OR (current_job.result->>'error') IS DISTINCT FROM exact_result.last_error THEN
        RAISE EXCEPTION 'derived enrichment terminal job lacks exact stage result'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM durable_stage_publications AS publication
        WHERE publication.job_id = current_job.id
          AND publication.job_generation = current_job.completed_generation
          AND publication.chain_id = current_job.chain_id
          AND publication.block_number = exact_result.block_number
          AND publication.block_hash = exact_result.block_hash
          AND publication.stage = current_job.stage
          AND publication.stage_version = current_job.stage_version
          AND publication.state = exact_result.state
          AND publication.details = exact_result.details
          AND publication.last_error IS NOT DISTINCT FROM exact_result.last_error
    ) THEN
        RAISE EXCEPTION 'derived enrichment terminal job lacks durable publication proof'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;

    IF current_job.status = 'succeeded' THEN
        IF exact_result.state <> 'complete'
           OR NOT EXISTS (
               SELECT 1 FROM block_journals AS journal
               WHERE journal.durable_job_id = current_job.id
                 AND journal.job_generation = current_job.completed_generation
                 AND journal.chain_id = current_job.chain_id
                 AND journal.block_hash = exact_result.block_hash
                 AND journal.stage = current_job.stage || '@' || current_job.stage_version::text
                 AND journal.sequence = 1
           ) THEN
            RAISE EXCEPTION 'successful derived enrichment job lacks exact journal'
                USING ERRCODE = 'integrity_constraint_violation';
        END IF;
    ELSIF exact_result.state NOT IN ('failed', 'unavailable')
          OR EXISTS (
              SELECT 1 FROM block_journals AS journal
              WHERE journal.durable_job_id = current_job.id
                AND journal.job_generation = current_job.completed_generation
          ) THEN
        RAISE EXCEPTION 'terminal derived enrichment failure has invalid result or journal'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NULL;
END
$$;

DROP TRIGGER IF EXISTS durable_jobs_terminal_publication
    ON durable_jobs;
CREATE CONSTRAINT TRIGGER durable_jobs_terminal_publication
AFTER INSERT OR UPDATE ON durable_jobs
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION enforce_enrichment_terminal_publication();

-- This is the only readiness/publication relation for enrichment stages.
-- Merely committing processor output is insufficient: the job must have
-- atomically published this exact result and still have no newer generation
-- requested. Comparing the stored job result also prevents a hand-written or
-- stale marker from impersonating durable completion.
CREATE OR REPLACE VIEW published_block_stage_results AS
SELECT result.*
FROM block_stage_results AS result
JOIN durable_jobs AS job
  ON job.id = result.durable_job_id
 AND job.chain_id = result.chain_id
 AND job.kind = 'enrichment'
 AND job.stage = result.stage
 AND job.stage_version = result.stage_version
 AND job.payload->>'block_hash' = '0x' || encode(result.block_hash, 'hex')
 AND job.payload->>'block_number' = result.block_number::text
WHERE result.durable_job_id IS NOT NULL
  AND result.job_generation IS NOT NULL
  AND job.status IN ('succeeded', 'failed')
  AND job.claimed_generation = result.job_generation
  AND job.completed_generation = result.job_generation
  AND job.requested_generation = result.job_generation
  AND job.leased_generation IS NULL
  AND job.leased_by IS NULL
  AND job.lease_token IS NULL
  AND job.lease_expires_at IS NULL
  AND job.result IS NOT NULL
  AND job.result->>'state' = result.state
  -- An orphan attempt that observed no canonical mapping is durable audit
  -- evidence, not publication readiness. The same hash may reattach before
  -- its canonical outbox generation is dispatched; keep it unavailable until
  -- that replay completes instead of exposing a complete empty result.
  AND COALESCE(result.details->>'outcome', '') <> 'stale_canonical_skipped'
  AND NOT EXISTS (
      SELECT 1
      FROM transactional_outbox AS pending_attach
      WHERE pending_attach.chain_id = result.chain_id
        AND pending_attach.topic = 'core.block.canonical'
        AND pending_attach.message_key = '0x' || encode(result.block_hash, 'hex')
        AND pending_attach.published_at IS NULL
  )
  AND (
      (
          job.status = 'succeeded'
          AND result.state = 'complete'
          AND EXISTS (
              SELECT 1
              FROM block_journals AS journal
              WHERE journal.chain_id = result.chain_id
                AND journal.block_hash = result.block_hash
                AND journal.stage = result.stage || '@' || result.stage_version::text
                AND journal.sequence = 1
                AND journal.durable_job_id = result.durable_job_id
                AND journal.job_generation = result.job_generation
          )
      )
      OR (job.status = 'failed' AND result.state IN ('failed', 'unavailable'))
  )
  AND COALESCE(job.result->'details', '{}'::jsonb) = result.details
  AND (job.result->>'error') IS NOT DISTINCT FROM result.last_error;

-- Trigger functions execute under the caller's session by default. Pin all
-- relation lookup to the schema that this migration installed plus pg_catalog
-- so a hostile or merely different runtime search_path cannot redirect them.
DO $$
DECLARE
    migration_schema TEXT := current_schema();
BEGIN
    EXECUTE format(
        'ALTER FUNCTION %I.reject_durable_stage_publication_mutation() SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
    EXECUTE format(
        'ALTER FUNCTION %I.require_enrichment_publication_protocol() SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
    EXECUTE format(
        'ALTER FUNCTION %I.enforce_enrichment_terminal_publication() SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
END
$$;
