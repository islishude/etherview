-- name: QueryListAddressTransactions :many
WITH candidates AS (
    SELECT candidate.block_number, candidate.block_hash, candidate.tx_index, candidate.tx_hash
    FROM (
        SELECT block_number, block_hash, tx_index, tx_hash
        FROM transaction_inclusions
        WHERE chain_id = $1::numeric
          AND (block_number < $2::numeric OR (block_number = $2::numeric AND tx_index < $3::bigint))
          AND lower(raw->>'from') = $4::text
        UNION
        SELECT block_number, block_hash, tx_index, tx_hash
        FROM transaction_inclusions
        WHERE chain_id = $1::numeric
          AND (block_number < $2::numeric OR (block_number = $2::numeric AND tx_index < $3::bigint))
          AND lower(raw->>'to') = $4::text
        UNION
        SELECT block_number, block_hash, tx_index, tx_hash
        FROM receipts
        WHERE chain_id = $1::numeric
          AND (block_number < $2::numeric OR (block_number = $2::numeric AND tx_index < $3::bigint))
          AND lower(raw->>'contractAddress') = $4::text
    ) AS candidate)
SELECT
    inclusion.raw,
    receipt.raw,
    inclusion.block_number::text,
    inclusion.block_hash,
    inclusion.tx_index,
    inclusion.tx_hash,
    TRUE,
    finality.safe_number::text,
    finality.finalized_number::text,
	block.raw,
	EXISTS (
	    SELECT 1
	    FROM published_block_stage_results AS published_state_diff
	    WHERE published_state_diff.chain_id = inclusion.chain_id
	      AND published_state_diff.block_number = inclusion.block_number
	      AND published_state_diff.block_hash = inclusion.block_hash
	      AND published_state_diff.stage = 'state_diff'
	      AND published_state_diff.stage_version = 3
	      AND published_state_diff.state = 'complete'
	),
	execution.resolution,
	execution.execution_address,
	execution.execution_code_hash,
	decoding.signature,
	decoding.source,
	decoding.confidence
FROM candidates
JOIN transaction_inclusions AS inclusion
  ON inclusion.chain_id = $1::numeric
 AND inclusion.block_number = candidates.block_number
 AND inclusion.block_hash = candidates.block_hash
 AND inclusion.tx_index = candidates.tx_index
 AND inclusion.tx_hash = candidates.tx_hash
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = inclusion.chain_id
 AND canonical.number = inclusion.block_number
 AND canonical.block_hash = inclusion.block_hash
JOIN blocks AS block
  ON block.chain_id = inclusion.chain_id
 AND block.number = inclusion.block_number
 AND block.hash = inclusion.block_hash
JOIN receipts AS receipt
  ON receipt.chain_id = inclusion.chain_id
 AND receipt.block_number = inclusion.block_number
 AND receipt.block_hash = inclusion.block_hash
 AND receipt.tx_index = inclusion.tx_index
LEFT JOIN chain_finality AS finality ON finality.chain_id = inclusion.chain_id

