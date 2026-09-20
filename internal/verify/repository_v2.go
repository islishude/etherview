package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgx "github.com/jackc/pgx/v5"
	pgtype "github.com/jackc/pgx/v5/pgtype"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/cwiaargs"
	dbgen "github.com/islishude/etherview/internal/db/gen"
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
	var language *string
	var version *string
	var platform *string
	var catalogLanguage *string
	var generation *int64
	var compilerDigest []byte
	if request.Kind != JobSourcify && request.Kind != JobSourcifyFromEtherscan &&
		request.Kind != JobProxy {
		language, version = new(string(request.Language)), new(request.CompilerVersion)
		if request.Language == LanguageSolidity || request.Language == LanguageYul {
			catalogLanguage = new(string(LanguageSolidity))
		}
		if request.Language == LanguageVyper {
			catalogLanguage = new(string(LanguageVyper))
		}
	}
	var chainID *string
	var address []byte
	var codeHash []byte
	var blockHash []byte
	if request.Kind == JobAddress || request.Kind == JobProxy {
		chainID = new(strconv.FormatUint(request.Target.ChainID, 10))
		address, _ = decodeFixedHex(request.Target.Address, 20)
		codeHash, _ = decodeFixedHex(request.Target.CodeHash, 32)
		blockHash, _ = decodeFixedHex(request.Target.AtBlockHash, 32)
	}
	job, err := func() (VerificationJob, error) {
		var queryValue0 pgtype.UUID
		if err := queryValue0.Scan(id); err != nil {
			return VerificationJob{}, err
		}
		var queryValue1 pgtype.Numeric
		if chainID != nil {
			if err := queryValue1.Scan(*chainID); err != nil {
				return VerificationJob{}, err
			}
		}
		if repository.options.MaxAttempts < -2147483648 || repository.options.MaxAttempts > 2147483647 {
			return VerificationJob{}, errors.New("invalid stored query value")
		}
		row, err := dbgen.New(repository.db).VerifyV2SubmitJob(ctx, dbgen.VerifyV2SubmitJobParams{ID: queryValue0, Kind: string(request.Kind), Language: language, CatalogLanguage: catalogLanguage, CompilerVersion: version, CompilerPlatform: platform, CatalogGenerationID: generation, CompilerDigest: compilerDigest, ChainID: queryValue1, Address: address, CodeHash: codeHash, BlockHash: blockHash, Request: []byte(string(encoded)), RequestPayload: encoded, RequestDigest: digest[:], MaxAttempts: int32(repository.options.MaxAttempts)})
		if err != nil {
			return VerificationJob{}, err
		}
		return repository.decodeV2Job(dbgen.VerifyV2GetJobRow(row))
	}()
	created := err == nil
	if errors.Is(err, pgx.ErrNoRows) {
		job, err = func() (VerificationJob, error) {

			row, err := dbgen.New(repository.db).VerifyV2FindActiveJobByDigest(ctx, digest[:])
			if err != nil {
				return VerificationJob{}, err
			}
			return repository.decodeV2Job(dbgen.VerifyV2GetJobRow(row))
		}()
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
	return repository.claimRunnable(ctx, workerID, leaseFor, CompilerAvailability{SolcJS: true, Geas: true, Vyper: true})
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
	job, err := func() (VerificationJob, error) {

		row, err := dbgen.New(repository.db).VerifyV2ClaimRunnable(ctx, dbgen.VerifyV2ClaimRunnableParams{LeasedBy: new(workerID), LeaseToken: new(token), LeaseMicroseconds: microseconds, SolidityEnabled: availability.SolcJS, GeasEnabled: availability.Geas, VyperEnabled: availability.Vyper, VyperPreparedEnabled: availability.VyperBound})
		if err != nil {
			return VerificationJob{}, err
		}
		return repository.decodeV2Job(dbgen.VerifyV2GetJobRow(row))
	}()
	if errors.Is(err, pgx.ErrNoRows) {
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
	if (provenance.Kind != CompilerSolcJS && provenance.Kind != CompilerGeas && provenance.Kind != CompilerVyper) ||
		provenance.ExecutionPolicy != TrustedSubprocessPolicy ||
		provenance.ExecutorDigest == [sha256.Size]byte{} {
		return ErrCompilerProvenanceConflict
	}
	if (lease.Job.RequestV2.Language == LanguageVyper) != (provenance.Kind == CompilerVyper) ||
		(lease.Job.RequestV2.Language == LanguageGeas) != (provenance.Kind == CompilerGeas) ||
		(provenance.Kind == CompilerSolcJS &&
			lease.Job.RequestV2.Language != LanguageSolidity &&
			lease.Job.RequestV2.Language != LanguageYul) {
		return ErrCompilerProvenanceConflict
	}
	var generation *int64
	if provenance.CatalogGeneration > 0 {
		generation = new(provenance.CatalogGeneration)
	}
	result, err := func() (int64, error) {
		var queryValue0 pgtype.UUID
		if err := queryValue0.Scan(lease.Job.ID); err != nil {
			return 0, err
		}
		return dbgen.New(repository.db).VerifyInlineBindCompilerStatement1(ctx, dbgen.VerifyInlineBindCompilerStatement1Params{CompilerPlatform: new(provenance.Platform), CatalogGenerationID: generation, CompilerDigest: provenance.Digest[:], ExecutorKind: new(provenance.ExecutorKind), ExecutionPolicy: new(provenance.ExecutionPolicy), ExecutorDigest: provenance.ExecutorDigest[:], ID: queryValue0, LeaseToken: new(lease.Token)})
	}()
	if err != nil {
		return err
	}
	if affected := result; affected == 1 {
		return nil
	}
	err = func() error {
		var queryValue0 pgtype.UUID
		if err := queryValue0.Scan(lease.Job.ID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(repository.db).VerifyInlineBindCompilerStatement2(ctx, queryValue0, new(lease.Token))
		if err != nil {
			return err
		}
		_ = queryRow
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
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
	compilations ...AuthenticatedCompilation,
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
	if len(compilations) > 1 || (len(compilations) == 1 &&
		(lease.Job.Kind != JobAddress || outcomeKind != "verification_success")) {
		return errors.New("v2 authenticated compilation is invalid for outcome")
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
	resultFields, err := decodeV2ResultFields(outcomeKind, outcome)
	if err != nil {
		return err
	}
	var authenticatedCompilation *AuthenticatedCompilation
	if len(compilations) == 1 {
		validated, validateErr := validateAuthenticatedCompilation(
			job, compilations[0], repository.options.MaxResultBytes,
		)
		if validateErr != nil {
			return validateErr
		}
		authenticatedCompilation = &validated
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
		runtimeErr := func() error {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(strconv.FormatUint(job.RequestV2.Target.ChainID, 10)); err != nil {
				return err
			}
			var queryValue1 pgtype.Numeric
			if err := queryValue1.Scan(strconv.FormatUint(publicationBlockNumber, 10)); err != nil {
				return err
			}
			queryRow, err := dbgen.New(tx).VerifyInlineCompleteV2Statement1(ctx, dbgen.VerifyInlineCompleteV2Statement1Params{ChainID: queryValue0, Address: publicationAddress, BlockNumber: queryValue1, BlockHash: blockHash, CodeHash: publicationCodeHash})
			if err != nil {
				return err
			}
			actualRuntime = queryRow
			return nil
		}()
		if runtimeErr != nil && !errors.Is(runtimeErr, pgx.ErrNoRows) {
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
	if err := func() error {
		var queryValue0 pgtype.UUID
		if err := queryValue0.Scan(job.ID); err != nil {
			return err
		}
		return dbgen.New(tx).VerifyInlineCompleteV2Statement2(ctx, dbgen.VerifyInlineCompleteV2Statement2Params{ID: queryValue0, LeaseToken: new(lease.Token), OutcomeKind: new(outcomeKind), Outcome: []byte(string(outcome))})
	}(); err != nil {
		return err
	}
	artifactKind, artifactVersion, artifactImmutable, artifactManifest :=
		proxyArtifactAttestationValues(authenticatedArtifact)
	if err := func() error {
		var queryValue0 pgtype.UUID
		if err := queryValue0.Scan(job.ID); err != nil {
			return err
		}
		return dbgen.New(tx).VerifyInlineCompleteV2Statement3(ctx, dbgen.VerifyInlineCompleteV2Statement3Params{JobID: queryValue0, RequestDigest: job.RequestDigest[:], OutcomeKind: outcomeKind, Outcome: []byte(string(outcome)), FileName: resultFields.FileName, ContractName: resultFields.ContractName, Language: resultFields.Language, CompilerVersion: resultFields.CompilerVersion, MatchType: resultFields.MatchType, Abi: resultFields.ABI, Sources: resultFields.Sources, Settings: resultFields.Settings, CompilationArtifacts: resultFields.CompilationArtifacts, CreationCodeArtifacts: resultFields.CreationArtifacts, RuntimeCodeArtifacts: resultFields.RuntimeArtifacts, ConstructorArguments: resultFields.ConstructorArguments, Libraries: resultFields.Libraries, IsBlueprint: resultFields.Blueprint, ProxyArtifactKind: artifactKind, ProxyStandardVersion: artifactVersion, ProxyRuntimeImmutableAddress: artifactImmutable, ProxySourceManifestSha256: artifactManifest})
	}(); err != nil {
		return err
	}
	if job.Kind == JobAddress && outcomeKind == "verification_success" {
		if err := repository.publishVerifiedContractTx(ctx, tx, verifiedPublication{
			Job: job, Fields: resultFields, BlockNumber: publicationBlockNumber,
			Address: publicationAddress, CodeHash: publicationCodeHash,
			Target: publicationTarget, AuthenticatedArtifact: authenticatedArtifact,
		}); err != nil {
			return err
		}
		if authenticatedCompilation != nil {
			compilationID, err := repository.persistAuthenticatedCompilationTx(
				ctx, tx, job, *authenticatedCompilation,
			)
			if err != nil {
				return err
			}
			blockHash, _ := decodeFixedHex(job.RequestV2.Target.AtBlockHash, 32)
			var epochStartText string
			if err := func() error {
				var queryValue0 pgtype.Numeric
				if err := queryValue0.Scan(strconv.FormatUint(job.RequestV2.Target.ChainID, 10)); err != nil {
					return err
				}
				var queryValue1 pgtype.Numeric
				if err := queryValue1.Scan(strconv.FormatUint(publicationBlockNumber, 10)); err != nil {
					return err
				}
				queryRow, err := dbgen.New(tx).DerivedVerifyCreatorCodeEpochStart(ctx, dbgen.DerivedVerifyCreatorCodeEpochStartParams{ChainID: queryValue0, Address: publicationAddress, CodeHash: publicationCodeHash, BlockNumber: queryValue1, BlockHash: blockHash})
				if err != nil {
					return err
				}
				epochStartText = queryRow
				return nil
			}(); err != nil {
				return fmt.Errorf("resolve derived verification creator code epoch: %w", err)
			}
			epochStart, err := strconv.ParseUint(epochStartText, 10, 64)
			if err != nil || epochStart > publicationBlockNumber {
				return errors.New("derived verification creator code epoch is invalid")
			}
			if err := func() error {
				var queryValue0 pgtype.UUID
				if err := queryValue0.Scan(compilationID); err != nil {
					return err
				}
				var queryValue1 pgtype.Numeric
				if err := queryValue1.Scan(strconv.FormatUint(job.RequestV2.Target.ChainID, 10)); err != nil {
					return err
				}
				var queryValue2 pgtype.Numeric
				if err := queryValue2.Scan(strconv.FormatUint(epochStart, 10)); err != nil {
					return err
				}
				return dbgen.New(tx).DerivedVerifyEnqueueHistoricalScan(ctx, dbgen.DerivedVerifyEnqueueHistoricalScanParams{CompilationID: queryValue0, ChainID: queryValue1, CreatorAddress: publicationAddress, CreatorCodeHash: publicationCodeHash, CursorBlockNumber: queryValue2, ValidToBlock: pgtype.Numeric{}})
			}(); err != nil {
				return fmt.Errorf("enqueue historical derived verification: %w", err)
			}
		}
	}
	return tx.Commit(ctx)
}

func (repository *PostgresRepository) persistAuthenticatedCompilationTx(
	ctx context.Context,
	tx pgx.Tx,
	job VerificationJob,
	compilation AuthenticatedCompilation,
) (string, error) {
	id, err := randomUUID(repository.random)
	if err != nil {
		return "", err
	}
	compiler := job.Compiler
	if compiler == nil {
		return "", errors.New("authenticated compilation compiler provenance is unavailable")
	}
	storedID := ""
	err = func() error {
		var queryValue0 pgtype.UUID
		if err := queryValue0.Scan(id); err != nil {
			return err
		}
		var queryValue1 pgtype.UUID
		if err := queryValue1.Scan(job.ID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).VerifyInlineCompleteV2Statement6(ctx, dbgen.VerifyInlineCompleteV2Statement6Params{ID: queryValue0, SourceJobID: queryValue1, RequestDigest: job.RequestDigest[:], Language: string(job.RequestV2.Language), CompilerVersion: job.RequestV2.CompilerVersion, CompilerPlatform: compiler.Platform, CatalogGenerationID: compiler.CatalogGeneration, CompilerSha256: compiler.Digest[:], ExecutorKind: compiler.ExecutorKind, ExecutionPolicy: compiler.ExecutionPolicy, ExecutorSha256: compiler.ExecutorDigest[:], StandardJson: []byte(string(compilation.StandardJSON)), StandardJsonPayload: []byte(compilation.StandardJSON)})
		if err != nil {
			return err
		}
		storedID = queryRow
		return nil
	}()
	if err != nil {
		return "", fmt.Errorf("persist authenticated compilation unit: %w", err)
	}
	if storedID != id {
		return "", errors.New("persisted authenticated compilation identity changed")
	}
	for _, candidate := range compilation.Candidates {
		creation, _ := decodeBytecode(candidate.CreationBytecode)
		runtime, _ := decodeBytecode(candidate.RuntimeBytecode)
		if err := func() error {
			var queryValue0 pgtype.UUID
			if err := queryValue0.Scan(id); err != nil {
				return err
			}
			return dbgen.New(tx).VerifyInlineCompleteV2Statement7(ctx, dbgen.VerifyInlineCompleteV2Statement7Params{CompilationID: queryValue0, FileName: candidate.FileName, ContractName: candidate.ContractName, Abi: []byte(string(candidate.ABI)), CreationBytecode: creation, RuntimeBytecode: runtime, CompilationArtifacts: []byte(string(candidate.CompilationArtifacts)), CreationCodeArtifacts: []byte(string(candidate.CreationCodeArtifacts)), RuntimeCodeArtifacts: []byte(string(candidate.RuntimeCodeArtifacts))})
		}(); err != nil {
			return "", fmt.Errorf("persist authenticated compilation candidate: %w", err)
		}
	}
	return id, nil
}

type verifiedPublication struct {
	Job                   VerificationJob
	Fields                v2ResultFields
	BlockNumber           uint64
	Address               []byte
	CodeHash              []byte
	Target                common.Address
	AuthenticatedArtifact *recognizedProxyArtifact
}

func (repository *PostgresRepository) publishVerifiedContractTx(
	ctx context.Context,
	tx pgx.Tx,
	publication verifiedPublication,
) error {
	job, fields := publication.Job, publication.Fields
	chainID := strconv.FormatUint(job.RequestV2.Target.ChainID, 10)
	blockNumber := strconv.FormatUint(publication.BlockNumber, 10)
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(chainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(blockNumber); err != nil {
			return err
		}
		var queryValue2 pgtype.UUID
		if err := queryValue2.Scan(job.ID); err != nil {
			return err
		}
		return dbgen.New(tx).VerifyInlineCompleteV2Statement4(ctx, dbgen.VerifyInlineCompleteV2Statement4Params{ChainID: queryValue0, Address: publication.Address, CodeHash: publication.CodeHash, ValidFromBlock: queryValue1, VerificationJobID: queryValue2, RequestDigest: job.RequestDigest[:], FileName: fields.FileName, ContractName: fields.ContractName, Language: fields.Language, CompilerVersion: fields.CompilerVersion, MatchType: fields.RuntimeMatch, Abi: fields.ABI, Sources: fields.Sources, Settings: fields.Settings, CompilationArtifacts: fields.CompilationArtifacts, CreationCodeArtifacts: fields.CreationArtifacts, RuntimeCodeArtifacts: fields.RuntimeArtifacts, ConstructorArguments: fields.ConstructorArguments, Libraries: fields.Libraries, IsBlueprint: fields.Blueprint})
	}(); err != nil {
		return err
	}
	if publication.AuthenticatedArtifact != nil {
		artifact := publication.AuthenticatedArtifact
		var immutable []byte
		if artifact.RuntimeImmutable != nil {
			immutable = artifact.RuntimeImmutable[:]
		}
		if err := func() error {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(chainID); err != nil {
				return err
			}
			var queryValue1 pgtype.Numeric
			if err := queryValue1.Scan(blockNumber); err != nil {
				return err
			}
			var queryValue2 pgtype.UUID
			if err := queryValue2.Scan(job.ID); err != nil {
				return err
			}
			return dbgen.New(tx).VerifyInlineCompleteV2Statement5(ctx, dbgen.VerifyInlineCompleteV2Statement5Params{ChainID: queryValue0, Address: publication.Address, CodeHash: publication.CodeHash, ValidFromBlock: queryValue1, VerificationJobID: queryValue2, RequestDigest: job.RequestDigest[:], ArtifactKind: artifact.Kind, StandardVersion: artifact.StandardVersion, RuntimeImmutableAddress: immutable, SourceManifestSha256: artifact.SourceManifestSHA256[:]})
		}(); err != nil {
			return fmt.Errorf("publish authenticated OpenZeppelin proxy artifact: %w", err)
		}
	}
	selectorABI := fields.ABI
	if err := verifiedselector.Persist(ctx, tx, verifiedselector.Identity{
		JobID: job.ID, RequestDigest: job.RequestDigest[:], ChainID: chainID,
		Address: publication.Address, CodeHash: publication.CodeHash,
		ValidFromBlock: publication.BlockNumber,
	}, selectorABI); err != nil {
		return err
	}
	return repository.requestVerificationProxyReplayTx(
		ctx, tx, job, publication.BlockNumber, publication.Target,
		publication.AuthenticatedArtifact,
	)
}

func proxyArtifactAttestationValues(artifact *recognizedProxyArtifact) (*string, *string, []byte, []byte) {
	if artifact == nil {
		return nil, nil, nil, nil
	}
	var immutable []byte
	if artifact.RuntimeImmutable != nil {
		immutable = artifact.RuntimeImmutable[:]
	}
	return new(artifact.Kind), new(artifact.StandardVersion), immutable, artifact.SourceManifestSHA256[:]
}

func (repository *PostgresRepository) Fail(
	ctx context.Context,
	lease VerificationLease,
	code ErrorCode,
) error {
	if !code.valid() {
		return errors.New("verification failure code is invalid")
	}
	result, err := func() (int64, error) {
		var queryValue0 pgtype.UUID
		if err := queryValue0.Scan(lease.Job.ID); err != nil {
			return 0, err
		}
		return dbgen.New(repository.db).VerifyInlineFailStatement1(ctx, new(string(code)), queryValue0, new(lease.Token))
	}()
	if err != nil {
		return err
	}
	return requireVerificationLease(result)
}

func (repository *PostgresRepository) Job(ctx context.Context, id string) (VerificationJob, bool, error) {
	if !validUUID(id) {
		return VerificationJob{}, false, errors.New("verification job ID is invalid")
	}
	job, err := func() (VerificationJob, error) {
		var queryValue0 pgtype.UUID
		if err := queryValue0.Scan(id); err != nil {
			return VerificationJob{}, err
		}
		row, err := dbgen.New(repository.db).VerifyV2GetJob(ctx, queryValue0)
		if err != nil {
			return VerificationJob{}, err
		}
		return repository.decodeV2Job(dbgen.VerifyV2GetJobRow(row))
	}()
	if errors.Is(err, pgx.ErrNoRows) {
		return VerificationJob{}, false, nil
	}
	if err != nil {
		return VerificationJob{}, false, fmt.Errorf("query v2 verification job: %w", err)
	}
	return job, true, nil
}

func (repository *PostgresRepository) decodeV2Job(row dbgen.VerifyV2GetJobRow) (VerificationJob, error) {
	if !row.CreatedAt.Valid || !row.UpdatedAt.Valid || row.CreatedAt.InfinityModifier != pgtype.Finite || row.UpdatedAt.InfinityModifier != pgtype.Finite {
		return VerificationJob{}, errors.New("stored verification job timestamp is invalid")
	}
	job := VerificationJob{ID: row.ID, Kind: JobKind(row.Kind), Status: JobStatus(row.Status), AttemptCount: int(row.AttemptCount), MaxAttempts: int(row.MaxAttempts), CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time, Outcome: row.Outcome}
	payload, digest := row.RequestPayload, row.RequestDigest
	compilerDigest, executorDigest := row.CompilerDigest, row.ExecutorDigest
	var language, version, platform, executorKind, executionPolicy, errorCode pgtype.Text
	for _, field := range []struct {
		source *string
		target *pgtype.Text
	}{
		{row.Language, &language}, {row.CompilerVersion, &version}, {row.CompilerPlatform, &platform}, {row.ExecutorKind, &executorKind}, {row.ExecutionPolicy, &executionPolicy}, {row.ErrorCode, &errorCode},
	} {
		if field.source != nil {
			*field.target = pgtype.Text{String: *field.source, Valid: true}
		}
	}
	var generation pgtype.Int8
	if row.CatalogGenerationID != nil {
		generation = pgtype.Int8{Int64: *row.CatalogGenerationID, Valid: true}
	}

	if len(payload) > repository.options.MaxRequestBytes ||
		json.Unmarshal(payload, &job.RequestV2) != nil || job.RequestV2 == nil ||
		len(digest) != sha256.Size {
		return VerificationJob{}, errors.New("stored v2 verification job is invalid")
	}
	copy(job.RequestDigest[:], digest)
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
			case VyperExecutorKind, VyperDynamicExecutorKind:
				provenance.Kind = CompilerVyper
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
	if err := repository.loadDerivedArtifactDetails(ctx, resolved, &contract); err != nil {
		if errors.Is(err, ErrDerivedEvidenceStale) {
			return VerifiedContract{}, false, nil
		}
		return VerifiedContract{}, false, err
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
	FileName             *string
	ContractName         *string
	Language             *string
	CompilerVersion      *string
	MatchType            *string
	CreationMatch        string
	RuntimeMatch         string
	ABI                  []byte
	Sources              []byte
	Settings             []byte
	CompilationArtifacts []byte
	CreationArtifacts    []byte
	RuntimeArtifacts     []byte
	ConstructorArguments []byte
	Libraries            []byte
	Blueprint            *bool
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
	var constructor []byte
	if success.Constructor != "" {
		decoded, err := decodeBytecode(success.Constructor)
		if err != nil {
			return v2ResultFields{}, errors.New("verification constructor arguments are invalid")
		}
		constructor = decoded
	}
	var abi []byte
	if len(success.ABI) > 0 {
		if !jsonArray(success.ABI) {
			return v2ResultFields{}, errors.New("verification ABI is invalid")
		}
		abi = success.ABI
	}
	return v2ResultFields{
		FileName: new(success.FileName), ContractName: new(success.ContractName),
		Language: new(string(success.Language)), CompilerVersion: new(success.CompilerVersion),
		MatchType: new(string(matchType)), CreationMatch: creationMatch, RuntimeMatch: runtimeMatch, ABI: abi,
		Sources: success.Sources, Settings: success.Settings,
		CompilationArtifacts: success.Compilation,
		CreationArtifacts:    success.Creation, RuntimeArtifacts: success.Runtime,
		ConstructorArguments: constructor, Libraries: success.Libraries,
		Blueprint: new(success.Blueprint),
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

func canonicalV2Target(ctx context.Context, tx pgx.Tx, target *VerificationTarget) (uint64, error) {
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
		if err := func() error {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(strconv.FormatUint(target.ChainID, 10)); err != nil {
				return err
			}
			queryRow, err := dbgen.New(tx).VerifyLegacyVerificationCanonicalGenesisTarget(ctx, dbgen.VerifyLegacyVerificationCanonicalGenesisTargetParams{ChainID: queryValue0, Address: address, CodeHash: codeHash, BlockHash: blockHash, Code: runtime})
			if err != nil {
				return err
			}
			blockNumber = queryRow
			return nil
		}(); err != nil {
			return 0, err
		}
	} else if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(strconv.FormatUint(target.ChainID, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).VerifyLegacyVerificationCanonicalTarget(ctx, dbgen.VerifyLegacyVerificationCanonicalTargetParams{ChainID: queryValue0, Address: address, CodeHash: codeHash, BlockHash: blockHash})
		if err != nil {
			return err
		}
		blockNumber = queryRow
		return nil
	}(); err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(blockNumber, 10, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}
