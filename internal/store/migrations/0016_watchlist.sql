-- Fresh-database baseline: watchlist.

-- Functions

CREATE FUNCTION invalidate_watch_block(p_chain numeric, p_number numeric, p_hash bytea) RETURNS void
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM address_watches WHERE chain_id = p_chain AND deleted_at IS NULL AND enabled AND start_number < p_number)
       AND NOT EXISTS (SELECT 1 FROM watch_notifications WHERE chain_id = p_chain AND block_hash = p_hash) THEN
        RETURN;
    END IF;
    INSERT INTO watch_notification_work(chain_id, block_number, block_hash)
    VALUES(p_chain, p_number, p_hash)
    ON CONFLICT(chain_id, block_hash) DO UPDATE SET
        generation = watch_notification_work.generation + 1,
        after_watch = '00000000-0000-0000-0000-000000000000', after_source = '';
END
$$;

CREATE FUNCTION watch_core_change() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    PERFORM invalidate_watch_block(NEW.chain_id, (NEW.payload->>'block_number')::numeric, decode(substr(NEW.payload->>'block_hash',3),'hex'));
    RETURN NULL;
END
$$;

CREATE FUNCTION watch_token_change() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    PERFORM invalidate_watch_block(NEW.chain_id, (NEW.payload->>'block_number')::numeric, decode(substr(NEW.payload->>'block_hash',3),'hex'));
    RETURN NULL;
END
$$;


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
    deleted_at timestamp with time zone,
    CONSTRAINT address_watches_address_check CHECK ((octet_length(address) = 20)),
    CONSTRAINT address_watches_direction_check CHECK ((direction = ANY (ARRAY['in'::text, 'out'::text, 'both'::text]))),
    CONSTRAINT address_watches_kinds_check CHECK (((cardinality(kinds) BETWEEN 1 AND 4) AND (kinds <@ ARRAY['transaction'::text, 'erc20'::text, 'erc721'::text, 'erc1155'::text]))),
    CONSTRAINT address_watches_label_check CHECK (((char_length(label) <= 64) AND (label !~ '[[:cntrl:]]'::text))),
    CONSTRAINT address_watches_start_hash_check CHECK ((octet_length(start_hash) = 32)),
    CONSTRAINT address_watches_pkey PRIMARY KEY (id),
    CONSTRAINT address_watches_chain_id_fkey FOREIGN KEY (chain_id) REFERENCES chains(chain_id),
    CONSTRAINT address_watches_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id)
);

CREATE TABLE watch_notification_work (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    generation bigint DEFAULT 1 NOT NULL,
    done_generation bigint DEFAULT 0 NOT NULL,
    lease_token uuid,
    lease_until timestamp with time zone,
    after_watch uuid DEFAULT '00000000-0000-0000-0000-000000000000'::uuid NOT NULL,
    after_source text DEFAULT ''::text NOT NULL,
    CONSTRAINT watch_notification_work_pkey PRIMARY KEY (chain_id, block_hash)
);

CREATE TABLE watch_notifications (
    id bigint NOT NULL GENERATED ALWAYS AS IDENTITY (
        SEQUENCE NAME watch_notifications_id_seq
        START WITH 1
        INCREMENT BY 1
        NO MINVALUE
        NO MAXVALUE
        CACHE 1
    ),
    watch_id uuid NOT NULL,
    user_id uuid NOT NULL,
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    source_key text NOT NULL,
    source_kind text NOT NULL,
    source_generation bigint NOT NULL,
    activity jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    read_at timestamp with time zone,
    CONSTRAINT watch_notifications_block_hash_check CHECK ((octet_length(block_hash) = 32)),
    CONSTRAINT watch_notifications_pkey PRIMARY KEY (id),
    CONSTRAINT watch_notifications_watch_id_block_hash_source_key_key UNIQUE (watch_id, block_hash, source_key),
    CONSTRAINT watch_notifications_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id),
    CONSTRAINT watch_notifications_watch_id_fkey FOREIGN KEY (watch_id) REFERENCES address_watches(id)
);


-- Indexes

CREATE INDEX address_export_admissions_active ON address_export_admissions USING btree (expires_at) WHERE (NOT released);

CREATE INDEX address_export_admissions_user ON address_export_admissions USING btree (user_id, started_at);

CREATE INDEX address_watches_match ON address_watches USING btree (chain_id, address, id) WHERE ((deleted_at IS NULL) AND enabled);

CREATE UNIQUE INDEX address_watches_owner_address ON address_watches USING btree (user_id, chain_id, address) WHERE (deleted_at IS NULL);

CREATE INDEX watch_notification_work_pending ON watch_notification_work USING btree (chain_id, block_number, block_hash) WHERE (generation > done_generation);

CREATE INDEX watch_notifications_block ON watch_notifications USING btree (chain_id, block_hash, source_key, watch_id);

CREATE INDEX watch_notifications_expiry ON watch_notifications USING btree (created_at, id);

CREATE INDEX watch_notifications_owner_page ON watch_notifications USING btree (user_id, id DESC);

CREATE INDEX watch_notifications_unread ON watch_notifications USING btree (user_id, id) WHERE (read_at IS NULL);


-- Triggers

CREATE TRIGGER watch_core_change AFTER INSERT OR UPDATE OF generation ON transactional_outbox FOR EACH ROW WHEN ((new.topic = ANY (ARRAY['core.block.canonical'::text, 'core.block.orphaned'::text]))) EXECUTE FUNCTION watch_core_change();

CREATE TRIGGER watch_token_change AFTER INSERT OR UPDATE OF requested_generation, completed_generation, status ON durable_jobs FOR EACH ROW WHEN (((new.kind = 'enrichment'::text) AND (new.stage = 'token'::text) AND (new.stage_version = 2))) EXECUTE FUNCTION watch_token_change();
