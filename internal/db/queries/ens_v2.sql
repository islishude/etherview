-- name: ListENSSyncEmitterDeployments :many
SELECT d.*,b.number FROM ens_sync_deployments d
JOIN ens_sync_blocks b ON b.source_id=d.source_id AND b.hash=d.block_hash AND b.canonical
WHERE d.source_id= @source_id AND d.address=ANY(@addresses::bytea[]);

-- name: DirtyENSSyncGraph :exec
INSERT INTO ens_sync_graph_work(source_id) VALUES (@source_id)
ON CONFLICT(source_id) DO UPDATE SET requested_revision=ens_sync_graph_work.requested_revision+1,pending=true;

-- name: GetENSSyncGraphWork :one
SELECT * FROM ens_sync_graph_work WHERE source_id= @source_id AND pending FOR UPDATE;

-- name: AdvanceENSSyncGraphWork :exec
UPDATE ens_sync_graph_work SET after_key= @after_key,
 pending=CASE WHEN @complete::boolean THEN requested_revision<>running_revision ELSE true END,
 running_revision=CASE WHEN @complete::boolean THEN requested_revision ELSE running_revision END
WHERE source_id= @source_id;

-- name: ListENSSyncV2Registrations :many
SELECT p.* FROM ens_sync_projections p JOIN ens_sync_blocks b ON b.source_id=p.source_id AND b.hash=p.block_hash AND b.canonical
WHERE p.source_id= @source_id AND p.kind='registration' AND p.key> @after_key AND p.payload->>'protocol'='v2'
AND NOT EXISTS(SELECT 1 FROM ens_sync_projections newer JOIN ens_sync_blocks nb ON nb.source_id=newer.source_id AND nb.hash=newer.block_hash AND nb.canonical
 WHERE newer.source_id=p.source_id AND newer.kind=p.kind AND newer.key=p.key AND newer.revision>p.revision)
ORDER BY p.key LIMIT @row_limit;

-- name: ListENSSyncV2Parents :many
SELECT p.* FROM ens_sync_projections p JOIN ens_sync_blocks b ON b.source_id=p.source_id AND b.hash=p.block_hash AND b.canonical
WHERE p.source_id= @source_id AND p.kind='registration' AND p.payload->>'protocol'='v2' AND p.payload->>'subregistry'= @registry::text
AND NOT EXISTS(SELECT 1 FROM ens_sync_projections newer JOIN ens_sync_blocks nb ON nb.source_id=newer.source_id AND nb.hash=newer.block_hash AND nb.canonical
 WHERE newer.source_id=p.source_id AND newer.kind=p.kind AND newer.key=p.key AND newer.revision>p.revision)
ORDER BY p.key LIMIT @row_limit;
