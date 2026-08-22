-- name: QueryAddressOriginCoverage :many
WITH core_complete AS (
    SELECT EXISTS (
        SELECT 1
        FROM core_index_configuration AS configuration
        JOIN core_coverage_ranges AS coverage
          ON coverage.chain_id = configuration.chain_id
         AND coverage.range_start = 0
         AND coverage.range_end >= $2::numeric
        WHERE configuration.chain_id = $1::numeric
          AND configuration.configured_start = 0
    ) AS complete
), trace_complete AS (
    SELECT NOT EXISTS (
        SELECT 1
        FROM canonical_blocks AS canonical
        LEFT JOIN LATERAL (
            SELECT result.state
            FROM published_block_stage_results AS result
            WHERE result.chain_id = canonical.chain_id
              AND result.block_number = canonical.number
              AND result.block_hash = canonical.block_hash
              AND result.stage = 'trace'
              AND result.stage_version = 3
            LIMIT 1
        ) AS latest ON TRUE
        WHERE canonical.chain_id = $1::numeric
          AND canonical.number <= $2::numeric
          AND latest.state IS DISTINCT FROM 'complete'
    ) AS complete
)
SELECT core_complete.complete AND trace_complete.complete
FROM core_complete CROSS JOIN trace_complete;

-- name: QueryAddressOriginReference :many
SELECT EXISTS (
    SELECT 1
    FROM canonical_blocks
    WHERE chain_id = $1::numeric
      AND number = $2::numeric
      AND block_hash = $3
);

-- name: QueryBlockByHash :many
SELECT
    block.raw,
    block.number::text,
    block.hash,
    (canonical.block_hash IS NOT NULL),
    finality.safe_number::text,
    finality.finalized_number::text
FROM blocks AS block
LEFT JOIN canonical_blocks AS canonical
  ON canonical.chain_id = block.chain_id
 AND canonical.number = block.number
 AND canonical.block_hash = block.hash
LEFT JOIN chain_finality AS finality ON finality.chain_id = block.chain_id
WHERE block.chain_id = $1::numeric AND block.hash = $2
LIMIT 1;

-- name: QueryBlockByNumber :many
SELECT
    block.raw,
    canonical.number::text,
    canonical.block_hash,
    TRUE,
    finality.safe_number::text,
    finality.finalized_number::text
FROM canonical_blocks AS canonical
JOIN blocks AS block
  ON block.chain_id = canonical.chain_id
 AND block.number = canonical.number
 AND block.hash = canonical.block_hash
LEFT JOIN chain_finality AS finality ON finality.chain_id = canonical.chain_id
WHERE canonical.chain_id = $1::numeric AND canonical.number = $2::numeric;

-- name: QueryFirstContractOrigin :many
WITH candidates AS (
    SELECT receipt.block_number, receipt.tx_index,
           ARRAY[]::bigint[] AS trace_order, 0 AS source_rank,
           decode(substr(inclusion.raw->>'from', 3), 'hex') AS source_address,
           receipt.tx_hash AS transaction_hash
    FROM receipts AS receipt
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = receipt.chain_id
     AND canonical.number = receipt.block_number
     AND canonical.block_hash = receipt.block_hash
    JOIN transaction_inclusions AS inclusion
      ON inclusion.chain_id = receipt.chain_id
     AND inclusion.block_number = receipt.block_number
     AND inclusion.block_hash = receipt.block_hash
     AND inclusion.tx_index = receipt.tx_index
     AND inclusion.tx_hash = receipt.tx_hash
    WHERE receipt.chain_id = $1::numeric
      AND receipt.block_number <= $2::numeric
      AND lower(receipt.raw->>'contractAddress') =
          lower('0x' || encode($3, 'hex'))
      AND receipt.raw->>'status' = '0x1'

    UNION ALL

    SELECT trace.block_number, trace.transaction_index,
           string_to_array(trace.trace_path, '.')::bigint[] AS trace_order,
           1 AS source_rank, trace.from_address, trace.transaction_hash
    FROM normalized_traces AS trace
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = trace.chain_id
     AND canonical.number = trace.block_number
     AND canonical.block_hash = trace.block_hash
    WHERE trace.chain_id = $1::numeric
      AND trace.block_number <= $2::numeric
      AND trace.created_address = $3
      AND trace.canonical = TRUE
      AND trace.reverted = FALSE
      AND trace.depth > 0
      AND trace.call_type IN ('CREATE', 'CREATE2')
      AND trace.from_address IS NOT NULL
)
SELECT block_number::text, source_address, transaction_hash
FROM candidates
ORDER BY block_number, tx_index, source_rank, trace_order
LIMIT 1;

