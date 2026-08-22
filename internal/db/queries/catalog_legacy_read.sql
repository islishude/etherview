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
    WHERE authz.chain_id = $1::numeric
      AND authz.authority = $2
      AND authz.application_status = 'applied'
      AND authz.canonical
      AND authz.block_number <= $3::numeric
)
SELECT block_number::text, block_hash, transaction_hash,
       transaction_index::text, authorization_index::text,
       delegate_address, previous_delegate
FROM ordered
WHERE NOT $4 OR (block_number, transaction_index, authorization_index)
    < ($5::numeric, $6::numeric, $7::numeric)
ORDER BY ordered.block_number DESC, ordered.transaction_index DESC,
         ordered.authorization_index DESC
LIMIT $8;

-- name: CatalogAddressInternalTransactions :many
WITH candidates AS (
    SELECT chain_id, block_number, block_hash, transaction_hash, trace_path
    FROM normalized_traces
    WHERE chain_id = $1::numeric AND canonical = TRUE AND depth > 0
      AND block_number <= $2::numeric AND from_address = $3::bytea
    UNION
    SELECT chain_id, block_number, block_hash, transaction_hash, trace_path
    FROM normalized_traces
    WHERE chain_id = $1::numeric AND canonical = TRUE AND depth > 0
      AND block_number <= $2::numeric AND to_address = $3::bytea
    UNION
    SELECT chain_id, block_number, block_hash, transaction_hash, trace_path
    FROM normalized_traces
    WHERE chain_id = $1::numeric AND canonical = TRUE AND depth > 0
      AND block_number <= $2::numeric AND created_address = $3::bytea
)
SELECT trace.block_number::text, trace.block_hash, block.timestamp::text,
       trace.transaction_hash, trace.transaction_index::text,
       trace.trace_path, trace.depth, trace.call_type,
       trace.from_address, trace.to_address, trace.created_address,
       trace.value::text, trace.gas::text, trace.gas_used::text,
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
WHERE NOT $4::boolean OR (
    trace.block_number,
    trace.transaction_index,
    string_to_array(trace.trace_path, '.')::bigint[],
    trace.block_hash,
    trace.transaction_hash
) < (
    $5::numeric,
    $6::bigint,
    string_to_array($7, '.')::bigint[],
    $8::bytea,
    $9::bytea
)
ORDER BY trace.block_number DESC, trace.transaction_index DESC,
         string_to_array(trace.trace_path, '.')::bigint[] DESC,
         trace.block_hash DESC, trace.transaction_hash DESC
LIMIT $10;

-- name: CatalogAddressTokenTransfers :many
WITH candidates AS (
    SELECT chain_id, block_number, block_hash, log_index, sub_index
    FROM token_events
    WHERE chain_id = $1::numeric AND canonical = TRUE
      AND block_number <= $2::numeric AND from_address = $3::bytea
      AND event_kind IN ('transfer', 'mint', 'burn')
      AND (($4 = 'erc20' AND standard = 'erc20') OR ($4 = 'nft' AND standard IN ('erc721', 'erc1155')))
    UNION
    SELECT chain_id, block_number, block_hash, log_index, sub_index
    FROM token_events
    WHERE chain_id = $1::numeric AND canonical = TRUE
      AND block_number <= $2::numeric AND to_address = $3::bytea
      AND event_kind IN ('transfer', 'mint', 'burn')
      AND (($4 = 'erc20' AND standard = 'erc20') OR ($4 = 'nft' AND standard IN ('erc721', 'erc1155')))
)
SELECT event.block_number::text, event.block_hash, block.timestamp::text,
       event.transaction_hash, inclusion.tx_index::text,
       event.log_index::text, event.sub_index::text,
       event.token_address, event.standard, event.event_kind,
       event.from_address, event.to_address, event.token_id::text,
       event.amount::text, event.confidence, metadata.decimals
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
    SELECT CASE
               WHEN contract.standard = 'erc20' AND contract.metadata_state = 'complete'
               THEN contract.decimals
           END AS decimals
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
WHERE NOT $5::boolean OR (
    event.block_number,
    inclusion.tx_index,
    event.log_index,
    event.sub_index,
    event.block_hash,
    event.transaction_hash
) < (
    $6::numeric,
    $7::bigint,
    $8::bigint,
    $9::integer,
    $10::bytea,
    $11::bytea
)
ORDER BY event.block_number DESC, inclusion.tx_index DESC,
         event.log_index DESC, event.sub_index DESC,
         event.block_hash DESC, event.transaction_hash DESC
