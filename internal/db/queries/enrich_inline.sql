-- name: EnrichInlineAuthenticateCloneCreationStatement1 :many
SELECT trace.input, trace.output
		FROM normalized_traces AS trace
		JOIN canonical_blocks AS canonical
		  ON canonical.chain_id = trace.chain_id
		 AND canonical.number = trace.block_number
		 AND canonical.block_hash = trace.block_hash
		WHERE trace.chain_id = $1::numeric
		  AND trace.created_address = $2
		  AND trace.call_type IN ('CREATE', 'CREATE2')
		  AND NOT trace.reverted
		  AND trace.canonical
		  AND EXISTS (
		      SELECT 1
		      FROM published_block_stage_results AS published
		      WHERE published.chain_id = trace.chain_id
		        AND published.block_hash = trace.block_hash
		        AND published.stage = $4
		        AND published.stage_version = $5
		        AND published.state = 'complete'
		  )
		  AND trace.block_number <= $3::numeric
		ORDER BY trace.block_number DESC, trace.transaction_index DESC,
		         trace.trace_path DESC
		LIMIT 1;

-- name: EnrichInlineDiamondHistoryCoverageCompleteStatement1 :many
WITH first_cut AS (
		    SELECT event.block_number, event.block_hash
		    FROM diamond_cut_events AS event
		    JOIN canonical_blocks AS canonical
		      ON canonical.chain_id = event.chain_id
		     AND canonical.number = event.block_number
		     AND canonical.block_hash = event.block_hash
		    WHERE event.chain_id = $1::numeric
		      AND event.diamond_address = $2
		      AND event.block_number <= $3::numeric
		      AND event.stage_version = $5
		      AND event.canonical
		      AND (
		          event.block_hash = $4 OR EXISTS (
		              SELECT 1
		              FROM published_block_stage_results AS published
		              WHERE published.chain_id = event.chain_id
		                AND published.block_number = event.block_number
		                AND published.block_hash = event.block_hash
		                AND published.stage = 'proxy'
		                AND published.stage_version = event.stage_version
		                AND published.state = 'complete'
		          )
		      )
		    ORDER BY event.block_number, event.transaction_index, event.log_index
		    LIMIT 1
		), created AS (
		    SELECT EXISTS (
		        SELECT 1
		        FROM receipts AS receipt
		        WHERE receipt.chain_id = $1::numeric
		          AND receipt.block_number = first_cut.block_number
		          AND receipt.block_hash = first_cut.block_hash
		          AND lower(receipt.raw->>'contractAddress') =
		              lower('0x' || encode($2, 'hex'))
		        UNION ALL
		        SELECT 1
		        FROM normalized_traces AS trace
		        JOIN published_block_stage_results AS published
		          ON published.chain_id = trace.chain_id
		         AND published.block_number = trace.block_number
		         AND published.block_hash = trace.block_hash
		         AND published.stage = 'trace'
		         AND published.stage_version = 2
		         AND published.state = 'complete'
		        WHERE trace.chain_id = $1::numeric
		          AND trace.block_number = first_cut.block_number
		          AND trace.block_hash = first_cut.block_hash
		          AND trace.created_address = $2
		          AND trace.call_type IN ('CREATE', 'CREATE2')
		          AND NOT trace.reverted
		          AND trace.canonical
		    ) AS at_first_cut
		    FROM first_cut
		)
		SELECT COALESCE(
		    created.at_first_cut AND proxy_interaction_coverage_contains(
		        $1::numeric, first_cut.block_number, first_cut.block_hash,
		        $3::numeric, $4
		    ), FALSE
		)
		FROM first_cut
		JOIN created ON TRUE;

-- name: EnrichInlineHasCanonicalCodeHistoryStatement1 :many
SELECT EXISTS (
		    SELECT 1
		    FROM contract_code_observations AS code
		    JOIN canonical_blocks AS canonical
		      ON canonical.chain_id = code.chain_id
		     AND canonical.number = code.block_number
		     AND canonical.block_hash = code.block_hash
		    WHERE code.chain_id = $1::numeric AND code.address = $2
		      AND code.block_number <= $3::numeric AND code.canonical
		);

-- name: EnrichInlineHasVerifiedDiamondLoupeABIStatement1 :many
SELECT EXISTS (
			SELECT 1
			FROM verified_contracts AS verified
			WHERE verified.chain_id = $1::numeric
			  AND verified.address = $2
			  AND verified.abi IS NOT NULL
			  AND verified.valid_from_block <= $3::numeric
			  AND (verified.valid_to_block IS NULL OR verified.valid_to_block >= $3::numeric)
			  AND verified.abi @> '[{"type":"function","name":"facets","inputs":[]}]'::jsonb
			  AND verified.abi @> '[{"type":"function","name":"facetAddresses","inputs":[]}]'::jsonb
			  AND verified.abi @> '[{"type":"function","name":"facetFunctionSelectors","inputs":[{"type":"address"}]}]'::jsonb
			  AND verified.abi @> '[{"type":"function","name":"facetAddress","inputs":[{"type":"bytes4"}]}]'::jsonb
		);

