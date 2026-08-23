package derivedverify

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/db/gen"
	"github.com/islishude/etherview/internal/verify"
)

type Publisher interface {
	CompleteDerived(context.Context, verify.DerivedTraceIdentity) (string, error)
}

type Options struct {
	WorkerID       string
	LeaseDuration  time.Duration
	PollInterval   time.Duration
	MaxTraces      int
	PublishMatches bool
	Observer       Observer
}

type Observation struct {
	Kind   string
	Result string
}

type Observer interface {
	RecordDerivedVerification(Observation)
}

func (options *Options) defaults() {
	if options.LeaseDuration <= 0 {
		options.LeaseDuration = 30 * time.Second
	}
	if options.PollInterval <= 0 {
		options.PollInterval = time.Second
	}
	if options.MaxTraces <= 0 {
		options.MaxTraces = 100
	}
}

type Worker struct {
	db        *sql.DB
	publisher Publisher
	options   Options
}

func NewWorker(db *sql.DB, publisher Publisher, options Options) (*Worker, error) {
	options.defaults()
	if db == nil || publisher == nil || strings.TrimSpace(options.WorkerID) == "" ||
		len(options.WorkerID) > 128 || options.LeaseDuration < time.Millisecond ||
		options.PollInterval <= 0 || options.MaxTraces <= 0 || options.MaxTraces > 10_000 {
		return nil, errors.New("derived verification worker configuration is invalid")
	}
	return &Worker{db: db, publisher: publisher, options: options}, nil
}

func (worker *Worker) Name() string { return "factory-derived-verification-worker" }

