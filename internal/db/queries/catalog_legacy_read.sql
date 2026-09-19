-- name: CatalogAddressDelegations :many
WITH ordered AS (
    SELECT authz.block_number, authz.block_hash,
           authz.transaction_hash, authz.transaction_index,
           authz.authorization_index, authz.delegate_address,
           lag(authz.delegate_address) OVER (
               ORDER BY authz.block_number, authz.transaction_index,
                        authz.authorization_index
           ) AS previous_delegate
    FROM eip7702_authorizations AS authz
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = authz.chain_id
     AND canonical.number = authz.block_number
     AND canonical.block_hash = authz.block_hash
    WHERE authz.chain_id = sqlc.arg('chain_id')::numeric
      AND authz.authority = sqlc.arg('authority')
      AND authz.application_status = 'applied'
      AND authz.canonical
      AND authz.block_number <= sqlc.arg('max_block_number')::numeric
)
SELECT block_number::text, block_hash, transaction_hash,
       transaction_index::text, authorization_index::text,
       delegate_address, previous_delegate::bytea AS previous_delegate
FROM ordered
WHERE NOT sqlc.arg('has_cursor')::boolean OR (block_number, transaction_index, authorization_index)
    < (sqlc.arg('cursor_block_number')::numeric, sqlc.arg('cursor_transaction_index')::numeric, sqlc.arg('cursor_authorization_index')::numeric)
ORDER BY ordered.block_number DESC, ordered.transaction_index DESC,
         ordered.authorization_index DESC
LIMIT sqlc.arg('limit');

-- name: CatalogAddressInternalTransactions :many
WITH candidates AS (
    SELECT chain_id, block_number, block_hash, transaction_hash, trace_path
    FROM normalized_traces
    WHERE chain_id = sqlc.arg('chain_id')::numeric AND canonical = TRUE AND depth > 0
      AND block_number <= sqlc.arg('max_block_number')::numeric AND from_address = sqlc.arg('from_address')::bytea
    UNION
    SELECT chain_id, block_number, block_hash, transaction_hash, trace_path
    FROM normalized_traces
    WHERE chain_id = sqlc.arg('chain_id')::numeric AND canonical = TRUE AND depth > 0
      AND block_number <= sqlc.arg('max_block_number')::numeric AND to_address = sqlc.arg('from_address')::bytea
    UNION
    SELECT chain_id, block_number, block_hash, transaction_hash, trace_path
    FROM normalized_traces
    WHERE chain_id = sqlc.arg('chain_id')::numeric AND canonical = TRUE AND depth > 0
      AND block_number <= sqlc.arg('max_block_number')::numeric AND created_address = sqlc.arg('from_address')::bytea
)
SELECT trace.block_number::text, trace.block_hash, block.timestamp::text,
       trace.transaction_hash, trace.transaction_index::text,
       trace.trace_path, trace.depth, trace.call_type,
       trace.from_address, trace.to_address, trace.created_address,
       trace.value AS trace_value, trace.gas AS trace_gas, trace.gas_used AS trace_gas_used,
       trace.input, trace.error, trace.reverted
FROM candidates
JOIN normalized_traces AS trace
  ON trace.chain_id = candidates.chain_id
 AND trace.block_number = candidates.block_number
 AND trace.block_hash = candidates.block_hash
 AND trace.transaction_hash = candidates.transaction_hash
 AND trace.trace_path = candidates.trace_path
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = trace.chain_id
 AND canonical.number = trace.block_number
 AND canonical.block_hash = trace.block_hash
JOIN blocks AS block
  ON block.chain_id = trace.chain_id
 AND block.number = trace.block_number
 AND block.hash = trace.block_hash
WHERE NOT sqlc.arg('has_cursor')::boolean OR (
    trace.block_number,
    trace.transaction_index,
    string_to_array(trace.trace_path, '.')::bigint[],
    trace.block_hash,
    trace.transaction_hash
) < (
    sqlc.arg('cursor_block_number')::numeric,
    sqlc.arg('cursor_transaction_index')::bigint,
    string_to_array(sqlc.arg('string_to_array'), '.')::bigint[],
    sqlc.arg('cursor_block_hash')::bytea,
    sqlc.arg('cursor_transaction_hash')::bytea
)
ORDER BY trace.block_number DESC, trace.transaction_index DESC,
         string_to_array(trace.trace_path, '.')::bigint[] DESC,
         trace.block_hash DESC, trace.transaction_hash DESC
LIMIT sqlc.arg('limit');