-- name: QueryFirstFundingOrigin :many
WITH candidates AS (
    SELECT inclusion.block_number, inclusion.tx_index,
           ARRAY[]::bigint[] AS trace_order, 0 AS source_rank,
           decode(substr(inclusion.raw->>'from', 3), 'hex') AS source_address,
           inclusion.tx_hash AS transaction_hash,
           inclusion.block_hash AS block_hash,
           'funding'::text AS origin_kind,
           NULL::text AS withdrawal_index
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
    WHERE inclusion.chain_id = $1::numeric
      AND inclusion.block_number <= $2::numeric
      AND lower(inclusion.raw->>'to') = lower('0x' || encode($3, 'hex'))
      AND inclusion.raw->>'value' <> '0x0'
      AND receipt.raw->>'status' = '0x1'

    UNION ALL

    SELECT trace.block_number, trace.transaction_index,
           string_to_array(trace.trace_path, '.')::bigint[] AS trace_order,
           1 AS source_rank, trace.from_address, trace.transaction_hash
           , trace.block_hash, 'funding'::text AS origin_kind, NULL::text AS withdrawal_index
    FROM normalized_traces AS trace
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = trace.chain_id
     AND canonical.number = trace.block_number
     AND canonical.block_hash = trace.block_hash
    WHERE trace.chain_id = $1::numeric
      AND trace.block_number <= $2::numeric
      AND trace.to_address = $3
      AND trace.canonical = TRUE
      AND trace.reverted = FALSE
      AND trace.depth > 0
      AND trace.value > 0
      AND trace.from_address IS NOT NULL

    UNION ALL

    SELECT withdrawal.block_number, 0::bigint,
           ARRAY[]::bigint[] AS trace_order, 2 AS source_rank,
           NULL::bytea AS source_address, NULL::bytea AS transaction_hash,
           withdrawal.block_hash, 'withdrawal'::text AS origin_kind,
           withdrawal.withdrawal_index::text AS withdrawal_index
    FROM withdrawals AS withdrawal
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = withdrawal.chain_id
     AND canonical.number = withdrawal.block_number
     AND canonical.block_hash = withdrawal.block_hash
    WHERE withdrawal.chain_id = $1::numeric
      AND withdrawal.block_number <= $2::numeric
      AND withdrawal.address = $3

    UNION ALL

    SELECT block.number, 0::bigint,
           ARRAY[]::bigint[] AS trace_order, 3 AS source_rank,
           NULL::bytea AS source_address, NULL::bytea AS transaction_hash,
           block.hash, 'block_fee_recipient'::text AS origin_kind,
           NULL::text AS withdrawal_index
    FROM blocks AS block
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = block.chain_id
     AND canonical.number = block.number
     AND canonical.block_hash = block.hash
    WHERE block.chain_id = $1::numeric
      AND block.number <= $2::numeric
      AND lower(block.raw->>'miner') = lower('0x' || encode($3, 'hex'))
)
SELECT block_number::text, source_address, transaction_hash, origin_kind, block_hash, withdrawal_index
FROM candidates
ORDER BY block_number, tx_index, source_rank, trace_order
LIMIT 1;

-- name: QueryGenesisAddressOrigin :many
SELECT EXISTS (
    SELECT 1
    FROM genesis_account_observations AS observation
    JOIN genesis_state_imports AS imported
      ON imported.chain_id = observation.chain_id
     AND imported.block_hash = observation.block_hash
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = observation.chain_id
     AND canonical.number = 0
     AND canonical.block_hash = observation.block_hash
    WHERE observation.chain_id = $1::numeric
      AND observation.address = $2
      AND imported.state = 'complete'
);

-- name: QueryListBlocks :many
SELECT
    block.raw,
    canonical.number::text,
    canonical.block_hash,
    TRUE,
    finality.safe_number::text,
    finality.finalized_number::text
FROM canonical_blocks AS canonical
JOIN blocks AS block
  ON block.chain_id = canonical.chain_id
 AND block.number = canonical.number
 AND block.hash = canonical.block_hash
