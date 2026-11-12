-- Fresh-database baseline: transfers statistics.

-- Functions

CREATE FUNCTION mark_canonical_chart_rollup_dirty() RETURNS trigger
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
    -- Appended blocks leave the last completed prefix readable. Changes within
    -- that prefix withdraw it atomically; the worker will publish its replacement.
    DELETE FROM chart_hourly_rollups
    WHERE chain_id = source_chain AND bucket_start = hour_start
      AND to_block >= source_number;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END
$$;

CREATE FUNCTION mark_chart_rollup_dirty() RETURNS trigger
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
    -- Appended blocks leave the last completed prefix readable. Changes within
    -- that prefix withdraw it atomically; the worker will publish its replacement.
    DELETE FROM chart_hourly_rollups
    WHERE chain_id = source_chain AND bucket_start = hour_start
      AND to_block >= source_number;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END
$$;

CREATE FUNCTION mark_chart_rollup_dirty_for_block(source_chain numeric, source_number numeric, source_hash bytea) RETURNS void
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
    -- Appended blocks leave the last completed prefix readable. Changes within
    -- that prefix withdraw it atomically; the worker will publish its replacement.
    DELETE FROM chart_hourly_rollups
    WHERE chain_id = source_chain AND bucket_start = hour_start
      AND to_block >= source_number;
END
$$;

CREATE FUNCTION mark_chart_rollup_dirty_from_job() RETURNS trigger
    LANGUAGE plpgsql
    AS $_$
DECLARE
    source_number NUMERIC(78, 0);
    source_hash BYTEA;
