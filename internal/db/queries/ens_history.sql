-- name: PutENSHistoryEvent :exec
INSERT INTO ens_history_events(source_id,block_hash,log_index,block_number,event_name,action,fields)
VALUES(@source_id,@block_hash,@log_index,@block_number,@event_name,@action,@fields)
ON CONFLICT DO NOTHING;

-- name: CaptureENSHistoryStates :exec
INSERT INTO ens_history_states(source_id,block_hash,log_index,kind,key,payload)
SELECT DISTINCT ON(p.kind,p.key) p.source_id,p.block_hash,@log_index,p.kind,p.key,p.payload FROM ens_sync_projections p
WHERE p.source_id= @source_id AND p.block_hash= @block_hash AND p.revision> @after_revision
ORDER BY p.kind,p.key,p.revision DESC
ON CONFLICT DO NOTHING;

-- name: PutENSHistorySubject :execrows
INSERT INTO ens_history_subjects(source_id,block_hash,log_index,node)
VALUES(@source_id,@block_hash,@log_index,@node) ON CONFLICT DO NOTHING;

-- name: PutENSHistoryPending :exec
INSERT INTO ens_history_pending(source_id,block_hash,log_index)
VALUES(@source_id,@block_hash,@log_index) ON CONFLICT DO NOTHING;

-- name: CompleteENSHistoryPending :exec
DELETE FROM ens_history_pending WHERE source_id= @source_id AND block_hash= @block_hash AND log_index= @log_index;

-- name: RetryENSHistoryPending :exec
UPDATE ens_history_pending SET attempts=attempts+1,retry_at=now()+interval '30 seconds'
WHERE source_id= @source_id AND block_hash= @block_hash AND log_index= @log_index AND attempts<8;

-- name: ListENSHistoryPending :many
SELECT p.block_hash,p.log_index,l.raw,b.number,b.block_time
FROM ens_history_pending p JOIN ens_sync_blocks b ON b.source_id=p.source_id AND b.hash=p.block_hash AND b.canonical
JOIN ens_sync_sources s ON s.id=p.source_id
JOIN logs l ON l.chain_id=s.source_chain_id AND l.block_number=b.number AND l.block_hash=b.hash AND l.log_index=p.log_index
WHERE p.source_id= @source_id AND p.attempts<8 AND p.retry_at<=now()
ORDER BY p.retry_at,p.block_hash,p.log_index LIMIT @row_limit;

-- name: ReadENSHistoryState :one
SELECT h.payload FROM ens_history_states h
JOIN ens_sync_blocks b ON b.source_id=h.source_id AND b.hash=h.block_hash AND b.canonical
WHERE h.source_id= @source_id AND h.kind= @kind AND h.key= @key
AND (b.number< @at_number OR (b.number= @at_number AND h.log_index<= @at_log))
ORDER BY b.number DESC,h.log_index DESC LIMIT 1;

-- name: ListENSHistoryStates :many
SELECT h.key,h.payload FROM ens_history_states h
JOIN ens_sync_blocks b ON b.source_id=h.source_id AND b.hash=h.block_hash AND b.canonical
WHERE h.source_id= @source_id AND h.kind= @kind AND starts_with(h.key,@key_prefix::text)
AND h.payload @> jsonb_build_object(@field::text,@value::text)
AND (b.number< @at_number OR (b.number= @at_number AND h.log_index<= @at_log))
AND NOT EXISTS (
 SELECT 1 FROM ens_history_states newer JOIN ens_sync_blocks nb ON nb.source_id=newer.source_id AND nb.hash=newer.block_hash AND nb.canonical
 WHERE newer.source_id=h.source_id AND newer.kind=h.kind AND newer.key=h.key
 AND (nb.number< @at_number OR (nb.number= @at_number AND newer.log_index<= @at_log))
 AND (nb.number,newer.log_index)>(b.number,h.log_index)
)
ORDER BY h.key LIMIT @row_limit;

-- name: ReadENSHistoryScope :one
SELECT s.id,s.manifest_digest,s.start_number,p.revision,p.scanned_number,p.scanned_hash,tip.number AS target_number,tip.block_hash AS target_hash,
 p.state,p.healthy_until,COALESCE(b.canonical,false)::boolean AS canonical,
 (SELECT count(*) FROM ens_history_pending h JOIN ens_sync_blocks hb ON hb.source_id=h.source_id AND hb.hash=h.block_hash AND hb.canonical WHERE h.source_id=s.id)::bigint AS pending
