-- name: StateWriteClassifyERC20BalancePersistenceMiss :one
SELECT
            EXISTS (
                SELECT 1 FROM canonical_blocks
                    WHERE chain_id = sqlc.arg('chain_id')::numeric AND number = sqlc.arg('number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
            ) AS canonical,
            EXISTS (
                SELECT 1 FROM erc20_balance_reconciliations
                    WHERE chain_id = sqlc.arg('chain_id')::numeric AND token_address = sqlc.arg('token_address')::bytea
                      AND owner_address = sqlc.arg('owner_address')::bytea AND block_hash = sqlc.arg('block_hash')::bytea
            ) AS stored;

-- name: StateWriteInsertERC20Balance :execrows
INSERT INTO erc20_balance_reconciliations AS current (
            chain_id, token_address, owner_address,
            block_number, block_hash, balance, confidence
        )
        SELECT sqlc.arg('chain_id')::numeric, sqlc.arg('token_address'), sqlc.arg('owner_address'),
               sqlc.arg('block_number')::numeric, sqlc.arg('block_hash'), sqlc.arg('balance')::numeric, 'rpc_exact'
        FROM canonical_blocks AS canonical
        WHERE canonical.chain_id = sqlc.arg('chain_id')::numeric
          AND canonical.number = sqlc.arg('block_number')::numeric
          AND canonical.block_hash = sqlc.arg('block_hash')
            ON CONFLICT (chain_id, token_address, owner_address, block_hash) DO UPDATE SET
                observed_at = current.observed_at
            WHERE current.block_number = EXCLUDED.block_number
              AND current.balance = EXCLUDED.balance
              AND current.confidence = EXCLUDED.confidence;

-- name: StateWriteClassifyBalancePersistenceMissStatement1 :one
SELECT
			EXISTS (
				SELECT 1 FROM canonical_blocks
					WHERE chain_id = sqlc.arg('chain_id')::numeric AND number = sqlc.arg('number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
			),
			EXISTS (
				SELECT 1 FROM erc1155_balance_reconciliations
					WHERE chain_id = sqlc.arg('chain_id')::numeric AND token_address = sqlc.arg('token_address')::bytea
					  AND token_id = sqlc.arg('token_id')::numeric AND owner_address = sqlc.arg('owner_address')::bytea AND block_hash = sqlc.arg('block_hash')::bytea
			);

-- name: StateWriteClassifyOwnerPersistenceMissStatement1 :one
SELECT
			EXISTS (
				SELECT 1 FROM canonical_blocks
					WHERE chain_id = sqlc.arg('chain_id')::numeric AND number = sqlc.arg('number')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
			),
			EXISTS (
				SELECT 1 FROM erc721_owner_reconciliations
					WHERE chain_id = sqlc.arg('chain_id')::numeric AND token_address = sqlc.arg('token_address')::bytea
					  AND token_id = sqlc.arg('token_id')::numeric AND block_hash = sqlc.arg('block_hash')::bytea
			);

-- name: StateWriteInsertERC1155BalanceStatement1 :execrows
INSERT INTO erc1155_balance_reconciliations AS current (
			chain_id, token_address, token_id, owner_address,
			block_number, block_hash, balance, confidence
		)
		SELECT sqlc.arg('chain_id')::numeric, sqlc.arg('token_address'), sqlc.arg('token_id')::numeric, sqlc.arg('owner_address'),
		       sqlc.arg('block_number')::numeric, sqlc.arg('block_hash'), sqlc.arg('balance')::numeric, 'rpc_exact'
		FROM canonical_blocks AS canonical
		WHERE canonical.chain_id = sqlc.arg('chain_id')::numeric
		  AND canonical.number = sqlc.arg('block_number')::numeric
		  AND canonical.block_hash = sqlc.arg('block_hash')
			ON CONFLICT (chain_id, token_address, token_id, owner_address, block_hash) DO UPDATE SET
				observed_at = current.observed_at
			WHERE current.block_number = EXCLUDED.block_number
			  AND current.balance = EXCLUDED.balance
			  AND current.confidence = EXCLUDED.confidence;

-- name: StateWriteInsertOwnerObservationStatement1 :execrows
INSERT INTO erc721_owner_reconciliations AS current (
			chain_id, token_address, token_id, block_number, block_hash,
			state, owner_address, confidence
		)
		SELECT sqlc.arg('chain_id')::numeric, sqlc.arg('token_address'), sqlc.arg('token_id')::numeric, sqlc.arg('block_number')::numeric, sqlc.arg('block_hash'),
		       sqlc.arg('state'), sqlc.arg('owner_address'), 'rpc_exact'
		FROM canonical_blocks AS canonical
		WHERE canonical.chain_id = sqlc.arg('chain_id')::numeric
		  AND canonical.number = sqlc.arg('block_number')::numeric
		  AND canonical.block_hash = sqlc.arg('block_hash')
			ON CONFLICT (chain_id, token_address, token_id, block_hash) DO UPDATE SET
				observed_at = current.observed_at
			WHERE current.block_number = EXCLUDED.block_number
			  AND current.state = EXCLUDED.state
			  AND current.owner_address IS NOT DISTINCT FROM EXCLUDED.owner_address
			  AND current.confidence = EXCLUDED.confidence;
