-- Fresh-database baseline: ens search.

-- Functions

CREATE FUNCTION prune_search_catalog(requested_chain_id numeric, requested_retention_generations bigint DEFAULT 100000) RETURNS bigint
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

CREATE FUNCTION record_search_catalog_document() RETURNS trigger
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
    -- Conditional name upserts deliberately perform a no-op UPDATE to make an
    -- identical concurrent observation visible to the losing statement. Do
    -- not create a catalog generation for that idempotent write.
    IF TG_OP = 'UPDATE' AND OLD IS NOT DISTINCT FROM NEW THEN
        RETURN NEW;
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

CREATE FUNCTION record_verified_contract_search_catalog_document() RETURNS trigger
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
    IF TG_OP = 'UPDATE' AND OLD IS NOT DISTINCT FROM NEW THEN
        RETURN NEW;
    END IF;
    IF TG_OP = 'DELETE' THEN
        source_chain := OLD.chain_id;
        old_identity := jsonb_build_array(
            encode(OLD.address, 'hex'), encode(OLD.code_hash, 'hex'),
            OLD.valid_from_block::text, OLD.verification_job_id::text
        )::text;
    ELSE
        source_chain := NEW.chain_id;
        new_identity := jsonb_build_array(
            encode(NEW.address, 'hex'), encode(NEW.code_hash, 'hex'),
            NEW.valid_from_block::text, NEW.verification_job_id::text
        )::text;
        logical_identity_value := jsonb_build_array(
            encode(NEW.address, 'hex'), encode(NEW.code_hash, 'hex'),
            NEW.valid_from_block::text
        )::text;
        IF TG_OP = 'UPDATE' THEN
            old_identity := jsonb_build_array(
                encode(OLD.address, 'hex'), encode(OLD.code_hash, 'hex'),
                OLD.valid_from_block::text, OLD.verification_job_id::text
            )::text;
        END IF;
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
      AND source_kind = 'verified_contract'
      AND source_identity = old_identity
      AND valid_to_generation IS NULL;

    IF TG_OP = 'UPDATE' AND old_identity IS DISTINCT FROM new_identity THEN
        UPDATE search_catalog_documents
        SET valid_to_generation = next_generation
        WHERE chain_id = source_chain
          AND source_kind = 'verified_contract'
          AND source_identity = new_identity
          AND valid_to_generation IS NULL;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;

    INSERT INTO search_catalog_documents (
        chain_id, source_kind, source_identity, logical_identity,
        valid_from_generation, result_kind, result_key, result_label,
        exact_terms, partial_terms, target_address, code_hash,
        valid_from_block, valid_to_block, verification_match_type,
        verification_request_digest, verification_job_id
    ) VALUES (
        NEW.chain_id, 'verified_contract', new_identity,
        logical_identity_value, next_generation, 'contract',
        '0x' || encode(NEW.address, 'hex'), NEW.contract_name,
        ARRAY[
            lower('0x' || encode(NEW.address, 'hex')),
            lower(NEW.contract_name)
        ], ARRAY[lower(NEW.contract_name)], NEW.address, NEW.code_hash,
        NEW.valid_from_block, NEW.valid_to_block, NEW.match_type,
        NEW.request_digest, NEW.verification_job_id
    );
    RETURN NEW;
END
$$;


-- Tables

