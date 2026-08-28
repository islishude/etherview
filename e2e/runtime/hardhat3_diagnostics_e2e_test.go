//go:build runtimee2e && hardhat3e2e

package runtimee2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

func hardhatProxyAdmissionDiagnostics(
	t *testing.T,
	ctx context.Context,
	h *harness,
	proxy string,
) string {
	t.Helper()
	var summary string
	err := h.db.QueryRow(ctx, `
		SELECT jsonb_build_object(
		  'tip', COALESCE((
		    SELECT jsonb_build_object(
		      'number', number::text,
		      'hash', '0x' || encode(block_hash, 'hex')
		    )
		    FROM canonical_blocks
		    WHERE chain_id = 1
		    ORDER BY number DESC
		    LIMIT 1
		  ), '{}'::jsonb),
		  'observations', COALESCE((
		    SELECT jsonb_agg(to_jsonb(row) ORDER BY row.block_number DESC, row.block_hash DESC)
		    FROM (
		      SELECT observation.block_number::text,
		             '0x' || encode(observation.block_hash, 'hex') AS block_hash,
		             observation.proxy_pattern,
		             observation.evidence_state,
		             '0x' || encode(observation.proxy_code_hash, 'hex') AS proxy_code_hash,
		             '0x' || encode(observation.implementation_address, 'hex') AS implementation
		      FROM proxy_observations AS observation
		      JOIN canonical_blocks AS canonical
		        ON canonical.chain_id = observation.chain_id
		       AND canonical.number = observation.block_number
		       AND canonical.block_hash = observation.block_hash
		      WHERE observation.chain_id = 1
		        AND observation.proxy_address = $1
		        AND observation.canonical
		      ORDER BY observation.block_number DESC, observation.block_hash DESC
		      LIMIT 8
		    ) AS row
		  ), '[]'::jsonb),
		  'resolutions', COALESCE((
		    SELECT jsonb_agg(to_jsonb(row) ORDER BY row.id DESC)
		    FROM (
		      SELECT resolution.id,
		             '0x' || encode(resolution.observation_block_hash, 'hex') AS block_hash,
		             resolution.proxy_pattern,
		             resolution.standard_version,
		             resolution.proxy_artifact_job_id,
		             resolution.implementation_artifact_job_id,
		             '0x' || encode(resolution.implementation_address, 'hex') AS implementation,
		             published.state AS publication_state
		      FROM proxy_artifact_resolutions AS resolution
		      LEFT JOIN published_block_stage_results AS published
		        ON published.chain_id = resolution.chain_id
		       AND published.block_hash = resolution.observation_block_hash
		       AND published.stage = 'proxy'
		       AND published.stage_version = resolution.observation_stage_version
		       AND published.durable_job_id = resolution.durable_job_id
		       AND published.job_generation = resolution.job_generation
		      WHERE resolution.chain_id = 1
		        AND resolution.proxy_address = $1
		      ORDER BY resolution.id DESC
		      LIMIT 8
		    ) AS row
		  ), '[]'::jsonb),
		  'negative_evidence', COALESCE((
		    SELECT jsonb_agg(to_jsonb(row) ORDER BY row.block_number DESC, row.job_generation DESC)
		    FROM (
		      SELECT evidence.block_number::text,
		             '0x' || encode(evidence.block_hash, 'hex') AS block_hash,
		             '0x' || encode(evidence.code_hash, 'hex') AS code_hash,
		             evidence.detection_state, evidence.reason,
		             evidence.durable_job_id, evidence.job_generation,
		             published.state AS publication_state
		      FROM proxy_detection_evidence AS evidence
		      JOIN canonical_blocks AS canonical
		        ON canonical.chain_id = evidence.chain_id
		       AND canonical.number = evidence.block_number
		       AND canonical.block_hash = evidence.block_hash
		      LEFT JOIN published_block_stage_results AS published
		        ON published.chain_id = evidence.chain_id
		       AND published.block_hash = evidence.block_hash
		       AND published.stage = 'proxy'
		       AND published.stage_version = evidence.stage_version
		       AND published.durable_job_id = evidence.durable_job_id
		       AND published.job_generation = evidence.job_generation
		      WHERE evidence.chain_id = 1
		        AND evidence.address = $1
		        AND evidence.canonical
		      ORDER BY evidence.block_number DESC, evidence.job_generation DESC
		      LIMIT 8
		    ) AS row
		  ), '[]'::jsonb),
		  'artifacts', COALESCE((
		    SELECT jsonb_agg(to_jsonb(row) ORDER BY row.valid_from_block DESC)
		    FROM (
		      SELECT artifact.artifact_kind, artifact.standard_version,
		             artifact.valid_from_block::text,
		             artifact.verification_job_id,
		             '0x' || encode(artifact.code_hash, 'hex') AS code_hash,
		             verified.valid_to_block::text
		      FROM verified_contract_proxy_artifacts AS artifact
		      JOIN verified_contracts AS verified
		        ON verified.chain_id = artifact.chain_id
		       AND verified.address = artifact.address
		       AND verified.code_hash = artifact.code_hash
		       AND verified.valid_from_block = artifact.valid_from_block
		       AND verified.verification_job_id = artifact.verification_job_id
		       AND verified.request_digest = artifact.request_digest
		      WHERE artifact.chain_id = 1
		        AND artifact.address = $1
		    ) AS row
		  ), '[]'::jsonb),
		  'code_changes', COALESCE((
		    SELECT jsonb_agg(to_jsonb(row) ORDER BY row.block_number DESC)
		    FROM (
		      SELECT change.block_number::text,
		             '0x' || encode(change.block_hash, 'hex') AS block_hash,
		             change.before_value, change.after_value
		      FROM transaction_state_changes AS change
		      JOIN canonical_blocks AS canonical
		        ON canonical.chain_id = change.chain_id
		       AND canonical.number = change.block_number
		       AND canonical.block_hash = change.block_hash
		      WHERE change.chain_id = 1
		        AND change.address = $1
		        AND change.field_kind = 'code'
		        AND change.canonical
		      ORDER BY change.block_number DESC
		      LIMIT 8
		    ) AS row
		  ), '[]'::jsonb)
		)::text`, common.HexToAddress(proxy).Bytes()).Scan(&summary)
	if err != nil {
		return "diagnostic-error=" + diagnosticError(err)
	}
	return summary
}