LEFT JOIN LATERAL (
    SELECT resolution, execution_address, execution_code_hash
    FROM (
        SELECT effective.resolution, effective.execution_address,
               effective.execution_code_hash, 1 AS priority
        FROM transaction_effective_execution_identities AS effective
        WHERE effective.chain_id = inclusion.chain_id
          AND effective.block_number = inclusion.block_number
          AND effective.block_hash = inclusion.block_hash
          AND effective.transaction_hash = inclusion.tx_hash
          AND effective.transaction_index = inclusion.tx_index
          AND effective.context_address =
              decode(substring(inclusion.raw->>'to' from 3), 'hex')
          AND effective.canonical
          AND EXISTS (
              SELECT 1 FROM published_block_stage_results AS published_abi
              WHERE published_abi.chain_id = effective.chain_id
                AND published_abi.block_number = effective.block_number
                AND published_abi.block_hash = effective.block_hash
                AND published_abi.stage = 'abi'
                AND published_abi.stage_version = 4
                AND published_abi.state = 'complete'
          )
        UNION ALL
        SELECT raw.resolution, raw.execution_address,
               raw.execution_code_hash, 2 AS priority
        FROM transaction_execution_code_resolutions AS raw
        WHERE raw.chain_id = inclusion.chain_id
          AND raw.block_number = inclusion.block_number
          AND raw.block_hash = inclusion.block_hash
          AND raw.transaction_hash = inclusion.tx_hash
          AND raw.transaction_index = inclusion.tx_index
          AND raw.context_address =
              decode(substring(inclusion.raw->>'to' from 3), 'hex')
          AND raw.canonical
          AND NOT EXISTS (
              SELECT 1 FROM published_block_stage_results AS published_abi
              WHERE published_abi.chain_id = raw.chain_id
                AND published_abi.block_number = raw.block_number
                AND published_abi.block_hash = raw.block_hash
                AND published_abi.stage = 'abi'
                AND published_abi.stage_version = 4
                AND published_abi.state = 'complete'
          )
          AND EXISTS (
              SELECT 1
              FROM published_block_stage_results AS published_state_diff
              WHERE published_state_diff.chain_id = raw.chain_id
                AND published_state_diff.block_number = raw.block_number
                AND published_state_diff.block_hash = raw.block_hash
                AND published_state_diff.stage = 'state_diff'
                AND published_state_diff.stage_version = 3
                AND published_state_diff.state = 'complete'
          )
    ) AS candidates
    ORDER BY priority
    LIMIT 1
) AS execution ON TRUE
LEFT JOIN abi_decodings AS decoding
  ON decoding.chain_id = inclusion.chain_id
 AND decoding.block_number = inclusion.block_number
 AND decoding.block_hash = inclusion.block_hash
 AND decoding.transaction_hash = inclusion.tx_hash
 AND decoding.object_kind = 'transaction_calldata'
 AND decoding.object_index = ''
 AND decoding.target_address = execution.execution_address
 AND decoding.target_code_hash = execution.execution_code_hash
 AND decoding.status = 'decoded'
 AND decoding.canonical
 AND EXISTS (
     SELECT 1
     FROM published_block_stage_results AS published_abi
     WHERE published_abi.chain_id = decoding.chain_id
       AND published_abi.block_number = decoding.block_number
       AND published_abi.block_hash = decoding.block_hash
       AND published_abi.stage = 'abi'
       AND published_abi.stage_version = 4
       AND published_abi.state = 'complete'
 )
ORDER BY inclusion.block_number DESC, inclusion.tx_index DESC, inclusion.tx_hash DESC
LIMIT $5;

-- name: QueryListAddressTransactionsFirst :many
WITH candidates AS (
    SELECT candidate.block_number, candidate.block_hash, candidate.tx_index, candidate.tx_hash
    FROM (
        SELECT block_number, block_hash, tx_index, tx_hash
        FROM transaction_inclusions
        WHERE chain_id = $1::numeric
          AND block_number <= $2::numeric
          AND lower(raw->>'from') = $3::text
        UNION
        SELECT block_number, block_hash, tx_index, tx_hash
        FROM transaction_inclusions
        WHERE chain_id = $1::numeric
          AND block_number <= $2::numeric
          AND lower(raw->>'to') = $3::text
        UNION
        SELECT block_number, block_hash, tx_index, tx_hash
        FROM receipts
        WHERE chain_id = $1::numeric
          AND block_number <= $2::numeric
          AND lower(raw->>'contractAddress') = $3::text
    ) AS candidate)
SELECT
    inclusion.raw,
    receipt.raw,
    inclusion.block_number::text,
    inclusion.block_hash,
    inclusion.tx_index,
    inclusion.tx_hash,
    TRUE,
    finality.safe_number::text,
    finality.finalized_number::text,
	block.raw,
	EXISTS (
	    SELECT 1
	    FROM published_block_stage_results AS published_state_diff
	    WHERE published_state_diff.chain_id = inclusion.chain_id
	      AND published_state_diff.block_number = inclusion.block_number
	      AND published_state_diff.block_hash = inclusion.block_hash
	      AND published_state_diff.stage = 'state_diff'
	      AND published_state_diff.stage_version = 3
	      AND published_state_diff.state = 'complete'
	),
	execution.resolution,
	execution.execution_address,
	execution.execution_code_hash,
	decoding.signature,
	decoding.source,
	decoding.confidence
FROM candidates
JOIN transaction_inclusions AS inclusion
  ON inclusion.chain_id = $1::numeric
 AND inclusion.block_number = candidates.block_number
 AND inclusion.block_hash = candidates.block_hash
 AND inclusion.tx_index = candidates.tx_index
 AND inclusion.tx_hash = candidates.tx_hash
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = inclusion.chain_id
 AND canonical.number = inclusion.block_number
 AND canonical.block_hash = inclusion.block_hash
