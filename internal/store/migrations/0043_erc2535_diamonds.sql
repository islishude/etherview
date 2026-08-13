-- ERC-2535 state is selector-scoped.  Keep the exact Loupe snapshot separate
-- from the generic proxy evidence document so no reader can accidentally
-- project a Diamond to one implementation address.
CREATE TABLE diamond_loupe_snapshots (
    id BIGSERIAL PRIMARY KEY,
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    diamond_address BYTEA NOT NULL,
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    stage_version INTEGER NOT NULL DEFAULT 2,
    detection_state TEXT NOT NULL,
    completeness TEXT NOT NULL,
    validation TEXT NOT NULL,
    standard_diamond_cut TEXT NOT NULL,
    standard_diamond_cut_facet BYTEA,
    loupe_interface_reported BOOLEAN,
    truncated BOOLEAN NOT NULL DEFAULT FALSE,
    truncation_reason TEXT,
    warnings JSONB NOT NULL DEFAULT '[]'::jsonb,
    canonical BOOLEAN NOT NULL,
    durable_job_id BIGINT REFERENCES durable_jobs(id),
    job_generation BIGINT,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE NULLS NOT DISTINCT (
        chain_id, diamond_address, block_hash, stage_version,
        durable_job_id, job_generation
    ),
    FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash),
    CHECK (octet_length(diamond_address) = 20),
    CHECK (octet_length(block_hash) = 32),
    CHECK (stage_version = 2),
    CHECK (detection_state IN ('confirmed', 'candidate', 'inconsistent')),
    CHECK (completeness IN ('complete', 'partial', 'unknown')),
    CHECK (validation IN ('full', 'sampled', 'interface-only')),
    CHECK (standard_diamond_cut IN ('present', 'absent', 'unknown')),
    CHECK (
        (standard_diamond_cut = 'present' AND
         standard_diamond_cut_facet IS NOT NULL AND
         octet_length(standard_diamond_cut_facet) = 20) OR
        (standard_diamond_cut <> 'present' AND
         standard_diamond_cut_facet IS NULL)
    ),
    CHECK (truncated = (truncation_reason IS NOT NULL)),
    CHECK (truncation_reason IS NULL OR truncation_reason <> ''),
    CHECK (jsonb_typeof(warnings) = 'array'),
    CHECK (
        (durable_job_id IS NULL AND job_generation IS NULL) OR
        (durable_job_id IS NOT NULL AND job_generation > 0)
    )
);

CREATE INDEX diamond_loupe_snapshots_current_idx
    ON diamond_loupe_snapshots (
        chain_id, diamond_address, block_number DESC, id DESC
    ) WHERE canonical AND stage_version = 2;

CREATE TRIGGER diamond_loupe_snapshots_source_guard
BEFORE INSERT ON diamond_loupe_snapshots
FOR EACH ROW EXECUTE FUNCTION enforce_proxy_detection_generation();

CREATE TABLE diamond_loupe_facets (
    snapshot_id BIGINT NOT NULL REFERENCES diamond_loupe_snapshots(id)
        ON DELETE CASCADE,
    facet_address BYTEA NOT NULL,
    facet_kind TEXT NOT NULL,
    code_exists BOOLEAN NOT NULL,
    code_hash BYTEA,
    PRIMARY KEY (snapshot_id, facet_address),
    CHECK (octet_length(facet_address) = 20),
    CHECK (facet_kind IN ('facet', 'immutable')),
    CHECK (code_hash IS NULL OR octet_length(code_hash) = 32),
    CHECK ((facet_kind = 'facet' AND code_exists = (code_hash IS NOT NULL))
        OR facet_kind = 'immutable')
);

CREATE TABLE diamond_loupe_selectors (
    snapshot_id BIGINT NOT NULL,
    selector BYTEA NOT NULL,
    facet_address BYTEA NOT NULL,
    PRIMARY KEY (snapshot_id, selector),
    FOREIGN KEY (snapshot_id, facet_address)
        REFERENCES diamond_loupe_facets(snapshot_id, facet_address)
        ON DELETE CASCADE,
    CHECK (octet_length(selector) = 4),
    CHECK (octet_length(facet_address) = 20)
);

CREATE INDEX diamond_loupe_selectors_facet_idx
    ON diamond_loupe_selectors (snapshot_id, facet_address, selector);

