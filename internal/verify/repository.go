package verify

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

var (
	ErrLeaseLost                  = errors.New("verification job lease is no longer owned")
	ErrTargetNotCanonical         = errors.New("verification target is no longer canonical")
	ErrCompilerProvenanceConflict = errors.New("verification job compiler provenance conflicts with its first attempt")
)

type JobStatus string

const (
	JobQueued    JobStatus = "queued"
	JobRunning   JobStatus = "running"
	JobSucceeded JobStatus = "succeeded"
	JobFailed    JobStatus = "failed"
	JobCancelled JobStatus = "cancelled"
)

type ErrorCode string

const (
	ErrorCompileFailed              ErrorCode = "compile_failed"
	ErrorCompilerOutput             ErrorCode = "compiler_output_invalid"
	ErrorCompilerTooLarge           ErrorCode = "compiler_output_too_large"
	ErrorMatchFailed                ErrorCode = "match_failed"
	ErrorSandboxRequired            ErrorCode = "sandbox_required"
	ErrorCompilerProvenanceMismatch ErrorCode = "compiler_provenance_mismatch"
	ErrorCompilerUnavailable        ErrorCode = "compiler_unavailable"
	ErrorTargetNotCanonical         ErrorCode = "target_not_canonical"
	ErrorAttemptsExhausted          ErrorCode = "attempts_exhausted"
)

func (code ErrorCode) valid() bool {
	switch code {
	case ErrorCompileFailed, ErrorCompilerOutput, ErrorCompilerTooLarge, ErrorMatchFailed,
		ErrorSandboxRequired, ErrorCompilerProvenanceMismatch, ErrorCompilerUnavailable,
		ErrorTargetNotCanonical, ErrorAttemptsExhausted:
		return true
	default:
		return false
	}
}

type VerificationJobResult struct {
	Match     MatchResult `json:"match"`
	Published bool        `json:"published"`
}

type VerificationJob struct {
	ID                    string
	Request               Request
	RequestDigest         [sha256.Size]byte
	RequiresHardIsolation bool
	AttemptCount          int
	MaxAttempts           int
	Compiler              *CompilerProvenance
	Status                JobStatus
	ResultKind            *MatchKind
	Result                *VerificationJobResult
	ErrorCode             ErrorCode
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type VerificationLease struct {
	Job   VerificationJob
	Token string
}

type Completion struct {
	Kind     MatchKind
	Match    MatchResult
	Artifact Artifact
	Sources  json.RawMessage
	Settings json.RawMessage
}

type VerifiedContract struct {
	ChainID         uint64
	Address         string
	CodeHash        string
	ValidFromBlock  uint64
	ValidToBlock    *uint64
	Language        Language
	CompilerVersion string
	MatchKind       MatchKind
	ContractName    string
	ABI             json.RawMessage
	Sources         json.RawMessage
	Settings        json.RawMessage
	CreatedAt       time.Time
}

type Repository interface {
	Submit(context.Context, Request, ...SubmissionOptions) (VerificationJob, bool, error)
	Claim(context.Context, string, time.Duration) (VerificationLease, bool, error)
	Renew(context.Context, VerificationLease, time.Duration) error
	BindCompiler(context.Context, VerificationLease, CompilerProvenance) error
	Complete(context.Context, VerificationLease, Completion) error
	Fail(context.Context, VerificationLease, ErrorCode) error
	Job(context.Context, string) (VerificationJob, bool, error)
	VerifiedContract(context.Context, uint64, string, string) (VerifiedContract, bool, error)
}

type RepositoryOptions struct {
	MaxRequestBytes int
	MaxResultBytes  int
	MaxAttempts     int
}

type SubmissionOptions struct {
	RequiresHardIsolation bool
}

func (options *RepositoryOptions) defaults() {
	if options.MaxRequestBytes <= 0 {
		options.MaxRequestBytes = 5 << 20
	}
	if options.MaxResultBytes <= 0 {
		options.MaxResultBytes = 16 << 20
	}
	if options.MaxAttempts <= 0 {
		options.MaxAttempts = 3
	}
}

type PostgresRepository struct {
	db      *sql.DB
	options RepositoryOptions
	random  io.Reader
}

func NewPostgresRepository(db *sql.DB, options RepositoryOptions) (*PostgresRepository, error) {
	if db == nil {
		return nil, errors.New("verification repository requires a database")
	}
	options.defaults()
	if options.MaxRequestBytes <= 0 || options.MaxResultBytes <= 0 || options.MaxAttempts > 100 {
		return nil, errors.New("verification repository limits must be positive")
	}
	return &PostgresRepository{db: db, options: options, random: rand.Reader}, nil
}

func (repository *PostgresRepository) Submit(ctx context.Context, request Request, submissions ...SubmissionOptions) (VerificationJob, bool, error) {
	if repository == nil || repository.db == nil {
		return VerificationJob{}, false, errors.New("submit using nil verification repository")
	}
	if len(submissions) > 1 {
		return VerificationJob{}, false, errors.New("verification submission accepts at most one option set")
	}
	var submission SubmissionOptions
	if len(submissions) == 1 {
		submission = submissions[0]
	}
	encoded, address, codeHash, blockHash, err := repository.encodeRequest(request)
	if err != nil {
		return VerificationJob{}, false, err
	}
	id, err := randomUUID(repository.random)
	if err != nil {
		return VerificationJob{}, false, fmt.Errorf("generate verification job ID: %w", err)
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return VerificationJob{}, false, fmt.Errorf("begin verification submission: %w", err)
	}
	defer tx.Rollback()
	digest := verificationRequestDigest(encoded, submission.RequiresHardIsolation)
	var (
		job     VerificationJob
		created bool
		scanErr error
	)
	// An active conflicting row may become failed after ON CONFLICT observes it
	// but before the fallback SELECT takes its next read-committed snapshot. In
	// that case the request is eligible again, so retry the insert once instead
	// of returning a transient storage error.
	for attempt := 0; attempt < 2; attempt++ {
		job, scanErr = repository.scanJob(tx.QueryRowContext(ctx, submitVerificationSQL,
			id,
			strconv.FormatUint(request.ChainID, 10),
			address,
			codeHash,
			blockHash,
			request.Language,
			request.CompilerVersion,
			string(encoded),
			encoded,
			digest[:],
			submission.RequiresHardIsolation,
			repository.options.MaxAttempts,
		))
		if scanErr == nil {
			created = true
			break
		}
		if !errors.Is(scanErr, sql.ErrNoRows) {
			break
		}
		job, scanErr = repository.scanJob(tx.QueryRowContext(ctx, selectVerificationBindingSQL,
			strconv.FormatUint(request.ChainID, 10), address, codeHash, blockHash, digest[:],
		))
		if scanErr == nil || !errors.Is(scanErr, sql.ErrNoRows) {
			break
		}
	}
	if scanErr != nil {
		return VerificationJob{}, false, fmt.Errorf("submit verification job: %w", scanErr)
	}
	if err := tx.Commit(); err != nil {
		return VerificationJob{}, false, fmt.Errorf("commit verification submission: %w", err)
	}
	return job, created, nil
}

func verificationRequestDigest(payload []byte, requiresHardIsolation bool) [sha256.Size]byte {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("etherview:verification-request:v1"))
	if requiresHardIsolation {
		_, _ = hasher.Write([]byte{1})
	} else {
		_, _ = hasher.Write([]byte{0})
	}
	_, _ = hasher.Write(payload)
	var digest [sha256.Size]byte
	copy(digest[:], hasher.Sum(nil))
	return digest
}