CREATE TABLE operator_labels (
    id bigint NOT NULL GENERATED ALWAYS AS IDENTITY (
        SEQUENCE NAME operator_labels_id_seq
        START WITH 1
        INCREMENT BY 1
        NO MINVALUE
        NO MAXVALUE
        CACHE 1
    ),
    chain_id numeric(78,0) NOT NULL,
    object_kind text NOT NULL,
    object_key text NOT NULL,
    label text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT operator_labels_label_check CHECK (((length(label) >= 1) AND (length(label) <= 256))),
    CONSTRAINT operator_labels_object_key_check CHECK (((length(object_key) >= 1) AND (length(object_key) <= 256))),
    CONSTRAINT operator_labels_object_kind_check CHECK ((object_kind = ANY (ARRAY['address'::text, 'block'::text, 'transaction'::text, 'token'::text, 'contract'::text]))),
    CONSTRAINT operator_labels_chain_id_object_kind_object_key_key UNIQUE (chain_id, object_kind, object_key),
    CONSTRAINT operator_labels_pkey PRIMARY KEY (id),
    CONSTRAINT operator_labels_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

-- ADR-0058 background ENS facts and publications. No request-time cache tables.
CREATE TABLE ens_sync_sources (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    target_chain_id numeric(78,0) NOT NULL REFERENCES chains(chain_id),
    source_chain_id numeric(78,0) NOT NULL CHECK (source_chain_id > 0),
    genesis_hash bytea NOT NULL CHECK (octet_length(genesis_hash) = 32),
    namespace text NOT NULL CHECK (namespace IN ('ens', 'custom_ens')),
    policy_digest bytea NOT NULL CHECK (octet_length(policy_digest) = 32),
    manifest_digest bytea NOT NULL CHECK (octet_length(manifest_digest) = 32),
    start_number numeric(78,0) NOT NULL CHECK (start_number >= 0),
    start_hash bytea NOT NULL CHECK (octet_length(start_hash) = 32),
    CHECK (namespace <> 'custom_ens' OR source_chain_id = target_chain_id),
    CHECK (namespace <> 'ens' OR source_chain_id = 1),
    CHECK (start_number <> 0 OR start_hash = genesis_hash),
    UNIQUE (target_chain_id, source_chain_id, genesis_hash, namespace, policy_digest)
);

CREATE TABLE ens_sync_blocks (
    source_id bigint NOT NULL REFERENCES ens_sync_sources(id),
    number numeric(78,0) NOT NULL CHECK (number >= 0),
    hash bytea NOT NULL CHECK (octet_length(hash) = 32),
    parent_hash bytea NOT NULL CHECK (octet_length(parent_hash) = 32),
    block_time timestamptz NOT NULL,
    canonical boolean NOT NULL,
    PRIMARY KEY (source_id, hash),
    UNIQUE (source_id, number, hash)
);
CREATE UNIQUE INDEX ens_sync_blocks_canonical ON ens_sync_blocks(source_id, number) WHERE canonical;

-- A cursor is evidence only from the manifest's exact start through consecutive
-- parent-linked blocks. A target or successful poll alone is not coverage.
CREATE TABLE ens_sync_progress (
    source_id bigint PRIMARY KEY REFERENCES ens_sync_sources(id),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    projection_revision bigint NOT NULL DEFAULT 1 CHECK (projection_revision > 0),
    target_number numeric(78,0),
    target_hash bytea,
    scanned_number numeric(78,0),
    scanned_hash bytea,
    healthy_until timestamptz,
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'active', 'unsupported', 'halted')),
    failure_code text CHECK (failure_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    CHECK ((target_number IS NULL) = (target_hash IS NULL)),
    CHECK ((scanned_number IS NULL) = (scanned_hash IS NULL)),
    CHECK (scanned_number IS NULL OR (target_number IS NOT NULL AND scanned_number <= target_number)),
    FOREIGN KEY (source_id, target_number, target_hash) REFERENCES ens_sync_blocks(source_id, number, hash),
    FOREIGN KEY (source_id, scanned_number, scanned_hash) REFERENCES ens_sync_blocks(source_id, number, hash)
);

CREATE TABLE ens_sync_deployments (
    source_id bigint NOT NULL REFERENCES ens_sync_sources(id),
    address bytea NOT NULL CHECK (octet_length(address) = 20),
    protocol text NOT NULL CHECK (protocol IN ('v1', 'v2', 'custom')),
    block_hash bytea NOT NULL,
    code_hash bytea NOT NULL CHECK (octet_length(code_hash) = 32),
    abi_digest bytea NOT NULL CHECK (octet_length(abi_digest) = 32),
    implementation_address bytea CHECK (octet_length(implementation_address) = 20),
    implementation_code_hash bytea CHECK (octet_length(implementation_code_hash) = 32),
    CHECK ((implementation_address IS NULL) = (implementation_code_hash IS NULL)),
    PRIMARY KEY (source_id, address),
    FOREIGN KEY (source_id, block_hash) REFERENCES ens_sync_blocks(source_id, hash)
);

CREATE FUNCTION valid_ens_sync_topics(topics bytea[]) RETURNS boolean
LANGUAGE sql IMMUTABLE SET search_path = pg_catalog AS $$
    SELECT cardinality(topics) BETWEEN 0 AND 4
      AND (cardinality(topics) = 0 OR array_ndims(topics) = 1)
      AND NOT EXISTS (SELECT 1 FROM unnest(topics) AS topic WHERE topic IS NULL OR octet_length(topic) <> 32)
$$;

CREATE TABLE ens_sync_events (
    source_id bigint NOT NULL,
    block_hash bytea NOT NULL,
    log_index integer NOT NULL CHECK (log_index >= 0),
    transaction_hash bytea NOT NULL CHECK (octet_length(transaction_hash) = 32),
    transaction_index integer NOT NULL CHECK (transaction_index >= 0),
    emitter bytea NOT NULL,
    topics bytea[] NOT NULL CHECK (valid_ens_sync_topics(topics)),
    data bytea NOT NULL CHECK (octet_length(data) <= 1048576),
    PRIMARY KEY (source_id, block_hash, log_index),
    FOREIGN KEY (source_id, block_hash) REFERENCES ens_sync_blocks(source_id, hash),
    FOREIGN KEY (source_id, emitter) REFERENCES ens_sync_deployments(source_id, address)
);

