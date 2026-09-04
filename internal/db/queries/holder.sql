-- name: HolderSourcePrerequisites :one
SELECT configuration.configured_start::text,
       EXISTS (
           SELECT 1 FROM canonical_blocks AS canonical
           WHERE canonical.chain_id = sqlc.arg(chain_id)::numeric
             AND canonical.number = sqlc.arg(block_number)::numeric
             AND canonical.block_hash = sqlc.arg(block_hash)::bytea
       ) AS canonical,
       EXISTS (
           SELECT 1 FROM published_block_stage_results AS published
           WHERE published.chain_id = sqlc.arg(chain_id)::numeric
             AND published.block_number = sqlc.arg(block_number)::numeric
             AND published.block_hash = sqlc.arg(block_hash)::bytea
             AND published.stage = 'token' AND published.stage_version = 1
             AND published.state = 'complete'
       ) AS token_complete,
       EXISTS (
           SELECT 1 FROM published_block_stage_results AS published
           WHERE published.chain_id = sqlc.arg(chain_id)::numeric
             AND published.block_number = sqlc.arg(block_number)::numeric
             AND published.block_hash = sqlc.arg(block_hash)::bytea
             AND published.stage = 'proxy' AND published.stage_version = 2
             AND published.state IN ('complete', 'unavailable')
       ) AS proxy_terminal
FROM core_index_configuration AS configuration
WHERE configuration.chain_id = sqlc.arg(chain_id)::numeric;

-- name: HolderAffectedTokens :many
WITH event_tokens AS (
    SELECT DISTINCT event.token_address
    FROM token_events AS event
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = event.chain_id
     AND canonical.number = event.block_number
     AND canonical.block_hash = event.block_hash
    WHERE event.chain_id = sqlc.arg(chain_id)::numeric
      AND event.block_number = sqlc.arg(block_number)::numeric
      AND event.block_hash = sqlc.arg(block_hash)::bytea
      AND event.canonical AND event.standard = 'erc20'
      AND event.event_kind IN ('transfer', 'mint', 'burn')
      AND event.confidence IN ('high', 'verified')
), latest_tokens AS (
    SELECT DISTINCT ON (token.address)
           token.address AS token_address, token.standard, token.confidence
    FROM token_contracts AS token
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = token.chain_id
     AND canonical.number = token.observed_block_number
     AND canonical.block_hash = token.observed_block_hash
    WHERE token.chain_id = sqlc.arg(chain_id)::numeric
      AND token.observed_block_number <= sqlc.arg(block_number)::numeric
    ORDER BY token.address, token.observed_block_number DESC,
             token.updated_at DESC, token.code_hash DESC
), eligible_tokens AS (
    SELECT token_address
    FROM latest_tokens
    WHERE standard = 'erc20' AND confidence IN ('high', 'verified')
), latest_snapshots AS (
    SELECT token_address, max(block_number) AS block_number
    FROM erc20_holder_snapshots
    WHERE chain_id = sqlc.arg(chain_id)::numeric AND canonical
    GROUP BY token_address
), generation_tokens AS (
    SELECT eligible.token_address
    FROM eligible_tokens AS eligible
    JOIN contract_code_observations AS code
      ON code.chain_id = sqlc.arg(chain_id)::numeric
     AND code.address = eligible.token_address
     AND code.block_number = sqlc.arg(block_number)::numeric
     AND code.block_hash = sqlc.arg(block_hash)::bytea
     AND code.canonical
    UNION
    SELECT eligible.token_address
    FROM eligible_tokens AS eligible
    JOIN proxy_upgrade_events AS upgrade
      ON upgrade.chain_id = sqlc.arg(chain_id)::numeric
     AND upgrade.emitter_address = eligible.token_address
     AND upgrade.block_number = sqlc.arg(block_number)::numeric
     AND upgrade.block_hash = sqlc.arg(block_hash)::bytea
     AND upgrade.canonical AND upgrade.stage_version = 2
), audit_token AS (
    SELECT eligible.token_address
    FROM eligible_tokens AS eligible
    LEFT JOIN latest_snapshots AS snapshot
      ON snapshot.token_address = eligible.token_address
    WHERE NOT EXISTS (
        SELECT 1 FROM event_tokens WHERE event_tokens.token_address = eligible.token_address
    ) AND NOT EXISTS (
        SELECT 1 FROM generation_tokens WHERE generation_tokens.token_address = eligible.token_address
    )
    ORDER BY snapshot.block_number ASC NULLS FIRST, eligible.token_address
    LIMIT 1
)
SELECT token_address, bool_or(full_reconciliation) AS full_reconciliation
FROM (
    SELECT token_address, FALSE AS full_reconciliation FROM event_tokens
    UNION ALL
    SELECT token_address, TRUE AS full_reconciliation FROM generation_tokens
    UNION ALL
    SELECT token_address, TRUE AS full_reconciliation FROM audit_token
) AS work
GROUP BY token_address
ORDER BY token_address;

