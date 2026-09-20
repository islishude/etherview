package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strconv"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgx "github.com/jackc/pgx/v5"
	pgtype "github.com/jackc/pgx/v5/pgtype"

	"github.com/ethereum/go-ethereum/common"
	"github.com/islishude/etherview/internal/contractartifact"
	dbgen "github.com/islishude/etherview/internal/db/gen"
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
	err := func() error {
		var queryValue0 pgtype.UUID
		if err := queryValue0.Scan(resolved.Source.VerificationJobID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(repository.db).DerivedVerifyArtifactJobKind(ctx, queryValue0)
		if err != nil {
			return err
		}
		kind = JobKind(queryRow)
		return nil
	}()
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if kind == JobSourcify || kind == JobSourcifyFromEtherscan {
		contract.VerificationOrigin = VerificationOriginSourcify
	}
	if kind == JobDerived {
		contract.VerificationOrigin = VerificationOriginFactoryDerived
	}
	contract.DerivedChildren = make([]DerivedContract, 0)
	if resolved.Resolution != contractartifact.ResolutionExactAddress {
		return nil
	}

	var creator, created, transaction, blockHash []byte
	var tracePath, callType, blockNumber, parentFile, parentContract string
	err = func() error {
		var queryValue0 pgtype.UUID
		if err := queryValue0.Scan(resolved.Source.VerificationJobID); err != nil {
			return err
		}
		queryRow, err := dbgen.New(repository.db).DerivedVerifyArtifactProvenance(ctx, queryValue0)
		if err != nil {
			return err
		}
		creator = queryRow.CreatorAddress
		created = queryRow.CreatedAddress
		transaction = queryRow.TransactionHash
		tracePath = queryRow.TracePath
		callType = queryRow.CallType
		blockNumber = queryRow.ExactBlockNumber
		blockHash = queryRow.BlockHash
		parentFile = queryRow.FileName
		parentContract = queryRow.ContractName
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) && kind == JobDerived {
		return ErrDerivedEvidenceStale
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("load derived verification provenance: %w", err)
	}
	if err == nil {
		number, parseErr := strconv.ParseUint(blockNumber, 10, 64)
		if parseErr != nil || len(creator) != 20 || len(created) != 20 ||
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
	rows, err := func() ([]dbgen.DerivedVerifyCreatedContractsRow, error) {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(resolved.Target.ChainID); err != nil {
			return nil, err
		}
		var queryValue1 pgtype.UUID
		if err := queryValue1.Scan(resolved.Source.VerificationJobID); err != nil {
			return nil, err
		}
		var queryValue2 pgtype.Numeric
		if err := queryValue2.Scan(resolved.Target.BlockNumber); err != nil {
			return nil, err
		}
		return dbgen.New(repository.db).DerivedVerifyCreatedContracts(ctx, dbgen.DerivedVerifyCreatedContractsParams{ChainID: queryValue0, CreatorAddress: resolved.Target.Address, CreatorCodeHash: resolved.Source.CodeHash, SourceJobID: queryValue1, MaxBlockNumber: queryValue2})
	}()
	if err != nil {
		return err
	}

	for _, storedRow := range rows {
		var child DerivedContract
		var address, transaction, blockHash []byte
		var blockNumber string
		var fileName, contractName pgtype.Text
		{
			address = storedRow.CreatedAddress
			transaction = storedRow.TransactionHash
			child.TracePath = storedRow.TracePath
			child.CallType = storedRow.CallType
			blockNumber = storedRow.AttemptBlockNumber
			blockHash = storedRow.BlockHash
			child.Status = storedRow.Status
			var queryValue7 pgtype.Text
			if storedRow.FileName != nil {
				queryValue7 = pgtype.Text{String: *storedRow.FileName, Valid: true}
			}
			fileName = queryValue7
			var queryValue9 pgtype.Text
			if storedRow.ContractName != nil {
				queryValue9 = pgtype.Text{String: *storedRow.ContractName, Valid: true}
			}
			contractName = queryValue9
			child.AutoVerified = storedRow.AutoVerified
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

	return nil
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

// PreparedDerivedMatch is an opaque, transformation-aware unique match. Only
// PrepareDerivedMatch can construct a valid value; publication rechecks its
// exact block evidence before trusting the immutable compilation result.
type PreparedDerivedMatch struct {
	match            CandidateMatch
	outcome          json.RawMessage
	creationHash     [sha256.Size]byte
	runtimeHash      [sha256.Size]byte
	standardJSONHash [sha256.Size]byte
}

func PrepareDerivedMatch(
	candidates []CandidateArtifact,
	standardJSON json.RawMessage,
	input MatchInput,
) (PreparedDerivedMatch, string, error) {
	if input.Runtime == "" || input.Runtime == "0x" {
		return PreparedDerivedMatch{}, "pending_runtime", nil
	}
	creation, err := decodeBytecode(input.Creation)
	if err != nil || len(creation) == 0 {
		return PreparedDerivedMatch{}, "", errors.New("derived creation evidence is invalid")
	}
	runtime, err := decodeBytecode(input.Runtime)
	if err != nil || len(runtime) == 0 || !jsonObject(standardJSON) {
		return PreparedDerivedMatch{}, "", errors.New("derived runtime evidence is invalid")
	}
	creationMatches := 0
	confirmed := make([]CandidateMatch, 0, 1)
	for _, candidate := range candidates {
		match, ok, matchErr := MatchCandidate(candidate, input, false)
		if matchErr != nil {
			return PreparedDerivedMatch{}, "", matchErr
		}
		if ok && match.Creation != nil {
			creationMatches++
			if match.Runtime != nil {
				confirmed = append(confirmed, match)
			}
		}
	}
	switch {
	case creationMatches == 0:
		return PreparedDerivedMatch{}, "no_match", nil
	case len(confirmed) == 0:
		return PreparedDerivedMatch{}, "runtime_mismatch", nil
	case len(confirmed) > 1:
		return PreparedDerivedMatch{}, "ambiguous", nil
	}
	outcome, err := derivedVerificationOutcome(confirmed[0], standardJSON)
	if err != nil {
		return PreparedDerivedMatch{}, "", err
	}
	return PreparedDerivedMatch{
		match: confirmed[0], outcome: outcome,
		creationHash: sha256.Sum256(creation), runtimeHash: sha256.Sum256(runtime),
		standardJSONHash: sha256.Sum256(standardJSON),
	}, "matched", nil
}

func (prepared PreparedDerivedMatch) valid() bool {
	return prepared.match.Creation != nil && prepared.match.Runtime != nil &&
		prepared.match.Candidate.FileName != "" && prepared.match.Candidate.ContractName != "" &&
		jsonObject(prepared.outcome) && prepared.creationHash != [sha256.Size]byte{} &&
		prepared.runtimeHash != [sha256.Size]byte{} &&
		prepared.standardJSONHash != [sha256.Size]byte{}
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
	prepared PreparedDerivedMatch,
) (string, error) {
	if repository == nil || repository.db == nil || !validUUID(identity.CompilationID) ||
		identity.BlockNumber == 0 || len(identity.BlockHash) != 32 ||
		len(identity.Transaction) != 32 || identity.TracePath == "" || len(identity.TracePath) > 2048 ||
		!prepared.valid() {
		return "", errors.New("derived verification trace identity is invalid")
	}
	tx, err := repository.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", err
	}
	defer dbaccess.Rollback(ctx, tx)
	evidence, err := loadDerivedPublicationEvidence(ctx, tx, identity)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrDerivedEvidenceStale
	}
	if err != nil {
		return "", err
	}
	if sha256.Sum256(evidence.CreationCode) != prepared.creationHash ||
		sha256.Sum256(evidence.RuntimeCode) != prepared.runtimeHash ||
		sha256.Sum256(evidence.StandardJSON) != prepared.standardJSONHash {
		return "", ErrDerivedEvidenceStale
	}
	match, outcome := prepared.match, prepared.outcome
	if len(outcome) > repository.options.MaxResultBytes {
		return "", errors.New("derived verification outcome is invalid")
	}
	fields, err := decodeV2ResultFields("verification_success", outcome)
	if err != nil {
		return "", err
	}
	if err := func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(evidence.ChainID); err != nil {
			return err
		}
		return dbgen.New(tx).DerivedVerifyLockTarget(ctx, queryValue0, evidence.CreatedAddress)
	}(); err != nil {
		return "", fmt.Errorf("lock derived verification target: %w", err)
	}
	var existingJobID string
	err = func() error {
		var queryValue0 pgtype.Numeric
		if err := queryValue0.Scan(evidence.ChainID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(evidence.BlockNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).DerivedVerifyExistingPublication(ctx, dbgen.DerivedVerifyExistingPublicationParams{ChainID: queryValue0, Address: evidence.CreatedAddress, CodeHash: evidence.RuntimeCodeHash, ValidFromBlock: queryValue1})
		if err != nil {
			return err
		}
		existingJobID = queryRow
		return nil
	}()
	if err == nil {
		if err := enqueueDerivedTargetTx(ctx, tx, identity.CompilationID, evidence); err != nil {
			return "", err
		}
		if err := repository.recordDerivedMatchTx(
			ctx, tx, identity.CompilationID, evidence, match, existingJobID,
		); err != nil {
			return "", err
		}
		if err := tx.Commit(ctx); err != nil {
			return "", err
		}
		return existingJobID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
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
	if err := func() error {
		var queryValue0 pgtype.UUID
		if err := queryValue0.Scan(jobID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(evidence.ChainID); err != nil {
			return err
		}
		return dbgen.New(tx).DerivedVerifyInsertJob(ctx, dbgen.DerivedVerifyInsertJobParams{ID: queryValue0, CompilerVersion: new(evidence.CompilerVersion), CompilerPlatform: new(evidence.CompilerPlatform), CatalogGenerationID: evidence.CatalogGenerationID, CompilerDigest: evidence.CompilerDigest, ExecutorKind: new(evidence.ExecutorKind), ExecutionPolicy: new(evidence.ExecutionPolicy), ExecutorDigest: evidence.ExecutorDigest, ChainID: queryValue1, Address: evidence.CreatedAddress, CodeHash: evidence.RuntimeCodeHash, BlockHash: evidence.BlockHash, Request: []byte(string(requestPayload)), RequestPayload: requestPayload, RequestDigest: requestDigest[:], Outcome: []byte(string(outcome))})
	}(); err != nil {
		return "", fmt.Errorf("insert derived verification job: %w", err)
	}
	if err := func() error {
		var queryValue0 pgtype.UUID
		if err := queryValue0.Scan(jobID); err != nil {
			return err
		}
		return dbgen.New(tx).VerifyInlineCompleteV2Statement3(ctx, dbgen.VerifyInlineCompleteV2Statement3Params{JobID: queryValue0, RequestDigest: requestDigest[:], OutcomeKind: "verification_success", Outcome: []byte(string(outcome)), FileName: fields.FileName, ContractName: fields.ContractName, Language: fields.Language, CompilerVersion: fields.CompilerVersion, MatchType: fields.MatchType, Abi: fields.ABI, Sources: fields.Sources, Settings: fields.Settings, CompilationArtifacts: fields.CompilationArtifacts, CreationCodeArtifacts: fields.CreationArtifacts, RuntimeCodeArtifacts: fields.RuntimeArtifacts, ConstructorArguments: fields.ConstructorArguments, Libraries: fields.Libraries, IsBlueprint: fields.Blueprint, ProxyArtifactKind: artifactKind, ProxyStandardVersion: artifactVersion, ProxyRuntimeImmutableAddress: artifactImmutable, ProxySourceManifestSha256: artifactManifest})
	}(); err != nil {
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
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return jobID, nil
}

func enqueueDerivedTargetTx(
	ctx context.Context,
	tx pgx.Tx,
	compilationID string,
	evidence derivedPublicationEvidence,
) error {
	if err := func() error {
		var queryValue0 pgtype.UUID
		if err := queryValue0.Scan(compilationID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(evidence.ChainID); err != nil {
			return err
		}
		var queryValue2 pgtype.Numeric
		if err := queryValue2.Scan(strconv.FormatUint(evidence.BlockNumber, 10)); err != nil {
			return err
		}
		return dbgen.New(tx).DerivedVerifyEnqueueHistoricalScan(ctx, dbgen.DerivedVerifyEnqueueHistoricalScanParams{CompilationID: queryValue0, ChainID: queryValue1, CreatorAddress: evidence.CreatedAddress, CreatorCodeHash: evidence.RuntimeCodeHash, CursorBlockNumber: queryValue2, ValidToBlock: pgtype.Numeric{}})
	}(); err != nil {
		return fmt.Errorf("enqueue transitive derived verification: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) recordDerivedMatchTx(
	ctx context.Context,
	tx pgx.Tx,
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
	if err := func() error {
		var queryValue0 pgtype.UUID
		if err := queryValue0.Scan(attemptID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(evidence.ChainID); err != nil {
			return err
		}
		var queryValue2 pgtype.Numeric
		if err := queryValue2.Scan(strconv.FormatUint(evidence.BlockNumber, 10)); err != nil {
			return err
		}
		var queryValue3 pgtype.UUID
		if err := queryValue3.Scan(compilationID); err != nil {
			return err
		}
		var queryValue4 pgtype.UUID
		if err := queryValue4.Scan(jobID); err != nil {
			return err
		}
		return dbgen.New(tx).DerivedVerifyMatchAttempt(ctx, dbgen.DerivedVerifyMatchAttemptParams{ID: queryValue0, ChainID: queryValue1, BlockNumber: queryValue2, BlockHash: evidence.BlockHash, TransactionHash: evidence.TransactionHash, TracePath: evidence.TracePath, CreatorAddress: evidence.CreatorAddress, CreatedAddress: evidence.CreatedAddress, CallType: evidence.CallType, CompilationID: queryValue3, FileName: new(match.Candidate.FileName), ContractName: new(match.Candidate.ContractName), CreationMatch: []byte(string(creationMatch)), RuntimeMatch: []byte(string(runtimeMatch)), VerificationJobID: queryValue4})
	}(); err != nil {
		return fmt.Errorf("record derived verification match: %w", err)
	}
	return nil
}

func loadDerivedPublicationEvidence(
	ctx context.Context,
	tx pgx.Tx,
	identity DerivedTraceIdentity,
) (derivedPublicationEvidence, error) {
	var evidence derivedPublicationEvidence
	var blockNumber string
	err := func() error {
		var queryValue0 pgtype.UUID
		if err := queryValue0.Scan(identity.CompilationID); err != nil {
			return err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(strconv.FormatUint(identity.BlockNumber, 10)); err != nil {
			return err
		}
		queryRow, err := dbgen.New(tx).DerivedVerifyPublicationEvidence(ctx, dbgen.DerivedVerifyPublicationEvidenceParams{ID: queryValue0, BlockNumber: queryValue1, BlockHash: identity.BlockHash, TransactionHash: identity.Transaction, TracePath: identity.TracePath})
		if err != nil {
			return err
		}
		evidence.ChainID = queryRow.TraceChainID
		blockNumber = queryRow.TraceBlockNumber
		evidence.BlockHash = queryRow.BlockHash
		evidence.TransactionHash = queryRow.TransactionHash
		evidence.TracePath = queryRow.TracePath
		evidence.CallType = queryRow.CallType
		evidence.CreatorAddress = queryRow.FromAddress
		evidence.CreatedAddress = queryRow.CreatedAddress
		evidence.CreationCode = queryRow.Input
		evidence.RuntimeCode = queryRow.Code
		evidence.RuntimeCodeHash = queryRow.CodeHash
		evidence.SourceRequestDigest = queryRow.RequestDigest
		evidence.Language = Language(queryRow.Language)
		evidence.CompilerVersion = queryRow.CompilerVersion
		evidence.CompilerPlatform = queryRow.CompilerPlatform
		evidence.CatalogGenerationID = queryRow.CatalogGenerationID
		evidence.CompilerDigest = queryRow.CompilerSha256
		evidence.ExecutorKind = queryRow.ExecutorKind
		evidence.ExecutionPolicy = queryRow.ExecutionPolicy
		evidence.ExecutorDigest = queryRow.ExecutorSha256
		evidence.StandardJSON = json.RawMessage(queryRow.StandardJsonPayload)
		evidence.ParentVerificationID = queryRow.ParentVerificationJobID
		return nil
	}()
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
