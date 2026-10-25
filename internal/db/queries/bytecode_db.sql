-- name: BytecodeDBInit :exec
INSERT INTO bytecode_db_services(id) VALUES (sqlc.arg('id')) ON CONFLICT DO NOTHING;

-- name: BytecodeDBLockService :one
SELECT * FROM bytecode_db_services WHERE id=sqlc.arg('id') FOR UPDATE;

-- name: BytecodeDBInitScan :exec
INSERT INTO bytecode_db_scans(service_id,chain_id) VALUES (sqlc.arg('service_id'),sqlc.arg('chain_id')) ON CONFLICT DO NOTHING;

-- name: BytecodeDBLockScan :one
SELECT * FROM bytecode_db_scans WHERE service_id=sqlc.arg('service_id') AND chain_id=sqlc.arg('chain_id') FOR UPDATE;

-- name: BytecodeDBDiscover :many
SELECT DISTINCT o.address FROM contract_code_observations o
JOIN canonical_blocks c ON c.chain_id=o.chain_id AND c.number=o.block_number AND c.block_hash=o.block_hash
WHERE o.chain_id=sqlc.arg('chain_id') AND o.canonical AND o.address>sqlc.arg('address') AND octet_length(o.code)>0
ORDER BY o.address LIMIT 100;

-- name: BytecodeDBAdvance :exec
UPDATE bytecode_db_scans SET cursor_address=sqlc.arg('cursor_address'),scan_after=clock_timestamp()+(sqlc.arg('delay_us')::bigint*interval '1 microsecond') WHERE service_id=sqlc.arg('service_id') AND chain_id=sqlc.arg('chain_id');

-- name: BytecodeDBEnqueueTarget :exec
INSERT INTO bytecode_db_targets(service_id,chain_id,address) VALUES (sqlc.arg('service_id'),sqlc.arg('chain_id'),sqlc.arg('address'))
ON CONFLICT (service_id,chain_id,address) DO UPDATE SET next_attempt_at=LEAST(bytecode_db_targets.next_attempt_at,clock_timestamp()+interval '60 seconds');

-- name: BytecodeDBClaimTarget :one
UPDATE bytecode_db_targets SET lease_token=sqlc.arg('token'),lease_expires_at=clock_timestamp()+interval '60 seconds'
WHERE (service_id,chain_id,address)=(
 SELECT candidate.service_id,candidate.chain_id,candidate.address FROM bytecode_db_targets candidate
 WHERE candidate.service_id=sqlc.arg('service_id') AND candidate.chain_id=sqlc.arg('chain_id') AND candidate.next_attempt_at<=clock_timestamp()
 AND (candidate.lease_expires_at IS NULL OR candidate.lease_expires_at<=clock_timestamp())
 ORDER BY candidate.next_attempt_at,candidate.address FOR UPDATE SKIP LOCKED LIMIT 1
) RETURNING *;

-- name: BytecodeDBLockTarget :one
SELECT * FROM bytecode_db_targets
WHERE service_id=sqlc.arg('service_id') AND chain_id=sqlc.arg('chain_id') AND address=sqlc.arg('address') AND lease_token=sqlc.arg('lease_token') AND lease_expires_at>clock_timestamp() FOR UPDATE;

-- name: BytecodeDBFinishTarget :execrows
UPDATE bytecode_db_targets SET outcome=sqlc.arg('outcome'),next_attempt_at=clock_timestamp()+(sqlc.arg('delay_us')::bigint*interval '1 microsecond'),lease_token=NULL,lease_expires_at=NULL
WHERE service_id=sqlc.arg('service_id') AND chain_id=sqlc.arg('chain_id') AND address=sqlc.arg('address') AND lease_token=sqlc.arg('token') AND lease_expires_at>clock_timestamp();

-- name: BytecodeDBEnqueueLookup :exec
INSERT INTO bytecode_db_lookups(service_id,bytecode_type,digest,bytecode) VALUES (sqlc.arg('service_id'),sqlc.arg('bytecode_type'),sqlc.arg('digest'),sqlc.arg('bytecode')) ON CONFLICT (service_id,bytecode_type,digest) DO UPDATE SET requested=true
WHERE bytecode_db_lookups.next_attempt_at<=clock_timestamp();

-- name: BytecodeDBGetLookup :one
SELECT * FROM bytecode_db_lookups WHERE service_id=sqlc.arg('service_id') AND bytecode_type=sqlc.arg('bytecode_type') AND digest=sqlc.arg('digest');

-- name: BytecodeDBActiveLookups :one
SELECT count(*) AS count FROM bytecode_db_lookups WHERE service_id=sqlc.arg('service_id') AND lease_expires_at>clock_timestamp();

-- name: BytecodeDBClaimLookup :one
UPDATE bytecode_db_lookups SET lease_token=sqlc.arg('token'),lease_expires_at=clock_timestamp()+interval '60 seconds'
WHERE (service_id,bytecode_type,digest)=(
 SELECT candidate.service_id,candidate.bytecode_type,candidate.digest FROM bytecode_db_lookups candidate
 WHERE candidate.service_id=sqlc.arg('service_id') AND candidate.requested AND candidate.next_attempt_at<=clock_timestamp()
 AND (candidate.lease_expires_at IS NULL OR candidate.lease_expires_at<=clock_timestamp())
 ORDER BY candidate.next_attempt_at,candidate.digest FOR UPDATE SKIP LOCKED LIMIT 1
) RETURNING *;

-- name: BytecodeDBAdmitRequest :exec
UPDATE bytecode_db_services SET next_request_at=clock_timestamp()+(sqlc.arg('interval_us')::bigint*interval '1 microsecond') WHERE id=sqlc.arg('id');

