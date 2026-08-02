-- P20-T13/P30-T15 are a destructive proxy-publication cutover. The former table bound a
-- broad slot guess directly to a verification result and cannot represent an
-- authenticated OpenZeppelin artifact, an immutable authority, or a durable
-- proxy@2 generation. Do not carry those publications across the boundary.
DROP TABLE verified_proxy_contracts;
DROP FUNCTION reject_verified_proxy_contract_mutation();
DROP FUNCTION enforce_verified_proxy_contract();

-- A code incarnation may have more than one valid verification result. In
-- particular, an ordinary source publication must not be able to occupy the
-- identity key and prevent a later exact OpenZeppelin artifact attestation.
-- Keep every immutable result projection and make readers choose
-- deterministically; artifact references below bind to the exact job that
-- authenticated the upstream source.
ALTER TABLE verified_contracts
    DROP CONSTRAINT verified_contracts_pkey,
    ADD PRIMARY KEY (
        chain_id, address, code_hash, valid_from_block, verification_job_id
    );

ALTER TABLE search_catalog_documents
    ADD COLUMN verification_match_type TEXT,
    ADD COLUMN verification_request_digest BYTEA,
    ADD COLUMN verification_job_id UUID,
    ADD CONSTRAINT search_catalog_verified_result_shape CHECK (
        (
            source_kind <> 'verified_contract' AND
            verification_match_type IS NULL AND
            verification_request_digest IS NULL AND
            verification_job_id IS NULL
        ) OR (
            source_kind = 'verified_contract' AND (
                (
                    verification_match_type IS NULL AND
                    verification_request_digest IS NULL AND
                    verification_job_id IS NULL
                ) OR (
                    verification_match_type IN ('full', 'partial') AND
                    octet_length(verification_request_digest) = 32 AND
                    verification_job_id IS NOT NULL
                )
            )
        )
    );

-- Search history previously used only the code-incarnation identity. Once
-- multiple immutable verification results can coexist, each result needs its
-- own source identity so closing or deleting one result cannot retire another
-- result's document. The logical identity deliberately remains the shared
-- code incarnation so search still returns one contract result.
CREATE OR REPLACE FUNCTION record_verified_contract_search_catalog_document()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    source_chain NUMERIC(78, 0);
    old_identity TEXT;
    new_identity TEXT;
    logical_identity_value TEXT;
    next_generation BIGINT;
BEGIN
    IF TG_OP = 'UPDATE' AND OLD.chain_id IS DISTINCT FROM NEW.chain_id THEN
        RAISE EXCEPTION 'search catalog source chain_id is immutable';
    END IF;
    IF TG_OP = 'UPDATE' AND OLD IS NOT DISTINCT FROM NEW THEN
        RETURN NEW;
    END IF;
    IF TG_OP = 'DELETE' THEN
        source_chain := OLD.chain_id;
        old_identity := jsonb_build_array(
            encode(OLD.address, 'hex'), encode(OLD.code_hash, 'hex'),
            OLD.valid_from_block::text, OLD.verification_job_id::text
        )::text;
    ELSE
        source_chain := NEW.chain_id;
        new_identity := jsonb_build_array(
            encode(NEW.address, 'hex'), encode(NEW.code_hash, 'hex'),
            NEW.valid_from_block::text, NEW.verification_job_id::text
        )::text;
        logical_identity_value := jsonb_build_array(
            encode(NEW.address, 'hex'), encode(NEW.code_hash, 'hex'),
            NEW.valid_from_block::text
        )::text;
        IF TG_OP = 'UPDATE' THEN
            old_identity := jsonb_build_array(
                encode(OLD.address, 'hex'), encode(OLD.code_hash, 'hex'),
                OLD.valid_from_block::text, OLD.verification_job_id::text
            )::text;
        END IF;
    END IF;

    INSERT INTO search_catalog_generations (chain_id)
    VALUES (source_chain)
    ON CONFLICT (chain_id) DO NOTHING;
    UPDATE search_catalog_generations
    SET generation = generation + 1, updated_at = now()
    WHERE chain_id = source_chain
    RETURNING generation INTO next_generation;

    UPDATE search_catalog_documents
    SET valid_to_generation = next_generation
    WHERE chain_id = source_chain
      AND source_kind = 'verified_contract'
      AND source_identity = old_identity
      AND valid_to_generation IS NULL;

    IF TG_OP = 'UPDATE' AND old_identity IS DISTINCT FROM new_identity THEN
        UPDATE search_catalog_documents
        SET valid_to_generation = next_generation
        WHERE chain_id = source_chain
          AND source_kind = 'verified_contract'
          AND source_identity = new_identity
          AND valid_to_generation IS NULL;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;

    INSERT INTO search_catalog_documents (
        chain_id, source_kind, source_identity, logical_identity,
        valid_from_generation, result_kind, result_key, result_label,
        exact_terms, partial_terms, target_address, code_hash,
        valid_from_block, valid_to_block, verification_match_type,
        verification_request_digest, verification_job_id
    ) VALUES (
        NEW.chain_id, 'verified_contract', new_identity,
        logical_identity_value, next_generation, 'contract',
        '0x' || encode(NEW.address, 'hex'), NEW.contract_name,
        ARRAY[
            lower('0x' || encode(NEW.address, 'hex')),
            lower(NEW.contract_name)
        ], ARRAY[lower(NEW.contract_name)], NEW.address, NEW.code_hash,
        NEW.valid_from_block, NEW.valid_to_block, NEW.match_type,
        NEW.request_digest, NEW.verification_job_id
    );
    RETURN NEW;
END
$$;

-- Rows that predate this migration were unique by code incarnation, so the
-- old search identities have an unambiguous verification job to attach.
UPDATE search_catalog_documents AS document
SET source_identity = jsonb_build_array(
        encode(verified.address, 'hex'), encode(verified.code_hash, 'hex'),
        verified.valid_from_block::text,
        verified.verification_job_id::text
    )::text,
    logical_identity = jsonb_build_array(
        encode(verified.address, 'hex'), encode(verified.code_hash, 'hex'),
        verified.valid_from_block::text
    )::text,
    verification_match_type = verified.match_type,
    verification_request_digest = verified.request_digest,
    verification_job_id = verified.verification_job_id
