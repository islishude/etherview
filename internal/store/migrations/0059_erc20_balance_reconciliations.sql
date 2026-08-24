-- P20-T16 retains request-discovered ERC-20 balances as immutable exact-block
-- observations. Zero balances are facts too; no event delta or newer block may
-- overwrite an observation for a different block hash.
CREATE TABLE erc20_balance_reconciliations (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    token_address BYTEA NOT NULL,
    owner_address BYTEA NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    balance NUMERIC(78, 0) NOT NULL,
    confidence TEXT NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (chain_id, token_address, owner_address, block_hash),
    FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash),
    CHECK (octet_length(token_address) = 20),
    CHECK (octet_length(owner_address) = 20),
    CHECK (octet_length(block_hash) = 32),
    CHECK (
        balance BETWEEN 0 AND
        115792089237316195423570985008687907853269984665640564039457584007913129639935
    ),
    CHECK (confidence = 'rpc_exact')
);

CREATE INDEX erc20_balance_reconciliations_owner_snapshot_idx
    ON erc20_balance_reconciliations
       (chain_id, owner_address, block_number, block_hash, token_address);

CREATE FUNCTION etherview_guard_erc20_balance_reconciliation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'exact ERC-20 balance observations are immutable'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER erc20_balance_reconciliation_immutable
BEFORE UPDATE ON erc20_balance_reconciliations
FOR EACH ROW EXECUTE FUNCTION etherview_guard_erc20_balance_reconciliation();

DO $migration$
DECLARE
    migration_schema TEXT := current_schema();
BEGIN
    IF migration_schema IS NULL THEN
        RAISE EXCEPTION 'ERC-20 balance reconciliation migration requires a current schema';
    END IF;
    EXECUTE format(
        'ALTER FUNCTION %I.etherview_guard_erc20_balance_reconciliation() SET search_path = %I, pg_catalog',
        migration_schema,
        migration_schema
    );
END
$migration$;
