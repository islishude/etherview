-- Fresh-database baseline: derived verification.

-- Functions

CREATE FUNCTION enqueue_derived_forward_event_after_stage_publication() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.state = 'complete' AND
       ((NEW.stage = 'trace' AND NEW.stage_version = 4) OR
        (NEW.stage = 'proxy' AND NEW.stage_version = 2)) THEN
        INSERT INTO derived_verification_forward_blocks (
            chain_id, block_number, block_hash, source_stage,
            source_job_id, source_generation
        ) VALUES (
            NEW.chain_id, NEW.block_number, NEW.block_hash, NEW.stage,
            NEW.job_id, NEW.job_generation
        )
        ON CONFLICT (source_job_id, source_generation) DO NOTHING;
    END IF;
    RETURN NEW;
END
$$;

CREATE FUNCTION synchronize_derived_attempt_trace_canonicality() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.canonical IS NOT DISTINCT FROM OLD.canonical THEN
        RETURN NEW;
    END IF;
    IF NOT NEW.canonical THEN
        UPDATE derived_verification_attempts
        SET stale_from_status = status,
            status = 'stale',
            updated_at = clock_timestamp()
        WHERE chain_id = NEW.chain_id
          AND block_number = NEW.block_number
          AND block_hash = NEW.block_hash
          AND transaction_hash = NEW.transaction_hash
          AND trace_path = NEW.trace_path
          AND status <> 'stale';
    ELSE
        UPDATE derived_verification_attempts
        SET status = stale_from_status,
            stale_from_status = NULL,
            updated_at = clock_timestamp()
        WHERE chain_id = NEW.chain_id
          AND block_number = NEW.block_number
          AND block_hash = NEW.block_hash
          AND transaction_hash = NEW.transaction_hash
          AND trace_path = NEW.trace_path
          AND status = 'stale'
          AND stale_from_status IS NOT NULL;
    END IF;
    RETURN NEW;
END
$$;


-- Tables

CREATE TABLE derived_verification_attempts (
    id uuid NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    transaction_hash bytea NOT NULL,
    trace_path text NOT NULL,
    creator_address bytea NOT NULL,
    created_address bytea NOT NULL,
    call_type text NOT NULL,
    compilation_id uuid NOT NULL,
    file_name text,
    contract_name text,
    status text NOT NULL,
    creation_match jsonb,
    runtime_match jsonb,
    verification_job_id uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    stale_from_status text,
    CONSTRAINT derived_verification_attempts_identity_check CHECK (((octet_length(block_hash) = 32) AND (octet_length(transaction_hash) = 32) AND ((length(trace_path) >= 1) AND (length(trace_path) <= 2048)) AND (octet_length(creator_address) = 20) AND (octet_length(created_address) = 20) AND (call_type = ANY (ARRAY['CREATE'::text, 'CREATE2'::text])))),
    CONSTRAINT derived_verification_attempts_outcome_check CHECK ((((status = 'matched'::text) AND (stale_from_status IS NULL) AND (file_name IS NOT NULL) AND (contract_name IS NOT NULL) AND (jsonb_typeof(creation_match) = 'object'::text) AND (jsonb_typeof(runtime_match) = 'object'::text) AND (verification_job_id IS NOT NULL)) OR ((status <> ALL (ARRAY['matched'::text, 'stale'::text])) AND (stale_from_status IS NULL) AND (file_name IS NULL) AND (contract_name IS NULL) AND (creation_match IS NULL) AND (runtime_match IS NULL) AND (verification_job_id IS NULL)) OR ((status = 'stale'::text) AND (stale_from_status IS NULL) AND (file_name IS NULL) AND (contract_name IS NULL) AND (creation_match IS NULL) AND (runtime_match IS NULL) AND (verification_job_id IS NULL)) OR ((status = 'stale'::text) AND (stale_from_status IS NOT NULL) AND (((stale_from_status = 'matched'::text) AND (file_name IS NOT NULL) AND (contract_name IS NOT NULL) AND (jsonb_typeof(creation_match) = 'object'::text) AND (jsonb_typeof(runtime_match) = 'object'::text) AND (verification_job_id IS NOT NULL)) OR ((stale_from_status <> 'matched'::text) AND (file_name IS NULL) AND (contract_name IS NULL) AND (creation_match IS NULL) AND (runtime_match IS NULL) AND (verification_job_id IS NULL)))))),
    CONSTRAINT derived_verification_attempts_status_check CHECK (((status = ANY (ARRAY['pending_runtime'::text, 'matched'::text, 'ambiguous'::text, 'no_match'::text, 'runtime_mismatch'::text, 'stale'::text])) AND ((stale_from_status IS NULL) OR (stale_from_status = ANY (ARRAY['pending_runtime'::text, 'matched'::text, 'ambiguous'::text, 'no_match'::text, 'runtime_mismatch'::text]))))),
    CONSTRAINT derived_verification_attempts_chain_id_block_hash_transacti_key UNIQUE (chain_id, block_hash, transaction_hash, trace_path, compilation_id),
    CONSTRAINT derived_verification_attempts_pkey PRIMARY KEY (id),
    CONSTRAINT derived_verification_attempts_compilation_id_fkey FOREIGN KEY (compilation_id) REFERENCES verification_compilation_units(id) ON DELETE RESTRICT,
    CONSTRAINT derived_verification_attempts_trace_fk FOREIGN KEY (chain_id, block_number, block_hash, transaction_hash, trace_path) REFERENCES normalized_traces(chain_id, block_number, block_hash, transaction_hash, trace_path) ON DELETE RESTRICT,
    CONSTRAINT derived_verification_attempts_verification_job_id_fkey FOREIGN KEY (verification_job_id) REFERENCES verification_jobs(id) ON DELETE RESTRICT
);

