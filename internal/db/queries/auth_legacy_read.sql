-- name: AuthLegacyGetAPIKeyByPrefix :one
SELECT key.prefix, key.digest, key.name, key.rate_per_second, key.burst,
       key.created_at, key.revoked_at, key.owner_user_id, key.scopes,
       COALESCE(owner.status = 'active', TRUE)::boolean AS owner_active
FROM api_keys AS key
LEFT JOIN users AS owner ON owner.id = key.owner_user_id
WHERE key.prefix = $1;

-- name: AuthLegacyLockActiveOwner :one
SELECT id::text
FROM users
WHERE id = $1 AND status = 'active'
FOR UPDATE;

-- name: AuthLegacyLockAPIKeyForRotation :one
SELECT name, rate_per_second, burst, revoked_at, owner_user_id, scopes
FROM api_keys
WHERE prefix = $1
FOR UPDATE;

-- name: AuthLegacyListAPIKeys :many
SELECT prefix, name, rate_per_second, burst, created_at, revoked_at,
       owner_user_id, scopes
FROM api_keys
WHERE NOT sqlc.arg('has_cursor')::boolean
   OR (created_at, prefix) > (sqlc.arg('after_created_at')::timestamptz, sqlc.arg('after_prefix')::text)
ORDER BY created_at, prefix
LIMIT sqlc.arg('page_limit')::integer;
