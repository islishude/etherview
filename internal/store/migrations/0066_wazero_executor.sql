-- ADR-0048 / P30-T105: immutable executor migration, no rebinding/backfill.
LOCK TABLE verification_results IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE verified_contracts IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE verification_jobs IN SHARE ROW EXCLUSIVE MODE;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM verification_jobs
        WHERE status IN ('queued','running')
          AND executor_kind IN ('node_solcjs_v1','etherview_vyper_v1')
    ) THEN
        RAISE EXCEPTION 'drain old compiler-bound jobs before WASM executor migration';
    END IF;
END
$$;

ALTER TABLE verification_jobs DROP CONSTRAINT verification_jobs_compiler_check;
ALTER TABLE verification_jobs
    ADD CONSTRAINT verification_jobs_compiler_check CHECK (
        (
            kind IN ('sourcify', 'sourcify_from_etherscan', 'proxy') AND
            language IS NULL AND compiler_version IS NULL AND
            compiler_platform IS NULL AND catalog_language IS NULL AND
            catalog_generation_id IS NULL AND compiler_digest IS NULL AND
            executor_kind IS NULL AND execution_policy IS NULL AND
            executor_digest IS NULL
        ) OR (
            kind NOT IN ('sourcify', 'sourcify_from_etherscan', 'proxy') AND
            language IN ('solidity', 'yul') AND
            length(compiler_version) BETWEEN 1 AND 128 AND
            catalog_language = 'solidity' AND
            (
                (
                    compiler_platform IS NULL AND
                    catalog_generation_id IS NULL AND
                    compiler_digest IS NULL AND
                    executor_kind IS NULL AND
                    execution_policy IS NULL AND
                    executor_digest IS NULL AND
                    status <> 'succeeded'
                ) OR (
                    compiler_platform IN (
                        'bin', 'emscripten-asmjs', 'emscripten-wasm32',
                        'linux-amd64', 'linux-arm64', 'macosx-amd64',
                        'wasm', 'windows-amd64'
                    ) AND
                    catalog_generation_id IS NOT NULL AND
                    octet_length(compiler_digest) = 32 AND
                    (
                        (
                            executor_kind = 'etherview_wazero_v1' AND
                            execution_policy = 'wasm_subprocess_v1' AND
                            octet_length(executor_digest) = 32 AND
                            compiler_platform = 'emscripten-wasm32'
                        ) OR (
                            executor_kind = 'node_solcjs_v1' AND status IN ('succeeded','failed') AND
                            execution_policy = 'trusted_subprocess' AND
                            octet_length(executor_digest) = 32 AND
                            compiler_platform = 'emscripten-wasm32'
                        ) OR (
                            executor_kind = 'legacy_runner' AND
                            execution_policy = 'legacy_hard_isolation' AND
                            octet_length(executor_digest) = 32
                        ) OR (
                            executor_kind = 'legacy_process' AND
                            execution_policy = 'legacy_trusted_process' AND
                            executor_digest IS NULL
                        )
                    )
                )
            )
        ) OR (
            ((kind = 'address' AND language = 'geas' AND compiler_version = '0.3.3') OR
             (kind IN ('address', 'vyper_standard_json', 'vyper_multipart') AND
              language = 'vyper' AND compiler_version = '0.4.3')) AND catalog_language IS NULL AND
            (
                (
                    compiler_platform IS NULL AND
                    catalog_generation_id IS NULL AND
                    compiler_digest IS NULL AND
                    executor_kind IS NULL AND
                    execution_policy IS NULL AND
                    executor_digest IS NULL AND
                    status <> 'succeeded'
                ) OR (
                    ((language = 'geas' AND compiler_platform = 'go-module' AND executor_kind = 'etherview_geas_v1' AND execution_policy = 'trusted_subprocess') OR
                     (language = 'vyper' AND compiler_platform = 'python-wheel' AND
                      compiler_digest = decode('3b9671727c888363740dc678e60336759871487d0e4e9fdd973048fa9635c4fd', 'hex') AND
                      ((executor_kind = 'etherview_wazero_v1' AND execution_policy = 'wasm_subprocess_v1') OR
                       (executor_kind = 'etherview_vyper_v1' AND execution_policy = 'trusted_subprocess' AND status IN ('succeeded','failed'))))) AND
                    catalog_generation_id IS NULL AND octet_length(compiler_digest) = 32 AND
                    octet_length(executor_digest) = 32
                )
            )
        ) IS TRUE
    );

CREATE OR REPLACE FUNCTION enforce_verification_compiler_provenance()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.compiler_platform IS NOT DISTINCT FROM OLD.compiler_platform AND
       NEW.catalog_generation_id IS NOT DISTINCT FROM OLD.catalog_generation_id AND
       NEW.compiler_digest IS NOT DISTINCT FROM OLD.compiler_digest AND
       NEW.executor_kind IS NOT DISTINCT FROM OLD.executor_kind AND
       NEW.execution_policy IS NOT DISTINCT FROM OLD.execution_policy AND
       NEW.executor_digest IS NOT DISTINCT FROM OLD.executor_digest THEN
        RETURN NEW;
    END IF;

    IF OLD.compiler_platform IS NULL AND
       OLD.catalog_generation_id IS NULL AND
       OLD.compiler_digest IS NULL AND
       OLD.executor_kind IS NULL AND
       OLD.execution_policy IS NULL AND
       OLD.executor_digest IS NULL AND
       (
           (
               OLD.language IN ('solidity', 'yul') AND
               NEW.compiler_platform = 'emscripten-wasm32' AND
               NEW.catalog_generation_id IS NOT NULL AND
               octet_length(NEW.compiler_digest) = 32 AND
               NEW.executor_kind = 'etherview_wazero_v1' AND
               NEW.execution_policy = 'wasm_subprocess_v1' AND
               octet_length(NEW.executor_digest) = 32
           ) OR (
               OLD.language = 'geas' AND
               NEW.compiler_platform = 'go-module' AND
               NEW.catalog_generation_id IS NULL AND
               octet_length(NEW.compiler_digest) = 32 AND
               NEW.executor_kind = 'etherview_geas_v1' AND
               NEW.execution_policy = 'trusted_subprocess' AND
               octet_length(NEW.executor_digest) = 32
           ) OR (
               OLD.language = 'vyper' AND NEW.compiler_platform = 'python-wheel' AND
               NEW.catalog_generation_id IS NULL AND
               NEW.compiler_digest = decode('3b9671727c888363740dc678e60336759871487d0e4e9fdd973048fa9635c4fd', 'hex') AND
               NEW.executor_kind = 'etherview_wazero_v1' AND
               NEW.execution_policy = 'wasm_subprocess_v1' AND
               octet_length(NEW.executor_digest) = 32
           )
       ) AND
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
