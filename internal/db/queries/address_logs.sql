-- Materialize the ordered identity page before reading bodies. Correlated scalar
-- lookups keep publication joins from driving an unbounded candidate sort.
-- name: AddressLogsAll :many
WITH selected AS MATERIALIZED (
 SELECT l.chain_id,l.block_number,l.block_hash,l.tx_index,l.log_index
 FROM logs l
 WHERE l.chain_id=sqlc.arg(chain_id)::numeric AND l.address=sqlc.arg(address)::bytea
  AND l.block_number<=sqlc.arg(snapshot_number)::numeric AND l.block_number>=sqlc.arg(from_block)::numeric
  AND (l.block_number,l.tx_index,l.log_index)<(sqlc.arg(before_number)::numeric,sqlc.arg(before_tx)::bigint,sqlc.arg(before_log)::bigint)
  AND l.block_hash=(SELECT c.block_hash FROM canonical_blocks c WHERE c.chain_id=l.chain_id AND c.number=l.block_number)
 ORDER BY l.block_number DESC,l.tx_index DESC,l.log_index DESC LIMIT sqlc.arg(page_limit)::integer
)
SELECT s.block_number::text AS block_number,s.block_hash,
 (SELECT l.tx_hash FROM logs l WHERE l.chain_id=s.chain_id AND l.block_number=s.block_number AND l.block_hash=s.block_hash AND l.log_index=s.log_index) AS tx_hash,
 s.tx_index,s.log_index,
 COALESCE((SELECT b.timestamp::text FROM blocks b WHERE b.chain_id=s.chain_id AND b.number=s.block_number AND b.hash=s.block_hash),'')::text AS block_timestamp
FROM selected s
ORDER BY s.block_number DESC,s.tx_index DESC,s.log_index DESC;

-- name: AddressLogsTopic0 :many
WITH selected AS MATERIALIZED (
 SELECT l.chain_id,l.block_number,l.block_hash,l.tx_index,l.log_index
 FROM logs l
 WHERE l.chain_id=sqlc.arg(chain_id)::numeric AND l.address=sqlc.arg(address)::bytea
  AND l.block_number<=sqlc.arg(snapshot_number)::numeric AND l.block_number>=sqlc.arg(from_block)::numeric
  AND (l.block_number,l.tx_index,l.log_index)<(sqlc.arg(before_number)::numeric,sqlc.arg(before_tx)::bigint,sqlc.arg(before_log)::bigint) AND l.topic0=sqlc.arg(topic)::bytea
  AND (sqlc.narg(topic0)::bytea IS NULL OR l.topic0=sqlc.narg(topic0)::bytea)
  AND (sqlc.narg(topic1)::bytea IS NULL OR l.topic1=sqlc.narg(topic1)::bytea)
  AND (sqlc.narg(topic2)::bytea IS NULL OR l.topic2=sqlc.narg(topic2)::bytea)
  AND (sqlc.narg(topic3)::bytea IS NULL OR l.topic3=sqlc.narg(topic3)::bytea)
  AND l.block_hash=(SELECT c.block_hash FROM canonical_blocks c WHERE c.chain_id=l.chain_id AND c.number=l.block_number)
 ORDER BY l.block_number DESC,l.tx_index DESC,l.log_index DESC LIMIT sqlc.arg(page_limit)::integer
)
SELECT s.block_number::text AS block_number,s.block_hash,
 (SELECT l.tx_hash FROM logs l WHERE l.chain_id=s.chain_id AND l.block_number=s.block_number AND l.block_hash=s.block_hash AND l.log_index=s.log_index) AS tx_hash,
 s.tx_index,s.log_index,
 COALESCE((SELECT b.timestamp::text FROM blocks b WHERE b.chain_id=s.chain_id AND b.number=s.block_number AND b.hash=s.block_hash),'')::text AS block_timestamp
FROM selected s
ORDER BY s.block_number DESC,s.tx_index DESC,s.log_index DESC;

