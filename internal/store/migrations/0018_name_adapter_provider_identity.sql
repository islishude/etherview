-- P30-T06 isolates optional-adapter observations by a stable provider
-- namespace. The namespace is a non-secret identifier derived by the caller;
-- configured URLs and credentials are never persisted in this table.

ALTER TABLE external_adapter_observations
    ADD COLUMN IF NOT EXISTS provider_key TEXT;

UPDATE external_adapter_observations
SET provider_key = 'default'
WHERE provider_key IS NULL;

ALTER TABLE external_adapter_observations
    ALTER COLUMN provider_key SET DEFAULT 'default',
    ALTER COLUMN provider_key SET NOT NULL;

DO $migration$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'external_adapter_observations'::regclass
          AND conname = 'external_adapter_observations_provider_key_check'
    ) THEN
        ALTER TABLE external_adapter_observations
            ADD CONSTRAINT external_adapter_observations_provider_key_check
            CHECK (length(provider_key) BETWEEN 1 AND 128);
    END IF;
END
$migration$;

DROP INDEX IF EXISTS external_adapter_observations_latest_idx;
CREATE INDEX external_adapter_observations_latest_idx
    ON external_adapter_observations (
        chain_id, capability, provider_key, observation_key,
        observed_at DESC, id DESC
    );

COMMENT ON COLUMN external_adapter_observations.provider_key IS
    'Stable non-secret namespace for the configured adapter provider';
