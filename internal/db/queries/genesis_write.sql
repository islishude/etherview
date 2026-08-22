-- name: GenesisWriteCompletedRemoteImportStatement1 :many
SELECT
		    imported.xmin::text,
		    imported.state,
		    imported.block_hash,
		    imported.state_root,
		    imported.document_sha256,
		    canonical.block_hash IS NOT NULL,
		    block.raw
		FROM genesis_state_imports AS imported
		LEFT JOIN canonical_blocks AS canonical
		  ON canonical.chain_id = imported.chain_id
		 AND canonical.number = 0
		 AND canonical.block_hash = imported.block_hash
		LEFT JOIN blocks AS block
		  ON block.chain_id = canonical.chain_id
		 AND block.number = canonical.number
		 AND block.hash = canonical.block_hash
		WHERE imported.chain_id = $1::numeric;

-- name: GenesisWriteImportOnceUsingStatement1 :many
SELECT pg_advisory_xact_lock(hashtext('etherview:genesis-state'), hashtext($1));

-- name: GenesisWriteImportOnceUsingStatement2 :many
SELECT block.hash, block.raw
		FROM canonical_blocks AS canonical
		JOIN blocks AS block
		  ON block.chain_id = canonical.chain_id
		 AND block.number = canonical.number
		 AND block.hash = canonical.block_hash
		WHERE canonical.chain_id = $1::numeric AND canonical.number = 0;

-- name: GenesisWriteImportOnceUsingStatement3 :many
SELECT state, block_hash, state_root, document_sha256
		FROM genesis_state_imports
		WHERE chain_id = $1::numeric
		FOR UPDATE;

-- name: GenesisWriteImportOnceUsingStatement4 :exec
INSERT INTO genesis_state_imports (
		    chain_id, block_hash, state_root, document_sha256, state,
		    account_count, last_error_code, imported_at, updated_at
		) VALUES (
		    $1::numeric, $2, $3, $4, 'complete', $5::numeric,
		    NULL, clock_timestamp(), clock_timestamp()
		)
		ON CONFLICT (chain_id) DO UPDATE SET
		    block_hash = EXCLUDED.block_hash,
		    state_root = EXCLUDED.state_root,
		    document_sha256 = EXCLUDED.document_sha256,
		    state = EXCLUDED.state,
		    account_count = EXCLUDED.account_count,
		    last_error_code = NULL,
		    imported_at = EXCLUDED.imported_at,
		    updated_at = EXCLUDED.updated_at
		WHERE genesis_state_imports.state <> 'complete';

-- name: GenesisWriteImportOnceUsingStatement5 :exec
INSERT INTO genesis_account_observations (
			    chain_id, address, block_hash, balance, nonce,
			    code_hash, code, storage_root
			) VALUES (
			    $1::numeric, $2, $3, $4::numeric, $5::numeric, $6, $7, $8
			);

-- name: GenesisWriteImportOnceUsingStatement6 :exec
INSERT INTO contract_code_observations AS current (
				    chain_id, address, block_number, block_hash,
				    code_hash, code, canonical
				) VALUES ($1::numeric, $2, 0, $3, $4, $5, TRUE)
				ON CONFLICT (chain_id, address, block_hash) DO UPDATE SET
				    code = COALESCE(current.code, EXCLUDED.code),
				    canonical = TRUE
				WHERE current.code_hash = EXCLUDED.code_hash
				  AND (current.code IS NULL OR current.code = EXCLUDED.code);

-- name: GenesisWriteMarkUnavailableStatement1 :exec
INSERT INTO genesis_state_imports (chain_id, state, last_error_code)
		VALUES ($1::numeric, 'unavailable', 'genesis_file_not_configured')
		ON CONFLICT (chain_id) DO UPDATE SET
		    state = 'unavailable',
		    block_hash = NULL,
		    state_root = NULL,
		    document_sha256 = NULL,
		    account_count = NULL,
		    last_error_code = 'genesis_file_not_configured',
		    imported_at = NULL,
		    updated_at = clock_timestamp()
		WHERE genesis_state_imports.state <> 'complete';

-- name: GenesisWriteRecordRemoteFailureStatement1 :exec
INSERT INTO genesis_state_imports (chain_id, state, last_error_code)
		VALUES ($1::numeric, $2, $3)
		ON CONFLICT (chain_id) DO UPDATE SET
		    state = EXCLUDED.state,
		    block_hash = NULL,
		    state_root = NULL,
		    document_sha256 = NULL,
		    account_count = NULL,
		    last_error_code = EXCLUDED.last_error_code,
		    imported_at = NULL,
		    updated_at = clock_timestamp()
		WHERE genesis_state_imports.state <> 'complete';

-- name: GenesisWriteWaitForCanonicalBlockZeroStatement1 :many
SELECT EXISTS (
				    SELECT 1
				    FROM canonical_blocks
				    WHERE chain_id = $1::numeric AND number = 0
				);
-- name: GenesisWriteWithRemoteSourceLockStatement1 :many
SELECT pg_advisory_unlock(
				    hashtext('etherview:genesis-remote-source'),
				    hashtext($1)
				);

-- name: GenesisWriteWithRemoteSourceLockStatement2 :many
SELECT pg_try_advisory_lock(
			    hashtext('etherview:genesis-remote-source'),
			    hashtext($1)
			);
