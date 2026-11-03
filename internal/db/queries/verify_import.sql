-- name: VerifyImportSaveInput :execrows
INSERT INTO verification_import_inputs (job_id, request, payload, digest)
SELECT id, sqlc.arg('request')::jsonb, sqlc.arg('payload')::bytea, sqlc.arg('digest')::bytea
FROM verification_jobs
WHERE id = sqlc.arg('id')::uuid AND kind = 'sourcify_import'
  AND status = 'running' AND lease_token = sqlc.arg('token') AND lease_expires_at > clock_timestamp()
ON CONFLICT (job_id) DO NOTHING;

-- name: VerifyImportBindInput :execrows
UPDATE verification_jobs AS job
SET language = input.request->>'language', compiler_version = input.request->>'compiler_version',
    catalog_language = CASE WHEN input.request->>'language' = 'vyper' THEN 'vyper' ELSE 'solidity' END
FROM verification_import_inputs AS input
WHERE job.id = input.job_id AND job.id = sqlc.arg('id')::uuid
  AND job.status = 'running' AND job.lease_token = sqlc.arg('token') AND job.lease_expires_at > clock_timestamp()
  AND job.language IS NULL;

-- name: VerifyImportAwaitCompiler :execrows
UPDATE verification_jobs
SET status = 'queued', leased_by = NULL, lease_token = NULL, lease_expires_at = NULL,
    attempt_count = attempt_count - 1, updated_at = clock_timestamp()
WHERE id = sqlc.arg('id')::uuid AND kind = 'sourcify_import'
  AND language IS NOT NULL AND status = 'running'
  AND lease_token = sqlc.arg('token') AND lease_expires_at > clock_timestamp();
