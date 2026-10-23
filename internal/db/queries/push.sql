-- name: PushLockSubscriptions :exec
SELECT pg_advisory_xact_lock(hashtext('etherview:push-subscriptions'));

-- name: PushSession :one
SELECT s.expires_at FROM user_sessions s JOIN users u ON u.id=s.user_id
WHERE s.id=sqlc.arg(session_id)::uuid AND s.user_id=sqlc.arg(user_id)::uuid
AND u.chain_id=sqlc.arg(chain_id)::numeric AND u.status='active' AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp();

-- name: PushList :many
SELECT p.id,p.endpoint_hash,p.language,s.expires_at FROM push_subscriptions p JOIN user_sessions s ON s.id=p.session_id AND s.user_id=p.user_id
WHERE p.user_id=sqlc.arg(user_id)::uuid AND p.session_id=sqlc.arg(session_id)::uuid ORDER BY p.id LIMIT 10;

-- name: PushCount :one
SELECT count(*) FROM push_subscriptions p JOIN user_sessions s ON s.id=p.session_id AND s.user_id=p.user_id
WHERE p.user_id=sqlc.arg(user_id)::uuid AND p.endpoint<>sqlc.arg(endpoint)::text AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp();

-- name: PushSave :one
INSERT INTO push_subscriptions(id,user_id,session_id,endpoint,endpoint_hash,p256dh,auth,language,start_number,start_hash)
VALUES(sqlc.arg(id),sqlc.arg(user_id),sqlc.arg(session_id),sqlc.arg(endpoint),sqlc.arg(endpoint_hash),sqlc.arg(p256dh),sqlc.arg(auth),sqlc.arg(language),sqlc.arg(start_number),sqlc.arg(start_hash))
ON CONFLICT(endpoint) DO UPDATE SET user_id=EXCLUDED.user_id,session_id=EXCLUDED.session_id,
p256dh=EXCLUDED.p256dh,auth=EXCLUDED.auth,language=EXCLUDED.language,
start_number=CASE WHEN push_subscriptions.session_id=EXCLUDED.session_id THEN push_subscriptions.start_number ELSE EXCLUDED.start_number END,
start_hash=CASE WHEN push_subscriptions.session_id=EXCLUDED.session_id THEN push_subscriptions.start_hash ELSE EXCLUDED.start_hash END
RETURNING id,endpoint_hash,language;

-- name: PushDelete :execrows
DELETE FROM push_subscriptions WHERE id=sqlc.arg(id)::uuid AND user_id=sqlc.arg(user_id)::uuid AND session_id=sqlc.arg(session_id)::uuid;

-- name: PushDeleteExpiredEndpoint :exec
DELETE FROM push_subscriptions WHERE id=sqlc.arg(id)::uuid AND session_id=sqlc.arg(session_id)::uuid AND p256dh=sqlc.arg(p256dh)::text AND auth=sqlc.arg(auth)::text;

-- name: PushBoundary :one
SELECT COALESCE((SELECT max(id) FROM transactional_outbox WHERE chain_id=sqlc.arg(chain_id)::numeric),0)::bigint AS core_id, clock_timestamp()::timestamptz AS token_time;

-- name: PushCoreSources :many
SELECT id,payload FROM transactional_outbox
WHERE chain_id=sqlc.arg(chain_id)::numeric AND id>sqlc.arg(after_id)::bigint
AND topic='core.block.canonical' AND generation=1 ORDER BY id LIMIT 100;

-- name: PushTokenSources :many
SELECT p.committed_at,p.job_id,p.job_generation,p.block_number::text AS block_number,p.block_hash
FROM durable_stage_publications p
WHERE p.chain_id=sqlc.arg(chain_id)::numeric AND p.stage='token' AND p.stage_version=2 AND p.state='complete'
AND (p.committed_at,p.job_id,p.job_generation)>(sqlc.arg(after_time)::timestamptz,sqlc.arg(after_job)::bigint,sqlc.arg(after_generation)::bigint)
AND NOT EXISTS(SELECT 1 FROM durable_stage_publications older WHERE older.job_id=p.job_id AND older.job_generation<p.job_generation AND older.state='complete')
ORDER BY p.committed_at,p.job_id,p.job_generation LIMIT 100;

