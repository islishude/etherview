package verify

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func (repository *PostgresRepository) SubmitV2(
	ctx context.Context,
	request SubmissionV2,
	requiresHardIsolation bool,
) (VerificationJob, bool, error) {
	encoded, err := json.Marshal(request)
	if err != nil || len(encoded) > repository.options.MaxRequestBytes {
		return VerificationJob{}, false, errors.New("v2 verification request exceeds configured bounds")
	}
	digest := sha256.Sum256(append([]byte("etherview:verification-request:v2\x00"), encoded...))
	id, err := randomUUID(repository.random)
	if err != nil {
		return VerificationJob{}, false, err
	}
	var language, version, platform any
	var catalogLanguage any
	var generation any
	var compilerDigest, runnerDigest any
	if request.Kind != JobSourcify && request.Kind != JobSourcifyFromEtherscan {
		if !validCompilerPlatform(request.CompilerPlatform) {
			return VerificationJob{}, false, errors.New("v2 compiler platform is invalid")
		}
		language, version, platform, generation = request.Language, request.CompilerVersion,
			request.CompilerPlatform, request.CatalogGenerationID
		catalogLanguage = request.Language
		if request.Language == LanguageYul {
			catalogLanguage = LanguageSolidity
		}
		decoded, decodeErr := hex.DecodeString(request.CompilerDigest)
		if decodeErr != nil || len(decoded) != sha256.Size {
			return VerificationJob{}, false, errors.New("v2 compiler digest is invalid")
		}
		compilerDigest = decoded
		if request.RunnerDigest != "" {
			decodedRunner, runnerErr := hex.DecodeString(request.RunnerDigest)
			if runnerErr != nil || len(decodedRunner) != sha256.Size {
				return VerificationJob{}, false, errors.New("v2 runner digest is invalid")
			}
			runnerDigest = decodedRunner
		}
	}
	var chainID, address, codeHash, blockHash any
	if request.Kind == JobAddress {
		chainID = strconv.FormatUint(request.Target.ChainID, 10)
		address, _ = decodeFixedHex(request.Target.Address, 20)
		codeHash, _ = decodeFixedHex(request.Target.CodeHash, 32)
		blockHash, _ = decodeFixedHex(request.Target.AtBlockHash, 32)
	}
	job, err := repository.scanV2Job(repository.db.QueryRowContext(ctx, `
		INSERT INTO verification_jobs (
			id, kind, language, catalog_language, compiler_version, compiler_platform,
			catalog_generation_id,
			compiler_digest, runner_digest, chain_id, address, code_hash, block_hash,
			request, request_payload, request_digest, requires_hard_isolation, max_attempts
		) VALUES (
			$1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10::numeric, $11, $12, $13,
			$14::jsonb, $15, $16, $17, $18
		)
		ON CONFLICT (request_digest) WHERE status IN ('queued', 'running', 'succeeded')
		DO NOTHING
		RETURNING `+v2VerificationJobColumns,
		id, request.Kind, language, catalogLanguage, version, platform, generation,
		compilerDigest, runnerDigest,
		chainID, address, codeHash, blockHash, string(encoded), encoded, digest[:],
		requiresHardIsolation, repository.options.MaxAttempts,
	))
	created := err == nil
	if errors.Is(err, sql.ErrNoRows) {
		job, err = repository.scanV2Job(repository.db.QueryRowContext(ctx, `
			SELECT `+v2VerificationJobColumns+`
			FROM verification_jobs
			WHERE request_digest = $1 AND status IN ('queued', 'running', 'succeeded')
			ORDER BY created_at, id LIMIT 1
		`, digest[:]))
	}
	if err != nil {
		return VerificationJob{}, false, fmt.Errorf("submit v2 verification job: %w", err)
	}
	return job, created, nil
}