LEFT JOIN chain_finality AS finality ON finality.chain_id = canonical.chain_id
WHERE canonical.chain_id = $1::numeric
  AND canonical.number < $2::numeric
ORDER BY canonical.number DESC
LIMIT $3;

-- name: QueryListBlocksFirst :many
SELECT
    block.raw,
    canonical.number::text,
    canonical.block_hash,
    TRUE,
    finality.safe_number::text,
    finality.finalized_number::text
FROM canonical_blocks AS canonical
JOIN blocks AS block
  ON block.chain_id = canonical.chain_id
 AND block.number = canonical.number
 AND block.hash = canonical.block_hash
LEFT JOIN chain_finality AS finality ON finality.chain_id = canonical.chain_id
WHERE canonical.chain_id = $1::numeric
  AND canonical.number <= $2::numeric
ORDER BY canonical.number DESC
LIMIT $3;

-- name: QueryListTransactionsFirst :many
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
    block.raw
FROM transaction_inclusions AS inclusion
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
WHERE inclusion.chain_id = $1::numeric
  AND inclusion.block_number <= $2::numeric
ORDER BY inclusion.block_number DESC, inclusion.tx_index DESC
LIMIT $3;

-- name: QueryListTransactionsWithMethod :many
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
FROM transaction_inclusions AS inclusion
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
              SELECT 1
              FROM published_block_stage_results AS published_abi
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
              SELECT 1
              FROM published_block_stage_results AS published_abi
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
WHERE inclusion.chain_id = $1::numeric
  AND (
      inclusion.block_number < $2::numeric
      OR (inclusion.block_number = $2::numeric AND inclusion.tx_index < $3)
  )
ORDER BY inclusion.block_number DESC, inclusion.tx_index DESC
LIMIT $4;

-- name: QueryListTransactionsWithMethodFirst :many
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
FROM transaction_inclusions AS inclusion
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
              SELECT 1
              FROM published_block_stage_results AS published_abi
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
              SELECT 1
              FROM published_block_stage_results AS published_abi
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
WHERE inclusion.chain_id = $1::numeric
  AND inclusion.block_number <= $2::numeric
ORDER BY inclusion.block_number DESC, inclusion.tx_index DESC
LIMIT $3;

-- name: QuerySearchBlockNumber :many
WITH visible_labels AS (
    SELECT document.result_key, document.result_label, document.id
    FROM search_catalog_documents AS document
    WHERE document.chain_id = $1::numeric
      AND document.source_kind = 'label'
      AND document.result_kind = 'block'
      AND document.valid_from_generation <= $3
      AND (document.valid_to_generation IS NULL OR document.valid_to_generation > $3)
)
SELECT canonical.number::text,
       canonical.block_hash,
       COALESCE(operator_label.result_label, 'Block #' || canonical.number::text),
       CASE WHEN operator_label.result_label IS NULL THEN 100 ELSE 110 END::bigint
FROM canonical_blocks AS canonical
LEFT JOIN LATERAL (
    SELECT visible.result_label
    FROM visible_labels AS visible
    WHERE lower(visible.result_key) IN (
        canonical.number::text,
        '0x' || encode(canonical.block_hash, 'hex')
    )
    ORDER BY CASE WHEN lower(visible.result_key) = canonical.number::text THEN 0 ELSE 1 END,
             visible.id DESC
    LIMIT 1
) AS operator_label ON TRUE
WHERE canonical.chain_id = $1::numeric AND canonical.number = $2::numeric;

