-- Public proxy reads are executed on the PostgreSQL writer inside a
-- repeatable-read, read-only transaction. Every derived fact below is joined
-- to its current proxy@2 publication witness; raw or superseded generations
-- are retained for audit but never cross the public boundary.

-- name: GetProxyAPISnapshot :one
WITH canonical_tip AS (
    SELECT number, block_hash
    FROM canonical_blocks
    WHERE chain_id = sqlc.arg(chain_id)::numeric
    ORDER BY number DESC
    LIMIT 1
), history_epoch AS (
    SELECT COALESCE((
        SELECT epoch.epoch_id
        FROM canonical_tip AS tip
        JOIN proxy_history_epochs AS epoch
          ON epoch.chain_id = sqlc.arg(chain_id)::numeric
         AND epoch.block_number <= tip.number
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = epoch.chain_id
         AND canonical.number = epoch.block_number
         AND canonical.block_hash = epoch.block_hash
        ORDER BY epoch.epoch_id DESC
        LIMIT 1
    ), 0)::bigint AS epoch_id
)
SELECT tip.number::text AS snapshot_number,
       tip.block_hash AS snapshot_hash,
       COALESCE(stage.state, 'unavailable')::text AS stage_state,
       COALESCE(stage.details, '{}'::jsonb) AS stage_details,
       COALESCE(stage.durable_job_id, 0)::bigint AS durable_job_id,
       COALESCE(stage.job_generation, 0)::bigint AS job_generation,
       history_epoch.epoch_id AS history_epoch
FROM canonical_tip AS tip
CROSS JOIN history_epoch
LEFT JOIN published_block_stage_results AS stage
  ON stage.chain_id = sqlc.arg(chain_id)::numeric
 AND stage.block_number = tip.number
 AND stage.block_hash = tip.block_hash
 AND stage.stage = 'proxy'
 AND stage.stage_version = 2;

-- name: ValidateProxyAPISnapshot :one
SELECT EXISTS (
    SELECT 1
    FROM canonical_blocks
    JOIN published_block_stage_results AS published
      ON published.chain_id = canonical_blocks.chain_id
     AND published.block_number = canonical_blocks.number
     AND published.block_hash = canonical_blocks.block_hash
     AND published.stage = 'proxy'
     AND published.stage_version = 2
     AND published.durable_job_id = sqlc.arg(durable_job_id)::bigint
     AND published.job_generation = sqlc.arg(job_generation)::bigint
    WHERE canonical_blocks.chain_id = sqlc.arg(chain_id)::numeric
      AND canonical_blocks.number = sqlc.arg(snapshot_number)::numeric
      AND canonical_blocks.block_hash = sqlc.arg(snapshot_hash)::bytea
      AND (
          SELECT COALESCE((
              SELECT epoch.epoch_id
              FROM proxy_history_epochs AS epoch
              JOIN canonical_blocks AS epoch_canonical
                ON epoch_canonical.chain_id = epoch.chain_id
               AND epoch_canonical.number = epoch.block_number
               AND epoch_canonical.block_hash = epoch.block_hash
              WHERE epoch.chain_id = sqlc.arg(chain_id)::numeric
                AND epoch.block_number <= sqlc.arg(snapshot_number)::numeric
              ORDER BY epoch.epoch_id DESC
              LIMIT 1
          ), 0)::bigint
      ) = sqlc.arg(history_epoch)::bigint
) AS canonical;

-- name: GetLatestPublishedProxyDetection :one
WITH canonical_tip AS (
    SELECT number, block_hash
    FROM canonical_blocks
    WHERE chain_id = sqlc.arg(chain_id)::numeric
    ORDER BY number DESC
    LIMIT 1
), latest_raw AS (
    SELECT observation.*, witness.id AS observation_generation_id,
           witness.durable_job_id, witness.job_generation,
           tip.number AS context_number
    FROM canonical_tip AS tip
    JOIN LATERAL (
        SELECT candidate.*
        FROM proxy_observations AS candidate
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = candidate.chain_id
         AND canonical.number = candidate.block_number
         AND canonical.block_hash = candidate.block_hash
        WHERE candidate.chain_id = sqlc.arg(chain_id)::numeric
          AND candidate.proxy_address = sqlc.arg(proxy_address)::bytea
          AND candidate.stage_version = 2
          AND candidate.canonical = TRUE
          AND candidate.block_number <= tip.number
          AND EXISTS (
              SELECT 1
              FROM proxy_observation_generations AS generation
              JOIN published_block_stage_results AS published
                ON published.chain_id = generation.chain_id
               AND published.block_hash = generation.observation_block_hash
               AND published.stage = 'proxy'
               AND published.stage_version = generation.observation_stage_version
               AND published.durable_job_id = generation.durable_job_id
               AND published.job_generation = generation.job_generation
               AND published.state = 'complete'
              WHERE generation.chain_id = candidate.chain_id
                AND generation.proxy_address = candidate.proxy_address
                AND generation.observation_block_hash = candidate.block_hash
                AND generation.observation_stage_version = candidate.stage_version
          )
        ORDER BY candidate.block_number DESC, candidate.block_hash DESC
        LIMIT 1
    ) AS observation ON TRUE
    JOIN LATERAL (
        SELECT generation.id, generation.durable_job_id,
               generation.job_generation
        FROM proxy_observation_generations AS generation
        JOIN published_block_stage_results AS published
          ON published.chain_id = generation.chain_id
         AND published.block_hash = generation.observation_block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = generation.observation_stage_version
         AND published.durable_job_id = generation.durable_job_id
         AND published.job_generation = generation.job_generation
         AND published.state = 'complete'
        WHERE generation.chain_id = observation.chain_id
          AND generation.proxy_address = observation.proxy_address
          AND generation.observation_block_hash = observation.block_hash
          AND generation.observation_stage_version = observation.stage_version
        ORDER BY generation.id DESC
        LIMIT 1
    ) AS witness ON TRUE
), unshadowed AS (
    SELECT raw.*
    FROM latest_raw AS raw
    WHERE EXISTS (
        SELECT 1
        FROM contract_code_observations AS code
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = code.chain_id
         AND canonical.number = code.block_number
         AND canonical.block_hash = code.block_hash
        WHERE code.chain_id = raw.chain_id
          AND code.address = raw.proxy_address
          AND code.code_hash = raw.proxy_code_hash
          AND code.canonical = TRUE
          AND code.block_number <= raw.context_number
          AND NOT EXISTS (
              SELECT 1
              FROM contract_code_observations AS newer
              JOIN canonical_blocks AS newer_canonical
                ON newer_canonical.chain_id = newer.chain_id
               AND newer_canonical.number = newer.block_number
               AND newer_canonical.block_hash = newer.block_hash
              WHERE newer.chain_id = code.chain_id
                AND newer.address = code.address
                AND newer.canonical = TRUE
                AND newer.block_number <= raw.context_number
                AND newer.block_number > code.block_number
          )
    )
      AND NOT EXISTS (
        SELECT 1
        FROM proxy_detection_evidence AS evidence
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = evidence.chain_id
         AND canonical.number = evidence.block_number
         AND canonical.block_hash = evidence.block_hash
        JOIN published_block_stage_results AS published
          ON published.chain_id = evidence.chain_id
         AND published.block_hash = evidence.block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = evidence.stage_version
         AND published.durable_job_id = evidence.durable_job_id
         AND published.job_generation = evidence.job_generation
         AND published.state = 'complete'
        WHERE evidence.chain_id = raw.chain_id
          AND evidence.address = raw.proxy_address
          AND evidence.candidate_kind = 'proxy'
          AND evidence.stage_version = 2
          AND evidence.canonical = TRUE
          AND evidence.block_number <= raw.context_number
          AND NOT (
              evidence.reason = 'immutable_args_creation_unverified'
              AND evidence.code_hash = raw.proxy_code_hash
              AND raw.proxy_pattern = 'clone'
              AND raw.evidence_state = 'exact'
              AND octet_length(raw.immutable_args) > 0
              AND raw.details->>'immutable_args_creation_authenticated' = 'true'
          )
          AND (
              evidence.block_number > raw.block_number OR (
                  evidence.block_number = raw.block_number
                  AND evidence.block_hash = raw.block_hash
                  AND evidence.durable_job_id = raw.durable_job_id
                  AND evidence.job_generation >= raw.job_generation
              )
          )
    )
), effective AS (
    SELECT raw.*,
           CASE WHEN raw.proxy_kind = 'beacon'
                THEN beacon.implementation_address
                ELSE raw.implementation_address END AS current_implementation_address,
           CASE WHEN raw.proxy_kind = 'beacon'
                THEN beacon.implementation_code_hash
                ELSE raw.implementation_code_hash END AS current_implementation_code_hash,
           CASE WHEN raw.proxy_kind = 'beacon' AND
                          beacon.implementation_address IS NULL
                THEN 'partial'::text
                ELSE raw.evidence_state END AS current_evidence_state,
           CASE WHEN raw.proxy_kind = 'beacon' THEN beacon.block_number
                ELSE raw.block_number END AS implementation_observation_block_number,
           CASE WHEN raw.proxy_kind = 'beacon' THEN beacon.block_hash
                ELSE raw.block_hash END AS implementation_observation_block_hash
    FROM unshadowed AS raw
    LEFT JOIN LATERAL (
        SELECT observation.implementation_address,
               observation.implementation_code_hash,
               observation.block_number, observation.block_hash
        FROM beacon_implementation_observations AS observation
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = observation.chain_id
         AND canonical.number = observation.block_number
         AND canonical.block_hash = observation.block_hash
        JOIN LATERAL (
            SELECT generation.durable_job_id, generation.job_generation
            FROM beacon_observation_generations AS generation
            JOIN published_block_stage_results AS published
              ON published.chain_id = generation.chain_id
             AND published.block_hash = generation.observation_block_hash
             AND published.stage = 'proxy'
             AND published.stage_version = generation.observation_stage_version
             AND published.durable_job_id = generation.durable_job_id
             AND published.job_generation = generation.job_generation
             AND published.state = 'complete'
            WHERE generation.chain_id = observation.chain_id
              AND generation.beacon_address = observation.beacon_address
              AND generation.observation_block_hash = observation.block_hash
              AND generation.observation_stage_version = observation.stage_version
            ORDER BY generation.id DESC
            LIMIT 1
        ) AS generation ON TRUE
        WHERE raw.proxy_kind = 'beacon'
          AND observation.chain_id = raw.chain_id
          AND observation.beacon_address = raw.beacon_address
          AND observation.stage_version = 2
          AND observation.canonical = TRUE
          AND observation.block_number <= raw.context_number
          AND NOT EXISTS (
              SELECT 1
              FROM proxy_detection_evidence AS evidence
              JOIN canonical_blocks AS evidence_canonical
                ON evidence_canonical.chain_id = evidence.chain_id
               AND evidence_canonical.number = evidence.block_number
               AND evidence_canonical.block_hash = evidence.block_hash
              JOIN published_block_stage_results AS evidence_publication
                ON evidence_publication.chain_id = evidence.chain_id
               AND evidence_publication.block_hash = evidence.block_hash
               AND evidence_publication.stage = 'proxy'
               AND evidence_publication.stage_version = evidence.stage_version
               AND evidence_publication.durable_job_id = evidence.durable_job_id
               AND evidence_publication.job_generation = evidence.job_generation
               AND evidence_publication.state = 'complete'
              WHERE evidence.chain_id = observation.chain_id
                AND evidence.address = observation.beacon_address
                AND evidence.code_hash = observation.beacon_code_hash
                AND evidence.candidate_kind = 'beacon'
                AND evidence.stage_version = observation.stage_version
                AND evidence.canonical = TRUE
                AND evidence.block_number <= raw.context_number
                AND (
                    evidence.block_number > observation.block_number OR (
                        evidence.block_number = observation.block_number
                        AND evidence.block_hash = observation.block_hash
                        AND evidence.durable_job_id = generation.durable_job_id
                        AND evidence.job_generation >= generation.job_generation
                    )
                )
          )
        ORDER BY observation.block_number DESC, observation.block_hash DESC
        LIMIT 1
    ) AS beacon ON TRUE
)
SELECT effective.block_number::text AS observation_block_number,
       effective.block_hash AS observation_block_hash,
       effective.proxy_code_hash, effective.proxy_kind,
       effective.proxy_pattern, effective.standard_version,
       effective.current_implementation_address::bytea AS implementation_address,
       effective.current_implementation_code_hash::bytea AS implementation_code_hash,
       effective.admin_address, effective.admin_code_hash,
       effective.beacon_address, effective.beacon_code_hash,
       effective.immutable_args, effective.confidence,
       effective.current_evidence_state::text AS evidence_state, effective.details,
       COALESCE(
           effective.implementation_observation_block_number::text, ''::text
       )::text AS implementation_observation_block_number,
       COALESCE(
           effective.implementation_observation_block_hash, ''::bytea
       )::bytea AS implementation_observation_block_hash,
       EXISTS (
           SELECT 1 FROM verified_contracts AS verified
           WHERE verified.chain_id = effective.chain_id
             AND verified.address = effective.proxy_address
             AND verified.code_hash = effective.proxy_code_hash
             AND verified.valid_from_block <= effective.context_number
             AND (verified.valid_to_block IS NULL OR
                  verified.valid_to_block >= effective.context_number)
       ) AS proxy_verified,
       EXISTS (
           SELECT 1 FROM verified_contracts AS verified
           WHERE verified.chain_id = effective.chain_id
             AND verified.address = effective.current_implementation_address
             AND verified.code_hash = effective.current_implementation_code_hash
             AND verified.valid_from_block <= effective.context_number
             AND (verified.valid_to_block IS NULL OR
                  verified.valid_to_block >= effective.context_number)
       ) AS implementation_verified,
       EXISTS (
           SELECT 1 FROM verified_contracts AS verified
           WHERE verified.chain_id = effective.chain_id
             AND verified.address = effective.admin_address
             AND verified.code_hash = effective.admin_code_hash
             AND verified.valid_from_block <= effective.context_number
             AND (verified.valid_to_block IS NULL OR
                  verified.valid_to_block >= effective.context_number)
       ) AS admin_verified,
       EXISTS (
           SELECT 1 FROM verified_contracts AS verified
           WHERE verified.chain_id = effective.chain_id
             AND verified.address = effective.beacon_address
             AND verified.code_hash = effective.beacon_code_hash
             AND verified.valid_from_block <= effective.context_number
             AND (verified.valid_to_block IS NULL OR
                  verified.valid_to_block >= effective.context_number)
       ) AS beacon_verified
FROM effective;

-- name: GetLatestPublishedProxyNegativeEvidence :one
SELECT evidence.block_number::text AS block_number,
       evidence.block_hash, evidence.code_hash,
       evidence.detection_state, evidence.reason, evidence.details
FROM proxy_detection_evidence AS evidence
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = evidence.chain_id
 AND canonical.number = evidence.block_number
 AND canonical.block_hash = evidence.block_hash
JOIN published_block_stage_results AS published
  ON published.chain_id = evidence.chain_id
 AND published.block_hash = evidence.block_hash
 AND published.stage = 'proxy'
 AND published.stage_version = evidence.stage_version
 AND published.durable_job_id = evidence.durable_job_id
 AND published.job_generation = evidence.job_generation
 AND published.state = 'complete'
