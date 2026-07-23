package maintenance

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type memoryMaintenanceRepository struct {
	mu          sync.Mutex
	lease       Lease
	found       bool
	claimErr    error
	guardErr    error
	completeErr error
	failErr     error
	releaseErr  error
	workerID    string
	completed   int
	failed      int
	released    int
	failure     error
}

func (repository *memoryMaintenanceRepository) Claim(_ context.Context, workerID string) (Lease, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.workerID = workerID
	return repository.lease, repository.found, repository.claimErr
}

func (repository *memoryMaintenanceRepository) GuardFinalized(context.Context, Lease) error {
	return repository.guardErr
}

func (repository *memoryMaintenanceRepository) Complete(context.Context, Lease) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.completed++
	return repository.completeErr
}

func (repository *memoryMaintenanceRepository) Fail(_ context.Context, _ Lease, cause error) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.failed++
	repository.failure = cause
	return repository.failErr
}

func (repository *memoryMaintenanceRepository) Release(context.Context, Lease) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.released++
	return repository.releaseErr
}

type memoryRangeExecutor struct {
	mu              sync.Mutex
	repairRequests  []Request
	reindexRequests []Request
	repairErr       error
	reindexErr      error
	panicRepair     bool
}

type recordingRequestObserver struct {
	operation string
	result    string
}

func (observer *recordingRequestObserver) RecordMaintenanceRequest(operation, result string) {
	observer.operation, observer.result = operation, result
}

func (executor *memoryRangeExecutor) Repair(_ context.Context, request Request) error {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	executor.repairRequests = append(executor.repairRequests, request)
	if executor.panicRepair {
		panic("executor secret must not enter last_error")
	}
	return executor.repairErr
}

func (executor *memoryRangeExecutor) Reindex(_ context.Context, request Request) error {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	executor.reindexRequests = append(executor.reindexRequests, request)
	return executor.reindexErr
}

func TestWorkerDispatchesRepairAndReindexBoundaries(t *testing.T) {
	t.Parallel()
	for _, operation := range []Operation{OperationRepair, OperationReindex} {
		t.Run(string(operation), func(t *testing.T) {
			t.Parallel()
			request := validRequest()
			request.Operation = operation
			if operation == OperationReindex {
				request.Stage = "token"
			}
			repository := &memoryMaintenanceRepository{lease: Lease{Request: request}, found: true}
			executor := &memoryRangeExecutor{}
			worker, err := NewWorker(repository, executor, WorkerOptions{WorkerID: "worker-a"})
			if err != nil {
				t.Fatal(err)
			}
			found, err := worker.ProcessOne(context.Background())
			if err != nil || !found {
				t.Fatalf("found=%v error=%v", found, err)
			}
			if repository.workerID != "worker-a" || repository.completed != 1 || repository.failed != 0 || repository.released != 0 {
				t.Fatalf("repository=%+v", repository)
			}
			if operation == OperationRepair {
				if len(executor.repairRequests) != 1 || len(executor.reindexRequests) != 0 || executor.repairRequests[0] != request {
					t.Fatalf("executor=%+v", executor)
				}
			} else if len(executor.reindexRequests) != 1 || len(executor.repairRequests) != 0 || executor.reindexRequests[0] != request {
				t.Fatalf("executor=%+v", executor)
			}
		})
	}
}

func TestWorkerObservesPersistedMaintenanceOutcome(t *testing.T) {
	repository := &memoryMaintenanceRepository{lease: Lease{Request: validRequest()}, found: true}
	observer := &recordingRequestObserver{}
	worker, err := NewWorker(repository, &memoryRangeExecutor{}, WorkerOptions{WorkerID: "worker", Observer: observer})
	if err != nil {
		t.Fatal(err)
	}
	if found, err := worker.ProcessOne(t.Context()); err != nil || !found {
		t.Fatalf("found=%t error=%v", found, err)
	}
	if observer.operation != "repair" || observer.result != "succeeded" {
		t.Fatalf("maintenance observation operation=%q result=%q", observer.operation, observer.result)
	}
}

