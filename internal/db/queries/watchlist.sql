-- name: WatchLockOwner :one
SELECT id FROM users WHERE id=sqlc.arg('user_id')::uuid AND chain_id=sqlc.arg('chain_id')::numeric AND status='active' FOR UPDATE;

-- name: WatchTip :one
SELECT number::text AS block_number, block_hash FROM canonical_blocks WHERE chain_id=sqlc.arg('chain_id')::numeric ORDER BY number DESC LIMIT 1;

-- name: WatchCount :one
SELECT count(*) FROM address_watches WHERE user_id=sqlc.arg('user_id')::uuid;

-- name: WatchCreate :one
INSERT INTO address_watches(id,user_id,chain_id,address,label,kinds,direction,enabled,start_number,start_hash)
VALUES(sqlc.arg('id'),sqlc.arg('user_id'),sqlc.arg('chain_id'),sqlc.arg('address'),sqlc.arg('label'),sqlc.arg('kinds'),sqlc.arg('direction'),sqlc.arg('enabled'),sqlc.arg('start_number'),sqlc.arg('start_hash')) RETURNING *;

-- name: WatchList :many
SELECT * FROM address_watches WHERE user_id=sqlc.arg('user_id')::uuid AND chain_id=sqlc.arg('chain_id')::numeric ORDER BY created_at,id LIMIT 5;

-- name: WatchUpdate :one
UPDATE address_watches SET label=sqlc.arg('label'), kinds=sqlc.arg('kinds'), direction=sqlc.arg('direction'), enabled=sqlc.arg('enabled'),
start_number=CASE WHEN NOT enabled AND sqlc.arg('enabled') THEN sqlc.arg('start_number')::numeric ELSE start_number END,
start_hash=CASE WHEN NOT enabled AND sqlc.arg('enabled') THEN sqlc.arg('start_hash')::bytea ELSE start_hash END
WHERE id=sqlc.arg('id')::uuid AND user_id=sqlc.arg('user_id')::uuid RETURNING *;

-- name: WatchDelete :execrows
DELETE FROM address_watches WHERE id=sqlc.arg('id')::uuid AND user_id=sqlc.arg('user_id')::uuid;

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
 AND NOT EXISTS(SELECT 1 FROM published_block_stage_results AS publication WHERE publication.chain_id=canonical.chain_id AND publication.block_hash=canonical.block_hash AND publication.stage='token' AND publication.stage_version=2 AND publication.state='complete')) AS token_complete;

-- name: ExportActivity :many
SELECT source.activity FROM watch_activity_sources AS source
WHERE source.chain_id=sqlc.arg('chain_id')::numeric AND source.block_number<=sqlc.arg('tip')::numeric
AND source.block_timestamp>=sqlc.arg('from_timestamp')::numeric AND source.block_timestamp<sqlc.arg('to_timestamp')::numeric
AND (source.source_kind=sqlc.arg('kind')::text OR (sqlc.arg('kind')='nft' AND source.source_kind IN ('erc721','erc1155')))
AND ((sqlc.arg('direction')::text IN ('in','both') AND source.to_address=sqlc.arg('address')::text)
 OR (sqlc.arg('direction') IN ('out','both') AND source.from_address=sqlc.arg('address')))
ORDER BY source.block_number,source.tx_index,source.source_key LIMIT 10001;
