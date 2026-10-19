-- Fresh-database baseline: proxy observations.

-- Functions

CREATE FUNCTION enforce_proxy_detection_generation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.durable_job_id IS NOT NULL AND NOT EXISTS (
        SELECT 1
        FROM durable_jobs AS job
        WHERE job.id = NEW.durable_job_id
          AND job.chain_id = NEW.chain_id
          AND job.stage = 'proxy'
          AND job.stage_version = NEW.stage_version
          AND job.payload->>'block_hash' =
              '0x' || encode(NEW.block_hash, 'hex')
          AND job.leased_generation = NEW.job_generation
          AND job.status = 'leased'
    ) THEN
        RAISE EXCEPTION 'proxy detection evidence is not from the active proxy@2 lease';
    END IF;
    RETURN NEW;
END
$$;

CREATE FUNCTION enforce_proxy_observation_generation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.durable_job_id IS NOT NULL AND NOT EXISTS (
        SELECT 1
        FROM durable_jobs AS job
        WHERE job.id = NEW.durable_job_id
          AND job.chain_id = NEW.chain_id
          AND job.stage = 'proxy'
          AND job.stage_version = NEW.observation_stage_version
          AND job.payload->>'block_hash' =
              '0x' || encode(NEW.observation_block_hash, 'hex')
          AND job.leased_generation = NEW.job_generation
          AND job.status = 'leased'
    ) THEN
        RAISE EXCEPTION 'proxy observation generation is not the active proxy@2 lease';
    END IF;
    RETURN NEW;
END
$$;

CREATE FUNCTION reject_proxy_evidence_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'proxy evidence is append-only';
END
$$;


-- Sequences

CREATE SEQUENCE beacon_observation_generations_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

CREATE SEQUENCE diamond_loupe_snapshots_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

CREATE SEQUENCE proxy_detection_evidence_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

CREATE SEQUENCE proxy_observation_generations_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


-- Tables

CREATE TABLE beacon_implementation_observations (
    chain_id numeric(78,0) NOT NULL,
    beacon_address bytea NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    beacon_code_hash bytea NOT NULL,
    implementation_address bytea CONSTRAINT beacon_implementation_observati_implementation_address_not_null NOT NULL,
    implementation_code_hash bytea CONSTRAINT beacon_implementation_observa_implementation_code_hash_not_null NOT NULL,
    stage_version integer DEFAULT 2 NOT NULL,
    confidence text NOT NULL,
    canonical boolean NOT NULL,
    details jsonb DEFAULT '{}'::jsonb NOT NULL,
    observed_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT beacon_implementation_observatio_implementation_code_hash_check CHECK ((octet_length(implementation_code_hash) = 32)),
    CONSTRAINT beacon_implementation_observations_beacon_address_check CHECK ((octet_length(beacon_address) = 20)),
    CONSTRAINT beacon_implementation_observations_beacon_code_hash_check CHECK ((octet_length(beacon_code_hash) = 32)),
    CONSTRAINT beacon_implementation_observations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT beacon_implementation_observations_confidence_check CHECK ((confidence = ANY (ARRAY['verified'::text, 'high'::text, 'inferred'::text, 'guess'::text]))),
    CONSTRAINT beacon_implementation_observations_details_check CHECK ((jsonb_typeof(details) = 'object'::text)),
    CONSTRAINT beacon_implementation_observations_implementation_address_check CHECK ((octet_length(implementation_address) = 20)),
    CONSTRAINT beacon_implementation_observations_stage_version_check CHECK ((stage_version = 2)),
    CONSTRAINT beacon_implementation_observations_pkey PRIMARY KEY (chain_id, beacon_address, block_hash, stage_version),
    CONSTRAINT beacon_implementation_observa_chain_id_block_number_block__fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash),
    CONSTRAINT beacon_implementation_observations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE beacon_observation_generations (
    id bigint NOT NULL DEFAULT nextval('beacon_observation_generations_id_seq'::regclass),
    chain_id numeric(78,0) NOT NULL,
    beacon_address bytea NOT NULL,
    observation_block_hash bytea NOT NULL,
    observation_stage_version integer CONSTRAINT beacon_observation_generatio_observation_stage_version_not_null NOT NULL,
    durable_job_id bigint,
    job_generation bigint,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT beacon_observation_generations_check CHECK ((((durable_job_id IS NULL) AND (job_generation IS NULL)) OR ((durable_job_id IS NOT NULL) AND (job_generation > 0)))),
    CONSTRAINT beacon_observation_generations_observation_stage_version_check CHECK ((observation_stage_version = 2)),
    CONSTRAINT beacon_observation_generation_chain_id_beacon_address_obser_key UNIQUE NULLS NOT DISTINCT (chain_id, beacon_address, observation_block_hash, observation_stage_version, durable_job_id, job_generation),
    CONSTRAINT beacon_observation_generations_pkey PRIMARY KEY (id),
    CONSTRAINT beacon_observation_generation_chain_id_beacon_address_obse_fkey FOREIGN KEY (chain_id, beacon_address, observation_block_hash, observation_stage_version) REFERENCES beacon_implementation_observations(chain_id, beacon_address, block_hash, stage_version) ON DELETE RESTRICT,
    CONSTRAINT beacon_observation_generations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT beacon_observation_generations_durable_job_id_fkey FOREIGN KEY (durable_job_id) REFERENCES durable_jobs(id)
);

