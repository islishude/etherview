-- name: VerifyLockBudget :exec
SELECT lock_verification_budget();

-- name: VerifyConfigureBudget :exec
UPDATE verification_budget SET max_jobs=sqlc.arg('max_jobs'), max_bytes=sqlc.arg('max_bytes'),
 max_user_jobs=sqlc.arg('max_user_jobs'),max_input_bytes=sqlc.arg('max_input_bytes')
WHERE singleton AND ROW(max_jobs,max_bytes,max_user_jobs,max_input_bytes) IS DISTINCT FROM
 ROW(sqlc.arg('max_jobs')::bigint,sqlc.arg('max_bytes')::bigint,sqlc.arg('max_user_jobs')::bigint,sqlc.arg('max_input_bytes')::bigint);

-- name: VerifyCleanupInputs :execrows
WITH candidates AS (
 SELECT id FROM verification_jobs job WHERE status IN ('failed','cancelled') AND request_payload IS NOT NULL
 AND updated_at<clock_timestamp()-INTERVAL '7 days'
 AND NOT EXISTS(SELECT 1 FROM verification_results result WHERE result.job_id=job.id)
 AND NOT EXISTS(SELECT 1 FROM verification_compilation_units unit WHERE unit.source_job_id=job.id)
 ORDER BY updated_at,id FOR UPDATE SKIP LOCKED LIMIT 100
), removed AS (
 UPDATE verification_import_inputs SET payload=NULL WHERE job_id IN(SELECT id FROM candidates)
)
UPDATE verification_jobs SET request_payload=NULL WHERE id IN(SELECT id FROM candidates);