-- name: EnrichInlineLoadABIConstructorsStatement1 :many
SELECT DISTINCT ON (trace.transaction_hash, trace.trace_path)
		       trace.transaction_hash, trace.transaction_index, trace.trace_path, trace.created_address,
		       trace.input, verified.constructor_arguments, verified.abi,
		       verified.code_hash
		FROM normalized_traces AS trace
		JOIN contract_code_observations AS code
		  ON code.chain_id = trace.chain_id
		 AND code.block_number = trace.block_number
		 AND code.block_hash = trace.block_hash
		 AND code.address = trace.created_address
		 AND code.canonical
		JOIN verified_contracts AS verified
		  ON verified.chain_id = trace.chain_id
		 AND verified.address = trace.created_address
		 AND verified.code_hash = code.code_hash
		 AND verified.valid_from_block <= trace.block_number
		 AND (verified.valid_to_block IS NULL OR verified.valid_to_block >= trace.block_number)
		 AND verified.abi IS NOT NULL
		JOIN verification_results AS result
		  ON result.job_id = verified.verification_job_id
		 AND result.request_digest = verified.request_digest
		 AND result.outcome_kind = 'verification_success'
		 AND result.outcome->'creation_match'->>'match_type' = 'full'
		WHERE trace.chain_id = $1::numeric
		  AND trace.block_number = $2::numeric
		  AND trace.block_hash = $3
		  AND trace.call_type IN ('CREATE', 'CREATE2')
		  AND trace.created_address IS NOT NULL
		  AND NOT trace.reverted
		  AND trace.canonical
		ORDER BY trace.transaction_hash, trace.trace_path, verified.valid_from_block DESC;

-- name: EnrichInlineLoadABILogsStatement1 :many
SELECT log.log_index, log.tx_hash, log.address, log.raw,
		       attribution.execution_address
		FROM logs AS log
		LEFT JOIN trace_log_attributions AS attribution
		  ON attribution.chain_id = log.chain_id
		 AND attribution.block_number = log.block_number
		 AND attribution.block_hash = log.block_hash
		 AND attribution.transaction_hash = log.tx_hash
		 AND attribution.log_index = log.log_index
		 AND attribution.canonical
		 AND EXISTS (
		     SELECT 1
		     FROM published_block_stage_results AS published
		     WHERE published.chain_id = attribution.chain_id
		       AND published.block_hash = attribution.block_hash
		       AND published.stage = $4
		       AND published.stage_version = $5
		       AND published.state = 'complete'
		 )
		WHERE log.chain_id = $1::numeric AND log.block_number = $2::numeric AND log.block_hash = $3
		ORDER BY log.log_index;

-- name: EnrichInlineLoadABITracesStatement1 :many
SELECT transaction_hash, transaction_index, trace_path, execution_address,
		       execution_code_hash, input, output, direct_reverted
		FROM normalized_traces AS trace
		WHERE trace.chain_id = $1::numeric
		  AND trace.block_number = $2::numeric
		  AND trace.block_hash = $3
		  AND trace.canonical
		  AND trace.execution_address IS NOT NULL
		  AND trace.execution_resolution IN ('direct', 'eip7702_delegate', 'unavailable')
		  AND EXISTS (
		      SELECT 1
		      FROM published_block_stage_results AS published
		      WHERE published.chain_id = trace.chain_id
		        AND published.block_hash = trace.block_hash
		        AND published.stage = $4
		        AND published.stage_version = $5
		        AND published.state = 'complete'
		  )
		ORDER BY transaction_index, trace_path;

-- name: EnrichInlineLoadAndReplayDiamondHistoryStatement1 :many
SELECT change.selector, change.action, change.facet_address
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
		WHERE event.chain_id = $1::numeric
		  AND event.diamond_address = $2
		  AND event.block_number <= $3::numeric
		  AND event.canonical
		  AND event.stage_version = $5
		  AND (
		      event.block_hash = $4 OR EXISTS (
		          SELECT 1
		          FROM published_block_stage_results AS published
		          WHERE published.chain_id = event.chain_id
		            AND published.block_number = event.block_number
		            AND published.block_hash = event.block_hash
		            AND published.stage = 'proxy'
		            AND published.stage_version = event.stage_version
		            AND published.state = 'complete'
		      )
		  )
		ORDER BY event.block_number, event.transaction_index, event.log_index,
		         change.cut_index, change.selector_index
		LIMIT $6;

