-- Fresh-database baseline: execution.

-- Tables

CREATE TABLE abi_decodings (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    object_kind text NOT NULL,
    transaction_hash bytea NOT NULL,
    object_index text NOT NULL,
    target_address bytea NOT NULL,
    target_code_hash bytea NOT NULL,
    abi_kind text NOT NULL,
    status text NOT NULL,
    signature text,
    source text,
    confidence text,
    arguments jsonb DEFAULT '[]'::jsonb NOT NULL,
    candidates jsonb DEFAULT '[]'::jsonb NOT NULL,
    warning text DEFAULT ''::text NOT NULL,
    canonical boolean NOT NULL,
    decoded_at timestamp with time zone DEFAULT now() NOT NULL,
    source_address bytea,
    source_code_hash bytea,
    return_status text DEFAULT 'not_applicable'::text NOT NULL,
    return_arguments jsonb DEFAULT '[]'::jsonb NOT NULL,
    decoding_kind text DEFAULT 'function'::text NOT NULL,
    CONSTRAINT abi_decodings_abi_kind_v3_check CHECK ((abi_kind = ANY (ARRAY['function'::text, 'constructor'::text, 'event'::text, 'error'::text]))),
    CONSTRAINT abi_decodings_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT abi_decodings_check1 CHECK (((status = ANY (ARRAY['decoded'::text, 'ambiguous'::text])) OR (signature IS NULL))),
    CONSTRAINT abi_decodings_decoding_kind_check CHECK ((decoding_kind = ANY (ARRAY['function'::text, 'constructor'::text, 'event'::text, 'error'::text]))),
    CONSTRAINT abi_decodings_object_kind_v3_check CHECK ((object_kind = ANY (ARRAY['transaction_calldata'::text, 'log'::text, 'trace_calldata'::text, 'trace_constructor'::text, 'trace_revert'::text]))),
    CONSTRAINT abi_decodings_return_arguments_array_check CHECK ((jsonb_typeof(return_arguments) = 'array'::text)),
    CONSTRAINT abi_decodings_return_status_check CHECK ((return_status = ANY (ARRAY['decoded'::text, 'empty'::text, 'unknown'::text, 'malformed'::text, 'unavailable'::text, 'not_applicable'::text]))),
    CONSTRAINT abi_decodings_source_address_length_check CHECK (((source_address IS NULL) OR (octet_length(source_address) = 20))),
    CONSTRAINT abi_decodings_source_code_hash_length_check CHECK (((source_code_hash IS NULL) OR (octet_length(source_code_hash) = 32))),
    CONSTRAINT abi_decodings_source_confidence_v3_check CHECK ((((source IS NULL) AND (confidence IS NULL)) OR ((source = 'verified'::text) AND (confidence = 'verified'::text)) OR ((source = ANY (ARRAY['code_hash'::text, 'proxy_implementation'::text, 'diamond_facet'::text, 'builtin'::text])) AND (confidence = 'high'::text)) OR ((source = 'signature_database'::text) AND (confidence = 'guess'::text)))),
    CONSTRAINT abi_decodings_source_identity_pair_check CHECK (((source_address IS NULL) = (source_code_hash IS NULL))),
    CONSTRAINT abi_decodings_status_check CHECK ((status = ANY (ARRAY['decoded'::text, 'unknown'::text, 'malformed'::text, 'ambiguous'::text]))),
    CONSTRAINT abi_decodings_target_address_check CHECK ((octet_length(target_address) = 20)),
    CONSTRAINT abi_decodings_target_code_hash_check CHECK ((octet_length(target_code_hash) = 32)),
    CONSTRAINT abi_decodings_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT abi_decodings_pkey PRIMARY KEY (chain_id, block_number, block_hash, object_kind, transaction_hash, object_index),
    CONSTRAINT abi_decodings_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash)
)
PARTITION BY RANGE (block_number);

CREATE TABLE abi_decodings_default PARTITION OF abi_decodings DEFAULT;

CREATE TABLE abi_decodings_p_0_1000000 PARTITION OF abi_decodings FOR VALUES FROM ('0') TO ('1000000');

CREATE TABLE abi_signature_candidates (
    kind text NOT NULL,
    identifier bytea NOT NULL,
    signature text NOT NULL,
    abi_entry jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT abi_signature_candidates_abi_entry_check CHECK ((jsonb_typeof(abi_entry) = 'object'::text)),
    CONSTRAINT abi_signature_candidates_check CHECK ((((kind = ANY (ARRAY['function'::text, 'error'::text])) AND (octet_length(identifier) = 4)) OR ((kind = 'event'::text) AND (octet_length(identifier) = 32)))),
    CONSTRAINT abi_signature_candidates_kind_check CHECK ((kind = ANY (ARRAY['function'::text, 'event'::text, 'error'::text]))),
    CONSTRAINT abi_signature_candidates_signature_check CHECK ((signature <> ''::text)),
    CONSTRAINT abi_signature_candidates_pkey PRIMARY KEY (kind, identifier, signature)
);

CREATE TABLE transactions (
    chain_id numeric(78,0) NOT NULL,
    hash bytea NOT NULL,
    tx_type numeric(78,0),
    raw jsonb NOT NULL,
    inserted_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT transactions_hash_check CHECK ((octet_length(hash) = 32)),
    CONSTRAINT transactions_pkey PRIMARY KEY (chain_id, hash),
    CONSTRAINT transactions_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE address_activities (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    transaction_hash bytea NOT NULL,
    activity_index integer NOT NULL,
    address bytea NOT NULL,
    activity_kind text NOT NULL,
    canonical boolean NOT NULL,
    details jsonb DEFAULT '{}'::jsonb NOT NULL,
    CONSTRAINT address_activities_activity_index_check CHECK ((activity_index >= 0)),
    CONSTRAINT address_activities_activity_kind_check CHECK ((activity_kind = ANY (ARRAY['transaction'::text, 'token'::text, 'trace'::text, 'contract_creation'::text]))),
    CONSTRAINT address_activities_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT address_activities_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT address_activities_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT address_activities_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, activity_index, address),
    CONSTRAINT address_activities_chain_id_transaction_hash_fkey FOREIGN KEY (chain_id, transaction_hash) REFERENCES transactions(chain_id, hash)
)
PARTITION BY RANGE (block_number);

