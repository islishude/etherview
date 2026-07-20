-- P20-T06 extends statistics through a new stats@2 contract and records
-- bounded optional-adapter observations without making either adapter a
-- correctness dependency of core ingestion.

ALTER TABLE block_statistics
    ADD COLUMN IF NOT EXISTS block_timestamp NUMERIC(78, 0),
    ADD COLUMN IF NOT EXISTS block_interval_seconds NUMERIC(78, 0),
    ADD COLUMN IF NOT EXISTS transactions_per_second NUMERIC(78, 18),
    ADD COLUMN IF NOT EXISTS excess_blob_gas NUMERIC(78, 0),
    ADD COLUMN IF NOT EXISTS blob_base_fee_per_gas NUMERIC(78, 0),
    ADD COLUMN IF NOT EXISTS blob_burned_wei NUMERIC(78, 0);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'block_statistics_v2_nonnegative_check'
          AND conrelid = 'block_statistics'::regclass
    ) THEN
        ALTER TABLE block_statistics
            ADD CONSTRAINT block_statistics_v2_nonnegative_check CHECK (
                block_timestamp IS NULL OR block_timestamp >= 0
            ) NOT VALID;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'block_statistics_v2_interval_check'
          AND conrelid = 'block_statistics'::regclass
    ) THEN
        ALTER TABLE block_statistics
            ADD CONSTRAINT block_statistics_v2_interval_check CHECK (
                block_interval_seconds IS NULL OR block_interval_seconds > 0
            ) NOT VALID;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'block_statistics_v2_tps_check'
          AND conrelid = 'block_statistics'::regclass
    ) THEN
        ALTER TABLE block_statistics
            ADD CONSTRAINT block_statistics_v2_tps_check CHECK (
                transactions_per_second IS NULL OR transactions_per_second >= 0
            ) NOT VALID;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'block_statistics_v2_blob_check'
          AND conrelid = 'block_statistics'::regclass
    ) THEN
        ALTER TABLE block_statistics
            ADD CONSTRAINT block_statistics_v2_blob_check CHECK (
                (excess_blob_gas IS NULL OR excess_blob_gas >= 0)
                AND (blob_base_fee_per_gas IS NULL OR blob_base_fee_per_gas >= 0)
                AND (blob_burned_wei IS NULL OR blob_burned_wei >= 0)
            ) NOT VALID;
    END IF;
END
$$;

ALTER TABLE block_statistics VALIDATE CONSTRAINT block_statistics_v2_nonnegative_check;
ALTER TABLE block_statistics VALIDATE CONSTRAINT block_statistics_v2_interval_check;
ALTER TABLE block_statistics VALIDATE CONSTRAINT block_statistics_v2_tps_check;
ALTER TABLE block_statistics VALIDATE CONSTRAINT block_statistics_v2_blob_check;

ALTER TABLE name_records
    ADD COLUMN IF NOT EXISTS observed_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- Search pagination cannot use only the canonical tip as its snapshot: labels
-- and late enrichment can change without a reorg. Keep an immutable, per-chain
-- generation history for every searchable document and every code observation
-- used to qualify a verified contract. Writers serialize generation assignment
-- through one row, while readers can reproduce any retained generation.
CREATE TABLE IF NOT EXISTS search_catalog_generations (
    chain_id NUMERIC(78, 0) PRIMARY KEY REFERENCES chains(chain_id),
    generation BIGINT NOT NULL DEFAULT 0,
    min_generation BIGINT NOT NULL DEFAULT 0,
    retention_generations BIGINT NOT NULL DEFAULT 100000,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (generation >= 0),
    CHECK (min_generation >= 0 AND min_generation <= generation),
    CHECK (retention_generations BETWEEN 1000 AND 10000000)
);