func captureHardhatSQLDiagnostics(h *harness) {
	if h == nil || h.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var summary string
	err := h.db.QueryRow(ctx, `
		SELECT jsonb_build_object(
		  'catalog_heads', COALESCE((
		    SELECT jsonb_agg(to_jsonb(row) ORDER BY row.language)
		    FROM (
		      SELECT head.language, head.generation_id, head.updated_at
		      FROM compiler_catalog_heads AS head
		    ) AS row
		  ), '[]'::jsonb),
		  'catalog_generations', COALESCE((
		    SELECT jsonb_agg(to_jsonb(row) ORDER BY row.id)
		    FROM (
		      SELECT id, language, source_url, entry_count, fetched_at
		      FROM compiler_catalog_generations
		    ) AS row
		  ), '[]'::jsonb),
		  'jobs', COALESCE((
		    SELECT jsonb_agg(to_jsonb(row) ORDER BY row.created_at, row.id)
		    FROM (
		      SELECT id, kind, status, error_code, outcome_kind,
		             compiler_platform, catalog_generation_id,
		             encode(compiler_digest, 'hex') AS compiler_digest,
		             executor_kind, execution_policy,
		             encode(executor_digest, 'hex') AS executor_digest,
		             attempt_count, max_attempts, leased_by, lease_expires_at,
		             created_at, updated_at
		      FROM verification_jobs
		    ) AS row
		  ), '[]'::jsonb),
		  'results', COALESCE((
		    SELECT jsonb_agg(to_jsonb(row) ORDER BY row.created_at, row.job_id)
		    FROM (
		      SELECT job_id, outcome_kind, created_at
		      FROM verification_results
		    ) AS row
		  ), '[]'::jsonb),
		  'proxy_bindings', COALESCE((
		    SELECT jsonb_agg(to_jsonb(row) ORDER BY row.created_at, row.verification_job_id)
		    FROM (
		      SELECT verification_job_id, proxy_kind, proxy_pattern,
		             '0x' || encode(proxy_address, 'hex') AS proxy_address,
		             '0x' || encode(implementation_address, 'hex') AS implementation_address,
		             '0x' || encode(observation_block_hash, 'hex') AS observation_block_hash,
		             created_at
		      FROM verified_proxy_bindings
		    ) AS row
		  ), '[]'::jsonb),
		  'proxy_detection_v2', COALESCE((
		    SELECT jsonb_agg(to_jsonb(row) ORDER BY row.block_number DESC, row.job_generation DESC)
		    FROM (
		      SELECT evidence.block_number::text AS block_number,
			             '0x' || encode(evidence.block_hash, 'hex') AS block_hash,
			             '0x' || encode(evidence.address, 'hex') AS address,
			             evidence.detection_state, evidence.durable_job_id,
			             evidence.job_generation,
			             evidence.details #>> '{primary,detector}' AS detector,
			             evidence.details #>> '{primary,family}' AS family,
			             evidence.details #>> '{primary,variant}' AS variant,
			             evidence.details #>> '{primary,implementation}' AS implementation
		      FROM proxy_detection_evidence AS evidence
		      WHERE evidence.chain_id = 1
		        AND evidence.candidate_kind = 'proxy_v2'
		      ORDER BY evidence.block_number DESC, evidence.job_generation DESC
		      LIMIT 64
		    ) AS row
		  ), '[]'::jsonb)
		)::text`).Scan(&summary)
	if err != nil {
		h.writeArtifact("verification-sql-summary-error.txt", []byte(diagnosticError(err)))
		return
	}
	h.writeArtifact("verification-sql-summary.json", []byte(summary))
}

