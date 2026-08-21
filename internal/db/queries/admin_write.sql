-- name: AdminWriteEnqueueRepairStatement1 :many
INSERT INTO repair_requests (
			chain_id, operation, stage, from_block, to_block, allow_finalized, reason
		) VALUES ($1::numeric, $2, $3, $4::numeric, $5::numeric, $6, $7)
			RETURNING id, status, requested_at;
