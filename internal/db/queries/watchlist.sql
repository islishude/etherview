-- name: WatchLockOwner :one
SELECT id FROM users WHERE id=sqlc.arg('user_id')::uuid AND chain_id=sqlc.arg('chain_id')::numeric AND status='active' FOR UPDATE;

-- name: WatchTip :one
SELECT number::text AS block_number, block_hash FROM canonical_blocks WHERE chain_id=sqlc.arg('chain_id')::numeric ORDER BY number DESC LIMIT 1;

-- name: WatchCount :one
SELECT count(*) FROM address_watches WHERE user_id=sqlc.arg('user_id')::uuid AND deleted_at IS NULL;

-- name: WatchCreate :one
INSERT INTO address_watches(id,user_id,chain_id,address,label,kinds,direction,enabled,start_number,start_hash)
VALUES(sqlc.arg('id'),sqlc.arg('user_id'),sqlc.arg('chain_id'),sqlc.arg('address'),sqlc.arg('label'),sqlc.arg('kinds'),sqlc.arg('direction'),sqlc.arg('enabled'),sqlc.arg('start_number'),sqlc.arg('start_hash')) RETURNING *;

-- name: WatchList :many
SELECT * FROM address_watches WHERE user_id=sqlc.arg('user_id')::uuid AND chain_id=sqlc.arg('chain_id')::numeric AND deleted_at IS NULL ORDER BY created_at,id LIMIT 100;

-- name: WatchUpdate :one
UPDATE address_watches SET label=sqlc.arg('label'), kinds=sqlc.arg('kinds'), direction=sqlc.arg('direction'), enabled=sqlc.arg('enabled'),
start_number=CASE WHEN NOT enabled AND sqlc.arg('enabled') THEN sqlc.arg('start_number')::numeric ELSE start_number END,
start_hash=CASE WHEN NOT enabled AND sqlc.arg('enabled') THEN sqlc.arg('start_hash')::bytea ELSE start_hash END
WHERE id=sqlc.arg('id')::uuid AND user_id=sqlc.arg('user_id')::uuid AND deleted_at IS NULL RETURNING *;

-- name: WatchDelete :execrows
UPDATE address_watches SET deleted_at=now(),enabled=FALSE WHERE id=sqlc.arg('id')::uuid AND user_id=sqlc.arg('user_id')::uuid AND deleted_at IS NULL;

-- name: WatchNotificationSummary :one
SELECT count(*) FILTER(WHERE read_at IS NULL)::text AS unread_count, COALESCE(max(id),0)::text AS watermark
FROM watch_notifications WHERE user_id=sqlc.arg('user_id')::uuid AND created_at > now()-interval '90 days';

-- name: WatchNotificationList :many
SELECT notification.*, watch.address, watch.label,
 EXISTS(SELECT 1 FROM canonical_blocks AS canonical WHERE canonical.chain_id=notification.chain_id AND canonical.number=notification.block_number AND canonical.block_hash=notification.block_hash) AS canonical,
 (notification.source_kind='transaction' OR EXISTS(SELECT 1 FROM published_block_stage_results AS publication
 WHERE publication.chain_id=notification.chain_id AND publication.block_hash=notification.block_hash AND publication.stage='token' AND publication.stage_version=1 AND publication.state='complete' AND publication.job_generation=notification.source_generation)) AS published
FROM watch_notifications AS notification JOIN address_watches AS watch ON watch.id=notification.watch_id
WHERE notification.user_id=sqlc.arg('user_id')::uuid AND notification.created_at > now()-interval '90 days'
 AND (sqlc.arg('before_id')::bigint=0 OR notification.id < sqlc.arg('before_id'))
 AND (NOT sqlc.arg('unread_only')::boolean OR notification.read_at IS NULL)
ORDER BY notification.id DESC LIMIT sqlc.arg('page_limit')::integer;

-- name: WatchReadNotification :execrows
UPDATE watch_notifications SET read_at=COALESCE(read_at,now()) WHERE user_id=sqlc.arg('user_id')::uuid AND id=sqlc.arg('id')::bigint;

-- name: WatchReadThrough :exec
UPDATE watch_notifications SET read_at=now() WHERE user_id=sqlc.arg('user_id')::uuid AND id<=sqlc.arg('through_id')::bigint AND read_at IS NULL;

