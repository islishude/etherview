-- name: CreateBillingTopupIntent :one
INSERT INTO billing_topup_intents (
    id, user_id, chain_id, network, asset, amount_atomic, recipient, payer,
    state, expires_at, created_at, updated_at
)
SELECT
    sqlc.arg(id)::uuid,
    users.id,
    users.chain_id,
    sqlc.arg(network),
    sqlc.arg(asset),
    sqlc.arg(amount_atomic)::numeric,
    sqlc.arg(recipient),
    users.address,
    'open',
    sqlc.arg(expires_at),
    sqlc.arg(created_at),
    sqlc.arg(created_at)
FROM users
WHERE users.id = sqlc.arg(user_id)::uuid
  AND users.chain_id = sqlc.arg(chain_id)::numeric
  AND users.address = sqlc.arg(payer)
  AND users.status = 'active'
RETURNING *;

-- name: GetBillingTopupIntent :one
SELECT *
FROM billing_topup_intents
WHERE id = sqlc.arg(id)::uuid
  AND chain_id = sqlc.arg(chain_id)::numeric;

-- name: GetUserBillingTopupIntent :one
SELECT *
FROM billing_topup_intents
WHERE id = sqlc.arg(id)::uuid
  AND chain_id = sqlc.arg(chain_id)::numeric
  AND user_id = sqlc.arg(user_id)::uuid;

-- name: ListUserBillingTopupIntents :many
SELECT *
FROM billing_topup_intents
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

-- name: EnsureBillingAccount :one
WITH inserted AS (
    INSERT INTO billing_accounts (
        user_id, chain_id, network, asset, created_at, updated_at
    )
    SELECT users.id, users.chain_id, sqlc.arg(network), sqlc.arg(asset),
           sqlc.arg(created_at), sqlc.arg(created_at)
    FROM users
    WHERE users.id = sqlc.arg(user_id)::uuid
      AND users.chain_id = sqlc.arg(chain_id)::numeric
      AND users.status = 'active'
    ON CONFLICT (user_id, chain_id, network, asset) DO NOTHING
    RETURNING *
)
SELECT * FROM inserted
UNION ALL
SELECT * FROM billing_accounts
WHERE user_id = sqlc.arg(user_id)::uuid
  AND chain_id = sqlc.arg(chain_id)::numeric
  AND network = sqlc.arg(network)
  AND asset = sqlc.arg(asset)
LIMIT 1;

-- name: ClaimBillingTopupIntent :one
UPDATE billing_topup_intents
SET state = 'processing', active_payment_id = sqlc.arg(payment_id)::uuid,
    processing_at = sqlc.arg(transitioned_at), updated_at = sqlc.arg(transitioned_at)
WHERE id = sqlc.arg(id)::uuid
  AND user_id = sqlc.arg(user_id)::uuid
  AND chain_id = sqlc.arg(chain_id)::numeric
  AND payer = sqlc.arg(payer)
  AND state = 'open'
  AND expires_at > sqlc.arg(transitioned_at)
RETURNING id;

-- name: BeginBillingTopupSettlement :one
WITH candidate AS (
    SELECT payment.id, payment.topup_intent_id
    FROM billing_payments AS payment
    JOIN billing_topup_intents AS intent
      ON intent.id = payment.topup_intent_id
    WHERE payment.id = sqlc.arg(payment_id)::uuid
      AND payment.reservation_owner = sqlc.arg(reservation_owner)::uuid
      AND payment.purpose = 'account_topup'
      AND payment.topup_intent_id = sqlc.arg(intent_id)::uuid
      AND payment.state = 'verified'
      AND payment.handler_started_at IS NULL
      AND payment.reservation_expires_at > sqlc.arg(transitioned_at)
      AND intent.state = 'processing'
      AND intent.active_payment_id = payment.id
    FOR UPDATE OF payment, intent
), payment_update AS (
    UPDATE billing_payments
    SET state = 'settling', handler_started_at = sqlc.arg(transitioned_at),
        settling_at = sqlc.arg(transitioned_at), updated_at = sqlc.arg(transitioned_at)
    FROM candidate
    WHERE billing_payments.id = candidate.id
    RETURNING billing_payments.id, billing_payments.topup_intent_id
), intent_update AS (
    UPDATE billing_topup_intents AS intent
    SET state = 'settling', settling_at = sqlc.arg(transitioned_at),
        updated_at = sqlc.arg(transitioned_at)
    FROM payment_update
    WHERE intent.id = payment_update.topup_intent_id
    RETURNING intent.id, payment_update.id AS payment_id
), events AS (
    INSERT INTO billing_payment_events (
        payment_id, from_state, to_state, code, actor, occurred_at
    )
    SELECT payment_id, 'verified', 'settling', 'settlement_started', 'runtime',
           sqlc.arg(transitioned_at)
    FROM intent_update
)
SELECT payment_id FROM intent_update;

