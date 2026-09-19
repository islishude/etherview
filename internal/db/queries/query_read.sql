-- name: GetAddressDelegationHistory :one
SELECT EXISTS (
           SELECT 1
           FROM canonical_blocks AS reference
           WHERE reference.chain_id = sqlc.arg('chain_id')::numeric
             AND reference.number = sqlc.arg('reference_number')::numeric
             AND reference.block_hash = sqlc.arg('reference_hash')
       ) AS reference_canonical,
       EXISTS (
           SELECT 1
           FROM eip7702_authorizations AS authz
           JOIN canonical_blocks AS canonical
             ON canonical.chain_id = authz.chain_id
            AND canonical.number = authz.block_number
            AND canonical.block_hash = authz.block_hash
           WHERE authz.chain_id = sqlc.arg('chain_id')::numeric
             AND authz.authority = sqlc.arg('authority')
             AND authz.application_status = 'applied'
             AND authz.canonical
             AND authz.block_number <= sqlc.arg('reference_number')::numeric
       ) AS has_history;

-- name: ListAddressWithdrawalsFirst :many
SELECT withdrawal.withdrawal_index::text,
       withdrawal.validator_index::text,
       withdrawal.address,
       withdrawal.amount::text,
       withdrawal.block_number::text,
       withdrawal.block_hash,
       block.timestamp::text
FROM withdrawals AS withdrawal
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = withdrawal.chain_id
 AND canonical.number = withdrawal.block_number
 AND canonical.block_hash = withdrawal.block_hash
JOIN blocks AS block
  ON block.chain_id = withdrawal.chain_id
 AND block.number = withdrawal.block_number
 AND block.hash = withdrawal.block_hash
WHERE withdrawal.chain_id = sqlc.arg('chain_id')::numeric
  AND withdrawal.address = sqlc.arg('address')
  AND withdrawal.block_number <= sqlc.arg('max_block_number')::numeric
ORDER BY withdrawal.withdrawal_index DESC
LIMIT sqlc.arg('limit');

-- name: ListAddressWithdrawalsAfter :many
SELECT withdrawal.withdrawal_index::text,
       withdrawal.validator_index::text,
       withdrawal.address,
       withdrawal.amount::text,
       withdrawal.block_number::text,
       withdrawal.block_hash,
       block.timestamp::text
FROM withdrawals AS withdrawal
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = withdrawal.chain_id
 AND canonical.number = withdrawal.block_number
 AND canonical.block_hash = withdrawal.block_hash
JOIN blocks AS block
  ON block.chain_id = withdrawal.chain_id
 AND block.number = withdrawal.block_number
 AND block.hash = withdrawal.block_hash
WHERE withdrawal.chain_id = sqlc.arg('chain_id')::numeric
  AND withdrawal.address = sqlc.arg('address')
  AND withdrawal.block_number <= sqlc.arg('max_block_number')::numeric
  AND withdrawal.withdrawal_index < sqlc.arg('max_withdrawal_index')::numeric
ORDER BY withdrawal.withdrawal_index DESC
LIMIT sqlc.arg('limit');

-- name: ValidateAddressWithdrawalCursor :one
SELECT EXISTS (
           SELECT 1 FROM canonical_blocks AS snapshot
           WHERE snapshot.chain_id = sqlc.arg('chain_id')::numeric
             AND snapshot.number = sqlc.arg('snapshot_number')::numeric
             AND snapshot.block_hash = sqlc.arg('snapshot_hash')
       )
       AND EXISTS (
           SELECT 1
           FROM withdrawals AS withdrawal
           JOIN canonical_blocks AS canonical
             ON canonical.chain_id = withdrawal.chain_id
            AND canonical.number = withdrawal.block_number
            AND canonical.block_hash = withdrawal.block_hash
           WHERE withdrawal.chain_id = sqlc.arg('chain_id')::numeric
             AND withdrawal.address = sqlc.arg('address')
             AND withdrawal.withdrawal_index = sqlc.arg('before_index')::numeric
             AND withdrawal.block_number = sqlc.arg('before_number')::numeric
             AND withdrawal.block_hash = sqlc.arg('before_hash')
       ) AS valid;

-- name: GetBlockTransactionTargetByHash :one
SELECT number::text AS block_number, hash AS block_hash
FROM blocks
WHERE chain_id = sqlc.arg('chain_id')::numeric AND hash = sqlc.arg('hash')
LIMIT 1;

-- name: GetBlockTransactionTargetByNumber :one
SELECT number::text AS block_number, block_hash
FROM canonical_blocks
WHERE chain_id = sqlc.arg('chain_id')::numeric AND number = sqlc.arg('number')::numeric;

-- name: ValidateBlockTransactionCursor :one
SELECT EXISTS (
    SELECT 1
    FROM blocks
    WHERE chain_id = sqlc.arg('chain_id')::numeric AND number = sqlc.arg('number')::numeric AND hash = sqlc.arg('hash')
) AS valid;

-- name: ListBlockTransactions :many
SELECT
    inclusion.raw AS raw,
    receipt.raw AS receipt_raw,
    inclusion.block_number::text AS block_number,
    inclusion.block_hash AS block_hash,
    inclusion.tx_index AS tx_index,
    inclusion.tx_hash AS tx_hash,
    ((canonical.block_hash IS NOT NULL))::boolean AS canonical,
    finality.safe_number AS safe_number,
    finality.finalized_number AS finalized_number,
    block.timestamp::text AS block_timestamp,
    block.base_fee_per_gas_quantity AS block_base_fee_per_gas