CREATE TABLE derived_verification_backfill_requests (
    id bigint NOT NULL GENERATED ALWAYS AS IDENTITY (
        SEQUENCE NAME derived_verification_backfill_requests_id_seq
        START WITH 1
        INCREMENT BY 1
        NO MINVALUE
        NO MAXVALUE
        CACHE 1
    ),
    chain_id numeric(78,0) NOT NULL,
    creator_address bytea,
    reason text NOT NULL,
    scan_count integer DEFAULT 0 NOT NULL,
    requested_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT derived_verification_backfill_requests_shape_check CHECK ((((creator_address IS NULL) OR (octet_length(creator_address) = 20)) AND ((length(reason) >= 1) AND (length(reason) <= 512)) AND (scan_count >= 0))),
    CONSTRAINT derived_verification_backfill_requests_pkey PRIMARY KEY (id),
    CONSTRAINT derived_verification_backfill_requests_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE derived_verification_forward_blocks (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    status text DEFAULT 'queued'::text NOT NULL,
    leased_by text,
    lease_token text,
    lease_expires_at timestamp with time zone,
    attempt_count integer DEFAULT 0 NOT NULL,
    max_attempts integer DEFAULT 5 NOT NULL,
    last_error text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    id bigint NOT NULL GENERATED ALWAYS AS IDENTITY (
        SEQUENCE NAME derived_verification_forward_blocks_id_seq
        START WITH 1
        INCREMENT BY 1
        NO MINVALUE
        NO MAXVALUE
        CACHE 1
    ),
    source_stage text,
    source_job_id bigint,
    source_generation bigint,
    CONSTRAINT derived_verification_forward_blocks_attempt_check CHECK (((attempt_count >= 0) AND ((max_attempts >= 1) AND (max_attempts <= 100)) AND (attempt_count <= max_attempts))),
    CONSTRAINT derived_verification_forward_blocks_identity_check CHECK (((block_number >= (0)::numeric) AND (octet_length(block_hash) = 32))),
    CONSTRAINT derived_verification_forward_blocks_lease_check CHECK ((((leased_by IS NULL) = (lease_token IS NULL)) AND ((leased_by IS NULL) = (lease_expires_at IS NULL)) AND ((status = 'running'::text) = (leased_by IS NOT NULL)))),
    CONSTRAINT derived_verification_forward_blocks_source_check CHECK ((((source_stage = ANY (ARRAY['trace'::text, 'proxy'::text])) AND (source_job_id IS NOT NULL) AND (source_generation IS NOT NULL) AND (source_generation > 0)) OR ((status = 'failed'::text) AND (last_error = 'unbound_source_generation'::text) AND (source_stage IS NULL) AND (source_job_id IS NULL) AND (source_generation IS NULL)))),
    CONSTRAINT derived_verification_forward_blocks_status_check CHECK ((status = ANY (ARRAY['queued'::text, 'running'::text, 'succeeded'::text, 'failed'::text]))),
    CONSTRAINT derived_verification_forward_blocks_pkey PRIMARY KEY (id),
    CONSTRAINT derived_verification_forward_blocks_source_unique UNIQUE (source_job_id, source_generation),
    CONSTRAINT derived_verification_forward__chain_id_block_number_block__fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash) ON DELETE RESTRICT,
    CONSTRAINT derived_verification_forward_blocks_source_fk FOREIGN KEY (source_job_id, source_generation) REFERENCES durable_stage_publications(job_id, job_generation) ON DELETE RESTRICT
);