-- name: AddressLogsTopic1 :many
WITH selected AS MATERIALIZED (
 SELECT l.chain_id,l.block_number,l.block_hash,l.tx_index,l.log_index
 FROM logs l
 WHERE l.chain_id=sqlc.arg(chain_id)::numeric AND l.address=sqlc.arg(address)::bytea
  AND l.block_number<=sqlc.arg(snapshot_number)::numeric AND l.block_number>=sqlc.arg(from_block)::numeric
  AND (l.block_number,l.tx_index,l.log_index)<(sqlc.arg(before_number)::numeric,sqlc.arg(before_tx)::bigint,sqlc.arg(before_log)::bigint) AND l.topic1=sqlc.arg(topic)::bytea
  AND (sqlc.narg(topic0)::bytea IS NULL OR l.topic0=sqlc.narg(topic0)::bytea)
  AND (sqlc.narg(topic1)::bytea IS NULL OR l.topic1=sqlc.narg(topic1)::bytea)
  AND (sqlc.narg(topic2)::bytea IS NULL OR l.topic2=sqlc.narg(topic2)::bytea)
  AND (sqlc.narg(topic3)::bytea IS NULL OR l.topic3=sqlc.narg(topic3)::bytea)
  AND l.block_hash=(SELECT c.block_hash FROM canonical_blocks c WHERE c.chain_id=l.chain_id AND c.number=l.block_number)
 ORDER BY l.block_number DESC,l.tx_index DESC,l.log_index DESC LIMIT sqlc.arg(page_limit)::integer
)
SELECT s.block_number::text AS block_number,s.block_hash,
 (SELECT l.tx_hash FROM logs l WHERE l.chain_id=s.chain_id AND l.block_number=s.block_number AND l.block_hash=s.block_hash AND l.log_index=s.log_index) AS tx_hash,
 s.tx_index,s.log_index,
 COALESCE((SELECT b.timestamp::text FROM blocks b WHERE b.chain_id=s.chain_id AND b.number=s.block_number AND b.hash=s.block_hash),'')::text AS block_timestamp
FROM selected s
ORDER BY s.block_number DESC,s.tx_index DESC,s.log_index DESC;

-- name: AddressLogsTopic2 :many
WITH selected AS MATERIALIZED (
 SELECT l.chain_id,l.block_number,l.block_hash,l.tx_index,l.log_index
 FROM logs l
 WHERE l.chain_id=sqlc.arg(chain_id)::numeric AND l.address=sqlc.arg(address)::bytea
  AND l.block_number<=sqlc.arg(snapshot_number)::numeric AND l.block_number>=sqlc.arg(from_block)::numeric
  AND (l.block_number,l.tx_index,l.log_index)<(sqlc.arg(before_number)::numeric,sqlc.arg(before_tx)::bigint,sqlc.arg(before_log)::bigint) AND l.topic2=sqlc.arg(topic)::bytea
  AND (sqlc.narg(topic0)::bytea IS NULL OR l.topic0=sqlc.narg(topic0)::bytea)
  AND (sqlc.narg(topic1)::bytea IS NULL OR l.topic1=sqlc.narg(topic1)::bytea)
  AND (sqlc.narg(topic2)::bytea IS NULL OR l.topic2=sqlc.narg(topic2)::bytea)
  AND (sqlc.narg(topic3)::bytea IS NULL OR l.topic3=sqlc.narg(topic3)::bytea)
  AND l.block_hash=(SELECT c.block_hash FROM canonical_blocks c WHERE c.chain_id=l.chain_id AND c.number=l.block_number)
 ORDER BY l.block_number DESC,l.tx_index DESC,l.log_index DESC LIMIT sqlc.arg(page_limit)::integer
)
SELECT s.block_number::text AS block_number,s.block_hash,
 (SELECT l.tx_hash FROM logs l WHERE l.chain_id=s.chain_id AND l.block_number=s.block_number AND l.block_hash=s.block_hash AND l.log_index=s.log_index) AS tx_hash,
 s.tx_index,s.log_index,
 COALESCE((SELECT b.timestamp::text FROM blocks b WHERE b.chain_id=s.chain_id AND b.number=s.block_number AND b.hash=s.block_hash),'')::text AS block_timestamp
FROM selected s
ORDER BY s.block_number DESC,s.tx_index DESC,s.log_index DESC;

-- name: AddressLogsTopic3 :many
WITH selected AS MATERIALIZED (
 SELECT l.chain_id,l.block_number,l.block_hash,l.tx_index,l.log_index
 FROM logs l
 WHERE l.chain_id=sqlc.arg(chain_id)::numeric AND l.address=sqlc.arg(address)::bytea
  AND l.block_number<=sqlc.arg(snapshot_number)::numeric AND l.block_number>=sqlc.arg(from_block)::numeric
  AND (l.block_number,l.tx_index,l.log_index)<(sqlc.arg(before_number)::numeric,sqlc.arg(before_tx)::bigint,sqlc.arg(before_log)::bigint) AND l.topic3=sqlc.arg(topic)::bytea
  AND (sqlc.narg(topic0)::bytea IS NULL OR l.topic0=sqlc.narg(topic0)::bytea)
  AND (sqlc.narg(topic1)::bytea IS NULL OR l.topic1=sqlc.narg(topic1)::bytea)
  AND (sqlc.narg(topic2)::bytea IS NULL OR l.topic2=sqlc.narg(topic2)::bytea)
  AND (sqlc.narg(topic3)::bytea IS NULL OR l.topic3=sqlc.narg(topic3)::bytea)
  AND l.block_hash=(SELECT c.block_hash FROM canonical_blocks c WHERE c.chain_id=l.chain_id AND c.number=l.block_number)
 ORDER BY l.block_number DESC,l.tx_index DESC,l.log_index DESC LIMIT sqlc.arg(page_limit)::integer
)
SELECT s.block_number::text AS block_number,s.block_hash,
 (SELECT l.tx_hash FROM logs l WHERE l.chain_id=s.chain_id AND l.block_number=s.block_number AND l.block_hash=s.block_hash AND l.log_index=s.log_index) AS tx_hash,
 s.tx_index,s.log_index,
 COALESCE((SELECT b.timestamp::text FROM blocks b WHERE b.chain_id=s.chain_id AND b.number=s.block_number AND b.hash=s.block_hash),'')::text AS block_timestamp
