-- Fresh-database baseline: publication-aware explorer browsing.
CREATE SEQUENCE browsing_revision_seq;

CREATE TABLE browsing_blocks (
    chain_id numeric(78,0) NOT NULL REFERENCES chains(chain_id),
    stage text NOT NULL,
    stage_version integer NOT NULL CHECK (stage_version > 0),
    block_number numeric(78,0) NOT NULL CHECK (block_number >= 0),
    identity text NOT NULL,
    configuration_digest text NOT NULL,
    complete boolean NOT NULL,
    revision bigint NOT NULL DEFAULT nextval('browsing_revision_seq'),
    PRIMARY KEY (chain_id, stage, stage_version, block_number)
);
CREATE INDEX browsing_blocks_revision_idx ON browsing_blocks
    (chain_id, stage, stage_version, revision DESC) INCLUDE (block_number);

CREATE TABLE browsing_coverage (
    chain_id numeric(78,0) NOT NULL REFERENCES chains(chain_id),
    stage text NOT NULL,
    stage_version integer NOT NULL CHECK (stage_version > 0),
    configuration_digest text NOT NULL,
    covered nummultirange NOT NULL DEFAULT '{}'::nummultirange,
    PRIMARY KEY (chain_id, stage, stage_version, configuration_digest)
);

-- Called only when the authoritative ABI publication identity changes. The
-- browsing ABI advisory lock serializes directory deltas across block writers.
CREATE FUNCTION refresh_address_event_block(target_chain numeric, target_number numeric) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE contribution record;
BEGIN
    FOR contribution IN DELETE FROM address_event_blocks
        WHERE chain_id=target_chain AND block_number=target_number RETURNING * LOOP
        UPDATE address_event_directory SET log_count=log_count-contribution.log_count
        WHERE chain_id=target_chain AND address=contribution.address AND signature_id=contribution.signature_id;
    END LOOP;
    DELETE FROM address_event_logs WHERE chain_id=target_chain AND block_number=target_number;
    INSERT INTO event_signatures(signature,signature_digest,definition,digest)
    SELECT DISTINCT d.signature,sha256(convert_to(d.signature,'UTF8')),d.event_definition,sha256(convert_to(d.signature || d.event_definition::text,'UTF8')) FROM abi_decodings d
    JOIN canonical_blocks c ON c.chain_id=d.chain_id AND c.number=d.block_number AND c.block_hash=d.block_hash
    JOIN published_block_stage_results p ON p.chain_id=d.chain_id AND p.block_number=d.block_number
        AND p.block_hash=d.block_hash AND p.stage='abi' AND p.stage_version=5 AND p.state='complete'
    WHERE d.chain_id=target_chain AND d.block_number=target_number AND d.object_kind='log'
        AND d.status='decoded' AND d.canonical AND d.signature IS NOT NULL AND d.event_definition IS NOT NULL
    ON CONFLICT (digest) DO NOTHING;
    INSERT INTO address_event_logs(chain_id,block_number,block_hash,log_index,tx_index,address,signature_id)
    SELECT l.chain_id,l.block_number,l.block_hash,l.log_index,l.tx_index,l.address,s.id
    FROM logs l JOIN canonical_blocks c ON c.chain_id=l.chain_id AND c.number=l.block_number AND c.block_hash=l.block_hash
    JOIN published_block_stage_results p ON p.chain_id=l.chain_id AND p.block_number=l.block_number
        AND p.block_hash=l.block_hash AND p.stage='abi' AND p.stage_version=5 AND p.state='complete'
    JOIN abi_decodings d ON d.chain_id=l.chain_id AND d.block_number=l.block_number AND d.block_hash=l.block_hash
        AND d.transaction_hash=l.tx_hash AND d.object_kind='log' AND d.object_index=l.log_index::text AND d.canonical AND d.status='decoded'
    JOIN event_signatures s ON s.digest=sha256(convert_to(d.signature || d.event_definition::text,'UTF8')) AND s.signature=d.signature AND s.definition=d.event_definition
    WHERE l.chain_id=target_chain AND l.block_number=target_number;
    FOR contribution IN
        INSERT INTO address_event_blocks(chain_id,block_number,block_hash,address,signature_id,log_count)
        SELECT chain_id,block_number,block_hash,address,signature_id,count(*) FROM address_event_logs
        WHERE chain_id=target_chain AND block_number=target_number
        GROUP BY chain_id,block_number,block_hash,address,signature_id RETURNING * LOOP
        INSERT INTO address_event_directory(chain_id,address,signature_id,name,log_count)
        SELECT target_chain,contribution.address,contribution.signature_id,name,contribution.log_count
        FROM event_signatures WHERE id=contribution.signature_id
        ON CONFLICT (chain_id,address,signature_id) DO UPDATE
        SET log_count=address_event_directory.log_count+EXCLUDED.log_count;
    END LOOP;
END
$$;

CREATE FUNCTION refresh_browsing_block(target_chain numeric, target_number numeric) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    target record;
    publication record;
    previous browsing_blocks%ROWTYPE;
    canonical_hash bytea;
    new_identity text;
    new_digest text;
    is_complete boolean;
    singleton nummultirange;
