CREATE TABLE IF NOT EXISTS transaction_state_changes (
    chain_id NUMERIC(78, 0) NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    transaction_hash BYTEA NOT NULL,
    transaction_index BIGINT NOT NULL,
    address BYTEA NOT NULL,
    field_kind TEXT NOT NULL,
    storage_key BYTEA NOT NULL DEFAULT ''::bytea,
    before_value TEXT,
    after_value TEXT,
    canonical BOOLEAN NOT NULL,
    PRIMARY KEY (
        chain_id, block_number, block_hash, transaction_hash,
        address, field_kind, storage_key
    ),
    FOREIGN KEY (chain_id, block_number, block_hash, transaction_index)
        REFERENCES transaction_inclusions(chain_id, block_number, block_hash, tx_index),
    CHECK (octet_length(block_hash) = 32),
    CHECK (octet_length(transaction_hash) = 32),
    CHECK (octet_length(address) = 20),
    CHECK (field_kind IN ('balance', 'nonce', 'code', 'storage')),
    CHECK (
        (field_kind = 'storage' AND octet_length(storage_key) = 32)
        OR (field_kind <> 'storage' AND octet_length(storage_key) = 0)
    ),
    CHECK (before_value IS NOT NULL OR after_value IS NOT NULL)
) PARTITION BY RANGE (block_number);

CREATE TABLE IF NOT EXISTS transaction_state_changes_p_0_1000000
    PARTITION OF transaction_state_changes FOR VALUES FROM (0) TO (1000000);
CREATE TABLE IF NOT EXISTS transaction_state_changes_default
    PARTITION OF transaction_state_changes DEFAULT;

CREATE INDEX IF NOT EXISTS transaction_state_changes_tx_idx
    ON transaction_state_changes (
        chain_id, transaction_hash, block_number DESC, address, field_kind, storage_key
    );

CREATE INDEX IF NOT EXISTS transaction_state_changes_address_idx
    ON transaction_state_changes (chain_id, address, block_number DESC)
    WHERE canonical;
