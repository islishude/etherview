-- name: MetadataAnyNFTMetadata :many
SELECT EXISTS (
    SELECT 1 FROM external_metadata AS metadata
    WHERE metadata.chain_id = $1::numeric AND metadata.resource_kind = 'nft'
      AND metadata.token_address = $2 AND metadata.token_id = $3::numeric
) OR EXISTS (
    SELECT 1 FROM nft_metadata_source_observations AS source
    WHERE source.chain_id = $1::numeric
      AND source.token_address = $2 AND source.token_id = $3::numeric
) OR EXISTS (
    SELECT 1 FROM nft_metadata_update_observations AS update
    WHERE update.chain_id = $1::numeric AND update.state = 'accepted'
      AND update.token_address = $2
      AND (
          (
              update.event_kind IN ('erc4906_single', 'erc1155_uri')
              AND update.from_token_id = $3::numeric
          ) OR (
              update.event_kind = 'erc4906_batch'
              AND $3::numeric BETWEEN update.from_token_id AND update.to_token_id
              AND (
                  EXISTS (
                      SELECT 1
                      FROM token_events AS known_event
                      WHERE known_event.chain_id = update.chain_id
                        AND known_event.token_address = update.token_address
                        AND known_event.token_id = $3::numeric
                        AND known_event.standard = update.standard
                        AND known_event.block_number <= update.block_number
                  ) OR EXISTS (
                      SELECT 1
                      FROM nft_metadata_source_observations AS known_source
                      WHERE known_source.chain_id = update.chain_id
                        AND known_source.token_address = update.token_address
                        AND known_source.token_id = $3::numeric
                        AND known_source.standard = update.standard
                        AND known_source.block_number <= update.block_number
                  )
              )
          )
      )
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

-- name: MetadataExistingNFTUpdateObservation :many
SELECT block_number::text, block_hash, log_index, token_address,
       standard, event_kind, state,
       COALESCE(from_token_id::text, ''), COALESCE(to_token_id::text, ''), error_code
FROM nft_metadata_update_observations
WHERE chain_id = $1::numeric AND block_number = $2::numeric
  AND block_hash = $3 AND log_index = $4;

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
WITH known_ids AS (
    SELECT event.chain_id, event.token_address, event.token_id,
           event.standard, event.block_number, event.block_hash
    FROM token_events AS event
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = event.chain_id
     AND canonical.number = event.block_number
     AND canonical.block_hash = event.block_hash
    WHERE event.chain_id = $1::numeric
      AND event.token_id IS NOT NULL
      AND event.standard IN ('erc721', 'erc1155')
    UNION
    SELECT source.chain_id, source.token_address, source.token_id,
           source.standard, source.block_number, source.block_hash
    FROM nft_metadata_source_observations AS source
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = source.chain_id
     AND canonical.number = source.block_number
     AND canonical.block_hash = source.block_hash
    WHERE source.chain_id = $1::numeric
), candidates AS (
    SELECT event.token_address, event.token_id, event.block_number,
           event.block_hash, event.standard, 0::bigint AS signal_order,
           event.log_index, event.sub_index
    FROM token_events AS event
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = event.chain_id
     AND canonical.number = event.block_number
     AND canonical.block_hash = event.block_hash
    WHERE event.chain_id = $1::numeric
      AND event.token_id IS NOT NULL
      AND event.standard IN ('erc721', 'erc1155')
    UNION ALL
    SELECT update.token_address, update.from_token_id, update.block_number,
           update.block_hash, update.standard, 1::bigint,
           update.log_index, 0
    FROM nft_metadata_update_observations AS update
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = update.chain_id
     AND canonical.number = update.block_number
     AND canonical.block_hash = update.block_hash
    WHERE update.chain_id = $1::numeric
      AND update.state = 'accepted'
      AND update.event_kind IN ('erc4906_single', 'erc1155_uri')
    UNION ALL
    SELECT update.token_address, known.token_id, update.block_number,
           update.block_hash, update.standard, 2::bigint,
           update.log_index, 0
    FROM nft_metadata_update_observations AS update
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = update.chain_id
     AND canonical.number = update.block_number
     AND canonical.block_hash = update.block_hash
    JOIN known_ids AS known
      ON known.chain_id = update.chain_id
     AND known.token_address = update.token_address
     AND known.standard = update.standard
     AND known.block_number <= update.block_number
     AND known.token_id BETWEEN update.from_token_id AND update.to_token_id
    WHERE update.chain_id = $1::numeric
      AND update.state = 'accepted'
      AND update.event_kind = 'erc4906_batch'
), pending AS (
    SELECT DISTINCT ON (
        candidate.token_address, candidate.token_id, candidate.block_hash
    )
        candidate.token_address, candidate.token_id, candidate.block_number,
        candidate.block_hash, candidate.standard, candidate.signal_order,
        candidate.log_index, candidate.sub_index
    FROM candidates AS candidate
    WHERE NOT EXISTS (
        SELECT 1
        FROM external_metadata AS metadata
        WHERE metadata.chain_id = $1::numeric
          AND metadata.resource_kind = 'nft'
          AND metadata.token_address = candidate.token_address
          AND metadata.token_id = candidate.token_id
          AND metadata.observed_block_hash = candidate.block_hash
    )
      AND NOT EXISTS (
        SELECT 1
        FROM nft_metadata_source_observations AS source
        WHERE source.chain_id = $1::numeric
          AND source.token_address = candidate.token_address
          AND source.token_id = candidate.token_id
          AND source.block_hash = candidate.block_hash
    )
    ORDER BY candidate.token_address, candidate.token_id,
             candidate.block_hash, candidate.signal_order,
             candidate.log_index, candidate.sub_index
)
SELECT pending.token_address, pending.token_id::text,
       pending.block_number::text, pending.block_hash, pending.standard
FROM pending
ORDER BY pending.block_number, pending.signal_order, pending.log_index,
         pending.sub_index, pending.token_address, pending.token_id
LIMIT 1;

-- name: MetadataNextNFTUpdateLog :many
SELECT log.block_number::text, log.block_hash, log.log_index,
       log.tx_hash, log.address, log.raw, token.standard
FROM logs AS log
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = log.chain_id
 AND canonical.number = log.block_number
 AND canonical.block_hash = log.block_hash
JOIN LATERAL (
    SELECT observation.standard
    FROM token_contracts AS observation
    JOIN canonical_blocks AS token_canonical
      ON token_canonical.chain_id = observation.chain_id
     AND token_canonical.number = observation.observed_block_number
     AND token_canonical.block_hash = observation.observed_block_hash
    WHERE observation.chain_id = log.chain_id
      AND observation.address = log.address
      AND observation.observed_block_number <= log.block_number
    ORDER BY observation.observed_block_number DESC,
             observation.observed_block_hash DESC,
             observation.code_hash DESC
    LIMIT 1
) AS token ON token.standard IN ('erc721', 'erc1155')
WHERE log.chain_id = $1::numeric
  AND log.topic0 IN ($2, $3, $4)
  AND NOT EXISTS (
      SELECT 1
      FROM nft_metadata_update_observations AS update
      WHERE update.chain_id = log.chain_id
        AND update.block_number = log.block_number
        AND update.block_hash = log.block_hash
        AND update.log_index = log.log_index
  )
ORDER BY log.block_number, log.log_index
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
WITH signals AS (
    SELECT metadata.state,
           metadata.observed_block_number AS block_number,
           metadata.observed_block_hash AS block_hash,
           3::bigint AS source_priority,
           0::bigint AS source_order
    FROM external_metadata AS metadata
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = metadata.chain_id
     AND canonical.number = metadata.observed_block_number
     AND canonical.block_hash = metadata.observed_block_hash
    WHERE metadata.chain_id = $1::numeric
      AND metadata.resource_kind = 'nft'
      AND metadata.token_address = $2
      AND metadata.token_id = $3::numeric
    UNION ALL
    SELECT CASE source.state WHEN 'found' THEN 'pending' ELSE 'unavailable' END,
           source.block_number, source.block_hash,
           2::bigint, 0::bigint
    FROM nft_metadata_source_observations AS source
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = source.chain_id
     AND canonical.number = source.block_number
     AND canonical.block_hash = source.block_hash
    WHERE source.chain_id = $1::numeric
      AND source.token_address = $2
      AND source.token_id = $3::numeric
    UNION ALL
    SELECT 'pending', update.block_number, update.block_hash,
           1::bigint, update.log_index
    FROM nft_metadata_update_observations AS update
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = update.chain_id
     AND canonical.number = update.block_number
     AND canonical.block_hash = update.block_hash
    WHERE update.chain_id = $1::numeric
      AND update.state = 'accepted'
      AND update.token_address = $2
      AND (
          (
              update.event_kind IN ('erc4906_single', 'erc1155_uri')
              AND update.from_token_id = $3::numeric
          ) OR (
              update.event_kind = 'erc4906_batch'
              AND $3::numeric BETWEEN update.from_token_id AND update.to_token_id
              AND (
                  EXISTS (
                      SELECT 1
                      FROM token_events AS known_event
                      JOIN canonical_blocks AS known_canonical
                        ON known_canonical.chain_id = known_event.chain_id
                       AND known_canonical.number = known_event.block_number
                       AND known_canonical.block_hash = known_event.block_hash
                      WHERE known_event.chain_id = update.chain_id
                        AND known_event.token_address = update.token_address
                        AND known_event.token_id = $3::numeric
                        AND known_event.standard = update.standard
                        AND known_event.block_number <= update.block_number
                  ) OR EXISTS (
                      SELECT 1
                      FROM nft_metadata_source_observations AS known_source
                      JOIN canonical_blocks AS known_canonical
                        ON known_canonical.chain_id = known_source.chain_id
                       AND known_canonical.number = known_source.block_number
                       AND known_canonical.block_hash = known_source.block_hash
                      WHERE known_source.chain_id = update.chain_id
                        AND known_source.token_address = update.token_address
                        AND known_source.token_id = $3::numeric
                        AND known_source.standard = update.standard
                        AND known_source.block_number <= update.block_number
                  )
              )
          )
      )
), latest_signal AS (
    SELECT state, block_number, block_hash
    FROM signals
    ORDER BY block_number DESC, source_priority DESC, source_order DESC, block_hash DESC
    LIMIT 1
), content AS (
    SELECT metadata.document,
           metadata.observed_block_number AS block_number,
           metadata.observed_block_hash AS block_hash
    FROM external_metadata AS metadata
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = metadata.chain_id
     AND canonical.number = metadata.observed_block_number
     AND canonical.block_hash = metadata.observed_block_hash
    WHERE metadata.chain_id = $1::numeric
      AND metadata.resource_kind = 'nft'
      AND metadata.token_address = $2
      AND metadata.token_id = $3::numeric
      AND metadata.state = 'available'
    ORDER BY metadata.observed_block_number DESC, metadata.observed_block_hash DESC
    LIMIT 1
)
SELECT latest_signal.state,
       latest_signal.block_number::text,
       latest_signal.block_hash,
       content.document,
       content.block_number::text,
       content.block_hash
FROM latest_signal
LEFT JOIN content ON TRUE;
