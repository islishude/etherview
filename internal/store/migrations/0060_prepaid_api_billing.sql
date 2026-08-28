-- P73 prepaid API billing and x402 account top-ups.
CREATE TABLE billing_accounts (
    user_id UUID NOT NULL REFERENCES users(id),
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    network TEXT NOT NULL,
    asset BYTEA NOT NULL,
    total_credit_atomic NUMERIC(78, 0) NOT NULL DEFAULT 0,
    total_debit_atomic NUMERIC(78, 0) NOT NULL DEFAULT 0,
    reserved_atomic NUMERIC(78, 0) NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, chain_id, network, asset),
    CHECK (network ~ '^eip155:[1-9][0-9]*$' AND length(network) <= 96),
    CHECK (octet_length(asset) = 20),
    CHECK (total_credit_atomic >= 0),
    CHECK (total_debit_atomic >= 0),
    CHECK (reserved_atomic >= 0),
    CHECK (total_credit_atomic >= total_debit_atomic + reserved_atomic),
    CHECK (updated_at >= created_at)
);

CREATE INDEX billing_accounts_chain_updated_idx
    ON billing_accounts (chain_id, updated_at DESC, user_id);

CREATE TABLE billing_topup_intents (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id),
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    network TEXT NOT NULL,
    asset BYTEA NOT NULL,
    amount_atomic NUMERIC(78, 0) NOT NULL,
    recipient BYTEA NOT NULL,
    payer BYTEA NOT NULL,
    state TEXT NOT NULL,
    active_payment_id UUID,
    transaction_hash BYTEA,
    failure_code TEXT,
    expires_at TIMESTAMPTZ NOT NULL,
    processing_at TIMESTAMPTZ,
    settling_at TIMESTAMPTZ,
    credited_at TIMESTAMPTZ,
    failed_at TIMESTAMPTZ,
    expired_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK (network ~ '^eip155:[1-9][0-9]*$' AND length(network) <= 96),
    CHECK (octet_length(asset) = 20),
    CHECK (octet_length(recipient) = 20),
    CHECK (octet_length(payer) = 20),
    CHECK (transaction_hash IS NULL OR octet_length(transaction_hash) = 32),
    CHECK (
        amount_atomic > 0
        AND amount_atomic <= 115792089237316195423570985008687907853269984665640564039457584007913129639935
    ),
    CHECK (state IN ('open', 'processing', 'settling', 'credited', 'failed', 'expired')),
    CHECK (failure_code IS NULL OR length(failure_code) BETWEEN 1 AND 128),
    CHECK (expires_at > created_at),
    CHECK (updated_at >= created_at),
    CHECK (
        (state = 'open'
            AND active_payment_id IS NULL
            AND transaction_hash IS NULL
            AND processing_at IS NULL
            AND settling_at IS NULL
            AND credited_at IS NULL
            AND failed_at IS NULL
            AND expired_at IS NULL
            AND failure_code IS NULL)
        OR
        (state = 'processing'
            AND active_payment_id IS NOT NULL
            AND transaction_hash IS NULL
            AND processing_at IS NOT NULL
            AND settling_at IS NULL
            AND credited_at IS NULL
            AND failed_at IS NULL
            AND expired_at IS NULL
            AND failure_code IS NULL)
        OR
        (state = 'settling'
            AND active_payment_id IS NOT NULL
            AND processing_at IS NOT NULL
            AND settling_at IS NOT NULL
            AND credited_at IS NULL
            AND failed_at IS NULL
            AND expired_at IS NULL
            AND (
                (failure_code IS NULL AND transaction_hash IS NULL)
                OR (failure_code = 'settlement_unknown' AND transaction_hash IS NULL)
                OR (failure_code = 'settlement_pending' AND transaction_hash IS NOT NULL)
            ))
        OR
        (state = 'credited'
            AND active_payment_id IS NOT NULL
            AND transaction_hash IS NOT NULL
            AND processing_at IS NOT NULL
            AND settling_at IS NOT NULL
            AND credited_at IS NOT NULL
            AND failed_at IS NULL
            AND expired_at IS NULL
            AND failure_code IS NULL)
        OR
        (state = 'failed'
            AND active_payment_id IS NOT NULL
            AND failed_at IS NOT NULL
            AND credited_at IS NULL
            AND expired_at IS NULL
            AND failure_code IS NOT NULL)
        OR
        (state = 'expired'
            AND active_payment_id IS NULL
            AND transaction_hash IS NULL
            AND credited_at IS NULL
            AND failed_at IS NULL
            AND expired_at IS NOT NULL
            AND failure_code = 'topup_intent_expired')
    )
);

