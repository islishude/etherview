-- P67 replaces the generic HTTPS name adapter with snapshot-pinned official
-- and custom ENS resolution. Active development targets the fresh schema; no
-- legacy name rows or adapter compatibility state is retained.

DROP TRIGGER IF EXISTS name_records_search_catalog_trigger ON name_records;
DROP TABLE IF EXISTS name_records;

ALTER TABLE search_catalog_documents
    ADD COLUMN IF NOT EXISTS name_observation_id BIGINT,
    ADD COLUMN IF NOT EXISTS name_source TEXT;

ALTER TABLE search_catalog_documents
    ADD CONSTRAINT search_catalog_documents_name_source_check CHECK (
        (source_kind = 'name' AND name_observation_id IS NOT NULL AND name_source IN ('ens', 'custom_ens'))
        OR
        (source_kind <> 'name' AND name_observation_id IS NULL AND name_source IS NULL)
    ) NOT VALID;

CREATE TABLE ens_resolution_generations (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    policy_key TEXT NOT NULL,
    coin_type NUMERIC(78, 0) NOT NULL,
    official_endpoint TEXT NOT NULL,
    official_block_number NUMERIC(78, 0) NOT NULL,
    official_block_hash BYTEA NOT NULL,
    custom_endpoint TEXT,
    custom_coin_type NUMERIC(78, 0),
    custom_block_number NUMERIC(78, 0),
    custom_block_hash BYTEA,
    created_at TIMESTAMPTZ NOT NULL,
    fresh_until TIMESTAMPTZ NOT NULL,
    retain_until TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (chain_id, custom_block_number, custom_block_hash)
        REFERENCES blocks(chain_id, number, hash),
    CHECK (length(policy_key) BETWEEN 8 AND 128),
    CHECK (coin_type > 0),
    CHECK (length(official_endpoint) BETWEEN 1 AND 128),
    CHECK (official_block_number >= 0),
    CHECK (octet_length(official_block_hash) = 32),
    CHECK ((custom_endpoint IS NULL) = (custom_block_number IS NULL)),
    CHECK ((custom_endpoint IS NULL) = (custom_block_hash IS NULL)),
    CHECK ((custom_endpoint IS NULL) = (custom_coin_type IS NULL)),
    CHECK (custom_endpoint IS NULL OR length(custom_endpoint) BETWEEN 1 AND 128),
    CHECK (custom_coin_type IS NULL OR custom_coin_type > 0),
    CHECK (custom_block_number IS NULL OR custom_block_number >= 0),
    CHECK (custom_block_hash IS NULL OR octet_length(custom_block_hash) = 32),
    CHECK (fresh_until > created_at),
    CHECK (retain_until >= fresh_until)
);

CREATE INDEX ens_resolution_generations_current_idx
    ON ens_resolution_generations (chain_id, policy_key, fresh_until DESC, id DESC);

CREATE INDEX ens_resolution_generations_retention_idx
    ON ens_resolution_generations (retain_until, id);

CREATE TABLE ens_name_observations (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    generation_id BIGINT NOT NULL REFERENCES ens_resolution_generations(id) ON DELETE CASCADE,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    source TEXT NOT NULL,
    direction TEXT NOT NULL,
    lookup_key TEXT NOT NULL,
    outcome TEXT NOT NULL,
    name TEXT,
    address BYTEA,
    resolver BYTEA,
    reverse_resolver BYTEA,
    observed_at TIMESTAMPTZ NOT NULL,
    publication_nonce BIGINT NOT NULL DEFAULT 0,
    UNIQUE (generation_id, source, direction, lookup_key),
    CHECK (source IN ('ens', 'custom_ens')),
    CHECK (direction IN ('forward', 'primary')),
    CHECK (length(lookup_key) BETWEEN 1 AND 512),
    CHECK (outcome IN ('resolved', 'not_found')),
    CHECK (name IS NULL OR length(name) BETWEEN 1 AND 255),
    CHECK (address IS NULL OR octet_length(address) = 20),
    CHECK (resolver IS NULL OR octet_length(resolver) = 20),
    CHECK (reverse_resolver IS NULL OR octet_length(reverse_resolver) = 20),
    CHECK (publication_nonce >= 0),
    CHECK (
        (direction = 'forward' AND outcome = 'resolved' AND name IS NOT NULL AND address IS NOT NULL AND resolver IS NOT NULL AND reverse_resolver IS NULL)
        OR (direction = 'forward' AND outcome = 'not_found' AND name IS NOT NULL AND address IS NULL AND resolver IS NULL AND reverse_resolver IS NULL)
        OR (direction = 'primary' AND outcome = 'resolved' AND name IS NOT NULL AND address IS NOT NULL AND resolver IS NOT NULL AND reverse_resolver IS NOT NULL)
        OR (direction = 'primary' AND outcome = 'not_found' AND name IS NULL AND address IS NOT NULL AND resolver IS NULL AND reverse_resolver IS NULL)
    )
);