-- name: HolderTokenIdentity :one
SELECT token.standard, token.confidence
FROM token_contracts AS token
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = token.chain_id
 AND canonical.number = token.observed_block_number
 AND canonical.block_hash = token.observed_block_hash
WHERE token.chain_id = sqlc.arg(chain_id)::numeric
  AND token.address = sqlc.arg(token_address)::bytea
  AND token.observed_block_number <= sqlc.arg(block_number)::numeric
ORDER BY token.observed_block_number DESC, token.updated_at DESC, token.code_hash DESC
LIMIT 1;

-- name: HolderCandidates :many
SELECT candidate.holder_address
FROM (
    SELECT event.from_address AS holder_address
    FROM token_events AS event
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = event.chain_id
     AND canonical.number = event.block_number
     AND canonical.block_hash = event.block_hash
    WHERE event.chain_id = sqlc.arg(chain_id)::numeric
      AND event.token_address = sqlc.arg(token_address)::bytea
      AND event.block_number <= sqlc.arg(block_number)::numeric
      AND event.canonical AND event.standard = 'erc20'
      AND event.event_kind IN ('transfer', 'mint', 'burn')
      AND event.confidence IN ('high', 'verified')
      AND event.from_address IS NOT NULL
      AND event.from_address <> decode(repeat('00', 20), 'hex')
    UNION
    SELECT event.to_address AS holder_address
    FROM token_events AS event
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = event.chain_id
     AND canonical.number = event.block_number
     AND canonical.block_hash = event.block_hash
    WHERE event.chain_id = sqlc.arg(chain_id)::numeric
      AND event.token_address = sqlc.arg(token_address)::bytea
      AND event.block_number <= sqlc.arg(block_number)::numeric
      AND event.canonical AND event.standard = 'erc20'
      AND event.event_kind IN ('transfer', 'mint', 'burn')
      AND event.confidence IN ('high', 'verified')
      AND event.to_address IS NOT NULL
      AND event.to_address <> decode(repeat('00', 20), 'hex')
) AS candidate
ORDER BY candidate.holder_address;

-- name: HolderTouchedCandidates :many
SELECT candidate.holder_address
FROM (
    SELECT event.from_address AS holder_address
    FROM token_events AS event
    WHERE event.chain_id = sqlc.arg(chain_id)::numeric
      AND event.token_address = sqlc.arg(token_address)::bytea
      AND event.block_number = sqlc.arg(block_number)::numeric
      AND event.block_hash = sqlc.arg(block_hash)::bytea
      AND event.canonical AND event.standard = 'erc20'
      AND event.event_kind IN ('transfer', 'mint', 'burn')
      AND event.confidence IN ('high', 'verified')
      AND event.from_address IS NOT NULL
      AND event.from_address <> decode(repeat('00', 20), 'hex')
    UNION
    SELECT event.to_address AS holder_address
    FROM token_events AS event
    WHERE event.chain_id = sqlc.arg(chain_id)::numeric
      AND event.token_address = sqlc.arg(token_address)::bytea
      AND event.block_number = sqlc.arg(block_number)::numeric
      AND event.block_hash = sqlc.arg(block_hash)::bytea
      AND event.canonical AND event.standard = 'erc20'
      AND event.event_kind IN ('transfer', 'mint', 'burn')
      AND event.confidence IN ('high', 'verified')
      AND event.to_address IS NOT NULL
      AND event.to_address <> decode(repeat('00', 20), 'hex')
) AS candidate
ORDER BY candidate.holder_address;

