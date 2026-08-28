-- name: InsertBillingPayment :one
INSERT INTO billing_payments (
    id,
    chain_id,
    fingerprint,
    reservation_owner,
    method,
    operation,
    resource_digest,
    requirement_digest,
    protocol_version,
    scheme,
    network,
    asset,
    amount_atomic,
    recipient,
    user_id,
    api_key_prefix,
    facilitator_digest,
    purpose,
    asset_transfer_method,
    payment_flow,
    fingerprint_version,
    topup_intent_id,
    state,
    reservation_expires_at,
    created_at,
    updated_at
) VALUES (
    sqlc.arg(id)::uuid,
    sqlc.arg(chain_id)::numeric,
    sqlc.arg(fingerprint),
    sqlc.arg(reservation_owner)::uuid,
    sqlc.arg(method),
    sqlc.arg(operation),
    sqlc.arg(resource_digest),
    sqlc.arg(requirement_digest),
    2,
    'exact',
    sqlc.arg(network),
    sqlc.arg(asset),
    sqlc.arg(amount_atomic)::numeric,
    sqlc.arg(recipient),
    NULL::uuid,
    sqlc.narg(api_key_prefix)::text,
    sqlc.arg(facilitator_digest),
    sqlc.arg(purpose),
    sqlc.arg(asset_transfer_method),
    sqlc.arg(payment_flow),
    sqlc.arg(fingerprint_version),
    sqlc.narg(topup_intent_id)::uuid,
    'reserved',
    sqlc.arg(reservation_expires_at),
    sqlc.arg(created_at),
    sqlc.arg(created_at)
)
ON CONFLICT (fingerprint) DO NOTHING
RETURNING *;

-- name: GetBillingPaymentByFingerprint :one
SELECT *
FROM billing_payments
WHERE fingerprint = sqlc.arg(fingerprint);

-- name: GetBillingPaymentByID :one
SELECT *
FROM billing_payments
WHERE id = sqlc.arg(id)::uuid
  AND chain_id = sqlc.arg(chain_id)::numeric;

-- name: GetBillingPaymentForInspection :one
SELECT *
FROM billing_payments
WHERE id = sqlc.arg(id)::uuid
  AND chain_id = sqlc.arg(chain_id)::numeric
FOR SHARE;

-- name: ListBillingPaymentEvents :many
SELECT *
FROM billing_payment_events
WHERE payment_id = sqlc.arg(payment_id)::uuid
ORDER BY id;

-- name: AppendBillingPaymentEvent :exec
INSERT INTO billing_payment_events (
    payment_id,
    from_state,
    to_state,
    code,
    actor,
    transaction_hash,
    occurred_at
) VALUES (
    sqlc.arg(payment_id)::uuid,
    sqlc.narg(from_state)::text,
    sqlc.arg(to_state),
    sqlc.arg(code),
    sqlc.arg(actor),
    sqlc.narg(transaction_hash)::bytea,
    sqlc.arg(occurred_at)
);

-- name: MarkBillingPaymentVerified :one
WITH updated AS (
    UPDATE billing_payments AS payment
    SET state = 'verified',
        payer = sqlc.arg(payer),
        user_id = COALESCE(payment.user_id, sqlc.narg(user_id)::uuid),
        api_key_prefix = COALESCE(
            payment.api_key_prefix,
            sqlc.narg(api_key_prefix)::text
        ),
        verified_at = sqlc.arg(transitioned_at),
        updated_at = sqlc.arg(transitioned_at)
    WHERE id = sqlc.arg(id)::uuid
      AND reservation_owner = sqlc.arg(reservation_owner)::uuid
      AND state = 'reserved'
      AND reservation_expires_at > sqlc.arg(transitioned_at)
      AND (
          sqlc.narg(user_id)::uuid IS NULL
          OR EXISTS (
              SELECT 1
              FROM users AS matched_user
              WHERE matched_user.id = sqlc.narg(user_id)::uuid
                AND matched_user.chain_id = payment.chain_id
                AND matched_user.address = sqlc.arg(payer)
          )
      )
    RETURNING *
), event AS (
    INSERT INTO billing_payment_events (
        payment_id, from_state, to_state, code, actor, occurred_at
    )
    SELECT id, 'reserved', 'verified', 'payment_verified', 'runtime',
           sqlc.arg(transitioned_at)
    FROM updated
)
SELECT id FROM updated;

