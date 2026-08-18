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

-- name: DeleteExpiredENSAddressNameSnapshots :one
WITH expired AS MATERIALIZED (
    SELECT snapshot.id
    FROM ens_address_name_snapshots AS snapshot
    WHERE snapshot.chain_id = sqlc.arg(chain_id)::numeric
      AND snapshot.expires_at <= sqlc.arg(expired_before)::timestamptz
    ORDER BY snapshot.expires_at, snapshot.id
    LIMIT sqlc.arg(delete_limit)
    FOR UPDATE SKIP LOCKED
), deleted AS (
    DELETE FROM ens_address_name_snapshots AS snapshot
    USING expired
    WHERE snapshot.id = expired.id
    RETURNING 1
)
SELECT count(*)::bigint AS deleted_count FROM deleted;

-- name: DeleteExpiredENSResolutionGenerations :one
WITH expired AS MATERIALIZED (
    SELECT generation.id
    FROM ens_resolution_generations AS generation
    WHERE generation.chain_id = sqlc.arg(chain_id)::numeric
      AND generation.retain_until <= sqlc.arg(expired_before)::timestamptz
      AND NOT EXISTS (
          SELECT 1 FROM ens_address_name_snapshots AS snapshot
          WHERE snapshot.generation_id = generation.id
      )
    ORDER BY generation.retain_until, generation.id
    LIMIT sqlc.arg(delete_limit)
    FOR UPDATE SKIP LOCKED
), deleted AS (
    DELETE FROM ens_resolution_generations AS generation
    USING expired
    WHERE generation.id = expired.id
    RETURNING 1
)
SELECT count(*)::bigint AS deleted_count FROM deleted;
