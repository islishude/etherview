package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
	dbaccess "github.com/islishude/etherview/internal/db"
	dbgen "github.com/islishude/etherview/internal/db/gen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type BytecodeDBObserver interface {
	RecordBytecodeDB(string)
	SetBytecodeDBBacklog(int64)
}

type BytecodeDBWorkerOptions struct {
	Name            string
	ChainID         uint64
	MaxConcurrency  int
	RequestInterval time.Duration
	Cooldown        time.Duration
	MaxInputBytes   int
	Observer        BytecodeDBObserver
	Logger          *slog.Logger
}

type BytecodeDBWorker struct {
	repository *PostgresRepository
	client     *BytecodeDBClient
	compiler   Compiler
	options    BytecodeDBWorkerOptions
	chain      pgtype.Numeric
	service    []byte
}

func NewBytecodeDBWorker(repository *PostgresRepository, client *BytecodeDBClient, compiler Compiler, options BytecodeDBWorkerOptions) (*BytecodeDBWorker, error) {
	if repository == nil || client == nil || compiler == nil || options.ChainID == 0 || options.MaxConcurrency < 1 || options.MaxConcurrency > 16 || options.RequestInterval <= 0 || options.Cooldown <= 0 || options.MaxInputBytes <= 0 {
		return nil, errors.New("invalid bytecode database worker configuration")
	}
	var chain pgtype.Numeric
	if err := chain.Scan(strconv.FormatUint(options.ChainID, 10)); err != nil {
		return nil, err
	}
	return &BytecodeDBWorker{repository: repository, client: client, compiler: compiler, options: options, chain: chain, service: client.Identity()}, nil
}
func (w *BytecodeDBWorker) Name() string { return w.options.Name }
func (w *BytecodeDBWorker) observe(result string) {
	if w.options.Observer != nil {
		w.options.Observer.RecordBytecodeDB(result)
	}
}