func (repository *PostgresRepository) Claim(
	ctx context.Context,
	workerID string,
	leaseFor time.Duration,
) (VerificationLease, bool, error) {
	if strings.TrimSpace(workerID) == "" || len(workerID) > 128 {
		return VerificationLease{}, false, errors.New("verification worker ID is invalid")
	}
	microseconds, err := positiveMicroseconds(leaseFor)
	if err != nil {
		return VerificationLease{}, false, err
	}
	token, err := randomToken(repository.random)
	if err != nil {
		return VerificationLease{}, false, err
	}
	job, err := repository.scanV2Job(repository.db.QueryRowContext(ctx, `
		WITH exhausted AS (
			UPDATE verification_jobs
			SET status = 'failed', error_code = 'attempts_exhausted',
			    leased_by = NULL, lease_token = NULL, lease_expires_at = NULL,
			    updated_at = clock_timestamp()
			WHERE id = (
				SELECT id FROM verification_jobs
				WHERE (status = 'queued' OR (status = 'running' AND lease_expires_at <= clock_timestamp()))
				  AND attempt_count >= max_attempts
				ORDER BY created_at, id FOR UPDATE SKIP LOCKED LIMIT 1
			)
			RETURNING id
		), candidate AS (
			SELECT id FROM verification_jobs
			WHERE (status = 'queued' OR (status = 'running' AND lease_expires_at <= clock_timestamp()))
			  AND attempt_count < max_attempts
			  AND NOT EXISTS (SELECT 1 FROM exhausted WHERE exhausted.id = verification_jobs.id)
			ORDER BY created_at, id FOR UPDATE SKIP LOCKED LIMIT 1
		)
		UPDATE verification_jobs AS job
		SET status = 'running', leased_by = $1, lease_token = $2,
		    lease_expires_at = clock_timestamp() + ($3 * INTERVAL '1 microsecond'),
		    attempt_count = job.attempt_count + 1, updated_at = clock_timestamp()
		FROM candidate WHERE job.id = candidate.id
		RETURNING `+v2ClaimedVerificationJobColumns,
		workerID, token, microseconds,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return VerificationLease{}, false, nil
	}
	if err != nil {
		return VerificationLease{}, false, err
	}
	return VerificationLease{Job: job, Token: token}, true, nil
}

func (repository *PostgresRepository) BindCompiler(
	ctx context.Context,
	lease VerificationLease,
	provenance CompilerProvenance,
) error {
	if err := validateVerificationLease(lease); err != nil {
		return err
	}
	if lease.Job.RequestV2 == nil || !provenance.valid() {
		return errors.New("v2 compiler binding is invalid")
	}
	request := lease.Job.RequestV2
	if request.CatalogGenerationID != provenance.CatalogGeneration ||
		request.CompilerDigest != hex.EncodeToString(provenance.Digest[:]) ||
		(request.RunnerDigest != "" && request.RunnerDigest != hex.EncodeToString(provenance.RunnerDigest[:])) ||
		(lease.Job.RequiresHardIsolation && !provenance.HardIsolated) {
		return ErrCompilerProvenanceConflict
	}
	var exists bool
	err := repository.db.QueryRowContext(ctx, `
		SELECT TRUE FROM verification_jobs
		WHERE id = $1::uuid AND status = 'running' AND lease_token = $2
		  AND lease_expires_at > clock_timestamp()
	`, lease.Job.ID, lease.Token).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	return err
}

func (repository *PostgresRepository) CompleteV2(
	ctx context.Context,
	lease VerificationLease,
	outcomeKind string,
	outcome json.RawMessage,
) error {
	if err := validateVerificationLease(lease); err != nil {
		return err
	}
	if !jsonObject(outcome) || len(outcome) > repository.options.MaxResultBytes {
		return errors.New("v2 verification outcome is invalid")
	}
	switch outcomeKind {
	case "compilation_failure", "verification_failure", "verification_success", "batch_results", "sourcify_success":
	default:
		return errors.New("v2 verification outcome kind is invalid")
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
	resultFields, err := decodeV2ResultFields(outcomeKind, outcome)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE verification_jobs
		SET status = 'succeeded', outcome_kind = $3, outcome = $4::jsonb,
		    error_code = NULL, leased_by = NULL, lease_token = NULL,
		    lease_expires_at = NULL, updated_at = clock_timestamp()
		WHERE id = $1::uuid AND lease_token = $2
	`, job.ID, lease.Token, outcomeKind, string(outcome)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO verification_results (
			job_id, request_digest, outcome_kind, outcome, file_name, contract_name,
			language, compiler_version, match_type, abi, sources, settings,
			compilation_artifacts, creation_code_artifacts, runtime_code_artifacts,
			constructor_arguments, libraries, is_blueprint
		) VALUES (
			$1::uuid, $2, $3, $4::jsonb, $5, $6, $7, $8, $9, $10::jsonb,
			$11::jsonb, $12::jsonb, $13::jsonb, $14::jsonb, $15::jsonb,
			$16, $17::jsonb, $18
		)
	`, job.ID, job.RequestDigest[:], outcomeKind, string(outcome),
		resultFields.FileName, resultFields.ContractName, resultFields.Language,
		resultFields.CompilerVersion, resultFields.MatchType, resultFields.ABI,
		resultFields.Sources, resultFields.Settings, resultFields.CompilationArtifacts,
		resultFields.CreationArtifacts, resultFields.RuntimeArtifacts,
		resultFields.ConstructorArguments, resultFields.Libraries, resultFields.Blueprint,
	); err != nil {
		return err
	}
	if job.Kind == JobAddress && outcomeKind == "verification_success" {
		if resultFields.RuntimeMatch == "" {
			return errors.New("address verification success lacks a runtime match")
		}
		blockNumber, err := canonicalV2Target(ctx, tx, job.RequestV2.Target)
		if err != nil {
			return ErrTargetNotCanonical
		}
		address, _ := decodeFixedHex(job.RequestV2.Target.Address, 20)
		codeHash, _ := decodeFixedHex(job.RequestV2.Target.CodeHash, 32)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO verified_contracts (
				chain_id, address, code_hash, valid_from_block, verification_job_id,
				request_digest, file_name, contract_name, language, compiler_version,
				match_type, abi, sources, settings, compilation_artifacts,
				creation_code_artifacts, runtime_code_artifacts, constructor_arguments,
				libraries, is_blueprint
			) VALUES (
				$1::numeric, $2, $3, $4::numeric, $5::uuid, $6, $7, $8, $9, $10,
				$11, $12::jsonb, $13::jsonb, $14::jsonb, $15::jsonb, $16::jsonb,
				$17::jsonb, $18, $19::jsonb, $20
			)
		`, strconv.FormatUint(job.RequestV2.Target.ChainID, 10), address, codeHash,
			strconv.FormatUint(blockNumber, 10), job.ID, job.RequestDigest[:],
			resultFields.FileName, resultFields.ContractName, resultFields.Language,
			resultFields.CompilerVersion, resultFields.RuntimeMatch, resultFields.ABI,
			resultFields.Sources, resultFields.Settings, resultFields.CompilationArtifacts,
			resultFields.CreationArtifacts, resultFields.RuntimeArtifacts,
			resultFields.ConstructorArguments, resultFields.Libraries, resultFields.Blueprint,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (repository *PostgresRepository) Complete(
	context.Context,
	VerificationLease,
	Completion,
) error {
	return errors.New("legacy verification completion is disabled")
}

func (repository *PostgresRepository) Fail(
	ctx context.Context,
	lease VerificationLease,
	code ErrorCode,
) error {
	if !code.valid() {
		return errors.New("verification failure code is invalid")
	}
	result, err := repository.db.ExecContext(ctx, `
		UPDATE verification_jobs
		SET status = 'failed', outcome_kind = NULL, outcome = NULL, error_code = $3,
		    leased_by = NULL, lease_token = NULL, lease_expires_at = NULL,
		    updated_at = clock_timestamp()
		WHERE id = $1::uuid AND status = 'running' AND lease_token = $2
		  AND lease_expires_at > clock_timestamp()
	`, lease.Job.ID, lease.Token, code)
	if err != nil {
		return err
	}
	return requireVerificationLease(result)
}

func (repository *PostgresRepository) Job(ctx context.Context, id string) (VerificationJob, bool, error) {
	if !validUUID(id) {
		return VerificationJob{}, false, errors.New("verification job ID is invalid")
	}
	job, err := repository.scanV2Job(repository.db.QueryRowContext(ctx, `
		SELECT `+v2VerificationJobColumns+` FROM verification_jobs WHERE id = $1::uuid
	`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return VerificationJob{}, false, nil
	}
	if err != nil {
		return VerificationJob{}, false, fmt.Errorf("query v2 verification job: %w", err)
	}
	return job, true, nil
}

func (repository *PostgresRepository) scanV2Job(row rowScanner) (VerificationJob, error) {
	var job VerificationJob
	var payload, digest []byte
	var outcomeKind, outcome, errorCode sql.NullString
	var language, version, platform sql.NullString
	var generation sql.NullInt64
	var compilerDigest, runnerDigest []byte
	if err := row.Scan(
		&job.ID, &job.Kind, &language, &version, &platform, &generation,
		&compilerDigest, &runnerDigest, &payload, &digest,
		&job.RequiresHardIsolation, &job.Status, &outcomeKind, &outcome, &errorCode,
		&job.AttemptCount, &job.MaxAttempts, &job.CreatedAt, &job.UpdatedAt,
	); err != nil {
		return VerificationJob{}, err
	}
	if len(payload) > repository.options.MaxRequestBytes ||
		json.Unmarshal(payload, &job.RequestV2) != nil || job.RequestV2 == nil ||
		len(digest) != sha256.Size {
		return VerificationJob{}, errors.New("stored v2 verification job is invalid")
	}
	copy(job.RequestDigest[:], digest)
	if outcome.Valid {
		job.Outcome = json.RawMessage(outcome.String)
	}
	if errorCode.Valid {
		job.ErrorCode = ErrorCode(errorCode.String)
	}
	if language.Valid {
		job.RequestV2.Language = Language(language.String)
		job.RequestV2.CompilerVersion = version.String
		job.RequestV2.CompilerPlatform = platform.String
		job.RequestV2.CatalogGenerationID = generation.Int64
		job.RequestV2.CompilerDigest = hex.EncodeToString(compilerDigest)
		job.RequestV2.RunnerDigest = hex.EncodeToString(runnerDigest)
	}
	return job, nil
}

func (repository *PostgresRepository) VerifiedContract(
	ctx context.Context,
	chainID uint64,
	addressHex string,
	codeHashHex string,
) (VerifiedContract, bool, error) {
	address, err := decodeFixedHex(addressHex, 20)
	if err != nil {
		return VerifiedContract{}, false, err
	}
	codeHash, err := decodeFixedHex(codeHashHex, 32)
	if err != nil {
		return VerifiedContract{}, false, err
	}
	var contract VerifiedContract
	var addressBytes, codeHashBytes []byte
	var validFrom string
	var validTo sql.NullString
	var abi, sources, settings, compilation, creationArtifacts, runtimeArtifacts, libraries []byte
	var constructor []byte
	err = repository.db.QueryRowContext(ctx, `
		SELECT chain_id::text, address, code_hash, valid_from_block::text,
		       valid_to_block::text, file_name, contract_name, language,
		       compiler_version, match_type, abi, sources, settings,
		       compilation_artifacts, creation_code_artifacts, runtime_code_artifacts,
		       constructor_arguments, libraries, is_blueprint, created_at
		FROM verified_contracts
		WHERE chain_id = $1::numeric AND address = $2 AND code_hash = $3
		  AND valid_to_block IS NULL
		ORDER BY valid_from_block DESC LIMIT 1
	`, strconv.FormatUint(chainID, 10), address, codeHash).Scan(
		new(string), &addressBytes, &codeHashBytes, &validFrom, &validTo,
		&contract.FileName, &contract.ContractName, &contract.Language,
		&contract.CompilerVersion, &contract.MatchType, &abi, &sources, &settings,
		&compilation, &creationArtifacts, &runtimeArtifacts, &constructor,
		&libraries, &contract.IsBlueprint, &contract.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return VerifiedContract{}, false, nil
	}
	if err != nil {
		return VerifiedContract{}, false, err
	}
	contract.ChainID = chainID
	contract.Address = "0x" + hex.EncodeToString(addressBytes)
	contract.CodeHash = "0x" + hex.EncodeToString(codeHashBytes)
	contract.ValidFromBlock, err = strconv.ParseUint(validFrom, 10, 64)
	if err != nil {
		return VerifiedContract{}, false, errors.New("stored verified block is invalid")
	}
	if validTo.Valid {
		value, parseErr := strconv.ParseUint(validTo.String, 10, 64)
		if parseErr != nil {
			return VerifiedContract{}, false, parseErr
		}
		contract.ValidToBlock = &value
	}
	contract.ABI, contract.Sources, contract.Settings = abi, sources, settings
	contract.CompilationArtifacts = compilation
	contract.CreationCodeArtifacts = creationArtifacts
	contract.RuntimeCodeArtifacts = runtimeArtifacts
	if err := json.Unmarshal(libraries, &contract.Libraries); err != nil {
		return VerifiedContract{}, false, errors.New("stored verified libraries are invalid")
	}
	if len(constructor) > 0 {
		contract.ConstructorArguments = "0x" + hex.EncodeToString(constructor)
	}
	return contract, true, nil
}

const v2VerificationJobColumns = `
id::text, kind, language, compiler_version, compiler_platform, catalog_generation_id,
compiler_digest, runner_digest, request_payload, request_digest,
requires_hard_isolation, status, outcome_kind, outcome, error_code,
attempt_count, max_attempts, created_at, updated_at`

const v2ClaimedVerificationJobColumns = `
job.id::text, job.kind, job.language, job.compiler_version, job.compiler_platform,
job.catalog_generation_id,
job.compiler_digest, job.runner_digest, job.request_payload, job.request_digest,
job.requires_hard_isolation, job.status, job.outcome_kind, job.outcome, job.error_code,
job.attempt_count, job.max_attempts, job.created_at, job.updated_at`

type v2ResultFields struct {
	FileName             any
	ContractName         any
	Language             any
	CompilerVersion      any
	MatchType            any
	RuntimeMatch         string
	ABI                  any
	Sources              any
	Settings             any
	CompilationArtifacts any
	CreationArtifacts    any
	RuntimeArtifacts     any
	ConstructorArguments any
	Libraries            any
	Blueprint            any
}

func decodeV2ResultFields(kind string, outcome json.RawMessage) (v2ResultFields, error) {
	if kind != "verification_success" {
		return v2ResultFields{}, nil
	}
	var success struct {
		FileName        string                    `json:"file_name"`
		ContractName    string                    `json:"contract_name"`
		Language        Language                  `json:"language"`
		CompilerVersion string                    `json:"compiler_version"`
		ABI             json.RawMessage           `json:"abi"`
		Sources         json.RawMessage           `json:"sources"`
		Settings        json.RawMessage           `json:"settings"`
		Compilation     json.RawMessage           `json:"compilation_artifacts"`
		Creation        json.RawMessage           `json:"creation_code_artifacts"`
		Runtime         json.RawMessage           `json:"runtime_code_artifacts"`
		Constructor     string                    `json:"constructor_arguments"`
		Libraries       json.RawMessage           `json:"libraries"`
		Blueprint       bool                      `json:"is_blueprint"`
		CreationMatch   *VerificationMatchDetails `json:"creation_match"`
		RuntimeMatch    *VerificationMatchDetails `json:"runtime_match"`
	}
	if err := json.Unmarshal(outcome, &success); err != nil ||
		success.FileName == "" || success.ContractName == "" ||
		!jsonObject(success.Sources) || !jsonObject(success.Settings) ||
		!jsonObject(success.Compilation) || !jsonObject(success.Creation) ||
		!jsonObject(success.Runtime) || !jsonObject(success.Libraries) {
		return v2ResultFields{}, errors.New("verification success outcome is incomplete")
	}
	matchType := VerificationMatchPartial
	runtimeMatch := ""
	if success.CreationMatch != nil {
		matchType = success.CreationMatch.MatchType
	}
	if success.RuntimeMatch != nil {
		matchType = success.RuntimeMatch.MatchType
		runtimeMatch = string(success.RuntimeMatch.MatchType)
	}
	if matchType != VerificationMatchFull && matchType != VerificationMatchPartial {
		return v2ResultFields{}, errors.New("verification success match type is invalid")
	}
	var constructor any
	if success.Constructor != "" {
		decoded, err := decodeBytecode(success.Constructor)
		if err != nil {
			return v2ResultFields{}, errors.New("verification constructor arguments are invalid")
		}
		constructor = decoded
	}
	var abi any
	if len(success.ABI) > 0 {
		if !jsonArray(success.ABI) {
			return v2ResultFields{}, errors.New("verification ABI is invalid")
		}
		abi = string(success.ABI)
	}
	return v2ResultFields{
		FileName: success.FileName, ContractName: success.ContractName,
		Language: success.Language, CompilerVersion: success.CompilerVersion,
		MatchType: matchType, RuntimeMatch: runtimeMatch, ABI: abi,
		Sources: string(success.Sources), Settings: string(success.Settings),
		CompilationArtifacts: string(success.Compilation),
		CreationArtifacts:    string(success.Creation), RuntimeArtifacts: string(success.Runtime),
		ConstructorArguments: constructor, Libraries: string(success.Libraries),
		Blueprint: success.Blueprint,
	}, nil
}

func canonicalV2Target(ctx context.Context, tx *sql.Tx, target *VerificationTarget) (uint64, error) {
	if target == nil {
		return 0, ErrTargetNotCanonical
	}
	address, _ := decodeFixedHex(target.Address, 20)
	codeHash, _ := decodeFixedHex(target.CodeHash, 32)
	blockHash, _ := decodeFixedHex(target.AtBlockHash, 32)
	var blockNumber string
	if err := tx.QueryRowContext(ctx, verificationCanonicalTargetSQL,
		strconv.FormatUint(target.ChainID, 10), address, codeHash, blockHash,
	).Scan(&blockNumber); err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(blockNumber, 10, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}