CREATE TABLE address_activities_default PARTITION OF address_activities DEFAULT;

CREATE TABLE address_activities_p_0_1000000 PARTITION OF address_activities FOR VALUES FROM ('0') TO ('1000000');

CREATE TABLE contract_abis (
    chain_id numeric(78,0) NOT NULL,
    address bytea NOT NULL,
    code_hash bytea NOT NULL,
    source text NOT NULL,
    confidence text NOT NULL,
    abi jsonb NOT NULL,
    valid_from_block numeric(78,0) NOT NULL,
    valid_to_block numeric(78,0),
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    source_address bytea NOT NULL,
    source_code_hash bytea NOT NULL,
    canonical boolean NOT NULL,
    selector_scope bytea DEFAULT decode(repeat('00'::text, 32), 'hex'::text) NOT NULL,
    CONSTRAINT contract_abis_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT contract_abis_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT contract_abis_check CHECK (((valid_to_block IS NULL) OR (valid_to_block >= valid_from_block))),
    CONSTRAINT contract_abis_check1 CHECK ((block_number >= valid_from_block)),
    CONSTRAINT contract_abis_check2 CHECK (((valid_to_block IS NULL) OR (block_number <= valid_to_block))),
    CONSTRAINT contract_abis_code_hash_check CHECK ((octet_length(code_hash) = 32)),
    CONSTRAINT contract_abis_confidence_v3_check CHECK ((((source = 'verified'::text) AND (confidence = 'verified'::text)) OR ((source = ANY (ARRAY['code_hash'::text, 'proxy_implementation'::text, 'diamond_facet'::text])) AND (confidence = 'high'::text)) OR ((source = 'signature_database'::text) AND (confidence = 'guess'::text)))),
    CONSTRAINT contract_abis_selector_scope_v3_check CHECK (((octet_length(selector_scope) = 32) AND (((source = 'diamond_facet'::text) AND (selector_scope <> decode(repeat('00'::text, 32), 'hex'::text))) OR ((source <> 'diamond_facet'::text) AND (selector_scope = decode(repeat('00'::text, 32), 'hex'::text)))))),
    CONSTRAINT contract_abis_source_address_check CHECK ((octet_length(source_address) = 20)),
    CONSTRAINT contract_abis_source_code_hash_check CHECK ((octet_length(source_code_hash) = 32)),
    CONSTRAINT contract_abis_source_identity_v3_check CHECK (((source = ANY (ARRAY['code_hash'::text, 'proxy_implementation'::text, 'diamond_facet'::text])) OR ((source_address = address) AND (source_code_hash = code_hash)))),
    CONSTRAINT contract_abis_source_v3_check CHECK ((source = ANY (ARRAY['verified'::text, 'code_hash'::text, 'proxy_implementation'::text, 'diamond_facet'::text, 'signature_database'::text]))),
    CONSTRAINT contract_abis_valid_from_block_check CHECK ((valid_from_block >= (0)::numeric)),
    CONSTRAINT contract_abis_pkey PRIMARY KEY (chain_id, address, code_hash, source, source_address, source_code_hash, selector_scope, valid_from_block, block_hash),
    CONSTRAINT contract_abis_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash),
    CONSTRAINT contract_abis_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE contract_code_observations (
    chain_id numeric(78,0) NOT NULL,
    address bytea NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    code_hash bytea NOT NULL,
    code bytea,
    canonical boolean NOT NULL,
    observed_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT contract_code_observations_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT contract_code_observations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT contract_code_observations_code_hash_check CHECK ((octet_length(code_hash) = 32)),
    CONSTRAINT contract_code_observations_pkey PRIMARY KEY (chain_id, address, block_hash),
    CONSTRAINT contract_code_observations_chain_id_block_number_block_has_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash),
    CONSTRAINT contract_code_observations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE transaction_inclusions (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    tx_index bigint NOT NULL,
    tx_hash bytea NOT NULL,
    raw jsonb NOT NULL,
    CONSTRAINT transaction_inclusions_tx_index_check CHECK ((tx_index >= 0)),
    CONSTRAINT transaction_inclusions_chain_id_block_number_block_hash_tx__key UNIQUE (chain_id, block_number, block_hash, tx_hash),
    CONSTRAINT transaction_inclusions_position_hash_key UNIQUE (chain_id, block_number, block_hash, tx_index, tx_hash),
    CONSTRAINT transaction_inclusions_pkey PRIMARY KEY (chain_id, block_number, block_hash, tx_index),
    CONSTRAINT transaction_inclusions_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash),
    CONSTRAINT transaction_inclusions_chain_id_tx_hash_fkey FOREIGN KEY (chain_id, tx_hash) REFERENCES transactions(chain_id, hash)
)
PARTITION BY RANGE (block_number);

CREATE TABLE transaction_inclusions_default PARTITION OF transaction_inclusions DEFAULT;

CREATE TABLE transaction_inclusions_p_0_1000000 PARTITION OF transaction_inclusions FOR VALUES FROM ('0') TO ('1000000');

