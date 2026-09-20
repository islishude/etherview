-- name: StoreLegacyAppendJournalStatement1 :execrows
INSERT INTO block_journals (
			chain_id, block_hash, stage, sequence, payload, canonical, created_at
		)
		SELECT sqlc.arg('chain_id')::numeric, sqlc.arg('block_hash'), sqlc.arg('stage'), sqlc.arg('sequence')::numeric, sqlc.arg('payload')::jsonb,
		       EXISTS (
		           SELECT 1 FROM canonical_blocks
		           WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_hash = sqlc.arg('block_hash')
		       ), sqlc.arg('created_at')
		WHERE EXISTS (
		    SELECT 1 FROM blocks WHERE chain_id = sqlc.arg('chain_id')::numeric AND hash = sqlc.arg('block_hash')
			);
-- name: StoreLegacyApplyReorgStatement5 :exec
DELETE FROM index_checkpoints
		WHERE chain_id = sqlc.arg('chain_id')::numeric AND stage = sqlc.arg('stage');

-- name: StoreLegacyBindChainIdentityStatement1 :one
SELECT genesis_hash
		FROM chains
		WHERE chain_id = sqlc.arg('chain_id')::numeric
		FOR NO KEY UPDATE;

-- name: StoreLegacyBindChainIdentityStatement2 :one
INSERT INTO chains (chain_id, genesis_hash)
			VALUES (sqlc.arg('chain_id')::numeric, sqlc.arg('genesis_hash'))
			RETURNING genesis_hash;

-- name: StoreLegacyBindChainIdentityStatement3 :one
UPDATE chains
			SET genesis_hash = sqlc.arg('genesis_hash')
			WHERE chain_id = sqlc.arg('chain_id')::numeric AND genesis_hash IS NULL
			RETURNING genesis_hash;

-- name: StoreLegacyBundleByHashStatement1 :one
SELECT raw FROM blocks WHERE chain_id = sqlc.arg('chain_id')::numeric AND hash = sqlc.arg('hash');

-- name: StoreLegacyBundleByHashStatement2 :many
SELECT raw
		FROM receipts
		WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_hash = sqlc.arg('block_hash')
		ORDER BY tx_index;

-- name: StoreLegacyCheckCheckpointTxStatement1 :one
SELECT contiguous_through::text, block_hash
		FROM index_checkpoints
		WHERE chain_id = sqlc.arg('chain_id')::numeric AND stage = sqlc.arg('stage')
		FOR UPDATE;

-- name: StoreLegacyCheckpointStatement1 :one
SELECT contiguous_through::text, block_hash, updated_at
		FROM index_checkpoints
		WHERE chain_id = sqlc.arg('chain_id')::numeric AND stage = sqlc.arg('stage');

-- name: StoreLegacyClaimBackfillRangeStatement1 :exec
DELETE FROM core_backfill_leases
		WHERE chain_id = sqlc.arg('chain_id')::numeric AND expires_at <= sqlc.arg('expires_at');

-- name: StoreLegacyClaimBackfillRangeStatement2 :one
SELECT EXISTS (
			SELECT 1
			FROM core_backfill_leases
			WHERE chain_id = sqlc.arg('chain_id')::numeric
			  AND expires_at > sqlc.arg('expires_at')
			  AND NOT (range_end < sqlc.arg('max_range_end')::numeric OR range_start > sqlc.arg('min_range_start')::numeric)
		);

-- name: StoreLegacyClaimBackfillRangeStatement3 :exec
INSERT INTO core_backfill_leases (
			chain_id, range_start, range_end, owner, lease_token, claimed_at, expires_at
		) VALUES (sqlc.arg('chain_id')::numeric, sqlc.arg('range_start')::numeric, sqlc.arg('range_end')::numeric, sqlc.arg('owner'), sqlc.arg('lease_token')::uuid, sqlc.arg('claimed_at'), sqlc.arg('expires_at'));

-- name: StoreLegacyCompleteBackfillRangeStatement1 :one
SELECT expires_at
		FROM core_backfill_leases
		WHERE chain_id = sqlc.arg('chain_id')::numeric
		  AND range_start = sqlc.arg('range_start')::numeric AND range_end = sqlc.arg('range_end')::numeric
		  AND owner = sqlc.arg('owner') AND lease_token = sqlc.arg('lease_token')::uuid
		  AND expires_at > CURRENT_TIMESTAMP
		FOR UPDATE;