-- name: EnrichInlineLoadDiamondAuxiliaryABIBindingsStatement1 :many
WITH selected_snapshots AS (
		    (SELECT snapshot.id
		     FROM published_diamond_loupe_snapshots AS snapshot
		     JOIN canonical_blocks AS canonical
		       ON canonical.chain_id = snapshot.chain_id
		      AND canonical.number = snapshot.block_number
		      AND canonical.block_hash = snapshot.block_hash
		     WHERE snapshot.chain_id = $1::numeric
		       AND snapshot.diamond_address = $2
		       AND snapshot.block_number <= $3::numeric
		       AND snapshot.detection_state = 'confirmed'
		       AND snapshot.completeness = 'complete'
		       AND snapshot.canonical
		     ORDER BY snapshot.block_number DESC, snapshot.id DESC
		     LIMIT 1)
		    UNION
		    (SELECT snapshot.id
		     FROM published_diamond_loupe_snapshots AS snapshot
		     JOIN canonical_blocks AS canonical
		       ON canonical.chain_id = snapshot.chain_id
		      AND canonical.number = snapshot.block_number
		      AND canonical.block_hash = snapshot.block_hash
		     WHERE snapshot.chain_id = $1::numeric
		       AND snapshot.diamond_address = $2
		       AND snapshot.block_number < $3::numeric
		       AND snapshot.detection_state = 'confirmed'
		       AND snapshot.completeness = 'complete'
		       AND snapshot.canonical
		     ORDER BY snapshot.block_number DESC, snapshot.id DESC
		     LIMIT 1)
		), candidates AS (
		    SELECT facet.facet_address
		    FROM diamond_loupe_facets AS facet
		    WHERE facet.snapshot_id IN (SELECT id FROM selected_snapshots)
		      AND facet.facet_kind = 'facet'
		      AND facet.code_exists
		      AND facet.code_hash IS NOT NULL
		    UNION
		    SELECT change.facet_address
		    FROM diamond_cut_events AS event
		    JOIN diamond_selector_changes AS change
		      ON change.chain_id = event.chain_id
		     AND change.block_hash = event.block_hash
		     AND change.log_index = event.log_index
		     AND change.stage_version = event.stage_version
		    WHERE event.chain_id = $1::numeric
		      AND event.block_hash = $4
		      AND event.diamond_address = $2
		      AND event.canonical
		      AND event.stage_version = $5
		      AND change.action IN (0, 1)
		      AND change.facet_address <> decode(repeat('00', 20), 'hex')
		)
		SELECT facet_address
		FROM candidates
		ORDER BY facet_address
		LIMIT $6;

-- name: EnrichInlineLoadDiamondFacetCodeHashStatement1 :many
SELECT facet.code_hash
		FROM published_diamond_loupe_snapshots AS snapshot
		JOIN canonical_blocks AS canonical
		  ON canonical.chain_id = snapshot.chain_id
		 AND canonical.number = snapshot.block_number
		 AND canonical.block_hash = snapshot.block_hash
		JOIN diamond_loupe_facets AS facet
		  ON facet.snapshot_id = snapshot.id
		WHERE snapshot.chain_id = $1::numeric
		  AND snapshot.diamond_address = $2
		  AND snapshot.block_number <= $3::numeric
		  AND snapshot.detection_state = 'confirmed'
		  AND snapshot.canonical
		  AND facet.facet_address = $4
		  AND facet.facet_kind = 'facet'
		  AND facet.code_exists
		  AND facet.code_hash IS NOT NULL
		ORDER BY snapshot.block_number DESC, snapshot.id DESC
		LIMIT 1;

-- name: EnrichInlineLoadEffectiveTransactionExecutionsStatement1 :many
SELECT inclusion.tx_hash, inclusion.tx_index, inclusion.raw,
		       resolution.context_address, resolution.execution_address,
		       resolution.execution_code_hash, resolution.resolution,
		       resolution.evidence_source,
		       root.to_address, root.execution_address,
		       root.execution_code_hash, root.execution_resolution, root.input
		FROM transaction_inclusions AS inclusion
		LEFT JOIN transaction_execution_code_resolutions AS resolution
		  ON resolution.chain_id = inclusion.chain_id
		 AND resolution.block_number = inclusion.block_number
		 AND resolution.block_hash = inclusion.block_hash
		 AND resolution.transaction_hash = inclusion.tx_hash
		 AND resolution.transaction_index = inclusion.tx_index
		 AND resolution.context_address =
		     decode(substring(inclusion.raw->>'to' from 3), 'hex')
		 AND resolution.canonical
		 AND EXISTS (
		     SELECT 1 FROM published_block_stage_results AS published
		     WHERE published.chain_id = resolution.chain_id
		       AND published.block_number = resolution.block_number
		       AND published.block_hash = resolution.block_hash
		       AND published.stage = $4
		       AND published.stage_version = $5
		       AND published.state = 'complete'
		 )
		LEFT JOIN normalized_traces AS root
		  ON root.chain_id = inclusion.chain_id
		 AND root.block_number = inclusion.block_number
		 AND root.block_hash = inclusion.block_hash
		 AND root.transaction_hash = inclusion.tx_hash
		 AND root.transaction_index = inclusion.tx_index
		 AND root.trace_path = ''
		 AND root.canonical
		 AND EXISTS (
		     SELECT 1 FROM published_block_stage_results AS published
		     WHERE published.chain_id = root.chain_id
		       AND published.block_number = root.block_number
		       AND published.block_hash = root.block_hash
		       AND published.stage = $6
		       AND published.stage_version = $7
		       AND published.state = 'complete'
		 )
		WHERE inclusion.chain_id = $1::numeric
		  AND inclusion.block_number = $2::numeric
		  AND inclusion.block_hash = $3
		ORDER BY inclusion.tx_index;

-- name: EnrichInlineLoadGenesisCandidatesStatement1 :many
SELECT account.address
		FROM genesis_account_observations AS account
		JOIN genesis_state_imports AS imported
		  ON imported.chain_id = account.chain_id
		 AND imported.block_hash = account.block_hash
		 AND imported.state = 'complete'
		WHERE account.chain_id = $1::numeric
		  AND account.block_hash = $2
		  AND octet_length(account.code) > 0
		ORDER BY account.address;

-- name: EnrichInlineLoadLogCandidatesStatement1 :many
SELECT log_index, tx_hash, address, topic0, raw
		FROM logs
		WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3
		ORDER BY log_index;

