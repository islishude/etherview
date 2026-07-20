-- name: GetChainIdentity :one
SELECT chain_id::text AS chain_id, genesis_hash
FROM chains
WHERE chain_id = sqlc.arg(chain_id)::numeric;

-- name: GetCanonicalTip :one
SELECT canonical.number::text AS number, canonical.block_hash, block.parent_hash
FROM canonical_blocks AS canonical
JOIN blocks AS block
  ON block.chain_id = canonical.chain_id
 AND block.number = canonical.number
 AND block.hash = canonical.block_hash
WHERE canonical.chain_id = sqlc.arg(chain_id)::numeric
ORDER BY canonical.number DESC
LIMIT 1;

-- name: GetCanonicalBlock :one
SELECT canonical.number::text AS number, canonical.block_hash, block.parent_hash
FROM canonical_blocks AS canonical
JOIN blocks AS block
  ON block.chain_id = canonical.chain_id
 AND block.number = canonical.number
 AND block.hash = canonical.block_hash
WHERE canonical.chain_id = sqlc.arg(chain_id)::numeric
  AND canonical.number = sqlc.arg(block_number)::numeric;

-- name: ListCanonicalBlocks :many
SELECT block.raw
FROM canonical_blocks AS canonical
JOIN blocks AS block
  ON block.chain_id = canonical.chain_id
 AND block.number = canonical.number
 AND block.hash = canonical.block_hash
WHERE canonical.chain_id = sqlc.arg(chain_id)::numeric
  AND canonical.number < sqlc.arg(before_number)::numeric
ORDER BY canonical.number DESC
LIMIT sqlc.arg(page_limit);

-- name: ListAppliedMigrations :many
SELECT version, checksum, applied_at
FROM etherview_schema_migrations
ORDER BY version;
