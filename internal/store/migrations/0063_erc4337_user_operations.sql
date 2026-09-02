CREATE TABLE erc4337_user_operations (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    configuration_digest BYTEA NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    transaction_hash BYTEA NOT NULL,
    transaction_index BIGINT NOT NULL,
    operation_index BIGINT NOT NULL,
    event_log_index BIGINT NOT NULL,
    user_op_hash BYTEA NOT NULL,
    entry_point BYTEA NOT NULL,
    entry_point_version TEXT NOT NULL,
    sender BYTEA NOT NULL,
    nonce NUMERIC(78, 0) NOT NULL,
    nonce_key NUMERIC(78, 0) NOT NULL,
    nonce_sequence NUMERIC(78, 0) NOT NULL,
    bundler BYTEA NOT NULL,
    beneficiary BYTEA NOT NULL,
    init_kind TEXT NOT NULL,
    factory BYTEA,
    paymaster BYTEA,
    aggregator BYTEA,
    success BOOLEAN NOT NULL,
    actual_gas_cost NUMERIC(78, 0) NOT NULL,
    actual_gas_used NUMERIC(78, 0) NOT NULL,
    call_gas_limit NUMERIC(78, 0) NOT NULL,
    verification_gas_limit NUMERIC(78, 0) NOT NULL,
    pre_verification_gas NUMERIC(78, 0) NOT NULL,
    max_fee_per_gas NUMERIC(78, 0) NOT NULL,
    max_priority_fee_per_gas NUMERIC(78, 0) NOT NULL,
    paymaster_verification_gas_limit NUMERIC(78, 0),
    paymaster_post_op_gas_limit NUMERIC(78, 0),
    init_code BYTEA NOT NULL,
    factory_data BYTEA NOT NULL,
    call_data BYTEA NOT NULL,
    paymaster_and_data BYTEA NOT NULL,
    paymaster_data BYTEA NOT NULL,
    paymaster_signature BYTEA NOT NULL,
    signature BYTEA NOT NULL,
    account_gas_limits BYTEA,
    gas_fees BYTEA,
    aggregated_signature BYTEA NOT NULL,
    canonical BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (
        chain_id, configuration_digest, block_number, block_hash,
        transaction_hash, operation_index
    ),
    FOREIGN KEY (chain_id, block_number, block_hash, transaction_index)
        REFERENCES transaction_inclusions(chain_id, block_number, block_hash, tx_index),
    CHECK (octet_length(configuration_digest) = 32),
    CHECK (octet_length(block_hash) = 32),
    CHECK (octet_length(transaction_hash) = 32),
    CHECK (octet_length(user_op_hash) = 32),
    CHECK (octet_length(entry_point) = 20),
    CHECK (octet_length(sender) = 20),
    CHECK (octet_length(bundler) = 20),
    CHECK (octet_length(beneficiary) = 20),
    CHECK (factory IS NULL OR octet_length(factory) = 20),
    CHECK (paymaster IS NULL OR octet_length(paymaster) = 20),
    CHECK (aggregator IS NULL OR octet_length(aggregator) = 20),
    CHECK (entry_point_version IN ('0.6', '0.7', '0.8', '0.9')),
    CHECK (init_kind IN ('none', 'factory', 'eip7702')),
    CHECK (transaction_index >= 0 AND operation_index >= 0 AND event_log_index >= 0),
    CHECK (nonce >= 0 AND nonce_key >= 0 AND nonce_sequence >= 0),
    CHECK (actual_gas_cost >= 0 AND actual_gas_used >= 0),
    CHECK (call_gas_limit >= 0 AND verification_gas_limit >= 0),
    CHECK (pre_verification_gas >= 0 AND max_fee_per_gas >= 0 AND max_priority_fee_per_gas >= 0),
    CHECK (paymaster_verification_gas_limit IS NULL OR paymaster_verification_gas_limit >= 0),
    CHECK (paymaster_post_op_gas_limit IS NULL OR paymaster_post_op_gas_limit >= 0),
    CHECK (account_gas_limits IS NULL OR octet_length(account_gas_limits) = 32),
    CHECK (gas_fees IS NULL OR octet_length(gas_fees) = 32)
) PARTITION BY RANGE (block_number);

CREATE TABLE erc4337_user_operations_p_0_1000000
    PARTITION OF erc4337_user_operations FOR VALUES FROM (0) TO (1000000);
CREATE TABLE erc4337_user_operations_default
    PARTITION OF erc4337_user_operations DEFAULT;

CREATE INDEX erc4337_user_operations_canonical_hash_idx
    ON erc4337_user_operations (chain_id, configuration_digest, user_op_hash)
    WHERE canonical;