-- name: EnrichInlineLoadProxyABIBindingStatement1 :many
WITH published_proxy_candidates AS (
		    SELECT observation.*, generation.id AS observation_generation_id,
		           generation.durable_job_id, generation.job_generation
		    FROM proxy_observations AS observation
		    JOIN canonical_blocks AS canonical
		      ON canonical.chain_id = observation.chain_id
		     AND canonical.number = observation.block_number
		     AND canonical.block_hash = observation.block_hash
		    JOIN proxy_observation_generations AS generation
		      ON generation.chain_id = observation.chain_id
		     AND generation.proxy_address = observation.proxy_address
		     AND generation.observation_block_hash = observation.block_hash
		     AND generation.observation_stage_version = observation.stage_version
		    JOIN published_block_stage_results AS published
		      ON published.chain_id = generation.chain_id
		     AND published.block_hash = generation.observation_block_hash
		     AND published.stage = 'proxy'
		     AND published.stage_version = generation.observation_stage_version
		     AND published.durable_job_id = generation.durable_job_id
		     AND published.job_generation = generation.job_generation
		     AND published.state = 'complete'
		    WHERE observation.chain_id = $1::numeric
			      AND observation.proxy_address = $2::bytea
			      AND observation.proxy_code_hash = $3::bytea
		      AND observation.stage_version = $6
		      AND observation.canonical
		      AND observation.confidence IN ('verified', 'high')
		      AND observation.block_number <= $4::numeric
		), resolved_candidates AS (
		    SELECT raw.*, resolution.id AS artifact_resolution_id,
		           resolution.proxy_kind AS resolved_kind,
		           resolution.proxy_pattern AS resolved_pattern,
		           resolution.implementation_address AS resolved_implementation,
		           resolution.implementation_code_hash AS resolved_implementation_hash,
		           resolution.beacon_address AS resolved_beacon,
		           resolution.beacon_code_hash AS resolved_beacon_hash
		    FROM published_proxy_candidates AS raw
		    LEFT JOIN LATERAL (
		        SELECT candidate.*
		        FROM proxy_artifact_resolutions AS candidate
		        JOIN published_block_stage_results AS published
		          ON published.chain_id = candidate.chain_id
		         AND published.block_hash = candidate.observation_block_hash
		         AND published.stage = 'proxy'
		         AND published.stage_version = candidate.observation_stage_version
		         AND published.durable_job_id = candidate.durable_job_id
		         AND published.job_generation = candidate.job_generation
		         AND published.state = 'complete'
		        WHERE candidate.chain_id = raw.chain_id
		          AND candidate.proxy_address = raw.proxy_address
		          AND candidate.observation_block_hash = raw.block_hash
		          AND candidate.observation_stage_version = raw.stage_version
		          AND candidate.proxy_code_hash = raw.proxy_code_hash
		          AND candidate.durable_job_id = raw.durable_job_id
		          AND candidate.job_generation = raw.job_generation
		        ORDER BY candidate.id DESC
		        LIMIT 1
		    ) AS resolution ON raw.proxy_pattern <> 'clone'
		    WHERE (
		        raw.proxy_pattern = 'clone'
		        AND raw.evidence_state = 'exact'
		        AND raw.implementation_address IS NOT NULL
		        AND raw.implementation_code_hash IS NOT NULL
		    ) OR (
		        resolution.id IS NOT NULL
		        AND (
		            resolution.proxy_pattern = 'beacon'
			            OR (raw.block_number = $4::numeric AND raw.block_hash = $5::bytea)
		        )
		    ) OR (
		        resolution.id IS NULL
		        AND raw.proxy_kind = 'eip1967'
		        AND raw.proxy_pattern = 'unknown'
		        AND raw.evidence_state = 'generic'
		        AND raw.beacon_address IS NULL
		        AND raw.beacon_code_hash IS NULL
		        AND raw.block_number = $4::numeric
			        AND raw.block_hash = $5::bytea
		        AND raw.implementation_address IS NOT NULL
		        AND raw.implementation_code_hash IS NOT NULL
		    )
		), selected_proxy AS (
		    SELECT candidate.*,
		           CASE WHEN candidate.artifact_resolution_id IS NULL
		                THEN candidate.proxy_pattern
		                ELSE candidate.resolved_pattern END AS effective_pattern,
		           CASE WHEN candidate.artifact_resolution_id IS NULL
		                THEN candidate.implementation_address
		                ELSE candidate.resolved_implementation END AS effective_implementation,
		           CASE WHEN candidate.artifact_resolution_id IS NULL
		                THEN candidate.implementation_code_hash
		                ELSE candidate.resolved_implementation_hash END AS effective_implementation_hash
		    FROM resolved_candidates AS candidate
		    ORDER BY (candidate.artifact_resolution_id IS NOT NULL) DESC,
		             candidate.block_number DESC,
		             candidate.observation_generation_id DESC
		    LIMIT 1
		), published_beacon AS (
		    SELECT observation.implementation_address,
		           observation.implementation_code_hash
		    FROM selected_proxy AS proxy
		    JOIN beacon_implementation_observations AS observation
		      ON observation.chain_id = proxy.chain_id
		     AND observation.beacon_address = proxy.resolved_beacon
		     AND observation.beacon_code_hash = proxy.resolved_beacon_hash
		    JOIN canonical_blocks AS canonical
		      ON canonical.chain_id = observation.chain_id
		     AND canonical.number = observation.block_number
		     AND canonical.block_hash = observation.block_hash
		    JOIN beacon_observation_generations AS generation
		      ON generation.chain_id = observation.chain_id
		     AND generation.beacon_address = observation.beacon_address
		     AND generation.observation_block_hash = observation.block_hash
		     AND generation.observation_stage_version = observation.stage_version
		    JOIN published_block_stage_results AS published
		      ON published.chain_id = generation.chain_id
		     AND published.block_hash = generation.observation_block_hash
		     AND published.stage = 'proxy'
		     AND published.stage_version = generation.observation_stage_version
		     AND published.durable_job_id = generation.durable_job_id
		     AND published.job_generation = generation.job_generation
		     AND published.state = 'complete'
		    WHERE proxy.effective_pattern = 'beacon'
		      AND observation.stage_version = $6
		      AND observation.canonical
		      AND observation.confidence IN ('verified', 'high')
		      AND observation.block_number <= $4::numeric
		    ORDER BY observation.block_number DESC, generation.id DESC
		    LIMIT 1
		)
		SELECT CASE WHEN proxy.effective_pattern = 'beacon'
		                   THEN beacon.implementation_address
		                   ELSE proxy.effective_implementation END,
		       CASE WHEN proxy.effective_pattern = 'beacon'
		                   THEN beacon.implementation_code_hash
		                   ELSE proxy.effective_implementation_hash END,
		       (proxy.proxy_kind = 'cwia'
		        AND proxy.effective_pattern = 'clone'
		        AND proxy.evidence_state = 'exact')::boolean
		FROM selected_proxy AS proxy
		LEFT JOIN published_beacon AS beacon
		  ON proxy.effective_pattern = 'beacon'
		WHERE proxy.effective_pattern <> 'beacon'
		   OR beacon.implementation_address IS NOT NULL;

