-- name: TrySearchCatalogMaintenanceLock :one
SELECT pg_try_advisory_xact_lock(
    hashtext('etherview:search-catalog-maintenance'),
    hashtext(sqlc.arg(chain_id)::text)
);

-- name: PruneSearchCatalog :one
SELECT prune_search_catalog(
    sqlc.arg(chain_id)::numeric,
    sqlc.arg(retention_generations)::bigint
) AS min_generation;

-- name: DeleteExpiredAdapterObservations :one
WITH expired AS MATERIALIZED (
    SELECT observation.id
    FROM external_adapter_observations AS observation
    WHERE observation.chain_id = sqlc.arg(chain_id)::numeric
      AND observation.expires_at <= sqlc.arg(expired_before)::timestamptz
    ORDER BY observation.expires_at, observation.id
    LIMIT sqlc.arg(delete_limit)
    FOR UPDATE SKIP LOCKED
), deleted AS (
    DELETE FROM external_adapter_observations AS observation
    USING expired
    WHERE observation.id = expired.id
    RETURNING 1
)
SELECT count(*)::bigint AS deleted_count
FROM deleted;
