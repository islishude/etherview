-- name: RetryBillingTopupVerification :one
UPDATE billing_payments AS payment
SET verification_owner=sqlc.arg(owner)::uuid,
    verification_expires_at=sqlc.arg(expires_at)::timestamptz,
    verification_retryable=false,updated_at=sqlc.arg(observed_at)::timestamptz
FROM billing_topup_intents AS intent
WHERE payment.fingerprint=sqlc.arg(fingerprint)
  AND payment.authorization_id=sqlc.arg(authorization_id)
  AND payment.request_digest=sqlc.arg(request_digest)
  AND payment.state='reserved' AND payment.settling_at IS NULL
  AND (payment.verification_retryable OR COALESCE(payment.verification_expires_at,payment.reservation_expires_at)<=sqlc.arg(observed_at)::timestamptz)
  AND intent.id=payment.topup_intent_id AND intent.active_payment_id=payment.id
  AND intent.state='processing' AND intent.expires_at>sqlc.arg(observed_at)::timestamptz
RETURNING payment.*;

-- name: ReleaseBillingTopupVerification :execrows
UPDATE billing_payments AS payment
SET verification_retryable=true, verification_expires_at=intent.expires_at, updated_at=sqlc.arg(observed_at)::timestamptz
FROM billing_topup_intents AS intent
WHERE payment.id=sqlc.arg(payment_id)::uuid
  AND COALESCE(payment.verification_owner,payment.reservation_owner)=sqlc.arg(owner)::uuid
  AND payment.state='reserved' AND payment.settling_at IS NULL
  AND intent.id=payment.topup_intent_id AND intent.active_payment_id=payment.id
  AND intent.state='processing';

-- name: ClaimBillingTopupResults :many
WITH candidates AS (
 SELECT payment.id FROM billing_payments AS payment
 WHERE payment.chain_id=sqlc.arg(chain_id)::numeric AND payment.state='settling'
   AND payment.authorization_id IS NOT NULL
   AND (payment.reconciliation_expires_at IS NULL OR payment.reconciliation_expires_at<=now())
   AND (payment.failure_code IS NOT NULL OR payment.settling_at<=now()-interval '2 minutes')
 ORDER BY payment.updated_at,payment.id LIMIT 32 FOR UPDATE SKIP LOCKED
)
UPDATE billing_payments AS payment
SET reconciliation_owner=sqlc.arg(owner)::uuid,reconciliation_expires_at=now()+interval '2 minutes'
FROM candidates WHERE payment.id=candidates.id RETURNING payment.*;

-- name: ClaimBillingTopupResult :one
UPDATE billing_payments AS payment
SET reconciliation_owner=sqlc.arg(owner)::uuid,reconciliation_expires_at=now()+interval '2 minutes'
WHERE payment.id=sqlc.arg(payment_id)::uuid AND payment.state='settling'
  AND COALESCE(payment.verification_owner,payment.reservation_owner)=sqlc.arg(attempt_owner)::uuid
  AND (payment.reconciliation_expires_at IS NULL OR payment.reconciliation_expires_at<=now())
RETURNING payment.*;

-- name: ReleaseBillingTopupResult :execrows
UPDATE billing_payments SET reconciliation_expires_at=now()+interval '2 seconds'
WHERE id=sqlc.arg(payment_id)::uuid AND reconciliation_owner=sqlc.arg(owner)::uuid AND state='settling';

