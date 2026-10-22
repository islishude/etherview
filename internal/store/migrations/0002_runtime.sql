-- Fresh-database baseline: runtime.

-- Functions

CREATE FUNCTION enforce_enrichment_terminal_publication() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    current_job durable_jobs%ROWTYPE;
    exact_result block_stage_results%ROWTYPE;
BEGIN
    SELECT * INTO current_job FROM durable_jobs WHERE id = NEW.id;
    IF NOT FOUND
       OR current_job.kind <> 'enrichment'
       OR current_job.stage NOT IN ('proxy', 'abi', 'token', 'stats', 'trace', 'native_transfer', 'consensus', 'blob')
       OR current_job.status NOT IN ('succeeded', 'failed') THEN
        RETURN NULL;
    END IF;
    IF COALESCE(current_setting('etherview.enrichment_publication_protocol', true), '') <> '2' THEN
        RAISE EXCEPTION 'derived enrichment terminal transition requires publication protocol 2'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF current_job.claimed_generation <> current_job.completed_generation
       OR current_job.requested_generation <> current_job.completed_generation
       OR current_job.leased_generation IS NOT NULL
       OR current_job.lease_token IS NOT NULL
       OR current_job.lease_expires_at IS NOT NULL
       OR current_job.result IS NULL THEN
        RAISE EXCEPTION 'derived enrichment terminal job lacks exact completed generation'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;

    SELECT result.* INTO exact_result
    FROM block_stage_results AS result
    WHERE result.durable_job_id = current_job.id
      AND result.job_generation = current_job.completed_generation
      AND result.chain_id = current_job.chain_id
      AND result.stage = current_job.stage
      AND result.stage_version = current_job.stage_version
      AND current_job.payload->>'block_hash' = '0x' || encode(result.block_hash, 'hex')
      AND current_job.payload->>'block_number' = result.block_number::text;
    IF NOT FOUND
       OR current_job.result->>'state' IS DISTINCT FROM exact_result.state
       OR COALESCE(current_job.result->'details', '{}'::jsonb) IS DISTINCT FROM exact_result.details
       OR (current_job.result->>'error') IS DISTINCT FROM exact_result.last_error THEN
        RAISE EXCEPTION 'derived enrichment terminal job lacks exact stage result'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM durable_stage_publications AS publication
        WHERE publication.job_id = current_job.id
          AND publication.job_generation = current_job.completed_generation
          AND publication.chain_id = current_job.chain_id
          AND publication.block_number = exact_result.block_number
          AND publication.block_hash = exact_result.block_hash
          AND publication.stage = current_job.stage
          AND publication.stage_version = current_job.stage_version
          AND publication.state = exact_result.state
          AND publication.details = exact_result.details
          AND publication.last_error IS NOT DISTINCT FROM exact_result.last_error
    ) THEN
        RAISE EXCEPTION 'derived enrichment terminal job lacks durable publication proof'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;

    IF current_job.status = 'succeeded' THEN
        IF exact_result.state <> 'complete'
           OR NOT EXISTS (
               SELECT 1 FROM block_journals AS journal
               WHERE journal.durable_job_id = current_job.id
                 AND journal.job_generation = current_job.completed_generation
                 AND journal.chain_id = current_job.chain_id
                 AND journal.block_hash = exact_result.block_hash
                 AND journal.stage = current_job.stage || '@' || current_job.stage_version::text
                 AND journal.sequence = 1
           ) THEN
            RAISE EXCEPTION 'successful derived enrichment job lacks exact journal'
                USING ERRCODE = 'integrity_constraint_violation';
        END IF;
    ELSIF exact_result.state NOT IN ('failed', 'unavailable')
          OR EXISTS (
              SELECT 1 FROM block_journals AS journal
              WHERE journal.durable_job_id = current_job.id
                AND journal.job_generation = current_job.completed_generation
          ) THEN
        RAISE EXCEPTION 'terminal derived enrichment failure has invalid result or journal'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NULL;
END
$$;

CREATE FUNCTION reject_durable_stage_publication_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'durable stage publication ledger is append-only'
        USING ERRCODE = 'integrity_constraint_violation';
END
$$;

