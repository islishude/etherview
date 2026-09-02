-- name: ERC4337SourceBlock :one
SELECT block.raw
FROM blocks AS block
WHERE block.chain_id = sqlc.arg(chain_id)::numeric
  AND block.number = sqlc.arg(block_number)::numeric
  AND block.hash = sqlc.arg(block_hash)::bytea;

-- name: ERC4337SourceReceipts :many
SELECT receipt.raw
FROM receipts AS receipt
WHERE receipt.chain_id = sqlc.arg(chain_id)::numeric
  AND receipt.block_number = sqlc.arg(block_number)::numeric
  AND receipt.block_hash = sqlc.arg(block_hash)::bytea
ORDER BY receipt.tx_index;

-- name: ERC4337DeleteBlockOutput :exec
WITH delete_events AS (
    DELETE FROM erc4337_user_operation_events
    WHERE chain_id = sqlc.arg(chain_id)::numeric
      AND configuration_digest = sqlc.arg(configuration_digest)::bytea
      AND block_number = sqlc.arg(block_number)::numeric
      AND block_hash = sqlc.arg(block_hash)::bytea
), delete_participants AS (
    DELETE FROM erc4337_user_operation_participants
    WHERE chain_id = sqlc.arg(chain_id)::numeric
      AND configuration_digest = sqlc.arg(configuration_digest)::bytea
      AND block_number = sqlc.arg(block_number)::numeric
      AND block_hash = sqlc.arg(block_hash)::bytea
)
DELETE FROM erc4337_user_operations
WHERE chain_id = sqlc.arg(chain_id)::numeric
  AND configuration_digest = sqlc.arg(configuration_digest)::bytea
  AND block_number = sqlc.arg(block_number)::numeric
  AND block_hash = sqlc.arg(block_hash)::bytea;

-- name: ERC4337ClearReplayOutputs :exec
WITH delete_events AS (
    DELETE FROM erc4337_user_operation_events
    WHERE chain_id = sqlc.arg(chain_id)::numeric
      AND block_number = sqlc.arg(block_number)::numeric
      AND block_hash = sqlc.arg(block_hash)::bytea
), delete_participants AS (
    DELETE FROM erc4337_user_operation_participants
    WHERE chain_id = sqlc.arg(chain_id)::numeric
      AND block_number = sqlc.arg(block_number)::numeric
      AND block_hash = sqlc.arg(block_hash)::bytea
)
DELETE FROM erc4337_user_operations
WHERE chain_id = sqlc.arg(chain_id)::numeric
  AND block_number = sqlc.arg(block_number)::numeric
  AND block_hash = sqlc.arg(block_hash)::bytea;

-- name: ERC4337InsertUserOperation :exec
INSERT INTO erc4337_user_operations (
    chain_id, configuration_digest, block_number, block_hash,
    transaction_hash, transaction_index, operation_index, event_log_index, user_op_hash,
    entry_point, entry_point_version, sender, nonce, nonce_key, nonce_sequence,
    bundler, beneficiary, init_kind, factory, paymaster, aggregator,
    success, actual_gas_cost, actual_gas_used,
    call_gas_limit, verification_gas_limit, pre_verification_gas,
    max_fee_per_gas, max_priority_fee_per_gas,
    paymaster_verification_gas_limit, paymaster_post_op_gas_limit,
    init_code, factory_data, call_data, paymaster_and_data, paymaster_data,
    paymaster_signature, signature, account_gas_limits, gas_fees,
    aggregated_signature, canonical
) VALUES (
    sqlc.arg(chain_id)::numeric,
    sqlc.arg(configuration_digest)::bytea,
    sqlc.arg(block_number)::numeric,
    sqlc.arg(block_hash)::bytea,
    sqlc.arg(transaction_hash)::bytea,
    sqlc.arg(transaction_index)::bigint,
    sqlc.arg(operation_index)::bigint,
    sqlc.arg(event_log_index)::bigint,
    sqlc.arg(user_op_hash)::bytea,
    sqlc.arg(entry_point)::bytea,
    sqlc.arg(entry_point_version)::text,
    sqlc.arg(sender)::bytea,
    sqlc.arg(nonce)::numeric,
    sqlc.arg(nonce_key)::numeric,
    sqlc.arg(nonce_sequence)::numeric,
    sqlc.arg(bundler)::bytea,
    sqlc.arg(beneficiary)::bytea,
    sqlc.arg(init_kind)::text,
    sqlc.narg(factory)::bytea,
    sqlc.narg(paymaster)::bytea,
    sqlc.narg(aggregator)::bytea,
    sqlc.arg(success)::boolean,
    sqlc.arg(actual_gas_cost)::numeric,
    sqlc.arg(actual_gas_used)::numeric,
    sqlc.arg(call_gas_limit)::numeric,
    sqlc.arg(verification_gas_limit)::numeric,
    sqlc.arg(pre_verification_gas)::numeric,
    sqlc.arg(max_fee_per_gas)::numeric,
    sqlc.arg(max_priority_fee_per_gas)::numeric,
    NULLIF(sqlc.arg(paymaster_verification_gas_limit)::text, '')::numeric,
    NULLIF(sqlc.arg(paymaster_post_op_gas_limit)::text, '')::numeric,
    sqlc.arg(init_code)::bytea,
    sqlc.arg(factory_data)::bytea,
    sqlc.arg(call_data)::bytea,
    sqlc.arg(paymaster_and_data)::bytea,
    sqlc.arg(paymaster_data)::bytea,
    sqlc.arg(paymaster_signature)::bytea,
    sqlc.arg(signature)::bytea,
    sqlc.narg(account_gas_limits)::bytea,
    sqlc.narg(gas_fees)::bytea,
    sqlc.arg(aggregated_signature)::bytea,
    TRUE
);