-- name: StartBillingPaymentHandler :one
WITH updated AS (
    UPDATE billing_payments
    SET handler_started_at = sqlc.arg(transitioned_at),
        updated_at = sqlc.arg(transitioned_at)
    WHERE id = sqlc.arg(id)::uuid
      AND reservation_owner = sqlc.arg(reservation_owner)::uuid
      AND purpose = 'legacy_request'
      AND state = 'verified'
      AND handler_started_at IS NULL
      AND reservation_expires_at > sqlc.arg(transitioned_at)
    RETURNING *
), event AS (
    INSERT INTO billing_payment_events (
        payment_id, from_state, to_state, code, actor, occurred_at
    )
    SELECT id, 'verified', 'verified', 'handler_started', 'runtime',
           sqlc.arg(transitioned_at)
    FROM updated
)
SELECT id FROM updated;

-- name: MarkBillingPaymentSettling :one
WITH updated AS (
    UPDATE billing_payments
    SET state = 'settling',
        settling_at = sqlc.arg(transitioned_at),
        updated_at = sqlc.arg(transitioned_at)
    WHERE id = sqlc.arg(id)::uuid
      AND reservation_owner = sqlc.arg(reservation_owner)::uuid
      AND purpose = 'legacy_request'
      AND state = 'verified'
      AND handler_started_at IS NOT NULL
      AND reservation_expires_at > sqlc.arg(transitioned_at)
    RETURNING *
), event AS (
    INSERT INTO billing_payment_events (
        payment_id, from_state, to_state, code, actor, occurred_at
    )
    SELECT id, 'verified', 'settling', 'settlement_started', 'runtime',
           sqlc.arg(transitioned_at)
    FROM updated
)
SELECT id FROM updated;

-- name: MarkBillingPaymentSettlementUnknown :one
WITH updated AS (
    UPDATE billing_payments
    SET failure_code = 'settlement_unknown',
        updated_at = sqlc.arg(transitioned_at)
    WHERE id = sqlc.arg(id)::uuid
      AND reservation_owner = sqlc.arg(reservation_owner)::uuid
      AND purpose = 'legacy_request'
      AND state = 'settling'
      AND failure_code IS NULL
    RETURNING *
), event AS (
    INSERT INTO billing_payment_events (
        payment_id, from_state, to_state, code, actor, occurred_at
    )
    SELECT id, 'settling', 'settling', 'settlement_unknown', 'runtime',
           sqlc.arg(transitioned_at)
    FROM updated
)
SELECT id FROM updated;

-- name: MarkBillingPaymentSettlementPending :one
WITH updated AS (
    UPDATE billing_payments
    SET failure_code = 'settlement_pending',
        transaction_hash = sqlc.arg(transaction_hash),
        updated_at = sqlc.arg(transitioned_at)
    WHERE id = sqlc.arg(id)::uuid
      AND reservation_owner = sqlc.arg(reservation_owner)::uuid
      AND purpose = 'legacy_request'
      AND state = 'settling'
      AND failure_code IS NULL
      AND transaction_hash IS NULL
    RETURNING *
), event AS (
    INSERT INTO billing_payment_events (
        payment_id, from_state, to_state, code, actor, transaction_hash, occurred_at
    )
    SELECT id, 'settling', 'settling', 'settlement_pending', 'runtime',
           sqlc.arg(transaction_hash), sqlc.arg(transitioned_at)
    FROM updated
)
SELECT id FROM updated;

-- name: MarkBillingPaymentSettled :one
WITH updated AS (
    UPDATE billing_payments
    SET state = 'settled',
        transaction_hash = sqlc.arg(transaction_hash),
        failure_code = NULL,
        settled_at = sqlc.arg(transitioned_at),
        updated_at = sqlc.arg(transitioned_at)
    WHERE id = sqlc.arg(id)::uuid
      AND purpose = 'legacy_request'
      AND state = 'settling'
      AND reservation_owner = sqlc.arg(reservation_owner)::uuid
      AND failure_code IS NULL
    RETURNING *
), event AS (
    INSERT INTO billing_payment_events (
        payment_id,
        from_state,
        to_state,
        code,
        actor,
        transaction_hash,
        occurred_at
    )
    SELECT id,
           'settling',
           'settled',
           'payment_settled',
           'runtime',
           sqlc.arg(transaction_hash),
           sqlc.arg(transitioned_at)
    FROM updated
)
SELECT id FROM updated;

