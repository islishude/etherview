ALTER TABLE blocks
    ADD COLUMN miner_text TEXT
        GENERATED ALWAYS AS (raw->>'miner') STORED,
    ADD COLUMN gas_used_quantity TEXT
        GENERATED ALWAYS AS (raw->>'gasUsed') STORED,
    ADD COLUMN gas_limit_quantity TEXT
        GENERATED ALWAYS AS (raw->>'gasLimit') STORED,
    ADD COLUMN base_fee_per_gas_quantity TEXT
        GENERATED ALWAYS AS (raw->>'baseFeePerGas') STORED,
    ADD COLUMN transaction_count BIGINT
        GENERATED ALWAYS AS (
            CASE
                WHEN jsonb_typeof(raw->'transactions') = 'array'
                    THEN jsonb_array_length(raw->'transactions')::bigint
                ELSE NULL
            END
        ) STORED,
    ADD COLUMN withdrawals_present BOOLEAN
        GENERATED ALWAYS AS (
            raw ? 'withdrawals' AND raw->'withdrawals' <> 'null'::jsonb
        ) STORED,
    ADD COLUMN withdrawal_count BIGINT
        GENERATED ALWAYS AS (
            CASE
                WHEN jsonb_typeof(raw->'withdrawals') = 'array'
                    THEN jsonb_array_length(raw->'withdrawals')::bigint
                ELSE NULL
            END
        ) STORED;

DROP INDEX IF EXISTS blocks_miner_number_idx;

CREATE INDEX blocks_miner_number_idx
    ON blocks (chain_id, lower(miner_text), number DESC, hash DESC);
