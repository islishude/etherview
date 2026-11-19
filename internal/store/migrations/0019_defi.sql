-- Current-chain DeFi publications. Historical and orphan observations are retained.
CREATE TABLE defi_sources (
 id text PRIMARY KEY,
 chain_id numeric(78,0) NOT NULL REFERENCES chains(chain_id),
 deployment text NOT NULL,
 protocol text NOT NULL CHECK (protocol IN ('uniswap-v2','uniswap-v3','uniswap-v4','aave-v3','curve','lido')),
 manifest_digest text NOT NULL,
 lease_token text,
 lease_until timestamptz,
 failure_code text NOT NULL DEFAULT '',
 failures integer NOT NULL DEFAULT 0 CHECK (failures >= 0),
 next_run_at timestamptz NOT NULL DEFAULT now(),
 CHECK ((lease_token IS NULL) = (lease_until IS NULL))
);
CREATE TABLE defi_runs (
 id text PRIMARY KEY,
 source_id text NOT NULL REFERENCES defi_sources(id),
 previous_run text REFERENCES defi_runs(id),
 block_number numeric(78,0) NOT NULL CHECK (block_number >= 0),
 block_hash bytea NOT NULL CHECK (octet_length(block_hash)=32),
 attachment timestamptz NOT NULL,
 coverage_start numeric(78,0) NOT NULL CHECK (coverage_start >= 0),
 state text NOT NULL CHECK (state IN ('seed_instruments','instruments','seed_candidates','candidates','observations','complete','invalid')),
 instrument_offset bigint NOT NULL DEFAULT 0 CHECK (instrument_offset >= 0),
 scan_number numeric(78,0) NOT NULL DEFAULT 0,
 scan_index bigint NOT NULL DEFAULT -1,
 after_candidate text NOT NULL DEFAULT '',
 failure_code text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 published_at timestamptz,
 UNIQUE (source_id,id)
);
CREATE UNIQUE INDEX defi_one_active_run ON defi_runs(source_id) WHERE state IN ('seed_instruments','instruments','seed_candidates','candidates','observations');
CREATE INDEX defi_published_runs ON defi_runs(source_id,block_number DESC) WHERE state='complete';
CREATE TABLE defi_instruments (
 run_id text NOT NULL REFERENCES defi_runs(id),
 address bytea NOT NULL CHECK (octet_length(address)=20),
 pool bytea NOT NULL CHECK (octet_length(pool)=20),
 kind text NOT NULL,
 PRIMARY KEY(run_id,address)
);
CREATE TABLE defi_candidates (
 run_id text NOT NULL REFERENCES defi_runs(id),
 key text NOT NULL,
 owner bytea NOT NULL CHECK (octet_length(owner)=20),
 candidate jsonb NOT NULL CHECK (jsonb_typeof(candidate)='object'),
 PRIMARY KEY(run_id,key)
);
CREATE INDEX defi_candidates_owner ON defi_candidates(run_id,owner,key);
CREATE TABLE defi_observations (
 run_id text NOT NULL,
 key text NOT NULL,
 owner bytea NOT NULL CHECK (octet_length(owner)=20),
 state text NOT NULL CHECK (state IN ('available','closed','pending','claimable','unsupported','unavailable')),
 position jsonb NOT NULL CHECK (jsonb_typeof(position)='object'),
 PRIMARY KEY(run_id,key),
 FOREIGN KEY(run_id,key) REFERENCES defi_candidates(run_id,key)
);
CREATE INDEX defi_observations_owner ON defi_observations(run_id,owner,key) WHERE state<>'closed';
CREATE FUNCTION etherview_defi_immutable() RETURNS trigger LANGUAGE plpgsql SET search_path TO 'pg_catalog' AS $$
BEGIN
 IF NEW IS DISTINCT FROM OLD THEN
  RAISE EXCEPTION 'DeFi observation is immutable' USING ERRCODE='integrity_constraint_violation';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER defi_observation_immutable BEFORE UPDATE ON defi_observations FOR EACH ROW EXECUTE FUNCTION etherview_defi_immutable();
