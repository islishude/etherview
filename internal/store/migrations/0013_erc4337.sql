-- Fresh-database baseline: erc4337.

-- Functions

CREATE FUNCTION enforce_erc4337_canonical_user_op_hash() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.canonical THEN
        PERFORM pg_advisory_xact_lock(hashtextextended(
            'etherview:erc4337-userop:' || NEW.chain_id::text || ':' ||
            encode(NEW.configuration_digest, 'hex') || ':' ||
            encode(NEW.user_op_hash, 'hex'), 0
        ));
        IF EXISTS (
            SELECT 1
            FROM erc4337_user_operations AS existing
            WHERE existing.chain_id = NEW.chain_id
              AND existing.configuration_digest = NEW.configuration_digest
              AND existing.user_op_hash = NEW.user_op_hash
              AND existing.canonical
              AND (
                  existing.block_number, existing.block_hash,
                  existing.transaction_hash, existing.operation_index
              ) IS DISTINCT FROM (
                  NEW.block_number, NEW.block_hash,
                  NEW.transaction_hash, NEW.operation_index
              )
        ) THEN
            RAISE EXCEPTION 'canonical userOpHash already belongs to another inclusion';
        END IF;
    END IF;
    RETURN NEW;
END
$$;

CREATE FUNCTION erc4337_add_covered_block(target_chain_id numeric, target_configuration_digest bytea, target_block_number numeric, target_block_hash bytea, target_durable_job_id bigint, target_job_generation bigint) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE
    left_range erc4337_coverage_ranges%ROWTYPE;
    right_range erc4337_coverage_ranges%ROWTYPE;
BEGIN
    IF octet_length(target_block_hash) <> 32 OR
       target_durable_job_id <= 0 OR target_job_generation <= 0 THEN
        RAISE EXCEPTION 'ERC-4337 coverage insertion target is invalid';
    END IF;
    PERFORM erc4337_remove_covered_block(
        target_chain_id, target_configuration_digest, target_block_number
    );
    INSERT INTO erc4337_covered_blocks (
        chain_id, configuration_digest, block_number, block_hash,
        durable_job_id, job_generation
    ) VALUES (
        target_chain_id, target_configuration_digest, target_block_number,
        target_block_hash, target_durable_job_id, target_job_generation
    );

    SELECT * INTO left_range
    FROM erc4337_coverage_ranges
    WHERE chain_id = target_chain_id
      AND configuration_digest = target_configuration_digest
      AND end_block = target_block_number - 1;
    SELECT * INTO right_range
    FROM erc4337_coverage_ranges
    WHERE chain_id = target_chain_id
      AND configuration_digest = target_configuration_digest
      AND start_block = target_block_number + 1;

    IF left_range.start_block IS NOT NULL AND right_range.start_block IS NOT NULL THEN
        DELETE FROM erc4337_coverage_ranges
        WHERE chain_id = target_chain_id
          AND configuration_digest = target_configuration_digest
          AND start_block = right_range.start_block;
        UPDATE erc4337_coverage_ranges
        SET end_block = right_range.end_block,
            end_block_hash = right_range.end_block_hash,
            updated_at = clock_timestamp()
        WHERE chain_id = target_chain_id
          AND configuration_digest = target_configuration_digest
          AND start_block = left_range.start_block;
    ELSIF left_range.start_block IS NOT NULL THEN
        UPDATE erc4337_coverage_ranges
        SET end_block = target_block_number,
            end_block_hash = target_block_hash,
            updated_at = clock_timestamp()
        WHERE chain_id = target_chain_id
          AND configuration_digest = target_configuration_digest
          AND start_block = left_range.start_block;
    ELSIF right_range.start_block IS NOT NULL THEN
        DELETE FROM erc4337_coverage_ranges
        WHERE chain_id = target_chain_id
          AND configuration_digest = target_configuration_digest
          AND start_block = right_range.start_block;
        INSERT INTO erc4337_coverage_ranges (
            chain_id, configuration_digest, start_block, start_block_hash,
            end_block, end_block_hash
        ) VALUES (
            target_chain_id, target_configuration_digest,
            target_block_number, target_block_hash,
            right_range.end_block, right_range.end_block_hash
        );
    ELSE
        INSERT INTO erc4337_coverage_ranges (
            chain_id, configuration_digest, start_block, start_block_hash,
            end_block, end_block_hash
        ) VALUES (
            target_chain_id, target_configuration_digest,
            target_block_number, target_block_hash,
            target_block_number, target_block_hash
        );
    END IF;
