-- name: StateInputGroup :one
INSERT INTO state_input_groups(chain_id,block_number,block_hash,consumer) VALUES($1,$2,$3,$4)
ON CONFLICT(chain_id,block_hash,consumer) DO UPDATE SET consumer=EXCLUDED.consumer
RETURNING id;

-- name: StageInputRead :one
SELECT outcome,result FROM stage_state_inputs WHERE group_id=$1 AND request_hash=sha256(sqlc.arg(request)::bytea) AND request=sqlc.arg(request) AND (outcome<>'failed' OR lease_until>clock_timestamp());

-- name: StageInputSubmit :exec
INSERT INTO stage_state_inputs(group_id,request) VALUES($1,$2) ON CONFLICT(group_id,request_hash) DO UPDATE SET outcome='',attempts=0,lease_until='-infinity',result=NULL
WHERE stage_state_inputs.outcome='failed' AND stage_state_inputs.lease_until<=clock_timestamp()
AND stage_state_inputs.request=EXCLUDED.request;

-- name: StageInputClaim :one
WITH next AS (
 SELECT s.id,g.block_hash,g.endpoint FROM stage_state_inputs s JOIN state_input_groups g ON g.id=s.group_id
 WHERE g.chain_id=sqlc.arg(chain_id)::numeric AND s.outcome='' AND s.lease_until<=clock_timestamp()
 ORDER BY s.lease_until,s.id LIMIT 1 FOR UPDATE OF s SKIP LOCKED)
UPDATE stage_state_inputs s SET attempts=attempts+1,lease_generation=lease_generation+1,lease_until=clock_timestamp()+sqlc.arg(lease_ms)::bigint*interval '1 millisecond'
FROM next WHERE next.id=s.id RETURNING s.id,s.group_id,s.request,s.attempts,s.lease_generation,next.block_hash,next.endpoint;

-- name: StageInputFinish :execrows
UPDATE stage_state_inputs s SET result=sqlc.narg(result),outcome=sqlc.arg(outcome), lease_until=clock_timestamp()+interval '1 minute'
FROM state_input_groups g WHERE s.id=sqlc.arg(id) AND s.lease_generation=sqlc.arg(lease_generation) AND s.lease_until>clock_timestamp()
AND s.group_id=g.id AND g.endpoint=sqlc.arg(endpoint) AND s.outcome='';

-- name: StateInputPin :one
UPDATE state_input_groups SET endpoint=CASE WHEN endpoint='' THEN sqlc.arg(endpoint)::text ELSE endpoint END WHERE id=$1 RETURNING endpoint;

-- name: StateInputEndpoint :one
SELECT endpoint FROM state_input_groups WHERE id=$1;

-- name: RecordSyncCapabilities :exec
INSERT INTO sync_capabilities(chain_id,historical_state,trace) VALUES($1,$2,$3)
ON CONFLICT(chain_id) DO UPDATE SET historical_state=EXCLUDED.historical_state,trace=EXCLUDED.trace;

-- name: ReadSyncCapabilities :one
SELECT historical_state,trace FROM sync_capabilities WHERE chain_id=$1;

-- A group may switch upstreams until its first retained response. Serializing
-- this update with completion prevents responses from two nodes being combined.
-- name: StateInputUnpinEmpty :exec
UPDATE state_input_groups g SET endpoint='' WHERE g.id=sqlc.arg(group_id) AND g.endpoint=sqlc.arg(endpoint) AND EXISTS
 (SELECT 1 FROM stage_state_inputs s WHERE s.group_id=g.id AND s.id=sqlc.arg(id) AND s.lease_generation=sqlc.arg(lease_generation) AND s.outcome='' AND s.lease_until>clock_timestamp()) AND NOT EXISTS
 (SELECT 1 FROM stage_state_inputs s WHERE s.group_id=g.id AND s.outcome IN ('ready','reverted'));

-- name: LockStateInputGroup :one
SELECT endpoint FROM state_input_groups WHERE id=$1 FOR UPDATE;

-- name: StateInputLookupGroup :one
SELECT id FROM state_input_groups WHERE chain_id=$1 AND block_number=$2 AND block_hash=$3 AND consumer=$4;

-- name: StageInputRetry :exec
UPDATE stage_state_inputs SET lease_until=clock_timestamp()+interval '1 second'
WHERE id=$1 AND lease_generation=$2 AND outcome='' AND lease_until>clock_timestamp();
