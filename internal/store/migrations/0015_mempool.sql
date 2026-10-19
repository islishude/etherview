-- Fresh-database baseline: mempool.

-- Tables

CREATE TABLE mempool_snapshots (
    chain_id numeric(78,0) NOT NULL,
    id bigint NOT NULL GENERATED ALWAYS AS IDENTITY (
        SEQUENCE NAME mempool_snapshots_id_seq
        START WITH 1
        INCREMENT BY 1
        NO MINVALUE
        NO MAXVALUE
        CACHE 1
    ),
    endpoint_name text NOT NULL,
    observed_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    transaction_count integer NOT NULL,
    CONSTRAINT mempool_snapshots_check CHECK ((expires_at > observed_at)),
    CONSTRAINT mempool_snapshots_endpoint_name_check CHECK (((length(endpoint_name) >= 1) AND (length(endpoint_name) <= 128))),
    CONSTRAINT mempool_snapshots_transaction_count_check CHECK ((transaction_count >= 0)),
    CONSTRAINT mempool_snapshots_pkey PRIMARY KEY (chain_id, id),
    CONSTRAINT mempool_snapshots_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE mempool_transactions (
    chain_id numeric(78,0) NOT NULL,
    tx_hash bytea NOT NULL,
    from_address bytea NOT NULL,
    to_address bytea,
    nonce numeric(78,0) NOT NULL,
    value numeric(78,0) NOT NULL,
    gas numeric(78,0) NOT NULL,
    gas_price numeric(78,0),
    max_fee_per_gas numeric(78,0),
    max_priority_fee_per_gas numeric(78,0),
    tx_type numeric(78,0),
    input bytea NOT NULL,
    raw jsonb NOT NULL,
    first_seen_at timestamp with time zone NOT NULL,
    last_seen_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    last_endpoint_name text NOT NULL,
    CONSTRAINT mempool_transactions_check CHECK (((nonce >= (0)::numeric) AND (value >= (0)::numeric) AND (gas >= (0)::numeric))),
    CONSTRAINT mempool_transactions_check1 CHECK ((last_seen_at >= first_seen_at)),
    CONSTRAINT mempool_transactions_check2 CHECK ((expires_at > last_seen_at)),
    CONSTRAINT mempool_transactions_from_address_check CHECK ((octet_length(from_address) = 20)),
    CONSTRAINT mempool_transactions_gas_price_check CHECK (((gas_price IS NULL) OR (gas_price >= (0)::numeric))),
    CONSTRAINT mempool_transactions_last_endpoint_name_check CHECK (((length(last_endpoint_name) >= 1) AND (length(last_endpoint_name) <= 128))),
    CONSTRAINT mempool_transactions_max_fee_per_gas_check CHECK (((max_fee_per_gas IS NULL) OR (max_fee_per_gas >= (0)::numeric))),
    CONSTRAINT mempool_transactions_max_priority_fee_per_gas_check CHECK (((max_priority_fee_per_gas IS NULL) OR (max_priority_fee_per_gas >= (0)::numeric))),
    CONSTRAINT mempool_transactions_to_address_check CHECK (((to_address IS NULL) OR (octet_length(to_address) = 20))),
    CONSTRAINT mempool_transactions_tx_hash_check CHECK ((octet_length(tx_hash) = 32)),
    CONSTRAINT mempool_transactions_tx_type_check CHECK (((tx_type IS NULL) OR (tx_type >= (0)::numeric))),
    CONSTRAINT mempool_transactions_pkey PRIMARY KEY (chain_id, tx_hash),
    CONSTRAINT mempool_transactions_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE mempool_snapshot_transactions (
    chain_id numeric(78,0) NOT NULL,
    snapshot_id bigint NOT NULL,
    tx_hash bytea NOT NULL,
    CONSTRAINT mempool_snapshot_transactions_tx_hash_check CHECK ((octet_length(tx_hash) = 32)),
    CONSTRAINT mempool_snapshot_transactions_pkey PRIMARY KEY (chain_id, snapshot_id, tx_hash),
    CONSTRAINT mempool_snapshot_transactions_chain_id_snapshot_id_fkey FOREIGN KEY (chain_id, snapshot_id) REFERENCES mempool_snapshots(chain_id, id) ON DELETE CASCADE,
    CONSTRAINT mempool_snapshot_transactions_chain_id_tx_hash_fkey FOREIGN KEY (chain_id, tx_hash) REFERENCES mempool_transactions(chain_id, tx_hash)
);

CREATE TABLE mempool_status (
    chain_id numeric(78,0) NOT NULL,
    state text NOT NULL,
    endpoint_name text,
    latest_snapshot_id bigint,
    transaction_count integer,
    last_attempt_at timestamp with time zone NOT NULL,
    last_success_at timestamp with time zone,
    error_code text,
    error_message text,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    last_snapshot_write_id bigint,
    CONSTRAINT mempool_status_check CHECK (((latest_snapshot_id IS NULL) = (last_success_at IS NULL))),
    CONSTRAINT mempool_status_check1 CHECK (((state = ANY (ARRAY['unavailable'::text, 'failed'::text])) = (error_code IS NOT NULL))),
    CONSTRAINT mempool_status_check2 CHECK (((state = ANY (ARRAY['unavailable'::text, 'failed'::text])) = (error_message IS NOT NULL))),
    CONSTRAINT mempool_status_endpoint_name_check CHECK (((endpoint_name IS NULL) OR ((length(endpoint_name) >= 1) AND (length(endpoint_name) <= 128)))),
    CONSTRAINT mempool_status_error_code_check CHECK (((error_code IS NULL) OR ((length(error_code) >= 1) AND (length(error_code) <= 64)))),
    CONSTRAINT mempool_status_error_message_check CHECK (((error_message IS NULL) OR ((length(error_message) >= 1) AND (length(error_message) <= 1024)))),
    CONSTRAINT mempool_status_last_snapshot_write_id_positive CHECK (((last_snapshot_write_id IS NULL) OR (last_snapshot_write_id > 0))),
    CONSTRAINT mempool_status_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'complete'::text, 'unavailable'::text, 'failed'::text]))),
    CONSTRAINT mempool_status_transaction_count_check CHECK (((transaction_count IS NULL) OR (transaction_count >= 0))),
    CONSTRAINT mempool_status_pkey PRIMARY KEY (chain_id),
    CONSTRAINT mempool_status_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT mempool_status_chain_id_latest_snapshot_id_fkey FOREIGN KEY (chain_id, latest_snapshot_id) REFERENCES mempool_snapshots(chain_id, id)
);

