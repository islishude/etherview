package verify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// CompleteProxyV2 publishes a proxy binding only while the exact submitted
// observation remains the current canonical mapping, every participating code
// identity is current, and each required interaction target retains a verified
// source publication.
func (repository *PostgresRepository) CompleteProxyV2(
	ctx context.Context,
	lease VerificationLease,
) error {
	if err := validateVerificationLease(lease); err != nil {
		return err
	}
	if lease.Job.RequestV2 == nil || lease.Job.RequestV2.Kind != JobProxy ||
		lease.Job.RequestV2.Target == nil || lease.Job.RequestV2.ProxyTarget == nil {
		return errors.New("proxy verification lease is invalid")
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	job, err := repository.scanV2Job(tx.QueryRowContext(ctx, `
		SELECT `+v2VerificationJobColumns+` FROM verification_jobs
		WHERE id = $1::uuid AND status = 'running' AND lease_token = $2
		  AND lease_expires_at > clock_timestamp() FOR UPDATE
	`, lease.Job.ID, lease.Token))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	request := job.RequestV2
	if request == nil || request.Kind != JobProxy || request.Target == nil ||
		request.ProxyTarget == nil {
		return errors.New("stored proxy verification request is invalid")
	}
	if err := validateProxyVerificationSubmission(request); err != nil {
		return errors.New("stored proxy verification request is invalid")
	}
	chainID := strconv.FormatUint(request.Target.ChainID, 10)
	// Serialize the entire current-state read and immutable publication with
	// every proxy interaction coverage refresh. Taking this fence only in the
	// binding INSERT trigger leaves a window where a canonical-tip advance or a
	// same-block proxy/state-diff replay can replace the facts selected below.
	if _, err := tx.ExecContext(ctx, `
		SELECT pg_advisory_xact_lock(hashtextextended(
		    'etherview:proxy-interaction-coverage:' || $1::numeric::text,
		    0
		))
	`, chainID); err != nil {
		return err
	}
	proxyAddress, _ := decodeFixedHex(request.Target.Address, 20)
	proxyCodeHash, _ := decodeFixedHex(request.Target.CodeHash, 32)
	blockHash, _ := decodeFixedHex(request.Target.AtBlockHash, 32)
	submissionContextHash, _ := decodeFixedHex(
		request.ProxyTarget.SubmissionContextBlockHash,
		32,
	)
	implementationAddress, _ := decodeFixedHex(request.ProxyTarget.ImplementationAddress, 20)
	implementationCodeHash, _ := decodeFixedHex(request.ProxyTarget.ImplementationCodeHash, 32)
	adminAddress, adminCodeHash := proxyIdentitySQLValues(
		request.ProxyTarget.AdminAddress,
		request.ProxyTarget.AdminCodeHash,
	)
	beaconAddress, beaconCodeHash := proxyIdentitySQLValues(
		request.ProxyTarget.BeaconAddress,
		request.ProxyTarget.BeaconCodeHash,
	)
	managementAddress, managementCodeHash := proxyIdentitySQLValues(
		request.ProxyTarget.ManagementAddress,
		request.ProxyTarget.ManagementCodeHash,
	)
	observationGenerationID, _ := strconv.ParseInt(
		request.ProxyTarget.ObservationGenerationID,
		10,
		64,
	)
	artifactResolutionID := proxyGenerationSQLValue(request.ProxyTarget.ArtifactResolutionID)
	beaconGenerationID := proxyGenerationSQLValue(request.ProxyTarget.BeaconGenerationID)
	uupsGenerationID := proxyGenerationSQLValue(request.ProxyTarget.UUPSGenerationID)
	var standardVersion any
	if request.ProxyTarget.StandardVersion != "" {
		standardVersion = request.ProxyTarget.StandardVersion
	}

	var observationBlock, contextBlock string
	var observationGeneration int64
	var artifactResolution, beaconGeneration, uupsGeneration sql.NullInt64
	var contextHash []byte
	err = tx.QueryRowContext(ctx, proxyVerificationCurrentTargetSQL,
		chainID,
		proxyAddress,
		proxyCodeHash,
		blockHash,
		request.ProxyTarget.Kind,
		implementationAddress,
		implementationCodeHash,
		request.ProxyTarget.Pattern,
		standardVersion,
		adminAddress,
		adminCodeHash,
		beaconAddress,
		beaconCodeHash,
		request.ProxyTarget.ManagementKind,
		managementAddress,
		managementCodeHash,
		observationGenerationID,
		artifactResolutionID,
		beaconGenerationID,
		request.ProxyTarget.SubmissionContextBlockNumber,
		submissionContextHash,
		uupsGenerationID,
	).Scan(
		&observationBlock, &observationGeneration, &artifactResolution,
		&beaconGeneration, &uupsGeneration, &contextBlock, &contextHash,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrTargetNotCanonical
	}
	if err != nil {
		return err
	}
	if observationGeneration <= 0 || len(contextHash) != 32 {
		return errors.New("current proxy target generation context is invalid")
	}
	outcome, err := json.Marshal(map[string]any{
		"kind":                            "proxy_verification_success",
		"proxy_address":                   request.Target.Address,
		"proxy_code_hash":                 request.Target.CodeHash,
		"observation_block_hash":          request.Target.AtBlockHash,
		"observation_stage_version":       2,
		"proxy_kind":                      request.ProxyTarget.Kind,
		"proxy_pattern":                   request.ProxyTarget.Pattern,
		"standard_version":                proxyOutcomeValue(request.ProxyTarget.StandardVersion),
		"implementation_address":          request.ProxyTarget.ImplementationAddress,
		"implementation_code_hash":        request.ProxyTarget.ImplementationCodeHash,
		"admin_address":                   proxyOutcomeValue(request.ProxyTarget.AdminAddress),
		"admin_code_hash":                 proxyOutcomeValue(request.ProxyTarget.AdminCodeHash),
		"beacon_address":                  proxyOutcomeValue(request.ProxyTarget.BeaconAddress),
		"beacon_code_hash":                proxyOutcomeValue(request.ProxyTarget.BeaconCodeHash),
		"management_kind":                 request.ProxyTarget.ManagementKind,
		"management_address":              proxyOutcomeValue(request.ProxyTarget.ManagementAddress),
		"management_code_hash":            proxyOutcomeValue(request.ProxyTarget.ManagementCodeHash),
		"observation_generation_id":       observationGeneration,
		"artifact_resolution_id":          nullInt64Outcome(artifactResolution),
		"beacon_generation_id":            nullInt64Outcome(beaconGeneration),
		"uups_generation_id":              nullInt64Outcome(uupsGeneration),
		"submission_context_block_number": request.ProxyTarget.SubmissionContextBlockNumber,
		"submission_context_block_hash":   request.ProxyTarget.SubmissionContextBlockHash,
		"context_block_number":            contextBlock,
		"context_block_hash":              "0x" + fmt.Sprintf("%x", contextHash),
	})
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE verification_jobs
		SET status = 'succeeded', outcome_kind = 'proxy_verification_success',
		    outcome = $3::jsonb, error_code = NULL, leased_by = NULL,
		    lease_token = NULL, lease_expires_at = NULL,
		    updated_at = clock_timestamp()
		WHERE id = $1::uuid AND lease_token = $2
	`, job.ID, lease.Token, string(outcome)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO verification_results (
			job_id, request_digest, outcome_kind, outcome
		) VALUES ($1::uuid, $2, 'proxy_verification_success', $3::jsonb)
	`, job.ID, job.RequestDigest[:], string(outcome)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO verified_proxy_bindings (
			chain_id, proxy_address, proxy_code_hash, observation_block_number,
			observation_block_hash, observation_stage_version, proxy_kind,
			proxy_pattern, standard_version, implementation_address,
			implementation_code_hash, admin_address, admin_code_hash,
			beacon_address, beacon_code_hash, management_kind,
			management_address, management_code_hash,
			observation_generation_id, artifact_resolution_id,
			beacon_generation_id, uups_generation_id,
			context_block_number, context_block_hash,
			verification_job_id, request_digest
		) VALUES (
			$1::numeric, $2, $3, $4::numeric, $5, 2, $6, $7, $8, $9, $10,
			$11, $12, $13, $14, $15, $16, $17,
			$18::bigint, $19::bigint, $20::bigint, $21::bigint,
			$22::numeric, $23, $24::uuid, $25
		)
	`, chainID, proxyAddress, proxyCodeHash,
		observationBlock, blockHash, request.ProxyTarget.Kind, request.ProxyTarget.Pattern,
		standardVersion, implementationAddress, implementationCodeHash,
		adminAddress, adminCodeHash, beaconAddress, beaconCodeHash,
		request.ProxyTarget.ManagementKind, managementAddress, managementCodeHash,
		observationGeneration, nullInt64SQLValue(artifactResolution),
		nullInt64SQLValue(beaconGeneration), nullInt64SQLValue(uupsGeneration),
		contextBlock, contextHash,
		job.ID, job.RequestDigest[:],
	); err != nil {
		return err
	}
	return tx.Commit()
}

func nullInt64Outcome(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func nullInt64SQLValue(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func proxyIdentitySQLValues(address, codeHash string) (any, any) {
	if address == "" {
		return nil, nil
	}
	addressBytes, _ := decodeFixedHex(address, 20)
	codeHashBytes, _ := decodeFixedHex(codeHash, 32)
	return addressBytes, codeHashBytes
}

func proxyGenerationSQLValue(value string) any {
	if value == "" {
		return nil
	}
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}

func proxyOutcomeValue(value string) any {
	if value == "" {
		return nil
	}
	return value
}

const proxyVerificationCurrentTargetSQL = `
WITH submission_context AS (
    SELECT number, block_hash
    FROM canonical_blocks
    WHERE chain_id = $1::numeric
      AND number = $20::numeric
      AND block_hash = $21
), canonical_tip AS (
    SELECT number, block_hash
    FROM canonical_blocks
    WHERE chain_id = $1::numeric
    ORDER BY number DESC
    LIMIT 1
), latest_raw AS (
    SELECT observation.*, tip.number AS context_number,
           tip.block_hash AS context_hash
    FROM canonical_tip AS tip
    JOIN LATERAL (
        SELECT observation.*
        FROM proxy_observations AS observation
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = observation.chain_id
         AND canonical.number = observation.block_number
         AND canonical.block_hash = observation.block_hash
        WHERE observation.chain_id = $1::numeric
          AND observation.proxy_address = $2
          AND observation.canonical = TRUE
          AND observation.stage_version = 2
          AND observation.confidence IN ('verified', 'high')
          AND observation.block_number <= tip.number
        ORDER BY observation.block_number DESC, observation.block_hash DESC
        LIMIT 1
    ) AS observation ON TRUE
), published_raw AS (
    SELECT raw.*, generation.id AS observation_generation_id,
           generation.durable_job_id AS observation_durable_job_id,
           generation.job_generation AS observation_job_generation
    FROM latest_raw AS raw
    JOIN LATERAL (
        SELECT witness.id, witness.durable_job_id, witness.job_generation
        FROM proxy_observation_generations AS witness
        JOIN published_block_stage_results AS published
          ON published.chain_id = witness.chain_id
         AND published.block_hash = witness.observation_block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = witness.observation_stage_version
         AND published.durable_job_id = witness.durable_job_id
         AND published.job_generation = witness.job_generation
         AND published.state = 'complete'
        WHERE witness.chain_id = raw.chain_id
          AND witness.proxy_address = raw.proxy_address
          AND witness.observation_block_hash = raw.block_hash
          AND witness.observation_stage_version = raw.stage_version
        ORDER BY witness.id DESC
        LIMIT 1
    ) AS generation ON TRUE
), unshadowed_raw AS (
    SELECT raw.*
    FROM published_raw AS raw
    WHERE NOT EXISTS (
        SELECT 1
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
        WHERE evidence.chain_id = raw.chain_id
          AND evidence.address = raw.proxy_address
          AND evidence.code_hash = raw.proxy_code_hash
          AND evidence.candidate_kind = 'proxy'
          AND evidence.stage_version = raw.stage_version
          AND evidence.canonical = TRUE
          AND evidence.block_number <= raw.context_number
          AND NOT (
              evidence.reason = 'immutable_args_creation_unverified'
              AND raw.proxy_pattern = 'clone'
              AND raw.evidence_state = 'exact'
              AND octet_length(raw.immutable_args) > 0
              AND raw.details->>'immutable_args_creation_authenticated' = 'true'
          )
          AND (
              evidence.block_number > raw.block_number OR (
                  evidence.block_number = raw.block_number
                  AND evidence.block_hash = raw.block_hash
                  AND evidence.durable_job_id = raw.observation_durable_job_id
                  AND evidence.job_generation >= raw.observation_job_generation
              )
          )
    )
), resolved_proxy AS (
    SELECT raw.*, resolution.id AS artifact_resolution_id,
           resolution.proxy_artifact_job_id,
           resolution.implementation_artifact_job_id,
           CASE WHEN raw.proxy_pattern = 'clone' THEN raw.proxy_kind
                ELSE resolution.proxy_kind END AS effective_kind,
           CASE WHEN raw.proxy_pattern = 'clone' THEN raw.proxy_pattern
                WHEN resolution.proxy_pattern = 'uups' THEN 'erc1967'
                ELSE resolution.proxy_pattern END AS effective_pattern,
           CASE WHEN raw.proxy_pattern = 'clone' THEN NULL::text
                ELSE resolution.standard_version END AS effective_standard,
           CASE WHEN raw.proxy_pattern = 'clone' THEN raw.implementation_address
                ELSE resolution.implementation_address END AS effective_implementation,
           CASE WHEN raw.proxy_pattern = 'clone' THEN raw.implementation_code_hash
                ELSE resolution.implementation_code_hash END AS effective_implementation_hash,
           resolution.admin_address AS effective_admin,
           resolution.admin_code_hash AS effective_admin_hash,
           resolution.beacon_address AS effective_beacon,
           resolution.beacon_code_hash AS effective_beacon_hash
    FROM unshadowed_raw AS raw
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
          AND candidate.proxy_pattern <> 'uups'
        ORDER BY candidate.id DESC
        LIMIT 1
    ) AS resolution ON raw.proxy_pattern <> 'clone'
    WHERE (raw.proxy_pattern = 'clone' AND raw.evidence_state = 'exact')
       OR resolution.id IS NOT NULL
), resolved_epoch AS (
    SELECT proxy.*,
           COALESCE(code_epoch.block_number, 0::numeric) AS implementation_epoch_block
    FROM resolved_proxy AS proxy
    LEFT JOIN LATERAL (
        SELECT max(change.block_number) AS block_number
        FROM transaction_state_changes AS change
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = change.chain_id
         AND canonical.number = change.block_number
         AND canonical.block_hash = change.block_hash
        WHERE change.chain_id = proxy.chain_id
          AND change.address = proxy.effective_implementation
          AND change.field_kind = 'code'
          AND change.canonical = TRUE
          AND change.block_number <= proxy.context_number
          AND lower(change.before_value) IS DISTINCT FROM lower(change.after_value)
    ) AS code_epoch ON TRUE
), latest_uups_probe AS (
    SELECT proxy.chain_id, proxy.proxy_address,
           latest.block_number, latest.block_hash,
           latest.implementation_code_hash, latest.verification_job_id,
           latest.standard_version, latest.probe_state,
           latest.proxiable_uuid, latest.upgrade_interface_version,
           latest.uups_generation_id
    FROM resolved_epoch AS proxy
    JOIN LATERAL (
        SELECT candidate.*
        FROM (
            SELECT observation.*,
                   generation.id AS uups_generation_id
            FROM uups_implementation_observations AS observation
            JOIN canonical_blocks AS canonical
              ON canonical.chain_id = observation.chain_id
             AND canonical.number = observation.block_number
             AND canonical.block_hash = observation.block_hash
            JOIN uups_implementation_observation_generations AS generation
              ON generation.chain_id = observation.chain_id
             AND generation.implementation_address = observation.implementation_address
             AND generation.observation_block_hash = observation.block_hash
             AND generation.observation_stage_version = observation.stage_version
             AND generation.verification_job_id = observation.verification_job_id
            JOIN published_block_stage_results AS published
              ON published.chain_id = generation.chain_id
             AND published.block_hash = generation.observation_block_hash
             AND published.stage = 'proxy'
             AND published.stage_version = generation.observation_stage_version
             AND published.durable_job_id = generation.durable_job_id
             AND published.job_generation = generation.job_generation
             AND published.state = 'complete'
            WHERE observation.chain_id = proxy.chain_id
              AND observation.implementation_address = proxy.effective_implementation
              AND observation.implementation_code_hash = proxy.effective_implementation_hash
              AND observation.stage_version = 2
              AND observation.canonical = TRUE
              AND observation.block_number <= proxy.context_number
            ORDER BY observation.block_number DESC,
                     observation.block_hash DESC,
                     generation.id DESC,
                     observation.verification_job_id DESC
            LIMIT 1
        ) AS candidate
        WHERE NOT EXISTS (
            SELECT 1
            FROM uups_implementation_observations AS conflict
            JOIN canonical_blocks AS conflict_canonical
              ON conflict_canonical.chain_id = conflict.chain_id
             AND conflict_canonical.number = conflict.block_number
             AND conflict_canonical.block_hash = conflict.block_hash
            JOIN uups_implementation_observation_generations AS conflict_generation
              ON conflict_generation.chain_id = conflict.chain_id
             AND conflict_generation.implementation_address = conflict.implementation_address
             AND conflict_generation.observation_block_hash = conflict.block_hash
             AND conflict_generation.observation_stage_version = conflict.stage_version
             AND conflict_generation.verification_job_id = conflict.verification_job_id
            JOIN published_block_stage_results AS conflict_published
              ON conflict_published.chain_id = conflict_generation.chain_id
             AND conflict_published.block_hash = conflict_generation.observation_block_hash
             AND conflict_published.stage = 'proxy'
             AND conflict_published.stage_version = conflict_generation.observation_stage_version
             AND conflict_published.durable_job_id = conflict_generation.durable_job_id
             AND conflict_published.job_generation = conflict_generation.job_generation
             AND conflict_published.state = 'complete'
            WHERE conflict.chain_id = candidate.chain_id
              AND conflict.implementation_address = candidate.implementation_address
              AND conflict.implementation_code_hash = candidate.implementation_code_hash
              AND conflict.block_number = candidate.block_number
              AND conflict.block_hash = candidate.block_hash
              AND conflict.stage_version = candidate.stage_version
              AND conflict.canonical = TRUE
              AND (
                  conflict.probe_state || ':' ||
                  COALESCE(conflict.rejection_reason, '')
              ) IS DISTINCT FROM (
                  candidate.probe_state || ':' ||
                  COALESCE(candidate.rejection_reason, '')
              )
        )
    ) AS latest ON proxy.effective_pattern = 'erc1967'
), uups_overlay AS (
    SELECT proxy.chain_id, proxy.proxy_address,
           probe.verification_job_id AS implementation_artifact_job_id,
           probe.uups_generation_id
    FROM resolved_epoch AS proxy
    JOIN latest_uups_probe AS probe
      ON probe.chain_id = proxy.chain_id
     AND probe.proxy_address = proxy.proxy_address
    JOIN verified_contract_proxy_artifacts AS artifact
      ON artifact.verification_job_id = probe.verification_job_id
     AND artifact.chain_id = probe.chain_id
     AND artifact.address = proxy.effective_implementation
     AND artifact.code_hash = proxy.effective_implementation_hash
     AND artifact.artifact_kind = 'uups_implementation'
     AND artifact.standard_version = '5.6.1'
     AND artifact.runtime_immutable_address = proxy.effective_implementation
    JOIN verified_contracts AS verified
      ON verified.chain_id = artifact.chain_id
     AND verified.address = artifact.address
     AND verified.code_hash = artifact.code_hash
     AND verified.valid_from_block = artifact.valid_from_block
     AND verified.verification_job_id = artifact.verification_job_id
     AND verified.request_digest = artifact.request_digest
    JOIN verification_jobs AS artifact_job
      ON artifact_job.id = artifact.verification_job_id
     AND artifact_job.kind = 'address'
     AND artifact_job.chain_id = artifact.chain_id
     AND artifact_job.address = artifact.address
     AND artifact_job.code_hash = artifact.code_hash
     AND artifact_job.status = 'succeeded'
    JOIN blocks AS artifact_block
      ON artifact_block.chain_id = artifact_job.chain_id
     AND artifact_block.hash = artifact_job.block_hash
    JOIN canonical_blocks AS artifact_canonical
      ON artifact_canonical.chain_id = artifact_block.chain_id
     AND artifact_canonical.number = artifact_block.number
     AND artifact_canonical.block_hash = artifact_block.hash
    WHERE proxy.effective_pattern = 'erc1967'
      AND probe.probe_state = 'compatible'
      AND probe.standard_version = '5.6.1'
      AND probe.proxiable_uuid = decode(
          '360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc',
          'hex'
      )
      AND probe.upgrade_interface_version = '5.0.0'
      AND probe.block_number >= proxy.implementation_epoch_block
      AND artifact.valid_from_block >= proxy.implementation_epoch_block
      AND artifact.valid_from_block <= proxy.context_number
      AND (verified.valid_to_block IS NULL OR
           verified.valid_to_block >= proxy.context_number)
      AND proxy_interaction_coverage_contains(
          proxy.chain_id,
          probe.block_number, probe.block_hash,
          proxy.context_number, proxy.context_hash
      )
), effective_proxy AS (
    SELECT proxy.*,
           CASE WHEN overlay.uups_generation_id IS NOT NULL THEN 'uups'
                ELSE proxy.effective_pattern END AS current_pattern,
           CASE WHEN overlay.uups_generation_id IS NOT NULL
                THEN overlay.implementation_artifact_job_id
                ELSE NULL::uuid END AS current_implementation_artifact_job_id,
           overlay.uups_generation_id
    FROM resolved_epoch AS proxy
    LEFT JOIN uups_overlay AS overlay
      ON overlay.chain_id = proxy.chain_id
     AND overlay.proxy_address = proxy.proxy_address
), latest_beacon AS (
    SELECT observation.*, proxy.context_number AS proxy_context_number
    FROM effective_proxy AS proxy
    JOIN LATERAL (
        SELECT observation.*
        FROM beacon_implementation_observations AS observation
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = observation.chain_id
         AND canonical.number = observation.block_number
         AND canonical.block_hash = observation.block_hash
        WHERE observation.chain_id = proxy.chain_id
          AND observation.beacon_address = proxy.effective_beacon
          AND observation.beacon_code_hash = proxy.effective_beacon_hash
          AND observation.stage_version = 2
          AND observation.canonical
          AND observation.confidence IN ('verified', 'high')
          AND observation.block_number <= proxy.context_number
        ORDER BY observation.block_number DESC, observation.block_hash DESC
        LIMIT 1
    ) AS observation ON proxy.current_pattern = 'beacon'
), published_beacon AS (
    SELECT beacon.*, generation.id AS beacon_generation_id,
           generation.durable_job_id AS beacon_durable_job_id,
           generation.job_generation AS beacon_job_generation
    FROM latest_beacon AS beacon
    JOIN LATERAL (
        SELECT witness.id, witness.durable_job_id, witness.job_generation
        FROM beacon_observation_generations AS witness
        JOIN published_block_stage_results AS published
          ON published.chain_id = witness.chain_id
         AND published.block_hash = witness.observation_block_hash
         AND published.stage = 'proxy'
         AND published.stage_version = witness.observation_stage_version
         AND published.durable_job_id = witness.durable_job_id
         AND published.job_generation = witness.job_generation
         AND published.state = 'complete'
        WHERE witness.chain_id = beacon.chain_id
          AND witness.beacon_address = beacon.beacon_address
          AND witness.observation_block_hash = beacon.block_hash
          AND witness.observation_stage_version = beacon.stage_version
        ORDER BY witness.id DESC
        LIMIT 1
    ) AS generation ON TRUE
), unshadowed_beacon AS (
    SELECT beacon.*
    FROM published_beacon AS beacon
    WHERE NOT EXISTS (
        SELECT 1
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
        WHERE evidence.chain_id = beacon.chain_id
          AND evidence.address = beacon.beacon_address
          AND evidence.code_hash = beacon.beacon_code_hash
          AND evidence.candidate_kind = 'beacon'
          AND evidence.stage_version = beacon.stage_version
          AND evidence.canonical = TRUE
          AND evidence.block_number <= beacon.proxy_context_number
          AND (
              evidence.block_number > beacon.block_number OR (
                  evidence.block_number = beacon.block_number
                  AND evidence.block_hash = beacon.block_hash
                  AND evidence.durable_job_id = beacon.beacon_durable_job_id
                  AND evidence.job_generation >= beacon.beacon_job_generation
              )
          )
    )
), current_proxy AS (
    SELECT proxy.block_number, proxy.block_hash, proxy.proxy_code_hash,
           proxy.effective_kind AS proxy_kind,
           proxy.current_pattern AS proxy_pattern,
           proxy.effective_standard AS standard_version,
           CASE WHEN proxy.current_pattern = 'beacon'
                THEN beacon.implementation_address
                ELSE proxy.effective_implementation END AS implementation_address,
           CASE WHEN proxy.current_pattern = 'beacon'
                THEN beacon.implementation_code_hash
                ELSE proxy.effective_implementation_hash END AS implementation_code_hash,
           proxy.effective_admin AS admin_address,
           proxy.effective_admin_hash AS admin_code_hash,
           proxy.effective_beacon AS beacon_address,
           proxy.effective_beacon_hash AS beacon_code_hash,
           proxy.observation_generation_id, proxy.artifact_resolution_id,
           proxy.proxy_artifact_job_id,
           proxy.current_implementation_artifact_job_id AS implementation_artifact_job_id,
           beacon.beacon_generation_id, proxy.uups_generation_id,
           proxy.context_number,
           proxy.context_hash
    FROM effective_proxy AS proxy
    LEFT JOIN unshadowed_beacon AS beacon
      ON proxy.current_pattern = 'beacon'
     AND beacon.beacon_address = proxy.effective_beacon
    WHERE proxy.current_pattern <> 'beacon' OR beacon.beacon_generation_id IS NOT NULL
), expected_identity(address, code_hash, epoch_block) AS (
    SELECT DISTINCT identity.address, identity.code_hash,
           COALESCE(code_epoch.block_number, 0::numeric)
    FROM current_proxy
    CROSS JOIN LATERAL (VALUES
        ($2::bytea, $3::bytea),
        ($6::bytea, $7::bytea),
        ($10::bytea, $11::bytea),
        ($12::bytea, $13::bytea),
        ($15::bytea, $16::bytea)
    ) AS identity(address, code_hash)
    LEFT JOIN LATERAL (
        SELECT max(change.block_number) AS block_number
        FROM transaction_state_changes AS change
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = change.chain_id
         AND canonical.number = change.block_number
         AND canonical.block_hash = change.block_hash
        WHERE change.chain_id = $1::numeric
          AND change.address = identity.address
          AND change.field_kind = 'code'
          AND change.canonical = TRUE
          AND change.block_number <= current_proxy.context_number
          AND lower(change.before_value) IS DISTINCT FROM
              lower(change.after_value)
    ) AS code_epoch ON TRUE
    WHERE identity.address IS NOT NULL
), current_identity AS (
    SELECT expected.address, expected.code_hash,
           current_code.code_hash AS current_code_hash
    FROM canonical_tip AS tip
    CROSS JOIN expected_identity AS expected
    LEFT JOIN LATERAL (
        SELECT observation.code_hash
        FROM contract_code_observations AS observation
        JOIN canonical_blocks AS canonical
          ON canonical.chain_id = observation.chain_id
         AND canonical.number = observation.block_number
         AND canonical.block_hash = observation.block_hash
        WHERE observation.chain_id = $1::numeric
          AND observation.address = expected.address
          AND observation.canonical = TRUE
          AND observation.block_number <= tip.number
        ORDER BY observation.block_number DESC,
                 observation.observed_at DESC,
                 observation.code_hash DESC
        LIMIT 1
    ) AS current_code ON TRUE
), required_publication(address, code_hash, epoch_block) AS (
    SELECT publication.address, publication.code_hash, identity.epoch_block
    FROM (VALUES
        ($2::bytea, $3::bytea, $8::text <> 'clone'),
        ($6::bytea, $7::bytea, TRUE),
        ($15::bytea, $16::bytea, $14::text <> 'none')
    ) AS publication(address, code_hash, required)
    JOIN expected_identity AS identity
      ON identity.address = publication.address
     AND identity.code_hash = publication.code_hash
    WHERE publication.required
)
SELECT current_proxy.block_number::text
     , current_proxy.observation_generation_id
     , current_proxy.artifact_resolution_id
     , current_proxy.beacon_generation_id
     , current_proxy.uups_generation_id
     , current_proxy.context_number::text
     , current_proxy.context_hash
FROM current_proxy
JOIN submission_context ON TRUE
WHERE current_proxy.proxy_code_hash = $3
  AND current_proxy.block_hash = $4
  AND current_proxy.proxy_kind = $5
  AND current_proxy.implementation_address = $6
  AND current_proxy.implementation_code_hash = $7
  AND current_proxy.proxy_pattern = $8
  AND current_proxy.standard_version IS NOT DISTINCT FROM $9::text
  AND current_proxy.admin_address IS NOT DISTINCT FROM $10::bytea
  AND current_proxy.admin_code_hash IS NOT DISTINCT FROM $11::bytea
  AND current_proxy.beacon_address IS NOT DISTINCT FROM $12::bytea
  AND current_proxy.beacon_code_hash IS NOT DISTINCT FROM $13::bytea
  AND current_proxy.observation_generation_id = $17::bigint
  AND current_proxy.artifact_resolution_id IS NOT DISTINCT FROM $18::bigint
  AND current_proxy.beacon_generation_id IS NOT DISTINCT FROM $19::bigint
  AND current_proxy.uups_generation_id IS NOT DISTINCT FROM $22::bigint
  AND current_proxy.block_number <= submission_context.number
  AND proxy_interaction_coverage_contains(
      $1::numeric,
      current_proxy.block_number, current_proxy.block_hash,
      current_proxy.context_number, current_proxy.context_hash
  )
  AND (
      $14::text = 'none' OR EXISTS (
          SELECT 1
          FROM verified_contract_proxy_artifacts AS artifact
          JOIN verified_contracts AS verified
            ON verified.chain_id = artifact.chain_id
           AND verified.address = artifact.address
           AND verified.code_hash = artifact.code_hash
           AND verified.valid_from_block = artifact.valid_from_block
           AND verified.verification_job_id = artifact.verification_job_id
           AND verified.request_digest = artifact.request_digest
          JOIN expected_identity AS identity
            ON identity.address = artifact.address
           AND identity.code_hash = artifact.code_hash
          WHERE artifact.chain_id = $1::numeric
            AND artifact.address = $15
            AND artifact.code_hash = $16
            AND artifact.standard_version = '5.6.1'
            AND artifact.artifact_kind = CASE $14::text
                WHEN 'proxy_admin' THEN 'proxy_admin'
                WHEN 'upgradeable_beacon' THEN 'upgradeable_beacon'
            END
            AND artifact.valid_from_block >= identity.epoch_block
            AND artifact.valid_from_block <= current_proxy.context_number
            AND (verified.valid_to_block IS NULL
                 OR verified.valid_to_block >= current_proxy.context_number)
      )
  )
  AND (
      current_proxy.proxy_pattern = 'clone' OR EXISTS (
          SELECT 1
          FROM verified_contract_proxy_artifacts AS artifact
          JOIN verified_contracts AS verified
            ON verified.chain_id = artifact.chain_id
           AND verified.address = artifact.address
           AND verified.code_hash = artifact.code_hash
           AND verified.valid_from_block = artifact.valid_from_block
           AND verified.verification_job_id = artifact.verification_job_id
           AND verified.request_digest = artifact.request_digest
          JOIN expected_identity AS identity
            ON identity.address = artifact.address
           AND identity.code_hash = artifact.code_hash
          WHERE artifact.verification_job_id = current_proxy.proxy_artifact_job_id
            AND artifact.chain_id = $1::numeric
            AND artifact.address = $2
            AND artifact.code_hash = $3
            AND artifact.standard_version = '5.6.1'
            AND artifact.artifact_kind = CASE current_proxy.proxy_pattern
                WHEN 'erc1967' THEN 'erc1967_proxy'
                WHEN 'transparent' THEN 'transparent_proxy'
                WHEN 'uups' THEN 'erc1967_proxy'
                WHEN 'beacon' THEN 'beacon_proxy'
            END
            AND artifact.valid_from_block >= identity.epoch_block
            AND artifact.valid_from_block <= current_proxy.context_number
            AND (verified.valid_to_block IS NULL
                 OR verified.valid_to_block >= current_proxy.context_number)
      )
  )
  AND (
      current_proxy.proxy_pattern <> 'uups' OR EXISTS (
          SELECT 1
          FROM verified_contract_proxy_artifacts AS artifact
          JOIN verified_contracts AS verified
            ON verified.chain_id = artifact.chain_id
           AND verified.address = artifact.address
           AND verified.code_hash = artifact.code_hash
           AND verified.valid_from_block = artifact.valid_from_block
           AND verified.verification_job_id = artifact.verification_job_id
           AND verified.request_digest = artifact.request_digest
          JOIN expected_identity AS identity
            ON identity.address = artifact.address
           AND identity.code_hash = artifact.code_hash
          WHERE artifact.verification_job_id =
                current_proxy.implementation_artifact_job_id
            AND artifact.chain_id = $1::numeric
            AND artifact.address = $6
            AND artifact.code_hash = $7
            AND artifact.standard_version = '5.6.1'
            AND artifact.artifact_kind = 'uups_implementation'
            AND artifact.valid_from_block >= identity.epoch_block
            AND artifact.valid_from_block <= current_proxy.context_number
            AND (verified.valid_to_block IS NULL
                 OR verified.valid_to_block >= current_proxy.context_number)
      )
  )
  AND NOT EXISTS (
      SELECT 1
      FROM current_identity AS identity
      WHERE identity.current_code_hash IS DISTINCT FROM identity.code_hash
  )
  AND NOT EXISTS (
      SELECT 1
      FROM expected_identity AS identity
      JOIN contract_code_observations AS observation
        ON observation.chain_id = $1::numeric
       AND observation.address = identity.address
       AND observation.canonical = TRUE
      JOIN canonical_blocks AS canonical
        ON canonical.chain_id = observation.chain_id
       AND canonical.number = observation.block_number
       AND canonical.block_hash = observation.block_hash
      WHERE observation.block_number > submission_context.number
        AND observation.block_number <= current_proxy.context_number
        AND observation.code_hash IS DISTINCT FROM identity.code_hash
  )
  AND NOT EXISTS (
      SELECT 1
      FROM expected_identity AS identity
      JOIN transaction_state_changes AS change
        ON change.chain_id = $1::numeric
       AND change.address = identity.address
       AND change.field_kind = 'code'
       AND change.canonical = TRUE
      JOIN canonical_blocks AS canonical
        ON canonical.chain_id = change.chain_id
       AND canonical.number = change.block_number
       AND canonical.block_hash = change.block_hash
      WHERE change.block_number > submission_context.number
        AND change.block_number <= current_proxy.context_number
        AND lower(change.before_value) IS DISTINCT FROM
            lower(change.after_value)
  )
  AND NOT EXISTS (
      SELECT 1
      FROM required_publication AS publication
      WHERE NOT EXISTS (
          SELECT 1
          FROM verified_contracts AS verified
          WHERE verified.chain_id = $1::numeric
            AND verified.address = publication.address
            AND verified.code_hash = publication.code_hash
            AND verified.valid_from_block >= publication.epoch_block
            AND verified.valid_from_block <= current_proxy.context_number
            AND (verified.valid_to_block IS NULL
                 OR verified.valid_to_block >= current_proxy.context_number)
      )
  )`