FROM verified_contracts AS verified
WHERE document.chain_id = verified.chain_id
  AND document.source_kind = 'verified_contract'
  AND document.source_identity = jsonb_build_array(
        encode(verified.address, 'hex'), encode(verified.code_hash, 'hex'),
        verified.valid_from_block::text
      )::text;

DROP TRIGGER verified_contracts_search_catalog_trigger ON verified_contracts;
CREATE TRIGGER verified_contracts_search_catalog_trigger
AFTER INSERT OR UPDATE OR DELETE ON verified_contracts
FOR EACH ROW EXECUTE FUNCTION record_verified_contract_search_catalog_document();

-- An OpenZeppelin artifact publication must be derived while the immutable
-- verification result is created. Keeping the authenticated identity on the
-- result prevents a later writer from promoting an ordinary verified contract
-- into a trusted proxy or management artifact.
ALTER TABLE verification_results
    ADD COLUMN proxy_artifact_kind TEXT,
    ADD COLUMN proxy_standard_version TEXT,
    ADD COLUMN proxy_runtime_immutable_address BYTEA,
    ADD COLUMN proxy_source_manifest_sha256 BYTEA,
    ADD CONSTRAINT verification_results_proxy_attestation_shape CHECK (
        (
            proxy_artifact_kind IS NULL AND
            proxy_standard_version IS NULL AND
            proxy_runtime_immutable_address IS NULL AND
            proxy_source_manifest_sha256 IS NULL
        ) OR (
            outcome_kind = 'verification_success' AND
            proxy_artifact_kind IS NOT NULL AND
            proxy_artifact_kind IN (
                'erc1967_proxy', 'transparent_proxy', 'beacon_proxy',
                'uups_implementation', 'proxy_admin', 'upgradeable_beacon'
            ) AND
            proxy_standard_version = '5.6.1' AND
            proxy_source_manifest_sha256 IS NOT NULL AND
            octet_length(proxy_source_manifest_sha256) = 32 AND
            proxy_source_manifest_sha256 <>
                decode(repeat('00', 32), 'hex') AND
            (
                (
                    proxy_artifact_kind IN (
                        'transparent_proxy', 'beacon_proxy', 'uups_implementation'
                    ) AND
                    proxy_runtime_immutable_address IS NOT NULL AND
                    octet_length(proxy_runtime_immutable_address) = 20 AND
                    proxy_runtime_immutable_address <>
                        decode(repeat('00', 20), 'hex')
                ) OR (
                    proxy_artifact_kind NOT IN (
                        'transparent_proxy', 'beacon_proxy', 'uups_implementation'
                    ) AND
                    proxy_runtime_immutable_address IS NULL
                )
            )
        )
    );

ALTER TABLE proxy_observations
    DROP CONSTRAINT proxy_observations_pkey,
    ADD COLUMN stage_version INTEGER NOT NULL DEFAULT 1,
    ADD COLUMN proxy_pattern TEXT NOT NULL DEFAULT 'unknown',
    ADD COLUMN standard_version TEXT,
    ADD COLUMN admin_address BYTEA,
    ADD COLUMN admin_code_hash BYTEA,
    ADD COLUMN beacon_code_hash BYTEA,
    ADD COLUMN immutable_args BYTEA,
    ADD COLUMN evidence_state TEXT NOT NULL DEFAULT 'generic',
    ADD CONSTRAINT proxy_observations_pattern_check CHECK (
        proxy_pattern IN ('clone', 'erc1967', 'transparent', 'uups', 'beacon', 'unknown')
    ),
    ADD CONSTRAINT proxy_observations_standard_check CHECK (
        standard_version IS NULL OR standard_version = '5.6.1'
    ),
    ADD CONSTRAINT proxy_observations_admin_identity_check CHECK (
        (admin_address IS NULL AND admin_code_hash IS NULL) OR
        (octet_length(admin_address) = 20 AND octet_length(admin_code_hash) = 32)
    ),
    ADD CONSTRAINT proxy_observations_beacon_code_hash_check CHECK (
        beacon_code_hash IS NULL OR octet_length(beacon_code_hash) = 32
    ),
    ADD CONSTRAINT proxy_observations_immutable_args_check CHECK (
        immutable_args IS NULL OR octet_length(immutable_args) <= 24531
    ),
    ADD CONSTRAINT proxy_observations_evidence_state_check CHECK (
        evidence_state IN ('exact', 'partial', 'generic')
    ),
    ADD CONSTRAINT proxy_observations_stage_version_check CHECK (
        stage_version IN (1, 2)
    ),
    ADD PRIMARY KEY (chain_id, proxy_address, block_hash, stage_version);

ALTER TABLE proxy_observations
    ALTER COLUMN stage_version SET DEFAULT 2;

CREATE INDEX proxy_observations_v2_history_idx
    ON proxy_observations (
        chain_id, proxy_address, block_number DESC, block_hash DESC
    ) WHERE canonical AND stage_version = 2;

CREATE INDEX proxy_observations_v2_beacon_idx
    ON proxy_observations (
        chain_id, beacon_address, block_number DESC, block_hash DESC
    ) WHERE canonical AND stage_version = 2 AND beacon_address IS NOT NULL;

CREATE TABLE beacon_implementation_observations (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    beacon_address BYTEA NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    beacon_code_hash BYTEA NOT NULL,
    implementation_address BYTEA NOT NULL,
    implementation_code_hash BYTEA NOT NULL,
    stage_version INTEGER NOT NULL DEFAULT 2,
    confidence TEXT NOT NULL,
    canonical BOOLEAN NOT NULL,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, beacon_address, block_hash, stage_version),
    FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash),
    CHECK (octet_length(beacon_address) = 20),
    CHECK (octet_length(block_hash) = 32),
    CHECK (octet_length(beacon_code_hash) = 32),
    CHECK (octet_length(implementation_address) = 20),
    CHECK (octet_length(implementation_code_hash) = 32),
    CHECK (stage_version = 2),
    CHECK (confidence IN ('verified', 'high', 'inferred', 'guess')),
    CHECK (jsonb_typeof(details) = 'object')
);

CREATE INDEX beacon_implementation_history_idx
    ON beacon_implementation_observations (
        chain_id, beacon_address, block_number DESC, block_hash DESC
    ) WHERE canonical;