LIMIT $12;

-- name: CatalogAggregateStats :many
WITH selected_stats AS (
    SELECT stats.*
    FROM block_statistics AS stats
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = stats.chain_id
     AND canonical.number = stats.block_number
     AND canonical.block_hash = stats.block_hash
    WHERE stats.chain_id = $1::numeric
      AND stats.block_number BETWEEN $2::numeric AND $3::numeric
      AND stats.canonical = true
), selected_tokens AS (
    SELECT event.standard, event.event_kind
    FROM token_events AS event
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = event.chain_id
     AND canonical.number = event.block_number
     AND canonical.block_hash = event.block_hash
    WHERE event.chain_id = $1::numeric
      AND event.block_number BETWEEN $2::numeric AND $3::numeric
      AND event.canonical = true
)
SELECT count(*)::text,
       COALESCE(sum(transaction_count), 0)::text,
       COALESCE(sum(gas_used), 0)::text,
       COALESCE(sum(burned_wei), 0)::text,
       COALESCE(sum(blob_burned_wei), 0)::text,
       (SELECT count(*)::text FROM selected_tokens),
       (SELECT count(*)::text FROM selected_tokens
         WHERE standard = 'erc20' AND event_kind IN ('transfer', 'mint', 'burn')),
       (SELECT count(*)::text FROM selected_tokens
         WHERE standard IN ('erc721', 'erc1155') AND event_kind IN ('transfer', 'mint', 'burn')),
       CASE WHEN COALESCE(sum(block_interval_seconds) FILTER (
                     WHERE block_interval_seconds IS NOT NULL
                 ), 0) = 0 THEN NULL
            ELSE trim(trailing '.' FROM trim(trailing '0' FROM
                 round(
                     sum(transaction_count) FILTER (WHERE block_interval_seconds IS NOT NULL)
                     / sum(block_interval_seconds) FILTER (WHERE block_interval_seconds IS NOT NULL),
                     18
                 )::text))
       END
FROM selected_stats;

-- name: CatalogBlockStats :many
SELECT stats.chain_id::text, stats.block_number::text, stats.block_hash,
       stats.transaction_count::text, stats.gas_used::text, stats.gas_limit::text,
       stats.base_fee_per_gas::text, stats.blob_gas_used::text,
       stats.excess_blob_gas::text, stats.blob_base_fee_per_gas::text,
       stats.burned_wei::text, stats.blob_burned_wei::text,
       stats.block_timestamp::text, stats.block_interval_seconds::text,
       trim(trailing '.' FROM trim(trailing '0' FROM stats.transactions_per_second::text)),
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
WHERE stats.chain_id = $1::numeric
  AND stats.block_number BETWEEN $2::numeric AND $3::numeric
  AND stats.canonical = true
ORDER BY stats.block_number;

-- name: CatalogCanonicalSnapshot :many
SELECT number::text, block_hash
FROM canonical_blocks AS canonical
WHERE chain_id = $1::numeric
ORDER BY canonical.number DESC
LIMIT 1;

-- name: CatalogCanonicalTransactionInclusion :many
SELECT inclusion.block_number::text, inclusion.block_hash, inclusion.tx_index::text
FROM transaction_inclusions AS inclusion
JOIN canonical_blocks AS cb
  ON cb.chain_id = inclusion.chain_id
 AND cb.number = inclusion.block_number
 AND cb.block_hash = inclusion.block_hash
WHERE inclusion.chain_id = $1::numeric AND inclusion.tx_hash = $2
LIMIT 1;