BEGIN
    IF NEW.kind <> 'enrichment'
       OR NOT (
           (NEW.stage = 'stats' AND NEW.stage_version = 4)
           OR (NEW.stage = 'token' AND NEW.stage_version = 2)
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
$_$;

CREATE FUNCTION native_transfer_canonical_coverage() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP='DELETE' THEN
 PERFORM refresh_native_transfer_coverage(OLD.chain_id,OLD.number); RETURN OLD;
 END IF;
 PERFORM refresh_native_transfer_coverage(NEW.chain_id,NEW.number); RETURN NEW;
END $$;

CREATE FUNCTION native_transfer_job_coverage() RETURNS trigger
    LANGUAGE plpgsql
    AS $_$
BEGIN
 -- Heartbeats run while holding the lease's in-process guard. They must not
 -- wait for the coverage lock held by a publisher before its final guarded
 -- CAS. Only changes to the publication view's job inputs can affect coverage;
 -- lease expiry matters by nullness, not by the renewed timestamp value.
 IF TG_OP='UPDATE' AND ROW(
  NEW.kind,NEW.chain_id,NEW.stage,NEW.stage_version,NEW.payload,NEW.status,
  NEW.requested_generation,NEW.claimed_generation,NEW.completed_generation,
  NEW.leased_generation,NEW.leased_by,NEW.lease_token,
  NEW.lease_expires_at IS NULL,NEW.result
 ) IS NOT DISTINCT FROM ROW(
  OLD.kind,OLD.chain_id,OLD.stage,OLD.stage_version,OLD.payload,OLD.status,
  OLD.requested_generation,OLD.claimed_generation,OLD.completed_generation,
  OLD.leased_generation,OLD.leased_by,OLD.lease_token,
  OLD.lease_expires_at IS NULL,OLD.result
 ) THEN RETURN NEW; END IF;
 IF NEW.kind='enrichment' AND NEW.stage='native_transfer' AND NEW.stage_version=1
 AND NEW.payload->>'block_number' ~ '^(0|[1-9][0-9]*)$' THEN
 PERFORM refresh_native_transfer_coverage(NEW.chain_id,(NEW.payload->>'block_number')::numeric);
 END IF;
 RETURN NEW;
END $_$;

CREATE FUNCTION native_transfer_outbox_coverage() RETURNS trigger
    LANGUAGE plpgsql
    AS $_$
DECLARE number NUMERIC; chain NUMERIC; key TEXT;
BEGIN
 IF TG_OP='DELETE' THEN chain:=OLD.chain_id; key:=OLD.message_key;
 ELSE chain:=NEW.chain_id; key:=NEW.message_key; END IF;
 IF key !~ '^0x[0-9a-f]{64}$' THEN
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
 END IF;
 SELECT c.number INTO number FROM canonical_blocks c
 WHERE c.chain_id=chain AND c.block_hash=decode(substr(key,3),'hex');
 IF number IS NOT NULL THEN PERFORM refresh_native_transfer_coverage(chain,number); END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $_$;

CREATE FUNCTION native_transfer_result_coverage() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.stage='native_transfer' THEN PERFORM refresh_native_transfer_coverage(OLD.chain_id,OLD.block_number); END IF;
  RETURN OLD;
 END IF;
 IF NEW.stage='native_transfer' THEN PERFORM refresh_native_transfer_coverage(NEW.chain_id,NEW.block_number); END IF;
 RETURN NEW;
END $$;

CREATE FUNCTION refresh_native_transfer_coverage(p_chain numeric, p_number numeric) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE segment NUMMULTIRANGE; ready BOOLEAN;
BEGIN
 INSERT INTO native_transfer_coverage(chain_id) VALUES(p_chain) ON CONFLICT DO NOTHING;
 PERFORM 1 FROM native_transfer_coverage WHERE chain_id=p_chain FOR UPDATE;
 segment := nummultirange(numrange(p_number,p_number+1,'[)'));
 SELECT EXISTS(SELECT 1 FROM canonical_blocks c JOIN published_block_stage_results s
 ON s.chain_id=c.chain_id AND s.block_hash=c.block_hash AND s.block_number=c.number
 WHERE c.chain_id=p_chain AND c.number=p_number AND s.stage='native_transfer'
 AND s.stage_version=1 AND s.state='complete') INTO ready;
 UPDATE native_transfer_coverage SET covered=CASE WHEN ready THEN covered+segment ELSE covered-segment END WHERE chain_id=p_chain;
END $$;


-- Tables

CREATE TABLE block_statistics (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    transaction_count bigint NOT NULL,
    gas_used numeric(78,0) NOT NULL,
    gas_limit numeric(78,0) NOT NULL,
    base_fee_per_gas numeric(78,0),
    blob_gas_used numeric(78,0),
    burned_wei numeric(78,0),
    canonical boolean NOT NULL,
    computed_at timestamp with time zone DEFAULT now() NOT NULL,
    block_timestamp numeric(78,0),
    block_interval_seconds numeric(78,0),
    transactions_per_second numeric(78,18),
    excess_blob_gas numeric(78,0),
    blob_base_fee_per_gas numeric(78,0),
    blob_burned_wei numeric(78,0),
    execution_gas_fee_wei numeric(78,0),
    priority_fee_wei numeric(78,0),
    failed_transaction_count bigint,
    contract_creation_count bigint,
    CONSTRAINT block_statistics_base_fee_per_gas_check CHECK (((base_fee_per_gas IS NULL) OR (base_fee_per_gas >= (0)::numeric))),
    CONSTRAINT block_statistics_blob_gas_used_check CHECK (((blob_gas_used IS NULL) OR (blob_gas_used >= (0)::numeric))),
    CONSTRAINT block_statistics_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT block_statistics_burned_wei_check CHECK (((burned_wei IS NULL) OR (burned_wei >= (0)::numeric))),
    CONSTRAINT block_statistics_check CHECK (((gas_used >= (0)::numeric) AND (gas_limit >= (0)::numeric))),
    CONSTRAINT block_statistics_transaction_count_check CHECK ((transaction_count >= 0)),
    CONSTRAINT block_statistics_v2_blob_check CHECK ((((excess_blob_gas IS NULL) OR (excess_blob_gas >= (0)::numeric)) AND ((blob_base_fee_per_gas IS NULL) OR (blob_base_fee_per_gas >= (0)::numeric)) AND ((blob_burned_wei IS NULL) OR (blob_burned_wei >= (0)::numeric)))),
    CONSTRAINT block_statistics_v2_interval_check CHECK (((block_interval_seconds IS NULL) OR (block_interval_seconds > (0)::numeric))),
    CONSTRAINT block_statistics_v2_nonnegative_check CHECK (((block_timestamp IS NULL) OR (block_timestamp >= (0)::numeric))),
    CONSTRAINT block_statistics_v2_tps_check CHECK (((transactions_per_second IS NULL) OR (transactions_per_second >= (0)::numeric))),
    CONSTRAINT block_statistics_v3_execution_check CHECK ((((execution_gas_fee_wei IS NULL) OR (execution_gas_fee_wei >= (0)::numeric)) AND ((priority_fee_wei IS NULL) OR (priority_fee_wei >= (0)::numeric)) AND ((failed_transaction_count IS NULL) OR (failed_transaction_count >= 0)) AND ((contract_creation_count IS NULL) OR (contract_creation_count >= 0)) AND ((failed_transaction_count IS NULL) OR (failed_transaction_count <= transaction_count)) AND ((contract_creation_count IS NULL) OR (contract_creation_count <= transaction_count)))),
    CONSTRAINT block_statistics_pkey PRIMARY KEY (chain_id, block_number, block_hash),
    CONSTRAINT block_statistics_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash)
);