WHERE evidence.chain_id = sqlc.arg(chain_id)::numeric
  AND evidence.address = sqlc.arg(proxy_address)::bytea
  AND evidence.candidate_kind = 'proxy'
  AND evidence.stage_version = 2
  AND evidence.canonical = TRUE
ORDER BY evidence.block_number DESC, evidence.block_hash DESC,
         evidence.id DESC
LIMIT 1;

-- name: GetCurrentVerifiedProxyBinding :one
WITH canonical_tip AS (
    SELECT number, block_hash
    FROM canonical_blocks
    WHERE chain_id = sqlc.arg(chain_id)::numeric
    ORDER BY number DESC
    LIMIT 1
), candidate AS (
    SELECT binding.*, tip.number AS snapshot_number,
           tip.block_hash AS snapshot_hash,
           resolution.proxy_artifact_job_id,
           resolution.implementation_artifact_job_id
    FROM canonical_tip AS tip
    JOIN verified_proxy_bindings AS binding
      ON binding.chain_id = sqlc.arg(chain_id)::numeric
     AND binding.proxy_address = sqlc.arg(proxy_address)::bytea
     AND binding.observation_stage_version = 2
    LEFT JOIN proxy_artifact_resolutions AS resolution
      ON resolution.id = binding.artifact_resolution_id
    JOIN canonical_blocks AS binding_context
      ON binding_context.chain_id = binding.chain_id
     AND binding_context.number = binding.context_block_number
     AND binding_context.block_hash = binding.context_block_hash
    ORDER BY binding.created_at DESC, binding.verification_job_id DESC
), current_binding AS (
    SELECT binding.*
    FROM candidate AS binding
    WHERE proxy_interaction_coverage_contains(
              binding.chain_id,
              binding.observation_block_number,
              binding.observation_block_hash,
              binding.snapshot_number,
              binding.snapshot_hash
          )
      AND EXISTS (
          SELECT 1
          FROM proxy_observation_generations AS generation
          JOIN published_block_stage_results AS published
            ON published.chain_id = generation.chain_id
           AND published.block_hash = generation.observation_block_hash
           AND published.stage = 'proxy'
           AND published.stage_version = generation.observation_stage_version
           AND published.durable_job_id = generation.durable_job_id
           AND published.job_generation = generation.job_generation
           AND published.state = 'complete'
          WHERE generation.id = binding.observation_generation_id
            AND generation.chain_id = binding.chain_id
            AND generation.proxy_address = binding.proxy_address
            AND generation.observation_block_hash = binding.observation_block_hash
      )
      AND NOT EXISTS (
          SELECT 1
          FROM proxy_observations AS observation
          JOIN canonical_blocks AS canonical
            ON canonical.chain_id = observation.chain_id
           AND canonical.number = observation.block_number
           AND canonical.block_hash = observation.block_hash
          WHERE observation.chain_id = binding.chain_id
            AND observation.proxy_address = binding.proxy_address
            AND observation.stage_version = 2
            AND observation.canonical = TRUE
            AND observation.block_number <= binding.snapshot_number
            AND EXISTS (
                SELECT 1
                FROM proxy_observation_generations AS generation
                JOIN published_block_stage_results AS published
                  ON published.chain_id = generation.chain_id
                 AND published.block_hash = generation.observation_block_hash
                 AND published.stage = 'proxy'
                 AND published.stage_version = generation.observation_stage_version
                 AND published.durable_job_id = generation.durable_job_id
                 AND published.job_generation = generation.job_generation
                 AND published.state = 'complete'
                WHERE generation.chain_id = observation.chain_id
                  AND generation.proxy_address = observation.proxy_address
                  AND generation.observation_block_hash = observation.block_hash
                  AND generation.observation_stage_version = observation.stage_version
            )
            AND (
                observation.block_number > binding.observation_block_number OR
                (observation.block_number = binding.observation_block_number AND
                 observation.block_hash <> binding.observation_block_hash)
            )
      )
      AND NOT EXISTS (
          SELECT 1
          FROM proxy_detection_evidence AS evidence
          JOIN canonical_blocks AS canonical
            ON canonical.chain_id = evidence.chain_id
           AND canonical.number = evidence.block_number
           AND canonical.block_hash = evidence.block_hash
          JOIN published_block_stage_results AS published
            ON published.chain_id = evidence.chain_id
           AND published.block_hash = evidence.block_hash
           AND published.stage = 'proxy'
           AND published.stage_version = evidence.stage_version
           AND published.durable_job_id = evidence.durable_job_id
           AND published.job_generation = evidence.job_generation
           AND published.state = 'complete'
          WHERE evidence.chain_id = binding.chain_id
            AND evidence.address = binding.proxy_address
            AND evidence.candidate_kind = 'proxy'
            AND evidence.stage_version = 2
            AND evidence.canonical = TRUE
            AND evidence.block_number <= binding.snapshot_number
            AND NOT (
                evidence.reason = 'immutable_args_creation_unverified'
                AND evidence.code_hash = binding.proxy_code_hash
                AND binding.proxy_pattern = 'clone'
                AND EXISTS (
                    SELECT 1
                    FROM proxy_observation_generations AS exact_generation
                    JOIN proxy_observations AS exact_clone
                      ON exact_clone.chain_id = exact_generation.chain_id
                     AND exact_clone.proxy_address = exact_generation.proxy_address
                     AND exact_clone.block_hash = exact_generation.observation_block_hash
                     AND exact_clone.stage_version = exact_generation.observation_stage_version
                    JOIN published_block_stage_results AS exact_published
                      ON exact_published.chain_id = exact_generation.chain_id
                     AND exact_published.block_hash = exact_generation.observation_block_hash
                     AND exact_published.stage = 'proxy'
                     AND exact_published.stage_version = exact_generation.observation_stage_version
                     AND exact_published.durable_job_id = exact_generation.durable_job_id
                     AND exact_published.job_generation = exact_generation.job_generation
                     AND exact_published.state = 'complete'
                    WHERE exact_generation.id = binding.observation_generation_id
                      AND exact_clone.proxy_code_hash = binding.proxy_code_hash
                      AND exact_clone.proxy_pattern = 'clone'
                      AND exact_clone.evidence_state = 'exact'
                      AND octet_length(exact_clone.immutable_args) > 0
                      AND exact_clone.details->>'immutable_args_creation_authenticated' = 'true'
                )
            )
            AND (
                evidence.block_number > binding.observation_block_number OR (
                    evidence.block_number = binding.observation_block_number
                    AND evidence.block_hash = binding.observation_block_hash
                    AND EXISTS (
                        SELECT 1
                        FROM proxy_observation_generations AS bound_generation
                        WHERE bound_generation.id = binding.observation_generation_id
                          AND evidence.durable_job_id = bound_generation.durable_job_id
                          AND evidence.job_generation >= bound_generation.job_generation
                    )
                )
            )
      )
      AND (
          binding.proxy_pattern <> 'beacon' OR EXISTS (
              SELECT 1
              FROM beacon_observation_generations AS generation
              JOIN beacon_implementation_observations AS observation
                ON observation.chain_id = generation.chain_id
               AND observation.beacon_address = generation.beacon_address
               AND observation.block_hash = generation.observation_block_hash
               AND observation.stage_version = generation.observation_stage_version
              JOIN canonical_blocks AS canonical
                ON canonical.chain_id = observation.chain_id
               AND canonical.number = observation.block_number
               AND canonical.block_hash = observation.block_hash
              JOIN published_block_stage_results AS published
                ON published.chain_id = generation.chain_id
               AND published.block_hash = generation.observation_block_hash
               AND published.stage = 'proxy'
               AND published.stage_version = generation.observation_stage_version
               AND published.durable_job_id = generation.durable_job_id
               AND published.job_generation = generation.job_generation
               AND published.state = 'complete'
              WHERE generation.id = binding.beacon_generation_id
                AND observation.canonical = TRUE
                AND observation.beacon_address = binding.beacon_address
                AND observation.beacon_code_hash = binding.beacon_code_hash
                AND observation.implementation_address = binding.implementation_address
                AND observation.implementation_code_hash = binding.implementation_code_hash
                AND NOT EXISTS (
                    SELECT 1
                    FROM beacon_implementation_observations AS newer
                    JOIN canonical_blocks AS newer_canonical
                      ON newer_canonical.chain_id = newer.chain_id
                     AND newer_canonical.number = newer.block_number
                     AND newer_canonical.block_hash = newer.block_hash
                    WHERE newer.chain_id = observation.chain_id
                      AND newer.beacon_address = observation.beacon_address
                      AND newer.stage_version = 2
                      AND newer.canonical = TRUE
                      AND newer.block_number > observation.block_number
                      AND newer.block_number <= binding.snapshot_number
                      AND (
                          newer.beacon_code_hash IS DISTINCT FROM
                              observation.beacon_code_hash OR
                          newer.implementation_address IS DISTINCT FROM
                              observation.implementation_address OR
                          newer.implementation_code_hash IS DISTINCT FROM
                              observation.implementation_code_hash
                      )
                      AND EXISTS (
                          SELECT 1
                          FROM beacon_observation_generations AS newer_generation
                          JOIN published_block_stage_results AS newer_published
                            ON newer_published.chain_id = newer_generation.chain_id
                           AND newer_published.block_hash = newer_generation.observation_block_hash
                           AND newer_published.stage = 'proxy'
                           AND newer_published.stage_version = newer_generation.observation_stage_version
                           AND newer_published.durable_job_id = newer_generation.durable_job_id
                           AND newer_published.job_generation = newer_generation.job_generation
                           AND newer_published.state = 'complete'
                          WHERE newer_generation.chain_id = newer.chain_id
                            AND newer_generation.beacon_address = newer.beacon_address
                            AND newer_generation.observation_block_hash = newer.block_hash
                            AND newer_generation.observation_stage_version = newer.stage_version
                      )
                )
                AND NOT EXISTS (
                    SELECT 1
                    FROM proxy_detection_evidence AS evidence
                    JOIN canonical_blocks AS evidence_canonical
                      ON evidence_canonical.chain_id = evidence.chain_id
                     AND evidence_canonical.number = evidence.block_number
                     AND evidence_canonical.block_hash = evidence.block_hash
                    JOIN published_block_stage_results AS evidence_publication
                      ON evidence_publication.chain_id = evidence.chain_id
                     AND evidence_publication.block_hash = evidence.block_hash
                     AND evidence_publication.stage = 'proxy'
                     AND evidence_publication.stage_version = evidence.stage_version
                     AND evidence_publication.durable_job_id = evidence.durable_job_id
                     AND evidence_publication.job_generation = evidence.job_generation
                     AND evidence_publication.state = 'complete'
                    WHERE evidence.chain_id = observation.chain_id
                      AND evidence.address = observation.beacon_address
                      AND evidence.code_hash = observation.beacon_code_hash
                      AND evidence.candidate_kind = 'beacon'
                      AND evidence.stage_version = observation.stage_version
                      AND evidence.canonical = TRUE
                      AND evidence.block_number <= binding.snapshot_number
                      AND (
                          evidence.block_number > observation.block_number OR (
                              evidence.block_number = observation.block_number
                              AND evidence.block_hash = observation.block_hash
                              AND evidence.durable_job_id = generation.durable_job_id
                              AND evidence.job_generation >= generation.job_generation
                          )
                      )
                )
          )
      )
      AND (
          binding.proxy_pattern <> 'uups' OR EXISTS (
              SELECT 1
              FROM uups_implementation_observation_generations AS generation
              JOIN uups_implementation_observations AS observation
                ON observation.chain_id = generation.chain_id
               AND observation.implementation_address = generation.implementation_address
               AND observation.block_hash = generation.observation_block_hash
               AND observation.stage_version = generation.observation_stage_version
               AND observation.verification_job_id = generation.verification_job_id
              JOIN canonical_blocks AS canonical
                ON canonical.chain_id = observation.chain_id
               AND canonical.number = observation.block_number
               AND canonical.block_hash = observation.block_hash
              JOIN published_block_stage_results AS published
                ON published.chain_id = generation.chain_id
               AND published.block_hash = generation.observation_block_hash
               AND published.stage = 'proxy'
               AND published.stage_version = generation.observation_stage_version
               AND published.durable_job_id = generation.durable_job_id
               AND published.job_generation = generation.job_generation
               AND published.state = 'complete'
              WHERE generation.id = binding.uups_generation_id
                AND observation.canonical = TRUE
                AND observation.probe_state = 'compatible'
                AND observation.implementation_address = binding.implementation_address
                AND observation.implementation_code_hash = binding.implementation_code_hash
                AND NOT EXISTS (
                    SELECT 1
                    FROM uups_implementation_observations AS conflict
                    JOIN canonical_blocks AS conflict_canonical
                      ON conflict_canonical.chain_id = conflict.chain_id
                     AND conflict_canonical.number = conflict.block_number
                     AND conflict_canonical.block_hash = conflict.block_hash
                    JOIN uups_implementation_observation_generations AS conflict_generation
                      ON conflict_generation.chain_id = conflict.chain_id
                     AND conflict_generation.implementation_address = conflict.implementation_address
                     AND conflict_generation.observation_block_hash = conflict.block_hash
                     AND conflict_generation.observation_stage_version = conflict.stage_version
                     AND conflict_generation.verification_job_id = conflict.verification_job_id
                    JOIN published_block_stage_results AS conflict_published
                      ON conflict_published.chain_id = conflict_generation.chain_id
                     AND conflict_published.block_hash = conflict_generation.observation_block_hash
                     AND conflict_published.stage = 'proxy'
                     AND conflict_published.stage_version = conflict_generation.observation_stage_version
                     AND conflict_published.durable_job_id = conflict_generation.durable_job_id
                     AND conflict_published.job_generation = conflict_generation.job_generation
                     AND conflict_published.state = 'complete'
                    WHERE conflict.chain_id = observation.chain_id
                      AND conflict.implementation_address = observation.implementation_address
                      AND conflict.implementation_code_hash = observation.implementation_code_hash
                      AND conflict.stage_version = 2
                      AND conflict.canonical = TRUE
                      AND conflict.block_number <= binding.snapshot_number
                      AND (
                          conflict.block_number > observation.block_number OR (
                              conflict.block_number = observation.block_number
                              AND (
                                  (
                                      conflict.probe_state || ':' ||
                                      COALESCE(conflict.rejection_reason, '')
                                  ) IS DISTINCT FROM (
                                      observation.probe_state || ':' ||
                                      COALESCE(observation.rejection_reason, '')
                                  ) OR conflict_generation.id > generation.id
                              )
                          )
                      )
                )
          )
      )
      AND NOT EXISTS (
          SELECT 1
          FROM (VALUES
              (binding.proxy_address, binding.proxy_code_hash),
              (binding.implementation_address, binding.implementation_code_hash),
              (binding.admin_address, binding.admin_code_hash),
              (binding.beacon_address, binding.beacon_code_hash),
              (binding.management_address, binding.management_code_hash)
          ) AS identity(address, code_hash)
          WHERE identity.address IS NOT NULL AND NOT EXISTS (
              SELECT 1
              FROM contract_code_observations AS observation
              JOIN canonical_blocks AS canonical
                ON canonical.chain_id = observation.chain_id
               AND canonical.number = observation.block_number
               AND canonical.block_hash = observation.block_hash
              WHERE observation.chain_id = binding.chain_id
                AND observation.address = identity.address
                AND observation.code_hash = identity.code_hash
                AND observation.canonical = TRUE
                AND observation.block_number <= binding.snapshot_number
                AND NOT EXISTS (
                    SELECT 1
                    FROM contract_code_observations AS newer
                    JOIN canonical_blocks AS newer_canonical
                      ON newer_canonical.chain_id = newer.chain_id
                     AND newer_canonical.number = newer.block_number
                     AND newer_canonical.block_hash = newer.block_hash
                    WHERE newer.chain_id = observation.chain_id
                      AND newer.address = observation.address
                      AND newer.canonical = TRUE
                      AND newer.block_number <= binding.snapshot_number
                      AND newer.block_number > observation.block_number
                )
          )
      )
      AND NOT EXISTS (
          SELECT 1
          FROM (VALUES
              (binding.proxy_address), (binding.implementation_address),
              (binding.admin_address), (binding.beacon_address),
              (binding.management_address)
          ) AS identity(address)
          JOIN transaction_state_changes AS change
            ON change.chain_id = binding.chain_id
           AND change.address = identity.address
           AND change.field_kind = 'code'
           AND change.canonical = TRUE
          JOIN canonical_blocks AS canonical
            ON canonical.chain_id = change.chain_id
           AND canonical.number = change.block_number
           AND canonical.block_hash = change.block_hash
          WHERE identity.address IS NOT NULL
            AND change.block_number > binding.context_block_number
            AND change.block_number <= binding.snapshot_number
            AND lower(change.before_value) IS DISTINCT FROM lower(change.after_value)
      )
      AND NOT EXISTS (
          SELECT 1
          FROM (VALUES
              (CASE WHEN binding.proxy_pattern = 'clone' THEN NULL::bytea
                    ELSE binding.proxy_address END,
               CASE WHEN binding.proxy_pattern = 'clone' THEN NULL::bytea
                    ELSE binding.proxy_code_hash END),
              (binding.implementation_address, binding.implementation_code_hash),
              (binding.management_address, binding.management_code_hash)
          ) AS identity(address, code_hash)
          WHERE identity.address IS NOT NULL AND NOT EXISTS (
              SELECT 1
              FROM verified_contracts AS verified
              WHERE verified.chain_id = binding.chain_id
                AND verified.address = identity.address
                AND verified.code_hash = identity.code_hash
                AND verified.valid_from_block <= binding.snapshot_number
                AND (verified.valid_to_block IS NULL OR
                     verified.valid_to_block >= binding.snapshot_number)
          )
      )
    ORDER BY binding.created_at DESC, binding.verification_job_id DESC
    LIMIT 1
)
SELECT verification_job_id::text AS binding_id,
       proxy_code_hash, (proxy_pattern <> 'clone')::boolean AS proxy_verified,
       proxy_kind, proxy_pattern, standard_version,
       implementation_address, implementation_code_hash,
       admin_address, admin_code_hash, beacon_address, beacon_code_hash,
       management_kind, management_address, management_code_hash,
       observation_block_number::text AS observation_block_number,
       observation_block_hash, snapshot_number::text AS snapshot_number,
       snapshot_hash, proxy_artifact_job_id,
       implementation_artifact_job_id,
       CASE WHEN proxy_pattern = 'beacon' THEN (
           SELECT observation.block_number::text
           FROM beacon_observation_generations AS generation
           JOIN beacon_implementation_observations AS observation
             ON observation.chain_id = generation.chain_id
            AND observation.beacon_address = generation.beacon_address
            AND observation.block_hash = generation.observation_block_hash
            AND observation.stage_version = generation.observation_stage_version
           WHERE generation.id = current_binding.beacon_generation_id
       ) ELSE observation_block_number::text END AS implementation_observation_block_number,
       CASE WHEN proxy_pattern = 'beacon' THEN (
           SELECT observation.block_hash
           FROM beacon_observation_generations AS generation
           JOIN beacon_implementation_observations AS observation
             ON observation.chain_id = generation.chain_id
            AND observation.beacon_address = generation.beacon_address
            AND observation.block_hash = generation.observation_block_hash
            AND observation.stage_version = generation.observation_stage_version
           WHERE generation.id = current_binding.beacon_generation_id
       ) ELSE observation_block_hash END::bytea AS implementation_observation_block_hash