CREATE TABLE IF NOT EXISTS search_catalog_documents (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    source_kind TEXT NOT NULL,
    source_identity TEXT NOT NULL,
    logical_identity TEXT NOT NULL,
    valid_from_generation BIGINT NOT NULL,
    valid_to_generation BIGINT,
    result_kind TEXT,
    result_key TEXT,
    result_label TEXT,
    exact_terms TEXT[] NOT NULL DEFAULT '{}',
    partial_terms TEXT[] NOT NULL DEFAULT '{}',
    block_number NUMERIC(78, 0),
    block_hash BYTEA,
    target_address BYTEA,
    code_hash BYTEA,
    valid_from_block NUMERIC(78, 0),
    valid_to_block NUMERIC(78, 0),
    source_canonical BOOLEAN,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (chain_id, source_kind, source_identity, valid_from_generation),
    CHECK (source_kind IN ('label', 'name', 'token', 'verified_contract', 'code')),
    CHECK (length(source_identity) BETWEEN 1 AND 1024),
    CHECK (length(logical_identity) BETWEEN 1 AND 1024),
    CHECK (valid_from_generation >= 1),
    CHECK (valid_to_generation IS NULL OR valid_to_generation > valid_from_generation),
    CHECK ((result_kind IS NULL) = (result_key IS NULL)),
    CHECK ((result_kind IS NULL) = (result_label IS NULL)),
    CHECK (result_kind IS NULL OR result_kind IN ('address', 'block', 'transaction', 'token', 'contract')),
    CHECK (result_key IS NULL OR length(result_key) BETWEEN 1 AND 256),
    CHECK (result_label IS NULL OR length(result_label) BETWEEN 1 AND 4096),
    CHECK ((block_number IS NULL) = (block_hash IS NULL)),
    CHECK (block_hash IS NULL OR octet_length(block_hash) = 32),
    CHECK (target_address IS NULL OR octet_length(target_address) = 20),
    CHECK (code_hash IS NULL OR octet_length(code_hash) = 32),
    CHECK (valid_to_block IS NULL OR valid_from_block IS NOT NULL),
    CHECK (valid_to_block IS NULL OR valid_to_block >= valid_from_block),
    CHECK (
        (source_kind = 'code' AND result_kind IS NULL AND target_address IS NOT NULL AND code_hash IS NOT NULL)
        OR
        (source_kind <> 'code' AND result_kind IS NOT NULL)
    )
);

CREATE INDEX IF NOT EXISTS search_catalog_documents_generation_idx
    ON search_catalog_documents (chain_id, valid_from_generation, valid_to_generation, source_kind);

CREATE INDEX IF NOT EXISTS search_catalog_documents_terms_idx
    ON search_catalog_documents USING GIN (exact_terms);

CREATE OR REPLACE FUNCTION record_search_catalog_document()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    source_chain NUMERIC(78, 0);
    old_identity TEXT;
    new_identity TEXT;
    logical_identity_value TEXT;
    next_generation BIGINT;
