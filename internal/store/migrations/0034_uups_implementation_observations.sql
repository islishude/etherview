-- A verified UUPS source artifact is necessary but not sufficient to expose
-- upgrade writes. Record the direct implementation probes at one canonical
-- block hash so proxy@2 can distinguish exact OpenZeppelin 5.x compatibility
-- from candidate-local negative evidence without reusing proxy delegatecalls.

ALTER TABLE proxy_replay_targets
    DROP CONSTRAINT IF EXISTS proxy_replay_targets_target_kind_check;
ALTER TABLE proxy_replay_targets
    ADD CONSTRAINT proxy_replay_targets_target_kind_check
    CHECK (target_kind IN ('proxy', 'beacon', 'uups'));

CREATE TABLE uups_implementation_observations (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    implementation_address BYTEA NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    implementation_code_hash BYTEA NOT NULL,
    verification_job_id UUID NOT NULL
        REFERENCES verified_contract_proxy_artifacts(verification_job_id),
    stage_version INTEGER NOT NULL DEFAULT 2,
    standard_version TEXT NOT NULL,
    probe_state TEXT NOT NULL,
    rejection_reason TEXT,
    proxiable_uuid BYTEA,
    upgrade_interface_version TEXT,
    canonical BOOLEAN NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (
        chain_id, implementation_address, block_hash,
        stage_version, verification_job_id
    ),
    FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash),
    CHECK (
        octet_length(implementation_address) = 20 AND
        implementation_address <> decode(repeat('00', 20), 'hex')
    ),
    CHECK (block_number >= 0),
    CHECK (octet_length(block_hash) = 32),
    CHECK (octet_length(implementation_code_hash) = 32),
    CHECK (stage_version = 2),
    CHECK (standard_version = '5.6.1'),
    CHECK (probe_state IN ('compatible', 'rejected')),
    CHECK (
        rejection_reason IS NULL OR rejection_reason IN (
            'proxiable_uuid_unavailable',
            'proxiable_uuid_invalid',
            'upgrade_interface_version_unavailable',
            'upgrade_interface_version_invalid'
        )
    ),
    CONSTRAINT uups_implementation_observations_exact_probe CHECK (
        (
            probe_state = 'compatible' AND
            rejection_reason IS NULL AND
            proxiable_uuid = decode(
                '360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc',
                'hex'
            ) AND
            upgrade_interface_version = '5.0.0'
        ) OR (
            probe_state = 'rejected' AND
            rejection_reason IS NOT NULL AND
            proxiable_uuid IS NULL AND
            upgrade_interface_version IS NULL
        )
    )
);

CREATE INDEX uups_implementation_observations_current_idx
    ON uups_implementation_observations (
        chain_id, implementation_address, implementation_code_hash,
        block_number DESC, block_hash DESC
    ) WHERE canonical AND probe_state = 'compatible';

-- Current-state promotion must rank rejected and compatible outcomes together:
-- a newer rejection shadows an older compatible probe.
CREATE INDEX uups_implementation_observations_latest_idx
    ON uups_implementation_observations (
        chain_id, implementation_address, implementation_code_hash,
        block_number DESC, block_hash DESC
    ) WHERE canonical;

CREATE INDEX uups_implementation_observations_verification_idx
    ON uups_implementation_observations (
        verification_job_id, chain_id, block_hash
    );

-- Raw probe facts are shared across replay generations. Every usable fact has
-- a separate append-only witness produced by the active durable proxy@2 lease.
CREATE TABLE uups_implementation_observation_generations (
    id BIGSERIAL PRIMARY KEY,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    implementation_address BYTEA NOT NULL,
    observation_block_hash BYTEA NOT NULL,
    observation_stage_version INTEGER NOT NULL,
    verification_job_id UUID NOT NULL,
    durable_job_id BIGINT NOT NULL REFERENCES durable_jobs(id),
    job_generation BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (
        chain_id, implementation_address, observation_block_hash,
        observation_stage_version, verification_job_id
    ) REFERENCES uups_implementation_observations (
        chain_id, implementation_address, block_hash,
        stage_version, verification_job_id
    ) ON DELETE RESTRICT,
    UNIQUE (
        chain_id, implementation_address, observation_block_hash,
        observation_stage_version, verification_job_id,
        durable_job_id, job_generation
    ),
    CHECK (octet_length(implementation_address) = 20),
    CHECK (octet_length(observation_block_hash) = 32),
    CHECK (observation_stage_version = 2),
    CHECK (job_generation > 0)
);

CREATE INDEX uups_implementation_observation_generations_job_idx
    ON uups_implementation_observation_generations (
        durable_job_id, job_generation, id
    );