-- name: HolderPreviousSnapshot :one
SELECT snapshot.block_number::text, snapshot.state,
       snapshot.holder_count::text, snapshot.total_supply::text,
       snapshot.reconciled_balance_sum::text
FROM erc20_holder_snapshots AS snapshot
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = snapshot.chain_id
 AND canonical.number = snapshot.block_number
 AND canonical.block_hash = snapshot.block_hash
WHERE snapshot.chain_id = sqlc.arg(chain_id)::numeric
  AND snapshot.token_address = sqlc.arg(token_address)::bytea
  AND snapshot.block_number < sqlc.arg(block_number)::numeric
  AND snapshot.canonical
ORDER BY snapshot.block_number DESC
LIMIT 1;

-- name: HolderHasUnreconciledEvents :one
SELECT EXISTS (
    SELECT 1
    FROM token_events AS event
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = event.chain_id
     AND canonical.number = event.block_number
     AND canonical.block_hash = event.block_hash
    WHERE event.chain_id = sqlc.arg(chain_id)::numeric
      AND event.token_address = sqlc.arg(token_address)::bytea
      AND event.block_number > sqlc.arg(previous_block)::numeric
      AND event.block_number < sqlc.arg(block_number)::numeric
      AND event.canonical AND event.standard = 'erc20'
      AND event.event_kind IN ('transfer', 'mint', 'burn')
      AND event.confidence IN ('high', 'verified')
      AND NOT EXISTS (
          SELECT 1
          FROM erc20_holder_snapshots AS snapshot
          WHERE snapshot.chain_id = event.chain_id
            AND snapshot.token_address = event.token_address
            AND snapshot.block_number = event.block_number
            AND snapshot.block_hash = event.block_hash
            AND snapshot.canonical
      )
);

-- name: HolderPreviousBalance :one
SELECT balance.balance::text
FROM erc20_holder_balances AS balance
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = balance.chain_id
 AND canonical.number = balance.block_number
 AND canonical.block_hash = balance.block_hash
WHERE balance.chain_id = sqlc.arg(chain_id)::numeric
  AND balance.token_address = sqlc.arg(token_address)::bytea
  AND balance.holder_address = sqlc.arg(holder_address)::bytea
  AND balance.block_number <= sqlc.arg(block_number)::numeric
  AND balance.canonical
ORDER BY balance.block_number DESC
LIMIT 1;

-- name: HolderEventSupply :one
SELECT COALESCE(sum(CASE event.event_kind
    WHEN 'mint' THEN event.amount WHEN 'burn' THEN -event.amount ELSE 0 END), 0)::text
FROM token_events AS event
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = event.chain_id
 AND canonical.number = event.block_number
 AND canonical.block_hash = event.block_hash
WHERE event.chain_id = sqlc.arg(chain_id)::numeric
  AND event.token_address = sqlc.arg(token_address)::bytea
  AND event.block_number <= sqlc.arg(block_number)::numeric
  AND event.canonical AND event.standard = 'erc20'
  AND event.event_kind IN ('mint', 'burn')
  AND event.confidence IN ('high', 'verified');

