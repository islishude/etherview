-- name: TryRuntimeSessionLock :one
SELECT pg_try_advisory_lock(hashtextextended(current_schema() || ':' || sqlc.arg(lock_name)::text, 0));

-- name: ReleaseRuntimeSessionLock :one
SELECT pg_advisory_unlock(hashtextextended(current_schema() || ':' || sqlc.arg(lock_name)::text, 0));
