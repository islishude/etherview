ALTER TABLE verification_jobs
    DROP CONSTRAINT verification_jobs_kind_check,
    DROP CONSTRAINT verification_jobs_compiler_check,
    DROP CONSTRAINT verification_jobs_target_check,
    DROP CONSTRAINT verification_jobs_terminal_check;

ALTER TABLE verification_jobs
    ADD CONSTRAINT verification_jobs_kind_check CHECK (
        kind IN (
            'address',
            'solidity_multipart',
            'solidity_standard_json',
            'solidity_batch_multipart',
            'solidity_batch_standard_json',
            'vyper_multipart',
            'vyper_standard_json',
            'sourcify',
            'sourcify_from_etherscan',
            'proxy'
        )
    ),
    ADD CONSTRAINT verification_jobs_compiler_check CHECK (
        (kind IN ('sourcify', 'sourcify_from_etherscan', 'proxy') AND
            language IS NULL AND compiler_version IS NULL AND
            compiler_platform IS NULL AND catalog_language IS NULL AND
            catalog_generation_id IS NULL AND compiler_digest IS NULL AND
            (kind <> 'proxy' OR runner_digest IS NULL)) OR
        (kind NOT IN ('sourcify', 'sourcify_from_etherscan', 'proxy') AND
            language IS NOT NULL AND length(compiler_version) BETWEEN 1 AND 128 AND
            compiler_platform IN (
                'bin', 'emscripten-asmjs', 'emscripten-wasm32',
                'linux-amd64', 'linux-arm64', 'macosx-amd64',
                'wasm', 'windows-amd64'
            ) AND
            catalog_language = CASE WHEN language = 'yul' THEN 'solidity' ELSE language END AND
            catalog_generation_id IS NOT NULL AND
            octet_length(compiler_digest) = 32)
    ),
    ADD CONSTRAINT verification_jobs_target_check CHECK (
        (kind IN ('address', 'proxy') AND chain_id IS NOT NULL AND
            octet_length(address) = 20 AND octet_length(code_hash) = 32 AND
            octet_length(block_hash) = 32) OR
        (kind NOT IN ('address', 'proxy') AND chain_id IS NULL AND
            address IS NULL AND code_hash IS NULL AND block_hash IS NULL)
    ),
    ADD CONSTRAINT verification_jobs_terminal_check CHECK (
        (status IN ('queued', 'running', 'cancelled') AND
            outcome_kind IS NULL AND outcome IS NULL AND error_code IS NULL) OR
        (status = 'succeeded' AND
            outcome_kind IN (
                'compilation_failure', 'verification_failure',
                'verification_success', 'batch_results', 'sourcify_success',
                'proxy_verification_success'
            ) AND jsonb_typeof(outcome) = 'object' AND error_code IS NULL) OR
        (status = 'failed' AND outcome_kind IS NULL AND outcome IS NULL AND
            length(error_code) BETWEEN 1 AND 64)
    );

ALTER TABLE verification_results
    DROP CONSTRAINT verification_results_outcome_check;

ALTER TABLE verification_results
    ADD CONSTRAINT verification_results_outcome_check CHECK (
        outcome_kind IN (
            'compilation_failure', 'verification_failure',
            'verification_success', 'batch_results', 'sourcify_success',
            'proxy_verification_success'
        ) AND jsonb_typeof(outcome) = 'object' AND
        octet_length(outcome::text) <= 268435456
    );

CREATE TABLE verified_proxy_contracts (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    proxy_address BYTEA NOT NULL,
    proxy_code_hash BYTEA NOT NULL,
    observation_block_number NUMERIC(78, 0) NOT NULL,
    observation_block_hash BYTEA NOT NULL,
    proxy_kind TEXT NOT NULL,
    implementation_address BYTEA NOT NULL,
    implementation_code_hash BYTEA NOT NULL,
    verification_job_id UUID NOT NULL UNIQUE,
    request_digest BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (
        chain_id, proxy_address, proxy_code_hash, observation_block_hash,
        implementation_address, implementation_code_hash
    ),
    CONSTRAINT verified_proxy_contracts_observation_fk FOREIGN KEY (
        chain_id, proxy_address, observation_block_hash
    ) REFERENCES proxy_observations (
        chain_id, proxy_address, block_hash
    ) ON DELETE RESTRICT,
    CONSTRAINT verified_proxy_contracts_result_fk FOREIGN KEY (
        verification_job_id, request_digest
    ) REFERENCES verification_results (
        job_id, request_digest
    ) ON DELETE RESTRICT,
    CONSTRAINT verified_proxy_contracts_identity_check CHECK (
        octet_length(proxy_address) = 20 AND
        octet_length(proxy_code_hash) = 32 AND
        observation_block_number >= 0 AND
        octet_length(observation_block_hash) = 32 AND
        proxy_kind IN ('eip1167', 'eip1967', 'beacon') AND
        octet_length(implementation_address) = 20 AND
        octet_length(implementation_code_hash) = 32 AND
        octet_length(request_digest) = 32
    )
);