CREATE TABLE chart_hourly_rollups (
    chain_id numeric(78,0) NOT NULL,
    bucket_start timestamp with time zone NOT NULL,
    source_generation bigint NOT NULL,
    from_block numeric(78,0) NOT NULL,
    to_block numeric(78,0) NOT NULL,
    block_count bigint NOT NULL,
    transaction_count numeric(78,0) NOT NULL,
    failed_transaction_count numeric(78,0) NOT NULL,
    contract_creation_count numeric(78,0) NOT NULL,
    gas_used numeric(78,0) NOT NULL,
    gas_limit numeric(78,0) NOT NULL,
    block_interval_seconds numeric(78,0) NOT NULL,
    block_interval_samples bigint NOT NULL,
    base_fee_per_gas_sum numeric(78,0) NOT NULL,
    base_fee_samples bigint NOT NULL,
    execution_gas_fee_wei numeric(78,0) NOT NULL,
    priority_fee_wei numeric(78,0) NOT NULL,
    burned_wei numeric(78,0) NOT NULL,
    blob_gas_used numeric(78,0) NOT NULL,
    blob_base_fee_per_gas_sum numeric(78,0) NOT NULL,
    blob_base_fee_samples bigint NOT NULL,
    blob_burned_wei numeric(78,0) NOT NULL,
    erc20_transfer_count numeric(78,0) NOT NULL,
    nft_transfer_count numeric(78,0) NOT NULL,
    computed_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT chart_hourly_rollups_bucket_start_check CHECK ((date_trunc('hour'::text, bucket_start, 'UTC'::text) = bucket_start)),
    CONSTRAINT chart_hourly_rollups_check CHECK (((from_block >= (0)::numeric) AND (to_block >= from_block))),
    CONSTRAINT chart_hourly_rollups_check1 CHECK (((block_count > 0) AND (transaction_count >= (0)::numeric) AND (failed_transaction_count >= (0)::numeric) AND (contract_creation_count >= (0)::numeric) AND (gas_used >= (0)::numeric) AND (gas_limit >= (0)::numeric) AND (block_interval_seconds >= (0)::numeric) AND (block_interval_samples >= 0) AND (base_fee_per_gas_sum >= (0)::numeric) AND (base_fee_samples >= 0) AND (execution_gas_fee_wei >= (0)::numeric) AND (priority_fee_wei >= (0)::numeric) AND (burned_wei >= (0)::numeric) AND (blob_gas_used >= (0)::numeric) AND (blob_base_fee_per_gas_sum >= (0)::numeric) AND (blob_base_fee_samples >= 0) AND (blob_burned_wei >= (0)::numeric) AND (erc20_transfer_count >= (0)::numeric) AND (nft_transfer_count >= (0)::numeric))),
    CONSTRAINT chart_hourly_rollups_source_generation_check CHECK ((source_generation > 0)),
    CONSTRAINT chart_hourly_rollups_pkey PRIMARY KEY (chain_id, bucket_start),
    CONSTRAINT chart_hourly_rollups_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE chart_rollup_backfill (
    chain_id numeric(78,0) NOT NULL,
    available_from timestamp with time zone,
    available_to timestamp with time zone,
    next_block numeric(78,0),
    target_start_block numeric(78,0),
    completed_blocks numeric(78,0) DEFAULT 0 NOT NULL,
    total_blocks numeric(78,0) DEFAULT 0 NOT NULL,
    complete boolean DEFAULT false NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT chart_rollup_backfill_check CHECK (((available_from IS NULL) = (available_to IS NULL))),
    CONSTRAINT chart_rollup_backfill_check1 CHECK (((available_to IS NULL) OR (available_to >= available_from))),
    CONSTRAINT chart_rollup_backfill_check2 CHECK (((completed_blocks >= (0)::numeric) AND (total_blocks >= completed_blocks))),
    CONSTRAINT chart_rollup_backfill_next_block_check CHECK (((next_block IS NULL) OR (next_block >= (0)::numeric))),
    CONSTRAINT chart_rollup_backfill_target_start_block_check CHECK (((target_start_block IS NULL) OR (target_start_block >= (0)::numeric))),
    CONSTRAINT chart_rollup_backfill_pkey PRIMARY KEY (chain_id),
    CONSTRAINT chart_rollup_backfill_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE chart_rollup_dirty_hours (
    chain_id numeric(78,0) NOT NULL,
    bucket_start timestamp with time zone NOT NULL,
    generation bigint DEFAULT 1 NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    next_attempt_at timestamp with time zone DEFAULT now() NOT NULL,
    dirtied_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT chart_rollup_dirty_hours_bucket_start_check CHECK ((date_trunc('hour'::text, bucket_start, 'UTC'::text) = bucket_start)),
    CONSTRAINT chart_rollup_dirty_hours_check CHECK (((generation > 0) AND (attempts >= 0))),
    CONSTRAINT chart_rollup_dirty_hours_pkey PRIMARY KEY (chain_id, bucket_start),
    CONSTRAINT chart_rollup_dirty_hours_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE native_transfer_coverage (
    chain_id numeric(78,0) NOT NULL,
    covered nummultirange DEFAULT '{}'::nummultirange NOT NULL,
    CONSTRAINT native_transfer_coverage_pkey PRIMARY KEY (chain_id),
    CONSTRAINT native_transfer_coverage_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE native_transfers (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    transaction_hash bytea NOT NULL,
    transaction_index bigint NOT NULL,
    log_index bigint NOT NULL,
    from_address bytea NOT NULL,
    to_address bytea NOT NULL,
    amount numeric(78,0) NOT NULL,
    canonical boolean NOT NULL,
    CONSTRAINT native_transfers_amount_check CHECK (((amount > (0)::numeric) AND (amount < '115792089237316195423570985008687907853269984665640564039457584007913129639936'::numeric))),
    CONSTRAINT native_transfers_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT native_transfers_check CHECK ((from_address <> to_address)),
    CONSTRAINT native_transfers_from_address_check CHECK ((octet_length(from_address) = 20)),
    CONSTRAINT native_transfers_log_index_check CHECK ((log_index >= 0)),
    CONSTRAINT native_transfers_to_address_check CHECK ((octet_length(to_address) = 20)),
    CONSTRAINT native_transfers_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT native_transfers_transaction_index_check CHECK ((transaction_index >= 0)),
    CONSTRAINT native_transfers_pkey PRIMARY KEY (chain_id, block_number, block_hash, log_index),
    CONSTRAINT native_transfers_chain_id_block_number_block_hash_transact_fkey FOREIGN KEY (chain_id, block_number, block_hash, transaction_index, transaction_hash) REFERENCES transaction_inclusions(chain_id, block_number, block_hash, tx_index, tx_hash),
    CONSTRAINT native_transfers_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
)
PARTITION BY RANGE (block_number);

CREATE TABLE native_transfers_default PARTITION OF native_transfers DEFAULT;

CREATE TABLE native_transfers_p_0_1000000 PARTITION OF native_transfers FOR VALUES FROM ('0') TO ('1000000');


-- Indexes

CREATE INDEX native_transfers_default_chain_id_from_address_block_number_idx ON native_transfers_default USING btree (chain_id, from_address, block_number DESC, transaction_index DESC, log_index DESC) WHERE canonical;

CREATE INDEX native_transfers_default_chain_id_to_address_block_number_t_idx ON native_transfers_default USING btree (chain_id, to_address, block_number DESC, transaction_index DESC, log_index DESC) WHERE canonical;

CREATE INDEX native_transfers_default_chain_id_transaction_hash_log_inde_idx ON native_transfers_default USING btree (chain_id, transaction_hash, log_index) WHERE canonical;

CREATE INDEX native_transfers_p_0_1000000_chain_id_from_address_block_nu_idx ON native_transfers_p_0_1000000 USING btree (chain_id, from_address, block_number DESC, transaction_index DESC, log_index DESC) WHERE canonical;

CREATE INDEX native_transfers_p_0_1000000_chain_id_to_address_block_numb_idx ON native_transfers_p_0_1000000 USING btree (chain_id, to_address, block_number DESC, transaction_index DESC, log_index DESC) WHERE canonical;

CREATE INDEX native_transfers_p_0_1000000_chain_id_transaction_hash_log__idx ON native_transfers_p_0_1000000 USING btree (chain_id, transaction_hash, log_index) WHERE canonical;

CREATE INDEX chart_hourly_rollups_range_idx ON chart_hourly_rollups USING btree (chain_id, bucket_start DESC);

CREATE INDEX chart_rollup_dirty_recent_idx ON chart_rollup_dirty_hours USING btree (chain_id, bucket_start DESC);

CREATE INDEX native_transfers_from_idx ON native_transfers USING btree (chain_id, from_address, block_number DESC, transaction_index DESC, log_index DESC) WHERE canonical;

CREATE INDEX native_transfers_to_idx ON native_transfers USING btree (chain_id, to_address, block_number DESC, transaction_index DESC, log_index DESC) WHERE canonical;

CREATE INDEX native_transfers_transaction_idx ON native_transfers USING btree (chain_id, transaction_hash, log_index) WHERE canonical;


-- Triggers

CREATE TRIGGER block_stage_results_chart_dirty_delete_trigger AFTER DELETE ON block_stage_results FOR EACH ROW WHEN ((((old.stage = 'stats'::text) AND (old.stage_version = 4)) OR ((old.stage = 'token'::text) AND (old.stage_version = 2)))) EXECUTE FUNCTION mark_chart_rollup_dirty();

CREATE TRIGGER block_stage_results_chart_dirty_insert_trigger AFTER INSERT ON block_stage_results FOR EACH ROW WHEN ((((new.stage = 'stats'::text) AND (new.stage_version = 4)) OR ((new.stage = 'token'::text) AND (new.stage_version = 2)))) EXECUTE FUNCTION mark_chart_rollup_dirty();

CREATE TRIGGER block_stage_results_chart_dirty_update_trigger AFTER UPDATE ON block_stage_results FOR EACH ROW WHEN ((((new.stage = 'stats'::text) AND (new.stage_version = 4)) OR ((new.stage = 'token'::text) AND (new.stage_version = 2)) OR ((old.stage = 'stats'::text) AND (old.stage_version = 4)) OR ((old.stage = 'token'::text) AND (old.stage_version = 2)))) EXECUTE FUNCTION mark_chart_rollup_dirty();

CREATE TRIGGER block_statistics_chart_dirty_trigger AFTER INSERT OR DELETE OR UPDATE ON block_statistics FOR EACH ROW EXECUTE FUNCTION mark_chart_rollup_dirty();

CREATE TRIGGER canonical_blocks_chart_dirty_trigger AFTER INSERT OR DELETE OR UPDATE ON canonical_blocks FOR EACH ROW EXECUTE FUNCTION mark_canonical_chart_rollup_dirty();

CREATE TRIGGER durable_jobs_chart_dirty_trigger AFTER INSERT OR UPDATE OF status, requested_generation, claimed_generation, completed_generation, leased_generation, result ON durable_jobs FOR EACH ROW EXECUTE FUNCTION mark_chart_rollup_dirty_from_job();

CREATE TRIGGER native_transfer_canonical_coverage_trigger AFTER INSERT OR DELETE OR UPDATE ON canonical_blocks FOR EACH ROW EXECUTE FUNCTION native_transfer_canonical_coverage();

CREATE TRIGGER native_transfer_job_coverage_trigger AFTER INSERT OR UPDATE ON durable_jobs FOR EACH ROW WHEN ((new.stage = 'native_transfer'::text)) EXECUTE FUNCTION native_transfer_job_coverage();

CREATE TRIGGER native_transfer_outbox_coverage_trigger AFTER INSERT OR DELETE OR UPDATE ON transactional_outbox FOR EACH ROW EXECUTE FUNCTION native_transfer_outbox_coverage();

CREATE TRIGGER native_transfer_result_coverage_trigger AFTER INSERT OR DELETE OR UPDATE ON block_stage_results FOR EACH ROW EXECUTE FUNCTION native_transfer_result_coverage();

CREATE TRIGGER token_events_chart_dirty_trigger AFTER INSERT OR DELETE OR UPDATE ON token_events FOR EACH ROW EXECUTE FUNCTION mark_chart_rollup_dirty();

-- Incremental canonical source completeness. Raw rows never prove readiness;
-- each refresh reads the exact published stage identities at transaction end.
CREATE TABLE chart_source_blocks (
    chain_id numeric(78,0) NOT NULL REFERENCES chains(chain_id),
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL CHECK (octet_length(block_hash)=32),
    bucket_start timestamptz,
    ready boolean NOT NULL,
    PRIMARY KEY(chain_id,block_number)
);
CREATE INDEX chart_source_hour_idx ON chart_source_blocks(chain_id,bucket_start,block_number);
CREATE INDEX chart_source_pending_idx ON chart_source_blocks(chain_id,block_number) WHERE NOT ready;
CREATE INDEX chart_source_hour_pending_idx ON chart_source_blocks(chain_id,bucket_start,block_number) WHERE NOT ready;
CREATE TABLE chart_source_totals (
    chain_id numeric(78,0) PRIMARY KEY REFERENCES chains(chain_id),
    total_blocks numeric(78,0) NOT NULL DEFAULT 0,
    completed_blocks numeric(78,0) NOT NULL DEFAULT 0,
    generation bigint NOT NULL DEFAULT 1,
    CHECK (total_blocks >= completed_blocks AND completed_blocks >= 0)
);
CREATE TABLE chart_source_hours (
    chain_id numeric(78,0) NOT NULL REFERENCES chains(chain_id),
    bucket_start timestamptz NOT NULL,
    block_count bigint NOT NULL,
    ready_count bigint NOT NULL,
    first_block numeric(78,0),
    last_block numeric(78,0),
    first_pending numeric(78,0),
    PRIMARY KEY(chain_id,bucket_start),
    CHECK (block_count >= ready_count AND ready_count >= 0)
);
CREATE TABLE chart_dirty_totals (
    chain_id numeric(78,0) PRIMARY KEY REFERENCES chains(chain_id),
    dirty_hours bigint NOT NULL DEFAULT 0 CHECK (dirty_hours >= 0)
);
CREATE INDEX chart_dirty_oldest_idx ON chart_rollup_dirty_hours(chain_id,dirtied_at);
CREATE TABLE chart_source_refresh_work (
    transaction_id xid8 NOT NULL DEFAULT pg_current_xact_id(),
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    PRIMARY KEY(transaction_id,chain_id,block_number)
);
CREATE TABLE chart_source_reconciliation (
    chain_id numeric(78,0) PRIMARY KEY REFERENCES chains(chain_id),
    next_block numeric(78,0) NOT NULL DEFAULT 0,
    next_at timestamptz NOT NULL DEFAULT now()
);

CREATE FUNCTION chart_current_source(target_chain numeric,target_number numeric)
RETURNS TABLE(block_hash bytea,bucket_start timestamptz,ready boolean) LANGUAGE plpgsql AS $$
BEGIN
    RETURN QUERY SELECT c.block_hash,
        CASE WHEN b.timestamp BETWEEN 0 AND 253402300799
             THEN date_trunc('hour',to_timestamp(b.timestamp::double precision),'UTC') END,
        COALESCE(s.state='complete' AND t.state='complete' AND st.canonical
            AND st.execution_gas_fee_wei IS NOT NULL AND st.priority_fee_wei IS NOT NULL
            AND st.failed_transaction_count IS NOT NULL AND st.contract_creation_count IS NOT NULL, false)
    FROM canonical_blocks c JOIN blocks b ON b.chain_id=c.chain_id AND b.number=c.number AND b.hash=c.block_hash
    LEFT JOIN published_block_stage_results s ON s.chain_id=c.chain_id AND s.block_number=c.number AND s.block_hash=c.block_hash AND s.stage='stats' AND s.stage_version=4
    LEFT JOIN published_block_stage_results t ON t.chain_id=c.chain_id AND t.block_number=c.number AND t.block_hash=c.block_hash AND t.stage='token' AND t.stage_version=2
    LEFT JOIN block_statistics st ON st.chain_id=c.chain_id AND st.block_number=c.number AND st.block_hash=c.block_hash
    WHERE c.chain_id=target_chain AND c.number=target_number;
END
$$;

CREATE FUNCTION refresh_chart_source(target_chain numeric,target_number numeric) RETURNS void LANGUAGE plpgsql AS $$
DECLARE previous chart_source_blocks%ROWTYPE; current_source record; had_previous boolean; has_current boolean; hour_start timestamptz;
BEGIN
    -- Deferred projections share the prefix: proxy coverage, chart source, browsing.
    PERFORM pg_advisory_xact_lock(hashtextextended('etherview:proxy-interaction-coverage:'||target_chain::text,0));
    PERFORM pg_advisory_xact_lock(hashtextextended('chart-source:'||target_chain::text,0));
    SELECT * INTO previous FROM chart_source_blocks WHERE chain_id=target_chain AND block_number=target_number;
    had_previous := FOUND;
    SELECT * INTO current_source FROM chart_current_source(target_chain,target_number);
    has_current := FOUND;
    IF had_previous=has_current AND (NOT has_current OR
        ROW(previous.block_hash,previous.bucket_start,previous.ready) IS NOT DISTINCT FROM
        ROW(current_source.block_hash,current_source.bucket_start,current_source.ready)) THEN RETURN; END IF;
    IF had_previous THEN
        DELETE FROM chart_source_blocks WHERE chain_id=target_chain AND block_number=target_number;
        UPDATE chart_source_hours SET block_count=block_count-1,ready_count=ready_count-previous.ready::integer
        WHERE chain_id=target_chain AND bucket_start=previous.bucket_start;
    END IF;
    IF has_current THEN
        INSERT INTO chart_source_blocks VALUES(target_chain,target_number,current_source.block_hash,current_source.bucket_start,current_source.ready);
        IF current_source.bucket_start IS NOT NULL THEN
            INSERT INTO chart_source_hours(chain_id,bucket_start,block_count,ready_count)
            VALUES(target_chain,current_source.bucket_start,1,current_source.ready::integer)
            ON CONFLICT(chain_id,bucket_start) DO UPDATE SET block_count=chart_source_hours.block_count+1,ready_count=chart_source_hours.ready_count+EXCLUDED.ready_count;
        END IF;
    END IF;
    INSERT INTO chart_source_totals(chain_id) VALUES(target_chain) ON CONFLICT DO NOTHING;
    UPDATE chart_source_totals SET total_blocks=total_blocks+has_current::integer-had_previous::integer,
        completed_blocks=completed_blocks+CASE WHEN has_current AND current_source.ready THEN 1 ELSE 0 END-CASE WHEN had_previous AND previous.ready THEN 1 ELSE 0 END,
        generation=generation+1 WHERE chain_id=target_chain;
    FOR hour_start IN SELECT DISTINCT hour FROM unnest(ARRAY[previous.bucket_start,current_source.bucket_start]) AS hours(hour) WHERE hour IS NOT NULL LOOP
        DELETE FROM chart_source_hours WHERE chain_id=target_chain AND bucket_start=hour_start AND block_count=0;
        UPDATE chart_source_hours SET
            first_block=(SELECT block_number FROM chart_source_blocks WHERE chain_id=target_chain AND bucket_start=hour_start ORDER BY block_number LIMIT 1),
            last_block=(SELECT block_number FROM chart_source_blocks WHERE chain_id=target_chain AND bucket_start=hour_start ORDER BY block_number DESC LIMIT 1),
            first_pending=(SELECT block_number FROM chart_source_blocks WHERE chain_id=target_chain AND bucket_start=hour_start AND NOT ready ORDER BY block_number LIMIT 1)
        WHERE chain_id=target_chain AND bucket_start=hour_start;
    END LOOP;
END
$$;

CREATE FUNCTION flush_chart_sources() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target record;
BEGIN
    FOR target IN SELECT chain_id,block_number FROM chart_source_refresh_work
        WHERE transaction_id=pg_current_xact_id() ORDER BY chain_id,block_number LOOP
        DELETE FROM chart_source_refresh_work WHERE transaction_id=pg_current_xact_id() AND chain_id=target.chain_id AND block_number=target.block_number;
        PERFORM refresh_chart_source(target.chain_id,target.block_number);
    END LOOP;
    RETURN NULL;
END
$$;
CREATE CONSTRAINT TRIGGER chart_source_flush AFTER INSERT ON chart_source_refresh_work
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION flush_chart_sources();

CREATE FUNCTION queue_chart_source() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source jsonb; number numeric;
BEGIN
    IF TG_TABLE_NAME='durable_jobs' AND TG_OP='UPDATE' THEN
        IF ROW(NEW.kind,NEW.chain_id,NEW.stage,NEW.stage_version,NEW.payload,NEW.status,
            NEW.requested_generation,NEW.claimed_generation,NEW.completed_generation,NEW.leased_generation,
            NEW.leased_by,NEW.lease_token,NEW.lease_expires_at IS NULL,NEW.result) IS NOT DISTINCT FROM
           ROW(OLD.kind,OLD.chain_id,OLD.stage,OLD.stage_version,OLD.payload,OLD.status,
            OLD.requested_generation,OLD.claimed_generation,OLD.completed_generation,OLD.leased_generation,
            OLD.leased_by,OLD.lease_token,OLD.lease_expires_at IS NULL,OLD.result) THEN RETURN NULL; END IF;
    END IF;
    FOR source IN SELECT DISTINCT value FROM jsonb_array_elements(CASE TG_OP
        WHEN 'INSERT' THEN jsonb_build_array(to_jsonb(NEW)) WHEN 'DELETE' THEN jsonb_build_array(to_jsonb(OLD))
        ELSE jsonb_build_array(to_jsonb(OLD),to_jsonb(NEW)) END) LOOP
        IF TG_TABLE_NAME IN ('durable_jobs','block_stage_results','block_journals') AND
            NOT ((source->>'stage'='stats' AND source->>'stage_version'='4') OR (source->>'stage'='token' AND source->>'stage_version'='2')) THEN CONTINUE; END IF;
        IF TG_TABLE_NAME='durable_jobs' AND source->>'kind'<>'enrichment' THEN CONTINUE; END IF;
        number := COALESCE(source->>'number',source->>'block_number',source->'payload'->>'block_number')::numeric;
        IF number IS NULL THEN CONTINUE; END IF;
        INSERT INTO chart_source_refresh_work(chain_id,block_number) VALUES((source->>'chain_id')::numeric,number) ON CONFLICT DO NOTHING;
    END LOOP;
    RETURN NULL;
END
$$;
CREATE TRIGGER chart_source_canonical AFTER INSERT OR UPDATE OR DELETE ON canonical_blocks FOR EACH ROW EXECUTE FUNCTION queue_chart_source();
CREATE TRIGGER chart_source_jobs AFTER INSERT OR UPDATE OR DELETE ON durable_jobs FOR EACH ROW EXECUTE FUNCTION queue_chart_source();
CREATE TRIGGER chart_source_results AFTER INSERT OR UPDATE OR DELETE ON block_stage_results FOR EACH ROW EXECUTE FUNCTION queue_chart_source();
CREATE TRIGGER chart_source_statistics AFTER INSERT OR UPDATE OR DELETE ON block_statistics FOR EACH ROW EXECUTE FUNCTION queue_chart_source();
CREATE TRIGGER chart_source_journals AFTER INSERT OR UPDATE OR DELETE ON block_journals FOR EACH ROW EXECUTE FUNCTION queue_chart_source();

CREATE FUNCTION update_chart_dirty_total() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='INSERT' THEN
        INSERT INTO chart_dirty_totals VALUES(NEW.chain_id,1) ON CONFLICT(chain_id)
        DO UPDATE SET dirty_hours=chart_dirty_totals.dirty_hours+1;
    ELSE
        UPDATE chart_dirty_totals SET dirty_hours=dirty_hours-1 WHERE chain_id=OLD.chain_id;
    END IF;
    RETURN NULL;
END
$$;
CREATE TRIGGER chart_dirty_total AFTER INSERT OR DELETE ON chart_rollup_dirty_hours FOR EACH ROW EXECUTE FUNCTION update_chart_dirty_total();

CREATE FUNCTION reconcile_chart_sources(target_chain numeric) RETURNS void LANGUAGE plpgsql AS $$
DECLARE cursor_row chart_source_reconciliation%ROWTYPE; target record; previous chart_source_blocks%ROWTYPE; current_source record; visited integer:=0; next_number numeric:=0;
BEGIN
    INSERT INTO chart_source_reconciliation(chain_id) VALUES(target_chain) ON CONFLICT DO NOTHING;
    SELECT * INTO cursor_row FROM chart_source_reconciliation WHERE chain_id=target_chain AND next_at<=now() FOR UPDATE;
    IF NOT FOUND THEN RETURN; END IF;
    FOR target IN SELECT number FROM (
        (SELECT number FROM canonical_blocks WHERE chain_id=target_chain AND number>=cursor_row.next_block ORDER BY number LIMIT 1000)
        UNION
        (SELECT block_number AS number FROM chart_source_blocks WHERE chain_id=target_chain AND block_number>=cursor_row.next_block ORDER BY block_number LIMIT 1000)
    ) candidates ORDER BY number LIMIT 1000 LOOP
        visited:=visited+1; next_number:=target.number+1;
        SELECT * INTO previous FROM chart_source_blocks WHERE chain_id=target_chain AND block_number=target.number;
        SELECT * INTO current_source FROM chart_current_source(target_chain,target.number);
        IF ROW(previous.block_hash,previous.bucket_start,previous.ready) IS DISTINCT FROM
           ROW(current_source.block_hash,current_source.bucket_start,current_source.ready) THEN
            -- Invalidate before taking the deferred source lock, preserving the
            -- publisher's dirty-hour -> source-summary lock order.
            IF previous.block_hash IS NOT NULL THEN PERFORM mark_chart_rollup_dirty_for_block(target_chain,target.number,previous.block_hash); END IF;
            IF current_source.block_hash IS NOT NULL THEN PERFORM mark_chart_rollup_dirty_for_block(target_chain,target.number,current_source.block_hash); END IF;
            INSERT INTO chart_source_refresh_work(chain_id,block_number) VALUES(target_chain,target.number) ON CONFLICT DO NOTHING;
        END IF;
    END LOOP;
    UPDATE chart_source_reconciliation SET next_block=CASE WHEN visited<1000 THEN 0 ELSE next_number END,next_at=now()+interval '15 minutes' WHERE chain_id=target_chain;
END
$$;

-- Authenticated BAL facts and exact native-balance baselines. Hash identity is
-- immutable; canonical visibility also requires the current publication.
CREATE TABLE native_balance_blocks (
    chain_id numeric(78,0) NOT NULL REFERENCES chains(chain_id),
    block_number numeric(78,0) NOT NULL CHECK (block_number >= 0),
    block_hash bytea NOT NULL CHECK (octet_length(block_hash)=32),
    raw jsonb NOT NULL,
    method text NOT NULL CHECK (method IN ('eth_getBlockAccessListRaw','eth_getBlockAccessList')),
    canonical boolean NOT NULL,
    PRIMARY KEY(chain_id,block_number,block_hash),
    FOREIGN KEY(chain_id,block_number,block_hash) REFERENCES blocks(chain_id,number,hash)
) PARTITION BY RANGE(block_number);
CREATE TABLE native_balance_blocks_default PARTITION OF native_balance_blocks DEFAULT;
CREATE TABLE native_balance_blocks_p_0_1000000 PARTITION OF native_balance_blocks FOR VALUES FROM (0) TO (1000000);
CREATE TABLE native_balance_changes (
    chain_id numeric(78,0) NOT NULL REFERENCES chains(chain_id),
    block_number numeric(78,0) NOT NULL CHECK (block_number >= 0),
    block_hash bytea NOT NULL CHECK (octet_length(block_hash)=32),
    address bytea NOT NULL CHECK (octet_length(address)=20),
    access_index bigint NOT NULL CHECK (access_index BETWEEN 0 AND 4294967295),
    balance numeric(78,0) NOT NULL CHECK (balance>=0 AND balance<115792089237316195423570985008687907853269984665640564039457584007913129639936),
    canonical boolean NOT NULL,
    PRIMARY KEY(chain_id,block_number,block_hash,address,access_index),
    FOREIGN KEY(chain_id,block_number,block_hash) REFERENCES blocks(chain_id,number,hash)
) PARTITION BY RANGE(block_number);
CREATE TABLE native_balance_changes_default PARTITION OF native_balance_changes DEFAULT;
CREATE TABLE native_balance_changes_p_0_1000000 PARTITION OF native_balance_changes FOR VALUES FROM (0) TO (1000000);
CREATE INDEX native_balance_changes_address_idx ON native_balance_changes(chain_id,address,block_number DESC,access_index DESC) WHERE canonical;
CREATE TABLE native_balance_observations (
    chain_id numeric(78,0) NOT NULL REFERENCES chains(chain_id),
    block_number numeric(78,0) NOT NULL CHECK (block_number >= 0),
    block_hash bytea NOT NULL CHECK (octet_length(block_hash)=32),
    address bytea NOT NULL CHECK (octet_length(address)=20),
    balance numeric(78,0) NOT NULL CHECK (balance>=0 AND balance<115792089237316195423570985008687907853269984665640564039457584007913129639936),
    rpc_exact boolean NOT NULL,
    canonical boolean NOT NULL,
    PRIMARY KEY(chain_id,block_number,block_hash,address),
    FOREIGN KEY(chain_id,block_number,block_hash) REFERENCES blocks(chain_id,number,hash)
) PARTITION BY RANGE(block_number);
CREATE TABLE native_balance_observations_default PARTITION OF native_balance_observations DEFAULT;
CREATE TABLE native_balance_observations_p_0_1000000 PARTITION OF native_balance_observations FOR VALUES FROM (0) TO (1000000);
CREATE INDEX native_balance_observations_address_idx ON native_balance_observations(chain_id,address,block_number DESC) WHERE canonical;

-- Exact evidence may gain canonicality or an independent RPC confirmation,
-- but its identity and value never change in place.
CREATE FUNCTION guard_native_balance_evidence() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF (to_jsonb(NEW) - ARRAY['canonical','rpc_exact']) IS DISTINCT FROM
       (to_jsonb(OLD) - ARRAY['canonical','rpc_exact'])
       OR (to_jsonb(OLD)->>'rpc_exact' = 'true' AND to_jsonb(NEW)->>'rpc_exact' <> 'true') THEN
        RAISE EXCEPTION 'native balance evidence is immutable'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER native_balance_body_immutable BEFORE UPDATE ON native_balance_blocks
FOR EACH ROW EXECUTE FUNCTION guard_native_balance_evidence();
CREATE TRIGGER native_balance_change_immutable BEFORE UPDATE ON native_balance_changes
FOR EACH ROW EXECUTE FUNCTION guard_native_balance_evidence();
CREATE TRIGGER native_balance_observation_immutable BEFORE UPDATE ON native_balance_observations
FOR EACH ROW EXECUTE FUNCTION guard_native_balance_evidence();