-- name: CatalogAddressTokenTransfers :many
WITH candidates AS (
    SELECT chain_id, block_number, block_hash, log_index, sub_index
    FROM token_events
    WHERE chain_id = sqlc.arg('chain_id')::numeric AND canonical = TRUE
      AND block_number <= sqlc.arg('max_block_number')::numeric AND from_address = sqlc.arg('from_address')::bytea
      AND event_kind IN ('transfer', 'mint', 'burn')
      AND ((sqlc.arg('token_family')::text = 'erc20' AND standard = 'erc20') OR (sqlc.arg('token_family')::text = 'nft' AND standard IN ('erc721', 'erc1155')))
    UNION
    SELECT chain_id, block_number, block_hash, log_index, sub_index
    FROM token_events
    WHERE chain_id = sqlc.arg('chain_id')::numeric AND canonical = TRUE
      AND block_number <= sqlc.arg('max_block_number')::numeric AND to_address = sqlc.arg('from_address')::bytea
      AND event_kind IN ('transfer', 'mint', 'burn')
      AND ((sqlc.arg('token_family')::text = 'erc20' AND standard = 'erc20') OR (sqlc.arg('token_family')::text = 'nft' AND standard IN ('erc721', 'erc1155')))
)
SELECT event.block_number::text, event.block_hash, block.timestamp::text,
       event.transaction_hash, inclusion.tx_index::text,
       event.log_index::text, event.sub_index::text,
       event.token_address, event.standard, event.event_kind,
       event.from_address, event.to_address, event.token_id AS event_token_id,
       event.amount AS event_amount, event.confidence, metadata.decimals
FROM candidates
JOIN token_events AS event
  ON event.chain_id = candidates.chain_id
 AND event.block_number = candidates.block_number
 AND event.block_hash = candidates.block_hash
 AND event.log_index = candidates.log_index
 AND event.sub_index = candidates.sub_index
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = event.chain_id
 AND canonical.number = event.block_number
 AND canonical.block_hash = event.block_hash
JOIN blocks AS block
  ON block.chain_id = event.chain_id
 AND block.number = event.block_number
 AND block.hash = event.block_hash
JOIN transaction_inclusions AS inclusion
  ON inclusion.chain_id = event.chain_id
 AND inclusion.block_number = event.block_number
 AND inclusion.block_hash = event.block_hash
 AND inclusion.tx_hash = event.transaction_hash
LEFT JOIN LATERAL (
    SELECT (CASE
               WHEN contract.standard = 'erc20' AND contract.metadata_state = 'complete'
               THEN contract.decimals
           END)::numeric AS decimals
    FROM token_contracts AS contract
    JOIN canonical_blocks AS observation
      ON observation.chain_id = contract.chain_id
     AND observation.number = contract.observed_block_number
     AND observation.block_hash = contract.observed_block_hash
    WHERE contract.chain_id = event.chain_id
      AND contract.address = event.token_address
      AND contract.observed_block_number <= event.block_number
    ORDER BY contract.observed_block_number DESC, contract.code_hash DESC
    LIMIT 1
) AS metadata ON event.standard = 'erc20'
WHERE NOT sqlc.arg('has_cursor')::boolean OR (
    event.block_number,
    inclusion.tx_index,
    event.log_index,
    event.sub_index,
    event.block_hash,
    event.transaction_hash
) < (
    sqlc.arg('cursor_block_number')::numeric,
    sqlc.arg('cursor_transaction_index')::bigint,
    sqlc.arg('cursor_log_index')::bigint,
    sqlc.arg('cursor_batch_index')::integer,
    sqlc.arg('cursor_block_hash')::bytea,
    sqlc.arg('cursor_transaction_hash')::bytea
)
ORDER BY event.block_number DESC, inclusion.tx_index DESC,
         event.log_index DESC, event.sub_index DESC,
         event.block_hash DESC, event.transaction_hash DESC
LIMIT sqlc.arg('limit');

-- name: CatalogAggregateStats :one
WITH selected_stats AS (
    SELECT stats.*
    FROM block_statistics AS stats
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = stats.chain_id
     AND canonical.number = stats.block_number
     AND canonical.block_hash = stats.block_hash
    WHERE stats.chain_id = sqlc.arg('chain_id')::numeric
      AND stats.block_number BETWEEN sqlc.arg('from_block_number')::numeric AND sqlc.arg('to_block_number')::numeric
      AND stats.canonical = true
), selected_tokens AS (
    SELECT event.standard, event.event_kind
    FROM token_events AS event
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = event.chain_id
     AND canonical.number = event.block_number
     AND canonical.block_hash = event.block_hash
    WHERE event.chain_id = sqlc.arg('chain_id')::numeric
      AND event.block_number BETWEEN sqlc.arg('from_block_number')::numeric AND sqlc.arg('to_block_number')::numeric
      AND event.canonical = true
)
SELECT
count(*)::text AS block_count,
COALESCE(sum(transaction_count), 0)::text AS transaction_count,
COALESCE(sum(gas_used), 0)::text AS gas_used,
COALESCE(sum(burned_wei), 0)::text AS burned_wei,
COALESCE(sum(blob_burned_wei), 0)::text AS blob_burned_wei,
(SELECT count(*)::text FROM selected_tokens) AS token_event_count,
(SELECT count(*)::text FROM selected_tokens
         WHERE standard = 'erc20' AND event_kind IN ('transfer', 'mint', 'burn')) AS erc20_transfer_count,
(SELECT count(*)::text FROM selected_tokens
         WHERE standard IN ('erc721', 'erc1155') AND event_kind IN ('transfer', 'mint', 'burn')) AS nft_transfer_count,