BEGIN
    IF target_chain IS NULL OR target_number IS NULL THEN RETURN; END IF;
    singleton := nummultirange(numrange(target_number, target_number + 1, '[)'));
    -- A stable order is shared by every trigger and role.
    FOR target IN SELECT * FROM (VALUES ('core', 1), ('abi', 5), ('native_transfer', 1), ('token', 2), ('trace', 4), ('userop', 1)) AS stages(name, version) ORDER BY name LOOP
        PERFORM pg_advisory_xact_lock(hashtextextended('browsing:' || target_chain::text || ':' || target.name, 0));
        SELECT block_hash INTO canonical_hash FROM canonical_blocks
        WHERE chain_id = target_chain AND number = target_number;
        SELECT state, durable_job_id, job_generation, details INTO publication
        FROM published_block_stage_results
        WHERE chain_id = target_chain AND block_number = target_number
          AND block_hash = canonical_hash AND stage = target.name AND stage_version = target.version;
        IF target.name = 'core' THEN
            -- Canonical Core bundles and receipts commit atomically; Core has no enrichment job.
            publication.state := CASE WHEN canonical_hash IS NOT NULL THEN 'complete' ELSE 'pending' END;
        END IF;
        is_complete := COALESCE(publication.state = 'complete', false);
        new_digest := COALESCE(publication.details->>'configuration_digest', '');
        new_identity := COALESCE(encode(canonical_hash, 'hex'), '') || ':' ||
            COALESCE(publication.state, 'pending') || ':' ||
            COALESCE(publication.durable_job_id::text, '') || ':' ||
            COALESCE(publication.job_generation::text, '') || ':' || new_digest;
        IF target.name = 'core' THEN
            new_identity := new_identity || ':' || COALESCE((SELECT max(id)::text FROM transactional_outbox
                WHERE chain_id=target_chain AND topic='core.block.canonical'
                  AND message_key='0x' || encode(canonical_hash,'hex')), '0');
        END IF;
        SELECT * INTO previous FROM browsing_blocks
        WHERE chain_id = target_chain AND stage = target.name
          AND stage_version = target.version AND block_number = target_number;
        IF FOUND AND previous.identity = new_identity THEN CONTINUE; END IF;
        IF target.name = 'abi' THEN PERFORM refresh_address_event_block(target_chain, target_number); END IF;
        IF previous.complete THEN
            UPDATE browsing_coverage SET covered = covered - singleton
            WHERE chain_id = target_chain AND stage = target.name
              AND stage_version = target.version AND configuration_digest = previous.configuration_digest;
        END IF;
        INSERT INTO browsing_blocks(chain_id, stage, stage_version, block_number, identity, configuration_digest, complete)
        VALUES (target_chain, target.name, target.version, target_number, new_identity, new_digest, is_complete)
        ON CONFLICT (chain_id, stage, stage_version, block_number) DO UPDATE
        SET identity = EXCLUDED.identity, configuration_digest = EXCLUDED.configuration_digest,
            complete = EXCLUDED.complete, revision = EXCLUDED.revision;
        IF is_complete THEN
            INSERT INTO browsing_coverage(chain_id, stage, stage_version, configuration_digest, covered)
            VALUES (target_chain, target.name, target.version, new_digest, singleton)
            ON CONFLICT (chain_id, stage, stage_version, configuration_digest) DO UPDATE
            SET covered = browsing_coverage.covered + EXCLUDED.covered;
        END IF;
        -- Only transitions of published data wake browsers, not every queued job.
        IF is_complete OR COALESCE(previous.complete, false) THEN
            INSERT INTO runtime_events(chain_id, event_type, payload)
            VALUES (target_chain, 'status', jsonb_build_object('reason', 'browsing_publication', 'stage', target.name, 'block_number', target_number::text));
        END IF;
    END LOOP;
END
$$;

CREATE FUNCTION refresh_browsing_source() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    source jsonb;
    target_number numeric;
    target_hash text;
BEGIN
    FOR source IN SELECT value FROM jsonb_array_elements(
        CASE WHEN TG_OP = 'INSERT' THEN jsonb_build_array(to_jsonb(NEW))
             WHEN TG_OP = 'DELETE' THEN jsonb_build_array(to_jsonb(OLD))
             ELSE jsonb_build_array(to_jsonb(OLD), to_jsonb(NEW)) END
    ) LOOP
        IF TG_TABLE_NAME = 'durable_jobs' AND source->>'kind' <> 'enrichment' THEN CONTINUE; END IF;
        IF TG_TABLE_NAME = 'transactional_outbox' AND source->>'topic' <> 'core.block.canonical' THEN CONTINUE; END IF;
        target_number := COALESCE(source->>'block_number', source->>'number', source->'payload'->>'block_number')::numeric;
        IF target_number IS NULL THEN
            target_hash := COALESCE(source->>'block_hash', source->>'message_key');
            IF target_hash IS NULL OR target_hash !~ '^(0x|\\x)[0-9a-fA-F]{64}$' THEN CONTINUE; END IF;
            SELECT number INTO target_number FROM blocks WHERE chain_id = (source->>'chain_id')::numeric
              AND hash = decode(regexp_replace(target_hash, '^(0x|\\x)', ''), 'hex');
        END IF;
        PERFORM refresh_browsing_block((source->>'chain_id')::numeric, target_number);
    END LOOP;
    RETURN NULL;
END
$$;

CREATE CONSTRAINT TRIGGER browsing_canonical AFTER INSERT OR UPDATE OR DELETE ON canonical_blocks DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION refresh_browsing_source();
CREATE CONSTRAINT TRIGGER browsing_jobs AFTER INSERT OR UPDATE OR DELETE ON durable_jobs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION refresh_browsing_source();
CREATE CONSTRAINT TRIGGER browsing_results AFTER INSERT OR UPDATE OR DELETE ON block_stage_results DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION refresh_browsing_source();
CREATE CONSTRAINT TRIGGER browsing_journals AFTER INSERT OR UPDATE OR DELETE ON block_journals DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION refresh_browsing_source();
CREATE CONSTRAINT TRIGGER browsing_outbox AFTER INSERT OR UPDATE OR DELETE ON transactional_outbox DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION refresh_browsing_source();
