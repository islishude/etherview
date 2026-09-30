-- P78 / ADR-0051. Private account state and independent durable delivery work.
CREATE TABLE address_watches (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id),
    chain_id NUMERIC(78,0) NOT NULL REFERENCES chains(chain_id),
    address BYTEA NOT NULL CHECK (octet_length(address) = 20),
    label TEXT NOT NULL DEFAULT '' CHECK (char_length(label) <= 64 AND label !~ '[[:cntrl:]]'),
    kinds TEXT[] NOT NULL CHECK (cardinality(kinds) BETWEEN 1 AND 4 AND kinds <@ ARRAY['transaction','erc20','erc721','erc1155']::text[]),
    direction TEXT NOT NULL CHECK (direction IN ('in','out','both')),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    start_number NUMERIC(78,0) NOT NULL,
    start_hash BYTEA NOT NULL CHECK (octet_length(start_hash) = 32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX address_watches_owner_address ON address_watches(user_id, chain_id, address) WHERE deleted_at IS NULL;
CREATE INDEX address_watches_match ON address_watches(chain_id, address, id) WHERE deleted_at IS NULL AND enabled;

CREATE TABLE watch_notifications (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    watch_id UUID NOT NULL REFERENCES address_watches(id),
    user_id UUID NOT NULL REFERENCES users(id),
    chain_id NUMERIC(78,0) NOT NULL,
    block_number NUMERIC(78,0) NOT NULL,
    block_hash BYTEA NOT NULL CHECK (octet_length(block_hash) = 32),
    source_key TEXT NOT NULL,
    source_kind TEXT NOT NULL,
    source_generation BIGINT NOT NULL,
    activity JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    read_at TIMESTAMPTZ,
    UNIQUE (watch_id, block_hash, source_key)
);
CREATE INDEX watch_notifications_owner_page ON watch_notifications(user_id, id DESC);
CREATE INDEX watch_notifications_unread ON watch_notifications(user_id, id) WHERE read_at IS NULL;
CREATE INDEX watch_notifications_expiry ON watch_notifications(created_at, id);
CREATE INDEX watch_notifications_block ON watch_notifications(chain_id, block_hash, source_key, watch_id);

CREATE TABLE watch_notification_work (
    chain_id NUMERIC(78,0) NOT NULL,
    block_number NUMERIC(78,0) NOT NULL,
    block_hash BYTEA NOT NULL,
    generation BIGINT NOT NULL DEFAULT 1,
    done_generation BIGINT NOT NULL DEFAULT 0,
    lease_token UUID,
    lease_until TIMESTAMPTZ,
    -- Progress is ordered by (source_key, watch_id); the maximum UUID ends a source.
    after_watch UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000',
    after_source TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (chain_id, block_hash)
);
CREATE INDEX watch_notification_work_pending ON watch_notification_work(chain_id, block_number, block_hash) WHERE generation > done_generation;

-- Producers coalesce work in their own transaction, never wait for a consumer.
CREATE FUNCTION invalidate_watch_block(p_chain NUMERIC, p_number NUMERIC, p_hash BYTEA) RETURNS VOID LANGUAGE plpgsql AS $$
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
CREATE FUNCTION watch_core_change() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    PERFORM invalidate_watch_block(NEW.chain_id, (NEW.payload->>'block_number')::numeric, decode(substr(NEW.payload->>'block_hash',3),'hex'));
    RETURN NULL;
END
$$;
CREATE TRIGGER watch_core_change AFTER INSERT OR UPDATE OF generation ON transactional_outbox
FOR EACH ROW WHEN (NEW.topic IN ('core.block.canonical','core.block.orphaned')) EXECUTE FUNCTION watch_core_change();

CREATE FUNCTION watch_token_change() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    PERFORM invalidate_watch_block(NEW.chain_id, (NEW.payload->>'block_number')::numeric, decode(substr(NEW.payload->>'block_hash',3),'hex'));
    RETURN NULL;
END
$$;
CREATE TRIGGER watch_token_change AFTER INSERT OR UPDATE OF requested_generation, completed_generation, status ON durable_jobs
FOR EACH ROW WHEN (NEW.kind = 'enrichment' AND NEW.stage = 'token' AND NEW.stage_version = 1) EXECUTE FUNCTION watch_token_change();

CREATE TABLE address_export_admissions (
    token UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id),
    started_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp() + interval '20 seconds',
    released BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE INDEX address_export_admissions_user ON address_export_admissions(user_id, started_at);
CREATE INDEX address_export_admissions_active ON address_export_admissions(expires_at) WHERE NOT released;

-- Neutral, exact-block source shared by delivery and CSV, not public HTTP types.
CREATE VIEW watch_activity_sources AS
SELECT inclusion.chain_id, inclusion.block_number, inclusion.block_hash,
       block.timestamp AS block_timestamp, inclusion.tx_index,
       'tx:' || encode(inclusion.tx_hash,'hex') AS source_key,
       'transaction'::text AS source_kind, 0::bigint AS source_generation,
       lower(inclusion.raw->>'from') AS from_address,
       lower(COALESCE(inclusion.raw->>'to', receipt.raw->>'contractAddress')) AS to_address,
       jsonb_build_object('block_number', inclusion.block_number::text,
         'block_hash', '0x'||encode(inclusion.block_hash,'hex'), 'timestamp',block.timestamp::text,
         'transaction_hash','0x'||encode(inclusion.tx_hash,'hex'), 'transaction_index',inclusion.tx_index::text,
         'kind','transaction', 'from',lower(inclusion.raw->>'from'),
         'to',lower(COALESCE(inclusion.raw->>'to',receipt.raw->>'contractAddress')),
         'value',inclusion.raw->>'value', 'status',receipt.raw->>'status') AS activity
FROM transaction_inclusions AS inclusion
JOIN blocks AS block ON block.chain_id=inclusion.chain_id AND block.number=inclusion.block_number AND block.hash=inclusion.block_hash
JOIN canonical_blocks AS canonical ON canonical.chain_id=inclusion.chain_id AND canonical.number=inclusion.block_number AND canonical.block_hash=inclusion.block_hash
JOIN receipts AS receipt ON receipt.chain_id=inclusion.chain_id AND receipt.block_number=inclusion.block_number AND receipt.block_hash=inclusion.block_hash AND receipt.tx_index=inclusion.tx_index
UNION ALL
SELECT event.chain_id,event.block_number,event.block_hash,block.timestamp,inclusion.tx_index,
       'log:'||event.log_index::text||':'||event.sub_index::text, event.standard, publication.job_generation,
       '0x'||encode(event.from_address,'hex'), '0x'||encode(event.to_address,'hex'),
       jsonb_build_object('block_number',event.block_number::text,'block_hash','0x'||encode(event.block_hash,'hex'),
         'timestamp',block.timestamp::text,'transaction_hash','0x'||encode(event.transaction_hash,'hex'),
         'transaction_index',inclusion.tx_index::text,'kind',event.standard,
         'from','0x'||encode(event.from_address,'hex'),'to','0x'||encode(event.to_address,'hex'),
         'token_address','0x'||encode(event.token_address,'hex'),'event_kind',event.event_kind,
         'log_index',event.log_index::text,'sub_index',event.sub_index::text,
         'token_id',event.token_id::text,'amount',event.amount::text,'decimals',metadata.decimals::text)
FROM token_events AS event
JOIN canonical_blocks AS canonical ON canonical.chain_id=event.chain_id AND canonical.number=event.block_number AND canonical.block_hash=event.block_hash
JOIN blocks AS block ON block.chain_id=event.chain_id AND block.number=event.block_number AND block.hash=event.block_hash
JOIN transaction_inclusions AS inclusion ON inclusion.chain_id=event.chain_id AND inclusion.block_number=event.block_number AND inclusion.block_hash=event.block_hash AND inclusion.tx_hash=event.transaction_hash
JOIN published_block_stage_results AS publication ON publication.chain_id=event.chain_id AND publication.block_hash=event.block_hash AND publication.stage='token' AND publication.stage_version=1 AND publication.state='complete'
LEFT JOIN LATERAL (
 SELECT contract.decimals FROM token_contracts AS contract
 WHERE contract.chain_id=event.chain_id AND contract.address=event.token_address
 AND contract.observed_block_number=event.block_number AND contract.observed_block_hash=event.block_hash
 AND contract.standard='erc20' AND contract.metadata_state='complete'
 ORDER BY contract.code_hash LIMIT 1
) AS metadata ON event.standard='erc20'
WHERE event.canonical AND event.event_kind IN ('transfer','mint','burn');