FROM current_binding;

-- name: CountCurrentBeaconProxies :one
WITH canonical_tip AS (
    SELECT number
    FROM canonical_blocks
    WHERE chain_id = sqlc.arg(chain_id)::numeric
    ORDER BY number DESC
    LIMIT 1
), current_proxy AS (
    SELECT DISTINCT ON (observation.proxy_address)
           observation.proxy_address, observation.proxy_code_hash,
           COALESCE(resolution.proxy_kind, observation.proxy_kind)::text AS proxy_kind,
           COALESCE(resolution.beacon_address, observation.beacon_address)::bytea AS beacon_address,
           observation.block_number, observation.block_hash,
           generation.durable_job_id, generation.job_generation,
           tip.number AS snapshot_number
    FROM canonical_tip AS tip
    JOIN proxy_observations AS observation
      ON observation.chain_id = sqlc.arg(chain_id)::numeric
     AND observation.block_number <= tip.number
     AND observation.stage_version = 2
     AND observation.canonical = TRUE
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = observation.chain_id
     AND canonical.number = observation.block_number
     AND canonical.block_hash = observation.block_hash
    JOIN LATERAL (
        SELECT generation.durable_job_id, generation.job_generation
        FROM proxy_observation_generations AS generation
        JOIN published_block_stage_results AS published
          ON published.chain_id = generation.chain_id
         AND published.block_hash = generation.observation_block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = generation.observation_stage_version
         AND published.durable_job_id = generation.durable_job_id
         AND published.job_generation = generation.job_generation
         AND published.state = 'complete'
        WHERE generation.chain_id = observation.chain_id
          AND generation.proxy_address = observation.proxy_address
          AND generation.observation_block_hash = observation.block_hash
          AND generation.observation_stage_version = observation.stage_version
        ORDER BY generation.id DESC
        LIMIT 1
    ) AS generation ON TRUE
    LEFT JOIN LATERAL (
        SELECT exact.proxy_kind, exact.beacon_address
        FROM proxy_artifact_resolutions AS exact
        WHERE exact.chain_id = observation.chain_id
          AND exact.proxy_address = observation.proxy_address
          AND exact.observation_block_hash = observation.block_hash
          AND exact.observation_stage_version = observation.stage_version
          AND exact.durable_job_id = generation.durable_job_id
          AND exact.job_generation = generation.job_generation
          AND exact.proxy_pattern = 'beacon'
          AND exact.standard_version = '5.6.1'
        ORDER BY exact.id DESC
        LIMIT 1
    ) AS resolution ON TRUE
    ORDER BY observation.proxy_address, observation.block_number DESC,
             observation.block_hash DESC
), effective_current AS (
    SELECT current.*
    FROM current_proxy AS current
    JOIN LATERAL (
        SELECT code.code_hash
        FROM contract_code_observations AS code
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = code.chain_id
         AND canonical.number = code.block_number
         AND canonical.block_hash = code.block_hash
        WHERE code.chain_id = sqlc.arg(chain_id)::numeric
          AND code.address = current.proxy_address
          AND code.canonical = TRUE
          AND code.block_number <= current.snapshot_number
        ORDER BY code.block_number DESC, code.observed_at DESC
        LIMIT 1
    ) AS current_code ON current_code.code_hash = current.proxy_code_hash
    WHERE current.proxy_kind = 'beacon'
      AND NOT EXISTS (
          SELECT 1
          FROM proxy_detection_evidence AS evidence
          JOIN canonical_blocks AS canonical
            ON canonical.chain_id = evidence.chain_id
           AND canonical.number = evidence.block_number
           AND canonical.block_hash = evidence.block_hash
          JOIN published_block_stage_results AS published
            ON published.chain_id = evidence.chain_id
           AND published.block_hash = evidence.block_hash
           AND published.stage = 'proxy'
           AND published.stage_version = evidence.stage_version
           AND published.durable_job_id = evidence.durable_job_id
           AND published.job_generation = evidence.job_generation
           AND published.state = 'complete'
          WHERE evidence.chain_id = sqlc.arg(chain_id)::numeric
            AND evidence.address = current.proxy_address
            AND evidence.candidate_kind = 'proxy'
            AND evidence.stage_version = 2
            AND evidence.canonical = TRUE
            AND evidence.block_number <= current.snapshot_number
            AND (
                evidence.block_number > current.block_number OR (
                    evidence.block_number = current.block_number
                    AND evidence.block_hash = current.block_hash
                    AND evidence.durable_job_id = current.durable_job_id
                    AND evidence.job_generation >= current.job_generation
                )
            )
      )
)
SELECT count(*)::text AS proxy_count
FROM effective_current
WHERE beacon_address = sqlc.arg(beacon_address)::bytea;

-- name: GetProxyHistoryCoverage :one
WITH canonical_tip AS (
    SELECT number, block_hash
    FROM canonical_blocks
    WHERE chain_id = sqlc.arg(chain_id)::numeric
      AND number = sqlc.arg(snapshot_number)::numeric
      AND block_hash = sqlc.arg(snapshot_hash)::bytea
), first_observation AS (
    SELECT observation.block_number, observation.block_hash,
           observation.proxy_pattern
    FROM proxy_observations AS observation
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = observation.chain_id
     AND canonical.number = observation.block_number
     AND canonical.block_hash = observation.block_hash
    WHERE observation.chain_id = sqlc.arg(chain_id)::numeric
      AND observation.proxy_address = sqlc.arg(proxy_address)::bytea
      AND observation.stage_version = 2
      AND observation.canonical = TRUE
      AND observation.block_number <= sqlc.arg(snapshot_number)::numeric
      AND EXISTS (
          SELECT 1
          FROM proxy_observation_generations AS generation
          JOIN published_block_stage_results AS published
            ON published.chain_id = generation.chain_id
           AND published.block_hash = generation.observation_block_hash
           AND published.stage = 'proxy'
           AND published.stage_version = generation.observation_stage_version
           AND published.durable_job_id = generation.durable_job_id
           AND published.job_generation = generation.job_generation
           AND published.state = 'complete'
          WHERE generation.chain_id = observation.chain_id
            AND generation.proxy_address = observation.proxy_address
            AND generation.observation_block_hash = observation.block_hash
            AND generation.observation_stage_version = observation.stage_version
      )
    ORDER BY observation.block_number ASC, observation.block_hash ASC
    LIMIT 1
)
SELECT first_observation.block_number::text AS from_block,
       tip.number::text AS to_block,
       (CASE WHEN first_observation.proxy_pattern = 'clone' AND
                         sqlc.arg(history_kind)::text = 'upgrades' THEN TRUE
            ELSE proxy_interaction_coverage_contains(
                sqlc.arg(chain_id)::numeric,
                first_observation.block_number, first_observation.block_hash,
                tip.number, tip.block_hash
            ) END)::boolean AS complete,
       first_observation.proxy_pattern
FROM canonical_tip AS tip
JOIN first_observation ON TRUE;

