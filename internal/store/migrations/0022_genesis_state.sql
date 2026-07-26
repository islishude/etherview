CREATE TABLE IF NOT EXISTS genesis_state_imports (
    chain_id NUMERIC(78, 0) PRIMARY KEY REFERENCES chains(chain_id),
    block_hash BYTEA,
    state_root BYTEA,
    document_sha256 BYTEA,
    state TEXT NOT NULL,
    account_count NUMERIC(78, 0),
    last_error_code TEXT,
    imported_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (chain_id, block_hash),
    FOREIGN KEY (chain_id, block_hash)
        REFERENCES blocks(chain_id, hash),
    CHECK (block_hash IS NULL OR octet_length(block_hash) = 32),
    CHECK (state_root IS NULL OR octet_length(state_root) = 32),
    CHECK (document_sha256 IS NULL OR octet_length(document_sha256) = 32),
    CHECK (state IN ('pending', 'complete', 'unavailable', 'failed')),
    CHECK (account_count IS NULL OR account_count >= 0),
    CHECK (
        (state = 'complete' AND block_hash IS NOT NULL AND state_root IS NOT NULL
            AND document_sha256 IS NOT NULL AND account_count IS NOT NULL
            AND imported_at IS NOT NULL AND last_error_code IS NULL)
        OR
        (state <> 'complete' AND account_count IS NULL AND imported_at IS NULL)
    )
);

CREATE TABLE IF NOT EXISTS genesis_account_observations (
    chain_id NUMERIC(78, 0) NOT NULL,
    address BYTEA NOT NULL,
    block_hash BYTEA NOT NULL,
    balance NUMERIC(78, 0) NOT NULL,
    nonce NUMERIC(78, 0) NOT NULL,
    code_hash BYTEA NOT NULL,
    code BYTEA NOT NULL,
    storage_root BYTEA NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, address, block_hash),
    FOREIGN KEY (chain_id, block_hash)
        REFERENCES genesis_state_imports(chain_id, block_hash),
    CHECK (octet_length(address) = 20),
    CHECK (octet_length(block_hash) = 32),
    CHECK (balance >= 0),
    CHECK (nonce >= 0),
    CHECK (octet_length(code_hash) = 32),
    CHECK (octet_length(storage_root) = 32)
);

CREATE INDEX IF NOT EXISTS genesis_account_observations_page_idx
    ON genesis_account_observations (chain_id, block_hash, address);

CREATE OR REPLACE FUNCTION etherview_guard_completed_genesis_import()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    IF OLD.state = 'complete' AND NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'completed genesis state import is immutable'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER genesis_state_import_complete_immutable
BEFORE UPDATE ON genesis_state_imports
FOR EACH ROW EXECUTE FUNCTION etherview_guard_completed_genesis_import();

CREATE OR REPLACE FUNCTION etherview_guard_genesis_account_observation()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    IF NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'exact genesis account observation is immutable'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER genesis_account_observation_immutable
BEFORE UPDATE ON genesis_account_observations
FOR EACH ROW EXECUTE FUNCTION etherview_guard_genesis_account_observation();
