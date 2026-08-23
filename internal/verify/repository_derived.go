package verify

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/contractartifact"
	"github.com/islishude/etherview/internal/db/gen"
)

func (repository *PostgresRepository) loadDerivedArtifactDetails(
	ctx context.Context,
	resolved contractartifact.Result,
	contract *VerifiedContract,
) error {
	if contract == nil {
		return errors.New("verified contract provenance target is nil")
	}
	contract.VerificationOrigin = VerificationOriginSubmitted
	var kind JobKind
	err := repository.db.QueryRowContext(
		ctx, dbgen.DerivedVerifyArtifactJobKind, resolved.Source.VerificationJobID,
	).Scan(&kind)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if kind == JobSourcify || kind == JobSourcifyFromEtherscan {
		contract.VerificationOrigin = VerificationOriginSourcify
	}
	if kind == JobDerived {
		contract.VerificationOrigin = VerificationOriginFactoryDerived
		var creator, created, transaction, blockHash []byte
		var tracePath, callType, blockNumber, parentFile, parentContract string
		err := repository.db.QueryRowContext(
			ctx, dbgen.DerivedVerifyArtifactProvenance,
			resolved.Source.VerificationJobID,
		).Scan(
			&creator, &created, &transaction, &tracePath, &callType,
			&blockNumber, &blockHash, &parentFile, &parentContract,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDerivedEvidenceStale
		}
		if err != nil {
			return fmt.Errorf("load derived verification provenance: %w", err)
		}
		number, err := strconv.ParseUint(blockNumber, 10, 64)
		if err != nil || len(creator) != 20 || len(created) != 20 ||
			len(transaction) != 32 || len(blockHash) != 32 {
			return errors.New("stored derived verification provenance is invalid")
		}
		contract.DerivedFrom = &DerivedVerificationProvenance{
			CreatorAddress:  "0x" + hex.EncodeToString(creator),
			CreatedAddress:  "0x" + hex.EncodeToString(created),
			TransactionHash: "0x" + hex.EncodeToString(transaction),
			TracePath:       tracePath, CallType: callType, BlockNumber: number,
			BlockHash:      "0x" + hex.EncodeToString(blockHash),
			ParentFileName: parentFile, ParentContractName: parentContract,
		}
	}
	contract.DerivedChildren = make([]DerivedContract, 0)
	if resolved.Resolution != contractartifact.ResolutionExactAddress {
		return nil
	}
	rows, err := repository.db.QueryContext(
		ctx, dbgen.DerivedVerifyCreatedContracts,
		resolved.Target.ChainID, resolved.Target.Address,
	)
	if err != nil {
		return err
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var child DerivedContract
		var address, transaction, blockHash []byte
		var blockNumber string
		var fileName, contractName sql.NullString
		if err := rows.Scan(
			&address, &transaction, &child.TracePath, &child.CallType,
			&blockNumber, &blockHash, &child.Status, &fileName,
			&contractName, &child.AutoVerified,
		); err != nil {
			return err
		}
		child.BlockNumber, err = strconv.ParseUint(blockNumber, 10, 64)
		if err != nil || len(address) != 20 || len(transaction) != 32 || len(blockHash) != 32 {
			return errors.New("stored derived child provenance is invalid")
		}
		child.Address = "0x" + hex.EncodeToString(address)
		child.TransactionHash = "0x" + hex.EncodeToString(transaction)
		child.BlockHash = "0x" + hex.EncodeToString(blockHash)
		child.FileName, child.ContractName = fileName.String, contractName.String
		contract.DerivedChildren = append(contract.DerivedChildren, child)
	}
	return rows.Err()
}

var (
	ErrDerivedEvidenceStale = errors.New("derived verification evidence is stale")
	ErrDerivedNotUnique     = errors.New("derived verification candidate is not uniquely matched")
)

