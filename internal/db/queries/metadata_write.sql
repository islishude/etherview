-- name: MetadataWriteEnqueueMetadataJob :one
INSERT INTO durable_jobs (
    chain_id, kind, stage, stage_version, idempotency_key, payload,
    priority, max_attempts
) VALUES (
    sqlc.arg('chain_id')::numeric, 'metadata', 'nft-metadata', 1, sqlc.arg('idempotency_key'),
    sqlc.arg('payload')::jsonb, sqlc.arg('priority'), sqlc.arg('max_attempts')
)
ON CONFLICT (chain_id, kind, idempotency_key) DO NOTHING
RETURNING id;

-- name: MetadataWriteFinishMetadataJob :execrows
UPDATE durable_jobs
SET status = sqlc.arg('status'), result = sqlc.arg('result')::jsonb, last_error = sqlc.narg('last_error'),
    leased_by = NULL, lease_token = NULL, lease_expires_at = NULL,
    updated_at = clock_timestamp()
WHERE id = sqlc.arg('id') AND kind = 'metadata' AND status = 'leased'
  AND lease_token = sqlc.arg('lease_token') AND lease_expires_at > clock_timestamp();
-- name: MetadataWriteFinishMetadataResource :execrows
UPDATE external_metadata
SET state = sqlc.arg('state'), resolved_uri = sqlc.narg('resolved_uri'), media_type = sqlc.narg('media_type'), content_hash = sqlc.arg('content_hash'),
    document = sqlc.arg('document')::jsonb, content_size = sqlc.narg('content_size'), attempt_count = sqlc.arg('attempt_count'),
    last_error_code = sqlc.narg('last_error_code'), last_error = sqlc.narg('last_error'),
    fetched_at = clock_timestamp(), terminal_at = clock_timestamp(), updated_at = clock_timestamp()
WHERE chain_id = sqlc.arg('chain_id')::numeric AND resource_kind = 'nft' AND resource_key = sqlc.arg('resource_key')
  AND identity_hash = sqlc.arg('identity_hash')
  AND source_uri = sqlc.arg('source_uri') AND observed_block_number = sqlc.arg('observed_block_number')::numeric AND observed_block_hash = sqlc.arg('identity_hash');

-- name: MetadataWriteInsertMetadataAttempt :exec
INSERT INTO external_metadata_attempts (
    chain_id, resource_kind, resource_key, durable_job_id, attempt, state,
    source_uri, resolved_uri, media_type, content_hash, content_size,
    error_code, error_message
) VALUES (
    sqlc.arg('chain_id')::numeric, 'nft', sqlc.arg('resource_key'), sqlc.arg('durable_job_id'), sqlc.arg('attempt'), sqlc.arg('state'), sqlc.arg('source_uri'), sqlc.narg('resolved_uri'), sqlc.narg('media_type'), sqlc.arg('content_hash'), sqlc.narg('content_size'), sqlc.narg('error_code'), sqlc.narg('error_message')
)
ON CONFLICT (durable_job_id, attempt) DO UPDATE SET
    state = EXCLUDED.state, resolved_uri = EXCLUDED.resolved_uri,
    media_type = EXCLUDED.media_type, content_hash = EXCLUDED.content_hash,
    content_size = EXCLUDED.content_size, error_code = EXCLUDED.error_code,
    error_message = EXCLUDED.error_message, attempted_at = clock_timestamp();

-- name: MetadataWriteInsertMetadataResource :one
INSERT INTO external_metadata (
    chain_id, resource_kind, resource_key, source_uri, state,
    token_address, token_id, observed_block_number, observed_block_hash,
    identity_hash, attempt_count, updated_at
) VALUES (
    sqlc.arg('chain_id')::numeric, 'nft', sqlc.arg('resource_key'), sqlc.arg('source_uri'), 'pending',
    sqlc.arg('token_address'), sqlc.arg('token_id')::numeric, sqlc.arg('observed_block_number')::numeric, sqlc.arg('observed_block_hash'),
    sqlc.arg('observed_block_hash'), 0, clock_timestamp()
)
ON CONFLICT DO NOTHING
RETURNING 1 AS inserted;

-- name: MetadataWriteInsertNFTSource :one
INSERT INTO nft_metadata_source_observations (
    chain_id, token_address, token_id, block_number, block_hash,
    standard, state, source_uri, error_code
) VALUES (
    sqlc.arg('chain_id')::numeric, sqlc.arg('token_address'), sqlc.arg('token_id')::numeric, sqlc.arg('block_number')::numeric, sqlc.arg('block_hash'),
    sqlc.arg('standard'), sqlc.arg('state'), sqlc.narg('source_uri'), sqlc.narg('error_code')
)
ON CONFLICT DO NOTHING
RETURNING 1 AS inserted;

-- name: MetadataWriteInsertNFTUpdateObservation :one
WITH canonical AS (
    SELECT 1
    FROM canonical_blocks
    WHERE chain_id = sqlc.arg('chain_id')::numeric
      AND number = sqlc.arg('block_number')::numeric
      AND block_hash = sqlc.arg('block_hash')
    FOR KEY SHARE
)
INSERT INTO nft_metadata_update_observations (
    chain_id, block_number, block_hash, log_index, token_address,
    standard, event_kind, state, from_token_id, to_token_id, error_code
)
SELECT
    sqlc.arg('chain_id')::numeric, sqlc.arg('block_number')::numeric, sqlc.arg('block_hash'), sqlc.arg('log_index'), sqlc.arg('token_address'),
    sqlc.arg('standard'), sqlc.arg('event_kind'), sqlc.arg('state'), sqlc.narg('from_token_id')::numeric, sqlc.narg('to_token_id')::numeric, sqlc.narg('error_code')
FROM canonical
ON CONFLICT DO NOTHING
RETURNING 1 AS inserted;

-- name: MetadataWriteRecordMetadataRetry :execrows
UPDATE external_metadata
SET state = 'pending', attempt_count = sqlc.arg('attempt_count'), last_error_code = sqlc.arg('last_error_code'), last_error = sqlc.arg('last_error'),
    fetched_at = clock_timestamp(), terminal_at = NULL, updated_at = clock_timestamp()
WHERE chain_id = sqlc.arg('chain_id')::numeric AND resource_kind = 'nft' AND resource_key = sqlc.arg('resource_key')
  AND identity_hash = sqlc.arg('identity_hash')
  AND source_uri = sqlc.arg('source_uri') AND observed_block_number = sqlc.arg('observed_block_number')::numeric AND observed_block_hash = sqlc.arg('identity_hash');

-- name: MetadataWriteRenewMetadataJob :execrows
UPDATE durable_jobs
SET lease_expires_at = clock_timestamp() + (sqlc.arg('lease_microseconds')::bigint * INTERVAL '1 microsecond'),
    updated_at = clock_timestamp()
WHERE id = sqlc.arg('i_d') AND kind = 'metadata' AND status = 'leased'
  AND lease_token = sqlc.arg('lease_token') AND lease_expires_at > clock_timestamp();

-- name: MetadataWriteRetryMetadataJob :execrows
UPDATE durable_jobs
SET status = 'queued', available_at = clock_timestamp() + (sqlc.arg('retry_microseconds')::bigint * INTERVAL '1 microsecond'),
    last_error = sqlc.arg('last_error'), result = NULL,
    leased_by = NULL, lease_token = NULL, lease_expires_at = NULL,
    updated_at = clock_timestamp()
WHERE id = sqlc.arg('id') AND kind = 'metadata' AND status = 'leased'
  AND lease_token = sqlc.arg('lease_token') AND lease_expires_at > clock_timestamp();
