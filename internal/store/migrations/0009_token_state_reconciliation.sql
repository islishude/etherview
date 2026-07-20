-- P20-T04: preserve immutable token observations and exact NFT state.
ALTER TABLE token_contracts
    DROP CONSTRAINT token_contracts_pkey;

ALTER TABLE token_contracts
    ADD PRIMARY KEY (chain_id, address, code_hash, observed_block_hash);

CREATE INDEX token_contracts_canonical_lookup_idx
    ON token_contracts (chain_id, address, observed_block_number DESC, observed_block_hash, code_hash);

CREATE TABLE erc721_owner_reconciliations (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    token_address BYTEA NOT NULL,
    token_id NUMERIC(78, 0) NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    state TEXT NOT NULL,
    owner_address BYTEA,
    confidence TEXT NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, token_address, token_id, block_hash),
    FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash),
    CHECK (octet_length(token_address) = 20),
    CHECK (token_id >= 0),
    CHECK (octet_length(block_hash) = 32),
    CHECK (owner_address IS NULL OR octet_length(owner_address) = 20),
    CHECK (state IN ('owned', 'not_found')),
    CHECK (
        (state = 'owned' AND owner_address IS NOT NULL) OR
        (state = 'not_found' AND owner_address IS NULL)
    ),
    CHECK (confidence = 'rpc_exact')
);

CREATE INDEX erc721_owner_reconciliations_owner_idx
    ON erc721_owner_reconciliations
       (chain_id, owner_address, token_address, token_id, block_number DESC)
    WHERE state = 'owned';

CREATE TABLE erc1155_balance_reconciliations (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    token_address BYTEA NOT NULL,
    token_id NUMERIC(78, 0) NOT NULL,
    owner_address BYTEA NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    balance NUMERIC(78, 0) NOT NULL,
    confidence TEXT NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, token_address, token_id, owner_address, block_hash),
    FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash),
    CHECK (octet_length(token_address) = 20),
    CHECK (token_id >= 0),
    CHECK (octet_length(owner_address) = 20),
    CHECK (octet_length(block_hash) = 32),
    CHECK (balance >= 0),
    CHECK (confidence = 'rpc_exact')
);

CREATE INDEX erc1155_balance_reconciliations_owner_idx
    ON erc1155_balance_reconciliations
       (chain_id, owner_address, token_address, token_id, block_number DESC);