-- name: StoreLegacyCompleteBackfillRangeStatement2 :exec
DELETE FROM core_backfill_leases
		WHERE chain_id = sqlc.arg('chain_id')::numeric
		  AND range_start = sqlc.arg('range_start')::numeric AND range_end = sqlc.arg('range_end')::numeric
		  AND lease_token = sqlc.arg('lease_token')::uuid;

-- name: StoreLegacyConfigureIndexStatement1 :exec
INSERT INTO core_index_configuration (chain_id, configured_start)
		VALUES (sqlc.arg('chain_id')::numeric, sqlc.arg('configured_start')::numeric);

-- name: StoreLegacyConfigureIndexStatement2 :exec
DELETE FROM index_checkpoints
		WHERE chain_id = sqlc.arg('chain_id')::numeric AND stage = sqlc.arg('stage');

-- name: StoreLegacyDeleteBundleFactsTxStatement1 :exec
DELETE FROM block_journals
		WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_hash = sqlc.arg('block_hash');

-- name: StoreLegacyEnsureChainStatement1 :exec
INSERT INTO chains (chain_id) VALUES (sqlc.arg('chain_id')::numeric) ON CONFLICT (chain_id) DO NOTHING;

-- name: StoreLegacyInsertReorgEventStatement1 :exec
INSERT INTO reorg_events (
			chain_id, ancestor_number, ancestor_hash, old_tip_number, old_tip_hash,
			new_tip_number, new_tip_hash, detached, attached, reason
		) VALUES (sqlc.arg('chain_id')::numeric, sqlc.arg('ancestor_number')::numeric, sqlc.arg('ancestor_hash'), sqlc.arg('old_tip_number')::numeric, sqlc.arg('old_tip_hash'), sqlc.arg('new_tip_number')::numeric, sqlc.arg('new_tip_hash'), sqlc.arg('detached')::jsonb, sqlc.arg('attached')::jsonb, sqlc.arg('reason'));

-- name: StoreLegacyInsertRuntimeEventTxStatement1 :exec
INSERT INTO runtime_events (chain_id, event_type, payload)
		VALUES (sqlc.arg('chain_id')::numeric, sqlc.arg('event_type'), sqlc.arg('payload')::jsonb);

-- name: StoreLegacyInsertSparseReorgEventsTxStatement1 :exec
INSERT INTO reorg_events (
			chain_id, ancestor_number, ancestor_hash, old_tip_number, old_tip_hash,
			new_tip_number, new_tip_hash, detached, attached, reason
		) VALUES (sqlc.arg('chain_id')::numeric, sqlc.arg('ancestor_number')::numeric, sqlc.arg('ancestor_hash'), sqlc.arg('old_tip_number')::numeric, sqlc.arg('old_tip_hash'), sqlc.arg('new_tip_number')::numeric, sqlc.arg('new_tip_hash'), sqlc.arg('detached')::jsonb, sqlc.arg('attached')::jsonb, sqlc.arg('reason'));

-- name: StoreLegacyJournalsByBlockStatement1 :many
SELECT stage, sequence::text, payload, canonical, created_at
		FROM block_journals
		WHERE chain_id = sqlc.arg('chain_id')::numeric AND block_hash = sqlc.arg('block_hash')
		ORDER BY stage, sequence;

-- name: StoreLegacyLockChainStatement1 :exec
SELECT pg_advisory_xact_lock(hashtext('etherview:chain:' || sqlc.arg('chain_id')));

-- name: StoreLegacyQueryCanonicalReferencesTxStatement1 :many
SELECT cb.number::text AS number, cb.block_hash, b.parent_hash
FROM canonical_blocks cb
JOIN blocks b ON b.chain_id = cb.chain_id AND b.number = cb.number AND b.hash = cb.block_hash
WHERE cb.chain_id = sqlc.arg('chain_id')::text::numeric
  AND cb.number >= sqlc.arg('configured_start')::text::numeric
  AND (NOT sqlc.arg('has_cursor')::boolean OR cb.number > sqlc.arg('after_number')::text::numeric)
ORDER BY cb.number
LIMIT sqlc.arg('page_limit')::integer;