-- name: QuerySearchHash :many
WITH visible_labels AS (
    SELECT document.result_kind, document.result_key, document.result_label, document.id
    FROM search_catalog_documents AS document
    WHERE document.chain_id = $1::numeric
      AND document.source_kind = 'label'
      AND document.valid_from_generation <= $3
      AND (document.valid_to_generation IS NULL OR document.valid_to_generation > $3)
)
SELECT kind, key, label, rank, canonical
FROM (
    SELECT
        'block'::text AS kind,
        '0x' || encode(block.hash, 'hex') AS key,
        COALESCE(operator_label.result_label, 'Block #' || block.number::text) AS label,
        CASE WHEN operator_label.result_label IS NULL THEN 100 ELSE 110 END::bigint AS rank,
        (canonical.block_hash IS NOT NULL) AS canonical
    FROM blocks AS block
    LEFT JOIN canonical_blocks AS canonical
      ON canonical.chain_id = block.chain_id
     AND canonical.number = block.number
     AND canonical.block_hash = block.hash
    LEFT JOIN LATERAL (
        SELECT visible.result_label
        FROM visible_labels AS visible
        WHERE visible.result_kind = 'block'
          AND lower(visible.result_key) = ('0x' || encode(block.hash, 'hex'))
        ORDER BY visible.id DESC
        LIMIT 1
    ) AS operator_label ON TRUE
    WHERE block.chain_id = $1::numeric AND block.hash = $2

    UNION ALL

    SELECT
        'transaction'::text,
        '0x' || encode(transaction.hash, 'hex'),
        COALESCE(operator_label.result_label, 'Transaction 0x' || encode(transaction.hash, 'hex')),
        CASE WHEN operator_label.result_label IS NULL THEN 90 ELSE 110 END::bigint,
        EXISTS (
            SELECT 1 FROM transaction_inclusions AS inclusion
            JOIN canonical_blocks AS canonical
              ON canonical.chain_id = inclusion.chain_id
             AND canonical.number = inclusion.block_number
             AND canonical.block_hash = inclusion.block_hash
            WHERE inclusion.chain_id = transaction.chain_id
              AND inclusion.tx_hash = transaction.hash
        )
    FROM transactions AS transaction
    LEFT JOIN LATERAL (
        SELECT visible.result_label
        FROM visible_labels AS visible
        WHERE visible.result_kind = 'transaction'
          AND lower(visible.result_key) = ('0x' || encode(transaction.hash, 'hex'))
        ORDER BY visible.id DESC
        LIMIT 1
    ) AS operator_label ON TRUE
    WHERE transaction.chain_id = $1::numeric AND transaction.hash = $2
) AS results
ORDER BY rank DESC, kind
LIMIT $4;

