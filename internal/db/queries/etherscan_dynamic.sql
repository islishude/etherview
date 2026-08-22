-- name: EtherscanAccountTransactions :many
WITH tip AS (
    SELECT number
    FROM canonical_blocks
    WHERE chain_id = $1::numeric
    ORDER BY number DESC
    LIMIT 1
)
SELECT inclusion.raw, receipt.raw, block.raw, inclusion.block_number::text,
       inclusion.block_hash, inclusion.tx_index, inclusion.tx_hash,
       tip.number::text
FROM transaction_inclusions AS inclusion
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = inclusion.chain_id
 AND canonical.number = inclusion.block_number
 AND canonical.block_hash = inclusion.block_hash
JOIN receipts AS receipt
  ON receipt.chain_id = inclusion.chain_id
 AND receipt.block_number = inclusion.block_number
 AND receipt.block_hash = inclusion.block_hash
 AND receipt.tx_index = inclusion.tx_index
JOIN blocks AS block
  ON block.chain_id = inclusion.chain_id
 AND block.number = inclusion.block_number
 AND block.hash = inclusion.block_hash
CROSS JOIN tip
WHERE inclusion.chain_id = $1::numeric
  AND (lower(inclusion.raw->>'from') = $2::text
       OR lower(inclusion.raw->>'to') = $2::text)
  AND inclusion.block_number >= $3::numeric
  AND ($4::numeric IS NULL OR inclusion.block_number <= $4::numeric)
ORDER BY
    CASE WHEN $7::text = 'ASC' THEN inclusion.block_number END ASC,
    CASE WHEN $7::text = 'DESC' THEN inclusion.block_number END DESC,
    CASE WHEN $7::text = 'ASC' THEN inclusion.tx_index END ASC,
    CASE WHEN $7::text = 'DESC' THEN inclusion.tx_index END DESC,
    CASE WHEN $7::text = 'ASC' THEN inclusion.tx_hash END ASC,
    CASE WHEN $7::text = 'DESC' THEN inclusion.tx_hash END DESC
LIMIT $5 OFFSET $6;

-- name: EtherscanMinedBlocks :many
SELECT block.raw, block.number::text, block.hash
FROM blocks AS block
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = block.chain_id
 AND canonical.number = block.number
 AND canonical.block_hash = block.hash
WHERE block.chain_id = $1::numeric
  AND lower(block.raw->>'miner') = $2::text
ORDER BY
    CASE WHEN $5::text = 'ASC' THEN block.number END ASC,
    CASE WHEN $5::text = 'DESC' THEN block.number END DESC,
    CASE WHEN $5::text = 'ASC' THEN block.hash END ASC,
    CASE WHEN $5::text = 'DESC' THEN block.hash END DESC
LIMIT $3 OFFSET $4;

-- name: EtherscanBlockNumberByTime :many
SELECT block.raw, block.number::text, block.hash, block.timestamp::text
FROM blocks AS block
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = block.chain_id
 AND canonical.number = block.number
 AND canonical.block_hash = block.hash
WHERE block.chain_id = $1::numeric
  AND (($3::text = 'before' AND block.timestamp <= $2::numeric)
       OR ($3::text = 'after' AND block.timestamp >= $2::numeric))
ORDER BY
    CASE WHEN $3::text = 'before' THEN block.timestamp END DESC,
    CASE WHEN $3::text = 'after' THEN block.timestamp END ASC,
    CASE WHEN $3::text = 'before' THEN block.number END DESC,
    CASE WHEN $3::text = 'after' THEN block.number END ASC,
    CASE WHEN $3::text = 'before' THEN block.hash END DESC,
    CASE WHEN $3::text = 'after' THEN block.hash END ASC
LIMIT 1;

-- name: EtherscanTokenTransfers :many
SELECT event.block_number::text, event.block_hash, event.log_index,
       event.sub_index, event.transaction_hash, event.token_address,
       event.standard, event.event_kind, event.from_address, event.to_address,
       event.token_id::text, event.amount::text, inclusion.raw, receipt.raw,
       block.raw, inclusion.tx_index, metadata.name, metadata.symbol,
       metadata.decimals
FROM token_events AS event
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = event.chain_id
 AND canonical.number = event.block_number
 AND canonical.block_hash = event.block_hash