COALESCE((CASE WHEN COALESCE(sum(block_interval_seconds) FILTER (
                     WHERE block_interval_seconds IS NOT NULL
                 ), 0) = 0 THEN NULL
            ELSE trim(trailing '.' FROM trim(trailing '0' FROM
                 round(
                     sum(transaction_count) FILTER (WHERE block_interval_seconds IS NOT NULL)
                     / sum(block_interval_seconds) FILTER (WHERE block_interval_seconds IS NOT NULL),
                     18
                 )::text))
       END),'')::text AS weighted_tps,
(CASE WHEN COALESCE(sum(block_interval_seconds) FILTER (
                     WHERE block_interval_seconds IS NOT NULL
                 ), 0) = 0 THEN NULL
            ELSE trim(trailing '.' FROM trim(trailing '0' FROM
                 round(
                     sum(transaction_count) FILTER (WHERE block_interval_seconds IS NOT NULL)
                     / sum(block_interval_seconds) FILTER (WHERE block_interval_seconds IS NOT NULL),
                     18
                 )::text))
       END IS NOT NULL)::boolean AS weighted_tps_present
FROM selected_stats;

-- name: CatalogBlockStats :many
SELECT stats.chain_id::text, stats.block_number::text, stats.block_hash,
       stats.transaction_count::text, stats.gas_used::text, stats.gas_limit::text,
       stats.base_fee_per_gas AS stats_base_fee_per_gas, stats.blob_gas_used AS stats_blob_gas_used,
       stats.excess_blob_gas AS stats_excess_blob_gas, stats.blob_base_fee_per_gas AS stats_blob_base_fee_per_gas,
       stats.burned_wei AS stats_burned_wei, stats.blob_burned_wei AS stats_blob_burned_wei,
       stats.block_timestamp::text, stats.block_interval_seconds AS stats_block_interval_seconds,
       stats.transactions_per_second AS transactions_per_second,
       token.token_event_count::text, token.token_transfer_count::text,
       token.nft_transfer_count::text, stats.computed_at
FROM block_statistics AS stats
JOIN canonical_blocks AS cb
  ON cb.chain_id = stats.chain_id
 AND cb.number = stats.block_number
 AND cb.block_hash = stats.block_hash
LEFT JOIN LATERAL (
    SELECT count(*) AS token_event_count,
           count(*) FILTER (
               WHERE event.standard = 'erc20'
                 AND event.event_kind IN ('transfer', 'mint', 'burn')
           ) AS token_transfer_count,
           count(*) FILTER (
               WHERE event.standard IN ('erc721', 'erc1155')
                 AND event.event_kind IN ('transfer', 'mint', 'burn')
           ) AS nft_transfer_count
    FROM token_events AS event
    WHERE event.chain_id = stats.chain_id
      AND event.block_number = stats.block_number
      AND event.block_hash = stats.block_hash
      AND event.canonical = true
) AS token ON true
WHERE stats.chain_id = sqlc.arg('chain_id')::numeric
  AND stats.block_number BETWEEN sqlc.arg('from_block_number')::numeric AND sqlc.arg('to_block_number')::numeric
  AND stats.canonical = true
ORDER BY stats.block_number;

-- name: CatalogCanonicalSnapshot :one
SELECT number::text, block_hash
FROM canonical_blocks AS canonical
WHERE chain_id = sqlc.arg('chain_id')::numeric
ORDER BY canonical.number DESC
LIMIT 1;

-- name: CatalogCanonicalTransactionInclusion :one
SELECT inclusion.block_number::text, inclusion.block_hash, inclusion.tx_index::text
FROM transaction_inclusions AS inclusion
JOIN canonical_blocks AS cb
  ON cb.chain_id = inclusion.chain_id
 AND cb.number = inclusion.block_number
 AND cb.block_hash = inclusion.block_hash
WHERE inclusion.chain_id = sqlc.arg('chain_id')::numeric AND inclusion.tx_hash = sqlc.arg('tx_hash')
LIMIT 1;

-- name: CatalogErc20BalanceCandidates :many
SELECT d.token_address
FROM token_balance_deltas AS d
JOIN canonical_blocks AS cb
  ON cb.chain_id = d.chain_id
 AND cb.number = d.block_number
 AND cb.block_hash = d.block_hash
WHERE d.chain_id = sqlc.arg('chain_id')::numeric
  AND d.block_number <= sqlc.arg('max_block_number')::numeric
  AND d.owner_address = sqlc.arg('owner_address')
  AND d.token_id IS NULL
  AND d.canonical = TRUE
  AND (sqlc.arg('has_cursor')::boolean = FALSE OR d.token_address > sqlc.arg('token_address'))
GROUP BY d.token_address
ORDER BY d.token_address
LIMIT sqlc.arg('limit');

-- name: CatalogExactConstructorArtifact :one
SELECT verified.code_hash, verified.abi, verified.constructor_arguments,
       verified.valid_from_block::text, verified.valid_to_block
FROM contract_code_observations AS code
JOIN verified_contracts AS verified
  ON verified.chain_id = code.chain_id
 AND verified.address = code.address
 AND verified.code_hash = code.code_hash
 AND verified.valid_from_block <= sqlc.arg('max_valid_from_block')::numeric
 AND (verified.valid_to_block IS NULL OR verified.valid_to_block >= sqlc.arg('max_valid_from_block')::numeric)
