-- name: VerifyVyperPersistRuntime :exec
UPDATE compiler_catalog_entries SET vyper_runtimes = $3::jsonb, expires_at = $4
WHERE generation_id = $1 AND version = $2 AND language = 'vyper' AND vyper_runtimes IS NULL;

-- name: VerifyVyperRuntime :many
SELECT generation_id, version, artifact_sha256, vyper_runtimes
FROM compiler_catalog_entries WHERE generation_id = $1 AND version = $2 AND language = 'vyper';