-- Raw, strictly decoded DiamondCut events remain block-hash keyed.  The
-- normalized selector changes are deliberately children of the raw record;
-- `_init` is event metadata and can never become a facet through this schema.
CREATE TABLE diamond_cut_events (
    chain_id NUMERIC(78, 0) NOT NULL REFERENCES chains(chain_id),
    block_number NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    transaction_hash BYTEA NOT NULL,
    transaction_index BIGINT NOT NULL,
    log_index BIGINT NOT NULL,
    diamond_address BYTEA NOT NULL,
    init_address BYTEA NOT NULL,
    init_calldata BYTEA NOT NULL,
    cuts JSONB NOT NULL,
    stage_version INTEGER NOT NULL DEFAULT 2,
    canonical BOOLEAN NOT NULL,
    PRIMARY KEY (chain_id, block_hash, log_index, stage_version),
    FOREIGN KEY (chain_id, block_number, block_hash)
        REFERENCES blocks(chain_id, number, hash),
    FOREIGN KEY (chain_id, block_number, block_hash, log_index)
        REFERENCES logs(chain_id, block_number, block_hash, log_index),
    CHECK (transaction_index >= 0),
    CHECK (log_index >= 0),
    CHECK (octet_length(block_hash) = 32),
    CHECK (octet_length(transaction_hash) = 32),
    CHECK (octet_length(diamond_address) = 20),
    CHECK (octet_length(init_address) = 20),
    CHECK (jsonb_typeof(cuts) = 'array'),
    CHECK (stage_version = 2)
);

CREATE INDEX diamond_cut_events_history_idx
    ON diamond_cut_events (
        chain_id, diamond_address, block_number,
        transaction_index, log_index
    ) WHERE canonical AND stage_version = 2;

CREATE TABLE diamond_selector_changes (
    chain_id NUMERIC(78, 0) NOT NULL,
    block_hash BYTEA NOT NULL,
    log_index BIGINT NOT NULL,
    stage_version INTEGER NOT NULL DEFAULT 2,
    cut_index INTEGER NOT NULL,
    selector_index INTEGER NOT NULL,
    selector BYTEA NOT NULL,
    action SMALLINT NOT NULL,
    facet_address BYTEA NOT NULL,
    PRIMARY KEY (
        chain_id, block_hash, log_index, stage_version,
        cut_index, selector_index
    ),
    FOREIGN KEY (chain_id, block_hash, log_index, stage_version)
        REFERENCES diamond_cut_events (
            chain_id, block_hash, log_index, stage_version
        ) ON DELETE CASCADE,
    CHECK (cut_index >= 0),
    CHECK (selector_index >= 0),
    CHECK (octet_length(selector) = 4),
    CHECK (action IN (0, 1, 2)),
    CHECK (octet_length(facet_address) = 20)
);

CREATE INDEX diamond_selector_changes_selector_idx
    ON diamond_selector_changes (chain_id, selector, block_hash, log_index);

-- A replayed proxy generation is public only with its exact publication
-- witness.  Direct processor fixtures (NULL generation identity) remain
-- inspectable in the base table but are not promoted by this view.
CREATE VIEW published_diamond_loupe_snapshots AS
SELECT snapshot.*
FROM diamond_loupe_snapshots AS snapshot
JOIN published_block_stage_results AS published
  ON published.chain_id = snapshot.chain_id
 AND published.block_number = snapshot.block_number
 AND published.block_hash = snapshot.block_hash
 AND published.stage = 'proxy'
 AND published.stage_version = snapshot.stage_version
 AND published.state = 'complete'
 AND published.durable_job_id = snapshot.durable_job_id
 AND published.job_generation = snapshot.job_generation
WHERE snapshot.canonical;