CREATE INDEX billing_topup_intents_user_created_idx
    ON billing_topup_intents (user_id, created_at DESC, id DESC);

CREATE INDEX billing_topup_intents_expiry_idx
    ON billing_topup_intents (expires_at, id)
    WHERE state = 'open';

ALTER TABLE billing_payments
    ADD COLUMN purpose TEXT NOT NULL DEFAULT 'legacy_request',
    ADD COLUMN asset_transfer_method TEXT NOT NULL DEFAULT 'eip3009',
    ADD COLUMN payment_flow TEXT NOT NULL DEFAULT 'authorization',
    ADD COLUMN fingerprint_version SMALLINT NOT NULL DEFAULT 1,
    ADD COLUMN topup_intent_id UUID REFERENCES billing_topup_intents(id);

ALTER TABLE billing_payments
    ALTER COLUMN purpose DROP DEFAULT,
    ALTER COLUMN asset_transfer_method DROP DEFAULT,
    ALTER COLUMN payment_flow DROP DEFAULT,
    ALTER COLUMN fingerprint_version DROP DEFAULT,
    DROP CONSTRAINT IF EXISTS billing_payments_method_check,
    DROP CONSTRAINT IF EXISTS billing_payments_check8,
    DROP CONSTRAINT IF EXISTS billing_payments_state_check1,
    ADD CONSTRAINT billing_payments_method_v2_check
        CHECK (method IN ('GET', 'POST')),
    ADD CONSTRAINT billing_payments_purpose_check
        CHECK (purpose IN ('legacy_request', 'account_topup')),
    ADD CONSTRAINT billing_payments_transfer_method_check
        CHECK (asset_transfer_method IN ('eip3009', 'permit2')),
    ADD CONSTRAINT billing_payments_payment_flow_check
        CHECK (payment_flow = 'authorization'),
    ADD CONSTRAINT billing_payments_fingerprint_version_check
        CHECK (fingerprint_version IN (1, 2)),
    ADD CONSTRAINT billing_payments_topup_binding_check CHECK (
        (purpose = 'legacy_request' AND topup_intent_id IS NULL)
        OR
        (purpose = 'account_topup' AND topup_intent_id IS NOT NULL AND method = 'POST')
    ),
    ADD CONSTRAINT billing_payments_state_facts_v2_check CHECK (
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
            AND (
                (failure_code IS NULL AND transaction_hash IS NULL)
                OR (failure_code = 'settlement_unknown' AND transaction_hash IS NULL)
                OR (failure_code = 'settlement_pending' AND transaction_hash IS NOT NULL)
            ))
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
            AND failure_code IS NOT NULL)
        OR
        (state = 'expired'
            AND settling_at IS NULL
            AND settled_at IS NULL
            AND failed_at IS NULL
            AND expired_at IS NOT NULL
            AND transaction_hash IS NULL
            AND failure_code = 'reservation_expired')
    );

ALTER TABLE billing_topup_intents
    ADD CONSTRAINT billing_topup_intents_active_payment_fk
        FOREIGN KEY (active_payment_id) REFERENCES billing_payments(id);

CREATE UNIQUE INDEX billing_topup_intents_active_payment_idx
    ON billing_topup_intents (active_payment_id)
    WHERE active_payment_id IS NOT NULL;

CREATE UNIQUE INDEX billing_payments_topup_settled_idx
    ON billing_payments (topup_intent_id)
    WHERE purpose = 'account_topup' AND state = 'settled';

CREATE TABLE billing_account_entries (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    chain_id NUMERIC(78, 0) NOT NULL,
    network TEXT NOT NULL,
    asset BYTEA NOT NULL,
    direction TEXT NOT NULL,
    kind TEXT NOT NULL,
    amount_atomic NUMERIC(78, 0) NOT NULL,
    source_id UUID NOT NULL,
    reason TEXT,
    occurred_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (user_id, chain_id, network, asset)
        REFERENCES billing_accounts(user_id, chain_id, network, asset),
    UNIQUE (source_id),
    CHECK (direction IN ('credit', 'debit')),
    CHECK (kind IN ('topup', 'usage', 'adjustment')),
    CHECK (amount_atomic > 0),
    CHECK (
        (kind = 'adjustment' AND reason IS NOT NULL
            AND length(reason) BETWEEN 1 AND 256)
        OR (kind <> 'adjustment' AND reason IS NULL)
    )
);

CREATE INDEX billing_account_entries_user_time_idx
    ON billing_account_entries (user_id, occurred_at DESC, id DESC);