BEGIN
    IF TG_OP = 'UPDATE' AND OLD.chain_id IS DISTINCT FROM NEW.chain_id THEN
        RAISE EXCEPTION 'search catalog source chain_id is immutable';
    END IF;
    IF TG_OP = 'DELETE' THEN
        source_chain := OLD.chain_id;
    ELSE
        source_chain := NEW.chain_id;
    END IF;

    IF TG_TABLE_NAME = 'operator_labels' THEN
        IF TG_OP <> 'INSERT' THEN old_identity := OLD.id::text; END IF;
        IF TG_OP <> 'DELETE' THEN
            new_identity := NEW.id::text;
            logical_identity_value := NEW.id::text;
        END IF;
    ELSIF TG_TABLE_NAME = 'name_records' THEN
        IF TG_OP <> 'INSERT' THEN
            old_identity := jsonb_build_array(encode(OLD.registry, 'hex'), OLD.name, encode(OLD.block_hash, 'hex'))::text;
        END IF;
        IF TG_OP <> 'DELETE' THEN
            new_identity := jsonb_build_array(encode(NEW.registry, 'hex'), NEW.name, encode(NEW.block_hash, 'hex'))::text;
            logical_identity_value := jsonb_build_array(encode(NEW.registry, 'hex'), lower(NEW.name))::text;
        END IF;
    ELSIF TG_TABLE_NAME = 'token_contracts' THEN
        IF TG_OP <> 'INSERT' THEN
            old_identity := jsonb_build_array(encode(OLD.address, 'hex'), encode(OLD.code_hash, 'hex'), encode(OLD.observed_block_hash, 'hex'))::text;
        END IF;
        IF TG_OP <> 'DELETE' THEN
            new_identity := jsonb_build_array(encode(NEW.address, 'hex'), encode(NEW.code_hash, 'hex'), encode(NEW.observed_block_hash, 'hex'))::text;
            logical_identity_value := encode(NEW.address, 'hex');
        END IF;
    ELSIF TG_TABLE_NAME = 'verified_contracts' THEN
        IF TG_OP <> 'INSERT' THEN
            old_identity := jsonb_build_array(encode(OLD.address, 'hex'), encode(OLD.code_hash, 'hex'), OLD.valid_from_block::text)::text;
        END IF;
        IF TG_OP <> 'DELETE' THEN
            new_identity := jsonb_build_array(encode(NEW.address, 'hex'), encode(NEW.code_hash, 'hex'), NEW.valid_from_block::text)::text;
            logical_identity_value := new_identity;
        END IF;
    ELSIF TG_TABLE_NAME = 'contract_code_observations' THEN
        IF TG_OP <> 'INSERT' THEN
            old_identity := jsonb_build_array(encode(OLD.address, 'hex'), encode(OLD.block_hash, 'hex'))::text;
        END IF;
        IF TG_OP <> 'DELETE' THEN
            new_identity := jsonb_build_array(encode(NEW.address, 'hex'), encode(NEW.block_hash, 'hex'))::text;
            logical_identity_value := encode(NEW.address, 'hex');
        END IF;
    ELSE
        RAISE EXCEPTION 'unsupported search catalog source table %', TG_TABLE_NAME;
    END IF;

    INSERT INTO search_catalog_generations (chain_id)
    VALUES (source_chain)
    ON CONFLICT (chain_id) DO NOTHING;
    UPDATE search_catalog_generations
    SET generation = generation + 1, updated_at = now()
    WHERE chain_id = source_chain
    RETURNING generation INTO next_generation;

    UPDATE search_catalog_documents
    SET valid_to_generation = next_generation
    WHERE chain_id = source_chain
      AND source_kind = TG_ARGV[0]
      AND source_identity = old_identity
      AND valid_to_generation IS NULL;

    IF TG_OP = 'UPDATE' AND old_identity IS DISTINCT FROM new_identity THEN
        UPDATE search_catalog_documents
        SET valid_to_generation = next_generation
        WHERE chain_id = source_chain
          AND source_kind = TG_ARGV[0]
          AND source_identity = new_identity
          AND valid_to_generation IS NULL;
    END IF;

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;

    IF TG_TABLE_NAME = 'operator_labels' THEN
        INSERT INTO search_catalog_documents (
            chain_id, source_kind, source_identity, logical_identity, valid_from_generation,
            result_kind, result_key, result_label, exact_terms, partial_terms
        ) VALUES (
            NEW.chain_id, 'label', new_identity, logical_identity_value, next_generation,
            NEW.object_kind, NEW.object_key, NEW.label,
            ARRAY[lower(NEW.label), lower(NEW.object_key)], ARRAY[lower(NEW.label)]
        );
    ELSIF TG_TABLE_NAME = 'name_records' THEN
        INSERT INTO search_catalog_documents (
            chain_id, source_kind, source_identity, logical_identity, valid_from_generation,
            result_kind, result_key, result_label, exact_terms, partial_terms,
            block_number, block_hash, target_address, source_canonical
        ) VALUES (
            NEW.chain_id, 'name', new_identity, logical_identity_value, next_generation,
            'address', '0x' || encode(NEW.address, 'hex'), NEW.name,
            ARRAY[lower(NEW.name)], ARRAY[lower(NEW.name)],
            NEW.block_number, NEW.block_hash, NEW.address, NEW.canonical
        );
    ELSIF TG_TABLE_NAME = 'token_contracts' THEN
        INSERT INTO search_catalog_documents (
            chain_id, source_kind, source_identity, logical_identity, valid_from_generation,
            result_kind, result_key, result_label, exact_terms, partial_terms,
            block_number, block_hash, target_address, code_hash, source_canonical
        ) VALUES (
            NEW.chain_id, 'token', new_identity, logical_identity_value, next_generation,
            'token', '0x' || encode(NEW.address, 'hex'),
            COALESCE(NULLIF(NEW.name, ''), NULLIF(NEW.symbol, ''), 'Token 0x' || encode(NEW.address, 'hex')),
            array_remove(ARRAY[lower('0x' || encode(NEW.address, 'hex')), lower(COALESCE(NEW.name, '')), lower(COALESCE(NEW.symbol, ''))], ''),
            array_remove(ARRAY[lower(COALESCE(NEW.name, '')), lower(COALESCE(NEW.symbol, ''))], ''),
            NEW.observed_block_number, NEW.observed_block_hash, NEW.address, NEW.code_hash, TRUE
        );
    ELSIF TG_TABLE_NAME = 'verified_contracts' THEN
        INSERT INTO search_catalog_documents (
            chain_id, source_kind, source_identity, logical_identity, valid_from_generation,
            result_kind, result_key, result_label, exact_terms, partial_terms,
            target_address, code_hash, valid_from_block, valid_to_block
        ) VALUES (
            NEW.chain_id, 'verified_contract', new_identity, logical_identity_value, next_generation,
            'contract', '0x' || encode(NEW.address, 'hex'), NEW.contract_name,
            ARRAY[lower('0x' || encode(NEW.address, 'hex')), lower(NEW.contract_name)],
            ARRAY[lower(NEW.contract_name)], NEW.address, NEW.code_hash,
            NEW.valid_from_block, NEW.valid_to_block
        );
    ELSIF TG_TABLE_NAME = 'contract_code_observations' THEN
        INSERT INTO search_catalog_documents (
            chain_id, source_kind, source_identity, logical_identity, valid_from_generation,
            block_number, block_hash, target_address, code_hash, source_canonical
        ) VALUES (
            NEW.chain_id, 'code', new_identity, logical_identity_value, next_generation,
            NEW.block_number, NEW.block_hash, NEW.address, NEW.code_hash, NEW.canonical
        );
    END IF;
    RETURN NEW;
