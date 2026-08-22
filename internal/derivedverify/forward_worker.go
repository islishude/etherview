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
		len(options.WorkerID) > 128 || options.LeaseDuration < time.Millisecond ||
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
	ChainID     string
	BlockNumber string
	BlockHash   []byte
	WorkerID    string
	Token       string
}

func (worker *ForwardWorker) ProcessOne(ctx context.Context) (bool, error) {
	lease, found, err := worker.claim(ctx)
	if err != nil || !found {
		return found, err
	}
	if _, err := worker.db.ExecContext(ctx, dbgen.DerivedVerifyDispatchForwardBlock,
		lease.ChainID, lease.BlockNumber, lease.BlockHash,
	); err != nil {
		worker.retry(ctx, lease) //nolint:errcheck
		return true, err
	}
	result, err := worker.db.ExecContext(ctx, dbgen.DerivedVerifyFinishForwardBlock,
		lease.ChainID, lease.BlockHash, lease.WorkerID, lease.Token,
	)
	if err != nil {
		return true, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return true, errors.New("derived forward block lease was lost")
	}
	return true, nil
}

func (worker *ForwardWorker) claim(ctx context.Context) (forwardLease, bool, error) {
	lease := forwardLease{WorkerID: worker.options.WorkerID, Token: uuid.NewString()}
	err := worker.db.QueryRowContext(ctx, dbgen.DerivedVerifyClaimForwardBlock,
		lease.WorkerID, lease.Token, worker.options.LeaseDuration.Microseconds(),
	).Scan(&lease.ChainID, &lease.BlockNumber, &lease.BlockHash)
	if errors.Is(err, sql.ErrNoRows) {
		return forwardLease{}, false, nil
	}
	if err != nil {
		return forwardLease{}, false, err
	}
	if lease.ChainID == "" || lease.BlockNumber == "" || len(lease.BlockHash) != 32 {
		return forwardLease{}, false, errors.New("stored derived forward block is invalid")
	}
	return lease, true, nil
}

func (worker *ForwardWorker) retry(ctx context.Context, lease forwardLease) error {
	result, err := worker.db.ExecContext(ctx, dbgen.DerivedVerifyRetryForwardBlock,
		lease.ChainID, lease.BlockNumber, lease.BlockHash,
		lease.WorkerID, "dispatch_failed",
	)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return errors.New("retry derived forward block: lease lost")
	}
	return nil
}
