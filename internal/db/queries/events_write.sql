-- name: EventsWriteRecordStatusStatement1 :exec
SELECT pg_advisory_xact_lock(hashtext('etherview:sync-status:' || sqlc.arg('chain_id')));

-- name: EventsWriteRecordStatusStatement2 :one
INSERT INTO sync_runtime_status_writer_leases (
			chain_id, reporter_id,
			observed_latest_number, observed_latest_known, safety_halt,
			expires_at, updated_at
		) VALUES (
			sqlc.arg('chain_id')::numeric, sqlc.arg('reporter_id'),
			sqlc.narg('observed_latest_number')::numeric, sqlc.arg('observed_latest_known'), sqlc.arg('safety_halt'),
			CASE
				WHEN sqlc.arg('last_error')::text <> '' AND NOT sqlc.arg('safety_halt') THEN clock_timestamp()
				ELSE clock_timestamp() + (sqlc.arg('lease_milliseconds')::bigint * interval '1 millisecond')
			END,
			clock_timestamp()
		)
		ON CONFLICT (chain_id) DO UPDATE SET
			reporter_id = EXCLUDED.reporter_id,
			observed_latest_number = EXCLUDED.observed_latest_number,
			observed_latest_known = EXCLUDED.observed_latest_known,
			safety_halt = EXCLUDED.safety_halt,
			expires_at = EXCLUDED.expires_at,
			updated_at = clock_timestamp()
		WHERE (
				sync_runtime_status_writer_leases.reporter_id = EXCLUDED.reporter_id
				AND (
					NOT sync_runtime_status_writer_leases.safety_halt
					OR sqlc.arg('safety_halt')
				)
		   )
		   OR sync_runtime_status_writer_leases.expires_at <= clock_timestamp()
		   OR (sqlc.arg('safety_halt') AND NOT sync_runtime_status_writer_leases.safety_halt)
		   OR (
				NOT sync_runtime_status_writer_leases.safety_halt
				AND sqlc.arg('last_error')::text = ''
				AND sqlc.arg('observed_latest_known')
				AND (
					NOT sync_runtime_status_writer_leases.observed_latest_known
					OR sync_runtime_status_writer_leases.observed_latest_number < sqlc.narg('observed_latest_number')::numeric
				)
		   )
		RETURNING reporter_id;

-- name: EventsWriteRecordStatusStatement3 :exec
INSERT INTO sync_runtime_status (
			chain_id, latest_number, indexed_number, highest_covered_number,
			backfill_complete, ready,
			last_poll_at, last_error_code, updated_at
		) VALUES (sqlc.arg('chain_id')::numeric, sqlc.narg('latest_number')::numeric, sqlc.narg('indexed_number')::numeric, sqlc.narg('highest_covered_number')::numeric, sqlc.arg('backfill_complete'), sqlc.arg('ready'), sqlc.arg('last_poll_at'), sqlc.arg('last_error_code'), clock_timestamp())
		ON CONFLICT (chain_id) DO UPDATE SET
			latest_number = EXCLUDED.latest_number,
			indexed_number = EXCLUDED.indexed_number,
			highest_covered_number = EXCLUDED.highest_covered_number,
			backfill_complete = EXCLUDED.backfill_complete,
			ready = EXCLUDED.ready,
			last_poll_at = EXCLUDED.last_poll_at,
			last_error_code = EXCLUDED.last_error_code,
			updated_at = clock_timestamp();

-- name: EventsWriteRecordStatusStatement4 :one
INSERT INTO runtime_events (chain_id, event_type, payload)
		VALUES (sqlc.arg('chain_id')::numeric, 'status', sqlc.arg('payload')::jsonb)
		RETURNING id, created_at;

-- name: EventsWriteRecordStatusStatement5 :exec
DELETE FROM runtime_events
		WHERE chain_id = sqlc.arg('chain_id')::numeric
		  AND id < COALESCE((
			SELECT id
			FROM runtime_events
			WHERE chain_id = sqlc.arg('chain_id')::numeric
			ORDER BY id DESC
			OFFSET sqlc.arg('offset') LIMIT 1
			  ), 0);