JOIN transaction_inclusions AS inclusion
  ON inclusion.chain_id = event.chain_id
 AND inclusion.block_number = event.block_number
 AND inclusion.block_hash = event.block_hash
 AND inclusion.tx_hash = event.transaction_hash
JOIN receipts AS receipt
  ON receipt.chain_id = inclusion.chain_id
 AND receipt.block_number = inclusion.block_number
 AND receipt.block_hash = inclusion.block_hash
 AND receipt.tx_index = inclusion.tx_index
JOIN blocks AS block
  ON block.chain_id = event.chain_id
 AND block.number = event.block_number
 AND block.hash = event.block_hash
LEFT JOIN LATERAL (
    SELECT token.name, token.symbol, token.decimals
    FROM token_contracts AS token
    JOIN canonical_blocks AS observed
      ON observed.chain_id = token.chain_id
     AND observed.number = token.observed_block_number
     AND observed.block_hash = token.observed_block_hash
    WHERE token.chain_id = event.chain_id
      AND token.address = event.token_address
      AND token.observed_block_number <= event.block_number
    ORDER BY token.observed_block_number DESC, token.updated_at DESC,
             token.code_hash DESC
    LIMIT 1
) AS metadata ON TRUE
WHERE event.chain_id = $1::numeric
  AND event.canonical = TRUE
  AND (event.from_address = $2::bytea OR event.to_address = $2::bytea)
  AND event.standard = $3::text
  AND event.event_kind IN ('transfer', 'mint', 'burn')
  AND event.block_number >= $4::numeric
  AND ($5::numeric IS NULL OR event.block_number <= $5::numeric)
  AND ($6::bytea IS NULL OR event.token_address = $6::bytea)
ORDER BY
    CASE WHEN $9::text = 'ASC' THEN event.block_number END ASC,
    CASE WHEN $9::text = 'DESC' THEN event.block_number END DESC,
    CASE WHEN $9::text = 'ASC' THEN inclusion.tx_index END ASC,
    CASE WHEN $9::text = 'DESC' THEN inclusion.tx_index END DESC,
    CASE WHEN $9::text = 'ASC' THEN event.log_index END ASC,
    CASE WHEN $9::text = 'DESC' THEN event.log_index END DESC,
    CASE WHEN $9::text = 'ASC' THEN event.sub_index END ASC,
    CASE WHEN $9::text = 'DESC' THEN event.sub_index END DESC,
    CASE WHEN $9::text = 'ASC' THEN event.block_hash END ASC,
    CASE WHEN $9::text = 'DESC' THEN event.block_hash END DESC
LIMIT $7 OFFSET $8;

-- name: EtherscanInternalTransactions :many
SELECT trace.block_number::text, trace.block_hash,
       trace.transaction_hash, block.timestamp::text, trace.trace_path,
       trace.depth, trace.call_type, trace.from_address, trace.to_address,
       trace.created_address, trace.value::text, trace.gas::text,
       trace.gas_used::text, trace.input, trace.error, trace.reverted
FROM normalized_traces AS trace
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = trace.chain_id
 AND canonical.number = trace.block_number
 AND canonical.block_hash = trace.block_hash
JOIN blocks AS block
  ON block.chain_id = trace.chain_id
 AND block.number = trace.block_number
 AND block.hash = trace.block_hash
WHERE trace.chain_id = $1::numeric
  AND trace.canonical = TRUE
  AND trace.depth > 0
  AND ($2::bytea IS NULL OR trace.from_address = $2::bytea
       OR trace.to_address = $2::bytea OR trace.created_address = $2::bytea)
  AND ($3::bytea IS NULL OR trace.transaction_hash = $3::bytea)
  AND trace.block_number >= $4::numeric
  AND ($5::numeric IS NULL OR trace.block_number <= $5::numeric)
ORDER BY
    CASE WHEN $8::text = 'ASC' THEN trace.block_number END ASC,
    CASE WHEN $8::text = 'DESC' THEN trace.block_number END DESC,
    CASE WHEN $8::text = 'ASC' THEN trace.transaction_index END ASC,
    CASE WHEN $8::text = 'DESC' THEN trace.transaction_index END DESC,
    CASE WHEN $8::text = 'ASC'
         THEN string_to_array(trace.trace_path, '.')::bigint[] END ASC,
    CASE WHEN $8::text = 'DESC'
         THEN string_to_array(trace.trace_path, '.')::bigint[] END DESC,
    CASE WHEN $8::text = 'ASC' THEN trace.block_hash END ASC,
    CASE WHEN $8::text = 'DESC' THEN trace.block_hash END DESC,
    CASE WHEN $8::text = 'ASC' THEN trace.transaction_hash END ASC,
    CASE WHEN $8::text = 'DESC' THEN trace.transaction_hash END DESC