CREATE TABLE normalized_traces (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    transaction_hash bytea NOT NULL,
    transaction_index bigint NOT NULL,
    trace_path text NOT NULL,
    parent_path text,
    depth integer NOT NULL,
    call_type text NOT NULL,
    from_address bytea,
    to_address bytea,
    created_address bytea,
    value numeric(78,0),
    gas numeric(78,0),
    gas_used numeric(78,0),
    input bytea,
    output bytea,
    error text,
    reverted boolean NOT NULL,
    canonical boolean NOT NULL,
    direct_reverted boolean DEFAULT false NOT NULL,
    execution_address bytea,
    execution_code_hash bytea,
    execution_resolution text DEFAULT 'unavailable'::text NOT NULL,
    CONSTRAINT normalized_traces_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT normalized_traces_created_address_check CHECK (((created_address IS NULL) OR (octet_length(created_address) = 20))),
    CONSTRAINT normalized_traces_depth_check CHECK ((depth >= 0)),
    CONSTRAINT normalized_traces_execution_address_length_check CHECK (((execution_address IS NULL) OR (octet_length(execution_address) = 20))),
    CONSTRAINT normalized_traces_execution_code_hash_length_check CHECK (((execution_code_hash IS NULL) OR (octet_length(execution_code_hash) = 32))),
    CONSTRAINT normalized_traces_execution_identity_check CHECK ((((execution_resolution = ANY (ARRAY['direct'::text, 'eip7702_delegate'::text])) AND (execution_address IS NOT NULL) AND (execution_code_hash IS NOT NULL)) OR ((execution_resolution = ANY (ARRAY['empty'::text, 'not_applicable'::text])) AND (execution_address IS NULL) AND (execution_code_hash IS NULL)) OR ((execution_resolution = 'unavailable'::text) AND (execution_code_hash IS NULL)))),
    CONSTRAINT normalized_traces_execution_resolution_check CHECK ((execution_resolution = ANY (ARRAY['direct'::text, 'eip7702_delegate'::text, 'empty'::text, 'unavailable'::text, 'not_applicable'::text]))),
    CONSTRAINT normalized_traces_from_address_check CHECK (((from_address IS NULL) OR (octet_length(from_address) = 20))),
    CONSTRAINT normalized_traces_gas_check CHECK (((gas IS NULL) OR (gas >= (0)::numeric))),
    CONSTRAINT normalized_traces_gas_used_check CHECK (((gas_used IS NULL) OR (gas_used >= (0)::numeric))),
    CONSTRAINT normalized_traces_to_address_check CHECK (((to_address IS NULL) OR (octet_length(to_address) = 20))),
    CONSTRAINT normalized_traces_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT normalized_traces_value_check CHECK (((value IS NULL) OR (value >= (0)::numeric))),
    CONSTRAINT normalized_traces_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, trace_path),
    CONSTRAINT normalized_traces_chain_id_block_number_block_hash_transac_fkey FOREIGN KEY (chain_id, block_number, block_hash, transaction_index) REFERENCES transaction_inclusions(chain_id, block_number, block_hash, tx_index)
)
PARTITION BY RANGE (block_number);

CREATE TABLE normalized_traces_default PARTITION OF normalized_traces DEFAULT;

CREATE TABLE normalized_traces_p_0_1000000 PARTITION OF normalized_traces FOR VALUES FROM ('0') TO ('1000000');

CREATE TABLE receipts (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    tx_index bigint NOT NULL,
    tx_hash bytea NOT NULL,
    raw jsonb NOT NULL,
    CONSTRAINT receipts_tx_index_check CHECK ((tx_index >= 0)),
    CONSTRAINT receipts_chain_id_block_number_block_hash_tx_hash_key UNIQUE (chain_id, block_number, block_hash, tx_hash),
    CONSTRAINT receipts_pkey PRIMARY KEY (chain_id, block_number, block_hash, tx_index),
    CONSTRAINT receipts_chain_id_block_number_block_hash_tx_index_fkey FOREIGN KEY (chain_id, block_number, block_hash, tx_index) REFERENCES transaction_inclusions(chain_id, block_number, block_hash, tx_index)
)
PARTITION BY RANGE (block_number);

CREATE TABLE receipts_default PARTITION OF receipts DEFAULT;

CREATE TABLE receipts_p_0_1000000 PARTITION OF receipts FOR VALUES FROM ('0') TO ('1000000');

CREATE TABLE logs (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    log_index bigint NOT NULL,
    tx_index bigint NOT NULL,
    tx_hash bytea NOT NULL,
    address bytea NOT NULL,
    topic0 bytea,
    raw jsonb NOT NULL,
    CONSTRAINT logs_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT logs_log_index_check CHECK ((log_index >= 0)),
    CONSTRAINT logs_topic0_check CHECK (((topic0 IS NULL) OR (octet_length(topic0) = 32))),
    CONSTRAINT logs_tx_index_check CHECK ((tx_index >= 0)),
    CONSTRAINT logs_pkey PRIMARY KEY (chain_id, block_number, block_hash, log_index),
    CONSTRAINT logs_chain_id_block_number_block_hash_tx_index_fkey FOREIGN KEY (chain_id, block_number, block_hash, tx_index) REFERENCES receipts(chain_id, block_number, block_hash, tx_index)
)
PARTITION BY RANGE (block_number);

CREATE TABLE logs_default PARTITION OF logs DEFAULT;

CREATE TABLE logs_p_0_1000000 PARTITION OF logs FOR VALUES FROM ('0') TO ('1000000');

CREATE TABLE eip7702_authorizations (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    transaction_hash bytea NOT NULL,
    transaction_index bigint NOT NULL,
    authorization_index bigint NOT NULL,
    authorization_chain_id numeric(78,0) NOT NULL,
    authorization_nonce numeric(78,0) NOT NULL,
    delegate_address bytea NOT NULL,
    y_parity smallint NOT NULL,
    r bytea NOT NULL,
    s bytea NOT NULL,
    authority bytea,
    signature_status text NOT NULL,
    application_status text NOT NULL,
    skip_reason text,
    canonical boolean NOT NULL,
    CONSTRAINT eip7702_authorizations_application_status_check CHECK ((application_status = ANY (ARRAY['applied'::text, 'skipped'::text, 'unavailable'::text]))),
    CONSTRAINT eip7702_authorizations_authority_check CHECK (((authority IS NULL) OR (octet_length(authority) = 20))),
    CONSTRAINT eip7702_authorizations_authorization_chain_id_check CHECK ((authorization_chain_id >= (0)::numeric)),
    CONSTRAINT eip7702_authorizations_authorization_index_check CHECK ((authorization_index >= 0)),
    CONSTRAINT eip7702_authorizations_authorization_nonce_check CHECK ((authorization_nonce >= (0)::numeric)),
    CONSTRAINT eip7702_authorizations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT eip7702_authorizations_block_number_check CHECK ((block_number >= (0)::numeric)),
    CONSTRAINT eip7702_authorizations_check CHECK ((((application_status = 'applied'::text) AND (skip_reason IS NULL) AND (signature_status = 'valid'::text) AND (authority IS NOT NULL)) OR ((application_status <> 'applied'::text) AND (skip_reason IS NOT NULL)))),
    CONSTRAINT eip7702_authorizations_delegate_address_check CHECK ((octet_length(delegate_address) = 20)),
    CONSTRAINT eip7702_authorizations_r_check CHECK ((octet_length(r) = 32)),
    CONSTRAINT eip7702_authorizations_s_check CHECK ((octet_length(s) = 32)),
    CONSTRAINT eip7702_authorizations_signature_status_check CHECK ((signature_status = ANY (ARRAY['valid'::text, 'invalid'::text, 'unavailable'::text]))),
    CONSTRAINT eip7702_authorizations_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT eip7702_authorizations_transaction_index_check CHECK ((transaction_index >= 0)),
    CONSTRAINT eip7702_authorizations_y_parity_check CHECK ((y_parity = ANY (ARRAY[0, 1]))),
    CONSTRAINT eip7702_authorizations_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, authorization_index),
    CONSTRAINT eip7702_authorizations_chain_id_block_number_block_hash_tr_fkey FOREIGN KEY (chain_id, block_number, block_hash, transaction_index) REFERENCES transaction_inclusions(chain_id, block_number, block_hash, tx_index)
)
PARTITION BY RANGE (block_number);

