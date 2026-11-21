-- name: BalanceDemandLock :exec
SELECT pg_advisory_xact_lock(hashtextextended('balance-acquisition:' || sqlc.arg(chain)::text, 0));

-- name: BalanceDemandExpire :exec
DELETE FROM balance_acquisitions WHERE chain_id=sqlc.arg(chain_id)::numeric AND expires_at <= now();

-- name: BalanceDemandSubmit :execrows
INSERT INTO balance_acquisitions (chain_id,block_number,block_hash,addresses,expires_at)
SELECT sqlc.arg(chain_id)::numeric, sqlc.arg(block_number)::numeric, sqlc.arg(block_hash)::bytea,
       sqlc.arg(addresses)::bytea, now()+interval '2 minutes'
WHERE (SELECT count(*) FROM balance_acquisitions WHERE chain_id=sqlc.arg(chain_id)::numeric) < 1024
   OR EXISTS (SELECT 1 FROM balance_acquisitions WHERE chain_id=sqlc.arg(chain_id)::numeric
       AND block_hash=sqlc.arg(block_hash) AND batch_digest=sha256(sqlc.arg(addresses)::bytea))
ON CONFLICT (chain_id,block_hash,batch_digest) DO UPDATE SET expires_at=balance_acquisitions.expires_at
WHERE balance_acquisitions.addresses=EXCLUDED.addresses AND balance_acquisitions.block_number=EXCLUDED.block_number;

-- name: BalanceDemandClaim :one
WITH next AS (
    SELECT b.id FROM balance_acquisitions b WHERE b.chain_id=sqlc.arg(chain_id)::numeric
      AND b.available_at<=now() AND b.expires_at>now() AND b.generation<4
    ORDER BY b.available_at,b.id LIMIT 1 FOR UPDATE SKIP LOCKED
)
UPDATE balance_acquisitions b SET generation=b.generation+1,
    available_at=now()+sqlc.arg(lease_ms)::bigint*interval '1 millisecond'
FROM next WHERE b.id=next.id
RETURNING b.id,b.generation,b.block_number::text AS number,b.block_hash,b.addresses;

-- name: BalanceDemandFinish :execrows
DELETE FROM balance_acquisitions WHERE id=$1 AND generation=$2 AND available_at>clock_timestamp() AND expires_at>clock_timestamp();
