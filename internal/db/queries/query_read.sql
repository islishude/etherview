-- name: GetAddressDelegationHistory :one
SELECT EXISTS (
           SELECT 1
           FROM canonical_blocks AS reference
           WHERE reference.chain_id = sqlc.arg(chain_id)::numeric
             AND reference.number = sqlc.arg(reference_number)::numeric
             AND reference.block_hash = sqlc.arg(reference_hash)
       ) AS reference_canonical,
       EXISTS (
           SELECT 1
           FROM eip7702_authorizations AS authz
           JOIN canonical_blocks AS canonical
             ON canonical.chain_id = authz.chain_id
            AND canonical.number = authz.block_number
            AND canonical.block_hash = authz.block_hash
           WHERE authz.chain_id = sqlc.arg(chain_id)::numeric
             AND authz.authority = sqlc.arg(authority)
             AND authz.application_status = 'applied'
             AND authz.canonical
             AND authz.block_number <= sqlc.arg(reference_number)::numeric
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
WHERE withdrawal.chain_id = $1::numeric
  AND withdrawal.address = $2
  AND withdrawal.block_number <= $3::numeric
ORDER BY withdrawal.withdrawal_index DESC
LIMIT $4;

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
WHERE withdrawal.chain_id = $1::numeric
  AND withdrawal.address = $2
  AND withdrawal.block_number <= $3::numeric
  AND withdrawal.withdrawal_index < $4::numeric
ORDER BY withdrawal.withdrawal_index DESC
LIMIT $5;

-- name: ValidateAddressWithdrawalCursor :one
SELECT EXISTS (
           SELECT 1 FROM canonical_blocks AS snapshot
           WHERE snapshot.chain_id = sqlc.arg(chain_id)::numeric
             AND snapshot.number = sqlc.arg(snapshot_number)::numeric
             AND snapshot.block_hash = sqlc.arg(snapshot_hash)
       )
       AND EXISTS (
           SELECT 1
           FROM withdrawals AS withdrawal
           JOIN canonical_blocks AS canonical
             ON canonical.chain_id = withdrawal.chain_id
            AND canonical.number = withdrawal.block_number
            AND canonical.block_hash = withdrawal.block_hash
           WHERE withdrawal.chain_id = sqlc.arg(chain_id)::numeric
             AND withdrawal.address = sqlc.arg(address)
             AND withdrawal.withdrawal_index = sqlc.arg(before_index)::numeric
             AND withdrawal.block_number = sqlc.arg(before_number)::numeric
             AND withdrawal.block_hash = sqlc.arg(before_hash)
       ) AS valid;

-- name: GetBlockTransactionTargetByHash :one
SELECT number::text AS block_number, hash AS block_hash
FROM blocks
WHERE chain_id = $1::numeric AND hash = $2
LIMIT 1;

-- name: GetBlockTransactionTargetByNumber :one
SELECT number::text AS block_number, block_hash
FROM canonical_blocks
WHERE chain_id = $1::numeric AND number = $2::numeric;

-- name: ValidateBlockTransactionCursor :one
SELECT EXISTS (
    SELECT 1
    FROM blocks
    WHERE chain_id = $1::numeric AND number = $2::numeric AND hash = $3
) AS valid;

-- name: ListBlockTransactions :many
SELECT inclusion.raw,
       receipt.raw AS receipt_raw,
       inclusion.block_number::text,
       inclusion.block_hash,
       inclusion.tx_index,
       inclusion.tx_hash,
       (canonical.block_hash IS NOT NULL) AS canonical,
       finality.safe_number::text AS safe_number,
       finality.finalized_number::text AS finalized_number,
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
WHERE inclusion.chain_id = $1::numeric
  AND inclusion.block_number = $2::numeric
  AND inclusion.block_hash = $3
  AND inclusion.tx_index > $4
ORDER BY inclusion.tx_index ASC
LIMIT $5;

-- name: GetCurrentQueryTip :one
SELECT canonical.number::text, canonical.block_hash
FROM canonical_blocks AS canonical
WHERE canonical.chain_id = $1::numeric
ORDER BY canonical.number DESC, canonical.block_hash DESC
LIMIT 1;

-- name: ValidateBlockCursor :one
SELECT EXISTS (
           SELECT 1 FROM canonical_blocks AS snapshot
           WHERE snapshot.chain_id = $1::numeric
             AND snapshot.number = $2::numeric
             AND snapshot.block_hash = $3
       )
       AND EXISTS (
           SELECT 1 FROM canonical_blocks AS boundary
           WHERE boundary.chain_id = $1::numeric
             AND boundary.number = $4::numeric
             AND boundary.block_hash = $5
       ) AS valid;

-- name: ValidateSearchCursor :one
SELECT EXISTS (
           SELECT 1 FROM canonical_blocks AS snapshot
           WHERE snapshot.chain_id = $1::numeric
             AND snapshot.number = $2::numeric
             AND snapshot.block_hash = $3
       ) AND COALESCE((
           SELECT min_generation <= $4 AND generation >= $4
           FROM search_catalog_generations
           WHERE chain_id = $1::numeric
       ), $4 = 0) AS valid;

-- name: GetCurrentSearchGeneration :one
SELECT COALESCE(generation, 0)::bigint AS generation,
       COALESCE(min_generation, 0)::bigint AS min_generation
FROM (SELECT 1) AS singleton
LEFT JOIN search_catalog_generations ON chain_id = $1::numeric;

-- name: ValidateResolvedSearchName :one
WITH visible_documents AS (
    SELECT document.*
    FROM search_catalog_documents AS document
    WHERE document.chain_id = $1::numeric
      AND document.valid_from_generation <= $5
      AND (document.valid_to_generation IS NULL OR document.valid_to_generation > $5)
)
SELECT EXISTS (
    SELECT 1
    FROM ens_name_observations AS observation
    WHERE observation.chain_id = $1::numeric
      AND observation.id = $2::bigint
      AND observation.direction = 'forward'
      AND observation.lookup_key = $3
      AND observation.name = $3
      AND observation.source = $4
      AND (
          (
              observation.outcome = 'not_found'
              AND $6::bytea IS NULL
              AND NOT EXISTS (
                  SELECT 1 FROM visible_documents AS document
                  WHERE document.source_kind = 'name'
                    AND document.logical_identity = lower(observation.name)
              )
          )
          OR (
              observation.outcome = 'resolved'
              AND observation.address = $6::bytea
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
SELECT MAX(id)
FROM runtime_events
WHERE chain_id = $1::numeric;

-- name: GetHomeRuntimeStatus :one
SELECT latest_number::text,
       indexed_number::text,
       highest_covered_number::text,
       backfill_complete,
       ready
FROM sync_runtime_status
WHERE chain_id = $1::numeric;
