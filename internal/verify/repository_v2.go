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
	"github.com/islishude/etherview/internal/cwiaargs"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/verifiedselector"
)

func (repository *PostgresRepository) SubmitV2(
	ctx context.Context,
	request SubmissionV2,
) (VerificationJob, bool, error) {
	normalizeSolidityAnalysisVersion(&request)
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
		if request.Language == LanguageSolidity || request.Language == LanguageYul {
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
	job, err := repository.scanV2Job(repository.db.QueryRowContext(ctx, dbgen.VerifyV2SubmitJob,
		id, request.Kind, language, catalogLanguage, version, platform, generation,
		compilerDigest,
		chainID, address, codeHash, blockHash, string(encoded), encoded, digest[:],
		repository.options.MaxAttempts,
	))
	created := err == nil
	if errors.Is(err, sql.ErrNoRows) {
		job, err = repository.scanV2Job(repository.db.QueryRowContext(ctx, dbgen.VerifyV2FindActiveJobByDigest, digest[:]))
	}
	if err != nil {
		return VerificationJob{}, false, fmt.Errorf("submit v2 verification job: %w", err)
	}
	return job, created, nil
}

func normalizeSolidityAnalysisVersion(request *SubmissionV2) {
	if request != nil && request.Language == LanguageSolidity &&
		request.Kind != JobSourcify && request.Kind != JobSourcifyFromEtherscan &&
		request.Kind != JobProxy {
		request.SolidityAnalysis = cwiaargs.AnalysisVersion
		return
	}
	if request != nil {
		request.SolidityAnalysis = 0
	}
}

func (repository *PostgresRepository) Claim(
	ctx context.Context,
	workerID string,
	leaseFor time.Duration,
) (VerificationLease, bool, error) {
	return repository.claimRunnable(ctx, workerID, leaseFor, CompilerAvailability{SolcJS: true, Geas: true})
}

func (repository *PostgresRepository) ClaimRunnable(
	ctx context.Context,
	workerID string,
	leaseFor time.Duration,
	availability CompilerAvailability,
) (VerificationLease, bool, error) {
	return repository.claimRunnable(ctx, workerID, leaseFor, availability)
}

func (repository *PostgresRepository) claimRunnable(
	ctx context.Context,
	workerID string,
	leaseFor time.Duration,
	availability CompilerAvailability,
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
	job, err := repository.scanV2Job(repository.db.QueryRowContext(ctx, dbgen.VerifyV2ClaimRunnable,
		workerID, token, microseconds, availability.SolcJS, availability.Geas,
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
	if (provenance.Kind != CompilerSolcJS && provenance.Kind != CompilerGeas) ||
		provenance.ExecutionPolicy != TrustedSubprocessPolicy ||
		provenance.ExecutorDigest == [sha256.Size]byte{} {
		return ErrCompilerProvenanceConflict
	}
	if (lease.Job.RequestV2.Language == LanguageGeas) != (provenance.Kind == CompilerGeas) ||
		(provenance.Kind == CompilerSolcJS &&
			lease.Job.RequestV2.Language != LanguageSolidity &&
			lease.Job.RequestV2.Language != LanguageYul) {
		return ErrCompilerProvenanceConflict
	}
	var generation any
	if provenance.CatalogGeneration > 0 {
		generation = provenance.CatalogGeneration
	}
	result, err := repository.db.ExecContext(ctx, dbgen.VerifyInlineBindCompilerStatement1, lease.Job.ID, lease.Token, provenance.Platform,
		generation, provenance.Digest[:],
		provenance.ExecutorKind, provenance.ExecutionPolicy,
		provenance.ExecutorDigest[:])
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 1 {
		return nil
	}
	var leaseOwned bool
	err = repository.db.QueryRowContext(ctx, dbgen.VerifyInlineBindCompilerStatement2, lease.Job.ID, lease.Token).Scan(&leaseOwned)
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
	job, err := repository.scanV2Job(tx.QueryRowContext(ctx, dbgen.VerifyV2LockRunningJob, lease.Job.ID, lease.Token))
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
		if err := validateAddressSuccessEvidence(job.RequestV2.Target, resultFields); err != nil {
			return err
		}
		publicationBlockNumber, err = canonicalV2Target(ctx, tx, job.RequestV2.Target)
		if err != nil {
			return ErrTargetNotCanonical
		}
		publicationAddress, _ = decodeFixedHex(job.RequestV2.Target.Address, 20)
		publicationCodeHash, _ = decodeFixedHex(job.RequestV2.Target.CodeHash, 32)
		blockHash, _ := decodeFixedHex(job.RequestV2.Target.AtBlockHash, 32)
		var actualRuntime []byte
		runtimeErr := tx.QueryRowContext(ctx, dbgen.VerifyInlineCompleteV2Statement1, strconv.FormatUint(job.RequestV2.Target.ChainID, 10), publicationAddress,
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
	if _, err := tx.ExecContext(ctx, dbgen.VerifyInlineCompleteV2Statement2, job.ID, lease.Token, outcomeKind, string(outcome)); err != nil {
		return err
	}
	artifactKind, artifactVersion, artifactImmutable, artifactManifest :=
		proxyArtifactAttestationValues(authenticatedArtifact)
	if _, err := tx.ExecContext(ctx, dbgen.VerifyInlineCompleteV2Statement3, job.ID, job.RequestDigest[:], outcomeKind, string(outcome),
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
		if _, err := tx.ExecContext(ctx, dbgen.VerifyInlineCompleteV2Statement4, strconv.FormatUint(job.RequestV2.Target.ChainID, 10), publicationAddress,
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
			if _, err := tx.ExecContext(ctx, dbgen.VerifyInlineCompleteV2Statement5, strconv.FormatUint(job.RequestV2.Target.ChainID, 10),
				publicationAddress, publicationCodeHash,
				strconv.FormatUint(publicationBlockNumber, 10),
				job.ID, job.RequestDigest[:], authenticatedArtifact.Kind,
				authenticatedArtifact.StandardVersion, immutable,
				authenticatedArtifact.SourceManifestSHA256[:],
			); err != nil {
				return fmt.Errorf("publish authenticated OpenZeppelin proxy artifact: %w", err)
			}
		}
		var selectorABI []byte
		if resultFields.ABI != nil {
			encoded, ok := resultFields.ABI.(string)
			if !ok {
				return errors.New("verification ABI selector projection is invalid")
			}
			selectorABI = []byte(encoded)
		}
		if err := verifiedselector.Persist(ctx, tx, verifiedselector.Identity{
			JobID: job.ID, RequestDigest: job.RequestDigest[:],
			ChainID: strconv.FormatUint(job.RequestV2.Target.ChainID, 10),
			Address: publicationAddress, CodeHash: publicationCodeHash,
			ValidFromBlock: publicationBlockNumber,
		}, selectorABI); err != nil {
			return err
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
	result, err := repository.db.ExecContext(ctx, dbgen.VerifyInlineFailStatement1, lease.Job.ID, lease.Token, code)
	if err != nil {
		return err
	}
	return requireVerificationLease(result)
}

func (repository *PostgresRepository) Job(ctx context.Context, id string) (VerificationJob, bool, error) {
	if !validUUID(id) {
		return VerificationJob{}, false, errors.New("verification job ID is invalid")
	}
	job, err := repository.scanV2Job(repository.db.QueryRowContext(ctx, dbgen.VerifyV2GetJob, id))
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
			if !platform.Valid || (generation.Valid && generation.Int64 <= 0) ||
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
			case GeasExecutorKind:
				provenance.Kind = CompilerGeas
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
) (VerifiedContract, bool, error) {
	address, err := decodeFixedHex(addressHex, 20)
	if err != nil {
		return VerifiedContract{}, false, err
	}
	if repository == nil || repository.artifacts == nil {
		return VerifiedContract{}, false, errors.New("verification artifact resolver is unavailable")
	}
	resolved, found, err := repository.artifacts.ResolveCurrent(
		ctx, strconv.FormatUint(chainID, 10), address,
	)
	if err != nil {
		return VerifiedContract{}, false, err
	}
	if !found {
		return VerifiedContract{}, false, nil
	}
	var contract VerifiedContract
	contract.CreationMatch, err = decodeStoredVerificationMatch(resolved.Source.CreationMatch)
	if err != nil {
		return VerifiedContract{}, false, errors.New("stored creation match is invalid")
	}
	contract.RuntimeMatch, err = decodeStoredVerificationMatch(resolved.Source.RuntimeMatch)
	if err != nil || contract.RuntimeMatch == nil {
		return VerifiedContract{}, false, errors.New("stored runtime match is invalid")
	}
	contract.Resolution = string(resolved.Resolution)
	contract.Target.ChainID = chainID
	contract.Target.Address = "0x" + hex.EncodeToString(resolved.Target.Address)
	contract.Target.CodeHash = "0x" + hex.EncodeToString(resolved.Target.CodeHash)
	contract.Target.BlockHash = "0x" + hex.EncodeToString(resolved.Target.BlockHash)
	contract.Target.BlockNumber, err = strconv.ParseUint(resolved.Target.BlockNumber, 10, 64)
	if err != nil {
		return VerifiedContract{}, false, errors.New("stored artifact target block is invalid")
	}
	contract.Source.Address = "0x" + hex.EncodeToString(resolved.Source.Address)
	contract.Source.CodeHash = "0x" + hex.EncodeToString(resolved.Source.CodeHash)
	contract.Source.ValidFromBlock, err = strconv.ParseUint(resolved.Source.ValidFromBlock, 10, 64)
	if err != nil {
		return VerifiedContract{}, false, errors.New("stored artifact source block is invalid")
	}
	if resolved.Source.ValidToBlock.Valid {
		value, parseErr := strconv.ParseUint(resolved.Source.ValidToBlock.String, 10, 64)
		if parseErr != nil {
			return VerifiedContract{}, false, parseErr
		}
		contract.Source.ValidToBlock = &value
	}
	contract.Source.CreatedAt = resolved.Source.CreatedAt.Time
	contract.ChainID = chainID
	contract.Address = contract.Source.Address
	contract.CodeHash = contract.Source.CodeHash
	contract.ValidFromBlock = contract.Source.ValidFromBlock
	contract.ValidToBlock = contract.Source.ValidToBlock
	contract.CreatedAt = contract.Source.CreatedAt
	contract.FileName = resolved.Source.FileName
	contract.ContractName = resolved.Source.ContractName
	contract.Language = Language(resolved.Source.Language)
	contract.CompilerVersion = resolved.Source.CompilerVersion
	contract.MatchType = VerificationMatchType(resolved.Source.MatchType)
	contract.ABI = resolved.Source.ABI
	contract.Sources = resolved.Source.Sources
	contract.Settings = resolved.Source.Settings
	contract.CompilationArtifacts = resolved.Source.CompilationArtifacts
	contract.CreationCodeArtifacts = resolved.Source.CreationCodeArtifacts
	contract.RuntimeCodeArtifacts = resolved.Source.RuntimeCodeArtifacts
	contract.IsBlueprint = resolved.Source.IsBlueprint
	if err := json.Unmarshal(resolved.Source.Libraries, &contract.Libraries); err != nil {
		return VerifiedContract{}, false, errors.New("stored verified libraries are invalid")
	}
	if len(resolved.Source.ConstructorArguments) > 0 {
		contract.ConstructorArguments = "0x" + hex.EncodeToString(resolved.Source.ConstructorArguments)
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
	if details.Transformations == nil {
		details.Transformations = make([]Transformation, 0)
	}
	return &details, nil
}

type v2ResultFields struct {
	FileName             any
	ContractName         any
	Language             any
	CompilerVersion      any
	MatchType            any
	CreationMatch        string
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
	creationMatch := ""
	runtimeMatch := ""
	if success.CreationMatch != nil {
		if success.CreationMatch.MatchType != VerificationMatchFull &&
			success.CreationMatch.MatchType != VerificationMatchPartial {
			return v2ResultFields{}, errors.New("verification creation match type is invalid")
		}
		matchType = success.CreationMatch.MatchType
		creationMatch = string(success.CreationMatch.MatchType)
	}
	if success.RuntimeMatch != nil {
		if success.RuntimeMatch.MatchType != VerificationMatchFull &&
			success.RuntimeMatch.MatchType != VerificationMatchPartial {
			return v2ResultFields{}, errors.New("verification runtime match type is invalid")
		}
		matchType = success.RuntimeMatch.MatchType
		runtimeMatch = string(success.RuntimeMatch.MatchType)
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
		MatchType: matchType, CreationMatch: creationMatch, RuntimeMatch: runtimeMatch, ABI: abi,
		Sources: string(success.Sources), Settings: string(success.Settings),
		CompilationArtifacts: string(success.Compilation),
		CreationArtifacts:    string(success.Creation), RuntimeArtifacts: string(success.Runtime),
		ConstructorArguments: constructor, Libraries: string(success.Libraries),
		Blueprint: success.Blueprint,
	}, nil
}

func validateAddressSuccessEvidence(target *VerificationTarget, fields v2ResultFields) error {
	if fields.RuntimeMatch == "" {
		return errors.New("address verification success lacks a runtime match")
	}
	if target != nil && target.GenesisPredeploy &&
		(fields.CreationMatch != "" || fields.ConstructorArguments != nil) {
		return errors.New("genesis predeploy verification success contains creation evidence")
	}
	return nil
}

func canonicalV2Target(ctx context.Context, tx *sql.Tx, target *VerificationTarget) (uint64, error) {
	if target == nil {
		return 0, ErrTargetNotCanonical
	}
	address, _ := decodeFixedHex(target.Address, 20)
	codeHash, _ := decodeFixedHex(target.CodeHash, 32)
	blockHash, _ := decodeFixedHex(target.AtBlockHash, 32)
	var blockNumber string
	if target.GenesisPredeploy {
		runtime, err := decodeBytecode(target.RuntimeBytecode)
		if err != nil || len(runtime) == 0 {
			return 0, ErrTargetNotCanonical
		}
		if err := tx.QueryRowContext(ctx, dbgen.VerifyLegacyVerificationCanonicalGenesisTarget, strconv.FormatUint(target.ChainID, 10), address, codeHash, blockHash, runtime).Scan(&blockNumber); err != nil {
			return 0, err
		}
	} else if err := tx.QueryRowContext(ctx, dbgen.VerifyLegacyVerificationCanonicalTarget, strconv.FormatUint(target.ChainID, 10), address, codeHash, blockHash).Scan(&blockNumber); err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(blockNumber, 10, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}