-- Versioned protocol facts include graph edges, record bundles, lifecycle and
-- preimages. Projectors own each bounded payload's protocol-specific shape.
-- Versions advance independently of the source epoch. Jobs pin only the
-- projection keys they actually depend on; changes invalidate those jobs.
CREATE TABLE ens_sync_projections (
    source_id bigint NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    kind text NOT NULL CHECK (kind IN ('registration', 'control', 'registry_link', 'resolver_record', 'name_link', 'preimage', 'lifecycle')),
    key text NOT NULL CHECK (octet_length(key) BETWEEN 1 AND 512),
    deployment bytea NOT NULL,
    block_hash bytea NOT NULL,
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object' AND octet_length(payload::text) <= 65536),
    PRIMARY KEY (source_id, kind, key, revision),
    FOREIGN KEY (source_id, deployment) REFERENCES ens_sync_deployments(source_id, address),
    FOREIGN KEY (source_id, block_hash) REFERENCES ens_sync_blocks(source_id, hash)
);

CREATE TABLE ens_sync_candidates (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_id bigint NOT NULL REFERENCES ens_sync_sources(id),
    direction text NOT NULL CHECK (direction IN ('forward', 'primary')),
    lookup_key text NOT NULL CHECK (octet_length(lookup_key) BETWEEN 1 AND 512),
    coin_type numeric(78,0) NOT NULL CHECK (coin_type >= 0),
    requested_generation bigint NOT NULL DEFAULT 1 CHECK (requested_generation > 0),
    discovery_revision bigint NOT NULL DEFAULT 1 CHECK (discovery_revision > 0),
    minimum_generation bigint NOT NULL DEFAULT 1 CHECK (minimum_generation BETWEEN 1 AND requested_generation),
    discovered_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    refresh_at timestamptz,
    UNIQUE (source_id, direction, lookup_key, coin_type),
    UNIQUE (source_id, id)
);
CREATE TABLE ens_sync_candidate_origins (
    source_id bigint NOT NULL,
    candidate_id bigint NOT NULL,
    kind text NOT NULL CHECK (kind IN ('event', 'indexed_address', 'import', 'genesis')),
    identity text NOT NULL CHECK (octet_length(identity) BETWEEN 1 AND 512),
    discovered_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (source_id, candidate_id, kind, identity),
    FOREIGN KEY (source_id, candidate_id) REFERENCES ens_sync_candidates(source_id, id)
);

CREATE TABLE ens_sync_jobs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_id bigint NOT NULL,
    candidate_id bigint NOT NULL,
    generation bigint NOT NULL CHECK (generation > 0),
    source_revision bigint NOT NULL CHECK (source_revision > 0),
    target_hash bytea NOT NULL,
    core_canonical_at timestamptz,
    state text NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'leased', 'complete', 'failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts integer NOT NULL CHECK (max_attempts BETWEEN 1 AND 100),
    retry_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    deadline timestamptz NOT NULL,
    lease_token uuid,
    lease_until timestamptz,
    failure_code text CHECK (failure_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    CHECK (attempts <= max_attempts),
    CHECK ((state = 'leased') = (lease_token IS NOT NULL AND lease_until IS NOT NULL)),
    CHECK ((lease_token IS NULL) = (lease_until IS NULL)),
    CHECK (state <> 'failed' OR failure_code IS NOT NULL),
    UNIQUE (source_id, candidate_id, generation),
    UNIQUE (source_id, candidate_id, id, generation),
    UNIQUE (source_id, id),
    FOREIGN KEY (source_id, candidate_id) REFERENCES ens_sync_candidates(source_id, id),
    FOREIGN KEY (source_id, target_hash) REFERENCES ens_sync_blocks(source_id, hash)
);
CREATE INDEX ens_sync_jobs_due ON ens_sync_jobs(source_id, retry_at, id) WHERE state IN ('queued', 'leased');
CREATE INDEX ens_sync_candidates_refresh ON ens_sync_candidates(source_id, refresh_at, id);

CREATE TABLE ens_sync_job_dependencies (
    source_id bigint NOT NULL,
    job_id bigint NOT NULL,
    kind text NOT NULL,
    key text NOT NULL,
    revision bigint NOT NULL,
    PRIMARY KEY (source_id, job_id, kind, key),
    FOREIGN KEY (source_id, job_id) REFERENCES ens_sync_jobs(source_id, id),
    FOREIGN KEY (source_id, kind, key, revision) REFERENCES ens_sync_projections(source_id, kind, key, revision)
);

