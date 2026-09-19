-- name: AdminWriteEnqueueRepairStatement1 :one
INSERT INTO repair_requests (
			chain_id, operation, stage, from_block, to_block, allow_finalized, reason
		) VALUES (sqlc.arg('chain_id')::numeric, sqlc.arg('operation'), sqlc.arg('stage'), sqlc.arg('from_block')::numeric, sqlc.arg('to_block')::numeric, sqlc.arg('allow_finalized'), sqlc.arg('reason'))
			RETURNING id, status, requested_at;