CREATE TABLE eip7702_authorizations_default PARTITION OF eip7702_authorizations DEFAULT;

CREATE TABLE eip7702_authorizations_p_0_1000000 PARTITION OF eip7702_authorizations FOR VALUES FROM ('0') TO ('1000000');

CREATE TABLE trace_log_attributions (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    transaction_hash bytea NOT NULL,
    log_index bigint NOT NULL,
    trace_path text NOT NULL,
    call_type text NOT NULL,
    execution_address bytea NOT NULL,
    canonical boolean NOT NULL,
    CONSTRAINT trace_log_attributions_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT trace_log_attributions_call_type_check CHECK ((call_type = ANY (ARRAY['CALL'::text, 'CALLCODE'::text, 'DELEGATECALL'::text, 'STATICCALL'::text, 'CREATE'::text, 'CREATE2'::text]))),
    CONSTRAINT trace_log_attributions_execution_address_check CHECK ((octet_length(execution_address) = 20)),
    CONSTRAINT trace_log_attributions_log_index_check CHECK ((log_index >= 0)),
    CONSTRAINT trace_log_attributions_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT trace_log_attributions_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, log_index),
    CONSTRAINT trace_log_attributions_chain_id_block_number_block_hash_lo_fkey FOREIGN KEY (chain_id, block_number, block_hash, log_index) REFERENCES logs(chain_id, block_number, block_hash, log_index),
    CONSTRAINT trace_log_attributions_chain_id_block_number_block_hash_tr_fkey FOREIGN KEY (chain_id, block_number, block_hash, transaction_hash, trace_path) REFERENCES normalized_traces(chain_id, block_number, block_hash, transaction_hash, trace_path)
)
PARTITION BY RANGE (block_number);

CREATE TABLE trace_log_attributions_default PARTITION OF trace_log_attributions DEFAULT;

CREATE TABLE trace_log_attributions_p_0_1000000 PARTITION OF trace_log_attributions FOR VALUES FROM ('0') TO ('1000000');

CREATE TABLE transaction_effective_execution_identities (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) CONSTRAINT transaction_effective_execution_identitie_block_number_not_null NOT NULL,
    block_hash bytea NOT NULL,
    transaction_hash bytea CONSTRAINT transaction_effective_execution_ident_transaction_hash_not_null NOT NULL,
    transaction_index bigint CONSTRAINT transaction_effective_execution_iden_transaction_index_not_null NOT NULL,
    context_address bytea CONSTRAINT transaction_effective_execution_identi_context_address_not_null NOT NULL,
    execution_address bytea,
    execution_code_hash bytea,
    resolution text NOT NULL,
    evidence_source text CONSTRAINT transaction_effective_execution_identi_evidence_source_not_null NOT NULL,
    root_trace_path text,
    canonical boolean NOT NULL,
    CONSTRAINT transaction_effective_execution_ident_execution_code_hash_check CHECK (((execution_code_hash IS NULL) OR (octet_length(execution_code_hash) = 32))),
    CONSTRAINT transaction_effective_execution_identit_execution_address_check CHECK (((execution_address IS NULL) OR (octet_length(execution_address) = 20))),
    CONSTRAINT transaction_effective_execution_identit_transaction_index_check CHECK ((transaction_index >= 0)),
    CONSTRAINT transaction_effective_execution_identiti_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT transaction_effective_execution_identitie_context_address_check CHECK ((octet_length(context_address) = 20)),
    CONSTRAINT transaction_effective_execution_identitie_evidence_source_check CHECK ((evidence_source = ANY (ARRAY['prestate_tracer'::text, 'root_trace_code_observation'::text, 'unavailable'::text]))),
    CONSTRAINT transaction_effective_execution_identities_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT transaction_effective_execution_identities_block_number_check CHECK ((block_number >= (0)::numeric)),
    CONSTRAINT transaction_effective_execution_identities_check CHECK ((((evidence_source = 'root_trace_code_observation'::text) AND (root_trace_path = ''::text)) OR ((evidence_source <> 'root_trace_code_observation'::text) AND (root_trace_path IS NULL)))),
    CONSTRAINT transaction_effective_execution_identities_check1 CHECK ((((resolution = 'direct'::text) AND (execution_address = context_address) AND (execution_code_hash IS NOT NULL) AND (evidence_source = 'prestate_tracer'::text)) OR ((resolution = 'eip7702_delegate'::text) AND (execution_address IS NOT NULL) AND (execution_code_hash IS NOT NULL) AND (evidence_source = ANY (ARRAY['prestate_tracer'::text, 'root_trace_code_observation'::text]))) OR ((resolution = 'empty'::text) AND (execution_address IS NULL) AND (execution_code_hash IS NULL) AND (evidence_source = ANY (ARRAY['prestate_tracer'::text, 'root_trace_code_observation'::text]))) OR ((resolution = 'unavailable'::text) AND (execution_code_hash IS NULL) AND (evidence_source = 'unavailable'::text)))),
    CONSTRAINT transaction_effective_execution_identities_resolution_check CHECK ((resolution = ANY (ARRAY['direct'::text, 'eip7702_delegate'::text, 'empty'::text, 'unavailable'::text]))),
    CONSTRAINT transaction_effective_execution_identities_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, context_address),
    CONSTRAINT transaction_effective_executi_chain_id_block_number_block__fkey FOREIGN KEY (chain_id, block_number, block_hash, transaction_index, transaction_hash) REFERENCES transaction_inclusions(chain_id, block_number, block_hash, tx_index, tx_hash)
)
PARTITION BY RANGE (block_number);