-- name: ERC4337InsertUserOperationEvent :exec
INSERT INTO erc4337_user_operation_events (
    chain_id, configuration_digest, block_number, block_hash,
    transaction_hash, operation_index, log_index, event_kind,
    sender, nonce, related_address, paymaster, raw_data, reason, panic_code,
    canonical
) VALUES (
    sqlc.arg(chain_id)::numeric,
    sqlc.arg(configuration_digest)::bytea,
    sqlc.arg(block_number)::numeric,
    sqlc.arg(block_hash)::bytea,
    sqlc.arg(transaction_hash)::bytea,
    sqlc.arg(operation_index)::bigint,
    sqlc.arg(log_index)::bigint,
    sqlc.arg(event_kind)::text,
    sqlc.arg(sender)::bytea,
    NULLIF(sqlc.arg(nonce)::text, '')::numeric,
    sqlc.narg(related_address)::bytea,
    sqlc.narg(paymaster)::bytea,
    sqlc.arg(raw_data)::bytea,
    NULLIF(sqlc.arg(reason)::text, ''),
    NULLIF(sqlc.arg(panic_code)::text, '')::numeric,
    TRUE
);

-- name: ERC4337InsertUserOperationParticipant :exec
INSERT INTO erc4337_user_operation_participants (
    chain_id, configuration_digest, block_number, block_hash,
    transaction_hash, operation_index, address, role, canonical
) VALUES (
    sqlc.arg(chain_id)::numeric,
    sqlc.arg(configuration_digest)::bytea,
    sqlc.arg(block_number)::numeric,
    sqlc.arg(block_hash)::bytea,
    sqlc.arg(transaction_hash)::bytea,
    sqlc.arg(operation_index)::bigint,
    sqlc.arg(address)::bytea,
    sqlc.arg(role)::text,
    TRUE
) ON CONFLICT DO NOTHING;

-- name: ERC4337RemoveCoveredBlock :one
SELECT erc4337_remove_covered_block(
    sqlc.arg(chain_id)::numeric,
    sqlc.arg(configuration_digest)::bytea,
    sqlc.arg(block_number)::numeric
) IS NULL AS removed;

-- name: ERC4337AddCoveredBlock :one
SELECT erc4337_add_covered_block(
    sqlc.arg(chain_id)::numeric,
    sqlc.arg(configuration_digest)::bytea,
    sqlc.arg(block_number)::numeric,
    sqlc.arg(block_hash)::bytea,
    sqlc.arg(durable_job_id)::bigint,
    sqlc.arg(job_generation)::bigint
) IS NULL AS added;

-- name: ERC4337RemoveBlockCoverage :one
SELECT erc4337_remove_block_coverage(
    sqlc.arg(chain_id)::numeric,
    sqlc.arg(block_number)::numeric,
    sqlc.arg(block_hash)::bytea
);

-- name: ERC4337CurrentSnapshot :one
SELECT coverage.end_block::text AS snapshot_number,
       coverage.end_block_hash AS snapshot_hash
