-- name: VerifyResolveTarget :one
WITH current_code AS (
    SELECT observation.code_hash, observation.block_hash,
           observation.block_number, observation.code
    FROM contract_code_observations AS observation
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = observation.chain_id
     AND canonical.number = observation.block_number
     AND canonical.block_hash = observation.block_hash
    WHERE observation.chain_id = sqlc.arg('chain_id')::numeric
      AND observation.address = sqlc.arg('address')
      AND observation.canonical = TRUE
    ORDER BY observation.block_number DESC,
             observation.observed_at DESC,
             observation.code_hash DESC
    LIMIT 1
)
SELECT
current_code.code_hash,
current_code.block_hash,
current_code.code,
COALESCE((creation.creation_bytecode),'')::text AS creation_bytecode,
EXISTS (
           SELECT 1
           FROM genesis_state_imports AS imported
           JOIN canonical_blocks AS genesis_canonical
             ON genesis_canonical.chain_id = imported.chain_id
            AND genesis_canonical.number = 0
            AND genesis_canonical.block_hash = imported.block_hash
           JOIN genesis_account_observations AS account
             ON account.chain_id = imported.chain_id
            AND account.block_hash = imported.block_hash
            AND account.address = sqlc.arg('address')
           WHERE imported.chain_id = sqlc.arg('chain_id')::numeric
             AND imported.state = 'complete'
             AND octet_length(account.code) > 0
             AND account.code_hash = current_code.code_hash
             AND account.code = current_code.code
       ) AS genesis_predeploy,
(creation.creation_bytecode IS NOT NULL)::boolean AS creation_bytecode_present
FROM current_code
LEFT JOIN LATERAL (
    SELECT candidate.creation_bytecode
    FROM (
        SELECT inclusion.raw->>'input' AS creation_bytecode,
               receipt.block_number, receipt.tx_index,
               ''::text AS trace_path, 0 AS source_rank
        FROM receipts AS receipt
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = receipt.chain_id
         AND canonical.number = receipt.block_number
         AND canonical.block_hash = receipt.block_hash
        JOIN transaction_inclusions AS inclusion
          ON inclusion.chain_id = receipt.chain_id
         AND inclusion.block_number = receipt.block_number
         AND inclusion.block_hash = receipt.block_hash
         AND inclusion.tx_index = receipt.tx_index
        WHERE receipt.chain_id = sqlc.arg('chain_id')::numeric
          AND lower(receipt.raw->>'contractAddress') = sqlc.arg('contract_address_hex')::text
          AND receipt.raw->>'status' IS DISTINCT FROM '0x0'
          AND receipt.block_number <= current_code.block_number
          AND inclusion.raw->>'input' IS NOT NULL

        UNION ALL

        SELECT '0x' || encode(trace.input, 'hex') AS creation_bytecode,
               trace.block_number, trace.transaction_index,
               trace.trace_path, 1 AS source_rank
        FROM normalized_traces AS trace
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = trace.chain_id
         AND canonical.number = trace.block_number
         AND canonical.block_hash = trace.block_hash
        WHERE trace.chain_id = sqlc.arg('chain_id')::numeric
          AND trace.created_address = sqlc.arg('address')
          AND trace.canonical = TRUE
          AND trace.reverted = FALSE
          AND trace.input IS NOT NULL
          AND trace.block_number <= current_code.block_number
    ) AS candidate
    ORDER BY candidate.block_number DESC, candidate.tx_index DESC,
             candidate.source_rank DESC, candidate.trace_path DESC
       LIMIT 1
    ) AS creation ON TRUE;
