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

	"github.com/ethereum/go-ethereum/common"
)

func (repository *PostgresRepository) SubmitV2(
	ctx context.Context,
	request SubmissionV2,
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
	var compilerDigest any
	if request.Kind != JobSourcify && request.Kind != JobSourcifyFromEtherscan &&
		request.Kind != JobProxy {
		language, version = request.Language, request.CompilerVersion
		catalogLanguage = request.Language
		if request.Language == LanguageYul {
			catalogLanguage = LanguageSolidity
		}
	}
	var chainID, address, codeHash, blockHash any
	if request.Kind == JobAddress || request.Kind == JobProxy {
		chainID = strconv.FormatUint(request.Target.ChainID, 10)
		address, _ = decodeFixedHex(request.Target.Address, 20)
		codeHash, _ = decodeFixedHex(request.Target.CodeHash, 32)
		blockHash, _ = decodeFixedHex(request.Target.AtBlockHash, 32)
	}
	job, err := repository.scanV2Job(repository.db.QueryRowContext(ctx, `
		INSERT INTO verification_jobs (
			id, kind, language, catalog_language, compiler_version, compiler_platform,
			catalog_generation_id,
			compiler_digest, executor_kind, execution_policy, executor_digest,
			chain_id, address, code_hash, block_hash,
			request, request_payload, request_digest, max_attempts
		) VALUES (
			$1::uuid, $2, $3, $4, $5, $6, $7, $8, NULL, NULL, NULL,
			$9::numeric, $10, $11, $12, $13::jsonb, $14, $15, $16
		)
		ON CONFLICT (request_digest) WHERE status IN ('queued', 'running', 'succeeded')
		DO NOTHING
		RETURNING `+v2VerificationJobColumns,
		id, request.Kind, language, catalogLanguage, version, platform, generation,
		compilerDigest,
		chainID, address, codeHash, blockHash, string(encoded), encoded, digest[:],
		repository.options.MaxAttempts,
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
	return repository.claimRunnable(ctx, workerID, leaseFor, true)
}

func (repository *PostgresRepository) ClaimRunnable(
	ctx context.Context,
	workerID string,
	leaseFor time.Duration,
	compilerAvailable bool,
) (VerificationLease, bool, error) {
	return repository.claimRunnable(ctx, workerID, leaseFor, compilerAvailable)
}

func (repository *PostgresRepository) claimRunnable(
	ctx context.Context,
	workerID string,
	leaseFor time.Duration,
	compilerAvailable bool,
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
				  AND ($4 OR kind IN ('proxy', 'sourcify', 'sourcify_from_etherscan'))
				  AND attempt_count >= max_attempts
				ORDER BY created_at, id FOR UPDATE SKIP LOCKED LIMIT 1
			)
			RETURNING id
		), candidate AS (
			SELECT id FROM verification_jobs
			WHERE (status = 'queued' OR (status = 'running' AND lease_expires_at <= clock_timestamp()))
			  AND ($4 OR kind IN ('proxy', 'sourcify', 'sourcify_from_etherscan'))
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
		workerID, token, microseconds, compilerAvailable,
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
	if provenance.Kind != CompilerSolcJS ||
		provenance.ExecutorKind != SolcJSExecutorKind ||
		provenance.ExecutionPolicy != TrustedSubprocessPolicy ||
		provenance.ExecutorDigest == [sha256.Size]byte{} {
		return ErrCompilerProvenanceConflict
	}
	result, err := repository.db.ExecContext(ctx, `
		UPDATE verification_jobs
		SET compiler_platform = $3, catalog_generation_id = $4,
		    compiler_digest = $5, executor_kind = $6,
		    execution_policy = $7, executor_digest = $8,
		    updated_at = clock_timestamp()
		WHERE id = $1::uuid AND status = 'running' AND lease_token = $2
		  AND lease_expires_at > clock_timestamp()
		  AND (
		    (compiler_platform IS NULL AND catalog_generation_id IS NULL
		     AND compiler_digest IS NULL AND executor_kind IS NULL
		     AND execution_policy IS NULL AND executor_digest IS NULL)
		    OR
		    (compiler_platform = $3 AND catalog_generation_id = $4
		     AND compiler_digest = $5 AND executor_kind = $6
		     AND execution_policy = $7 AND executor_digest = $8)
		  )
	`, lease.Job.ID, lease.Token, provenance.Platform,
		provenance.CatalogGeneration, provenance.Digest[:],
		provenance.ExecutorKind, provenance.ExecutionPolicy,
		provenance.ExecutorDigest[:])
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 1 {
		return nil
	}
	var leaseOwned bool
	err = repository.db.QueryRowContext(ctx, `
		SELECT TRUE FROM verification_jobs
		WHERE id = $1::uuid AND status = 'running' AND lease_token = $2
		  AND lease_expires_at > clock_timestamp()
	`, lease.Job.ID, lease.Token).Scan(&leaseOwned)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	return ErrCompilerProvenanceConflict
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
	var (
		publicationBlockNumber uint64
		publicationAddress     []byte
		publicationCodeHash    []byte
		publicationTarget      common.Address
		authenticatedArtifact  *recognizedProxyArtifact
	)
	if job.Kind == JobAddress && outcomeKind == "verification_success" {
		if resultFields.RuntimeMatch == "" {
			return errors.New("address verification success lacks a runtime match")
		}
		publicationBlockNumber, err = canonicalV2Target(ctx, tx, job.RequestV2.Target)
		if err != nil {
			return ErrTargetNotCanonical
		}
		publicationAddress, _ = decodeFixedHex(job.RequestV2.Target.Address, 20)
		publicationCodeHash, _ = decodeFixedHex(job.RequestV2.Target.CodeHash, 32)
		blockHash, _ := decodeFixedHex(job.RequestV2.Target.AtBlockHash, 32)
		var actualRuntime []byte
		runtimeErr := tx.QueryRowContext(ctx, `
			SELECT code
			FROM contract_code_observations
			WHERE chain_id = $1::numeric AND address = $2
			  AND block_number = $3::numeric AND block_hash = $4
			  AND code_hash = $5 AND canonical = TRUE`,
			strconv.FormatUint(job.RequestV2.Target.ChainID, 10), publicationAddress,
			strconv.FormatUint(publicationBlockNumber, 10), blockHash, publicationCodeHash,
		).Scan(&actualRuntime)
		if runtimeErr != nil && !errors.Is(runtimeErr, sql.ErrNoRows) {
			return fmt.Errorf("load verified runtime for proxy artifact authentication: %w", runtimeErr)
		}
		publicationTarget = common.BytesToAddress(publicationAddress)
		if runtimeErr == nil {
			artifact, recognized := recognizeOpenZeppelin561Artifact(
				outcome, publicationTarget, actualRuntime,
			)
			if recognized {
				if err := validateRecognizedProxyArtifact(artifact); err != nil {
					return err
				}
				authenticatedArtifact = &artifact
			}
		}
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
	artifactKind, artifactVersion, artifactImmutable, artifactManifest :=
		proxyArtifactAttestationValues(authenticatedArtifact)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO verification_results (
			job_id, request_digest, outcome_kind, outcome, file_name, contract_name,
			language, compiler_version, match_type, abi, sources, settings,
			compilation_artifacts, creation_code_artifacts, runtime_code_artifacts,
			constructor_arguments, libraries, is_blueprint,
			proxy_artifact_kind, proxy_standard_version,
			proxy_runtime_immutable_address, proxy_source_manifest_sha256
		) VALUES (
			$1::uuid, $2, $3, $4::jsonb, $5, $6, $7, $8, $9, $10::jsonb,
			$11::jsonb, $12::jsonb, $13::jsonb, $14::jsonb, $15::jsonb,
			$16, $17::jsonb, $18, $19, $20, $21, $22
		)
	`, job.ID, job.RequestDigest[:], outcomeKind, string(outcome),
		resultFields.FileName, resultFields.ContractName, resultFields.Language,
		resultFields.CompilerVersion, resultFields.MatchType, resultFields.ABI,
		resultFields.Sources, resultFields.Settings, resultFields.CompilationArtifacts,
		resultFields.CreationArtifacts, resultFields.RuntimeArtifacts,
		resultFields.ConstructorArguments, resultFields.Libraries, resultFields.Blueprint,
		artifactKind, artifactVersion, artifactImmutable, artifactManifest,
	); err != nil {
		return err
	}
	if job.Kind == JobAddress && outcomeKind == "verification_success" {
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
		`, strconv.FormatUint(job.RequestV2.Target.ChainID, 10), publicationAddress,
			publicationCodeHash, strconv.FormatUint(publicationBlockNumber, 10),
			job.ID, job.RequestDigest[:],
			resultFields.FileName, resultFields.ContractName, resultFields.Language,
			resultFields.CompilerVersion, resultFields.RuntimeMatch, resultFields.ABI,
			resultFields.Sources, resultFields.Settings, resultFields.CompilationArtifacts,
			resultFields.CreationArtifacts, resultFields.RuntimeArtifacts,
			resultFields.ConstructorArguments, resultFields.Libraries, resultFields.Blueprint,
		); err != nil {
			return err
		}
		if authenticatedArtifact != nil {
			var immutable any
			if authenticatedArtifact.RuntimeImmutable != nil {
				immutable = authenticatedArtifact.RuntimeImmutable[:]
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO verified_contract_proxy_artifacts (
					chain_id, address, code_hash, valid_from_block,
					verification_job_id, request_digest, artifact_kind,
					standard_version, runtime_immutable_address,
					source_manifest_sha256
				) VALUES (
					$1::numeric, $2, $3, $4::numeric,
					$5::uuid, $6, $7, $8, $9, $10
				)`, strconv.FormatUint(job.RequestV2.Target.ChainID, 10),
				publicationAddress, publicationCodeHash,
				strconv.FormatUint(publicationBlockNumber, 10),
				job.ID, job.RequestDigest[:], authenticatedArtifact.Kind,
				authenticatedArtifact.StandardVersion, immutable,
				authenticatedArtifact.SourceManifestSHA256[:],
			); err != nil {
				return fmt.Errorf("publish authenticated OpenZeppelin proxy artifact: %w", err)
			}
		}
		if err := repository.requestVerificationProxyReplayTx(
			ctx, tx, job, publicationBlockNumber, publicationTarget,
			authenticatedArtifact,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func proxyArtifactAttestationValues(artifact *recognizedProxyArtifact) (any, any, any, any) {
	if artifact == nil {
		return nil, nil, nil, nil
	}
	var immutable any
	if artifact.RuntimeImmutable != nil {
		immutable = artifact.RuntimeImmutable[:]
	}
	return artifact.Kind, artifact.StandardVersion, immutable, artifact.SourceManifestSHA256[:]
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
	var language, version, platform, executorKind, executionPolicy sql.NullString
	var generation sql.NullInt64
	var compilerDigest, executorDigest []byte
	if err := row.Scan(
		&job.ID, &job.Kind, &language, &version, &platform, &generation,
		&compilerDigest, &executorKind, &executionPolicy, &executorDigest,
		&payload, &digest, &job.Status, &outcomeKind, &outcome, &errorCode,
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
		bound := platform.Valid || generation.Valid || len(compilerDigest) > 0
		if bound {
			if !platform.Valid || !generation.Valid || generation.Int64 <= 0 ||
				len(compilerDigest) != sha256.Size ||
				!executorKind.Valid || !executionPolicy.Valid {
				return VerificationJob{}, errors.New("stored compiler provenance is incomplete")
			}
			job.RequestV2.CompilerPlatform = platform.String
			job.RequestV2.CatalogGenerationID = generation.Int64
			job.RequestV2.CompilerDigest = hex.EncodeToString(compilerDigest)
			job.RequestV2.ExecutorKind = executorKind.String
			job.RequestV2.ExecutionPolicy = executionPolicy.String
			if len(executorDigest) > 0 {
				if len(executorDigest) != sha256.Size {
					return VerificationJob{}, errors.New("stored executor digest is invalid")
				}
				job.RequestV2.ExecutorDigest = hex.EncodeToString(executorDigest)
			}
			provenance := CompilerProvenance{
				CatalogGeneration: generation.Int64,
				Platform:          platform.String,
				ExecutorKind:      executorKind.String,
				ExecutionPolicy:   executionPolicy.String,
			}
			copy(provenance.Digest[:], compilerDigest)
			if len(executorDigest) == sha256.Size {
				copy(provenance.ExecutorDigest[:], executorDigest)
			}
			switch executorKind.String {
			case SolcJSExecutorKind:
				provenance.Kind = CompilerSolcJS
			case "legacy_runner":
				provenance.Kind = CompilerLegacyRunner
			case "legacy_process":
				provenance.Kind = CompilerLegacyProcess
			default:
				return VerificationJob{}, errors.New("stored executor kind is invalid")
			}
			if !provenance.valid() {
				return VerificationJob{}, errors.New("stored compiler provenance is invalid")
			}
			job.Compiler = &provenance
		}
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
	var creationMatch, runtimeMatch []byte
	var constructor []byte
	err = repository.db.QueryRowContext(ctx, verifiedContractV2SQL,
		strconv.FormatUint(chainID, 10), address, codeHash).Scan(
		new(string), &addressBytes, &codeHashBytes, &validFrom, &validTo,
		&contract.FileName, &contract.ContractName, &contract.Language,
		&contract.CompilerVersion, &contract.MatchType, &abi, &sources, &settings,
		&compilation, &creationArtifacts, &runtimeArtifacts, &creationMatch,
		&runtimeMatch, &constructor, &libraries, &contract.IsBlueprint,
		&contract.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return VerifiedContract{}, false, nil
	}
	if err != nil {
		return VerifiedContract{}, false, err
	}
	contract.CreationMatch, err = decodeStoredVerificationMatch(creationMatch)
	if err != nil {
		return VerifiedContract{}, false, errors.New("stored creation match is invalid")
	}
	contract.RuntimeMatch, err = decodeStoredVerificationMatch(runtimeMatch)
	if err != nil || contract.RuntimeMatch == nil {
		return VerifiedContract{}, false, errors.New("stored runtime match is invalid")
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

func decodeStoredVerificationMatch(value []byte) (*VerificationMatchDetails, error) {
	if len(value) == 0 || string(value) == "null" {
		return nil, nil
	}
	var details VerificationMatchDetails
	if err := json.Unmarshal(value, &details); err != nil ||
		(details.MatchType != VerificationMatchFull &&
			details.MatchType != VerificationMatchPartial) {
		return nil, errors.New("verification match is invalid")
	}
	return &details, nil
}

const verifiedContractV2SQL = `
		SELECT verified.chain_id::text, verified.address, verified.code_hash,
		       verified.valid_from_block::text, verified.valid_to_block::text,
		       verified.file_name, verified.contract_name,
		       verified.language, verified.compiler_version, verified.match_type,
		       verified.abi, verified.sources, verified.settings,
		       verified.compilation_artifacts, verified.creation_code_artifacts,
		       verified.runtime_code_artifacts,
		       result.outcome->'creation_match', result.outcome->'runtime_match',
		       verified.constructor_arguments, verified.libraries,
		       verified.is_blueprint, verified.created_at
		FROM verified_contracts AS verified
		JOIN verification_results AS result
		  ON result.job_id = verified.verification_job_id
		 AND result.request_digest = verified.request_digest
		 AND result.outcome_kind = 'verification_success'
		WHERE verified.chain_id = $1::numeric
		  AND verified.address = $2 AND verified.code_hash = $3
		  AND verified.valid_to_block IS NULL
		ORDER BY (verified.match_type = 'full') DESC,
		         verified.valid_from_block DESC,
		         verified.request_digest ASC,
		         verified.verification_job_id ASC
		LIMIT 1`

const v2VerificationJobColumns = `
id::text, kind, language, compiler_version, compiler_platform, catalog_generation_id,
compiler_digest, executor_kind, execution_policy, executor_digest,
request_payload, request_digest, status, outcome_kind, outcome, error_code,
attempt_count, max_attempts, created_at, updated_at`

const v2ClaimedVerificationJobColumns = `
job.id::text, job.kind, job.language, job.compiler_version, job.compiler_platform,
job.catalog_generation_id,
job.compiler_digest, job.executor_kind, job.execution_policy, job.executor_digest,
job.request_payload, job.request_digest,
job.status, job.outcome_kind, job.outcome, job.error_code,
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
