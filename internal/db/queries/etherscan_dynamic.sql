-- name: EtherscanAccountTransactions :many
WITH tip AS (
    SELECT number
    FROM canonical_blocks
    WHERE chain_id = sqlc.arg('chain_id')::text::numeric
    ORDER BY number DESC
    LIMIT 1
)
SELECT
    inclusion.raw AS transaction_raw,
    receipt.raw AS receipt_raw,
    block.timestamp::text AS block_timestamp,
    block.base_fee_per_gas_quantity AS block_base_fee,
    inclusion.block_number::text AS block_number,
    inclusion.block_hash AS block_hash,
    inclusion.tx_index AS transaction_index,
    inclusion.tx_hash AS transaction_hash,
    tip.number::text AS tip_number
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
WHERE inclusion.chain_id = sqlc.arg('chain_id')::text::numeric
  AND (lower(inclusion.raw->>'from') = sqlc.arg('address')::text
       OR lower(inclusion.raw->>'to') = sqlc.arg('address')::text)
  AND inclusion.block_number >= sqlc.arg('from_block')::text::numeric
  AND (sqlc.narg('to_block')::text::numeric IS NULL OR inclusion.block_number <= sqlc.narg('to_block')::text::numeric)
ORDER BY
    CASE WHEN sqlc.arg('direction')::text = 'ASC' THEN inclusion.block_number END ASC,
    CASE WHEN sqlc.arg('direction')::text = 'DESC' THEN inclusion.block_number END DESC,
    CASE WHEN sqlc.arg('direction')::text = 'ASC' THEN inclusion.tx_index END ASC,
    CASE WHEN sqlc.arg('direction')::text = 'DESC' THEN inclusion.tx_index END DESC,
    CASE WHEN sqlc.arg('direction')::text = 'ASC' THEN inclusion.tx_hash END ASC,
    CASE WHEN sqlc.arg('direction')::text = 'DESC' THEN inclusion.tx_hash END DESC
LIMIT sqlc.arg('limit')::bigint OFFSET sqlc.arg('offset')::bigint;

-- name: EtherscanAccountTransactionsAdvanced :many
WITH tip AS (
    SELECT number
    FROM canonical_blocks
    WHERE chain_id = sqlc.arg('chain_id')::text::numeric
    ORDER BY number DESC
    LIMIT 1
)
SELECT
    inclusion.raw AS transaction_raw,
    receipt.raw AS receipt_raw,
    block.timestamp::text AS block_timestamp,
    block.base_fee_per_gas_quantity AS block_base_fee,
    inclusion.block_number::text AS block_number,
    inclusion.block_hash AS block_hash,
    inclusion.tx_index AS transaction_index,
    inclusion.tx_hash AS transaction_hash,
    tip.number::text AS tip_number
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
WHERE inclusion.chain_id = sqlc.arg('chain_id')::text::numeric
  AND (
      (sqlc.arg('operator')::text = 'AND'
       AND (sqlc.narg('from_address')::text IS NULL OR lower(inclusion.raw->>'from') = sqlc.narg('from_address')::text)
       AND (sqlc.narg('to_address')::text IS NULL OR lower(inclusion.raw->>'to') = sqlc.narg('to_address')::text))
      OR
      (sqlc.arg('operator')::text = 'OR'
       AND ((sqlc.narg('from_address')::text IS NOT NULL AND lower(inclusion.raw->>'from') = sqlc.narg('from_address')::text)
            OR (sqlc.narg('to_address')::text IS NOT NULL AND lower(inclusion.raw->>'to') = sqlc.narg('to_address')::text)))
  )
  AND inclusion.block_number >= sqlc.arg('from_block')::text::numeric
  AND (sqlc.narg('to_block')::text::numeric IS NULL OR inclusion.block_number <= sqlc.narg('to_block')::text::numeric)
ORDER BY
    CASE WHEN sqlc.arg('direction')::text = 'ASC' THEN inclusion.block_number END ASC,
    CASE WHEN sqlc.arg('direction')::text = 'DESC' THEN inclusion.block_number END DESC,
    CASE WHEN sqlc.arg('direction')::text = 'ASC' THEN inclusion.tx_index END ASC,
    CASE WHEN sqlc.arg('direction')::text = 'DESC' THEN inclusion.tx_index END DESC,
    CASE WHEN sqlc.arg('direction')::text = 'ASC' THEN inclusion.tx_hash END ASC,
    CASE WHEN sqlc.arg('direction')::text = 'DESC' THEN inclusion.tx_hash END DESC
LIMIT sqlc.arg('limit')::bigint OFFSET sqlc.arg('offset')::bigint;

