-- P30-T05: retain NFT metadata and source-discovery facts by exact block hash.
ALTER TABLE external_metadata
    ADD COLUMN identity_hash BYTEA;

UPDATE external_metadata
SET identity_hash = CASE
    WHEN resource_kind = 'nft' AND observed_block_hash IS NOT NULL
        THEN observed_block_hash
    ELSE '\x'::bytea
END;

ALTER TABLE external_metadata
    ALTER COLUMN identity_hash SET DEFAULT '\x'::bytea,
    ALTER COLUMN identity_hash SET NOT NULL,
    DROP CONSTRAINT external_metadata_pkey;

ALTER TABLE external_metadata
    ADD PRIMARY KEY (chain_id, resource_kind, resource_key, identity_hash),
    ADD CONSTRAINT external_metadata_identity_hash_check
        CHECK (
            (resource_kind = 'nft' AND observed_block_hash IS NOT NULL
                AND identity_hash = observed_block_hash)
            OR (resource_kind <> 'nft' AND identity_hash = '\x'::bytea)
            OR (resource_kind = 'nft' AND observed_block_hash IS NULL
                AND identity_hash = '\x'::bytea)
        ),
    ADD CONSTRAINT external_metadata_nft_uint256_check
        CHECK (
            resource_kind <> 'nft'
            OR token_id IS NULL
            OR token_id <= 115792089237316195423570985008687907853269984665640564039457584007913129639935
        ),
    ADD CONSTRAINT external_metadata_nft_source_uri_check
        CHECK (
            resource_kind <> 'nft'
            OR observed_block_hash IS NULL
            OR length(source_uri) BETWEEN 1 AND 4096
        ),
    ADD CONSTRAINT external_metadata_nft_attempt_bound_check
        CHECK (resource_kind <> 'nft' OR attempt_count BETWEEN 0 AND 100),
    ADD CONSTRAINT external_metadata_nft_outcome_check
        CHECK (
            resource_kind <> 'nft'
            OR observed_block_hash IS NULL
            OR (
                state = 'pending'
                AND resolved_uri IS NULL AND media_type IS NULL
                AND content_hash IS NULL AND document IS NULL AND content_size IS NULL
                AND terminal_at IS NULL
            )
            OR (
                state = 'available'
                AND resolved_uri IS NOT NULL AND media_type IS NOT NULL
                AND content_hash IS NOT NULL AND document IS NOT NULL AND content_size IS NOT NULL
                AND last_error_code IS NULL AND last_error IS NULL
                AND fetched_at IS NOT NULL AND terminal_at IS NOT NULL
            )
            OR (
                state IN ('unavailable', 'unsafe', 'error')
                AND resolved_uri IS NULL AND media_type IS NULL
                AND content_hash IS NULL AND document IS NULL AND content_size IS NULL
                AND last_error_code IS NOT NULL AND last_error IS NOT NULL
                AND fetched_at IS NOT NULL AND terminal_at IS NOT NULL
            )
        );

DROP INDEX external_metadata_nft_identity_uq;

CREATE UNIQUE INDEX external_metadata_nft_identity_uq
    ON external_metadata (chain_id, token_address, token_id, observed_block_hash)
    WHERE resource_kind = 'nft'
      AND token_address IS NOT NULL
      AND token_id IS NOT NULL
      AND observed_block_hash IS NOT NULL;

CREATE INDEX external_metadata_nft_canonical_lookup_idx
    ON external_metadata
       (chain_id, token_address, token_id, observed_block_number DESC, observed_block_hash);

CREATE OR REPLACE FUNCTION etherview_guard_exact_nft_metadata()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    IF OLD.resource_kind <> 'nft' OR OLD.observed_block_hash IS NULL THEN
        RETURN NEW;
    END IF;
    IF ROW(NEW.chain_id, NEW.resource_kind, NEW.resource_key, NEW.identity_hash,
           NEW.token_address, NEW.token_id, NEW.observed_block_number,
           NEW.observed_block_hash, NEW.source_uri)
       IS DISTINCT FROM
       ROW(OLD.chain_id, OLD.resource_kind, OLD.resource_key, OLD.identity_hash,
           OLD.token_address, OLD.token_id, OLD.observed_block_number,
           OLD.observed_block_hash, OLD.source_uri) THEN
        RAISE EXCEPTION 'exact NFT metadata identity is immutable'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF OLD.state <> 'pending' AND
       ROW(NEW.state, NEW.resolved_uri, NEW.media_type, NEW.content_hash,
           NEW.document, NEW.content_size, NEW.attempt_count,
           NEW.last_error_code, NEW.last_error, NEW.fetched_at, NEW.terminal_at)
       IS DISTINCT FROM
       ROW(OLD.state, OLD.resolved_uri, OLD.media_type, OLD.content_hash,
           OLD.document, OLD.content_size, OLD.attempt_count,
           OLD.last_error_code, OLD.last_error, OLD.fetched_at, OLD.terminal_at) THEN
        RAISE EXCEPTION 'terminal exact NFT metadata outcome is immutable'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER external_metadata_exact_nft_immutable
BEFORE UPDATE ON external_metadata
FOR EACH ROW EXECUTE FUNCTION etherview_guard_exact_nft_metadata();

CREATE TABLE nft_metadata_source_observations (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    token_address BYTEA NOT NULL,
    token_id NUMERIC(78, 0) NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    standard TEXT NOT NULL,
    state TEXT NOT NULL,
    source_uri TEXT,
    error_code TEXT,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, token_address, token_id, block_hash),
    FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash),
    CHECK (octet_length(token_address) = 20),
    CHECK (token_id BETWEEN 0 AND 115792089237316195423570985008687907853269984665640564039457584007913129639935),
    CHECK (octet_length(block_hash) = 32),
    CHECK (standard IN ('erc721', 'erc1155')),
    CHECK (state IN ('found', 'unavailable')),
    CHECK (source_uri IS NULL OR length(source_uri) BETWEEN 1 AND 4096),
    CHECK (error_code IS NULL OR length(error_code) BETWEEN 1 AND 64),
    CHECK (
        (state = 'found' AND source_uri IS NOT NULL AND error_code IS NULL)
        OR (state = 'unavailable' AND source_uri IS NULL AND error_code IS NOT NULL)
    )
);

CREATE INDEX nft_metadata_source_candidate_idx
    ON nft_metadata_source_observations
       (chain_id, token_address, token_id, block_number DESC, block_hash);

CREATE OR REPLACE FUNCTION etherview_guard_nft_metadata_source()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    IF NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'exact NFT metadata source observation is immutable'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER nft_metadata_source_observation_immutable
BEFORE UPDATE ON nft_metadata_source_observations
FOR EACH ROW EXECUTE FUNCTION etherview_guard_nft_metadata_source();
