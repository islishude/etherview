CREATE TABLE IF NOT EXISTS etherview_schema_migrations (
    version TEXT PRIMARY KEY,
    checksum TEXT NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS chains (
    chain_id NUMERIC(78, 0) PRIMARY KEY,
    genesis_hash BYTEA,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (chain_id >= 0),
    CHECK (genesis_hash IS NULL OR octet_length(genesis_hash) = 32)
);

CREATE TABLE IF NOT EXISTS blocks (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    number NUMERIC(78, 0) NOT NULL,
    hash BYTEA NOT NULL,
    parent_hash BYTEA NOT NULL,
    timestamp NUMERIC(78, 0) NOT NULL,
    raw JSONB NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, number, hash),
    CHECK (number >= 0),
    CHECK (octet_length(hash) = 32),
    CHECK (octet_length(parent_hash) = 32),
    CHECK (timestamp >= 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS blocks_chain_hash_uq ON blocks (chain_id, hash);
CREATE INDEX IF NOT EXISTS blocks_parent_idx ON blocks (chain_id, parent_hash);

CREATE TABLE IF NOT EXISTS canonical_blocks (
    chain_id NUMERIC(78, 0) NOT NULL,
    number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, number),
    FOREIGN KEY (chain_id, number, block_hash)
        REFERENCES blocks(chain_id, number, hash)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE UNIQUE INDEX IF NOT EXISTS canonical_blocks_hash_uq
    ON canonical_blocks (chain_id, block_hash);

CREATE TABLE IF NOT EXISTS transactions (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    hash BYTEA NOT NULL,
    tx_type NUMERIC(78, 0),
    raw JSONB NOT NULL,
    inserted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, hash),
    CHECK (octet_length(hash) = 32)
);

CREATE TABLE IF NOT EXISTS transaction_inclusions (
    chain_id NUMERIC(78, 0) NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    tx_index BIGINT NOT NULL,
    tx_hash BYTEA NOT NULL,
    raw JSONB NOT NULL,
    PRIMARY KEY (chain_id, block_number, block_hash, tx_index),
    UNIQUE (chain_id, block_number, block_hash, tx_hash),
    FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash),
    FOREIGN KEY (chain_id, tx_hash)
        REFERENCES transactions(chain_id, hash),
    CHECK (tx_index >= 0)
) PARTITION BY RANGE (block_number);

CREATE TABLE IF NOT EXISTS transaction_inclusions_p_0_1000000
    PARTITION OF transaction_inclusions FOR VALUES FROM (0) TO (1000000);

CREATE TABLE IF NOT EXISTS transaction_inclusions_default
    PARTITION OF transaction_inclusions DEFAULT;

CREATE TABLE IF NOT EXISTS receipts (
    chain_id NUMERIC(78, 0) NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    tx_index BIGINT NOT NULL,
    tx_hash BYTEA NOT NULL,
    raw JSONB NOT NULL,
    PRIMARY KEY (chain_id, block_number, block_hash, tx_index),
    UNIQUE (chain_id, block_number, block_hash, tx_hash),
    FOREIGN KEY (chain_id, block_number, block_hash, tx_index)
        REFERENCES transaction_inclusions(chain_id, block_number, block_hash, tx_index),
    CHECK (tx_index >= 0)
) PARTITION BY RANGE (block_number);

CREATE TABLE IF NOT EXISTS receipts_p_0_1000000
    PARTITION OF receipts FOR VALUES FROM (0) TO (1000000);

CREATE TABLE IF NOT EXISTS receipts_default PARTITION OF receipts DEFAULT;

CREATE TABLE IF NOT EXISTS logs (
    chain_id NUMERIC(78, 0) NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    log_index BIGINT NOT NULL,
    tx_index BIGINT NOT NULL,
    tx_hash BYTEA NOT NULL,
    address BYTEA NOT NULL,
    topic0 BYTEA,
    raw JSONB NOT NULL,
    PRIMARY KEY (chain_id, block_number, block_hash, log_index),
    FOREIGN KEY (chain_id, block_number, block_hash, tx_index)
        REFERENCES receipts(chain_id, block_number, block_hash, tx_index),
    CHECK (log_index >= 0),
    CHECK (tx_index >= 0),
    CHECK (octet_length(address) = 20),
    CHECK (topic0 IS NULL OR octet_length(topic0) = 32)
) PARTITION BY RANGE (block_number);

CREATE TABLE IF NOT EXISTS logs_p_0_1000000
    PARTITION OF logs FOR VALUES FROM (0) TO (1000000);

CREATE TABLE IF NOT EXISTS logs_default PARTITION OF logs DEFAULT;
CREATE INDEX IF NOT EXISTS logs_address_idx ON logs (chain_id, address, block_number DESC);
CREATE INDEX IF NOT EXISTS logs_topic0_idx ON logs (chain_id, topic0, block_number DESC);

CREATE TABLE IF NOT EXISTS withdrawals (
    chain_id NUMERIC(78, 0) NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    withdrawal_index NUMERIC(78, 0) NOT NULL,
    validator_index NUMERIC(78, 0) NOT NULL,
    address BYTEA NOT NULL,
    amount NUMERIC(78, 0) NOT NULL,
    raw JSONB NOT NULL,
    PRIMARY KEY (chain_id, block_number, block_hash, withdrawal_index),
    FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash),
    CHECK (octet_length(address) = 20)
) PARTITION BY RANGE (block_number);

CREATE TABLE IF NOT EXISTS withdrawals_p_0_1000000
    PARTITION OF withdrawals FOR VALUES FROM (0) TO (1000000);

CREATE TABLE IF NOT EXISTS withdrawals_default PARTITION OF withdrawals DEFAULT;

CREATE TABLE IF NOT EXISTS index_checkpoints (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    stage TEXT NOT NULL,
    contiguous_through NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, stage),
    CHECK (octet_length(block_hash) = 32)
);

CREATE TABLE IF NOT EXISTS chain_finality (
    chain_id NUMERIC(78, 0) PRIMARY KEY REFERENCES chains(chain_id),
    safe_number NUMERIC(78, 0),
    safe_hash BYTEA,
    finalized_number NUMERIC(78, 0),
    finalized_hash BYTEA,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((safe_number IS NULL) = (safe_hash IS NULL)),
    CHECK ((finalized_number IS NULL) = (finalized_hash IS NULL)),
    CHECK (safe_hash IS NULL OR octet_length(safe_hash) = 32),
    CHECK (finalized_hash IS NULL OR octet_length(finalized_hash) = 32)
);

CREATE TABLE IF NOT EXISTS block_journals (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    block_hash BYTEA NOT NULL,
    stage TEXT NOT NULL,
    sequence NUMERIC(78, 0) NOT NULL,
    payload JSONB NOT NULL,
    canonical BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, block_hash, stage, sequence),
    CHECK (octet_length(block_hash) = 32)
);

CREATE INDEX IF NOT EXISTS block_journals_canonical_idx
    ON block_journals (chain_id, stage, canonical, block_hash);

CREATE TABLE IF NOT EXISTS reorg_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    ancestor_number NUMERIC(78, 0) NOT NULL,
    ancestor_hash BYTEA NOT NULL,
    old_tip_number NUMERIC(78, 0),
    old_tip_hash BYTEA,
    new_tip_number NUMERIC(78, 0),
    new_tip_hash BYTEA,
    detached JSONB NOT NULL,
    attached JSONB NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
