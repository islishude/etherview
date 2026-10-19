-- Fresh-database baseline: tokens metadata.

-- Functions

CREATE FUNCTION etherview_guard_erc20_balance_reconciliation() RETURNS trigger
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

CREATE FUNCTION etherview_guard_erc20_holder_fact() RETURNS trigger
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

CREATE FUNCTION etherview_guard_exact_nft_metadata() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path TO 'pg_catalog'
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

CREATE FUNCTION etherview_guard_nft_metadata_source() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path TO 'pg_catalog'
    AS $$
BEGIN
    IF NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'exact NFT metadata source observation is immutable'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION etherview_guard_nft_metadata_update_observation() RETURNS trigger
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

CREATE FUNCTION reject_exact_nft_observation_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'exact NFT state observations are immutable'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END
$$;


-- Sequences

CREATE SEQUENCE external_adapter_observations_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


-- Tables

CREATE TABLE erc1155_balance_reconciliations (
    chain_id numeric(78,0) NOT NULL,
    token_address bytea NOT NULL,
    token_id numeric(78,0) NOT NULL,
    owner_address bytea NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    balance numeric(78,0) NOT NULL,
    confidence text NOT NULL,
    observed_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT erc1155_balance_reconciliations_balance_check CHECK ((balance >= (0)::numeric)),
    CONSTRAINT erc1155_balance_reconciliations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc1155_balance_reconciliations_confidence_check CHECK ((confidence = 'rpc_exact'::text)),
    CONSTRAINT erc1155_balance_reconciliations_owner_address_check CHECK ((octet_length(owner_address) = 20)),
    CONSTRAINT erc1155_balance_reconciliations_token_address_check CHECK ((octet_length(token_address) = 20)),
    CONSTRAINT erc1155_balance_reconciliations_token_id_check CHECK ((token_id >= (0)::numeric)),
    CONSTRAINT erc1155_balance_reconciliations_pkey PRIMARY KEY (chain_id, token_address, token_id, owner_address, block_hash),
    CONSTRAINT erc1155_balance_reconciliatio_chain_id_block_number_block__fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash),
    CONSTRAINT erc1155_balance_reconciliations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE erc20_balance_reconciliations (
    chain_id numeric(78,0) NOT NULL,
    token_address bytea NOT NULL,
    owner_address bytea NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    balance numeric(78,0) NOT NULL,
    confidence text NOT NULL,
    observed_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT erc20_balance_reconciliations_balance_check CHECK (((balance >= (0)::numeric) AND (balance <= '115792089237316195423570985008687907853269984665640564039457584007913129639935'::numeric))),
    CONSTRAINT erc20_balance_reconciliations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc20_balance_reconciliations_confidence_check CHECK ((confidence = 'rpc_exact'::text)),
    CONSTRAINT erc20_balance_reconciliations_owner_address_check CHECK ((octet_length(owner_address) = 20)),
    CONSTRAINT erc20_balance_reconciliations_token_address_check CHECK ((octet_length(token_address) = 20)),
    CONSTRAINT erc20_balance_reconciliations_pkey PRIMARY KEY (chain_id, token_address, owner_address, block_hash),
    CONSTRAINT erc20_balance_reconciliations_chain_id_block_number_block__fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash),
    CONSTRAINT erc20_balance_reconciliations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE erc20_holder_snapshots (
    chain_id numeric(78,0) NOT NULL,
    token_address bytea NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    state text NOT NULL,
    holder_count numeric(78,0) NOT NULL,
    total_supply numeric(78,0) NOT NULL,
    reconciled_balance_sum numeric(78,0) NOT NULL,
    canonical boolean NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT erc20_holder_snapshots_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc20_holder_snapshots_check CHECK ((((state = 'complete'::text) AND (total_supply = reconciled_balance_sum)) OR (state = 'unavailable'::text))),
    CONSTRAINT erc20_holder_snapshots_holder_count_check CHECK ((holder_count >= (0)::numeric)),
    CONSTRAINT erc20_holder_snapshots_reconciled_balance_sum_check CHECK ((reconciled_balance_sum >= (0)::numeric)),
    CONSTRAINT erc20_holder_snapshots_state_check CHECK ((state = ANY (ARRAY['complete'::text, 'unavailable'::text]))),
    CONSTRAINT erc20_holder_snapshots_token_address_check CHECK ((octet_length(token_address) = 20)),
    CONSTRAINT erc20_holder_snapshots_total_supply_check CHECK ((total_supply >= (0)::numeric)),
    CONSTRAINT erc20_holder_snapshots_pkey PRIMARY KEY (chain_id, token_address, block_number, block_hash),
    CONSTRAINT erc20_holder_snapshots_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash),
    CONSTRAINT erc20_holder_snapshots_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
)
PARTITION BY RANGE (block_number);

