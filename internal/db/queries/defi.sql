-- name: RegisterDeFiSource :exec
INSERT INTO defi_sources(id,chain_id,deployment,protocol,manifest_digest)
VALUES(@id,@chain_id,@deployment,@protocol,@manifest_digest) ON CONFLICT(id) DO NOTHING;

-- name: ClaimDeFiSource :one
UPDATE defi_sources SET lease_token= @lease_token,lease_until=clock_timestamp()+interval '60 seconds'
WHERE id= @id AND failures < 5 AND next_run_at<=clock_timestamp() AND (lease_until IS NULL OR lease_until<clock_timestamp()) RETURNING *;

-- name: FenceDeFiSource :one
SELECT id FROM defi_sources WHERE id= @id AND lease_token= @lease_token AND lease_until>clock_timestamp() FOR UPDATE;

-- name: ReleaseDeFiSource :execrows
UPDATE defi_sources SET lease_token=NULL,lease_until=NULL,next_run_at=clock_timestamp()+make_interval(secs=> @delay_seconds::integer)
WHERE id= @id AND lease_token= @lease_token AND lease_until>clock_timestamp();

-- name: LatestDeFiBlock :one
SELECT c.number::text AS number,c.block_hash,c.updated_at FROM canonical_blocks c
JOIN chains ch ON ch.chain_id=c.chain_id AND ch.genesis_hash= @genesis_hash
WHERE c.chain_id= @chain_id AND c.number>= @start_block::numeric AND EXISTS (SELECT 1 FROM core_coverage_ranges r WHERE r.chain_id=c.chain_id AND r.range_start<= @start_block::numeric AND r.range_end>=c.number)
ORDER BY c.number DESC LIMIT 1;

-- name: DeFiCanonicalBlock :one
SELECT updated_at FROM canonical_blocks WHERE chain_id= @chain_id AND number= @block_number AND block_hash= @block_hash;

-- name: ActiveDeFiRun :one
SELECT * FROM defi_runs WHERE source_id= @source_id AND state IN ('seed_instruments','instruments','seed_candidates','candidates','observations');

-- name: InsertDeFiRun :exec
INSERT INTO defi_runs(id,source_id,previous_run,block_number,block_hash,attachment,coverage_start,scan_number,scan_index,instrument_offset,state)
VALUES(@id,@source_id,sqlc.narg(previous_run),@block_number,@block_hash,@attachment,@coverage_start,@scan_number,@scan_index,@instrument_offset,@state);

-- name: InvalidateDeFiRun :exec
UPDATE defi_runs SET state='invalid' WHERE id= @id AND state<>'complete';

-- name: AdvanceDeFiRun :execrows
UPDATE defi_runs SET state= @state,instrument_offset= @instrument_offset,scan_number= @scan_number,scan_index= @scan_index,after_candidate= @after_candidate,
 failure_code=CASE WHEN @failure_code::text='' THEN failure_code ELSE @failure_code::text END,
 published_at=CASE WHEN @state::text='complete' THEN clock_timestamp() ELSE published_at END
WHERE id= @id AND state NOT IN ('complete','invalid');

-- name: InsertDeFiInstrument :exec
INSERT INTO defi_instruments(run_id,address,pool,kind) VALUES(@run_id,@address,@pool,@kind) ON CONFLICT DO NOTHING;

-- name: ListDeFiCandidateLogs :many
SELECT l.raw,i.kind,i.pool FROM logs l
JOIN canonical_blocks c ON c.chain_id=l.chain_id AND c.number=l.block_number AND c.block_hash=l.block_hash
JOIN defi_instruments i ON i.run_id= @run_id AND i.address=l.address
WHERE l.chain_id= @chain_id AND l.block_number>= @start_block::numeric AND l.block_number<= @end_block::numeric
AND (l.block_number,l.log_index)>(@after_number::numeric,@after_index::bigint)
AND l.topic0= @transfer_topic
ORDER BY l.block_number,l.log_index LIMIT 201;

-- name: InsertDeFiCandidate :exec
INSERT INTO defi_candidates(run_id,key,owner,candidate) VALUES(@run_id,@key,@owner,@candidate) ON CONFLICT DO NOTHING;

