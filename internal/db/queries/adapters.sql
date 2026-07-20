-- name: GetFreshAdapterObservation :one
SELECT state, code, value, block_number, block_hash,
       observed_at, expires_at
FROM external_adapter_observations
WHERE chain_id = sqlc.arg(chain_id)::numeric
  AND capability = sqlc.arg(capability)
  AND provider_key = sqlc.arg(provider_key)
  AND observation_key = sqlc.arg(observation_key)
  AND expires_at > sqlc.arg(now_at)::timestamptz
  AND (
      capability <> 'name'
      OR state <> 'complete'
      OR EXISTS (
          SELECT 1 FROM canonical_blocks AS canonical
          WHERE canonical.chain_id = external_adapter_observations.chain_id
            AND canonical.number = external_adapter_observations.block_number
            AND canonical.block_hash = external_adapter_observations.block_hash
      )
  )
ORDER BY observed_at DESC, id DESC
LIMIT 1;

-- name: RecordAdapterFailure :exec
INSERT INTO external_adapter_observations (
    chain_id, capability, provider_key, observation_key, state, code,
    observed_at, expires_at
) VALUES (
    sqlc.arg(chain_id)::numeric, sqlc.arg(capability), sqlc.arg(provider_key),
    sqlc.arg(observation_key), sqlc.arg(state), sqlc.arg(code),
    sqlc.arg(observed_at)::timestamptz, sqlc.arg(expires_at)::timestamptz
);

-- name: RecordPriceAdapterSuccess :exec
INSERT INTO external_adapter_observations (
    chain_id, capability, provider_key, observation_key, state, value,
    observed_at, expires_at
) VALUES (
    sqlc.arg(chain_id)::numeric, 'price', 'default', 'native', 'complete',
    sqlc.arg(value)::jsonb, sqlc.arg(observed_at)::timestamptz,
    sqlc.arg(expires_at)::timestamptz
);

-- name: RecordNameAdapterSuccess :one
WITH canonical AS MATERIALIZED (
    SELECT 1
    FROM canonical_blocks AS canonical_block
    WHERE canonical_block.chain_id = sqlc.arg(chain_id)::numeric
      AND canonical_block.number = sqlc.arg(observed_block_number)::numeric
      AND canonical_block.block_hash = sqlc.arg(observed_block_hash)
    FOR KEY SHARE
), accepted_name AS (
    INSERT INTO name_records AS stored_name (
        chain_id, registry, name, address, resolver, block_number, block_hash,
        canonical, observed_at
    )
    SELECT sqlc.arg(chain_id)::numeric, sqlc.arg(registry), sqlc.arg(name),
           sqlc.arg(address), sqlc.narg(resolver), sqlc.arg(observed_block_number)::numeric,
           sqlc.arg(observed_block_hash), TRUE, sqlc.arg(observed_at)::timestamptz
    FROM canonical
    ON CONFLICT (chain_id, registry, name, block_hash) DO UPDATE
    SET address = stored_name.address
    WHERE stored_name.address = EXCLUDED.address
      AND stored_name.resolver IS NOT DISTINCT FROM EXCLUDED.resolver
      AND stored_name.block_number = EXCLUDED.block_number
    RETURNING stored_name.address
), stored_observation AS (
    INSERT INTO external_adapter_observations (
        chain_id, capability, provider_key, observation_key, state, value,
        block_number, block_hash, observed_at, expires_at
    )
    SELECT sqlc.arg(chain_id)::numeric, 'name', sqlc.arg(provider_key),
           sqlc.arg(name), 'complete', sqlc.arg(value)::jsonb,
           sqlc.arg(observed_block_number)::numeric, sqlc.arg(observed_block_hash),
           sqlc.arg(observed_at)::timestamptz, sqlc.arg(expires_at)::timestamptz
    FROM accepted_name
    RETURNING 1
)
SELECT CASE
           WHEN NOT EXISTS (SELECT 1 FROM canonical) THEN 'stale_block'::text
           WHEN NOT EXISTS (SELECT 1 FROM accepted_name) THEN 'identity_conflict'::text
           ELSE 'stored'::text
       END AS state,
       (SELECT address FROM accepted_name LIMIT 1) AS address
FROM (SELECT 1) AS singleton;