CREATE TABLE erc20_holder_snapshots_default PARTITION OF erc20_holder_snapshots DEFAULT;

CREATE TABLE erc20_holder_snapshots_p_0_1000000 PARTITION OF erc20_holder_snapshots FOR VALUES FROM ('0') TO ('1000000');

CREATE TABLE erc20_holder_balances (
    chain_id numeric(78,0) NOT NULL,
    token_address bytea NOT NULL,
    holder_address bytea NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    balance numeric(78,0) NOT NULL,
    confidence text NOT NULL,
    canonical boolean NOT NULL,
    observed_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT erc20_holder_balances_balance_check CHECK ((balance >= (0)::numeric)),
    CONSTRAINT erc20_holder_balances_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc20_holder_balances_confidence_check CHECK ((confidence = 'rpc_exact'::text)),
    CONSTRAINT erc20_holder_balances_holder_address_check CHECK ((octet_length(holder_address) = 20)),
    CONSTRAINT erc20_holder_balances_token_address_check CHECK ((octet_length(token_address) = 20)),
    CONSTRAINT erc20_holder_balances_pkey PRIMARY KEY (chain_id, token_address, block_number, block_hash, holder_address),
    CONSTRAINT erc20_holder_balances_chain_id_token_address_block_number__fkey FOREIGN KEY (chain_id, token_address, block_number, block_hash) REFERENCES erc20_holder_snapshots(chain_id, token_address, block_number, block_hash)
)
PARTITION BY RANGE (block_number);

CREATE TABLE erc20_holder_balances_default PARTITION OF erc20_holder_balances DEFAULT;

CREATE TABLE erc20_holder_balances_p_0_1000000 PARTITION OF erc20_holder_balances FOR VALUES FROM ('0') TO ('1000000');

