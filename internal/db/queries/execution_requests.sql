-- name: RequestsWriteSet :execrows
INSERT INTO execution_request_sets(chain_id,block_number,block_hash,manifest,canonical,durable_job_id,job_generation)
VALUES(sqlc.arg(chain_id)::numeric,sqlc.arg(block_number)::numeric,sqlc.arg(block_hash),sqlc.arg(manifest)::jsonb,true,sqlc.arg(durable_job_id),sqlc.arg(job_generation))
ON CONFLICT(chain_id,block_number,block_hash) DO UPDATE SET canonical=true,durable_job_id=EXCLUDED.durable_job_id,job_generation=EXCLUDED.job_generation
WHERE execution_request_sets.manifest=EXCLUDED.manifest;

-- name: RequestsWriteItems :many
INSERT INTO execution_requests(chain_id,block_number,block_hash,request_type,sequence_index,record)
SELECT sqlc.arg(chain_id)::numeric,sqlc.arg(block_number)::numeric,sqlc.arg(block_hash),
 (('x'||substring(item->>'request_type' FROM 3))::bit(8)::integer)::smallint,
 (item->>'sequence_index')::integer,item
FROM jsonb_array_elements(sqlc.arg(items)::jsonb) AS item
ON CONFLICT(chain_id,block_number,block_hash,request_type,sequence_index) DO UPDATE SET record=execution_requests.record
WHERE execution_requests.record=EXCLUDED.record
RETURNING sequence_index;

-- name: RequestsRetainedSet :one
SELECT manifest FROM execution_request_sets
WHERE chain_id=sqlc.arg(chain_id)::numeric AND block_number=sqlc.arg(block_number)::numeric AND block_hash=sqlc.arg(block_hash);

-- name: RequestsRetainedItems :many
SELECT record FROM execution_requests
WHERE chain_id=sqlc.arg(chain_id)::numeric AND block_number=sqlc.arg(block_number)::numeric AND block_hash=sqlc.arg(block_hash)
ORDER BY request_type,sequence_index LIMIT 40961;

-- name: RequestsReadSet :one
SELECT o.manifest,o.durable_job_id,o.job_generation,o.block_number::text AS block_number
FROM execution_request_sets o JOIN published_block_stage_results p
ON p.chain_id=o.chain_id AND p.block_number=o.block_number AND p.block_hash=o.block_hash
AND p.stage='requests' AND p.stage_version=1 AND p.state='complete'
AND p.durable_job_id=o.durable_job_id AND p.job_generation=o.job_generation
WHERE o.chain_id=sqlc.arg(chain_id)::numeric AND o.block_hash=sqlc.arg(block_hash);

-- name: RequestsListItems :many
SELECT record,request_type,sequence_index FROM execution_requests
WHERE chain_id=sqlc.arg(chain_id)::numeric AND block_number=sqlc.arg(block_number)::numeric AND block_hash=sqlc.arg(block_hash)
AND (sqlc.arg(filter_type)::integer=-1 OR request_type=sqlc.arg(filter_type)::integer)
AND (request_type,sequence_index)>(sqlc.arg(after_type)::integer,sqlc.arg(after_index)::integer)
ORDER BY request_type,sequence_index LIMIT sqlc.arg(page_limit);