-- name: CatalogErc20BalanceCandidates :many
SELECT d.token_address
FROM token_balance_deltas AS d
JOIN canonical_blocks AS cb
  ON cb.chain_id = d.chain_id
 AND cb.number = d.block_number
 AND cb.block_hash = d.block_hash
WHERE d.chain_id = $1::numeric
  AND d.block_number <= $2::numeric
  AND d.owner_address = $3
  AND d.token_id IS NULL
  AND d.canonical = TRUE
  AND ($4::boolean = FALSE OR d.token_address > $5)
GROUP BY d.token_address
ORDER BY d.token_address
LIMIT $6;

-- name: CatalogExactConstructorArtifact :many
SELECT verified.code_hash, verified.abi, verified.constructor_arguments,
       verified.valid_from_block::text, verified.valid_to_block::text
FROM contract_code_observations AS code
JOIN verified_contracts AS verified
  ON verified.chain_id = code.chain_id
 AND verified.address = code.address
 AND verified.code_hash = code.code_hash
 AND verified.valid_from_block <= $2::numeric
 AND (verified.valid_to_block IS NULL OR verified.valid_to_block >= $2::numeric)
JOIN verification_results AS result
  ON result.job_id = verified.verification_job_id
 AND result.request_digest = verified.request_digest
 AND result.outcome_kind = 'verification_success'
 AND result.outcome->'creation_match'->>'match_type' = 'full'
WHERE code.chain_id = $1::numeric
  AND code.block_number = $2::numeric
  AND code.block_hash = $3
  AND code.address = $4
  AND code.canonical
  AND verified.abi IS NOT NULL
ORDER BY verified.valid_from_block DESC
LIMIT 1;

-- name: CatalogFirstIncompleteStageInRange :many
WITH heights AS (
    SELECT generate_series($2::numeric, $3::numeric, 1::numeric) AS number
)
SELECT heights.number::text, cb.block_hash, latest.state
FROM heights
LEFT JOIN canonical_blocks AS cb
  ON cb.chain_id = $1::numeric AND cb.number = heights.number
LEFT JOIN LATERAL (
    SELECT result.state
    FROM published_block_stage_results AS result
    WHERE result.chain_id = cb.chain_id
      AND result.block_number = cb.number
      AND result.block_hash = cb.block_hash
      AND result.stage = $4
      AND result.stage_version = $5
) AS latest ON true
WHERE cb.block_hash IS NULL OR latest.state IS DISTINCT FROM 'complete'
ORDER BY heights.number
LIMIT 1;

-- name: CatalogLatestStage :many
SELECT state
FROM published_block_stage_results
WHERE chain_id = $1::numeric
  AND block_number = $2::numeric
  AND block_hash = $3
  AND stage = $4
  AND stage_version = $5;

-- name: CatalogNftBalanceCandidates :many
SELECT d.token_address, d.token_id::text
FROM token_balance_deltas AS d
JOIN canonical_blocks AS cb
  ON cb.chain_id = d.chain_id
 AND cb.number = d.block_number
 AND cb.block_hash = d.block_hash
WHERE d.chain_id = $1::numeric
  AND d.block_number <= $2::numeric
  AND d.owner_address = $3
  AND d.token_id IS NOT NULL
  AND d.canonical = true
  AND (
      $4::boolean = false OR
      (d.token_address, d.token_id) > ($5, $6::numeric)
  )
GROUP BY d.token_address, d.token_id
ORDER BY d.token_address, d.token_id
LIMIT $7;

-- name: CatalogTokenEvents :many
SELECT e.chain_id::text, e.block_number::text, e.block_hash,
       e.log_index::text, e.sub_index::text, e.transaction_hash,
       e.token_address, e.standard, e.event_kind, e.operator,
       e.from_address, e.to_address, e.token_id::text, e.amount::text,
       e.confidence, metadata.decimals
FROM token_events AS e
JOIN canonical_blocks AS cb
  ON cb.chain_id = e.chain_id
 AND cb.number = e.block_number
 AND cb.block_hash = e.block_hash