FROM selected s
ORDER BY s.block_number DESC,s.tx_index DESC,s.log_index DESC;

-- name: AddressLogsSignature :many
WITH selected AS MATERIALIZED (
 SELECT l.chain_id,l.block_number,l.block_hash,l.tx_index,l.log_index
 FROM address_event_logs l
 WHERE l.chain_id=sqlc.arg(chain_id)::numeric AND l.address=sqlc.arg(address)::bytea
  AND l.block_number<=sqlc.arg(snapshot_number)::numeric AND l.block_number>=sqlc.arg(from_block)::numeric
  AND (l.block_number,l.tx_index,l.log_index)<(sqlc.arg(before_number)::numeric,sqlc.arg(before_tx)::bigint,sqlc.arg(before_log)::bigint) AND l.signature_id = ANY (ARRAY(SELECT id FROM event_signatures WHERE (sqlc.arg(signature)::text='' OR (signature_digest=sha256(convert_to(sqlc.arg(signature)::text,'UTF8')) AND signature=sqlc.arg(signature)::text)) AND (sqlc.narg(event_id)::bytea IS NULL OR digest=sqlc.narg(event_id)::bytea))) AND (SELECT true FROM logs raw WHERE raw.chain_id=l.chain_id AND raw.block_number=l.block_number AND raw.block_hash=l.block_hash AND raw.log_index=l.log_index
 AND (sqlc.narg(topic0)::bytea IS NULL OR raw.topic0=sqlc.narg(topic0)::bytea)
 AND (sqlc.narg(topic1)::bytea IS NULL OR raw.topic1=sqlc.narg(topic1)::bytea)
 AND (sqlc.narg(topic2)::bytea IS NULL OR raw.topic2=sqlc.narg(topic2)::bytea)
 AND (sqlc.narg(topic3)::bytea IS NULL OR raw.topic3=sqlc.narg(topic3)::bytea)) AND (SELECT p.state FROM published_block_stage_results p WHERE p.chain_id=l.chain_id AND p.block_number=l.block_number AND p.block_hash=l.block_hash AND p.stage='abi' AND p.stage_version=5 LIMIT 1)='complete'
  AND l.block_hash=(SELECT c.block_hash FROM canonical_blocks c WHERE c.chain_id=l.chain_id AND c.number=l.block_number)
 ORDER BY l.block_number DESC,l.tx_index DESC,l.log_index DESC LIMIT sqlc.arg(page_limit)::integer
)
SELECT s.block_number::text AS block_number,s.block_hash,
 (SELECT l.tx_hash FROM logs l WHERE l.chain_id=s.chain_id AND l.block_number=s.block_number AND l.block_hash=s.block_hash AND l.log_index=s.log_index) AS tx_hash,
 s.tx_index,s.log_index,
 COALESCE((SELECT b.timestamp::text FROM blocks b WHERE b.chain_id=s.chain_id AND b.number=s.block_number AND b.hash=s.block_hash),'')::text AS block_timestamp
FROM selected s
ORDER BY s.block_number DESC,s.tx_index DESC,s.log_index DESC;

-- name: AddressLogDetail :one
SELECT log.log_index, log.raw, decoding.status, decoding.signature,
       decoding.source, decoding.confidence, decoding.arguments,
       decoding.candidates, decoding.warning,
       decoding.target_address, decoding.target_code_hash,
       decoding.source_address, decoding.source_code_hash,
       attribution.trace_path, attribution.execution_address,
       (b.slot_number_raw IS NOT NULL AND b.slot_number_raw <> 'null'::jsonb)::boolean AS amsterdam,
       (native_stage.block_hash IS NOT NULL)::boolean AS native_published,
       native.from_address AS native_from, native.to_address AS native_to,
       COALESCE(native.amount::text, '')::text AS native_amount