END
$$;

INSERT INTO search_catalog_generations (chain_id, generation)
SELECT chain_id, 1 FROM chains
ON CONFLICT (chain_id) DO NOTHING;

INSERT INTO search_catalog_documents (
    chain_id, source_kind, source_identity, logical_identity, valid_from_generation,
    result_kind, result_key, result_label, exact_terms, partial_terms
)
SELECT chain_id, 'label', id::text, id::text, 1, object_kind, object_key, label,
       ARRAY[lower(label), lower(object_key)], ARRAY[lower(label)]
FROM operator_labels
ON CONFLICT DO NOTHING;

INSERT INTO search_catalog_documents (
    chain_id, source_kind, source_identity, logical_identity, valid_from_generation,
    result_kind, result_key, result_label, exact_terms, partial_terms,
    block_number, block_hash, target_address, source_canonical
)
SELECT chain_id, 'name', jsonb_build_array(encode(registry, 'hex'), name, encode(block_hash, 'hex'))::text,
       jsonb_build_array(encode(registry, 'hex'), lower(name))::text,
       1, 'address', '0x' || encode(address, 'hex'), name,
       ARRAY[lower(name)], ARRAY[lower(name)], block_number, block_hash, address, canonical
FROM name_records
ON CONFLICT DO NOTHING;

