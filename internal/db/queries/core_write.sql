-- name: StorePutBlocksBatch :exec
WITH decoded AS (
    SELECT item.ordinality,
           (item.value->>'number')::numeric AS number,
           decode(item.value->>'hash', 'hex') AS hash,
           decode(item.value->>'parent_hash', 'hex') AS parent_hash,
           (item.value->>'timestamp')::numeric AS timestamp,
           item.value->'raw' AS raw
    FROM jsonb_array_elements($2::jsonb) WITH ORDINALITY AS item(value, ordinality)
), input AS (
    SELECT DISTINCT ON (number, hash)
           number, hash, parent_hash, timestamp, raw
    FROM decoded
    ORDER BY number, hash, ordinality DESC
)
INSERT INTO blocks (chain_id, number, hash, parent_hash, timestamp, raw)
SELECT $1::numeric, number, hash, parent_hash, timestamp, raw
FROM input
ON CONFLICT (chain_id, number, hash) DO UPDATE SET
    parent_hash = EXCLUDED.parent_hash,
    timestamp = EXCLUDED.timestamp,
    raw = EXCLUDED.raw;

-- name: StorePutTransactionsBatch :exec
WITH decoded AS (
    SELECT item.ordinality,
           decode(item.value->>'hash', 'hex') AS hash,
           (item.value->>'tx_type')::numeric AS tx_type,
           item.value->'raw' AS raw
    FROM jsonb_array_elements($2::jsonb) WITH ORDINALITY AS item(value, ordinality)
), input AS (
    SELECT DISTINCT ON (hash) hash, tx_type, raw
    FROM decoded
    ORDER BY hash, ordinality DESC
)
INSERT INTO transactions (chain_id, hash, tx_type, raw)
SELECT $1::numeric, hash, tx_type, raw
FROM input
ON CONFLICT (chain_id, hash) DO UPDATE SET
    tx_type = EXCLUDED.tx_type,
    raw = EXCLUDED.raw;

-- name: StorePutTransactionInclusionsBatch :exec
WITH decoded AS (
    SELECT item.ordinality,
           (item.value->>'block_number')::numeric AS block_number,
           decode(item.value->>'block_hash', 'hex') AS block_hash,
           (item.value->>'tx_index')::bigint AS tx_index,
           decode(item.value->>'tx_hash', 'hex') AS tx_hash,
           item.value->'raw' AS raw
    FROM jsonb_array_elements($2::jsonb) WITH ORDINALITY AS item(value, ordinality)
), input AS (
    SELECT DISTINCT ON (block_number, block_hash, tx_index)
           block_number, block_hash, tx_index, tx_hash, raw
    FROM decoded
    ORDER BY block_number, block_hash, tx_index, ordinality DESC
)
INSERT INTO transaction_inclusions (
    chain_id, block_number, block_hash, tx_index, tx_hash, raw
)
SELECT $1::numeric, block_number, block_hash, tx_index, tx_hash, raw
FROM input
ON CONFLICT (chain_id, block_number, block_hash, tx_index)
DO UPDATE SET raw = EXCLUDED.raw;

-- name: StorePutReceiptsBatch :exec
WITH decoded AS (
    SELECT item.ordinality,
           (item.value->>'block_number')::numeric AS block_number,
           decode(item.value->>'block_hash', 'hex') AS block_hash,
           (item.value->>'tx_index')::bigint AS tx_index,
           decode(item.value->>'tx_hash', 'hex') AS tx_hash,
           item.value->'raw' AS raw
    FROM jsonb_array_elements($2::jsonb) WITH ORDINALITY AS item(value, ordinality)
), input AS (
    SELECT DISTINCT ON (block_number, block_hash, tx_index)
           block_number, block_hash, tx_index, tx_hash, raw
    FROM decoded
    ORDER BY block_number, block_hash, tx_index, ordinality DESC
)
INSERT INTO receipts (
    chain_id, block_number, block_hash, tx_index, tx_hash, raw
)
SELECT $1::numeric, block_number, block_hash, tx_index, tx_hash, raw
FROM input
ON CONFLICT (chain_id, block_number, block_hash, tx_index)
DO UPDATE SET raw = EXCLUDED.raw;

