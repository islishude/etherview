-- name: StateWriteClassifyBalancePersistenceMissStatement1 :many
SELECT
			EXISTS (
				SELECT 1 FROM canonical_blocks
					WHERE chain_id = $1::numeric AND number = $5::numeric AND block_hash = $6::bytea
			),
			EXISTS (
				SELECT 1 FROM erc1155_balance_reconciliations
					WHERE chain_id = $1::numeric AND token_address = $2::bytea
					  AND token_id = $3::numeric AND owner_address = $4::bytea AND block_hash = $6::bytea
			);

-- name: StateWriteClassifyOwnerPersistenceMissStatement1 :many
SELECT
			EXISTS (
				SELECT 1 FROM canonical_blocks
					WHERE chain_id = $1::numeric AND number = $4::numeric AND block_hash = $5::bytea
			),
			EXISTS (
				SELECT 1 FROM erc721_owner_reconciliations
					WHERE chain_id = $1::numeric AND token_address = $2::bytea
					  AND token_id = $3::numeric AND block_hash = $5::bytea
			);

-- name: StateWriteInsertERC1155BalanceStatement1 :exec
INSERT INTO erc1155_balance_reconciliations AS current (
			chain_id, token_address, token_id, owner_address,
			block_number, block_hash, balance, confidence
		)
		SELECT $1::numeric, $2, $3::numeric, $4,
		       $5::numeric, $6, $7::numeric, 'rpc_exact'
		FROM canonical_blocks AS canonical
		WHERE canonical.chain_id = $1::numeric
		  AND canonical.number = $5::numeric
		  AND canonical.block_hash = $6
			ON CONFLICT (chain_id, token_address, token_id, owner_address, block_hash) DO UPDATE SET
				observed_at = current.observed_at
			WHERE current.block_number = EXCLUDED.block_number
			  AND current.balance = EXCLUDED.balance
			  AND current.confidence = EXCLUDED.confidence;

-- name: StateWriteInsertOwnerObservationStatement1 :exec
INSERT INTO erc721_owner_reconciliations AS current (
			chain_id, token_address, token_id, block_number, block_hash,
			state, owner_address, confidence
		)
		SELECT $1::numeric, $2, $3::numeric, $4::numeric, $5,
		       $6, $7, 'rpc_exact'
		FROM canonical_blocks AS canonical
		WHERE canonical.chain_id = $1::numeric
		  AND canonical.number = $4::numeric
		  AND canonical.block_hash = $5
			ON CONFLICT (chain_id, token_address, token_id, block_hash) DO UPDATE SET
				observed_at = current.observed_at
			WHERE current.block_number = EXCLUDED.block_number
			  AND current.state = EXCLUDED.state
			  AND current.owner_address IS NOT DISTINCT FROM EXCLUDED.owner_address
			  AND current.confidence = EXCLUDED.confidence;
