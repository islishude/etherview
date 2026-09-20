package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	pgx "github.com/jackc/pgx/v5"
	pgtype "github.com/jackc/pgx/v5/pgtype"
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
	tx, err := repository.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer dbaccess.Rollback(ctx, tx)

	job, err := func() (VerificationJob, error) {
		var queryValue0 pgtype.UUID
		if err := queryValue0.Scan(lease.Job.ID); err != nil {
			return VerificationJob{}, err
		}
		row, err := dbgen.New(tx).VerifyV2LockRunningJob(ctx, queryValue0, new(lease.Token))
		if err != nil {
			return VerificationJob{}, err
		}
		return repository.decodeV2Job(dbgen.VerifyV2GetJobRow(row))
	}()
	if errors.Is(err, pgx.ErrNoRows) {
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
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		return dbgen.New(tx).VerifyInlineCompleteProxyV2Statement1(ctx, queryValue0)
	}(); err != nil {
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
	var standardVersion *string
	if request.ProxyTarget.StandardVersion != "" {
		standardVersion = new(request.ProxyTarget.StandardVersion)
	}

	var observationBlock, contextBlock string
	var observationGeneration int64
	var artifactResolution, beaconGeneration, uupsGeneration pgtype.Int8
	var contextHash []byte
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(request.ProxyTarget.SubmissionContextBlockNumber); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).VerifyLegacyProxyVerificationCurrentTarget(ctx, dbgen.VerifyLegacyProxyVerificationCurrentTargetParams{CodeHash: proxyCodeHash, BlockHash: blockHash, ProxyKind: request.ProxyTarget.Kind, ImplementationAddress: implementationAddress, ImplementationCodeHash: implementationCodeHash, ProxyPattern: request.ProxyTarget.Pattern, StandardVersion: standardVersion, AdminAddress: adminAddress, AdminCodeHash: adminCodeHash, BeaconAddress: beaconAddress, BeaconCodeHash: beaconCodeHash, ObservationGenerationID: observationGenerationID, ArtifactResolutionID: artifactResolutionID, BeaconGenerationID: beaconGenerationID, UupsGenerationID: uupsGenerationID, ChainID: queryValue0, ManagementKind: request.ProxyTarget.ManagementKind, ManagementAddress: managementAddress, ManagementCodeHash: managementCodeHash, ProxyAddress: proxyAddress, SubmissionBlockNumber: queryValue1, SubmissionBlockHash: submissionContextHash})
		if err != nil {
			return err
		}
		observationBlock = queryRow.CurrentProxyBlockNumber
		observationGeneration = queryRow.ObservationGenerationID
		artifactResolution = pgtype.Int8{Int64: queryRow.ArtifactResolutionID, Valid: queryRow.ArtifactResolutionPresent}
		var resultValue3 pgtype.Int8
		if queryRow.BeaconGenerationID != nil {
			resultValue3 = pgtype.Int8{Int64: *queryRow.BeaconGenerationID, Valid: true}
		}
		beaconGeneration = resultValue3
		var resultValue5 pgtype.Int8
		if queryRow.UupsGenerationID != nil {
			resultValue5 = pgtype.Int8{Int64: *queryRow.UupsGenerationID, Valid: true}
		}
		uupsGeneration = resultValue5
		contextBlock = queryRow.CurrentProxyContextNumber
		contextHash = queryRow.ContextHash
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
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
	if err := func() error {
		var queryValue0 pgtype.UUID
		if err := queryValue0.Scan(job.ID); err != nil {
			return err
		}
		return dbgen.New(tx).VerifyInlineCompleteProxyV2Statement2(ctx, []byte(string(outcome)), queryValue0, new(lease.Token))
	}(); err != nil {
		return err
	}
	if err := func() error {
		var queryValue0 pgtype.UUID
		if err := queryValue0.Scan(job.ID); err != nil {
			return err
		}
		return dbgen.New(tx).VerifyInlineCompleteProxyV2Statement3(ctx, queryValue0, job.RequestDigest[:], []byte(string(outcome)))
	}(); err != nil {
		return err
	}
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(observationBlock); err != nil {
			return err
		}
		var queryValue2 pgtype.Numeric
		if err := queryValue2.Scan(contextBlock); err != nil {
			return err
		}
		var queryValue3 pgtype.UUID
		if err := queryValue3.Scan(job.ID); err != nil {
			return err
		}
		return dbgen.New(tx).VerifyInlineCompleteProxyV2Statement4(ctx, dbgen.VerifyInlineCompleteProxyV2Statement4Params{ChainID: queryValue0, ProxyAddress: proxyAddress, ProxyCodeHash: proxyCodeHash, ObservationBlockNumber: queryValue1, ObservationBlockHash: blockHash, ProxyKind: request.ProxyTarget.Kind, ProxyPattern: request.ProxyTarget.Pattern, StandardVersion: standardVersion, ImplementationAddress: implementationAddress, ImplementationCodeHash: implementationCodeHash, AdminAddress: adminAddress, AdminCodeHash: adminCodeHash, BeaconAddress: beaconAddress, BeaconCodeHash: beaconCodeHash, ManagementKind: request.ProxyTarget.ManagementKind, ManagementAddress: managementAddress, ManagementCodeHash: managementCodeHash, ObservationGenerationID: observationGeneration, ArtifactResolutionID: nullInt64SQLValue(artifactResolution), BeaconGenerationID: nullInt64SQLValue(beaconGeneration), UupsGenerationID: nullInt64SQLValue(uupsGeneration), ContextBlockNumber: queryValue2, ContextBlockHash: contextHash, VerificationJobID: queryValue3, RequestDigest: job.RequestDigest[:]})
	}(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func nullInt64Outcome(value pgtype.Int8) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func nullInt64SQLValue(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	return new(value.Int64)
}

func proxyIdentitySQLValues(address, codeHash string) ([]byte, []byte) {
	if address == "" {
		return nil, nil
	}
	addressBytes, _ := decodeFixedHex(address, 20)
	codeHashBytes, _ := decodeFixedHex(codeHash, 32)
	return addressBytes, codeHashBytes
}

func proxyGenerationSQLValue(value string) *int64 {
	if value == "" {
		return nil
	}
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return new(parsed)
}

func proxyOutcomeValue(value string) any {
	if value == "" {
		return nil
	}
	return value
}