CREATE FUNCTION require_enrichment_publication_protocol() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.kind = 'enrichment'
       AND NEW.stage IN ('proxy', 'abi', 'token', 'stats', 'trace', 'native_transfer', 'consensus', 'blob')
       AND NEW.status = 'leased'
       AND (
           OLD.status <> 'leased'
           OR OLD.lease_token IS DISTINCT FROM NEW.lease_token
           OR OLD.leased_generation IS DISTINCT FROM NEW.leased_generation
       )
       AND COALESCE(current_setting('etherview.enrichment_publication_protocol', true), '') <> '2' THEN
        RAISE EXCEPTION 'derived enrichment lease requires publication protocol 2'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END
$$;


-- Tables

CREATE TABLE durable_jobs (
    id bigint NOT NULL GENERATED ALWAYS AS IDENTITY (
        SEQUENCE NAME durable_jobs_id_seq
        START WITH 1
        INCREMENT BY 1
        NO MINVALUE
        NO MAXVALUE
        CACHE 1
    ),
    chain_id numeric(78,0) NOT NULL,
    kind text NOT NULL,
    stage text NOT NULL,
    stage_version integer DEFAULT 1 NOT NULL,
    idempotency_key text NOT NULL,
    payload jsonb DEFAULT '{}'::jsonb NOT NULL,
    priority integer DEFAULT 0 NOT NULL,
    status text DEFAULT 'queued'::text NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    max_attempts integer DEFAULT 10 NOT NULL,
    available_at timestamp with time zone DEFAULT now() NOT NULL,
    leased_by text,
    lease_token text,
    lease_expires_at timestamp with time zone,
    result jsonb,
    last_error text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    requested_generation bigint DEFAULT 1 NOT NULL,
    claimed_generation bigint DEFAULT 0 NOT NULL,
    leased_generation bigint,
    completed_generation bigint DEFAULT 0 NOT NULL,
    CONSTRAINT durable_jobs_check CHECK (((attempts >= 0) AND (max_attempts > 0))),
    CONSTRAINT durable_jobs_check1 CHECK (((leased_by IS NULL) = (lease_expires_at IS NULL))),
    CONSTRAINT durable_jobs_check2 CHECK (((leased_by IS NULL) = (lease_token IS NULL))),
    CONSTRAINT durable_jobs_generation_check CHECK (((requested_generation > 0) AND ((claimed_generation >= 0) AND (claimed_generation <= requested_generation)) AND ((completed_generation >= 0) AND (completed_generation <= requested_generation)) AND ((leased_generation IS NULL) OR ((leased_generation >= 1) AND (leased_generation <= requested_generation))))),
    CONSTRAINT durable_jobs_metadata_payload_size_check CHECK (((kind <> 'metadata'::text) OR (octet_length((payload)::text) <= 8192))),
    CONSTRAINT durable_jobs_metadata_stage_check CHECK (((kind <> 'metadata'::text) OR ((stage = 'nft-metadata'::text) AND (stage_version = 1)))),
    CONSTRAINT durable_jobs_stage_version_check CHECK ((stage_version > 0)),
    CONSTRAINT durable_jobs_status_check CHECK ((status = ANY (ARRAY['queued'::text, 'leased'::text, 'succeeded'::text, 'failed'::text, 'cancelled'::text]))),
    CONSTRAINT durable_jobs_chain_id_kind_idempotency_key_key UNIQUE (chain_id, kind, idempotency_key),
    CONSTRAINT durable_jobs_pkey PRIMARY KEY (id),
    CONSTRAINT durable_jobs_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE block_journals (
    chain_id numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    stage text NOT NULL,
    sequence numeric(78,0) NOT NULL,
    payload jsonb NOT NULL,
    canonical boolean NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    durable_job_id bigint,
    job_generation bigint,
    CONSTRAINT block_journals_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT block_journals_publication_pair_check CHECK ((((durable_job_id IS NULL) AND (job_generation IS NULL)) OR ((durable_job_id IS NOT NULL) AND (job_generation IS NOT NULL) AND (job_generation > 0)))),
    CONSTRAINT block_journals_pkey PRIMARY KEY (chain_id, block_hash, stage, sequence),
    CONSTRAINT block_journals_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT block_journals_durable_job_fkey FOREIGN KEY (durable_job_id) REFERENCES durable_jobs(id)
);

CREATE TABLE block_stage_results (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    stage text NOT NULL,
    stage_version integer NOT NULL,
    state text NOT NULL,
    details jsonb DEFAULT '{}'::jsonb NOT NULL,
    last_error text,
    completed_at timestamp with time zone DEFAULT now() NOT NULL,
    durable_job_id bigint,
    job_generation bigint,
    CONSTRAINT block_stage_results_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT block_stage_results_publication_pair_check CHECK ((((durable_job_id IS NULL) AND (job_generation IS NULL)) OR ((durable_job_id IS NOT NULL) AND (job_generation IS NOT NULL) AND (job_generation > 0)))),
    CONSTRAINT block_stage_results_stage_version_check CHECK ((stage_version > 0)),
    CONSTRAINT block_stage_results_state_check CHECK ((state = ANY (ARRAY['complete'::text, 'unavailable'::text, 'failed'::text]))),
    CONSTRAINT block_stage_results_pkey PRIMARY KEY (chain_id, block_hash, stage, stage_version),
    CONSTRAINT block_stage_results_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash),
    CONSTRAINT block_stage_results_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT block_stage_results_durable_job_fkey FOREIGN KEY (durable_job_id) REFERENCES durable_jobs(id)
);