-- name: EtherscanMinedBlocksAsc :many
SELECT
    block.number::text AS block_number,
    block.hash AS block_hash,
    block.timestamp::text AS block_timestamp,
    block.miner_text AS miner
FROM blocks AS block
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = block.chain_id
 AND canonical.number = block.number
 AND canonical.block_hash = block.hash
WHERE block.chain_id = sqlc.arg('chain_id')::text::numeric
  AND lower(block.miner_text) = sqlc.arg('miner')::text
ORDER BY block.number ASC, block.hash ASC
LIMIT sqlc.arg('limit')::bigint OFFSET sqlc.arg('offset')::bigint;

-- name: EtherscanMinedBlocksDesc :many
SELECT
    block.number::text AS block_number,
    block.hash AS block_hash,
    block.timestamp::text AS block_timestamp,
    block.miner_text AS miner
FROM blocks AS block
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = block.chain_id
 AND canonical.number = block.number
 AND canonical.block_hash = block.hash
WHERE block.chain_id = sqlc.arg('chain_id')::text::numeric
  AND lower(block.miner_text) = sqlc.arg('miner')::text
ORDER BY block.number DESC, block.hash DESC
LIMIT sqlc.arg('limit')::bigint OFFSET sqlc.arg('offset')::bigint;

-- name: EtherscanBlockNumberByTimeBefore :one
SELECT
    block.number::text AS block_number,
    block.hash AS block_hash,
    block.timestamp::text AS block_timestamp
FROM blocks AS block
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = block.chain_id
 AND canonical.number = block.number
 AND canonical.block_hash = block.hash
WHERE block.chain_id = sqlc.arg('chain_id')::text::numeric
  AND block.timestamp <= sqlc.arg('timestamp')::text::numeric
ORDER BY block.timestamp DESC, block.number DESC, block.hash DESC
LIMIT 1;

-- name: EtherscanBlockNumberByTimeAfter :one
SELECT
    block.number::text AS block_number,
    block.hash AS block_hash,
    block.timestamp::text AS block_timestamp
FROM blocks AS block
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = block.chain_id
 AND canonical.number = block.number
 AND canonical.block_hash = block.hash
WHERE block.chain_id = sqlc.arg('chain_id')::text::numeric
  AND block.timestamp >= sqlc.arg('timestamp')::text::numeric
ORDER BY block.timestamp ASC, block.number ASC, block.hash ASC
LIMIT 1;

-- name: EtherscanTokenTransfers :many
SELECT
    event.block_number::text AS block_number,
    event.block_hash AS block_hash,
    event.log_index AS log_index,
    event.sub_index AS sub_index,
    event.transaction_hash AS transaction_hash,
    event.token_address AS token_address,
    event.standard AS standard,
    event.event_kind AS event_kind,
    event.from_address AS from_address,
    event.to_address AS to_address,
    event.token_id AS token_id,
    event.amount AS amount,
    inclusion.raw AS transaction_raw,
    receipt.raw AS receipt_raw,
    block.timestamp::text AS block_timestamp,
    block.base_fee_per_gas_quantity AS block_base_fee,
    inclusion.tx_index AS transaction_index,
    metadata.name AS name,
    metadata.symbol AS symbol,
    metadata.decimals AS decimals
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
WHERE event.chain_id = sqlc.arg('chain_id')::text::numeric
  AND event.canonical = TRUE
  AND (event.from_address = sqlc.arg('address')::bytea OR event.to_address = sqlc.arg('address')::bytea)
  AND event.standard = sqlc.arg('standard')::text
  AND event.event_kind IN ('transfer', 'mint', 'burn')
  AND event.block_number >= sqlc.arg('from_block')::text::numeric
  AND (sqlc.narg('to_block')::text::numeric IS NULL OR event.block_number <= sqlc.narg('to_block')::text::numeric)
  AND (sqlc.arg('contract_address')::bytea IS NULL OR event.token_address = sqlc.arg('contract_address')::bytea)
ORDER BY
    CASE WHEN sqlc.arg('direction')::text = 'ASC' THEN event.block_number END ASC,
    CASE WHEN sqlc.arg('direction')::text = 'DESC' THEN event.block_number END DESC,
    CASE WHEN sqlc.arg('direction')::text = 'ASC' THEN inclusion.tx_index END ASC,
    CASE WHEN sqlc.arg('direction')::text = 'DESC' THEN inclusion.tx_index END DESC,
    CASE WHEN sqlc.arg('direction')::text = 'ASC' THEN event.log_index END ASC,
    CASE WHEN sqlc.arg('direction')::text = 'DESC' THEN event.log_index END DESC,
    CASE WHEN sqlc.arg('direction')::text = 'ASC' THEN event.sub_index END ASC,
    CASE WHEN sqlc.arg('direction')::text = 'DESC' THEN event.sub_index END DESC,
    CASE WHEN sqlc.arg('direction')::text = 'ASC' THEN event.block_hash END ASC,
    CASE WHEN sqlc.arg('direction')::text = 'DESC' THEN event.block_hash END DESC
