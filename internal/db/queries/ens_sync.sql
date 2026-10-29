-- Background ENS storage (ADR-0058). Source progress is the short transaction
-- lock shared by projection invalidation, leasing and publication. Never keep
-- it while contacting a resolver. Public reads use only the read queries.

-- name: CreateENSSyncSource :one
INSERT INTO ens_sync_sources(target_chain_id, source_chain_id, genesis_hash, namespace,
        policy_digest, manifest_digest, start_number, start_hash)
    VALUES (@target_chain_id, @source_chain_id, @genesis_hash, @namespace,
        @policy_digest, @manifest_digest, @start_number, @start_hash)
    ON CONFLICT (target_chain_id, source_chain_id, genesis_hash, namespace, policy_digest)
    DO UPDATE SET manifest_digest = EXCLUDED.manifest_digest, start_number = EXCLUDED.start_number,
        start_hash = EXCLUDED.start_hash
RETURNING id;

-- name: InitializeENSSyncProgress :exec
INSERT INTO ens_sync_progress (source_id)
VALUES (sqlc.arg(source_id))
ON CONFLICT (source_id) DO NOTHING;

-- name: LockENSSyncSource :one
SELECT * FROM ens_sync_progress WHERE source_id = @source_id FOR UPDATE;

-- name: PutENSSyncBlock :execrows
INSERT INTO ens_sync_blocks(source_id, number, hash, parent_hash, block_time, canonical)
VALUES (@source_id, @number, @hash, @parent_hash, @block_time, @canonical)
ON CONFLICT (source_id, hash) DO UPDATE SET hash = EXCLUDED.hash
WHERE ens_sync_blocks.number = EXCLUDED.number
  AND ens_sync_blocks.parent_hash = EXCLUDED.parent_hash
  AND ens_sync_blocks.block_time = EXCLUDED.block_time
  AND ens_sync_blocks.canonical = EXCLUDED.canonical;

-- name: PutENSSyncDeployment :exec
INSERT INTO ens_sync_deployments(source_id, address, protocol, block_hash, code_hash, abi_digest, implementation_address, implementation_code_hash)
VALUES (@source_id, @address, @protocol, @block_hash, @code_hash, @abi_digest, @implementation_address, @implementation_code_hash)
ON CONFLICT (source_id, address) DO UPDATE SET protocol = EXCLUDED.protocol,
    block_hash = EXCLUDED.block_hash, code_hash = EXCLUDED.code_hash, abi_digest = EXCLUDED.abi_digest,
    implementation_address = EXCLUDED.implementation_address, implementation_code_hash = EXCLUDED.implementation_code_hash;

-- name: PutENSSyncEvent :exec
INSERT INTO ens_sync_events(source_id, block_hash, log_index, transaction_hash, transaction_index, emitter, topics, data)
VALUES (@source_id, @block_hash, @log_index, @transaction_hash, @transaction_index, @emitter, @topics, @data)
ON CONFLICT (source_id, block_hash, log_index) DO UPDATE SET transaction_hash = EXCLUDED.transaction_hash,
    transaction_index = EXCLUDED.transaction_index, emitter = EXCLUDED.emitter, topics = EXCLUDED.topics, data = EXCLUDED.data;

-- name: SetENSSyncTarget :execrows
UPDATE ens_sync_progress p SET target_number = b.number, target_hash = b.hash,
    healthy_until = @healthy_until, state = 'active', failure_code = NULL
FROM ens_sync_blocks b
WHERE p.source_id = @source_id AND b.source_id = p.source_id AND b.hash = @block_hash
  AND b.canonical AND p.state IN ('pending', 'active')
  AND (p.target_number IS NULL OR b.number >= p.target_number)
  AND (p.target_number IS DISTINCT FROM b.number OR p.target_hash = b.hash);

-- name: AdvanceENSSyncCoverage :execrows
UPDATE ens_sync_progress p SET scanned_number = b.number, scanned_hash = b.hash
FROM ens_sync_sources s, ens_sync_blocks b
WHERE p.source_id = @source_id AND s.id = p.source_id
  AND b.source_id = p.source_id AND b.hash = @block_hash AND b.canonical
  AND p.revision = @revision AND p.state = 'active' AND b.number <= p.target_number
  AND ((p.scanned_number IS NULL AND b.number = s.start_number AND b.hash = s.start_hash)
    OR (b.number = p.scanned_number + 1 AND b.parent_hash = p.scanned_hash AND EXISTS (
        SELECT 1 FROM ens_sync_blocks previous WHERE previous.source_id = p.source_id AND previous.hash = p.scanned_hash AND previous.canonical))
    OR (b.number = p.scanned_number AND b.hash = p.scanned_hash));