-- name: MarkBillingTopupSettlementUnknown :one
WITH payment_update AS (
    UPDATE billing_payments
    SET failure_code = 'settlement_unknown', updated_at = sqlc.arg(transitioned_at)
    WHERE id = sqlc.arg(payment_id)::uuid
      AND reservation_owner = sqlc.arg(reservation_owner)::uuid
      AND purpose = 'account_topup'
      AND topup_intent_id = sqlc.arg(intent_id)::uuid
      AND state = 'settling'
      AND failure_code IS NULL
    RETURNING id, topup_intent_id
), intent_update AS (
    UPDATE billing_topup_intents AS intent
    SET failure_code = 'settlement_unknown', updated_at = sqlc.arg(transitioned_at)
    FROM payment_update
    WHERE intent.id = payment_update.topup_intent_id
      AND intent.state = 'settling'
      AND intent.active_payment_id = payment_update.id
    RETURNING payment_update.id AS payment_id
), events AS (
    INSERT INTO billing_payment_events (
        payment_id, from_state, to_state, code, actor, occurred_at
    )
    SELECT payment_id, 'settling', 'settling', 'settlement_unknown', 'runtime',
           sqlc.arg(transitioned_at)
    FROM intent_update
)
SELECT payment_id FROM intent_update;

-- name: MarkBillingTopupSettlementPending :one
WITH payment_update AS (
    UPDATE billing_payments
    SET failure_code = 'settlement_pending', transaction_hash = sqlc.arg(transaction_hash),
        updated_at = sqlc.arg(transitioned_at)
    WHERE id = sqlc.arg(payment_id)::uuid
      AND reservation_owner = sqlc.arg(reservation_owner)::uuid
      AND purpose = 'account_topup'
      AND topup_intent_id = sqlc.arg(intent_id)::uuid
      AND state = 'settling'
      AND failure_code IS NULL
      AND transaction_hash IS NULL
    RETURNING id, topup_intent_id
), intent_update AS (
    UPDATE billing_topup_intents AS intent
    SET failure_code = 'settlement_pending', transaction_hash = sqlc.arg(transaction_hash),
        updated_at = sqlc.arg(transitioned_at)
    FROM payment_update
    WHERE intent.id = payment_update.topup_intent_id
      AND intent.state = 'settling'
      AND intent.active_payment_id = payment_update.id
    RETURNING payment_update.id AS payment_id
), events AS (
    INSERT INTO billing_payment_events (
        payment_id, from_state, to_state, code, actor, transaction_hash, occurred_at
    )
    SELECT payment_id, 'settling', 'settling', 'settlement_pending', 'runtime',
           sqlc.arg(transaction_hash), sqlc.arg(transitioned_at)
    FROM intent_update
)
SELECT payment_id FROM intent_update;