CREATE TABLE proxy_upgrade_events (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    log_index BIGINT NOT NULL,
    transaction_hash BYTEA NOT NULL,
    emitter_address BYTEA NOT NULL,
    event_kind TEXT NOT NULL,
    target_address BYTEA NOT NULL,
    stage_version INTEGER NOT NULL DEFAULT 2,
    canonical BOOLEAN NOT NULL,
    PRIMARY KEY (chain_id, block_hash, log_index, stage_version),
    FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash),
    FOREIGN KEY (chain_id, block_number, block_hash, log_index)
        REFERENCES logs(chain_id, block_number, block_hash, log_index),
    CHECK (log_index >= 0),
    CHECK (octet_length(block_hash) = 32),
    CHECK (octet_length(transaction_hash) = 32),
    CHECK (octet_length(emitter_address) = 20),
    CHECK (event_kind IN ('implementation', 'beacon')),
    CHECK (octet_length(target_address) = 20),
    CHECK (stage_version = 2)
);

CREATE INDEX proxy_upgrade_events_emitter_idx
    ON proxy_upgrade_events (
        chain_id, emitter_address, block_number DESC, log_index DESC
    ) WHERE canonical AND stage_version = 2;

CREATE TABLE proxy_initialization_events (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    log_index BIGINT NOT NULL,
    transaction_hash BYTEA NOT NULL,
    contract_address BYTEA NOT NULL,
    version NUMERIC(20, 0) NOT NULL,
    stage_version INTEGER NOT NULL DEFAULT 2,
    canonical BOOLEAN NOT NULL,
    PRIMARY KEY (chain_id, block_hash, log_index, stage_version),
    FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash),
    FOREIGN KEY (chain_id, block_number, block_hash, log_index)
        REFERENCES logs(chain_id, block_number, block_hash, log_index),
    CHECK (log_index >= 0),
    CHECK (octet_length(block_hash) = 32),
    CHECK (octet_length(transaction_hash) = 32),
    CHECK (octet_length(contract_address) = 20),
    CHECK (version >= 0 AND version <= 18446744073709551615),
    CHECK (stage_version = 2)
);

CREATE INDEX proxy_initialization_events_contract_idx
    ON proxy_initialization_events (
        chain_id, contract_address, block_number DESC, log_index DESC
    ) WHERE canonical AND stage_version = 2;

-- Candidate-local failures are retained instead of being collapsed into one
-- block counter. Public readers can therefore distinguish an ordinary
-- non-proxy from a proxy-shaped address whose evidence was rejected.
CREATE TABLE proxy_detection_evidence (
    id BIGSERIAL PRIMARY KEY,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    address BYTEA NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    stage_version INTEGER NOT NULL DEFAULT 2,
    code_hash BYTEA NOT NULL,
    candidate_kind TEXT NOT NULL,
    detection_state TEXT NOT NULL,
    reason TEXT NOT NULL,
    canonical BOOLEAN NOT NULL,
    durable_job_id BIGINT REFERENCES durable_jobs(id),
    job_generation BIGINT,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE NULLS NOT DISTINCT (
        chain_id, address, block_hash, stage_version, candidate_kind,
        durable_job_id, job_generation
    ),
    FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash),
    CHECK (octet_length(address) = 20),
    CHECK (octet_length(block_hash) = 32),
    CHECK (octet_length(code_hash) = 32),
    CHECK (stage_version = 2),
    CHECK (candidate_kind IN ('proxy', 'beacon')),
    CHECK (detection_state IN ('not_detected', 'rejected')),
    CHECK (reason IN (
        'empty_code', 'not_proxy', 'minimal_zero_implementation',
        'immutable_args_too_large', 'immutable_args_creation_unverified',
        'self_implementation',
        'implementation_has_no_code', 'invalid_slot_address',
        'ambiguous_slots', 'beacon_has_no_code',
        'invalid_beacon_implementation'
    )),
    CHECK (
        (durable_job_id IS NULL AND job_generation IS NULL) OR
        (durable_job_id IS NOT NULL AND job_generation > 0)
    ),
    CHECK (jsonb_typeof(details) = 'object')
);

CREATE INDEX proxy_detection_evidence_current_idx
    ON proxy_detection_evidence (
        chain_id, address, block_number DESC, block_hash DESC
    ) WHERE canonical AND stage_version = 2;

-- Verification authenticates exact upstream source bytes before inserting an
-- artifact identity here. This is intentionally separate from the ordinary
-- verified-contract publication: a contract being verified does not imply it
-- is an OpenZeppelin management contract or proxy implementation.
CREATE TABLE verified_contract_proxy_artifacts (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    address BYTEA NOT NULL,
    code_hash BYTEA NOT NULL,
    valid_from_block NUMERIC(78, 0) NOT NULL,
    verification_job_id UUID NOT NULL UNIQUE,
    request_digest BYTEA NOT NULL,
    artifact_kind TEXT NOT NULL,
    standard_version TEXT NOT NULL,
    runtime_immutable_address BYTEA,
    source_manifest_sha256 BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (
        chain_id, address, code_hash, valid_from_block, verification_job_id
    ),
    FOREIGN KEY (
        chain_id, address, code_hash, valid_from_block, verification_job_id
    )
        REFERENCES verified_contracts (
            chain_id, address, code_hash, valid_from_block, verification_job_id
        ) ON DELETE RESTRICT,
    FOREIGN KEY (verification_job_id, request_digest)
        REFERENCES verification_results (job_id, request_digest)
        ON DELETE RESTRICT,
    CHECK (octet_length(address) = 20),
    CHECK (octet_length(code_hash) = 32),
    CHECK (valid_from_block >= 0),
    CHECK (octet_length(request_digest) = 32),
    CHECK (artifact_kind IN (
        'erc1967_proxy', 'transparent_proxy', 'beacon_proxy',
        'uups_implementation', 'proxy_admin', 'upgradeable_beacon'
    )),
    CHECK (standard_version = '5.6.1'),
    CHECK (
        (artifact_kind IN ('transparent_proxy', 'beacon_proxy', 'uups_implementation')
         AND runtime_immutable_address IS NOT NULL
         AND octet_length(runtime_immutable_address) = 20
         AND runtime_immutable_address <> decode(repeat('00', 20), 'hex'))
        OR
        (artifact_kind NOT IN ('transparent_proxy', 'beacon_proxy', 'uups_implementation')
         AND runtime_immutable_address IS NULL)
    ),
    CHECK (
        octet_length(source_manifest_sha256) = 32 AND
        source_manifest_sha256 <> decode(repeat('00', 32), 'hex')
    )
);

