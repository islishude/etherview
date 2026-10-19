-- Fresh-database baseline: schema bindings.
-- Cross-domain views and schema bindings follow all domain objects.

-- Views

CREATE VIEW published_block_stage_results AS
 SELECT result.chain_id,
    result.block_number,
    result.block_hash,
    result.stage,
    result.stage_version,
    result.state,
    result.details,
    result.last_error,
    result.completed_at,
    result.durable_job_id,
    result.job_generation
   FROM (block_stage_results result
     JOIN durable_jobs job ON (((job.id = result.durable_job_id) AND (job.chain_id = result.chain_id) AND (job.kind = 'enrichment'::text) AND (job.stage = result.stage) AND (job.stage_version = result.stage_version) AND ((job.payload ->> 'block_hash'::text) = ('0x'::text || encode(result.block_hash, 'hex'::text))) AND ((job.payload ->> 'block_number'::text) = (result.block_number)::text))))
  WHERE ((result.durable_job_id IS NOT NULL) AND (result.job_generation IS NOT NULL) AND (job.status = ANY (ARRAY['succeeded'::text, 'failed'::text])) AND (job.claimed_generation = result.job_generation) AND (job.completed_generation = result.job_generation) AND (job.requested_generation = result.job_generation) AND (job.leased_generation IS NULL) AND (job.leased_by IS NULL) AND (job.lease_token IS NULL) AND (job.lease_expires_at IS NULL) AND (job.result IS NOT NULL) AND ((job.result ->> 'state'::text) = result.state) AND (COALESCE((result.details ->> 'outcome'::text), ''::text) <> 'stale_canonical_skipped'::text) AND (NOT (EXISTS ( SELECT 1
           FROM transactional_outbox pending_attach
          WHERE ((pending_attach.chain_id = result.chain_id) AND (pending_attach.topic = 'core.block.canonical'::text) AND (pending_attach.message_key = ('0x'::text || encode(result.block_hash, 'hex'::text))) AND (pending_attach.published_at IS NULL))))) AND (((job.status = 'succeeded'::text) AND (result.state = 'complete'::text) AND (EXISTS ( SELECT 1
           FROM block_journals journal
          WHERE ((journal.chain_id = result.chain_id) AND (journal.block_hash = result.block_hash) AND (journal.stage = ((result.stage || '@'::text) || (result.stage_version)::text)) AND (journal.sequence = (1)::numeric) AND (journal.durable_job_id = result.durable_job_id) AND (journal.job_generation = result.job_generation))))) OR ((job.status = 'failed'::text) AND (result.state = ANY (ARRAY['failed'::text, 'unavailable'::text])))) AND (COALESCE((job.result -> 'details'::text), '{}'::jsonb) = result.details) AND (NOT ((job.result ->> 'error'::text) IS DISTINCT FROM result.last_error)));

CREATE VIEW canonical_diamond_selector_intervals AS
 WITH ordered AS (
         SELECT event.chain_id,
            event.diamond_address,
            change.selector,
            change.facet_address,
            change.action,
            event.block_number AS valid_from_block_number,
            event.block_hash AS valid_from_block_hash,
            event.transaction_index AS valid_from_transaction_index,
            event.log_index AS valid_from_log_index,
            change.cut_index AS valid_from_cut_index,
            change.selector_index AS valid_from_selector_index,
            lead(event.block_number) OVER route AS valid_to_block_number,
            lead(event.block_hash) OVER route AS valid_to_block_hash,
            lead(event.transaction_index) OVER route AS valid_to_transaction_index,
            lead(event.log_index) OVER route AS valid_to_log_index,
            lead(change.cut_index) OVER route AS valid_to_cut_index,
            lead(change.selector_index) OVER route AS valid_to_selector_index
           FROM (((diamond_cut_events event
             JOIN diamond_selector_changes change ON (((change.chain_id = event.chain_id) AND (change.block_hash = event.block_hash) AND (change.log_index = event.log_index) AND (change.stage_version = event.stage_version))))
             JOIN canonical_blocks canonical ON (((canonical.chain_id = event.chain_id) AND (canonical.number = event.block_number) AND (canonical.block_hash = event.block_hash))))
             JOIN published_block_stage_results published ON (((published.chain_id = event.chain_id) AND (published.block_number = event.block_number) AND (published.block_hash = event.block_hash) AND (published.stage = 'proxy'::text) AND (published.stage_version = event.stage_version) AND (published.state = 'complete'::text))))
          WHERE (event.canonical AND (event.stage_version = 2))
          WINDOW route AS (PARTITION BY event.chain_id, event.diamond_address, change.selector ORDER BY event.block_number, event.transaction_index, event.log_index, change.cut_index, change.selector_index)
        )
 SELECT chain_id,
    diamond_address,
    selector,
    facet_address,
    valid_from_block_number,
    valid_from_block_hash,
    valid_from_transaction_index,
    valid_from_log_index,
    valid_from_cut_index,
    valid_from_selector_index,
    valid_to_block_number,
    valid_to_block_hash,
    valid_to_transaction_index,
    valid_to_log_index,
    valid_to_cut_index,
    valid_to_selector_index
   FROM ordered
  WHERE (action <> 2);