func (repository *PostgresRepository) encodeRequest(request Request) ([]byte, []byte, []byte, []byte, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, nil, nil, nil, errors.New("verification request is not valid JSON")
	}
	if len(encoded) > repository.options.MaxRequestBytes {
		return nil, nil, nil, nil, errors.New("verification request exceeds configured size limit")
	}
	if err := request.Validate(repository.options.MaxRequestBytes); err != nil {
		return nil, nil, nil, nil, err
	}
	address, err := decodeFixedHex(request.Address, 20)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	codeHash, err := decodeFixedHex(request.CodeHash, 32)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	blockHash, err := decodeFixedHex(request.AtBlockHash, 32)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if _, err := decodeBytecode(request.CreationBytecode); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("creation bytecode: %w", err)
	}
	if _, err := decodeBytecode(request.RuntimeBytecode); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("runtime bytecode: %w", err)
	}
	return encoded, address, codeHash, blockHash, nil
}

func (repository *PostgresRepository) Claim(ctx context.Context, workerID string, leaseFor time.Duration) (VerificationLease, bool, error) {
	if repository == nil || repository.db == nil {
		return VerificationLease{}, false, errors.New("claim using nil verification repository")
	}
	if strings.TrimSpace(workerID) == "" || len(workerID) > 128 {
		return VerificationLease{}, false, errors.New("verification worker ID must contain between 1 and 128 bytes")
	}
	microseconds, err := positiveMicroseconds(leaseFor)
	if err != nil {
		return VerificationLease{}, false, fmt.Errorf("verification lease duration: %w", err)
	}
	token, err := randomToken(repository.random)
	if err != nil {
		return VerificationLease{}, false, fmt.Errorf("generate verification lease token: %w", err)
	}
	job, err := repository.scanJob(repository.db.QueryRowContext(ctx, claimVerificationSQL, workerID, token, microseconds))
	if errors.Is(err, sql.ErrNoRows) {
		return VerificationLease{}, false, nil
	}
	if err != nil {
		return VerificationLease{}, false, fmt.Errorf("claim verification job: %w", err)
	}
	return VerificationLease{Job: job, Token: token}, true, nil
}

func (repository *PostgresRepository) Renew(ctx context.Context, lease VerificationLease, leaseFor time.Duration) error {
	if repository == nil || repository.db == nil {
		return errors.New("renew using nil verification repository")
	}
	if err := validateVerificationLease(lease); err != nil {
		return err
	}
	microseconds, err := positiveMicroseconds(leaseFor)
	if err != nil {
		return fmt.Errorf("verification lease duration: %w", err)
	}
	result, err := repository.db.ExecContext(ctx, renewVerificationSQL, lease.Job.ID, lease.Token, microseconds)
	if err != nil {
		return fmt.Errorf("renew verification lease: %w", err)
	}
	return requireVerificationLease(result)
}