-- name: StoreLegacyQueryCoverageRangesTxStatement1 :many
SELECT range_start::text AS range_start, range_end::text AS range_end
FROM core_coverage_ranges
WHERE chain_id = sqlc.arg('chain_id')::text::numeric
  AND (NOT sqlc.arg('has_cursor')::boolean OR range_start > sqlc.arg('after_start')::text::numeric)
ORDER BY range_start
LIMIT sqlc.arg('page_limit')::integer;

-- name: StoreLegacyReadSchemaStatusStatement1 :one
SELECT (to_regclass('etherview_schema_migrations') IS NOT NULL)::boolean AS ledger_exists;

-- name: StoreLegacyReadSchemaStatusStatement2 :many
SELECT version, checksum
		FROM etherview_schema_migrations
		ORDER BY version;

-- name: StoreLegacyReleaseBackfillRangeStatement1 :execrows
DELETE FROM core_backfill_leases
		WHERE chain_id = sqlc.arg('chain_id')::numeric
		  AND range_start = sqlc.arg('range_start')::numeric AND range_end = sqlc.arg('range_end')::numeric
		  AND owner = sqlc.arg('owner') AND lease_token = sqlc.arg('lease_token')::uuid AND expires_at > CURRENT_TIMESTAMP;

-- name: StoreLegacyRenewBackfillRangeStatement1 :one
UPDATE core_backfill_leases
		SET expires_at = sqlc.arg('expires_at'), updated_at = now()
		WHERE chain_id = sqlc.arg('chain_id')::numeric
		  AND range_start = sqlc.arg('range_start')::numeric AND range_end = sqlc.arg('range_end')::numeric
		  AND owner = sqlc.arg('owner') AND lease_token = sqlc.arg('lease_token')::uuid AND expires_at > sqlc.arg('expires_at_2')
		RETURNING expires_at;

-- name: StoreLegacyReplaceCoverageRangesTxStatement1 :exec
DELETE FROM core_coverage_ranges
		WHERE chain_id = sqlc.arg('chain_id')::numeric;

-- name: StoreLegacyReplaceCoverageRangesTxStatement2 :exec
INSERT INTO core_coverage_ranges (chain_id, range_start, range_end)
			VALUES (sqlc.arg('chain_id')::numeric, sqlc.arg('range_start')::numeric, sqlc.arg('range_end')::numeric);

-- name: StoreLegacyReplaceHighestCanonicalSegmentStatement1 :one
SELECT COUNT(*)
			FROM canonical_blocks
			WHERE chain_id = sqlc.arg('chain_id')::numeric AND number > sqlc.arg('min_number')::numeric;

-- name: StoreLegacyUpdateFinalityStatement1 :exec
INSERT INTO chain_finality (
			chain_id, safe_number, safe_hash, finalized_number, finalized_hash, updated_at
		) VALUES (sqlc.arg('chain_id')::numeric, sqlc.narg('safe_number')::numeric, sqlc.arg('safe_hash'), sqlc.narg('finalized_number')::numeric, sqlc.arg('finalized_hash'), sqlc.arg('updated_at'))
		ON CONFLICT (chain_id) DO UPDATE SET
			safe_number = EXCLUDED.safe_number,
			safe_hash = EXCLUDED.safe_hash,
			finalized_number = EXCLUDED.finalized_number,
			finalized_hash = EXCLUDED.finalized_hash,
			updated_at = EXCLUDED.updated_at;

-- name: StoreLegacyUpsertCheckpointTxStatement1 :exec
INSERT INTO index_checkpoints (
			chain_id, stage, contiguous_through, block_hash, updated_at
		) VALUES (sqlc.arg('chain_id')::numeric, sqlc.arg('stage'), sqlc.arg('contiguous_through')::numeric, sqlc.arg('block_hash'), sqlc.arg('updated_at'))
		ON CONFLICT (chain_id, stage) DO UPDATE SET
			contiguous_through = EXCLUDED.contiguous_through,
			block_hash = EXCLUDED.block_hash,
			updated_at = EXCLUDED.updated_at;

-- name: StoreLegacyValidateRefreshParentTxStatement1 :one
SELECT EXISTS (
			SELECT 1 FROM canonical_blocks
			WHERE chain_id = sqlc.arg('chain_id')::numeric AND number < sqlc.arg('max_number')::numeric
		);
