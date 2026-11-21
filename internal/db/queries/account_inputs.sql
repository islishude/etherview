-- name: AccountInputRead :one
SELECT evidence FROM account_inputs WHERE chain_id=$1 AND block_number=$2 AND block_hash=$3 AND operation=$4 AND address=$5 AND argument_hash=sha256(sqlc.arg(argument)::bytea) AND argument=sqlc.arg(argument) AND expires_at>now();

-- name: AccountInputExpire :exec
DELETE FROM account_inputs WHERE chain_id=$1 AND expires_at<=now();

-- name: AccountInputSubmit :execrows
INSERT INTO account_inputs(chain_id,block_number,block_hash,operation,address,argument,expires_at)
SELECT sqlc.arg(chain_id)::numeric,sqlc.arg(block_number)::numeric,sqlc.arg(block_hash)::bytea,sqlc.arg(operation)::text,sqlc.arg(address)::bytea,sqlc.arg(argument)::bytea,now()+interval '2 minutes'
WHERE (SELECT count(*) FROM account_inputs WHERE chain_id=sqlc.arg(chain_id)) < 8192
ON CONFLICT(chain_id,block_hash,operation,address,argument_hash) DO NOTHING;

-- name: AccountInputClaim :one
WITH next AS (SELECT id FROM account_inputs WHERE chain_id=sqlc.arg(chain_id)::numeric AND evidence IS NULL
  AND attempts<4 AND lease_until<=now() AND expires_at>now() ORDER BY lease_until,id LIMIT 1 FOR UPDATE SKIP LOCKED)
UPDATE account_inputs a SET attempts=attempts+1,lease_until=now()+sqlc.arg(lease_ms)::bigint*interval '1 millisecond'
FROM next WHERE a.id=next.id RETURNING a.id,a.attempts,a.operation,a.block_number::text AS height,a.block_hash,a.address,a.argument;

-- name: AccountInputFinish :execrows
UPDATE account_inputs SET evidence=sqlc.arg(evidence)::jsonb,expires_at=CASE WHEN sqlc.arg(success)::boolean THEN now()+interval '1 hour' ELSE now()+interval '10 seconds' END
WHERE id=$1 AND attempts=$2 AND lease_until>clock_timestamp() AND expires_at>clock_timestamp();
