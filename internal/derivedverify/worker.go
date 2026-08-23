package derivedverify

import (
	"bytes"
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
	CompleteDerived(
		context.Context,
		verify.DerivedTraceIdentity,
		verify.PreparedDerivedMatch,
	) (string, error)
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
		len(options.WorkerID) > 128 || options.LeaseDuration < 3*time.Millisecond ||
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
	err = runWithLeaseHeartbeat(
		ctx, worker.options.LeaseDuration,
		func(renewContext context.Context) error {
			return worker.renew(renewContext, lease)
		},
		worker.options.Observer,
		func(operationContext context.Context, guard *leaseHeartbeatGuard) error {
			return worker.processLease(operationContext, guard, lease)
		},
	)
	return true, err
}

func (worker *Worker) processLease(
	ctx context.Context,
	guard *leaseHeartbeatGuard,
	lease scanLease,
) error {
	compilation, err := worker.loadCandidates(ctx, lease.CompilationID)
	if err != nil {
		return worker.failLease(ctx, guard, lease, err)
	}
	traces, err := worker.listTraces(ctx, lease)
	if err != nil {
		return worker.failLease(ctx, guard, lease, err)
	}
	for _, trace := range traces {
		worker.observe("scan", "trace")
		prepared, status, err := verify.PrepareDerivedMatch(
			compilation.Candidates, compilation.StandardJSON,
			verify.MatchInput{
				Creation: "0x" + hex.EncodeToString(trace.CreationCode),
				Runtime:  "0x" + hex.EncodeToString(trace.RuntimeCode),
			},
		)
		if err != nil {
			return worker.failLease(ctx, guard, lease, err)
		}
		unique := status == "matched"
		if unique && worker.options.PublishMatches {
			err = guard.exclusive(func() error {
				if renewErr := worker.renew(ctx, lease); renewErr != nil {
					return renewErr
				}
				_, publishErr := worker.publisher.CompleteDerived(
					ctx,
					verify.DerivedTraceIdentity{
						CompilationID: lease.CompilationID, BlockNumber: trace.BlockNumber,
						BlockHash: trace.BlockHash, Transaction: trace.TransactionHash,
						TracePath: trace.TracePath,
					},
					prepared,
				)
				return publishErr
			})
			if errors.Is(err, verify.ErrDerivedEvidenceStale) {
				status, unique, err = "stale", false, nil
			} else if errors.Is(err, verify.ErrDerivedNotUnique) {
				status, unique, err = "ambiguous", false, nil
			}
			if err != nil {
				return worker.failLease(
					ctx, guard, lease, fmt.Errorf("publish derived verification: %w", err),
				)
			}
			if unique {
				worker.observe("publish", "matched")
			}
		}
		if !unique {
			err = guard.exclusive(func() error {
				var recordErr error
				status, recordErr = worker.recordAttempt(ctx, lease, trace, status)
				return recordErr
			})
			if err != nil {
				return worker.failLease(ctx, guard, lease, err)
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
	return guard.finalize(func() error {
		if err := worker.renew(ctx, lease); err != nil {
			return err
		}
		result, err := worker.db.ExecContext(ctx, dbgen.DerivedVerifyAdvanceScan,
			lease.ID, lease.Token, lease.WorkerID, done,
			cursorBlock, cursorTransaction, cursorPath,
		)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			worker.observe("lease", "lost")
			return errors.New("derived verification scan lease was lost")
		}
		return nil
	})
}

func (worker *Worker) failLease(
	ctx context.Context,
	guard *leaseHeartbeatGuard,
	lease scanLease,
	cause error,
) error {
	retryErr := guard.finalize(func() error { return worker.retry(ctx, lease) })
	return errors.Join(cause, retryErr)
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

func (worker *Worker) renew(ctx context.Context, lease scanLease) error {
	result, err := worker.db.ExecContext(
		ctx, dbgen.DerivedVerifyRenewScan,
		lease.ID, lease.Token, lease.WorkerID, worker.options.LeaseDuration.Microseconds(),
	)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		worker.observe("lease", "lost")
		return errors.New("derived verification scan lease was lost")
	}
	return nil
}

func (worker *Worker) loadCandidates(
	ctx context.Context,
	compilationID string,
) (verify.AuthenticatedCompilation, error) {
	rows, err := worker.db.QueryContext(ctx, dbgen.DerivedVerifyLoadCompilationCandidates, compilationID)
	if err != nil {
		return verify.AuthenticatedCompilation{}, err
	}
	defer rows.Close() //nolint:errcheck
	var compilation verify.AuthenticatedCompilation
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
			return verify.AuthenticatedCompilation{}, err
		}
		if language != verify.LanguageSolidity || !json.Valid(standardJSON) {
			return verify.AuthenticatedCompilation{}, errors.New("stored derived verification compilation is invalid")
		}
		if len(compilation.StandardJSON) == 0 {
			compilation.StandardJSON = append(json.RawMessage(nil), standardJSON...)
		} else if !bytes.Equal(compilation.StandardJSON, standardJSON) {
			return verify.AuthenticatedCompilation{}, errors.New("stored derived verification Standard JSON conflicts")
		}
		candidate.Language, candidate.CompilerVersion = language, version
		candidate.CreationBytecode = "0x" + hex.EncodeToString(creation)
		candidate.RuntimeBytecode = "0x" + hex.EncodeToString(runtime)
		candidate, err = verify.RestoreCandidateArtifact(candidate)
		if err != nil {
			return verify.AuthenticatedCompilation{}, err
		}
		compilation.Candidates = append(compilation.Candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return verify.AuthenticatedCompilation{}, err
	}
	if len(compilation.Candidates) == 0 || len(compilation.Candidates) > 4096 {
		return verify.AuthenticatedCompilation{}, errors.New("stored derived verification candidates are invalid")
	}
	return compilation, nil
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