CREATE TABLE erc721_owner_reconciliations (
    chain_id numeric(78,0) NOT NULL,
    token_address bytea NOT NULL,
    token_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    state text NOT NULL,
    owner_address bytea,
    confidence text NOT NULL,
    observed_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT erc721_owner_reconciliations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc721_owner_reconciliations_check CHECK ((((state = 'owned'::text) AND (owner_address IS NOT NULL)) OR ((state = 'not_found'::text) AND (owner_address IS NULL)))),
    CONSTRAINT erc721_owner_reconciliations_confidence_check CHECK ((confidence = 'rpc_exact'::text)),
    CONSTRAINT erc721_owner_reconciliations_owner_address_check CHECK (((owner_address IS NULL) OR (octet_length(owner_address) = 20))),
    CONSTRAINT erc721_owner_reconciliations_state_check CHECK ((state = ANY (ARRAY['owned'::text, 'not_found'::text]))),
    CONSTRAINT erc721_owner_reconciliations_token_address_check CHECK ((octet_length(token_address) = 20)),
    CONSTRAINT erc721_owner_reconciliations_token_id_check CHECK ((token_id >= (0)::numeric)),
    CONSTRAINT erc721_owner_reconciliations_pkey PRIMARY KEY (chain_id, token_address, token_id, block_hash),
    CONSTRAINT erc721_owner_reconciliations_chain_id_block_number_block_h_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash),
    CONSTRAINT erc721_owner_reconciliations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE external_adapter_observations (
    id bigint NOT NULL DEFAULT nextval('external_adapter_observations_id_seq'::regclass),
    chain_id numeric(78,0) NOT NULL,
    capability text NOT NULL,
    observation_key text NOT NULL,
    state text NOT NULL,
    code text,
    value jsonb,
    block_number numeric(78,0),
    block_hash bytea,
    observed_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    recorded_at timestamp with time zone DEFAULT now() NOT NULL,
    provider_key text DEFAULT 'default'::text NOT NULL,
    CONSTRAINT external_adapter_observations_block_hash_check CHECK (((block_hash IS NULL) OR (octet_length(block_hash) = 32))),
    CONSTRAINT external_adapter_observations_capability_check CHECK ((capability = ANY (ARRAY['name'::text, 'price'::text]))),
    CONSTRAINT external_adapter_observations_check CHECK (((block_number IS NULL) = (block_hash IS NULL))),
    CONSTRAINT external_adapter_observations_check1 CHECK ((expires_at > observed_at)),
    CONSTRAINT external_adapter_observations_check2 CHECK ((((state = 'complete'::text) AND (code IS NULL) AND (value IS NOT NULL)) OR ((state = ANY (ARRAY['unavailable'::text, 'failed'::text])) AND (code IS NOT NULL) AND (value IS NULL)))),
    CONSTRAINT external_adapter_observations_code_check CHECK (((code IS NULL) OR ((length(code) >= 1) AND (length(code) <= 128)))),
    CONSTRAINT external_adapter_observations_observation_key_check CHECK (((length(observation_key) >= 1) AND (length(observation_key) <= 512))),
    CONSTRAINT external_adapter_observations_provider_key_check CHECK (((length(provider_key) >= 1) AND (length(provider_key) <= 128))),
    CONSTRAINT external_adapter_observations_state_check CHECK ((state = ANY (ARRAY['complete'::text, 'unavailable'::text, 'failed'::text]))),
    CONSTRAINT external_adapter_observations_pkey PRIMARY KEY (id),
    CONSTRAINT external_adapter_observations_chain_id_block_number_block__fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash),
    CONSTRAINT external_adapter_observations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE external_metadata (
    chain_id numeric(78,0) NOT NULL,
    resource_kind text NOT NULL,
    resource_key text NOT NULL,
    source_uri text NOT NULL,
    resolved_uri text,
    state text NOT NULL,
    media_type text,
    content_hash bytea,
    document jsonb,
    last_error text,
    fetched_at timestamp with time zone,
    expires_at timestamp with time zone,
    token_address bytea,
    token_id numeric(78,0),
    observed_block_number numeric(78,0),
    observed_block_hash bytea,
    content_size bigint,
    attempt_count integer DEFAULT 0 NOT NULL,
    last_error_code text,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    terminal_at timestamp with time zone,
    identity_hash bytea DEFAULT '\x'::bytea NOT NULL,
    CONSTRAINT external_metadata_attempt_count_check CHECK ((attempt_count >= 0)),
    CONSTRAINT external_metadata_content_hash_check CHECK (((content_hash IS NULL) OR (octet_length(content_hash) = 32))),
    CONSTRAINT external_metadata_content_size_check CHECK (((content_size IS NULL) OR ((content_size >= 0) AND (content_size <= 2097152)))),
    CONSTRAINT external_metadata_error_code_check CHECK (((last_error_code IS NULL) OR ((length(last_error_code) >= 1) AND (length(last_error_code) <= 64)))),
    CONSTRAINT external_metadata_identity_hash_check CHECK ((((resource_kind = 'nft'::text) AND (observed_block_hash IS NOT NULL) AND (identity_hash = observed_block_hash)) OR ((resource_kind <> 'nft'::text) AND (identity_hash = '\x'::bytea)) OR ((resource_kind = 'nft'::text) AND (observed_block_hash IS NULL) AND (identity_hash = '\x'::bytea)))),
    CONSTRAINT external_metadata_nft_attempt_bound_check CHECK (((resource_kind <> 'nft'::text) OR ((attempt_count >= 0) AND (attempt_count <= 100)))),
    CONSTRAINT external_metadata_nft_identity_check CHECK (((resource_kind <> 'nft'::text) OR ((token_address IS NULL) AND (token_id IS NULL) AND (observed_block_number IS NULL)) OR ((token_address IS NOT NULL) AND (token_id IS NOT NULL) AND (observed_block_number IS NOT NULL)))),
    CONSTRAINT external_metadata_nft_outcome_check CHECK (((resource_kind <> 'nft'::text) OR (observed_block_hash IS NULL) OR ((state = 'pending'::text) AND (resolved_uri IS NULL) AND (media_type IS NULL) AND (content_hash IS NULL) AND (document IS NULL) AND (content_size IS NULL) AND (terminal_at IS NULL)) OR ((state = 'available'::text) AND (resolved_uri IS NOT NULL) AND (media_type IS NOT NULL) AND (content_hash IS NOT NULL) AND (document IS NOT NULL) AND (content_size IS NOT NULL) AND (last_error_code IS NULL) AND (last_error IS NULL) AND (fetched_at IS NOT NULL) AND (terminal_at IS NOT NULL)) OR ((state = ANY (ARRAY['unavailable'::text, 'unsafe'::text, 'error'::text])) AND (resolved_uri IS NULL) AND (media_type IS NULL) AND (content_hash IS NULL) AND (document IS NULL) AND (content_size IS NULL) AND (last_error_code IS NOT NULL) AND (last_error IS NOT NULL) AND (fetched_at IS NOT NULL) AND (terminal_at IS NOT NULL)))),
    CONSTRAINT external_metadata_nft_source_uri_check CHECK (((resource_kind <> 'nft'::text) OR (observed_block_hash IS NULL) OR ((length(source_uri) >= 1) AND (length(source_uri) <= 4096)))),
    CONSTRAINT external_metadata_nft_uint256_check CHECK (((resource_kind <> 'nft'::text) OR (token_id IS NULL) OR (token_id <= '115792089237316195423570985008687907853269984665640564039457584007913129639935'::numeric))),
    CONSTRAINT external_metadata_observation_pair_check CHECK (((observed_block_number IS NULL) = (observed_block_hash IS NULL))),
    CONSTRAINT external_metadata_observed_hash_check CHECK (((observed_block_hash IS NULL) OR (octet_length(observed_block_hash) = 32))),
    CONSTRAINT external_metadata_resource_kind_check CHECK ((resource_kind = ANY (ARRAY['token'::text, 'nft'::text, 'name'::text]))),
    CONSTRAINT external_metadata_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'available'::text, 'unavailable'::text, 'unsafe'::text, 'error'::text]))),
    CONSTRAINT external_metadata_token_address_check CHECK (((token_address IS NULL) OR (octet_length(token_address) = 20))),
    CONSTRAINT external_metadata_token_id_check CHECK (((token_id IS NULL) OR (token_id >= (0)::numeric))),
    CONSTRAINT external_metadata_pkey PRIMARY KEY (chain_id, resource_kind, resource_key, identity_hash),
    CONSTRAINT external_metadata_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT external_metadata_observed_block_fk FOREIGN KEY (chain_id, observed_block_number, observed_block_hash) REFERENCES blocks(chain_id, number, hash)
);