CREATE TABLE transaction_effective_execution_identities_default PARTITION OF transaction_effective_execution_identities DEFAULT;

CREATE TABLE transaction_effective_execution_identities_p_0_1000000 PARTITION OF transaction_effective_execution_identities FOR VALUES FROM ('0') TO ('1000000');

CREATE TABLE transaction_execution_code_resolutions (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    transaction_hash bytea CONSTRAINT transaction_execution_code_resolution_transaction_hash_not_null NOT NULL,
    transaction_index bigint CONSTRAINT transaction_execution_code_resolutio_transaction_index_not_null NOT NULL,
    context_address bytea NOT NULL,
    execution_address bytea,
    execution_code_hash bytea,
    resolution text NOT NULL,
    evidence_source text NOT NULL,
    canonical boolean NOT NULL,
    CONSTRAINT transaction_execution_code_resolution_execution_code_hash_check CHECK (((execution_code_hash IS NULL) OR (octet_length(execution_code_hash) = 32))),
    CONSTRAINT transaction_execution_code_resolutions_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT transaction_execution_code_resolutions_block_number_check CHECK ((block_number >= (0)::numeric)),
    CONSTRAINT transaction_execution_code_resolutions_check CHECK ((((resolution = 'direct'::text) AND (execution_address = context_address) AND (execution_code_hash IS NOT NULL) AND (evidence_source = 'prestate_tracer'::text)) OR ((resolution = 'eip7702_delegate'::text) AND (execution_address IS NOT NULL) AND (execution_code_hash IS NOT NULL) AND (evidence_source = 'prestate_tracer'::text)) OR ((resolution = 'empty'::text) AND (execution_address IS NULL) AND (execution_code_hash IS NULL) AND (evidence_source = 'prestate_tracer'::text)) OR ((resolution = 'unavailable'::text) AND (execution_code_hash IS NULL) AND (evidence_source = 'unavailable'::text)))),
    CONSTRAINT transaction_execution_code_resolutions_context_address_check CHECK ((octet_length(context_address) = 20)),
    CONSTRAINT transaction_execution_code_resolutions_evidence_source_check CHECK ((evidence_source = ANY (ARRAY['prestate_tracer'::text, 'unavailable'::text]))),
    CONSTRAINT transaction_execution_code_resolutions_execution_address_check CHECK (((execution_address IS NULL) OR (octet_length(execution_address) = 20))),
    CONSTRAINT transaction_execution_code_resolutions_resolution_check CHECK ((resolution = ANY (ARRAY['direct'::text, 'eip7702_delegate'::text, 'empty'::text, 'unavailable'::text]))),
    CONSTRAINT transaction_execution_code_resolutions_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT transaction_execution_code_resolutions_transaction_index_check CHECK ((transaction_index >= 0)),
    CONSTRAINT transaction_execution_code_resolutions_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, context_address),
    CONSTRAINT transaction_execution_code_re_chain_id_block_number_block__fkey FOREIGN KEY (chain_id, block_number, block_hash, transaction_index) REFERENCES transaction_inclusions(chain_id, block_number, block_hash, tx_index)
)
PARTITION BY RANGE (block_number);

CREATE TABLE transaction_execution_code_resolutions_default PARTITION OF transaction_execution_code_resolutions DEFAULT;

CREATE TABLE transaction_execution_code_resolutions_p_0_1000000 PARTITION OF transaction_execution_code_resolutions FOR VALUES FROM ('0') TO ('1000000');

CREATE TABLE transaction_state_changes (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    transaction_hash bytea NOT NULL,
    transaction_index bigint NOT NULL,
    address bytea NOT NULL,
    field_kind text NOT NULL,
    storage_key bytea DEFAULT '\x'::bytea NOT NULL,
    before_value text,
    after_value text,
    canonical boolean NOT NULL,
    CONSTRAINT transaction_state_changes_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT transaction_state_changes_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT transaction_state_changes_check CHECK ((((field_kind = 'storage'::text) AND (octet_length(storage_key) = 32)) OR ((field_kind <> 'storage'::text) AND (octet_length(storage_key) = 0)))),
    CONSTRAINT transaction_state_changes_check1 CHECK (((before_value IS NOT NULL) OR (after_value IS NOT NULL))),
    CONSTRAINT transaction_state_changes_field_kind_check CHECK ((field_kind = ANY (ARRAY['balance'::text, 'nonce'::text, 'code'::text, 'storage'::text]))),
    CONSTRAINT transaction_state_changes_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT transaction_state_changes_pkey PRIMARY KEY (chain_id, block_number, block_hash, transaction_hash, address, field_kind, storage_key),
    CONSTRAINT transaction_state_changes_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash, transaction_index) REFERENCES transaction_inclusions(chain_id, block_number, block_hash, tx_index)
)
PARTITION BY RANGE (block_number);

CREATE TABLE transaction_state_changes_default PARTITION OF transaction_state_changes DEFAULT;

CREATE TABLE transaction_state_changes_p_0_1000000 PARTITION OF transaction_state_changes FOR VALUES FROM ('0') TO ('1000000');

CREATE TABLE withdrawals (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    withdrawal_index numeric(78,0) NOT NULL,
    validator_index numeric(78,0) NOT NULL,
    address bytea NOT NULL,
    amount numeric(78,0) NOT NULL,
    raw jsonb NOT NULL,
    CONSTRAINT withdrawals_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT withdrawals_pkey PRIMARY KEY (chain_id, block_number, block_hash, withdrawal_index),
    CONSTRAINT withdrawals_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash)
)
PARTITION BY RANGE (block_number);

CREATE TABLE withdrawals_default PARTITION OF withdrawals DEFAULT;

CREATE TABLE withdrawals_p_0_1000000 PARTITION OF withdrawals FOR VALUES FROM ('0') TO ('1000000');


