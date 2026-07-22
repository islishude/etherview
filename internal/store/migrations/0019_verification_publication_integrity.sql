-- P30-T07 closes post-0016 publication gaps without changing the checksum of
-- the already-applied durable verification migration.

-- Freeze every publication write surface before replacing any guard or
-- validating existing rows. SHARE ROW EXCLUSIVE conflicts with the
-- RowExclusiveLock taken by INSERT, UPDATE, and DELETE and is held until this
-- migration transaction commits or rolls back. The order mirrors production
-- completion's RowExclusiveLock acquisition (immutable result, projection,
-- then terminal job update). verification_jobs is first read FOR UPDATE, but
-- its RowShareLock is compatible here; locking it first could deadlock a
-- completion that already inserted its result and still needs the job upgrade.
LOCK TABLE verification_results IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE verified_contracts IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE verification_jobs IN SHARE ROW EXCLUSIVE MODE;

CREATE OR REPLACE FUNCTION enforce_verification_result_job_state()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    target_job UUID;
    target_status TEXT;
BEGIN
    IF TG_TABLE_NAME = 'verification_results' THEN
        target_job := NEW.job_id;
    ELSE
        target_job := NEW.id;
    END IF;

    SELECT job.status
    INTO target_status
    FROM verification_jobs AS job
    WHERE job.id = target_job;

    IF NOT FOUND THEN
        RETURN NEW;
    END IF;

    IF target_status = 'succeeded' THEN
        IF NOT EXISTS (
            SELECT 1
            FROM verification_results AS immutable_result
            JOIN verification_jobs AS job ON job.id = immutable_result.job_id
            WHERE immutable_result.job_id = target_job
              AND job.status = 'succeeded'
              AND job.result_kind IS NOT DISTINCT FROM immutable_result.result_kind
              AND job.result IS NOT DISTINCT FROM immutable_result.result
              AND job.error_code IS NULL
        ) THEN
            RAISE EXCEPTION 'successful verification job requires its exact immutable result';
        END IF;
    ELSIF EXISTS (
        SELECT 1
        FROM verification_results AS immutable_result
        WHERE immutable_result.job_id = target_job
    ) THEN
        RAISE EXCEPTION 'verification result and terminal job state disagree';
    END IF;

    RETURN NEW;
END
$$;

-- The 0016 update trigger protects normal worker completion. This additional
-- deferred insert trigger prevents a direct succeeded INSERT from bypassing
-- the same exact-result requirement.
CREATE CONSTRAINT TRIGGER verification_jobs_insert_result
AFTER INSERT ON verification_jobs
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION enforce_verification_result_job_state();

CREATE OR REPLACE FUNCTION enforce_verified_contract_source()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    -- Rows that existed before 0016 remain readable in place, but every INSERT
    -- or UPDATE after this migration must carry immutable provenance.
    IF NEW.verification_job_id IS NULL OR NEW.request_digest IS NULL THEN
        RAISE EXCEPTION 'verified contract projection requires an immutable result';
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM verification_results AS immutable_result
        JOIN verification_jobs AS job ON job.id = immutable_result.job_id
        WHERE immutable_result.job_id = NEW.verification_job_id
          AND immutable_result.request_digest = NEW.request_digest
          AND immutable_result.chain_id = NEW.chain_id
          AND immutable_result.address = NEW.address
          AND immutable_result.code_hash = NEW.code_hash
          AND immutable_result.block_number = NEW.valid_from_block
          AND immutable_result.result_kind = NEW.match_kind
          AND immutable_result.contract_name IS NOT DISTINCT FROM NEW.contract_name
          AND immutable_result.abi IS NOT DISTINCT FROM NEW.abi
          AND immutable_result.sources IS NOT DISTINCT FROM NEW.sources
          AND immutable_result.settings IS NOT DISTINCT FROM NEW.settings
          AND job.language = NEW.language
          AND job.compiler_version = NEW.compiler_version
    ) THEN
        RAISE EXCEPTION 'verified contract projection disagrees with immutable result';
    END IF;
    RETURN NEW;
END
$$;

DO $migration$
DECLARE
    migration_schema TEXT := current_schema();
BEGIN
    IF migration_schema IS NULL THEN
        RAISE EXCEPTION 'verification publication migration requires a current schema';
    END IF;
    EXECUTE format(
        'ALTER FUNCTION %I.enforce_verification_result_job_state() SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
    EXECUTE format(
        'ALTER FUNCTION %I.enforce_verified_contract_source() SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
END
$migration$;

-- Existing databases may have accepted a non-legacy successful job without a
-- result after 0016 but before this migration. Refuse to record 0019 until an
-- operator repairs that corruption. The explicit legacy provenance assigned
-- by 0016 is the only grandfathered successful-job exemption.
DO $validation$
DECLARE
    migration_schema TEXT := current_schema();
BEGIN
    IF migration_schema IS NULL THEN
        RAISE EXCEPTION 'verification publication validation requires a current schema';
    END IF;
    PERFORM pg_catalog.set_config(
        'search_path', pg_catalog.format('%I, pg_catalog', migration_schema), TRUE
    );

    IF EXISTS (
        SELECT 1
        FROM verification_jobs AS job
        WHERE job.status = 'succeeded'
          AND job.compiler_kind IS DISTINCT FROM 'legacy_unrecorded'
          AND NOT EXISTS (
              SELECT 1
              FROM verification_results AS immutable_result
              WHERE immutable_result.job_id = job.id
                AND immutable_result.result_kind IS NOT DISTINCT FROM job.result_kind
                AND immutable_result.result IS NOT DISTINCT FROM job.result
                AND job.error_code IS NULL
          )
    ) THEN
        RAISE EXCEPTION 'non-legacy successful verification job lacks its exact immutable result';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM verification_results AS immutable_result
        JOIN verification_jobs AS job ON job.id = immutable_result.job_id
        WHERE job.status IS DISTINCT FROM 'succeeded'
           OR job.result_kind IS DISTINCT FROM immutable_result.result_kind
           OR job.result IS DISTINCT FROM immutable_result.result
           OR job.error_code IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'existing verification result and terminal job state disagree';
    END IF;
END
$validation$;