-- name: QuerySearchText :many
WITH visible_documents AS (
    SELECT document.*
    FROM search_catalog_documents AS document
    WHERE document.chain_id = $1::numeric
      AND document.valid_from_generation <= $4
      AND (document.valid_to_generation IS NULL OR document.valid_to_generation > $4)
), candidates(
    kind, key, label, rank, canonical, name_source,
    verification_match_type, verification_valid_from_block,
    verification_request_digest, verification_job_id,
    verification_proxy_artifact
) AS (
    SELECT document.result_kind AS kind,
           lower(document.result_key) AS key,
           document.result_label AS label,
           CASE document.source_kind
             WHEN 'label' THEN CASE WHEN $2 = ANY(document.exact_terms) THEN 110 ELSE 80 END
             WHEN 'name' THEN CASE WHEN $2 = ANY(document.exact_terms) THEN 100 ELSE 70 END
             WHEN 'token' THEN CASE
                 WHEN lower(document.result_key) = $2 THEN 105
                 WHEN $2 = ANY(document.exact_terms) THEN 95 ELSE 65 END
             WHEN 'verified_contract' THEN CASE
                 WHEN lower(document.result_key) = $2 THEN 104
                 WHEN $2 = ANY(document.exact_terms) THEN 94 ELSE 64 END
           END::bigint AS rank,
           CASE WHEN document.source_kind IN ('name', 'token') THEN TRUE ELSE NULL END::boolean AS canonical,
           document.name_source,
           document.verification_match_type,
           document.valid_from_block AS verification_valid_from_block,
           document.verification_request_digest,
           document.verification_job_id,
           proxy_artifact.verification_job_id IS NOT NULL AS verification_proxy_artifact
    FROM visible_documents AS document
    LEFT JOIN canonical_blocks AS canonical
      ON (document.source_kind = 'token' OR (document.source_kind = 'name' AND document.block_hash IS NOT NULL))
     AND canonical.chain_id = document.chain_id
     AND canonical.number = document.block_number
     AND canonical.block_hash = document.block_hash
    LEFT JOIN LATERAL (
        SELECT observation.code_hash, observation.block_number
        FROM visible_documents AS observation
        JOIN canonical_blocks AS observed_canonical
          ON observed_canonical.chain_id = observation.chain_id
         AND observed_canonical.number = observation.block_number
         AND observed_canonical.block_hash = observation.block_hash
        WHERE document.source_kind = 'verified_contract'
          AND observation.source_kind = 'code'
          AND observation.target_address = document.target_address
          AND observation.block_number <= $3::numeric
          AND observation.source_canonical = TRUE
        ORDER BY observation.block_number DESC, observation.block_hash DESC
        LIMIT 1
    ) AS current_code ON TRUE
    LEFT JOIN verified_contract_proxy_artifacts AS proxy_artifact
      ON document.source_kind = 'verified_contract'
     AND proxy_artifact.chain_id = document.chain_id
     AND proxy_artifact.address = document.target_address
     AND proxy_artifact.code_hash = document.code_hash
     AND proxy_artifact.valid_from_block = document.valid_from_block
     AND proxy_artifact.verification_job_id = document.verification_job_id
     AND proxy_artifact.request_digest = document.verification_request_digest
    WHERE document.source_kind <> 'code'
      AND (
          document.source_kind <> 'name'
          OR $10::bigint = 0
          OR document.name_observation_id = $10::bigint
      )
      AND ($2 = ANY(document.exact_terms) OR EXISTS (
          SELECT 1 FROM unnest(document.partial_terms) AS term
          WHERE strpos(term, $2) > 0
      ))
      AND (
          document.source_kind <> 'token'
          OR document.id = (
              SELECT latest.id
              FROM visible_documents AS latest
              JOIN canonical_blocks AS latest_canonical
                ON latest_canonical.chain_id = latest.chain_id
               AND latest_canonical.number = latest.block_number
               AND latest_canonical.block_hash = latest.block_hash
              WHERE latest.source_kind = 'token'
                AND latest.logical_identity = document.logical_identity
                AND latest.block_number <= $3::numeric
                AND latest.source_canonical = TRUE
              ORDER BY latest.block_number DESC, latest.valid_from_generation DESC, latest.id DESC
              LIMIT 1
          )
      )
      AND (
          document.source_kind = 'label'
          OR (
              document.source_kind = 'name'
              AND document.source_canonical = TRUE
              AND (
                  document.block_hash IS NULL
                  OR (
                      document.block_number <= $3::numeric
                      AND canonical.block_hash IS NOT NULL
                  )
              )
          )
          OR (
              document.source_kind = 'token'
              AND document.block_number <= $3::numeric
              AND canonical.block_hash IS NOT NULL
              AND document.source_canonical = TRUE
          )
          OR (
              document.source_kind = 'verified_contract'
              AND document.verification_job_id IS NOT NULL
              AND current_code.code_hash = document.code_hash
              AND document.valid_from_block <= current_code.block_number
              AND (document.valid_to_block IS NULL OR document.valid_to_block >= current_code.block_number)
          )
      )
)
SELECT result.kind, result.key, result.label, result.rank,
       result.canonical, result.name_source
FROM (
    SELECT DISTINCT ON (kind, key) kind, key, label, rank, canonical, name_source
    FROM candidates
    ORDER BY kind, key, rank DESC,
             verification_proxy_artifact DESC,
             (verification_match_type = 'full') DESC NULLS LAST,
             verification_valid_from_block DESC NULLS LAST,
             verification_request_digest ASC NULLS LAST,
             verification_job_id ASC NULLS LAST,
             label
) AS result
WHERE $5::boolean = false
   OR result.rank < $6::bigint
   OR (result.rank = $6::bigint AND result.kind > $7::text)
   OR (result.rank = $6::bigint AND result.kind = $7::text AND result.key > $8::text)
ORDER BY result.rank DESC, result.kind, result.key
LIMIT $9;

-- name: QueryStatusState :many
SELECT
	configuration.configured_start::text,
	contiguous.range_end::text,
	contiguous_block.block_hash,
    checkpoint.contiguous_through::text,
    checkpoint.block_hash,
	highest.range_end::text,
	highest_block.block_hash,
    finality.safe_number::text,
    finality.finalized_number::text,
    trace_result.state
FROM (SELECT 1) AS singleton
LEFT JOIN core_index_configuration AS configuration
  ON configuration.chain_id = $1::numeric
LEFT JOIN core_coverage_ranges AS contiguous
  ON contiguous.chain_id = $1::numeric
 AND contiguous.range_start = configuration.configured_start
LEFT JOIN canonical_blocks AS contiguous_block
  ON contiguous_block.chain_id = $1::numeric
 AND contiguous_block.number = contiguous.range_end