-- name: CreditBillingTopup :one
WITH candidate AS (
    SELECT payment.id, payment.topup_intent_id, payment.user_id,
           payment.chain_id, payment.network, payment.asset,
           payment.amount_atomic
    FROM billing_payments AS payment
    JOIN billing_topup_intents AS intent ON intent.id = payment.topup_intent_id
    WHERE payment.id = sqlc.arg(payment_id)::uuid
      AND payment.reservation_owner = sqlc.arg(reservation_owner)::uuid
      AND payment.purpose = 'account_topup'
      AND payment.state = 'settling'
      AND payment.failure_code IS NULL
      AND payment.user_id IS NOT NULL
      AND intent.state = 'settling'
      AND intent.active_payment_id = payment.id
      AND intent.user_id = payment.user_id
    FOR UPDATE OF payment, intent
), payment_update AS (
    UPDATE billing_payments AS payment
    SET state = 'settled', transaction_hash = sqlc.arg(transaction_hash),
        settled_at = sqlc.arg(transitioned_at), updated_at = sqlc.arg(transitioned_at)
    FROM candidate
    WHERE payment.id = candidate.id
    RETURNING candidate.*
), intent_update AS (
    UPDATE billing_topup_intents AS intent
    SET state = 'credited', failure_code = NULL,
        transaction_hash = sqlc.arg(transaction_hash),
        credited_at = sqlc.arg(transitioned_at), updated_at = sqlc.arg(transitioned_at)
    FROM payment_update
    WHERE intent.id = payment_update.topup_intent_id
    RETURNING payment_update.*
), account_update AS (
    UPDATE billing_accounts AS account
    SET total_credit_atomic = account.total_credit_atomic + intent_update.amount_atomic,
        updated_at = sqlc.arg(transitioned_at)
    FROM intent_update
    WHERE account.user_id = intent_update.user_id
      AND account.chain_id = intent_update.chain_id
      AND account.network = intent_update.network
      AND account.asset = intent_update.asset
    RETURNING account.*, intent_update.id AS payment_id,
              intent_update.amount_atomic AS topup_amount_atomic
), entry AS (
    INSERT INTO billing_account_entries (
        id, user_id, chain_id, network, asset, direction, kind,
        amount_atomic, source_id, occurred_at
    )
    SELECT sqlc.arg(entry_id)::uuid, user_id, chain_id, network, asset,
           'credit', 'topup', topup_amount_atomic, payment_id, sqlc.arg(transitioned_at)
    FROM account_update
), event AS (
    INSERT INTO billing_payment_events (
        payment_id, from_state, to_state, code, actor, transaction_hash, occurred_at
    )
    SELECT payment_id, 'settling', 'settled', 'payment_settled', 'runtime',
           sqlc.arg(transaction_hash), sqlc.arg(transitioned_at)
    FROM account_update
)
SELECT user_id, chain_id, network, asset, total_credit_atomic,
       total_debit_atomic, reserved_atomic, created_at, updated_at
FROM account_update;

-- name: ReconcileBillingTopupFailed :one
WITH candidate AS (
    SELECT payment.id, payment.topup_intent_id, payment.failure_code,
           payment.transaction_hash
    FROM billing_payments AS payment
    JOIN billing_topup_intents AS intent ON intent.id = payment.topup_intent_id
    WHERE payment.id = sqlc.arg(payment_id)::uuid
      AND payment.chain_id = sqlc.arg(chain_id)::numeric
      AND payment.purpose = 'account_topup'
      AND payment.state = 'settling'
      AND sqlc.arg(transitioned_at)::timestamptz >= payment.settling_at
      AND sqlc.arg(transitioned_at)::timestamptz >= payment.updated_at
      AND (
          payment.failure_code IN ('settlement_unknown', 'settlement_pending')
          OR (
              payment.failure_code IS NULL
              AND payment.settling_at <= sqlc.arg(stale_before)::timestamptz
          )
      )
      AND intent.state = 'settling'
      AND intent.active_payment_id = payment.id
    FOR UPDATE OF payment, intent
), payment_update AS (
    UPDATE billing_payments AS payment
    SET state = 'failed', failure_code = 'operator_reconciled_failed',
        failed_at = sqlc.arg(transitioned_at), updated_at = sqlc.arg(transitioned_at)
    FROM candidate
    WHERE payment.id = candidate.id
    RETURNING payment.id, payment.topup_intent_id, payment.transaction_hash,
              candidate.failure_code AS prior_failure_code
), intent_update AS (
    UPDATE billing_topup_intents AS intent
    SET state = 'failed', failure_code = 'operator_reconciled_failed',
        failed_at = sqlc.arg(transitioned_at), updated_at = sqlc.arg(transitioned_at)
    FROM payment_update
    WHERE intent.id = payment_update.topup_intent_id
    RETURNING payment_update.*
), event AS (
    INSERT INTO billing_payment_events (
        payment_id, from_state, to_state, code, actor, transaction_hash, occurred_at
    )
    SELECT id, 'settling', 'failed',
           CASE WHEN prior_failure_code IS NULL
                THEN 'operator_reconciled_stale_settling_failed'
                ELSE 'operator_reconciled_failed' END,
           'operator', transaction_hash, sqlc.arg(transitioned_at)
    FROM intent_update
)
SELECT id FROM intent_update;