LIMIT sqlc.arg('limit')::bigint OFFSET sqlc.arg('offset')::bigint;

-- name: EtherscanTokenTransfersAdvanced :many
SELECT
    event.block_number::text AS block_number,
    event.block_hash AS block_hash,
    event.log_index AS log_index,
    event.sub_index AS sub_index,
    event.transaction_hash AS transaction_hash,
    event.token_address AS token_address,
    event.standard AS standard,
    event.event_kind AS event_kind,
    event.from_address AS from_address,
    event.to_address AS to_address,
    event.token_id AS token_id,
    event.amount AS amount,
    inclusion.raw AS transaction_raw,
    receipt.raw AS receipt_raw,
    block.timestamp::text AS block_timestamp,
    block.base_fee_per_gas_quantity AS block_base_fee,
    inclusion.tx_index AS transaction_index,
    metadata.name AS name,
    metadata.symbol AS symbol,
    metadata.decimals AS decimals
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
WHERE event.chain_id = sqlc.arg('chain_id')::text::numeric
  AND event.canonical = TRUE
  AND event.standard = sqlc.arg('standard')::text
  AND event.event_kind IN ('transfer', 'mint', 'burn')
  AND (sqlc.arg('contract_address')::bytea IS NULL OR event.token_address = sqlc.arg('contract_address')::bytea)
  AND (
      (sqlc.arg('operator')::text = 'AND'
       AND (sqlc.arg('from_address')::bytea IS NULL OR event.from_address = sqlc.arg('from_address')::bytea)
       AND (sqlc.arg('to_address')::bytea IS NULL OR event.to_address = sqlc.arg('to_address')::bytea))
      OR
      (sqlc.arg('operator')::text = 'OR'
       AND ((sqlc.arg('from_address')::bytea IS NOT NULL AND event.from_address = sqlc.arg('from_address')::bytea)
            OR (sqlc.arg('to_address')::bytea IS NOT NULL AND event.to_address = sqlc.arg('to_address')::bytea)))
  )
  AND event.block_number >= sqlc.arg('from_block')::text::numeric
  AND (sqlc.narg('to_block')::text::numeric IS NULL OR event.block_number <= sqlc.narg('to_block')::text::numeric)
ORDER BY
    CASE WHEN sqlc.arg('direction')::text = 'ASC' THEN event.block_number END ASC,
    CASE WHEN sqlc.arg('direction')::text = 'DESC' THEN event.block_number END DESC,
    CASE WHEN sqlc.arg('direction')::text = 'ASC' THEN inclusion.tx_index END ASC,
    CASE WHEN sqlc.arg('direction')::text = 'DESC' THEN inclusion.tx_index END DESC,
    CASE WHEN sqlc.arg('direction')::text = 'ASC' THEN event.log_index END ASC,
    CASE WHEN sqlc.arg('direction')::text = 'DESC' THEN event.log_index END DESC,
    CASE WHEN sqlc.arg('direction')::text = 'ASC' THEN event.sub_index END ASC,
    CASE WHEN sqlc.arg('direction')::text = 'DESC' THEN event.sub_index END DESC,
    CASE WHEN sqlc.arg('direction')::text = 'ASC' THEN event.block_hash END ASC,
    CASE WHEN sqlc.arg('direction')::text = 'DESC' THEN event.block_hash END DESC
LIMIT sqlc.arg('limit')::bigint OFFSET sqlc.arg('offset')::bigint;

-- name: EtherscanInternalTransactions :many
SELECT
    trace.block_number::text AS block_number,
    trace.block_hash AS block_hash,
    trace.transaction_hash AS transaction_hash,
    block.timestamp::text AS block_timestamp,
    trace.trace_path AS trace_path,
    trace.depth AS depth,
    trace.call_type AS call_type,
    trace.from_address AS from_address,
    trace.to_address AS to_address,
    trace.created_address AS created_address,
    trace.value AS value,
    trace.gas AS gas,
    trace.gas_used AS gas_used,
    trace.input AS input,
    trace.error AS error,
    trace.reverted AS reverted
FROM normalized_traces AS trace
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = trace.chain_id
 AND canonical.number = trace.block_number
 AND canonical.block_hash = trace.block_hash
JOIN blocks AS block
  ON block.chain_id = trace.chain_id
 AND block.number = trace.block_number
 AND block.hash = trace.block_hash
