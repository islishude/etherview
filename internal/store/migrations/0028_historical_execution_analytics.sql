-- P70-T16 upgrades block statistics to stats@3 and introduces additive,
-- reorg-aware hourly execution analytics projections.

ALTER TABLE block_statistics
    ADD COLUMN IF NOT EXISTS execution_gas_fee_wei NUMERIC(78, 0),
    ADD COLUMN IF NOT EXISTS priority_fee_wei NUMERIC(78, 0),
    ADD COLUMN IF NOT EXISTS failed_transaction_count BIGINT,
    ADD COLUMN IF NOT EXISTS contract_creation_count BIGINT;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'block_statistics_v3_execution_check'
          AND conrelid = 'block_statistics'::regclass
    ) THEN
        ALTER TABLE block_statistics
            ADD CONSTRAINT block_statistics_v3_execution_check CHECK (
                (execution_gas_fee_wei IS NULL OR execution_gas_fee_wei >= 0)
                AND (priority_fee_wei IS NULL OR priority_fee_wei >= 0)
                AND (failed_transaction_count IS NULL OR failed_transaction_count >= 0)
                AND (contract_creation_count IS NULL OR contract_creation_count >= 0)
                AND (
                    failed_transaction_count IS NULL
                    OR failed_transaction_count <= transaction_count
                )
                AND (
                    contract_creation_count IS NULL
                    OR contract_creation_count <= transaction_count
                )
            ) NOT VALID;
    END IF;
END
$$;

ALTER TABLE block_statistics
    VALIDATE CONSTRAINT block_statistics_v3_execution_check;

CREATE TABLE IF NOT EXISTS chart_hourly_rollups (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    bucket_start TIMESTAMPTZ NOT NULL,
    source_generation BIGINT NOT NULL,
    from_block NUMERIC(78, 0) NOT NULL,
    to_block NUMERIC(78, 0) NOT NULL,
    block_count BIGINT NOT NULL,
    transaction_count NUMERIC(78, 0) NOT NULL,
    failed_transaction_count NUMERIC(78, 0) NOT NULL,
    contract_creation_count NUMERIC(78, 0) NOT NULL,
    gas_used NUMERIC(78, 0) NOT NULL,
    gas_limit NUMERIC(78, 0) NOT NULL,
    block_interval_seconds NUMERIC(78, 0) NOT NULL,
    block_interval_samples BIGINT NOT NULL,
    base_fee_per_gas_sum NUMERIC(78, 0) NOT NULL,
    base_fee_samples BIGINT NOT NULL,
    execution_gas_fee_wei NUMERIC(78, 0) NOT NULL,
    priority_fee_wei NUMERIC(78, 0) NOT NULL,
    burned_wei NUMERIC(78, 0) NOT NULL,
    blob_gas_used NUMERIC(78, 0) NOT NULL,
    blob_base_fee_per_gas_sum NUMERIC(78, 0) NOT NULL,
    blob_base_fee_samples BIGINT NOT NULL,
    blob_burned_wei NUMERIC(78, 0) NOT NULL,
    erc20_transfer_count NUMERIC(78, 0) NOT NULL,
    nft_transfer_count NUMERIC(78, 0) NOT NULL,
    computed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, bucket_start),
    CHECK (date_trunc('hour', bucket_start, 'UTC') = bucket_start),
    CHECK (source_generation > 0),
    CHECK (from_block >= 0 AND to_block >= from_block),
    CHECK (
        block_count > 0
        AND transaction_count >= 0
        AND failed_transaction_count >= 0
        AND contract_creation_count >= 0
        AND gas_used >= 0
        AND gas_limit >= 0
        AND block_interval_seconds >= 0
        AND block_interval_samples >= 0
        AND base_fee_per_gas_sum >= 0
        AND base_fee_samples >= 0
        AND execution_gas_fee_wei >= 0
        AND priority_fee_wei >= 0
        AND burned_wei >= 0
        AND blob_gas_used >= 0
        AND blob_base_fee_per_gas_sum >= 0
        AND blob_base_fee_samples >= 0
        AND blob_burned_wei >= 0
        AND erc20_transfer_count >= 0
        AND nft_transfer_count >= 0
    )
);

CREATE INDEX IF NOT EXISTS chart_hourly_rollups_range_idx
    ON chart_hourly_rollups (chain_id, bucket_start DESC);

CREATE TABLE IF NOT EXISTS chart_rollup_dirty_hours (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    bucket_start TIMESTAMPTZ NOT NULL,
    generation BIGINT NOT NULL DEFAULT 1,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    dirtied_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, bucket_start),
    CHECK (date_trunc('hour', bucket_start, 'UTC') = bucket_start),
    CHECK (generation > 0 AND attempts >= 0)
);