END
$$;

CREATE FUNCTION erc4337_remove_block_coverage(target_chain_id numeric, target_block_number numeric, target_block_hash bytea) RETURNS integer
    LANGUAGE plpgsql
    AS $$
DECLARE
    digest_row RECORD;
    removed INTEGER := 0;
BEGIN
    FOR digest_row IN
        SELECT configuration_digest
        FROM erc4337_covered_blocks
        WHERE chain_id = target_chain_id
          AND block_number = target_block_number
          AND block_hash = target_block_hash
    LOOP
        PERFORM erc4337_remove_covered_block(
            target_chain_id, digest_row.configuration_digest, target_block_number
        );
        removed := removed + 1;
    END LOOP;
    RETURN removed;
END
$$;

CREATE FUNCTION erc4337_remove_covered_block(target_chain_id numeric, target_configuration_digest bytea, target_block_number numeric) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE
    containing erc4337_coverage_ranges%ROWTYPE;
    next_hash BYTEA;
    previous_hash BYTEA;
BEGIN
    IF target_chain_id IS NULL OR target_configuration_digest IS NULL OR
       octet_length(target_configuration_digest) <> 32 OR
       target_block_number IS NULL OR target_block_number < 0 THEN
        RAISE EXCEPTION 'ERC-4337 coverage removal target is invalid';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended(
        'etherview:erc4337-coverage:' || target_chain_id::text || ':' ||
        encode(target_configuration_digest, 'hex'), 0
    ));
    SELECT * INTO containing
    FROM erc4337_coverage_ranges
    WHERE chain_id = target_chain_id
      AND configuration_digest = target_configuration_digest
      AND start_block <= target_block_number
      AND end_block >= target_block_number
    ORDER BY start_block DESC
    LIMIT 1;

    DELETE FROM erc4337_covered_blocks
    WHERE chain_id = target_chain_id
      AND configuration_digest = target_configuration_digest
      AND block_number = target_block_number;
    IF containing.start_block IS NULL THEN
        RETURN;
    END IF;

    IF containing.start_block = target_block_number AND containing.end_block = target_block_number THEN
        DELETE FROM erc4337_coverage_ranges
        WHERE chain_id = target_chain_id
          AND configuration_digest = target_configuration_digest
          AND start_block = containing.start_block;
    ELSIF containing.start_block = target_block_number THEN
        SELECT block_hash INTO next_hash
        FROM erc4337_covered_blocks
        WHERE chain_id = target_chain_id
          AND configuration_digest = target_configuration_digest
          AND block_number = target_block_number + 1;
        DELETE FROM erc4337_coverage_ranges
        WHERE chain_id = target_chain_id
          AND configuration_digest = target_configuration_digest
          AND start_block = containing.start_block;
        INSERT INTO erc4337_coverage_ranges (
            chain_id, configuration_digest, start_block, start_block_hash,
            end_block, end_block_hash
        ) VALUES (
            target_chain_id, target_configuration_digest,
            target_block_number + 1, next_hash,
            containing.end_block, containing.end_block_hash
        );
    ELSIF containing.end_block = target_block_number THEN
        SELECT block_hash INTO previous_hash
        FROM erc4337_covered_blocks
        WHERE chain_id = target_chain_id
          AND configuration_digest = target_configuration_digest
          AND block_number = target_block_number - 1;
        UPDATE erc4337_coverage_ranges
        SET end_block = target_block_number - 1,
            end_block_hash = previous_hash,
            updated_at = clock_timestamp()
        WHERE chain_id = target_chain_id
          AND configuration_digest = target_configuration_digest
          AND start_block = containing.start_block;
    ELSE
        SELECT block_hash INTO previous_hash
        FROM erc4337_covered_blocks
        WHERE chain_id = target_chain_id
          AND configuration_digest = target_configuration_digest
          AND block_number = target_block_number - 1;
        SELECT block_hash INTO next_hash
        FROM erc4337_covered_blocks
        WHERE chain_id = target_chain_id
          AND configuration_digest = target_configuration_digest
          AND block_number = target_block_number + 1;
        UPDATE erc4337_coverage_ranges
        SET end_block = target_block_number - 1,
            end_block_hash = previous_hash,
            updated_at = clock_timestamp()
        WHERE chain_id = target_chain_id
          AND configuration_digest = target_configuration_digest
          AND start_block = containing.start_block;
        INSERT INTO erc4337_coverage_ranges (
            chain_id, configuration_digest, start_block, start_block_hash,
            end_block, end_block_hash
        ) VALUES (
            target_chain_id, target_configuration_digest,
            target_block_number + 1, next_hash,
            containing.end_block, containing.end_block_hash
        );
    END IF;