WHERE trace.chain_id = sqlc.arg('chain_id')::text::numeric
  AND trace.canonical = TRUE
  AND trace.depth > 0
  AND (sqlc.arg('address')::bytea IS NULL OR trace.from_address = sqlc.arg('address')::bytea
       OR trace.to_address = sqlc.arg('address')::bytea OR trace.created_address = sqlc.arg('address')::bytea)
  AND (sqlc.arg('transaction_hash')::bytea IS NULL OR trace.transaction_hash = sqlc.arg('transaction_hash')::bytea)
  AND trace.block_number >= sqlc.arg('from_block')::text::numeric
  AND (sqlc.narg('to_block')::text::numeric IS NULL OR trace.block_number <= sqlc.narg('to_block')::text::numeric)
ORDER BY
    CASE WHEN sqlc.arg('direction')::text = 'ASC' THEN trace.block_number END ASC,
    CASE WHEN sqlc.arg('direction')::text = 'DESC' THEN trace.block_number END DESC,
    CASE WHEN sqlc.arg('direction')::text = 'ASC' THEN trace.transaction_index END ASC,
    CASE WHEN sqlc.arg('direction')::text = 'DESC' THEN trace.transaction_index END DESC,
    CASE WHEN sqlc.arg('direction')::text = 'ASC'
         THEN string_to_array(trace.trace_path, '.')::bigint[] END ASC,
    CASE WHEN sqlc.arg('direction')::text = 'DESC'
         THEN string_to_array(trace.trace_path, '.')::bigint[] END DESC,
    CASE WHEN sqlc.arg('direction')::text = 'ASC' THEN trace.block_hash END ASC,
    CASE WHEN sqlc.arg('direction')::text = 'DESC' THEN trace.block_hash END DESC,
    CASE WHEN sqlc.arg('direction')::text = 'ASC' THEN trace.transaction_hash END ASC,
    CASE WHEN sqlc.arg('direction')::text = 'DESC' THEN trace.transaction_hash END DESC
LIMIT sqlc.arg('limit')::bigint OFFSET sqlc.arg('offset')::bigint;

-- name: EtherscanInternalTransactionsAdvanced :many
SELECT
    trace.block_number::text AS block_number,
    trace.block_hash AS block_hash,
    trace.transaction_hash AS transaction_hash,
    block.timestamp::text AS block_timestamp,
    trace.trace_path AS trace_path,
    trace.depth AS depth,
    trace.call_type AS call_type,
    trace.from_address AS from_address,
    trace.to_address AS to_address,
    trace.created_address AS created_address,
    trace.value AS value,
    trace.gas AS gas,
    trace.gas_used AS gas_used,
    trace.input AS input,
    trace.error AS error,
    trace.reverted AS reverted
FROM normalized_traces AS trace
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = trace.chain_id
 AND canonical.number = trace.block_number
 AND canonical.block_hash = trace.block_hash
JOIN blocks AS block
  ON block.chain_id = trace.chain_id
 AND block.number = trace.block_number
 AND block.hash = trace.block_hash
WHERE trace.chain_id = sqlc.arg('chain_id')::text::numeric
  AND trace.canonical = TRUE
  AND trace.depth > 0
  AND (
      (sqlc.arg('operator')::text = 'AND'
       AND (sqlc.arg('from_address')::bytea IS NULL OR trace.from_address = sqlc.arg('from_address')::bytea)
       AND (sqlc.arg('to_address')::bytea IS NULL OR trace.to_address = sqlc.arg('to_address')::bytea OR trace.created_address = sqlc.arg('to_address')::bytea))
      OR
      (sqlc.arg('operator')::text = 'OR'
       AND ((sqlc.arg('from_address')::bytea IS NOT NULL AND trace.from_address = sqlc.arg('from_address')::bytea)
            OR (sqlc.arg('to_address')::bytea IS NOT NULL AND (trace.to_address = sqlc.arg('to_address')::bytea OR trace.created_address = sqlc.arg('to_address')::bytea))))
  )
  AND trace.block_number >= sqlc.arg('from_block')::text::numeric
  AND (sqlc.narg('to_block')::text::numeric IS NULL OR trace.block_number <= sqlc.narg('to_block')::text::numeric)