LIMIT $6 OFFSET $7;

-- name: EtherscanLogs :many
SELECT log.raw, receipt.raw, inclusion.raw, block.raw,
       log.block_number::text, log.block_hash, log.log_index, log.tx_index,
       log.tx_hash, log.address
FROM logs AS log
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = log.chain_id
 AND canonical.number = log.block_number
 AND canonical.block_hash = log.block_hash
JOIN receipts AS receipt
  ON receipt.chain_id = log.chain_id
 AND receipt.block_number = log.block_number
 AND receipt.block_hash = log.block_hash
 AND receipt.tx_index = log.tx_index
JOIN transaction_inclusions AS inclusion
  ON inclusion.chain_id = log.chain_id
 AND inclusion.block_number = log.block_number
 AND inclusion.block_hash = log.block_hash
 AND inclusion.tx_index = log.tx_index
JOIN blocks AS block
  ON block.chain_id = log.chain_id
 AND block.number = log.block_number
 AND block.hash = log.block_hash
CROSS JOIN LATERAL (
    SELECT jsonb_array_length($5::jsonb) AS topic_count,
           COALESCE(
               lower(log.raw->'topics'->>(($5::jsonb->0->>'index')::integer)) =
                   lower($5::jsonb->0->>'value'),
               FALSE
           ) AS match_1,
           COALESCE(
               lower(log.raw->'topics'->>(($5::jsonb->1->>'index')::integer)) =
                   lower($5::jsonb->1->>'value'),
               FALSE
           ) AS match_2,
           COALESCE(
               lower(log.raw->'topics'->>(($5::jsonb->2->>'index')::integer)) =
                   lower($5::jsonb->2->>'value'),
               FALSE
           ) AS match_3,
           COALESCE(
               lower(log.raw->'topics'->>(($5::jsonb->3->>'index')::integer)) =
                   lower($5::jsonb->3->>'value'),
               FALSE
           ) AS match_4
) AS requested
CROSS JOIN LATERAL (
    SELECT CASE upper($5::jsonb->1->>'operator')
               WHEN 'OR' THEN requested.match_1 OR requested.match_2
               ELSE requested.match_1 AND requested.match_2
           END AS matched
) AS folded_2
CROSS JOIN LATERAL (
    SELECT CASE upper($5::jsonb->2->>'operator')
               WHEN 'OR' THEN folded_2.matched OR requested.match_3
               ELSE folded_2.matched AND requested.match_3
           END AS matched
) AS folded_3
CROSS JOIN LATERAL (
    SELECT CASE upper($5::jsonb->3->>'operator')
               WHEN 'OR' THEN folded_3.matched OR requested.match_4
               ELSE folded_3.matched AND requested.match_4
           END AS matched
) AS folded_4
WHERE log.chain_id = $1::numeric
  AND log.block_number >= $2::numeric
  AND ($3::numeric IS NULL OR log.block_number <= $3::numeric)
  AND ($4::bytea IS NULL OR log.address = $4::bytea)
  AND CASE requested.topic_count
          WHEN 0 THEN TRUE
          WHEN 1 THEN requested.match_1
          WHEN 2 THEN folded_2.matched
          WHEN 3 THEN folded_3.matched
          WHEN 4 THEN folded_4.matched
          ELSE FALSE
      END
ORDER BY
    CASE WHEN $8::text = 'ASC' THEN log.block_number END ASC,
    CASE WHEN $8::text = 'DESC' THEN log.block_number END DESC,
    CASE WHEN $8::text = 'ASC' THEN log.log_index END ASC,
    CASE WHEN $8::text = 'DESC' THEN log.log_index END DESC,
    CASE WHEN $8::text = 'ASC' THEN log.block_hash END ASC,
    CASE WHEN $8::text = 'DESC' THEN log.block_hash END DESC
LIMIT $6 OFFSET $7;