CREATE VIEW published_diamond_loupe_snapshots AS
 SELECT snapshot.id,
    snapshot.chain_id,
    snapshot.diamond_address,
    snapshot.block_number,
    snapshot.block_hash,
    snapshot.stage_version,
    snapshot.detection_state,
    snapshot.completeness,
    snapshot.validation,
    snapshot.standard_diamond_cut,
    snapshot.standard_diamond_cut_facet,
    snapshot.loupe_interface_reported,
    snapshot.truncated,
    snapshot.truncation_reason,
    snapshot.warnings,
    snapshot.canonical,
    snapshot.durable_job_id,
    snapshot.job_generation,
    snapshot.observed_at
   FROM (diamond_loupe_snapshots snapshot
     JOIN published_block_stage_results published ON (((published.chain_id = snapshot.chain_id) AND (published.block_number = snapshot.block_number) AND (published.block_hash = snapshot.block_hash) AND (published.stage = 'proxy'::text) AND (published.stage_version = snapshot.stage_version) AND (published.state = 'complete'::text) AND (published.durable_job_id = snapshot.durable_job_id) AND (published.job_generation = snapshot.job_generation))))
  WHERE snapshot.canonical;

CREATE VIEW published_erc4337_user_operations AS
 SELECT operation.chain_id,
    operation.configuration_digest,
    operation.block_number,
    operation.block_hash,
    operation.transaction_hash,
    operation.transaction_index,
    operation.operation_index,
    operation.event_log_index,
    operation.user_op_hash,
    operation.entry_point,
    operation.entry_point_version,
    operation.sender,
    operation.nonce,
    operation.nonce_key,
    operation.nonce_sequence,
    operation.bundler,
    operation.beneficiary,
    operation.init_kind,
    operation.factory,
    operation.paymaster,
    operation.aggregator,
    operation.success,
    operation.actual_gas_cost,
    operation.actual_gas_used,
    operation.call_gas_limit,
    operation.verification_gas_limit,
    operation.pre_verification_gas,
    operation.max_fee_per_gas,
    operation.max_priority_fee_per_gas,
    operation.paymaster_verification_gas_limit,
    operation.paymaster_post_op_gas_limit,
    operation.init_code,
    operation.factory_data,
    operation.call_data,
    operation.paymaster_and_data,
    operation.paymaster_data,
    operation.paymaster_signature,
    operation.signature,
    operation.account_gas_limits,
    operation.gas_fees,
    operation.aggregated_signature,
    operation.canonical,
    operation.created_at,
    block."timestamp" AS block_timestamp,
    finality.safe_number,
    finality.finalized_number,
    covered.durable_job_id,
    covered.job_generation
   FROM (((((erc4337_user_operations operation
     JOIN canonical_blocks canonical ON (((canonical.chain_id = operation.chain_id) AND (canonical.number = operation.block_number) AND (canonical.block_hash = operation.block_hash))))
     JOIN blocks block ON (((block.chain_id = operation.chain_id) AND (block.number = operation.block_number) AND (block.hash = operation.block_hash))))
     JOIN erc4337_covered_blocks covered ON (((covered.chain_id = operation.chain_id) AND (covered.configuration_digest = operation.configuration_digest) AND (covered.block_number = operation.block_number) AND (covered.block_hash = operation.block_hash))))
     JOIN published_block_stage_results published ON (((published.chain_id = covered.chain_id) AND (published.block_number = covered.block_number) AND (published.block_hash = covered.block_hash) AND (published.stage = 'userop'::text) AND (published.stage_version = 1) AND (published.state = 'complete'::text) AND (published.durable_job_id = covered.durable_job_id) AND (published.job_generation = covered.job_generation) AND ((published.details ->> 'configuration_digest'::text) = encode(covered.configuration_digest, 'hex'::text)))))
     LEFT JOIN chain_finality finality ON ((finality.chain_id = operation.chain_id)))
  WHERE operation.canonical;