CREATE INDEX erc4337_user_operations_global_idx
    ON erc4337_user_operations (
        chain_id, configuration_digest, block_number DESC,
        transaction_index DESC, operation_index DESC, user_op_hash DESC
    ) WHERE canonical;
CREATE INDEX erc4337_user_operations_transaction_idx
    ON erc4337_user_operations (
        chain_id, configuration_digest, transaction_hash, operation_index
    ) WHERE canonical;

CREATE FUNCTION enforce_erc4337_canonical_user_op_hash()
RETURNS trigger
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

CREATE TRIGGER erc4337_canonical_user_op_hash_trigger
BEFORE INSERT OR UPDATE OF canonical ON erc4337_user_operations
FOR EACH ROW EXECUTE FUNCTION enforce_erc4337_canonical_user_op_hash();

CREATE TABLE erc4337_user_operation_events (
    chain_id NUMERIC(78, 0) NOT NULL,
    configuration_digest BYTEA NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    transaction_hash BYTEA NOT NULL,
    operation_index BIGINT NOT NULL,
    log_index BIGINT NOT NULL,
    event_kind TEXT NOT NULL,
    sender BYTEA NOT NULL,
    nonce NUMERIC(78, 0),
    related_address BYTEA,
    paymaster BYTEA,
    raw_data BYTEA NOT NULL,
    reason TEXT,
    panic_code NUMERIC(78, 0),
    canonical BOOLEAN NOT NULL,
    PRIMARY KEY (
        chain_id, configuration_digest, block_number, block_hash,
        transaction_hash, operation_index, log_index
    ),
    FOREIGN KEY (
        chain_id, configuration_digest, block_number, block_hash,
        transaction_hash, operation_index
    ) REFERENCES erc4337_user_operations(
        chain_id, configuration_digest, block_number, block_hash,
        transaction_hash, operation_index
    ),
    CHECK (octet_length(configuration_digest) = 32),
    CHECK (octet_length(block_hash) = 32),
    CHECK (octet_length(transaction_hash) = 32),
    CHECK (octet_length(sender) = 20),
    CHECK (related_address IS NULL OR octet_length(related_address) = 20),
    CHECK (paymaster IS NULL OR octet_length(paymaster) = 20),
    CHECK (operation_index >= 0 AND log_index >= 0),
    CHECK (nonce IS NULL OR nonce >= 0),
    CHECK (panic_code IS NULL OR panic_code >= 0),
    CHECK (event_kind IN (
        'account_deployed', 'ignored_init_code', 'eip7702_initialized',
        'execution_revert', 'post_op_revert', 'prefund_too_low'
    ))
) PARTITION BY RANGE (block_number);

CREATE TABLE erc4337_user_operation_events_p_0_1000000
    PARTITION OF erc4337_user_operation_events FOR VALUES FROM (0) TO (1000000);
CREATE TABLE erc4337_user_operation_events_default
    PARTITION OF erc4337_user_operation_events DEFAULT;

CREATE TABLE erc4337_user_operation_participants (
    chain_id NUMERIC(78, 0) NOT NULL,
    configuration_digest BYTEA NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    transaction_hash BYTEA NOT NULL,
    operation_index BIGINT NOT NULL,
    address BYTEA NOT NULL,
    role TEXT NOT NULL,
    canonical BOOLEAN NOT NULL,
    PRIMARY KEY (
        chain_id, configuration_digest, block_number, block_hash,
        transaction_hash, operation_index, address, role
    ),
    FOREIGN KEY (
        chain_id, configuration_digest, block_number, block_hash,
        transaction_hash, operation_index
    ) REFERENCES erc4337_user_operations(
        chain_id, configuration_digest, block_number, block_hash,
        transaction_hash, operation_index
    ),
    CHECK (octet_length(configuration_digest) = 32),
    CHECK (octet_length(block_hash) = 32),
    CHECK (octet_length(transaction_hash) = 32),
    CHECK (octet_length(address) = 20),
    CHECK (operation_index >= 0),
    CHECK (role IN (
        'sender', 'entry_point', 'bundler', 'beneficiary', 'factory',
        'paymaster', 'aggregator', 'eip7702_delegate'
    ))
) PARTITION BY RANGE (block_number);

CREATE TABLE erc4337_user_operation_participants_p_0_1000000
    PARTITION OF erc4337_user_operation_participants FOR VALUES FROM (0) TO (1000000);
CREATE TABLE erc4337_user_operation_participants_default
    PARTITION OF erc4337_user_operation_participants DEFAULT;