-- name: PutENSSyncProjection :one
WITH existing AS (
    SELECT prior.* FROM ens_sync_projections prior
    WHERE prior.source_id = @source_id AND prior.kind = @kind AND prior.key = @key
    ORDER BY prior.revision DESC LIMIT 1
), changed AS (
    UPDATE ens_sync_progress p SET projection_revision = p.projection_revision + 1
    WHERE p.source_id = @source_id AND p.projection_revision = @expected_revision
      AND NOT EXISTS (SELECT 1 FROM ens_sync_projections same
          WHERE same.source_id = p.source_id AND same.kind = @kind AND same.key = @key
            AND same.deployment = @deployment AND same.block_hash = @block_hash AND same.payload = @payload::jsonb
            AND NOT EXISTS (SELECT 1 FROM ens_sync_projections later WHERE later.source_id = same.source_id
                AND later.kind = same.kind AND later.key = same.key AND later.revision > same.revision))
    RETURNING p.source_id, p.projection_revision
), inserted AS (
    INSERT INTO ens_sync_projections(source_id, revision, kind, key, deployment, block_hash, payload)
    SELECT source_id, projection_revision, @kind, @key, @deployment, @block_hash, @payload FROM changed
    RETURNING revision
)
SELECT revision FROM inserted
UNION ALL
SELECT revision FROM existing WHERE existing.deployment = @deployment AND existing.block_hash = @block_hash AND existing.payload = @payload::jsonb;

-- name: InvalidateENSSyncSource :one
UPDATE ens_sync_progress SET revision = revision + 1
WHERE source_id = @source_id AND revision = @expected_revision
RETURNING revision;

-- name: BindENSSyncJobDependency :execrows
WITH source_lock AS (
    SELECT p.source_id FROM ens_sync_progress p WHERE p.source_id = @source_id FOR UPDATE
), job AS (
    SELECT j.* FROM ens_sync_jobs j JOIN source_lock l ON l.source_id = j.source_id
    WHERE j.id = @job_id AND
      (j.state = 'queued' OR (j.state = 'leased' AND j.lease_token = @lease_token AND j.lease_until > clock_timestamp()))
    FOR UPDATE OF j
)
INSERT INTO ens_sync_job_dependencies(source_id, job_id, kind, key, revision)
SELECT j.source_id, j.id, @kind, @key, @revision FROM job j
WHERE (SELECT count(*) FROM ens_sync_job_dependencies dep WHERE dep.source_id = j.source_id AND dep.job_id = j.id) < 256
   OR EXISTS (SELECT 1 FROM ens_sync_job_dependencies dep WHERE dep.source_id = j.source_id AND dep.job_id = j.id AND dep.kind = @kind AND dep.key = @key)
ON CONFLICT (source_id, job_id, kind, key) DO UPDATE SET revision = EXCLUDED.revision;

-- name: EnsureENSSyncCandidate :one
WITH candidate AS (
    INSERT INTO ens_sync_candidates(source_id, direction, lookup_key, coin_type)
    VALUES (@source_id, @direction, @lookup_key, @coin_type)
    ON CONFLICT (source_id, direction, lookup_key, coin_type)
    DO UPDATE SET lookup_key = EXCLUDED.lookup_key
    RETURNING *
), origin AS (
    INSERT INTO ens_sync_candidate_origins(source_id, candidate_id, kind, identity)
    SELECT source_id, id, @origin_kind, @origin_identity FROM candidate
    ON CONFLICT DO NOTHING
)
SELECT * FROM candidate;

-- name: LockENSSyncCandidate :one
SELECT * FROM ens_sync_candidates WHERE source_id = @source_id AND id = @candidate_id FOR UPDATE;

-- name: InvalidateENSSyncCandidate :one
UPDATE ens_sync_candidates SET requested_generation = requested_generation + 1, minimum_generation = requested_generation + 1, refresh_at = NULL
WHERE source_id = @source_id AND id = @candidate_id AND requested_generation = @expected_generation
RETURNING requested_generation;

