-- name: ListENSSyncSchedulableCandidates :many
SELECT c.*, j.id AS previous_job_id, COALESCE(j.state='complete' AND j.source_revision=p.revision,false)::boolean AS refresh_only, (j.id IS NOT NULL AND j.state IN ('queued','leased') AND j.source_revision=p.revision AND NOT EXISTS(SELECT 1 FROM ens_sync_job_dependencies d
 JOIN ens_sync_projections fact ON fact.source_id=d.source_id AND fact.kind=d.kind AND fact.key=d.key AND fact.revision=d.revision
 JOIN ens_sync_blocks b ON b.source_id=fact.source_id AND b.hash=fact.block_hash
 WHERE d.source_id=j.source_id AND d.job_id=j.id AND (NOT b.canonical OR EXISTS(
 SELECT 1 FROM ens_sync_projections n JOIN ens_sync_blocks nb ON nb.source_id=n.source_id AND nb.hash=n.block_hash AND nb.canonical
 WHERE n.source_id=d.source_id AND n.kind=d.kind AND n.key=d.key AND n.revision>d.revision))))::boolean AS preserve_budget FROM ens_sync_candidates c
JOIN ens_sync_progress p ON p.source_id=c.source_id
LEFT JOIN ens_sync_jobs j ON j.source_id=c.source_id AND j.candidate_id=c.id AND j.generation=c.requested_generation
WHERE c.source_id= @source_id AND p.state='active' AND p.scanned_hash=p.target_hash AND p.healthy_until>clock_timestamp()
AND (j.id IS NULL OR j.source_revision<>p.revision
 OR (j.state='complete' AND c.refresh_at<=clock_timestamp())
 OR (j.state IN ('queued','leased') AND j.target_hash<>p.target_hash)
 OR EXISTS(SELECT 1 FROM ens_sync_job_dependencies d
   JOIN ens_sync_projections fact ON fact.source_id=d.source_id AND fact.kind=d.kind AND fact.key=d.key AND fact.revision=d.revision
   JOIN ens_sync_blocks b ON b.source_id=fact.source_id AND b.hash=fact.block_hash
   WHERE d.source_id=j.source_id AND d.job_id=j.id AND (NOT b.canonical OR EXISTS(
    SELECT 1 FROM ens_sync_projections newer JOIN ens_sync_blocks nb ON nb.source_id=newer.source_id AND nb.hash=newer.block_hash AND nb.canonical
    WHERE newer.source_id=d.source_id AND newer.kind=d.kind AND newer.key=d.key AND newer.revision>d.revision))))
-- Interleave changed work, indexed addresses, due refreshes and name backfill.
ORDER BY row_number() OVER(PARTITION BY CASE WHEN j.id IS NULL AND c.direction='primary' THEN 1 WHEN j.id IS NULL THEN 3 WHEN j.state='complete' AND c.refresh_at<=clock_timestamp() THEN 2 ELSE 0 END ORDER BY COALESCE(c.refresh_at,c.discovered_at),c.id),
 CASE WHEN j.id IS NULL AND c.direction='primary' THEN 1 WHEN j.id IS NULL THEN 3 WHEN j.state='complete' AND c.refresh_at<=clock_timestamp() THEN 2 ELSE 0 END,c.id LIMIT @row_limit;

-- name: GetENSSyncCandidateByID :one
SELECT * FROM ens_sync_candidates WHERE source_id= @source_id AND id= @candidate_id;

-- name: GetENSSyncWorkerJob :one
SELECT j.*,c.direction,c.lookup_key,c.coin_type,b.number,b.block_time
FROM ens_sync_jobs j JOIN ens_sync_candidates c ON c.source_id=j.source_id AND c.id=j.candidate_id
JOIN ens_sync_blocks b ON b.source_id=j.source_id AND b.hash=j.target_hash
WHERE j.source_id= @source_id AND j.id= @job_id;

-- name: ListENSSyncLatestContractDependencies :many
SELECT p.* FROM ens_sync_projections p JOIN ens_sync_blocks b ON b.source_id=p.source_id AND b.hash=p.block_hash AND b.canonical
WHERE p.source_id= @source_id AND p.deployment= @deployment AND p.kind='lifecycle' AND p.key= @key
ORDER BY p.revision DESC LIMIT 1;

-- name: ListENSSyncJobDependencies :many
SELECT p.* FROM ens_sync_job_dependencies d JOIN ens_sync_projections p
ON p.source_id=d.source_id AND p.kind=d.kind AND p.key=d.key AND p.revision=d.revision
WHERE d.source_id= @source_id AND d.job_id= @job_id ORDER BY d.kind,d.key LIMIT 256;

-- name: GetENSSyncPreviousProjectionObservation :one
SELECT o.* FROM ens_sync_observations o JOIN ens_sync_jobs j ON j.id=o.job_id
WHERE o.source_id= @source_id AND o.candidate_id= @candidate_id AND o.evidence='projection' AND j.state='complete'
AND NOT EXISTS(SELECT 1 FROM ens_sync_job_dependencies d JOIN ens_sync_projections f
 ON f.source_id=d.source_id AND f.kind=d.kind AND f.key=d.key AND f.revision=d.revision
 JOIN ens_sync_blocks b ON b.source_id=f.source_id AND b.hash=f.block_hash
 WHERE d.source_id=o.source_id AND d.job_id=o.job_id AND (NOT b.canonical OR EXISTS(
  SELECT 1 FROM ens_sync_projections newer JOIN ens_sync_blocks nb ON nb.source_id=newer.source_id AND nb.hash=newer.block_hash AND nb.canonical
  WHERE newer.source_id=d.source_id AND newer.kind=d.kind AND newer.key=d.key AND newer.revision>d.revision)))
ORDER BY o.id DESC LIMIT 1;

-- name: EnqueueENSSyncSuccessor :execrows
INSERT INTO ens_sync_jobs(source_id,candidate_id,generation,source_revision,target_hash,core_canonical_at,max_attempts,attempts,deadline,retry_at,state,failure_code)
SELECT c.source_id,c.id,c.requested_generation,p.revision,p.target_hash,cb.updated_at,old.max_attempts,old.attempts,old.deadline,
 GREATEST(old.retry_at,COALESCE(old.lease_until,old.retry_at)),
 CASE WHEN old.attempts>=old.max_attempts OR old.deadline<=clock_timestamp() THEN 'failed' ELSE 'queued' END,
 CASE WHEN old.attempts>=old.max_attempts OR old.deadline<=clock_timestamp() THEN 'retry_exhausted' ELSE NULL END
FROM ens_sync_candidates c JOIN ens_sync_progress p ON p.source_id=c.source_id
JOIN ens_sync_sources s ON s.id=c.source_id
JOIN ens_sync_jobs old ON old.source_id=c.source_id AND old.candidate_id=c.id AND old.id= @previous_job_id
LEFT JOIN canonical_blocks cb ON s.namespace='custom_ens' AND cb.chain_id=s.target_chain_id AND cb.block_hash=p.target_hash
WHERE c.source_id= @source_id AND c.id= @candidate_id
ON CONFLICT(source_id,candidate_id,generation) DO NOTHING;

-- name: RefreshENSSyncCandidate :one
UPDATE ens_sync_candidates SET requested_generation=requested_generation+1,refresh_at=NULL
WHERE source_id= @source_id AND id= @candidate_id AND requested_generation= @expected_generation
RETURNING requested_generation;
