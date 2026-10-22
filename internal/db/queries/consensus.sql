-- name: ConsensusSource :one
SELECT raw FROM blocks WHERE chain_id = sqlc.arg(chain_id)::numeric AND number = sqlc.arg(block_number)::numeric AND hash = sqlc.arg(block_hash);

-- name: ConsensusBindNetwork :execrows
INSERT INTO consensus_networks (chain_id, identity) VALUES (sqlc.arg(chain_id)::numeric, sqlc.arg(identity)::jsonb)
ON CONFLICT (chain_id) DO UPDATE SET identity = consensus_networks.identity
WHERE consensus_networks.identity = EXCLUDED.identity;

-- name: ConsensusNetwork :one
SELECT identity FROM consensus_networks WHERE chain_id = sqlc.arg(chain_id)::numeric;

-- name: ConsensusRetainedManifest :one
SELECT manifest FROM blob_manifests
WHERE chain_id=sqlc.arg(chain_id)::numeric AND block_number=sqlc.arg(block_number)::numeric AND block_hash=sqlc.arg(block_hash);

-- name: ConsensusRetainedObservation :one
SELECT observation FROM consensus_observations
WHERE chain_id=sqlc.arg(chain_id)::numeric AND block_number=sqlc.arg(block_number)::numeric AND block_hash=sqlc.arg(block_hash);

-- name: ConsensusWriteObservation :execrows
INSERT INTO consensus_observations (chain_id,block_number,block_hash,slot,observation,canonical,durable_job_id,job_generation)
VALUES (sqlc.arg(chain_id)::numeric,sqlc.arg(block_number)::numeric,sqlc.arg(block_hash),sqlc.arg(slot)::numeric,sqlc.arg(observation)::jsonb,true,sqlc.arg(durable_job_id),sqlc.arg(job_generation))
ON CONFLICT (chain_id,block_number,block_hash) DO UPDATE SET canonical=true,durable_job_id=EXCLUDED.durable_job_id,job_generation=EXCLUDED.job_generation
WHERE consensus_observations.observation = EXCLUDED.observation;

-- name: ConsensusWriteManifest :execrows
INSERT INTO blob_manifests (chain_id,block_number,block_hash,manifest,canonical,durable_job_id,job_generation)
VALUES (sqlc.arg(chain_id)::numeric,sqlc.arg(block_number)::numeric,sqlc.arg(block_hash),sqlc.arg(manifest)::jsonb,true,sqlc.arg(durable_job_id),sqlc.arg(job_generation))
ON CONFLICT (chain_id,block_number,block_hash) DO UPDATE SET canonical=true,durable_job_id=EXCLUDED.durable_job_id,job_generation=EXCLUDED.job_generation
WHERE blob_manifests.manifest = EXCLUDED.manifest;

-- name: ConsensusReadObservation :one
SELECT o.observation, o.slot::text AS slot, o.durable_job_id, o.job_generation
FROM consensus_observations o JOIN published_block_stage_results p
ON p.chain_id=o.chain_id AND p.block_number=o.block_number AND p.block_hash=o.block_hash
AND p.stage='consensus' AND p.stage_version=1 AND p.state='complete'
AND p.durable_job_id=o.durable_job_id AND p.job_generation=o.job_generation
WHERE o.chain_id=sqlc.arg(chain_id)::numeric AND o.block_hash=sqlc.arg(block_hash);

-- name: ConsensusReadManifest :one
SELECT o.manifest, o.durable_job_id, o.job_generation
FROM blob_manifests o JOIN published_block_stage_results p
ON p.chain_id=o.chain_id AND p.block_number=o.block_number AND p.block_hash=o.block_hash
AND p.stage='blob' AND p.stage_version=1 AND p.state='complete'
AND p.durable_job_id=o.durable_job_id AND p.job_generation=o.job_generation
WHERE o.chain_id=sqlc.arg(chain_id)::numeric AND o.block_hash=sqlc.arg(block_hash);

-- name: ConsensusStageState :one
SELECT state, COALESCE(last_error,'')::text AS code FROM published_block_stage_results
WHERE chain_id=sqlc.arg(chain_id)::numeric AND block_hash=sqlc.arg(block_hash) AND stage=sqlc.arg(stage) AND stage_version=1;

-- name: ConsensusDiscover :many
WITH recent AS (
SELECT c.number,c.block_hash FROM canonical_blocks c
JOIN consensus_discovery_cursors d ON d.chain_id=c.chain_id AND d.stage=sqlc.arg(stage)
WHERE c.chain_id=sqlc.arg(chain_id)::numeric AND d.latest_number>=0 AND c.number>d.latest_number
ORDER BY c.number ASC LIMIT 32
), historical AS (
SELECT c.number,c.block_hash FROM canonical_blocks c
LEFT JOIN consensus_discovery_cursors d ON d.chain_id=c.chain_id AND d.stage=sqlc.arg(stage)
WHERE c.chain_id=sqlc.arg(chain_id)::numeric AND c.number<=COALESCE(d.next_number,18446744073709551615)
ORDER BY c.number DESC LIMIT 32
), candidates AS (
SELECT * FROM recent UNION SELECT * FROM historical
)
SELECT number::text AS block_number,block_hash,d.generation,
 COALESCE(o.id::text||':'||o.generation::text,'')::text AS replay_key