CREATE TABLE ens_sync_observations (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_id bigint NOT NULL,
    candidate_id bigint NOT NULL,
    job_id bigint NOT NULL,
    lease_token uuid NOT NULL,
    generation bigint NOT NULL,
    source_revision bigint NOT NULL CHECK (source_revision > 0),
    block_hash bytea NOT NULL,
    deployment bytea NOT NULL,
    endpoint text NOT NULL CHECK (endpoint ~ '^[a-zA-Z0-9_-]{1,64}$'),
    evidence text NOT NULL CHECK (evidence IN ('projection', 'resolver')),
    outcome text NOT NULL CHECK (outcome IN ('resolved', 'not_found')),
    name text CHECK (octet_length(name) BETWEEN 1 AND 255),
    address bytea CHECK (octet_length(address) = 20),
    resolver bytea CHECK (octet_length(resolver) = 20),
    reverse_resolver bytea CHECK (octet_length(reverse_resolver) = 20),
    observed_at timestamptz NOT NULL,
    valid_until timestamptz NOT NULL,
    CHECK (valid_until > observed_at),
    CHECK ((outcome = 'resolved' AND name IS NOT NULL AND address IS NOT NULL AND resolver IS NOT NULL)
        OR (outcome = 'not_found' AND resolver IS NULL AND reverse_resolver IS NULL)),
    UNIQUE (source_id, candidate_id, generation),
    UNIQUE (source_id, candidate_id, id),
    FOREIGN KEY (source_id, candidate_id, job_id, generation) REFERENCES ens_sync_jobs(source_id, candidate_id, id, generation),
    FOREIGN KEY (source_id, block_hash) REFERENCES ens_sync_blocks(source_id, hash),
    FOREIGN KEY (source_id, deployment) REFERENCES ens_sync_deployments(source_id, address)
);

-- Source publications are immutable building blocks. T26 owns effective
-- official/custom selection, catalog and durable names-event publication.
CREATE TABLE ens_sync_publications (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_id bigint NOT NULL,
    candidate_id bigint NOT NULL,
    observation_id bigint NOT NULL,
    published_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    retain_until timestamptz NOT NULL CHECK (retain_until > published_at),
    UNIQUE (observation_id),
    FOREIGN KEY (source_id, candidate_id, observation_id) REFERENCES ens_sync_observations(source_id, candidate_id, id)
);
CREATE INDEX ens_sync_publications_lookup ON ens_sync_publications(source_id, candidate_id, id DESC);

CREATE FUNCTION reject_ens_sync_mutation() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF OLD IS DISTINCT FROM NEW THEN
        RAISE EXCEPTION 'ENS synchronization evidence is immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER ens_sync_sources_immutable BEFORE UPDATE ON ens_sync_sources FOR EACH ROW EXECUTE FUNCTION reject_ens_sync_mutation();
CREATE TRIGGER ens_sync_deployments_immutable BEFORE UPDATE ON ens_sync_deployments FOR EACH ROW EXECUTE FUNCTION reject_ens_sync_mutation();
CREATE TRIGGER ens_sync_events_immutable BEFORE UPDATE ON ens_sync_events FOR EACH ROW EXECUTE FUNCTION reject_ens_sync_mutation();
CREATE TRIGGER ens_sync_projections_immutable BEFORE UPDATE ON ens_sync_projections FOR EACH ROW EXECUTE FUNCTION reject_ens_sync_mutation();
CREATE TRIGGER ens_sync_observations_immutable BEFORE UPDATE ON ens_sync_observations FOR EACH ROW EXECUTE FUNCTION reject_ens_sync_mutation();
CREATE TRIGGER ens_sync_publications_immutable BEFORE UPDATE ON ens_sync_publications FOR EACH ROW EXECUTE FUNCTION reject_ens_sync_mutation();

-- Header content is immutable; canonical detach retains the exact orphan facts.
CREATE FUNCTION reject_ens_sync_header_mutation() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF (to_jsonb(OLD) - 'canonical') IS DISTINCT FROM (to_jsonb(NEW) - 'canonical') THEN
        RAISE EXCEPTION 'ENS source header is immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER ens_sync_blocks_immutable BEFORE UPDATE ON ens_sync_blocks FOR EACH ROW EXECUTE FUNCTION reject_ens_sync_header_mutation();

CREATE TRIGGER ens_sync_job_dependencies_immutable BEFORE UPDATE ON ens_sync_job_dependencies FOR EACH ROW EXECUTE FUNCTION reject_ens_sync_mutation();

-- Completed collector blocks are separate from headers observed as targets or
-- deployment identities. Their attachment stamps fence disabled-period reorgs.
CREATE TABLE ens_sync_scans (
    source_id bigint NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    core_canonical_at timestamptz,
    PRIMARY KEY (source_id, block_hash),
    FOREIGN KEY (source_id, block_number, block_hash) REFERENCES ens_sync_blocks(source_id, number, hash)
);
CREATE INDEX ens_sync_scans_rewind ON ens_sync_scans(source_id, block_number DESC);

CREATE TABLE ens_sync_address_cursors (
    source_id bigint PRIMARY KEY REFERENCES ens_sync_sources(id),
    after_address bytea NOT NULL DEFAULT '\x' CHECK (octet_length(after_address) IN (0,20)),
    cycle bigint NOT NULL DEFAULT 0 CHECK (cycle >= 0)
);

