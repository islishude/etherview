-- Bind every persisted ABI candidate set to an exact observed block identity
-- and an inclusive code-validity range. Existing rows predate production ABI
-- stage wiring; they can be upgraded only when their range start is a known
-- canonical block, otherwise the NOT NULL transition intentionally fails and
-- requires an operator to repair the orphaned legacy fact.
ALTER TABLE contract_abis
    ADD COLUMN IF NOT EXISTS block_number NUMERIC(78, 0),
    ADD COLUMN IF NOT EXISTS block_hash BYTEA,
    ADD COLUMN IF NOT EXISTS source_address BYTEA,
    ADD COLUMN IF NOT EXISTS source_code_hash BYTEA,
    ADD COLUMN IF NOT EXISTS canonical BOOLEAN;

UPDATE contract_abis AS abi
SET block_number = abi.valid_from_block,
    block_hash = canonical.block_hash,
    source_address = abi.address,
    source_code_hash = abi.code_hash,
    canonical = TRUE
FROM canonical_blocks AS canonical
WHERE abi.block_number IS NULL
  AND canonical.chain_id = abi.chain_id
  AND canonical.number = abi.valid_from_block;

-- Source is the authority for confidence. Normalize pre-stage rows before the
-- exact mapping becomes a database constraint, including downgrading any
-- legacy signature candidate that was incorrectly labelled verified.
UPDATE contract_abis
SET confidence = CASE source
    WHEN 'verified' THEN 'verified'
    WHEN 'proxy_implementation' THEN 'high'
    WHEN 'signature_database' THEN 'guess'
END;

ALTER TABLE contract_abis
    ALTER COLUMN block_number SET NOT NULL,
    ALTER COLUMN block_hash SET NOT NULL,
    ALTER COLUMN source_address SET NOT NULL,
    ALTER COLUMN source_code_hash SET NOT NULL,
    ALTER COLUMN canonical SET NOT NULL,
    DROP CONSTRAINT IF EXISTS contract_abis_pkey,
    DROP CONSTRAINT IF EXISTS contract_abis_source_check,
    DROP CONSTRAINT IF EXISTS contract_abis_confidence_check;

ALTER TABLE contract_abis
    ADD PRIMARY KEY (
        chain_id, address, code_hash, source, valid_from_block, block_hash
    ),
    ADD FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash),
    ADD CHECK (octet_length(block_hash) = 32),
    ADD CHECK (octet_length(source_address) = 20),
    ADD CHECK (octet_length(source_code_hash) = 32),
    ADD CHECK (block_number >= valid_from_block),
    ADD CHECK (valid_to_block IS NULL OR block_number <= valid_to_block),
    ADD CHECK (source IN ('verified', 'proxy_implementation', 'signature_database')),
    ADD CHECK (
        (source = 'verified' AND confidence = 'verified') OR
        (source = 'proxy_implementation' AND confidence = 'high') OR
        (source = 'signature_database' AND confidence = 'guess')
    ),
    ADD CHECK (
        source = 'proxy_implementation' OR
        (source_address = address AND source_code_hash = code_hash)
    );

CREATE INDEX IF NOT EXISTS contract_abis_context_idx
    ON contract_abis (
        chain_id, address, code_hash, block_number DESC, source, canonical
    );

-- External signature adapters populate this table with one canonical ABI
-- entry per signature. The stage re-hashes and validates every candidate
-- before it can become a block-bound guess.
CREATE TABLE IF NOT EXISTS abi_signature_candidates (
    kind TEXT NOT NULL,
    identifier BYTEA NOT NULL,
    signature TEXT NOT NULL,
    abi_entry JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (kind, identifier, signature),
    CHECK (kind IN ('function', 'event', 'error')),
    CHECK (
        (kind IN ('function', 'error') AND octet_length(identifier) = 4) OR
        (kind = 'event' AND octet_length(identifier) = 32)
    ),
    CHECK (signature <> ''),
    CHECK (jsonb_typeof(abi_entry) = 'object')
);

CREATE TABLE IF NOT EXISTS abi_decodings (
    chain_id NUMERIC(78, 0) NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    object_kind TEXT NOT NULL,
    transaction_hash BYTEA NOT NULL,
    object_index TEXT NOT NULL,
    target_address BYTEA NOT NULL,
    target_code_hash BYTEA NOT NULL,
    abi_kind TEXT NOT NULL,
    status TEXT NOT NULL,
    signature TEXT,
    source TEXT,
    confidence TEXT,
    arguments JSONB NOT NULL DEFAULT '[]'::jsonb,
    candidates JSONB NOT NULL DEFAULT '[]'::jsonb,
    warning TEXT NOT NULL DEFAULT '',
    canonical BOOLEAN NOT NULL,
    decoded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (
        chain_id, block_number, block_hash, object_kind,
        transaction_hash, object_index
    ),
    FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash),
    CHECK (octet_length(block_hash) = 32),
    CHECK (octet_length(transaction_hash) = 32),
    CHECK (octet_length(target_address) = 20),
    CHECK (octet_length(target_code_hash) = 32),
    CHECK (object_kind IN (
        'transaction_calldata', 'log', 'trace_calldata', 'trace_revert'
    )),
    CHECK (abi_kind IN ('function', 'event', 'error')),
    CHECK (status IN ('decoded', 'unknown', 'malformed', 'ambiguous')),
    CHECK (
        (source IS NULL AND confidence IS NULL) OR
        (source = 'verified' AND confidence = 'verified') OR
        (source = 'proxy_implementation' AND confidence = 'high') OR
        (source = 'signature_database' AND confidence = 'guess') OR
        (source = 'builtin' AND confidence = 'high')
    ),
    CHECK (status IN ('decoded', 'ambiguous') OR signature IS NULL)
) PARTITION BY RANGE (block_number);

CREATE TABLE IF NOT EXISTS abi_decodings_p_0_1000000
    PARTITION OF abi_decodings FOR VALUES FROM (0) TO (1000000);
CREATE TABLE IF NOT EXISTS abi_decodings_default
    PARTITION OF abi_decodings DEFAULT;
CREATE INDEX IF NOT EXISTS abi_decodings_target_idx
    ON abi_decodings (
        chain_id, target_address, target_code_hash, block_number DESC
    ) WHERE canonical;
CREATE INDEX IF NOT EXISTS abi_decodings_tx_idx
    ON abi_decodings (chain_id, transaction_hash, block_number DESC);