CREATE TABLE diamond_cut_events (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    transaction_hash bytea NOT NULL,
    transaction_index bigint NOT NULL,
    log_index bigint NOT NULL,
    diamond_address bytea NOT NULL,
    init_address bytea NOT NULL,
    init_calldata bytea NOT NULL,
    cuts jsonb NOT NULL,
    stage_version integer DEFAULT 2 NOT NULL,
    canonical boolean NOT NULL,
    CONSTRAINT diamond_cut_events_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT diamond_cut_events_cuts_check CHECK ((jsonb_typeof(cuts) = 'array'::text)),
    CONSTRAINT diamond_cut_events_diamond_address_check CHECK ((octet_length(diamond_address) = 20)),
    CONSTRAINT diamond_cut_events_init_address_check CHECK ((octet_length(init_address) = 20)),
    CONSTRAINT diamond_cut_events_log_index_check CHECK ((log_index >= 0)),
    CONSTRAINT diamond_cut_events_stage_version_check CHECK ((stage_version = 2)),
    CONSTRAINT diamond_cut_events_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT diamond_cut_events_transaction_index_check CHECK ((transaction_index >= 0)),
    CONSTRAINT diamond_cut_events_pkey PRIMARY KEY (chain_id, block_hash, log_index, stage_version),
    CONSTRAINT diamond_cut_events_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash),
    CONSTRAINT diamond_cut_events_chain_id_block_number_block_hash_log_in_fkey FOREIGN KEY (chain_id, block_number, block_hash, log_index) REFERENCES logs(chain_id, block_number, block_hash, log_index),
    CONSTRAINT diamond_cut_events_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE diamond_loupe_snapshots (
    id bigint NOT NULL DEFAULT nextval('diamond_loupe_snapshots_id_seq'::regclass),
    chain_id numeric(78,0) NOT NULL,
    diamond_address bytea NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    stage_version integer DEFAULT 2 NOT NULL,
    detection_state text NOT NULL,
    completeness text NOT NULL,
    validation text NOT NULL,
    standard_diamond_cut text NOT NULL,
    standard_diamond_cut_facet bytea,
    loupe_interface_reported boolean,
    truncated boolean DEFAULT false NOT NULL,
    truncation_reason text,
    warnings jsonb DEFAULT '[]'::jsonb NOT NULL,
    canonical boolean NOT NULL,
    durable_job_id bigint,
    job_generation bigint,
    observed_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT diamond_loupe_snapshots_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT diamond_loupe_snapshots_check CHECK ((((standard_diamond_cut = 'present'::text) AND (standard_diamond_cut_facet IS NOT NULL) AND (octet_length(standard_diamond_cut_facet) = 20)) OR ((standard_diamond_cut <> 'present'::text) AND (standard_diamond_cut_facet IS NULL)))),
    CONSTRAINT diamond_loupe_snapshots_check1 CHECK ((truncated = (truncation_reason IS NOT NULL))),
    CONSTRAINT diamond_loupe_snapshots_check2 CHECK ((((durable_job_id IS NULL) AND (job_generation IS NULL)) OR ((durable_job_id IS NOT NULL) AND (job_generation > 0)))),
    CONSTRAINT diamond_loupe_snapshots_completeness_check CHECK ((completeness = ANY (ARRAY['complete'::text, 'partial'::text, 'unknown'::text]))),
    CONSTRAINT diamond_loupe_snapshots_detection_state_check CHECK ((detection_state = ANY (ARRAY['confirmed'::text, 'candidate'::text, 'inconsistent'::text]))),
    CONSTRAINT diamond_loupe_snapshots_diamond_address_check CHECK ((octet_length(diamond_address) = 20)),
    CONSTRAINT diamond_loupe_snapshots_stage_version_check CHECK ((stage_version = 2)),
    CONSTRAINT diamond_loupe_snapshots_standard_diamond_cut_check CHECK ((standard_diamond_cut = ANY (ARRAY['present'::text, 'absent'::text, 'unknown'::text]))),
    CONSTRAINT diamond_loupe_snapshots_truncation_reason_check CHECK (((truncation_reason IS NULL) OR (truncation_reason <> ''::text))),
    CONSTRAINT diamond_loupe_snapshots_validation_check CHECK ((validation = ANY (ARRAY['full'::text, 'sampled'::text, 'interface-only'::text]))),
    CONSTRAINT diamond_loupe_snapshots_warnings_check CHECK ((jsonb_typeof(warnings) = 'array'::text)),
    CONSTRAINT diamond_loupe_snapshots_chain_id_diamond_address_block_hash_key UNIQUE NULLS NOT DISTINCT (chain_id, diamond_address, block_hash, stage_version, durable_job_id, job_generation),
    CONSTRAINT diamond_loupe_snapshots_pkey PRIMARY KEY (id),
    CONSTRAINT diamond_loupe_snapshots_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash),
    CONSTRAINT diamond_loupe_snapshots_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT diamond_loupe_snapshots_durable_job_id_fkey FOREIGN KEY (durable_job_id) REFERENCES durable_jobs(id)
);

