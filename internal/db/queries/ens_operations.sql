-- name: RecordENSSyncImport :exec
INSERT INTO ens_sync_imports(source_id,batch_id,digest,count) VALUES(@source_id,@batch_id,@digest,@count)
ON CONFLICT(source_id,batch_id) DO UPDATE SET digest=EXCLUDED.digest,count=EXCLUDED.count;

-- name: ListENSSyncReplayCandidates :many
SELECT id,requested_generation FROM ens_sync_candidates WHERE source_id= @source_id AND id> @after_candidate ORDER BY id LIMIT @row_limit;

-- name: RecordENSSyncReplay :one
INSERT INTO ens_sync_replays(source_id,batch_id,row_limit,after_candidate,through_candidate,count,reason)
VALUES(@source_id,@batch_id,@row_limit,@after_candidate,@through_candidate,@count,@reason) RETURNING id;

-- name: GetENSSyncReplay :one
SELECT * FROM ens_sync_replays WHERE source_id= @source_id AND batch_id= @batch_id;