CREATE INDEX verified_contract_proxy_artifacts_current_idx
    ON verified_contract_proxy_artifacts (
        chain_id, address, valid_from_block DESC, code_hash
    );

-- A verification publication can happen in a block with no prior proxy
-- observation. Persist explicit targets so proxy@2 replay never depends on
-- rediscovering an address from old same-block output.
CREATE TABLE proxy_replay_targets (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    address BYTEA NOT NULL,
    target_kind TEXT NOT NULL,
    source_kind TEXT NOT NULL,
    source_verification_job_id UUID NOT NULL REFERENCES verification_jobs(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (
        chain_id, block_hash, address, target_kind,
        source_kind, source_verification_job_id
    ),
    FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash),
    CHECK (octet_length(block_hash) = 32),
    CHECK (octet_length(address) = 20),
    CHECK (target_kind IN ('proxy', 'beacon')),
    CHECK (source_kind = 'verification_publication')
);

CREATE INDEX proxy_replay_targets_verification_source_idx
    ON proxy_replay_targets (
        (source_verification_job_id::text), chain_id, block_hash
    );

-- A verification can create the first proxy@2 job for a block. Its replay
-- provenance belongs to generation 1 rather than an artificial empty
-- generation 2; later replays retain the existing monotonically increasing
-- source identity contract.
ALTER TABLE durable_job_replay_requests
    DROP CONSTRAINT IF EXISTS durable_job_replay_requests_requested_generation_check;
ALTER TABLE durable_job_replay_requests
    ADD CONSTRAINT durable_job_replay_requests_positive_generation_check
    CHECK (requested_generation >= 1);

-- Observations stay immutable across replay. These append-only generation
-- witnesses say which durable proxy@2 generation reproduced the same fact.
CREATE TABLE proxy_observation_generations (
    id BIGSERIAL PRIMARY KEY,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    proxy_address BYTEA NOT NULL,
    observation_block_hash BYTEA NOT NULL,
    observation_stage_version INTEGER NOT NULL,
    durable_job_id BIGINT REFERENCES durable_jobs(id),
    job_generation BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (
        chain_id, proxy_address, observation_block_hash,
        observation_stage_version
    ) REFERENCES proxy_observations (
        chain_id, proxy_address, block_hash, stage_version
    ) ON DELETE RESTRICT,
    UNIQUE NULLS NOT DISTINCT (
        chain_id, proxy_address, observation_block_hash,
        observation_stage_version, durable_job_id, job_generation
    ),
    CHECK (observation_stage_version = 2),
    CHECK (
        (durable_job_id IS NULL AND job_generation IS NULL) OR
        (durable_job_id IS NOT NULL AND job_generation > 0)
    )
);

CREATE TABLE beacon_observation_generations (
    id BIGSERIAL PRIMARY KEY,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    beacon_address BYTEA NOT NULL,
    observation_block_hash BYTEA NOT NULL,
    observation_stage_version INTEGER NOT NULL,
    durable_job_id BIGINT REFERENCES durable_jobs(id),
    job_generation BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (
        chain_id, beacon_address, observation_block_hash,
        observation_stage_version
    ) REFERENCES beacon_implementation_observations (
        chain_id, beacon_address, block_hash, stage_version
    ) ON DELETE RESTRICT,
    UNIQUE NULLS NOT DISTINCT (
        chain_id, beacon_address, observation_block_hash,
        observation_stage_version, durable_job_id, job_generation
    ),
    CHECK (observation_stage_version = 2),
    CHECK (
        (durable_job_id IS NULL AND job_generation IS NULL) OR
        (durable_job_id IS NOT NULL AND job_generation > 0)
    )
);

-- Raw slot observations are never rewritten when verification arrives. An
-- authenticated, fixed-block re-identification is appended here and is usable
-- only while its own durable proxy@2 generation is the published generation.
CREATE TABLE proxy_artifact_resolutions (
    id BIGSERIAL PRIMARY KEY,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    proxy_address BYTEA NOT NULL,
    observation_block_hash BYTEA NOT NULL,
    observation_stage_version INTEGER NOT NULL,
    proxy_code_hash BYTEA NOT NULL,
    proxy_kind TEXT NOT NULL,
    proxy_pattern TEXT NOT NULL,
    standard_version TEXT NOT NULL,
    implementation_address BYTEA NOT NULL,
    implementation_code_hash BYTEA NOT NULL,
    admin_address BYTEA,
    admin_code_hash BYTEA,
    beacon_address BYTEA,
    beacon_code_hash BYTEA,
    proxy_artifact_job_id UUID NOT NULL
        REFERENCES verified_contract_proxy_artifacts(verification_job_id),
    implementation_artifact_job_id UUID
        REFERENCES verified_contract_proxy_artifacts(verification_job_id),
    durable_job_id BIGINT REFERENCES durable_jobs(id),
    job_generation BIGINT,
    evidence JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (
        chain_id, proxy_address, observation_block_hash,
        observation_stage_version
    ) REFERENCES proxy_observations (
        chain_id, proxy_address, block_hash, stage_version
    ) ON DELETE RESTRICT,
    UNIQUE NULLS NOT DISTINCT (
        chain_id, proxy_address, observation_block_hash,
        observation_stage_version, durable_job_id, job_generation
    ),
    CHECK (observation_stage_version = 2),
    CHECK (octet_length(proxy_code_hash) = 32),
    CHECK (proxy_kind IN ('eip1967', 'beacon')),
    CHECK (proxy_pattern IN ('erc1967', 'transparent', 'uups', 'beacon')),
    CHECK (standard_version = '5.6.1'),
    CHECK (octet_length(implementation_address) = 20),
    CHECK (octet_length(implementation_code_hash) = 32),
    CHECK (
        (admin_address IS NULL AND admin_code_hash IS NULL) OR
        (octet_length(admin_address) = 20 AND octet_length(admin_code_hash) = 32)
    ),
    CHECK (
        (beacon_address IS NULL AND beacon_code_hash IS NULL) OR
        (octet_length(beacon_address) = 20 AND octet_length(beacon_code_hash) = 32)
    ),
    CHECK (
        (proxy_pattern = 'transparent' AND admin_address IS NOT NULL
         AND beacon_address IS NULL) OR
        (proxy_pattern = 'beacon' AND beacon_address IS NOT NULL
         AND admin_address IS NULL) OR
        (proxy_pattern IN ('erc1967', 'uups') AND admin_address IS NULL
         AND beacon_address IS NULL)
    ),
    CHECK (
        (proxy_pattern = 'uups' AND implementation_artifact_job_id IS NOT NULL) OR
        (proxy_pattern <> 'uups')
    ),
    CHECK (
        (durable_job_id IS NULL AND job_generation IS NULL) OR
        (durable_job_id IS NOT NULL AND job_generation > 0)
    ),
    CHECK (jsonb_typeof(evidence) = 'object')
);

