-- Fresh-database baseline: billing.

-- Functions

CREATE FUNCTION guard_billing_account_entry_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'billing account entries are append-only';
END;
$$;

CREATE FUNCTION guard_billing_payment_event_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'billing payment events are append-only';
END;
$$;

CREATE FUNCTION guard_billing_payment_mutation() RETURNS trigger
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

CREATE FUNCTION guard_billing_topup_intent_mutation() RETURNS trigger
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
        OR NEW.payment_option IS DISTINCT FROM OLD.payment_option
        OR NEW.credit_amount_atomic IS DISTINCT FROM OLD.credit_amount_atomic
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

CREATE FUNCTION guard_billing_usage_charge_mutation() RETURNS trigger
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


-- Tables

CREATE TABLE billing_accounts (
    user_id uuid NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    total_credit_atomic numeric(78,0) DEFAULT 0 NOT NULL,
    total_debit_atomic numeric(78,0) DEFAULT 0 NOT NULL,
    reserved_atomic numeric(78,0) DEFAULT 0 NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT billing_accounts_check CHECK ((total_credit_atomic >= (total_debit_atomic + reserved_atomic))),
    CONSTRAINT billing_accounts_check1 CHECK ((updated_at >= created_at)),
    CONSTRAINT billing_accounts_reserved_atomic_check CHECK ((reserved_atomic >= (0)::numeric)),
    CONSTRAINT billing_accounts_total_credit_atomic_check CHECK ((total_credit_atomic >= (0)::numeric)),
    CONSTRAINT billing_accounts_total_debit_atomic_check CHECK ((total_debit_atomic >= (0)::numeric)),
    CONSTRAINT billing_accounts_pkey PRIMARY KEY (user_id, chain_id),
    CONSTRAINT billing_accounts_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT billing_accounts_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id)
);

CREATE TABLE billing_account_entries (
    id uuid NOT NULL,
    user_id uuid NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    direction text NOT NULL,
    kind text NOT NULL,
    amount_atomic numeric(78,0) NOT NULL,
    source_id uuid NOT NULL,
    reason text,
    occurred_at timestamp with time zone NOT NULL,
    CONSTRAINT billing_account_entries_amount_atomic_check CHECK ((amount_atomic > (0)::numeric)),
    CONSTRAINT billing_account_entries_check CHECK ((((kind = 'adjustment'::text) AND (reason IS NOT NULL) AND ((length(reason) >= 1) AND (length(reason) <= 256))) OR ((kind <> 'adjustment'::text) AND (reason IS NULL)))),
    CONSTRAINT billing_account_entries_direction_check CHECK ((direction = ANY (ARRAY['credit'::text, 'debit'::text]))),
    CONSTRAINT billing_account_entries_kind_check CHECK ((kind = ANY (ARRAY['topup'::text, 'usage'::text, 'adjustment'::text]))),
    CONSTRAINT billing_account_entries_pkey PRIMARY KEY (id),
    CONSTRAINT billing_account_entries_source_id_key UNIQUE (source_id),
    CONSTRAINT billing_account_entries_user_id_chain_id_fkey FOREIGN KEY (user_id, chain_id) REFERENCES billing_accounts(user_id, chain_id)
);