CREATE OR REPLACE FUNCTION enforce_uups_implementation_observation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.canonical IS NOT TRUE OR NOT EXISTS (
        SELECT 1
        FROM verified_contract_proxy_artifacts AS artifact
        JOIN verified_contracts AS verified
          ON verified.chain_id = artifact.chain_id
         AND verified.address = artifact.address
         AND verified.code_hash = artifact.code_hash
         AND verified.valid_from_block = artifact.valid_from_block
         AND verified.verification_job_id = artifact.verification_job_id
         AND verified.request_digest = artifact.request_digest
        JOIN contract_code_observations AS code_observation
          ON code_observation.chain_id = NEW.chain_id
         AND code_observation.address = NEW.implementation_address
         AND code_observation.block_number = NEW.block_number
         AND code_observation.block_hash = NEW.block_hash
         AND code_observation.code_hash = NEW.implementation_code_hash
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = code_observation.chain_id
         AND canonical.number = code_observation.block_number
         AND canonical.block_hash = code_observation.block_hash
        WHERE artifact.verification_job_id = NEW.verification_job_id
          AND artifact.chain_id = NEW.chain_id
          AND artifact.address = NEW.implementation_address
          AND artifact.code_hash = NEW.implementation_code_hash
          AND artifact.artifact_kind = 'uups_implementation'
          AND artifact.standard_version = NEW.standard_version
          AND artifact.runtime_immutable_address = NEW.implementation_address
          AND artifact.valid_from_block <= NEW.block_number
          AND (verified.valid_to_block IS NULL OR
               verified.valid_to_block >= NEW.block_number)
          AND code_observation.code IS NOT NULL
          AND code_observation.canonical = TRUE
    ) THEN
        RAISE EXCEPTION 'UUPS observation lacks exact canonical artifact and code evidence';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER uups_implementation_observations_source_guard
BEFORE INSERT ON uups_implementation_observations
FOR EACH ROW EXECUTE FUNCTION enforce_uups_implementation_observation();

-- Canonicality is the only mutable projection. Probe identity and outcome stay
-- append-only, including rejected candidate-local evidence.
CREATE OR REPLACE FUNCTION enforce_uups_implementation_observation_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' OR
       (to_jsonb(NEW) - 'canonical') IS DISTINCT FROM
       (to_jsonb(OLD) - 'canonical') THEN
        RAISE EXCEPTION 'UUPS implementation observations are append-only';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER uups_implementation_observations_immutable
BEFORE UPDATE OR DELETE ON uups_implementation_observations
FOR EACH ROW EXECUTE FUNCTION enforce_uups_implementation_observation_mutation();

CREATE TRIGGER uups_implementation_observation_generations_immutable
BEFORE UPDATE OR DELETE ON uups_implementation_observation_generations
FOR EACH ROW EXECUTE FUNCTION reject_proxy_v2_evidence_mutation();

CREATE TRIGGER uups_implementation_observation_generations_source_guard
BEFORE INSERT ON uups_implementation_observation_generations
FOR EACH ROW EXECUTE FUNCTION enforce_proxy_observation_generation();

-- The deferred check lets the worker insert the raw fact before its witness in
-- one transaction, while preventing even an authenticated probe row from
-- surviving without an active-lease-fenced generation identity.
CREATE OR REPLACE FUNCTION enforce_uups_implementation_observation_witness()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM uups_implementation_observation_generations AS generation
        WHERE generation.chain_id = NEW.chain_id
          AND generation.implementation_address = NEW.implementation_address
          AND generation.observation_block_hash = NEW.block_hash
          AND generation.observation_stage_version = NEW.stage_version
          AND generation.verification_job_id = NEW.verification_job_id
    ) THEN
        RAISE EXCEPTION 'UUPS implementation observation lacks a lease-fenced generation witness';
    END IF;
    RETURN NEW;
END
$$;

CREATE CONSTRAINT TRIGGER uups_implementation_observations_witness_guard
AFTER INSERT ON uups_implementation_observations
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION enforce_uups_implementation_observation_witness();

DO $migration$
DECLARE
    migration_schema TEXT := current_schema();
    function_name TEXT;
BEGIN
    IF migration_schema IS NULL THEN
        RAISE EXCEPTION 'UUPS implementation observation migration requires a current schema';
    END IF;
    FOREACH function_name IN ARRAY ARRAY[
        'enforce_uups_implementation_observation',
        'enforce_uups_implementation_observation_mutation',
        'enforce_uups_implementation_observation_witness'
    ]
    LOOP
        EXECUTE format(
            'ALTER FUNCTION %I.%I() SET search_path = %I, pg_catalog',
            migration_schema, function_name, migration_schema
        );
    END LOOP;
END
$migration$;
