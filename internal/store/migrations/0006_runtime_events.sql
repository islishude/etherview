-- PostgreSQL is the runtime-status and live-event source of truth. Every API
-- replica reads this ledger independently; rows are never claimed or deleted
-- by consumers.
CREATE TABLE IF NOT EXISTS sync_runtime_status (
    chain_id NUMERIC(78, 0) PRIMARY KEY REFERENCES chains(chain_id),
    latest_number NUMERIC(78, 0),
    indexed_number NUMERIC(78, 0),
    ready BOOLEAN NOT NULL DEFAULT false,
    last_poll_at TIMESTAMPTZ NOT NULL,
    last_error_code TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (latest_number IS NULL OR latest_number >= 0),
    CHECK (indexed_number IS NULL OR indexed_number >= 0),
    CHECK (NOT ready OR (latest_number IS NOT NULL AND indexed_number IS NOT NULL AND indexed_number >= latest_number)),
    CHECK (length(last_error_code) <= 64),
    CHECK (last_error_code ~ '^[a-z0-9_]*$')
);

CREATE TABLE IF NOT EXISTS runtime_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (event_type IN ('head', 'reorg', 'status')),
    CHECK (octet_length(payload::text) <= 8192)
);

CREATE INDEX IF NOT EXISTS runtime_events_chain_id_idx
    ON runtime_events (chain_id, id);