INSERT INTO search_catalog_documents (
    chain_id, source_kind, source_identity, logical_identity, valid_from_generation,
    result_kind, result_key, result_label, exact_terms, partial_terms,
    block_number, block_hash, target_address, code_hash, source_canonical
)
SELECT chain_id, 'token', jsonb_build_array(encode(address, 'hex'), encode(code_hash, 'hex'), encode(observed_block_hash, 'hex'))::text,
       encode(address, 'hex'), 1, 'token', '0x' || encode(address, 'hex'),
       COALESCE(NULLIF(name, ''), NULLIF(symbol, ''), 'Token 0x' || encode(address, 'hex')),
       array_remove(ARRAY[lower('0x' || encode(address, 'hex')), lower(COALESCE(name, '')), lower(COALESCE(symbol, ''))], ''),
       array_remove(ARRAY[lower(COALESCE(name, '')), lower(COALESCE(symbol, ''))], ''),
       observed_block_number, observed_block_hash, address, code_hash, TRUE
FROM token_contracts
ON CONFLICT DO NOTHING;

INSERT INTO search_catalog_documents (
    chain_id, source_kind, source_identity, logical_identity, valid_from_generation,
    result_kind, result_key, result_label, exact_terms, partial_terms,
    target_address, code_hash, valid_from_block, valid_to_block
)
SELECT chain_id, 'verified_contract', jsonb_build_array(encode(address, 'hex'), encode(code_hash, 'hex'), valid_from_block::text)::text,
       jsonb_build_array(encode(address, 'hex'), encode(code_hash, 'hex'), valid_from_block::text)::text,
       1, 'contract', '0x' || encode(address, 'hex'), contract_name,
       ARRAY[lower('0x' || encode(address, 'hex')), lower(contract_name)],
       ARRAY[lower(contract_name)], address, code_hash, valid_from_block, valid_to_block
FROM verified_contracts
ON CONFLICT DO NOTHING;

INSERT INTO search_catalog_documents (
    chain_id, source_kind, source_identity, logical_identity, valid_from_generation,
    block_number, block_hash, target_address, code_hash, source_canonical
)
SELECT chain_id, 'code', jsonb_build_array(encode(address, 'hex'), encode(block_hash, 'hex'))::text,
       encode(address, 'hex'), 1, block_number, block_hash, address, code_hash, canonical
FROM contract_code_observations
ON CONFLICT DO NOTHING;

DROP TRIGGER IF EXISTS operator_labels_search_catalog_trigger ON operator_labels;
CREATE TRIGGER operator_labels_search_catalog_trigger
AFTER INSERT OR UPDATE OR DELETE ON operator_labels
FOR EACH ROW EXECUTE FUNCTION record_search_catalog_document('label');

DROP TRIGGER IF EXISTS name_records_search_catalog_trigger ON name_records;
CREATE TRIGGER name_records_search_catalog_trigger
AFTER INSERT OR UPDATE OR DELETE ON name_records
FOR EACH ROW EXECUTE FUNCTION record_search_catalog_document('name');

DROP TRIGGER IF EXISTS token_contracts_search_catalog_trigger ON token_contracts;
CREATE TRIGGER token_contracts_search_catalog_trigger
AFTER INSERT OR UPDATE OR DELETE ON token_contracts
FOR EACH ROW EXECUTE FUNCTION record_search_catalog_document('token');

DROP TRIGGER IF EXISTS verified_contracts_search_catalog_trigger ON verified_contracts;
CREATE TRIGGER verified_contracts_search_catalog_trigger
AFTER INSERT OR UPDATE OR DELETE ON verified_contracts
FOR EACH ROW EXECUTE FUNCTION record_search_catalog_document('verified_contract');

DROP TRIGGER IF EXISTS contract_code_observations_search_catalog_trigger ON contract_code_observations;
CREATE TRIGGER contract_code_observations_search_catalog_trigger
AFTER INSERT OR UPDATE OR DELETE ON contract_code_observations
FOR EACH ROW EXECUTE FUNCTION record_search_catalog_document('code');