FROM erc4337_coverage_ranges AS coverage
JOIN erc4337_covered_blocks AS required_start
  ON required_start.chain_id = coverage.chain_id
 AND required_start.configuration_digest = coverage.configuration_digest
 AND required_start.block_number = sqlc.arg(index_start)::numeric
JOIN erc4337_covered_blocks AS required_end
  ON required_end.chain_id = coverage.chain_id
 AND required_end.configuration_digest = coverage.configuration_digest
 AND required_end.block_number = coverage.end_block
 AND required_end.block_hash = coverage.end_block_hash
JOIN canonical_blocks AS canonical_end
  ON canonical_end.chain_id = required_end.chain_id
 AND canonical_end.number = required_end.block_number
 AND canonical_end.block_hash = required_end.block_hash
JOIN published_block_stage_results AS published
  ON published.chain_id = required_end.chain_id
 AND published.block_number = required_end.block_number
 AND published.block_hash = required_end.block_hash
 AND published.stage = 'userop'
 AND published.stage_version = 1
 AND published.state = 'complete'
 AND published.durable_job_id = required_end.durable_job_id
 AND published.job_generation = required_end.job_generation
 AND published.details->>'configuration_digest' = encode(required_end.configuration_digest, 'hex')
WHERE coverage.chain_id = sqlc.arg(chain_id)::numeric
  AND coverage.configuration_digest = sqlc.arg(configuration_digest)::bytea
  AND coverage.start_block <= sqlc.arg(index_start)::numeric
  AND coverage.end_block >= sqlc.arg(index_start)::numeric
ORDER BY coverage.start_block DESC
LIMIT 1;

-- name: ERC4337ValidateSnapshot :one
SELECT EXISTS (
    SELECT 1
    FROM erc4337_coverage_ranges AS coverage
    JOIN erc4337_covered_blocks AS required_start
      ON required_start.chain_id = coverage.chain_id
     AND required_start.configuration_digest = coverage.configuration_digest
     AND required_start.block_number = sqlc.arg(index_start)::numeric
    JOIN erc4337_covered_blocks AS required_end
      ON required_end.chain_id = coverage.chain_id
     AND required_end.configuration_digest = coverage.configuration_digest
     AND required_end.block_number = sqlc.arg(snapshot_number)::numeric
     AND required_end.block_hash = sqlc.arg(snapshot_hash)::bytea
    JOIN canonical_blocks AS canonical_end
      ON canonical_end.chain_id = required_end.chain_id
     AND canonical_end.number = required_end.block_number
     AND canonical_end.block_hash = required_end.block_hash
    JOIN published_block_stage_results AS published
      ON published.chain_id = required_end.chain_id
     AND published.block_number = required_end.block_number
     AND published.block_hash = required_end.block_hash
     AND published.stage = 'userop'
     AND published.stage_version = 1
     AND published.state = 'complete'
     AND published.durable_job_id = required_end.durable_job_id
     AND published.job_generation = required_end.job_generation
     AND published.details->>'configuration_digest' = encode(required_end.configuration_digest, 'hex')
    WHERE coverage.chain_id = sqlc.arg(chain_id)::numeric
      AND coverage.configuration_digest = sqlc.arg(configuration_digest)::bytea
      AND coverage.start_block <= sqlc.arg(index_start)::numeric
      AND coverage.end_block >= sqlc.arg(snapshot_number)::numeric
);

-- name: ERC4337ListUserOperations :many
SELECT operation.user_op_hash, operation.entry_point,
       operation.entry_point_version, operation.sender,
       operation.nonce::text, operation.nonce_key::text,
       operation.nonce_sequence::text, operation.success,
       operation.actual_gas_cost::text, operation.actual_gas_used::text,
       operation.transaction_hash, operation.transaction_index,
       operation.operation_index, operation.event_log_index, operation.block_number::text,
       operation.block_hash, operation.block_timestamp::text,
       COALESCE(operation.safe_number::text, ''), COALESCE(operation.finalized_number::text, ''),
       operation.bundler, operation.beneficiary, operation.init_kind,
       operation.factory, operation.paymaster, operation.aggregator,
       '[]'::jsonb AS participating_roles