CREATE TABLE diamond_loupe_facets (
    snapshot_id bigint NOT NULL,
    facet_address bytea NOT NULL,
    facet_kind text NOT NULL,
    code_exists boolean NOT NULL,
    code_hash bytea,
    CONSTRAINT diamond_loupe_facets_check CHECK ((((facet_kind = 'facet'::text) AND (code_exists = (code_hash IS NOT NULL))) OR (facet_kind = 'immutable'::text))),
    CONSTRAINT diamond_loupe_facets_code_hash_check CHECK (((code_hash IS NULL) OR (octet_length(code_hash) = 32))),
    CONSTRAINT diamond_loupe_facets_facet_address_check CHECK ((octet_length(facet_address) = 20)),
    CONSTRAINT diamond_loupe_facets_facet_kind_check CHECK ((facet_kind = ANY (ARRAY['facet'::text, 'immutable'::text]))),
    CONSTRAINT diamond_loupe_facets_pkey PRIMARY KEY (snapshot_id, facet_address),
    CONSTRAINT diamond_loupe_facets_snapshot_id_fkey FOREIGN KEY (snapshot_id) REFERENCES diamond_loupe_snapshots(id) ON DELETE CASCADE
);

CREATE TABLE diamond_loupe_selectors (
    snapshot_id bigint NOT NULL,
    selector bytea NOT NULL,
    facet_address bytea NOT NULL,
    CONSTRAINT diamond_loupe_selectors_facet_address_check CHECK ((octet_length(facet_address) = 20)),
    CONSTRAINT diamond_loupe_selectors_selector_check CHECK ((octet_length(selector) = 4)),
    CONSTRAINT diamond_loupe_selectors_pkey PRIMARY KEY (snapshot_id, selector),
    CONSTRAINT diamond_loupe_selectors_snapshot_id_facet_address_fkey FOREIGN KEY (snapshot_id, facet_address) REFERENCES diamond_loupe_facets(snapshot_id, facet_address) ON DELETE CASCADE
);