CREATE INDEX verified_proxy_contracts_current_idx
    ON verified_proxy_contracts (
        chain_id, proxy_address, observation_block_number DESC
    );

CREATE OR REPLACE FUNCTION reject_verified_proxy_contract_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'verified proxy publications are immutable';
END
$$;

CREATE TRIGGER verified_proxy_contracts_immutable
BEFORE UPDATE OR DELETE ON verified_proxy_contracts
FOR EACH ROW EXECUTE FUNCTION reject_verified_proxy_contract_mutation();

CREATE OR REPLACE FUNCTION enforce_verified_proxy_contract()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM verification_results AS result
        JOIN verification_jobs AS job ON job.id = result.job_id
        JOIN proxy_observations AS observation
          ON observation.chain_id = NEW.chain_id
         AND observation.proxy_address = NEW.proxy_address
         AND observation.block_hash = NEW.observation_block_hash
        WHERE result.job_id = NEW.verification_job_id
          AND result.request_digest = NEW.request_digest
          AND result.outcome_kind = 'proxy_verification_success'
          AND job.kind = 'proxy'
          AND job.status = 'succeeded'
          AND job.chain_id = NEW.chain_id
          AND job.address = NEW.proxy_address
          AND job.code_hash = NEW.proxy_code_hash
          AND job.block_hash = NEW.observation_block_hash
          AND observation.block_number = NEW.observation_block_number
          AND observation.proxy_code_hash = NEW.proxy_code_hash
          AND observation.proxy_kind = NEW.proxy_kind
          AND observation.implementation_address = NEW.implementation_address
          AND observation.implementation_code_hash = NEW.implementation_code_hash
          AND observation.canonical = TRUE
          AND observation.confidence IN ('verified', 'high')
          AND job.request->>'kind' = 'proxy'
          AND job.request->'target'->>'Address' =
              '0x' || encode(NEW.proxy_address, 'hex')
          AND job.request->'target'->>'CodeHash' =
              '0x' || encode(NEW.proxy_code_hash, 'hex')
          AND job.request->'target'->>'AtBlockHash' =
              '0x' || encode(NEW.observation_block_hash, 'hex')
          AND job.request->'proxy_target'->>'kind' = NEW.proxy_kind
          AND job.request->'proxy_target'->>'implementation_address' =
              '0x' || encode(NEW.implementation_address, 'hex')
          AND job.request->'proxy_target'->>'implementation_code_hash' =
              '0x' || encode(NEW.implementation_code_hash, 'hex')
          AND result.outcome->>'kind' = 'proxy_verification_success'
          AND result.outcome->>'proxy_address' =
              '0x' || encode(NEW.proxy_address, 'hex')
          AND result.outcome->>'proxy_code_hash' =
              '0x' || encode(NEW.proxy_code_hash, 'hex')
          AND result.outcome->>'observation_block_hash' =
              '0x' || encode(NEW.observation_block_hash, 'hex')
          AND result.outcome->>'proxy_kind' = NEW.proxy_kind
          AND result.outcome->>'implementation_address' =
              '0x' || encode(NEW.implementation_address, 'hex')
          AND result.outcome->>'implementation_code_hash' =
              '0x' || encode(NEW.implementation_code_hash, 'hex')
    ) THEN
        RAISE EXCEPTION 'verified proxy publication disagrees with its immutable result';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER verified_proxy_contracts_source_guard
BEFORE INSERT ON verified_proxy_contracts
FOR EACH ROW EXECUTE FUNCTION enforce_verified_proxy_contract();
