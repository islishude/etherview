-- name: AcquireENSGenerationLock :exec
SELECT pg_advisory_xact_lock(hashtextextended(
    'etherview:ens-generation:' || sqlc.arg(chain_id)::numeric::text, 0
));

-- name: GetFreshENSResolutionGeneration :one
SELECT generation.*
FROM ens_resolution_generations AS generation
WHERE generation.chain_id = sqlc.arg(chain_id)::numeric
  AND generation.policy_key = sqlc.arg(policy_key)
  AND generation.fresh_until > sqlc.arg(now_at)::timestamptz
  AND (
      generation.custom_block_hash IS NULL
      OR EXISTS (
          SELECT 1 FROM canonical_blocks AS canonical
          WHERE canonical.chain_id = generation.chain_id
            AND canonical.number = generation.custom_block_number
            AND canonical.block_hash = generation.custom_block_hash
      )
  )
ORDER BY generation.id DESC
LIMIT 1;

-- name: InsertENSResolutionGeneration :one
INSERT INTO ens_resolution_generations (
    chain_id, policy_key, coin_type,
    official_endpoint, official_block_number, official_block_hash,
    custom_endpoint, custom_coin_type, custom_block_number, custom_block_hash,
    created_at, fresh_until, retain_until
) SELECT
    sqlc.arg(chain_id)::numeric, sqlc.arg(policy_key), sqlc.arg(coin_type)::numeric,
    sqlc.arg(official_endpoint), sqlc.arg(official_block_number)::numeric,
    sqlc.arg(official_block_hash), sqlc.narg(custom_endpoint), sqlc.narg(custom_coin_type)::numeric,
    sqlc.narg(custom_block_number)::numeric, sqlc.narg(custom_block_hash),
    sqlc.arg(created_at)::timestamptz, sqlc.arg(fresh_until)::timestamptz,
    sqlc.arg(retain_until)::timestamptz
WHERE sqlc.narg(custom_block_hash)::bytea IS NULL
   OR EXISTS (
       SELECT 1 FROM canonical_blocks AS canonical
       WHERE canonical.chain_id = sqlc.arg(chain_id)::numeric
         AND canonical.number = sqlc.narg(custom_block_number)::numeric
         AND canonical.block_hash = sqlc.narg(custom_block_hash)::bytea
   )
RETURNING *;

-- name: GetENSResolutionGeneration :one
SELECT generation.*
FROM ens_resolution_generations AS generation
WHERE generation.id = sqlc.arg(generation_id)
  AND generation.chain_id = sqlc.arg(chain_id)::numeric
  AND generation.policy_key = sqlc.arg(policy_key)
  AND generation.retain_until > sqlc.arg(now_at)::timestamptz
  AND (
      generation.custom_block_hash IS NULL
      OR EXISTS (
          SELECT 1 FROM canonical_blocks AS canonical
          WHERE canonical.chain_id = generation.chain_id
            AND canonical.number = generation.custom_block_number
            AND canonical.block_hash = generation.custom_block_hash
      )
  );

-- name: GetENSNameObservation :one
SELECT observation.*
FROM ens_name_observations AS observation
WHERE observation.generation_id = sqlc.arg(generation_id)
  AND observation.chain_id = sqlc.arg(chain_id)::numeric
  AND observation.source = sqlc.arg(source)
  AND observation.direction = sqlc.arg(direction)
  AND observation.lookup_key = sqlc.arg(lookup_key)
LIMIT 1;