CREATE INDEX proxy_artifact_resolutions_current_idx
    ON proxy_artifact_resolutions (
        chain_id, proxy_address, id DESC
    );

CREATE INDEX proxy_artifact_resolutions_beacon_idx
    ON proxy_artifact_resolutions (
        chain_id, beacon_address, observation_block_hash, id DESC
    ) WHERE beacon_address IS NOT NULL;

CREATE TABLE verified_proxy_bindings (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    proxy_address BYTEA NOT NULL,
    proxy_code_hash BYTEA NOT NULL,
    observation_block_number NUMERIC(78, 0) NOT NULL,
    observation_block_hash BYTEA NOT NULL,
    observation_stage_version INTEGER NOT NULL,
    proxy_kind TEXT NOT NULL,
    proxy_pattern TEXT NOT NULL,
    standard_version TEXT,
    implementation_address BYTEA NOT NULL,
    implementation_code_hash BYTEA NOT NULL,
    admin_address BYTEA,
    admin_code_hash BYTEA,
    beacon_address BYTEA,
    beacon_code_hash BYTEA,
    management_kind TEXT NOT NULL,
    management_address BYTEA,
    management_code_hash BYTEA,
    observation_generation_id BIGINT NOT NULL
        REFERENCES proxy_observation_generations(id),
    artifact_resolution_id BIGINT REFERENCES proxy_artifact_resolutions(id),
    beacon_generation_id BIGINT REFERENCES beacon_observation_generations(id),
    context_block_number NUMERIC(78, 0) NOT NULL,
    context_block_hash BYTEA NOT NULL,
    verification_job_id UUID NOT NULL,
    request_digest BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- The verification job UUID is the opaque binding identity. This permits
    -- A -> B -> A upgrades to create a fresh immutable binding.
    PRIMARY KEY (verification_job_id),
    CONSTRAINT verified_proxy_bindings_observation_fk FOREIGN KEY (
        chain_id, proxy_address, observation_block_hash,
        observation_stage_version
    ) REFERENCES proxy_observations (
        chain_id, proxy_address, block_hash, stage_version
    ) ON DELETE RESTRICT,
    CONSTRAINT verified_proxy_bindings_result_fk FOREIGN KEY (
        verification_job_id, request_digest
    ) REFERENCES verification_results (
        job_id, request_digest
    ) ON DELETE RESTRICT,
    CHECK (octet_length(proxy_address) = 20),
    CHECK (octet_length(proxy_code_hash) = 32),
    CHECK (observation_block_number >= 0),
    CHECK (octet_length(observation_block_hash) = 32),
    CHECK (observation_stage_version = 2),
    CHECK (proxy_kind IN ('eip1167', 'eip1967', 'beacon')),
    CHECK (proxy_pattern IN ('clone', 'erc1967', 'transparent', 'uups', 'beacon')),
    CHECK (standard_version IS NULL OR standard_version = '5.6.1'),
    CHECK (octet_length(implementation_address) = 20),
    CHECK (octet_length(implementation_code_hash) = 32),
    CHECK (
        (admin_address IS NULL AND admin_code_hash IS NULL) OR
        (octet_length(admin_address) = 20 AND octet_length(admin_code_hash) = 32)
    ),
    CHECK (
        (beacon_address IS NULL AND beacon_code_hash IS NULL) OR
        (octet_length(beacon_address) = 20 AND octet_length(beacon_code_hash) = 32)
    ),
    CHECK (management_kind IN ('none', 'proxy_admin', 'upgradeable_beacon')),
    CHECK (
        (management_kind = 'none' AND management_address IS NULL AND management_code_hash IS NULL) OR
        (management_kind <> 'none' AND octet_length(management_address) = 20 AND
            octet_length(management_code_hash) = 32)
    ),
    CONSTRAINT verified_proxy_bindings_management_semantics CHECK (
        (
            proxy_pattern = 'transparent'
            AND proxy_kind = 'eip1967'
            AND admin_address IS NOT NULL
            AND admin_code_hash IS NOT NULL
            AND beacon_address IS NULL
            AND beacon_code_hash IS NULL
            AND management_kind = 'proxy_admin'
            AND management_address IS NOT DISTINCT FROM admin_address
            AND management_code_hash IS NOT DISTINCT FROM admin_code_hash
        ) OR (
            proxy_pattern = 'beacon'
            AND proxy_kind = 'beacon'
            AND admin_address IS NULL
            AND admin_code_hash IS NULL
            AND beacon_address IS NOT NULL
            AND beacon_code_hash IS NOT NULL
            AND management_kind = 'upgradeable_beacon'
            AND management_address IS NOT DISTINCT FROM beacon_address
            AND management_code_hash IS NOT DISTINCT FROM beacon_code_hash
        ) OR (
            proxy_pattern = 'clone'
            AND proxy_kind = 'eip1167'
            AND admin_address IS NULL
            AND admin_code_hash IS NULL
            AND beacon_address IS NULL
            AND beacon_code_hash IS NULL
            AND management_kind = 'none'
        ) OR (
            proxy_pattern IN ('erc1967', 'uups')
            AND proxy_kind = 'eip1967'
            AND admin_address IS NULL
            AND admin_code_hash IS NULL
            AND beacon_address IS NULL
            AND beacon_code_hash IS NULL
            AND management_kind = 'none'
        )
    ),
    CHECK (
        (proxy_pattern = 'clone' AND artifact_resolution_id IS NULL
         AND standard_version IS NULL) OR
        (proxy_pattern <> 'clone' AND artifact_resolution_id IS NOT NULL
         AND standard_version = '5.6.1')
    ),
    CHECK (
        (proxy_pattern = 'beacon' AND beacon_generation_id IS NOT NULL) OR
        (proxy_pattern <> 'beacon' AND beacon_generation_id IS NULL)
    ),
    CHECK (context_block_number >= observation_block_number),
    CHECK (octet_length(context_block_hash) = 32),
    CHECK (octet_length(request_digest) = 32)
);