-- name: ListProxyUpgradeHistory :many
WITH published_proxy_observations AS (
    SELECT observation.chain_id, observation.proxy_address,
           observation.block_number, observation.block_hash,
           COALESCE(resolution.proxy_code_hash, observation.proxy_code_hash)::bytea
               AS proxy_code_hash,
           COALESCE(resolution.proxy_kind, observation.proxy_kind)::text
               AS proxy_kind,
           COALESCE(resolution.implementation_address, observation.implementation_address)::bytea
               AS implementation_address,
           COALESCE(resolution.beacon_address, observation.beacon_address)::bytea
               AS beacon_address,
           COALESCE(
               resolution.implementation_code_hash,
               observation.implementation_code_hash
           )::bytea AS implementation_code_hash,
           CASE WHEN resolution.id IS NULL THEN observation.confidence
                ELSE 'verified'::text END AS confidence,
           observation.canonical, observation.details,
           observation.stage_version,
           COALESCE(resolution.proxy_pattern, observation.proxy_pattern)::text
               AS proxy_pattern,
           COALESCE(resolution.standard_version, observation.standard_version)::text
               AS standard_version,
           COALESCE(resolution.admin_address, observation.admin_address)::bytea
               AS admin_address,
           COALESCE(resolution.admin_code_hash, observation.admin_code_hash)::bytea
               AS admin_code_hash,
           COALESCE(resolution.beacon_code_hash, observation.beacon_code_hash)::bytea
               AS beacon_code_hash,
           observation.immutable_args,
           CASE WHEN resolution.id IS NULL THEN observation.evidence_state
                ELSE 'exact'::text END AS evidence_state,
           generation.durable_job_id AS witness_durable_job_id,
           generation.job_generation AS witness_job_generation
    FROM proxy_observations AS observation
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = observation.chain_id
     AND canonical.number = observation.block_number
     AND canonical.block_hash = observation.block_hash
    JOIN LATERAL (
        SELECT witness.durable_job_id, witness.job_generation
        FROM proxy_observation_generations AS witness
        JOIN published_block_stage_results AS published
          ON published.chain_id = witness.chain_id
         AND published.block_hash = witness.observation_block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = witness.observation_stage_version
         AND published.durable_job_id = witness.durable_job_id
         AND published.job_generation = witness.job_generation
         AND published.state = 'complete'
        WHERE witness.chain_id = observation.chain_id
          AND witness.proxy_address = observation.proxy_address
          AND witness.observation_block_hash = observation.block_hash
          AND witness.observation_stage_version = observation.stage_version
        ORDER BY witness.id DESC
        LIMIT 1
    ) AS generation ON TRUE
    LEFT JOIN LATERAL (
        SELECT exact.*
        FROM proxy_artifact_resolutions AS exact
        WHERE exact.chain_id = observation.chain_id
          AND exact.proxy_address = observation.proxy_address
          AND exact.observation_block_hash = observation.block_hash
          AND exact.observation_stage_version = observation.stage_version
          AND exact.durable_job_id = generation.durable_job_id
          AND exact.job_generation = generation.job_generation
        ORDER BY exact.id DESC
        LIMIT 1
    ) AS resolution ON TRUE
    WHERE observation.chain_id = sqlc.arg(chain_id)::numeric
      AND observation.proxy_address = sqlc.arg(proxy_address)::bytea
      AND observation.stage_version = 2
      AND observation.canonical = TRUE
      AND observation.block_number <= sqlc.arg(snapshot_number)::numeric
), relevant_beacons AS (
    SELECT DISTINCT beacon_address
    FROM published_proxy_observations
    WHERE beacon_address IS NOT NULL
), published_negative_evidence AS (
    SELECT evidence.*
    FROM proxy_detection_evidence AS evidence
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = evidence.chain_id
     AND canonical.number = evidence.block_number
     AND canonical.block_hash = evidence.block_hash
    JOIN published_block_stage_results AS published
      ON published.chain_id = evidence.chain_id
     AND published.block_hash = evidence.block_hash
     AND published.stage = 'proxy'
     AND published.stage_version = evidence.stage_version
     AND published.durable_job_id = evidence.durable_job_id
     AND published.job_generation = evidence.job_generation
     AND published.state = 'complete'
    WHERE evidence.chain_id = sqlc.arg(chain_id)::numeric
      AND evidence.stage_version = 2
      AND evidence.canonical = TRUE
      AND evidence.block_number <= sqlc.arg(snapshot_number)::numeric
      AND NOT (
          evidence.reason = 'immutable_args_creation_unverified'
          AND evidence.candidate_kind = 'proxy'
          AND EXISTS (
              SELECT 1
              FROM published_proxy_observations AS exact_clone
              WHERE exact_clone.proxy_address = evidence.address
                AND exact_clone.proxy_code_hash = evidence.code_hash
                AND exact_clone.proxy_pattern = 'clone'
                AND exact_clone.evidence_state = 'exact'
                AND octet_length(exact_clone.immutable_args) > 0
                AND exact_clone.details->>'immutable_args_creation_authenticated' = 'true'
          )
      )
      AND (
          (evidence.candidate_kind = 'proxy' AND
           evidence.address = sqlc.arg(proxy_address)::bytea) OR
          (evidence.candidate_kind = 'beacon' AND
           evidence.address IN (SELECT beacon_address FROM relevant_beacons))
      )
), published_beacon_observations AS (
    SELECT observation.*,
           generation.durable_job_id AS witness_durable_job_id,
           generation.job_generation AS witness_job_generation
    FROM beacon_implementation_observations AS observation
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = observation.chain_id
     AND canonical.number = observation.block_number
     AND canonical.block_hash = observation.block_hash
    JOIN relevant_beacons AS relevant
      ON relevant.beacon_address = observation.beacon_address
    JOIN LATERAL (
        SELECT witness.durable_job_id, witness.job_generation
        FROM beacon_observation_generations AS witness
        JOIN published_block_stage_results AS published
          ON published.chain_id = witness.chain_id
         AND published.block_hash = witness.observation_block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = witness.observation_stage_version
         AND published.durable_job_id = witness.durable_job_id
         AND published.job_generation = witness.job_generation
         AND published.state = 'complete'
        WHERE witness.chain_id = observation.chain_id
          AND witness.beacon_address = observation.beacon_address
          AND witness.observation_block_hash = observation.block_hash
          AND witness.observation_stage_version = observation.stage_version
        ORDER BY witness.id DESC
        LIMIT 1
    ) AS generation ON TRUE
    WHERE observation.chain_id = sqlc.arg(chain_id)::numeric
      AND observation.stage_version = 2
      AND observation.canonical = TRUE
      AND observation.block_number <= sqlc.arg(snapshot_number)::numeric
), published_events AS (
    SELECT event.*
    FROM proxy_upgrade_events AS event
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = event.chain_id
     AND canonical.number = event.block_number
     AND canonical.block_hash = event.block_hash
    WHERE event.chain_id = sqlc.arg(chain_id)::numeric
      AND event.stage_version = 2
      AND event.canonical = TRUE
      AND event.block_number <= sqlc.arg(snapshot_number)::numeric
      AND (
          event.emitter_address = sqlc.arg(proxy_address)::bytea OR
          event.emitter_address IN (SELECT beacon_address FROM relevant_beacons)
      )
      AND EXISTS (
          SELECT 1
          FROM published_block_stage_results AS published
          WHERE published.chain_id = event.chain_id
            AND published.block_hash = event.block_hash
            AND published.stage = 'proxy'
            AND published.stage_version = event.stage_version
            AND published.state = 'complete'
      )
), proxy_beacon_association_points AS (
    SELECT observation.block_number,
           9223372036854775807::bigint AS event_order,
           observation.beacon_address,
           observation.beacon_code_hash,
           observation.proxy_code_hash,
           observation.block_hash AS source_block_hash,
           observation.witness_durable_job_id,
           observation.witness_job_generation,
           TRUE AS observation_source
    FROM published_proxy_observations AS observation
    WHERE observation.beacon_address IS NOT NULL
    UNION ALL
    SELECT event.block_number, event.log_index AS event_order,
           event.target_address AS beacon_address,
           NULL::bytea AS beacon_code_hash,
           NULL::bytea AS proxy_code_hash,
           event.block_hash AS source_block_hash,
           NULL::bigint AS witness_durable_job_id,
           NULL::bigint AS witness_job_generation,
           FALSE AS observation_source
    FROM published_events AS event
    WHERE event.emitter_address = sqlc.arg(proxy_address)::bytea
      AND event.event_kind = 'beacon'
      AND NOT EXISTS (
          SELECT 1
          FROM published_proxy_observations AS immutable_proxy
          WHERE immutable_proxy.proxy_pattern = 'beacon'
            AND immutable_proxy.standard_version = '5.6.1'
            AND immutable_proxy.evidence_state = 'exact'
            AND immutable_proxy.proxy_code_hash = (
                SELECT code.code_hash
                FROM contract_code_observations AS code
                JOIN canonical_blocks AS code_canonical
                  ON code_canonical.chain_id = code.chain_id
                 AND code_canonical.number = code.block_number
                 AND code_canonical.block_hash = code.block_hash
                WHERE code.chain_id = immutable_proxy.chain_id
                  AND code.address = immutable_proxy.proxy_address
                  AND code.canonical = TRUE
                  AND code.block_number <= event.block_number
                ORDER BY code.block_number DESC, code.observed_at DESC,
                         code.code_hash DESC
                LIMIT 1
            )
            AND NOT EXISTS (
                SELECT 1
                FROM published_negative_evidence AS evidence
                WHERE evidence.candidate_kind = 'proxy'
                  AND evidence.address = immutable_proxy.proxy_address
                  AND evidence.block_number <= event.block_number
                  AND (
                      evidence.block_number > immutable_proxy.block_number OR (
                          evidence.block_number = immutable_proxy.block_number
                          AND evidence.block_hash = immutable_proxy.block_hash
                          AND evidence.durable_job_id = immutable_proxy.witness_durable_job_id
                          AND evidence.job_generation >= immutable_proxy.witness_job_generation
                      )
                  )
            )
            AND (
                immutable_proxy.block_number < event.block_number OR
                (immutable_proxy.block_number = event.block_number AND
                 immutable_proxy.beacon_address <> event.target_address)
            )
      )
), beacon_implementation_state_points AS (
    SELECT observation.block_number,
           9223372036854775807::bigint AS event_order,
           observation.beacon_address,
           observation.implementation_address,
           observation.beacon_code_hash,
           observation.block_hash AS source_block_hash,
           observation.witness_durable_job_id,
           observation.witness_job_generation,
           TRUE AS observation_source
    FROM published_beacon_observations AS observation
    UNION ALL
    SELECT event.block_number, event.log_index AS event_order,
           event.emitter_address AS beacon_address,
           event.target_address AS implementation_address,
           (
               SELECT code.code_hash
               FROM contract_code_observations AS code
               JOIN canonical_blocks AS code_canonical
                 ON code_canonical.chain_id = code.chain_id
                AND code_canonical.number = code.block_number
                AND code_canonical.block_hash = code.block_hash
               WHERE code.chain_id = event.chain_id
                 AND code.address = event.emitter_address
                 AND code.canonical = TRUE
                 AND code.block_number <= event.block_number
               ORDER BY code.block_number DESC, code.observed_at DESC
               LIMIT 1
           ) AS beacon_code_hash,
           event.block_hash AS source_block_hash,
           NULL::bigint AS witness_durable_job_id,
           NULL::bigint AS witness_job_generation,
           FALSE AS observation_source
    FROM published_events AS event
    WHERE event.event_kind = 'implementation'
      AND event.emitter_address IN (SELECT beacon_address FROM relevant_beacons)
), proxy_observation_points AS (
    SELECT observation.block_number, observation.block_hash,
           block.timestamp::text AS block_timestamp,
           9223372036854775807::bigint AS event_order,
           1::integer AS source_rank,
           'implementation'::text AS change_type,
           'observation'::text AS evidence_type,
           CASE WHEN observation.proxy_kind = 'beacon'
                THEN beacon.implementation_address
                ELSE observation.implementation_address END AS new_implementation_address,
           NULL::bytea AS transaction_hash, NULL::bigint AS log_index,
           NULL::bytea AS emitter_address,
           observation.beacon_address, observation.beacon_code_hash,
           CASE WHEN observation.proxy_pattern = 'transparent'
                          AND observation.standard_version = '5.6.1'
                          AND observation.evidence_state = 'exact'
                          AND attestation.proxy_attested
                          AND attestation.target_attested
                THEN 'proxy_admin'::text
                WHEN observation.proxy_pattern = 'beacon'
                          AND observation.standard_version = '5.6.1'
                          AND observation.evidence_state = 'exact'
                          AND attestation.proxy_attested
                          AND attestation.target_attested
                THEN 'upgradeable_beacon'::text
                ELSE NULL::text END AS management_kind,
           CASE WHEN observation.proxy_pattern IN ('transparent', 'beacon')
                          AND observation.standard_version = '5.6.1'
                          AND observation.evidence_state = 'exact'
                          AND attestation.proxy_attested
                          AND attestation.target_attested
                THEN CASE WHEN observation.proxy_pattern = 'transparent'
                THEN observation.admin_address
                ELSE observation.beacon_address END
                ELSE NULL::bytea END AS management_address
    FROM published_proxy_observations AS observation
    JOIN blocks AS block
      ON block.chain_id = observation.chain_id
     AND block.number = observation.block_number
     AND block.hash = observation.block_hash
    LEFT JOIN LATERAL (
        SELECT candidate.implementation_address
        FROM beacon_implementation_state_points AS candidate
        WHERE candidate.beacon_address = observation.beacon_address
          AND (
              candidate.block_number < observation.block_number OR
              (candidate.block_number = observation.block_number AND
               candidate.event_order < 9223372036854775807::bigint)
          )
          AND NOT EXISTS (
              SELECT 1
              FROM published_negative_evidence AS evidence
              WHERE evidence.candidate_kind = 'beacon'
                AND evidence.address = candidate.beacon_address
                AND evidence.block_number <= observation.block_number
                AND (
                    evidence.block_number > candidate.block_number OR
                    (evidence.block_number = candidate.block_number AND
                     (NOT candidate.observation_source OR (
                         evidence.block_hash = candidate.source_block_hash
                         AND evidence.durable_job_id = candidate.witness_durable_job_id
                         AND evidence.job_generation >= candidate.witness_job_generation
                     )))
                )
          )
        ORDER BY candidate.block_number DESC, candidate.event_order DESC
        LIMIT 1
    ) AS beacon ON observation.proxy_kind = 'beacon'
    LEFT JOIN LATERAL (
        SELECT EXISTS (
                   SELECT 1
                   FROM verified_contract_proxy_artifacts AS artifact
                   WHERE artifact.chain_id = observation.chain_id
                     AND artifact.address = observation.proxy_address
                     AND artifact.code_hash = observation.proxy_code_hash
                     AND artifact.valid_from_block <= observation.block_number
                     AND artifact.standard_version = '5.6.1'
                     AND artifact.artifact_kind = CASE observation.proxy_pattern
                         WHEN 'transparent' THEN 'transparent_proxy'
                         WHEN 'beacon' THEN 'beacon_proxy'
                         ELSE '' END
               ) AS proxy_attested,
               EXISTS (
                   SELECT 1
                   FROM verified_contract_proxy_artifacts AS artifact
                   WHERE artifact.chain_id = observation.chain_id
                     AND artifact.address = CASE observation.proxy_pattern
                         WHEN 'transparent' THEN observation.admin_address
                         WHEN 'beacon' THEN observation.beacon_address END
                     AND artifact.code_hash = CASE observation.proxy_pattern
                         WHEN 'transparent' THEN observation.admin_code_hash
                         WHEN 'beacon' THEN observation.beacon_code_hash END
                     AND artifact.valid_from_block <= observation.block_number
                     AND artifact.standard_version = '5.6.1'
                     AND artifact.artifact_kind = CASE observation.proxy_pattern
                         WHEN 'transparent' THEN 'proxy_admin'
                         WHEN 'beacon' THEN 'upgradeable_beacon'
                         ELSE '' END
               ) AS target_attested
    ) AS attestation ON observation.proxy_pattern IN ('transparent', 'beacon')
    WHERE observation.proxy_pattern <> 'clone'
      AND NOT EXISTS (
        SELECT 1
        FROM published_negative_evidence AS evidence
        WHERE evidence.address = observation.proxy_address
          AND evidence.candidate_kind = 'proxy'
          AND evidence.block_number = observation.block_number
          AND evidence.block_hash = observation.block_hash
          AND evidence.durable_job_id = observation.witness_durable_job_id
          AND evidence.job_generation >= observation.witness_job_generation
    )
), beacon_observation_points AS (
    SELECT observation.block_number, observation.block_hash,
           block.timestamp::text AS block_timestamp,
           9223372036854775807::bigint AS event_order,
           2::integer AS source_rank,
           'beacon_implementation'::text AS change_type,
           'observation'::text AS evidence_type,
           observation.implementation_address AS new_implementation_address,
           NULL::bytea AS transaction_hash, NULL::bigint AS log_index,
           NULL::bytea AS emitter_address,
           observation.beacon_address, observation.beacon_code_hash,
           CASE WHEN association.proxy_pattern = 'beacon'
                          AND association.standard_version = '5.6.1'
                          AND association.evidence_state = 'exact'
                          AND EXISTS (
                              SELECT 1 FROM verified_contract_proxy_artifacts AS artifact
                              WHERE artifact.chain_id = observation.chain_id
                                AND artifact.address = sqlc.arg(proxy_address)::bytea
                                AND artifact.code_hash = association.proxy_code_hash
                                AND artifact.valid_from_block <= observation.block_number
                                AND artifact.artifact_kind = 'beacon_proxy'
                                AND artifact.standard_version = '5.6.1'
                          )
                          AND EXISTS (
                              SELECT 1 FROM verified_contract_proxy_artifacts AS artifact
                              WHERE artifact.chain_id = observation.chain_id
                                AND artifact.address = observation.beacon_address
                                AND artifact.code_hash = observation.beacon_code_hash
                                AND artifact.valid_from_block <= observation.block_number
                                AND artifact.artifact_kind = 'upgradeable_beacon'
                                AND artifact.standard_version = '5.6.1'
                          )
                THEN 'upgradeable_beacon'::text ELSE NULL::text END AS management_kind,
           CASE WHEN association.proxy_pattern = 'beacon'
                          AND association.standard_version = '5.6.1'
                          AND association.evidence_state = 'exact'
                          AND EXISTS (
                              SELECT 1 FROM verified_contract_proxy_artifacts AS artifact
                              WHERE artifact.chain_id = observation.chain_id
                                AND artifact.address = observation.beacon_address
                                AND artifact.code_hash = observation.beacon_code_hash
                                AND artifact.valid_from_block <= observation.block_number
                                AND artifact.artifact_kind = 'upgradeable_beacon'
                                AND artifact.standard_version = '5.6.1'
                          )
                THEN observation.beacon_address ELSE NULL::bytea END AS management_address
    FROM published_beacon_observations AS observation
    JOIN blocks AS block
      ON block.chain_id = observation.chain_id
     AND block.number = observation.block_number
     AND block.hash = observation.block_hash
    JOIN LATERAL (
        SELECT association.beacon_address, association.proxy_code_hash,
               association.proxy_pattern, association.standard_version,
               association.evidence_state
        FROM published_proxy_observations AS association
        WHERE association.proxy_address = sqlc.arg(proxy_address)::bytea
          AND association.block_number <= observation.block_number
          AND association.proxy_code_hash = (
              SELECT code.code_hash
              FROM contract_code_observations AS code
              JOIN canonical_blocks AS code_canonical
                ON code_canonical.chain_id = code.chain_id
               AND code_canonical.number = code.block_number
               AND code_canonical.block_hash = code.block_hash
              WHERE code.chain_id = association.chain_id
                AND code.address = association.proxy_address
                AND code.canonical = TRUE
                AND code.block_number <= observation.block_number
              ORDER BY code.block_number DESC, code.observed_at DESC,
                       code.code_hash DESC
              LIMIT 1
          )
          AND NOT EXISTS (
              SELECT 1
              FROM published_negative_evidence AS evidence
              WHERE evidence.candidate_kind = 'proxy'
                AND evidence.address = association.proxy_address
                AND evidence.block_number <= observation.block_number
                AND (
                    evidence.block_number > association.block_number OR (
                        evidence.block_number = association.block_number
                        AND evidence.block_hash = association.block_hash
                        AND evidence.durable_job_id = association.witness_durable_job_id
                        AND evidence.job_generation >= association.witness_job_generation
                    )
                )
          )
        ORDER BY association.block_number DESC, association.block_hash DESC
        LIMIT 1
    ) AS association
      ON association.beacon_address = observation.beacon_address
    WHERE NOT EXISTS (
        SELECT 1
        FROM published_negative_evidence AS evidence
        WHERE evidence.candidate_kind = 'beacon'
          AND evidence.address = observation.beacon_address
          AND evidence.block_number = observation.block_number
          AND evidence.block_hash = observation.block_hash
          AND evidence.durable_job_id = observation.witness_durable_job_id
          AND evidence.job_generation >= observation.witness_job_generation
    )
), direct_event_points AS (
    SELECT event.block_number, event.block_hash,
           block.timestamp::text AS block_timestamp,
           event.log_index AS event_order, 0::integer AS source_rank,
           CASE event.event_kind WHEN 'beacon' THEN 'beacon'
                ELSE 'implementation' END AS change_type,
           'event'::text AS evidence_type,
           CASE WHEN event.event_kind = 'beacon'
                THEN beacon.implementation_address
                ELSE event.target_address END AS new_implementation_address,
           event.transaction_hash, event.log_index,
           event.emitter_address,
           CASE WHEN event.event_kind = 'beacon'
                THEN event.target_address ELSE association.beacon_address END AS beacon_address,
           CASE WHEN event.event_kind = 'beacon'
                THEN beacon.beacon_code_hash ELSE association.beacon_code_hash END AS beacon_code_hash,
           COALESCE(management.kind, '')::text AS management_kind,
           management.address AS management_address
    FROM published_events AS event
    JOIN blocks AS block
      ON block.chain_id = event.chain_id
     AND block.number = event.block_number
     AND block.hash = event.block_hash
    JOIN LATERAL (
        SELECT observation.admin_address, observation.beacon_address,
               observation.implementation_address,
               observation.beacon_code_hash, observation.admin_code_hash,
               observation.proxy_code_hash, observation.proxy_pattern,
               observation.proxy_kind, observation.standard_version,
               observation.evidence_state
        FROM published_proxy_observations AS observation
        WHERE observation.proxy_address = sqlc.arg(proxy_address)::bytea
          AND observation.block_number <= event.block_number
          AND observation.proxy_code_hash = (
              SELECT code.code_hash
              FROM contract_code_observations AS code
              JOIN canonical_blocks AS code_canonical
                ON code_canonical.chain_id = code.chain_id
               AND code_canonical.number = code.block_number
               AND code_canonical.block_hash = code.block_hash
              WHERE code.chain_id = observation.chain_id
                AND code.address = observation.proxy_address
                AND code.canonical = TRUE
                AND code.block_number <= event.block_number
              ORDER BY code.block_number DESC, code.observed_at DESC,
                       code.code_hash DESC
              LIMIT 1
          )
          AND NOT EXISTS (
              SELECT 1
              FROM published_negative_evidence AS evidence
              WHERE evidence.candidate_kind = 'proxy'
                AND evidence.address = observation.proxy_address
                AND evidence.block_number <= event.block_number
                AND (
                    evidence.block_number > observation.block_number OR (
                        evidence.block_number = observation.block_number
                        AND evidence.block_hash = observation.block_hash
                        AND evidence.durable_job_id = observation.witness_durable_job_id
                        AND evidence.job_generation >= observation.witness_job_generation
                    )
                )
          )
        ORDER BY observation.block_number DESC, observation.block_hash DESC
        LIMIT 1
    ) AS association ON TRUE
    LEFT JOIN LATERAL (
        SELECT point.implementation_address, point.beacon_code_hash
        FROM beacon_implementation_state_points AS point
        WHERE point.beacon_address = event.target_address
          AND (
              point.block_number < event.block_number OR
              (point.block_number = event.block_number AND
               point.event_order < event.log_index)
          )
          AND NOT EXISTS (
              SELECT 1
              FROM published_negative_evidence AS evidence
              WHERE evidence.candidate_kind = 'beacon'
                AND evidence.address = point.beacon_address
                AND evidence.block_number <= event.block_number
                AND (
                    evidence.block_number > point.block_number OR
                    (evidence.block_number = point.block_number AND
                     (NOT point.observation_source OR (
                         evidence.block_hash = point.source_block_hash
                         AND evidence.durable_job_id = point.witness_durable_job_id
                         AND evidence.job_generation >= point.witness_job_generation
                     )))
                )
          )
        ORDER BY point.block_number DESC, point.event_order DESC
        LIMIT 1
    ) AS beacon ON event.event_kind = 'beacon'
    LEFT JOIN LATERAL (
        SELECT 'proxy_admin'::text AS kind,
               observation.admin_address AS address
        FROM published_proxy_observations AS observation
        WHERE event.event_kind = 'implementation'
          AND observation.proxy_address = sqlc.arg(proxy_address)::bytea
          AND observation.block_number <= event.block_number
          AND observation.standard_version = '5.6.1'
          AND observation.evidence_state = 'exact'
          AND observation.proxy_pattern = 'transparent'
          AND NOT EXISTS (
              SELECT 1
              FROM published_negative_evidence AS evidence
              WHERE evidence.candidate_kind = 'proxy'
                AND evidence.address = observation.proxy_address
                AND evidence.block_number <= event.block_number
                AND (
                    evidence.block_number > observation.block_number OR (
                        evidence.block_number = observation.block_number
                        AND evidence.block_hash = observation.block_hash
                        AND evidence.durable_job_id = observation.witness_durable_job_id
                        AND evidence.job_generation >= observation.witness_job_generation
                    )
                )
          )
          AND EXISTS (
              SELECT 1
              FROM verified_contract_proxy_artifacts AS artifact
              WHERE artifact.chain_id = event.chain_id
                AND artifact.address = sqlc.arg(proxy_address)::bytea
                AND artifact.code_hash = observation.proxy_code_hash
                AND artifact.valid_from_block <= event.block_number
                AND artifact.standard_version = '5.6.1'
                AND artifact.artifact_kind = 'transparent_proxy'
          )
          AND EXISTS (
              SELECT 1
              FROM verified_contract_proxy_artifacts AS artifact
              WHERE artifact.chain_id = event.chain_id
                AND artifact.address = observation.admin_address
                AND artifact.code_hash = observation.admin_code_hash
                AND artifact.valid_from_block <= event.block_number
                AND artifact.standard_version = '5.6.1'
                AND artifact.artifact_kind = 'proxy_admin'
          )
          AND EXISTS (
              SELECT 1
              FROM transactions AS transaction
              WHERE transaction.chain_id = event.chain_id
                AND transaction.hash = event.transaction_hash
                AND lower(COALESCE(transaction.raw->>'to', '')) =
                    '0x' || encode(observation.admin_address, 'hex')
          )
        ORDER BY observation.block_number DESC, observation.block_hash DESC
        LIMIT 1
    ) AS management ON TRUE
    WHERE event.emitter_address = sqlc.arg(proxy_address)::bytea
      AND association.proxy_pattern <> 'clone'
      AND (
          (event.event_kind = 'implementation' AND
           association.proxy_kind = 'eip1967') OR
          (event.event_kind = 'beacon' AND
           association.proxy_kind = 'beacon' AND
           NOT (association.proxy_pattern = 'beacon' AND
                association.standard_version = '5.6.1' AND
                association.evidence_state = 'exact'))
      )
      AND (
          EXISTS (
              SELECT 1
              FROM transaction_state_changes AS change
              WHERE change.chain_id = event.chain_id
                AND change.block_number = event.block_number
                AND change.block_hash = event.block_hash
                AND change.transaction_hash = event.transaction_hash
                AND change.address = event.emitter_address
                AND change.field_kind = 'storage'
                AND change.storage_key = CASE event.event_kind
                    WHEN 'implementation' THEN decode(
                        '360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc', 'hex')
                    ELSE decode(
                        'a3f0ad74e5423aebfd80d3ef4346578335a9a72aeaee59ff6cb3582b35133d50', 'hex')
                    END
                AND change.canonical = TRUE
                AND lower(regexp_replace(COALESCE(change.after_value, ''),
                                         '^0x0*', '')) =
                    lower(regexp_replace('0x' || encode(event.target_address, 'hex'),
                                         '^0x0*', ''))
          ) OR (
              NOT EXISTS (
                  SELECT 1
                  FROM published_events AS later_event
                  WHERE later_event.block_hash = event.block_hash
                    AND later_event.emitter_address = event.emitter_address
                    AND later_event.event_kind = event.event_kind
                    AND later_event.log_index > event.log_index
              )
              AND CASE event.event_kind
                  WHEN 'implementation' THEN
                      association.implementation_address = event.target_address
                  ELSE association.beacon_address = event.target_address
              END
          )
      )
), beacon_event_points AS (
    SELECT event.block_number, event.block_hash,
           block.timestamp::text AS block_timestamp,
           event.log_index AS event_order, 0::integer AS source_rank,
           'beacon_implementation'::text AS change_type,
           'event'::text AS evidence_type,
           event.target_address AS new_implementation_address,
           event.transaction_hash, event.log_index,
           event.emitter_address,
           event.emitter_address AS beacon_address,
           association.beacon_code_hash,
           CASE WHEN attestation.exact THEN 'upgradeable_beacon'::text
                ELSE NULL::text END AS management_kind,
           CASE WHEN attestation.exact THEN event.emitter_address
                ELSE NULL::bytea END AS management_address
    FROM published_events AS event
    JOIN blocks AS block
      ON block.chain_id = event.chain_id
     AND block.number = event.block_number
     AND block.hash = event.block_hash
    JOIN LATERAL (
        SELECT point.beacon_address, point.beacon_code_hash
        FROM proxy_beacon_association_points AS point
        WHERE (
              point.block_number < event.block_number OR
              (point.block_number = event.block_number AND
               point.event_order < event.log_index)
          )
          AND (
              NOT point.observation_source OR
              point.proxy_code_hash = (
                  SELECT code.code_hash
                  FROM contract_code_observations AS code
                  JOIN canonical_blocks AS code_canonical
                    ON code_canonical.chain_id = code.chain_id
                   AND code_canonical.number = code.block_number
                   AND code_canonical.block_hash = code.block_hash
                  WHERE code.chain_id = sqlc.arg(chain_id)::numeric
                    AND code.address = sqlc.arg(proxy_address)::bytea
                    AND code.canonical = TRUE
                    AND code.block_number <= event.block_number
                  ORDER BY code.block_number DESC, code.observed_at DESC,
                           code.code_hash DESC
                  LIMIT 1
              )
          )
          AND NOT EXISTS (
              SELECT 1
              FROM published_negative_evidence AS evidence
              WHERE evidence.candidate_kind = 'proxy'
                AND evidence.address = sqlc.arg(proxy_address)::bytea
                AND evidence.block_number <= event.block_number
                AND (
                    evidence.block_number > point.block_number OR
                    (evidence.block_number = point.block_number AND
                     (NOT point.observation_source OR (
                         evidence.block_hash = point.source_block_hash
                         AND evidence.durable_job_id = point.witness_durable_job_id
                         AND evidence.job_generation >= point.witness_job_generation
                     )))
                )
          )
        ORDER BY point.block_number DESC, point.event_order DESC
        LIMIT 1
    ) AS association ON association.beacon_address = event.emitter_address
    LEFT JOIN LATERAL (
        SELECT TRUE AS exact
        FROM published_proxy_observations AS context
        WHERE context.block_number < event.block_number
          AND context.proxy_pattern = 'beacon'
          AND context.standard_version = '5.6.1'
          AND context.evidence_state = 'exact'
          AND EXISTS (
              SELECT 1
              FROM verified_contract_proxy_artifacts AS artifact
              WHERE artifact.chain_id = event.chain_id
                AND artifact.address = sqlc.arg(proxy_address)::bytea
                AND artifact.code_hash = context.proxy_code_hash
                AND artifact.valid_from_block <= event.block_number
                AND artifact.artifact_kind = 'beacon_proxy'
                AND artifact.standard_version = '5.6.1'
          )
          AND EXISTS (
              SELECT 1
              FROM verified_contract_proxy_artifacts AS artifact
              JOIN LATERAL (
                  SELECT code.code_hash
                  FROM contract_code_observations AS code
                  JOIN canonical_blocks AS canonical
                    ON canonical.chain_id = code.chain_id
                   AND canonical.number = code.block_number
                   AND canonical.block_hash = code.block_hash
                  WHERE code.chain_id = event.chain_id
                    AND code.address = event.emitter_address
                    AND code.canonical = TRUE
                    AND code.block_number <= event.block_number
                  ORDER BY code.block_number DESC, code.observed_at DESC
                  LIMIT 1
              ) AS code ON code.code_hash = artifact.code_hash
              WHERE artifact.chain_id = event.chain_id
                AND artifact.address = event.emitter_address
                AND artifact.valid_from_block <= event.block_number
                AND artifact.artifact_kind = 'upgradeable_beacon'
                AND artifact.standard_version = '5.6.1'
          )
          AND EXISTS (
              SELECT 1
              FROM transactions AS transaction
              WHERE transaction.chain_id = event.chain_id
                AND transaction.hash = event.transaction_hash
                AND lower(COALESCE(transaction.raw->>'to', '')) =
                    '0x' || encode(event.emitter_address, 'hex')
          )
        ORDER BY context.block_number DESC, context.block_hash DESC
        LIMIT 1
    ) AS attestation ON TRUE
    WHERE event.event_kind = 'implementation'
      AND event.emitter_address <> sqlc.arg(proxy_address)::bytea
      AND (
          attestation.exact OR (
              NOT EXISTS (
                  SELECT 1
                  FROM published_events AS later_event
                  WHERE later_event.block_hash = event.block_hash
                    AND later_event.emitter_address = event.emitter_address
                    AND later_event.event_kind = 'implementation'
                    AND later_event.log_index > event.log_index
              )
              AND EXISTS (
                  SELECT 1
                  FROM published_beacon_observations AS observed
                  WHERE observed.beacon_address = event.emitter_address
                    AND observed.block_hash = event.block_hash
                    AND observed.implementation_address = event.target_address
              )
          )
      )
), points AS (
    SELECT * FROM proxy_observation_points
    UNION ALL SELECT * FROM beacon_observation_points
    UNION ALL SELECT * FROM direct_event_points
    UNION ALL SELECT * FROM beacon_event_points
), canonical_proxy_code AS (
    SELECT DISTINCT ON (code.block_number, code.block_hash)
           code.block_number, code.block_hash, code.code_hash
    FROM contract_code_observations AS code
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = code.chain_id
     AND canonical.number = code.block_number
     AND canonical.block_hash = code.block_hash
    WHERE code.chain_id = sqlc.arg(chain_id)::numeric
      AND code.address = sqlc.arg(proxy_address)::bytea
      AND code.canonical = TRUE
      AND code.block_number <= sqlc.arg(snapshot_number)::numeric
    ORDER BY code.block_number, code.block_hash,
             code.observed_at DESC, code.code_hash DESC
), canonical_beacon_code AS (
    SELECT DISTINCT ON (code.address, code.block_number, code.block_hash)
           code.address AS beacon_address,
           code.block_number, code.block_hash, code.code_hash
    FROM contract_code_observations AS code
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = code.chain_id
     AND canonical.number = code.block_number
     AND canonical.block_hash = code.block_hash
    WHERE code.chain_id = sqlc.arg(chain_id)::numeric
      AND code.address IN (SELECT beacon_address FROM relevant_beacons)
      AND code.canonical = TRUE
      AND code.block_number <= sqlc.arg(snapshot_number)::numeric
    ORDER BY code.address, code.block_number, code.block_hash,
             code.observed_at DESC, code.code_hash DESC
), proxy_code_epoch_points AS (
    SELECT code.block_number, code.block_hash, code.code_hash,
           lag(code.code_hash) OVER (
               ORDER BY code.block_number, code.block_hash
           ) AS previous_code_hash
    FROM canonical_proxy_code AS code
), beacon_code_epoch_points AS (
    SELECT code.beacon_address, code.block_number, code.block_hash,
           code.code_hash,
           lag(code.code_hash) OVER (
               PARTITION BY code.beacon_address
               ORDER BY code.block_number, code.block_hash
           ) AS previous_code_hash
    FROM canonical_beacon_code AS code
), discontinuities AS (
    -- Negative evidence and code replacement both end the current continuity
    -- epoch. Materialize one marker per block and assign epochs with one
    -- window pass; a correlated count here would rescan all evidence for every
    -- history point on long-lived proxies.
    SELECT DISTINCT ON (candidate.block_number)
           candidate.block_number, candidate.block_hash
    FROM (
        SELECT evidence.block_number, evidence.block_hash
        FROM published_negative_evidence AS evidence
        WHERE evidence.candidate_kind = 'proxy'
           OR (
               evidence.candidate_kind = 'beacon'
               AND evidence.address = (
                   SELECT observation.beacon_address
                   FROM published_proxy_observations AS observation
                   WHERE observation.proxy_kind = 'beacon'
                     AND observation.block_number <= evidence.block_number
                     AND observation.proxy_code_hash = (
                         SELECT code.code_hash
                         FROM canonical_proxy_code AS code
                         WHERE code.block_number <= evidence.block_number
                         ORDER BY code.block_number DESC, code.block_hash DESC
                         LIMIT 1
                     )
                     AND NOT EXISTS (
                         SELECT 1
                         FROM published_negative_evidence AS proxy_negative
                         WHERE proxy_negative.candidate_kind = 'proxy'
                           AND proxy_negative.address = observation.proxy_address
                           AND proxy_negative.block_number <= evidence.block_number
                           AND (
                               proxy_negative.block_number > observation.block_number OR (
                                   proxy_negative.block_number = observation.block_number
                                   AND proxy_negative.block_hash = observation.block_hash
                                   AND proxy_negative.durable_job_id = observation.witness_durable_job_id
                                   AND proxy_negative.job_generation >= observation.witness_job_generation
                               )
                           )
                     )
                   ORDER BY observation.block_number DESC,
                            observation.block_hash DESC
                   LIMIT 1
               )
           )
        UNION ALL
        SELECT epoch.block_number, epoch.block_hash
        FROM proxy_code_epoch_points AS epoch
        WHERE epoch.previous_code_hash IS NOT NULL
          AND epoch.previous_code_hash <> epoch.code_hash
        UNION ALL
        SELECT epoch.block_number, epoch.block_hash
        FROM beacon_code_epoch_points AS epoch
        WHERE epoch.previous_code_hash IS NOT NULL
          AND epoch.previous_code_hash <> epoch.code_hash
          AND epoch.beacon_address = (
              SELECT observation.beacon_address
              FROM published_proxy_observations AS observation
              WHERE observation.proxy_kind = 'beacon'
                AND observation.block_number <= epoch.block_number
                AND observation.proxy_code_hash = (
                    SELECT code.code_hash
                    FROM canonical_proxy_code AS code
                    WHERE code.block_number <= epoch.block_number
                    ORDER BY code.block_number DESC, code.block_hash DESC
                    LIMIT 1
                )
                AND NOT EXISTS (
                    SELECT 1
                    FROM published_negative_evidence AS proxy_negative
                    WHERE proxy_negative.candidate_kind = 'proxy'
                      AND proxy_negative.address = observation.proxy_address
                      AND proxy_negative.block_number <= epoch.block_number
                      AND (
                          proxy_negative.block_number > observation.block_number OR (
                              proxy_negative.block_number = observation.block_number
                              AND proxy_negative.block_hash = observation.block_hash
                              AND proxy_negative.durable_job_id = observation.witness_durable_job_id
                              AND proxy_negative.job_generation >= observation.witness_job_generation
                          )
                      )
                )
              ORDER BY observation.block_number DESC, observation.block_hash DESC
              LIMIT 1
          )
    ) AS candidate
    ORDER BY candidate.block_number, candidate.block_hash DESC
), sequenced_input AS (
    SELECT points.*, 0::bigint AS reset_marker, FALSE AS reset_point
    FROM points
    UNION ALL
    SELECT discontinuity.block_number, discontinuity.block_hash,
           '0'::text AS block_timestamp,
           (-1)::bigint AS event_order,
           (-1)::integer AS source_rank,
           ''::text AS change_type, ''::text AS evidence_type,
           NULL::bytea AS new_implementation_address,
           NULL::bytea AS transaction_hash, NULL::bigint AS log_index,
           NULL::bytea AS emitter_address,
           NULL::bytea AS beacon_address, NULL::bytea AS beacon_code_hash,
           NULL::text AS management_kind, NULL::bytea AS management_address,
           1::bigint AS reset_marker, TRUE AS reset_point
    FROM discontinuities AS discontinuity
), sequenced AS (
    SELECT input.*,
           sum(input.reset_marker) OVER (
               ORDER BY input.block_number, input.event_order,
                        input.source_rank
               ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
           ) AS continuity_epoch
    FROM sequenced_input AS input
), target_is_clone AS (
    -- A currently effective Clone is immutable and therefore has no upgrade
    -- history. Historical Clone epochs do not suppress a later, different
    -- proxy code epoch at the same address.
    SELECT COALESCE((
        SELECT observation.proxy_pattern = 'clone'
        FROM published_proxy_observations AS observation
        WHERE observation.proxy_code_hash = (
                  SELECT code.code_hash
                  FROM contract_code_observations AS code
                  JOIN canonical_blocks AS canonical
                    ON canonical.chain_id = code.chain_id
                   AND canonical.number = code.block_number
                   AND canonical.block_hash = code.block_hash
                  WHERE code.chain_id = sqlc.arg(chain_id)::numeric
                    AND code.address = sqlc.arg(proxy_address)::bytea
                    AND code.canonical = TRUE
                    AND code.block_number <= sqlc.arg(snapshot_number)::numeric
                  ORDER BY code.block_number DESC, code.observed_at DESC,
                           code.code_hash DESC
                  LIMIT 1
              )
          AND NOT EXISTS (
              SELECT 1
              FROM published_negative_evidence AS evidence
              WHERE evidence.candidate_kind = 'proxy'
                AND evidence.address = observation.proxy_address
                AND (
                    evidence.block_number > observation.block_number OR (
                        evidence.block_number = observation.block_number
                        AND evidence.block_hash = observation.block_hash
                        AND evidence.durable_job_id = observation.witness_durable_job_id
                        AND evidence.job_generation >= observation.witness_job_generation
                    )
                )
          )
        ORDER BY observation.block_number DESC, observation.block_hash DESC
        LIMIT 1
    ), FALSE) AS value
), ordered AS (
    SELECT points.*,
           lag(new_implementation_address) OVER (
               PARTITION BY continuity_epoch
               ORDER BY block_number, event_order, source_rank
           ) AS old_implementation_address
    FROM sequenced AS points
    WHERE NOT reset_point AND new_implementation_address IS NOT NULL
), changes AS (
    SELECT *
    FROM ordered
    WHERE evidence_type = 'event'
       OR (old_implementation_address IS NOT NULL AND
           old_implementation_address <> new_implementation_address)
), bounded AS (
    SELECT *
    FROM changes
    WHERE NOT (SELECT value FROM target_is_clone)
      AND (
          NOT sqlc.arg(has_boundary)::boolean
          OR block_number < sqlc.arg(before_block_number)::numeric
          OR (block_number = sqlc.arg(before_block_number)::numeric AND
              event_order < sqlc.arg(before_event_order)::bigint)
          OR (block_number = sqlc.arg(before_block_number)::numeric AND
              event_order = sqlc.arg(before_event_order)::bigint AND
              source_rank < sqlc.arg(before_source_rank)::integer)
      )
)
SELECT bounded.block_number::text AS block_number,
       bounded.block_hash, bounded.block_timestamp,
       bounded.event_order, bounded.source_rank,
       bounded.change_type, bounded.evidence_type,
       bounded.old_implementation_address::bytea AS old_implementation_address,
       old_code.code_hash AS old_implementation_code_hash,
       bounded.new_implementation_address::bytea AS new_implementation_address,
       new_code.code_hash AS new_implementation_code_hash,
       bounded.transaction_hash, bounded.log_index,
       bounded.emitter_address, bounded.beacon_address,
       bounded.beacon_code_hash,
       COALESCE(bounded.management_kind, '')::text AS management_kind,
       bounded.management_address,
       management_code.code_hash AS management_code_hash,
       -- Historical rows identify event-time code. Verification state answers
       -- whether that immutable code identity is known by the requested
       -- snapshot, even when verification was published after the event.
       EXISTS (
           SELECT 1 FROM verified_contracts AS verified
           WHERE verified.chain_id = sqlc.arg(chain_id)::numeric
             AND verified.address = bounded.old_implementation_address
             AND verified.code_hash = old_code.code_hash
             AND verified.valid_from_block <= sqlc.arg(snapshot_number)::numeric
       ) AS old_implementation_verified,
       EXISTS (
           SELECT 1 FROM verified_contracts AS verified
           WHERE verified.chain_id = sqlc.arg(chain_id)::numeric
             AND verified.address = bounded.new_implementation_address
             AND verified.code_hash = new_code.code_hash
             AND verified.valid_from_block <= sqlc.arg(snapshot_number)::numeric
       ) AS new_implementation_verified,
       EXISTS (
           SELECT 1 FROM verified_contracts AS verified
           WHERE verified.chain_id = sqlc.arg(chain_id)::numeric
             AND verified.address = bounded.management_address
             AND verified.code_hash = management_code.code_hash
             AND verified.valid_from_block <= sqlc.arg(snapshot_number)::numeric
       ) AS management_verified