CREATE TABLE billing_usage_charges (
    id UUID PRIMARY KEY,
    reservation_owner UUID NOT NULL,
    user_id UUID NOT NULL,
    api_key_prefix TEXT NOT NULL REFERENCES api_keys(prefix),
    chain_id NUMERIC(78, 0) NOT NULL,
    network TEXT NOT NULL,
    asset BYTEA NOT NULL,
    method TEXT NOT NULL,
    operation TEXT NOT NULL,
    resource_digest BYTEA NOT NULL,
    amount_atomic NUMERIC(78, 0) NOT NULL,
    state TEXT NOT NULL,
    failure_code TEXT,
    response_digest BYTEA,
    response_bytes BIGINT,
    reservation_expires_at TIMESTAMPTZ NOT NULL,
    committed_at TIMESTAMPTZ,
    released_at TIMESTAMPTZ,
    expired_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (user_id, chain_id, network, asset)
        REFERENCES billing_accounts(user_id, chain_id, network, asset),
    CHECK (method IN ('GET', 'POST')),
    CHECK (length(operation) BETWEEN 1 AND 128),
    CHECK (octet_length(resource_digest) = 32),
    CHECK (amount_atomic > 0),
    CHECK (state IN ('reserved', 'committed', 'released', 'expired')),
    CHECK (failure_code IS NULL OR length(failure_code) BETWEEN 1 AND 128),
    CHECK (response_digest IS NULL OR octet_length(response_digest) = 32),
    CHECK (response_bytes IS NULL OR response_bytes >= 0),
    CHECK (reservation_expires_at > created_at),
    CHECK (updated_at >= created_at),
    CHECK (
        (state = 'reserved'
            AND failure_code IS NULL
            AND response_digest IS NULL
            AND response_bytes IS NULL
            AND committed_at IS NULL
            AND released_at IS NULL
            AND expired_at IS NULL)
        OR
        (state = 'committed'
            AND failure_code IS NULL
            AND response_digest IS NOT NULL
            AND response_bytes IS NOT NULL
            AND committed_at IS NOT NULL
            AND released_at IS NULL
            AND expired_at IS NULL)
        OR
        (state = 'released'
            AND failure_code IS NOT NULL
            AND committed_at IS NULL
            AND released_at IS NOT NULL
            AND expired_at IS NULL)
        OR
        (state = 'expired'
            AND failure_code = 'usage_reservation_expired'
            AND committed_at IS NULL
            AND released_at IS NULL
            AND expired_at IS NOT NULL)
    )
);

CREATE INDEX billing_usage_charges_user_created_idx
    ON billing_usage_charges (user_id, created_at DESC, id DESC);

CREATE INDEX billing_usage_charges_expiry_idx
    ON billing_usage_charges (reservation_expires_at, id)
    WHERE state = 'reserved';

CREATE INDEX billing_usage_charges_operation_idx
    ON billing_usage_charges (chain_id, operation, state, created_at DESC);

CREATE FUNCTION guard_billing_account_entry_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'billing account entries are append-only';
END;
$$;

CREATE TRIGGER billing_account_entries_guard
BEFORE UPDATE OR DELETE ON billing_account_entries
FOR EACH ROW
EXECUTE FUNCTION guard_billing_account_entry_mutation();

CREATE FUNCTION guard_billing_usage_charge_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'billing usage charges are durable audit records';
    END IF;
    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.reservation_owner IS DISTINCT FROM OLD.reservation_owner
        OR NEW.user_id IS DISTINCT FROM OLD.user_id
        OR NEW.api_key_prefix IS DISTINCT FROM OLD.api_key_prefix
        OR NEW.chain_id IS DISTINCT FROM OLD.chain_id
        OR NEW.network IS DISTINCT FROM OLD.network
        OR NEW.asset IS DISTINCT FROM OLD.asset
        OR NEW.method IS DISTINCT FROM OLD.method
        OR NEW.operation IS DISTINCT FROM OLD.operation
        OR NEW.resource_digest IS DISTINCT FROM OLD.resource_digest
        OR NEW.amount_atomic IS DISTINCT FROM OLD.amount_atomic
        OR NEW.reservation_expires_at IS DISTINCT FROM OLD.reservation_expires_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
    THEN
        RAISE EXCEPTION 'billing usage charge identity is immutable';
    END IF;
    IF OLD.state <> 'reserved' OR NEW.state NOT IN ('committed', 'released', 'expired') THEN
        RAISE EXCEPTION 'invalid billing usage charge transition';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER billing_usage_charges_guard