-- name: ReconcileBillingTopupSettled :one
WITH candidate AS (
    SELECT payment.id, payment.topup_intent_id, payment.user_id,
           payment.chain_id, payment.network, payment.asset,
           payment.amount_atomic, payment.failure_code,
           payment.transaction_hash
    FROM billing_payments AS payment
    JOIN billing_topup_intents AS intent ON intent.id = payment.topup_intent_id
    JOIN billing_accounts AS account
      ON account.user_id = payment.user_id
     AND account.chain_id = payment.chain_id
     AND account.network = payment.network
     AND account.asset = payment.asset
    WHERE payment.id = sqlc.arg(payment_id)::uuid
      AND payment.chain_id = sqlc.arg(chain_id)::numeric
      AND payment.purpose = 'account_topup'
      AND payment.state = 'settling'
      AND payment.user_id IS NOT NULL
      AND sqlc.arg(transitioned_at)::timestamptz >= payment.settling_at
      AND sqlc.arg(transitioned_at)::timestamptz >= payment.updated_at
      AND (
          payment.failure_code IN ('settlement_unknown', 'settlement_pending')
          OR (
              payment.failure_code IS NULL
              AND payment.settling_at <= sqlc.arg(stale_before)::timestamptz
          )
      )
      AND (payment.transaction_hash IS NULL
           OR payment.transaction_hash = sqlc.arg(transaction_hash))
      AND intent.state = 'settling'
      AND intent.active_payment_id = payment.id
      AND intent.user_id = payment.user_id
    FOR UPDATE OF payment, intent, account
), payment_update AS (
    UPDATE billing_payments AS payment
    SET state = 'settled',
        transaction_hash = COALESCE(payment.transaction_hash, sqlc.arg(transaction_hash)),
        failure_code = NULL, settled_at = sqlc.arg(transitioned_at),
        updated_at = sqlc.arg(transitioned_at)
    FROM candidate
    WHERE payment.id = candidate.id
    RETURNING candidate.*
), intent_update AS (
    UPDATE billing_topup_intents AS intent
    SET state = 'credited', failure_code = NULL,
        transaction_hash = COALESCE(intent.transaction_hash, sqlc.arg(transaction_hash)),
        credited_at = sqlc.arg(transitioned_at), updated_at = sqlc.arg(transitioned_at)
    FROM payment_update
    WHERE intent.id = payment_update.topup_intent_id
    RETURNING payment_update.*
), account_update AS (
    UPDATE billing_accounts AS account
    SET total_credit_atomic = account.total_credit_atomic + intent_update.amount_atomic,
        updated_at = sqlc.arg(transitioned_at)
    FROM intent_update
    WHERE account.user_id = intent_update.user_id
      AND account.chain_id = intent_update.chain_id
      AND account.network = intent_update.network
      AND account.asset = intent_update.asset
    RETURNING account.*, intent_update.id AS payment_id,
              intent_update.amount_atomic AS topup_amount_atomic,
              intent_update.failure_code AS prior_failure_code,
              intent_update.transaction_hash AS prior_transaction_hash
), entry AS (
    INSERT INTO billing_account_entries (
        id, user_id, chain_id, network, asset, direction, kind,
        amount_atomic, source_id, occurred_at
    )
    SELECT sqlc.arg(entry_id)::uuid, user_id, chain_id, network, asset,
           'credit', 'topup', topup_amount_atomic, payment_id,
           sqlc.arg(transitioned_at)
    FROM account_update
), event AS (
    INSERT INTO billing_payment_events (
        payment_id, from_state, to_state, code, actor, transaction_hash, occurred_at
    )
    SELECT payment_id, 'settling', 'settled',
           CASE WHEN prior_failure_code IS NULL
                THEN 'operator_reconciled_stale_settling_settled'
                ELSE 'operator_reconciled_settled' END,
           'operator', COALESCE(prior_transaction_hash, sqlc.arg(transaction_hash)),
           sqlc.arg(transitioned_at)
    FROM account_update
)
SELECT user_id, chain_id, network, asset, total_credit_atomic,
       total_debit_atomic, reserved_atomic, created_at, updated_at
FROM account_update;

