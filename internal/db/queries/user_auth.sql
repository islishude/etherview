-- name: CreateAuthChallenge :one
INSERT INTO auth_challenges (
    id, chain_id, address, message, nonce, issued_at, expires_at
) VALUES (
    sqlc.arg(id)::uuid,
    sqlc.arg(chain_id)::numeric,
    sqlc.arg(address),
    sqlc.arg(message),
    sqlc.arg(nonce),
    sqlc.arg(issued_at),
    sqlc.arg(expires_at)
)
RETURNING id, chain_id, address, message, nonce, issued_at, expires_at,
          consumed_at;

-- name: GetAuthChallenge :one
SELECT id, chain_id, address, message, nonce, issued_at, expires_at, consumed_at
FROM auth_challenges
WHERE id = sqlc.arg(id)::uuid;

-- name: ConsumeAuthChallenge :one
UPDATE auth_challenges
SET consumed_at = sqlc.arg(consumed_at)
WHERE id = sqlc.arg(id)::uuid
  AND consumed_at IS NULL
  AND expires_at > sqlc.arg(consumed_at)
RETURNING id, chain_id, address, message, nonce, issued_at, expires_at,
          consumed_at;

-- name: GetOrCreateUserForLogin :one
WITH inserted AS (
    INSERT INTO users (
        id, chain_id, address, role, status, created_at, updated_at
    ) VALUES (
        sqlc.arg(id)::uuid,
        sqlc.arg(chain_id)::numeric,
        sqlc.arg(address),
        'user',
        'active',
        sqlc.arg(created_at),
        sqlc.arg(created_at)
    )
    ON CONFLICT (chain_id, address) DO NOTHING
    RETURNING id, chain_id, address, display_name, role, status, created_at,
              updated_at, last_login_at
)
SELECT id, chain_id, address, display_name, role, status, created_at,
       updated_at, last_login_at
FROM inserted
UNION ALL
SELECT id, chain_id, address, display_name, role, status, created_at,
       updated_at, last_login_at
FROM users
WHERE chain_id = sqlc.arg(chain_id)::numeric
  AND address = sqlc.arg(address)
LIMIT 1;

-- name: RecordUserLogin :one
UPDATE users
SET last_login_at = sqlc.arg(logged_in_at),
    updated_at = sqlc.arg(logged_in_at)
WHERE id = sqlc.arg(id)::uuid
  AND status = 'active'
RETURNING id, chain_id, address, display_name, role, status, created_at,
          updated_at, last_login_at;

-- name: CreateUserSession :one
INSERT INTO user_sessions (
    id, user_id, token_digest, csrf_digest, created_at, expires_at, last_used_at
) VALUES (
    sqlc.arg(id)::uuid,
    sqlc.arg(user_id)::uuid,
    sqlc.arg(token_digest),
    sqlc.arg(csrf_digest),
    sqlc.arg(created_at),
    sqlc.arg(expires_at),
    sqlc.arg(created_at)
)
RETURNING id, user_id, token_digest, csrf_digest, created_at, expires_at,
          last_used_at, revoked_at;

-- name: GetActiveUserSession :one
SELECT
    s.id AS session_id,
    s.user_id,
    s.created_at AS session_created_at,
    s.expires_at AS session_expires_at,
    s.last_used_at,
    s.csrf_digest,
    u.chain_id,
    u.address,
    u.display_name,
    u.role,
    u.status,
    u.created_at AS user_created_at,
    u.updated_at AS user_updated_at,
    u.last_login_at
FROM user_sessions AS s
JOIN users AS u ON u.id = s.user_id
WHERE s.token_digest = sqlc.arg(token_digest)
  AND s.revoked_at IS NULL
  AND s.expires_at > sqlc.arg(observed_at);

-- name: TouchActiveUserSession :exec
UPDATE user_sessions
SET last_used_at = sqlc.arg(observed_at)
WHERE id = sqlc.arg(id)::uuid
  AND revoked_at IS NULL
  AND expires_at > sqlc.arg(observed_at)
  AND last_used_at <= sqlc.arg(touch_before);

-- name: RevokeUserSessionByDigest :execrows
UPDATE user_sessions
SET revoked_at = sqlc.arg(revoked_at)
WHERE token_digest = sqlc.arg(token_digest)
  AND revoked_at IS NULL;

-- name: RevokeAllUserSessions :one
WITH revoked AS (
    UPDATE user_sessions
    SET revoked_at = sqlc.arg(revoked_at)
    WHERE user_id = sqlc.arg(user_id)::uuid
      AND revoked_at IS NULL
    RETURNING 1
)
SELECT count(*)::bigint AS revoked_sessions
FROM revoked;

-- name: GetUserByID :one
SELECT id, chain_id, address, display_name, role, status, created_at,
       updated_at, last_login_at
FROM users
WHERE id = sqlc.arg(id)::uuid
  AND chain_id = sqlc.arg(chain_id)::numeric;

-- name: GetUserByAddress :one
SELECT id, chain_id, address, display_name, role, status, created_at,
       updated_at, last_login_at
FROM users
WHERE chain_id = sqlc.arg(chain_id)::numeric
  AND address = sqlc.arg(address);

-- name: UpdateCurrentUserDisplayName :one
UPDATE users
SET display_name = sqlc.narg(display_name)::text,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)::uuid
  AND status = 'active'
RETURNING id, chain_id, address, display_name, role, status, created_at,
          updated_at, last_login_at;

-- name: ListUsersPage :many
SELECT id, chain_id, address, display_name, role, status, created_at,
       updated_at, last_login_at
FROM users
WHERE chain_id = sqlc.arg(chain_id)::numeric
  AND (
      sqlc.narg(before_created_at)::timestamptz IS NULL
      OR (created_at, id) < (
          sqlc.narg(before_created_at)::timestamptz,
          sqlc.narg(before_id)::uuid
      )
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit);

-- name: UpdateAdminUser :one
UPDATE users
SET role = COALESCE(sqlc.narg(role)::text, role),
    status = COALESCE(sqlc.narg(status)::text, status),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)::uuid
  AND chain_id = sqlc.arg(chain_id)::numeric
RETURNING id, chain_id, address, display_name, role, status, created_at,
          updated_at, last_login_at;

-- name: DeleteExpiredAuthChallenges :execrows
WITH candidates AS (
    SELECT candidate.id
    FROM auth_challenges AS candidate
    WHERE candidate.chain_id = sqlc.arg(chain_id)::numeric
      AND candidate.expires_at <= sqlc.arg(expired_before)
    ORDER BY candidate.expires_at, candidate.id
    FOR UPDATE OF candidate SKIP LOCKED
    LIMIT sqlc.arg(delete_limit)
)
DELETE FROM auth_challenges AS challenge
USING candidates
WHERE challenge.id = candidates.id;

-- name: DeleteExpiredUserSessions :execrows
WITH candidates AS (
    SELECT candidate.id
    FROM user_sessions AS candidate
    JOIN users AS candidate_user ON candidate_user.id = candidate.user_id
    WHERE candidate_user.chain_id = sqlc.arg(chain_id)::numeric
      AND (
          candidate.expires_at <= sqlc.arg(expired_before)
          OR (
              candidate.revoked_at IS NOT NULL
              AND candidate.revoked_at <= sqlc.arg(expired_before)
          )
      )
    ORDER BY candidate.expires_at, candidate.id
    FOR UPDATE OF candidate SKIP LOCKED
    LIMIT sqlc.arg(delete_limit)
)
DELETE FROM user_sessions AS session
USING candidates
WHERE session.id = candidates.id;
