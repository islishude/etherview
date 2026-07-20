-- Core history coverage, rather than a queue status, is the durable source of
-- truth for deciding which backfill work remains. Ranges are inclusive and are
-- maintained sorted, disjoint, and non-adjacent by chain-locked transactions.
CREATE TABLE IF NOT EXISTS core_index_configuration (
    chain_id NUMERIC(78, 0) PRIMARY KEY REFERENCES chains(chain_id),
    configured_start NUMERIC(78, 0) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (configured_start >= 0)
);

CREATE TABLE IF NOT EXISTS core_coverage_ranges (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    range_start NUMERIC(78, 0) NOT NULL,
    range_end NUMERIC(78, 0) NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, range_start),
    CHECK (range_start >= 0),
    CHECK (range_end >= range_start)
);

CREATE UNIQUE INDEX IF NOT EXISTS core_coverage_ranges_end_uq
    ON core_coverage_ranges (chain_id, range_end);

CREATE TABLE IF NOT EXISTS core_backfill_leases (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    range_start NUMERIC(78, 0) NOT NULL,
    range_end NUMERIC(78, 0) NOT NULL,
    owner TEXT NOT NULL,
    lease_token UUID NOT NULL,
    claimed_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, range_start, range_end),
    UNIQUE (lease_token),
    CHECK (range_start >= 0),
    CHECK (range_end >= range_start),
    CHECK (range_end - range_start < 256),
    CHECK (length(owner) BETWEEN 1 AND 128),
    CHECK (expires_at > claimed_at)
);

CREATE INDEX IF NOT EXISTS core_backfill_leases_expiry_idx
    ON core_backfill_leases (chain_id, expires_at, range_start);

ALTER TABLE sync_runtime_status
    ADD COLUMN IF NOT EXISTS highest_covered_number NUMERIC(78, 0),
    ADD COLUMN IF NOT EXISTS backfill_complete BOOLEAN NOT NULL DEFAULT false;

-- The pre-coverage readiness bit was derived from a maximum canonical height
-- and cannot be trusted across gaps. Force a conservative status until the
-- sync role records coverage-derived fields after this migration.
UPDATE sync_runtime_status
SET ready = false,
    backfill_complete = false,
    highest_covered_number = NULL;

ALTER TABLE sync_runtime_status
    ADD CONSTRAINT sync_runtime_status_highest_covered_number_check
        CHECK (
            highest_covered_number IS NULL OR
            (highest_covered_number >= 0 AND
             (indexed_number IS NULL OR indexed_number <= highest_covered_number))
        ),
    ADD CONSTRAINT sync_runtime_status_backfill_complete_check
        CHECK (
            NOT backfill_complete OR
            (latest_number IS NOT NULL AND indexed_number IS NOT NULL AND
             highest_covered_number IS NOT NULL AND
             indexed_number >= latest_number AND
             indexed_number <= highest_covered_number)
        ),
    ADD CONSTRAINT sync_runtime_status_ready_requires_backfill_check
        CHECK (NOT ready OR backfill_complete);