-- name: EnrichInlineLoadProxyArtifactStatement1 :many
SELECT artifact.artifact_kind, artifact.standard_version,
		       artifact.runtime_immutable_address,
		       artifact.verification_job_id::text
		FROM verified_contract_proxy_artifacts AS artifact
		JOIN verified_contracts AS verified
		  ON verified.chain_id = artifact.chain_id
		 AND verified.address = artifact.address
		 AND verified.code_hash = artifact.code_hash
		 AND verified.valid_from_block = artifact.valid_from_block
		 AND verified.verification_job_id = artifact.verification_job_id
		 AND verified.request_digest = artifact.request_digest
		WHERE artifact.chain_id = $1::numeric
		  AND artifact.address = $2
		  AND artifact.code_hash = $3
		  AND artifact.valid_from_block <= $4::numeric
		  AND (verified.valid_to_block IS NULL OR verified.valid_to_block >= $4::numeric)
		ORDER BY artifact.valid_from_block DESC, artifact.verification_job_id
		LIMIT 1;

-- name: EnrichInlineLoadProxyCoverageDetailsStatement1 :many
SELECT stage, stage_version, state, durable_job_id, job_generation
		FROM published_block_stage_results
		WHERE chain_id = $1::numeric
		  AND block_hash = $2
		  AND ((stage = $3 AND stage_version = $4) OR
		       (stage = $5 AND stage_version = $6))
		ORDER BY stage, stage_version;

-- name: EnrichInlineLoadReceiptCandidatesStatement1 :many
SELECT tx_index, tx_hash, raw
		FROM receipts
		WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3
		ORDER BY tx_index;

-- name: EnrichInlineLoadSameCodeABIBindingStatement1 :many
SELECT address, abi
		FROM verified_contracts
		WHERE chain_id = $1::numeric
		  AND code_hash = $2
		  AND abi IS NOT NULL
		  AND (address <> $3 OR valid_from_block > $4::numeric OR
		       (valid_to_block IS NOT NULL AND valid_to_block < $4::numeric))
		ORDER BY (match_type = 'full') DESC, (address = $3) DESC, created_at DESC,
		         request_digest ASC, verification_job_id ASC, address
		LIMIT 1;

-- name: EnrichInlineLoadSignatureABIBindingStatement1 :many
SELECT signature, abi_entry
			FROM abi_signature_candidates
			WHERE kind = $1 AND identifier = $2
			  AND octet_length(signature) <= $3
			  AND octet_length(abi_entry::text) <= $4
			ORDER BY signature
			LIMIT $5;

-- name: EnrichInlineLoadStateDiffCandidatesStatement1 :many
SELECT DISTINCT address
		FROM transaction_state_changes AS change
		WHERE change.chain_id = $1::numeric
		  AND change.block_number = $2::numeric
		  AND change.block_hash = $3
		  AND change.canonical
		  AND EXISTS (
		      SELECT 1
		      FROM published_block_stage_results AS published
		      WHERE published.chain_id = change.chain_id
		        AND published.block_hash = change.block_hash
		        AND published.stage = $7
		        AND published.stage_version = $8
		        AND published.state = 'complete'
		  )
		  AND (
		      change.field_kind = 'code'
		      OR (change.field_kind = 'storage' AND change.storage_key IN ($4, $5, $6))
		  )
		ORDER BY change.address;

