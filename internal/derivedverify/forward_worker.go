package derivedverify

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/islishude/etherview/internal/db/gen"
)

type ForwardOptions struct {
	WorkerID      string
	LeaseDuration time.Duration
	PollInterval  time.Duration
	Observer      Observer
}

func (options *ForwardOptions) defaults() {
	if options.LeaseDuration <= 0 {
		options.LeaseDuration = 30 * time.Second
	}
	if options.PollInterval <= 0 {
		options.PollInterval = time.Second
	}
}

type ForwardWorker struct {
	db      *sql.DB
	options ForwardOptions
}

func NewForwardWorker(db *sql.DB, options ForwardOptions) (*ForwardWorker, error) {
	options.defaults()
	if db == nil || strings.TrimSpace(options.WorkerID) == "" ||
		len(options.WorkerID) > 128 || options.LeaseDuration < 3*time.Millisecond ||
		options.PollInterval <= 0 {
		return nil, errors.New("derived forward worker configuration is invalid")
	}
	return &ForwardWorker{db: db, options: options}, nil
}

func (worker *ForwardWorker) Name() string { return "factory-derived-forward-worker" }

func (worker *ForwardWorker) Run(ctx context.Context) error {
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

type forwardLease struct {
	ID          string
	ChainID     string
	BlockNumber string
	BlockHash   []byte
	SourceStage string
	SourceJobID int64
	Generation  int64
	WorkerID    string
	Token       string
}

func (worker *ForwardWorker) ProcessOne(ctx context.Context) (bool, error) {
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

func (worker *ForwardWorker) processLease(
	ctx context.Context,
	guard *leaseHeartbeatGuard,
	lease forwardLease,
) error {
	var dispatchErr error
	var affected int64
	switch lease.SourceStage {
	case "trace":
		var result sql.Result
		result, dispatchErr = worker.db.ExecContext(ctx, dbgen.DerivedVerifyDispatchTraceEvent,
			lease.ChainID, lease.BlockNumber, lease.BlockHash,
		)
		if dispatchErr == nil {
			affected, dispatchErr = result.RowsAffected()
		}
	case "proxy":
		var result sql.Result
		result, dispatchErr = worker.db.ExecContext(ctx, dbgen.DerivedVerifyDispatchProxyEvent,
			lease.ChainID, lease.BlockNumber, lease.BlockHash,
		)
		if dispatchErr == nil {
			affected, dispatchErr = result.RowsAffected()
		}
	default:
		dispatchErr = errors.New("derived forward event stage is invalid")
	}
	if dispatchErr != nil {
		return worker.failLease(ctx, guard, lease, dispatchErr)
	}
	worker.observe("dispatch", lease.SourceStage+"_generation")
	if affected > 0 {
		result := "trace"
		if lease.SourceStage == "proxy" {
			result = "pending_runtime"
		}
		worker.observe("rewind", result)
	}
	return guard.finalize(func() error {
		if err := worker.renew(ctx, lease); err != nil {
			return err
		}
		result, err := worker.db.ExecContext(ctx, dbgen.DerivedVerifyFinishForwardBlock,
			lease.ID, lease.ChainID, lease.BlockHash, lease.SourceJobID,
			lease.Generation, lease.WorkerID, lease.Token,
		)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			worker.observe("lease", "lost")
			return errors.New("derived forward event lease was lost")
		}
		return nil
	})
}

func (worker *ForwardWorker) observe(kind, result string) {
	if worker.options.Observer != nil {
		worker.options.Observer.RecordDerivedVerification(Observation{Kind: kind, Result: result})
	}
}

func (worker *ForwardWorker) claim(ctx context.Context) (forwardLease, bool, error) {
	lease := forwardLease{WorkerID: worker.options.WorkerID, Token: uuid.NewString()}
	err := worker.db.QueryRowContext(ctx, dbgen.DerivedVerifyClaimForwardBlock,
		lease.WorkerID, lease.Token, worker.options.LeaseDuration.Microseconds(),
	).Scan(
		&lease.ID, &lease.ChainID, &lease.BlockNumber, &lease.BlockHash,
		&lease.SourceStage, &lease.SourceJobID, &lease.Generation,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return forwardLease{}, false, nil
	}
	if err != nil {
		return forwardLease{}, false, err
	}
	if lease.ID == "" || lease.ChainID == "" || lease.BlockNumber == "" ||
		len(lease.BlockHash) != 32 ||
		(lease.SourceStage != "trace" && lease.SourceStage != "proxy") ||
		lease.SourceJobID <= 0 || lease.Generation <= 0 {
		return forwardLease{}, false, errors.New("stored derived forward block is invalid")
	}
	return lease, true, nil
}

func (worker *ForwardWorker) renew(ctx context.Context, lease forwardLease) error {
	result, err := worker.db.ExecContext(ctx, dbgen.DerivedVerifyRenewForwardEvent,
		lease.ID, lease.ChainID, lease.BlockHash, lease.SourceJobID,
		lease.Generation, lease.WorkerID, lease.Token,
		worker.options.LeaseDuration.Microseconds(),
	)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		worker.observe("lease", "lost")
		return errors.New("derived forward event lease was lost")
	}
	return nil
}

func (worker *ForwardWorker) failLease(
	ctx context.Context,
	guard *leaseHeartbeatGuard,
	lease forwardLease,
	cause error,
) error {
	retryErr := guard.finalize(func() error { return worker.retry(ctx, lease) })
	return errors.Join(cause, retryErr)
}

func (worker *ForwardWorker) retry(ctx context.Context, lease forwardLease) error {
	result, err := worker.db.ExecContext(ctx, dbgen.DerivedVerifyRetryForwardBlock,
		lease.ID, lease.ChainID, lease.BlockNumber, lease.BlockHash,
		lease.SourceJobID, lease.Generation, lease.WorkerID, "dispatch_failed",
	)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return errors.New("retry derived forward block: lease lost")
	}
	return nil
}