CREATE VIEW watch_activity_sources AS
 SELECT inclusion.chain_id,
    inclusion.block_number,
    inclusion.block_hash,
    block."timestamp" AS block_timestamp,
    inclusion.tx_index,
    ('tx:'::text || encode(inclusion.tx_hash, 'hex'::text)) AS source_key,
    'transaction'::text AS source_kind,
    (0)::bigint AS source_generation,
    lower((inclusion.raw ->> 'from'::text)) AS from_address,
    lower(COALESCE((inclusion.raw ->> 'to'::text), (receipt.raw ->> 'contractAddress'::text))) AS to_address,
    jsonb_build_object('block_number', (inclusion.block_number)::text, 'block_hash', ('0x'::text || encode(inclusion.block_hash, 'hex'::text)), 'timestamp', (block."timestamp")::text, 'transaction_hash', ('0x'::text || encode(inclusion.tx_hash, 'hex'::text)), 'transaction_index', (inclusion.tx_index)::text, 'kind', 'transaction', 'from', lower((inclusion.raw ->> 'from'::text)), 'to', lower(COALESCE((inclusion.raw ->> 'to'::text), (receipt.raw ->> 'contractAddress'::text))), 'value', (inclusion.raw ->> 'value'::text), 'status', (receipt.raw ->> 'status'::text)) AS activity
   FROM (((transaction_inclusions inclusion
     JOIN blocks block ON (((block.chain_id = inclusion.chain_id) AND (block.number = inclusion.block_number) AND (block.hash = inclusion.block_hash))))
     JOIN canonical_blocks canonical ON (((canonical.chain_id = inclusion.chain_id) AND (canonical.number = inclusion.block_number) AND (canonical.block_hash = inclusion.block_hash))))
     JOIN receipts receipt ON (((receipt.chain_id = inclusion.chain_id) AND (receipt.block_number = inclusion.block_number) AND (receipt.block_hash = inclusion.block_hash) AND (receipt.tx_index = inclusion.tx_index))))
UNION ALL
 SELECT event.chain_id,
    event.block_number,
    event.block_hash,
    block."timestamp" AS block_timestamp,
    inclusion.tx_index,
    ((('log:'::text || (event.log_index)::text) || ':'::text) || (event.sub_index)::text) AS source_key,
    event.standard AS source_kind,
    publication.job_generation AS source_generation,
    ('0x'::text || encode(event.from_address, 'hex'::text)) AS from_address,
    ('0x'::text || encode(event.to_address, 'hex'::text)) AS to_address,
    jsonb_build_object('block_number', (event.block_number)::text, 'block_hash', ('0x'::text || encode(event.block_hash, 'hex'::text)), 'timestamp', (block."timestamp")::text, 'transaction_hash', ('0x'::text || encode(event.transaction_hash, 'hex'::text)), 'transaction_index', (inclusion.tx_index)::text, 'kind', event.standard, 'from', ('0x'::text || encode(event.from_address, 'hex'::text)), 'to', ('0x'::text || encode(event.to_address, 'hex'::text)), 'token_address', ('0x'::text || encode(event.token_address, 'hex'::text)), 'event_kind', event.event_kind, 'log_index', (event.log_index)::text, 'sub_index', (event.sub_index)::text, 'token_id', (event.token_id)::text, 'amount', (event.amount)::text, 'decimals', (metadata.decimals)::text) AS activity
   FROM (((((token_events event
     JOIN canonical_blocks canonical ON (((canonical.chain_id = event.chain_id) AND (canonical.number = event.block_number) AND (canonical.block_hash = event.block_hash))))
     JOIN blocks block ON (((block.chain_id = event.chain_id) AND (block.number = event.block_number) AND (block.hash = event.block_hash))))
     JOIN transaction_inclusions inclusion ON (((inclusion.chain_id = event.chain_id) AND (inclusion.block_number = event.block_number) AND (inclusion.block_hash = event.block_hash) AND (inclusion.tx_hash = event.transaction_hash))))
     JOIN published_block_stage_results publication ON (((publication.chain_id = event.chain_id) AND (publication.block_hash = event.block_hash) AND (publication.stage = 'token'::text) AND (publication.stage_version = 2) AND (publication.state = 'complete'::text))))
     LEFT JOIN LATERAL ( SELECT contract.decimals
           FROM token_contracts contract
          WHERE ((contract.chain_id = event.chain_id) AND (contract.address = event.token_address) AND (contract.observed_block_number = event.block_number) AND (contract.observed_block_hash = event.block_hash) AND (contract.standard = 'erc20'::text) AND (contract.metadata_state = 'complete'::text))
          ORDER BY contract.code_hash
         LIMIT 1) metadata ON ((event.standard = 'erc20'::text)))
  WHERE (event.canonical AND (event.event_kind = ANY (ARRAY['transfer'::text, 'mint'::text, 'burn'::text])));