CREATE TABLE external_metadata_attempts (
    id bigint NOT NULL GENERATED ALWAYS AS IDENTITY (
        SEQUENCE NAME external_metadata_attempts_id_seq
        START WITH 1
        INCREMENT BY 1
        NO MINVALUE
        NO MAXVALUE
        CACHE 1
    ),
    chain_id numeric(78,0) NOT NULL,
    resource_kind text NOT NULL,
    resource_key text NOT NULL,
    durable_job_id bigint NOT NULL,
    attempt integer NOT NULL,
    state text NOT NULL,
    source_uri text NOT NULL,
    resolved_uri text,
    media_type text,
    content_hash bytea,
    content_size bigint,
    error_code text,
    error_message text,
    attempted_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT external_metadata_attempts_attempt_check CHECK ((attempt > 0)),
    CONSTRAINT external_metadata_attempts_content_hash_check CHECK (((content_hash IS NULL) OR (octet_length(content_hash) = 32))),
    CONSTRAINT external_metadata_attempts_content_size_check CHECK (((content_size IS NULL) OR ((content_size >= 0) AND (content_size <= 2097152)))),
    CONSTRAINT external_metadata_attempts_error_code_check CHECK (((error_code IS NULL) OR ((length(error_code) >= 1) AND (length(error_code) <= 64)))),
    CONSTRAINT external_metadata_attempts_error_message_check CHECK (((error_message IS NULL) OR (length(error_message) <= 1024))),
    CONSTRAINT external_metadata_attempts_resource_kind_check CHECK ((resource_kind = ANY (ARRAY['token'::text, 'nft'::text, 'name'::text]))),
    CONSTRAINT external_metadata_attempts_state_check CHECK ((state = ANY (ARRAY['available'::text, 'unavailable'::text, 'unsafe'::text, 'error'::text]))),
    CONSTRAINT external_metadata_attempts_durable_job_id_attempt_key UNIQUE (durable_job_id, attempt),
    CONSTRAINT external_metadata_attempts_pkey PRIMARY KEY (id),
    CONSTRAINT external_metadata_attempts_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT external_metadata_attempts_durable_job_id_fkey FOREIGN KEY (durable_job_id) REFERENCES durable_jobs(id)
);

