CREATE TABLE derived_verification_backfill_requests (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    creator_address BYTEA,
    reason TEXT NOT NULL,
    scan_count INTEGER NOT NULL DEFAULT 0,
    requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT derived_verification_backfill_requests_shape_check CHECK (
        (creator_address IS NULL OR octet_length(creator_address) = 20) AND
        length(reason) BETWEEN 1 AND 512 AND scan_count >= 0
    )
);