CREATE TABLE billing_topup_intents (
    id uuid NOT NULL,
    user_id uuid NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    network text NOT NULL,
    asset bytea NOT NULL,
    payment_option jsonb NOT NULL,
    credit_amount_atomic numeric(78,0) NOT NULL,
    CONSTRAINT billing_topup_credit_check CHECK (credit_amount_atomic > 0 AND credit_amount_atomic <= 115792089237316195423570985008687907853269984665640564039457584007913129639935),
    CONSTRAINT billing_topup_snapshot_check CHECK (
        jsonb_typeof(payment_option) = 'object'
        AND payment_option ?& ARRAY['id', 'network', 'asset', 'recipient', 'asset_decimals', 'credit_rate_numerator', 'credit_rate_denominator']
        AND (payment_option->>'network' = network) IS TRUE
        AND (lower(payment_option->>'asset') = '0x' || encode(asset, 'hex')) IS TRUE
        AND (lower(payment_option->>'recipient') = '0x' || encode(recipient, 'hex')) IS TRUE
        AND ((payment_option->>'asset_decimals')::integer BETWEEN 0 AND 255) IS TRUE
        AND (payment_option->>'credit_rate_numerator' ~ '^[1-9][0-9]{0,77}$') IS TRUE
        AND (payment_option->>'credit_rate_denominator' ~ '^[1-9][0-9]{0,77}$') IS TRUE
    ),
    CONSTRAINT billing_topup_conversion_check CHECK (amount_atomic * (payment_option->>'credit_rate_numerator')::numeric = credit_amount_atomic * (payment_option->>'credit_rate_denominator')::numeric),
    amount_atomic numeric(78,0) NOT NULL,
    recipient bytea NOT NULL,
    payer bytea NOT NULL,
    state text NOT NULL,
    active_payment_id uuid,
    transaction_hash bytea,
    submission_progress text CHECK (submission_progress IN ('submitting','confirming','unknown')),
    submission_transaction_hash bytea CHECK (octet_length(submission_transaction_hash) = 32 AND submission_transaction_hash <> decode(repeat('00',32),'hex')),
    CONSTRAINT billing_topup_submission_evidence_check CHECK (submission_progress <> 'confirming' OR submission_transaction_hash IS NOT NULL),
    failure_code text,
    expires_at timestamp with time zone NOT NULL,
    processing_at timestamp with time zone,
    settling_at timestamp with time zone,
    credited_at timestamp with time zone,
    failed_at timestamp with time zone,
    expired_at timestamp with time zone,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT billing_topup_intents_amount_atomic_check CHECK (((amount_atomic > (0)::numeric) AND (amount_atomic <= '115792089237316195423570985008687907853269984665640564039457584007913129639935'::numeric))),
    CONSTRAINT billing_topup_intents_asset_check CHECK ((octet_length(asset) = 20)),
    CONSTRAINT billing_topup_intents_check CHECK ((expires_at > created_at)),
    CONSTRAINT billing_topup_intents_check1 CHECK ((updated_at >= created_at)),
    CONSTRAINT billing_topup_intents_check2 CHECK ((((state = 'open'::text) AND (active_payment_id IS NULL) AND (transaction_hash IS NULL) AND (processing_at IS NULL) AND (settling_at IS NULL) AND (credited_at IS NULL) AND (failed_at IS NULL) AND (expired_at IS NULL) AND (failure_code IS NULL)) OR ((state = 'processing'::text) AND (active_payment_id IS NOT NULL) AND (transaction_hash IS NULL) AND (processing_at IS NOT NULL) AND (settling_at IS NULL) AND (credited_at IS NULL) AND (failed_at IS NULL) AND (expired_at IS NULL) AND (failure_code IS NULL)) OR ((state = 'settling'::text) AND (active_payment_id IS NOT NULL) AND (processing_at IS NOT NULL) AND (settling_at IS NOT NULL) AND (credited_at IS NULL) AND (failed_at IS NULL) AND (expired_at IS NULL) AND (((failure_code IS NULL) AND (transaction_hash IS NULL)) OR ((failure_code = 'settlement_unknown'::text) AND (transaction_hash IS NULL)) OR ((failure_code = 'settlement_pending'::text) AND (transaction_hash IS NOT NULL)))) OR ((state = 'credited'::text) AND (active_payment_id IS NOT NULL) AND (transaction_hash IS NOT NULL) AND (processing_at IS NOT NULL) AND (settling_at IS NOT NULL) AND (credited_at IS NOT NULL) AND (failed_at IS NULL) AND (expired_at IS NULL) AND (failure_code IS NULL)) OR ((state = 'failed'::text) AND (active_payment_id IS NOT NULL) AND (failed_at IS NOT NULL) AND (credited_at IS NULL) AND (expired_at IS NULL) AND (failure_code IS NOT NULL)) OR ((state = 'expired'::text) AND (active_payment_id IS NULL) AND (transaction_hash IS NULL) AND (credited_at IS NULL) AND (failed_at IS NULL) AND (expired_at IS NOT NULL) AND (failure_code = 'topup_intent_expired'::text)))),
    CONSTRAINT billing_topup_intents_failure_code_check CHECK (((failure_code IS NULL) OR ((length(failure_code) >= 1) AND (length(failure_code) <= 128)))),
    CONSTRAINT billing_topup_intents_network_check CHECK (((network ~ '^eip155:[1-9][0-9]*$'::text) AND (length(network) <= 96))),
    CONSTRAINT billing_topup_intents_payer_check CHECK ((octet_length(payer) = 20)),
    CONSTRAINT billing_topup_intents_recipient_check CHECK ((octet_length(recipient) = 20)),
    CONSTRAINT billing_topup_intents_state_check CHECK ((state = ANY (ARRAY['open'::text, 'processing'::text, 'settling'::text, 'credited'::text, 'failed'::text, 'expired'::text]))),
    CONSTRAINT billing_topup_intents_transaction_hash_check CHECK (((transaction_hash IS NULL) OR (octet_length(transaction_hash) = 32))),
    CONSTRAINT billing_topup_intents_pkey PRIMARY KEY (id),
    CONSTRAINT billing_topup_intents_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT billing_topup_intents_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id)
);

