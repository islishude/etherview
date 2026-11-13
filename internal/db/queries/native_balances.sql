-- name: NativeBalanceBody :one
SELECT raw, method FROM native_balance_blocks
WHERE chain_id=sqlc.arg(chain_id)::numeric AND block_hash=sqlc.arg(block_hash)
  AND block_number=sqlc.arg(block_number)::numeric;

-- name: NativeBalanceInsertBody :execrows
INSERT INTO native_balance_blocks(chain_id,block_number,block_hash,raw,method,canonical)
VALUES(sqlc.arg(chain_id)::numeric,sqlc.arg(block_number)::numeric,sqlc.arg(block_hash),sqlc.arg(raw)::jsonb,sqlc.arg(method),true)
ON CONFLICT (chain_id,block_number,block_hash) DO UPDATE SET canonical=true
WHERE native_balance_blocks.raw=EXCLUDED.raw AND native_balance_blocks.method=EXCLUDED.method;

-- name: NativeBalanceInsertChanges :execrows
INSERT INTO native_balance_changes(chain_id,block_number,block_hash,address,access_index,balance,canonical)
SELECT sqlc.arg(chain_id)::numeric,sqlc.arg(block_number)::numeric,sqlc.arg(block_hash),
       decode(item.address,'hex'),item.access_index,item.balance::numeric,true
FROM jsonb_to_recordset(sqlc.arg(changes)::jsonb) AS item(address text,access_index bigint,balance text)
ON CONFLICT (chain_id,block_number,block_hash,address,access_index) DO UPDATE SET canonical=true
WHERE native_balance_changes.balance=EXCLUDED.balance;

-- name: NativeBalanceInsertObservation :execrows
INSERT INTO native_balance_observations(chain_id,block_number,block_hash,address,balance,rpc_exact,canonical)
VALUES(sqlc.arg(chain_id)::numeric,sqlc.arg(block_number)::numeric,sqlc.arg(block_hash),sqlc.arg(address),sqlc.arg(balance)::numeric,sqlc.arg(rpc_exact),true)
ON CONFLICT (chain_id,block_number,block_hash,address) DO UPDATE
SET rpc_exact=native_balance_observations.rpc_exact OR EXCLUDED.rpc_exact,canonical=true
WHERE native_balance_observations.balance=EXCLUDED.balance
  AND native_balance_observations.block_number=EXCLUDED.block_number;

-- name: NativeBalanceLockCanonical :one
SELECT block_hash FROM canonical_blocks
WHERE chain_id=sqlc.arg(chain_id)::numeric AND number=sqlc.arg(block_number)::numeric
  AND block_hash=sqlc.arg(block_hash) FOR SHARE;

-- name: NativeBalanceInsertEnds :execrows
INSERT INTO native_balance_observations(chain_id,block_number,block_hash,address,balance,rpc_exact,canonical)
SELECT sqlc.arg(chain_id)::numeric,sqlc.arg(block_number)::numeric,sqlc.arg(block_hash),
       decode(item.address,'hex'),item.balance::numeric,false,true
FROM jsonb_to_recordset(sqlc.arg(observations)::jsonb) AS item(address text,balance text)
ORDER BY item.address
ON CONFLICT (chain_id,block_number,block_hash,address) DO UPDATE SET canonical=true
WHERE native_balance_observations.balance=EXCLUDED.balance
  AND native_balance_observations.block_number=EXCLUDED.block_number;

-- name: NativeBalanceRead :many
WITH requested AS (SELECT DISTINCT unnest(sqlc.arg(addresses)::bytea[]) AS address)
SELECT requested.address::bytea AS address, observation.balance::text AS balance
FROM requested
CROSS JOIN LATERAL (
    SELECT candidate.balance,candidate.block_number FROM (
        (SELECT o.balance,o.block_number FROM native_balance_observations o
         JOIN canonical_blocks c ON c.chain_id=o.chain_id AND c.number=o.block_number AND c.block_hash=o.block_hash
         WHERE o.chain_id=sqlc.arg(chain_id)::numeric AND o.address=requested.address
           AND o.block_number<=sqlc.arg(block_number)::numeric AND o.canonical
           AND (o.rpc_exact OR EXISTS(SELECT 1 FROM published_block_stage_results p
               WHERE p.chain_id=o.chain_id AND p.block_number=o.block_number AND p.block_hash=o.block_hash
                 AND p.stage='native_balance' AND p.stage_version=1 AND p.state='complete' AND p.details->>'outcome'='indexed'))
         ORDER BY o.block_number DESC LIMIT 1)
        UNION ALL
        SELECT g.balance,0::numeric FROM genesis_account_observations g
        JOIN genesis_state_imports i ON i.chain_id=g.chain_id AND i.block_hash=g.block_hash AND i.state='complete'
        JOIN canonical_blocks c ON c.chain_id=g.chain_id AND c.number=0 AND c.block_hash=g.block_hash
        WHERE g.chain_id=sqlc.arg(chain_id)::numeric AND g.address=requested.address
    ) candidate ORDER BY candidate.block_number DESC LIMIT 1
) observation
WHERE EXISTS(SELECT 1 FROM canonical_blocks c WHERE c.chain_id=sqlc.arg(chain_id)::numeric
    AND c.number=sqlc.arg(block_number)::numeric AND c.block_hash=sqlc.arg(block_hash))
AND (observation.block_number=sqlc.arg(block_number)::numeric OR EXISTS (
    SELECT 1 FROM browsing_coverage coverage WHERE coverage.chain_id=sqlc.arg(chain_id)::numeric
      AND coverage.stage='native_balance' AND coverage.stage_version=1 AND coverage.configuration_digest=''
      AND coverage.covered @> numrange(observation.block_number+1,sqlc.arg(block_number)::numeric+1,'[)')
));
