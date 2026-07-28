CREATE INDEX IF NOT EXISTS transaction_inclusions_from_address_idx
    ON transaction_inclusions (
        chain_id,
        lower(raw->>'from'),
        block_number DESC,
        tx_index DESC
    );

CREATE INDEX IF NOT EXISTS transaction_inclusions_to_address_idx
    ON transaction_inclusions (
        chain_id,
        lower(raw->>'to'),
        block_number DESC,
        tx_index DESC
    );

CREATE INDEX IF NOT EXISTS receipts_contract_address_idx
    ON receipts (
        chain_id,
        lower(raw->>'contractAddress'),
        block_number DESC,
        tx_index DESC
    )
    WHERE raw->>'contractAddress' IS NOT NULL;

CREATE INDEX IF NOT EXISTS normalized_traces_created_idx
    ON normalized_traces (
        chain_id,
        created_address,
        block_number DESC
    )
    WHERE canonical AND created_address IS NOT NULL;