-- name: BytecodeDBRenewLookup :execrows
UPDATE bytecode_db_lookups SET lease_expires_at=clock_timestamp()+interval '60 seconds'
WHERE service_id=sqlc.arg('service_id') AND bytecode_type=sqlc.arg('bytecode_type') AND digest=sqlc.arg('digest') AND lease_token=sqlc.arg('lease_token') AND lease_expires_at>clock_timestamp();

-- name: BytecodeDBFinishLookup :execrows
UPDATE bytecode_db_lookups SET requested=sqlc.arg('retry')::boolean,sources=sqlc.narg('sources')::jsonb,limited=sqlc.arg('limited'),outcome=sqlc.arg('outcome'),failures=sqlc.arg('failures'),next_attempt_at=clock_timestamp()+(sqlc.arg('delay_us')::bigint*interval '1 microsecond'),lease_token=NULL,lease_expires_at=NULL
WHERE service_id=sqlc.arg('service_id') AND bytecode_type=sqlc.arg('bytecode_type') AND digest=sqlc.arg('digest') AND lease_token=sqlc.arg('token') AND lease_expires_at>clock_timestamp();

-- name: BytecodeDBAttempt :one
SELECT j.id::text AS job_id,j.status,j.outcome_kind FROM bytecode_db_attempts a JOIN verification_jobs j ON j.id=a.job_id
WHERE service_id=sqlc.arg('service_id') AND target_digest=sqlc.arg('target_digest') AND candidate_digest=sqlc.arg('candidate_digest') AND support_digest=sqlc.arg('support_digest');

-- name: BytecodeDBAddAttempt :exec
INSERT INTO bytecode_db_attempts(service_id,target_digest,candidate_digest,support_digest,job_id) VALUES (sqlc.arg('service_id'),sqlc.arg('target_digest'),sqlc.arg('candidate_digest'),sqlc.arg('support_digest'),sqlc.arg('job_id'));

-- name: BytecodeDBRetryUnavailable :execrows
UPDATE verification_jobs j
SET status='queued', error_code=NULL, attempt_count=0, updated_at=clock_timestamp()
FROM bytecode_db_attempts a
WHERE a.job_id=j.id AND a.service_id=sqlc.arg('service_id')
AND a.target_digest=sqlc.arg('target_digest') AND a.candidate_digest=sqlc.arg('candidate_digest')
AND a.support_digest=sqlc.arg('support_digest')
AND j.status='failed' AND j.error_code IN (
 'compiler_unavailable','compiler_download_unavailable','compiler_download_timeout','compiler_download_rate_limited'
)
AND j.updated_at<=clock_timestamp()-(sqlc.arg('cooldown_us')::bigint*interval '1 microsecond')
AND NOT EXISTS (SELECT 1 FROM verification_jobs active WHERE active.request_digest=j.request_digest AND active.id<>j.id AND active.status IN ('queued','running','succeeded'));

-- name: BytecodeDBOrigin :one
SELECT EXISTS(SELECT 1 FROM bytecode_db_attempts WHERE job_id=sqlc.arg('job_id'));

-- name: BytecodeDBBacklog :one
SELECT count(*) AS count FROM bytecode_db_targets WHERE service_id=sqlc.arg('service_id') AND chain_id=sqlc.arg('chain_id') AND next_attempt_at<=clock_timestamp();

-- name: VerifyLockClaimGate :exec
SELECT singleton FROM verification_claim_gate WHERE singleton FOR UPDATE;

-- name: BytecodeDBResetCooldowns :exec
UPDATE bytecode_db_lookups SET next_attempt_at=clock_timestamp(), failures=0,requested=true
WHERE service_id=sqlc.arg('service_id') AND lease_token IS NULL;

-- name: VerifyLockPublication :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg('chain_id')::text || ':' || encode(sqlc.arg('address')::bytea,'hex'), 0));

-- name: BytecodeDBHasPublication :one
SELECT EXISTS(
 SELECT 1 FROM verified_contracts v
 JOIN verification_jobs j ON j.id=v.verification_job_id
 JOIN canonical_blocks c ON c.chain_id=j.chain_id AND c.block_hash=j.block_hash
 JOIN contract_code_observations current ON current.chain_id=v.chain_id AND current.address=v.address AND current.code_hash=v.code_hash
 WHERE v.chain_id=sqlc.arg('chain_id') AND v.address=sqlc.arg('address') AND v.code_hash=sqlc.arg('code_hash')
 AND current.block_hash=sqlc.arg('block_hash') AND current.canonical
 AND v.valid_from_block<=current.block_number AND (v.valid_to_block IS NULL OR current.block_number<=v.valid_to_block)
);

-- name: BytecodeDBRequestReady :one
SELECT next_request_at<=clock_timestamp() AS ready FROM bytecode_db_services WHERE id=sqlc.arg('id');

-- name: BytecodeDBScanReady :one
SELECT scan_after<=clock_timestamp() AS ready FROM bytecode_db_scans WHERE service_id=sqlc.arg('service_id') AND chain_id=sqlc.arg('chain_id');

-- name: BytecodeDBBackoffService :exec
UPDATE bytecode_db_services SET next_request_at=GREATEST(next_request_at,clock_timestamp()+(sqlc.arg('delay_us')::bigint*interval '1 microsecond')) WHERE id=sqlc.arg('id');

-- name: BytecodeDBRenewTarget :execrows
UPDATE bytecode_db_targets SET lease_expires_at=clock_timestamp()+interval '60 seconds'
WHERE service_id=sqlc.arg('service_id') AND chain_id=sqlc.arg('chain_id') AND address=sqlc.arg('address') AND lease_token=sqlc.arg('lease_token') AND lease_expires_at>clock_timestamp();
