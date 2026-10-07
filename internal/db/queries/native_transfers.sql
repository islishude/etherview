-- name: NativeTransferSourceBlock :one
SELECT raw FROM blocks WHERE chain_id=sqlc.arg(chain_id)::numeric AND number=sqlc.arg(block_number)::numeric AND hash=sqlc.arg(block_hash);

-- name: NativeTransferSourceReceipts :many
SELECT raw FROM receipts WHERE chain_id=sqlc.arg(chain_id)::numeric AND block_number=sqlc.arg(block_number)::numeric AND block_hash=sqlc.arg(block_hash) ORDER BY tx_index;

-- name: NativeTransferClear :exec
DELETE FROM native_transfers WHERE chain_id=sqlc.arg(chain_id)::numeric AND block_number=sqlc.arg(block_number)::numeric AND block_hash=sqlc.arg(block_hash);

-- name: NativeTransferInsert :exec
INSERT INTO native_transfers(chain_id,block_number,block_hash,transaction_hash,transaction_index,log_index,from_address,to_address,amount,canonical)
VALUES(sqlc.arg(chain_id)::numeric,sqlc.arg(block_number)::numeric,sqlc.arg(block_hash),sqlc.arg(transaction_hash),sqlc.arg(transaction_index),sqlc.arg(log_index),sqlc.arg(from_address),sqlc.arg(to_address),sqlc.arg(amount)::numeric,true);

-- name: NativeTransferActivation :one
SELECT COALESCE(MIN(b.number)::text,'')::text AS start_number FROM blocks b
JOIN canonical_blocks c ON c.chain_id=b.chain_id AND c.number=b.number AND c.block_hash=b.hash
WHERE b.chain_id=sqlc.arg(chain_id)::numeric AND b.number>=sqlc.arg(index_start)::numeric AND b.number<=sqlc.arg(snapshot_number)::numeric
AND b.slot_number_raw IS NOT NULL AND b.slot_number_raw<>'null'::jsonb;

-- name: NativeTransferCoverage :one
SELECT EXISTS(SELECT 1 FROM native_transfer_coverage WHERE chain_id=sqlc.arg(chain_id)::numeric AND covered @> numrange(sqlc.arg(start_number)::numeric,sqlc.arg(end_number)::numeric+1,'[)')) AS complete;

-- name: NativeTransferTransactionBlock :one
SELECT b.number::text AS block_number,b.hash AS block_hash,(b.slot_number_raw IS NOT NULL AND b.slot_number_raw<>'null'::jsonb)::boolean AS amsterdam
FROM transaction_inclusions t JOIN canonical_blocks c ON c.chain_id=t.chain_id AND c.number=t.block_number AND c.block_hash=t.block_hash
JOIN blocks b ON b.chain_id=c.chain_id AND b.number=c.number AND b.hash=c.block_hash
WHERE t.chain_id=sqlc.arg(chain_id)::numeric AND t.tx_hash=sqlc.arg(transaction_hash) AND t.block_number<=sqlc.arg(snapshot_number)::numeric;

-- name: NativeTransferList :many
SELECT n.block_number::text AS block_number,n.block_hash,n.transaction_hash,n.transaction_index,n.log_index,n.from_address,n.to_address,n.amount::text AS amount,b.timestamp::text AS block_timestamp
FROM native_transfers n JOIN canonical_blocks c ON c.chain_id=n.chain_id AND c.number=n.block_number AND c.block_hash=n.block_hash
JOIN blocks b ON b.chain_id=n.chain_id AND b.number=n.block_number AND b.hash=n.block_hash
JOIN published_block_stage_results s ON s.chain_id=n.chain_id AND s.block_number=n.block_number AND s.block_hash=n.block_hash AND s.stage='native_transfer' AND s.stage_version=1 AND s.state='complete'
WHERE n.chain_id=sqlc.arg(chain_id)::numeric AND n.canonical AND n.block_number>=sqlc.arg(start_number)::numeric AND n.block_number<=sqlc.arg(snapshot_number)::numeric
AND (NOT sqlc.arg(by_transaction)::boolean OR n.transaction_hash=sqlc.arg(transaction_hash)::bytea)
AND (NOT sqlc.arg(by_address)::boolean OR n.from_address=sqlc.arg(address)::bytea OR n.to_address=sqlc.arg(address)::bytea)
AND (NOT sqlc.arg(has_cursor)::boolean OR (n.block_number,n.transaction_index,n.log_index)<(sqlc.arg(before_block)::numeric,sqlc.arg(before_transaction)::bigint,sqlc.arg(before_log)::bigint))
ORDER BY n.block_number DESC,n.transaction_index DESC,n.log_index DESC LIMIT sqlc.arg(page_limit)::integer;

-- name: NativeTransferValidateSnapshot :one
SELECT EXISTS(SELECT 1 FROM canonical_blocks WHERE chain_id=sqlc.arg(chain_id)::numeric AND number=sqlc.arg(snapshot_number)::numeric AND block_hash=sqlc.arg(snapshot_hash)) AS valid;