JOIN verification_results AS result
  ON result.job_id = verified.verification_job_id
 AND result.request_digest = verified.request_digest
 AND result.outcome_kind = 'verification_success'
 AND result.outcome->'creation_match'->>'match_type' = 'full'
WHERE code.chain_id = sqlc.arg('chain_id')::numeric
  AND code.block_number = sqlc.arg('max_valid_from_block')::numeric
  AND code.block_hash = sqlc.arg('block_hash')
  AND code.address = sqlc.arg('address')
  AND code.canonical
  AND verified.abi IS NOT NULL
ORDER BY verified.valid_from_block DESC
LIMIT 1;

-- name: CatalogFirstIncompleteStageInRange :one
WITH heights AS (
    SELECT generate_series(sqlc.arg('from_block')::numeric, sqlc.arg('to_block')::numeric, 1::numeric) AS number
)
SELECT heights.number::text, cb.block_hash, latest.state
FROM heights
LEFT JOIN canonical_blocks AS cb
  ON cb.chain_id = sqlc.arg('chain_id')::numeric AND cb.number = heights.number
LEFT JOIN LATERAL (
    SELECT result.state
    FROM published_block_stage_results AS result
    WHERE result.chain_id = cb.chain_id
      AND result.block_number = cb.number
      AND result.block_hash = cb.block_hash
      AND result.stage = sqlc.arg('stage')
      AND result.stage_version = sqlc.arg('stage_version')
) AS latest ON true
WHERE cb.block_hash IS NULL OR latest.state IS DISTINCT FROM 'complete'
ORDER BY heights.number
LIMIT 1;

-- name: CatalogLatestStage :one
SELECT state
FROM published_block_stage_results
WHERE chain_id = sqlc.arg('chain_id')::numeric
  AND block_number = sqlc.arg('block_number')::numeric
  AND block_hash = sqlc.arg('block_hash')
  AND stage = sqlc.arg('stage')
  AND stage_version = sqlc.arg('stage_version');

-- name: CatalogNftBalanceCandidates :many
SELECT d.token_address, d.token_id::text
FROM token_balance_deltas AS d
JOIN canonical_blocks AS cb
  ON cb.chain_id = d.chain_id
 AND cb.number = d.block_number
 AND cb.block_hash = d.block_hash
WHERE d.chain_id = sqlc.arg('chain_id')::numeric
  AND d.block_number <= sqlc.arg('max_block_number')::numeric
  AND d.owner_address = sqlc.arg('owner_address')
  AND d.token_id IS NOT NULL
  AND d.canonical = true
  AND (
      sqlc.arg('has_cursor')::boolean = false OR
      (d.token_address, d.token_id) > (sqlc.arg('token_address'), sqlc.arg('cursor_token_id')::numeric)
  )
GROUP BY d.token_address, d.token_id
ORDER BY d.token_address, d.token_id
LIMIT sqlc.arg('limit');

-- name: CatalogTokenEvents :many
SELECT
e.chain_id::text AS chain_id,
e.block_number::text AS block_number,
e.block_hash AS block_hash,
e.log_index::text AS log_index,
e.sub_index::text AS sub_index,
e.transaction_hash AS transaction_hash,
e.token_address AS token_address,
e.standard AS standard,
e.event_kind AS event_kind,
e.operator AS operator,
e.from_address AS from_address,
e.to_address AS to_address,
e.token_id AS token_id,
e.amount AS amount,
e.confidence AS confidence,
metadata.decimals AS decimals
FROM token_events AS e
JOIN canonical_blocks AS cb
  ON cb.chain_id = e.chain_id
 AND cb.number = e.block_number
 AND cb.block_hash = e.block_hash
LEFT JOIN LATERAL (
    SELECT (CASE
               WHEN contract.standard = 'erc20' AND contract.metadata_state = 'complete'
               THEN contract.decimals
           END)::numeric AS decimals
    FROM token_contracts AS contract
    JOIN canonical_blocks AS observation
      ON observation.chain_id = contract.chain_id
     AND observation.number = contract.observed_block_number
     AND observation.block_hash = contract.observed_block_hash
    WHERE contract.chain_id = e.chain_id
      AND contract.address = e.token_address
      AND contract.observed_block_number <= e.block_number
    ORDER BY contract.observed_block_number DESC, contract.code_hash DESC
    LIMIT 1
) AS metadata ON e.standard = 'erc20'
WHERE e.chain_id = sqlc.arg('chain_id')::text::numeric
  AND e.block_number <= sqlc.arg('snapshot_number')::text::numeric
  AND e.token_address = sqlc.arg('token_address')
  AND e.canonical = true
  AND (
      sqlc.arg('has_cursor')::boolean = false OR
      (e.block_number, e.log_index, e.sub_index, e.block_hash) <
      (sqlc.arg('before_number')::text::numeric, sqlc.arg('before_log_index')::text::bigint, sqlc.arg('before_sub_index')::text::integer, sqlc.arg('before_block_hash')::bytea)
  )
ORDER BY e.block_number DESC, e.log_index DESC, e.sub_index DESC, e.block_hash DESC
LIMIT sqlc.arg('page_limit');

