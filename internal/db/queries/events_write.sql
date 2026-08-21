-- name: EventsWriteRecordStatusStatement1 :many
SELECT pg_advisory_xact_lock(hashtext('etherview:sync-status:' || $1));

-- name: EventsWriteRecordStatusStatement2 :many
INSERT INTO sync_runtime_status_writer_leases (
			chain_id, reporter_id,
			observed_latest_number, observed_latest_known, safety_halt,
			expires_at, updated_at
		) VALUES (
			$1::numeric, $2,
			$6::numeric, $5, $7,
			CASE
				WHEN $4 <> '' AND NOT $7 THEN clock_timestamp()
				ELSE clock_timestamp() + ($3 * interval '1 millisecond')
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
					OR $7
				)
		   )
		   OR sync_runtime_status_writer_leases.expires_at <= clock_timestamp()
		   OR ($7 AND NOT sync_runtime_status_writer_leases.safety_halt)
		   OR (
				NOT sync_runtime_status_writer_leases.safety_halt
				AND $4 = ''
				AND $5
				AND (
					NOT sync_runtime_status_writer_leases.observed_latest_known
					OR sync_runtime_status_writer_leases.observed_latest_number < $6::numeric
				)
		   )
		RETURNING reporter_id;

-- name: EventsWriteRecordStatusStatement3 :exec
INSERT INTO sync_runtime_status (
			chain_id, latest_number, indexed_number, highest_covered_number,
			backfill_complete, ready,
			last_poll_at, last_error_code, updated_at
		) VALUES ($1::numeric, $2::numeric, $3::numeric, $4::numeric, $5, $6, $7, $8, clock_timestamp())
		ON CONFLICT (chain_id) DO UPDATE SET
			latest_number = EXCLUDED.latest_number,
			indexed_number = EXCLUDED.indexed_number,
			highest_covered_number = EXCLUDED.highest_covered_number,
			backfill_complete = EXCLUDED.backfill_complete,
			ready = EXCLUDED.ready,
			last_poll_at = EXCLUDED.last_poll_at,
			last_error_code = EXCLUDED.last_error_code,
			updated_at = clock_timestamp();

-- name: EventsWriteRecordStatusStatement4 :many
INSERT INTO runtime_events (chain_id, event_type, payload)
		VALUES ($1::numeric, 'status', $2::jsonb)
		RETURNING id, created_at;

-- name: EventsWriteRecordStatusStatement5 :exec
DELETE FROM runtime_events
		WHERE chain_id = $1::numeric
		  AND id < COALESCE((
			SELECT id
			FROM runtime_events
			WHERE chain_id = $1::numeric
			ORDER BY id DESC
			OFFSET $2 LIMIT 1
			  ), 0);
