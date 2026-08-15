-- Normalize verified function selectors so read paths never need to fetch and
-- parse complete ABI documents.  One durable set records that a verification
-- result was processed even when it contains no functions or is unusable.
ALTER TABLE verified_contracts
    ADD CONSTRAINT verified_contracts_selector_identity_unique UNIQUE (
        chain_id, address, code_hash, valid_from_block, verification_job_id
    );

CREATE TABLE verified_function_selector_sets (
    verification_job_id UUID PRIMARY KEY,
    request_digest BYTEA NOT NULL,
    chain_id NUMERIC(78, 0) NOT NULL,
    address BYTEA NOT NULL,
    code_hash BYTEA NOT NULL,
    valid_from_block NUMERIC(78, 0) NOT NULL,
    status TEXT NOT NULL,
    function_count INTEGER NOT NULL,
    warning TEXT NOT NULL DEFAULT '',
    indexed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT verified_function_selector_sets_result_fk FOREIGN KEY (
        verification_job_id, request_digest
    ) REFERENCES verification_results (job_id, request_digest) ON DELETE RESTRICT,
    CONSTRAINT verified_function_selector_sets_contract_fk FOREIGN KEY (
        chain_id, address, code_hash, valid_from_block, verification_job_id
    ) REFERENCES verified_contracts (
        chain_id, address, code_hash, valid_from_block, verification_job_id
    ) ON DELETE CASCADE,
    CONSTRAINT verified_function_selector_sets_identity_check CHECK (
        octet_length(request_digest) = 32 AND
        octet_length(address) = 20 AND octet_length(code_hash) = 32 AND
        valid_from_block >= 0
    ),
    CONSTRAINT verified_function_selector_sets_status_check CHECK (
        status IN ('complete', 'invalid') AND function_count >= 0 AND
        ((status = 'complete' AND warning = '') OR
         (status = 'invalid' AND function_count = 0 AND warning <> ''))
    )
);

CREATE INDEX verified_function_selector_sets_identity_idx
    ON verified_function_selector_sets (
        chain_id, code_hash, address, valid_from_block, verification_job_id
    );

ALTER TABLE verified_function_selector_sets
    ADD CONSTRAINT verified_function_selector_sets_lookup_identity_unique
    UNIQUE (verification_job_id, chain_id, address, code_hash);

CREATE TABLE verified_function_selectors (
    verification_job_id UUID NOT NULL,
    chain_id NUMERIC(78, 0) NOT NULL,
    address BYTEA NOT NULL,
    code_hash BYTEA NOT NULL,
    selector BYTEA NOT NULL,
    signature TEXT NOT NULL,
    function_name TEXT NOT NULL,
    abi_entry JSONB NOT NULL,
    PRIMARY KEY (verification_job_id, selector, signature),
    CONSTRAINT verified_function_selectors_set_fk FOREIGN KEY (
        verification_job_id, chain_id, address, code_hash
    ) REFERENCES verified_function_selector_sets (
        verification_job_id, chain_id, address, code_hash
    )
        ON DELETE CASCADE,
    CONSTRAINT verified_function_selectors_shape_check CHECK (
        octet_length(address) = 20 AND octet_length(code_hash) = 32 AND
        octet_length(selector) = 4 AND
        length(signature) BETWEEN 1 AND 4096 AND
        length(function_name) BETWEEN 1 AND 4096 AND
        jsonb_typeof(abi_entry) = 'object' AND
        abi_entry->>'type' = 'function'
    )
);

CREATE INDEX verified_function_selectors_lookup_idx
    ON verified_function_selectors (
        chain_id, code_hash, selector, verification_job_id, signature
    );
