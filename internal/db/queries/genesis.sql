-- name: GetGenesisImport :one
SELECT
    imported.state,
    imported.block_hash,
    imported.state_root,
    COALESCE(imported.account_count::text, '') AS account_count,
    imported.last_error_code,
    EXISTS (
        SELECT 1
        FROM canonical_blocks AS canonical
        WHERE canonical.chain_id = imported.chain_id
          AND canonical.number = 0
          AND canonical.block_hash = imported.block_hash
    ) AS canonical
FROM genesis_state_imports AS imported
WHERE imported.chain_id = sqlc.arg(chain_id)::numeric;

-- name: ListGenesisAccounts :many
SELECT
    account.address,
    account.balance::text AS balance,
    account.nonce::text AS nonce,
    account.code_hash,
    account.storage_root,
    account.block_hash,
    octet_length(account.code) > 0 AS contract
FROM genesis_account_observations AS account
WHERE account.chain_id = sqlc.arg(chain_id)::numeric
  AND account.block_hash = sqlc.arg(block_hash)
  AND (
      octet_length(sqlc.arg(after_address)::bytea) = 0
      OR account.address > sqlc.arg(after_address)::bytea
  )
ORDER BY account.address
LIMIT sqlc.arg(page_limit);
