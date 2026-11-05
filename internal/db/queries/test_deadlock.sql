-- name: TestDeadlockSnapshot :one
SELECT d.datid::bigint AS database_id, d.deadlocks::bigint AS deadlocks,
       COALESCE(d.stats_reset::text, '')::text AS stats_reset,
       pg_postmaster_start_time()::text AS started_at
FROM pg_catalog.pg_stat_database AS d WHERE d.datname = current_database();

-- name: TestDeadlockSessions :many
SELECT a.pid::integer AS pid, COALESCE(a.application_name, '')::text AS application_name,
       COALESCE(a.state, '')::text AS state,
       COALESCE(a.wait_event_type, '')::text AS wait_type,
       COALESCE(a.wait_event, '')::text AS wait_event,
       COALESCE(a.xact_start::text, '')::text AS transaction_start,
       pg_blocking_pids(a.pid)::integer[] AS blockers,
       CASE WHEN a.wait_event_type = 'Lock' THEN COALESCE((SELECT jsonb_agg(l)::text FROM (
         SELECT locktype, mode, relation::text, transactionid::text,
                classid::text, objid::text, objsubid
         FROM pg_catalog.pg_locks WHERE pid = a.pid AND NOT granted
         LIMIT 64
       ) AS l), '[]') ELSE '[]' END::text AS waiting_locks
FROM pg_catalog.pg_stat_activity AS a
WHERE a.datname = current_database() AND a.pid <> pg_backend_pid()
  AND a.backend_type = 'client backend'
ORDER BY a.pid LIMIT 1025;
