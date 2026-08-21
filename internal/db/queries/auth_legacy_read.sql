-- name: AuthLegacyGetAPIKeyByPrefix :many
SELECT key.prefix, key.digest, key.name, key.rate_per_second, key.burst,
       key.created_at, key.revoked_at, key.owner_user_id, key.scopes,
       COALESCE(owner.status = 'active', TRUE)
FROM api_keys AS key
LEFT JOIN users AS owner ON owner.id = key.owner_user_id
WHERE key.prefix = $1;

-- name: AuthLegacyLockActiveOwner :many
SELECT id::text
FROM users
WHERE id = $1 AND status = 'active'
FOR UPDATE;

-- name: AuthLegacyLockAPIKeyForRotation :many
SELECT name, rate_per_second, burst, revoked_at, owner_user_id, scopes
FROM api_keys
WHERE prefix = $1
FOR UPDATE;

-- name: AuthLegacyListAPIKeys :many
SELECT prefix, name, rate_per_second, burst, created_at, revoked_at,
       owner_user_id, scopes
FROM api_keys
ORDER BY created_at, prefix;
