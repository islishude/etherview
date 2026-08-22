-- name: StateCanonicalTip :many
SELECT canonical.number::text, canonical.block_hash
FROM canonical_blocks AS canonical
WHERE canonical.chain_id = $1::numeric
ORDER BY canonical.number DESC
LIMIT 1;

-- name: StateIsCanonical :many
SELECT EXISTS (
    SELECT 1 FROM canonical_blocks
    WHERE chain_id = $1::numeric AND number = $2::numeric AND block_hash = $3
);

-- name: StateERC721OwnerObservation :many
SELECT observation.state, observation.owner_address, observation.confidence
FROM erc721_owner_reconciliations AS observation
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = observation.chain_id
 AND canonical.number = observation.block_number
 AND canonical.block_hash = observation.block_hash
WHERE observation.chain_id = $1::numeric
  AND observation.token_address = $2
  AND observation.token_id = $3::numeric
  AND observation.block_number = $4::numeric
  AND observation.block_hash = $5;

-- name: StateERC1155BalanceObservation :many
SELECT observation.balance::text, observation.confidence
FROM erc1155_balance_reconciliations AS observation
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = observation.chain_id
 AND canonical.number = observation.block_number
 AND canonical.block_hash = observation.block_hash
WHERE observation.chain_id = $1::numeric
  AND observation.token_address = $2
  AND observation.token_id = $3::numeric
  AND observation.owner_address = $4
  AND observation.block_number = $5::numeric
  AND observation.block_hash = $6;
