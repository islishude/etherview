-- name: CatalogTokenContract :many
SELECT
tc.chain_id::text, tc.address, tc.code_hash, tc.standard, tc.confidence,
tc.name, tc.symbol, tc.decimals, tc.total_supply::text, tc.metadata_state,
tc.observed_block_number::text, tc.observed_block_hash, tc.updated_at
FROM token_contracts AS tc
JOIN canonical_blocks AS cb
  ON cb.chain_id = tc.chain_id
 AND cb.number = tc.observed_block_number
 AND cb.block_hash = tc.observed_block_hash
WHERE tc.chain_id = $1::numeric
  AND tc.address = $2
  AND tc.observed_block_number <= $3::numeric
ORDER BY tc.observed_block_number DESC, tc.code_hash DESC
LIMIT 1;

-- name: CatalogTokenContracts :many
WITH current_tokens AS (
    SELECT DISTINCT ON (tc.address)
tc.chain_id::text, tc.address, tc.code_hash, tc.standard, tc.confidence,
tc.name, tc.symbol, tc.decimals, tc.total_supply::text, tc.metadata_state,
tc.observed_block_number::text, tc.observed_block_hash, tc.updated_at
    FROM token_contracts AS tc
    JOIN canonical_blocks AS cb
      ON cb.chain_id = tc.chain_id
     AND cb.number = tc.observed_block_number
     AND cb.block_hash = tc.observed_block_hash
    WHERE tc.chain_id = $1::numeric
      AND tc.observed_block_number <= $2::numeric
      AND ($3::boolean = false OR tc.address > $4)
    ORDER BY tc.address, tc.observed_block_number DESC, tc.code_hash DESC
)
SELECT chain_id::text, address, code_hash, standard, confidence,
       name, symbol, decimals, total_supply::text, metadata_state,
       observed_block_number::text, observed_block_hash, updated_at
FROM current_tokens
ORDER BY address
LIMIT $5;
