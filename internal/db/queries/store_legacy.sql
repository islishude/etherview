-- name: StoreLegacyAppendJournalStatement1 :exec
INSERT INTO block_journals (
			chain_id, block_hash, stage, sequence, payload, canonical, created_at
		)
		SELECT $1::numeric, $2, $3, $4::numeric, $5::jsonb,
		       EXISTS (
		           SELECT 1 FROM canonical_blocks
		           WHERE chain_id = $1::numeric AND block_hash = $2
		       ), $6
		WHERE EXISTS (
		    SELECT 1 FROM blocks WHERE chain_id = $1::numeric AND hash = $2
			);
-- name: StoreLegacyApplyReorgStatement1 :exec
DELETE FROM canonical_blocks
			WHERE chain_id = $1::numeric AND number = $2::numeric AND block_hash = $3;

-- name: StoreLegacyApplyReorgStatement2 :exec
UPDATE block_journals SET canonical = FALSE
			WHERE chain_id = $1::numeric AND block_hash = $2;

-- name: StoreLegacyApplyReorgStatement3 :exec
INSERT INTO canonical_blocks (chain_id, number, block_hash)
			VALUES ($1::numeric, $2::numeric, $3);

-- name: StoreLegacyApplyReorgStatement4 :exec
UPDATE block_journals SET canonical = TRUE
			WHERE chain_id = $1::numeric AND block_hash = $2;

-- name: StoreLegacyApplyReorgStatement5 :exec
DELETE FROM index_checkpoints
		WHERE chain_id = $1::numeric AND stage = $2;

-- name: StoreLegacyBindChainIdentityStatement1 :many
SELECT genesis_hash
		FROM chains
		WHERE chain_id = $1::numeric
		FOR NO KEY UPDATE;

-- name: StoreLegacyBindChainIdentityStatement2 :many
INSERT INTO chains (chain_id, genesis_hash)
			VALUES ($1::numeric, $2)
			RETURNING genesis_hash;

-- name: StoreLegacyBindChainIdentityStatement3 :many
UPDATE chains
			SET genesis_hash = $2
			WHERE chain_id = $1::numeric AND genesis_hash IS NULL
			RETURNING genesis_hash;

-- name: StoreLegacyBundleByHashStatement1 :many
SELECT raw FROM blocks WHERE chain_id = $1::numeric AND hash = $2;

-- name: StoreLegacyBundleByHashStatement2 :many
SELECT raw
		FROM receipts
		WHERE chain_id = $1::numeric AND block_hash = $2
		ORDER BY tx_index;

-- name: StoreLegacyCheckCheckpointTxStatement1 :many
SELECT contiguous_through::text, block_hash
		FROM index_checkpoints
		WHERE chain_id = $1::numeric AND stage = $2
		FOR UPDATE;

-- name: StoreLegacyCheckpointStatement1 :many
SELECT contiguous_through::text, block_hash, updated_at
		FROM index_checkpoints
		WHERE chain_id = $1::numeric AND stage = $2;

-- name: StoreLegacyClaimBackfillRangeStatement1 :exec
DELETE FROM core_backfill_leases
		WHERE chain_id = $1::numeric AND expires_at <= $2;

-- name: StoreLegacyClaimBackfillRangeStatement2 :many
SELECT EXISTS (
			SELECT 1
			FROM core_backfill_leases
			WHERE chain_id = $1::numeric
			  AND expires_at > $2
			  AND NOT (range_end < $3::numeric OR range_start > $4::numeric)
		);

-- name: StoreLegacyClaimBackfillRangeStatement3 :exec
INSERT INTO core_backfill_leases (
			chain_id, range_start, range_end, owner, lease_token, claimed_at, expires_at
		) VALUES ($1::numeric, $2::numeric, $3::numeric, $4, $5::uuid, $6, $7);

-- name: StoreLegacyCommitCanonicalSegmentStatement1 :exec
INSERT INTO canonical_blocks (chain_id, number, block_hash)
				VALUES ($1::numeric, $2::numeric, $3);

-- name: StoreLegacyCommitCanonicalStatement1 :exec
INSERT INTO canonical_blocks (chain_id, number, block_hash)
			VALUES ($1::numeric, $2::numeric, $3);

-- name: StoreLegacyCompleteBackfillRangeStatement1 :many
SELECT expires_at
		FROM core_backfill_leases
		WHERE chain_id = $1::numeric
		  AND range_start = $2::numeric AND range_end = $3::numeric
		  AND owner = $4 AND lease_token = $5::uuid
		  AND expires_at > CURRENT_TIMESTAMP
		FOR UPDATE;

-- name: StoreLegacyCompleteBackfillRangeStatement2 :exec
DELETE FROM core_backfill_leases
		WHERE chain_id = $1::numeric
		  AND range_start = $2::numeric AND range_end = $3::numeric
		  AND lease_token = $4::uuid;