CREATE INDEX verified_proxy_bindings_current_idx
    ON verified_proxy_bindings (
        chain_id, proxy_address, observation_block_number DESC
    );

CREATE OR REPLACE FUNCTION reject_verified_proxy_binding_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'verified proxy bindings are immutable';
END
$$;

CREATE TRIGGER verified_proxy_bindings_immutable
BEFORE UPDATE OR DELETE ON verified_proxy_bindings
FOR EACH ROW EXECUTE FUNCTION reject_verified_proxy_binding_mutation();

CREATE OR REPLACE FUNCTION enforce_verified_proxy_binding()
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
         AND observation.stage_version = NEW.observation_stage_version
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
          AND (
              NEW.proxy_pattern <> 'clone' OR (
                  observation.proxy_kind = NEW.proxy_kind
                  AND observation.proxy_pattern = 'clone'
                  AND observation.evidence_state = 'exact'
                  AND observation.implementation_address = NEW.implementation_address
                  AND observation.implementation_code_hash = NEW.implementation_code_hash
              )
          )
          AND observation.canonical = TRUE
          AND observation.confidence IN ('verified', 'high')
          AND result.outcome->>'proxy_address' =
              '0x' || encode(NEW.proxy_address, 'hex')
          AND result.outcome->>'proxy_code_hash' =
              '0x' || encode(NEW.proxy_code_hash, 'hex')
          AND result.outcome->>'observation_block_hash' =
              '0x' || encode(NEW.observation_block_hash, 'hex')
          AND (result.outcome->>'observation_stage_version')::integer =
              NEW.observation_stage_version
          AND result.outcome->>'proxy_kind' = NEW.proxy_kind
          AND result.outcome->>'proxy_pattern' = NEW.proxy_pattern
          AND result.outcome->>'standard_version' IS NOT DISTINCT FROM
              NEW.standard_version
          AND result.outcome->>'management_kind' = NEW.management_kind
          AND result.outcome->>'implementation_address' =
              '0x' || encode(NEW.implementation_address, 'hex')
          AND result.outcome->>'implementation_code_hash' =
              '0x' || encode(NEW.implementation_code_hash, 'hex')
          AND result.outcome->>'admin_address' IS NOT DISTINCT FROM
              CASE WHEN NEW.admin_address IS NULL THEN NULL
                   ELSE '0x' || encode(NEW.admin_address, 'hex') END
          AND result.outcome->>'admin_code_hash' IS NOT DISTINCT FROM
              CASE WHEN NEW.admin_code_hash IS NULL THEN NULL
                   ELSE '0x' || encode(NEW.admin_code_hash, 'hex') END
          AND result.outcome->>'beacon_address' IS NOT DISTINCT FROM
              CASE WHEN NEW.beacon_address IS NULL THEN NULL
                   ELSE '0x' || encode(NEW.beacon_address, 'hex') END
          AND result.outcome->>'beacon_code_hash' IS NOT DISTINCT FROM
              CASE WHEN NEW.beacon_code_hash IS NULL THEN NULL
                   ELSE '0x' || encode(NEW.beacon_code_hash, 'hex') END
          AND result.outcome->>'management_address' IS NOT DISTINCT FROM
              CASE WHEN NEW.management_address IS NULL THEN NULL
                   ELSE '0x' || encode(NEW.management_address, 'hex') END
          AND result.outcome->>'management_code_hash' IS NOT DISTINCT FROM
              CASE WHEN NEW.management_code_hash IS NULL THEN NULL
                   ELSE '0x' || encode(NEW.management_code_hash, 'hex') END
          AND (result.outcome->>'observation_generation_id')::bigint =
              NEW.observation_generation_id
          AND (result.outcome->>'artifact_resolution_id')::bigint
              IS NOT DISTINCT FROM NEW.artifact_resolution_id
          AND (result.outcome->>'beacon_generation_id')::bigint
              IS NOT DISTINCT FROM NEW.beacon_generation_id
          AND (result.outcome->>'context_block_number')::numeric =
              NEW.context_block_number
          AND result.outcome->>'context_block_hash' =
              '0x' || encode(NEW.context_block_hash, 'hex')
    ) THEN
        RAISE EXCEPTION 'verified proxy binding disagrees with its immutable result';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM canonical_blocks AS context
        WHERE context.chain_id = NEW.chain_id
          AND context.number = NEW.context_block_number
          AND context.block_hash = NEW.context_block_hash
          AND NOT EXISTS (
              SELECT 1
              FROM canonical_blocks AS newer
              WHERE newer.chain_id = NEW.chain_id
                AND newer.number > context.number
          )
    ) THEN
        RAISE EXCEPTION 'proxy binding context is not the canonical tip';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM proxy_observation_generations AS generation
        JOIN published_block_stage_results AS published
          ON published.chain_id = generation.chain_id
         AND published.block_hash = generation.observation_block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = generation.observation_stage_version
         AND published.durable_job_id = generation.durable_job_id
         AND published.job_generation = generation.job_generation
        WHERE generation.id = NEW.observation_generation_id
          AND generation.chain_id = NEW.chain_id
          AND generation.proxy_address = NEW.proxy_address
          AND generation.observation_block_hash = NEW.observation_block_hash
    ) THEN
        RAISE EXCEPTION 'proxy observation generation is not published';
    END IF;

    IF NEW.artifact_resolution_id IS NOT NULL AND NOT EXISTS (
        SELECT 1
        FROM proxy_artifact_resolutions AS resolution
        JOIN published_block_stage_results AS published
          ON published.chain_id = resolution.chain_id
         AND published.block_hash = resolution.observation_block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = resolution.observation_stage_version
         AND published.durable_job_id = resolution.durable_job_id
         AND published.job_generation = resolution.job_generation
        WHERE resolution.id = NEW.artifact_resolution_id
          AND resolution.chain_id = NEW.chain_id
          AND resolution.proxy_address = NEW.proxy_address
          AND resolution.observation_block_hash = NEW.observation_block_hash
          AND resolution.proxy_code_hash = NEW.proxy_code_hash
          AND resolution.proxy_kind = NEW.proxy_kind
          AND resolution.proxy_pattern = NEW.proxy_pattern
          AND resolution.standard_version = NEW.standard_version
          AND (
              NEW.proxy_pattern = 'beacon' OR (
                  resolution.implementation_address = NEW.implementation_address
                  AND resolution.implementation_code_hash = NEW.implementation_code_hash
              )
          )
          AND resolution.admin_address IS NOT DISTINCT FROM NEW.admin_address
          AND resolution.admin_code_hash IS NOT DISTINCT FROM NEW.admin_code_hash
          AND resolution.beacon_address IS NOT DISTINCT FROM NEW.beacon_address
          AND resolution.beacon_code_hash IS NOT DISTINCT FROM NEW.beacon_code_hash
    ) THEN
        RAISE EXCEPTION 'proxy artifact resolution is not published';
    END IF;

    IF NEW.beacon_generation_id IS NOT NULL AND NOT EXISTS (
        SELECT 1
        FROM beacon_observation_generations AS generation
        JOIN beacon_implementation_observations AS observation
          ON observation.chain_id = generation.chain_id
         AND observation.beacon_address = generation.beacon_address
         AND observation.block_hash = generation.observation_block_hash
         AND observation.stage_version = generation.observation_stage_version
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = observation.chain_id
         AND canonical.number = observation.block_number
         AND canonical.block_hash = observation.block_hash
        JOIN published_block_stage_results AS published
          ON published.chain_id = generation.chain_id
         AND published.block_hash = generation.observation_block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = generation.observation_stage_version
         AND published.durable_job_id = generation.durable_job_id
         AND published.job_generation = generation.job_generation
        WHERE generation.id = NEW.beacon_generation_id
          AND generation.chain_id = NEW.chain_id
          AND generation.beacon_address = NEW.beacon_address
          AND observation.beacon_code_hash = NEW.beacon_code_hash
          AND observation.implementation_address = NEW.implementation_address
          AND observation.implementation_code_hash = NEW.implementation_code_hash
          AND observation.canonical = TRUE
          AND observation.confidence IN ('verified', 'high')
          AND observation.block_number <= NEW.context_block_number
          AND NOT EXISTS (
              SELECT 1
              FROM beacon_implementation_observations AS newer_observation
              JOIN canonical_blocks AS newer_canonical
                ON newer_canonical.chain_id = newer_observation.chain_id
               AND newer_canonical.number = newer_observation.block_number
               AND newer_canonical.block_hash = newer_observation.block_hash
              JOIN beacon_observation_generations AS newer_generation
                ON newer_generation.chain_id = newer_observation.chain_id
               AND newer_generation.beacon_address = newer_observation.beacon_address
               AND newer_generation.observation_block_hash = newer_observation.block_hash
               AND newer_generation.observation_stage_version = newer_observation.stage_version
              JOIN published_block_stage_results AS newer_published
                ON newer_published.chain_id = newer_generation.chain_id
               AND newer_published.block_hash = newer_generation.observation_block_hash
               AND newer_published.stage = 'proxy'
               AND newer_published.stage_version = newer_generation.observation_stage_version
               AND newer_published.durable_job_id = newer_generation.durable_job_id
               AND newer_published.job_generation = newer_generation.job_generation
              WHERE newer_observation.chain_id = observation.chain_id
                AND newer_observation.beacon_address = observation.beacon_address
                AND newer_observation.beacon_code_hash = NEW.beacon_code_hash
                AND newer_observation.stage_version = 2
                AND newer_observation.canonical = TRUE
                AND newer_observation.confidence IN ('verified', 'high')
                AND newer_observation.block_number <= NEW.context_block_number
                AND (
                    newer_observation.block_number > observation.block_number OR (
                        newer_observation.block_number = observation.block_number
                        AND newer_generation.id > generation.id
                    )
                )
          )
    ) THEN
        RAISE EXCEPTION 'beacon implementation generation is not published';
    END IF;

    IF NEW.management_kind <> 'none' AND NOT EXISTS (
        SELECT 1
        FROM verified_contract_proxy_artifacts AS artifact
        JOIN verified_contracts AS verified
          ON verified.chain_id = artifact.chain_id
         AND verified.address = artifact.address
         AND verified.code_hash = artifact.code_hash
         AND verified.valid_from_block = artifact.valid_from_block
         AND verified.verification_job_id = artifact.verification_job_id
         AND verified.request_digest = artifact.request_digest
        WHERE artifact.chain_id = NEW.chain_id
          AND artifact.address = NEW.management_address
          AND artifact.code_hash = NEW.management_code_hash
          AND artifact.standard_version = '5.6.1'
          AND artifact.artifact_kind = CASE NEW.management_kind
              WHEN 'proxy_admin' THEN 'proxy_admin'
              WHEN 'upgradeable_beacon' THEN 'upgradeable_beacon'
          END
          AND artifact.valid_from_block <= NEW.context_block_number
          AND (verified.valid_to_block IS NULL OR
               verified.valid_to_block >= NEW.context_block_number)
    ) THEN
        RAISE EXCEPTION 'proxy management contract is not authenticated OpenZeppelin 5.6.1';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER verified_proxy_bindings_source_guard
