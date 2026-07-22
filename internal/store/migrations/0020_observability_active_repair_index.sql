-- P60-T05 polls only active control-plane state for current backlog gauges.
-- Queue age already uses repair_requests_pending_idx; this companion partial
-- index keeps queued/running grouping independent of terminal history size.
CREATE INDEX IF NOT EXISTS repair_requests_active_metrics_idx
    ON repair_requests (chain_id, operation, status)
    WHERE status IN ('queued', 'running');