-- name: EnqueueENSSyncJob :execrows
INSERT INTO ens_sync_jobs(source_id, candidate_id, generation, source_revision, target_hash, core_canonical_at, max_attempts, deadline)
SELECT c.source_id, c.id, c.requested_generation, p.revision, p.target_hash, cb.updated_at, @max_attempts, @deadline
FROM ens_sync_candidates c JOIN ens_sync_progress p ON p.source_id = c.source_id
JOIN ens_sync_sources s ON s.id = c.source_id
LEFT JOIN canonical_blocks cb ON s.namespace = 'custom_ens' AND cb.chain_id = s.target_chain_id AND cb.block_hash = p.target_hash
WHERE c.source_id = @source_id AND c.id = @candidate_id AND p.target_hash IS NOT NULL
ON CONFLICT (source_id, candidate_id, generation) DO NOTHING;

-- name: ClaimENSSyncJob :one
WITH next AS (
    SELECT j.id FROM ens_sync_jobs j
    JOIN ens_sync_candidates c ON c.source_id = j.source_id AND c.id = j.candidate_id
    JOIN ens_sync_progress p ON p.source_id = j.source_id
    WHERE j.source_id = @source_id AND j.generation = c.requested_generation
      AND j.source_revision = p.revision AND p.state = 'active'
      AND j.target_hash = p.target_hash AND NOT EXISTS (
          SELECT 1 FROM ens_sync_job_dependencies dep
          JOIN ens_sync_projections fact ON fact.source_id = dep.source_id AND fact.kind = dep.kind AND fact.key = dep.key AND fact.revision = dep.revision
          JOIN ens_sync_blocks fact_block ON fact_block.source_id = fact.source_id AND fact_block.hash = fact.block_hash
          JOIN ens_sync_blocks target_block ON target_block.source_id = j.source_id AND target_block.hash = j.target_hash
          WHERE dep.source_id = j.source_id AND dep.job_id = j.id
            AND (NOT fact_block.canonical OR fact_block.number > target_block.number OR EXISTS (
                SELECT 1 FROM ens_sync_projections newer
                  JOIN ens_sync_blocks newer_block ON newer_block.source_id = newer.source_id AND newer_block.hash = newer.block_hash AND newer_block.canonical
                  WHERE newer.source_id = dep.source_id
                  AND newer.kind = dep.kind AND newer.key = dep.key AND newer.revision > dep.revision)))
      AND j.attempts < j.max_attempts AND j.deadline > clock_timestamp()
      AND ((j.state = 'queued' AND j.retry_at <= clock_timestamp())
        OR (j.state = 'leased' AND j.lease_until <= clock_timestamp()))
    ORDER BY j.retry_at, j.id LIMIT 1 FOR UPDATE OF j SKIP LOCKED
)
UPDATE ens_sync_jobs j SET state = 'leased', attempts = attempts + 1,
    lease_token = @lease_token, lease_until = LEAST(j.deadline, clock_timestamp() + sqlc.arg(lease_seconds)::integer * interval '1 second')
FROM next WHERE j.id = next.id AND sqlc.arg(lease_seconds)::integer BETWEEN 1 AND 300
RETURNING j.*;

-- name: RetryENSSyncJob :execrows
UPDATE ens_sync_jobs SET state = CASE WHEN attempts >= max_attempts OR @retry_at >= deadline THEN 'failed' ELSE 'queued' END,
    failure_code = @failure_code, retry_at = @retry_at, lease_token = NULL, lease_until = NULL
WHERE source_id = @source_id AND id = @job_id AND state = 'leased'
  AND lease_token = @lease_token AND lease_until > clock_timestamp();

-- name: ExpireENSSyncJobs :execrows
WITH expired AS (
    SELECT e.id FROM ens_sync_jobs e WHERE e.source_id = @source_id
      AND e.state IN ('queued', 'leased')
      AND (e.state <> 'leased' OR e.lease_until <= clock_timestamp())
      AND (e.attempts >= e.max_attempts OR e.deadline <= clock_timestamp())
    ORDER BY e.id LIMIT 100 FOR UPDATE SKIP LOCKED
)
UPDATE ens_sync_jobs j SET state = 'failed', failure_code = 'retry_exhausted', lease_token = NULL, lease_until = NULL
FROM expired WHERE j.id = expired.id;

