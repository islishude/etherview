\set ON_ERROR_STOP on
\pset tuples_only on
\pset format unaligned

-- Compare correctness-bearing identities and results while excluding process
-- ownership, wall-clock timestamps, generated surrogate IDs, and poll cadence.
-- The smoke runs this projection against two independently migrated databases.
-- Raw runtime-event, lease, superseded-stage, and mempool-poll history is
-- topology-dependent. The projection compares final durable jobs/publications,
-- durable replay sources, runtime status, and the latest mempool snapshot
-- instead.
WITH normalized(relation_name, payload) AS (
    SELECT 'chains', to_jsonb(row_value) - '{created_at}'::text[]
    FROM chains AS row_value
    UNION ALL
    SELECT 'core_index_configuration', to_jsonb(row_value) - '{created_at,updated_at}'::text[]
    FROM core_index_configuration AS row_value
    UNION ALL
    SELECT 'core_coverage_ranges', to_jsonb(row_value) - '{created_at,updated_at}'::text[]
    FROM core_coverage_ranges AS row_value
    UNION ALL
    SELECT 'blocks', to_jsonb(row_value) - '{inserted_at}'::text[]
    FROM blocks AS row_value
    UNION ALL
    SELECT 'canonical_blocks', to_jsonb(row_value) - '{updated_at}'::text[]
    FROM canonical_blocks AS row_value
    UNION ALL
    SELECT 'transactions', to_jsonb(row_value) - '{inserted_at}'::text[]
    FROM transactions AS row_value
    UNION ALL
    SELECT 'transaction_inclusions', to_jsonb(row_value)
    FROM transaction_inclusions AS row_value
    UNION ALL
    SELECT 'receipts', to_jsonb(row_value)
    FROM receipts AS row_value
    UNION ALL
    SELECT 'logs', to_jsonb(row_value)
    FROM logs AS row_value
    UNION ALL
    SELECT 'withdrawals', to_jsonb(row_value)
    FROM withdrawals AS row_value
    UNION ALL
    SELECT 'index_checkpoints', to_jsonb(row_value) - '{updated_at}'::text[]
    FROM index_checkpoints AS row_value
    UNION ALL
    SELECT 'chain_finality', to_jsonb(row_value) - '{updated_at}'::text[]
    FROM chain_finality AS row_value
    UNION ALL
    SELECT 'transactional_outbox',
           (to_jsonb(row_value) - '{id,available_at,published_at,last_error,created_at}'::text[])
               || jsonb_build_object('published', published_at IS NOT NULL)
    FROM transactional_outbox AS row_value
    UNION ALL
    SELECT 'durable_jobs',
           to_jsonb(row_value) - '{id,available_at,leased_by,lease_token,lease_expires_at,last_error,created_at,updated_at}'::text[]
    FROM durable_jobs AS row_value
    UNION ALL
    SELECT 'durable_job_replay_requests',
           jsonb_build_object(
               'chain_id', job.chain_id,
               'stage', job.stage,
               'stage_version', job.stage_version,
               'idempotency_key', job.idempotency_key,
               'source_kind', replay.source_kind,
               'source_key', replay.source_key,
               'requested_generation', replay.requested_generation
           )
    FROM durable_job_replay_requests AS replay
    JOIN durable_jobs AS job ON job.id = replay.job_id
    UNION ALL
    SELECT 'published_block_stage_results',
           to_jsonb(row_value) - '{durable_job_id,completed_at,last_error}'::text[]
    FROM published_block_stage_results AS row_value
    UNION ALL
    SELECT 'block_journals',
           to_jsonb(row_value) - '{durable_job_id,created_at}'::text[]
    FROM block_journals AS row_value
    UNION ALL
    SELECT 'contract_code_observations', to_jsonb(row_value) - '{observed_at}'::text[]
    FROM contract_code_observations AS row_value
    UNION ALL
    SELECT 'proxy_observations', to_jsonb(row_value)
    FROM proxy_observations AS row_value
    UNION ALL
    SELECT 'contract_abis', to_jsonb(row_value) - '{created_at}'::text[]
    FROM contract_abis AS row_value
    UNION ALL
    SELECT 'token_contracts', to_jsonb(row_value) - '{updated_at}'::text[]
    FROM token_contracts AS row_value
    UNION ALL
    SELECT 'token_events', to_jsonb(row_value)
    FROM token_events AS row_value
    UNION ALL
    SELECT 'token_balance_deltas', to_jsonb(row_value)
    FROM token_balance_deltas AS row_value
    UNION ALL
    SELECT 'erc721_owner_reconciliations', to_jsonb(row_value) - '{observed_at}'::text[]
    FROM erc721_owner_reconciliations AS row_value
    UNION ALL
    SELECT 'erc1155_balance_reconciliations', to_jsonb(row_value) - '{observed_at}'::text[]
    FROM erc1155_balance_reconciliations AS row_value
    UNION ALL
    SELECT 'normalized_traces', to_jsonb(row_value)
    FROM normalized_traces AS row_value
    UNION ALL
    SELECT 'block_statistics', to_jsonb(row_value) - '{computed_at}'::text[]
    FROM block_statistics AS row_value
    UNION ALL
    SELECT 'abi_decodings', to_jsonb(row_value) - '{decoded_at}'::text[]
    FROM abi_decodings AS row_value
    UNION ALL
    SELECT 'external_metadata',
           to_jsonb(row_value) - '{fetched_at,expires_at,updated_at,terminal_at}'::text[]
    FROM external_metadata AS row_value
    UNION ALL
    SELECT 'external_metadata_attempts',
           to_jsonb(row_value) - '{id,durable_job_id,attempted_at}'::text[]
    FROM external_metadata_attempts AS row_value
    UNION ALL
    SELECT 'nft_metadata_source_observations', to_jsonb(row_value) - '{observed_at}'::text[]
    FROM nft_metadata_source_observations AS row_value
    UNION ALL
    SELECT 'name_records', to_jsonb(row_value) - '{observed_at}'::text[]
    FROM name_records AS row_value
    UNION ALL
    SELECT 'address_activities', to_jsonb(row_value)
    FROM address_activities AS row_value
    UNION ALL
    SELECT 'search_catalog_generations', to_jsonb(row_value) - '{updated_at}'::text[]
    FROM search_catalog_generations AS row_value
    UNION ALL
    SELECT 'search_catalog_documents',
           to_jsonb(row_value) - '{id,recorded_at}'::text[]
    FROM search_catalog_documents AS row_value
    UNION ALL
    SELECT 'sync_runtime_status',
           to_jsonb(row_value) - '{last_poll_at,updated_at}'::text[]
    FROM sync_runtime_status AS row_value
    UNION ALL
    SELECT 'mempool_transactions',
           to_jsonb(row_value) - '{first_seen_at,last_seen_at,expires_at}'::text[]
    FROM mempool_transactions AS row_value
    UNION ALL
    SELECT 'mempool_status',
           to_jsonb(row_value) - '{latest_snapshot_id,last_attempt_at,last_success_at,updated_at}'::text[]
    FROM mempool_status AS row_value
    UNION ALL
    SELECT 'mempool_latest_snapshot',
           jsonb_build_object(
               'chain_id', snapshot.chain_id,
               'endpoint_name', snapshot.endpoint_name,
               'transaction_count', snapshot.transaction_count
           )
    FROM mempool_status AS status
    JOIN mempool_snapshots AS snapshot
      ON snapshot.chain_id = status.chain_id
     AND snapshot.id = status.latest_snapshot_id
    UNION ALL
    SELECT 'mempool_latest_snapshot_transactions',
           jsonb_build_object(
               'chain_id', member.chain_id,
               'tx_hash', encode(member.tx_hash, 'hex')
           )
    FROM mempool_status AS status
    JOIN mempool_snapshot_transactions AS member
      ON member.chain_id = status.chain_id
     AND member.snapshot_id = status.latest_snapshot_id
),
grouped AS (
    SELECT relation_name, jsonb_agg(payload ORDER BY payload::text) AS payloads
    FROM normalized
    GROUP BY relation_name
)
SELECT relation_name || E'\t' || payloads::text
FROM grouped
ORDER BY relation_name;
