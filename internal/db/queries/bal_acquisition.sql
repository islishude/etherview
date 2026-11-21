-- name: BALDemandSubmit :one
INSERT INTO bal_acquisitions(chain_id,block_number,block_hash,generation)
VALUES(sqlc.arg(chain_id)::numeric,sqlc.arg(block_number)::numeric,sqlc.arg(block_hash),sqlc.arg(generation)::bigint)
ON CONFLICT (chain_id,block_number,block_hash) DO UPDATE SET
    code=CASE WHEN (bal_acquisitions.code IN ('invalid','unavailable') AND EXCLUDED.generation>bal_acquisitions.generation) OR (bal_acquisitions.code='ready' AND NOT EXISTS (SELECT 1 FROM native_balance_blocks n WHERE n.chain_id=EXCLUDED.chain_id AND n.block_number=EXCLUDED.block_number AND n.block_hash=EXCLUDED.block_hash)) THEN 'pending' ELSE bal_acquisitions.code END,
    attempts=CASE WHEN (bal_acquisitions.code IN ('invalid','unavailable') AND EXCLUDED.generation>bal_acquisitions.generation) OR (bal_acquisitions.code='ready' AND NOT EXISTS (SELECT 1 FROM native_balance_blocks n WHERE n.chain_id=EXCLUDED.chain_id AND n.block_number=EXCLUDED.block_number AND n.block_hash=EXCLUDED.block_hash)) THEN 0 ELSE bal_acquisitions.attempts END,
    generation=GREATEST(bal_acquisitions.generation,EXCLUDED.generation)
RETURNING code;

-- name: BALDemandClaim :one
WITH candidate AS (
  SELECT b.block_number,b.block_hash FROM bal_acquisitions b
  WHERE b.chain_id=sqlc.arg(chain_id)::numeric AND b.code='pending' AND b.lease_until<=clock_timestamp()
  ORDER BY b.lease_until,b.block_number LIMIT 1 FOR UPDATE SKIP LOCKED
)
UPDATE bal_acquisitions b SET attempts=b.attempts+1,lease_until=clock_timestamp()+sqlc.arg(lease_ms)::bigint*interval '1 millisecond'
FROM candidate c WHERE b.chain_id=sqlc.arg(chain_id)::numeric AND b.block_number=c.block_number AND b.block_hash=c.block_hash
RETURNING b.block_number,b.block_hash,b.attempts,b.generation;

-- name: BALDemandFinish :execrows
UPDATE bal_acquisitions SET code=sqlc.arg(code),endpoint=sqlc.arg(endpoint),lease_until='-infinity'
WHERE chain_id=sqlc.arg(chain_id)::numeric AND block_number=sqlc.arg(block_number)::numeric AND block_hash=sqlc.arg(block_hash)
  AND code='pending' AND attempts=sqlc.arg(attempts) AND generation=sqlc.arg(generation) AND lease_until>clock_timestamp();

-- name: BALStoreAcquiredBody :execrows
INSERT INTO native_balance_blocks(chain_id,block_number,block_hash,method,raw,canonical)
VALUES(sqlc.arg(chain_id)::numeric,sqlc.arg(block_number)::numeric,sqlc.arg(block_hash),sqlc.arg(method),sqlc.arg(raw)::jsonb,false)
ON CONFLICT (chain_id,block_number,block_hash) DO UPDATE SET raw=native_balance_blocks.raw
WHERE native_balance_blocks.raw=EXCLUDED.raw AND native_balance_blocks.method=EXCLUDED.method;
