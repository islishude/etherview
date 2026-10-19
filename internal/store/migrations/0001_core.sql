-- Fresh-database baseline: core.
-- Earlier development schemas, including the single-file baseline, must be recreated.
-- Apply all numbered files through the migration runner in one transaction.
-- Declare final objects directly; Git retains migration history.
-- Deferred PL/pgSQL bodies may reference objects created by later domains.

-- Settings

SET LOCAL check_function_bodies = false;


-- Functions

CREATE FUNCTION etherview_guard_completed_genesis_import() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path TO 'pg_catalog'
    AS $$
BEGIN
    IF OLD.state = 'complete' AND NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'completed genesis state import is immutable'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION etherview_guard_genesis_account_observation() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path TO 'pg_catalog'
    AS $$
BEGIN
    IF NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'exact genesis account observation is immutable'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END;
$$;


-- Tables

CREATE TABLE chains (
    chain_id numeric(78,0) NOT NULL,
    genesis_hash bytea,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT chains_chain_id_check CHECK ((chain_id >= (0)::numeric)),
    CONSTRAINT chains_genesis_hash_check CHECK (((genesis_hash IS NULL) OR (octet_length(genesis_hash) = 32))),
    CONSTRAINT chains_pkey PRIMARY KEY (chain_id)
);