-- name: FailBillingTopupPayment :one
WITH payment_update AS (
    UPDATE billing_payments
    SET state = 'failed', failure_code = sqlc.arg(failure_code),
        failed_at = sqlc.arg(transitioned_at), updated_at = sqlc.arg(transitioned_at)
    WHERE id = sqlc.arg(payment_id)::uuid
      AND reservation_owner = sqlc.arg(reservation_owner)::uuid
      AND purpose = 'account_topup'
      AND topup_intent_id = sqlc.arg(intent_id)::uuid
      AND state IN ('reserved', 'verified')
    RETURNING id, topup_intent_id, verified_at
), intent_update AS (
    UPDATE billing_topup_intents AS intent
    SET state = CASE WHEN intent.expires_at <= sqlc.arg(transitioned_at)
                     THEN 'expired' ELSE 'open' END,
        active_payment_id = NULL,
        processing_at = NULL,
        failure_code = CASE WHEN intent.expires_at <= sqlc.arg(transitioned_at)
                            THEN 'topup_intent_expired' ELSE NULL END,
        expired_at = CASE WHEN intent.expires_at <= sqlc.arg(transitioned_at)
                          THEN sqlc.arg(transitioned_at) ELSE NULL END,
        updated_at = sqlc.arg(transitioned_at)
    FROM payment_update
    WHERE intent.id = payment_update.topup_intent_id
      AND intent.state = 'processing'
      AND intent.active_payment_id = payment_update.id
    RETURNING payment_update.id AS payment_id, payment_update.verified_at
), event AS (
    INSERT INTO billing_payment_events (
        payment_id, from_state, to_state, code, actor, occurred_at
    )
    SELECT payment_id,
           CASE WHEN verified_at IS NULL THEN 'reserved' ELSE 'verified' END,
           'failed', sqlc.arg(failure_code), 'runtime', sqlc.arg(transitioned_at)
    FROM intent_update
)
SELECT payment_id FROM intent_update;

-- name: FailBillingTopupSettlement :one
WITH payment_update AS (
    UPDATE billing_payments
    SET state = 'failed', failure_code = sqlc.arg(failure_code),
        failed_at = sqlc.arg(transitioned_at), updated_at = sqlc.arg(transitioned_at)
    WHERE id = sqlc.arg(payment_id)::uuid
      AND reservation_owner = sqlc.arg(reservation_owner)::uuid
      AND purpose = 'account_topup'
      AND topup_intent_id = sqlc.arg(intent_id)::uuid
      AND state = 'settling'
      AND failure_code IS NULL
      AND transaction_hash IS NULL
    RETURNING id, topup_intent_id
), intent_update AS (
    UPDATE billing_topup_intents AS intent
    SET state = CASE WHEN intent.expires_at <= sqlc.arg(transitioned_at)
                     THEN 'expired' ELSE 'open' END,
        active_payment_id = NULL,
        processing_at = NULL,
        settling_at = NULL,
        failure_code = CASE WHEN intent.expires_at <= sqlc.arg(transitioned_at)
                            THEN 'topup_intent_expired' ELSE NULL END,
        expired_at = CASE WHEN intent.expires_at <= sqlc.arg(transitioned_at)
                          THEN sqlc.arg(transitioned_at) ELSE NULL END,
        updated_at = sqlc.arg(transitioned_at)
    FROM payment_update
    WHERE intent.id = payment_update.topup_intent_id
      AND intent.state = 'settling'
      AND intent.active_payment_id = payment_update.id
    RETURNING payment_update.id AS payment_id
), event AS (
    INSERT INTO billing_payment_events (
        payment_id, from_state, to_state, code, actor, occurred_at
    )
    SELECT payment_id, 'settling', 'failed', sqlc.arg(failure_code),
           'runtime', sqlc.arg(transitioned_at)
    FROM intent_update
)
SELECT payment_id FROM intent_update;

-- name: GetBillingAccount :one
SELECT *
FROM billing_accounts
WHERE user_id = sqlc.arg(user_id)::uuid
  AND chain_id = sqlc.arg(chain_id)::numeric
  AND network = sqlc.arg(network)
  AND asset = sqlc.arg(asset);

-- name: ListAdminBillingAccounts :many
SELECT *
FROM billing_accounts
WHERE chain_id = sqlc.arg(chain_id)::numeric
  AND network = sqlc.arg(network)
  AND asset = sqlc.arg(asset)
  AND (
      sqlc.narg(before_updated_at)::timestamptz IS NULL
      OR (updated_at, user_id) < (
          sqlc.narg(before_updated_at)::timestamptz,
          sqlc.narg(before_user_id)::uuid
      )
  )
ORDER BY updated_at DESC, user_id DESC
LIMIT sqlc.arg(page_limit);

-- name: ListAdminBillingTopupIntents :many
SELECT *
FROM billing_topup_intents
WHERE chain_id = sqlc.arg(chain_id)::numeric
  AND network = sqlc.arg(network)
  AND asset = sqlc.arg(asset)
  AND (
      sqlc.narg(before_created_at)::timestamptz IS NULL
      OR (created_at, id) < (
          sqlc.narg(before_created_at)::timestamptz,
          sqlc.narg(before_id)::uuid
      )
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit);