BEFORE UPDATE OR DELETE ON billing_usage_charges
FOR EACH ROW
EXECUTE FUNCTION guard_billing_usage_charge_mutation();

CREATE FUNCTION guard_billing_topup_intent_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'billing top-up intents are durable audit records';
    END IF;
    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.user_id IS DISTINCT FROM OLD.user_id
        OR NEW.chain_id IS DISTINCT FROM OLD.chain_id
        OR NEW.network IS DISTINCT FROM OLD.network
        OR NEW.asset IS DISTINCT FROM OLD.asset
        OR NEW.amount_atomic IS DISTINCT FROM OLD.amount_atomic
        OR NEW.recipient IS DISTINCT FROM OLD.recipient
        OR NEW.payer IS DISTINCT FROM OLD.payer
        OR NEW.expires_at IS DISTINCT FROM OLD.expires_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
    THEN
        RAISE EXCEPTION 'billing top-up intent identity is immutable';
    END IF;
    IF OLD.transaction_hash IS NOT NULL
        AND NEW.transaction_hash IS DISTINCT FROM OLD.transaction_hash
    THEN
        RAISE EXCEPTION 'billing top-up settlement hash is immutable';
    END IF;
    IF NEW.updated_at < OLD.updated_at THEN
        RAISE EXCEPTION 'billing top-up intent time cannot move backward';
    END IF;
    IF OLD.state IN ('credited', 'failed', 'expired') THEN
        RAISE EXCEPTION 'terminal billing top-up intents are immutable';
    END IF;
    IF NOT (
        (OLD.state = 'open' AND NEW.state IN ('processing', 'expired'))
        OR (OLD.state = 'processing' AND NEW.state IN ('open', 'settling', 'expired'))
        OR (OLD.state = 'settling' AND NEW.state IN ('settling', 'open', 'credited', 'failed', 'expired'))
    ) THEN
        RAISE EXCEPTION 'invalid billing top-up intent transition';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER billing_topup_intents_guard
BEFORE UPDATE OR DELETE ON billing_topup_intents
FOR EACH ROW
EXECUTE FUNCTION guard_billing_topup_intent_mutation();

CREATE OR REPLACE FUNCTION guard_billing_payment_mutation()
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
        OR NEW.purpose IS DISTINCT FROM OLD.purpose
        OR NEW.asset_transfer_method IS DISTINCT FROM OLD.asset_transfer_method
        OR NEW.payment_flow IS DISTINCT FROM OLD.payment_flow
        OR NEW.fingerprint_version IS DISTINCT FROM OLD.fingerprint_version
        OR NEW.topup_intent_id IS DISTINCT FROM OLD.topup_intent_id
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR NEW.reservation_expires_at IS DISTINCT FROM OLD.reservation_expires_at
    THEN
        RAISE EXCEPTION 'billing payment identity is immutable';
    END IF;

    IF OLD.payer IS NOT NULL AND NEW.payer IS DISTINCT FROM OLD.payer
        OR OLD.user_id IS NOT NULL AND NEW.user_id IS DISTINCT FROM OLD.user_id
        OR OLD.api_key_prefix IS NOT NULL AND NEW.api_key_prefix IS DISTINCT FROM OLD.api_key_prefix
        OR OLD.handler_started_at IS NOT NULL AND NEW.handler_started_at IS DISTINCT FROM OLD.handler_started_at
        OR OLD.verified_at IS NOT NULL AND NEW.verified_at IS DISTINCT FROM OLD.verified_at
        OR OLD.settling_at IS NOT NULL AND NEW.settling_at IS DISTINCT FROM OLD.settling_at
        OR OLD.transaction_hash IS NOT NULL AND NEW.transaction_hash IS DISTINCT FROM OLD.transaction_hash
    THEN
        RAISE EXCEPTION 'billing payment bound facts are immutable';
    END IF;

    IF OLD.state IN ('settled', 'failed', 'expired') THEN
        RAISE EXCEPTION 'terminal billing payments are immutable';
    END IF;
    IF NOT (
        (OLD.state = 'reserved' AND NEW.state IN ('reserved', 'verified', 'failed', 'expired'))
        OR (OLD.state = 'verified' AND NEW.state IN ('verified', 'settling', 'failed', 'expired'))
        OR (OLD.state = 'settling' AND NEW.state IN ('settling', 'settled', 'failed'))
    ) THEN
        RAISE EXCEPTION 'invalid billing payment state transition';
    END IF;
    RETURN NEW;
END;
$$;