CREATE INDEX IF NOT EXISTS chart_rollup_dirty_recent_idx
    ON chart_rollup_dirty_hours (chain_id, bucket_start DESC);

CREATE TABLE IF NOT EXISTS chart_rollup_backfill (
    chain_id NUMERIC(78, 0) PRIMARY KEY REFERENCES chains(chain_id),
    available_from TIMESTAMPTZ,
    available_to TIMESTAMPTZ,
    next_block NUMERIC(78, 0),
    target_start_block NUMERIC(78, 0),
    completed_blocks NUMERIC(78, 0) NOT NULL DEFAULT 0,
    total_blocks NUMERIC(78, 0) NOT NULL DEFAULT 0,
    complete BOOLEAN NOT NULL DEFAULT false,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((available_from IS NULL) = (available_to IS NULL)),
    CHECK (available_to IS NULL OR available_to >= available_from),
    CHECK (next_block IS NULL OR next_block >= 0),
    CHECK (target_start_block IS NULL OR target_start_block >= 0),
    CHECK (completed_blocks >= 0 AND total_blocks >= completed_blocks)
);

CREATE OR REPLACE FUNCTION mark_chart_rollup_dirty()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    source_chain NUMERIC(78, 0);
    source_number NUMERIC(78, 0);
    source_hash BYTEA;
    source_timestamp NUMERIC(78, 0);
    hour_start TIMESTAMPTZ;
BEGIN
    IF TG_OP = 'DELETE' THEN
        source_chain := OLD.chain_id;
        source_number := OLD.block_number;
        source_hash := OLD.block_hash;
    ELSE
        source_chain := NEW.chain_id;
        source_number := NEW.block_number;
        source_hash := NEW.block_hash;
    END IF;
    SELECT block.timestamp INTO source_timestamp
    FROM blocks AS block
    WHERE block.chain_id = source_chain
      AND block.number = source_number
      AND block.hash = source_hash;
    IF source_timestamp IS NULL OR source_timestamp > 253402300799 THEN
        IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
        RETURN NEW;
    END IF;
    hour_start := date_trunc('hour', to_timestamp(source_timestamp::double precision), 'UTC');
    INSERT INTO chart_rollup_dirty_hours (chain_id, bucket_start)
    VALUES (source_chain, hour_start)
    ON CONFLICT (chain_id, bucket_start) DO UPDATE SET
        generation = chart_rollup_dirty_hours.generation + 1,
        attempts = 0,
        next_attempt_at = now(),
        dirtied_at = now();
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS block_statistics_chart_dirty_trigger ON block_statistics;
CREATE TRIGGER block_statistics_chart_dirty_trigger
AFTER INSERT OR UPDATE OR DELETE ON block_statistics
FOR EACH ROW EXECUTE FUNCTION mark_chart_rollup_dirty();

DROP TRIGGER IF EXISTS token_events_chart_dirty_trigger ON token_events;
CREATE TRIGGER token_events_chart_dirty_trigger
AFTER INSERT OR UPDATE OR DELETE ON token_events
FOR EACH ROW EXECUTE FUNCTION mark_chart_rollup_dirty();

CREATE OR REPLACE FUNCTION mark_canonical_chart_rollup_dirty()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    source_chain NUMERIC(78, 0);
    source_number NUMERIC(78, 0);
    source_hash BYTEA;
    source_timestamp NUMERIC(78, 0);
    hour_start TIMESTAMPTZ;
BEGIN
    IF TG_OP = 'DELETE' THEN
        source_chain := OLD.chain_id;
        source_number := OLD.number;
        source_hash := OLD.block_hash;
    ELSE
        source_chain := NEW.chain_id;
        source_number := NEW.number;
        source_hash := NEW.block_hash;
    END IF;
    SELECT block.timestamp INTO source_timestamp
    FROM blocks AS block
    WHERE block.chain_id = source_chain
      AND block.number = source_number
      AND block.hash = source_hash;
    IF source_timestamp IS NULL OR source_timestamp > 253402300799 THEN
        IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
        RETURN NEW;
    END IF;
    hour_start := date_trunc('hour', to_timestamp(source_timestamp::double precision), 'UTC');
    INSERT INTO chart_rollup_dirty_hours (chain_id, bucket_start)
    VALUES (source_chain, hour_start)
    ON CONFLICT (chain_id, bucket_start) DO UPDATE SET
        generation = chart_rollup_dirty_hours.generation + 1,
        attempts = 0,
        next_attempt_at = now(),
        dirtied_at = now();
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS canonical_blocks_chart_dirty_trigger ON canonical_blocks;
CREATE TRIGGER canonical_blocks_chart_dirty_trigger
AFTER INSERT OR UPDATE OR DELETE ON canonical_blocks
FOR EACH ROW EXECUTE FUNCTION mark_canonical_chart_rollup_dirty();

