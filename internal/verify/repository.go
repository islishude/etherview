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

type JobKind string

const (
	JobAddress                   JobKind = "address"
	JobSolidityMultipart         JobKind = "solidity_multipart"
	JobSolidityStandardJSON      JobKind = "solidity_standard_json"
	JobSolidityBatchMultipart    JobKind = "solidity_batch_multipart"
	JobSolidityBatchStandardJSON JobKind = "solidity_batch_standard_json"
	JobVyperMultipart            JobKind = "vyper_multipart"
	JobVyperStandardJSON         JobKind = "vyper_standard_json"
	JobSourcify                  JobKind = "sourcify"
	JobSourcifyFromEtherscan     JobKind = "sourcify_from_etherscan"
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
	Kind                  JobKind
	Request               Request
	RequestV2             *SubmissionV2
	RequestDigest         [sha256.Size]byte
	RequiresHardIsolation bool
	AttemptCount          int
	MaxAttempts           int
	Compiler              *CompilerProvenance
	Status                JobStatus
	ResultKind            *MatchKind
	Result                *VerificationJobResult
	Outcome               json.RawMessage
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
	ChainID               uint64
	Address               string
	CodeHash              string
	ValidFromBlock        uint64
	ValidToBlock          *uint64
	Language              Language
	CompilerVersion       string
	MatchKind             MatchKind
	MatchType             VerificationMatchType
	FileName              string
	ContractName          string
	ABI                   json.RawMessage
	Sources               json.RawMessage
	Settings              json.RawMessage
	CompilationArtifacts  json.RawMessage
	CreationCodeArtifacts json.RawMessage
	RuntimeCodeArtifacts  json.RawMessage
	CreationMatch         *VerificationMatchDetails
	RuntimeMatch          *VerificationMatchDetails
	ConstructorArguments  string
	Libraries             map[string]string
	IsBlueprint           bool
	CreatedAt             time.Time
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
	defer tx.Rollback() //nolint:errcheck
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
	for range 2 {
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
	standardJSON, err := PrepareStandardJSON(
		request.StandardJSON,
		request.Language,
		request.CompilerVersion,
		request.ContractIdentifier,
		repository.options.MaxRequestBytes,
	)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	request.StandardJSON = standardJSON
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

func equalDigest(value []byte, digest [sha256.Size]byte) bool {
	return len(value) == len(digest) && string(value) == string(digest[:])
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

const renewVerificationSQL = `
UPDATE verification_jobs
SET lease_expires_at = clock_timestamp() + ($3 * INTERVAL '1 microsecond'),
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