func (repository *PostgresRepository) BindCompiler(ctx context.Context, lease VerificationLease, provenance CompilerProvenance) error {
	if repository == nil || repository.db == nil {
		return errors.New("bind compiler using nil verification repository")
	}
	if err := validateVerificationLease(lease); err != nil {
		return err
	}
	if !provenance.valid() {
		return errors.New("verification compiler provenance is invalid")
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin verification compiler binding: %w", err)
	}
	defer tx.Rollback()
	var (
		requiresHard bool
		kind         sql.NullString
		digest       []byte
		hard         sql.NullBool
	)
	if err := tx.QueryRowContext(ctx, selectVerificationCompilerSQL, lease.Job.ID, lease.Token).Scan(
		&requiresHard, &kind, &digest, &hard,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLeaseLost
		}
		return fmt.Errorf("lock verification compiler binding: %w", err)
	}
	if requiresHard && !provenance.HardIsolated {
		return ErrSandboxRequired
	}
	if kind.Valid || len(digest) != 0 || hard.Valid {
		if !kind.Valid || len(digest) != sha256.Size || !hard.Valid ||
			CompilerKind(kind.String) != provenance.Kind ||
			!equalDigest(digest, provenance.Digest) || hard.Bool != provenance.HardIsolated {
			return ErrCompilerProvenanceConflict
		}
	}
	result, err := tx.ExecContext(ctx, bindVerificationCompilerSQL,
		lease.Job.ID, lease.Token, provenance.Kind, provenance.Digest[:], provenance.HardIsolated,
	)
	if err != nil {
		return fmt.Errorf("bind verification compiler: %w", err)
	}
	if err := requireVerificationLease(result); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit verification compiler binding: %w", err)
	}
	return nil
}

func equalDigest(value []byte, digest [sha256.Size]byte) bool {
	return len(value) == len(digest) && string(value) == string(digest[:])
}

func (repository *PostgresRepository) Complete(ctx context.Context, lease VerificationLease, completion Completion) error {
	if repository == nil || repository.db == nil {
		return errors.New("complete using nil verification repository")
	}
	if err := validateVerificationLease(lease); err != nil {
		return err
	}
	if err := repository.validateCompletion(completion); err != nil {
		return err
	}
	jobResult := VerificationJobResult{Match: completion.Match, Published: completion.Kind != MatchMismatch}
	encodedResult, err := json.Marshal(jobResult)
	if err != nil || len(encodedResult) > repository.options.MaxResultBytes {
		return errors.New("verification result exceeds configured size limit")
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin verification completion: %w", err)
	}
	defer tx.Rollback()
	job, err := repository.scanJob(tx.QueryRowContext(ctx, lockVerificationLeaseSQL, lease.Job.ID, lease.Token))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("lock verification completion: %w", err)
	}
	if job.Compiler == nil {
		return errors.New("verification completion has no compiler provenance")
	}
	blockNumber, err := repository.canonicalVerificationTarget(ctx, tx, job)
	if errors.Is(err, sql.ErrNoRows) {
		result, failErr := tx.ExecContext(ctx, failVerificationSQL,
			job.ID, lease.Token, ErrorTargetNotCanonical,
		)
		if failErr != nil {
			return fmt.Errorf("record stale verification target: %w", failErr)
		}
		if failErr := requireVerificationLease(result); failErr != nil {
			return failErr
		}
		if failErr := tx.Commit(); failErr != nil {
			return fmt.Errorf("commit stale verification target: %w", failErr)
		}
		return ErrTargetNotCanonical
	}
	if err != nil {
		return err
	}
	if err := repository.insertVerificationResult(ctx, tx, job, blockNumber, completion, encodedResult); err != nil {
		return err
	}
	if completion.Kind != MatchMismatch {
		if err := repository.publishVerifiedContract(ctx, tx, job, blockNumber, completion); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, completeVerificationSQL,
		job.ID, lease.Token, completion.Kind, string(encodedResult),
	)
	if err != nil {
		return fmt.Errorf("complete verification job: %w", err)
	}
	if err := requireVerificationLease(result); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit verification completion: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) validateCompletion(completion Completion) error {
	if !validMatchResult(completion.Match) || !validMatchKind(completion.Kind) {
		return errors.New("verification completion contains an invalid match result")
	}
	if completion.Kind != summarizeMatch(completion.Match) {
		return errors.New("verification completion kind does not match creation/runtime results")
	}
	if completion.Kind == MatchMismatch {
		if len(completion.Artifact.ABI) != 0 || len(completion.Sources) != 0 || len(completion.Settings) != 0 {
			return errors.New("mismatch completion must not contain publishable ABI or sources")
		}
		return nil
	}
	if !jsonArray(completion.Artifact.ABI) || !jsonObject(completion.Sources) || !jsonObject(completion.Settings) {
		return errors.New("verified completion requires an ABI array and source/settings objects")
	}
	total := len(completion.Artifact.ABI) + len(completion.Sources) + len(completion.Settings)
	if total > repository.options.MaxResultBytes {
		return errors.New("verified contract output exceeds configured size limit")
	}
	return nil
}