FROM bounded
LEFT JOIN LATERAL (
    SELECT observation.code_hash
    FROM contract_code_observations AS observation
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = observation.chain_id
     AND canonical.number = observation.block_number
     AND canonical.block_hash = observation.block_hash
    WHERE observation.chain_id = sqlc.arg(chain_id)::numeric
      AND observation.address = bounded.old_implementation_address
      AND observation.canonical = TRUE
      AND observation.block_number <= bounded.block_number
    ORDER BY observation.block_number DESC, observation.observed_at DESC
    LIMIT 1
) AS old_code ON TRUE
LEFT JOIN LATERAL (
    SELECT observation.code_hash
    FROM contract_code_observations AS observation
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = observation.chain_id
     AND canonical.number = observation.block_number
     AND canonical.block_hash = observation.block_hash
    WHERE observation.chain_id = sqlc.arg(chain_id)::numeric
      AND observation.address = bounded.new_implementation_address
      AND observation.canonical = TRUE
      AND observation.block_number <= bounded.block_number
    ORDER BY observation.block_number DESC, observation.observed_at DESC
    LIMIT 1
) AS new_code ON TRUE
LEFT JOIN LATERAL (
    SELECT observation.code_hash
    FROM contract_code_observations AS observation
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = observation.chain_id
     AND canonical.number = observation.block_number
     AND canonical.block_hash = observation.block_hash
    WHERE observation.chain_id = sqlc.arg(chain_id)::numeric
      AND observation.address = bounded.management_address
      AND observation.canonical = TRUE
      AND observation.block_number <= bounded.block_number
    ORDER BY observation.block_number DESC, observation.observed_at DESC
    LIMIT 1
) AS management_code ON TRUE
WHERE new_code.code_hash IS NOT NULL
  AND (COALESCE(bounded.management_kind, '') = '' OR
       management_code.code_hash IS NOT NULL)
