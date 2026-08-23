-- P64-T04 records standard NFT metadata-update signals as immutable exact-log
-- facts. The event payload is only a refresh trigger; tokenURI/uri remains the
-- source authority at the exact event block hash.
CREATE TABLE nft_metadata_update_observations (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    log_index BIGINT NOT NULL,
    token_address BYTEA NOT NULL,
    standard TEXT NOT NULL,
    event_kind TEXT NOT NULL,
    state TEXT NOT NULL,
    from_token_id NUMERIC(78, 0),
    to_token_id NUMERIC(78, 0),
    error_code TEXT,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (chain_id, block_number, block_hash, log_index),
    FOREIGN KEY (chain_id, block_number, block_hash, log_index)
        REFERENCES logs(chain_id, block_number, block_hash, log_index),
    CHECK (octet_length(block_hash) = 32),
    CHECK (log_index >= 0),
    CHECK (octet_length(token_address) = 20),
    CHECK (standard IN ('erc721', 'erc1155')),
    CHECK (event_kind IN ('erc4906_single', 'erc4906_batch', 'erc1155_uri')),
    CHECK (state IN ('accepted', 'malformed')),
    CHECK (from_token_id IS NULL OR from_token_id BETWEEN 0 AND 115792089237316195423570985008687907853269984665640564039457584007913129639935),
    CHECK (to_token_id IS NULL OR to_token_id BETWEEN 0 AND 115792089237316195423570985008687907853269984665640564039457584007913129639935),
    CHECK (error_code IS NULL OR length(error_code) BETWEEN 1 AND 64),
    CHECK (
        (
            state = 'accepted' AND error_code IS NULL AND
            from_token_id IS NOT NULL AND to_token_id IS NOT NULL AND
            from_token_id <= to_token_id AND
            (
                (event_kind IN ('erc4906_single', 'erc1155_uri') AND from_token_id = to_token_id)
                OR event_kind = 'erc4906_batch'
            ) AND
            (
                (event_kind IN ('erc4906_single', 'erc4906_batch') AND standard = 'erc721')
                OR (event_kind = 'erc1155_uri' AND standard = 'erc1155')
            )
        ) OR (
            state = 'malformed' AND error_code IS NOT NULL AND
            from_token_id IS NULL AND to_token_id IS NULL
        )
    )
);

CREATE INDEX nft_metadata_update_candidate_idx
    ON nft_metadata_update_observations
       (chain_id, token_address, block_number DESC, block_hash, from_token_id, to_token_id)
    WHERE state = 'accepted';

CREATE INDEX token_events_metadata_known_id_idx
    ON token_events
       (chain_id, token_address, token_id, block_number DESC, block_hash)
    WHERE token_id IS NOT NULL AND standard IN ('erc721', 'erc1155');

CREATE FUNCTION etherview_guard_nft_metadata_update_observation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'exact NFT metadata update observation is immutable'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER nft_metadata_update_observation_immutable
BEFORE UPDATE ON nft_metadata_update_observations
FOR EACH ROW EXECUTE FUNCTION etherview_guard_nft_metadata_update_observation();

DO $migration$
DECLARE
    migration_schema TEXT := current_schema();
BEGIN
    IF migration_schema IS NULL THEN
        RAISE EXCEPTION 'NFT metadata update migration requires a current schema';
    END IF;
    EXECUTE format(
        'ALTER FUNCTION %I.etherview_guard_nft_metadata_update_observation() SET search_path = %I, pg_catalog',
        migration_schema,
        migration_schema
    );
END
$migration$;