-- A genesis contract has authenticated deployment/code evidence, not a set of
-- fabricated registration events. Preallocated resolver records still require
-- a verified state baseline or background resolution before proving absence.
CREATE TABLE ens_sync_genesis_baselines (
    source_id bigint NOT NULL,
    deployment bytea NOT NULL,
    genesis_hash bytea NOT NULL CHECK (octet_length(genesis_hash)=32),
    document_digest bytea NOT NULL CHECK (octet_length(document_digest)=32),
    PRIMARY KEY (source_id, deployment),
    FOREIGN KEY (source_id, deployment) REFERENCES ens_sync_deployments(source_id,address)
);
CREATE TRIGGER ens_sync_genesis_baselines_immutable BEFORE UPDATE ON ens_sync_genesis_baselines FOR EACH ROW EXECUTE FUNCTION reject_ens_sync_mutation();

-- Registry changes invalidate projection dependencies immediately. This cursor
-- bounds subsequent name-path discovery without discarding concurrent dirties.
CREATE TABLE ens_sync_graph_work (
    source_id bigint PRIMARY KEY REFERENCES ens_sync_sources(id),
    requested_revision bigint NOT NULL DEFAULT 1 CHECK (requested_revision > 0),
    running_revision bigint NOT NULL DEFAULT 1 CHECK (running_revision > 0),
    after_key text NOT NULL DEFAULT '' CHECK (octet_length(after_key) <= 512),
    pending boolean NOT NULL DEFAULT true
);

CREATE TABLE ens_effective_policies (
    target_chain_id numeric(78,0) NOT NULL REFERENCES chains(chain_id),
    policy_digest bytea NOT NULL CHECK (octet_length(policy_digest)=32),
    mode text NOT NULL CHECK (mode IN ('official_first','custom_only')),
    official_source_id bigint REFERENCES ens_sync_sources(id),
    custom_source_id bigint REFERENCES ens_sync_sources(id),
    unsupported_coin boolean NOT NULL DEFAULT false,
    coin_type numeric(78,0) NOT NULL CHECK (coin_type>=0),
    custom_coin_type numeric(78,0) NOT NULL CHECK (custom_coin_type>=0),
    PRIMARY KEY(target_chain_id,policy_digest),
    CHECK ((mode='custom_only' AND official_source_id IS NULL AND custom_source_id IS NOT NULL AND NOT unsupported_coin)
      OR (mode='official_first' AND ((official_source_id IS NOT NULL AND NOT unsupported_coin) OR (official_source_id IS NULL AND unsupported_coin AND custom_source_id IS NOT NULL))))
);
CREATE TRIGGER ens_effective_policies_immutable BEFORE UPDATE ON ens_effective_policies FOR EACH ROW EXECUTE FUNCTION reject_ens_sync_mutation();

CREATE TABLE ens_effective_publications (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    target_chain_id numeric(78,0) NOT NULL,
    policy_digest bytea NOT NULL,
    direction text NOT NULL CHECK (direction IN ('forward','primary')),
    lookup_key text NOT NULL CHECK (octet_length(lookup_key) BETWEEN 1 AND 512),
    official_publication_id bigint REFERENCES ens_sync_publications(id),
    custom_publication_id bigint REFERENCES ens_sync_publications(id),
    selected_publication_id bigint REFERENCES ens_sync_publications(id),
    state text NOT NULL CHECK (state IN ('resolved','not_found','unavailable')),
    code text CHECK (code ~ '^[a-z][a-z0-9_]{0,63}$'),
    name text CHECK (octet_length(name) BETWEEN 1 AND 255),
    address bytea CHECK (octet_length(address)=20),
    namespace text CHECK (namespace IN ('ens','custom_ens')),
    valid_until timestamptz,
    retain_until timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY(target_chain_id,policy_digest) REFERENCES ens_effective_policies(target_chain_id,policy_digest),
    CHECK (retain_until>created_at),
    CHECK ((state='unavailable' AND selected_publication_id IS NULL AND name IS NULL AND address IS NULL AND namespace IS NULL AND valid_until IS NULL AND code IS NOT NULL)
      OR (state<>'unavailable' AND selected_publication_id IS NOT NULL AND valid_until IS NOT NULL AND namespace IS NOT NULL AND code IS NULL)),
    CHECK (state<>'resolved' OR (name IS NOT NULL AND address IS NOT NULL)),
    CHECK (valid_until IS NULL OR retain_until>=valid_until)
);
CREATE INDEX ens_effective_publications_lookup ON ens_effective_publications(target_chain_id,policy_digest,direction,lookup_key,id DESC);
CREATE TRIGGER ens_effective_publications_immutable BEFORE UPDATE ON ens_effective_publications FOR EACH ROW EXECUTE FUNCTION reject_ens_sync_mutation();