-- Indexes

CREATE INDEX abi_decodings_default_chain_id_target_address_target_code_h_idx ON abi_decodings_default USING btree (chain_id, target_address, target_code_hash, block_number DESC) WHERE canonical;

CREATE INDEX abi_decodings_default_chain_id_transaction_hash_block_numbe_idx ON abi_decodings_default USING btree (chain_id, transaction_hash, block_number DESC);

CREATE INDEX abi_decodings_p_0_1000000_chain_id_target_address_target_co_idx ON abi_decodings_p_0_1000000 USING btree (chain_id, target_address, target_code_hash, block_number DESC) WHERE canonical;

CREATE INDEX abi_decodings_p_0_1000000_chain_id_transaction_hash_block_n_idx ON abi_decodings_p_0_1000000 USING btree (chain_id, transaction_hash, block_number DESC);

CREATE INDEX address_activities_default_chain_id_address_block_number_ac_idx ON address_activities_default USING btree (chain_id, address, block_number DESC, activity_index DESC) WHERE canonical;

CREATE INDEX address_activities_p_0_100000_chain_id_address_block_number_idx ON address_activities_p_0_1000000 USING btree (chain_id, address, block_number DESC, activity_index DESC) WHERE canonical;

CREATE INDEX eip7702_authorizations_defaul_chain_id_authority_block_numb_idx ON eip7702_authorizations_default USING btree (chain_id, authority, block_number DESC, transaction_index DESC, authorization_index DESC) WHERE (canonical AND (authority IS NOT NULL));

CREATE INDEX eip7702_authorizations_defaul_chain_id_transaction_hash_aut_idx ON eip7702_authorizations_default USING btree (chain_id, transaction_hash, authorization_index);

CREATE INDEX eip7702_authorizations_p_0_10_chain_id_authority_block_numb_idx ON eip7702_authorizations_p_0_1000000 USING btree (chain_id, authority, block_number DESC, transaction_index DESC, authorization_index DESC) WHERE (canonical AND (authority IS NOT NULL));

CREATE INDEX eip7702_authorizations_p_0_10_chain_id_transaction_hash_aut_idx ON eip7702_authorizations_p_0_1000000 USING btree (chain_id, transaction_hash, authorization_index);

CREATE INDEX logs_default_chain_id_address_block_number_idx ON logs_default USING btree (chain_id, address, block_number DESC);

CREATE INDEX logs_default_chain_id_topic0_block_number_idx ON logs_default USING btree (chain_id, topic0, block_number DESC);

CREATE INDEX logs_p_0_1000000_chain_id_address_block_number_idx ON logs_p_0_1000000 USING btree (chain_id, address, block_number DESC);

CREATE INDEX logs_p_0_1000000_chain_id_topic0_block_number_idx ON logs_p_0_1000000 USING btree (chain_id, topic0, block_number DESC);

CREATE INDEX normalized_traces_default_chain_id_created_address_block_nu_idx ON normalized_traces_default USING btree (chain_id, created_address, block_number DESC) WHERE (canonical AND (created_address IS NOT NULL));

CREATE INDEX normalized_traces_default_chain_id_from_address_block_numbe_idx ON normalized_traces_default USING btree (chain_id, from_address, block_number DESC) WHERE canonical;

CREATE INDEX normalized_traces_default_chain_id_to_address_block_number_idx ON normalized_traces_default USING btree (chain_id, to_address, block_number DESC) WHERE canonical;

CREATE INDEX normalized_traces_default_chain_id_transaction_hash_trace_p_idx ON normalized_traces_default USING btree (chain_id, transaction_hash, trace_path);

CREATE INDEX normalized_traces_p_0_1000000_chain_id_created_address_bloc_idx ON normalized_traces_p_0_1000000 USING btree (chain_id, created_address, block_number DESC) WHERE (canonical AND (created_address IS NOT NULL));

CREATE INDEX normalized_traces_p_0_1000000_chain_id_from_address_block_n_idx ON normalized_traces_p_0_1000000 USING btree (chain_id, from_address, block_number DESC) WHERE canonical;

CREATE INDEX normalized_traces_p_0_1000000_chain_id_to_address_block_num_idx ON normalized_traces_p_0_1000000 USING btree (chain_id, to_address, block_number DESC) WHERE canonical;

CREATE INDEX normalized_traces_p_0_1000000_chain_id_transaction_hash_tra_idx ON normalized_traces_p_0_1000000 USING btree (chain_id, transaction_hash, trace_path);

CREATE INDEX receipts_default_chain_id_lower_block_number_tx_index_idx ON receipts_default USING btree (chain_id, lower((raw ->> 'contractAddress'::text)), block_number DESC, tx_index DESC) WHERE ((raw ->> 'contractAddress'::text) IS NOT NULL);

CREATE INDEX receipts_p_0_1000000_chain_id_lower_block_number_tx_index_idx ON receipts_p_0_1000000 USING btree (chain_id, lower((raw ->> 'contractAddress'::text)), block_number DESC, tx_index DESC) WHERE ((raw ->> 'contractAddress'::text) IS NOT NULL);

CREATE INDEX trace_log_attributions_defaul_chain_id_transaction_hash_tra_idx ON trace_log_attributions_default USING btree (chain_id, transaction_hash, trace_path, log_index) WHERE canonical;

CREATE INDEX trace_log_attributions_p_0_10_chain_id_transaction_hash_tra_idx ON trace_log_attributions_p_0_1000000 USING btree (chain_id, transaction_hash, trace_path, log_index) WHERE canonical;

CREATE INDEX transaction_effective_executi_chain_id_execution_address_e_idx1 ON transaction_effective_execution_identities_default USING btree (chain_id, execution_address, execution_code_hash, block_number DESC, transaction_index DESC) WHERE (canonical AND (execution_address IS NOT NULL));

CREATE INDEX transaction_effective_executi_chain_id_execution_address_ex_idx ON transaction_effective_execution_identities_p_0_1000000 USING btree (chain_id, execution_address, execution_code_hash, block_number DESC, transaction_index DESC) WHERE (canonical AND (execution_address IS NOT NULL));

