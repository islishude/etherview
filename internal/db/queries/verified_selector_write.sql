-- name: VerifiedSelectorWritePersistStatement1 :exec
INSERT INTO verified_function_selector_sets (
			verification_job_id, request_digest, chain_id, address, code_hash,
			valid_from_block, status, function_count, warning
		) VALUES ($1::uuid, $2, $3::numeric, $4, $5, $6::numeric, $7, $8, $9)
		ON CONFLICT (verification_job_id) DO NOTHING;

-- name: VerifiedSelectorWritePersistStatement2 :exec
INSERT INTO verified_function_selectors (
				verification_job_id, chain_id, address, code_hash,
				selector, signature, function_name, abi_entry
				) VALUES ($1::uuid, $2::numeric, $3, $4, $5, $6, $7, $8::jsonb);
