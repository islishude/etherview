-- name: MempoolReplacementPredecessorStatus :many
SELECT state, endpoint_name, latest_snapshot_id, last_snapshot_write_id, last_attempt_at
FROM mempool_status
WHERE chain_id = $1::numeric
FOR UPDATE;

-- name: MempoolReplacementPredecessorSnapshot :many
SELECT endpoint_name, observed_at, expires_at
FROM mempool_snapshots
WHERE chain_id = $1::numeric AND id = $2;

-- name: MempoolLookupPending :many
SELECT pending.tx_hash, pending.from_address, pending.to_address,
       pending.nonce::text, pending.value::text, pending.gas::text,
       pending.gas_price::text, pending.max_fee_per_gas::text,
       pending.max_priority_fee_per_gas::text, pending.tx_type::text,
       pending.input, pending.raw, pending.first_seen_at,
       pending.last_seen_at, pending.expires_at,
       predecessor.replaced_hash
FROM mempool_snapshot_transactions AS member
JOIN mempool_transactions AS pending
  ON pending.chain_id = member.chain_id AND pending.tx_hash = member.tx_hash
LEFT JOIN LATERAL (
    SELECT replacement.replaced_hash
    FROM mempool_transaction_replacements AS replacement
    JOIN mempool_snapshots AS evidence
      ON evidence.chain_id = replacement.chain_id AND evidence.id = replacement.snapshot_id
    WHERE replacement.chain_id = pending.chain_id
      AND replacement.replacement_hash = pending.tx_hash
      AND evidence.observed_at <= $4
      AND evidence.expires_at > $5
    ORDER BY evidence.observed_at DESC, evidence.id DESC
    LIMIT 1
) AS predecessor ON TRUE
WHERE member.chain_id = $1::numeric
  AND member.snapshot_id = $2
  AND member.tx_hash = $3;

-- name: MempoolLookupReplaced :many
SELECT pending.tx_hash, pending.from_address, pending.to_address,
       pending.nonce::text, pending.value::text, pending.gas::text,
       pending.gas_price::text, pending.max_fee_per_gas::text,
       pending.max_priority_fee_per_gas::text, pending.tx_type::text,
       pending.input, pending.raw, pending.first_seen_at,
       pending.last_seen_at, pending.expires_at,
       predecessor.replaced_hash,
       replacement.replacement_hash, evidence.observed_at,
       evidence.expires_at, evidence.endpoint_name
FROM mempool_transaction_replacements AS replacement
JOIN mempool_snapshots AS evidence
  ON evidence.chain_id = replacement.chain_id AND evidence.id = replacement.snapshot_id
JOIN mempool_transactions AS pending
  ON pending.chain_id = replacement.chain_id AND pending.tx_hash = replacement.replaced_hash
LEFT JOIN LATERAL (
    SELECT earlier.replaced_hash
    FROM mempool_transaction_replacements AS earlier
    JOIN mempool_snapshots AS earlier_evidence
      ON earlier_evidence.chain_id = earlier.chain_id AND earlier_evidence.id = earlier.snapshot_id
    WHERE earlier.chain_id = pending.chain_id
      AND earlier.replacement_hash = pending.tx_hash
      AND earlier_evidence.observed_at <= evidence.observed_at
      AND earlier_evidence.expires_at > $3
    ORDER BY earlier_evidence.observed_at DESC, earlier_evidence.id DESC
    LIMIT 1
) AS predecessor ON TRUE
WHERE replacement.chain_id = $1::numeric
  AND replacement.replaced_hash = $2
  AND evidence.expires_at > $3
ORDER BY evidence.observed_at DESC, evidence.id DESC
LIMIT 1;

-- name: MempoolReadStatus :many
SELECT state, latest_snapshot_id, error_code, last_attempt_at
FROM mempool_status
WHERE chain_id = $1::numeric;

-- name: MempoolReadSnapshot :many
SELECT id, endpoint_name, observed_at, expires_at, transaction_count
FROM mempool_snapshots
WHERE chain_id = $1::numeric AND id = $2;

-- name: MempoolListPendingFirst :many
SELECT pending.tx_hash, pending.from_address, pending.to_address,
       pending.nonce::text, pending.value::text, pending.gas::text,
       pending.gas_price::text, pending.max_fee_per_gas::text,
       pending.max_priority_fee_per_gas::text, pending.tx_type::text,
       pending.input, pending.raw, pending.first_seen_at,
       pending.last_seen_at, pending.expires_at,
       predecessor.replaced_hash
FROM mempool_snapshot_transactions AS member
JOIN mempool_transactions AS pending
  ON pending.chain_id = member.chain_id AND pending.tx_hash = member.tx_hash
LEFT JOIN LATERAL (
    SELECT replacement.replaced_hash
    FROM mempool_transaction_replacements AS replacement
    JOIN mempool_snapshots AS evidence
      ON evidence.chain_id = replacement.chain_id AND evidence.id = replacement.snapshot_id
    WHERE replacement.chain_id = pending.chain_id
      AND replacement.replacement_hash = pending.tx_hash
      AND evidence.observed_at <= $3
      AND evidence.expires_at > $4
    ORDER BY evidence.observed_at DESC, evidence.id DESC
    LIMIT 1
) AS predecessor ON TRUE
WHERE member.chain_id = $1::numeric AND member.snapshot_id = $2
ORDER BY pending.first_seen_at DESC, pending.tx_hash DESC
LIMIT $5;

-- name: MempoolListPendingAfter :many
SELECT pending.tx_hash, pending.from_address, pending.to_address,
       pending.nonce::text, pending.value::text, pending.gas::text,
       pending.gas_price::text, pending.max_fee_per_gas::text,
       pending.max_priority_fee_per_gas::text, pending.tx_type::text,
       pending.input, pending.raw, pending.first_seen_at,
       pending.last_seen_at, pending.expires_at,
       predecessor.replaced_hash
FROM mempool_snapshot_transactions AS member
JOIN mempool_transactions AS pending
  ON pending.chain_id = member.chain_id AND pending.tx_hash = member.tx_hash
LEFT JOIN LATERAL (
    SELECT replacement.replaced_hash
    FROM mempool_transaction_replacements AS replacement
    JOIN mempool_snapshots AS evidence
      ON evidence.chain_id = replacement.chain_id AND evidence.id = replacement.snapshot_id
    WHERE replacement.chain_id = pending.chain_id
      AND replacement.replacement_hash = pending.tx_hash
      AND evidence.observed_at <= $3
      AND evidence.expires_at > $4
    ORDER BY evidence.observed_at DESC, evidence.id DESC
    LIMIT 1
) AS predecessor ON TRUE
WHERE member.chain_id = $1::numeric AND member.snapshot_id = $2
  AND (pending.first_seen_at, pending.tx_hash) < ($5, $6)
ORDER BY pending.first_seen_at DESC, pending.tx_hash DESC
LIMIT $7;
