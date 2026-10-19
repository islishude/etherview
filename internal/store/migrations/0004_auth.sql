-- Fresh-database baseline: auth.

-- Tables

CREATE TABLE users (
    id uuid NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    address bytea NOT NULL,
    display_name text,
    role text DEFAULT 'user'::text NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    last_login_at timestamp with time zone,
    CONSTRAINT users_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT users_display_name_check CHECK (((display_name IS NULL) OR ((char_length(display_name) BETWEEN 1 AND 64) AND (octet_length(display_name) <= 256) AND (display_name !~ '[[:cntrl:]]'::text)))),
    CONSTRAINT users_role_check CHECK ((role = ANY (ARRAY['user'::text, 'admin'::text]))),
    CONSTRAINT users_status_check CHECK ((status = ANY (ARRAY['active'::text, 'disabled'::text]))),
    CONSTRAINT users_chain_id_address_key UNIQUE (chain_id, address),
    CONSTRAINT users_pkey PRIMARY KEY (id),
    CONSTRAINT users_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE api_keys (
    prefix text NOT NULL,
    digest bytea NOT NULL,
    name text NOT NULL,
    rate_per_second integer NOT NULL,
    burst integer NOT NULL,
    created_at timestamp with time zone NOT NULL,
    revoked_at timestamp with time zone,
    owner_user_id uuid,
    scopes text[] NOT NULL,
    CONSTRAINT api_keys_check CHECK ((burst >= rate_per_second)),
    CONSTRAINT api_keys_digest_check CHECK ((octet_length(digest) = 32)),
    CONSTRAINT api_keys_prefix_check CHECK ((length(prefix) = 10)),
    CONSTRAINT api_keys_rate_per_second_check CHECK ((rate_per_second > 0)),
    CONSTRAINT api_keys_scopes_check CHECK (((scopes = ARRAY['api:read'::text]) OR (scopes = ARRAY['contract:verify'::text]) OR (scopes = ARRAY['api:read'::text, 'contract:verify'::text]))),
    CONSTRAINT api_keys_pkey PRIMARY KEY (prefix),
    CONSTRAINT api_keys_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES users(id)
);

CREATE TABLE auth_challenges (
    id uuid NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    address bytea NOT NULL,
    message text NOT NULL,
    nonce text NOT NULL,
    issued_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    consumed_at timestamp with time zone,
    CONSTRAINT auth_challenges_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT auth_challenges_check CHECK ((expires_at > issued_at)),
    CONSTRAINT auth_challenges_check1 CHECK (((consumed_at IS NULL) OR (consumed_at >= issued_at))),
    CONSTRAINT auth_challenges_message_check CHECK (((octet_length(message) >= 1) AND (octet_length(message) <= 4096))),
    CONSTRAINT auth_challenges_nonce_check CHECK (((char_length(nonce) >= 8) AND (char_length(nonce) <= 64))),
    CONSTRAINT auth_challenges_nonce_check1 CHECK ((nonce ~ '^[A-Za-z0-9]+$'::text)),
    CONSTRAINT auth_challenges_chain_id_nonce_key UNIQUE (chain_id, nonce),
    CONSTRAINT auth_challenges_pkey PRIMARY KEY (id),
    CONSTRAINT auth_challenges_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id)
);

CREATE TABLE user_sessions (
    id uuid NOT NULL,
    user_id uuid NOT NULL,
    token_digest bytea NOT NULL,
    csrf_digest bytea NOT NULL,
    created_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    last_used_at timestamp with time zone NOT NULL,
    revoked_at timestamp with time zone,
    CONSTRAINT user_sessions_check CHECK ((expires_at > created_at)),
    CONSTRAINT user_sessions_check1 CHECK ((last_used_at >= created_at)),
    CONSTRAINT user_sessions_check2 CHECK (((revoked_at IS NULL) OR (revoked_at >= created_at))),
    CONSTRAINT user_sessions_csrf_digest_check CHECK ((octet_length(csrf_digest) = 32)),
    CONSTRAINT user_sessions_token_digest_check CHECK ((octet_length(token_digest) = 32)),
    CONSTRAINT user_sessions_pkey PRIMARY KEY (id),
    CONSTRAINT user_sessions_token_digest_key UNIQUE (token_digest),
    CONSTRAINT user_sessions_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id)
);


-- Indexes

CREATE INDEX api_keys_owner_active_idx ON api_keys USING btree (owner_user_id, prefix) WHERE ((owner_user_id IS NOT NULL) AND (revoked_at IS NULL));

CREATE INDEX api_keys_owner_page_idx ON api_keys USING btree (owner_user_id, created_at DESC, prefix DESC) WHERE (owner_user_id IS NOT NULL);

CREATE INDEX auth_challenges_active_idx ON auth_challenges USING btree (chain_id, address, expires_at, id) WHERE (consumed_at IS NULL);

CREATE INDEX auth_challenges_cleanup_idx ON auth_challenges USING btree (expires_at, id);

CREATE INDEX user_sessions_active_user_idx ON user_sessions USING btree (user_id, expires_at, id) WHERE (revoked_at IS NULL);

CREATE INDEX user_sessions_cleanup_idx ON user_sessions USING btree (expires_at, id);

CREATE INDEX users_chain_created_idx ON users USING btree (chain_id, created_at DESC, id DESC);
