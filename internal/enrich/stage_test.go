package enrich

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type testJobQueue struct {
	mu       sync.Mutex
	lease    Lease
	claimed  bool
	renewed  int
	finished *StageResult
	retried  *Retry
	cancel   context.CancelFunc
}

func (queue *testJobQueue) Claim(context.Context, string, []StageID, time.Duration) (Lease, bool, error) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if queue.claimed {
		return Lease{}, false, nil
	}
	queue.claimed = true
	return queue.lease, true, nil
}

func (queue *testJobQueue) Renew(context.Context, Lease, time.Duration) error {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	queue.renewed++
	return nil
}

func (queue *testJobQueue) Finish(_ context.Context, _ Lease, result StageResult) error {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	copy := result
	queue.finished = &copy
	queue.cancel()
	return nil
}

func (queue *testJobQueue) Retry(_ context.Context, _ Lease, retry Retry) error {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	copy := retry
	queue.retried = &copy
	queue.cancel()
	return nil
}

func TestStageVersionChangesIdempotencyKey(t *testing.T) {
	t.Parallel()
	base := Job{ID: "job-1", Stage: StageID{Name: "token", Version: 1}, ChainID: "1", BlockHash: uintWord(1)}
	first, err := base.IdempotencyKey()
	if err != nil {
		t.Fatal(err)
	}
	retry := base
	retry.Attempt = 7
	second, err := retry.IdempotencyKey()
	if err != nil || first != second {
		t.Fatalf("retry key=%q err=%v; want %q", second, err, first)
	}
	upgraded := base
	upgraded.Stage.Version = 2
	third, err := upgraded.IdempotencyKey()
	if err != nil || third == first {
		t.Fatalf("versioned key=%q err=%v", third, err)
	}
}

func TestDurableWorkerWakeIsOnlyALatencyHint(t *testing.T) {
	t.Parallel()
	wake := make(chan struct{}, 1)
	wake <- struct{}{}
	started := time.Now()
	if err := waitContextOrWake(context.Background(), time.Hour, wake); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("wake was not consumed promptly: %s", elapsed)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if err := waitContextOrWake(ctx, time.Hour, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("nil/lost wake did not retain cancellable polling fallback: %v", err)
	}
}

func TestWorkerRenewsAndCompletes(t *testing.T) {
	t.Parallel()
	stage := StageID{Name: "trace", Version: 1}
	ctx, cancel := context.WithCancel(context.Background())
	queue := &testJobQueue{cancel: cancel, lease: Lease{
		Job: Job{ID: "job-1", Stage: stage, ChainID: "1", BlockHash: uintWord(1), Attempt: 1}, Token: "lease-1",
	}}
	worker, err := NewWorker(queue, []Processor{ProcessorFunc{ID: stage, Fn: func(context.Context, Job) (StageResult, error) {
		time.Sleep(18 * time.Millisecond)
		return StageResult{Details: map[string]string{"frames": "2"}}, nil
	}}}, WorkerOptions{ID: "worker-1", LeaseDuration: 30 * time.Millisecond, PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("run err=%v", err)
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if queue.finished == nil || queue.finished.State != ResultComplete || queue.finished.Details["frames"] != "2" || queue.renewed == 0 {
		t.Fatalf("finished=%+v renewed=%d", queue.finished, queue.renewed)
	}
}

func TestWorkerDistinguishesUnavailableAndRetry(t *testing.T) {
	t.Parallel()
	stage := StageID{Name: "trace", Version: 1}
	t.Run("unavailable", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		queue := &testJobQueue{cancel: cancel, lease: Lease{Job: Job{ID: "job-1", Stage: stage, ChainID: "1", BlockHash: uintWord(1), Attempt: 1}, Token: "lease"}}
		worker, err := NewWorker(queue, []Processor{ProcessorFunc{ID: stage, Fn: func(context.Context, Job) (StageResult, error) {
			return StageResult{}, Unavailable(errors.New("trace RPC disabled"))
		}}}, WorkerOptions{ID: "worker", PollInterval: time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		_ = worker.Run(ctx)
		if queue.finished == nil || queue.finished.State != ResultUnavailable || queue.retried != nil {
			t.Fatalf("finished=%+v retried=%+v", queue.finished, queue.retried)
		}
	})
	t.Run("transient", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		queue := &testJobQueue{cancel: cancel, lease: Lease{Job: Job{ID: "job-2", Stage: stage, ChainID: "1", BlockHash: uintWord(2), Attempt: 2}, Token: "lease"}}
		worker, err := NewWorker(queue, []Processor{ProcessorFunc{ID: stage, Fn: func(context.Context, Job) (StageResult, error) {
			return StageResult{}, errors.New("temporary RPC error")
		}}}, WorkerOptions{ID: "worker", RetryBase: time.Second, RetryMax: 10 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		_ = worker.Run(ctx)
		if queue.retried == nil || queue.retried.After != 2*time.Second || queue.finished != nil {
			t.Fatalf("finished=%+v retried=%+v", queue.finished, queue.retried)
		}
	})
}

func TestWorkerDelegatesRetryExhaustionToDurableQueue(t *testing.T) {
	t.Parallel()
	stage := StageID{Name: "trace", Version: 1}
	ctx, cancel := context.WithCancel(context.Background())
	queue := &testJobQueue{cancel: cancel, lease: Lease{
		Job: Job{
			ID: "durable-budget", Stage: stage, ChainID: "1",
			BlockHash: uintWord(99), Attempt: 10, Generation: 1,
		},
		Token: "lease",
	}}
	worker, err := NewWorker(queue, []Processor{ProcessorFunc{ID: stage, Fn: func(context.Context, Job) (StageResult, error) {
		return StageResult{}, errors.New("retry at durable boundary")
	}}}, WorkerOptions{ID: "worker", RetryBase: time.Millisecond, RetryMax: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	_ = worker.Run(ctx)
	if queue.retried == nil || queue.finished != nil {
		t.Fatalf("retried=%+v finished=%+v; worker must let the durable queue decide exhaustion", queue.retried, queue.finished)
	}
}
