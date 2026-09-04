CREATE TABLE erc20_holder_snapshots (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    token_address BYTEA NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    state TEXT NOT NULL,
    holder_count NUMERIC(78, 0) NOT NULL,
    total_supply NUMERIC(78, 0) NOT NULL,
    reconciled_balance_sum NUMERIC(78, 0) NOT NULL,
    canonical BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (chain_id, token_address, block_number, block_hash),
    FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash),
    CHECK (octet_length(token_address) = 20),
    CHECK (octet_length(block_hash) = 32),
    CHECK (state IN ('complete', 'unavailable')),
    CHECK (holder_count >= 0),
    CHECK (total_supply >= 0),
    CHECK (reconciled_balance_sum >= 0),
    CHECK ((state = 'complete' AND total_supply = reconciled_balance_sum) OR state = 'unavailable')
) PARTITION BY RANGE (block_number);

CREATE TABLE erc20_holder_snapshots_p_0_1000000
    PARTITION OF erc20_holder_snapshots FOR VALUES FROM (0) TO (1000000);
CREATE TABLE erc20_holder_snapshots_default
    PARTITION OF erc20_holder_snapshots DEFAULT;
CREATE INDEX erc20_holder_snapshots_current_idx
    ON erc20_holder_snapshots (chain_id, token_address, block_number DESC, block_hash)
    WHERE canonical;

CREATE TABLE erc20_holder_balances (
    chain_id NUMERIC(78, 0) NOT NULL,
    token_address BYTEA NOT NULL,
    holder_address BYTEA NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    balance NUMERIC(78, 0) NOT NULL,
    confidence TEXT NOT NULL,
    canonical BOOLEAN NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (chain_id, token_address, block_number, block_hash, holder_address),
    FOREIGN KEY (chain_id, token_address, block_number, block_hash)
        REFERENCES erc20_holder_snapshots(chain_id, token_address, block_number, block_hash),
    CHECK (octet_length(token_address) = 20),
    CHECK (octet_length(holder_address) = 20),
    CHECK (octet_length(block_hash) = 32),
    CHECK (balance >= 0),
    CHECK (confidence = 'rpc_exact')
) PARTITION BY RANGE (block_number);

CREATE TABLE erc20_holder_balances_p_0_1000000
    PARTITION OF erc20_holder_balances FOR VALUES FROM (0) TO (1000000);
CREATE TABLE erc20_holder_balances_default
    PARTITION OF erc20_holder_balances DEFAULT;
CREATE INDEX erc20_holder_balances_page_idx
    ON erc20_holder_balances (
        chain_id, token_address, holder_address, block_number DESC, block_hash
    ) WHERE canonical;

CREATE FUNCTION etherview_guard_erc20_holder_fact()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF (to_jsonb(NEW) - 'canonical') IS DISTINCT FROM (to_jsonb(OLD) - 'canonical') THEN
        RAISE EXCEPTION 'exact ERC-20 holder facts are immutable'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER erc20_holder_snapshots_immutable
BEFORE UPDATE ON erc20_holder_snapshots
FOR EACH ROW EXECUTE FUNCTION etherview_guard_erc20_holder_fact();

CREATE TRIGGER erc20_holder_balances_immutable
BEFORE UPDATE ON erc20_holder_balances
FOR EACH ROW EXECUTE FUNCTION etherview_guard_erc20_holder_fact();

DO $migration$
DECLARE
    migration_schema TEXT := current_schema();
BEGIN
    IF migration_schema IS NULL THEN
        RAISE EXCEPTION 'ERC-20 holder migration requires a current schema';
    END IF;
    EXECUTE format(
        'ALTER FUNCTION %I.etherview_guard_erc20_holder_fact() SET search_path = %I, pg_catalog',
        migration_schema,
        migration_schema
    );
END
$migration$;
