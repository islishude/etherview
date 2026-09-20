-- name: CatalogTokenContract :one
SELECT
tc.chain_id::text AS chain_id,
tc.address AS address,
tc.code_hash AS code_hash,
tc.standard AS standard,
tc.confidence AS confidence,
tc.name AS name,
tc.symbol AS symbol,
tc.decimals AS decimals,
tc.total_supply AS total_supply,
tc.metadata_state AS metadata_state,
tc.observed_block_number::text AS observed_block_number,
tc.observed_block_hash AS observed_block_hash,
tc.updated_at AS updated_at
FROM token_contracts AS tc
JOIN canonical_blocks AS cb
  ON cb.chain_id = tc.chain_id
 AND cb.number = tc.observed_block_number
 AND cb.block_hash = tc.observed_block_hash
WHERE tc.chain_id = sqlc.arg('chain_id')::numeric
  AND tc.address = sqlc.arg('address')
  AND tc.observed_block_number <= sqlc.arg('max_observed_block_number')::numeric
ORDER BY tc.observed_block_number DESC, tc.code_hash DESC
LIMIT 1;

-- name: CatalogTokenContracts :many
WITH current_tokens AS (
    SELECT DISTINCT ON (tc.address)
tc.chain_id::text AS chain_id, tc.address, tc.code_hash, tc.standard, tc.confidence,
tc.name, tc.symbol, tc.decimals, tc.total_supply AS total_supply, tc.metadata_state,
tc.observed_block_number::text AS observed_block_number, tc.observed_block_hash, tc.updated_at
    FROM token_contracts AS tc
    JOIN canonical_blocks AS cb
      ON cb.chain_id = tc.chain_id
     AND cb.number = tc.observed_block_number
     AND cb.block_hash = tc.observed_block_hash
    WHERE tc.chain_id = sqlc.arg('chain_id')::numeric
      AND tc.observed_block_number <= sqlc.arg('max_observed_block_number')::numeric
      AND (sqlc.arg('has_cursor')::boolean = false OR tc.address > sqlc.arg('address'))
    ORDER BY tc.address, tc.observed_block_number DESC, tc.code_hash DESC
)
SELECT
chain_id::text AS chain_id,
address AS address,
code_hash AS code_hash,
standard AS standard,
confidence AS confidence,
name AS name,
symbol AS symbol,
decimals AS decimals,
total_supply AS total_supply,
metadata_state AS metadata_state,
observed_block_number::text AS observed_block_number,
observed_block_hash AS observed_block_hash,
updated_at AS updated_at
FROM current_tokens
ORDER BY address
LIMIT sqlc.arg('limit');
