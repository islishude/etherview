-- name: VerifyVyperPersistRuntime :exec
UPDATE compiler_catalog_entries SET vyper_runtimes = sqlc.arg('vyper_runtimes')::jsonb, expires_at = sqlc.arg('expires_at')
WHERE generation_id = sqlc.arg('generation_id') AND version = sqlc.arg('version') AND language = 'vyper' AND vyper_runtimes IS NULL;

-- name: VerifyVyperRuntime :one
SELECT generation_id, version, artifact_sha256, vyper_runtimes
FROM compiler_catalog_entries WHERE generation_id = $1 AND version = $2 AND language = 'vyper';
