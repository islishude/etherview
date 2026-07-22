-- name: OperationalMetricSnapshot :many
WITH metric_rows AS (
    SELECT 'durable'::text AS metric_kind,
           (stage || '@' || stage_version::text)::text AS metric_name,
           status AS metric_status
    FROM durable_jobs
    WHERE chain_id = sqlc.arg(chain_id)::numeric
      AND status IN ('queued', 'leased')
    UNION ALL
    SELECT 'verification'::text, 'verification'::text, status
    FROM verification_jobs
    WHERE chain_id = sqlc.arg(chain_id)::numeric
      AND status IN ('queued', 'running')
    UNION ALL
    SELECT 'repair'::text, operation, status
    FROM repair_requests
    WHERE chain_id = sqlc.arg(chain_id)::numeric
      AND status IN ('queued', 'running')
), grouped AS (
    SELECT metric_kind, metric_name, metric_status,
           count(*)::bigint AS metric_count
    FROM metric_rows
    GROUP BY metric_kind, metric_name, metric_status
), oldest_repair AS (
    SELECT COALESCE(
               GREATEST(0, EXTRACT(EPOCH FROM (now() - min(requested_at)))),
               0
           )::double precision AS repair_oldest_seconds
    FROM repair_requests
    WHERE chain_id = sqlc.arg(chain_id)::numeric AND status = 'queued'
)
SELECT metric_kind::text, metric_name::text, metric_status::text, metric_count,
       NULL::double precision AS repair_oldest_seconds
FROM grouped
UNION ALL
SELECT 'snapshot'::text, ''::text, ''::text, 0::bigint,
       repair_oldest_seconds
FROM oldest_repair
ORDER BY metric_kind, metric_name, metric_status;