type DerivedTraceIdentity struct {
	CompilationID string
	BlockNumber   uint64
	BlockHash     []byte
	Transaction   []byte
	TracePath     string
}

type derivedPublicationEvidence struct {
	ChainID              string
	BlockNumber          uint64
	BlockHash            []byte
	TransactionHash      []byte
	TracePath            string
	CallType             string
	CreatorAddress       []byte
	CreatedAddress       []byte
	CreationCode         []byte
	RuntimeCode          []byte
	RuntimeCodeHash      []byte
	SourceRequestDigest  []byte
	Language             Language
	CompilerVersion      string
	CompilerPlatform     string
	CatalogGenerationID  int64
	CompilerDigest       []byte
	ExecutorKind         string
	ExecutionPolicy      string
	ExecutorDigest       []byte
	StandardJSON         json.RawMessage
	ParentVerificationID string
}

// CompleteDerived rechecks the complete database evidence and publishes one
// uniquely matched child in the same transaction as its attempt provenance.
func (repository *PostgresRepository) CompleteDerived(
	ctx context.Context,
	identity DerivedTraceIdentity,
) (string, error) {
	if repository == nil || repository.db == nil || !validUUID(identity.CompilationID) ||
		identity.BlockNumber == 0 || len(identity.BlockHash) != 32 ||
		len(identity.Transaction) != 32 || identity.TracePath == "" || len(identity.TracePath) > 2048 {
		return "", errors.New("derived verification trace identity is invalid")
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback() //nolint:errcheck
	evidence, err := loadDerivedPublicationEvidence(ctx, tx, identity)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrDerivedEvidenceStale
	}
	if err != nil {
		return "", err
	}
	candidates, err := loadDerivedCandidatesTx(ctx, tx, identity.CompilationID, evidence)
	if err != nil {
		return "", err
	}
	var confirmed []CandidateMatch
	for _, candidate := range candidates {
		match, ok, matchErr := MatchCandidate(candidate, MatchInput{
			Creation: "0x" + hex.EncodeToString(evidence.CreationCode),
			Runtime:  "0x" + hex.EncodeToString(evidence.RuntimeCode),
		}, false)
		if matchErr != nil {
			return "", matchErr
		}
		if ok && match.Creation != nil && match.Runtime != nil {
			confirmed = append(confirmed, match)
		}
	}
	if len(confirmed) != 1 {
		return "", ErrDerivedNotUnique
	}
	match := confirmed[0]
	outcome, err := derivedVerificationOutcome(match, evidence.StandardJSON)
	if err != nil || len(outcome) > repository.options.MaxResultBytes {
		return "", errors.New("derived verification outcome is invalid")
	}
	fields, err := decodeV2ResultFields("verification_success", outcome)
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(
		ctx, dbgen.DerivedVerifyLockTarget, evidence.ChainID, evidence.CreatedAddress,
	); err != nil {
		return "", fmt.Errorf("lock derived verification target: %w", err)
	}
	var existingJobID string
	err = tx.QueryRowContext(ctx, dbgen.DerivedVerifyExistingPublication,
		evidence.ChainID, evidence.CreatedAddress, evidence.RuntimeCodeHash,
		strconv.FormatUint(evidence.BlockNumber, 10),
	).Scan(&existingJobID)
	if err == nil {
		if err := enqueueDerivedTargetTx(ctx, tx, identity.CompilationID, evidence); err != nil {
			return "", err
		}
		if err := repository.recordDerivedMatchTx(
			ctx, tx, identity.CompilationID, evidence, match, existingJobID,
		); err != nil {
			return "", err
		}
		if err := tx.Commit(); err != nil {
			return "", err
		}
		return existingJobID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	jobID, err := randomUUID(repository.random)
	if err != nil {
		return "", err
	}
	chainID := parseDerivedChainID(evidence.ChainID)
	if chainID == 0 {
		return "", errors.New("derived verification chain identity is invalid")
	}
	request := SubmissionV2{
		Kind: JobDerived, Language: evidence.Language,
		CompilerVersion: evidence.CompilerVersion,
		CompilationID:   identity.CompilationID,
		Target: &VerificationTarget{
			ChainID:          chainID,
			Address:          "0x" + hex.EncodeToString(evidence.CreatedAddress),
			CodeHash:         "0x" + hex.EncodeToString(evidence.RuntimeCodeHash),
			AtBlockHash:      "0x" + hex.EncodeToString(evidence.BlockHash),
			CreationBytecode: "0x" + hex.EncodeToString(evidence.CreationCode),
			RuntimeBytecode:  "0x" + hex.EncodeToString(evidence.RuntimeCode),
		},
		DerivedFrom: &DerivedVerificationFrom{
			CreatorAddress:  "0x" + hex.EncodeToString(evidence.CreatorAddress),
			TransactionHash: "0x" + hex.EncodeToString(evidence.TransactionHash),
			TracePath:       evidence.TracePath, CallType: evidence.CallType,
			BlockNumber: evidence.BlockNumber,
			BlockHash:   "0x" + hex.EncodeToString(evidence.BlockHash),
		},
	}
	requestPayload, err := json.Marshal(request)
	if err != nil || len(requestPayload) > repository.options.MaxRequestBytes {
		return "", errors.New("derived verification request is invalid")
	}
	requestDigest := sha256.Sum256(append(
		[]byte("etherview:verification-request:v2\x00"), requestPayload...,
	))
	target := common.BytesToAddress(evidence.CreatedAddress)
	var authenticatedArtifact *recognizedProxyArtifact
	if artifact, recognized := recognizeOpenZeppelin561Artifact(
		outcome, target, evidence.RuntimeCode,
	); recognized {
		if err := validateRecognizedProxyArtifact(artifact); err != nil {
			return "", err
		}
		authenticatedArtifact = &artifact
	}
	artifactKind, artifactVersion, artifactImmutable, artifactManifest :=
		proxyArtifactAttestationValues(authenticatedArtifact)
	if _, err := tx.ExecContext(ctx, dbgen.DerivedVerifyInsertJob,
		jobID, evidence.CompilerVersion, evidence.CompilerPlatform,
		evidence.CatalogGenerationID, evidence.CompilerDigest,
		evidence.ExecutorKind, evidence.ExecutionPolicy, evidence.ExecutorDigest,
		evidence.ChainID, evidence.CreatedAddress, evidence.RuntimeCodeHash,
		evidence.BlockHash, string(requestPayload), requestPayload,
		requestDigest[:], string(outcome),
	); err != nil {
		return "", fmt.Errorf("insert derived verification job: %w", err)
	}
	if _, err := tx.ExecContext(ctx, dbgen.VerifyInlineCompleteV2Statement3,
		jobID, requestDigest[:], "verification_success", string(outcome),
		fields.FileName, fields.ContractName, fields.Language,
		fields.CompilerVersion, fields.MatchType, fields.ABI,
		fields.Sources, fields.Settings, fields.CompilationArtifacts,
		fields.CreationArtifacts, fields.RuntimeArtifacts,
		fields.ConstructorArguments, fields.Libraries, fields.Blueprint,
		artifactKind, artifactVersion, artifactImmutable, artifactManifest,
	); err != nil {
		return "", fmt.Errorf("insert derived verification result: %w", err)
	}
	job := VerificationJob{
		ID: jobID, Kind: JobDerived, RequestV2: &request,
		RequestDigest: requestDigest, Status: JobSucceeded,
		Compiler: &CompilerProvenance{
			Kind: CompilerSolcJS, CatalogGeneration: evidence.CatalogGenerationID,
			Platform: evidence.CompilerPlatform, ExecutorKind: evidence.ExecutorKind,
			ExecutionPolicy: evidence.ExecutionPolicy,
		},
	}
	copy(job.Compiler.Digest[:], evidence.CompilerDigest)
	copy(job.Compiler.ExecutorDigest[:], evidence.ExecutorDigest)
	if err := repository.publishVerifiedContractTx(ctx, tx, verifiedPublication{
		Job: job, Fields: fields, BlockNumber: evidence.BlockNumber,
		Address: evidence.CreatedAddress, CodeHash: evidence.RuntimeCodeHash,
		Target: target, AuthenticatedArtifact: authenticatedArtifact,
	}); err != nil {
		return "", err
	}
	if err := enqueueDerivedTargetTx(ctx, tx, identity.CompilationID, evidence); err != nil {
		return "", err
	}
	if err := repository.recordDerivedMatchTx(
		ctx, tx, identity.CompilationID, evidence, match, jobID,
	); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return jobID, nil
}

func enqueueDerivedTargetTx(
	ctx context.Context,
	tx *sql.Tx,
	compilationID string,
	evidence derivedPublicationEvidence,
) error {
	if _, err := tx.ExecContext(ctx, dbgen.DerivedVerifyEnqueueHistoricalScan,
		compilationID, evidence.ChainID, evidence.CreatedAddress,
		evidence.RuntimeCodeHash, strconv.FormatUint(evidence.BlockNumber, 10), nil,
	); err != nil {
		return fmt.Errorf("enqueue transitive derived verification: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) recordDerivedMatchTx(
	ctx context.Context,
	tx *sql.Tx,
	compilationID string,
	evidence derivedPublicationEvidence,
	match CandidateMatch,
	jobID string,
) error {
	attemptID, err := randomUUID(repository.random)
	if err != nil {
		return err
	}
	creationMatch, err := json.Marshal(match.Creation)
	if err != nil {
		return err
	}
	runtimeMatch, err := json.Marshal(match.Runtime)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, dbgen.DerivedVerifyMatchAttempt,
		attemptID, evidence.ChainID, strconv.FormatUint(evidence.BlockNumber, 10),
		evidence.BlockHash, evidence.TransactionHash, evidence.TracePath,
		evidence.CreatorAddress, evidence.CreatedAddress, evidence.CallType,
		compilationID, match.Candidate.FileName, match.Candidate.ContractName,
		string(creationMatch), string(runtimeMatch), jobID,
	); err != nil {
		return fmt.Errorf("record derived verification match: %w", err)
	}
	return nil
}

func loadDerivedPublicationEvidence(
	ctx context.Context,
	tx *sql.Tx,
	identity DerivedTraceIdentity,
) (derivedPublicationEvidence, error) {
	var evidence derivedPublicationEvidence
	var blockNumber string
	err := tx.QueryRowContext(ctx, dbgen.DerivedVerifyPublicationEvidence,
		identity.CompilationID, strconv.FormatUint(identity.BlockNumber, 10),
		identity.BlockHash, identity.Transaction, identity.TracePath,
	).Scan(
		&evidence.ChainID, &blockNumber, &evidence.BlockHash,
		&evidence.TransactionHash, &evidence.TracePath, &evidence.CallType,
		&evidence.CreatorAddress, &evidence.CreatedAddress,
		&evidence.CreationCode, &evidence.RuntimeCode, &evidence.RuntimeCodeHash,
		&evidence.SourceRequestDigest, &evidence.Language,
		&evidence.CompilerVersion, &evidence.CompilerPlatform,
		&evidence.CatalogGenerationID, &evidence.CompilerDigest,
		&evidence.ExecutorKind, &evidence.ExecutionPolicy, &evidence.ExecutorDigest,
		&evidence.StandardJSON, &evidence.ParentVerificationID,
	)
	if err != nil {
		return derivedPublicationEvidence{}, err
	}
	evidence.BlockNumber, err = strconv.ParseUint(blockNumber, 10, 64)
	if err != nil || evidence.BlockNumber != identity.BlockNumber ||
		len(evidence.BlockHash) != 32 || len(evidence.TransactionHash) != 32 ||
		len(evidence.CreatorAddress) != 20 || len(evidence.CreatedAddress) != 20 ||
		len(evidence.CreationCode) == 0 || len(evidence.RuntimeCode) == 0 ||
		len(evidence.RuntimeCodeHash) != 32 || len(evidence.SourceRequestDigest) != 32 ||
		evidence.Language != LanguageSolidity || !jsonObject(evidence.StandardJSON) {
		return derivedPublicationEvidence{}, ErrDerivedEvidenceStale
	}
	return evidence, nil
}

func loadDerivedCandidatesTx(
	ctx context.Context,
	tx *sql.Tx,
	compilationID string,
	evidence derivedPublicationEvidence,
) ([]CandidateArtifact, error) {
	rows, err := tx.QueryContext(ctx, dbgen.DerivedVerifyLoadCompilationCandidates, compilationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var candidates []CandidateArtifact
	for rows.Next() {
		var language Language
		var version string
		var standardJSON []byte
		var candidate CandidateArtifact
		var creation, runtime []byte
		if err := rows.Scan(
			&language, &version, &standardJSON, &candidate.FileName,
			&candidate.ContractName, &candidate.ABI, &creation, &runtime,
			&candidate.CompilationArtifacts, &candidate.CreationCodeArtifacts,
			&candidate.RuntimeCodeArtifacts,
		); err != nil {
			return nil, err
		}
		if language != evidence.Language || version != evidence.CompilerVersion ||
			!json.Valid(standardJSON) {
			return nil, errors.New("stored derived compilation identity is invalid")
		}
		candidate.Language, candidate.CompilerVersion = language, version
		candidate.CreationBytecode = "0x" + hex.EncodeToString(creation)
		candidate.RuntimeBytecode = "0x" + hex.EncodeToString(runtime)
		candidate, err = RestoreCandidateArtifact(candidate)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(candidates) == 0 || len(candidates) > maxStandardJSONSelectorEntries {
		return nil, errors.New("stored derived compilation candidates are invalid")
	}
	return candidates, nil
}

func derivedVerificationOutcome(
	match CandidateMatch,
	standardJSON json.RawMessage,
) (json.RawMessage, error) {
	input, err := decodeRawJSONObject(standardJSON)
	if err != nil || !jsonObject(input["sources"]) || !jsonObject(input["settings"]) {
		return nil, errors.New("derived verification Standard JSON is invalid")
	}
	libraries := make(map[string]string)
	if match.Creation != nil {
		maps.Copy(libraries, match.Creation.Values.Libraries)
	}
	if match.Runtime != nil {
		maps.Copy(libraries, match.Runtime.Values.Libraries)
	}
	outcome := map[string]any{
		"kind": "verification_success", "file_name": match.Candidate.FileName,
		"contract_name": match.Candidate.ContractName,
		"language":      match.Candidate.Language, "compiler_version": match.Candidate.CompilerVersion,
		"sources": input["sources"], "settings": input["settings"],
		"abi":                     match.Candidate.ABI,
		"compilation_artifacts":   match.Candidate.CompilationArtifacts,
		"creation_code_artifacts": match.Candidate.CreationCodeArtifacts,
		"runtime_code_artifacts":  match.Candidate.RuntimeCodeArtifacts,
		"creation_match":          match.Creation, "runtime_match": match.Runtime,
		"libraries": libraries, "is_blueprint": match.Blueprint,
	}
	if match.Creation != nil && match.Creation.Values.ConstructorArguments != "" {
		outcome["constructor_arguments"] = match.Creation.Values.ConstructorArguments
	}
	return json.Marshal(outcome)
}

func parseDerivedChainID(value string) uint64 {
	parsed, _ := strconv.ParseUint(value, 10, 64)
	return parsed
}