END
$$;

CREATE FUNCTION erc4337_remove_detached_coverage() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    digest_row RECORD;
BEGIN
    IF TG_OP = 'DELETE' OR NEW.block_hash IS DISTINCT FROM OLD.block_hash THEN
        FOR digest_row IN
            SELECT configuration_digest
            FROM erc4337_covered_blocks
            WHERE chain_id = OLD.chain_id
              AND block_number = OLD.number
              AND block_hash = OLD.block_hash
        LOOP
            PERFORM erc4337_remove_covered_block(
                OLD.chain_id, digest_row.configuration_digest, OLD.number
            );
        END LOOP;
    END IF;
    RETURN NULL;
END
$$;


-- Tables

CREATE TABLE erc4337_coverage_ranges (
    chain_id numeric(78,0) NOT NULL,
    configuration_digest bytea NOT NULL,
    start_block numeric(78,0) NOT NULL,
    start_block_hash bytea NOT NULL,
    end_block numeric(78,0) NOT NULL,
    end_block_hash bytea NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT erc4337_coverage_ranges_check CHECK (((start_block >= (0)::numeric) AND (end_block >= start_block))),
    CONSTRAINT erc4337_coverage_ranges_configuration_digest_check CHECK ((octet_length(configuration_digest) = 32)),
    CONSTRAINT erc4337_coverage_ranges_end_block_hash_check CHECK ((octet_length(end_block_hash) = 32)),
    CONSTRAINT erc4337_coverage_ranges_start_block_hash_check CHECK ((octet_length(start_block_hash) = 32)),
    CONSTRAINT erc4337_coverage_ranges_chain_id_configuration_digest_end_b_key UNIQUE (chain_id, configuration_digest, end_block),
    CONSTRAINT erc4337_coverage_ranges_pkey PRIMARY KEY (chain_id, configuration_digest, start_block),
    CONSTRAINT erc4337_coverage_ranges_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE erc4337_covered_blocks (
    chain_id numeric(78,0) NOT NULL,
    configuration_digest bytea NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    durable_job_id bigint NOT NULL,
    job_generation bigint NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT erc4337_covered_blocks_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc4337_covered_blocks_check CHECK (((block_number >= (0)::numeric) AND (durable_job_id > 0) AND (job_generation > 0))),
    CONSTRAINT erc4337_covered_blocks_configuration_digest_check CHECK ((octet_length(configuration_digest) = 32)),
    CONSTRAINT erc4337_covered_blocks_pkey PRIMARY KEY (chain_id, configuration_digest, block_number),
    CONSTRAINT erc4337_covered_blocks_chain_id_block_number_block_hash_fkey FOREIGN KEY (chain_id, block_number, block_hash) REFERENCES blocks(chain_id, number, hash),
    CONSTRAINT erc4337_covered_blocks_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE erc4337_user_operations (
    chain_id numeric(78,0) NOT NULL,
    configuration_digest bytea NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    transaction_hash bytea NOT NULL,
    transaction_index bigint NOT NULL,
    operation_index bigint NOT NULL,
    event_log_index bigint NOT NULL,
    user_op_hash bytea NOT NULL,
    entry_point bytea NOT NULL,
    entry_point_version text NOT NULL,
    sender bytea NOT NULL,
    nonce numeric(78,0) NOT NULL,
    nonce_key numeric(78,0) NOT NULL,
    nonce_sequence numeric(78,0) NOT NULL,
    bundler bytea NOT NULL,
    beneficiary bytea NOT NULL,
    init_kind text NOT NULL,
    factory bytea,
    paymaster bytea,
    aggregator bytea,
    success boolean NOT NULL,
    actual_gas_cost numeric(78,0) NOT NULL,
    actual_gas_used numeric(78,0) NOT NULL,
    call_gas_limit numeric(78,0) NOT NULL,
    verification_gas_limit numeric(78,0) NOT NULL,
    pre_verification_gas numeric(78,0) NOT NULL,
    max_fee_per_gas numeric(78,0) NOT NULL,
    max_priority_fee_per_gas numeric(78,0) NOT NULL,
    paymaster_verification_gas_limit numeric(78,0),
    paymaster_post_op_gas_limit numeric(78,0),
    init_code bytea NOT NULL,
    factory_data bytea NOT NULL,
    call_data bytea NOT NULL,
    paymaster_and_data bytea NOT NULL,
    paymaster_data bytea NOT NULL,
    paymaster_signature bytea NOT NULL,
    signature bytea NOT NULL,
    account_gas_limits bytea,
    gas_fees bytea,
    aggregated_signature bytea NOT NULL,
    canonical boolean NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT erc4337_user_operations_account_gas_limits_check CHECK (((account_gas_limits IS NULL) OR (octet_length(account_gas_limits) = 32))),
    CONSTRAINT erc4337_user_operations_aggregator_check CHECK (((aggregator IS NULL) OR (octet_length(aggregator) = 20))),
    CONSTRAINT erc4337_user_operations_beneficiary_check CHECK ((octet_length(beneficiary) = 20)),
    CONSTRAINT erc4337_user_operations_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc4337_user_operations_bundler_check CHECK ((octet_length(bundler) = 20)),
    CONSTRAINT erc4337_user_operations_check CHECK (((transaction_index >= 0) AND (operation_index >= 0) AND (event_log_index >= 0))),
    CONSTRAINT erc4337_user_operations_check1 CHECK (((nonce >= (0)::numeric) AND (nonce_key >= (0)::numeric) AND (nonce_sequence >= (0)::numeric))),
    CONSTRAINT erc4337_user_operations_check2 CHECK (((actual_gas_cost >= (0)::numeric) AND (actual_gas_used >= (0)::numeric))),
    CONSTRAINT erc4337_user_operations_check3 CHECK (((call_gas_limit >= (0)::numeric) AND (verification_gas_limit >= (0)::numeric))),
    CONSTRAINT erc4337_user_operations_check4 CHECK (((pre_verification_gas >= (0)::numeric) AND (max_fee_per_gas >= (0)::numeric) AND (max_priority_fee_per_gas >= (0)::numeric))),
    CONSTRAINT erc4337_user_operations_configuration_digest_check CHECK ((octet_length(configuration_digest) = 32)),
    CONSTRAINT erc4337_user_operations_entry_point_check CHECK ((octet_length(entry_point) = 20)),
    CONSTRAINT erc4337_user_operations_entry_point_version_check CHECK ((entry_point_version = ANY (ARRAY['0.6'::text, '0.7'::text, '0.8'::text, '0.9'::text]))),
    CONSTRAINT erc4337_user_operations_factory_check CHECK (((factory IS NULL) OR (octet_length(factory) = 20))),
    CONSTRAINT erc4337_user_operations_gas_fees_check CHECK (((gas_fees IS NULL) OR (octet_length(gas_fees) = 32))),
    CONSTRAINT erc4337_user_operations_init_kind_check CHECK ((init_kind = ANY (ARRAY['none'::text, 'factory'::text, 'eip7702'::text]))),
    CONSTRAINT erc4337_user_operations_paymaster_check CHECK (((paymaster IS NULL) OR (octet_length(paymaster) = 20))),
    CONSTRAINT erc4337_user_operations_paymaster_post_op_gas_limit_check CHECK (((paymaster_post_op_gas_limit IS NULL) OR (paymaster_post_op_gas_limit >= (0)::numeric))),
    CONSTRAINT erc4337_user_operations_paymaster_verification_gas_limit_check CHECK (((paymaster_verification_gas_limit IS NULL) OR (paymaster_verification_gas_limit >= (0)::numeric))),
    CONSTRAINT erc4337_user_operations_sender_check CHECK ((octet_length(sender) = 20)),
    CONSTRAINT erc4337_user_operations_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT erc4337_user_operations_user_op_hash_check CHECK ((octet_length(user_op_hash) = 32)),
    CONSTRAINT erc4337_user_operations_pkey PRIMARY KEY (chain_id, configuration_digest, block_number, block_hash, transaction_hash, operation_index),
    CONSTRAINT erc4337_user_operations_chain_id_block_number_block_hash_t_fkey FOREIGN KEY (chain_id, block_number, block_hash, transaction_index) REFERENCES transaction_inclusions(chain_id, block_number, block_hash, tx_index),
    CONSTRAINT erc4337_user_operations_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
)
PARTITION BY RANGE (block_number);

CREATE TABLE erc4337_user_operations_default PARTITION OF erc4337_user_operations DEFAULT;

CREATE TABLE erc4337_user_operations_p_0_1000000 PARTITION OF erc4337_user_operations FOR VALUES FROM ('0') TO ('1000000');

CREATE TABLE erc4337_user_operation_events (
    chain_id numeric(78,0) NOT NULL,
    configuration_digest bytea NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    transaction_hash bytea NOT NULL,
    operation_index bigint NOT NULL,
    log_index bigint NOT NULL,
    event_kind text NOT NULL,
    sender bytea NOT NULL,
    nonce numeric(78,0),
    related_address bytea,
    paymaster bytea,
    raw_data bytea NOT NULL,
    reason text,
    panic_code numeric(78,0),
    canonical boolean NOT NULL,
    CONSTRAINT erc4337_user_operation_events_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc4337_user_operation_events_check CHECK (((operation_index >= 0) AND (log_index >= 0))),
    CONSTRAINT erc4337_user_operation_events_configuration_digest_check CHECK ((octet_length(configuration_digest) = 32)),
    CONSTRAINT erc4337_user_operation_events_event_kind_check CHECK ((event_kind = ANY (ARRAY['account_deployed'::text, 'ignored_init_code'::text, 'eip7702_initialized'::text, 'execution_revert'::text, 'post_op_revert'::text, 'prefund_too_low'::text]))),
    CONSTRAINT erc4337_user_operation_events_nonce_check CHECK (((nonce IS NULL) OR (nonce >= (0)::numeric))),
    CONSTRAINT erc4337_user_operation_events_panic_code_check CHECK (((panic_code IS NULL) OR (panic_code >= (0)::numeric))),
    CONSTRAINT erc4337_user_operation_events_paymaster_check CHECK (((paymaster IS NULL) OR (octet_length(paymaster) = 20))),
    CONSTRAINT erc4337_user_operation_events_related_address_check CHECK (((related_address IS NULL) OR (octet_length(related_address) = 20))),
    CONSTRAINT erc4337_user_operation_events_sender_check CHECK ((octet_length(sender) = 20)),
    CONSTRAINT erc4337_user_operation_events_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT erc4337_user_operation_events_pkey PRIMARY KEY (chain_id, configuration_digest, block_number, block_hash, transaction_hash, operation_index, log_index),
    CONSTRAINT erc4337_user_operation_events_chain_id_configuration_diges_fkey FOREIGN KEY (chain_id, configuration_digest, block_number, block_hash, transaction_hash, operation_index) REFERENCES erc4337_user_operations(chain_id, configuration_digest, block_number, block_hash, transaction_hash, operation_index)
)
PARTITION BY RANGE (block_number);

CREATE TABLE erc4337_user_operation_events_default PARTITION OF erc4337_user_operation_events DEFAULT;

CREATE TABLE erc4337_user_operation_events_p_0_1000000 PARTITION OF erc4337_user_operation_events FOR VALUES FROM ('0') TO ('1000000');

CREATE TABLE erc4337_user_operation_participants (
    chain_id numeric(78,0) NOT NULL,
    configuration_digest bytea CONSTRAINT erc4337_user_operation_participan_configuration_digest_not_null NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    transaction_hash bytea NOT NULL,
    operation_index bigint NOT NULL,
    address bytea NOT NULL,
    role text NOT NULL,
    canonical boolean NOT NULL,
    CONSTRAINT erc4337_user_operation_participants_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT erc4337_user_operation_participants_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT erc4337_user_operation_participants_configuration_digest_check CHECK ((octet_length(configuration_digest) = 32)),
    CONSTRAINT erc4337_user_operation_participants_operation_index_check CHECK ((operation_index >= 0)),
    CONSTRAINT erc4337_user_operation_participants_role_check CHECK ((role = ANY (ARRAY['sender'::text, 'entry_point'::text, 'bundler'::text, 'beneficiary'::text, 'factory'::text, 'paymaster'::text, 'aggregator'::text, 'eip7702_delegate'::text]))),
    CONSTRAINT erc4337_user_operation_participants_transaction_hash_check CHECK ((octet_length(transaction_hash) = 32)),
    CONSTRAINT erc4337_user_operation_participants_pkey PRIMARY KEY (chain_id, configuration_digest, block_number, block_hash, transaction_hash, operation_index, address, role),
    CONSTRAINT erc4337_user_operation_partic_chain_id_configuration_diges_fkey FOREIGN KEY (chain_id, configuration_digest, block_number, block_hash, transaction_hash, operation_index) REFERENCES erc4337_user_operations(chain_id, configuration_digest, block_number, block_hash, transaction_hash, operation_index)
)
PARTITION BY RANGE (block_number);

CREATE TABLE erc4337_user_operation_participants_default PARTITION OF erc4337_user_operation_participants DEFAULT;

CREATE TABLE erc4337_user_operation_participants_p_0_1000000 PARTITION OF erc4337_user_operation_participants FOR VALUES FROM ('0') TO ('1000000');


-- Indexes

CREATE INDEX erc4337_user_operation_partic_chain_id_configuration_diges_idx1 ON erc4337_user_operation_participants_default USING btree (chain_id, configuration_digest, address, block_number DESC, operation_index DESC) WHERE canonical;

CREATE INDEX erc4337_user_operation_partic_chain_id_configuration_digest_idx ON erc4337_user_operation_participants_p_0_1000000 USING btree (chain_id, configuration_digest, address, block_number DESC, operation_index DESC) WHERE canonical;

CREATE INDEX erc4337_user_operations_defau_chain_id_configuration_diges_idx1 ON erc4337_user_operations_default USING btree (chain_id, configuration_digest, block_number DESC, transaction_index DESC, operation_index DESC, user_op_hash DESC) WHERE canonical;

CREATE INDEX erc4337_user_operations_defau_chain_id_configuration_diges_idx2 ON erc4337_user_operations_default USING btree (chain_id, configuration_digest, transaction_hash, operation_index) WHERE canonical;

CREATE INDEX erc4337_user_operations_defau_chain_id_configuration_digest_idx ON erc4337_user_operations_default USING btree (chain_id, configuration_digest, user_op_hash) WHERE canonical;

CREATE INDEX erc4337_user_operations_p_0_1_chain_id_configuration_diges_idx1 ON erc4337_user_operations_p_0_1000000 USING btree (chain_id, configuration_digest, block_number DESC, transaction_index DESC, operation_index DESC, user_op_hash DESC) WHERE canonical;

CREATE INDEX erc4337_user_operations_p_0_1_chain_id_configuration_diges_idx2 ON erc4337_user_operations_p_0_1000000 USING btree (chain_id, configuration_digest, transaction_hash, operation_index) WHERE canonical;

CREATE INDEX erc4337_user_operations_p_0_1_chain_id_configuration_digest_idx ON erc4337_user_operations_p_0_1000000 USING btree (chain_id, configuration_digest, user_op_hash) WHERE canonical;

CREATE INDEX erc4337_coverage_ranges_end_idx ON erc4337_coverage_ranges USING btree (chain_id, configuration_digest, end_block, start_block);

CREATE INDEX erc4337_user_operation_participants_address_idx ON erc4337_user_operation_participants USING btree (chain_id, configuration_digest, address, block_number DESC, operation_index DESC) WHERE canonical;

CREATE INDEX erc4337_user_operations_canonical_hash_idx ON erc4337_user_operations USING btree (chain_id, configuration_digest, user_op_hash) WHERE canonical;

CREATE INDEX erc4337_user_operations_global_idx ON erc4337_user_operations USING btree (chain_id, configuration_digest, block_number DESC, transaction_index DESC, operation_index DESC, user_op_hash DESC) WHERE canonical;

CREATE INDEX erc4337_user_operations_transaction_idx ON erc4337_user_operations USING btree (chain_id, configuration_digest, transaction_hash, operation_index) WHERE canonical;


-- Triggers

CREATE TRIGGER erc4337_canonical_user_op_hash_trigger BEFORE INSERT OR UPDATE OF canonical ON erc4337_user_operations FOR EACH ROW EXECUTE FUNCTION enforce_erc4337_canonical_user_op_hash();

CREATE TRIGGER erc4337_coverage_canonical_detach_trigger AFTER DELETE OR UPDATE ON canonical_blocks FOR EACH ROW EXECUTE FUNCTION erc4337_remove_detached_coverage();