BEFORE INSERT ON verified_proxy_bindings
FOR EACH ROW EXECUTE FUNCTION enforce_verified_proxy_binding();

CREATE OR REPLACE FUNCTION reject_proxy_v2_evidence_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'proxy v2 evidence is append-only';
END
$$;

CREATE TRIGGER verified_contract_proxy_artifacts_immutable
BEFORE UPDATE OR DELETE ON verified_contract_proxy_artifacts
FOR EACH ROW EXECUTE FUNCTION reject_proxy_v2_evidence_mutation();

CREATE TRIGGER proxy_replay_targets_immutable
BEFORE UPDATE OR DELETE ON proxy_replay_targets
FOR EACH ROW EXECUTE FUNCTION reject_proxy_v2_evidence_mutation();

CREATE TRIGGER proxy_observation_generations_immutable
BEFORE UPDATE OR DELETE ON proxy_observation_generations
FOR EACH ROW EXECUTE FUNCTION reject_proxy_v2_evidence_mutation();

CREATE TRIGGER beacon_observation_generations_immutable
BEFORE UPDATE OR DELETE ON beacon_observation_generations
FOR EACH ROW EXECUTE FUNCTION reject_proxy_v2_evidence_mutation();

CREATE TRIGGER proxy_artifact_resolutions_immutable
BEFORE UPDATE OR DELETE ON proxy_artifact_resolutions
FOR EACH ROW EXECUTE FUNCTION reject_proxy_v2_evidence_mutation();

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
          AND job.kind = 'address'
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

