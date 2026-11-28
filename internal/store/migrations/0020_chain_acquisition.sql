-- A sorted concatenation of 20-byte addresses identifies one endpoint-pinned batch.
CREATE TABLE balance_acquisitions (
    chain_id numeric(78,0) NOT NULL REFERENCES chains(chain_id),
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL CHECK (octet_length(block_hash) = 32),
    addresses bytea NOT NULL CHECK (octet_length(addresses) BETWEEN 20 AND 5120 AND octet_length(addresses) % 20 = 0),
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    batch_digest bytea GENERATED ALWAYS AS (sha256(addresses)) STORED,
    generation bigint NOT NULL DEFAULT 0,
    available_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    UNIQUE (chain_id, block_hash, batch_digest)
);
CREATE INDEX balance_acquisitions_pending ON balance_acquisitions(chain_id, available_at, id);

-- A verified BAL-bearing header is required before the processor submits work.
CREATE TABLE bal_acquisitions (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    generation bigint NOT NULL,
    endpoint text NOT NULL DEFAULT '',
    attempts integer NOT NULL DEFAULT 0,
    lease_until timestamptz NOT NULL DEFAULT '-infinity',
    code text NOT NULL DEFAULT 'pending' CHECK (code IN ('pending','ready','unavailable','invalid')),
    PRIMARY KEY (chain_id,block_number,block_hash),
    FOREIGN KEY (chain_id,block_number,block_hash) REFERENCES blocks(chain_id,number,hash)
);
CREATE INDEX bal_acquisitions_pending ON bal_acquisitions(chain_id,lease_until,block_number) WHERE code='pending';

CREATE TABLE block_inputs (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    kind text NOT NULL CHECK (kind IN ('requests','consensus','blob','trace@4','state_diff@3','full_prestate@1')),
    version integer NOT NULL CHECK (version=1),
    generation bigint NOT NULL CHECK (generation>0),
    attempts integer NOT NULL DEFAULT 0,
    lease_until timestamptz NOT NULL DEFAULT '-infinity',
    error_code text NOT NULL DEFAULT '',
    evidence jsonb,
    PRIMARY KEY (chain_id,block_number,block_hash,kind,version),
    FOREIGN KEY (chain_id,block_number,block_hash) REFERENCES blocks(chain_id,number,hash)
);
CREATE INDEX block_inputs_pending ON block_inputs(chain_id,lease_until,block_number)
WHERE evidence IS NULL AND error_code='';

CREATE TABLE account_inputs (
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    operation text NOT NULL CHECK (operation IN ('summary@1','delegation@1','code@1','balanceOf@1','totalSupply@1','tokenBatch@1')),
    address bytea NOT NULL CHECK (octet_length(address)=20),
    argument bytea NOT NULL CHECK (CASE operation WHEN 'balanceOf@1' THEN octet_length(argument)=20 WHEN 'tokenBatch@1' THEN octet_length(argument) BETWEEN 53 AND 13568 AND octet_length(argument)%53=0 ELSE octet_length(argument)=0 END),
    argument_hash bytea GENERATED ALWAYS AS (sha256(argument)) STORED,
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    attempts integer NOT NULL DEFAULT 0,
    lease_until timestamptz NOT NULL DEFAULT '-infinity',
    expires_at timestamptz NOT NULL,
    evidence jsonb,
    UNIQUE(chain_id,block_hash,operation,address,argument_hash),
    FOREIGN KEY (chain_id,block_number,block_hash) REFERENCES blocks(chain_id,number,hash)
);
CREATE INDEX account_inputs_pending ON account_inputs(chain_id,lease_until,id) WHERE evidence IS NULL;
CREATE INDEX account_inputs_expiry ON account_inputs(chain_id,expires_at);

CREATE TABLE state_input_groups (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    chain_id numeric(78,0) NOT NULL,
    block_number numeric(78,0) NOT NULL,
    block_hash bytea NOT NULL,
    consumer text NOT NULL CHECK(length(consumer) BETWEEN 1 AND 240),
    endpoint text NOT NULL DEFAULT '',
    UNIQUE(chain_id,block_hash,consumer),
    FOREIGN KEY(chain_id,block_number,block_hash) REFERENCES blocks(chain_id,number,hash)
);
CREATE TABLE stage_state_inputs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    group_id bigint NOT NULL REFERENCES state_input_groups(id),
    request bytea NOT NULL CHECK(octet_length(request) BETWEEN 1 AND 263168),
    request_hash bytea GENERATED ALWAYS AS (sha256(request)) STORED,
    lease_generation bigint NOT NULL DEFAULT 0,
    attempts integer NOT NULL DEFAULT 0,
    lease_until timestamptz NOT NULL DEFAULT '-infinity',
    outcome text NOT NULL DEFAULT '' CHECK(outcome IN ('','ready','reverted','failed','exact_unavailable')),
    result bytea CHECK(octet_length(result)<=1048576),
    UNIQUE(group_id,request_hash)
);
CREATE INDEX stage_state_inputs_pending ON stage_state_inputs(lease_until,id) WHERE outcome='';

CREATE TABLE sync_capabilities (
    chain_id numeric(78,0) PRIMARY KEY REFERENCES chains(chain_id),
    historical_state text NOT NULL CHECK(historical_state IN ('pending','complete','unavailable')),
    trace text NOT NULL CHECK(trace IN ('pending','unavailable'))
);