func TestWorkerRecordsExecutorFailureAndPanic(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		executor  *memoryRangeExecutor
		wantError string
	}{
		{"error", &memoryRangeExecutor{repairErr: errors.New("range fetch failed")}, "range fetch failed"},
		{"panic", &memoryRangeExecutor{panicRepair: true}, "maintenance range executor panicked"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &memoryMaintenanceRepository{lease: Lease{Request: validRequest()}, found: true}
			worker, err := NewWorker(repository, test.executor, WorkerOptions{WorkerID: "worker"})
			if err != nil {
				t.Fatal(err)
			}
			found, err := worker.ProcessOne(context.Background())
			if err != nil || !found {
				t.Fatalf("found=%v error=%v", found, err)
			}
			if repository.failed != 1 || repository.completed != 0 || repository.released != 0 || repository.failure == nil || repository.failure.Error() != test.wantError {
				t.Fatalf("repository=%+v", repository)
			}
		})
	}
}

func TestWorkerRejectsFinalizedRangeBeforeExecutor(t *testing.T) {
	t.Parallel()
	guardErr := errors.Join(ErrFinalizedRange, errors.New("through block 50"))
	repository := &memoryMaintenanceRepository{
		lease: Lease{Request: validRequest()}, found: true, guardErr: guardErr,
	}
	executor := &memoryRangeExecutor{}
	worker, err := NewWorker(repository, executor, WorkerOptions{WorkerID: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	found, err := worker.ProcessOne(context.Background())
	if err != nil || !found {
		t.Fatalf("found=%v error=%v", found, err)
	}
	if repository.failed != 1 || !errors.Is(repository.failure, ErrFinalizedRange) || len(executor.repairRequests) != 0 {
		t.Fatalf("repository=%+v executor=%+v", repository, executor)
	}
}

func TestWorkerReleasesLeaseOnCancellationOrInfrastructureError(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		guardErr error
		cancel   bool
	}{
		{"guard", errors.New("database unavailable"), false},
		{"cancel", nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &memoryMaintenanceRepository{lease: Lease{Request: validRequest()}, found: true, guardErr: test.guardErr}
			executor := &memoryRangeExecutor{}
			if test.cancel {
				executor.repairErr = context.Canceled
			}
			worker, err := NewWorker(repository, executor, WorkerOptions{WorkerID: "worker"})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if test.cancel {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			found, err := worker.ProcessOne(ctx)
			if !found || err == nil || repository.released != 1 || repository.failed != 0 || repository.completed != 0 {
				t.Fatalf("found=%v error=%v repository=%+v", found, err, repository)
			}
		})
	}
}

func TestWorkerRunPollsWithoutOptionalInfrastructure(t *testing.T) {
	t.Parallel()
	repository := &memoryMaintenanceRepository{}
	worker, err := NewWorker(repository, &memoryRangeExecutor{}, WorkerOptions{WorkerID: "worker", PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err = worker.Run(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
}

func TestNewWorkerValidatesDependenciesAndIdentity(t *testing.T) {
	t.Parallel()
	repository := &memoryMaintenanceRepository{}
	executor := &memoryRangeExecutor{}
	for _, test := range []struct {
		repository Repository
		executor   RangeExecutor
		options    WorkerOptions
	}{
		{nil, executor, WorkerOptions{WorkerID: "worker"}},
		{repository, nil, WorkerOptions{WorkerID: "worker"}},
		{repository, executor, WorkerOptions{}},
		{repository, executor, WorkerOptions{WorkerID: string(make([]byte, 129))}},
	} {
		if _, err := NewWorker(test.repository, test.executor, test.options); err == nil {
			t.Fatalf("configuration=%+v was accepted", test.options)
		}
	}
}

func validRequest() Request {
	return Request{
		ID: 7, ChainID: "1", Operation: OperationRepair, Stage: "core",
		FromBlock: 100, ToBlock: 199, Reason: "operator requested gap repair",
	}
}
