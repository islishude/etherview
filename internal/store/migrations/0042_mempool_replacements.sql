CREATE TABLE IF NOT EXISTS mempool_transaction_replacements (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    snapshot_id BIGINT NOT NULL,
    replaced_hash BYTEA NOT NULL,
    replacement_hash BYTEA NOT NULL,
    PRIMARY KEY (chain_id, snapshot_id, replaced_hash),
    UNIQUE (chain_id, snapshot_id, replacement_hash),
    FOREIGN KEY (chain_id, snapshot_id)
        REFERENCES mempool_snapshots(chain_id, id) ON DELETE CASCADE,
    FOREIGN KEY (chain_id, replaced_hash)
        REFERENCES mempool_transactions(chain_id, tx_hash),
    FOREIGN KEY (chain_id, replacement_hash)
        REFERENCES mempool_transactions(chain_id, tx_hash),
    CHECK (octet_length(replaced_hash) = 32),
    CHECK (octet_length(replacement_hash) = 32),
    CHECK (replaced_hash <> replacement_hash)
);

CREATE INDEX IF NOT EXISTS mempool_transaction_replacements_old_idx
    ON mempool_transaction_replacements (chain_id, replaced_hash, snapshot_id DESC);

CREATE INDEX IF NOT EXISTS mempool_transaction_replacements_new_idx
    ON mempool_transaction_replacements (chain_id, replacement_hash, snapshot_id DESC);

ALTER TABLE mempool_status
    ADD COLUMN last_snapshot_write_id BIGINT;

UPDATE mempool_status
SET last_snapshot_write_id = latest_snapshot_id
WHERE latest_snapshot_id IS NOT NULL;

ALTER TABLE mempool_status
    ADD CONSTRAINT mempool_status_last_snapshot_write_id_positive
    CHECK (last_snapshot_write_id IS NULL OR last_snapshot_write_id > 0);
