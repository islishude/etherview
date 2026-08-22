ALTER TABLE derived_verification_attempts
    ADD COLUMN stale_from_status TEXT;

ALTER TABLE derived_verification_attempts
    DROP CONSTRAINT derived_verification_attempts_status_check,
    DROP CONSTRAINT derived_verification_attempts_outcome_check,
    ADD CONSTRAINT derived_verification_attempts_status_check CHECK (
        status IN (
            'pending_runtime', 'matched', 'ambiguous', 'no_match',
            'runtime_mismatch', 'stale'
        ) AND
        (stale_from_status IS NULL OR
         stale_from_status IN (
            'pending_runtime', 'matched', 'ambiguous', 'no_match',
            'runtime_mismatch'
         ))
    ),
    ADD CONSTRAINT derived_verification_attempts_outcome_check CHECK (
        (
            status = 'matched' AND stale_from_status IS NULL AND
            file_name IS NOT NULL AND contract_name IS NOT NULL AND
            jsonb_typeof(creation_match) = 'object' AND
            jsonb_typeof(runtime_match) = 'object' AND
            verification_job_id IS NOT NULL
        ) OR (
            status NOT IN ('matched', 'stale') AND stale_from_status IS NULL AND
            file_name IS NULL AND contract_name IS NULL AND
            creation_match IS NULL AND runtime_match IS NULL AND
            verification_job_id IS NULL
        ) OR (
            status = 'stale' AND stale_from_status IS NOT NULL AND
            (
                (stale_from_status = 'matched' AND file_name IS NOT NULL AND
                 contract_name IS NOT NULL AND
                 jsonb_typeof(creation_match) = 'object' AND
                 jsonb_typeof(runtime_match) = 'object' AND
                 verification_job_id IS NOT NULL) OR
                (stale_from_status <> 'matched' AND file_name IS NULL AND
                 contract_name IS NULL AND creation_match IS NULL AND
                 runtime_match IS NULL AND verification_job_id IS NULL)
            )
        )
    );

CREATE FUNCTION synchronize_derived_attempt_trace_canonicality()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.canonical IS NOT DISTINCT FROM OLD.canonical THEN
        RETURN NEW;
    END IF;
    IF NOT NEW.canonical THEN
        UPDATE derived_verification_attempts
        SET stale_from_status = status,
            status = 'stale',
            updated_at = clock_timestamp()
        WHERE chain_id = NEW.chain_id
          AND block_number = NEW.block_number
          AND block_hash = NEW.block_hash
          AND transaction_hash = NEW.transaction_hash
          AND trace_path = NEW.trace_path
          AND status <> 'stale';
    ELSE
        UPDATE derived_verification_attempts
        SET status = stale_from_status,
            stale_from_status = NULL,
            updated_at = clock_timestamp()
        WHERE chain_id = NEW.chain_id
          AND block_number = NEW.block_number
          AND block_hash = NEW.block_hash
          AND transaction_hash = NEW.transaction_hash
          AND trace_path = NEW.trace_path
          AND status = 'stale'
          AND stale_from_status IS NOT NULL;
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER normalized_traces_derived_attempt_canonicality
AFTER UPDATE OF canonical ON normalized_traces
FOR EACH ROW EXECUTE FUNCTION synchronize_derived_attempt_trace_canonicality();

DO $migration$
DECLARE
    migration_schema TEXT := current_schema();
BEGIN
    IF migration_schema IS NULL THEN
        RAISE EXCEPTION 'derived reorg migration requires a current schema';
    END IF;
    EXECUTE format(
        'ALTER FUNCTION %I.synchronize_derived_attempt_trace_canonicality() SET search_path = %I, pg_catalog',
        migration_schema,
        migration_schema
    );
END
$migration$;
