ALTER TABLE blocks ADD COLUMN block_access_list_hash_raw JSONB GENERATED ALWAYS AS (raw->'blockAccessListHash') STORED;

-- Current fresh-schema Amsterdam stage publications. No startup reindex.
DROP TRIGGER IF EXISTS block_stage_results_chart_dirty_insert_trigger ON block_stage_results;
CREATE TRIGGER block_stage_results_chart_dirty_insert_trigger
AFTER INSERT ON block_stage_results
FOR EACH ROW
WHEN (
    (NEW.stage = 'stats' AND NEW.stage_version = 4)
    OR (NEW.stage = 'token' AND NEW.stage_version = 2)
)
EXECUTE FUNCTION mark_chart_rollup_dirty();

DROP TRIGGER IF EXISTS block_stage_results_chart_dirty_update_trigger ON block_stage_results;
CREATE TRIGGER block_stage_results_chart_dirty_update_trigger
AFTER UPDATE ON block_stage_results
FOR EACH ROW
WHEN (
    (NEW.stage = 'stats' AND NEW.stage_version = 4)
    OR (NEW.stage = 'token' AND NEW.stage_version = 2)
    OR (OLD.stage = 'stats' AND OLD.stage_version = 4)
    OR (OLD.stage = 'token' AND OLD.stage_version = 2)
)
EXECUTE FUNCTION mark_chart_rollup_dirty();

DROP TRIGGER IF EXISTS block_stage_results_chart_dirty_delete_trigger ON block_stage_results;
CREATE TRIGGER block_stage_results_chart_dirty_delete_trigger
AFTER DELETE ON block_stage_results
FOR EACH ROW
WHEN (
    (OLD.stage = 'stats' AND OLD.stage_version = 4)
    OR (OLD.stage = 'token' AND OLD.stage_version = 2)
)
EXECUTE FUNCTION mark_chart_rollup_dirty();

CREATE OR REPLACE FUNCTION mark_chart_rollup_dirty_from_job()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    source_number NUMERIC(78, 0);
    source_hash BYTEA;
BEGIN
    IF NEW.kind <> 'enrichment'
       OR NOT (
           (NEW.stage = 'stats' AND NEW.stage_version = 4)
           OR (NEW.stage = 'token' AND NEW.stage_version = 2)
       )
       OR NEW.payload->>'block_number' !~ '^(0|[1-9][0-9]*)$'
       OR NEW.payload->>'block_hash' !~ '^0x[0-9a-f]{64}$' THEN
        RETURN NEW;
    END IF;
    source_number := (NEW.payload->>'block_number')::numeric;
    source_hash := decode(substr(NEW.payload->>'block_hash', 3), 'hex');
    PERFORM mark_chart_rollup_dirty_for_block(NEW.chain_id, source_number, source_hash);
    RETURN NEW;
END
$$;


DROP TRIGGER watch_token_change ON durable_jobs;
CREATE TRIGGER watch_token_change AFTER INSERT OR UPDATE OF requested_generation, completed_generation, status ON durable_jobs
FOR EACH ROW WHEN (NEW.kind = 'enrichment' AND NEW.stage = 'token' AND NEW.stage_version = 2) EXECUTE FUNCTION watch_token_change();

CREATE OR REPLACE VIEW watch_activity_sources AS
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
JOIN published_block_stage_results AS publication ON publication.chain_id=event.chain_id AND publication.block_hash=event.block_hash AND publication.stage='token' AND publication.stage_version=2 AND publication.state='complete'
LEFT JOIN LATERAL (
 SELECT contract.decimals FROM token_contracts AS contract
 WHERE contract.chain_id=event.chain_id AND contract.address=event.token_address
 AND contract.observed_block_number=event.block_number AND contract.observed_block_hash=event.block_hash
 AND contract.standard='erc20' AND contract.metadata_state='complete'
 ORDER BY contract.code_hash LIMIT 1
) AS metadata ON event.standard='erc20'
WHERE event.canonical AND event.event_kind IN ('transfer','mint','burn');

-- Old trace witnesses cannot prove current proxy interaction coverage.
TRUNCATE TABLE proxy_interaction_coverage_ranges, proxy_interaction_covered_blocks;

-- Persisted coverage and forward-discovery functions must use the same trace
-- witness as readers and workers. This changes definitions, never starts jobs.
DO $$
DECLARE target REGPROCEDURE; definition TEXT; upgraded TEXT;
BEGIN
 FOREACH target IN ARRAY ARRAY[
 'refresh_proxy_interaction_coverage_block(numeric,numeric)'::regprocedure,
 'refresh_proxy_interaction_coverage_from_stage_result()'::regprocedure,
 'refresh_proxy_interaction_coverage_from_job()'::regprocedure,
 'refresh_proxy_interaction_coverage_from_journal()'::regprocedure,
 'enqueue_derived_forward_event_after_stage_publication()'::regprocedure
 ] LOOP
 definition:=pg_get_functiondef(target);
 upgraded:=replace(definition, '''trace''::text, 3', '''trace''::text, 4');
 upgraded:=replace(upgraded, '''trace'' AND OLD.stage_version = 3', '''trace'' AND OLD.stage_version = 4');
 upgraded:=replace(upgraded, '''trace'' AND NEW.stage_version = 3', '''trace'' AND NEW.stage_version = 4');
 upgraded:=replace(upgraded, '''trace@3''', '''trace@4''');
 IF upgraded = definition THEN RAISE EXCEPTION 'function % has no trace@3 witness',target; END IF;
 EXECUTE upgraded;
 END LOOP;
END $$;
