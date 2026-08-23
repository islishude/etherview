CREATE TABLE verification_compilation_units (
    id UUID PRIMARY KEY,
    source_job_id UUID NOT NULL UNIQUE
        REFERENCES verification_jobs(id) ON DELETE RESTRICT,
    request_digest BYTEA NOT NULL,
    language TEXT NOT NULL,
    compiler_version TEXT NOT NULL,
    compiler_platform TEXT NOT NULL,
    catalog_generation_id BIGINT NOT NULL,
    compiler_sha256 BYTEA NOT NULL,
    executor_kind TEXT NOT NULL,
    execution_policy TEXT NOT NULL,
    executor_sha256 BYTEA NOT NULL,
    standard_json JSONB NOT NULL,
    standard_json_payload BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT verification_compilation_units_job_digest_fk
        FOREIGN KEY (source_job_id, request_digest)
        REFERENCES verification_results(job_id, request_digest) ON DELETE RESTRICT,
    CONSTRAINT verification_compilation_units_shape_check CHECK (
        octet_length(request_digest) = 32 AND
        language = 'solidity' AND
        length(compiler_version) BETWEEN 1 AND 128 AND
        length(compiler_platform) BETWEEN 1 AND 64 AND
        catalog_generation_id > 0 AND
        octet_length(compiler_sha256) = 32 AND
        length(executor_kind) BETWEEN 1 AND 64 AND
        length(execution_policy) BETWEEN 1 AND 64 AND
        octet_length(executor_sha256) = 32 AND
        jsonb_typeof(standard_json) = 'object' AND
        octet_length(standard_json_payload) BETWEEN 2 AND 67108864 AND
        standard_json = convert_from(standard_json_payload, 'UTF8')::jsonb
    )
);

CREATE TABLE verification_compilation_contracts (
    compilation_id UUID NOT NULL
        REFERENCES verification_compilation_units(id) ON DELETE RESTRICT,
    file_name TEXT NOT NULL,
    contract_name TEXT NOT NULL,
    abi JSONB NOT NULL,
    creation_bytecode BYTEA NOT NULL,
    runtime_bytecode BYTEA NOT NULL,
    compilation_artifacts JSONB NOT NULL,
    creation_code_artifacts JSONB NOT NULL,
    runtime_code_artifacts JSONB NOT NULL,
    PRIMARY KEY (compilation_id, file_name, contract_name),
    CONSTRAINT verification_compilation_contracts_shape_check CHECK (
        length(file_name) BETWEEN 1 AND 384 AND
        length(contract_name) BETWEEN 1 AND 256 AND
        jsonb_typeof(abi) = 'array' AND
        octet_length(creation_bytecode) BETWEEN 1 AND 67108864 AND
        octet_length(runtime_bytecode) BETWEEN 1 AND 67108864 AND
        jsonb_typeof(compilation_artifacts) = 'object' AND
        jsonb_typeof(creation_code_artifacts) = 'object' AND
        jsonb_typeof(runtime_code_artifacts) = 'object'
    )
);

CREATE FUNCTION reject_verification_compilation_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'authenticated verification compilation units are immutable';
END
$$;

CREATE TRIGGER verification_compilation_units_immutable
BEFORE UPDATE OR DELETE ON verification_compilation_units
FOR EACH ROW EXECUTE FUNCTION reject_verification_compilation_mutation();

CREATE TRIGGER verification_compilation_contracts_immutable
BEFORE UPDATE OR DELETE ON verification_compilation_contracts
FOR EACH ROW EXECUTE FUNCTION reject_verification_compilation_mutation();
