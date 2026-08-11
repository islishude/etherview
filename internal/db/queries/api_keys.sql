-- name: LockActiveUserForAPIKey :one
SELECT id
FROM users
WHERE id = sqlc.arg(user_id)::uuid
  AND status = 'active'
FOR UPDATE;

-- name: CountActiveUserAPIKeys :one
SELECT count(*)::bigint
FROM api_keys
WHERE owner_user_id = sqlc.arg(user_id)::uuid
  AND revoked_at IS NULL;

-- name: CreateUserAPIKey :exec
INSERT INTO api_keys (
    prefix, digest, name, rate_per_second, burst, created_at, revoked_at,
    owner_user_id, scopes
) VALUES (
    sqlc.arg(prefix), sqlc.arg(digest), sqlc.arg(name),
    sqlc.arg(rate_per_second), sqlc.arg(burst), sqlc.arg(created_at), NULL,
    sqlc.arg(user_id)::uuid, sqlc.arg(scopes)::text[]
);

-- name: ListUserAPIKeysPage :many
SELECT prefix, name, rate_per_second, burst, created_at, revoked_at,
       owner_user_id, scopes
FROM api_keys
WHERE owner_user_id = sqlc.arg(user_id)::uuid
  AND (
      sqlc.narg(before_created_at)::timestamptz IS NULL
      OR (created_at, prefix) < (
          sqlc.narg(before_created_at)::timestamptz,
          sqlc.narg(before_prefix)::text
      )
  )
ORDER BY created_at DESC, prefix DESC
LIMIT sqlc.arg(page_limit);

-- name: LockUserAPIKey :one
SELECT prefix, digest, name, rate_per_second, burst, created_at, revoked_at,
       owner_user_id, scopes
FROM api_keys
WHERE prefix = sqlc.arg(prefix)
  AND owner_user_id = sqlc.arg(user_id)::uuid
FOR UPDATE;

-- name: RevokeUserAPIKey :one
UPDATE api_keys
SET revoked_at = COALESCE(revoked_at, sqlc.arg(revoked_at))
WHERE prefix = sqlc.arg(prefix)
  AND owner_user_id = sqlc.arg(user_id)::uuid
RETURNING prefix, digest, name, rate_per_second, burst, created_at, revoked_at,
          owner_user_id, scopes;

-- name: RevokeAllUserAPIKeys :execrows
UPDATE api_keys
SET revoked_at = sqlc.arg(revoked_at)
WHERE owner_user_id = sqlc.arg(user_id)::uuid
  AND revoked_at IS NULL;