-- name: EnrichInlineLoadTraceCandidatesStatement1 :many
SELECT call_type, from_address, to_address, created_address, reverted
		FROM normalized_traces AS trace
		WHERE trace.chain_id = $1::numeric
		  AND trace.block_number = $2::numeric
		  AND trace.block_hash = $3
		  AND trace.canonical
		  AND EXISTS (
		      SELECT 1
		      FROM published_block_stage_results AS published
		      WHERE published.chain_id = trace.chain_id
		        AND published.block_hash = trace.block_hash
		        AND published.stage = $4
		        AND published.stage_version = $5
		        AND published.state = 'complete'
		  )
		ORDER BY transaction_index, trace_path;

-- name: EnrichInlineLoadTransactionCandidatesStatement1 :many
SELECT tx_hash, raw
		FROM transaction_inclusions
		WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3
		ORDER BY tx_index;

-- name: EnrichInlineLoadVerifiedABIBindingStatement1 :many
SELECT abi, valid_from_block::text, valid_to_block::text
		FROM verified_contracts
		WHERE chain_id = $1::numeric AND address = $2 AND code_hash = $3
		  AND abi IS NOT NULL
		  AND valid_from_block <= $4::numeric
		  AND (valid_to_block IS NULL OR valid_to_block >= $4::numeric)
		ORDER BY (match_type = 'full') DESC, valid_from_block DESC,
		         request_digest ASC, verification_job_id ASC
		LIMIT 1;

-- name: EnrichInlinePersistABIBindingStatement1 :exec
INSERT INTO contract_abis (
			chain_id, address, code_hash, source, confidence, abi,
			valid_from_block, valid_to_block, block_number, block_hash,
			source_address, source_code_hash, selector_scope, canonical
		) VALUES (
			$1::numeric, $2, $3, $4, $5, $6::jsonb,
			$7::numeric, $8::numeric, $9::numeric, $10, $11, $12, $13, TRUE
		)
		ON CONFLICT (
			chain_id, address, code_hash, source, source_address,
			source_code_hash, selector_scope, valid_from_block, block_hash
		)
		DO UPDATE SET
			confidence = EXCLUDED.confidence,
			abi = EXCLUDED.abi,
			valid_to_block = EXCLUDED.valid_to_block,
			block_number = EXCLUDED.block_number,
			source_address = EXCLUDED.source_address,
			source_code_hash = EXCLUDED.source_code_hash,
			canonical = TRUE;

-- name: EnrichInlinePersistABIDecodingStatement1 :exec
INSERT INTO abi_decodings (
			chain_id, block_number, block_hash, object_kind, transaction_hash,
			object_index, target_address, target_code_hash, abi_kind, status,
			signature, source, confidence, source_address, source_code_hash,
			arguments, candidates, warning, return_status, return_arguments,
			decoding_kind, canonical
		) VALUES (
			$1::numeric, $2::numeric, $3, $4, $5, $6, $7, $8, $9, $10,
			$11, $12, $13, $14, $15, $16::jsonb, $17::jsonb, $18,
			$19, $20::jsonb, $21, TRUE
		);

-- name: EnrichInlinePersistDiamondCutRecordStatement1 :exec
INSERT INTO diamond_cut_events AS current (
		    chain_id, block_number, block_hash, transaction_hash,
		    transaction_index, log_index, diamond_address, init_address,
		    init_calldata, cuts, stage_version, canonical
		)
		SELECT $1::numeric, $2::numeric, $3, $4, $5::bigint, $6::bigint,
		       $7, $8, $9, $10::jsonb, $11,
		       EXISTS (
		           SELECT 1 FROM canonical_blocks
		           WHERE chain_id = $1::numeric AND number = $2::numeric
		             AND block_hash = $3
		       )
		ON CONFLICT (chain_id, block_hash, log_index, stage_version)
		DO UPDATE SET canonical = EXCLUDED.canonical
		WHERE current.block_number = EXCLUDED.block_number
		  AND current.transaction_hash = EXCLUDED.transaction_hash
		  AND current.transaction_index = EXCLUDED.transaction_index
		  AND current.diamond_address = EXCLUDED.diamond_address
		  AND current.init_address = EXCLUDED.init_address
		  AND current.init_calldata = EXCLUDED.init_calldata
		  AND current.cuts = EXCLUDED.cuts;

-- name: EnrichInlinePersistDiamondCutRecordStatement2 :exec
INSERT INTO diamond_selector_changes AS current (
				    chain_id, block_hash, log_index, stage_version,
				    cut_index, selector_index, selector, action, facet_address
				) VALUES (
				    $1::numeric, $2, $3::bigint, $4, $5, $6, $7, $8, $9
				)
				ON CONFLICT (
				    chain_id, block_hash, log_index, stage_version,
				    cut_index, selector_index
				) DO UPDATE SET selector = EXCLUDED.selector
				WHERE current.selector = EXCLUDED.selector
				  AND current.action = EXCLUDED.action
				  AND current.facet_address = EXCLUDED.facet_address;

