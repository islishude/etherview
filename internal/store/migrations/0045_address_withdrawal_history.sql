CREATE INDEX IF NOT EXISTS withdrawals_address_index_idx
    ON withdrawals (chain_id, address, withdrawal_index DESC);
