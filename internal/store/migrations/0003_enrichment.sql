CREATE TABLE IF NOT EXISTS block_stage_results (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    stage TEXT NOT NULL,
    stage_version INTEGER NOT NULL,
    state TEXT NOT NULL,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    last_error TEXT,
    completed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, block_hash, stage, stage_version),
    FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash),
    CHECK (octet_length(block_hash) = 32),
    CHECK (stage_version > 0),
    CHECK (state IN ('complete', 'unavailable', 'failed'))
);

CREATE INDEX IF NOT EXISTS block_stage_results_height_idx
    ON block_stage_results (chain_id, block_number, stage, stage_version);

CREATE TABLE IF NOT EXISTS contract_code_observations (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    address BYTEA NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    code_hash BYTEA NOT NULL,
    code BYTEA,
    canonical BOOLEAN NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, address, block_hash),
    FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash),
    CHECK (octet_length(address) = 20),
    CHECK (octet_length(block_hash) = 32),
    CHECK (octet_length(code_hash) = 32)
);

CREATE INDEX IF NOT EXISTS contract_code_history_idx
    ON contract_code_observations (chain_id, address, block_number DESC)
    WHERE canonical;

CREATE TABLE IF NOT EXISTS proxy_observations (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    proxy_address BYTEA NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    proxy_code_hash BYTEA NOT NULL,
    proxy_kind TEXT NOT NULL,
    implementation_address BYTEA,
    beacon_address BYTEA,
    implementation_code_hash BYTEA,
    confidence TEXT NOT NULL,
    canonical BOOLEAN NOT NULL,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (chain_id, proxy_address, block_hash),
    FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash),
    CHECK (octet_length(proxy_address) = 20),
    CHECK (octet_length(block_hash) = 32),
    CHECK (octet_length(proxy_code_hash) = 32),
    CHECK (implementation_address IS NULL OR octet_length(implementation_address) = 20),
    CHECK (beacon_address IS NULL OR octet_length(beacon_address) = 20),
    CHECK (implementation_code_hash IS NULL OR octet_length(implementation_code_hash) = 32),
    CHECK (proxy_kind IN ('eip1167', 'eip1967', 'beacon', 'unknown')),
    CHECK (confidence IN ('verified', 'high', 'inferred', 'guess'))
);

CREATE INDEX IF NOT EXISTS proxy_history_idx
    ON proxy_observations (chain_id, proxy_address, block_number DESC)
    WHERE canonical;

CREATE TABLE IF NOT EXISTS contract_abis (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    address BYTEA NOT NULL,
    code_hash BYTEA NOT NULL,
    source TEXT NOT NULL,
    confidence TEXT NOT NULL,
    abi JSONB NOT NULL,
    valid_from_block NUMERIC(78, 0) NOT NULL,
    valid_to_block NUMERIC(78, 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, address, code_hash, source),
    CHECK (octet_length(address) = 20),
    CHECK (octet_length(code_hash) = 32),
    CHECK (source IN ('verified', 'proxy_implementation', 'signature_database')),
    CHECK (confidence IN ('verified', 'high', 'inferred', 'guess')),
    CHECK (valid_from_block >= 0),
    CHECK (valid_to_block IS NULL OR valid_to_block >= valid_from_block)
);

CREATE TABLE IF NOT EXISTS token_contracts (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    address BYTEA NOT NULL,
    code_hash BYTEA NOT NULL,
    standard TEXT NOT NULL,
    confidence TEXT NOT NULL,
    name TEXT,
    symbol TEXT,
    decimals INTEGER,
    total_supply NUMERIC(78, 0),
    metadata_state TEXT NOT NULL DEFAULT 'pending',
    observed_block_number NUMERIC(78, 0) NOT NULL,
    observed_block_hash BYTEA NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, address, code_hash),
    FOREIGN KEY (chain_id, observed_block_number, observed_block_hash)
        REFERENCES blocks(chain_id, number, hash),
    CHECK (octet_length(address) = 20),
    CHECK (octet_length(code_hash) = 32),
    CHECK (octet_length(observed_block_hash) = 32),
    CHECK (standard IN ('erc20', 'erc721', 'erc1155', 'unknown')),
    CHECK (confidence IN ('verified', 'high', 'inferred', 'guess')),
    CHECK (decimals IS NULL OR decimals BETWEEN 0 AND 255),
    CHECK (total_supply IS NULL OR total_supply >= 0),
    CHECK (metadata_state IN ('pending', 'complete', 'unavailable', 'failed'))
);