-- name: SummarizeBillingAccounts :one
SELECT count(*)::numeric AS account_count,
       COALESCE(sum(total_credit_atomic), 0)::numeric AS total_credit_atomic,
       COALESCE(sum(total_debit_atomic), 0)::numeric AS total_debit_atomic,
       COALESCE(sum(reserved_atomic), 0)::numeric AS reserved_atomic
FROM billing_accounts
WHERE chain_id = sqlc.arg(chain_id)::numeric
  AND network = sqlc.arg(network)
  AND asset = sqlc.arg(asset);

-- name: ReserveBillingUsage :one
WITH eligible AS (
    SELECT account.user_id, account.chain_id, account.network, account.asset
    FROM billing_accounts AS account
    JOIN users ON users.id = account.user_id
    JOIN api_keys ON api_keys.prefix = sqlc.arg(api_key_prefix)
    WHERE account.user_id = sqlc.arg(user_id)::uuid
      AND account.chain_id = sqlc.arg(chain_id)::numeric
      AND account.network = sqlc.arg(network)
      AND account.asset = sqlc.arg(asset)
      AND users.status = 'active'
      AND api_keys.owner_user_id = account.user_id
      AND api_keys.revoked_at IS NULL
      AND api_keys.scopes @> ARRAY['api:read']::text[]
      AND account.total_credit_atomic - account.total_debit_atomic - account.reserved_atomic
          >= sqlc.arg(amount_atomic)::numeric
    FOR UPDATE OF account
), inserted AS (
    INSERT INTO billing_usage_charges (
        id, reservation_owner, user_id, api_key_prefix, chain_id, network,
        asset, method, operation, resource_digest, amount_atomic, state,
        reservation_expires_at, created_at, updated_at
    )
    SELECT
        sqlc.arg(id)::uuid,
        sqlc.arg(reservation_owner)::uuid,
        eligible.user_id,
        sqlc.arg(api_key_prefix),
        eligible.chain_id,
        eligible.network,
        eligible.asset,
        sqlc.arg(method),
        sqlc.arg(operation),
        sqlc.arg(resource_digest),
        sqlc.arg(amount_atomic)::numeric,
        'reserved',
        sqlc.arg(reservation_expires_at),
        sqlc.arg(created_at),
        sqlc.arg(created_at)
    FROM eligible
    RETURNING *
), updated AS (
    UPDATE billing_accounts AS account
    SET reserved_atomic = account.reserved_atomic + inserted.amount_atomic,
        updated_at = inserted.created_at
    FROM inserted
    WHERE account.user_id = inserted.user_id
      AND account.chain_id = inserted.chain_id
      AND account.network = inserted.network
      AND account.asset = inserted.asset
)
SELECT * FROM inserted;

-- name: CommitBillingUsage :one
WITH candidate AS (
    SELECT *
    FROM billing_usage_charges
    WHERE id = sqlc.arg(id)::uuid
      AND reservation_owner = sqlc.arg(reservation_owner)::uuid
      AND state = 'reserved'
      AND billing_usage_charges.reservation_expires_at > sqlc.arg(committed_at)
    FOR UPDATE
), account_update AS (
    UPDATE billing_accounts AS account
    SET reserved_atomic = account.reserved_atomic - candidate.amount_atomic,
        total_debit_atomic = account.total_debit_atomic + candidate.amount_atomic,
        updated_at = sqlc.arg(committed_at)
    FROM candidate
    WHERE account.user_id = candidate.user_id
      AND account.chain_id = candidate.chain_id
      AND account.network = candidate.network
      AND account.asset = candidate.asset
    RETURNING candidate.*
), entry AS (
    INSERT INTO billing_account_entries (
        id, user_id, chain_id, network, asset, direction, kind,
        amount_atomic, source_id, occurred_at
    )
    SELECT sqlc.arg(entry_id)::uuid, user_id, chain_id, network, asset,
           'debit', 'usage', amount_atomic, id, sqlc.arg(committed_at)
    FROM account_update
), updated AS (
    UPDATE billing_usage_charges AS charge
    SET state = 'committed',
        response_digest = sqlc.arg(response_digest),
        response_bytes = sqlc.arg(response_bytes),
        committed_at = sqlc.arg(committed_at),
        updated_at = sqlc.arg(committed_at)
    FROM account_update
    WHERE charge.id = account_update.id
    RETURNING charge.*
)
SELECT * FROM updated;