func (repository *PostgresRepository) canonicalVerificationTarget(ctx context.Context, tx *sql.Tx, job VerificationJob) (uint64, error) {
	blockHash, _ := decodeFixedHex(job.Request.AtBlockHash, 32)
	address, _ := decodeFixedHex(job.Request.Address, 20)
	codeHash, _ := decodeFixedHex(job.Request.CodeHash, 32)
	var blockNumberText string
	if err := tx.QueryRowContext(ctx, verificationCanonicalTargetSQL,
		strconv.FormatUint(job.Request.ChainID, 10), address, codeHash, blockHash,
	).Scan(&blockNumberText); err != nil {
		return 0, fmt.Errorf("resolve canonical verification target: %w", err)
	}
	blockNumber, err := strconv.ParseUint(blockNumberText, 10, 64)
	if err != nil || strconv.FormatUint(blockNumber, 10) != blockNumberText {
		return 0, errors.New("resolve canonical verification target: invalid block number")
	}
	return blockNumber, nil
}

func (repository *PostgresRepository) insertVerificationResult(
	ctx context.Context,
	tx *sql.Tx,
	job VerificationJob,
	blockNumber uint64,
	completion Completion,
	encodedResult []byte,
) error {
	address, _ := decodeFixedHex(job.Request.Address, 20)
	codeHash, _ := decodeFixedHex(job.Request.CodeHash, 32)
	blockHash, _ := decodeFixedHex(job.Request.AtBlockHash, 32)
	var contractNameValue any
	var abi, sources, settings any
	if completion.Kind != MatchMismatch {
		contractNameValue = contractName(job.Request.ContractIdentifier)
		abi, sources, settings = string(completion.Artifact.ABI), string(completion.Sources), string(completion.Settings)
	}
	result, err := tx.ExecContext(ctx, insertVerificationResultSQL,
		job.ID, strconv.FormatUint(job.Request.ChainID, 10), address, codeHash, blockHash,
		strconv.FormatUint(blockNumber, 10), job.RequestDigest[:], job.Compiler.Kind,
		job.Compiler.Digest[:], job.Compiler.HardIsolated, completion.Kind, string(encodedResult),
		contractNameValue, abi, sources, settings,
	)
	if err != nil {
		return fmt.Errorf("insert immutable verification result: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return errors.New("insert immutable verification result affected an unexpected row count")
	}
	return nil
}

func (repository *PostgresRepository) publishVerifiedContract(
	ctx context.Context,
	tx *sql.Tx,
	job VerificationJob,
	blockNumber uint64,
	completion Completion,
) error {
	address, _ := decodeFixedHex(job.Request.Address, 20)
	codeHash, _ := decodeFixedHex(job.Request.CodeHash, 32)
	contractName := contractName(job.Request.ContractIdentifier)
	if contractName == "" {
		return errors.New("verified contract name is empty")
	}
	if _, err := tx.ExecContext(ctx, publishVerifiedContractSQL,
		strconv.FormatUint(job.Request.ChainID, 10),
		address,
		codeHash,
		strconv.FormatUint(blockNumber, 10),
		job.Request.Language,
		job.Request.CompilerVersion,
		completion.Kind,
		contractName,
		string(completion.Artifact.ABI),
		string(completion.Sources),
		string(completion.Settings),
		job.ID,
		job.RequestDigest[:],
	); err != nil {
		return fmt.Errorf("publish verified contract: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) Fail(ctx context.Context, lease VerificationLease, code ErrorCode) error {
	if repository == nil || repository.db == nil {
		return errors.New("fail using nil verification repository")
	}
	if err := validateVerificationLease(lease); err != nil {
		return err
	}
	if !code.valid() {
		return errors.New("verification error code is not allowlisted")
	}
	result, err := repository.db.ExecContext(ctx, failVerificationSQL, lease.Job.ID, lease.Token, code)
	if err != nil {
		return fmt.Errorf("fail verification job: %w", err)
	}
	return requireVerificationLease(result)
}

func (repository *PostgresRepository) Job(ctx context.Context, id string) (VerificationJob, bool, error) {
	if repository == nil || repository.db == nil {
		return VerificationJob{}, false, errors.New("query using nil verification repository")
	}
	if !validUUID(id) {
		return VerificationJob{}, false, errors.New("verification job ID is invalid")
	}
	job, err := repository.scanJob(repository.db.QueryRowContext(ctx, selectVerificationJobSQL, id))
	if errors.Is(err, sql.ErrNoRows) {
		return VerificationJob{}, false, nil
	}
	if err != nil {
		return VerificationJob{}, false, fmt.Errorf("query verification job: %w", err)
	}
	return job, true, nil
}

func (repository *PostgresRepository) VerifiedContract(ctx context.Context, chainID uint64, addressHex, codeHashHex string) (VerifiedContract, bool, error) {
	if repository == nil || repository.db == nil {
		return VerifiedContract{}, false, errors.New("query using nil verification repository")
	}
	if chainID == 0 {
		return VerifiedContract{}, false, errors.New("chain ID is required")
	}
	address, err := decodeFixedHex(addressHex, 20)
	if err != nil {
		return VerifiedContract{}, false, err
	}
	codeHash, err := decodeFixedHex(codeHashHex, 32)
	if err != nil {
		return VerifiedContract{}, false, err
	}
	contract, err := repository.scanVerifiedContract(repository.db.QueryRowContext(ctx, selectVerifiedContractSQL,
		strconv.FormatUint(chainID, 10), address, codeHash,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return VerifiedContract{}, false, nil
	}
	if err != nil {
		return VerifiedContract{}, false, fmt.Errorf("query verified contract: %w", err)
	}
	return contract, true, nil
}

type rowScanner interface{ Scan(...any) error }

func (repository *PostgresRepository) scanJob(row rowScanner) (VerificationJob, error) {
	var (
		job                           VerificationJob
		chainIDText                   string
		address, codeHash, blockHash  []byte
		language, compilerVersion     string
		requestJSON                   []byte
		status                        string
		resultKind, errorCode         sql.NullString
		resultJSON                    []byte
		requestDigest, compilerDigest []byte
		requiresHardIsolation         bool
		attemptCount, maxAttempts     int
		compilerKind                  sql.NullString
		compilerHardIsolated          sql.NullBool
	)
	if err := row.Scan(
		&job.ID, &chainIDText, &address, &codeHash, &blockHash, &language, &compilerVersion,
		&requestJSON, &status, &resultKind, &resultJSON, &errorCode, &job.CreatedAt, &job.UpdatedAt,
		&requestDigest, &requiresHardIsolation, &attemptCount, &maxAttempts,
		&compilerKind, &compilerDigest, &compilerHardIsolated,
	); err != nil {
		return VerificationJob{}, err
	}
	if !validUUID(job.ID) {
		return VerificationJob{}, errors.New("verification job contains invalid ID")
	}
	chainID, err := strconv.ParseUint(chainIDText, 10, 64)
	if err != nil || chainID == 0 || strconv.FormatUint(chainID, 10) != chainIDText {
		return VerificationJob{}, errors.New("verification job contains invalid chain ID")
	}
	if len(requestJSON) > repository.options.MaxRequestBytes || !json.Valid(requestJSON) {
		return VerificationJob{}, errors.New("verification job request exceeds configured bounds")
	}
	if err := json.Unmarshal(requestJSON, &job.Request); err != nil {
		return VerificationJob{}, errors.New("verification job request is invalid")
	}
	if err := job.Request.Validate(repository.options.MaxRequestBytes); err != nil {
		return VerificationJob{}, errors.New("verification job request failed validation")
	}
	if job.Request.ChainID != chainID || !equalHexBytes(job.Request.Address, address) ||
		!equalHexBytes(job.Request.CodeHash, codeHash) || !equalHexBytes(job.Request.AtBlockHash, blockHash) ||
		string(job.Request.Language) != language || job.Request.CompilerVersion != compilerVersion {
		return VerificationJob{}, errors.New("verification job request does not match bound columns")
	}
	if len(requestDigest) != sha256.Size ||
		!equalDigest(requestDigest, verificationRequestDigest(requestJSON, requiresHardIsolation)) {
		return VerificationJob{}, errors.New("verification job request digest is inconsistent")
	}
	copy(job.RequestDigest[:], requestDigest)
	job.RequiresHardIsolation = requiresHardIsolation
	job.AttemptCount, job.MaxAttempts = attemptCount, maxAttempts
	if attemptCount < 0 || maxAttempts <= 0 || attemptCount > maxAttempts {
		return VerificationJob{}, errors.New("verification job attempt budget is inconsistent")
	}
	if compilerKind.Valid || len(compilerDigest) != 0 || compilerHardIsolated.Valid {
		if !compilerKind.Valid || len(compilerDigest) != sha256.Size || !compilerHardIsolated.Valid {
			return VerificationJob{}, errors.New("verification job compiler provenance is incomplete")
		}
		kind := CompilerKind(compilerKind.String)
		if kind != CompilerProcess && kind != CompilerContainer && kind != CompilerKind("legacy_unrecorded") {
			return VerificationJob{}, errors.New("verification job compiler provenance is invalid")
		}
		provenance := CompilerProvenance{Kind: kind, HardIsolated: compilerHardIsolated.Bool}
		copy(provenance.Digest[:], compilerDigest)
		job.Compiler = &provenance
	}
	if job.RequiresHardIsolation && job.Compiler != nil && !job.Compiler.HardIsolated {
		return VerificationJob{}, errors.New("verification job compiler violates its isolation requirement")
	}
	job.Status = JobStatus(status)
	if !job.Status.valid() {
		return VerificationJob{}, errors.New("verification job contains invalid status")
	}
	if resultKind.Valid {
		kind := MatchKind(resultKind.String)
		if kind != MatchExact && kind != MatchMetadataOnly && kind != MatchMismatch {
			return VerificationJob{}, errors.New("verification job contains invalid result kind")
		}
		job.ResultKind = &kind
	}
	if len(resultJSON) > 0 {
		if len(resultJSON) > repository.options.MaxResultBytes || !json.Valid(resultJSON) {
			return VerificationJob{}, errors.New("verification job result exceeds configured bounds")
		}
		var result VerificationJobResult
		if err := json.Unmarshal(resultJSON, &result); err != nil {
			return VerificationJob{}, errors.New("verification job result is invalid")
		}
		if !validMatchResult(result.Match) || result.Published != (summarizeMatch(result.Match) != MatchMismatch) {
			return VerificationJob{}, errors.New("verification job result is inconsistent")
		}
		job.Result = &result
	}
	if errorCode.Valid {
		job.ErrorCode = ErrorCode(errorCode.String)
		if !job.ErrorCode.valid() {
			return VerificationJob{}, errors.New("verification job contains invalid error code")
		}
	}
	if err := validatePersistedJobState(job); err != nil {
		return VerificationJob{}, err
	}
	return job, nil
}

func validatePersistedJobState(job VerificationJob) error {
	switch job.Status {
	case JobQueued, JobRunning, JobCancelled:
		if job.ResultKind != nil || job.Result != nil || job.ErrorCode != "" {
			return errors.New("verification job state contains terminal output")
		}
	case JobSucceeded:
		if job.ResultKind == nil || job.Result == nil || job.ErrorCode != "" ||
			job.Compiler == nil || !validMatchKind(*job.ResultKind) ||
			*job.ResultKind != summarizeMatch(job.Result.Match) {
			return errors.New("succeeded verification job result is inconsistent")
		}
	case JobFailed:
		if job.ResultKind != nil || job.Result != nil || !job.ErrorCode.valid() {
			return errors.New("failed verification job result is inconsistent")
		}
	default:
		return errors.New("verification job contains invalid status")
	}
	return nil
}

func (status JobStatus) valid() bool {
	switch status {
	case JobQueued, JobRunning, JobSucceeded, JobFailed, JobCancelled:
		return true
	default:
		return false
	}
}

func (repository *PostgresRepository) scanVerifiedContract(row rowScanner) (VerifiedContract, error) {
	var (
		contract               VerifiedContract
		chainIDText, fromText  string
		toText                 sql.NullString
		address, codeHash      []byte
		language, kind         string
		abi, sources, settings []byte
	)
	if err := row.Scan(
		&chainIDText, &address, &codeHash, &fromText, &toText, &language,
		&contract.CompilerVersion, &kind, &contract.ContractName,
		&abi, &sources, &settings, &contract.CreatedAt,
	); err != nil {
		return VerifiedContract{}, err
	}
	chainID, err := strconv.ParseUint(chainIDText, 10, 64)
	if err != nil || chainID == 0 {
		return VerifiedContract{}, errors.New("verified contract contains invalid chain ID")
	}
	from, err := strconv.ParseUint(fromText, 10, 64)
	if err != nil {
		return VerifiedContract{}, errors.New("verified contract contains invalid start block")
	}
	if toText.Valid {
		to, err := strconv.ParseUint(toText.String, 10, 64)
		if err != nil || to < from {
			return VerifiedContract{}, errors.New("verified contract contains invalid end block")
		}
		contract.ValidToBlock = &to
	}
	if len(address) != 20 || len(codeHash) != 32 || !jsonArray(abi) || !jsonObject(sources) || !jsonObject(settings) {
		return VerifiedContract{}, errors.New("verified contract contains invalid persisted data")
	}
	if len(abi)+len(sources)+len(settings) > repository.options.MaxResultBytes {
		return VerifiedContract{}, errors.New("verified contract exceeds configured output bound")
	}
	contract.ChainID = chainID
	contract.Address = "0x" + hex.EncodeToString(address)
	contract.CodeHash = "0x" + hex.EncodeToString(codeHash)
	contract.ValidFromBlock = from
	contract.Language = Language(language)
	contract.MatchKind = MatchKind(kind)
	if contract.Language != LanguageSolidity && contract.Language != LanguageVyper {
		return VerifiedContract{}, errors.New("verified contract contains invalid language")
	}
	if contract.MatchKind != MatchExact && contract.MatchKind != MatchMetadataOnly {
		return VerifiedContract{}, errors.New("verified contract contains invalid match kind")
	}
	contract.ABI = append(json.RawMessage(nil), abi...)
	contract.Sources = append(json.RawMessage(nil), sources...)
	contract.Settings = append(json.RawMessage(nil), settings...)
	return contract, nil
}

func validateVerificationLease(lease VerificationLease) error {
	if !validUUID(lease.Job.ID) {
		return errors.New("verification lease job ID is invalid")
	}
	if lease.Token == "" || len(lease.Token) > 128 {
		return errors.New("verification lease token must contain between 1 and 128 bytes")
	}
	return nil
}

func requireVerificationLease(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read verification lease update count: %w", err)
	}
	if affected != 1 {
		return ErrLeaseLost
	}
	return nil
}

func summarizeMatch(result MatchResult) MatchKind {
	if result.Creation == MatchMismatch || result.Runtime == MatchMismatch {
		return MatchMismatch
	}
	if result.Creation == MatchExact && result.Runtime == MatchExact {
		return MatchExact
	}
	return MatchMetadataOnly
}

func contractName(identifier string) string {
	separator := strings.LastIndex(identifier, ":")
	if separator < 0 || separator == len(identifier)-1 {
		return ""
	}
	return identifier[separator+1:]
}

func decodeFixedHex(value string, size int) ([]byte, error) {
	if len(value) != 2+size*2 || !strings.HasPrefix(value, "0x") {
		return nil, fmt.Errorf("hex value must be %d bytes", size)
	}
	decoded, err := hex.DecodeString(value[2:])
	if err != nil {
		return nil, errors.New("hex value is invalid")
	}
	return decoded, nil
}

func equalHexBytes(value string, expected []byte) bool {
	decoded, err := decodeFixedHex(value, len(expected))
	return err == nil && string(decoded) == string(expected)
}

func positiveMicroseconds(duration time.Duration) (int64, error) {
	if duration <= 0 {
		return 0, errors.New("duration must be positive")
	}
	microseconds := duration / time.Microsecond
	if duration%time.Microsecond != 0 {
		microseconds++
	}
	return int64(microseconds), nil
}

func randomToken(source io.Reader) (string, error) {
	if source == nil {
		return "", errors.New("random source is nil")
	}
	value := make([]byte, 32)
	if _, err := io.ReadFull(source, value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func randomUUID(source io.Reader) (string, error) {
	if source == nil {
		return "", errors.New("random source is nil")
	}
	value := make([]byte, 16)
	if _, err := io.ReadFull(source, value); err != nil {
		return "", err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	encoded := hex.EncodeToString(value)
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	compact := strings.ReplaceAll(value, "-", "")
	_, err := hex.DecodeString(compact)
	return err == nil
}

const verificationJobColumns = `
id::text, chain_id::text, address, code_hash, block_hash, language,
compiler_version, request_payload, status, result_kind, result, error_code,
created_at, updated_at, request_digest, requires_hard_isolation,
attempt_count, max_attempts, compiler_kind, compiler_digest,
compiler_hard_isolated`

const claimedVerificationJobColumns = `
job.id::text, job.chain_id::text, job.address, job.code_hash, job.block_hash, job.language,
job.compiler_version, job.request_payload, job.status, job.result_kind, job.result, job.error_code,
job.created_at, job.updated_at, job.request_digest, job.requires_hard_isolation,
job.attempt_count, job.max_attempts, job.compiler_kind, job.compiler_digest,
job.compiler_hard_isolated`

var submitVerificationSQL = `
	INSERT INTO verification_jobs (
	    id, chain_id, address, code_hash, block_hash, language,
	    compiler_version, request, request_payload, request_digest,
	    requires_hard_isolation, max_attempts
	) VALUES ($1::uuid, $2::numeric, $3, $4, $5, $6, $7, $8::jsonb, $9, $10, $11, $12)
	ON CONFLICT (chain_id, address, code_hash, block_hash, request_digest)
	    WHERE status IN ('queued', 'running', 'succeeded')
	DO NOTHING
	RETURNING ` + verificationJobColumns

var selectVerificationBindingSQL = `
SELECT ` + verificationJobColumns + `
	FROM verification_jobs
	WHERE chain_id = $1::numeric AND address = $2 AND code_hash = $3 AND block_hash = $4
	  AND request_digest = $5
	  AND status IN ('queued', 'running', 'succeeded')`

var selectVerificationJobSQL = `
SELECT ` + verificationJobColumns + `
FROM verification_jobs
WHERE id = $1::uuid`

var claimVerificationSQL = `
WITH exhausted_candidate AS (
	SELECT id
	FROM verification_jobs
	WHERE (status = 'queued' OR (status = 'running' AND lease_expires_at <= clock_timestamp()))
	  AND attempt_count >= max_attempts
	ORDER BY created_at, id
	FOR UPDATE SKIP LOCKED
	LIMIT 1
), exhausted AS (
	UPDATE verification_jobs AS job
	SET status = 'failed', result_kind = NULL, result = NULL,
	    error_code = 'attempts_exhausted', leased_by = NULL,
	    lease_token = NULL, lease_expires_at = NULL,
	    updated_at = clock_timestamp()
	FROM exhausted_candidate
	WHERE job.id = exhausted_candidate.id
	RETURNING job.id
), candidate AS (
	    SELECT id
	    FROM verification_jobs
	    WHERE (status = 'queued'
	       OR (status = 'running' AND lease_expires_at <= clock_timestamp()))
	      AND attempt_count < max_attempts
	      AND NOT EXISTS (SELECT 1 FROM exhausted WHERE exhausted.id = verification_jobs.id)
	    ORDER BY created_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE verification_jobs AS job
SET status = 'running',
    leased_by = $1,
    lease_token = $2,
    lease_expires_at = clock_timestamp() + ($3 * INTERVAL '1 microsecond'),
	    result_kind = NULL,
	    result = NULL,
	    error_code = NULL,
	    attempt_count = job.attempt_count + 1,
	    updated_at = clock_timestamp()
FROM candidate
WHERE job.id = candidate.id
RETURNING ` + claimedVerificationJobColumns

const renewVerificationSQL = `
UPDATE verification_jobs
SET lease_expires_at = clock_timestamp() + ($3 * INTERVAL '1 microsecond'),
    updated_at = clock_timestamp()
WHERE id = $1::uuid
  AND status = 'running'
  AND lease_token = $2
	  AND lease_expires_at > clock_timestamp()`

const selectVerificationCompilerSQL = `
SELECT requires_hard_isolation, compiler_kind, compiler_digest, compiler_hard_isolated
FROM verification_jobs
WHERE id = $1::uuid
  AND status = 'running'
  AND lease_token = $2
  AND lease_expires_at > clock_timestamp()
FOR UPDATE`

const bindVerificationCompilerSQL = `
UPDATE verification_jobs
SET compiler_kind = $3,
    compiler_digest = $4,
    compiler_hard_isolated = $5,
    updated_at = clock_timestamp()
WHERE id = $1::uuid
  AND status = 'running'
  AND lease_token = $2
  AND lease_expires_at > clock_timestamp()
  AND (compiler_digest IS NULL OR
       (compiler_kind = $3 AND compiler_digest = $4 AND compiler_hard_isolated = $5))`

var lockVerificationLeaseSQL = `
SELECT ` + verificationJobColumns + `
FROM verification_jobs
WHERE id = $1::uuid
  AND status = 'running'
  AND lease_token = $2
  AND lease_expires_at > clock_timestamp()
FOR UPDATE`

const completeVerificationSQL = `
UPDATE verification_jobs
SET status = 'succeeded',
    result_kind = $3,
    result = $4::jsonb,
    error_code = NULL,
    leased_by = NULL,
    lease_token = NULL,
    lease_expires_at = NULL,
    updated_at = clock_timestamp()
WHERE id = $1::uuid
  AND status = 'running'
  AND lease_token = $2
  AND lease_expires_at > clock_timestamp()`

const failVerificationSQL = `
UPDATE verification_jobs
SET status = 'failed',
    result_kind = NULL,
    result = NULL,
    error_code = $3,
    leased_by = NULL,
    lease_token = NULL,
    lease_expires_at = NULL,
    updated_at = clock_timestamp()
WHERE id = $1::uuid
  AND status = 'running'
  AND lease_token = $2
  AND lease_expires_at > clock_timestamp()`

const verificationCanonicalTargetSQL = `
SELECT observation.block_number::text
FROM contract_code_observations AS observation
JOIN canonical_blocks AS canonical
  ON canonical.chain_id = observation.chain_id
 AND canonical.number = observation.block_number
 AND canonical.block_hash = observation.block_hash
WHERE observation.chain_id = $1::numeric
  AND observation.address = $2
  AND observation.code_hash = $3
  AND observation.block_hash = $4
  AND observation.canonical = TRUE
	FOR SHARE OF observation, canonical`

const insertVerificationResultSQL = `
INSERT INTO verification_results (
    job_id, chain_id, address, code_hash, block_hash, block_number,
    request_digest, compiler_kind, compiler_digest, compiler_hard_isolated,
    result_kind, result, contract_name, abi, sources, settings
) VALUES (
    $1::uuid, $2::numeric, $3, $4, $5, $6::numeric,
    $7, $8, $9, $10, $11, $12::jsonb, $13, $14::jsonb, $15::jsonb, $16::jsonb
)`

const publishVerifiedContractSQL = `
	INSERT INTO verified_contracts (
	    chain_id, address, code_hash, valid_from_block, language,
	    compiler_version, match_kind, contract_name, abi, sources, settings,
	    verification_job_id, request_digest
	) VALUES ($1::numeric, $2, $3, $4::numeric, $5, $6, $7, $8, $9::jsonb, $10::jsonb, $11::jsonb, $12::uuid, $13)
	ON CONFLICT (chain_id, address, code_hash, valid_from_block) DO UPDATE SET
    language = EXCLUDED.language,
    compiler_version = EXCLUDED.compiler_version,
    match_kind = EXCLUDED.match_kind,
    contract_name = EXCLUDED.contract_name,
	    abi = EXCLUDED.abi,
	    sources = EXCLUDED.sources,
	    settings = EXCLUDED.settings,
	    verification_job_id = EXCLUDED.verification_job_id,
	    request_digest = EXCLUDED.request_digest
	WHERE
	    (verified_contracts.verification_job_id IS NULL AND
	        (verified_contracts.match_kind <> 'exact' OR EXCLUDED.match_kind = 'exact')) OR
	    (verified_contracts.match_kind <> 'exact' AND EXCLUDED.match_kind = 'exact') OR
	    (verified_contracts.match_kind = EXCLUDED.match_kind AND
	        verified_contracts.request_digest IS NOT NULL AND
	        EXCLUDED.request_digest < verified_contracts.request_digest)`

const selectVerifiedContractSQL = `
SELECT chain_id::text, address, code_hash, valid_from_block::text,
       valid_to_block::text, language, compiler_version, match_kind,
       contract_name, abi, sources, settings, created_at
FROM verified_contracts
WHERE chain_id = $1::numeric AND address = $2 AND code_hash = $3
	ORDER BY (match_kind = 'exact') DESC, valid_from_block DESC,
	         request_digest ASC NULLS LAST, created_at ASC
	LIMIT 1`