-- name: MarkBillingPaymentFailed :one
WITH updated AS (
    UPDATE billing_payments
    SET state = 'failed',
        failure_code = sqlc.arg(failure_code),
        failed_at = sqlc.arg(transitioned_at),
        updated_at = sqlc.arg(transitioned_at)
    WHERE id = sqlc.arg(id)::uuid
      AND purpose = 'legacy_request'
      AND (
          (
              state IN ('reserved', 'verified')
              AND reservation_owner = sqlc.arg(reservation_owner)::uuid
          )
          OR (
              state = 'settling'
              AND reservation_owner = sqlc.arg(reservation_owner)::uuid
              AND failure_code IS NULL
          )
      )
    RETURNING *
), event AS (
    INSERT INTO billing_payment_events (
        payment_id, from_state, to_state, code, actor, occurred_at
    )
    SELECT id,
           CASE
               WHEN settling_at IS NOT NULL THEN 'settling'
               WHEN verified_at IS NOT NULL THEN 'verified'
               ELSE 'reserved'
           END,
           'failed',
           sqlc.arg(failure_code),
           'runtime',
           sqlc.arg(transitioned_at)
    FROM updated
)
SELECT id FROM updated;

-- name: ReconcileBillingPaymentSettled :one
WITH candidate AS (
    SELECT id, failure_code, transaction_hash
    FROM billing_payments
    WHERE id = sqlc.arg(id)::uuid
      AND chain_id = sqlc.arg(chain_id)::numeric
      AND purpose = 'legacy_request'
      AND state = 'settling'
      AND sqlc.arg(transitioned_at)::timestamptz >= settling_at
      AND sqlc.arg(transitioned_at)::timestamptz >= updated_at
      AND (
          failure_code IN ('settlement_unknown', 'settlement_pending')
          OR (
              failure_code IS NULL
              AND settling_at <= sqlc.arg(stale_before)::timestamptz
          )
      )
    FOR UPDATE
), updated AS (
    UPDATE billing_payments AS payment
    SET state = 'settled',
        transaction_hash = COALESCE(payment.transaction_hash, sqlc.arg(transaction_hash)),
        failure_code = NULL,
        settled_at = sqlc.arg(transitioned_at),
        updated_at = sqlc.arg(transitioned_at)
    FROM candidate
    WHERE payment.id = candidate.id
      AND (payment.transaction_hash IS NULL OR payment.transaction_hash = sqlc.arg(transaction_hash))
    RETURNING payment.id, payment.transaction_hash,
              candidate.failure_code AS prior_failure_code
), event AS (
    INSERT INTO billing_payment_events (
        payment_id,
        from_state,
        to_state,
        code,
        actor,
        transaction_hash,
        occurred_at
    )
    SELECT id,
           'settling',
           'settled',
           CASE
               WHEN prior_failure_code IN ('settlement_unknown', 'settlement_pending')
                   THEN 'operator_reconciled_settled'
               ELSE 'operator_reconciled_stale_settling_settled'
           END,
           'operator',
           transaction_hash,
           sqlc.arg(transitioned_at)
    FROM updated
)
SELECT id FROM updated;

-- name: ReconcileBillingPaymentFailed :one
WITH candidate AS (
    SELECT id, failure_code, transaction_hash
    FROM billing_payments
    WHERE id = sqlc.arg(id)::uuid
      AND chain_id = sqlc.arg(chain_id)::numeric
      AND purpose = 'legacy_request'
      AND state = 'settling'
      AND sqlc.arg(transitioned_at)::timestamptz >= settling_at
      AND sqlc.arg(transitioned_at)::timestamptz >= updated_at
      AND (
          failure_code IN ('settlement_unknown', 'settlement_pending')
          OR (
              failure_code IS NULL
              AND settling_at <= sqlc.arg(stale_before)::timestamptz
          )
      )
    FOR UPDATE
), updated AS (
    UPDATE billing_payments AS payment
    SET state = 'failed',
        failure_code = 'operator_reconciled_failed',
        failed_at = sqlc.arg(transitioned_at),
        updated_at = sqlc.arg(transitioned_at)
    FROM candidate
    WHERE payment.id = candidate.id
    RETURNING payment.id, payment.transaction_hash,
              candidate.failure_code AS prior_failure_code
), event AS (
    INSERT INTO billing_payment_events (
        payment_id, from_state, to_state, code, actor, transaction_hash, occurred_at
    )
    SELECT id,
           'settling',
           'failed',
           CASE
               WHEN prior_failure_code IN ('settlement_unknown', 'settlement_pending')
                   THEN 'operator_reconciled_failed'
               ELSE 'operator_reconciled_stale_settling_failed'
           END,
           'operator',
           transaction_hash,
           sqlc.arg(transitioned_at)
    FROM updated
)
SELECT id FROM updated;