-- name: EnrichInlinePersistDiamondDetectionSnapshotStatement1 :many
INSERT INTO diamond_loupe_snapshots AS current (
		    chain_id, diamond_address, block_number, block_hash, stage_version,
		    detection_state, completeness, validation, standard_diamond_cut,
		    standard_diamond_cut_facet, loupe_interface_reported, truncated,
		    truncation_reason, warnings, canonical, durable_job_id, job_generation
		)
		SELECT $1::numeric, $2, $3::numeric, $4, $5, $6, $7, $8, $9,
		       $10, $11, $12, $13, $14::jsonb,
		       EXISTS (
		           SELECT 1 FROM canonical_blocks
		           WHERE chain_id = $1::numeric AND number = $3::numeric
		             AND block_hash = $4
		       ),
		       $15, $16::bigint
		ON CONFLICT (
		    chain_id, diamond_address, block_hash, stage_version,
		    durable_job_id, job_generation
		) DO UPDATE SET canonical = EXCLUDED.canonical
		WHERE current.block_number = EXCLUDED.block_number
		  AND current.detection_state = EXCLUDED.detection_state
		  AND current.completeness = EXCLUDED.completeness
		  AND current.validation = EXCLUDED.validation
		  AND current.standard_diamond_cut = EXCLUDED.standard_diamond_cut
		  AND current.standard_diamond_cut_facet IS NOT DISTINCT FROM
		      EXCLUDED.standard_diamond_cut_facet
		  AND current.loupe_interface_reported IS NOT DISTINCT FROM
		      EXCLUDED.loupe_interface_reported
		  AND current.truncated = EXCLUDED.truncated
		  AND current.truncation_reason IS NOT DISTINCT FROM
		      EXCLUDED.truncation_reason
		  AND current.warnings = EXCLUDED.warnings
		RETURNING id;

-- name: EnrichInlinePersistDiamondDetectionSnapshotStatement2 :exec
INSERT INTO diamond_loupe_facets AS current (
			    snapshot_id, facet_address, facet_kind, code_exists, code_hash
			) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (snapshot_id, facet_address)
			DO UPDATE SET facet_kind = EXCLUDED.facet_kind
			WHERE current.facet_kind = EXCLUDED.facet_kind
			  AND current.code_exists = EXCLUDED.code_exists
			  AND current.code_hash IS NOT DISTINCT FROM EXCLUDED.code_hash;

-- name: EnrichInlinePersistDiamondDetectionSnapshotStatement3 :exec
INSERT INTO diamond_loupe_selectors AS current (
				    snapshot_id, selector, facet_address
				) VALUES ($1, $2, $3)
				ON CONFLICT (snapshot_id, selector)
				DO UPDATE SET facet_address = EXCLUDED.facet_address
				WHERE current.facet_address = EXCLUDED.facet_address;

-- name: EnrichInlinePersistEffectiveTransactionExecutionsStatement1 :exec
DELETE FROM transaction_effective_execution_identities
		WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3;

-- name: EnrichInlinePersistEffectiveTransactionExecutionsStatement2 :exec
INSERT INTO transaction_effective_execution_identities (
			    chain_id, block_number, block_hash, transaction_hash,
			    transaction_index, context_address, execution_address,
			    execution_code_hash, resolution, evidence_source,
			    root_trace_path, canonical
			) VALUES (
			    $1::numeric, $2::numeric, $3, $4, $5, $6, $7, $8,
			    $9, $10, $11, TRUE
			);

-- name: EnrichInlineProcessTxStatement1 :exec
DELETE FROM abi_decodings
		WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3;

-- name: EnrichInlineProcessTxStatement2 :exec
DELETE FROM contract_abis
		WHERE chain_id = $1::numeric AND block_number = $2::numeric AND block_hash = $3;

-- name: EnrichInlineProxyDependencyStateStatement1 :many
SELECT state
		FROM published_block_stage_results
		WHERE chain_id = $1::numeric AND block_hash = $2
		  AND stage = $3 AND stage_version = $4;

-- name: EnrichInlineProxyOrBeaconHistoryStatement1 :many
SELECT
		    EXISTS (
		        SELECT 1
		        FROM proxy_observations AS observation
		        JOIN canonical_blocks AS canonical
		          ON canonical.chain_id = observation.chain_id
		         AND canonical.number = observation.block_number
		         AND canonical.block_hash = observation.block_hash
		        WHERE observation.chain_id = $1::numeric
			          AND observation.proxy_address = $2::bytea
		          AND observation.block_number <= $3::numeric
		          AND observation.canonical
		        UNION ALL
		        SELECT 1
		        FROM published_diamond_loupe_snapshots AS snapshot
		        JOIN canonical_blocks AS canonical
		          ON canonical.chain_id = snapshot.chain_id
		         AND canonical.number = snapshot.block_number
		         AND canonical.block_hash = snapshot.block_hash
		        WHERE snapshot.chain_id = $1::numeric
			          AND snapshot.diamond_address = $2::bytea
		          AND snapshot.block_number <= $3::numeric
		          AND snapshot.canonical
		    ),
		    EXISTS (
		        SELECT 1
		        FROM proxy_observations AS observation
		        JOIN canonical_blocks AS canonical
		          ON canonical.chain_id = observation.chain_id
		         AND canonical.number = observation.block_number
		         AND canonical.block_hash = observation.block_hash
		        WHERE observation.chain_id = $1::numeric
			          AND observation.beacon_address = $2::bytea
		          AND observation.block_number <= $3::numeric
		          AND observation.canonical
		        UNION ALL
		        SELECT 1
		        FROM beacon_implementation_observations AS observation
		        JOIN canonical_blocks AS canonical
		          ON canonical.chain_id = observation.chain_id
		         AND canonical.number = observation.block_number
		         AND canonical.block_hash = observation.block_hash
		        WHERE observation.chain_id = $1::numeric
		          AND observation.beacon_address = $2
		          AND observation.block_number <= $3::numeric
		          AND observation.canonical
		    );