FROM logs AS log
JOIN blocks b ON b.chain_id=log.chain_id AND b.number=log.block_number AND b.hash=log.block_hash
LEFT JOIN published_block_stage_results native_stage
  ON native_stage.chain_id=log.chain_id AND native_stage.block_number=log.block_number
 AND native_stage.block_hash=log.block_hash AND native_stage.stage='native_transfer'
 AND native_stage.stage_version=1 AND native_stage.state='complete'
 AND EXISTS (SELECT 1 FROM canonical_blocks c WHERE c.chain_id=log.chain_id
             AND c.number=log.block_number AND c.block_hash=log.block_hash)
LEFT JOIN native_transfers native
  ON native.chain_id=log.chain_id AND native.block_number=log.block_number
 AND native.block_hash=log.block_hash AND native.transaction_hash=log.tx_hash
 AND native.log_index=log.log_index AND native.canonical
LEFT JOIN abi_decodings AS decoding
  ON decoding.chain_id = log.chain_id
 AND decoding.block_number = log.block_number
 AND decoding.block_hash = log.block_hash
 AND decoding.transaction_hash = log.tx_hash
 AND decoding.object_kind = 'log'
 AND decoding.object_index = log.log_index::text
 AND decoding.canonical
 AND EXISTS (SELECT 1 FROM published_block_stage_results p WHERE p.chain_id=log.chain_id AND p.block_number=log.block_number AND p.block_hash=log.block_hash AND p.stage='abi' AND p.stage_version=5 AND p.state='complete')
LEFT JOIN trace_log_attributions AS attribution
  ON attribution.chain_id = log.chain_id
 AND attribution.block_number = log.block_number
 AND attribution.block_hash = log.block_hash
 AND attribution.transaction_hash = log.tx_hash
 AND attribution.log_index = log.log_index
 AND attribution.canonical
 AND EXISTS (
     SELECT 1
     FROM published_block_stage_results AS published
     WHERE published.chain_id = attribution.chain_id
       AND published.block_hash = attribution.block_hash
       AND published.stage = 'trace'
       AND published.stage_version = 4
       AND published.state = 'complete'
 )
WHERE log.chain_id = sqlc.arg('chain_id')::numeric AND log.block_hash = sqlc.arg('block_hash') AND log.tx_hash = sqlc.arg('tx_hash')
AND log.block_number=sqlc.arg(block_number)::numeric AND log.log_index=sqlc.arg(log_index)::bigint;

-- name: AddressLogEvents :many
SELECT d.signature_id,s.digest,s.definition,COALESCE(s.name,'')::text AS name,s.signature,lower(left(d.name,128))::text AS sort_name
FROM address_event_directory d JOIN event_signatures s ON s.id=d.signature_id
WHERE d.chain_id=sqlc.arg(chain_id)::numeric AND d.address=sqlc.arg(address)::bytea AND d.log_count>0
 AND (sqlc.narg(event_id)::bytea IS NULL OR s.digest=sqlc.narg(event_id)::bytea)
 AND lower(left(d.name,128)) LIKE sqlc.arg(name_prefix)::text ESCAPE '\'
 AND (lower(left(d.name,128)),d.signature_id)>(COALESCE((SELECT lower(left(name,128)) FROM event_signatures WHERE id=sqlc.arg(after_id)::bigint),''),sqlc.arg(after_id)::bigint)
 AND EXISTS (SELECT 1 FROM address_event_blocks b
 JOIN canonical_blocks c ON c.chain_id=b.chain_id AND c.number=b.block_number AND c.block_hash=b.block_hash
 JOIN published_block_stage_results p ON p.chain_id=b.chain_id AND p.block_number=b.block_number AND p.block_hash=b.block_hash AND p.stage='abi' AND p.stage_version=5 AND p.state='complete'
 WHERE b.chain_id=d.chain_id AND b.address=d.address AND b.signature_id=d.signature_id AND b.block_number<=sqlc.arg(snapshot_number)::numeric AND b.block_number>=sqlc.arg(from_block)::numeric)
ORDER BY lower(left(d.name,128)),d.signature_id LIMIT sqlc.arg(page_limit)::integer;

-- name: AddressLogEventDefinition :one
SELECT s.definition FROM event_signatures s
WHERE s.digest=sqlc.arg(event_id)::bytea
 AND EXISTS (SELECT 1 FROM address_event_directory d WHERE d.signature_id=s.id AND d.chain_id=sqlc.arg(chain_id)::numeric AND d.address=sqlc.arg(address)::bytea AND d.log_count>0);
