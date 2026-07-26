-- P66 durable x402 billing ledger.
CREATE TABLE billing_payments (
    id UUID PRIMARY KEY,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    fingerprint BYTEA NOT NULL UNIQUE,
    reservation_owner UUID NOT NULL,
    method TEXT NOT NULL,
    operation TEXT NOT NULL,
    resource_digest BYTEA NOT NULL,
    requirement_digest BYTEA NOT NULL,
    protocol_version SMALLINT NOT NULL,
    scheme TEXT NOT NULL,
    network TEXT NOT NULL,
    asset BYTEA NOT NULL,
    amount_atomic NUMERIC(78, 0) NOT NULL,
    recipient BYTEA NOT NULL,
    payer BYTEA,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    api_key_prefix TEXT REFERENCES api_keys(prefix) ON DELETE SET NULL,
    facilitator_digest BYTEA NOT NULL,
    transaction_hash BYTEA,
    state TEXT NOT NULL,
    failure_code TEXT,
    reservation_expires_at TIMESTAMPTZ NOT NULL,
    handler_started_at TIMESTAMPTZ,
    verified_at TIMESTAMPTZ,
    settling_at TIMESTAMPTZ,
    settled_at TIMESTAMPTZ,
    failed_at TIMESTAMPTZ,
    expired_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK (octet_length(fingerprint) = 32),
    CHECK (method = 'GET'),
    CHECK (length(operation) BETWEEN 1 AND 128),
    CHECK (octet_length(resource_digest) = 32),
    CHECK (octet_length(requirement_digest) = 32),
    CHECK (protocol_version = 2),
    CHECK (scheme = 'exact'),
    CHECK (network ~ '^eip155:[1-9][0-9]*$' AND length(network) <= 96),
    CHECK (octet_length(asset) = 20),
    CHECK (
        amount_atomic > 0
        AND amount_atomic <= 115792089237316195423570985008687907853269984665640564039457584007913129639935
    ),
    CHECK (octet_length(recipient) = 20),
    CHECK (payer IS NULL OR octet_length(payer) = 20),
    CHECK (octet_length(facilitator_digest) = 32),
    CHECK (transaction_hash IS NULL OR octet_length(transaction_hash) = 32),
    CHECK (failure_code IS NULL OR length(failure_code) BETWEEN 1 AND 128),
    CHECK (reservation_expires_at > created_at),
    CHECK (updated_at >= created_at),
    CHECK (handler_started_at IS NULL OR handler_started_at >= created_at),
    CHECK (verified_at IS NULL OR verified_at >= created_at),
    CHECK (settling_at IS NULL OR settling_at >= created_at),
    CHECK (settled_at IS NULL OR settled_at >= created_at),
    CHECK (failed_at IS NULL OR failed_at >= created_at),
    CHECK (expired_at IS NULL OR expired_at >= created_at),
    CHECK (state IN (
        'reserved', 'verified', 'settling', 'settled', 'failed', 'expired'
    )),
    CHECK (
        (state = 'reserved'
            AND verified_at IS NULL
            AND handler_started_at IS NULL
            AND settling_at IS NULL
            AND settled_at IS NULL
            AND failed_at IS NULL
            AND expired_at IS NULL
            AND transaction_hash IS NULL
            AND failure_code IS NULL)
        OR
        (state = 'verified'
            AND verified_at IS NOT NULL
            AND settling_at IS NULL
            AND settled_at IS NULL
            AND failed_at IS NULL
            AND expired_at IS NULL
            AND transaction_hash IS NULL
            AND failure_code IS NULL)
        OR
        (state = 'settling'
            AND verified_at IS NOT NULL
            AND handler_started_at IS NOT NULL
            AND settling_at IS NOT NULL
            AND settled_at IS NULL
            AND failed_at IS NULL
            AND expired_at IS NULL
            AND transaction_hash IS NULL
            AND (failure_code IS NULL OR failure_code = 'settlement_unknown'))
        OR
        (state = 'settled'
            AND verified_at IS NOT NULL
            AND handler_started_at IS NOT NULL
            AND settling_at IS NOT NULL
            AND settled_at IS NOT NULL
            AND failed_at IS NULL
            AND expired_at IS NULL
            AND transaction_hash IS NOT NULL
            AND failure_code IS NULL)
        OR
        (state = 'failed'
            AND settled_at IS NULL
            AND failed_at IS NOT NULL
            AND expired_at IS NULL
            AND transaction_hash IS NULL
            AND failure_code IS NOT NULL)
        OR
        (state = 'expired'
            AND settling_at IS NULL
            AND settled_at IS NULL
            AND failed_at IS NULL
            AND expired_at IS NOT NULL
            AND transaction_hash IS NULL
            AND failure_code = 'reservation_expired')
    )
);

CREATE INDEX billing_payments_chain_created_idx
    ON billing_payments (chain_id, created_at DESC, id DESC);

CREATE INDEX billing_payments_user_created_idx
    ON billing_payments (user_id, created_at DESC, id DESC)
    WHERE user_id IS NOT NULL;