-- name: StorePutLogsBatch :exec
WITH decoded AS (
    SELECT item.ordinality,
           (item.value->>'block_number')::numeric AS block_number,
           decode(item.value->>'block_hash', 'hex') AS block_hash,
           (item.value->>'log_index')::bigint AS log_index,
           (item.value->>'tx_index')::bigint AS tx_index,
           decode(item.value->>'tx_hash', 'hex') AS tx_hash,
           decode(item.value->>'address', 'hex') AS address,
           CASE WHEN item.value->'topic0' = 'null'::jsonb
                THEN NULL ELSE decode(item.value->>'topic0', 'hex') END AS topic0,
           item.value->'raw' AS raw
    FROM jsonb_array_elements($2::jsonb) WITH ORDINALITY AS item(value, ordinality)
), input AS (
    SELECT DISTINCT ON (block_number, block_hash, log_index)
           block_number, block_hash, log_index, tx_index, tx_hash, address, topic0, raw
    FROM decoded
    ORDER BY block_number, block_hash, log_index, ordinality DESC
)
INSERT INTO logs (
    chain_id, block_number, block_hash, log_index, tx_index,
    tx_hash, address, topic0, raw
)
SELECT $1::numeric, block_number, block_hash, log_index, tx_index,
       tx_hash, address, topic0, raw
FROM input
ON CONFLICT (chain_id, block_number, block_hash, log_index)
DO UPDATE SET raw = EXCLUDED.raw;

-- name: StorePutWithdrawalsBatch :exec
WITH decoded AS (
    SELECT item.ordinality,
           (item.value->>'block_number')::numeric AS block_number,
           decode(item.value->>'block_hash', 'hex') AS block_hash,
           (item.value->>'withdrawal_index')::numeric AS withdrawal_index,
           (item.value->>'validator_index')::numeric AS validator_index,
           decode(item.value->>'address', 'hex') AS address,
           (item.value->>'amount')::numeric AS amount,
           item.value->'raw' AS raw
    FROM jsonb_array_elements($2::jsonb) WITH ORDINALITY AS item(value, ordinality)
), input AS (
    SELECT DISTINCT ON (block_number, block_hash, withdrawal_index)
           block_number, block_hash, withdrawal_index, validator_index, address, amount, raw
    FROM decoded
    ORDER BY block_number, block_hash, withdrawal_index, ordinality DESC
)
INSERT INTO withdrawals (
    chain_id, block_number, block_hash, withdrawal_index,
    validator_index, address, amount, raw
)
SELECT $1::numeric, block_number, block_hash, withdrawal_index,
       validator_index, address, amount, raw
FROM input
ON CONFLICT (chain_id, block_number, block_hash, withdrawal_index)
DO UPDATE SET raw = EXCLUDED.raw;

-- name: StoreInsertCanonicalBlocksBatch :exec
INSERT INTO canonical_blocks (chain_id, number, block_hash)
SELECT $1::numeric, row.number::numeric, decode(row.hash, 'hex')
FROM jsonb_to_recordset($2::jsonb) AS row(number text, hash text)
ORDER BY row.number::numeric;

-- name: StoreDeleteCanonicalBlocksBatch :execrows
WITH input AS (
    SELECT row.number::numeric AS number, decode(row.hash, 'hex') AS hash
    FROM jsonb_to_recordset($2::jsonb) AS row(number text, hash text)
)
DELETE FROM canonical_blocks AS canonical
USING input
WHERE canonical.chain_id = $1::numeric
  AND canonical.number = input.number
  AND canonical.block_hash = input.hash;

-- name: StoreSetBlockJournalsCanonicalBatch :exec
WITH input AS (
    SELECT decode(value, 'hex') AS hash
    FROM jsonb_array_elements_text($2::jsonb)
)
UPDATE block_journals AS journal
SET canonical = $3::boolean
FROM input
WHERE journal.chain_id = $1::numeric
  AND journal.block_hash = input.hash;