-- Effective publication validity is shared by catalog reads and housekeeping.
-- No network state or request-time mutation participates in these predicates.
CREATE FUNCTION ens_sync_publication_current(publication bigint) RETURNS boolean LANGUAGE sql STABLE AS $$
SELECT EXISTS (
 SELECT 1 FROM ens_sync_publications pub
 JOIN ens_sync_observations o ON o.id=pub.observation_id
 JOIN ens_sync_jobs j ON j.id=o.job_id AND j.state='complete'
 JOIN ens_sync_candidates c ON c.id=o.candidate_id AND c.source_id=o.source_id
 JOIN ens_sync_sources s ON s.id=o.source_id
 JOIN ens_sync_progress p ON p.source_id=s.id
 JOIN ens_sync_blocks b ON b.source_id=o.source_id AND b.hash=o.block_hash AND b.canonical
 JOIN ens_sync_blocks target ON target.source_id=p.source_id AND target.hash=p.target_hash AND target.canonical
 WHERE pub.id=publication AND o.generation>=c.minimum_generation AND o.source_revision=p.revision
 AND p.state='active' AND p.healthy_until>clock_timestamp() AND p.target_hash=p.scanned_hash
 AND pub.retain_until>clock_timestamp() AND o.valid_until>clock_timestamp()
 AND (s.namespace<>'custom_ens' OR (
  EXISTS(SELECT 1 FROM canonical_blocks cb JOIN chains ch ON ch.chain_id=cb.chain_id AND ch.genesis_hash=s.genesis_hash
   WHERE cb.chain_id=s.target_chain_id AND cb.block_hash=b.hash AND cb.number=b.number AND cb.updated_at=j.core_canonical_at)
  AND EXISTS(SELECT 1 FROM canonical_blocks cb WHERE cb.chain_id=s.target_chain_id AND cb.block_hash=target.hash AND cb.number=target.number)))
 AND NOT EXISTS(SELECT 1 FROM ens_sync_job_dependencies d
  JOIN ens_sync_projections f ON f.source_id=d.source_id AND f.kind=d.kind AND f.key=d.key AND f.revision=d.revision
  JOIN ens_sync_blocks fb ON fb.source_id=f.source_id AND fb.hash=f.block_hash
  WHERE d.source_id=j.source_id AND d.job_id=j.id AND (NOT fb.canonical OR fb.number>b.number OR EXISTS(
   SELECT 1 FROM ens_sync_projections n JOIN ens_sync_blocks nb ON nb.source_id=n.source_id AND nb.hash=n.block_hash AND nb.canonical
   WHERE n.source_id=d.source_id AND n.kind=d.kind AND n.key=d.key AND n.revision>d.revision)))
);
$$;
CREATE FUNCTION ens_effective_publication_current(publication bigint) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM ens_effective_publications e
 JOIN ens_effective_policies p ON p.target_chain_id=e.target_chain_id AND p.policy_digest=e.policy_digest
 WHERE e.id=publication AND e.state<>'unavailable' AND e.valid_until>clock_timestamp() AND e.retain_until>clock_timestamp()
 AND ens_sync_publication_current(e.selected_publication_id)
 AND (p.mode='custom_only' OR p.unsupported_coin OR ens_sync_publication_current(e.official_publication_id)));
$$;

CREATE FUNCTION record_ens_effective_search_document() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE next_generation bigint;
BEGIN
 IF NEW.direction<>'forward' THEN RETURN NEW; END IF;
 INSERT INTO search_catalog_generations(chain_id) VALUES(NEW.target_chain_id) ON CONFLICT DO NOTHING;
 UPDATE search_catalog_generations SET generation=generation+1,updated_at=now()
 WHERE chain_id=NEW.target_chain_id RETURNING generation INTO next_generation;
 UPDATE search_catalog_documents SET valid_to_generation=next_generation
 WHERE chain_id=NEW.target_chain_id AND source_kind='name' AND logical_identity=NEW.lookup_key AND valid_to_generation IS NULL;
 IF NEW.state<>'resolved' THEN RETURN NEW; END IF;
 INSERT INTO search_catalog_documents(chain_id,source_kind,source_identity,logical_identity,valid_from_generation,
 result_kind,result_key,result_label,exact_terms,partial_terms,target_address,source_canonical,name_publication_id,name_source)
 VALUES(NEW.target_chain_id,'name','publication:'||NEW.id,NEW.lookup_key,next_generation,
 'address','0x'||encode(NEW.address,'hex'),NEW.name,ARRAY[NEW.name],ARRAY[NEW.name],NEW.address,true,NEW.id,NEW.namespace);
 RETURN NEW;
END;
$$;
CREATE TRIGGER ens_effective_search_catalog AFTER INSERT ON ens_effective_publications
 FOR EACH ROW EXECUTE FUNCTION record_ens_effective_search_document();

CREATE FUNCTION notify_ens_source_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.revision IS DISTINCT FROM OLD.revision OR NEW.state IS DISTINCT FROM OLD.state
    OR ((NEW.target_hash IS DISTINCT FROM NEW.scanned_hash) IS DISTINCT FROM (OLD.target_hash IS DISTINCT FROM OLD.scanned_hash)) THEN
  INSERT INTO runtime_events(chain_id,event_type,payload)
  SELECT DISTINCT target_chain_id,'names',jsonb_build_object('source',NEW.source_id::text,'invalidated',true)
  FROM ens_effective_policies WHERE official_source_id=NEW.source_id OR custom_source_id=NEW.source_id;
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER ens_source_change AFTER UPDATE ON ens_sync_progress FOR EACH ROW EXECUTE FUNCTION notify_ens_source_change();

