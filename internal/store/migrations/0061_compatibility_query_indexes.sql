CREATE INDEX blocks_miner_number_idx
    ON blocks (
        chain_id,
        (lower(raw->>'miner')),
        number DESC,
        hash DESC
    );

CREATE INDEX blocks_timestamp_number_idx
    ON blocks (
        chain_id,
        timestamp,
        number,
        hash
    );