-- name: StoreLegacyConfigureIndexStatement1 :exec
INSERT INTO core_index_configuration (chain_id, configured_start)
		VALUES ($1::numeric, $2::numeric);

-- name: StoreLegacyConfigureIndexStatement2 :exec
DELETE FROM index_checkpoints
		WHERE chain_id = $1::numeric AND stage = $2;

-- name: StoreLegacyDeleteBundleFactsTxStatement1 :exec
DELETE FROM block_journals
		WHERE chain_id = $1::numeric AND block_hash = $2;

-- name: StoreLegacyEnsureChainStatement1 :exec
INSERT INTO chains (chain_id) VALUES ($1::numeric) ON CONFLICT (chain_id) DO NOTHING;

-- name: StoreLegacyInsertCoreOutboxTxStatement1 :exec
INSERT INTO transactional_outbox (
			chain_id, topic, message_key, payload, generation
		)
		VALUES ($1::numeric, $2, $3, $4::jsonb, 1)
		ON CONFLICT (chain_id, topic, message_key) DO UPDATE SET
			payload = EXCLUDED.payload,
			generation = transactional_outbox.generation + 1,
			available_at = clock_timestamp(),
			published_at = NULL,
			attempts = 0,
			last_error = NULL;

-- name: StoreLegacyInsertReorgEventStatement1 :exec
INSERT INTO reorg_events (
			chain_id, ancestor_number, ancestor_hash, old_tip_number, old_tip_hash,
			new_tip_number, new_tip_hash, detached, attached, reason
		) VALUES ($1::numeric, $2::numeric, $3, $4::numeric, $5, $6::numeric, $7, $8::jsonb, $9::jsonb, $10);

-- name: StoreLegacyInsertRuntimeEventTxStatement1 :exec
INSERT INTO runtime_events (chain_id, event_type, payload)
		VALUES ($1::numeric, $2, $3::jsonb);

-- name: StoreLegacyInsertSparseReorgEventsTxStatement1 :exec
INSERT INTO reorg_events (
			chain_id, ancestor_number, ancestor_hash, old_tip_number, old_tip_hash,
			new_tip_number, new_tip_hash, detached, attached, reason
		) VALUES ($1::numeric, $2::numeric, $3, $4::numeric, $5, $6::numeric, $7, $8::jsonb, $9::jsonb, $10);

-- name: StoreLegacyJournalsByBlockStatement1 :many
SELECT stage, sequence::text, payload, canonical, created_at
		FROM block_journals
		WHERE chain_id = $1::numeric AND block_hash = $2
		ORDER BY stage, sequence;

-- name: StoreLegacyLockChainStatement1 :many
SELECT pg_advisory_xact_lock(hashtext('etherview:chain:' || $1));

-- name: StoreLegacyPutBundleTxStatement1 :exec
INSERT INTO blocks (chain_id, number, hash, parent_hash, timestamp, raw)
		VALUES ($1::numeric, $2::numeric, $3, $4, $5::numeric, $6::jsonb)
		ON CONFLICT (chain_id, number, hash) DO UPDATE SET
			parent_hash = EXCLUDED.parent_hash,
			timestamp = EXCLUDED.timestamp,
			raw = EXCLUDED.raw;

-- name: StoreLegacyPutBundleTxStatement2 :exec
INSERT INTO transactions (chain_id, hash, tx_type, raw)
			VALUES ($1::numeric, $2, $3::numeric, $4::jsonb)
			ON CONFLICT (chain_id, hash) DO UPDATE SET
				tx_type = EXCLUDED.tx_type,
				raw = EXCLUDED.raw;

-- name: StoreLegacyPutBundleTxStatement3 :exec
INSERT INTO transaction_inclusions (
				chain_id, block_number, block_hash, tx_index, tx_hash, raw
			) VALUES ($1::numeric, $2::numeric, $3, $4, $5, $6::jsonb)
			ON CONFLICT (chain_id, block_number, block_hash, tx_index)
			DO UPDATE SET raw = EXCLUDED.raw;

-- name: StoreLegacyPutBundleTxStatement4 :exec
INSERT INTO receipts (
				chain_id, block_number, block_hash, tx_index, tx_hash, raw
			) VALUES ($1::numeric, $2::numeric, $3, $4, $5, $6::jsonb)
			ON CONFLICT (chain_id, block_number, block_hash, tx_index)
			DO UPDATE SET raw = EXCLUDED.raw;

-- name: StoreLegacyPutBundleTxStatement5 :exec
INSERT INTO logs (
					chain_id, block_number, block_hash, log_index, tx_index,
					tx_hash, address, topic0, raw
				) VALUES ($1::numeric, $2::numeric, $3, $4, $5, $6, $7, $8, $9::jsonb)
				ON CONFLICT (chain_id, block_number, block_hash, log_index)
				DO UPDATE SET raw = EXCLUDED.raw;