CREATE TRIGGER verified_contract_proxy_artifacts_source_guard
BEFORE INSERT ON verified_contract_proxy_artifacts
FOR EACH ROW EXECUTE FUNCTION enforce_verified_contract_proxy_artifact();

CREATE OR REPLACE FUNCTION enforce_proxy_observation_generation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.durable_job_id IS NOT NULL AND NOT EXISTS (
        SELECT 1
        FROM durable_jobs AS job
        WHERE job.id = NEW.durable_job_id
          AND job.chain_id = NEW.chain_id
          AND job.stage = 'proxy'
          AND job.stage_version = NEW.observation_stage_version
          AND job.payload->>'block_hash' =
              '0x' || encode(NEW.observation_block_hash, 'hex')
          AND job.leased_generation = NEW.job_generation
          AND job.status = 'leased'
    ) THEN
        RAISE EXCEPTION 'proxy observation generation is not the active proxy@2 lease';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER proxy_observation_generations_source_guard
BEFORE INSERT ON proxy_observation_generations
FOR EACH ROW EXECUTE FUNCTION enforce_proxy_observation_generation();

CREATE TRIGGER beacon_observation_generations_source_guard
BEFORE INSERT ON beacon_observation_generations
FOR EACH ROW EXECUTE FUNCTION enforce_proxy_observation_generation();

CREATE OR REPLACE FUNCTION enforce_proxy_detection_generation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.durable_job_id IS NOT NULL AND NOT EXISTS (
        SELECT 1
        FROM durable_jobs AS job
        WHERE job.id = NEW.durable_job_id
          AND job.chain_id = NEW.chain_id
          AND job.stage = 'proxy'
          AND job.stage_version = NEW.stage_version
          AND job.payload->>'block_hash' =
              '0x' || encode(NEW.block_hash, 'hex')
          AND job.leased_generation = NEW.job_generation
          AND job.status = 'leased'
    ) THEN
        RAISE EXCEPTION 'proxy detection evidence is not from the active proxy@2 lease';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER proxy_detection_evidence_source_guard
BEFORE INSERT ON proxy_detection_evidence
FOR EACH ROW EXECUTE FUNCTION enforce_proxy_detection_generation();

CREATE OR REPLACE FUNCTION enforce_proxy_artifact_resolution()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    expected_proxy_artifact TEXT;
BEGIN
    IF NEW.proxy_pattern = 'uups' THEN
        RAISE EXCEPTION 'UUPS compatibility is shared implementation evidence, not a proxy artifact resolution';
    END IF;
    expected_proxy_artifact := CASE NEW.proxy_pattern
        WHEN 'transparent' THEN 'transparent_proxy'
        WHEN 'beacon' THEN 'beacon_proxy'
        ELSE 'erc1967_proxy'
    END;
    IF NOT EXISTS (
        SELECT 1
        FROM verified_contract_proxy_artifacts AS artifact
        JOIN proxy_observations AS observation
          ON observation.chain_id = NEW.chain_id
         AND observation.proxy_address = NEW.proxy_address
         AND observation.block_hash = NEW.observation_block_hash
         AND observation.stage_version = NEW.observation_stage_version
        WHERE artifact.verification_job_id = NEW.proxy_artifact_job_id
          AND artifact.chain_id = NEW.chain_id
          AND artifact.address = NEW.proxy_address
          AND artifact.code_hash = NEW.proxy_code_hash
          AND observation.proxy_code_hash = NEW.proxy_code_hash
          AND artifact.artifact_kind = expected_proxy_artifact
          AND artifact.standard_version = NEW.standard_version
          AND (
              (NEW.proxy_pattern = 'transparent'
               AND artifact.runtime_immutable_address = NEW.admin_address) OR
              (NEW.proxy_pattern = 'beacon'
               AND artifact.runtime_immutable_address = NEW.beacon_address) OR
              (NEW.proxy_pattern = 'erc1967'
               AND artifact.runtime_immutable_address IS NULL)
          )
    ) THEN
        RAISE EXCEPTION 'proxy artifact resolution lacks exact proxy evidence';
    END IF;
    IF NEW.durable_job_id IS NOT NULL AND NOT EXISTS (
        SELECT 1
        FROM durable_jobs AS job
        WHERE job.id = NEW.durable_job_id
          AND job.chain_id = NEW.chain_id
          AND job.stage = 'proxy'
          AND job.stage_version = 2
          AND job.payload->>'block_hash' =
              '0x' || encode(NEW.observation_block_hash, 'hex')
          AND job.leased_generation = NEW.job_generation
          AND job.status = 'leased'
    ) THEN
        RAISE EXCEPTION 'proxy artifact resolution generation is not the active proxy@2 lease';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER proxy_artifact_resolutions_source_guard
BEFORE INSERT ON proxy_artifact_resolutions
FOR EACH ROW EXECUTE FUNCTION enforce_proxy_artifact_resolution();

DO $migration$
DECLARE
    migration_schema TEXT := current_schema();
    function_name TEXT;
BEGIN
    IF migration_schema IS NULL THEN
        RAISE EXCEPTION 'OpenZeppelin proxy interaction migration requires a current schema';
    END IF;
    FOREACH function_name IN ARRAY ARRAY[
        'reject_verified_proxy_binding_mutation',
        'enforce_verified_proxy_binding',
        'record_verified_contract_search_catalog_document',
        'reject_proxy_v2_evidence_mutation',
        'enforce_verified_contract_proxy_artifact',
        'enforce_proxy_observation_generation',
        'enforce_proxy_detection_generation',
        'enforce_proxy_artifact_resolution'
    ]
    LOOP
        EXECUTE format(
            'ALTER FUNCTION %I.%I() SET search_path = %I, pg_catalog',
            migration_schema, function_name, migration_schema
        );
    END LOOP;
END
$migration$;
