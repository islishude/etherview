-- Multiple sync replicas share one public runtime snapshot. A short writer
-- lease prevents a slow or failing peer from replacing a fresher observation,
-- while allowing an expired or failed writer to be replaced without operator
-- intervention. A safety halt is protected only for the active lease; this is
-- an HA writer election, not a permanent cluster safety latch. The public
-- snapshot remains one row per chain.
CREATE TABLE IF NOT EXISTS sync_runtime_status_writer_leases (
    chain_id NUMERIC(78, 0) PRIMARY KEY REFERENCES chains(chain_id),
    reporter_id TEXT NOT NULL,
    observed_latest_number NUMERIC(78, 0),
    observed_latest_known BOOLEAN NOT NULL,
    safety_halt BOOLEAN NOT NULL DEFAULT false,
    expires_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (length(reporter_id) BETWEEN 1 AND 128),
    CHECK (
        (observed_latest_known AND observed_latest_number IS NOT NULL AND observed_latest_number >= 0)
        OR
        (NOT observed_latest_known AND observed_latest_number IS NULL)
    )
);