-- Function schema bindings

-- Bind to the actual migration schema, including isolated test schemas.
DO $$
BEGIN
    EXECUTE format('ALTER FUNCTION %I.enforce_enrichment_terminal_publication() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.enforce_erc4337_canonical_user_op_hash() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.enforce_proxy_artifact_resolution() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.enforce_proxy_detection_generation() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.enforce_proxy_observation_generation() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.enforce_unbound_verification_job_insert() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.enforce_uups_implementation_observation() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.enforce_uups_implementation_observation_mutation() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.enforce_uups_implementation_observation_witness() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.enforce_verified_contract_proxy_artifact() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.enforce_verified_proxy_binding() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.enforce_verifier_v2_publication() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.enqueue_derived_forward_event_after_stage_publication() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.erc4337_add_covered_block(target_chain_id numeric, target_configuration_digest bytea, target_block_number numeric, target_block_hash bytea, target_durable_job_id bigint, target_job_generation bigint) SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.erc4337_remove_block_coverage(target_chain_id numeric, target_block_number numeric, target_block_hash bytea) SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.erc4337_remove_covered_block(target_chain_id numeric, target_configuration_digest bytea, target_block_number numeric) SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.erc4337_remove_detached_coverage() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.etherview_guard_erc20_balance_reconciliation() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.etherview_guard_erc20_holder_fact() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.etherview_guard_nft_metadata_update_observation() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.native_transfer_canonical_coverage() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.native_transfer_job_coverage() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.native_transfer_outbox_coverage() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.native_transfer_result_coverage() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.proxy_interaction_coverage_contains(target_chain_id numeric, target_start_block numeric, target_start_hash bytea, target_end_block numeric, target_end_hash bytea) SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.prune_search_catalog(requested_chain_id numeric, requested_retention_generations bigint) SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.record_ens_name_search_document() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.record_search_catalog_document() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.record_verified_contract_search_catalog_document() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.refresh_native_transfer_coverage(p_chain numeric, p_number numeric) SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.refresh_proxy_interaction_coverage_block(target_chain_id numeric, target_block_number numeric) SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.refresh_proxy_interaction_coverage_from_canonical() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.refresh_proxy_interaction_coverage_from_journal() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.refresh_proxy_interaction_coverage_from_outbox() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.refresh_proxy_interaction_coverage_from_stage_result() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.reject_durable_stage_publication_mutation() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.reject_proxy_evidence_mutation() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.reject_verified_proxy_binding_mutation() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.reject_verifier_v2_result_mutation() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.require_enrichment_publication_protocol() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
    EXECUTE format('ALTER FUNCTION %I.synchronize_derived_attempt_trace_canonicality() SET search_path = %I, pg_catalog',
        current_schema(), current_schema());
END;
$$;
