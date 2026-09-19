-- name: VerifiedSelectorWritePersistStatement1 :execrows
INSERT INTO verified_function_selector_sets (
			verification_job_id, request_digest, chain_id, address, code_hash,
			valid_from_block, status, function_count, warning
		) VALUES (sqlc.arg('verification_job_id')::uuid, sqlc.arg('request_digest'), sqlc.arg('chain_id')::numeric, sqlc.arg('address'), sqlc.arg('code_hash'), sqlc.arg('valid_from_block')::numeric, sqlc.arg('status'), sqlc.arg('function_count'), sqlc.arg('warning'))
		ON CONFLICT (verification_job_id) DO NOTHING;

-- name: VerifiedSelectorWritePersistStatement2 :exec
INSERT INTO verified_function_selectors (
				verification_job_id, chain_id, address, code_hash,
				selector, signature, function_name, abi_entry
				) VALUES (sqlc.arg('verification_job_id')::uuid, sqlc.arg('chain_id')::numeric, sqlc.arg('address'), sqlc.arg('code_hash'), sqlc.arg('selector'), sqlc.arg('signature'), sqlc.arg('function_name'), sqlc.arg('abi_entry')::jsonb);