LEFT JOIN index_checkpoints AS checkpoint
  ON checkpoint.chain_id = $1::numeric AND checkpoint.stage = 'core'
LEFT JOIN LATERAL (
	SELECT range_end
	FROM core_coverage_ranges
	WHERE chain_id = $1::numeric
	ORDER BY range_end DESC
	LIMIT 1
) AS highest ON TRUE
LEFT JOIN canonical_blocks AS highest_block
  ON highest_block.chain_id = $1::numeric
 AND highest_block.number = highest.range_end
LEFT JOIN chain_finality AS finality
  ON finality.chain_id = $1::numeric
LEFT JOIN published_block_stage_results AS trace_result
  ON trace_result.chain_id = $1::numeric
 AND trace_result.block_number = contiguous.range_end
 AND trace_result.block_hash = contiguous_block.block_hash
 AND trace_result.stage = 'trace'
 AND trace_result.stage_version = 3;

-- name: QueryTransactionByHash :many
SELECT
    inclusion.raw,
    receipt.raw,
    inclusion.block_number::text,
    inclusion.block_hash,
    inclusion.tx_index,
    inclusion.tx_hash,
    (canonical.block_hash IS NOT NULL),
    finality.safe_number::text,
    finality.finalized_number::text,
    block.raw
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
WHERE inclusion.chain_id = $1::numeric AND inclusion.tx_hash = $2
ORDER BY (canonical.block_hash IS NOT NULL) DESC, inclusion.block_number DESC
LIMIT 1;