-- name: ExpireBillingPayments :one
WITH candidates AS (
    SELECT payment.id
    FROM billing_payments AS payment
    WHERE payment.chain_id = sqlc.arg(chain_id)::numeric
      AND payment.purpose = 'legacy_request'
      AND payment.state IN ('reserved', 'verified')
      AND payment.reservation_expires_at <= sqlc.arg(observed_at)
    ORDER BY payment.reservation_expires_at, payment.id
    FOR UPDATE SKIP LOCKED
    LIMIT sqlc.arg(expire_limit)
), updated AS (
    UPDATE billing_payments AS payment
    SET state = 'expired',
        failure_code = 'reservation_expired',
        expired_at = sqlc.arg(observed_at),
        updated_at = sqlc.arg(observed_at)
    FROM candidates
    WHERE payment.id = candidates.id
    RETURNING payment.*
), event AS (
    INSERT INTO billing_payment_events (
        payment_id, from_state, to_state, code, actor, occurred_at
    )
    SELECT id,
           CASE WHEN verified_at IS NULL THEN 'reserved' ELSE 'verified' END,
           'expired',
           'reservation_expired',
           'runtime',
           sqlc.arg(observed_at)
    FROM updated
)
SELECT count(*)::bigint AS expired_count
FROM updated;

-- name: ListUserBillingPayments :many
SELECT *
FROM billing_payments
WHERE chain_id = sqlc.arg(chain_id)::numeric
  AND user_id = sqlc.arg(user_id)::uuid
  AND (
      sqlc.narg(before_created_at)::timestamptz IS NULL
      OR (created_at, id) < (
          sqlc.narg(before_created_at)::timestamptz,
          sqlc.narg(before_id)::uuid
      )
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit);

-- name: ListAdminBillingPayments :many
SELECT *
FROM billing_payments
WHERE chain_id = sqlc.arg(chain_id)::numeric
  AND (
      sqlc.narg(state)::text IS NULL
      OR state = sqlc.narg(state)::text
  )
  AND (
      sqlc.narg(operation)::text IS NULL
      OR operation = sqlc.narg(operation)::text
  )
  AND (
      sqlc.narg(network)::text IS NULL
      OR network = sqlc.narg(network)::text
  )
  AND (
      sqlc.narg(asset)::bytea IS NULL
      OR asset = sqlc.narg(asset)::bytea
  )
  AND (
      sqlc.narg(from_time)::timestamptz IS NULL
      OR created_at >= sqlc.narg(from_time)::timestamptz
  )
  AND (
      sqlc.narg(to_time)::timestamptz IS NULL
      OR created_at < sqlc.narg(to_time)::timestamptz
  )
  AND (
      sqlc.narg(before_created_at)::timestamptz IS NULL
      OR (created_at, id) < (
          sqlc.narg(before_created_at)::timestamptz,
          sqlc.narg(before_id)::uuid
      )
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit);

-- name: SummarizeBillingPayments :many
SELECT
    state,
    operation,
    network,
    asset,
    count(*)::numeric AS payment_count,
    COALESCE(sum(amount_atomic), 0)::numeric AS amount_atomic
FROM billing_payments
WHERE chain_id = sqlc.arg(chain_id)::numeric
  AND created_at >= sqlc.arg(from_time)
  AND created_at < sqlc.arg(to_time)
  AND (
      sqlc.narg(state)::text IS NULL
      OR state = sqlc.narg(state)::text
  )
  AND (
      sqlc.narg(operation)::text IS NULL
      OR operation = sqlc.narg(operation)::text
  )
  AND (
      sqlc.narg(network)::text IS NULL
      OR network = sqlc.narg(network)::text
  )
  AND (
      sqlc.narg(asset)::bytea IS NULL
      OR asset = sqlc.narg(asset)::bytea
  )
GROUP BY state, operation, network, asset
ORDER BY state, operation, network, asset;

-- name: GetX402TestnetWriterFence :one
SELECT
    pg_is_in_recovery() AS in_recovery,
    current_setting('transaction_read_only')::text AS transaction_read_only,
    clock_timestamp()::timestamptz AS created_at_fence;

-- name: FindX402TestnetBillingPayments :many
SELECT id
FROM billing_payments
WHERE chain_id = sqlc.arg(chain_id)::numeric
  AND operation = sqlc.arg(operation)
  AND protocol_version = 2
  AND scheme = 'exact'
  AND resource_digest = sqlc.arg(resource_digest)
  AND requirement_digest = sqlc.arg(requirement_digest)
  AND network = sqlc.arg(network)
  AND asset = sqlc.arg(asset)
  AND amount_atomic = sqlc.arg(amount_atomic)::numeric
  AND recipient = sqlc.arg(recipient)
  AND payer = sqlc.arg(payer)
  AND created_at >= sqlc.arg(created_at_fence)::timestamptz
  AND api_key_prefix IS NULL
ORDER BY created_at, id
LIMIT 2;