-- name: InsertENSNameObservation :one
INSERT INTO ens_name_observations (
    generation_id, chain_id, source, direction, lookup_key, outcome,
    name, address, resolver, reverse_resolver, observed_at
) VALUES (
    sqlc.arg(generation_id), sqlc.arg(chain_id)::numeric, sqlc.arg(source),
    sqlc.arg(direction), sqlc.arg(lookup_key), sqlc.arg(outcome),
    sqlc.narg(name), sqlc.narg(address), sqlc.narg(resolver),
    sqlc.narg(reverse_resolver), sqlc.arg(observed_at)::timestamptz
)
ON CONFLICT (generation_id, source, direction, lookup_key) DO UPDATE
SET lookup_key = ens_name_observations.lookup_key
WHERE ens_name_observations.chain_id = EXCLUDED.chain_id
  AND ens_name_observations.outcome = EXCLUDED.outcome
  AND ens_name_observations.name IS NOT DISTINCT FROM EXCLUDED.name
  AND ens_name_observations.address IS NOT DISTINCT FROM EXCLUDED.address
  AND ens_name_observations.resolver IS NOT DISTINCT FROM EXCLUDED.resolver
  AND ens_name_observations.reverse_resolver IS NOT DISTINCT FROM EXCLUDED.reverse_resolver
RETURNING *;

-- name: GetFreshENSResolutionFailure :one
SELECT failure.*
FROM ens_resolution_failures AS failure
WHERE failure.generation_id = sqlc.arg(generation_id)
  AND failure.chain_id = sqlc.arg(chain_id)::numeric
  AND failure.source = sqlc.arg(source)
  AND failure.direction = sqlc.arg(direction)
  AND failure.lookup_key = sqlc.arg(lookup_key)
  AND failure.expires_at > sqlc.arg(now_at)::timestamptz
ORDER BY failure.id DESC
LIMIT 1;

-- name: EnsureENSNameObservationPublished :exec
UPDATE ens_name_observations AS observation
SET publication_nonce = observation.publication_nonce + 1
WHERE observation.id = sqlc.arg(observation_id)
  AND observation.chain_id = sqlc.arg(chain_id)::numeric
  AND observation.direction = 'forward'
  AND observation.outcome = 'resolved'
  AND NOT EXISTS (
      SELECT 1 FROM search_catalog_documents AS document
      WHERE document.chain_id = observation.chain_id
        AND document.source_kind = 'name'
        AND document.name_observation_id = observation.id
        AND document.valid_to_generation IS NULL
  );

-- name: InsertENSResolutionFailure :exec
INSERT INTO ens_resolution_failures (
    generation_id, chain_id, source, direction, lookup_key, code,
    observed_at, expires_at
) VALUES (
    sqlc.arg(generation_id), sqlc.arg(chain_id)::numeric, sqlc.arg(source),
    sqlc.arg(direction), sqlc.arg(lookup_key), sqlc.arg(code),
    sqlc.arg(observed_at)::timestamptz, sqlc.arg(expires_at)::timestamptz
);

-- name: InsertENSAddressNameSnapshot :one
INSERT INTO ens_address_name_snapshots (
    id, chain_id, generation_id, created_at, expires_at
) VALUES (
    sqlc.arg(id), sqlc.arg(chain_id)::numeric, sqlc.arg(generation_id),
    sqlc.arg(created_at)::timestamptz, sqlc.arg(expires_at)::timestamptz
)
RETURNING *;

-- name: GetENSAddressNameSnapshot :one
SELECT snapshot.*
FROM ens_address_name_snapshots AS snapshot
JOIN ens_resolution_generations AS generation ON generation.id = snapshot.generation_id
WHERE snapshot.id = sqlc.arg(id)
  AND snapshot.chain_id = sqlc.arg(chain_id)::numeric
  AND snapshot.expires_at > sqlc.arg(now_at)::timestamptz
  AND generation.policy_key = sqlc.arg(policy_key)
  AND generation.retain_until > sqlc.arg(now_at)::timestamptz
  AND (
      generation.custom_block_hash IS NULL
      OR EXISTS (
          SELECT 1 FROM canonical_blocks AS canonical
          WHERE canonical.chain_id = generation.chain_id
            AND canonical.number = generation.custom_block_number
            AND canonical.block_hash = generation.custom_block_hash
      )
  );