ORDER BY
    CASE WHEN sqlc.arg('direction')::text = 'ASC' THEN trace.block_number END ASC,
    CASE WHEN sqlc.arg('direction')::text = 'DESC' THEN trace.block_number END DESC,
    CASE WHEN sqlc.arg('direction')::text = 'ASC' THEN trace.transaction_index END ASC,
    CASE WHEN sqlc.arg('direction')::text = 'DESC' THEN trace.transaction_index END DESC,
    CASE WHEN sqlc.arg('direction')::text = 'ASC'
         THEN string_to_array(trace.trace_path, '.')::bigint[] END ASC,
    CASE WHEN sqlc.arg('direction')::text = 'DESC'
         THEN string_to_array(trace.trace_path, '.')::bigint[] END DESC,
    CASE WHEN sqlc.arg('direction')::text = 'ASC' THEN trace.block_hash END ASC,
    CASE WHEN sqlc.arg('direction')::text = 'DESC' THEN trace.block_hash END DESC,
    CASE WHEN sqlc.arg('direction')::text = 'ASC' THEN trace.transaction_hash END ASC,
    CASE WHEN sqlc.arg('direction')::text = 'DESC' THEN trace.transaction_hash END DESC
LIMIT sqlc.arg('limit')::bigint OFFSET sqlc.arg('offset')::bigint;

-- name: EtherscanBeaconWithdrawals :many
SELECT withdrawal.withdrawal_index::text, withdrawal.validator_index::text,
       withdrawal.address, withdrawal.amount::text,
       withdrawal.block_number::text, block.timestamp::text
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
  AND (sqlc.arg('address')::bytea IS NULL OR withdrawal.address = sqlc.arg('address')::bytea)
  AND withdrawal.block_number >= sqlc.arg('min_block_number')::numeric
  AND (sqlc.arg('max_block_number')::numeric IS NULL OR withdrawal.block_number <= sqlc.arg('max_block_number')::numeric)
ORDER BY
    CASE WHEN sqlc.arg('sort_order')::text = 'ASC' THEN withdrawal.block_number END ASC,
    CASE WHEN sqlc.arg('sort_order')::text = 'DESC' THEN withdrawal.block_number END DESC,
    CASE WHEN sqlc.arg('sort_order')::text = 'ASC' THEN withdrawal.withdrawal_index END ASC,
    CASE WHEN sqlc.arg('sort_order')::text = 'DESC' THEN withdrawal.withdrawal_index END DESC,
    CASE WHEN sqlc.arg('sort_order')::text = 'ASC' THEN withdrawal.block_hash END ASC,
    CASE WHEN sqlc.arg('sort_order')::text = 'DESC' THEN withdrawal.block_hash END DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: EtherscanBlockTransactionCounts :one
SELECT canonical.number::text,
       (SELECT count(*)::text
          FROM transaction_inclusions AS inclusion
         WHERE inclusion.chain_id = canonical.chain_id
           AND inclusion.block_number = canonical.number
           AND inclusion.block_hash = canonical.block_hash) AS transaction_count,
       (SELECT count(*)::text
          FROM normalized_traces AS trace
         WHERE trace.chain_id = canonical.chain_id
           AND trace.block_number = canonical.number
           AND trace.block_hash = canonical.block_hash
           AND trace.canonical = TRUE
           AND trace.depth > 0) AS internal_count,
       (SELECT count(*) FILTER (WHERE event.standard = 'erc20')::text
          FROM token_events AS event
         WHERE event.chain_id = canonical.chain_id
           AND event.block_number = canonical.number
           AND event.block_hash = canonical.block_hash
           AND event.canonical = TRUE
           AND event.event_kind IN ('transfer', 'mint', 'burn')) AS erc20_count,
       (SELECT count(*) FILTER (WHERE event.standard = 'erc721')::text
          FROM token_events AS event
         WHERE event.chain_id = canonical.chain_id
           AND event.block_number = canonical.number
           AND event.block_hash = canonical.block_hash
           AND event.canonical = TRUE
           AND event.event_kind IN ('transfer', 'mint', 'burn')) AS erc721_count,
       (SELECT count(*) FILTER (WHERE event.standard = 'erc1155')::text
          FROM token_events AS event
         WHERE event.chain_id = canonical.chain_id
           AND event.block_number = canonical.number
           AND event.block_hash = canonical.block_hash
           AND event.canonical = TRUE
           AND event.event_kind IN ('transfer', 'mint', 'burn')) AS erc1155_count
FROM canonical_blocks AS canonical
WHERE canonical.chain_id = sqlc.arg('chain_id')::numeric
  AND canonical.number = sqlc.arg('number')::numeric;