-- name: WatchClaimWork :one
WITH candidate AS (
 SELECT chain_id,block_hash FROM watch_notification_work
 WHERE chain_id=sqlc.arg('chain_id')::numeric AND generation>done_generation
 AND (lease_until IS NULL OR lease_until<clock_timestamp())
 ORDER BY block_number,block_hash FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE watch_notification_work AS work SET lease_token=sqlc.arg('lease_token')::uuid,lease_until=clock_timestamp()+interval '30 seconds'
FROM candidate WHERE work.chain_id=candidate.chain_id AND work.block_hash=candidate.block_hash RETURNING work.*;

-- name: WatchLockWork :one
SELECT * FROM watch_notification_work WHERE chain_id=sqlc.arg('chain_id')::numeric AND block_hash=sqlc.arg('block_hash')::bytea
AND lease_token=sqlc.arg('lease_token')::uuid AND lease_until>clock_timestamp() FOR UPDATE;

-- name: WatchWorkPage :many
WITH source_page AS MATERIALIZED (
 SELECT source.* FROM watch_activity_sources AS source
 WHERE source.chain_id=sqlc.arg('chain_id')::numeric
 AND source.block_hash=sqlc.arg('block_hash')::bytea
 AND source.block_number=sqlc.arg('block_number')::numeric
 AND source.block_timestamp >= extract(epoch FROM now()-interval '90 days')
 AND (source.source_key>sqlc.arg('after_source')::text
   OR (source.source_key=sqlc.arg('after_source') AND sqlc.arg('after_watch')::uuid<'ffffffff-ffff-ffff-ffff-ffffffffffff'::uuid))
 ORDER BY source.source_key LIMIT 100
), active_candidates AS MATERIALIZED (
 -- Each of the two address probes has its own indexed keyset and bound.
 SELECT source.source_key, active.*
 FROM source_page AS source
 CROSS JOIN LATERAL (
  SELECT decode(substr(source.from_address,3),'hex') AS address
  UNION SELECT decode(substr(source.to_address,3),'hex') AS address
 ) AS endpoint
 CROSS JOIN LATERAL (
  SELECT watch.id AS watch_id,watch.user_id,watch.address,watch.start_number,watch.kinds,watch.direction
  FROM address_watches AS watch
  WHERE watch.chain_id=source.chain_id AND watch.address=endpoint.address
   AND watch.deleted_at IS NULL AND watch.enabled
   AND watch.id>CASE WHEN source.source_key=sqlc.arg('after_source')
     THEN sqlc.arg('after_watch')::uuid ELSE '00000000-0000-0000-0000-000000000000'::uuid END
  ORDER BY watch.id LIMIT 201
 ) AS active
), historical_candidates AS MATERIALIZED (
 -- Repair existing records regardless of owner status or watch preferences.
 SELECT source.source_key,historical.*
 FROM source_page AS source
 CROSS JOIN LATERAL (
  SELECT existing.watch_id,existing.user_id,existing.source_kind FROM watch_notifications AS existing
  WHERE existing.chain_id=source.chain_id AND existing.block_hash=source.block_hash
   AND existing.source_key=source.source_key
   AND existing.watch_id>CASE WHEN source.source_key=sqlc.arg('after_source')
     THEN sqlc.arg('after_watch')::uuid ELSE '00000000-0000-0000-0000-000000000000'::uuid END
  ORDER BY existing.watch_id LIMIT 201
 ) AS historical
), progress AS (
 -- Ineligible candidates advance progress too; the maximum UUID ends a source.
 SELECT source_key,watch_id FROM active_candidates
 UNION SELECT source_key,watch_id FROM historical_candidates
 UNION SELECT source_key,'ffffffff-ffff-ffff-ffff-ffffffffffff'::uuid FROM source_page
), progress_page AS MATERIALIZED (
 SELECT source_key,watch_id FROM progress ORDER BY source_key,watch_id LIMIT 200
)
SELECT progress_page.watch_id,
 CASE WHEN historical.user_id IS NOT NULL AND historical.source_kind=source.source_kind THEN historical.user_id
  WHEN owner.status='active' AND active.start_number<source.block_number
   AND source.source_kind=ANY(active.kinds)
   AND ((active.direction IN ('in','both') AND active.address=decode(substr(source.to_address,3),'hex'))
     OR (active.direction IN ('out','both') AND active.address=decode(substr(source.from_address,3),'hex')))
  THEN active.user_id ELSE NULL::uuid END AS user_id,
 source.source_key::text AS source_key,source.source_kind,source.source_generation,source.activity,
 ((SELECT count(*) FROM source_page)=100)::boolean AS source_page_full
FROM progress_page JOIN source_page AS source USING (source_key)
LEFT JOIN active_candidates AS active ON active.source_key=progress_page.source_key AND active.watch_id=progress_page.watch_id
LEFT JOIN historical_candidates AS historical ON historical.source_key=progress_page.source_key AND historical.watch_id=progress_page.watch_id
LEFT JOIN users AS owner ON owner.id=active.user_id
ORDER BY progress_page.source_key,progress_page.watch_id;

-- name: WatchPublishNotification :exec
INSERT INTO watch_notifications(watch_id,user_id,chain_id,block_number,block_hash,source_key,source_kind,source_generation,activity)
VALUES(sqlc.arg('watch_id'),sqlc.arg('user_id'),sqlc.arg('chain_id'),sqlc.arg('block_number'),sqlc.arg('block_hash'),sqlc.arg('source_key'),sqlc.arg('source_kind'),sqlc.arg('source_generation'),sqlc.arg('activity'))
ON CONFLICT(watch_id,block_hash,source_key) DO UPDATE SET source_kind=EXCLUDED.source_kind, source_generation=EXCLUDED.source_generation, activity=EXCLUDED.activity;

-- name: WatchFinishPage :execrows
UPDATE watch_notification_work SET after_watch=sqlc.arg('after_watch')::uuid,after_source=sqlc.arg('after_source')::text,
 done_generation=CASE WHEN sqlc.arg('finished')::boolean THEN generation ELSE done_generation END,lease_token=NULL,lease_until=NULL
WHERE chain_id=sqlc.arg('chain_id')::numeric AND block_hash=sqlc.arg('block_hash')::bytea
 AND generation=sqlc.arg('generation')::bigint AND lease_token=sqlc.arg('lease_token')::uuid AND lease_until>clock_timestamp();

-- name: WatchCleanupNotifications :exec
DELETE FROM watch_notifications WHERE id IN (SELECT id FROM watch_notifications WHERE created_at <= now()-interval '90 days' ORDER BY created_at,id LIMIT 1000 FOR UPDATE SKIP LOCKED);

-- name: WatchCleanupWork :exec
DELETE FROM watch_notification_work WHERE (chain_id,block_hash) IN (SELECT chain_id,block_hash FROM watch_notification_work WHERE generation=done_generation AND lease_token IS NULL LIMIT 1000 FOR UPDATE SKIP LOCKED);

-- name: WatchCleanupDeleted :exec
DELETE FROM address_watches WHERE id IN (SELECT watch.id FROM address_watches AS watch WHERE deleted_at IS NOT NULL AND NOT EXISTS(SELECT 1 FROM watch_notifications AS notification WHERE notification.watch_id=watch.id) LIMIT 1000 FOR UPDATE SKIP LOCKED);

-- name: ExportLock :exec
SELECT pg_advisory_xact_lock(hashtext('etherview:address-export-admission'));

-- name: ExportAdmit :execrows
INSERT INTO address_export_admissions(token,user_id)
SELECT sqlc.arg('token')::uuid,sqlc.arg('user_id')::uuid
WHERE (SELECT count(*) FROM address_export_admissions WHERE user_id=sqlc.arg('user_id') AND started_at>clock_timestamp()-interval '1 minute')<5
AND NOT EXISTS(SELECT 1 FROM address_export_admissions WHERE user_id=sqlc.arg('user_id') AND NOT released AND expires_at>clock_timestamp())
AND (SELECT count(*) FROM address_export_admissions WHERE NOT released AND expires_at>clock_timestamp())<4;

-- name: ExportRelease :exec
UPDATE address_export_admissions SET released=TRUE WHERE token=sqlc.arg('token')::uuid;

-- name: ExportCleanup :exec
DELETE FROM address_export_admissions WHERE token IN (SELECT token FROM address_export_admissions WHERE started_at<now()-interval '1 minute' AND (released OR expires_at<now()) LIMIT 1000 FOR UPDATE SKIP LOCKED);

-- name: ExportCoverage :one
WITH boundary AS (
 SELECT canonical.number FROM canonical_blocks AS canonical JOIN blocks AS block ON block.chain_id=canonical.chain_id AND block.number=canonical.number AND block.hash=canonical.block_hash
 WHERE canonical.chain_id=sqlc.arg('chain_id')::numeric AND canonical.number<=sqlc.arg('tip')::numeric AND (block.timestamp<=sqlc.arg('from_timestamp')::numeric OR canonical.number=0)
 ORDER BY canonical.number DESC LIMIT 1
)
SELECT EXISTS(SELECT 1 FROM core_coverage_ranges AS coverage, boundary WHERE coverage.chain_id=sqlc.arg('chain_id') AND coverage.range_start<=boundary.number AND coverage.range_end>=sqlc.arg('tip')) AS core_complete,
 NOT EXISTS(SELECT 1 FROM canonical_blocks AS canonical JOIN blocks AS block ON block.chain_id=canonical.chain_id AND block.number=canonical.number AND block.hash=canonical.block_hash
 WHERE canonical.chain_id=sqlc.arg('chain_id') AND canonical.number<=sqlc.arg('tip') AND block.timestamp>=sqlc.arg('from_timestamp') AND block.timestamp<sqlc.arg('to_timestamp')::numeric
 AND NOT EXISTS(SELECT 1 FROM published_block_stage_results AS publication WHERE publication.chain_id=canonical.chain_id AND publication.block_hash=canonical.block_hash AND publication.stage='token' AND publication.stage_version=1 AND publication.state='complete')) AS token_complete;

-- name: ExportActivity :many
SELECT source.activity FROM watch_activity_sources AS source
WHERE source.chain_id=sqlc.arg('chain_id')::numeric AND source.block_number<=sqlc.arg('tip')::numeric
AND source.block_timestamp>=sqlc.arg('from_timestamp')::numeric AND source.block_timestamp<sqlc.arg('to_timestamp')::numeric
AND (source.source_kind=sqlc.arg('kind')::text OR (sqlc.arg('kind')='nft' AND source.source_kind IN ('erc721','erc1155')))
AND ((sqlc.arg('direction')::text IN ('in','both') AND source.to_address=sqlc.arg('address')::text)
 OR (sqlc.arg('direction') IN ('out','both') AND source.from_address=sqlc.arg('address')))
ORDER BY source.block_number,source.tx_index,source.source_key LIMIT 10001;
