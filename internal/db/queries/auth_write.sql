-- name: AuthWritePutStatement1 :exec
INSERT INTO api_keys (
			prefix, digest, name, rate_per_second, burst, created_at, revoked_at,
			owner_user_id, scopes
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: AuthWriteRevokeStatement1 :exec
UPDATE api_keys
		SET revoked_at = COALESCE(revoked_at, $2)
		WHERE prefix = $1;

-- name: AuthWriteRotateStatement1 :exec
INSERT INTO api_keys (
			prefix, digest, name, rate_per_second, burst, created_at, revoked_at,
			owner_user_id, scopes
		) VALUES ($1, $2, $3, $4, $5, $6, NULL, $7, $8);

-- name: AuthWriteRotateStatement2 :exec
UPDATE api_keys
		SET revoked_at = $2
			WHERE prefix = $1 AND revoked_at IS NULL;