-- Maintenance calls this function with the configured retained generation
-- window. It first closes redundant current observations at a new generation,
-- then advances the oldest accepted cursor generation and removes only history
-- that no accepted cursor can still reference. Source facts remain untouched.
CREATE OR REPLACE FUNCTION prune_search_catalog(
    requested_chain_id NUMERIC,
    requested_retention_generations BIGINT DEFAULT 100000
)
RETURNS BIGINT
LANGUAGE plpgsql
AS $$
DECLARE
    cleanup_generation BIGINT;
    cleanup_min_generation BIGINT;
BEGIN
    IF requested_retention_generations < 1000 OR requested_retention_generations > 10000000 THEN
        RAISE EXCEPTION 'search catalog retention must be between 1000 and 10000000 generations';
    END IF;
    INSERT INTO search_catalog_generations (chain_id)
    VALUES (requested_chain_id)
    ON CONFLICT (chain_id) DO NOTHING;
    UPDATE search_catalog_generations
    SET generation = generation + 1,
        retention_generations = requested_retention_generations,
        updated_at = now()
    WHERE chain_id = requested_chain_id
    RETURNING generation INTO cleanup_generation;

    WITH ranked AS (
        SELECT document.id,
               row_number() OVER (
                   PARTITION BY document.source_kind, document.logical_identity
                   ORDER BY document.block_number DESC NULLS LAST,
                            document.valid_from_generation DESC,
                            document.id DESC
               ) AS position
        FROM search_catalog_documents AS document
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = document.chain_id
         AND canonical.number = document.block_number
         AND canonical.block_hash = document.block_hash
        JOIN chain_finality AS finality
          ON finality.chain_id = document.chain_id
         AND finality.finalized_number IS NOT NULL
         AND document.block_number <= finality.finalized_number
        WHERE document.chain_id = requested_chain_id
          AND document.valid_to_generation IS NULL
          AND document.source_kind IN ('name', 'token', 'code')
          AND document.source_canonical IS TRUE
    )
    UPDATE search_catalog_documents AS document
    SET valid_to_generation = cleanup_generation
    FROM ranked
    WHERE document.id = ranked.id AND ranked.position > 1;

    cleanup_min_generation := GREATEST(0, cleanup_generation - requested_retention_generations);
    UPDATE search_catalog_generations
    SET min_generation = GREATEST(min_generation, cleanup_min_generation), updated_at = now()
    WHERE chain_id = requested_chain_id;

    DELETE FROM search_catalog_documents
    WHERE chain_id = requested_chain_id
      AND valid_to_generation IS NOT NULL
      AND valid_to_generation <= cleanup_min_generation;
    RETURN cleanup_min_generation;
END
$$;

CREATE TABLE IF NOT EXISTS external_adapter_observations (
    id BIGSERIAL PRIMARY KEY,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    capability TEXT NOT NULL,
    observation_key TEXT NOT NULL,
    state TEXT NOT NULL,
    code TEXT,
    value JSONB,
    block_number NUMERIC(78, 0),
    block_hash BYTEA,
    observed_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash),
    CHECK (capability IN ('name', 'price')),
    CHECK (length(observation_key) BETWEEN 1 AND 512),
    CHECK (state IN ('complete', 'unavailable', 'failed')),
    CHECK (code IS NULL OR length(code) BETWEEN 1 AND 128),
    CHECK ((block_number IS NULL) = (block_hash IS NULL)),
    CHECK (block_hash IS NULL OR octet_length(block_hash) = 32),
    CHECK (expires_at > observed_at),
    CHECK (
        (state = 'complete' AND code IS NULL AND value IS NOT NULL)
        OR
        (state IN ('unavailable', 'failed') AND code IS NOT NULL AND value IS NULL)
    )
);

CREATE INDEX IF NOT EXISTS external_adapter_observations_latest_idx
    ON external_adapter_observations (chain_id, capability, observation_key, observed_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS external_adapter_observations_expiry_idx
    ON external_adapter_observations (expires_at);
