CREATE TABLE IF NOT EXISTS mempool_snapshots (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    id BIGINT GENERATED ALWAYS AS IDENTITY,
    endpoint_name TEXT NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    transaction_count INTEGER NOT NULL,
    PRIMARY KEY (chain_id, id),
    CHECK (length(endpoint_name) BETWEEN 1 AND 128),
    CHECK (expires_at > observed_at),
    CHECK (transaction_count >= 0)
);

CREATE INDEX IF NOT EXISTS mempool_snapshots_expiry_idx
    ON mempool_snapshots (chain_id, expires_at, id);

CREATE TABLE IF NOT EXISTS mempool_transactions (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    tx_hash BYTEA NOT NULL,
    from_address BYTEA NOT NULL,
    to_address BYTEA,
    nonce NUMERIC(78, 0) NOT NULL,
    value NUMERIC(78, 0) NOT NULL,
    gas NUMERIC(78, 0) NOT NULL,
    gas_price NUMERIC(78, 0),
    max_fee_per_gas NUMERIC(78, 0),
    max_priority_fee_per_gas NUMERIC(78, 0),
    tx_type NUMERIC(78, 0),
    input BYTEA NOT NULL,
    raw JSONB NOT NULL,
    first_seen_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    last_endpoint_name TEXT NOT NULL,
    PRIMARY KEY (chain_id, tx_hash),
    CHECK (octet_length(tx_hash) = 32),
    CHECK (octet_length(from_address) = 20),
    CHECK (to_address IS NULL OR octet_length(to_address) = 20),
    CHECK (nonce >= 0 AND value >= 0 AND gas >= 0),
    CHECK (gas_price IS NULL OR gas_price >= 0),
    CHECK (max_fee_per_gas IS NULL OR max_fee_per_gas >= 0),
    CHECK (max_priority_fee_per_gas IS NULL OR max_priority_fee_per_gas >= 0),
    CHECK (tx_type IS NULL OR tx_type >= 0),
    CHECK (last_seen_at >= first_seen_at),
    CHECK (expires_at > last_seen_at),
    CHECK (length(last_endpoint_name) BETWEEN 1 AND 128)
);

CREATE INDEX IF NOT EXISTS mempool_transactions_expiry_idx
    ON mempool_transactions (chain_id, expires_at, tx_hash);

CREATE TABLE IF NOT EXISTS mempool_snapshot_transactions (
    chain_id NUMERIC(78, 0) NOT NULL,
    snapshot_id BIGINT NOT NULL,
    tx_hash BYTEA NOT NULL,
    PRIMARY KEY (chain_id, snapshot_id, tx_hash),
    FOREIGN KEY (chain_id, snapshot_id)
        REFERENCES mempool_snapshots(chain_id, id) ON DELETE CASCADE,
    FOREIGN KEY (chain_id, tx_hash)
        REFERENCES mempool_transactions(chain_id, tx_hash),
    CHECK (octet_length(tx_hash) = 32)
);

CREATE INDEX IF NOT EXISTS mempool_snapshot_transactions_tx_idx
    ON mempool_snapshot_transactions (chain_id, tx_hash, snapshot_id);

CREATE TABLE IF NOT EXISTS mempool_status (
    chain_id NUMERIC(78, 0) PRIMARY KEY REFERENCES chains(chain_id),
    state TEXT NOT NULL,
    endpoint_name TEXT,
    latest_snapshot_id BIGINT,
    transaction_count INTEGER,
    last_attempt_at TIMESTAMPTZ NOT NULL,
    last_success_at TIMESTAMPTZ,
    error_code TEXT,
    error_message TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (chain_id, latest_snapshot_id)
        REFERENCES mempool_snapshots(chain_id, id),
    CHECK (state IN ('pending', 'complete', 'unavailable', 'failed')),
    CHECK (endpoint_name IS NULL OR length(endpoint_name) BETWEEN 1 AND 128),
    CHECK ((latest_snapshot_id IS NULL) = (last_success_at IS NULL)),
    CHECK (transaction_count IS NULL OR transaction_count >= 0),
    CHECK (error_code IS NULL OR length(error_code) BETWEEN 1 AND 64),
    CHECK (error_message IS NULL OR length(error_message) BETWEEN 1 AND 1024),
    CHECK ((state IN ('unavailable', 'failed')) = (error_code IS NOT NULL)),
    CHECK ((state IN ('unavailable', 'failed')) = (error_message IS NOT NULL))
);
