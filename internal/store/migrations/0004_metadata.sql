-- External metadata is optional enrichment. The durable job row owns retry and
-- lease semantics; this table is the queryable, auditable state for the latest
-- canonical token/NFT identity.
ALTER TABLE external_metadata
    DROP CONSTRAINT IF EXISTS external_metadata_state_check;

UPDATE external_metadata
SET state = CASE state
    WHEN 'complete' THEN 'available'
    WHEN 'failed' THEN 'error'
    ELSE state
END;

ALTER TABLE external_metadata
    ADD COLUMN IF NOT EXISTS token_address BYTEA,
    ADD COLUMN IF NOT EXISTS token_id NUMERIC(78, 0),
    ADD COLUMN IF NOT EXISTS observed_block_number NUMERIC(78, 0),
    ADD COLUMN IF NOT EXISTS observed_block_hash BYTEA,
    ADD COLUMN IF NOT EXISTS content_size BIGINT,
    ADD COLUMN IF NOT EXISTS attempt_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_error_code TEXT,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN IF NOT EXISTS terminal_at TIMESTAMPTZ;

UPDATE external_metadata
SET content_size = octet_length(convert_to(document::text, 'UTF8'))
WHERE document IS NOT NULL AND content_size IS NULL;

ALTER TABLE external_metadata
    ADD CONSTRAINT external_metadata_state_check
        CHECK (state IN ('pending', 'available', 'unavailable', 'unsafe', 'error')),
    ADD CONSTRAINT external_metadata_token_address_check
        CHECK (token_address IS NULL OR octet_length(token_address) = 20),
    ADD CONSTRAINT external_metadata_token_id_check
        CHECK (token_id IS NULL OR token_id >= 0),
    ADD CONSTRAINT external_metadata_observed_hash_check
        CHECK (observed_block_hash IS NULL OR octet_length(observed_block_hash) = 32),
    ADD CONSTRAINT external_metadata_observation_pair_check
        CHECK ((observed_block_number IS NULL) = (observed_block_hash IS NULL)),
    ADD CONSTRAINT external_metadata_nft_identity_check
        CHECK (
            resource_kind <> 'nft'
            OR (token_address IS NULL AND token_id IS NULL AND observed_block_number IS NULL)
            OR (token_address IS NOT NULL AND token_id IS NOT NULL AND observed_block_number IS NOT NULL)
        ),
    ADD CONSTRAINT external_metadata_content_size_check
        CHECK (content_size IS NULL OR content_size BETWEEN 0 AND 2097152),
    ADD CONSTRAINT external_metadata_attempt_count_check
        CHECK (attempt_count >= 0),
    ADD CONSTRAINT external_metadata_error_code_check
        CHECK (last_error_code IS NULL OR length(last_error_code) BETWEEN 1 AND 64),
    ADD CONSTRAINT external_metadata_observed_block_fk
        FOREIGN KEY (chain_id, observed_block_number, observed_block_hash)
        REFERENCES blocks(chain_id, number, hash);

CREATE UNIQUE INDEX IF NOT EXISTS external_metadata_nft_identity_uq
    ON external_metadata (chain_id, token_address, token_id)
    WHERE resource_kind = 'nft' AND token_address IS NOT NULL AND token_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS external_metadata_pending_idx
    ON external_metadata (chain_id, updated_at, resource_key)
    WHERE state = 'pending';

CREATE TABLE IF NOT EXISTS external_metadata_attempts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    resource_kind TEXT NOT NULL,
    resource_key TEXT NOT NULL,
    durable_job_id BIGINT NOT NULL REFERENCES durable_jobs(id),
    attempt INTEGER NOT NULL,
    state TEXT NOT NULL,
    source_uri TEXT NOT NULL,
    resolved_uri TEXT,
    media_type TEXT,
    content_hash BYTEA,
    content_size BIGINT,
    error_code TEXT,
    error_message TEXT,
    attempted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (resource_kind IN ('token', 'nft', 'name')),
    CHECK (attempt > 0),
    CHECK (state IN ('available', 'unavailable', 'unsafe', 'error')),
    CHECK (content_hash IS NULL OR octet_length(content_hash) = 32),
    CHECK (content_size IS NULL OR content_size BETWEEN 0 AND 2097152),
    CHECK (error_code IS NULL OR length(error_code) BETWEEN 1 AND 64),
    CHECK (error_message IS NULL OR length(error_message) <= 1024),
    UNIQUE (durable_job_id, attempt)
);

CREATE INDEX IF NOT EXISTS external_metadata_attempts_resource_idx
    ON external_metadata_attempts (chain_id, resource_kind, resource_key, attempted_at DESC);

ALTER TABLE durable_jobs
    ADD CONSTRAINT durable_jobs_metadata_payload_size_check
        CHECK (kind <> 'metadata' OR octet_length(payload::text) <= 8192),
    ADD CONSTRAINT durable_jobs_metadata_stage_check
        CHECK (
            kind <> 'metadata'
            OR (stage = 'nft-metadata' AND stage_version = 1)
        );