CREATE TABLE nft_metadata_source_observations (
    chain_id numeric(78,0) NOT NULL,
    token_address bytea NOT NULL,
    token_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    standard text NOT NULL,
    state text NOT NULL,
    source_uri text,
    error_code text,
    observed_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT nft_metadata_source_observations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT nft_metadata_source_observations_check CHECK ((((state = 'found'::text) AND (source_uri IS NOT NULL) AND (error_code IS NULL)) OR ((state = 'unavailable'::text) AND (source_uri IS NULL) AND (error_code IS NOT NULL)))),
    CONSTRAINT nft_metadata_source_observations_error_code_check CHECK (((error_code IS NULL) OR ((length(error_code) >= 1) AND (length(error_code) <= 64)))),
    CONSTRAINT nft_metadata_source_observations_source_uri_check CHECK (((source_uri IS NULL) OR ((length(source_uri) >= 1) AND (length(source_uri) <= 4096)))),
    CONSTRAINT nft_metadata_source_observations_standard_check CHECK ((standard = ANY (ARRAY['erc721'::text, 'erc1155'::text]))),
    CONSTRAINT nft_metadata_source_observations_state_check CHECK ((state = ANY (ARRAY['found'::text, 'unavailable'::text]))),
    CONSTRAINT nft_metadata_source_observations_token_address_check CHECK ((octet_length(token_address) = 20)),
    CONSTRAINT nft_metadata_source_observations_token_id_check CHECK (((token_id >= (0)::numeric) AND (token_id <= '115792089237316195423570985008687907853269984665640564039457584007913129639935'::numeric))),
    CONSTRAINT nft_metadata_source_observations_pkey PRIMARY KEY (chain_id, token_address, token_id, block_hash),
    CONSTRAINT nft_metadata_source_observati_chain_id_block_number_block__fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash),
    CONSTRAINT nft_metadata_source_observations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE nft_metadata_update_observations (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    log_index bigint NOT NULL,
    token_address bytea NOT NULL,
    standard text NOT NULL,
    event_kind text NOT NULL,
    state text NOT NULL,
    from_token_id numeric(78,0),
    to_token_id numeric(78,0),
    error_code text,
    observed_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT nft_metadata_update_observations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT nft_metadata_update_observations_check CHECK ((((state = 'accepted'::text) AND (error_code IS NULL) AND (from_token_id IS NOT NULL) AND (to_token_id IS NOT NULL) AND (from_token_id <= to_token_id) AND (((event_kind = ANY (ARRAY['erc4906_single'::text, 'erc1155_uri'::text])) AND (from_token_id = to_token_id)) OR (event_kind = 'erc4906_batch'::text)) AND (((event_kind = ANY (ARRAY['erc4906_single'::text, 'erc4906_batch'::text])) AND (standard = 'erc721'::text)) OR ((event_kind = 'erc1155_uri'::text) AND (standard = 'erc1155'::text)))) OR ((state = 'malformed'::text) AND (error_code IS NOT NULL) AND (from_token_id IS NULL) AND (to_token_id IS NULL)))),
    CONSTRAINT nft_metadata_update_observations_error_code_check CHECK (((error_code IS NULL) OR ((length(error_code) >= 1) AND (length(error_code) <= 64)))),
    CONSTRAINT nft_metadata_update_observations_event_kind_check CHECK ((event_kind = ANY (ARRAY['erc4906_single'::text, 'erc4906_batch'::text, 'erc1155_uri'::text]))),
    CONSTRAINT nft_metadata_update_observations_from_token_id_check CHECK (((from_token_id IS NULL) OR ((from_token_id >= (0)::numeric) AND (from_token_id <= '115792089237316195423570985008687907853269984665640564039457584007913129639935'::numeric)))),
    CONSTRAINT nft_metadata_update_observations_log_index_check CHECK ((log_index >= 0)),
    CONSTRAINT nft_metadata_update_observations_standard_check CHECK ((standard = ANY (ARRAY['erc721'::text, 'erc1155'::text]))),
    CONSTRAINT nft_metadata_update_observations_state_check CHECK ((state = ANY (ARRAY['accepted'::text, 'malformed'::text]))),
    CONSTRAINT nft_metadata_update_observations_to_token_id_check CHECK (((to_token_id IS NULL) OR ((to_token_id >= (0)::numeric) AND (to_token_id <= '115792089237316195423570985008687907853269984665640564039457584007913129639935'::numeric)))),
    CONSTRAINT nft_metadata_update_observations_token_address_check CHECK ((octet_length(token_address) = 20)),
    CONSTRAINT nft_metadata_update_observations_pkey PRIMARY KEY (chain_id, block_number, block_hash, log_index),
    CONSTRAINT nft_metadata_update_observati_chain_id_block_number_block__fkey FOREIGN KEY (chain_id, block_number, block_hash, log_index) REFERENCES logs(chain_id, block_number, block_hash, log_index),
    CONSTRAINT nft_metadata_update_observations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE token_events (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    log_index bigint NOT NULL,
    sub_index integer DEFAULT 0 NOT NULL,
    transaction_hash bytea NOT NULL,
    token_address bytea NOT NULL,
    standard text NOT NULL,
    event_kind text NOT NULL,
    operator bytea,
    from_address bytea,
    to_address bytea,
    token_id numeric(78,0),
    amount numeric(78,0),
    canonical boolean NOT NULL,
    confidence text NOT NULL,
    raw jsonb NOT NULL,
    CONSTRAINT token_events_amount_check CHECK (((amount IS NULL) OR (amount >= (0)::numeric))),
    CONSTRAINT token_events_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT token_events_check CHECK (((log_index >= 0) AND (sub_index >= 0))),
    CONSTRAINT token_events_confidence_check CHECK ((confidence = ANY (ARRAY['verified'::text, 'high'::text, 'inferred'::text, 'guess'::text]))),
    CONSTRAINT token_events_event_kind_check CHECK ((event_kind = ANY (ARRAY['transfer'::text, 'approval'::text, 'approval_for_all'::text, 'mint'::text, 'burn'::text]))),
    CONSTRAINT token_events_from_address_check CHECK (((from_address IS NULL) OR (octet_length(from_address) = 20))),
    CONSTRAINT token_events_operator_check CHECK (((operator IS NULL) OR (octet_length(operator) = 20))),
    CONSTRAINT token_events_standard_check CHECK ((standard = ANY (ARRAY['erc20'::text, 'erc721'::text, 'erc1155'::text]))),
    CONSTRAINT token_events_to_address_check CHECK (((to_address IS NULL) OR (octet_length(to_address) = 20))),
    CONSTRAINT token_events_token_address_check CHECK ((octet_length(token_address) = 20)),
    CONSTRAINT token_events_token_id_check CHECK (((token_id IS NULL) OR (token_id >= (0)::numeric))),
    CONSTRAINT token_events_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT token_events_pkey PRIMARY KEY (chain_id, block_number, block_hash, log_index, sub_index),
    CONSTRAINT token_events_chain_id_block_number_block_hash_log_index_fkey FOREIGN KEY (chain_id, block_number, block_hash, log_index) REFERENCES logs(chain_id, block_number, block_hash, log_index)
)
PARTITION BY RANGE (block_number);

CREATE TABLE token_events_default PARTITION OF token_events DEFAULT;

CREATE TABLE token_events_p_0_1000000 PARTITION OF token_events FOR VALUES FROM ('0') TO ('1000000');

CREATE TABLE token_balance_deltas (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    log_index bigint NOT NULL,
    sub_index integer DEFAULT 0 NOT NULL,
    token_address bytea NOT NULL,
    owner_address bytea NOT NULL,
    token_id numeric(78,0),
    delta numeric(79,0) NOT NULL,
    canonical boolean NOT NULL,
    CONSTRAINT token_balance_deltas_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT token_balance_deltas_owner_address_check CHECK ((octet_length(owner_address) = 20)),
    CONSTRAINT token_balance_deltas_token_address_check CHECK ((octet_length(token_address) = 20)),
    CONSTRAINT token_balance_deltas_token_id_check CHECK (((token_id IS NULL) OR (token_id >= (0)::numeric))),
    CONSTRAINT token_balance_deltas_pkey PRIMARY KEY (chain_id, block_number, block_hash, log_index, sub_index, token_address, owner_address),
    CONSTRAINT token_balance_deltas_chain_id_block_number_block_hash_log__fkey FOREIGN KEY (chain_id, block_number, block_hash, log_index, sub_index) REFERENCES token_events(chain_id, block_number, block_hash, log_index, sub_index)
)
PARTITION BY RANGE (block_number);

CREATE TABLE token_balance_deltas_default PARTITION OF token_balance_deltas DEFAULT;

CREATE TABLE token_balance_deltas_p_0_1000000 PARTITION OF token_balance_deltas FOR VALUES FROM ('0') TO ('1000000');

CREATE TABLE token_contracts (
    chain_id numeric(78,0) NOT NULL,
    address bytea NOT NULL,
    code_hash bytea NOT NULL,
    standard text NOT NULL,
    confidence text NOT NULL,
    name text,
    symbol text,
    decimals integer,
    total_supply numeric(78,0),
    metadata_state text DEFAULT 'pending'::text NOT NULL,
    observed_block_number numeric(78,0) NOT NULL,
    observed_block_hash bytea NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT token_contracts_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT token_contracts_code_hash_check CHECK ((octet_length(code_hash) = 32)),
    CONSTRAINT token_contracts_confidence_check CHECK ((confidence = ANY (ARRAY['verified'::text, 'high'::text, 'inferred'::text, 'guess'::text]))),
    CONSTRAINT token_contracts_decimals_check CHECK (((decimals IS NULL) OR ((decimals >= 0) AND (decimals <= 255)))),
    CONSTRAINT token_contracts_metadata_state_check CHECK ((metadata_state = ANY (ARRAY['pending'::text, 'complete'::text, 'unavailable'::text, 'failed'::text]))),
    CONSTRAINT token_contracts_observed_block_hash_check CHECK ((octet_length(observed_block_hash) = 32)),
    CONSTRAINT token_contracts_standard_check CHECK ((standard = ANY (ARRAY['erc20'::text, 'erc721'::text, 'erc1155'::text, 'unknown'::text]))),
    CONSTRAINT token_contracts_total_supply_check CHECK (((total_supply IS NULL) OR (total_supply >= (0)::numeric))),
    CONSTRAINT token_contracts_pkey PRIMARY KEY (chain_id, address, code_hash, observed_block_hash),
    CONSTRAINT token_contracts_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT token_contracts_chain_id_observed_block_number_observed_bl_fkey FOREIGN KEY (chain_id, observed_block_number, observed_block_hash) REFERENCES blocks(chain_id, number, hash)
);


-- Dependency completion

ALTER SEQUENCE external_adapter_observations_id_seq OWNED BY external_adapter_observations.id;


-- Indexes

CREATE INDEX erc20_holder_balances_default_chain_id_token_address_holder_idx ON erc20_holder_balances_default USING btree (chain_id, token_address, holder_address, block_number DESC, block_hash) WHERE canonical;

CREATE INDEX erc20_holder_balances_p_0_100_chain_id_token_address_holder_idx ON erc20_holder_balances_p_0_1000000 USING btree (chain_id, token_address, holder_address, block_number DESC, block_hash) WHERE canonical;

CREATE INDEX erc20_holder_snapshots_defaul_chain_id_token_address_block__idx ON erc20_holder_snapshots_default USING btree (chain_id, token_address, block_number DESC, block_hash) WHERE canonical;

CREATE INDEX erc20_holder_snapshots_p_0_10_chain_id_token_address_block__idx ON erc20_holder_snapshots_p_0_1000000 USING btree (chain_id, token_address, block_number DESC, block_hash) WHERE canonical;

CREATE INDEX token_balance_deltas_default_chain_id_owner_address_token_a_idx ON token_balance_deltas_default USING btree (chain_id, owner_address, token_address, token_id, block_number) WHERE canonical;

CREATE INDEX token_balance_deltas_p_0_1000_chain_id_owner_address_token__idx ON token_balance_deltas_p_0_1000000 USING btree (chain_id, owner_address, token_address, token_id, block_number) WHERE canonical;

CREATE INDEX token_events_default_chain_id_from_address_block_number_idx ON token_events_default USING btree (chain_id, from_address, block_number DESC) WHERE canonical;

CREATE INDEX token_events_default_chain_id_to_address_block_number_idx ON token_events_default USING btree (chain_id, to_address, block_number DESC) WHERE canonical;

CREATE INDEX token_events_default_chain_id_token_address_block_number_lo_idx ON token_events_default USING btree (chain_id, token_address, block_number DESC, log_index DESC);

CREATE INDEX token_events_default_chain_id_token_address_token_id_block__idx ON token_events_default USING btree (chain_id, token_address, token_id, block_number DESC, block_hash) WHERE ((token_id IS NOT NULL) AND (standard = ANY (ARRAY['erc721'::text, 'erc1155'::text])));

CREATE INDEX token_events_p_0_1000000_chain_id_from_address_block_number_idx ON token_events_p_0_1000000 USING btree (chain_id, from_address, block_number DESC) WHERE canonical;

CREATE INDEX token_events_p_0_1000000_chain_id_to_address_block_number_idx ON token_events_p_0_1000000 USING btree (chain_id, to_address, block_number DESC) WHERE canonical;

CREATE INDEX token_events_p_0_1000000_chain_id_token_address_block_numbe_idx ON token_events_p_0_1000000 USING btree (chain_id, token_address, block_number DESC, log_index DESC);

CREATE INDEX token_events_p_0_1000000_chain_id_token_address_token_id_bl_idx ON token_events_p_0_1000000 USING btree (chain_id, token_address, token_id, block_number DESC, block_hash) WHERE ((token_id IS NOT NULL) AND (standard = ANY (ARRAY['erc721'::text, 'erc1155'::text])));

CREATE INDEX erc1155_balance_reconciliations_owner_idx ON erc1155_balance_reconciliations USING btree (chain_id, owner_address, token_address, token_id, block_number DESC);

CREATE INDEX erc20_balance_reconciliations_owner_snapshot_idx ON erc20_balance_reconciliations USING btree (chain_id, owner_address, block_number, block_hash, token_address);

CREATE INDEX erc20_holder_balances_page_idx ON erc20_holder_balances USING btree (chain_id, token_address, holder_address, block_number DESC, block_hash) WHERE canonical;

CREATE INDEX erc20_holder_snapshots_current_idx ON erc20_holder_snapshots USING btree (chain_id, token_address, block_number DESC, block_hash) WHERE canonical;

CREATE INDEX erc721_owner_reconciliations_owner_idx ON erc721_owner_reconciliations USING btree (chain_id, owner_address, token_address, token_id, block_number DESC) WHERE (state = 'owned'::text);

CREATE INDEX external_adapter_observations_expiry_idx ON external_adapter_observations USING btree (expires_at);

CREATE INDEX external_adapter_observations_latest_idx ON external_adapter_observations USING btree (chain_id, capability, provider_key, observation_key, observed_at DESC, id DESC);

CREATE INDEX external_metadata_attempts_resource_idx ON external_metadata_attempts USING btree (chain_id, resource_kind, resource_key, attempted_at DESC);

CREATE INDEX external_metadata_nft_canonical_lookup_idx ON external_metadata USING btree (chain_id, token_address, token_id, observed_block_number DESC, observed_block_hash);

CREATE UNIQUE INDEX external_metadata_nft_identity_uq ON external_metadata USING btree (chain_id, token_address, token_id, observed_block_hash) WHERE ((resource_kind = 'nft'::text) AND (token_address IS NOT NULL) AND (token_id IS NOT NULL) AND (observed_block_hash IS NOT NULL));

CREATE INDEX external_metadata_pending_idx ON external_metadata USING btree (chain_id, updated_at, resource_key) WHERE (state = 'pending'::text);

CREATE INDEX nft_metadata_source_candidate_idx ON nft_metadata_source_observations USING btree (chain_id, token_address, token_id, block_number DESC, block_hash);

CREATE INDEX nft_metadata_update_candidate_idx ON nft_metadata_update_observations USING btree (chain_id, token_address, block_number DESC, block_hash, from_token_id, to_token_id) WHERE (state = 'accepted'::text);

CREATE INDEX token_balance_deltas_owner_idx ON token_balance_deltas USING btree (chain_id, owner_address, token_address, token_id, block_number) WHERE canonical;

CREATE INDEX token_contracts_canonical_lookup_idx ON token_contracts USING btree (chain_id, address, observed_block_number DESC, observed_block_hash, code_hash);

CREATE INDEX token_events_from_idx ON token_events USING btree (chain_id, from_address, block_number DESC) WHERE canonical;

CREATE INDEX token_events_to_idx ON token_events USING btree (chain_id, to_address, block_number DESC) WHERE canonical;

CREATE INDEX token_events_token_idx ON token_events USING btree (chain_id, token_address, block_number DESC, log_index DESC);

CREATE INDEX token_events_metadata_known_id_idx ON token_events USING btree (chain_id, token_address, token_id, block_number DESC, block_hash) WHERE ((token_id IS NOT NULL) AND (standard = ANY (ARRAY['erc721'::text, 'erc1155'::text])));


-- Comments

COMMENT ON COLUMN external_adapter_observations.provider_key IS 'Stable non-secret namespace for the configured adapter provider';


-- Triggers

CREATE TRIGGER erc1155_balance_reconciliations_immutable_trigger BEFORE UPDATE ON erc1155_balance_reconciliations FOR EACH ROW EXECUTE FUNCTION reject_exact_nft_observation_mutation();

CREATE TRIGGER erc20_balance_reconciliation_immutable BEFORE UPDATE ON erc20_balance_reconciliations FOR EACH ROW EXECUTE FUNCTION etherview_guard_erc20_balance_reconciliation();

CREATE TRIGGER erc20_holder_balances_immutable BEFORE UPDATE ON erc20_holder_balances FOR EACH ROW EXECUTE FUNCTION etherview_guard_erc20_holder_fact();

CREATE TRIGGER erc20_holder_snapshots_immutable BEFORE UPDATE ON erc20_holder_snapshots FOR EACH ROW EXECUTE FUNCTION etherview_guard_erc20_holder_fact();

CREATE TRIGGER erc721_owner_reconciliations_immutable_trigger BEFORE UPDATE ON erc721_owner_reconciliations FOR EACH ROW EXECUTE FUNCTION reject_exact_nft_observation_mutation();

CREATE TRIGGER external_metadata_exact_nft_immutable BEFORE UPDATE ON external_metadata FOR EACH ROW EXECUTE FUNCTION etherview_guard_exact_nft_metadata();

CREATE TRIGGER nft_metadata_source_observation_immutable BEFORE UPDATE ON nft_metadata_source_observations FOR EACH ROW EXECUTE FUNCTION etherview_guard_nft_metadata_source();

CREATE TRIGGER nft_metadata_update_observation_immutable BEFORE UPDATE ON nft_metadata_update_observations FOR EACH ROW EXECUTE FUNCTION etherview_guard_nft_metadata_update_observation();