FROM published_erc4337_user_operations AS operation
WHERE operation.chain_id = sqlc.arg(chain_id)::numeric
  AND operation.configuration_digest = sqlc.arg(configuration_digest)::bytea
  AND operation.block_number >= sqlc.arg(index_start)::numeric
  AND operation.block_number <= sqlc.arg(snapshot_number)::numeric
  AND (
      NOT sqlc.arg(has_boundary)::boolean OR
      (operation.block_number, operation.transaction_index,
       operation.operation_index, operation.user_op_hash) <
      (sqlc.arg(before_block_number)::numeric,
       sqlc.arg(before_transaction_index)::bigint,
       sqlc.arg(before_operation_index)::bigint,
       sqlc.arg(before_user_op_hash)::bytea)
  )
ORDER BY operation.block_number DESC, operation.transaction_index DESC,
         operation.operation_index DESC, operation.user_op_hash DESC
LIMIT sqlc.arg(page_limit);

-- name: ERC4337ListTransactionUserOperations :many
SELECT operation.user_op_hash, operation.entry_point,
       operation.entry_point_version, operation.sender,
       operation.nonce::text, operation.nonce_key::text,
       operation.nonce_sequence::text, operation.success,
       operation.actual_gas_cost::text, operation.actual_gas_used::text,
       operation.transaction_hash, operation.transaction_index,
       operation.operation_index, operation.event_log_index, operation.block_number::text,
       operation.block_hash, operation.block_timestamp::text,
       COALESCE(operation.safe_number::text, ''), COALESCE(operation.finalized_number::text, ''),
       operation.bundler, operation.beneficiary, operation.init_kind,
       operation.factory, operation.paymaster, operation.aggregator,
       '[]'::jsonb AS participating_roles
FROM published_erc4337_user_operations AS operation
WHERE operation.chain_id = sqlc.arg(chain_id)::numeric
  AND operation.configuration_digest = sqlc.arg(configuration_digest)::bytea
  AND operation.transaction_hash = sqlc.arg(transaction_hash)::bytea
  AND operation.block_number >= sqlc.arg(index_start)::numeric
  AND operation.block_number <= sqlc.arg(snapshot_number)::numeric
  AND (NOT sqlc.arg(has_boundary)::boolean OR
       operation.operation_index > sqlc.arg(after_operation_index)::bigint)
ORDER BY operation.operation_index
LIMIT sqlc.arg(page_limit);

-- name: ERC4337CanonicalTransactionBlock :one
SELECT inclusion.block_number::text, inclusion.block_hash
FROM transaction_inclusions AS inclusion
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = inclusion.chain_id
 AND canonical.number = inclusion.block_number
 AND canonical.block_hash = inclusion.block_hash
WHERE inclusion.chain_id = sqlc.arg(chain_id)::numeric
  AND inclusion.tx_hash = sqlc.arg(transaction_hash)::bytea;

-- name: ERC4337ListAddressUserOperations :many
SELECT operation.user_op_hash, operation.entry_point,
       operation.entry_point_version, operation.sender,
       operation.nonce::text, operation.nonce_key::text,
       operation.nonce_sequence::text, operation.success,
       operation.actual_gas_cost::text, operation.actual_gas_used::text,
       operation.transaction_hash, operation.transaction_index,
       operation.operation_index, operation.event_log_index, operation.block_number::text,
       operation.block_hash, operation.block_timestamp::text,
       COALESCE(operation.safe_number::text, ''), COALESCE(operation.finalized_number::text, ''),
       operation.bundler, operation.beneficiary, operation.init_kind,
       operation.factory, operation.paymaster, operation.aggregator,
       roles.participating_roles
FROM published_erc4337_user_operations AS operation
JOIN LATERAL (
    SELECT jsonb_agg(participant.role ORDER BY CASE participant.role
        WHEN 'sender' THEN 1 WHEN 'entry_point' THEN 2 WHEN 'bundler' THEN 3
        WHEN 'beneficiary' THEN 4 WHEN 'factory' THEN 5 WHEN 'paymaster' THEN 6
        WHEN 'aggregator' THEN 7 WHEN 'eip7702_delegate' THEN 8 END
    ) AS participating_roles
    FROM erc4337_user_operation_participants AS participant
    WHERE participant.chain_id = operation.chain_id
      AND participant.configuration_digest = operation.configuration_digest
      AND participant.block_number = operation.block_number
      AND participant.block_hash = operation.block_hash
      AND participant.transaction_hash = operation.transaction_hash
      AND participant.operation_index = operation.operation_index
      AND participant.address = sqlc.arg(address)::bytea
      AND participant.canonical
) AS roles ON roles.participating_roles IS NOT NULL
WHERE operation.chain_id = sqlc.arg(chain_id)::numeric
  AND operation.configuration_digest = sqlc.arg(configuration_digest)::bytea
  AND operation.block_number >= sqlc.arg(index_start)::numeric
  AND operation.block_number <= sqlc.arg(snapshot_number)::numeric
  AND (
      NOT sqlc.arg(has_boundary)::boolean OR
      (operation.block_number, operation.transaction_index,
       operation.operation_index, operation.user_op_hash) <
      (sqlc.arg(before_block_number)::numeric,
       sqlc.arg(before_transaction_index)::bigint,
       sqlc.arg(before_operation_index)::bigint,
       sqlc.arg(before_user_op_hash)::bytea)
  )