func (worker *Worker) Run(ctx context.Context) error {
	for {
		found, err := worker.ProcessOne(ctx)
		if err != nil && ctx.Err() == nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !found {
			timer := time.NewTimer(worker.options.PollInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
}

type scanLease struct {
	ID                    string
	CompilationID         string
	ChainID               string
	CreatorAddress        []byte
	CreatorCodeHash       []byte
	ValidFromBlock        string
	ValidToBlock          sql.NullString
	CursorBlockNumber     string
	CursorTransactionHash []byte
	CursorTracePath       string
	WorkerID              string
	Token                 string
}

type traceCandidate struct {
	BlockNumber     uint64
	BlockHash       []byte
	TransactionHash []byte
	TracePath       string
	CallType        string
	CreatorAddress  []byte
	CreatedAddress  []byte
	CreationCode    []byte
	RuntimeCode     []byte
}

func (worker *Worker) ProcessOne(ctx context.Context) (bool, error) {
	lease, found, err := worker.claim(ctx)
	if err != nil || !found {
		return found, err
	}
	candidates, err := worker.loadCandidates(ctx, lease.CompilationID)
	if err != nil {
		worker.retry(ctx, lease) //nolint:errcheck
		return true, err
	}
	traces, err := worker.listTraces(ctx, lease)
	if err != nil {
		worker.retry(ctx, lease) //nolint:errcheck
		return true, err
	}
	for _, trace := range traces {
		worker.observe("scan", "trace")
		status, unique, err := classifyTrace(candidates, trace)
		if err != nil {
			worker.retry(ctx, lease) //nolint:errcheck
			return true, err
		}
		if unique && worker.options.PublishMatches {
			_, err = worker.publisher.CompleteDerived(ctx, verify.DerivedTraceIdentity{
				CompilationID: lease.CompilationID, BlockNumber: trace.BlockNumber,
				BlockHash: trace.BlockHash, Transaction: trace.TransactionHash,
				TracePath: trace.TracePath,
			})
			if errors.Is(err, verify.ErrDerivedEvidenceStale) {
				status, unique, err = "stale", false, nil
			} else if errors.Is(err, verify.ErrDerivedNotUnique) {
				status, unique, err = "ambiguous", false, nil
			}
			if err != nil {
				worker.retry(ctx, lease) //nolint:errcheck
				return true, fmt.Errorf("publish derived verification: %w", err)
			}
			if unique {
				worker.observe("publish", "matched")
			}
		}
		if !unique {
			status, err = worker.recordAttempt(ctx, lease, trace, status)
			if err != nil {
				worker.retry(ctx, lease) //nolint:errcheck
				return true, err
			}
		}
		worker.observe("match", status)
	}
	done := len(traces) < worker.options.MaxTraces
	cursorBlock, cursorTransaction, cursorPath := lease.CursorBlockNumber,
		lease.CursorTransactionHash, lease.CursorTracePath
	if len(traces) > 0 {
		last := traces[len(traces)-1]
		cursorBlock = strconv.FormatUint(last.BlockNumber, 10)
		cursorTransaction = last.TransactionHash
		cursorPath = last.TracePath
	}
	result, err := worker.db.ExecContext(ctx, dbgen.DerivedVerifyAdvanceScan,
		lease.ID, lease.Token, lease.WorkerID, done,
		cursorBlock, cursorTransaction, cursorPath,
	)
	if err != nil {
		return true, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return true, errors.New("derived verification scan lease was lost")
	}
	return true, nil
}

func (worker *Worker) observe(kind, result string) {
	if worker.options.Observer != nil {
		worker.options.Observer.RecordDerivedVerification(Observation{Kind: kind, Result: result})
	}
}

func (worker *Worker) claim(ctx context.Context) (scanLease, bool, error) {
	token := uuid.NewString()
	microseconds := worker.options.LeaseDuration.Microseconds()
	var lease scanLease
	err := worker.db.QueryRowContext(ctx, dbgen.DerivedVerifyClaimScan,
		worker.options.WorkerID, token, microseconds,
	).Scan(
		&lease.ID, &lease.CompilationID, &lease.ChainID, &lease.CreatorAddress,
		&lease.CreatorCodeHash, &lease.ValidFromBlock, &lease.ValidToBlock,
		&lease.CursorBlockNumber, &lease.CursorTransactionHash,
		&lease.CursorTracePath,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return scanLease{}, false, nil
	}
	if err != nil {
		return scanLease{}, false, err
	}
	lease.WorkerID, lease.Token = worker.options.WorkerID, token
	if len(lease.CreatorAddress) != 20 || len(lease.CreatorCodeHash) != 32 ||
		len(lease.CursorTransactionHash) != 32 {
		return scanLease{}, false, errors.New("stored derived verification scan is invalid")
	}
	return lease, true, nil
}

func (worker *Worker) loadCandidates(
	ctx context.Context,
	compilationID string,
) ([]verify.CandidateArtifact, error) {
	rows, err := worker.db.QueryContext(ctx, dbgen.DerivedVerifyLoadCompilationCandidates, compilationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var candidates []verify.CandidateArtifact
	for rows.Next() {
		var language verify.Language
		var version string
		var standardJSON, creation, runtime []byte
		var candidate verify.CandidateArtifact
		if err := rows.Scan(
			&language, &version, &standardJSON, &candidate.FileName,
			&candidate.ContractName, &candidate.ABI, &creation, &runtime,
			&candidate.CompilationArtifacts, &candidate.CreationCodeArtifacts,
			&candidate.RuntimeCodeArtifacts,
		); err != nil {
			return nil, err
		}
		if language != verify.LanguageSolidity || !json.Valid(standardJSON) {
			return nil, errors.New("stored derived verification compilation is invalid")
		}
		candidate.Language, candidate.CompilerVersion = language, version
		candidate.CreationBytecode = "0x" + hex.EncodeToString(creation)
		candidate.RuntimeBytecode = "0x" + hex.EncodeToString(runtime)
		candidate, err = verify.RestoreCandidateArtifact(candidate)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(candidates) == 0 || len(candidates) > 4096 {
		return nil, errors.New("stored derived verification candidates are invalid")
	}
	return candidates, nil
}

func (worker *Worker) listTraces(ctx context.Context, lease scanLease) ([]traceCandidate, error) {
	var validTo any
	if lease.ValidToBlock.Valid {
		validTo = lease.ValidToBlock.String
	}
	rows, err := worker.db.QueryContext(ctx, dbgen.DerivedVerifyListHistoricalTraces,
		lease.CompilationID, lease.ChainID, lease.CreatorAddress,
		lease.CreatorCodeHash, lease.ValidFromBlock, validTo, lease.CursorBlockNumber,
		lease.CursorTransactionHash, lease.CursorTracePath, worker.options.MaxTraces,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var traces []traceCandidate
	for rows.Next() {
		var trace traceCandidate
		var blockNumber string
		if err := rows.Scan(
			&blockNumber, &trace.BlockHash, &trace.TransactionHash,
			&trace.TracePath, &trace.CallType, &trace.CreatorAddress,
			&trace.CreatedAddress, &trace.CreationCode, &trace.RuntimeCode,
		); err != nil {
			return nil, err
		}
		trace.BlockNumber, err = strconv.ParseUint(blockNumber, 10, 64)
		if err != nil || len(trace.BlockHash) != 32 || len(trace.TransactionHash) != 32 ||
			len(trace.CreatorAddress) != 20 || len(trace.CreatedAddress) != 20 ||
			len(trace.CreationCode) == 0 ||
			(trace.CallType != "CREATE" && trace.CallType != "CREATE2") {
			return nil, errors.New("stored derived verification trace is invalid")
		}
		traces = append(traces, trace)
	}
	return traces, rows.Err()
}

func classifyTrace(
	candidates []verify.CandidateArtifact,
	trace traceCandidate,
) (string, bool, error) {
	if len(trace.RuntimeCode) == 0 {
		return "pending_runtime", false, nil
	}
	creationMatches, confirmed := 0, 0
	for _, candidate := range candidates {
		match, ok, err := verify.MatchCandidate(candidate, verify.MatchInput{
			Creation: "0x" + hex.EncodeToString(trace.CreationCode),
			Runtime:  "0x" + hex.EncodeToString(trace.RuntimeCode),
		}, false)
		if err != nil {
			return "", false, err
		}
		if ok && match.Creation != nil {
			creationMatches++
			if match.Runtime != nil {
				confirmed++
			}
		}
	}
	switch {
	case creationMatches == 0:
		return "no_match", false, nil
	case confirmed == 0:
		return "runtime_mismatch", false, nil
	case confirmed > 1:
		return "ambiguous", false, nil
	default:
		return "matched", true, nil
	}
}

func (worker *Worker) recordAttempt(
	ctx context.Context,
	lease scanLease,
	trace traceCandidate,
	status string,
) (string, error) {
	var stored string
	err := worker.db.QueryRowContext(ctx, dbgen.DerivedVerifyRecordAttempt,
		uuid.NewString(), lease.ChainID, strconv.FormatUint(trace.BlockNumber, 10),
		trace.BlockHash, trace.TransactionHash, trace.TracePath,
		trace.CreatorAddress, trace.CreatedAddress, trace.CallType,
		lease.CompilationID, status,
	).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return "stale", nil
	}
	if err != nil {
		return "", fmt.Errorf("record derived verification attempt %s: %w", status, err)
	}
	if stored != status && stored != "stale" {
		return "", errors.New("stored derived verification attempt status is invalid")
	}
	return stored, nil
}

func (worker *Worker) retry(ctx context.Context, lease scanLease) error {
	result, err := worker.db.ExecContext(ctx, dbgen.DerivedVerifyRetryScan,
		lease.ID, lease.Token, lease.WorkerID, "processing_failed",
	)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return fmt.Errorf("retry derived verification scan: lease lost")
	}
	return nil
}
