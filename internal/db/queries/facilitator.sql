-- name: FacilitatorAcquireLock :one
SELECT pg_try_advisory_lock(402059::bigint);

-- name: FacilitatorReserve :execrows
INSERT INTO facilitator_settlements (authorization_id,request_digest,request_body,network,signer_address)
VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING;

-- name: FacilitatorGet :one
SELECT * FROM facilitator_settlements WHERE authorization_id=$1;

-- name: FacilitatorSaveSigned :execrows
UPDATE facilitator_settlements SET nonce=$2,raw_transaction=$3,transaction_hash=$4,broadcast_status='unknown',state='signed',updated_at=now()
WHERE authorization_id=$1 AND state='reserved' AND raw_transaction IS NULL;

-- name: FacilitatorFinish :execrows
UPDATE facilitator_settlements SET state=$2,response=$3,updated_at=now()
WHERE authorization_id=$1 AND state IN ('reserved','signed');

-- name: FacilitatorPendingCount :one
SELECT count(*) FROM facilitator_settlements WHERE network=$1 AND state IN ('reserved','signed');

-- name: FacilitatorPending :many
SELECT * FROM facilitator_settlements WHERE network=$1 AND state IN ('reserved','signed') ORDER BY created_at LIMIT 32;

-- name: FacilitatorConfirmBroadcast :execrows
UPDATE facilitator_settlements SET broadcast_status='confirmed',updated_at=now()
WHERE authorization_id=$1 AND transaction_hash=$2 AND broadcast_status <> 'confirmed';