FROM candidates CROSS JOIN consensus_discovery_cursors d
LEFT JOIN transactional_outbox o ON o.chain_id=d.chain_id AND o.topic='core.block.canonical'
AND o.message_key='0x'||encode(candidates.block_hash,'hex')
WHERE d.chain_id=sqlc.arg(chain_id)::numeric AND d.stage=sqlc.arg(stage)
ORDER BY number DESC;

-- name: ConsensusInitializeDiscovery :exec
INSERT INTO consensus_discovery_cursors(chain_id,stage,next_number,latest_number)
VALUES(sqlc.arg(chain_id)::numeric,sqlc.arg(stage),18446744073709551615,-1)
ON CONFLICT DO NOTHING;

-- name: ConsensusAdvanceDiscovery :exec
UPDATE consensus_discovery_cursors SET
 next_number=LEAST(next_number,sqlc.arg(last_number)::numeric-1),
 latest_number=GREATEST(latest_number,sqlc.arg(latest_number)::numeric)
WHERE chain_id=sqlc.arg(chain_id)::numeric AND stage=sqlc.arg(stage)
AND generation=sqlc.arg(generation);

-- name: ConsensusHeadSource :one
SELECT b.raw FROM canonical_blocks c JOIN blocks b ON b.chain_id=c.chain_id AND b.number=c.number AND b.hash=c.block_hash
WHERE c.chain_id=sqlc.arg(chain_id)::numeric ORDER BY c.number DESC LIMIT 1;

-- name: ConsensusClaimPoll :one
INSERT INTO consensus_overviews(chain_id,generation,lease_until) VALUES(sqlc.arg(chain_id)::numeric,1,clock_timestamp()+sqlc.arg(lease_ms)::bigint*interval '1 millisecond')
ON CONFLICT(chain_id) DO UPDATE SET generation=consensus_overviews.generation+1,lease_until=EXCLUDED.lease_until
WHERE consensus_overviews.lease_until<clock_timestamp() AND consensus_overviews.next_poll_at<=clock_timestamp()
RETURNING generation;

-- name: ConsensusPublishOverview :execrows
UPDATE consensus_overviews SET overview=CASE WHEN sqlc.arg(error_code)::text='' THEN sqlc.arg(overview)::jsonb ELSE overview END,
 observed_at=CASE WHEN sqlc.arg(error_code)::text='' THEN clock_timestamp() ELSE observed_at END,
 last_error=sqlc.arg(error_code),lease_until='-infinity',next_poll_at=clock_timestamp()+sqlc.arg(poll_ms)::bigint*interval '1 millisecond'
WHERE chain_id=sqlc.arg(chain_id)::numeric AND generation=sqlc.arg(generation) AND lease_until>clock_timestamp();

-- name: ConsensusReadOverview :one
SELECT overview,observed_at,last_error FROM consensus_overviews WHERE chain_id=sqlc.arg(chain_id)::numeric;

-- name: ConsensusEvent :exec
INSERT INTO runtime_events(chain_id,event_type,payload) VALUES(sqlc.arg(chain_id)::numeric,'status','{"consensus_changed":true}'::jsonb);

-- name: ConsensusSlots :many
SELECT o.block_hash,o.slot::text AS slot FROM consensus_observations o
JOIN published_block_stage_results p ON p.chain_id=o.chain_id AND p.block_number=o.block_number AND p.block_hash=o.block_hash
AND p.stage='consensus' AND p.stage_version=1 AND p.state='complete' AND p.durable_job_id=o.durable_job_id AND p.job_generation=o.job_generation
WHERE o.chain_id=sqlc.arg(chain_id)::numeric AND o.block_hash=ANY(sqlc.arg(block_hashes)::bytea[]);

-- name: ConsensusBlobReferences :many
SELECT (row_number() OVER (ORDER BY i.tx_index, h.ordinality)-1)::text AS blob_index,
 ('0x'||encode(i.tx_hash,'hex'))::text AS transaction_hash,
 (h.ordinality-1)::text AS transaction_index,h.value::text AS versioned_hash
FROM transaction_inclusions i JOIN transactions t ON t.chain_id=i.chain_id AND t.hash=i.tx_hash
CROSS JOIN LATERAL jsonb_array_elements_text(COALESCE(t.raw->'blobVersionedHashes','[]'::jsonb)) WITH ORDINALITY h(value,ordinality)
WHERE i.chain_id=sqlc.arg(chain_id)::numeric AND i.block_hash=sqlc.arg(block_hash)
ORDER BY i.tx_index,h.ordinality LIMIT 4096;

-- name: ConsensusBlobHealth :one
SELECT p.state, COALESCE(p.last_error,'')::text AS code FROM published_block_stage_results p
JOIN canonical_blocks c ON c.chain_id=p.chain_id AND c.number=p.block_number AND c.block_hash=p.block_hash
WHERE p.chain_id=sqlc.arg(chain_id)::numeric AND p.stage='blob' AND p.stage_version=1
ORDER BY p.block_number DESC LIMIT 1;