-- name: EnrichInlineResolveABICodeIdentityStatement1 :many
SELECT observation.block_number::text, observation.code_hash
		FROM contract_code_observations AS observation
		WHERE observation.chain_id = $1::numeric AND observation.address = $2 AND observation.canonical
		  AND observation.block_number <= $3::numeric
		ORDER BY observation.block_number DESC, observation.observed_at DESC
		LIMIT 1;

-- name: EnrichInlineResolveABICodeIdentityStatement2 :many
SELECT min(block_number)::text
		FROM contract_code_observations
		WHERE chain_id = $1::numeric AND address = $2 AND canonical
		  AND block_number > $3::numeric AND code_hash <> $4;

-- name: EnrichInlineResolveDiamondABIRouteStatement1 :many
SELECT EXISTS (
		    SELECT 1
		    FROM published_diamond_loupe_snapshots AS snapshot
		    JOIN canonical_blocks AS canonical
		      ON canonical.chain_id = snapshot.chain_id
		     AND canonical.number = snapshot.block_number
		     AND canonical.block_hash = snapshot.block_hash
		    WHERE snapshot.chain_id = $1::numeric
		      AND snapshot.diamond_address = $2
		      AND snapshot.block_number <= $3::numeric
		      AND snapshot.detection_state = 'confirmed'
		      AND snapshot.canonical
		);

-- name: EnrichInlineResolveDiamondABIRouteStatement2 :many
SELECT EXISTS (
			    SELECT 1
			    FROM diamond_cut_events AS event
			    WHERE event.chain_id = $1::numeric
			      AND event.block_hash = $2
			      AND event.diamond_address = $3
			      AND event.transaction_index = $4::bigint
			      AND event.canonical
			      AND event.stage_version = $5
			);

-- name: EnrichInlineResolveDiamondABIRouteStatement3 :many
SELECT change.action, change.facet_address
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
		WHERE event.chain_id = $1::numeric
		  AND event.diamond_address = $2
		  AND change.selector = $3
		  AND event.canonical
		  AND (
		      event.block_number < $4::numeric OR
		      (event.block_number = $4::numeric AND
		       event.transaction_index < $5::bigint)
		  )
		ORDER BY event.block_number DESC, event.transaction_index DESC,
		         event.log_index DESC, change.cut_index DESC,
		         change.selector_index DESC
		LIMIT 1;

-- name: EnrichInlineResolveDiamondABIRouteStatement4 :many
SELECT EXISTS (
		    SELECT 1 FROM diamond_cut_events AS event
		    WHERE event.chain_id = $1::numeric
		      AND event.block_hash = $2
		      AND event.diamond_address = $3
		      AND event.canonical
		      AND event.stage_version = $4
		);

-- name: EnrichInlineResolveDiamondABIRouteStatement5 :many
SELECT snapshot.completeness, selector.facet_address
		FROM published_diamond_loupe_snapshots AS snapshot
		JOIN canonical_blocks AS canonical
		  ON canonical.chain_id = snapshot.chain_id
		 AND canonical.number = snapshot.block_number
		 AND canonical.block_hash = snapshot.block_hash
		LEFT JOIN diamond_loupe_selectors AS selector
		  ON selector.snapshot_id = snapshot.id AND selector.selector = $3
		WHERE snapshot.chain_id = $1::numeric
		  AND snapshot.diamond_address = $2
		  AND snapshot.block_number <= $4::numeric
		  AND snapshot.detection_state = 'confirmed'
		  AND snapshot.canonical
		ORDER BY snapshot.block_number DESC, snapshot.id DESC
		LIMIT 1;

-- name: EnrichInlineResolveTransactionStartCodeStatement1 :many
SELECT transaction_index, before_value, after_value
		FROM transaction_state_changes
		WHERE chain_id = $1::numeric
		  AND block_number = $2::numeric
		  AND block_hash = $3
		  AND address = $4
		  AND field_kind = 'code'
		  AND canonical
		ORDER BY transaction_index;

-- name: EnrichInlineResolveTransactionStartCodeStatement2 :many
SELECT observation.code_hash, observation.code
			FROM contract_code_observations AS observation
			JOIN canonical_blocks AS canonical
			  ON canonical.chain_id = observation.chain_id
			 AND canonical.number = observation.block_number
			 AND canonical.block_hash = observation.block_hash
			WHERE observation.chain_id = $1::numeric
			  AND observation.address = $2
			  AND observation.block_number < $3::numeric
			  AND observation.canonical
			ORDER BY observation.block_number DESC, observation.observed_at DESC,
			         observation.code_hash DESC
			LIMIT 1;

-- name: EnrichInlineResolveTransactionStartCodeStatement3 :many
SELECT observation.code_hash, observation.code
		FROM contract_code_observations AS observation
		JOIN canonical_blocks AS canonical
		  ON canonical.chain_id = observation.chain_id
		 AND canonical.number = observation.block_number
		 AND canonical.block_hash = observation.block_hash
		WHERE observation.chain_id = $1::numeric
		  AND observation.address = $2
		  AND observation.block_number < $3::numeric
		  AND observation.canonical
		ORDER BY observation.block_number DESC, observation.observed_at DESC,
		         observation.code_hash DESC
		LIMIT 1;