CREATE TABLE blocks (
    chain_id numeric(78,0) NOT NULL,
    number numeric(78,0) NOT NULL,
    hash bytea NOT NULL,
    parent_hash bytea NOT NULL,
    "timestamp" numeric(78,0) NOT NULL,
    raw jsonb NOT NULL,
    inserted_at timestamp with time zone DEFAULT now() NOT NULL,
    miner_text text GENERATED ALWAYS AS ((raw ->> 'miner'::text)) STORED,
    gas_used_quantity text GENERATED ALWAYS AS ((raw ->> 'gasUsed'::text)) STORED,
    gas_limit_quantity text GENERATED ALWAYS AS ((raw ->> 'gasLimit'::text)) STORED,
    base_fee_per_gas_quantity text GENERATED ALWAYS AS ((raw ->> 'baseFeePerGas'::text)) STORED,
    transaction_count bigint GENERATED ALWAYS AS (
CASE
    WHEN (jsonb_typeof((raw -> 'transactions'::text)) = 'array'::text) THEN (jsonb_array_length((raw -> 'transactions'::text)))::bigint
    ELSE NULL::bigint
END) STORED,
    withdrawals_present boolean GENERATED ALWAYS AS (((raw ? 'withdrawals'::text) AND ((raw -> 'withdrawals'::text) <> 'null'::jsonb))) STORED,
    withdrawal_count bigint GENERATED ALWAYS AS (
CASE
    WHEN (jsonb_typeof((raw -> 'withdrawals'::text)) = 'array'::text) THEN (jsonb_array_length((raw -> 'withdrawals'::text)))::bigint
    ELSE NULL::bigint
END) STORED,
    slot_number_raw jsonb GENERATED ALWAYS AS ((raw -> 'slotNumber'::text)) STORED,
    block_access_list_hash_raw jsonb GENERATED ALWAYS AS ((raw -> 'blockAccessListHash'::text)) STORED,
    CONSTRAINT blocks_hash_check CHECK ((octet_length(hash) = 32)),
    CONSTRAINT blocks_number_check CHECK ((number >= (0)::numeric)),
    CONSTRAINT blocks_parent_hash_check CHECK ((octet_length(parent_hash) = 32)),
    CONSTRAINT blocks_timestamp_check CHECK (("timestamp" >= (0)::numeric)),
    CONSTRAINT blocks_pkey PRIMARY KEY (chain_id, number, hash),
    CONSTRAINT blocks_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE UNIQUE INDEX blocks_chain_hash_uq ON blocks USING btree (chain_id, hash);

CREATE TABLE canonical_blocks (
    chain_id numeric(78,0) NOT NULL,
    number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT canonical_blocks_pkey PRIMARY KEY (chain_id, number),
    CONSTRAINT canonical_blocks_chain_id_number_block_hash_fkey FOREIGN KEY (chain_id, number, block_hash) REFERENCES blocks(chain_id, number, hash) DEFERRABLE INITIALLY DEFERRED
);

CREATE UNIQUE INDEX canonical_blocks_hash_uq ON canonical_blocks USING btree (chain_id, block_hash);

CREATE TABLE chain_finality (
    chain_id numeric(78,0) NOT NULL,
    safe_number numeric(78,0),
    safe_hash bytea,
    finalized_number numeric(78,0),
    finalized_hash bytea,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT chain_finality_check CHECK (((safe_number IS NULL) = (safe_hash IS NULL))),
    CONSTRAINT chain_finality_check1 CHECK (((finalized_number IS NULL) = (finalized_hash IS NULL))),
    CONSTRAINT chain_finality_finalized_hash_check CHECK (((finalized_hash IS NULL) OR (octet_length(finalized_hash) = 32))),
    CONSTRAINT chain_finality_safe_hash_check CHECK (((safe_hash IS NULL) OR (octet_length(safe_hash) = 32))),
    CONSTRAINT chain_finality_pkey PRIMARY KEY (chain_id),
    CONSTRAINT chain_finality_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE core_backfill_leases (
    chain_id numeric(78,0) NOT NULL,
    range_start numeric(78,0) NOT NULL,
    range_end numeric(78,0) NOT NULL,
    owner text NOT NULL,
    lease_token uuid NOT NULL,
    claimed_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT core_backfill_leases_check CHECK ((range_end >= range_start)),
    CONSTRAINT core_backfill_leases_check1 CHECK (((range_end - range_start) < (256)::numeric)),
    CONSTRAINT core_backfill_leases_check2 CHECK ((expires_at > claimed_at)),
    CONSTRAINT core_backfill_leases_owner_check CHECK (((length(owner) >= 1) AND (length(owner) <= 128))),
    CONSTRAINT core_backfill_leases_range_start_check CHECK ((range_start >= (0)::numeric)),
    CONSTRAINT core_backfill_leases_lease_token_key UNIQUE (lease_token),
    CONSTRAINT core_backfill_leases_pkey PRIMARY KEY (chain_id, range_start, range_end),
    CONSTRAINT core_backfill_leases_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE core_coverage_ranges (
    chain_id numeric(78,0) NOT NULL,
    range_start numeric(78,0) NOT NULL,
    range_end numeric(78,0) NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT core_coverage_ranges_check CHECK ((range_end >= range_start)),
    CONSTRAINT core_coverage_ranges_range_start_check CHECK ((range_start >= (0)::numeric)),
    CONSTRAINT core_coverage_ranges_pkey PRIMARY KEY (chain_id, range_start),
    CONSTRAINT core_coverage_ranges_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE UNIQUE INDEX core_coverage_ranges_end_uq ON core_coverage_ranges USING btree (chain_id, range_end);

CREATE TABLE core_index_configuration (
    chain_id numeric(78,0) NOT NULL,
    configured_start numeric(78,0) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT core_index_configuration_configured_start_check CHECK ((configured_start >= (0)::numeric)),
    CONSTRAINT core_index_configuration_pkey PRIMARY KEY (chain_id),
    CONSTRAINT core_index_configuration_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE IF NOT EXISTS etherview_schema_migrations (
    version text PRIMARY KEY,
    checksum text NOT NULL,
    applied_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE genesis_state_imports (
    chain_id numeric(78,0) NOT NULL,
    block_hash bytea,
    state_root bytea,
    document_sha256 bytea,
    state text NOT NULL,
    account_count numeric(78,0),
    last_error_code text,
    imported_at timestamp with time zone,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT genesis_state_imports_account_count_check CHECK (((account_count IS NULL) OR (account_count >= (0)::numeric))),
    CONSTRAINT genesis_state_imports_block_hash_check CHECK (((block_hash IS NULL) OR (octet_length(block_hash) = 32))),
    CONSTRAINT genesis_state_imports_check CHECK ((((state = 'complete'::text) AND (block_hash IS NOT NULL) AND (state_root IS NOT NULL) AND (document_sha256 IS NOT NULL) AND (account_count IS NOT NULL) AND (imported_at IS NOT NULL) AND (last_error_code IS NULL)) OR ((state <> 'complete'::text) AND (account_count IS NULL) AND (imported_at IS NULL)))),
    CONSTRAINT genesis_state_imports_document_sha256_check CHECK (((document_sha256 IS NULL) OR (octet_length(document_sha256) = 32))),
    CONSTRAINT genesis_state_imports_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'complete'::text, 'unavailable'::text, 'failed'::text]))),
    CONSTRAINT genesis_state_imports_state_root_check CHECK (((state_root IS NULL) OR (octet_length(state_root) = 32))),
    CONSTRAINT genesis_state_imports_chain_id_block_hash_key UNIQUE (chain_id, block_hash),
    CONSTRAINT genesis_state_imports_pkey PRIMARY KEY (chain_id),
    CONSTRAINT genesis_state_imports_chain_id_block_hash_fkey FOREIGN KEY (chain_id, block_hash) REFERENCES blocks(chain_id, hash),
    CONSTRAINT genesis_state_imports_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE genesis_account_observations (
    chain_id numeric(78,0) NOT NULL,
    address bytea NOT NULL,
    block_hash bytea NOT NULL,
    balance numeric(78,0) NOT NULL,
    nonce numeric(78,0) NOT NULL,
    code_hash bytea NOT NULL,
    code bytea NOT NULL,
    storage_root bytea NOT NULL,
    observed_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT genesis_account_observations_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT genesis_account_observations_balance_check CHECK ((balance >= (0)::numeric)),
    CONSTRAINT genesis_account_observations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT genesis_account_observations_code_hash_check CHECK ((octet_length(code_hash) = 32)),
    CONSTRAINT genesis_account_observations_nonce_check CHECK ((nonce >= (0)::numeric)),
    CONSTRAINT genesis_account_observations_storage_root_check CHECK ((octet_length(storage_root) = 32)),
    CONSTRAINT genesis_account_observations_pkey PRIMARY KEY (chain_id, address, block_hash),
    CONSTRAINT genesis_account_observations_chain_id_block_hash_fkey FOREIGN KEY (chain_id, block_hash) REFERENCES genesis_state_imports(chain_id, block_hash)
);

CREATE TABLE index_checkpoints (
    chain_id numeric(78,0) NOT NULL,
    stage text NOT NULL,
    contiguous_through numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT index_checkpoints_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT index_checkpoints_pkey PRIMARY KEY (chain_id, stage),
    CONSTRAINT index_checkpoints_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);


-- Indexes

CREATE INDEX blocks_amsterdam_idx ON blocks USING btree (chain_id, number, hash) WHERE ((slot_number_raw IS NOT NULL) AND (slot_number_raw <> 'null'::jsonb));

CREATE INDEX blocks_miner_number_idx ON blocks USING btree (chain_id, lower(miner_text), number DESC, hash DESC);

CREATE INDEX blocks_parent_idx ON blocks USING btree (chain_id, parent_hash);

CREATE INDEX blocks_timestamp_number_idx ON blocks USING btree (chain_id, "timestamp", number, hash);

CREATE INDEX core_backfill_leases_expiry_idx ON core_backfill_leases USING btree (chain_id, expires_at, range_start);

CREATE INDEX genesis_account_observations_page_idx ON genesis_account_observations USING btree (chain_id, block_hash, address);


-- Triggers

CREATE TRIGGER genesis_account_observation_immutable BEFORE UPDATE ON genesis_account_observations FOR EACH ROW EXECUTE FUNCTION etherview_guard_genesis_account_observation();

CREATE TRIGGER genesis_state_import_complete_immutable BEFORE UPDATE ON genesis_state_imports FOR EACH ROW EXECUTE FUNCTION etherview_guard_completed_genesis_import();