func captureHardhatProxySnapshot(
	t *testing.T,
	ctx context.Context,
	h *harness,
	proxy, diamond, safeProxy, safeSingleton string,
) hardhatProxySnapshot {
	t.Helper()
	var result hardhatProxySnapshot
	if err := h.db.QueryRow(ctx, `
		SELECT
		  count(*) FILTER (WHERE kind = 'address' AND status = 'succeeded'),
		  count(*) FILTER (WHERE kind = 'derived' AND status = 'succeeded'),
		  count(*) FILTER (WHERE language = 'yul' AND status = 'succeeded'),
		  count(*) FILTER (WHERE kind = 'proxy' AND status = 'succeeded'),
		  count(*) FILTER (WHERE kind = 'address' AND status = 'succeeded'
		    AND compiler_platform = 'emscripten-wasm32'
		    AND catalog_generation_id IS NOT NULL
		    AND executor_digest IS NOT NULL
		    AND executor_kind = 'node_solcjs_v1'
		    AND execution_policy = 'trusted_subprocess') = 9,
		  count(*) FILTER (WHERE language = 'yul' AND status = 'succeeded'
		    AND compiler_version = '0.8.30+commit.73712a01'
		    AND compiler_platform = 'emscripten-wasm32'
		    AND catalog_generation_id IS NOT NULL
		    AND compiler_digest IS NOT NULL
		    AND executor_digest IS NOT NULL
		    AND executor_kind = 'node_solcjs_v1'
		    AND execution_policy = 'trusted_subprocess') = 1,
		  count(*) FILTER (WHERE kind = 'address' AND status = 'succeeded'
		    AND compiler_digest IS NOT NULL) = 9
		FROM verification_jobs`).Scan(
		&result.AddressJobs, &result.DerivedJobs, &result.YulJobs, &result.ProxyJobs,
		&result.ExecutorProvenance, &result.YulProvenance,
		&result.CompilerProvenance,
	); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRow(ctx, `
		SELECT
		  count(*) FILTER (WHERE outcome_kind = 'verification_success'),
		  count(*) FILTER (WHERE outcome_kind = 'verification_success'
		    AND job_id IN (SELECT id FROM verification_jobs WHERE kind = 'derived')),
		  count(*) FILTER (WHERE outcome_kind = 'verification_success'
		    AND job_id IN (SELECT id FROM verification_jobs WHERE language = 'yul')),
		  count(*) FILTER (WHERE outcome_kind = 'proxy_verification_success')
		FROM verification_results`).Scan(
		&result.CompilerResults, &result.DerivedResults,
		&result.YulResults, &result.ProxyResults,
	); err != nil {
		t.Fatal(err)
	}
	if result.YulJobs != 1 || result.YulResults != 1 || !result.YulProvenance {
		t.Fatalf("Yul compiler provenance is incomplete: %#v", result)
	}
	if err := h.db.QueryRow(ctx, `SELECT count(*) FROM verified_proxy_bindings`).Scan(&result.ProxyBindings); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM verified_contracts AS publication
		   JOIN verification_jobs AS job ON job.id = publication.verification_job_id
		   WHERE job.kind = 'derived'),
		  (SELECT count(*) FROM derived_verification_attempts
		   WHERE status = 'matched' AND verification_job_id IS NOT NULL)`).Scan(
		&result.DerivedPublications, &result.DerivedAttempts,
	); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRow(ctx, `SELECT count(*) FROM compiler_catalog_entries`).Scan(&result.CatalogEntries); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRow(ctx, `
		SELECT '0x' || encode(observation.proxy_address, 'hex'),
		       '0x' || encode(observation.implementation_address, 'hex'),
		       observation.proxy_kind
		FROM proxy_observations AS observation
		JOIN canonical_blocks AS canonical
		  ON canonical.chain_id = observation.chain_id
		 AND canonical.number = observation.block_number
		 AND canonical.block_hash = observation.block_hash
		WHERE observation.chain_id = 1
		  AND observation.proxy_address = $1
		  AND observation.canonical
		ORDER BY observation.block_number DESC
		LIMIT 1`, common.HexToAddress(proxy).Bytes()).Scan(
		&result.CurrentProxy, &result.CurrentImpl, &result.CurrentProxyKind,
	); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRow(ctx, `
		WITH latest AS (
		    SELECT snapshot.id, snapshot.detection_state
		    FROM published_diamond_loupe_snapshots AS snapshot
		    JOIN canonical_blocks AS canonical
		      ON canonical.chain_id = snapshot.chain_id
		     AND canonical.number = snapshot.block_number
		     AND canonical.block_hash = snapshot.block_hash
		    WHERE snapshot.chain_id = 1
		      AND snapshot.diamond_address = $1
		      AND snapshot.canonical
		    ORDER BY snapshot.block_number DESC, snapshot.id DESC
		    LIMIT 1
		)
		SELECT
		  COALESCE((SELECT detection_state FROM latest), ''),
		  (SELECT count(*) FROM diamond_loupe_facets
		   WHERE snapshot_id IN (SELECT id FROM latest)),
		  (SELECT count(*) FROM diamond_loupe_selectors
		   WHERE snapshot_id IN (SELECT id FROM latest)),
		  (SELECT count(*) FROM diamond_cut_events
		   WHERE chain_id = 1 AND diamond_address = $1 AND canonical),
		  (SELECT count(*) FROM proxy_observations
		   WHERE chain_id = 1 AND proxy_address = $1)`,
		common.HexToAddress(diamond).Bytes(),
	).Scan(
		&result.DiamondState, &result.DiamondFacets,
		&result.DiamondSelectors, &result.DiamondCuts,
		&result.DiamondSingular,
	); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRow(ctx, `
		WITH current AS (
		    SELECT evidence.detection_state, evidence.details
		    FROM proxy_detection_evidence AS evidence
		    JOIN canonical_blocks AS canonical
		      ON canonical.chain_id = evidence.chain_id
		     AND canonical.number = evidence.block_number
		     AND canonical.block_hash = evidence.block_hash
		    JOIN published_block_stage_results AS published
		      ON published.chain_id = evidence.chain_id
		     AND published.block_hash = evidence.block_hash
		     AND published.stage = 'proxy'
		     AND published.stage_version = evidence.stage_version
		     AND published.durable_job_id = evidence.durable_job_id
		     AND published.job_generation = evidence.job_generation
		     AND published.state = 'complete'
		    WHERE evidence.chain_id = 1
		      AND evidence.address = $1
		      AND evidence.candidate_kind = 'proxy_v2'
		      AND evidence.canonical
		    ORDER BY evidence.block_number DESC, evidence.job_generation DESC
		    LIMIT 1
		)
		SELECT
		  COALESCE((SELECT detection_state FROM current), ''),
		  COALESCE((SELECT details #>> '{primary,detector}' FROM current), ''),
		  COALESCE((SELECT details #>> '{primary,family}' FROM current), ''),
		  COALESCE((SELECT details #>> '{primary,variant}' FROM current), ''),
		  COALESCE((SELECT details #>> '{primary,implementation_role}' FROM current), ''),
		  COALESCE((SELECT details #>> '{primary,implementation}' FROM current), ''),
		  COALESCE((SELECT (details #>> '{primary,canonical_proxy_shell}')::boolean FROM current), false),
		  COALESCE((SELECT (details #>> '{primary,official_singleton}')::boolean FROM current), false),
		  (SELECT count(*) FROM proxy_observations
		   WHERE chain_id = 1 AND proxy_address = $1),
		  (SELECT count(*) FROM normalized_traces
		   WHERE chain_id = 1 AND created_address = $1
		     AND call_type = 'CREATE2' AND NOT reverted AND canonical)`,
		common.HexToAddress(safeProxy).Bytes(),
	).Scan(
		&result.SafeState, &result.SafeDetector, &result.SafeFamily,
		&result.SafeVariant, &result.SafeRole, &result.SafeImplementation,
		&result.SafeCanonicalShell, &result.SafeOfficial,
		&result.SafeLegacyRows, &result.SafeTraceCreates,
	); err != nil {
		t.Fatal(err)
	}
	if result.AddressJobs != 9 || result.DerivedJobs != 1 || result.ProxyJobs != 11 ||
		result.CompilerResults != 11 || result.ProxyResults != 11 ||
		result.DerivedResults != 1 || result.DerivedPublications != 1 ||
		result.DerivedAttempts != 1 ||
		result.ProxyBindings != 11 || result.CatalogEntries == 0 ||
		!result.ExecutorProvenance || !result.CompilerProvenance ||
		result.CurrentProxyKind != "eip1967" ||
		result.DiamondState != "confirmed" || result.DiamondFacets != 3 ||
		result.DiamondSelectors != 8 || result.DiamondCuts != 1 ||
		result.DiamondSingular != 0 || result.SafeState != "confirmed" ||
		result.SafeDetector != "safe" || result.SafeFamily != "safe" ||
		result.SafeVariant != "safe-proxy" || result.SafeRole != "singleton" ||
		!strings.EqualFold(result.SafeImplementation, safeSingleton) ||
		!result.SafeCanonicalShell || result.SafeOfficial || result.SafeLegacyRows != 0 ||
		result.SafeTraceCreates != 1 {
		t.Fatalf("incomplete Hardhat/proxy persistence: %#v", result)
	}
	return result
}