CREATE TABLE ens_effective_retention (
 target_chain_id numeric(78,0) NOT NULL, policy_digest bytea NOT NULL,
 min_publication bigint NOT NULL CHECK(min_publication>=0),
 PRIMARY KEY(target_chain_id,policy_digest),
 FOREIGN KEY(target_chain_id,policy_digest) REFERENCES ens_effective_policies(target_chain_id,policy_digest)
);

-- ENS can publish while the core status reporter is idle. Retain the same
-- 256-event window as the application's durable broker in that case too.
CREATE FUNCTION prune_ens_event_window() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 DELETE FROM runtime_events WHERE chain_id=NEW.chain_id AND id<COALESCE((
  SELECT id FROM runtime_events WHERE chain_id=NEW.chain_id ORDER BY id DESC OFFSET 255 LIMIT 1),0);
 RETURN NEW;
END;
$$;
CREATE TRIGGER ens_event_window AFTER INSERT ON runtime_events FOR EACH ROW
 WHEN (NEW.event_type='names') EXECUTE FUNCTION prune_ens_event_window();

CREATE TABLE ens_sync_imports (
 source_id bigint NOT NULL REFERENCES ens_sync_sources(id), batch_id text NOT NULL CHECK(batch_id ~ '^[a-zA-Z0-9_-]{1,64}$'),
 digest bytea NOT NULL CHECK(octet_length(digest)=32), count integer NOT NULL CHECK(count BETWEEN 1 AND 1000),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), PRIMARY KEY(source_id,batch_id)
);
CREATE TRIGGER ens_sync_imports_immutable BEFORE UPDATE ON ens_sync_imports FOR EACH ROW EXECUTE FUNCTION reject_ens_sync_mutation();
CREATE TABLE ens_sync_replays (
 batch_id text NOT NULL CHECK(batch_id ~ '^[a-zA-Z0-9_-]{1,64}$'),
 row_limit integer NOT NULL CHECK(row_limit BETWEEN 1 AND 1000),
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, source_id bigint NOT NULL REFERENCES ens_sync_sources(id),
 after_candidate bigint NOT NULL CHECK(after_candidate>=0), through_candidate bigint NOT NULL CHECK(through_candidate>=after_candidate),
 count integer NOT NULL CHECK(count BETWEEN 0 AND 1000),reason text NOT NULL CHECK(octet_length(reason) BETWEEN 1 AND 128),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), UNIQUE(source_id,batch_id)
);
CREATE TRIGGER ens_sync_replays_immutable BEFORE UPDATE ON ens_sync_replays FOR EACH ROW EXECUTE FUNCTION reject_ens_sync_mutation();