ORDER BY operation.block_number DESC, operation.transaction_index DESC,
         operation.operation_index DESC, operation.user_op_hash DESC
LIMIT sqlc.arg(page_limit);

-- name: ERC4337GetUserOperation :one
SELECT operation.user_op_hash, operation.entry_point,
       operation.entry_point_version, operation.sender,
       operation.nonce::text, operation.nonce_key::text,
       operation.nonce_sequence::text, operation.success,
       operation.actual_gas_cost::text, operation.actual_gas_used::text,
       operation.transaction_hash, operation.transaction_index,
       operation.operation_index, operation.event_log_index, operation.block_number::text,
       operation.block_hash, operation.block_timestamp::text,
       COALESCE(operation.safe_number::text, ''), COALESCE(operation.finalized_number::text, ''),
       operation.bundler, operation.beneficiary, operation.init_kind,
       operation.factory, operation.paymaster, operation.aggregator,
       '[]'::jsonb AS participating_roles,
       operation.call_gas_limit::text,
       operation.verification_gas_limit::text,
       operation.pre_verification_gas::text,
       operation.max_fee_per_gas::text,
       operation.max_priority_fee_per_gas::text,
       COALESCE(operation.paymaster_verification_gas_limit::text, ''),
       COALESCE(operation.paymaster_post_op_gas_limit::text, ''),
       operation.init_code, operation.factory_data, operation.call_data,
       operation.paymaster_and_data, operation.paymaster_data,
       operation.paymaster_signature, operation.signature,
       operation.account_gas_limits, operation.gas_fees,
       operation.aggregated_signature
FROM published_erc4337_user_operations AS operation
WHERE operation.chain_id = sqlc.arg(chain_id)::numeric
  AND operation.configuration_digest = sqlc.arg(configuration_digest)::bytea
  AND operation.user_op_hash = sqlc.arg(user_op_hash)::bytea;

-- name: ERC4337SearchUserOperation :one
SELECT operation.user_op_hash, operation.sender
FROM published_erc4337_user_operations AS operation
WHERE operation.chain_id = sqlc.arg(chain_id)::numeric
  AND operation.configuration_digest = sqlc.arg(configuration_digest)::bytea
  AND operation.user_op_hash = sqlc.arg(user_op_hash)::bytea
  AND operation.block_number <= sqlc.arg(snapshot_number)::numeric;

-- name: ERC4337ListUserOperationEvents :many
SELECT event.event_kind, event.log_index, event.sender, COALESCE(event.nonce::text, ''),
       event.related_address, event.paymaster, event.raw_data,
       event.reason, COALESCE(event.panic_code::text, '')
FROM erc4337_user_operation_events AS event
JOIN published_erc4337_user_operations AS operation
  ON operation.chain_id = event.chain_id
 AND operation.configuration_digest = event.configuration_digest
 AND operation.block_number = event.block_number
 AND operation.block_hash = event.block_hash
 AND operation.transaction_hash = event.transaction_hash
 AND operation.operation_index = event.operation_index
WHERE event.chain_id = sqlc.arg(chain_id)::numeric
  AND event.configuration_digest = sqlc.arg(configuration_digest)::bytea
  AND event.block_number = sqlc.arg(block_number)::numeric
  AND event.block_hash = sqlc.arg(block_hash)::bytea
  AND event.transaction_hash = sqlc.arg(transaction_hash)::bytea
  AND event.operation_index = sqlc.arg(operation_index)::bigint
  AND event.canonical
ORDER BY event.log_index;
