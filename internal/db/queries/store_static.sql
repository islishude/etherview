-- name: StoreConfiguredStart :one
SELECT configured_start::text
FROM core_index_configuration
WHERE chain_id = sqlc.arg('chain_id')::text::numeric;

-- name: StoreLockConfiguredStart :one
SELECT configured_start::text
FROM core_index_configuration
WHERE chain_id = sqlc.arg('chain_id')::text::numeric
FOR UPDATE;

-- name: StoreCanonicalTip :one
SELECT canonical.number::text, canonical.block_hash, block.parent_hash
FROM canonical_blocks AS canonical
JOIN blocks AS block
  ON block.chain_id = canonical.chain_id
 AND block.number = canonical.number
 AND block.hash = canonical.block_hash
WHERE canonical.chain_id = sqlc.arg('chain_id')::text::numeric
ORDER BY canonical.number DESC
LIMIT 1;

-- name: StoreLockCanonicalTip :one
SELECT canonical.number::text, canonical.block_hash, block.parent_hash
FROM canonical_blocks AS canonical
JOIN blocks AS block
  ON block.chain_id = canonical.chain_id
 AND block.number = canonical.number
 AND block.hash = canonical.block_hash
WHERE canonical.chain_id = sqlc.arg('chain_id')::text::numeric
ORDER BY canonical.number DESC
LIMIT 1
FOR UPDATE OF canonical;

-- name: StoreCanonicalBlock :one
SELECT canonical.number::text, canonical.block_hash, block.parent_hash
FROM canonical_blocks AS canonical
JOIN blocks AS block
  ON block.chain_id = canonical.chain_id
 AND block.number = canonical.number
 AND block.hash = canonical.block_hash
WHERE canonical.chain_id = sqlc.arg('chain_id')::text::numeric
  AND canonical.number = sqlc.arg('number')::text::numeric;

-- name: StoreLockCanonicalBlock :one
SELECT canonical.number::text, canonical.block_hash, block.parent_hash
FROM canonical_blocks AS canonical
JOIN blocks AS block
  ON block.chain_id = canonical.chain_id
 AND block.number = canonical.number
 AND block.hash = canonical.block_hash
WHERE canonical.chain_id = sqlc.arg('chain_id')::text::numeric
  AND canonical.number = sqlc.arg('number')::text::numeric
FOR UPDATE OF canonical;

-- name: StoreFinality :one
SELECT safe_number, safe_hash, finalized_number,
       finalized_hash, updated_at
FROM chain_finality
WHERE chain_id = sqlc.arg('chain_id')::text::numeric;

-- name: StoreLockFinality :one
SELECT safe_number, safe_hash, finalized_number,
       finalized_hash, updated_at
FROM chain_finality
WHERE chain_id = sqlc.arg('chain_id')::text::numeric
FOR UPDATE;

-- name: StoreDeleteDerivedBlockFacts :exec
WITH delete_stage_results AS (
    DELETE FROM block_stage_results WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
), delete_proxy_upgrades AS (
    DELETE FROM proxy_upgrade_events WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
), delete_proxy_initializations AS (
    DELETE FROM proxy_initialization_events WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
), delete_diamond_snapshots AS (
    DELETE FROM diamond_loupe_snapshots WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
), delete_diamond_cuts AS (
    DELETE FROM diamond_cut_events WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
), delete_abi_decodings AS (
    DELETE FROM abi_decodings WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
), delete_contract_abis AS (
    DELETE FROM contract_abis WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
), delete_effective_identities AS (
    DELETE FROM transaction_effective_execution_identities WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
), delete_token_deltas AS (
    DELETE FROM token_balance_deltas WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
), delete_holder_balances AS (
    DELETE FROM erc20_holder_balances WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
), delete_holder_snapshots AS (
    DELETE FROM erc20_holder_snapshots WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
), delete_nft_metadata_updates AS (
    DELETE FROM nft_metadata_update_observations WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
), delete_token_events AS (
    DELETE FROM token_events WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
), delete_trace_attributions AS (
    DELETE FROM trace_log_attributions WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
), delete_traces AS (
    DELETE FROM normalized_traces WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
), delete_execution_resolutions AS (
    DELETE FROM transaction_execution_code_resolutions WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
), delete_authorizations AS (
    DELETE FROM eip7702_authorizations WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
), delete_state_changes AS (
    DELETE FROM transaction_state_changes WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
), delete_user_operation_events AS (
    DELETE FROM erc4337_user_operation_events WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
), delete_user_operation_participants AS (
    DELETE FROM erc4337_user_operation_participants WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
), delete_user_operations AS (
    DELETE FROM erc4337_user_operations WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
), delete_user_operation_coverage AS (
    SELECT erc4337_remove_covered_block(chain_id, configuration_digest, block_number)
    FROM erc4337_covered_blocks
    WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
), delete_address_activities AS (
    DELETE FROM address_activities WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
)
DELETE FROM block_statistics
WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea;

-- name: StoreDeleteCoreBlockFacts :exec
WITH delete_logs AS (
    DELETE FROM logs WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
), delete_receipts AS (
    DELETE FROM receipts WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
), delete_inclusions AS (
    DELETE FROM transaction_inclusions WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
)
DELETE FROM withdrawals
WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea;

-- name: EnrichClearABIReplayOutputs :exec
WITH delete_abi_decodings AS (
    DELETE FROM abi_decodings WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
), delete_contract_abis AS (
    DELETE FROM contract_abis WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
)
DELETE FROM transaction_effective_execution_identities
WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_number = sqlc.arg('block_number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea;