-- name: ReleaseBillingUsage :one
WITH candidate AS (
    SELECT *
    FROM billing_usage_charges
    WHERE id = sqlc.arg(id)::uuid
      AND reservation_owner = sqlc.arg(reservation_owner)::uuid
      AND state = 'reserved'
    FOR UPDATE
), account_update AS (
    UPDATE billing_accounts AS account
    SET reserved_atomic = account.reserved_atomic - candidate.amount_atomic,
        updated_at = sqlc.arg(released_at)
    FROM candidate
    WHERE account.user_id = candidate.user_id
      AND account.chain_id = candidate.chain_id
      AND account.network = candidate.network
      AND account.asset = candidate.asset
    RETURNING candidate.*
), updated AS (
    UPDATE billing_usage_charges AS charge
    SET state = 'released', failure_code = sqlc.arg(failure_code),
        released_at = sqlc.arg(released_at), updated_at = sqlc.arg(released_at)
    FROM account_update
    WHERE charge.id = account_update.id
    RETURNING charge.*
)
SELECT * FROM updated;

-- name: GetBillingUsageCharge :one
SELECT *
FROM billing_usage_charges
WHERE id = sqlc.arg(id)::uuid
  AND chain_id = sqlc.arg(chain_id)::numeric;

-- name: ListUserBillingUsage :many
SELECT *
FROM billing_usage_charges
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

-- name: ListAdminBillingUsage :many
SELECT *
FROM billing_usage_charges
WHERE chain_id = sqlc.arg(chain_id)::numeric
  AND network = sqlc.arg(network)
  AND asset = sqlc.arg(asset)
  AND (
      sqlc.narg(before_created_at)::timestamptz IS NULL
      OR (created_at, id) < (
          sqlc.narg(before_created_at)::timestamptz,
          sqlc.narg(before_id)::uuid
      )
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit);

-- name: AdjustBillingAccount :one
WITH locked AS (
    SELECT *
    FROM billing_accounts
    WHERE user_id = sqlc.arg(user_id)::uuid
      AND chain_id = sqlc.arg(chain_id)::numeric
      AND billing_accounts.network = sqlc.arg(network)
      AND billing_accounts.asset = sqlc.arg(asset)
      AND (
          sqlc.arg(direction) = 'credit'
          OR total_credit_atomic - total_debit_atomic - reserved_atomic
              >= sqlc.arg(amount_atomic)::numeric
      )
    FOR UPDATE
), updated AS (
    UPDATE billing_accounts AS account
    SET total_credit_atomic = account.total_credit_atomic +
            CASE WHEN sqlc.arg(direction) = 'credit'
                THEN sqlc.arg(amount_atomic)::numeric ELSE 0 END,
        total_debit_atomic = account.total_debit_atomic +
            CASE WHEN sqlc.arg(direction) = 'debit'
                THEN sqlc.arg(amount_atomic)::numeric ELSE 0 END,
        updated_at = sqlc.arg(occurred_at)
    FROM locked
    WHERE account.user_id = locked.user_id
      AND account.chain_id = locked.chain_id
      AND account.network = locked.network
      AND account.asset = locked.asset
    RETURNING account.*
), entry AS (
    INSERT INTO billing_account_entries (
        id, user_id, chain_id, network, asset, direction, kind,
        amount_atomic, source_id, reason, occurred_at
    )
    SELECT sqlc.arg(entry_id)::uuid, user_id, chain_id, network, asset,
           sqlc.arg(direction), 'adjustment', sqlc.arg(amount_atomic)::numeric,
           sqlc.arg(source_id)::uuid, sqlc.arg(reason), sqlc.arg(occurred_at)
    FROM updated
)
SELECT * FROM updated;