CREATE TABLE diamond_selector_changes (
    chain_id numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    log_index bigint NOT NULL,
    stage_version integer DEFAULT 2 NOT NULL,
    cut_index integer NOT NULL,
    selector_index integer NOT NULL,
    selector bytea NOT NULL,
    action smallint NOT NULL,
    facet_address bytea NOT NULL,
    CONSTRAINT diamond_selector_changes_action_check CHECK ((action = ANY (ARRAY[0, 1, 2]))),
    CONSTRAINT diamond_selector_changes_cut_index_check CHECK ((cut_index >= 0)),
    CONSTRAINT diamond_selector_changes_facet_address_check CHECK ((octet_length(facet_address) = 20)),
    CONSTRAINT diamond_selector_changes_selector_check CHECK ((octet_length(selector) = 4)),
    CONSTRAINT diamond_selector_changes_selector_index_check CHECK ((selector_index >= 0)),
    CONSTRAINT diamond_selector_changes_pkey PRIMARY KEY (chain_id, block_hash, log_index, stage_version, cut_index, selector_index),
    CONSTRAINT diamond_selector_changes_chain_id_block_hash_log_index_sta_fkey FOREIGN KEY (chain_id, block_hash, log_index, stage_version) REFERENCES diamond_cut_events(chain_id, block_hash, log_index, stage_version) ON DELETE CASCADE
);