ORDER BY bounded.block_number DESC, bounded.event_order DESC,
         bounded.source_rank DESC
LIMIT sqlc.arg(page_limit);

-- name: ListProxyInitializationHistory :many
WITH published_proxy_observations AS (
    SELECT observation.chain_id, observation.proxy_address,
           observation.block_number, observation.block_hash,
           COALESCE(resolution.proxy_code_hash, observation.proxy_code_hash)::bytea
               AS proxy_code_hash,
           COALESCE(resolution.proxy_kind, observation.proxy_kind)::text
               AS proxy_kind,
           COALESCE(resolution.implementation_address, observation.implementation_address)::bytea
               AS implementation_address,
           COALESCE(resolution.beacon_address, observation.beacon_address)::bytea
               AS beacon_address,
           COALESCE(
               resolution.implementation_code_hash,
               observation.implementation_code_hash
           )::bytea AS implementation_code_hash,
           CASE WHEN resolution.id IS NULL THEN observation.confidence
                ELSE 'verified'::text END AS confidence,
           observation.canonical, observation.details,
           observation.stage_version,
           COALESCE(resolution.proxy_pattern, observation.proxy_pattern)::text
               AS proxy_pattern,
           COALESCE(resolution.standard_version, observation.standard_version)::text
               AS standard_version,
           COALESCE(resolution.admin_address, observation.admin_address)::bytea
               AS admin_address,
           COALESCE(resolution.admin_code_hash, observation.admin_code_hash)::bytea
               AS admin_code_hash,
           COALESCE(resolution.beacon_code_hash, observation.beacon_code_hash)::bytea
               AS beacon_code_hash,
           observation.immutable_args,
           CASE WHEN resolution.id IS NULL THEN observation.evidence_state
                ELSE 'exact'::text END AS evidence_state,
           generation.durable_job_id AS witness_durable_job_id,
           generation.job_generation AS witness_job_generation
    FROM proxy_observations AS observation
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = observation.chain_id
     AND canonical.number = observation.block_number
     AND canonical.block_hash = observation.block_hash
    JOIN LATERAL (
        SELECT witness.durable_job_id, witness.job_generation
        FROM proxy_observation_generations AS witness
        JOIN published_block_stage_results AS published
          ON published.chain_id = witness.chain_id
         AND published.block_hash = witness.observation_block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = witness.observation_stage_version
         AND published.durable_job_id = witness.durable_job_id
         AND published.job_generation = witness.job_generation
         AND published.state = 'complete'
        WHERE witness.chain_id = observation.chain_id
          AND witness.proxy_address = observation.proxy_address
          AND witness.observation_block_hash = observation.block_hash
          AND witness.observation_stage_version = observation.stage_version
        ORDER BY witness.id DESC
        LIMIT 1
    ) AS generation ON TRUE
    LEFT JOIN LATERAL (
        SELECT exact.*
        FROM proxy_artifact_resolutions AS exact
        WHERE exact.chain_id = observation.chain_id
          AND exact.proxy_address = observation.proxy_address
          AND exact.observation_block_hash = observation.block_hash
          AND exact.observation_stage_version = observation.stage_version
          AND exact.durable_job_id = generation.durable_job_id
          AND exact.job_generation = generation.job_generation
        ORDER BY exact.id DESC
        LIMIT 1
    ) AS resolution ON TRUE
    WHERE observation.chain_id = sqlc.arg(chain_id)::numeric
      AND observation.proxy_address = sqlc.arg(proxy_address)::bytea
      AND observation.stage_version = 2
      AND observation.canonical = TRUE
      AND observation.block_number <= sqlc.arg(snapshot_number)::numeric
), relevant_beacons AS (
    SELECT DISTINCT beacon_address
    FROM published_proxy_observations
    WHERE beacon_address IS NOT NULL
), published_negative_evidence AS (
    SELECT evidence.*
    FROM proxy_detection_evidence AS evidence
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = evidence.chain_id
     AND canonical.number = evidence.block_number
     AND canonical.block_hash = evidence.block_hash
    JOIN published_block_stage_results AS published
      ON published.chain_id = evidence.chain_id
     AND published.block_hash = evidence.block_hash
     AND published.stage = 'proxy'
     AND published.stage_version = evidence.stage_version
     AND published.durable_job_id = evidence.durable_job_id
     AND published.job_generation = evidence.job_generation
     AND published.state = 'complete'
    WHERE evidence.chain_id = sqlc.arg(chain_id)::numeric
      AND evidence.stage_version = 2
      AND evidence.canonical = TRUE
      AND evidence.block_number <= sqlc.arg(snapshot_number)::numeric
      AND NOT (
          evidence.reason = 'immutable_args_creation_unverified'
          AND evidence.candidate_kind = 'proxy'
          AND EXISTS (
              SELECT 1
              FROM published_proxy_observations AS exact_clone
              WHERE exact_clone.proxy_address = evidence.address
                AND exact_clone.proxy_code_hash = evidence.code_hash
                AND exact_clone.proxy_pattern = 'clone'
                AND exact_clone.evidence_state = 'exact'
                AND octet_length(exact_clone.immutable_args) > 0
                AND exact_clone.details->>'immutable_args_creation_authenticated' = 'true'
          )
      )
      AND (
          (evidence.candidate_kind = 'proxy' AND
           evidence.address = sqlc.arg(proxy_address)::bytea) OR
          (evidence.candidate_kind = 'beacon' AND
           evidence.address IN (SELECT beacon_address FROM relevant_beacons))
      )
), published_beacon_observations AS (
    SELECT observation.*,
           generation.durable_job_id AS witness_durable_job_id,
           generation.job_generation AS witness_job_generation
    FROM beacon_implementation_observations AS observation
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = observation.chain_id
     AND canonical.number = observation.block_number
     AND canonical.block_hash = observation.block_hash
    JOIN relevant_beacons AS relevant
      ON relevant.beacon_address = observation.beacon_address
    JOIN LATERAL (
        SELECT witness.durable_job_id, witness.job_generation
        FROM beacon_observation_generations AS witness
        JOIN published_block_stage_results AS published
          ON published.chain_id = witness.chain_id
         AND published.block_hash = witness.observation_block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = witness.observation_stage_version
         AND published.durable_job_id = witness.durable_job_id
         AND published.job_generation = witness.job_generation
         AND published.state = 'complete'
        WHERE witness.chain_id = observation.chain_id
          AND witness.beacon_address = observation.beacon_address
          AND witness.observation_block_hash = observation.block_hash
          AND witness.observation_stage_version = observation.stage_version
        ORDER BY witness.id DESC
        LIMIT 1
    ) AS generation ON TRUE
    WHERE observation.chain_id = sqlc.arg(chain_id)::numeric
      AND observation.stage_version = 2
      AND observation.canonical = TRUE
      AND observation.block_number <= sqlc.arg(snapshot_number)::numeric
), published_upgrade_events AS (
    SELECT event.*
    FROM proxy_upgrade_events AS event
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = event.chain_id
     AND canonical.number = event.block_number
     AND canonical.block_hash = event.block_hash
    WHERE event.chain_id = sqlc.arg(chain_id)::numeric
      AND event.stage_version = 2
      AND event.canonical = TRUE
      AND event.block_number <= sqlc.arg(snapshot_number)::numeric
      AND (
          event.emitter_address = sqlc.arg(proxy_address)::bytea OR
          event.emitter_address IN (SELECT beacon_address FROM relevant_beacons)
      )
      AND EXISTS (
          SELECT 1 FROM published_block_stage_results AS published
          WHERE published.chain_id = event.chain_id
            AND published.block_hash = event.block_hash
            AND published.stage = 'proxy'
            AND published.stage_version = event.stage_version
            AND published.state = 'complete'
      )
), proxy_beacon_association_points AS (
    SELECT observation.block_number,
           9223372036854775807::bigint AS event_order,
           observation.beacon_address,
           observation.proxy_code_hash,
           observation.block_hash AS source_block_hash,
           observation.witness_durable_job_id,
           observation.witness_job_generation,
           TRUE AS observation_source
    FROM published_proxy_observations AS observation
    WHERE observation.beacon_address IS NOT NULL
    UNION ALL
    SELECT event.block_number, event.log_index AS event_order,
           event.target_address AS beacon_address,
           NULL::bytea AS proxy_code_hash,
           event.block_hash AS source_block_hash,
           NULL::bigint AS witness_durable_job_id,
           NULL::bigint AS witness_job_generation,
           FALSE AS observation_source
    FROM published_upgrade_events AS event
    WHERE event.emitter_address = sqlc.arg(proxy_address)::bytea
      AND event.event_kind = 'beacon'
      AND NOT EXISTS (
          SELECT 1
          FROM published_proxy_observations AS immutable_proxy
          WHERE immutable_proxy.proxy_pattern = 'beacon'
            AND immutable_proxy.standard_version = '5.6.1'
            AND immutable_proxy.evidence_state = 'exact'
            AND immutable_proxy.proxy_code_hash = (
                SELECT code.code_hash
                FROM contract_code_observations AS code
                JOIN canonical_blocks AS code_canonical
                  ON code_canonical.chain_id = code.chain_id
                 AND code_canonical.number = code.block_number
                 AND code_canonical.block_hash = code.block_hash
                WHERE code.chain_id = immutable_proxy.chain_id
                  AND code.address = immutable_proxy.proxy_address
                  AND code.canonical = TRUE
                  AND code.block_number <= event.block_number
                ORDER BY code.block_number DESC, code.observed_at DESC,
                         code.code_hash DESC
                LIMIT 1
            )
            AND NOT EXISTS (
                SELECT 1
                FROM published_negative_evidence AS evidence
                WHERE evidence.candidate_kind = 'proxy'
                  AND evidence.address = immutable_proxy.proxy_address
                  AND evidence.block_number <= event.block_number
                  AND (
                      evidence.block_number > immutable_proxy.block_number OR (
                          evidence.block_number = immutable_proxy.block_number
                          AND evidence.block_hash = immutable_proxy.block_hash
                          AND evidence.durable_job_id = immutable_proxy.witness_durable_job_id
                          AND evidence.job_generation >= immutable_proxy.witness_job_generation
                      )
                  )
            )
            AND (
                immutable_proxy.block_number < event.block_number OR
                (immutable_proxy.block_number = event.block_number AND
                 immutable_proxy.beacon_address <> event.target_address)
            )
      )
), beacon_implementation_state_points AS (
    SELECT observation.block_number,
           9223372036854775807::bigint AS event_order,
           observation.beacon_address,
           observation.implementation_address,
           observation.block_hash AS source_block_hash,
           observation.witness_durable_job_id,
           observation.witness_job_generation,
           TRUE AS observation_source
    FROM published_beacon_observations AS observation
    UNION ALL
    SELECT event.block_number, event.log_index AS event_order,
           event.emitter_address AS beacon_address,
           event.target_address AS implementation_address,
           event.block_hash AS source_block_hash,
           NULL::bigint AS witness_durable_job_id,
           NULL::bigint AS witness_job_generation,
           FALSE AS observation_source
    FROM published_upgrade_events AS event
    WHERE event.event_kind = 'implementation'
      AND event.emitter_address IN (SELECT beacon_address FROM relevant_beacons)
), direct_event_points AS (
    SELECT event.block_number, event.log_index,
           CASE WHEN event.event_kind = 'beacon'
                THEN beacon.implementation_address
                ELSE event.target_address END AS implementation_address
    FROM published_upgrade_events AS event
    JOIN LATERAL (
        SELECT observation.proxy_address, observation.proxy_kind,
               observation.implementation_address, observation.beacon_address,
               observation.proxy_pattern, observation.standard_version,
               observation.evidence_state
        FROM published_proxy_observations AS observation
        WHERE observation.block_number <= event.block_number
          AND observation.proxy_code_hash = (
              SELECT code.code_hash
              FROM contract_code_observations AS code
              JOIN canonical_blocks AS code_canonical
                ON code_canonical.chain_id = code.chain_id
               AND code_canonical.number = code.block_number
               AND code_canonical.block_hash = code.block_hash
              WHERE code.chain_id = observation.chain_id
                AND code.address = observation.proxy_address
                AND code.canonical = TRUE
                AND code.block_number <= event.block_number
              ORDER BY code.block_number DESC, code.observed_at DESC,
                       code.code_hash DESC
              LIMIT 1
          )
          AND NOT EXISTS (
              SELECT 1
              FROM published_negative_evidence AS evidence
              WHERE evidence.candidate_kind = 'proxy'
                AND evidence.address = observation.proxy_address
                AND evidence.block_number <= event.block_number
                AND (
                    evidence.block_number > observation.block_number OR (
                        evidence.block_number = observation.block_number
                        AND evidence.block_hash = observation.block_hash
                        AND evidence.durable_job_id = observation.witness_durable_job_id
                        AND evidence.job_generation >= observation.witness_job_generation
                    )
                )
          )
        ORDER BY observation.block_number DESC, observation.block_hash DESC
        LIMIT 1
    ) AS proxy_context ON TRUE
    LEFT JOIN LATERAL (
        SELECT point.implementation_address
        FROM beacon_implementation_state_points AS point
        WHERE point.beacon_address = event.target_address
          AND (
              point.block_number < event.block_number OR
              (point.block_number = event.block_number AND
               point.event_order < event.log_index)
          )
          AND NOT EXISTS (
              SELECT 1
              FROM published_negative_evidence AS evidence
              WHERE evidence.candidate_kind = 'beacon'
                AND evidence.address = point.beacon_address
                AND evidence.block_number <= event.block_number
                AND (
                    evidence.block_number > point.block_number OR
                    (evidence.block_number = point.block_number AND
                     (NOT point.observation_source OR (
                         evidence.block_hash = point.source_block_hash
                         AND evidence.durable_job_id = point.witness_durable_job_id
                         AND evidence.job_generation >= point.witness_job_generation
                     )))
                )
          )
        ORDER BY point.block_number DESC, point.event_order DESC
        LIMIT 1
    ) AS beacon ON event.event_kind = 'beacon'
    WHERE event.emitter_address = sqlc.arg(proxy_address)::bytea
      AND (
          (event.event_kind = 'implementation' AND
           proxy_context.proxy_kind = 'eip1967') OR
          (event.event_kind = 'beacon' AND
           proxy_context.proxy_kind = 'beacon' AND
           NOT (proxy_context.proxy_pattern = 'beacon' AND
                proxy_context.standard_version = '5.6.1' AND
                proxy_context.evidence_state = 'exact'))
      )
      AND (
          EXISTS (
              SELECT 1
              FROM transaction_state_changes AS change
              WHERE change.chain_id = event.chain_id
                AND change.block_number = event.block_number
                AND change.block_hash = event.block_hash
                AND change.transaction_hash = event.transaction_hash
                AND change.address = event.emitter_address
                AND change.field_kind = 'storage'
                AND change.storage_key = CASE event.event_kind
                    WHEN 'implementation' THEN decode(
                        '360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc', 'hex')
                    ELSE decode(
                        'a3f0ad74e5423aebfd80d3ef4346578335a9a72aeaee59ff6cb3582b35133d50', 'hex')
                    END
                AND change.canonical = TRUE
                AND lower(regexp_replace(COALESCE(change.after_value, ''),
                                         '^0x0*', '')) =
                    lower(regexp_replace('0x' || encode(event.target_address, 'hex'),
                                         '^0x0*', ''))
          ) OR (
              NOT EXISTS (
                  SELECT 1
                  FROM published_upgrade_events AS later_event
                  WHERE later_event.block_hash = event.block_hash
                    AND later_event.emitter_address = event.emitter_address
                    AND later_event.event_kind = event.event_kind
                    AND later_event.log_index > event.log_index
              )
              AND CASE event.event_kind
                  WHEN 'implementation' THEN
                      proxy_context.implementation_address = event.target_address
                  ELSE proxy_context.beacon_address = event.target_address
              END
          )
      )
), beacon_event_points AS (
    SELECT event.block_number, event.log_index,
           event.target_address AS implementation_address
    FROM published_upgrade_events AS event
    JOIN LATERAL (
        SELECT point.beacon_address
        FROM proxy_beacon_association_points AS point
        WHERE (
              point.block_number < event.block_number OR
              (point.block_number = event.block_number AND
               point.event_order < event.log_index)
          )
          AND (
              NOT point.observation_source OR
              point.proxy_code_hash = (
                  SELECT code.code_hash
                  FROM contract_code_observations AS code
                  JOIN canonical_blocks AS code_canonical
                    ON code_canonical.chain_id = code.chain_id
                   AND code_canonical.number = code.block_number
                   AND code_canonical.block_hash = code.block_hash
                  WHERE code.chain_id = sqlc.arg(chain_id)::numeric
                    AND code.address = sqlc.arg(proxy_address)::bytea
                    AND code.canonical = TRUE
                    AND code.block_number <= event.block_number
                  ORDER BY code.block_number DESC, code.observed_at DESC,
                           code.code_hash DESC
                  LIMIT 1
              )
          )
          AND NOT EXISTS (
              SELECT 1
              FROM published_negative_evidence AS evidence
              WHERE evidence.candidate_kind = 'proxy'
                AND evidence.address = sqlc.arg(proxy_address)::bytea
                AND evidence.block_number <= event.block_number
                AND (
                    evidence.block_number > point.block_number OR
                    (evidence.block_number = point.block_number AND
                     (NOT point.observation_source OR (
                         evidence.block_hash = point.source_block_hash
                         AND evidence.durable_job_id = point.witness_durable_job_id
                         AND evidence.job_generation >= point.witness_job_generation
                     )))
                )
          )
        ORDER BY point.block_number DESC, point.event_order DESC
        LIMIT 1
    ) AS association ON association.beacon_address = event.emitter_address
    WHERE event.event_kind = 'implementation'
      AND (
          (
              EXISTS (
                  SELECT 1
                  FROM verified_contract_proxy_artifacts AS artifact
                  JOIN LATERAL (
                      SELECT code.code_hash
                      FROM contract_code_observations AS code
                      JOIN canonical_blocks AS canonical
                        ON canonical.chain_id = code.chain_id
                       AND canonical.number = code.block_number
                       AND canonical.block_hash = code.block_hash
                      WHERE code.chain_id = event.chain_id
                        AND code.address = event.emitter_address
                        AND code.canonical = TRUE
                        AND code.block_number <= event.block_number
                      ORDER BY code.block_number DESC, code.observed_at DESC
                      LIMIT 1
                  ) AS code ON code.code_hash = artifact.code_hash
                  WHERE artifact.chain_id = event.chain_id
                    AND artifact.address = event.emitter_address
                    AND artifact.valid_from_block <= event.block_number
                    AND artifact.artifact_kind = 'upgradeable_beacon'
                    AND artifact.standard_version = '5.6.1'
              )
              AND EXISTS (
                  SELECT 1
                  FROM transactions AS transaction
                  WHERE transaction.chain_id = event.chain_id
                    AND transaction.hash = event.transaction_hash
                    AND lower(COALESCE(transaction.raw->>'to', '')) =
                        '0x' || encode(event.emitter_address, 'hex')
              )
          ) OR (
              NOT EXISTS (
                  SELECT 1
                  FROM published_upgrade_events AS later_event
                  WHERE later_event.block_hash = event.block_hash
                    AND later_event.emitter_address = event.emitter_address
                    AND later_event.event_kind = 'implementation'
                    AND later_event.log_index > event.log_index
              )
              AND EXISTS (
                  SELECT 1
                  FROM published_beacon_observations AS observed
                  WHERE observed.beacon_address = event.emitter_address
                    AND observed.block_hash = event.block_hash
                    AND observed.implementation_address = event.target_address
              )
          )
      )
), event_points AS (
    SELECT * FROM direct_event_points
    UNION ALL SELECT * FROM beacon_event_points
), proxy_observation_points AS (
    SELECT observation.block_number,
           9223372036854775807::bigint AS event_order,
           CASE WHEN observation.proxy_kind = 'beacon'
                THEN beacon.implementation_address
                ELSE observation.implementation_address END AS implementation_address
    FROM published_proxy_observations AS observation
    LEFT JOIN LATERAL (
        SELECT candidate.implementation_address
        FROM beacon_implementation_state_points AS candidate
        WHERE candidate.beacon_address = observation.beacon_address
          AND (
              candidate.block_number < observation.block_number OR
              (candidate.block_number = observation.block_number AND
               candidate.event_order < 9223372036854775807::bigint)
          )
          AND NOT EXISTS (
              SELECT 1
              FROM published_negative_evidence AS evidence
              WHERE evidence.candidate_kind = 'beacon'
                AND evidence.address = candidate.beacon_address
                AND evidence.block_number <= observation.block_number
                AND (
                    evidence.block_number > candidate.block_number OR
                    (evidence.block_number = candidate.block_number AND
                     (NOT candidate.observation_source OR (
                         evidence.block_hash = candidate.source_block_hash
                         AND evidence.durable_job_id = candidate.witness_durable_job_id
                         AND evidence.job_generation >= candidate.witness_job_generation
                     )))
                )
          )
        ORDER BY candidate.block_number DESC, candidate.event_order DESC
        LIMIT 1
    ) AS beacon ON observation.proxy_kind = 'beacon'
    WHERE NOT EXISTS (
        SELECT 1
        FROM published_negative_evidence AS evidence
        WHERE evidence.candidate_kind = 'proxy'
          AND evidence.address = observation.proxy_address
          AND evidence.block_number = observation.block_number
          AND evidence.block_hash = observation.block_hash
          AND evidence.durable_job_id = observation.witness_durable_job_id
          AND evidence.job_generation >= observation.witness_job_generation
    )
), beacon_observation_points AS (
    SELECT observation.block_number,
           9223372036854775807::bigint AS event_order,
           observation.implementation_address
    FROM published_beacon_observations AS observation
    JOIN LATERAL (
        SELECT point.beacon_address
        FROM proxy_beacon_association_points AS point
        WHERE point.block_number <= observation.block_number
          AND (
              NOT point.observation_source OR
              point.proxy_code_hash = (
                  SELECT code.code_hash
                  FROM contract_code_observations AS code
                  JOIN canonical_blocks AS code_canonical
                    ON code_canonical.chain_id = code.chain_id
                   AND code_canonical.number = code.block_number
                   AND code_canonical.block_hash = code.block_hash
                  WHERE code.chain_id = sqlc.arg(chain_id)::numeric
                    AND code.address = sqlc.arg(proxy_address)::bytea
                    AND code.canonical = TRUE
                    AND code.block_number <= observation.block_number
                  ORDER BY code.block_number DESC, code.observed_at DESC,
                           code.code_hash DESC
                  LIMIT 1
              )
          )
          AND NOT EXISTS (
              SELECT 1
              FROM published_negative_evidence AS evidence
              WHERE evidence.candidate_kind = 'proxy'
                AND evidence.address = sqlc.arg(proxy_address)::bytea
                AND evidence.block_number <= observation.block_number
                AND (
                    evidence.block_number > point.block_number OR
                    (evidence.block_number = point.block_number AND
                     (NOT point.observation_source OR (
                         evidence.block_hash = point.source_block_hash
                         AND evidence.durable_job_id = point.witness_durable_job_id
                         AND evidence.job_generation >= point.witness_job_generation
                     )))
                )
          )
        ORDER BY point.block_number DESC, point.event_order DESC
        LIMIT 1
    ) AS association ON association.beacon_address = observation.beacon_address
    WHERE NOT EXISTS (
        SELECT 1
        FROM published_negative_evidence AS evidence
        WHERE evidence.candidate_kind = 'beacon'
          AND evidence.address = observation.beacon_address
          AND evidence.block_number = observation.block_number
          AND evidence.block_hash = observation.block_hash
          AND evidence.durable_job_id = observation.witness_durable_job_id
          AND evidence.job_generation >= observation.witness_job_generation
    )
), state_points AS (
    SELECT block_number, log_index AS event_order, implementation_address
    FROM event_points
    UNION ALL
    SELECT block_number, event_order, implementation_address
    FROM proxy_observation_points
    UNION ALL
    SELECT block_number, event_order, implementation_address
    FROM beacon_observation_points
), initialization AS (
    SELECT event.*
    FROM proxy_initialization_events AS event
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = event.chain_id
     AND canonical.number = event.block_number
     AND canonical.block_hash = event.block_hash
    WHERE event.chain_id = sqlc.arg(chain_id)::numeric
      AND event.contract_address = sqlc.arg(proxy_address)::bytea
      AND event.stage_version = 2
      AND event.canonical = TRUE
      AND event.block_number <= sqlc.arg(snapshot_number)::numeric
      AND EXISTS (
          SELECT 1 FROM published_block_stage_results AS published
          WHERE published.chain_id = event.chain_id
            AND published.block_hash = event.block_hash
            AND published.stage = 'proxy'
            AND published.stage_version = event.stage_version
            AND published.state = 'complete'
      )
      AND (
          NOT sqlc.arg(has_boundary)::boolean OR
          event.block_number < sqlc.arg(before_block_number)::numeric OR
          (event.block_number = sqlc.arg(before_block_number)::numeric AND
           event.log_index < sqlc.arg(before_log_index)::bigint)
      )
)
SELECT initialization.version::text AS version,
       initialization.block_number::text AS block_number,
       initialization.block_hash, block.timestamp::text AS block_timestamp,
       initialization.transaction_hash, initialization.log_index,
       COALESCE(preceding.implementation_address,
                observed.implementation_address,
                standalone.address)::bytea AS implementation_address,
       implementation_code.code_hash AS implementation_code_hash,
       -- Initializations keep their event-time implementation association,
       -- while verification state reflects knowledge at the API snapshot.
       EXISTS (
           SELECT 1 FROM verified_contracts AS verified
           WHERE verified.chain_id = sqlc.arg(chain_id)::numeric
             AND verified.address = COALESCE(preceding.implementation_address,
                                             observed.implementation_address,
                                             standalone.address)
             AND verified.code_hash = implementation_code.code_hash
             AND verified.valid_from_block <= sqlc.arg(snapshot_number)::numeric
       ) AS implementation_verified
