-- name: BlockInputRead :one
SELECT evidence,error_code,generation,attempts FROM block_inputs
WHERE chain_id=sqlc.arg(chain_id)::numeric AND block_number=sqlc.arg(block_number)::numeric AND block_hash=sqlc.arg(block_hash)
AND kind=sqlc.arg(kind) AND version=1;

-- name: BlockInputSubmit :exec
INSERT INTO block_inputs(chain_id,block_number,block_hash,kind,version,generation)
VALUES(sqlc.arg(chain_id)::numeric,sqlc.arg(block_number)::numeric,sqlc.arg(block_hash),sqlc.arg(kind),1,sqlc.arg(generation)::bigint)
ON CONFLICT(chain_id,block_number,block_hash,kind,version) DO UPDATE
SET generation=EXCLUDED.generation,attempts=0,error_code='',evidence=NULL,lease_until='-infinity'
WHERE (block_inputs.error_code<>'' OR block_inputs.kind='blob') AND block_inputs.generation<EXCLUDED.generation;

-- name: BlockInputClaim :one
WITH next AS (
 SELECT b.block_number,b.block_hash,b.kind FROM block_inputs b
 WHERE b.chain_id=sqlc.arg(chain_id)::numeric AND b.version=1 AND b.kind=ANY(sqlc.arg(kinds)::text[]) AND b.evidence IS NULL AND b.error_code=''
 AND b.lease_until<=clock_timestamp() ORDER BY b.lease_until,b.block_number LIMIT 1 FOR UPDATE SKIP LOCKED
)
UPDATE block_inputs b SET attempts=attempts+1,lease_until=clock_timestamp()+sqlc.arg(lease_ms)::bigint*interval '1 millisecond'
FROM next n WHERE b.chain_id=sqlc.arg(chain_id)::numeric AND b.version=1 AND b.block_number=n.block_number AND b.block_hash=n.block_hash AND b.kind=n.kind
RETURNING b.block_number,b.block_hash,b.kind,b.generation,b.attempts;

-- name: BlockInputFinish :execrows
UPDATE block_inputs SET evidence=sqlc.narg(evidence)::jsonb,error_code=sqlc.arg(error_code),lease_until='-infinity'
WHERE chain_id=sqlc.arg(chain_id)::numeric AND block_number=sqlc.arg(block_number)::numeric AND block_hash=sqlc.arg(block_hash)
AND kind=sqlc.arg(kind) AND version=1 AND generation=sqlc.arg(generation) AND attempts=sqlc.arg(attempts)
AND evidence IS NULL AND error_code='' AND lease_until>clock_timestamp();

-- name: BeaconNetworkConflict :one
SELECT EXISTS(SELECT 1 FROM consensus_overviews WHERE chain_id=sqlc.arg(chain_id)::numeric AND last_error='beacon_network_mismatch');

-- name: BlockInputTransactionHashes :many
SELECT tx_hash FROM transaction_inclusions WHERE chain_id=$1 AND block_number=$2 AND block_hash=$3 ORDER BY tx_index;

-- name: BlockInputTraceEndpoint :one
SELECT (evidence->>'endpoint')::text AS endpoint FROM block_inputs
WHERE chain_id=$1 AND block_number=$2 AND block_hash=$3 AND kind='state_diff@3' AND version=1 AND error_code='' AND evidence IS NOT NULL;

-- A processor may discover that a slot has moved, or an acquired blob has
-- disappeared from the archive. Preserve the lease counter and retry budget.
-- name: BlockInputRetryEvidence :execrows
UPDATE block_inputs SET evidence=NULL,lease_until=clock_timestamp()+interval '1 second'
WHERE chain_id=$1 AND block_number=$2 AND block_hash=$3 AND kind=$4 AND version=1
  AND generation=$5 AND attempts<4 AND evidence=sqlc.arg(previous)::jsonb;