CREATE TABLE derived_verification_scans (
    compilation_id uuid NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    creator_address bytea NOT NULL,
    creator_code_hash bytea NOT NULL,
    valid_from_block numeric(78,0) NOT NULL,
    valid_to_block numeric(78,0),
    cursor_block_number numeric(78,0) NOT NULL,
    cursor_transaction_hash bytea DEFAULT decode(repeat('00'::text, 32), 'hex'::text) NOT NULL,
    cursor_trace_path text DEFAULT ''::text NOT NULL,
    status text DEFAULT 'queued'::text NOT NULL,
    leased_by text,
    lease_token text,
    lease_expires_at timestamp with time zone,
    attempt_count integer DEFAULT 0 NOT NULL,
    max_attempts integer DEFAULT 5 NOT NULL,
    last_error text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    id bigint NOT NULL GENERATED ALWAYS AS IDENTITY (
        SEQUENCE NAME derived_verification_scans_id_seq
        START WITH 1
        INCREMENT BY 1
        NO MINVALUE
        NO MAXVALUE
        CACHE 1
    ),
    rescan_from_block numeric(78,0),
    CONSTRAINT derived_verification_scans_attempt_check CHECK (((attempt_count >= 0) AND ((max_attempts >= 1) AND (max_attempts <= 100)) AND (attempt_count <= max_attempts))),
    CONSTRAINT derived_verification_scans_identity_check CHECK (((octet_length(creator_address) = 20) AND (octet_length(creator_code_hash) = 32) AND (valid_from_block >= (0)::numeric) AND ((valid_to_block IS NULL) OR (valid_to_block >= valid_from_block)) AND (cursor_block_number >= valid_from_block) AND (octet_length(cursor_transaction_hash) = 32) AND (length(cursor_trace_path) <= 2048))),
    CONSTRAINT derived_verification_scans_lease_check CHECK ((((leased_by IS NULL) = (lease_token IS NULL)) AND ((leased_by IS NULL) = (lease_expires_at IS NULL)) AND ((status = 'running'::text) = (leased_by IS NOT NULL)))),
    CONSTRAINT derived_verification_scans_rescan_check CHECK (((rescan_from_block IS NULL) OR ((rescan_from_block >= valid_from_block) AND ((valid_to_block IS NULL) OR (rescan_from_block <= valid_to_block))))),
    CONSTRAINT derived_verification_scans_status_check CHECK ((status = ANY (ARRAY['queued'::text, 'running'::text, 'succeeded'::text, 'failed'::text]))),
    CONSTRAINT derived_verification_scans_pkey PRIMARY KEY (id),
    CONSTRAINT derived_verification_scans_target_key UNIQUE (compilation_id, creator_address, creator_code_hash, valid_from_block),
    CONSTRAINT derived_verification_scans_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT derived_verification_scans_compilation_id_fkey FOREIGN KEY (compilation_id) REFERENCES verification_compilation_units(id) ON DELETE RESTRICT
);


-- Indexes

CREATE INDEX derived_verification_attempts_created_idx ON derived_verification_attempts USING btree (chain_id, created_address, block_number DESC);

CREATE INDEX derived_verification_attempts_creator_idx ON derived_verification_attempts USING btree (chain_id, creator_address, block_number DESC);

CREATE INDEX derived_verification_forward_blocks_claim_idx ON derived_verification_forward_blocks USING btree (status, updated_at, id) WHERE (status = ANY (ARRAY['queued'::text, 'running'::text]));

CREATE INDEX derived_verification_scans_claim_idx ON derived_verification_scans USING btree (status, updated_at, compilation_id) WHERE (status = ANY (ARRAY['queued'::text, 'running'::text]));


-- Triggers

CREATE TRIGGER durable_stage_publications_derived_forward_enqueue AFTER INSERT ON durable_stage_publications FOR EACH ROW EXECUTE FUNCTION enqueue_derived_forward_event_after_stage_publication();

CREATE TRIGGER normalized_traces_derived_attempt_canonicality AFTER UPDATE OF canonical ON normalized_traces FOR EACH ROW EXECUTE FUNCTION synchronize_derived_attempt_trace_canonicality();
