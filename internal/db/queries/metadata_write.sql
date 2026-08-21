-- name: MetadataWriteEnqueueMetadataJob :many
INSERT INTO durable_jobs (
    chain_id, kind, stage, stage_version, idempotency_key, payload,
    priority, max_attempts
) VALUES (
    $1::numeric, 'metadata', 'nft-metadata', 1, $2,
    $3::jsonb, $4, $5
)
ON CONFLICT (chain_id, kind, idempotency_key) DO NOTHING
RETURNING id;

-- name: MetadataWriteFinishMetadataJob :exec
UPDATE durable_jobs
SET status = $3, result = $4::jsonb, last_error = $5,
    leased_by = NULL, lease_token = NULL, lease_expires_at = NULL,
    updated_at = clock_timestamp()
WHERE id = $1 AND kind = 'metadata' AND status = 'leased'
  AND lease_token = $2 AND lease_expires_at > clock_timestamp();
-- name: MetadataWriteFinishMetadataResource :exec
UPDATE external_metadata
SET state = $6, resolved_uri = $7, media_type = $8, content_hash = $9,
    document = $10::jsonb, content_size = $11, attempt_count = $12,
    last_error_code = $13, last_error = $14,
    fetched_at = clock_timestamp(), terminal_at = clock_timestamp(), updated_at = clock_timestamp()
WHERE chain_id = $1::numeric AND resource_kind = 'nft' AND resource_key = $2
  AND identity_hash = $5
  AND source_uri = $3 AND observed_block_number = $4::numeric AND observed_block_hash = $5;

-- name: MetadataWriteInsertMetadataAttempt :exec
INSERT INTO external_metadata_attempts (
    chain_id, resource_kind, resource_key, durable_job_id, attempt, state,
    source_uri, resolved_uri, media_type, content_hash, content_size,
    error_code, error_message
) VALUES (
    $1::numeric, 'nft', $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
)
ON CONFLICT (durable_job_id, attempt) DO UPDATE SET
    state = EXCLUDED.state, resolved_uri = EXCLUDED.resolved_uri,
    media_type = EXCLUDED.media_type, content_hash = EXCLUDED.content_hash,
    content_size = EXCLUDED.content_size, error_code = EXCLUDED.error_code,
    error_message = EXCLUDED.error_message, attempted_at = clock_timestamp();

-- name: MetadataWriteInsertMetadataResource :many
INSERT INTO external_metadata (
    chain_id, resource_kind, resource_key, source_uri, state,
    token_address, token_id, observed_block_number, observed_block_hash,
    identity_hash, attempt_count, updated_at
) VALUES (
    $1::numeric, 'nft', $2, $3, 'pending',
    $4, $5::numeric, $6::numeric, $7,
    $7, 0, clock_timestamp()
)
ON CONFLICT DO NOTHING
RETURNING 1;

-- name: MetadataWriteInsertNFTSource :many
INSERT INTO nft_metadata_source_observations (
    chain_id, token_address, token_id, block_number, block_hash,
    standard, state, source_uri, error_code
) VALUES (
    $1::numeric, $2, $3::numeric, $4::numeric, $5,
    $6, $7, $8, $9
)
ON CONFLICT DO NOTHING
RETURNING 1;

-- name: MetadataWriteRecordMetadataRetry :exec
UPDATE external_metadata
SET state = 'pending', attempt_count = $6, last_error_code = $7, last_error = $8,
    fetched_at = clock_timestamp(), terminal_at = NULL, updated_at = clock_timestamp()
WHERE chain_id = $1::numeric AND resource_kind = 'nft' AND resource_key = $2
  AND identity_hash = $5
  AND source_uri = $3 AND observed_block_number = $4::numeric AND observed_block_hash = $5;

-- name: MetadataWriteRenewMetadataJob :exec
UPDATE durable_jobs
SET lease_expires_at = clock_timestamp() + ($3 * INTERVAL '1 microsecond'),
    updated_at = clock_timestamp()
WHERE id = $1 AND kind = 'metadata' AND status = 'leased'
  AND lease_token = $2 AND lease_expires_at > clock_timestamp();

-- name: MetadataWriteRetryMetadataJob :exec
UPDATE durable_jobs
SET status = 'queued', available_at = clock_timestamp() + ($4 * INTERVAL '1 microsecond'),
    last_error = $3, result = NULL,
    leased_by = NULL, lease_token = NULL, lease_expires_at = NULL,
    updated_at = clock_timestamp()
WHERE id = $1 AND kind = 'metadata' AND status = 'leased'
  AND lease_token = $2 AND lease_expires_at > clock_timestamp();
