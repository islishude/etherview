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
	"strings"
	"time"

	"github.com/islishude/etherview/internal/contractartifact"
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
	JobSourcify                  JobKind = "sourcify"
	JobSourcifyFromEtherscan     JobKind = "sourcify_from_etherscan"
	JobProxy                     JobKind = "proxy"
)

type ErrorCode string

const (
	ErrorCompileFailed    ErrorCode = "compile_failed"
	ErrorCompilerOutput   ErrorCode = "compiler_output_invalid"
	ErrorCompilerTooLarge ErrorCode = "compiler_output_too_large"
	ErrorMatchFailed      ErrorCode = "match_failed"
	// ErrorSandboxRequired remains readable only for immutable pre-0031 jobs.
	ErrorSandboxRequired            ErrorCode = "sandbox_required"
	ErrorCompilerProvenanceMismatch ErrorCode = "compiler_provenance_mismatch"
	ErrorCompilerUnavailable        ErrorCode = "compiler_unavailable"
	ErrorTargetNotCanonical         ErrorCode = "target_not_canonical"
	ErrorAttemptsExhausted          ErrorCode = "attempts_exhausted"
	ErrorExecutorMigrated           ErrorCode = "executor_migrated"
)

func (code ErrorCode) valid() bool {
	switch code {
	case ErrorCompileFailed, ErrorCompilerOutput, ErrorCompilerTooLarge, ErrorMatchFailed,
		ErrorSandboxRequired, ErrorCompilerProvenanceMismatch, ErrorCompilerUnavailable,
		ErrorTargetNotCanonical, ErrorAttemptsExhausted, ErrorExecutorMigrated:
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
	ID            string
	Kind          JobKind
	Request       Request
	RequestV2     *SubmissionV2
	RequestDigest [sha256.Size]byte
	AttemptCount  int
	MaxAttempts   int
	Compiler      *CompilerProvenance
	Status        JobStatus
	ResultKind    *MatchKind
	Result        *VerificationJobResult
	Outcome       json.RawMessage
	ErrorCode     ErrorCode
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type VerificationLease struct {
	Job   VerificationJob
	Token string
}

type VerifiedContract struct {
	Resolution            string
	Target                ContractCodeIdentity
	Source                VerifiedArtifactSource
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

type ContractCodeIdentity struct {
	ChainID     uint64
	Address     string
	CodeHash    string
	BlockNumber uint64
	BlockHash   string
}

type VerifiedArtifactSource struct {
	Address        string
	CodeHash       string
	ValidFromBlock uint64
	ValidToBlock   *uint64
	CreatedAt      time.Time
}

type Repository interface {
	Claim(context.Context, string, time.Duration) (VerificationLease, bool, error)
	Renew(context.Context, VerificationLease, time.Duration) error
	BindCompiler(context.Context, VerificationLease, CompilerProvenance) error
	Fail(context.Context, VerificationLease, ErrorCode) error
	Job(context.Context, string) (VerificationJob, bool, error)
	VerifiedContract(context.Context, uint64, string) (VerifiedContract, bool, error)
}

type RepositoryOptions struct {
	MaxRequestBytes int
	MaxResultBytes  int
	MaxAttempts     int
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
	db        *sql.DB
	artifacts *contractartifact.Resolver
	options   RepositoryOptions
	random    io.Reader
}

func NewPostgresRepository(db *sql.DB, options RepositoryOptions) (*PostgresRepository, error) {
	if db == nil {
		return nil, errors.New("verification repository requires a database")
	}
	options.defaults()
	if options.MaxRequestBytes <= 0 || options.MaxResultBytes <= 0 || options.MaxAttempts > 100 {
		return nil, errors.New("verification repository limits must be positive")
	}
	artifacts, err := contractartifact.NewResolver(db)
	if err != nil {
		return nil, err
	}
	return &PostgresRepository{db: db, artifacts: artifacts, options: options, random: rand.Reader}, nil
}

func verificationRequestDigest(payload []byte, _ ...bool) [sha256.Size]byte {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("etherview:verification-request:v1"))
	_, _ = hasher.Write(payload)
	var digest [sha256.Size]byte
	copy(digest[:], hasher.Sum(nil))
	return digest
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

type rowScanner interface{ Scan(...any) error }

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
