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

-- name: FacilitatorAbortUnsigned :execrows
UPDATE facilitator_settlements SET state='failed',response=$2,updated_at=now()
WHERE authorization_id=$1 AND state='reserved'
  AND nonce IS NULL AND raw_transaction IS NULL AND transaction_hash IS NULL;

-- name: FacilitatorFinish :execrows
UPDATE facilitator_settlements SET state=$2,response=$3,updated_at=now()
WHERE authorization_id=$1 AND state IN ('signed','reconciling') AND response IS NULL;

-- name: FacilitatorPendingCount :one
SELECT count(*) FROM facilitator_settlements WHERE network=$1 AND (state IN ('reserved','signed','reconciling') OR (transaction_hash IS NOT NULL AND NOT transaction_confirmed));

-- name: FacilitatorPending :many
SELECT * FROM facilitator_settlements WHERE network=$1 AND (state IN ('reserved','signed','reconciling') OR (transaction_hash IS NOT NULL AND NOT transaction_confirmed)) ORDER BY updated_at, authorization_id LIMIT 32;

-- name: FacilitatorConfirmBroadcast :execrows
UPDATE facilitator_settlements SET broadcast_status='confirmed',updated_at=now()
WHERE authorization_id=$1 AND transaction_hash=$2 AND broadcast_status <> 'confirmed';

-- name: FacilitatorRejectUnsubmitted :execrows
INSERT INTO facilitator_settlements
    (authorization_id, request_digest, request_body, network, signer_address, state, response)
VALUES (sqlc.arg(authorization_id), sqlc.arg(request_digest), sqlc.arg(request_body),
        sqlc.arg(network), sqlc.arg(signer_address), 'failed', sqlc.arg(response))
ON CONFLICT DO NOTHING;

-- name: FacilitatorReconcileUnsigned :execrows
UPDATE facilitator_settlements SET state='reconciling',updated_at=now()
WHERE authorization_id=$1 AND state='reserved'
  AND nonce IS NULL AND raw_transaction IS NULL AND transaction_hash IS NULL;

-- name: FacilitatorConfirmTransaction :execrows
UPDATE facilitator_settlements SET transaction_confirmed=true,broadcast_status='confirmed',updated_at=now()
WHERE authorization_id=$1 AND transaction_hash=$2;

-- name: FacilitatorScanCursor :execrows
UPDATE facilitator_settlements SET scan_cursor=$2,updated_at=now()
WHERE authorization_id=$1 AND state IN ('signed','reconciling') AND response IS NULL;

-- name: FacilitatorSucceed :execrows
UPDATE facilitator_settlements SET state='succeeded',response=$2,settlement_evidence=$3,updated_at=now()
WHERE authorization_id=$1 AND state IN ('signed','reconciling') AND response IS NULL;

-- name: FacilitatorTouchRecovery :execrows
UPDATE facilitator_settlements SET updated_at=now()
WHERE authorization_id=$1 AND (state IN ('reserved','signed','reconciling') OR (transaction_hash IS NOT NULL AND NOT transaction_confirmed));
