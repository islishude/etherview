package verify

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type wakeProbeRepository struct {
	verifyMemoryRepository
	calls chan struct{}
	count atomic.Int32
}

func (r *wakeProbeRepository) ClaimRunnable(context.Context, string, time.Duration, CompilerAvailability) (VerificationLease, bool, error) {
	r.count.Add(1)
	select {
	case r.calls <- struct{}{}:
	default:
	}
	return VerificationLease{}, false, nil
}
func TestWorkerWakeAndPollingFallback(t *testing.T) {
	for _, notifications := range []bool{false, true} {
		t.Run(map[bool]string{false: "PostgreSQL polling", true: "NATS hint"}[notifications], func(t *testing.T) {
			repository := &wakeProbeRepository{calls: make(chan struct{}, 4)}
			var wake chan struct{}
			interval := 5 * time.Millisecond
			if notifications {
				wake = make(chan struct{}, 1)
				interval = time.Hour
			}
			worker, err := NewWorker(repository, &verifyTestCompiler{}, WorkerOptions{WorkerID: "wake-test", PollInterval: interval, Wake: wake})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- worker.Run(ctx) }()
			await := func() {
				t.Helper()
				select {
				case <-repository.calls:
				case <-time.After(time.Second):
					t.Fatal("worker did not poll")
				}
			}
			await()
			if notifications {
				wake <- struct{}{}
			}
			await()
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("shutdown: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("worker did not stop")
			}
		})
	}
}
