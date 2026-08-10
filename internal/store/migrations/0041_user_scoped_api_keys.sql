-- P65-T09 user-owned, scoped API keys.
ALTER TABLE api_keys
    ADD COLUMN owner_user_id UUID REFERENCES users(id),
    ADD COLUMN scopes TEXT[] NOT NULL
        DEFAULT ARRAY['api:read', 'contract:verify']::TEXT[];

ALTER TABLE api_keys
    ALTER COLUMN scopes DROP DEFAULT,
    ADD CONSTRAINT api_keys_scopes_check CHECK (
        scopes = ARRAY['api:read']::TEXT[]
        OR scopes = ARRAY['contract:verify']::TEXT[]
        OR scopes = ARRAY['api:read', 'contract:verify']::TEXT[]
    );

CREATE INDEX api_keys_owner_page_idx
    ON api_keys (owner_user_id, created_at DESC, prefix DESC)
    WHERE owner_user_id IS NOT NULL;

CREATE INDEX api_keys_owner_active_idx
    ON api_keys (owner_user_id, prefix)
    WHERE owner_user_id IS NOT NULL AND revoked_at IS NULL;
