-- abi@4 materializes the effective transaction-root execution identity while
-- retaining state_diff@3 as the immutable provider-evidence boundary. History
-- is rebuilt only by an explicit bounded ABI reindex; this migration never
-- enqueues work.

ALTER TABLE transaction_inclusions
    ADD CONSTRAINT transaction_inclusions_position_hash_key
    UNIQUE (
        chain_id, block_number, block_hash, tx_index, tx_hash
    );

CREATE TABLE IF NOT EXISTS transaction_effective_execution_identities (
    chain_id NUMERIC(78, 0) NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    transaction_hash BYTEA NOT NULL,
    transaction_index BIGINT NOT NULL,
    context_address BYTEA NOT NULL,
    execution_address BYTEA,
    execution_code_hash BYTEA,
    resolution TEXT NOT NULL,
    evidence_source TEXT NOT NULL,
    root_trace_path TEXT,
    canonical BOOLEAN NOT NULL,
    PRIMARY KEY (
        chain_id, block_number, block_hash, transaction_hash, context_address
    ),
    FOREIGN KEY (
        chain_id, block_number, block_hash, transaction_index, transaction_hash
    )
        REFERENCES transaction_inclusions(
            chain_id, block_number, block_hash, tx_index, tx_hash
        ),
    CHECK (block_number >= 0),
    CHECK (transaction_index >= 0),
    CHECK (octet_length(block_hash) = 32),
    CHECK (octet_length(transaction_hash) = 32),
    CHECK (octet_length(context_address) = 20),
    CHECK (
        execution_address IS NULL OR octet_length(execution_address) = 20
    ),
    CHECK (
        execution_code_hash IS NULL OR octet_length(execution_code_hash) = 32
    ),
    CHECK (
        resolution IN (
            'direct', 'eip7702_delegate', 'empty', 'unavailable'
        )
    ),
    CHECK (
        evidence_source IN (
            'prestate_tracer', 'root_trace_code_observation', 'unavailable'
        )
    ),
    CHECK (
        (evidence_source = 'root_trace_code_observation' AND
         root_trace_path = '')
        OR
        (evidence_source <> 'root_trace_code_observation' AND
         root_trace_path IS NULL)
    ),
    CHECK (
        (resolution = 'direct' AND
         execution_address = context_address AND
         execution_code_hash IS NOT NULL AND
         evidence_source = 'prestate_tracer')
        OR
        (resolution = 'eip7702_delegate' AND
         execution_address IS NOT NULL AND
         execution_code_hash IS NOT NULL AND
         evidence_source IN (
             'prestate_tracer', 'root_trace_code_observation'
         ))
        OR
        (resolution = 'empty' AND
         execution_address IS NULL AND
         execution_code_hash IS NULL AND
         evidence_source IN (
             'prestate_tracer', 'root_trace_code_observation'
         ))
        OR
        (resolution = 'unavailable' AND
         execution_code_hash IS NULL AND
         evidence_source = 'unavailable')
    )
) PARTITION BY RANGE (block_number);

CREATE TABLE IF NOT EXISTS transaction_effective_execution_identities_p_0_1000000
    PARTITION OF transaction_effective_execution_identities
    FOR VALUES FROM (0) TO (1000000);
CREATE TABLE IF NOT EXISTS transaction_effective_execution_identities_default
    PARTITION OF transaction_effective_execution_identities DEFAULT;
CREATE INDEX IF NOT EXISTS transaction_effective_execution_identities_tx_idx
    ON transaction_effective_execution_identities (
        chain_id, transaction_hash, context_address
    ) WHERE canonical;
CREATE INDEX IF NOT EXISTS transaction_effective_execution_identities_code_idx
    ON transaction_effective_execution_identities (
        chain_id, execution_address, execution_code_hash,
        block_number DESC, transaction_index DESC
    ) WHERE canonical AND execution_address IS NOT NULL;
