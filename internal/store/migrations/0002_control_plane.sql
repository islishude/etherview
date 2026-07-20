CREATE TABLE IF NOT EXISTS durable_jobs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    kind TEXT NOT NULL,
    stage TEXT NOT NULL,
    stage_version INTEGER NOT NULL DEFAULT 1,
    idempotency_key TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    priority INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'queued',
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 10,
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    leased_by TEXT,
    lease_token TEXT,
    lease_expires_at TIMESTAMPTZ,
    result JSONB,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (chain_id, kind, idempotency_key),
    CHECK (stage_version > 0),
    CHECK (status IN ('queued', 'leased', 'succeeded', 'failed', 'cancelled')),
    CHECK (attempts >= 0 AND max_attempts > 0),
    CHECK ((leased_by IS NULL) = (lease_expires_at IS NULL)),
    CHECK ((leased_by IS NULL) = (lease_token IS NULL))
);

CREATE INDEX IF NOT EXISTS durable_jobs_claim_idx
    ON durable_jobs (chain_id, stage, priority DESC, available_at, id)
    WHERE status IN ('queued', 'leased');

CREATE TABLE IF NOT EXISTS transactional_outbox (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    topic TEXT NOT NULL,
    message_key TEXT NOT NULL,
    payload JSONB NOT NULL,
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (chain_id, topic, message_key),
    CHECK (attempts >= 0)
);

CREATE INDEX IF NOT EXISTS transactional_outbox_pending_idx
    ON transactional_outbox (available_at, id)
    WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS api_keys (
    prefix TEXT PRIMARY KEY,
    digest BYTEA NOT NULL,
    name TEXT NOT NULL,
    rate_per_second INTEGER NOT NULL,
    burst INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    CHECK (length(prefix) = 10),
    CHECK (octet_length(digest) = 32),
    CHECK (rate_per_second > 0),
    CHECK (burst >= rate_per_second)
);

CREATE TABLE IF NOT EXISTS operator_labels (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    object_kind TEXT NOT NULL,
    object_key TEXT NOT NULL,
    label TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (chain_id, object_kind, object_key),
    CHECK (object_kind IN ('address', 'block', 'transaction', 'token', 'contract')),
    CHECK (length(object_key) BETWEEN 1 AND 256),
    CHECK (length(label) BETWEEN 1 AND 256)
);

CREATE TABLE IF NOT EXISTS repair_requests (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    operation TEXT NOT NULL,
    stage TEXT NOT NULL,
    from_block NUMERIC(78, 0) NOT NULL,
    to_block NUMERIC(78, 0) NOT NULL,
    allow_finalized BOOLEAN NOT NULL DEFAULT false,
    reason TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued',
    requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    last_error TEXT,
    CHECK (operation IN ('repair', 'reindex')),
    CHECK (status IN ('queued', 'running', 'done', 'failed', 'cancelled')),
    CHECK (from_block >= 0 AND to_block >= from_block),
    CHECK (length(reason) BETWEEN 1 AND 1024)
);

CREATE INDEX IF NOT EXISTS repair_requests_pending_idx
    ON repair_requests (chain_id, requested_at, id)
    WHERE status = 'queued';

CREATE TABLE IF NOT EXISTS verification_jobs (
    id UUID PRIMARY KEY,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    address BYTEA NOT NULL,
    code_hash BYTEA NOT NULL,
    block_hash BYTEA NOT NULL,
    language TEXT NOT NULL,
    compiler_version TEXT NOT NULL,
    request JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued',
    leased_by TEXT,
    lease_token TEXT,
    lease_expires_at TIMESTAMPTZ,
    result_kind TEXT,
    result JSONB,
    error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE (chain_id, address, code_hash, block_hash),
    CHECK (octet_length(address) = 20),
    CHECK (octet_length(code_hash) = 32),
    CHECK (octet_length(block_hash) = 32),
    CHECK (language IN ('solidity', 'vyper')),
    CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
	CHECK ((leased_by IS NULL) = (lease_token IS NULL)),
	CHECK ((leased_by IS NULL) = (lease_expires_at IS NULL)),
	CHECK ((status = 'running') = (leased_by IS NOT NULL)),
    CHECK (result_kind IS NULL OR result_kind IN ('exact', 'metadata_only', 'mismatch'))
);

CREATE INDEX IF NOT EXISTS verification_jobs_target_idx
    ON verification_jobs (chain_id, address, code_hash, created_at DESC);

CREATE INDEX IF NOT EXISTS verification_jobs_claim_idx
    ON verification_jobs (status, created_at, id)
    WHERE status IN ('queued', 'running');

CREATE TABLE IF NOT EXISTS verified_contracts (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    address BYTEA NOT NULL,
    code_hash BYTEA NOT NULL,
    valid_from_block NUMERIC(78, 0) NOT NULL,
    valid_to_block NUMERIC(78, 0),
    language TEXT NOT NULL,
    compiler_version TEXT NOT NULL,
    match_kind TEXT NOT NULL,
    contract_name TEXT NOT NULL,
    abi JSONB,
    sources JSONB NOT NULL,
    settings JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, address, code_hash, valid_from_block),
    CHECK (octet_length(address) = 20),
    CHECK (octet_length(code_hash) = 32),
    CHECK (valid_from_block >= 0),
    CHECK (valid_to_block IS NULL OR valid_to_block >= valid_from_block),
    CHECK (match_kind IN ('exact', 'metadata_only'))
);