FROM ens_effective_policies policy JOIN ens_sync_sources s ON s.id=policy.source_id
JOIN ens_sync_progress p ON p.source_id=s.id
LEFT JOIN ens_sync_blocks b ON b.source_id=s.id AND b.hash=p.scanned_hash
LEFT JOIN LATERAL (SELECT number,block_hash FROM canonical_blocks WHERE chain_id=s.target_chain_id ORDER BY number DESC LIMIT 1) tip ON true
WHERE policy.target_chain_id= @chain_id AND policy.policy_digest= @policy_digest;

-- name: ReadENSHistoryFence :one
SELECT b.number,b.block_time,
 COALESCE((SELECT max(h.id) FROM ens_history_subjects h JOIN ens_history_events e USING(source_id,block_hash,log_index)
 JOIN ens_sync_blocks hb ON hb.source_id=h.source_id AND hb.hash=h.block_hash AND hb.canonical
 WHERE h.source_id= @source_id AND h.node= @node AND e.block_number<=b.number),0)::bigint AS watermark
FROM ens_sync_blocks b WHERE b.source_id= @source_id AND b.hash= @snapshot_hash AND b.canonical;

-- name: ListENSHistoryTransactions :many
WITH matching AS (
 SELECT e.block_number,e.block_hash,l.tx_hash,l.tx_index,e.action
 FROM ens_history_subjects h JOIN ens_history_events e USING(source_id,block_hash,log_index)
 JOIN ens_sync_sources s ON s.id=h.source_id
 JOIN ens_sync_blocks b ON b.source_id=h.source_id AND b.hash=h.block_hash AND b.canonical
 JOIN logs l ON l.chain_id=s.source_chain_id AND l.block_number=e.block_number AND l.block_hash=e.block_hash AND l.log_index=e.log_index
 WHERE h.source_id= @source_id AND h.node= @node AND e.block_number<= @snapshot_number
 AND (NOT @has_boundary::boolean OR (e.block_number,l.tx_index)<(@before_number,@before_tx::bigint))
)
SELECT m.block_number,m.block_hash,m.tx_hash,m.tx_index,array_agg(DISTINCT m.action)::text[] AS actions,count(*)::bigint AS event_count,
 b.block_time FROM matching m JOIN ens_sync_blocks b ON b.source_id= @source_id AND b.hash=m.block_hash AND b.canonical
GROUP BY m.block_number,m.block_hash,m.tx_hash,m.tx_index,b.block_time
ORDER BY m.block_number DESC,m.tx_index DESC LIMIT @row_limit;

-- name: ListENSHistoryTransactionEvents :many
SELECT e.log_index,e.event_name,e.action,e.fields,l.address
FROM ens_history_subjects h JOIN ens_history_events e USING(source_id,block_hash,log_index)
JOIN ens_sync_sources s ON s.id=h.source_id
JOIN ens_sync_blocks b ON b.source_id=h.source_id AND b.hash=h.block_hash AND b.canonical
JOIN logs l ON l.chain_id=s.source_chain_id AND l.block_number=e.block_number AND l.block_hash=e.block_hash AND l.log_index=e.log_index
WHERE h.source_id= @source_id AND h.node= @node AND e.block_hash= @block_hash AND l.tx_hash= @transaction_hash
AND e.block_number<= @snapshot_number AND e.log_index> @after_log
ORDER BY e.log_index LIMIT @row_limit;

-- name: EmitENSHistoryRefresh :exec
INSERT INTO runtime_events(chain_id,event_type,payload)
SELECT s.target_chain_id,'names',jsonb_build_object('history',true,'invalidated',false) FROM ens_sync_sources s WHERE s.id= @source_id;

-- name: ReadENSHistoryMintLabel :one
SELECT h.payload FROM ens_history_states h
JOIN ens_sync_events he ON he.source_id=h.source_id AND he.block_hash=h.block_hash AND he.log_index=h.log_index
JOIN ens_sync_events origin ON origin.source_id=he.source_id AND origin.block_hash=he.block_hash AND origin.log_index= @log_index
JOIN ens_history_events e ON e.source_id=h.source_id AND e.block_hash=h.block_hash AND e.log_index=h.log_index
JOIN ens_sync_blocks b ON b.source_id=h.source_id AND b.hash=h.block_hash AND b.canonical
WHERE h.source_id= @source_id AND h.block_hash= @block_hash AND h.kind='lifecycle' AND h.key= @key
 AND he.transaction_hash=origin.transaction_hash AND e.event_name='LabelRegistered'
ORDER BY h.log_index LIMIT 1;
