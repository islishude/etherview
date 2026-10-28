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

CREATE FUNCTION record_ens_name_search_document() RETURNS trigger
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

CREATE TABLE ens_resolution_generations (
    id bigint NOT NULL GENERATED ALWAYS AS IDENTITY (
        SEQUENCE NAME ens_resolution_generations_id_seq
        START WITH 1
        INCREMENT BY 1
        NO MINVALUE
        NO MAXVALUE
        CACHE 1
    ),
    chain_id numeric(78,0) NOT NULL,
    policy_key text NOT NULL,
    coin_type numeric(78,0) NOT NULL,
    official_endpoint text,
    official_block_number numeric(78,0),
    official_block_hash bytea,
    custom_endpoint text,
    custom_coin_type numeric(78,0),
    custom_block_number numeric(78,0),
    custom_block_hash bytea,
    created_at timestamp with time zone NOT NULL,
    fresh_until timestamp with time zone NOT NULL,
    retain_until timestamp with time zone NOT NULL,
    CONSTRAINT ens_resolution_generations_official_complete CHECK ((official_endpoint IS NULL) = (official_block_number IS NULL) AND (official_endpoint IS NULL) = (official_block_hash IS NULL)),
    CONSTRAINT ens_resolution_generations_source_required CHECK (official_endpoint IS NOT NULL OR custom_endpoint IS NOT NULL),
    CONSTRAINT ens_resolution_generations_check CHECK (((custom_endpoint IS NULL) = (custom_block_number IS NULL))),
    CONSTRAINT ens_resolution_generations_check1 CHECK (((custom_endpoint IS NULL) = (custom_block_hash IS NULL))),
    CONSTRAINT ens_resolution_generations_check2 CHECK (((custom_endpoint IS NULL) = (custom_coin_type IS NULL))),
    CONSTRAINT ens_resolution_generations_check3 CHECK ((fresh_until > created_at)),
    CONSTRAINT ens_resolution_generations_check4 CHECK ((retain_until >= fresh_until)),
    CONSTRAINT ens_resolution_generations_coin_type_check CHECK ((coin_type > (0)::numeric)),
    CONSTRAINT ens_resolution_generations_custom_block_hash_check CHECK (((custom_block_hash IS NULL) OR (octet_length(custom_block_hash) = 32))),
    CONSTRAINT ens_resolution_generations_custom_block_number_check CHECK (((custom_block_number IS NULL) OR (custom_block_number >= (0)::numeric))),
    CONSTRAINT ens_resolution_generations_custom_coin_type_check CHECK (((custom_coin_type IS NULL) OR (custom_coin_type > (0)::numeric))),
    CONSTRAINT ens_resolution_generations_custom_endpoint_check CHECK (((custom_endpoint IS NULL) OR ((length(custom_endpoint) >= 1) AND (length(custom_endpoint) <= 128)))),
    CONSTRAINT ens_resolution_generations_official_block_hash_check CHECK ((octet_length(official_block_hash) = 32)),
    CONSTRAINT ens_resolution_generations_official_block_number_check CHECK ((official_block_number >= (0)::numeric)),
    CONSTRAINT ens_resolution_generations_official_endpoint_check CHECK (((length(official_endpoint) >= 1) AND (length(official_endpoint) <= 128))),
    CONSTRAINT ens_resolution_generations_policy_key_check CHECK (((length(policy_key) >= 8) AND (length(policy_key) <= 128))),
    CONSTRAINT ens_resolution_generations_pkey PRIMARY KEY (id),
    CONSTRAINT ens_resolution_generations_chain_id_custom_block_number_cu_fkey FOREIGN KEY (chain_id, custom_block_number, custom_block_hash) REFERENCES blocks(chain_id, number, hash),
    CONSTRAINT ens_resolution_generations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE ens_address_name_snapshots (
    id uuid NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    generation_id bigint NOT NULL,
    created_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    CONSTRAINT ens_address_name_snapshots_check CHECK ((expires_at > created_at)),
    CONSTRAINT ens_address_name_snapshots_pkey PRIMARY KEY (id),
    CONSTRAINT ens_address_name_snapshots_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT ens_address_name_snapshots_generation_id_fkey FOREIGN KEY (generation_id) REFERENCES ens_resolution_generations(id) ON DELETE CASCADE
);

CREATE TABLE ens_name_observations (
    id bigint NOT NULL GENERATED ALWAYS AS IDENTITY (
        SEQUENCE NAME ens_name_observations_id_seq
        START WITH 1
        INCREMENT BY 1
        NO MINVALUE
        NO MAXVALUE
        CACHE 1
    ),
    generation_id bigint NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    source text NOT NULL,
    direction text NOT NULL,
    lookup_key text NOT NULL,
    outcome text NOT NULL,
    name text,
    address bytea,
    resolver bytea,
    reverse_resolver bytea,
    observed_at timestamp with time zone NOT NULL,
    publication_nonce bigint DEFAULT 0 NOT NULL,
    CONSTRAINT ens_name_observations_address_check CHECK (((address IS NULL) OR (octet_length(address) = 20))),
    CONSTRAINT ens_name_observations_check CHECK ((((direction = 'forward'::text) AND (outcome = 'resolved'::text) AND (name IS NOT NULL) AND (address IS NOT NULL) AND (resolver IS NOT NULL) AND (reverse_resolver IS NULL)) OR ((direction = 'forward'::text) AND (outcome = 'not_found'::text) AND (name IS NOT NULL) AND (address IS NULL) AND (resolver IS NULL) AND (reverse_resolver IS NULL)) OR ((direction = 'primary'::text) AND (outcome = 'resolved'::text) AND (name IS NOT NULL) AND (address IS NOT NULL) AND (resolver IS NOT NULL) AND (reverse_resolver IS NOT NULL)) OR ((direction = 'primary'::text) AND (outcome = 'not_found'::text) AND (name IS NULL) AND (address IS NOT NULL) AND (resolver IS NULL) AND (reverse_resolver IS NULL)))),
    CONSTRAINT ens_name_observations_direction_check CHECK ((direction = ANY (ARRAY['forward'::text, 'primary'::text]))),
    CONSTRAINT ens_name_observations_lookup_key_check CHECK (((length(lookup_key) >= 1) AND (length(lookup_key) <= 512))),
    CONSTRAINT ens_name_observations_name_check CHECK (((name IS NULL) OR ((length(name) >= 1) AND (length(name) <= 255)))),
    CONSTRAINT ens_name_observations_outcome_check CHECK ((outcome = ANY (ARRAY['resolved'::text, 'not_found'::text]))),
    CONSTRAINT ens_name_observations_publication_nonce_check CHECK ((publication_nonce >= 0)),
    CONSTRAINT ens_name_observations_resolver_check CHECK (((resolver IS NULL) OR (octet_length(resolver) = 20))),
    CONSTRAINT ens_name_observations_reverse_resolver_check CHECK (((reverse_resolver IS NULL) OR (octet_length(reverse_resolver) = 20))),
    CONSTRAINT ens_name_observations_source_check CHECK ((source = ANY (ARRAY['ens'::text, 'custom_ens'::text]))),
    CONSTRAINT ens_name_observations_generation_id_source_direction_lookup_key UNIQUE (generation_id, source, direction, lookup_key),
    CONSTRAINT ens_name_observations_pkey PRIMARY KEY (id),
    CONSTRAINT ens_name_observations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT ens_name_observations_generation_id_fkey FOREIGN KEY (generation_id) REFERENCES ens_resolution_generations(id) ON DELETE CASCADE
);

CREATE TABLE ens_resolution_failures (
    id bigint NOT NULL GENERATED ALWAYS AS IDENTITY (
        SEQUENCE NAME ens_resolution_failures_id_seq
        START WITH 1
        INCREMENT BY 1
        NO MINVALUE
        NO MAXVALUE
        CACHE 1
    ),
    generation_id bigint NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    source text NOT NULL,
    direction text NOT NULL,
    lookup_key text NOT NULL,
    code text NOT NULL,
    observed_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    CONSTRAINT ens_resolution_failures_check CHECK ((expires_at > observed_at)),
    CONSTRAINT ens_resolution_failures_code_check CHECK (((length(code) >= 1) AND (length(code) <= 128))),
    CONSTRAINT ens_resolution_failures_direction_check CHECK ((direction = ANY (ARRAY['forward'::text, 'primary'::text]))),
    CONSTRAINT ens_resolution_failures_lookup_key_check CHECK (((length(lookup_key) >= 1) AND (length(lookup_key) <= 512))),
    CONSTRAINT ens_resolution_failures_source_check CHECK ((source = ANY (ARRAY['ens'::text, 'custom_ens'::text]))),
    CONSTRAINT ens_resolution_failures_pkey PRIMARY KEY (id),
    CONSTRAINT ens_resolution_failures_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT ens_resolution_failures_generation_id_fkey FOREIGN KEY (generation_id) REFERENCES ens_resolution_generations(id) ON DELETE CASCADE
);

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
    name_observation_id bigint,
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
ALTER TABLE search_catalog_documents
    ADD CONSTRAINT search_catalog_documents_name_source_check CHECK ((((source_kind = 'name'::text) AND (name_observation_id IS NOT NULL) AND (name_source = ANY (ARRAY['ens'::text, 'custom_ens'::text]))) OR ((source_kind <> 'name'::text) AND (name_observation_id IS NULL) AND (name_source IS NULL)))) NOT VALID;


-- Indexes

CREATE INDEX ens_address_name_snapshots_expiry_idx ON ens_address_name_snapshots USING btree (expires_at, id);

CREATE INDEX ens_name_observations_address_idx ON ens_name_observations USING btree (generation_id, source, direction, address) WHERE (direction = 'primary'::text);

CREATE INDEX ens_resolution_failures_fresh_idx ON ens_resolution_failures USING btree (generation_id, source, direction, lookup_key, expires_at DESC, id DESC);

CREATE INDEX ens_resolution_generations_current_idx ON ens_resolution_generations USING btree (chain_id, policy_key, fresh_until DESC, id DESC);

CREATE INDEX ens_resolution_generations_retention_idx ON ens_resolution_generations USING btree (retain_until, id);

CREATE INDEX search_catalog_documents_generation_idx ON search_catalog_documents USING btree (chain_id, valid_from_generation, valid_to_generation, source_kind);

CREATE INDEX search_catalog_documents_terms_idx ON search_catalog_documents USING gin (exact_terms);


-- Triggers

CREATE TRIGGER contract_code_observations_search_catalog_trigger AFTER INSERT OR DELETE OR UPDATE ON contract_code_observations FOR EACH ROW EXECUTE FUNCTION record_search_catalog_document('code');

CREATE TRIGGER ens_name_observations_search_catalog_trigger AFTER INSERT OR UPDATE OF publication_nonce ON ens_name_observations FOR EACH ROW EXECUTE FUNCTION record_ens_name_search_document();

CREATE TRIGGER operator_labels_search_catalog_trigger AFTER INSERT OR DELETE OR UPDATE ON operator_labels FOR EACH ROW EXECUTE FUNCTION record_search_catalog_document('label');

CREATE TRIGGER token_contracts_search_catalog_trigger AFTER INSERT OR DELETE OR UPDATE ON token_contracts FOR EACH ROW EXECUTE FUNCTION record_search_catalog_document('token');

CREATE TRIGGER verified_contracts_search_catalog_trigger AFTER INSERT OR DELETE OR UPDATE ON verified_contracts FOR EACH ROW EXECUTE FUNCTION record_verified_contract_search_catalog_document();