-- name: CatalogTraceStagePublication :one
SELECT state, durable_job_id, job_generation
FROM published_block_stage_results
WHERE chain_id = sqlc.arg('chain_id')::numeric
  AND block_number = sqlc.arg('block_number')::numeric
  AND block_hash = sqlc.arg('block_hash')
  AND stage = sqlc.arg('stage')
  AND stage_version = sqlc.arg('stage_version');

-- name: CatalogTransactionAuthorizations :many
SELECT authorization_index, authorization_chain_id::text,
       authorization_nonce::text, delegate_address, y_parity, r, s,
       authority, signature_status, application_status, skip_reason
FROM eip7702_authorizations
WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_hash = sqlc.arg('block_hash') AND transaction_hash = sqlc.arg('transaction_hash')
  AND canonical
ORDER BY authorization_index
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CatalogTransactionCalldataDecoding :one
SELECT decoding.status, decoding.signature, decoding.source, decoding.confidence,
       decoding.arguments, decoding.candidates, decoding.warning,
       decoding.target_address, decoding.target_code_hash,
       decoding.source_address, decoding.source_code_hash,
       decoding.return_status, decoding.return_arguments
FROM abi_decodings AS decoding
WHERE decoding.chain_id = sqlc.arg('chain_id')::numeric
  AND decoding.block_hash = sqlc.arg('block_hash')
  AND decoding.transaction_hash = sqlc.arg('transaction_hash')
  AND decoding.object_kind = 'transaction_calldata'
  AND decoding.object_index = ''
  AND decoding.target_address = sqlc.arg('target_address')
  AND decoding.target_code_hash = sqlc.arg('target_code_hash')
  AND decoding.canonical
  AND EXISTS (
      SELECT 1
      FROM published_block_stage_results AS published
      WHERE published.chain_id = decoding.chain_id
        AND published.block_number = decoding.block_number
        AND published.block_hash = decoding.block_hash
        AND published.stage = 'abi'
        AND published.stage_version = 4
        AND published.state = 'complete'
  );

-- name: CatalogTransactionCalldataExecution :one
WITH published_abi AS (
    SELECT 1
    FROM published_block_stage_results AS published
    WHERE published.chain_id = sqlc.arg('chain_id')::numeric
      AND published.block_number = sqlc.arg('block_number')::numeric
      AND published.block_hash = sqlc.arg('block_hash')::bytea
      AND published.stage = 'abi'
      AND published.stage_version = 4
      AND published.state = 'complete'
), selected AS (
    SELECT effective.context_address, effective.execution_address,
           effective.execution_code_hash, effective.resolution,
           effective.evidence_source, 1 AS priority
    FROM transaction_effective_execution_identities AS effective
    WHERE effective.chain_id = sqlc.arg('chain_id')::numeric
      AND effective.block_number = sqlc.arg('block_number')::numeric
      AND effective.block_hash = sqlc.arg('block_hash')
      AND effective.transaction_hash = sqlc.arg('transaction_hash')
      AND effective.context_address = sqlc.arg('context_address')
	  AND effective.transaction_index = sqlc.arg('transaction_index')
      AND effective.canonical
      AND EXISTS (SELECT 1 FROM published_abi)
    UNION ALL
    SELECT raw.context_address, raw.execution_address,
           raw.execution_code_hash, raw.resolution,
           raw.evidence_source, 2 AS priority
    FROM transaction_execution_code_resolutions AS raw
    WHERE raw.chain_id = sqlc.arg('chain_id')::numeric
      AND raw.block_number = sqlc.arg('block_number')::numeric
      AND raw.block_hash = sqlc.arg('block_hash')
      AND raw.transaction_hash = sqlc.arg('transaction_hash')
      AND raw.context_address = sqlc.arg('context_address')
	  AND raw.transaction_index = sqlc.arg('transaction_index')
      AND raw.canonical
      AND NOT EXISTS (SELECT 1 FROM published_abi)
)
SELECT context_address, execution_address, execution_code_hash,
       resolution, evidence_source
FROM selected
ORDER BY priority
LIMIT 1;

-- name: CatalogTransactionCalldataIdentity :one
SELECT inclusion.block_number::text, inclusion.block_hash, inclusion.tx_index, inclusion.raw
FROM transaction_inclusions AS inclusion
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = inclusion.chain_id
 AND canonical.number = inclusion.block_number
 AND canonical.block_hash = inclusion.block_hash
WHERE inclusion.chain_id = sqlc.arg('chain_id')::numeric AND inclusion.tx_hash = sqlc.arg('tx_hash')
LIMIT 1;

-- name: CatalogTransactionFailureReceiptStatus :one
SELECT
COALESCE((receipt.raw->>'status'),'')::text AS status,
(receipt.raw->>'status' IS NOT NULL)::boolean AS status_present
FROM receipts AS receipt
WHERE receipt.chain_id = sqlc.arg('chain_id')::numeric
  AND receipt.block_number = sqlc.arg('block_number')::numeric
  AND receipt.block_hash = sqlc.arg('block_hash')
  AND receipt.tx_hash = sqlc.arg('tx_hash')
LIMIT 1;

