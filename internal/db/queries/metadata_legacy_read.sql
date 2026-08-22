-- name: MetadataAnyNFTMetadata :many
SELECT EXISTS (
    SELECT 1 FROM external_metadata
    WHERE chain_id = $1::numeric AND resource_kind = 'nft'
      AND token_address = $2 AND token_id = $3::numeric
);

-- name: MetadataCanonicalNFTContract :many
SELECT EXISTS (
    SELECT 1
    FROM token_contracts AS token
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = token.chain_id
     AND canonical.number = token.observed_block_number
     AND canonical.block_hash = token.observed_block_hash
    WHERE token.chain_id = $1::numeric
      AND token.address = $2
      AND token.standard IN ('erc721', 'erc1155')
);

-- name: MetadataCanonicalObservation :many
SELECT EXISTS (
    SELECT 1 FROM canonical_blocks
    WHERE chain_id = $1::numeric AND number = $2::numeric AND block_hash = $3
);

-- name: MetadataClaimMetadataJob :many
WITH candidate AS (
    SELECT id FROM durable_jobs
    WHERE kind = 'metadata'
      AND chain_id = $4::numeric
      AND stage = 'nft-metadata' AND stage_version = 1
      AND attempts < max_attempts
      AND ((status = 'queued' AND available_at <= clock_timestamp())
        OR (status = 'leased' AND lease_expires_at <= clock_timestamp()))
    ORDER BY priority DESC, available_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE durable_jobs AS job
SET status = 'leased', attempts = job.attempts + 1,
    leased_by = $1, lease_token = $2,
    lease_expires_at = clock_timestamp() + ($3 * INTERVAL '1 microsecond'),
    result = NULL, updated_at = clock_timestamp()
FROM candidate
WHERE job.id = candidate.id
RETURNING job.id, job.chain_id::text, job.attempts, job.max_attempts, job.payload;

-- name: MetadataCurrentMetadataResource :many
SELECT
    EXISTS (
        SELECT 1 FROM external_metadata
        WHERE chain_id = $1::numeric AND resource_kind = 'nft' AND resource_key = $2
          AND identity_hash = $6
          AND token_address = $3 AND token_id = $4::numeric
          AND observed_block_number = $5::numeric AND observed_block_hash = $6
          AND source_uri = $7
    ),
    EXISTS (
        SELECT 1 FROM canonical_blocks
        WHERE chain_id = $1::numeric AND number = $5::numeric AND block_hash = $6
    );

-- name: MetadataCurrentNFTImage :many
SELECT EXISTS (
    SELECT 1
    FROM external_metadata AS metadata
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = metadata.chain_id
     AND canonical.number = metadata.observed_block_number
     AND canonical.block_hash = metadata.observed_block_hash
    WHERE metadata.chain_id = $1::numeric
      AND metadata.resource_kind = 'nft'
      AND metadata.token_address = $2
      AND metadata.token_id = $3::numeric
      AND metadata.observed_block_number = $4::numeric
      AND metadata.observed_block_hash = $5
      AND metadata.state = 'available'
      AND jsonb_typeof(metadata.document -> 'image') = 'string'
      AND btrim(metadata.document ->> 'image') = $6
      AND NOT EXISTS (
          SELECT 1
          FROM external_metadata AS newer
          JOIN canonical_blocks AS newer_canonical
            ON newer_canonical.chain_id = newer.chain_id
           AND newer_canonical.number = newer.observed_block_number
           AND newer_canonical.block_hash = newer.observed_block_hash
          WHERE newer.chain_id = metadata.chain_id
            AND newer.resource_kind = 'nft'
            AND newer.token_address = metadata.token_address
            AND newer.token_id = metadata.token_id
            AND newer.observed_block_number > metadata.observed_block_number
      )
);

-- name: MetadataExhaustMetadataJobs :exec
WITH exhausted AS (
    UPDATE durable_jobs
    SET status = 'failed',
        result = jsonb_build_object('state', 'error', 'code', 'attempts_exhausted'),
        last_error = COALESCE(last_error, 'maximum metadata attempts exhausted'),
        leased_by = NULL, lease_token = NULL, lease_expires_at = NULL,
        updated_at = clock_timestamp()
    WHERE kind = 'metadata'
      AND chain_id = $1::numeric
      AND stage = 'nft-metadata' AND stage_version = 1
      AND attempts >= max_attempts
      AND ((status = 'queued' AND available_at <= clock_timestamp())
        OR (status = 'leased' AND lease_expires_at <= clock_timestamp()))
    RETURNING id, chain_id, attempts, payload, last_error
), updated AS (
    UPDATE external_metadata AS metadata
    SET state = 'error', attempt_count = exhausted.attempts,
        last_error_code = 'attempts_exhausted', last_error = exhausted.last_error,
        fetched_at = clock_timestamp(), terminal_at = clock_timestamp(), updated_at = clock_timestamp()
    FROM exhausted
    WHERE metadata.chain_id = exhausted.chain_id
      AND metadata.resource_kind = 'nft'
      AND metadata.resource_key = exhausted.payload->>'resource_key'
      AND metadata.identity_hash = decode(substr(exhausted.payload->>'block_hash', 3), 'hex')
      AND metadata.source_uri = exhausted.payload->>'source_uri'
      AND metadata.observed_block_number = (exhausted.payload->>'block_number')::numeric
      AND metadata.observed_block_hash = decode(substr(exhausted.payload->>'block_hash', 3), 'hex')
    RETURNING exhausted.id, exhausted.chain_id, exhausted.attempts,
        exhausted.payload, exhausted.last_error
)
INSERT INTO external_metadata_attempts (
    chain_id, resource_kind, resource_key, durable_job_id, attempt, state,
    source_uri, error_code, error_message
)
SELECT chain_id, 'nft', payload->>'resource_key', id, attempts, 'error',
       payload->>'source_uri', 'attempts_exhausted', left(last_error, 1024)
FROM updated
ON CONFLICT (durable_job_id, attempt) DO NOTHING;

-- name: MetadataExistingMetadataJob :many
SELECT id FROM durable_jobs
WHERE chain_id = $1::numeric AND kind = 'metadata' AND idempotency_key = $2;

-- name: MetadataExistingMetadataResource :many
SELECT resource_key, source_uri, token_address, token_id::text,
       observed_block_number::text, observed_block_hash
FROM external_metadata
WHERE chain_id = $1::numeric AND resource_kind = 'nft'
  AND token_address = $2 AND token_id = $3::numeric AND observed_block_hash = $4
FOR UPDATE;

-- name: MetadataExistingNFTSource :many
SELECT token_address, token_id::text, block_number::text, block_hash,
       standard, state, source_uri, error_code
FROM nft_metadata_source_observations
WHERE chain_id = $1::numeric AND token_address = $2
  AND token_id = $3::numeric AND block_hash = $4;

-- name: MetadataLockMetadataResource :many
SELECT token_address = $3
   AND token_id = $4::numeric
   AND observed_block_number = $5::numeric
   AND observed_block_hash = $6
   AND source_uri = $7
FROM external_metadata
WHERE chain_id = $1::numeric AND resource_kind = 'nft' AND resource_key = $2
  AND identity_hash = $6
FOR UPDATE;

-- name: MetadataLockOwnedMetadataJob :many
SELECT chain_id::text, payload, max_attempts
FROM durable_jobs
WHERE id = $1 AND kind = 'metadata' AND status = 'leased'
  AND lease_token = $2 AND lease_expires_at > clock_timestamp()
FOR UPDATE;

-- name: MetadataNextNFTSource :many
SELECT event.token_address, event.token_id::text, event.block_number::text,
       event.block_hash, event.standard
FROM token_events AS event
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = event.chain_id
 AND canonical.number = event.block_number
 AND canonical.block_hash = event.block_hash
WHERE event.chain_id = $1::numeric
  AND event.token_id IS NOT NULL
  AND event.standard IN ('erc721', 'erc1155')
  AND NOT EXISTS (
      SELECT 1
      FROM external_metadata AS metadata
      WHERE metadata.chain_id = event.chain_id
        AND metadata.resource_kind = 'nft'
        AND metadata.token_address = event.token_address
        AND metadata.token_id = event.token_id
        AND metadata.observed_block_hash = event.block_hash
  )
  AND NOT EXISTS (
      SELECT 1
      FROM nft_metadata_source_observations AS source
      WHERE source.chain_id = event.chain_id
        AND source.token_address = event.token_address
        AND source.token_id = event.token_id
        AND source.block_hash = event.block_hash
  )
ORDER BY event.block_number, event.log_index, event.sub_index,
         event.token_address, event.token_id
LIMIT 1;

-- name: MetadataSelectCanonicalNFTImage :many
SELECT metadata.state,
       CASE
           WHEN jsonb_typeof(metadata.document -> 'image') = 'string'
           THEN metadata.document ->> 'image'
           ELSE NULL
       END,
       metadata.observed_block_number::text,
       metadata.observed_block_hash
FROM external_metadata AS metadata
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = metadata.chain_id
 AND canonical.number = metadata.observed_block_number
 AND canonical.block_hash = metadata.observed_block_hash
WHERE metadata.chain_id = $1::numeric
  AND metadata.resource_kind = 'nft'
  AND metadata.token_address = $2
  AND metadata.token_id = $3::numeric
ORDER BY metadata.observed_block_number DESC, metadata.observed_block_hash
LIMIT 1;

-- name: MetadataSelectCanonicalNFTMetadata :many
SELECT metadata.state,
       metadata.document,
       metadata.observed_block_number::text,
       metadata.observed_block_hash
FROM external_metadata AS metadata
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = metadata.chain_id
 AND canonical.number = metadata.observed_block_number
 AND canonical.block_hash = metadata.observed_block_hash
WHERE metadata.chain_id = $1::numeric
  AND metadata.resource_kind = 'nft'
  AND metadata.token_address = $2
  AND metadata.token_id = $3::numeric
ORDER BY metadata.observed_block_number DESC, metadata.observed_block_hash
LIMIT 1;
