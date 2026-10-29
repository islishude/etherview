-- name: GetENSSyncSource :one
SELECT * FROM ens_sync_sources WHERE id = @source_id;

-- name: GetENSSyncProgress :one
SELECT * FROM ens_sync_progress WHERE source_id = @source_id;

-- name: GetENSSyncSourceBlock :one
SELECT * FROM ens_sync_blocks WHERE source_id = @source_id AND hash = @hash;

-- name: GetENSSyncCoreTip :one
SELECT b.number::text AS number, b.hash, b.parent_hash, b.raw, c.updated_at
FROM canonical_blocks c JOIN blocks b ON b.chain_id=c.chain_id AND b.number=c.number AND b.hash=c.block_hash
JOIN chains ch ON ch.chain_id=c.chain_id
WHERE c.chain_id= @chain_id AND ch.genesis_hash= @genesis_hash
ORDER BY c.number DESC LIMIT 1;

-- name: GetENSSyncCoreBlock :one
SELECT b.number::text AS number, b.hash, b.parent_hash, b.raw, c.updated_at
FROM canonical_blocks c JOIN blocks b ON b.chain_id=c.chain_id AND b.number=c.number AND b.hash=c.block_hash
JOIN chains ch ON ch.chain_id=c.chain_id
WHERE c.chain_id= @chain_id AND ch.genesis_hash= @genesis_hash AND c.number= @number
  AND EXISTS (SELECT 1 FROM core_coverage_ranges coverage WHERE coverage.chain_id=c.chain_id AND coverage.range_start<=c.number AND coverage.range_end>=c.number);

-- name: ListENSSyncCoreLogs :many
SELECT log_index,tx_index,tx_hash,address,raw FROM logs
WHERE chain_id= @chain_id AND block_number= @number AND block_hash= @block_hash AND (@all_emitters::boolean OR address=ANY(@addresses::bytea[]))
ORDER BY log_index LIMIT @row_limit;

-- name: GetENSSyncCoreDeploymentCode :one
SELECT code,code_hash,document_digest FROM (
 SELECT g.code,g.code_hash,i.document_sha256 AS document_digest FROM genesis_account_observations g
 JOIN genesis_state_imports i ON i.chain_id=g.chain_id AND i.block_hash=g.block_hash AND i.state='complete'
 JOIN canonical_blocks c ON c.chain_id=g.chain_id AND c.number=0 AND c.block_hash=g.block_hash
 WHERE g.chain_id= @chain_id AND g.address= @address AND g.block_hash= @block_hash
 UNION ALL
 SELECT o.code,o.code_hash,NULL::bytea AS document_digest FROM contract_code_observations o
 JOIN canonical_blocks c ON c.chain_id=o.chain_id AND c.number=o.block_number AND c.block_hash=o.block_hash
 WHERE o.chain_id= @chain_id AND o.address= @address AND o.block_hash= @block_hash AND o.canonical AND o.block_number > 0 AND o.code IS NOT NULL
) evidence ORDER BY document_digest NULLS LAST LIMIT 1;

-- name: SaveENSSyncScan :exec
INSERT INTO ens_sync_scans(source_id,block_number,block_hash,core_canonical_at)
VALUES (@source_id,@block_number,@block_hash,@core_canonical_at)
ON CONFLICT(source_id,block_hash) DO UPDATE SET core_canonical_at=EXCLUDED.core_canonical_at;

-- name: GetENSSyncScan :one
SELECT * FROM ens_sync_scans WHERE source_id= @source_id AND block_hash= @block_hash;

-- name: ListENSSyncRewindBlocks :many
WITH RECURSIVE lineage AS (
 SELECT b.*,0 AS depth FROM ens_sync_blocks b WHERE b.source_id= @source_id AND b.hash= @through_hash
 UNION ALL
 SELECT parent.*,child.depth+1 FROM lineage child JOIN ens_sync_blocks parent
 ON parent.source_id=child.source_id AND parent.hash=child.parent_hash AND parent.number=child.number-1
 WHERE child.depth+1 < @row_limit
)
SELECT b.number,b.hash,s.core_canonical_at,c.updated_at AS current_attachment,
       COALESCE(c.block_hash=b.hash,false)::boolean AS current_canonical
FROM lineage b JOIN ens_sync_scans s ON b.source_id=s.source_id AND b.hash=s.block_hash
LEFT JOIN canonical_blocks c ON c.chain_id= @chain_id AND c.number=b.number
ORDER BY b.depth LIMIT @row_limit;

-- name: RewindENSSyncProgress :execrows
UPDATE ens_sync_progress SET revision=revision+1,scanned_number=sqlc.narg(scanned_number),scanned_hash=sqlc.narg(scanned_hash),
    state='pending',healthy_until=NULL,failure_code='canonical_rewind'
WHERE source_id= @source_id AND revision= @revision;

-- name: SetENSSyncCollectionTarget :execrows
UPDATE ens_sync_progress p SET target_number=b.number,target_hash=b.hash,healthy_until= @healthy_until,state='active',failure_code=NULL
FROM ens_sync_blocks b WHERE p.source_id= @source_id AND b.source_id=p.source_id AND b.hash= @hash AND b.canonical
  AND p.state NOT IN ('halted','unsupported') AND (p.scanned_number IS NULL OR b.number>=p.scanned_number) AND p.revision= @revision;

-- name: FailENSSyncCollection :execrows
UPDATE ens_sync_progress SET state= CASE WHEN @state::text='pending' AND target_hash IS NOT NULL THEN state ELSE @state::text END, failure_code= @failure_code
WHERE source_id= @source_id AND state NOT IN ('halted','unsupported') AND revision= @revision;