-- name: CreditBillingTopupResult :one
WITH candidate AS (
    SELECT payment.id, payment.topup_intent_id, payment.user_id,
           payment.chain_id,
           intent.credit_amount_atomic AS amount_atomic
    FROM billing_payments AS payment
    JOIN billing_topup_intents AS intent ON intent.id = payment.topup_intent_id
    WHERE payment.id = sqlc.arg('payment_id')::uuid
      AND payment.reconciliation_owner = sqlc.arg('reservation_owner')::uuid
      AND payment.reconciliation_expires_at>now()
      AND payment.authorization_id=sqlc.arg('authorization_id')
      AND payment.request_digest=sqlc.arg('request_digest')
      AND payment.facilitator_digest=sqlc.arg('facilitator_digest')
      AND payment.network=sqlc.arg('network')
      AND payment.asset=sqlc.arg('asset')
      AND payment.payer=sqlc.arg('payer')
      AND payment.recipient=sqlc.arg('recipient')
      AND payment.amount_atomic=sqlc.arg('amount_atomic')::numeric
      AND (payment.transaction_hash IS NULL OR payment.transaction_hash=sqlc.narg('original_transaction'))
      AND payment.purpose = 'account_topup'
      AND payment.state = 'settling'
      AND payment.user_id IS NOT NULL
      AND intent.state = 'settling'
      AND intent.active_payment_id = payment.id
      AND (intent.submission_transaction_hash IS NULL OR intent.submission_transaction_hash = sqlc.narg('original_transaction'))
      AND intent.user_id = payment.user_id
    FOR UPDATE OF payment, intent
), payment_update AS (
    UPDATE billing_payments AS payment
    SET state = 'settled', transaction_hash = COALESCE(payment.transaction_hash,sqlc.narg('original_transaction')),
        settlement_transaction_hash=sqlc.arg('transaction_hash'),settlement_evidence=sqlc.arg('evidence'),failure_code=NULL,
        settled_at = sqlc.arg('transitioned_at'), updated_at = sqlc.arg('transitioned_at')
    FROM candidate
    WHERE payment.id = candidate.id
    RETURNING candidate.*
), intent_update AS (
    UPDATE billing_topup_intents AS intent
    SET state = 'credited', failure_code = NULL,
        transaction_hash = COALESCE(intent.transaction_hash,sqlc.narg('original_transaction')),
        settlement_transaction_hash = sqlc.arg('transaction_hash'),
        credited_at = sqlc.arg('transitioned_at'), updated_at = sqlc.arg('transitioned_at')
    FROM payment_update
    WHERE intent.id = payment_update.topup_intent_id
    RETURNING payment_update.*
), account_update AS (
    UPDATE billing_accounts AS account
    SET total_credit_atomic = account.total_credit_atomic + intent_update.amount_atomic,
        updated_at = sqlc.arg('transitioned_at')
    FROM intent_update
    WHERE account.user_id = intent_update.user_id
      AND account.chain_id = intent_update.chain_id
    RETURNING account.*, intent_update.id AS payment_id,
              intent_update.amount_atomic AS topup_amount_atomic
), entry AS (
    INSERT INTO billing_account_entries (
        id, user_id, chain_id, direction, kind,
        amount_atomic, source_id, occurred_at
    )
    SELECT sqlc.arg('entry_id')::uuid, user_id, chain_id,
           'credit', 'topup', topup_amount_atomic, payment_id, sqlc.arg('transitioned_at')
    FROM account_update
), event AS (
    INSERT INTO billing_payment_events (
        payment_id, from_state, to_state, code, actor, transaction_hash, occurred_at
    )
    SELECT payment_id, 'settling', 'settled', 'payment_settled', 'runtime',
           sqlc.arg('transaction_hash'), sqlc.arg('transitioned_at')
    FROM account_update
)
SELECT user_id, chain_id, total_credit_atomic,
       total_debit_atomic, reserved_atomic, created_at, updated_at
FROM account_update;


-- name: FailBillingTopupResult :one
WITH candidate AS (
    SELECT payment.id, payment.topup_intent_id, payment.failure_code,
           payment.transaction_hash
    FROM billing_payments AS payment
    JOIN billing_topup_intents AS intent ON intent.id = payment.topup_intent_id
    WHERE payment.id = sqlc.arg('payment_id')::uuid
      AND payment.reconciliation_owner=sqlc.arg('owner')::uuid
      AND payment.reconciliation_expires_at>now()
      AND payment.state='settling'
      AND payment.authorization_id=sqlc.arg('authorization_id')
      AND payment.request_digest=sqlc.arg('request_digest')
      AND (payment.transaction_hash IS NULL OR payment.transaction_hash=sqlc.narg('original_transaction'))
      AND intent.state = 'settling'
      AND intent.active_payment_id = payment.id
    FOR UPDATE OF payment, intent
), payment_update AS (
    UPDATE billing_payments AS payment
    SET state = 'failed', failure_code = 'settlement_rejected',
        transaction_hash = COALESCE(payment.transaction_hash,sqlc.narg('original_transaction')),
        failed_at = sqlc.arg('transitioned_at'), updated_at = sqlc.arg('transitioned_at')
    FROM candidate
    WHERE payment.id = candidate.id
    RETURNING payment.id, payment.topup_intent_id, payment.transaction_hash,
              candidate.failure_code AS prior_failure_code
), intent_update AS (
    UPDATE billing_topup_intents AS intent
    SET state = 'failed', failure_code = 'settlement_rejected',
        transaction_hash = COALESCE(intent.transaction_hash,sqlc.narg('original_transaction')),
        failed_at = sqlc.arg('transitioned_at'), updated_at = sqlc.arg('transitioned_at')
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
                ELSE 'settlement_rejected' END,
           'runtime', transaction_hash, sqlc.arg('transitioned_at')
    FROM intent_update
)
SELECT id FROM intent_update;