-- name: PublishENSSyncObservation :one
WITH finished AS (
    UPDATE ens_sync_jobs j SET state = 'complete', lease_token = NULL, lease_until = NULL, failure_code = NULL
    FROM ens_sync_candidates c, ens_sync_progress p, ens_sync_sources s, ens_sync_blocks b
    WHERE j.id = @job_id AND j.source_id = @source_id AND j.candidate_id = @candidate_id
      AND c.source_id = j.source_id AND c.id = j.candidate_id
      AND p.source_id = j.source_id AND s.id = j.source_id
      AND b.source_id = j.source_id AND b.hash = j.target_hash AND b.canonical
      AND j.state = 'leased' AND j.lease_token = @lease_token AND j.lease_until > clock_timestamp()
      AND j.deadline > clock_timestamp() AND j.generation = @generation
      AND j.generation = c.requested_generation AND j.source_revision = p.revision
      AND NOT EXISTS (
          SELECT 1 FROM ens_sync_job_dependencies dep
          JOIN ens_sync_projections fact ON fact.source_id = dep.source_id AND fact.kind = dep.kind AND fact.key = dep.key AND fact.revision = dep.revision
          JOIN ens_sync_blocks fact_block ON fact_block.source_id = fact.source_id AND fact_block.hash = fact.block_hash
          JOIN ens_sync_blocks target_block ON target_block.source_id = j.source_id AND target_block.hash = j.target_hash
          WHERE dep.source_id = j.source_id AND dep.job_id = j.id
            AND (NOT fact_block.canonical OR fact_block.number > target_block.number OR EXISTS (
                SELECT 1 FROM ens_sync_projections newer
                  JOIN ens_sync_blocks newer_block ON newer_block.source_id = newer.source_id AND newer_block.hash = newer.block_hash AND newer_block.canonical
                  WHERE newer.source_id = dep.source_id
                  AND newer.kind = dep.kind AND newer.key = dep.key AND newer.revision > dep.revision)))
      AND (@evidence::text <> 'projection' OR EXISTS (SELECT 1 FROM ens_sync_job_dependencies dep WHERE dep.source_id = j.source_id AND dep.job_id = j.id))
      AND p.state = 'active' AND p.healthy_until > clock_timestamp()
      AND p.target_hash = j.target_hash AND p.scanned_hash = j.target_hash
      AND @observed_at::timestamptz <= clock_timestamp() AND @valid_until::timestamptz > clock_timestamp()
      AND @refresh_at::timestamptz > @observed_at::timestamptz AND @refresh_at::timestamptz < @valid_until::timestamptz
      AND @retain_until::timestamptz >= @valid_until::timestamptz
      AND EXISTS (SELECT 1 FROM ens_sync_deployments d JOIN ens_sync_blocks db
          ON db.source_id = d.source_id AND db.hash = d.block_hash
          WHERE d.source_id = j.source_id AND d.address = @deployment AND db.canonical)
      AND ((@outcome::text = 'resolved') OR
        (c.direction = 'forward' AND @address::bytea IS NULL) OR
        (c.direction = 'primary' AND sqlc.narg(name)::text IS NULL))
      AND ((c.direction = 'forward' AND sqlc.narg(name)::text = c.lookup_key AND @reverse_resolver::bytea IS NULL)
        OR (c.direction = 'primary' AND '0x' || encode(@address::bytea, 'hex') = c.lookup_key
            AND (@outcome::text = 'not_found' OR @reverse_resolver::bytea IS NOT NULL)))
      AND (s.namespace <> 'custom_ens' OR EXISTS (
          SELECT 1 FROM canonical_blocks cb JOIN chains core ON core.chain_id = cb.chain_id AND core.genesis_hash = s.genesis_hash WHERE cb.chain_id = s.target_chain_id AND cb.number = b.number AND cb.block_hash = b.hash AND cb.updated_at = j.core_canonical_at))
    RETURNING j.*
), observation AS (
    INSERT INTO ens_sync_observations(source_id, candidate_id, job_id, lease_token, generation, source_revision,
        block_hash, deployment, endpoint, evidence, outcome, name, address, resolver, reverse_resolver, observed_at, valid_until)
    SELECT source_id, candidate_id, id, @lease_token, generation, source_revision, target_hash, @deployment, @endpoint, @evidence,
        @outcome::text, sqlc.narg(name), @address, @resolver, @reverse_resolver, @observed_at, @valid_until FROM finished
    RETURNING *
), refreshed AS (
    UPDATE ens_sync_candidates c SET refresh_at = @refresh_at FROM observation o
    WHERE c.id = o.candidate_id
)
INSERT INTO ens_sync_publications(source_id, candidate_id, observation_id, retain_until)
SELECT source_id, candidate_id, id, @retain_until FROM observation
RETURNING id;