CREATE TABLE billing_payments (
    id uuid NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    fingerprint bytea NOT NULL,
    reservation_owner uuid NOT NULL,
    method text NOT NULL,
    operation text NOT NULL,
    resource_digest bytea NOT NULL,
    requirement_digest bytea NOT NULL,
    protocol_version smallint NOT NULL,
    scheme text NOT NULL,
    network text NOT NULL,
    asset bytea NOT NULL,
    amount_atomic numeric(78,0) NOT NULL,
    recipient bytea NOT NULL,
    payer bytea,
    user_id uuid,
    api_key_prefix text,
    facilitator_digest bytea NOT NULL,
    transaction_hash bytea,
    state text NOT NULL,
    failure_code text,
    reservation_expires_at timestamp with time zone NOT NULL,
    handler_started_at timestamp with time zone,
    verified_at timestamp with time zone,
    settling_at timestamp with time zone,
    settled_at timestamp with time zone,
    failed_at timestamp with time zone,
    expired_at timestamp with time zone,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    purpose text NOT NULL,
    asset_transfer_method text NOT NULL,
    payment_flow text NOT NULL,
    fingerprint_version smallint NOT NULL,
    topup_intent_id uuid,
    CONSTRAINT billing_payments_amount_atomic_check CHECK (((amount_atomic > (0)::numeric) AND (amount_atomic <= '115792089237316195423570985008687907853269984665640564039457584007913129639935'::numeric))),
    CONSTRAINT billing_payments_asset_check CHECK ((octet_length(asset) = 20)),
    CONSTRAINT billing_payments_check CHECK ((reservation_expires_at > created_at)),
    CONSTRAINT billing_payments_check1 CHECK ((updated_at >= created_at)),
    CONSTRAINT billing_payments_check2 CHECK (((handler_started_at IS NULL) OR (handler_started_at >= created_at))),
    CONSTRAINT billing_payments_check3 CHECK (((verified_at IS NULL) OR (verified_at >= created_at))),
    CONSTRAINT billing_payments_check4 CHECK (((settling_at IS NULL) OR (settling_at >= created_at))),
    CONSTRAINT billing_payments_check5 CHECK (((settled_at IS NULL) OR (settled_at >= created_at))),
    CONSTRAINT billing_payments_check6 CHECK (((failed_at IS NULL) OR (failed_at >= created_at))),
    CONSTRAINT billing_payments_check7 CHECK (((expired_at IS NULL) OR (expired_at >= created_at))),
    CONSTRAINT billing_payments_facilitator_digest_check CHECK ((octet_length(facilitator_digest) = 32)),
    CONSTRAINT billing_payments_failure_code_check CHECK (((failure_code IS NULL) OR ((length(failure_code) >= 1) AND (length(failure_code) <= 128)))),
    CONSTRAINT billing_payments_fingerprint_check CHECK ((octet_length(fingerprint) = 32)),
    CONSTRAINT billing_payments_fingerprint_version_check CHECK ((fingerprint_version = ANY (ARRAY[1, 2]))),
    CONSTRAINT billing_payments_method_v2_check CHECK ((method = ANY (ARRAY['GET'::text, 'POST'::text]))),
    CONSTRAINT billing_payments_network_check CHECK (((network ~ '^eip155:[1-9][0-9]*$'::text) AND (length(network) <= 96))),
    CONSTRAINT billing_payments_operation_check CHECK (((length(operation) >= 1) AND (length(operation) <= 128))),
    CONSTRAINT billing_payments_payer_check CHECK (((payer IS NULL) OR (octet_length(payer) = 20))),
    CONSTRAINT billing_payments_payment_flow_check CHECK ((payment_flow = 'authorization'::text)),
    CONSTRAINT billing_payments_protocol_version_check CHECK ((protocol_version = 2)),
    CONSTRAINT billing_payments_purpose_check CHECK ((purpose = ANY (ARRAY['legacy_request'::text, 'account_topup'::text]))),
    CONSTRAINT billing_payments_recipient_check CHECK ((octet_length(recipient) = 20)),
    CONSTRAINT billing_payments_requirement_digest_check CHECK ((octet_length(requirement_digest) = 32)),
    CONSTRAINT billing_payments_resource_digest_check CHECK ((octet_length(resource_digest) = 32)),
    CONSTRAINT billing_payments_scheme_check CHECK ((scheme = 'exact'::text)),
    CONSTRAINT billing_payments_state_check CHECK ((state = ANY (ARRAY['reserved'::text, 'verified'::text, 'settling'::text, 'settled'::text, 'failed'::text, 'expired'::text]))),
    CONSTRAINT billing_payments_state_facts_v2_check CHECK ((((state = 'reserved'::text) AND (verified_at IS NULL) AND (handler_started_at IS NULL) AND (settling_at IS NULL) AND (settled_at IS NULL) AND (failed_at IS NULL) AND (expired_at IS NULL) AND (transaction_hash IS NULL) AND (failure_code IS NULL)) OR ((state = 'verified'::text) AND (verified_at IS NOT NULL) AND (settling_at IS NULL) AND (settled_at IS NULL) AND (failed_at IS NULL) AND (expired_at IS NULL) AND (transaction_hash IS NULL) AND (failure_code IS NULL)) OR ((state = 'settling'::text) AND (verified_at IS NOT NULL) AND (handler_started_at IS NOT NULL) AND (settling_at IS NOT NULL) AND (settled_at IS NULL) AND (failed_at IS NULL) AND (expired_at IS NULL) AND (((failure_code IS NULL) AND (transaction_hash IS NULL)) OR ((failure_code = 'settlement_unknown'::text) AND (transaction_hash IS NULL)) OR ((failure_code = 'settlement_pending'::text) AND (transaction_hash IS NOT NULL)))) OR ((state = 'settled'::text) AND (verified_at IS NOT NULL) AND (handler_started_at IS NOT NULL) AND (settling_at IS NOT NULL) AND (settled_at IS NOT NULL) AND (failed_at IS NULL) AND (expired_at IS NULL) AND (transaction_hash IS NOT NULL) AND (failure_code IS NULL)) OR ((state = 'failed'::text) AND (settled_at IS NULL) AND (failed_at IS NOT NULL) AND (expired_at IS NULL) AND (failure_code IS NOT NULL)) OR ((state = 'expired'::text) AND (settling_at IS NULL) AND (settled_at IS NULL) AND (failed_at IS NULL) AND (expired_at IS NOT NULL) AND (transaction_hash IS NULL) AND (failure_code = 'reservation_expired'::text)))),
    CONSTRAINT billing_payments_topup_binding_check CHECK ((((purpose = 'legacy_request'::text) AND (topup_intent_id IS NULL)) OR ((purpose = 'account_topup'::text) AND (topup_intent_id IS NOT NULL) AND (method = 'POST'::text)))),
    CONSTRAINT billing_payments_transaction_hash_check CHECK (((transaction_hash IS NULL) OR (octet_length(transaction_hash) = 32))),
    CONSTRAINT billing_payments_transfer_method_check CHECK ((asset_transfer_method = ANY (ARRAY['eip3009'::text, 'permit2'::text]))),
    CONSTRAINT billing_payments_fingerprint_key UNIQUE (fingerprint),
    CONSTRAINT billing_payments_pkey PRIMARY KEY (id),
    CONSTRAINT billing_payments_api_key_prefix_fkey FOREIGN KEY (api_key_prefix) REFERENCES api_keys(prefix) ON DELETE SET NULL,
    CONSTRAINT billing_payments_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT billing_payments_topup_intent_id_fkey FOREIGN KEY (topup_intent_id) REFERENCES billing_topup_intents(id),
    CONSTRAINT billing_payments_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE billing_payment_events (
    id bigint NOT NULL GENERATED ALWAYS AS IDENTITY (
        SEQUENCE NAME billing_payment_events_id_seq
        START WITH 1
        INCREMENT BY 1
        NO MINVALUE
        NO MAXVALUE
        CACHE 1
    ),
    payment_id uuid NOT NULL,
    from_state text,
    to_state text NOT NULL,
    code text NOT NULL,
    actor text NOT NULL,
    transaction_hash bytea,
    occurred_at timestamp with time zone NOT NULL,
    CONSTRAINT billing_payment_events_actor_check CHECK ((actor = ANY (ARRAY['runtime'::text, 'operator'::text]))),
    CONSTRAINT billing_payment_events_code_check CHECK (((length(code) >= 1) AND (length(code) <= 128))),
    CONSTRAINT billing_payment_events_from_state_check CHECK (((from_state IS NULL) OR (from_state = ANY (ARRAY['reserved'::text, 'verified'::text, 'settling'::text, 'settled'::text, 'failed'::text, 'expired'::text])))),
    CONSTRAINT billing_payment_events_to_state_check CHECK ((to_state = ANY (ARRAY['reserved'::text, 'verified'::text, 'settling'::text, 'settled'::text, 'failed'::text, 'expired'::text]))),
    CONSTRAINT billing_payment_events_transaction_hash_check CHECK (((transaction_hash IS NULL) OR (octet_length(transaction_hash) = 32))),
    CONSTRAINT billing_payment_events_pkey PRIMARY KEY (id),
    CONSTRAINT billing_payment_events_payment_id_fkey FOREIGN KEY (payment_id) REFERENCES billing_payments(id)
);

CREATE TABLE billing_usage_charges (
    id uuid NOT NULL,
    reservation_owner uuid NOT NULL,
    user_id uuid NOT NULL,
    api_key_prefix text NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    method text NOT NULL,
    operation text NOT NULL,
    resource_digest bytea NOT NULL,
    amount_atomic numeric(78,0) NOT NULL,
    state text NOT NULL,
    failure_code text,
    response_digest bytea,
    response_bytes bigint,
    reservation_expires_at timestamp with time zone NOT NULL,
    committed_at timestamp with time zone,
    released_at timestamp with time zone,
    expired_at timestamp with time zone,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT billing_usage_charges_amount_atomic_check CHECK ((amount_atomic > (0)::numeric)),
    CONSTRAINT billing_usage_charges_check CHECK ((reservation_expires_at > created_at)),
    CONSTRAINT billing_usage_charges_check1 CHECK ((updated_at >= created_at)),
    CONSTRAINT billing_usage_charges_check2 CHECK ((((state = 'reserved'::text) AND (failure_code IS NULL) AND (response_digest IS NULL) AND (response_bytes IS NULL) AND (committed_at IS NULL) AND (released_at IS NULL) AND (expired_at IS NULL)) OR ((state = 'committed'::text) AND (failure_code IS NULL) AND (response_digest IS NOT NULL) AND (response_bytes IS NOT NULL) AND (committed_at IS NOT NULL) AND (released_at IS NULL) AND (expired_at IS NULL)) OR ((state = 'released'::text) AND (failure_code IS NOT NULL) AND (committed_at IS NULL) AND (released_at IS NOT NULL) AND (expired_at IS NULL)) OR ((state = 'expired'::text) AND (failure_code = 'usage_reservation_expired'::text) AND (committed_at IS NULL) AND (released_at IS NULL) AND (expired_at IS NOT NULL)))),
    CONSTRAINT billing_usage_charges_failure_code_check CHECK (((failure_code IS NULL) OR ((length(failure_code) >= 1) AND (length(failure_code) <= 128)))),
    CONSTRAINT billing_usage_charges_method_check CHECK ((method = ANY (ARRAY['GET'::text, 'POST'::text]))),
    CONSTRAINT billing_usage_charges_operation_check CHECK (((length(operation) >= 1) AND (length(operation) <= 128))),
    CONSTRAINT billing_usage_charges_resource_digest_check CHECK ((octet_length(resource_digest) = 32)),
    CONSTRAINT billing_usage_charges_response_bytes_check CHECK (((response_bytes IS NULL) OR (response_bytes >= 0))),
    CONSTRAINT billing_usage_charges_response_digest_check CHECK (((response_digest IS NULL) OR (octet_length(response_digest) = 32))),
    CONSTRAINT billing_usage_charges_state_check CHECK ((state = ANY (ARRAY['reserved'::text, 'committed'::text, 'released'::text, 'expired'::text]))),
    CONSTRAINT billing_usage_charges_pkey PRIMARY KEY (id),
    CONSTRAINT billing_usage_charges_api_key_prefix_fkey FOREIGN KEY (api_key_prefix) REFERENCES api_keys(prefix),
    CONSTRAINT billing_usage_charges_user_id_chain_id_fkey FOREIGN KEY (user_id, chain_id) REFERENCES billing_accounts(user_id, chain_id)
);


-- Dependency completion

-- Circular FK: top-up intents precede payments, which reference the intent.
ALTER TABLE ONLY billing_topup_intents
    ADD CONSTRAINT billing_topup_intents_active_payment_fk FOREIGN KEY (active_payment_id) REFERENCES billing_payments(id);


-- Indexes

CREATE INDEX billing_account_entries_user_time_idx ON billing_account_entries USING btree (user_id, occurred_at DESC, id DESC);

CREATE INDEX billing_accounts_chain_updated_idx ON billing_accounts USING btree (chain_id, updated_at DESC, user_id);

CREATE INDEX billing_payment_events_payment_idx ON billing_payment_events USING btree (payment_id, id);

CREATE INDEX billing_payments_active_expiry_idx ON billing_payments USING btree (reservation_expires_at, id) WHERE (state = ANY (ARRAY['reserved'::text, 'verified'::text]));

CREATE INDEX billing_payments_admin_filter_idx ON billing_payments USING btree (chain_id, state, operation, network, asset, created_at DESC, id DESC);

CREATE INDEX billing_payments_chain_created_idx ON billing_payments USING btree (chain_id, created_at DESC, id DESC);

CREATE INDEX billing_payments_settling_metrics_idx ON billing_payments USING btree (chain_id, operation, failure_code, settling_at) WHERE (state = 'settling'::text);

CREATE UNIQUE INDEX billing_payments_topup_settled_idx ON billing_payments USING btree (topup_intent_id) WHERE ((purpose = 'account_topup'::text) AND (state = 'settled'::text));

CREATE INDEX billing_payments_user_created_idx ON billing_payments USING btree (user_id, created_at DESC, id DESC) WHERE (user_id IS NOT NULL);

CREATE UNIQUE INDEX billing_topup_intents_active_payment_idx ON billing_topup_intents USING btree (active_payment_id) WHERE (active_payment_id IS NOT NULL);

CREATE INDEX billing_topup_intents_expiry_idx ON billing_topup_intents USING btree (expires_at, id) WHERE (state = 'open'::text);

CREATE INDEX billing_topup_intents_user_created_idx ON billing_topup_intents USING btree (user_id, created_at DESC, id DESC);

CREATE INDEX billing_usage_charges_expiry_idx ON billing_usage_charges USING btree (reservation_expires_at, id) WHERE (state = 'reserved'::text);

CREATE INDEX billing_usage_charges_operation_idx ON billing_usage_charges USING btree (chain_id, operation, state, created_at DESC);

CREATE INDEX billing_usage_charges_user_created_idx ON billing_usage_charges USING btree (user_id, created_at DESC, id DESC);


-- Triggers

CREATE TRIGGER billing_account_entries_guard BEFORE DELETE OR UPDATE ON billing_account_entries FOR EACH ROW EXECUTE FUNCTION guard_billing_account_entry_mutation();

CREATE TRIGGER billing_payment_events_guard BEFORE DELETE OR UPDATE ON billing_payment_events FOR EACH ROW EXECUTE FUNCTION guard_billing_payment_event_mutation();

CREATE TRIGGER billing_payments_guard BEFORE DELETE OR UPDATE ON billing_payments FOR EACH ROW EXECUTE FUNCTION guard_billing_payment_mutation();

CREATE TRIGGER billing_topup_intents_guard BEFORE DELETE OR UPDATE ON billing_topup_intents FOR EACH ROW EXECUTE FUNCTION guard_billing_topup_intent_mutation();

CREATE TRIGGER billing_usage_charges_guard BEFORE DELETE OR UPDATE ON billing_usage_charges FOR EACH ROW EXECUTE FUNCTION guard_billing_usage_charge_mutation();

-- Standalone facilitator journal; never used as an account credit ledger.
CREATE TABLE facilitator_settlements (
    authorization_id text PRIMARY KEY,
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    request_body jsonb NOT NULL,
    network text NOT NULL,
    signer_address bytea NOT NULL CHECK (octet_length(signer_address) = 20),
    nonce numeric(20,0),
    raw_transaction bytea,
    transaction_hash bytea CHECK (octet_length(transaction_hash) = 32),
    state text NOT NULL DEFAULT 'reserved' CHECK (state IN ('reserved','signed','succeeded','failed')),
    broadcast_status text NOT NULL DEFAULT 'unsubmitted' CHECK (broadcast_status IN ('unsubmitted','unknown','confirmed')),
    CHECK (broadcast_status = 'unsubmitted' OR transaction_hash IS NOT NULL),
    response jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (network, signer_address, nonce),
    CHECK (nonce IS NULL OR nonce >= 0),
    CHECK (state NOT IN ('signed','succeeded') OR transaction_hash IS NOT NULL),
    CHECK (state <> 'reserved' OR transaction_hash IS NULL),
    CHECK (raw_transaction IS NULL OR octet_length(raw_transaction) BETWEEN 1 AND 1048576),
    CHECK ((raw_transaction IS NULL AND transaction_hash IS NULL AND nonce IS NULL) OR
           (raw_transaction IS NOT NULL AND transaction_hash IS NOT NULL AND nonce IS NOT NULL))
);
CREATE INDEX facilitator_pending_by_network
    ON facilitator_settlements (network, created_at)
    WHERE state IN ('reserved','signed');