CREATE INDEX ens_name_observations_address_idx
    ON ens_name_observations (generation_id, source, direction, address)
    WHERE direction = 'primary';

CREATE TABLE ens_resolution_failures (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    generation_id BIGINT NOT NULL REFERENCES ens_resolution_generations(id) ON DELETE CASCADE,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    source TEXT NOT NULL,
    direction TEXT NOT NULL,
    lookup_key TEXT NOT NULL,
    code TEXT NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    CHECK (source IN ('ens', 'custom_ens')),
    CHECK (direction IN ('forward', 'primary')),
    CHECK (length(lookup_key) BETWEEN 1 AND 512),
    CHECK (length(code) BETWEEN 1 AND 128),
    CHECK (expires_at > observed_at)
);

CREATE INDEX ens_resolution_failures_fresh_idx
    ON ens_resolution_failures (
        generation_id, source, direction, lookup_key, expires_at DESC, id DESC
    );

CREATE TABLE ens_address_name_snapshots (
    id UUID PRIMARY KEY,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    generation_id BIGINT NOT NULL REFERENCES ens_resolution_generations(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    CHECK (expires_at > created_at)
);

CREATE INDEX ens_address_name_snapshots_expiry_idx
    ON ens_address_name_snapshots (expires_at, id);

CREATE OR REPLACE FUNCTION record_ens_name_search_document()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    next_generation BIGINT;
    local_number NUMERIC(78, 0);
    local_hash BYTEA;
    local_canonical BOOLEAN;
BEGIN
    IF NEW.direction <> 'forward' THEN
        RETURN NEW;
    END IF;

    INSERT INTO search_catalog_generations (chain_id)
    VALUES (NEW.chain_id)
    ON CONFLICT (chain_id) DO NOTHING;
    UPDATE search_catalog_generations
    SET generation = generation + 1, updated_at = now()
    WHERE chain_id = NEW.chain_id
    RETURNING generation INTO next_generation;

    UPDATE search_catalog_documents
    SET valid_to_generation = next_generation
    WHERE chain_id = NEW.chain_id
      AND source_kind = 'name'
      AND logical_identity = lower(NEW.name)
      AND valid_to_generation IS NULL;

    IF NEW.outcome = 'not_found' THEN
        RETURN NEW;
    END IF;

    IF NEW.source = 'custom_ens' THEN
        SELECT source.custom_block_number, source.custom_block_hash,
               EXISTS (
                   SELECT 1 FROM canonical_blocks AS canonical
                   WHERE canonical.chain_id = source.chain_id
                     AND canonical.number = source.custom_block_number
                     AND canonical.block_hash = source.custom_block_hash
               )
        INTO local_number, local_hash, local_canonical
        FROM ens_resolution_generations AS source
        WHERE source.id = NEW.generation_id;
    ELSE
        local_canonical := TRUE;
    END IF;

    INSERT INTO search_catalog_documents (
        chain_id, source_kind, source_identity, logical_identity,
        valid_from_generation, result_kind, result_key, result_label,
        exact_terms, partial_terms, block_number, block_hash, target_address,
        source_canonical, name_observation_id, name_source
    ) VALUES (
        NEW.chain_id, 'name', NEW.id::text, lower(NEW.name), next_generation,
        'address', '0x' || encode(NEW.address, 'hex'), NEW.name,
        ARRAY[lower(NEW.name)], ARRAY[lower(NEW.name)],
        local_number, local_hash, NEW.address, local_canonical,
        NEW.id, NEW.source
    );
    RETURN NEW;
END
$$;

ALTER FUNCTION record_ens_name_search_document() SET search_path FROM CURRENT;

CREATE TRIGGER ens_name_observations_search_catalog_trigger
AFTER INSERT OR UPDATE OF publication_nonce ON ens_name_observations
FOR EACH ROW EXECUTE FUNCTION record_ens_name_search_document();
