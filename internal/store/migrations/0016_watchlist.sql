-- Fresh-database baseline: watchlist.

-- Tables

CREATE TABLE address_export_admissions (
    token uuid NOT NULL,
    user_id uuid NOT NULL,
    started_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    expires_at timestamp with time zone DEFAULT (clock_timestamp() + '00:00:20'::interval) NOT NULL,
    released boolean DEFAULT false NOT NULL,
    CONSTRAINT address_export_admissions_pkey PRIMARY KEY (token),
    CONSTRAINT address_export_admissions_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id)
);

CREATE TABLE address_watches (
    id uuid NOT NULL,
    user_id uuid NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    address bytea NOT NULL,
    label text DEFAULT ''::text NOT NULL,
    kinds text[] NOT NULL,
    direction text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    start_number numeric(78,0) NOT NULL,
    start_hash bytea NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT address_watches_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT address_watches_direction_check CHECK ((direction = ANY (ARRAY['in'::text, 'out'::text, 'both'::text]))),
    CONSTRAINT address_watches_kinds_check CHECK (((cardinality(kinds) BETWEEN 1 AND 4) AND (kinds <@ ARRAY['transaction'::text, 'erc20'::text, 'erc721'::text, 'erc1155'::text]))),
    CONSTRAINT address_watches_label_check CHECK (((char_length(label) <= 64) AND (label !~ '[[:cntrl:]]'::text))),
    CONSTRAINT address_watches_start_hash_check CHECK ((octet_length(start_hash) = 32)),
    CONSTRAINT address_watches_pkey PRIMARY KEY (id),
    CONSTRAINT address_watches_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT address_watches_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id)
);

CREATE TABLE push_subscriptions (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id),
    session_id uuid NOT NULL REFERENCES user_sessions(id) ON DELETE CASCADE,
    endpoint text NOT NULL UNIQUE CHECK (octet_length(endpoint) <= 2048),
    endpoint_hash text NOT NULL CHECK(length(endpoint_hash)=64),
    p256dh text NOT NULL CHECK(length(p256dh)=87),
    auth text NOT NULL CHECK(length(auth)=22),
    language text NOT NULL CHECK (language IN ('en','zh')),
    start_number numeric(78,0) NOT NULL,
    start_hash bytea NOT NULL CHECK (octet_length(start_hash)=32),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- Indexes

CREATE INDEX address_export_admissions_active ON address_export_admissions USING btree (expires_at) WHERE (NOT released);

CREATE INDEX address_export_admissions_user ON address_export_admissions USING btree (user_id, started_at);

CREATE INDEX address_watches_match ON address_watches USING btree (chain_id, address, id) WHERE enabled;

CREATE UNIQUE INDEX address_watches_owner_address ON address_watches USING btree (user_id, chain_id, address);

CREATE INDEX push_subscriptions_owner ON push_subscriptions(user_id,session_id,id);
CREATE INDEX push_subscriptions_session ON push_subscriptions(session_id);