-- name: ListDeFiCandidates :many
SELECT key,owner,candidate FROM defi_candidates WHERE run_id= @run_id AND key> @after_key ORDER BY key LIMIT 201;

-- name: InsertDeFiObservation :exec
INSERT INTO defi_observations(run_id,key,owner,state,position) VALUES(@run_id,@key,@owner,@state,@position) ON CONFLICT(run_id,key) DO UPDATE SET owner=EXCLUDED.owner,state=EXCLUDED.state,position=EXCLUDED.position;

-- name: ListDeFiPublications :many
SELECT s.id AS source_id,s.protocol,s.deployment,s.failure_code AS source_failure,COALESCE(r.id,'')::text AS run_id,COALESCE(r.block_number::text,'')::text AS block_number,r.block_hash,COALESCE(r.coverage_start::text,'')::text AS coverage_start,r.published_at,COALESCE(r.failure_code,'')::text AS failure_code
FROM defi_sources s
LEFT JOIN LATERAL (
 SELECT dr.* FROM defi_runs dr JOIN canonical_blocks c ON c.chain_id=s.chain_id AND c.number=dr.block_number AND c.block_hash=dr.block_hash AND c.updated_at=dr.attachment
 WHERE dr.source_id=s.id AND dr.state='complete' AND (cardinality(@run_ids::text[])=0 OR dr.id=ANY(@run_ids::text[]))
 ORDER BY dr.block_number DESC,dr.published_at DESC LIMIT 1
) r ON true
WHERE s.id=ANY(@source_ids::text[]) ORDER BY s.protocol,s.deployment;

-- name: ListDeFiPositions :many
SELECT o.position,s.protocol,s.deployment,o.key FROM defi_observations o
JOIN defi_runs r ON r.id=o.run_id JOIN defi_sources s ON s.id=r.source_id
JOIN canonical_blocks c ON c.chain_id=s.chain_id AND c.number=r.block_number AND c.block_hash=r.block_hash AND c.updated_at=r.attachment
WHERE o.run_id=ANY(@run_ids::text[]) AND o.owner= @owner AND o.state<>'closed' AND r.state='complete'
AND (s.protocol,s.deployment,o.key)>(@after_protocol::text,@after_deployment::text,@after_key::text)
ORDER BY s.protocol,s.deployment,o.key LIMIT @row_limit;

-- name: FailDeFiSource :execrows
UPDATE defi_sources SET failure_code= @failure_code,failures=failures+1,lease_token=NULL,lease_until=NULL,next_run_at=clock_timestamp()+interval '30 seconds'
WHERE id= @id AND lease_token= @lease_token AND lease_until>clock_timestamp();

-- name: ClearDeFiSourceFailure :exec
UPDATE defi_sources SET failure_code='',failures=0 WHERE id= @id AND lease_token= @lease_token AND lease_until>clock_timestamp();

-- name: GetDeFiRun :one
SELECT * FROM defi_runs WHERE id= @id;

-- name: ListDeFiSeedInstruments :many
SELECT address,pool,kind FROM defi_instruments WHERE run_id= @run_id AND address> @after_address ORDER BY address LIMIT 201;

-- name: ListDeFiSeedCandidates :many
SELECT c.key,c.owner,c.candidate FROM defi_candidates c
JOIN defi_observations o ON o.run_id=c.run_id AND o.key=c.key AND o.state<>'closed'
WHERE c.run_id= @previous_run AND c.key> @after_key
AND EXISTS (SELECT 1 FROM defi_instruments i WHERE i.run_id= @run_id AND encode(i.address,'hex')=lower(substring(c.candidate->>'contract' FROM 3)))
ORDER BY c.key LIMIT 201;

-- name: EmitDeFiPublication :exec
INSERT INTO runtime_events(chain_id,event_type,payload)
VALUES(@chain_id,'status',jsonb_build_object('reason','defi_publication','deployment',@deployment::text,'block_number',@block_number::text));

-- name: CountDeFiBlockPublications :one
SELECT count(*) FROM defi_runs WHERE source_id= @source_id AND block_hash= @block_hash AND attachment= @attachment AND state='complete';