-- name: GetENSSyncLatestProjection :one
SELECT p.* FROM ens_sync_projections p
JOIN ens_sync_blocks b ON b.source_id=p.source_id AND b.hash=p.block_hash AND b.canonical
JOIN ens_sync_sources s ON s.id=p.source_id
WHERE p.source_id= @source_id AND p.kind= @kind AND p.key= @key
  AND (EXISTS(SELECT 1 FROM canonical_blocks c WHERE c.chain_id=s.target_chain_id AND c.number=b.number AND c.block_hash=b.hash))
ORDER BY p.revision DESC LIMIT 1;

-- name: EnsureENSSyncDiscoveredCandidate :one
INSERT INTO ens_sync_candidates(source_id,direction,lookup_key,coin_type)
VALUES (@source_id,@direction,@lookup_key,@coin_type)
ON CONFLICT(source_id,direction,lookup_key,coin_type) DO UPDATE SET lookup_key=EXCLUDED.lookup_key
RETURNING *;

-- name: InsertENSSyncDiscoveryOrigin :execrows
INSERT INTO ens_sync_candidate_origins(source_id,candidate_id,kind,identity)
VALUES (@source_id,@candidate_id,@kind,@identity) ON CONFLICT DO NOTHING;

-- name: AdvanceENSSyncCandidateDiscovery :execrows
UPDATE ens_sync_candidates c SET requested_generation=requested_generation+1,discovery_revision=p.revision,refresh_at=NULL
FROM ens_sync_progress p WHERE c.source_id= @source_id AND c.id= @candidate_id AND p.source_id=c.source_id
AND c.discovery_revision<>p.revision;

-- name: GetENSSyncAddressCursor :one
INSERT INTO ens_sync_address_cursors(source_id) VALUES (@source_id)
ON CONFLICT(source_id) DO UPDATE SET source_id=EXCLUDED.source_id
RETURNING *;

-- name: AdvanceENSSyncAddressCursor :exec
UPDATE ens_sync_address_cursors SET after_address= @after_address,cycle=cycle + @completed_cycle::bigint WHERE source_id= @source_id;

-- name: ListENSSyncKnownAddresses :many
WITH activity AS (
(SELECT DISTINCT decode(substr(lower(t.raw->>'from'),3),'hex') AS address
 FROM transaction_inclusions t JOIN canonical_blocks c ON c.chain_id=t.chain_id AND c.number=t.block_number AND c.block_hash=t.block_hash
 WHERE t.chain_id= @chain_id AND lower(t.raw->>'from') ~ '^0x[0-9a-f]{40}$'
 AND lower(t.raw->>'from') > '0x'||encode(@after_address::bytea,'hex')
 ORDER BY address LIMIT @row_limit)
 UNION
(SELECT DISTINCT decode(substr(lower(t.raw->>'to'),3),'hex') AS address
 FROM transaction_inclusions t JOIN canonical_blocks c ON c.chain_id=t.chain_id AND c.number=t.block_number AND c.block_hash=t.block_hash
 WHERE t.chain_id= @chain_id AND lower(t.raw->>'to') ~ '^0x[0-9a-f]{40}$'
 AND lower(t.raw->>'to') > '0x'||encode(@after_address::bytea,'hex')
 ORDER BY address LIMIT @row_limit)
 UNION
(SELECT DISTINCT decode(substr(lower(t.raw->>'contractAddress'),3),'hex') AS address
 FROM receipts t JOIN canonical_blocks c ON c.chain_id=t.chain_id AND c.number=t.block_number AND c.block_hash=t.block_hash
 WHERE t.chain_id= @chain_id AND lower(t.raw->>'contractAddress') ~ '^0x[0-9a-f]{40}$'
 AND lower(t.raw->>'contractAddress') > '0x'||encode(@after_address::bytea,'hex')
 ORDER BY address LIMIT @row_limit)
 UNION
 (SELECT DISTINCT w.address FROM withdrawals w JOIN canonical_blocks c ON c.chain_id=w.chain_id AND c.number=w.block_number AND c.block_hash=w.block_hash
 WHERE w.chain_id= @chain_id AND w.address> @after_address ORDER BY w.address LIMIT @row_limit)
), genesis AS (
 SELECT g.address FROM genesis_account_observations g JOIN genesis_state_imports i ON i.chain_id=g.chain_id AND i.block_hash=g.block_hash AND i.state='complete'
 JOIN canonical_blocks c ON c.chain_id=g.chain_id AND c.number=0 AND c.block_hash=g.block_hash
 WHERE g.chain_id= @chain_id AND g.address> @after_address ORDER BY g.address LIMIT @row_limit
)
SELECT address, (CASE WHEN EXISTS(SELECT 1 FROM genesis g WHERE g.address=known.address) THEN 'genesis' ELSE 'indexed_address' END)::text AS kind
FROM (SELECT address FROM activity UNION SELECT address FROM genesis) known
WHERE address<>decode(repeat('00',20),'hex') ORDER BY address LIMIT @row_limit;

-- name: PutENSSyncGenesisBaseline :exec
INSERT INTO ens_sync_genesis_baselines(source_id,deployment,genesis_hash,document_digest)
VALUES (@source_id,@deployment,@genesis_hash,@document_digest)
ON CONFLICT(source_id,deployment) DO UPDATE SET genesis_hash=EXCLUDED.genesis_hash,document_digest=EXCLUDED.document_digest;