FROM initialization
JOIN blocks AS block
  ON block.chain_id = initialization.chain_id
 AND block.number = initialization.block_number
 AND block.hash = initialization.block_hash
LEFT JOIN LATERAL (
    SELECT CASE WHEN observation.proxy_kind = 'beacon'
                THEN beacon.implementation_address
                ELSE observation.implementation_address END AS implementation_address,
           observation.block_number AS epoch_block_number,
           TRUE AS association_present
    FROM published_proxy_observations AS observation
    LEFT JOIN LATERAL (
        SELECT point.beacon_address
        FROM proxy_beacon_association_points AS point
        WHERE (
              point.block_number < initialization.block_number OR
              (point.block_number = initialization.block_number AND
               point.event_order < initialization.log_index)
          )
          AND (
              NOT point.observation_source OR
              point.proxy_code_hash = (
                  SELECT code.code_hash
                  FROM contract_code_observations AS code
                  JOIN canonical_blocks AS code_canonical
                    ON code_canonical.chain_id = code.chain_id
                   AND code_canonical.number = code.block_number
                   AND code_canonical.block_hash = code.block_hash
                  WHERE code.chain_id = sqlc.arg(chain_id)::numeric
                    AND code.address = sqlc.arg(proxy_address)::bytea
                    AND code.canonical = TRUE
                    AND code.block_number <= initialization.block_number
                  ORDER BY code.block_number DESC, code.observed_at DESC,
                           code.code_hash DESC
                  LIMIT 1
              )
          )
          AND NOT EXISTS (
              SELECT 1
              FROM published_negative_evidence AS evidence
              WHERE evidence.candidate_kind = 'proxy'
                AND evidence.address = sqlc.arg(proxy_address)::bytea
                AND evidence.block_number <= initialization.block_number
                AND (
                    evidence.block_number > point.block_number OR
                    (evidence.block_number = point.block_number AND
                     (NOT point.observation_source OR (
                         evidence.block_hash = point.source_block_hash
                         AND evidence.durable_job_id = point.witness_durable_job_id
                         AND evidence.job_generation >= point.witness_job_generation
                     )))
                )
          )
        ORDER BY point.block_number DESC, point.event_order DESC
        LIMIT 1
    ) AS association ON observation.proxy_kind = 'beacon'
    LEFT JOIN LATERAL (
        SELECT point.implementation_address
        FROM beacon_implementation_state_points AS point
        WHERE point.beacon_address = COALESCE(association.beacon_address,
                                              observation.beacon_address)
          AND (
              point.block_number < initialization.block_number OR
              (point.block_number = initialization.block_number AND
               point.event_order < initialization.log_index)
          )
          AND NOT EXISTS (
              SELECT 1
              FROM published_negative_evidence AS evidence
              WHERE evidence.candidate_kind = 'beacon'
                AND evidence.address = point.beacon_address
                AND evidence.block_number <= initialization.block_number
                AND (
                    evidence.block_number > point.block_number OR
                    (evidence.block_number = point.block_number AND
                     (NOT point.observation_source OR (
                         evidence.block_hash = point.source_block_hash
                         AND evidence.durable_job_id = point.witness_durable_job_id
                         AND evidence.job_generation >= point.witness_job_generation
                     )))
                )
          )
        ORDER BY point.block_number DESC, point.event_order DESC
        LIMIT 1
    ) AS beacon ON observation.proxy_kind = 'beacon'
    WHERE observation.block_number <= initialization.block_number
      AND observation.proxy_code_hash = (
          SELECT code.code_hash
          FROM contract_code_observations AS code
          JOIN canonical_blocks AS code_canonical
            ON code_canonical.chain_id = code.chain_id
           AND code_canonical.number = code.block_number
           AND code_canonical.block_hash = code.block_hash
          WHERE code.chain_id = observation.chain_id
            AND code.address = observation.proxy_address
            AND code.canonical = TRUE
            AND code.block_number <= initialization.block_number
          ORDER BY code.block_number DESC, code.observed_at DESC,
                   code.code_hash DESC
          LIMIT 1
      )
      AND NOT EXISTS (
          SELECT 1
          FROM published_negative_evidence AS evidence
          WHERE evidence.candidate_kind = 'proxy'
            AND evidence.address = observation.proxy_address
            AND evidence.block_number <= initialization.block_number
            AND (
                evidence.block_number > observation.block_number OR (
                    evidence.block_number = observation.block_number
                    AND evidence.block_hash = observation.block_hash
                    AND evidence.durable_job_id = observation.witness_durable_job_id
                    AND evidence.job_generation >= observation.witness_job_generation
                )
            )
      )
    ORDER BY observation.block_number DESC, observation.block_hash DESC
    LIMIT 1
) AS observed ON TRUE
LEFT JOIN LATERAL (
    SELECT point.implementation_address
    FROM state_points AS point
    WHERE observed.association_present
      AND point.block_number >= observed.epoch_block_number
      AND (
          point.block_number < initialization.block_number OR
          (point.block_number = initialization.block_number AND
           point.event_order < initialization.log_index)
      )
    ORDER BY point.block_number DESC, point.event_order DESC
    LIMIT 1
) AS preceding ON TRUE
LEFT JOIN LATERAL (
    SELECT sqlc.arg(proxy_address)::bytea AS address
    WHERE observed.association_present IS NOT TRUE
) AS standalone ON TRUE
LEFT JOIN LATERAL (
    SELECT observation.code_hash
    FROM contract_code_observations AS observation
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = observation.chain_id
     AND canonical.number = observation.block_number
     AND canonical.block_hash = observation.block_hash
    WHERE observation.chain_id = sqlc.arg(chain_id)::numeric
      AND observation.address = COALESCE(preceding.implementation_address,
                                         observed.implementation_address,
                                         standalone.address)
      AND observation.canonical = TRUE
      AND observation.block_number <= initialization.block_number
    ORDER BY observation.block_number DESC, observation.observed_at DESC
    LIMIT 1
) AS implementation_code ON TRUE
WHERE COALESCE(preceding.implementation_address,
               observed.implementation_address,
               standalone.address) IS NOT NULL
  AND (observed.implementation_address IS NOT NULL OR
       standalone.address IS NOT NULL)
  AND implementation_code.code_hash IS NOT NULL
ORDER BY initialization.block_number DESC, initialization.log_index DESC
LIMIT sqlc.arg(page_limit);