CREATE TABLE search_catalog_documents (
    id bigint NOT NULL GENERATED ALWAYS AS IDENTITY (
        SEQUENCE NAME search_catalog_documents_id_seq
        START WITH 1
        INCREMENT BY 1
        NO MINVALUE
        NO MAXVALUE
        CACHE 1
    ),
    chain_id numeric(78,0) NOT NULL,
    source_kind text NOT NULL,
    source_identity text NOT NULL,
    logical_identity text NOT NULL,
    valid_from_generation bigint NOT NULL,
    valid_to_generation bigint,
    result_kind text,
    result_key text,
    result_label text,
    exact_terms text[] DEFAULT '{}'::text[] NOT NULL,
    partial_terms text[] DEFAULT '{}'::text[] NOT NULL,
    block_number numeric(78,0),
    block_hash bytea,
    target_address bytea,
    code_hash bytea,
    valid_from_block numeric(78,0),
    valid_to_block numeric(78,0),
    source_canonical boolean,
    recorded_at timestamp with time zone DEFAULT now() NOT NULL,
    verification_match_type text,
    verification_request_digest bytea,
    verification_job_id uuid,
    name_publication_id bigint REFERENCES ens_effective_publications(id),
    name_source text,
    CONSTRAINT search_catalog_documents_block_hash_check CHECK (((block_hash IS NULL) OR (octet_length(block_hash) = 32))),
    CONSTRAINT search_catalog_documents_check CHECK (((valid_to_generation IS NULL) OR (valid_to_generation > valid_from_generation))),
    CONSTRAINT search_catalog_documents_check1 CHECK (((result_kind IS NULL) = (result_key IS NULL))),
    CONSTRAINT search_catalog_documents_check2 CHECK (((result_kind IS NULL) = (result_label IS NULL))),
    CONSTRAINT search_catalog_documents_check3 CHECK (((block_number IS NULL) = (block_hash IS NULL))),
    CONSTRAINT search_catalog_documents_check4 CHECK (((valid_to_block IS NULL) OR (valid_from_block IS NOT NULL))),
    CONSTRAINT search_catalog_documents_check5 CHECK (((valid_to_block IS NULL) OR (valid_to_block >= valid_from_block))),
    CONSTRAINT search_catalog_documents_check6 CHECK ((((source_kind = 'code'::text) AND (result_kind IS NULL) AND (target_address IS NOT NULL) AND (code_hash IS NOT NULL)) OR ((source_kind <> 'code'::text) AND (result_kind IS NOT NULL)))),
    CONSTRAINT search_catalog_documents_code_hash_check CHECK (((code_hash IS NULL) OR (octet_length(code_hash) = 32))),
    CONSTRAINT search_catalog_documents_logical_identity_check CHECK (((length(logical_identity) >= 1) AND (length(logical_identity) <= 1024))),
    CONSTRAINT search_catalog_documents_result_key_check CHECK (((result_key IS NULL) OR ((length(result_key) >= 1) AND (length(result_key) <= 256)))),
    CONSTRAINT search_catalog_documents_result_kind_check CHECK (((result_kind IS NULL) OR (result_kind = ANY (ARRAY['address'::text, 'block'::text, 'transaction'::text, 'token'::text, 'contract'::text])))),
    CONSTRAINT search_catalog_documents_result_label_check CHECK (((result_label IS NULL) OR ((length(result_label) >= 1) AND (length(result_label) <= 4096)))),
    CONSTRAINT search_catalog_documents_source_identity_check CHECK (((length(source_identity) >= 1) AND (length(source_identity) <= 1024))),
    CONSTRAINT search_catalog_documents_source_kind_check CHECK ((source_kind = ANY (ARRAY['label'::text, 'name'::text, 'token'::text, 'verified_contract'::text, 'code'::text]))),
    CONSTRAINT search_catalog_documents_target_address_check CHECK (((target_address IS NULL) OR (octet_length(target_address) = 20))),
    CONSTRAINT search_catalog_documents_valid_from_generation_check CHECK ((valid_from_generation >= 1)),
    CONSTRAINT search_catalog_verified_result_shape CHECK ((((source_kind <> 'verified_contract'::text) AND (verification_match_type IS NULL) AND (verification_request_digest IS NULL) AND (verification_job_id IS NULL)) OR ((source_kind = 'verified_contract'::text) AND (((verification_match_type IS NULL) AND (verification_request_digest IS NULL) AND (verification_job_id IS NULL)) OR ((verification_match_type = ANY (ARRAY['full'::text, 'partial'::text])) AND (octet_length(verification_request_digest) = 32) AND (verification_job_id IS NOT NULL)))))),
    CONSTRAINT search_catalog_documents_chain_id_source_kind_source_identi_key UNIQUE (chain_id, source_kind, source_identity, valid_from_generation),
    CONSTRAINT search_catalog_documents_pkey PRIMARY KEY (id),
    CONSTRAINT search_catalog_documents_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE search_catalog_generations (
    chain_id numeric(78,0) NOT NULL,
    generation bigint DEFAULT 0 NOT NULL,
    min_generation bigint DEFAULT 0 NOT NULL,
    retention_generations bigint DEFAULT 100000 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT search_catalog_generations_check CHECK (((min_generation >= 0) AND (min_generation <= generation))),
    CONSTRAINT search_catalog_generations_generation_check CHECK ((generation >= 0)),
    CONSTRAINT search_catalog_generations_retention_generations_check CHECK (((retention_generations >= 1000) AND (retention_generations <= 10000000))),
    CONSTRAINT search_catalog_generations_pkey PRIMARY KEY (chain_id),
    CONSTRAINT search_catalog_generations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);


-- Dependency completion

-- Preserve NOT VALID catalog state; inline CHECK is validated by CREATE TABLE.
ALTER TABLE search_catalog_documents ADD CONSTRAINT search_catalog_documents_name_source_check CHECK ((source_kind='name' AND name_publication_id IS NOT NULL AND name_source IN ('ens','custom_ens')) OR (source_kind<>'name' AND name_publication_id IS NULL AND name_source IS NULL)) NOT VALID;


-- Indexes






CREATE INDEX search_catalog_documents_generation_idx ON search_catalog_documents USING btree (chain_id, valid_from_generation, valid_to_generation, source_kind);

CREATE INDEX search_catalog_documents_terms_idx ON search_catalog_documents USING gin (exact_terms);


-- Triggers

CREATE TRIGGER contract_code_observations_search_catalog_trigger AFTER INSERT OR DELETE OR UPDATE ON contract_code_observations FOR EACH ROW EXECUTE FUNCTION record_search_catalog_document('code');


CREATE TRIGGER operator_labels_search_catalog_trigger AFTER INSERT OR DELETE OR UPDATE ON operator_labels FOR EACH ROW EXECUTE FUNCTION record_search_catalog_document('label');

CREATE TRIGGER token_contracts_search_catalog_trigger AFTER INSERT OR DELETE OR UPDATE ON token_contracts FOR EACH ROW EXECUTE FUNCTION record_search_catalog_document('token');

CREATE TRIGGER verified_contracts_search_catalog_trigger AFTER INSERT OR DELETE OR UPDATE ON verified_contracts FOR EACH ROW EXECUTE FUNCTION record_verified_contract_search_catalog_document();