-- name: QueryTransactionSelectorCandidates :many
WITH request(
    ordinal, block_number, block_hash, transaction_index,
    address, code_hash, selector, selector_scope, exact_address_only
) AS (
    SELECT (element->>'ordinal')::integer,
           element->>'block_number',
           element->>'block_hash',
           (element->>'transaction_index')::bigint,
           element->>'address',
           element->>'code_hash',
           element->>'selector',
           element->>'selector_scope',
           (element->>'exact_address_only')::boolean
    FROM jsonb_array_elements($2::jsonb) AS element
), direct_candidates AS (
    SELECT request.ordinal,
           CASE WHEN indexed.address = decode(request.address, 'hex')
                  AND indexed.valid_from_block <= request.block_number::numeric
                  AND (verified.valid_to_block IS NULL OR
                       verified.valid_to_block >= request.block_number::numeric)
                THEN 'verified' ELSE 'code_hash' END AS source,
           indexed.address AS source_address,
           indexed.code_hash AS source_code_hash,
           selector.abi_entry,
           CASE WHEN indexed.address = decode(request.address, 'hex')
                  AND indexed.valid_from_block <= request.block_number::numeric
                  AND (verified.valid_to_block IS NULL OR
                       verified.valid_to_block >= request.block_number::numeric)
                THEN indexed.valid_from_block ELSE 0 END AS valid_from_block,
           CASE WHEN indexed.address = decode(request.address, 'hex')
                  AND indexed.valid_from_block <= request.block_number::numeric
                  AND (verified.valid_to_block IS NULL OR
                       verified.valid_to_block >= request.block_number::numeric)
                THEN verified.valid_to_block ELSE NULL END AS valid_to_block,
           FALSE AS selector_scoped,
           selector.signature
    FROM request
    JOIN verified_function_selector_sets AS indexed
      ON indexed.chain_id = $1::numeric
     AND ((NOT request.exact_address_only AND
           indexed.code_hash = decode(request.code_hash, 'hex')) OR
          (request.exact_address_only AND
           indexed.address = decode(request.address, 'hex')))
     AND indexed.status = 'complete'
    JOIN verified_contracts AS verified
      ON verified.chain_id = indexed.chain_id
     AND verified.address = indexed.address
     AND verified.code_hash = indexed.code_hash
     AND verified.valid_from_block = indexed.valid_from_block
     AND verified.verification_job_id = indexed.verification_job_id
    JOIN verified_function_selectors AS selector
      ON selector.verification_job_id = indexed.verification_job_id
     AND selector.chain_id = indexed.chain_id
     AND selector.address = indexed.address
     AND selector.code_hash = indexed.code_hash
     AND selector.selector = decode(request.selector, 'hex')
    WHERE NOT request.exact_address_only OR
          (indexed.valid_from_block <= request.block_number::numeric AND
           (verified.valid_to_block IS NULL OR
            verified.valid_to_block >= request.block_number::numeric))
), bound_routes AS (
    SELECT request.ordinal, binding.source,
           binding.source_address, binding.source_code_hash,
           selector.abi_entry,
           request.block_number::numeric AS valid_from_block,
           request.block_number::numeric AS valid_to_block,
           binding.source = 'diamond_facet' AS selector_scoped,
           selector.signature
    FROM request
    JOIN contract_abis AS binding
      ON NOT request.exact_address_only
     AND binding.chain_id = $1::numeric
     AND binding.address = decode(request.address, 'hex')
     AND binding.code_hash = decode(request.code_hash, 'hex')
     AND binding.canonical
     AND binding.source IN ('proxy_implementation', 'diamond_facet')
     AND binding.valid_from_block <= request.block_number::numeric
     AND (binding.valid_to_block IS NULL OR binding.valid_to_block >= request.block_number::numeric)
     AND (binding.source <> 'diamond_facet' OR
          binding.selector_scope = decode(request.selector_scope, 'hex'))
    JOIN verified_function_selector_sets AS indexed
      ON indexed.chain_id = binding.chain_id
     AND indexed.address = binding.source_address
     AND indexed.code_hash = binding.source_code_hash
     AND indexed.status = 'complete'
    JOIN verified_function_selectors AS selector
      ON selector.verification_job_id = indexed.verification_job_id
     AND selector.chain_id = indexed.chain_id
     AND selector.address = indexed.address
     AND selector.code_hash = indexed.code_hash
     AND selector.selector = decode(request.selector, 'hex')
), published_proxy_routes AS (
    SELECT request.ordinal, 'proxy_implementation'::text AS source,
           route.implementation_address AS source_address,
           route.implementation_code_hash AS source_code_hash,
           selector.abi_entry,
           request.block_number::numeric AS valid_from_block,
           request.block_number::numeric AS valid_to_block,
           FALSE AS selector_scoped,
           selector.signature
    FROM request
    JOIN LATERAL (
        SELECT observation.proxy_kind, observation.proxy_pattern,
               observation.evidence_state, observation.implementation_address,
               observation.implementation_code_hash
        FROM proxy_observations AS observation
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = observation.chain_id
         AND canonical.number = observation.block_number
         AND canonical.block_hash = observation.block_hash
        JOIN proxy_observation_generations AS generation
          ON generation.chain_id = observation.chain_id
         AND generation.proxy_address = observation.proxy_address
         AND generation.observation_block_hash = observation.block_hash
         AND generation.observation_stage_version = observation.stage_version
        JOIN published_block_stage_results AS published
          ON published.chain_id = generation.chain_id
         AND published.block_hash = generation.observation_block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = generation.observation_stage_version
         AND published.durable_job_id = generation.durable_job_id
         AND published.job_generation = generation.job_generation
         AND published.state = 'complete'
        WHERE observation.chain_id = $1::numeric
          AND observation.proxy_address = decode(request.address, 'hex')
          AND observation.proxy_code_hash = decode(request.code_hash, 'hex')
          AND observation.stage_version = 2
          AND observation.canonical
          AND observation.confidence IN ('verified', 'high')
          AND observation.implementation_address IS NOT NULL
          AND observation.implementation_code_hash IS NOT NULL
          AND observation.block_number <= request.block_number::numeric
        ORDER BY observation.block_number DESC, generation.id DESC
        LIMIT 1
    ) AS route ON NOT request.exact_address_only
    JOIN verified_function_selector_sets AS indexed
      ON indexed.chain_id = $1::numeric
     AND indexed.code_hash = route.implementation_code_hash
     AND indexed.status = 'complete'
     AND (
         indexed.address = route.implementation_address OR (
             route.proxy_kind = 'cwia'
             AND route.proxy_pattern = 'clone'
             AND route.evidence_state = 'exact'
         )
     )
    JOIN verified_function_selectors AS selector
      ON selector.verification_job_id = indexed.verification_job_id
     AND selector.chain_id = indexed.chain_id
     AND selector.address = indexed.address
     AND selector.code_hash = indexed.code_hash
     AND selector.selector = decode(request.selector, 'hex')
), published_diamond_routes AS (
    SELECT request.ordinal, 'diamond_facet'::text AS source,
           route.facet_address AS source_address,
           route.facet_code_hash AS source_code_hash,
           selector.abi_entry,
           request.block_number::numeric AS valid_from_block,
           request.block_number::numeric AS valid_to_block,
           TRUE AS selector_scoped,
           selector.signature
    FROM request
    JOIN LATERAL (
        SELECT active.facet_address, facet.code_hash AS facet_code_hash
        FROM canonical_diamond_selector_intervals AS active
        JOIN LATERAL (
            SELECT candidate.code_hash
            FROM published_diamond_loupe_snapshots AS snapshot
            JOIN diamond_loupe_facets AS candidate
              ON candidate.snapshot_id = snapshot.id
            WHERE snapshot.chain_id = active.chain_id
              AND snapshot.diamond_address = active.diamond_address
              AND snapshot.block_number <= request.block_number::numeric
              AND snapshot.detection_state = 'confirmed'
              AND snapshot.canonical
              AND candidate.facet_address = active.facet_address
              AND candidate.facet_kind = 'facet'
              AND candidate.code_exists
              AND candidate.code_hash IS NOT NULL
            ORDER BY snapshot.block_number DESC, snapshot.id DESC
            LIMIT 1
        ) AS facet ON TRUE
        WHERE active.chain_id = $1::numeric
          AND active.diamond_address = decode(request.address, 'hex')
          AND active.selector = decode(request.selector, 'hex')
          AND (
              active.valid_from_block_number < request.block_number::numeric OR
              (active.valid_from_block_number = request.block_number::numeric AND
               active.valid_from_transaction_index < request.transaction_index)
          )
          AND (
              active.valid_to_block_number IS NULL OR
              active.valid_to_block_number > request.block_number::numeric OR
              (active.valid_to_block_number = request.block_number::numeric AND
               active.valid_to_transaction_index >= request.transaction_index)
          )
        ORDER BY active.valid_from_block_number DESC,
                 active.valid_from_transaction_index DESC,
                 active.valid_from_log_index DESC,
                 active.valid_from_cut_index DESC,
                 active.valid_from_selector_index DESC
        LIMIT 1
    ) AS route ON NOT request.exact_address_only
    JOIN verified_function_selector_sets AS indexed
      ON indexed.chain_id = $1::numeric
     AND indexed.address = route.facet_address
     AND indexed.code_hash = route.facet_code_hash
     AND indexed.status = 'complete'
    JOIN verified_function_selectors AS selector
      ON selector.verification_job_id = indexed.verification_job_id
     AND selector.chain_id = indexed.chain_id
     AND selector.address = indexed.address
     AND selector.code_hash = indexed.code_hash
     AND selector.selector = decode(request.selector, 'hex')
), combined AS (
    SELECT * FROM direct_candidates
    UNION
    SELECT * FROM bound_routes
    UNION
    SELECT * FROM published_proxy_routes
    UNION
    SELECT * FROM published_diamond_routes
)
SELECT ranked.ordinal, ranked.source, ranked.source_address,
       ranked.source_code_hash, ranked.abi_entry,
       ranked.valid_from_block::text, ranked.valid_to_block::text,
       ranked.selector_scoped, ranked.signature
FROM (
    SELECT combined.*,
           row_number() OVER (
               PARTITION BY combined.ordinal
               ORDER BY CASE combined.source
                            WHEN 'verified' THEN 1
                            WHEN 'code_hash' THEN 2
                            ELSE 3
                        END,
                        combined.signature, combined.source_address,
                        combined.source_code_hash
           ) AS candidate_number
    FROM combined
) AS ranked
WHERE ranked.candidate_number <= $3::bigint
ORDER BY ranked.ordinal, ranked.candidate_number;

-- name: QueryValidateTransactionCursor :many
SELECT
    EXISTS (
	    SELECT 1 FROM canonical_blocks AS snapshot
	    WHERE snapshot.chain_id = $1::numeric
	      AND snapshot.number = $2::numeric
	      AND snapshot.block_hash = $3
    )
AND EXISTS (
    SELECT 1
    FROM transaction_inclusions AS inclusion
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = inclusion.chain_id
     AND canonical.number = inclusion.block_number
     AND canonical.block_hash = inclusion.block_hash
    WHERE inclusion.chain_id = $1::numeric
      AND inclusion.block_number = $4::numeric
      AND inclusion.block_hash = $5
      AND inclusion.tx_index = $6
      AND inclusion.tx_hash = $7
);