CREATE TABLE durable_stage_publications (
    job_id bigint NOT NULL,
    job_generation bigint NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    stage text NOT NULL,
    stage_version integer NOT NULL,
    state text NOT NULL,
    details jsonb DEFAULT '{}'::jsonb NOT NULL,
    last_error text,
    committed_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT durable_stage_publications_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT durable_stage_publications_check CHECK ((((state = ANY (ARRAY['complete'::text, 'superseded'::text])) AND (last_error IS NULL)) OR ((state = ANY (ARRAY['failed'::text, 'unavailable'::text])) AND (last_error IS NOT NULL)))),
    CONSTRAINT durable_stage_publications_job_generation_check CHECK ((job_generation > 0)),
    CONSTRAINT durable_stage_publications_stage_version_check CHECK ((stage_version > 0)),
    CONSTRAINT durable_stage_publications_state_check CHECK ((state = ANY (ARRAY['complete'::text, 'failed'::text, 'unavailable'::text, 'superseded'::text]))),
    CONSTRAINT durable_stage_publications_pkey PRIMARY KEY (job_id, job_generation),
    CONSTRAINT durable_stage_publications_chain_id_block_number_block_has_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash),
    CONSTRAINT durable_stage_publications_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT durable_stage_publications_job_id_fkey FOREIGN KEY (job_id) REFERENCES durable_jobs(id)
);

CREATE TABLE durable_job_replay_requests (
    job_id bigint NOT NULL,
    source_kind text NOT NULL,
    source_key text NOT NULL,
    requested_generation bigint NOT NULL,
    requested_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT durable_job_replay_requests_positive_generation_check CHECK ((requested_generation >= 1)),
    CONSTRAINT durable_job_replay_requests_source_key_check CHECK (((length(source_key) >= 1) AND (length(source_key) <= 256))),
    CONSTRAINT durable_job_replay_requests_source_kind_check CHECK (((length(source_kind) >= 1) AND (length(source_kind) <= 64))),
    CONSTRAINT durable_job_replay_requests_job_id_requested_generation_key UNIQUE (job_id, requested_generation),
    CONSTRAINT durable_job_replay_requests_pkey PRIMARY KEY (job_id, source_kind, source_key),
    CONSTRAINT durable_job_replay_requests_job_id_fkey FOREIGN KEY (job_id) REFERENCES durable_jobs(id) ON DELETE CASCADE
);