JOIN blocks AS block
  ON block.chain_id = inclusion.chain_id
 AND block.number = inclusion.block_number
 AND block.hash = inclusion.block_hash
JOIN receipts AS receipt
  ON receipt.chain_id = inclusion.chain_id
 AND receipt.block_number = inclusion.block_number
 AND receipt.block_hash = inclusion.block_hash
 AND receipt.tx_index = inclusion.tx_index
LEFT JOIN chain_finality AS finality ON finality.chain_id = inclusion.chain_id

LEFT JOIN LATERAL (
    SELECT resolution, execution_address, execution_code_hash
    FROM (
        SELECT effective.resolution, effective.execution_address,
               effective.execution_code_hash, 1 AS priority
        FROM transaction_effective_execution_identities AS effective
        WHERE effective.chain_id = inclusion.chain_id
          AND effective.block_number = inclusion.block_number
          AND effective.block_hash = inclusion.block_hash
          AND effective.transaction_hash = inclusion.tx_hash
          AND effective.transaction_index = inclusion.tx_index
          AND effective.context_address =
              decode(substring(inclusion.raw->>'to' from 3), 'hex')
          AND effective.canonical
          AND EXISTS (
              SELECT 1 FROM published_block_stage_results AS published_abi
              WHERE published_abi.chain_id = effective.chain_id
                AND published_abi.block_number = effective.block_number
                AND published_abi.block_hash = effective.block_hash
                AND published_abi.stage = 'abi'
                AND published_abi.stage_version = 4
                AND published_abi.state = 'complete'
          )
        UNION ALL
        SELECT raw.resolution, raw.execution_address,
               raw.execution_code_hash, 2 AS priority
        FROM transaction_execution_code_resolutions AS raw
        WHERE raw.chain_id = inclusion.chain_id
          AND raw.block_number = inclusion.block_number
          AND raw.block_hash = inclusion.block_hash
          AND raw.transaction_hash = inclusion.tx_hash
          AND raw.transaction_index = inclusion.tx_index
          AND raw.context_address =
              decode(substring(inclusion.raw->>'to' from 3), 'hex')
          AND raw.canonical
          AND NOT EXISTS (
              SELECT 1 FROM published_block_stage_results AS published_abi
              WHERE published_abi.chain_id = raw.chain_id
                AND published_abi.block_number = raw.block_number
                AND published_abi.block_hash = raw.block_hash
                AND published_abi.stage = 'abi'
                AND published_abi.stage_version = 4
                AND published_abi.state = 'complete'
          )
          AND EXISTS (
              SELECT 1
              FROM published_block_stage_results AS published_state_diff
              WHERE published_state_diff.chain_id = raw.chain_id
                AND published_state_diff.block_number = raw.block_number
                AND published_state_diff.block_hash = raw.block_hash
                AND published_state_diff.stage = 'state_diff'
                AND published_state_diff.stage_version = 3
                AND published_state_diff.state = 'complete'
          )
    ) AS candidates
    ORDER BY priority
    LIMIT 1
) AS execution ON TRUE
LEFT JOIN abi_decodings AS decoding
  ON decoding.chain_id = inclusion.chain_id
 AND decoding.block_number = inclusion.block_number
 AND decoding.block_hash = inclusion.block_hash
 AND decoding.transaction_hash = inclusion.tx_hash
 AND decoding.object_kind = 'transaction_calldata'
 AND decoding.object_index = ''
 AND decoding.target_address = execution.execution_address
 AND decoding.target_code_hash = execution.execution_code_hash
 AND decoding.status = 'decoded'
 AND decoding.canonical
 AND EXISTS (
     SELECT 1
     FROM published_block_stage_results AS published_abi
     WHERE published_abi.chain_id = decoding.chain_id
       AND published_abi.block_number = decoding.block_number
       AND published_abi.block_hash = decoding.block_hash
       AND published_abi.stage = 'abi'
       AND published_abi.stage_version = 4
       AND published_abi.state = 'complete'
 )
ORDER BY inclusion.block_number DESC, inclusion.tx_index DESC, inclusion.tx_hash DESC
LIMIT $4;