-- name: HolderDeleteBlockOutput :exec
WITH delete_balances AS (
    DELETE FROM erc20_holder_balances
    WHERE chain_id = sqlc.arg(chain_id)::numeric
      AND block_number = sqlc.arg(block_number)::numeric
      AND block_hash = sqlc.arg(block_hash)::bytea
)
DELETE FROM erc20_holder_snapshots
WHERE chain_id = sqlc.arg(chain_id)::numeric
  AND block_number = sqlc.arg(block_number)::numeric
  AND block_hash = sqlc.arg(block_hash)::bytea;

-- name: HolderInsertSnapshot :exec
INSERT INTO erc20_holder_snapshots (
    chain_id, token_address, block_number, block_hash, state,
    holder_count, total_supply, reconciled_balance_sum, canonical
) VALUES (
    sqlc.arg(chain_id)::numeric, sqlc.arg(token_address)::bytea,
    sqlc.arg(block_number)::numeric, sqlc.arg(block_hash)::bytea,
    sqlc.arg(state)::text, sqlc.arg(holder_count)::numeric,
    sqlc.arg(total_supply)::numeric, sqlc.arg(reconciled_balance_sum)::numeric, TRUE
)
ON CONFLICT (chain_id, token_address, block_number, block_hash) DO UPDATE SET canonical = TRUE
WHERE erc20_holder_snapshots.state = EXCLUDED.state
  AND erc20_holder_snapshots.holder_count = EXCLUDED.holder_count
  AND erc20_holder_snapshots.total_supply = EXCLUDED.total_supply
  AND erc20_holder_snapshots.reconciled_balance_sum = EXCLUDED.reconciled_balance_sum;

-- name: HolderInsertBalance :exec
INSERT INTO erc20_holder_balances (
    chain_id, token_address, holder_address, block_number, block_hash,
    balance, confidence, canonical
) VALUES (
    sqlc.arg(chain_id)::numeric, sqlc.arg(token_address)::bytea,
    sqlc.arg(holder_address)::bytea, sqlc.arg(block_number)::numeric,
    sqlc.arg(block_hash)::bytea, sqlc.arg(balance)::numeric, 'rpc_exact', TRUE
)
ON CONFLICT (chain_id, token_address, block_number, block_hash, holder_address)
DO UPDATE SET canonical = TRUE
WHERE erc20_holder_balances.balance = EXCLUDED.balance
  AND erc20_holder_balances.confidence = EXCLUDED.confidence;

-- name: CatalogHolderCoverage :one
SELECT configuration.configured_start::text,
       count(published.block_number)::text AS covered_blocks,
       count(token_publication.block_number)::text AS token_blocks,
       count(proxy_publication.block_number)::text AS proxy_blocks,
       COALESCE(sum(published.job_generation), 0)::text AS publication_epoch
FROM core_index_configuration AS configuration
LEFT JOIN canonical_blocks AS canonical
  ON canonical.chain_id = configuration.chain_id
 AND canonical.number BETWEEN 0 AND sqlc.arg(block_number)::numeric
LEFT JOIN published_block_stage_results AS published
  ON published.chain_id = canonical.chain_id
 AND published.block_number = canonical.number
 AND published.block_hash = canonical.block_hash
 AND published.stage = 'holder'
 AND published.stage_version = 1
 AND published.state = 'complete'
LEFT JOIN published_block_stage_results AS token_publication
  ON token_publication.chain_id = canonical.chain_id
 AND token_publication.block_number = canonical.number
 AND token_publication.block_hash = canonical.block_hash
 AND token_publication.stage = 'token'
 AND token_publication.stage_version = 1
 AND token_publication.state = 'complete'
LEFT JOIN published_block_stage_results AS proxy_publication
  ON proxy_publication.chain_id = canonical.chain_id
 AND proxy_publication.block_number = canonical.number
 AND proxy_publication.block_hash = canonical.block_hash
 AND proxy_publication.stage = 'proxy'
 AND proxy_publication.stage_version = 2
 AND proxy_publication.state IN ('complete', 'unavailable')
WHERE configuration.chain_id = sqlc.arg(chain_id)::numeric
GROUP BY configuration.configured_start;

