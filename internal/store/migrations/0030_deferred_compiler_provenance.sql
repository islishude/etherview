ALTER TABLE verification_jobs
    DROP CONSTRAINT verification_jobs_compiler_check;

ALTER TABLE verification_jobs
    ADD CONSTRAINT verification_jobs_compiler_check CHECK (
        (
            kind IN ('sourcify', 'sourcify_from_etherscan', 'proxy') AND
            language IS NULL AND compiler_version IS NULL AND
            compiler_platform IS NULL AND catalog_language IS NULL AND
            catalog_generation_id IS NULL AND compiler_digest IS NULL AND
            (kind <> 'proxy' OR runner_digest IS NULL)
        ) OR (
            kind NOT IN ('sourcify', 'sourcify_from_etherscan', 'proxy') AND
            language IN ('solidity', 'yul', 'vyper') AND
            length(compiler_version) BETWEEN 1 AND 128 AND
            catalog_language =
                CASE WHEN language = 'yul' THEN 'solidity' ELSE language END AND
            (
                (
                    compiler_platform IS NULL AND
                    catalog_generation_id IS NULL AND
                    compiler_digest IS NULL
                ) OR (
                    compiler_platform IN (
                        'bin', 'emscripten-asmjs', 'emscripten-wasm32',
                        'linux-amd64', 'linux-arm64', 'macosx-amd64',
                        'wasm', 'windows-amd64'
                    ) AND
                    catalog_generation_id IS NOT NULL AND
                    octet_length(compiler_digest) = 32
                )
            ) AND
            (
                status <> 'succeeded' OR
                (
                    compiler_platform IS NOT NULL AND
                    catalog_generation_id IS NOT NULL AND
                    compiler_digest IS NOT NULL
                )
            )
        )
    );

CREATE OR REPLACE FUNCTION enforce_deferred_compiler_provenance()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.compiler_platform IS NOT DISTINCT FROM OLD.compiler_platform AND
       NEW.catalog_generation_id IS NOT DISTINCT FROM OLD.catalog_generation_id AND
       NEW.compiler_digest IS NOT DISTINCT FROM OLD.compiler_digest THEN
        RETURN NEW;
    END IF;

    IF OLD.compiler_platform IS NULL AND
       OLD.catalog_generation_id IS NULL AND
       OLD.compiler_digest IS NULL AND
       NEW.compiler_platform IS NOT NULL AND
       NEW.catalog_generation_id IS NOT NULL AND
       NEW.compiler_digest IS NOT NULL AND
       OLD.status = 'running' AND NEW.status = 'running' AND
       OLD.lease_token IS NOT NULL AND
       OLD.lease_expires_at > clock_timestamp() AND
       NEW.lease_token IS NOT DISTINCT FROM OLD.lease_token AND
       NEW.leased_by IS NOT DISTINCT FROM OLD.leased_by AND
       NEW.lease_expires_at IS NOT DISTINCT FROM OLD.lease_expires_at THEN
        RETURN NEW;
    END IF;

    RAISE EXCEPTION 'verification compiler provenance is immutable after binding';
END
$$;

CREATE TRIGGER verification_jobs_deferred_compiler_provenance
BEFORE UPDATE OF compiler_platform, catalog_generation_id, compiler_digest
ON verification_jobs
FOR EACH ROW EXECUTE FUNCTION enforce_deferred_compiler_provenance();

DO $migration$
DECLARE
    migration_schema TEXT := current_schema();
BEGIN
    IF migration_schema IS NULL THEN
        RAISE EXCEPTION 'deferred compiler provenance migration requires a current schema';
    END IF;
    EXECUTE format(
        'ALTER FUNCTION %I.enforce_deferred_compiler_provenance() SET search_path = %I, pg_catalog',
        migration_schema, migration_schema
    );
END
$migration$;