DROP TRIGGER IF EXISTS block_stage_results_chart_dirty_insert_trigger ON block_stage_results;
CREATE TRIGGER block_stage_results_chart_dirty_insert_trigger
AFTER INSERT ON block_stage_results
FOR EACH ROW
WHEN (
    (NEW.stage = 'stats' AND NEW.stage_version = 3)
    OR (NEW.stage = 'token' AND NEW.stage_version = 1)
)
EXECUTE FUNCTION mark_chart_rollup_dirty();

DROP TRIGGER IF EXISTS block_stage_results_chart_dirty_update_trigger ON block_stage_results;
CREATE TRIGGER block_stage_results_chart_dirty_update_trigger
AFTER UPDATE ON block_stage_results
FOR EACH ROW
WHEN (
    (NEW.stage = 'stats' AND NEW.stage_version = 3)
    OR (NEW.stage = 'token' AND NEW.stage_version = 1)
    OR (OLD.stage = 'stats' AND OLD.stage_version = 3)
    OR (OLD.stage = 'token' AND OLD.stage_version = 1)
)
EXECUTE FUNCTION mark_chart_rollup_dirty();

DROP TRIGGER IF EXISTS block_stage_results_chart_dirty_delete_trigger ON block_stage_results;
CREATE TRIGGER block_stage_results_chart_dirty_delete_trigger
AFTER DELETE ON block_stage_results
FOR EACH ROW
WHEN (
    (OLD.stage = 'stats' AND OLD.stage_version = 3)
    OR (OLD.stage = 'token' AND OLD.stage_version = 1)
)
EXECUTE FUNCTION mark_chart_rollup_dirty();

CREATE OR REPLACE FUNCTION mark_chart_rollup_dirty_from_job()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    source_number NUMERIC(78, 0);
    source_hash BYTEA;
BEGIN
    IF NEW.kind <> 'enrichment'
       OR NOT (
           (NEW.stage = 'stats' AND NEW.stage_version = 3)
           OR (NEW.stage = 'token' AND NEW.stage_version = 1)
       )
       OR NEW.payload->>'block_number' !~ '^(0|[1-9][0-9]*)$'
       OR NEW.payload->>'block_hash' !~ '^0x[0-9a-f]{64}$' THEN
        RETURN NEW;
    END IF;
    source_number := (NEW.payload->>'block_number')::numeric;
    source_hash := decode(substr(NEW.payload->>'block_hash', 3), 'hex');
    PERFORM mark_chart_rollup_dirty_for_block(NEW.chain_id, source_number, source_hash);
    RETURN NEW;
END
$$;

CREATE OR REPLACE FUNCTION mark_chart_rollup_dirty_for_block(
    source_chain NUMERIC(78, 0),
    source_number NUMERIC(78, 0),
    source_hash BYTEA
)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    source_timestamp NUMERIC(78, 0);
    hour_start TIMESTAMPTZ;
BEGIN
    SELECT block.timestamp INTO source_timestamp
    FROM blocks AS block
    WHERE block.chain_id = source_chain
      AND block.number = source_number
      AND block.hash = source_hash;
    IF source_timestamp IS NULL OR source_timestamp > 253402300799 THEN
        RETURN;
    END IF;
    hour_start := date_trunc('hour', to_timestamp(source_timestamp::double precision), 'UTC');
    INSERT INTO chart_rollup_dirty_hours (chain_id, bucket_start)
    VALUES (source_chain, hour_start)
    ON CONFLICT (chain_id, bucket_start) DO UPDATE SET
        generation = chart_rollup_dirty_hours.generation + 1,
        attempts = 0,
        next_attempt_at = now(),
        dirtied_at = now();
END
$$;

DROP TRIGGER IF EXISTS durable_jobs_chart_dirty_trigger ON durable_jobs;
CREATE TRIGGER durable_jobs_chart_dirty_trigger
AFTER INSERT OR UPDATE OF status, requested_generation, claimed_generation,
    completed_generation, leased_generation, result ON durable_jobs
FOR EACH ROW EXECUTE FUNCTION mark_chart_rollup_dirty_from_job();