-- name: StoreSetDerivedCanonicalBatch :exec
WITH input AS (
    SELECT decode(value, 'hex') AS hash
    FROM jsonb_array_elements_text($2::jsonb)
), update_contract_code AS (
    UPDATE contract_code_observations AS target SET canonical = $3
    FROM input WHERE target.chain_id = $1::numeric AND target.block_hash = input.hash
), update_proxy AS (
    UPDATE proxy_observations AS target SET canonical = $3
    FROM input WHERE target.chain_id = $1::numeric AND target.block_hash = input.hash
), update_beacon AS (
    UPDATE beacon_implementation_observations AS target SET canonical = $3
    FROM input WHERE target.chain_id = $1::numeric AND target.block_hash = input.hash
), update_uups AS (
    UPDATE uups_implementation_observations AS target SET canonical = $3
    FROM input WHERE target.chain_id = $1::numeric AND target.block_hash = input.hash
), update_detection AS (
    UPDATE proxy_detection_evidence AS target SET canonical = $3
    FROM input WHERE target.chain_id = $1::numeric AND target.block_hash = input.hash
), update_proxy_upgrades AS (
    UPDATE proxy_upgrade_events AS target SET canonical = $3
    FROM input WHERE target.chain_id = $1::numeric AND target.block_hash = input.hash
), update_proxy_initializations AS (
    UPDATE proxy_initialization_events AS target SET canonical = $3
    FROM input WHERE target.chain_id = $1::numeric AND target.block_hash = input.hash
), update_diamond_snapshots AS (
    UPDATE diamond_loupe_snapshots AS target SET canonical = $3
    FROM input WHERE target.chain_id = $1::numeric AND target.block_hash = input.hash
), update_diamond_cuts AS (
    UPDATE diamond_cut_events AS target SET canonical = $3
    FROM input WHERE target.chain_id = $1::numeric AND target.block_hash = input.hash
), update_contract_abis AS (
    UPDATE contract_abis AS target SET canonical = $3
    FROM input WHERE target.chain_id = $1::numeric AND target.block_hash = input.hash
), update_abi_decodings AS (
    UPDATE abi_decodings AS target SET canonical = $3
    FROM input WHERE target.chain_id = $1::numeric AND target.block_hash = input.hash
), update_effective_identities AS (
    UPDATE transaction_effective_execution_identities AS target SET canonical = $3
    FROM input WHERE target.chain_id = $1::numeric AND target.block_hash = input.hash
), update_token_events AS (
    UPDATE token_events AS target SET canonical = $3
    FROM input WHERE target.chain_id = $1::numeric AND target.block_hash = input.hash
), update_token_deltas AS (
    UPDATE token_balance_deltas AS target SET canonical = $3
    FROM input WHERE target.chain_id = $1::numeric AND target.block_hash = input.hash
), update_trace_attributions AS (
    UPDATE trace_log_attributions AS target SET canonical = $3
    FROM input WHERE target.chain_id = $1::numeric AND target.block_hash = input.hash
), update_traces AS (
    UPDATE normalized_traces AS target SET canonical = $3
    FROM input WHERE target.chain_id = $1::numeric AND target.block_hash = input.hash
), update_execution_resolutions AS (
    UPDATE transaction_execution_code_resolutions AS target SET canonical = $3
    FROM input WHERE target.chain_id = $1::numeric AND target.block_hash = input.hash
), update_authorizations AS (
    UPDATE eip7702_authorizations AS target SET canonical = $3
    FROM input WHERE target.chain_id = $1::numeric AND target.block_hash = input.hash
), update_state_changes AS (
    UPDATE transaction_state_changes AS target SET canonical = $3
    FROM input WHERE target.chain_id = $1::numeric AND target.block_hash = input.hash
), update_statistics AS (
    UPDATE block_statistics AS target SET canonical = $3
    FROM input WHERE target.chain_id = $1::numeric AND target.block_hash = input.hash
)
UPDATE address_activities AS target
SET canonical = $3
FROM input
WHERE target.chain_id = $1::numeric AND target.block_hash = input.hash;

-- name: StoreInsertCoreOutboxBatch :exec
WITH decoded AS (
    SELECT item.ordinality,
           item.value->>'topic' AS topic,
           item.value->>'message_key' AS message_key,
           item.value->'payload' AS payload
    FROM jsonb_array_elements($2::jsonb) WITH ORDINALITY AS item(value, ordinality)
), input AS (
    SELECT DISTINCT ON (topic, message_key) topic, message_key, payload
    FROM decoded
    ORDER BY topic, message_key, ordinality DESC
)
INSERT INTO transactional_outbox (
    chain_id, topic, message_key, payload, generation
)
SELECT $1::numeric, topic, message_key, payload, 1
FROM input
ON CONFLICT (chain_id, topic, message_key) DO UPDATE SET
    payload = EXCLUDED.payload,
    generation = transactional_outbox.generation + 1,
    available_at = clock_timestamp(),
    published_at = NULL,
    attempts = 0,
    last_error = NULL;