-- name: ReadENSSyncCandidate :one
SELECT c.id, c.requested_generation, c.minimum_generation, s.namespace, p.state AS source_state, p.revision,
    p.healthy_until, p.target_hash, p.scanned_hash,
    COALESCE(target.canonical AND (s.namespace <> 'custom_ens' OR EXISTS (
        SELECT 1 FROM canonical_blocks cb JOIN chains core ON core.chain_id = cb.chain_id AND core.genesis_hash = s.genesis_hash WHERE cb.chain_id = s.target_chain_id AND cb.number = target.number AND cb.block_hash = target.hash)), false)::boolean AS coverage_canonical,
    o.generation, o.source_revision, o.outcome, o.name, o.address, o.resolver, o.reverse_resolver,
    o.valid_until, o.block_hash, COALESCE(pub.id, 0)::bigint AS publication_id, pub.retain_until,
    COALESCE(b.canonical AND j.state = 'complete' AND j.target_hash = o.block_hash AND j.source_revision = o.source_revision AND NOT EXISTS (
          SELECT 1 FROM ens_sync_job_dependencies dep
          JOIN ens_sync_projections fact ON fact.source_id = dep.source_id AND fact.kind = dep.kind AND fact.key = dep.key AND fact.revision = dep.revision
          JOIN ens_sync_blocks fact_block ON fact_block.source_id = fact.source_id AND fact_block.hash = fact.block_hash
          JOIN ens_sync_blocks target_block ON target_block.source_id = j.source_id AND target_block.hash = j.target_hash
          WHERE dep.source_id = j.source_id AND dep.job_id = j.id
            AND (NOT fact_block.canonical OR fact_block.number > target_block.number OR EXISTS (
                SELECT 1 FROM ens_sync_projections newer
                  JOIN ens_sync_blocks newer_block ON newer_block.source_id = newer.source_id AND newer_block.hash = newer.block_hash AND newer_block.canonical
                  WHERE newer.source_id = dep.source_id
                  AND newer.kind = dep.kind AND newer.key = dep.key AND newer.revision > dep.revision))) AND (s.namespace <> 'custom_ens' OR EXISTS (
        SELECT 1 FROM canonical_blocks cb JOIN chains core ON core.chain_id = cb.chain_id AND core.genesis_hash = s.genesis_hash WHERE cb.chain_id = s.target_chain_id AND cb.number = b.number AND cb.block_hash = b.hash AND cb.updated_at = j.core_canonical_at)), false)::boolean AS canonical
FROM ens_sync_sources s
JOIN ens_sync_progress p ON p.source_id = s.id
LEFT JOIN ens_sync_blocks target ON target.source_id = s.id AND target.hash = p.target_hash
JOIN ens_sync_candidates c ON c.source_id = s.id
LEFT JOIN LATERAL (
    SELECT * FROM ens_sync_publications v WHERE v.source_id = c.source_id AND v.candidate_id = c.id
      AND (@publication_id::bigint = 0 OR v.id = @publication_id::bigint)
    ORDER BY v.id DESC LIMIT 1
) pub ON true
LEFT JOIN ens_sync_observations o ON o.id = pub.observation_id
LEFT JOIN ens_sync_blocks b ON b.source_id = o.source_id AND b.hash = o.block_hash
LEFT JOIN ens_sync_jobs j ON j.id = o.job_id AND j.generation = o.generation AND j.candidate_id = o.candidate_id AND j.source_id = o.source_id
WHERE s.id = @source_id AND s.target_chain_id = @target_chain_id AND s.policy_digest = @policy_digest
  AND c.direction = @direction AND c.lookup_key = @lookup_key AND c.coin_type = @coin_type;

-- name: ConfirmENSSyncPublication :one
SELECT p.id FROM ens_sync_publications p
JOIN ens_sync_observations o ON o.id = p.observation_id AND o.source_id = p.source_id AND o.candidate_id = p.candidate_id
WHERE o.source_id = @source_id AND o.job_id = @job_id AND o.generation = @generation AND o.lease_token = @lease_token;