CREATE TABLE proxy_observations (
    chain_id numeric(78,0) NOT NULL,
    proxy_address bytea NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    proxy_code_hash bytea NOT NULL,
    proxy_kind text NOT NULL,
    implementation_address bytea,
    beacon_address bytea,
    implementation_code_hash bytea,
    confidence text NOT NULL,
    canonical boolean NOT NULL,
    details jsonb DEFAULT '{}'::jsonb NOT NULL,
    stage_version integer DEFAULT 2 NOT NULL,
    proxy_pattern text DEFAULT 'unknown'::text NOT NULL,
    standard_version text,
    admin_address bytea,
    admin_code_hash bytea,
    beacon_code_hash bytea,
    immutable_args bytea,
    evidence_state text DEFAULT 'generic'::text NOT NULL,
    CONSTRAINT proxy_observations_admin_identity_check CHECK ((((admin_address IS NULL) AND (admin_code_hash IS NULL)) OR ((octet_length(admin_address) = 20) AND (octet_length(admin_code_hash) = 32)))),
    CONSTRAINT proxy_observations_beacon_address_check CHECK (((beacon_address IS NULL) OR (octet_length(beacon_address) = 20))),
    CONSTRAINT proxy_observations_beacon_code_hash_check CHECK (((beacon_code_hash IS NULL) OR (octet_length(beacon_code_hash) = 32))),
    CONSTRAINT proxy_observations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT proxy_observations_confidence_check CHECK ((confidence = ANY (ARRAY['verified'::text, 'high'::text, 'inferred'::text, 'guess'::text]))),
    CONSTRAINT proxy_observations_evidence_state_check CHECK ((evidence_state = ANY (ARRAY['exact'::text, 'partial'::text, 'generic'::text]))),
    CONSTRAINT proxy_observations_immutable_args_check CHECK (((immutable_args IS NULL) OR (octet_length(immutable_args) <= 24531))),
    CONSTRAINT proxy_observations_implementation_address_check CHECK (((implementation_address IS NULL) OR (octet_length(implementation_address) = 20))),
    CONSTRAINT proxy_observations_implementation_code_hash_check CHECK (((implementation_code_hash IS NULL) OR (octet_length(implementation_code_hash) = 32))),
    CONSTRAINT proxy_observations_pattern_check CHECK ((proxy_pattern = ANY (ARRAY['clone'::text, 'erc1967'::text, 'transparent'::text, 'uups'::text, 'beacon'::text, 'unknown'::text]))),
    CONSTRAINT proxy_observations_proxy_address_check CHECK ((octet_length(proxy_address) = 20)),
    CONSTRAINT proxy_observations_proxy_code_hash_check CHECK ((octet_length(proxy_code_hash) = 32)),
    CONSTRAINT proxy_observations_proxy_kind_check CHECK ((proxy_kind = ANY (ARRAY['eip1167'::text, 'cwia'::text, 'eip1967'::text, 'beacon'::text, 'unknown'::text]))),
    CONSTRAINT proxy_observations_stage_version_check CHECK ((stage_version = ANY (ARRAY[1, 2]))),
    CONSTRAINT proxy_observations_standard_check CHECK (((standard_version IS NULL) OR (standard_version = '5.6.1'::text))),
    CONSTRAINT proxy_observations_pkey PRIMARY KEY (chain_id, proxy_address, block_hash, stage_version),
    CONSTRAINT proxy_observations_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash),
    CONSTRAINT proxy_observations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE proxy_detection_evidence (
    id bigint NOT NULL DEFAULT nextval('proxy_detection_evidence_id_seq'::regclass),
    chain_id numeric(78,0) NOT NULL,
    address bytea NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    stage_version integer DEFAULT 2 NOT NULL,
    code_hash bytea NOT NULL,
    candidate_kind text NOT NULL,
    detection_state text NOT NULL,
    reason text NOT NULL,
    canonical boolean NOT NULL,
    durable_job_id bigint,
    job_generation bigint,
    details jsonb DEFAULT '{}'::jsonb NOT NULL,
    observed_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT proxy_detection_evidence_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT proxy_detection_evidence_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT proxy_detection_evidence_candidate_kind_check CHECK ((candidate_kind = ANY (ARRAY['proxy'::text, 'beacon'::text, 'proxy_resolution'::text]))),
    CONSTRAINT proxy_detection_evidence_check CHECK ((((durable_job_id IS NULL) AND (job_generation IS NULL)) OR ((durable_job_id IS NOT NULL) AND (job_generation > 0)))),
    CONSTRAINT proxy_detection_evidence_code_hash_check CHECK ((octet_length(code_hash) = 32)),
    CONSTRAINT proxy_detection_evidence_details_check CHECK ((jsonb_typeof(details) = 'object'::text)),
    CONSTRAINT proxy_detection_evidence_detection_state_check CHECK ((detection_state = ANY (ARRAY['not_detected'::text, 'rejected'::text, 'confirmed'::text, 'candidate'::text, 'inconsistent'::text, 'unknown'::text]))),
    CONSTRAINT proxy_detection_evidence_reason_check CHECK ((reason = ANY (ARRAY['empty_code'::text, 'not_proxy'::text, 'minimal_zero_implementation'::text, 'immutable_args_too_large'::text, 'immutable_args_creation_unverified'::text, 'self_implementation'::text, 'cwia_invalid_length'::text, 'cwia_immutable_args_too_large'::text, 'cwia_zero_implementation'::text, 'cwia_self_implementation'::text, 'implementation_has_no_code'::text, 'invalid_slot_address'::text, 'ambiguous_slots'::text, 'beacon_has_no_code'::text, 'invalid_beacon_implementation'::text, 'resolver'::text]))),
    CONSTRAINT proxy_detection_evidence_stage_version_check CHECK ((stage_version = 2)),
    CONSTRAINT proxy_detection_evidence_shape_check CHECK ((((candidate_kind = 'proxy_resolution'::text) AND (reason = 'resolver'::text) AND (detection_state = ANY (ARRAY['confirmed'::text, 'candidate'::text, 'inconsistent'::text, 'not_detected'::text, 'unknown'::text]))) OR ((candidate_kind <> 'proxy_resolution'::text) AND (reason <> 'resolver'::text) AND (detection_state = ANY (ARRAY['not_detected'::text, 'rejected'::text]))))),
    CONSTRAINT proxy_detection_evidence_chain_id_address_block_hash_stage__key UNIQUE NULLS NOT DISTINCT (chain_id, address, block_hash, stage_version, candidate_kind, durable_job_id, job_generation),
    CONSTRAINT proxy_detection_evidence_pkey PRIMARY KEY (id),
    CONSTRAINT proxy_detection_evidence_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash),
    CONSTRAINT proxy_detection_evidence_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT proxy_detection_evidence_durable_job_id_fkey FOREIGN KEY (durable_job_id) REFERENCES durable_jobs(id)
);