CREATE TABLE mempool_transaction_replacements (
    chain_id numeric(78,0) NOT NULL,
    snapshot_id bigint NOT NULL,
    replaced_hash bytea NOT NULL,
    replacement_hash bytea NOT NULL,
    CONSTRAINT mempool_transaction_replacements_check CHECK ((replaced_hash <> replacement_hash)),
    CONSTRAINT mempool_transaction_replacements_replaced_hash_check CHECK ((octet_length(replaced_hash) = 32)),
    CONSTRAINT mempool_transaction_replacements_replacement_hash_check CHECK ((octet_length(replacement_hash) = 32)),
    CONSTRAINT mempool_transaction_replaceme_chain_id_snapshot_id_replacem_key UNIQUE (chain_id, snapshot_id, replacement_hash),
    CONSTRAINT mempool_transaction_replacements_pkey PRIMARY KEY (chain_id, snapshot_id, replaced_hash),
    CONSTRAINT mempool_transaction_replacements_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT mempool_transaction_replacements_chain_id_replaced_hash_fkey FOREIGN KEY (chain_id, replaced_hash) REFERENCES mempool_transactions(chain_id, tx_hash),
    CONSTRAINT mempool_transaction_replacements_chain_id_replacement_hash_fkey FOREIGN KEY (chain_id, replacement_hash) REFERENCES mempool_transactions(chain_id, tx_hash),
    CONSTRAINT mempool_transaction_replacements_chain_id_snapshot_id_fkey FOREIGN KEY (chain_id, snapshot_id) REFERENCES mempool_snapshots(chain_id, id) ON DELETE CASCADE
);


-- Indexes

CREATE INDEX mempool_snapshot_transactions_tx_idx ON mempool_snapshot_transactions USING btree (chain_id, tx_hash, snapshot_id);

CREATE INDEX mempool_snapshots_expiry_idx ON mempool_snapshots USING btree (chain_id, expires_at, id);

CREATE INDEX mempool_transaction_replacements_new_idx ON mempool_transaction_replacements USING btree (chain_id, replacement_hash, snapshot_id DESC);

CREATE INDEX mempool_transaction_replacements_old_idx ON mempool_transaction_replacements USING btree (chain_id, replaced_hash, snapshot_id DESC);

CREATE INDEX mempool_transactions_expiry_idx ON mempool_transactions USING btree (chain_id, expires_at, tx_hash);