-- name: EtherscanFirstFunding :one
WITH candidates AS (
    SELECT inclusion.block_number, inclusion.tx_index,
           ARRAY[]::bigint[] AS trace_order, 0 AS source_rank,
           decode(substr(inclusion.raw->>'from', 3), 'hex') AS source_address,
           inclusion.tx_hash AS transaction_hash,
           inclusion.raw->>'value' AS value_hex,
           NULL::text AS value_decimal,
           block.timestamp::text AS block_timestamp
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
     AND receipt.tx_hash = inclusion.tx_hash
    JOIN normalized_traces AS root_trace
      ON root_trace.chain_id = inclusion.chain_id
     AND root_trace.block_number = inclusion.block_number
     AND root_trace.block_hash = inclusion.block_hash
     AND root_trace.transaction_hash = inclusion.tx_hash
     AND root_trace.trace_path = ''
     AND root_trace.depth = 0
    JOIN blocks AS block
      ON block.chain_id = inclusion.chain_id
     AND block.number = inclusion.block_number
     AND block.hash = inclusion.block_hash
    WHERE inclusion.chain_id = sqlc.arg('chain_id')::numeric
      AND inclusion.block_number <= sqlc.arg('max_block_number')::numeric
      AND lower(inclusion.raw->>'to') = lower('0x' || encode(sqlc.arg('encode'), 'hex'))
      AND lower(inclusion.raw->>'from') <> lower('0x' || encode(sqlc.arg('encode'), 'hex'))
      AND inclusion.raw->>'value' <> '0x0'
      AND root_trace.canonical = TRUE
      AND root_trace.reverted = FALSE

    UNION ALL

    SELECT trace.block_number, trace.transaction_index,
           string_to_array(trace.trace_path, '.')::bigint[] AS trace_order,
           1 AS source_rank, trace.from_address, trace.transaction_hash,
           NULL::text AS value_hex, trace.value::text AS value_decimal,
           block.timestamp::text AS block_timestamp
    FROM normalized_traces AS trace
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = trace.chain_id
     AND canonical.number = trace.block_number
     AND canonical.block_hash = trace.block_hash
    JOIN blocks AS block
      ON block.chain_id = trace.chain_id
     AND block.number = trace.block_number
     AND block.hash = trace.block_hash
    WHERE trace.chain_id = sqlc.arg('chain_id')::numeric
      AND trace.block_number <= sqlc.arg('max_block_number')::numeric
      AND trace.to_address = sqlc.arg('encode')
      AND trace.from_address IS NOT NULL
      AND trace.from_address <> sqlc.arg('encode')
      AND trace.value > 0
      AND trace.canonical = TRUE
      AND trace.reverted = FALSE
      AND trace.depth > 0
)
SELECT
block_number::text,
source_address,
transaction_hash,
COALESCE((value_hex),'')::text AS value_hex,
COALESCE((value_decimal),'')::text AS value_decimal,
block_timestamp,
(value_hex IS NOT NULL)::boolean AS value_hex_present,
(value_decimal IS NOT NULL)::boolean AS value_decimal_present
FROM candidates
ORDER BY block_number, tx_index, source_rank, trace_order
LIMIT 1;

-- name: EtherscanERC20HoldingCandidates :many
WITH candidates AS (
    SELECT delta.token_address
    FROM token_balance_deltas AS delta
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = delta.chain_id
     AND canonical.number = delta.block_number
     AND canonical.block_hash = delta.block_hash
    WHERE delta.chain_id = sqlc.arg('chain_id')::numeric
      AND delta.block_number <= sqlc.arg('max_block_number')::numeric
      AND delta.owner_address = sqlc.arg('owner_address')
      AND delta.token_id IS NULL
      AND delta.canonical = TRUE
    GROUP BY delta.token_address
)
SELECT candidates.token_address, metadata.name, metadata.symbol,
       metadata.decimals
FROM candidates
JOIN LATERAL (
    SELECT token.standard, token.name, token.symbol, token.decimals
    FROM token_contracts AS token
    JOIN canonical_blocks AS observed
      ON observed.chain_id = token.chain_id
     AND observed.number = token.observed_block_number
     AND observed.block_hash = token.observed_block_hash
    WHERE token.chain_id = sqlc.arg('chain_id')::numeric
      AND token.address = candidates.token_address
      AND token.observed_block_number <= sqlc.arg('max_block_number')::numeric
    ORDER BY token.observed_block_number DESC, token.updated_at DESC,
             token.code_hash DESC
    LIMIT 1
) AS metadata ON TRUE
WHERE metadata.standard = 'erc20'
ORDER BY candidates.token_address
LIMIT sqlc.arg('limit');

-- name: EtherscanERC721HoldingCandidates :many
WITH candidates AS (
    SELECT delta.token_address, delta.token_id
    FROM token_balance_deltas AS delta
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = delta.chain_id
     AND canonical.number = delta.block_number
     AND canonical.block_hash = delta.block_hash
    WHERE delta.chain_id = sqlc.arg('chain_id')::numeric
      AND delta.block_number <= sqlc.arg('max_block_number')::numeric
      AND delta.owner_address = sqlc.arg('owner_address')
      AND delta.token_id IS NOT NULL
      AND delta.canonical = TRUE
      AND (sqlc.arg('token_address')::bytea IS NULL OR delta.token_address = sqlc.arg('token_address')::bytea)
    GROUP BY delta.token_address, delta.token_id
)
SELECT candidates.token_address, candidates.token_id::text,
       metadata.name, metadata.symbol