-- name: CatalogHolderTokenSnapshot :one
SELECT snapshot.block_number::text, snapshot.block_hash, snapshot.state,
       snapshot.holder_count::text, snapshot.total_supply::text,
       snapshot.reconciled_balance_sum::text,
       EXISTS (
           SELECT 1
           FROM published_block_stage_results AS holder_publication
           WHERE holder_publication.chain_id = snapshot.chain_id
             AND holder_publication.block_number = snapshot.block_number
             AND holder_publication.block_hash = snapshot.block_hash
             AND holder_publication.stage = 'holder'
             AND holder_publication.stage_version = 1
             AND holder_publication.state = 'complete'
             AND NOT EXISTS (
                 SELECT 1
                 FROM published_block_stage_results AS source_publication
                 JOIN canonical_blocks AS source_canonical
                   ON source_canonical.chain_id = source_publication.chain_id
                  AND source_canonical.number = source_publication.block_number
                  AND source_canonical.block_hash = source_publication.block_hash
                 WHERE source_publication.chain_id = snapshot.chain_id
                   AND source_publication.block_number <= snapshot.block_number
                   AND (
                       (source_publication.stage = 'token' AND source_publication.stage_version = 1 AND source_publication.state = 'complete') OR
                       (source_publication.stage = 'proxy' AND source_publication.stage_version = 2 AND source_publication.state IN ('complete', 'unavailable'))
                   )
                   AND source_publication.completed_at > holder_publication.completed_at
             )
       ) AS coherent
FROM erc20_holder_snapshots AS snapshot
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = snapshot.chain_id
 AND canonical.number = snapshot.block_number
 AND canonical.block_hash = snapshot.block_hash
WHERE snapshot.chain_id = sqlc.arg(chain_id)::numeric
  AND snapshot.token_address = sqlc.arg(token_address)::bytea
  AND snapshot.block_number <= sqlc.arg(block_number)::numeric
  AND snapshot.canonical
ORDER BY snapshot.block_number DESC
LIMIT 1;

-- name: CatalogHolderPage :many
WITH latest AS (
    SELECT DISTINCT ON (balance.holder_address)
           balance.holder_address, balance.balance,
           balance.block_number, balance.block_hash, balance.confidence
    FROM erc20_holder_balances AS balance
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = balance.chain_id
     AND canonical.number = balance.block_number
     AND canonical.block_hash = balance.block_hash
    WHERE balance.chain_id = sqlc.arg(chain_id)::numeric
      AND balance.token_address = sqlc.arg(token_address)::bytea
      AND balance.block_number <= sqlc.arg(block_number)::numeric
      AND balance.canonical
      AND (NOT sqlc.arg(has_after)::boolean OR balance.holder_address > sqlc.arg(after_address)::bytea)
    ORDER BY balance.holder_address, balance.block_number DESC
)
SELECT latest.holder_address, latest.balance::text,
       latest.block_number::text, latest.block_hash, latest.confidence
FROM latest
WHERE latest.balance > 0
ORDER BY latest.holder_address
LIMIT sqlc.arg(row_limit)::bigint;

-- name: EtherscanHolderPage :many
WITH latest AS (
    SELECT DISTINCT ON (balance.holder_address)
           balance.holder_address, balance.balance
    FROM erc20_holder_balances AS balance
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = balance.chain_id
     AND canonical.number = balance.block_number
     AND canonical.block_hash = balance.block_hash
    WHERE balance.chain_id = sqlc.arg(chain_id)::numeric
      AND balance.token_address = sqlc.arg(token_address)::bytea
      AND balance.block_number <= sqlc.arg(block_number)::numeric
      AND balance.canonical
    ORDER BY balance.holder_address, balance.block_number DESC
)
SELECT latest.holder_address, latest.balance::text
FROM latest
WHERE latest.balance > 0
ORDER BY latest.holder_address
LIMIT sqlc.arg(row_limit)::bigint OFFSET sqlc.arg(row_offset)::bigint;