CREATE TABLE reorg_events (
    id bigint NOT NULL GENERATED ALWAYS AS IDENTITY (
        SEQUENCE NAME reorg_events_id_seq
        START WITH 1
        INCREMENT BY 1
        NO MINVALUE
        NO MAXVALUE
        CACHE 1
    ),
    chain_id numeric(78,0) NOT NULL,
    ancestor_number numeric(78,0) NOT NULL,
    ancestor_hash bytea NOT NULL,
    old_tip_number numeric(78,0),
    old_tip_hash bytea,
    new_tip_number numeric(78,0),
    new_tip_hash bytea,
    detached jsonb NOT NULL,
    attached jsonb NOT NULL,
    reason text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT reorg_events_pkey PRIMARY KEY (id),
    CONSTRAINT reorg_events_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE repair_requests (
    id bigint NOT NULL GENERATED ALWAYS AS IDENTITY (
        SEQUENCE NAME repair_requests_id_seq
        START WITH 1
        INCREMENT BY 1
        NO MINVALUE
        NO MAXVALUE
        CACHE 1
    ),
    chain_id numeric(78,0) NOT NULL,
    operation text NOT NULL,
    stage text NOT NULL,
    from_block numeric(78,0) NOT NULL,
    to_block numeric(78,0) NOT NULL,
    allow_finalized boolean DEFAULT false NOT NULL,
    reason text NOT NULL,
    status text DEFAULT 'queued'::text NOT NULL,
    requested_at timestamp with time zone DEFAULT now() NOT NULL,
    started_at timestamp with time zone,
    completed_at timestamp with time zone,
    last_error text,
    CONSTRAINT repair_requests_check CHECK (((from_block >= (0)::numeric) AND (to_block >= from_block))),
    CONSTRAINT repair_requests_operation_check CHECK ((operation = ANY (ARRAY['repair'::text, 'reindex'::text]))),
    CONSTRAINT repair_requests_reason_check CHECK (((length(reason) >= 1) AND (length(reason) <= 1024))),
    CONSTRAINT repair_requests_status_check CHECK ((status = ANY (ARRAY['queued'::text, 'running'::text, 'done'::text, 'failed'::text, 'cancelled'::text]))),
    CONSTRAINT repair_requests_pkey PRIMARY KEY (id),
    CONSTRAINT repair_requests_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE runtime_events (
    id bigint NOT NULL GENERATED ALWAYS AS IDENTITY (
        SEQUENCE NAME runtime_events_id_seq
        START WITH 1
        INCREMENT BY 1
        NO MINVALUE
        NO MAXVALUE
        CACHE 1
    ),
    chain_id numeric(78,0) NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT runtime_events_event_type_check CHECK ((event_type = ANY (ARRAY['head'::text, 'reorg'::text, 'status'::text]))),
    CONSTRAINT runtime_events_payload_check CHECK ((octet_length((payload)::text) <= 8192)),
    CONSTRAINT runtime_events_pkey PRIMARY KEY (id),
    CONSTRAINT runtime_events_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE sync_runtime_status (
    chain_id numeric(78,0) NOT NULL,
    latest_number numeric(78,0),
    indexed_number numeric(78,0),
    ready boolean DEFAULT false NOT NULL,
    last_poll_at timestamp with time zone NOT NULL,
    last_error_code text DEFAULT ''::text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    highest_covered_number numeric(78,0),
    backfill_complete boolean DEFAULT false NOT NULL,
    CONSTRAINT sync_runtime_status_backfill_complete_check CHECK (((NOT backfill_complete) OR ((latest_number IS NOT NULL) AND (indexed_number IS NOT NULL) AND (highest_covered_number IS NOT NULL) AND (indexed_number >= latest_number) AND (indexed_number <= highest_covered_number)))),
    CONSTRAINT sync_runtime_status_check CHECK (((NOT ready) OR ((latest_number IS NOT NULL) AND (indexed_number IS NOT NULL) AND (indexed_number >= latest_number)))),
    CONSTRAINT sync_runtime_status_highest_covered_number_check CHECK (((highest_covered_number IS NULL) OR ((highest_covered_number >= (0)::numeric) AND ((indexed_number IS NULL) OR (indexed_number <= highest_covered_number))))),
    CONSTRAINT sync_runtime_status_indexed_number_check CHECK (((indexed_number IS NULL) OR (indexed_number >= (0)::numeric))),
    CONSTRAINT sync_runtime_status_last_error_code_check CHECK ((length(last_error_code) <= 64)),
    CONSTRAINT sync_runtime_status_last_error_code_check1 CHECK ((last_error_code ~ '^[a-z0-9_]*$'::text)),
    CONSTRAINT sync_runtime_status_latest_number_check CHECK (((latest_number IS NULL) OR (latest_number >= (0)::numeric))),
    CONSTRAINT sync_runtime_status_ready_requires_backfill_check CHECK (((NOT ready) OR backfill_complete)),
    CONSTRAINT sync_runtime_status_pkey PRIMARY KEY (chain_id),
    CONSTRAINT sync_runtime_status_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE sync_runtime_status_writer_leases (
    chain_id numeric(78,0) NOT NULL,
    reporter_id text NOT NULL,
    observed_latest_number numeric(78,0),
    observed_latest_known boolean CONSTRAINT sync_runtime_status_writer_lease_observed_latest_known_not_null NOT NULL,
    safety_halt boolean DEFAULT false NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT sync_runtime_status_writer_leases_check CHECK (((observed_latest_known AND (observed_latest_number IS NOT NULL) AND (observed_latest_number >= (0)::numeric)) OR ((NOT observed_latest_known) AND (observed_latest_number IS NULL)))),
    CONSTRAINT sync_runtime_status_writer_leases_reporter_id_check CHECK (((length(reporter_id) >= 1) AND (length(reporter_id) <= 128))),
    CONSTRAINT sync_runtime_status_writer_leases_pkey PRIMARY KEY (chain_id),
    CONSTRAINT sync_runtime_status_writer_leases_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE transactional_outbox (
    id bigint NOT NULL GENERATED ALWAYS AS IDENTITY (
        SEQUENCE NAME transactional_outbox_id_seq
        START WITH 1
        INCREMENT BY 1
        NO MINVALUE
        NO MAXVALUE
        CACHE 1
    ),
    chain_id numeric(78,0) NOT NULL,
    topic text NOT NULL,
    message_key text NOT NULL,
    payload jsonb NOT NULL,
    available_at timestamp with time zone DEFAULT now() NOT NULL,
    published_at timestamp with time zone,
    attempts integer DEFAULT 0 NOT NULL,
    last_error text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    generation bigint DEFAULT 1 NOT NULL,
    CONSTRAINT transactional_outbox_attempts_check CHECK ((attempts >= 0)),
    CONSTRAINT transactional_outbox_generation_check CHECK ((generation > 0)),
    CONSTRAINT transactional_outbox_chain_id_topic_message_key_key UNIQUE (chain_id, topic, message_key),
    CONSTRAINT transactional_outbox_pkey PRIMARY KEY (id),
    CONSTRAINT transactional_outbox_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);


-- Indexes

CREATE INDEX block_journals_canonical_idx ON block_journals USING btree (chain_id, stage, canonical, block_hash);

CREATE UNIQUE INDEX block_journals_job_generation_idx ON block_journals USING btree (durable_job_id, job_generation) WHERE (durable_job_id IS NOT NULL);

CREATE INDEX block_stage_results_height_idx ON block_stage_results USING btree (chain_id, block_number, stage, stage_version);

CREATE UNIQUE INDEX block_stage_results_job_generation_idx ON block_stage_results USING btree (durable_job_id, job_generation) WHERE (durable_job_id IS NOT NULL);

CREATE INDEX durable_jobs_claim_idx ON durable_jobs USING btree (chain_id, stage, priority DESC, available_at, id) WHERE (status = ANY (ARRAY['queued'::text, 'leased'::text]));

CREATE INDEX durable_jobs_replay_pending_idx ON durable_jobs USING btree (available_at, id) WHERE ((requested_generation > completed_generation) AND (status = ANY (ARRAY['queued'::text, 'leased'::text])));

CREATE INDEX repair_requests_active_metrics_idx ON repair_requests USING btree (chain_id, operation, status) WHERE (status = ANY (ARRAY['queued'::text, 'running'::text]));

CREATE INDEX repair_requests_pending_idx ON repair_requests USING btree (chain_id, requested_at, id) WHERE (status = 'queued'::text);

CREATE INDEX runtime_events_chain_id_idx ON runtime_events USING btree (chain_id, id);

CREATE INDEX transactional_outbox_pending_idx ON transactional_outbox USING btree (available_at, id) WHERE (published_at IS NULL);


-- Triggers

CREATE TRIGGER durable_jobs_publication_protocol BEFORE UPDATE ON durable_jobs FOR EACH ROW EXECUTE FUNCTION require_enrichment_publication_protocol();

CREATE CONSTRAINT TRIGGER durable_jobs_terminal_publication AFTER INSERT OR UPDATE ON durable_jobs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION enforce_enrichment_terminal_publication();

CREATE TRIGGER durable_stage_publications_append_only BEFORE DELETE OR UPDATE ON durable_stage_publications FOR EACH ROW EXECUTE FUNCTION reject_durable_stage_publication_mutation();