CREATE INDEX billing_payments_active_expiry_idx
    ON billing_payments (reservation_expires_at, id)
    WHERE state IN ('reserved', 'verified');

CREATE INDEX billing_payments_admin_filter_idx
    ON billing_payments (
        chain_id, state, operation, network, asset, created_at DESC, id DESC
    );

CREATE INDEX billing_payments_settling_metrics_idx
    ON billing_payments (
        chain_id, operation, failure_code, settling_at
    )
    WHERE state = 'settling';

CREATE TABLE billing_payment_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    payment_id UUID NOT NULL REFERENCES billing_payments(id),
    from_state TEXT,
    to_state TEXT NOT NULL,
    code TEXT NOT NULL,
    actor TEXT NOT NULL,
    transaction_hash BYTEA,
    occurred_at TIMESTAMPTZ NOT NULL,
    CHECK (from_state IS NULL OR from_state IN (
        'reserved', 'verified', 'settling', 'settled', 'failed', 'expired'
    )),
    CHECK (to_state IN (
        'reserved', 'verified', 'settling', 'settled', 'failed', 'expired'
    )),
    CHECK (length(code) BETWEEN 1 AND 128),
    CHECK (actor IN ('runtime', 'operator')),
    CHECK (transaction_hash IS NULL OR octet_length(transaction_hash) = 32)
);

CREATE INDEX billing_payment_events_payment_idx
    ON billing_payment_events (payment_id, id);

CREATE FUNCTION guard_billing_payment_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'billing payments are durable audit records';
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.chain_id IS DISTINCT FROM OLD.chain_id
        OR NEW.fingerprint IS DISTINCT FROM OLD.fingerprint
        OR NEW.reservation_owner IS DISTINCT FROM OLD.reservation_owner
        OR NEW.method IS DISTINCT FROM OLD.method
        OR NEW.operation IS DISTINCT FROM OLD.operation
        OR NEW.resource_digest IS DISTINCT FROM OLD.resource_digest
        OR NEW.requirement_digest IS DISTINCT FROM OLD.requirement_digest
        OR NEW.protocol_version IS DISTINCT FROM OLD.protocol_version
        OR NEW.scheme IS DISTINCT FROM OLD.scheme
        OR NEW.network IS DISTINCT FROM OLD.network
        OR NEW.asset IS DISTINCT FROM OLD.asset
        OR NEW.amount_atomic IS DISTINCT FROM OLD.amount_atomic
        OR NEW.recipient IS DISTINCT FROM OLD.recipient
        OR NEW.facilitator_digest IS DISTINCT FROM OLD.facilitator_digest
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR NEW.reservation_expires_at IS DISTINCT FROM OLD.reservation_expires_at
    THEN
        RAISE EXCEPTION 'billing payment identity is immutable';
    END IF;

    IF OLD.payer IS NOT NULL AND NEW.payer IS DISTINCT FROM OLD.payer
        OR OLD.user_id IS NOT NULL AND NEW.user_id IS DISTINCT FROM OLD.user_id
        OR OLD.api_key_prefix IS NOT NULL
            AND NEW.api_key_prefix IS DISTINCT FROM OLD.api_key_prefix
        OR OLD.handler_started_at IS NOT NULL
            AND NEW.handler_started_at IS DISTINCT FROM OLD.handler_started_at
        OR OLD.verified_at IS NOT NULL
            AND NEW.verified_at IS DISTINCT FROM OLD.verified_at
        OR OLD.settling_at IS NOT NULL
            AND NEW.settling_at IS DISTINCT FROM OLD.settling_at
    THEN
        RAISE EXCEPTION 'billing payment bound facts are immutable';
    END IF;

    IF OLD.state = 'settled' THEN
        RAISE EXCEPTION 'settled billing payments are immutable';
    END IF;
    IF OLD.state IN ('failed', 'expired') THEN
        RAISE EXCEPTION 'terminal billing payments are immutable';
    END IF;
    IF NOT (
        (OLD.state = 'reserved' AND NEW.state IN (
            'reserved', 'verified', 'failed', 'expired'
        ))
        OR
        (OLD.state = 'verified' AND NEW.state IN (
            'verified', 'settling', 'failed', 'expired'
        ))
        OR
        (OLD.state = 'settling' AND NEW.state IN (
            'settling', 'settled', 'failed'
        ))
    ) THEN
        RAISE EXCEPTION 'invalid billing payment state transition';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER billing_payments_guard
BEFORE UPDATE OR DELETE ON billing_payments
FOR EACH ROW
EXECUTE FUNCTION guard_billing_payment_mutation();

CREATE FUNCTION guard_billing_payment_event_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'billing payment events are append-only';
END;
$$;

CREATE TRIGGER billing_payment_events_guard
BEFORE UPDATE OR DELETE ON billing_payment_events
FOR EACH ROW
EXECUTE FUNCTION guard_billing_payment_event_mutation();
