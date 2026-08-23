package derivedverify

import (
	"context"
	"errors"
	"sync"
	"time"
)

type leaseHeartbeatGuard struct {
	mu       sync.Mutex
	lost     error
	finished bool
}

func (guard *leaseHeartbeatGuard) exclusive(operation func() error) error {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if guard.lost != nil {
		return guard.lost
	}
	if guard.finished {
		return errors.New("derived verification lease was already finalized")
	}
	return operation()
}

func (guard *leaseHeartbeatGuard) finalize(operation func() error) error {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if guard.lost != nil {
		return guard.lost
	}
	if guard.finished {
		return errors.New("derived verification lease was already finalized")
	}
	if err := operation(); err != nil {
		return err
	}
	guard.finished = true
	return nil
}

func runWithLeaseHeartbeat(
	ctx context.Context,
	duration time.Duration,
	renew func(context.Context) error,
	observer Observer,
	operation func(context.Context, *leaseHeartbeatGuard) error,
) error {
	if ctx == nil || duration < 3*time.Millisecond || renew == nil || operation == nil {
		return errors.New("derived verification lease heartbeat is invalid")
	}
	operationContext, cancel := context.WithCancel(ctx)
	defer cancel()
	guard := &leaseHeartbeatGuard{}
	renewed := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(duration / 3)
		defer ticker.Stop()
		for {
			select {
			case <-operationContext.Done():
				renewed <- nil
				return
			case <-ticker.C:
				guard.mu.Lock()
				if guard.finished {
					guard.mu.Unlock()
					renewed <- nil
					return
				}
				err := renew(operationContext)
				if err != nil {
					guard.lost = err
					guard.mu.Unlock()
					cancel()
					renewed <- err
					return
				}
				guard.mu.Unlock()
				if observer != nil {
					observer.RecordDerivedVerification(Observation{Kind: "lease", Result: "renewed"})
				}
			}
		}
	}()
	err := operation(operationContext, guard)
	cancel()
	renewErr := <-renewed
	if err != nil {
		return err
	}
	return renewErr
}
