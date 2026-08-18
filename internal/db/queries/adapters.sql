-- name: GetFreshAdapterObservation :one
SELECT state, code, value, block_number, block_hash,
       observed_at, expires_at
FROM external_adapter_observations
WHERE chain_id = sqlc.arg(chain_id)::numeric
  AND capability = sqlc.arg(capability)
  AND provider_key = sqlc.arg(provider_key)
  AND observation_key = sqlc.arg(observation_key)
  AND expires_at > sqlc.arg(now_at)::timestamptz
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