-- name: CatalogTransactionFailureRoot :one
SELECT trace_path, parent_path, depth, call_type,
       from_address, to_address, created_address,
       value AS value, gas AS gas, gas_used AS gas_used,
       input, output, error, direct_reverted, reverted,
       execution_address, execution_code_hash, execution_resolution
FROM normalized_traces
WHERE chain_id = sqlc.arg('chain_id')::numeric
  AND block_number = sqlc.arg('block_number')::numeric
  AND block_hash = sqlc.arg('block_hash')
  AND transaction_hash = sqlc.arg('transaction_hash')
  AND trace_path = ''
  AND depth = 0
  AND canonical = true
LIMIT 1;

-- name: CatalogTransactionInternalTransactions :many
SELECT trace.trace_path, trace.depth, trace.call_type,
       trace.from_address, trace.to_address, trace.created_address,
       trace.value::text
FROM normalized_traces AS trace
WHERE trace.chain_id = sqlc.arg('chain_id')::numeric
  AND trace.block_hash = sqlc.arg('block_hash')
  AND trace.transaction_hash = sqlc.arg('transaction_hash')
  AND trace.canonical = true
  AND trace.depth > 0
  AND trace.value > 0
  AND trace.reverted = false
ORDER BY string_to_array(trace.trace_path, '.')::bigint[]
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CatalogTransactionLogABICandidates :many
WITH target_code AS (
    SELECT sqlc.arg('code_hash')::bytea AS code_hash
    WHERE sqlc.arg('code_hash')::bytea IS NOT NULL
    UNION ALL
    (
        SELECT observation.code_hash
        FROM contract_code_observations AS observation
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = observation.chain_id
         AND canonical.number = observation.block_number
         AND canonical.block_hash = observation.block_hash
        WHERE observation.chain_id = sqlc.arg('chain_id')::numeric
          AND observation.address = sqlc.arg('address')::bytea
          AND observation.block_number <= sqlc.arg('max_block_number')::numeric
          AND observation.canonical
          AND sqlc.arg('code_hash')::bytea IS NULL
        ORDER BY observation.block_number DESC, observation.observed_at DESC
        LIMIT 1
    )
), historical_proxy AS (
    SELECT observation.proxy_kind, observation.proxy_pattern,
           observation.evidence_state, observation.implementation_address,
           observation.implementation_code_hash
    FROM proxy_observations AS observation
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = observation.chain_id
     AND canonical.number = observation.block_number
     AND canonical.block_hash = observation.block_hash
    CROSS JOIN target_code
    WHERE observation.chain_id = sqlc.arg('chain_id')::numeric
      AND observation.proxy_address = sqlc.arg('address')
      AND observation.proxy_code_hash = target_code.code_hash
      AND observation.block_number <= sqlc.arg('max_block_number')::numeric
      AND observation.canonical
      AND observation.confidence IN ('verified', 'high')
      AND observation.implementation_address IS NOT NULL
      AND observation.implementation_code_hash IS NOT NULL
    ORDER BY observation.block_number DESC, observation.stage_version DESC,
             observation.block_hash DESC
    LIMIT 1
), candidates AS (
	    SELECT target_code.code_hash AS target_code_hash, binding.abi,
	           CASE binding.source
	             WHEN 'verified' THEN 'verified'
	             WHEN 'proxy_implementation' THEN 'proxy_implementation'
	             WHEN 'diamond_facet' THEN 'diamond_facet'
	             ELSE 'signature_database'
	           END AS registry_source,
	           CASE binding.source
	             WHEN 'verified' THEN 'exact_address'
	             WHEN 'proxy_implementation' THEN 'proxy_implementation'
	             WHEN 'diamond_facet' THEN 'diamond_facet'
	             ELSE 'signature_database'
	           END AS source_kind,
	           binding.source_address, binding.source_code_hash,
	           binding.selector_scope,
	           binding.valid_from_block, binding.valid_to_block,
	           CASE binding.source
	             WHEN 'verified' THEN 0
	             WHEN 'proxy_implementation' THEN 2
	             WHEN 'diamond_facet' THEN 2
	             ELSE 4
	           END AS priority,
           binding.created_at, NULL::bytea AS request_digest, NULL::uuid AS job_id
    FROM contract_abis AS binding, target_code
    WHERE binding.chain_id = sqlc.arg('chain_id')::numeric
      AND binding.address = sqlc.arg('address')
      AND binding.code_hash = target_code.code_hash
      AND binding.valid_from_block <= sqlc.arg('max_block_number')::numeric
      AND (binding.valid_to_block IS NULL OR binding.valid_to_block >= sqlc.arg('max_block_number')::numeric)
      AND binding.canonical
    UNION ALL
    SELECT target_code.code_hash, verified.abi,
           CASE WHEN verified.address = sqlc.arg('address')
                     AND verified.valid_from_block <= sqlc.arg('max_block_number')::numeric
                     AND (verified.valid_to_block IS NULL OR verified.valid_to_block >= sqlc.arg('max_block_number')::numeric)
                THEN 'verified' ELSE 'code_hash' END,
           CASE WHEN verified.address = sqlc.arg('address')
                     AND verified.valid_from_block <= sqlc.arg('max_block_number')::numeric
                     AND (verified.valid_to_block IS NULL OR verified.valid_to_block >= sqlc.arg('max_block_number')::numeric)
                THEN 'exact_address' ELSE 'code_hash' END,
	           verified.address, verified.code_hash,
	           decode(repeat('00', 32), 'hex'),
           0::numeric, NULL::numeric,
           CASE WHEN verified.address = sqlc.arg('address')
                     AND verified.valid_from_block <= sqlc.arg('max_block_number')::numeric
                     AND (verified.valid_to_block IS NULL OR verified.valid_to_block >= sqlc.arg('max_block_number')::numeric)
                THEN 1 ELSE 3 END,
           verified.created_at, verified.request_digest, verified.verification_job_id
    FROM verified_contracts AS verified, target_code
    WHERE verified.chain_id = sqlc.arg('chain_id')::numeric
      AND verified.code_hash = target_code.code_hash
      AND verified.abi IS NOT NULL
    UNION ALL
    SELECT target_code.code_hash, verified.abi, 'proxy_implementation',
	           'proxy_implementation', verified.address, verified.code_hash,
	           decode(repeat('00', 32), 'hex'),
           0::numeric, NULL::numeric,
           CASE WHEN verified.address = proxy.implementation_address THEN 2 ELSE 3 END,
           verified.created_at, verified.request_digest, verified.verification_job_id
    FROM verified_contracts AS verified, target_code, historical_proxy AS proxy
    WHERE verified.chain_id = sqlc.arg('chain_id')::numeric
      AND verified.code_hash = proxy.implementation_code_hash
      AND verified.abi IS NOT NULL
      AND (
          verified.address = proxy.implementation_address OR (
              proxy.proxy_kind = 'cwia'
              AND proxy.proxy_pattern = 'clone'
              AND proxy.evidence_state = 'exact'
          )
      )
)
SELECT target_code_hash, abi, registry_source, source_kind,
       source_address, source_code_hash, selector_scope,
       valid_from_block::text, valid_to_block AS valid_to_block