-- name: StoreLegacyPutBundleTxStatement6 :exec
INSERT INTO withdrawals (
				chain_id, block_number, block_hash, withdrawal_index,
				validator_index, address, amount, raw
			) VALUES ($1::numeric, $2::numeric, $3, $4::numeric, $5::numeric, $6, $7::numeric, $8::jsonb)
			ON CONFLICT (chain_id, block_number, block_hash, withdrawal_index)
			DO UPDATE SET raw = EXCLUDED.raw;

-- name: StoreLegacyQueryCanonicalReferencesTxStatement1 :many
SELECT cb.number::text, cb.block_hash, b.parent_hash
		FROM canonical_blocks cb
		JOIN blocks b
		  ON b.chain_id = cb.chain_id AND b.number = cb.number AND b.hash = cb.block_hash
		WHERE cb.chain_id = $1::numeric AND cb.number >= $2::numeric
		ORDER BY cb.number;

-- name: StoreLegacyQueryCoverageRangesTxStatement1 :many
SELECT range_start::text, range_end::text
		FROM core_coverage_ranges
		WHERE chain_id = $1::numeric
		ORDER BY range_start;

-- name: StoreLegacyReadSchemaStatusStatement1 :many
SELECT to_regclass('etherview_schema_migrations')::text;

-- name: StoreLegacyReadSchemaStatusStatement2 :many
SELECT version, checksum
		FROM etherview_schema_migrations
		ORDER BY version;

-- name: StoreLegacyReleaseBackfillRangeStatement1 :exec
DELETE FROM core_backfill_leases
		WHERE chain_id = $1::numeric
		  AND range_start = $2::numeric AND range_end = $3::numeric
		  AND owner = $4 AND lease_token = $5::uuid AND expires_at > CURRENT_TIMESTAMP;

-- name: StoreLegacyRenewBackfillRangeStatement1 :many
UPDATE core_backfill_leases
		SET expires_at = $1, updated_at = now()
		WHERE chain_id = $2::numeric
		  AND range_start = $3::numeric AND range_end = $4::numeric
		  AND owner = $5 AND lease_token = $6::uuid AND expires_at > $7
		RETURNING expires_at;

-- name: StoreLegacyReplaceCoverageRangesTxStatement1 :exec
DELETE FROM core_coverage_ranges
		WHERE chain_id = $1::numeric;

-- name: StoreLegacyReplaceCoverageRangesTxStatement2 :exec
INSERT INTO core_coverage_ranges (chain_id, range_start, range_end)
			VALUES ($1::numeric, $2::numeric, $3::numeric);

-- name: StoreLegacyReplaceHighestCanonicalSegmentStatement1 :many
SELECT COUNT(*)
			FROM canonical_blocks
			WHERE chain_id = $1::numeric AND number > $2::numeric;

-- name: StoreLegacyReplaceHighestCanonicalSegmentStatement2 :exec
DELETE FROM canonical_blocks
			WHERE chain_id = $1::numeric AND number = $2::numeric AND block_hash = $3;

-- name: StoreLegacyReplaceHighestCanonicalSegmentStatement3 :exec
UPDATE block_journals SET canonical = FALSE
			WHERE chain_id = $1::numeric AND block_hash = $2;

-- name: StoreLegacyReplaceHighestCanonicalSegmentStatement4 :exec
INSERT INTO canonical_blocks (chain_id, number, block_hash)
			VALUES ($1::numeric, $2::numeric, $3);

-- name: StoreLegacyReplaceHighestCanonicalSegmentStatement5 :exec
UPDATE block_journals SET canonical = TRUE
			WHERE chain_id = $1::numeric AND block_hash = $2;

-- name: StoreLegacyUpdateFinalityStatement1 :exec
INSERT INTO chain_finality (
			chain_id, safe_number, safe_hash, finalized_number, finalized_hash, updated_at
		) VALUES ($1::numeric, $2::numeric, $3, $4::numeric, $5, $6)
		ON CONFLICT (chain_id) DO UPDATE SET
			safe_number = EXCLUDED.safe_number,
			safe_hash = EXCLUDED.safe_hash,
			finalized_number = EXCLUDED.finalized_number,
			finalized_hash = EXCLUDED.finalized_hash,
			updated_at = EXCLUDED.updated_at;

-- name: StoreLegacyUpsertCheckpointTxStatement1 :exec
INSERT INTO index_checkpoints (
			chain_id, stage, contiguous_through, block_hash, updated_at
		) VALUES ($1::numeric, $2, $3::numeric, $4, $5)
		ON CONFLICT (chain_id, stage) DO UPDATE SET
			contiguous_through = EXCLUDED.contiguous_through,
			block_hash = EXCLUDED.block_hash,
			updated_at = EXCLUDED.updated_at;

-- name: StoreLegacyValidateRefreshParentTxStatement1 :many
SELECT EXISTS (
			SELECT 1 FROM canonical_blocks
			WHERE chain_id = $1::numeric AND number < $2::numeric
		);
