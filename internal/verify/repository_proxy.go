package verify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/islishude/etherview/internal/db/gen"
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

	job, err := repository.scanV2Job(tx.QueryRowContext(ctx, dbgen.VerifyV2LockRunningJob, lease.Job.ID, lease.Token))
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
	if _, err := tx.ExecContext(ctx, dbgen.VerifyInlineCompleteProxyV2Statement1, chainID); err != nil {
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
	err = tx.QueryRowContext(ctx, dbgen.VerifyLegacyProxyVerificationCurrentTarget, chainID,
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
	if _, err := tx.ExecContext(ctx, dbgen.VerifyInlineCompleteProxyV2Statement2, job.ID, lease.Token, string(outcome)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, dbgen.VerifyInlineCompleteProxyV2Statement3, job.ID, job.RequestDigest[:], string(outcome)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, dbgen.VerifyInlineCompleteProxyV2Statement4, chainID, proxyAddress, proxyCodeHash,
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
