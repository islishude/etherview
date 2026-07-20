package maintenance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type WorkerOptions struct {
	ServiceName  string
	WorkerID     string
	PollInterval time.Duration
}

func (options *WorkerOptions) defaults() {
	if options.ServiceName == "" {
		options.ServiceName = "maintenance-worker"
	}
	if options.PollInterval <= 0 {
		options.PollInterval = time.Second
	}
}

// Worker is shared by monolith and split maintenance roles. It only dispatches
// validated ranges; concrete index repair/reindex semantics are injected via
// RangeExecutor so both deployment forms call the same implementation.
type Worker struct {
	repository Repository
	executor   RangeExecutor
	options    WorkerOptions
}

func NewWorker(repository Repository, executor RangeExecutor, options WorkerOptions) (*Worker, error) {
	if repository == nil {
		return nil, errors.New("maintenance worker requires a repository")
	}
	if executor == nil {
		return nil, errors.New("maintenance worker requires a range executor")
	}
	options.defaults()
	options.ServiceName = strings.TrimSpace(options.ServiceName)
	options.WorkerID = strings.TrimSpace(options.WorkerID)
	if options.ServiceName == "" || options.WorkerID == "" {
		return nil, errors.New("maintenance service and worker IDs are required")
	}
	if len(options.ServiceName) > 128 || len(options.WorkerID) > 128 {
		return nil, errors.New("maintenance service or worker ID exceeds 128 bytes")
	}
	if options.PollInterval <= 0 {
		return nil, errors.New("maintenance poll interval must be positive")
	}
	return &Worker{repository: repository, executor: executor, options: options}, nil
}

func (worker *Worker) Name() string {
	if worker == nil || worker.options.ServiceName == "" {
		return "maintenance-worker"
	}
	return worker.options.ServiceName
}

func (worker *Worker) Run(ctx context.Context) error {
	if worker == nil || worker.repository == nil || worker.executor == nil {
		return errors.New("run nil maintenance worker")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		found, err := worker.ProcessOne(ctx)
		if err != nil {
			return err
		}
		if !found {
			if err := waitForContext(ctx, worker.options.PollInterval); err != nil {
				return err
			}
		}
	}
}

func (worker *Worker) ProcessOne(ctx context.Context) (bool, error) {
	if worker == nil || worker.repository == nil || worker.executor == nil {
		return false, errors.New("process using nil maintenance worker")
	}
	lease, found, err := worker.repository.Claim(ctx, worker.options.WorkerID)
	if err != nil || !found {
		return found, err
	}
	if err := lease.Request.Validate(); err != nil {
		cleanupCtx, cancel := leaseCleanupContext(ctx)
		defer cancel()
		_ = worker.repository.Release(cleanupCtx, lease)
		return true, fmt.Errorf("repository returned invalid maintenance request: %w", err)
	}
	leaseOpen := true
	defer func() {
		if leaseOpen {
			cleanupCtx, cancel := leaseCleanupContext(ctx)
			defer cancel()
			_ = worker.repository.Release(cleanupCtx, lease)
		}
	}()

	if err := worker.repository.GuardFinalized(ctx, lease); err != nil {
		if errors.Is(err, ErrFinalizedRange) {
			recordCtx, cancel := leaseCleanupContext(ctx)
			defer cancel()
			if failErr := worker.repository.Fail(recordCtx, lease, err); failErr != nil {
				return true, fmt.Errorf("record finalized-range rejection: %w", failErr)
			}
			leaseOpen = false
			return true, nil
		}
		return true, fmt.Errorf("guard maintenance finality: %w", err)
	}

	executionErr := worker.execute(ctx, lease.Request)
	if executionErr != nil {
		if ctx.Err() != nil {
			return true, ctx.Err()
		}
		recordCtx, cancel := leaseCleanupContext(ctx)
		defer cancel()
		if err := worker.repository.Fail(recordCtx, lease, executionErr); err != nil {
			return true, fmt.Errorf("record maintenance failure: %w", err)
		}
		leaseOpen = false
		return true, nil
	}

	recordCtx, cancel := leaseCleanupContext(ctx)
	defer cancel()
	if err := worker.repository.Complete(recordCtx, lease); err != nil {
		return true, fmt.Errorf("complete maintenance request: %w", err)
	}
	leaseOpen = false
	return true, nil
}

func (worker *Worker) execute(ctx context.Context, request Request) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("maintenance range executor panicked")
		}
	}()
	switch request.Operation {
	case OperationRepair:
		return worker.executor.Repair(ctx, request)
	case OperationReindex:
		return worker.executor.Reindex(ctx, request)
	default:
		return fmt.Errorf("%w: unsupported operation %q", ErrInvalidRequest, request.Operation)
	}
}

func waitForContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