CREATE INDEX erc4337_user_operation_participants_address_idx
    ON erc4337_user_operation_participants (
        chain_id, configuration_digest, address, block_number DESC,
        operation_index DESC
    ) WHERE canonical;

CREATE TABLE erc4337_covered_blocks (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    configuration_digest BYTEA NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    durable_job_id BIGINT NOT NULL,
    job_generation BIGINT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (chain_id, configuration_digest, block_number),
    FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash),
    CHECK (octet_length(configuration_digest) = 32),
    CHECK (octet_length(block_hash) = 32),
    CHECK (block_number >= 0 AND durable_job_id > 0 AND job_generation > 0)
);

CREATE TABLE erc4337_coverage_ranges (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    configuration_digest BYTEA NOT NULL,
    start_block NUMERIC(78, 0) NOT NULL,
    start_block_hash BYTEA NOT NULL,
    end_block NUMERIC(78, 0) NOT NULL,
    end_block_hash BYTEA NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (chain_id, configuration_digest, start_block),
    UNIQUE (chain_id, configuration_digest, end_block),
    CHECK (octet_length(configuration_digest) = 32),
    CHECK (octet_length(start_block_hash) = 32),
    CHECK (octet_length(end_block_hash) = 32),
    CHECK (start_block >= 0 AND end_block >= start_block)
);

CREATE INDEX erc4337_coverage_ranges_end_idx
    ON erc4337_coverage_ranges (
        chain_id, configuration_digest, end_block, start_block
    );

CREATE FUNCTION erc4337_remove_covered_block(
    target_chain_id NUMERIC,
    target_configuration_digest BYTEA,
    target_block_number NUMERIC
)
RETURNS void
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

CREATE VIEW published_erc4337_user_operations AS
SELECT operation.*,
       block.timestamp AS block_timestamp,
       finality.safe_number,
       finality.finalized_number,
       covered.durable_job_id,
       covered.job_generation
FROM erc4337_user_operations AS operation
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = operation.chain_id
 AND canonical.number = operation.block_number
 AND canonical.block_hash = operation.block_hash
JOIN blocks AS block
  ON block.chain_id = operation.chain_id
 AND block.number = operation.block_number
 AND block.hash = operation.block_hash
JOIN erc4337_covered_blocks AS covered
  ON covered.chain_id = operation.chain_id
 AND covered.configuration_digest = operation.configuration_digest
 AND covered.block_number = operation.block_number
 AND covered.block_hash = operation.block_hash
JOIN published_block_stage_results AS published
  ON published.chain_id = covered.chain_id
 AND published.block_number = covered.block_number
 AND published.block_hash = covered.block_hash
 AND published.stage = 'userop'
 AND published.stage_version = 1
 AND published.state = 'complete'
 AND published.durable_job_id = covered.durable_job_id
 AND published.job_generation = covered.job_generation
 AND published.details->>'configuration_digest' = encode(covered.configuration_digest, 'hex')
LEFT JOIN chain_finality AS finality ON finality.chain_id = operation.chain_id
WHERE operation.canonical;

CREATE FUNCTION erc4337_add_covered_block(
    target_chain_id NUMERIC,
    target_configuration_digest BYTEA,
    target_block_number NUMERIC,
    target_block_hash BYTEA,
    target_durable_job_id BIGINT,
    target_job_generation BIGINT
)
RETURNS void
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

CREATE FUNCTION erc4337_remove_block_coverage(
    target_chain_id NUMERIC,
    target_block_number NUMERIC,
    target_block_hash BYTEA
)
RETURNS INTEGER
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

CREATE FUNCTION erc4337_remove_detached_coverage()
RETURNS trigger
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

CREATE TRIGGER erc4337_coverage_canonical_detach_trigger
AFTER UPDATE OR DELETE ON canonical_blocks
FOR EACH ROW EXECUTE FUNCTION erc4337_remove_detached_coverage();

DO $$
DECLARE
    migration_schema TEXT := current_schema();
BEGIN
    EXECUTE format(
        'ALTER FUNCTION %I.enforce_erc4337_canonical_user_op_hash() SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
    EXECUTE format(
        'ALTER FUNCTION %I.erc4337_remove_covered_block(NUMERIC, BYTEA, NUMERIC) SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
    EXECUTE format(
        'ALTER FUNCTION %I.erc4337_add_covered_block(NUMERIC, BYTEA, NUMERIC, BYTEA, BIGINT, BIGINT) SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
    EXECUTE format(
        'ALTER FUNCTION %I.erc4337_remove_block_coverage(NUMERIC, NUMERIC, BYTEA) SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
    EXECUTE format(
        'ALTER FUNCTION %I.erc4337_remove_detached_coverage() SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
END
$$;
