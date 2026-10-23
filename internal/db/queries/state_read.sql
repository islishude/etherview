-- name: StateCanonicalTip :one
SELECT canonical.number::text, canonical.block_hash
FROM canonical_blocks AS canonical
WHERE canonical.chain_id = sqlc.arg('chain_id')::numeric
ORDER BY canonical.number DESC
LIMIT 1;

-- name: StateIsCanonical :one
SELECT EXISTS (
    SELECT 1 FROM canonical_blocks
    WHERE chain_id = sqlc.arg('chain_id')::numeric AND number = sqlc.arg('number')::numeric AND block_hash = sqlc.arg('block_hash')
);

-- name: StateERC20BalanceObservations :many
SELECT observation.token_address, observation.balance::text, observation.confidence
FROM erc20_balance_reconciliations AS observation
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = observation.chain_id
 AND canonical.number = observation.block_number
 AND canonical.block_hash = observation.block_hash
WHERE observation.chain_id = sqlc.arg('chain_id')::numeric
  AND observation.owner_address = sqlc.arg('owner_address')
  AND observation.block_number = sqlc.arg('block_number')::numeric
  AND observation.block_hash = sqlc.arg('block_hash')
  AND observation.token_address = ANY(sqlc.arg('token_addresses')::bytea[])
ORDER BY observation.token_address;

-- name: StateERC721OwnerObservation :one
SELECT observation.state, observation.owner_address, observation.confidence
FROM erc721_owner_reconciliations AS observation
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = observation.chain_id
 AND canonical.number = observation.block_number
 AND canonical.block_hash = observation.block_hash
WHERE observation.chain_id = sqlc.arg('chain_id')::numeric
  AND observation.token_address = sqlc.arg('token_address')
  AND observation.token_id = sqlc.arg('token_id')::numeric
  AND observation.block_number = sqlc.arg('block_number')::numeric
  AND observation.block_hash = sqlc.arg('block_hash');

-- name: StateERC721OwnerObservations :many
-- Parallel set-returning expressions pair equal-length arrays by position.
WITH candidate AS (
    SELECT unnest(sqlc.arg('token_addresses')::bytea[]) AS token_address,
           unnest(sqlc.arg('token_ids')::numeric[]) AS token_id
)
SELECT observation.token_address, observation.token_id::text,
       observation.state, observation.owner_address, observation.confidence
FROM candidate
JOIN erc721_owner_reconciliations AS observation
  ON observation.token_address = candidate.token_address
 AND observation.token_id = candidate.token_id
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = observation.chain_id
 AND canonical.number = observation.block_number
 AND canonical.block_hash = observation.block_hash
WHERE observation.chain_id = sqlc.arg('chain_id')::numeric
  AND observation.block_number = sqlc.arg('block_number')::numeric
  AND observation.block_hash = sqlc.arg('block_hash');

-- name: StateERC1155BalanceObservations :many
-- Parallel set-returning expressions pair equal-length arrays by position.
WITH candidate AS (
    SELECT unnest(sqlc.arg('token_addresses')::bytea[]) AS token_address,
           unnest(sqlc.arg('token_ids')::numeric[]) AS token_id
)
SELECT observation.token_address, observation.token_id::text,
       observation.balance::text, observation.confidence
FROM candidate
JOIN erc1155_balance_reconciliations AS observation
  ON observation.token_address = candidate.token_address
 AND observation.token_id = candidate.token_id
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = observation.chain_id
 AND canonical.number = observation.block_number
 AND canonical.block_hash = observation.block_hash
WHERE observation.chain_id = sqlc.arg('chain_id')::numeric
  AND observation.owner_address = sqlc.arg('owner_address')
  AND observation.block_number = sqlc.arg('block_number')::numeric
  AND observation.block_hash = sqlc.arg('block_hash');