FROM candidates
JOIN LATERAL (
    SELECT token.standard, token.name, token.symbol
    FROM token_contracts AS token
    JOIN canonical_blocks AS observed
      ON observed.chain_id = token.chain_id
     AND observed.number = token.observed_block_number
     AND observed.block_hash = token.observed_block_hash
    WHERE token.chain_id = sqlc.arg('chain_id')::numeric
      AND token.address = candidates.token_address
      AND token.observed_block_number <= sqlc.arg('max_block_number')::numeric
    ORDER BY token.observed_block_number DESC, token.updated_at DESC,
             token.code_hash DESC
    LIMIT 1
) AS metadata ON TRUE
WHERE metadata.standard = 'erc721'
ORDER BY candidates.token_address, candidates.token_id
LIMIT sqlc.arg('limit');

-- name: EtherscanLogsAsc :many
WITH candidate_logs AS (
    SELECT chain_id, block_number, block_hash, log_index, tx_index,
           tx_hash, address, topic0, raw
    FROM logs
    WHERE sqlc.arg('indexed_topic_zero')::boolean
      AND chain_id = sqlc.arg('chain_id')::text::numeric
      AND block_number >= sqlc.arg('from_block')::text::numeric
      AND (sqlc.narg('to_block')::text::numeric IS NULL OR block_number <= sqlc.narg('to_block')::text::numeric)
      AND topic0 = sqlc.arg('topic_zero')::bytea
    UNION ALL
    SELECT chain_id, block_number, block_hash, log_index, tx_index,
           tx_hash, address, topic0, raw
    FROM logs
    WHERE NOT sqlc.arg('indexed_topic_zero')::boolean
      AND chain_id = sqlc.arg('chain_id')::text::numeric
      AND block_number >= sqlc.arg('from_block')::text::numeric
      AND (sqlc.narg('to_block')::text::numeric IS NULL OR block_number <= sqlc.narg('to_block')::text::numeric)
)
SELECT
    log.raw AS log_raw,
    receipt.raw AS receipt_raw,
    inclusion.raw AS transaction_raw,
    block.timestamp::text AS block_timestamp,
    block.base_fee_per_gas_quantity AS block_base_fee,
    log.block_number::text AS block_number,
    log.block_hash AS block_hash,
    log.log_index AS log_index,
    log.tx_index AS transaction_index,
    log.tx_hash AS transaction_hash,
    log.address AS address
FROM candidate_logs AS log
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
    SELECT jsonb_array_length(sqlc.arg('topics')::jsonb) AS topic_count,
           COALESCE(
               lower(log.raw->'topics'->>((sqlc.arg('topics')::jsonb->0->>'index')::integer)) =
                   lower(sqlc.arg('topics')::jsonb->0->>'value'),
               FALSE
           ) AS match_1,
           COALESCE(
               lower(log.raw->'topics'->>((sqlc.arg('topics')::jsonb->1->>'index')::integer)) =
                   lower(sqlc.arg('topics')::jsonb->1->>'value'),
               FALSE
           ) AS match_2,
           COALESCE(
               lower(log.raw->'topics'->>((sqlc.arg('topics')::jsonb->2->>'index')::integer)) =
                   lower(sqlc.arg('topics')::jsonb->2->>'value'),
               FALSE
           ) AS match_3,
           COALESCE(
               lower(log.raw->'topics'->>((sqlc.arg('topics')::jsonb->3->>'index')::integer)) =
                   lower(sqlc.arg('topics')::jsonb->3->>'value'),
               FALSE
           ) AS match_4
) AS requested
CROSS JOIN LATERAL (
    SELECT CASE upper(sqlc.arg('topics')::jsonb->1->>'operator')
               WHEN 'OR' THEN requested.match_1 OR requested.match_2
               ELSE requested.match_1 AND requested.match_2
           END AS matched
) AS folded_2
CROSS JOIN LATERAL (
    SELECT CASE upper(sqlc.arg('topics')::jsonb->2->>'operator')
               WHEN 'OR' THEN folded_2.matched OR requested.match_3
               ELSE folded_2.matched AND requested.match_3
           END AS matched
) AS folded_3
CROSS JOIN LATERAL (
    SELECT CASE upper(sqlc.arg('topics')::jsonb->3->>'operator')
               WHEN 'OR' THEN folded_3.matched OR requested.match_4
               ELSE folded_3.matched AND requested.match_4
           END AS matched
) AS folded_4
WHERE (sqlc.arg('address')::bytea IS NULL OR log.address = sqlc.arg('address')::bytea)
  AND CASE requested.topic_count
          WHEN 0 THEN TRUE
          WHEN 1 THEN requested.match_1
          WHEN 2 THEN folded_2.matched
          WHEN 3 THEN folded_3.matched
          WHEN 4 THEN folded_4.matched
          ELSE FALSE
      END