FROM candidates
ORDER BY priority, created_at DESC, request_digest ASC NULLS FIRST,
         job_id ASC NULLS FIRST, source_address, source_code_hash
LIMIT sqlc.arg('limit');

-- name: CatalogTransactionLogs :many
SELECT log.log_index, log.raw, decoding.status, decoding.signature,
       decoding.source, decoding.confidence, decoding.arguments,
       decoding.candidates, decoding.warning,
       decoding.target_address, decoding.target_code_hash,
       decoding.source_address, decoding.source_code_hash,
       attribution.trace_path, attribution.execution_address
FROM logs AS log
LEFT JOIN abi_decodings AS decoding
  ON decoding.chain_id = log.chain_id
 AND decoding.block_hash = log.block_hash
 AND decoding.transaction_hash = log.tx_hash
 AND decoding.object_kind = 'log'
 AND decoding.object_index = log.log_index::text
 AND decoding.canonical
LEFT JOIN trace_log_attributions AS attribution
  ON attribution.chain_id = log.chain_id
 AND attribution.block_number = log.block_number
 AND attribution.block_hash = log.block_hash
 AND attribution.transaction_hash = log.tx_hash
 AND attribution.log_index = log.log_index
 AND attribution.canonical
 AND EXISTS (
     SELECT 1
     FROM published_block_stage_results AS published
     WHERE published.chain_id = attribution.chain_id
       AND published.block_hash = attribution.block_hash
       AND published.stage = 'trace'
       AND published.stage_version = 3
       AND published.state = 'complete'
 )
WHERE log.chain_id = sqlc.arg('chain_id')::numeric AND log.block_hash = sqlc.arg('block_hash') AND log.tx_hash = sqlc.arg('tx_hash')
ORDER BY log.log_index
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CatalogTransactionResourceIdentity :one
SELECT
inclusion.block_number::text,
inclusion.block_hash,
inclusion.tx_index,
((canonical.block_hash IS NOT NULL))::boolean AS canonical
FROM transaction_inclusions AS inclusion
LEFT JOIN canonical_blocks AS canonical
  ON canonical.chain_id = inclusion.chain_id
 AND canonical.number = inclusion.block_number
 AND canonical.block_hash = inclusion.block_hash
WHERE inclusion.chain_id = sqlc.arg('chain_id')::numeric AND inclusion.tx_hash = sqlc.arg('tx_hash')
ORDER BY (canonical.block_hash IS NOT NULL) DESC, inclusion.block_number DESC
LIMIT 1;

-- name: CatalogTransactionStageState :one
SELECT state, job_generation
FROM published_block_stage_results
WHERE chain_id = sqlc.arg('chain_id')::numeric
  AND block_number = sqlc.arg('block_number')::numeric
  AND block_hash = sqlc.arg('block_hash')
  AND stage = sqlc.arg('stage')
  AND stage_version = sqlc.arg('stage_version');

-- name: CatalogTransactionStateChanges :many
SELECT address, field_kind, storage_key, before_value, after_value
FROM transaction_state_changes
WHERE chain_id = sqlc.arg('chain_id')::numeric
  AND block_hash = sqlc.arg('block_hash')
  AND transaction_hash = sqlc.arg('transaction_hash')
  AND canonical = true