CREATE TABLE IF NOT EXISTS token_events (
    chain_id NUMERIC(78, 0) NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    log_index BIGINT NOT NULL,
    sub_index INTEGER NOT NULL DEFAULT 0,
    transaction_hash BYTEA NOT NULL,
    token_address BYTEA NOT NULL,
    standard TEXT NOT NULL,
    event_kind TEXT NOT NULL,
    operator BYTEA,
    from_address BYTEA,
    to_address BYTEA,
    token_id NUMERIC(78, 0),
    amount NUMERIC(78, 0),
    canonical BOOLEAN NOT NULL,
    confidence TEXT NOT NULL,
    raw JSONB NOT NULL,
    PRIMARY KEY (chain_id, block_number, block_hash, log_index, sub_index),
    FOREIGN KEY (chain_id, block_number, block_hash, log_index)
        REFERENCES logs(chain_id, block_number, block_hash, log_index),
    CHECK (octet_length(block_hash) = 32),
    CHECK (octet_length(transaction_hash) = 32),
    CHECK (octet_length(token_address) = 20),
    CHECK (operator IS NULL OR octet_length(operator) = 20),
    CHECK (from_address IS NULL OR octet_length(from_address) = 20),
    CHECK (to_address IS NULL OR octet_length(to_address) = 20),
    CHECK (log_index >= 0 AND sub_index >= 0),
    CHECK (standard IN ('erc20', 'erc721', 'erc1155')),
    CHECK (event_kind IN ('transfer', 'approval', 'approval_for_all', 'mint', 'burn')),
    CHECK (token_id IS NULL OR token_id >= 0),
    CHECK (amount IS NULL OR amount >= 0),
    CHECK (confidence IN ('verified', 'high', 'inferred', 'guess'))
) PARTITION BY RANGE (block_number);

CREATE TABLE IF NOT EXISTS token_events_p_0_1000000
    PARTITION OF token_events FOR VALUES FROM (0) TO (1000000);
CREATE TABLE IF NOT EXISTS token_events_default PARTITION OF token_events DEFAULT;
CREATE INDEX IF NOT EXISTS token_events_token_idx
    ON token_events (chain_id, token_address, block_number DESC, log_index DESC);
CREATE INDEX IF NOT EXISTS token_events_from_idx
    ON token_events (chain_id, from_address, block_number DESC) WHERE canonical;
CREATE INDEX IF NOT EXISTS token_events_to_idx
    ON token_events (chain_id, to_address, block_number DESC) WHERE canonical;

CREATE TABLE IF NOT EXISTS token_balance_deltas (
    chain_id NUMERIC(78, 0) NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    log_index BIGINT NOT NULL,
    sub_index INTEGER NOT NULL DEFAULT 0,
    token_address BYTEA NOT NULL,
    owner_address BYTEA NOT NULL,
    token_id NUMERIC(78, 0),
    delta NUMERIC(79, 0) NOT NULL,
    canonical BOOLEAN NOT NULL,
    PRIMARY KEY (
        chain_id, block_number, block_hash, log_index, sub_index,
        token_address, owner_address
    ),
    FOREIGN KEY (chain_id, block_number, block_hash, log_index, sub_index)
        REFERENCES token_events(chain_id, block_number, block_hash, log_index, sub_index),
    CHECK (octet_length(block_hash) = 32),
    CHECK (octet_length(token_address) = 20),
    CHECK (octet_length(owner_address) = 20),
    CHECK (token_id IS NULL OR token_id >= 0)
) PARTITION BY RANGE (block_number);

CREATE TABLE IF NOT EXISTS token_balance_deltas_p_0_1000000
    PARTITION OF token_balance_deltas FOR VALUES FROM (0) TO (1000000);
CREATE TABLE IF NOT EXISTS token_balance_deltas_default PARTITION OF token_balance_deltas DEFAULT;
CREATE INDEX IF NOT EXISTS token_balance_deltas_owner_idx
    ON token_balance_deltas (chain_id, owner_address, token_address, token_id, block_number)
    WHERE canonical;

CREATE TABLE IF NOT EXISTS normalized_traces (
    chain_id NUMERIC(78, 0) NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    transaction_hash BYTEA NOT NULL,
    transaction_index BIGINT NOT NULL,
    trace_path TEXT NOT NULL,
    parent_path TEXT,
    depth INTEGER NOT NULL,
    call_type TEXT NOT NULL,
    from_address BYTEA,
    to_address BYTEA,
    created_address BYTEA,
    value NUMERIC(78, 0),
    gas NUMERIC(78, 0),
    gas_used NUMERIC(78, 0),
    input BYTEA,
    output BYTEA,
    error TEXT,
    reverted BOOLEAN NOT NULL,
    canonical BOOLEAN NOT NULL,
    PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, trace_path),
    FOREIGN KEY (chain_id, block_number, block_hash, transaction_index)
        REFERENCES transaction_inclusions(chain_id, block_number, block_hash, tx_index),
    CHECK (octet_length(block_hash) = 32),
    CHECK (octet_length(transaction_hash) = 32),
    CHECK (from_address IS NULL OR octet_length(from_address) = 20),
    CHECK (to_address IS NULL OR octet_length(to_address) = 20),
    CHECK (created_address IS NULL OR octet_length(created_address) = 20),
    CHECK (depth >= 0),
    CHECK (value IS NULL OR value >= 0),
    CHECK (gas IS NULL OR gas >= 0),
    CHECK (gas_used IS NULL OR gas_used >= 0)
) PARTITION BY RANGE (block_number);