ORDER BY log.block_number ASC, log.log_index ASC, log.block_hash ASC
LIMIT sqlc.arg('limit')::bigint OFFSET sqlc.arg('offset')::bigint;

-- name: EtherscanLogsDesc :many
WITH candidate_logs AS (
    SELECT chain_id, block_number, block_hash, log_index, tx_index,
           tx_hash, address, topic0, raw
    FROM logs
    WHERE sqlc.arg('indexed_topic_zero')::boolean
      AND chain_id = sqlc.arg('chain_id')::text::numeric
      AND block_number >= sqlc.arg('from_block')::text::numeric
      AND (sqlc.narg('to_block')::text::numeric IS NULL OR block_number <= sqlc.narg('to_block')::text::numeric)
      AND topic0 = sqlc.arg('topic_zero')::bytea
    UNION ALL
    SELECT chain_id, block_number, block_hash, log_index, tx_index,
           tx_hash, address, topic0, raw
    FROM logs
    WHERE NOT sqlc.arg('indexed_topic_zero')::boolean
      AND chain_id = sqlc.arg('chain_id')::text::numeric
      AND block_number >= sqlc.arg('from_block')::text::numeric
      AND (sqlc.narg('to_block')::text::numeric IS NULL OR block_number <= sqlc.narg('to_block')::text::numeric)
)
SELECT
    log.raw AS log_raw,
    receipt.raw AS receipt_raw,
    inclusion.raw AS transaction_raw,
    block.timestamp::text AS block_timestamp,
    block.base_fee_per_gas_quantity AS block_base_fee,
    log.block_number::text AS block_number,
    log.block_hash AS block_hash,
    log.log_index AS log_index,
    log.tx_index AS transaction_index,
    log.tx_hash AS transaction_hash,
    log.address AS address
FROM candidate_logs AS log
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
    SELECT jsonb_array_length(sqlc.arg('topics')::jsonb) AS topic_count,
           COALESCE(
               lower(log.raw->'topics'->>((sqlc.arg('topics')::jsonb->0->>'index')::integer)) =
                   lower(sqlc.arg('topics')::jsonb->0->>'value'),
               FALSE
           ) AS match_1,
           COALESCE(
               lower(log.raw->'topics'->>((sqlc.arg('topics')::jsonb->1->>'index')::integer)) =
                   lower(sqlc.arg('topics')::jsonb->1->>'value'),
               FALSE
           ) AS match_2,
           COALESCE(
               lower(log.raw->'topics'->>((sqlc.arg('topics')::jsonb->2->>'index')::integer)) =
                   lower(sqlc.arg('topics')::jsonb->2->>'value'),
               FALSE
           ) AS match_3,
           COALESCE(
               lower(log.raw->'topics'->>((sqlc.arg('topics')::jsonb->3->>'index')::integer)) =
                   lower(sqlc.arg('topics')::jsonb->3->>'value'),
               FALSE
           ) AS match_4
) AS requested
CROSS JOIN LATERAL (
    SELECT CASE upper(sqlc.arg('topics')::jsonb->1->>'operator')
               WHEN 'OR' THEN requested.match_1 OR requested.match_2
               ELSE requested.match_1 AND requested.match_2
           END AS matched
) AS folded_2
CROSS JOIN LATERAL (
    SELECT CASE upper(sqlc.arg('topics')::jsonb->2->>'operator')
               WHEN 'OR' THEN folded_2.matched OR requested.match_3
               ELSE folded_2.matched AND requested.match_3
           END AS matched
) AS folded_3
CROSS JOIN LATERAL (
    SELECT CASE upper(sqlc.arg('topics')::jsonb->3->>'operator')
               WHEN 'OR' THEN folded_3.matched OR requested.match_4
               ELSE folded_3.matched AND requested.match_4
           END AS matched
) AS folded_4
WHERE (sqlc.arg('address')::bytea IS NULL OR log.address = sqlc.arg('address')::bytea)
  AND CASE requested.topic_count
          WHEN 0 THEN TRUE
          WHEN 1 THEN requested.match_1
          WHEN 2 THEN folded_2.matched
          WHEN 3 THEN folded_3.matched
          WHEN 4 THEN folded_4.matched
          ELSE FALSE
      END
ORDER BY log.block_number DESC, log.log_index DESC, log.block_hash DESC
LIMIT sqlc.arg('limit')::bigint OFFSET sqlc.arg('offset')::bigint;
