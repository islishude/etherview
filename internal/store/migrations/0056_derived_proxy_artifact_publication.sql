CREATE OR REPLACE FUNCTION enforce_verified_contract_proxy_artifact()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM verification_results AS result
        JOIN verification_jobs AS job ON job.id = result.job_id
        JOIN verified_contracts AS verified
          ON verified.verification_job_id = result.job_id
         AND verified.request_digest = result.request_digest
        JOIN blocks AS target_block
          ON target_block.chain_id = job.chain_id
         AND target_block.hash = job.block_hash
        JOIN contract_code_observations AS observation
          ON observation.chain_id = job.chain_id
         AND observation.address = job.address
         AND observation.code_hash = job.code_hash
         AND observation.block_hash = job.block_hash
         AND observation.block_number = target_block.number
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = target_block.chain_id
         AND canonical.number = target_block.number
         AND canonical.block_hash = target_block.hash
        WHERE result.job_id = NEW.verification_job_id
          AND result.request_digest = NEW.request_digest
          AND result.outcome_kind = 'verification_success'
          AND result.proxy_artifact_kind = NEW.artifact_kind
          AND result.proxy_standard_version = NEW.standard_version
          AND result.proxy_runtime_immutable_address IS NOT DISTINCT FROM
              NEW.runtime_immutable_address
          AND result.proxy_source_manifest_sha256 = NEW.source_manifest_sha256
          AND job.kind IN ('address', 'derived')
          AND job.status = 'succeeded'
          AND job.chain_id = NEW.chain_id
          AND job.address = NEW.address
          AND job.code_hash = NEW.code_hash
          AND target_block.number = NEW.valid_from_block
          AND observation.canonical = TRUE
          AND verified.chain_id = NEW.chain_id
          AND verified.address = NEW.address
          AND verified.code_hash = NEW.code_hash
          AND verified.valid_from_block = NEW.valid_from_block
          AND (
              NEW.artifact_kind <> 'uups_implementation' OR
              (
                  result.proxy_runtime_immutable_address = job.address AND
                  NEW.runtime_immutable_address = job.address
              )
          )
    ) THEN
        RAISE EXCEPTION 'proxy artifact disagrees with verified contract publication';
    END IF;
    RETURN NEW;
END
$$;

DO $migration$
DECLARE
    migration_schema TEXT := current_schema();
BEGIN
    IF migration_schema IS NULL THEN
        RAISE EXCEPTION 'derived proxy artifact migration requires a current schema';
    END IF;
    EXECUTE format(
        'ALTER FUNCTION %I.enforce_verified_contract_proxy_artifact() SET search_path = %I, pg_catalog',
        migration_schema,
        migration_schema
    );
END
$migration$;