-- Intervals are derived from append-only canonical changes instead of being
-- mutated across blocks.  Reorg canonicality therefore switches the visible
-- timeline without erasing either branch.
CREATE VIEW canonical_diamond_selector_intervals AS
WITH ordered AS (
    SELECT event.chain_id,
           event.diamond_address,
           change.selector,
           change.facet_address,
           change.action,
           event.block_number AS valid_from_block_number,
           event.block_hash AS valid_from_block_hash,
           event.transaction_index AS valid_from_transaction_index,
           event.log_index AS valid_from_log_index,
           change.cut_index AS valid_from_cut_index,
           change.selector_index AS valid_from_selector_index,
           lead(event.block_number) OVER route AS valid_to_block_number,
           lead(event.block_hash) OVER route AS valid_to_block_hash,
           lead(event.transaction_index) OVER route AS valid_to_transaction_index,
           lead(event.log_index) OVER route AS valid_to_log_index,
           lead(change.cut_index) OVER route AS valid_to_cut_index,
           lead(change.selector_index) OVER route AS valid_to_selector_index
    FROM diamond_cut_events AS event
    JOIN diamond_selector_changes AS change
      ON change.chain_id = event.chain_id
     AND change.block_hash = event.block_hash
     AND change.log_index = event.log_index
     AND change.stage_version = event.stage_version
    JOIN canonical_blocks AS canonical
      ON canonical.chain_id = event.chain_id
     AND canonical.number = event.block_number
     AND canonical.block_hash = event.block_hash
    JOIN published_block_stage_results AS published
      ON published.chain_id = event.chain_id
     AND published.block_number = event.block_number
     AND published.block_hash = event.block_hash
     AND published.stage = 'proxy'
     AND published.stage_version = event.stage_version
     AND published.state = 'complete'
    WHERE event.canonical AND event.stage_version = 2
    WINDOW route AS (
        PARTITION BY event.chain_id, event.diamond_address, change.selector
        ORDER BY event.block_number, event.transaction_index, event.log_index,
                 change.cut_index, change.selector_index
    )
)
SELECT chain_id, diamond_address, selector, facet_address,
       valid_from_block_number, valid_from_block_hash,
       valid_from_transaction_index, valid_from_log_index,
       valid_from_cut_index, valid_from_selector_index,
       valid_to_block_number, valid_to_block_hash,
       valid_to_transaction_index, valid_to_log_index,
       valid_to_cut_index, valid_to_selector_index
FROM ordered
WHERE action <> 2;

-- A Diamond ABI binding is owned by the Diamond call target but sourced from
-- one exact facet code identity.  Include source identity in the key so
-- multiple active facets can contribute selector-filtered candidates.
ALTER TABLE contract_abis
    ADD COLUMN selector_scope BYTEA NOT NULL
        DEFAULT decode(repeat('00', 32), 'hex');

ALTER TABLE contract_abis
    DROP CONSTRAINT contract_abis_pkey,
    DROP CONSTRAINT contract_abis_source_v2_check,
    DROP CONSTRAINT contract_abis_confidence_v2_check,
    DROP CONSTRAINT contract_abis_source_identity_v2_check,
    ADD PRIMARY KEY (
        chain_id, address, code_hash, source,
        source_address, source_code_hash, selector_scope,
        valid_from_block, block_hash
    ),
    ADD CONSTRAINT contract_abis_source_v3_check
        CHECK (source IN (
            'verified', 'code_hash', 'proxy_implementation',
            'diamond_facet', 'signature_database'
        )),
    ADD CONSTRAINT contract_abis_confidence_v3_check
        CHECK (
            (source = 'verified' AND confidence = 'verified') OR
            (source IN (
                'code_hash', 'proxy_implementation', 'diamond_facet'
             ) AND confidence = 'high') OR
            (source = 'signature_database' AND confidence = 'guess')
        ),
    ADD CONSTRAINT contract_abis_source_identity_v3_check
        CHECK (
            source IN (
                'code_hash', 'proxy_implementation', 'diamond_facet'
            ) OR
            (source_address = address AND source_code_hash = code_hash)
        ),
    ADD CONSTRAINT contract_abis_selector_scope_v3_check
        CHECK (
            octet_length(selector_scope) = 32 AND
            ((source = 'diamond_facet' AND
              selector_scope <> decode(repeat('00', 32), 'hex')) OR
             (source <> 'diamond_facet' AND
              selector_scope = decode(repeat('00', 32), 'hex')))
        );

ALTER TABLE abi_decodings
    DROP CONSTRAINT abi_decodings_source_confidence_v2_check,
    ADD CONSTRAINT abi_decodings_source_confidence_v3_check
        CHECK (
            (source IS NULL AND confidence IS NULL) OR
            (source = 'verified' AND confidence = 'verified') OR
            (source IN (
                'code_hash', 'proxy_implementation', 'diamond_facet',
                'builtin'
             ) AND confidence = 'high') OR
            (source = 'signature_database' AND confidence = 'guess')
        );