func (w *BytecodeDBWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := w.tick(ctx); err != nil && ctx.Err() == nil {
			w.observe("storage_error")
			if w.options.Logger != nil {
				w.options.Logger.Debug("Bytecode source discovery deferred", "component", w.Name(), "error_code", "storage_unavailable")
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (w *BytecodeDBWorker) tick(ctx context.Context) error {
	q := dbgen.New(w.repository.db)
	if err := q.BytecodeDBInit(ctx, w.service); err != nil {
		return err
	}
	if err := w.discover(ctx); err != nil {
		return err
	}
	if pending, err := q.BytecodeDBBacklog(ctx, w.service, w.chain); err == nil && w.options.Observer != nil {
		w.options.Observer.SetBytecodeDBBacklog(pending)
	}
	token, err := randomToken(w.repository.random)
	if err != nil {
		return err
	}
	task, err := q.BytecodeDBClaimTarget(ctx, new(token), w.service, w.chain)
	if err == nil {
		operation, stop := bytecodeDBHeartbeat(ctx, func(ctx context.Context) (int64, error) {
			return q.BytecodeDBRenewTarget(ctx, dbgen.BytecodeDBRenewTargetParams{ServiceID: w.service, ChainID: task.ChainID, Address: task.Address, LeaseToken: task.LeaseToken})
		})
		outcome, delay, processErr := w.processTarget(operation, task)
		leaseErr := stop()
		if leaseErr != nil {
			return leaseErr
		}
		if processErr != nil {
			return processErr
		}
		affected, finishErr := q.BytecodeDBFinishTarget(ctx, dbgen.BytecodeDBFinishTargetParams{ServiceID: w.service, ChainID: task.ChainID, Address: task.Address, Token: task.LeaseToken, Outcome: outcome, DelayUs: delay.Microseconds()})
		if finishErr != nil {
			return finishErr
		}
		if affected != 1 {
			return ErrLeaseLost
		}
		w.observe(outcome)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	lookup, found, err := w.claimLookup(ctx)
	if err != nil || !found {
		return err
	}
	return w.search(ctx, lookup)
}

func (w *BytecodeDBWorker) discover(ctx context.Context) error {
	if err := dbgen.New(w.repository.db).BytecodeDBInitScan(ctx, w.service, w.chain); err != nil {
		return err
	}
	return dbaccess.WithTransaction(ctx, w.repository.db, func(q *dbgen.Queries) error {
		state, err := q.BytecodeDBLockScan(ctx, w.service, w.chain)
		if err != nil {
			return err
		}
		ready, err := q.BytecodeDBScanReady(ctx, w.service, w.chain)
		if err != nil || !ready {
			return err
		}
		addresses, err := q.BytecodeDBDiscover(ctx, w.chain, state.CursorAddress)
		if err != nil {
			return err
		}
		cursor := []byte{}
		delay := time.Second
		for _, address := range addresses {
			if err = q.BytecodeDBEnqueueTarget(ctx, w.service, w.chain, address); err != nil {
				return err
			}
			cursor = address
		}
		if len(addresses) < 100 {
			cursor = []byte{}
			delay = time.Minute
		}
		return q.BytecodeDBAdvance(ctx, dbgen.BytecodeDBAdvanceParams{CursorAddress: cursor, DelayUs: delay.Microseconds(), ServiceID: w.service, ChainID: w.chain})
	})
}

func (w *BytecodeDBWorker) claimLookup(ctx context.Context) (dbgen.BytecodeDbLookup, bool, error) {
	var lookup dbgen.BytecodeDbLookup
	found := false
	err := dbaccess.WithTransaction(ctx, w.repository.db, func(q *dbgen.Queries) error {
		if _, err := q.BytecodeDBLockService(ctx, w.service); err != nil {
			return err
		}
		ready, err := q.BytecodeDBRequestReady(ctx, w.service)
		if err != nil {
			return err
		}
		count, err := q.BytecodeDBActiveLookups(ctx, w.service)
		if err != nil {
			return err
		}
		if !ready || count >= int64(w.options.MaxConcurrency) {
			w.observe("limited")
			return nil
		}
		token, err := randomToken(w.repository.random)
		if err != nil {
			return err
		}
		lookup, err = q.BytecodeDBClaimLookup(ctx, new(token), w.service)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		return q.BytecodeDBAdmitRequest(ctx, w.options.RequestInterval.Microseconds(), w.service)
	})
	return lookup, found, err
}

func (w *BytecodeDBWorker) search(ctx context.Context, lookup dbgen.BytecodeDbLookup) error {
	operation, stop := bytecodeDBHeartbeat(ctx, func(ctx context.Context) (int64, error) {
		return dbgen.New(w.repository.db).BytecodeDBRenewLookup(ctx, dbgen.BytecodeDBRenewLookupParams{ServiceID: w.service, BytecodeType: lookup.BytecodeType, Digest: lookup.Digest, LeaseToken: lookup.LeaseToken})
	})
	sources, searchErr := w.client.Search(operation, lookup.Bytecode, lookup.BytecodeType)
	if err := stop(); err != nil {
		return err
	}
	outcome := "not_found"
	limited := false
	var encoded []byte
	failures := int32(0)
	delay := w.options.Cooldown
	if searchErr == nil {
		sources, limited = bytecodeDBCandidates(sources)
		encoded, _ = json.Marshal(sources)
		if len(sources) > 0 {
			outcome = "hit"
		}
		if limited {
			w.observe("candidate_limited")
		}
	} else {
		var upstream BytecodeDBError
		outcome = "invalid_response"
		if errors.As(searchErr, &upstream) {
			outcome = upstream.Code
		}
		failures = lookup.Failures + 1
		if failures < 3 && (outcome == "unavailable" || outcome == "rate_limited") {
			delay = time.Second * time.Duration(1<<failures)
		} else {
			failures = 0
		}
		delay = max(delay, upstream.RetryAfter)
	}
	err := dbaccess.WithTransaction(ctx, w.repository.db, func(q *dbgen.Queries) error {
		if outcome == "rate_limited" {
			if _, err := q.BytecodeDBLockService(ctx, w.service); err != nil {
				return err
			}
		}
		affected, err := q.BytecodeDBFinishLookup(ctx, dbgen.BytecodeDBFinishLookupParams{ServiceID: w.service, BytecodeType: lookup.BytecodeType, Digest: lookup.Digest, Token: lookup.LeaseToken, Retry: failures > 0, Sources: encoded, Limited: limited, Outcome: outcome, Failures: failures, DelayUs: delay.Microseconds()})
		if err != nil {
			return err
		}
		if affected != 1 {
			return ErrLeaseLost
		}
		if outcome == "rate_limited" {
			return q.BytecodeDBBackoffService(ctx, delay.Microseconds(), w.service)
		}
		return nil
	})
	if err != nil {
		return err
	}
	w.observe(outcome)
	return nil
}

func (w *BytecodeDBWorker) processTarget(ctx context.Context, task dbgen.BytecodeDbTarget) (string, time.Duration, error) {
	target, err := ResolveTarget(ctx, w.repository.db, w.options.ChainID, common.BytesToAddress(task.Address).Hex(), w.options.MaxInputBytes)
	if errors.Is(err, ErrTargetUnavailable) {
		return "waiting_facts", time.Minute, nil
	}
	if err != nil {
		return "", 0, err
	}
	q := dbgen.New(w.repository.db)
	published, err := q.BytecodeDBHasPublication(ctx, dbgen.BytecodeDBHasPublicationParams{ChainID: w.chain, Address: task.Address, CodeHash: common.FromHex(target.CodeHash), BlockHash: common.FromHex(target.AtBlockHash)})
	if err != nil {
		return "", 0, err
	}
	if published {
		return "verified", w.options.Cooldown, nil
	}
	seen := map[string]bool{}
	outcome := "not_found"
	for _, input := range []struct{ kind, code string }{{"DEPLOYED_BYTECODE", target.RuntimeBytecode}, {"CREATION_INPUT", target.CreationBytecode}} {
		if input.code == "" {
			continue
		}
		code, err := decodeBytecode(input.code)
		if err != nil {
			return "waiting_facts", time.Minute, nil
		}
		digest := sha256.Sum256(code)
		if err = q.BytecodeDBEnqueueLookup(ctx, dbgen.BytecodeDBEnqueueLookupParams{ServiceID: w.service, BytecodeType: input.kind, Digest: digest[:], Bytecode: code}); err != nil {
			return "", 0, err
		}
		lookup, err := q.BytecodeDBGetLookup(ctx, w.service, input.kind, digest[:])
		if err != nil {
			return "", 0, err
		}
		if lookup.Sources == nil {
			if lookup.Outcome == "pending" || lookup.LeaseToken != nil {
				return "lookup_pending", 5 * time.Second, nil
			}
			return lookup.Outcome, time.Minute, nil
		}
		var sources []BytecodeDBSource
		if err = json.Unmarshal(lookup.Sources, &sources); err != nil {
			return "", 0, err
		}
		for _, source := range sources {
			key := hex.EncodeToString(bytecodeDBSourceDigest(source))
			if seen[key] {
				continue
			}
			if len(seen) == 5 {
				return "candidate_limited", w.options.Cooldown, nil
			}
			seen[key] = true
			state, err := w.tryCandidate(ctx, task, target, source)
			if err != nil {
				return "", 0, err
			}
			if state == "verification_pending" {
				return state, 5 * time.Second, nil
			}
			if state == "verified" {
				return state, w.options.Cooldown, nil
			}
			outcome = state
		}
		if lookup.Limited {
			outcome = "candidate_limited"
		}
	}
	return outcome, w.options.Cooldown, nil
}

func bytecodeDBSupportDigest(p CompilerProvenance) []byte {
	raw, _ := json.Marshal(struct {
		Compiler, Executor     [32]byte
		Kind, Policy, Platform string
	}{p.Digest, p.ExecutorDigest, p.ExecutorKind, p.ExecutionPolicy, p.Platform})
	sum := sha256.Sum256(raw)
	return sum[:]
}

func (w *BytecodeDBWorker) tryCandidate(ctx context.Context, task dbgen.BytecodeDbTarget, target VerificationTarget, source BytecodeDBSource) (string, error) {
	request, err := bytecodeDBSubmission(source, target, w.options.MaxInputBytes)
	if err != nil {
		return "invalid_candidate", nil
	}
	service := Service{repository: w.repository, maxInputBytes: w.options.MaxInputBytes}
	if err = service.prepareV2(ctx, &request); err != nil {
		return "unsupported_candidate", nil
	}
	var provenance CompilerProvenance
	if pinned, ok := w.compiler.(PinnedCompiler); ok {
		provenance, err = pinned.Resolve(ctx, request.Language, request.CompilerVersion)
	} else {
		provenance, err = w.compiler.Provenance(request.Language, request.CompilerVersion)
	}
	if err != nil {
		return "compiler_unavailable", nil
	}
	support := bytecodeDBSupportDigest(provenance)
	candidate := bytecodeDBSourceDigest(source)
	raw, _ := json.Marshal(target)
	identity := sha256.Sum256(raw)
	params := dbgen.BytecodeDBAttemptParams{ServiceID: w.service, TargetDigest: identity[:], CandidateDigest: candidate, SupportDigest: support}
	attempt, err := dbgen.New(w.repository.db).BytecodeDBAttempt(ctx, params)
	if err == nil {
		if attempt.Status == "queued" || attempt.Status == "running" {
			return "verification_pending", nil
		}
		if attempt.OutcomeKind != nil && *attempt.OutcomeKind == "verification_success" {
			return "verified", nil
		}
		if attempt.Status == "failed" {
			return w.retryUnavailable(ctx, task, target, params)
		}
		return "verification_failed", nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	request.BytecodeDB = &BytecodeDBProvenance{Provider: hex.EncodeToString(w.service), Candidate: hex.EncodeToString(candidate), Support: hex.EncodeToString(support)}
	tx, err := w.repository.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", err
	}
	defer dbaccess.Rollback(ctx, tx)
	q := dbgen.New(tx)
	if _, err = q.BytecodeDBLockTarget(ctx, dbgen.BytecodeDBLockTargetParams{ServiceID: w.service, ChainID: task.ChainID, Address: task.Address, LeaseToken: task.LeaseToken}); err != nil {
		return "", ErrLeaseLost
	}
	if _, err = canonicalV2Target(ctx, tx, &target); err != nil {
		return "waiting_facts", nil
	}
	job, _, err := w.repository.submitV2(ctx, q, request)
	if err != nil {
		return "", err
	}
	var jobID pgtype.UUID
	if err = jobID.Scan(job.ID); err != nil {
		return "", err
	}
	if err = q.BytecodeDBAddAttempt(ctx, dbgen.BytecodeDBAddAttemptParams{ServiceID: w.service, TargetDigest: identity[:], CandidateDigest: candidate, SupportDigest: support, JobID: jobID}); err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return "verification_pending", nil
}

// Retry the same immutable candidate/job association only after an infrastructure
// failure has cooled down. Each retry wave retains its bound compiler identity.
func (w *BytecodeDBWorker) retryUnavailable(ctx context.Context, task dbgen.BytecodeDbTarget, target VerificationTarget, attempt dbgen.BytecodeDBAttemptParams) (string, error) {
	tx, err := w.repository.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", err
	}
	defer dbaccess.Rollback(ctx, tx)
	q := dbgen.New(tx)
	if _, err = q.BytecodeDBLockTarget(ctx, dbgen.BytecodeDBLockTargetParams{ServiceID: w.service, ChainID: task.ChainID, Address: task.Address, LeaseToken: task.LeaseToken}); err != nil {
		return "", ErrLeaseLost
	}
	if _, err = canonicalV2Target(ctx, tx, &target); err != nil {
		return "waiting_facts", nil
	}
	count, err := q.BytecodeDBRetryUnavailable(ctx, dbgen.BytecodeDBRetryUnavailableParams{
		ServiceID: attempt.ServiceID, TargetDigest: attempt.TargetDigest, CandidateDigest: attempt.CandidateDigest,
		SupportDigest: attempt.SupportDigest, CooldownUs: w.options.Cooldown.Microseconds(),
	})
	if err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	if count == 1 {
		return "verification_pending", nil
	}
	return "verification_failed", nil
}

// The operation owns no snapshot while waiting for remote data or preparing inputs.
func bytecodeDBHeartbeat(ctx context.Context, renew func(context.Context) (int64, error)) (context.Context, func() error) {
	operation, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopped:
				return
			case <-operation.Done():
				return
			case <-ticker.C:
				renewContext, renewCancel := context.WithTimeout(operation, 5*time.Second)
				count, err := renew(renewContext)
				renewCancel()
				if err != nil || count != 1 {
					cancel()
					return
				}
			}
		}
	}()
	return operation, func() error {
		close(stopped)
		<-done
		err := operation.Err()
		cancel()
		if err != nil {
			return ErrLeaseLost
		}
		return nil
	}
}
