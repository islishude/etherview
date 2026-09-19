package derivedverify

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbaccess "github.com/islishude/etherview/internal/db"
	pgx "github.com/jackc/pgx/v5"

	"github.com/google/uuid"
	dbgen "github.com/islishude/etherview/internal/db/gen"
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
	db      dbaccess.Database
	options ForwardOptions
}

func NewForwardWorker(db dbaccess.Database, options ForwardOptions) (*ForwardWorker, error) {
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
		var result int64
		result, dispatchErr = func() (int64, error) {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(lease.ChainID); err != nil {
				return 0, err
			}
			var queryValue1 pgtype.Numeric
			if err := queryValue1.Scan(lease.BlockNumber); err != nil {
				return 0, err
			}
			return dbgen.New(worker.db).DerivedVerifyDispatchTraceEvent(ctx, queryValue1, queryValue0, lease.BlockHash)
		}()
		if dispatchErr == nil {
			affected = result
		}
	case "proxy":
		var result int64
		result, dispatchErr = func() (int64, error) {
			var queryValue0 pgtype.Numeric
			if err := queryValue0.Scan(lease.ChainID); err != nil {
				return 0, err
			}
			var queryValue1 pgtype.Numeric
			if err := queryValue1.Scan(lease.BlockNumber); err != nil {
				return 0, err
			}
			return dbgen.New(worker.db).DerivedVerifyDispatchProxyEvent(ctx, queryValue0, queryValue1, lease.BlockHash)
		}()
		if dispatchErr == nil {
			affected = result
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
		result, err := func() (int64, error) {
			queryValue0, err := strconv.ParseInt(lease.ID, 10, 64)
			if err != nil {
				return 0, err
			}
			var queryValue1 pgtype.Numeric
			if err := queryValue1.Scan(lease.ChainID); err != nil {
				return 0, err
			}
			return dbgen.New(worker.db).DerivedVerifyFinishForwardBlock(ctx, dbgen.DerivedVerifyFinishForwardBlockParams{ID: int64(queryValue0), ChainID: queryValue1, BlockHash: lease.BlockHash, SourceJobID: lease.SourceJobID, SourceGeneration: lease.Generation, LeasedBy: new(lease.WorkerID), LeaseToken: new(lease.Token)})
		}()
		if err != nil {
			return err
		}
		if affected := result; affected != 1 {
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
	err := func() error {

		queryRow, err := dbgen.New(worker.db).DerivedVerifyClaimForwardBlock(ctx, new(lease.WorkerID), new(lease.Token), worker.options.LeaseDuration.Microseconds())
		if err != nil {
			return err
		}
		lease.ID = queryRow.BlockID
		lease.ChainID = queryRow.BlockChainID
		lease.BlockNumber = queryRow.BlockBlockNumber
		lease.BlockHash = queryRow.BlockHash
		if queryRow.SourceStage == nil {
			return errors.New("invalid stored query value")
		}
		lease.SourceStage = *queryRow.SourceStage
		if queryRow.SourceJobID == nil {
			return errors.New("invalid stored query value")
		}
		lease.SourceJobID = *queryRow.SourceJobID
		if queryRow.SourceGeneration == nil {
			return errors.New("invalid stored query value")
		}
		lease.Generation = *queryRow.SourceGeneration
		return nil
	}()
	if errors.Is(err, pgx.ErrNoRows) {
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
	result, err := func() (int64, error) {
		queryValue0, err := strconv.ParseInt(lease.ID, 10, 64)
		if err != nil {
			return 0, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(lease.ChainID); err != nil {
			return 0, err
		}
		return dbgen.New(worker.db).DerivedVerifyRenewForwardEvent(ctx, dbgen.DerivedVerifyRenewForwardEventParams{ID: int64(queryValue0), ChainID: queryValue1, BlockHash: lease.BlockHash, SourceJobID: lease.SourceJobID, SourceGeneration: lease.Generation, LeasedBy: new(lease.WorkerID), LeaseToken: new(lease.Token), LeaseMicroseconds: worker.options.LeaseDuration.Microseconds()})
	}()
	if err != nil {
		return err
	}
	if affected := result; affected != 1 {
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
	result, err := func() (int64, error) {
		queryValue0, err := strconv.ParseInt(lease.ID, 10, 64)
		if err != nil {
			return 0, err
		}
		var queryValue1 pgtype.Numeric
		if err := queryValue1.Scan(lease.ChainID); err != nil {
			return 0, err
		}
		var queryValue2 pgtype.Numeric
		if err := queryValue2.Scan(lease.BlockNumber); err != nil {
			return 0, err
		}
		return dbgen.New(worker.db).DerivedVerifyRetryForwardBlock(ctx, dbgen.DerivedVerifyRetryForwardBlockParams{ID: int64(queryValue0), ChainID: queryValue1, BlockNumber: queryValue2, BlockHash: lease.BlockHash, SourceJobID: lease.SourceJobID, SourceGeneration: lease.Generation, LeasedBy: new(lease.WorkerID), LastError: new("dispatch_failed")})
	}()
	if err != nil {
		return err
	}
	if affected := result; affected != 1 {
		return errors.New("retry derived forward block: lease lost")
	}
	return nil
}