LEFT JOIN LATERAL (
    SELECT CASE
               WHEN contract.standard = 'erc20' AND contract.metadata_state = 'complete'
               THEN contract.decimals
           END AS decimals
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
WHERE e.chain_id = $1::numeric
  AND e.block_number <= $2::numeric
  AND e.token_address = $3
  AND e.canonical = true
  AND (
      $4::boolean = false OR
      (e.block_number, e.log_index, e.sub_index, e.block_hash) <
      ($5::numeric, $6::bigint, $7::integer, $8)
  )
ORDER BY e.block_number DESC, e.log_index DESC, e.sub_index DESC, e.block_hash DESC
LIMIT $9;

-- name: CatalogTraceStagePublication :many
SELECT state, durable_job_id, job_generation
FROM published_block_stage_results
WHERE chain_id = $1::numeric
  AND block_number = $2::numeric
  AND block_hash = $3
  AND stage = $4
  AND stage_version = $5;

-- name: CatalogTransactionAuthorizations :many
SELECT authorization_index, authorization_chain_id::text,
       authorization_nonce::text, delegate_address, y_parity, r, s,
       authority, signature_status, application_status, skip_reason
FROM eip7702_authorizations
WHERE chain_id = $1::numeric AND block_hash = $2 AND transaction_hash = $3
  AND canonical
ORDER BY authorization_index
LIMIT $4 OFFSET $5;

-- name: CatalogTransactionCalldataDecoding :many
SELECT decoding.status, decoding.signature, decoding.source, decoding.confidence,
       decoding.arguments, decoding.candidates, decoding.warning,
       decoding.target_address, decoding.target_code_hash,
       decoding.source_address, decoding.source_code_hash,
       decoding.return_status, decoding.return_arguments
FROM abi_decodings AS decoding
WHERE decoding.chain_id = $1::numeric
  AND decoding.block_hash = $2
  AND decoding.transaction_hash = $3
  AND decoding.object_kind = 'transaction_calldata'
  AND decoding.object_index = ''
  AND decoding.target_address = $4
  AND decoding.target_code_hash = $5
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

-- name: CatalogTransactionCalldataExecution :many
WITH published_abi AS (
    SELECT 1
    FROM published_block_stage_results AS published
    WHERE published.chain_id = $1::numeric
      AND published.block_number = $2::numeric
      AND published.block_hash = $3::bytea
      AND published.stage = 'abi'
      AND published.stage_version = 4
      AND published.state = 'complete'
), selected AS (
    SELECT effective.context_address, effective.execution_address,
           effective.execution_code_hash, effective.resolution,
           effective.evidence_source, 1 AS priority
    FROM transaction_effective_execution_identities AS effective
    WHERE effective.chain_id = $1::numeric
      AND effective.block_number = $2::numeric
      AND effective.block_hash = $3
      AND effective.transaction_hash = $4
      AND effective.context_address = $5
	  AND effective.transaction_index = $6
      AND effective.canonical
      AND EXISTS (SELECT 1 FROM published_abi)
    UNION ALL
    SELECT raw.context_address, raw.execution_address,
           raw.execution_code_hash, raw.resolution,
           raw.evidence_source, 2 AS priority
    FROM transaction_execution_code_resolutions AS raw
    WHERE raw.chain_id = $1::numeric
      AND raw.block_number = $2::numeric
      AND raw.block_hash = $3
      AND raw.transaction_hash = $4
      AND raw.context_address = $5
	  AND raw.transaction_index = $6
      AND raw.canonical
      AND NOT EXISTS (SELECT 1 FROM published_abi)
)
SELECT context_address, execution_address, execution_code_hash,
       resolution, evidence_source
FROM selected
ORDER BY priority
LIMIT 1;

-- name: CatalogTransactionCalldataIdentity :many
SELECT inclusion.block_number::text, inclusion.block_hash, inclusion.tx_index, inclusion.raw
FROM transaction_inclusions AS inclusion
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = inclusion.chain_id
 AND canonical.number = inclusion.block_number
 AND canonical.block_hash = inclusion.block_hash
WHERE inclusion.chain_id = $1::numeric AND inclusion.tx_hash = $2
LIMIT 1;

-- name: CatalogTransactionFailureReceiptStatus :many
SELECT receipt.raw->>'status'
FROM receipts AS receipt
WHERE receipt.chain_id = $1::numeric
  AND receipt.block_number = $2::numeric
  AND receipt.block_hash = $3
  AND receipt.tx_hash = $4
LIMIT 1;

-- name: CatalogTransactionFailureRoot :many
SELECT trace_path, parent_path, depth, call_type,
       from_address, to_address, created_address,
       value::text, gas::text, gas_used::text,
       input, output, error, direct_reverted, reverted,
       execution_address, execution_code_hash, execution_resolution
FROM normalized_traces
WHERE chain_id = $1::numeric
  AND block_number = $2::numeric
  AND block_hash = $3
  AND transaction_hash = $4
  AND trace_path = ''
  AND depth = 0
  AND canonical = true
LIMIT 1;

-- name: CatalogTransactionInternalTransactions :many
SELECT trace.trace_path, trace.depth, trace.call_type,
       trace.from_address, trace.to_address, trace.created_address,
       trace.value::text
FROM normalized_traces AS trace
WHERE trace.chain_id = $1::numeric
  AND trace.block_hash = $2
  AND trace.transaction_hash = $3
  AND trace.canonical = true
  AND trace.depth > 0
  AND trace.value > 0
  AND trace.reverted = false
ORDER BY string_to_array(trace.trace_path, '.')::bigint[]
LIMIT $4 OFFSET $5;

-- name: CatalogTransactionLogABICandidates :many
WITH target_code AS (
    SELECT $5::bytea AS code_hash
    WHERE $5::bytea IS NOT NULL
    UNION ALL
    (
        SELECT observation.code_hash
        FROM contract_code_observations AS observation
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = observation.chain_id
         AND canonical.number = observation.block_number
         AND canonical.block_hash = observation.block_hash
        WHERE observation.chain_id = $1::numeric
          AND observation.address = $2::bytea
          AND observation.block_number <= $3::numeric
          AND observation.canonical
          AND $5::bytea IS NULL
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
    WHERE observation.chain_id = $1::numeric
      AND observation.proxy_address = $2
      AND observation.proxy_code_hash = target_code.code_hash
      AND observation.block_number <= $3::numeric
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
    WHERE binding.chain_id = $1::numeric
      AND binding.address = $2
      AND binding.code_hash = target_code.code_hash
      AND binding.valid_from_block <= $3::numeric
      AND (binding.valid_to_block IS NULL OR binding.valid_to_block >= $3::numeric)
      AND binding.canonical
    UNION ALL
    SELECT target_code.code_hash, verified.abi,
           CASE WHEN verified.address = $2
                     AND verified.valid_from_block <= $3::numeric
                     AND (verified.valid_to_block IS NULL OR verified.valid_to_block >= $3::numeric)
                THEN 'verified' ELSE 'code_hash' END,
           CASE WHEN verified.address = $2
                     AND verified.valid_from_block <= $3::numeric
                     AND (verified.valid_to_block IS NULL OR verified.valid_to_block >= $3::numeric)
                THEN 'exact_address' ELSE 'code_hash' END,
	           verified.address, verified.code_hash,
	           decode(repeat('00', 32), 'hex'),
           0::numeric, NULL::numeric,
           CASE WHEN verified.address = $2
                     AND verified.valid_from_block <= $3::numeric
                     AND (verified.valid_to_block IS NULL OR verified.valid_to_block >= $3::numeric)
                THEN 1 ELSE 3 END,
           verified.created_at, verified.request_digest, verified.verification_job_id
    FROM verified_contracts AS verified, target_code
    WHERE verified.chain_id = $1::numeric
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
    WHERE verified.chain_id = $1::numeric
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
       valid_from_block::text, valid_to_block::text
FROM candidates
ORDER BY priority, created_at DESC, request_digest ASC NULLS FIRST,
         job_id ASC NULLS FIRST, source_address, source_code_hash
LIMIT $4;

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
WHERE log.chain_id = $1::numeric AND log.block_hash = $2 AND log.tx_hash = $3
ORDER BY log.log_index
LIMIT $4 OFFSET $5;

-- name: CatalogTransactionResourceIdentity :many
SELECT inclusion.block_number::text, inclusion.block_hash, inclusion.tx_index,
       (canonical.block_hash IS NOT NULL)
FROM transaction_inclusions AS inclusion
LEFT JOIN canonical_blocks AS canonical
  ON canonical.chain_id = inclusion.chain_id
 AND canonical.number = inclusion.block_number
 AND canonical.block_hash = inclusion.block_hash
WHERE inclusion.chain_id = $1::numeric AND inclusion.tx_hash = $2
ORDER BY (canonical.block_hash IS NOT NULL) DESC, inclusion.block_number DESC
LIMIT 1;

-- name: CatalogTransactionStageState :many
SELECT state, job_generation
FROM published_block_stage_results
WHERE chain_id = $1::numeric
  AND block_number = $2::numeric
  AND block_hash = $3
  AND stage = $4
  AND stage_version = $5;

-- name: CatalogTransactionStateChanges :many
SELECT address, field_kind, storage_key, before_value, after_value
FROM transaction_state_changes
WHERE chain_id = $1::numeric
  AND block_hash = $2
  AND transaction_hash = $3
  AND canonical = true
ORDER BY address, field_kind, storage_key
LIMIT $4 OFFSET $5;

-- name: CatalogTransactionTokenEvents :many
SELECT event.chain_id::text, event.block_number::text, event.block_hash,
       event.log_index::text, event.sub_index::text, event.transaction_hash,
       event.token_address, event.standard, event.event_kind, event.operator,
       event.from_address, event.to_address, event.token_id::text, event.amount::text,
       event.confidence, metadata.decimals
FROM token_events AS event
LEFT JOIN LATERAL (
    SELECT CASE
               WHEN contract.standard = 'erc20' AND contract.metadata_state = 'complete'
               THEN contract.decimals
           END AS decimals
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
WHERE event.chain_id = $1::numeric
  AND event.block_hash = $2
  AND event.transaction_hash = $3
  AND event.canonical = true
ORDER BY event.log_index, event.sub_index
LIMIT $4 OFFSET $5;

-- name: CatalogTransactionTrace :many
SELECT trace_path, parent_path, depth, call_type,
       from_address, to_address, created_address,
       value::text, gas::text, gas_used::text,
       input, output, error, direct_reverted, reverted,
       execution_address, execution_code_hash, execution_resolution
FROM normalized_traces
WHERE chain_id = $1::numeric
  AND block_number = $2::numeric
  AND block_hash = $3
  AND transaction_hash = $4
  AND canonical = true
ORDER BY depth, trace_path
LIMIT $5;

-- name: CatalogTransactionTraceDecodings :many
SELECT decoding.object_kind, decoding.object_index, decoding.status,
       decoding.signature, decoding.source, decoding.confidence,
       decoding.arguments, decoding.candidates, decoding.warning,
       decoding.target_address, decoding.target_code_hash,
       decoding.source_address, decoding.source_code_hash,
       decoding.return_status, decoding.return_arguments
FROM abi_decodings AS decoding
WHERE decoding.chain_id = $1::numeric
  AND decoding.block_hash = $2
  AND decoding.transaction_hash = $3
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
WHERE chain_id = $1::numeric
  AND block_number = $2::numeric
  AND block_hash = $3
  AND transaction_hash = $4
  AND canonical
ORDER BY depth, trace_path
LIMIT $5;

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
WHERE indexed.chain_id = $1::numeric
  AND indexed.address = $2
  AND indexed.status = 'complete'
  AND indexed.valid_from_block <= $3::numeric
  AND (verified.valid_to_block IS NULL OR verified.valid_to_block >= $3::numeric)
  AND selector.selector = $4
ORDER BY selector.signature, indexed.code_hash, indexed.verification_job_id
LIMIT $5;

-- name: CatalogValidateCanonicalSnapshot :many
SELECT EXISTS (
    SELECT 1
    FROM canonical_blocks
    WHERE chain_id = $1::numeric AND number = $2::numeric AND block_hash = $3
);