CREATE INDEX transaction_effective_executi_chain_id_transaction_hash_co_idx1 ON transaction_effective_execution_identities_default USING btree (chain_id, transaction_hash, context_address) WHERE canonical;

CREATE INDEX transaction_effective_executi_chain_id_transaction_hash_con_idx ON transaction_effective_execution_identities_p_0_1000000 USING btree (chain_id, transaction_hash, context_address) WHERE canonical;

CREATE INDEX transaction_execution_code_re_chain_id_execution_address_b_idx1 ON transaction_execution_code_resolutions_default USING btree (chain_id, execution_address, block_number DESC) WHERE (canonical AND (execution_address IS NOT NULL));

CREATE INDEX transaction_execution_code_re_chain_id_execution_address_bl_idx ON transaction_execution_code_resolutions_p_0_1000000 USING btree (chain_id, execution_address, block_number DESC) WHERE (canonical AND (execution_address IS NOT NULL));

CREATE INDEX transaction_execution_code_re_chain_id_transaction_hash_co_idx1 ON transaction_execution_code_resolutions_default USING btree (chain_id, transaction_hash, context_address) WHERE canonical;

CREATE INDEX transaction_execution_code_re_chain_id_transaction_hash_con_idx ON transaction_execution_code_resolutions_p_0_1000000 USING btree (chain_id, transaction_hash, context_address) WHERE canonical;

CREATE INDEX transaction_inclusions_defaul_chain_id_lower_block_number__idx1 ON transaction_inclusions_default USING btree (chain_id, lower((raw ->> 'to'::text)), block_number DESC, tx_index DESC);

CREATE INDEX transaction_inclusions_defaul_chain_id_lower_block_number_t_idx ON transaction_inclusions_default USING btree (chain_id, lower((raw ->> 'from'::text)), block_number DESC, tx_index DESC);

CREATE INDEX transaction_inclusions_p_0_10_chain_id_lower_block_number__idx1 ON transaction_inclusions_p_0_1000000 USING btree (chain_id, lower((raw ->> 'to'::text)), block_number DESC, tx_index DESC);

CREATE INDEX transaction_inclusions_p_0_10_chain_id_lower_block_number_t_idx ON transaction_inclusions_p_0_1000000 USING btree (chain_id, lower((raw ->> 'from'::text)), block_number DESC, tx_index DESC);

CREATE INDEX transaction_state_changes_def_chain_id_address_block_numbe_idx1 ON transaction_state_changes_default USING btree (chain_id, address, block_number DESC) INCLUDE (block_hash) WHERE (canonical AND (field_kind = 'code'::text));

CREATE INDEX transaction_state_changes_def_chain_id_address_block_number_idx ON transaction_state_changes_default USING btree (chain_id, address, block_number DESC) WHERE canonical;

CREATE INDEX transaction_state_changes_def_chain_id_transaction_hash_blo_idx ON transaction_state_changes_default USING btree (chain_id, transaction_hash, block_number DESC, address, field_kind, storage_key);

CREATE INDEX transaction_state_changes_p_0_chain_id_address_block_numbe_idx1 ON transaction_state_changes_p_0_1000000 USING btree (chain_id, address, block_number DESC) INCLUDE (block_hash) WHERE (canonical AND (field_kind = 'code'::text));

CREATE INDEX transaction_state_changes_p_0_chain_id_address_block_number_idx ON transaction_state_changes_p_0_1000000 USING btree (chain_id, address, block_number DESC) WHERE canonical;

CREATE INDEX transaction_state_changes_p_0_chain_id_transaction_hash_blo_idx ON transaction_state_changes_p_0_1000000 USING btree (chain_id, transaction_hash, block_number DESC, address, field_kind, storage_key);

CREATE INDEX withdrawals_default_chain_id_address_withdrawal_index_idx ON withdrawals_default USING btree (chain_id, address, withdrawal_index DESC);

CREATE INDEX withdrawals_p_0_1000000_chain_id_address_withdrawal_index_idx ON withdrawals_p_0_1000000 USING btree (chain_id, address, withdrawal_index DESC);

CREATE INDEX abi_decodings_target_idx ON abi_decodings USING btree (chain_id, target_address, target_code_hash, block_number DESC) WHERE canonical;

CREATE INDEX abi_decodings_tx_idx ON abi_decodings USING btree (chain_id, transaction_hash, block_number DESC);

CREATE INDEX address_activities_address_idx ON address_activities USING btree (chain_id, address, block_number DESC, activity_index DESC) WHERE canonical;

CREATE INDEX contract_abis_context_idx ON contract_abis USING btree (chain_id, address, code_hash, block_number DESC, source, canonical);

CREATE INDEX contract_code_history_idx ON contract_code_observations USING btree (chain_id, address, block_number DESC) WHERE canonical;

CREATE INDEX eip7702_authorizations_authority_idx ON eip7702_authorizations USING btree (chain_id, authority, block_number DESC, transaction_index DESC, authorization_index DESC) WHERE (canonical AND (authority IS NOT NULL));

CREATE INDEX eip7702_authorizations_tx_idx ON eip7702_authorizations USING btree (chain_id, transaction_hash, authorization_index);

CREATE INDEX logs_address_idx ON logs USING btree (chain_id, address, block_number DESC);

CREATE INDEX logs_topic0_idx ON logs USING btree (chain_id, topic0, block_number DESC);

CREATE INDEX normalized_traces_created_idx ON normalized_traces USING btree (chain_id, created_address, block_number DESC) WHERE (canonical AND (created_address IS NOT NULL));

CREATE INDEX normalized_traces_from_idx ON normalized_traces USING btree (chain_id, from_address, block_number DESC) WHERE canonical;

CREATE INDEX normalized_traces_to_idx ON normalized_traces USING btree (chain_id, to_address, block_number DESC) WHERE canonical;

CREATE INDEX normalized_traces_tx_idx ON normalized_traces USING btree (chain_id, transaction_hash, trace_path);

CREATE INDEX receipts_contract_address_idx ON receipts USING btree (chain_id, lower((raw ->> 'contractAddress'::text)), block_number DESC, tx_index DESC) WHERE ((raw ->> 'contractAddress'::text) IS NOT NULL);