CREATE TABLE IF NOT EXISTS normalized_traces_p_0_1000000
    PARTITION OF normalized_traces FOR VALUES FROM (0) TO (1000000);
CREATE TABLE IF NOT EXISTS normalized_traces_default PARTITION OF normalized_traces DEFAULT;
CREATE INDEX IF NOT EXISTS normalized_traces_tx_idx
    ON normalized_traces (chain_id, transaction_hash, trace_path);
CREATE INDEX IF NOT EXISTS normalized_traces_from_idx
    ON normalized_traces (chain_id, from_address, block_number DESC) WHERE canonical;
CREATE INDEX IF NOT EXISTS normalized_traces_to_idx
    ON normalized_traces (chain_id, to_address, block_number DESC) WHERE canonical;

CREATE TABLE IF NOT EXISTS block_statistics (
    chain_id NUMERIC(78, 0) NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    transaction_count BIGINT NOT NULL,
    gas_used NUMERIC(78, 0) NOT NULL,
    gas_limit NUMERIC(78, 0) NOT NULL,
    base_fee_per_gas NUMERIC(78, 0),
    blob_gas_used NUMERIC(78, 0),
    burned_wei NUMERIC(78, 0),
    canonical BOOLEAN NOT NULL,
    computed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, block_number, block_hash),
    FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash),
    CHECK (octet_length(block_hash) = 32),
    CHECK (transaction_count >= 0),
    CHECK (gas_used >= 0 AND gas_limit >= 0),
    CHECK (base_fee_per_gas IS NULL OR base_fee_per_gas >= 0),
    CHECK (blob_gas_used IS NULL OR blob_gas_used >= 0),
    CHECK (burned_wei IS NULL OR burned_wei >= 0)
);

CREATE TABLE IF NOT EXISTS external_metadata (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    resource_kind TEXT NOT NULL,
    resource_key TEXT NOT NULL,
    source_uri TEXT NOT NULL,
    resolved_uri TEXT,
    state TEXT NOT NULL,
    media_type TEXT,
    content_hash BYTEA,
    document JSONB,
    last_error TEXT,
    fetched_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    PRIMARY KEY (chain_id, resource_kind, resource_key),
    CHECK (resource_kind IN ('token', 'nft', 'name')),
    CHECK (state IN ('pending', 'complete', 'unavailable', 'failed')),
    CHECK (content_hash IS NULL OR octet_length(content_hash) = 32)
);

CREATE TABLE IF NOT EXISTS name_records (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    registry BYTEA NOT NULL,
    name TEXT NOT NULL,
    address BYTEA NOT NULL,
    resolver BYTEA,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    canonical BOOLEAN NOT NULL,
    PRIMARY KEY (chain_id, registry, name, block_hash),
    FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash),
    CHECK (octet_length(registry) = 20),
    CHECK (octet_length(address) = 20),
    CHECK (resolver IS NULL OR octet_length(resolver) = 20),
    CHECK (octet_length(block_hash) = 32)
);

CREATE INDEX IF NOT EXISTS name_records_name_idx
    ON name_records (chain_id, lower(name), block_number DESC) WHERE canonical;

CREATE TABLE IF NOT EXISTS address_activities (
    chain_id NUMERIC(78, 0) NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    transaction_hash BYTEA NOT NULL,
    activity_index INTEGER NOT NULL,
    address BYTEA NOT NULL,
    activity_kind TEXT NOT NULL,
    canonical BOOLEAN NOT NULL,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, activity_index, address),
    FOREIGN KEY (chain_id, transaction_hash)
        REFERENCES transactions(chain_id, hash),
    CHECK (octet_length(block_hash) = 32),
    CHECK (octet_length(transaction_hash) = 32),
    CHECK (octet_length(address) = 20),
    CHECK (activity_index >= 0),
    CHECK (activity_kind IN ('transaction', 'token', 'trace', 'contract_creation'))
) PARTITION BY RANGE (block_number);

CREATE TABLE IF NOT EXISTS address_activities_p_0_1000000
    PARTITION OF address_activities FOR VALUES FROM (0) TO (1000000);
CREATE TABLE IF NOT EXISTS address_activities_default PARTITION OF address_activities DEFAULT;
CREATE INDEX IF NOT EXISTS address_activities_address_idx
    ON address_activities (chain_id, address, block_number DESC, activity_index DESC)
    WHERE canonical;