ORDER BY address, field_kind, storage_key
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CatalogTransactionTokenEvents :many
SELECT
event.chain_id::text AS chain_id,
event.block_number::text AS block_number,
event.block_hash AS block_hash,
event.log_index::text AS log_index,
event.sub_index::text AS sub_index,
event.transaction_hash AS transaction_hash,
event.token_address AS token_address,
event.standard AS standard,
event.event_kind AS event_kind,
event.operator AS operator,
event.from_address AS from_address,
event.to_address AS to_address,
event.token_id AS token_id,
event.amount AS amount,
event.confidence AS confidence,
metadata.decimals AS decimals
FROM token_events AS event
LEFT JOIN LATERAL (
    SELECT (CASE
               WHEN contract.standard = 'erc20' AND contract.metadata_state = 'complete'
               THEN contract.decimals
           END)::numeric AS decimals
    FROM token_contracts AS contract
    JOIN canonical_blocks AS observation
      ON observation.chain_id = contract.chain_id
     AND observation.number = contract.observed_block_number
     AND observation.block_hash = contract.observed_block_hash
    WHERE contract.chain_id = event.chain_id
      AND contract.address = event.token_address
      AND contract.observed_block_number <= event.block_number
    ORDER BY contract.observed_block_number DESC, contract.code_hash DESC
    LIMIT 1
) AS metadata ON event.standard = 'erc20'
WHERE event.chain_id = sqlc.arg('chain_id')::numeric
  AND event.block_hash = sqlc.arg('block_hash')
  AND event.transaction_hash = sqlc.arg('transaction_hash')
  AND event.canonical = true
ORDER BY event.log_index, event.sub_index
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CatalogTransactionTrace :many
SELECT trace_path, parent_path, depth, call_type,
       from_address, to_address, created_address,
       value AS value, gas AS gas, gas_used AS gas_used,
       input, output, error, direct_reverted, reverted,
       execution_address, execution_code_hash, execution_resolution
FROM normalized_traces
WHERE chain_id = sqlc.arg('chain_id')::numeric
  AND block_number = sqlc.arg('block_number')::numeric
  AND block_hash = sqlc.arg('block_hash')
  AND transaction_hash = sqlc.arg('transaction_hash')
  AND canonical = true
ORDER BY depth, trace_path
LIMIT sqlc.arg('limit');

-- name: CatalogTransactionTraceDecodings :many
SELECT decoding.object_kind, decoding.object_index, decoding.status,
       decoding.signature, decoding.source, decoding.confidence,
       decoding.arguments, decoding.candidates, decoding.warning,
       decoding.target_address, decoding.target_code_hash,
       decoding.source_address, decoding.source_code_hash,
       decoding.return_status, decoding.return_arguments
FROM abi_decodings AS decoding
WHERE decoding.chain_id = sqlc.arg('chain_id')::numeric
  AND decoding.block_hash = sqlc.arg('block_hash')
  AND decoding.transaction_hash = sqlc.arg('transaction_hash')
  AND decoding.object_kind IN ('trace_calldata', 'trace_constructor', 'trace_revert')
  AND decoding.canonical
  AND EXISTS (
      SELECT 1
      FROM published_block_stage_results AS published
      WHERE published.chain_id = decoding.chain_id
        AND published.block_hash = decoding.block_hash
        AND published.stage = 'abi'
        AND published.stage_version = 4
        AND published.state = 'complete'
  )
ORDER BY decoding.object_index, decoding.object_kind;

-- name: CatalogTransactionTraceExecution :many
SELECT trace_path, COALESCE(to_address, created_address, from_address), execution_address,
       execution_code_hash, execution_resolution
FROM normalized_traces
WHERE chain_id = sqlc.arg('chain_id')::numeric
  AND block_number = sqlc.arg('block_number')::numeric
  AND block_hash = sqlc.arg('block_hash')
  AND transaction_hash = sqlc.arg('transaction_hash')
  AND canonical
ORDER BY depth, trace_path
LIMIT sqlc.arg('limit');

-- name: CatalogTransactionVerifiedAddressSelectors :many
SELECT indexed.code_hash, selector.signature, selector.abi_entry
FROM verified_function_selector_sets AS indexed
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
WHERE indexed.chain_id = sqlc.arg('chain_id')::numeric
  AND indexed.address = sqlc.arg('address')
  AND indexed.status = 'complete'
  AND indexed.valid_from_block <= sqlc.arg('max_valid_from_block')::numeric
  AND (verified.valid_to_block IS NULL OR verified.valid_to_block >= sqlc.arg('max_valid_from_block')::numeric)
  AND selector.selector = sqlc.arg('selector')
ORDER BY selector.signature, indexed.code_hash, indexed.verification_job_id
LIMIT sqlc.arg('limit');

-- name: CatalogValidateCanonicalSnapshot :one
SELECT EXISTS (
    SELECT 1
    FROM canonical_blocks
    WHERE chain_id = sqlc.arg('chain_id')::numeric AND number = sqlc.arg('number')::numeric AND block_hash = sqlc.arg('block_hash')
);
