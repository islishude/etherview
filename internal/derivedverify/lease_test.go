package derivedverify

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestLeaseHeartbeatStopsBeforeFinalizedOperationReturns(t *testing.T) {
	t.Parallel()
	var renewals atomic.Int64
	renewed := make(chan struct{}, 4)
	err := runWithLeaseHeartbeat(
		t.Context(), 9*time.Millisecond,
		func(context.Context) error {
			renewals.Add(1)
			renewed <- struct{}{}
			return nil
		},
		nil,
		func(_ context.Context, guard *leaseHeartbeatGuard) error {
			<-renewed
			<-renewed
			return guard.finalize(func() error { return nil })
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	completed := renewals.Load()
	time.Sleep(12 * time.Millisecond)
	if renewals.Load() != completed {
		t.Fatalf("heartbeat renewed after finalization: before=%d after=%d", completed, renewals.Load())
	}
}

func TestLeaseHeartbeatPreventsFinalizationAfterRenewalLoss(t *testing.T) {
	t.Parallel()
	lost := errors.New("lease lost")
	var finalized atomic.Bool
	err := runWithLeaseHeartbeat(
		t.Context(), 6*time.Millisecond,
		func(context.Context) error { return lost },
		nil,
		func(ctx context.Context, guard *leaseHeartbeatGuard) error {
			<-ctx.Done()
			return guard.finalize(func() error {
				finalized.Store(true)
				return nil
			})
		},
	)
	if !errors.Is(err, lost) {
		t.Fatalf("heartbeat error = %v", err)
	}
	if finalized.Load() {
		t.Fatal("lost lease finalized its operation")
	}
}