CREATE INDEX trace_log_attributions_frame_idx ON trace_log_attributions USING btree (chain_id, transaction_hash, trace_path, log_index) WHERE canonical;

CREATE INDEX transaction_effective_execution_identities_code_idx ON transaction_effective_execution_identities USING btree (chain_id, execution_address, execution_code_hash, block_number DESC, transaction_index DESC) WHERE (canonical AND (execution_address IS NOT NULL));

CREATE INDEX transaction_effective_execution_identities_tx_idx ON transaction_effective_execution_identities USING btree (chain_id, transaction_hash, context_address) WHERE canonical;

CREATE INDEX transaction_execution_code_resolutions_execution_idx ON transaction_execution_code_resolutions USING btree (chain_id, execution_address, block_number DESC) WHERE (canonical AND (execution_address IS NOT NULL));

CREATE INDEX transaction_execution_code_resolutions_tx_idx ON transaction_execution_code_resolutions USING btree (chain_id, transaction_hash, context_address) WHERE canonical;

CREATE INDEX transaction_inclusions_to_address_idx ON transaction_inclusions USING btree (chain_id, lower((raw ->> 'to'::text)), block_number DESC, tx_index DESC);

CREATE INDEX transaction_inclusions_from_address_idx ON transaction_inclusions USING btree (chain_id, lower((raw ->> 'from'::text)), block_number DESC, tx_index DESC);

CREATE INDEX transaction_state_changes_address_idx ON transaction_state_changes USING btree (chain_id, address, block_number DESC) WHERE canonical;

CREATE INDEX transaction_state_changes_code_epoch_idx ON transaction_state_changes USING btree (chain_id, address, block_number DESC) INCLUDE (block_hash) WHERE (canonical AND (field_kind = 'code'::text));

CREATE INDEX transaction_state_changes_tx_idx ON transaction_state_changes USING btree (chain_id, transaction_hash, block_number DESC, address, field_kind, storage_key);

CREATE INDEX withdrawals_address_index_idx ON withdrawals USING btree (chain_id, address, withdrawal_index DESC);

-- Optional Beacon observations and immutable blob manifests (ADR-0055).
CREATE TABLE consensus_networks (
    chain_id numeric(78,0) PRIMARY KEY REFERENCES chains(chain_id),
    identity jsonb NOT NULL CHECK (jsonb_typeof(identity) = 'object' AND octet_length(identity::text) <= 4096)
);
CREATE TABLE consensus_observations (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL CHECK (octet_length(block_hash) = 32),
    slot numeric(20,0) NOT NULL CHECK (slot >= 0),
    observation jsonb NOT NULL CHECK (jsonb_typeof(observation) = 'object' AND octet_length(observation::text) <= 1048576),
    canonical boolean NOT NULL,
    durable_job_id bigint NOT NULL REFERENCES durable_jobs(id),
    job_generation bigint NOT NULL CHECK (job_generation > 0),
    PRIMARY KEY (chain_id, block_number, block_hash),
    FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash)
);
CREATE TABLE blob_manifests (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL CHECK (octet_length(block_hash) = 32),
    manifest jsonb NOT NULL CHECK (jsonb_typeof(manifest) = 'array' AND jsonb_array_length(manifest) <= 4096 AND octet_length(manifest::text) <= 4194304),
    canonical boolean NOT NULL,
    durable_job_id bigint NOT NULL REFERENCES durable_jobs(id),
    job_generation bigint NOT NULL CHECK (job_generation > 0),
    PRIMARY KEY (chain_id, block_number, block_hash),
    FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash)
);
CREATE TABLE execution_request_sets (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL CHECK (octet_length(block_hash) = 32),
    manifest jsonb NOT NULL CHECK (jsonb_typeof(manifest) = 'object' AND octet_length(manifest::text) <= 8192),
    canonical boolean NOT NULL,
    durable_job_id bigint NOT NULL REFERENCES durable_jobs(id),
    job_generation bigint NOT NULL CHECK (job_generation > 0),
    PRIMARY KEY (chain_id, block_number, block_hash),
    FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash)
);
CREATE INDEX execution_request_sets_hash_idx ON execution_request_sets(chain_id,block_hash);
CREATE TABLE execution_requests (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL CHECK (octet_length(block_hash) = 32),
    request_type smallint NOT NULL CHECK (request_type BETWEEN 0 AND 4),
    sequence_index integer NOT NULL CHECK (sequence_index BETWEEN 0 AND 8191),
    record jsonb NOT NULL CHECK (jsonb_typeof(record) = 'object' AND octet_length(record::text) <= 2048),
    PRIMARY KEY (chain_id, block_number, block_hash, request_type, sequence_index),
    FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES execution_request_sets(chain_id, block_number, block_hash)
);
CREATE TABLE consensus_overviews (
    chain_id numeric(78,0) PRIMARY KEY REFERENCES chains(chain_id),
    generation bigint NOT NULL DEFAULT 0 CHECK (generation >= 0),
    lease_until timestamptz NOT NULL DEFAULT '-infinity',
    next_poll_at timestamptz NOT NULL DEFAULT '-infinity',
    observed_at timestamptz,
    overview jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(overview) = 'object' AND octet_length(overview::text) <= 8192),
    last_error text NOT NULL DEFAULT '' CHECK (last_error ~ '^[a-z0-9_]*$' AND length(last_error) <= 64)
);

CREATE TABLE consensus_discovery_cursors (
 chain_id numeric(78,0) NOT NULL REFERENCES chains(chain_id),
 stage text NOT NULL CHECK (stage IN ('consensus','blob','requests')),
 generation bigint NOT NULL DEFAULT 0 CHECK (generation >= 0),
 next_number numeric(20,0) NOT NULL CHECK (next_number >= -1),
 latest_number numeric(20,0) NOT NULL CHECK (latest_number >= -1),
 PRIMARY KEY(chain_id,stage)
);

CREATE INDEX consensus_observations_hash_idx ON consensus_observations(chain_id,block_hash);
CREATE INDEX blob_manifests_hash_idx ON blob_manifests(chain_id,block_hash);
CREATE INDEX block_stage_results_consensus_health_idx ON block_stage_results(chain_id,stage,stage_version,block_number DESC)
WHERE stage IN ('consensus','blob','requests');