FROM transaction_inclusions AS inclusion
JOIN blocks AS block
  ON block.chain_id = inclusion.chain_id
 AND block.number = inclusion.block_number
 AND block.hash = inclusion.block_hash
JOIN receipts AS receipt
  ON receipt.chain_id = inclusion.chain_id
 AND receipt.block_number = inclusion.block_number
 AND receipt.block_hash = inclusion.block_hash
 AND receipt.tx_index = inclusion.tx_index
LEFT JOIN canonical_blocks AS canonical
  ON canonical.chain_id = inclusion.chain_id
 AND canonical.number = inclusion.block_number
 AND canonical.block_hash = inclusion.block_hash
LEFT JOIN chain_finality AS finality ON finality.chain_id = inclusion.chain_id
WHERE inclusion.chain_id = sqlc.arg('chain_id')::numeric
  AND inclusion.block_number = sqlc.arg('block_number')::numeric
  AND inclusion.block_hash = sqlc.arg('block_hash')
  AND inclusion.tx_index > sqlc.arg('tx_index')
ORDER BY inclusion.tx_index ASC
LIMIT sqlc.arg('limit');

-- name: GetCurrentQueryTip :one
SELECT canonical.number::text, canonical.block_hash
FROM canonical_blocks AS canonical
WHERE canonical.chain_id = sqlc.arg('chain_id')::numeric
ORDER BY canonical.number DESC, canonical.block_hash DESC
LIMIT 1;

-- name: ValidateBlockCursor :one
SELECT EXISTS (
           SELECT 1 FROM canonical_blocks AS snapshot
           WHERE snapshot.chain_id = sqlc.arg('chain_id')::numeric
             AND snapshot.number = sqlc.arg('snapshot_number')::numeric
             AND snapshot.block_hash = sqlc.arg('snapshot_hash')
       )
       AND EXISTS (
           SELECT 1 FROM canonical_blocks AS boundary
           WHERE boundary.chain_id = sqlc.arg('chain_id')::numeric
             AND boundary.number = sqlc.arg('boundary_number')::numeric
             AND boundary.block_hash = sqlc.arg('boundary_hash')
       ) AS valid;

-- name: ValidateSearchCursor :one
SELECT EXISTS (
           SELECT 1 FROM canonical_blocks AS snapshot
           WHERE snapshot.chain_id = sqlc.arg('chain_id')::numeric
             AND snapshot.number = sqlc.arg('number')::numeric
             AND snapshot.block_hash = sqlc.arg('block_hash')
       ) AND COALESCE((
           SELECT min_generation <= sqlc.arg('min_generation') AND generation >= sqlc.arg('min_generation')
           FROM search_catalog_generations
           WHERE chain_id = sqlc.arg('chain_id')::numeric
       ), sqlc.arg('min_generation') = 0) AS valid;

-- name: GetCurrentSearchGeneration :one
SELECT COALESCE(generation, 0)::bigint AS generation,
       COALESCE(min_generation, 0)::bigint AS min_generation
FROM (SELECT 1) AS singleton
LEFT JOIN search_catalog_generations ON chain_id = sqlc.arg('chain_id')::numeric;

-- name: ValidateResolvedSearchName :one
WITH visible_documents AS (
    SELECT document.*
    FROM search_catalog_documents AS document
    WHERE document.chain_id = sqlc.arg('chain_id')::numeric
      AND document.valid_from_generation <= sqlc.arg('valid_from_generation')
      AND (document.valid_to_generation IS NULL OR document.valid_to_generation > sqlc.arg('valid_from_generation'))
)
SELECT EXISTS (
    SELECT 1
    FROM ens_name_observations AS observation
    WHERE observation.chain_id = sqlc.arg('chain_id')::numeric
      AND observation.id = sqlc.arg('id')::bigint
      AND observation.direction = 'forward'
      AND observation.lookup_key = sqlc.arg('lookup_key')
      AND observation.name = sqlc.arg('lookup_key')
      AND observation.source = sqlc.arg('source')
      AND (
          (
              observation.outcome = 'not_found'
              AND sqlc.arg('address')::bytea IS NULL
              AND NOT EXISTS (
                  SELECT 1 FROM visible_documents AS document
                  WHERE document.source_kind = 'name'
                    AND document.logical_identity = lower(observation.name)
              )
          )
          OR (
              observation.outcome = 'resolved'
              AND observation.address = sqlc.arg('address')::bytea
              AND EXISTS (
                  SELECT 1 FROM visible_documents AS document
                  WHERE document.source_kind = 'name'
                    AND document.name_observation_id = observation.id
                    AND document.source_canonical IS TRUE
                    AND (
                        document.block_hash IS NULL
                        OR EXISTS (
                            SELECT 1 FROM canonical_blocks AS canonical
                            WHERE canonical.chain_id = document.chain_id
                              AND canonical.number = document.block_number
                              AND canonical.block_hash = document.block_hash
                        )
                    )
              )
          )
      )
) AS valid;

-- name: GetHomeRuntimeEventID :one
SELECT COALESCE(MAX(id),0)::bigint AS event_id
FROM runtime_events
WHERE chain_id = sqlc.arg('chain_id')::numeric;

-- name: GetHomeRuntimeStatus :one
SELECT latest_number,
       indexed_number,
       highest_covered_number,
       backfill_complete,
       ready
FROM sync_runtime_status
WHERE chain_id = sqlc.arg('chain_id')::numeric;