CREATE TABLE proxy_initialization_events (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    log_index bigint NOT NULL,
    transaction_hash bytea NOT NULL,
    contract_address bytea NOT NULL,
    version numeric(20,0) NOT NULL,
    stage_version integer DEFAULT 2 NOT NULL,
    canonical boolean NOT NULL,
    CONSTRAINT proxy_initialization_events_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT proxy_initialization_events_contract_address_check CHECK ((octet_length(contract_address) = 20)),
    CONSTRAINT proxy_initialization_events_log_index_check CHECK ((log_index >= 0)),
    CONSTRAINT proxy_initialization_events_stage_version_check CHECK ((stage_version = 2)),
    CONSTRAINT proxy_initialization_events_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT proxy_initialization_events_version_check CHECK (((version >= (0)::numeric) AND (version <= '18446744073709551615'::numeric))),
    CONSTRAINT proxy_initialization_events_pkey PRIMARY KEY (chain_id, block_hash, log_index, stage_version),
    CONSTRAINT proxy_initialization_events_chain_id_block_number_block_h_fkey1 FOREIGN KEY (chain_id, block_number, block_hash, log_index) REFERENCES logs(chain_id, block_number, block_hash, log_index),
    CONSTRAINT proxy_initialization_events_chain_id_block_number_block_ha_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash),
    CONSTRAINT proxy_initialization_events_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE proxy_observation_generations (
    id bigint NOT NULL DEFAULT nextval('proxy_observation_generations_id_seq'::regclass),
    chain_id numeric(78,0) NOT NULL,
    proxy_address bytea NOT NULL,
    observation_block_hash bytea NOT NULL,
    observation_stage_version integer CONSTRAINT proxy_observation_generation_observation_stage_version_not_null NOT NULL,
    durable_job_id bigint,
    job_generation bigint,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT proxy_observation_generations_check CHECK ((((durable_job_id IS NULL) AND (job_generation IS NULL)) OR ((durable_job_id IS NOT NULL) AND (job_generation > 0)))),
    CONSTRAINT proxy_observation_generations_observation_stage_version_check CHECK ((observation_stage_version = 2)),
    CONSTRAINT proxy_observation_generations_chain_id_proxy_address_observ_key UNIQUE NULLS NOT DISTINCT (chain_id, proxy_address, observation_block_hash, observation_stage_version, durable_job_id, job_generation),
    CONSTRAINT proxy_observation_generations_pkey PRIMARY KEY (id),
    CONSTRAINT proxy_observation_generations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT proxy_observation_generations_chain_id_proxy_address_obser_fkey FOREIGN KEY (chain_id, proxy_address, observation_block_hash, observation_stage_version) REFERENCES proxy_observations(chain_id, proxy_address, block_hash, stage_version) ON DELETE RESTRICT,
    CONSTRAINT proxy_observation_generations_durable_job_id_fkey FOREIGN KEY (durable_job_id) REFERENCES durable_jobs(id)
);

CREATE TABLE proxy_upgrade_events (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    log_index bigint NOT NULL,
    transaction_hash bytea NOT NULL,
    emitter_address bytea NOT NULL,
    event_kind text NOT NULL,
    target_address bytea NOT NULL,
    stage_version integer DEFAULT 2 NOT NULL,
    canonical boolean NOT NULL,
    CONSTRAINT proxy_upgrade_events_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT proxy_upgrade_events_emitter_address_check CHECK ((octet_length(emitter_address) = 20)),
    CONSTRAINT proxy_upgrade_events_event_kind_check CHECK ((event_kind = ANY (ARRAY['implementation'::text, 'beacon'::text]))),
    CONSTRAINT proxy_upgrade_events_log_index_check CHECK ((log_index >= 0)),
    CONSTRAINT proxy_upgrade_events_stage_version_check CHECK ((stage_version = 2)),
    CONSTRAINT proxy_upgrade_events_target_address_check CHECK ((octet_length(target_address) = 20)),
    CONSTRAINT proxy_upgrade_events_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT proxy_upgrade_events_pkey PRIMARY KEY (chain_id, block_hash, log_index, stage_version),
    CONSTRAINT proxy_upgrade_events_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash),
    CONSTRAINT proxy_upgrade_events_chain_id_block_number_block_hash_log__fkey FOREIGN KEY (chain_id, block_number, block_hash, log_index) REFERENCES logs(chain_id, block_number, block_hash, log_index),
    CONSTRAINT proxy_upgrade_events_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);


-- Dependency completion

ALTER SEQUENCE beacon_observation_generations_id_seq OWNED BY beacon_observation_generations.id;