-- name: PushActivity :many
SELECT source_key::text AS source_key,source_generation,source_kind,activity,
decode(substr(from_address,3),'hex')::bytea AS from_address,
decode(substr(to_address,3),'hex')::bytea AS to_address FROM watch_activity_sources
WHERE chain_id=sqlc.arg(chain_id)::numeric AND block_hash=sqlc.arg(block_hash)::bytea
AND source_key>sqlc.arg(after_source)::text AND ((sqlc.arg(token)::boolean AND source_kind<>'transaction') OR (NOT sqlc.arg(token)::boolean AND source_kind='transaction'))
ORDER BY source_key LIMIT 100;

-- name: PushFollowers :many
WITH candidates AS MATERIALIZED (
 SELECT w.* FROM (SELECT sqlc.arg(from_address)::bytea AS address UNION SELECT sqlc.arg(to_address)::bytea) endpoint
 CROSS JOIN LATERAL (
  SELECT w.* FROM address_watches w WHERE w.chain_id=sqlc.arg(chain_id)::numeric AND w.address=endpoint.address
  AND w.enabled AND w.id>sqlc.arg(after_watch)::uuid ORDER BY w.id LIMIT 201
 ) w
)
SELECT * FROM candidates ORDER BY id LIMIT 200;

-- name: PushRecipients :many
SELECT p.* FROM push_subscriptions p JOIN user_sessions s ON s.id=p.session_id AND s.user_id=p.user_id JOIN users u ON u.id=p.user_id
WHERE p.user_id=sqlc.arg(user_id)::uuid AND p.start_number<sqlc.arg(block_number)::numeric
AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp() AND u.status='active' ORDER BY p.id LIMIT 10;

-- name: PushValidateDelivery :one
SELECT EXISTS(
SELECT 1 FROM push_subscriptions p JOIN user_sessions s ON s.id=p.session_id AND s.user_id=p.user_id JOIN users u ON u.id=p.user_id
JOIN address_watches w ON w.user_id=p.user_id AND w.id=sqlc.arg(watch_id)::uuid
JOIN canonical_blocks c ON c.chain_id=w.chain_id AND c.number=sqlc.arg(block_number)::numeric AND c.block_hash=sqlc.arg(block_hash)::bytea
WHERE p.id=sqlc.arg(subscription_id)::uuid AND p.session_id=sqlc.arg(session_id)::uuid AND p.p256dh=sqlc.arg(p256dh)::text AND p.auth=sqlc.arg(auth)::text
AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp() AND u.status='active'
AND w.enabled AND w.start_number<c.number AND p.start_number<c.number
AND sqlc.arg(kind)::text=ANY(w.kinds)
AND ((w.direction IN ('in','both') AND w.address=sqlc.arg(to_address)::bytea) OR (w.direction IN ('out','both') AND w.address=sqlc.arg(from_address)::bytea))
AND (sqlc.arg(kind)='transaction' OR EXISTS(SELECT 1 FROM published_block_stage_results pub WHERE pub.chain_id=c.chain_id AND pub.block_hash=c.block_hash AND pub.stage='token' AND pub.stage_version=2 AND pub.state='complete' AND pub.job_generation=sqlc.arg(source_generation)::bigint))
)::boolean;

-- name: PushLeaderLock :one
SELECT pg_try_advisory_lock(hashtext('etherview:browser-push'),hashtext(sqlc.arg(chain_id)::text));

-- name: PushLeaderUnlock :one
SELECT pg_advisory_unlock(hashtext('etherview:browser-push'),hashtext(sqlc.arg(chain_id)::text));

-- name: PushCleanup :exec
DELETE FROM push_subscriptions WHERE id IN (
SELECT p.id FROM push_subscriptions p JOIN user_sessions s ON s.id=p.session_id AND s.user_id=p.user_id
WHERE s.revoked_at IS NOT NULL OR s.expires_at<=clock_timestamp() LIMIT 1000);
