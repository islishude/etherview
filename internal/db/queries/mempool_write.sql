-- name: MempoolWriteLockMempoolStatement1 :many
SELECT pg_advisory_xact_lock(hashtext('etherview:mempool:' || $1));

-- name: MempoolWriteStoreFailureStatement1 :exec
INSERT INTO mempool_status (
			chain_id, state, endpoint_name, latest_snapshot_id, transaction_count,
			last_attempt_at, last_success_at, error_code, error_message, updated_at
		) VALUES ($1::numeric, $2, $3, NULL, NULL, $4, NULL, $5, $6, now())
		ON CONFLICT (chain_id) DO UPDATE SET
			state = EXCLUDED.state,
			endpoint_name = EXCLUDED.endpoint_name,
			last_attempt_at = EXCLUDED.last_attempt_at,
			error_code = EXCLUDED.error_code,
			error_message = EXCLUDED.error_message,
			updated_at = now()
		WHERE mempool_status.last_attempt_at <= EXCLUDED.last_attempt_at;

-- name: MempoolWriteStoreSnapshotStatement1 :many
INSERT INTO mempool_snapshots (
			chain_id, endpoint_name, observed_at, expires_at, transaction_count
		) VALUES ($1::numeric, $2, $3, $4, $5)
		RETURNING id;

-- name: MempoolWriteStoreSnapshotStatement2 :exec
INSERT INTO mempool_transactions (
			chain_id, tx_hash, from_address, to_address, nonce, value, gas,
			gas_price, max_fee_per_gas, max_priority_fee_per_gas, tx_type,
			input, raw, first_seen_at, last_seen_at, expires_at, last_endpoint_name
		) VALUES (
			$1::numeric, $2, $3, $4, $5::numeric, $6::numeric, $7::numeric,
			$8::numeric, $9::numeric, $10::numeric, $11::numeric,
			$12, $13::jsonb, $14, $15, $16, $17
		)
		ON CONFLICT (chain_id, tx_hash) DO UPDATE SET
			last_seen_at = GREATEST(mempool_transactions.last_seen_at, EXCLUDED.last_seen_at),
			expires_at = GREATEST(mempool_transactions.expires_at, EXCLUDED.expires_at),
			last_endpoint_name = CASE
				WHEN EXCLUDED.last_seen_at >= mempool_transactions.last_seen_at THEN EXCLUDED.last_endpoint_name
				ELSE mempool_transactions.last_endpoint_name
			END
		WHERE mempool_transactions.from_address = EXCLUDED.from_address
		  AND mempool_transactions.to_address IS NOT DISTINCT FROM EXCLUDED.to_address
		  AND mempool_transactions.nonce = EXCLUDED.nonce
		  AND mempool_transactions.value = EXCLUDED.value
		  AND mempool_transactions.gas = EXCLUDED.gas
		  AND mempool_transactions.gas_price IS NOT DISTINCT FROM EXCLUDED.gas_price
		  AND mempool_transactions.max_fee_per_gas IS NOT DISTINCT FROM EXCLUDED.max_fee_per_gas
		  AND mempool_transactions.max_priority_fee_per_gas IS NOT DISTINCT FROM EXCLUDED.max_priority_fee_per_gas
		  AND mempool_transactions.tx_type IS NOT DISTINCT FROM EXCLUDED.tx_type
		  AND mempool_transactions.input = EXCLUDED.input;

-- name: MempoolWriteStoreSnapshotStatement3 :exec
INSERT INTO mempool_snapshot_transactions (chain_id, snapshot_id, tx_hash)
			VALUES ($1::numeric, $2, $3);

-- name: MempoolWriteStoreSnapshotStatement4 :exec
WITH previous_slots AS (
				SELECT pending.from_address, pending.nonce, (array_agg(pending.tx_hash))[1] AS tx_hash
				FROM mempool_snapshot_transactions AS member
				JOIN mempool_transactions AS pending
				  ON pending.chain_id = member.chain_id AND pending.tx_hash = member.tx_hash
				WHERE member.chain_id = $1::numeric AND member.snapshot_id = $2
				GROUP BY pending.from_address, pending.nonce
				HAVING count(*) = 1
			), current_slots AS (
				SELECT pending.from_address, pending.nonce, (array_agg(pending.tx_hash))[1] AS tx_hash
				FROM mempool_snapshot_transactions AS member
				JOIN mempool_transactions AS pending
				  ON pending.chain_id = member.chain_id AND pending.tx_hash = member.tx_hash
				WHERE member.chain_id = $1::numeric AND member.snapshot_id = $3
				GROUP BY pending.from_address, pending.nonce
				HAVING count(*) = 1
			)
			INSERT INTO mempool_transaction_replacements (
				chain_id, snapshot_id, replaced_hash, replacement_hash
			)
			SELECT $1::numeric, $3, previous.tx_hash, current.tx_hash
			FROM previous_slots AS previous
			JOIN current_slots AS current
			  ON current.from_address = previous.from_address
			 AND current.nonce = previous.nonce
			WHERE current.tx_hash <> previous.tx_hash;

-- name: MempoolWriteStoreSnapshotStatement5 :exec
UPDATE mempool_transactions AS pending
			SET expires_at = GREATEST(pending.expires_at, $3)
			FROM mempool_transaction_replacements AS replacement
			WHERE replacement.chain_id = $1::numeric
			  AND replacement.snapshot_id = $2
			  AND pending.chain_id = replacement.chain_id
			  AND pending.tx_hash = replacement.replaced_hash;

-- name: MempoolWriteStoreSnapshotStatement6 :exec
INSERT INTO mempool_status (
			chain_id, state, endpoint_name, latest_snapshot_id, transaction_count,
			last_attempt_at, last_success_at, error_code, error_message, updated_at
		) VALUES ($1::numeric, 'complete', $2, $3, $4, $5, $5, NULL, NULL, now())
		ON CONFLICT (chain_id) DO UPDATE SET
			state = EXCLUDED.state,
			endpoint_name = EXCLUDED.endpoint_name,
			latest_snapshot_id = EXCLUDED.latest_snapshot_id,
			transaction_count = EXCLUDED.transaction_count,
			last_attempt_at = EXCLUDED.last_attempt_at,
			last_success_at = EXCLUDED.last_success_at,
			error_code = NULL,
			error_message = NULL,
			updated_at = now()
		WHERE mempool_status.last_attempt_at <= EXCLUDED.last_attempt_at;

-- name: MempoolWriteStoreSnapshotStatement7 :exec
UPDATE mempool_status
		SET last_snapshot_write_id = $2
		WHERE chain_id = $1::numeric;

-- name: MempoolWriteStoreSnapshotStatement8 :exec
DELETE FROM mempool_snapshots
		WHERE chain_id = $1::numeric AND expires_at <= $2 AND id <> $3;

-- name: MempoolWriteStoreSnapshotStatement9 :exec
DELETE FROM mempool_transactions AS pending
		WHERE pending.chain_id = $1::numeric
		  AND pending.expires_at <= $2
		  AND NOT EXISTS (
			SELECT 1 FROM mempool_snapshot_transactions AS member
			WHERE member.chain_id = pending.chain_id AND member.tx_hash = pending.tx_hash
			  );