ALTER SEQUENCE diamond_loupe_snapshots_id_seq OWNED BY diamond_loupe_snapshots.id;

ALTER SEQUENCE proxy_detection_evidence_id_seq OWNED BY proxy_detection_evidence.id;

ALTER SEQUENCE proxy_observation_generations_id_seq OWNED BY proxy_observation_generations.id;


-- Indexes

CREATE INDEX beacon_implementation_history_idx ON beacon_implementation_observations USING btree (chain_id, beacon_address, block_number DESC, block_hash DESC) WHERE canonical;

CREATE INDEX diamond_cut_events_history_idx ON diamond_cut_events USING btree (chain_id, diamond_address, block_number, transaction_index, log_index) WHERE (canonical AND (stage_version = 2));

CREATE INDEX diamond_loupe_selectors_facet_idx ON diamond_loupe_selectors USING btree (snapshot_id, facet_address, selector);

CREATE INDEX diamond_loupe_snapshots_current_idx ON diamond_loupe_snapshots USING btree (chain_id, diamond_address, block_number DESC, id DESC) WHERE (canonical AND (stage_version = 2));

CREATE INDEX diamond_selector_changes_selector_idx ON diamond_selector_changes USING btree (chain_id, selector, block_hash, log_index);

CREATE INDEX proxy_detection_evidence_current_idx ON proxy_detection_evidence USING btree (chain_id, address, block_number DESC, block_hash DESC) WHERE (canonical AND (stage_version = 2));

CREATE INDEX proxy_detection_evidence_negative_shadow_idx ON proxy_detection_evidence USING btree (chain_id, address, candidate_kind, code_hash, block_number DESC, block_hash DESC) WHERE (canonical AND (stage_version = 2));

CREATE INDEX proxy_history_idx ON proxy_observations USING btree (chain_id, proxy_address, block_number DESC) WHERE canonical;

CREATE INDEX proxy_initialization_events_contract_idx ON proxy_initialization_events USING btree (chain_id, contract_address, block_number DESC, log_index DESC) WHERE (canonical AND (stage_version = 2));

CREATE INDEX proxy_observations_v2_beacon_idx ON proxy_observations USING btree (chain_id, beacon_address, block_number DESC, block_hash DESC) WHERE (canonical AND (stage_version = 2) AND (beacon_address IS NOT NULL));

CREATE INDEX proxy_observations_v2_history_idx ON proxy_observations USING btree (chain_id, proxy_address, block_number DESC, block_hash DESC) WHERE (canonical AND (stage_version = 2));

CREATE INDEX proxy_upgrade_events_emitter_idx ON proxy_upgrade_events USING btree (chain_id, emitter_address, block_number DESC, log_index DESC) WHERE (canonical AND (stage_version = 2));


-- Triggers

CREATE TRIGGER beacon_observation_generations_immutable BEFORE DELETE OR UPDATE ON beacon_observation_generations FOR EACH ROW EXECUTE FUNCTION reject_proxy_evidence_mutation();

CREATE TRIGGER beacon_observation_generations_source_guard BEFORE INSERT ON beacon_observation_generations FOR EACH ROW EXECUTE FUNCTION enforce_proxy_observation_generation();

CREATE TRIGGER diamond_loupe_snapshots_source_guard BEFORE INSERT ON diamond_loupe_snapshots FOR EACH ROW EXECUTE FUNCTION enforce_proxy_detection_generation();

CREATE TRIGGER proxy_detection_evidence_source_guard BEFORE INSERT ON proxy_detection_evidence FOR EACH ROW EXECUTE FUNCTION enforce_proxy_detection_generation();

CREATE TRIGGER proxy_observation_generations_immutable BEFORE DELETE OR UPDATE ON proxy_observation_generations FOR EACH ROW EXECUTE FUNCTION reject_proxy_evidence_mutation();

CREATE TRIGGER proxy_observation_generations_source_guard BEFORE INSERT ON proxy_observation_generations FOR EACH ROW EXECUTE FUNCTION enforce_proxy_observation_generation();

CREATE TRIGGER verified_contract_proxy_artifacts_immutable BEFORE DELETE OR UPDATE ON verified_contract_proxy_artifacts FOR EACH ROW EXECUTE FUNCTION reject_proxy_evidence_mutation();
