-- P65 writer-authoritative user authentication.
CREATE TABLE users (
    id UUID PRIMARY KEY,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    address BYTEA NOT NULL,
    display_name TEXT,
    role TEXT NOT NULL DEFAULT 'user',
    status TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    last_login_at TIMESTAMPTZ,
    UNIQUE (chain_id, address),
    CHECK (octet_length(address) = 20),
    CHECK (display_name IS NULL OR (
        char_length(display_name) BETWEEN 1 AND 64
        AND octet_length(display_name) <= 256
        AND display_name !~ '[[:cntrl:]]'
    )),
    CHECK (role IN ('user', 'admin')),
    CHECK (status IN ('active', 'disabled'))
);

CREATE INDEX users_chain_created_idx
    ON users (chain_id, created_at DESC, id DESC);

CREATE TABLE auth_challenges (
    id UUID PRIMARY KEY,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    address BYTEA NOT NULL,
    message TEXT NOT NULL,
    nonce TEXT NOT NULL,
    issued_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    UNIQUE (chain_id, nonce),
    CHECK (octet_length(address) = 20),
    CHECK (octet_length(message) BETWEEN 1 AND 4096),
    CHECK (char_length(nonce) BETWEEN 8 AND 64),
    CHECK (nonce ~ '^[A-Za-z0-9]+$'),
    CHECK (expires_at > issued_at),
    CHECK (consumed_at IS NULL OR consumed_at >= issued_at)
);

CREATE INDEX auth_challenges_active_idx
    ON auth_challenges (chain_id, address, expires_at, id)
    WHERE consumed_at IS NULL;

CREATE INDEX auth_challenges_cleanup_idx
    ON auth_challenges (expires_at, id);

CREATE TABLE user_sessions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id),
    token_digest BYTEA NOT NULL UNIQUE,
    csrf_digest BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    CHECK (octet_length(token_digest) = 32),
    CHECK (octet_length(csrf_digest) = 32),
    CHECK (expires_at > created_at),
    CHECK (last_used_at >= created_at),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE INDEX user_sessions_active_user_idx
    ON user_sessions (user_id, expires_at, id)
    WHERE revoked_at IS NULL;

CREATE INDEX user_sessions_cleanup_idx
    ON user_sessions (expires_at, id);