-- name: ExpireBillingUsageReservations :one
WITH candidates AS (
    SELECT charge.*
    FROM billing_usage_charges AS charge
    WHERE charge.chain_id = sqlc.arg(chain_id)::numeric
      AND charge.network = sqlc.arg(network)
      AND charge.asset = sqlc.arg(asset)
      AND charge.state = 'reserved'
      AND charge.reservation_expires_at <= sqlc.arg(observed_at)
    ORDER BY charge.reservation_expires_at, charge.id
    FOR UPDATE SKIP LOCKED
    LIMIT sqlc.arg(expire_limit)
), totals AS (
    SELECT user_id, chain_id, network, asset, sum(amount_atomic) AS amount_atomic
    FROM candidates
    GROUP BY user_id, chain_id, network, asset
), account_update AS (
    UPDATE billing_accounts AS account
    SET reserved_atomic = account.reserved_atomic - totals.amount_atomic,
        updated_at = sqlc.arg(observed_at)
    FROM totals
    WHERE account.user_id = totals.user_id
      AND account.chain_id = totals.chain_id
      AND account.network = totals.network
      AND account.asset = totals.asset
    RETURNING account.user_id, account.chain_id, account.network, account.asset
), updated AS (
    UPDATE billing_usage_charges AS charge
    SET state = 'expired', failure_code = 'usage_reservation_expired',
        expired_at = sqlc.arg(observed_at), updated_at = sqlc.arg(observed_at)
    FROM candidates, account_update
    WHERE charge.id = candidates.id
      AND account_update.user_id = candidates.user_id
      AND account_update.chain_id = candidates.chain_id
      AND account_update.network = candidates.network
      AND account_update.asset = candidates.asset
    RETURNING charge.id
)
SELECT count(*)::bigint AS expired_count FROM updated;

-- name: ExpireOpenBillingTopupIntents :one
WITH candidates AS (
    SELECT intent.id
    FROM billing_topup_intents AS intent
    WHERE intent.chain_id = sqlc.arg(chain_id)::numeric
      AND intent.network = sqlc.arg(network)
      AND intent.asset = sqlc.arg(asset)
      AND intent.state = 'open'
      AND intent.expires_at <= sqlc.arg(observed_at)
    ORDER BY intent.expires_at, intent.id
    FOR UPDATE SKIP LOCKED
    LIMIT sqlc.arg(expire_limit)
), updated AS (
    UPDATE billing_topup_intents AS intent
    SET state = 'expired', failure_code = 'topup_intent_expired',
        expired_at = sqlc.arg(observed_at), updated_at = sqlc.arg(observed_at)
    FROM candidates
    WHERE intent.id = candidates.id
    RETURNING intent.id
)
SELECT count(*)::bigint AS expired_count FROM updated;

-- name: ExpireBillingTopupPayments :one
WITH candidates AS (
    SELECT payment.id, payment.topup_intent_id, payment.state AS prior_state
    FROM billing_payments AS payment
    JOIN billing_topup_intents AS intent ON intent.id = payment.topup_intent_id
    WHERE payment.chain_id = sqlc.arg(chain_id)::numeric
      AND payment.purpose = 'account_topup'
      AND payment.state IN ('reserved', 'verified')
      AND payment.reservation_expires_at <= sqlc.arg(observed_at)
      AND intent.state = 'processing'
      AND intent.active_payment_id = payment.id
    ORDER BY payment.reservation_expires_at, payment.id
    FOR UPDATE OF payment, intent SKIP LOCKED
    LIMIT sqlc.arg(expire_limit)
), payment_update AS (
    UPDATE billing_payments AS payment
    SET state = 'expired', failure_code = 'reservation_expired',
        expired_at = sqlc.arg(observed_at), updated_at = sqlc.arg(observed_at)
    FROM candidates
    WHERE payment.id = candidates.id
    RETURNING payment.id, payment.topup_intent_id, candidates.prior_state
), intent_update AS (
    UPDATE billing_topup_intents AS intent
    SET state = CASE WHEN intent.expires_at <= sqlc.arg(observed_at)
                     THEN 'expired' ELSE 'open' END,
        active_payment_id = NULL, processing_at = NULL, settling_at = NULL,
        failure_code = CASE WHEN intent.expires_at <= sqlc.arg(observed_at)
                            THEN 'topup_intent_expired' ELSE NULL END,
        expired_at = CASE WHEN intent.expires_at <= sqlc.arg(observed_at)
                          THEN sqlc.arg(observed_at) ELSE NULL END,
        updated_at = sqlc.arg(observed_at)
    FROM payment_update
    WHERE intent.id = payment_update.topup_intent_id
    RETURNING payment_update.id AS payment_id, payment_update.prior_state
), event AS (
    INSERT INTO billing_payment_events (
        payment_id, from_state, to_state, code, actor, occurred_at
    )
    SELECT payment_id, prior_state, 'expired', 'reservation_expired',
           'runtime', sqlc.arg(observed_at)
    FROM intent_update
)
SELECT count(*)::bigint AS expired_count FROM intent_update;
